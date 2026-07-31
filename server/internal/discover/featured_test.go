package discover

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/serversettings"
)

// decodeFeatured parses a headline-row response into the source plus the
// entries as generic maps, which is what the client sees.
func decodeFeatured(t *testing.T, body string) (string, []map[string]any) {
	t.Helper()
	var page struct {
		Source  string           `json:"source"`
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatalf("decode featured page: %v (body = %s)", err, body)
	}
	return page.Source, page.Results
}

// tmdbListPage renders a TMDB list payload of n entries whose original
// languages cycle through the given codes.
func tmdbListPage(prefix string, n int, languages ...string) string {
	entries := make([]string, 0, n)
	for i := 0; i < n; i++ {
		lang := languages[i%len(languages)]
		entries = append(entries, fmt.Sprintf(
			`{"id":%d,"name":%q,"original_language":%q}`,
			i+1, fmt.Sprintf("%s-%d", prefix, i+1), lang))
	}
	return fmt.Sprintf(`{"page":1,"total_pages":9,"total_results":180,"results":[%s]}`,
		strings.Join(entries, ","))
}

// TestFeaturedFallsBackWhenTraktIsDown covers the outage case: the credential
// is present and valid-looking, so nothing upstream of here can tell that
// Trakt has stopped answering. The landing screen must not go with it.
func TestFeaturedFallsBackWhenTraktIsDown(t *testing.T) {
	e := newEnv(t, true)
	e.prefs.set(serversettings.DiscoverySourceTraktTrending, false)
	e.upstream.setRespond(func(req *http.Request) (int, string) {
		if strings.HasPrefix(req.URL.Path, "/shows/") {
			return http.StatusInternalServerError, `{"error":"trakt is having a day"}`
		}
		return http.StatusOK, tmdbListPage("show", 25, "en")
	})

	source, results := decodeFeatured(t, e.doOK(t, "/discover/tv/featured"))
	// The envelope names the feed that actually answered, which is what makes
	// the client retitle the row instead of lying about where this came from.
	if source != serversettings.DiscoverySourceTMDBTrending {
		t.Errorf("source = %q, want the fallback %q", source, serversettings.DiscoverySourceTMDBTrending)
	}
	if len(results) != featuredLimit {
		t.Errorf("results = %d, want a full row of %d", len(results), featuredLimit)
	}
	if got := e.upstream.hit(t, 1).path; got != "/3/trending/tv/week" {
		t.Errorf("second upstream path = %q, want TMDB weekly trending", got)
	}
}

// TestFeaturedOutageFallbackIsCachedAgainstTheChosenSource keeps a Trakt
// outage from turning every request into another failed upstream call.
func TestFeaturedOutageFallbackIsCachedAgainstTheChosenSource(t *testing.T) {
	e := newEnv(t, true)
	e.prefs.set(serversettings.DiscoverySourceTraktTrending, false)
	e.upstream.setRespond(func(req *http.Request) (int, string) {
		if strings.HasPrefix(req.URL.Path, "/shows/") {
			return http.StatusInternalServerError, `{"error":"trakt is having a day"}`
		}
		return http.StatusOK, tmdbListPage("show", 25, "en")
	})

	e.doOK(t, "/discover/tv/featured")
	first := e.upstream.hitCount()
	e.doOK(t, "/discover/tv/featured")
	if got := e.upstream.hitCount(); got != first {
		t.Errorf("upstream calls = %d after a second request, want the fallback served from cache at %d", got, first)
	}
}

