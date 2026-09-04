package instance

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/mediapath"
)

// newUsersRouter mounts the instance-users endpoints the way router.go does,
// so the tests exercise the real URL params and JSON shapes the app relies on.
func newUsersRouter(h *Handler) http.Handler {
	r := chi.NewRouter()
	r.Get("/instances/{instanceID}/users", h.GetInstanceUsers)
	r.Put("/instances/{instanceID}/users", h.UpdateInstanceUsers)
	r.Get("/instances/{instanceID}/grant-users", h.GetInstanceGrantUsers)
	r.Put("/instances/{instanceID}/grant-users", h.UpdateInstanceGrantUsers)
	r.Get("/users/{userID}/default-instances", h.GetUserDefaultInstances)
	r.Put("/users/{userID}/default-instances", h.UpdateUserDefaultInstances)
	r.Get("/users/{userID}/instance-grants", h.GetUserInstanceGrants)
	r.Put("/users/{userID}/instance-grants", h.UpdateUserInstanceGrants)
	return r
}

func TestDeletePendingBookInstanceReturnsConflict(t *testing.T) {
	s := newTestStore(t)
	uid := createUser(t, s, "delete-conflict")
	instanceID := mkInstance(t, s, "chaptarr", "Books")
	if _, err := s.db.Exec(
		`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, instance_id, media_type, title, status)
		 VALUES (?, 0, 'pending', 'ebook', ?, 'book', 'Pending', 'pending')`, uid, instanceID,
	); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(s, nil)
	router := chi.NewRouter()
	router.Delete("/instances/{instanceID}", h.Delete)
	req := httptest.NewRequest(http.MethodDelete, "/instances/"+instanceID, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "await approval") {
		t.Fatalf("delete response = %d %q, want 409 structured refusal", rec.Code, rec.Body.String())
	}
}

