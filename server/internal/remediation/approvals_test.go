package remediation

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/mcp"
)

// testAdminID is the seeded admin who approves/denies in these tests (a real
// users row, so agent_actions.decided_by's FK is satisfied).
const testAdminID = int64(99)

// fakeExecutor is the test seam for the arr mutation: it records every Execute
// call (so a double-approve can be proven to mutate exactly once) and returns a
// scripted result/err WITHOUT touching any network or arr client.
type fakeExecutor struct {
	mu    sync.Mutex
	calls []execCall
	err   error  // when non-nil, Execute returns this (a definitive failure)
	out   string // result text on success
}

type execCall struct {
	issueID int64
	kind    ActionKind
	params  string
}

func (f *fakeExecutor) Execute(ctx context.Context, issueID int64, kind ActionKind, params json.RawMessage) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, execCall{issueID: issueID, kind: kind, params: string(params)})
	if f.err != nil {
		return "", f.err
	}
	out := f.out
	if out == "" {
		out = "did the thing"
	}
	return out, nil
}

func (f *fakeExecutor) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeExecutor) last() (execCall, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return execCall{}, false
	}
	return f.calls[len(f.calls)-1], true
}

// approvalFixture seeds an in-memory DB with a service (fake executor), an issue,
// a parked run carrying a transcript with a propose_action tool_use, and a
// proposed action row keyed to that tool_use_id. It returns the service, the fake
// executor, the issue id, and the action id — the shape ApproveAction/DenyAction
// operate on after a Runner has parked.
func approvalFixture(t *testing.T) (*Service, *fakeExecutor, int64, int64) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	svc := NewService(database, nil, nil, &fakeNotifier{})
	fx := &fakeExecutor{out: "Removed and blocklisted queue item 7 and started a fresh search."}
	svc.executor = fx

	// Seed the admin who decides (agent_actions.decided_by REFERENCES users(id),
	// so a real admin row must exist for the decision to persist).
	if _, err := database.Exec("INSERT INTO users (id, username, password_hash, role) VALUES (?, 'admin', '', 'admin')", testAdminID); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	// Feature on so a resume is enqueued (the worker pool isn't running in these
	// tests, so the resume job is simply queued and harmless).
	if _, err := svc.SetSettings(Settings{Enabled: true, Autonomy: AutonomyPropose, MaxSteps: 12, MaxTurnTokens: 1024, MaxWallClockSecs: 30, MaxCostMicros: 500000, DailyRunCap: 50, DailyCostCeilingMicros: 5000000}); err != nil {
		t.Fatalf("set settings: %v", err)
	}

	// Seed an issue (movie scope), claimed by a parked run.
	res, err := database.Exec(
		"INSERT INTO issues (source, status, media_type, tmdb_id, title, detail) VALUES ('user','awaiting_approval','movie',42,'Test Movie','wrong content')",
	)
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issueID, _ := res.LastInsertId()

	// A parked run with a transcript that ends in the propose_action tool_use.
	toolUseID := "toolu_propose_1"
	history := []map[string]any{
		{"role": "user", "content": []map[string]any{{"type": "text", "text": "investigate"}}},
		{"role": "assistant", "content": []map[string]any{{"type": "tool_use", "id": toolUseID, "name": "propose_action", "input": map[string]any{}}}},
	}
	htData, _ := json.Marshal(history)
	runRes, err := database.Exec(
		"INSERT INTO agent_runs (issue_id, trigger, status, model, step_count, cost_micros, transcript_json) VALUES (?, 'user_report', ?, 'claude-haiku-4-5', 3, 1200, ?)",
		issueID, runStatusWaitingApproval, string(htData),
	)
	if err != nil {
		t.Fatalf("seed run: %v", err)
	}
	runID, _ := runRes.LastInsertId()
	// Point the issue's active_run_id claim... actually a parked issue has a NULL
	// claim (released on park). Leave active_run_id NULL so Resume can re-claim.
	database.Exec("UPDATE issues SET active_run_id = NULL WHERE id = ?", issueID)

	// The proposed action keyed to the run + tool_use_id.
	params := `{"media_type":"movie","queue_id":7,"action":"blocklist_search"}`
	fp := fingerprint(issueID, ActionRemediateQueue, json.RawMessage(params))
	actRes, err := database.Exec(
		"INSERT INTO agent_actions (issue_id, run_id, tool_use_id, kind, params, rationale, risk, status, fingerprint) VALUES (?, ?, ?, ?, ?, 'because', 'mutating', ?, ?)",
		issueID, runID, toolUseID, string(ActionRemediateQueue), params, ActionProposed, fp,
	)
	if err != nil {
		t.Fatalf("seed action: %v", err)
	}
	actionID, _ := actRes.LastInsertId()
	return svc, fx, issueID, actionID
}