// TestFeaturedTVDefaultsToTMDBWeeklyTrending pins the fix for the headline row:
// with no admin choice stored it reads TMDB's weekly trending feed, not the
// lifetime popularity ranking that fills up with decade-old catalogue shows.
func TestFeaturedTVDefaultsToTMDBWeeklyTrending(t *testing.T) {
	e := newEnv(t, true)
	e.upstream.setRespond(func(*http.Request) (int, string) {
		return http.StatusOK, tmdbListPage("show", 25, "en")
	})

	source, results := decodeFeatured(t, e.doOK(t, "/discover/tv/featured"))
	if source != serversettings.DiscoverySourceTMDBTrending {
		t.Errorf("source = %q, want %q", source, serversettings.DiscoverySourceTMDBTrending)
	}
	if len(results) != featuredLimit {
		t.Errorf("results = %d, want %d", len(results), featuredLimit)
	}
	if got := e.upstream.hitCount(); got != 1 {
		t.Errorf("upstream calls = %d, want 1 when nothing is filtered", got)
	}
	if path := e.upstream.hit(t, 0).path; path != "/3/trending/tv/week" {
		t.Errorf("upstream path = %s, want /3/trending/tv/week", path)
	}
}

// TestFeaturedMoviesFollowsTheConfiguredSource proves the admin's choice is
// what selects the upstream feed, for movies as well as TV.
func TestFeaturedMoviesFollowsTheConfiguredSource(t *testing.T) {
	e := newEnv(t, true)
	e.prefs.set(serversettings.DiscoverySourceTMDBPopular, false)
	e.upstream.setRespond(func(*http.Request) (int, string) {
		return http.StatusOK, tmdbListPage("movie", 5, "en")
	})

	source, results := decodeFeatured(t, e.doOK(t, "/discover/movies/featured"))
	if source != serversettings.DiscoverySourceTMDBPopular {
		t.Errorf("source = %q, want %q", source, serversettings.DiscoverySourceTMDBPopular)
	}
	if len(results) != 5 {
		t.Errorf("results = %d, want 5", len(results))
	}
	if path := e.upstream.hit(t, 0).path; path != "/3/movie/popular" {
		t.Errorf("upstream path = %s, want /3/movie/popular", path)
	}
}

// TestFeaturedTraktNormalizesToTheTMDBShape covers the whole Trakt path: the
// row asks Trakt, and every entry comes back in the shape the client's TMDB
// parser reads, with the TMDB id it needs to open a detail page.
func TestFeaturedTraktNormalizesToTheTMDBShape(t *testing.T) {
	e := newEnv(t, true)
	e.prefs.set(serversettings.DiscoverySourceTraktTrending, false)
	e.upstream.setRespond(func(*http.Request) (int, string) {
		return http.StatusOK, `[
			{"watchers":900,"show":{"title":"Severance","year":2022,"overview":"Work self.",
			 "rating":8.6,"language":"en","first_aired":"2022-02-18T09:00:00.000Z",
			 "ids":{"trakt":1,"tmdb":95396},
			 "images":{"poster":["walter-r2.trakt.tv/poster.jpg"],"fanart":["walter-r2.trakt.tv/fanart.jpg"]}}},
			{"watchers":800,"show":{"title":"No TMDB Id","year":2021,"language":"en",
			 "ids":{"trakt":2,"tmdb":0}}}
		]`
	})

	source, results := decodeFeatured(t, e.doOK(t, "/discover/tv/featured"))
	if source != serversettings.DiscoverySourceTraktTrending {
		t.Errorf("source = %q, want %q", source, serversettings.DiscoverySourceTraktTrending)
	}
	// The second entry has no TMDB id: nothing to open, nothing to request.
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1 (the entry without a TMDB id is dropped)", len(results))
	}

	item := results[0]
	if got := item["id"]; got != float64(95396) {
		t.Errorf("id = %v, want 95396", got)
	}
	if got := item["name"]; got != "Severance" {
		t.Errorf("name = %v, want Severance (TV entries use `name`)", got)
	}
	if _, ok := item["title"]; ok {
		t.Error("TV entry carries `title`, want only `name`")
	}
	if got := item["first_air_date"]; got != "2022-02-18" {
		t.Errorf("first_air_date = %v, want the date trimmed to 2022-02-18", got)
	}
	if got := item["poster_path"]; got != "https://walter-r2.trakt.tv/poster.jpg" {
		t.Errorf("poster_path = %v, want an absolute https URL", got)
	}
	if got := item["backdrop_path"]; got != "https://walter-r2.trakt.tv/fanart.jpg" {
		t.Errorf("backdrop_path = %v, want an absolute https URL", got)
	}

	hit := e.upstream.hit(t, 0)
	if hit.host != "api.trakt.tv" || hit.path != "/shows/trending" {
		t.Errorf("upstream = %s%s, want api.trakt.tv/shows/trending", hit.host, hit.path)
	}
	if hit.query.Get("extended") != "full" {
		t.Errorf("extended = %q, want full (images and language come from it)", hit.query.Get("extended"))
	}
}

