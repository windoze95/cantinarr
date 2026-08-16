package remediation

import (
	"testing"
)

// These tests pin recoverWork's handling of staged decision handoffs
// (resume_pending runs) against the strand found on issue 856 (2026-08-11):
// an approval staged a resume, a suspend/re-promote cycle let a FRESH run
// claim the issue and give up to needs_admin, and the handoff then sat
// resume_pending for two days — unconsumable (the resume lane only accepted
// investigating) while its existence also blocked the fresh-run lane's
// NOT EXISTS guard for the issue forever.

func seedHandoffIssue(t *testing.T, svc *Service, status string, closed bool) int64 {
	t.Helper()
	closedAt := "NULL"
	if closed {
		closedAt = "CURRENT_TIMESTAMP"
	}
	res, err := svc.db.Exec(
		`INSERT INTO issues (source, status, media_type, tmdb_id, title, detail, instance_id, closed_at)
		 VALUES ('auto', ?, 'tv', 42, 'Handoff Fixture', 'seed', 'sonarr-test', `+closedAt+`)`,
		status,
	)
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func seedRun(t *testing.T, svc *Service, issueID int64, status string) int64 {
	t.Helper()
	res, err := svc.db.Exec(
		`INSERT INTO agent_runs (issue_id, trigger, status, model, proc_generation)
		 VALUES (?, 'auto', ?, 'test', 'live-proc')`,
		issueID, status,
	)
	if err != nil {
		t.Fatalf("seed run: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func runStateOf(t *testing.T, svc *Service, runID int64) (status, stopReason string) {
	t.Helper()
	if err := svc.db.QueryRow(
		"SELECT status, COALESCE(stop_reason,'') FROM agent_runs WHERE id = ?", runID,
	).Scan(&status, &stopReason); err != nil {
		t.Fatalf("read run %d: %v", runID, err)
	}
	return status, stopReason
}

func TestRecoverWorkTerminalizesStrandedHandoffs(t *testing.T) {
	svc, _, _ := setupTestService(t)
	if _, err := svc.SetSettings(Settings{Enabled: true, Mode: ModeSupervised}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	runner := &Runner{db: svc.db, svc: svc, procToken: "live-proc"}

	// Case 1: the issue closed under the staged handoff — it can never run.
	closedIssue := seedHandoffIssue(t, svc, IssueResolved, true)
	closedRun := seedRun(t, svc, closedIssue, runStatusResumePending)

	// Case 2: issue 856's shape — a later run reached needs_admin, so the
	// handoff's moment is gone even though the issue stays open.
	supersededIssue := seedHandoffIssue(t, svc, IssueNeedsAdmin, false)
	strandedRun := seedRun(t, svc, supersededIssue, runStatusResumePending)
	seedRun(t, svc, supersededIssue, "gave_up")

	// Case 3: needs_admin with NO later run — a suspend/re-promote cycle may
	// still return this issue to a workable state, so the handoff is kept.
	dormantIssue := seedHandoffIssue(t, svc, IssueNeedsAdmin, false)
	dormantRun := seedRun(t, svc, dormantIssue, runStatusResumePending)

	svc.recoverWork(runner)

	if status, stop := runStateOf(t, svc, closedRun); status != "aborted" || stop != "issue_closed" {
		t.Errorf("handoff on a closed issue = %s/%s, want aborted/issue_closed", status, stop)
	}
	if status, stop := runStateOf(t, svc, strandedRun); status != "aborted" || stop != "superseded_by_later_run" {
		t.Errorf("handoff superseded by a later run = %s/%s, want aborted/superseded_by_later_run", status, stop)
	}
	if status, _ := runStateOf(t, svc, dormantRun); status != runStatusResumePending {
		t.Errorf("dormant needs_admin handoff = %s, want kept %s", status, runStatusResumePending)
	}
	_ = dormantIssue
}

func TestRecoverWorkResumesHandoffFromOpenIssue(t *testing.T) {
	svc, _, _ := setupTestService(t)
	if _, err := svc.SetSettings(Settings{Enabled: true, Mode: ModeSupervised}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	runner := &Runner{db: svc.db, svc: svc, procToken: "live-proc"}

	// An approval's resume staged while the issue flapped back through
	// tracking and re-promoted to OPEN: the resume claim handles open, so the
	// recovery lane must hand it back to the workers instead of stranding it.
	openIssue := seedHandoffIssue(t, svc, IssueOpen, false)
	openRun := seedRun(t, svc, openIssue, runStatusResumePending)

	svc.recoverWork(runner)

	if got := drainJobs(svc); got != 1 {
		t.Fatalf("jobs enqueued for an open issue with a staged resume = %d, want 1", got)
	}
	if status, _ := runStateOf(t, svc, openRun); status != runStatusResumePending {
		t.Fatalf("staged resume consumed prematurely by the reaper itself: %s", status)
	}
	// And the fresh-run guard still holds: no duplicate fresh job appeared for
	// the same issue (the single drained job was the resume).
}
