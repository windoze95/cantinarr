package request

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

type Service struct {
	db     *sql.DB
	radarr *radarr.Client
	sonarr *sonarr.Client
	bridge *tmdb.Bridge
}

func NewService(db *sql.DB, radarrClient *radarr.Client, sonarrClient *sonarr.Client, bridge *tmdb.Bridge) *Service {
	return &Service{
		db:     db,
		radarr: radarrClient,
		sonarr: sonarrClient,
		bridge: bridge,
	}
}

type CreateRequest struct {
	TmdbID    int    `json:"tmdb_id"`
	MediaType string `json:"media_type"`
	Title     string `json:"title"`
	TvdbID    int    `json:"tvdb_id"`
}

type CreateResponse struct {
	Success bool   `json:"success"`
	Status  string `json:"status"`
	Title   string `json:"title"`
}

type StatusResponse struct {
	Status   string  `json:"status"`
	Progress float64 `json:"progress"`
}

type RequestLog struct {
	TmdbID      int       `json:"tmdb_id"`
	MediaType   string    `json:"media_type"`
	Title       string    `json:"title"`
	Status      string    `json:"status"`
	RequestedAt time.Time `json:"requested_at"`
}

func (s *Service) CreateMediaRequest(userID int64, req *CreateRequest) (*CreateResponse, error) {
	switch req.MediaType {
	case "movie":
		return s.requestMovie(userID, req.TmdbID)
	case "tv":
		return s.requestTV(userID, req)
	default:
		return nil, fmt.Errorf("unsupported media type: %s", req.MediaType)
	}
}

func (s *Service) requestMovie(userID int64, tmdbID int) (*CreateResponse, error) {
	if s.radarr == nil {
		return nil, fmt.Errorf("radarr is not configured")
	}

	// Check if already in Radarr
	existing, err := s.radarr.GetMovieByTMDB(tmdbID)
	if err == nil && existing != nil {
		status := "requested"
		if existing.HasFile {
			status = "available"
		}
		s.logRequest(userID, tmdbID, "movie", existing.Title, status)
		return &CreateResponse{Success: true, Status: status, Title: existing.Title}, nil
	}

	// Lookup movie
	lookup, err := s.radarr.LookupByTMDB(tmdbID)
	if err != nil {
		return nil, fmt.Errorf("movie lookup failed: %w", err)
	}

	// Get defaults
	profiles, err := s.radarr.GetQualityProfiles()
	if err != nil || len(profiles) == 0 {
		return nil, fmt.Errorf("no quality profiles available")
	}
	folders, err := s.radarr.GetRootFolders()
	if err != nil || len(folders) == 0 {
		return nil, fmt.Errorf("no root folders available")
	}

	addReq := &radarr.AddMovieRequest{
		Title:            lookup.Title,
		TmdbID:           lookup.TmdbID,
		Year:             lookup.Year,
		QualityProfileID: profiles[0].ID,
		RootFolderPath:   folders[0].Path,
		Monitored:        true,
	}
	addReq.AddOptions.SearchForMovie = true

	if err := s.radarr.AddMovie(addReq); err != nil {
		return nil, fmt.Errorf("add movie failed: %w", err)
	}

	s.logRequest(userID, tmdbID, "movie", lookup.Title, "requested")
	return &CreateResponse{Success: true, Status: "requested", Title: lookup.Title}, nil
}

func (s *Service) requestTV(userID int64, req *CreateRequest) (*CreateResponse, error) {
	if s.sonarr == nil {
		return nil, fmt.Errorf("sonarr is not configured")
	}

	tvdbID := req.TvdbID
	title := req.Title

	// Cache the client-provided TVDB ID so getTVStatus works without TMDB calls
	if tvdbID != 0 {
		s.db.Exec(
			"INSERT OR REPLACE INTO tmdb_tvdb_cache (tmdb_id, tvdb_id) VALUES (?, ?)",
			req.TmdbID, tvdbID,
		)
	}

	// Check if already in Sonarr
	if tvdbID != 0 {
		existing, err := s.sonarr.GetSeriesByTVDB(tvdbID)
		if err == nil && existing != nil {
			status := "requested"
			if existing.Statistics != nil && existing.Statistics.PercentOfEpisodes >= 100 {
				status = "available"
			} else if existing.Statistics != nil && existing.Statistics.EpisodeFileCount > 0 {
				status = "partial"
			}
			s.logRequest(userID, req.TmdbID, "tv", existing.Title, status)
			return &CreateResponse{Success: true, Status: status, Title: existing.Title}, nil
		}
	}

	// Lookup series
	var lookup *sonarr.LookupResult
	var err error
	if tvdbID != 0 {
		lookup, err = s.sonarr.LookupByTVDB(tvdbID)
	}
	if lookup == nil || err != nil {
		// Fallback to title search
		if title == "" {
			return nil, fmt.Errorf("series lookup failed: no TVDB ID or title provided")
		}
		lookup, err = s.sonarr.LookupByTitle(title)
		if err != nil {
			return nil, fmt.Errorf("series lookup failed: %w", err)
		}
		tvdbID = lookup.TvdbID
	}

	// Get defaults
	profiles, err := s.sonarr.GetQualityProfiles()
	if err != nil || len(profiles) == 0 {
		return nil, fmt.Errorf("no quality profiles available")
	}
	folders, err := s.sonarr.GetRootFolders()
	if err != nil || len(folders) == 0 {
		return nil, fmt.Errorf("no root folders available")
	}

	addReq := &sonarr.AddSeriesRequest{
		Title:            lookup.Title,
		TvdbID:           tvdbID,
		Year:             lookup.Year,
		QualityProfileID: profiles[0].ID,
		RootFolderPath:   folders[0].Path,
		Monitored:        true,
		SeasonFolder:     true,
	}
	addReq.AddOptions.SearchForMissingEpisodes = true

	if err := s.sonarr.AddSeries(addReq); err != nil {
		return nil, fmt.Errorf("add series failed: %w", err)
	}

	s.logRequest(userID, req.TmdbID, "tv", lookup.Title, "requested")
	return &CreateResponse{Success: true, Status: "requested", Title: lookup.Title}, nil
}

