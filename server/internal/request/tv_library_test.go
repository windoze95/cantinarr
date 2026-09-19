package request

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
)

func libraryParent(lab *correctionLab) {
	lab.parent = map[string]any{"id": 42, "tvdbId": 389492, "tmdbId": 0, "title": "Monster (2022)", "seasons": lab.seasonMetadata()}
}

func TestTVLibraryTitlesResolveZeroAndPositiveParentIDs(t *testing.T) {
	for _, parentID := range []int{0, 113988} {
		t.Run(fmt.Sprint(parentID), func(t *testing.T) {
			s, uid, _, lab := newCorrectionLab(t)
			libraryParent(lab)
			lab.parent["tmdbId"] = parentID
			_, instanceID, _ := s.resolveSonarr(uid, "")
			all, err := s.TVLibraryDestinations(uid, instanceID, 42, nil)
			if err != nil {
				t.Fatal(err)
			}
			var ids []int
			for _, title := range all.Titles {
				ids = append(ids, title.TmdbID)
			}
			if all.InstanceID != instanceID || !reflect.DeepEqual(ids, []int{113988, 225634, 286801, 299939}) {
				t.Fatalf("destinations: %+v", all)
			}
			for season, id := range map[int]int{1: 113988, 2: 225634, 3: 286801, 4: 299939} {
				one, err := s.TVLibraryDestinations(uid, instanceID, 42, &season)
				if err != nil || len(one.Titles) != 1 || one.Titles[0].TmdbID != id {
					t.Fatalf("season %d: %+v %v", season, one, err)
				}
			}
			if lab.mutations != 0 {
				t.Fatal("navigation mutated Sonarr")
			}
		})
	}
}

func TestTVLibraryTitlesNeverReturnPartialOrGuessedChoices(t *testing.T) {
	for _, tc := range []struct {
		name, sql string
		down      bool
		season    *int
	}{
		{name: "paused", sql: `INSERT INTO tv_match_overrides(tmdb_id,mode,tvdb_id,season_map,revision) VALUES(225634,'paused',389492,'{"1":2}',1)`},
		{name: "overlap", sql: `INSERT INTO tv_match_overrides(tmdb_id,mode,tvdb_id,season_map,revision) VALUES(555,'custom',389492,'{"1":2}',1)`},
		{name: "moved", sql: `INSERT INTO tv_match_overrides(tmdb_id,mode,tvdb_id,season_map,revision) VALUES(225634,'custom',12345,'{"1":1}',1)`},
		{name: "metadata unavailable", down: true},
		{name: "unknown season", season: func() *int { n := 5; return &n }()},
		{name: "specials", season: func() *int { n := 0; return &n }()},
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
			_, instanceID, _ := s.resolveSonarr(uid, "")
			out, err := s.TVLibraryDestinations(uid, instanceID, 42, tc.season)
			if err == nil || out != nil {
				t.Fatalf("unsafe destinations: %+v %v", out, err)
			}
		})
	}
}

func TestTVLibraryTitlesReadCurrentCustomMapping(t *testing.T) {
	s, uid, _, lab := newCorrectionLab(t)
	libraryParent(lab)
	_, instanceID, _ := s.resolveSonarr(uid, "")
	season := 2
	for _, sql := range []string{
		`INSERT INTO tv_match_overrides(tmdb_id,mode,tvdb_id,season_map,revision) VALUES(113988,'custom',389492,'{"1":2}',1)`,
		`INSERT INTO tv_match_overrides(tmdb_id,mode,tvdb_id,season_map,revision) VALUES(225634,'custom',389492,'{"1":1}',1)`,
	} {
		if _, err := s.db.Exec(sql); err != nil {
			t.Fatal(err)
		}
	}
	out, err := s.TVLibraryDestinations(uid, instanceID, 42, &season)
	if err != nil || len(out.Titles) != 1 || out.Titles[0].TmdbID != 113988 {
		t.Fatalf("custom: %+v %v", out, err)
	}
	if _, err := s.db.Exec(`DELETE FROM tv_match_overrides`); err != nil {
		t.Fatal(err)
	}
	out, err = s.TVLibraryDestinations(uid, instanceID, 42, &season)
	if err != nil || len(out.Titles) != 1 || out.Titles[0].TmdbID != 225634 {
		t.Fatalf("reset: %+v %v", out, err)
	}
}

