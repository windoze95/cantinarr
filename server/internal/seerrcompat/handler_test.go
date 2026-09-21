package seerrcompat

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/request"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"github.com/windoze95/cantinarr-server/internal/serversettings"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

// The fixtures mirror what Maintainerr's own Seerr mock
// (tools/dev/fake-seerr.mjs) and its rule getter read, so a shape that
// satisfies these tests is one Maintainerr consumes: paginated /request with
// media.tmdbId and requestedBy names, per-title detail with mediaInfo.id and
// requests, season-level requests, and the deletion + availability-sync
// writes.

type harness struct {
	t        *testing.T
	db       *sql.DB
	settings *serversettings.Service
	handler  *Handler
	server   *httptest.Server
	key      string
	radarr   *fakeRadarr
	sonarr   *fakeSonarr
	admin    int64
	plexUser int64
	jfUser   int64
	local    int64
}

type fakeRadarr struct {
	movies string
	fail   bool
	hits   int
	added  int
}

func (f *fakeRadarr) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/movie":
			f.hits++
			if f.fail {
				http.Error(w, "down", http.StatusBadGateway)
				return
			}
			if r.URL.Query().Get("tmdbId") != "" {
				// The approval path asks for one title; none of the fixtures'
				// pending titles are in the library.
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(f.movies))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/movie/lookup":
			_, _ = w.Write([]byte(`[{"title":"Inception","tmdbId":27205,"year":2010}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Any"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/rootfolder":
			_, _ = w.Write([]byte(`[{"id":1,"path":"/movies"}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/movie":
			f.added++
			_, _ = w.Write([]byte(`{"id":99,"tmdbId":27205}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/queue":
			_, _ = w.Write([]byte(`{"totalRecords":0,"records":[]}`))
		default:
			t.Errorf("unexpected radarr request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

type fakeSonarr struct {
	series string
	fail   bool
}

func (f *fakeSonarr) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/series":
			if f.fail {
				http.Error(w, "down", http.StatusBadGateway)
				return
			}
			_, _ = w.Write([]byte(f.series))
		default:
			t.Errorf("unexpected sonarr request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func fakeTMDB(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/movie/550":
			_, _ = w.Write([]byte(`{"id":550,"title":"Fight Club","imdb_id":"tt0137523","release_date":"1999-10-15","overview":"Soap.","poster_path":"/fc.jpg"}`))
		case "/movie/404":
			http.Error(w, `{"status_message":"not found"}`, http.StatusNotFound)
		case "/tv/1396":
			_, _ = w.Write([]byte(`{"id":1396,"name":"Breaking Bad","first_air_date":"2008-01-20","seasons":[{"season_number":1,"name":"Season 1","episode_count":7,"air_date":"2008-01-20"},{"season_number":2,"name":"Season 2","episode_count":13,"air_date":"2009-03-08"}]}`))
		case "/tv/1396/season/2":
			_, _ = w.Write([]byte(`{"id":3573,"name":"Season 2","air_date":"2009-03-08","season_number":2,"episodes":[{"id":62086,"name":"Seven Thirty-Seven","air_date":"2009-03-08","season_number":2,"episode_number":1}]}`))
		default:
			t.Errorf("unexpected tmdb request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

const radarrLibrary = `[
  {"id":1,"title":"Fight Club","tmdbId":550,"hasFile":true,"monitored":true,"movieFile":{"id":10,"dateAdded":"2024-03-01T10:00:00Z"}},
  {"id":2,"title":"Heat","tmdbId":949,"hasFile":false,"monitored":true},
  {"id":3,"title":"Unrequested","tmdbId":603,"hasFile":true,"monitored":true}
]`

const sonarrLibrary = `[
  {"id":42,"title":"Breaking Bad","tvdbId":81189,"monitored":true,
   "seasons":[
     {"seasonNumber":1,"monitored":true,"statistics":{"episodeFileCount":7,"totalEpisodeCount":7,"episodeCount":7}},
     {"seasonNumber":2,"monitored":true,"statistics":{"episodeFileCount":3,"totalEpisodeCount":13,"episodeCount":13}}
   ]}
]`

func newHarness(t *testing.T) *harness {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x24}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}

	h := &harness{t: t, db: database, radarr: &fakeRadarr{movies: radarrLibrary}, sonarr: &fakeSonarr{series: sonarrLibrary}}
	h.admin = h.user("admin", "admin")
	h.plexUser = h.user("alice", "user")
	h.jfUser = h.user("bob", "user")
	h.local = h.user("carol", "user")

	radarrSrv := httptest.NewServer(h.radarr.handler(t))
	sonarrSrv := httptest.NewServer(h.sonarr.handler(t))
	t.Cleanup(radarrSrv.Close)
	t.Cleanup(sonarrSrv.Close)

	store := instance.NewStore(database, cipher)
	for _, inst := range []*instance.Instance{
		{ServiceType: "radarr", Name: "Radarr", URL: radarrSrv.URL, APIKey: "key"},
		{ServiceType: "sonarr", Name: "Sonarr", URL: sonarrSrv.URL, APIKey: "key"},
		{ServiceType: "jellyfin", Name: "Jellyfin", URL: "http://jellyfin.test", APIKey: "key"},
		{ServiceType: "plex", Name: "Plex", URL: "https://plex.tv", APIKey: "key"},
	} {
		if err := store.Create(inst); err != nil {
			t.Fatalf("create %s: %v", inst.ServiceType, err)
		}
		switch inst.ServiceType {
		case "jellyfin":
			h.exec(`INSERT INTO user_media_server_accounts (user_id, instance_id, remote_user_id, remote_username) VALUES (?, ?, 'jf-uuid-bob', 'bob-on-jellyfin')`, h.jfUser, inst.ID)
		case "plex":
			h.exec(`INSERT INTO user_media_server_accounts (user_id, instance_id, remote_user_id, remote_username) VALUES (?, ?, 'alice@example.test', 'alice_plex')`, h.plexUser, inst.ID)
		}
	}
	h.exec(`INSERT INTO plex_identities (plex_account_id, user_id, email, username) VALUES (12345, ?, 'alice@example.test', 'alice_plex')`, h.plexUser)

	h.settings = serversettings.NewService(database, func() bool { return false }, serversettings.WithCipher(cipher))
	requests := request.NewService(database, instance.NewRegistry(store), nil, nil)
	tmdbSrv := fakeTMDB(t)
	client := tmdb.NewClientWithBaseURL("token", tmdbSrv.URL)
	h.handler = NewHandler(database, requests, h.settings, func() *tmdb.Client { return client })
	h.handler.now = func() time.Time { return time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC) }

	issued, err := h.settings.IssueSeerrAPIKey(h.admin)
	if err != nil {
		t.Fatalf("IssueSeerrAPIKey: %v", err)
	}
	h.key = issued.Key

	root := chi.NewRouter()
	root.Mount("/api/v1", h.handler.Routes())
	root.Route("/api", func(r chi.Router) {
		r.Get("/health", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"status":"ok"}`)) })
	})
	h.server = httptest.NewServer(root)
	t.Cleanup(h.server.Close)
	return h
}

