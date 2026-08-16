package sonarr

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type failingTransport struct{ err error }

func (f failingTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, f.err }

func TestGetEpisodeFileUsesExactAuthenticatedEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v3/episodefile/91" || r.URL.RawQuery != "" {
			t.Errorf("request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if got := r.Header.Get("X-Api-Key"); got != "episode-key" {
			t.Errorf("X-Api-Key = %q", got)
		}
		_, _ = w.Write([]byte(`{"id":91,"seriesId":4,"seasonNumber":2,"relativePath":"Season 02/Episode.mkv","path":"/library/Season 02/Episode.mkv","size":654321}`))
	}))
	t.Cleanup(server.Close)

	file, err := NewClient(server.URL, "episode-key").GetEpisodeFile(91)
	if err != nil {
		t.Fatalf("GetEpisodeFile() error = %v", err)
	}
	if file.ID != 91 || file.SeriesID != 4 || file.SeasonNumber != 2 || file.Path != "/library/Season 02/Episode.mkv" || file.RelativePath != "Season 02/Episode.mkv" || file.Size != 654321 {
		t.Fatalf("file = %#v", file)
	}
}

func TestGetEpisodeFileOmitsUpstreamErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`secret path /library/private and signed-token`))
	}))
	t.Cleanup(server.Close)

	_, err := NewClient(server.URL, "key").GetEpisodeFile(91)
	if err == nil {
		t.Fatal("GetEpisodeFile() error = nil")
	}
	if message := err.Error(); strings.Contains(message, "/library/private") || strings.Contains(message, "signed-token") {
		t.Fatalf("error leaked upstream body: %q", message)
	}
}

// TestTransportErrorOmitsHost pins the topology-privacy property: transport
// failures embed the full request URL (and DNS errors repeat the hostname),
// and these errors surface to requesters through request failures — so the
// client must summarize them host-free.
func TestTransportErrorOmitsHost(t *testing.T) {
	dnsFailure := &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: "sonarr-internal"}}
	c := NewClient("http://sonarr-internal:8989", "key")
	c.httpClient = &http.Client{Transport: failingTransport{dnsFailure}}

	if err := c.AddSeries(&AddSeriesRequest{}); err == nil {
		t.Fatal("AddSeries succeeded against a failing transport")
	} else if msg := err.Error(); strings.Contains(msg, "sonarr-internal") || strings.Contains(msg, "8989") {
		t.Errorf("AddSeries error %q names the host", msg)
	} else if !strings.Contains(msg, "could not resolve host") {
		t.Errorf("AddSeries error %q lacks the failure summary", msg)
	}

	if _, err := c.LookupByTVDB(1234); err == nil {
		t.Fatal("LookupByTVDB succeeded against a failing transport")
	} else if msg := err.Error(); strings.Contains(msg, "sonarr-internal") || strings.Contains(msg, "8989") {
		t.Errorf("LookupByTVDB error %q names the host", msg)
	} else if !strings.Contains(msg, "sonarr GET /api/v3/series/lookup") {
		t.Errorf("LookupByTVDB error %q does not identify the call", msg)
	}
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	var redirectedRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(destination.Close)

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/credential-sink", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)

	client := NewClient(source.URL, "sonarr-secret")
	if _, err := client.GetAllSeries(); err == nil {
		t.Fatal("GetAllSeries accepted an upstream redirect")
	}
	if _, err := client.SearchReleases(42, 1); err == nil {
		t.Fatal("SearchReleases accepted an upstream redirect")
	}
	if got := redirectedRequests.Load(); got != 0 {
		t.Fatalf("redirect destination received %d requests, want 0", got)
	}
}