func TestTVLibraryTitlesNormalAndMissingIdentity(t *testing.T) {
	s, uid, _, lab := newCorrectionLab(t)
	libraryParent(lab)
	_, instanceID, _ := s.resolveSonarr(uid, "")
	lab.parent["tvdbId"] = 123
	lab.parent["tmdbId"] = 456
	lab.parent["title"] = "Normal series"
	out, err := s.TVLibraryDestinations(uid, instanceID, 42, nil)
	if err != nil || len(out.Titles) != 1 || out.Titles[0].TmdbID != 456 {
		t.Fatalf("normal: %+v %v", out, err)
	}
	lab.parent["tmdbId"] = 0
	if out, err = s.TVLibraryDestinations(uid, instanceID, 42, nil); err == nil || out != nil {
		t.Fatalf("zero: %+v %v", out, err)
	}
	lab.parent = nil
	if _, err = s.TVLibraryDestinations(uid, instanceID, 42, nil); !errors.Is(err, ErrTitleNotAvailable) {
		t.Fatal(err)
	}
	if _, err = s.TVLibraryDestinations(uid, "not-granted", 42, nil); err == nil {
		t.Fatal("unknown library accepted")
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

func TestTVLibraryTitlesKeepExplicitLibraryAndRecheckRevokedGrant(t *testing.T) {
	s, uid, _, lab := newCorrectionLab(t)
	libraryParent(lab)
	other, _, _, otherLab := newCorrectionLab(t)
	libraryParent(otherLab)
	otherLab.parent["tvdbId"], otherLab.parent["tmdbId"], otherLab.parent["title"] = 123, 456, "Other library title"
	_, primary, _ := s.resolveSonarr(uid, "")
	var otherURL string
	if err := other.db.QueryRow(`SELECT url FROM service_instances WHERE service_type='sonarr'`).Scan(&otherURL); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO service_instances(id,service_type,name,url,api_key) SELECT 'tv-other',service_type,'Other library',?,api_key FROM service_instances WHERE id=?`, otherURL, primary); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TVLibraryDestinations(uid, "tv-other", 42, nil); !errors.Is(err, ErrArrInstanceForbidden) {
		t.Fatalf("ungranted: %v", err)
	}
	if _, err := s.db.Exec(`INSERT INTO user_instance_grants(user_id,instance_id) VALUES(?,?),(?,'tv-other')`, uid, primary, uid); err != nil {
		t.Fatal(err)
	}
	out, err := s.TVLibraryDestinations(uid, "tv-other", 42, nil)
	if err != nil || out.InstanceID != "tv-other" || len(out.Titles) != 1 || out.Titles[0].Title != "Other library title" {
		t.Fatalf("wrong library: %+v %v", out, err)
	}
	ratings := &libraryRatings{onRead: func() {
		if _, err := s.db.Exec(`DELETE FROM user_instance_grants WHERE user_id=? AND instance_id='tv-other'`, uid); err != nil {
			t.Error(err)
		}
	}}
	policy := contentpolicy.New(s.db, func() contentpolicy.RawGetter { return ratings }, nil)
	s.SetContentPolicy(policy)
	if err := policy.Store.Set(uid, contentpolicy.Policy{MaxMovieRating: "PG", MaxTVRating: "TV-PG", RatingRegion: "US", BlockUnrated: true}); err != nil {
		t.Fatal(err)
	}
	out, err = s.TVLibraryDestinations(uid, "tv-other", 42, nil)
	if !errors.Is(err, ErrArrInstanceForbidden) || out != nil {
		t.Fatalf("revoked grant returned titles: %+v %v", out, err)
	}
}

func TestTVLibraryTitlesApplyKidsPolicyToEachStory(t *testing.T) {
	for _, down := range []bool{false, true} {
		t.Run(fmt.Sprint(down), func(t *testing.T) {
			s, uid, _, lab := newCorrectionLab(t)
			libraryParent(lab)
			ratings := &libraryRatings{down: down}
			policy := contentpolicy.New(s.db, func() contentpolicy.RawGetter { return ratings }, nil)
			s.SetContentPolicy(policy)
			if err := policy.Store.Set(uid, contentpolicy.Policy{MaxMovieRating: "PG", MaxTVRating: "TV-PG", RatingRegion: "US", BlockUnrated: true}); err != nil {
				t.Fatal(err)
			}
			_, instanceID, _ := s.resolveSonarr(uid, "")
			out, err := s.TVLibraryDestinations(uid, instanceID, 42, nil)
			if down {
				if !errors.Is(err, ErrContentPolicyUnavailable) || out != nil {
					t.Fatalf("policy outage: %+v %v", out, err)
				}
				return
			}
			if err != nil || len(out.Titles) != 1 || out.Titles[0].TmdbID != 225634 {
				t.Fatalf("policy: %+v %v", out, err)
			}
		})
	}
}

func TestTVLibraryTitlesHandlerValidation(t *testing.T) {
	s, uid, _, lab := newCorrectionLab(t)
	libraryParent(lab)
	h := &Handler{service: s}
	for _, query := range []string{"", "?instance_id=a&series_id=0", "?instance_id=a&series_id=42&season_number=-1", "?instance_id=a&series_id=42&season_number=no"} {
		r := httptest.NewRequest("GET", "/"+query, nil)
		r = r.WithContext(context.WithValue(r.Context(), auth.ClaimsKey, &auth.Claims{UserID: uid}))
		w := httptest.NewRecorder()
		h.GetTVLibraryTitles(w, r)
		if w.Code != 400 {
			t.Fatalf("%s: %d %s", query, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	h.GetTVLibraryTitles(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
}
