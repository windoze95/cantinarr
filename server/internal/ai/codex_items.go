package ai

import (
	"encoding/json"
	"strings"
)

// Raw Responses API item shapes accepted by the pinned codex-app-server's
// thread/inject_items (protocol/src/models.rs ResponseItem, serde tag "type",
// snake_case). function_call carries arguments as a JSON-encoded string;
// function_call_output's output is a plain string on the wire, never an
// object. CI proves these shapes against the checksum-pinned binary.
type codexContentItem struct {
	Type string `json:"type"` // input_text | output_text
	Text string `json:"text"`
}

type codexMessageItem struct {
	Type    string             `json:"type"` // message
	Role    string             `json:"role"`
	Content []codexContentItem `json:"content"`
}

type codexFunctionCallItem struct {
	Type      string `json:"type"` // function_call
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	CallID    string `json:"call_id"`
}

type codexFunctionCallOutputItem struct {
	Type   string `json:"type"` // function_call_output
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

// codexNativeTurn splits a resolved conversation into the prior history as
// native Responses items plus the closing user message as the turn prompt.
// ok is false when the transcript does not end in a plain user text message,
// in which case the caller keeps the flattened-replay path. Client-supplied
// fallback transcripts are text-only, so replaying them as native turns adds
// no capability beyond what the base instructions' untrusted-data rule and
// per-call tool reauthorization already govern.
func codexNativeTurn(history transcript) (items []json.RawMessage, prompt string, ok bool) {
	if len(history) == 0 {
		return nil, "", false
	}
	last := history[len(history)-1]
	if last.Role == agentRoleAssistant {
		return nil, "", false
	}
	var text strings.Builder
	for _, block := range last.Content {
		if block.Type != blockTypeText {
			return nil, "", false
		}
		if block.Text == "" {
			continue
		}
		if text.Len() > 0 {
			text.WriteByte('\n')
		}
		text.WriteString(block.Text)
	}
	prompt = strings.TrimSpace(text.String())
	if prompt == "" {
		return nil, "", false
	}
	return codexResponseItems(history[:len(history)-1]), prompt, true
}

// codexResponseItems renders provider-neutral transcript messages as raw
// Responses API items, preserving block order. Tool pairs are emitted as
// matched function_call / function_call_output items (the app-server drops
// orphan outputs at prompt build); blocks without a usable identity and
// provider-opaque continuation blocks are skipped.
func codexResponseItems(messages transcript) []json.RawMessage {
	var items []json.RawMessage
	appendItem := func(item any) {
		encoded, err := json.Marshal(item)
		if err != nil {
			return
		}
		items = append(items, json.RawMessage(encoded))
	}
	for _, message := range messages {
		role := "user"
		contentType := "input_text"
		if message.Role == agentRoleAssistant {
			role = "assistant"
			contentType = "output_text"
		}
		var pending []codexContentItem
		flushText := func() {
			if len(pending) == 0 {
				return
			}
			appendItem(codexMessageItem{Type: "message", Role: role, Content: pending})
			pending = nil
		}
		for _, block := range message.Content {
			switch block.Type {
			case blockTypeText:
				if block.Text != "" {
					pending = append(pending, codexContentItem{Type: contentType, Text: block.Text})
				}
			case blockTypeToolUse:
				if block.ID == "" {
					continue
				}
				flushText()
				arguments := string(block.Input)
				if arguments == "" {
					arguments = "{}"
				}
				appendItem(codexFunctionCallItem{
					Type:      "function_call",
					Name:      block.Name,
					Arguments: arguments,
					CallID:    block.ID,
				})
			case blockTypeToolResult:
				if block.ToolUseID == "" {
					continue
				}
				flushText()
				appendItem(codexFunctionCallOutputItem{
					Type:   "function_call_output",
					CallID: block.ToolUseID,
					Output: block.Content,
				})
			}
		}
		flushText()
	}
	return items
}
