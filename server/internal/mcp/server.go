package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/request"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// Tool describes a tool that the AI can invoke.
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// ToolServer executes tools in-process on behalf of the AI.
type ToolServer struct {
	creds    *credentials.Registry
	request  *request.Service
	registry *instance.Registry
}

func NewToolServer(creds *credentials.Registry, requestSvc *request.Service, registry *instance.Registry) *ToolServer {
	return &ToolServer{
		creds:    creds,
		request:  requestSvc,
		registry: registry,
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
	return nil
}

// GetSonarr returns the default Sonarr client.
func (s *ToolServer) GetSonarr() *sonarr.Client {
	if s.registry != nil {
		client, _, err := s.registry.GetDefaultSonarrClient()
		if err == nil && client != nil {
			return client
		}
	}
	return nil
}

// GetTools returns the list of tools available to the AI.
func (s *ToolServer) GetTools() []Tool {
	return toolDefinitions
}

// ExecuteTool runs the named tool with the given JSON input.
func (s *ToolServer) ExecuteTool(ctx context.Context, name string, input json.RawMessage, userID int64) (*ToolResult, error) {
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
	case "display_media":
		return s.displayMedia(input)
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}
