package request

import (
	"strconv"
	"time"

	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

// Artwork for the admin approval queue.
//
// request_log is a decision record, not a metadata copy: it stores what was
// asked for, never how it looked. Posters are therefore resolved at read time
// and cached in memory, which also means requests filed before the queue showed
// artwork are not permanently poster-less.

const (
	// posterCacheTTL is long because TMDB artwork changes rarely and the queue
	// is admin-only: a re-opened queue should cost one DB query, not a fan-out.
	posterCacheTTL = 6 * time.Hour
	// posterLookupConcurrency bounds simultaneous TMDB connections when a
	// backlogged queue needs many rows resolved at once.
	posterLookupConcurrency = 4
)

// posterLookupBudget caps what a slow or unreachable TMDB can cost one queue
// load. Lookups that overrun it keep running and still populate the cache, so
// the artwork this load gives up on is already waiting for the next one. A var
// so tests don't have to spend it.
var posterLookupBudget = 3 * time.Second

// posterLookup is the slice of TMDB the queue needs. An interface so tests can
// drive artwork — and TMDB outages — without a live client.
type posterLookup interface {
	GetMovieDetails(tmdbID int) (*tmdb.MovieDetails, error)
	GetTVDetails(tmdbID int) (*tmdb.TVDetails, error)
}

// posterKey identifies one piece of artwork. Movie and TV ids share TMDB's
// number space only by accident, so the media type is part of the identity.
type posterKey struct {
	mediaType string
	tmdbID    int
}

func (k posterKey) cacheKey() string {
	return "poster:" + k.mediaType + ":" + strconv.Itoa(k.tmdbID)
}

type posterResult struct {
	key      posterKey
	path     string
	resolved bool
}

// posterKeyFor reports the artwork a pending row needs, if any.
//
// Books are deliberately excluded. A pending book is usually not in the library
// yet, so its cover would cost a Chaptarr metadata lookup per row — seconds of
// arr traffic to return, in this fork, either nothing or an arr-origin
// /MediaCoverProxy path no client may dereference. Those rows carry a book
// placeholder instead.
func posterKeyFor(item PendingRequest) (posterKey, bool) {
	if item.TmdbID <= 0 {
		return posterKey{}, false
	}
	switch item.MediaType {
	case "movie", "tv":
		return posterKey{mediaType: item.MediaType, tmdbID: item.TmdbID}, true
	default:
		return posterKey{}, false
	}
}

// posterSource resolves the metadata client used for queue artwork. TMDB is
// configured at runtime, so this resolves per call; nil means "no artwork this
// load", never an error.
func (s *Service) posterSource() posterLookup {
	if s.posterLookupOverride != nil {
		return s.posterLookupOverride
	}
	client := s.bridge.TMDB()
	if client == nil {
		return nil
	}
	return client
}

// attachPosterPaths fills PosterPath on the rows that can carry one. Artwork is
// decoration: an unconfigured TMDB, a dead id, or a slow API leaves a row
// poster-less rather than failing the queue.
func (s *Service) attachPosterPaths(items []PendingRequest) {
	if len(items) == 0 {
		return
	}
	seen := make(map[posterKey]bool, len(items))
	resolved := make(map[posterKey]string, len(items))
	var misses []posterKey
	for _, item := range items {
		key, ok := posterKeyFor(item)
		if !ok || seen[key] {
			continue // two users can have the same title pending
		}
		seen[key] = true
		if path, hit := s.cachedPosterPath(key); hit {
			resolved[key] = path
			continue
		}
		misses = append(misses, key)
	}
	for key, path := range s.fetchPosterPaths(misses) {
		resolved[key] = path
	}
	for i := range items {
		if key, ok := posterKeyFor(items[i]); ok {
			items[i].PosterPath = resolved[key]
		}
	}
}

// fetchPosterPaths resolves uncached artwork, bounded by posterLookupBudget.
func (s *Service) fetchPosterPaths(keys []posterKey) map[posterKey]string {
	out := make(map[posterKey]string, len(keys))
	if len(keys) == 0 {
		return out
	}
	client := s.posterSource()
	if client == nil {
		return out
	}
	// Buffered for every key so a lookup that lands after the budget expires
	// still finishes and caches instead of blocking on an abandoned read.
	results := make(chan posterResult, len(keys))
	slots := make(chan struct{}, posterLookupConcurrency)
	for _, key := range keys {
		go func(key posterKey) {
			slots <- struct{}{}
			defer func() { <-slots }()
			path, err := lookupPosterPath(client, key)
			if err != nil {
				results <- posterResult{key: key}
				return
			}
			s.cachePosterPath(key, path)
			results <- posterResult{key: key, path: path, resolved: true}
		}(key)
	}
	budget := time.After(posterLookupBudget)
	for range keys {
		select {
		case result := <-results:
			if result.resolved {
				out[result.key] = result.path
			}
		case <-budget:
			return out
		}
	}
	return out
}

func lookupPosterPath(client posterLookup, key posterKey) (string, error) {
	if key.mediaType == "tv" {
		tv, err := client.GetTVDetails(key.tmdbID)
		if err != nil {
			return "", err
		}
		return tv.PosterPath, nil
	}
	movie, err := client.GetMovieDetails(key.tmdbID)
	if err != nil {
		return "", err
	}
	return movie.PosterPath, nil
}

// cachedPosterPath reports a cached path and whether one was cached at all — a
// title TMDB has no artwork for caches as an empty path, and re-asking every
// load would be the same answer at network cost.
func (s *Service) cachedPosterPath(key posterKey) (string, bool) {
	if s.posterCache == nil {
		return "", false
	}
	data, ok := s.posterCache.Get(key.cacheKey())
	if !ok {
		return "", false
	}
	return string(data), true
}

func (s *Service) cachePosterPath(key posterKey, path string) {
	if s.posterCache == nil {
		return
	}
	s.posterCache.Set(key.cacheKey(), []byte(path), posterCacheTTL)
}
