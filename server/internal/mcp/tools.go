package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/request"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

var toolDefinitions = []Tool{
	{
		Name:        "search_movies",
		Permission:  auth.PermissionMediaDiscover,
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
		Name:        "search_movie_collections",
		Permission:  auth.PermissionMediaDiscover,
		Description: "Search TMDB movie collections/franchises by title or keyword. Use this before answering movie franchise, series, saga, collection, count, or title-list questions such as \"how many Minions movies are there?\" so recent and upcoming installments are not missed.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Franchise, collection, series, saga, or title keyword to search for",
				},
			},
			"required": []string{"query"},
		},
	},
	{
		Name:        "search_tv_shows",
		Permission:  auth.PermissionMediaDiscover,
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
		Permission:  auth.PermissionMediaDiscover,
		Description: "Get trending movies and/or TV shows. Use media_type \"all\" for general trending, unspecified category requests, or when the user asks for both movies and shows/TV; it returns a balanced mixed list.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "all"},
					"description": "Type of media to get trending for. Use \"all\" for mixed movie/show requests.",
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
		Permission:  auth.PermissionMediaDiscover,
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
		Permission:  auth.PermissionMediaDiscover,
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
		Permission:  auth.PermissionMediaDiscover,
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
		Permission:  auth.PermissionMediaRequest,
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
		Name:        "get_request_options",
		Permission:  auth.PermissionMediaRequest,
		Description: "Show whether the current user may choose request options and list the quality profiles available for a movie or TV request",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv"},
					"description": "Whether the planned request is a movie or TV show",
				},
			},
			"required": []string{"media_type"},
		},
	},
	{
		Name:        "request_media",
		Permission:  auth.PermissionMediaRequest,
		Description: "Request a movie or TV show, optionally selecting a quality_profile_id returned by get_request_options when the current user may choose quality",
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
				"quality_profile_id": map[string]interface{}{
					"type":        "integer",
					"minimum":     1,
					"description": "Optional Radarr/Sonarr quality profile ID. Honored only when the requester is allowed to choose quality; otherwise their configured default is used.",
				},
			},
			"required": []string{"tmdb_id", "media_type"},
		},
	},
	{
		Name:        "list_my_requests",
		Permission:  auth.PermissionMediaRequest,
		Description: "List the current user's media request history",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "display_media",
		Permission:  auth.PermissionMediaDiscover,
		Description: "Display specific movies or TV shows in the UI carousel. Call this whenever your answer names concrete titles to showcase, including recommendations, search/trending picks, franchise/title-list answers, or count answers that enumerate titles. Keep the item order identical to the order you mention in text. Prefer TMDB IDs copied from prior tool results; if you only have exact title/year values, omit tmdb_id and the server will resolve and verify them.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"items": map[string]interface{}{
					"type":        "array",
					"description": "List of media items to display, ordered by relevance (max 10)",
					"maxItems":    10,
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"tmdb_id": map[string]interface{}{
								"type":        "integer",
								"description": "The TMDB ID of the movie or TV show when known from prior tool output. Omit or pass 0 to resolve by exact title/year.",
							},
							"media_type": map[string]interface{}{
								"type":        "string",
								"enum":        []string{"movie", "tv"},
								"description": "Whether this is a movie or TV show",
							},
							"title": map[string]interface{}{
								"type":        "string",
								"description": "Exact title from the prior search/tool result for this TMDB ID",
							},
							"year": map[string]interface{}{
								"type":        "string",
								"description": "Four-digit release/first-air year from the prior search/tool result, if available",
							},
						},
						"required": []string{"media_type", "title"},
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
	// ReleaseCandidates is internal, server-observed metadata used to bind a
	// remediation proposal to the exact candidate the model just saw. Reference
	// is a strict one-way SHA-256 selector; raw release capabilities never cross
	// this boundary.
	ReleaseCandidates []ReleaseCandidate
	// Verification is server-authored, typed evidence for safety-sensitive
	// callers. It is never inferred from the model-facing Text field.
	Verification *ToolVerification
}

type ReleaseCandidate struct {
	Reference  string
	IndexerID  int
	Title      string
	Quality    string
	Size       int64
	Protocol   string
	Indexer    string
	Rejected   bool
	Rejections []string
}

// ToolVerification describes one exact, scoped observation made by a read
// tool. Remediation uses it to distinguish "the model read something" from
// deterministic proof that the detector's original queue target disappeared.
type ToolVerification struct {
	Kind          string
	ExactScope    bool
	TargetPresent bool
}

const VerificationQueueTarget = "queue_target"

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
		if r.MediaType != "" {
			fmt.Fprintf(&sb, " [media_type: %s]", r.MediaType)
		}
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

const maxMovieCollectionResults = 3

