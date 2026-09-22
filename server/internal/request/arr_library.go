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
// AddedAt is when Radarr imported the file (nil without one); only the
// Seerr-compatible ledger reads it, as the "media added" date.
type movieAvailability struct {
	HasFile   bool       `json:"has_file"`
	Monitored bool       `json:"monitored"`
	AddedAt   *time.Time `json:"added_at,omitempty"`
}

// seriesAvailability is the slice of Sonarr state a request-side status needs,
// from season-statistics totals (EpisodeTotals semantics: every known episode
// across real seasons, monitored or not — see Series.EpisodeTotals for why
// monitored-only counts must not be used here). Seasons carries the same
// counts per real season, for readers that answer season by season (the
// Seerr-compatible ledger); specials stay out, as in EpisodeTotals.
type seriesAvailability struct {
	Files     int                        `json:"files"`
	Total     int                        `json:"total"`
	Monitored bool                       `json:"monitored"`
	Seasons   map[int]seasonAvailability `json:"seasons,omitempty"`
}

// seasonAvailability is one season's slice of seriesAvailability.
type seasonAvailability struct {
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
	return s.movieDigestFor(client, instanceID)
}

// movieAvailabilityDigestForInstance is movieAvailabilityDigest with the
// resolution hoisted out: it reads one specific library's digest. The caller
// authorizes the selection — this is reached with ids from the user's own
// history rows or their granted set, never a raw client-supplied id.
func (s *Service) movieAvailabilityDigestForInstance(instanceID string) (map[int]movieAvailability, bool) {
	if s.registry == nil || instanceID == "" {
		return nil, false
	}
	client, err := s.registry.GetRadarrClient(instanceID)
	if err != nil || client == nil {
		return nil, false
	}
	return s.movieDigestFor(client, instanceID)
}

func (s *Service) movieDigestFor(client *radarr.Client, instanceID string) (map[int]movieAvailability, bool) {
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
		entry := movieAvailability{HasFile: m.HasFile, Monitored: m.Monitored}
		if m.HasFile && m.MovieFile.DateAdded != nil {
			added := m.MovieFile.DateAdded.UTC()
			entry.AddedAt = &added
		}
		digest[m.TmdbID] = entry
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
	return s.seriesDigestFor(client, instanceID)
}

// seriesAvailabilityDigestForInstance is seriesAvailabilityDigest with the
// resolution hoisted out; the same authorization note as the movie variant
// applies.
func (s *Service) seriesAvailabilityDigestForInstance(instanceID string) (map[int]seriesAvailability, bool) {
	if s.registry == nil || instanceID == "" {
		return nil, false
	}
	client, err := s.registry.GetSonarrClient(instanceID)
	if err != nil || client == nil {
		return nil, false
	}
	return s.seriesDigestFor(client, instanceID)
}

func (s *Service) seriesDigestFor(client *sonarr.Client, instanceID string) (map[int]seriesAvailability, bool) {
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
		entry := seriesAvailability{Files: files, Total: total, Monitored: sr.Monitored}
		for _, season := range sr.Seasons {
			if season.SeasonNumber <= 0 || season.Statistics == nil {
				continue
			}
			seasonTotal := season.Statistics.TotalEpisodeCount
			if seasonTotal == 0 {
				seasonTotal = season.Statistics.EpisodeCount
			}
			if entry.Seasons == nil {
				entry.Seasons = map[int]seasonAvailability{}
			}
			entry.Seasons[season.SeasonNumber] = seasonAvailability{
				Files:     season.Statistics.EpisodeFileCount,
				Total:     seasonTotal,
				Monitored: season.Monitored,
			}
		}
		digest[sr.TvdbID] = entry
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

// InvalidateAvailabilityDigests drops the cached movie/series availability
// digests for one instance so the next status or history read refetches.
// Called by the arr webhook receiver when the library changes out-of-band
// (imports, deletes, adds done directly in the arr).
func (s *Service) InvalidateAvailabilityDigests(instanceID string) {
	if s.libraryCache == nil || instanceID == "" {
		return
	}
	s.libraryCache.Delete("movie-availability:" + instanceID)
	s.libraryCache.Delete("series-availability:" + instanceID)
}

// InvalidateAllAvailabilityDigests drops every Radarr and Sonarr digest, so
// the next ledger read fetches fresh library state. It is what the
// Seerr-compatible availability-sync job does: an integrator that just
// deleted from a library asks for the change to show now, not after the
// cache window.
func (s *Service) InvalidateAllAvailabilityDigests() {
	if s.registry == nil {
		return
	}
	for _, serviceType := range []string{"radarr", "sonarr"} {
		instances, err := s.registry.ListInstanceSummaries(serviceType)
		if err != nil {
			continue
		}
		for _, inst := range instances {
			s.InvalidateAvailabilityDigests(inst.ID)
		}
	}
}

// InvalidateBookDigests drops the cached book availability and recency for one
// Chaptarr instance so the next read refetches. These are the only keys holding
// book state; the per-title search cache holds metadata and is deliberately
// left alone.
//
// Called by the arr webhook receiver when a Chaptarr library changes
// out-of-band, so a user who taps a "book is ready" alert lands on fresh state
// instead of up to the cache TTL of stale "Requested" — and so a newly imported
// book appears in the Recently Added row immediately.
func (s *Service) InvalidateBookDigests(instanceID string) {
	if s.libraryCache == nil || instanceID == "" {
		return
	}
	s.libraryCache.Delete("book-library:" + instanceID)
	s.libraryCache.Delete("book-live:" + instanceID)
	s.libraryCache.Delete("book-recent:" + instanceID)
	s.libraryCache.Delete("book-authors:" + instanceID)
	s.libraryCache.Delete("book-series:" + instanceID)
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
