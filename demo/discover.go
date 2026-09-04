// discover.go — /api/discover/*, /api/search, /api/search/keyword|company,
// /api/media/*, /api/genres/*, /api/languages, /api/providers/*, and
// GET|PUT /api/admin/discovery-settings (D3).
//
// Every list response is the TMDB page envelope, really paged: twenty
// items a page, page clamped to 1..500 like the real server, a truthful
// total_pages, and an empty page past the end. Filtering (English-only,
// browse filters, the caller's content policy) always happens before the
// slice, so a page is never thin. The two /discover/{movies,tv} routes
// mirror the real server's allowlist and validation (400 on a bad sort or
// date) and match the browse filters against the catalog plus the
// attachment maps in data_browse.go.
//
// Detail endpoints answer 200 for every id the demo emits anywhere — the
// app's detail screens are all-or-nothing (detail + recommendations +
// similar must all succeed), so recommendations/similar NEVER 404: unknown
// ids get an empty page. Unknown ids on the detail endpoints themselves
// mirror the real server's upstream surfacing: 502 "TMDB API returned
// status 404". A 404 on a detail route means exactly one thing, as on the
// real server: the title exists but the caller's content policy hides it.
//
// Kids accounts: every payload that can name a title passes the policy
// helpers in contentpolicy.go on the way out (post-render, per request,
// never stored); the lookup routes (languages, providers, regions,
// keyword/company search) are opaque and never filtered.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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
	r.Get("/discover/tv/on-the-air", discHandleTVOnTheAir)
	r.Get("/discover/tv/top-rated", discHandleTVTopRated)
	r.Get("/discover/tv/upcoming", discHandleTVUpcoming)

	r.Get("/search", discHandleSearch)
	r.Get("/search/keyword", discHandleSearchKeyword)
	r.Get("/search/company", discHandleSearchCompany)

	r.Get("/media/movie/{id}", discHandleMovieDetail)
	r.Get("/media/movie/{id}/recommendations", discHandleMovieRelated)
	r.Get("/media/movie/{id}/similar", discHandleMovieRelated)
	r.Get("/media/tv/{id}", discHandleTVDetail)
	r.Get("/media/tv/{id}/recommendations", discHandleTVRelated)
	r.Get("/media/tv/{id}/similar", discHandleTVRelated)
	r.Get("/media/person/{id}", discHandlePersonDetail)
	r.Get("/media/person/{id}/credits", discHandlePersonCredits)

	r.Get("/genres/movie", discHandleMovieGenres)
	r.Get("/genres/tv", discHandleTVGenres)
	r.Get("/languages", discHandleLanguages)
	r.Get("/providers/movie", discHandleProvidersMovie)
	r.Get("/providers/tv", discHandleProvidersTV)
	r.Get("/providers/regions", discHandleProviderRegions)

	r.With(requireAdmin).Get("/admin/discovery-settings", discHandleSettingsGet)
	r.With(requireAdmin).Put("/admin/discovery-settings", discHandleSettingsPut)
}

// ─── Page envelope ──────────────────────────────────────────────────────

// discPageSize is TMDB's page size, which the real server (and the
// browse_titles tool description) pass through unchanged.
const discPageSize = 20

// discMaxPage is the last page TMDB serves; the real server clamps to it
// rather than earning an upstream error (browse.go ClampPage).
const discMaxPage = 500

// discQueryPage reads `page` clamped to 1..discMaxPage.
func discQueryPage(r *http.Request) int {
	return discClampPage(queryInt(r, "page", 1))
}

func discClampPage(page int) int {
	if page < 1 {
		return 1
	}
	if page > discMaxPage {
		return discMaxPage
	}
	return page
}

// discEnvelope slices items into the TMDB page envelope: twenty a page, a
// truthful total_pages (never below 1), and an empty — never nil — results
// slice past the end. Filter before calling it, never after.
func discEnvelope(page int, items []map[string]any) map[string]any {
	page = discClampPage(page)
	total := len(items)
	totalPages := max(1, (total+discPageSize-1)/discPageSize)
	lo := min((page-1)*discPageSize, total)
	hi := min(lo+discPageSize, total)
	results := items[lo:hi]
	if results == nil {
		results = []map[string]any{}
	}
	return map[string]any{
		"page":          page,
		"results":       results,
		"total_pages":   totalPages,
		"total_results": total,
	}
}

// ─── Shared rendering helpers ───────────────────────────────────────────

// discNullable renders a blank string as JSON null: TMDB sends null for an
// unknown profile_path, imdb_id, birthday, or place_of_birth, and the app
// would try to load "" as an image path.
func discNullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func discGenreObjs(genres []DemoGenre) []map[string]any {
	out := make([]map[string]any, 0, len(genres))
	for _, g := range genres {
		out = append(out, map[string]any{"id": g.ID, "name": g.Name})
	}
	return out
}

// discMovieCountries is a movie's production countries; the origin region
// comes first. Falls back to the original language when no extra exists.
func discMovieCountries(m *DemoMovie) []string {
	if x := discMovieExtras[m.TmdbID]; x != nil && len(x.Countries) > 0 {
		return x.Countries
	}
	switch m.OriginalLanguage {
	case "de":
		return []string{"DE"}
	case "fr":
		return []string{"FR"}
	default:
		return []string{"US"}
	}
}

