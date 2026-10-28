package request

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
	"github.com/windoze95/cantinarr-server/internal/mediametadata"
)

type presentationFake struct {
	calls        atomic.Int32
	episodeCalls atomic.Int32
	read         func(context.Context, string, int, string) (*mediametadata.Presentation, error)
	episode      func(context.Context, int, int, int, int, string) (*mediametadata.Presentation, error)
}

func (f *presentationFake) Presentation(ctx context.Context, kind string, id int, language string) (*mediametadata.Presentation, error) {
	f.calls.Add(1)
	if f.read != nil {
		return f.read(ctx, kind, id, language)
	}
	return &mediametadata.Presentation{Title: "Titre français", Overview: "Résumé", PosterPath: "/fr.jpg"}, nil
}
func (f *presentationFake) Episode(ctx context.Context, id, season, episode, tvdb int, language string) (*mediametadata.Presentation, error) {
	f.episodeCalls.Add(1)
	if f.episode != nil {
		return f.episode(ctx, id, season, episode, tvdb, language)
	}
	return &mediametadata.Presentation{Title: "Épisode français"}, nil
}

func TestNativePresentationPreservesProviderAndCorrectedParent(t *testing.T) {
	for _, corrected := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "combined"}[corrected], func(t *testing.T) {
			s, uid, _, lab := newCorrectionLab(t)
			libraryParent(lab)
			if !corrected {
				lab.parent["tvdbId"], lab.parent["tmdbId"] = 123, 456
			} else {
				lab.parent["tmdbId"] = 113988
			}
			before, _ := json.Marshal(lab.parent)
			source := &presentationFake{}
			s.SetMetadataService(source)
			_, instance, _ := s.resolveSonarr(uid, "")
			got, err := s.NativePresentation(context.Background(), uid, "tv", instance, 42, 0, "fr-FR")
			if err != nil {
				t.Fatal(err)
			}
			if corrected {
				if got != nil || source.calls.Load() != 0 {
					t.Fatalf("localized combined parent %+v", got)
				}
			} else if got == nil || got.Title != "Titre français" || got.TMDBID != 456 || got.MediaType != "tv" {
				t.Fatalf("ordinary %+v", got)
			}
			after, _ := json.Marshal(lab.parent)
			if string(before) != string(after) || lab.mutations != 0 {
				t.Fatal("presentation changed native state")
			}
		})
	}
}
func TestPresentationOutageRetainsSourceAndDoesNotBypassKidsPolicy(t *testing.T) {
	for _, mode := range []string{"metadata_down", "policy_down", "blocked"} {
		t.Run(mode, func(t *testing.T) {
			s, uid, _, _ := newCorrectionLab(t)
			source := &presentationFake{read: func(context.Context, string, int, string) (*mediametadata.Presentation, error) {
				return nil, errors.New("translation unavailable")
			}}
			s.SetMetadataService(source)
			if mode != "metadata_down" {
				policy := contentpolicy.New(s.db, func() contentpolicy.RawGetter { return &libraryRatings{down: mode == "policy_down"} }, nil)
				s.SetContentPolicy(policy)
				if err := policy.Store.Set(uid, contentpolicy.Policy{MaxMovieRating: "PG", MaxTVRating: "TV-PG", RatingRegion: "US", BlockUnrated: true}); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.Presentations(context.Background(), uid, []PresentationReference{{Key: "a", MediaType: "tv", TMDBID: 113988}}, "fr-FR")
			switch mode {
			case "metadata_down":
				if err != nil || got == nil || len(got.Presentations) != 0 || got.Unavailable["a"] == "" {
					t.Fatalf("fallback %+v %v", got, err)
				}
			case "policy_down":
				if !errors.Is(err, ErrContentPolicyUnavailable) || got != nil || source.calls.Load() != 0 {
					t.Fatalf("policy outage %+v %v", got, err)
				}
			case "blocked":
				if !errors.Is(err, ErrTitleNotAvailable) || got != nil || source.calls.Load() != 0 {
					t.Fatalf("blocked %+v %v", got, err)
				}
			}
		})
	}
}
func TestNativePresentationRechecksRevokedGrant(t *testing.T) {
	s, uid, _, lab := newCorrectionLab(t)
	libraryParent(lab)
	lab.parent["tvdbId"], lab.parent["tmdbId"] = 123, 456
	_, instance, _ := s.resolveSonarr(uid, "")
	source := &presentationFake{read: func(context.Context, string, int, string) (*mediametadata.Presentation, error) {
		if _, err := s.db.Exec(`DELETE FROM user_instance_grants WHERE user_id=? AND instance_id=?`, uid, instance); err != nil {
			t.Error(err)
		}
		return &mediametadata.Presentation{Title: "Must not escape"}, nil
	}}
	s.SetMetadataService(source)
	got, err := s.Presentations(context.Background(), uid, []PresentationReference{{Key: "native", MediaType: "tv", InstanceID: instance, RecordID: 42}}, "fr-FR")
	if !errors.Is(err, ErrArrInstanceForbidden) || got != nil {
		t.Fatalf("revoked metadata escaped %+v %v", got, err)
	}
}
func TestNativePresentationEpisodeUsesExactExternalIdentity(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "unique", true: "duplicate"}[duplicate], func(t *testing.T) {
			s, uid, _, lab := newCorrectionLab(t)
			libraryParent(lab)
			lab.parent["tvdbId"], lab.parent["tmdbId"] = 123, 456
			lab.episodes[0]["seriesId"], lab.episodes[0]["tvdbId"] = 42, 900
			if duplicate {
				lab.episodes[1]["seriesId"], lab.episodes[1]["tvdbId"] = 42, 900
			}
			source := &presentationFake{episode: func(_ context.Context, id, season, episode, tvdb int, language string) (*mediametadata.Presentation, error) {
				if id != 456 || season != 1 || episode != 1 || tvdb != 900 || language != "fr-FR" {
					t.Errorf("wrong episode proof %d %d %d %d %q", id, season, episode, tvdb, language)
				}
				return &mediametadata.Presentation{Title: "Épisode français"}, nil
			}}
			s.SetMetadataService(source)
			_, instance, _ := s.resolveSonarr(uid, "")
			got, err := s.NativePresentation(context.Background(), uid, "tv", instance, 42, 11, "fr-FR")
			if err != nil {
				t.Fatal(err)
			}
			if duplicate {
				if got != nil || source.episodeCalls.Load() != 0 {
					t.Fatalf("ambiguous episode accepted %+v", got)
				}
			} else if got == nil || got.Title != "Titre français" || got.EpisodeTitle != "Épisode français" {
				t.Fatalf("episode %+v", got)
			}
			if lab.mutations != 0 {
				t.Fatal("episode projection mutated provider")
			}
		})
	}
}
func TestPresentationBatchRejectsInvalidAndOversizedReferences(t *testing.T) {
	s, uid, _, _ := newCorrectionLab(t)
	source := &presentationFake{}
	s.SetMetadataService(source)
	handler := &Handler{service: s}
	refs := make([]PresentationReference, 51)
	for i := range refs {
		refs[i] = PresentationReference{Key: "key", MediaType: "movie", TMDBID: 1}
	}
	oversized, _ := json.Marshal(map[string]any{"references": refs})
	for _, body := range []string{string(oversized), `{"references":[]}`, `{"references":[{"key":"a","media_type":"movie","tmdb_id":1,"instance_id":"library","record_id":1}]}`, `{"references":[{"key":"a","media_type":"movie","tmdb_id":1},{"key":"a","media_type":"movie","tmdb_id":2}]}`} {
		req := httptest.NewRequest("POST", "/media/presentation?metadata_language=fr-FR", strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: uid}))
		rec := httptest.NewRecorder()
		handler.GetPresentations(rec, req)
		if rec.Code != 400 {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
	}
	if source.calls.Load() != 0 {
		t.Fatal("invalid batch reached metadata")
	}
}

