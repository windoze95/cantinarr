package mcp

import (
	"context"
	"errors"

	"github.com/windoze95/cantinarr-server/internal/auth"
)

// ChatCapabilities is a presentation summary, not an authorization token. Each
// tool still authorizes its execution independently. No instance identities,
// credentials, or titles are exposed here.
type ChatCapabilities struct {
	DiscoverMediaTypes []string `json:"discover_media_types"`
	RequestMediaTypes  []string `json:"request_media_types"`
	CheckAvailability  bool     `json:"check_availability"`
	BrowseLibraries    bool     `json:"browse_libraries"`
	CheckDownloads     bool     `json:"check_downloads"`
	ManageDownloads    bool     `json:"manage_downloads"`
	Troubleshoot       bool     `json:"troubleshoot"`
	ConfigureServices  bool     `json:"configure_services"`
}

// ChatCapabilities reads current role, library grants, and enabled tools for
// the assistant introduction. Inventory failures return an error, not a claim
// that no services exist. This does not probe upstream service health.
func (s *ToolServer) ChatCapabilities(ctx context.Context, callCtx CallContext) (*ChatCapabilities, error) {
	callCtx, err := s.authorizeCall(ctx, callCtx)
	if err != nil {
		return nil, err
	}
	if s.registry == nil {
		return nil, errors.New("assistant service inventory unavailable")
	}
	instances, err := s.registry.ListInstanceSummaries("")
	if err != nil {
		return nil, err
	}
	services := map[string]bool{}
	for _, inst := range instances {
		switch inst.ServiceType {
		case "radarr", "sonarr", "chaptarr", "lidarr":
		default:
			continue
		}
		allowed := auth.HasPermission(callCtx.Role, auth.PermissionInstancesManage)
		if !allowed {
			allowed, err = s.registry.UserCanAccessInstance(callCtx.UserID, inst.ID, inst.ServiceType)
			if err != nil {
				return nil, err
			}
		}
		if allowed {
			services[inst.ServiceType] = true
		}
	}
	tools := map[string]bool{}
	for _, tool := range s.GetToolsForRole(callCtx.Role) {
		tools[tool.Name] = true
	}
	caps := &ChatCapabilities{
		DiscoverMediaTypes: []string{},
		RequestMediaTypes:  []string{},
	}
	tmdbAvailable := s.creds != nil && s.creds.TMDBAvailable()
	for _, media := range []struct{ mediaType, service, search string }{
		{"movie", "radarr", "search_movies"},
		{"tv", "sonarr", "search_tv_shows"},
		{"book", "chaptarr", "search_books"},
		{"music", "lidarr", "search_music"},
	} {
		canDiscover := services[media.service]
		if media.mediaType == "movie" || media.mediaType == "tv" {
			// TMDB catalog discovery works without a connected movie/TV library.
			canDiscover = tmdbAvailable
		}
		if canDiscover && tools[media.search] {
			caps.DiscoverMediaTypes = append(caps.DiscoverMediaTypes, media.mediaType)
		}
		if services[media.service] && s.request != nil && tools["request_media"] {
			caps.RequestMediaTypes = append(caps.RequestMediaTypes, media.mediaType)
		}
	}
	hasLibrary := len(services) > 0
	caps.CheckAvailability = hasLibrary && s.request != nil && tools["check_request_status"]
	caps.BrowseLibraries = hasLibrary && tools["get_library"]
	caps.CheckDownloads = hasLibrary && tools["get_queue"]
	caps.ManageDownloads = caps.CheckDownloads && (tools["remove_queue_item"] || tools["remediate_queue_item"])
	caps.Troubleshoot = hasLibrary && (tools["diagnose_queue"] || tools["get_arr_health"])
	caps.ConfigureServices = hasLibrary && ((tools["get_quality_profiles"] && tools["preview_profile_change"] && tools["apply_profile_change"]) ||
		(tools["get_custom_formats"] && tools["upsert_custom_format"]))
	return caps, nil
}
