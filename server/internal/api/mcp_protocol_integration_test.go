package api

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests drive the MCP protocol layer (internal/mcpserver) over the wire
// through the SAME router mount production uses (/mcp behind MCPAuthMiddleware
// + RequirePermission(mcp:access)), reusing newRBACRouterHarness. The risks
// under test are the audit's cross-user attribution concerns: the identity a
// tool observes must come from the caller's token on every request, sessions
// must not leak identity into each other, and unauthenticated or wrong-audience
// tokens must never reach the tool layer.

const mcpTestResource = "http://cantinarr.test/mcp"

// mintMCPAccessToken walks the real OAuth machinery (client registration,
// PKCE-bound authorization code, code exchange) to obtain an access token whose
// audience is the /mcp resource — the only token shape MCPAuthMiddleware admits.
func mintMCPAccessToken(t *testing.T, harness *rbacRouterHarness, userID int64) string {
	t.Helper()
	const redirectURI = "http://localhost/callback"
	client, err := harness.authService.RegisterOAuthClient("MCP Protocol Test", []string{redirectURI})
	if err != nil {
		t.Fatalf("register oauth client: %v", err)
	}
	verifier := "mcp-protocol-test-verifier-0123456789abcdefghij"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	code, err := harness.authService.CreateOAuthAuthorizationCode(client, userID, redirectURI, challenge, mcpTestResource, "")
	if err != nil {
		t.Fatalf("create authorization code: %v", err)
	}
	tokens, err := harness.authService.ExchangeOAuthAuthorizationCode(client.ClientID, code, redirectURI, verifier, mcpTestResource)
	if err != nil {
		t.Fatalf("exchange authorization code: %v", err)
	}
	return tokens.AccessToken
}

func postMCP(router http.Handler, token, sessionID, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, mcpTestResource, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if sessionID != "" {
		request.Header.Set("Mcp-Session-Id", sessionID)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// decodeJSONRPC returns the decoded JSON-RPC envelope from either a plain JSON
// response or the final data frame of an SSE response.
func decodeJSONRPC(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	payload := recorder.Body.String()
	if strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/event-stream") {
		var last string
		scanner := bufio.NewScanner(recorder.Body)
		scanner.Buffer(make([]byte, 64<<10), 8<<20)
		for scanner.Scan() {
			if line := scanner.Text(); strings.HasPrefix(line, "data: ") {
				last = strings.TrimPrefix(line, "data: ")
			}
		}
		payload = last
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		t.Fatalf("decode JSON-RPC payload %q: %v", payload, err)
	}
	return envelope
}

func jsonRPCResult(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	envelope := decodeJSONRPC(t, recorder)
	if envelope["error"] != nil {
		t.Fatalf("JSON-RPC error: %v", envelope["error"])
	}
	result, ok := envelope["result"].(map[string]any)
	if !ok {
		t.Fatalf("JSON-RPC result missing: %v", envelope)
	}
	return result
}

func initializeMCPSession(t *testing.T, router http.Handler, token string) string {
	t.Helper()
	recorder := postMCP(router, token, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"protocol-test","version":"0.0.0"}}}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("initialize status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	result := jsonRPCResult(t, recorder)
	serverInfo, _ := result["serverInfo"].(map[string]any)
	if serverInfo["name"] != "cantinarr" {
		t.Fatalf("serverInfo = %v", result["serverInfo"])
	}
	sessionID := recorder.Header().Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatal("initialize did not return an Mcp-Session-Id")
	}
	return sessionID
}

