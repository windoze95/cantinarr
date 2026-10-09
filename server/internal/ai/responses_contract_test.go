package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/mcp"
)

const responsesTextOutput = `[{"id":"msg_1","type":"message","role":"assistant","status":"completed","phase":"final_answer","content":[{"type":"output_text","text":"OK","annotations":[]}]}]`
const responsesToolOutput = `[{"id":"rs_1","type":"reasoning","summary":[],"encrypted_content":"opaque-state"},{"id":"msg_1","type":"message","role":"assistant","status":"completed","phase":"commentary","content":[{"type":"output_text","text":"Searching.","annotations":[]}]},{"id":"fc_1","type":"function_call","call_id":"call_1","name":"search_movies","arguments":"{}","status":"completed"}]`

func writeResponsesOutput(w http.ResponseWriter, output string) {
	w.Header().Set("Content-Type", "text/event-stream")
	if output == responsesTextOutput {
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\n")
	}
	_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"completed\",\"model\":\"gpt-6.1-sol\",\"output\":%s,\"usage\":{\"input_tokens\":7,\"input_tokens_details\":{\"cached_tokens\":2},\"output_tokens\":3,\"total_tokens\":10}}}\n\n", output)
}

func TestOpenAIResponsesValidationRetainsExactModelAndRequestContract(t *testing.T) {
	requests := make(chan providerRequest, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureProviderRequest(r)
		writeResponsesOutput(w, responsesTextOutput)
	}))
	t.Cleanup(server.Close)
	t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")
	service := NewOpenAIService("contract-secret", "gpt-6.1-sol", "", "none", nil)
	tools := mcp.NewToolServer(nil, nil, nil, nil).GetToolsForRole(auth.RoleAdmin)
	result, err := service.NextTurn(context.Background(), validationProbeParams(tools))
	if err != nil {
		t.Fatal(err)
	}
	if result.Usage.InputTokens != 7 || result.Usage.CacheReadTokens != 2 || result.StopReason != StopReasonEndTurn {
		t.Fatalf("response result=%#v", result)
	}
	req := <-requests
	if req.path != "/v1/responses" || req.body["model"] != "gpt-6.1-sol" || req.body["store"] != false || req.body["stream"] != true {
		t.Fatalf("wrong Responses request: %#v", req)
	}
	if req.header.Get("Authorization") != "Bearer contract-secret" || req.header.Get("x-grok-model-override") != "" {
		t.Fatal("OpenAI Responses used the wrong auth/header contract")
	}
	if req.body["tool_choice"] != "none" || len(req.body["tools"].([]any)) == 0 {
		t.Fatalf("validation omitted the disabled tool catalog: %#v", req.body)
	}
	if req.body["reasoning"].(map[string]any)["effort"] != "low" || int(req.body["max_output_tokens"].(float64)) < openAIValidationReasoningMaxTokens {
		t.Fatalf("invalid reasoning probe contract: %#v", req.body)
	}
	if _, exists := req.body["max_completion_tokens"]; exists {
		t.Fatal("Responses request contains Chat Completions token parameter")
	}
	if _, exists := req.body["reasoning_effort"]; exists {
		t.Fatal("Responses request contains Chat Completions reasoning parameter")
	}
	if err := validateAPIKeyProfile(t, credentials.AIProviderOpenAI, "gpt-6.1-sol"); err != nil {
		t.Fatalf("save-time validation: %v", err)
	}
	if req := <-requests; req.path != "/v1/responses" || req.body["model"] != "gpt-6.1-sol" {
		t.Fatalf("save-time validation changed model or endpoint: %#v", req)
	}
}

