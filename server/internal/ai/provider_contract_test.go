package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	openai "github.com/openai/openai-go/v3"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/codexapp"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/mcp"
	"google.golang.org/genai"
)

type providerRequest struct {
	path   string
	query  string
	header http.Header
	body   map[string]any
}

func captureProviderRequest(r *http.Request) providerRequest {
	body, _ := io.ReadAll(r.Body)
	var decoded map[string]any
	_ = json.Unmarshal(body, &decoded)
	return providerRequest{
		path: r.URL.Path, query: r.URL.RawQuery, header: r.Header.Clone(), body: decoded,
	}
}

func validateAPIKeyProfile(t *testing.T, provider, model string) error {
	t.Helper()
	h, _, _, userID := newResolverTestHandler(t)
	h.validationProbe = nil
	return h.ValidatePersonalAISettings(context.Background(), userID, credentials.AIProfile{
		Config: credentials.AIConfig{Provider: provider, Model: model},
		APIKey: "contract-secret",
	})
}

func writeAnthropicTextSSE(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, "event: message_start\n")
	_, _ = io.WriteString(w, `data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"claude-opus-4-8","content":[],"stop_reason":null,"usage":{"input_tokens":7,"output_tokens":0}}}`+"\n\n")
	_, _ = io.WriteString(w, "event: content_block_start\n")
	_, _ = io.WriteString(w, `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"OK"}}`+"\n\n")
	_, _ = io.WriteString(w, "event: content_block_stop\n")
	_, _ = io.WriteString(w, `data: {"type":"content_block_stop","index":0}`+"\n\n")
	_, _ = io.WriteString(w, "event: message_delta\n")
	_, _ = io.WriteString(w, `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`+"\n\n")
	_, _ = io.WriteString(w, "event: message_stop\n")
	_, _ = io.WriteString(w, `data: {"type":"message_stop"}`+"\n\n")
}

func writeOpenAITextSSE(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, `data: {"id":"chatcmpl_test","object":"chat.completion.chunk","created":1,"model":"gpt-5.5","choices":[{"index":0,"delta":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`+"\n\n")
	_, _ = io.WriteString(w, `data: {"id":"chatcmpl_test","object":"chat.completion.chunk","created":1,"model":"gpt-5.5","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":1,"total_tokens":8}}`+"\n\n")
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

func writeOpenAILengthOnlySSE(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, `data: {"id":"chatcmpl_test","object":"chat.completion.chunk","created":1,"model":"custom-reasoner","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":"length"}]}`+"\n\n")
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

func writeOpenAIAPIError(w http.ResponseWriter, status int, code, param, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
		"message": message,
		"type":    "invalid_request_error",
		"param":   param,
		"code":    code,
	}})
}

func writeGeminiTextSSE(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"OK"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":1,"thoughtsTokenCount":2,"totalTokenCount":10}}`+"\n\n")
}

func writeAnthropicToolSSE(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, "event: message_start\n")
	_, _ = io.WriteString(w, `data: {"type":"message_start","message":{"id":"msg_tool","type":"message","role":"assistant","model":"claude-opus-4-8","content":[],"stop_reason":null,"usage":{"input_tokens":7,"output_tokens":0}}}`+"\n\n")
	_, _ = io.WriteString(w, "event: content_block_start\n")
	_, _ = io.WriteString(w, `data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"search_movies","input":{}}}`+"\n\n")
	_, _ = io.WriteString(w, "event: content_block_stop\n")
	_, _ = io.WriteString(w, `data: {"type":"content_block_stop","index":0}`+"\n\n")
	_, _ = io.WriteString(w, "event: message_delta\n")
	_, _ = io.WriteString(w, `data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":1}}`+"\n\n")
	_, _ = io.WriteString(w, "event: message_stop\n")
	_, _ = io.WriteString(w, `data: {"type":"message_stop"}`+"\n\n")
}

func writeOpenAIToolSSE(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, `data: {"id":"chatcmpl_tool","object":"chat.completion.chunk","created":1,"model":"gpt-5.5","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"search_movies","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`+"\n\n")
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