func (h *harness) exec(query string, args ...interface{}) {
	h.t.Helper()
	if _, err := h.db.Exec(query, args...); err != nil {
		h.t.Fatalf("exec %q: %v", query, err)
	}
}

func (h *harness) user(name, role string) int64 {
	h.t.Helper()
	res, err := h.db.Exec("INSERT INTO users (username, password_hash, role, created_at) VALUES (?, '', ?, '2026-01-02 03:04:05')", name, role)
	if err != nil {
		h.t.Fatalf("create user %s: %v", name, err)
	}
	id, _ := res.LastInsertId()
	return id
}

// request inserts a request_log row and returns its id.
func (h *harness) request(userID int64, mediaType string, tmdbID int, title, status string, requestedAt string) int64 {
	h.t.Helper()
	res, err := h.db.Exec(`INSERT INTO request_log (user_id, tmdb_id, media_type, title, status, requested_at) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, tmdbID, mediaType, title, status, requestedAt)
	if err != nil {
		h.t.Fatalf("insert request: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (h *harness) do(method, path string, key string) (*http.Response, []byte) {
	h.t.Helper()
	req, _ := http.NewRequest(method, h.server.URL+path, nil)
	if key != "" {
		req.Header.Set("X-Api-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp, buf.Bytes()
}

func (h *harness) get(path string) (int, map[string]interface{}) {
	h.t.Helper()
	resp, body := h.do(http.MethodGet, path, h.key)
	var out map[string]interface{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &out); err != nil {
			h.t.Fatalf("GET %s: not JSON: %s", path, body)
		}
	}
	return resp.StatusCode, out
}

func results(t *testing.T, body map[string]interface{}) []map[string]interface{} {
	t.Helper()
	raw, _ := body["results"].([]interface{})
	out := make([]map[string]interface{}, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.(map[string]interface{}))
	}
	return out
}

func num(v interface{}) int {
	f, _ := v.(float64)
	return int(f)
}

func TestAuthenticationFollowsTheIssuedKeyAndItsAdministrator(t *testing.T) {
	h := newHarness(t)

	resp, body := h.do(http.MethodGet, "/api/v1/status", "")
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(body), "X-Api-Key") {
		t.Fatalf("no key: %d %s", resp.StatusCode, body)
	}
	resp, _ = h.do(http.MethodGet, "/api/v1/status", "cantinarr-wrong")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d", resp.StatusCode)
	}
	resp, body = h.do(http.MethodGet, "/api/v1/status", h.key)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"version"`) {
		t.Fatalf("right key: %d %s", resp.StatusCode, body)
	}
	// The session API is untouched by the mount next to it.
	resp, _ = h.do(http.MethodGet, "/api/health", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/health through the sibling mount: %d", resp.StatusCode)
	}

	// Demoting the issuing administrator kills the key.
	h.exec("UPDATE users SET role = 'user' WHERE id = ?", h.admin)
	resp, body = h.do(http.MethodGet, "/api/v1/status", h.key)
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(body), "no longer authorizes") {
		t.Fatalf("demoted issuer: %d %s", resp.StatusCode, body)
	}
	h.exec("UPDATE users SET role = 'admin' WHERE id = ?", h.admin)

	// Revoking closes the surface; reissuing invalidates the old key.
	if err := h.settings.RevokeSeerrAPIKey(); err != nil {
		t.Fatal(err)
	}
	if resp, _ = h.do(http.MethodGet, "/api/v1/status", h.key); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked key: %d", resp.StatusCode)
	}
	fresh, err := h.settings.IssueSeerrAPIKey(h.admin)
	if err != nil {
		t.Fatal(err)
	}
	if resp, _ = h.do(http.MethodGet, "/api/v1/status", h.key); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old key after reissue: %d", resp.StatusCode)
	}
	if resp, _ = h.do(http.MethodGet, "/api/v1/status", fresh.Key); resp.StatusCode != http.StatusOK {
		t.Fatalf("fresh key: %d", resp.StatusCode)
	}
	if !strings.HasPrefix(fresh.Key, "cantinarr-") || len(fresh.Key) < 40 {
		t.Fatalf("key shape: %q", fresh.Key)
	}
}

