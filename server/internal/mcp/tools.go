package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/request"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

var toolDefinitions = []Tool{
	{
		Name:        "search_movies",
		Description: "Search TMDB for movies by title or keyword",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Search query for movie titles",
				},
			},
			"required": []string{"query"},
		},
	},
	{
		Name:        "search_tv_shows",
		Description: "Search TMDB for TV shows by title or keyword",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Search query for TV show titles",
				},
			},
			"required": []string{"query"},
		},
	},
	{
		Name:        "get_trending",
		Description: "Get trending movies and/or TV shows",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "all"},
					"description": "Type of media to get trending for",
				},
				"time_window": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"day", "week"},
					"description": "Time window for trending results",
				},
			},
			"required": []string{"media_type", "time_window"},
		},
	},
	{
		Name:        "get_movie_details",
		Description: "Get detailed information about a specific movie",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"tmdb_id": map[string]interface{}{
					"type":        "integer",
					"description": "The TMDB ID of the movie",
				},
			},
			"required": []string{"tmdb_id"},
		},
	},
	{
		Name:        "get_tv_details",
		Description: "Get detailed information about a specific TV show",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"tmdb_id": map[string]interface{}{
					"type":        "integer",
					"description": "The TMDB ID of the TV show",
				},
			},
			"required": []string{"tmdb_id"},
		},
	},
	{
		Name:        "get_recommendations",
		Description: "Get recommendations based on a movie or TV show",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"tmdb_id": map[string]interface{}{
					"type":        "integer",
					"description": "The TMDB ID of the movie or TV show",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv"},
					"description": "Whether this is a movie or TV show",
				},
			},
			"required": []string{"tmdb_id", "media_type"},
		},
	},
	{
		Name:        "check_request_status",
		Description: "Check if a movie or TV show is available, requested, or downloading on the media server",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"tmdb_id": map[string]interface{}{
					"type":        "integer",
					"description": "The TMDB ID of the movie or TV show",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv"},
					"description": "Whether this is a movie or TV show",
				},
			},
			"required": []string{"tmdb_id", "media_type"},
		},
	},
	{
		Name:        "request_media",
		Description: "Request a movie or TV show to be added to the media server",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"tmdb_id": map[string]interface{}{
					"type":        "integer",
					"description": "The TMDB ID of the movie or TV show",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv"},
					"description": "Whether this is a movie or TV show",
				},
			},
			"required": []string{"tmdb_id", "media_type"},
		},
	},
	{
		Name:        "list_my_requests",
		Description: "List the current user's media request history",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "display_media",
		Description: "Display specific movies or TV shows in the UI carousel. Call this after searching to show only the items you want to recommend. The carousel will display items in the order provided.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"items": map[string]interface{}{
					"type":        "array",
					"description": "List of media items to display, ordered by relevance",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"tmdb_id": map[string]interface{}{
								"type":        "integer",
								"description": "The TMDB ID of the movie or TV show",
							},
							"media_type": map[string]interface{}{
								"type":        "string",
								"enum":        []string{"movie", "tv"},
								"description": "Whether this is a movie or TV show",
							},
						},
						"required": []string{"tmdb_id", "media_type"},
					},
				},
			},
			"required": []string{"items"},
		},
	},
}

// MediaResultItem is the structured data the MCP App UI renders.
type MediaResultItem struct {
	ID          int     `json:"id"`
	Title       string  `json:"title"`
	Year        string  `json:"year,omitempty"`
	PosterPath  string  `json:"poster_path,omitempty"`
	VoteAverage float64 `json:"vote_average,omitempty"`
	Overview    string  `json:"overview,omitempty"`
	MediaType   string  `json:"media_type,omitempty"`
}

// ToolResult holds the plain-text output (for LLM consumption) and optional
// structured data (for the MCP App UI).
type ToolResult struct {
	Text           string
	StructuredData any // nil for tools without UI; []MediaResultItem for search/browse tools
}

// ToolsWithUI is the set of tool names that have MCP App UI attached.
var ToolsWithUI = map[string]bool{
	"display_media": true,
}

