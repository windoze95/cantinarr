package instance

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/windoze95/cantinarr-server/internal/hardcover"
)

var errHardcoverReconnect = errors.New("Hardcover sign-in has expired; reconnect it in the instance settings")
var errHardcoverStorage = errors.New("could not save the Hardcover connection; try again")
var errHardcoverFlowMissing = errors.New("Hardcover sign-in was not found or the server restarted; start again")

// hardcoverOAuthProvider keeps provider protocol tests independent of storage.
type hardcoverOAuthProvider interface {
	Begin(context.Context) (hardcover.DeviceCode, error)
	Poll(context.Context, string) (hardcover.OAuthTokens, error)
	Refresh(context.Context, string) (hardcover.OAuthTokens, error)
}

type HardcoverDeviceResult struct {
	FlowID          string    `json:"flow_id"`
	Status          string    `json:"status"`
	UserCode        string    `json:"user_code,omitempty"`
	VerificationURI string    `json:"verification_uri,omitempty"`
	ExpiresAt       time.Time `json:"expires_at"`
	Interval        int64     `json:"interval"`
	ConnectionID    string    `json:"connection_id,omitempty"`
	Error           string    `json:"error,omitempty"`
}

type hardcoverFlow struct {
	result     HardcoverDeviceResult
	actor      int64
	instance   string
	revision   int64
	deviceCode string
	nextPoll   time.Time
	polling    bool
	candidate  *hardcover.OAuthTokens
	verified   bool
}

type hardcoverRefresh struct {
	mu      sync.Mutex
	pending *hardcoverCredential
	retryAt time.Time
}

// HardcoverManager owns this process's unfinished device flows and shared
// refresh serialization. Unfinished flows intentionally do not survive restart.
// Committed OAuth records do, using the same cipher as other instance secrets.
type HardcoverManager struct {
	store     *Store
	provider  hardcoverOAuthProvider
	verify    func(context.Context, string) error
	now       func() time.Time
	changed   func(string)
	mu        sync.Mutex
	flows     map[string]*hardcoverFlow
	refreshes map[string]*hardcoverRefresh
	persist   func(string, hardcoverCredential) error
}

func newHardcoverManager(s *Store) *HardcoverManager {
	return &HardcoverManager{store: s, provider: hardcover.NewOAuthClient(), verify: hardcover.NewClient().VerifyCatalog,
		now: time.Now, flows: make(map[string]*hardcoverFlow), refreshes: make(map[string]*hardcoverRefresh), persist: s.updateHardcoverCredential}
}

func (m *HardcoverManager) purgeLocked() {
	for id, f := range m.flows {
		if !m.now().Before(f.result.ExpiresAt.Add(5 * time.Minute)) {
			delete(m.flows, id)
		}
	}
}

func finishHardcoverFlow(f *hardcoverFlow, status, message string) {
	f.result.Status = status
	f.result.Error = message
	f.deviceCode = ""
	f.candidate = nil
	f.polling = false
}