func (s *ToolServer) searchMovieCollections(input json.RawMessage) (*ToolResult, error) {
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
	matches, err := tmdbClient.SearchMovieCollections(params.Query)
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return &ToolResult{Text: "No movie collections found."}, nil
	}
	if len(matches) > maxMovieCollectionResults {
		matches = matches[:maxMovieCollectionResults]
	}

	collections := make([]tmdb.MovieCollection, 0, len(matches))
	var failures []string
	for _, match := range matches {
		collection, err := tmdbClient.GetMovieCollection(match.ID)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s [%d]: %s", match.Name, match.ID, err.Error()))
			continue
		}
		collections = append(collections, *collection)
	}
	if len(collections) == 0 {
		return &ToolResult{Text: fmt.Sprintf("Movie collections were found, but details could not be loaded: %s", strings.Join(failures, "; "))}, nil
	}

	text := formatMovieCollectionResults(collections, maxDisplayMediaItems)
	if len(failures) > 0 {
		text += fmt.Sprintf("\nSome collection details could not be loaded: %s\n", strings.Join(failures, "; "))
	}
	return &ToolResult{Text: text}, nil
}

func formatMovieCollectionResults(collections []tmdb.MovieCollection, maxParts int) string {
	if len(collections) == 0 {
		return "No movie collections found."
	}
	var sb strings.Builder
	for i, collection := range collections {
		parts := collection.Parts
		displayedParts := parts
		if maxParts > 0 && len(displayedParts) > maxParts {
			displayedParts = displayedParts[:maxParts]
		}
		fmt.Fprintf(&sb, "%d. %s [collection ID: %d] - %d movie(s)\n", i+1, collection.Name, collection.ID, len(parts))
		for _, part := range displayedParts {
			title := part.Title
			if title == "" {
				title = part.Name
			}
			year := searchResultYear(part)
			fmt.Fprintf(&sb, "   - %s", title)
			if year != "" {
				fmt.Fprintf(&sb, " (%s)", year)
			}
			fmt.Fprintf(&sb, " [TMDB ID: %d] [media_type: movie]\n", part.ID)
		}
		if maxParts > 0 && len(parts) > maxParts {
			fmt.Fprintf(&sb, "   ...and %d more movie(s).\n", len(parts)-maxParts)
		}
	}
	return sb.String()
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

func (s *ToolServer) checkRequestStatus(input json.RawMessage, userID int64) (*ToolResult, error) {
	var params struct {
		TmdbID    int    `json:"tmdb_id"`
		MediaType string `json:"media_type"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	status, err := s.request.GetUserStatus(userID, params.TmdbID, params.MediaType)
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(status)
	return &ToolResult{Text: string(data)}, nil
}

func (s *ToolServer) getRequestOptions(input json.RawMessage, userID int64, role string) (*ToolResult, error) {
	var params struct {
		MediaType string `json:"media_type"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	if params.MediaType != "movie" && params.MediaType != "tv" {
		return &ToolResult{Text: "Request options require media_type movie or tv."}, nil
	}
	opts, err := s.request.GetRequestOptions(userID, auth.HasPermission(role, auth.PermissionAdmin), params.MediaType)
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(opts)
	return &ToolResult{Text: string(data)}, nil
}

func (s *ToolServer) requestMedia(input json.RawMessage, userID int64) (*ToolResult, error) {
	var params struct {
		TmdbID           int    `json:"tmdb_id"`
		MediaType        string `json:"media_type"`
		QualityProfileID int    `json:"quality_profile_id"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	resp, err := s.request.CreateMediaRequest(userID, &request.CreateRequest{
		TmdbID:           params.TmdbID,
		MediaType:        params.MediaType,
		QualityProfileID: params.QualityProfileID,
	})
	if err != nil {
		return &ToolResult{Text: fmt.Sprintf("Request failed: %s", err.Error())}, nil
	}
	data, _ := json.Marshal(resp)
	return &ToolResult{Text: string(data)}, nil
}

const maxDisplayMediaItems = 10

func (s *ToolServer) displayMedia(input json.RawMessage) (*ToolResult, error) {
	tmdbClient := s.creds.TMDB()
	if tmdbClient == nil {
		return &ToolResult{Text: "TMDB is not configured on the server."}, nil
	}
	var params struct {
		Items []struct {
			TmdbID    int    `json:"tmdb_id"`
			MediaType string `json:"media_type"`
			Title     string `json:"title"`
			Year      string `json:"year"`
		} `json:"items"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	if len(params.Items) > maxDisplayMediaItems {
		params.Items = params.Items[:maxDisplayMediaItems]
	}

	items := make([]MediaResultItem, 0, len(params.Items))
	var failures []string
	for _, p := range params.Items {
		if strings.TrimSpace(p.Title) == "" {
			failures = append(failures, fmt.Sprintf("%s %d: missing title; pass the exact title for every displayed item", p.MediaType, p.TmdbID))
			continue
		}
		switch p.MediaType {
		case "movie":
			tmdbID := p.TmdbID
			if tmdbID <= 0 {
				result, err := resolveDisplayMediaSearchResult(
					tmdbClient.SearchMovies,
					p.Title,
					p.Year,
				)
				if err != nil {
					failures = append(failures, fmt.Sprintf("movie %q: %s", p.Title, err.Error()))
					continue
				}
				tmdbID = result.ID
			}
			movie, err := tmdbClient.GetMovieDetails(tmdbID)
			if err != nil {
				failures = append(failures, fmt.Sprintf("movie %d: %s", tmdbID, err.Error()))
				continue
			}
			year := ""
			if len(movie.ReleaseDate) >= 4 {
				year = movie.ReleaseDate[:4]
			}
			if reason := displayMediaMismatch(p.Title, p.Year, movie.Title, year); reason != "" {
				failures = append(failures, fmt.Sprintf("movie %d: %s", tmdbID, reason))
				continue
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
			tmdbID := p.TmdbID
			if tmdbID <= 0 {
				result, err := resolveDisplayMediaSearchResult(
					tmdbClient.SearchTV,
					p.Title,
					p.Year,
				)
				if err != nil {
					failures = append(failures, fmt.Sprintf("tv %q: %s", p.Title, err.Error()))
					continue
				}
				tmdbID = result.ID
			}
			tv, err := tmdbClient.GetTVDetails(tmdbID)
			if err != nil {
				failures = append(failures, fmt.Sprintf("tv %d: %s", tmdbID, err.Error()))
				continue
			}
			year := ""
			if len(tv.FirstAir) >= 4 {
				year = tv.FirstAir[:4]
			}
			if reason := displayMediaMismatch(p.Title, p.Year, tv.Name, year); reason != "" {
				failures = append(failures, fmt.Sprintf("tv %d: %s", tmdbID, reason))
				continue
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
		default:
			failures = append(failures, fmt.Sprintf("item %d: invalid media_type %q", p.TmdbID, p.MediaType))
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Displaying %d media item(s) in the carousel.", len(items))
	if len(failures) > 0 {
		fmt.Fprintf(&sb, " Rejected or failed %d item(s): %s. If the user expects these items, call display_media again with exact titles, years, media types, and TMDB IDs from search/tool results when available.", len(failures), strings.Join(failures, "; "))
	}

	return &ToolResult{
		Text:           sb.String(),
		StructuredData: items,
	}, nil
}

type searchMediaFunc func(query string) ([]tmdb.SearchResult, error)

func resolveDisplayMediaSearchResult(search searchMediaFunc, title, year string) (tmdb.SearchResult, error) {
	results, err := search(title)
	if err != nil {
		return tmdb.SearchResult{}, err
	}
	if len(results) == 0 {
		return tmdb.SearchResult{}, fmt.Errorf("no TMDB results found")
	}

	wantTitle := normalizeMediaTitle(title)
	wantYear := strings.TrimSpace(year)
	var titleMatch *tmdb.SearchResult
	for i := range results {
		resultTitle := results[i].Title
		if resultTitle == "" {
			resultTitle = results[i].Name
		}
		if normalizeMediaTitle(resultTitle) != wantTitle {
			continue
		}
		if titleMatch == nil {
			titleMatch = &results[i]
		}
		resultYear := searchResultYear(results[i])
		if wantYear == "" || resultYear == "" || resultYear == wantYear {
			return results[i], nil
		}
	}

	if titleMatch != nil && wantYear == "" {
		return *titleMatch, nil
	}
	if wantYear != "" {
		return tmdb.SearchResult{}, fmt.Errorf("no exact title/year match found for %q (%s)", title, wantYear)
	}
	return tmdb.SearchResult{}, fmt.Errorf("no exact title match found for %q", title)
}

func searchResultYear(result tmdb.SearchResult) string {
	date := result.ReleaseDate
	if date == "" {
		date = result.FirstAirDate
	}
	if len(date) >= 4 {
		return date[:4]
	}
	return ""
}

func displayMediaMismatch(expectedTitle, expectedYear, actualTitle, actualYear string) string {
	if normalizeMediaTitle(expectedTitle) != normalizeMediaTitle(actualTitle) {
		return fmt.Sprintf("title mismatch: expected %q, got %q", expectedTitle, actualTitle)
	}
	expectedYear = strings.TrimSpace(expectedYear)
	if expectedYear != "" && actualYear != "" && expectedYear != actualYear {
		return fmt.Sprintf("year mismatch for %q: expected %s, got %s", actualTitle, expectedYear, actualYear)
	}
	return ""
}

func normalizeMediaTitle(title string) string {
	title = strings.ToLower(strings.TrimSpace(title))
	var sb strings.Builder
	lastSpace := false
	for _, r := range title {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(r)
			lastSpace = false
			continue
		}
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			if !lastSpace && sb.Len() > 0 {
				sb.WriteByte(' ')
				lastSpace = true
			}
		}
	}
	return strings.TrimSpace(sb.String())
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
