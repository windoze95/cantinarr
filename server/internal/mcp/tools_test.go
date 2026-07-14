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

func TestFormatMovieCollectionResultsIncludesPartsAndCount(t *testing.T) {
	text := formatMovieCollectionResults([]tmdb.MovieCollection{
		{
			ID:   10,
			Name: "Minions Collection",
			Parts: []tmdb.SearchResult{
				{
					ID:          1,
					Title:       "Minions",
					ReleaseDate: "2015-06-17",
				},
				{
					ID:          2,
					Title:       "Minions: The Rise of Gru",
					ReleaseDate: "2022-06-29",
				},
				{
					ID:          3,
					Title:       "Minions & Monsters",
					ReleaseDate: "2026-07-01",
				},
			},
		},
	}, 10)

	for _, want := range []string{
		"Minions Collection [collection ID: 10] - 3 movie(s)",
		"Minions (2015) [TMDB ID: 1] [media_type: movie]",
		"Minions: The Rise of Gru (2022) [TMDB ID: 2] [media_type: movie]",
		"Minions & Monsters (2026) [TMDB ID: 3] [media_type: movie]",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("formatted collection missing %q:\n%s", want, text)
		}
	}
}

func TestResolveDisplayMediaSearchResultUsesExactTitleAndYear(t *testing.T) {
	got, err := resolveDisplayMediaSearchResult(
		func(query string) ([]tmdb.SearchResult, error) {
			if query != "Minions: The Rise of Gru" {
				t.Fatalf("query = %q", query)
			}
			return []tmdb.SearchResult{
				{
					ID:          1,
					Title:       "Minions",
					ReleaseDate: "2015-06-17",
				},
				{
					ID:          2,
					Title:       "Minions: The Rise of Gru",
					ReleaseDate: "2022-06-29",
				},
			}, nil
		},
		"Minions: The Rise of Gru",
		"2022",
	)
	if err != nil {
		t.Fatalf("resolve returned error: %v", err)
	}
	if got.ID != 2 {
		t.Fatalf("resolved ID = %d, want 2", got.ID)
	}
}

func TestResolveDisplayMediaSearchResultRejectsWrongYear(t *testing.T) {
	_, err := resolveDisplayMediaSearchResult(
		func(query string) ([]tmdb.SearchResult, error) {
			return []tmdb.SearchResult{
				{
					ID:          1,
					Title:       "Despicable Me",
					ReleaseDate: "2010-07-08",
				},
			}, nil
		},
		"Despicable Me",
		"2024",
	)
	if err == nil {
		t.Fatal("wrong year was not rejected")
	}
}

func TestToolDefinitionsDeclarePermissions(t *testing.T) {
	for _, tool := range toolDefinitions {
		if tool.RequiredPermission() == "" {
			t.Fatalf("tool %q does not declare an RBAC permission", tool.Name)
		}
	}
}

