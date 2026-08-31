package instance

import (
	"bytes"
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/plex"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// fakePlexTV serves the slice of plex.tv the instance layer talks to: the PIN
// flow (approved when the test says so), the account, its resources, one
// owned server with sections, and that server's share list.
type fakePlexTV struct {
	mu       sync.Mutex
	approved bool
	token    string
	pinID    int64
	requests []string
}

func newFakePlexTV(t *testing.T) (*fakePlexTV, *httptest.Server) {
	t.Helper()
	f := &fakePlexTV{token: "plex-token-SENTINEL", pinID: 4242}
	mux := http.NewServeMux()
	record := func(r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
	}
	authed := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("X-Plex-Token") != f.token {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"errors":[{"code":1001,"message":"Unauthorized"}]}`))
			return false
		}
		return true
	}
	mux.HandleFunc("/api/v2/pins", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if r.Header.Get("X-Plex-Client-Identifier") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": f.pinID, "code": "ABCD", "authToken": nil})
	})
	mux.HandleFunc("/api/v2/pins/", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		f.mu.Lock()
		approved := f.approved
		f.mu.Unlock()
		body := map[string]any{"id": f.pinID, "code": "ABCD", "authToken": nil}
		if approved {
			body["authToken"] = f.token
		}
		json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("/api/v2/user", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if !authed(w, r) {
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": 12345, "uuid": "u-owner", "username": "cantina-owner", "email": "Owner@Example.com", "title": "Cantina Owner"})
	})
	mux.HandleFunc("/api/v2/resources", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if !authed(w, r) {
			return
		}
		json.NewEncoder(w).Encode([]map[string]any{
			{"name": "Cantina", "clientIdentifier": "m1", "provides": "server", "owned": true},
			{"name": "Friend's", "clientIdentifier": "m9", "provides": "server", "owned": false},
			{"name": "Living room TV", "clientIdentifier": "tv1", "provides": "player", "owned": true},
		})
	})
	mux.HandleFunc("/api/servers/m1", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if !authed(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><MediaContainer size="1"><Server name="Cantina" version="1.41.0" machineIdentifier="m1" host="10.0.0.5"><Section id="101" key="1" type="movie" title="Movies"/><Section id="102" key="2" type="show" title="TV Shows"/></Server></MediaContainer>`))
	})
	mux.HandleFunc("/api/servers/m1/shared_servers", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if !authed(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><MediaContainer size="0"></MediaContainer>`))
	})
	mux.HandleFunc("/api/invites/requested", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if !authed(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><MediaContainer size="0"></MediaContainer>`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakePlexTV) approve() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.approved = true
}

func newPlexRouter(h *Handler) http.Handler {
	r := chi.NewRouter()
	r.Post("/instances", h.Create)
	r.Put("/instances/{instanceID}", h.Update)
	r.Post("/instances/test", h.TestConnection)
	r.Post("/instances/media-server/libraries", h.MediaServerLibraries)
	r.Post("/instances/plex/link/begin", h.PlexLinkBegin)
	r.Post("/instances/plex/link/check", h.PlexLinkCheck)
	r.Post("/instances/plex/servers", h.PlexServers)
	return r
}

