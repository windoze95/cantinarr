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
	"github.com/windoze95/cantinarr-server/internal/config"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
	"github.com/windoze95/cantinarr-server/internal/trakt"
)

// TTL constants for different content types.
const (
	ttlTrending       = 5 * time.Minute
	ttlDetails        = 1 * time.Hour
	ttlGenresProvider = 24 * time.Hour
	ttlRecommendation = 30 * time.Minute
	ttlTrakt          = 10 * time.Minute
)

// Handler serves discovery endpoints, proxying TMDB/Trakt with caching.
type Handler struct {
	tmdb  *tmdb.Client
	trakt *trakt.Client
	cache *cache.Cache
	cfg   *config.Config
}

// NewHandler creates a new discover handler.
func NewHandler(tmdbClient *tmdb.Client, traktClient *trakt.Client, c *cache.Cache, cfg *config.Config) *Handler {
	return &Handler{tmdb: tmdbClient, trakt: traktClient, cache: c, cfg: cfg}
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

// cachedTMDB checks cache, calls TMDB on miss, caches result.
func (h *Handler) cachedTMDB(w http.ResponseWriter, cacheKey string, ttl time.Duration, path string, params url.Values) {
	if data, ok := h.cache.Get(cacheKey); ok {
		writeJSON(w, data)
		return
	}
	data, err := h.tmdb.DoGetRaw(path, params)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadGateway)
		return
	}
	h.cache.Set(cacheKey, data, ttl)
	writeJSON(w, data)
}

// cachedTrakt checks cache, calls Trakt on miss, caches result.
func (h *Handler) cachedTrakt(w http.ResponseWriter, cacheKey string, ttl time.Duration, path string, params url.Values) {
	if data, ok := h.cache.Get(cacheKey); ok {
		writeJSON(w, data)
		return
	}
	data, err := h.trakt.DoGetRaw(path, params)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadGateway)
		return
	}
	h.cache.Set(cacheKey, data, ttl)
	writeJSON(w, data)
}

// ─── TMDB Endpoints ─────────────────────────────────────

func (h *Handler) Trending(w http.ResponseWriter, r *http.Request) {
	tw := r.URL.Query().Get("time_window")
	if tw == "" {
		tw = "day"
	}
	page := queryInt(r, "page", 1)
	key := fmt.Sprintf("trending:%s:%d", tw, page)
	params := url.Values{"page": {strconv.Itoa(page)}}
	h.cachedTMDB(w, key, ttlTrending, fmt.Sprintf("/trending/all/%s", tw), params)
}

func (h *Handler) PopularMovies(w http.ResponseWriter, r *http.Request) {
	page := queryInt(r, "page", 1)
	key := fmt.Sprintf("pop_movies:%d", page)
	params := url.Values{"page": {strconv.Itoa(page)}}
	h.cachedTMDB(w, key, ttlTrending, "/movie/popular", params)
}

func (h *Handler) PopularTV(w http.ResponseWriter, r *http.Request) {
	page := queryInt(r, "page", 1)
	key := fmt.Sprintf("pop_tv:%d", page)
	params := url.Values{"page": {strconv.Itoa(page)}}
	h.cachedTMDB(w, key, ttlTrending, "/tv/popular", params)
}

func (h *Handler) TopRatedMovies(w http.ResponseWriter, r *http.Request) {
	page := queryInt(r, "page", 1)
	key := fmt.Sprintf("top_movies:%d", page)
	params := url.Values{"page": {strconv.Itoa(page)}}
	h.cachedTMDB(w, key, ttlTrending, "/movie/top_rated", params)
}

func (h *Handler) UpcomingMovies(w http.ResponseWriter, r *http.Request) {
	page := queryInt(r, "page", 1)
	key := fmt.Sprintf("upcoming:%d", page)
	params := url.Values{"page": {strconv.Itoa(page)}}
	h.cachedTMDB(w, key, ttlTrending, "/movie/upcoming", params)
}

