package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

func TestDisplayMediaMismatch(t *testing.T) {
	if got := displayMediaMismatch("Toy Story 5", "2026", "Toy Story 5", "2026"); got != "" {
		t.Fatalf("matching item rejected: %s", got)
	}
	if got := displayMediaMismatch("I Will Find You", "2026", "Kaliveedu", "1996"); got == "" {
		t.Fatal("mismatched title was not rejected")
	}
	if got := displayMediaMismatch("Toy Story 5", "2026", "Toy Story 5", "1995"); got == "" {
		t.Fatal("mismatched year was not rejected")
	}
}

func TestFormatSearchResultsIncludesMediaType(t *testing.T) {
	text := formatSearchResults([]tmdb.SearchResult{
		{
			ID:          123,
			Title:       "Example",
			ReleaseDate: "2026-01-01",
			MediaType:   "movie",
		},
	}, 10)

	if !strings.Contains(text, "[media_type: movie]") {
		t.Fatalf("formatted result missing media_type: %s", text)
	}
}

func TestToolDefinitionsDeclarePermissions(t *testing.T) {
	for _, tool := range toolDefinitions {
		if tool.RequiredPermission() == "" {
			t.Fatalf("tool %q does not declare an RBAC permission", tool.Name)
		}
	}
}

func TestGetToolsForRoleFiltersOperationalTools(t *testing.T) {
	server := NewToolServer(nil, nil, nil, nil)
	userTools := server.GetToolsForRole(auth.RoleUser)

	if !hasTool(userTools, "search_movies") {
		t.Fatal("user tools should include media discovery")
	}
	if hasTool(userTools, "get_queue") {
		t.Fatal("user tools should not include operational queue access")
	}

	adminTools := server.GetToolsForRole(auth.RoleAdmin)
	if !hasTool(adminTools, "get_queue") {
		t.Fatal("admin tools should include operational queue access")
	}
}

func TestExecuteToolDeniesRoleBeforeRunningTool(t *testing.T) {
	server := NewToolServer(nil, nil, nil, nil)

	result, err := server.ExecuteTool(
		context.Background(),
		"get_queue",
		json.RawMessage(`{}`),
		CallContext{UserID: 1, Role: auth.RoleUser},
	)
	if err != nil {
		t.Fatalf("ExecuteTool returned error: %v", err)
	}
	if result == nil || result.Text != "This action is not permitted for your role." {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func hasTool(tools []Tool, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}
