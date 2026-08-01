// discover.go — /api/discover/*, /api/search, /api/media/*, /api/genres/*,
// /api/providers/movie, and GET|PUT /api/admin/discovery-settings (D3).
//
// Every list response is the TMDB page envelope; the demo serves exactly one
// page (page:1, total_pages:1), so infinite scroll fires once and stops.
// Detail endpoints answer 200 for every id the demo emits anywhere — the
// app's detail screens are all-or-nothing (detail + recommendations +
// similar must all succeed), so recommendations/similar NEVER 404: unknown
// ids get an empty page. Unknown ids on the detail endpoints themselves
// mirror the real server's upstream surfacing: 502 "TMDB API returned
// status 404".
package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
)

// ─── Discovery settings state (seeded SAVED per contract §9) ────────────

var (
	discSettingsMu  sync.Mutex
	discSource      = "tmdb_trending"
	discEnglishOnly = false
)

// discoveryPrefsSaved reports whether an admin has ever saved discovery
// preferences (contract.md §7 — the demo seeds them as saved, and a PUT
// can only re-save, so this is always true).
func discoveryPrefsSaved() bool { return true }

func discCurrentSettings() (source string, englishOnly bool) {
	discSettingsMu.Lock()
	defer discSettingsMu.Unlock()
	return discSource, discEnglishOnly
}

func discSettingsJSON() map[string]any {
	source, englishOnly := discCurrentSettings()
	return map[string]any{
		"source":           source,
		"english_only":     englishOnly,
		"sources":          []string{"tmdb_trending", "trakt_trending", "tmdb_popular"},
		"trakt_configured": true,
	}
}

// ─── Route registration ─────────────────────────────────────────────────

func registerDiscover(r chi.Router) {
	r.Get("/discover/trending", discHandleTrending)
	r.Get("/discover/movies", discHandleDiscoverMovies)
	r.Get("/discover/movies/popular", discHandleMoviesPopular)
	r.Get("/discover/movies/featured", discHandleMoviesFeatured)
	r.Get("/discover/movies/top-rated", discHandleMoviesTopRated)
	r.Get("/discover/movies/upcoming", discHandleMoviesUpcoming)
	r.Get("/discover/movies/now-playing", discHandleMoviesNowPlaying)
	r.Get("/discover/tv", discHandleDiscoverTV)
	r.Get("/discover/tv/popular", discHandleTVPopular)
	r.Get("/discover/tv/featured", discHandleTVFeatured)

	r.Get("/search", discHandleSearch)

	r.Get("/media/movie/{id}", discHandleMovieDetail)
	r.Get("/media/movie/{id}/recommendations", discHandleMovieRelated)
	r.Get("/media/movie/{id}/similar", discHandleMovieRelated)
	r.Get("/media/tv/{id}", discHandleTVDetail)
	r.Get("/media/tv/{id}/recommendations", discHandleTVRelated)
	r.Get("/media/tv/{id}/similar", discHandleTVRelated)
	r.Get("/media/person/{id}", discHandlePersonDetail)
	r.Get("/media/person/{id}/credits", discHandlePersonCredits)

	r.Get("/genres/movie", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"genres": discGenreObjs(discMovieGenres)})
	})
	r.Get("/genres/tv", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"genres": discGenreObjs(discTVGenres)})
	})
	r.Get("/providers/movie", discHandleProviders)

	r.With(requireAdmin).Get("/admin/discovery-settings", discHandleSettingsGet)
	r.With(requireAdmin).Put("/admin/discovery-settings", discHandleSettingsPut)
}

// ─── Shared rendering helpers ───────────────────────────────────────────

// discEnvelope wraps items in the TMDB page envelope. The demo serves one
// page: page 1 carries every item; any later page is empty (total_pages
// stays 1, so a well-behaved client never asks).
func discEnvelope(page int, items []map[string]any) map[string]any {
	if page < 1 {
		page = 1
	}
	results := items
	if page > 1 {
		results = []map[string]any{}
	}
	return map[string]any{
		"page":          page,
		"results":       results,
		"total_pages":   1,
		"total_results": len(items),
	}
}

func discGenreObjs(genres []DemoGenre) []map[string]any {
	out := make([]map[string]any, 0, len(genres))
	for _, g := range genres {
		out = append(out, map[string]any{"id": g.ID, "name": g.Name})
	}
	return out
}

