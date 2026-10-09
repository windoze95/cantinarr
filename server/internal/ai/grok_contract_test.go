package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/mcp"
)

// TestGrokValidationProviderContract pins the xAI request shape: the OpenAI
// wire format against the XAI base URL, bearer auth, and — because Grok
// models reason internally without an effort control — no reasoning_effort
// field plus the generous reasoning output budget.
func TestGrokValidationProviderContract(t *testing.T) {
	requests := make(chan providerRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureProviderRequest(r)
		writeOpenAITextSSE(w)
	}))
	t.Cleanup(server.Close)
	t.Setenv("XAI_BASE_URL", server.URL+"/v1")

	if err := validateAPIKeyProfile(t, credentials.AIProviderGrok, "grok-4.7"); err != nil {
		t.Fatalf("validate Grok profile: %v", err)
	}
	req := <-requests
	if req.path != "/v1/chat/completions" {
		t.Fatalf("path=%q, want /v1/chat/completions", req.path)
	}
	if got := req.header.Get("Authorization"); got != "Bearer contract-secret" {
		t.Fatalf("Authorization=%q", got)
	}
	if _, found := req.body["reasoning_effort"]; found {
		t.Fatalf("grok validation request sent reasoning_effort: %#v", req.body["reasoning_effort"])
	}
	want := openAIValidationReasoningMaxTokens
	if aiValidationMaxTokens > want {
		want = aiValidationMaxTokens
	}
	if got := int(req.body["max_completion_tokens"].(float64)); got != want {
		t.Fatalf("max_completion_tokens=%d, want %d", got, want)
	}
	if got := req.body["model"]; got != "grok-4.7" {
		t.Fatalf("model=%v", got)
	}
}

// TestGrokOAuthValidationUsesLiveBearerToken proves a grok_oauth save-time
// probe authenticates with the linked account's access token, not a stored
// API key.
func TestGrokOAuthValidationUsesLiveBearerToken(t *testing.T) {
	requests := make(chan providerRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureProviderRequest(r)
		writeGrokOAuthTextSSE(w)
	}))
	t.Cleanup(server.Close)
	t.Setenv("GROK_OAUTH_BASE_URL", server.URL+"/v1")

	h, _, database, userID := newResolverTestHandler(t)
	manager, now := newGrokTestManager(t, h, database)
	linkPersonalGrok(t, manager, now, userID)
	h.validationProbe = nil
	h.toolServer = mcp.NewToolServer(nil, nil, nil, nil)

	err := h.ValidatePersonalAISettings(context.Background(), userID, credentials.AIProfile{
		Config:            credentials.AIConfig{Provider: credentials.AIProviderGrokOAuth, Model: "grok-4.6"},
		CredentialPresent: true,
	})
	if err != nil {
		t.Fatalf("validate grok_oauth profile: %v", err)
	}
	req := <-requests
	if req.path != "/v1/responses" {
		t.Fatalf("path=%q, want /v1/responses", req.path)
	}
	if got := req.header.Get("Authorization"); got != "Bearer grok-bearer-token" {
		t.Fatalf("Authorization=%q, want the linked account's bearer token", got)
	}
	if req.header.Get("x-grok-model-override") != "grok-4.6" || req.header.Get("x-grok-conv-id") == "" || req.header.Get("x-grok-session-id") == "" {
		t.Fatalf("missing Grok Build request context headers: %#v", req.header)
	}
	if req.header.Get("x-grok-client-version") != "1.0.13" || req.header.Get("x-grok-client-identifier") != "cantinarr" {
		t.Fatal("Grok OAuth omitted proxy version metadata or lost its Cantinarr identity")
	}
	if req.body["model"] != "grok-4.6" || req.body["stream"] != true || req.body["store"] != false {
		t.Fatalf("Grok OAuth Responses payload has wrong model/stream/store: %#v", req.body)
	}
	if req.body["tool_choice"] != "none" {
		t.Fatalf("validation must carry tools with tool_choice=none; payload=%#v", req.body)
	}
	if req.body["max_output_tokens"] != float64(openAIValidationReasoningMaxTokens) {
		t.Fatalf("OAuth readiness probe lacks a bounded reasoning allowance: %v", req.body["max_output_tokens"])
	}
	if _, exists := req.body["reasoning"]; exists {
		t.Fatal("Grok OAuth must not inherit OpenAI reasoning effort controls")
	}
	tools, ok := req.body["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("validation must serialize the Grok Build function catalog: %#v", req.body["tools"])
	}
	firstTool, ok := tools[0].(map[string]any)
	if !ok || firstTool["type"] != "function" || firstTool["name"] == "" {
		t.Fatalf("Grok Build tool is not a Responses function: %#v", tools[0])
	}
}

