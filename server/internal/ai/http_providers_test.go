package ai

import (
	"context"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/mcp"
)

func TestOpenAIServiceEmitsToolCallTextBeforeRunningTool(t *testing.T) {
	toolServer := mcp.NewToolServer(nil, nil, nil, nil)
	calls := 0
	service := &openAIService{
		toolServer: toolServer,
		chatFn: func(context.Context, openAIChatRequest) (openAIMessage, string, error) {
			calls++
			if calls == 1 {
				return openAIMessage{
					Role:    "assistant",
					Content: "Visible answer first.",
					ToolCalls: []openAIToolCall{
						{
							ID: "call-1",
							Function: openAIFunctionCall{
								Name:      "unknown_tool",
								Arguments: "{}",
							},
						},
					},
				}, "tool_calls", nil
			}
			return openAIMessage{Role: "assistant"}, "stop", nil
		},
	}

	var events []string
	err := service.SendMessage(
		context.Background(),
		[]Message{{Role: "user", Content: "question"}},
		ChatContext{},
		StreamCallbacks{
			OnText:      func(text string) { events = append(events, "text:"+text) },
			OnToolStart: func(name, label string) { events = append(events, "tool:"+name) },
		},
	)
	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}

	assertEventOrder(t, events, "text:Visible answer first.", "tool:unknown_tool")
}

func TestGeminiServiceEmitsToolCallTextBeforeRunningTool(t *testing.T) {
	toolServer := mcp.NewToolServer(nil, nil, nil, nil)
	calls := 0
	service := &geminiService{
		toolServer: toolServer,
		generateFn: func(context.Context, geminiGenerateRequest) (geminiGenerateResponse, error) {
			calls++
			if calls == 1 {
				return geminiTestResponse(geminiContent{
					Role: "model",
					Parts: []geminiPart{
						{Text: "Visible answer first."},
						{FunctionCall: &geminiFunctionCall{
							Name: "unknown_tool",
							Args: map[string]any{},
							ID:   "call-1",
						}},
					},
				}, "STOP"), nil
			}
			return geminiTestResponse(geminiContent{Role: "model"}, "STOP"), nil
		},
	}

	var events []string
	err := service.SendMessage(
		context.Background(),
		[]Message{{Role: "user", Content: "question"}},
		ChatContext{},
		StreamCallbacks{
			OnText:      func(text string) { events = append(events, "text:"+text) },
			OnToolStart: func(name, label string) { events = append(events, "tool:"+name) },
		},
	)
	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}

	assertEventOrder(t, events, "text:Visible answer first.", "tool:unknown_tool")
}

func geminiTestResponse(content geminiContent, finishReason string) geminiGenerateResponse {
	return geminiGenerateResponse{
		Candidates: []struct {
			Content      geminiContent `json:"content"`
			FinishReason string        `json:"finishReason"`
		}{
			{Content: content, FinishReason: finishReason},
		},
	}
}

func assertEventOrder(t *testing.T, events []string, wantFirst, wantSecond string) {
	t.Helper()
	if len(events) < 2 {
		t.Fatalf("events = %#v, want at least two events", events)
	}
	if events[0] != wantFirst || events[1] != wantSecond {
		t.Fatalf("events = %#v, want first two [%q, %q]", events, wantFirst, wantSecond)
	}
}
