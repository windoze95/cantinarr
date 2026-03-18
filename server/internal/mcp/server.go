package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/request"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

// Tool describes a tool that the AI can invoke.
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// ToolServer executes tools in-process on behalf of the AI.
type ToolServer struct {
	tmdb     *tmdb.Client
	request  *request.Service
	registry *instance.Registry

	// Legacy direct clients (used when registry is nil)
	radarr *radarr.Client
	sonarr *sonarr.Client
}

func NewToolServer(tmdbClient *tmdb.Client, requestSvc *request.Service, registry *instance.Registry, radarrClient *radarr.Client, sonarrClient *sonarr.Client) *ToolServer {
	return &ToolServer{
		tmdb:     tmdbClient,
		request:  requestSvc,
		registry: registry,
		radarr:   radarrClient,
		sonarr:   sonarrClient,
	}
}

// GetRadarr returns the default Radarr client.
func (s *ToolServer) GetRadarr() *radarr.Client {
	if s.registry != nil {
		client, _, err := s.registry.GetDefaultRadarrClient()
		if err == nil && client != nil {
			return client
		}
	}
	return s.radarr
}

// GetSonarr returns the default Sonarr client.
func (s *ToolServer) GetSonarr() *sonarr.Client {
	if s.registry != nil {
		client, _, err := s.registry.GetDefaultSonarrClient()
		if err == nil && client != nil {
			return client
		}
	}
	return s.sonarr
}

// GetTools returns the list of tools available to the AI.
func (s *ToolServer) GetTools() []Tool {
	return toolDefinitions
}

// ExecuteTool runs the named tool with the given JSON input.
func (s *ToolServer) ExecuteTool(ctx context.Context, name string, input json.RawMessage, userID int64) (string, error) {
	switch name {
	case "search_movies":
		return s.searchMovies(input)
	case "search_tv_shows":
		return s.searchTVShows(input)
	case "get_trending":
		return s.getTrending(input)
	case "get_movie_details":
		return s.getMovieDetails(input)
	case "get_tv_details":
		return s.getTVDetails(input)
	case "get_recommendations":
		return s.getRecommendations(input)
	case "check_request_status":
		return s.checkRequestStatus(input)
	case "request_media":
		return s.requestMedia(input, userID)
	case "list_my_requests":
		return s.listMyRequests(userID)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}