// discOriginCountry derives a plausible origin country from the original
// language (used on detail responses; harmless, ignored by the app).
func discOriginCountry(lang string) []string {
	switch lang {
	case "de":
		return []string{"DE"}
	case "fr":
		return []string{"FR"}
	default:
		return []string{"US"}
	}
}

// discMovieItem renders a TMDB movie list item. withMediaType adds
// media_type:"movie" (required on trending and multi-search results).
func discMovieItem(m *DemoMovie, withMediaType bool) map[string]any {
	item := map[string]any{
		"adult":             false,
		"backdrop_path":     m.BackdropPath,
		"genre_ids":         m.GenreIDs(),
		"id":                m.TmdbID,
		"original_language": m.OriginalLanguage,
		"original_title":    m.OriginalTitle,
		"overview":          m.Overview,
		"popularity":        m.Popularity,
		"poster_path":       m.PosterPath,
		"release_date":      m.ReleaseDate,
		"title":             m.Title,
		"video":             false,
		"vote_average":      m.VoteAverage,
		"vote_count":        m.VoteCount,
	}
	if withMediaType {
		item["media_type"] = mediaTypeMovie
	}
	return item
}

// discTVItem renders a TMDB TV list item.
func discTVItem(s *DemoShow, withMediaType bool) map[string]any {
	item := map[string]any{
		"adult":             false,
		"backdrop_path":     s.BackdropPath,
		"genre_ids":         discShowGenreIDs(s),
		"id":                s.TmdbID,
		"origin_country":    []string{"US"},
		"original_language": s.OriginalLanguage,
		"original_name":     s.Name,
		"overview":          s.Overview,
		"popularity":        s.Popularity,
		"poster_path":       s.PosterPath,
		"first_air_date":    s.FirstAirDate,
		"name":              s.Name,
		"vote_average":      s.VoteAverage,
		"vote_count":        s.VoteCount,
	}
	if withMediaType {
		item["media_type"] = mediaTypeTV
	}
	return item
}

func discShowGenreIDs(s *DemoShow) []int {
	ids := make([]int, 0, len(s.Genres))
	for _, g := range s.Genres {
		ids = append(ids, g.ID)
	}
	return ids
}

// discPersonItem renders a TMDB multi-search person item.
func discPersonItem(p *DemoPerson) map[string]any {
	return map[string]any{
		"adult":                false,
		"gender":               p.Gender,
		"id":                   p.ID,
		"known_for_department": p.KnownForDept,
		"media_type":           "person",
		"name":                 p.Name,
		"original_name":        p.Name,
		"popularity":           p.Popularity,
		"profile_path":         p.ProfilePath,
		"known_for":            []map[string]any{},
	}
}

// discFilterEnglish drops non-English items when the english_only setting
// is on. Applies to feed endpoints only — never search, details, genres,
// providers, or Trakt lists (srv-discover §1.5).
func discFilterEnglish(items []map[string]any) []map[string]any {
	_, englishOnly := discCurrentSettings()
	if !englishOnly {
		return items
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		lang, ok := item["original_language"].(string)
		if ok && lang != "" {
			l := strings.ToLower(lang)
			if l != "en" && !strings.HasPrefix(l, "en-") {
				continue
			}
		}
		out = append(out, item)
	}
	return out
}

func discMovieItems(movies []*DemoMovie, withMediaType bool) []map[string]any {
	items := make([]map[string]any, 0, len(movies))
	for _, m := range movies {
		items = append(items, discMovieItem(m, withMediaType))
	}
	return items
}

func discTVItems(shows []*DemoShow, withMediaType bool) []map[string]any {
	items := make([]map[string]any, 0, len(shows))
	for _, s := range shows {
		items = append(items, discTVItem(s, withMediaType))
	}
	return items
}

// ─── Feed handlers ──────────────────────────────────────────────────────

func discHandleTrending(w http.ResponseWriter, r *http.Request) {
	page := queryInt(r, "page", 1)
	items := make([]map[string]any, 0, len(demoMovies)+len(demoShows))
	mi, ti := 0, 0
	for mi < len(demoMovies) || ti < len(demoShows) {
		if mi < len(demoMovies) {
			items = append(items, discMovieItem(demoMovies[mi], true))
			mi++
		}
		if ti < len(demoShows) {
			items = append(items, discTVItem(demoShows[ti], true))
			ti++
		}
	}
	writeJSON(w, http.StatusOK, discEnvelope(page, discFilterEnglish(items)))
}

