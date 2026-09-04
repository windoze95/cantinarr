package discover

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/cache"
	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/serversettings"
)

// TTL constants for different content types.
const (
	ttlTrending       = 5 * time.Minute
	ttlDetails        = 1 * time.Hour
	ttlGenresProvider = 24 * time.Hour
	ttlRecommendation = 30 * time.Minute
	ttlTrakt          = 10 * time.Minute
)

// DiscoveryPrefs supplies the admin-chosen discovery preferences. The handler
// reads them per request, so changing the source or the language filter takes
// effect on the next row refresh rather than on the next restart.
type DiscoveryPrefs interface {
	Get() serversettings.Settings
}

// Handler serves discovery endpoints, proxying TMDB/Trakt with caching.
type Handler struct {
	creds *credentials.Registry
	cache *cache.Cache
	prefs DiscoveryPrefs
	// policies filters every payload for kids accounts after the cache
	// read; nil until wired (see SetContentPolicy).
	policies *contentpolicy.Service
}

// NewHandler creates a new discover handler.
func NewHandler(creds *credentials.Registry, c *cache.Cache, prefs DiscoveryPrefs) *Handler {
	return &Handler{creds: creds, cache: c, prefs: prefs}
}

// discoveryPrefs reads the current preferences, falling back to the defaults
// when no settings service is wired in.
func (h *Handler) discoveryPrefs() (source string, englishOnly bool) {
	if h.prefs == nil {
		return serversettings.DefaultSourceFor(h.creds.Trakt() != nil), serversettings.DefaultDiscoveryEnglishOnly
	}
	current := h.prefs.Get()
	return current.DiscoverySource, current.DiscoveryEnglishOnly
}

// helper: write raw JSON bytes as response
func writeJSON(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func queryInt(r *http.Request, key string, fallback int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// queryPage reads `page` clamped to 1..MaxTMDBPage. Clamping rather than
// rejecting lets a client scrolling off the end stop cleanly: the body
// reports the page actually served, and anything past the cap answers as
// the cap instead of as an upstream error.
func queryPage(r *http.Request) int {
	return ClampPage(queryInt(r, "page", 1))
}

// cachedTMDB checks credentials, then cache, then calls TMDB on miss. The body
// comes back verbatim — use it for lookups the admin's row preferences must not
// touch (search, details, genres, providers). The shape says what a kids
// account's filter should look for in it on the way out.
func (h *Handler) cachedTMDB(w http.ResponseWriter, r *http.Request, cacheKey string, ttl time.Duration, path string, params url.Values, shape payloadShape) {
	h.serveTMDB(w, r, cacheKey, ttl, path, params, false, shape)
}

// cachedTMDBFeed is cachedTMDB for the discovery feeds, honoring the admin's
// English-only preference. The preference is part of the cache key so flipping
// it never serves the other variant's rows.
func (h *Handler) cachedTMDBFeed(w http.ResponseWriter, r *http.Request, cacheKey string, ttl time.Duration, path string, params url.Values, shape payloadShape) {
	_, englishOnly := h.discoveryPrefs()
	if englishOnly {
		cacheKey += ":en"
	}
	h.serveTMDB(w, r, cacheKey, ttl, path, params, englishOnly, shape)
}

// serveTMDB is the one funnel every TMDB body passes: cache, else upstream
// (English-filtered and cached, server-wide), then the caller's own content
// policy on the way out, which is per user and therefore never cached.
func (h *Handler) serveTMDB(w http.ResponseWriter, r *http.Request, cacheKey string, ttl time.Duration, path string, params url.Values, englishOnly bool, shape payloadShape) {
	tmdbClient := h.creds.TMDB()
	if tmdbClient == nil {
		http.Error(w, `{"error":"TMDB is not configured"}`, http.StatusServiceUnavailable)
		return
	}
	data, ok := h.cache.Get(cacheKey)
	if !ok {
		var err error
		data, err = tmdbClient.DoGetRaw(path, params)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadGateway)
			return
		}
		if englishOnly {
			data = filterEnglishFeed(data)
		}
		h.cache.Set(cacheKey, data, ttl)
	}
	h.writeForCaller(w, r, shape, data)
}

