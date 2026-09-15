package musicdiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const aID = "11111111-1111-4111-8111-111111111111"
const bID = "22222222-2222-4222-8222-222222222222"
const cID = "33333333-3333-4333-8333-333333333333"

func testService(t *testing.T, fn http.HandlerFunc) *Service {
	t.Helper()
	upstream := httptest.NewServer(fn)
	t.Cleanup(upstream.Close)
	s := NewService()
	s.mb.base, s.lb.base = upstream.URL, upstream.URL
	s.mb.interval, s.lb.interval = 0, 0
	s.now = func() time.Time { return time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC) }
	return s
}

func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
func groups(ids ...string) []map[string]any {
	out := []map[string]any{}
	for _, id := range ids {
		out = append(out, map[string]any{"id": id, "title": "Same title", "primary-type": "Album", "artist-credit": []any{map[string]any{"name": "Artist", "joinphrase": " & "}, map[string]any{"artist": map[string]any{"name": "Guest"}}}})
	}
	return out
}
func decodePage(t *testing.T, body []byte, err error) Page {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	var page Page
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	return page
}

func TestChartBatchEnrichmentOrderAndFilteredPagination(t *testing.T) {
	var charts, batches atomic.Int32
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != userAgent {
			t.Error("missing contactable User-Agent")
		}
		if strings.Contains(r.URL.Path, "sitewide") {
			charts.Add(1)
			if r.URL.Query().Get("count") != "20" || r.URL.Query().Get("range") != "this_month" {
				t.Error(r.URL.RawQuery)
			}
			if r.URL.Query().Get("offset") == "20" {
				jsonResponse(w, map[string]any{"payload": map[string]any{"offset": 20, "total_release_group_count": 21, "release_groups": []any{map[string]string{"release_group_mbid": bID}}}})
				return
			}
			jsonResponse(w, map[string]any{"payload": map[string]any{"offset": 0, "total_release_group_count": 21, "release_groups": []any{
				map[string]string{"release_group_mbid": aID}, map[string]string{"release_group_mbid": bID},
				map[string]string{"release_group_mbid": aID}, map[string]string{"release_group_mbid": cID}}}})
		} else {
			batches.Add(1)
			if !strings.HasPrefix(r.URL.Query().Get("query"), "rgid:(") {
				t.Error(r.URL.RawQuery)
			}
			g := groups(cID, bID, aID)
			g[0]["primary-type"] = "Single"
			g[1]["primary-type"] = "EP"
			if r.URL.Query().Get("limit") == "1" {
				g = g[1:2]
			}
			jsonResponse(w, map[string]any{"count": len(g), "offset": 0, "release-groups": g})
		}
	})
	body, err := s.Feed(context.Background(), "popular", "this_month", "", 1)
	page := decodePage(t, body, err)
	if len(page.Results) != 2 || page.Results[0].ForeignID != aID || page.Results[1].ForeignID != bID || page.Results[1].ReleaseType != "EP" || page.NextPage != 2 {
		t.Fatalf("%+v", page)
	}
	if page.Results[0].Artist != "Artist & Guest" {
		t.Fatalf("%+v", page.Results[0])
	}
	body, err = s.Feed(context.Background(), "popular", "this_month", "", 2)
	if p := decodePage(t, body, err); p.NextPage != 0 || p.Results[0].ForeignID != bID {
		t.Fatalf("%+v", p)
	}
	_, err = s.Feed(context.Background(), "popular", "this_month", "", 1)
	if err != nil || charts.Load() != 2 || batches.Load() != 2 {
		t.Fatalf("cache: %v charts %d batches %d", err, charts.Load(), batches.Load())
	}
}

func TestMissingEnrichmentFailsWholePageAndIsNotCached(t *testing.T) {
	var hits atomic.Int32
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "sitewide") {
			jsonResponse(w, map[string]any{"payload": map[string]any{"offset": 0, "total_release_group_count": 2,
				"release_groups": []any{map[string]string{"release_group_mbid": aID}, map[string]string{"release_group_mbid": bID}}}})
		} else {
			hits.Add(1)
			jsonResponse(w, map[string]any{"count": 1, "offset": 0, "release-groups": groups(aID)})
		}
	})
	for range 2 {
		if body, err := s.Feed(context.Background(), "popular", "this_week", "", 1); err == nil || body != nil {
			t.Fatalf("must fail, got %s / %v", body, err)
		}
	}
	if hits.Load() != 2 {
		t.Fatal("failed enrichment was cached")
	}
}

