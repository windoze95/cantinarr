package ai

import "testing"

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
