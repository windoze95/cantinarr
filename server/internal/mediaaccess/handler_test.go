package mediaaccess

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/instance"
)

// newHandlerEnv mounts the handler under the production route patterns so
// chi param extraction and the JSON shapes are the real ones.
func newHandlerEnv(t *testing.T) (*env, http.Handler) {
	t.Helper()
	e := newEnv(t)
	h := NewHandler(e.svc, slog.New(slog.NewTextHandler(e.logs, nil)))
	r := chi.NewRouter()
	r.Get("/api/media-servers", h.List)
	r.Post("/api/media-servers/{instanceID}/account", h.CreateAccount)
	r.Get("/api/admin/media-servers/accounts", h.ListAccounts)
	r.Get("/api/admin/media-servers/{instanceID}/users", h.RemoteUsers)
	r.Put("/api/admin/users/{userID}/media-servers/{instanceID}/account", h.LinkAccount)
	r.Delete("/api/admin/users/{userID}/media-servers/{instanceID}/account", h.UnlinkAccount)
	return e, r
}

func serve(router http.Handler, method, path string, userID int64, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if userID != 0 {
		req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: userID, Username: "u", Role: auth.RoleUser}))
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestCreateAccountStatusMapping(t *testing.T) {
	e, router := newHandlerEnv(t)
	alice := e.user("alice")
	bob := e.user("bob")
	weird := e.user("we/ird")
	jf := e.jellyfin("Home", instance.MediaServerConfig{PublicAddress: "https://jf.example.com"})
	other := e.jellyfin("Other", instance.MediaServerConfig{})
	e.grant(alice, jf)
	e.grant(bob, jf)
	e.grant(weird, jf)
	e.provider.addUser("Bob", false, false)
	const goodBody = `{"password":"alice-pass-1"}`

	rec := serve(router, "POST", "/api/media-servers/"+jf+"/account", 0, goodBody)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", rec.Code)
	}
	for name, body := range map[string]string{"not json": `nope`, "short": `{"password":"short"}`, "long": `{"password":"` + strings.Repeat("x", 1025) + `"}`} {
		if rec := serve(router, "POST", "/api/media-servers/"+jf+"/account", alice, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", name, rec.Code)
		}
	}

	unknown := serve(router, "POST", "/api/media-servers/jellyfin-nope/account", alice, goodBody)
	ungranted := serve(router, "POST", "/api/media-servers/"+other+"/account", alice, goodBody)
	if unknown.Code != http.StatusForbidden || ungranted.Code != http.StatusForbidden {
		t.Fatalf("unknown = %d, ungranted = %d, want 403/403", unknown.Code, ungranted.Code)
	}
	if unknown.Body.String() != ungranted.Body.String() {
		t.Fatalf("403 bodies differ (existence oracle): %q vs %q", unknown.Body.String(), ungranted.Body.String())
	}

	rec = serve(router, "POST", "/api/media-servers/"+jf+"/account", alice, goodBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	var created CreatedAccount
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Username != "alice" || created.PublicAddress != "https://jf.example.com" {
		t.Fatalf("created = %+v", created)
	}

	rec = serve(router, "POST", "/api/media-servers/"+jf+"/account", alice, goodBody)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"account_exists"`) {
		t.Fatalf("repeat = %d %s", rec.Code, rec.Body.String())
	}
	rec = serve(router, "POST", "/api/media-servers/"+jf+"/account", bob, goodBody)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"name_taken"`) {
		t.Fatalf("name taken = %d %s", rec.Code, rec.Body.String())
	}
	rec = serve(router, "POST", "/api/media-servers/"+jf+"/account", weird, goodBody)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_name"`) {
		t.Fatalf("invalid name = %d %s", rec.Code, rec.Body.String())
	}

	// Upstream failure: fixed body, nothing the server said is echoed.
	carol := e.user("carol")
	e.grant(carol, jf)
	e.provider.createErr = errors.New("jellyfin create user: token=UPSTREAM_SECRET host=jellyfin.internal")
	rec = serve(router, "POST", "/api/media-servers/"+jf+"/account", carol, goodBody)
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "UPSTREAM_SECRET") || strings.Contains(rec.Body.String(), "jellyfin.internal") {
		t.Fatalf("upstream failure = %d %s", rec.Code, rec.Body.String())
	}
}