func TestClientErrorDoesNotEchoUpstreamBody(t *testing.T) {
	const secret = "PROWLARR_DOWNLOAD_URL_API_KEY_SENTINEL"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"downloadUrl":"https://indexer.invalid/download?apikey=`+secret+`"}`, http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)

	_, err := NewClient(server.URL, "sonarr-secret").GetAllSeries()
	if err == nil {
		t.Fatal("GetAllSeries returned nil error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error echoed upstream response secret: %v", err)
	}
}

// TestUpdateSeriesMonitoring asserts the series update round-trip: the full
// series JSON Sonarr returned is PUT back with only the monitored flags
// patched, preserving every other field (Sonarr's series PUT expects the whole
// resource; dropping fields would corrupt the series). It must NOT use the
// /seasonpass endpoint: seasonpass forces a monitoringOptions.monitor scope and
// every scope rewrites episode monitoring series-wide ("none" even unmonitors
// the series and all of its episodes).
func TestUpdateSeriesMonitoring(t *testing.T) {
	seriesJSON := `{
		"id": 42,
		"title": "Example",
		"monitored": false,
		"qualityProfileId": 7,
		"path": "/tv/Example",
		"someUnknownField": {"nested": [1, 2, 3]},
		"seasons": [
			{"seasonNumber": 0, "monitored": false, "statistics": {"episodeCount": 1}},
			{"seasonNumber": 1, "monitored": true},
			{"seasonNumber": 2, "monitored": false}
		]
	}`

	var putBody map[string]any
	var gotPut bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/series/42":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(seriesJSON))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v3/series/42":
			gotPut = true
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &putBody)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "key")
	if err := c.UpdateSeriesMonitoring(42, true, map[int]bool{2: true}); err != nil {
		t.Fatalf("UpdateSeriesMonitoring: %v", err)
	}
	if !gotPut {
		t.Fatal("expected a PUT /api/v3/series/42")
	}

	if putBody["monitored"] != true {
		t.Errorf("monitored = %v, want true", putBody["monitored"])
	}
	// Fields not related to monitoring must survive the round-trip verbatim.
	if putBody["qualityProfileId"] != float64(7) || putBody["path"] != "/tv/Example" {
		t.Errorf("round-trip dropped fields: qualityProfileId=%v path=%v", putBody["qualityProfileId"], putBody["path"])
	}
	if _, ok := putBody["someUnknownField"]; !ok {
		t.Error("round-trip dropped an unknown field")
	}

	seasons := putBody["seasons"].([]any)
	got := map[int]bool{}
	for _, s := range seasons {
		m := s.(map[string]any)
		got[int(m["seasonNumber"].(float64))] = m["monitored"].(bool)
	}
	// Season 2 was requested; seasons absent from the map keep their flags.
	want := map[int]bool{0: false, 1: true, 2: true}
	for n, mon := range want {
		if got[n] != mon {
			t.Errorf("season %d monitored = %v, want %v", n, got[n], mon)
		}
	}
	// Season statistics and other season fields must survive too.
	s0 := seasons[0].(map[string]any)
	if _, ok := s0["statistics"]; !ok {
		t.Error("round-trip dropped season statistics")
	}
}

// TestSetEpisodesMonitored asserts the bulk episode monitor payload, and that
// an empty id list makes no HTTP call at all.
func TestSetEpisodesMonitored(t *testing.T) {
	var gotPath, gotMethod string
	var body map[string]any
	calls := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotPath = r.URL.Path
		gotMethod = r.Method
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "key")
	if err := c.SetEpisodesMonitored(nil, true); err != nil {
		t.Fatalf("SetEpisodesMonitored(empty): %v", err)
	}
	if calls != 0 {
		t.Fatalf("empty id list made %d HTTP calls, want 0", calls)
	}

	if err := c.SetEpisodesMonitored([]int{11, 12}, true); err != nil {
		t.Fatalf("SetEpisodesMonitored: %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/v3/episode/monitor" {
		t.Errorf("request = %s %s, want PUT /api/v3/episode/monitor", gotMethod, gotPath)
	}
	if body["monitored"] != true {
		t.Errorf("monitored = %v, want true", body["monitored"])
	}
	ids := body["episodeIds"].([]any)
	if len(ids) != 2 || int(ids[0].(float64)) != 11 || int(ids[1].(float64)) != 12 {
		t.Errorf("episodeIds = %v, want [11 12]", body["episodeIds"])
	}
}

// TestAddSeriesCarriesSeasons asserts an explicit-season add sends per-season
// monitored flags in the add payload (Sonarr keeps them through the add and its
// async metadata refresh) and omits addOptions.monitor so episode monitoring
// follows the season flags.
func TestAddSeriesCarriesSeasons(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	addReq := &AddSeriesRequest{
		Title:     "Example",
		TvdbID:    99,
		Monitored: true,
		Seasons: []SeasonResource{
			{SeasonNumber: 1, Monitored: false},
			{SeasonNumber: 2, Monitored: true},
		},
	}
	addReq.AddOptions.SearchForMissingEpisodes = true

	c := NewClient(srv.URL, "key")
	if err := c.AddSeries(addReq); err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	seasons := body["seasons"].([]any)
	got := map[int]bool{}
	for _, s := range seasons {
		m := s.(map[string]any)
		got[int(m["seasonNumber"].(float64))] = m["monitored"].(bool)
	}
	if !got[2] || got[1] {
		t.Errorf("seasons monitored = %v, want season 2 only", got)
	}

	opts := body["addOptions"].(map[string]any)
	if opts["searchForMissingEpisodes"] != true {
		t.Errorf("searchForMissingEpisodes = %v, want true", opts["searchForMissingEpisodes"])
	}
	if _, ok := opts["monitor"]; ok {
		t.Errorf("addOptions.monitor = %v, want omitted for an explicit-season add", opts["monitor"])
	}
}

// TestSeriesCompletion covers the aired-aware completeness rollup: unaired
// episodes don't count against completeness, Specials are excluded from the
// series-wide numbers, and monitoring plays no role at all.
func TestSeriesCompletion(t *testing.T) {
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	past := now.Add(-24 * time.Hour)
	future := now.Add(24 * time.Hour)
	episodes := []Episode{
		// Specials: on disk, must not count toward the series rollup.
		{ID: 1, SeasonNumber: 0, HasFile: true, AirDateUtc: &past},
		// S1: the reported trap — 2 downloaded, 2 aired-but-missing
		// (unmonitored, irrelevant here), 1 unaired.
		{ID: 11, SeasonNumber: 1, HasFile: true, AirDateUtc: &past, Monitored: true},
		{ID: 12, SeasonNumber: 1, HasFile: true, AirDateUtc: &past, Monitored: true},
		{ID: 13, SeasonNumber: 1, AirDateUtc: &past},
		{ID: 14, SeasonNumber: 1, AirDateUtc: &past},
		{ID: 15, SeasonNumber: 1, AirDateUtc: &future},
		// S2: fully unaired (also one with no air date at all = treated unaired).
		{ID: 21, SeasonNumber: 2, AirDateUtc: &future},
		{ID: 22, SeasonNumber: 2},
		// S3: file present despite a future air date (early release) counts
		// as obtainable.
		{ID: 31, SeasonNumber: 3, HasFile: true, AirDateUtc: &future},
	}

	series, bySeason := SeriesCompletion(episodes, now)
	if series.Files != 3 || series.Aired != 5 {
		t.Errorf("series completion = %d/%d, want 3/5", series.Files, series.Aired)
	}
	if series.Complete() {
		t.Error("series with aired episodes missing files must not be complete")
	}
	if c := bySeason[1]; c.Files != 2 || c.Aired != 4 || c.Complete() {
		t.Errorf("season 1 completion = %+v, want 2/4 incomplete", c)
	}
	if c := bySeason[2]; c.Files != 0 || c.Aired != 0 || c.Complete() {
		t.Errorf("season 2 completion = %+v, want 0/0 incomplete", c)
	}
	if c := bySeason[3]; !c.Complete() {
		t.Errorf("season 3 completion = %+v, want complete (early release)", c)
	}
	if c := bySeason[0]; c.Files != 1 {
		t.Errorf("season 0 completion = %+v, want its own per-season entry", c)
	}

	// A caught-up airing series (all aired downloaded, more announced) IS
	// complete.
	caughtUp := []Episode{
		{ID: 1, SeasonNumber: 1, HasFile: true, AirDateUtc: &past},
		{ID: 2, SeasonNumber: 1, AirDateUtc: &future},
	}
	if c, _ := SeriesCompletion(caughtUp, now); !c.Complete() {
		t.Errorf("caught-up airing series = %+v, want complete", c)
	}
}

// TestEpisodeTotals covers the statistics-based fallback totals: real seasons
// only, totalEpisodeCount preferred over the monitored-skewed episodeCount,
// series-level statistics only when no per-season breakdown exists.
func TestEpisodeTotals(t *testing.T) {
	s := &Series{
		Seasons: []SeasonResource{
			{SeasonNumber: 0, Statistics: &SeasonStatistics{EpisodeFileCount: 9, TotalEpisodeCount: 9}},
			{SeasonNumber: 1, Statistics: &SeasonStatistics{EpisodeFileCount: 2, EpisodeCount: 2, TotalEpisodeCount: 4}},
			{SeasonNumber: 2, Statistics: &SeasonStatistics{EpisodeFileCount: 3, EpisodeCount: 3}},
			{SeasonNumber: 3},
		},
	}
	if files, total := s.EpisodeTotals(); files != 5 || total != 7 {
		t.Errorf("EpisodeTotals = %d/%d, want 5/7 (specials excluded, total preferred)", files, total)
	}

	fallback := &Series{Statistics: &SeriesStatistics{EpisodeFileCount: 2, EpisodeCount: 2, TotalEpisodeCount: 12}}
	if files, total := fallback.EpisodeTotals(); files != 2 || total != 12 {
		t.Errorf("EpisodeTotals fallback = %d/%d, want 2/12", files, total)
	}
}

func TestGetImportHistoryUsesExactBoundedFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("eventType") != "3" || q.Get("episodeId") != "99" || q.Get("downloadId") != "ABC/Case+ID" || q.Get("pageSize") != "20" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalRecords":1,"records":[{"id":7,"episodeId":99,"downloadId":"ABC/Case+ID"}]}`))
	}))
	t.Cleanup(server.Close)
	records, err := NewClient(server.URL, "key").GetImportHistory(99, "ABC/Case+ID", 20)
	if err != nil || len(records) != 1 || records[0].EpisodeID != 99 {
		t.Fatalf("records=%+v err=%v", records, err)
	}
}

func TestGetImportHistoryRejectsTruncatedResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"totalRecords":21,"records":[]}`))
	}))
	t.Cleanup(server.Close)
	if _, err := NewClient(server.URL, "key").GetImportHistory(99, "id", 20); err == nil {
		t.Fatal("accepted incomplete filtered history")
	}
}

// TestGetQueueReadsOneCompleteBoundedPage pins the lean queue read's contract:
// explicit paging (an unpaged read is the server's silent 10-row default page),
// unknown-series rows included, and truncation an error rather than a shorter
// queue.
func TestGetQueueReadsOneCompleteBoundedPage(t *testing.T) {
	body := `{"totalRecords":2,"records":[{"seriesId":42,"title":"Andor","status":"downloading"},{"seriesId":0,"title":"Unmatched.Release","status":"downloading"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("page") != "1" || q.Get("pageSize") != "1000" {
			t.Errorf("queue was not requested in one bounded page: %s", r.URL.RawQuery)
		}
		if q.Get("includeUnknownSeriesItems") != "true" {
			t.Errorf("includeUnknownSeriesItems = %q, want true", q.Get("includeUnknownSeriesItems"))
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	items, err := NewClient(server.URL, "key").GetQueue()
	if err != nil {
		t.Fatalf("GetQueue: %v", err)
	}
	if len(items) != 2 || items[0].SeriesID != 42 || items[1].SeriesID != 0 {
		t.Fatalf("items = %+v, want the matched row and the unknown-series row", items)
	}

	body = `{"totalRecords":2,"records":[{"seriesId":42}]}`
	if _, err := NewClient(server.URL, "key").GetQueue(); err == nil {
		t.Fatal("accepted a truncated queue as a complete page")
	}
}

func TestGetQueueDetailedRejectsClampedSinglePage(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Query().Get("sortKey") != "id" || r.URL.Query().Get("sortDirection") != "ascending" {
			t.Errorf("queue snapshot is not stably sorted: %s", r.URL.RawQuery)
		}
		if r.URL.Query().Get("page") != "1" || r.URL.Query().Get("pageSize") != "1000" {
			t.Errorf("queue snapshot was not requested in one bounded page: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"totalRecords":2,"records":[{"id":1}]}`))
	}))
	t.Cleanup(server.Close)
	if _, err := NewClient(server.URL, "key").GetQueueDetailed(); err == nil {
		t.Fatal("accepted a truncated queue as a complete snapshot")
	}
	if requests != 1 {
		t.Fatalf("queue requests=%d, want one atomic bounded page", requests)
	}
}

func TestDetailedQueueFileStateRequiresConsistentEpisodeIdentity(t *testing.T) {
	noFile := 0
	falseValue, trueValue := false, true
	item := DetailedQueueItem{
		SeriesID:       2,
		EpisodeID:      4,
		EpisodeHasFile: &falseValue,
		Series:         &SeriesContext{ID: 2, TvdbID: 100},
		Episode: &EpisodeContext{
			ID: 4, SeriesID: 2, SeasonNumber: 0, EpisodeNumber: 1,
			EpisodeFileID: &noFile, HasFile: &falseValue,
		},
	}
	if got := item.FileIDAtSnapshot(); got == nil || *got != 0 {
		t.Fatalf("exact episode file ID = %v, want known absent", got)
	}
	item.EpisodeHasFile = &trueValue
	if got := item.FileIDAtSnapshot(); got != nil {
		t.Fatalf("contradictory episode state produced file ID %v", *got)
	}
	item.EpisodeHasFile = &falseValue
	item.Episode.ID = 5
	if got := item.FileIDAtSnapshot(); got != nil {
		t.Fatalf("mismatched episode identity produced file ID %v", *got)
	}
}

// TestGetImportHistorySinceCarriesSeriesIdentity pins the catch-up reader: the
// v3 endpoint with the import event filter, cursor windowing, completeness
// proof, and the top-level seriesId each record must carry so a resumed poll
// can collapse per-episode imports to one series alert.
func TestGetImportHistorySinceCarriesSeriesIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/history" || r.URL.Query().Get("eventType") != "3" {
			t.Errorf("request = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalRecords":3,"records":[
			{"id":3,"episodeId":301,"seriesId":6,"eventType":"downloadFolderImported","date":"2026-07-25T12:30:00Z"},
			{"id":2,"episodeId":302,"seriesId":6,"eventType":"downloadFolderImported","date":"2026-07-25T12:20:00Z"},
			{"id":1,"episodeId":100,"seriesId":4,"eventType":"downloadFolderImported","date":"2026-07-25T11:00:00Z"}]}`))
	}))
	t.Cleanup(server.Close)

	since, err := time.Parse(time.RFC3339, "2026-07-25T12:00:00Z")
	if err != nil {
		t.Fatalf("parse since: %v", err)
	}
	records, complete, err := NewClient(server.URL, "key").GetImportHistorySince(since, 10)
	if err != nil {
		t.Fatalf("GetImportHistorySince() error = %v", err)
	}
	if !complete {
		t.Fatal("a page reaching past the cursor must prove the window complete")
	}
	if len(records) != 2 || records[0].SeriesID != 6 || records[1].SeriesID != 6 {
		t.Fatalf("in-window records = %+v, want two series-6 imports", records)
	}
}

// TestGetEpisodeFilesReadsSeriesScopedFileFacts pins the series-wide file read:
// one seriesId-scoped call, and the fields that identify a file apart from the
// episode it sits on (scene name, import time, quality) survive decoding.
func TestGetEpisodeFilesReadsSeriesScopedFileFacts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v3/episodefile" || r.URL.RawQuery != "seriesId=28" {
			t.Errorf("request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if got := r.Header.Get("X-Api-Key"); got != "files-key" {
			t.Errorf("X-Api-Key = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id":501,"seriesId":28,"seasonNumber":11,"relativePath":"Season 11/S11E07.mkv","path":"/tv/Season 11/S11E07.mkv","size":900,
			 "sceneName":"Futurama.S11E07.Love.at.First.Scam.1080p.WEB.h264","dateAdded":"2026-07-21T09:30:00Z",
			 "quality":{"quality":{"name":"WEBDL-1080p"}}},
			{"id":502,"seriesId":28,"seasonNumber":11}]`))
	}))
	t.Cleanup(server.Close)

	files, err := NewClient(server.URL, "files-key").GetEpisodeFiles(28)
	if err != nil {
		t.Fatalf("GetEpisodeFiles() error = %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %d, want 2", len(files))
	}
	if files[0].SceneName != "Futurama.S11E07.Love.at.First.Scam.1080p.WEB.h264" {
		t.Errorf("sceneName = %q", files[0].SceneName)
	}
	if files[0].DateAdded == nil || !files[0].DateAdded.Equal(time.Date(2026, 7, 21, 9, 30, 0, 0, time.UTC)) {
		t.Errorf("dateAdded = %v, want 2026-07-21T09:30:00Z", files[0].DateAdded)
	}
	if files[0].Quality.Quality.Name != "WEBDL-1080p" {
		t.Errorf("quality = %q", files[0].Quality.Quality.Name)
	}
	// A file Sonarr sent without those fields must decode as absent, not as a
	// zero timestamp that would read as "imported in year 1".
	if files[1].DateAdded != nil || files[1].SceneName != "" {
		t.Errorf("missing file facts decoded as present: %#v", files[1])
	}
}