func writeGeminiToolSSE(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"search_movies","args":{}}}]} ,"finishReason":"STOP"}]}`+"\n\n")
}

func revokedInteractiveToolServer() *mcp.ToolServer {
	toolServer := mcp.NewToolServer(nil, nil, nil, nil)
	toolServer.SetCallAuthorizer(func(context.Context, mcp.CallContext) (string, error) {
		return "", errors.New("device revoked")
	})
	return toolServer
}

func assertRevokedToolCallbacks(t *testing.T, starts []string, ends []bool) {
	t.Helper()
	if len(starts) != 1 || starts[0] != "search_movies" {
		t.Fatalf("tool starts = %v, want search_movies", starts)
	}
	if len(ends) != 1 || ends[0] {
		t.Fatalf("tool ends = %v, want one failed callback", ends)
	}
}

func assertNoRevocationToolResult(t *testing.T, history transcript) {
	t.Helper()
	for _, message := range history {
		for _, block := range message.Content {
			if block.Type == blockTypeToolResult {
				t.Fatalf("authorization failure became a model-visible tool result: %#v", block)
			}
		}
	}
}

// AUTH-027: Provider loops stop immediately when dispatch authorization is revoked.
func TestInteractiveAuthorizationRevocationStopsProviderLoops(t *testing.T) {
	history := transcript{textTranscriptMessage(agentRoleUser, "find Dune")}
	chatCtx := ChatContext{UserID: 7, Role: auth.RoleUser, DeviceID: "device-7", RequireSharedAI: true}

	t.Run("Anthropic", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			writeAnthropicToolSSE(w)
		}))
		t.Cleanup(server.Close)
		t.Setenv("ANTHROPIC_BASE_URL", server.URL)
		var starts []string
		var ends []bool
		finalHistory, err := NewService("secret", "claude-opus-4-8", revokedInteractiveToolServer()).SendMessage(
			context.Background(), history, chatCtx, StreamCallbacks{
				OnToolStart: func(name, _ string) { starts = append(starts, name) },
				OnToolEnd:   func(_ string, ok bool) { ends = append(ends, ok) },
			},
		)
		if !errors.Is(err, mcp.ErrToolAuthorization) {
			t.Fatalf("SendMessage error = %v, want ErrToolAuthorization", err)
		}
		if got := requests.Load(); got != 1 {
			t.Fatalf("provider requests = %d, authorization failure continued the model loop", got)
		}
		assertRevokedToolCallbacks(t, starts, ends)
		assertNoRevocationToolResult(t, finalHistory)
	})

	t.Run("OpenAI", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			writeOpenAIToolSSE(w)
		}))
		t.Cleanup(server.Close)
		t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")
		var starts []string
		var ends []bool
		finalHistory, err := NewOpenAIService("secret", "gpt-5.5", revokedInteractiveToolServer()).SendMessage(
			context.Background(), history, chatCtx, StreamCallbacks{
				OnToolStart: func(name, _ string) { starts = append(starts, name) },
				OnToolEnd:   func(_ string, ok bool) { ends = append(ends, ok) },
			},
		)
		if !errors.Is(err, mcp.ErrToolAuthorization) {
			t.Fatalf("SendMessage error = %v, want ErrToolAuthorization", err)
		}
		if got := requests.Load(); got != 1 {
			t.Fatalf("provider requests = %d, authorization failure continued the model loop", got)
		}
		assertRevokedToolCallbacks(t, starts, ends)
		assertNoRevocationToolResult(t, finalHistory)
	})

	t.Run("Gemini", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			writeGeminiToolSSE(w)
		}))
		t.Cleanup(server.Close)
		t.Setenv("GOOGLE_GEMINI_BASE_URL", server.URL)
		var starts []string
		var ends []bool
		finalHistory, err := NewGeminiService("secret", "gemini-contract", revokedInteractiveToolServer()).SendMessage(
			context.Background(), history, chatCtx, StreamCallbacks{
				OnToolStart: func(name, _ string) { starts = append(starts, name) },
				OnToolEnd:   func(_ string, ok bool) { ends = append(ends, ok) },
			},
		)
		if !errors.Is(err, mcp.ErrToolAuthorization) {
			t.Fatalf("SendMessage error = %v, want ErrToolAuthorization", err)
		}
		if got := requests.Load(); got != 1 {
			t.Fatalf("provider requests = %d, authorization failure continued the model loop", got)
		}
		assertRevokedToolCallbacks(t, starts, ends)
		assertNoRevocationToolResult(t, finalHistory)
	})
}

func TestAnthropicValidationProviderContract(t *testing.T) {
	requests := make(chan providerRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureProviderRequest(r)
		writeAnthropicTextSSE(w)
	}))
	t.Cleanup(server.Close)
	t.Setenv("ANTHROPIC_BASE_URL", server.URL)

	if err := validateAPIKeyProfile(t, credentials.AIProviderAnthropic, "claude-opus-4-8"); err != nil {
		t.Fatalf("validate Anthropic profile: %v", err)
	}
	req := <-requests
	if req.path != "/v1/messages" {
		t.Fatalf("path=%q, want /v1/messages", req.path)
	}
	if got := req.header.Get("X-Api-Key"); got != "contract-secret" {
		t.Fatalf("x-api-key=%q", got)
	}
	if got := req.header.Get("Authorization"); got != "" {
		t.Fatalf("unexpected Authorization header %q", got)
	}
	if got := int(req.body["max_tokens"].(float64)); got != aiValidationMaxTokens {
		t.Fatalf("max_tokens=%d, want %d", got, aiValidationMaxTokens)
	}
	if got := req.body["thinking"].(map[string]any)["type"]; got != "disabled" {
		t.Fatalf("thinking.type=%v, want disabled", got)
	}
	if got := req.body["tool_choice"].(map[string]any)["type"]; got != "none" {
		t.Fatalf("tool_choice.type=%v, want none", got)
	}
	if _, found := req.body["tools"]; found {
		t.Fatal("validation request unexpectedly included tools")
	}
}

func TestAnthropicFableValidationUsesAlwaysAdaptiveContract(t *testing.T) {
	requests := make(chan providerRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureProviderRequest(r)
		writeAnthropicTextSSE(w)
	}))
	t.Cleanup(server.Close)
	t.Setenv("ANTHROPIC_BASE_URL", server.URL)

	if err := validateAPIKeyProfile(t, credentials.AIProviderAnthropic, "claude-fable-5"); err != nil {
		t.Fatalf("validate Anthropic Fable profile: %v", err)
	}
	req := <-requests
	if _, found := req.body["thinking"]; found {
		t.Fatalf("always-adaptive Fable request must omit unsupported thinking override: %#v", req.body["thinking"])
	}
	if got := int(req.body["max_tokens"].(float64)); got != anthropicValidationReasoningMaxTokens {
		t.Fatalf("max_tokens=%d, want %d", got, anthropicValidationReasoningMaxTokens)
	}
}

func TestOpenAIValidationProviderContract(t *testing.T) {
	requests := make(chan providerRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureProviderRequest(r)
		writeOpenAITextSSE(w)
	}))
	t.Cleanup(server.Close)
	t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")

	if err := validateAPIKeyProfile(t, credentials.AIProviderOpenAI, "gpt-5.5"); err != nil {
		t.Fatalf("validate OpenAI profile: %v", err)
	}
	req := <-requests
	if req.path != "/v1/chat/completions" {
		t.Fatalf("path=%q, want /v1/chat/completions", req.path)
	}
	if got := req.header.Get("Authorization"); got != "Bearer contract-secret" {
		t.Fatalf("Authorization=%q", got)
	}
	if got := req.header.Get("X-Api-Key"); got != "" {
		t.Fatalf("unexpected x-api-key header %q", got)
	}
	if got := req.header.Get("X-Goog-Api-Key"); got != "" {
		t.Fatalf("unexpected x-goog-api-key header %q", got)
	}
	if got := int(req.body["max_completion_tokens"].(float64)); got != aiValidationMaxTokens {
		t.Fatalf("max_completion_tokens=%d, want %d", got, aiValidationMaxTokens)
	}
	if got := req.body["reasoning_effort"]; got != "none" {
		t.Fatalf("reasoning_effort=%v, want none", got)
	}
	if _, found := req.body["tool_choice"]; found {
		t.Fatalf("tool-free validation request unexpectedly included tool_choice: %#v", req.body)
	}
	if got := req.body["stream_options"].(map[string]any)["include_usage"]; got != true {
		t.Fatalf("stream_options.include_usage=%v, want true", got)
	}
	if _, found := req.body["tools"]; found {
		t.Fatal("validation request unexpectedly included tools")
	}
}

func TestOpenAIInteractiveFinalIterationKeepsToolChoiceWithTools(t *testing.T) {
	tools := toOpenAITools(mcp.NewToolServer(nil, nil, nil, nil).GetToolsForRole(auth.RoleUser))
	if len(tools) == 0 {
		t.Fatal("user tool catalog is empty")
	}
	params := openAIInteractiveParams(
		"gpt-5.5",
		[]openai.ChatCompletionMessageParamUnion{openai.UserMessage("hello")},
		tools,
		true,
	)
	body, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := decoded["tool_choice"]; got != "none" {
		t.Fatalf("tool_choice=%v, want none", got)
	}
	if got, ok := decoded["tools"].([]any); !ok || len(got) == 0 {
		t.Fatalf("final interactive request omitted tools: %#v", decoded)
	}
}

func TestOpenAIToolSchemasUseSupportedObjectRoots(t *testing.T) {
	adminTools := mcp.NewToolServer(nil, nil, nil, nil).GetToolsForRole(auth.RoleAdmin)
	params := openAIInteractiveParams(
		"gpt-5.5",
		[]openai.ChatCompletionMessageParamUnion{openai.UserMessage("hello")},
		toOpenAITools(adminTools),
		false,
	)
	body, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Tools []struct {
			Function struct {
				Name       string         `json:"name"`
				Parameters map[string]any `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	seenGrabRelease := false
	for _, tool := range decoded.Tools {
		if tool.Function.Name == "grab_release" {
			seenGrabRelease = true
		}
		if tool.Function.Parameters["type"] != "object" {
			t.Errorf("tool %q parameters must have type object", tool.Function.Name)
		}
		for _, keyword := range []string{"oneOf", "anyOf", "allOf", "enum", "const", "not"} {
			if _, found := tool.Function.Parameters[keyword]; found {
				t.Errorf("tool %q has unsupported top-level %s", tool.Function.Name, keyword)
			}
		}
	}
	if !seenGrabRelease {
		t.Fatal("serialized OpenAI request omitted grab_release")
	}
	// The sanitizer must strip a copy, never the canonical schema: Gemini and
	// native MCP clients still rely on grab_release's root oneOf.
	for _, tool := range adminTools {
		if tool.Name != "grab_release" {
			continue
		}
		if _, found := tool.InputSchema["oneOf"]; !found {
			t.Error("toOpenAITools mutated the canonical grab_release schema: root oneOf removed")
		}
	}
}

