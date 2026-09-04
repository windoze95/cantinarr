package push

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// testCipher builds a deterministic cipher for manager tests that exercise the
// stored-key and auto-enroll paths.
func testCipher(t *testing.T) *secrets.Cipher {
	t.Helper()
	c, err := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

// gatewayUnauthorized is the envelope the real gateway answers with when the
// bearer key's hash is unknown to it, e.g. after its database was replaced.
const gatewayUnauthorized = `{"error":{"code":"unauthorized","message":"invalid tenant key"}}`

// registration is one accepted /v1/devices upsert: which device, under which
// bearer key, so a test can tell the old tenant's registrations from the new.
type registration struct {
	deviceID string
	key      string
}

// mockGateway records enroll calls, device registrations, and sends.
// enrollFails, when set, makes /v1/enroll return 503 so the background-retry
// path can be driven. accepted, when set via acceptKeys, is the only set of
// bearer keys /v1/devices and /v1/notifications honour; any other key gets the
// gateway's real 401 envelope, the way a gateway whose database no longer
// holds the tenant answers. Left nil, every key is accepted.
type mockGateway struct {
	mu                sync.Mutex
	enrollCalls       int
	enrollFails       bool
	issuedKey         string          // api_key /v1/enroll hands out
	accepted          map[string]bool // nil = accept every bearer key
	registrations     []registration  // accepted /v1/devices upserts
	notificationCalls int
	notificationAuth  string
}

func (g *mockGateway) setEnrollFails(v bool) {
	g.mu.Lock()
	g.enrollFails = v
	g.mu.Unlock()
}

// acceptKeys replaces the set of bearer keys the gateway knows.
func (g *mockGateway) acceptKeys(keys ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.accepted = make(map[string]bool, len(keys))
	for _, k := range keys {
		g.accepted[k] = true
	}
}

// authorized reports whether the request's bearer key is one the gateway knows.
func (g *mockGateway) authorized(r *http.Request) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.accepted == nil || g.accepted[bearerKey(r)]
}

// bearerKey extracts the bearer key from a request's Authorization header.
func bearerKey(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func (g *mockGateway) enrollCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.enrollCalls
}

// registeredIDs returns every device id the gateway accepted, under any key.
func (g *mockGateway) registeredIDs() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]string, 0, len(g.registrations))
	for _, reg := range g.registrations {
		out = append(out, reg.deviceID)
	}
	return out
}

// registeredIDsWith returns the device ids the gateway accepted under one key.
func (g *mockGateway) registeredIDsWith(key string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for _, reg := range g.registrations {
		if reg.key == key {
			out = append(out, reg.deviceID)
		}
	}
	return out
}

func (g *mockGateway) notificationResult() (calls int, auth string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.notificationCalls, g.notificationAuth
}

