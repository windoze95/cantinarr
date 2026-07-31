package tmdb

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/trakt"
)

// fakeServiceClients satisfies ServiceClients with fixed clients.
type fakeServiceClients struct {
	tmdb  *Client
	trakt *trakt.Client
}

func (f fakeServiceClients) TMDB() *Client        { return f.tmdb }
func (f fakeServiceClients) Trakt() *trakt.Client { return f.trakt }

func newBridgeDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// fakeTraktUpstream reroutes requests for api.trakt.tv to a local fake server.
// trakt.Client hardcodes its base URL (only trakt's own tests can repoint it),
// so the bridge's Trakt-fallback leg is faked at the transport layer: the
// trakt client uses http.DefaultTransport, which the returned cleanup-scoped
// swap intercepts. No request ever leaves the test process.
func fakeTraktUpstream(t *testing.T, handler http.Handler) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse fake trakt url: %v", err)
	}
	original := http.DefaultTransport
	http.DefaultTransport = rerouteTransport{host: target.Host, next: original}
	t.Cleanup(func() { http.DefaultTransport = original })
}

type rerouteTransport struct {
	host string
	next http.RoundTripper
}

func (rt rerouteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "api.trakt.tv" {
		return rt.next.RoundTrip(req)
	}
	clone := req.Clone(req.Context())
	clone.URL.Scheme = "http"
	clone.URL.Host = rt.host
	return rt.next.RoundTrip(clone)
}

func TestResolveTVDBIDCacheHitSkipsHTTP(t *testing.T) {
	database := newBridgeDB(t)
	if _, err := database.Exec(
		"INSERT INTO tmdb_tvdb_cache (tmdb_id, tvdb_id, imdb_id, cached_at) VALUES (?, ?, ?, ?)",
		1399, 121361, "tt0944947", time.Now(),
	); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	var requests atomic.Int32
	tmdbClient := testHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "cache hit must not fetch", http.StatusInternalServerError)
	}))
	bridge := NewBridge(fakeServiceClients{tmdb: tmdbClient}, database)

	got, err := bridge.ResolveTVDBID(1399)
	if err != nil {
		t.Fatalf("ResolveTVDBID: %v", err)
	}
	if got.TVDBID != 121361 || got.IMDBID != "tt0944947" {
		t.Fatalf("result = %+v", got)
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("cache hit made %d HTTP requests, want 0", n)
	}
}

func TestResolveTVDBIDCacheMissResolvesViaTMDBAndCaches(t *testing.T) {
	database := newBridgeDB(t)
	tmdbClient := testHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tv/1399/external_ids" {
			t.Errorf("path = %q, want /tv/1399/external_ids", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":1399,"imdb_id":"tt0944947","tvdb_id":121361}`))
	}))
	bridge := NewBridge(fakeServiceClients{tmdb: tmdbClient}, database)

	got, err := bridge.ResolveTVDBID(1399)
	if err != nil {
		t.Fatalf("ResolveTVDBID: %v", err)
	}
	if got.TVDBID != 121361 || got.IMDBID != "tt0944947" {
		t.Fatalf("result = %+v", got)
	}

	var cachedTVDB int
	var cachedIMDB string
	if err := database.QueryRow(
		"SELECT tvdb_id, imdb_id FROM tmdb_tvdb_cache WHERE tmdb_id = 1399",
	).Scan(&cachedTVDB, &cachedIMDB); err != nil {
		t.Fatalf("read cache row: %v", err)
	}
	if cachedTVDB != 121361 || cachedIMDB != "tt0944947" {
		t.Fatalf("cache row = (%d, %q), want (121361, tt0944947)", cachedTVDB, cachedIMDB)
	}
}

