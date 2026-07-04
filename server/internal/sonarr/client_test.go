package sonarr

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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
