package mcpserver

import (
	"strings"
	"unicode"

	"github.com/mark3labs/mcp-go/mcp"
)

type mcpToolBehavior struct {
	readOnly    bool
	destructive bool
	idempotent  bool
	openWorld   bool
}

var mcpToolBehaviors = map[string]mcpToolBehavior{
	"search_movies":                {readOnly: true, idempotent: true, openWorld: true},
	"search_movie_collections":     {readOnly: true, idempotent: true, openWorld: true},
	"search_tv_shows":              {readOnly: true, idempotent: true, openWorld: true},
	"get_trending":                 {readOnly: true, idempotent: true, openWorld: true},
	"get_movie_details":            {readOnly: true, idempotent: true, openWorld: true},
	"get_tv_details":               {readOnly: true, idempotent: true, openWorld: true},
	"get_recommendations":          {readOnly: true, idempotent: true, openWorld: true},
	"check_request_status":         {readOnly: true, idempotent: true, openWorld: true},
	"get_request_options":          {readOnly: true, idempotent: true, openWorld: true},
	"request_media":                {openWorld: true},
	"list_my_requests":             {readOnly: true, idempotent: true},
	"display_media":                {readOnly: true, idempotent: true, openWorld: true},
	"get_queue":                    {readOnly: true, idempotent: true, openWorld: true},
	"get_calendar":                 {readOnly: true, idempotent: true, openWorld: true},
	"get_library":                  {readOnly: true, idempotent: true, openWorld: true},
	"get_history":                  {readOnly: true, idempotent: true, openWorld: true},
	"trigger_search":               {openWorld: true},
	"search_releases":              {readOnly: true, idempotent: true, openWorld: true},
	"grab_release":                 {openWorld: true},
	"remove_queue_item":            {destructive: true, openWorld: true},
	"get_disk_space":               {readOnly: true, idempotent: true, openWorld: true},
	"get_arr_health":               {readOnly: true, idempotent: true, openWorld: true},
	"diagnose_queue":               {readOnly: true, idempotent: true, openWorld: true},
	"get_manual_import_candidates": {readOnly: true, idempotent: true, openWorld: true},
	"execute_manual_import":        {destructive: true, openWorld: true},
	"remediate_queue_item":         {destructive: true, openWorld: true},
	"rescan_media":                 {destructive: true, openWorld: true},
	"list_arr_instances":           {readOnly: true, idempotent: true},
	"get_quality_profiles":         {readOnly: true, idempotent: true, openWorld: true},
	"get_custom_formats":           {readOnly: true, idempotent: true, openWorld: true},
	"upsert_custom_format":         {destructive: true, openWorld: true},
	"preview_profile_change":       {openWorld: true},
	"apply_profile_change":         {destructive: true, openWorld: true},
}

func mcpToolAnnotations(name string) mcp.ToolAnnotation {
	behavior, ok := mcpToolBehaviors[name]
	if !ok {
		// Unknown tools receive MCP's conservative defaults until they are
		// explicitly classified and covered by the registry test.
		behavior = mcpToolBehavior{destructive: true, openWorld: true}
	}
	return mcp.ToolAnnotation{
		Title:           mcpToolTitle(name),
		ReadOnlyHint:    mcp.ToBoolPtr(behavior.readOnly),
		DestructiveHint: mcp.ToBoolPtr(behavior.destructive),
		IdempotentHint:  mcp.ToBoolPtr(behavior.idempotent),
		OpenWorldHint:   mcp.ToBoolPtr(behavior.openWorld),
	}
}

func mcpToolTitle(name string) string {
	words := strings.Split(name, "_")
	for i, word := range words {
		switch word {
		case "arr":
			words[i] = "*arr"
		case "tv":
			words[i] = "TV"
		default:
			runes := []rune(word)
			if len(runes) > 0 {
				runes[0] = unicode.ToUpper(runes[0])
			}
			words[i] = string(runes)
		}
	}
	return strings.Join(words, " ")
}