// cachedTrakt checks credentials, then cache, then calls Trakt on miss; the
// caller's content policy applies on the way out like serveTMDB.
func (h *Handler) cachedTrakt(w http.ResponseWriter, r *http.Request, cacheKey string, ttl time.Duration, path string, params url.Values, shape traktShape) {
	traktClient := h.creds.Trakt()
	if traktClient == nil {
		http.Error(w, `{"error":"trakt not configured"}`, http.StatusServiceUnavailable)
		return
	}
	data, ok := h.cache.Get(cacheKey)
	if !ok {
		var err error
		data, err = traktClient.DoGetRaw(path, params)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadGateway)
			return
		}
		h.cache.Set(cacheKey, data, ttl)
	}
	h.writeTraktForCaller(w, r, shape, data)
}

// ─── TMDB Endpoints ─────────────────────────────────────

func (h *Handler) Trending(w http.ResponseWriter, r *http.Request) {
	tw := r.URL.Query().Get("time_window")
	if tw == "" {
		tw = "day"
	}
	page := queryPage(r)
	key := fmt.Sprintf("trending:%s:%d", tw, page)
	params := url.Values{"page": {strconv.Itoa(page)}}
	h.cachedTMDBFeed(w, r, key, ttlTrending, fmt.Sprintf("/trending/all/%s", tw), params, shapeMixedList)
}

func (h *Handler) PopularMovies(w http.ResponseWriter, r *http.Request) {
	page := queryPage(r)
	key := fmt.Sprintf("pop_movies:%d", page)
	params := url.Values{"page": {strconv.Itoa(page)}}
	h.cachedTMDBFeed(w, r, key, ttlTrending, "/movie/popular", params, shapeMovieList)
}

func (h *Handler) PopularTV(w http.ResponseWriter, r *http.Request) {
	page := queryPage(r)
	key := fmt.Sprintf("pop_tv:%d", page)
	params := url.Values{"page": {strconv.Itoa(page)}}
	h.cachedTMDBFeed(w, r, key, ttlTrending, "/tv/popular", params, shapeTVList)
}

// OnTheAirTV backs the Airing This Week row: TMDB's /tv/on_the_air lists
// series with an episode airing in the next seven days.
func (h *Handler) OnTheAirTV(w http.ResponseWriter, r *http.Request) {
	page := queryPage(r)
	key := fmt.Sprintf("on_air_tv:%d", page)
	params := url.Values{"page": {strconv.Itoa(page)}}
	h.cachedTMDBFeed(w, r, key, ttlTrending, "/tv/on_the_air", params, shapeTVList)
}

func (h *Handler) TopRatedTV(w http.ResponseWriter, r *http.Request) {
	page := queryPage(r)
	key := fmt.Sprintf("top_tv:%d", page)
	params := url.Values{"page": {strconv.Itoa(page)}}
	h.cachedTMDBFeed(w, r, key, ttlTrending, "/tv/top_rated", params, shapeTVList)
}

func (h *Handler) TopRatedMovies(w http.ResponseWriter, r *http.Request) {
	page := queryPage(r)
	key := fmt.Sprintf("top_movies:%d", page)
	params := url.Values{"page": {strconv.Itoa(page)}}
	h.cachedTMDBFeed(w, r, key, ttlTrending, "/movie/top_rated", params, shapeMovieList)
}

// UpcomingMovies backs the Coming Soon row. TMDB's /movie/upcoming matches any
// country's theatrical window, so a title already streaming here — or a 1994
// re-release — still counts as "upcoming" somewhere and lands in the row.
// Discovering on the primary release date instead keeps the row to movies not
// released anywhere yet, and the three-month cap leaves far-future titles to
// the Most Anticipated row.
func (h *Handler) UpcomingMovies(w http.ResponseWriter, r *http.Request) {
	h.upcoming(w, r, "upcoming", "/discover/movie", "primary_release_date", shapeMovieList)
}

// UpcomingTV discovers on the first air date, so the row is premieres only: a
// returning season carries a first air date years back and stays out. The
// three-month cap leaves far-future titles to Most Anticipated.
func (h *Handler) UpcomingTV(w http.ResponseWriter, r *http.Request) {
	h.upcoming(w, r, "upcoming_tv", "/discover/tv", "first_air_date", shapeTVList)
}

