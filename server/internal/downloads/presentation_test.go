package downloads

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/mediametadata"
)

func TestActivityPresentationKeepsSourceCacheAndSummaryUnchanged(t *testing.T) {
	e := newActivityEnv(t)
	arr := e.add(t, "radarr", "http://arr.invalid")
	cacheSource(e, arr, sourceSnapshot{rows: []activityRow{movieRow(7, 100, "download", 100, 60)}})
	var calls atomic.Int32
	e.h.SetPresentationResolver(func(_ context.Context, user int64, kind, instance string, record, episode int, language string) (*mediametadata.Presentation, error) {
		calls.Add(1)
		if user != 2 || kind != "movie" || instance != arr.ID || record != 7 || episode != 0 || language != "fr-FR" {
			t.Errorf("unexpected identity/language: %d %s %s %d %d %s", user, kind, instance, record, episode, language)
		}
		return &mediametadata.Presentation{Title: "Titre français", PosterPath: "/fr.jpg"}, nil
	})
	view, w := e.get(t, 2, "/api/downloads/activity?metadata_language=fr-FR")
	if w.Code != 200 || len(view.Groups) != 1 || view.Groups[0].Title != "Movie 7" || view.Groups[0].Presentation == nil || view.Groups[0].Presentation.Title != "Titre français" {
		t.Fatalf("localized view: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "recordID") || strings.Contains(w.Body.String(), "PRIVATE.release.filename") {
		t.Fatal("internal identity or release name exposed")
	}
	source, w := e.get(t, 2, "/api/downloads/activity")
	if w.Code != 200 || source.Groups[0].Presentation != nil || source.Groups[0].Title != "Movie 7" {
		t.Fatalf("shared source changed: %s", w.Body.String())
	}
	summary, w := e.get(t, 2, "/api/downloads/summary?metadata_language=fr-FR")
	if w.Code != 200 || len(summary.Groups) != 0 || calls.Load() != 1 {
		t.Fatalf("summary fetched presentation: %s calls=%d", w.Body.String(), calls.Load())
	}
}

func TestActivityPresentationRechecksGrantAfterEnrichment(t *testing.T) {
	e := newActivityEnv(t)
	arr := e.add(t, "radarr", "http://arr.invalid")
	cacheSource(e, arr, sourceSnapshot{rows: []activityRow{movieRow(1, 100, "download", 100, 60)}})
	e.h.SetPresentationResolver(func(context.Context, int64, string, string, int, int, string) (*mediametadata.Presentation, error) {
		if _, err := e.db.Exec("DELETE FROM user_instance_grants WHERE user_id=2 AND instance_id=?", arr.ID); err != nil {
			t.Error(err)
		}
		return &mediametadata.Presentation{Title: "Private translated title"}, nil
	})
	_, w := e.get(t, 2, "/api/downloads/activity?metadata_language=fr-FR")
	if w.Code != 503 || strings.Contains(w.Body.String(), "Private translated") || strings.Contains(w.Body.String(), "Movie 1") {
		t.Fatalf("revoked response: %s", w.Body.String())
	}
}

func TestActivityPresentationDistinguishesMetadataAbsenceFromPolicyFailure(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "metadata unavailable", true: "access unavailable"}[failure], func(t *testing.T) {
			e := newActivityEnv(t)
			arr := e.add(t, "radarr", "http://arr.invalid")
			cacheSource(e, arr, sourceSnapshot{rows: []activityRow{movieRow(1, 100, "download", 100, 60)}})
			e.h.SetPresentationResolver(func(context.Context, int64, string, string, int, int, string) (*mediametadata.Presentation, error) {
				if failure {
					return nil, errors.New("policy unavailable")
				}
				return nil, nil
			})
			view, w := e.get(t, 2, "/api/downloads/activity?metadata_language=fr-FR")
			if failure {
				if w.Code != 503 || strings.Contains(w.Body.String(), "Movie 1") {
					t.Fatal(w.Body.String())
				}
			} else if w.Code != 200 || view.Groups[0].Title != "Movie 1" || view.Groups[0].Presentation != nil {
				t.Fatal(w.Body.String())
			}
		})
	}
}
