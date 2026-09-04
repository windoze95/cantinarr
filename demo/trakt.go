// trakt.go — /api/trakt/* (D3). All Trakt list endpoints return a bare
// JSON array (no envelope). Trakt ids are derived: trakt = tmdb*10,
// episode trakt id = tmdb*100 + episode number (traktEpisodeID). Artwork
// is scheme-less "image.tmdb.org/t/p/w500/..." strings — the app prefixes
// "https://" itself, and by never emitting *.trakt.tv hosts the demo never
// needs the Trakt image relay.
//
// The lists endpoint serves FLAT list objects (contract §9 — what the app
// parses, not the real relay's nested shape). Inner-object keys always
// agree with the requested type (movie/movies, show/shows) and with the
// per-item "type" field on list items. Every item carries ids.tmdb: the app
// drops an item without one before it ever renders.
//
// Kids accounts: the six title-carrying handlers filter the catalog through
// the caller's content policy before the loop (a calendar row is judged by
// its show); the lists endpoint is opaque metadata and stays as seeded.
package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

func registerTrakt(r chi.Router) {
	r.Get("/trakt/trending", traktHandleTrending)
	r.Get("/trakt/popular", traktHandlePopular)
	r.Get("/trakt/lists", traktHandleLists)
	r.Get("/trakt/lists/{user}/{slug}/items", traktHandleListItems)
	r.Get("/trakt/calendar", traktHandleCalendar)
	r.Get("/trakt/anticipated", traktHandleAnticipated)
	r.Get("/trakt/recommendations", traktHandleRecommendations)
}

// ─── Object builders ────────────────────────────────────────────────────

