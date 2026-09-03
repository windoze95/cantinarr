package discover

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A discover query is the one place a client composes TMDB parameters itself,
// so it is allowlisted and validated here rather than passed through: an
// unknown key is dropped, and a value TMDB would refuse is answered 400 by
// this server instead of surfacing as a 502 indistinguishable from an outage.

// discoverSpec is what one media type's discover route accepts.
type discoverSpec struct {
	path      string
	keyPrefix string
	// keys is the allowlist. `language` is never on it: the TMDB client welds
	// language=en-US onto every request, so a second value would arrive as a
	// duplicate key with undefined precedence.
	keys       []string
	sortFields []string
	dateKeys   []string
}

var (
	movieDiscover = discoverSpec{
		path:      "/discover/movie",
		keyPrefix: "disc_movies:",
		keys: []string{
			"page", "sort_by", "with_genres", "primary_release_year",
			"primary_release_date.gte", "primary_release_date.lte",
			"vote_average.gte", "vote_count.gte", "with_original_language",
			"with_watch_providers", "watch_region",
		},
		sortFields: []string{
			"original_title", "popularity", "primary_release_date", "revenue",
			"title", "vote_average", "vote_count",
		},
		dateKeys: []string{"primary_release_date.gte", "primary_release_date.lte"},
	}
	tvDiscover = discoverSpec{
		path:      "/discover/tv",
		keyPrefix: "disc_tv:",
		keys: []string{
			"page", "sort_by", "with_genres", "first_air_date_year",
			"first_air_date.gte", "first_air_date.lte",
			"vote_average.gte", "vote_count.gte", "with_original_language",
			"with_watch_providers", "watch_region",
		},
		sortFields: []string{
			"first_air_date", "name", "original_name", "popularity",
			"vote_average", "vote_count",
		},
		dateKeys: []string{"first_air_date.gte", "first_air_date.lte"},
	}
)

// ratingSortMinVotes floors a rating sort the caller did not floor
// themselves: ordered by vote_average alone, TMDB's catalogue is a wall of
// one-vote 10.0s.
const ratingSortMinVotes = "200"

var errInvalidSort = errors.New("invalid sort_by")

// discoverQuery builds the upstream query from the allowlisted parameters.
// It also reports whether the caller named a language of their own: that
// query is an explicit ask, served the way a search is, never thinned by the
// admin's English-only preference.
func discoverQuery(r *http.Request, spec discoverSpec, englishOnly bool) (params url.Values, explicitLanguage bool, err error) {
	params = url.Values{}
	for _, k := range spec.keys {
		if v := r.URL.Query().Get(k); v != "" {
			params.Set(k, v)
		}
	}
	params.Set("page", strconv.Itoa(queryPage(r)))

	if sort := params.Get("sort_by"); sort != "" {
		if !validSortBy(sort, spec.sortFields) {
			return nil, false, errInvalidSort
		}
		if strings.HasPrefix(sort, "vote_average.") && params.Get("vote_count.gte") == "" {
			params.Set("vote_count.gte", ratingSortMinVotes)
		}
	}
	for _, k := range spec.dateKeys {
		if v := params.Get(k); v != "" {
			if _, err := time.Parse("2006-01-02", v); err != nil {
				return nil, false, fmt.Errorf("invalid %s: want YYYY-MM-DD", k)
			}
		}
	}
	// TMDB applies the admin's language preference itself on a discover
	// query, so the page arrives full with an exact page count instead of
	// thinned after the fact. The list feeds have no such parameter and keep
	// the post-filter. A language the caller asked for is kept as given.
	explicitLanguage = params.Get("with_original_language") != ""
	if englishOnly && !explicitLanguage {
		params.Set("with_original_language", "en")
	}
	return params, explicitLanguage, nil
}

// validSortBy accepts `<field>.asc` or `<field>.desc` over the media type's
// sortable fields.
func validSortBy(sort string, fields []string) bool {
	field, direction, ok := strings.Cut(sort, ".")
	if !ok || (direction != "asc" && direction != "desc") {
		return false
	}
	return slices.Contains(fields, field)
}

// discover serves a filterable discover route.
func (h *Handler) discover(w http.ResponseWriter, r *http.Request, spec discoverSpec) {
	_, englishOnly := h.discoveryPrefs()
	params, explicitLanguage, err := discoverQuery(r, spec, englishOnly)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}
	key := spec.keyPrefix + params.Encode()
	if explicitLanguage {
		// Asking for Korean films is as explicit as searching for one: the
		// page comes back as TMDB sent it, whatever the admin's preference.
		h.cachedTMDB(w, key, ttlTrending, spec.path, params)
		return
	}
	h.cachedTMDBFeed(w, key, ttlTrending, spec.path, params)
}
