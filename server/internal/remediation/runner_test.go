package remediation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/mcp"
)

// mutatingTools is the exact set of arr-mutating tool names that MUST be
// unreachable by the read-only remediation agent. The safety test asserts none
// is offered to the model and none is dispatchable.
var mutatingTools = []string{
	"grab_release",
	"remove_queue_item",
	"remediate_queue_item",
	"execute_manual_import",
	"rescan_media",
	"trigger_search",
}

// fakeToolHost is a stand-in for *mcp.ToolServer that records every ExecuteTool
// call so a test can prove a mutating tool name NEVER reaches arr execution. It
// also serves the read allow-list definitions and benign agent-tool results.
type fakeToolHost struct {
	mu               sync.Mutex
	executeToolNames []string // every name passed to ExecuteTool, in order
	concludeCalled   bool
	proposeCalled    bool
	queuePresent     *bool // when set, get_queue returns typed exact-scope evidence
}

func (f *fakeToolHost) ToolsByName(names []string) []mcp.Tool {
	out := make([]mcp.Tool, 0, len(names))
	for _, n := range names {
		out = append(out, mcp.Tool{Name: n})
	}
	return out
}

func (f *fakeToolHost) ExecuteTool(ctx context.Context, name string, input json.RawMessage, callCtx mcp.CallContext) (*mcp.ToolResult, error) {
	f.mu.Lock()
	f.executeToolNames = append(f.executeToolNames, name)
	f.mu.Unlock()
	result := &mcp.ToolResult{Text: "ok: " + name}
	if name == "get_queue" && f.queuePresent != nil {
		result.Verification = &mcp.ToolVerification{
			Kind:          mcp.VerificationQueueTarget,
			ExactScope:    true,
			TargetPresent: *f.queuePresent,
		}
	}
	return result, nil
}

func (f *fakeToolHost) ExecuteAgentTool(ctx context.Context, name string, input json.RawMessage, issueID int64, toolUseID string) (*mcp.AgentToolResult, error) {
	switch name {
	case mcp.ToolPostIssueMessage:
		return &mcp.AgentToolResult{Text: "posted"}, nil
	case mcp.ToolConcludeIssue:
		f.mu.Lock()
		f.concludeCalled = true
		f.mu.Unlock()
		// Mirror the real tool: it also writes the terminal issue state via the
		// IssueStore. The Runner injects the real Service for that side effect, so
		// here we only signal Concluded.
		return &mcp.AgentToolResult{Text: "conclusion requested", Concluded: true, Status: mcp.ConcludeResolved, Resolution: "verified"}, nil
	case mcp.ToolProposeAction:
		f.mu.Lock()
		f.proposeCalled = true
		f.mu.Unlock()
		// Mirror the real tool's park signal so the Runner exits the loop. The real
		// proposal row is written by the Service's ProposeAction in integration
		// tests; this fake only drives the Runner's park behavior.
		return &mcp.AgentToolResult{Text: "Proposal #1 recorded; awaiting admin approval.", Parked: true}, nil
	}
	return &mcp.AgentToolResult{Text: "noop"}, nil
}

func (f *fakeToolHost) executedNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.executeToolNames))
	copy(out, f.executeToolNames)
	return out
}

// scriptedTurn is a fake ai.TurnRunner that returns pre-scripted assistant turns
// and records the tool list it was offered on each call.
type scriptedTurn struct {
	turns       []ai.TranscriptMessage
	idx         int
	offeredTool [][]string // names offered to NextTurn per call
}