func TestCombinedTVPresentationKeepsParentRevisionAndCanonicalMatches(t *testing.T) {
	s, uid, _, lab := newCorrectionLab(t)
	libraryParent(lab)
	out := libraryDetail(t, s, uid)
	revision := out.Revision
	beforeMatches, _ := json.Marshal(out.Matches)
	parentName, parentOverview, parentPoster := out.Detail["name"], out.Detail["overview"], out.Detail["poster_path"]
	source := &presentationFake{read: func(_ context.Context, _ string, id int, _ string) (*mediametadata.Presentation, error) {
		return &mediametadata.Presentation{Title: fmt.Sprintf("Titre français %d", id)}, nil
	}}
	s.SetMetadataService(source)
	if err := s.localizeTVLibrary(context.Background(), uid, out, "fr-FR"); err != nil {
		t.Fatal(err)
	}
	afterMatches, _ := json.Marshal(out.Matches)
	if out.Revision != revision || string(beforeMatches) != string(afterMatches) || out.Detail["name"] != parentName || out.Detail["overview"] != parentOverview || out.Detail["poster_path"] != parentPoster {
		t.Fatalf("canonical/native data changed: %+v", out)
	}
	for _, season := range out.Detail["seasons"].([]map[string]any) {
		if !strings.HasPrefix(season["name"].(string), "Titre français ") || season["source_name"] == "" {
			t.Fatalf("season presentation: %+v", season)
		}
	}
	if lab.mutations != 0 {
		t.Fatal("presentation mutated provider")
	}
}
func TestCombinedTVPresentationRechecksGrantAndCorrectionDuringEnrichment(t *testing.T) {
	for _, change := range []string{"grant", "correction"} {
		t.Run(change, func(t *testing.T) {
			s, uid, _, lab := newCorrectionLab(t)
			libraryParent(lab)
			out := libraryDetail(t, s, uid)
			var once sync.Once
			source := &presentationFake{read: func(context.Context, string, int, string) (*mediametadata.Presentation, error) {
				once.Do(func() {
					var err error
					if change == "grant" {
						_, err = s.db.Exec(`DELETE FROM user_instance_grants WHERE user_id=? AND instance_id=?`, uid, out.InstanceID)
					} else {
						_, err = s.db.Exec(`INSERT INTO tv_match_overrides(tmdb_id,mode,revision) VALUES(113988,'paused',1)`)
					}
					if err != nil {
						t.Error(err)
					}
				})
				return &mediametadata.Presentation{Title: "Must not escape"}, nil
			}}
			s.SetMetadataService(source)
			err := s.localizeTVLibrary(context.Background(), uid, out, "fr-FR")
			want := ErrArrInstanceForbidden
			if change == "correction" {
				want = ErrTVMatchStale
			}
			if !errors.Is(err, want) {
				t.Fatalf("%s change returned %v, want %v", change, err, want)
			}
		})
	}
}
