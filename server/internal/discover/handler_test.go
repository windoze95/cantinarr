package discover

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/windoze95/cantinarr-server/internal/cache"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"github.com/windoze95/cantinarr-server/internal/serversettings"
)

const (
	testTMDBToken = "TMDB_TOKEN_SENTINEL"
	testTraktID   = "TRAKT_CLIENT_ID_SENTINEL"
)

// upstreamHit records one intercepted outbound request.
type upstreamHit struct {
	host   string
	path   string
	query  url.Values
	header http.Header
}

// fakeUpstream intercepts ALL outbound HTTP by standing in for
// http.DefaultTransport: the tmdb/trakt clients hold zero-Transport
// http.Clients, which consult http.DefaultTransport on every request, so the
// swap reaches them without touching production code. Responses are
// synthesized in-memory — no sockets, no TLS, no network.
type fakeUpstream struct {
	mu      sync.Mutex
	hits    []upstreamHit
	respond func(req *http.Request) (int, string)
}

func (f *fakeUpstream) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.hits = append(f.hits, upstreamHit{
		host:   req.URL.Host,
		path:   req.URL.Path,
		query:  req.URL.Query(),
		header: req.Header.Clone(),
	})
	respond := f.respond
	f.mu.Unlock()

	status, body := respond(req)
	return &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}, nil
}

func (f *fakeUpstream) setRespond(respond func(req *http.Request) (int, string)) {
	f.mu.Lock()
	f.respond = respond
	f.mu.Unlock()
}

func (f *fakeUpstream) hitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.hits)
}

func (f *fakeUpstream) hit(t *testing.T, i int) upstreamHit {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.hits) {
		t.Fatalf("upstream hit %d requested, only %d recorded", i, len(f.hits))
	}
	return f.hits[i]
}

// echoUpstream answers 200 with a body derived from the full upstream URL, so
// every distinct upstream request produces a distinct payload — which makes
// cache collisions observable as one request receiving another's body.
func echoUpstream(req *http.Request) (int, string) {
	return http.StatusOK, fmt.Sprintf(`{"echo":%q}`, req.URL.String())
}

// --- test environment ---

// stubPrefs is a settable DiscoveryPrefs, so a test can choose the row source
// and the language filter without going through the settings table.
type stubPrefs struct {
	mu       sync.Mutex
	settings serversettings.Settings
}

func (s *stubPrefs) Get() serversettings.Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

func (s *stubPrefs) set(source string, englishOnly bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = serversettings.Settings{
		DiscoverySource:      source,
		DiscoveryEnglishOnly: englishOnly,
	}
}

type env struct {
	upstream *fakeUpstream
	prefs    *stubPrefs
	creds    *credentials.Registry
	router   chi.Router
}

// setTMDBOnly configures TMDB and deliberately leaves Trakt unset, which is the
// shape a server has when an admin picks Trakt without a client ID.
func (e *env) setTMDBOnly(t *testing.T) {
	t.Helper()
	if err := e.creds.SetCredential(credentials.KeyTMDBAccessToken, testTMDBToken); err != nil {
		t.Fatalf("set TMDB credential: %v", err)
	}
}