func TestDeleteEpisodeFileUsesExactAuthenticatedEndpoint(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v3/episodefile/501" || r.URL.RawQuery != "" {
			t.Errorf("request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if got := r.Header.Get("X-Api-Key"); got != "delete-key" {
			t.Errorf("X-Api-Key = %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	if err := NewClient(server.URL, "delete-key").DeleteEpisodeFile(501); err != nil {
		t.Fatalf("DeleteEpisodeFile() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

// TestGetSeriesHistoryScopesToSeriesAndSeason pins the scoped-history read that
// makes months-old records reachable: it hits /history/series (server-side
// filtered, bare JSON array — no records envelope), asks for the series and
// episode context, and sends seasonNumber only when the caller named a season.
func TestGetSeriesHistoryScopesToSeriesAndSeason(t *testing.T) {
	var queries []url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v3/history/series" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		queries = append(queries, r.URL.Query())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id":9001,"episodeId":301,"seriesId":28,"eventType":"downloadFolderImported","downloadId":"SAB123","date":"2026-07-21T09:30:00Z","quality":{"quality":{"name":"WEBDL-1080p"}}},
			{"id":9000,"episodeId":301,"seriesId":28,"eventType":"grabbed","downloadId":"SAB123","date":"2026-07-21T09:00:00Z","data":{"releaseSource":"Rss"}}]`))
	}))
	t.Cleanup(server.Close)

	client := NewClient(server.URL, "key")
	records, err := client.GetSeriesHistory(28, 0, 0)
	if err != nil {
		t.Fatalf("GetSeriesHistory() error = %v", err)
	}
	if len(records) != 2 || records[0].ID != 9001 || records[0].EventType != "downloadFolderImported" ||
		records[0].Quality.Quality.Name != "WEBDL-1080p" || records[1].DownloadID != "SAB123" ||
		records[1].Data["releaseSource"] != "Rss" {
		t.Fatalf("records = %+v", records)
	}

	if _, err := client.GetSeriesHistory(28, 11, 0); err != nil {
		t.Fatalf("GetSeriesHistory(season) error = %v", err)
	}
	if len(queries) != 2 {
		t.Fatalf("requests = %d, want 2", len(queries))
	}
	for i, q := range queries {
		if q.Get("seriesId") != "28" || q.Get("includeSeries") != "true" || q.Get("includeEpisode") != "true" {
			t.Errorf("query[%d] = %v", i, q)
		}
	}
	if _, ok := queries[0]["seasonNumber"]; ok {
		t.Errorf("season 0 sent seasonNumber = %q, want the whole series", queries[0].Get("seasonNumber"))
	}
	if got := queries[1].Get("seasonNumber"); got != "11" {
		t.Errorf("seasonNumber = %q, want 11", got)
	}
}

// TestGetSeriesHistoryCapsClientSide pins pageSize as OUR cap, not Sonarr's:
// /history/series takes no page size, so the request must not invent one and
// the caller's bound is applied to the (newest-first) records it returned.
func TestGetSeriesHistoryCapsClientSide(t *testing.T) {
	var query url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":3},{"id":2},{"id":1}]`))
	}))
	t.Cleanup(server.Close)

	client := NewClient(server.URL, "key")
	records, err := client.GetSeriesHistory(28, 11, 2)
	if err != nil {
		t.Fatalf("GetSeriesHistory() error = %v", err)
	}
	if len(records) != 2 || records[0].ID != 3 || records[1].ID != 2 {
		t.Fatalf("capped records = %+v, want the two newest", records)
	}
	if _, ok := query["pageSize"]; ok {
		t.Errorf("request sent pageSize = %q to an unpaged endpoint", query.Get("pageSize"))
	}
	if _, ok := query["page"]; ok {
		t.Errorf("request sent page = %q to an unpaged endpoint", query.Get("page"))
	}

	all, err := client.GetSeriesHistory(28, 11, 0)
	if err != nil {
		t.Fatalf("GetSeriesHistory(uncapped) error = %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("uncapped records = %d, want all 3", len(all))
	}
}

