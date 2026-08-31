package mediaaccess

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/instance"
)

// fakeCreator stands in for auth.Service: it finds or creates the user by
// exact name the way CreateConnectToken does and hands back a link built
// on the server URL it was given, so a test can see both.
type fakeCreator struct {
	e     *env
	names []string
	urls  []string
	fail  map[string]bool
}

func (f *fakeCreator) CreateConnectToken(_ int64, name, serverURL string) (*auth.CreateConnectTokenResponse, error) {
	f.names = append(f.names, name)
	f.urls = append(f.urls, serverURL)
	if f.fail[name] {
		return nil, errors.New("boom")
	}
	if _, err := f.e.db.Exec("INSERT INTO users (username, password_hash, role) VALUES (?, '', 'user') ON CONFLICT(username) DO NOTHING", name); err != nil {
		return nil, err
	}
	return &auth.CreateConnectTokenResponse{Link: serverURL + "/connect?token=" + name, OriginSource: "external_address"}, nil
}

func TestImportAccountsCreatesGrantsAndLinks(t *testing.T) {
	e := newEnv(t)
	creator := &fakeCreator{e: e, fail: map[string]bool{"failing": true}}
	e.svc.SetUserCreator(creator)
	admin := e.user("admin")
	han := e.user("han")
	bob := e.user("bob")
	dooku := e.user("dooku")
	jf := e.jellyfin("Home", instance.MediaServerConfig{PublicAddress: "https://jf.example.com"})
	kyloID := e.provider.addUser("kylo", false, false)
	hanID := e.provider.addUser("han", false, false)
	adminID := e.provider.addUser("vaderadmin", true, false)
	takenID := e.provider.addUser("taken", false, false)
	failingID := e.provider.addUser("failing", false, false)
	secondDookuID := e.provider.addUser("dooku", false, false)
	for _, pair := range []struct {
		user   int64
		remote string
	}{{bob, takenID}, {dooku, "remote-dooku-first"}} {
		if _, err := e.svc.insertAccount(accountRow{UserID: pair.user, InstanceID: jf, RemoteUserID: pair.remote, RemoteUsername: "x", CreatedByCantinarr: false}, false); err != nil {
			t.Fatal(err)
		}
	}

	results, err := e.svc.ImportAccounts(context.Background(), admin, jf, "https://cantinarr.example", []string{kyloID, " " + kyloID + " ", hanID, adminID, takenID, "remote-nope", failingID, secondDookuID})
	if err != nil {
		t.Fatal(err)
	}
	byRemote := map[string]ImportResult{}
	for _, r := range results {
		byRemote[r.RemoteUserID] = r
	}
	if len(results) != 7 {
		t.Fatalf("results = %+v, want one per distinct id", results)
	}

	kylo := byRemote[kyloID]
	if !kylo.Created || !kylo.Linked || kylo.Username != "kylo" || kylo.Link != "https://cantinarr.example/connect?token=kylo" || kylo.OriginSource != "external_address" || kylo.Error != "" {
		t.Fatalf("new account: %+v, want created, linked, with its link", kylo)
	}
	var role string
	if err := e.db.QueryRow("SELECT role FROM users WHERE id = ?", kylo.UserID).Scan(&role); err != nil || role != "user" {
		t.Fatalf("kylo's user row: role %q err %v", role, err)
	}
	if row := e.row(kylo.UserID, jf); row == nil || row.RemoteUserID != kyloID || row.CreatedByCantinarr {
		t.Fatalf("kylo's row = %+v, want linked as an existing account", row)
	}
	grants, _ := e.store.ListUserGrants(kylo.UserID)
	if !contains(grants["jellyfin"], jf) {
		t.Fatalf("kylo's grants = %v, want the instance", grants)
	}

	if r := byRemote[hanID]; r.Created || !r.Linked || r.UserID != han || r.Link != "" || r.Error != "" {
		t.Fatalf("existing user of that name: %+v, want reused and linked, no new link", r)
	}
	if r := byRemote[adminID]; !r.Created || !r.Linked || r.Error != "" {
		t.Fatalf("administrator account: %+v, want imported like any other", r)
	}
	if r := byRemote[takenID]; r.Error != ImportAlreadyLinked || r.UserID != 0 || r.Linked {
		t.Fatalf("already linked: %+v, want already_linked and no user", r)
	}
	if r := byRemote["remote-nope"]; r.Error != ImportNotFound || r.UserID != 0 {
		t.Fatalf("unknown id: %+v, want not_found", r)
	}
	if r := byRemote[failingID]; r.Error != ImportUserFailed || r.Linked {
		t.Fatalf("creator failure: %+v, want user_failed", r)
	}
	if r := byRemote[secondDookuID]; r.Error != ImportUserHasAccount || r.Linked || r.UserID != dooku {
		t.Fatalf("user already has an account here: %+v, want user_has_account", r)
	}
	if got := strings.Join(creator.names, ","); got != "kylo,vaderadmin,failing" {
		t.Fatalf("users created = %q, want only the ones that needed creating, in order", got)
	}
	var taken int
	if err := e.db.QueryRow("SELECT COUNT(*) FROM users WHERE username = 'taken'").Scan(&taken); err != nil || taken != 0 {
		t.Fatalf("a user was created for an already linked account (%d)", taken)
	}
	if strings.Contains(e.logs.String(), "kylo") {
		t.Fatalf("logs = %q, want no account names", e.logs.String())
	}

	// Again: nothing new to do, said per row.
	again, err := e.svc.ImportAccounts(context.Background(), admin, jf, "https://cantinarr.example", []string{kyloID})
	if err != nil || len(again) != 1 || again[0].Error != ImportAlreadyLinked {
		t.Fatalf("second import = %+v (%v), want already_linked", again, err)
	}
}