func TestFreshDateBoundariesAndReleaseGroupIdentity(t *testing.T) {
	today := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	input := []freshRelease{
		{aID, "Same title", "A", "2026-08-07", "Album"},
		{bID, "Same title", "B", "2026-09-05", "EP"},
		{aID, "Same title", "A", "2026-09-01", "Album"},
		{cID, "Future", "C", "2026-09-06", "Album"},
		{cID, "Old", "C", "2026-08-06", "Album"},
		{cID, "Single", "C", "2026-09-04", "Single"},
		{cID, "Unknown day", "C", "2026-09", "Album"},
	}
	got, err := normalizeFresh(input, today)
	if err != nil || len(got) != 2 || got[0].ForeignID != bID || got[1].ForeignID != aID || got[1].ReleaseDate != "2026-09-01" {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = normalizeFresh(input[:1], today)
	if err != nil || len(got) != 1 {
		t.Fatalf("inclusive lower boundary: %+v %v", got, err)
	}
}

func TestFreshWindowFetchedOnceAndPaginatedLocally(t *testing.T) {
	var hits atomic.Int32
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		q := r.URL.Query()
		if q.Get("days") != "30" || q.Get("future") != "false" || q.Get("past") != "true" || q.Get("offset") != "" || q.Get("release_date") != "2026-09-05" {
			t.Error(q)
		}
		releases := []freshRelease{}
		for i := 0; i < 43; i++ {
			releases = append(releases, freshRelease{fmt.Sprintf("%08d-1111-4111-8111-111111111111", i), "Album", "Artist", "2026-09-04", "Album"})
		}
		jsonResponse(w, map[string]any{"payload": map[string]any{"releases": releases}})
	})
	for page := 1; page <= 3; page++ {
		body, err := s.Feed(context.Background(), "new-releases", "this_week", "", page)
		p := decodePage(t, body, err)
		if len(p.Results) != map[int]int{1: 20, 2: 20, 3: 3}[page] {
			t.Fatalf("%+v", p)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("fresh endpoint fetched %d times", hits.Load())
	}
}

func TestGenreUsesTagQueryAndProviderOrder(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") != "tag:\"hip hop\" AND (primarytype:album OR primarytype:ep)" || r.URL.Query().Get("offset") != "20" || r.URL.Query().Get("limit") != "20" {
			t.Error(r.URL.RawQuery)
		}
		jsonResponse(w, map[string]any{"count": 22, "offset": 20, "release-groups": groups(bID, aID)})
	})
	body, err := s.Feed(context.Background(), "genre", "this_week", "hip-hop", 2)
	p := decodePage(t, body, err)
	if len(p.Results) != 2 || p.Results[0].ForeignID != bID || p.Results[1].ForeignID != aID || p.NextPage != 0 || strings.Contains(p.Scope, "popular") {
		t.Fatalf("%+v", p)
	}
}

func TestMusicPaginationDoesNotInheritTMDBPageLimit(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("offset") != "10000" {
			t.Error(r.URL.RawQuery)
		}
		jsonResponse(w, map[string]any{"count": 10040, "offset": 10000, "release-groups": groups(aID)})
	})
	body, err := s.Feed(context.Background(), "genre", "this_week", "rock", 501)
	p := decodePage(t, body, err)
	if p.NextPage != 502 || len(p.Results) != 1 {
		t.Fatalf("provider still has results: %+v", p)
	}
}

func TestColdAlbumRejectsMismatchedIdentityWithoutCaching(t *testing.T) {
	var hits atomic.Int32
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		id := bID
		if hits.Add(1) > 1 {
			id = aID
		}
		jsonResponse(w, groups(id)[0])
	})
	if _, err := s.Album(context.Background(), aID); err == nil {
		t.Fatal("cold link accepted another release group's metadata")
	}
	body, err := s.Album(context.Background(), aID)
	if err != nil || !strings.Contains(string(body), aID) || hits.Load() != 2 {
		t.Fatalf("failed metadata was cached: %s %v", body, err)
	}
}

