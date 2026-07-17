package db

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
)

// ISS-028: Restart converts in-flight mutations to unknown outcomes without replay.
func TestOpenRepairsUnsafeRemediationStates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cantinarr.db")
	database, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	closed, err := database.Exec(
		`INSERT INTO issues (source, status, media_type, tmdb_id, title, closed_at)
		 VALUES ('auto','resolved','tv',1,'closed',CURRENT_TIMESTAMP)`,
	)
	if err != nil {
		t.Fatalf("insert closed issue: %v", err)
	}
	closedID, _ := closed.LastInsertId()
	run, err := database.Exec(
		`INSERT INTO agent_runs (issue_id, trigger, status) VALUES (?, 'auto', 'waiting_approval')`, closedID,
	)
	if err != nil {
		t.Fatalf("insert waiting run: %v", err)
	}
	runID, _ := run.LastInsertId()
	if _, err := database.Exec(
		`INSERT INTO agent_actions (issue_id, run_id, kind, params, status, fingerprint)
		 VALUES (?, ?, 'rescan', '{"media_type":"tv","tmdb_id":1}', 'proposed', 'closed-proposal')`,
		closedID, runID,
	); err != nil {
		t.Fatalf("insert proposed action: %v", err)
	}
	const rawReleaseSecret = "https://indexer.invalid/download?id=9&apikey=legacy-release-secret"
	if _, err := database.Exec(
		`INSERT INTO agent_actions (issue_id, run_id, kind, params, approved_params, status, fingerprint)
		 VALUES (?, ?, 'grab_release', ?, ?, 'denied', 'legacy-release-action')`,
		closedID, runID,
		`{"media_type":"tv","guid":"`+rawReleaseSecret+`","indexer_id":3}`,
		`{"media_type":"tv","guid":"`+rawReleaseSecret+`","indexer_id":3}`,
	); err != nil {
		t.Fatalf("insert legacy release action: %v", err)
	}
	legacyIssue, err := database.Exec(
		`INSERT INTO issues (source, status, media_type, tmdb_id, title)
		 VALUES ('user','awaiting_approval','movie',9,'legacy release gate')`,
	)
	if err != nil {
		t.Fatalf("insert legacy release issue: %v", err)
	}
	legacyIssueID, _ := legacyIssue.LastInsertId()
	legacyRun, err := database.Exec(
		`INSERT INTO agent_runs (issue_id, trigger, status) VALUES (?, 'user_report', 'waiting_approval')`, legacyIssueID,
	)
	if err != nil {
		t.Fatalf("insert legacy release run: %v", err)
	}
	legacyRunID, _ := legacyRun.LastInsertId()
	if _, err := database.Exec("ALTER TABLE agent_runs ADD COLUMN cost_micros INTEGER NOT NULL DEFAULT 0"); err != nil {
		t.Fatalf("add legacy cost column: %v", err)
	}
	if _, err := database.Exec("UPDATE agent_runs SET cost_micros = 80000 WHERE id = ?", legacyRunID); err != nil {
		t.Fatalf("seed legacy cost estimate: %v", err)
	}
	if _, err := database.Exec("ALTER TABLE issue_observation_downloads DROP COLUMN arr_added_at"); err != nil {
		t.Fatalf("restore legacy observation-download schema: %v", err)
	}
	if _, err := database.Exec("ALTER TABLE issue_observation_downloads DROP COLUMN queue_file_id"); err != nil {
		t.Fatalf("restore legacy queue file-state schema: %v", err)
	}
	if _, err := database.Exec("UPDATE issues SET active_run_id = ? WHERE id = ?", legacyRunID, legacyIssueID); err != nil {
		t.Fatalf("bind legacy release run: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO agent_actions (issue_id, run_id, kind, params, status, fingerprint)
		 VALUES (?, ?, 'grab_release', ?, 'proposed', 'legacy-pending-release')`,
		legacyIssueID, legacyRunID,
		`{"media_type":"movie","guid":"`+rawReleaseSecret+`","indexer_id":3}`,
	); err != nil {
		t.Fatalf("insert legacy pending release: %v", err)
	}
	open, err := database.Exec(
		`INSERT INTO issues (source, status, media_type, tmdb_id, title)
		 VALUES ('user','awaiting_approval','movie',2,'executing')`,
	)
	if err != nil {
		t.Fatalf("insert open issue: %v", err)
	}
	openID, _ := open.LastInsertId()
	if _, err := database.Exec(
		`INSERT INTO agent_actions (issue_id, kind, params, status, fingerprint)
		 VALUES (?, 'rescan', '{"media_type":"movie","tmdb_id":2}', 'executing', 'unknown-outcome')`, openID,
	); err != nil {
		t.Fatalf("insert executing action: %v", err)
	}
	for _, oldKey := range []string{"old-problem-a", "old-problem-b"} {
		if _, err := database.Exec(
			`INSERT INTO issues (source, status, media_type, tmdb_id, title, instance_id, download_id, dedupe_key)
			 VALUES ('auto','open','tv',3,'duplicate','sonarr-1','download-1',?)`, oldKey,
		); err != nil {
			t.Fatalf("insert legacy auto issue: %v", err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	database, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer database.Close()
	var actionStatus, runStatus, stopReason, resolutionKind, openIssueStatus string
	if err := database.QueryRow("SELECT status FROM agent_actions WHERE fingerprint = 'closed-proposal'").Scan(&actionStatus); err != nil {
		t.Fatalf("load repaired proposal: %v", err)
	}
	if err := database.QueryRow("SELECT status, COALESCE(stop_reason,'') FROM agent_runs WHERE id = ?", runID).Scan(&runStatus, &stopReason); err != nil {
		t.Fatalf("load repaired run: %v", err)
	}
	if err := database.QueryRow("SELECT resolution_kind FROM issues WHERE id = ?", closedID).Scan(&resolutionKind); err != nil {
		t.Fatalf("load repaired issue: %v", err)
	}
	if actionStatus != "superseded" || runStatus != "aborted" || stopReason != "issue_closed" || resolutionKind != "legacy_unknown" {
		t.Fatalf("closed repair = action %q run %q/%q resolution %q", actionStatus, runStatus, stopReason, resolutionKind)
	}
	if err := database.QueryRow("SELECT status FROM agent_actions WHERE fingerprint = 'unknown-outcome'").Scan(&actionStatus); err != nil {
		t.Fatalf("load unknown action: %v", err)
	}
	if actionStatus != "outcome_unknown" {
		t.Fatalf("executing repair status = %q, want outcome_unknown", actionStatus)
	}
	if err := database.QueryRow("SELECT status FROM issues WHERE id = ?", openID).Scan(&openIssueStatus); err != nil {
		t.Fatalf("load unknown-outcome issue: %v", err)
	}
	if openIssueStatus != "needs_admin" {
		t.Fatalf("unknown-outcome issue status = %q, want needs_admin", openIssueStatus)
	}

	sum := sha256.Sum256([]byte("sonarr-1|download-1"))
	wantKey := hex.EncodeToString(sum[:])
	var openCount, closedDuplicateCount int
	var gotKey string
	if err := database.QueryRow(
		`SELECT COUNT(*), COALESCE(MAX(dedupe_key),'') FROM issues
		 WHERE instance_id = 'sonarr-1' AND download_id = 'download-1' AND closed_at IS NULL`,
	).Scan(&openCount, &gotKey); err != nil {
		t.Fatalf("load migrated auto issue: %v", err)
	}
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM issues
		 WHERE instance_id = 'sonarr-1' AND download_id = 'download-1' AND closed_at IS NOT NULL`,
	).Scan(&closedDuplicateCount); err != nil {
		t.Fatalf("count closed duplicate: %v", err)
	}
	if openCount != 1 || closedDuplicateCount != 1 || gotKey != wantKey {
		t.Fatalf("dedupe repair = open %d closed %d key %q, want 1/1/%q", openCount, closedDuplicateCount, gotKey, wantKey)
	}
	var safeParams, safeApproved string
	if err := database.QueryRow(
		"SELECT params, approved_params FROM agent_actions WHERE fingerprint = 'legacy-release-action'",
	).Scan(&safeParams, &safeApproved); err != nil {
		t.Fatalf("load scrubbed release action: %v", err)
	}
	if strings.Contains(safeParams, "legacy-release-secret") || strings.Contains(safeApproved, "legacy-release-secret") ||
		!strings.Contains(safeParams, "[REDACTED release sha256:") || !strings.Contains(safeApproved, "[REDACTED release sha256:") {
		t.Fatalf("legacy release capability not scrubbed: params=%s approved=%s", safeParams, safeApproved)
	}
	var legacyActionStatus, legacyIssueStatus, legacyRunStatus string
	if err := database.QueryRow(
		"SELECT status FROM agent_actions WHERE fingerprint = 'legacy-pending-release'",
	).Scan(&legacyActionStatus); err != nil {
		t.Fatalf("load legacy pending release: %v", err)
	}
	if err := database.QueryRow("SELECT status FROM issues WHERE id = ?", legacyIssueID).Scan(&legacyIssueStatus); err != nil {
		t.Fatalf("load legacy release issue: %v", err)
	}
	if err := database.QueryRow("SELECT status FROM agent_runs WHERE id = ?", legacyRunID).Scan(&legacyRunStatus); err != nil {
		t.Fatalf("load legacy release run: %v", err)
	}
	if legacyActionStatus != "superseded" || legacyIssueStatus != "needs_admin" || legacyRunStatus != "aborted" {
		t.Fatalf("legacy release gate repair = action %q issue %q run %q", legacyActionStatus, legacyIssueStatus, legacyRunStatus)
	}
	var legacyCost int64
	if err := database.QueryRow("SELECT cost_micros FROM agent_runs WHERE id = ?", legacyRunID).Scan(&legacyCost); err != nil {
		t.Fatalf("load cleared legacy run cost: %v", err)
	}
	if legacyCost != 0 {
		t.Fatalf("legacy agent run cost = %d, want erased", legacyCost)
	}
	rows, err := database.Query("SELECT arr_added_at,queue_file_id FROM issue_observation_downloads LIMIT 1")
	if err != nil {
		t.Fatalf("arr attempt-boundary migration missing: %v", err)
	}
	rows.Close()
}

func TestSafeReleaseActionJSONFingerprintsFakeRedactionMarkers(t *testing.T) {
	for _, unsafe := range []string{
		"[REDACTED release sha256:opaque-secret]",
		"https://idx.invalid/[REDACTED]?unrecognized=opaque-secret",
	} {
		safe, _, err := safeReleaseActionJSON(`{"media_type":"movie","guid":"` + unsafe + `","indexer_id":3}`)
		if err != nil {
			t.Fatalf("safeReleaseActionJSON(%q): %v", unsafe, err)
		}
		if strings.Contains(safe, "opaque-secret") || strings.Contains(safe, "unrecognized") ||
			!strings.Contains(safe, "[REDACTED release sha256:") {
			t.Fatalf("unsafe release marker survived migration boundary: %s", safe)
		}
	}
}