// traktSlug builds a Trakt-style slug ("the-general-1926").
func traktSlug(title string, year int) string {
	var b strings.Builder
	pendingDash := false
	for _, ch := range strings.ToLower(title) {
		isAlnum := (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')
		if isAlnum {
			if pendingDash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(ch)
			pendingDash = false
		} else {
			pendingDash = true
		}
	}
	slug := b.String()
	if year > 0 {
		slug = fmt.Sprintf("%s-%d", slug, year)
	}
	return slug
}

func traktGenreNames(genres []DemoGenre) []string {
	out := make([]string, 0, len(genres))
	for _, g := range genres {
		out = append(out, strings.ToLower(g.Name))
	}
	return out
}

// traktPosterImages builds the scheme-less TMDB-hosted poster list.
func traktPosterImages(posterPath string) map[string]any {
	posters := []string{}
	if posterPath != "" {
		posters = append(posters, "image.tmdb.org/t/p/w500"+posterPath)
	}
	return map[string]any{"poster": posters}
}

func traktMovieObj(m *DemoMovie, withImages bool) map[string]any {
	obj := map[string]any{
		"title":    m.Title,
		"year":     m.Year(),
		"overview": m.Overview,
		"released": m.ReleaseDate,
		"runtime":  m.Runtime,
		"language": m.OriginalLanguage,
		"rating":   m.VoteAverage,
		"votes":    m.VoteCount,
		"status":   "released",
		"genres":   traktGenreNames(m.Genres),
		"ids": map[string]any{
			"trakt": m.TraktID(),
			"slug":  traktSlug(m.Title, m.Year()),
			"imdb":  m.ImdbID,
			"tmdb":  m.TmdbID,
		},
	}
	if withImages {
		obj["images"] = traktPosterImages(m.PosterPath)
	}
	return obj
}

// traktShowStatus maps the TMDB status vocabulary onto Trakt's.
func traktShowStatus(status string) string {
	switch status {
	case "Ended":
		return "ended"
	case "In Production":
		return "in production"
	case "Planned":
		return "planned"
	case "Canceled":
		return "canceled"
	default:
		return "returning series"
	}
}

func traktShowObj(s *DemoShow, withImages bool) map[string]any {
	// The shows are fiction: no IMDb record exists, so the link is absent
	// rather than pointing at an unrelated real title.
	var imdb any
	if s.ImdbID != "" {
		imdb = s.ImdbID
	}
	obj := map[string]any{
		"title":          s.Name,
		"year":           s.Year(),
		"overview":       s.Overview,
		"first_aired":    s.FirstAirDate + "T02:00:00.000Z",
		"language":       s.OriginalLanguage,
		"rating":         s.VoteAverage,
		"votes":          s.VoteCount,
		"status":         traktShowStatus(s.Status),
		"network":        discShowNetworkName(s.TmdbID),
		"aired_episodes": s.EpisodeCount(),
		"genres":         traktGenreNames(s.Genres),
		"ids": map[string]any{
			"trakt": s.TraktID(),
			"slug":  traktSlug(s.Name, 0),
			"imdb":  imdb,
			"tmdb":  s.TmdbID,
			"tvdb":  s.TvdbID,
		},
	}
	if withImages {
		obj["images"] = traktPosterImages(s.PosterPath)
	}
	return obj
}

// traktWantsShows reports whether the type query asks for shows
// (default and every other value = movies, per the Trakt path shape).
func traktWantsShows(r *http.Request) bool {
	return r.URL.Query().Get("type") == "shows"
}

// ─── Handlers ───────────────────────────────────────────────────────────

func traktHandleTrending(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if queryInt(r, "page", 1) > 1 {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	if traktWantsShows(r) {
		shows := cpKeepShows(u, demoShows)
		items := make([]map[string]any, 0, len(shows))
		for i, s := range shows {
			items = append(items, map[string]any{
				"watchers": 800 - i*40,
				"show":     traktShowObj(s, false),
			})
		}
		writeJSON(w, http.StatusOK, items)
		return
	}
	movies := cpKeepMovies(u, demoMovies)
	items := make([]map[string]any, 0, len(movies))
	for i, m := range movies {
		items = append(items, map[string]any{
			"watchers": 1000 - i*50,
			"movie":    traktMovieObj(m, false),
		})
	}
	writeJSON(w, http.StatusOK, items)
}

func traktHandlePopular(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if queryInt(r, "page", 1) > 1 {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	if traktWantsShows(r) {
		shows := cpKeepShows(u, demoShows)
		items := make([]map[string]any, 0, len(shows))
		for _, s := range shows {
			items = append(items, traktShowObj(s, false))
		}
		writeJSON(w, http.StatusOK, items)
		return
	}
	movies := cpKeepMovies(u, demoMovies)
	items := make([]map[string]any, 0, len(movies))
	for _, m := range movies {
		items = append(items, traktMovieObj(m, false))
	}
	writeJSON(w, http.StatusOK, items)
}

func traktHandleLists(w http.ResponseWriter, r *http.Request) {
	if queryInt(r, "page", 1) > 1 {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	lists := make([]map[string]any, 0, len(discTraktLists))
	for _, l := range discTraktLists {
		lists = append(lists, map[string]any{
			"name":            l.Name,
			"description":     l.Description,
			"privacy":         "public",
			"share_link":      "",
			"type":            "personal",
			"display_numbers": false,
			"allow_comments":  true,
			"sort_by":         "rank",
			"sort_how":        "asc",
			"created_at":      now,
			"updated_at":      now,
			"item_count":      l.ItemCount,
			"comment_count":   l.Comments,
			"like_count":      l.Likes,
			"ids": map[string]any{
				"trakt": l.TraktID,
				"slug":  l.Slug,
			},
			"user": map[string]any{
				"username": l.Username,
				"private":  false,
				"name":     l.Username,
				"vip":      false,
				"ids":      map[string]any{"slug": l.Username},
			},
		})
	}
	writeJSON(w, http.StatusOK, lists)
}

func traktHandleListItems(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	slug := chi.URLParam(r, "slug")
	ids, ok := discTraktListItems[slug]
	if !ok {
		ids = discTraktListItems["best-of-film-noir"]
	}
	resolved := make([]*DemoMovie, 0, len(ids))
	for _, tmdbID := range ids {
		if m, found := findMovie(tmdbID); found {
			resolved = append(resolved, m)
		}
	}
	// Rank renumbers over what the caller may see.
	movies := cpKeepMovies(u, resolved)
	now := time.Now().UTC().Format(time.RFC3339)
	items := make([]map[string]any, 0, len(movies))
	rank := 0
	for _, m := range movies {
		rank++
		items = append(items, map[string]any{
			"rank":      rank,
			"id":        100 + rank,
			"listed_at": now,
			"notes":     nil,
			"type":      "movie",
			"movie":     traktMovieObj(m, false),
		})
	}
	writeJSON(w, http.StatusOK, items)
}

func traktHandleCalendar(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	days := queryInt(r, "days", 14)
	if days < 1 {
		days = 14
	}
	now := time.Now().UTC()
	shows := cpKeepShows(u, demoShows)
	items := make([]map[string]any, 0, len(shows))
	for i, s := range shows {
		if i >= days {
			break
		}
		epNum := i + 1
		items = append(items, map[string]any{
			"first_aired": now.AddDate(0, 0, i).Format("2006-01-02T15:04:05.000Z"),
			"episode": map[string]any{
				"season": s.SeasonCount(),
				"number": epNum,
				"title":  fmt.Sprintf("Episode %d", epNum),
				"ids": map[string]any{
					"trakt": traktEpisodeID(s.TmdbID, epNum),
					"tvdb":  nil,
					"imdb":  nil,
					"tmdb":  nil,
				},
			},
			"show": traktShowObj(s, false),
		})
	}
	writeJSON(w, http.StatusOK, items)
}

// traktAnticipatedPageSize is how many items one "Most Anticipated" page
// carries. The app pages this feed open-ended until an empty page, so the
// grid under the row grows once (18 films = two pages) and then stops.
const traktAnticipatedPageSize = 10

// traktPageWindow is the [lo, hi) slice of a list for a 1-based page.
func traktPageWindow(page, size, total int) (lo, hi int) {
	if page < 1 {
		page = 1
	}
	lo = min((page-1)*size, total)
	hi = min(lo+size, total)
	return lo, hi
}

func traktHandleAnticipated(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	page := queryInt(r, "page", 1)
	if traktWantsShows(r) {
		// The unaired show leads (highest list count), then the rest.
		ordered := make([]*DemoShow, 0, len(demoShows))
		for _, s := range demoShows {
			if s.Status == "In Production" || s.Status == "Planned" {
				ordered = append(ordered, s)
			}
		}
		for _, s := range demoShows {
			if s.Status != "In Production" && s.Status != "Planned" {
				ordered = append(ordered, s)
			}
		}
		shows := cpKeepShows(u, ordered)
		lo, hi := traktPageWindow(page, traktAnticipatedPageSize, len(shows))
		items := make([]map[string]any, 0, hi-lo)
		for i := lo; i < hi; i++ {
			items = append(items, map[string]any{
				"list_count": 400 - i*25,
				"show":       traktShowObj(shows[i], true),
			})
		}
		writeJSON(w, http.StatusOK, items)
		return
	}
	movies := cpKeepMovies(u, demoMovies)
	lo, hi := traktPageWindow(page, traktAnticipatedPageSize, len(movies))
	items := make([]map[string]any, 0, hi-lo)
	for i := lo; i < hi; i++ {
		items = append(items, map[string]any{
			"list_count": 500 - i*25,
			"movie":      traktMovieObj(movies[i], true),
		})
	}
	writeJSON(w, http.StatusOK, items)
}

func traktHandleRecommendations(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	// No page param on this endpoint (srv-discover §6.7).
	if traktWantsShows(r) {
		shows := cpKeepShows(u, demoShows)
		items := make([]map[string]any, 0, len(shows))
		for _, s := range shows {
			items = append(items, traktShowObj(s, false))
		}
		writeJSON(w, http.StatusOK, items)
		return
	}
	n := min(8, len(demoMovies))
	movies := cpKeepMovies(u, demoMovies[:n])
	items := make([]map[string]any, 0, len(movies))
	for _, m := range movies {
		items = append(items, traktMovieObj(m, false))
	}
	writeJSON(w, http.StatusOK, items)
}
