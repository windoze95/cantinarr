package remediation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/mcp"
)

// askCycleRunner builds a Runner whose scripted model investigates (a read),
// then asks the reporter a clarifying question via ask_reporter (the run parks
// awaiting_user), then — on resume after the reply — proposes a grab_release and
// parks again for approval. It returns the Runner, the Service (real agent-tool
// side effects via serviceBackedHost), the fake executor, the issue id, and the
// reporter id. The askToolUseID is the tool_use.id the ask_reporter call carries
// so the test can assert the reply keys back to it.
const askToolUseID = "toolu_ask_1"

func askCycleRunner(t *testing.T, withReporter bool) (*Runner, *Service, *fakeExecutor, int64, int64) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	svc := NewService(database, nil, nil, &fakeNotifier{})
	fx := &fakeExecutor{out: "Release sent to the download client."}
	svc.executor = fx
	if _, err := svc.SetSettings(Settings{Enabled: true, Mode: ModeSupervised, MaxSteps: 12, MaxTurnTokens: 1024, MaxWallClockSecs: 30, DailyRunCap: 50, MaxUserWaitHours: 72}); err != nil {
		t.Fatalf("set settings: %v", err)
	}
	if _, err := database.Exec("INSERT INTO users (id, username, password_hash, role) VALUES (?, 'admin', '', 'admin')", testAdminID); err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	// Seed the issue with or without a reporter. A reporter-less issue mirrors an
	// auto-detected one (ask_reporter must not park it).
	var reporterID int64
	if withReporter {
		reporterID = seedUser(t, database, "reporter")
	}
	res, err := database.Exec(
		"INSERT INTO issues (source, status, category, reporter_id, media_type, tmdb_id, title, detail) VALUES (?, 'open', ?, ?, 'movie', 42, 'Test Movie', 'wrong audio')",
		map[bool]string{true: SourceUser, false: SourceAuto}[withReporter], sqlNullStr(CategoryWrongAudio), sqlNullInt64(reporterID),
	)
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issueID, _ := res.LastInsertId()

	askInput := json.RawMessage(`{"question":"This release is dual-audio English+Russian — do you want English specifically?"}`)
	grabInput := json.RawMessage(`{"kind":"grab_release","params":{"media_type":"movie","guid":"` +
		releaseGUIDFingerprint("abc-guid") + `","indexer_id":3},"rationale":"reporter confirmed English"}`)
	script := &scriptedTurn{turns: []ai.TranscriptMessage{
		// Turn 1: investigate (a read), then ask the reporter. The Runner parks
		// awaiting_user after dispatching ask_reporter.
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{toolUse("r1", "get_manual_import_candidates")}},
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{{Type: ai.BlockToolUse, ID: askToolUseID, Name: mcp.ToolAskReporter, Input: askInput}}},
		// Resume after the reply: refresh exact release candidates, then propose
		// the selected release so server-observed metadata is bound to the gate.
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{toolUse("release-search", "search_releases")}},
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{{Type: ai.BlockToolUse, ID: "prop1", Name: mcp.ToolProposeAction, Input: grabInput}}},
	}}

	r := &Runner{
		db:         database,
		svc:        svc,
		toolServer: &serviceBackedHost{svc: svc},
		turns:      scriptedTurnResolver(script),
		procToken:  "test",
	}
	return r, svc, fx, issueID, reporterID
}

