package request

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
)

func libraryParent(lab *correctionLab) {
	lab.parent = map[string]any{"id": 42, "tvdbId": 389492, "tmdbId": 0, "title": "Monster (2022)",
		"overview": "An anthology with four stories.", "firstAired": "2022-09-21T00:00:00Z",
		"images":  []map[string]any{{"coverType": "poster", "remoteUrl": "https://cdn.example/monster.jpg", "url": "http://sonarr:8989/secret"}},
		"seasons": lab.seasonMetadata()}
}

func libraryDetail(t *testing.T, s *Service, uid int64) *TVLibraryDetail {
	t.Helper()
	_, instance, _ := s.resolveSonarr(uid, "")
	out, err := s.TVLibraryDetail(uid, instance, 42)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTVLibraryDetailGroupsAllSeasonsWithoutCatalogParentID(t *testing.T) {
	for _, parentID := range []int{0, 113988} {
		t.Run(fmt.Sprint(parentID), func(t *testing.T) {
			s, uid, _, lab := newCorrectionLab(t)
			libraryParent(lab)
			lab.parent["tmdbId"] = parentID
			out := libraryDetail(t, s, uid)
			if out.TmdbID != 0 || out.Detail["name"] != "Monster (2022)" || out.Detail["overview"] != lab.parent["overview"] || out.Detail["poster_path"] != "https://cdn.example/monster.jpg" || out.Detail["first_air_date"] != "2022-09-21" {
				t.Fatalf("wrong parent presentation: %+v", out)
			}
			var seasons []int
			for _, season := range out.Status.Seasons {
				seasons = append(seasons, season.SeasonNumber)
			}
			if !reflect.DeepEqual(seasons, []int{1, 2, 3, 4}) || len(out.Matches) != 4 || !*out.Status.StatusKnown || out.Revision == "" {
				t.Fatalf("incomplete parent: %+v", out)
			}
			if lab.mutations != 0 {
				t.Fatal("opening title mutated Sonarr")
			}
		})
	}
}

func TestTVLibraryDetailUsesCustomMappingsForAnUnrelatedSeries(t *testing.T) {
	s, uid, _, lab := newCorrectionLab(t)
	libraryParent(lab)
	lab.lookupTVDB = 987654
	lab.extraSource = true
	lab.parent["tvdbId"], lab.parent["title"] = 987654, "Another anthology"
	lab.parent["seasons"] = lab.seasonMetadata()[:3] // specials, season 1, season 2
	if _, err := s.db.Exec(`INSERT INTO tv_match_overrides(tmdb_id,mode,tvdb_id,season_map,revision) VALUES(555,'custom',987654,'{"1":2,"2":1}',1)`); err != nil {
		t.Fatal(err)
	}
	out := libraryDetail(t, s, uid)
	if out.Detail["name"] != "Another anthology" || len(out.Status.Seasons) != 2 || len(out.Matches) != 1 || out.Matches[0].TmdbID != 555 {
		t.Fatalf("custom parent not grouped: %+v", out)
	}
	result, err := s.RequestTVLibrary(uid, TVLibraryRequest{InstanceID: out.InstanceID, SeriesID: 42, Revision: out.Revision, Seasons: []int{2}})
	if err != nil || !result.Success {
		t.Fatalf("custom request: %+v %v", result, err)
	}
	var source int
	var scope string
	if err := s.db.QueryRow(`SELECT tmdb_id,season_scope FROM request_log WHERE user_id=?`, uid).Scan(&source, &scope); err != nil {
		t.Fatal(err)
	}
	if source != 555 || scope != "[1]" {
		t.Fatalf("wrong source request: %d %s", source, scope)
	}
	assertOnlySeasons(t, lab, 2)
}

func TestTVLibraryDetailOrdinaryMissingAndUnmappedIdentity(t *testing.T) {
	s, uid, _, lab := newCorrectionLab(t)
	libraryParent(lab)
	_, instance, _ := s.resolveSonarr(uid, "")
	lab.parent["tvdbId"], lab.parent["tmdbId"] = 123, 456
	out := libraryDetail(t, s, uid)
	if out.TmdbID != 456 || out.Detail != nil {
		t.Fatalf("ordinary page changed: %+v", out)
	}
	lab.parent["tmdbId"] = 0
	if out, err := s.TVLibraryDetail(uid, instance, 42); err != nil || out.TmdbID != 0 || len(out.Status.Seasons) != 4 || len(out.Matches) != 0 {
		t.Fatalf("native identity should remain browsable without guessing a catalog ID: %+v %v", out, err)
	}
	lab.parent = nil
	if _, err := s.TVLibraryDetail(uid, instance, 42); !errors.Is(err, ErrTitleNotAvailable) {
		t.Fatal(err)
	}
}

func TestTVLibraryDetailKeepsEverySeasonWhenOneMatchFails(t *testing.T) {
	for _, tc := range []struct {
		name, sql string
		downID    int
	}{
		{name: "paused", sql: `INSERT INTO tv_match_overrides(tmdb_id,mode,tvdb_id,season_map,revision) VALUES(225634,'paused',389492,'{"1":2}',1)`},
		{name: "overlap", sql: `INSERT INTO tv_match_overrides(tmdb_id,mode,tvdb_id,season_map,revision) VALUES(555,'custom',389492,'{"1":2}',1)`},
		{name: "moved", sql: `INSERT INTO tv_match_overrides(tmdb_id,mode,tvdb_id,season_map,revision) VALUES(225634,'custom',12345,'{"1":1}',1)`},
		{name: "metadata unavailable", downID: 225634},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, uid, _, lab := newCorrectionLab(t)
			libraryParent(lab)
			lab.metadataDownID = tc.downID
			if tc.sql != "" {
				if _, err := s.db.Exec(tc.sql); err != nil {
					t.Fatal(err)
				}
			}
			out := libraryDetail(t, s, uid)
			if len(out.Status.Seasons) != 4 || len(out.Matches) != 3 || !*out.Status.StatusKnown {
				t.Fatalf("one failed mapping hid the series: %+v", out)
			}
			for _, season := range out.Status.Seasons {
				if (season.RequestBlockedReason != "") != (season.SeasonNumber == 2) {
					t.Fatalf("wrong season blocked: %+v", season)
				}
			}
			// A mixed valid/invalid explicit selection is refused BEFORE any write.
			if _, err := s.RequestTVLibrary(uid, TVLibraryRequest{InstanceID: out.InstanceID, SeriesID: 42, Revision: out.Revision, Seasons: []int{1, 2}}); err == nil || lab.mutations != 0 {
				t.Fatalf("unverified selection was submitted: %v, mutations %d", err, lab.mutations)
			}
			result, err := s.RequestTVLibrary(uid, TVLibraryRequest{InstanceID: out.InstanceID, SeriesID: 42, Revision: out.Revision, Seasons: []int{3}})
			if err != nil || !result.Success || !reflect.DeepEqual(result.AcceptedSeasons, []int{3}) {
				t.Fatalf("unrelated season was blocked: %+v %v", result, err)
			}
			assertOnlySeasons(t, lab, 3)
		})
	}
}

