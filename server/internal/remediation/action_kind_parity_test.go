package remediation

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/mcp"
)

// The propose_action vocabulary lives twice by necessity — remediation owns the
// typed kinds, mcp owns the wire tool (schema enum, validator, correction text)
// and cannot import remediation. This test is the coupling: one list per
// package, pinned equal here, so a kind added on either side without the other
// fails loudly instead of shipping silently unproposable (delete_media_files
// was missing from the correction text for exactly this reason).
func TestProposeActionKindVocabularyParity(t *testing.T) {
	typed := make([]string, len(ProposableActionKinds))
	for i, kind := range ProposableActionKinds {
		typed[i] = string(kind)
	}
	wire := mcp.ProposableActionKinds()

	sortedTyped := slices.Clone(typed)
	sortedWire := slices.Clone(wire)
	slices.Sort(sortedTyped)
	slices.Sort(sortedWire)
	if !slices.Equal(sortedTyped, sortedWire) {
		t.Fatalf("action-kind vocabulary drifted:\n  remediation: %v\n  mcp wire:    %v", typed, wire)
	}

	// Every canonical kind must be KNOWN to the typed validator: garbage params
	// must fail on their shape, never as an unknown kind. This binds the slice
	// to the real switch in validateActionParams, so a case added without a
	// slice entry (or vice versa) cannot pass its own feature tests.
	for _, kind := range ProposableActionKinds {
		if _, err := validateActionParams(kind, json.RawMessage(`{}`)); err != nil &&
			strings.Contains(err.Error(), "unknown action kind") {
			t.Errorf("validateActionParams does not recognize canonical kind %q: %v", kind, err)
		}
	}
}
