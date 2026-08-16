package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

func TestFilterRadarrQueueIntersectsCandidateQueueAndTMDB(t *testing.T) {
	items := []radarr.DetailedQueueItem{
		{ID: 7, DownloadID: "old", Movie: &radarr.MovieContext{ID: 1, TmdbID: 99}},
		{ID: 8, DownloadID: "current", Movie: &radarr.MovieContext{ID: 2, TmdbID: 42}},
	}
	matched, err := filterRadarrQueue(nil, items, mediaReadScope{QueueID: 7, TmdbID: 42})
	if err != nil {
		t.Fatalf("filterRadarrQueue: %v", err)
	}
	if len(matched) != 0 {
		t.Fatalf("unrelated candidate queue row survived: %+v", matched)
	}
	matched, err = filterRadarrQueue(nil, items, mediaReadScope{TmdbID: 42})
	if err != nil || len(matched) != 1 || matched[0].ID != 8 {
		t.Fatalf("matching movie filter = %+v, %v", matched, err)
	}
	matched, err = filterRadarrQueue(nil, items, mediaReadScope{QueueID: 8, DownloadID: "stale", TmdbID: 42})
	if err != nil || len(matched) != 0 {
		t.Fatalf("reassigned download survived filter = %+v, %v", matched, err)
	}
}

func TestReleaseCandidateMetadataUsesOneWayReference(t *testing.T) {
	const secret = "indexer-capability-secret"
	rawGUID := "https://indexer.invalid/download/signed-path-sentinel?id=7&apikey=" + secret
	release := radarr.Release{
		GUID:      rawGUID,
		IndexerID: 3, Indexer: "Example", Title: "Movie.2026.1080p", Size: 1024,
		Protocol: "usenet", Rejected: true, Rejections: []string{"Not an upgrade"},
	}
	release.Quality.Quality.Name = "WEBDL-1080p"
	candidates := radarrReleaseCandidates([]radarr.Release{release})
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}
	got := candidates[0]
	if got.Reference != releaseGUIDReference(rawGUID) || !isReleaseGUIDReference(got.Reference) ||
		strings.Contains(got.Reference, secret) || strings.Contains(got.Reference, "signed-path-sentinel") {
		t.Fatalf("unsafe release reference = %q", got.Reference)
	}
	if got.Title != release.Title || got.Quality != "WEBDL-1080p" || got.Size != 1024 ||
		got.Protocol != "usenet" || got.Indexer != "Example" || !got.Rejected || len(got.Rejections) != 1 {
		t.Fatalf("candidate metadata = %+v", got)
	}
}

func TestFilterSonarrQueueIntersectsSeriesAndEpisode(t *testing.T) {
	series := &sonarr.SeriesContext{ID: 1, TmdbID: 42, TvdbID: 4242}
	items := []sonarr.DetailedQueueItem{
		{ID: 7, Series: series, Episode: &sonarr.EpisodeContext{SeasonNumber: 2, EpisodeNumber: 7}},
		{ID: 8, Series: series, Episode: &sonarr.EpisodeContext{SeasonNumber: 2, EpisodeNumber: 8}},
		{ID: 9, Series: &sonarr.SeriesContext{ID: 2, TmdbID: 99, TvdbID: 9999}, Episode: &sonarr.EpisodeContext{SeasonNumber: 2, EpisodeNumber: 7}},
	}
	matched, err := filterSonarrQueue(nil, items, mediaReadScope{TmdbID: 42, TvdbID: 4242, SeasonNumber: 2, EpisodeNumber: 7})
	if err != nil {
		t.Fatalf("filterSonarrQueue: %v", err)
	}
	if len(matched) != 1 || matched[0].ID != 7 {
		t.Fatalf("scoped TV matches = %+v, want only queue 7", matched)
	}
}

func TestQueueTargetVerificationRequiresExactScope(t *testing.T) {
	if got := queueTargetVerification(false, 0); got != nil {
		t.Fatalf("unscoped queue read produced verification: %+v", got)
	}
	absent := queueTargetVerification(true, 0)
	if absent == nil || absent.Kind != VerificationQueueTarget || !absent.ExactScope || absent.TargetPresent {
		t.Fatalf("exact absent verification = %+v", absent)
	}
	present := queueTargetVerification(true, 1)
	if present == nil || !present.TargetPresent {
		t.Fatalf("exact present verification = %+v", present)
	}
}