func TestTVLibraryDetailUnmappedFutureSeasonAndAllPausedRemainVisible(t *testing.T) {
	for _, allPaused := range []bool{false, true} {
		t.Run(fmt.Sprint(allPaused), func(t *testing.T) {
			s, uid, _, lab := newCorrectionLab(t)
			libraryParent(lab)
			lab.parent["seasons"] = append(lab.seasonMetadata(), map[string]any{"seasonNumber": 5})
			if allPaused {
				for _, id := range []int{113988, 225634, 286801, 299939} {
					if _, err := s.db.Exec(`INSERT INTO tv_match_overrides(tmdb_id,mode,revision) VALUES(?,'paused',1)`, id); err != nil {
						t.Fatal(err)
					}
				}
			}
			out := libraryDetail(t, s, uid)
			if len(out.Status.Seasons) != 5 || out.Status.Seasons[4].RequestBlockedReason != "tv_seasons_unmapped" {
				t.Fatalf("new season disappeared: %+v", out)
			}
			if allPaused && len(out.Matches) != 0 {
				t.Fatalf("paused mapping remained actionable: %+v", out.Matches)
			}
			if lab.mutations != 0 {
				t.Fatal("browsing mutated the library")
			}
		})
	}
}

func TestTVLibraryRequestsAllSkipsBlockedButFirstNeverChangesTarget(t *testing.T) {
	for _, scope := range []string{SeasonScopeAll, SeasonScopeFirst, SeasonScopePilot} {
		t.Run(scope, func(t *testing.T) {
			s, uid, _, lab := newCorrectionLab(t)
			libraryParent(lab)
			if _, err := s.db.Exec(`INSERT INTO tv_match_overrides(tmdb_id,mode,revision) VALUES(113988,'paused',1)`); err != nil {
				t.Fatal(err)
			}
			if err := s.SetGlobalSettings(GlobalSettings{RequireApproval: true, DefaultSeasonScope: scope}); err != nil {
				t.Fatal(err)
			}
			out := libraryDetail(t, s, uid)
			result, err := s.RequestTVLibrary(uid, TVLibraryRequest{InstanceID: out.InstanceID, SeriesID: 42, Revision: out.Revision})
			if scope == SeasonScopeAll {
				if err != nil || !result.Success || !reflect.DeepEqual(result.AcceptedSeasons, []int{2, 3, 4}) || !reflect.DeepEqual(result.SkippedSeasons, []int{1}) {
					t.Fatalf("all: %+v %v", result, err)
				}
			} else if err == nil || result != nil {
				t.Fatalf("first silently changed season: %+v %v", result, err)
			}
			if lab.mutations != 0 {
				t.Fatal("approval was bypassed")
			}
		})
	}
}