func toMediaResultItems(results []tmdb.SearchResult, limit int) []MediaResultItem {
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	items := make([]MediaResultItem, 0, len(results))
	for _, r := range results {
		title := r.Title
		if title == "" {
			title = r.Name
		}
		date := r.ReleaseDate
		if date == "" {
			date = r.FirstAirDate
		}
		year := ""
		if len(date) >= 4 {
			year = date[:4]
		}
		items = append(items, MediaResultItem{
			ID:          r.ID,
			Title:       title,
			Year:        year,
			PosterPath:  r.PosterPath,
			VoteAverage: r.VoteAverage,
			Overview:    r.Overview,
			MediaType:   r.MediaType,
		})
	}
	return items
}

func formatSearchResults(results []tmdb.SearchResult, limit int) string {
	if len(results) == 0 {
		return "No results found."
	}
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	var sb strings.Builder
	for i, r := range results {
		title := r.Title
		if title == "" {
			title = r.Name
		}
		date := r.ReleaseDate
		if date == "" {
			date = r.FirstAirDate
		}
		year := ""
		if len(date) >= 4 {
			year = date[:4]
		}
		fmt.Fprintf(&sb, "%d. %s", i+1, title)
		if year != "" {
			fmt.Fprintf(&sb, " (%s)", year)
		}
		fmt.Fprintf(&sb, " [TMDB ID: %d]", r.ID)
		if r.VoteAverage > 0 {
			fmt.Fprintf(&sb, " - Rating: %.1f/10", r.VoteAverage)
		}
		sb.WriteString("\n")
		if r.Overview != "" {
			overview := r.Overview
			if len(overview) > 200 {
				overview = overview[:200] + "..."
			}
			fmt.Fprintf(&sb, "   %s\n", overview)
		}
	}
	return sb.String()
}

func (s *ToolServer) searchMovies(input json.RawMessage) (*ToolResult, error) {
	tmdbClient := s.creds.TMDB()
	if tmdbClient == nil {
		return &ToolResult{Text: "TMDB is not configured on the server."}, nil
	}
	var params struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	results, err := tmdbClient.SearchMovies(params.Query)
	if err != nil {
		return nil, err
	}
	return &ToolResult{
		Text:           formatSearchResults(results, 10),
		StructuredData: toMediaResultItems(results, 10),
	}, nil
}

func (s *ToolServer) searchTVShows(input json.RawMessage) (*ToolResult, error) {
	tmdbClient := s.creds.TMDB()
	if tmdbClient == nil {
		return &ToolResult{Text: "TMDB is not configured on the server."}, nil
	}
	var params struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	results, err := tmdbClient.SearchTV(params.Query)
	if err != nil {
		return nil, err
	}
	return &ToolResult{
		Text:           formatSearchResults(results, 10),
		StructuredData: toMediaResultItems(results, 10),
	}, nil
}