func TestInstanceUsersEndpoints(t *testing.T) {
	s := newTestStore(t)
	h := NewHandler(s, nil)
	router := newUsersRouter(h)
	alice := createUser(t, s, "alice")
	bob := createUser(t, s, "bob")
	r1 := mkInstance(t, s, "radarr", "R1")
	r2 := mkInstance(t, s, "radarr", "R2")

	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	decodePins := func(rec *httptest.ResponseRecorder) map[int64]string {
		t.Helper()
		var rows []struct {
			UserID     int64  `json:"user_id"`
			InstanceID string `json:"instance_id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
			t.Fatalf("decode %q: %v", rec.Body.String(), err)
		}
		pins := make(map[int64]string, len(rows))
		for _, row := range rows {
			pins[row.UserID] = row.InstanceID
		}
		return pins
	}

	// No pins yet: an empty JSON array, not null.
	rec := do("GET", "/instances/"+r1+"/users", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d %s", rec.Code, rec.Body.String())
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Fatalf("empty pins body = %q, want []", got)
	}

	// Assign alice; the response reports the whole service type, so bob's
	// separate pin to a sibling instance shows up too.
	if err := s.SetUserDefault(bob, "radarr", r2); err != nil {
		t.Fatalf("SetUserDefault: %v", err)
	}
	rec = do("PUT", "/instances/"+r1+"/users", `{"user_ids":[`+jsonInt(alice)+`]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body.String())
	}
	pins := decodePins(rec)
	if pins[alice] != r1 || pins[bob] != r2 {
		t.Fatalf("pins = %v, want alice=%s bob=%s", pins, r1, r2)
	}

	// Unknown instance → 404; unknown user → 400 (FK).
	if rec := do("GET", "/instances/radarr-missing/users", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET unknown instance = %d, want 404", rec.Code)
	}
	if rec := do("PUT", "/instances/radarr-missing/users", `{"user_ids":[]}`); rec.Code != http.StatusNotFound {
		t.Fatalf("PUT unknown instance = %d, want 404", rec.Code)
	}
	if rec := do("PUT", "/instances/"+r1+"/users", `{"user_ids":[999999]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT unknown user = %d, want 400", rec.Code)
	}
	if rec := do("PUT", "/instances/"+r1+"/users", `not json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT invalid body = %d, want 400", rec.Code)
	}
}

func jsonInt(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestUserDefaultInstancesEndpoints(t *testing.T) {
	s := newTestStore(t)
	h := NewHandler(s, nil)
	router := newUsersRouter(h)
	alice := createUser(t, s, "defaults-alice")
	r1 := mkInstance(t, s, "radarr", "R1")

	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	userPath := "/users/" + jsonInt(alice) + "/default-instances"

	// No overrides yet: an empty JSON object, not null.
	if rec := do("GET", userPath, ""); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "{}" {
		t.Fatalf("GET empty = %d %q, want 200 {}", rec.Code, rec.Body.String())
	}

	// Pin, read back from the PUT response, clear with null.
	rec := do("PUT", userPath, `{"radarr":"`+r1+`"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), r1) {
		t.Fatalf("PUT pin = %d %q, want 200 with %s", rec.Code, rec.Body.String(), r1)
	}
	if rec := do("PUT", userPath, `{"radarr":null}`); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "{}" {
		t.Fatalf("PUT clear = %d %q, want 200 {}", rec.Code, rec.Body.String())
	}

	// Unknown service type and mismatched instance are rejected.
	if rec := do("PUT", userPath, `{"flixarr":"`+r1+`"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT unknown type = %d, want 400", rec.Code)
	}
	if rec := do("PUT", userPath, `{"sonarr":"`+r1+`"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT mismatched type = %d, want 400", rec.Code)
	}
	if rec := do("GET", "/users/nope/default-instances", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("GET bad user id = %d, want 400", rec.Code)
	}
}

func TestUserInstanceGrantsEndpoints(t *testing.T) {
	s := newTestStore(t)
	h := NewHandler(s, nil)
	router := newUsersRouter(h)
	alice := createUser(t, s, "grants-alice")
	hd := mkInstance(t, s, "radarr", "Movies")
	uhd := mkInstance(t, s, "radarr", "4K Movies")

	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	decodeGrants := func(rec *httptest.ResponseRecorder) map[string][]string {
		t.Helper()
		var grants map[string][]string
		if err := json.Unmarshal(rec.Body.Bytes(), &grants); err != nil {
			t.Fatalf("decode %q: %v", rec.Body.String(), err)
		}
		return grants
	}
	userPath := "/users/" + jsonInt(alice) + "/instance-grants"

	// No grants yet: an empty JSON object, not null.
	if rec := do("GET", userPath, ""); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "{}" {
		t.Fatalf("GET empty = %d %q, want 200 {}", rec.Code, rec.Body.String())
	}

	// Grant both radarr instances and read the map back.
	rec := do("PUT", userPath, `{"radarr":["`+hd+`","`+uhd+`"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body.String())
	}
	if grants := decodeGrants(rec); len(grants["radarr"]) != 2 {
		t.Fatalf("grants = %v, want 2 radarr entries", grants)
	}

	// A type absent from the body is untouched; an empty list clears.
	if rec := do("PUT", userPath, `{"sonarr":[]}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT sonarr-only = %d %s", rec.Code, rec.Body.String())
	} else if grants := decodeGrants(rec); len(grants["radarr"]) != 2 {
		t.Fatalf("radarr grants lost by a sonarr-only PUT: %v", grants)
	}
	if rec := do("PUT", userPath, `{"radarr":null}`); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "{}" {
		t.Fatalf("PUT clear = %d %q, want 200 {}", rec.Code, rec.Body.String())
	}

	// Grants accept only per-user-routable service types, and validate ids.
	if rec := do("PUT", userPath, `{"sabnzbd":["`+hd+`"]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT ungrantable type = %d, want 400", rec.Code)
	}
	if rec := do("PUT", userPath, `{"sonarr":["`+hd+`"]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT mismatched type = %d, want 400", rec.Code)
	}
	if rec := do("PUT", userPath, `{"radarr":["nope-12345678"]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT unknown instance = %d, want 400", rec.Code)
	}
	if rec := do("PUT", userPath, `not json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT invalid body = %d, want 400", rec.Code)
	}
}

func TestInstanceGrantUsersEndpoints(t *testing.T) {
	s := newTestStore(t)
	h := NewHandler(s, nil)
	router := newUsersRouter(h)
	alice := createUser(t, s, "ig-endpoint-alice")
	bob := createUser(t, s, "ig-endpoint-bob")
	uhd := mkInstance(t, s, "radarr", "4K Movies")
	sab := mkInstance(t, s, "sabnzbd", "Downloads")

	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	// No grants yet: an empty JSON array, not null.
	if rec := do("GET", "/instances/"+uhd+"/grant-users", ""); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("GET empty = %d %q, want 200 []", rec.Code, rec.Body.String())
	}

	// Grant to both, then revoke bob by omission.
	rec := do("PUT", "/instances/"+uhd+"/grant-users", `{"user_ids":[`+jsonInt(alice)+`,`+jsonInt(bob)+`]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body.String())
	}
	rec = do("PUT", "/instances/"+uhd+"/grant-users", `{"user_ids":[`+jsonInt(alice)+`]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT revoke = %d %s", rec.Code, rec.Body.String())
	}
	var rows []struct {
		UserID     int64  `json:"user_id"`
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	if len(rows) != 1 || rows[0].UserID != alice || rows[0].InstanceID != uhd {
		t.Fatalf("grants = %v, want only alice on %s", rows, uhd)
	}

	// Unknown instance → 404; ungrantable service type → 400; unknown user →
	// 400 (FK).
	if rec := do("GET", "/instances/radarr-missing/grant-users", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET unknown instance = %d, want 404", rec.Code)
	}
	if rec := do("PUT", "/instances/"+sab+"/grant-users", `{"user_ids":[]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT ungrantable type = %d, want 400", rec.Code)
	}
	if rec := do("PUT", "/instances/"+uhd+"/grant-users", `{"user_ids":[999999]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT unknown user = %d, want 400", rec.Code)
	}
}

func TestPerInstanceMediaPathMappingsPreserveClearAndValidate(t *testing.T) {
	root := t.TempDir()
	books := root + "/books"
	if err := os.Mkdir(books, 0700); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(newTestStore(t), nil)
	h.SetMediaDownloadRoots([]string{root})
	existing := &Instance{
		ServiceType:       "chaptarr",
		MediaDownloadMode: MediaDownloadModeMapped,
		MediaPathMappings: []mediapath.Mapping{{ArrPath: "/kept", CantinarrPath: books}},
	}

	preserved := &Instance{ServiceType: "chaptarr"}
	if err := h.applyMediaPathMappings(preserved, nil, existing); err != nil {
		t.Fatal(err)
	}
	if preserved.MediaDownloadMode != MediaDownloadModeMapped ||
		len(preserved.MediaPathMappings) != 1 ||
		preserved.MediaPathMappings[0].ArrPath != "/kept" {
		t.Fatalf("omitted update config = %q %+v, want preserved mapped rows",
			preserved.MediaDownloadMode, preserved.MediaPathMappings)
	}

	empty := []mediapath.Mapping{}
	if err := h.applyMediaPathMappings(preserved, &empty, existing); err != nil {
		t.Fatal(err)
	}
	if preserved.MediaDownloadMode != MediaDownloadModeDisabled {
		t.Fatalf("explicit clear mode = %q, want disabled", preserved.MediaDownloadMode)
	}
	removedRootHandler := NewHandler(newTestStore(t), nil)
	removedRootHandler.SetMediaDownloadRoots([]string{root + "/missing"})
	if err := removedRootHandler.applyMediaPathMappings(preserved, &empty, existing); err != nil {
		t.Fatalf("clear with unavailable deployment root: %v", err)
	}

	mappings := []mediapath.Mapping{
		{ArrPath: "/ebooks", CantinarrPath: books},
		{ArrPath: `Z:\Audiobooks`, CantinarrPath: books},
	}
	if err := h.applyMediaPathMappings(preserved, &mappings, existing); err != nil {
		t.Fatal(err)
	}
	if preserved.MediaDownloadMode != MediaDownloadModeMapped || len(preserved.MediaPathMappings) != 2 {
		t.Fatalf("mapped config = mode %q mappings %#v", preserved.MediaDownloadMode, preserved.MediaPathMappings)
	}

	outside := []mediapath.Mapping{{ArrPath: "/ebooks", CantinarrPath: t.TempDir()}}
	if err := h.applyMediaPathMappings(preserved, &outside, existing); err == nil {
		t.Fatal("accepted a Cantinarr path outside the deployment allowlist")
	}
	nonArr := &Instance{ServiceType: "sabnzbd"}
	if err := h.applyMediaPathMappings(nonArr, &mappings, nil); err == nil {
		t.Fatal("accepted media mappings on a download client")
	}
}

func TestInstanceHTTPWritesPreserveOmittedMappingsAndDisableNewInstances(t *testing.T) {
	arr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(arr.Close)
	root := t.TempDir()
	movies := root + "/movies"
	if err := os.Mkdir(movies, 0700); err != nil {
		t.Fatal(err)
	}
	store := newTestStore(t)
	existing := &Instance{
		ServiceType:       "radarr",
		Name:              "Movies",
		URL:               arr.URL,
		APIKey:            "key",
		MediaDownloadMode: MediaDownloadModeMapped,
		MediaPathMappings: []mediapath.Mapping{{
			ArrPath:       "/movies",
			CantinarrPath: movies,
		}},
	}
	if err := store.Create(existing); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(store, NewRegistry(store))
	h.SetMediaDownloadRoots([]string{root})
	router := chi.NewRouter()
	router.Put("/instances/{instanceID}", h.Update)
	router.Post("/instances", h.Create)

	update := httptest.NewRequest(
		http.MethodPut,
		"/instances/"+existing.ID,
		strings.NewReader(`{"name":"Renamed","url":"`+arr.URL+`","api_key":"","is_default":true}`),
	)
	updatedResponse := httptest.NewRecorder()
	router.ServeHTTP(updatedResponse, update)
	if updatedResponse.Code != http.StatusOK {
		t.Fatalf("old-client update = %d %s", updatedResponse.Code, updatedResponse.Body.String())
	}
	updated, err := store.Get(existing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.MediaDownloadMode != MediaDownloadModeMapped ||
		len(updated.MediaPathMappings) != 1 ||
		updated.MediaPathMappings[0].CantinarrPath != movies {
		t.Fatalf("omitted update changed mappings: %+v", updated)
	}

	create := httptest.NewRequest(
		http.MethodPost,
		"/instances",
		strings.NewReader(`{"service_type":"radarr","name":"New Movies","url":"`+arr.URL+`","api_key":"key"}`),
	)
	createdResponse := httptest.NewRecorder()
	router.ServeHTTP(createdResponse, create)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("old-client create = %d %s", createdResponse.Code, createdResponse.Body.String())
	}
	var created instanceResponse
	if err := json.NewDecoder(createdResponse.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	createdStored, err := store.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if createdStored.MediaDownloadMode != MediaDownloadModeDisabled || created.MediaDownloads {
		t.Fatalf("new omitted mapping config = stored %q response enabled %t", createdStored.MediaDownloadMode, created.MediaDownloads)
	}
}

func TestMediaRootsReturnsOnlyConfiguredRootsWithoutCaching(t *testing.T) {
	h := NewHandler(newTestStore(t), nil)
	h.SetMediaDownloadRoots([]string{"/media/books", "/media/movies"})
	recorder := httptest.NewRecorder()
	h.MediaRoots(recorder, httptest.NewRequest(http.MethodGet, "/instances/media-roots", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", recorder.Header().Get("Cache-Control"))
	}
	var roots []string
	if err := json.NewDecoder(recorder.Body).Decode(&roots); err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 || roots[0] != "/media/books" || roots[1] != "/media/movies" {
		t.Fatalf("roots = %#v", roots)
	}
}

func TestInstanceURLRejectsEmbeddedSecrets(t *testing.T) {
	for _, rawURL := range []string{
		"http://user:password@example.test/sonarr",
		"https://example.test/sonarr?apiKey=secret",
		"https://example.test/sonarr#secret",
	} {
		inst := &Instance{ServiceType: "sonarr", Name: "TV", URL: rawURL, APIKey: "write-only"}
		if err := validateRequiredFields(inst); err == nil {
			t.Fatalf("validateRequiredFields(%q) accepted a secret-bearing URL", rawURL)
		}
	}
}

// Instance URLs are dialed only by the server, so cluster-internal names
// (Docker service names, k8s cluster DNS, Tailscale MagicDNS) are a supported
// production configuration — lock in that the URL contract accepts them.
func TestInstanceURLAcceptsClusterInternalHostnames(t *testing.T) {
	for _, rawURL := range []string{
		"http://radarr:7878",
		"http://sonarr",
		"https://radarr.media.svc.cluster.local:7878",
		"http://chaptarr:8787/books",
	} {
		inst := &Instance{ServiceType: "sonarr", Name: "TV", URL: rawURL, APIKey: "write-only"}
		if err := validateRequiredFields(inst); err != nil {
			t.Fatalf("validateRequiredFields(%q) = %v, want accepted", rawURL, err)
		}
	}
	// A schemeless host:port parses as an opaque URL, not an absolute one.
	inst := &Instance{ServiceType: "sonarr", Name: "TV", URL: "radarr:7878", APIKey: "write-only"}
	if err := validateRequiredFields(inst); err == nil {
		t.Fatal("validateRequiredFields accepted a schemeless URL")
	}
}

func TestTestConnectionEndpoint(t *testing.T) {
	const storedKey = "stored-api-secret"
	arr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/system/status" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Api-Key") != storedKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(arr.Close)

	s := newTestStore(t)
	stored := &Instance{ServiceType: "radarr", Name: "Movies", URL: arr.URL, APIKey: storedKey}
	if err := s.Create(stored); err != nil {
		t.Fatalf("create instance: %v", err)
	}

	h := NewHandler(s, nil)
	router := chi.NewRouter()
	router.Post("/instances/test", h.TestConnection)
	do := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("POST", "/instances/test", strings.NewReader(body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	// A candidate config tests without persisting anything; name is optional
	// because the Test button is usable before the form is complete.
	rec := do(`{"service_type":"radarr","url":"` + arr.URL + `","api_key":"` + storedKey + `"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("test candidate = %d %s, want 204", rec.Code, rec.Body.String())
	}

	// Editing an existing instance: blank credentials fall back to the stored
	// write-only ones, so re-testing an unmodified form passes.
	rec = do(`{"id":"` + stored.ID + `","url":"` + arr.URL + `","api_key":""}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("test with stored credentials = %d %s, want 204", rec.Code, rec.Body.String())
	}

	// A wrong key still fails even when an id is supplied.
	rec = do(`{"id":"` + stored.ID + `","url":"` + arr.URL + `","api_key":"wrong"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "connection test failed") {
		t.Fatalf("test with wrong key = %d %s, want 400 connection test failed", rec.Code, rec.Body.String())
	}

	if rec := do(`{"id":"radarr-missing","url":"` + arr.URL + `"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("test unknown id = %d, want 404", rec.Code)
	}
	if rec := do(`{"service_type":"floppy","url":"` + arr.URL + `"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("test unknown service type = %d, want 400", rec.Code)
	}
	if rec := do(`not json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("test invalid body = %d, want 400", rec.Code)
	}
}

func TestValidateArrURLDoesNotFollowRedirects(t *testing.T) {
	var redirectedRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedRequests.Add(1)
		if got := r.Header.Get("X-Api-Key"); got != "" {
			t.Errorf("redirect destination received X-Api-Key %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(destination.Close)

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Api-Key"); got != "validation-secret" {
			t.Errorf("validation source X-Api-Key = %q", got)
		}
		http.Redirect(w, r, destination.URL+"/stolen", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)

	err := validateArrURL(source.URL, "validation-secret", "v3")
	if err == nil || !strings.Contains(err.Error(), "status 307") {
		t.Fatalf("validateArrURL redirect error = %v, want status 307", err)
	}
	if got := redirectedRequests.Load(); got != 0 {
		t.Fatalf("redirect destination received %d requests, want 0", got)
	}
}

func TestValidateConnectionDoesNotFollowServiceRedirects(t *testing.T) {
	serviceTypes := []string{
		"radarr",
		"sonarr",
		"chaptarr",
		"sabnzbd",
		"qbittorrent",
		"nzbget",
		"transmission",
		"deluge",
		"rutorrent",
		"tautulli",
		"tracearr",
		"jellyfin",
		"emby",
		"plex",
	}

	for _, serviceType := range serviceTypes {
		t.Run(serviceType, func(t *testing.T) {
			var redirectedRequests atomic.Int32
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				redirectedRequests.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(destination.Close)

			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, destination.URL+"/credential-sink", http.StatusTemporaryRedirect)
			}))
			t.Cleanup(source.Close)

			inst := &Instance{
				ServiceType: serviceType,
				Name:        serviceType,
				URL:         source.URL,
				APIKey:      "service-api-secret",
				Username:    "service-user",
				Password:    "service-password",
			}
			if err := validateConnection(inst); err == nil {
				t.Fatal("validateConnection accepted an upstream redirect")
			}
			if got := redirectedRequests.Load(); got != 0 {
				t.Fatalf("redirect destination received %d requests, want 0", got)
			}
		})
	}
}

// TestQbittorrentCredentialShapes pins qBittorrent's two credential shapes
// through the HTTP surface: an API key alone or a username and password
// alone is accepted and neither is refused; a save carrying one shape clears
// the stored other while a save carrying neither keeps what is stored; the
// test endpoint falls back to the stored key and names a rejected one; and
// the admin list says which shape is stored without revealing the key.
func TestQbittorrentCredentialShapes(t *testing.T) {
	const key, rotated = "qbit-key-1", "qbit-key-2"
	qbit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			if r.Header.Get("Authorization") != "" {
				// qBittorrent refuses auth/* under a Bearer key.
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_ = r.ParseForm()
			if r.PostForm.Get("username") != "admin" || r.PostForm.Get("password") != "secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid"})
			w.WriteHeader(http.StatusNoContent)
		case "/api/v2/app/version":
			auth := r.Header.Get("Authorization")
			cookie, _ := r.Cookie("SID")
			keyed := auth == "Bearer "+key || auth == "Bearer "+rotated
			if !keyed && (cookie == nil || cookie.Value != "sid") {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = w.Write([]byte("v5.2.0"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(qbit.Close)

	for _, tc := range []struct {
		name    string
		inst    Instance
		wantErr bool
	}{
		{"key only", Instance{APIKey: key}, false},
		{"password only", Instance{Username: "admin", Password: "secret"}, false},
		{"nothing", Instance{}, true},
		{"username without password", Instance{Username: "admin"}, true},
	} {
		inst := tc.inst
		inst.ServiceType, inst.Name, inst.URL = "qbittorrent", "Torrents", qbit.URL
		err := validateRequiredFields(&inst)
		if (err != nil) != tc.wantErr {
			t.Fatalf("validateRequiredFields(%s) = %v, wantErr %t", tc.name, err, tc.wantErr)
		}
		if err != nil && !strings.Contains(err.Error(), "API key") {
			t.Fatalf("validateRequiredFields(%s) = %v, want the API key named as an option", tc.name, err)
		}
	}

	store := newTestStore(t)
	h := NewHandler(store, NewRegistry(store))
	router := chi.NewRouter()
	router.Post("/instances", h.Create)
	router.Put("/instances/{instanceID}", h.Update)
	router.Post("/instances/test", h.TestConnection)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}
	stored := func(id string) *Instance {
		t.Helper()
		inst, err := store.Get(id)
		if err != nil || inst == nil {
			t.Fatalf("store.Get(%s) = %v, %v", id, inst, err)
		}
		return inst
	}

	// Created with a key (and a stray username/password an old client might
	// still send): stored as a key alone, reported as one, never revealed.
	rec := do(http.MethodPost, "/instances", `{"service_type":"qbittorrent","name":"Torrents","url":"`+qbit.URL+`","api_key":"`+key+`","username":"stale","password":"stale"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create with key = %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	var created instanceResponse
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}
	if !created.HasAPIKey || created.Username != "" || strings.Contains(body, key) {
		t.Fatalf("create response = %s, want has_api_key with no username and no key", body)
	}
	if inst := stored(created.ID); inst.APIKey != key || inst.Username != "" || inst.Password != "" {
		t.Fatalf("stored after key create = %+v, want the key alone", inst)
	}

	// The test endpoint falls back to the stored key, and names a rejected one.
	if rec := do(http.MethodPost, "/instances/test", `{"id":"`+created.ID+`","url":"`+qbit.URL+`","api_key":""}`); rec.Code != http.StatusNoContent {
		t.Fatalf("test with stored key = %d %s", rec.Code, rec.Body.String())
	}
	rec = do(http.MethodPost, "/instances/test", `{"id":"`+created.ID+`","url":"`+qbit.URL+`","api_key":"wrong"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "API key") {
		t.Fatalf("test with wrong key = %d %s, want 400 naming the API key", rec.Code, rec.Body.String())
	}

	// Switching to a password clears the stored key.
	rec = do(http.MethodPut, "/instances/"+created.ID, `{"name":"Torrents","url":"`+qbit.URL+`","api_key":"","username":"admin","password":"secret"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update to password = %d %s", rec.Code, rec.Body.String())
	}
	var updated instanceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.HasAPIKey || updated.Username != "admin" {
		t.Fatalf("update response = %s, want a username and no has_api_key", rec.Body.String())
	}
	if inst := stored(created.ID); inst.APIKey != "" || inst.Username != "admin" || inst.Password != "secret" {
		t.Fatalf("stored after password update = %+v, want the password alone", inst)
	}

	// Switching back to a key clears the password.
	rec = do(http.MethodPut, "/instances/"+created.ID, `{"name":"Torrents","url":"`+qbit.URL+`","api_key":"`+rotated+`","username":"","password":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update back to key = %d %s", rec.Code, rec.Body.String())
	}
	if inst := stored(created.ID); inst.APIKey != rotated || inst.Username != "" || inst.Password != "" {
		t.Fatalf("stored after key update = %+v, want the rotated key alone", inst)
	}

	// An edit that carries no credential at all keeps the stored shape.
	rec = do(http.MethodPut, "/instances/"+created.ID, `{"name":"Renamed","url":"`+qbit.URL+`","api_key":"","username":"","password":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("credential-less update = %d %s", rec.Code, rec.Body.String())
	}
	if inst := stored(created.ID); inst.APIKey != rotated || inst.Name != "Renamed" {
		t.Fatalf("stored after credential-less update = %+v, want the key kept and the name changed", inst)
	}
}

// TestDelugeCredentialShape pins Deluge's single credential: the web UI
// password is required and a username plays no part. The connection test
// drives the web UI's login, daemon connection, and version call, and a
// rejected password is named without being echoed.
func TestDelugeCredentialShape(t *testing.T) {
	const password = "deluge-web-secret"
	var methods []string
	var mu sync.Mutex
	deluge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json" {
			http.NotFound(w, r)
			return
		}
		var call struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&call)
		mu.Lock()
		methods = append(methods, call.Method)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch call.Method {
		case "auth.login":
			var pw string
			if len(call.Params) > 0 {
				_ = json.Unmarshal(call.Params[0], &pw)
			}
			if pw != password {
				_, _ = w.Write([]byte(`{"result":false,"error":null,"id":1}`))
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "sess", Path: "/"})
			_, _ = w.Write([]byte(`{"result":true,"error":null,"id":1}`))
		case "web.connected":
			_, _ = w.Write([]byte(`{"result":true,"error":null,"id":2}`))
		case "daemon.get_version":
			_, _ = w.Write([]byte(`{"result":"2.1.1","error":null,"id":3}`))
		default:
			_, _ = w.Write([]byte(`{"result":null,"error":{"message":"Unknown method","code":2},"id":4}`))
		}
	}))
	t.Cleanup(deluge.Close)

	for _, tc := range []struct {
		name    string
		inst    Instance
		wantErr bool
	}{
		{"password only", Instance{Password: password}, false},
		{"username and password", Instance{Username: "ignored", Password: password}, false},
		{"nothing", Instance{}, true},
		{"username without password", Instance{Username: "admin"}, true},
		{"api key instead", Instance{APIKey: "not-how-deluge-works"}, true},
	} {
		inst := tc.inst
		inst.ServiceType, inst.Name, inst.URL = "deluge", "Torrents", deluge.URL
		err := validateRequiredFields(&inst)
		if (err != nil) != tc.wantErr {
			t.Fatalf("validateRequiredFields(%s) = %v, wantErr %t", tc.name, err, tc.wantErr)
		}
		if err != nil && !strings.Contains(err.Error(), "password is required for deluge") {
			t.Fatalf("validateRequiredFields(%s) = %v, want the password named", tc.name, err)
		}
	}

	store := newTestStore(t)
	h := NewHandler(store, NewRegistry(store))
	router := chi.NewRouter()
	router.Post("/instances", h.Create)
	router.Post("/instances/test", h.TestConnection)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}

	rec := do(http.MethodPost, "/instances", `{"service_type":"deluge","name":"Torrents","url":"`+deluge.URL+`/","password":"`+password+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), password) {
		t.Fatalf("create response reveals the password: %s", rec.Body.String())
	}
	var created instanceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	inst, err := store.Get(created.ID)
	if err != nil || inst == nil || inst.Password != password || inst.Username != "" || inst.URL != deluge.URL {
		t.Fatalf("stored = %+v, %v; want the password alone and the URL without its trailing slash", inst, err)
	}
	mu.Lock()
	got := strings.Join(methods, ",")
	mu.Unlock()
	if got != "auth.login,web.connected,daemon.get_version" {
		t.Fatalf("connection test called %s, want auth.login,web.connected,daemon.get_version", got)
	}

	rec = do(http.MethodPost, "/instances/test", `{"service_type":"deluge","name":"Torrents","url":"`+deluge.URL+`","password":"wrong-secret"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid password") {
		t.Fatalf("test with wrong password = %d %s, want 400 naming the password", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "wrong-secret") {
		t.Fatalf("test response echoes the password: %s", rec.Body.String())
	}
	if rec := do(http.MethodPost, "/instances/test", `{"service_type":"deluge","name":"Torrents","url":"`+deluge.URL+`"}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "password is required") {
		t.Fatalf("test without password = %d %s, want 400", rec.Code, rec.Body.String())
	}
}

// TestRutorrentCredentialShape pins ruTorrent's optional Basic-auth
// credentials: nothing is required, and the connection test reaches
// rTorrent through ruTorrent's httprpc plugin.
func TestRutorrentCredentialShape(t *testing.T) {
	var paths []string
	var mu sync.Mutex
	rt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if u, p, ok := r.BasicAuth(); ok && (u != "rt-user" || p != "rt-secret") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/plugins/httprpc/action.php" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><methodResponse><params><param><value><string>0.16.21</string></value></param></params></methodResponse>`))
	}))
	t.Cleanup(rt.Close)

	for _, tc := range []struct {
		name string
		inst Instance
	}{
		{"no credentials", Instance{}},
		{"credentials", Instance{Username: "rt-user", Password: "rt-secret"}},
	} {
		inst := tc.inst
		inst.ServiceType, inst.Name, inst.URL = "rutorrent", "Torrents", rt.URL
		if err := validateRequiredFields(&inst); err != nil {
			t.Fatalf("validateRequiredFields(%s) = %v, want nil", tc.name, err)
		}
	}

	store := newTestStore(t)
	h := NewHandler(store, NewRegistry(store))
	router := chi.NewRouter()
	router.Post("/instances", h.Create)
	router.Post("/instances/test", h.TestConnection)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}

	rec := do(http.MethodPost, "/instances", `{"service_type":"rutorrent","name":"Torrents","url":"`+rt.URL+`/"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create without credentials = %d %s", rec.Code, rec.Body.String())
	}
	mu.Lock()
	got := strings.Join(paths, ",")
	mu.Unlock()
	if got != "/plugins/httprpc/action.php" {
		t.Fatalf("connection test hit %s, want the httprpc plugin once", got)
	}

	rec = do(http.MethodPost, "/instances/test", `{"service_type":"rutorrent","name":"Torrents","url":"`+rt.URL+`","username":"rt-user","password":"wrong-secret"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "refused the credentials") || strings.Contains(rec.Body.String(), "wrong-secret") {
		t.Fatalf("test with wrong credentials = %d %s, want 400 naming the refusal without the password", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodPost, "/instances/test", `{"service_type":"rutorrent","name":"Torrents","url":"`+rt.URL+`","username":"rt-user","password":"rt-secret"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("test with credentials = %d %s", rec.Code, rec.Body.String())
	}
}
