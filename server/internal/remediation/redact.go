package remediation

import (
	"encoding/json"

	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// redactTranscript creates a provider-neutral copy safe for persistence and for
// sending back to a hosted model. It is intentionally applied even to model
// output: an assistant can echo a credential it saw in a legacy transcript, and
// tool inputs are otherwise persisted verbatim for the audit view.
func redactTranscript(history ai.Transcript) ai.Transcript {
	redacted := make(ai.Transcript, len(history))
	for i, message := range history {
		redacted[i] = redactTranscriptMessage(message)
	}
	return redacted
}

func redactTranscriptMessage(message ai.TranscriptMessage) ai.TranscriptMessage {
	redacted := ai.TranscriptMessage{Role: message.Role, Content: make([]ai.TranscriptBlock, len(message.Content))}
	for i, block := range message.Content {
		redactedBlock := block
		redactedBlock.Text = secrets.RedactText(block.Text)
		redactedBlock.Content = secrets.RedactText(block.Content)
		redactedBlock.Input = redactRawJSON(block.Input)
		redacted.Content[i] = redactedBlock
	}
	return redacted
}

func redactRawJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	redacted := secrets.RedactText(string(raw))
	if !json.Valid([]byte(redacted)) {
		// A malformed tool input is still untrusted text. Store a JSON string so
		// the transcript remains valid rather than falling back to raw bytes.
		encoded, _ := json.Marshal(redacted)
		return encoded
	}
	return json.RawMessage(redacted)
}