func discHandleMoviesPopular(w http.ResponseWriter, r *http.Request) {
	page := queryInt(r, "page", 1)
	writeJSON(w, http.StatusOK, discEnvelope(page, discFilterEnglish(discMovieItems(demoMovies, false))))
}

func discHandleTVPopular(w http.ResponseWriter, r *http.Request) {
	page := queryInt(r, "page", 1)
	writeJSON(w, http.StatusOK, discEnvelope(page, discFilterEnglish(discTVItems(demoShows, false))))
}

func discHandleMoviesTopRated(w http.ResponseWriter, r *http.Request) {
	page := queryInt(r, "page", 1)
	sorted := make([]*DemoMovie, len(demoMovies))
	copy(sorted, demoMovies)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].VoteAverage > sorted[j].VoteAverage })
	writeJSON(w, http.StatusOK, discEnvelope(page, discFilterEnglish(discMovieItems(sorted, false))))
}

func discHandleMoviesUpcoming(w http.ResponseWriter, r *http.Request) {
	page := queryInt(r, "page", 1)
	n := min(6, len(demoMovies))
	writeJSON(w, http.StatusOK, discEnvelope(page, discFilterEnglish(discMovieItems(demoMovies[:n], false))))
}

func discHandleMoviesNowPlaying(w http.ResponseWriter, r *http.Request) {
	page := queryInt(r, "page", 1)
	lo := min(6, len(demoMovies))
	hi := min(12, len(demoMovies))
	writeJSON(w, http.StatusOK, discEnvelope(page, discFilterEnglish(discMovieItems(demoMovies[lo:hi], false))))
}

// ─── Featured rows ──────────────────────────────────────────────────────

// discHandleMoviesFeatured / discHandleTVFeatured serve the headline rows:
// the TMDB envelope plus a "source" field naming the feed that answered
// (the saved discovery source — the demo's catalog answers for all three
// sources, always with TMDB-relative artwork, so no Trakt image relay is
// ever needed). Always page 1 of 1, max 20 items, item[0] = hero.
func discHandleMoviesFeatured(w http.ResponseWriter, _ *http.Request) {
	source, _ := discCurrentSettings()
	movies := make([]*DemoMovie, len(demoMovies))
	copy(movies, demoMovies)
	if source == "tmdb_popular" {
		sort.SliceStable(movies, func(i, j int) bool { return movies[i].Popularity > movies[j].Popularity })
	}
	items := discFilterEnglish(discMovieItems(movies, true))
	if len(items) > 20 {
		items = items[:20]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source":        source,
		"page":          1,
		"results":       items,
		"total_pages":   1,
		"total_results": len(items),
	})
}

func discHandleTVFeatured(w http.ResponseWriter, _ *http.Request) {
	source, _ := discCurrentSettings()
	shows := make([]*DemoShow, len(demoShows))
	copy(shows, demoShows)
	if source == "tmdb_popular" {
		sort.SliceStable(shows, func(i, j int) bool { return shows[i].Popularity > shows[j].Popularity })
	}
	items := discFilterEnglish(discTVItems(shows, true))
	if len(items) > 20 {
		items = items[:20]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source":        source,
		"page":          1,
		"results":       items,
		"total_pages":   1,
		"total_results": len(items),
	})
}

// ─── Filterable discover ────────────────────────────────────────────────

func discParseIDSet(csv string) map[int]bool {
	set := map[int]bool{}
	for _, part := range strings.Split(csv, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
			set[n] = true
		}
	}
	return set
}

func discHasAnyGenre(ids []int, set map[int]bool) bool {
	for _, id := range ids {
		if set[id] {
			return true
		}
	}
	return false
}

// discSortKey splits a TMDB sort_by value ("popularity.desc") into field
// and direction, defaulting to popularity.desc.
func discSortKey(sortBy string) (field string, desc bool) {
	field, desc = "popularity", true
	sortBy = strings.TrimSpace(sortBy)
	if sortBy == "" {
		return
	}
	if i := strings.LastIndex(sortBy, "."); i > 0 {
		switch sortBy[i+1:] {
		case "asc":
			return sortBy[:i], false
		case "desc":
			return sortBy[:i], true
		}
	}
	return sortBy, true
}

