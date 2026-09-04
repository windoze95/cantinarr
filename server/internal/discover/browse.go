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

	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
	"github.com/windoze95/cantinarr-server/internal/serversettings"
)

// A discover query is the one place a client composes TMDB parameters itself,
// so it is allowlisted and validated here rather than passed through: an
// unknown key is dropped, and a value TMDB would refuse is answered 400 by
// this server instead of surfacing as a 502 indistinguishable from an outage.

// discoverSpec is what one media type's discover route accepts.
type discoverSpec struct {
	mediaType string
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
		mediaType: contentpolicy.MediaMovie,
		path:      "/discover/movie",
		keyPrefix: "disc_movies:",
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
	}
	tvDiscover = discoverSpec{
		mediaType: contentpolicy.MediaTV,
		path:      "/discover/tv",
		keyPrefix: "disc_tv:",
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
	}
)

// ratingSortMinVotes floors a rating sort the caller did not floor
// themselves: ordered by vote_average alone, TMDB's catalogue is a wall of
// one-vote 10.0s.
const ratingSortMinVotes = "200"

var errInvalidSort = errors.New("invalid sort_by")

// MaxTMDBPage is the last page TMDB serves; it reports total_pages above it
// and refuses requests past it.
const MaxTMDBPage = 500

// ClampPage folds any page number into TMDB's 1..MaxTMDBPage, so a caller
// running off either end asks for a real page instead of earning an
// upstream error.
func ClampPage(page int) int {
	if page < 1 {
		return 1
	}
	if page > MaxTMDBPage {
		return MaxTMDBPage
	}
	return page
}

// EnglishOnly reads the admin's language preference, with the shipped
// default when no settings service is wired in (the same fallback the
// handler uses).
func EnglishOnly(prefs DiscoveryPrefs) bool {
	if prefs == nil {
		return serversettings.DefaultDiscoveryEnglishOnly
	}
	return prefs.Get().DiscoveryEnglishOnly
}

func browseSpec(mediaType string) (discoverSpec, bool) {
	switch mediaType {
	case "movie":
		return movieDiscover, true
	case "tv":
		return tvDiscover, true
	}
	return discoverSpec{}, false
}

// BuildBrowseQuery is the discover query for a caller that is not an HTTP
// request (the assistant's browse tool): the same allowlist, sort and date
// validation, rating floor, and English-only handling the browse routes
// apply, keyed by media type. It reports whether the caller named a language
// of its own, which the routes serve unfiltered. A kids account's policy
// (nil for everyone else) is pushed upstream so its pages arrive full.
func BuildBrowseQuery(mediaType string, in url.Values, page int, englishOnly bool, policy *contentpolicy.Policy) (params url.Values, explicitLanguage bool, err error) {
	spec, ok := browseSpec(mediaType)
	if !ok {
		return nil, false, fmt.Errorf("unknown media type %q", mediaType)
	}
	return buildDiscoverQuery(in, page, spec, englishOnly, policy)
}

// discoverQuery builds the upstream query from a request's allowlisted
// parameters; see buildDiscoverQuery.
func discoverQuery(r *http.Request, spec discoverSpec, englishOnly bool, policy *contentpolicy.Policy) (url.Values, bool, error) {
	return buildDiscoverQuery(r.URL.Query(), queryPage(r), spec, englishOnly, policy)
}

// buildDiscoverQuery builds the upstream query from the allowlisted
// parameters in `in`. It also reports whether the caller named a language of
// its own: that query is an explicit ask, served the way a search is, never
// thinned by the admin's English-only preference.
func buildDiscoverQuery(in url.Values, page int, spec discoverSpec, englishOnly bool, policy *contentpolicy.Policy) (params url.Values, explicitLanguage bool, err error) {
	params = url.Values{}
	for _, k := range spec.keys {
		if v := in.Get(k); v != "" {
			params.Set(k, v)
		}
	}
	params.Set("page", strconv.Itoa(ClampPage(page)))

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
	applyPolicyToQuery(params, spec.mediaType, policy)
	return params, explicitLanguage, nil
}

// applyPolicyToQuery pushes a kids account's limits into a discover query
// so the page arrives full instead of thinned afterwards: adult titles out,
// hidden genres out, and — for movies, where TMDB has a certification
// filter — the rating cap, but only while unrated titles are hidden, since
// TMDB drops every uncertified title the moment a certification filter is
// set. The per-item pass still runs on the way out; this is the page
// fullness, not the safeguard. The params are part of the cache key, so a
// child's page never collides with an adult's.
func applyPolicyToQuery(params url.Values, mediaType string, policy *contentpolicy.Policy) {
	if policy == nil {
		return
	}
	params.Set("include_adult", "false")
	var blocked []int
	switch mediaType {
	case contentpolicy.MediaMovie:
		blocked = policy.BlockedMovieGenres
	case contentpolicy.MediaTV:
		blocked = policy.BlockedTVGenres
	}
	if len(blocked) > 0 {
		ids := make([]string, 0, len(blocked))
		for _, id := range blocked {
			ids = append(ids, strconv.Itoa(id))
		}
		params.Set("without_genres", strings.Join(ids, ","))
	}
	if mediaType == contentpolicy.MediaMovie && policy.BlockUnrated && policy.MaxMovieRating != "" && policy.RatingRegion != "" {
		params.Set("certification_country", policy.RatingRegion)
		params.Set("certification.lte", policy.MaxMovieRating)
	}
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
	policy, err := h.viewerPolicy(r)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	params, explicitLanguage, err := discoverQuery(r, spec, englishOnly, policy)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}
	key := spec.keyPrefix + params.Encode()
	shape := listShape(spec.mediaType)
	if explicitLanguage {
		// Asking for Korean films is as explicit as searching for one: the
		// page comes back as TMDB sent it, whatever the admin's preference.
		h.cachedTMDB(w, r, key, ttlTrending, spec.path, params, shape)
		return
	}
	h.cachedTMDBFeed(w, r, key, ttlTrending, spec.path, params, shape)
}
