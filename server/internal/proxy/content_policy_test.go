package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// arrFake is a Radarr or Sonarr that answers a path table and records the
// query it was asked with, so forced includes are observable.
type arrFake struct {
	mu      sync.Mutex
	bodies  map[string]string
	raw     map[string]string // non-JSON bodies
	queries map[string]string
	server  *httptest.Server
}

func newArrFake(t *testing.T) *arrFake {
	t.Helper()
	f := &arrFake{bodies: map[string]string{}, raw: map[string]string{}, queries: map[string]string{}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.queries[r.URL.Path] = r.URL.RawQuery
		body, ok := f.bodies[r.URL.Path]
		raw, isRaw := f.raw[r.URL.Path]
		f.mu.Unlock()
		if isRaw {
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, raw)
			return
		}
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"NotFound"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *arrFake) query(path string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.queries[path]
}

type kidsProxyEnv struct {
	radarr, sonarr, chaptarr       *arrFake
	radarrID, sonarrID, chaptarrID string
	proxy                          *httptest.Server
	kid                            *auth.User
	adult                          *auth.User
	admin                          *auth.User
	identity                       *auth.User
	mu                             sync.Mutex
}

func newKidsProxyEnv(t *testing.T) *kidsProxyEnv {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := instance.NewStore(database, cipher)
	e := &kidsProxyEnv{radarr: newArrFake(t), sonarr: newArrFake(t), chaptarr: newArrFake(t)}
	for _, entry := range []struct {
		serviceType string
		fake        *arrFake
		id          *string
	}{{"radarr", e.radarr, &e.radarrID}, {"sonarr", e.sonarr, &e.sonarrID}, {"chaptarr", e.chaptarr, &e.chaptarrID}} {
		inst := &instance.Instance{ServiceType: entry.serviceType, Name: entry.serviceType, URL: entry.fake.server.URL, APIKey: "key"}
		if err := store.Create(inst); err != nil {
			t.Fatal(err)
		}
		*entry.id = inst.ID
	}
	insert := func(name, role string) *auth.User {
		res, err := database.Exec("INSERT INTO users (username, password_hash, role) VALUES (?, '', ?)", name, role)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return &auth.User{ID: id, Username: name, Role: role}
	}
	e.kid = insert("kid", auth.RoleUser)
	e.adult = insert("adult", auth.RoleUser)
	e.admin = insert("admin", auth.RoleAdmin)
	// No TMDB: the built-in US scheme ranks the arr certifications.
	policies := contentpolicy.New(database, nil, nil)
	if err := policies.Store.Set(e.kid.ID, contentpolicy.Policy{MaxMovieRating: "PG", MaxTVRating: "TV-PG", RatingRegion: "US", BlockUnrated: true, BlockedMovieGenres: []int{27}, BlockedTVGenres: []int{10768}}); err != nil {
		t.Fatal(err)
	}
	e.kid.Child = true

	handler := NewHandler(store)
	handler.SetContentPolicy(policies)
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			e.mu.Lock()
			user := e.identity
			e.mu.Unlock()
			if user != nil {
				ctx := context.WithValue(r.Context(), auth.ClaimsKey, &auth.Claims{UserID: user.ID, Username: user.Username, Role: user.Role})
				ctx = context.WithValue(ctx, auth.UserKey, user)
				r = r.WithContext(ctx)
			}
			next.ServeHTTP(w, r)
		})
	})
	router.HandleFunc("/api/instances/{instanceID}/*", handler.InstanceProxy())
	e.proxy = httptest.NewServer(router)
	t.Cleanup(e.proxy.Close)
	return e
}

func (e *kidsProxyEnv) as(user *auth.User) *kidsProxyEnv {
	e.mu.Lock()
	e.identity = user
	e.mu.Unlock()
	return e
}