func TestPlexInstanceLinksByPinAndNeverShowsTheToken(t *testing.T) {
	f, plexTV := newFakePlexTV(t)
	store := newTestStore(t)
	h := NewHandler(store, NewRegistry(store))
	h.SetPlexBaseURL(plexTV.URL)
	router := newPlexRouter(h)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	// Begin: a PIN and the approval page.
	rec := do("POST", "/instances/plex/link/begin", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("begin = %d %s", rec.Code, rec.Body.String())
	}
	var begun struct {
		PinID int64  `json:"pin_id"`
		Code  string `json:"code"`
		URL   string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &begun); err != nil {
		t.Fatal(err)
	}
	if begun.PinID != 4242 || begun.Code != "ABCD" || !strings.HasPrefix(begun.URL, "https://app.plex.tv/auth#?") || !strings.Contains(begun.URL, "code=ABCD") {
		t.Fatalf("begin = %+v", begun)
	}
	pin := `{"pin_id":4242}`

	// Not approved yet: linked=false, and the instance cannot be saved.
	rec = do("POST", "/instances/plex/link/check", pin)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"linked":false`) {
		t.Fatalf("check before approval = %d %s", rec.Code, rec.Body.String())
	}
	rec = do("POST", "/instances", `{"service_type":"plex","name":"Cantina","url":"`+plexTV.URL+`","plex_link_pin":4242,"media_server_config":{"machine_identifier":"m1"}}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "not approved") {
		t.Fatalf("create before approval = %d %s", rec.Code, rec.Body.String())
	}

	f.approve()
	rec = do("POST", "/instances/plex/link/check", pin)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"linked":true`) || !strings.Contains(rec.Body.String(), "cantina-owner") {
		t.Fatalf("check after approval = %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), f.token) {
		t.Fatal("the link check served the token")
	}

	// The owned servers for the picker, by pin.
	rec = do("POST", "/instances/plex/servers", `{"service_type":"plex","url":"`+plexTV.URL+`","plex_link_pin":4242}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("servers = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"machine_identifier":"m1"`) || strings.Contains(rec.Body.String(), "m9") || strings.Contains(rec.Body.String(), "tv1") {
		t.Fatalf("servers = %s, want only the owned server", rec.Body.String())
	}

	// Libraries and the connection test work before the save, by pin, with
	// the machine identifier in the config.
	candidate := `{"service_type":"plex","url":"` + plexTV.URL + `","plex_link_pin":4242,"media_server_config":{"machine_identifier":"m1"}}`
	rec = do("POST", "/instances/media-server/libraries", candidate)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"101"`) || !strings.Contains(rec.Body.String(), `"server_name":"Cantina"`) {
		t.Fatalf("libraries = %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("POST", "/instances/test", candidate); rec.Code != http.StatusNoContent {
		t.Fatalf("test = %d %s", rec.Code, rec.Body.String())
	}

	// No server picked: refused before anything is stored.
	rec = do("POST", "/instances", `{"service_type":"plex","name":"Cantina","url":"`+plexTV.URL+`","plex_link_pin":4242}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "machine_identifier") {
		t.Fatalf("create without a server = %d %s", rec.Code, rec.Body.String())
	}

	rec = do("POST", "/instances", `{"service_type":"plex","name":"Cantina","url":"`+plexTV.URL+`","plex_link_pin":4242,"is_default":true,
		"media_server_config":{"machine_identifier":"m1","library_ids":["102","101"],"auto_approve":true}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	var created instanceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.IsDefault || created.ServiceType != "plex" || created.MediaServerConfig == nil {
		t.Fatalf("created = %+v", created)
	}
	cfg := created.MediaServerConfig
	if cfg.MachineIdentifier != "m1" || !cfg.AutoApprove || cfg.PublicAddress != PlexPublicAddress || len(cfg.LibraryIDs) != 2 || cfg.LibraryIDs[0] != "101" {
		t.Fatalf("created config = %+v", cfg)
	}
	if strings.Contains(rec.Body.String(), f.token) || strings.Contains(rec.Body.String(), "client_id") || strings.Contains(rec.Body.String(), "plex_owner") {
		t.Fatalf("create response leaked a server-managed value: %s", rec.Body.String())
	}
	stored, err := store.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.APIKey != f.token || stored.MediaServerConfig.ClientID == "" || stored.URL != plexTV.URL {
		t.Fatalf("stored = key %q client %q url %q", stored.APIKey, stored.MediaServerConfig.ClientID, stored.URL)
	}
	// The link recorded who owns the server, exactly as plex.tv spelled it.
	if cfg := stored.MediaServerConfig; cfg.PlexOwnerID != 12345 || cfg.PlexOwnerUsername != "cantina-owner" || cfg.PlexOwnerEmail != "Owner@Example.com" {
		t.Fatalf("stored owner = %+v", cfg)
	}
	clientID := stored.MediaServerConfig.ClientID

	// A used pin is not reusable for another instance? It is: the link is
	// the admin's for its window. What matters is that an update without a
	// pin keeps the token and the client id, and an unrelated config change
	// keeps the machine.
	rec = do("PUT", "/instances/"+created.ID, `{"name":"Cantina Plex","url":"`+plexTV.URL+`","api_key":"","media_server_config":{"machine_identifier":"m1","library_ids":["101"],"public_address":"https://app.plex.tv"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update = %d %s", rec.Code, rec.Body.String())
	}
	stored, _ = store.Get(created.ID)
	if stored.APIKey != f.token || stored.MediaServerConfig.ClientID != clientID || stored.MediaServerConfig.MachineIdentifier != "m1" || len(stored.MediaServerConfig.LibraryIDs) != 1 {
		t.Fatalf("update changed what it should not: %+v key=%q", stored.MediaServerConfig, stored.APIKey)
	}
	if stored.MediaServerConfig.PlexOwnerID != 12345 || stored.MediaServerConfig.PlexOwnerEmail != "Owner@Example.com" {
		t.Fatalf("update dropped the owner: %+v", stored.MediaServerConfig)
	}
	// An old client omitting the config keeps it whole.
	rec = do("PUT", "/instances/"+created.ID, `{"name":"Cantina Plex","url":"`+plexTV.URL+`","api_key":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("omitted update = %d %s", rec.Code, rec.Body.String())
	}
	stored, _ = store.Get(created.ID)
	if stored.MediaServerConfig.MachineIdentifier != "m1" || stored.MediaServerConfig.ClientID != clientID || stored.MediaServerConfig.PlexOwnerUsername != "cantina-owner" {
		t.Fatalf("omitted update dropped plex fields: %+v", stored.MediaServerConfig)
	}

	// Editing: the stored token serves the picker, the libraries, and the test.
	byID := `{"id":"` + created.ID + `","url":"` + plexTV.URL + `"}`
	for _, path := range []string{"/instances/plex/servers", "/instances/media-server/libraries"} {
		if rec := do("POST", path, byID); rec.Code != http.StatusOK {
			t.Fatalf("%s by id = %d %s", path, rec.Code, rec.Body.String())
		}
	}
	if rec := do("POST", "/instances/test", byID); rec.Code != http.StatusNoContent {
		t.Fatalf("test by id = %d %s", rec.Code, rec.Body.String())
	}

	// An unknown or expired pin is refused.
	rec = do("POST", "/instances", `{"service_type":"plex","name":"Other","url":"`+plexTV.URL+`","plex_link_pin":99,"media_server_config":{"machine_identifier":"m1"}}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "expired") {
		t.Fatalf("unknown pin = %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("POST", "/instances/plex/link/check", `{"pin_id":99}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown pin check = %d", rec.Code)
	}
	// Nothing linked, nothing pasted: refused.
	rec = do("POST", "/instances", `{"service_type":"plex","name":"Other","url":"`+plexTV.URL+`","media_server_config":{"machine_identifier":"m1"}}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "link a Plex account") {
		t.Fatalf("unlinked create = %d %s", rec.Code, rec.Body.String())
	}
	// A pasted token works too (labs, scripts) and gets its own client id.
	rec = do("POST", "/instances", `{"service_type":"plex","name":"Pasted","url":"`+plexTV.URL+`","api_key":"`+f.token+`","media_server_config":{"machine_identifier":"m1"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("pasted create = %d %s", rec.Code, rec.Body.String())
	}
	var pasted instanceResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &pasted)
	if stored, _ := store.Get(pasted.ID); stored.MediaServerConfig.ClientID == "" || stored.MediaServerConfig.ClientID == clientID {
		t.Fatalf("pasted token client id = %q", stored.MediaServerConfig.ClientID)
	} else if stored.MediaServerConfig.PlexOwnerID != 0 || stored.MediaServerConfig.PlexOwnerEmail != "" {
		// Nobody read the pasted token's account; the owner is backfilled
		// from the token later, not guessed.
		t.Fatalf("pasted token recorded an owner: %+v", stored.MediaServerConfig)
	}
}

func TestSetPlexOwnerPatchesOnlyTheConfig(t *testing.T) {
	s := newTestStore(t)
	inst := &Instance{ServiceType: "plex", Name: "Cantina", URL: plex.BaseURL, APIKey: "tok",
		MediaServerConfig: MediaServerConfig{PublicAddress: PlexPublicAddress, LibraryIDs: []string{"101", "102"}, MachineIdentifier: "m1", AutoApprove: true, ClientID: "cid"}}
	if err := s.Create(inst); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPlexOwner(inst.ID, plex.Account{ID: 7, Username: "owner", Email: "Owner@Example.com"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	cfg := got.MediaServerConfig
	if cfg.PlexOwnerID != 7 || cfg.PlexOwnerUsername != "owner" || cfg.PlexOwnerEmail != "Owner@Example.com" {
		t.Fatalf("owner = %+v", cfg)
	}
	if cfg.ClientID != "cid" || cfg.MachineIdentifier != "m1" || !cfg.AutoApprove || len(cfg.LibraryIDs) != 2 || cfg.PublicAddress != PlexPublicAddress || got.APIKey != "tok" {
		t.Fatalf("patch touched more than the owner: %+v key=%q", cfg, got.APIKey)
	}
	if err := s.SetPlexOwner("plex-nope", plex.Account{ID: 1}); err == nil {
		t.Fatal("unknown instance accepted")
	}
	jf := mkInstance(t, s, "jellyfin", "Main")
	if err := s.SetPlexOwner(jf, plex.Account{ID: 1}); err == nil {
		t.Fatal("a Jellyfin instance took a Plex owner")
	}
}

// A plex.tv call that fails answers the fixed 502 and logs the reason, so an
// admin whose box cannot reach plex.tv can see why from the container log.
func TestPlexLinkFailuresAreLogged(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	store := newTestStore(t)
	h := NewHandler(store, NewRegistry(store))
	h.SetPlexBaseURL("http://127.0.0.1:1")
	router := newPlexRouter(h)
	req := httptest.NewRequest("POST", "/instances/plex/link/begin", strings.NewReader(""))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "could not reach plex.tv") {
		t.Fatalf("begin = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logs.String(), "plex link: could not mint a PIN at plex.tv") {
		t.Fatalf("the failure reason was not logged: %q", logs.String())
	}
}

func TestPlexLinkExpires(t *testing.T) {
	links := newPlexLinks()
	links.put(1, &plexLink{clientID: "c", expires: time.Now().Add(-time.Second)})
	links.put(2, &plexLink{clientID: "c", token: "t", expires: time.Now().Add(time.Minute)})
	if links.get(1) != nil {
		t.Fatal("an expired link was served")
	}
	if links.get(2) == nil {
		t.Fatal("a live link was dropped")
	}
}

func seedLegacyPlex(t *testing.T, s *Store, cipher *secrets.Cipher, token, machineID string) {
	t.Helper()
	enc, err := cipher.Encrypt(token)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"plex_client_id":   "legacy-client-id",
		"plex_token":       enc,
		"plex_account":     "cantina-owner",
		"plex_machine_id":  machineID,
		"plex_server_name": "Cantina",
		"plex_library_ids": "[102,101]",
		"plex_auto_invite": "1",
	} {
		if _, err := s.db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", key, value); err != nil {
			t.Fatal(err)
		}
	}
}

func legacyUser(t *testing.T, s *Store, username, email string, invitedAt string) int64 {
	t.Helper()
	id := createUser(t, s, username)
	if _, err := s.db.Exec("UPDATE users SET plex_email = ?, plex_invited_at = ? WHERE id = ?", email, sqlNullable(invitedAt), id); err != nil {
		t.Fatal(err)
	}
	return id
}

func sqlNullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func TestMigratePlexSingletonCarriesTheLinkedAccountOver(t *testing.T) {
	s := newTestStore(t)
	cipher := s.cipher
	seedLegacyPlex(t, s, cipher, "plex-token-SENTINEL", "m1")
	alice := legacyUser(t, s, "alice", "Alice@Example.com", "2026-07-04 10:00:00")
	bob := legacyUser(t, s, "bob", "bob@example.com", "2026-07-05 10:00:00")
	dupe := legacyUser(t, s, "alice2", "alice@example.com", "2026-07-06 10:00:00") // same address, later
	waiting := legacyUser(t, s, "carol", "carol@example.com", "")                  // shared, never invited
	blank := legacyUser(t, s, "dave", "", "2026-07-07 10:00:00")                   // stamped with no email (cannot happen; skipped)
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))

	if err := MigratePlexSingleton(s.db, cipher, logger); err != nil {
		t.Fatal(err)
	}

	instances, err := s.List("plex")
	if err != nil || len(instances) != 1 {
		t.Fatalf("plex instances = %+v, %v", instances, err)
	}
	inst := instances[0]
	if inst.Name != "Cantina" || inst.URL != "https://plex.tv" || inst.APIKey != "plex-token-SENTINEL" || inst.IsDefault {
		t.Fatalf("instance = %+v", inst)
	}
	cfg := inst.MediaServerConfig
	if cfg.MachineIdentifier != "m1" || cfg.ClientID != "legacy-client-id" || !cfg.AutoApprove || cfg.PublicAddress != PlexPublicAddress ||
		len(cfg.LibraryIDs) != 2 || cfg.LibraryIDs[0] != "101" || cfg.LibraryIDs[1] != "102" {
		t.Fatalf("config = %+v", cfg)
	}

	grants := func(userID int64) []string {
		g, err := s.ListUserGrants(userID)
		if err != nil {
			t.Fatal(err)
		}
		return g["plex"]
	}
	if len(grants(alice)) != 1 || len(grants(bob)) != 1 {
		t.Fatalf("invited users not granted: alice=%v bob=%v", grants(alice), grants(bob))
	}
	for name, id := range map[string]int64{"duplicate address": dupe, "never invited": waiting, "no email": blank} {
		if len(grants(id)) != 0 {
			t.Errorf("%s user was granted", name)
		}
	}
	var remote, username string
	var createdAt time.Time
	if err := s.db.QueryRow("SELECT remote_user_id, remote_username, created_at FROM user_media_server_accounts WHERE user_id = ? AND instance_id = ?", alice, inst.ID).Scan(&remote, &username, &createdAt); err != nil {
		t.Fatalf("alice's row: %v", err)
	}
	if remote != "alice@example.com" || username != "Alice@Example.com" || createdAt.Year() != 2026 || createdAt.Month() != 7 || createdAt.Day() != 4 {
		t.Fatalf("alice's row = %s %s %v", remote, username, createdAt)
	}
	var rows int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM user_media_server_accounts WHERE instance_id = ?", inst.ID).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("rows = %d, %v; want 2", rows, err)
	}
	if !strings.Contains(logs.String(), "share one Plex email") {
		t.Fatalf("the duplicate address was not logged: %s", logs.String())
	}

	// The old keys are gone, except the client identifier; the marker is set.
	for _, key := range []string{"plex_token", "plex_account", "plex_machine_id", "plex_server_name", "plex_library_ids", "plex_auto_invite"} {
		if _, ok := getSetting(s.db, key); ok {
			t.Errorf("%s survived the migration", key)
		}
	}
	if v, ok := getSetting(s.db, "plex_client_id"); !ok || v != "legacy-client-id" {
		t.Fatal("the client identifier was removed")
	}
	if _, ok := getSetting(s.db, plexMigratedKey); !ok {
		t.Fatal("marker not set")
	}

	// A second boot changes nothing.
	if err := MigratePlexSingleton(s.db, cipher, logger); err != nil {
		t.Fatal(err)
	}
	if instances, _ := s.List("plex"); len(instances) != 1 {
		t.Fatalf("second run created another instance: %d", len(instances))
	}
}