func TestNoResponseOrLogContainsPasswordOrKey(t *testing.T) {
	e, router := newHandlerEnv(t)
	alice := e.user("alice")
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	e.grant(alice, jf)
	const password = "alice-pass-SENTINEL"
	bodies := []string{
		serve(router, "POST", "/api/media-servers/"+jf+"/account", alice, `{"password":"`+password+`"}`).Body.String(),
		serve(router, "POST", "/api/media-servers/"+jf+"/account", alice, `{"password":"`+password+`"}`).Body.String(),
		serve(router, "GET", "/api/media-servers", alice, "").Body.String(),
		serve(router, "GET", "/api/admin/media-servers/accounts", alice, "").Body.String(),
	}
	for _, body := range bodies {
		if strings.Contains(body, password) || strings.Contains(body, testInstanceKey) {
			t.Fatalf("response leaked a secret: %s", body)
		}
	}
	if logs := e.logs.String(); strings.Contains(logs, password) || strings.Contains(logs, testInstanceKey) {
		t.Fatalf("logs leaked a secret: %s", logs)
	}
	var stored int
	if err := e.db.QueryRow("SELECT COUNT(*) FROM user_media_server_accounts WHERE remote_username LIKE '%SENTINEL%' OR remote_user_id LIKE '%SENTINEL%'").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 0 {
		t.Fatal("the password reached the database")
	}
}

func TestListNeverIncludesInstanceURL(t *testing.T) {
	e, router := newHandlerEnv(t)
	alice := e.user("alice")
	jf := e.jellyfin("Home", instance.MediaServerConfig{PublicAddress: "https://jf.example.com"})
	e.grant(alice, jf)
	rec := serve(router, "GET", "/api/media-servers", alice, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, forbidden := range []string{"jellyfin.internal", "8096", `"url"`, "api_key", testInstanceKey} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("list leaked %q: %s", forbidden, body)
		}
	}
	var views []ServerView
	if err := json.Unmarshal(rec.Body.Bytes(), &views); err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].PublicAddress != "https://jf.example.com" || views[0].Account != nil || views[0].ServiceType != "jellyfin" {
		t.Fatalf("views = %+v", views)
	}
	// No grants at all is an empty list, not null.
	bob := e.user("bob")
	if rec := serve(router, "GET", "/api/media-servers", bob, ""); strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("ungranted list = %q, want []", rec.Body.String())
	}
}

