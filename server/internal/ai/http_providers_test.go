package ai

import (
	"errors"
	"strings"
	"testing"
)

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
	if openAIMessages[1].Role != "assistant" || len(openAIMessages[1].ToolCalls) != 1 {
		t.Fatalf("expected assistant tool call, got %#v", openAIMessages[1])
	}
	if got := openAIMessages[1].ToolCalls[0].Function.Arguments; got != `{"query":"Dune"}` {
		t.Fatalf("unexpected OpenAI tool args: %s", got)
	}
	if openAIMessages[2].Role != "tool" || openAIMessages[2].ToolCallID != "call_1" {
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

func TestReadSSEHandlesMultilineEventsAndDone(t *testing.T) {
	body := strings.NewReader("data: first\ndata: second\n\n: keepalive\n\ndata: [DONE]\n\n")
	var events []string
	err := readSSE(body, func(data string) error {
		events = append(events, data)
		if data == "[DONE]" {
			return errSSEDone
		}
		return nil
	})
	if !errors.Is(err, errSSEDone) {
		t.Fatalf("expected errSSEDone, got %v", err)
	}
	if len(events) != 2 || events[0] != "first\nsecond" || events[1] != "[DONE]" {
		t.Fatalf("unexpected SSE events: %#v", events)
	}
}