func TestFilterChaptarrQueueIntersectsBookIdentity(t *testing.T) {
	items := []chaptarr.DetailedQueueItem{
		{ID: 7, DownloadID: "old", BookID: 999, AuthorID: 1},
		{ID: 8, DownloadID: "current", BookID: 123, AuthorID: 456},
		{ID: 9, DownloadID: "ctx", Book: &chaptarr.BookContext{ID: 123}, Author: &chaptarr.AuthorContext{ID: 456}},
	}
	if matched := filterChaptarrQueue(items, mediaReadScope{BookID: 123}); len(matched) != 2 || matched[0].ID != 8 || matched[1].ID != 9 {
		t.Fatalf("book filter = %+v", matched)
	}
	if matched := filterChaptarrQueue(items, mediaReadScope{QueueID: 7, BookID: 123}); len(matched) != 0 {
		t.Fatalf("unrelated candidate queue row survived: %+v", matched)
	}
	if matched := filterChaptarrQueue(items, mediaReadScope{AuthorID: 1}); len(matched) != 1 || matched[0].ID != 7 {
		t.Fatalf("author filter = %+v", matched)
	}
	if matched := filterChaptarrQueue(items, mediaReadScope{QueueID: 8, DownloadID: "stale", BookID: 123}); len(matched) != 0 {
		t.Fatalf("reassigned download survived filter = %+v", matched)
	}
}

func TestFilterChaptarrHistoryMatchesBookThenAuthor(t *testing.T) {
	records := []chaptarr.HistoryRecord{
		{ID: 1, BookID: 123, AuthorID: 456},
		{ID: 2, BookID: 999, AuthorID: 456},
		{ID: 3, Book: &chaptarr.BookContext{ID: 123}, Author: &chaptarr.AuthorContext{ID: 456}},
	}
	if matched := filterChaptarrHistory(records, mediaReadScope{}); len(matched) != 3 {
		t.Fatalf("unscoped history filter = %+v", matched)
	}
	if matched := filterChaptarrHistory(records, mediaReadScope{BookID: 123}); len(matched) != 2 || matched[0].ID != 1 || matched[1].ID != 3 {
		t.Fatalf("book history filter = %+v", matched)
	}
	if matched := filterChaptarrHistory(records, mediaReadScope{AuthorID: 456}); len(matched) != 3 {
		t.Fatalf("author history filter = %+v", matched)
	}
	if matched := filterChaptarrHistory(records, mediaReadScope{BookID: 111}); len(matched) != 0 {
		t.Fatalf("unrelated book history survived = %+v", matched)
	}
}

// --- scoped history: the read that went blind ---
//
// GetHistory returns one page of the GLOBAL log. On a busy library that window
// is a few days wide, so post-filtering it for a title whose last event was two
// weeks ago returns nothing — and the agent is told "No TV history found." when
// what actually happened is that it looked in the wrong place. That is what
// happened on issue #814: the nine 2026-07-21 imports were the whole evidence
// base and had long fallen off the page. Every fixture below therefore fills
// the global page with OTHER titles: a scoped read that falls back to it must
// fail these tests.

// otherTitlesGlobalHistory is a global history page that says nothing about the
// title in scope — the state a busy instance is in within days.
func otherTitlesGlobalHistory() []map[string]any {
	out := make([]map[string]any, 0, 100)
	for i := 0; i < 100; i++ {
		out = append(out, map[string]any{
			"id": int64(70000 + i), "seriesId": 9, "movieId": 9, "eventType": "grabbed",
			"sourceTitle": fmt.Sprintf("Other.Show.S01E%02d.1080p-NOISE", i),
			"date":        "2026-08-03T09:00:00Z",
			"series":      map[string]any{"id": 9, "title": "Other Show", "tmdbId": 111, "tvdbId": 999},
			"movie":       map[string]any{"id": 9, "title": "Other Movie", "tmdbId": 111},
			"episode":     map[string]any{"id": 900 + i, "seriesId": 9, "seasonNumber": 1, "episodeNumber": i},
		})
	}
	return out
}

