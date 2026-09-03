package discover

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/serversettings"
)

// decodeFeaturedEnvelope reads the whole headline envelope, page count
// included, which is what a grid continuing the row relies on.
func decodeFeaturedEnvelope(t *testing.T, body string) featuredPage {
	t.Helper()
	var page featuredPage
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatalf("decode featured envelope: %v (body = %s)", err, body)
	}
	return page
}

// TestFeaturedPageTwoIsThePlainUpstreamPage pins the paging contract for the
// headline feed: page one is still the backfilled row, while a later page is
// the matching upstream page, filtered but never refilled, and both report
// how far the feed goes.
func TestFeaturedPageTwoIsThePlainUpstreamPage(t *testing.T) {
	e := newEnv(t, true)
	e.prefs.set(serversettings.DiscoverySourceTMDBTrending, true)
	// Every page is 10 entries, 5 English, 9 pages in the feed.
	e.upstream.setRespond(func(*http.Request) (int, string) {
		return http.StatusOK, tmdbListPage("show", 10, "en", "es")
	})

	second := decodeFeaturedEnvelope(t, e.doOK(t, "/discover/tv/featured?page=2"))
	if e.upstream.hitCount() != 1 {
		t.Fatalf("upstream calls = %d, want 1 (a later page is never backfilled)", e.upstream.hitCount())
	}
	if got := e.upstream.hit(t, 0).query.Get("page"); got != "2" {
		t.Errorf("upstream page = %q, want 2", got)
	}
	if second.Page != 2 || second.TotalPages != 9 || len(second.Results) != 5 {
		t.Errorf("page 2 = page %d of %d with %d results, want page 2 of 9 with the 5 English entries",
			second.Page, second.TotalPages, len(second.Results))
	}

	first := decodeFeaturedEnvelope(t, e.doOK(t, "/discover/tv/featured?page=1"))
	if e.upstream.hitCount() != 1+maxFeaturedPages {
		t.Errorf("upstream calls = %d, want the row's %d-page backfill on top", e.upstream.hitCount(), maxFeaturedPages)
	}
	if first.Page != 1 || first.TotalPages != 9 || len(first.Results) != 15 {
		t.Errorf("page 1 = page %d of %d with %d results, want the backfilled row reporting 9 pages",
			first.Page, first.TotalPages, len(first.Results))
	}

	// A bare request is page one, served from the same cache entry.
	if body := e.doOK(t, "/discover/tv/featured"); e.upstream.hitCount() != 1+maxFeaturedPages || !strings.Contains(body, `"page":1`) {
		t.Errorf("bare featured request cost %d calls and answered %s, want the cached page 1", e.upstream.hitCount(), body)
	}
}

// TestFeaturedTraktPagesOpenEnded covers the Trakt source, whose page count
// this server does not read: each page past the row asks for the same twenty
// a TMDB page carries, and the feed reports another page for as long as the
// one served had entries.
func TestFeaturedTraktPagesOpenEnded(t *testing.T) {
	e := newEnv(t, true)
	e.prefs.set(serversettings.DiscoverySourceTraktTrending, false)
	e.upstream.setRespond(func(req *http.Request) (int, string) {
		if req.URL.Query().Get("page") == "3" {
			return http.StatusOK, `[]`
		}
		return http.StatusOK, `[
			{"watchers":50,"movie":{"title":"Sinners","year":2025,"language":"en",
			 "released":"2025-04-18","ids":{"tmdb":1233413}}},
			{"watchers":40,"movie":{"title":"No TMDB Id","year":2025,"language":"en",
			 "ids":{"tmdb":0}}}
		]`
	})

	first := decodeFeaturedEnvelope(t, e.doOK(t, "/discover/movies/featured"))
	if got := e.upstream.hit(t, 0).query; got.Get("page") != "1" || got.Get("limit") != "40" {
		t.Errorf("row asked Trakt for page %s limit %s, want page 1 over-asking 40", got.Get("page"), got.Get("limit"))
	}
	if first.Source != serversettings.DiscoverySourceTraktTrending || first.TotalPages != 2 || len(first.Results) != 1 {
		t.Errorf("row = %s page 1 of %d with %d results, want Trakt, another page reported, the id-less entry dropped",
			first.Source, first.TotalPages, len(first.Results))
	}

	second := decodeFeaturedEnvelope(t, e.doOK(t, "/discover/movies/featured?page=2"))
	if got := e.upstream.hit(t, 1).query; got.Get("page") != "2" || got.Get("limit") != "20" {
		t.Errorf("page 2 asked Trakt for page %s limit %s, want page 2 of 20", got.Get("page"), got.Get("limit"))
	}
	if second.Page != 2 || second.TotalPages != 3 || len(second.Results) != 1 {
		t.Errorf("page 2 = page %d of %d with %d results, want page 2 of 3 with 1 result", second.Page, second.TotalPages, len(second.Results))
	}

	last := decodeFeaturedEnvelope(t, e.doOK(t, "/discover/movies/featured?page=3"))
	if last.TotalPages != 3 || len(last.Results) != 0 {
		t.Errorf("empty page 3 = page 3 of %d with %d results, want it to close the feed at 3", last.TotalPages, len(last.Results))
	}
}

// TestFeaturedLaterPageFallsBackWithTheRow keeps a grid under a Trakt row
// from erroring where the row itself would have fallen back.
func TestFeaturedLaterPageFallsBackWithTheRow(t *testing.T) {
	e := newEnv(t, true)
	e.prefs.set(serversettings.DiscoverySourceTraktTrending, false)
	e.upstream.setRespond(func(req *http.Request) (int, string) {
		if strings.HasPrefix(req.URL.Path, "/movies/") {
			return http.StatusInternalServerError, `{"error":"trakt is having a day"}`
		}
		return http.StatusOK, tmdbListPage("movie", 20, "en")
	})

	page := decodeFeaturedEnvelope(t, e.doOK(t, "/discover/movies/featured?page=4"))
	if page.Source != serversettings.DiscoverySourceTMDBTrending || page.Page != 4 {
		t.Errorf("fallback page = %s page %d, want TMDB trending page 4", page.Source, page.Page)
	}
	if hit := e.upstream.hit(t, 1); hit.path != "/3/trending/movie/week" || hit.query.Get("page") != "4" {
		t.Errorf("fallback upstream = %s page %s, want /3/trending/movie/week page 4", hit.path, hit.query.Get("page"))
	}
}
