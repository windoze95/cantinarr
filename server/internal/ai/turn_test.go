package ai

import (
	"encoding/json"
	"testing"

	openai "github.com/openai/openai-go/v3"
)

func TestOpenAINextTurnParamsRequestsStreamingUsage(t *testing.T) {
	params := openAINextTurnParams(openai.ChatModel("gpt-test"), TurnParams{
		System: "test system prompt",
	})

	payload, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal OpenAI next-turn params: %v", err)
	}

	var request struct {
		StreamOptions *struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	if err := json.Unmarshal(payload, &request); err != nil {
		t.Fatalf("decode OpenAI next-turn request: %v", err)
	}
	if request.StreamOptions == nil {
		t.Fatalf("stream_options missing from OpenAI next-turn request: %s", payload)
	}
	if !request.StreamOptions.IncludeUsage {
		t.Fatalf("stream_options.include_usage = false, want true: %s", payload)
	}
}