// futuramaSeriesHistory is what the per-series endpoint still holds two weeks
// after the fact: the grab and import behind every one of the nine files.
func futuramaSeriesHistory() []map[string]any {
	var out []map[string]any
	for i := 9; i >= 1; i-- {
		for _, event := range []string{"downloadFolderImported", "grabbed"} {
			out = append(out, map[string]any{
				"id": int64(8000 + i*10), "seriesId": 28, "episodeId": 1100 + i, "eventType": event,
				"downloadId":  fmt.Sprintf("SABnzbd_nzo_%02d", i),
				"sourceTitle": fmt.Sprintf("Futurama.S11E%02d.1080p.DSNP.WEB-DL.DDP5.1.H.264.Dual-CM", i),
				"date":        "2026-07-21T09:30:00Z",
				"series":      map[string]any{"id": 28, "title": "Futurama", "tmdbId": 615, "tvdbId": 73871},
				"episode":     map[string]any{"id": 1100 + i, "seriesId": 28, "seasonNumber": 11, "episodeNumber": i},
			})
		}
	}
	return out
}

// TestGetHistoryScopedToASeriesReadsThePerSeriesEndpoint is the regression. The
// records exist, the global page cannot see them, and the answer must still be
// the series' own history.
func TestGetHistoryScopedToASeriesReadsThePerSeriesEndpoint(t *testing.T) {
	fake := &sonarrFileFake{
		t: t, series: futuramaSeries(),
		history:       futuramaSeriesHistory(),
		globalHistory: otherTitlesGlobalHistory(),
	}
	arrServer := fake.start()
	server := newDefaultInstanceToolServer(t, map[string]string{"sonarr": arrServer.URL})

	result, err := server.ExecuteTool(context.Background(), "get_history",
		json.RawMessage(`{"media_type":"tv","tmdb_id":615,"season_number":11}`), adminCallContext())
	if err != nil {
		t.Fatalf("get_history: %v", err)
	}
	if strings.Contains(result.Text, "No TV history found.") {
		t.Fatalf("a scoped read went blind — the per-series records exist:\n%s", result.Text)
	}
	if !strings.Contains(result.Text, "Futurama S11E09") ||
		!strings.Contains(result.Text, "Futurama.S11E09.1080p.DSNP.WEB-DL.DDP5.1.H.264.Dual-CM") ||
		!strings.Contains(result.Text, "2026-07-21 09:30") {
		t.Fatalf("scoped history did not render the series' own records:\n%s", result.Text)
	}
	if strings.Contains(result.Text, "Other Show") {
		t.Fatalf("scoped history leaked another title:\n%s", result.Text)
	}

	var scopedRead, globalRead string
	for _, call := range fake.rec.all() {
		switch {
		case strings.HasPrefix(call.URI, "/api/v3/history/series?"):
			scopedRead = call.URI
		case strings.HasPrefix(call.URI, "/api/v3/history?"):
			globalRead = call.URI
		}
	}
	if scopedRead == "" {
		t.Fatalf("the per-series endpoint was never called: %+v", fake.rec.all())
	}
	if !strings.Contains(scopedRead, "seriesId=28") || !strings.Contains(scopedRead, "seasonNumber=11") {
		t.Fatalf("per-series read = %q, want the resolved series and the scoped season", scopedRead)
	}
	if globalRead != "" {
		t.Fatalf("a scoped read still sifted the global page: %q", globalRead)
	}
}

// TestGetHistoryWithoutIdentityStillReadsTheGlobalPage: an ordinary admin
// "what has been happening" call has no title to scope to, and its behaviour is
// unchanged.
func TestGetHistoryWithoutIdentityStillReadsTheGlobalPage(t *testing.T) {
	fake := &sonarrFileFake{
		t: t, series: futuramaSeries(),
		history:       futuramaSeriesHistory(),
		globalHistory: otherTitlesGlobalHistory(),
	}
	arrServer := fake.start()
	server := newDefaultInstanceToolServer(t, map[string]string{"sonarr": arrServer.URL})

	result, err := server.ExecuteTool(context.Background(), "get_history",
		json.RawMessage(`{"media_type":"tv"}`), adminCallContext())
	if err != nil {
		t.Fatalf("get_history: %v", err)
	}
	if !strings.Contains(result.Text, "Other Show") {
		t.Fatalf("unscoped history should render the global page:\n%s", result.Text)
	}
	for _, call := range fake.rec.all() {
		if strings.HasPrefix(call.URI, "/api/v3/history/series") {
			t.Fatalf("an unscoped read asked for one series' history: %q", call.URI)
		}
	}
}

