package request

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
	"github.com/windoze95/cantinarr-server/internal/mediaavailability"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

type fakeMediaAvailability struct {
	onLookup func()
	mu       sync.Mutex
	result   mediaavailability.Result
	fresh    []bool
}

func (f *fakeMediaAvailability) Lookup(_ context.Context, _ int64, _ mediaserver.ItemQuery, fresh bool) (mediaavailability.Result, error) {
	f.mu.Lock()
	f.fresh = append(f.fresh, fresh)
	result, hook := f.result, f.onLookup
	f.onLookup = nil
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return result, nil
}
func (f *fakeMediaAvailability) set(result mediaavailability.Result) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.result = result
}
func knownMedia(episodes ...int) mediaavailability.Result {
	out := mediaavailability.Result{Enabled: true, Known: true}
	for _, n := range episodes {
		out.Episodes = append(out.Episodes, mediaavailability.Episode{TMDBID: int64(20000 + n), TVDBID: int64(10000 + n), SeasonNumber: 1, EpisodeNumber: n})
	}
	return out
}

func TestMovieMediaAvailabilityRequestModes(t *testing.T) {
	for _, mode := range []string{RequestModeMissing, RequestModeLibraryCopy} {
		t.Run(mode, func(t *testing.T) {
			f := &fakeRadarr{lookupJSON: `[{"title":"Movie","tmdbId":550,"year":1999}]`}
			srv := newFakeRadarrServer(t, f)
			s, uid := newHistoryTestService(t, srv.URL, "", "")
			source := &fakeMediaAvailability{result: mediaavailability.Result{Enabled: true, Known: true, Movie: true}}
			s.SetMediaAvailability(source)
			out, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 550, MediaType: "movie", Title: "Movie", RequestMode: mode})
			if err != nil {
				t.Fatal(err)
			}
			if mode == RequestModeMissing {
				if out.Status != StatusAvailable || out.Availability == nil || f.addBody != nil {
					t.Fatalf("covered request dispatched: %+v, add=%v", out, f.addBody)
				}
				var count int
				s.db.QueryRow(`SELECT COUNT(*) FROM request_log`).Scan(&count)
				if count != 0 {
					t.Fatalf("covered no-op persisted %d requests", count)
				}
				if !slices.Contains(source.fresh, true) {
					t.Fatal("suppression did not use live evidence")
				}
			} else if f.addBody == nil || out.Status != StatusRequested {
				t.Fatalf("explicit library copy not dispatched: %+v", out)
			}
		})
	}
}

func TestMovieUnknownAvailabilityBlocksDefaultOnly(t *testing.T) {
	f := &fakeRadarr{lookupJSON: `[{"title":"Movie","tmdbId":550}]`}
	srv := newFakeRadarrServer(t, f)
	s, uid := newHistoryTestService(t, srv.URL, "", "")
	s.SetMediaAvailability(&fakeMediaAvailability{result: mediaavailability.Result{Enabled: true}})
	_, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 550, MediaType: "movie"})
	if !errors.Is(err, ErrMediaAvailabilityUnknown) || f.addBody != nil {
		t.Fatalf("unknown evidence allowed write: %v", err)
	}
}

func TestMediaCoverageUsesCanonicalEpisodeIdentity(t *testing.T) {
	canonical := []CanonicalEpisode{{TMDBID: 201, TVDBID: 101, SeasonNumber: 1, EpisodeNumber: 1}, {TMDBID: 202, TVDBID: 102, SeasonNumber: 1, EpisodeNumber: 2}}
	tests := []struct {
		name  string
		items []mediaavailability.Episode
		want  int
		known bool
	}{
		{"different native numbering", []mediaavailability.Episode{{TMDBID: 201, SeasonNumber: 3, EpisodeNumber: 4}}, 1, true},
		{"duplicate copies union", []mediaavailability.Episode{{TVDBID: 101}, {TMDBID: 201}}, 1, true},
		{"unknown numbering", []mediaavailability.Episode{{SeasonNumber: 1, EpisodeNumber: 1, SeriesTMDBID: 200}}, 0, false},
		{"explicit TMDB numbering", []mediaavailability.Episode{{SeasonNumber: 1, EpisodeNumber: 1, SeriesTMDBID: 200, Numbering: "tmdb"}}, 1, true},
		{"conflicting external ids", []mediaavailability.Episode{{TMDBID: 201, TVDBID: 102}}, 0, false},
		{"confirmed specials do not pollute ordinary scope", []mediaavailability.Episode{{TMDBID: 999, Numbering: "tmdb", SeriesTMDBID: 200, SeasonNumber: 0}, {TMDBID: 201}}, 1, true},
		{"unproven native season zero remains unknown", []mediaavailability.Episode{{TVDBID: 999, SeasonNumber: 0}}, 0, false},
		{"multi-provider split coverage", []mediaavailability.Episode{{TMDBID: 201, InstanceID: "plex"}, {TVDBID: 102, InstanceID: "emby"}, {TMDBID: 201, InstanceID: "jellyfin"}}, 2, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			covered, _, known := mediaEpisodeCoverage(200, canonical, mediaavailability.Result{Known: true, Episodes: tt.items})
			if len(covered) != tt.want || known != tt.known {
				t.Fatalf("coverage %v known %v", covered, known)
			}
		})
	}
	_, err := resolveCanonicalEpisode(canonical[0], 42, []sonarr.Episode{{ID: 1, SeriesID: 42, TVDBID: 101}, {ID: 2, SeriesID: 42, TVDBID: 101}})
	if err == nil {
		t.Fatal("ambiguous native episode mapping accepted")
	}
}

// This fixture preserves unrelated native monitoring and records every write.
// Canonical episode IDs differ from Sonarr record IDs and native numbering.
type missingTVFixture struct {
	missingBinding      map[int]bool
	onMonitor           func()
	commandsCompleted   bool
	findCalls           int
	queueUnavailable    bool
	episodesUnavailable bool
	episodesPayload     string
	commandHistory      bool
	commandTimes        []time.Time
	unavailable         bool
	mu                  sync.Mutex
	exists, monitored   bool
	count               int
	future              map[int]bool
	airDates            map[int]string
	files               map[int]bool
	monitoredEpisodes   map[int]bool
	adds                []sonarr.AddSeriesRequest
	monitorWrites       [][]int
	commands            [][]int
	uncertain           bool
	externalCalls       int
}