// discShowCountries is a show's production countries (US when unknown).
func discShowCountries(s *DemoShow) []string {
	if x := discShowExtras[s.TmdbID]; x != nil && len(x.Countries) > 0 {
		return x.Countries
	}
	return []string{"US"}
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
		"origin_country":    discShowCountries(s),
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
		"profile_path":         discNullable(p.ProfilePath),
		"known_for":            []map[string]any{},
	}
}

// discFilterEnglish drops non-English items when the english_only setting
// is on. Applies to the list feeds only — never search, details, genres,
// providers, or Trakt lists (srv-discover §1.5). The two filterable
// /discover routes push the preference into the query instead, the way
// the real server sends it upstream (discBrowseQuery).
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

// discCloneMovies / discCloneShows copy the catalog so a handler can sort
// without reordering the shared seed.
func discCloneMovies() []*DemoMovie {
	out := make([]*DemoMovie, len(demoMovies))
	copy(out, demoMovies)
	return out
}

func discCloneShows() []*DemoShow {
	out := make([]*DemoShow, len(demoShows))
	copy(out, demoShows)
	return out
}

// discTodayDate is today's calendar date (UTC) for the date-windowed rows.
func discTodayDate() time.Time {
	return time.Now().UTC().Truncate(24 * time.Hour)
}

// ─── Feed handlers ──────────────────────────────────────────────────────

func discHandleTrending(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	page := discQueryPage(r)
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
	writeJSON(w, http.StatusOK, discEnvelope(page, cpKeepItems(u, discFilterEnglish(items))))
}

func discHandleMoviesPopular(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	page := discQueryPage(r)
	writeJSON(w, http.StatusOK, discEnvelope(page, discFilterEnglish(discMovieItems(cpKeepMovies(u, demoMovies), false))))
}

func discHandleTVPopular(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	page := discQueryPage(r)
	writeJSON(w, http.StatusOK, discEnvelope(page, discFilterEnglish(discTVItems(cpKeepShows(u, demoShows), false))))
}

func discHandleMoviesTopRated(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	page := discQueryPage(r)
	sorted := discCloneMovies()
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].VoteAverage > sorted[j].VoteAverage })
	writeJSON(w, http.StatusOK, discEnvelope(page, discFilterEnglish(discMovieItems(cpKeepMovies(u, sorted), false))))
}

// discHandleMoviesUpcoming / discHandleMoviesNowPlaying serve fixed windows
// of the catalog (no film is unreleased); the policy thins the window after
// it is cut, the way the real server thins a fetched page.
func discHandleMoviesUpcoming(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	page := discQueryPage(r)
	n := min(6, len(demoMovies))
	writeJSON(w, http.StatusOK, discEnvelope(page, discFilterEnglish(discMovieItems(cpKeepMovies(u, demoMovies[:n]), false))))
}

func discHandleMoviesNowPlaying(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	page := discQueryPage(r)
	lo := min(6, len(demoMovies))
	hi := min(12, len(demoMovies))
	writeJSON(w, http.StatusOK, discEnvelope(page, discFilterEnglish(discMovieItems(cpKeepMovies(u, demoMovies[lo:hi]), false))))
}

// ─── TV rows ────────────────────────────────────────────────────────────

// discShowAirsBetween reports whether any episode airs within [from, to]
// (calendar dates, inclusive).
func discShowAirsBetween(s *DemoShow, from, to string) bool {
	for _, se := range s.Seasons {
		for _, ep := range se.Episodes {
			if ep.AirDate >= from && ep.AirDate <= to {
				return true
			}
		}
	}
	return false
}

func discSortShowsByPopularity(shows []*DemoShow) {
	sort.SliceStable(shows, func(i, j int) bool { return shows[i].Popularity > shows[j].Popularity })
}

// discHandleTVOnTheAir backs "Airing This Week": a series with an episode
// airing in the next seven days (TMDB /tv/on_the_air).
func discHandleTVOnTheAir(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	page := discQueryPage(r)
	today := discTodayDate()
	from, to := today.Format("2006-01-02"), today.AddDate(0, 0, 7).Format("2006-01-02")
	matched := make([]*DemoShow, 0, len(demoShows))
	for _, s := range demoShows {
		if discShowAirsBetween(s, from, to) {
			matched = append(matched, s)
		}
	}
	discSortShowsByPopularity(matched)
	writeJSON(w, http.StatusOK, discEnvelope(page, discFilterEnglish(discTVItems(cpKeepShows(u, matched), false))))
}

// discHandleTVTopRated backs "Top Rated" (TMDB /tv/top_rated).
func discHandleTVTopRated(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	page := discQueryPage(r)
	sorted := discCloneShows()
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].VoteAverage > sorted[j].VoteAverage })
	writeJSON(w, http.StatusOK, discEnvelope(page, discFilterEnglish(discTVItems(cpKeepShows(u, sorted), false))))
}