func TestAboutReportsVersionAndCounts(t *testing.T) {
	h := newHarness(t)
	h.request(h.plexUser, "movie", 550, "Fight Club", "requested", "2026-01-10 00:00:00")
	h.request(h.local, "movie", 550, "Fight Club", "requested", "2026-01-11 00:00:00")
	h.request(h.jfUser, "tv", 1396, "Breaking Bad", "requested", "2026-01-12 00:00:00")
	h.request(h.local, "book", 0, "A Book", "requested", "2026-01-13 00:00:00")

	status, body := h.get("/api/v1/settings/about")
	if status != http.StatusOK {
		t.Fatalf("about: %d", status)
	}
	if body["version"] == "" || num(body["totalRequests"]) != 3 || num(body["totalMediaItems"]) != 2 {
		t.Fatalf("about = %v", body)
	}
}

func TestRequestListCarriesSeerrShapesAndLiveStatus(t *testing.T) {
	h := newHarness(t)
	fightClub := h.request(h.plexUser, "movie", 550, "Fight Club", "requested", "2026-01-10 08:00:00")
	h.exec("UPDATE request_log SET approved_by = ?, decided_at = '2026-01-10 09:00:00' WHERE id = ?", h.admin, fightClub)
	heat := h.request(h.jfUser, "movie", 949, "Heat", "requested", "2026-01-11 08:00:00")
	pendingID := h.request(h.local, "movie", 27205, "Inception", "pending", "2026-01-12 08:00:00")
	denied := h.request(h.local, "movie", 10, "Star Wars", "denied", "2026-01-13 08:00:00")
	failed := h.request(h.local, "movie", 11, "Missing", "requested", "2026-01-14 08:00:00")
	h.exec("UPDATE request_log SET add_failure_reason = 'radarr_unreachable' WHERE id = ?", failed)
	h.request(h.local, "book", 0, "A Book", "requested", "2026-01-15 08:00:00")
	bb := h.request(h.jfUser, "tv", 1396, "Breaking Bad", "requested", "2026-01-16 08:00:00")
	h.exec("UPDATE request_log SET tvdb_id = 81189 WHERE id = ?", bb)
	h.exec(`INSERT INTO request_tv_targets (request_id, snapshot) VALUES (?, '{"match":{"tvdb_id":81189},"source_seasons":[1,2],"target_seasons":[1,2]}')`, bb)

	status, body := h.get("/api/v1/request?take=100&skip=0&filter=all")
	if status != http.StatusOK {
		t.Fatalf("list: %d %v", status, body)
	}
	info := body["pageInfo"].(map[string]interface{})
	if num(info["results"]) != 6 || num(info["pages"]) != 1 || num(info["page"]) != 1 || num(info["pageSize"]) != 100 {
		t.Fatalf("pageInfo = %v", info)
	}
	rows := results(t, body)
	if len(rows) != 6 {
		t.Fatalf("rows = %d", len(rows))
	}
	byID := map[int64]map[string]interface{}{}
	for _, r := range rows {
		byID[int64(num(r["id"]))] = r
	}
	// Newest first, as Seerr's default sort.
	if num(rows[0]["id"]) != int(bb) || num(rows[5]["id"]) != int(fightClub) {
		t.Fatalf("order = %v ... %v", rows[0]["id"], rows[5]["id"])
	}

	fc := byID[fightClub]
	media := fc["media"].(map[string]interface{})
	if num(fc["status"]) != requestCompleted || num(media["status"]) != mediaAvailable {
		t.Fatalf("fight club status = %v / media %v", fc["status"], media["status"])
	}
	if num(media["tmdbId"]) != 550 || num(media["id"]) != 1100 || media["mediaType"] != "movie" {
		t.Fatalf("fight club media = %v", media)
	}
	if media["mediaAddedAt"] != "2024-03-01T10:00:00Z" {
		t.Fatalf("mediaAddedAt = %v", media["mediaAddedAt"])
	}
	if fc["updatedAt"] != "2026-01-10T09:00:00Z" || media["updatedAt"] != "2026-01-10T09:00:00Z" {
		t.Fatalf("approval timestamps = %v / %v", fc["updatedAt"], media["updatedAt"])
	}
	requester := fc["requestedBy"].(map[string]interface{})
	if requester["plexUsername"] != "alice_plex" || num(requester["userType"]) != userTypePlex || num(requester["plexId"]) != 12345 || requester["displayName"] != "alice" {
		t.Fatalf("plex requester = %v", requester)
	}
	if fc["modifiedBy"].(map[string]interface{})["displayName"] != "admin" {
		t.Fatalf("modifiedBy = %v", fc["modifiedBy"])
	}
	if fc["type"] != "movie" || fc["is4k"] != false || len(fc["seasons"].([]interface{})) != 0 {
		t.Fatalf("movie request shape = %v", fc)
	}

	ht := byID[heat]
	if num(ht["status"]) != requestApproved || num(ht["media"].(map[string]interface{})["status"]) != mediaProcessing {
		t.Fatalf("heat (monitored, no file) = %v", ht)
	}
	if by := ht["requestedBy"].(map[string]interface{}); by["jellyfinUsername"] != "bob-on-jellyfin" || num(by["userType"]) != userTypeJellyfin || by["plexUsername"] != nil {
		t.Fatalf("jellyfin requester = %v", by)
	}

	pd := byID[pendingID]
	if num(pd["status"]) != requestPending || num(pd["media"].(map[string]interface{})["status"]) != mediaUnknown {
		t.Fatalf("pending = %v", pd)
	}
	if by := pd["requestedBy"].(map[string]interface{}); num(by["userType"]) != userTypeLocal || by["plexUsername"] != nil || by["jellyfinUsername"] != nil || *stringPtr(by["username"]) != "carol" {
		t.Fatalf("local requester = %v", by)
	}
	if num(byID[denied]["status"]) != requestDeclined {
		t.Fatalf("denied = %v", byID[denied]["status"])
	}
	if num(byID[failed]["status"]) != requestFailed {
		t.Fatalf("failed = %v", byID[failed]["status"])
	}

	show := byID[bb]
	if show["type"] != "tv" || num(show["status"]) != requestApproved {
		t.Fatalf("tv request = %v", show)
	}
	showMedia := show["media"].(map[string]interface{})
	if num(showMedia["status"]) != mediaPartiallyAvailable || num(showMedia["tvdbId"]) != 81189 || num(showMedia["id"]) != 1396*2+1 {
		t.Fatalf("tv media = %v", showMedia)
	}
	seasons := show["seasons"].([]interface{})
	if len(seasons) != 2 || num(show["seasonCount"]) != 2 {
		t.Fatalf("tv seasons = %v", seasons)
	}
	s1, s2 := seasons[0].(map[string]interface{}), seasons[1].(map[string]interface{})
	if num(s1["seasonNumber"]) != 1 || num(s1["status"]) != requestCompleted || num(s2["seasonNumber"]) != 2 || num(s2["status"]) != requestApproved {
		t.Fatalf("season statuses = %v %v", s1, s2)
	}
	mediaSeasons := showMedia["seasons"].([]interface{})
	if len(mediaSeasons) != 2 || num(mediaSeasons[0].(map[string]interface{})["status"]) != mediaAvailable || num(mediaSeasons[1].(map[string]interface{})["status"]) != mediaPartiallyAvailable {
		t.Fatalf("media seasons = %v", mediaSeasons)
	}
	// The list endpoint never nests requests inside media (Seerr leaves it
	// out there; it would be circular).
	if _, nested := showMedia["requests"]; nested {
		t.Fatalf("media.requests must not appear on the list: %v", showMedia)
	}

	// Filters and pagination follow Seerr's vocabulary.
	status, body = h.get("/api/v1/request?filter=pending")
	if status != http.StatusOK || num(body["pageInfo"].(map[string]interface{})["results"]) != 1 {
		t.Fatalf("pending filter = %v", body)
	}
	status, body = h.get("/api/v1/request?filter=available")
	if rows := results(t, body); status != http.StatusOK || len(rows) != 1 || num(rows[0]["id"]) != int(fightClub) {
		t.Fatalf("available filter = %v", body)
	}
	status, body = h.get("/api/v1/request?filter=processing")
	if rows := results(t, body); status != http.StatusOK || len(rows) != 2 {
		t.Fatalf("processing filter = %v", body)
	}
	status, body = h.get("/api/v1/request?take=2&skip=2")
	info = body["pageInfo"].(map[string]interface{})
	if rows := results(t, body); status != http.StatusOK || len(rows) != 2 || num(info["page"]) != 2 || num(info["pages"]) != 3 {
		t.Fatalf("page 2 = %v", body)
	}
	status, body = h.get("/api/v1/request?mediaType=tv")
	if rows := results(t, body); status != http.StatusOK || len(rows) != 1 || rows[0]["type"] != "tv" {
		t.Fatalf("mediaType filter = %v", body)
	}
	status, body = h.get(fmt.Sprintf("/api/v1/request?requestedBy=%d", h.local))
	if rows := results(t, body); status != http.StatusOK || len(rows) != 3 {
		t.Fatalf("requestedBy filter = %v", body)
	}
	status, body = h.get("/api/v1/request?sort=modified&sortDirection=asc&take=1")
	if rows := results(t, body); status != http.StatusOK || num(rows[0]["id"]) != int(fightClub) {
		t.Fatalf("modified asc = %v", body)
	}

	status, body = h.get("/api/v1/request/count")
	if status != http.StatusOK {
		t.Fatalf("count: %d", status)
	}
	want := map[string]int{"total": 6, "movie": 5, "tv": 1, "pending": 1, "approved": 2, "declined": 1, "processing": 2, "available": 0, "completed": 1}
	for k, v := range want {
		if num(body[k]) != v {
			t.Errorf("count %s = %v, want %d (%v)", k, body[k], v, body)
		}
	}

	status, body = h.get(fmt.Sprintf("/api/v1/request/%d", heat))
	if status != http.StatusOK || num(body["id"]) != int(heat) {
		t.Fatalf("single request = %d %v", status, body)
	}
	if status, _ = h.get("/api/v1/request/999999"); status != http.StatusNotFound {
		t.Fatalf("missing request = %d", status)
	}

	// The Radarr library was fetched once for all of that: the digest is
	// shared and cached.
	if h.radarr.hits != 1 {
		t.Fatalf("radarr fetches = %d, want 1", h.radarr.hits)
	}
}

