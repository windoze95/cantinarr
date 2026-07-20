package ai

import (
	"encoding/json"
	"strings"
	"sync"
)

// codexTranscriptBuilder folds the Codex app-server's callback stream into an
// interleaved provider-neutral transcript. The app-server executes dynamic
// tools strictly in sequence, so each tool record closes the assistant text
// that preceded it: [assistant text+tool_use][user tool_result]... followed by
// any trailing assistant text on Finish. Callbacks may arrive from the
// app-server notification goroutine while the handler goroutine finishes, so
// the builder is safe for concurrent use.
type codexTranscriptBuilder struct {
	mu       sync.Mutex
	messages transcript
	text     strings.Builder
	// textWritten caps total stored assistant text across the whole run, not
	// per segment, matching the pre-interleaving bound.
	textWritten int
}

func (b *codexTranscriptBuilder) Text(value string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if remaining := maxStoredTextBytes - b.textWritten; remaining > 0 {
		bounded := boundedString(value, remaining)
		b.text.WriteString(bounded)
		b.textWritten += len(bounded)
	}
}

func (b *codexTranscriptBuilder) ToolRecord(name string, input json.RawMessage, result string, isError bool) {
	recordInput := append(json.RawMessage(nil), input...)
	if len(recordInput) > maxStoredToolInputBytes || !json.Valid(recordInput) {
		recordInput = json.RawMessage(`{"_cantinarr_truncated":true}`)
	}
	id := "codex_" + newConversationID()
	b.mu.Lock()
	defer b.mu.Unlock()
	assistant := transcriptMessage{Role: agentRoleAssistant}
	if text := strings.TrimSpace(b.text.String()); text != "" {
		assistant.Content = append(assistant.Content, transcriptBlock{Type: blockTypeText, Text: text})
	}
	b.text.Reset()
	assistant.Content = append(assistant.Content, transcriptBlock{
		Type: blockTypeToolUse, ID: id, Name: name, Input: recordInput,
	})
	b.messages = append(b.messages,
		assistant,
		transcriptMessage{Role: agentRoleUser, Content: []transcriptBlock{{
			Type:      blockTypeToolResult,
			ToolUseID: id,
			Name:      name,
			Content:   boundedString(result, maxStoredToolResultBytes),
			IsError:   isError,
		}}},
	)
}

// Finish flushes any trailing assistant text and returns the accumulated
// interleaved messages.
func (b *codexTranscriptBuilder) Finish() transcript {
	b.mu.Lock()
	defer b.mu.Unlock()
	if text := strings.TrimSpace(b.text.String()); text != "" {
		b.messages = append(b.messages, textTranscriptMessage(agentRoleAssistant, text))
		b.text.Reset()
	}
	return b.messages
}