// upcoming backs both Coming Soon rows: a discover query on the type's own
// date field from today through three months out, most popular first.
func (h *Handler) upcoming(w http.ResponseWriter, r *http.Request, keyPrefix, path, dateField string, shape payloadShape) {
	page := queryPage(r)
	now := time.Now()
	from := now.Format("2006-01-02")
	to := now.AddDate(0, 3, 0).Format("2006-01-02")
	key := fmt.Sprintf("%s:%s:%d", keyPrefix, from, page)
	params := url.Values{
		"page":             {strconv.Itoa(page)},
		"sort_by":          {"popularity.desc"},
		dateField + ".gte": {from},
		dateField + ".lte": {to},
	}
	// A discover query, so the language preference goes upstream and the
	// row's page arrives full (see discoverQuery).
	if _, englishOnly := h.discoveryPrefs(); englishOnly {
		params.Set("with_original_language", "en")
	}
	h.cachedTMDBFeed(w, r, key, ttlTrending, path, params, shape)
}

func (h *Handler) NowPlayingMovies(w http.ResponseWriter, r *http.Request) {
	page := queryPage(r)
	key := fmt.Sprintf("now_playing:%d", page)
	params := url.Values{"page": {strconv.Itoa(page)}}
	h.cachedTMDBFeed(w, r, key, ttlTrending, "/movie/now_playing", params, shapeMovieList)
}

// DiscoverMovies and DiscoverTV are the filterable browse feeds; the
// allowlist, validation, and guards live in browse.go.
func (h *Handler) DiscoverMovies(w http.ResponseWriter, r *http.Request) {
	h.discover(w, r, movieDiscover)
}

func (h *Handler) DiscoverTV(w http.ResponseWriter, r *http.Request) {
	h.discover(w, r, tvDiscover)
}

func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("query")
	if query == "" {
		http.Error(w, `{"error":"query parameter required"}`, http.StatusBadRequest)
		return
	}
	page := queryPage(r)
	key := fmt.Sprintf("search:%s:%d", query, page)
	params := url.Values{"query": {query}, "page": {strconv.Itoa(page)}}
	h.cachedTMDB(w, r, key, ttlTrending, "/search/multi", params, shapeMultiSearch)
}

func (h *Handler) MovieDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	key := "movie:" + id
	params := url.Values{"append_to_response": {"videos,release_dates,credits"}}
	h.cachedTMDB(w, r, key, ttlDetails, fmt.Sprintf("/movie/%s", id), params, shapeMovieDetail)
}

func (h *Handler) TVDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	key := "tv:" + id
	// content_ratings rides along so a kids account's verdict comes from
	// the body the detail read already fetched.
	params := url.Values{"append_to_response": {"videos,external_ids,credits,content_ratings"}}
	h.cachedTMDB(w, r, key, ttlDetails, fmt.Sprintf("/tv/%s", id), params, shapeTVDetail)
}

func (h *Handler) PersonDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	key := "person:" + id
	h.cachedTMDB(w, r, key, ttlDetails, fmt.Sprintf("/person/%s", id), nil, shapePersonDetail)
}

func (h *Handler) PersonCredits(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	key := "person_credits:" + id
	h.cachedTMDB(w, r, key, ttlDetails, fmt.Sprintf("/person/%s/combined_credits", id), nil, shapePersonCredits)
}

func (h *Handler) MovieRecommendations(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	page := queryPage(r)
	key := fmt.Sprintf("movie_rec:%s:%d", id, page)
	params := url.Values{"page": {strconv.Itoa(page)}}
	h.cachedTMDBFeed(w, r, key, ttlRecommendation, fmt.Sprintf("/movie/%s/recommendations", id), params, shapeMovieList)
}

func (h *Handler) TVRecommendations(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	page := queryPage(r)
	key := fmt.Sprintf("tv_rec:%s:%d", id, page)
	params := url.Values{"page": {strconv.Itoa(page)}}
	h.cachedTMDBFeed(w, r, key, ttlRecommendation, fmt.Sprintf("/tv/%s/recommendations", id), params, shapeTVList)
}

