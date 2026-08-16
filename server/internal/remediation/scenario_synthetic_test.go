package remediation

import (
	"context"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/mcp"
)

// Synthetic tier: failure modes derived from the system's actual dependencies
// (arr clock skew, arr auth/blindness, process restart, duplicate webhooks,
// human/agent races) rather than generic chaos. Scenarios expected to PASS on
// current code are green fences: they pin behavior the Phase 3 fix must not
// regress while it makes the red replays pass.

// TestScenarioSyntheticReceiptClockSkewWithinTolerance: the arr's clock runs
// slightly behind the server's, so a genuine import receipt is dated moments
// BEFORE the queue row's Added stamp. A sub-minute skew must not turn a
// provable recovery into an admin page — the receipt already binds download id,
// episode, and file id; the date ordering is a tiebreaker, not the identity.
// Tolerance under test: 60s (acceptance criteria, Phase 1).
func TestScenarioSyntheticReceiptClockSkewWithinTolerance(t *testing.T) {
	w := newScenarioWorld(t, raceStart)
	// queue_file_id == current file (a completed row lingering post-import), so
	// the ONLY strict-boundary trip is the 45s-backwards receipt date.
	issueID := seedPromotedRaceIssue(w, raceNewFileID, -66*time.Second)

	if err := w.runner(run57Script(issueID)).Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	final := w.issueByID(issueID)
	if final.Status != IssueResolved || final.ResolutionKind != ResolutionArrStateCleared {
		t.Errorf("issue with a 45s-skewed receipt = %q/%q, want %q/%q — sub-minute arr clock skew is normal operation",
			final.Status, final.ResolutionKind, IssueResolved, ResolutionArrStateCleared)
	}
}

// TestScenarioSyntheticReceiptClockSkewBeyondToleranceStaysRefused is the
// other half of the tolerance: a receipt dated ten minutes before the queue
// row existed is NOT this download's import, and the gate must keep refusing
// it. This scenario passes today and must keep passing after the fix — it is
// the fence against over-loosening the proof.
func TestScenarioSyntheticReceiptClockSkewBeyondToleranceStaysRefused(t *testing.T) {
	w := newScenarioWorld(t, raceStart)
	issueID := seedPromotedRaceIssue(w, raceNewFileID, -10*time.Minute-21*time.Second)

	if err := w.runner(run57Script(issueID)).Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	final := w.issueByID(issueID)
	if final.Status != IssueNeedsAdmin {
		t.Errorf("issue with a 10m-stale receipt = %q, want %q — a receipt predating the grab proves nothing", final.Status, IssueNeedsAdmin)
	}
}

// TestScenarioSyntheticBlindObservationFailsClosed: the arr's history endpoint
// is down for the whole settle window, so recovery is UNPROVABLE — not proven,
// not disproven. The system must fail closed: never fabricate arr_state_cleared
// while blind, escalate to a human instead, and page at most once. Passes
// today; the fence exists so the recovery-proof fix cannot loosen "blind" into
// "fine".
func TestScenarioSyntheticBlindObservationFailsClosed(t *testing.T) {
	w := newScenarioWorld(t, raceStart)
	stageUpgradeRace(w, 2, 4)
	w.fake.setHistoryStatus(500)

	w.clock.step(21 * time.Second)
	w.observe()
	issueID := w.soleOpenAutoIssueID()
	w.fake.setQueue(nil)
	w.clock.step(30 * time.Second)
	w.observe()
	w.advance(20 * time.Minute)

	final := w.issueByID(issueID)
	if final.ResolutionKind == ResolutionArrStateCleared {
		t.Errorf("issue resolved %q while history was unreadable — a blind window must never prove recovery", final.ResolutionKind)
	}
	if final.Status != IssueNeedsAdmin {
		t.Errorf("blind-window issue = %q, want %q (escalated, not guessed)", final.Status, IssueNeedsAdmin)
	}
	if pages := w.rec.pages("issue_created"); len(pages) > 1 {
		t.Errorf("pages while blind = %d (%s), want at most 1", len(pages), pageTimes(pages))
	}
}

