package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/plex"
	"golang.org/x/oauth2"
)

type plexTestDirectory struct {
	servers []PlexServerAccounts
	err     error
	calls   int
}

func (d *plexTestDirectory) PlexAccounts(context.Context) ([]PlexServerAccounts, error) {
	d.calls++
	return d.servers, d.err
}

type plexTestProvider struct {
	mu                        sync.Mutex
	account                   plex.Account
	approved                  bool
	userStatus, cleanupStatus int
	expires                   int64
	polls, cleanups           atomic.Int32
	entered, release          chan struct{}
}

func newPlexTestProvider(t *testing.T) (*plexTestProvider, *httptest.Server) {
	t.Helper()
	p := &plexTestProvider{account: plex.Account{ID: 42, Username: "viewer", Email: "viewer@example.test"}, approved: true, expires: 600}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		account, approved, userStatus, cleanupStatus, expires := p.account, p.approved, p.userStatus, p.cleanupStatus, p.expires
		entered, release := p.entered, p.release
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/pins":
			if r.Method != "POST" || r.URL.Query().Get("strong") != "true" || r.Header.Get("X-Plex-Client-Identifier") == "" {
				t.Error("PIN must be strong and client-bound")
				w.WriteHeader(400)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "code": "strong-pin-secret", "expiresIn": expires})
		case "/api/v2/pins/7":
			p.polls.Add(1)
			if r.URL.Query().Get("code") != "strong-pin-secret" {
				w.WriteHeader(400)
				return
			}
			token := ""
			if approved {
				token = "TEMPORARY-PLEX-TOKEN"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "authToken": token})
		case "/api/v2/user":
			if entered != nil {
				close(entered)
				<-release
			}
			if userStatus != 0 {
				w.WriteHeader(userStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(account)
		case "/devices.xml":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = fmt.Fprintf(w, `<MediaContainer><Device id="91" clientIdentifier="%s"/></MediaContainer>`, r.Header.Get("X-Plex-Client-Identifier"))
		case "/devices/91.xml", "/api/v2/users/signout":
			if r.Method != "DELETE" {
				t.Error("cleanup must revoke token")
			}
			p.cleanups.Add(1)
			if cleanupStatus != 0 {
				w.WriteHeader(cleanupStatus)
			} else {
				w.WriteHeader(204)
			}
		default:
			t.Errorf("unexpected Plex request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	return p, server
}

type plexFixture struct {
	s         *Service
	p         *plexTestProvider
	admin     *Claims
	directory *plexTestDirectory
}

func newPlexFixture(t *testing.T) *plexFixture {
	t.Helper()
	s := setupTestService(t)
	p, server := newPlexTestProvider(t)
	s.plexBaseURL = server.URL
	session, err := s.Login("admin", "testpass123", "admin", "")
	if err != nil {
		t.Fatal(err)
	}
	admin := &Claims{UserID: 1, DeviceID: session.DeviceID}
	if _, err = s.savePlexConfig(PlexConfig{Enabled: true}, admin); err != nil {
		t.Fatal(err)
	}
	d := &plexTestDirectory{}
	s.SetPlexDirectory(d)
	return &plexFixture{s: s, p: p, admin: admin, directory: d}
}
func (f *plexFixture) config(t *testing.T, auto bool) {
	t.Helper()
	if _, err := f.s.savePlexConfig(PlexConfig{Enabled: true, AutoCreate: auto}, f.admin); err != nil {
		t.Fatal(err)
	}
}
func (f *plexFixture) user(t *testing.T, name string) int64 {
	t.Helper()
	r, err := f.s.db.Exec("INSERT INTO users(username,password_hash,role)VALUES (?,'','user')", name)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := r.LastInsertId()
	return id
}
func (f *plexFixture) link(t *testing.T, userID int64) {
	t.Helper()
	if _, err := f.s.db.Exec("INSERT INTO plex_identities(plex_account_id,user_id,email,username)VALUES (42,?,'old@example.test','old')", userID); err != nil {
		t.Fatal(err)
	}
}
func (f *plexFixture) begin(t *testing.T, purpose string, actor *Claims) (plexBeginResponse, string) {
	t.Helper()
	verifier := oauth2.GenerateVerifier()
	client := "web"
	if purpose == "mcp" {
		client = "mcp"
	}
	result, err := f.s.beginPlex(context.Background(), oidcBeginRequest{Client: client, Challenge: oauth2.S256ChallengeFromVerifier(verifier), HardwareID: "same-hardware"}, purpose, actor)
	if err != nil {
		t.Fatal(err)
	}
	return result, verifier
}
func (f *plexFixture) ticket(t *testing.T, b plexBeginResponse, v string) string {
	t.Helper()
	result, err := f.s.checkPlex(context.Background(), b.Flow, v)
	if err != nil {
		t.Fatal(err)
	}
	code, ok := result["code"].(string)
	if !ok {
		t.Fatalf("no ticket: %#v", result)
	}
	return code
}
func (f *plexFixture) complete(t *testing.T) (any, error) {
	t.Helper()
	b, v := f.begin(t, "login", nil)
	code := f.ticket(t, b, v)
	return f.s.exchangePlex(context.Background(), b.Flow, code, v)
}
func (f *plexFixture) server(t *testing.T, accepted bool) {
	t.Helper()
	if _, err := f.s.db.Exec("INSERT INTO service_instances(id,service_type,name,url,api_key)VALUES ('plex-test','plex','Test Plex','https://plex.tv','encrypted-test-placeholder')"); err != nil {
		t.Fatal(err)
	}
	revision, err := PlexInstanceRevision(f.s.db, "plex-test")
	if err != nil {
		t.Fatal(err)
	}
	f.directory.servers = []PlexServerAccounts{{InstanceID: "plex-test", Name: "Test Plex", Revision: revision, Accounts: []PlexSharedAccount{{Account: f.p.account, Accepted: accepted}}}}
}
func TestPlexDefaultsAndStatus(t *testing.T) {
	s := setupTestService(t)
	c, err := s.plexConfiguration()
	if err != nil || c.Enabled || c.AutoCreate {
		t.Fatalf("defaults %#v %v", c, err)
	}
	if _, err = s.beginPlex(context.Background(), oidcBeginRequest{Client: "web", Challenge: oauth2.S256ChallengeFromVerifier(oauth2.GenerateVerifier())}, "login", nil); !errors.Is(err, ErrPlexDisabled) {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	NewHandler(s).AuthStatus(w, httptest.NewRequest("GET", "http://localhost/api/auth/status", nil))
	if strings.Contains(w.Body.String(), `"plex_available":true`) {
		t.Fatal(w.Body.String())
	}
}
func TestPlexKnownIdentityUsesNumericIDAndIndependentShareAccess(t *testing.T) {
	f := newPlexFixture(t)
	id := f.user(t, "existing")
	f.link(t, id)
	if _, err := f.s.db.Exec("INSERT INTO devices(id,user_id,device_name,hardware_id)VALUES ('local-before',?,'Local','same-hardware')", id); err != nil {
		t.Fatal(err)
	}
	// Email and username drift. Neither may move this link to a namesake.
	f.user(t, "viewer")
	f.directory.err = ErrPlexUnavailable
	result, err := f.complete(t)
	if err != nil {
		t.Fatal(err)
	}
	session := result.(*TokenResponse)
	if session.User.ID != id || session.DeviceID == "local-before" || f.directory.calls != 0 {
		t.Fatalf("wrong identity or lookup %#v calls=%d", session, f.directory.calls)
	}
	var method string
	var plexID int64
	if f.s.db.QueryRow("SELECT auth_method,plex_account_id FROM devices WHERE id=?", session.DeviceID).Scan(&method, &plexID) != nil || method != "plex" || plexID != 42 {
		t.Fatal("missing Plex provenance")
	}
	f.s.db.QueryRow("SELECT auth_method FROM devices WHERE id='local-before'").Scan(&method)
	if method != "local" {
		t.Fatal("upgraded local device")
	}
	if _, err = f.s.Refresh(session.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if f.p.cleanups.Load() != 1 {
		t.Fatal("temporary token not removed")
	}
}
func TestPlexSignupEligibilityAndCollisions(t *testing.T) {
	for _, mode := range []string{"accepted", "owner", "pending", "friend-only", "revoked", "unavailable", "disabled-signup"} {
		t.Run(mode, func(t *testing.T) {
			f := newPlexFixture(t)
			f.config(t, mode != "disabled-signup")
			existing := f.user(t, "viewer")
			f.server(t, mode == "accepted" || mode == "owner" || mode == "disabled-signup")
			if mode == "friend-only" || mode == "revoked" {
				f.directory.servers[0].Accounts = nil
			}
			if mode == "unavailable" {
				f.directory.servers[0].Error = "unavailable"
			}
			result, err := f.complete(t)
			if mode == "accepted" || mode == "owner" {
				if err != nil {
					t.Fatal(err)
				}
				u := result.(*TokenResponse).User
				if u.ID == existing || u.Username == "viewer" || u.Role != "user" || u.PasswordEnabled || u.PasskeyEnabled || u.Child {
					t.Fatalf("unsafe signup %#v", u)
				}
				var grants, accounts int
				f.s.db.QueryRow("SELECT COUNT(*) FROM user_instance_grants WHERE user_id=?", u.ID).Scan(&grants)
				f.s.db.QueryRow("SELECT COUNT(*) FROM user_media_server_accounts WHERE user_id=? AND created_by_cantinarr=0", u.ID).Scan(&accounts)
				if grants != 1 || accounts != 1 {
					t.Fatalf("did not adopt verified access %d %d", grants, accounts)
				}
			} else {
				expected := ErrPlexDenied
				if mode == "unavailable" {
					expected = ErrPlexUnavailable
				}
				if !errors.Is(err, expected) {
					t.Fatalf("%v expected %v", err, expected)
				}
				var count int
				f.s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
				if count != 2 {
					t.Fatal("denial created a user")
				}
			}
			if f.p.cleanups.Load() != 1 {
				t.Fatal("denied sign-in kept its token")
			}
		})
	}
}
func TestPlexVerifierExpiryReplayAndPollBudget(t *testing.T) {
	f := newPlexFixture(t)
	f.link(t, f.user(t, "existing"))
	b, v := f.begin(t, "login", nil)
	wrong := oauth2.GenerateVerifier()
	if _, err := f.s.checkPlex(context.Background(), b.Flow, wrong); !errors.Is(err, ErrPlexFlow) || f.p.polls.Load() != 0 {
		t.Fatal("stolen flow polled Plex")
	}
	f.p.approved = false
	for i := 0; i < 4; i++ {
		out, err := f.s.checkPlex(context.Background(), b.Flow, v)
		if err != nil || out["status"] != "pending" {
			t.Fatal(out, err)
		}
	}
	if f.p.polls.Load() != 1 {
		t.Fatal("poll interval not enforced")
	}
	a := f.s.plexFlows.get(b.Flow)
	a.mu.Lock()
	a.lastCheck = time.Time{}
	a.mu.Unlock()
	f.p.approved = true
	code := f.ticket(t, b, v)
	for _, pair := range [][2]string{{code, wrong}, {"stolen-wrong-ticket", v}} {
		if _, err := f.s.exchangePlex(context.Background(), b.Flow, pair[0], pair[1]); !errors.Is(err, ErrPlexFlow) {
			t.Fatal(err)
		}
	}
	if _, err := f.s.exchangePlex(context.Background(), b.Flow, code, v); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.exchangePlex(context.Background(), b.Flow, code, v); !errors.Is(err, ErrPlexFlow) {
		t.Fatal("replayed ticket", err)
	}
	b, v = f.begin(t, "login", nil)
	code = f.ticket(t, b, v)
	a = f.s.plexFlows.get(b.Flow)
	a.mu.Lock()
	a.ticketExpires = time.Now().Add(-time.Second)
	a.mu.Unlock()
	if _, err := f.s.exchangePlex(context.Background(), b.Flow, code, v); !errors.Is(err, ErrPlexFlow) {
		t.Fatal("expired handoff", err)
	}
	f.p.expires = 2
	b, v = f.begin(t, "login", nil)
	if time.Until(b.Expires) > 3*time.Second {
		t.Fatal("ignored upstream expiry")
	}
	f.s.plexFlows.mu.Lock()
	f.s.plexFlows.attempts[b.Flow].Expires = time.Now().Add(-time.Second)
	f.s.plexFlows.mu.Unlock()
	if _, err := f.s.checkPlex(context.Background(), b.Flow, v); !errors.Is(err, ErrPlexFlow) {
		t.Fatal("expired attempt", err)
	}
}
func TestPlexConcurrentPollAndExchange(t *testing.T) {
	f := newPlexFixture(t)
	f.link(t, f.user(t, "existing"))
	b, v := f.begin(t, "login", nil)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.s.checkPlex(context.Background(), b.Flow, v)
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if f.p.polls.Load() != 1 || f.p.cleanups.Load() != 1 {
		t.Fatal("duplicate provider exchange")
	}
	code := f.ticket(t, b, v)
	var successes atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.s.exchangePlex(context.Background(), b.Flow, code, v); err == nil {
				successes.Add(1)
			} else if !errors.Is(err, ErrPlexFlow) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("issued %d sessions", successes.Load())
	}
}
func TestPlexPolicyChangeUnlinkAndDeletionDuringVerification(t *testing.T) {
	for _, change := range []string{"disable", "unlink", "delete", "revoke-link-session", "cancel"} {
		t.Run(change, func(t *testing.T) {
			f := newPlexFixture(t)
			id := f.user(t, "existing")
			f.link(t, id)
			purpose := "login"
			var actor *Claims
			if change == "revoke-link-session" {
				purpose = "link"
				actor = f.admin
			}
			b, v := f.begin(t, purpose, actor)
			f.p.entered = make(chan struct{})
			f.p.release = make(chan struct{})
			done := make(chan error, 1)
			go func() { _, err := f.s.checkPlex(context.Background(), b.Flow, v); done <- err }()
			<-f.p.entered
			switch change {
			case "disable":
				_, err := f.s.savePlexConfig(PlexConfig{}, f.admin)
				if err != nil {
					t.Fatal(err)
				}
			case "unlink":
				if err := f.s.unlinkPlex(f.admin, id); err != nil {
					t.Fatal(err)
				}
			case "delete":
				if err := f.s.DeleteUser(1, id); err != nil {
					t.Fatal(err)
				}
			case "revoke-link-session":
				if err := f.s.RevokeDevice(f.admin.DeviceID); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				if err := f.s.cancelPlex(b.Flow, v); err != nil {
					t.Fatal(err)
				}
			}
			close(f.p.release)
			if err := <-done; err == nil {
				t.Fatal("issued completion after revocation")
			}
			if f.p.cleanups.Load() != 1 {
				t.Fatal("revocation skipped token cleanup")
			}
		})
	}
}
func TestPlexVerificationAndCleanupFailuresAreSafe(t *testing.T) {
	for _, status := range []int{401, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f := newPlexFixture(t)
			f.p.userStatus = status
			b, v := f.begin(t, "login", nil)
			_, err := f.s.checkPlex(context.Background(), b.Flow, v)
			if err == nil || f.p.cleanups.Load() != 1 {
				t.Fatal("verification failure skipped cleanup", err)
			}
			rec := httptest.NewRecorder()
			plexHTTPError(rec, err)
			if strings.Contains(rec.Body.String(), "TOKEN") || strings.Contains(rec.Body.String(), "127.0.0.1") {
				t.Fatal("unsafe error", rec.Body.String())
			}
			expected := 403
			if status == 503 {
				expected = 503
			}
			if rec.Code != expected {
				t.Fatal(rec.Code)
			}
		})
	}
	f := newPlexFixture(t)
	f.p.cleanupStatus = 503
	b, v := f.begin(t, "login", nil)
	if _, err := f.s.checkPlex(context.Background(), b.Flow, v); !errors.Is(err, ErrPlexUnavailable) {
		t.Fatal(err)
	}
}
func TestPlexLinkPreservesPoliciesAndRefusesConflicts(t *testing.T) {
	f := newPlexFixture(t)
	id := f.user(t, "child")
	if _, err := f.s.db.Exec("INSERT INTO user_content_policies(user_id,max_movie_rating,max_tv_rating,rating_region)VALUES (?,'PG','TV-PG','US')", id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.Exec("INSERT INTO devices(id,user_id,device_name)VALUES ('child-session',?,'Local')", id); err != nil {
		t.Fatal(err)
	}
	actor := &Claims{UserID: id, DeviceID: "child-session"}
	b, v := f.begin(t, "link", actor)
	code := f.ticket(t, b, v)
	if _, err := f.s.exchangePlex(context.Background(), b.Flow, code, v); err != nil {
		t.Fatal(err)
	}
	u, err := f.s.getUserByID(id)
	if err != nil || !u.Child || u.Role != "user" || u.PasswordEnabled || u.PasskeyEnabled {
		t.Fatalf("policy changed %#v %v", u, err)
	}
	b, v = f.begin(t, "link", f.admin)
	code = f.ticket(t, b, v)
	if _, err := f.s.exchangePlex(context.Background(), b.Flow, code, v); !errors.Is(err, ErrPlexConflict) {
		t.Fatal(err)
	}
	if err := f.s.unlinkPlex(actor, id); err == nil {
		t.Fatal("self unlink without another credential")
	}
}
func TestPlexMCPBindingRefreshAndRevocation(t *testing.T) {
	f := newPlexFixture(t)
	id := f.user(t, "existing")
	f.link(t, id)
	client, err := f.s.RegisterOAuthClient("Plex MCP", []string{"http://localhost/client"})
	if err != nil {
		t.Fatal(err)
	}
	pkce := oauth2.GenerateVerifier()
	oauth := url.Values{"response_type": {"code"}, "client_id": {client.ClientID}, "redirect_uri": {"http://localhost/client"}, "scope": {"mcp"}, "state": {"original-state"}, "code_challenge": {oauth2.S256ChallengeFromVerifier(pkce)}, "code_challenge_method": {"S256"}, "resource": {"http://localhost:8585/mcp"}}
	b, v := f.begin(t, "mcp", nil)
	f.s.plexFlows.get(b.Flow).Request.OAuth = oauth
	code := f.ticket(t, b, v)
	result, err := f.s.exchangePlex(context.Background(), b.Flow, code, v)
	if err != nil {
		t.Fatal(err)
	}
	consent := result.(map[string]string)["consent"]
	handler := NewOAuthHandler(f.s, "http://localhost:8585")
	for _, key := range oidcOAuthFields {
		altered := oidcOAuthValues(oauth)
		altered.Set(key, "attacker-value")
		altered.Set("plex_consent", consent)
		r := httptest.NewRequest("POST", "http://localhost:8585/oauth/authorize", nil)
		r.Form = altered
		if _, err = handler.authorizeExternal(r, client, "plex"); err == nil {
			t.Fatalf("consent allowed changed %s", key)
		}
	}
	r := httptest.NewRequest("POST", "http://localhost:8585/oauth/authorize", nil)
	r.Form = oidcOAuthValues(oauth)
	r.Form.Set("plex_consent", consent)
	grant, err := handler.authorizeExternal(r, client, "plex")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = handler.authorizeExternal(r, client, "plex"); err == nil {
		t.Fatal("replayed consent")
	}
	session, err := f.s.ExchangeOAuthAuthorizationCode(client.ClientID, grant, "http://localhost/client", pkce, "http://localhost:8585/mcp")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := f.s.RefreshOAuthToken(client.ClientID, session.RefreshToken, "http://localhost:8585/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.unlinkPlex(f.admin, id); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.RefreshOAuthToken(client.ClientID, fresh.RefreshToken, "http://localhost:8585/mcp"); err == nil {
		t.Fatal("refresh survived unlink")
	}
}

func TestPlexMappingReviewRequiresExplicitFreshUnambiguousProof(t *testing.T) {
	for _, mode := range []string{"valid", "email-only", "unavailable", "ambiguous", "already-claimed", "changed-at-save"} {
		t.Run(mode, func(t *testing.T) {
			f := newPlexFixture(t)
			id := f.user(t, "old-import")
			f.server(t, true)
			if _, err := f.s.db.Exec("UPDATE users SET plex_email='viewer@example.test' WHERE id=?", id); err != nil {
				t.Fatal(err)
			}
			if mode != "email-only" {
				if _, err := f.s.db.Exec("INSERT INTO user_media_server_accounts(user_id,instance_id,remote_user_id,remote_username)VALUES (?,'plex-test','viewer@example.test','viewer')", id); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "unavailable" {
				f.directory.servers[0].Error = "service unavailable"
			}
			if mode == "ambiguous" {
				f.directory.servers[0].Accounts = append(f.directory.servers[0].Accounts, PlexSharedAccount{Account: plex.Account{ID: 99, Email: "viewer@example.test"}})
			}
			if mode == "already-claimed" {
				f.link(t, f.user(t, "another"))
			}
			candidates, err := f.s.plexCandidates(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if mode == "email-only" {
				if len(candidates) != 0 {
					t.Fatal("typed email became a candidate")
				}
				return
			}
			if len(candidates) != 1 {
				t.Fatal(candidates)
			}
			if mode == "changed-at-save" {
				f.directory.servers[0].Accounts[0].Account.ID = 100
			}
			err = f.s.confirmPlexMappings(context.Background(), f.admin, []PlexMapping{{UserID: id, AccountID: 42}})
			if mode == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				identities, err := f.s.plexIdentities(id)
				if err != nil || len(identities) != 1 || identities[0].AccountID != 42 {
					t.Fatal(identities, err)
				}
			} else if err == nil {
				t.Fatal("unconfirmed mapping was accepted")
			}
		})
	}
}
func TestPlexOIDCOnlyRevokesSessionsGrantsAndPreservesAdminRecovery(t *testing.T) {
	oidc := newOIDCFixture(t)
	p, server := newPlexTestProvider(t)
	s := oidc.s
	s.plexBaseURL = server.URL
	if _, err := s.savePlexConfig(PlexConfig{Enabled: true}, oidc.admin); err != nil {
		t.Fatal(err)
	}
	f := &plexFixture{s: s, p: p, admin: oidc.admin, directory: &plexTestDirectory{}}
	s.SetPlexDirectory(f.directory)
	id := f.user(t, "plex-user")
	f.link(t, id)
	result, err := f.complete(t)
	if err != nil {
		t.Fatal(err)
	}
	session := result.(*TokenResponse)
	client, err := s.RegisterOAuthClient("Plex test", []string{"http://localhost/client"})
	if err != nil {
		t.Fatal(err)
	}
	verifier := oauth2.GenerateVerifier()
	newGrant := func() string {
		t.Helper()
		g, e := s.createOAuthAuthorizationCode(client, id, "http://localhost/client", oauth2.S256ChallengeFromVerifier(verifier), "http://localhost:8585/mcp", "mcp", "plex", "", 42)
		if e != nil {
			t.Fatal(e)
		}
		return g
	}
	mcp, err := s.ExchangeOAuthAuthorizationCode(client.ClientID, newGrant(), "http://localhost/client", verifier, "http://localhost:8585/mcp")
	if err != nil {
		t.Fatal(err)
	}
	unexchanged := newGrant()
	b, v := f.begin(t, "login", nil)
	ticket := f.ticket(t, b, v)
	if _, err = oidc.complete(t, "test", oidc.admin, ""); err != nil {
		t.Fatal(err)
	}
	oidc.update(t, func(c *OIDCConfig) { c.SSOOnly = true })
	if _, err = s.Refresh(session.RefreshToken); err == nil {
		t.Fatal("Plex app refresh survived OIDC-only")
	}
	if _, _, err = s.AuthenticateTokenForAudience(mcp.AccessToken, "http://localhost:8585/mcp"); err == nil {
		t.Fatal("MCP access survived")
	}
	if _, err = s.RefreshOAuthToken(client.ClientID, mcp.RefreshToken, "http://localhost:8585/mcp"); err == nil {
		t.Fatal("MCP refresh survived")
	}
	if _, err = s.ExchangeOAuthAuthorizationCode(client.ClientID, unexchanged, "http://localhost/client", verifier, "http://localhost:8585/mcp"); err == nil {
		t.Fatal("pending OAuth grant survived")
	}
	if _, err = s.exchangePlex(context.Background(), b.Flow, ticket, v); err == nil {
		t.Fatal("pending Plex handoff survived")
	}
	if _, err = f.complete(t); !errors.Is(err, ErrSSORequired) {
		t.Fatal("regular Plex login allowed", err)
	}
	if _, err = s.db.Exec("INSERT INTO plex_identities(plex_account_id,user_id)VALUES (99,1)"); err != nil {
		t.Fatal(err)
	}
	p.account.ID = 99
	if _, err = f.complete(t); err != nil {
		t.Fatal("administrator recovery blocked", err)
	}
}
func TestPlexStoreCapAndTransactionalSessionRollback(t *testing.T) {
	f := newPlexFixture(t)
	f.s.plexFlows.mu.Lock()
	for i := 0; i < plexMaxFlows; i++ {
		f.s.plexFlows.attempts[fmt.Sprint(i)] = &plexAttempt{Expires: time.Now().Add(time.Minute)}
	}
	f.s.plexFlows.mu.Unlock()
	if _, err := f.s.beginPlex(context.Background(), oidcBeginRequest{Client: "web", Challenge: oauth2.S256ChallengeFromVerifier(oauth2.GenerateVerifier())}, "login", nil); !errors.Is(err, ErrPlexUnavailable) {
		t.Fatal("unbounded flow store", err)
	}
	f.s.plexFlows.clear()
	id := f.user(t, "existing")
	f.link(t, id)
	if _, err := f.s.db.Exec("CREATE TRIGGER refuse_refresh BEFORE INSERT ON refresh_tokens BEGIN SELECT RAISE(ABORT,'simulated storage failure'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.complete(t); !errors.Is(err, ErrAuthUnavailable) {
		t.Fatal(err)
	}
	var devices int
	f.s.db.QueryRow("SELECT COUNT(*) FROM devices WHERE auth_method='plex'").Scan(&devices)
	if devices != 0 {
		t.Fatal("partial device escaped failed transaction")
	}
}

func TestPlexCancellationAndExchangeHaveOneWinner(t *testing.T) {
	f := newPlexFixture(t)
	id := f.user(t, "existing")
	f.link(t, id)
	for n := 0; n < 20; n++ {
		b, v := f.begin(t, "login", nil)
		ticket := f.ticket(t, b, v)
		start := make(chan struct{})
		cancelled, exchanged := make(chan error, 1), make(chan error, 1)
		go func() { <-start; cancelled <- f.s.cancelPlex(b.Flow, v) }()
		go func() { <-start; _, err := f.s.exchangePlex(context.Background(), b.Flow, ticket, v); exchanged <- err }()
		close(start)
		c, e := <-cancelled, <-exchanged
		if (c == nil) == (e == nil) {
			t.Fatalf("expected exactly one winner, cancel=%v exchange=%v", c, e)
		}
		if c != nil && !errors.Is(c, ErrPlexFlow) || e != nil && !errors.Is(e, ErrPlexFlow) {
			t.Fatal(c, e)
		}
	}
}

func TestPlexImportCreationNeverReusesConcurrentNamesake(t *testing.T) {
	f := newPlexFixture(t)
	var wg sync.WaitGroup
	var success atomic.Int32
	for n := 0; n < 20; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, response, err := f.s.CreateImportUser(1, "concurrent-friend", "https://example.test")
			if err == nil {
				if id <= 1 || response.Link == "" {
					t.Error("missing new user or connect link")
				}
				success.Add(1)
			} else if !errors.Is(err, ErrUserExists) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if success.Load() != 1 {
		t.Fatalf("created %d import accounts", success.Load())
	}
	var tokens int
	if err := f.s.db.QueryRow("SELECT COUNT(*) FROM connect_tokens c JOIN users u ON u.id=c.user_id WHERE u.username='concurrent-friend'").Scan(&tokens); err != nil || tokens != 1 {
		t.Fatalf("tokens=%d err=%v", tokens, err)
	}
}