func (m *HardcoverManager) Begin(ctx context.Context, actor int64, id string) (HardcoverDeviceResult, error) {
	m.mu.Lock()
	m.purgeLocked()
	if len(m.flows) >= 100 {
		m.mu.Unlock()
		return HardcoverDeviceResult{}, hardcover.ErrProvider
	}
	revision, err := m.store.beginHardcoverChange(id)
	if err != nil {
		m.mu.Unlock()
		return HardcoverDeviceResult{}, err
	}
	// Other actors may replace a connection, but can never read each other's codes.
	for _, f := range m.flows {
		if f.instance == id && f.result.Status == "pending" {
			finishHardcoverFlow(f, "superseded", errHardcoverChanged.Error())
		}
	}
	f := &hardcoverFlow{actor: actor, instance: id, revision: revision, polling: true,
		result: HardcoverDeviceResult{FlowID: uuid.NewString(), Status: "pending", ExpiresAt: m.now().Add(15 * time.Minute), Interval: 5}}
	m.flows[f.result.FlowID] = f
	m.mu.Unlock()
	code, err := m.provider.Begin(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if f.result.Status != "pending" {
		return f.result, nil
	}
	if err != nil {
		finishHardcoverFlow(f, "failed", hardcover.ErrProvider.Error())
		return f.result, err
	}
	state, stateErr := m.store.HardcoverState(id)
	if stateErr != nil || state.Revision != revision {
		finishHardcoverFlow(f, "superseded", errHardcoverChanged.Error())
		return f.result, nil
	}
	f.deviceCode = code.DeviceCode
	f.result.UserCode = code.UserCode
	f.result.VerificationURI = code.VerificationURI
	f.result.ExpiresAt = m.now().Add(time.Duration(code.ExpiresIn) * time.Second)
	f.result.Interval = code.Interval
	f.nextPoll = m.now().Add(time.Duration(code.Interval) * time.Second)
	f.polling = false
	return f.result, nil
}

func (m *HardcoverManager) flowLocked(actor int64, id, flowID string) (*hardcoverFlow, error) {
	f, ok := m.flows[flowID]
	if !ok || f.actor != actor || f.instance != id {
		return nil, errHardcoverFlowMissing
	}
	state, err := m.store.HardcoverState(id)
	if err != nil && !errors.Is(err, errHardcoverMissing) {
		return nil, errHardcoverStorage
	}
	if f.result.Status == "connected" {
		if err != nil || state.ConnectionID != f.result.ConnectionID {
			finishHardcoverFlow(f, "superseded", errHardcoverChanged.Error())
			f.result.ConnectionID = ""
		}
	} else if f.result.Status == "pending" {
		if err != nil || state.Revision != f.revision {
			finishHardcoverFlow(f, "superseded", errHardcoverChanged.Error())
		} else if !m.now().Before(f.result.ExpiresAt) {
			finishHardcoverFlow(f, "expired", "That code expired. Start again.")
		}
	}
	return f, nil
}

func (m *HardcoverManager) Check(ctx context.Context, actor int64, id, flowID string) (HardcoverDeviceResult, error) {
	m.mu.Lock()
	f, err := m.flowLocked(actor, id, flowID)
	if err != nil {
		m.mu.Unlock()
		return HardcoverDeviceResult{}, err
	}
	if f.result.Status != "pending" || f.polling || m.now().Before(f.nextPoll) {
		result := f.result
		m.mu.Unlock()
		return result, nil
	}
	f.polling = true
	f.nextPoll = m.now().Add(time.Duration(f.result.Interval) * time.Second)
	code := f.deviceCode
	var candidate hardcover.OAuthTokens
	haveCandidate := f.candidate != nil
	if haveCandidate {
		candidate = *f.candidate
	}
	verified := f.verified
	m.mu.Unlock()
	if !haveCandidate {
		candidate, err = m.provider.Poll(ctx, code)
	}
	// Retain an issued token pair before catalog verification or storage. Once
	// issued, the device code must never be replayed, even after a timeout.
	m.mu.Lock()
	f.polling = false
	if err == nil && f.result.Status == "pending" {
		f.candidate = &candidate
	}
	if _, checkErr := m.flowLocked(actor, id, flowID); checkErr != nil {
		m.mu.Unlock()
		return HardcoverDeviceResult{}, checkErr
	}
	if f.result.Status != "pending" {
		result := f.result
		m.mu.Unlock()
		return result, nil
	}
	if err != nil {
		switch {
		case errors.Is(err, hardcover.ErrPending):
			f.result.Error = ""
		case errors.Is(err, hardcover.ErrSlowDown):
			f.result.Interval += 5
			f.result.Error = "Hardcover asked us to wait. Checking again shortly."
		case errors.Is(err, hardcover.ErrDenied):
			finishHardcoverFlow(f, "denied", "Hardcover sign-in was declined. Start again when you are ready.")
		case errors.Is(err, hardcover.ErrExpired), errors.Is(err, hardcover.ErrInvalidGrant):
			finishHardcoverFlow(f, "expired", "That code expired. Start again.")
		case errors.Is(err, hardcover.ErrInsufficientScope):
			finishHardcoverFlow(f, "failed", hardcover.ErrInsufficientScope.Error())
		default:
			f.result.Interval = max(f.result.Interval, min(f.result.Interval*2, 300))
			f.result.Error = hardcover.ErrProvider.Error()
		}
		f.nextPoll = m.now().Add(time.Duration(f.result.Interval) * time.Second)
		result := f.result
		m.mu.Unlock()
		return result, nil
	}
	f.candidate = &candidate
	f.polling = true
	m.mu.Unlock()
	if !verified {
		err = m.verify(ctx, candidate.AccessToken)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	f.polling = false
	if _, checkErr := m.flowLocked(actor, id, flowID); checkErr != nil {
		return HardcoverDeviceResult{}, checkErr
	}
	if f.result.Status != "pending" {
		return f.result, nil
	}
	if err != nil {
		if errors.Is(err, hardcover.ErrUnauthorized) || errors.Is(err, hardcover.ErrInsufficientScope) {
			finishHardcoverFlow(f, "failed", err.Error())
		} else {
			f.result.Error = "Could not verify the Hardcover catalog. Retrying; your existing connection is still in place."
		}
		f.nextPoll = m.now().Add(time.Duration(f.result.Interval) * time.Second)
		return f.result, nil
	}
	f.verified = true
	connectionID, err := m.store.connectHardcoverOAuth(id, f.revision, candidate)
	if errors.Is(err, errHardcoverChanged) {
		finishHardcoverFlow(f, "superseded", err.Error())
		return f.result, nil
	}
	if err != nil {
		f.result.Error = errHardcoverStorage.Error()
		return f.result, nil
	}
	finishHardcoverFlow(f, "connected", "")
	f.result.ConnectionID = connectionID
	if m.changed != nil {
		m.changed(id)
	}
	return f.result, nil
}

func (m *HardcoverManager) Cancel(actor int64, id, flowID string) (HardcoverDeviceResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, err := m.flowLocked(actor, id, flowID)
	if err != nil {
		return HardcoverDeviceResult{}, err
	}
	if f.result.Status == "pending" {
		finishHardcoverFlow(f, "cancelled", "Hardcover sign-in was cancelled.")
	}
	// If the commit won the race, report connected so the app refreshes status.
	return f.result, nil
}

func (m *HardcoverManager) refreshLock(id string) *hardcoverRefresh {
	m.mu.Lock()
	defer m.mu.Unlock()
	cell := m.refreshes[id]
	if cell == nil {
		cell = &hardcoverRefresh{}
		m.refreshes[id] = cell
	}
	return cell
}

// Token is called only on a feed cache miss. rejected is the access token
// just rejected by a catalog read, or empty on the first attempt. Comparing it
// avoids a second rotation if another instance has already renewed the pair.
func (m *HardcoverManager) Token(ctx context.Context, id, rejected string) (string, error) {
	state, err := m.store.HardcoverState(id)
	if err != nil {
		return "", errHardcoverStorage
	}
	if state.Method != "oauth" {
		return m.store.HardcoverToken(id)
	}
	cell := m.refreshLock(state.ConnectionID)
	cell.mu.Lock()
	defer cell.mu.Unlock()
	c, err := m.store.hardcoverCredential(state.ConnectionID)
	if err != nil {
		if err == sql.ErrNoRows {
			cell.pending = nil
		}
		return "", errHardcoverStorage
	}
	if cell.pending != nil {
		if cell.pending.Revision != c.Revision {
			cell.pending = nil
		} else {
			if err = m.persist(state.ConnectionID, *cell.pending); err != nil {
				return "", errHardcoverStorage
			}
			c = *cell.pending
			c.Revision++
			cell.pending = nil
			if c.Reconnect {
				m.invalidateConnection(state.ConnectionID)
			}
		}
	}
	if c.Reconnect {
		return "", errHardcoverReconnect
	}
	// The margin covers both catalog calls; short tokens still get a useful
	// lifetime, and a cache hit never spends a refresh-token rotation.
	if m.now().Add(30*time.Second).Before(c.Tokens.ExpiresAt) && (rejected == "" || c.Tokens.AccessToken != rejected) {
		return c.Tokens.AccessToken, nil
	}
	if m.now().Before(cell.retryAt) {
		return "", hardcover.ErrProvider
	}
	if c.Tokens.RefreshToken == "" {
		c.Reconnect = true
	} else {
		tokens, refreshErr := m.provider.Refresh(ctx, c.Tokens.RefreshToken)
		if refreshErr != nil {
			if !errors.Is(refreshErr, hardcover.ErrInvalidGrant) {
				cell.retryAt = m.now().Add(30 * time.Second)
				return "", hardcover.ErrProvider
			}
			c.Reconnect = true
		} else {
			if tokens.RefreshToken == "" {
				tokens.RefreshToken = c.Tokens.RefreshToken
			}
			c.Tokens = tokens
		}
	}
	// Holding the successor in memory prevents reusing a consumed predecessor
	// if encryption/SQLite is temporarily unavailable. Subsequent calls persist
	// this exact pair before doing any more provider traffic.
	cell.pending = &c
	if err = m.persist(state.ConnectionID, c); err != nil {
		return "", errHardcoverStorage
	}
	cell.pending = nil
	if c.Reconnect {
		m.invalidateConnection(state.ConnectionID)
		return "", errHardcoverReconnect
	}
	// Disconnect or replacement during refresh must not lend this credential
	// to an instance that has moved to another connection.
	latest, err := m.store.HardcoverState(id)
	if err != nil || latest.ConnectionID != state.ConnectionID {
		return "", errHardcoverChanged
	}
	return c.Tokens.AccessToken, nil
}

func (m *HardcoverManager) invalidateConnection(connectionID string) {
	rows, err := m.store.db.Query("SELECT instance_id FROM hardcover_instance_connections WHERE connection_id=?", connectionID)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if m.changed != nil {
			m.changed(id)
		}
	}
}

// ForgetUnlinked discards any memory-held successor after the final link is
// deleted. Locks are kept as harmless synchronization objects so an in-flight
// refresh and a later caller can never acquire two locks for one connection.
func (m *HardcoverManager) ForgetUnlinked() {
	m.mu.Lock()
	for _, f := range m.flows {
		_, _ = m.flowLocked(f.actor, f.instance, f.result.FlowID)
	}
	cells := make(map[string]*hardcoverRefresh, len(m.refreshes))
	for id, cell := range m.refreshes {
		cells[id] = cell
	}
	m.mu.Unlock()
	for id, cell := range cells {
		cell.mu.Lock()
		var exists int
		if err := m.store.db.QueryRow("SELECT 1 FROM hardcover_connections WHERE id=?", id).Scan(&exists); err == sql.ErrNoRows {
			cell.pending = nil
		}
		cell.mu.Unlock()
	}
}
