package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
	"github.com/windoze95/cantinarr-server/internal/httpx"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"golang.org/x/oauth2"
)

// The IdP performs a real code exchange with PKCE and serves discovery, JWKS
// and UserInfo over HTTP. Tests never substitute a verified principal for an
// ID token except when deliberately exercising store expiry/concurrency.
type testOIDCProvider struct {
	server     *httptest.Server
	key        *rsa.PrivateKey
	mu         sync.Mutex
	codes      map[string]url.Values
	claims     map[string]any
	info       map[string]any
	mode       string
	jwksCalls  atomic.Int32
	tokenCalls atomic.Int32
	infoCalls  atomic.Int32
	block      chan struct{}
	entered    chan struct{}
}

func newTestOIDCProvider(t *testing.T) *testOIDCProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &testOIDCProvider{key: key, codes: map[string]url.Values{}, claims: map[string]any{"sub": "subject-1", "preferred_username": "viewer", "groups": []string{"family"}}, info: map[string]any{"sub": "subject-1"}}
	p.server = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.server.Close)
	return p
}
func (p *testOIDCProvider) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		if p.mode == "discovery_failure" {
			http.Error(w, "offline", 503)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": p.server.URL, "authorization_endpoint": p.server.URL + "/authorize", "token_endpoint": p.server.URL + "/token", "jwks_uri": p.server.URL + "/jwks", "userinfo_endpoint": p.server.URL + "/userinfo", "id_token_signing_alg_values_supported": []string{"RS256"}})
	case "/authorize":
		q := r.URL.Query()
		code, _ := randomURLToken(24)
		p.mu.Lock()
		p.codes[code] = q
		p.mu.Unlock()
		callback, _ := url.Parse(q.Get("redirect_uri"))
		params := callback.Query()
		params.Set("state", q.Get("state"))
		params.Set("code", code)
		callback.RawQuery = params.Encode()
		http.Redirect(w, r, callback.String(), 302)
	case "/token":
		p.tokenCalls.Add(1)
		if p.block != nil {
			close(p.entered)
			<-p.block
		}
		if p.mode == "token_failure" {
			http.Error(w, "offline", 503)
			return
		}
		_ = r.ParseForm()
		p.mu.Lock()
		q, ok := p.codes[r.Form.Get("code")]
		delete(p.codes, r.Form.Get("code"))
		p.mu.Unlock()
		if !ok || !verifyPKCES256(r.Form.Get("code_verifier"), q.Get("code_challenge")) || r.Form.Get("redirect_uri") != q.Get("redirect_uri") {
			http.Error(w, "bad code or PKCE", 400)
			return
		}
		claims := jwt.MapClaims{"iss": p.server.URL, "aud": "client", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": q.Get("nonce")}
		for k, v := range p.claims {
			claims[k] = v
		}
		key := p.key
		if p.mode == "signature" {
			key, _ = rsa.GenerateKey(rand.Reader, 2048)
		}
		switch p.mode {
		case "issuer":
			claims["iss"] = "https://wrong.example"
		case "audience":
			claims["aud"] = "wrong"
		case "nonce":
			claims["nonce"] = "wrong"
		case "expired":
			claims["exp"] = time.Now().Add(-time.Hour).Unix()
		case "no_subject":
			delete(claims, "sub")
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		token.Header["kid"] = "current"
		signed, _ := token.SignedString(key)
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "provider-access", "token_type": "Bearer", "id_token": signed})
	case "/jwks":
		p.jwksCalls.Add(1)
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &p.key.PublicKey, KeyID: "current", Algorithm: "RS256", Use: "sig"}}})
	case "/userinfo":
		p.infoCalls.Add(1)
		if p.mode == "userinfo_failure" {
			http.Error(w, "offline", 503)
			return
		}
		_ = json.NewEncoder(w).Encode(p.info)
	default:
		http.NotFound(w, r)
	}
}

type oidcFixture struct {
	s     *Service
	p     *testOIDCProvider
	admin *Claims
}