// TestMarkHistoryFailedPostsGrabRecord pins the only route Sonarr offers for
// blocklisting an already-imported release: a bodiless POST to the grab's own
// history id.
func TestMarkHistoryFailedPostsGrabRecord(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/api/v3/history/failed/9000" || r.URL.RawQuery != "" {
			t.Errorf("request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if got := r.Header.Get("X-Api-Key"); got != "failed-key" {
			t.Errorf("X-Api-Key = %q", got)
		}
		if body, _ := io.ReadAll(r.Body); len(body) != 0 {
			t.Errorf("body = %q, want empty", body)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)

	if err := NewClient(server.URL, "failed-key").MarkHistoryFailed(9000); err != nil {
		t.Fatalf("MarkHistoryFailed() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

// TestGetFailedDownloadPolicyReadsInstanceSetting pins where the replacement
// decision comes from: the instance's own download-client config, not us. Each
// case sets autoRedownloadFailedFromInteractiveSearch to the OPPOSITE value —
// it is a neighbouring setting about a different trigger, and a reader that
// grabbed it instead would otherwise pass.
func TestGetFailedDownloadPolicyReadsInstanceSetting(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"enabled", `{"enableCompletedDownloadHandling":true,"autoRedownloadFailed":true,"autoRedownloadFailedFromInteractiveSearch":false}`, true},
		{"disabled", `{"enableCompletedDownloadHandling":true,"autoRedownloadFailed":false,"autoRedownloadFailedFromInteractiveSearch":true}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v3/config/downloadclient" || r.URL.RawQuery != "" {
					t.Errorf("request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(server.Close)

			autoRedownload, err := NewClient(server.URL, "key").GetFailedDownloadPolicy()
			if err != nil {
				t.Fatalf("GetFailedDownloadPolicy() error = %v", err)
			}
			if autoRedownload != tc.want {
				t.Errorf("autoRedownloadFailed = %v, want %v", autoRedownload, tc.want)
			}
		})
	}
}
