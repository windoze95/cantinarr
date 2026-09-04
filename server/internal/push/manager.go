package push

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/secrets"
)

const (
	// defaultRetryInterval is the StartRetry tick and the wait after a first
	// failure to reach the gateway; each consecutive failure doubles it.
	defaultRetryInterval = 60 * time.Second
	// defaultMaxRetryInterval caps that doubling.
	defaultMaxRetryInterval = 30 * time.Minute
)

// Manager owns the lazily-built push gateway client and makes the integration
// self-healing: a gateway that was down at boot, device tokens registered
// while push was disabled, or a stored key the gateway stopped accepting all
// reach a working gateway WITHOUT a server restart or app re-open. It resolves
// the per-app API key (explicit env key > a key auto-enrolled on a previous
// start > a fresh self-enrollment), caches the client behind a mutex, and on
// each success reconciles every stored token with the gateway. All failures
// are non-fatal: push simply stays off until the next Ensure (driven by a
// registration, a test push, or the background retry), and every failed
// attempt pushes the next one further out so a broken gateway is never
// hammered.
type Manager struct {
	db          *sql.DB
	cipher      *secrets.Cipher
	gatewayURL  string
	explicitKey string
	enrollToken string
	serverName  string
	logger      *slog.Logger
	// retryInterval is the StartRetry tick and the base backoff after a
	// failure; maxRetryInterval caps the doubling. Both overridable in tests.
	retryInterval    time.Duration
	maxRetryInterval time.Duration

	mu     sync.Mutex
	client *Client
	// storedKeyEnc is the encrypted settings value the current client's key
	// came from ("" for an explicit key). ReportUnauthorized deletes the row
	// by this value, so a late report can never remove a newer key.
	storedKeyEnc string
	// failedAttempts counts consecutive failed key resolutions; it sizes the
	// wait before the next one and resets on success.
	failedAttempts int
	// refusals counts the stored keys the gateway has refused over the life
	// of the process. It is never reset: a gateway that keeps refusing the
	// keys it just issued is broken, and each re-enrollment waits longer than
	// the one before rather than looping at /v1/enroll.
	refusals int
	// nextAttempt is the earliest Ensure may contact the gateway again. The
	// zero value means now.
	nextAttempt time.Time
	// lastExplicitRefusalLog is when the refusal of an explicit key was last
	// logged, so a burst of 401s logs once per maxRetryInterval, not once per
	// send.
	lastExplicitRefusalLog time.Time
}

// NewManager builds a push Manager. gatewayURL is required (callers gate on it
// being non-empty); explicitKey is an operator-supplied per-app key that wins
// over any stored/auto-enrolled key; enrollToken is sent as X-Enroll-Token on
// auto-enroll (needed only for gated gateways); serverName names the tenant on
// auto-enroll (defaults to "Cantinarr" when empty).
func NewManager(db *sql.DB, cipher *secrets.Cipher, gatewayURL, explicitKey, enrollToken, serverName string, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	if serverName == "" {
		serverName = "Cantinarr"
	}
	return &Manager{
		db:               db,
		cipher:           cipher,
		gatewayURL:       gatewayURL,
		explicitKey:      explicitKey,
		enrollToken:      enrollToken,
		serverName:       serverName,
		logger:           logger,
		retryInterval:    defaultRetryInterval,
		maxRetryInterval: defaultMaxRetryInterval,
	}
}

// Ensure returns a ready gateway client, building it on first use. It is
// single-flight: a cached client is returned immediately, otherwise the key is
// resolved (explicit > stored > self-enroll), the client is built and cached,
// and stored device tokens are reconciled with the gateway. A resolution
// failure is logged and reported as a nil client (never fatal) and opens a
// backoff window (retryInterval, doubling per consecutive failure up to
// maxRetryInterval) during which Ensure answers nil without contacting the
// gateway; the first call after the window closes tries again.
func (m *Manager) Ensure(ctx context.Context) *Client {
	m.mu.Lock()
	if m.client != nil {
		client := m.client
		m.mu.Unlock()
		return client
	}
	if time.Now().Before(m.nextAttempt) {
		// Inside the backoff window after a failed attempt or a refused key.
		m.mu.Unlock()
		return nil
	}

	apiKey, storedEnc, err := m.resolveAPIKey(ctx)
	if err != nil {
		m.failedAttempts++
		wait := m.backoffFor(m.failedAttempts)
		m.nextAttempt = time.Now().Add(wait)
		m.mu.Unlock()
		m.logger.Error("push: resolve gateway key", "err", err, "retry_in", wait)
		return nil
	}

	client := NewClient(m.gatewayURL, apiKey)
	m.client = client
	m.storedKeyEnc = storedEnc
	m.failedAttempts = 0
	m.nextAttempt = time.Time{}
	m.mu.Unlock()
	m.logger.Info("push: gateway client ready", "gateway", m.gatewayURL)

	// Reconcile every locally-stored token with the gateway so tokens
	// registered while push was disabled (or against a gateway that was down),
	// and every token after a re-enrollment (the new tenant knows no devices),
	// are now known to it. Done after releasing the lock: it makes one network
	// call per token and must not block concurrent Client() readers.
	// Best-effort; errors are logged inside.
	m.forwardStoredTokens(ctx, client)

	return client
}

