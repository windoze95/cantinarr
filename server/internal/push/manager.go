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

// defaultRetryInterval is how often StartRetry re-attempts enrollment while the
// gateway is configured but not yet reachable.
const defaultRetryInterval = 60 * time.Second

// Manager owns the lazily-built push gateway client and makes the integration
// self-healing: a gateway that was down at boot, or device tokens registered
// while push was disabled, reach the gateway WITHOUT a server restart or app
// re-open. It resolves the per-app API key (explicit env key > a key
// auto-enrolled on a previous start > a fresh self-enrollment), caches the
// client behind a mutex, and on its first success reconciles every stored
// token with the gateway. All failures are non-fatal — push simply stays off
// until the next Ensure (driven by a registration or the background retry).
type Manager struct {
	db          *sql.DB
	cipher      *secrets.Cipher
	gatewayURL  string
	explicitKey string
	enrollToken string
	serverName  string
	logger      *slog.Logger
	// retryInterval is the StartRetry backoff; overridable in tests.
	retryInterval time.Duration

	mu       sync.Mutex
	client   *Client
	enrolled bool
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
		db:            db,
		cipher:        cipher,
		gatewayURL:    gatewayURL,
		explicitKey:   explicitKey,
		enrollToken:   enrollToken,
		serverName:    serverName,
		logger:        logger,
		retryInterval: defaultRetryInterval,
	}
}

// Ensure returns a ready gateway client, building it on first use. It is
// single-flight: a cached client is returned immediately, otherwise the key is
// resolved (explicit > stored > self-enroll), the client is built and cached,
// and stored device tokens are reconciled with the gateway exactly once. A
// resolution failure is logged and reported as a nil client (never fatal); the
// next call retries.
func (m *Manager) Ensure(ctx context.Context) *Client {
	m.mu.Lock()
	if m.client != nil {
		client := m.client
		m.mu.Unlock()
		return client
	}

	apiKey, err := m.resolveAPIKey(ctx)
	if err != nil {
		m.mu.Unlock()
		m.logger.Error("push: resolve gateway key", "err", err)
		return nil
	}

	client := NewClient(m.gatewayURL, apiKey)
	m.client = client
	m.enrolled = true
	m.mu.Unlock()
	m.logger.Info("push: gateway client ready", "gateway", m.gatewayURL)

	// First success: reconcile every locally-stored token with the gateway so
	// tokens registered while push was disabled (or against a gateway that was
	// down) are now known to it. Done after releasing the lock — it makes one
	// network call per token and must not block concurrent Client() readers.
	// Best-effort; errors are logged inside.
	m.forwardStoredTokens(ctx, client)

	return client
}

// Client returns the cached gateway client without attempting to enroll. It is
// nil until a successful Ensure, so send paths can be wired unconditionally and
// no-op while push is unconfigured or the gateway is unreachable.
func (m *Manager) Client() *Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.client
}

// StartRetry runs a background goroutine that retries Ensure every
// retryInterval until enrollment succeeds, then exits. It returns immediately;
// pass the server context so the loop stops on shutdown. Safe to call once at
// startup — if the first Ensure (run elsewhere) already succeeded, the loop
// exits on its first check.
func (m *Manager) StartRetry(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(m.retryInterval)
		defer ticker.Stop()
		for {
			m.mu.Lock()
			done := m.enrolled
			m.mu.Unlock()
			if done {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if m.Ensure(ctx) != nil {
					return
				}
			}
		}
	}()
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
// continue. The client is passed in (rather than read from m.client) so the
// caller controls locking — it runs without the manager mutex held.
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
// key handling. To force re-enrollment, delete the 'push_api_key' settings row.
// Mirrors the former main.ensurePushAPIKey logic.
func (m *Manager) resolveAPIKey(ctx context.Context) (string, error) {
	if m.explicitKey != "" {
		return m.explicitKey, nil // explicit operator override; not persisted
	}

	var stored string
	if err := m.db.QueryRow("SELECT value FROM settings WHERE key = 'push_api_key'").Scan(&stored); err == nil {
		return m.cipher.Decrypt(stored)
	}

	// No key yet: self-enroll with the gateway and persist the issued key.
	res, err := Enroll(m.gatewayURL, m.serverName, m.enrollToken)
	if err != nil {
		return "", fmt.Errorf("auto-enroll with push gateway: %w", err)
	}
	enc, err := m.cipher.Encrypt(res.APIKey)
	if err != nil {
		return "", fmt.Errorf("encrypt push key: %w", err)
	}
	if _, err := m.db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES ('push_api_key', ?)", enc); err != nil {
		return "", fmt.Errorf("persist push key: %w", err)
	}
	m.logger.Info("push: auto-enrolled with gateway; key persisted", "gateway", m.gatewayURL, "tenant", res.TenantID)
	return res.APIKey, nil
}
