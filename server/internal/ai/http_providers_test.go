package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/mcp"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"google.golang.org/genai"
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

func TestAnthropicTerminalResponsesMustContainUsableOutput(t *testing.T) {
	tests := []struct {
		name    string
		message *anthropic.Message
		want    string
	}{
		{name: "nil", want: "empty"},
		{name: "refusal", message: &anthropic.Message{StopReason: anthropic.StopReasonRefusal}, want: "refused"},
		{name: "pause", message: &anthropic.Message{StopReason: anthropic.StopReasonPauseTurn}, want: "paused"},
		{name: "no content", message: &anthropic.Message{StopReason: anthropic.StopReasonEndTurn}, want: "no text or tool calls"},
		{name: "whitespace", message: &anthropic.Message{
			StopReason: anthropic.StopReasonEndTurn,
			Content:    []anthropic.ContentBlockUnion{{Type: "text", Text: " \n "}},
		}, want: "no text or tool calls"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateAnthropicMessage(test.message)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestLiveGeminiUserToolCatalog(t *testing.T) {
	if os.Getenv("CANTINARR_LIVE_AI_TESTS") != "1" {
		t.Skip("set CANTINARR_LIVE_AI_TESTS=1 to run hosted-provider smoke tests")
	}
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		t.Skip("GEMINI_API_KEY is not set")
	}

	toolServer := mcp.NewToolServer(nil, nil, nil, nil)
	service := NewGeminiService(apiKey, "gemini-3.5-flash", toolServer)
	result, err := service.NextTurn(context.Background(), TurnParams{
		System: "Call the requested tool.",
		Tools:  toolServer.GetToolsForRole(auth.RoleUser),
		History: Transcript{{
			Role: RoleUser,
			Content: []TranscriptBlock{{
				Type: BlockText,
				Text: "Call search_movies for Dune.",
			}},
		}},
		MaxTokens: 256,
	})
	if err != nil {
		t.Fatalf("Gemini user tool catalog: %v", err)
	}
	for _, block := range result.Message.Content {
		if block.Type == BlockToolUse && block.Name == "search_movies" {
			return
		}
	}
	t.Fatalf("Gemini returned no search_movies tool call: %#v", result.Message.Content)
}

// TestLiveAPIKeyValidationCatalog is opt-in because it spends real provider
// quota. It proves every advertised API-key model against the production
// validation adapter. Model access and quota are account-specific, so those
// classified outcomes are reported as skips; malformed responses, rejected
// credentials, and exhausted transient retries remain hard failures.
func TestLiveAPIKeyValidationCatalog(t *testing.T) {
	if os.Getenv("CANTINARR_LIVE_AI_TESTS") != "1" {
		t.Skip("set CANTINARR_LIVE_AI_TESTS=1 to run hosted-provider smoke tests")
	}
	providerFilter := strings.TrimSpace(os.Getenv("CANTINARR_LIVE_AI_PROVIDER"))
	keys := map[string]string{
		credentials.AIProviderAnthropic: os.Getenv("ANTHROPIC_API_KEY"),
		credentials.AIProviderOpenAI:    os.Getenv("OPENAI_API_KEY"),
		credentials.AIProviderGemini:    os.Getenv("GEMINI_API_KEY"),
	}
	for _, provider := range credentials.AIProviders {
		if provider.AuthType != credentials.AIAuthTypeAPIKey || (providerFilter != "" && provider.ID != providerFilter) {
			continue
		}
		key := keys[provider.ID]
		if key == "" {
			t.Run(provider.ID, func(t *testing.T) { t.Skip("provider API key is not set") })
			continue
		}
		for _, model := range provider.Models {
			providerID, modelID := provider.ID, model.ID
			t.Run(providerID+"/"+modelID, func(t *testing.T) {
				h, _, _, userID := newResolverTestHandler(t)
				h.validationProbe = nil
				err := h.ValidatePersonalAISettings(context.Background(), userID, credentials.AIProfile{
					Config:            credentials.AIConfig{Provider: providerID, Model: modelID},
					APIKey:            key,
					CredentialPresent: true,
				})
				if err == nil {
					return
				}
				var failure *AIValidationFailure
				if errors.As(err, &failure) && (failure.Kind == AIValidationFailureUnsupportedModel || failure.Kind == AIValidationFailureQuota) {
					t.Skip(AIValidationUserMessage(err))
				}
				t.Fatal(AIValidationUserMessage(err))
			})
		}
	}
}

// TestLiveAPIKeyInteractiveChat exercises the production multi-turn chat
// adapters, including SSE decoding and transcript assembly, against one stable
// low-cost model per configured provider.
func TestLiveAPIKeyInteractiveChat(t *testing.T) {
	if os.Getenv("CANTINARR_LIVE_AI_TESTS") != "1" {
		t.Skip("set CANTINARR_LIVE_AI_TESTS=1 to run hosted-provider smoke tests")
	}
	providerFilter := strings.TrimSpace(os.Getenv("CANTINARR_LIVE_AI_PROVIDER"))
	tests := []struct {
		provider string
		model    string
		key      string
	}{
		{credentials.AIProviderAnthropic, "claude-haiku-4-5", os.Getenv("ANTHROPIC_API_KEY")},
		{credentials.AIProviderOpenAI, "gpt-4.1-mini", os.Getenv("OPENAI_API_KEY")},
		{credentials.AIProviderGemini, "gemini-3.1-flash-lite", os.Getenv("GEMINI_API_KEY")},
	}
	for _, test := range tests {
		if providerFilter != "" && providerFilter != test.provider {
			continue
		}
		t.Run(test.provider, func(t *testing.T) {
			if test.key == "" {
				t.Skip("provider API key is not set")
			}
			toolServer := mcp.NewToolServer(nil, nil, nil, nil)
			toolServer.SetCallAuthorizer(func(context.Context, mcp.CallContext) (string, error) {
				return auth.RoleUser, nil
			})
			history := transcript{textTranscriptMessage(agentRoleUser, "Reply with exactly LIVE_OK. Do not call a tool.")}
			chatCtx := ChatContext{UserID: 1, Role: auth.RoleUser, DeviceID: "live-test-device"}
			var streamed strings.Builder
			callbacks := StreamCallbacks{OnText: func(value string) { streamed.WriteString(value) }}
			var (
				final transcript
				err   error
			)
			switch test.provider {
			case credentials.AIProviderAnthropic:
				final, err = NewService(test.key, test.model, toolServer).SendMessage(context.Background(), history, chatCtx, callbacks)
			case credentials.AIProviderOpenAI:
				final, err = NewOpenAIService(test.key, test.model, toolServer).SendMessage(context.Background(), history, chatCtx, callbacks)
			case credentials.AIProviderGemini:
				final, err = NewGeminiService(test.key, test.model, toolServer).SendMessage(context.Background(), history, chatCtx, callbacks)
			}
			if err != nil {
				t.Fatalf("interactive chat failed: %v", secrets.RedactError(err))
			}
			if strings.TrimSpace(streamed.String()) == "" || len(final) <= len(history) {
				t.Fatalf("interactive chat returned no assistant response: streamed=%q messages=%d", streamed.String(), len(final))
			}
		})
	}
}

// TestLiveOpenAIInteractiveCatalog proves that every advertised OpenAI model
// accepts Cantinarr's production interactive request shape, including the
// requester tool catalog. It is intentionally opt-in because it spends real
// API quota.
func TestLiveOpenAIInteractiveCatalog(t *testing.T) {
	if os.Getenv("CANTINARR_LIVE_AI_TESTS") != "1" {
		t.Skip("set CANTINARR_LIVE_AI_TESTS=1 to run hosted-provider smoke tests")
	}
	if filter := strings.TrimSpace(os.Getenv("CANTINARR_LIVE_AI_PROVIDER")); filter != "" && filter != credentials.AIProviderOpenAI {
		t.Skip("OpenAI is not selected by CANTINARR_LIVE_AI_PROVIDER")
	}
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("OPENAI_API_KEY is not set")
	}

	var models []credentials.AIModelOption
	for _, provider := range credentials.AIProviders {
		if provider.ID == credentials.AIProviderOpenAI {
			models = provider.Models
			break
		}
	}
	if len(models) == 0 {
		t.Fatal("advertised OpenAI model catalog is empty")
	}

	for _, model := range models {
		model := model
		t.Run(model.ID, func(t *testing.T) {
			toolServer := mcp.NewToolServer(nil, nil, nil, nil)
			toolServer.SetCallAuthorizer(func(context.Context, mcp.CallContext) (string, error) {
				return auth.RoleUser, nil
			})
			history := transcript{textTranscriptMessage(agentRoleUser, "Reply with exactly LIVE_OK. Do not call a tool.")}
			var streamed strings.Builder
			final, err := NewOpenAIService(key, model.ID, toolServer).SendMessage(
				context.Background(),
				history,
				ChatContext{UserID: 1, Role: auth.RoleUser, DeviceID: "live-openai-catalog-device"},
				StreamCallbacks{OnText: func(value string) { streamed.WriteString(value) }},
			)
			if err != nil {
				classified := newAIValidationFailure(err)
				var failure *AIValidationFailure
				if errors.As(classified, &failure) && (failure.Kind == AIValidationFailureUnsupportedModel || failure.Kind == AIValidationFailureQuota) {
					t.Skip(AIValidationUserMessage(classified))
				}
				t.Fatal(AIValidationUserMessage(classified))
			}
			if strings.TrimSpace(streamed.String()) == "" || len(final) <= len(history) {
				t.Fatalf("interactive chat returned no assistant response: messages=%d", len(final))
			}
		})
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

func TestInteractiveConvertersReplayOpaqueProviderContinuationState(t *testing.T) {
	history := transcript{
		textTranscriptMessage(agentRoleUser, "find dune"),
		{
			Role: agentRoleAssistant,
			Content: []transcriptBlock{
				{Type: blockTypeAnthropicThinking, Text: "private", Signature: "sig-123"},
				{Type: blockTypeAnthropicRedactedThinking, Data: "redacted-456"},
				{Type: blockTypeGeminiThought, Text: "private", ThoughtSignature: []byte{1, 2, 3}},
				{Type: blockTypeToolUse, ID: "call_1", Name: "search_movies", Input: []byte(`{"query":"Dune"}`), ThoughtSignature: []byte{4, 5}},
			},
		},
		{
			Role: agentRoleUser,
			Content: []transcriptBlock{{
				Type: blockTypeToolResult, ToolUseID: "call_1", Name: "search_movies", Content: "Dune (2021)",
			}},
		},
	}

	anthropicMessages := toSDKMessages(history)
	if len(anthropicMessages) != 3 {
		t.Fatalf("Anthropic messages=%d, want 3", len(anthropicMessages))
	}
	blocks := anthropicMessages[1].Content
	if blocks[0].OfThinking == nil || blocks[0].OfThinking.Signature != "sig-123" || blocks[0].OfThinking.Thinking != "private" {
		t.Fatalf("Anthropic thinking block=%#v", blocks[0])
	}
	if blocks[1].OfRedactedThinking == nil || blocks[1].OfRedactedThinking.Data != "redacted-456" {
		t.Fatalf("Anthropic redacted block=%#v", blocks[1])
	}

	geminiContents := toGeminiContents(history)
	if len(geminiContents) != 3 {
		t.Fatalf("Gemini contents=%d, want 3", len(geminiContents))
	}
	parts := geminiContents[1].Parts
	if len(parts) != 2 {
		t.Fatalf("Gemini parts=%d, want provider-relevant thought + tool call", len(parts))
	}
	if !parts[0].Thought || string(parts[0].ThoughtSignature) != string([]byte{1, 2, 3}) {
		t.Fatalf("Gemini thought part=%#v", parts[0])
	}
	if parts[1].FunctionCall == nil || string(parts[1].ThoughtSignature) != string([]byte{4, 5}) {
		t.Fatalf("Gemini tool-call part=%#v", parts[1])
	}

	var anthropicResponse anthropic.Message
	if err := json.Unmarshal([]byte(`{"role":"assistant","content":[{"type":"thinking","thinking":"private","signature":"sig-123"},{"type":"redacted_thinking","data":"redacted-456"}]}`), &anthropicResponse); err != nil {
		t.Fatal(err)
	}
	fromAnthropic := anthropicMessageToTranscript(anthropicResponse)
	if len(fromAnthropic.Content) != 2 || fromAnthropic.Content[0].Signature != "sig-123" || fromAnthropic.Content[1].Data != "redacted-456" {
		t.Fatalf("Anthropic response state was dropped: %#v", fromAnthropic.Content)
	}

	fromGemini := geminiContentToTranscript(&genai.Content{Role: "model", Parts: []*genai.Part{
		{Text: "private", Thought: true, ThoughtSignature: []byte{1, 2, 3}},
		{FunctionCall: &genai.FunctionCall{ID: "call_1", Name: "search_movies", Args: map[string]any{"query": "Dune"}}, ThoughtSignature: []byte{4, 5}},
	}})
	if len(fromGemini.Content) != 2 || string(fromGemini.Content[0].ThoughtSignature) != string([]byte{1, 2, 3}) || string(fromGemini.Content[1].ThoughtSignature) != string([]byte{4, 5}) {
		t.Fatalf("Gemini response state was dropped: %#v", fromGemini.Content)
	}
}
