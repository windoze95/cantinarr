package discover

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/serversettings"
)

// TestDiscoverForwardsBrowseFiltersVerbatim pins the browse contract: every
// allowlisted key reaches TMDB's discover endpoint unchanged, and anything
// else a caller smuggles in never leaves this server.
func TestDiscoverForwardsBrowseFiltersVerbatim(t *testing.T) {
	e := newEnv(t, true)

	e.doOK(t, "/discover/movies?page=2&sort_by=primary_release_date.desc&with_genres=28,12"+
		"&primary_release_year=2019&primary_release_date.gte=2019-01-01&primary_release_date.lte=2019-12-31"+
		"&vote_average.gte=7&vote_count.gte=50&with_watch_providers=8|9&watch_region=GB"+
		"&include_adult=true&language=fr&region=GB&with_keywords=1&with_original_language=ko")

	hit := e.upstream.hit(t, 0)
	if hit.path != "/3/discover/movie" {
		t.Fatalf("upstream path = %s, want /3/discover/movie", hit.path)
	}
	want := map[string]string{
		"page":                     "2",
		"sort_by":                  "primary_release_date.desc",
		"with_genres":              "28,12",
		"primary_release_year":     "2019",
		"primary_release_date.gte": "2019-01-01",
		"primary_release_date.lte": "2019-12-31",
		"vote_average.gte":         "7",
		"vote_count.gte":           "50",
		"with_watch_providers":     "8|9",
		"watch_region":             "GB",
	}
	for k, v := range want {
		if got := hit.query.Get(k); got != v {
			t.Errorf("upstream %s = %q, want %q", k, got, v)
		}
	}
	for _, k := range []string{"include_adult", "region", "with_keywords", "with_original_language"} {
		if _, ok := hit.query[k]; ok {
			t.Errorf("upstream carried %s=%q, want it dropped", k, hit.query.Get(k))
		}
	}
	// The locale is the client's fixed en-US, never a caller's.
	if got := hit.query["language"]; len(got) != 1 || got[0] != "en-US" {
		t.Errorf("upstream language = %v, want exactly [en-US]", got)
	}
}

// TestDiscoverTVForwardsAirDateFilters keeps the TV allowlist on TV's own
// date keys: a movie-only key is dropped rather than forwarded.
func TestDiscoverTVForwardsAirDateFilters(t *testing.T) {
	e := newEnv(t, true)

	e.doOK(t, "/discover/tv?first_air_date_year=2020&first_air_date.gte=2020-01-01"+
		"&sort_by=first_air_date.desc&primary_release_year=2019")

	hit := e.upstream.hit(t, 0)
	if hit.path != "/3/discover/tv" {
		t.Fatalf("upstream path = %s, want /3/discover/tv", hit.path)
	}
	for k, v := range map[string]string{
		"first_air_date_year": "2020",
		"first_air_date.gte":  "2020-01-01",
		"sort_by":             "first_air_date.desc",
	} {
		if got := hit.query.Get(k); got != v {
			t.Errorf("upstream %s = %q, want %q", k, got, v)
		}
	}
	if _, ok := hit.query["primary_release_year"]; ok {
		t.Error("upstream carried primary_release_year on a TV query, want it dropped")
	}
}

// TestDiscoverRejectsUnknownSortBy answers a sort TMDB would 422 on with a 400
// from this server, and never dials out for it: the alternative is a 502 that
// reads as an outage and is retried on every request.
func TestDiscoverRejectsUnknownSortBy(t *testing.T) {
	e := newEnv(t, true)

	for _, path := range []string{
		"/discover/movies?sort_by=rating.desc",
		"/discover/movies?sort_by=first_air_date.desc",
		"/discover/tv?sort_by=primary_release_date.desc",
		"/discover/movies?sort_by=popularity",
		"/discover/movies?sort_by=popularity.sideways",
	} {
		rec := e.do(t, path)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid sort_by") {
			t.Errorf("GET %s status = %d body = %s, want 400 invalid sort_by", path, rec.Code, rec.Body.String())
		}
	}
	if e.upstream.hitCount() != 0 {
		t.Fatalf("upstream hits = %d, want 0 for rejected sorts", e.upstream.hitCount())
	}

	// Every sortable field is accepted in both directions.
	for _, field := range movieDiscover.sortFields {
		for _, direction := range []string{"asc", "desc"} {
			e.doOK(t, fmt.Sprintf("/discover/movies?sort_by=%s.%s", field, direction))
		}
	}
	for _, field := range tvDiscover.sortFields {
		e.doOK(t, fmt.Sprintf("/discover/tv?sort_by=%s.desc", field))
	}
}