func stringPtr(v interface{}) *string {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	return &s
}

func TestLegacyTVRowsResolveSeasonsAgainstTheLibrary(t *testing.T) {
	h := newHarness(t)
	all := h.request(h.local, "tv", 1396, "Breaking Bad", "requested", "2026-01-16 08:00:00")
	h.exec("UPDATE request_log SET tvdb_id = 81189, season_scope = 'all' WHERE id = ?", all)
	latest := h.request(h.local, "tv", 1396, "Breaking Bad", "requested", "2026-01-17 08:00:00")
	h.exec("UPDATE request_log SET tvdb_id = 81189, season_scope = 'latest' WHERE id = ?", latest)
	explicit := h.request(h.local, "tv", 1396, "Breaking Bad", "requested", "2026-01-18 08:00:00")
	h.exec("UPDATE request_log SET tvdb_id = 81189, season_scope = '[2]' WHERE id = ?", explicit)
	// A show no library holds, mapped through the bridge cache only.
	unknown := h.request(h.local, "tv", 60625, "Rick and Morty", "requested", "2026-01-19 08:00:00")
	h.exec("INSERT INTO tmdb_tvdb_cache (tmdb_id, tvdb_id) VALUES (60625, 275274)")

	_, body := h.get("/api/v1/request?take=10")
	seasonsOf := map[int64][]int{}
	statusOf := map[int64]int{}
	for _, r := range results(t, body) {
		var numbers []int
		for _, s := range r["seasons"].([]interface{}) {
			numbers = append(numbers, num(s.(map[string]interface{})["seasonNumber"]))
		}
		seasonsOf[int64(num(r["id"]))] = numbers
		statusOf[int64(num(r["id"]))] = num(r["media"].(map[string]interface{})["status"])
	}
	if got := seasonsOf[all]; len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("all → %v", got)
	}
	if got := seasonsOf[latest]; len(got) != 1 || got[0] != 2 {
		t.Fatalf("latest → %v", got)
	}
	if got := seasonsOf[explicit]; len(got) != 1 || got[0] != 2 {
		t.Fatalf("explicit → %v", got)
	}
	if got := seasonsOf[unknown]; len(got) != 0 || statusOf[unknown] != mediaUnknown {
		t.Fatalf("unknown show → seasons %v status %d", got, statusOf[unknown])
	}
}