func (s *Service) GetStatus(tmdbID int, mediaType string) (*StatusResponse, error) {
	switch mediaType {
	case "movie":
		return s.getMovieStatus(tmdbID)
	case "tv":
		return s.getTVStatus(tmdbID)
	default:
		return &StatusResponse{Status: "unavailable"}, nil
	}
}

func (s *Service) getMovieStatus(tmdbID int) (*StatusResponse, error) {
	if s.radarr == nil {
		return &StatusResponse{Status: "unavailable"}, nil
	}

	movie, err := s.radarr.GetMovieByTMDB(tmdbID)
	if err != nil || movie == nil {
		return &StatusResponse{Status: "unavailable"}, nil
	}

	if movie.HasFile {
		return &StatusResponse{Status: "available", Progress: 1.0}, nil
	}

	// Check queue for download progress
	queue, err := s.radarr.GetQueue()
	if err == nil {
		for _, item := range queue {
			if item.MovieID == movie.ID {
				progress := 0.0
				if item.Size > 0 {
					progress = (item.Size - item.Sizeleft) / item.Size
				}
				return &StatusResponse{Status: "downloading", Progress: progress}, nil
			}
		}
	}

	if movie.Monitored {
		return &StatusResponse{Status: "requested", Progress: 0}, nil
	}

	return &StatusResponse{Status: "unavailable"}, nil
}

func (s *Service) getTVStatus(tmdbID int) (*StatusResponse, error) {
	if s.sonarr == nil {
		return &StatusResponse{Status: "unavailable"}, nil
	}

	// Check cache for tvdb_id
	var tvdbID int
	err := s.db.QueryRow("SELECT tvdb_id FROM tmdb_tvdb_cache WHERE tmdb_id = ?", tmdbID).Scan(&tvdbID)
	if err != nil || tvdbID == 0 {
		// Try to resolve
		bridgeResult, err := s.bridge.ResolveTVDBID(tmdbID)
		if err != nil {
			return &StatusResponse{Status: "unavailable"}, nil
		}
		tvdbID = bridgeResult.TVDBID
	}

	series, err := s.sonarr.GetSeriesByTVDB(tvdbID)
	if err != nil || series == nil {
		return &StatusResponse{Status: "unavailable"}, nil
	}

	if series.Statistics != nil {
		if series.Statistics.PercentOfEpisodes >= 100 {
			return &StatusResponse{Status: "available", Progress: 1.0}, nil
		}
		if series.Statistics.EpisodeFileCount > 0 {
			progress := series.Statistics.PercentOfEpisodes / 100.0
			return &StatusResponse{Status: "partial", Progress: progress}, nil
		}
	}

	if series.Monitored {
		return &StatusResponse{Status: "requested", Progress: 0}, nil
	}

	return &StatusResponse{Status: "unavailable"}, nil
}

func (s *Service) GetRequests(userID int64) ([]RequestLog, error) {
	rows, err := s.db.Query(
		"SELECT tmdb_id, media_type, title, status, requested_at FROM request_log WHERE user_id = ? ORDER BY requested_at DESC",
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("query requests: %w", err)
	}
	defer rows.Close()

	var requests []RequestLog
	for rows.Next() {
		var r RequestLog
		if err := rows.Scan(&r.TmdbID, &r.MediaType, &r.Title, &r.Status, &r.RequestedAt); err != nil {
			return nil, fmt.Errorf("scan request: %w", err)
		}
		requests = append(requests, r)
	}
	return requests, rows.Err()
}

func (s *Service) logRequest(userID int64, tmdbID int, mediaType, title, status string) {
	s.db.Exec(
		"INSERT INTO request_log (user_id, tmdb_id, media_type, title, status) VALUES (?, ?, ?, ?, ?)",
		userID, tmdbID, mediaType, title, status,
	)
}