func TestGrokOAuthSerializesFunctionCallHistoryForResponses(t *testing.T) {
	service := NewGrokOAuthService("", "grok-4.6", "conversation-1", nil)
	tools := service.responseTools([]mcp.Tool{{
		Name:        "search_movies",
		Description: "Search movie titles",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"query": map[string]any{"type": "string"}},
			"required":   []string{"query"},
			"anyOf":      []any{map[string]any{"type": "object"}},
		},
	}})
	toolJSON, err := json.Marshal(tools)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"type":"function"`, `"name":"search_movies"`, `"description":"Search movie titles"`, `"query"`} {
		if !strings.Contains(string(toolJSON), field) {
			t.Fatalf("Responses tool schema missing %s: %s", field, toolJSON)
		}
	}
	if strings.Contains(string(toolJSON), "anyOf") {
		t.Fatalf("unsupported root keyword survived Grok Build schema conversion: %s", toolJSON)
	}

	history := transcript{
		textTranscriptMessage(agentRoleUser, "find Dune"),
		{
			Role: agentRoleAssistant,
			Content: []transcriptBlock{
				{Type: blockTypeText, Text: "Searching now."},
				{Type: blockTypeToolUse, ID: "call_123", Name: "search_movies", Input: json.RawMessage(`{"query":"Dune"}`)},
			},
		},
		{Role: agentRoleUser, Content: []transcriptBlock{{Type: blockTypeToolResult, ToolUseID: "call_123", Name: "search_movies", Content: "Found Dune."}}},
	}
	items := grokOAuthInputItems(history)
	if len(items) != 4 {
		t.Fatalf("serialized Responses history items = %d, want user, assistant text/call, and tool result", len(items))
	}
	inputJSON, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"find Dune"`, `"Searching now."`, `"call_id":"call_123"`, `"name":"search_movies"`, `"Found Dune."`} {
		if !strings.Contains(string(inputJSON), field) {
			t.Fatalf("Responses history missing %s: %s", field, inputJSON)
		}
	}
}

func TestGrokOAuthVersionGateForBothModelsAndExecutionPaths(t *testing.T) {
	for _, model := range []string{"grok-4.6", "grok-4.5"} {
		for _, interactive := range []bool{false, true} {
			t.Run(model+map[bool]string{false: "/next-turn", true: "/chat"}[interactive], func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("x-grok-client-version") != "1.0.13" {
						w.WriteHeader(http.StatusUpgradeRequired)
						_, _ = io.WriteString(w, `{"error":{"message":"Your Grok CLI version (none) is outdated"}}`)
						return
					}
					if r.URL.Path != "/v1/responses" || r.Header.Get("x-grok-model-override") != model || r.Header.Get("x-grok-client-identifier") != "cantinarr" {
						t.Error("wrong OAuth endpoint, model, or client identity")
					}
					writeGrokOAuthTextSSE(w)
				}))
				t.Cleanup(server.Close)
				t.Setenv("GROK_OAUTH_BASE_URL", server.URL+"/v1")
				service := NewGrokOAuthService("contract-secret", model, "", mcp.NewToolServer(nil, nil, nil, nil))
				var err error
				if interactive {
					_, err = service.SendMessage(context.Background(), transcript{textTranscriptMessage(agentRoleUser, "Say OK")}, ChatContext{UserID: 1, Role: auth.RoleAdmin}, StreamCallbacks{})
				} else {
					_, err = service.NextTurn(context.Background(), validationProbeParams(nil))
				}
				if err != nil {
					t.Fatalf("OAuth request failed proxy version gate: %v", err)
				}
			})
		}
	}
}

func TestGrokOAuthVersionGateFailureIsActionableAndSafe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUpgradeRequired)
		_, _ = io.WriteString(w, `{"error":{"message":"Upgrade client; access_token=private-test-token"}}`)
	}))
	t.Cleanup(server.Close)
	t.Setenv("GROK_OAUTH_BASE_URL", server.URL+"/v1")
	_, err := NewGrokOAuthService("contract-secret", "grok-4.6", "", nil).NextTurn(context.Background(), validationProbeParams(nil))
	if err == nil || classifyAIValidationFailure(err) != AIValidationFailureUpgradeRequired {
		t.Fatalf("version rejection was misclassified: %v", err)
	}
	message := AIValidationUserMessage(newAIValidationFailure(err))
	if !strings.Contains(message, "Update Cantinarr") || strings.Contains(message, "private-test-token") || !strings.Contains(message, "Nothing was saved") {
		t.Fatalf("unsafe or unhelpful upgrade message: %q", message)
	}
}

func writeGrokOAuthTextSSE(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	// Comment-only keepalives carry no JSON event and must not become an
	// unexpected-end-of-JSON failure.
	_, _ = io.WriteString(w, ": keepalive\n\n\nevent: ping\ndata: \n\n")
	_, _ = io.WriteString(w, `data: {"type":"response.output_text.delta","delta":"OK"}`+"\n\n")
	_, _ = io.WriteString(w, `data: {"type":"response.completed","response":{"id":"resp_test","object":"response","status":"completed","model":"grok-4.6","output":[{"id":"msg_test","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"OK","annotations":[]}]}],"usage":{"input_tokens":7,"input_tokens_details":{"cached_tokens":0},"output_tokens":1,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":8}}}`+"\n\n")
}

func TestGrokOAuthStreamStillRejectsMalformedNonemptyData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, ": keepalive\n\ndata: {broken\n\n")
	}))
	t.Cleanup(server.Close)
	t.Setenv("GROK_OAUTH_BASE_URL", server.URL+"/v1")
	_, err := NewGrokOAuthService("contract-secret", "grok-4.6", "", nil).NextTurn(context.Background(), validationProbeParams(nil))
	if err == nil {
		t.Fatal("malformed nonempty JSON was silently ignored")
	}
}