func TestUnreadableLibraryIsA503NeverAThinnerLedger(t *testing.T) {
	h := newHarness(t)
	h.request(h.plexUser, "movie", 550, "Fight Club", "requested", "2026-01-10 08:00:00")
	h.radarr.fail = true

	status, body := h.get("/api/v1/request")
	if status != http.StatusServiceUnavailable || !strings.Contains(fmt.Sprint(body["message"]), "could not read") {
		t.Fatalf("list with Radarr down = %d %v", status, body)
	}
	if status, _ = h.get("/api/v1/request/count"); status != http.StatusServiceUnavailable {
		t.Fatalf("count with Radarr down = %d", status)
	}
	if status, _ = h.get("/api/v1/movie/550"); status != http.StatusServiceUnavailable {
		t.Fatalf("movie with Radarr down = %d", status)
	}
	// A TV-only read never touches Radarr, so it still answers.
	if status, _ = h.get("/api/v1/request?mediaType=tv"); status != http.StatusOK {
		t.Fatalf("tv list with Radarr down = %d", status)
	}
}

func TestTitleDetailCarriesMediaInfoWithRequests(t *testing.T) {
	h := newHarness(t)
	first := h.request(h.plexUser, "movie", 550, "Fight Club", "requested", "2026-01-10 08:00:00")
	second := h.request(h.local, "movie", 550, "Fight Club", "requested", "2026-01-12 08:00:00")

	status, body := h.get("/api/v1/movie/550")
	if status != http.StatusOK {
		t.Fatalf("movie: %d %v", status, body)
	}
	if body["title"] != "Fight Club" || body["releaseDate"] != "1999-10-15" || num(body["id"]) != 550 || body["mediaType"] != "movie" {
		t.Fatalf("movie shape = %v", body)
	}
	info := body["mediaInfo"].(map[string]interface{})
	if num(info["id"]) != 1100 || num(info["status"]) != mediaAvailable || num(info["tmdbId"]) != 550 {
		t.Fatalf("mediaInfo = %v", info)
	}
	reqs := info["requests"].([]interface{})
	// Oldest first: integrators read requests[0].createdAt as the request date.
	if len(reqs) != 2 || num(reqs[0].(map[string]interface{})["id"]) != int(first) || num(reqs[1].(map[string]interface{})["id"]) != int(second) {
		t.Fatalf("mediaInfo.requests = %v", reqs)
	}
	if info["createdAt"] != "2026-01-10T08:00:00Z" {
		t.Fatalf("media createdAt = %v", info["createdAt"])
	}

	// A title in a library that nobody requested still has media info (Seerr
	// learns those from its library scan); one nowhere has none.
	h.exec("UPDATE request_log SET tmdb_id = 603 WHERE id = ?", second)
	status, body = h.get("/api/v1/movie/550")
	info = body["mediaInfo"].(map[string]interface{})
	if status != http.StatusOK || len(info["requests"].([]interface{})) != 1 {
		t.Fatalf("after moving a request: %v", body)
	}
	h.exec("DELETE FROM request_log")
	status, body = h.get("/api/v1/movie/550")
	if info := body["mediaInfo"].(map[string]interface{}); status != http.StatusOK || num(info["status"]) != mediaAvailable || len(info["requests"].([]interface{})) != 0 {
		t.Fatalf("library-only title: %v", body)
	}
	if status, _ = h.get("/api/v1/movie/404"); status != http.StatusBadGateway {
		t.Fatalf("tmdb failure = %d", status)
	}

	bb := h.request(h.jfUser, "tv", 1396, "Breaking Bad", "requested", "2026-01-16 08:00:00")
	h.exec(`INSERT INTO request_tv_targets (request_id, snapshot) VALUES (?, '{"match":{"tvdb_id":81189},"source_seasons":[2],"target_seasons":[2]}')`, bb)
	status, body = h.get("/api/v1/tv/1396")
	if status != http.StatusOK || body["name"] != "Breaking Bad" || body["firstAirDate"] != "2008-01-20" {
		t.Fatalf("tv: %d %v", status, body)
	}
	tvSeasons := body["seasons"].([]interface{})
	if len(tvSeasons) != 2 || tvSeasons[1].(map[string]interface{})["airDate"] != "2009-03-08" {
		t.Fatalf("tv seasons = %v", tvSeasons)
	}
	info = body["mediaInfo"].(map[string]interface{})
	if num(info["id"]) != 1396*2+1 || num(info["status"]) != mediaPartiallyAvailable {
		t.Fatalf("tv mediaInfo = %v", info)
	}
	tvReq := info["requests"].([]interface{})[0].(map[string]interface{})
	if seasons := tvReq["seasons"].([]interface{}); len(seasons) != 1 || num(seasons[0].(map[string]interface{})["seasonNumber"]) != 2 {
		t.Fatalf("tv request seasons = %v", tvReq)
	}

	status, body = h.get("/api/v1/tv/1396/season/2")
	if status != http.StatusOK || body["airDate"] != "2009-03-08" || num(body["seasonNumber"]) != 2 {
		t.Fatalf("season: %d %v", status, body)
	}
	episodes := body["episodes"].([]interface{})
	if ep := episodes[0].(map[string]interface{}); len(episodes) != 1 || num(ep["episodeNumber"]) != 1 || ep["airDate"] != "2009-03-08" {
		t.Fatalf("episodes = %v", episodes)
	}
}