func newMissingTVFixture(t *testing.T) (*Service, int64, *missingTVFixture, *fakeMediaAvailability) {
	t.Helper()
	f := &missingTVFixture{count: 6, future: map[int]bool{}, files: map[int]bool{}, monitoredEpisodes: map[int]bool{201: true}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if f.unavailable {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		series := func() map[string]any {
			return map[string]any{"id": 42, "tvdbId": 500, "tmdbId": 200, "title": "Show", "year": 2020, "monitored": f.monitored, "qualityProfileId": 7, "path": "/tv/Show", "monitorNewItems": "none", "seasons": []map[string]any{{"seasonNumber": 1, "monitored": false}, {"seasonNumber": 2, "monitored": true}}}
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v3/series":
			if f.exists {
				json.NewEncoder(w).Encode([]any{series()})
			} else {
				w.Write([]byte(`[]`))
			}
		case r.Method == "GET" && r.URL.Path == "/api/v3/series/lookup":
			json.NewEncoder(w).Encode([]any{map[string]any{"tvdbId": 500, "title": "Show", "year": 2020, "seasons": []map[string]any{{"seasonNumber": 1}, {"seasonNumber": 2}}}})
		case r.Method == "GET" && r.URL.Path == "/api/v3/series/42":
			json.NewEncoder(w).Encode(series())
		case r.Method == "GET" && r.URL.Path == "/api/v3/qualityprofile":
			w.Write([]byte(`[{"id":7,"name":"Any"}]`))
		case r.Method == "GET" && r.URL.Path == "/api/v3/rootfolder":
			w.Write([]byte(`[{"id":1,"path":"/tv"}]`))
		case r.Method == "POST" && r.URL.Path == "/api/v3/series":
			var add sonarr.AddSeriesRequest
			json.NewDecoder(r.Body).Decode(&add)
			f.adds = append(f.adds, add)
			f.exists = true
			f.monitored = add.Monitored
			json.NewEncoder(w).Encode(series())
		case r.Method == "PUT" && r.URL.Path == "/api/v3/series/42":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			f.monitored, _ = body["monitored"].(bool)
			if body["qualityProfileId"] != float64(7) || body["path"] != "/tv/Show" {
				t.Errorf("unrelated native settings overwritten: %v", body)
			}
			seasons, _ := body["seasons"].([]any)
			if len(seasons) != 2 || seasons[0].(map[string]any)["monitored"] != false || seasons[1].(map[string]any)["monitored"] != true {
				t.Errorf("broad season monitoring changed: %v", seasons)
			}
			json.NewEncoder(w).Encode(series())
		case r.Method == "GET" && r.URL.Path == "/api/v3/episode":
			if f.episodesUnavailable {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			if f.episodesPayload != "" {
				_, _ = w.Write([]byte(f.episodesPayload))
				return
			}
			episodes := []map[string]any{}
			for n := 1; n <= f.count; n++ {
				air := "2020-01-01T00:00:00Z"
				if f.future[n] {
					air = "2099-01-01T00:00:00Z"
				}
				episodes = append(episodes, map[string]any{"id": 100 + n, "seriesId": 42, "tvdbId": 10000 + n, "seasonNumber": 1, "episodeNumber": n, "monitored": f.monitoredEpisodes[100+n], "hasFile": f.files[n], "airDateUtc": air})
			}
			episodes = append(episodes, map[string]any{"id": 201, "seriesId": 42, "tvdbId": 10201, "seasonNumber": 2, "episodeNumber": 1, "monitored": true, "hasFile": false, "airDateUtc": "2020-01-01T00:00:00Z"})
			json.NewEncoder(w).Encode(episodes)
		case r.Method == "PUT" && r.URL.Path == "/api/v3/episode/monitor":
			var body struct {
				EpisodeIDs []int `json:"episodeIds"`
				Monitored  bool  `json:"monitored"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			f.monitorWrites = append(f.monitorWrites, body.EpisodeIDs)
			if f.onMonitor != nil {
				hook := f.onMonitor
				f.onMonitor = nil
				hook()
			}
			for _, id := range body.EpisodeIDs {
				f.monitoredEpisodes[id] = body.Monitored
			}
			w.Write([]byte(`[]`))
		case r.Method == "GET" && r.URL.Path == "/api/v3/queue":
			if f.queueUnavailable {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.Write([]byte(`{"totalRecords":0,"records":[]}`))
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/v3/command/"):
			id, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/v3/command/"))
			if id < 1 || id > len(f.commands) {
				http.NotFound(w, r)
				return
			}
			state := "started"
			if f.commandsCompleted {
				state = "completed"
			}
			json.NewEncoder(w).Encode(map[string]any{"id": id, "name": "EpisodeSearch", "status": state, "body": map[string]any{"episodeIds": f.commands[id-1]}})
		case r.Method == "GET" && r.URL.Path == "/api/v3/command":
			commands := []map[string]any{}
			if f.commandHistory {
				for i, ids := range f.commands {
					commands = append(commands, map[string]any{"id": i + 1, "name": "EpisodeSearch", "queued": f.commandTimes[i], "body": map[string]any{"episodeIds": ids}})
				}
			}
			json.NewEncoder(w).Encode(commands)
		case r.Method == "POST" && r.URL.Path == "/api/v3/command":
			var body struct {
				Name       string `json:"name"`
				EpisodeIDs []int  `json:"episodeIds"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.Name != "EpisodeSearch" {
				t.Errorf("broad search %q", body.Name)
			}
			f.commands = append(f.commands, body.EpisodeIDs)
			f.commandTimes = append(f.commandTimes, time.Now().UTC())
			if f.uncertain {
				w.Write([]byte(`{}`))
			} else {
				json.NewEncoder(w).Encode(map[string]int{"id": len(f.commands)})
			}
		default:
			t.Errorf("unexpected Sonarr %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	s, uid := newHistoryTestService(t, "", srv.URL, "")
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/tv/200":
			json.NewEncoder(w).Encode(map[string]any{"id": 200, "name": "Show", "first_air_date": "2020-01-01", "seasons": []map[string]int{{"season_number": 1, "episode_count": f.count}, {"season_number": 2, "episode_count": 1}}})
		case strings.HasPrefix(r.URL.Path, "/find/"):
			f.findCalls++
			id, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/find/"))
			season, canonical := 1, id+10000
			if id == 10201 {
				season, canonical = 2, 20201
			}
			if id == 9999 {
				season, canonical = 0, 29999
			}
			json.NewEncoder(w).Encode(map[string]any{"tv_episode_results": []map[string]int{{"id": canonical, "show_id": 200, "season_number": season}}})
		case r.URL.Path == "/tv/200/season/0":
			w.Write([]byte(`{"season_number":0,"episodes":[{"id":29999,"season_number":0,"episode_number":1,"air_date":"2020-01-01"}]}`))
		case r.URL.Path == "/tv/200/external_ids":
			w.Write([]byte(`{"id":200,"tvdb_id":500}`))
		case strings.Contains(r.URL.Path, "/episode/") && strings.HasSuffix(r.URL.Path, "/external_ids"):
			var season, n int
			fmt.Sscanf(r.URL.Path, "/tv/200/season/%d/episode/%d/external_ids", &season, &n)
			f.externalCalls++
			if f.missingBinding[n] && season == 1 {
				fmt.Fprintf(w, `{"id":%d,"tvdb_id":null}`, 20000+n)
				return
			}
			id, tid := 20000+n, 10000+n
			if season == 2 {
				id, tid = 20201, 10201
			}
			fmt.Fprintf(w, `{"id":%d,"tvdb_id":%d}`, id, tid)
		case r.URL.Path == "/tv/200/season/1" || r.URL.Path == "/tv/200/season/2":
			season := 1
			count := f.count
			if strings.HasSuffix(r.URL.Path, "/2") {
				season, count = 2, 1
			}
			episodes := []map[string]any{}
			for n := 1; n <= count; n++ {
				id := 20000 + n
				if season == 2 {
					id = 20201
				}
				air := "2020-01-01"
				if f.future[n] && season == 1 {
					air = "2099-01-01"
				}
				if override, ok := f.airDates[n]; ok && season == 1 {
					air = override
				}
				episodes = append(episodes, map[string]any{"id": id, "season_number": season, "episode_number": n, "air_date": air})
			}
			json.NewEncoder(w).Encode(map[string]any{"id": season, "season_number": season, "episodes": episodes})
		default:
			t.Errorf("unexpected TMDB %s", r.URL.String())
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(api.Close)
	s.bridge = tmdb.NewBridge(bridgeClients{tmdb.NewClientWithBaseURL("test", api.URL)}, s.db)
	if err := s.SetGlobalSettings(GlobalSettings{AllowSeasonChoice: true, DefaultSeasonScope: SeasonScopeAll}); err != nil {
		t.Fatal(err)
	}
	source := &fakeMediaAvailability{result: knownMedia(1, 2, 3, 4)}
	s.SetMediaAvailability(source)
	return s, uid, f, source
}
func createMissingTV(t *testing.T, s *Service, uid int64) *CreateResponse {
	t.Helper()
	out, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 200, MediaType: "tv", Title: "Show", Seasons: []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func retryMissingTV(t *testing.T, s *Service) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE request_dispatch SET next_attempt_at=0`); err != nil {
		t.Fatal(err)
	}
	s.SweepDispatch(context.Background())
}
func followMissingTV(t *testing.T, s *Service) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE request_tv_follows SET next_attempt_at=0`); err != nil {
		t.Fatal(err)
	}
	s.SweepTVFollows(context.Background())
}
func TestMissingTVExactEpisodesAndDurableFollow(t *testing.T) {
	s, uid, f, source := newMissingTVFixture(t)
	out := createMissingTV(t, s, uid)
	if len(f.adds) != 1 {
		t.Fatalf("expected safe add, got %+v", out)
	}
	add := f.adds[0]
	if add.Monitored || add.MonitorNewItems != "none" || add.AddOptions.Monitor != "none" || add.AddOptions.SearchForMissingEpisodes {
		t.Fatalf("unsafe new-series defaults: %+v", add)
	}
	for _, season := range add.Seasons {
		if season.Monitored {
			t.Fatal("new series enabled whole season")
		}
	}
	retryMissingTV(t, s)
	if fmt.Sprint(f.commands) != "[[105] [106]]" || fmt.Sprint(f.monitorWrites) != "[[105] [106]]" {
		t.Fatalf("missing-only writes: monitor=%v search=%v", f.monitorWrites, f.commands)
	}
	// A removed originally covered episode remains excluded. New selected-season
	// episodes get independent work; season 2 remains outside the follow.
	source.set(knownMedia())
	f.mu.Lock()
	f.count = 7
	f.mu.Unlock()
	followMissingTV(t, s)
	if fmt.Sprint(f.commands) != "[[105] [106] [107]]" {
		t.Fatalf("future work not scoped/deduped: %v", f.commands)
	}
	followMissingTV(t, s)
	if len(f.commands) != 3 {
		t.Fatalf("replayed submitted search: %v", f.commands)
	}
	var state string
	s.db.QueryRow(`SELECT state FROM request_tv_follow_episodes WHERE request_id=? AND episode_key='20001'`, out.RequestID).Scan(&state)
	if state != "excluded" {
		t.Fatalf("original coverage changed: %s", state)
	}
	stopped, err := s.StopTVFollow(uid, out.RequestID)
	if err != nil || stopped.State != "stopped" {
		t.Fatalf("stop: %+v %v", stopped, err)
	}
	f.mu.Lock()
	f.count = 8
	f.mu.Unlock()
	followMissingTV(t, s)
	if len(f.commands) != 3 {
		t.Fatal("stopped follow scheduled work")
	}
}
func TestMissingTVDoesNotMonitorUnairedEpisodes(t *testing.T) {
	s, uid, f, _ := newMissingTVFixture(t)
	f.future[6] = true
	createMissingTV(t, s, uid)
	retryMissingTV(t, s)
	if fmt.Sprint(f.commands) != "[[105]]" || f.monitoredEpisodes[106] {
		t.Fatalf("unaired work enabled Sonarr RSS: commands=%v monitor=%v", f.commands, f.monitoredEpisodes)
	}
	f.mu.Lock()
	delete(f.future, 6)
	f.mu.Unlock()
	followMissingTV(t, s)
	if fmt.Sprint(f.commands) != "[[105] [106]]" {
		t.Fatalf("newly aired episode not delivered: %v", f.commands)
	}
}
func TestMissingTVUncertainSearchNeverReplays(t *testing.T) {
	s, uid, f, _ := newMissingTVFixture(t)
	f.uncertain = true
	out := createMissingTV(t, s, uid)
	retryMissingTV(t, s)
	retryMissingTV(t, s)
	followMissingTV(t, s)
	if len(f.commands) != 1 {
		t.Fatalf("uncertain search replayed: %v", f.commands)
	}
	var state string
	s.db.QueryRow(`SELECT state FROM request_tv_follows WHERE request_id=?`, out.RequestID).Scan(&state)
	if state != "paused" {
		t.Fatalf("uncertain follow state %s", state)
	}
}
func TestMissingTVAllCoveredExplicitFollowAndManualMonitorPause(t *testing.T) {
	s, uid, f, source := newMissingTVFixture(t)
	source.set(knownMedia(1, 2, 3, 4, 5, 6))
	out := createMissingTV(t, s, uid)
	retryMissingTV(t, s)
	if out.RequestID == 0 || len(f.commands) != 0 {
		t.Fatalf("covered explicit follow not saved: %+v", out)
	}
	f.mu.Lock()
	f.count = 7
	f.mu.Unlock()
	followMissingTV(t, s)
	if fmt.Sprint(f.commands) != "[[107]]" {
		t.Fatalf("new episode missing: %v", f.commands)
	}
	f.mu.Lock()
	f.monitoredEpisodes[107] = false
	f.count = 8
	f.mu.Unlock()
	followMissingTV(t, s)
	var state string
	s.db.QueryRow(`SELECT state FROM request_tv_follows WHERE request_id=?`, out.RequestID).Scan(&state)
	if state != "paused" || len(f.commands) != 1 {
		t.Fatalf("external change overridden: state=%s commands=%v", state, f.commands)
	}
}
func TestMissingTVStopPreservesAcceptedInitialWork(t *testing.T) {
	s, uid, f, _ := newMissingTVFixture(t)
	out := createMissingTV(t, s, uid)
	if _, err := s.StopTVFollow(uid, out.RequestID); err != nil {
		t.Fatal(err)
	}
	retryMissingTV(t, s)
	if fmt.Sprint(f.commands) != "[[105] [106]]" {
		t.Fatalf("stop canceled accepted initial delivery: %v", f.commands)
	}
}
func TestAvailabilityProjectionPreservesArrStateAndAvoidsEpisodeIDFanout(t *testing.T) {
	s, uid, f, _ := newMissingTVFixture(t)
	out, err := s.GetUserStatus(uid, 200, "tv", "")
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != StatusUnavailable || out.Availability == nil || out.Availability.Status != StatusPartial || len(out.Availability.Seasons) != 2 {
		t.Fatalf("status projection: %+v", out)
	}
	if out.Availability.Seasons[0].EpisodeFileCount != 4 {
		t.Fatalf("episode union: %+v", out.Availability.Seasons)
	}
	if f.externalCalls != 0 {
		t.Fatalf("catalog read fanned out %d external-id calls", f.externalCalls)
	}
}

func TestEmptyTVAvailabilityDoesNotDependOnEpisodeAirDates(t *testing.T) {
	for _, tc := range []struct {
		name          string
		airDate       string
		providerKnown bool
	}{
		{"aired", "2020-01-01", true},
		{"future", "2099-01-01", true},
		{"missing date", "", true},
		{"invalid date", "not-a-date", true},
		{"unverified provider remains unknown", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, uid, f, source := newMissingTVFixture(t)
			f.airDates = map[int]string{1: tc.airDate}
			// Sonarr successfully returns no series, and the provider reports
			// no files. Only a verified provider read can prove absence.
			result := knownMedia()
			result.Known = tc.providerKnown
			source.set(result)
			out, err := s.GetUserStatus(uid, 200, "tv", "")
			if err != nil {
				t.Fatal(err)
			}
			if out.Status != StatusUnavailable || out.StatusKnown == nil || !*out.StatusKnown || out.Match == nil || out.Match.State != "resolved" {
				t.Fatalf("fixture must establish known Sonarr absence and a resolved match: %+v", out)
			}
			if out.Availability == nil || out.Availability.Status != StatusUnavailable || out.Availability.StatusKnown != tc.providerKnown {
				t.Fatalf("empty library availability must reflect provider verification, not episode dates: %+v", out.Availability)
			}
		})
	}
}

func TestTVUnknownDatesPreserveIncompleteEvidence(t *testing.T) {
	for _, scenario := range []string{"provider files", "Sonarr files", "paused match", "unreadable Sonarr"} {
		t.Run(scenario, func(t *testing.T) {
			s, uid, f, source := newMissingTVFixture(t)
			f.airDates = map[int]string{1: ""}
			source.set(knownMedia())
			switch scenario {
			case "provider files":
				source.set(knownMedia(2))
			case "Sonarr files":
				f.exists = true
				f.files[2] = true
			case "paused match":
				if _, err := s.db.Exec(`INSERT INTO tv_match_overrides(tmdb_id,mode,revision) VALUES(200,'paused',1)`); err != nil {
					t.Fatal(err)
				}
			case "unreadable Sonarr":
				f.unavailable = true
			}
			out, err := s.GetUserStatus(uid, 200, "tv", "")
			if err != nil {
				t.Fatal(err)
			}
			if out.Availability == nil || out.Availability.StatusKnown || out.Availability.Status == StatusAvailable {
				t.Fatalf("incomplete evidence must not become known absence or complete coverage: %+v", out.Availability)
			}
			if scenario == "provider files" && out.Availability.Status != StatusPartial {
				t.Fatalf("verified files must remain partial: %+v", out.Availability)
			}
			if scenario == "Sonarr files" && out.Status != StatusPartial {
				t.Fatalf("fixture must retain live Sonarr files: %+v", out)
			}
			if scenario == "paused match" || scenario == "unreadable Sonarr" {
				if out.StatusKnown == nil || *out.StatusKnown {
					t.Fatalf("failed match/library evidence became known: %+v", out)
				}
			}
		})
	}
}

func TestTVUnknownDatesRecheckEmptyLibraryAfterInitialStatus(t *testing.T) {
	for _, scenario := range []string{"still absent", "empty series", "empty episode list", "warm import", "cold import", "series read fails", "episode read fails", "null episode list"} {
		t.Run(scenario, func(t *testing.T) {
			s, uid, f, source := newMissingTVFixture(t)
			f.airDates = map[int]string{1: ""}
			source.set(knownMedia())
			out, err := s.userTVStatus(uid, 200, "")
			if err != nil {
				t.Fatal(err)
			}
			if out.Status != StatusUnavailable || out.StatusKnown == nil || !*out.StatusKnown {
				t.Fatalf("fixture must begin with verified Sonarr absence: %+v", out)
			}
			// The later coverage read must stand on its own, even when an
			// import's episode identities have never been cached before.
			if scenario == "warm import" {
				if _, err := s.canonicalEpisodes(200, []int{1, 2}, true); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "empty series":
				f.exists = true
			case "empty episode list":
				f.exists = true
				f.episodesPayload = "[]"
			case "warm import", "cold import":
				f.exists = true
				f.files[2] = true
			case "series read fails":
				f.unavailable = true
			case "episode read fails":
				f.exists = true
				f.episodesUnavailable = true
			case "null episode list":
				f.exists = true
				f.episodesPayload = "null"
			}
			if err := s.attachMediaAvailability(context.Background(), uid, 200, "tv", "", out); err != nil {
				t.Fatal(err)
			}
			wantKnown := scenario == "still absent" || scenario == "empty series" || scenario == "empty episode list"
			if out.Availability == nil || out.Availability.StatusKnown != wantKnown || out.Availability.Status == StatusAvailable {
				t.Fatalf("later files or failed reads must not reuse earlier absence: %+v", out.Availability)
			}
			if scenario == "warm import" && out.Availability.Status != StatusPartial {
				t.Fatalf("verified episode files must remain partial: %+v", out.Availability)
			}
		})
	}
}

// Keep date-only scheduling conservative; unknown air dates never trigger RSS.
func TestCanonicalEpisodeAired(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	for date, want := range map[string]bool{"2026-10-26": true, "2026-10-27": false, "": false, "not-a-date": false} {
		if got := (CanonicalEpisode{AirDate: date}).aired(now); got != want {
			t.Errorf("%s aired=%v", strconv.Quote(date), got)
		}
	}
}

func TestTVFollowingProjectionRespectsApprovalDecision(t *testing.T) {
	s, uid, _, _ := newMissingTVFixture(t)
	out := createMissingTV(t, s, uid)
	for _, tc := range []struct{ status, want string }{{StatusPending, "paused"}, {StatusDenied, "stopped"}} {
		if _, err := s.db.Exec(`UPDATE request_log SET status=?,park_reason=NULL WHERE id=?`, tc.status, out.RequestID); err != nil {
			t.Fatal(err)
		}
		rows, err := s.tvFollowing(uid, 200, "")
		if err != nil || len(rows) != 1 || rows[0].State != tc.want || rows[0].Message == "" {
			t.Fatalf("%s follow projection: %+v, %v", tc.status, rows, err)
		}
	}
}

func restartMissingService(s *Service, source *fakeMediaAvailability) *Service {
	next := NewService(s.db, s.registry, s.bridge, nil)
	next.SetMediaAvailability(source)
	next.SetContentPolicy(s.contentPolicy)
	return next
}
func TestMissingTVRestartAfterMonitorBeforeSearch(t *testing.T) {
	s, uid, f, source := newMissingTVFixture(t)
	createMissingTV(t, s, uid)
	f.mu.Lock()
	f.queueUnavailable = true
	f.mu.Unlock()
	retryMissingTV(t, s)
	if len(f.commands) != 0 || !f.monitoredEpisodes[105] {
		t.Fatalf("fixture did not stop after monitoring: commands=%v flags=%v", f.commands, f.monitoredEpisodes)
	}
	s = restartMissingService(s, source)
	f.mu.Lock()
	f.queueUnavailable = false
	f.mu.Unlock()
	retryMissingTV(t, s)
	if fmt.Sprint(f.commands) != "[[105] [106]]" || fmt.Sprint(f.monitorWrites) != "[[105] [106]]" {
		t.Fatalf("restart lost/repeated exact work: commands=%v monitor=%v", f.commands, f.monitorWrites)
	}
}
func TestMissingTVRestartReconcilesExactUncertainCommand(t *testing.T) {
	s, uid, f, source := newMissingTVFixture(t)
	f.uncertain = true
	out := createMissingTV(t, s, uid)
	retryMissingTV(t, s)
	if len(f.commands) != 1 {
		t.Fatalf("fixture did not reach uncertain search: %v", f.commands)
	}
	s = restartMissingService(s, source)
	f.mu.Lock()
	f.uncertain = false
	f.commandHistory = true
	f.mu.Unlock()
	retryMissingTV(t, s)
	if fmt.Sprint(f.commands) != "[[105] [106]]" {
		t.Fatalf("reconciled command was repeated: %v", f.commands)
	}
	var state string
	s.db.QueryRow(`SELECT state FROM request_tv_follows WHERE request_id=?`, out.RequestID).Scan(&state)
	if state != "active" {
		t.Fatalf("positive receipt did not restore follow: %s", state)
	}
}
func TestMissingTVRestartRetainsUncertainWithoutEvidence(t *testing.T) {
	s, uid, f, source := newMissingTVFixture(t)
	f.uncertain = true
	createMissingTV(t, s, uid)
	retryMissingTV(t, s)
	s = restartMissingService(s, source)
	retryMissingTV(t, s)
	if len(f.commands) != 1 {
		t.Fatalf("restart replayed uncertain search: %v", f.commands)
	}
}
func TestMissingTVScopeChangePausesBeforeAnyAdd(t *testing.T) {
	s, uid, f, source := newMissingTVFixture(t)
	first := knownMedia(1, 2, 3, 4)
	first.ScopeRevision = "scope-a"
	source.set(first)
	requireApproval(t, s)
	out := createMissingTV(t, s, uid)
	if len(f.adds) != 0 {
		t.Fatal("approval bypassed")
	}
	second := first
	second.ScopeRevision = "scope-b"
	source.set(second)
	// Grant approval directly to isolate dispatch's postapproval guard.
	s.db.Exec(`UPDATE request_log SET park_reason='delivery' WHERE id=?`, out.RequestID)
	s.db.Exec(`UPDATE request_dispatch SET state='queued' WHERE request_id=?`, out.RequestID)
	retryMissingTV(t, s)
	var state string
	s.db.QueryRow(`SELECT state FROM request_tv_follows WHERE request_id=?`, out.RequestID).Scan(&state)
	if len(f.adds) != 0 || state != "paused" {
		t.Fatalf("scope revocation added series: adds=%d state=%s", len(f.adds), state)
	}
	status, err := s.GetUserStatus(uid, 200, "tv", "")
	if err != nil || len(status.Following) == 0 {
		t.Fatalf("saved follow lost: %+v %v", status, err)
	}
	source.set(mediaavailability.Result{Known: true})
	status, err = s.GetUserStatus(uid, 200, "tv", "")
	if err != nil || len(status.Following) == 0 || status.Availability != nil {
		t.Fatalf("disabled sources hid stop control: %+v %v", status, err)
	}
}
func TestMissingTVGrantAndPolicyLossPause(t *testing.T) {
	for _, kind := range []string{"grant", "policy"} {
		t.Run(kind, func(t *testing.T) {
			s, uid, f, _ := newMissingTVFixture(t)
			out := createMissingTV(t, s, uid)
			retryMissingTV(t, s)
			f.mu.Lock()
			f.count = 7
			f.mu.Unlock()
			if kind == "grant" {
				s.db.Exec(`DELETE FROM user_instance_grants WHERE user_id=?`, uid)
			} else {
				policy := contentpolicy.New(s.db, func() contentpolicy.RawGetter { return &ratingsTMDB{} }, nil)
				s.SetContentPolicy(policy)
				if err := policy.Store.Set(uid, contentpolicy.Policy{MaxMovieRating: "PG", MaxTVRating: "TV-PG", RatingRegion: "US", BlockUnrated: true}); err != nil {
					t.Fatal(err)
				}
			}
			followMissingTV(t, s)
			var state string
			s.db.QueryRow(`SELECT state FROM request_tv_follows WHERE request_id=?`, out.RequestID).Scan(&state)
			if state != "paused" || len(f.commands) != 2 {
				t.Fatalf("%s loss still delivered: state=%s commands=%v", kind, state, f.commands)
			}
		})
	}
}
func TestMoviePendingModesNeverDedupeDistinctIntent(t *testing.T) {
	f := &fakeRadarr{lookupJSON: `[{"title":"Movie","tmdbId":550}]`}
	srv := newFakeRadarrServer(t, f)
	s, uid := newHistoryTestService(t, srv.URL, "", "")
	source := &fakeMediaAvailability{result: mediaavailability.Result{Enabled: true, Known: true, ScopeRevision: "original"}}
	s.SetMediaAvailability(source)
	requireApproval(t, s)
	missing, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 550, MediaType: "movie", Title: "Movie"})
	if err != nil {
		t.Fatal(err)
	}
	copy, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 550, MediaType: "movie", Title: "Movie", RequestMode: RequestModeLibraryCopy})
	if err != nil {
		t.Fatal(err)
	}
	if missing.RequestID == copy.RequestID || missing.RequestMode != RequestModeMissing || copy.RequestMode != RequestModeLibraryCopy {
		t.Fatalf("distinct intent conflated: %+v / %+v", missing, copy)
	}
	pending, err := s.ListPending()
	if err != nil || len(pending) != 2 {
		t.Fatalf("approval intents missing: %v %v", pending, err)
	}
	source.set(mediaavailability.Result{Enabled: true, Known: true, Movie: true, ScopeRevision: "original"})
	s.db.Exec(`UPDATE request_log SET park_reason='delivery' WHERE id IN (?,?)`, missing.RequestID, copy.RequestID)
	s.db.Exec(`UPDATE request_dispatch SET state='queued' WHERE request_id IN (?,?)`, missing.RequestID, copy.RequestID)
	retryMissingTV(t, s)
	if f.addBody == nil {
		t.Fatal("explicit pending copy was suppressed")
	}
	var code string
	s.db.QueryRow(`SELECT code FROM request_dispatch WHERE request_id=?`, missing.RequestID).Scan(&code)
	if code != StatusAvailable {
		t.Fatalf("missing intent was not freshly suppressed: %s", code)
	}
}
func TestMovieNoInitialSourcesRetainsMissingIntentWithoutCoverageRequirement(t *testing.T) {
	f := &fakeRadarr{lookupJSON: `[{"title":"Movie","tmdbId":550}]`}
	srv := newFakeRadarrServer(t, f)
	s, uid := newHistoryTestService(t, srv.URL, "", "")
	source := &fakeMediaAvailability{result: mediaavailability.Result{Known: true}}
	s.SetMediaAvailability(source)
	requireApproval(t, s)
	out, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 550, MediaType: "movie", Title: "Movie"})
	if err != nil {
		t.Fatal(err)
	}
	var mode string
	var required bool
	s.db.QueryRow(`SELECT mode,requires_media_coverage FROM request_media_modes WHERE request_id=?`, out.RequestID).Scan(&mode, &required)
	if mode != RequestModeMissing || required {
		t.Fatalf("intent misrepresented: %s/%v", mode, required)
	}
	again, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 550, MediaType: "movie", Title: "Movie"})
	if err != nil || again.RequestID != out.RequestID {
		t.Fatalf("same mode not deduped: %+v %v", again, err)
	}
}
func TestAvailableMediaSurvivesSonarrOutage(t *testing.T) {
	s, uid, f, source := newMissingTVFixture(t)
	data := knownMedia(1, 2, 3, 4, 5, 6)
	data.Episodes = append(data.Episodes, mediaavailability.Episode{TMDBID: 20201})
	source.set(data)
	f.mu.Lock()
	f.unavailable = true
	f.mu.Unlock()
	out, err := s.GetUserStatus(uid, 200, "tv", "")
	if err != nil || out.Availability == nil || out.Availability.Status != StatusAvailable || !out.Availability.StatusKnown {
		t.Fatalf("media proof disappeared on arr outage: %+v %v", out, err)
	}
	if out.StatusKnown == nil || *out.StatusKnown {
		t.Fatalf("arr outage misrepresented: %+v", out)
	}
}
func TestKidsCannotReadOrRequestMediaOnlyRestrictedMovie(t *testing.T) {
	e := newKidsRequestEnv(t)
	source := &fakeMediaAvailability{result: mediaavailability.Result{Enabled: true, Known: true, Movie: true}}
	e.svc.SetMediaAvailability(source)
	if out, err := e.svc.GetUserStatus(e.kid, 2, "movie", ""); err != nil || out.Status != StatusUnavailable || out.Availability != nil {
		t.Fatalf("media-only restricted status leaked: %+v %v", out, err)
	}
	if _, err := e.svc.CreateMediaRequest(e.kid, &CreateRequest{TmdbID: 2, MediaType: "movie", Title: "Grown"}); !errors.Is(err, ErrTitleNotAvailable) {
		t.Fatalf("media-only restricted request allowed: %v", err)
	}
	if len(source.fresh) > 0 {
		t.Fatal("restricted title reached media lookup")
	}
}

