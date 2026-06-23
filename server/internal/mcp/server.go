package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
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
	// Permission is the RBAC capability required to list and execute this tool.
	Permission auth.Permission `json:"-"`
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

// GetTools returns all enabled tools. Prefer GetToolsForRole when serving a
// user request so RBAC filtering happens before tools are offered to the model.
func (s *ToolServer) GetTools() []Tool {
	return s.GetToolsForRole(auth.RoleAdmin)
}

// GetToolsForRole returns the enabled tools a role is allowed to execute.
func (s *ToolServer) GetToolsForRole(role string) []Tool {
	tools := make([]Tool, 0, len(toolDefinitions))
	for _, t := range toolDefinitions {
		if s.IsToolEnabled(t.Name) && t.AllowedForRole(role) {
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

func (t Tool) RequiredPermission() auth.Permission {
	if t.Permission != "" {
		return t.Permission
	}
	if t.AdminOnly {
		return auth.PermissionAdmin
	}
	return ""
}

func (t Tool) AllowedForRole(role string) bool {
	return auth.HasPermission(role, t.RequiredPermission())
}

func (t Tool) IsAdminOnly() bool {
	return t.AdminOnly || !auth.HasPermission(auth.RoleUser, t.RequiredPermission())
}

// ExecuteTool runs the named tool with the given JSON input.
func (s *ToolServer) ExecuteTool(ctx context.Context, name string, input json.RawMessage, callCtx CallContext) (result *ToolResult, err error) {
	debug := s.IsAIDebugEnabled()
	start := time.Now()
	if debug {
		log.Printf("ai debug: tool start name=%s user_id=%d role=%s input=%s", name, callCtx.UserID, callCtx.Role, truncateLog(string(input), 2000))
		defer func() {
			status := "ok"
			if err != nil {
				status = "error"
			}
			log.Printf("ai debug: tool end name=%s status=%s duration_ms=%d result=%s err=%v",
				name, status, time.Since(start).Milliseconds(), toolResultLog(result), err)
		}()
	}

	def := findToolDefinition(name)
	if def == nil {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	if !s.IsToolEnabled(name) {
		return &ToolResult{Text: "This tool is disabled by the administrator."}, nil
	}
	if !def.AllowedForRole(callCtx.Role) {
		return &ToolResult{Text: "This action is not permitted for your role."}, nil
	}

	switch name {
	case "search_movies":
		return s.searchMovies(input)
	case "search_movie_collections":
		return s.searchMovieCollections(input)
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
		return s.checkRequestStatus(input, callCtx.UserID)
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
	case "diagnose_queue":
		return s.diagnoseQueue(input)
	case "get_manual_import_candidates":
		return s.getManualImportCandidates(input)
	case "execute_manual_import":
		return s.executeManualImport(input)
	case "remediate_queue_item":
		return s.remediateQueueItem(input)
	case "rescan_media":
		return s.rescanMedia(input)
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func toolResultLog(result *ToolResult) string {
	if result == nil {
		return ""
	}
	var parts []string
	if result.Text != "" {
		parts = append(parts, "text="+truncateLog(result.Text, 2000))
	}
	if result.StructuredData != nil {
		switch v := result.StructuredData.(type) {
		case []MediaResultItem:
			parts = append(parts, fmt.Sprintf("structured_media_items=%d", len(v)))
		default:
			parts = append(parts, fmt.Sprintf("structured_type=%T", result.StructuredData))
		}
	}
	return strings.Join(parts, " ")
}

func truncateLog(value string, max int) string {
	value = strings.ReplaceAll(value, "\n", "\\n")
	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}