func TestImportAccountsAdoptsPlexShares(t *testing.T) {
	e := newEnv(t)
	e.svc.SetUserCreator(&fakeCreator{e: e})
	admin := e.user("admin")
	rey := e.user("rey")
	if err := e.svc.rememberEmail(rey, "rey@example.com"); err != nil {
		t.Fatal(err)
	}
	plex := e.mediaServer("plex", "Den Plex", instance.MediaServerConfig{PublicAddress: instance.PlexPublicAddress})
	invite := newFakeInviteProvider()
	invite.share("Newbie@Example.com", false)
	invite.share("rey-old@example.com", true)
	e.providers[plex] = invite

	results, err := e.svc.ImportAccounts(context.Background(), admin, plex, "https://cantinarr.example", []string{"newbie@example.com", "REY-OLD@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %+v", results)
	}
	newbie := results[0]
	if !newbie.Created || !newbie.Linked || newbie.Username != "newbie" || newbie.RemoteUserID != "newbie@example.com" {
		t.Fatalf("new share: %+v, want a user named after the share, linked", newbie)
	}
	if email, _ := e.svc.plexEmail(newbie.UserID); email != "newbie@example.com" {
		t.Fatalf("newbie's plex email = %q, want the share's", email)
	}
	if row := e.row(newbie.UserID, plex); row == nil || row.RemoteUserID != "newbie@example.com" || row.CreatedByCantinarr {
		t.Fatalf("newbie's row = %+v", row)
	}
	if invite.invites != 0 {
		t.Fatalf("an import sent %d invites, want none", invite.invites)
	}
	// "rey-old" is a new username, so a new user is made; an existing user's
	// own email is never overwritten, which the rey row proves untouched.
	if r := results[1]; !r.Created || !r.Linked || r.Username != "rey-old" {
		t.Fatalf("pending share: %+v, want adopted as a pending link", r)
	}
	if email, _ := e.svc.plexEmail(rey); email != "rey@example.com" {
		t.Fatalf("rey's own email changed to %q", email)
	}
}

func TestImportStatusMapping(t *testing.T) {
	e := newEnv(t)
	h := NewHandler(e.svc, slog.New(slog.NewTextHandler(e.logs, nil)))
	router := chi.NewRouter()
	router.Post("/api/admin/media-servers/{instanceID}/import", h.Import)
	admin := e.user("admin")
	jf := e.jellyfin("Home", instance.MediaServerConfig{PublicAddress: "https://jf.example.com"})
	kyloID := e.provider.addUser("kylo", false, false)
	radarr := &instance.Instance{ServiceType: "radarr", Name: "Movies", URL: "http://radarr:7878", APIKey: "k"}
	if err := e.store.Create(radarr); err != nil {
		t.Fatal(err)
	}
	path := "/api/admin/media-servers/" + jf + "/import"
	good := `{"remote_user_ids":["` + kyloID + `"],"server_url":"https://app.example"}`

	if rec := serve(router, "POST", path, 0, good); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", rec.Code)
	}
	if rec := serve(router, "POST", path, admin, good); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no user creation wired = %d, want 503", rec.Code)
	}
	creator := &fakeCreator{e: e}
	e.svc.SetUserCreator(creator)
	for name, body := range map[string]string{
		"garbage":       `{`,
		"no ids":        `{"remote_user_ids":[],"server_url":"https://app.example"}`,
		"too many":      `{"remote_user_ids":[` + strings.Repeat(`"x",`, maxImportAccounts) + `"y"],"server_url":"https://app.example"}`,
		"no server url": `{"remote_user_ids":["x"]}`,
	} {
		if rec := serve(router, "POST", path, admin, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", name, rec.Code)
		}
	}
	if rec := serve(router, "POST", "/api/admin/media-servers/jellyfin-nope/import", admin, good); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown instance = %d, want 404", rec.Code)
	}
	if rec := serve(router, "POST", "/api/admin/media-servers/"+radarr.ID+"/import", admin, good); rec.Code != http.StatusBadRequest {
		t.Fatalf("radarr instance = %d, want 400", rec.Code)
	}
	e.provider.usersErr = errors.New("jellyfin list users: status 503")
	if rec := serve(router, "POST", path, admin, good); rec.Code != http.StatusBadGateway {
		t.Fatalf("server down = %d, want 502", rec.Code)
	}
	e.provider.usersErr = nil
	if len(creator.names) != 0 {
		t.Fatalf("refusals created users: %v", creator.names)
	}

	// The external address wins over the app-sent one for the links.
	h.SetExternalURLSource(func() string { return "https://public.example" })
	rec := serve(router, "POST", path, admin, good)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("import = %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Results []ImportResult `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Results) != 1 || !out.Results[0].Linked || out.Results[0].Link != "https://public.example/connect?token=kylo" {
		t.Fatalf("results = %s (%v)", rec.Body.String(), err)
	}
	if creator.urls[0] != "https://public.example" {
		t.Fatalf("link built on %q, want the external address", creator.urls[0])
	}
	if strings.Contains(rec.Body.String(), ".internal") {
		t.Fatalf("response carries an instance host: %s", rec.Body.String())
	}
}
