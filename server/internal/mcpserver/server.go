package mcpserver

import (
	"context"
	"net/http"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/windoze95/cantinarr-server/internal/mcp"
	"github.com/windoze95/cantinarr-server/internal/version"
)

const mcpServerInstructions = "Cantinarr manages household media discovery, requests, and administrator workflows. Tool availability is filtered for the authenticated role and current administrator settings. Read guide://cantinarr/agent-guide.md or use the cantinarr-agent-guide prompt before complex workflows."

// NewMCPHandler creates an http.Handler that serves the MCP protocol
// over Streamable HTTP, bridging Cantinarr's existing ToolServer.
func NewMCPHandler(toolServer *mcp.ToolServer) http.Handler {
	mcpServer := server.NewMCPServer(
		"cantinarr",
		version.Version,
		server.WithToolCapabilities(false),
		server.WithResourceCapabilities(false, false),
		server.WithPromptCapabilities(false),
		server.WithInstructions(mcpServerInstructions),
		server.WithToolFilter(ToolListFilter(toolServer)),
	)

	RegisterTools(mcpServer, toolServer)
	registerMediaResultsResource(mcpServer)
	registerAgentGuidance(mcpServer, toolServer)

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
		AdditionalFields: mediaResultsResourceMeta(),
	}
	mcpServer.AddResource(resource, func(ctx context.Context, request mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error) {
		return []mcplib.ResourceContents{
			mcplib.TextResourceContents{
				Meta:     mediaResultsResourceMeta(),
				URI:      MediaResultsResourceURI,
				MIMEType: "text/html;profile=mcp-app",
				Text:     mediaResultsAppHTML,
			},
		}, nil
	})
}

func mediaResultsResourceMeta() map[string]any {
	return map[string]any{
		"ui": map[string]any{
			"csp": map[string]any{
				"resourceDomains": []string{"https://image.tmdb.org"},
			},
		},
	}
}