func (h *Handler) SimilarMovies(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	page := queryPage(r)
	key := fmt.Sprintf("movie_sim:%s:%d", id, page)
	params := url.Values{"page": {strconv.Itoa(page)}}
	h.cachedTMDBFeed(w, r, key, ttlRecommendation, fmt.Sprintf("/movie/%s/similar", id), params, shapeMovieList)
}

func (h *Handler) SimilarTV(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	page := queryPage(r)
	key := fmt.Sprintf("tv_sim:%s:%d", id, page)
	params := url.Values{"page": {strconv.Itoa(page)}}
	h.cachedTMDBFeed(w, r, key, ttlRecommendation, fmt.Sprintf("/tv/%s/similar", id), params, shapeTVList)
}

func (h *Handler) MovieGenres(w http.ResponseWriter, r *http.Request) {
	h.cachedTMDB(w, r, "genres_movie", ttlGenresProvider, "/genre/movie/list", nil, shapeMovieGenres)
}

func (h *Handler) TVGenres(w http.ResponseWriter, r *http.Request) {
	h.cachedTMDB(w, r, "genres_tv", ttlGenresProvider, "/genre/tv/list", nil, shapeTVGenres)
}

// Languages serves TMDB's language table, a bare array of
// {iso_639_1, english_name, name}; the browse filter sheet labels its
// Language menu from it.
func (h *Handler) Languages(w http.ResponseWriter, r *http.Request) {
	h.cachedTMDB(w, r, "languages", ttlGenresProvider, "/configuration/languages", nil, shapeOpaque)
}

func (h *Handler) MovieWatchProviders(w http.ResponseWriter, r *http.Request) {
	h.watchProviders(w, r, "movie")
}

func (h *Handler) TVWatchProviders(w http.ResponseWriter, r *http.Request) {
	h.watchProviders(w, r, "tv")
}

// watchProviders lists the streaming services TMDB knows for one media type
// in one region. The movie and TV sets differ, so each keys its own entry.
func (h *Handler) watchProviders(w http.ResponseWriter, r *http.Request, kind string) {
	region := r.URL.Query().Get("region")
	if region == "" {
		region = "US"
	}
	key := "providers_" + kind + ":" + region
	params := url.Values{"watch_region": {region}}
	h.cachedTMDB(w, r, key, ttlGenresProvider, "/watch/providers/"+kind, params, shapeOpaque)
}

// WatchProviderRegions lists the countries TMDB tracks streaming
// availability for, so the sheet can offer a region other than the device's.
func (h *Handler) WatchProviderRegions(w http.ResponseWriter, r *http.Request) {
	h.cachedTMDB(w, r, "providers_regions", ttlGenresProvider, "/watch/providers/regions", nil, shapeOpaque)
}

func (h *Handler) SearchKeywords(w http.ResponseWriter, r *http.Request) {
	h.lookupSearch(w, r, "keyword")
}

func (h *Handler) SearchCompanies(w http.ResponseWriter, r *http.Request) {
	h.lookupSearch(w, r, "company")
}

// lookupSearch is Search for TMDB's single-kind lookups behind the filter
// sheet's type-ahead fields: query required, page clamped, body verbatim.
func (h *Handler) lookupSearch(w http.ResponseWriter, r *http.Request, kind string) {
	query := r.URL.Query().Get("query")
	if query == "" {
		http.Error(w, `{"error":"query parameter required"}`, http.StatusBadRequest)
		return
	}
	page := queryPage(r)
	key := fmt.Sprintf("search_%s:%s:%d", kind, query, page)
	params := url.Values{"query": {query}, "page": {strconv.Itoa(page)}}
	h.cachedTMDB(w, r, key, ttlTrending, "/search/"+kind, params, shapeOpaque)
}

// ─── Trakt Endpoints ────────────────────────────────────

func (h *Handler) TraktTrending(w http.ResponseWriter, r *http.Request) {
	if h.creds.Trakt() == nil {
		http.Error(w, `{"error":"trakt not configured"}`, http.StatusServiceUnavailable)
		return
	}
	typ := r.URL.Query().Get("type")
	if typ == "" {
		typ = "movies"
	}
	page := queryPage(r)
	key := fmt.Sprintf("trakt_trend:%s:%d", typ, page)
	params := url.Values{"page": {strconv.Itoa(page)}, "limit": {"20"}, "extended": {"full"}}
	h.cachedTrakt(w, r, key, ttlTrakt, fmt.Sprintf("/%s/trending", typ), params, traktTypeShape(typ))
}