func TestMalformedAndUnavailableProvidersNeverBecomeEmptyFeeds(t *testing.T) {
	for _, body := range []string{"{}", "null", "{", `{"payload":{"release_groups":[]}}`, `{"payload":{"release_groups":[],"offset":0,"total_release_group_count":10}}`} {
		t.Run(body, func(t *testing.T) {
			s := testService(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
			if _, err := s.Feed(context.Background(), "popular", "this_week", "", 1); err == nil {
				t.Fatal("accepted malformed chart")
			}
			if _, err := s.Feed(context.Background(), "new-releases", "this_week", "", 1); err == nil {
				t.Fatal("accepted malformed fresh response")
			}
			if _, err := s.Feed(context.Background(), "genre", "this_week", "rock", 1); err == nil {
				t.Fatal("accepted malformed genre response")
			}
		})
	}
}

func TestEmptyFeedNamesScopeAndKeepsContinuation(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "sitewide") {
			jsonResponse(w, map[string]any{"payload": map[string]any{"offset": 0, "total_release_group_count": 21, "release_groups": []any{map[string]string{"release_group_mbid": aID}}}})
		} else {
			g := groups(aID)
			g[0]["primary-type"] = "Single"
			jsonResponse(w, map[string]any{"count": 1, "offset": 0, "release-groups": g})
		}
	})
	body, err := s.Feed(context.Background(), "popular", "this_year", "", 1)
	p := decodePage(t, body, err)
	if len(p.Results) != 0 || p.NextPage != 2 || !strings.Contains(p.EmptyMessage, "this year") || !strings.Contains(p.EmptyMessage, "next page") {
		t.Fatalf("%+v", p)
	}
}

func TestCacheCombinesConcurrentRequestsAndSurvivesCanceledWaiter(t *testing.T) {
	var m memo
	var hits atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	releaseLoad := sync.OnceFunc(func() { close(release) })
	var wg sync.WaitGroup
	t.Cleanup(func() { releaseLoad(); wg.Wait() })
	load := func(ctx context.Context) ([]byte, error) {
		if hits.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return []byte("album"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := m.get(ctx, "key", time.Hour, load); done <- err }()
	<-started
	const waiters = 12
	for range waiters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body, err := m.get(context.Background(), "key", time.Hour, load)
			if err != nil || string(body) != "album" {
				t.Errorf("%s %v", body, err)
			}
		}()
	}
	// Starting goroutines does not mean they have joined the shared fill.
	// Without this barrier, cancel can abandon the only caller's work before
	// the other readers arrive, correctly causing a new fill.
	deadline := time.Now().Add(5 * time.Second)
	for {
		m.mu.Lock()
		callers := m.pending["key"].callers
		m.mu.Unlock()
		if callers == waiters+1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d callers joined the shared fill", callers, waiters+1)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	releaseLoad()
	wg.Wait()
	if hits.Load() != 1 {
		t.Fatalf("duplicate fills %d", hits.Load())
	}
	m.mu.Lock()
	e := m.entries["key"]
	e.expires = time.Now().Add(-time.Second)
	m.entries["key"] = e
	m.mu.Unlock()
	body, err := m.get(context.Background(), "key", time.Hour, func(context.Context) ([]byte, error) { return []byte("updated"), nil })
	if err != nil || string(body) != "updated" {
		t.Fatalf("%s %v", body, err)
	}
}

func TestProviderRateLimitCancellationAndBoundedRetry(t *testing.T) {
	p := newProvider("unused")
	if p.interval != time.Second {
		t.Fatalf("default interval %v", p.interval)
	}
	p.interval = 20 * time.Millisecond
	start := time.Now()
	for range 3 {
		if err := p.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if time.Since(start) < 40*time.Millisecond {
		t.Fatal("calls exceeded provider rate")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(p.wait(ctx), context.Canceled) {
		t.Fatal("wait ignored cancellation")
	}
	var hits atomic.Int32
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
	})
	if err := s.mb.get(context.Background(), "/", &map[string]any{}); err == nil || hits.Load() != 1 {
		t.Fatalf("long Retry-After not bounded: %v hits %d", err, hits.Load())
	}
}

var liveMusic = flag.Bool("music-live", false, "exercise the real ListenBrainz, MusicBrainz and Cover Art Archive endpoints")

func TestLiveProviders(t *testing.T) {
	if !*liveMusic {
		t.Skip("pass -music-live for the external provider journey")
	}
	s := NewService()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	for _, feed := range []struct{ feed, genre string }{{"popular", ""}, {"new-releases", ""}, {"genre", "rock"}} {
		body, err := s.Feed(ctx, feed.feed, "this_week", feed.genre, 1)
		p := decodePage(t, body, err)
		if len(p.Results) == 0 {
			t.Fatalf("no live %s albums: %+v", feed.feed, p)
		}
		t.Logf("%s: %d albums/EPs; next page %d; first %s (%s)", feed.feed, len(p.Results), p.NextPage, p.Results[0].Title, p.Results[0].ForeignID)
		if feed.feed == "popular" {
			detail, err := s.Album(ctx, p.Results[0].ForeignID)
			if err != nil {
				t.Fatal(err)
			}
			var a Album
			if json.Unmarshal(detail, &a) != nil || !reflect.DeepEqual(a.ForeignID, p.Results[0].ForeignID) {
				t.Fatal("cold lookup identity mismatch")
			}
			art, err := s.Artwork(ctx, a.ForeignID)
			if err != nil || len(art) == 0 {
				t.Fatalf("live artwork: %d bytes, %v", len(art), err)
			}
			t.Logf("artwork: %s, %d bytes", http.DetectContentType(art), len(art))
		}
	}
}