func TestAnthropicToolSchemasCarryFullRoots(t *testing.T) {
	adminTools := mcp.NewToolServer(nil, nil, nil, nil).GetToolsForRole(auth.RoleAdmin)
	body, err := json.Marshal(toSDKTools(adminTools))
	if err != nil {
		t.Fatal(err)
	}
	var decoded []struct {
		Name        string         `json:"name"`
		InputSchema map[string]any `json:"input_schema"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	seenGrabRelease := false
	for _, tool := range decoded {
		if tool.InputSchema["type"] != "object" {
			t.Errorf("tool %q input_schema must have type object", tool.Name)
		}
		if tool.Name != "grab_release" {
			continue
		}
		seenGrabRelease = true
		if oneOf, ok := tool.InputSchema["oneOf"].([]any); !ok || len(oneOf) != 3 {
			t.Errorf("grab_release input_schema oneOf = %#v, want the three media_type branches", tool.InputSchema["oneOf"])
		}
		if props, ok := tool.InputSchema["properties"].(map[string]any); !ok || len(props) == 0 {
			t.Error("grab_release input_schema lost its properties")
		}
		if req, ok := tool.InputSchema["required"].([]any); !ok || len(req) == 0 {
			t.Error("grab_release input_schema lost its required list")
		}
	}
	if !seenGrabRelease {
		t.Fatal("serialized Anthropic tools omitted grab_release")
	}
}

func TestOpenAIValidationOmitsUnsupportedReasoningField(t *testing.T) {
	params := openAINextTurnParams("gpt-4.1", TurnParams{
		DisableReasoning: true,
		ForceNoTools:     true,
		MaxTokens:        aiValidationMaxTokens,
	})
	body, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "reasoning_effort") {
		t.Fatalf("gpt-4.1 request contains unsupported reasoning field: %s", body)
	}
}

func TestOpenAIValidationReasoningCapabilityMatrix(t *testing.T) {
	tests := []struct {
		model        string
		wantEfforts  []string
		wantFirstMax int64
	}{
		{model: "gpt-5.5", wantEfforts: []string{"none", "low", "", ""}, wantFirstMax: aiValidationMaxTokens},
		{model: "gpt-5", wantEfforts: []string{"minimal", "low", "", ""}, wantFirstMax: openAIValidationReasoningMaxTokens},
		{model: "gpt-5-2025-08-07", wantEfforts: []string{"minimal", "low", "", ""}, wantFirstMax: openAIValidationReasoningMaxTokens},
		{model: "o3", wantEfforts: []string{"low", "", ""}, wantFirstMax: openAIValidationReasoningMaxTokens},
		{model: "o4-mini", wantEfforts: []string{"low", "", ""}, wantFirstMax: openAIValidationReasoningMaxTokens},
		{model: "ft:gpt-5:org:validation", wantEfforts: []string{"minimal", "low", "", ""}, wantFirstMax: openAIValidationReasoningMaxTokens},
		{model: "ft:gpt-5.5:org:validation", wantEfforts: []string{"none", "low", "", ""}, wantFirstMax: aiValidationMaxTokens},
		{model: "ft:o4-mini:org:validation", wantEfforts: []string{"low", "", ""}, wantFirstMax: openAIValidationReasoningMaxTokens},
		{model: "gpt-4.1", wantEfforts: []string{""}, wantFirstMax: aiValidationMaxTokens},
		{model: "ft:gpt-4.1:org:validation", wantEfforts: []string{""}, wantFirstMax: aiValidationMaxTokens},
		{model: "private-deployment-alias", wantEfforts: []string{"none", "low", "", ""}, wantFirstMax: aiValidationMaxTokens},
	}
	for _, test := range tests {
		t.Run(test.model, func(t *testing.T) {
			attempts := openAIReasoningAttempts(openai.ChatModel(test.model), TurnParams{
				DisableReasoning: true,
				MaxTokens:        aiValidationMaxTokens,
			})
			gotEfforts := make([]string, len(attempts))
			for i, attempt := range attempts {
				gotEfforts[i] = string(attempt.effort)
			}
			if !slices.Equal(gotEfforts, test.wantEfforts) {
				t.Fatalf("efforts=%q, want %q", gotEfforts, test.wantEfforts)
			}
			if got := attempts[0].maxTokens; got != test.wantFirstMax {
				t.Fatalf("first max_completion_tokens=%d, want %d", got, test.wantFirstMax)
			}
		})
	}
}

func TestOpenAIValidationKnownReasoningModelsUseProductionContract(t *testing.T) {
	tests := []struct {
		model      string
		wantEffort string
		wantMax    int
	}{
		{model: "gpt-5", wantEffort: "minimal", wantMax: openAIValidationReasoningMaxTokens},
		{model: "o3", wantEffort: "low", wantMax: openAIValidationReasoningMaxTokens},
		{model: "o4-mini", wantEffort: "low", wantMax: openAIValidationReasoningMaxTokens},
		{model: "ft:gpt-5:org:validation", wantEffort: "minimal", wantMax: openAIValidationReasoningMaxTokens},
		{model: "ft:gpt-4.1:org:validation", wantMax: aiValidationMaxTokens},
	}
	for _, test := range tests {
		t.Run(test.model, func(t *testing.T) {
			requests := make(chan providerRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- captureProviderRequest(r)
				writeOpenAITextSSE(w)
			}))
			t.Cleanup(server.Close)
			t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")

			if err := validateAPIKeyProfile(t, credentials.AIProviderOpenAI, test.model); err != nil {
				t.Fatalf("validate OpenAI profile: %v", err)
			}
			req := <-requests
			gotEffort, found := req.body["reasoning_effort"]
			if test.wantEffort == "" {
				if found {
					t.Fatalf("reasoning_effort=%v, want omitted", gotEffort)
				}
			} else if !found || gotEffort != test.wantEffort {
				t.Fatalf("reasoning_effort=%v, want %q", gotEffort, test.wantEffort)
			}
			if got := int(req.body["max_completion_tokens"].(float64)); got != test.wantMax {
				t.Fatalf("max_completion_tokens=%d, want %d", got, test.wantMax)
			}
		})
	}
}

func TestOpenAIValidationCustomReasoningAliasFallsBackToLow(t *testing.T) {
	requests := make(chan providerRequest, 2)
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureProviderRequest(r)
		if count.Add(1) == 1 {
			writeOpenAIAPIError(w, http.StatusBadRequest, "unsupported_value", "reasoning_effort", "unsupported value; supported values: low, medium, high")
			return
		}
		writeOpenAITextSSE(w)
	}))
	t.Cleanup(server.Close)
	t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")

	if err := validateAPIKeyProfile(t, credentials.AIProviderOpenAI, "private-reasoning-deployment"); err != nil {
		t.Fatalf("validate custom reasoning profile: %v", err)
	}
	first, second := <-requests, <-requests
	if got := first.body["reasoning_effort"]; got != "none" {
		t.Fatalf("first reasoning_effort=%v, want none", got)
	}
	if got := second.body["reasoning_effort"]; got != "low" {
		t.Fatalf("fallback reasoning_effort=%v, want low", got)
	}
	if got := int(second.body["max_completion_tokens"].(float64)); got != openAIValidationReasoningMaxTokens {
		t.Fatalf("fallback max_completion_tokens=%d, want %d", got, openAIValidationReasoningMaxTokens)
	}
}

func TestOpenAIValidationRetriesHiddenReasoningBudgetExhaustion(t *testing.T) {
	requests := make(chan providerRequest, 2)
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureProviderRequest(r)
		if count.Add(1) == 1 {
			writeOpenAILengthOnlySSE(w)
			return
		}
		writeOpenAITextSSE(w)
	}))
	t.Cleanup(server.Close)
	t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")

	if err := validateAPIKeyProfile(t, credentials.AIProviderOpenAI, "private-reasoning-deployment"); err != nil {
		t.Fatalf("validate after hidden reasoning exhaustion: %v", err)
	}
	first, second := <-requests, <-requests
	if got := int(first.body["max_completion_tokens"].(float64)); got != aiValidationMaxTokens {
		t.Fatalf("first max_completion_tokens=%d, want %d", got, aiValidationMaxTokens)
	}
	if got := second.body["reasoning_effort"]; got != "low" {
		t.Fatalf("retry reasoning_effort=%v, want low", got)
	}
	if got := int(second.body["max_completion_tokens"].(float64)); got != openAIValidationReasoningMaxTokens {
		t.Fatalf("retry max_completion_tokens=%d, want %d", got, openAIValidationReasoningMaxTokens)
	}
}

func TestOpenAIValidationCustomNonReasoningFallback(t *testing.T) {
	requests := make(chan providerRequest, 3)
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := captureProviderRequest(r)
		requests <- request
		switch count.Add(1) {
		case 1:
			writeOpenAIAPIError(w, http.StatusBadRequest, "unsupported_parameter", "reasoning_effort", "unsupported parameter: reasoning_effort")
		case 2:
			writeOpenAIAPIError(w, http.StatusBadRequest, "invalid_value", "max_completion_tokens", "maximum completion tokens exceeds this model's limit")
		default:
			writeOpenAITextSSE(w)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")

	if err := validateAPIKeyProfile(t, credentials.AIProviderOpenAI, "private-chat-deployment"); err != nil {
		t.Fatalf("validate custom non-reasoning profile: %v", err)
	}
	first, second, third := <-requests, <-requests, <-requests
	if got := first.body["reasoning_effort"]; got != "none" {
		t.Fatalf("first reasoning_effort=%v, want none", got)
	}
	if _, found := second.body["reasoning_effort"]; found {
		t.Fatalf("field-rejection fallback retained reasoning_effort: %#v", second.body)
	}
	if got := int(second.body["max_completion_tokens"].(float64)); got != openAIValidationReasoningMaxTokens {
		t.Fatalf("field-free max_completion_tokens=%d, want %d", got, openAIValidationReasoningMaxTokens)
	}
	if _, found := third.body["reasoning_effort"]; found {
		t.Fatalf("limit fallback retained reasoning_effort: %#v", third.body)
	}
	if got := int(third.body["max_completion_tokens"].(float64)); got != aiValidationMaxTokens {
		t.Fatalf("compatible max_completion_tokens=%d, want %d", got, aiValidationMaxTokens)
	}
}

func TestOpenAIValidationDoesNotFallbackAcrossCredentialFailure(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writeOpenAIAPIError(w, http.StatusUnauthorized, "invalid_api_key", "", "credential rejected")
	}))
	t.Cleanup(server.Close)
	t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")

	err := validateAPIKeyProfile(t, credentials.AIProviderOpenAI, "private-reasoning-deployment")
	var failure *AIValidationFailure
	if !errors.As(err, &failure) || failure.Kind != AIValidationFailureInvalidCredential {
		t.Fatalf("failure=%#v, want invalid credential", failure)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("credential rejection requests=%d, want no fallback", got)
	}
	if strings.Contains(AIValidationUserMessage(err), "credential rejected") {
		t.Fatal("safe validation response leaked upstream credential error")
	}
}

func TestGeminiValidationProviderContract(t *testing.T) {
	requests := make(chan providerRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureProviderRequest(r)
		writeGeminiTextSSE(w)
	}))
	t.Cleanup(server.Close)
	t.Setenv("GOOGLE_GEMINI_BASE_URL", server.URL)

	if err := validateAPIKeyProfile(t, credentials.AIProviderGemini, "gemini-contract"); err != nil {
		t.Fatalf("validate Gemini profile: %v", err)
	}
	req := <-requests
	if req.path != "/v1beta/models/gemini-contract:streamGenerateContent" || req.query != "alt=sse" {
		t.Fatalf("request=%s?%s", req.path, req.query)
	}
	if got := req.header.Get("X-Goog-Api-Key"); got != "contract-secret" {
		t.Fatalf("x-goog-api-key=%q", got)
	}
	if got := req.header.Get("Authorization"); got != "" {
		t.Fatalf("unexpected Authorization header %q", got)
	}
	config := req.body["generationConfig"].(map[string]any)
	if got := int(config["maxOutputTokens"].(float64)); got != aiValidationMaxTokens {
		t.Fatalf("maxOutputTokens=%d, want %d", got, aiValidationMaxTokens)
	}
	if _, found := req.body["tools"]; found {
		t.Fatal("validation request unexpectedly included tools")
	}
}

func TestGeminiRetriesTransientStartupFailure(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) < 3 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"code":503,"message":"busy","status":"UNAVAILABLE"}}`)
			return
		}
		writeGeminiTextSSE(w)
	}))
	t.Cleanup(server.Close)
	t.Setenv("GOOGLE_GEMINI_BASE_URL", server.URL)

	result, err := NewGeminiService("secret", "gemini-contract", nil).NextTurn(context.Background(), TurnParams{
		History: Transcript{{Role: RoleUser, Content: []TranscriptBlock{{Type: BlockText, Text: "hello"}}}},
	})
	if err != nil {
		t.Fatalf("NextTurn: %v", err)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("requests=%d, want 3", got)
	}
	if result.Usage.OutputTokens != 3 {
		t.Fatalf("output tokens=%d, want candidates + thoughts = 3", result.Usage.OutputTokens)
	}
}