func discHandleDiscoverMovies(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page := queryInt(r, "page", 1)
	genres := discParseIDSet(q.Get("with_genres"))
	year, _ := strconv.Atoi(q.Get("primary_release_year"))

	matched := make([]*DemoMovie, 0, len(demoMovies))
	for _, m := range demoMovies {
		if len(genres) > 0 && !discHasAnyGenre(m.GenreIDs(), genres) {
			continue
		}
		if year > 0 && m.Year() != year {
			continue
		}
		matched = append(matched, m)
	}

	field, desc := discSortKey(q.Get("sort_by"))
	movieLess := func(a, b *DemoMovie) bool {
		switch field {
		case "vote_average":
			return a.VoteAverage < b.VoteAverage
		case "primary_release_date", "release_date":
			return a.ReleaseDate < b.ReleaseDate
		case "title", "original_title":
			return a.Title < b.Title
		default: // popularity
			return a.Popularity < b.Popularity
		}
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if desc {
			return movieLess(matched[j], matched[i])
		}
		return movieLess(matched[i], matched[j])
	})

	writeJSON(w, http.StatusOK, discEnvelope(page, discFilterEnglish(discMovieItems(matched, false))))
}

func discHandleDiscoverTV(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page := queryInt(r, "page", 1)
	genres := discParseIDSet(q.Get("with_genres"))
	year, _ := strconv.Atoi(q.Get("first_air_date_year"))

	matched := make([]*DemoShow, 0, len(demoShows))
	for _, s := range demoShows {
		if len(genres) > 0 && !discHasAnyGenre(discShowGenreIDs(s), genres) {
			continue
		}
		if year > 0 && s.Year() != year {
			continue
		}
		matched = append(matched, s)
	}

	field, desc := discSortKey(q.Get("sort_by"))
	showLess := func(a, b *DemoShow) bool {
		switch field {
		case "vote_average":
			return a.VoteAverage < b.VoteAverage
		case "first_air_date":
			return a.FirstAirDate < b.FirstAirDate
		case "name", "original_name":
			return a.Name < b.Name
		default: // popularity
			return a.Popularity < b.Popularity
		}
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if desc {
			return showLess(matched[j], matched[i])
		}
		return showLess(matched[i], matched[j])
	})

	writeJSON(w, http.StatusOK, discEnvelope(page, discFilterEnglish(discTVItems(matched, false))))
}

// ─── Search ─────────────────────────────────────────────────────────────

func discHandleSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	if query == "" {
		writeErr(w, http.StatusBadRequest, "query parameter required")
		return
	}
	page := queryInt(r, "page", 1)
	q := strings.ToLower(query)

	results := make([]map[string]any, 0)
	for _, m := range demoMovies {
		if strings.Contains(strings.ToLower(m.Title), q) || strings.Contains(strings.ToLower(m.OriginalTitle), q) {
			results = append(results, discMovieItem(m, true))
		}
	}
	for _, s := range demoShows {
		if strings.Contains(strings.ToLower(s.Name), q) {
			results = append(results, discTVItem(s, true))
		}
	}
	for _, p := range discPersons {
		if strings.Contains(strings.ToLower(p.Name), q) {
			results = append(results, discPersonItem(p))
		}
	}
	// Multi-search is NEVER english-filtered (srv-discover §1.5).
	writeJSON(w, http.StatusOK, discEnvelope(page, results))
}

// ─── Media details ──────────────────────────────────────────────────────

// discPathID parses the {id} path param; the second return is false for
// non-numeric ids.
func discPathID(r *http.Request) (int, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	return id, err == nil
}

// discUnknownID mirrors the real server surfacing TMDB's 404 as a 502.
func discUnknownID(w http.ResponseWriter) {
	writeErr(w, http.StatusBadGateway, "TMDB API returned status 404")
}

func discHandleMovieDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := discPathID(r)
	if !ok {
		discUnknownID(w)
		return
	}
	m, found := findMovie(id)
	if !found {
		discUnknownID(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"adult":                 false,
		"backdrop_path":         m.BackdropPath,
		"belongs_to_collection": nil,
		"budget":                0,
		"genres":                discGenreObjs(m.Genres),
		"homepage":              "",
		"id":                    m.TmdbID,
		"imdb_id":               m.ImdbID,
		"origin_country":        discOriginCountry(m.OriginalLanguage),
		"original_language":     m.OriginalLanguage,
		"original_title":        m.OriginalTitle,
		"overview":              m.Overview,
		"popularity":            m.Popularity,
		"poster_path":           m.PosterPath,
		"production_companies":  []any{},
		"production_countries":  []any{},
		"release_date":          m.ReleaseDate,
		"revenue":               0,
		"runtime":               m.Runtime,
		"spoken_languages":      []any{},
		"status":                "Released",
		"tagline":               m.Tagline,
		"title":                 m.Title,
		"video":                 false,
		"vote_average":          m.VoteAverage,
		"vote_count":            m.VoteCount,
		"videos":                map[string]any{"results": []any{}},
	})
}

func discHandleTVDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := discPathID(r)
	if !ok {
		discUnknownID(w)
		return
	}
	s, found := findShow(id)
	if !found {
		discUnknownID(w)
		return
	}

	seasons := make([]map[string]any, 0, len(s.Seasons))
	lastAirDate := s.FirstAirDate
	for _, se := range s.Seasons {
		if n := len(se.Episodes); n > 0 && se.Episodes[n-1].AirDate > lastAirDate {
			lastAirDate = se.Episodes[n-1].AirDate
		}
		seasons = append(seasons, map[string]any{
			"air_date":      se.AirDate,
			"episode_count": se.EpisodeCount,
			"id":            se.ID,
			"name":          se.Name,
			"overview":      "",
			"poster_path":   se.PosterPath,
			"season_number": se.SeasonNumber,
			"vote_average":  s.VoteAverage,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"adult":              false,
		"backdrop_path":      s.BackdropPath,
		"first_air_date":     s.FirstAirDate,
		"genres":             discGenreObjs(s.Genres),
		"homepage":           "",
		"id":                 s.TmdbID,
		"in_production":      s.Status == "Returning Series",
		"languages":          []string{s.OriginalLanguage},
		"last_air_date":      lastAirDate,
		"name":               s.Name,
		"networks":           []any{},
		"number_of_episodes": s.EpisodeCount(),
		"number_of_seasons":  s.SeasonCount(),
		"origin_country":     []string{"US"},
		"original_language":  s.OriginalLanguage,
		"original_name":      s.Name,
		"overview":           s.Overview,
		"popularity":         s.Popularity,
		"poster_path":        s.PosterPath,
		"seasons":            seasons,
		"status":             s.Status,
		"tagline":            s.Tagline,
		"type":               s.Type,
		"vote_average":       s.VoteAverage,
		"vote_count":         s.VoteCount,
		"videos":             map[string]any{"results": []any{}},
		"external_ids": map[string]any{
			"imdb_id":      s.ImdbID,
			"tvdb_id":      s.TvdbID,
			"tvrage_id":    nil,
			"wikidata_id":  nil,
			"facebook_id":  nil,
			"instagram_id": nil,
			"twitter_id":   nil,
		},
	})
}

// discHandleMovieRelated serves both recommendations and similar: up to 6
// other catalog films. Unknown ids get an EMPTY page, never an error — the
// detail screen is all-or-nothing.
func discHandleMovieRelated(w http.ResponseWriter, r *http.Request) {
	page := queryInt(r, "page", 1)
	id, _ := discPathID(r)
	others := make([]*DemoMovie, 0, 6)
	if _, found := findMovie(id); found {
		for _, m := range demoMovies {
			if m.TmdbID == id {
				continue
			}
			others = append(others, m)
			if len(others) == 6 {
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, discEnvelope(page, discFilterEnglish(discMovieItems(others, false))))
}

// discHandleTVRelated serves recommendations/similar for TV: every other
// catalog show; empty page for unknown ids.
func discHandleTVRelated(w http.ResponseWriter, r *http.Request) {
	page := queryInt(r, "page", 1)
	id, _ := discPathID(r)
	others := make([]*DemoShow, 0, len(demoShows))
	if _, found := findShow(id); found {
		for _, s := range demoShows {
			if s.TmdbID != id {
				others = append(others, s)
			}
		}
	}
	writeJSON(w, http.StatusOK, discEnvelope(page, discFilterEnglish(discTVItems(others, false))))
}

// ─── Persons ────────────────────────────────────────────────────────────

func discHandlePersonDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := discPathID(r)
	if !ok {
		discUnknownID(w)
		return
	}
	p, found := discPersonByID(id)
	if !found {
		discUnknownID(w)
		return
	}
	var deathday any
	if p.Deathday != "" {
		deathday = p.Deathday
	}
	alsoKnownAs := p.AlsoKnownAs
	if alsoKnownAs == nil {
		alsoKnownAs = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"adult":                false,
		"also_known_as":        alsoKnownAs,
		"biography":            p.Biography,
		"birthday":             p.Birthday,
		"deathday":             deathday,
		"gender":               p.Gender,
		"homepage":             nil,
		"id":                   p.ID,
		"imdb_id":              p.ImdbID,
		"known_for_department": p.KnownForDept,
		"name":                 p.Name,
		"place_of_birth":       p.PlaceOfBirth,
		"popularity":           p.Popularity,
		"profile_path":         p.ProfilePath,
	})
}

