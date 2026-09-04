package discover

import (
	"testing"
	"time"
)

// TestTVListRowsProxyTheirTMDBLists pins the two plain TV rows onto the TMDB
// lists they stand for, page included.
func TestTVListRowsProxyTheirTMDBLists(t *testing.T) {
	e := newEnv(t, true)

	for i, row := range []struct{ route, path string }{
		{"/discover/tv/on-the-air?page=2", "/3/tv/on_the_air"},
		{"/discover/tv/top-rated?page=2", "/3/tv/top_rated"},
	} {
		e.doOK(t, row.route)
		hit := e.upstream.hit(t, i)
		if hit.path != row.path || hit.query.Get("page") != "2" {
			t.Errorf("%s reached %s?page=%s, want %s page 2", row.route, hit.path, hit.query.Get("page"), row.path)
		}
	}
}

// TestUpcomingTVQueriesFutureWindow is the TV twin of the movie contract: a
// discover query on the first air date from today through three months out,
// so the row is premieres rather than returning seasons, with the page clamp
// every list route has.
func TestUpcomingTVQueriesFutureWindow(t *testing.T) {
	e := newEnv(t, true)

	windowAt := func(now time.Time) [2]string {
		return [2]string{now.Format("2006-01-02"), now.AddDate(0, 3, 0).Format("2006-01-02")}
	}
	before := windowAt(time.Now())
	e.doOK(t, "/discover/tv/upcoming?page=9999")
	after := windowAt(time.Now())

	hit := e.upstream.hit(t, 0)
	if hit.path != "/3/discover/tv" {
		t.Errorf("upstream path = %s, want /3/discover/tv", hit.path)
	}
	if got := hit.query.Get("page"); got != "500" {
		t.Errorf("upstream page = %q, want the 500 clamp", got)
	}
	if got := hit.query.Get("sort_by"); got != "popularity.desc" {
		t.Errorf("upstream sort_by = %q, want popularity.desc", got)
	}
	window := [2]string{hit.query.Get("first_air_date.gte"), hit.query.Get("first_air_date.lte")}
	if window != before && window != after {
		t.Errorf("upstream air window = %v, want %v (today through three months out)", window, before)
	}
	if _, ok := hit.query["primary_release_date.gte"]; ok {
		t.Error("TV upcoming carried a movie date key")
	}

	e.doOK(t, "/discover/tv/upcoming?page=9999")
	if e.upstream.hitCount() != 1 {
		t.Errorf("upstream hits after repeat = %d, want 1 (served from cache)", e.upstream.hitCount())
	}
}
