package remediation

// The 2026-08-13 issue-859 aftermath, pinned. A stalled book download's
// blocklist+search fix executed correctly and the replacement search found
// nothing — the one shape the recovery proof could not name. What followed was
// a machine of small lies: the incident re-promoted forever (0 files → 0 files
// proves nothing), a fresh run beat the staged resume's minute tick and got it
// reaped as superseded, the eventual needs_admin claimed the fix "could not be
// verified" (the one thing that verifiably ran), and the standing rule was
// paused by its own success with a note about a close that never happened.
// These tests hold the honest endings in place.

import (
	"context"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/arr"
)

// seedRemovedNoReplacementIssue stages the exact 859 end-state: an auto book
// incident, promoted, baseline captured with NO file, whose blocklisting fix a
// standing rule approved and the executor completed.
func seedRemovedNoReplacementIssue(t *testing.T, svc *Service, facet string) (issueID, ruleID int64) {
	t.Helper()
	res, err := svc.db.Exec(
		`INSERT INTO issues (source, status, media_type, tmdb_id, title, detail, problem_kind, instance_id, author_id, book_id)
		 VALUES ('auto', 'open', 'book', 0, 'Example Book', 'stalled', 'Download stalled', 'chaptarr-observe', 456, 123)`)
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issueID, _ = res.LastInsertId()
	if _, err := svc.db.Exec(
		`INSERT INTO issue_observations (issue_id, service_type, scope_key, state, promoted_at,
		 baseline_has_file, baseline_captured_at, first_seen_at, last_activity_at)
		 VALUES (?, 'chaptarr', 'scope-859', 'settling', datetime('now','-30 minutes'),
		         0, datetime('now','-30 minutes'), datetime('now','-40 minutes'), datetime('now','-12 minutes'))`,
		issueID,
	); err != nil {
		t.Fatalf("seed observation: %v", err)
	}
	res, err = svc.db.Exec(
		`INSERT INTO agent_approval_rules (problem_kind, action_kind, action_facet, status, created_at, updated_at)
		 VALUES ('Download stalled', 'remediate_queue', ?, 'active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		facet,
	)
	if err != nil {
		t.Fatalf("seed rule: %v", err)
	}
	ruleID, _ = res.LastInsertId()
	if _, err := svc.db.Exec(
		`INSERT INTO agent_actions (issue_id, kind, params, rationale, risk, status, auto_rule_id, decided_at, executed_at, fingerprint, tool_use_id)
		 VALUES (?, 'remediate_queue', '{"media_type":"book","queue_id":9,"action":"`+facet+`"}',
		         'stalled', 'mutating', 'executed', ?, datetime('now','-14 minutes'), datetime('now','-14 minutes'), 'fp-859', 'tu-859')`,
		issueID, ruleID,
	); err != nil {
		t.Fatalf("seed executed action: %v", err)
	}
	return issueID, ruleID
}

// TestBookRemoveWithoutReplacementProofIsNarrow drives every typed condition
// the proof stands on. Each false row is a shape that must still reach the old
// machinery (re-promotion or an admin), because for it "no file arrived" is
// NOT a success story.
func TestBookRemoveWithoutReplacementProofIsNarrow(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, svc *Service, issueID int64)
		file   int // fake Chaptarr's live bookfile id; 0 = no file
		want   bool
	}{
		{name: "the 859 shape proves", file: 0, want: true},
		{name: "a live file means this was not the ending",
			file: 31, want: false},
		{name: "a baseline WITH a file is an upgrade story, not a want",
			mutate: func(t *testing.T, svc *Service, issueID int64) {
				if _, err := svc.db.Exec(
					"UPDATE issue_observations SET baseline_has_file=1, baseline_file_id=30 WHERE issue_id=?",
					issueID); err != nil {
					t.Fatal(err)
				}
			}, file: 0, want: false},
		{name: "no captured baseline proves nothing",
			mutate: func(t *testing.T, svc *Service, issueID int64) {
				if _, err := svc.db.Exec(
					"UPDATE issue_observations SET baseline_captured_at=NULL, baseline_has_file=NULL WHERE issue_id=?",
					issueID); err != nil {
					t.Fatal(err)
				}
			}, file: 0, want: false},
		{name: "a plain remove is the #359 re-grab loop, not an ending",
			mutate: func(t *testing.T, svc *Service, issueID int64) {
				if _, err := svc.db.Exec(
					`UPDATE agent_actions SET params='{"media_type":"book","queue_id":9,"action":"remove"}' WHERE issue_id=?`,
					issueID); err != nil {
					t.Fatal(err)
				}
			}, file: 0, want: false},
		{name: "a proposed-but-never-executed fix proves nothing",
			mutate: func(t *testing.T, svc *Service, issueID int64) {
				if _, err := svc.db.Exec(
					"UPDATE agent_actions SET status='proposed', executed_at=NULL WHERE issue_id=?",
					issueID); err != nil {
					t.Fatal(err)
				}
			}, file: 0, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := setupBookObservationService(t, &bookFileState{fileID: tc.file})
			issueID, _ := seedRemovedNoReplacementIssue(t, svc, arr.ActionBlocklistSearch)
			if tc.mutate != nil {
				tc.mutate(t, svc, issueID)
			}
			issue, err := svc.GetIssue(issueID)
			if err != nil {
				t.Fatal(err)
			}
			got, err := svc.bookRemoveWithoutReplacementProven(issue)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("proven = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("a user report is never machine-closed by this proof", func(t *testing.T) {
		svc, _ := setupBookObservationService(t, &bookFileState{fileID: 0})
		issueID, _ := seedRemovedNoReplacementIssue(t, svc, arr.ActionBlocklistSearch)
		if _, err := svc.db.Exec("UPDATE issues SET source='user' WHERE id=?", issueID); err != nil {
			t.Fatal(err)
		}
		issue, err := svc.GetIssue(issueID)
		if err != nil {
			t.Fatal(err)
		}
		got, err := svc.bookRemoveWithoutReplacementProven(issue)
		if err != nil || got {
			t.Fatalf("proven = %v err=%v, want false: a reporter's issue keeps its human ending", got, err)
		}
	})
}

// TestRemovedNoReplacementEndsTheIncidentHonestly runs the observation path 859
// looped on: the exact scope absent from a complete snapshot, on a promoted,
// settled incident. Before the terminal existed this re-promoted a fresh run
// (forever); now it closes with copy that matches what actually happened — and
// the rule whose action produced that ending keeps its clean record.
func TestRemovedNoReplacementEndsTheIncidentHonestly(t *testing.T) {
	svc, _ := setupBookObservationService(t, &bookFileState{fileID: 0})
	enableAutoDispatch(t, svc, 5)
	issueID, ruleID := seedRemovedNoReplacementIssue(t, svc, arr.ActionBlocklistSearch)

	// One complete empty snapshot: the promoted incident has already served its
	// absence settle (state settling, settling_since NULL), so the guard runs.
	if err := svc.observeQueueSnapshot("chaptarr", "chaptarr-observe", nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	issue, err := svc.GetIssue(issueID)
	if err != nil {
		t.Fatal(err)
	}
	if issue.Status != IssueResolved || issue.ClosedAt == nil {
		t.Fatalf("issue = %q closed=%v, want resolved (before the terminal existed this re-promoted forever)", issue.Status, issue.ClosedAt)
	}
	if issue.ResolutionKind != ResolutionRemovedNoReplacement {
		t.Fatalf("resolution_kind = %q, want %q", issue.ResolutionKind, ResolutionRemovedNoReplacement)
	}
	// The copy is the point: 859's needs_admin said the fix "could not be
	// verified" — about the one thing that verifiably ran.
	if issue.Resolution != removedNoReplacementResolution {
		t.Fatalf("resolution = %q, want the removed-no-replacement narration", issue.Resolution)
	}

	// The rule's action produced exactly this ending; pausing it here was 859's
	// fourth defect ("closed without being resolved", about an issue that never
	// closed). Success is counted, the rule stays armed.
	rule := ruleRow(t, svc, ruleID)
	if rule.Status != ApprovalRuleActive {
		t.Fatalf("rule status = %q (reason %v), want active", rule.Status, rule.PausedReason)
	}
	if rule.ResolvedCount != 1 {
		t.Fatalf("rule resolved_count = %d, want 1", rule.ResolvedCount)
	}
}

// TestRunHandsAStagedResumeToTheResumeLane pins the race that made 859's run
// 69: promoteObservedIssue re-promoted the issue to open and enqueued a FRESH
// run while run 68's approval sat staged as resume_pending — the fresh run
// re-litigated from scratch and the staged decision was reaped as superseded.
// Every fresh entry funnels through Runner.Run, so Run itself now delegates to
// the resume lane whenever a staged handoff exists.
func TestRunHandsAStagedResumeToTheResumeLane(t *testing.T) {
	svc, _ := setupBookObservationService(t, &bookFileState{fileID: 0})
	settings := Defaults()
	settings.Enabled = true
	settings.AutoDispatch = true
	settings.Mode = ModeSupervised
	if _, err := svc.SetSettings(settings); err != nil {
		t.Fatal(err)
	}
	issueID, _ := seedRemovedNoReplacementIssue(t, svc, arr.ActionBlocklistSearch)
	// Strip the executed action: with it, the removed-no-replacement terminal
	// closes this incident during Run's own preflight — an equally honest
	// ending that would mask the race this test exists to pin. Without it the
	// preflight proves nothing, which is exactly the state 859's run 69 found.
	if _, err := svc.db.Exec("DELETE FROM agent_actions WHERE issue_id=?", issueID); err != nil {
		t.Fatal(err)
	}

	// The staged handoff: run 68's shape, durable and waiting for the resume
	// lane's next tick. An empty transcript is fine — Resume consumes the
	// handoff and gives up cleanly, which is still the resume lane acting.
	res, err := svc.db.Exec(
		`INSERT INTO agent_runs (issue_id, trigger, status, model, proc_generation, transcript_json)
		 VALUES (?, 'auto', 'resume_pending', 'test-model', 'test', '[]')`,
		issueID,
	)
	if err != nil {
		t.Fatalf("stage handoff: %v", err)
	}
	stagedRunID, _ := res.LastInsertId()

	runner := &Runner{
		db:        svc.db,
		svc:       svc,
		turns:     scriptedTurnResolver(&scriptedTurn{}),
		procToken: "test",
	}
	if err := runner.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var runs int
	if err := svc.db.QueryRow("SELECT COUNT(1) FROM agent_runs WHERE issue_id=?", issueID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	// 859's signature was a SECOND run row racing the staged one. One row,
	// still: the fresh lane must not have claimed anything.
	if runs != 1 {
		t.Fatalf("agent_runs rows = %d, want the staged run to remain the only one", runs)
	}
	var status string
	if err := svc.db.QueryRow("SELECT status FROM agent_runs WHERE id=?", stagedRunID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status == runStatusResumePending {
		t.Fatal("staged handoff untouched; Run did not hand it to the resume lane")
	}
}
