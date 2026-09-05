package auth

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"golang.org/x/oauth2"
)

// A real discoverable WebAuthn assertion, signed by a test authenticator. The
// positive check before enabling SSO-only proves that a refusal afterwards
// comes from the policy, not a malformed credential or an incomplete mock.
func oidcPasskeyAssertion(t *testing.T, s *Service, userID int64) (string, *http.Request) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, err := webauthncbor.Marshal(map[int]any{
		1: 2, 3: -7, -1: 1,
		-2: key.X.FillBytes(make([]byte, 32)), -3: key.Y.FillBytes(make([]byte, 32)),
	})
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 32)
	if _, err = rand.Read(id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("INSERT INTO webauthn_credentials(id,user_id,public_key,attestation_type,rp_id) VALUES (?,?,?,'none','localhost')", hex.EncodeToString(id), userID, public); err != nil {
		t.Fatal(err)
	}
	options, session, err := s.BeginPasskeyLogin(httptest.NewRequest("POST", "http://localhost:8585/api/auth/passkey/login/begin", nil))
	if err != nil {
		t.Fatal(err)
	}
	challenge := options.(*protocol.CredentialAssertion).Response.Challenge.String()
	clientData, err := json.Marshal(map[string]any{"type": "webauthn.get", "challenge": challenge, "origin": "http://localhost:8585"})
	if err != nil {
		t.Fatal(err)
	}
	rpHash := sha256.Sum256([]byte("localhost"))
	authData := append(rpHash[:], byte(5), 0, 0, 0, 1) // user present + verified, counter 1
	clientHash := sha256.Sum256(clientData)
	signed := sha256.Sum256(append(append([]byte{}, authData...), clientHash[:]...))
	signature, err := ecdsa.SignASN1(rand.Reader, key, signed[:])
	if err != nil {
		t.Fatal(err)
	}
	handle := make([]byte, 8)
	binary.BigEndian.PutUint64(handle, uint64(userID))
	encode := base64.RawURLEncoding.EncodeToString
	body, err := json.Marshal(map[string]any{
		"id": encode(id), "rawId": encode(id), "type": "public-key",
		"response": map[string]string{"clientDataJSON": encode(clientData), "authenticatorData": encode(authData), "signature": encode(signature), "userHandle": encode(handle)},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://localhost:8585/api/auth/passkey/login/finish", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return session, r
}

func oidcLocalUser(t *testing.T, s *Service) int64 {
	t.Helper()
	// Use the same valid bcrypt hash as the fixture's administrator, but a
	// regular account with both local methods enabled.
	result, err := s.db.Exec("INSERT INTO users(username,password_hash,role,password_enabled,passkey_enabled) SELECT 'local-user',password_hash,'user',1,1 FROM users WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestOIDCOnlyEnforcesPasswordPasskeyAndIssuedMCPRefresh(t *testing.T) {
	f := newOIDCFixture(t)
	userID := oidcLocalUser(t, f.s)
	if _, err := f.s.Login("local-user", "testpass123", "local", "hardware"); err != nil {
		t.Fatal(err)
	}
	session, r := oidcPasskeyAssertion(t, f.s, userID)
	if _, err := f.s.FinishPasskeyLogin(session, r); err != nil {
		t.Fatalf("valid local passkey: %v", err)
	}
	client, err := f.s.RegisterOAuthClient("local agent", []string{"http://localhost/client"})
	if err != nil {
		t.Fatal(err)
	}
	verifier := oauth2.GenerateVerifier()
	grant, err := f.s.CreateOAuthAuthorizationCode(client, userID, "http://localhost/client", oauth2.S256ChallengeFromVerifier(verifier), "http://localhost:8585/mcp", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	mcp, err := f.s.ExchangeOAuthAuthorizationCode(client.ClientID, grant, "http://localhost/client", verifier, "http://localhost:8585/mcp")
	if err != nil {
		t.Fatal(err)
	}
	// An assertion may already be waiting in a browser when the admin changes policy.
	session, r = oidcPasskeyAssertion(t, f.s, userID)
	if _, err = f.complete(t, "test", f.admin, ""); err != nil {
		t.Fatal(err)
	}
	f.update(t, func(c *OIDCConfig) { c.SSOOnly = true })
	devices := f.count(t, "devices")
	if _, err = f.s.Login("local-user", "testpass123", "local", "hardware"); !errors.Is(err, ErrSSORequired) {
		t.Fatalf("password policy: %v", err)
	}
	if _, err = f.s.AuthenticatePassword("local-user", "testpass123"); !errors.Is(err, ErrSSORequired) {
		t.Fatalf("MCP password policy: %v", err)
	}
	if _, err = f.s.FinishPasskeyLogin(session, r); !errors.Is(err, ErrSSORequired) {
		t.Fatalf("passkey policy: %v", err)
	}
	if _, err = f.s.RefreshOAuthToken(client.ClientID, mcp.RefreshToken, "http://localhost:8585/mcp"); err == nil {
		t.Fatal("issued local MCP refresh survived SSO-only")
	}
	if _, _, err = f.s.AuthenticateTokenForAudience(mcp.AccessToken, "http://localhost:8585/mcp"); err == nil {
		t.Fatal("issued local MCP access survived SSO-only")
	}
	// The MCP passkey endpoint shares verification but has a separate grant path.
	session, r = oidcPasskeyAssertion(t, f.s, userID)
	r.URL.RawQuery = url.Values{"session_id": {session}, "response_type": {"code"}, "client_id": {client.ClientID}, "redirect_uri": {"http://localhost/client"}, "code_challenge": {oauth2.S256ChallengeFromVerifier(verifier)}, "code_challenge_method": {"S256"}, "resource": {"http://localhost:8585/mcp"}}.Encode()
	w := httptest.NewRecorder()
	NewOAuthHandler(f.s, "http://localhost:8585").FinishOAuthPasskeyLogin(w, r)
	if w.Code != http.StatusUnauthorized || f.count(t, "oauth_authorization_codes") != 0 {
		t.Fatalf("MCP passkey policy: %d %s", w.Code, w.Body.String())
	}
	if f.count(t, "devices") != devices {
		t.Fatal("denied local authentication created a device")
	}
	// Local administrator passkeys remain recovery credentials too.
	session, r = oidcPasskeyAssertion(t, f.s, 1)
	if _, err = f.s.FinishPasskeyLogin(session, r); err != nil {
		t.Fatalf("administrator passkey recovery: %v", err)
	}
}

func TestOIDCOnlyConcurrentLocalIssuanceCannotEscapeRevocation(t *testing.T) {
	f := newOIDCFixture(t)
	userID := oidcLocalUser(t, f.s)
	if _, err := f.complete(t, "test", f.admin, ""); err != nil {
		t.Fatal(err)
	}
	config, err := f.s.oidcConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	config.SSOOnly = true
	var wg sync.WaitGroup
	start := make(chan struct{})
	sessions := make(chan *TokenResponse, 8)
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			response, err := f.s.Login("local-user", "testpass123", "phone", "hardware")
			if err == nil {
				sessions <- response
			} else if !errors.Is(err, ErrSSORequired) {
				failures <- err
			}
		}()
	}
	close(start)
	if _, err = f.s.saveOIDCConfig(config.OIDCConfig, f.admin); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(sessions)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	for response := range sessions {
		if _, _, err = f.s.AuthenticateToken(response.AccessToken); err == nil {
			t.Fatal("racing local access survived policy commit")
		}
		if _, err = f.s.Refresh(response.RefreshToken); err == nil {
			t.Fatal("racing local refresh survived policy commit")
		}
	}
	var active int
	if err = f.s.db.QueryRow("SELECT COUNT(*) FROM devices WHERE user_id=? AND auth_method='local' AND revoked_at IS NULL", userID).Scan(&active); err != nil || active != 0 {
		t.Fatalf("local sessions after policy commit: %d %v", active, err)
	}
}
