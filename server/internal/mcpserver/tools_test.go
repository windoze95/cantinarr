package mcpserver

import (
	"context"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/windoze95/cantinarr-server/internal/auth"
	internalmcp "github.com/windoze95/cantinarr-server/internal/mcp"
)

func TestExternalMCPToolListHidesInAppChatOnlyTools(t *testing.T) {
	toolServer := internalmcp.NewToolServer(nil, nil, nil, nil)
	wantHidden := map[string]bool{
		"preview_profile_change": false,
		"apply_profile_change":   false,
	}
	for _, tool := range toolServer.AllTools() {
		if _, tracked := wantHidden[tool.Name]; tracked {
			wantHidden[tool.Name] = tool.InAppChatOnly
		}
	}
	for name, declaredInAppOnly := range wantHidden {
		if !declaredInAppOnly {
			t.Fatalf("tool %q is missing or is not marked InAppChatOnly", name)
		}
	}

	ctx := context.WithValue(context.Background(), roleKey, auth.RoleAdmin)
	listed := []mcplib.Tool{
		mcplib.NewTool("get_queue"),
		mcplib.NewTool("preview_profile_change"),
		mcplib.NewTool("apply_profile_change"),
	}
	filtered := ToolListFilter(toolServer)(ctx, listed)
	if len(filtered) != 1 || filtered[0].Name != "get_queue" {
		t.Fatalf("external MCP tool list = %#v, want only get_queue", toolNames(filtered))
	}

	guide := agentGuideText(toolServer, auth.RoleAdmin)
	if !strings.Contains(guide, "get_queue") {
		t.Fatalf("external MCP guide omitted an available tool:\n%s", guide)
	}
	for name := range wantHidden {
		if strings.Contains(guide, name) {
			t.Fatalf("external MCP guide advertised in-app-only tool %q:\n%s", name, guide)
		}
	}
}

func TestExternalMCPDirectCallHasNoTrustedInteractiveProvenance(t *testing.T) {
	toolServer := internalmcp.NewToolServer(nil, nil, nil, nil)
	var observed internalmcp.CallContext
	toolServer.SetCallAuthorizer(func(_ context.Context, callCtx internalmcp.CallContext) (string, error) {
		observed = callCtx
		return auth.RoleAdmin, nil
	})

	ctx := context.WithValue(context.Background(), userIDKey, int64(23))
	ctx = context.WithValue(ctx, roleKey, auth.RoleAdmin)
	ctx = context.WithValue(ctx, deviceIDKey, "device-23")
	result, err := makeToolHandler(toolServer, "apply_profile_change")(ctx, mcplib.CallToolRequest{
		Params: mcplib.CallToolParams{
			Name: "apply_profile_change",
			Arguments: map[string]any{
				"change_reference": "external-mcp-cannot-confirm",
			},
		},
	})
	if err != nil {
		t.Fatalf("external MCP handler: %v", err)
	}
	if observed.UserID != 23 || observed.Role != auth.RoleAdmin || observed.DeviceID != "device-23" || !observed.Reauthorize {
		t.Fatalf("external MCP actor context = %#v", observed)
	}
	if observed.Origin != internalmcp.OriginExternalMCP || observed.TrustedUserText != "" || observed.InteractiveTurnID != "" {
		t.Fatalf("external MCP trusted provenance = %#v", observed)
	}
	if result == nil || result.IsError || len(result.Content) != 1 {
		t.Fatalf("direct in-app-only call result = %#v", result)
	}
	content, ok := mcplib.AsTextContent(result.Content[0])
	if !ok || !strings.Contains(content.Text, "new in-app chat message") {
		t.Fatalf("direct in-app-only call did not fail closed: %#v", result.Content)
	}
}

func toolNames(tools []mcplib.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}