func (h *Handler) NowPlayingMovies(w http.ResponseWriter, r *http.Request) {
	page := queryInt(r, "page", 1)
	key := fmt.Sprintf("now_playing:%d", page)
	params := url.Values{"page": {strconv.Itoa(page)}}
	h.cachedTMDB(w, key, ttlTrending, "/movie/now_playing", params)
}

func (h *Handler) DiscoverMovies(w http.ResponseWriter, r *http.Request) {
	params := url.Values{}
	for _, k := range []string{"page", "with_genres", "sort_by", "primary_release_year", "with_watch_providers", "watch_region"} {
		if v := r.URL.Query().Get(k); v != "" {
			params.Set(k, v)
		}
	}
	if params.Get("page") == "" {
		params.Set("page", "1")
	}
	key := "disc_movies:" + params.Encode()
	h.cachedTMDB(w, key, ttlTrending, "/discover/movie", params)
}

func (h *Handler) DiscoverTV(w http.ResponseWriter, r *http.Request) {
	params := url.Values{}
	for _, k := range []string{"page", "with_genres", "sort_by", "first_air_date_year", "with_watch_providers", "watch_region"} {
		if v := r.URL.Query().Get(k); v != "" {
			params.Set(k, v)
		}
	}
	if params.Get("page") == "" {
		params.Set("page", "1")
	}
	key := "disc_tv:" + params.Encode()
	h.cachedTMDB(w, key, ttlTrending, "/discover/tv", params)
}

func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("query")
	if query == "" {
		http.Error(w, `{"error":"query parameter required"}`, http.StatusBadRequest)
		return
	}
	page := queryInt(r, "page", 1)
	key := fmt.Sprintf("search:%s:%d", query, page)
	params := url.Values{"query": {query}, "page": {strconv.Itoa(page)}}
	h.cachedTMDB(w, key, ttlTrending, "/search/multi", params)
}

func (h *Handler) MovieDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	key := "movie:" + id
	params := url.Values{"append_to_response": {"videos"}}
	h.cachedTMDB(w, key, ttlDetails, fmt.Sprintf("/movie/%s", id), params)
}

func (h *Handler) TVDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	key := "tv:" + id
	params := url.Values{"append_to_response": {"videos,external_ids"}}
	h.cachedTMDB(w, key, ttlDetails, fmt.Sprintf("/tv/%s", id), params)
}

func (h *Handler) MovieRecommendations(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	page := queryInt(r, "page", 1)
	key := fmt.Sprintf("movie_rec:%s:%d", id, page)
	params := url.Values{"page": {strconv.Itoa(page)}}
	h.cachedTMDB(w, key, ttlRecommendation, fmt.Sprintf("/movie/%s/recommendations", id), params)
}

func (h *Handler) TVRecommendations(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	page := queryInt(r, "page", 1)
	key := fmt.Sprintf("tv_rec:%s:%d", id, page)
	params := url.Values{"page": {strconv.Itoa(page)}}
	h.cachedTMDB(w, key, ttlRecommendation, fmt.Sprintf("/tv/%s/recommendations", id), params)
}

func (h *Handler) SimilarMovies(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	page := queryInt(r, "page", 1)
	key := fmt.Sprintf("movie_sim:%s:%d", id, page)
	params := url.Values{"page": {strconv.Itoa(page)}}
	h.cachedTMDB(w, key, ttlRecommendation, fmt.Sprintf("/movie/%s/similar", id), params)
}

func (h *Handler) SimilarTV(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	page := queryInt(r, "page", 1)
	key := fmt.Sprintf("tv_sim:%s:%d", id, page)
	params := url.Values{"page": {strconv.Itoa(page)}}
	h.cachedTMDB(w, key, ttlRecommendation, fmt.Sprintf("/tv/%s/similar", id), params)
}

func (h *Handler) MovieGenres(w http.ResponseWriter, r *http.Request) {
	h.cachedTMDB(w, "genres_movie", ttlGenresProvider, "/genre/movie/list", nil)
}