func TestMigratePlexSingletonEdges(t *testing.T) {
	t.Run("nothing linked", func(t *testing.T) {
		s := newTestStore(t)
		if err := MigratePlexSingleton(s.db, s.cipher, nil); err != nil {
			t.Fatal(err)
		}
		if instances, _ := s.List("plex"); len(instances) != 0 {
			t.Fatal("an instance appeared from nothing")
		}
		if _, ok := getSetting(s.db, plexMigratedKey); !ok {
			t.Fatal("marker not set")
		}
	})
	t.Run("linked without a server", func(t *testing.T) {
		s := newTestStore(t)
		seedLegacyPlex(t, s, s.cipher, "tok", "")
		logs := &bytes.Buffer{}
		if err := MigratePlexSingleton(s.db, s.cipher, slog.New(slog.NewTextHandler(logs, nil))); err != nil {
			t.Fatal(err)
		}
		if instances, _ := s.List("plex"); len(instances) != 0 {
			t.Fatal("an instance with no server was created")
		}
		if _, ok := getSetting(s.db, "plex_token"); ok {
			t.Fatal("the unusable token was kept")
		}
		if !strings.Contains(logs.String(), "no server was ever selected") {
			t.Fatalf("not explained: %s", logs.String())
		}
	})
	t.Run("token that does not decrypt", func(t *testing.T) {
		s := newTestStore(t)
		other, _ := secrets.NewCipher(bytes.Repeat([]byte{9}, 32))
		seedLegacyPlex(t, s, other, "tok", "m1")
		legacyUser(t, s, "alice", "alice@example.com", "2026-07-04 10:00:00")
		logs := &bytes.Buffer{}
		if err := MigratePlexSingleton(s.db, s.cipher, slog.New(slog.NewTextHandler(logs, nil))); err != nil {
			t.Fatal(err)
		}
		if instances, _ := s.List("plex"); len(instances) != 0 {
			t.Fatal("an instance was created from a token that does not decrypt")
		}
		if _, ok := getSetting(s.db, plexMigratedKey); ok {
			t.Fatal("marker set although nothing was carried over")
		}
		if _, ok := getSetting(s.db, "plex_token"); !ok {
			t.Fatal("the token was dropped")
		}
		if !strings.Contains(logs.String(), "does not decrypt") {
			t.Fatalf("not explained: %s", logs.String())
		}
	})
}
