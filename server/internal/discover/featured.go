package discover

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/serversettings"
)

// featuredLimit is how many titles a headline row asks for.
const featuredLimit = 20

// maxFeaturedPages bounds the upstream paging done to refill a row the
// English-only filter thinned out. Three TMDB pages is 60 candidate titles for
// a 20-slot row; stopping there keeps one cold render at three upstream calls
// instead of an open-ended walk when a feed happens to be mostly non-English.
const maxFeaturedPages = 3

// traktFeaturedFetch over-asks Trakt because entries are dropped before they
// reach the row: one without a TMDB id has nothing to open or request, and the
// English-only filter removes more.
const traktFeaturedFetch = 40

var (
	errTMDBNotConfigured  = errors.New("TMDB is not configured")
	errTraktNotConfigured = errors.New("trakt not configured")
)

// featuredPage is the headline-row envelope. It mirrors the TMDB list shape so
// one client parser handles every source, plus a `source` field so the row can
// name what it is actually showing.
type featuredPage struct {
	Source       string            `json:"source"`
	Page         int               `json:"page"`
	Results      []json.RawMessage `json:"results"`
	TotalPages   int               `json:"total_pages"`
	TotalResults int               `json:"total_results"`
}

// FeaturedTV serves the configured headline TV feed.
func (h *Handler) FeaturedTV(w http.ResponseWriter, r *http.Request) {
	h.featured(w, "tv")
}

// FeaturedMovies serves the configured headline movie feed.
func (h *Handler) FeaturedMovies(w http.ResponseWriter, r *http.Request) {
	h.featured(w, "movie")
}

func (h *Handler) featured(w http.ResponseWriter, mediaType string) {
	source, englishOnly := h.discoveryPrefs()

	// Trakt is optional. Falling back keeps the landing screen populated
	// rather than blanking a headline row because a credential went missing,
	// and the response still reports the source that actually answered.
	if source == serversettings.DiscoverySourceTraktTrending && h.creds.Trakt() == nil {
		source = serversettings.DiscoverySourceTMDBTrending
	}

	key := fmt.Sprintf("featured:%s:%s:%t", mediaType, source, englishOnly)
	if data, ok := h.cache.Get(key); ok {
		writeJSON(w, data)
		return
	}

	var (
		results []json.RawMessage
		err     error
	)
	switch source {
	case serversettings.DiscoverySourceTraktTrending:
		results, err = h.traktFeatured(mediaType, englishOnly)
		if err != nil {
			// A third-party API that is simply down is not the admin's
			// configuration problem, and the row it backs is the landing
			// screen — so an outage falls back exactly like a missing
			// credential does rather than erroring the row. The payload names
			// TMDB as the source, which is how the row retitles itself and
			// how anyone looking can tell the fallback happened without
			// reading logs.
			//
			// Cached under the Trakt key on purpose: a sustained outage then
			// costs one upstream attempt per TTL instead of one per request,
			// and recovery is picked up on the next miss.
			log.Printf("discover: trakt featured %s failed, serving TMDB weekly trending instead: %v", mediaType, err)
			source = serversettings.DiscoverySourceTMDBTrending
			results, err = h.tmdbFeatured(fmt.Sprintf("/trending/%s/week", mediaType), englishOnly)
		}
	case serversettings.DiscoverySourceTMDBPopular:
		results, err = h.tmdbFeatured(fmt.Sprintf("/%s/popular", mediaType), englishOnly)
	default:
		results, err = h.tmdbFeatured(fmt.Sprintf("/trending/%s/week", mediaType), englishOnly)
	}
	if err != nil {
		if errors.Is(err, errTMDBNotConfigured) || errors.Is(err, errTraktNotConfigured) {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusServiceUnavailable)
			return
		}
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadGateway)
		return
	}

	payload, err := json.Marshal(featuredPage{
		Source:       source,
		Page:         1,
		Results:      results,
		TotalPages:   1,
		TotalResults: len(results),
	})
	if err != nil {
		http.Error(w, `{"error":"encode featured feed"}`, http.StatusInternalServerError)
		return
	}
	h.cache.Set(key, payload, ttlTrending)
	writeJSON(w, payload)
}

// tmdbFeatured collects up to featuredLimit entries from a TMDB list endpoint,
// walking further pages only when the English-only filter has thinned the row.
// Entries pass through verbatim — the row keeps every field TMDB sent.
func (h *Handler) tmdbFeatured(path string, englishOnly bool) ([]json.RawMessage, error) {
	client := h.creds.TMDB()
	if client == nil {
		return nil, errTMDBNotConfigured
	}

	pages := 1
	if englishOnly {
		pages = maxFeaturedPages
	}

	kept := make([]json.RawMessage, 0, featuredLimit)
	for page := 1; page <= pages && len(kept) < featuredLimit; page++ {
		data, err := client.DoGetRaw(path, url.Values{"page": {strconv.Itoa(page)}})
		if err != nil {
			// A first-page failure has nothing to show; a later one still
			// leaves a usable, if shorter, row.
			if page == 1 {
				return nil, err
			}
			break
		}
		results, err := decodeResults(data)
		if err != nil {
			if page == 1 {
				return nil, err
			}
			break
		}
		if len(results) == 0 {
			break
		}
		for _, item := range results {
			if englishOnly && !isEnglishOriginal(item) {
				continue
			}
			kept = append(kept, item)
			if len(kept) == featuredLimit {
				break
			}
		}
	}
	return kept, nil
}