func (s *scriptedTurn) NextTurn(ctx context.Context, p ai.TurnParams) (ai.TurnResult, error) {
	names := make([]string, 0, len(p.Tools))
	for _, t := range p.Tools {
		names = append(names, t.Name)
	}
	s.offeredTool = append(s.offeredTool, names)

	if s.idx >= len(s.turns) {
		// Past the script: a plain end_turn with no tools so the loop terminates.
		return ai.TurnResult{
			Message:    ai.TranscriptMessage{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{{Type: ai.BlockText, Text: "done"}}},
			StopReason: ai.StopReasonEndTurn,
		}, nil
	}
	msg := s.turns[s.idx]
	s.idx++
	stop := ai.StopReasonEndTurn
	for _, b := range msg.Content {
		if b.Type == ai.BlockToolUse {
			stop = ai.StopReasonToolUse
			break
		}
	}
	return ai.TurnResult{Message: msg, StopReason: stop, Usage: ai.Usage{InputTokens: 10, OutputTokens: 5}}, nil
}

func toolUse(id, name string) ai.TranscriptBlock {
	return ai.TranscriptBlock{Type: ai.BlockToolUse, ID: id, Name: name, Input: json.RawMessage(`{}`)}
}

type autonomousTurnResolverFunc func(context.Context) (ai.AutonomousTurn, error)

func (f autonomousTurnResolverFunc) ResolveSharedAutonomousTurn(ctx context.Context) (ai.AutonomousTurn, error) {
	return f(ctx)
}

func scriptedTurnResolver(turn ai.TurnRunner) autonomousTurnResolver {
	return autonomousTurnResolverFunc(func(context.Context) (ai.AutonomousTurn, error) {
		return ai.AutonomousTurn{Runner: turn, Provider: "test", Model: "test-model"}, nil
	})
}

// newTestRunner builds a Runner over an in-memory DB with the feature ENABLED, a
// fake shared-turn resolver and the given fake tool host + scripted turns. It
// returns the Runner, the Service, and a seeded issue id.
func newTestRunner(t *testing.T, host toolHost, script *scriptedTurn) (*Runner, *Service, int64) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	svc := NewService(database, nil, nil, &fakeNotifier{})
	// Enable the feature and pin a tiny step budget by default; individual tests
	// override bounds as needed.
	if _, err := svc.SetSettings(Settings{Enabled: true, Mode: ModeSupervised, MaxSteps: 12, MaxTurnTokens: 1024, MaxWallClockSecs: 30, DailyRunCap: 50}); err != nil {
		t.Fatalf("set settings: %v", err)
	}

	// Seed a user issue to investigate.
	res, err := database.Exec(
		"INSERT INTO issues (source, status, media_type, tmdb_id, title, detail) VALUES ('user','open','movie',42,'Test Movie','wrong content')",
	)
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issueID, _ := res.LastInsertId()

	r := &Runner{
		db:         database,
		svc:        svc,
		toolServer: host,
		turns:      scriptedTurnResolver(script),
		procToken:  "test",
	}
	return r, svc, issueID
}

