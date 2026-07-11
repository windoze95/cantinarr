package remediation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/auth"
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

type partialTestError struct{}

func (partialTestError) Error() string         { return "old download removed; replacement search failed" }
func (partialTestError) PartialMutation() bool { return true }

type notStartedTestError struct{}

func (notStartedTestError) Error() string            { return "fresh scoped release no longer exists" }
func (notStartedTestError) MutationNotStarted() bool { return true }

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
	if _, err := database.Exec(
		`INSERT INTO service_instances (id, service_type, name, url, api_key)
		 VALUES ('radarr-main', 'radarr', 'Main Movies', 'http://radarr.test', 'key')`,
	); err != nil {
		t.Fatalf("seed target instance: %v", err)
	}
	// Feature on so a resume is enqueued (the worker pool isn't running in these
	// tests, so the resume job is simply queued and harmless).
	if _, err := svc.SetSettings(Settings{Enabled: true, Mode: ModeSupervised, MaxSteps: 12, MaxTurnTokens: 1024, MaxWallClockSecs: 30, MaxCostMicros: 500000, DailyRunCap: 50, DailyCostCeilingMicros: 5000000}); err != nil {
		t.Fatalf("set settings: %v", err)
	}

	// Seed an issue (movie scope), claimed by a parked run.
	res, err := database.Exec(
		"INSERT INTO issues (source, status, media_type, tmdb_id, title, detail, instance_id) VALUES ('user','awaiting_approval','movie',42,'Test Movie','wrong content','radarr-main')",
	)
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issueID, _ := res.LastInsertId()

	// A parked run with a transcript shaped exactly as the Runner persists it on
	// park: the assistant turn with the propose_action tool_use, then a user turn
	// carrying the PLACEHOLDER tool_result for that tool_use (so every tool_use is
	// answered). The decision must REPLACE this placeholder in place, not append a
	// second tool_result for the same id (which a real provider rejects).
	toolUseID := "toolu_propose_1"
	history := []map[string]any{
		{"role": "user", "content": []map[string]any{{"type": "text", "text": "investigate"}}},
		{"role": "assistant", "content": []map[string]any{{"type": "tool_use", "id": toolUseID, "name": "propose_action", "input": map[string]any{}}}},
		{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": toolUseID, "name": "propose_action", "content": "Proposal #1 recorded; awaiting admin approval."}}},
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
	fp := fingerprint(issueID, runID, toolUseID, ActionRemediateQueue, json.RawMessage(params))
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

func TestPartialMutationIsNotReportedAsCleanFailure(t *testing.T) {
	svc, fx, issueID, actionID := approvalFixture(t)
	fx.err = partialTestError{}
	action, err := svc.ApproveAction(testAdminID, actionID, nil)
	if err != nil {
		t.Fatalf("ApproveAction: %v", err)
	}
	if action.Status != ActionOutcomeUnknown {
		t.Fatalf("partial action status = %q, want outcome_unknown", action.Status)
	}
	if action.ResultText == nil || !strings.Contains(*action.ResultText, "Partially executed") {
		t.Fatalf("partial result = %v", action.ResultText)
	}
	assertUnknownOutcomeNeedsAdmin(t, svc, issueID)
}

func TestPreflightRejectionIsDefinitiveFailureNotUnknownOutcome(t *testing.T) {
	svc, fx, _, actionID := approvalFixture(t)
	fx.err = notStartedTestError{}
	action, err := svc.ApproveAction(testAdminID, actionID, nil)
	if err != nil {
		t.Fatalf("ApproveAction: %v", err)
	}
	if action.Status != ActionFailed {
		t.Fatalf("preflight action status = %q, want failed", action.Status)
	}
	if action.ResultText == nil || !strings.HasPrefix(*action.ResultText, "Not executed:") {
		t.Fatalf("preflight result = %v", action.ResultText)
	}
}

func TestRecoverDurableDecisionRebuildsResumeHandoff(t *testing.T) {
	svc, _, issueID, actionID := approvalFixture(t)
	if _, err := svc.db.Exec(
		"UPDATE agent_actions SET status = ?, result_text = ? WHERE id = ?",
		ActionExecuted, "replacement queued", actionID,
	); err != nil {
		t.Fatalf("seed decided action: %v", err)
	}
	svc.recoverDecisionHandoffs()
	var issueStatus, runStatus string
	if err := svc.db.QueryRow(
		`SELECT i.status, r.status FROM issues i JOIN agent_runs r ON r.issue_id = i.id
		 WHERE i.id = ?`, issueID,
	).Scan(&issueStatus, &runStatus); err != nil {
		t.Fatalf("load handoff: %v", err)
	}
	if issueStatus != IssueInvestigating || runStatus != runStatusResumePending {
		t.Fatalf("recovered handoff = issue %q run %q", issueStatus, runStatus)
	}
	assertWellFormedResume(t, loadTranscript(t, svc, issueID), "toolu_propose_1", "Approved and executed: replacement queued")
}

func TestRecoverUnknownOutcomeStopsInsteadOfResuming(t *testing.T) {
	svc, _, issueID, actionID := approvalFixture(t)
	if _, err := svc.db.Exec(
		"UPDATE agent_actions SET status = ?, result_text = ? WHERE id = ?",
		ActionOutcomeUnknown, "dispatch outcome unknown", actionID,
	); err != nil {
		t.Fatalf("seed unknown action: %v", err)
	}
	svc.recoverDecisionHandoffs()
	assertUnknownOutcomeNeedsAdmin(t, svc, issueID)
}

func TestClearedAutoSignalDoesNotCloseUnknownOutcome(t *testing.T) {
	svc, fx, issueID, actionID := approvalFixture(t)
	if _, err := svc.db.Exec(
		"UPDATE issues SET source = ?, instance_id = 'sonarr-1', download_id = 'download-1' WHERE id = ?",
		SourceAuto, issueID,
	); err != nil {
		t.Fatalf("scope auto issue: %v", err)
	}
	fx.err = partialTestError{}
	if _, err := svc.ApproveAction(testAdminID, actionID, nil); err != nil {
		t.Fatalf("ApproveAction: %v", err)
	}

	_ = svc.concludeIssue(context.Background(), issueID, IssueResolved,
		"The exact episode is now present in Sonarr.", ResolutionArrStateCleared)
	_ = svc.concludeIssue(context.Background(), issueID, IssueResolved,
		"The exact episode is now present in Sonarr.", ResolutionArrStateCleared) // evidence is idempotent
	issue, err := svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Status != IssueNeedsAdmin || issue.ClosedAt != nil {
		t.Fatalf("unknown outcome issue closed after signal cleared: status=%q closed=%v", issue.Status, issue.ClosedAt)
	}
	var evidenceCount int
	if err := svc.db.QueryRow(
		`SELECT COUNT(*) FROM issue_messages
		 WHERE issue_id = ? AND author_kind = ? AND body LIKE '%unknown or partial outcome%'`,
		issueID, AuthorSystem,
	).Scan(&evidenceCount); err != nil {
		t.Fatalf("count cleared evidence: %v", err)
	}
	if evidenceCount != 1 {
		t.Fatalf("cleared-signal evidence rows = %d, want 1", evidenceCount)
	}
}

func TestRecoverDecisionDoesNotBypassNewerPendingProposal(t *testing.T) {
	svc, _, issueID, historicalID := approvalFixture(t)
	var runID int64
	if err := svc.db.QueryRow("SELECT run_id FROM agent_actions WHERE id = ?", historicalID).Scan(&runID); err != nil {
		t.Fatalf("load run: %v", err)
	}
	if _, err := svc.db.Exec(
		"UPDATE agent_actions SET status = ?, result_text = ? WHERE id = ?",
		ActionExecuted, "historical result", historicalID,
	); err != nil {
		t.Fatalf("terminalize historical action: %v", err)
	}
	params := json.RawMessage(`{"media_type":"movie","tmdb_id":42}`)
	fp := fingerprint(issueID, runID, "toolu_propose_2", ActionRescan, params)
	current, err := svc.db.Exec(
		`INSERT INTO agent_actions
		 (issue_id, run_id, tool_use_id, kind, params, rationale, status, fingerprint)
		 VALUES (?, ?, 'toolu_propose_2', ?, ?, 'current proposal', ?, ?)`,
		issueID, runID, string(ActionRescan), string(params), ActionProposed, fp,
	)
	if err != nil {
		t.Fatalf("seed current proposal: %v", err)
	}
	currentID, _ := current.LastInsertId()

	svc.recoverDecisionHandoffs()

	var issueStatus, runStatus, currentStatus string
	if err := svc.db.QueryRow(
		`SELECT i.status, r.status FROM issues i JOIN agent_runs r ON r.id = ? WHERE i.id = ?`,
		runID, issueID,
	).Scan(&issueStatus, &runStatus); err != nil {
		t.Fatalf("load gate: %v", err)
	}
	if err := svc.db.QueryRow("SELECT status FROM agent_actions WHERE id = ?", currentID).Scan(&currentStatus); err != nil {
		t.Fatalf("load current action: %v", err)
	}
	if issueStatus != IssueAwaitingApproval || runStatus != runStatusWaitingApproval || currentStatus != ActionProposed {
		t.Fatalf("newer gate was bypassed: issue=%q run=%q action=%q", issueStatus, runStatus, currentStatus)
	}
}

func TestClaimResumeConsumesOneExactHandoff(t *testing.T) {
	svc, _, issueID, actionID := approvalFixture(t)
	if _, err := svc.ApproveAction(testAdminID, actionID, nil); err != nil {
		t.Fatalf("ApproveAction: %v", err)
	}
	select {
	case <-svc.jobs:
	default:
		t.Fatal("expected resume hint")
	}
	r := &Runner{db: svc.db}
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, claimed, err := r.claimResume(issueID)
			if err != nil {
				t.Errorf("claimResume: %v", err)
			}
			results <- claimed
		}()
	}
	wg.Wait()
	close(results)
	claimed := 0
	for result := range results {
		if result {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("successful resume claims = %d, want exactly 1", claimed)
	}
}

func TestInvalidOpenApprovalGateIsSuperseded(t *testing.T) {
	svc, fx, issueID, actionID := approvalFixture(t)
	if _, err := svc.db.Exec("UPDATE agent_runs SET status = 'aborted' WHERE issue_id = ?", issueID); err != nil {
		t.Fatalf("invalidate gate: %v", err)
	}
	action, err := svc.ApproveAction(testAdminID, actionID, nil)
	if err != nil {
		t.Fatalf("ApproveAction: %v", err)
	}
	if action.Status != ActionSuperseded || action.CanDecide {
		t.Fatalf("invalid gate action = %q can_decide=%v", action.Status, action.CanDecide)
	}
	if fx.count() != 0 {
		t.Fatalf("executor ran %d times for invalid gate", fx.count())
	}
}

func TestGiveUpSupersedesProposalWithRunTransition(t *testing.T) {
	svc, _, issueID, actionID := approvalFixture(t)
	var runID int64
	if err := svc.db.QueryRow("SELECT run_id FROM agent_actions WHERE id = ?", actionID).Scan(&runID); err != nil {
		t.Fatalf("load run: %v", err)
	}
	// Model the crash-sensitive interval after propose_action inserted its row but
	// before the Runner could persist the approval park.
	if _, err := svc.db.Exec("UPDATE agent_runs SET status = ? WHERE id = ?", runStatusRunning, runID); err != nil {
		t.Fatalf("activate run: %v", err)
	}
	if _, err := svc.db.Exec(
		"UPDATE issues SET status = ?, active_run_id = ? WHERE id = ?",
		IssueInvestigating, runID, issueID,
	); err != nil {
		t.Fatalf("claim issue: %v", err)
	}

	transitioned, err := svc.GiveUpIssue(context.Background(), issueID, runID,
		stopInfrastructure, "internal state error", "administrator review required")
	if err != nil || !transitioned {
		t.Fatalf("GiveUpIssue = %v, %v", transitioned, err)
	}
	var issueStatus, runStatus, actionStatus string
	if err := svc.db.QueryRow(
		`SELECT i.status, r.status, a.status
		 FROM issues i JOIN agent_runs r ON r.id = ? JOIN agent_actions a ON a.id = ?
		 WHERE i.id = ?`, runID, actionID, issueID,
	).Scan(&issueStatus, &runStatus, &actionStatus); err != nil {
		t.Fatalf("load aggregate: %v", err)
	}
	if issueStatus != IssueNeedsAdmin || runStatus != runStatusGaveUp || actionStatus != ActionSuperseded {
		t.Fatalf("give-up aggregate = issue %q run %q action %q", issueStatus, runStatus, actionStatus)
	}
}

func TestUnclaimedGiveUpCannotDisruptApprovalGate(t *testing.T) {
	svc, _, issueID, actionID := approvalFixture(t)
	transitioned, err := svc.GiveUpIssue(context.Background(), issueID, 0,
		stopModelError, "provider unavailable", "administrator review required")
	if err != nil {
		t.Fatalf("GiveUpIssue: %v", err)
	}
	if transitioned {
		t.Fatal("stale unclaimed give-up overwrote a live approval gate")
	}
	action, err := svc.GetAction(actionID)
	if err != nil {
		t.Fatalf("GetAction: %v", err)
	}
	issue, err := svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Status != IssueAwaitingApproval || action.Status != ActionProposed || !action.CanDecide {
		t.Fatalf("approval gate changed: issue=%q action=%q can_decide=%v", issue.Status, action.Status, action.CanDecide)
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
	// Second approve returns the durable result so a client can safely reconcile
	// a lost response, but must NOT execute again.
	if act, err := svc.ApproveAction(testAdminID, actionID, nil); err != nil || act.Status != ActionExecuted {
		t.Fatalf("second approve = (%+v, %v), want executed result", act, err)
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

	// The decision REPLACED the parked placeholder tool_result in place (keyed to
	// the proposal's tool_use_id), carrying the execution outcome — and there is
	// still EXACTLY ONE tool_result for that id (no duplicate that a real provider
	// would 400 on).
	transcriptJSON := loadTranscript(t, svc, issueID)
	assertWellFormedResume(t, transcriptJSON, "toolu_propose_1", "Approved and executed")

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

	// The denial REPLACED the placeholder tool_result in place (keyed to the
	// tool_use_id), with exactly one tool_result for that id.
	transcriptJSON := loadTranscript(t, svc, issueID)
	assertWellFormedResume(t, transcriptJSON, "toolu_propose_1", "Admin denied: wrong release")

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

func TestDenyRejectsOversizedNoteWithoutChangingGate(t *testing.T) {
	svc, _, _, actionID := approvalFixture(t)
	if _, err := svc.DenyAction(testAdminID, actionID, strings.Repeat("x", maxIssueReplyBytes+1)); err == nil {
		t.Fatal("oversized denial note was accepted")
	}
	action, err := svc.GetAction(actionID)
	if err != nil {
		t.Fatalf("GetAction: %v", err)
	}
	if action.Status != ActionProposed || !action.CanDecide {
		t.Fatalf("gate changed after rejected note: status=%q can_decide=%v", action.Status, action.CanDecide)
	}
}

func TestActionAPIsExposeImmutableTargetInstance(t *testing.T) {
	svc, _, issueID, actionID := approvalFixture(t)

	assertTarget := func(name string, action *AgentAction) {
		t.Helper()
		if action.InstanceID != "radarr-main" || action.InstanceName != "Main Movies" || action.InstanceServiceType != "radarr" {
			t.Fatalf("%s target = %q/%q/%q, want radarr-main/Main Movies/radarr",
				name, action.InstanceID, action.InstanceName, action.InstanceServiceType)
		}
	}

	got, err := svc.GetAction(actionID)
	if err != nil {
		t.Fatalf("GetAction: %v", err)
	}
	assertTarget("get", got)
	wire, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal action DTO: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(wire, &payload); err != nil {
		t.Fatalf("decode action DTO: %v", err)
	}
	if payload["instance_id"] != "radarr-main" || payload["instance_name"] != "Main Movies" || payload["instance_service_type"] != "radarr" {
		t.Fatalf("action JSON target = %#v", payload)
	}

	listed, err := svc.ListActions("all")
	if err != nil || len(listed) != 1 {
		t.Fatalf("ListActions = %d actions, err %v", len(listed), err)
	}
	assertTarget("list", &listed[0])

	activity, err := svc.GetIssueActivity(issueID)
	if err != nil || len(activity.Actions) != 1 {
		t.Fatalf("GetIssueActivity = %d actions, err %v", len(activity.Actions), err)
	}
	assertTarget("activity", &activity.Actions[0])
}

func TestDenyAfterApprovalReturnsConflict(t *testing.T) {
	svc, _, _, actionID := approvalFixture(t)
	if _, err := svc.db.Exec(
		"UPDATE agent_actions SET status = ?, decided_by = ?, decided_at = CURRENT_TIMESTAMP WHERE id = ?",
		ActionExecuting, testAdminID, actionID,
	); err != nil {
		t.Fatalf("seed approval winner: %v", err)
	}

	if _, err := svc.DenyAction(testAdminID, actionID, "too late"); !errors.Is(err, ErrActionDecisionConflict) {
		t.Fatalf("DenyAction error = %v, want decision conflict", err)
	}
	action, err := svc.GetAction(actionID)
	if err != nil {
		t.Fatalf("GetAction: %v", err)
	}
	if action.Status != ActionExecuting || action.DenyReason != nil {
		t.Fatalf("approval winner changed by denial: status=%q deny_reason=%v", action.Status, action.DenyReason)
	}
}

func TestDenyConflictReturnsHTTP409(t *testing.T) {
	svc, _, _, actionID := approvalFixture(t)
	if _, err := svc.db.Exec(
		"UPDATE agent_actions SET status = ?, decided_by = ?, decided_at = CURRENT_TIMESTAMP WHERE id = ?",
		ActionExecuted, testAdminID, actionID,
	); err != nil {
		t.Fatalf("seed approval winner: %v", err)
	}

	id := strconv.FormatInt(actionID, 10)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/agent-actions/"+id+"/deny", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, auth.ClaimsKey, &auth.Claims{UserID: testAdminID, Role: auth.RoleAdmin})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	NewHandler(svc).DenyAction(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("deny status = %d, body %s; want 409", rec.Code, rec.Body.String())
	}
}

// A transport failure after dispatch cannot prove whether the arr accepted the
// mutation, so it is outcome_unknown and is never silently re-executed.
func TestApproveExecutionFailureMarksOutcomeUnknown(t *testing.T) {
	svc, fx, issueID, actionID := approvalFixture(t)
	fx.err = context.DeadlineExceeded

	act, err := svc.ApproveAction(testAdminID, actionID, nil)
	if err != nil {
		t.Fatalf("ApproveAction returned a transport error to the caller: %v", err)
	}
	if act.Status != ActionOutcomeUnknown {
		t.Fatalf("action status = %q, want outcome_unknown", act.Status)
	}
	// A re-approve reconciles the stored failure but must never re-run it.
	if again, err := svc.ApproveAction(testAdminID, actionID, nil); err != nil || again.Status != ActionOutcomeUnknown {
		t.Fatalf("re-approve = (%+v, %v), want stored unknown outcome", again, err)
	}
	if fx.count() != 1 {
		t.Fatalf("executor ran %d times, want exactly 1 (failed action never re-runs)", fx.count())
	}
	assertUnknownOutcomeNeedsAdmin(t, svc, issueID)
}

func assertUnknownOutcomeNeedsAdmin(t *testing.T, svc *Service, issueID int64) {
	t.Helper()
	var issueStatus, runStatus, stopReason string
	var activeRun sql.NullInt64
	if err := svc.db.QueryRow(
		`SELECT i.status, i.active_run_id, r.status, COALESCE(r.stop_reason, '')
		 FROM issues i JOIN agent_runs r ON r.issue_id = i.id WHERE i.id = ?`, issueID,
	).Scan(&issueStatus, &activeRun, &runStatus, &stopReason); err != nil {
		t.Fatalf("load unknown outcome boundary: %v", err)
	}
	if issueStatus != IssueNeedsAdmin || activeRun.Valid || runStatus != "aborted" || stopReason != "action_outcome_unknown" {
		t.Fatalf("unknown outcome boundary = issue %s active=%v run %s/%s", issueStatus, activeRun, runStatus, stopReason)
	}
	select {
	case job := <-svc.jobs:
		t.Fatalf("unknown outcome enqueued model resume: %+v", job)
	default:
	}
}

func TestExternalResolutionSupersedesProposalAndAbortsRun(t *testing.T) {
	svc, fx, issueID, actionID := approvalFixture(t)
	if err := svc.concludeIssue(context.Background(), issueID, IssueResolved,
		"queue signal cleared", ResolutionArrStateCleared); err != nil {
		t.Fatalf("concludeIssue: %v", err)
	}

	action, err := svc.GetAction(actionID)
	if err != nil {
		t.Fatalf("GetAction: %v", err)
	}
	if action.Status != ActionSuperseded || action.CanDecide {
		t.Fatalf("action = status %q can_decide=%v, want superseded/false", action.Status, action.CanDecide)
	}
	var runStatus, stopReason string
	if err := svc.db.QueryRow(
		"SELECT status, COALESCE(stop_reason,'') FROM agent_runs WHERE issue_id = ?", issueID,
	).Scan(&runStatus, &stopReason); err != nil {
		t.Fatalf("load run: %v", err)
	}
	if runStatus != "aborted" || stopReason != "external_resolution" {
		t.Fatalf("run = %s/%s, want aborted/external_resolution", runStatus, stopReason)
	}
	if _, err := svc.ApproveAction(testAdminID, actionID, nil); err == nil {
		t.Fatal("approving a superseded action should conflict")
	}
	if fx.count() != 0 {
		t.Fatalf("executor ran %d times, want 0", fx.count())
	}
}

func TestDismissSupersedesProposal(t *testing.T) {
	svc, fx, issueID, actionID := approvalFixture(t)
	if err := svc.DismissIssue(issueID); err != nil {
		t.Fatalf("DismissIssue: %v", err)
	}
	action, err := svc.GetAction(actionID)
	if err != nil {
		t.Fatalf("GetAction: %v", err)
	}
	if action.Status != ActionSuperseded || action.CanDecide {
		t.Fatalf("action = status %q can_decide=%v, want superseded/false", action.Status, action.CanDecide)
	}
	if fx.count() != 0 {
		t.Fatalf("executor ran %d times, want 0", fx.count())
	}
}

func TestProposalScopeMustMatchIssue(t *testing.T) {
	svc, _, issueID, seededActionID := approvalFixture(t)
	if _, err := svc.db.Exec("DELETE FROM agent_actions WHERE id = ?", seededActionID); err != nil {
		t.Fatalf("clear fixture proposal: %v", err)
	}
	_, _, err := svc.ProposeAction(context.Background(), issueID, "trigger_search",
		json.RawMessage(`{"media_type":"tv","tmdb_id":42,"season":1}`), "wrong service", "tu")
	if err == nil {
		t.Fatal("proposal with media_type outside the issue scope should fail")
	}
}

// TestProposeActionFingerprintIdempotent asserts the propose path itself is
// idempotent: re-proposing an identical {issue, kind, params} returns the same
// row and does NOT create a duplicate (UNIQUE(fingerprint) + conditional insert).
func TestProposeActionFingerprintIdempotent(t *testing.T) {
	svc, _, issueID, seededActionID := approvalFixture(t)
	ctx := context.Background()
	if _, err := svc.db.Exec("DELETE FROM agent_actions WHERE id = ?", seededActionID); err != nil {
		t.Fatalf("clear fixture proposal: %v", err)
	}
	var runID int64
	if err := svc.db.QueryRow("SELECT id FROM agent_runs WHERE issue_id = ?", issueID).Scan(&runID); err != nil {
		t.Fatalf("load fixture run: %v", err)
	}
	if _, err := svc.db.Exec("UPDATE agent_runs SET status = ? WHERE id = ?", runStatusRunning, runID); err != nil {
		t.Fatalf("activate fixture run: %v", err)
	}
	if _, err := svc.db.Exec("UPDATE issues SET status = ?, active_run_id = ? WHERE id = ?", IssueInvestigating, runID, issueID); err != nil {
		t.Fatalf("claim fixture issue: %v", err)
	}

	params := json.RawMessage(`{"media_type":"movie","queue_id":9,"action":"remove"}`)
	id1, existed1, err := svc.ProposeAction(ctx, issueID, "remediate_queue", params, "r1", "tu_a")
	if err != nil {
		t.Fatalf("first propose: %v", err)
	}
	if existed1 || id1 == 0 {
		t.Fatalf("first propose existed=%v id=%d, want new row", existed1, id1)
	}

	// Retry the SAME model tool gate with reordered params and a different
	// rationale: same fingerprint => idempotent.
	reordered := json.RawMessage(`{"action":"remove","queue_id":9,"media_type":"movie"}`)
	id2, existed2, err := svc.ProposeAction(ctx, issueID, "remediate_queue", reordered, "r2", "tu_a")
	if err != nil {
		t.Fatalf("second propose: %v", err)
	}
	if !existed2 {
		t.Fatalf("second propose existed=%v, want true (idempotent)", existed2)
	}
	if id2 != id1 {
		t.Fatalf("second propose id=%d, want the same row %d", id2, id1)
	}

	// Once that gate is terminal, a later model tool gate may deliberately offer
	// the same fix again and must receive a new audit row.
	if _, err := svc.db.Exec("UPDATE agent_actions SET status = ? WHERE id = ?", ActionDenied, id1); err != nil {
		t.Fatalf("terminalize first proposal: %v", err)
	}
	id3, existed3, err := svc.ProposeAction(ctx, issueID, "remediate_queue", params, "new attempt", "tu_b")
	if err != nil {
		t.Fatalf("later re-proposal: %v", err)
	}
	if existed3 || id3 == id1 {
		t.Fatalf("later gate returned existed=%v id=%d, want a fresh row", existed3, id3)
	}

	// Each distinct gate has one row; the retry of tu_a did not add a third.
	var n int
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM agent_actions WHERE issue_id = ? AND kind = 'remediate_queue' AND params LIKE '%\"queue_id\":9%'", issueID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("rows for the action across two gates = %d, want 2", n)
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
	// Negative identifiers are not valid arr/indexer identities.
	if _, _, err := svc.ProposeAction(ctx, issueID, "grab_release", json.RawMessage(`{"media_type":"movie","guid":"x","indexer_id":-1}`), "", "tu"); err == nil {
		t.Fatalf("expected rejection of negative indexer_id")
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
	var persistedSteps int
	if err := svc.db.QueryRow("SELECT step_count FROM agent_runs WHERE issue_id = ?", issueID).Scan(&persistedSteps); err != nil {
		t.Fatalf("load parked step_count: %v", err)
	}
	if persistedSteps != 2 {
		t.Fatalf("parked step_count = %d, want 2 read/proposal tool calls", persistedSteps)
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
	if gp.GUID != releaseGUIDFingerprint("abc-guid") || gp.IndexerID != 3 || gp.MediaType != "movie" ||
		gp.ReleaseTitle != "Test.Movie.1080p" || gp.Quality != "WEBDL-1080p" ||
		gp.Size != 2048 || gp.Protocol != "usenet" || gp.Indexer != "Test Indexer" {
		t.Fatalf("executor params = %+v, want movie/safe-release-reference/3", gp)
	}

	// The transcript the REAL Runner parked (which already held the placeholder
	// propose_action tool_result) now carries exactly ONE tool_result for prop1,
	// replaced in place with the approval outcome — never a duplicate that a real
	// provider would 400 on. This is the regression guard for the park/resume bug.
	assertWellFormedResume(t, loadTranscript(t, svc, issueID), "prop1", "Approved and executed")

	// Drain the enqueued resume job (the worker pool isn't running in this test).
	select {
	case j := <-svc.jobs:
		if !j.resume {
			t.Fatalf("expected a resume job, got %+v", j)
		}
	default:
		t.Fatalf("expected a resume job after approval")
	}

	// 3) Resume: the agent sees the approval tool_result and verifies read-only.
	// This fixture has no live arr library, so queue absence is not enough to
	// claim resolution; the honest outcome is needs_admin.
	if err := r.Resume(context.Background(), issueID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	issue, _ = svc.GetIssue(issueID)
	if issue.Status != IssueNeedsAdmin {
		t.Fatalf("issue status after resume = %q, want needs_admin without an exact library witness", issue.Status)
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
	if name == "search_releases" {
		return &mcp.ToolResult{
			Text: "Found one scoped release.",
			ReleaseCandidates: []mcp.ReleaseCandidate{{
				Reference: "abc-guid", IndexerID: 3, Title: "Test.Movie.1080p",
				Quality: "WEBDL-1080p", Size: 2048, Protocol: "usenet", Indexer: "Test Indexer",
			}},
		}, nil
	}
	if name == "get_queue" {
		return &mcp.ToolResult{
			Text: "Movie queue: empty.",
			Verification: &mcp.ToolVerification{
				Kind:          mcp.VerificationQueueTarget,
				ExactScope:    true,
				TargetPresent: false,
			},
		}, nil
	}
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
		return &mcp.AgentToolResult{Text: "conclusion requested", Concluded: true, Status: a.Status, Resolution: a.Resolution}, nil
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
	case mcp.ToolAskReporter:
		// Mirror the real ExecuteAgentTool ask_reporter case: post the question +
		// signal an awaiting_user park ONLY when the issue has a reporter; otherwise
		// a benign no-op with NO park (the agent continues). The placeholder text
		// matches the real tool's so the reply can find/replace it on resume.
		var a struct {
			Question string `json:"question"`
		}
		json.Unmarshal(input, &a)
		hasReporter, err := h.svc.AskReporter(ctx, issueID, a.Question, toolUseID)
		if err != nil {
			return &mcp.AgentToolResult{Text: "could not ask: " + err.Error()}, nil
		}
		if !hasReporter {
			return &mcp.AgentToolResult{Text: "no reporter to ask"}, nil
		}
		return &mcp.AgentToolResult{Text: "Question posted to the reporter; the investigation will resume with their reply.", Parked: true, AwaitingUser: true, Question: a.Question}, nil
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
	if _, err := svc.SetSettings(Settings{Enabled: true, Mode: ModeSupervised, MaxSteps: 12, MaxTurnTokens: 1024, MaxWallClockSecs: 30, MaxCostMicros: 500000, DailyRunCap: 50, DailyCostCeilingMicros: 5000000}); err != nil {
		t.Fatalf("set settings: %v", err)
	}
	if _, err := database.Exec("INSERT INTO users (id, username, password_hash, role) VALUES (?, 'admin', '', 'admin')", testAdminID); err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	res, err := database.Exec(
		"INSERT INTO issues (source, status, media_type, tmdb_id, title, detail, arr_queue_id, download_id) VALUES ('auto','open','movie',42,'Test Movie','bad copy',7,'download-7')",
	)
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issueID, _ := res.LastInsertId()

	grabInput := json.RawMessage(`{"kind":"grab_release","params":{"media_type":"movie","guid":"` +
		releaseGUIDFingerprint("abc-guid") + `","indexer_id":3,"queue_id_to_replace":7},"rationale":"better release"}`)
	script := &scriptedTurn{turns: []ai.TranscriptMessage{
		// Turn 1: investigate (a read), then propose a grab_release. The Runner
		// parks after dispatching propose_action.
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{toolUse("r1", "search_releases")}},
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

// loadTranscript reads the run transcript for an issue's (single) run.
func loadTranscript(t *testing.T, svc *Service, issueID int64) string {
	t.Helper()
	var transcriptJSON string
	if err := svc.db.QueryRow("SELECT transcript_json FROM agent_runs WHERE issue_id = ? ORDER BY id DESC LIMIT 1", issueID).Scan(&transcriptJSON); err != nil {
		t.Fatalf("load transcript: %v", err)
	}
	return transcriptJSON
}

// assertWellFormedResume verifies the resumed transcript is something a real
// provider would accept: there is EXACTLY ONE tool_result for toolUseID (the
// placeholder was replaced in place, not duplicated), its content starts with
// wantPrefix, and no two consecutive user turns exist (which would also 400). It
// is the regression guard for the duplicate-tool_result park/resume bug.
func assertWellFormedResume(t *testing.T, transcriptJSON, toolUseID, wantPrefix string) {
	t.Helper()
	var history []map[string]any
	if err := json.Unmarshal([]byte(transcriptJSON), &history); err != nil {
		t.Fatalf("decode transcript: %v", err)
	}

	results := 0
	var gotContent string
	prevRole := ""
	for _, msg := range history {
		role, _ := msg["role"].(string)
		if role == "user" && prevRole == "user" {
			t.Fatalf("two consecutive user turns in the resumed transcript (a provider would 400): %s", transcriptJSON)
		}
		prevRole = role
		content, _ := msg["content"].([]any)
		for _, raw := range content {
			b, _ := raw.(map[string]any)
			if b["type"] == "tool_result" && b["tool_use_id"] == toolUseID {
				results++
				gotContent, _ = b["content"].(string)
			}
		}
	}
	if results != 1 {
		t.Fatalf("tool_result blocks for %s = %d, want exactly 1 (no duplicate)", toolUseID, results)
	}
	if !strings.HasPrefix(gotContent, wantPrefix) {
		t.Fatalf("resume tool_result content = %q, want prefix %q", gotContent, wantPrefix)
	}
}
