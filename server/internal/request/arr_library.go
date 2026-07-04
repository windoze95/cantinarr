package request

import (
	"encoding/json"
	"time"

	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// arrLibraryCacheTTL bounds how long a movie/series availability digest is
// served from cache before a fresh library fetch. Same tradeoff as
// bookLibraryCacheTTL: short enough that changes made directly in the arr show
// up quickly, long enough that a request-history read doesn't refetch the
// whole library every time.
const arrLibraryCacheTTL = 120 * time.Second

// movieAvailability is the slice of Radarr state a request-side status needs:
// whether a file is on disk and whether Radarr is still looking for one.
type movieAvailability struct {
	HasFile   bool `json:"has_file"`
	Monitored bool `json:"monitored"`
}

// seriesAvailability is the slice of Sonarr state a request-side status needs,
// from season-statistics totals (EpisodeTotals semantics: every known episode
// across real seasons, monitored or not — see Series.EpisodeTotals for why
// monitored-only counts must not be used here).
type seriesAvailability struct {
	Files     int  `json:"files"`
	Total     int  `json:"total"`
	Monitored bool `json:"monitored"`
}

// movieAvailabilityDigest returns tmdbID → availability for the user's Radarr
// library, cached per resolved instance for arrLibraryCacheTTL. ok is false
// when the user has no Radarr source or the fetch failed — callers should keep
// whatever status they already have rather than guessing.
func (s *Service) movieAvailabilityDigest(userID int64) (map[int]movieAvailability, bool) {
	client, instanceID := s.getRadarrWithID(userID)
	if client == nil {
		return nil, false
	}

	cacheKey := "movie-availability:" + instanceID
	if s.libraryCache != nil {
		if data, ok := s.libraryCache.Get(cacheKey); ok {
			var digest map[int]movieAvailability
			if err := json.Unmarshal(data, &digest); err == nil {
				return digest, true
			}
		}
	}

	movies, err := client.GetMovies()
	if err != nil {
		return nil, false
	}
	digest := make(map[int]movieAvailability, len(movies))
	for _, m := range movies {
		if m.TmdbID == 0 {
			continue
		}
		digest[m.TmdbID] = movieAvailability{HasFile: m.HasFile, Monitored: m.Monitored}
	}

	if s.libraryCache != nil {
		if data, err := json.Marshal(digest); err == nil {
			s.libraryCache.Set(cacheKey, data, arrLibraryCacheTTL)
		}
	}
	return digest, true
}

// seriesAvailabilityDigest returns tvdbID → availability for the user's Sonarr
// library, cached per resolved instance for arrLibraryCacheTTL. ok is false
// when the user has no Sonarr source or the fetch failed.
func (s *Service) seriesAvailabilityDigest(userID int64) (map[int]seriesAvailability, bool) {
	client, instanceID := s.getSonarrWithID(userID)
	if client == nil {
		return nil, false
	}

	cacheKey := "series-availability:" + instanceID
	if s.libraryCache != nil {
		if data, ok := s.libraryCache.Get(cacheKey); ok {
			var digest map[int]seriesAvailability
			if err := json.Unmarshal(data, &digest); err == nil {
				return digest, true
			}
		}
	}

	series, err := client.GetAllSeries()
	if err != nil {
		return nil, false
	}
	digest := make(map[int]seriesAvailability, len(series))
	for i := range series {
		sr := &series[i]
		if sr.TvdbID == 0 {
			continue
		}
		files, total := sr.EpisodeTotals()
		digest[sr.TvdbID] = seriesAvailability{Files: files, Total: total, Monitored: sr.Monitored}
	}

	if s.libraryCache != nil {
		if data, err := json.Marshal(digest); err == nil {
			s.libraryCache.Set(cacheKey, data, arrLibraryCacheTTL)
		}
	}
	return digest, true
}

// movieAvailabilityStatus maps a digest entry onto the request status
// vocabulary, mirroring getMovieStatus minus the queue check (a digest has no
// queue, so an actively-downloading title reads requested until its file
// lands).
func movieAvailabilityStatus(a movieAvailability, found bool) string {
	switch {
	case !found:
		return StatusUnavailable
	case a.HasFile:
		return StatusAvailable
	case a.Monitored:
		return StatusRequested
	default:
		return StatusUnavailable
	}
}

// seriesAvailabilityStatus maps a digest entry onto the request status
// vocabulary via the same completion rules getTVStatus uses on its
// season-statistics fallback path.
func seriesAvailabilityStatus(a seriesAvailability, found bool) string {
	if !found {
		return StatusUnavailable
	}
	status, _ := statusFromCompletion(sonarr.Completion{Files: a.Files, Aired: a.Total}, a.Monitored)
	return status
}

// getRadarrWithID resolves the same Radarr client as getRadarr but also
// returns the instance id it resolved to, for cache keying.
func (s *Service) getRadarrWithID(userID int64) (*radarr.Client, string) {
	if s.registry == nil {
		return nil, ""
	}
	if client, id, err := s.registry.GetUserDefaultRadarrClient(userID); err == nil && client != nil {
		return client, id
	}
	return nil, ""
}

// getSonarrWithID resolves the same Sonarr client as getSonarr but also
// returns the instance id it resolved to, for cache keying.
func (s *Service) getSonarrWithID(userID int64) (*sonarr.Client, string) {
	if s.registry == nil {
		return nil, ""
	}
	if client, id, err := s.registry.GetUserDefaultSonarrClient(userID); err == nil && client != nil {
		return client, id
	}
	return nil, ""
}