// TestRunnerNeverOffersOrDispatchesMutatingTools is the CRITICAL safety test.
// The model is scripted to attempt EVERY mutating tool. The test asserts that
// (1) no mutating tool is ever offered to the model, and (2) no mutating tool is
// ever dispatched to ExecuteTool — the dispatch refuses each by name. Read-only
// by construction.
func TestRunnerNeverOffersOrDispatchesMutatingTools(t *testing.T) {
	// Turn 1: the (hijacked) model asks to run every mutating tool at once.
	var mutBlocks []ai.TranscriptBlock
	for i, name := range mutatingTools {
		mutBlocks = append(mutBlocks, toolUse("mut"+string(rune('a'+i)), name))
	}
	script := &scriptedTurn{turns: []ai.TranscriptMessage{
		{Role: ai.RoleAssistant, Content: mutBlocks},
		// Turn 2: a legitimate read, then conclude.
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{toolUse("read1", "get_queue")}},
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{toolUse("c1", mcp.ToolConcludeIssue)}},
	}}
	host := &fakeToolHost{}
	r, svc, issueID := newTestRunner(t, host, script)
	if _, err := svc.db.Exec(
		"UPDATE issues SET source = ?, arr_queue_id = 7, download_id = 'download-7' WHERE id = ?",
		SourceAuto, issueID,
	); err != nil {
		t.Fatalf("scope issue as detector incident: %v", err)
	}

	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// (1) No mutating tool was ever OFFERED to the model.
	for callIdx, offered := range script.offeredTool {
		for _, name := range offered {
			if isMutating(name) {
				t.Fatalf("turn %d offered mutating tool %q to the model", callIdx, name)
			}
		}
		// Sanity: the read allow-list IS offered.
		if !contains(offered, "get_queue") || !contains(offered, "diagnose_queue") {
			t.Fatalf("turn %d did not offer the read allow-list: %v", callIdx, offered)
		}
		if !contains(offered, mcp.ToolPostIssueMessage) || !contains(offered, mcp.ToolConcludeIssue) {
			t.Fatalf("turn %d did not offer the agent-only tools: %v", callIdx, offered)
		}
	}

	// (2) ExecuteTool was reached ONLY for allow-listed read tools — never for a
	// mutating one. The dispatch refused every mutating name without calling it.
	for _, name := range host.executedNames() {
		if isMutating(name) {
			t.Fatalf("ExecuteTool was called with mutating tool %q — mutation reachable!", name)
		}
		if !isReadToolAllowed(name) {
			t.Fatalf("ExecuteTool was called with non-allow-listed tool %q", name)
		}
	}
	// The legitimate read DID reach ExecuteTool.
	if !contains(host.executedNames(), "get_queue") {
		t.Fatalf("expected the allow-listed get_queue to reach ExecuteTool, got %v", host.executedNames())
	}

	// A user-reported issue cannot be terminally closed solely on the model's
	// judgment, even after a read; it remains visible for an administrator.
	issue, err := svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Status != IssueNeedsAdmin {
		t.Fatalf("issue status = %q, want needs_admin", issue.Status)
	}
}

func TestRunnerPostClaimRecoveryRaceStartsNoModelTurn(t *testing.T) {
	script := &scriptedTurn{turns: []ai.TranscriptMessage{{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{toolUse("msg", mcp.ToolPostIssueMessage)}}}}
	r, svc, issueID := newTestRunner(t, &fakeToolHost{}, script)
	probes := 0
	svc.recoveryProbe = func(*Issue) (arrRecoveryProbe, error) {
		probes++
		if probes == 1 {
			return arrRecoveryProbe{}, nil
		}
		return arrRecoveryProbe{active: true, item: arr.QueueObservation{DownloadID: "retry", Media: arr.QueueMediaContext{QueueID: 7, TmdbID: 42}}}, nil
	}
	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatal(err)
	}
	if script.idx != 0 {
		t.Fatalf("provider turns=%d, want zero after post-claim recovery", script.idx)
	}
	issue, _ := svc.GetIssue(issueID)
	if issue.Status != IssueRecovering {
		t.Fatalf("issue=%+v", issue)
	}
	var runStatus string
	_ = svc.db.QueryRow("SELECT status FROM agent_runs WHERE issue_id=? ORDER BY id DESC LIMIT 1", issueID).Scan(&runStatus)
	if runStatus != "aborted" {
		t.Fatalf("run status=%q", runStatus)
	}
}