func TestGeminiRetriesTransientFailureAfterMetadataOnly(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 1 {
			_, _ = io.WriteString(w, `data: {"usageMetadata":{"promptTokenCount":99}}`+"\n\n")
			_, _ = io.WriteString(w, `{"error":{"code":503,"message":"busy","status":"UNAVAILABLE"}}`+"\n")
			return
		}
		writeGeminiTextSSE(w)
	}))
	t.Cleanup(server.Close)
	t.Setenv("GOOGLE_GEMINI_BASE_URL", server.URL)

	result, err := NewGeminiService("secret", "gemini-contract", nil).NextTurn(context.Background(), TurnParams{
		History: Transcript{{Role: RoleUser, Content: []TranscriptBlock{{Type: BlockText, Text: "hello"}}}},
	})
	if err != nil {
		t.Fatalf("NextTurn: %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("requests=%d, want metadata-only failure plus retry", got)
	}
	if result.Usage.InputTokens == 99 {
		t.Fatalf("discarded attempt poisoned usage aggregation: %#v", result.Usage)
	}
}

func TestGeminiDoesNotRetryAfterStreamOutput(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]}}]}`+"\n\n")
		_, _ = io.WriteString(w, "data: invalid\n\n")
	}))
	t.Cleanup(server.Close)
	t.Setenv("GOOGLE_GEMINI_BASE_URL", server.URL)

	_, err := NewGeminiService("secret", "gemini-contract", nil).NextTurn(context.Background(), TurnParams{
		History: Transcript{{Role: RoleUser, Content: []TranscriptBlock{{Type: BlockText, Text: "hello"}}}},
	})
	if err == nil {
		t.Fatal("expected malformed stream error")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("requests=%d, retry after output would duplicate streamed content", got)
	}
}

func TestGeminiRetriesSemanticallyIncompleteToolCallWithFreshAggregation(t *testing.T) {
	requests := make(chan providerRequest, 2)
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureProviderRequest(r)
		w.Header().Set("Content-Type", "text/event-stream")
		finish := ""
		if count.Add(1) == 2 {
			finish = `,"finishReason":"STOP"`
		}
		_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call-1","name":"search_movies","args":{"query":"Dune"}}}]}`+finish+`}]}`+"\n\n")
	}))
	t.Cleanup(server.Close)
	t.Setenv("GOOGLE_GEMINI_BASE_URL", server.URL)

	result, err := NewGeminiService("secret", "gemini-contract", nil).NextTurn(context.Background(), TurnParams{
		History: Transcript{{Role: RoleUser, Content: []TranscriptBlock{{Type: BlockText, Text: "find Dune"}}}},
	})
	if err != nil {
		t.Fatalf("NextTurn: %v", err)
	}
	if got := count.Load(); got != 2 {
		t.Fatalf("requests=%d, want one bounded semantic retry", got)
	}
	first, second := <-requests, <-requests
	if !reflect.DeepEqual(first.body, second.body) {
		t.Fatalf("semantic retry changed request\nfirst=%#v\nsecond=%#v", first.body, second.body)
	}
	if result.StopReason != StopReasonToolUse || len(result.Message.Content) != 1 {
		t.Fatalf("result=%#v, want one freshly aggregated tool call", result)
	}
	call := result.Message.Content[0]
	if call.Type != BlockToolUse || call.ID != "call-1" || call.Name != "search_movies" || string(call.Input) != `{"query":"Dune"}` {
		t.Fatalf("tool call=%#v", call)
	}
}

