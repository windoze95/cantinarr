package request

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

type correctionLab struct {
	mu             sync.Mutex
	parent         map[string]any
	episodes       []map[string]any
	commands       []map[string]any
	adds           []map[string]any
	delayRefresh   bool
	metadataDown   bool
	metadataDownID int
	episodesDown   bool
	extraSource    bool
	missingTarget  bool
	mutations      int
	lookupTVDB     int
}

func (l *correctionLab) seasonMetadata() []map[string]any {
	out := []map[string]any{}
	for n := 0; n <= 4; n++ {
		if l.missingTarget && n == 4 {
			continue
		}
		out = append(out, map[string]any{"seasonNumber": n, "monitored": false, "statistics": map[string]any{"episodeFileCount": 0, "episodeCount": 0, "totalEpisodeCount": 2}})
	}
	return out
}

func (l *correctionLab) finishRefresh() {
	l.parent["addOptions"] = nil
	flags := map[int]bool{}
	for _, v := range l.parent["seasons"].([]any) {
		ss := v.(map[string]any)
		flags[int(ss["seasonNumber"].(float64))] = ss["monitored"] == true
	}
	for _, ep := range l.episodes {
		ep["monitored"] = flags[ep["seasonNumber"].(int)]
	}
}

func newCorrectionLab(t *testing.T) (*Service, int64, int64, *correctionLab) {
	t.Helper()
	l := &correctionLab{}
	for n := 1; n <= 4; n++ {
		for ep := 1; ep <= 2; ep++ {
			air := "2020-01-01T00:00:00Z"
			if n == 4 {
				air = "2099-01-01T00:00:00Z"
			}
			l.episodes = append(l.episodes, map[string]any{"id": n*10 + ep, "seasonNumber": n, "episodeNumber": ep, "monitored": false, "hasFile": false, "airDateUtc": air})
		}
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l.mu.Lock()
		defer l.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		write := func(v any) {
			if err := json.NewEncoder(w).Encode(v); err != nil {
				t.Error(err)
			}
		}
		if r.Method != "GET" {
			l.mutations++
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/tv/"):
			parts := strings.Split(r.URL.Path, "/")
			id, _ := strconv.Atoi(parts[2])
			if l.metadataDown || l.metadataDownID == id {
				w.WriteHeader(503)
				return
			}
			if strings.HasSuffix(r.URL.Path, "/external_ids") {
				write(map[string]any{"tvdb_id": 999999})
				return
			}
			seasons := []map[string]any{{"season_number": 0, "name": "Specials"}, {"season_number": 1, "name": "Season 1", "episode_count": 2}}
			if l.extraSource {
				seasons = append(seasons, map[string]any{"season_number": 2, "name": "New season"})
			}
			write(map[string]any{"id": id, "name": fmt.Sprintf("Selected TMDB %d", id), "first_air_date": "2026-01-01", "seasons": seasons})
		case r.Method == "GET" && r.URL.Path == "/api/v3/series/lookup":
			tvdbID := l.lookupTVDB
			if tvdbID == 0 {
				tvdbID = 389492
			}
			write([]map[string]any{{"tvdbId": tvdbID, "title": "Monster (2022)", "year": 2022, "seasons": l.seasonMetadata()}})
		case r.Method == "GET" && r.URL.Path == "/api/v3/series":
			if l.parent == nil {
				write([]any{})
				return
			}
			write([]any{l.parent})
		case r.Method == "POST" && r.URL.Path == "/api/v3/series":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			l.adds = append(l.adds, body)
			data, _ := json.Marshal(body)
			_ = json.Unmarshal(data, &l.parent)
			l.parent["id"] = 42
			l.parent["customSetting"] = "preserve"
			if !l.delayRefresh {
				l.finishRefresh()
			}
			write(l.parent)
		case r.URL.Path == "/api/v3/series/42":
			if r.Method == "PUT" {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				l.parent = body
			}
			write(l.parent)
		case r.URL.Path == "/api/v3/qualityprofile":
			write([]map[string]any{{"id": 1, "name": "Any"}})
		case r.URL.Path == "/api/v3/rootfolder":
			write([]map[string]any{{"id": 1, "path": "/tv"}})
		case r.Method == "GET" && r.URL.Path == "/api/v3/episode":
			if l.episodesDown {
				w.WriteHeader(503)
				return
			}
			n, _ := strconv.Atoi(r.URL.Query().Get("seasonNumber"))
			out := []map[string]any{}
			for _, ep := range l.episodes {
				if n == 0 || ep["seasonNumber"] == n {
					out = append(out, ep)
				}
			}
			write(out)
		case r.Method == "PUT" && r.URL.Path == "/api/v3/episode/monitor":
			var body struct {
				EpisodeIDs []int `json:"episodeIds"`
				Monitored  bool  `json:"monitored"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			for _, id := range body.EpisodeIDs {
				for _, ep := range l.episodes {
					if ep["id"] == id {
						ep["monitored"] = body.Monitored
					}
				}
			}
			write(map[string]any{})
		case r.Method == "POST" && r.URL.Path == "/api/v3/command":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			l.commands = append(l.commands, body)
			write(map[string]any{"id": 1})
		default:
			t.Errorf("unexpected correction request %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(upstream.Close)
	s, uid := newHistoryTestService(t, "", upstream.URL, "")
	s.bridge = tmdb.NewBridge(bridgeClients{tmdb.NewClientWithBaseURL("fixture", upstream.URL)}, s.db)
	admin := createTestAdmin(t, s)
	return s, uid, admin, l
}

func tvRequest(id int, scope string) *CreateRequest {
	return &CreateRequest{TmdbID: id, MediaType: "tv", Title: "Untrusted client title", TvdbID: 999999, SeasonScope: scope}
}

func assertOnlySeasons(t *testing.T, l *correctionLab, want ...int) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	selected := map[int]bool{}
	for _, n := range want {
		selected[n] = true
	}
	for _, v := range l.parent["seasons"].([]any) {
		season := v.(map[string]any)
		n := int(season["seasonNumber"].(float64))
		if (season["monitored"] == true) != selected[n] {
			t.Errorf("season %d monitored=%v want=%v", n, season["monitored"], selected[n])
		}
	}
}

func TestBundledTVTargetsAllStoriesAndScopes(t *testing.T) {
	for id, targetSeason := range map[int]int{113988: 1, 225634: 2, 286801: 3, 299939: 4} {
		for _, scope := range []string{SeasonScopeAll, SeasonScopeFirst, SeasonScopeLatest, "explicit"} {
			t.Run(fmt.Sprintf("%d/%s", id, scope), func(t *testing.T) {
				s, uid, _, l := newCorrectionLab(t)
				req := tvRequest(id, scope)
				if scope == "explicit" {
					req.SeasonScope = ""
					req.Seasons = []int{1}
				}
				resp, err := s.CreateMediaRequest(uid, req)
				if err != nil {
					t.Fatal(err)
				}
				if resp.Title != fmt.Sprintf("Selected TMDB %d", id) || resp.Match == nil || resp.Match.TVDBID != 389492 {
					t.Fatalf("identity: %+v", resp)
				}
				assertOnlySeasons(t, l, targetSeason)
				if len(l.adds) != 1 || l.adds[0]["monitorNewItems"] != "none" {
					t.Fatalf("add: %v", l.adds)
				}
				if _, exists := l.adds[0]["addOptions"].(map[string]any)["monitor"]; exists {
					t.Fatal("explicit correction sent a parent monitor option")
				}
				saved, _, _, err := s.loadTVTarget(resp.RequestID)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(saved.SourceSeasons, []int{1}) || !reflect.DeepEqual(saved.TargetSeasons, []int{targetSeason}) {
					t.Fatalf("snapshot: %+v", saved)
				}
				var count int
				_ = s.db.QueryRow(`SELECT COUNT(*) FROM tmdb_tvdb_cache`).Scan(&count)
				if count != 0 {
					t.Fatal("client hint contaminated bridge cache")
				}
			})
		}
	}
}

func TestTVOverrideReplacesEntireBundledMap(t *testing.T) {
	for _, mode := range []string{"custom", "paused"} {
		t.Run(mode, func(t *testing.T) {
			s, _, _, _ := newCorrectionLab(t)
			// A reviewed source numbering change must not inherit any keys
			// from an older (or newly upgraded) bundled correction.
			_, err := s.db.Exec(`INSERT INTO tv_match_overrides(tmdb_id,mode,tvdb_id,season_map,revision) VALUES(299939,?,389492,'{"2":4}',7)`, mode)
			if err != nil {
				t.Fatal(err)
			}
			match, revision, err := configuredTVMatch(s.db, 299939)
			if err != nil {
				t.Fatal(err)
			}
			if revision != 7 || match.Provenance != "custom" || !reflect.DeepEqual(match.SeasonMap, map[int]int{2: 4}) {
				t.Fatalf("bundled keys leaked into local override: %+v revision=%d", match, revision)
			}
		})
	}
}

func TestTVCorrectionExistingParentIsAdditiveAndSeasonScoped(t *testing.T) {
	s, uid, _, l := newCorrectionLab(t)
	if _, err := s.CreateMediaRequest(uid, tvRequest(113988, SeasonScopeAll)); err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	l.parent["monitorNewItems"] = "all"
	for _, ep := range l.episodes {
		if ep["seasonNumber"] == 1 {
			ep["hasFile"] = true
		}
	}
	l.mu.Unlock()
	status, err := s.GetUserStatus(uid, 299939, "tv", "")
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != StatusUnavailable || len(status.Seasons) != 1 || status.Seasons[0].SeasonNumber != 1 {
		t.Fatalf("Dahmer leaked to Lizzie: %+v", status)
	}
	dahmer, err := s.GetUserStatus(uid, 113988, "tv", "")
	if err != nil || dahmer.Status != StatusAvailable {
		t.Fatalf("Dahmer: %+v %v", dahmer, err)
	}
	if _, err = s.CreateMediaRequest(uid, tvRequest(299939, SeasonScopeAll)); err != nil {
		t.Fatal(err)
	}
	assertOnlySeasons(t, l, 1, 4)
	if l.parent["monitorNewItems"] != "all" || l.parent["customSetting"] != "preserve" {
		t.Fatal("existing settings changed")
	}
	status, err = s.GetUserStatus(uid, 299939, "tv", "")
	if err != nil || status.Status != StatusRequested || status.Seasons[0].EpisodeFileCount != 0 {
		t.Fatalf("unaired Lizzie: %+v %v", status, err)
	}
	for _, ep := range l.episodes {
		if ep["seasonNumber"] == 1 && ep["hasFile"] != true {
			t.Fatal("prior files changed")
		}
		if ep["seasonNumber"] == 2 && ep["monitored"] == true {
			t.Fatal("unselected episodes changed")
		}
	}
	requests, err := s.GetRequests(uid)
	if err != nil {
		t.Fatal(err)
	}
	if statusOf(t, requests, "Selected TMDB 299939") != StatusRequested || statusOf(t, requests, "Selected TMDB 113988") != StatusAvailable {
		t.Fatalf("history: %+v", requests)
	}
	l.mu.Lock()
	l.episodesDown = true
	l.mu.Unlock()
	status, err = s.GetUserStatus(uid, 299939, "tv", "")
	if err != nil {
		t.Fatal(err)
	}
	if status.StatusKnown == nil || *status.StatusKnown || status.Status == StatusAvailable {
		t.Fatalf("failed episode read: %+v", status)
	}
}

func TestTVCorrectionPilotSurvivesRefreshAndRestart(t *testing.T) {
	s, uid, _, l := newCorrectionLab(t)
	l.delayRefresh = true
	resp, err := s.CreateMediaRequest(uid, tvRequest(286801, SeasonScopePilot))
	if err != nil {
		t.Fatal(err)
	}
	if len(l.adds) != 1 || l.adds[0]["monitored"] != false || l.adds[0]["addOptions"].(map[string]any)["searchForMissingEpisodes"] != false || l.adds[0]["addOptions"].(map[string]any)["monitor"] != "none" {
		t.Fatalf("unsafe pilot add: %v", l.adds)
	}
	if len(l.commands) != 0 {
		t.Fatal("pilot searched before initial refresh")
	}
	resumed := NewService(s.db, s.registry, s.bridge, nil)
	_, _ = s.db.Exec(`UPDATE request_dispatch SET next_attempt_at=0 WHERE request_id=?`, resp.RequestID)
	resumed.SweepDispatch(context.Background())
	if len(l.commands) != 0 {
		t.Fatal("restart bypassed metadata fence")
	}
	l.mu.Lock()
	l.finishRefresh()
	l.mu.Unlock()
	_, _ = s.db.Exec(`UPDATE request_dispatch SET next_attempt_at=0 WHERE request_id=?`, resp.RequestID)
	resumed.SweepDispatch(context.Background())
	assertOnlySeasons(t, l)
	for _, ep := range l.episodes {
		if (ep["monitored"] == true) != (ep["id"] == 31) {
			t.Fatalf("pilot scope leaked: %+v", ep)
		}
	}
	if len(l.commands) != 1 || l.commands[0]["name"] != "EpisodeSearch" || !reflect.DeepEqual(l.commands[0]["episodeIds"], []any{float64(31)}) {
		t.Fatalf("pilot search: %+v", l.commands)
	}
	states, err := s.deliveryStates(resp.RequestID)
	if err != nil || states[0].State != "complete" {
		t.Fatalf("delivery: %+v %v", states, err)
	}
}

func TestTVCorrectionApprovalAndMappingRevision(t *testing.T) {
	s, uid, admin, l := newCorrectionLab(t)
	requireApproval(t, s)
	resp, err := s.CreateMediaRequest(uid, tvRequest(299939, SeasonScopeAll))
	if err != nil {
		t.Fatal(err)
	}
	if l.mutations != 0 || resp.Status != StatusPending {
		t.Fatal("approval bypassed")
	}
	view, err := s.TVMatchDetail(admin, 299939, resp.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveTVMatch(admin, 299939, TVMatchEdit{Revision: view.Match.Revision, Mode: "custom", TVDBID: 389492, SeasonMap: map[int]int{1: 3}, InstanceID: resp.InstanceID})
	if err != nil {
		t.Fatal(err)
	}
	if l.mutations != 0 {
		t.Fatal("saving correction mutated Sonarr")
	}
	if _, err = s.ApproveRequest(admin, resp.RequestID, nil); !errors.Is(err, ErrTVMatchStale) {
		t.Fatalf("stale approval: %v", err)
	}
	if _, err = s.SaveTVMatch(admin, 299939, TVMatchEdit{Revision: view.Match.Revision, Mode: "paused", InstanceID: resp.InstanceID}); !errors.Is(err, ErrTVMatchStale) {
		t.Fatalf("stale save: %v", err)
	}
	paused, err := s.SaveTVMatch(admin, 299939, TVMatchEdit{Revision: saved.Match.Revision, Mode: "paused", InstanceID: resp.InstanceID})
	if err != nil || paused.Match.State != "paused" || paused.Match.SeasonMap[1] != 3 {
		t.Fatalf("pause lost the local target: %+v, %v", paused, err)
	}
	reset, err := s.SaveTVMatch(admin, 299939, TVMatchEdit{Revision: paused.Match.Revision, Mode: "default", InstanceID: resp.InstanceID})
	if err != nil {
		t.Fatal(err)
	}
	if reset.Match.Provenance != "bundled" || reset.Match.SeasonMap[1] != 4 {
		t.Fatalf("reset: %+v", reset)
	}
	fresh, err := s.CreateMediaRequest(uid, tvRequest(286801, SeasonScopeAll))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApproveRequest(admin, fresh.RequestID, nil); err != nil {
		t.Fatal(err)
	}
	assertOnlySeasons(t, l, 3)
	var scope string
	_ = s.db.QueryRow(`SELECT season_scope FROM request_log WHERE id=?`, fresh.RequestID).Scan(&scope)
	if scope != SeasonScopeAll {
		t.Fatal("approval replaced source scope")
	}
}

func TestTVCorrectionPauseMissingSeasonsAndAuthorization(t *testing.T) {
	s, uid, admin, l := newCorrectionLab(t)
	if _, err := s.ListTVMatches(uid); !errors.Is(err, ErrTVMatchAdmin) {
		t.Fatal("requester listed corrections")
	}
	if _, err := s.TVMatchCandidates(uid, "", "389492"); !errors.Is(err, ErrTVMatchAdmin) {
		t.Fatal("requester searched corrections")
	}
	if _, err := s.SaveTVMatch(uid, 299939, TVMatchEdit{}); !errors.Is(err, ErrTVMatchAdmin) {
		t.Fatal("requester saved correction")
	}
	view, err := s.TVMatchDetail(admin, 299939, "")
	if err != nil {
		t.Fatal(err)
	}
	paused, err := s.SaveTVMatch(admin, 299939, TVMatchEdit{Revision: view.Match.Revision, Mode: "paused"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateMediaRequest(uid, tvRequest(299939, "all")); err == nil {
		t.Fatal("paused match accepted")
	}
	if _, err = s.SaveTVMatch(admin, 299939, TVMatchEdit{Revision: paused.Match.Revision, Mode: "default"}); err != nil {
		t.Fatal(err)
	}
	l.extraSource = true
	if _, err = s.CreateMediaRequest(uid, tvRequest(299939, "all")); err == nil {
		t.Fatal("new unmapped source season accepted")
	}
	l.extraSource = false
	l.missingTarget = true
	if _, err = s.CreateMediaRequest(uid, tvRequest(299939, "all")); err == nil {
		t.Fatal("missing target season accepted")
	}
	if l.mutations != 0 {
		t.Fatal("unresolved request mutated Sonarr")
	}
}

func TestStrictTVTitleIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, title, original, date string
		candidates                  []sonarr.LookupResult
		want                        int
	}{
		{"Florence rejected", "Monster: The Lizzie Borden Story", "", "2026-01-01", []sonarr.LookupResult{{Title: "Florence", TvdbID: 1, Year: 2026}}, 0},
		{"remake rejected", "Tremors", "", "2018-01-01", []sonarr.LookupResult{{Title: "Tremors", TvdbID: 2, Year: 2003}}, 0},
		{"missing source year", "Show", "", "", []sonarr.LookupResult{{Title: "Show", TvdbID: 3, Year: 2026}}, 0},
		{"missing target year", "Show", "", "2026-01-01", []sonarr.LookupResult{{Title: "Show", TvdbID: 3}}, 0},
		{"ambiguous", "Show", "", "2026-01-01", []sonarr.LookupResult{{Title: "Show", TvdbID: 3, Year: 2026}, {Title: "Show", TvdbID: 4, Year: 2025}}, 0},
		{"canonical", "Show!", "", "2026-01-01", []sonarr.LookupResult{{Title: "Other", TvdbID: 4, Year: 2026}, {Title: "Show", TvdbID: 3, Year: 2025}}, 3},
		{"original", "Localized", "Original", "2026-01-01", []sonarr.LookupResult{{Title: "Original", TvdbID: 3, Year: 2026}}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := strictTVTitleMatch(&tmdb.TVDetails{Name: tc.title, OriginalName: tc.original, FirstAir: tc.date}, tc.candidates)
			if tc.want == 0 {
				if err == nil {
					t.Fatalf("unexpected match %+v", got)
				}
			} else if err != nil || got.TvdbID != tc.want {
				t.Fatalf("match %+v %v", got, err)
			}
		})
	}
}

func TestTVRepairIsAuditedIdempotentAndKeepsApproval(t *testing.T) {
	s, uid, admin, l := newCorrectionLab(t)
	quotaLimit(t, s, admin, 0, "tv", "", 0)
	instanceID := s.effectiveArrInstanceID(uid, "tv")
	for _, status := range []string{StatusRequested, StatusPending, StatusDenied} {
		original := &resolvedRequest{userID: uid, tmdbID: 299939, tvdbID: 12345, mediaType: "tv", title: "Original Lizzie title", instanceID: instanceID, seasonScope: SeasonScopePilot}
		id, err := s.insertRequest(original, original.title, status)
		if err != nil {
			t.Fatal(err)
		}
		preview, err := s.tvRepairPreview(admin, id)
		if err != nil {
			t.Fatal(err)
		}
		if status == StatusDenied {
			if preview.CanRepair {
				t.Fatal("denied request offered repair")
			}
			if _, err = s.RepairTVMatch(admin, id, preview.Revision); err == nil {
				t.Fatal("denied request revived")
			}
			continue
		}
		if preview.RecordedTargetKnown || preview.RecordedTVDBID != 12345 || !preview.IntendedTarget.Pilot || !reflect.DeepEqual(preview.IntendedTarget.TargetSeasons, []int{4}) {
			t.Fatalf("legacy preview: %+v", preview)
		}
		if _, err = s.RepairTVMatch(uid, id, preview.Revision); !errors.Is(err, ErrTVMatchAdmin) {
			t.Fatal("requester repaired a match")
		}
		if _, err = s.RepairTVMatch(admin, id, "stale"); !errors.Is(err, ErrTVMatchStale) {
			t.Fatal("stale repair accepted")
		}
		first, err := s.RepairTVMatch(admin, id, preview.Revision)
		if err != nil {
			t.Fatal(err)
		}
		second, err := s.RepairTVMatch(admin, id, preview.Revision)
		if err != nil || first.RequestID != second.RequestID {
			t.Fatalf("duplicate repair: %+v %+v %v", first, second, err)
		}
		if quotaRows(t, s, "request_quota_charges") != 0 {
			t.Fatal("corrective repair charged allowance")
		}
		if (first.Status == StatusPending) != (status == StatusPending) {
			t.Fatalf("approval changed: %+v", first)
		}
		var keptStatus, title string
		var tvdb int
		if err = s.db.QueryRow(`SELECT status,title,tvdb_id FROM request_log WHERE id=?`, id).Scan(&keptStatus, &title, &tvdb); err != nil {
			t.Fatal(err)
		}
		if keptStatus != status || title != original.title || tvdb != 12345 {
			t.Fatal("original history overwritten")
		}
		var actor, linked int64
		if err = s.db.QueryRow(`SELECT repair_of,repaired_by FROM request_tv_targets WHERE request_id=?`, first.RequestID).Scan(&linked, &actor); err != nil || linked != id || actor != admin {
			t.Fatal("repair audit missing")
		}
	}
	if l.mutations != 0 {
		t.Fatal("repair preview/intake mutated Sonarr before dispatch")
	}
}

func TestQueuedTVMappingChangePausesBeforePilot(t *testing.T) {
	s, uid, admin, l := newCorrectionLab(t)
	l.delayRefresh = true
	resp, err := s.CreateMediaRequest(uid, tvRequest(299939, SeasonScopePilot))
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.TVMatchDetail(admin, 299939, resp.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveTVMatch(admin, 299939, TVMatchEdit{Revision: view.Match.Revision, Mode: "custom", TVDBID: 389492, SeasonMap: map[int]int{1: 3}, InstanceID: resp.InstanceID}); err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	l.finishRefresh()
	l.mu.Unlock()
	_, _ = s.db.Exec(`UPDATE request_dispatch SET next_attempt_at=0 WHERE request_id=?`, resp.RequestID)
	s.SweepDispatch(context.Background())
	states, err := s.deliveryStates(resp.RequestID)
	if err != nil || states[0].State != "attention" || states[0].Code != "tv_match_changed" {
		t.Fatalf("stale delivery %+v %v", states, err)
	}
	if len(l.commands) != 0 {
		t.Fatal("stale pilot searched")
	}
	for _, ep := range l.episodes {
		if ep["monitored"] == true {
			t.Fatal("stale pilot monitored an episode")
		}
	}
}

func TestConcurrentMonsterStoriesKeepBothSeasonScopes(t *testing.T) {
	s, uid, _, l := newCorrectionLab(t)
	var wg sync.WaitGroup
	for _, id := range []int{113988, 299939} {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if _, err := s.CreateMediaRequest(uid, tvRequest(id, SeasonScopeAll)); err != nil {
				t.Error(err)
			}
		}(id)
	}
	wg.Wait()
	s.SweepDispatch(context.Background())
	assertOnlySeasons(t, l, 1, 4)
	if len(l.adds) != 1 {
		t.Fatalf("parent added %d times", len(l.adds))
	}
}

func TestTVCorrectionLibrariesAndKidsPolicy(t *testing.T) {
	s, uid, admin, l := newCorrectionLab(t)
	other, otherUID, _, otherLab := newCorrectionLab(t)
	if _, err := other.CreateMediaRequest(otherUID, tvRequest(299939, "all")); err != nil {
		t.Fatal(err)
	}
	otherLab.mu.Lock()
	for _, ep := range otherLab.episodes {
		if ep["seasonNumber"] == 4 {
			ep["hasFile"] = true
		}
	}
	otherLab.mu.Unlock()
	var otherURL string
	_ = other.db.QueryRow(`SELECT url FROM service_instances WHERE service_type='sonarr'`).Scan(&otherURL)
	primary := s.effectiveArrInstanceID(uid, "tv")
	_, err := s.db.Exec(`INSERT INTO service_instances(id,service_type,name,url,api_key) SELECT 'tv-sibling',service_type,'Other library',?,api_key FROM service_instances WHERE id=?`, otherURL, primary)
	if err != nil {
		t.Fatal(err)
	}
	request := tvRequest(299939, "all")
	request.InstanceID = "tv-sibling"
	if _, err = s.CreateMediaRequest(uid, request); !errors.Is(err, ErrArrInstanceForbidden) {
		t.Fatalf("ungranted target: %v", err)
	}
	if _, err = s.GetUserStatus(uid, 299939, "tv", "tv-sibling"); !errors.Is(err, ErrArrInstanceForbidden) {
		t.Fatal("ungranted status leaked")
	}
	_, err = s.db.Exec(`INSERT INTO user_instance_grants(user_id,instance_id) VALUES(?,?),(?,'tv-sibling')`, uid, primary, uid)
	if err != nil {
		t.Fatal(err)
	}
	status, err := s.GetUserStatus(uid, 299939, "tv", primary)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != StatusUnavailable || status.InstanceStatuses["tv-sibling"].Status != StatusAvailable || status.InstanceStatuses[primary].Status != StatusUnavailable {
		t.Fatalf("instance scoping: %+v", status)
	}
	adminStatus, err := s.GetUserStatus(admin, 299939, "tv", primary)
	if err != nil || len(adminStatus.InstanceStatuses) != 2 || adminStatus.InstanceStatuses["tv-sibling"].Status != StatusAvailable {
		t.Fatalf("admin library selection: %+v, %v", adminStatus, err)
	}
	policies := contentpolicy.New(s.db, func() contentpolicy.RawGetter { return &ratingsTMDB{} }, nil)
	s.SetContentPolicy(policies)
	if err = policies.Store.Set(uid, contentpolicy.Policy{MaxMovieRating: "PG", MaxTVRating: "TV-PG", RatingRegion: "US", BlockUnrated: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateMediaRequest(uid, tvRequest(299939, "all")); err == nil {
		t.Fatal("unrated kids request accepted")
	}
	if l.mutations != 0 {
		t.Fatal("policy rejection mutated library")
	}
}

func TestTVMatchHandlersRequireCurrentAdmin(t *testing.T) {
	s, uid, admin, lab := newCorrectionLab(t)
	h := NewHandler(s)
	handlers := []http.HandlerFunc{h.ListTVMatches, h.GetTVMatch, h.SaveTVMatch, h.TVMatchCandidates, h.TVRepairPreviews, h.RepairTVMatch}
	for _, handler := range handlers {
		for _, signedIn := range []bool{false, true} {
			r := httptest.NewRequest(http.MethodPost, "/api/admin/tv-matches/299939", strings.NewReader(`{}`))
			want := http.StatusUnauthorized
			if signedIn {
				// A stale/forged admin claim cannot bypass the current DB role.
				r = r.WithContext(context.WithValue(r.Context(), auth.ClaimsKey, &auth.Claims{UserID: uid, Role: auth.RoleAdmin}))
				want = http.StatusForbidden
			}
			w := httptest.NewRecorder()
			handler(w, r)
			if w.Code != want {
				t.Fatalf("authorization = %d, want %d", w.Code, want)
			}
		}
	}
	if _, err := s.TVMatchDetail(uid, 299939, ""); !errors.Is(err, ErrTVMatchAdmin) {
		t.Fatalf("service detail authorized requester: %v", err)
	}
	if _, err := s.TVRepairPreviews(uid, 299939); !errors.Is(err, ErrTVMatchAdmin) {
		t.Fatalf("service repair preview authorized requester: %v", err)
	}
	if _, err := s.RepairTVMatch(uid, 1, "revision"); !errors.Is(err, ErrTVMatchAdmin) {
		t.Fatalf("service repair authorized requester: %v", err)
	}
	if _, err := s.DeliveryStatus(admin, "tv", "", "", nil); err == nil {
		t.Fatal("TV deliveries grouped by empty foreign identity")
	}
	if lab.mutations != 0 {
		t.Fatal("unauthorized operation mutated Sonarr")
	}
}

func TestTVRepairPreviewsOnlyRepeatChangedDeliveryScopes(t *testing.T) {
	s, uid, admin, _ := newCorrectionLab(t)
	created, err := s.CreateMediaRequest(uid, tvRequest(299939, "all"))
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.TVMatchDetail(admin, 299939, created.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []int{4, 3} {
		view, err = s.SaveTVMatch(admin, 299939, TVMatchEdit{Revision: view.Match.Revision, Mode: "custom", TVDBID: 389492, SeasonMap: map[int]int{1: target}, InstanceID: created.InstanceID})
		if err != nil {
			t.Fatal(err)
		}
		previews, err := s.TVRepairPreviews(admin, 299939)
		if err != nil || (len(previews) == 0) != (target == 4) {
			t.Fatalf("target %d repair previews: %+v %v", target, previews, err)
		}
	}
}

func TestTVMetadataOutageCannotUseClientHint(t *testing.T) {
	s, uid, _, l := newCorrectionLab(t)
	l.metadataDown = true
	if _, err := s.CreateMediaRequest(uid, tvRequest(299939, "all")); err == nil {
		t.Fatal("outage enabled client mapping")
	}
	status, err := s.GetUserStatus(uid, 299939, "tv", "")
	if err != nil {
		t.Fatal(err)
	}
	if status.StatusKnown == nil || *status.StatusKnown || status.Match.State != "unresolved" {
		t.Fatalf("outage reported absence: %+v", status)
	}
	if l.mutations != 0 {
		t.Fatal("outage mutated library")
	}
}
