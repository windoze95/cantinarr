package ai

import (
	"strings"
	"testing"
)

func TestSanitizeTranscriptStripsOrphanToolUse(t *testing.T) {
	history := transcript{
		textTranscriptMessage(agentRoleUser, "search"),
		{
			Role: agentRoleAssistant,
			Content: []transcriptBlock{
				{Type: blockTypeText, Text: "Checking."},
				{Type: blockTypeToolUse, ID: "call_orphan", Name: "search_movies", Input: []byte(`{"query":"Alien"}`)},
			},
		},
	}

	sanitized := sanitizeTranscript(history)
	if len(sanitized) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(sanitized))
	}
	if len(sanitized[1].Content) != 1 || sanitized[1].Content[0].Type != blockTypeText {
		t.Fatalf("expected orphan tool_use to be stripped, got %#v", sanitized[1].Content)
	}
}

func TestTrimHistoryStartsAtPlainUserMessage(t *testing.T) {
	history := transcript{
		textTranscriptMessage(agentRoleUser, "old"),
		{
			Role: agentRoleAssistant,
			Content: []transcriptBlock{
				{Type: blockTypeToolUse, ID: "call_1", Name: "search_movies", Input: []byte(`{"query":"Dune"}`)},
			},
		},
		{
			Role: agentRoleUser,
			Content: []transcriptBlock{
				{Type: blockTypeToolResult, ToolUseID: "call_1", Name: "search_movies", Content: "Dune"},
			},
		},
		textTranscriptMessage(agentRoleUser, "new"),
		textTranscriptMessage(agentRoleAssistant, "answer"),
	}

	trimmed := trimHistory(history, 3)
	if len(trimmed) != 2 {
		t.Fatalf("expected trim to start at plain user message and keep 2 messages, got %d", len(trimmed))
	}
	if got := trimmed[0].Content[0].Text; got != "new" {
		t.Fatalf("expected first trimmed message to be new user text, got %q", got)
	}
}

func TestConversationStoreBoundsLargeToolHistoryByBytes(t *testing.T) {
	store := newConversationStore()
	history := transcript{textTranscriptMessage(agentRoleUser, strings.Repeat("u", maxStoredTextBytes*2))}
	for i := 0; i < 15; i++ {
		id := newConversationID()
		history = append(history,
			transcriptMessage{Role: agentRoleAssistant, Content: []transcriptBlock{{
				Type: blockTypeToolUse, ID: id, Name: "get_library",
				Input: jsonBytes(strings.Repeat("x", maxStoredToolInputBytes*2)),
			}}},
			transcriptMessage{Role: agentRoleUser, Content: []transcriptBlock{{
				Type: blockTypeToolResult, ToolUseID: id, Name: "get_library",
				Content: strings.Repeat("r", maxStoredToolResultBytes*2),
			}}},
		)
	}
	history = append(history, textTranscriptMessage(agentRoleAssistant, strings.Repeat("a", maxStoredTextBytes*2)))

	store.Put("large", 1, history)
	stored, ok := store.Get("large", 1)
	if !ok {
		t.Fatal("bounded conversation was not stored")
	}
	if size := transcriptSize(stored); size > maxStoredTranscriptBytes {
		t.Fatalf("stored transcript size = %d, want <= %d", size, maxStoredTranscriptBytes)
	}
	for _, message := range stored {
		for _, block := range message.Content {
			if len(block.Input) > maxStoredToolInputBytes || len(block.Content) > maxStoredToolResultBytes || len(block.Text) > maxStoredTextBytes {
				t.Fatalf("unbounded stored block: %#v", block)
			}
		}
	}
}

func TestTrimHistoryFallsBackToNewestPlainUserMessage(t *testing.T) {
	history := transcript{
		textTranscriptMessage(agentRoleUser, "safe fallback"),
		{Role: agentRoleAssistant, Content: []transcriptBlock{{Type: blockTypeText, Text: strings.Repeat("x", maxStoredTranscriptBytes)}}},
	}
	trimmed := trimHistory(history, maxStoredMessages)
	if len(trimmed) != 1 || trimmed[0].Role != agentRoleUser || trimmed[0].Content[0].Text != "safe fallback" {
		t.Fatalf("unsafe oversized fallback = %#v", trimmed)
	}
}

func jsonBytes(payload string) []byte {
	return []byte(`{"payload":"` + payload + `"}`)
}