// newEnv wires the handler over a real credentials registry (in-memory DB,
// credentials encrypted at rest) and a real TTL cache, and mounts the same
// route patterns the API router uses. Auth middleware is deliberately absent:
// authorization for these routes is covered by the api package's RBAC matrix
// tests.
func newEnv(t *testing.T, configured bool) *env {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	creds := credentials.NewRegistry(database, cipher)
	if configured {
		if err := creds.SetCredential(credentials.KeyTMDBAccessToken, testTMDBToken); err != nil {
			t.Fatalf("set TMDB credential: %v", err)
		}
		if err := creds.SetCredential(credentials.KeyTraktClientID, testTraktID); err != nil {
			t.Fatalf("set Trakt credential: %v", err)
		}
	}
	responseCache := cache.New()
	t.Cleanup(responseCache.Close)
	prefs := &stubPrefs{}
	prefs.set(serversettings.DefaultDiscoverySource, false)
	handler := NewHandler(creds, responseCache, prefs)

	router := chi.NewRouter()
	router.Get("/discover/trending", handler.Trending)
	router.Get("/discover/movies", handler.DiscoverMovies)
	router.Get("/discover/movies/upcoming", handler.UpcomingMovies)
	router.Get("/discover/tv", handler.DiscoverTV)
	router.Get("/discover/tv/popular", handler.PopularTV)
	router.Get("/discover/tv/on-the-air", handler.OnTheAirTV)
	router.Get("/discover/tv/top-rated", handler.TopRatedTV)
	router.Get("/discover/tv/upcoming", handler.UpcomingTV)
	router.Get("/discover/tv/featured", handler.FeaturedTV)
	router.Get("/discover/movies/featured", handler.FeaturedMovies)
	router.Get("/search", handler.Search)
	router.Get("/search/keyword", handler.SearchKeywords)
	router.Get("/search/company", handler.SearchCompanies)
	router.Get("/languages", handler.Languages)
	router.Get("/providers/movie", handler.MovieWatchProviders)
	router.Get("/providers/tv", handler.TVWatchProviders)
	router.Get("/providers/regions", handler.WatchProviderRegions)
	router.Get("/media/movie/{id}", handler.MovieDetail)
	router.Get("/media/tv/{id}", handler.TVDetail)
	router.Get("/trakt/trending", handler.TraktTrending)
	router.Get("/trakt/images/{host}/*", handler.TraktImage)

	upstream := &fakeUpstream{respond: echoUpstream}
	previous := http.DefaultTransport
	http.DefaultTransport = upstream
	t.Cleanup(func() { http.DefaultTransport = previous })

	return &env{upstream: upstream, prefs: prefs, creds: creds, router: router}
}