// discHandleTVUpcoming backs "Coming Soon": premieres only, first air date
// from today through three months out, most popular first — the discover
// query the real server issues, not /tv/upcoming.
func discHandleTVUpcoming(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	page := discQueryPage(r)
	today := discTodayDate()
	from, to := today.Format("2006-01-02"), today.AddDate(0, 3, 0).Format("2006-01-02")
	matched := make([]*DemoShow, 0, len(demoShows))
	for _, s := range demoShows {
		if s.FirstAirDate != "" && s.FirstAirDate >= from && s.FirstAirDate <= to {
			matched = append(matched, s)
		}
	}
	discSortShowsByPopularity(matched)
	writeJSON(w, http.StatusOK, discEnvelope(page, discFilterEnglish(discTVItems(cpKeepShows(u, matched), false))))
}

// ─── Featured rows ──────────────────────────────────────────────────────

// discHandleMoviesFeatured / discHandleTVFeatured serve the headline rows:
// the TMDB envelope plus a "source" field naming the feed that answered
// (the saved discovery source — the demo's catalog answers for all three
// sources, always with TMDB-relative artwork, so no Trakt image relay is
// ever needed). Page 1 is the row, item[0] the hero; later pages continue
// the same feed for the grid under it. total_results is the count on the
// page served, mirroring the real server's featured envelope
// (featured.go: TotalResults = len(results)).
func discHandleMoviesFeatured(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	page := discQueryPage(r)
	source, _ := discCurrentSettings()
	movies := discCloneMovies()
	if source == "tmdb_popular" {
		sort.SliceStable(movies, func(i, j int) bool { return movies[i].Popularity > movies[j].Popularity })
	}
	items := discFilterEnglish(discMovieItems(cpKeepMovies(u, movies), true))
	writeJSON(w, http.StatusOK, discFeaturedEnvelope(source, page, items))
}

func discHandleTVFeatured(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	page := discQueryPage(r)
	source, _ := discCurrentSettings()
	shows := discCloneShows()
	if source == "tmdb_popular" {
		sort.SliceStable(shows, func(i, j int) bool { return shows[i].Popularity > shows[j].Popularity })
	}
	items := discFilterEnglish(discTVItems(cpKeepShows(u, shows), true))
	writeJSON(w, http.StatusOK, discFeaturedEnvelope(source, page, items))
}

func discFeaturedEnvelope(source string, page int, items []map[string]any) map[string]any {
	env := discEnvelope(page, items)
	env["source"] = source
	env["total_results"] = len(env["results"].([]map[string]any))
	return env
}

// ─── Filterable discover ────────────────────────────────────────────────

// discBrowseSpec is what one media type's discover route accepts; the two
// instances are the real server's (browse.go movieDiscover / tvDiscover).
type discBrowseSpec struct {
	keys       []string
	sortFields []string
	dateKeys   []string
	yearKey    string
}

var discMovieBrowse = discBrowseSpec{
	keys: []string{
		"page", "sort_by", "with_genres", "primary_release_year",
		"primary_release_date.gte", "primary_release_date.lte",
		"vote_average.gte", "vote_count.gte", "with_original_language",
		"with_watch_providers", "watch_region", "with_keywords", "with_companies",
	},
	sortFields: []string{
		"original_title", "popularity", "primary_release_date", "revenue",
		"title", "vote_average", "vote_count",
	},
	dateKeys: []string{"primary_release_date.gte", "primary_release_date.lte"},
	yearKey:  "primary_release_year",
}

var discTVBrowse = discBrowseSpec{
	keys: []string{
		"page", "sort_by", "with_genres", "first_air_date_year",
		"first_air_date.gte", "first_air_date.lte",
		"vote_average.gte", "vote_count.gte", "with_original_language",
		"with_watch_providers", "watch_region", "with_keywords", "with_companies",
	},
	sortFields: []string{
		"first_air_date", "name", "original_name", "popularity",
		"vote_average", "vote_count",
	},
	dateKeys: []string{"first_air_date.gte", "first_air_date.lte"},
	yearKey:  "first_air_date_year",
}

// discRatingSortMinVotes floors a rating sort the caller did not floor
// themselves (browse.go ratingSortMinVotes).
const discRatingSortMinVotes = "200"

var errDiscInvalidSort = errors.New("invalid sort_by")

// discIDFilter is one id-list filter: TMDB's comma list means every id
// must match, a pipe list means any may.
type discIDFilter struct {
	ids []int
	any bool
}

func discParseIDFilter(v string) discIDFilter {
	f := discIDFilter{}
	sep := ","
	if strings.Contains(v, "|") {
		sep, f.any = "|", true
	}
	for _, part := range strings.Split(v, sep) {
		if n, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
			f.ids = append(f.ids, n)
		}
	}
	return f
}

// matches reports whether a title carrying `have` satisfies the filter; an
// empty filter matches everything.
func (f discIDFilter) matches(have []int) bool {
	if len(f.ids) == 0 {
		return true
	}
	for _, want := range f.ids {
		found := slices.Contains(have, want)
		if f.any && found {
			return true
		}
		if !f.any && !found {
			return false
		}
	}
	return !f.any
}