func TestToolRBACMatrixCoversEveryDefinition(t *testing.T) {
	server := NewToolServer(nil, nil, nil, nil)
	listedByRole := map[string]map[string]bool{}
	for _, role := range []string{auth.RoleAdmin, auth.RoleUser, "unknown"} {
		listedByRole[role] = make(map[string]bool)
		for _, tool := range server.GetToolsForRole(role) {
			if listedByRole[role][tool.Name] {
				t.Fatalf("role %q received duplicate tool %q", role, tool.Name)
			}
			listedByRole[role][tool.Name] = true
		}
	}

	seen := make(map[string]bool, len(toolDefinitions))
	for _, tool := range toolDefinitions {
		if seen[tool.Name] {
			t.Fatalf("duplicate tool definition %q", tool.Name)
		}
		seen[tool.Name] = true

		for _, role := range []string{auth.RoleAdmin, auth.RoleUser, "unknown"} {
			want := auth.HasPermission(role, tool.RequiredPermission())
			if got := listedByRole[role][tool.Name]; got != want {
				t.Errorf("role %q listed tool %q = %t, want %t for permission %q", role, tool.Name, got, want, tool.RequiredPermission())
			}
			if want {
				continue
			}

			result, err := server.ExecuteTool(
				context.Background(),
				tool.Name,
				json.RawMessage(`{}`),
				CallContext{UserID: 1, Role: role},
			)
			if err != nil {
				t.Errorf("role %q denied tool %q with error: %v", role, tool.Name, err)
				continue
			}
			if result == nil || result.Text != "This action is not permitted for your role." {
				t.Errorf("role %q tool %q denial = %#v", role, tool.Name, result)
			}
		}
	}

	if len(listedByRole[auth.RoleAdmin]) != len(toolDefinitions) {
		t.Fatalf("admin tool count = %d, want all %d definitions", len(listedByRole[auth.RoleAdmin]), len(toolDefinitions))
	}
	if len(listedByRole["unknown"]) != 0 {
		t.Fatalf("unknown role received %d tools", len(listedByRole["unknown"]))
	}
	for _, tool := range AgentTools() {
		if tool.RequiredPermission() != auth.PermissionRemediationManage {
			t.Errorf("agent-only tool %q permission = %q", tool.Name, tool.RequiredPermission())
		}
		if seen[tool.Name] || hasTool(server.GetToolsForRole(auth.RoleAdmin), tool.Name) || hasTool(server.GetToolsForRole(auth.RoleUser), tool.Name) {
			t.Errorf("agent-only tool %q leaked into interactive chat tools", tool.Name)
		}
	}
}

func TestGetToolsForRoleFiltersOperationalTools(t *testing.T) {
	server := NewToolServer(nil, nil, nil, nil)
	userTools := server.GetToolsForRole(auth.RoleUser)

	if !hasTool(userTools, "search_movies") {
		t.Fatal("user tools should include media discovery")
	}
	if !hasTool(userTools, "search_movie_collections") {
		t.Fatal("user tools should include movie collection discovery")
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

func TestSanitizeToolResultScrubsTextErrorsAndStructuredData(t *testing.T) {
	result := &ToolResult{
		Text: `arr failed: {"downloadUrl":"https://indexer.invalid/get?apiKey=text-secret&id=4"}`,
		StructuredData: map[string]any{
			"nested": map[string]any{
				"authorization": "Bearer structured-secret",
				"detail":        "kept",
			},
		},
	}
	if dropped := sanitizeToolResult(result); dropped {
		t.Fatal("JSON-compatible structured output was dropped")
	}
	encoded, err := json.Marshal(result.StructuredData)
	if err != nil {
		t.Fatal(err)
	}
	combined := result.Text + string(encoded)
	for _, secret := range []string{"text-secret", "structured-secret"} {
		if strings.Contains(combined, secret) {
			t.Fatalf("tool boundary leaked %q: %s", secret, combined)
		}
	}
	if !strings.Contains(combined, "id=4") || !strings.Contains(combined, "kept") {
		t.Fatalf("tool boundary removed useful diagnosis: %s", combined)
	}
}

func TestToolDebugMetadataContainsNoPayload(t *testing.T) {
	secret := "must-never-appear-in-tool-debug"
	result := &ToolResult{Text: "Authorization: Bearer " + secret, StructuredData: []any{"token=" + secret}}
	sanitizeToolResult(result)
	metadata := toolResultMetadata(result, false)
	if strings.Contains(metadata, secret) || strings.Contains(metadata, "Authorization") || strings.Contains(metadata, "REDACTED") {
		t.Fatalf("debug metadata contained payload data: %s", metadata)
	}
	for _, want := range []string{"text_bytes=", "structured_present=true"} {
		if !strings.Contains(metadata, want) {
			t.Errorf("debug metadata missing %q: %s", want, metadata)
		}
	}
	if got := safeToolLogName("unknown?apiKey=" + secret); got != "<unknown>" {
		t.Fatalf("unknown tool log name = %q", got)
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
