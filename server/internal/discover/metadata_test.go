package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/serversettings"
)

func TestMetadataLanguageSeparatesCacheAndPreservesOriginalFilter(t *testing.T) {
	e := newEnv(t, true)
	e.prefs.set(serversettings.DiscoverySourceTMDBTrending, true)
	e.upstream.setRespond(func(r *http.Request) (int, string) {
		title := "English"
		if r.URL.Query().Get("language") == "fr-FR" {
			title = "Français"
		}
		return 200, fmt.Sprintf(`{"results":[{"id":1,"title":%q,"overview":"","original_language":"en"},{"id":2,"title":"Filtered","original_language":"fr"}]}`, title)
	})
	french := e.doOK(t, "/discover/movies/popular?metadata_language=fr-FR")
	english := e.doOK(t, "/discover/movies/popular")
	if !strings.Contains(french, "Français") || !strings.Contains(french, `"source_title":"English"`) || strings.Contains(french, "Filtered") {
		t.Fatalf("French %s", french)
	}
	if strings.Contains(english, "Français") || !strings.Contains(english, "English") {
		t.Fatalf("English %s", english)
	}
	hits := e.upstream.hitCount()
	e.doOK(t, "/discover/movies/popular?metadata_language=fr-FR")
	if e.upstream.hitCount() != hits {
		t.Fatal("localized cache missed")
	}
}
func TestTraktMetadataPreservesOrderSourceAndMissingFallback(t *testing.T) {
	e := newEnv(t, true)
	e.upstream.setRespond(func(r *http.Request) (int, string) {
		if strings.Contains(r.URL.Host, "trakt") {
			return 200, `[{"title":"Original two","ids":{"tmdb":2}},{"title":"Original one","ids":{"tmdb":1}}]`
		}
		if strings.HasSuffix(r.URL.Path, "/1") {
			return 503, `{}`
		}
		title := "English two"
		if r.URL.Query().Get("language") == "fr-FR" {
			title = "Deux"
		}
		return 200, fmt.Sprintf(`{"id":2,"title":%q,"overview":"Résumé","poster_path":"/fr.jpg"}`, title)
	})
	body := e.doOK(t, "/trakt/popular?type=movies&metadata_language=fr-FR")
	if !strings.Contains(body, `"title":"Deux"`) || !strings.Contains(body, `"source_title":"Original two"`) || !strings.Contains(body, `"title":"Original one"`) || strings.Index(body, `"tmdb":2`) > strings.Index(body, `"tmdb":1`) {
		t.Fatalf("localized Trakt %s", body)
	}
}
func TestFeaturedTraktUsesLocalizedPresentation(t *testing.T) {
	e := newEnv(t, true)
	e.prefs.set(serversettings.DiscoverySourceTraktTrending, false)
	e.upstream.setRespond(func(r *http.Request) (int, string) {
		if strings.Contains(r.URL.Host, "trakt") {
			return 200, `[{"show":{"title":"Original","language":"en","ids":{"tmdb":4}}}]`
		}
		title := "English"
		if r.URL.Query().Get("language") == "fr-FR" {
			title = "Français"
		}
		return 200, fmt.Sprintf(`{"id":4,"name":%q}`, title)
	})
	body := e.doOK(t, "/discover/tv/featured?metadata_language=fr-FR")
	if !strings.Contains(body, `"name":"Français"`) || !strings.Contains(body, `"source_name":"Original"`) || !strings.Contains(body, `"original_language":"en"`) {
		t.Fatalf("featured %s", body)
	}
}

func TestLocalizedWarmCacheStillAppliesKidsPolicy(t *testing.T) {
	k := newKidsEnv(t)
	k.upstreamTable.movie(1, "G")
	k.upstreamTable.movie(2, "R")
	k.upstream.setRespond(func(r *http.Request) (int, string) {
		if r.URL.Path == "/3/movie/popular" {
			label := "English"
			if r.URL.Query().Get("language") == "fr-FR" {
				label = "Français"
			}
			return 200, fmt.Sprintf(`{"results":[{"id":1,"title":%q,"genre_ids":[18]},{"id":2,"title":"Blocked","genre_ids":[18]}]}`, label)
		}
		return k.upstreamTable.respond(r)
	})
	adult := k.as(k.adult).doOK(t, "/discover/movies/popular?metadata_language=fr-FR")
	if len(resultIDs(t, adult)) != 2 {
		t.Fatal(adult)
	}
	kid := k.as(k.kid).doOK(t, "/discover/movies/popular?metadata_language=fr-FR")
	if ids := resultIDs(t, kid); len(ids) != 1 || ids[0] != 1 || !strings.Contains(kid, "Français") {
		t.Fatal(kid)
	}
}

func TestOptionalFeaturedEnrichmentHonorsDeadlineAndKeepsSource(t *testing.T) {
	e := newEnv(t, true)
	e.upstream.setRespond(func(r *http.Request) (int, string) { <-r.Context().Done(); return 503, `{}` })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	rows := []json.RawMessage{json.RawMessage(`{"id":1,"title":"Original","overview":"Original overview"}`)}
	started := time.Now()
	got := e.handler.localizeFeatured(ctx, rows, "movie", "fr-FR")
	if time.Since(started) > time.Second || string(got[0]) != string(rows[0]) {
		t.Fatalf("source fallback delayed or changed: %s", got[0])
	}
}
