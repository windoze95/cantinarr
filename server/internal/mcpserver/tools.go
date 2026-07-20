package mcpserver

import (
	"context"
	"encoding/json"
	"log"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	internalmcp "github.com/windoze95/cantinarr-server/internal/mcp"
)

// mcpAppUIMeta returns a Meta with _meta.ui pointing to the media results app.
func mcpAppUIMeta() *mcp.Meta {
	return &mcp.Meta{
		AdditionalFields: map[string]interface{}{
			"ui": map[string]interface{}{
				"resourceUri": MediaResultsResourceURI,
			},
		},
	}
}

// RegisterTools bridges all existing ToolServer tools into the mcp-go MCPServer.
// Every tool is registered (including currently disabled ones) so that runtime
// toggle changes take effect without a restart; ToolListFilter hides disabled
// and unauthorized tools from list_tools, and ExecuteTool enforces both at
// call time.
func RegisterTools(mcpServer *server.MCPServer, toolServer *internalmcp.ToolServer) {
	for _, tool := range toolServer.AllTools() {
		schemaJSON, err := json.Marshal(tool.InputSchema)
		if err != nil {
			log.Printf("mcpserver: failed to marshal schema for tool %q: %v", tool.Name, err)
			continue
		}

		mcpTool := mcp.NewToolWithRawSchema(tool.Name, tool.Description, schemaJSON)
		if internalmcp.ToolsWithUI[tool.Name] {
			mcpTool.Meta = mcpAppUIMeta()
		}
		mcpServer.AddTool(mcpTool, makeToolHandler(toolServer, tool.Name))
	}
}

// ToolListFilter hides administrator-disabled tools and tools the current role
// is not allowed to execute.
func ToolListFilter(toolServer *internalmcp.ToolServer) server.ToolFilterFunc {
	toolByName := map[string]internalmcp.Tool{}
	for _, t := range toolServer.AllTools() {
		toolByName[t.Name] = t
	}
	return func(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
		role := GetRoleFromContext(ctx)
		filtered := make([]mcp.Tool, 0, len(tools))
		for _, t := range tools {
			if !toolServer.IsToolEnabled(t.Name) {
				continue
			}
			def, ok := toolByName[t.Name]
			if !ok || def.InAppChatOnly || !def.AllowedForRole(role) {
				continue
			}
			filtered = append(filtered, t)
		}
		return filtered
	}
}

func makeToolHandler(toolServer *internalmcp.ToolServer, toolName string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID := GetUserIDFromContext(ctx)
		if userID == 0 {
			return mcp.NewToolResultError("unauthorized: no user in context"), nil
		}

		inputJSON, err := json.Marshal(request.GetArguments())
		if err != nil {
			return mcp.NewToolResultError("invalid arguments"), nil
		}

		callCtx := internalmcp.CallContext{
			UserID:      userID,
			Role:        GetRoleFromContext(ctx),
			DeviceID:    GetDeviceIDFromContext(ctx),
			Reauthorize: true,
			Origin:      internalmcp.OriginExternalMCP,
		}
		result, err := toolServer.ExecuteTool(ctx, toolName, inputJSON, callCtx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		callResult := mcp.NewToolResultText(result.Text)
		if result.StructuredData != nil {
			// structuredContent must be a JSON object per MCP protocol schema
			callResult.StructuredContent = map[string]any{
				"results": result.StructuredData,
			}
		}
		return callResult, nil
	}
}