// TestRunnerMaxStepsGivesUp asserts the MaxSteps bound terminates a run that
// keeps calling tools without concluding, via the give-up terminal path.
func TestRunnerMaxStepsGivesUp(t *testing.T) {
	// Every scripted turn emits one allowed read tool and never concludes; the
	// fallback (past the script) would conclude, but MaxSteps must trip first.
	turns := make([]ai.TranscriptMessage, 10)
	for i := range turns {
		turns[i] = ai.TranscriptMessage{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{toolUse("r", "get_queue")}}
	}
	script := &scriptedTurn{turns: turns}
	host := &fakeToolHost{}
	r, svc, issueID := newTestRunner(t, host, script)

	// Tighten the step bound to 2.
	if _, err := svc.SetSettings(Settings{Enabled: true, Mode: ModeSupervised, MaxSteps: 2, MaxTurnTokens: 1024, MaxWallClockSecs: 30, DailyRunCap: 50}); err != nil {
		t.Fatalf("set settings: %v", err)
	}

	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Exhaustion remains visible for an administrator; it is not misreported as
	// a terminal resolution.
	issue, err := svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Status != IssueNeedsAdmin {
		t.Fatalf("issue status = %q, want needs_admin after max steps", issue.Status)
	}

	// A giveup audit step was recorded with the max_steps stop reason.
	var giveups int
	if err := r.db.QueryRow("SELECT COUNT(*) FROM agent_steps WHERE kind = 'giveup'").Scan(&giveups); err != nil {
		t.Fatalf("count giveup steps: %v", err)
	}
	if giveups != 1 {
		t.Fatalf("giveup steps = %d, want 1", giveups)
	}
	var runStatus, stopReason string
	if err := r.db.QueryRow("SELECT status, COALESCE(stop_reason,'') FROM agent_runs WHERE issue_id = ?", issueID).Scan(&runStatus, &stopReason); err != nil {
		t.Fatalf("load run: %v", err)
	}
	if runStatus != runStatusGaveUp || stopReason != stopMaxSteps {
		t.Fatalf("run status/stop = %q/%q, want gave_up/max_steps", runStatus, stopReason)
	}

	// A plain-language give-up message was posted to the thread.
	thread, err := svc.IssueThread(issueID)
	if err != nil {
		t.Fatalf("IssueThread: %v", err)
	}
	var agentMsgs int
	for _, m := range thread {
		if m.AuthorKind == AuthorAgent {
			agentMsgs++
		}
	}
	if agentMsgs == 0 {
		t.Fatalf("expected at least one agent message on the thread after give-up")
	}
}

func TestConclusionRejectsFreshReadWithoutTypedResolutionProof(t *testing.T) {
	script := &scriptedTurn{turns: []ai.TranscriptMessage{
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{toolUse("too-early", mcp.ToolConcludeIssue)}},
		// General health is diagnostic context, not proof that this media issue is
		// currently fixed, so it must not unlock conclusion either.
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{toolUse("health", "get_arr_health")}},
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{toolUse("still-too-early", mcp.ToolConcludeIssue)}},
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{toolUse("fresh-state", "get_queue")}},
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{toolUse("verified", mcp.ToolConcludeIssue)}},
	}}
	host := &fakeToolHost{}
	r, svc, issueID := newTestRunner(t, host, script)
	if _, err := svc.db.Exec(
		"UPDATE issues SET source = ?, arr_queue_id = 7, download_id = 'download-7' WHERE id = ?",
		SourceAuto, issueID,
	); err != nil {
		t.Fatalf("scope issue as detector incident: %v", err)
	}

	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if script.idx != len(script.turns) {
		t.Fatalf("model turns consumed = %d, want %d; an early conclusion bypassed the read gate", script.idx, len(script.turns))
	}
	if got := host.executedNames(); !contains(got, "get_arr_health") || !contains(got, "get_queue") {
		t.Fatalf("reads executed = %v, want health and scoped state verification", got)
	}
	issue, err := svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Status != IssueNeedsAdmin || issue.Resolution != "Agent needs administrator review: "+stopUnverifiedClose {
		t.Fatalf("conclusion = status %q resolution %q", issue.Status, issue.Resolution)
	}
}