func TestDeletionForgetsRequestsAndReleasesAllowances(t *testing.T) {
	h := newHarness(t)
	a := h.request(h.plexUser, "movie", 550, "Fight Club", "requested", "2026-01-10 08:00:00")
	b := h.request(h.local, "movie", 550, "Fight Club", "requested", "2026-01-12 08:00:00")
	book := h.request(h.local, "book", 0, "A Book", "requested", "2026-01-13 08:00:00")
	h.exec(`INSERT INTO request_quota_charges (id, user_id, media_type, instance_id, unit_key, charged_at) VALUES (7, ?, 'movie', 'inst', '550', 1)`, h.plexUser)
	h.exec(`INSERT INTO request_quota_items (request_id, user_id, media_type, unit_key, charge_id) VALUES (?, ?, 'movie', '550', 7)`, a, h.plexUser)

	resp, _ := h.do(http.MethodDelete, fmt.Sprintf("/api/v1/request/%d", a), h.key)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete request = %d", resp.StatusCode)
	}
	var n int
	_ = h.db.QueryRow("SELECT COUNT(*) FROM request_log WHERE id = ?", a).Scan(&n)
	if n != 0 {
		t.Fatal("request row survived its deletion")
	}
	var released sql.NullInt64
	var refunded sql.NullInt64
	_ = h.db.QueryRow("SELECT released_at FROM request_quota_items WHERE request_id = ?", a).Scan(&released)
	_ = h.db.QueryRow("SELECT refunded_at FROM request_quota_charges WHERE id = 7").Scan(&refunded)
	if !released.Valid || !refunded.Valid {
		t.Fatalf("allowance not released/refunded: %v %v", released, refunded)
	}
	if resp, _ = h.do(http.MethodDelete, fmt.Sprintf("/api/v1/request/%d", a), h.key); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete twice = %d", resp.StatusCode)
	}
	// A book request is invisible to this surface, including to deletion.
	if resp, _ = h.do(http.MethodDelete, fmt.Sprintf("/api/v1/request/%d", book), h.key); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete book = %d", resp.StatusCode)
	}
	_ = h.db.QueryRow("SELECT COUNT(*) FROM request_log WHERE id = ?", book).Scan(&n)
	if n != 1 {
		t.Fatal("book request was deleted through the Seerr surface")
	}

	// A running delivery blocks the delete.
	h.exec(`INSERT INTO request_dispatch (request_id, format, state) VALUES (?, '', 'processing')`, b)
	if resp, body := h.do(http.MethodDelete, fmt.Sprintf("/api/v1/request/%d", b), h.key); resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), "still being delivered") {
		t.Fatalf("delete in-flight = %d %s", resp.StatusCode, body)
	}
	h.exec(`UPDATE request_dispatch SET state = 'complete' WHERE request_id = ?`, b)

	// Media deletion forgets every request of the title, by the media id the
	// detail endpoint handed out; an unknown id is a 204 like Seerr's.
	c := h.request(h.jfUser, "movie", 550, "Fight Club", "pending", "2026-01-14 08:00:00")
	if resp, _ = h.do(http.MethodDelete, "/api/v1/media/1100", h.key); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete media = %d", resp.StatusCode)
	}
	_ = h.db.QueryRow("SELECT COUNT(*) FROM request_log WHERE id IN (?, ?)", b, c).Scan(&n)
	if n != 0 {
		t.Fatalf("media deletion left %d rows", n)
	}
	if resp, _ = h.do(http.MethodDelete, "/api/v1/media/98765432", h.key); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete unknown media = %d", resp.StatusCode)
	}
}