func newOIDCFixture(t *testing.T) *oidcFixture {
	t.Helper()
	s := setupTestService(t)
	p := newTestOIDCProvider(t)
	cipher, err := secrets.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	s.SetOIDCCipher(cipher)
	_, err = s.db.Exec(`INSERT INTO settings(key,value) VALUES ('server_settings','{"external_url":"http://localhost:8585"}')`)
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.Login("admin", "testpass123", "admin", "")
	if err != nil {
		t.Fatal(err)
	}
	admin := &Claims{UserID: session.User.ID, DeviceID: session.DeviceID}
	secret := "client-secret"
	_, err = s.saveOIDCConfig(OIDCConfig{Enabled: true, Issuer: p.server.URL, ClientID: "client", ClientSecret: &secret, Label: "Family", GroupClaim: "groups"}, admin)
	if err != nil {
		t.Fatal(err)
	}
	return &oidcFixture{s: s, p: p, admin: admin}
}
func (f *oidcFixture) update(t *testing.T, change func(*OIDCConfig)) {
	t.Helper()
	c, err := f.s.oidcConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	change(&c.OIDCConfig)
	if _, err = f.s.saveOIDCConfig(c.OIDCConfig, f.admin); err != nil {
		t.Fatal(err)
	}
}

type oidcTestFlow struct {
	flow, verifier string
	callback       *url.URL
	cookie         *http.Cookie
}