func TestResolveTVDBIDExpiredCacheReResolves(t *testing.T) {
	database := newBridgeDB(t)
	if _, err := database.Exec(
		"INSERT INTO tmdb_tvdb_cache (tmdb_id, tvdb_id, imdb_id, cached_at) VALUES (?, ?, ?, ?)",
		1399, 111, "tt-stale", time.Now().Add(-31*24*time.Hour),
	); err != nil {
		t.Fatalf("seed stale cache: %v", err)
	}
	tmdbClient := testHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":1399,"imdb_id":"tt0944947","tvdb_id":121361}`))
	}))
	bridge := NewBridge(fakeServiceClients{tmdb: tmdbClient}, database)

	got, err := bridge.ResolveTVDBID(1399)
	if err != nil {
		t.Fatalf("ResolveTVDBID: %v", err)
	}
	if got.TVDBID != 121361 {
		t.Fatalf("tvdb id = %d, want the freshly resolved 121361, not the stale 111", got.TVDBID)
	}

	var cachedTVDB int
	if err := database.QueryRow(
		"SELECT tvdb_id FROM tmdb_tvdb_cache WHERE tmdb_id = 1399",
	).Scan(&cachedTVDB); err != nil {
		t.Fatalf("read cache row: %v", err)
	}
	if cachedTVDB != 121361 {
		t.Fatalf("cache row tvdb = %d, want refreshed 121361", cachedTVDB)
	}
}

func TestResolveTVDBIDFallsBackToTraktAndCaches(t *testing.T) {
	database := newBridgeDB(t)
	// TMDB answers but has no TVDB mapping, forcing the Trakt fallback.
	tmdbClient := testHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":1399,"imdb_id":null,"tvdb_id":null}`))
	}))
	fakeTraktUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/tmdb/1399" {
			t.Errorf("path = %q, want /search/tmdb/1399", r.URL.Path)
		}
		if got := r.URL.Query().Get("type"); got != "show" {
			t.Errorf("type = %q, want show", got)
		}
		_, _ = w.Write([]byte(`[{"show":{"ids":{"tvdb":121361,"imdb":"tt0944947"}}}]`))
	}))
	bridge := NewBridge(fakeServiceClients{tmdb: tmdbClient, trakt: trakt.NewClient("trakt-client-id")}, database)

	got, err := bridge.ResolveTVDBID(1399)
	if err != nil {
		t.Fatalf("ResolveTVDBID: %v", err)
	}
	if got.TVDBID != 121361 || got.IMDBID != "tt0944947" {
		t.Fatalf("result = %+v", got)
	}

	var cachedTVDB int
	if err := database.QueryRow(
		"SELECT tvdb_id FROM tmdb_tvdb_cache WHERE tmdb_id = 1399",
	).Scan(&cachedTVDB); err != nil {
		t.Fatalf("read cache row: %v", err)
	}
	if cachedTVDB != 121361 {
		t.Fatalf("cache row tvdb = %d, want the trakt-resolved 121361", cachedTVDB)
	}
}

func TestResolveTVDBIDEverythingFails(t *testing.T) {
	database := newBridgeDB(t)
	tmdbClient := testHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	fakeTraktUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`)) // no results
	}))
	bridge := NewBridge(fakeServiceClients{tmdb: tmdbClient, trakt: trakt.NewClient("trakt-client-id")}, database)

	_, err := bridge.ResolveTVDBID(1399)
	if err == nil {
		t.Fatal("ResolveTVDBID succeeded with every upstream failing")
	}
	if !strings.Contains(err.Error(), "could not resolve TVDB ID for TMDB ID 1399") {
		t.Fatalf("error = %v, want the unresolved-id message", err)
	}

	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM tmdb_tvdb_cache").Scan(&count); err != nil {
		t.Fatalf("count cache rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("failed resolution wrote %d cache rows, want 0", count)
	}
}

func TestResolveTVDBIDWithoutTMDBClient(t *testing.T) {
	database := newBridgeDB(t)
	bridge := NewBridge(fakeServiceClients{}, database)

	_, err := bridge.ResolveTVDBID(1399)
	if err == nil {
		t.Fatal("ResolveTVDBID succeeded without a TMDB client")
	}
	if !strings.Contains(err.Error(), "TMDB client not configured") {
		t.Fatalf("error = %v, want the not-configured message", err)
	}
}
