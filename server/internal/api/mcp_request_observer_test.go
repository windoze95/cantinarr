package api

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPRequestObserverLogsOnlyProtocolMetadata(t *testing.T) {
	const body = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_media","arguments":{"query":"argument-secret"},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"Codex Desktop","version":"2.7.0"},"io.modelcontextprotocol/clientCapabilities":{"private-capability-secret":{}}}}}`

	logs := captureMCPObservationLogs(t)
	handler := mcpRequestObserver(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read restored request body: %v", err)
		}
		if string(got) != body {
			t.Fatalf("restored body = %q, want %q", got, body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"content":"result-secret"}}`)
	}))

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer authorization-secret")
	req.Header.Set("Mcp-Session-Id", "session-secret")
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", "search_media")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	got := logs.String()
	for _, want := range []string{
		`method="tools/call"`,
		`protocol="2026-07-28"`,
		`era="modern"`,
		`lifecycle="request"`,
		`capabilities="present"`,
		`target="search_media"`,
		`client_name="Codex Desktop"`,
		`client_version="2.7.0"`,
		`status=200`,
		`outcome="ok"`,
		`duration_ms=`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("observation log missing %q: %s", want, got)
		}
	}
	for _, forbidden := range []string{
		"authorization-secret",
		"session-secret",
		"argument-secret",
		"private-capability-secret",
		"result-secret",
	} {
		if strings.Contains(got, forbidden) {
			t.Errorf("observation log leaked %q: %s", forbidden, got)
		}
	}
}

func TestMCPRequestObserverClassifiesDiscoveryFallback(t *testing.T) {
	const body = `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"modern-client","version":"1.0.0"},"io.modelcontextprotocol/clientCapabilities":{}}}}`

	logs := captureMCPObservationLogs(t)
	handler := mcpRequestObserver(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"response-detail-secret"}}`)
	}))

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Mcp-Method", "server/discover")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	got := logs.String()
	for _, want := range []string{
		`method="server/discover"`,
		`lifecycle="discovery"`,
		`status=404`,
		`outcome="method_not_found"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("observation log missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "response-detail-secret") {
		t.Fatalf("observation log leaked the JSON-RPC error message: %s", got)
	}
}

func TestMCPRequestObserverRecognizesLegacyInitialization(t *testing.T) {
	const body = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"legacy-client","version":"0.9.0"}}}`

	logs := captureMCPObservationLogs(t)
	handler := mcpRequestObserver(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	got := logs.String()
	for _, want := range []string{
		`method="initialize"`,
		`protocol="2025-11-25"`,
		`era="legacy"`,
		`lifecycle="initialization"`,
		`capabilities="legacy"`,
		`client_name="legacy-client"`,
		`client_version="0.9.0"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("observation log missing %q: %s", want, got)
		}
	}
}

func TestMCPRequestObserverMarksRoutingMetadataMismatch(t *testing.T) {
	const body = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_media","arguments":{},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"modern-client","version":"1.0.0"},"io.modelcontextprotocol/clientCapabilities":{}}}}`

	logs := captureMCPObservationLogs(t)
	handler := mcpRequestObserver(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", "request_media")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	got := logs.String()
	if !strings.Contains(got, "metadata_mismatch=true") {
		t.Fatalf("observation log did not report Mcp-Name/body disagreement: %s", got)
	}
	if !strings.Contains(got, `target="search_media"`) {
		t.Fatalf("observation log did not retain the body source of truth: %s", got)
	}
}

func TestMCPRequestObserverDoesNotLogUnsafeClientMetadata(t *testing.T) {
	const body = "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\",\"params\":{\"_meta\":{\"io.modelcontextprotocol/protocolVersion\":\"2026-07-28\",\"io.modelcontextprotocol/clientInfo\":{\"name\":\"client-name-secret\\nforged-log\",\"version\":\"1.0.0\"},\"io.modelcontextprotocol/clientCapabilities\":{}}}}"

	logs := captureMCPObservationLogs(t)
	handler := mcpRequestObserver(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Mcp-Method", "tools/list")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	got := logs.String()
	if strings.Contains(got, "client-name-secret") || strings.Contains(got, "forged-log") {
		t.Fatalf("observation log retained unsafe client metadata: %s", got)
	}
	if !strings.Contains(got, `client_version="1.0.0"`) {
		t.Fatalf("observation log lost independent safe client metadata: %s", got)
	}
}

func captureMCPObservationLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	oldWriter := log.Writer()
	oldFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
	})
	return &logs
}
