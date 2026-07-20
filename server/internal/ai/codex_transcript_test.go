package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCodexTranscriptBuilderInterleavesTurns(t *testing.T) {
	b := &codexTranscriptBuilder{}
	b.Text("Let me search. ")
	b.ToolRecord("search_movies", json.RawMessage(`{"query":"dune"}`), "found 2 movies", false)
	b.Text("Grabbing the 2021 film.")
	b.ToolRecord("request_media", json.RawMessage(`{"tmdb_id":438631}`), "requested", false)
	b.ToolRecord("check_request_status", json.RawMessage(`{"tmdb_id":438631}`), "pending approval", true)
	b.Text("Done - it needs approval.")

	got := b.Finish()
	if len(got) != 7 {
		t.Fatalf("messages = %d, want 7 (three tool pairs plus trailing text)", len(got))
	}

	first := got[0]
	if first.Role != agentRoleAssistant || len(first.Content) != 2 {
		t.Fatalf("first message = %+v, want assistant with text+tool_use", first)
	}
	if first.Content[0].Type != blockTypeText || first.Content[0].Text != "Let me search." {
		t.Errorf("first text block = %+v, want the trimmed pre-tool text", first.Content[0])
	}
	if first.Content[1].Type != blockTypeToolUse || first.Content[1].Name != "search_movies" {
		t.Errorf("first tool_use = %+v", first.Content[1])
	}
	result := got[1]
	if result.Role != agentRoleUser || len(result.Content) != 1 || result.Content[0].Type != blockTypeToolResult {
		t.Fatalf("second message = %+v, want user tool_result", result)
	}
	if result.Content[0].ToolUseID != first.Content[1].ID || result.Content[0].ToolUseID == "" {
		t.Errorf("tool_result id %q does not link to tool_use id %q", result.Content[0].ToolUseID, first.Content[1].ID)
	}

	// The back-to-back third call carries no interleaved text.
	if third := got[4]; len(third.Content) != 1 || third.Content[0].Type != blockTypeToolUse {
		t.Errorf("no-text tool turn = %+v, want a lone tool_use", third)
	}
	if errResult := got[5]; !errResult.Content[0].IsError {
		t.Error("third tool_result lost its error flag")
	}

	last := got[6]
	if last.Role != agentRoleAssistant || len(last.Content) != 1 || last.Content[0].Text != "Done - it needs approval." {
		t.Errorf("trailing message = %+v, want the final assistant text", last)
	}
}

func TestCodexTranscriptBuilderTextOnly(t *testing.T) {
	b := &codexTranscriptBuilder{}
	b.Text("Just ")
	b.Text("an answer.")
	got := b.Finish()
	if len(got) != 1 || got[0].Role != agentRoleAssistant || got[0].Content[0].Text != "Just an answer." {
		t.Fatalf("messages = %+v, want one assistant text message", got)
	}
}

func TestCodexTranscriptBuilderEmpty(t *testing.T) {
	if got := (&codexTranscriptBuilder{}).Finish(); len(got) != 0 {
		t.Fatalf("messages = %+v, want none", got)
	}
}

func TestCodexTranscriptBuilderBoundsInputs(t *testing.T) {
	b := &codexTranscriptBuilder{}
	oversized := json.RawMessage(`{"blob":"` + strings.Repeat("x", maxStoredToolInputBytes) + `"}`)
	b.ToolRecord("search_movies", oversized, "ok", false)
	b.ToolRecord("search_movies", json.RawMessage(`{"broken":`), "ok", false)
	b.ToolRecord("search_movies", json.RawMessage(`{}`), strings.Repeat("y", maxStoredToolResultBytes+10), false)

	got := b.Finish()
	placeholder := `{"_cantinarr_truncated":true}`
	if input := string(got[0].Content[len(got[0].Content)-1].Input); input != placeholder {
		t.Errorf("oversized input stored as %q, want placeholder", input[:64])
	}
	if input := string(got[2].Content[len(got[2].Content)-1].Input); input != placeholder {
		t.Errorf("invalid input stored as %q, want placeholder", input)
	}
	if result := got[5].Content[0].Content; len(result) > maxStoredToolResultBytes {
		t.Errorf("result length = %d, want at most %d", len(result), maxStoredToolResultBytes)
	}
}

func TestCodexTranscriptBuilderCapsTextAcrossSegments(t *testing.T) {
	b := &codexTranscriptBuilder{}
	b.Text(strings.Repeat("a", maxStoredTextBytes))
	b.ToolRecord("search_movies", json.RawMessage(`{}`), "ok", false)
	// The run-wide cap is already spent; post-tool text must not grow the store.
	b.Text(strings.Repeat("b", 1024))

	got := b.Finish()
	total := 0
	for _, message := range got {
		for _, block := range message.Content {
			if block.Type == blockTypeText {
				total += len(block.Text)
			}
		}
	}
	if total > maxStoredTextBytes {
		t.Fatalf("stored text = %d bytes, want at most %d across the whole run", total, maxStoredTextBytes)
	}
	if len(got) != 2 {
		t.Fatalf("messages = %d, want 2 (capped text folds into the tool turn only)", len(got))
	}
}