// TestFeaturedTraktMoviesUseMovieFields pins the media-type split: a movie row
// must carry `title`/`release_date`, not the TV field names.
func TestFeaturedTraktMoviesUseMovieFields(t *testing.T) {
	e := newEnv(t, true)
	e.prefs.set(serversettings.DiscoverySourceTraktTrending, false)
	e.upstream.setRespond(func(*http.Request) (int, string) {
		return http.StatusOK, `[
			{"watchers":50,"movie":{"title":"Sinners","year":2025,"language":"en",
			 "released":"2025-04-18","ids":{"tmdb":1233413}}}
		]`
	})

	_, results := decodeFeatured(t, e.doOK(t, "/discover/movies/featured"))
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if got := results[0]["title"]; got != "Sinners" {
		t.Errorf("title = %v, want Sinners", got)
	}
	if got := results[0]["release_date"]; got != "2025-04-18" {
		t.Errorf("release_date = %v, want 2025-04-18", got)
	}
	if path := e.upstream.hit(t, 0).path; path != "/movies/trending" {
		t.Errorf("upstream path = %s, want /movies/trending", path)
	}
}

// TestFeaturedFallsBackWhenTraktIsUnconfigured keeps the landing screen
// populated when the chosen source has no credential, and reports the source
// that actually answered rather than the one that was asked for.
func TestFeaturedFallsBackWhenTraktIsUnconfigured(t *testing.T) {
	e := newEnv(t, false)
	e.setTMDBOnly(t)
	e.prefs.set(serversettings.DiscoverySourceTraktTrending, false)
	e.upstream.setRespond(func(*http.Request) (int, string) {
		return http.StatusOK, tmdbListPage("show", 3, "en")
	})

	source, results := decodeFeatured(t, e.doOK(t, "/discover/tv/featured"))
	if source != serversettings.DiscoverySourceTMDBTrending {
		t.Errorf("source = %q, want the fallback %q", source, serversettings.DiscoverySourceTMDBTrending)
	}
	if len(results) != 3 {
		t.Errorf("results = %d, want 3", len(results))
	}
	if path := e.upstream.hit(t, 0).path; path != "/3/trending/tv/week" {
		t.Errorf("upstream path = %s, want the TMDB fallback feed", path)
	}
}

// TestFeaturedEnglishOnlyFiltersAndBackfills proves the language filter drops
// non-English originals and pages further to refill the row, while stopping at
// maxFeaturedPages instead of walking the feed indefinitely.
func TestFeaturedEnglishOnlyFiltersAndBackfills(t *testing.T) {
	e := newEnv(t, true)
	e.prefs.set(serversettings.DiscoverySourceTMDBTrending, true)
	// Every page is 10 entries, 5 English — never enough to fill a 20-slot row.
	e.upstream.setRespond(func(*http.Request) (int, string) {
		return http.StatusOK, tmdbListPage("show", 10, "en", "es")
	})

	_, results := decodeFeatured(t, e.doOK(t, "/discover/tv/featured"))
	if got := e.upstream.hitCount(); got != maxFeaturedPages {
		t.Errorf("upstream calls = %d, want the %d-page cap", got, maxFeaturedPages)
	}
	if len(results) != 15 {
		t.Errorf("results = %d, want 15 (5 English from each of %d pages)", len(results), maxFeaturedPages)
	}
	for _, item := range results {
		if item["original_language"] != "en" {
			t.Fatalf("kept a %v entry with english_only on", item["original_language"])
		}
	}
	// Each backfill page must actually be a different page of the feed.
	for i := 0; i < maxFeaturedPages; i++ {
		if got := e.upstream.hit(t, i).query.Get("page"); got != fmt.Sprint(i+1) {
			t.Errorf("call %d requested page %q, want %d", i, got, i+1)
		}
	}
}