// TestDoubleApproveExecutesExactlyOnce is acceptance (a): two concurrent/back-to-
// back approvals of the same proposal execute the underlying mutation exactly
// once (the CAS proposed->executing lets only one caller through; the other sees
// the row already moved on).
func TestDoubleApproveExecutesExactlyOnce(t *testing.T) {
	svc, fx, _, actionID := approvalFixture(t)

	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			svc.ApproveAction(testAdminID, actionID, nil)
		}()
	}
	wg.Wait()

	if fx.count() != 1 {
		t.Fatalf("executor ran %d times, want exactly 1 (CAS must serialize the double-approve)", fx.count())
	}
	// The action ended executed exactly once.
	act, err := svc.GetAction(actionID)
	if err != nil {
		t.Fatalf("GetAction: %v", err)
	}
	if act.Status != ActionExecuted {
		t.Fatalf("action status = %q, want executed", act.Status)
	}
}

// TestSequentialDoubleApproveIdempotent is the deterministic companion to (a): a
// second approve AFTER the first completed is a no-op (the row is no longer
// proposed), so the mutation still ran exactly once.
func TestSequentialDoubleApproveIdempotent(t *testing.T) {
	svc, fx, _, actionID := approvalFixture(t)

	if _, err := svc.ApproveAction(testAdminID, actionID, nil); err != nil {
		t.Fatalf("first approve: %v", err)
	}
	// Second approve must NOT execute again (status is now executed, not proposed).
	if _, err := svc.ApproveAction(testAdminID, actionID, nil); err == nil {
		t.Fatalf("second approve should error (not proposed)")
	}
	if fx.count() != 1 {
		t.Fatalf("executor ran %d times, want exactly 1", fx.count())
	}
}