// TestDiscoverRejectsMalformedDates keeps the date keys to the YYYY-MM-DD
// TMDB reads.
func TestDiscoverRejectsMalformedDates(t *testing.T) {
	e := newEnv(t, true)

	for _, path := range []string{
		"/discover/movies?primary_release_date.gte=2019",
		"/discover/movies?primary_release_date.lte=12/31/2019",
		"/discover/tv?first_air_date.lte=next-week",
	} {
		rec := e.do(t, path)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "want YYYY-MM-DD") {
			t.Errorf("GET %s status = %d body = %s, want 400 naming the date shape", path, rec.Code, rec.Body.String())
		}
	}
	if e.upstream.hitCount() != 0 {
		t.Fatalf("upstream hits = %d, want 0 for rejected dates", e.upstream.hitCount())
	}
}

// TestPageIsClampedToTMDBRange covers every list shape: a page TMDB would
// refuse is clamped into its 1..500 range rather than surfaced as a 502, and
// the clamped requests share the cache entry of the page they land on.
func TestPageIsClampedToTMDBRange(t *testing.T) {
	e := newEnv(t, true)

	for _, route := range []string{"/discover/movies", "/discover/trending", "/discover/tv/popular", "/discover/movies/featured"} {
		t.Run(route, func(t *testing.T) {
			before := e.upstream.hitCount()

			low := e.doOK(t, route+"?page=0")
			if got := e.upstream.hit(t, before).query.Get("page"); got != "1" {
				t.Errorf("page=0 reached upstream as page %q, want 1", got)
			}
			e.doOK(t, route+"?page=9999")
			if got := e.upstream.hit(t, before+1).query.Get("page"); got != "500" {
				t.Errorf("page=9999 reached upstream as page %q, want 500", got)
			}
			// Both land on page 1, which is already cached.
			for _, junk := range []string{"-3", "abc"} {
				if body := e.doOK(t, route+"?page="+junk); body != low {
					t.Errorf("page=%s body = %s, want the cached page-1 body", junk, body)
				}
			}
			if got := e.upstream.hitCount(); got != before+2 {
				t.Errorf("upstream hits = %d, want %d (junk pages served from the page-1 entry)", got, before+2)
			}
		})
	}

	// The headline feed reports the page it actually served.
	if body := e.doOK(t, "/discover/movies/featured?page=9999"); !strings.Contains(body, `"page":500`) {
		t.Errorf("featured body = %s, want page 500 reported", body)
	}
}

// TestRatingSortGetsAVoteFloorUnlessGiven keeps a rating sort meaningful:
// without a vote floor TMDB orders one-vote titles first.
func TestRatingSortGetsAVoteFloorUnlessGiven(t *testing.T) {
	e := newEnv(t, true)

	e.doOK(t, "/discover/movies?sort_by=vote_average.desc")
	if got := e.upstream.hit(t, 0).query.Get("vote_count.gte"); got != ratingSortMinVotes {
		t.Errorf("rating sort vote_count.gte = %q, want the %s floor", got, ratingSortMinVotes)
	}

	e.doOK(t, "/discover/tv?sort_by=vote_average.asc&vote_count.gte=10")
	if got := e.upstream.hit(t, 1).query.Get("vote_count.gte"); got != "10" {
		t.Errorf("explicit vote_count.gte = %q, want the caller's 10 kept", got)
	}

	e.doOK(t, "/discover/movies?sort_by=popularity.desc")
	if _, ok := e.upstream.hit(t, 2).query["vote_count.gte"]; ok {
		t.Error("popularity sort carried a vote floor, want none")
	}
}

// TestBrowsePushesEnglishOnlyUpstream is the difference between a thinned
// page and a full one: on a discover query TMDB applies the admin's language
// preference itself, so the page count is exact and every page is full.
func TestBrowsePushesEnglishOnlyUpstream(t *testing.T) {
	e := newEnv(t, true)
	e.prefs.set(serversettings.DefaultDiscoverySource, true)

	e.doOK(t, "/discover/movies?with_genres=28")
	if got := e.upstream.hit(t, 0).query.Get("with_original_language"); got != "en" {
		t.Errorf("with_original_language = %q, want en with english_only on", got)
	}

	// Flipping the switch is a different query, never the cached one.
	e.prefs.set(serversettings.DefaultDiscoverySource, false)
	e.doOK(t, "/discover/movies?with_genres=28")
	if e.upstream.hitCount() != 2 {
		t.Fatalf("upstream hits = %d, want 2 (the unfiltered read is its own entry)", e.upstream.hitCount())
	}
	if _, ok := e.upstream.hit(t, 1).query["with_original_language"]; ok {
		t.Error("with_original_language sent with english_only off, want none")
	}
}

// TestUpcomingRowPushesEnglishOnlyUpstream: Coming Soon is a discover query
// too, so it fills the same way.
func TestUpcomingRowPushesEnglishOnlyUpstream(t *testing.T) {
	e := newEnv(t, true)
	e.prefs.set(serversettings.DefaultDiscoverySource, true)

	e.doOK(t, "/discover/movies/upcoming")
	hit := e.upstream.hit(t, 0)
	if hit.path != "/3/discover/movie" || hit.query.Get("with_original_language") != "en" {
		t.Errorf("upstream = %s %v, want /3/discover/movie with with_original_language=en", hit.path, hit.query)
	}
}
