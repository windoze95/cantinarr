package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/windoze95/cantinarr-server/internal/credentials"
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
	// AdminOnly marks tools that only admin-role users may execute.
	AdminOnly bool `json:"-"`
}

// CallContext carries per-call user identity into tool execution.
type CallContext struct {
	UserID int64
	Role   string
}

// ToolServer executes tools in-process on behalf of the AI.
type ToolServer struct {
	creds    *credentials.Registry
	request  *request.Service
	registry *instance.Registry
	bridge   *tmdb.Bridge

	toggleMu      sync.RWMutex
	disabledTools map[string]bool
	togglesLoaded bool
}

func NewToolServer(creds *credentials.Registry, requestSvc *request.Service, registry *instance.Registry, bridge *tmdb.Bridge) *ToolServer {
	return &ToolServer{
		creds:    creds,
		request:  requestSvc,
		registry: registry,
		bridge:   bridge,
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

// AllTools returns every tool definition regardless of enabled state.
func (s *ToolServer) AllTools() []Tool {
	return toolDefinitions
}

// GetTools returns the list of tools available to the AI, excluding tools
// disabled by the administrator.
func (s *ToolServer) GetTools() []Tool {
	tools := make([]Tool, 0, len(toolDefinitions))
	for _, t := range toolDefinitions {
		if s.IsToolEnabled(t.Name) {
			tools = append(tools, t)
		}
	}
	return tools
}

func findToolDefinition(name string) *Tool {
	for i := range toolDefinitions {
		if toolDefinitions[i].Name == name {
			return &toolDefinitions[i]
		}
	}
	return nil
}

// ExecuteTool runs the named tool with the given JSON input.
func (s *ToolServer) ExecuteTool(ctx context.Context, name string, input json.RawMessage, callCtx CallContext) (*ToolResult, error) {
	def := findToolDefinition(name)
	if def == nil {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	if !s.IsToolEnabled(name) {
		return &ToolResult{Text: "This tool is disabled by the administrator."}, nil
	}
	if def.AdminOnly && callCtx.Role != "admin" {
		return &ToolResult{Text: "This action requires an admin account."}, nil
	}

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
		return s.requestMedia(input, callCtx.UserID)
	case "list_my_requests":
		return s.listMyRequests(callCtx.UserID)
	case "display_media":
		return s.displayMedia(input)
	case "get_queue":
		return s.getQueue(input)
	case "get_calendar":
		return s.getCalendar(input)
	case "get_library":
		return s.getLibrary(input)
	case "get_history":
		return s.getHistory(input)
	case "trigger_search":
		return s.triggerSearch(input)
	case "search_releases":
		return s.searchReleases(input)
	case "grab_release":
		return s.grabRelease(input)
	case "remove_queue_item":
		return s.removeQueueItem(input)
	case "get_disk_space":
		return s.getDiskSpace()
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}
