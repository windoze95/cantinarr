package ai

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestCredentialHTTPClientDoesNotFollowRedirects(t *testing.T) {
	var redirectedRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedRequests.Add(1)
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("redirect destination received Authorization %q", got)
		}
		if got := r.Header.Get("X-Goog-Api-Key"); got != "" {
			t.Errorf("redirect destination received X-Goog-Api-Key %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(destination.Close)

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/credential-sink", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)

	req, err := http.NewRequest(http.MethodPost, source.URL+"/completion", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer provider-secret")
	req.Header.Set("X-Goog-Api-Key", "gemini-secret")
	resp, err := newCredentialHTTPClient(time.Second).Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307", resp.StatusCode)
	}
	if got := redirectedRequests.Load(); got != 0 {
		t.Fatalf("redirect destination received %d requests, want 0", got)
	}
}

func TestProviderNeutralTranscriptConvertersPreserveToolContext(t *testing.T) {
	history := transcript{
		textTranscriptMessage(agentRoleUser, "find dune"),
		{
			Role: agentRoleAssistant,
			Content: []transcriptBlock{
				{Type: blockTypeText, Text: "I'll check."},
				{Type: blockTypeToolUse, ID: "call_1", Name: "search_movies", Input: []byte(`{"query":"Dune"}`)},
			},
		},
		{
			Role: agentRoleUser,
			Content: []transcriptBlock{
				{Type: blockTypeToolResult, ToolUseID: "call_1", Name: "search_movies", Content: "Dune (2021)"},
			},
		},
		textTranscriptMessage(agentRoleAssistant, "Dune (2021) is available."),
	}

	openAIMessages := toOpenAIMessages(history)
	if len(openAIMessages) != 4 {
		t.Fatalf("expected 4 OpenAI messages, got %d", len(openAIMessages))
	}
	if openAIMessages[1].OfAssistant == nil || len(openAIMessages[1].OfAssistant.ToolCalls) != 1 {
		t.Fatalf("expected assistant tool call, got %#v", openAIMessages[1])
	}
	toolCall := openAIMessages[1].OfAssistant.ToolCalls[0].GetFunction()
	if toolCall == nil {
		t.Fatalf("expected OpenAI function tool call, got %#v", openAIMessages[1].OfAssistant.ToolCalls[0])
	}
	if got := toolCall.Arguments; got != `{"query":"Dune"}` {
		t.Fatalf("unexpected OpenAI tool args: %s", got)
	}
	if openAIMessages[2].OfTool == nil || openAIMessages[2].OfTool.ToolCallID != "call_1" {
		t.Fatalf("expected OpenAI tool result message, got %#v", openAIMessages[2])
	}

	geminiContents := toGeminiContents(history)
	if len(geminiContents) != 4 {
		t.Fatalf("expected 4 Gemini contents, got %d", len(geminiContents))
	}
	if call := geminiContents[1].Parts[1].FunctionCall; call == nil || call.Name != "search_movies" || call.Args["query"] != "Dune" {
		t.Fatalf("expected Gemini function call with args, got %#v", geminiContents[1].Parts[1])
	}
	if response := geminiContents[2].Parts[0].FunctionResponse; response == nil || response.Name != "search_movies" || response.ID != "call_1" {
		t.Fatalf("expected Gemini function response with name/id, got %#v", geminiContents[2].Parts[0])
	}

	anthropicMessages := toSDKMessages(history)
	if len(anthropicMessages) != 4 {
		t.Fatalf("expected 4 Anthropic messages, got %d", len(anthropicMessages))
	}
	if anthropicMessages[1].Content[1].OfToolUse == nil {
		t.Fatalf("expected Anthropic tool_use block, got %#v", anthropicMessages[1].Content[1])
	}
	if anthropicMessages[2].Content[0].OfToolResult == nil {
		t.Fatalf("expected Anthropic tool_result block, got %#v", anthropicMessages[2].Content[0])
	}
}