// discBrowseFilter is a validated discover query, ready to match titles.
type discBrowseFilter struct {
	page       int
	sortField  string
	sortDesc   bool
	genres     discIDFilter
	year       int
	dateGTE    string
	dateLTE    string
	voteAvgGTE float64
	hasVoteAvg bool
	voteCount  int
	language   string
	providers  discIDFilter
	keywords   discIDFilter
	companies  discIDFilter
}

// discBrowseQuery mirrors the real server's buildDiscoverQuery: copy the
// allowlisted keys (anything else is dropped silently), clamp the page,
// validate sort_by and the date keys (400 with the real messages), floor a
// rating sort's vote count, and push the admin's English-only preference in
// unless the caller named a language of their own.
func discBrowseQuery(r *http.Request, spec discBrowseSpec) (discBrowseFilter, error) {
	in := r.URL.Query()
	params := url.Values{}
	for _, k := range spec.keys {
		if v := in.Get(k); v != "" {
			params.Set(k, v)
		}
	}
	f := discBrowseFilter{page: discQueryPage(r), sortField: "popularity", sortDesc: true}

	if sortBy := params.Get("sort_by"); sortBy != "" {
		field, direction, ok := strings.Cut(sortBy, ".")
		if !ok || (direction != "asc" && direction != "desc") || !slices.Contains(spec.sortFields, field) {
			return f, errDiscInvalidSort
		}
		f.sortField, f.sortDesc = field, direction == "desc"
		if strings.HasPrefix(sortBy, "vote_average.") && params.Get("vote_count.gte") == "" {
			params.Set("vote_count.gte", discRatingSortMinVotes)
		}
	}
	for _, k := range spec.dateKeys {
		if v := params.Get(k); v != "" {
			if _, err := time.Parse("2006-01-02", v); err != nil {
				return f, fmt.Errorf("invalid %s: want YYYY-MM-DD", k)
			}
		}
	}
	// TMDB applies the admin's language preference itself on a discover
	// query; a language the caller asked for is kept as given (an explicit
	// ask is served like a search, never thinned).
	if _, englishOnly := discCurrentSettings(); englishOnly && params.Get("with_original_language") == "" {
		params.Set("with_original_language", "en")
	}

	f.genres = discParseIDFilter(params.Get("with_genres"))
	f.year, _ = strconv.Atoi(params.Get(spec.yearKey))
	f.dateGTE = params.Get(spec.dateKeys[0])
	f.dateLTE = params.Get(spec.dateKeys[1])
	if v := params.Get("vote_average.gte"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			f.voteAvgGTE, f.hasVoteAvg = n, true
		}
	}
	f.voteCount, _ = strconv.Atoi(params.Get("vote_count.gte"))
	f.language = params.Get("with_original_language")
	// watch_region only changes which providers are listed (the lookup
	// routes); the title→provider attachments are region-free.
	f.providers = discParseIDFilter(params.Get("with_watch_providers"))
	f.keywords = discParseIDFilter(params.Get("with_keywords"))
	f.companies = discParseIDFilter(params.Get("with_companies"))
	return f, nil
}

// discDateInRange applies inclusive string bounds to a YYYY-MM-DD date; an
// empty date never satisfies a bound.
func discDateInRange(date, gte, lte string) bool {
	if gte != "" && (date == "" || date < gte) {
		return false
	}
	if lte != "" && (date == "" || date > lte) {
		return false
	}
	return true
}

func (f discBrowseFilter) matchMovie(m *DemoMovie) bool {
	if !f.genres.matches(m.GenreIDs()) {
		return false
	}
	if f.year > 0 && m.Year() != f.year {
		return false
	}
	if !discDateInRange(m.ReleaseDate, f.dateGTE, f.dateLTE) {
		return false
	}
	if f.hasVoteAvg && m.VoteAverage < f.voteAvgGTE {
		return false
	}
	if f.voteCount > 0 && m.VoteCount < f.voteCount {
		return false
	}
	if f.language != "" && !strings.EqualFold(m.OriginalLanguage, f.language) {
		return false
	}
	return f.providers.matches(discMovieProviderIDs[m.TmdbID]) &&
		f.keywords.matches(discMovieKeywordIDs[m.TmdbID]) &&
		f.companies.matches(discMovieCompanyIDs[m.TmdbID])
}

func (f discBrowseFilter) matchShow(s *DemoShow) bool {
	if !f.genres.matches(discShowGenreIDs(s)) {
		return false
	}
	if f.year > 0 && s.Year() != f.year {
		return false
	}
	if !discDateInRange(s.FirstAirDate, f.dateGTE, f.dateLTE) {
		return false
	}
	if f.hasVoteAvg && s.VoteAverage < f.voteAvgGTE {
		return false
	}
	if f.voteCount > 0 && s.VoteCount < f.voteCount {
		return false
	}
	if f.language != "" && !strings.EqualFold(s.OriginalLanguage, f.language) {
		return false
	}
	return f.providers.matches(discTVProviderIDs[s.TmdbID]) &&
		f.keywords.matches(discTVKeywordIDs[s.TmdbID]) &&
		f.companies.matches(discTVCompanyIDs[s.TmdbID])
}