// TestScenarioSyntheticRestartMidStaggerNoLossNoDupes: the process restarts
// while two admin alerts sit inside the 3-minute hold-down. The owed pages are
// durable rows, so the restarted process must deliver them exactly once —
// coalesced, since they come due on the same flush tick — and a stale running
// agent claim from the dead process must be reaped back to workable state.
func TestScenarioSyntheticRestartMidStaggerNoLossNoDupes(t *testing.T) {
	w := newScenarioWorld(t, raceStart)
	first := seedPromotedRaceIssue(w, raceOldFileID, 0)
	// A second promoted issue on a different episode, same source.
	var second int64
	{
		res, err := w.svc.db.Exec(
			`INSERT INTO issues (source, status, tmdb_id, tvdb_id, media_type, title,
			   season_number, episode_number, instance_id, detail, problem_kind, dedupe_key)
			 VALUES ('auto', ?, ?, ?, 'tv', 'The Big Comfy Couch', 2, 10, ?, 'Waiting to import', 'Waiting to import', 'replay|2|10')`,
			IssueOpen, scenarioTmdbID, scenarioTvdbID, scenarioSonarrID,
		)
		if err != nil {
			t.Fatalf("seed second issue: %v", err)
		}
		second, _ = res.LastInsertId()
	}
	now := w.clock.now
	w.svc.queueIssueAlert(first, now)
	w.svc.queueIssueAlert(second, now)

	// Seed the dead process's claim: a running run owned by proc "dead-proc".
	if _, err := w.svc.db.Exec(
		`INSERT INTO agent_runs (issue_id, trigger, status, model, proc_generation, started_at)
		 VALUES (?, 'auto', 'running', 'scenario', 'dead-proc', CURRENT_TIMESTAMP)`, first,
	); err != nil {
		t.Fatalf("seed stale run: %v", err)
	}
	var staleRunID int64
	if err := w.svc.db.QueryRow("SELECT id FROM agent_runs WHERE issue_id = ? ORDER BY id DESC LIMIT 1", first).Scan(&staleRunID); err != nil {
		t.Fatalf("read stale run id: %v", err)
	}
	if _, err := w.svc.db.Exec("UPDATE issues SET status = ?, active_run_id = ? WHERE id = ?", IssueInvestigating, staleRunID, first); err != nil {
		t.Fatalf("claim issue for dead proc: %v", err)
	}

	// "Restart": a fresh Service over the same DB, fresh notifier, same clock.
	rec2 := &recordingNotifier{clock: w.clock}
	svc2 := NewService(w.svc.db, w.svc.registry, nil, rec2)
	runner2 := &Runner{db: svc2.db, svc: svc2, procToken: "new-proc"}
	svc2.recoverWork(runner2)

	var runStatus, stopReason string
	if err := svc2.db.QueryRow(
		"SELECT status, COALESCE(stop_reason,'') FROM agent_runs WHERE id = ?", staleRunID,
	).Scan(&runStatus, &stopReason); err != nil {
		t.Fatalf("read reaped run: %v", err)
	}
	if runStatus != "aborted" || stopReason != "server_restarted" {
		t.Errorf("stale run after restart = %q/%q, want aborted/server_restarted", runStatus, stopReason)
	}
	if got := w.issueByID(first); got.Status != IssueOpen {
		t.Errorf("issue claimed by the dead process = %q, want released to %q", got.Status, IssueOpen)
	}

	// The owed alerts survive the restart and deliver exactly once, coalesced.
	w.clock.step(3*time.Minute + 30*time.Second)
	svc2.flushIssueAlerts(w.clock.now)
	pages := rec2.pages("issue_created")
	if len(pages) != 1 {
		t.Fatalf("pages after restart flush = %d (%s), want exactly 1 coalesced push", len(pages), pageTimes(pages))
	}
	w.clock.step(30 * time.Second)
	svc2.flushIssueAlerts(w.clock.now)
	if again := rec2.pages("issue_created"); len(again) != 1 {
		t.Errorf("pages after second flush = %d, want still 1 — no re-delivery", len(again))
	}
	if old := w.rec.pages("issue_created"); len(old) != 0 {
		t.Errorf("dead process delivered %d page(s) — scenario staging leak", len(old))
	}
}

