package push

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

// mockGateway records enroll calls and device registrations. enrollFails, when
// set, makes /v1/enroll return 503 so the background-retry path can be driven.
type mockGateway struct {
	mu                sync.Mutex
	enrollCalls       int
	registered        []string // device ids seen at /v1/devices
	enrollFails       bool
	notificationCalls int
	notificationAuth  string
}

func (g *mockGateway) setEnrollFails(v bool) {
	g.mu.Lock()
	g.enrollFails = v
	g.mu.Unlock()
}

func (g *mockGateway) enrollCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.enrollCalls
}

func (g *mockGateway) registeredIDs() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]string, len(g.registered))
	copy(out, g.registered)
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
	g := &mockGateway{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/enroll":
			g.mu.Lock()
			g.enrollCalls++
			fail := g.enrollFails
			g.mu.Unlock()
			if fail {
				http.Error(w, `{"error":"unavailable"}`, http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"tenant_id":"t-1","api_key":"pgk_enrolled"}`)
		case "/v1/devices":
			body := map[string]any{}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			if id, _ := body["device_id"].(string); id != "" {
				g.mu.Lock()
				g.registered = append(g.registered, id)
				g.mu.Unlock()
			}
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"id":"d","created":true}`)
		case "/v1/notifications":
			g.mu.Lock()
			g.notificationCalls++
			g.notificationAuth = r.Header.Get("Authorization")
			g.mu.Unlock()
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