func TestAutoConclusionRejectsQueueAbsenceWithoutExactLibraryProof(t *testing.T) {
	present := false
	script := &scriptedTurn{turns: []ai.TranscriptMessage{
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{toolUse("fresh-state", "get_queue")}},
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{toolUse("verified", mcp.ToolConcludeIssue)}},
	}}
	host := &fakeToolHost{queuePresent: &present}
	r, svc, issueID := newTestRunner(t, host, script)
	if _, err := svc.db.Exec(
		"UPDATE issues SET source = ?, instance_id = ?, arr_queue_id = ?, download_id = ? WHERE id = ?",
		SourceAuto, "radarr-1", 7, "download-7", issueID,
	); err != nil {
		t.Fatalf("scope auto issue: %v", err)
	}

	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	issue, err := svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Status != IssueNeedsAdmin || issue.ResolutionKind != "" {
		t.Fatalf("conclusion = status %q resolution %q kind %q", issue.Status, issue.Resolution, issue.ResolutionKind)
	}
}

func TestAutoConclusionRejectsTypedQueueTargetStillPresent(t *testing.T) {
	present := true
	script := &scriptedTurn{turns: []ai.TranscriptMessage{
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{toolUse("fresh-state", "get_queue")}},
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{toolUse("premature", mcp.ToolConcludeIssue)}},
	}}
	host := &fakeToolHost{queuePresent: &present}
	r, svc, issueID := newTestRunner(t, host, script)
	if _, err := svc.db.Exec(
		"UPDATE issues SET source = ?, instance_id = ?, arr_queue_id = ?, download_id = ? WHERE id = ?",
		SourceAuto, "radarr-1", 7, "download-7", issueID,
	); err != nil {
		t.Fatalf("scope auto issue: %v", err)
	}

	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	issue, err := svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Status != IssueNeedsAdmin {
		t.Fatalf("issue status = %q, want needs_admin", issue.Status)
	}
}

func TestSuccessfulGenericReadDoesNotProveUserIssueResolved(t *testing.T) {
	script := &scriptedTurn{turns: []ai.TranscriptMessage{
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{toolUse("history", "get_history")}},
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{toolUse("conclude", mcp.ToolConcludeIssue)}},
	}}
	r, svc, issueID := newTestRunner(t, &fakeToolHost{}, script)
	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	issue, err := svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Status == IssueResolved {
		t.Fatal("generic history text falsely resolved a subjective user issue")
	}
}

func TestRecoveryStartingDuringModelTurnPreventsHumanGate(t *testing.T) {
	script := &scriptedTurn{turns: []ai.TranscriptMessage{{
		Role: ai.RoleAssistant,
		Content: []ai.TranscriptBlock{{Type: ai.BlockToolUse, ID: "proposal", Name: mcp.ToolProposeAction,
			Input: json.RawMessage(`{"kind":"trigger_search","params":{"media_type":"movie","tmdb_id":42},"rationale":"retry"}`)}},
	}}}
	host := &fakeToolHost{}
	r, svc, issueID := newTestRunner(t, host, script)
	calls := 0
	svc.recoveryProbe = func(*Issue) (arrRecoveryProbe, error) {
		calls++
		if calls < 3 {
			return arrRecoveryProbe{}, nil
		}
		return arrRecoveryProbe{active: true, item: arr.QueueObservation{
			DownloadID: "arr-retry", Media: arr.QueueMediaContext{QueueID: 88, TmdbID: 42},
		}}, nil
	}
	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatal(err)
	}
	host.mu.Lock()
	proposed := host.proposeCalled
	host.mu.Unlock()
	if proposed {
		t.Fatal("stale model proposal reached the human gate after arr recovery began")
	}
	var actions int
	_ = svc.db.QueryRow("SELECT COUNT(*) FROM agent_actions WHERE issue_id=?", issueID).Scan(&actions)
	issue, _ := svc.GetIssue(issueID)
	if actions != 0 || issue.Status != IssueRecovering {
		t.Fatalf("actions=%d issue=%+v", actions, issue)
	}
}