func TestAvailabilityRechecksPolicyAndGrantAfterProviderReads(t *testing.T) {
	for _, kind := range []string{"policy", "grant", "history_policy"} {
		t.Run(kind, func(t *testing.T) {
			srv := newFakeRadarrServer(t, &fakeRadarr{lookupJSON: `[{"title":"Restricted","tmdbId":550}]`})
			s, uid := newHistoryTestService(t, srv.URL, "", "")
			policy := contentpolicy.New(s.db, func() contentpolicy.RawGetter { return &ratingsTMDB{movies: map[int]string{550: "R"}} }, nil)
			s.SetContentPolicy(policy)
			source := &fakeMediaAvailability{result: mediaavailability.Result{Enabled: true, Known: true, Movie: true}}
			source.onLookup = func() {
				if kind == "grant" {
					s.db.Exec(`DELETE FROM user_instance_grants WHERE user_id=?`, uid)
				} else if err := policy.Store.Set(uid, contentpolicy.Policy{MaxMovieRating: "PG", MaxTVRating: "TV-PG", RatingRegion: "US", BlockUnrated: true}); err != nil {
					t.Fatal(err)
				}
			}
			s.SetMediaAvailability(source)
			if kind == "history_policy" {
				_, instanceID, _ := s.resolveRadarr(uid, "")
				s.db.Exec(`INSERT INTO request_log(user_id,tmdb_id,instance_id,media_type,title,status) VALUES (?,550,?,'movie','Restricted','requested')`, uid, instanceID)
				out, err := s.GetRequests(uid)
				if err != nil || len(out) != 0 {
					t.Fatalf("changed kids policy leaked history title: %+v %v", out, err)
				}
			} else {
				out, err := s.GetUserStatus(uid, 550, "movie", "")
				if err == nil {
					t.Fatalf("changed %s authority returned stale status: %+v", kind, out)
				}
			}
		})
	}
}