// TestScopedSonarrHistoryFallsBackWhenTheSeriesCannotBeResolved: an
// unresolvable identity is not an error. It falls back to exactly the behaviour
// that existed before, so a scoping failure can never make history unreadable.
func TestScopedSonarrHistoryFallsBackWhenTheSeriesCannotBeResolved(t *testing.T) {
	fake := &sonarrFileFake{
		t: t, series: []map[string]any{{"id": 9, "title": "Other Show", "tmdbId": 111, "tvdbId": 999}},
		globalHistory: otherTitlesGlobalHistory(),
	}
	arrServer := fake.start()

	records, _, err := scopedSonarrHistory(nil, sonarr.NewClient(arrServer.URL, "key"),
		mediaReadScope{TmdbID: 615, SeasonNumber: 11}, 100)
	if err != nil {
		t.Fatalf("an unresolvable series must not fail the read: %v", err)
	}
	if len(records) != 100 {
		t.Fatalf("fallback returned %d records, want the global page", len(records))
	}
	for _, call := range fake.rec.all() {
		if strings.HasPrefix(call.URI, "/api/v3/history/series") {
			t.Fatalf("an unresolved series was still asked for by id: %q", call.URI)
		}
	}
}

// TestScopedSonarrHistoryPrefersTVDBOverTheTMDBBridge: Sonarr indexes on TVDB
// directly, so a scope carrying it must not spend a library scan to get there.
func TestScopedSonarrHistoryPrefersTVDBOverTheTMDBBridge(t *testing.T) {
	fake := &sonarrFileFake{t: t, series: futuramaSeries(), history: futuramaSeriesHistory()}
	arrServer := fake.start()

	records, _, err := scopedSonarrHistory(nil, sonarr.NewClient(arrServer.URL, "key"),
		mediaReadScope{TmdbID: 615, TvdbID: 73871, SeasonNumber: 11}, 100)
	if err != nil {
		t.Fatalf("scopedSonarrHistory: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("TVDB-scoped read returned nothing")
	}
	resolved := false
	for _, call := range fake.rec.all() {
		if call.URI == "/api/v3/series?tvdbId=73871" {
			resolved = true
		}
		if call.URI == "/api/v3/series" {
			t.Fatalf("a TVDB-scoped read scanned the whole library: %+v", fake.rec.all())
		}
	}
	if !resolved {
		t.Fatalf("TVDB lookup never happened: %+v", fake.rec.all())
	}
}

// --- the movie half ---

// radarrHistoryFake serves the endpoints a scoped movie history read touches.
type radarrHistoryFake struct {
	t             *testing.T
	rec           *callRecorder
	movies        []map[string]any
	movieHistory  []map[string]any
	globalHistory []map[string]any
}

func (f *radarrHistoryFake) start() *httptest.Server {
	f.t.Helper()
	if f.rec == nil {
		f.rec = &callRecorder{}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v3/movie":
			_ = json.NewEncoder(w).Encode(matchingRecords(f.movies, "tmdbId", r.URL.Query().Get("tmdbId")))
		case strings.HasPrefix(r.URL.Path, "/api/v3/movie/"):
			for _, m := range f.movies {
				if fmt.Sprintf("%v", m["id"]) == fmt.Sprintf("%d", pathTailInt(f.t, r.URL.Path)) {
					_ = json.NewEncoder(w).Encode(m)
					return
				}
			}
			http.NotFound(w, r)
		case r.URL.Path == "/api/v3/history":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"totalRecords": len(f.globalHistory), "records": f.globalHistory,
			})
		case r.URL.Path == "/api/v3/history/movie":
			_ = json.NewEncoder(w).Encode(f.movieHistory)
		default:
			f.t.Errorf("unexpected radarr request %s %s", r.Method, r.URL.RequestURI())
			http.NotFound(w, r)
		}
	}))
	f.t.Cleanup(server.Close)
	return server
}

func scopedMovieFixture() ([]map[string]any, []map[string]any) {
	movies := []map[string]any{
		{"id": 9, "title": "Other Movie", "tmdbId": 111, "year": 2001},
		{"id": 42, "title": "Fight Club", "tmdbId": 550, "year": 1999},
	}
	history := []map[string]any{
		{"id": int64(4200), "movieId": 42, "eventType": "downloadFolderImported", "downloadId": "dl-42",
			"date": "2026-07-21T09:30:00Z", "sourceTitle": "Fight.Club.1999.1080p.BluRay-SENTINEL",
			"movie": map[string]any{"id": 42, "title": "Fight Club", "tmdbId": 550, "year": 1999}},
		{"id": int64(4100), "movieId": 42, "eventType": "grabbed", "downloadId": "dl-42",
			"date": "2026-07-21T08:00:00Z", "sourceTitle": "Fight.Club.1999.1080p.BluRay-SENTINEL",
			"movie": map[string]any{"id": 42, "title": "Fight Club", "tmdbId": 550, "year": 1999}},
	}
	return movies, history
}