func (e *kidsProxyEnv) get(t *testing.T, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(e.proxy.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func titlesOf(t *testing.T, body string) []string {
	t.Helper()
	var elements []map[string]any
	if err := json.Unmarshal([]byte(body), &elements); err != nil {
		t.Fatalf("decode array: %v (%s)", err, body)
	}
	titles := make([]string, 0, len(elements))
	for _, el := range elements {
		if title, ok := el["title"].(string); ok {
			titles = append(titles, title)
		}
	}
	return titles
}

const radarrLibrary = `[
	{"id":1,"title":"Gentle","tmdbId":1,"certification":"G","genres":["Drama"]},
	{"id":2,"title":"Grown","tmdbId":2,"certification":"R","genres":["Drama"]},
	{"id":3,"title":"Gory","tmdbId":3,"certification":"PG","genres":["Horror","Drama"]},
	{"id":4,"title":"Unrated","tmdbId":4,"certification":"","genres":["Drama"]},
	{"id":5,"title":"Fine","tmdbId":5,"certification":"pg","genres":[]}
]`

func TestKidsAccountRadarrLibraryIsCutByCertificationAndGenre(t *testing.T) {
	e := newKidsProxyEnv(t)
	e.radarr.bodies["/api/v3/movie"] = radarrLibrary

	status, body := e.as(e.kid).get(t, "/api/instances/"+e.radarrID+"/api/v3/movie")
	if status != http.StatusOK {
		t.Fatalf("kid library: %d %s", status, body)
	}
	if titles := titlesOf(t, body); strings.Join(titles, ",") != "Gentle,Fine" {
		t.Fatalf("kid library = %v, want Gentle and Fine", titles)
	}
	if _, body := e.as(e.adult).get(t, "/api/instances/"+e.radarrID+"/api/v3/movie"); len(titlesOf(t, body)) != 5 {
		t.Fatalf("adult library = %v", titlesOf(t, body))
	}
	if _, body := e.as(e.admin).get(t, "/api/instances/"+e.radarrID+"/api/v3/movie"); len(titlesOf(t, body)) != 5 {
		t.Fatalf("admin library = %v", titlesOf(t, body))
	}
	// No identity at all (a harness without auth): verbatim.
	if _, body := e.as(nil).get(t, "/api/instances/"+e.radarrID+"/api/v3/movie"); len(titlesOf(t, body)) != 5 {
		t.Fatalf("anonymous library = %v", titlesOf(t, body))
	}
}

func TestKidsAccountRadarrQueueForcesIncludeMovieAndDropsOrphans(t *testing.T) {
	e := newKidsProxyEnv(t)
	e.radarr.bodies["/api/v3/queue"] = `{"page":1,"pageSize":50,"totalRecords":3,"records":[
		{"id":10,"movieId":1,"movie":{"title":"Gentle","certification":"G","genres":["Drama"]}},
		{"id":11,"movieId":2,"movie":{"title":"Grown","certification":"R","genres":["Drama"]}},
		{"id":12,"movieId":6}
	]}`
	status, body := e.as(e.kid).get(t, "/api/instances/"+e.radarrID+"/api/v3/queue?pageSize=50")
	if status != http.StatusOK {
		t.Fatalf("kid queue: %d %s", status, body)
	}
	var page struct {
		TotalRecords int              `json:"totalRecords"`
		Records      []map[string]any `json:"records"`
	}
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 1 || page.Records[0]["movieId"].(float64) != 1 {
		t.Fatalf("kid queue records = %v, want the G movie only (the orphan row has nothing to judge)", page.Records)
	}
	if page.TotalRecords != 3 {
		t.Fatalf("totalRecords = %d, want the upstream count untouched", page.TotalRecords)
	}
	if q := e.radarr.query("/api/v3/queue"); !strings.Contains(q, "includeMovie=true") || !strings.Contains(q, "pageSize=50") {
		t.Fatalf("upstream queue query = %q, want includeMovie forced alongside the client's params", q)
	}
	e.as(e.adult).get(t, "/api/instances/"+e.radarrID+"/api/v3/queue?pageSize=50")
	if q := e.radarr.query("/api/v3/queue"); strings.Contains(q, "includeMovie") {
		t.Fatalf("adult queue query = %q, want the client's params only", q)
	}
}

func TestKidsAccountSonarrReadsForceIncludeSeries(t *testing.T) {
	e := newKidsProxyEnv(t)
	e.sonarr.bodies["/api/v3/calendar"] = `[
		{"id":1,"seriesId":1,"title":"Pilot","series":{"title":"Soft","certification":"TV-Y","genres":["Kids"]}},
		{"id":2,"seriesId":2,"title":"Pilot","series":{"title":"Hard","certification":"TV-MA","genres":["Drama"]}},
		{"id":3,"seriesId":3,"title":"Pilot","series":{"title":"Politics","certification":"TV-G","genres":["War & Politics"]}},
		{"id":4,"seriesId":4,"title":"Orphan"}
	]`
	e.sonarr.bodies["/api/v3/history"] = `{"records":[{"id":1,"series":{"title":"Soft","certification":"TV-Y"}},{"id":2,"series":{"title":"Hard","certification":"TV-14"}}]}`
	e.sonarr.bodies["/api/v3/series"] = `[{"id":1,"title":"Soft","certification":"TV-Y","genres":[]},{"id":2,"title":"Hard","certification":"TV-MA","genres":[]}]`
	e.sonarr.bodies["/api/v3/series/1"] = `{"id":1,"title":"Soft","certification":"TV-Y","genres":[]}`
	e.sonarr.bodies["/api/v3/series/2"] = `{"id":2,"title":"Hard","certification":"TV-MA","genres":[]}`
	e.sonarr.bodies["/api/v3/episode"] = `[{"id":9,"seriesId":2,"series":{"title":"Hard","certification":"TV-MA"}}]`

	status, body := e.as(e.kid).get(t, "/api/instances/"+e.sonarrID+"/api/v3/calendar?start=2026-01-01&end=2026-01-08")
	if status != http.StatusOK {
		t.Fatalf("kid calendar: %d %s", status, body)
	}
	var episodes []map[string]any
	if err := json.Unmarshal([]byte(body), &episodes); err != nil {
		t.Fatal(err)
	}
	if len(episodes) != 1 || episodes[0]["seriesId"].(float64) != 1 {
		t.Fatalf("kid calendar = %v, want the TV-Y series only", episodes)
	}
	if q := e.sonarr.query("/api/v3/calendar"); !strings.Contains(q, "includeSeries=true") || !strings.Contains(q, "start=2026-01-01") {
		t.Fatalf("upstream calendar query = %q", q)
	}
	_, body = e.as(e.kid).get(t, "/api/instances/"+e.sonarrID+"/api/v3/history?page=1")
	if !strings.Contains(body, "Soft") || strings.Contains(body, "Hard") {
		t.Fatalf("kid history = %s", body)
	}
	if q := e.sonarr.query("/api/v3/history"); !strings.Contains(q, "includeSeries=true") {
		t.Fatalf("upstream history query = %q", q)
	}
	_, body = e.as(e.kid).get(t, "/api/instances/"+e.sonarrID+"/api/v3/series")
	if titles := titlesOf(t, body); strings.Join(titles, ",") != "Soft" {
		t.Fatalf("kid series = %v", titles)
	}
	if status, _ := e.as(e.kid).get(t, "/api/instances/"+e.sonarrID+"/api/v3/series/1"); status != http.StatusOK {
		t.Fatalf("kid allowed series: %d", status)
	}
	status, body = e.as(e.kid).get(t, "/api/instances/"+e.sonarrID+"/api/v3/series/2")
	if status != http.StatusNotFound || !strings.Contains(body, "not found") || strings.Contains(body, "Hard") {
		t.Fatalf("kid blocked series: %d %s", status, body)
	}
	if status, _ := e.as(e.adult).get(t, "/api/instances/"+e.sonarrID+"/api/v3/series/2"); status != http.StatusOK {
		t.Fatalf("adult blocked series: %d", status)
	}
	_, body = e.as(e.kid).get(t, "/api/instances/"+e.sonarrID+"/api/v3/episode?seriesId=2")
	if body != "[]" {
		t.Fatalf("kid episodes of a TV-MA series = %s, want none", body)
	}
	if q := e.sonarr.query("/api/v3/episode"); !strings.Contains(q, "includeSeries=true") || !strings.Contains(q, "seriesId=2") {
		t.Fatalf("upstream episode query = %q", q)
	}
}

func TestKidsAccountGateFailsClosedAndLeavesTheRestAlone(t *testing.T) {
	e := newKidsProxyEnv(t)
	// A gated read whose body is not JSON cannot be judged: the proxy's
	// opaque 502, not the page.
	e.radarr.raw["/api/v3/movie"] = "<html>library</html>"
	if status, body := e.as(e.kid).get(t, "/api/instances/"+e.radarrID+"/api/v3/movie"); status != http.StatusBadGateway || !strings.Contains(body, "unsafe upstream response") {
		t.Fatalf("kid non-JSON library: %d %s", status, body)
	}
	// An arr error body carries no titles and passes through.
	if status, body := e.as(e.kid).get(t, "/api/instances/"+e.radarrID+"/api/v3/wanted/missing"); status != http.StatusNotFound || !strings.Contains(body, "NotFound") {
		t.Fatalf("kid arr error: %d %s", status, body)
	}
	// Chaptarr carries no ratings and is not gated.
	e.chaptarr.bodies["/api/v1/book/lookup"] = `[{"title":"Dune","foreignBookId":"gr:234"}]`
	if status, body := e.as(e.kid).get(t, "/api/instances/"+e.chaptarrID+"/api/v1/book/lookup?term=dune"); status != http.StatusOK || !strings.Contains(body, "Dune") {
		t.Fatalf("kid book lookup: %d %s", status, body)
	}
	// Secrets are scrubbed before the gate sees a record.
	e.radarr.bodies["/api/v3/history"] = `{"records":[{"id":1,"downloadUrl":"https://indexer.example/get?apikey=SECRET","movie":{"title":"Gentle","certification":"G","genres":[]}}]}`
	if _, body := e.as(e.kid).get(t, "/api/instances/"+e.radarrID+"/api/v3/history"); strings.Contains(body, "SECRET") || !strings.Contains(body, "Gentle") {
		t.Fatalf("kid history = %s", body)
	}
}

func TestResourceOfClassifiesArrPaths(t *testing.T) {
	cases := map[string]struct {
		name   string
		single bool
		ok     bool
	}{
		"/api/v3/movie":           {"movie", false, true},
		"/api/v3/movie/12":        {"movie", true, true},
		"/radarr/api/v3/movie/12": {"movie", true, true},
		"/api/v3/movie/lookup":    {"", false, false},
		"/api/v3/wanted/missing":  {"wanted", false, true},
		"/api/v3/queue":           {"queue", false, true},
		"/api/v3/calendar":        {"calendar", false, true},
		"/api/v3/episode/7":       {"episode", true, true},
		"/api/v3/system/status":   {"", false, false},
		"/api/v1/book/lookup":     {"", false, false},
		"/api/v3/queue/details":   {"", false, false},
	}
	for path, want := range cases {
		got, ok := resourceOf(path)
		if ok != want.ok || got.name != want.name || got.single != want.single {
			t.Fatalf("resourceOf(%q) = %+v, %v; want %+v", path, got, ok, want)
		}
	}
}