// discMovieRevenue is the revenue the extras table knows (0 = unknown).
func discMovieRevenue(m *DemoMovie) int {
	if x := discMovieExtras[m.TmdbID]; x != nil {
		return x.Revenue
	}
	return 0
}

func discMovieLess(field string) func(a, b *DemoMovie) bool {
	switch field {
	case "vote_average":
		return func(a, b *DemoMovie) bool { return a.VoteAverage < b.VoteAverage }
	case "vote_count":
		return func(a, b *DemoMovie) bool { return a.VoteCount < b.VoteCount }
	case "primary_release_date":
		return func(a, b *DemoMovie) bool { return a.ReleaseDate < b.ReleaseDate }
	case "revenue":
		return func(a, b *DemoMovie) bool { return discMovieRevenue(a) < discMovieRevenue(b) }
	case "title":
		return func(a, b *DemoMovie) bool { return a.Title < b.Title }
	case "original_title":
		return func(a, b *DemoMovie) bool { return a.OriginalTitle < b.OriginalTitle }
	default: // popularity
		return func(a, b *DemoMovie) bool { return a.Popularity < b.Popularity }
	}
}

func discShowLess(field string) func(a, b *DemoShow) bool {
	switch field {
	case "vote_average":
		return func(a, b *DemoShow) bool { return a.VoteAverage < b.VoteAverage }
	case "vote_count":
		return func(a, b *DemoShow) bool { return a.VoteCount < b.VoteCount }
	case "first_air_date":
		return func(a, b *DemoShow) bool { return a.FirstAirDate < b.FirstAirDate }
	case "name", "original_name":
		return func(a, b *DemoShow) bool { return a.Name < b.Name }
	default: // popularity
		return func(a, b *DemoShow) bool { return a.Popularity < b.Popularity }
	}
}

// discHandleDiscoverMovies / discHandleDiscoverTV are the filterable browse
// feeds. With no upstream, "push the limits into the query" and "filter on
// the way out" are the same loop: the policy runs on the matched list.
func discHandleDiscoverMovies(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	f, err := discBrowseQuery(r, discMovieBrowse)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	matched := make([]*DemoMovie, 0, len(demoMovies))
	for _, m := range demoMovies {
		if f.matchMovie(m) {
			matched = append(matched, m)
		}
	}
	less := discMovieLess(f.sortField)
	sort.SliceStable(matched, func(i, j int) bool {
		if f.sortDesc {
			return less(matched[j], matched[i])
		}
		return less(matched[i], matched[j])
	})
	writeJSON(w, http.StatusOK, discEnvelope(f.page, discMovieItems(cpKeepMovies(u, matched), false)))
}

func discHandleDiscoverTV(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	f, err := discBrowseQuery(r, discTVBrowse)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	matched := make([]*DemoShow, 0, len(demoShows))
	for _, s := range demoShows {
		if f.matchShow(s) {
			matched = append(matched, s)
		}
	}
	less := discShowLess(f.sortField)
	sort.SliceStable(matched, func(i, j int) bool {
		if f.sortDesc {
			return less(matched[j], matched[i])
		}
		return less(matched[i], matched[j])
	})
	writeJSON(w, http.StatusOK, discEnvelope(f.page, discTVItems(cpKeepShows(u, matched), false)))
}

// ─── Search ─────────────────────────────────────────────────────────────

func discHandleSearch(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	if query == "" {
		writeErr(w, http.StatusBadRequest, "query parameter required")
		return
	}
	page := discQueryPage(r)
	q := strings.ToLower(query)

	titles := make([]map[string]any, 0)
	for _, m := range demoMovies {
		if strings.Contains(strings.ToLower(m.Title), q) || strings.Contains(strings.ToLower(m.OriginalTitle), q) {
			titles = append(titles, discMovieItem(m, true))
		}
	}
	for _, s := range demoShows {
		if strings.Contains(strings.ToLower(s.Name), q) {
			titles = append(titles, discTVItem(s, true))
		}
	}
	// Titles pass the caller's policy; people stay (never adult, and their
	// known_for is always empty, so there is nothing to rewrite).
	results := cpKeepItems(u, titles)
	for _, p := range discPersons {
		if strings.Contains(strings.ToLower(p.Name), q) {
			results = append(results, discPersonItem(p))
		}
	}
	// Multi-search is NEVER english-filtered (srv-discover §1.5).
	writeJSON(w, http.StatusOK, discEnvelope(page, results))
}

// ─── Lookups behind the filter sheet ────────────────────────────────────

// discHandleLanguages serves the language table as a BARE ARRAY (the app
// casts the body to a list; an object throws).
func discHandleLanguages(w http.ResponseWriter, _ *http.Request) {
	out := make([]map[string]any, 0, len(discLanguages))
	for _, l := range discLanguages {
		out = append(out, map[string]any{"iso_639_1": l.Code, "english_name": l.English, "name": l.Native})
	}
	writeJSON(w, http.StatusOK, out)
}

func discProviderByID(id int) (discWatchProvider, bool) {
	for _, p := range discProviders {
		if p.ID == id {
			return p, true
		}
	}
	return discWatchProvider{}, false
}