func (f *oidcFixture) start(t *testing.T, purpose string, actor *Claims, invite string) oidcTestFlow {
	t.Helper()
	verifier := oauth2.GenerateVerifier()
	req := oidcBeginRequest{Client: "mobile", Challenge: oauth2.S256ChallengeFromVerifier(verifier), Invitation: invite, DeviceName: "Phone", HardwareID: "same-phone"}
	result, err := f.s.beginOIDC(req, purpose, actor)
	if err != nil {
		t.Fatal(err)
	}
	return f.beginBrowser(t, result, verifier)
}
func (f *oidcFixture) beginBrowser(t *testing.T, result map[string]string, verifier string) oidcTestFlow {
	t.Helper()

	record := httptest.NewRecorder()
	NewHandler(f.s).OIDCStart(record, httptest.NewRequest("GET", result["start_url"], nil))
	if record.Code != 302 {
		t.Fatalf("start %d %s", record.Code, record.Body.String())
	}
	client := &http.Client{Transport: http.DefaultTransport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(record.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	callback, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return oidcTestFlow{flow: result["flow"], verifier: verifier, callback: callback, cookie: record.Result().Cookies()[0]}
}
func (f *oidcFixture) callback(t *testing.T, flow oidcTestFlow) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", flow.callback.String(), nil)
	if flow.cookie != nil {
		r.AddCookie(flow.cookie)
	}
	w := httptest.NewRecorder()
	NewHandler(f.s).OIDCCallback(w, r)
	return w
}
func (f *oidcFixture) handoff(t *testing.T, flow oidcTestFlow) string {
	t.Helper()
	w := f.callback(t, flow)
	if w.Code != 302 {
		t.Fatalf("callback %d %s", w.Code, w.Body.String())
	}
	u, _ := url.Parse(w.Header().Get("Location"))
	if u.Scheme != "cantinarr" || u.Host != "oidc" {
		t.Fatalf("bad return %s", u)
	}
	if strings.Contains(u.String(), "provider-access") || strings.Contains(u.String(), "client-secret") {
		t.Fatal("secret in redirect")
	}
	return u.Query().Get("code")
}
func (f *oidcFixture) complete(t *testing.T, purpose string, actor *Claims, invite string) (any, error) {
	t.Helper()
	flow := f.start(t, purpose, actor, invite)
	code := f.handoff(t, flow)
	return f.s.exchangeOIDC(code, flow.verifier, flow.flow)
}
func (f *oidcFixture) count(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := f.s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestOIDCTestAndExplicitLink(t *testing.T) {
	f := newOIDCFixture(t)
	if _, err := f.complete(t, "login", nil, ""); !errors.Is(err, ErrOIDCUnlinked) {
		t.Fatalf("unlinked %v", err)
	}
	users := f.count(t, "users")
	devices := f.count(t, "devices")
	if _, err := f.complete(t, "test", f.admin, ""); err != nil {
		t.Fatal(err)
	}
	if f.count(t, "users") != users || f.count(t, "devices") != devices || f.count(t, "oidc_identities") != 0 {
		t.Fatal("test changed accounts")
	}
	c, _ := f.s.oidcConfiguration()
	if !c.Tested {
		t.Fatal("test not recorded")
	}
	raw, _ := json.Marshal(c.OIDCConfig)
	if strings.Contains(string(raw), "client-secret") || strings.Contains(string(raw), "enc:v1:") {
		t.Fatal("secret readable")
	}
	var saved string
	_ = f.s.db.QueryRow("SELECT value FROM settings WHERE key='oidc_config'").Scan(&saved)
	if strings.Contains(saved, "client-secret") || !strings.Contains(saved, "enc:v1:") {
		t.Fatal("secret not encrypted")
	}
	if _, err := f.complete(t, "link", f.admin, ""); err != nil {
		t.Fatal(err)
	}
	result, err := f.complete(t, "login", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	session := result.(*TokenResponse)
	if session.User.ID != f.admin.UserID || session.User.Role != RoleAdmin {
		t.Fatal("link lost role")
	}
	if _, _, err = f.s.AuthenticateToken(session.AccessToken); err != nil {
		t.Fatal(err)
	}
	// Provider downtime and later group changes do not shorten existing sessions.
	f.p.mode = "discovery_failure"
	if _, err = f.s.Refresh(session.RefreshToken); err != nil {
		t.Fatal(err)
	}
}
func TestOIDCRejectsInvalidProviderTokens(t *testing.T) {
	for _, mode := range []string{"signature", "issuer", "audience", "nonce", "expired", "no_subject", "token_failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newOIDCFixture(t)
			f.update(t, func(c *OIDCConfig) { c.AutoCreate = true })
			f.p.mode = mode
			if _, err := f.complete(t, "login", nil, ""); err == nil {
				t.Fatal("accepted invalid token")
			}
			if f.count(t, "users") != 1 || f.count(t, "oidc_identities") != 0 {
				t.Fatal("failed sign-in wrote account")
			}
		})
	}
}
func TestOIDCStateCookieReplayAndHandoffBinding(t *testing.T) {
	f := newOIDCFixture(t)
	f.update(t, func(c *OIDCConfig) { c.AutoCreate = true })
	flow := f.start(t, "login", nil, "")
	stolen := flow
	stolen.cookie = nil
	if w := f.callback(t, stolen); w.Code == 302 {
		t.Fatal("missing cookie accepted")
	}
	bad := *flow.callback
	q := bad.Query()
	q.Set("state", "wrong")
	bad.RawQuery = q.Encode()
	stolen = flow
	stolen.callback = &bad
	if w := f.callback(t, stolen); w.Code == 302 {
		t.Fatal("wrong state accepted")
	}
	code := f.handoff(t, flow)
	if w := f.callback(t, flow); w.Code == 302 {
		t.Fatal("callback replayed")
	}
	if _, err := f.s.exchangeOIDC(code, oauth2.GenerateVerifier(), flow.flow); err == nil {
		t.Fatal("stolen code exchanged")
	}
	if _, err := f.s.exchangeOIDC(code, flow.verifier, "wrong"); err == nil {
		t.Fatal("wrong initiating flow accepted")
	}
	if _, err := f.s.exchangeOIDC(code, flow.verifier, flow.flow); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.exchangeOIDC(code, flow.verifier, flow.flow); err == nil {
		t.Fatal("handoff replayed")
	}
}
func TestOIDCConcurrentExchangeCreatesOneSession(t *testing.T) {
	f := newOIDCFixture(t)
	f.update(t, func(c *OIDCConfig) { c.AutoCreate = true })
	flow := f.start(t, "login", nil, "")
	code := f.handoff(t, flow)
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := f.s.exchangeOIDC(code, flow.verifier, flow.flow); e == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || f.count(t, "users") != 2 || f.count(t, "devices") != 2 {
		t.Fatal("multiple sessions issued")
	}
}
func TestOIDCGroupsAndUserInfo(t *testing.T) {
	cases := []struct {
		name      string
		groups    any
		info      map[string]any
		wantError bool
	}{
		{"exact", []string{"family"}, nil, false}, {"wrong case", []string{"Family"}, nil, true}, {"string", "family", nil, true}, {"mixed", []any{"family", 3}, nil, true}, {"empty", []string{}, nil, true},
		{"missing", nil, map[string]any{"sub": "subject-1"}, true}, {"supplement", nil, map[string]any{"sub": "subject-1", "groups": []string{"family"}}, false}, {"wrong subject", nil, map[string]any{"sub": "other", "groups": []string{"family"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newOIDCFixture(t)
			f.update(t, func(c *OIDCConfig) { c.AutoCreate = true; c.AllowedGroups = []string{"family"} })
			if tc.groups == nil {
				delete(f.p.claims, "groups")
			} else {
				f.p.claims["groups"] = tc.groups
			}
			if tc.info != nil {
				f.p.info = tc.info
			}
			_, err := f.complete(t, "login", nil, "")
			if (err != nil) != tc.wantError {
				t.Fatalf("error %v", err)
			}
		})
	}
}
func TestOIDCConfigChangesAndExpiryInvalidateAttempts(t *testing.T) {
	f := newOIDCFixture(t)
	flow := f.start(t, "test", f.admin, "")
	f.update(t, func(c *OIDCConfig) { c.Label = "Changed" })
	if f.callback(t, flow).Code == 302 {
		t.Fatal("changed config callback accepted")
	}
	flow = f.start(t, "test", f.admin, "")
	code := f.handoff(t, flow)
	f.s.oidcFlows.mu.Lock()
	hand := f.s.oidcFlows.handoffs[hashToken(code)]
	hand.Expires = time.Now().Add(-time.Second)
	f.s.oidcFlows.handoffs[hashToken(code)] = hand
	f.s.oidcFlows.mu.Unlock()
	if _, err := f.s.exchangeOIDC(code, flow.verifier, flow.flow); err == nil {
		t.Fatal("expired handoff")
	}
	flow = f.start(t, "test", f.admin, "")
	f.s.oidcFlows.mu.Lock()
	a := f.s.oidcFlows.attempts[flow.flow]
	a.Expires = time.Now().Add(-time.Second)
	f.s.oidcFlows.attempts[flow.flow] = a
	f.s.oidcFlows.mu.Unlock()
	if f.callback(t, flow).Code == 302 {
		t.Fatal("expired attempt")
	}
	// Changing configuration while a token exchange is in flight cannot commit.
	flow = f.start(t, "test", f.admin, "")
	f.p.block = make(chan struct{})
	f.p.entered = make(chan struct{})
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- f.callback(t, flow) }()
	<-f.p.entered
	f.update(t, func(c *OIDCConfig) { c.Label = "Again" })
	close(f.p.block)
	w := <-done
	if w.Code == 302 {
		u, _ := url.Parse(w.Header().Get("Location"))
		if _, err := f.s.exchangeOIDC(u.Query().Get("code"), flow.verifier, flow.flow); err == nil {
			t.Fatal("in-flight stale exchange accepted")
		}
	}
}
func TestOIDCIdentityCollisionsNeverMerge(t *testing.T) {
	f := newOIDCFixture(t)
	f.update(t, func(c *OIDCConfig) { c.AutoCreate = true })
	f.p.claims["preferred_username"] = "admin"
	f.p.claims["email"] = "admin@example.com"
	first, err := f.complete(t, "login", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	f.p.claims["sub"] = "subject-2"
	second, err := f.complete(t, "login", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	a, b := first.(*TokenResponse), second.(*TokenResponse)
	if a.User.ID == b.User.ID || a.User.ID == 1 || b.User.ID == 1 || a.User.Role != RoleUser || a.User.PasswordEnabled || a.User.PasskeyEnabled || f.count(t, "oidc_identities") != 2 {
		t.Fatal("claim collision merged or elevated accounts")
	}
}
func TestOIDCLinkRechecksSession(t *testing.T) {
	f := newOIDCFixture(t)
	flow := f.start(t, "link", f.admin, "")
	code := f.handoff(t, flow)
	if err := f.s.RevokeDevice(f.admin.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.exchangeOIDC(code, flow.verifier, flow.flow); err == nil {
		t.Fatal("revoked link initiator accepted")
	}
	if f.count(t, "oidc_identities") != 0 {
		t.Fatal("revoked session linked")
	}
}
func TestOIDCOnlyPolicyInvitationsAndRecovery(t *testing.T) {
	f := newOIDCFixture(t)
	invite, err := f.s.CreateConnectToken(1, "child", "http://localhost:8585")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(invite.Link)
	token := u.Query().Get("token")
	local, err := f.s.RedeemConnectToken(token, "Phone", "same-phone")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.db.Exec("INSERT INTO user_content_policies(user_id,max_movie_rating,max_tv_rating,rating_region) VALUES (?,'PG','TV-PG','US')", local.User.ID); err != nil {
		t.Fatal(err)
	}
	actor := &Claims{UserID: local.User.ID, DeviceID: local.DeviceID}
	if _, err = f.complete(t, "link", actor, ""); err != nil {
		t.Fatal(err)
	}
	result, err := f.complete(t, "login", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	sso := result.(*TokenResponse)
	if sso.DeviceID == local.DeviceID || !sso.User.Child {
		t.Fatal("device upgraded or kids policy lost")
	}
	client, err := f.s.RegisterOAuthClient("agent", []string{"http://localhost/cb"})
	if err != nil {
		t.Fatal(err)
	}
	verifier := oauth2.GenerateVerifier()
	grant, err := f.s.CreateOAuthAuthorizationCode(client, local.User.ID, "http://localhost/cb", oauth2.S256ChallengeFromVerifier(verifier), "http://localhost:8585/mcp", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := f.s.oidcConfiguration()
	before.SSOOnly = true
	if _, err = f.s.saveOIDCConfig(before.OIDCConfig, f.admin); err == nil {
		t.Fatal("untested SSO-only enabled")
	}
	if _, err = f.complete(t, "test", f.admin, ""); err != nil {
		t.Fatal(err)
	}
	f.update(t, func(c *OIDCConfig) { c.SSOOnly = true })
	if _, _, err = f.s.AuthenticateToken(local.AccessToken); err == nil {
		t.Fatal("local access alive")
	}
	if _, err = f.s.Refresh(local.RefreshToken); err == nil {
		t.Fatal("local refresh alive")
	}
	if _, err = f.s.AuthorizeInteractiveToolCall(context.Background(), local.User.ID, local.DeviceID, false); err == nil {
		t.Fatal("local authoritative session alive")
	}
	if _, err = f.s.ExchangeOAuthAuthorizationCode(client.ClientID, grant, "http://localhost/cb", verifier, "http://localhost:8585/mcp"); err == nil {
		t.Fatal("pending local grant alive")
	}
	if _, err = f.s.CreateOAuthAuthorizationCode(client, local.User.ID, "http://localhost/cb", "challenge", "http://localhost:8585/mcp", "mcp"); !errors.Is(err, ErrSSORequired) {
		t.Fatalf("local grant policy %v", err)
	}
	if _, err = f.s.Refresh(sso.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Login("admin", "testpass123", "recovery", ""); err != nil {
		t.Fatal(err)
	}
	_, err = f.s.db.Exec("INSERT INTO users(username,password_hash,role) VALUES ('no-password','','admin')")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.UpdateUserRole(1, RoleUser); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("recovery demotion %v", err)
	}
	if err = f.s.DeleteUser(3, 1); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("recovery deletion %v", err)
	}
	invite, err = f.s.CreateConnectToken(1, "new-child", "http://localhost:8585")
	if err != nil {
		t.Fatal(err)
	}
	u, _ = url.Parse(invite.Link)
	token = u.Query().Get("token")
	if _, err = f.s.RedeemConnectToken(token, "Phone", ""); !errors.Is(err, ErrSSORequired) {
		t.Fatalf("invitation policy %v", err)
	}
	if _, err = f.s.oidcInvitation(token); err != nil {
		t.Fatal("invitation prematurely consumed")
	}
	if _, err = f.complete(t, "login", nil, token); !errors.Is(err, ErrOIDCConflict) {
		t.Fatalf("cross-account invite %v", err)
	}
	if _, err = f.s.oidcInvitation(token); err != nil {
		t.Fatal("conflicting invitation consumed")
	}
	f.p.claims["sub"] = "invited-subject"
	if _, err = f.complete(t, "login", nil, token); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.oidcInvitation(token); !errors.Is(err, ErrTokenRedeemed) {
		t.Fatal("invite replay allowed")
	}
	if err = f.s.unlinkOIDC(actor, local.User.ID, f.p.server.URL); err == nil {
		t.Fatal("self unlink stranded account")
	}
	if err = f.s.unlinkOIDC(f.admin, local.User.ID, f.p.server.URL); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Refresh(sso.RefreshToken); err == nil {
		t.Fatal("unlinked refresh alive")
	}
	f.update(t, func(c *OIDCConfig) { c.Enabled = false })
	if _, err = f.s.Refresh(local.RefreshToken); err == nil {
		t.Fatal("disabling revived revoked session")
	}
	if err = f.s.requireLocalSignIn(local.User.ID); err != nil {
		t.Fatal("local policy not restored")
	}
}

func TestOIDCMCPConsentAndProvenance(t *testing.T) {
	f := newOIDCFixture(t)
	f.update(t, func(c *OIDCConfig) { c.AutoCreate = true })
	client, err := f.s.RegisterOAuthClient("Test MCP", []string{"http://localhost/client"})
	if err != nil {
		t.Fatal(err)
	}
	h := NewOAuthHandler(f.s, "http://localhost:8585")
	mcpVerifier := oauth2.GenerateVerifier()
	values := url.Values{"response_type": {"code"}, "client_id": {client.ClientID}, "redirect_uri": {"http://localhost/client"}, "code_challenge": {oauth2.S256ChallengeFromVerifier(mcpVerifier)}, "code_challenge_method": {"S256"}, "state": {"client-state"}, "resource": {"http://localhost:8585/mcp"}, "scope": {"mcp"}}
	verifier := oauth2.GenerateVerifier()
	req := oidcBeginRequest{Client: "mcp", Challenge: oauth2.S256ChallengeFromVerifier(verifier), OAuth: values}
	data, _ := json.Marshal(req)
	r := httptest.NewRequest("POST", "http://localhost:8585/api/auth/oidc/mcp/begin", bytes.NewReader(data))
	w := httptest.NewRecorder()
	h.BeginOIDC(w, r)
	if w.Code != 200 {
		t.Fatalf("mcp begin %d %s", w.Code, w.Body.String())
	}
	var begun map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &begun)
	flow := f.beginBrowser(t, begun, verifier)
	w = f.callback(t, flow)
	if w.Code != 302 {
		t.Fatalf("callback %s", w.Body.String())
	}
	target, _ := url.Parse(w.Header().Get("Location"))
	q := target.Query()
	result, err := f.s.exchangeOIDC(q.Get("oidc_code"), verifier, flow.flow)
	if err != nil {
		t.Fatal(err)
	}
	if f.count(t, "oauth_authorization_codes") != 0 || f.count(t, "devices") != 1 {
		t.Fatal("MCP authorized before explicit consent")
	}
	consent := result.(map[string]string)["consent"]
	values.Set("oidc_consent", consent)
	// A consent cannot be moved to a different registered client request.
	wrong := oidcOAuthValues(values)
	wrong.Set("oidc_consent", consent)
	wrong.Set("state", "other")
	r = httptest.NewRequest("POST", "http://localhost:8585/oauth/authorize", strings.NewReader(wrong.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	h.Authorize(w, r)
	if w.Code == 302 {
		t.Fatal("mismatched consent authorized")
	}
	r = httptest.NewRequest("POST", "http://localhost:8585/oauth/authorize", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	h.Authorize(w, r)
	if w.Code != 302 {
		t.Fatalf("consent failed %s", w.Body.String())
	}
	redirect, _ := url.Parse(w.Header().Get("Location"))
	tokens, err := f.s.ExchangeOAuthAuthorizationCode(client.ClientID, redirect.Query().Get("code"), "http://localhost/client", mcpVerifier, "http://localhost:8585/mcp")
	if err != nil {
		t.Fatal(err)
	}
	claims, _, err := f.s.AuthenticateTokenForAudience(tokens.AccessToken, "http://localhost:8585/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.s.AuthenticateToken(tokens.AccessToken); err == nil {
		t.Fatal("MCP token admitted to app API")
	}
	var method, issuer string
	if err = f.s.db.QueryRow("SELECT auth_method,oidc_issuer FROM devices WHERE id=?", claims.DeviceID).Scan(&method, &issuer); err != nil {
		t.Fatal(err)
	}
	if method != "oidc" || issuer != f.p.server.URL {
		t.Fatal("MCP provenance lost")
	}
	if _, err = f.complete(t, "test", f.admin, ""); err != nil {
		t.Fatal(err)
	}
	f.update(t, func(c *OIDCConfig) { c.SSOOnly = true })
	refreshed, err := f.s.RefreshOAuthToken(client.ClientID, tokens.RefreshToken, "http://localhost:8585/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.unlinkOIDC(f.admin, claims.UserID, issuer); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.RefreshOAuthToken(client.ClientID, refreshed.RefreshToken, "http://localhost:8585/mcp"); err == nil {
		t.Fatal("unlinked MCP refresh accepted")
	}
}
func TestOIDCKeyRotationAndProxyChoice(t *testing.T) {
	f := newOIDCFixture(t)
	f.update(t, func(c *OIDCConfig) { c.AutoCreate = true; c.AllowedGroups = []string{"family"} })
	delete(f.p.claims, "groups")
	f.p.info = map[string]any{"sub": "subject-1", "groups": []string{"family"}}
	var mu sync.Mutex
	paths := map[string]int{}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths[r.URL.Path]++
		mu.Unlock()
		r.RequestURI = ""
		response, err := httpx.Internal().RoundTrip(r)
		if err != nil {
			http.Error(w, "bad gateway", 502)
			return
		}
		defer response.Body.Close()
		for k, v := range response.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	}))
	defer proxy.Close()
	previous := httpx.OutboundProxy()
	defer httpx.SetOutboundProxy(previous)
	u, _ := url.Parse(proxy.URL)
	httpx.SetOutboundProxy(u)
	if _, err := f.complete(t, "login", nil, ""); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	count := len(paths)
	mu.Unlock()
	if count != 0 {
		t.Fatal("default provider traffic used proxy")
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f.p.key = key
	f.update(t, func(c *OIDCConfig) { c.UseProxy = true })
	if _, err = f.complete(t, "login", nil, ""); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, path := range []string{"/.well-known/openid-configuration", "/token", "/jwks", "/userinfo"} {
		if paths[path] == 0 {
			t.Errorf("%s bypassed selected proxy", path)
		}
	}
}
func TestOIDCConfigAndBoundedStore(t *testing.T) {
	f := newOIDCFixture(t)
	c, _ := f.s.oidcConfiguration()
	for _, issuer := range []string{"http://idp.lan", "https://user:secret@example.com", "https://example.com?query=1", "https://example.com/#fragment"} {
		bad := c
		bad.Issuer = issuer
		if bad.validate() == nil {
			t.Errorf("accepted issuer %s", issuer)
		}
	}
	f.p.mode = "discovery_failure"
	if _, _, _, err := c.provider(context.Background()); !errors.Is(err, ErrOIDCUnavailable) {
		t.Fatalf("discovery error %v", err)
	}
	f.s.oidcFlows.mu.Lock()
	for i := 0; i < oidcMaxFlows; i++ {
		id := string(rune(i))
		f.s.oidcFlows.attempts[id] = oidcAttempt{Expires: time.Now().Add(time.Minute)}
	}
	f.s.oidcFlows.mu.Unlock()
	if _, err := f.s.beginOIDC(oidcBeginRequest{Client: "mobile", Challenge: oauth2.S256ChallengeFromVerifier(oauth2.GenerateVerifier())}, "login", nil); err == nil {
		t.Fatal("unbounded attempts")
	}
	f.s.oidcFlows.clear()
	_, err := f.s.db.Exec(`UPDATE settings SET value='{"config":{"enabled":true},"secret":"enc:v1:corrupt"}' WHERE key='oidc_config'`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.oidcConfiguration(); !errors.Is(err, ErrOIDCConfig) {
		t.Fatal("unreadable secret did not fail closed")
	}
}

func TestOIDCInvitationSessionFailureRollsBackLinkAndRedemption(t *testing.T) {
	f := newOIDCFixture(t)
	invite, err := f.s.CreateConnectToken(1, "invited", "http://localhost:8585")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(invite.Link)
	token := u.Query().Get("token")
	_, err = f.s.db.Exec(`CREATE TRIGGER fail_oidc_device BEFORE INSERT ON devices WHEN NEW.auth_method='oidc' BEGIN SELECT RAISE(ABORT,'session storage unavailable'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.complete(t, "login", nil, token); err == nil {
		t.Fatal("session failure ignored")
	}
	if f.count(t, "oidc_identities") != 0 {
		t.Fatal("identity committed without session")
	}
	if _, err = f.s.oidcInvitation(token); err != nil {
		t.Fatal("invitation consumed without session")
	}
}
func TestOIDCAuthorizedPartyAndSignedClaims(t *testing.T) {
	for _, tc := range []struct {
		name     string
		claims   map[string]any
		rejected bool
	}{
		{"wrong party", map[string]any{"azp": "another-client"}, true},
		{"missing party", map[string]any{"aud": []string{"client", "other"}}, true},
		{"multiple audiences", map[string]any{"aud": []string{"client", "other"}, "azp": "client"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOIDCFixture(t)
			f.update(t, func(c *OIDCConfig) { c.AutoCreate = true })
			for k, v := range tc.claims {
				f.p.claims[k] = v
			}
			_, err := f.complete(t, "login", nil, "")
			if (err != nil) != tc.rejected {
				t.Fatalf("result %v", err)
			}
		})
	}
	f := newOIDCFixture(t)
	f.update(t, func(c *OIDCConfig) { c.AutoCreate = true; c.AllowedGroups = []string{"family"} })
	f.p.claims["groups"] = []string{"blocked"}
	delete(f.p.claims, "preferred_username")
	f.p.info = map[string]any{"sub": "subject-1", "preferred_username": "viewer", "groups": []string{"family"}}
	if _, err := f.complete(t, "login", nil, ""); !errors.Is(err, ErrOIDCGroups) {
		t.Fatalf("UserInfo overwrote signed groups: %v", err)
	}
}
