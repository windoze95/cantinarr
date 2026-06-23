package mcp

import (
	"context"
	"encoding/json"
)

// IssueStore is the narrow write surface the remediation agent's agent-only
// tools call. Defining it HERE (and injecting a value at ToolServer
// construction, the same way request.Service is injected) breaks the import
// cycle: internal/mcp must NOT import internal/remediation, yet the agent-only
// tools need to write issue rows owned by remediation. remediation.Service
// implements this interface; mcp depends only on the interface.
//
// Wave 3 adds ProposeAction (the agent proposes a mutation; an admin approves
// it; the server replays it — the model never executes anything itself).
type IssueStore interface {
	// PostIssueMessage appends an agent-authored message to an issue's thread.
	PostIssueMessage(ctx context.Context, issueID int64, body string) error
	// ConcludeIssue moves an issue to a terminal state (resolved | wont_fix) with
	// a short closing note.
	ConcludeIssue(ctx context.Context, issueID int64, status, resolution string) error
	// RemediationEnabled reports whether the remediation feature is switched on.
	// Every agent-only tool early-returns a benign result when this is false.
	RemediationEnabled(ctx context.Context) bool
	// ProposeAction records a proposed (admin-approvable) arr mutation against an
	// issue. It validates params against the kind's schema, computes a stable
	// fingerprint, and conditionally inserts an agent_actions row keyed by that
	// fingerprint. The model NEVER executes the mutation: the row sits in
	// 'proposed' until an admin approves it, at which point the server replays the
	// stored params verbatim.
	//
	// proposalID is the row id (existing or new). alreadyExisted is true when a
	// row with the same fingerprint was already present (a re-proposed identical
	// action), so the caller can return an idempotent message. toolUseID is the
	// propose_action tool_use.id, stored so the resume tool_result pairs back to
	// the exact call when the investigation continues after the decision.
	ProposeAction(ctx context.Context, issueID int64, kind string, params json.RawMessage, rationale, toolUseID string) (proposalID int64, alreadyExisted bool, err error)
}

// SetIssueStore injects the remediation write surface after construction. It is
// optional: when nil (remediation not wired), the agent-only tools degrade to a
// benign "remediation is not enabled" result and never panic. Wiring it post-
// construction keeps NewToolServer's signature stable and avoids an import cycle
// at the call site in main.go.
func (s *ToolServer) SetIssueStore(store IssueStore) {
	s.issueStore = store
}