func TestAdminRoutesStatusMapping(t *testing.T) {
	e, router := newHandlerEnv(t)
	alice := e.user("alice")
	bob := e.user("bob")
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	radarr := &instance.Instance{ServiceType: "radarr", Name: "Movies", URL: "http://radarr:7878", APIKey: testInstanceKey}
	if err := e.store.Create(radarr); err != nil {
		t.Fatal(err)
	}
	adminID := e.provider.addUser("root", true, false)
	aliceID := e.provider.addUser("alice", false, true)
	link := func(userID int64, inst, remote string) *httptest.ResponseRecorder {
		return serve(router, "PUT", "/api/admin/users/"+itoa(userID)+"/media-servers/"+inst+"/account", alice, `{"remote_user_id":"`+remote+`"}`)
	}

	// Remote user list for the picker.
	rec := serve(router, "GET", "/api/admin/media-servers/"+jf+"/users", alice, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"users":[`) || !strings.Contains(rec.Body.String(), `"is_administrator":true`) {
		t.Fatalf("remote users = %d %s", rec.Code, rec.Body.String())
	}
	if rec := serve(router, "GET", "/api/admin/media-servers/"+radarr.ID+"/users", alice, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("remote users for radarr = %d, want 400", rec.Code)
	}
	if rec := serve(router, "GET", "/api/admin/media-servers/jellyfin-nope/users", alice, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("remote users for unknown = %d, want 404", rec.Code)
	}

	// Link mapping.
	if rec := serve(router, "PUT", "/api/admin/users/abc/media-servers/"+jf+"/account", alice, `{"remote_user_id":"x"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad user id = %d", rec.Code)
	}
	if rec := link(alice, jf, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing remote id = %d", rec.Code)
	}
	if rec := link(999, jf, aliceID); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown user = %d", rec.Code)
	}
	if rec := link(alice, "jellyfin-nope", aliceID); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown instance = %d", rec.Code)
	}
	if rec := link(alice, radarr.ID, aliceID); rec.Code != http.StatusBadRequest {
		t.Fatalf("radarr = %d", rec.Code)
	}
	if rec := link(alice, jf, "remote-nope"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown remote = %d", rec.Code)
	}
	if rec := link(alice, jf, adminID); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "administrator") {
		t.Fatalf("admin remote = %d %s", rec.Code, rec.Body.String())
	}
	rec = link(alice, jf, aliceID)
	if rec.Code != http.StatusOK {
		t.Fatalf("link = %d %s", rec.Code, rec.Body.String())
	}
	var account Account
	if err := json.Unmarshal(rec.Body.Bytes(), &account); err != nil {
		t.Fatal(err)
	}
	if account.UserID != alice || account.InstanceName != "Home" || account.Username != "alice" || account.CreatedByCantinarr || account.Disabled {
		t.Fatalf("account = %+v", account)
	}
	if rec := link(bob, jf, aliceID); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "remote_already_linked") {
		t.Fatalf("duplicate remote = %d %s", rec.Code, rec.Body.String())
	}
	carolID := e.provider.addUser("carol", false, false)
	if rec := link(alice, jf, carolID); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "account_exists") {
		t.Fatalf("second link = %d %s", rec.Code, rec.Body.String())
	}

	rec = serve(router, "GET", "/api/admin/media-servers/accounts", alice, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"instance_name":"Home"`) || !strings.Contains(rec.Body.String(), `"service_type":"jellyfin"`) {
		t.Fatalf("accounts = %d %s", rec.Code, rec.Body.String())
	}

	unlinkPath := "/api/admin/users/" + itoa(alice) + "/media-servers/" + jf + "/account"
	if rec := serve(router, "DELETE", unlinkPath, alice, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("unlink = %d %s", rec.Code, rec.Body.String())
	}
	if rec := serve(router, "DELETE", unlinkPath, alice, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("second unlink = %d", rec.Code)
	}
	if body, _ := io.ReadAll(serve(router, "GET", "/api/admin/media-servers/accounts", alice, "").Body); strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("accounts after unlink = %s, want []", body)
	}
}

func TestRequestInviteStatusMapping(t *testing.T) {
	e, router := newHandlerEnv(t)
	alice, bob := e.user("alice"), e.user("bob")
	plex, fake := e.inviteServer("Den Plex", instance.MediaServerConfig{PublicAddress: "https://app.plex.tv"})
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	e.grant(alice, plex, jf)
	e.grant(bob, plex)

	for name, body := range map[string]string{
		"both":          `{"password":"alice-pass-1","email":"alice@example.com"}`,
		"invalid email": `{"email":"nope"}`,
		"password here": `{"password":"alice-pass-1"}`,
	} {
		if rec := serve(router, "POST", "/api/media-servers/"+plex+"/account", alice, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", name, rec.Code, rec.Body.String())
		}
	}
	if rec := serve(router, "POST", "/api/media-servers/"+jf+"/account", alice, `{"email":"alice@example.com"}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "wrong_kind") {
		t.Fatalf("email on an account server = %d %s", rec.Code, rec.Body.String())
	}
	if fake.invites != 0 {
		t.Fatalf("refusals sent %d invites", fake.invites)
	}

	rec := serve(router, "POST", "/api/media-servers/"+plex+"/account", alice, `{"email":" Alice@Example.com "}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("request invite = %d %s", rec.Code, rec.Body.String())
	}
	var created CreatedAccount
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !created.Pending || created.Username != "alice" || created.PublicAddress != "https://app.plex.tv" {
		t.Fatalf("created = %+v", created)
	}
	if rec := serve(router, "POST", "/api/media-servers/"+plex+"/account", alice, `{"email":"alice@example.com"}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "account_exists") {
		t.Fatalf("second request = %d %s", rec.Code, rec.Body.String())
	}
	if rec := serve(router, "POST", "/api/media-servers/"+plex+"/account", bob, `{"email":"alice@example.com"}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "name_taken") {
		t.Fatalf("someone else's email = %d %s", rec.Code, rec.Body.String())
	}

	list := serve(router, "GET", "/api/media-servers", alice, "")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"kind":"invite"`) || !strings.Contains(list.Body.String(), `"pending":true`) {
		t.Fatalf("list = %d %s", list.Code, list.Body.String())
	}
	users := serve(router, "GET", "/api/admin/media-servers/"+plex+"/users", alice, "")
	if users.Code != http.StatusOK || !strings.Contains(users.Body.String(), `"pending":true`) {
		t.Fatalf("remote users = %d %s", users.Code, users.Body.String())
	}
}
