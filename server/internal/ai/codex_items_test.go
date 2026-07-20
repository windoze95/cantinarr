package ai

import (
	"encoding/json"
	"testing"
)

// The golden strings pin the exact wire shapes the pinned codex-app-server
// deserializes (ResponseItem, serde tag "type", snake_case): function_call
// arguments are a JSON-encoded string and function_call_output's output is a
// plain string. TestInstalledAppServerCommandSurface proves the same shapes
// against the checksum-pinned binary in CI.
func TestCodexResponseItemsRendersNativeShapes(t *testing.T) {
	history := transcript{
		textTranscriptMessage(agentRoleUser, "find dune"),
		{Role: agentRoleAssistant, Content: []transcriptBlock{
			{Type: blockTypeText, Text: "Searching now."},
			{Type: blockTypeToolUse, ID: "codex_1", Name: "search_movies", Input: json.RawMessage(`{"query":"dune"}`)},
		}},
		{Role: agentRoleUser, Content: []transcriptBlock{
			{Type: blockTypeToolResult, ToolUseID: "codex_1", Name: "search_movies", Content: "found 2 movies"},
		}},
		textTranscriptMessage(agentRoleAssistant, "Two matches: 1984 and 2021."),
	}

	items := codexResponseItems(history)
	want := []string{
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"find dune"}]}`,
		`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Searching now."}]}`,
		`{"type":"function_call","name":"search_movies","arguments":"{\"query\":\"dune\"}","call_id":"codex_1"}`,
		`{"type":"function_call_output","call_id":"codex_1","output":"found 2 movies"}`,
		`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Two matches: 1984 and 2021."}]}`,
	}
	if len(items) != len(want) {
		t.Fatalf("items = %d, want %d: %s", len(items), len(want), items)
	}
	for i := range want {
		if string(items[i]) != want[i] {
			t.Errorf("item %d = %s, want %s", i, items[i], want[i])
		}
	}
}

func TestCodexResponseItemsSkipsUnusableBlocks(t *testing.T) {
	history := transcript{
		{Role: agentRoleAssistant, Content: []transcriptBlock{
			{Type: blockTypeToolUse, ID: "", Name: "search_movies", Input: json.RawMessage(`{}`)},
			{Type: blockTypeAnthropicThinking, Text: "opaque"},
			{Type: blockTypeToolUse, ID: "codex_2", Name: "get_queue"},
		}},
		{Role: agentRoleUser, Content: []transcriptBlock{
			{Type: blockTypeToolResult, ToolUseID: "", Content: "orphan"},
			{Type: blockTypeToolResult, ToolUseID: "codex_2", Content: "Error: Radarr offline", IsError: true},
		}},
	}

	items := codexResponseItems(history)
	want := []string{
		`{"type":"function_call","name":"get_queue","arguments":"{}","call_id":"codex_2"}`,
		`{"type":"function_call_output","call_id":"codex_2","output":"Error: Radarr offline"}`,
	}
	if len(items) != len(want) {
		t.Fatalf("items = %d, want %d: %s", len(items), len(want), items)
	}
	for i := range want {
		if string(items[i]) != want[i] {
			t.Errorf("item %d = %s, want %s", i, items[i], want[i])
		}
	}
}

func TestCodexNativeTurnSplitsPromptFromHistory(t *testing.T) {
	history := transcript{
		textTranscriptMessage(agentRoleUser, "hi"),
		textTranscriptMessage(agentRoleAssistant, "Hello!"),
		textTranscriptMessage(agentRoleUser, "what's downloading?"),
	}
	items, prompt, ok := codexNativeTurn(history)
	if !ok || prompt != "what's downloading?" {
		t.Fatalf("prompt = %q ok = %v, want the closing user text", prompt, ok)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want the two prior messages", len(items))
	}

	if _, _, ok := codexNativeTurn(nil); ok {
		t.Error("empty transcript must not be native")
	}
	if _, _, ok := codexNativeTurn(transcript{textTranscriptMessage(agentRoleAssistant, "hi")}); ok {
		t.Error("assistant-final transcript must not be native")
	}
	toolFinal := transcript{{Role: agentRoleUser, Content: []transcriptBlock{
		{Type: blockTypeToolResult, ToolUseID: "codex_3", Content: "x"},
	}}}
	if _, _, ok := codexNativeTurn(toolFinal); ok {
		t.Error("tool-result-final transcript must not be native")
	}

	firstTurn := transcript{textTranscriptMessage(agentRoleUser, "first message")}
	items, prompt, ok = codexNativeTurn(firstTurn)
	if !ok || prompt != "first message" || len(items) != 0 {
		t.Fatalf("first turn = (%d items, %q, %v), want native with no injection", len(items), prompt, ok)
	}
}