// TestGetHistoryScopedToAMovieReadsThePerMovieEndpoint is the movie half of the
// same regression, with the same global page that cannot see the title.
func TestGetHistoryScopedToAMovieReadsThePerMovieEndpoint(t *testing.T) {
	movies, movieHistory := scopedMovieFixture()
	fake := &radarrHistoryFake{
		t: t, movies: movies, movieHistory: movieHistory, globalHistory: otherTitlesGlobalHistory(),
	}
	arrServer := fake.start()
	server := newDefaultInstanceToolServer(t, map[string]string{"radarr": arrServer.URL})

	result, err := server.ExecuteTool(context.Background(), "get_history",
		json.RawMessage(`{"media_type":"movie","tmdb_id":550}`), adminCallContext())
	if err != nil {
		t.Fatalf("get_history: %v", err)
	}
	if strings.Contains(result.Text, "No movie history found.") {
		t.Fatalf("a scoped read went blind — the per-movie records exist:\n%s", result.Text)
	}
	if !strings.Contains(result.Text, "Fight Club (1999)") ||
		!strings.Contains(result.Text, "Fight.Club.1999.1080p.BluRay-SENTINEL") {
		t.Fatalf("scoped history did not render the movie's own records:\n%s", result.Text)
	}
	if strings.Contains(result.Text, "Other Movie") {
		t.Fatalf("scoped history leaked another title:\n%s", result.Text)
	}

	var scopedRead, globalRead string
	for _, call := range fake.rec.all() {
		switch {
		case strings.HasPrefix(call.URI, "/api/v3/history/movie?"):
			scopedRead = call.URI
		case strings.HasPrefix(call.URI, "/api/v3/history?"):
			globalRead = call.URI
		}
	}
	if !strings.Contains(scopedRead, "movieId=42") {
		t.Fatalf("per-movie read = %q, want the resolved movie id", scopedRead)
	}
	if globalRead != "" {
		t.Fatalf("a scoped read still sifted the global page: %q", globalRead)
	}
}

// TestGetHistoryWithoutMovieIdentityStillReadsTheGlobalPage keeps the
// unscoped admin call unchanged.
func TestGetHistoryWithoutMovieIdentityStillReadsTheGlobalPage(t *testing.T) {
	movies, movieHistory := scopedMovieFixture()
	fake := &radarrHistoryFake{
		t: t, movies: movies, movieHistory: movieHistory, globalHistory: otherTitlesGlobalHistory(),
	}
	arrServer := fake.start()
	server := newDefaultInstanceToolServer(t, map[string]string{"radarr": arrServer.URL})

	result, err := server.ExecuteTool(context.Background(), "get_history",
		json.RawMessage(`{"media_type":"movie"}`), adminCallContext())
	if err != nil {
		t.Fatalf("get_history: %v", err)
	}
	if !strings.Contains(result.Text, "Other Movie") {
		t.Fatalf("unscoped history should render the global page:\n%s", result.Text)
	}
	for _, call := range fake.rec.all() {
		if strings.HasPrefix(call.URI, "/api/v3/history/movie") {
			t.Fatalf("an unscoped read asked for one movie's history: %q", call.URI)
		}
	}
}

// TestScopedRadarrHistoryFallsBackWhenTheMovieCannotBeResolved mirrors the TV
// fallback: a title Radarr does not hold reads the global page rather than
// failing.
func TestScopedRadarrHistoryFallsBackWhenTheMovieCannotBeResolved(t *testing.T) {
	fake := &radarrHistoryFake{
		t: t, movies: []map[string]any{{"id": 9, "title": "Other Movie", "tmdbId": 111}},
		globalHistory: otherTitlesGlobalHistory(),
	}
	arrServer := fake.start()

	records, _, err := scopedRadarrHistory(radarr.NewClient(arrServer.URL, "key"), mediaReadScope{TmdbID: 550}, 100)
	if err != nil {
		t.Fatalf("an unresolvable movie must not fail the read: %v", err)
	}
	if len(records) != 100 {
		t.Fatalf("fallback returned %d records, want the global page", len(records))
	}
	for _, call := range fake.rec.all() {
		if strings.HasPrefix(call.URI, "/api/v3/history/movie") {
			t.Fatalf("an unresolved movie was still asked for by id: %q", call.URI)
		}
	}
}