func TestPublicSearchQuotesUserQueryAndPreservesDistinctGroups(t *testing.T) {
	service := testService(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		if !strings.Contains(q, `artist:"Same title"`) || !strings.Contains(q, `primarytype:ep`) {
			t.Errorf("search scope: %s", q)
		}
		fmt.Fprintf(w, `{"count":2,"offset":0,"release-groups":[{"id":%q,"title":"Same title","primary-type":"Album","artist-credit":[{"name":"Artist A"}]},{"id":%q,"title":"Same title","primary-type":"EP","artist-credit":[{"name":"Artist B"}]}]}`, "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222")
	})
	body, err := service.Search(context.Background(), "Same title", 1)
	if err != nil {
		t.Fatal(err)
	}
	var page Page
	if json.Unmarshal(body, &page) != nil || len(page.Results) != 2 {
		t.Fatalf("distinct albums merged: %s", body)
	}
}

func TestReleaseReferenceResolvesProviderCanonicalGroup(t *testing.T) {
	release := "11111111-1111-1111-1111-111111111111"
	alias := "22222222-2222-2222-2222-222222222222"
	canonical := "33333333-3333-3333-3333-333333333333"
	service := testService(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release/" + release:
			fmt.Fprintf(w, `{"release-group":{"id":%q}}`, alias)
		case "/release-group/" + alias:
			http.Redirect(w, r, "/release-group/"+canonical+"?inc=artists&fmt=json", http.StatusMovedPermanently)
		case "/release-group/" + canonical:
			fmt.Fprintf(w, `{"id":%q,"title":"Canonical","primary-type":"Album","artist-credit":[{"name":"Artist"}]}`, canonical)
		default:
			t.Errorf("unexpected resolution path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	album, err := service.ResolveAlbum(context.Background(), release, true)
	if err != nil || album.ForeignID != canonical {
		t.Fatalf("release/group identity conflated: %+v %v", album, err)
	}
}

func TestLivePublicSearchAndResolution(t *testing.T) {
	if !*liveMusic {
		t.Skip("pass -music-live for public provider verification")
	}
	s := NewService()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	body, err := s.Search(ctx, "Nevermind", 1)
	if err != nil {
		t.Fatal(err)
	}
	var page Page
	if json.Unmarshal(body, &page) != nil || len(page.Results) == 0 {
		t.Fatal("public album search returned no results")
	}
	album, err := s.ResolveAlbum(ctx, page.Results[0].ForeignID, false)
	if err != nil {
		t.Fatal(err)
	}
	if body, err := s.Album(ctx, album.ForeignID); err != nil || !strings.Contains(string(body), album.ForeignID) {
		t.Fatalf("live cold metadata identity: %v", err)
	}
	t.Logf("MusicBrainz: %d album/EP results; verified release group %s (%s)", len(page.Results), album.ForeignID, album.Title)
}

func TestCanonicalRedirectStaysOnProviderAndPreservesGroupIdentity(t *testing.T) {
	var foreignHits atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { foreignHits.Add(1) }))
	defer foreign.Close()
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/ws/2") {
		case "/release-group/" + aID:
			http.Redirect(w, r, "/ws/2/release-group/"+bID+"?fmt=json", http.StatusMovedPermanently)
		case "/release-group/" + bID:
			fmt.Fprintf(w, `{"id":%q,"title":"Canonical album","primary-type":"Album","artist-credit":[]}`, bID)
		default:
			http.Redirect(w, r, foreign.URL, http.StatusMovedPermanently)
		}
	})
	s.mb.base += "/ws/2"
	album, err := s.ResolveAlbum(context.Background(), aID, false)
	if err != nil || album.ForeignID != bID {
		t.Fatalf("merged identity: %+v %v", album, err)
	}
	metadata, err := s.Album(context.Background(), aID)
	if err != nil || !strings.Contains(string(metadata), bID) {
		t.Fatalf("cold merged album under the production API prefix: %s %v", metadata, err)
	}
	if err := s.mb.get(context.Background(), "/escape", &map[string]any{}); err == nil || foreignHits.Load() != 0 {
		t.Fatalf("redirect left provider: %v hits=%d", err, foreignHits.Load())
	}
}
