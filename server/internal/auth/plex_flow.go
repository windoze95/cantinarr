package auth

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
	"github.com/windoze95/cantinarr-server/internal/plex"
)

// SetPlexBaseURL selects the PIN API for a simulated Plex environment.
// Production always uses plex.tv; no public configuration or request can set it.
func (s *Service) SetPlexBaseURL(baseURL string) { s.plexBaseURL = baseURL }

const plexAttemptTTL = 10 * time.Minute
const plexPollInterval = 3 * time.Second
const plexMaxFlows = 2048

type plexAttempt struct {
	mu                      sync.Mutex
	Request                 oidcBeginRequest
	Purpose                 string
	Actor                   *Claims
	Config                  plexConfiguration
	Generation              uint64
	Flow, ClientID, PinCode string
	PinID                   int64
	Expires                 time.Time
	lastCheck               time.Time
	account                 plex.Account
	ticket, ticketHash      string
	ticketExpires           time.Time
	failure                 error
}
type plexFlowStore struct {
	mu         sync.Mutex
	generation uint64
	attempts   map[string]*plexAttempt
}

func newPlexFlowStore() *plexFlowStore { return &plexFlowStore{attempts: map[string]*plexAttempt{}} }
func (f *plexFlowStore) clear()        { f.mu.Lock(); defer f.mu.Unlock(); f.generation++; clear(f.attempts) }
func (f *plexFlowStore) currentGeneration() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.generation
}
func (f *plexFlowStore) prune() {
	for key, a := range f.attempts {
		if !time.Now().Before(a.Expires) {
			delete(f.attempts, key)
		}
	}
}
func (f *plexFlowStore) get(flow string) *plexAttempt {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prune()
	return f.attempts[flow]
}
func (f *plexFlowStore) remove(flow string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.attempts, flow)
}
func (f *plexFlowStore) active(a *plexAttempt) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.generation == a.Generation && f.attempts[a.Flow] == a && time.Now().Before(a.Expires)
}
func (f *plexFlowStore) consume(a *plexAttempt) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.generation != a.Generation || f.attempts[a.Flow] != a || !time.Now().Before(a.Expires) {
		return false
	}
	delete(f.attempts, a.Flow)
	return true
}

type plexBeginResponse struct {
	Flow     string    `json:"flow"`
	URL      string    `json:"url"`
	Expires  time.Time `json:"expires_at"`
	Interval int       `json:"interval"`
}