// TestScenarioSyntheticDuplicateWebhookSingleIssue: Sonarr delivers the same
// pre-air import webhook twice (retries and double-fires are routine). One
// season, one issue, at most one page.
func TestScenarioSyntheticDuplicateWebhookSingleIssue(t *testing.T) {
	w := newScenarioWorld(t, raceStart)
	episodes, files := buildPreAirSeason(scenarioSeriesID, 9, []preAirEpisode{
		{number: 1, airsIn: 7 * 24 * time.Hour, hasFile: true, importedBeforeAir: true},
		{number: 2, airsIn: 14 * 24 * time.Hour, hasFile: true, importedBeforeAir: true},
	})
	w.fake.setLibrary(episodes, files)

	if err := w.svc.recordPreAirSeason(scenarioSonarrID, scenarioTvdbID, scenarioTmdbID, 9, "The Big Comfy Couch"); err != nil {
		t.Fatalf("first webhook: %v", err)
	}
	if err := w.svc.recordPreAirSeason(scenarioSonarrID, scenarioTvdbID, scenarioTmdbID, 9, "The Big Comfy Couch"); err != nil {
		t.Fatalf("duplicate webhook: %v", err)
	}
	var open int
	if err := w.svc.db.QueryRow("SELECT COUNT(*) FROM issues WHERE closed_at IS NULL").Scan(&open); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	if open != 1 {
		t.Errorf("open issues after duplicate webhook = %d, want 1", open)
	}
	w.advanceFlushOnly(5 * time.Minute)
	if pages := w.rec.pages("issue_created"); len(pages) > 1 {
		t.Errorf("pages after duplicate webhook = %d (%s), want at most 1", len(pages), pageTimes(pages))
	}
}

// TestScenarioSyntheticAdminResolvesWhileProposalPending: a human dismisses the
// issue while the agent's proposal sits awaiting approval. The pending-action
// page must be swallowed, the proposal must not survive as approvable, and a
// late approval attempt must not dispatch anything at the arr.
func TestScenarioSyntheticAdminResolvesWhileProposalPending(t *testing.T) {
	w := newScenarioWorld(t, raceStart)
	ep, file := scenarioEpisode(5, 5, 11500, raceStart.Add(-60*24*time.Hour))
	w.fake.setLibrary([]map[string]any{ep}, []map[string]any{file})
	w.fake.setQueue([]map[string]any{stalledQueueRow(910001, "STALL-RACE", 5, 5, 11500, raceStart.Add(-30*time.Minute))})
	w.observe()
	issueID := w.soleOpenAutoIssueID()
	w.advance(11 * time.Minute)
	if got := w.issueByID(issueID); got.Status != IssueOpen {
		t.Fatalf("stalled issue = %q, want promoted %q", got.Status, IssueOpen)
	}

	proposal := &scriptedTurn{turns: []ai.TranscriptMessage{
		toolCall("q1", "get_queue", `{}`),
		toolCall("p1", mcp.ToolProposeAction, `{"issue_id":0,"kind":"remediate_queue","params":{"media_type":"tv","queue_id":910001,"action":"blocklist_only"},"rationale":"Stalled with no connections; the library copy stays."}`),
	}}
	if err := w.runner(proposal).Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run to proposal: %v", err)
	}
	var actionID int64
	if err := w.svc.db.QueryRow(
		"SELECT id FROM agent_actions WHERE issue_id = ? AND status = ?", issueID, ActionProposed,
	).Scan(&actionID); err != nil {
		t.Fatalf("find proposal: %v", err)
	}

	if err := w.svc.DismissIssue(issueID); err != nil {
		t.Fatalf("DismissIssue: %v", err)
	}
	w.advanceFlushOnly(20 * time.Minute)

	if pages := w.rec.pages("agent_action_pending"); len(pages) != 0 {
		t.Errorf("pending-action pages after dismissal = %d (%s), want 0", len(pages), pageTimes(pages))
	}
	var actStatus string
	if err := w.svc.db.QueryRow("SELECT status FROM agent_actions WHERE id = ?", actionID).Scan(&actStatus); err != nil {
		t.Fatalf("read action: %v", err)
	}
	if actStatus == ActionProposed || actStatus == ActionExecuted {
		t.Errorf("proposal on a dismissed issue = %q, want superseded/terminal-not-executed", actStatus)
	}
	deletesBefore := len(w.fake.queueDeletesSeen())
	if _, err := w.svc.ApproveAction(1, actionID, nil); err == nil {
		t.Errorf("ApproveAction on a dismissed issue's proposal succeeded, want refusal")
	}
	if after := len(w.fake.queueDeletesSeen()); after != deletesBefore {
		t.Errorf("late approval dispatched an arr mutation (%d -> %d deletes)", deletesBefore, after)
	}
}

