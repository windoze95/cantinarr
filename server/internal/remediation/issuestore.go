package remediation

import (
	"context"
	"database/sql"
	"fmt"
)

// This file implements mcp.IssueStore on *Service so the agent-only MCP tools
// (post_issue_message / conclude_issue) can write issue rows without internal/mcp
// importing internal/remediation. The interface lives in internal/mcp; the
// dependency points mcp -> (its own interface) and remediation -> mcp, never the
// other way, which is what breaks the cycle.
//
// A compile-time assertion that *Service satisfies the interface lives in
// runner.go (where the mcp import is already present) to keep this file free of
// the import when only the methods are needed.

// PostIssueMessage appends an agent-authored message to an issue's thread and
// pings live watchers. The body is the model's plain-language finding; it is
// stored verbatim and is never interpreted as an instruction by any code path.
func (s *Service) PostIssueMessage(ctx context.Context, issueID int64, body string) error {
	if body == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx,
		"INSERT INTO issue_messages (issue_id, author_kind, author_id, body) VALUES (?, ?, NULL, ?)",
		issueID, AuthorAgent, body,
	); err != nil {
		return fmt.Errorf("post agent message: %w", err)
	}
	s.db.ExecContext(ctx, "UPDATE issues SET updated_at = CURRENT_TIMESTAMP WHERE id = ?", issueID)
	s.pingIssueUpdated(issueID)
	return nil
}

// ConcludeIssue moves an issue to a terminal state (resolved | wont_fix), records
// the closing note, and stamps closed_at so it leaves the open set (and the
// dedupe index no longer guards its key). It is idempotent on closed_at IS NULL,
// so a double-conclude is a no-op. An out-of-vocabulary status is coerced to
// wont_fix so a terminal state always results.
func (s *Service) ConcludeIssue(ctx context.Context, issueID int64, status, resolution string) error {
	if status != IssueResolved && status != IssueWontFix {
		status = IssueWontFix
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE issues SET status = ?, resolution = ?, closed_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND closed_at IS NULL`,
		status, resolution, issueID,
	)
	if err != nil {
		return fmt.Errorf("conclude issue: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Already closed (or no such issue): treat as idempotent success when the
		// row exists, so a resumed/duplicated conclude does not error.
		var exists int
		if qerr := s.db.QueryRowContext(ctx, "SELECT 1 FROM issues WHERE id = ?", issueID).Scan(&exists); qerr == sql.ErrNoRows {
			return fmt.Errorf("issue not found")
		}
		return nil
	}

	// Release any run claim now that the issue is terminal.
	s.db.ExecContext(ctx, "UPDATE issues SET active_run_id = NULL WHERE id = ?", issueID)
	s.notifyIssueResolved(issueID, status)
	return nil
}

// RemediationEnabled reports the master switch. Every agent-only tool and the
// Runner entry points consult it so nothing runs while the feature is off.
func (s *Service) RemediationEnabled(ctx context.Context) bool {
	return s.Settings().Enabled
}

// pingIssueUpdated fires a thin issue_updated ping (id only — no body text on the
// wire) to the reporter (if any) and to admins so a live thread refreshes.
func (s *Service) pingIssueUpdated(issueID int64) {
	if s.notifier == nil {
		return
	}
	data := map[string]interface{}{"issue_id": issueID}
	var reporterID sql.NullInt64
	if err := s.db.QueryRow("SELECT reporter_id FROM issues WHERE id = ?", issueID).Scan(&reporterID); err == nil && reporterID.Valid {
		s.notifier.NotifyUser(reporterID.Int64, "issue_updated", data)
	}
	s.notifier.NotifyAdmins("issue_updated", data)
}

// notifyIssueResolved pings on terminal resolution. Only the issue id + a fixed
// event string travel; no model-authored text is interpolated onto a
// notification (the untrusted-text invariant).
func (s *Service) notifyIssueResolved(issueID int64, status string) {
	if s.notifier == nil {
		return
	}
	data := map[string]interface{}{"issue_id": issueID, "status": status}
	var reporterID sql.NullInt64
	if err := s.db.QueryRow("SELECT reporter_id FROM issues WHERE id = ?", issueID).Scan(&reporterID); err == nil && reporterID.Valid {
		s.notifier.NotifyUser(reporterID.Int64, "issue_updated", data)
	}
	s.notifier.NotifyAdmins("issue_updated", data)
}
