package grokoauth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// fakeAuthServer scripts the xAI device-flow endpoints. Token responses are
// consumed in order; the last one repeats.
type fakeAuthServer struct {
	t *testing.T

	mu             sync.Mutex
	deviceRequests int
	tokenRequests  int
	tokenResponses []fakeTokenResponse
	deviceResponse map[string]any
}

type fakeTokenResponse struct {
	status int
	body   map[string]any
}

func newFakeAuthServer(t *testing.T) (*fakeAuthServer, *httptest.Server) {
	t.Helper()
	fake := &fakeAuthServer{t: t}
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/device/code", func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		fake.deviceRequests++
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse device form: %v", err)
		}
		if got := r.PostForm.Get("client_id"); got == "" {
			t.Error("device request missing client_id")
		}
		if got := r.PostForm.Get("scope"); got != oauthScope {
			t.Errorf("device scope = %q", got)
		}
		response := fake.deviceResponse
		if response == nil {
			response = map[string]any{
				"device_code":               "device-code-1",
				"user_code":                 "GROK-1234",
				"verification_uri":          "https://auth.x.ai/oauth2/device",
				"verification_uri_complete": "https://accounts.x.ai/oauth2/device?user_code=GROK-1234",
				"expires_in":                900,
				"interval":                  1,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	})
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse token form: %v", err)
		}
		index := fake.tokenRequests
		fake.tokenRequests++
		if len(fake.tokenResponses) == 0 {
			t.Error("unexpected token request")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if index >= len(fake.tokenResponses) {
			index = len(fake.tokenResponses) - 1
		}
		scripted := fake.tokenResponses[index]
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(scripted.status)
		_ = json.NewEncoder(w).Encode(scripted.body)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return fake, server
}

func testIDToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	segment := base64.RawURLEncoding.EncodeToString
	return segment([]byte(`{"alg":"none"}`)) + "." + segment(payload) + "." + segment([]byte("sig"))
}

func newTestManager(t *testing.T, serverURL string) (*Manager, *sql.DB, int64, *time.Time) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	result, err := database.Exec(`INSERT INTO users (username, password_hash, role) VALUES ('grok-user', '', 'user')`)
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := result.LastInsertId()
	manager := NewManager(database, cipher, Options{AuthBaseURL: serverURL})
	now := time.Now()
	manager.nowFn = func() time.Time { return now }
	return manager, database, userID, &now
}

func successTokenBody(t *testing.T, email string) map[string]any {
	t.Helper()
	return map[string]any{
		"access_token":  "access-1",
		"refresh_token": "refresh-1",
		"id_token":      testIDToken(t, map[string]any{"email": email, "plan": "supergrok"}),
		"expires_in":    3600,
	}
}

