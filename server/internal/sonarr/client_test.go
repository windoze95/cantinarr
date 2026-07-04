package sonarr

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
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