func (h *Handler) TraktPopular(w http.ResponseWriter, r *http.Request) {
	if h.creds.Trakt() == nil {
		http.Error(w, `{"error":"trakt not configured"}`, http.StatusServiceUnavailable)
		return
	}
	typ := r.URL.Query().Get("type")
	if typ == "" {
		typ = "movies"
	}
	page := queryPage(r)
	key := fmt.Sprintf("trakt_pop:%s:%d", typ, page)
	params := url.Values{"page": {strconv.Itoa(page)}, "limit": {"20"}, "extended": {"full"}}
	h.cachedTrakt(w, r, key, ttlTrakt, fmt.Sprintf("/%s/popular", typ), params, traktTypeShape(typ))
}

func (h *Handler) TraktPopularLists(w http.ResponseWriter, r *http.Request) {
	if h.creds.Trakt() == nil {
		http.Error(w, `{"error":"trakt not configured"}`, http.StatusServiceUnavailable)
		return
	}
	page := queryPage(r)
	key := fmt.Sprintf("trakt_lists:%d", page)
	params := url.Values{"page": {strconv.Itoa(page)}, "limit": {"20"}}
	h.cachedTrakt(w, r, key, ttlTrakt, "/lists/popular", params, traktShape{opaque: true})
}

func (h *Handler) TraktListItems(w http.ResponseWriter, r *http.Request) {
	if h.creds.Trakt() == nil {
		http.Error(w, `{"error":"trakt not configured"}`, http.StatusServiceUnavailable)
		return
	}
	user := chi.URLParam(r, "user")
	slug := chi.URLParam(r, "slug")
	key := fmt.Sprintf("trakt_list:%s/%s", user, slug)
	params := url.Values{"extended": {"full"}}
	h.cachedTrakt(w, r, key, ttlTrakt, fmt.Sprintf("/users/%s/lists/%s/items", user, slug), params, traktShape{})
}

func (h *Handler) TraktCalendar(w http.ResponseWriter, r *http.Request) {
	if h.creds.Trakt() == nil {
		http.Error(w, `{"error":"trakt not configured"}`, http.StatusServiceUnavailable)
		return
	}
	days := queryInt(r, "days", 14)
	today := time.Now().Format("2006-01-02")
	key := fmt.Sprintf("trakt_cal:%s:%d", today, days)
	h.cachedTrakt(w, r, key, ttlTrakt, fmt.Sprintf("/calendars/all/shows/%s/%d", today, days), nil, traktShape{mediaType: contentpolicy.MediaTV})
}

func (h *Handler) TraktAnticipated(w http.ResponseWriter, r *http.Request) {
	if h.creds.Trakt() == nil {
		http.Error(w, `{"error":"trakt not configured"}`, http.StatusServiceUnavailable)
		return
	}
	typ := r.URL.Query().Get("type")
	if typ == "" {
		typ = "movies"
	}
	page := queryPage(r)
	key := fmt.Sprintf("trakt_anticipated:%s:%d", typ, page)
	params := url.Values{"page": {strconv.Itoa(page)}, "limit": {"20"}, "extended": {"full"}}
	h.cachedTrakt(w, r, key, ttlTrakt, fmt.Sprintf("/%s/anticipated", typ), params, traktTypeShape(typ))
}

func (h *Handler) TraktRecommendations(w http.ResponseWriter, r *http.Request) {
	if h.creds.Trakt() == nil {
		http.Error(w, `{"error":"trakt not configured"}`, http.StatusServiceUnavailable)
		return
	}
	typ := r.URL.Query().Get("type")
	if typ == "" {
		typ = "movies"
	}
	// Normalize: strip trailing 's' for the Trakt API path if present
	apiType := strings.TrimSuffix(typ, "s")
	key := fmt.Sprintf("trakt_recs:%s", typ)
	params := url.Values{"limit": {"20"}, "extended": {"full"}}
	h.cachedTrakt(w, r, key, ttlTrakt, fmt.Sprintf("/recommendations/%s", apiType), params, traktTypeShape(typ))
}
