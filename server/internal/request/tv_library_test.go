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
	if out, err := s.TVLibraryDetail(uid, instance, 42); err == nil || out != nil {
		t.Fatalf("guessed zero ID: %+v %v", out, err)
	}
	lab.parent = nil
	if _, err := s.TVLibraryDetail(uid, instance, 42); !errors.Is(err, ErrTitleNotAvailable) {
		t.Fatal(err)
	}
}

func TestTVLibraryDetailNeverReturnsIncompleteOrGuessedSeasons(t *testing.T) {
	for _, tc := range []struct {
		name, sql string
		down      bool
	}{
		{name: "paused", sql: `INSERT INTO tv_match_overrides(tmdb_id,mode,tvdb_id,season_map,revision) VALUES(225634,'paused',389492,'{"1":2}',1)`},
		{name: "overlap", sql: `INSERT INTO tv_match_overrides(tmdb_id,mode,tvdb_id,season_map,revision) VALUES(555,'custom',389492,'{"1":2}',1)`},
		{name: "moved", sql: `INSERT INTO tv_match_overrides(tmdb_id,mode,tvdb_id,season_map,revision) VALUES(225634,'custom',12345,'{"1":1}',1)`},
		{name: "metadata unavailable", down: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, uid, _, lab := newCorrectionLab(t)
			libraryParent(lab)
			lab.metadataDown = tc.down
			if tc.sql != "" {
				if _, err := s.db.Exec(tc.sql); err != nil {
					t.Fatal(err)
				}
			}
			_, instance, _ := s.resolveSonarr(uid, "")
			if out, err := s.TVLibraryDetail(uid, instance, 42); err == nil || out != nil {
				t.Fatalf("unsafe parent: %+v %v", out, err)
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
	for _, change := range []string{"revision", "record", "seasons"} {
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
			}
			_, err := s.CreateMediaRequest(uid, &CreateRequest{MediaType: "tv", TmdbID: match.TmdbID, InstanceID: out.InstanceID, Seasons: []int{1}, tvLibraryScope: proof})
			if !errors.Is(err, ErrTVMatchStale) || lab.mutations != 0 {
				t.Fatalf("proof bypass: %v, mutations %d", err, lab.mutations)
			}
		})
	}
}

type libraryRatings struct {
	down   bool
	onRead func()
}

func (f *libraryRatings) DoGetRaw(path string, _ url.Values) ([]byte, error) {
	if f.onRead != nil {
		f.onRead()
	}
	if f.down {
		return nil, errors.New("unavailable")
	}
	rating := "TV-MA"
	if path == "/tv/225634/content_ratings" || path == "/tv/456/content_ratings" {
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