func (e *env) do(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *env) doOK(t *testing.T, path string) string {
	t.Helper()
	rec := e.do(t, path)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, body = %s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// --- tests ---

// TestTrendingPassesParamsAndAuthToUpstream pins the passthrough contract:
// requester parameters reach TMDB on the right path with the stored bearer
// token, and the raw upstream body comes back verbatim as JSON.
func TestTrendingPassesParamsAndAuthToUpstream(t *testing.T) {
	e := newEnv(t, true)
	e.upstream.setRespond(func(*http.Request) (int, string) { return http.StatusOK, `{"results":["heat"]}` })

	rec := e.do(t, "/discover/trending?time_window=week&page=2")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != `{"results":["heat"]}` {
		t.Errorf("body = %s, want the upstream payload verbatim", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content-type = %q, want application/json", got)
	}

	hit := e.upstream.hit(t, 0)
	if hit.host != "api.themoviedb.org" {
		t.Errorf("upstream host = %s, want api.themoviedb.org", hit.host)
	}
	if hit.path != "/3/trending/all/week" {
		t.Errorf("upstream path = %s, want /3/trending/all/week", hit.path)
	}
	if hit.query.Get("page") != "2" || hit.query.Get("language") != "en-US" {
		t.Errorf("upstream query = %v, want page=2 language=en-US", hit.query)
	}
	if got := hit.header.Get("Authorization"); got != "Bearer "+testTMDBToken {
		t.Errorf("Authorization = %q, want the stored bearer token", got)
	}
}

func TestTrendingDefaultsToDayPageOne(t *testing.T) {
	e := newEnv(t, true)
	e.doOK(t, "/discover/trending")
	hit := e.upstream.hit(t, 0)
	if hit.path != "/3/trending/all/day" || hit.query.Get("page") != "1" {
		t.Errorf("upstream = %s?%v, want /3/trending/all/day with page=1", hit.path, hit.query)
	}
}

// TestMovieDetailAppendsReleaseDatesAlongsideVideos pins the release-dates
// wire change: the movie detail proxy must keep requesting videos while also
// requesting release_dates, so the app can render the Release dates section
// for any TMDB movie. Asserts membership, not exact string equality, so a
// later third append does not break this test.
func TestMovieDetailAppendsReleaseDatesAlongsideVideos(t *testing.T) {
	e := newEnv(t, true)
	e.doOK(t, "/media/movie/603")
	hit := e.upstream.hit(t, 0)
	if hit.path != "/3/movie/603" {
		t.Errorf("upstream path = %s, want /3/movie/603", hit.path)
	}
	appends := strings.Split(hit.query.Get("append_to_response"), ",")
	if !slices.Contains(appends, "videos") {
		t.Errorf("append_to_response = %q, want it to contain videos", hit.query.Get("append_to_response"))
	}
	if !slices.Contains(appends, "release_dates") {
		t.Errorf("append_to_response = %q, want it to contain release_dates", hit.query.Get("append_to_response"))
	}
}

// TestDetailBodiesAppendCredits pins the credits append on both detail
// proxies: the title page's Cast row and Details lines read `credits` off
// the same body as everything else, so neither type may drop its earlier
// appends to gain it. Membership, not string equality, as above.
func TestDetailBodiesAppendCredits(t *testing.T) {
	cases := []struct {
		name  string
		route string
		path  string
		keeps []string
	}{
		{"movie", "/media/movie/603", "/3/movie/603", []string{"videos", "release_dates"}},
		{"tv", "/media/tv/603", "/3/tv/603", []string{"videos", "external_ids"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, true)
			e.doOK(t, tc.route)
			hit := e.upstream.hit(t, 0)
			if hit.path != tc.path {
				t.Errorf("upstream path = %s, want %s", hit.path, tc.path)
			}
			appends := strings.Split(hit.query.Get("append_to_response"), ",")
			for _, want := range append(tc.keeps, "credits") {
				if !slices.Contains(appends, want) {
					t.Errorf("append_to_response = %q, want it to contain %s", hit.query.Get("append_to_response"), want)
				}
			}
		})
	}
}

// TestRepeatQueryIsServedFromCache pins the TTL cache: an identical query
// within the TTL never reaches the upstream again, while a different page
// does.
func TestRepeatQueryIsServedFromCache(t *testing.T) {
	e := newEnv(t, true)

	first := e.doOK(t, "/discover/trending?page=1")
	if e.upstream.hitCount() != 1 {
		t.Fatalf("upstream hits = %d, want 1", e.upstream.hitCount())
	}
	second := e.doOK(t, "/discover/trending?page=1")
	if e.upstream.hitCount() != 1 {
		t.Errorf("upstream hits after repeat = %d, want 1 (served from cache)", e.upstream.hitCount())
	}
	if first != second {
		t.Errorf("cached body diverged: %s vs %s", first, second)
	}

	e.doOK(t, "/discover/trending?page=2")
	if e.upstream.hitCount() != 2 {
		t.Errorf("upstream hits for a new page = %d, want 2", e.upstream.hitCount())
	}
}

// TestAdjacentQueriesNeverShareCacheEntries crafts request pairs whose cache
// keys would collide under naive string concatenation and proves each keeps
// its own entry: every request is first served by the upstream (with its own
// echo body) and every repeat returns its own body from cache.
func TestAdjacentQueriesNeverShareCacheEntries(t *testing.T) {
	e := newEnv(t, true)

	pairs := [][2]string{
		// "search:" + query + ":" + page — under separator-less
		// concatenation both would key as "searchheat12".
		{"/search?query=heat1&page=2", "/search?query=heat&page=12"},
		// "movie:" + id vs "tv:" + id — same numeric id, different media type.
		{"/media/movie/603", "/media/tv/603"},
		// disc_movies uses params.Encode(): a with_genres VALUE containing a
		// literal "&page=2" must not collide with a genuine page=2 request.
		{"/discover/movies?with_genres=28&page=2", "/discover/movies?with_genres=28%26page%3D2"},
		// The headline feed pages: page 1 (the row) and page 2 (its grid)
		// are different entries of the same source.
		{"/discover/movies/featured?page=1", "/discover/movies/featured?page=2"},
		// The type-ahead lookups share a query with multi-search and with each
		// other, and the provider lists share a region across media types.
		{"/search?query=heat", "/search/keyword?query=heat"},
		{"/search/keyword?query=heist", "/search/company?query=heist"},
		{"/providers/movie?region=GB", "/providers/tv?region=GB"},
	}

	expectedHits := 0
	for _, pair := range pairs {
		bodyA := e.doOK(t, pair[0])
		bodyB := e.doOK(t, pair[1])
		expectedHits += 2
		if e.upstream.hitCount() != expectedHits {
			t.Fatalf("upstream hits = %d, want %d (each of %q and %q must fetch separately)",
				e.upstream.hitCount(), expectedHits, pair[0], pair[1])
		}
		if bodyA == bodyB {
			t.Errorf("%q and %q returned the same body %s — cache keys collided", pair[0], pair[1], bodyA)
		}
		// Repeats are cache hits and each returns its own payload.
		if again := e.doOK(t, pair[0]); again != bodyA {
			t.Errorf("repeat of %q = %s, want its own cached body %s", pair[0], again, bodyA)
		}
		if again := e.doOK(t, pair[1]); again != bodyB {
			t.Errorf("repeat of %q = %s, want its own cached body %s", pair[1], again, bodyB)
		}
		if e.upstream.hitCount() != expectedHits {
			t.Errorf("upstream hits after repeats = %d, want %d", e.upstream.hitCount(), expectedHits)
		}
	}

	// The media-type keys must also have fetched from distinct TMDB paths.
	if p := e.upstream.hit(t, 2).path; p != "/3/movie/603" {
		t.Errorf("movie detail upstream path = %s, want /3/movie/603", p)
	}
	if p := e.upstream.hit(t, 3).path; p != "/3/tv/603" {
		t.Errorf("tv detail upstream path = %s, want /3/tv/603", p)
	}
	// The smuggled with_genres value must arrive as data, not as a page param.
	smuggled := e.upstream.hit(t, 5)
	if smuggled.query.Get("with_genres") != "28&page=2" || smuggled.query.Get("page") != "1" {
		t.Errorf("upstream query = %v, want with_genres=%q page=1", smuggled.query, "28&page=2")
	}
}

// TestUpstreamErrorsAreNotCached pins the failure contract: a TMDB error maps
// to 502 without leaking the token, is NOT written to the cache (the next
// request retries the upstream), and the first success after recovery is
// cached as usual.
func TestUpstreamErrorsAreNotCached(t *testing.T) {
	e := newEnv(t, true)
	e.upstream.setRespond(func(*http.Request) (int, string) { return http.StatusInternalServerError, `{"status_message":"boom"}` })

	rec := e.do(t, "/discover/trending")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "TMDB API returned status 500") {
		t.Errorf("body = %s, want the upstream status surfaced", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), testTMDBToken) {
		t.Fatalf("error body leaked the TMDB token: %s", rec.Body.String())
	}

	// Upstream recovers: the same query must retry (error was not cached).
	e.upstream.setRespond(func(*http.Request) (int, string) { return http.StatusOK, `{"results":["fresh"]}` })
	if body := e.doOK(t, "/discover/trending"); body != `{"results":["fresh"]}` {
		t.Errorf("body after recovery = %s, want the fresh upstream payload", body)
	}
	if e.upstream.hitCount() != 2 {
		t.Errorf("upstream hits = %d, want 2 (the 502 must not have been cached)", e.upstream.hitCount())
	}

	// The success IS cached.
	e.doOK(t, "/discover/trending")
	if e.upstream.hitCount() != 2 {
		t.Errorf("upstream hits after repeat = %d, want 2 (success cached)", e.upstream.hitCount())
	}
}