func TestDeclineActsAsTheIssuingAdministrator(t *testing.T) {
	h := newHarness(t)
	pending := h.request(h.local, "movie", 27205, "Inception", "pending", "2026-01-12 08:00:00")

	req, _ := http.NewRequest(http.MethodPost, h.server.URL+fmt.Sprintf("/api/v1/request/%d/decline", pending), strings.NewReader("{}"))
	req.Header.Set("X-Api-Key", h.key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if resp.StatusCode != http.StatusOK || num(body["status"]) != requestDeclined {
		t.Fatalf("decline = %d %v", resp.StatusCode, body)
	}
	var status string
	var approvedBy int64
	_ = h.db.QueryRow("SELECT status, approved_by FROM request_log WHERE id = ?", pending).Scan(&status, &approvedBy)
	if status != "denied" || approvedBy != h.admin {
		t.Fatalf("row after decline = %s by %d", status, approvedBy)
	}
	if body["modifiedBy"].(map[string]interface{})["displayName"] != "admin" {
		t.Fatalf("modifiedBy = %v", body["modifiedBy"])
	}

	// Declining again is refused with Seerr's error body.
	resp2, _ := h.do(http.MethodPost, fmt.Sprintf("/api/v1/request/%d/decline", pending), h.key)
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("second decline = %d", resp2.StatusCode)
	}
}

func TestApproveAddsToTheLibraryAsTheIssuingAdministrator(t *testing.T) {
	h := newHarness(t)
	// The requester's default library is the one the approval adds to.
	var radarrID string
	if err := h.db.QueryRow("SELECT id FROM service_instances WHERE service_type = 'radarr'").Scan(&radarrID); err != nil {
		t.Fatal(err)
	}
	h.exec("INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (?, 'radarr', ?)", h.local, radarrID)
	pending := h.request(h.local, "movie", 27205, "Inception", "pending", "2026-01-12 08:00:00")

	resp, body := h.do(http.MethodPost, fmt.Sprintf("/api/v1/request/%d/approve", pending), h.key)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve = %d %s", resp.StatusCode, body)
	}
	var out map[string]interface{}
	_ = json.Unmarshal(body, &out)
	if num(out["status"]) != requestApproved || out["modifiedBy"].(map[string]interface{})["displayName"] != "admin" {
		t.Fatalf("approved request = %v", out)
	}
	if h.radarr.added != 1 {
		t.Fatalf("radarr adds = %d, want 1", h.radarr.added)
	}
	var status string
	var approvedBy int64
	_ = h.db.QueryRow("SELECT status, approved_by FROM request_log WHERE id = ?", pending).Scan(&status, &approvedBy)
	if status != "requested" || approvedBy != h.admin {
		t.Fatalf("row after approve = %s by %d", status, approvedBy)
	}
}

