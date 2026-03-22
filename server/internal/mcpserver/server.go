package mcpserver

import (
	"context"
	"net/http"

	mcplib "github.com/mark3labs/mcp-go/mcp"
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
		server.WithResourceCapabilities(true, true),
	)

	RegisterTools(mcpServer, toolServer)
	registerMediaResultsResource(mcpServer)

	httpServer := server.NewStreamableHTTPServer(mcpServer,
		server.WithEndpointPath("/mcp"),
		server.WithStateful(true),
		server.WithHTTPContextFunc(AuthContextFunc),
	)

	return httpServer
}

// MediaResultsResourceURI is the ui:// URI for the media results MCP App.
const MediaResultsResourceURI = "ui://cantinarr/media-results.html"

func registerMediaResultsResource(mcpServer *server.MCPServer) {
	resource := mcplib.NewResource(
		MediaResultsResourceURI,
		"Cantinarr Media Results",
		mcplib.WithResourceDescription("Interactive media results viewer"),
		mcplib.WithMIMEType("text/html;profile=mcp-app"),
	)
	resource.Meta = &mcplib.Meta{
		AdditionalFields: map[string]interface{}{
			"csp": map[string]interface{}{
				"img-src": []string{"https://image.tmdb.org"},
			},
		},
	}
	mcpServer.AddResource(resource, func(ctx context.Context, request mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error) {
		return []mcplib.ResourceContents{
			mcplib.TextResourceContents{
				URI:      MediaResultsResourceURI,
				MIMEType: "text/html;profile=mcp-app",
				Text:     mediaResultsAppHTML,
			},
		}, nil
	})
}