// TestAskReporterParksAwaitingUserNoMutation is acceptance: ask_reporter parks
// the issue awaiting_user (the run waiting_user) and performs NO mutation. The
// agent's question lands on the thread and the reporter is pinged.
func TestAskReporterParksAwaitingUserNoMutation(t *testing.T) {
	r, svc, fx, issueID, reporterID := askCycleRunner(t, true)

	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	issue, _ := svc.GetIssue(issueID)
	if issue.Status != IssueAwaitingUser {
		t.Fatalf("issue status after ask_reporter = %q, want awaiting_user", issue.Status)
	}
	// NO mutation happened (the agent only asked a question).
	if fx.count() != 0 {
		t.Fatalf("executor ran %d times after ask_reporter, want 0 (asking is never a mutation)", fx.count())
	}
	// No proposal was recorded either.
	if pending, _ := svc.ListPendingActions(); len(pending) != 0 {
		t.Fatalf("pending actions after ask_reporter = %d, want 0", len(pending))
	}

	// The run is parked waiting_user with the awaiting_user stop reason.
	var runStatus, stopReason string
	if err := svc.db.QueryRow("SELECT status, COALESCE(stop_reason,'') FROM agent_runs WHERE issue_id = ?", issueID).Scan(&runStatus, &stopReason); err != nil {
		t.Fatalf("load run: %v", err)
	}
	if runStatus != runStatusWaitingUser || stopReason != stopAwaitingUser {
		t.Fatalf("run status/stop = %q/%q, want waiting_user/awaiting_user", runStatus, stopReason)
	}
	// The active_run_id claim was released so a resume can re-claim.
	var activeRun *int64
	svc.db.QueryRow("SELECT active_run_id FROM issues WHERE id = ?", issueID).Scan(&activeRun)
	if activeRun != nil {
		t.Fatalf("active_run_id = %v after park, want NULL (released)", *activeRun)
	}

	// The question was posted as an agent message on the thread.
	thread, _ := svc.IssueThread(issueID)
	var askedOnThread bool
	for _, m := range thread {
		if m.AuthorKind == AuthorAgent && strings.Contains(m.Body, "English specifically") {
			askedOnThread = true
		}
	}
	if !askedOnThread {
		t.Fatalf("expected the agent's question on the thread, got %+v", thread)
	}
	// The reporter was pinged (NotifyUser fired).
	if notif, ok := svc.notifier.(*fakeNotifier); ok {
		if len(notif.userEvents) == 0 {
			t.Fatalf("expected a NotifyUser ping to the reporter %d, got none", reporterID)
		}
	}

	// The parked transcript carries the ask_reporter tool_use + a placeholder
	// tool_result keyed to it (so the reply can replace it in place on resume).
	tj := loadTranscript(t, svc, issueID)
	if !transcriptHasPlaceholderAsk(t, tj, askToolUseID) {
		t.Fatalf("parked transcript missing a placeholder ask_reporter tool_result for %s: %s", askToolUseID, tj)
	}
}