func TestAvailabilitySyncDropsTheCachedLibraryDigests(t *testing.T) {
	h := newHarness(t)
	h.request(h.plexUser, "movie", 550, "Fight Club", "requested", "2026-01-10 08:00:00")
	if status, _ := h.get("/api/v1/request"); status != http.StatusOK {
		t.Fatal("first read")
	}
	if status, _ := h.get("/api/v1/request"); status != http.StatusOK || h.radarr.hits != 1 {
		t.Fatalf("second read should be served from the digest cache: hits=%d", h.radarr.hits)
	}
	resp, body := h.do(http.MethodPost, "/api/v1/settings/jobs/availability-sync/run", h.key)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"id":"availability-sync"`) {
		t.Fatalf("run job = %d %s", resp.StatusCode, body)
	}
	if status, _ := h.get("/api/v1/request"); status != http.StatusOK || h.radarr.hits != 2 {
		t.Fatalf("read after sync should refetch: hits=%d", h.radarr.hits)
	}
	if resp, _ = h.do(http.MethodPost, "/api/v1/settings/jobs/plex-full-scan/run", h.key); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown job = %d", resp.StatusCode)
	}
	if resp, body := h.do(http.MethodGet, "/api/v1/settings/jobs", h.key); resp.StatusCode != http.StatusOK || !strings.HasPrefix(string(body), `[{"cronSchedule"`) {
		t.Fatalf("jobs list = %d %s", resp.StatusCode, body)
	}
}

func TestUsersCarryTheirMediaServerIdentities(t *testing.T) {
	h := newHarness(t)
	h.request(h.plexUser, "movie", 550, "Fight Club", "requested", "2026-01-10 08:00:00")
	h.request(h.plexUser, "tv", 1396, "Breaking Bad", "requested", "2026-01-11 08:00:00")

	status, body := h.get("/api/v1/user?take=50&skip=0")
	if status != http.StatusOK {
		t.Fatalf("users: %d", status)
	}
	info := body["pageInfo"].(map[string]interface{})
	if num(info["results"]) != 4 || num(info["pages"]) != 1 {
		t.Fatalf("pageInfo = %v", info)
	}
	byName := map[string]map[string]interface{}{}
	for _, u := range results(t, body) {
		byName[u["displayName"].(string)] = u
	}
	admin := byName["admin"]
	if num(admin["permissions"]) != permissionAdmin || num(admin["userType"]) != userTypeLocal {
		t.Fatalf("admin = %v", admin)
	}
	alice := byName["alice"]
	if alice["plexUsername"] != "alice_plex" || alice["email"] != "alice@example.test" || num(alice["requestCount"]) != 2 || num(alice["permissions"]) != permissionRequest {
		t.Fatalf("alice = %v", alice)
	}
	bob := byName["bob"]
	if bob["jellyfinUsername"] != "bob-on-jellyfin" || bob["jellyfinUserId"] != "jf-uuid-bob" || num(bob["userType"]) != userTypeJellyfin {
		t.Fatalf("bob = %v", bob)
	}
	carol := byName["carol"]
	if carol["plexUsername"] != nil || carol["jellyfinUsername"] != nil || carol["email"] != "" {
		t.Fatalf("carol = %v", carol)
	}

	status, body = h.get(fmt.Sprintf("/api/v1/user/%d", h.jfUser))
	if status != http.StatusOK || body["displayName"] != "bob" {
		t.Fatalf("single user = %d %v", status, body)
	}
	if status, _ = h.get("/api/v1/user/424242"); status != http.StatusNotFound {
		t.Fatalf("missing user = %d", status)
	}
}

func TestAdminKeyRoutes(t *testing.T) {
	h := newHarness(t)
	// The admin routes run behind the session middleware in production; here
	// the handler is exercised with claims placed the way that middleware
	// places them.
	call := func(method string) (int, keyResponse) {
		req := httptest.NewRequest(method, "/api/admin/seerr-api", nil)
		req = req.WithContext(withAdminClaims(req.Context(), h.admin, "admin"))
		rec := httptest.NewRecorder()
		h.handler.AdminKey(rec, req)
		var out keyResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	if code, out := call(http.MethodGet); code != http.StatusOK || !out.Configured || out.APIKey != h.key || out.IssuedBy != "admin" {
		t.Fatalf("GET = %d %+v", code, out)
	}
	code, reissued := call(http.MethodPost)
	if code != http.StatusOK || reissued.APIKey == h.key || !strings.HasPrefix(reissued.APIKey, "cantinarr-") {
		t.Fatalf("POST = %d %+v", code, reissued)
	}
	if resp, _ := h.do(http.MethodGet, "/api/v1/status", reissued.APIKey); resp.StatusCode != http.StatusOK {
		t.Fatalf("reissued key rejected: %d", resp.StatusCode)
	}
	if code, out := call(http.MethodDelete); code != http.StatusOK || out.Configured || out.APIKey != "" {
		t.Fatalf("DELETE = %d %+v", code, out)
	}
	if resp, _ := h.do(http.MethodGet, "/api/v1/status", reissued.APIKey); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked key accepted: %d", resp.StatusCode)
	}
}

func TestUnknownRoutesAnswerSeerrStyle(t *testing.T) {
	h := newHarness(t)
	resp, body := h.do(http.MethodGet, "/api/v1/discover/movies", h.key)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(string(body), `"message"`) {
		t.Fatalf("unknown route = %d %s", resp.StatusCode, body)
	}
}

func withAdminClaims(ctx context.Context, userID int64, username string) context.Context {
	return context.WithValue(ctx, auth.ClaimsKey, &auth.Claims{UserID: userID, Username: username, Role: auth.RoleAdmin})
}