func TestOpenAIResponsesReplaysReasoningPhaseAndToolResults(t *testing.T) {
	requests := make(chan providerRequest, 2)
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureProviderRequest(r)
		if count.Add(1) == 1 {
			writeResponsesOutput(w, responsesToolOutput)
		} else {
			writeResponsesOutput(w, responsesTextOutput)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")
	service := NewOpenAIService("secret", "gpt-6.1-sol", "", "", nil)
	p := TurnParams{System: "test", History: Transcript{{Role: RoleUser, Content: []TranscriptBlock{{Type: BlockText, Text: "find a movie"}}}}, Tools: []mcp.Tool{{Name: "search_movies", InputSchema: map[string]any{"type": "object"}}}}
	first, err := service.NextTurn(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if first.StopReason != StopReasonToolUse {
		t.Fatalf("stop=%s", first.StopReason)
	}
	p.History = append(p.History, first.Message, TranscriptMessage{Role: RoleUser, Content: []TranscriptBlock{{Type: BlockToolResult, ToolUseID: "call_1", Name: "search_movies", Content: "Found a movie."}}})
	if _, err := service.NextTurn(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	<-requests
	req := <-requests
	body, _ := json.Marshal(req.body["input"])
	for _, wanted := range []string{`"encrypted_content":"opaque-state"`, `"phase":"commentary"`, `"call_id":"call_1"`, `"type":"function_call_output"`, `Found a movie.`} {
		if !strings.Contains(string(body), wanted) {
			t.Fatalf("replay missing %s: %s", wanted, body)
		}
	}
	if strings.Count(string(body), `"type":"function_call"`) != 1 {
		t.Fatalf("function call duplicated during replay: %s", body)
	}
	// Removed orphan calls must not return through the saved raw output.
	private := first.Message.toPrivate()
	private = stripOrphanToolUse(private, map[string]bool{})
	items, _ := json.Marshal(service.responsesAdapter().inputItems(transcript{private}))
	if strings.Contains(string(items), `"type":"function_call"`) {
		t.Fatalf("orphan call restored from opaque state: %s", items)
	}
}

func TestOpenAIResponsesChatUsesSavedSelectionAndStreamsText(t *testing.T) {
	requests := make(chan providerRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureProviderRequest(r)
		writeResponsesOutput(w, responsesTextOutput)
	}))
	t.Cleanup(server.Close)
	t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")
	h, registry, _, userID := newResolverTestHandler(t)
	h.toolServer = mcp.NewToolServer(nil, nil, nil, nil)
	h.conversations = newConversationStore()
	if err := registry.SetUserAIConfig(userID, credentials.AIProviderOpenAI, "gpt-6.1-sol"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetUserAICredential(userID, credentials.AIProviderOpenAI, "contract-secret"); err != nil {
		t.Fatal(err)
	}
	recorder := postChat(t, h, userID)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "OK") {
		t.Fatalf("chat=%d %s", recorder.Code, recorder.Body.String())
	}
	for _, frame := range chatSSEFrames(t, recorder.Body.String()) {
		if frame["error"] != nil {
			t.Fatalf("chat returned error: %s", frame["error"])
		}
	}
	req := <-requests
	if req.path != "/v1/responses" || req.body["model"] != "gpt-6.1-sol" {
		t.Fatalf("chat request=%#v", req)
	}
}

func TestOpenAIResponsesToolAuthorizationStillStopsTheLoop(t *testing.T) {
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		writeResponsesOutput(w, responsesToolOutput)
	}))
	t.Cleanup(server.Close)
	t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")
	history := transcript{textTranscriptMessage(agentRoleUser, "find a movie")}
	final, err := NewOpenAIService("secret", "gpt-6.1-sol", "", "", revokedInteractiveToolServer()).SendMessage(context.Background(), history, ChatContext{UserID: 1, Role: auth.RoleUser}, StreamCallbacks{})
	if !errors.Is(err, mcp.ErrToolAuthorization) || count.Load() != 1 {
		t.Fatalf("err=%v requests=%d", err, count.Load())
	}
	assertNoRevocationToolResult(t, final)
}

func TestResponsesStreamingFailuresRemainActionable(t *testing.T) {
	for _, tc := range []struct {
		code string
		want AIValidationFailureKind
	}{
		{"model_not_found", AIValidationFailureUnsupportedModel},
		{"rate_limit_exceeded", AIValidationFailureQuota},
		{"server_error", AIValidationFailureTemporary},
		{"invalid_prompt", AIValidationFailureInvalidResponse},
	} {
		t.Run(tc.code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":%q,\"message\":\"provider rejected the request\"}}}\n\n", tc.code)
			}))
			t.Cleanup(server.Close)
			t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")
			_, err := NewOpenAIService("secret", "gpt-6.1-sol", "", "", nil).NextTurn(context.Background(), validationProbeParams(nil))
			if got := classifyAIValidationFailure(err); err == nil || got != tc.want {
				t.Fatalf("error=%v category=%s want=%s", err, got, tc.want)
			}
		})
	}
}

func TestResponsesRejectsIncompleteToolCalls(t *testing.T) {
	for _, tc := range []struct{ name, status, call string }{
		{"incomplete response", "incomplete", `{"type":"function_call","call_id":"call_1","name":"search_movies","arguments":"{}","status":"completed"}`},
		{"incomplete call", "completed", `{"type":"function_call","call_id":"call_1","name":"search_movies","arguments":"{}","status":"in_progress"}`},
		{"malformed arguments", "completed", `{"type":"function_call","call_id":"call_1","name":"search_movies","arguments":"{","status":"completed"}`},
		{"missing call ID", "completed", `{"type":"function_call","name":"search_movies","arguments":"{}","status":"completed"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: {\"type\":\"response.%s\",\"response\":{\"status\":%q,\"output\":[%s]}}\n\n", tc.status, tc.status, tc.call)
			}))
			t.Cleanup(server.Close)
			t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")
			result, err := NewOpenAIService("secret", "gpt-6.1-sol", "", "", nil).NextTurn(context.Background(), validationProbeParams(nil))
			if err == nil || len(result.Message.Content) != 0 {
				t.Fatalf("unsafe tool output accepted: result=%#v err=%v", result, err)
			}
		})
	}
}

func TestResponsesRejectsRefusalEvenWithVisibleText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeResponsesOutput(w, `[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Sorry","annotations":[]},{"type":"refusal","refusal":"Cannot answer"}]}]`)
	}))
	t.Cleanup(server.Close)
	t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")
	if _, err := NewOpenAIService("secret", "gpt-6.1-sol", "", "", nil).NextTurn(context.Background(), validationProbeParams(nil)); err == nil {
		t.Fatal("refusal passed validation")
	}
}
