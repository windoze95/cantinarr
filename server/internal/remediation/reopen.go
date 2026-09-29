package remediation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/windoze95/cantinarr-server/internal/secrets"
)

var errReopenIssueNotFound = errors.New("issue not found")

// IssueReopenConflict leaves the closed issue untouched. ExistingIssueID, when
// present, links to the open report that already owns this exact incident.
type IssueReopenConflict struct {
	ExistingIssueID int64
	Reason          string
}

func (e *IssueReopenConflict) Error() string { return e.Reason }

func canReopenIssue(issue *Issue) bool {
	if issue == nil || issue.ClosedAt == nil {
		return false
	}
	switch issue.Status {
	case IssueResolved, IssueWontFix, IssueDismissed, "failed":
		return true
	}
	return false
}

// ReopenIssueByAdmin restores the conversation for manual review. It never
// enqueues a run or revives a previous approval. The closure snapshot, actor,
// dedupe check and transition commit together under SQLite's writer lock.
func (s *Service) ReopenIssueByAdmin(ctx context.Context, adminID, issueID int64) (*Issue, error) {
	if adminID <= 0 {
		return nil, fmt.Errorf("admin identity is required")
	}
	// Serialize with snapshot reconciliation so an observation read just before
	// the reopen cannot apply its old lifecycle state after this transaction.
	s.observationMu.Lock()
	defer s.observationMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin issue reopen: %w", err)
	}
	defer tx.Rollback()
	// Acquire the writer before reading identity or checking duplicates. A
	// competing reopen/create/approval cannot pass those checks concurrently.
	res, err := tx.ExecContext(ctx, `UPDATE issues SET reopened_at = CURRENT_TIMESTAMP
		WHERE id = ? AND closed_at IS NOT NULL AND status IN (?, ?, ?, 'failed')`,
		issueID, IssueResolved, IssueWontFix, IssueDismissed)
	if err != nil {
		return nil, fmt.Errorf("claim closed issue: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var exists int
		if err := tx.QueryRowContext(ctx, "SELECT 1 FROM issues WHERE id = ?", issueID).Scan(&exists); err == sql.ErrNoRows {
			return nil, errReopenIssueNotFound
		} else if err != nil {
			return nil, fmt.Errorf("find issue to reopen: %w", err)
		}
		return nil, &IssueReopenConflict{Reason: "This issue is already open or has changed. Refresh it and try again."}
	}
	var executing bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM agent_actions WHERE issue_id = ? AND status = ?)`, issueID, ActionExecuting).Scan(&executing); err != nil {
		return nil, fmt.Errorf("check executing fix: %w", err)
	}
	if executing {
		return nil, &IssueReopenConflict{Reason: "A fix is still running. Wait for it to finish before reopening this issue."}
	}
	// Match the same boundaries used by creation: auto/system dedupe key, or
	// reporter + instance + media identity + exact episode/format + category.
	var existingID int64
	err = tx.QueryRowContext(ctx, `SELECT candidate.id FROM issues original JOIN issues candidate
		ON candidate.id != original.id AND candidate.closed_at IS NULL
		WHERE original.id = ? AND (
		 (original.dedupe_key IS NOT NULL AND candidate.dedupe_key = original.dedupe_key)
		 OR (original.source = 'user' AND candidate.source = 'user'
		  AND candidate.reporter_id = original.reporter_id
		  AND candidate.instance_id = original.instance_id AND candidate.media_type = original.media_type
		  AND ((original.tmdb_id > 0 AND candidate.tmdb_id = original.tmdb_id)
		    OR (original.tvdb_id > 0 AND candidate.tvdb_id = original.tvdb_id)
		    OR (original.book_id > 0 AND candidate.book_id = original.book_id))
		  AND candidate.season_number = original.season_number AND candidate.episode_number = original.episode_number
		  AND candidate.category = original.category))
		ORDER BY candidate.id DESC LIMIT 1`, issueID).Scan(&existingID)
	if err == nil {
		return nil, &IssueReopenConflict{ExistingIssueID: existingID,
			Reason: fmt.Sprintf("Issue #%d is already open for this problem. Continue there.", existingID)}
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("check existing open issue: %w", err)
	}
	var status, resolution, kind string
	var closedAt time.Time
	if err := tx.QueryRowContext(ctx, `SELECT status, COALESCE(resolution, ''), resolution_kind, closed_at
		FROM issues WHERE id = ?`, issueID).Scan(&status, &resolution, &kind, &closedAt); err != nil {
		return nil, fmt.Errorf("read previous closure: %w", err)
	}
	reopenResult, err := tx.ExecContext(ctx, `INSERT INTO issue_messages (issue_id, author_kind, author_id, body)
		VALUES (?, ?, ?, ?)`, issueID, AuthorAdmin, adminID, reopenAuditMessage(status, kind, resolution, closedAt))
	if err != nil {
		return nil, fmt.Errorf("record issue reopening: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE issues SET status = ?, closed_at = NULL,
		resolution = NULL, resolution_kind = '', active_run_id = NULL, read = 1,
		updated_at = CURRENT_TIMESTAMP WHERE id = ?`, IssueNeedsAdmin, issueID); err != nil {
		return nil, fmt.Errorf("reopen issue: %w", err)
	}
	// Older closures may have left live proposals/runs behind. Retire those
	// defensively; retain every completed action and run as historical evidence.
	if _, err := tx.ExecContext(ctx, `UPDATE agent_actions SET status = ?,
		decided_at = COALESCE(decided_at, CURRENT_TIMESTAMP),
		result_text = COALESCE(result_text, 'Superseded when the issue was reopened for manual review.')
		WHERE issue_id = ? AND status = ?`, ActionSuperseded, issueID, ActionProposed); err != nil {
		return nil, fmt.Errorf("retire stale proposals: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_runs SET status = 'aborted', stop_reason = 'admin_reopened',
		finished_at = COALESCE(finished_at, CURRENT_TIMESTAMP)
		WHERE issue_id = ? AND status IN ('running','waiting_user','waiting_approval','resume_pending')`, issueID); err != nil {
		return nil, fmt.Errorf("retire stale investigations: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit issue reopening: %w", err)
	}
	messageID, _ := reopenResult.LastInsertId()
	s.observeReport("issue_reopened", issueID, adminID, messageID)
	s.pingIssueUpdated(issueID)
	return s.GetIssue(issueID)
}

func reopenAuditMessage(status, kind, resolution string, closedAt time.Time) string {
	outcome := map[string]string{
		IssueResolved: "Resolved", IssueWontFix: "Closed without a fix",
		IssueDismissed: "Dismissed", "failed": "Could not resolve",
	}[status]
	provenance := map[string]string{
		ResolutionAgentConcluded: "Review completed", ResolutionArrStateCleared: "Media became available",
		ResolutionReporterTimeout: "Closed after no reply", ResolutionReporterConfirmed: "Confirmed fixed by the reporter",
		ResolutionAdminDismissed: "Closed after administrator review", ResolutionAdminCompleted: "Completed after administrator review",
		ResolutionAIHealthRestored: "Shared AI recovered", ResolutionPushDeliveryRestored: "Notifications recovered",
		ResolutionBookImportCleared: "Book imports cleared", ResolutionRemovedNoReplacement: "Dead download removed; no copy available yet",
		ResolutionRemovedWaitingForAir:          "Bad download removed; episode has not aired yet",
		ResolutionRemediationProviderConfigured: "AI service restored", ResolutionPreventionSettingChanged: "Settings changed",
	}[kind]
	if provenance == "" {
		provenance = "How it closed is unknown"
	}
	body := fmt.Sprintf("Reopened issue.\n\nPrevious closure: %s — %s (%s).", outcome, provenance, closedAt.UTC().Format(time.RFC3339))
	if resolution != "" {
		body += "\n" + secrets.RedactText(resolution)
	}
	return body
}