// TestApproveExecutesTypedMutationWithRightParams is part of acceptance (c): an
// approval drives the Executor once with the proposal's kind + verbatim params,
// and appends the resume tool_result keyed to the proposal's tool_use_id.
func TestApproveExecutesTypedMutationWithRightParams(t *testing.T) {
	svc, fx, issueID, actionID := approvalFixture(t)

	act, err := svc.ApproveAction(testAdminID, actionID, nil)
	if err != nil {
		t.Fatalf("ApproveAction: %v", err)
	}
	if act.Status != ActionExecuted {
		t.Fatalf("action status = %q, want executed", act.Status)
	}

	// The Executor was driven once, with the right kind + params.
	call, ok := fx.last()
	if !ok || fx.count() != 1 {
		t.Fatalf("executor calls = %d, want 1", fx.count())
	}
	if call.kind != ActionRemediateQueue {
		t.Fatalf("executor kind = %q, want remediate_queue", call.kind)
	}
	if call.issueID != issueID {
		t.Fatalf("executor issueID = %d, want %d", call.issueID, issueID)
	}
	var p RemediateQueueParams
	if err := json.Unmarshal([]byte(call.params), &p); err != nil {
		t.Fatalf("decode executor params: %v", err)
	}
	if p.MediaType != "movie" || p.QueueID != 7 || p.Action != "blocklist_search" {
		t.Fatalf("executor params = %+v, want movie/7/blocklist_search", p)
	}

	// The resume tool_result was appended to the run transcript, keyed to the
	// proposal's tool_use_id, carrying the execution outcome.
	var transcriptJSON string
	if err := svc.db.QueryRow("SELECT transcript_json FROM agent_runs WHERE issue_id = ?", issueID).Scan(&transcriptJSON); err != nil {
		t.Fatalf("load transcript: %v", err)
	}
	var history []map[string]any
	if err := json.Unmarshal([]byte(transcriptJSON), &history); err != nil {
		t.Fatalf("decode transcript: %v", err)
	}
	last := history[len(history)-1]
	content := last["content"].([]any)
	block := content[0].(map[string]any)
	if block["type"] != "tool_result" {
		t.Fatalf("last block type = %v, want tool_result", block["type"])
	}
	if block["tool_use_id"] != "toolu_propose_1" {
		t.Fatalf("resume tool_result tool_use_id = %v, want toolu_propose_1", block["tool_use_id"])
	}
	outcome, _ := block["content"].(string)
	if outcome == "" || outcome[:21] != "Approved and executed" {
		t.Fatalf("resume tool_result content = %q, want it to start with 'Approved and executed'", outcome)
	}

	// A resume job was enqueued (drained here so the test asserts it exists).
	select {
	case j := <-svc.jobs:
		if !j.resume || j.issueID != issueID {
			t.Fatalf("enqueued job = %+v, want resume of issue %d", j, issueID)
		}
	default:
		t.Fatalf("expected a resume job to be enqueued after approval")
	}
}

// TestDenyDoesNotExecuteAndResumes is acceptance (b): a denied action does NOT
// run the mutation, records the denial, appends the denial tool_result, and
// enqueues a resume (which returns the issue to investigating — the resume path,
// not a terminal failure).
func TestDenyDoesNotExecuteAndResumes(t *testing.T) {
	svc, fx, issueID, actionID := approvalFixture(t)

	act, err := svc.DenyAction(testAdminID, actionID, "wrong release")
	if err != nil {
		t.Fatalf("DenyAction: %v", err)
	}
	if act.Status != ActionDenied {
		t.Fatalf("action status = %q, want denied", act.Status)
	}
	if fx.count() != 0 {
		t.Fatalf("executor ran %d times on a denial, want 0", fx.count())
	}
	if act.DenyReason == nil || *act.DenyReason != "wrong release" {
		t.Fatalf("deny_reason = %v, want 'wrong release'", act.DenyReason)
	}

	// The denial tool_result was appended keyed to the tool_use_id.
	var transcriptJSON string
	if err := svc.db.QueryRow("SELECT transcript_json FROM agent_runs WHERE issue_id = ?", issueID).Scan(&transcriptJSON); err != nil {
		t.Fatalf("load transcript: %v", err)
	}
	var history []map[string]any
	if err := json.Unmarshal([]byte(transcriptJSON), &history); err != nil {
		t.Fatalf("decode transcript: %v", err)
	}
	block := history[len(history)-1]["content"].([]any)[0].(map[string]any)
	if block["tool_use_id"] != "toolu_propose_1" {
		t.Fatalf("denial tool_result tool_use_id = %v, want toolu_propose_1", block["tool_use_id"])
	}
	if outcome, _ := block["content"].(string); outcome != "Admin denied: wrong release" {
		t.Fatalf("denial tool_result = %q, want 'Admin denied: wrong release'", outcome)
	}

	// A resume job was enqueued so the agent can try another tack.
	select {
	case j := <-svc.jobs:
		if !j.resume || j.issueID != issueID {
			t.Fatalf("enqueued job = %+v, want resume of issue %d", j, issueID)
		}
	default:
		t.Fatalf("expected a resume job to be enqueued after denial")
	}
}