// newMockGatewayServer stands up the mock and returns it plus its base URL.
func newMockGatewayServer(t *testing.T) (*mockGateway, string) {
	t.Helper()
	g := &mockGateway{issuedKey: "pgk_enrolled"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/enroll":
			g.mu.Lock()
			g.enrollCalls++
			fail, issued := g.enrollFails, g.issuedKey
			g.mu.Unlock()
			if fail {
				http.Error(w, `{"error":"unavailable"}`, http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"tenant_id":"t-1","api_key":"`+issued+`"}`)
		case "/v1/devices":
			if !g.authorized(r) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, gatewayUnauthorized)
				return
			}
			body := map[string]any{}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			if id, _ := body["device_id"].(string); id != "" && r.Method == http.MethodPost {
				g.mu.Lock()
				g.registrations = append(g.registrations, registration{deviceID: id, key: bearerKey(r)})
				g.mu.Unlock()
			}
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"id":"d","created":true}`)
		case "/v1/notifications":
			g.mu.Lock()
			g.notificationCalls++
			g.notificationAuth = r.Header.Get("Authorization")
			g.mu.Unlock()
			if !g.authorized(r) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, gatewayUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"sent":1,"failed":0,"results":[]}`)
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(srv.Close)
	return g, srv.URL
}

// storedPushKey reads and decrypts the persisted push_api_key, or "" if absent.
func storedPushKey(t *testing.T, database *sql.DB, cipher *secrets.Cipher) string {
	t.Helper()
	var stored string
	if err := database.QueryRow("SELECT value FROM settings WHERE key = 'push_api_key'").Scan(&stored); err != nil {
		return ""
	}
	got, err := cipher.Decrypt(stored)
	if err != nil {
		t.Fatalf("decrypt stored push key: %v", err)
	}
	return got
}

// PUSH-001: an explicit gateway key skips enrollment, authenticates a successful send, and is never persisted.
func TestManagerExplicitKeySkipsEnrollmentAndAuthenticatesSend(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	g, url := newMockGatewayServer(t)
	cipher := testCipher(t)

	mgr := NewManager(database, cipher, url, "pgk_explicit", "", "Cantinarr", nil)
	client := mgr.Ensure(context.Background())
	if client == nil {
		t.Fatal("Ensure returned nil for explicit-key manager")
	}
	if client.apiKey != "pgk_explicit" {
		t.Errorf("client key = %q, want pgk_explicit", client.apiKey)
	}
	resp, err := client.Send(context.Background(), []int64{42}, "Test", "Body", map[string]any{"type": "test"})
	if err != nil {
		t.Fatalf("send with explicit key: %v", err)
	}
	if resp == nil || resp.Sent != 1 || resp.Failed != 0 {
		t.Fatalf("send response = %#v, want sent=1 failed=0", resp)
	}
	if g.enrollCount() != 0 {
		t.Errorf("enroll calls = %d, want 0 (explicit key must not enroll)", g.enrollCount())
	}
	calls, auth := g.notificationResult()
	if calls != 1 || auth != "Bearer pgk_explicit" {
		t.Errorf("notification calls/auth = %d, %q; want 1, %q", calls, auth, "Bearer pgk_explicit")
	}
	var persisted int
	if err := database.QueryRow("SELECT COUNT(*) FROM settings WHERE key = 'push_api_key'").Scan(&persisted); err != nil {
		t.Fatalf("count persisted push key: %v", err)
	}
	if persisted != 0 {
		t.Errorf("persisted push key rows = %d, want 0", persisted)
	}
}

func TestManagerEnsureUsesStoredKey(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	g, url := newMockGatewayServer(t)
	cipher := testCipher(t)

	// Seed an encrypted, previously-enrolled key.
	enc, err := cipher.Encrypt("pgk_stored")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	mustExec(t, database, "INSERT INTO settings (key, value) VALUES ('push_api_key', ?)", enc)

	mgr := NewManager(database, cipher, url, "", "", "Cantinarr", nil)
	client := mgr.Ensure(context.Background())
	if client == nil {
		t.Fatal("Ensure returned nil for stored-key manager")
	}
	if client.apiKey != "pgk_stored" {
		t.Errorf("client key = %q, want pgk_stored", client.apiKey)
	}
	if g.enrollCount() != 0 {
		t.Errorf("enroll calls = %d, want 0 (stored key must not enroll)", g.enrollCount())
	}
}

func TestManagerEnsureEnrollsAndPersists(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	g, url := newMockGatewayServer(t)
	cipher := testCipher(t)

	mgr := NewManager(database, cipher, url, "", "", "Cantinarr", nil)
	client := mgr.Ensure(context.Background())
	if client == nil {
		t.Fatal("Ensure returned nil; expected auto-enroll to succeed")
	}
	if client.apiKey != "pgk_enrolled" {
		t.Errorf("client key = %q, want pgk_enrolled", client.apiKey)
	}
	if g.enrollCount() != 1 {
		t.Errorf("enroll calls = %d, want 1", g.enrollCount())
	}
	// The issued key must be persisted (encrypted) for the next start.
	if k := storedPushKey(t, database, cipher); k != "pgk_enrolled" {
		t.Errorf("stored push key = %q, want pgk_enrolled", k)
	}

	// A second Ensure is single-flight: same client, no second enroll.
	if mgr.Ensure(context.Background()) != client {
		t.Error("second Ensure returned a different client")
	}
	if g.enrollCount() != 1 {
		t.Errorf("enroll calls after second Ensure = %d, want 1", g.enrollCount())
	}
}

func TestManagerEnsureReconcilesStoredTokens(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	g, url := newMockGatewayServer(t)
	cipher := testCipher(t)

	// Two device tokens registered while push was disabled (no gateway client).
	seedDeviceToken(t, database, 1, "dev-a", "tok-a")
	seedDeviceToken(t, database, 2, "dev-b", "tok-b")

	mgr := NewManager(database, cipher, url, "pgk_explicit", "", "Cantinarr", nil)
	if mgr.Ensure(context.Background()) == nil {
		t.Fatal("Ensure returned nil")
	}

	got := map[string]bool{}
	for _, id := range g.registeredIDs() {
		got[id] = true
	}
	if len(got) != 2 || !got["dev-a"] || !got["dev-b"] {
		t.Errorf("reconciled device ids = %v, want dev-a and dev-b", g.registeredIDs())
	}
}

func TestManagerStartRetrySucceedsWhenGatewayComesUp(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	g, url := newMockGatewayServer(t)
	cipher := testCipher(t)

	// Gateway starts down: the initial Ensure fails and the client stays nil.
	g.setEnrollFails(true)

	mgr := NewManager(database, cipher, url, "", "", "Cantinarr", nil)
	mgr.retryInterval = 10 * time.Millisecond // fast retry for the test
	if mgr.Ensure(context.Background()) != nil {
		t.Fatal("Ensure should return nil while the gateway is down")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.StartRetry(ctx)

	// Bring the gateway up; the retry loop should enroll and cache a client.
	g.setEnrollFails(false)

	deadline := time.After(2 * time.Second)
	for {
		if mgr.Client() != nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("background retry did not enroll after the gateway came up")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// seedDeviceToken inserts the user, device, and push_tokens rows needed for a
// stored token, satisfying the push_tokens -> devices -> users foreign keys.
// Used by the reconciliation and prune tests.
func seedDeviceToken(t *testing.T, database *sql.DB, userID int64, deviceID, token string) {
	t.Helper()
	mustExec(t, database,
		"INSERT OR IGNORE INTO users (id, username, password_hash, role) VALUES (?, ?, '', 'user')",
		userID, "user-"+deviceID)
	mustExec(t, database,
		"INSERT INTO devices (id, user_id, device_name) VALUES (?, ?, ?)",
		deviceID, userID, "device-"+deviceID)
	mustExec(t, database,
		"INSERT INTO push_tokens (id, device_id, user_id, platform, token) VALUES (?, ?, ?, 'ios', ?)",
		"pt-"+deviceID, deviceID, userID, token)
}

// seedStoredKey persists key encrypted as the auto-enrolled push_api_key, the
// way a previous start's enrollment left it.
func seedStoredKey(t *testing.T, database *sql.DB, cipher *secrets.Cipher, key string) {
	t.Helper()
	enc, err := cipher.Encrypt(key)
	if err != nil {
		t.Fatalf("encrypt %s: %v", key, err)
	}
	mustExec(t, database, "INSERT OR REPLACE INTO settings (key, value) VALUES ('push_api_key', ?)", enc)
}

// deliveryFailures reads the notifier's consecutive-failure run.
func deliveryFailures(n *Notifier) int {
	n.healthMu.Lock()
	defer n.healthMu.Unlock()
	return n.consecutiveFails
}

// PUSH-031: a stored key the gateway stops accepting is replaced by
// re-enrolling, and every stored device token follows the server to its new
// tenant, all without a restart or a hand-deleted settings row.
func TestManagerReenrollsWhenStoredKeyRefused(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	g, url := newMockGatewayServer(t)
	cipher := testCipher(t)
	seedDeviceToken(t, database, 1, "dev-a", "tok-a")
	seedDeviceToken(t, database, 2, "dev-b", "tok-b")
	seedStoredKey(t, database, cipher, "pgk_stale")

	// The gateway still knows the stored key when the server comes up.
	g.acceptKeys("pgk_stale", "pgk_enrolled")
	mgr := NewManager(database, cipher, url, "", "", "Cantinarr", nil)
	mgr.retryInterval = 10 * time.Millisecond
	stale := mgr.Ensure(context.Background())
	if stale == nil || stale.apiKey != "pgk_stale" {
		t.Fatalf("Ensure = %+v, want a client carrying pgk_stale", stale)
	}
	if g.enrollCount() != 0 {
		t.Fatalf("enroll calls after Ensure = %d, want 0 (a stored key must not enroll)", g.enrollCount())
	}

	// Then the gateway loses the tenant (its database was replaced), so the
	// stored key is refused from here on.
	g.acceptKeys("pgk_enrolled")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.StartRetry(ctx)

	n := NewNotifier(database, mgr, nil)
	n.sendWithOptions(stale, []int64{1}, "hello", "body", nil, SendOptions{})

	var fresh *Client
	waitFor(t, "the manager to re-enroll with a new key", func() bool {
		fresh = mgr.Client()
		return fresh != nil && fresh != stale && fresh.apiKey == "pgk_enrolled"
	})
	if g.enrollCount() != 1 {
		t.Errorf("enroll calls = %d, want exactly 1", g.enrollCount())
	}
	if k := storedPushKey(t, database, cipher); k != "pgk_enrolled" {
		t.Errorf("stored push key = %q, want pgk_enrolled", k)
	}
	waitFor(t, "both devices to be re-registered with the new tenant", func() bool {
		got := map[string]bool{}
		for _, id := range g.registeredIDsWith("pgk_enrolled") {
			got[id] = true
		}
		return got["dev-a"] && got["dev-b"]
	})
	// The refused send still counts toward the delivery-health run: the
	// notification it carried is gone whatever happens next.
	waitFor(t, "the refused send to be counted", func() bool { return deliveryFailures(n) == 1 })

	// A late or duplicate report about the old client is a no-op: the burst of
	// 401s one refusal produces collapses into the single re-enrollment above.
	mgr.ReportUnauthorized(stale)
	if mgr.Client() != fresh {
		t.Error("a stale report replaced the freshly enrolled client")
	}
	if g.enrollCount() != 1 {
		t.Errorf("enroll calls after a stale report = %d, want 1", g.enrollCount())
	}
}

// PUSH-031: an explicit CANTINARR_PUSH_API_KEY the gateway refuses is never
// replaced (there is nothing to replace it with); the client stays so the
// delivery-health issue keeps reporting, and nothing enrolls.
func TestManagerExplicitKeyRefusedNeverReenrolls(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	g, url := newMockGatewayServer(t)
	g.acceptKeys("pgk_enrolled") // the operator's key is not one the gateway knows

	mgr := NewManager(database, testCipher(t), url, "pgk_explicit", "", "Cantinarr", nil)
	mgr.retryInterval = 10 * time.Millisecond
	client := mgr.Ensure(context.Background())
	if client == nil {
		t.Fatal("Ensure returned nil for explicit-key manager")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.StartRetry(ctx)

	n := NewNotifier(database, mgr, nil)
	n.sendWithOptions(client, []int64{1}, "hello", "body", nil, SendOptions{})
	waitFor(t, "the refused send to be counted", func() bool { return deliveryFailures(n) == 1 })
	// Give the retry loop several ticks to prove it has nothing to do.
	time.Sleep(50 * time.Millisecond)

	if got := mgr.Client(); got != client {
		t.Errorf("client after refusal = %+v, want the explicit-key client kept", got)
	}
	if client.apiKey != "pgk_explicit" {
		t.Errorf("client key = %q, want pgk_explicit", client.apiKey)
	}
	if g.enrollCount() != 0 {
		t.Errorf("enroll calls = %d, want 0 (an explicit key never enrolls)", g.enrollCount())
	}
	var persisted int
	if err := database.QueryRow("SELECT COUNT(*) FROM settings WHERE key = 'push_api_key'").Scan(&persisted); err != nil {
		t.Fatalf("count persisted push key: %v", err)
	}
	if persisted != 0 {
		t.Errorf("persisted push key rows = %d, want 0", persisted)
	}
	// Reporting it again, as every further 401 will, changes nothing either.
	mgr.ReportUnauthorized(client)
	if mgr.Client() != client {
		t.Error("a repeat report dropped the explicit-key client")
	}
}

// TestManagerBackoffDoublesToTheCap pins the schedule both the retry loop and
// re-enrollment wait on: the base interval doubled per consecutive failure,
// capped, and safe for any failure count.
func TestManagerBackoffDoublesToTheCap(t *testing.T) {
	mgr := NewManager(nil, nil, "http://gateway", "", "", "Cantinarr", nil)
	want := []time.Duration{
		60 * time.Second, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute,
		16 * time.Minute, 30 * time.Minute, 30 * time.Minute,
	}
	for i, w := range want {
		if got := mgr.backoffFor(i + 1); got != w {
			t.Errorf("backoffFor(%d) = %v, want %v", i+1, got, w)
		}
	}
	if got := mgr.backoffFor(1000); got != 30*time.Minute {
		t.Errorf("backoffFor(1000) = %v, want the 30m cap (no overflow)", got)
	}
	if got := mgr.backoffFor(0); got != 60*time.Second {
		t.Errorf("backoffFor(0) = %v, want the base interval", got)
	}

	// Test-sized intervals follow the same rule.
	mgr.retryInterval, mgr.maxRetryInterval = 10*time.Millisecond, 25*time.Millisecond
	for k, w := range map[int]time.Duration{1: 10 * time.Millisecond, 2: 20 * time.Millisecond, 3: 25 * time.Millisecond} {
		if got := mgr.backoffFor(k); got != w {
			t.Errorf("backoffFor(%d) = %v, want %v", k, got, w)
		}
	}
}

// TestManagerReenrollBacksOff proves a refusal does not turn into a hammer on
// /v1/enroll: with the gateway refusing enrollment too, each failed attempt
// waits twice as long as the last, and the loop still recovers on its own once
// the gateway enrolls again.
func TestManagerReenrollBacksOff(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	g, url := newMockGatewayServer(t)
	cipher := testCipher(t)
	seedStoredKey(t, database, cipher, "pgk_stale")
	g.acceptKeys("pgk_stale")

	mgr := NewManager(database, cipher, url, "", "", "Cantinarr", nil)
	mgr.retryInterval = 10 * time.Millisecond
	stale := mgr.Ensure(context.Background())
	if stale == nil {
		t.Fatal("Ensure returned nil for stored-key manager")
	}

	// The gateway loses the tenant and, for now, refuses new enrollments too.
	g.acceptKeys("pgk_enrolled")
	g.setEnrollFails(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.StartRetry(ctx)
	n := NewNotifier(database, mgr, nil)
	n.sendWithOptions(stale, []int64{1}, "hello", "body", nil, SendOptions{})

	waitFor(t, "the first re-enrollment attempt", func() bool { return g.enrollCount() >= 1 })
	time.Sleep(300 * time.Millisecond)
	// Attempts wait 10, 20, 40, 80, then 160ms after the first, so 300ms can
	// hold at most six in total whatever the scheduler does; a fixed 10ms
	// cadence would have made around thirty.
	if got := g.enrollCount(); got > 6 {
		t.Errorf("enroll calls in 300ms = %d, want at most 6 (backoff not doubling)", got)
	}

	// Once the gateway enrolls again the loop recovers without help.
	g.setEnrollFails(false)
	waitFor(t, "re-enrollment once the gateway allows it", func() bool {
		c := mgr.Client()
		return c != nil && c.apiKey == "pgk_enrolled"
	})
	if k := storedPushKey(t, database, cipher); k != "pgk_enrolled" {
		t.Errorf("stored push key = %q, want pgk_enrolled", k)
	}
}
