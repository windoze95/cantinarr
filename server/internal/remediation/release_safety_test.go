package remediation

import (
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/mcp"
)

func TestPrepareReleaseCandidatesForAgentRemovesOpaqueCapabilities(t *testing.T) {
	for _, capability := range []string{
		"opaque-indexer-capability-sentinel",
		"https://indexer.invalid/signed/opaque-path-sentinel",
	} {
		text, candidates := prepareReleaseCandidatesForAgent([]mcp.ReleaseCandidate{{
			Reference: capability,
			IndexerID: 7,
			Title:     "Example.Release",
			Quality:   "WEBDL-1080p",
			Size:      1234,
			Protocol:  "usenet",
			Indexer:   "Example",
		}})
		if len(candidates) != 1 || !isReleaseGUIDFingerprint(candidates[0].Reference) {
			t.Fatalf("candidate reference was not canonicalized: %+v", candidates)
		}
		combined := text + candidates[0].Reference
		if strings.Contains(combined, capability) || strings.Contains(combined, "opaque-path-sentinel") {
			t.Fatalf("agent release result leaked capability %q: %s", capability, combined)
		}
		for _, want := range []string{"Example.Release", "indexer_id: 7", "size_bytes: 1234", "REDACTED release sha256:"} {
			if !strings.Contains(text, want) {
				t.Errorf("safe release result lost %q: %s", want, text)
			}
		}
	}
}

func TestPrepareReleaseCandidatesForAgentIsIdempotent(t *testing.T) {
	reference := releaseGUIDFingerprint("already-safe")
	_, candidates := prepareReleaseCandidatesForAgent([]mcp.ReleaseCandidate{{Reference: reference, IndexerID: 1}})
	if len(candidates) != 1 || candidates[0].Reference != reference {
		t.Fatalf("canonical reference changed: %+v", candidates)
	}
}