func TestTVDBOnlyCoverageBindsWithoutPriorRequest(t *testing.T) {
	s, uid, f, source := newMissingTVFixture(t)
	data := knownMedia(1, 2, 3, 4)
	for i := range data.Episodes {
		data.Episodes[i].TMDBID = 0
		data.Episodes[i].SeasonNumber = 9
		data.Episodes[i].EpisodeNumber = 900 + i
	}
	source.set(data)
	for n := 0; n < 2; n++ {
		out, err := s.GetUserStatus(uid, 200, "tv", "")
		if err != nil || out.Availability == nil || out.Availability.Seasons[0].EpisodeFileCount != 4 {
			t.Fatalf("cold TVDB-only coverage: %+v %v", out, err)
		}
	}
	if f.findCalls != 4 || f.externalCalls != 0 {
		t.Fatalf("unbounded or uncached metadata: find=%d external=%d", f.findCalls, f.externalCalls)
	}
}
func TestProvenSpecialsDoNotBlockOrdinaryCoverage(t *testing.T) {
	for _, identity := range []string{"tmdb", "tvdb"} {
		t.Run(identity, func(t *testing.T) {
			s, uid, _, source := newMissingTVFixture(t)
			data := knownMedia(1, 2, 3, 4, 5, 6)
			data.Episodes = append(data.Episodes, mediaavailability.Episode{TMDBID: 20201, SeasonNumber: 2, EpisodeNumber: 1})
			special := mediaavailability.Episode{SeasonNumber: 0, EpisodeNumber: 1}
			if identity == "tmdb" {
				special.TMDBID = 29999
			} else {
				special.TVDBID = 9999
			}
			data.Episodes = append(data.Episodes, special)
			source.set(data)
			out, err := s.GetUserStatus(uid, 200, "tv", "")
			if err != nil || out.Availability == nil || out.Availability.Status != StatusAvailable || !out.Availability.StatusKnown {
				t.Fatalf("special polluted main coverage: %+v %v", out, err)
			}
		})
	}
}
func TestUnselectedTVDBOnlyEpisodesDoNotPolluteSelectedWork(t *testing.T) {
	s, uid, f, source := newMissingTVFixture(t)
	data := knownMedia(1, 2, 3, 4)
	data.Episodes = append(data.Episodes, mediaavailability.Episode{TVDBID: 10201, SeasonNumber: 7, EpisodeNumber: 9})
	source.set(data)
	createMissingTV(t, s, uid)
	retryMissingTV(t, s)
	if fmt.Sprint(f.commands) != "[[105] [106]]" {
		t.Fatalf("unselected identity affected delivery: %v", f.commands)
	}
}

