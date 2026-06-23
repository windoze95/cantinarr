package mcp

import "context"

// IssueStore is the narrow write surface the remediation agent's agent-only
// tools call. Defining it HERE (and injecting a value at ToolServer
// construction, the same way request.Service is injected) breaks the import
// cycle: internal/mcp must NOT import internal/remediation, yet the agent-only
// tools need to write issue rows owned by remediation. remediation.Service
// implements this interface; mcp depends only on the interface.
//
// Wave 2 needs exactly these three methods. propose_action / ask_reporter (which
// would add ProposeAction/AskReporter here) are later waves and are deliberately
// omitted so the read-only core cannot record or hint at a mutation.
type IssueStore interface {
	// PostIssueMessage appends an agent-authored message to an issue's thread.
	PostIssueMessage(ctx context.Context, issueID int64, body string) error
	// ConcludeIssue moves an issue to a terminal state (resolved | wont_fix) with
	// a short closing note.
	ConcludeIssue(ctx context.Context, issueID int64, status, resolution string) error
	// RemediationEnabled reports whether the remediation feature is switched on.
	// Every agent-only tool early-returns a benign result when this is false.
	RemediationEnabled(ctx context.Context) bool
}

// SetIssueStore injects the remediation write surface after construction. It is
// optional: when nil (remediation not wired), the agent-only tools degrade to a
// benign "remediation is not enabled" result and never panic. Wiring it post-
// construction keeps NewToolServer's signature stable and avoids an import cycle
// at the call site in main.go.
func (s *ToolServer) SetIssueStore(store IssueStore) {
	s.issueStore = store
}
