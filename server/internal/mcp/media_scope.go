package mcp

import (
	"fmt"

	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// mediaReadScope is an optional, server-imposed identity filter used by the
// remediation runner. Ordinary admin MCP calls may omit it; when present, read
// tools fail closed instead of rendering records for another title/episode.
type mediaReadScope struct {
	QueueID       int
	DownloadID    string
	TmdbID        int
	TvdbID        int
	SeasonNumber  int
	EpisodeNumber int
}

func (s mediaReadScope) hasTitleIdentity() bool { return s.TmdbID > 0 || s.TvdbID > 0 }

func (s mediaReadScope) matchesSonarrIdentity(tmdbID, tvdbID int) bool {
	if !s.hasTitleIdentity() {
		return true
	}
	matched := false
	if s.TmdbID > 0 && tmdbID > 0 {
		if tmdbID != s.TmdbID {
			return false
		}
		matched = true
	}
	if s.TvdbID > 0 && tvdbID > 0 {
		if tvdbID != s.TvdbID {
			return false
		}
		matched = true
	}
	return matched
}

func (s mediaReadScope) matchesEpisode(ep *sonarr.EpisodeContext) bool {
	if s.SeasonNumber > 0 && (ep == nil || ep.SeasonNumber != s.SeasonNumber) {
		return false
	}
	if s.EpisodeNumber > 0 && (ep == nil || ep.SeasonNumber != s.SeasonNumber || ep.EpisodeNumber != s.EpisodeNumber) {
		return false
	}
	return true
}

func filterRadarrQueue(client *radarr.Client, items []radarr.DetailedQueueItem, scope mediaReadScope) ([]radarr.DetailedQueueItem, error) {
	out := make([]radarr.DetailedQueueItem, 0, len(items))
	for _, item := range items {
		if scope.QueueID > 0 && item.ID != scope.QueueID {
			continue
		}
		if scope.DownloadID != "" && item.DownloadID != scope.DownloadID {
			continue
		}
		if scope.TmdbID > 0 {
			tmdbID := 0
			movieID := item.MovieID
			if item.Movie != nil {
				tmdbID = item.Movie.TmdbID
				if movieID == 0 {
					movieID = item.Movie.ID
				}
			}
			if tmdbID == 0 && movieID > 0 {
				movie, err := client.GetMovie(movieID)
				if err != nil {
					return nil, fmt.Errorf("resolve queue movie identity: %w", err)
				}
				if movie != nil {
					tmdbID = movie.TmdbID
				}
			}
			if tmdbID != scope.TmdbID {
				continue
			}
		}
		out = append(out, item)
	}
	return out, nil
}

func filterSonarrQueue(client *sonarr.Client, items []sonarr.DetailedQueueItem, scope mediaReadScope) ([]sonarr.DetailedQueueItem, error) {
	out := make([]sonarr.DetailedQueueItem, 0, len(items))
	for _, item := range items {
		if scope.QueueID > 0 && item.ID != scope.QueueID {
			continue
		}
		if scope.DownloadID != "" && item.DownloadID != scope.DownloadID {
			continue
		}
		tmdbID, tvdbID, seriesID := 0, 0, item.SeriesID
		if item.Series != nil {
			tmdbID, tvdbID = item.Series.TmdbID, item.Series.TvdbID
			if seriesID == 0 {
				seriesID = item.Series.ID
			}
		}
		identityMissing := (scope.TmdbID > 0 && tmdbID == 0) || (scope.TvdbID > 0 && tvdbID == 0)
		if scope.hasTitleIdentity() && !scope.matchesSonarrIdentity(tmdbID, tvdbID) && identityMissing && seriesID > 0 {
			series, err := client.GetSeries(seriesID)
			if err != nil {
				return nil, fmt.Errorf("resolve queue series identity: %w", err)
			}
			if series != nil {
				tmdbID, tvdbID = series.TmdbID, series.TvdbID
			}
		}
		if !scope.matchesSonarrIdentity(tmdbID, tvdbID) || !scope.matchesEpisode(item.Episode) {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

func filterRadarrHistory(client *radarr.Client, records []radarr.HistoryRecord, scope mediaReadScope) ([]radarr.HistoryRecord, error) {
	if scope.TmdbID <= 0 {
		return records, nil
	}
	out := make([]radarr.HistoryRecord, 0, len(records))
	for _, rec := range records {
		tmdbID, movieID := 0, 0
		if rec.Movie != nil {
			tmdbID, movieID = rec.Movie.TmdbID, rec.Movie.ID
		}
		if tmdbID == 0 && movieID > 0 {
			movie, err := client.GetMovie(movieID)
			if err != nil {
				return nil, fmt.Errorf("resolve history movie identity: %w", err)
			}
			if movie != nil {
				tmdbID = movie.TmdbID
			}
		}
		if tmdbID == scope.TmdbID {
			out = append(out, rec)
		}
	}
	return out, nil
}

func filterSonarrHistory(client *sonarr.Client, records []sonarr.HistoryRecord, scope mediaReadScope) ([]sonarr.HistoryRecord, error) {
	out := make([]sonarr.HistoryRecord, 0, len(records))
	for _, rec := range records {
		tmdbID, tvdbID, seriesID := 0, 0, 0
		if rec.Series != nil {
			tmdbID, tvdbID, seriesID = rec.Series.TmdbID, rec.Series.TvdbID, rec.Series.ID
		}
		identityMissing := (scope.TmdbID > 0 && tmdbID == 0) || (scope.TvdbID > 0 && tvdbID == 0)
		if scope.hasTitleIdentity() && !scope.matchesSonarrIdentity(tmdbID, tvdbID) && identityMissing && seriesID > 0 {
			series, err := client.GetSeries(seriesID)
			if err != nil {
				return nil, fmt.Errorf("resolve history series identity: %w", err)
			}
			if series != nil {
				tmdbID, tvdbID = series.TmdbID, series.TvdbID
			}
		}
		if scope.matchesSonarrIdentity(tmdbID, tvdbID) && scope.matchesEpisode(rec.Episode) {
			out = append(out, rec)
		}
	}
	return out, nil
}