func TestTVOverlappingScopesReuseOnlyInFlightSearches(t *testing.T) {
	s, uid, f, _ := newMissingTVFixture(t)
	createMissingTV(t, s, uid)
	retryMissingTV(t, s)
	second, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 200, MediaType: "tv", Title: "Show", Seasons: []int{1, 2}})
	if err != nil {
		t.Fatal(err)
	}
	if second.RequestID == 0 || fmt.Sprint(f.commands) != "[[105] [106] [201]]" {
		t.Fatalf("overlap repeated search: %+v %v", second, f.commands)
	}
}
func TestNewTVRequestCanAcquirePreviouslyExcludedEpisodes(t *testing.T) {
	s, uid, f, source := newMissingTVFixture(t)
	first := createMissingTV(t, s, uid)
	retryMissingTV(t, s)
	unchanged := createMissingTV(t, s, uid)
	if unchanged.RequestID != first.RequestID {
		t.Fatal("unchanged episode intent failed to dedupe")
	}
	source.set(knownMedia(2, 3, 4)) // Episode 1 disappeared from recipient's source.
	second := createMissingTV(t, s, uid)
	if second.RequestID == first.RequestID || fmt.Sprint(f.commands) != "[[105] [106] [101]]" {
		t.Fatalf("new intent did not acquire formerly excluded episode: ids=%d/%d searches=%v", first.RequestID, second.RequestID, f.commands)
	}
	var state string
	s.db.QueryRow(`SELECT state FROM request_tv_follow_episodes WHERE request_id=? AND episode_key='20001'`, first.RequestID).Scan(&state)
	if state != "excluded" {
		t.Fatal("original exclusions were rewritten")
	}
	if second.RequestScope == nil || len(second.RequestScope.InitiallySatisfiedEpisodes) != 3 || len(second.RequestScope.MissingEpisodes) != 3 {
		t.Fatalf("immutable review receipt missing: %+v", second.RequestScope)
	}
}
func TestOldCompletedSearchDoesNotSuppressNewTVRequest(t *testing.T) {
	s, uid, f, source := newMissingTVFixture(t)
	first := createMissingTV(t, s, uid)
	retryMissingTV(t, s)
	if _, err := s.StopTVFollow(uid, first.RequestID); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.commandsCompleted = true
	f.mu.Unlock()
	source.set(knownMedia(2, 3, 4))
	second := createMissingTV(t, s, uid)
	if second.RequestID == first.RequestID || fmt.Sprint(f.commands) != "[[105] [106] [101] [105] [106]]" {
		t.Fatalf("completed receipt suppressed new missing request: %v", f.commands)
	}
}