// backoffFor returns the wait after the k-th consecutive failure (k >= 1):
// retryInterval doubled per failure, capped at maxRetryInterval. With the
// defaults that is 60s, 2m, 4m, 8m, 16m, then 30m for good.
func (m *Manager) backoffFor(k int) time.Duration {
	wait := m.retryInterval
	for i := 1; i < k && wait < m.maxRetryInterval; i++ {
		wait *= 2
	}
	if wait > m.maxRetryInterval {
		wait = m.maxRetryInterval
	}
	return wait
}

// Client returns the cached gateway client without attempting to enroll. It is
// nil until a successful Ensure, so send paths can be wired unconditionally and
// no-op while push is unconfigured or the gateway is unreachable.
func (m *Manager) Client() *Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.client
}

// StartRetry runs a background goroutine for the life of ctx that calls
// Ensure on every retryInterval tick while no client is cached. It is the
// path by which the integration heals without a restart: a gateway that was
// down at boot, or a stored key the gateway later refused (ReportUnauthorized
// drops the client), gets its next attempt here, with Ensure's own backoff
// deciding whether a given tick actually contacts the gateway. While a client
// exists a tick is one mutex read, so the loop never exits: the refusal case
// needs it long after enrollment first succeeded. It returns immediately; pass
// the server context so the loop stops on shutdown.
func (m *Manager) StartRetry(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(m.retryInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if m.Client() == nil {
					m.Ensure(ctx)
				}
			}
		}
	}()
}

// ReportUnauthorized is called by the send and register paths when a gateway
// call made with client came back 401: the gateway does not know the key that
// client carries (its tenant was deleted, or the gateway's database was
// replaced). What happens next depends on where the key came from.
//
// A stored, auto-enrolled key is discarded: the cached client and the
// push_api_key settings row go, and the next Ensure (the retry loop's tick,
// an inbound registration, or a test push) self-enrolls again, persists the
// new key, and re-registers every stored device token with the new tenant,
// which knows no devices yet. That next attempt waits a backoff sized by how
// many refusals this process has seen (never reset), so a gateway that keeps
// refusing the keys it just issued is asked less and less often instead of
// being hammered at /v1/enroll.
//
// An explicit CANTINARR_PUSH_API_KEY is never replaced: there is nothing to
// replace it with. The client stays so the delivery-health issue keeps
// reporting the failing sends, and the refusal is logged (once per
// maxRetryInterval, so a burst does not spam) for the operator to fix or
// clear the key.
//
// Only a report about the currently cached client counts. A stale or
// duplicate report (a burst of concurrent 401s, or a call that finished after
// the key was already replaced) is a no-op, so one refusal produces one
// invalidation. nil-receiver safe, like the Notifier's setters.
func (m *Manager) ReportUnauthorized(client *Client) {
	if m == nil || client == nil {
		return
	}
	m.mu.Lock()
	if m.client != client {
		m.mu.Unlock()
		return
	}
	now := time.Now()
	if m.explicitKey != "" {
		logIt := m.lastExplicitRefusalLog.IsZero() || now.Sub(m.lastExplicitRefusalLog) >= m.maxRetryInterval
		if logIt {
			m.lastExplicitRefusalLog = now
		}
		m.mu.Unlock()
		if logIt {
			m.logger.Error("push: gateway refused CANTINARR_PUSH_API_KEY; push stays down until the key is fixed or cleared",
				"gateway", m.gatewayURL)
		}
		return
	}

	m.client = nil
	storedEnc := m.storedKeyEnc
	m.storedKeyEnc = ""
	m.refusals++
	m.failedAttempts = 0
	wait := m.backoffFor(m.refusals)
	m.nextAttempt = now.Add(wait)
	refusals := m.refusals
	m.mu.Unlock()

	// The row goes after the lock is released: the DB pool is a single
	// connection, so no DB work happens under the manager mutex. The backoff
	// window set above keeps a concurrent Ensure from re-reading the key in
	// the meantime, and matching on the value means a report that lands after
	// a re-enrollment cannot delete the newer key.
	if storedEnc != "" {
		if _, err := m.db.Exec("DELETE FROM settings WHERE key = 'push_api_key' AND value = ?", storedEnc); err != nil {
			m.logger.Error("push: discard refused gateway key", "err", err)
		}
	}
	m.logger.Warn("push: gateway refused the stored key; re-enrolling", "retry_in", wait, "refusals", refusals)
}

