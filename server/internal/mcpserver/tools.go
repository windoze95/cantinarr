package mcpserver

import (
	"context"
	"encoding/json"
	"log"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	internalmcp "github.com/windoze95/cantinarr-server/internal/mcp"
)

// RegisterTools bridges all existing ToolServer tools into the mcp-go MCPServer.
func RegisterTools(mcpServer *server.MCPServer, toolServer *internalmcp.ToolServer) {
	for _, tool := range toolServer.GetTools() {
		schemaJSON, err := json.Marshal(tool.InputSchema)
		if err != nil {
			log.Printf("mcpserver: failed to marshal schema for tool %q: %v", tool.Name, err)
			continue
		}

		mcpTool := mcp.NewToolWithRawSchema(tool.Name, tool.Description, schemaJSON)
		mcpServer.AddTool(mcpTool, makeToolHandler(toolServer, tool.Name))
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

		result, err := toolServer.ExecuteTool(ctx, toolName, inputJSON, userID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(result), nil
	}
}