func TestTVSourceScopeChangeRequiresNewReview(t *testing.T) {
	s, uid, f, source := newMissingTVFixture(t)
	requireApproval(t, s)
	firstScope := knownMedia(1, 2, 3, 4)
	firstScope.ScopeRevision = "scope-a"
	source.set(firstScope)
	first := createMissingTV(t, s, uid)
	nextScope := firstScope
	nextScope.ScopeRevision = "scope-b"
	source.set(nextScope)
	second := createMissingTV(t, s, uid)
	if first.RequestID == second.RequestID || second.Status != StatusPending || len(f.adds) != 0 {
		t.Fatalf("source authority change reused approval: first=%+v second=%+v", first, second)
	}
}
func TestPausedTVScopeAllowsDeliberateNewRequest(t *testing.T) {
	s, uid, f, _ := newMissingTVFixture(t)
	requireApproval(t, s)
	first := createMissingTV(t, s, uid)
	s.pauseTVFollow(first.RequestID, "Review needed")
	second := createMissingTV(t, s, uid)
	if first.RequestID == second.RequestID || second.Status != StatusPending || len(f.adds) != 0 {
		t.Fatalf("paused pending scope stranded new request: %+v / %+v", first, second)
	}
}
func TestNewTVScopeCannotReplayAnotherUncertainSearch(t *testing.T) {
	s, uid, f, _ := newMissingTVFixture(t)
	f.uncertain = true
	first := createMissingTV(t, s, uid)
	retryMissingTV(t, s)
	second := createMissingTV(t, s, uid)
	if first.RequestID == second.RequestID || len(f.commands) != 1 {
		t.Fatalf("new scope replayed uncertain work: requests=%d/%d commands=%v", first.RequestID, second.RequestID, f.commands)
	}
	var state string
	s.db.QueryRow(`SELECT state FROM request_tv_follows WHERE request_id=?`, second.RequestID).Scan(&state)
	if state != "paused" {
		t.Fatalf("new scope ignored inherited uncertainty: %s", state)
	}
}