func TestRunOwnedClosureCannotWinAfterObservationSuspendsRun(t *testing.T) {
	r, svc, issueID := newTestRunner(t, &fakeToolHost{}, &scriptedTurn{})
	res, err := svc.db.Exec(
		"INSERT INTO agent_runs(issue_id,trigger,status,model) VALUES (?,'auto','running','m')", issueID,
	)
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := res.LastInsertId()
	if _, err := svc.db.Exec("UPDATE issues SET status=?,active_run_id=? WHERE id=?", IssueInvestigating, runID, issueID); err != nil {
		t.Fatal(err)
	}
	// This is the state change that may occur after exactRecoveryProven returns
	// but before the runner begins its close transaction.
	if _, err := svc.db.Exec("UPDATE agent_runs SET status='aborted' WHERE id=?", runID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec("UPDATE issues SET status=?,active_run_id=NULL WHERE id=?", IssueRecovering, issueID); err != nil {
		t.Fatal(err)
	}
	transitioned, err := r.svc.concludeIssueAggregate(context.Background(), issueID, IssueResolved,
		arrStateClearedResolution, ResolutionArrStateCleared, issueClosureOptions{expectedRunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	issue, _ := svc.GetIssue(issueID)
	if transitioned || issue.ClosedAt != nil || issue.Status != IssueRecovering {
		t.Fatalf("stale run closed issue after ownership loss: transitioned=%t issue=%+v", transitioned, issue)
	}
}

func TestRunnerEnforcesStepBudgetWithinOneTurn(t *testing.T) {
	blocks := make([]ai.TranscriptBlock, 0, 8)
	for i := 0; i < 8; i++ {
		blocks = append(blocks, toolUse(fmt.Sprintf("read-%d", i), "get_queue"))
	}
	script := &scriptedTurn{turns: []ai.TranscriptMessage{{
		Role: ai.RoleAssistant, Content: blocks,
	}}}
	host := &fakeToolHost{}
	r, svc, issueID := newTestRunner(t, host, script)
	settings := svc.Settings()
	settings.MaxSteps = 2
	if _, err := svc.SetSettings(settings); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}

	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(host.executedNames()); got != 2 {
		t.Fatalf("read tools executed = %d, want exactly the two-call budget", got)
	}
	var stepCount int
	if err := svc.db.QueryRow("SELECT step_count FROM agent_runs WHERE issue_id = ?", issueID).Scan(&stepCount); err != nil {
		t.Fatalf("load run: %v", err)
	}
	if stepCount != 2 {
		t.Fatalf("persisted step_count = %d, want 2", stepCount)
	}
	issue, err := svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Status != IssueNeedsAdmin {
		t.Fatalf("issue status = %q, want needs_admin after budget exhaustion", issue.Status)
	}
}

func TestInvestigateOnlyDoesNotOfferProposalTool(t *testing.T) {
	script := &scriptedTurn{}
	r, svc, issueID := newTestRunner(t, &fakeToolHost{}, script)
	settings := svc.Settings()
	settings.Enabled = true
	settings.Mode = ModeInvestigateOnly
	if _, err := svc.SetSettings(settings); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}
	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(script.offeredTool) == 0 {
		t.Fatal("model received no turn")
	}
	for _, name := range script.offeredTool[0] {
		if name == mcp.ToolProposeAction {
			t.Fatal("investigate-only mode offered propose_action")
		}
	}
}

func TestOneHumanGatePerModelTurn(t *testing.T) {
	host := &fakeToolHost{}
	script := &scriptedTurn{turns: []ai.TranscriptMessage{{
		Role: ai.RoleAssistant,
		Content: []ai.TranscriptBlock{
			{Type: ai.BlockToolUse, ID: "p1", Name: mcp.ToolProposeAction, Input: json.RawMessage(`{}`)},
			{Type: ai.BlockToolUse, ID: "c1", Name: mcp.ToolConcludeIssue, Input: json.RawMessage(`{"status":"resolved"}`)},
		},
	}}}
	r, svc, issueID := newTestRunner(t, host, script)
	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !host.proposeCalled || host.concludeCalled {
		t.Fatalf("propose=%v conclude=%v, want first gate only", host.proposeCalled, host.concludeCalled)
	}
	issue, err := svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Status != IssueAwaitingApproval {
		t.Fatalf("issue status = %q, want awaiting_approval", issue.Status)
	}
}

func TestAutoIssueRunTriggerIsAuto(t *testing.T) {
	r, svc, issueID := newTestRunner(t, &fakeToolHost{}, &scriptedTurn{})
	if _, err := svc.db.Exec("UPDATE issues SET source = ? WHERE id = ?", SourceAuto, issueID); err != nil {
		t.Fatalf("set source: %v", err)
	}
	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var trigger string
	if err := svc.db.QueryRow("SELECT trigger FROM agent_runs WHERE issue_id = ?", issueID).Scan(&trigger); err != nil {
		t.Fatalf("load trigger: %v", err)
	}
	if trigger != "auto" {
		t.Fatalf("trigger = %q, want auto", trigger)
	}
}

func TestScopeReadToolInputBindsUserQueueReadsToMediaAndEpisode(t *testing.T) {
	issue := &Issue{
		MediaType: "tv", TmdbID: 42, TvdbID: 4242,
		SeasonNumber: 2, EpisodeNumber: 7,
	}
	raw, err := scopeReadToolInput(issue, "diagnose_queue", json.RawMessage(
		`{"media_type":"movie","queue_id":91,"tmdb_id":999,"tvdb_id":998,"season_number":9,"episode_number":9}`,
	))
	if err != nil {
		t.Fatalf("scopeReadToolInput: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode scoped input: %v", err)
	}
	if got["media_type"] != "tv" || got["tmdb_id"] != float64(42) || got["tvdb_id"] != float64(4242) ||
		got["season_number"] != float64(2) || got["episode_number"] != float64(7) {
		t.Fatalf("authoritative scope was not imposed: %s", raw)
	}
	// A user issue has no detector queue id, so the model may narrow the read
	// to one candidate. The MCP handler still intersects it with every identity
	// field above; it can never expose an unrelated row.
	if got["queue_id"] != float64(91) {
		t.Fatalf("candidate queue id = %v, want 91: %s", got["queue_id"], raw)
	}
}

func TestScopeReadToolInputPreservesExactSpecialSeasonZero(t *testing.T) {
	issue := &Issue{MediaType: "tv", TmdbID: 42, TvdbID: 4242, SeasonNumber: 0, EpisodeNumber: 1}
	for _, toolName := range []string{"diagnose_queue", "search_releases", "get_history"} {
		raw, err := scopeReadToolInput(issue, toolName, json.RawMessage(`{"season_number":9,"episode_number":9}`))
		if err != nil {
			t.Fatalf("%s: %v", toolName, err)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got["season_number"] != float64(0) || got["episode_number"] != float64(1) {
			t.Fatalf("%s widened special scope: %s", toolName, raw)
		}
	}
}

func TestScopeReadToolInputExactQueueOverridesModelAndStripsUnknownIdentity(t *testing.T) {
	issue := &Issue{MediaType: "movie", TmdbID: 42, ArrQueueID: 7}
	raw, err := scopeReadToolInput(issue, "get_manual_import_candidates", json.RawMessage(
		`{"queue_id":999,"tmdb_id":999,"tvdb_id":888,"book_id":4,"author_id":5}`,
	))
	if err != nil {
		t.Fatalf("scopeReadToolInput: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode scoped input: %v", err)
	}
	if got["queue_id"] != float64(7) || got["tmdb_id"] != float64(42) || got["media_type"] != "movie" {
		t.Fatalf("exact scope was not imposed: %s", raw)
	}
	for _, forbidden := range []string{"tvdb_id", "book_id", "author_id"} {
		if _, ok := got[forbidden]; ok {
			t.Fatalf("model-selected %s survived scoping: %s", forbidden, raw)
		}
	}
}

func TestScopeReadToolInputDoesNotInventTMDBForTVDBOnlyIssue(t *testing.T) {
	issue := &Issue{MediaType: "tv", TvdbID: 1234, SeasonNumber: 1, EpisodeNumber: 3}
	raw, err := scopeReadToolInput(issue, "search_releases", json.RawMessage(
		`{"tmdb_id":999,"season_number":9,"book_id":1}`,
	))
	if err != nil {
		t.Fatalf("scopeReadToolInput: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode scoped input: %v", err)
	}
	if _, ok := got["tmdb_id"]; ok {
		t.Fatalf("untrusted tmdb_id survived: %s", raw)
	}
	if got["season_number"] != float64(1) || got["episode_number"] != float64(3) || got["media_type"] != "tv" {
		t.Fatalf("TV scope = %s", raw)
	}
}

func TestScopeReadToolInputFailsClosedWithoutUsableIdentity(t *testing.T) {
	if _, err := scopeReadToolInput(&Issue{MediaType: "movie"}, "get_queue", json.RawMessage(`{"queue_id":9}`)); err == nil {
		t.Fatal("unscoped movie queue read was accepted")
	}
	if _, err := scopeReadToolInput(&Issue{MediaType: "book", ArrQueueID: 7, DownloadID: "download-7"}, "get_history", nil); err == nil {
		t.Fatal("book history without durable book identity was accepted")
	}
	raw, err := scopeReadToolInput(
		&Issue{MediaType: "book", ArrQueueID: 7, DownloadID: "download-7"},
		"get_queue", nil,
	)
	if err != nil {
		t.Fatalf("exact book queue scope rejected: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode scoped book input: %v", err)
	}
	if got["queue_id"] != float64(7) || got["download_id"] != "download-7" {
		t.Fatalf("book queue scope = %s", raw)
	}
}

func TestBindReleaseCandidateMetadataUsesServerObservation(t *testing.T) {
	input := json.RawMessage(`{"kind":"grab_release","params":{"media_type":"movie","guid":"safe-ref","indexer_id":3,"release_title":"model lie"},"rationale":"replace it"}`)
	candidate := mcp.ReleaseCandidate{
		Reference: "safe-ref", IndexerID: 3, Title: "Movie.2026.1080p",
		Quality: "WEBDL-1080p", Size: 2048, Protocol: "usenet", Indexer: "Example",
		Rejected: true, Rejections: []string{"Not an upgrade"},
	}
	bound, err := bindReleaseCandidateMetadata(input, map[string]mcp.ReleaseCandidate{
		releaseCandidateKey(candidate.Reference, candidate.IndexerID): candidate,
	})
	if err != nil {
		t.Fatalf("bindReleaseCandidateMetadata: %v", err)
	}
	var envelope struct {
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(bound, &envelope); err != nil {
		t.Fatalf("decode bound proposal: %v", err)
	}
	canonical, err := validateActionParams(ActionGrabRelease, envelope.Params)
	if err != nil {
		t.Fatalf("validate bound params: %v", err)
	}
	var got GrabReleaseParams
	if err := json.Unmarshal(canonical, &got); err != nil {
		t.Fatalf("decode canonical params: %v", err)
	}
	if got.GUID != releaseGUIDFingerprint("safe-ref") || got.ReleaseTitle != candidate.Title ||
		got.Quality != candidate.Quality || got.Size != candidate.Size || got.Protocol != candidate.Protocol ||
		got.Indexer != candidate.Indexer || !got.Rejected || len(got.Rejections) != 1 {
		t.Fatalf("bound params = %+v", got)
	}
	if _, err := bindReleaseCandidateMetadata(input, nil); err == nil || !strings.Contains(err.Error(), "fresh issue-scoped") {
		t.Fatalf("missing-candidate error = %v", err)
	}
}

func isMutating(name string) bool {
	for _, m := range mutatingTools {
		if m == name {
			return true
		}
	}
	return false
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
