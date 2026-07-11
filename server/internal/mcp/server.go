package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/request"
	"github.com/windoze95/cantinarr-server/internal/secrets"
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
	UserID     int64
	Role       string
	InstanceID string // authoritative arr instance for scoped remediation reads
}

// ToolServer executes tools in-process on behalf of the AI.
type ToolServer struct {
	creds    *credentials.Registry
	request  *request.Service
	registry *instance.Registry
	bridge   *tmdb.Bridge

	// issueStore is the remediation write surface used ONLY by the agent-only
	// tools (post_issue_message / conclude_issue). It is injected after
	// construction via SetIssueStore (see issuestore.go) to avoid an import cycle.
	// nil until remediation is wired; the agent-only tools handle nil gracefully.
	issueStore IssueStore

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
	return s.GetRadarrFor("")
}

func (s *ToolServer) GetRadarrFor(instanceID string) *radarr.Client {
	if s.registry != nil {
		if instanceID != "" {
			client, err := s.registry.GetRadarrClient(instanceID)
			if err == nil {
				return client
			}
			return nil
		}
		client, _, err := s.registry.GetDefaultRadarrClient()
		if err == nil && client != nil {
			return client
		}
	}
	return nil
}

// GetSonarr returns the default Sonarr client.
func (s *ToolServer) GetSonarr() *sonarr.Client {
	return s.GetSonarrFor("")
}

func (s *ToolServer) GetSonarrFor(instanceID string) *sonarr.Client {
	if s.registry != nil {
		if instanceID != "" {
			client, err := s.registry.GetSonarrClient(instanceID)
			if err == nil {
				return client
			}
			return nil
		}
		client, _, err := s.registry.GetDefaultSonarrClient()
		if err == nil && client != nil {
			return client
		}
	}
	return nil
}

// GetChaptarr returns the default Chaptarr client. Chaptarr has no global
// default flag, so GetDefaultChaptarrClient resolves an arbitrary configured
// instance (and returns a nil client, no error, when none is configured).
func (s *ToolServer) GetChaptarr() *chaptarr.Client {
	return s.GetChaptarrFor("")
}

func (s *ToolServer) GetChaptarrFor(instanceID string) *chaptarr.Client {
	if s.registry != nil {
		if instanceID != "" {
			client, err := s.registry.GetChaptarrClient(instanceID)
			if err == nil {
				return client
			}
			return nil
		}
		client, _, err := s.registry.GetDefaultChaptarrClient()
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
	logName := safeToolLogName(name)
	if debug {
		// Tool input can contain credentials (notably release GUIDs). Debug mode
		// records shape/size only; it is never permission to log request data.
		log.Printf("ai debug: tool start name=%s user_id=%d role=%s input_bytes=%d", logName, callCtx.UserID, callCtx.Role, len(input))
	}
	defer func() {
		errorType := ""
		if err != nil {
			errorType = fmt.Sprintf("%T", err)
		}
		structuredDropped := sanitizeToolResult(result)
		err = secrets.RedactError(err)
		if debug {
			status := "ok"
			if err != nil {
				status = "error"
			}
			log.Printf("ai debug: tool end name=%s status=%s duration_ms=%d %s error_type=%s",
				logName, status, time.Since(start).Milliseconds(), toolResultMetadata(result, structuredDropped), errorType)
		}
	}()

	def := findToolDefinition(name)
	if def == nil {
		return nil, fmt.Errorf("unknown tool")
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
		return s.getQueue(input, callCtx.InstanceID)
	case "get_calendar":
		return s.getCalendar(input)
	case "get_library":
		return s.getLibrary(input, callCtx.InstanceID)
	case "get_history":
		return s.getHistory(input, callCtx.InstanceID)
	case "trigger_search":
		return s.triggerSearch(input)
	case "search_releases":
		return s.searchReleases(input, callCtx.InstanceID)
	case "grab_release":
		return s.grabRelease(input)
	case "remove_queue_item":
		return s.removeQueueItem(input)
	case "get_disk_space":
		return s.getDiskSpace()
	case "get_arr_health":
		return s.getArrHealth(input, callCtx.InstanceID)
	case "diagnose_queue":
		return s.diagnoseQueue(input, callCtx.InstanceID)
	case "get_manual_import_candidates":
		return s.getManualImportCandidates(input, callCtx.InstanceID)
	case "execute_manual_import":
		return s.executeManualImport(input)
	case "remediate_queue_item":
		return s.remediateQueueItem(input)
	case "rescan_media":
		return s.rescanMedia(input)
	default:
		return nil, fmt.Errorf("unknown tool")
	}
}

// sanitizeToolResult is the single output boundary for every ordinary MCP tool.
// The result may flow to an MCP client, a chat provider, or the remediation
// agent, so both its text and optional rich UI payload must be safe first.
// true means an unencodable structured value was dropped fail-closed.
func sanitizeToolResult(result *ToolResult) (structuredDropped bool) {
	if result == nil {
		return false
	}
	result.Text = secrets.RedactText(result.Text)
	for i := range result.ReleaseCandidates {
		candidate := &result.ReleaseCandidates[i]
		candidate.Reference = secrets.RedactText(candidate.Reference)
		candidate.Title = secrets.RedactText(candidate.Title)
		candidate.Quality = secrets.RedactText(candidate.Quality)
		candidate.Protocol = secrets.RedactText(candidate.Protocol)
		candidate.Indexer = secrets.RedactText(candidate.Indexer)
		for j := range candidate.Rejections {
			candidate.Rejections[j] = secrets.RedactText(candidate.Rejections[j])
		}
	}
	if result.StructuredData != nil {
		redacted, err := secrets.RedactJSONValue(result.StructuredData)
		if err != nil {
			result.StructuredData = nil
			return true
		}
		result.StructuredData = redacted
	}
	return false
}

func toolResultMetadata(result *ToolResult, structuredDropped bool) string {
	if result == nil {
		return "result_present=false text_bytes=0 structured_present=false structured_dropped=false"
	}
	return fmt.Sprintf("result_present=true text_bytes=%d structured_present=%t structured_dropped=%t",
		len(result.Text), result.StructuredData != nil, structuredDropped)
}

func safeToolLogName(name string) string {
	if findToolDefinition(name) == nil {
		return "<unknown>"
	}
	return name
}
