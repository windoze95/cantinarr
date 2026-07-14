package ai

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestConversationStorePreservesOpaqueProviderContinuationState(t *testing.T) {
	store := newConversationStore()
	binding := store.newBinding(1, resolvedAI{
		Source: aiSourcePersonal, Provider: "gemini", APIKey: "account-key",
	})
	const (
		anthropicText      = "Authorization: Bearer synthetic-thought-token"
		anthropicSignature = "signature=synthetic-anthropic-secret"
		anthropicData      = `{"api_key":"synthetic-redacted-state"}`
		geminiText         = "token=synthetic-gemini-thought"
		toolInput          = `{"api_key":"synthetic-signed-tool-input"}`
	)
	signature := []byte("token=synthetic-thought-signature")
	history := transcript{
		textTranscriptMessage(agentRoleUser, "search"),
		{Role: agentRoleAssistant, Content: []transcriptBlock{
			{Type: blockTypeAnthropicThinking, Text: anthropicText, Signature: anthropicSignature},
			{Type: blockTypeAnthropicRedactedThinking, Data: anthropicData},
			{Type: blockTypeGeminiThought, Text: geminiText, ThoughtSignature: signature},
			{Type: blockTypeToolUse, ID: "call-1", Name: "search_movies", Input: []byte(toolInput), ThoughtSignature: signature},
		}},
		{Role: agentRoleUser, Content: []transcriptBlock{{
			Type: blockTypeToolResult, ToolUseID: "call-1", Name: "search_movies", Content: "Dune",
		}}},
	}

	store.Put("opaque", 1, binding, history)
	for i := range signature {
		signature[i] = 9
	}
	stored, ok := store.Get("opaque", 1, binding)
	if !ok {
		t.Fatal("opaque-state conversation was not stored")
	}
	blocks := stored[1].Content
	if blocks[0].Text != anthropicText || blocks[0].Signature != anthropicSignature || blocks[1].Data != anthropicData {
		t.Fatalf("Anthropic state was not preserved: %#v", blocks[:2])
	}
	wantSignature := []byte("token=synthetic-thought-signature")
	if blocks[2].Text != geminiText || !bytes.Equal(blocks[2].ThoughtSignature, wantSignature) ||
		!bytes.Equal(blocks[3].ThoughtSignature, wantSignature) || string(blocks[3].Input) != toolInput {
		t.Fatalf("Gemini thought signatures were not copied: %#v", blocks)
	}
}