// TestApproveExecutionFailureMarksFailedNoSilentReexec asserts a definitive
// execution error marks the action failed (never reverted to proposed), so it
// can never be silently re-executed; the resume still fires so the agent reacts.
func TestApproveExecutionFailureMarksFailed(t *testing.T) {
	svc, fx, _, actionID := approvalFixture(t)
	fx.err = context.DeadlineExceeded // a definitive failure for the test

	act, err := svc.ApproveAction(testAdminID, actionID, nil)
	if err != nil {
		t.Fatalf("ApproveAction returned a transport error to the caller: %v", err)
	}
	if act.Status != ActionFailed {
		t.Fatalf("action status = %q, want failed", act.Status)
	}
	// A re-approve must NOT re-run it (no silent re-exec): status is failed.
	if _, err := svc.ApproveAction(testAdminID, actionID, nil); err == nil {
		t.Fatalf("re-approve of a failed action should error")
	}
	if fx.count() != 1 {
		t.Fatalf("executor ran %d times, want exactly 1 (failed action never re-runs)", fx.count())
	}
}

// TestProposeActionFingerprintIdempotent asserts the propose path itself is
// idempotent: re-proposing an identical {issue, kind, params} returns the same
// row and does NOT create a duplicate (UNIQUE(fingerprint) + conditional insert).
func TestProposeActionFingerprintIdempotent(t *testing.T) {
	svc, _, issueID, _ := approvalFixture(t)
	ctx := context.Background()

	params := json.RawMessage(`{"media_type":"movie","queue_id":9,"action":"remove"}`)
	id1, existed1, err := svc.ProposeAction(ctx, issueID, "remediate_queue", params, "r1", "tu_a")
	if err != nil {
		t.Fatalf("first propose: %v", err)
	}
	if existed1 || id1 == 0 {
		t.Fatalf("first propose existed=%v id=%d, want new row", existed1, id1)
	}

	// Re-propose the SAME action (even with different key order in params + a
	// different rationale/tool_use_id): same fingerprint => idempotent.
	reordered := json.RawMessage(`{"action":"remove","queue_id":9,"media_type":"movie"}`)
	id2, existed2, err := svc.ProposeAction(ctx, issueID, "remediate_queue", reordered, "r2", "tu_b")
	if err != nil {
		t.Fatalf("second propose: %v", err)
	}
	if !existed2 {
		t.Fatalf("second propose existed=%v, want true (idempotent)", existed2)
	}
	if id2 != id1 {
		t.Fatalf("second propose id=%d, want the same row %d", id2, id1)
	}

	// Exactly one row exists for that fingerprint.
	var n int
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM agent_actions WHERE issue_id = ? AND kind = 'remediate_queue' AND params LIKE '%\"queue_id\":9%'", issueID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("rows for the re-proposed action = %d, want 1", n)
	}
}

// TestProposeActionRejectsBadParams asserts validation rejects unknown fields and
// missing required values before any row is written.
func TestProposeActionRejectsBadParams(t *testing.T) {
	svc, _, issueID, _ := approvalFixture(t)
	ctx := context.Background()

	// Unknown field.
	if _, _, err := svc.ProposeAction(ctx, issueID, "grab_release", json.RawMessage(`{"media_type":"movie","guid":"x","indexer_id":1,"bogus":true}`), "", "tu"); err == nil {
		t.Fatalf("expected rejection of unknown field")
	}
	// Missing guid/indexer for grab_release.
	if _, _, err := svc.ProposeAction(ctx, issueID, "grab_release", json.RawMessage(`{"media_type":"movie"}`), "", "tu"); err == nil {
		t.Fatalf("expected rejection of missing guid/indexer_id")
	}
	// Unknown kind.
	if _, _, err := svc.ProposeAction(ctx, issueID, "delete_everything", json.RawMessage(`{}`), "", "tu"); err == nil {
		t.Fatalf("expected rejection of unknown kind")
	}
}

