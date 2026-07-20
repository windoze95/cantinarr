package ai

import (
	"context"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"google.golang.org/genai"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/mcp"
)

func TestSubmittedUserTextDoesNotScanBackward(t *testing.T) {
	request := "Set the x265 score to 25"
	tests := []struct {
		name     string
		messages []Message
		want     string
	}{
		{name: "empty submission"},
		{
			name: "prior request followed by assistant",
			messages: []Message{
				{Role: "user", Content: request},
				{Role: "assistant", Content: "That earlier change was applied."},
			},
		},
		{
			name: "prior request followed by empty current user message",
			messages: []Message{
				{Role: "user", Content: request},
				{Role: "assistant", Content: "Anything else?"},
				{Role: "user", Content: ""},
			},
		},
		{
			name: "current trailing user message",
			messages: []Message{
				{Role: "user", Content: "old text"},
				{Role: "assistant", Content: "reply"},
				{Role: "user", Content: request},
			},
			want: request,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := submittedUserText(tt.messages); got != tt.want {
				t.Fatalf("submittedUserText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInteractiveProviderToolCallsCarryTrustedTurnProvenance(t *testing.T) {
	chatCtx := ChatContext{
		UserID:            17,
		Role:              auth.RoleAdmin,
		DeviceID:          "device-17",
		RequireSharedAI:   true,
		TrustedUserText:   "Set the x265 score to 25",
		InteractiveTurnID: "interactive-turn-17",
	}

	tests := []struct {
		name string
		run  func(*mcp.ToolServer) error
	}{
		{
			name: "Anthropic",
			run: func(toolServer *mcp.ToolServer) error {
				_, _, err := (&Service{toolServer: toolServer}).runTool(
					context.Background(),
					anthropic.ToolUseBlock{ID: "tool-anthropic", Name: "get_disk_space"},
					chatCtx,
					StreamCallbacks{},
				)
				return err
			},
		},
		{
			name: "OpenAI",
			run: func(toolServer *mcp.ToolServer) error {
				_, _, err := (&openAIService{toolServer: toolServer}).runOpenAITool(
					context.Background(),
					openAIToolCall{
						ID: "tool-openai",
						Function: openAIFunctionCall{
							Name:      "get_disk_space",
							Arguments: `{}`,
						},
					},
					chatCtx,
					StreamCallbacks{},
				)
				return err
			},
		},
		{
			name: "Gemini",
			run: func(toolServer *mcp.ToolServer) error {
				_, _, err := (&geminiService{toolServer: toolServer}).runGeminiTool(
					context.Background(),
					&genai.FunctionCall{ID: "tool-gemini", Name: "get_disk_space", Args: map[string]any{}},
					chatCtx,
					StreamCallbacks{},
				)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolServer := mcp.NewToolServer(nil, nil, nil, nil)
			var observed mcp.CallContext
			toolServer.SetCallAuthorizer(func(_ context.Context, callCtx mcp.CallContext) (string, error) {
				observed = callCtx
				return auth.RoleAdmin, nil
			})

			if err := tt.run(toolServer); err != nil {
				t.Fatalf("tool dispatch: %v", err)
			}
			assertInteractiveCallProvenance(t, observed, chatCtx)
		})
	}
}

func assertInteractiveCallProvenance(t *testing.T, got mcp.CallContext, want ChatContext) {
	t.Helper()
	if got.Origin != mcp.OriginInteractiveChat ||
		got.TrustedUserText != want.TrustedUserText ||
		got.InteractiveTurnID != want.InteractiveTurnID {
		t.Fatalf("trusted turn provenance = %#v", got)
	}
	if got.UserID != want.UserID || got.Role != want.Role || got.DeviceID != want.DeviceID ||
		got.RequireSharedAI != want.RequireSharedAI || !got.Reauthorize {
		t.Fatalf("interactive actor context = %#v", got)
	}
}