func (s *ToolServer) getTrending(input json.RawMessage) (*ToolResult, error) {
	tmdbClient := s.creds.TMDB()
	if tmdbClient == nil {
		return &ToolResult{Text: "TMDB is not configured on the server."}, nil
	}
	var params struct {
		MediaType  string `json:"media_type"`
		TimeWindow string `json:"time_window"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	results, err := tmdbClient.GetTrending(params.MediaType, params.TimeWindow)
	if err != nil {
		return nil, err
	}
	return &ToolResult{
		Text:           formatSearchResults(results, 10),
		StructuredData: toMediaResultItems(results, 10),
	}, nil
}

func (s *ToolServer) getMovieDetails(input json.RawMessage) (*ToolResult, error) {
	tmdbClient := s.creds.TMDB()
	if tmdbClient == nil {
		return &ToolResult{Text: "TMDB is not configured on the server."}, nil
	}
	var params struct {
		TmdbID int `json:"tmdb_id"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	movie, err := tmdbClient.GetMovieDetails(params.TmdbID)
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(movie)
	return &ToolResult{Text: string(data)}, nil
}

func (s *ToolServer) getTVDetails(input json.RawMessage) (*ToolResult, error) {
	tmdbClient := s.creds.TMDB()
	if tmdbClient == nil {
		return &ToolResult{Text: "TMDB is not configured on the server."}, nil
	}
	var params struct {
		TmdbID int `json:"tmdb_id"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	tv, err := tmdbClient.GetTVDetails(params.TmdbID)
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(tv)
	return &ToolResult{Text: string(data)}, nil
}

func (s *ToolServer) getRecommendations(input json.RawMessage) (*ToolResult, error) {
	tmdbClient := s.creds.TMDB()
	if tmdbClient == nil {
		return &ToolResult{Text: "TMDB is not configured on the server."}, nil
	}
	var params struct {
		TmdbID    int    `json:"tmdb_id"`
		MediaType string `json:"media_type"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	results, err := tmdbClient.GetRecommendations(params.TmdbID, params.MediaType)
	if err != nil {
		return nil, err
	}
	return &ToolResult{
		Text:           formatSearchResults(results, 10),
		StructuredData: toMediaResultItems(results, 10),
	}, nil
}

func (s *ToolServer) checkRequestStatus(input json.RawMessage) (*ToolResult, error) {
	var params struct {
		TmdbID    int    `json:"tmdb_id"`
		MediaType string `json:"media_type"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	status, err := s.request.GetStatus(params.TmdbID, params.MediaType)
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(status)
	return &ToolResult{Text: string(data)}, nil
}

func (s *ToolServer) requestMedia(input json.RawMessage, userID int64) (*ToolResult, error) {
	var params struct {
		TmdbID    int    `json:"tmdb_id"`
		MediaType string `json:"media_type"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	resp, err := s.request.CreateMediaRequest(userID, &request.CreateRequest{
		TmdbID:    params.TmdbID,
		MediaType: params.MediaType,
	})
	if err != nil {
		return &ToolResult{Text: fmt.Sprintf("Request failed: %s", err.Error())}, nil
	}
	data, _ := json.Marshal(resp)
	return &ToolResult{Text: string(data)}, nil
}

func (s *ToolServer) displayMedia(input json.RawMessage) (*ToolResult, error) {
	tmdbClient := s.creds.TMDB()
	if tmdbClient == nil {
		return &ToolResult{Text: "TMDB is not configured on the server."}, nil
	}
	var params struct {
		Items []struct {
			TmdbID    int    `json:"tmdb_id"`
			MediaType string `json:"media_type"`
		} `json:"items"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}

	items := make([]MediaResultItem, 0, len(params.Items))
	for _, p := range params.Items {
		switch p.MediaType {
		case "movie":
			movie, err := tmdbClient.GetMovieDetails(p.TmdbID)
			if err != nil {
				continue
			}
			year := ""
			if len(movie.ReleaseDate) >= 4 {
				year = movie.ReleaseDate[:4]
			}
			items = append(items, MediaResultItem{
				ID:          movie.ID,
				Title:       movie.Title,
				Year:        year,
				PosterPath:  movie.PosterPath,
				VoteAverage: movie.VoteAverage,
				Overview:    movie.Overview,
				MediaType:   "movie",
			})
		case "tv":
			tv, err := tmdbClient.GetTVDetails(p.TmdbID)
			if err != nil {
				continue
			}
			year := ""
			if len(tv.FirstAir) >= 4 {
				year = tv.FirstAir[:4]
			}
			items = append(items, MediaResultItem{
				ID:          tv.ID,
				Title:       tv.Name,
				Year:        year,
				PosterPath:  tv.PosterPath,
				VoteAverage: tv.VoteAverage,
				Overview:    tv.Overview,
				MediaType:   "tv",
			})
		}
	}

	return &ToolResult{
		Text:           fmt.Sprintf("Displaying %d media item(s) in the carousel.", len(items)),
		StructuredData: items,
	}, nil
}

func (s *ToolServer) listMyRequests(userID int64) (*ToolResult, error) {
	requests, err := s.request.GetRequests(userID)
	if err != nil {
		return nil, err
	}
	if len(requests) == 0 {
		return &ToolResult{Text: "No requests found."}, nil
	}
	var sb strings.Builder
	for i, r := range requests {
		fmt.Fprintf(&sb, "%d. %s (%s) - Status: %s - Requested: %s\n",
			i+1, r.Title, r.MediaType, r.Status, r.RequestedAt.Format("2006-01-02"))
	}
	return &ToolResult{Text: sb.String()}, nil
}