// ForwardStoredTokens reconciles every locally-stored token with the gateway by
// re-registering each one. Used on first enrollment; exported so it can be
// driven on demand. A nil cached client makes it a no-op.
func (m *Manager) ForwardStoredTokens(ctx context.Context) {
	client := m.Client()
	if client == nil {
		return
	}
	m.forwardStoredTokens(ctx, client)
}

// forwardStoredTokens reads every push_tokens row and re-registers it with the
// given client. Best-effort: a failure on one token is logged and the rest
// continue, except a 401, which means the gateway does not know this key at
// all: the rest would fail the same way, so the loop stops and hands the
// refusal to ReportUnauthorized. The client is passed in (rather than read
// from m.client) so the caller controls locking; it runs without the manager
// mutex held, and the rows cursor is drained before the first network call so
// the single DB connection is free while the gateway is contacted.
func (m *Manager) forwardStoredTokens(ctx context.Context, client *Client) {
	rows, err := m.db.Query(`SELECT device_id, user_id, platform, token FROM push_tokens`)
	if err != nil {
		m.logger.Error("push: read stored tokens for reconciliation", "err", err)
		return
	}
	defer rows.Close()

	type token struct {
		deviceID string
		userID   int64
		platform string
		value    string
	}
	var tokens []token
	for rows.Next() {
		var t token
		if err := rows.Scan(&t.deviceID, &t.userID, &t.platform, &t.value); err != nil {
			m.logger.Error("push: scan stored token", "err", err)
			return
		}
		tokens = append(tokens, t)
	}
	if err := rows.Err(); err != nil {
		m.logger.Error("push: iterate stored tokens", "err", err)
		return
	}

	forwarded := 0
	for _, t := range tokens {
		if err := client.RegisterDevice(ctx, t.userID, t.deviceID, t.platform, t.value); err != nil {
			m.logger.Error("push: reconcile stored token with gateway", "err", err, "device_id", t.deviceID)
			if IsUnauthorized(err) {
				m.ReportUnauthorized(client)
				break
			}
			continue
		}
		forwarded++
	}
	m.logger.Info("push: reconciled stored device tokens with gateway", "forwarded", forwarded, "total", len(tokens))
}

// resolveAPIKey resolves the per-app gateway key: an explicit operator key
// wins (and is never persisted); otherwise a key auto-enrolled on a previous
// start is loaded from the settings table and decrypted; otherwise the server
// self-enrolls with the gateway once and persists the issued key (encrypted at
// rest, like the JWT secret). This gives self-hosters push with zero manual
// key handling. To force re-enrollment, delete the 'push_api_key' settings row
// (ReportUnauthorized does exactly that when the gateway refuses the key).
// The second result is the encrypted settings value the key came from, "" for
// an explicit key, so a later refusal can delete precisely that row.
func (m *Manager) resolveAPIKey(ctx context.Context) (string, string, error) {
	if m.explicitKey != "" {
		return m.explicitKey, "", nil // explicit operator override; not persisted
	}

	var stored string
	if err := m.db.QueryRow("SELECT value FROM settings WHERE key = 'push_api_key'").Scan(&stored); err == nil {
		key, err := m.cipher.Decrypt(stored)
		if err != nil {
			return "", "", err
		}
		return key, stored, nil
	}

	// No key yet: self-enroll with the gateway and persist the issued key.
	res, err := Enroll(m.gatewayURL, m.serverName, m.enrollToken)
	if err != nil {
		return "", "", fmt.Errorf("auto-enroll with push gateway: %w", err)
	}
	enc, err := m.cipher.Encrypt(res.APIKey)
	if err != nil {
		return "", "", fmt.Errorf("encrypt push key: %w", err)
	}
	if _, err := m.db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES ('push_api_key', ?)", enc); err != nil {
		return "", "", fmt.Errorf("persist push key: %w", err)
	}
	m.logger.Info("push: auto-enrolled with gateway; key persisted", "gateway", m.gatewayURL, "tenant", res.TenantID)
	return res.APIKey, enc, nil
}
