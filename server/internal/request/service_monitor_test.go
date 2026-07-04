package request

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// fakeSonarr simulates the Sonarr endpoints the season-monitoring flow touches
// and records what was written.
type fakeSonarr struct {
	seriesJSON string

	seriesPut        map[string]any
	monitoredIDs     []int
	monitoredFlag    bool
	commands         []map[string]any
	episodesBySeason map[int][]sonarr.Episode
}

func (f *fakeSonarr) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/series/42":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(f.seriesJSON))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v3/series/42":
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &f.seriesPut)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/episode":
			season := 0
			_, _ = fmt.Sscanf(r.URL.Query().Get("seasonNumber"), "%d", &season)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(f.episodesBySeason[season])
		case r.Method == http.MethodPut && r.URL.Path == "/api/v3/episode/monitor":
			var body struct {
				EpisodeIDs []int `json:"episodeIds"`
				Monitored  bool  `json:"monitored"`
			}
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &body)
			f.monitoredIDs = append(f.monitoredIDs, body.EpisodeIDs...)
			f.monitoredFlag = body.Monitored
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
			var cmd map[string]any
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &cmd)
			f.commands = append(f.commands, cmd)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func (f *fakeSonarr) commandNames() []string {
	var names []string
	for _, c := range f.commands {
		if n, ok := c["name"].(string); ok {
			names = append(names, n)
		}
	}
	return names
}

// TestMonitorAndSearchSeasonsRequestMore reproduces the reported "request more"
// bug scenario: the season is already flagged monitored and holds two
// downloaded episodes, but the remaining episodes are unmonitored. The request
// must explicitly monitor those episodes (Sonarr's series update won't — the
// season flag doesn't change) and trigger a season search, without ever
// touching the destructive /seasonpass endpoint.
func TestMonitorAndSearchSeasonsRequestMore(t *testing.T) {
	f := &fakeSonarr{
		seriesJSON: `{"id": 42, "monitored": true, "path": "/tv/x", "qualityProfileId": 3,
			"seasons": [{"seasonNumber": 1, "monitored": true}, {"seasonNumber": 2, "monitored": false}]}`,
		episodesBySeason: map[int][]sonarr.Episode{
			1: {
				{ID: 101, SeasonNumber: 1, EpisodeNumber: 1, HasFile: true, Monitored: true},
				{ID: 102, SeasonNumber: 1, EpisodeNumber: 2, HasFile: true, Monitored: true},
				{ID: 103, SeasonNumber: 1, EpisodeNumber: 3, Monitored: false},
				{ID: 104, SeasonNumber: 1, EpisodeNumber: 4, Monitored: false},
			},
		},
	}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	client := sonarr.NewClient(srv.URL, "key")
	series := &sonarr.Series{
		ID:        42,
		Monitored: true,
		Seasons: []sonarr.SeasonResource{
			{SeasonNumber: 1, Monitored: true},
			{SeasonNumber: 2, Monitored: false},
		},
	}

	svc := &Service{}
	if err := svc.monitorAndSearchSeasons(client, series, []int{1}); err != nil {
		t.Fatalf("monitorAndSearchSeasons: %v", err)
	}

	// The remaining episodes of season 1 must now be monitored.
	if !f.monitoredFlag || len(f.monitoredIDs) != 2 || f.monitoredIDs[0] != 103 || f.monitoredIDs[1] != 104 {
		t.Errorf("monitored episodes = %v (flag %v), want [103 104] true", f.monitoredIDs, f.monitoredFlag)
	}

	// A season search must have been triggered for the chosen season.
	names := f.commandNames()
	if len(names) != 1 || names[0] != "SeasonSearch" {
		t.Errorf("commands = %v, want exactly one SeasonSearch", names)
	}
	if got := f.commands[0]["seasonNumber"]; got != float64(1) {
		t.Errorf("SeasonSearch seasonNumber = %v, want 1", got)
	}

	// The series update must keep the series monitored, keep season 1
	// monitored, and leave season 2 alone.
	if f.seriesPut["monitored"] != true {
		t.Errorf("series PUT monitored = %v, want true", f.seriesPut["monitored"])
	}
	seasons := f.seriesPut["seasons"].([]any)
	flags := map[int]bool{}
	for _, s := range seasons {
		m := s.(map[string]any)
		flags[int(m["seasonNumber"].(float64))] = m["monitored"].(bool)
	}
	if !flags[1] || flags[2] {
		t.Errorf("series PUT season flags = %v, want season 1 monitored, season 2 not", flags)
	}
}