func (s *Service) beginPlex(ctx context.Context, req oidcBeginRequest, purpose string, actor *Claims) (plexBeginResponse, error) {
	var out plexBeginResponse
	if (req.Client != "web" && req.Client != "mobile" && req.Client != "mcp") || !validOIDCChallenge(req.Challenge) || len(req.DeviceName) > 200 || len(req.HardwareID) > 200 || req.Invitation != "" || (req.Client == "mcp") != (purpose == "mcp") {
		return out, ErrPlexFlow
	}
	if purpose != "login" && purpose != "link" && purpose != "mcp" {
		return out, ErrPlexFlow
	}
	c, err := s.plexConfiguration()
	if err != nil {
		return out, err
	}
	if !c.Enabled {
		return out, ErrPlexDisabled
	}
	if purpose == "link" {
		if actor == nil {
			return out, ErrInvalidCredentials
		}
		if _, err = s.authoritativeSession(ctx, actor.UserID, actor.DeviceID); err != nil {
			return out, err
		}
	}
	flow, err := randomURLToken(32)
	if err != nil {
		return out, ErrAuthUnavailable
	}
	a := &plexAttempt{Request: req, Purpose: purpose, Actor: actor, Config: c, Flow: flow, ClientID: uuid.NewString(), Expires: time.Now().Add(plexAttemptTTL)}
	// Reserve capacity before creating a PIN upstream. No unbounded in-flight store.
	f := s.plexFlows
	f.mu.Lock()
	f.prune()
	if len(f.attempts) >= plexMaxFlows {
		f.mu.Unlock()
		return out, ErrPlexUnavailable
	}
	a.Generation = f.generation
	f.attempts[flow] = a
	f.mu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	client := plex.NewClientAt(s.plexBaseURL)
	pin, err := client.CreatePin(ctx, a.ClientID)
	if err != nil {
		f.remove(flow)
		return out, ErrPlexUnavailable
	}
	if pin.ID <= 0 || pin.Code == "" {
		f.remove(flow)
		return out, ErrPlexUnavailable
	}
	a.PinID, a.PinCode = pin.ID, pin.Code
	// Expires is read by pruning under the store lock.
	f.mu.Lock()
	if pin.ExpiresIn > 0 {
		expiry := time.Now().Add(time.Duration(pin.ExpiresIn) * time.Second)
		if expiry.Before(a.Expires) {
			a.Expires = expiry
		}
	}
	if !pin.ExpiresAt.IsZero() && pin.ExpiresAt.Before(a.Expires) {
		a.Expires = pin.ExpiresAt
	}
	f.mu.Unlock()
	if !f.active(a) {
		f.remove(flow)
		return out, ErrPlexFlow
	}
	return plexBeginResponse{Flow: flow, URL: client.AuthURL(a.ClientID, pin.Code), Expires: a.Expires, Interval: 3}, nil
}
func (s *Service) currentPlexAttempt(a *plexAttempt) error {
	if !s.plexFlows.active(a) {
		return ErrPlexFlow
	}
	c, err := s.plexConfiguration()
	if err != nil {
		return err
	}
	if !c.Enabled {
		return ErrPlexDisabled
	}
	if c.raw != a.Config.raw || c.ssoOnly != a.Config.ssoOnly {
		return ErrPlexFlow
	}
	if a.Purpose == "link" {
		if a.Actor == nil {
			return ErrInvalidCredentials
		}
		_, err = s.authoritativeSession(context.Background(), a.Actor.UserID, a.Actor.DeviceID)
	}
	return err
}
func (s *Service) checkPlex(ctx context.Context, flow, verifier string) (map[string]any, error) {
	a := s.plexFlows.get(flow)
	if a == nil || !validCompletionProof(verifier, flow, a.Flow, a.Request.Challenge, a.Expires) {
		return nil, ErrPlexFlow
	}
	pending := map[string]any{"status": "pending", "interval": 3}
	if !a.mu.TryLock() {
		return pending, nil
	}
	defer a.mu.Unlock()
	if err := s.currentPlexAttempt(a); err != nil {
		return nil, err
	}
	if a.failure != nil {
		return nil, a.failure
	}
	if a.ticket != "" {
		if !time.Now().Before(a.ticketExpires) {
			return nil, ErrPlexFlow
		}
		return map[string]any{"status": "complete", "code": a.ticket}, nil
	}
	if time.Since(a.lastCheck) < plexPollInterval {
		return pending, nil
	}
	a.lastCheck = time.Now()
	client := plex.NewClientAt(s.plexBaseURL)
	pin, err := client.CheckPinCode(ctx, a.ClientID, a.PinID, a.PinCode)
	if err != nil {
		if plex.IsUnauthorized(err) || plex.IsNotFound(err) {
			a.failure = ErrPlexFlow
			return nil, a.failure
		}
		return nil, ErrPlexUnavailable
	}
	if pin.AuthToken == "" {
		return pending, nil
	}
	account, verifyErr := client.GetUser(ctx, a.ClientID, pin.AuthToken)
	// Cleanup has its own timeout and survives cancellation, verification failure,
	// denial, and a settings change during the upstream request. Never log tokens.
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	_, cleanupErr := client.SignOut(cleanupCtx, a.ClientID, pin.AuthToken)
	cancel()
	if verifyErr != nil || cleanupErr != nil {
		a.failure = ErrPlexUnavailable
		if plex.IsUnauthorized(verifyErr) {
			a.failure = ErrPlexDenied
		}
		return nil, a.failure
	}
	if account.ID <= 0 {
		a.failure = ErrPlexDenied
		return nil, a.failure
	}
	if err = s.currentPlexAttempt(a); err != nil {
		a.failure = err
		return nil, err
	}
	a.account = *account
	a.ticket, a.ticketHash, a.ticketExpires, err = newCompletionTicket()
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	return map[string]any{"status": "complete", "code": a.ticket}, nil
}
func (s *Service) cancelPlex(flow, verifier string) error {
	a := s.plexFlows.get(flow)
	if a == nil || !validCompletionProof(verifier, flow, a.Flow, a.Request.Challenge, a.Expires) {
		return ErrPlexFlow
	}
	if !s.plexFlows.consume(a) {
		return ErrPlexFlow
	}
	return nil
}
func plexAttemptInTransaction(tx *sql.Tx, a *plexAttempt) error {
	var raw, policy string
	if tx.QueryRow("SELECT COALESCE((SELECT value FROM settings WHERE key='plex_auth'),'{}'),COALESCE((SELECT value FROM settings WHERE key='oidc_sso_only'),'false')").Scan(&raw, &policy) != nil {
		return ErrAuthUnavailable
	}
	if raw != a.Config.raw || policy != a.Config.ssoOnly || !a.Config.Enabled {
		return ErrPlexFlow
	}
	if a.Purpose == "link" {
		return oidcActorInTransaction(tx, a.Actor, false)
	}
	return nil
}
func requirePlexIdentity(q interface{ QueryRow(string, ...any) *sql.Row }, userID, accountID int64) error {
	var allowed, linked, enabled bool
	err := q.QueryRow(`SELECT (u.role='admin' OR COALESCE((SELECT value FROM settings WHERE key='oidc_sso_only'),'false')='false'),EXISTS(SELECT 1 FROM plex_identities p WHERE p.user_id=u.id AND p.plex_account_id=?),COALESCE((SELECT value FROM settings WHERE key='plex_auth_enabled'),'false')='true' FROM users u WHERE u.id=?`, accountID, userID).Scan(&allowed, &linked, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPlexDenied
	}
	if err != nil {
		return ErrAuthUnavailable
	}
	if !enabled {
		return ErrPlexDisabled
	}
	if !allowed {
		return ErrSSORequired
	}
	if !linked {
		return ErrPlexDenied
	}
	return nil
}
func (s *Service) exchangePlex(ctx context.Context, flow, code, verifier string) (any, error) {
	a := s.plexFlows.get(flow)
	if a == nil {
		return nil, ErrPlexFlow
	}
	// Read fresh share evidence only for signup; existing identities deliberately
	// remain usable when a library share is later removed.
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ticket == "" || hashToken(code) != a.ticketHash || !validCompletionProof(verifier, flow, a.Flow, a.Request.Challenge, a.ticketExpires) {
		return nil, ErrPlexFlow
	}
	if err := s.currentPlexAttempt(a); err != nil {
		return nil, err
	}
	var linked int64
	err := s.db.QueryRow("SELECT user_id FROM plex_identities WHERE plex_account_id=?", a.account.ID).Scan(&linked)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAuthUnavailable
	}
	var servers []PlexServerAccounts
	if linked == 0 && a.Purpose != "link" && a.Config.AutoCreate && a.Config.ssoOnly == "false" {
		servers, err = s.plexAccounts(ctx)
		if err != nil {
			return nil, err
		}
	}
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	if err = s.currentPlexAttempt(a); err != nil {
		return nil, err
	}
	// Consume only after proving the verifier. Concurrent exchange gets no device.
	if !s.plexFlows.consume(a) {
		return nil, ErrPlexFlow
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	defer tx.Rollback()
	if err = plexAttemptInTransaction(tx, a); err != nil {
		return nil, err
	}
	linked = 0
	err = tx.QueryRow("SELECT user_id FROM plex_identities WHERE plex_account_id=?", a.account.ID).Scan(&linked)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAuthUnavailable
	}
	if a.Purpose == "link" {
		if linked != 0 && linked != a.Actor.UserID {
			return nil, ErrPlexConflict
		}
		linked = a.Actor.UserID
	} else if linked == 0 {
		if a.Config.ssoOnly != "false" {
			return nil, ErrSSORequired
		}
		if !a.Config.AutoCreate {
			return nil, ErrPlexDenied
		}
		eligible := []PlexServerAccounts{}
		unavailable := false
		for _, server := range servers {
			if server.Error != "" {
				unavailable = true
				continue
			}
			for _, p := range server.Accounts {
				if p.Accepted && p.Account.ID == a.account.ID {
					revision, e := PlexInstanceRevision(tx, server.InstanceID)
					if e != nil || revision != server.Revision {
						return nil, ErrPlexFlow
					}
					eligible = append(eligible, server)
					break
				}
			}
		}
		if len(eligible) == 0 {
			if unavailable {
				return nil, ErrPlexUnavailable
			}
			return nil, ErrPlexDenied
		}
		linked, err = createExternalUser(tx, a.account.Username, "plex-user")
		if err != nil {
			return nil, err
		}
		email := mediaserver.CanonicalEmail(a.account.Email)
		if !mediaserver.ValidEmail(email) {
			return nil, ErrPlexDenied
		}
		if _, err = tx.Exec("UPDATE users SET plex_email=? WHERE id=?", email, linked); err != nil {
			return nil, ErrAuthUnavailable
		}
		for _, server := range eligible {
			if _, err = tx.Exec("INSERT INTO user_instance_grants(user_id,instance_id) VALUES (?,?)", linked, server.InstanceID); err != nil {
				return nil, ErrAuthUnavailable
			}
			// A legacy row owned by someone else is a conflict, never an email match
			// authorizing takeover or a reason to move that user's grant.
			if _, err = tx.Exec("INSERT INTO user_media_server_accounts(user_id,instance_id,remote_user_id,remote_username,created_by_cantinarr,manage_access) VALUES (?,?,?,?,0,0)", linked, server.InstanceID, email, a.account.Username); err != nil {
				return nil, ErrPlexConflict
			}
		}
	}
	if err = linkPlexIdentity(tx, linked, a.account); err != nil {
		return nil, err
	}
	if a.Purpose != "link" {
		if err = requirePlexIdentity(tx, linked, a.account.ID); err != nil {
			return nil, err
		}
	}
	user, err := scanUserRecord(tx.QueryRow(userSelect+" WHERE u.id=?", linked))
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	response := &TokenResponse{User: userWithPermissions(user)}
	if a.Purpose == "login" {
		response, err = s.issueExternalSession(tx, user, a.Request.DeviceName, a.Request.HardwareID, "plex", "", a.account.ID)
		if err != nil {
			return nil, err
		}
	}
	if tx.Commit() != nil {
		return nil, ErrAuthUnavailable
	}
	if a.Purpose == "link" {
		return map[string]string{"status": "linked"}, nil
	}
	if a.Purpose == "mcp" {
		return s.createExternalConsent(response.User, "plex", "", a.account.ID, a.Config.raw, a.Request.OAuth)
	}
	response.User.PlexInvitedAt = s.plexInvitedAt(linked)
	return response, nil
}

func createExternalUser(tx *sql.Tx, name, fallback string) (int64, error) {
	if name == "" {
		name = fallback
	}
	name = oidcUsername(name)
	for i := 0; i < 20; i++ {
		candidate := name
		if i > 0 {
			suffix, err := randomURLToken(6)
			if err != nil {
				return 0, ErrAuthUnavailable
			}
			candidate += "-" + suffix
		}
		result, err := tx.Exec("INSERT INTO users(username,password_hash,role) VALUES (?,'','user') ON CONFLICT(username) DO NOTHING", candidate)
		if err != nil {
			return 0, ErrAuthUnavailable
		}
		n, err := result.RowsAffected()
		if err != nil {
			return 0, ErrAuthUnavailable
		}
		if n == 1 {
			return result.LastInsertId()
		}
	}
	return 0, ErrAuthUnavailable
}