func TestTVLibraryRequestsTranslateSeasonsAndRefreshPartialStatus(t *testing.T) {
	s, uid, _, lab := newCorrectionLab(t)
	libraryParent(lab)
	out := libraryDetail(t, s, uid)
	result, err := s.RequestTVLibrary(uid, TVLibraryRequest{InstanceID: out.InstanceID, SeriesID: 42, Revision: out.Revision, Seasons: []int{2, 3}})
	if err != nil || !result.Success || !reflect.DeepEqual(result.AcceptedSeasons, []int{2, 3}) {
		t.Fatalf("result: %+v %v", result, err)
	}
	assertOnlySeasons(t, lab, 2, 3)
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM request_log WHERE user_id=? AND tmdb_id IN (225634,286801) AND instance_id=? AND season_scope='[1]'`, uid, out.InstanceID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("source rows: %d %v", count, err)
	}
	out = libraryDetail(t, s, uid)
	if out.Status.Status != StatusPartial || out.Status.Seasons[0].Status != StatusUnavailable || out.Status.Seasons[1].Status != StatusRequested || out.Status.Seasons[2].Status != StatusRequested {
		t.Fatalf("sibling statuses leaked: %+v", out.Status)
	}
}

func TestTVLibraryRequestsPreserveApprovalAndDefaultSeasonPolicy(t *testing.T) {
	for _, scope := range []string{SeasonScopeFirst, SeasonScopeLatest, SeasonScopeAll, SeasonScopePilot} {
		t.Run(scope, func(t *testing.T) {
			s, uid, _, lab := newCorrectionLab(t)
			libraryParent(lab)
			if err := s.SetGlobalSettings(GlobalSettings{RequireApproval: true, DefaultSeasonScope: scope}); err != nil {
				t.Fatal(err)
			}
			out := libraryDetail(t, s, uid)
			result, err := s.RequestTVLibrary(uid, TVLibraryRequest{InstanceID: out.InstanceID, SeriesID: 42, Revision: out.Revision, Seasons: []int{2}, SeasonScope: SeasonScopeLatest})
			if err != nil || !result.Success {
				t.Fatalf("result: %+v %v", result, err)
			}
			want := []int{1}
			if scope == SeasonScopeLatest {
				want = []int{4}
			}
			if scope == SeasonScopeAll {
				want = []int{1, 2, 3, 4}
			}
			if !reflect.DeepEqual(result.AcceptedSeasons, want) || lab.mutations != 0 {
				t.Fatalf("policy bypass: %+v, mutations %d", result, lab.mutations)
			}
			out = libraryDetail(t, s, uid)
			for _, season := range out.Status.Seasons {
				pending := false
				for _, n := range want {
					pending = pending || season.SeasonNumber == n
				}
				if (season.Status == StatusPending) != pending {
					t.Fatalf("approval spread to wrong season: %+v", season)
				}
			}
		})
	}
}

func TestTVLibraryRequestsRefuseStaleAndUnknownSelections(t *testing.T) {
	s, uid, _, lab := newCorrectionLab(t)
	libraryParent(lab)
	out := libraryDetail(t, s, uid)
	for _, req := range []TVLibraryRequest{
		{InstanceID: out.InstanceID, SeriesID: 42, Revision: "stale", Seasons: []int{2}},
		{InstanceID: out.InstanceID, SeriesID: 42, Revision: out.Revision, Seasons: []int{0}},
		{InstanceID: out.InstanceID, SeriesID: 42, Revision: out.Revision, Seasons: []int{9}},
		{InstanceID: "ungranted", SeriesID: 42, Revision: out.Revision, Seasons: []int{2}},
	} {
		if result, err := s.RequestTVLibrary(uid, req); err == nil || result != nil {
			t.Fatalf("unsafe request: %+v %v", result, err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO tv_match_overrides(tmdb_id,mode,tvdb_id,season_map,revision) VALUES(225634,'paused',389492,'{"1":2}',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestTVLibrary(uid, TVLibraryRequest{InstanceID: out.InstanceID, SeriesID: 42, Revision: out.Revision, Seasons: []int{2}}); err == nil {
		t.Fatal("stale mapping accepted")
	}
	if lab.mutations != 0 {
		t.Fatal("refused request mutated Sonarr")
	}
}

func TestTVLibraryRequestsExposePartialAcceptanceAndKeepQuota(t *testing.T) {
	s, uid, admin, lab := newCorrectionLab(t)
	libraryParent(lab)
	requireApproval(t, s)
	quotaLimit(t, s, admin, 0, "tv", "", 1)
	out := libraryDetail(t, s, uid)
	result, err := s.RequestTVLibrary(uid, TVLibraryRequest{InstanceID: out.InstanceID, SeriesID: 42, Revision: out.Revision, Seasons: []int{2, 3}})
	if err != nil || result.Success || !reflect.DeepEqual(result.AcceptedSeasons, []int{2}) || result.Error == "" {
		t.Fatalf("partial result: %+v %v", result, err)
	}
	if quotaUsed(t, s, uid, "tv", "") != 1 || quotaRows(t, s, "request_log") != 1 || lab.mutations != 0 {
		t.Fatal("quota or approval bypassed")
	}
	out = libraryDetail(t, s, uid)
	if out.Status.Seasons[1].Status != StatusPending || out.Status.Seasons[2].Status != StatusUnavailable {
		t.Fatalf("partial status: %+v", out.Status)
	}
	_, err = s.RequestTVLibrary(uid, TVLibraryRequest{InstanceID: out.InstanceID, SeriesID: 42, Revision: out.Revision, Seasons: []int{3}})
	quotaMustExceed(t, err)
}

func TestTVLibraryStatusOutageDoesNotEnableRequests(t *testing.T) {
	s, uid, _, lab := newCorrectionLab(t)
	libraryParent(lab)
	lab.episodesDown = true
	out := libraryDetail(t, s, uid)
	if *out.Status.StatusKnown || len(out.Status.Seasons) != 4 {
		t.Fatalf("outage: %+v", out.Status)
	}
	if _, err := s.RequestTVLibrary(uid, TVLibraryRequest{InstanceID: out.InstanceID, SeriesID: 42, Revision: out.Revision, Seasons: []int{2}}); !errors.Is(err, ErrTVMatchStale) {
		t.Fatal(err)
	}
	if lab.mutations != 0 {
		t.Fatal("unknown status mutated Sonarr")
	}
}

func TestTVLibraryPreparedRequestRechecksSourceRevisionAndNativeRecord(t *testing.T) {
	for _, change := range []string{"revision", "record", "seasons", "another source overlaps"} {
		t.Run(change, func(t *testing.T) {
			s, uid, _, lab := newCorrectionLab(t)
			libraryParent(lab)
			out := libraryDetail(t, s, uid)
			match := out.Matches[1]
			proof := &tvLibraryScope{seriesID: 42, revision: match.Revision, targetSeasons: []int{2}}
			switch change {
			case "revision":
				proof.revision = "old"
			case "record":
				proof.seriesID = 99
			case "seasons":
				proof.targetSeasons = []int{3}
			case "another source overlaps":
				if _, err := s.db.Exec(`INSERT INTO tv_match_overrides(tmdb_id,mode,tvdb_id,season_map,revision) VALUES(555,'custom',389492,'{"1":2}',1)`); err != nil {
					t.Fatal(err)
				}
			}
			_, err := s.CreateMediaRequest(uid, &CreateRequest{MediaType: "tv", TmdbID: match.TmdbID, InstanceID: out.InstanceID, Seasons: []int{1}, tvLibraryScope: proof})
			if !errors.Is(err, ErrTVMatchStale) || lab.mutations != 0 {
				t.Fatalf("proof bypass: %v, mutations %d", err, lab.mutations)
			}
		})
	}
}

type libraryRatings struct {
	down       bool
	allAllowed bool
	onRead     func()
}

func (f *libraryRatings) DoGetRaw(path string, _ url.Values) ([]byte, error) {
	if f.onRead != nil {
		f.onRead()
	}
	if f.down {
		return nil, errors.New("unavailable")
	}
	rating := "TV-MA"
	if f.allAllowed || path == "/tv/225634/content_ratings" || path == "/tv/456/content_ratings" {
		rating = "TV-PG"
	}
	return []byte(fmt.Sprintf(`{"results":[{"iso_3166_1":"US","rating":%q}]}`, rating)), nil
}

func TestTVLibraryDetailAppliesKidsPolicyToEntireParent(t *testing.T) {
	for _, down := range []bool{false, true} {
		t.Run(fmt.Sprint(down), func(t *testing.T) {
			s, uid, _, lab := newCorrectionLab(t)
			libraryParent(lab)
			policy := contentpolicy.New(s.db, func() contentpolicy.RawGetter { return &libraryRatings{down: down} }, nil)
			s.SetContentPolicy(policy)
			if err := policy.Store.Set(uid, contentpolicy.Policy{MaxMovieRating: "PG", MaxTVRating: "TV-PG", RatingRegion: "US", BlockUnrated: true}); err != nil {
				t.Fatal(err)
			}
			_, instance, _ := s.resolveSonarr(uid, "")
			out, err := s.TVLibraryDetail(uid, instance, 42)
			want := ErrTitleNotAvailable
			if down {
				want = ErrContentPolicyUnavailable
			}
			if !errors.Is(err, want) || out != nil {
				t.Fatalf("parent policy leaked: %+v %v", out, err)
			}
		})
	}
}

func TestTVLibraryIncompleteMatchesNeverBypassKidsPolicy(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		allAllowed, unmapped bool
		want                 error
	}{
		{name: "paused blocked story", want: ErrTitleNotAvailable},
		{name: "paused allowed story", allAllowed: true},
		{name: "unmapped story", allAllowed: true, unmapped: true, want: ErrContentPolicyUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, uid, _, lab := newCorrectionLab(t)
			libraryParent(lab)
			if _, err := s.db.Exec(`INSERT INTO tv_match_overrides(tmdb_id,mode,revision) VALUES(113988,'paused',1)`); err != nil {
				t.Fatal(err)
			}
			if tc.unmapped {
				lab.parent["seasons"] = append(lab.seasonMetadata(), map[string]any{"seasonNumber": 5})
			}
			policy := contentpolicy.New(s.db, func() contentpolicy.RawGetter { return &libraryRatings{allAllowed: tc.allAllowed} }, nil)
			s.SetContentPolicy(policy)
			if err := policy.Store.Set(uid, contentpolicy.Policy{MaxMovieRating: "PG", MaxTVRating: "TV-PG", RatingRegion: "US", BlockUnrated: true}); err != nil {
				t.Fatal(err)
			}
			_, instance, _ := s.resolveSonarr(uid, "")
			out, err := s.TVLibraryDetail(uid, instance, 42)
			if !errors.Is(err, tc.want) || (tc.want != nil && out != nil) {
				t.Fatalf("policy: %+v %v", out, err)
			}
			if tc.want == nil && (out == nil || len(out.Status.Seasons) != 4 || out.Status.Seasons[0].RequestBlockedReason != "tv_match_paused") {
				t.Fatalf("allowed parent: %+v", out)
			}
		})
	}
}

func TestTVLibraryUnmappedAvailabilityIsLiveAndRepairsRefresh(t *testing.T) {
	s, uid, _, lab := newCorrectionLab(t)
	libraryParent(lab)
	if _, err := s.db.Exec(`INSERT INTO tv_match_overrides(tmdb_id,mode,revision) VALUES(225634,'paused',1)`); err != nil {
		t.Fatal(err)
	}
	for _, ep := range lab.episodes {
		if ep["seasonNumber"] == 2 {
			ep["hasFile"] = true
		}
	}
	out := libraryDetail(t, s, uid)
	row := out.Status.Seasons[1]
	if row.Status != StatusAvailable || row.EpisodeFileCount != 2 || row.RequestBlockedReason != "tv_match_paused" {
		t.Fatalf("native availability lost: %+v", row)
	}
	oldRevision := out.Revision
	if _, err := s.db.Exec(`UPDATE tv_match_overrides SET mode='default',revision=revision+1 WHERE tmdb_id=225634`); err != nil {
		t.Fatal(err)
	}
	out = libraryDetail(t, s, uid)
	if out.Revision == oldRevision || out.Status.Seasons[1].RequestBlockedReason != "" || len(out.Matches) != 4 {
		t.Fatalf("repair not reflected: %+v", out)
	}
	if lab.mutations != 0 {
		t.Fatal("a read changed Sonarr")
	}
}

func TestTVLibraryCustomPartialMappingDoesNotNeedBundledCorrections(t *testing.T) {
	s, uid, _, lab := newCorrectionLab(t)
	libraryParent(lab)
	lab.lookupTVDB = 987654
	lab.parent["tvdbId"], lab.parent["title"] = 987654, "Unrelated series"
	lab.parent["seasons"] = lab.seasonMetadata()[:3]
	if _, err := s.db.Exec(`INSERT INTO tv_match_overrides(tmdb_id,mode,tvdb_id,season_map,revision) VALUES(555,'custom',987654,'{"1":2}',1)`); err != nil {
		t.Fatal(err)
	}
	out := libraryDetail(t, s, uid)
	if len(out.Status.Seasons) != 2 || out.Status.Seasons[0].RequestBlockedReason != "tv_seasons_unmapped" || out.Status.Seasons[1].RequestBlockedReason != "" {
		t.Fatalf("partial custom series: %+v", out)
	}
	result, err := s.RequestTVLibrary(uid, TVLibraryRequest{InstanceID: out.InstanceID, SeriesID: 42, Revision: out.Revision, Seasons: []int{2}})
	if err != nil || !result.Success {
		t.Fatalf("valid custom season: %+v %v", result, err)
	}
	assertOnlySeasons(t, lab, 2)
}

func TestTVLibraryDetailKeepsExplicitLibraryAndRechecksGrant(t *testing.T) {
	s, uid, _, lab := newCorrectionLab(t)
	libraryParent(lab)
	other, _, _, otherLab := newCorrectionLab(t)
	libraryParent(otherLab)
	otherLab.parent["tvdbId"], otherLab.parent["tmdbId"] = 123, 456
	_, primary, _ := s.resolveSonarr(uid, "")
	var otherURL string
	if err := other.db.QueryRow(`SELECT url FROM service_instances WHERE service_type='sonarr'`).Scan(&otherURL); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO service_instances(id,service_type,name,url,api_key) SELECT 'tv-other',service_type,'Other library',?,api_key FROM service_instances WHERE id=?`, otherURL, primary); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TVLibraryDetail(uid, "tv-other", 42); !errors.Is(err, ErrArrInstanceForbidden) {
		t.Fatalf("ungranted: %v", err)
	}
	if _, err := s.db.Exec(`INSERT INTO user_instance_grants(user_id,instance_id) VALUES(?,?),(?,'tv-other')`, uid, primary, uid); err != nil {
		t.Fatal(err)
	}
	out, err := s.TVLibraryDetail(uid, "tv-other", 42)
	if err != nil || out.InstanceID != "tv-other" || out.TmdbID != 456 {
		t.Fatalf("wrong library: %+v %v", out, err)
	}
	policy := contentpolicy.New(s.db, func() contentpolicy.RawGetter {
		return &libraryRatings{onRead: func() {
			_, _ = s.db.Exec(`DELETE FROM user_instance_grants WHERE user_id=? AND instance_id='tv-other'`, uid)
		}}
	}, nil)
	s.SetContentPolicy(policy)
	if err := policy.Store.Set(uid, contentpolicy.Policy{MaxMovieRating: "PG", MaxTVRating: "TV-PG", RatingRegion: "US", BlockUnrated: true}); err != nil {
		t.Fatal(err)
	}
	if out, err = s.TVLibraryDetail(uid, "tv-other", 42); !errors.Is(err, ErrArrInstanceForbidden) || out != nil {
		t.Fatalf("revoked grant: %+v %v", out, err)
	}
}

func TestTVLibraryHandlersValidateNativeIdentity(t *testing.T) {
	s, uid, _, _ := newCorrectionLab(t)
	h := &Handler{service: s}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, authenticated := range []bool{false, true} {
			r := httptest.NewRequest(method, "/?instance_id=a&series_id=0", strings.NewReader(`{"instance_id":"a","series_id":0}`))
			if authenticated {
				r = r.WithContext(context.WithValue(r.Context(), auth.ClaimsKey, &auth.Claims{UserID: uid}))
			}
			w := httptest.NewRecorder()
			if method == http.MethodGet {
				h.GetTVLibrary(w, r)
			} else {
				h.CreateTVLibraryRequest(w, r)
			}
			want := 401
			if authenticated {
				want = 400
			}
			if w.Code != want {
				t.Fatalf("%s: %d %s", method, w.Code, w.Body.String())
			}
		}
	}
}
