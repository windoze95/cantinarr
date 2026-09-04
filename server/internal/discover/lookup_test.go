package discover

import (
	"net/http"
	"strings"
	"testing"
)

// TestLookupTablesProxyVerbatimAndCache covers the static tables behind the
// filter sheet: TMDB's language list is a bare array (not a results envelope)
// and must reach the client byte-for-byte, and a repeat is a cache hit.
func TestLookupTablesProxyVerbatimAndCache(t *testing.T) {
	e := newEnv(t, true)
	const languages = `[{"iso_639_1":"en","english_name":"English","name":"English"},{"iso_639_1":"ko","english_name":"Korean","name":"한국어/조선말"}]`
	e.upstream.setRespond(func(req *http.Request) (int, string) {
		if req.URL.Path == "/3/configuration/languages" {
			return http.StatusOK, languages
		}
		return echoUpstream(req)
	})

	if body := e.doOK(t, "/languages"); body != languages {
		t.Errorf("languages body = %s, want the upstream array verbatim", body)
	}
	if got := e.upstream.hit(t, 0).path; got != "/3/configuration/languages" {
		t.Errorf("upstream path = %s, want /3/configuration/languages", got)
	}
	e.doOK(t, "/languages")
	if e.upstream.hitCount() != 1 {
		t.Errorf("upstream hits after repeat = %d, want 1 (served from cache)", e.upstream.hitCount())
	}

	e.doOK(t, "/providers/regions")
	if got := e.upstream.hit(t, 1).path; got != "/3/watch/providers/regions" {
		t.Errorf("regions upstream path = %s, want /3/watch/providers/regions", got)
	}
}

// TestWatchProvidersKeyOnTypeAndRegion: the movie and TV service lists
// differ per region, so each (type, region) is its own upstream call and
// its own cache entry, and a request without a region asks for the US.
func TestWatchProvidersKeyOnTypeAndRegion(t *testing.T) {
	e := newEnv(t, true)

	movieGB := e.doOK(t, "/providers/movie?region=GB")
	tvGB := e.doOK(t, "/providers/tv?region=GB")
	if movieGB == tvGB {
		t.Error("movie and TV provider lists for GB returned the same body, want separate entries")
	}
	for i, want := range []struct{ path, region string }{
		{"/3/watch/providers/movie", "GB"},
		{"/3/watch/providers/tv", "GB"},
	} {
		hit := e.upstream.hit(t, i)
		if hit.path != want.path || hit.query.Get("watch_region") != want.region {
			t.Errorf("hit %d = %s?watch_region=%s, want %s?watch_region=%s", i, hit.path, hit.query.Get("watch_region"), want.path, want.region)
		}
	}

	e.doOK(t, "/providers/tv")
	if got := e.upstream.hit(t, 2).query.Get("watch_region"); got != "US" {
		t.Errorf("regionless TV providers asked for watch_region=%q, want US", got)
	}
}

// TestKeywordAndCompanySearchRequireQueryAndClampPage mirrors the
// multi-search contract for the two type-ahead lookups.
func TestKeywordAndCompanySearchRequireQueryAndClampPage(t *testing.T) {
	e := newEnv(t, true)

	for _, path := range []string{"/search/keyword", "/search/company"} {
		rec := e.do(t, path)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "query parameter required") {
			t.Errorf("GET %s status = %d body = %s, want 400 query-required", path, rec.Code, rec.Body.String())
		}
	}
	if e.upstream.hitCount() != 0 {
		t.Fatalf("upstream hits = %d, want 0 for rejected lookups", e.upstream.hitCount())
	}

	e.doOK(t, "/search/keyword?query=heat&page=9999")
	e.doOK(t, "/search/company?query=heat&page=9999")
	for i, want := range []string{"/3/search/keyword", "/3/search/company"} {
		hit := e.upstream.hit(t, i)
		if hit.path != want || hit.query.Get("query") != "heat" || hit.query.Get("page") != "500" {
			t.Errorf("hit %d = %s?%v, want %s with query=heat page=500", i, hit.path, hit.query, want)
		}
	}
}
