package remediation

import (
	"context"
	"database/sql"
	"encoding/json"
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
		// row exists, so a resumed/duplicated conclude does not error. No circuit-
		// breaker update here: the row did not transition, so this is not a fresh
		// terminal outcome (a double-conclude must never double-count a give-up).
		var exists int
		if qerr := s.db.QueryRowContext(ctx, "SELECT 1 FROM issues WHERE id = ?", issueID).Scan(&exists); qerr == sql.ErrNoRows {
			return fmt.Errorf("issue not found")
		}
		return nil
	}

	// Release any run claim now that the issue is terminal.
	s.db.ExecContext(ctx, "UPDATE issues SET active_run_id = NULL WHERE id = ?", issueID)
	// Feed the auto-dispatch circuit breaker exactly once per real terminal
	// transition: resolved resets the give-up streak, a non-resolved close bumps
	// it (and may disarm auto-dispatch). A no-op for user-reported issues.
	s.noteAutoTerminal(issueID, status)
	s.notifyIssueResolved(issueID, status)
	return nil
}

// RemediationEnabled reports the master switch. Every agent-only tool and the
// Runner entry points consult it so nothing runs while the feature is off.
func (s *Service) RemediationEnabled(ctx context.Context) bool {
	return s.Settings().Enabled
}

// ProposeAction records a proposed (admin-approvable) arr mutation. It validates
// params against the kind's schema (rejecting unknown fields / bad values),
// computes the canonical fingerprint, and conditionally inserts an agent_actions
// row keyed by that fingerprint under the single-writer DB — so a re-proposed
// identical action does NOT create a duplicate and is reported as already
// proposed/decided. The stored params are the CANONICAL form the Executor will
// replay verbatim on approval. On a genuinely new proposal it notifies admins
// with a fixed-template event (ids + kind only; no model text on the wire).
func (s *Service) ProposeAction(ctx context.Context, issueID int64, kindStr string, rawParams json.RawMessage, rationale, toolUseID string) (proposalID int64, alreadyExisted bool, err error) {
	kind := ActionKind(kindStr)
	canonical, verr := validateActionParams(kind, rawParams)
	if verr != nil {
		return 0, false, verr
	}
	fp := fingerprint(issueID, kind, canonical)

	// Resolve the originating run (the issue's current claim) so the audit links
	// the proposal to the run that made it. Best-effort: a NULL run_id is fine.
	var runID sql.NullInt64
	s.db.QueryRowContext(ctx, "SELECT active_run_id FROM issues WHERE id = ?", issueID).Scan(&runID)

	// Conditional insert: only create the row if no action with this fingerprint
	// exists yet. The UNIQUE(fingerprint) index is the backstop; the NOT EXISTS
	// keeps the common path a clean no-op instead of a constraint error.
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_actions (issue_id, run_id, tool_use_id, kind, params, rationale, risk, status, fingerprint)
		 SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?
		 WHERE NOT EXISTS (SELECT 1 FROM agent_actions WHERE fingerprint = ?)`,
		issueID, runID, sqlNullStr(toolUseID), kindStr, string(canonical), rationale, "mutating", ActionProposed, fp,
		fp,
	)
	if err != nil {
		return 0, false, fmt.Errorf("record proposal: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Already proposed/decided: return the existing row id, no new notification.
		var existingID int64
		if qerr := s.db.QueryRowContext(ctx, "SELECT id FROM agent_actions WHERE fingerprint = ?", fp).Scan(&existingID); qerr != nil {
			return 0, true, nil
		}
		return existingID, true, nil
	}
	newID, err := res.LastInsertId()
	if err != nil {
		return 0, false, fmt.Errorf("proposal id: %w", err)
	}

	s.notifyActionPending(issueID, kindStr)
	return newID, false, nil
}

// notifyActionPending fires the agent_action_pending admin event when the agent
// proposes a fix. The body is a FIXED template on the push side; here we pass
// only structured ids + kind + the live pending count for badging (never the
// untrusted rationale or any release name).
func (s *Service) notifyActionPending(issueID int64, kind string) {
	if s.notifier == nil {
		return
	}
	data := map[string]interface{}{
		"issue_id": issueID,
		"kind":     kind,
	}
	if title, err := s.issueTitle(issueID); err == nil {
		data["title"] = title
	}
	if count, err := s.PendingActionCount(); err == nil {
		data["pending_count"] = count
	}
	s.notifier.NotifyAdmins("agent_action_pending", data)
}

// issueTitle reads an issue's display title (arr/user-sourced; passed as a
// structured field, never concatenated into a notification body).
func (s *Service) issueTitle(issueID int64) (string, error) {
	var title string
	err := s.db.QueryRow("SELECT title FROM issues WHERE id = ?", issueID).Scan(&title)
	return title, err
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