func TestSanitizeTranscriptDropsOversizedOpaqueTurnAndOrphanedResults(t *testing.T) {
	tests := []struct {
		name  string
		block transcriptBlock
	}{
		{
			name: "Anthropic signed thinking text",
			block: transcriptBlock{
				Type: blockTypeAnthropicThinking, Text: strings.Repeat("t", maxStoredOpaqueBlockBytes+1), Signature: "signed",
			},
		},
		{
			name: "Anthropic redacted thinking data",
			block: transcriptBlock{
				Type: blockTypeAnthropicRedactedThinking, Data: strings.Repeat("d", maxStoredOpaqueBlockBytes+1),
			},
		},
		{
			name: "Gemini signed thought text",
			block: transcriptBlock{
				Type: blockTypeGeminiThought, Text: strings.Repeat("g", maxStoredOpaqueBlockBytes+1), ThoughtSignature: []byte("signed"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			history := transcript{
				textTranscriptMessage(agentRoleUser, "before"),
				{Role: agentRoleAssistant, Content: []transcriptBlock{
					tt.block,
					{Type: blockTypeToolUse, ID: "call-1", Name: "search_movies", Input: []byte(`{"query":"Dune"}`)},
				}},
				{Role: agentRoleUser, Content: []transcriptBlock{{
					Type: blockTypeToolResult, ToolUseID: "call-1", Name: "search_movies", Content: "Dune",
				}}},
				textTranscriptMessage(agentRoleUser, "after"),
			}

			sanitized := sanitizeTranscript(history)
			if len(sanitized) != 2 || sanitized[0].Content[0].Text != "before" || sanitized[1].Content[0].Text != "after" {
				t.Fatalf("oversized signed turn was partially retained: %#v", sanitized)
			}
		})
	}
}

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
	binding := store.newBinding(1, resolvedAI{
		Source: aiSourcePersonal, Provider: "anthropic", APIKey: "account-one-key",
	})
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

	store.Put("large", 1, binding, history)
	stored, ok := store.Get("large", 1, binding)
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

func TestConversationStoreReusesHistoryOnlyForSameProviderAccountAndModel(t *testing.T) {
	store := newConversationStore()
	history := transcript{textTranscriptMessage(agentRoleUser, "private history")}
	original := store.newBinding(7, resolvedAI{
		Source: aiSourcePersonal, Provider: "openai", Model: "model-a", APIKey: "account-one-key",
	})
	store.Put("conversation", 7, original, history)

	exactAccountAndModel := store.newBinding(7, resolvedAI{
		Source: aiSourcePersonal, Provider: "openai", Model: "model-a", APIKey: "account-one-key",
	})
	stored, ok := store.Get("conversation", 7, exactAccountAndModel)
	if !ok || len(stored) != 1 || stored[0].Content[0].Text != "private history" {
		t.Fatalf("same provider account and model did not resume its history: ok=%v history=%#v", ok, stored)
	}

	tests := []struct {
		name    string
		userID  int64
		binding conversationBinding
	}{
		{
			name:   "different requesting user",
			userID: 8,
			binding: store.newBinding(8, resolvedAI{
				Source: aiSourcePersonal, Provider: "openai", Model: "model-a", APIKey: "account-one-key",
			}),
		},
		{
			name:   "personal account identity changed",
			userID: 7,
			binding: store.newBinding(8, resolvedAI{
				Source: aiSourcePersonal, Provider: "openai", Model: "model-a", APIKey: "account-one-key",
			}),
		},
		{
			name:   "personal to shared source",
			userID: 7,
			binding: store.newBinding(7, resolvedAI{
				Source: aiSourceShared, Provider: "openai", Model: "model-a", APIKey: "account-one-key",
			}),
		},
		{
			name:   "provider changed",
			userID: 7,
			binding: store.newBinding(7, resolvedAI{
				Source: aiSourcePersonal, Provider: "gemini", Model: "model-a", APIKey: "account-one-key",
			}),
		},
		{
			name:   "model changed",
			userID: 7,
			binding: store.newBinding(7, resolvedAI{
				Source: aiSourcePersonal, Provider: "openai", Model: "model-b", APIKey: "account-one-key",
			}),
		},
		{
			name:   "api credential account changed",
			userID: 7,
			binding: store.newBinding(7, resolvedAI{
				Source: aiSourcePersonal, Provider: "openai", Model: "model-a", APIKey: "account-two-key",
			}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, ok := store.Get("conversation", tt.userID, tt.binding); ok || got != nil {
				t.Fatalf("cross-scope history was returned: ok=%v history=%#v", ok, got)
			}
		})
	}

	// A rejected lookup must not damage the original account's conversation.
	if _, ok := store.Get("conversation", 7, original); !ok {
		t.Fatal("scope mismatch removed the original conversation")
	}
	crossUserBinding := store.newBinding(8, resolvedAI{
		Source: aiSourcePersonal, Provider: "openai", Model: "model-a", APIKey: "account-one-key",
	})
	if store.Put("cross-user", 7, crossUserBinding, history) {
		t.Fatal("store accepted a personal binding minted for another user")
	}
	sharedCrossUserBinding := store.newBinding(8, resolvedAI{
		Source: aiSourceShared, Provider: "openai", Model: "model-a", APIKey: "shared-key",
	})
	if store.Put("shared-cross-user", 7, sharedCrossUserBinding, history) {
		t.Fatal("store accepted a shared binding minted for another user")
	}
}

func TestConversationStoreExpirationAndDeletionRemainScoped(t *testing.T) {
	store := newConversationStore()
	binding := store.newBinding(3, resolvedAI{
		Source: aiSourceShared, Provider: "codex",
	})
	store.Put("expired", 3, binding, transcript{textTranscriptMessage(agentRoleUser, "old")})
	store.mu.Lock()
	store.conversations["expired"].updatedAt = time.Now().Add(-conversationTTL - time.Second)
	store.mu.Unlock()
	if got, ok := store.Get("expired", 3, binding); ok || got != nil {
		t.Fatalf("expired history was returned: ok=%v history=%#v", ok, got)
	}

	store.Put("deleted", 3, binding, transcript{textTranscriptMessage(agentRoleUser, "delete me")})
	store.Delete("deleted")
	if got, ok := store.Get("deleted", 3, binding); ok || got != nil {
		t.Fatalf("deleted history was returned: ok=%v history=%#v", ok, got)
	}

	otherBinding := store.newBinding(4, resolvedAI{Source: aiSourcePersonal, Provider: "codex"})
	store.Put("user-3-a", 3, binding, transcript{textTranscriptMessage(agentRoleUser, "three a")})
	store.Put("user-3-b", 3, binding, transcript{textTranscriptMessage(agentRoleUser, "three b")})
	store.Put("user-4", 4, otherBinding, transcript{textTranscriptMessage(agentRoleUser, "four")})
	store.DeleteForUser(3)
	if _, ok := store.Get("user-3-a", 3, binding); ok {
		t.Fatal("DeleteForUser retained the first matching conversation")
	}
	if _, ok := store.Get("user-3-b", 3, binding); ok {
		t.Fatal("DeleteForUser retained the second matching conversation")
	}
	if _, ok := store.Get("user-4", 4, otherBinding); !ok {
		t.Fatal("DeleteForUser removed another user's conversation")
	}
	store.DeleteAll()
	if _, ok := store.Get("user-4", 4, otherBinding); ok {
		t.Fatal("DeleteAll retained a shared-account conversation")
	}
}

func TestConversationStoreRejectsInFlightPutAfterConnectionPurge(t *testing.T) {
	store := newConversationStore()
	personal := resolvedAI{Source: aiSourcePersonal, Provider: "codex", Model: "gpt-personal"}
	stalePersonal := store.newBinding(3, personal)
	store.DeleteForUser(3)
	freshPersonal := store.newBinding(3, personal)
	if !store.Put("personal", 3, freshPersonal, transcript{textTranscriptMessage(agentRoleUser, "fresh personal")}) {
		t.Fatal("fresh personal generation was rejected")
	}
	if store.Put("personal", 3, stalePersonal, transcript{textTranscriptMessage(agentRoleUser, "stale personal")}) {
		t.Fatal("stale personal request resurrected a purged conversation")
	}
	stored, ok := store.Get("personal", 3, freshPersonal)
	if !ok || stored[0].Content[0].Text != "fresh personal" {
		t.Fatalf("stale personal Put replaced fresh state: ok=%v history=%#v", ok, stored)
	}

	shared := resolvedAI{Source: aiSourceShared, Provider: "codex", Model: "gpt-shared"}
	staleShared := store.newBinding(4, shared)
	store.DeleteAll()
	freshShared := store.newBinding(4, shared)
	if !store.Put("shared", 4, freshShared, transcript{textTranscriptMessage(agentRoleUser, "fresh shared")}) {
		t.Fatal("fresh shared generation was rejected")
	}
	if store.Put("shared", 4, staleShared, transcript{textTranscriptMessage(agentRoleUser, "stale shared")}) {
		t.Fatal("stale shared request resurrected a purged conversation")
	}
	stored, ok = store.Get("shared", 4, freshShared)
	if !ok || stored[0].Content[0].Text != "fresh shared" {
		t.Fatalf("stale shared Put replaced fresh state: ok=%v history=%#v", ok, stored)
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