func (f *fakeMediaAvailability) ScopeRevision(_ int64) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.result.ScopeRevision, nil
}
func TestLocalMediaScopeRevocationStopsSearchAfterMonitoring(t *testing.T) {
	s, uid, f, source := newMissingTVFixture(t)
	initial := knownMedia(1, 2, 3, 4)
	initial.ScopeRevision = "original"
	source.set(initial)
	out := createMissingTV(t, s, uid)
	f.mu.Lock()
	f.onMonitor = func() { changed := initial; changed.ScopeRevision = "revoked"; source.set(changed) }
	f.mu.Unlock()
	retryMissingTV(t, s)
	var state string
	s.db.QueryRow(`SELECT state FROM request_tv_follows WHERE request_id=?`, out.RequestID).Scan(&state)
	if len(f.monitorWrites) != 1 || len(f.commands) != 0 || state != "paused" {
		t.Fatalf("local revocation missed within fresh window: monitors=%v commands=%v state=%s", f.monitorWrites, f.commands, state)
	}
}

func TestMissingTVAnnouncedEpisodeWaitsForUniqueBinding(t *testing.T) {
	s, uid, f, source := newMissingTVFixture(t)
	f.mu.Lock()
	f.future[6] = true
	f.missingBinding = map[int]bool{6: true}
	f.mu.Unlock()
	out := createMissingTV(t, s, uid)
	retryMissingTV(t, s)
	if fmt.Sprint(f.commands) != "[[105]]" || f.monitoredEpisodes[106] {
		t.Fatalf("unbound future episode acquired: %v %v", f.commands, f.monitoredEpisodes)
	}
	// The same rule applies to a newly announced episode after acceptance.
	f.mu.Lock()
	f.count = 7
	f.future[7] = true
	f.missingBinding[7] = true
	f.mu.Unlock()
	followMissingTV(t, s)
	var state, identity string
	s.db.QueryRow(`SELECT state FROM request_tv_follows WHERE request_id=?`, out.RequestID).Scan(&state)
	if state != "active" || len(f.commands) != 1 {
		t.Fatalf("announcement paused/acquired: state=%s commands=%v", state, f.commands)
	}
	s.db.QueryRow(`SELECT identity FROM request_tv_follow_episodes WHERE request_id=? AND episode_key='20007'`, out.RequestID).Scan(&identity)
	var saved CanonicalEpisode
	if json.Unmarshal([]byte(identity), &saved) != nil || saved.TMDBID != 20007 || saved.TVDBID != 0 {
		t.Fatalf("announced scope was not retained: %s", identity)
	}
	// An aired episode without a binding defers safely and remains active.
	f.mu.Lock()
	delete(f.future, 6)
	f.mu.Unlock()
	followMissingTV(t, s)
	s.db.QueryRow(`SELECT state FROM request_tv_follows WHERE request_id=?`, out.RequestID).Scan(&state)
	if state != "active" || len(f.commands) != 1 {
		t.Fatalf("incomplete aired metadata was unsafe: state=%s commands=%v", state, f.commands)
	}
	// Reconstructing the service proves the placeholder is durable. Both aired
	// episodes are bound and searched exactly once when metadata catches up.
	s = restartMissingService(s, source)
	f.mu.Lock()
	delete(f.future, 7)
	delete(f.missingBinding, 6)
	delete(f.missingBinding, 7)
	f.mu.Unlock()
	followMissingTV(t, s)
	if fmt.Sprint(f.commands) != "[[105] [106] [107]]" {
		t.Fatalf("later binding was not delivered exactly: %v", f.commands)
	}
	followMissingTV(t, s)
	if len(f.commands) != 3 {
		t.Fatalf("bound episode work replayed: %v", f.commands)
	}
}