func TestGeminiAcceptsUsableCleanEOFAfterBoundedRetry(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"OK"}]}}]}`+"\n\n")
	}))
	t.Cleanup(server.Close)
	t.Setenv("GOOGLE_GEMINI_BASE_URL", server.URL)

	result, err := NewGeminiService("secret", "gemini-contract", nil).NextTurn(context.Background(), TurnParams{
		History: Transcript{{Role: RoleUser, Content: []TranscriptBlock{{Type: BlockText, Text: "hello"}}}},
	})
	if err != nil {
		t.Fatalf("NextTurn: %v", err)
	}
	if got := requests.Load(); got != geminiSemanticMaxAttempts {
		t.Fatalf("requests=%d, want bounded clean-EOF retry", got)
	}
	if result.StopReason != StopReasonEndTurn || len(result.Message.Content) != 1 || result.Message.Content[0].Text != "OK" {
		t.Fatalf("implicit-stop result=%#v", result)
	}
}

func TestGeminiInteractiveSemanticRetryIsSafeBeforeTextOnly(t *testing.T) {
	t.Run("tool call is retried before execution", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			if requests.Add(1) == 1 {
				_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call-1","name":"search_movies","args":{"query":"Dune"}}}]}}]}`+"\n\n")
				return
			}
			writeGeminiTextSSE(w)
		}))
		t.Cleanup(server.Close)
		t.Setenv("GOOGLE_GEMINI_BASE_URL", server.URL)

		var text strings.Builder
		response, err := NewGeminiService("secret", "gemini-contract", nil).streamGenerate(
			context.Background(),
			[]*genai.Content{genai.NewContentFromText("hello", genai.RoleUser)},
			&genai.GenerateContentConfig{MaxOutputTokens: 128},
			StreamCallbacks{OnText: func(delta string) { text.WriteString(delta) }},
		)
		if err != nil {
			t.Fatalf("streamGenerate: %v", err)
		}
		if got := requests.Load(); got != 2 {
			t.Fatalf("requests=%d, want one semantic retry", got)
		}
		if text.String() == "" || len(response.Candidates) != 1 {
			t.Fatalf("retry response=%#v text=%q", response, text.String())
		}
	})

	t.Run("delivered text is never replayed", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]}}]}`+"\n\n")
		}))
		t.Cleanup(server.Close)
		t.Setenv("GOOGLE_GEMINI_BASE_URL", server.URL)

		var text strings.Builder
		response, err := NewGeminiService("secret", "gemini-contract", nil).streamGenerate(
			context.Background(),
			[]*genai.Content{genai.NewContentFromText("hello", genai.RoleUser)},
			&genai.GenerateContentConfig{MaxOutputTokens: 128},
			StreamCallbacks{OnText: func(delta string) { text.WriteString(delta) }},
		)
		if err != nil {
			t.Fatalf("streamGenerate: %v", err)
		}
		if got := requests.Load(); got != 1 || text.String() != "partial" || len(response.Candidates) != 1 {
			t.Fatalf("requests=%d text=%q response=%#v; delivered text must not be duplicated", got, text.String(), response)
		}
	})
}

func TestGeminiSemanticRetryBackoffIsCancelable(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]}}]}`+"\n\n")
	}))
	t.Cleanup(server.Close)
	t.Setenv("GOOGLE_GEMINI_BASE_URL", server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := NewGeminiService("secret", "gemini-contract", nil).NextTurn(ctx, TurnParams{
		History: Transcript{{Role: RoleUser, Content: []TranscriptBlock{{Type: BlockText, Text: "hello"}}}},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v, want deadline exceeded", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("requests=%d, want cancellation before semantic retry", got)
	}
}

func TestGeminiBlockedAbnormalAndEmptyResponsesAreExplicitErrors(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		want         string
		wantRequests int32
	}{
		{
			name: "blocked prompt",
			body: `data: {"promptFeedback":{"blockReason":"SAFETY"}}` + "\n\n",
			want: "prompt blocked (SAFETY)",
		},
		{
			name: "abnormal finish",
			body: `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]},"finishReason":"SAFETY"}]}` + "\n\n",
			want: "generation stopped (SAFETY)",
		},
		{
			name: "empty candidate",
			body: `data: {"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP"}]}` + "\n\n",
			want: "no text or tool calls",
		},
		{
			name: "no candidates",
			body: `data: {}` + "\n\n",
			want: "no candidates",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, test.body)
			}))
			t.Cleanup(server.Close)
			t.Setenv("GOOGLE_GEMINI_BASE_URL", server.URL)

			_, err := NewGeminiService("secret", "gemini-contract", nil).NextTurn(context.Background(), TurnParams{
				History: Transcript{{Role: RoleUser, Content: []TranscriptBlock{{Type: BlockText, Text: "hello"}}}},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
			wantRequests := test.wantRequests
			if wantRequests == 0 {
				wantRequests = 1
			}
			if got := requests.Load(); got != wantRequests {
				t.Fatalf("requests=%d, want %d", got, wantRequests)
			}
		})
	}
}