// traktFeatured maps Trakt's trending feed onto the TMDB list shape so the row
// renders identically whichever source is configured.
func (h *Handler) traktFeatured(mediaType string, englishOnly bool) ([]json.RawMessage, error) {
	client := h.creds.Trakt()
	if client == nil {
		return nil, errTraktNotConfigured
	}

	traktType := "shows"
	if mediaType == "movie" {
		traktType = "movies"
	}
	params := url.Values{
		"page":     {"1"},
		"limit":    {strconv.Itoa(traktFeaturedFetch)},
		"extended": {"full"},
	}
	data, err := client.DoGetRaw(fmt.Sprintf("/%s/trending", traktType), params)
	if err != nil {
		return nil, err
	}

	var entries []traktTrendingEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("decode trakt trending: %w", err)
	}

	kept := make([]json.RawMessage, 0, featuredLimit)
	for _, entry := range entries {
		media := entry.Show
		if mediaType == "movie" {
			media = entry.Movie
		}
		// Without a TMDB id there is no detail page to open and no way to
		// request the title, so the card would be a dead end.
		if media == nil || media.IDs.TMDB == 0 {
			continue
		}
		if englishOnly && !isEnglishLanguage(media.Language) {
			continue
		}
		item, err := json.Marshal(media.toFeaturedItem(mediaType))
		if err != nil {
			continue
		}
		kept = append(kept, item)
		if len(kept) == featuredLimit {
			break
		}
	}
	return kept, nil
}

// traktTrendingEntry is one row of Trakt's trending feed; exactly one of the
// two media fields is populated, depending on the endpoint.
type traktTrendingEntry struct {
	Show  *traktMedia `json:"show"`
	Movie *traktMedia `json:"movie"`
}

// traktMedia is the subset of an extended=full Trakt media object the rows use.
type traktMedia struct {
	Title      string  `json:"title"`
	Year       int     `json:"year"`
	Overview   string  `json:"overview"`
	Rating     float64 `json:"rating"`
	Language   string  `json:"language"`
	FirstAired string  `json:"first_aired"`
	Released   string  `json:"released"`
	IDs        struct {
		TMDB int `json:"tmdb"`
	} `json:"ids"`
	Images struct {
		Poster []string `json:"poster"`
		Fanart []string `json:"fanart"`
	} `json:"images"`
}

// featuredItem is a TMDB-shaped card. Media-specific fields are omitted when
// empty so a TV entry carries `name`/`first_air_date` and a movie entry carries
// `title`/`release_date`, exactly as the client's parsers expect.
type featuredItem struct {
	ID               int     `json:"id"`
	Name             string  `json:"name,omitempty"`
	Title            string  `json:"title,omitempty"`
	Overview         string  `json:"overview,omitempty"`
	PosterPath       string  `json:"poster_path,omitempty"`
	BackdropPath     string  `json:"backdrop_path,omitempty"`
	FirstAirDate     string  `json:"first_air_date,omitempty"`
	ReleaseDate      string  `json:"release_date,omitempty"`
	VoteAverage      float64 `json:"vote_average,omitempty"`
	OriginalLanguage string  `json:"original_language,omitempty"`
}

func (m *traktMedia) toFeaturedItem(mediaType string) featuredItem {
	item := featuredItem{
		ID:               m.IDs.TMDB,
		Overview:         m.Overview,
		PosterPath:       traktImageURL(m.Images.Poster),
		BackdropPath:     traktImageURL(m.Images.Fanart),
		VoteAverage:      m.Rating,
		OriginalLanguage: strings.TrimSpace(m.Language),
	}
	if mediaType == "movie" {
		item.Title = m.Title
		item.ReleaseDate = m.releaseDate(m.Released)
	} else {
		item.Name = m.Title
		item.FirstAirDate = m.releaseDate(m.FirstAired)
	}
	return item
}

// releaseDate prefers Trakt's own date, trimmed to the YYYY-MM-DD the client
// parses (first_aired arrives as a full timestamp). It falls back to the bare
// year rather than inventing a month and day.
func (m *traktMedia) releaseDate(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) >= 10 {
		return trimmed[:10]
	}
	if trimmed != "" {
		return trimmed
	}
	if m.Year > 0 {
		return strconv.Itoa(m.Year)
	}
	return ""
}

// traktImageURL returns the first image as an absolute URL. Trakt serves these
// host-relative ("media.trakt.tv/images/...", walter-r2 before July 2026), and
// they are public CDN assets — though unlike TMDB's they send no CORS headers,
// which is why the web client loads them through the /api/trakt/images relay.
func traktImageURL(images []string) string {
	for _, raw := range images {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
			return trimmed
		}
		return "https://" + trimmed
	}
	return ""
}

// decodeResults pulls the entries out of a TMDB list payload, leaving each one
// as raw JSON so nothing is lost re-encoding it.
func decodeResults(data []byte) ([]json.RawMessage, error) {
	var envelope struct {
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode tmdb results: %w", err)
	}
	return envelope.Results, nil
}