// TestProposeApproveExecuteResumeCycle is the end-to-end acceptance (c): the
// Runner investigates, PROPOSES a fix (real agent_actions row, run parks), an
// admin APPROVES (the Executor runs the typed mutation exactly once with the
// proposal's params), and the Runner RESUMES with the outcome appended to the
// transcript keyed to the propose_action tool_use_id, then verifies and concludes.
func TestProposeApproveExecuteResumeCycle(t *testing.T) {
	r, svc, fx, issueID := newProposeCycleRunner(t)

	// 1) Drive the investigation: the model proposes a grab_release. The Runner
	// dispatches propose_action (writing a real proposed row) and PARKS.
	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The issue parked awaiting approval; a proposed action exists; nothing ran.
	issue, _ := svc.GetIssue(issueID)
	if issue.Status != IssueAwaitingApproval {
		t.Fatalf("issue status after propose = %q, want awaiting_approval", issue.Status)
	}
	pending, err := svc.ListPendingActions()
	if err != nil {
		t.Fatalf("ListPendingActions: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending actions = %d, want 1", len(pending))
	}
	actionID := pending[0].ID
	if pending[0].Kind != string(ActionGrabRelease) {
		t.Fatalf("proposed kind = %q, want grab_release", pending[0].Kind)
	}
	if fx.count() != 0 {
		t.Fatalf("executor ran %d times before approval, want 0", fx.count())
	}

	// 2) Admin approves. The Executor runs the typed mutation exactly once with
	// the proposal's verbatim params; a resume job is enqueued.
	if _, err := svc.ApproveAction(testAdminID, actionID, nil); err != nil {
		t.Fatalf("ApproveAction: %v", err)
	}
	if fx.count() != 1 {
		t.Fatalf("executor ran %d times on approval, want exactly 1", fx.count())
	}
	call, _ := fx.last()
	if call.kind != ActionGrabRelease {
		t.Fatalf("executor kind = %q, want grab_release", call.kind)
	}
	var gp GrabReleaseParams
	if err := json.Unmarshal([]byte(call.params), &gp); err != nil {
		t.Fatalf("decode executor params: %v", err)
	}
	if gp.GUID != "abc-guid" || gp.IndexerID != 3 || gp.MediaType != "movie" {
		t.Fatalf("executor params = %+v, want movie/abc-guid/3", gp)
	}

	// Drain the enqueued resume job (the worker pool isn't running in this test).
	select {
	case j := <-svc.jobs:
		if !j.resume {
			t.Fatalf("expected a resume job, got %+v", j)
		}
	default:
		t.Fatalf("expected a resume job after approval")
	}

	// 3) Resume: the agent sees the approval tool_result (keyed to its proposal),
	// verifies read-only, and concludes resolved.
	if err := r.Resume(context.Background(), issueID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	issue, _ = svc.GetIssue(issueID)
	if issue.Status != IssueResolved {
		t.Fatalf("issue status after resume = %q, want resolved", issue.Status)
	}

	// The action ended executed exactly once (no re-exec across the resume).
	if fx.count() != 1 {
		t.Fatalf("executor ran %d times total, want exactly 1", fx.count())
	}
}

// serviceBackedHost is a toolHost whose agent-tool dispatch goes through the real
// Service (so propose_action writes a real agent_actions row and conclude_issue
// sets the terminal state). Read tools return canned text. It mirrors what the
// real ToolServer would do for the agent-only tools, without needing a registry.
type serviceBackedHost struct{ svc *Service }

func (h *serviceBackedHost) ToolsByName(names []string) []mcp.Tool {
	out := make([]mcp.Tool, 0, len(names))
	for _, n := range names {
		out = append(out, mcp.Tool{Name: n})
	}
	return out
}

func (h *serviceBackedHost) ExecuteTool(ctx context.Context, name string, input json.RawMessage, callCtx mcp.CallContext) (*mcp.ToolResult, error) {
	return &mcp.ToolResult{Text: "ok: " + name}, nil
}

func (h *serviceBackedHost) ExecuteAgentTool(ctx context.Context, name string, input json.RawMessage, issueID int64, toolUseID string) (*mcp.AgentToolResult, error) {
	switch name {
	case mcp.ToolPostIssueMessage:
		var a struct {
			Body string `json:"body"`
		}
		json.Unmarshal(input, &a)
		h.svc.PostIssueMessage(ctx, issueID, a.Body)
		return &mcp.AgentToolResult{Text: "posted"}, nil
	case mcp.ToolConcludeIssue:
		var a struct {
			Status     string `json:"status"`
			Resolution string `json:"resolution"`
		}
		json.Unmarshal(input, &a)
		if a.Status != mcp.ConcludeResolved && a.Status != mcp.ConcludeWontFix {
			a.Status = mcp.ConcludeWontFix
		}
		h.svc.ConcludeIssue(ctx, issueID, a.Status, a.Resolution)
		return &mcp.AgentToolResult{Text: "concluded", Concluded: true, Status: a.Status}, nil
	case mcp.ToolProposeAction:
		var a struct {
			Kind      string          `json:"kind"`
			Params    json.RawMessage `json:"params"`
			Rationale string          `json:"rationale"`
		}
		json.Unmarshal(input, &a)
		id, existed, err := h.svc.ProposeAction(ctx, issueID, a.Kind, a.Params, a.Rationale, toolUseID)
		if err != nil {
			return &mcp.AgentToolResult{Text: "could not propose: " + err.Error()}, nil
		}
		if existed {
			return &mcp.AgentToolResult{Text: "already proposed"}, nil
		}
		_ = id
		return &mcp.AgentToolResult{Text: "proposal recorded", Parked: true}, nil
	}
	return &mcp.AgentToolResult{Text: "noop"}, nil
}

// newProposeCycleRunner builds a Runner whose model proposes a grab_release on
// the first invocation, then (after the approval) verifies + concludes on resume.
func newProposeCycleRunner(t *testing.T) (*Runner, *Service, *fakeExecutor, int64) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	svc := NewService(database, nil, nil, &fakeNotifier{})
	fx := &fakeExecutor{out: "Release sent to the download client."}
	svc.executor = fx
	if _, err := svc.SetSettings(Settings{Enabled: true, Autonomy: AutonomyPropose, MaxSteps: 12, MaxTurnTokens: 1024, MaxWallClockSecs: 30, MaxCostMicros: 500000, DailyRunCap: 50, DailyCostCeilingMicros: 5000000}); err != nil {
		t.Fatalf("set settings: %v", err)
	}
	if _, err := database.Exec("INSERT INTO users (id, username, password_hash, role) VALUES (?, 'admin', '', 'admin')", testAdminID); err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	res, err := database.Exec(
		"INSERT INTO issues (source, status, media_type, tmdb_id, title, detail) VALUES ('user','open','movie',42,'Test Movie','bad copy')",
	)
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issueID, _ := res.LastInsertId()

	grabInput := json.RawMessage(`{"kind":"grab_release","params":{"media_type":"movie","guid":"abc-guid","indexer_id":3},"rationale":"better release"}`)
	script := &scriptedTurn{turns: []ai.TranscriptMessage{
		// Turn 1: investigate (a read), then propose a grab_release. The Runner
		// parks after dispatching propose_action.
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{toolUse("r1", "get_history")}},
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{{Type: ai.BlockToolUse, ID: "prop1", Name: mcp.ToolProposeAction, Input: grabInput}}},
		// Turn 2 (resume, after approval): verify read-only, then conclude resolved.
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{toolUse("r2", "get_queue")}},
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{{Type: ai.BlockToolUse, ID: "c1", Name: mcp.ToolConcludeIssue, Input: json.RawMessage(`{"status":"resolved","resolution":"fixed"}`)}}},
	}}

	r := &Runner{
		db:         database,
		svc:        svc,
		toolServer: &serviceBackedHost{svc: svc},
		creds:      newFakeCreds(t, database),
		procToken:  "test",
		newTurn: func(provider, apiKey, model string) (ai.TurnRunner, error) {
			return script, nil
		},
	}
	return r, svc, fx, issueID
}
