package ai

import (
	"strings"
	"testing"
)

// The multi-library rule exists because an unsteered model does the safe-but-
// slow thing: it resolves "test" to the one plausible library, then burns a
// round-trip asking to confirm its own inference. Assume-and-label is safe
// precisely because every library read names the library it read.
func TestSystemPromptResolvesLooseLibraryReferences(t *testing.T) {
	for _, want := range []string{
		"optional instance_id from list_arr_instances",
		"match it against the real names yourself",
		"if exactly one plausibly fits, use it and say which library you read",
		"ask only when several fit or none do",
		"Never quietly answer from a different library",
	} {
		if !strings.Contains(systemPrompt, want) {
			t.Fatalf("system prompt is missing loose-library-reference guidance %q", want)
		}
	}
}

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