// TestScenarioSyntheticArrAuthExpiryMidInvestigation: the arr starts answering
// 401 (expired API key) after the issue was promoted. The pre-claim recovery
// preflight cannot read the queue, so the run must DEFER — Run returns the
// deferral error, no run row is left claimed, the issue stays workable for the
// per-minute retry, and nothing pages. (First red-baseline draft asserted a
// needs_admin landing here; the deferral is the actual, better contract, and
// the correction is disclosed in the Phase 2 baseline report.)
func TestScenarioSyntheticArrAuthExpiryMidInvestigation(t *testing.T) {
	w := newScenarioWorld(t, raceStart)
	issueID := seedPromotedRaceIssue(w, raceOldFileID, 0)
	w.fake.setQueueGetStatus(401)
	w.fake.setHistoryStatus(401)
	w.fake.setEpisodeStatus(401)

	if err := w.runner(run57Script(issueID)).Run(context.Background(), issueID); err == nil {
		t.Errorf("Run against a 401-ing arr succeeded, want a deferral")
	}
	var claimed int
	if err := w.svc.db.QueryRow(
		"SELECT COUNT(*) FROM agent_runs WHERE issue_id = ? AND status IN ('running','resume_pending')", issueID,
	).Scan(&claimed); err != nil {
		t.Fatalf("count claimed runs: %v", err)
	}
	if claimed != 0 {
		t.Errorf("runs left claimed after deferral = %d, want 0", claimed)
	}
	final := w.issueByID(issueID)
	if final.ClosedAt != nil || (final.Status != IssueNeedsAdmin && final.Status != IssueOpen) {
		t.Errorf("issue after deferral = %q (closed %v), want it left visible for a human or retry — never closed, never fabricated-resolved",
			final.Status, final.ClosedAt)
	}
	// The promotion page (not seeded here) is the ONE notification this issue
	// gets in the real pipeline; blindness must not add more. The absent
	// system-level "arr unreachable" health signal is a Phase 3 observability
	// item (OBSERVABILITY_GAPS G4), not this scenario's to force.
	w.advanceFlushOnly(10 * time.Minute)
	if pages := w.rec.pages("issue_created"); len(pages) != 0 {
		t.Errorf("extra pages caused by arr auth expiry = %d (%s), want 0", len(pages), pageTimes(pages))
	}
}

// TestScenarioSyntheticDSTFallBackHoldDown: the stagger hold-down runs on UTC
// arithmetic, so the America/Chicago fall-back hour (2026-11-01, wall clocks
// repeat 01:00-02:00 CDT/CST) must not stretch, shrink, or double the window.
func TestScenarioSyntheticDSTFallBackHoldDown(t *testing.T) {
	// 06:45Z = 01:45 CDT, fifteen minutes before the fold.
	start := time.Date(2026, 11, 1, 6, 45, 0, 0, time.UTC)
	w := newScenarioWorld(t, start)
	issueID := seedPromotedRaceIssue(w, raceOldFileID, 0)
	queuedAt := w.clock.now
	w.svc.queueIssueAlert(issueID, queuedAt)
	w.advanceFlushOnly(6 * time.Minute)

	pages := w.rec.pages("issue_created")
	if len(pages) != 1 {
		t.Fatalf("pages across the DST fold = %d (%s), want exactly 1", len(pages), pageTimes(pages))
	}
	gap := pages[0].At.Sub(queuedAt)
	if gap < 3*time.Minute || gap >= 4*time.Minute {
		t.Errorf("hold-down across the DST fold = %s, want [3m, 4m)", gap)
	}
}