func TestGeminiRetryBackoffIsCancelable(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"code":503,"message":"busy","status":"UNAVAILABLE"}}`)
	}))
	t.Cleanup(server.Close)
	t.Setenv("GOOGLE_GEMINI_BASE_URL", server.URL)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)
	_, err := NewGeminiService("secret", "gemini-contract", nil).NextTurn(ctx, TurnParams{
		History: Transcript{{Role: RoleUser, Content: []TranscriptBlock{{Type: BlockText, Text: "hello"}}}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want context canceled", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("requests=%d, want cancellation before retry", got)
	}
}

func TestProviderContinuationStateIsReplayed(t *testing.T) {
	t.Run("Anthropic thinking signatures", func(t *testing.T) {
		requests := make(chan providerRequest, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests <- captureProviderRequest(r)
			writeAnthropicTextSSE(w)
		}))
		t.Cleanup(server.Close)
		t.Setenv("ANTHROPIC_BASE_URL", server.URL)

		_, err := NewService("secret", "claude-opus-4-8", nil).NextTurn(context.Background(), TurnParams{
			History: Transcript{
				{Role: RoleUser, Content: []TranscriptBlock{{Type: BlockText, Text: "look it up"}}},
				{Role: RoleAssistant, Content: []TranscriptBlock{
					{Type: BlockAnthropicThinking, Text: "private thought", Signature: "sig-123"},
					{Type: BlockAnthropicRedactedThinking, Data: "redacted-456"},
					{Type: BlockToolUse, ID: "call-1", Name: "search_movies", Input: json.RawMessage(`{"query":"Dune"}`)},
				}},
				{Role: RoleUser, Content: []TranscriptBlock{{Type: BlockToolResult, ToolUseID: "call-1", Name: "search_movies", Content: "Dune"}}},
			},
		})
		if err != nil {
			t.Fatalf("NextTurn: %v", err)
		}
		req := <-requests
		messages := req.body["messages"].([]any)
		blocks := messages[1].(map[string]any)["content"].([]any)
		thinking := blocks[0].(map[string]any)
		if thinking["type"] != "thinking" || thinking["thinking"] != "private thought" || thinking["signature"] != "sig-123" {
			t.Fatalf("thinking block=%#v", thinking)
		}
		redacted := blocks[1].(map[string]any)
		if redacted["type"] != "redacted_thinking" || redacted["data"] != "redacted-456" {
			t.Fatalf("redacted thinking block=%#v", redacted)
		}
	})

	t.Run("Gemini thought signatures", func(t *testing.T) {
		requests := make(chan providerRequest, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests <- captureProviderRequest(r)
			writeGeminiTextSSE(w)
		}))
		t.Cleanup(server.Close)
		t.Setenv("GOOGLE_GEMINI_BASE_URL", server.URL)

		_, err := NewGeminiService("secret", "gemini-contract", nil).NextTurn(context.Background(), TurnParams{
			History: Transcript{
				{Role: RoleUser, Content: []TranscriptBlock{{Type: BlockText, Text: "look it up"}}},
				{Role: RoleAssistant, Content: []TranscriptBlock{
					{Type: BlockGeminiThought, Text: "private thought", ThoughtSignature: []byte{1, 2, 3}},
					{Type: BlockToolUse, ID: "call-1", Name: "search_movies", Input: json.RawMessage(`{"query":"Dune"}`), ThoughtSignature: []byte{4, 5}},
				}},
				{Role: RoleUser, Content: []TranscriptBlock{{Type: BlockToolResult, ToolUseID: "call-1", Name: "search_movies", Content: "Dune"}}},
			},
		})
		if err != nil {
			t.Fatalf("NextTurn: %v", err)
		}
		req := <-requests
		contents := req.body["contents"].([]any)
		parts := contents[1].(map[string]any)["parts"].([]any)
		thought := parts[0].(map[string]any)
		if thought["thought"] != true || thought["thoughtSignature"] != "AQID" {
			t.Fatalf("thought part=%#v", thought)
		}
		call := parts[1].(map[string]any)
		if call["thoughtSignature"] != "BAU=" {
			t.Fatalf("function-call signature=%#v", call)
		}
	})
}

func TestOpenAIRefusalAndEmptyResponseAreExplicitErrors(t *testing.T) {
	tests := []struct {
		name  string
		delta string
		want  string
	}{
		{name: "refusal", delta: `{"role":"assistant","refusal":"not allowed"}`, want: "refused"},
		{name: "empty", delta: `{"role":"assistant"}`, want: "no text or tool calls"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, `data: {"id":"chatcmpl_test","object":"chat.completion.chunk","created":1,"model":"gpt-5.5","choices":[{"index":0,"delta":`+test.delta+`,"finish_reason":"stop"}]}`+"\n\n")
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
			}))
			t.Cleanup(server.Close)
			t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")

			_, err := NewOpenAIService("secret", "gpt-5.5", nil).NextTurn(context.Background(), TurnParams{
				History: Transcript{{Role: RoleUser, Content: []TranscriptBlock{{Type: BlockText, Text: "hello"}}}},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestValidationFailureClassificationIsSanitized(t *testing.T) {
	tests := []struct {
		status int
		kind   AIValidationFailureKind
		text   string
	}{
		{status: http.StatusUnauthorized, kind: AIValidationFailureInvalidCredential, text: "rejected"},
		{status: http.StatusNotFound, kind: AIValidationFailureUnsupportedModel, text: "unavailable"},
		{status: http.StatusTooManyRequests, kind: AIValidationFailureQuota, text: "quota"},
		{status: http.StatusServiceUnavailable, kind: AIValidationFailureTemporary, text: "temporarily"},
	}
	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			failure := newAIValidationFailure(genai.APIError{Code: test.status, Message: "upstream-secret-payload"})
			var typed *AIValidationFailure
			if !errors.As(failure, &typed) || typed.Kind != test.kind {
				t.Fatalf("failure=%#v, want kind %s", typed, test.kind)
			}
			message := AIValidationUserMessage(failure)
			if !strings.Contains(strings.ToLower(message), test.text) {
				t.Fatalf("message=%q, want %q", message, test.text)
			}
			if strings.Contains(message, "upstream-secret-payload") || strings.Contains(failure.Error(), "upstream-secret-payload") {
				t.Fatalf("validation error leaked provider body: %q / %q", message, failure)
			}
		})
	}
}

func TestCodexValidationFailureClassification(t *testing.T) {
	tests := []struct {
		err  error
		kind AIValidationFailureKind
	}{
		{err: codexapp.ErrNotConnected, kind: AIValidationFailureInvalidCredential},
		{err: codexapp.ErrUsageLimit, kind: AIValidationFailureQuota},
		{err: codexapp.ErrBusy, kind: AIValidationFailureTemporary},
		{err: codexapp.ErrUnavailable, kind: AIValidationFailureTemporary},
	}
	for _, test := range tests {
		if got := classifyAIValidationFailure(test.err); got != test.kind {
			t.Errorf("classify(%v)=%s, want %s", test.err, got, test.kind)
		}
	}
}

func TestAIValidationSafeDiagnosticRetainsStatusButRedactsCredentials(t *testing.T) {
	failure := newAIValidationFailure(errors.New("upstream 401: https://provider.invalid/test?api_key=diagnostic-secret&model=gpt"))
	var typed *AIValidationFailure
	if !errors.As(failure, &typed) {
		t.Fatalf("failure type=%T", failure)
	}
	diagnostic := typed.SafeDiagnostic()
	if !strings.Contains(diagnostic, "upstream 401") || !strings.Contains(diagnostic, "model=gpt") {
		t.Fatalf("diagnostic lost useful context: %q", diagnostic)
	}
	if strings.Contains(diagnostic, "diagnostic-secret") {
		t.Fatalf("diagnostic leaked credential: %q", diagnostic)
	}
}