// TestRequestExistingSeriesRevivesDormant covers the coarse-scope path on a
// series already in Sonarr: previously a silent no-op, a request must revive a
// dormant (unmonitored) series by monitoring the scope's seasons + episodes and
// searching, and report "requested".
func TestRequestExistingSeriesRevivesDormant(t *testing.T) {
	f := &fakeSonarr{
		seriesJSON: `{"id": 42, "monitored": false,
			"seasons": [{"seasonNumber": 1, "monitored": false}]}`,
		episodesBySeason: map[int][]sonarr.Episode{
			1: {
				{ID: 101, SeasonNumber: 1, EpisodeNumber: 1, Monitored: false},
				{ID: 102, SeasonNumber: 1, EpisodeNumber: 2, Monitored: false},
			},
		},
	}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	client := sonarr.NewClient(srv.URL, "key")
	series := &sonarr.Series{
		ID:        42,
		Title:     "Dormant Show",
		Monitored: false,
		Seasons: []sonarr.SeasonResource{
			{SeasonNumber: 1, Monitored: false, Statistics: &sonarr.SeasonStatistics{TotalEpisodeCount: 2}},
		},
	}

	svc := &Service{}
	status, title, err := svc.requestExistingSeries(client, series, &resolvedRequest{seasonScope: SeasonScopeAll})
	if err != nil {
		t.Fatalf("requestExistingSeries: %v", err)
	}
	if status != StatusRequested || title != "Dormant Show" {
		t.Errorf("status/title = %q/%q, want requested/Dormant Show", status, title)
	}
	if f.seriesPut["monitored"] != true {
		t.Errorf("series PUT monitored = %v, want true", f.seriesPut["monitored"])
	}
	if len(f.monitoredIDs) != 2 {
		t.Errorf("monitored episodes = %v, want both episodes", f.monitoredIDs)
	}
	if names := f.commandNames(); len(names) != 1 || names[0] != "SeasonSearch" {
		t.Errorf("commands = %v, want exactly one SeasonSearch", names)
	}
}

// TestRequestExistingSeriesFullyAvailableNoOp: when every season already has
// all its files, a coarse re-request must not fire searches and must report the
// live availability.
func TestRequestExistingSeriesFullyAvailableNoOp(t *testing.T) {
	f := &fakeSonarr{
		seriesJSON: `{"id": 42, "monitored": true, "seasons": [{"seasonNumber": 1, "monitored": true}]}`,
	}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	client := sonarr.NewClient(srv.URL, "key")
	series := &sonarr.Series{
		ID:         42,
		Title:      "Complete Show",
		Monitored:  true,
		Statistics: &sonarr.SeriesStatistics{EpisodeFileCount: 10, EpisodeCount: 10, TotalEpisodeCount: 10, PercentOfEpisodes: 100},
		Seasons: []sonarr.SeasonResource{
			{SeasonNumber: 1, Monitored: true, Statistics: &sonarr.SeasonStatistics{EpisodeFileCount: 10, EpisodeCount: 10, TotalEpisodeCount: 10}},
		},
	}

	svc := &Service{}
	status, _, err := svc.requestExistingSeries(client, series, &resolvedRequest{seasonScope: SeasonScopeAll})
	if err != nil {
		t.Fatalf("requestExistingSeries: %v", err)
	}
	if status != StatusAvailable {
		t.Errorf("status = %q, want available", status)
	}
	if len(f.commands) != 0 {
		t.Errorf("commands = %v, want none for a fully available series", f.commands)
	}
	if f.seriesPut != nil {
		t.Errorf("series PUT = %v, want no write for a fully available series", f.seriesPut)
	}
}
