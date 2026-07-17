package request

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeSonarrTV simulates the Sonarr endpoints the TV request/status flows
// touch and records what was written. It complements fakeSonarr in
// service_monitor_test.go, which drives the monitoring helpers directly; this
// fake serves the full CreateMediaRequest / getTVStatus surface (library
// lookup by tvdbId, series lookup, add, episodes, monitoring, commands).
type fakeSonarrTV struct {
	libraryJSON  string            // GET /api/v3/series?tvdbId= body; "" means not in library
	seriesJSON   string            // GET /api/v3/series/42 body (UpdateSeriesMonitoring round-trip)
	lookupJSON   string            // GET /api/v3/series/lookup body
	episodesJSON map[string]string // GET /api/v3/episode keyed by seasonNumber query ("" = all)
	episodesFail bool              // force the episode fetch to fail (statistics fallback)

	addBody     map[string]any
	seriesPut   map[string]any
	monitorBody map[string]any
	commands    []map[string]any
}

func (f *fakeSonarrTV) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/series":
			body := f.libraryJSON
			if body == "" {
				body = "[]"
			}
			_, _ = w.Write([]byte(body))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/series/lookup":
			_, _ = w.Write([]byte(f.lookupJSON))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/series/42":
			_, _ = w.Write([]byte(f.seriesJSON))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v3/series/42":
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &f.seriesPut)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":2,"name":"Any"},{"id":9,"name":"HD-1080p"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/rootfolder":
			_, _ = w.Write([]byte(`[{"id":1,"path":"/tv"}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/series":
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &f.addBody)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/episode":
			if f.episodesFail {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(f.episodesJSON[r.URL.Query().Get("seasonNumber")]))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v3/episode/monitor":
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &f.monitorBody)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
			var cmd map[string]any
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &cmd)
			f.commands = append(f.commands, cmd)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected sonarr request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func newFakeSonarrServer(t *testing.T, f *fakeSonarrTV) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	return srv
}

// TestCreateTVRequestAddsSeriesWithCoarseScope covers the auto-approve add of
// a series not yet in Sonarr: the payload carries the effective per-user
// quality profile, the first root folder, season folders, the coarse scope
// mapped onto addOptions.monitor, and an immediate missing-episode search. The
// supplied TVDB id must also be cached so later status checks skip the bridge.
func TestCreateTVRequestAddsSeriesWithCoarseScope(t *testing.T) {
	f := &fakeSonarrTV{
		lookupJSON: `[{"title":"Andor","tvdbId":121361,"year":2022,"seasons":[{"seasonNumber":1},{"seasonNumber":2}]}]`,
	}
	srv := newFakeSonarrServer(t, f)

	s, uid := newHistoryTestService(t, "", srv.URL, "")
	profile := 9
	if err := s.SetUserSettings(uid, UserSettingsDTO{QualityProfileSonarr: &profile}); err != nil {
		t.Fatalf("SetUserSettings: %v", err)
	}

	resp, err := s.CreateMediaRequest(uid, &CreateRequest{
		TmdbID:      1399,
		TvdbID:      121361,
		MediaType:   "tv",
		Title:       "andor (client title)",
		SeasonScope: SeasonScopeFirst,
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if resp.Status != StatusRequested || resp.Title != "Andor" {
		t.Fatalf("response = %+v, want requested/Andor", resp)
	}

	if f.addBody == nil {
		t.Fatal("AddSeries was not called")
	}
	if f.addBody["title"] != "Andor" || f.addBody["tvdbId"] != float64(121361) || f.addBody["year"] != float64(2022) {
		t.Errorf("add identity = %v/%v/%v, want Andor/121361/2022",
			f.addBody["title"], f.addBody["tvdbId"], f.addBody["year"])
	}
	if got := f.addBody["qualityProfileId"]; got != float64(9) {
		t.Errorf("qualityProfileId = %v, want 9 (admin-set per-user default)", got)
	}
	if f.addBody["rootFolderPath"] != "/tv" || f.addBody["monitored"] != true || f.addBody["seasonFolder"] != true {
		t.Errorf("add body = %#v, want /tv + monitored + seasonFolder", f.addBody)
	}
	addOptions, _ := f.addBody["addOptions"].(map[string]any)
	if addOptions["monitor"] != "firstSeason" || addOptions["searchForMissingEpisodes"] != true {
		t.Errorf("addOptions = %#v, want monitor firstSeason + search", addOptions)
	}
	if _, present := f.addBody["seasons"]; present {
		t.Errorf("seasons = %v, want omitted for a coarse-scope add", f.addBody["seasons"])
	}

	// The tmdb->tvdb mapping is cached so the status path never needs the bridge.
	var cachedTvdb int
	if err := s.db.QueryRow("SELECT tvdb_id FROM tmdb_tvdb_cache WHERE tmdb_id = 1399").Scan(&cachedTvdb); err != nil {
		t.Fatalf("read tmdb_tvdb_cache: %v", err)
	}
	if cachedTvdb != 121361 {
		t.Errorf("cached tvdb_id = %d, want 121361", cachedTvdb)
	}

	var status, scope string
	if err := s.db.QueryRow(
		"SELECT status, COALESCE(season_scope, '') FROM request_log WHERE user_id = ? AND tmdb_id = 1399 AND media_type = 'tv'", uid,
	).Scan(&status, &scope); err != nil {
		t.Fatalf("read request_log: %v", err)
	}
	if status != StatusRequested || scope != SeasonScopeFirst {
		t.Errorf("logged row = %s/%s, want requested/first", status, scope)
	}
}

// TestCreateTVRequestExplicitSeasonsNewSeries covers the explicit-season add:
// the payload must carry per-season monitored flags (only the chosen seasons
// true) with NO addOptions.monitor enum, and the stored season_scope must be
// the JSON-encoded season list so approval flows can replay it.
func TestCreateTVRequestExplicitSeasonsNewSeries(t *testing.T) {
	f := &fakeSonarrTV{
		lookupJSON: `[{"title":"Andor","tvdbId":121361,"year":2022,"seasons":[
			{"seasonNumber":0,"monitored":true},{"seasonNumber":1,"monitored":true},{"seasonNumber":2},{"seasonNumber":3}]}]`,
	}
	srv := newFakeSonarrServer(t, f)
	s, uid := newHistoryTestService(t, "", srv.URL, "")

	resp, err := s.CreateMediaRequest(uid, &CreateRequest{
		TmdbID:    1399,
		TvdbID:    121361,
		MediaType: "tv",
		Title:     "Andor",
		Seasons:   []int{3, 2, 3}, // unsorted + duplicate: must normalize to [2,3]
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if resp.Status != StatusRequested {
		t.Fatalf("status = %s, want requested", resp.Status)
	}

	addOptions, _ := f.addBody["addOptions"].(map[string]any)
	if _, present := addOptions["monitor"]; present {
		t.Errorf("addOptions.monitor = %v, want absent (season flags drive monitoring)", addOptions["monitor"])
	}
	if addOptions["searchForMissingEpisodes"] != true {
		t.Errorf("addOptions = %#v, want searchForMissingEpisodes true", addOptions)
	}
	seasons, _ := f.addBody["seasons"].([]any)
	if len(seasons) != 4 {
		t.Fatalf("seasons = %#v, want all 4 known seasons present", f.addBody["seasons"])
	}
	wantFlags := map[int]bool{0: false, 1: false, 2: true, 3: true}
	for _, raw := range seasons {
		m := raw.(map[string]any)
		n := int(m["seasonNumber"].(float64))
		if m["monitored"] != wantFlags[n] {
			t.Errorf("season %d monitored = %v, want %v", n, m["monitored"], wantFlags[n])
		}
	}

	var scope string
	if err := s.db.QueryRow(
		"SELECT COALESCE(season_scope, '') FROM request_log WHERE user_id = ? AND tmdb_id = 1399", uid,
	).Scan(&scope); err != nil {
		t.Fatalf("read request_log: %v", err)
	}
	if scope != "[2,3]" {
		t.Errorf("stored season_scope = %q, want [2,3]", scope)
	}
}

// TestCreateTVRequestExistingSeriesExplicitSeasons routes a "request more
// seasons" through the full CreateMediaRequest path: for a series already in
// Sonarr an explicit season list must NOT re-add the series; it monitors the
// chosen season (series flags + episodes) and fires a SeasonSearch.
func TestCreateTVRequestExistingSeriesExplicitSeasons(t *testing.T) {
	seriesObj := `{"id":42,"title":"Existing Show","tvdbId":121361,"monitored":true,"seasons":[
		{"seasonNumber":1,"monitored":true},{"seasonNumber":2,"monitored":false}]}`
	f := &fakeSonarrTV{
		libraryJSON: `[` + seriesObj + `]`,
		seriesJSON:  seriesObj,
		episodesJSON: map[string]string{
			"2": `[{"id":201,"seriesId":42,"seasonNumber":2,"episodeNumber":1,"monitored":false},
			       {"id":202,"seriesId":42,"seasonNumber":2,"episodeNumber":2,"monitored":false}]`,
		},
	}
	srv := newFakeSonarrServer(t, f)
	s, uid := newHistoryTestService(t, "", srv.URL, "")

	resp, err := s.CreateMediaRequest(uid, &CreateRequest{
		TmdbID:    1399,
		TvdbID:    121361,
		MediaType: "tv",
		Title:     "Existing Show",
		Seasons:   []int{2},
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if resp.Status != StatusRequested || resp.Title != "Existing Show" {
		t.Fatalf("response = %+v, want requested/Existing Show", resp)
	}
	if f.addBody != nil {
		t.Errorf("AddSeries called for an in-library series: %v", f.addBody)
	}

	// Season 2 must now be flagged monitored (season 1 untouched)...
	seasons, _ := f.seriesPut["seasons"].([]any)
	flags := map[int]bool{}
	for _, raw := range seasons {
		m := raw.(map[string]any)
		flags[int(m["seasonNumber"].(float64))] = m["monitored"].(bool)
	}
	if !flags[1] || !flags[2] {
		t.Errorf("series PUT season flags = %v, want both season 1 (kept) and 2 (added) monitored", flags)
	}
	// ...its episodes explicitly monitored...
	ids, _ := f.monitorBody["episodeIds"].([]any)
	if f.monitorBody["monitored"] != true || len(ids) != 2 {
		t.Errorf("episode monitor body = %#v, want both season-2 episodes monitored", f.monitorBody)
	}
	// ...and a SeasonSearch fired for it.
	if len(f.commands) != 1 || f.commands[0]["name"] != "SeasonSearch" ||
		f.commands[0]["seriesId"] != float64(42) || f.commands[0]["seasonNumber"] != float64(2) {
		t.Errorf("commands = %v, want one SeasonSearch for series 42 season 2", f.commands)
	}
}

// TestCreateTVRequestExistingSeriesPilotScope covers monitorPilot: a pilot
// request on an in-library series monitors exactly S1E1 (never the season
// flag, matching Sonarr's own pilot handling) and fires an EpisodeSearch.
func TestCreateTVRequestExistingSeriesPilotScope(t *testing.T) {
	f := &fakeSonarrTV{
		libraryJSON: `[{"id":42,"title":"Pilot Show","tvdbId":121361,"monitored":true,"seasons":[
			{"seasonNumber":1,"monitored":false}]}]`,
		episodesJSON: map[string]string{
			"1": `[{"id":301,"seriesId":42,"seasonNumber":1,"episodeNumber":1,"monitored":false},
			       {"id":302,"seriesId":42,"seasonNumber":1,"episodeNumber":2,"monitored":false}]`,
		},
	}
	srv := newFakeSonarrServer(t, f)
	s, uid := newHistoryTestService(t, "", srv.URL, "")

	resp, err := s.CreateMediaRequest(uid, &CreateRequest{
		TmdbID:      1399,
		TvdbID:      121361,
		MediaType:   "tv",
		Title:       "Pilot Show",
		SeasonScope: SeasonScopePilot,
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if resp.Status != StatusRequested || resp.Title != "Pilot Show" {
		t.Fatalf("response = %+v, want requested/Pilot Show", resp)
	}

	ids, _ := f.monitorBody["episodeIds"].([]any)
	if f.monitorBody["monitored"] != true || len(ids) != 1 || ids[0] != float64(301) {
		t.Errorf("episode monitor body = %#v, want only the pilot (301) monitored", f.monitorBody)
	}
	if f.seriesPut != nil {
		t.Errorf("series PUT = %v, want none (series already monitored; pilot never flips the season flag)", f.seriesPut)
	}
	if len(f.commands) != 1 || f.commands[0]["name"] != "EpisodeSearch" {
		t.Fatalf("commands = %v, want exactly one EpisodeSearch", f.commands)
	}
	cmdIDs, _ := f.commands[0]["episodeIds"].([]any)
	if len(cmdIDs) != 1 || cmdIDs[0] != float64(301) {
		t.Errorf("EpisodeSearch episodeIds = %v, want [301]", f.commands[0]["episodeIds"])
	}
}

// seedTvdbCache pre-seeds the tmdb->tvdb mapping so getTVStatus resolves the
// id from the 30-day DB cache and never needs the live tmdb.Bridge (which the
// tests leave nil — any bridge use would read unavailable and fail the
// assertions below).
func seedTvdbCache(t *testing.T, s *Service, tmdbID, tvdbID int) {
	t.Helper()
	if _, err := s.db.Exec("INSERT OR REPLACE INTO tmdb_tvdb_cache (tmdb_id, tvdb_id) VALUES (?, ?)", tmdbID, tvdbID); err != nil {
		t.Fatalf("seed tmdb_tvdb_cache: %v", err)
	}
}

// TestGetTVStatusFromEpisodeList covers the primary TV availability path: the
// per-season and series rollups derive from the real episode list (files vs
// aired), yielding available / partial / requested / unavailable seasons and a
// partial series with true progress — immune to Sonarr's monitored-only stats.
func TestGetTVStatusFromEpisodeList(t *testing.T) {
	f := &fakeSonarrTV{
		libraryJSON: `[{"id":7,"title":"Matrix Show","tvdbId":999,"monitored":true,"seasons":[
			{"seasonNumber":1,"monitored":true},
			{"seasonNumber":2,"monitored":true},
			{"seasonNumber":3,"monitored":true},
			{"seasonNumber":4,"monitored":false}]}]`,
		episodesJSON: map[string]string{
			// Season 1: fully downloaded. Season 2: one file, one aired-missing,
			// one unaired (excluded from "aired"). Season 3: monitored, aired,
			// nothing on disk. Season 4: unmonitored, aired, nothing on disk.
			"": `[
				{"id":1,"seriesId":7,"seasonNumber":1,"episodeNumber":1,"hasFile":true,"monitored":true},
				{"id":2,"seriesId":7,"seasonNumber":1,"episodeNumber":2,"hasFile":true,"monitored":true},
				{"id":3,"seriesId":7,"seasonNumber":2,"episodeNumber":1,"hasFile":true,"monitored":true},
				{"id":4,"seriesId":7,"seasonNumber":2,"episodeNumber":2,"hasFile":false,"monitored":true,"airDateUtc":"2020-01-01T00:00:00Z"},
				{"id":5,"seriesId":7,"seasonNumber":2,"episodeNumber":3,"hasFile":false,"monitored":true,"airDateUtc":"2100-01-01T00:00:00Z"},
				{"id":6,"seriesId":7,"seasonNumber":3,"episodeNumber":1,"hasFile":false,"monitored":true,"airDateUtc":"2020-06-01T00:00:00Z"},
				{"id":7,"seriesId":7,"seasonNumber":4,"episodeNumber":1,"hasFile":false,"monitored":false,"airDateUtc":"2020-06-01T00:00:00Z"}
			]`,
		},
	}
	srv := newFakeSonarrServer(t, f)
	s, uid := newHistoryTestService(t, "", srv.URL, "")
	seedTvdbCache(t, s, 300, 999)

	st, err := s.getTVStatus(uid, 300)
	if err != nil {
		t.Fatalf("getTVStatus: %v", err)
	}
	if st.Status != StatusPartial {
		t.Errorf("status = %s, want partial (3 of 6 aired episodes on disk)", st.Status)
	}
	if st.Progress != 0.5 {
		t.Errorf("progress = %v, want 0.5", st.Progress)
	}

	want := map[int]struct {
		status string
		files  int
		eps    int
	}{
		1: {StatusAvailable, 2, 2},
		2: {StatusPartial, 1, 2}, // unaired episode excluded from the aired count
		3: {StatusRequested, 0, 1},
		4: {StatusUnavailable, 0, 1},
	}
	if len(st.Seasons) != len(want) {
		t.Fatalf("got %d seasons, want %d: %+v", len(st.Seasons), len(want), st.Seasons)
	}
	for _, ss := range st.Seasons {
		w, ok := want[ss.SeasonNumber]
		if !ok {
			t.Errorf("unexpected season %d", ss.SeasonNumber)
			continue
		}
		if ss.Status != w.status || ss.EpisodeFileCount != w.files || ss.EpisodeCount != w.eps {
			t.Errorf("season %d = %s %d/%d, want %s %d/%d",
				ss.SeasonNumber, ss.Status, ss.EpisodeFileCount, ss.EpisodeCount, w.status, w.files, w.eps)
		}
	}
}

// TestGetTVStatusSeasonStatisticsFallback covers the branch taken when the
// episode fetch fails: availability falls back to seasons[].statistics using
// totalEpisodeCount (never the monitored-only episodeCount), so a
// partially-monitored season still reads partial.
func TestGetTVStatusSeasonStatisticsFallback(t *testing.T) {
	f := &fakeSonarrTV{
		libraryJSON: `[{"id":7,"title":"Stats Show","tvdbId":999,"monitored":true,"seasons":[
			{"seasonNumber":1,"monitored":true,"statistics":{"episodeFileCount":8,"episodeCount":8,"totalEpisodeCount":8}},
			{"seasonNumber":2,"monitored":true,"statistics":{"episodeFileCount":2,"episodeCount":2,"totalEpisodeCount":10,"percentOfEpisodes":100}}]}]`,
		episodesFail: true,
	}
	srv := newFakeSonarrServer(t, f)
	s, uid := newHistoryTestService(t, "", srv.URL, "")
	seedTvdbCache(t, s, 300, 999)

	st, err := s.getTVStatus(uid, 300)
	if err != nil {
		t.Fatalf("getTVStatus: %v", err)
	}
	if st.Status != StatusPartial {
		t.Errorf("status = %s, want partial (10 of 18 known episodes)", st.Status)
	}
	if st.Progress != 10.0/18.0 {
		t.Errorf("progress = %v, want %v", st.Progress, 10.0/18.0)
	}
	if len(st.Seasons) != 2 {
		t.Fatalf("got %d seasons, want 2: %+v", len(st.Seasons), st.Seasons)
	}
	for _, ss := range st.Seasons {
		switch ss.SeasonNumber {
		case 1:
			if ss.Status != StatusAvailable || ss.EpisodeFileCount != 8 || ss.EpisodeCount != 8 {
				t.Errorf("season 1 = %s %d/%d, want available 8/8", ss.Status, ss.EpisodeFileCount, ss.EpisodeCount)
			}
		case 2:
			// The percentOfEpisodes trap: 2/2 monitored episodes are done (100%)
			// but 10 episodes exist -> partial, counted against the true total.
			if ss.Status != StatusPartial || ss.EpisodeFileCount != 2 || ss.EpisodeCount != 10 {
				t.Errorf("season 2 = %s %d/%d, want partial 2/10", ss.Status, ss.EpisodeFileCount, ss.EpisodeCount)
			}
		default:
			t.Errorf("unexpected season %d", ss.SeasonNumber)
		}
	}
}

// TestGetTVStatusUnresolvable pins the unavailable outcomes: a cached mapping
// pointing at a series Sonarr doesn't have, and a missing mapping with no
// bridge configured (no HTTP may happen at all in that case).
func TestGetTVStatusUnresolvable(t *testing.T) {
	t.Run("not in sonarr", func(t *testing.T) {
		f := &fakeSonarrTV{} // empty library
		srv := newFakeSonarrServer(t, f)
		s, uid := newHistoryTestService(t, "", srv.URL, "")
		seedTvdbCache(t, s, 300, 999)
		st, err := s.getTVStatus(uid, 300)
		if err != nil || st.Status != StatusUnavailable {
			t.Errorf("status = %+v err=%v, want unavailable", st, err)
		}
	})

	t.Run("no tvdb mapping and no bridge", func(t *testing.T) {
		f := &fakeSonarrTV{} // any request would fail the test via t.Errorf
		srv := newFakeSonarrServer(t, f)
		s, uid := newHistoryTestService(t, "", srv.URL, "")
		st, err := s.getTVStatus(uid, 300)
		if err != nil || st.Status != StatusUnavailable {
			t.Errorf("status = %+v err=%v, want unavailable", st, err)
		}
	})
}
