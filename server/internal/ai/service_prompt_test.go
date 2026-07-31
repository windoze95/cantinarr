package ai

import (
	"strings"
	"testing"
)

func TestSystemPromptUsesExplicitIntentAndSameTurnProfileApply(t *testing.T) {
	for _, want := range []string{
		"Quality-profile edits require an explicit admin request",
		"never make the admin copy a command or capability string",
		"In that same turn, call preview_profile_change",
		"then call apply_profile_change with its reference",
		"Do not apply when the user only asks for diagnosis, options, or a recommendation",
		"records durable before/after history",
	} {
		if !strings.Contains(systemPrompt, want) {
			t.Fatalf("system prompt is missing same-turn profile safety guidance %q", want)
		}
	}
}