// TestTraktPassthroughCacheAndAuth mirrors the TMDB contract for Trakt:
// params and API-key headers reach api.trakt.tv, bodies pass through, repeats
// hit the cache, and upstream errors map to 502 without caching or leakage.
func TestTraktPassthroughCacheAndAuth(t *testing.T) {
	e := newEnv(t, true)

	body := e.doOK(t, "/trakt/trending?type=shows&page=3")
	hit := e.upstream.hit(t, 0)
	if hit.host != "api.trakt.tv" || hit.path != "/shows/trending" {
		t.Errorf("upstream = %s%s, want api.trakt.tv/shows/trending", hit.host, hit.path)
	}
	if hit.query.Get("page") != "3" || hit.query.Get("limit") != "20" || hit.query.Get("extended") != "full" {
		t.Errorf("upstream query = %v, want page=3 limit=20 extended=full", hit.query)
	}
	if hit.header.Get("trakt-api-key") != testTraktID || hit.header.Get("trakt-api-version") != "2" {
		t.Errorf("trakt headers = %v, want the stored client id and API version 2", hit.header)
	}

	if again := e.doOK(t, "/trakt/trending?type=shows&page=3"); again != body || e.upstream.hitCount() != 1 {
		t.Errorf("repeat = %s (hits=%d), want the cached body with no new upstream hit", again, e.upstream.hitCount())
	}
	// A different type is a different cache entry.
	e.doOK(t, "/trakt/trending?type=movies&page=3")
	if e.upstream.hitCount() != 2 {
		t.Errorf("upstream hits = %d, want 2 for a different type", e.upstream.hitCount())
	}

	// Errors: 502, no client-id leakage, not cached.
	e.upstream.setRespond(func(*http.Request) (int, string) { return http.StatusServiceUnavailable, `oops` })
	rec := e.do(t, "/trakt/trending?type=movies&page=9")
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "trakt API returned status 503") {
		t.Errorf("status = %d body = %s, want 502 with the trakt status", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), testTraktID) {
		t.Fatalf("error body leaked the Trakt client id: %s", rec.Body.String())
	}
	e.upstream.setRespond(echoUpstream)
	e.doOK(t, "/trakt/trending?type=movies&page=9")
	if e.upstream.hitCount() != 4 {
		t.Errorf("upstream hits = %d, want 4 (the trakt error must not have been cached)", e.upstream.hitCount())
	}
}