func (h *Handler) TVGenres(w http.ResponseWriter, r *http.Request) {
	h.cachedTMDB(w, "genres_tv", ttlGenresProvider, "/genre/tv/list", nil)
}

func (h *Handler) MovieWatchProviders(w http.ResponseWriter, r *http.Request) {
	region := r.URL.Query().Get("region")
	if region == "" {
		region = "US"
	}
	key := "providers_movie:" + region
	params := url.Values{"watch_region": {region}}
	h.cachedTMDB(w, key, ttlGenresProvider, "/watch/providers/movie", params)
}

// ─── Trakt Endpoints ────────────────────────────────────

func (h *Handler) TraktTrending(w http.ResponseWriter, r *http.Request) {
	if h.trakt == nil {
		http.Error(w, `{"error":"trakt not configured"}`, http.StatusServiceUnavailable)
		return
	}
	typ := r.URL.Query().Get("type")
	if typ == "" {
		typ = "movies"
	}
	page := queryInt(r, "page", 1)
	key := fmt.Sprintf("trakt_trend:%s:%d", typ, page)
	params := url.Values{"page": {strconv.Itoa(page)}, "limit": {"20"}, "extended": {"full"}}
	h.cachedTrakt(w, key, ttlTrakt, fmt.Sprintf("/%s/trending", typ), params)
}

func (h *Handler) TraktPopular(w http.ResponseWriter, r *http.Request) {
	if h.trakt == nil {
		http.Error(w, `{"error":"trakt not configured"}`, http.StatusServiceUnavailable)
		return
	}
	typ := r.URL.Query().Get("type")
	if typ == "" {
		typ = "movies"
	}
	page := queryInt(r, "page", 1)
	key := fmt.Sprintf("trakt_pop:%s:%d", typ, page)
	params := url.Values{"page": {strconv.Itoa(page)}, "limit": {"20"}, "extended": {"full"}}
	h.cachedTrakt(w, key, ttlTrakt, fmt.Sprintf("/%s/popular", typ), params)
}

func (h *Handler) TraktPopularLists(w http.ResponseWriter, r *http.Request) {
	if h.trakt == nil {
		http.Error(w, `{"error":"trakt not configured"}`, http.StatusServiceUnavailable)
		return
	}
	page := queryInt(r, "page", 1)
	key := fmt.Sprintf("trakt_lists:%d", page)
	params := url.Values{"page": {strconv.Itoa(page)}, "limit": {"20"}}
	h.cachedTrakt(w, key, ttlTrakt, "/lists/popular", params)
}

func (h *Handler) TraktListItems(w http.ResponseWriter, r *http.Request) {
	if h.trakt == nil {
		http.Error(w, `{"error":"trakt not configured"}`, http.StatusServiceUnavailable)
		return
	}
	user := chi.URLParam(r, "user")
	slug := chi.URLParam(r, "slug")
	key := fmt.Sprintf("trakt_list:%s/%s", user, slug)
	params := url.Values{"extended": {"full"}}
	h.cachedTrakt(w, key, ttlTrakt, fmt.Sprintf("/users/%s/lists/%s/items", user, slug), params)
}

func (h *Handler) TraktCalendar(w http.ResponseWriter, r *http.Request) {
	if h.trakt == nil {
		http.Error(w, `{"error":"trakt not configured"}`, http.StatusServiceUnavailable)
		return
	}
	days := queryInt(r, "days", 14)
	today := time.Now().Format("2006-01-02")
	key := fmt.Sprintf("trakt_cal:%s:%d", today, days)
	h.cachedTrakt(w, key, ttlTrakt, fmt.Sprintf("/calendars/all/shows/%s/%d", today, days), nil)
}

func (h *Handler) TraktRecommendations(w http.ResponseWriter, r *http.Request) {
	if h.trakt == nil {
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
	h.cachedTrakt(w, key, ttlTrakt, fmt.Sprintf("/recommendations/%s", apiType), params)
}