// TestFeaturedEnglishOnlyKeepsUnclassifiedEntries pins the deliberate bias:
// the filter hides titles known to be foreign, never titles it could not read.
func TestFeaturedEnglishOnlyKeepsUnclassifiedEntries(t *testing.T) {
	e := newEnv(t, true)
	e.prefs.set(serversettings.DiscoverySourceTMDBTrending, true)
	e.upstream.setRespond(func(req *http.Request) (int, string) {
		if req.URL.Query().Get("page") != "1" {
			return http.StatusOK, `{"results":[]}`
		}
		return http.StatusOK, `{"results":[
			{"id":1,"name":"Classified English","original_language":"en"},
			{"id":2,"name":"Unclassified"},
			{"id":3,"name":"Classified Korean","original_language":"ko"}
		]}`
	})

	_, results := decodeFeatured(t, e.doOK(t, "/discover/tv/featured"))
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2 (the Korean entry drops, the unclassified stays)", len(results))
	}
	if results[1]["name"] != "Unclassified" {
		t.Errorf("second entry = %v, want the unclassified one kept", results[1]["name"])
	}
}

// TestFeaturedCacheIsKeyedBySourceAndFilter guards the setting change itself:
// flipping either preference must not serve the previous variant's row out of
// the cache.
func TestFeaturedCacheIsKeyedBySourceAndFilter(t *testing.T) {
	e := newEnv(t, true)
	e.upstream.setRespond(func(req *http.Request) (int, string) {
		return http.StatusOK, fmt.Sprintf(
			`{"results":[{"id":1,"name":%q,"original_language":"en"}]}`, req.URL.Path)
	})

	firstSource, first := decodeFeatured(t, e.doOK(t, "/discover/tv/featured"))
	if first[0]["name"] != "/3/trending/tv/week" {
		t.Fatalf("first row came from %v, want the trending feed", first[0]["name"])
	}

	// Same source served from cache: no second upstream call.
	if _, _ = decodeFeatured(t, e.doOK(t, "/discover/tv/featured")); e.upstream.hitCount() != 1 {
		t.Errorf("upstream calls = %d, want 1 — the second read should hit the cache", e.upstream.hitCount())
	}

	e.prefs.set(serversettings.DiscoverySourceTMDBPopular, false)
	secondSource, second := decodeFeatured(t, e.doOK(t, "/discover/tv/featured"))
	if secondSource == firstSource {
		t.Error("source did not change after the setting changed")
	}
	if second[0]["name"] != "/3/tv/popular" {
		t.Errorf("row came from %v, want the popular feed after the setting changed", second[0]["name"])
	}
}

// TestFeaturedSurfacesMissingCredentials keeps a misconfigured server honest
// rather than serving an empty row that looks like "nothing is trending".
func TestFeaturedSurfacesMissingCredentials(t *testing.T) {
	e := newEnv(t, false)
	rec := e.do(t, "/discover/tv/featured")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when TMDB is not configured", rec.Code)
	}
	if e.upstream.hitCount() != 0 {
		t.Errorf("upstream calls = %d, want 0", e.upstream.hitCount())
	}
}

// TestFeaturedReportsUpstreamFailure distinguishes "the feed is broken" from
// "the feed is empty".
func TestFeaturedReportsUpstreamFailure(t *testing.T) {
	e := newEnv(t, true)
	e.upstream.setRespond(func(*http.Request) (int, string) {
		return http.StatusInternalServerError, `{"status_message":"boom"}`
	})

	rec := e.do(t, "/discover/tv/featured")
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 when the upstream feed fails", rec.Code)
	}
}