func TestDeviceLoginFlowConnectsAndPersistsEncrypted(t *testing.T) {
	fake, server := newFakeAuthServer(t)
	manager, database, userID, now := newTestManager(t, server.URL)
	fake.tokenResponses = []fakeTokenResponse{
		{status: http.StatusBadRequest, body: map[string]any{"error": "authorization_pending"}},
		{status: http.StatusBadRequest, body: map[string]any{"error": "slow_down"}},
		{status: http.StatusOK, body: successTokenBody(t, "julian@example.com")},
	}

	login, err := manager.BeginDeviceLogin(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if login.UserCode != "GROK-1234" || login.FlowID == "" {
		t.Fatalf("login = %#v", login)
	}
	if login.VerificationURI != "https://accounts.x.ai/oauth2/device?user_code=GROK-1234" {
		t.Fatalf("verification uri = %q", login.VerificationURI)
	}

	// Before the poll interval elapses no upstream request is made.
	check, err := manager.CheckDeviceLogin(context.Background(), userID, login.FlowID)
	if err != nil || check.Status != LoginPending {
		t.Fatalf("early check = %#v err=%v", check, err)
	}
	if fake.tokenRequests != 0 {
		t.Fatalf("token requests before interval = %d", fake.tokenRequests)
	}

	*now = now.Add(2 * time.Second)
	if check, err = manager.CheckDeviceLogin(context.Background(), userID, login.FlowID); err != nil || check.Status != LoginPending {
		t.Fatalf("pending check = %#v err=%v", check, err)
	}
	// slow_down stretches the interval by five seconds; the next check inside
	// the stretched window must not hit upstream.
	*now = now.Add(2 * time.Second)
	if check, err = manager.CheckDeviceLogin(context.Background(), userID, login.FlowID); err != nil || check.Status != LoginPending {
		t.Fatalf("slow_down check = %#v err=%v", check, err)
	}
	requestsAfterSlowDown := fake.tokenRequests
	*now = now.Add(2 * time.Second)
	if check, err = manager.CheckDeviceLogin(context.Background(), userID, login.FlowID); err != nil || check.Status != LoginPending {
		t.Fatalf("stretched-window check = %#v err=%v", check, err)
	}
	if fake.tokenRequests != requestsAfterSlowDown {
		t.Fatalf("token requests inside stretched window grew: %d -> %d", requestsAfterSlowDown, fake.tokenRequests)
	}

	*now = now.Add(10 * time.Second)
	check, err = manager.CheckDeviceLogin(context.Background(), userID, login.FlowID)
	if err != nil {
		t.Fatal(err)
	}
	if check.Status != LoginConnected || check.Account.Email != "julian@example.com" || check.Account.PlanType != "supergrok" {
		t.Fatalf("connected check = %#v", check)
	}

	connected, err := manager.AccountExists(PersonalAccount(userID))
	if err != nil || !connected {
		t.Fatalf("connected=%t err=%v", connected, err)
	}
	var blob string
	if err := database.QueryRow(`SELECT auth_blob FROM user_grok_accounts WHERE user_id = ?`, userID).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	if !secrets.IsEncrypted(blob) {
		t.Fatalf("auth_blob stored unencrypted: %q", blob)
	}

	// The completed flow is gone.
	if _, err := manager.CheckDeviceLogin(context.Background(), userID, login.FlowID); !errors.Is(err, ErrFlowNotFound) {
		t.Fatalf("finished-flow check err = %v", err)
	}
}

func TestDeviceLoginDeniedFailsAndRemovesFlow(t *testing.T) {
	fake, server := newFakeAuthServer(t)
	manager, _, userID, now := newTestManager(t, server.URL)
	fake.tokenResponses = []fakeTokenResponse{
		{status: http.StatusBadRequest, body: map[string]any{"error": "access_denied"}},
	}
	login, err := manager.BeginDeviceLogin(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Second)
	check, err := manager.CheckDeviceLogin(context.Background(), userID, login.FlowID)
	if err != nil || check.Status != LoginFailed {
		t.Fatalf("denied check = %#v err=%v", check, err)
	}
	if _, err := manager.CheckDeviceLogin(context.Background(), userID, login.FlowID); !errors.Is(err, ErrFlowNotFound) {
		t.Fatalf("denied-flow recheck err = %v", err)
	}
}

func TestDeviceLoginUpstreamExpiryMapsToFlowExpired(t *testing.T) {
	fake, server := newFakeAuthServer(t)
	manager, _, userID, now := newTestManager(t, server.URL)
	fake.tokenResponses = []fakeTokenResponse{
		{status: http.StatusBadRequest, body: map[string]any{"error": "expired_token"}},
	}
	login, err := manager.BeginDeviceLogin(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Second)
	if _, err := manager.CheckDeviceLogin(context.Background(), userID, login.FlowID); !errors.Is(err, ErrFlowExpired) {
		t.Fatalf("expired check err = %v", err)
	}
}

func TestBeginReplacesOwnFlowRejectsOthersAndConnectedAccount(t *testing.T) {
	fake, server := newFakeAuthServer(t)
	manager, database, userID, now := newTestManager(t, server.URL)
	fake.tokenResponses = []fakeTokenResponse{
		{status: http.StatusOK, body: successTokenBody(t, "julian@example.com")},
	}
	login, err := manager.BeginDeviceLogin(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	// The same actor starting over replaces their own orphaned flow instead
	// of being locked out until it expires.
	replacement, err := manager.BeginDeviceLogin(context.Background(), userID)
	if err != nil {
		t.Fatalf("same-actor second begin err = %v", err)
	}
	if _, err := manager.CheckDeviceLogin(context.Background(), userID, login.FlowID); !errors.Is(err, ErrFlowNotFound) {
		t.Fatalf("replaced flow check err = %v", err)
	}
	// A different actor cannot steal the shared slot mid-flow.
	result, err := database.Exec(`INSERT INTO users (username, password_hash, role) VALUES ('rival', '', 'admin')`)
	if err != nil {
		t.Fatal(err)
	}
	rivalID, _ := result.LastInsertId()
	if _, err := manager.BeginDeviceLoginForAccount(context.Background(), SharedAccount(), userID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.BeginDeviceLoginForAccount(context.Background(), SharedAccount(), rivalID); !errors.Is(err, ErrLoginInProgress) {
		t.Fatalf("cross-actor begin err = %v", err)
	}
	if err := manager.CancelDeviceLogin(userID, replacement.FlowID); err != nil {
		t.Fatal(err)
	}
	login, err = manager.BeginDeviceLogin(context.Background(), userID)
	if err != nil {
		t.Fatalf("begin after cancel: %v", err)
	}
	*now = now.Add(2 * time.Second)
	if check, err := manager.CheckDeviceLogin(context.Background(), userID, login.FlowID); err != nil || check.Status != LoginConnected {
		t.Fatalf("check = %#v err=%v", check, err)
	}
	if _, err := manager.BeginDeviceLogin(context.Background(), userID); !errors.Is(err, ErrAlreadyConnected) {
		t.Fatalf("begin while connected err = %v", err)
	}
}

func TestFlowsAreOwnedByActorAndAccount(t *testing.T) {
	fake, server := newFakeAuthServer(t)
	manager, database, userID, _ := newTestManager(t, server.URL)
	fake.tokenResponses = []fakeTokenResponse{
		{status: http.StatusBadRequest, body: map[string]any{"error": "authorization_pending"}},
	}
	result, err := database.Exec(`INSERT INTO users (username, password_hash, role) VALUES ('other', '', 'user')`)
	if err != nil {
		t.Fatal(err)
	}
	otherID, _ := result.LastInsertId()
	login, err := manager.BeginDeviceLogin(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CheckDeviceLogin(context.Background(), otherID, login.FlowID); !errors.Is(err, ErrFlowNotFound) {
		t.Fatalf("cross-user check err = %v", err)
	}
	if _, err := manager.CheckDeviceLoginForAccount(context.Background(), SharedAccount(), userID, login.FlowID); !errors.Is(err, ErrFlowNotFound) {
		t.Fatalf("cross-account check err = %v", err)
	}
	if err := manager.CancelDeviceLogin(otherID, login.FlowID); !errors.Is(err, ErrFlowNotFound) {
		t.Fatalf("cross-user cancel err = %v", err)
	}
}

func TestBeginRejectsUntrustedVerificationHost(t *testing.T) {
	fake, server := newFakeAuthServer(t)
	manager, _, userID, _ := newTestManager(t, server.URL)
	fake.deviceResponse = map[string]any{
		"device_code":      "device-code-1",
		"user_code":        "GROK-1234",
		"verification_uri": "https://evil.example/oauth2/device",
		"expires_in":       900,
		"interval":         1,
	}
	if _, err := manager.BeginDeviceLogin(context.Background(), userID); !errors.Is(err, ErrProvider) {
		t.Fatalf("untrusted host begin err = %v", err)
	}
	// The failed begin releases the account's login slot.
	fake.deviceResponse = nil
	if _, err := manager.BeginDeviceLogin(context.Background(), userID); err != nil {
		t.Fatalf("begin after rejected verification uri: %v", err)
	}
}

func TestAccessTokenReturnsStoredTokenWithoutRefresh(t *testing.T) {
	fake, server := newFakeAuthServer(t)
	manager, _, userID, now := newTestManager(t, server.URL)
	account := PersonalAccount(userID)
	auth := storedAuth{
		AccessToken:  "stored-access",
		RefreshToken: "stored-refresh",
		ObtainedAt:   now.Unix(),
		ExpiresAt:    now.Add(6 * time.Hour).Unix(),
	}
	if err := manager.saveAccount(account, auth, "julian@example.com", "supergrok"); err != nil {
		t.Fatal(err)
	}
	token, err := manager.AccessToken(context.Background(), account)
	if err != nil || token != "stored-access" {
		t.Fatalf("token=%q err=%v", token, err)
	}
	if fake.tokenRequests != 0 {
		t.Fatalf("unexpected refresh requests: %d", fake.tokenRequests)
	}
}

func TestAccessTokenRefreshRotatesAndPersists(t *testing.T) {
	fake, server := newFakeAuthServer(t)
	manager, _, userID, now := newTestManager(t, server.URL)
	account := PersonalAccount(userID)
	auth := storedAuth{
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		ObtainedAt:   now.Add(-6 * time.Hour).Unix(),
		ExpiresAt:    now.Add(time.Minute).Unix(),
	}
	if err := manager.saveAccount(account, auth, "julian@example.com", ""); err != nil {
		t.Fatal(err)
	}
	fake.tokenResponses = []fakeTokenResponse{
		{status: http.StatusOK, body: map[string]any{
			"access_token":  "new-access",
			"refresh_token": "new-refresh",
			"expires_in":    3600,
		}},
	}
	token, err := manager.AccessToken(context.Background(), account)
	if err != nil || token != "new-access" {
		t.Fatalf("token=%q err=%v", token, err)
	}
	if fake.tokenRequests != 1 {
		t.Fatalf("refresh requests = %d", fake.tokenRequests)
	}
	// The rotated pair was persisted: the next resolution is local.
	token, err = manager.AccessToken(context.Background(), account)
	if err != nil || token != "new-access" {
		t.Fatalf("second token=%q err=%v", token, err)
	}
	if fake.tokenRequests != 1 {
		t.Fatalf("refresh requests after persisted rotation = %d", fake.tokenRequests)
	}
	record, found, err := manager.loadAccount(account)
	if err != nil || !found || record.auth.RefreshToken != "new-refresh" {
		t.Fatalf("record=%#v found=%t err=%v", record, found, err)
	}
}

func TestAccessTokenInvalidGrantRequiresRelogin(t *testing.T) {
	fake, server := newFakeAuthServer(t)
	manager, _, userID, now := newTestManager(t, server.URL)
	account := PersonalAccount(userID)
	auth := storedAuth{
		AccessToken:  "old-access",
		RefreshToken: "revoked-refresh",
		ObtainedAt:   now.Add(-6 * time.Hour).Unix(),
		ExpiresAt:    now.Add(-time.Minute).Unix(),
	}
	if err := manager.saveAccount(account, auth, "", ""); err != nil {
		t.Fatal(err)
	}
	fake.tokenResponses = []fakeTokenResponse{
		{status: http.StatusBadRequest, body: map[string]any{"error": "invalid_grant"}},
	}
	if _, err := manager.AccessToken(context.Background(), account); !errors.Is(err, ErrReloginRequired) {
		t.Fatalf("invalid_grant err = %v", err)
	}
	// The identity row survives so the UI can still show what to reconnect.
	if connected, err := manager.AccountExists(account); err != nil || !connected {
		t.Fatalf("connected=%t err=%v", connected, err)
	}
}

func TestAccessTokenWithoutAccountFailsClosed(t *testing.T) {
	_, server := newFakeAuthServer(t)
	manager, _, userID, _ := newTestManager(t, server.URL)
	if _, err := manager.AccessToken(context.Background(), PersonalAccount(userID)); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("missing-account err = %v", err)
	}
}

func TestCorruptStoredRowFailsClosed(t *testing.T) {
	_, server := newFakeAuthServer(t)
	manager, database, userID, _ := newTestManager(t, server.URL)
	if _, err := database.Exec(
		`INSERT INTO user_grok_accounts (user_id, auth_blob) VALUES (?, ?)`,
		userID, `{"access_token":"plaintext"}`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AccountExists(PersonalAccount(userID)); !errors.Is(err, ErrStorage) {
		t.Fatalf("plaintext row err = %v", err)
	}
}

func TestUnlinkRemovesAccountAndPendingFlow(t *testing.T) {
	fake, server := newFakeAuthServer(t)
	manager, _, userID, now := newTestManager(t, server.URL)
	fake.tokenResponses = []fakeTokenResponse{
		{status: http.StatusOK, body: successTokenBody(t, "julian@example.com")},
	}
	login, err := manager.BeginDeviceLogin(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Second)
	if check, err := manager.CheckDeviceLogin(context.Background(), userID, login.FlowID); err != nil || check.Status != LoginConnected {
		t.Fatalf("check = %#v err=%v", check, err)
	}
	if err := manager.Unlink(userID); err != nil {
		t.Fatal(err)
	}
	if connected, err := manager.AccountExists(PersonalAccount(userID)); err != nil || connected {
		t.Fatalf("connected=%t err=%v after unlink", connected, err)
	}
	status, err := manager.StatusForAccount(PersonalAccount(userID))
	if err != nil || status.Connected {
		t.Fatalf("status=%#v err=%v after unlink", status, err)
	}
}

func TestSharedAndPersonalAccountsAreIndependent(t *testing.T) {
	fake, server := newFakeAuthServer(t)
	manager, _, userID, now := newTestManager(t, server.URL)
	fake.tokenResponses = []fakeTokenResponse{
		{status: http.StatusOK, body: successTokenBody(t, "admin@example.com")},
	}
	login, err := manager.BeginDeviceLoginForAccount(context.Background(), SharedAccount(), userID)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Second)
	check, err := manager.CheckDeviceLoginForAccount(context.Background(), SharedAccount(), userID, login.FlowID)
	if err != nil || check.Status != LoginConnected {
		t.Fatalf("shared check = %#v err=%v", check, err)
	}
	if connected, err := manager.AccountExists(SharedAccount()); err != nil || !connected {
		t.Fatalf("shared connected=%t err=%v", connected, err)
	}
	if connected, err := manager.AccountExists(PersonalAccount(userID)); err != nil || connected {
		t.Fatalf("personal connected=%t err=%v", connected, err)
	}
	status, err := manager.StatusForAccount(SharedAccount())
	if err != nil || !status.Connected || status.Email != "admin@example.com" {
		t.Fatalf("shared status = %#v err=%v", status, err)
	}
}

func TestPendingRotatedPairSurvivesSaveFailureAndLandsLater(t *testing.T) {
	fake, server := newFakeAuthServer(t)
	manager, _, userID, now := newTestManager(t, server.URL)
	account := PersonalAccount(userID)
	if err := manager.saveAccount(account, storedAuth{
		AccessToken:  "old-access",
		RefreshToken: "burned-refresh",
		ObtainedAt:   now.Add(-6 * time.Hour).Unix(),
		ExpiresAt:    now.Add(-time.Minute).Unix(),
	}, "julian@example.com", ""); err != nil {
		t.Fatal(err)
	}
	// Simulate a persist failure after the upstream rotation consumed the old
	// refresh token: stash the rotated pair exactly as AccessToken would.
	rotated := storedAuth{
		AccessToken:  "rotated-access",
		RefreshToken: "rotated-refresh",
		ObtainedAt:   now.Unix(),
		ExpiresAt:    now.Add(time.Hour).Unix(),
	}
	manager.putPending(account, rotated)

	// The pending pair is authoritative, is served without a refresh, and is
	// persisted on the next resolution.
	token, err := manager.AccessToken(context.Background(), account)
	if err != nil || token != "rotated-access" {
		t.Fatalf("token=%q err=%v", token, err)
	}
	if fake.tokenRequests != 0 {
		t.Fatalf("unexpected refresh requests: %d", fake.tokenRequests)
	}
	if _, ok := manager.peekPending(account); ok {
		t.Fatal("pending pair not cleared after a successful persist")
	}
	record, found, err := manager.loadAccount(account)
	if err != nil || !found || record.auth.RefreshToken != "rotated-refresh" {
		t.Fatalf("record=%#v found=%t err=%v", record, found, err)
	}
	if err := manager.UnlinkAccount(account); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.peekPending(account); ok {
		t.Fatal("unlink left a pending pair behind")
	}
}

func TestTokenExpiryFallsBackToJWTExpThenDefault(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	withExpiresIn := tokenResponse{AccessToken: "a", ExpiresIn: 600}
	if got := tokenExpiryUnix(withExpiresIn, now); got != now.Add(10*time.Minute).Unix() {
		t.Fatalf("expires_in expiry = %d", got)
	}
	jwtExp := now.Add(45 * time.Minute).Unix()
	segment := base64.RawURLEncoding.EncodeToString
	payload, err := json.Marshal(map[string]any{"exp": jwtExp})
	if err != nil {
		t.Fatal(err)
	}
	jwtToken := segment([]byte(`{"alg":"none"}`)) + "." + segment(payload) + "." + segment([]byte("sig"))
	if got := tokenExpiryUnix(tokenResponse{AccessToken: jwtToken}, now); got != jwtExp {
		t.Fatalf("jwt expiry = %d, want %d", got, jwtExp)
	}
	if got := tokenExpiryUnix(tokenResponse{AccessToken: "opaque"}, now); got != now.Add(defaultTokenLifetime).Unix() {
		t.Fatalf("default expiry = %d", got)
	}
}

func TestTokenExpiringAdaptsSkewToShortLifetimes(t *testing.T) {
	now := time.Now()
	longLived := storedAuth{
		AccessToken: "a",
		ObtainedAt:  now.Add(-time.Hour).Unix(),
		ExpiresAt:   now.Add(6 * time.Minute).Unix(),
	}
	if !tokenExpiring(longLived, now.Add(2*time.Minute)) {
		t.Fatal("long-lived token inside skew should refresh")
	}
	shortLived := storedAuth{
		AccessToken: "a",
		ObtainedAt:  now.Unix(),
		ExpiresAt:   now.Add(10 * time.Minute).Unix(),
	}
	if tokenExpiring(shortLived, now.Add(4*time.Minute)) {
		t.Fatal("short-lived token refreshed too eagerly")
	}
	if !tokenExpiring(shortLived, now.Add(9*time.Minute)) {
		t.Fatal("short-lived token near expiry should refresh")
	}
	if tokenExpiring(storedAuth{AccessToken: "a"}, now) {
		t.Fatal("token without expiry metadata must not refresh-loop")
	}
}
