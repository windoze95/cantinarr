package request

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

func importParent() *sonarr.Series {
	return &sonarr.Series{ID: 42, TvdbID: 389492, TmdbID: 113988, Title: "Monster (2022)",
		Seasons: []sonarr.SeasonResource{{SeasonNumber: 1}, {SeasonNumber: 2}, {SeasonNumber: 3}, {SeasonNumber: 4}}}
}

func TestTVImportsResolveAllStoriesWithoutRequests(t *testing.T) {
	s, uid, _, _ := newCorrectionLab(t)
	client, _, err := s.resolveSonarr(uid, "")
	if err != nil {
		t.Fatal(err)
	}
	imports := []sonarr.ImportedEpisode{
		{SeasonNumber: 1, Upgrade: true}, {SeasonNumber: 2},
		{SeasonNumber: 2}, {SeasonNumber: 3, Upgrade: true},
		{SeasonNumber: 3}, {SeasonNumber: 4},
	}
	titles, err := s.ResolveTVImports(client, importParent(), imports)
	if err != nil {
		t.Fatal(err)
	}
	want := []sonarr.ImportedTitle{}
	for i, id := range []int{113988, 225634, 286801, 299939} {
		want = append(want, sonarr.ImportedTitle{TmdbID: id, Title: fmt.Sprintf("Selected TMDB %d", id), Upgrade: i == 0})
	}
	if !reflect.DeepEqual(titles, want) {
		t.Fatalf("titles = %+v, want %+v", titles, want)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM request_log`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("request history was needed or written: %d, %v", count, err)
	}
}

func TestTVImportsRejectUnverifiableScopesIndependently(t *testing.T) {
	for _, tc := range []struct {
		name, override, reason                   string
		season                                   int
		metadataDown, missingTarget, extraSource bool
	}{
		{name: "paused", season: 2, override: `INSERT INTO tv_match_overrides(tmdb_id,mode,tvdb_id,season_map,revision) VALUES(225634,'paused',389492,'{"1":2}',1)`, reason: "paused"},
		{name: "ambiguous custom", season: 2, override: `INSERT INTO tv_match_overrides(tmdb_id,mode,tvdb_id,season_map,revision) VALUES(555,'custom',389492,'{"1":2}',1)`, reason: "one verified"},
		{name: "unknown season", season: 5, reason: "unmapped"},
		{name: "missing season evidence", season: 0, reason: "unmapped"},
		{name: "metadata down", season: 2, metadataDown: true, reason: "metadata"},
		{name: "source grew", season: 2, extraSource: true, reason: "unmapped"},
		{name: "target missing", season: 4, missingTarget: true, reason: "unmapped"},
		{name: "override replaces bundle", season: 2, override: `INSERT INTO tv_match_overrides(tmdb_id,mode,tvdb_id,season_map,revision) VALUES(225634,'custom',12345,'{"1":1}',1)`, reason: "unmapped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, uid, _, lab := newCorrectionLab(t)
			lab.metadataDown, lab.missingTarget, lab.extraSource = tc.metadataDown, tc.missingTarget, tc.extraSource
			if tc.override != "" {
				if _, err := s.db.Exec(tc.override); err != nil {
					t.Fatal(err)
				}
			}
			client, _, _ := s.resolveSonarr(uid, "")
			titles, err := s.ResolveTVImports(client, importParent(), []sonarr.ImportedEpisode{{SeasonNumber: tc.season}, {SeasonNumber: 1}})
			if err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("error = %v", err)
			}
			wantCount := 1
			if tc.metadataDown || tc.extraSource {
				wantCount = 0
			}
			if len(titles) != wantCount || (len(titles) > 0 && titles[0].TmdbID != 113988) {
				t.Fatalf("unverified scope announced or sibling lost: %+v", titles)
			}
		})
	}
}

func TestTVImportCustomPauseAndResetUseCurrentRevisions(t *testing.T) {
	s, uid, admin, _ := newCorrectionLab(t)
	client, instanceID, _ := s.resolveSonarr(uid, "")
	edit := func(id int, mode string, mapping map[int]int) {
		t.Helper()
		view, err := s.TVMatchDetail(admin, id, instanceID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.SaveTVMatch(admin, id, TVMatchEdit{Revision: view.Match.Revision, Mode: mode, TVDBID: 389492, SeasonMap: mapping, InstanceID: instanceID})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Swap two stories: local choices replace bundled ownership entirely.
	edit(113988, "custom", map[int]int{1: 2})
	edit(225634, "custom", map[int]int{1: 1})
	check := func(wantID int, wantError bool) {
		t.Helper()
		titles, err := s.ResolveTVImports(client, importParent(), []sonarr.ImportedEpisode{{SeasonNumber: 2}})
		if wantError {
			if err == nil || len(titles) != 0 {
				t.Fatalf("paused scope = %+v, %v", titles, err)
			}
			return
		}
		if err != nil || len(titles) != 1 || titles[0].TmdbID != wantID {
			t.Fatalf("titles = %+v, %v", titles, err)
		}
	}
	check(113988, false)
	edit(113988, "paused", nil)
	check(0, true)
	edit(113988, "default", nil)
	edit(225634, "default", nil)
	check(225634, false)
	if titles, err := s.ResolveTVImports(client, importParent(), nil); len(titles) != 0 || err == nil {
		t.Fatalf("queue departure guessed a story: %+v, %v", titles, err)
	}
}