// TestUnconfiguredCredentialsReturn503 pins the not-configured contract: the
// handler answers 503 itself and never dials out.
func TestUnconfiguredCredentialsReturn503(t *testing.T) {
	e := newEnv(t, false)

	for _, path := range []string{"/discover/trending", "/languages", "/search/keyword?query=x"} {
		rec := e.do(t, path)
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "TMDB is not configured") {
			t.Errorf("GET %s status = %d body = %s, want 503 not-configured", path, rec.Code, rec.Body.String())
		}
	}
	rec := e.do(t, "/discover/trending")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("TMDB status = %d, want 503 not-configured", rec.Code)
	}
	rec = e.do(t, "/trakt/trending")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "trakt not configured") {
		t.Errorf("Trakt status = %d body = %s, want 503 not-configured", rec.Code, rec.Body.String())
	}
	if e.upstream.hitCount() != 0 {
		t.Fatalf("upstream hits = %d, want 0 without credentials", e.upstream.hitCount())
	}
}

// TestUpcomingMoviesQueriesFutureWindow pins the Coming Soon contract: the row
// is a discover query on the primary release date from today through three
// months out, never TMDB's /movie/upcoming — whose any-country theatrical
// matching kept surfacing movies already out (and decades-old re-releases).
func TestUpcomingMoviesQueriesFutureWindow(t *testing.T) {
	e := newEnv(t, true)

	// Capture the window on both sides of the request so a midnight rollover
	// mid-test cannot flake the date assertions.
	windowAt := func(now time.Time) [2]string {
		return [2]string{now.Format("2006-01-02"), now.AddDate(0, 3, 0).Format("2006-01-02")}
	}
	before := windowAt(time.Now())
	e.doOK(t, "/discover/movies/upcoming?page=3")
	after := windowAt(time.Now())

	hit := e.upstream.hit(t, 0)
	if hit.path != "/3/discover/movie" {
		t.Errorf("upstream path = %s, want /3/discover/movie", hit.path)
	}
	if got := hit.query.Get("page"); got != "3" {
		t.Errorf("upstream page = %q, want 3", got)
	}
	if got := hit.query.Get("sort_by"); got != "popularity.desc" {
		t.Errorf("upstream sort_by = %q, want popularity.desc", got)
	}
	window := [2]string{hit.query.Get("primary_release_date.gte"), hit.query.Get("primary_release_date.lte")}
	if window != before && window != after {
		t.Errorf("upstream release window = %v, want %v (today through three months out)", window, before)
	}

	// Repeats within the TTL are cache hits.
	e.doOK(t, "/discover/movies/upcoming?page=3")
	if e.upstream.hitCount() != 1 {
		t.Errorf("upstream hits after repeat = %d, want 1 (served from cache)", e.upstream.hitCount())
	}
}

func TestSearchRequiresQueryParameter(t *testing.T) {
	e := newEnv(t, true)
	rec := e.do(t, "/search")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "query parameter required") {
		t.Errorf("status = %d body = %s, want 400 query-required", rec.Code, rec.Body.String())
	}
	if e.upstream.hitCount() != 0 {
		t.Fatalf("upstream hits = %d, want 0 for a rejected request", e.upstream.hitCount())
	}
}