// discCreditJSON renders one filmography row, resolving title/poster/
// overview from the catalog so the data never drifts. Credit media_type is
// exactly movie|tv (routing constraint).
func discCreditJSON(c DemoCredit, cast bool) map[string]any {
	item := map[string]any{
		"id":         c.TmdbID,
		"media_type": c.MediaType,
	}
	switch c.MediaType {
	case mediaTypeMovie:
		if m, found := findMovie(c.TmdbID); found {
			item["title"] = m.Title
			item["poster_path"] = m.PosterPath
			item["vote_average"] = m.VoteAverage
			item["release_date"] = m.ReleaseDate
			item["overview"] = m.Overview
			item["genre_ids"] = m.GenreIDs()
		}
	case mediaTypeTV:
		if s, found := findShow(c.TmdbID); found {
			item["name"] = s.Name
			item["poster_path"] = s.PosterPath
			item["vote_average"] = s.VoteAverage
			item["first_air_date"] = s.FirstAirDate
			item["overview"] = s.Overview
			item["genre_ids"] = discShowGenreIDs(s)
		}
	}
	if cast {
		item["character"] = c.Character
	} else {
		item["job"] = c.Job
		item["department"] = c.Department
	}
	return item
}

func discHandlePersonCredits(w http.ResponseWriter, r *http.Request) {
	id, ok := discPathID(r)
	if !ok {
		discUnknownID(w)
		return
	}
	p, found := discPersonByID(id)
	if !found {
		discUnknownID(w)
		return
	}
	cast := make([]map[string]any, 0, len(p.CastCredits))
	for _, c := range p.CastCredits {
		cast = append(cast, discCreditJSON(c, true))
	}
	crew := make([]map[string]any, 0, len(p.CrewCredits))
	for _, c := range p.CrewCredits {
		crew = append(crew, discCreditJSON(c, false))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":   p.ID,
		"cast": cast,
		"crew": crew,
	})
}

// ─── Providers ──────────────────────────────────────────────────────────

func discHandleProviders(w http.ResponseWriter, r *http.Request) {
	_ = r.URL.Query().Get("region") // accepted, ignored (single fictional region)
	results := make([]map[string]any, 0, len(discProviders))
	for i, p := range discProviders {
		results = append(results, map[string]any{
			"provider_id":      p.ID,
			"provider_name":    p.Name,
			"logo_path":        nil,
			"display_priority": i + 1,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// ─── Discovery settings (admin) ─────────────────────────────────────────

func discHandleSettingsGet(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, discSettingsJSON())
}

func discHandleSettingsPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Source      string `json:"source"`
		EnglishOnly bool   `json:"english_only"` // absent = false: full-replace semantics
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	source := strings.TrimSpace(body.Source)
	if source == "" {
		// Empty normalizes to the current default source; Trakt is always
		// configured in the demo, so the default is trakt_trending.
		source = "trakt_trending"
	}
	switch source {
	case "tmdb_trending", "trakt_trending", "tmdb_popular":
	default:
		writeErr(w, http.StatusBadRequest, "discovery_source must be one of tmdb_trending, trakt_trending, tmdb_popular")
		return
	}
	discSettingsMu.Lock()
	discSource = source
	discEnglishOnly = body.EnglishOnly
	discSettingsMu.Unlock()
	writeJSON(w, http.StatusOK, discSettingsJSON())
}
