package mcpserver

import (
	"context"
	"reflect"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestMediaResultsResourceUsesMCPAppMetadata(t *testing.T) {
	mcpServer := server.NewMCPServer("test", "1.0.0", server.WithResourceCapabilities(false, false))
	registerMediaResultsResource(mcpServer)

	listMessage := mcpServer.HandleMessage(context.Background(), []byte(`{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "resources/list"
	}`))
	listResponse, ok := listMessage.(mcplib.JSONRPCResponse)
	if !ok {
		t.Fatalf("resources/list response type = %T, want mcp.JSONRPCResponse", listMessage)
	}
	listResult, ok := listResponse.Result.(mcplib.ListResourcesResult)
	if !ok {
		t.Fatalf("resources/list result type = %T, want mcp.ListResourcesResult", listResponse.Result)
	}
	if len(listResult.Resources) != 1 {
		t.Fatalf("resources/list returned %d resources, want 1", len(listResult.Resources))
	}

	resource := listResult.Resources[0]
	if resource.URI != MediaResultsResourceURI {
		t.Fatalf("resource URI = %q, want %q", resource.URI, MediaResultsResourceURI)
	}
	if resource.Meta == nil {
		t.Fatal("resource metadata is nil")
	}
	wantMeta := mediaResultsResourceMeta()
	if !reflect.DeepEqual(resource.Meta.AdditionalFields, wantMeta) {
		t.Fatalf("resource metadata = %#v, want %#v", resource.Meta.AdditionalFields, wantMeta)
	}

	readMessage := mcpServer.HandleMessage(context.Background(), []byte(`{
		"jsonrpc": "2.0",
		"id": 2,
		"method": "resources/read",
		"params": {"uri": "ui://cantinarr/media-results.html"}
	}`))
	readResponse, ok := readMessage.(mcplib.JSONRPCResponse)
	if !ok {
		t.Fatalf("resources/read response type = %T, want mcp.JSONRPCResponse", readMessage)
	}
	readResult, ok := readResponse.Result.(mcplib.ReadResourceResult)
	if !ok {
		t.Fatalf("resources/read result type = %T, want mcp.ReadResourceResult", readResponse.Result)
	}
	if len(readResult.Contents) != 1 {
		t.Fatalf("resources/read returned %d contents, want 1", len(readResult.Contents))
	}
	contents, ok := readResult.Contents[0].(mcplib.TextResourceContents)
	if !ok {
		t.Fatalf("resource contents type = %T, want mcp.TextResourceContents", readResult.Contents[0])
	}
	if !reflect.DeepEqual(contents.Meta, wantMeta) {
		t.Fatalf("resource contents metadata = %#v, want %#v", contents.Meta, wantMeta)
	}
}

func TestMediaResultsAppDeclaresAppCapabilities(t *testing.T) {
	if !strings.Contains(mediaResultsAppHTML, "appCapabilities: {}") {
		t.Fatal("media results app does not declare appCapabilities in ui/initialize")
	}
	if !strings.Contains(mediaResultsAppHTML, "protocolVersion: '2026-01-26'") {
		t.Fatal("media results app does not declare the MCP Apps protocol version in ui/initialize")
	}
	if strings.Contains(mediaResultsAppHTML, "\n      capabilities: {}") {
		t.Fatal("media results app still uses the deprecated capabilities field in ui/initialize")
	}
}
