package ai

import (
	"strings"
	"testing"
)

func TestSystemPromptPreservesPendingProfileApplyReference(t *testing.T) {
	want := "call apply_profile_change directly with its reference and do not preview again; a new preview supersedes the pending reference"
	if !strings.Contains(systemPrompt, want) {
		t.Fatalf("system prompt does not tell the model to apply an exact later confirmation without replacing its preview")
	}
}

func TestSystemPromptRequiresCompleteProfileDiffReview(t *testing.T) {
	for _, want := range []string{
		"Show the admin its Target, Expires value, and every Proposed changes line exactly as returned",
		"never omit or summarize the diff",
	} {
		if !strings.Contains(systemPrompt, want) {
			t.Fatalf("system prompt does not require visible review of %q", want)
		}
	}
}