// discHandleProvidersMovie / discHandleProvidersTV list the streaming
// services for one media type in one region (default US; an unknown region
// lists nothing). Always {"results": [...]} — the app hard-casts the key.
func discHandleProvidersMovie(w http.ResponseWriter, r *http.Request) {
	discServeProviders(w, r, true)
}

func discHandleProvidersTV(w http.ResponseWriter, r *http.Request) {
	discServeProviders(w, r, false)
}

func discServeProviders(w http.ResponseWriter, r *http.Request, movie bool) {
	region := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("region")))
	if region == "" {
		region = "US"
	}
	lists := discProvidersByRegion[region]
	ids := lists.TV
	if movie {
		ids = lists.Movie
	}
	results := make([]map[string]any, 0, len(ids))
	for i, id := range ids {
		p, ok := discProviderByID(id)
		if !ok {
			continue
		}
		results = append(results, map[string]any{
			"provider_id":      p.ID,
			"provider_name":    p.Name,
			"logo_path":        nil,
			"display_priority": i + 1,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func discHandleProviderRegions(w http.ResponseWriter, _ *http.Request) {
	results := make([]map[string]any, 0, len(discRegions))
	for _, rg := range discRegions {
		results = append(results, map[string]any{"iso_3166_1": rg.Code, "english_name": rg.English, "native_name": rg.Native})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// discLookupQuery is the shared front of the keyword and company searches:
// query required (the real server's exact 400), page clamped, matching by
// case-insensitive substring.
func discLookupQuery(w http.ResponseWriter, r *http.Request) (needle string, page int, ok bool) {
	query := r.URL.Query().Get("query")
	if query == "" {
		writeErr(w, http.StatusBadRequest, "query parameter required")
		return "", 0, false
	}
	return strings.ToLower(strings.TrimSpace(query)), discQueryPage(r), true
}

func discHandleSearchKeyword(w http.ResponseWriter, r *http.Request) {
	needle, page, ok := discLookupQuery(w, r)
	if !ok {
		return
	}
	results := make([]map[string]any, 0)
	for _, k := range discKeywords {
		if strings.Contains(strings.ToLower(k.Name), needle) {
			results = append(results, map[string]any{"id": k.ID, "name": k.Name})
		}
	}
	writeJSON(w, http.StatusOK, discEnvelope(page, results))
}

func discHandleSearchCompany(w http.ResponseWriter, r *http.Request) {
	needle, page, ok := discLookupQuery(w, r)
	if !ok {
		return
	}
	results := make([]map[string]any, 0)
	for _, c := range discCompanies {
		if strings.Contains(strings.ToLower(c.Name), needle) {
			results = append(results, discCompanyJSON(c))
		}
	}
	writeJSON(w, http.StatusOK, discEnvelope(page, results))
}

// ─── Genres ─────────────────────────────────────────────────────────────

// The genre lists drop a kids account's blocked genres, so the browse strip
// never offers a genre whose grid would arrive empty.
func discHandleMovieGenres(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	writeJSON(w, http.StatusOK, map[string]any{"genres": discGenreObjs(cpKeepGenres(u, mediaTypeMovie, discMovieGenres))})
}

func discHandleTVGenres(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	writeJSON(w, http.StatusOK, map[string]any{"genres": discGenreObjs(cpKeepGenres(u, mediaTypeTV, discTVGenres))})
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

// discNotAvailable is the real server's answer for a title the caller's
// content policy hides; the app renders it as "not available to your
// account", never as a failure.
func discNotAvailable(w http.ResponseWriter) {
	writeErr(w, http.StatusNotFound, "not available")
}

// discCreditPerson renders the person fields shared by cast, crew, and
// created_by entries.
func discCreditPerson(p *DemoPerson) map[string]any {
	return map[string]any{
		"adult":                false,
		"gender":               p.Gender,
		"id":                   p.ID,
		"known_for_department": p.KnownForDept,
		"name":                 p.Name,
		"original_name":        p.Name,
		"popularity":           p.Popularity,
		"profile_path":         discNullable(p.ProfilePath),
	}
}

// discTitleCredits renders a title's credits append from its cast/crew
// tables (names resolved through the person catalog; discLinkCredits
// already proved every name resolves).
func discTitleCredits(tmdbID int, cast []discCastRef, crew []discCrewRef) map[string]any {
	castOut := make([]map[string]any, 0, len(cast))
	for i, c := range cast {
		p, ok := discPersonByName(c.Person)
		if !ok {
			continue
		}
		item := discCreditPerson(p)
		item["cast_id"] = i + 1
		item["character"] = c.Character
		item["credit_id"] = fmt.Sprintf("demo-%d-cast-%d", tmdbID, i)
		item["order"] = i
		castOut = append(castOut, item)
	}
	crewOut := make([]map[string]any, 0, len(crew))
	for i, c := range crew {
		p, ok := discPersonByName(c.Person)
		if !ok {
			continue
		}
		item := discCreditPerson(p)
		item["credit_id"] = fmt.Sprintf("demo-%d-crew-%d", tmdbID, i)
		item["department"] = c.Department
		item["job"] = c.Job
		crewOut = append(crewOut, item)
	}
	return map[string]any{"cast": castOut, "crew": crewOut}
}

func discCompaniesJSON(ids []int) []map[string]any {
	out := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		if c, ok := discCompanyByID(id); ok {
			out = append(out, discCompanyJSON(c))
		}
	}
	return out
}

func discCountriesJSON(codes []string) []map[string]any {
	out := make([]map[string]any, 0, len(codes))
	for _, code := range codes {
		out = append(out, discCountryJSON(code))
	}
	return out
}

// discMovieReleaseDatesJSON renders the release_dates append: the origin
// region first (its theatrical milestone is the catalog release_date), then
// the US block, each with the verified historical milestones from the
// extras table; the US block also carries the Radarr fixture's digital
// (type 4) and physical (type 5) dates so the title page and the arr
// calendar agree. certification comes from the content-policy map, "" when
// the region has no entry. A region with no milestone is skipped.
func discMovieReleaseDatesJSON(m *DemoMovie, x *discMovieExtra) map[string]any {
	origin := discMovieCountries(m)[0]
	regions := []string{origin}
	if origin != "US" {
		regions = append(regions, "US")
	}
	_, digital, physical := arrMovieReleaseMilestones(m.TmdbID)
	results := make([]map[string]any, 0, len(regions))
	for _, region := range regions {
		entries := make([]discRelease, 0, 4)
		if region == origin && m.ReleaseDate != "" {
			entries = append(entries, discRelease{3, m.ReleaseDate})
		}
		if x != nil {
			entries = append(entries, x.Releases[region]...)
		}
		if region == "US" {
			if digital != "" {
				entries = append(entries, discRelease{4, digital})
			}
			if physical != "" {
				entries = append(entries, discRelease{5, physical})
			}
		}
		if len(entries) == 0 {
			continue
		}
		cert := cpMovieCerts[m.TmdbID][region]
		list := make([]map[string]any, 0, len(entries))
		for _, e := range entries {
			list = append(list, map[string]any{
				"certification": cert,
				"descriptors":   []string{},
				"iso_639_1":     "",
				"note":          "",
				"release_date":  e.Date + "T00:00:00.000Z",
				"type":          e.Type,
			})
		}
		results = append(results, map[string]any{"iso_3166_1": region, "release_dates": list})
	}
	return map[string]any{"results": results}
}

// discShowContentRatingsJSON renders the content_ratings append from the
// content-policy map, regions in code order.
func discShowContentRatingsJSON(tmdbID int) map[string]any {
	certs := cpShowCerts[tmdbID]
	regions := make([]string, 0, len(certs))
	for region := range certs {
		regions = append(regions, region)
	}
	sort.Strings(regions)
	results := make([]map[string]any, 0, len(regions))
	for _, region := range regions {
		results = append(results, map[string]any{
			"descriptors": []string{},
			"iso_3166_1":  region,
			"rating":      certs[region],
		})
	}
	return map[string]any{"results": results}
}

func discHandleMovieDetail(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
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
	if !policyAllowsMovie(u, m) {
		discNotAvailable(w)
		return
	}
	x := discMovieExtras[m.TmdbID]
	if x == nil {
		x = &discMovieExtra{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"adult":                 false,
		"backdrop_path":         m.BackdropPath,
		"belongs_to_collection": nil,
		"budget":                x.Budget,
		"credits":               discTitleCredits(m.TmdbID, x.Cast, x.Crew),
		"genres":                discGenreObjs(m.Genres),
		"homepage":              "",
		"id":                    m.TmdbID,
		"imdb_id":               discNullable(m.ImdbID),
		"origin_country":        discMovieCountries(m),
		"original_language":     m.OriginalLanguage,
		"original_title":        m.OriginalTitle,
		"overview":              m.Overview,
		"popularity":            m.Popularity,
		"poster_path":           m.PosterPath,
		"production_companies":  discCompaniesJSON(x.Companies),
		"production_countries":  discCountriesJSON(discMovieCountries(m)),
		"release_date":          m.ReleaseDate,
		"release_dates":         discMovieReleaseDatesJSON(m, x),
		"revenue":               x.Revenue,
		"runtime":               m.Runtime,
		"spoken_languages":      []map[string]any{discSpokenLanguageJSON(m.OriginalLanguage)},
		"status":                "Released",
		"tagline":               m.Tagline,
		"title":                 m.Title,
		"video":                 false,
		"vote_average":          m.VoteAverage,
		"vote_count":            m.VoteCount,
		"videos":                map[string]any{"results": []any{}},
	})
}

// discShowLastAirDate is the air date of the latest episode that has aired
// (today or earlier); nil for a show that has not premiered, as TMDB sends
// it.
func discShowLastAirDate(s *DemoShow) any {
	today := discTodayDate().Format("2006-01-02")
	last := ""
	for _, se := range s.Seasons {
		for _, ep := range se.Episodes {
			if ep.AirDate <= today && ep.AirDate > last {
				last = ep.AirDate
			}
		}
	}
	return discNullable(last)
}

func discHandleTVDetail(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
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
	if !policyAllowsShow(u, s) {
		discNotAvailable(w)
		return
	}
	x := discShowExtras[s.TmdbID]
	if x == nil {
		x = &discShowExtra{}
	}

	seasons := make([]map[string]any, 0, len(s.Seasons))
	for _, se := range s.Seasons {
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
	createdBy := make([]map[string]any, 0, len(x.CreatedBy))
	for i, name := range x.CreatedBy {
		p, ok := discPersonByName(name)
		if !ok {
			continue
		}
		createdBy = append(createdBy, map[string]any{
			"id":            p.ID,
			"credit_id":     fmt.Sprintf("demo-%d-creator-%d", s.TmdbID, i),
			"name":          p.Name,
			"original_name": p.Name,
			"gender":        p.Gender,
			"profile_path":  discNullable(p.ProfilePath),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"adult":                false,
		"backdrop_path":        s.BackdropPath,
		"content_ratings":      discShowContentRatingsJSON(s.TmdbID),
		"created_by":           createdBy,
		"credits":              discTitleCredits(s.TmdbID, x.Cast, x.Crew),
		"first_air_date":       s.FirstAirDate,
		"genres":               discGenreObjs(s.Genres),
		"homepage":             "",
		"id":                   s.TmdbID,
		"in_production":        s.Status == "Returning Series" || s.Status == "In Production",
		"languages":            []string{s.OriginalLanguage},
		"last_air_date":        discShowLastAirDate(s),
		"name":                 s.Name,
		"networks":             discCompaniesJSON(x.Networks),
		"number_of_episodes":   s.EpisodeCount(),
		"number_of_seasons":    s.SeasonCount(),
		"origin_country":       discShowCountries(s),
		"original_language":    s.OriginalLanguage,
		"original_name":        s.Name,
		"overview":             s.Overview,
		"popularity":           s.Popularity,
		"poster_path":          s.PosterPath,
		"production_companies": discCompaniesJSON(x.Companies),
		"production_countries": discCountriesJSON(discShowCountries(s)),
		"seasons":              seasons,
		"status":               s.Status,
		"tagline":              s.Tagline,
		"type":                 s.Type,
		"vote_average":         s.VoteAverage,
		"vote_count":           s.VoteCount,
		"videos":               map[string]any{"results": []any{}},
		"external_ids": map[string]any{
			"imdb_id":      discNullable(s.ImdbID),
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
// other catalog films (thinned by the policy after the window is cut, like
// a fetched page). Unknown ids get an EMPTY page, never an error — the
// detail screen is all-or-nothing.
func discHandleMovieRelated(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	page := discQueryPage(r)
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
	writeJSON(w, http.StatusOK, discEnvelope(page, discFilterEnglish(discMovieItems(cpKeepMovies(u, others), false))))
}

// discHandleTVRelated serves recommendations/similar for TV: every other
// catalog show; empty page for unknown ids.
func discHandleTVRelated(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	page := discQueryPage(r)
	id, _ := discPathID(r)
	others := make([]*DemoShow, 0, len(demoShows))
	if _, found := findShow(id); found {
		for _, s := range demoShows {
			if s.TmdbID != id {
				others = append(others, s)
			}
		}
	}
	writeJSON(w, http.StatusOK, discEnvelope(page, discFilterEnglish(discTVItems(cpKeepShows(u, others), false))))
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
	alsoKnownAs := p.AlsoKnownAs
	if alsoKnownAs == nil {
		alsoKnownAs = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"adult":                false,
		"also_known_as":        alsoKnownAs,
		"biography":            p.Biography,
		"birthday":             discNullable(p.Birthday),
		"deathday":             discNullable(p.Deathday),
		"gender":               p.Gender,
		"homepage":             nil,
		"id":                   p.ID,
		"imdb_id":              discNullable(p.ImdbID),
		"known_for_department": p.KnownForDept,
		"name":                 p.Name,
		"place_of_birth":       discNullable(p.PlaceOfBirth),
		"popularity":           p.Popularity,
		"profile_path":         discNullable(p.ProfilePath),
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
			item["original_title"] = m.OriginalTitle
			item["poster_path"] = m.PosterPath
			item["vote_average"] = m.VoteAverage
			item["release_date"] = m.ReleaseDate
			item["overview"] = m.Overview
			item["genre_ids"] = m.GenreIDs()
			item["popularity"] = m.Popularity
		}
	case mediaTypeTV:
		if s, found := findShow(c.TmdbID); found {
			item["name"] = s.Name
			item["original_name"] = s.Name
			item["poster_path"] = s.PosterPath
			item["vote_average"] = s.VoteAverage
			item["first_air_date"] = s.FirstAirDate
			item["overview"] = s.Overview
			item["genre_ids"] = discShowGenreIDs(s)
			item["popularity"] = s.Popularity
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
	u := userFrom(r)
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
	// Rows carry id + media_type, so the policy judges them like any list;
	// a person whose only credit is hidden shows an empty filmography, which
	// is the real behaviour.
	writeJSON(w, http.StatusOK, map[string]any{
		"id":   p.ID,
		"cast": cpKeepItems(u, cast),
		"crew": cpKeepItems(u, crew),
	})
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