func listMCPTools(t *testing.T, router http.Handler, token, sessionID string) map[string]bool {
	t.Helper()
	recorder := postMCP(router, token, sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("tools/list status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	result := jsonRPCResult(t, recorder)
	tools, _ := result["tools"].([]any)
	names := make(map[string]bool, len(tools))
	for _, tool := range tools {
		entry, _ := tool.(map[string]any)
		name, _ := entry["name"].(string)
		names[name] = true
	}
	return names
}

// callMCPTool returns the text content of a tools/call round trip.
func callMCPTool(t *testing.T, router http.Handler, token, sessionID, tool, arguments string) string {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + arguments + `}}`
	recorder := postMCP(router, token, sessionID, body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("tools/call status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	result := jsonRPCResult(t, recorder)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("tools/call returned no content: %v", result)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	return text
}

func seedRequestLogRow(t *testing.T, harness *rbacRouterHarness, userID int64, title string) {
	t.Helper()
	if _, err := harness.database.Exec(
		`INSERT INTO request_log (user_id, tmdb_id, media_type, title, status) VALUES (?, 550, 'movie', ?, 'requested')`,
		userID, title,
	); err != nil {
		t.Fatalf("seed request_log: %v", err)
	}
}

func TestMCPEndpointRejectsMissingAndWrongAudienceTokens(t *testing.T) {
	harness := newRBACRouterHarness(t, false)
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`

	if recorder := postMCP(harness.router, "", "", initialize); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous /mcp status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
	}

	// A valid interactive app session token has no /mcp audience: the MCP
	// boundary must refuse it rather than admit any authenticated bearer.
	if recorder := postMCP(harness.router, harness.adminToken, "", initialize); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("audience-free session token /mcp status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
	}

	if recorder := postMCP(harness.router, "not-a-token", "", initialize); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("garbage token /mcp status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
	}

	// An MCP-audience token really is admitted (the 401s above are about
	// credentials, not a broken mount).
	adminMCP := mintMCPAccessToken(t, harness, harness.adminID)
	if recorder := postMCP(harness.router, adminMCP, "", initialize); recorder.Code != http.StatusOK {
		t.Fatalf("mcp-audience token /mcp status = %d, body=%s", recorder.Code, recorder.Body.String())
	}

	// Losing the token mid-session is a 401 too: a live session id alone must
	// never keep the connection authenticated.
	sessionID := initializeMCPSession(t, harness.router, adminMCP)
	if recorder := postMCP(harness.router, "", sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("tokenless call on live session status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMCPProtocolRoundTripPropagatesPerTokenIdentity(t *testing.T) {
	harness := newRBACRouterHarness(t, false)
	seedRequestLogRow(t, harness, harness.adminID, "Admin Sentinel Movie")
	seedRequestLogRow(t, harness, harness.requesterID, "Requester Sentinel Movie")

	adminToken := mintMCPAccessToken(t, harness, harness.adminID)
	requesterToken := mintMCPAccessToken(t, harness, harness.requesterID)

	adminSession := initializeMCPSession(t, harness.router, adminToken)
	requesterSession := initializeMCPSession(t, harness.router, requesterToken)
	if adminSession == requesterSession {
		t.Fatal("two initializations shared one session id")
	}

	// tools/list is RBAC-filtered per authenticated role over the wire.
	adminTools := listMCPTools(t, harness.router, adminToken, adminSession)
	if !adminTools["get_queue"] || !adminTools["search_movies"] || !adminTools["list_my_requests"] {
		t.Fatalf("admin tools = %v, want operational + discovery tools", adminTools)
	}
	requesterTools := listMCPTools(t, harness.router, requesterToken, requesterSession)
	if requesterTools["get_queue"] || requesterTools["grab_release"] {
		t.Fatalf("requester tool list leaked operational tools: %v", requesterTools)
	}
	if !requesterTools["search_movies"] || !requesterTools["list_my_requests"] {
		t.Fatalf("requester tools = %v, want discovery/request tools", requesterTools)
	}

	// Each caller's identity reaches the tool layer: list_my_requests returns
	// exactly the caller's own rows.
	adminText := callMCPTool(t, harness.router, adminToken, adminSession, "list_my_requests", `{}`)
	if !strings.Contains(adminText, "Admin Sentinel Movie") || strings.Contains(adminText, "Requester Sentinel Movie") {
		t.Fatalf("admin list_my_requests = %q, want only the admin's rows", adminText)
	}
	requesterText := callMCPTool(t, harness.router, requesterToken, requesterSession, "list_my_requests", `{}`)
	if !strings.Contains(requesterText, "Requester Sentinel Movie") || strings.Contains(requesterText, "Admin Sentinel Movie") {
		t.Fatalf("requester list_my_requests = %q, want only the requester's rows", requesterText)
	}

	// Cross-user attribution: replaying the ADMIN's session id with the
	// REQUESTER's token must attribute the call to the requester. The admin's
	// data must never appear.
	crossText := callMCPTool(t, harness.router, requesterToken, adminSession, "list_my_requests", `{}`)
	if strings.Contains(crossText, "Admin Sentinel Movie") {
		t.Fatalf("requester token on admin session leaked admin data: %q", crossText)
	}
	if !strings.Contains(crossText, "Requester Sentinel Movie") {
		t.Fatalf("requester token on admin session lost its own identity: %q", crossText)
	}

	// Call-time enforcement through the wire: a requester invoking an admin
	// tool by name receives the server-side denial, not tool output.
	denial := callMCPTool(t, harness.router, requesterToken, requesterSession, "get_queue", `{"media_type":"movie"}`)
	if denial != "This action is not permitted for your role." {
		t.Fatalf("requester get_queue over MCP = %q, want the role denial", denial)
	}
}