// TestReporterReplyResumesKeyedToAskToolUse is acceptance: a reporter reply
// resumes the run, the reply is appended as the ask_reporter tool_result keyed to
// the ask tool_use_id, the transcript stays WELL-FORMED (exactly one tool_result
// for that id, no two consecutive user turns), and the agent continues (here it
// proposes a fix, so the issue moves to awaiting_approval).
func TestReporterReplyResumesKeyedToAskToolUse(t *testing.T) {
	r, svc, fx, issueID, reporterID := askCycleRunner(t, true)

	// 1) Investigate -> ask the reporter -> park awaiting_user.
	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if issue, _ := svc.GetIssue(issueID); issue.Status != IssueAwaitingUser {
		t.Fatalf("issue status = %q, want awaiting_user before reply", issue.Status)
	}

	// 2) The reporter replies. PostReply appends the reply as the ask_reporter
	// tool_result (replacing the placeholder, keyed to askToolUseID) and enqueues a
	// resume.
	if err := svc.PostReply(issueID, AuthorUser, reporterID, "Yes, English please."); err != nil {
		t.Fatalf("PostReply: %v", err)
	}
	// A second message can arrive after the exact ask/reply handoff has already
	// been staged. It must still be delivered to the model on resume instead of
	// remaining visible only in the UI thread.
	if err := svc.PostReply(issueID, AuthorAdmin, testAdminID, "Prefer a release without forced subtitles too."); err != nil {
		t.Fatalf("PostReply follow-up: %v", err)
	}

	// The reply is keyed to the ask_reporter tool_use_id, exactly one tool_result
	// for it, and the transcript a real provider would accept (the W3 guard reused).
	assertWellFormedResume(t, loadTranscript(t, svc, issueID), askToolUseID, "The reporter replied: Yes, English please.")

	// A resume job was enqueued (drain it; the worker pool isn't running here).
	select {
	case j := <-svc.jobs:
		if !j.resume || j.issueID != issueID {
			t.Fatalf("enqueued job = %+v, want resume of issue %d", j, issueID)
		}
	default:
		t.Fatalf("expected a resume job after the reporter reply")
	}

	// 3) Resume: the agent sees the reply (keyed to its ask) and proposes a fix, so
	// the issue parks awaiting_approval. No mutation has run yet (propose != execute).
	if err := r.Resume(context.Background(), issueID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	transcriptJSON := loadTranscript(t, svc, issueID)
	if !strings.Contains(transcriptJSON, "Prefer a release without forced subtitles too.") ||
		!strings.Contains(transcriptJSON, threadCursorPrefix) {
		t.Fatalf("resumed transcript did not consume the follow-up thread message: %s", transcriptJSON)
	}
	var history ai.Transcript
	if err := json.Unmarshal([]byte(transcriptJSON), &history); err != nil {
		t.Fatalf("decode resumed transcript: %v", err)
	}
	for i := 1; i < len(history); i++ {
		if history[i-1].Role == ai.RoleUser && history[i].Role == ai.RoleUser {
			t.Fatalf("thread sync produced adjacent user turns at %d: %s", i, transcriptJSON)
		}
	}
	issue, _ := svc.GetIssue(issueID)
	if issue.Status != IssueAwaitingApproval {
		t.Fatalf("issue status after resume = %q, want awaiting_approval (agent proposed per the reply)", issue.Status)
	}
	pending, _ := svc.ListPendingActions()
	if len(pending) != 1 || pending[0].Kind != string(ActionGrabRelease) {
		t.Fatalf("pending actions after resume = %+v, want one grab_release", pending)
	}
	if fx.count() != 0 {
		t.Fatalf("executor ran %d times, want 0 (the proposal still needs admin approval)", fx.count())
	}
}

func TestReporterReplyResumeSurvivesDroppedQueueHint(t *testing.T) {
	r, svc, _, issueID, reporterID := askCycleRunner(t, true)
	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := svc.PostReply(issueID, AuthorUser, reporterID, "Yes, English please."); err != nil {
		t.Fatalf("PostReply: %v", err)
	}

	// Simulate an in-memory queue loss after the HTTP transaction committed.
	select {
	case <-svc.jobs:
	default:
		t.Fatal("expected initial resume hint")
	}
	var issueStatus, runStatus string
	if err := svc.db.QueryRow(
		`SELECT i.status, r.status FROM issues i JOIN agent_runs r ON r.issue_id = i.id
		 WHERE i.id = ? ORDER BY r.id DESC LIMIT 1`, issueID,
	).Scan(&issueStatus, &runStatus); err != nil {
		t.Fatalf("load durable handoff: %v", err)
	}
	if issueStatus != IssueInvestigating || runStatus != runStatusResumePending {
		t.Fatalf("durable handoff = issue %q run %q", issueStatus, runStatus)
	}

	// Startup/periodic recovery reconstructs only the queue hint; the answered
	// gate and transcript were already durable.
	svc.recoverWork(r)
	select {
	case job := <-svc.jobs:
		if !job.resume || job.issueID != issueID {
			t.Fatalf("recovered job = %+v", job)
		}
	default:
		t.Fatal("resume_pending handoff was not recovered")
	}
}

func TestResumeCannotBypassUnansweredReporterGate(t *testing.T) {
	r, svc, _, issueID, _ := askCycleRunner(t, true)
	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := r.Resume(context.Background(), issueID); err != nil {
		t.Fatalf("stale Resume: %v", err)
	}
	issue, _ := svc.GetIssue(issueID)
	if issue.Status != IssueAwaitingUser {
		t.Fatalf("unanswered gate status = %q, want awaiting_user", issue.Status)
	}
	var runStatus string
	if err := svc.db.QueryRow("SELECT status FROM agent_runs WHERE issue_id = ?", issueID).Scan(&runStatus); err != nil {
		t.Fatalf("load run: %v", err)
	}
	if runStatus != runStatusWaitingUser {
		t.Fatalf("unanswered run status = %q, want waiting_user", runStatus)
	}
}

func TestCorruptReporterGateSavesReplyAndStopsRun(t *testing.T) {
	r, svc, _, issueID, reporterID := askCycleRunner(t, true)
	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := svc.db.Exec(
		"UPDATE agent_runs SET transcript_json = '[]' WHERE issue_id = ? AND status = ?",
		issueID, runStatusWaitingUser,
	); err != nil {
		t.Fatalf("corrupt transcript: %v", err)
	}
	const reply = "Save this answer even though the transcript is broken."
	if err := svc.PostReply(issueID, AuthorUser, reporterID, reply); err != nil {
		t.Fatalf("PostReply: %v", err)
	}

	issue, err := svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	var runStatus string
	if err := svc.db.QueryRow("SELECT status FROM agent_runs WHERE issue_id = ?", issueID).Scan(&runStatus); err != nil {
		t.Fatalf("load run: %v", err)
	}
	thread, err := svc.IssueThread(issueID)
	if err != nil {
		t.Fatalf("IssueThread: %v", err)
	}
	replies := 0
	for _, message := range thread {
		if message.Body == reply {
			replies++
		}
	}
	if issue.Status != IssueNeedsAdmin || runStatus != "aborted" || replies != 1 {
		t.Fatalf("fallback = issue %q run %q reply count %d", issue.Status, runStatus, replies)
	}
}

func TestFallbackReplyDoesNotClobberNewerResumeState(t *testing.T) {
	r, svc, _, issueID, reporterID := askCycleRunner(t, true)
	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Model another reply winning between PostReply's failed staging transaction
	// and its fallback transaction. The fallback must append this reply without
	// overwriting the newer durable resume handoff.
	if _, err := svc.db.Exec("UPDATE issues SET status = ? WHERE id = ?", IssueInvestigating, issueID); err != nil {
		t.Fatalf("advance issue: %v", err)
	}
	if _, err := svc.db.Exec("UPDATE agent_runs SET status = ? WHERE issue_id = ?", runStatusResumePending, issueID); err != nil {
		t.Fatalf("advance run: %v", err)
	}
	const reply = "A second answer arrived during resume staging."
	if err := svc.saveUnresumableReply(issueID, AuthorUser, reporterID, reply); err != nil {
		t.Fatalf("saveUnresumableReply: %v", err)
	}

	issue, err := svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	var runStatus string
	if err := svc.db.QueryRow("SELECT status FROM agent_runs WHERE issue_id = ?", issueID).Scan(&runStatus); err != nil {
		t.Fatalf("load run: %v", err)
	}
	if issue.Status != IssueInvestigating || runStatus != runStatusResumePending {
		t.Fatalf("fallback clobbered newer state: issue %q run %q", issue.Status, runStatus)
	}
	thread, err := svc.IssueThread(issueID)
	if err != nil {
		t.Fatalf("IssueThread: %v", err)
	}
	count := 0
	for _, message := range thread {
		if message.Body == reply {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("raced reply stored %d times, want once", count)
	}
}

// TestAskReporterNoReporterDoesNotPark is acceptance: ask_reporter on a
// reporter-less (auto-detected) issue does NOT park — the dispatch returns a
// benign "no reporter to ask" result, the agent continues in the SAME run (here
// it proposes a fix), and the issue never enters awaiting_user.
func TestAskReporterNoReporterDoesNotPark(t *testing.T) {
	r, svc, fx, issueID, _ := askCycleRunner(t, false)

	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The issue NEVER parked awaiting_user. With no reporter the ask is a no-op,
	// the agent's NEXT scripted turn (propose_action) runs in the same loop, so the
	// issue ends awaiting_approval — proving the run did not stop at the ask.
	issue, _ := svc.GetIssue(issueID)
	if issue.Status == IssueAwaitingUser {
		t.Fatalf("reporter-less issue parked awaiting_user, want it NOT to park")
	}
	if issue.Status != IssueAwaitingApproval {
		t.Fatalf("issue status = %q, want awaiting_approval (agent continued past the no-op ask)", issue.Status)
	}
	// No run is left waiting_user.
	var waitingUser int
	svc.db.QueryRow("SELECT COUNT(*) FROM agent_runs WHERE issue_id = ? AND status = ?", issueID, runStatusWaitingUser).Scan(&waitingUser)
	if waitingUser != 0 {
		t.Fatalf("runs in waiting_user = %d for a reporter-less issue, want 0", waitingUser)
	}
	// Still no mutation (the agent proposed, which an admin must approve).
	if fx.count() != 0 {
		t.Fatalf("executor ran %d times, want 0", fx.count())
	}
}

// TestReplyTTLClosesStaleAwaitingUser is acceptance: an awaiting_user issue with
// no reply within the window is closed wont_fix(user_unresponsive) by the sweep,
// with a thread message and the run finalized.
func TestReplyTTLClosesStaleAwaitingUser(t *testing.T) {
	r, svc, _, issueID, _ := askCycleRunner(t, true)

	// Park the issue awaiting_user.
	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if issue, _ := svc.GetIssue(issueID); issue.Status != IssueAwaitingUser {
		t.Fatalf("issue status = %q, want awaiting_user", issue.Status)
	}

	// Age the issue past the TTL window (back-date updated_at so the sweep sees it
	// as stale; the ask message would otherwise have just touched updated_at).
	if _, err := svc.db.Exec("UPDATE issues SET updated_at = datetime('now','-100 hours') WHERE id = ?", issueID); err != nil {
		t.Fatalf("age issue: %v", err)
	}

	// Sweep with a 72h window: the 100h-old issue is closed.
	closed, err := svc.SweepStaleAwaitingUser(context.Background(), 72)
	if err != nil {
		t.Fatalf("SweepStaleAwaitingUser: %v", err)
	}
	if closed != 1 {
		t.Fatalf("sweep closed %d issues, want 1", closed)
	}

	issue, _ := svc.GetIssue(issueID)
	if issue.Status != IssueWontFix {
		t.Fatalf("issue status after sweep = %q, want wont_fix", issue.Status)
	}
	// closed_at is set + the resolution records user_unresponsive.
	var closedAt *string
	var resolution string
	if err := svc.db.QueryRow("SELECT closed_at, COALESCE(resolution,'') FROM issues WHERE id = ?", issueID).Scan(&closedAt, &resolution); err != nil {
		t.Fatalf("load closed issue: %v", err)
	}
	if closedAt == nil {
		t.Fatalf("closed_at is NULL after sweep, want set")
	}
	if resolution != ResolutionUserUnresponsive {
		t.Fatalf("resolution = %q, want %q", resolution, ResolutionUserUnresponsive)
	}
	// A plain-language closing message was posted.
	thread, _ := svc.IssueThread(issueID)
	var closedMsg bool
	for _, m := range thread {
		if m.AuthorKind == AuthorAgent && strings.Contains(m.Body, "didn't hear back") {
			closedMsg = true
		}
	}
	if !closedMsg {
		t.Fatalf("expected a closing agent message after the TTL sweep, got %+v", thread)
	}
	// The parked run was finalized (no longer waiting_user).
	var waitingUser int
	svc.db.QueryRow("SELECT COUNT(*) FROM agent_runs WHERE issue_id = ? AND status = ?", issueID, runStatusWaitingUser).Scan(&waitingUser)
	if waitingUser != 0 {
		t.Fatalf("a run is still waiting_user after the TTL close, want 0")
	}

	// Idempotent: a second sweep closes nothing (the issue already left awaiting_user).
	closed2, err := svc.SweepStaleAwaitingUser(context.Background(), 72)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if closed2 != 0 {
		t.Fatalf("second sweep closed %d, want 0 (idempotent)", closed2)
	}
}

// TestSweepLeavesFreshAwaitingUserUntouched confirms the TTL sweep does NOT close
// an awaiting_user issue whose ask is still within the window.
func TestSweepLeavesFreshAwaitingUserUntouched(t *testing.T) {
	r, svc, _, issueID, _ := askCycleRunner(t, true)
	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The issue was just parked (updated_at ~ now), well within 72h.
	closed, err := svc.SweepStaleAwaitingUser(context.Background(), 72)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if closed != 0 {
		t.Fatalf("sweep closed %d fresh issues, want 0", closed)
	}
	if issue, _ := svc.GetIssue(issueID); issue.Status != IssueAwaitingUser {
		t.Fatalf("fresh issue status = %q, want still awaiting_user", issue.Status)
	}
}

func TestReporterReplyWinsStaleTimeoutSnapshot(t *testing.T) {
	r, svc, _, issueID, reporterID := askCycleRunner(t, true)
	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := svc.db.Exec("UPDATE issues SET updated_at = datetime('now','-100 hours') WHERE id = ?", issueID); err != nil {
		t.Fatalf("age issue: %v", err)
	}
	// Model the sweeper having selected this id, then losing the race to a reply.
	if err := svc.PostReply(issueID, AuthorUser, reporterID, "Here is the answer."); err != nil {
		t.Fatalf("PostReply: %v", err)
	}
	transitioned, err := svc.concludeIssueCAS(context.Background(), issueID,
		IssueWontFix, ResolutionUserUnresponsive, ResolutionReporterTimeout,
		IssueAwaitingUser, "-72 hours")
	if err != nil {
		t.Fatalf("stale timeout CAS: %v", err)
	}
	if transitioned {
		t.Fatal("timeout closed an issue after its reporter replied")
	}
	issue, _ := svc.GetIssue(issueID)
	if issue.ClosedAt != nil || issue.Status != IssueInvestigating {
		t.Fatalf("answered issue = status %q closed_at %v", issue.Status, issue.ClosedAt)
	}
}

// transcriptHasPlaceholderAsk reports whether the transcript carries an
// ask_reporter tool_result keyed to toolUseID whose content is still the park
// placeholder (i.e. it has not yet been replaced by a reply).
func transcriptHasPlaceholderAsk(t *testing.T, transcriptJSON, toolUseID string) bool {
	t.Helper()
	var history []map[string]any
	if err := json.Unmarshal([]byte(transcriptJSON), &history); err != nil {
		t.Fatalf("decode transcript: %v", err)
	}
	for _, msg := range history {
		content, _ := msg["content"].([]any)
		for _, raw := range content {
			b, _ := raw.(map[string]any)
			if b["type"] == "tool_result" && b["tool_use_id"] == toolUseID && b["name"] == mcp.ToolAskReporter {
				c, _ := b["content"].(string)
				return strings.Contains(c, "Question posted to the reporter")
			}
		}
	}
	return false
}
