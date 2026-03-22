package mcpserver

import (
	"net/http"

	"github.com/mark3labs/mcp-go/server"
	"github.com/windoze95/cantinarr-server/internal/mcp"
)

// NewMCPHandler creates an http.Handler that serves the MCP protocol
// over Streamable HTTP, bridging Cantinarr's existing ToolServer.
func NewMCPHandler(toolServer *mcp.ToolServer) http.Handler {
	mcpServer := server.NewMCPServer(
		"cantinarr",
		"1.0.0",
		server.WithToolCapabilities(false),
	)

	RegisterTools(mcpServer, toolServer)

	httpServer := server.NewStreamableHTTPServer(mcpServer,
		server.WithEndpointPath("/mcp"),
		server.WithStateful(true),
		server.WithHTTPContextFunc(AuthContextFunc),
	)

	return httpServer
}
