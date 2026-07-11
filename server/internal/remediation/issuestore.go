package remediation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/mcp"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

const maxAdminResolutionNoteBytes = maxIssueReplyBytes

// ErrIssueCompletionConflict means another close or an in-flight approved
// mutation won the race with an admin completion attempt. The handler maps it
// to HTTP 409 and clients reconcile the authoritative issue.
var ErrIssueCompletionConflict = errors.New("issue completion conflict")

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
	if len(body) > maxIssueDetailBytes {
		return fmt.Errorf("agent message is too long")
	}
	body = secrets.RedactText(body)
	expectedRunID := mcp.AgentRunOwnership(ctx)
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO issue_messages (issue_id, author_kind, author_id, body)
		 SELECT ?, ?, NULL, ? WHERE EXISTS (
		   SELECT 1 FROM issues i WHERE i.id = ? AND i.closed_at IS NULL
		     AND (? = 0 OR (i.status = ? AND i.active_run_id = ? AND EXISTS (
		       SELECT 1 FROM agent_runs r WHERE r.id = ? AND r.issue_id = i.id AND r.status = ?
		     )))
		 )`,
		issueID, AuthorAgent, body, issueID,
		expectedRunID, IssueInvestigating, expectedRunID, expectedRunID, runStatusRunning,
	)
	if err != nil {
		return fmt.Errorf("post agent message: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if expectedRunID != 0 {
			return fmt.Errorf("agent run no longer owns this issue")
		}
		return nil // external/admin closure won the race; do not append afterward.
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
	if len(resolution) > maxIssueDetailBytes {
		return fmt.Errorf("issue resolution is too long")
	}
	if status != IssueResolved && status != IssueWontFix {
		status = IssueWontFix
	}
	resolution = secrets.RedactText(resolution)
	kind := ResolutionAgentConcluded
	if resolution == ResolutionUserUnresponsive {
		kind = ResolutionReporterTimeout
	}
	return s.concludeIssue(ctx, issueID, status, resolution, kind)
}

// concludeIssue is the transactional terminal transition for the entire issue
// aggregate. Closing the issue, superseding live proposals, and aborting parked
// work commit together, so no closed issue can retain an executable approval.
func (s *Service) concludeIssue(ctx context.Context, issueID int64, status, resolution, resolutionKind string) error {
	_, err := s.concludeIssueCAS(ctx, issueID, status, resolution, resolutionKind, "", "")
	return err
}

// concludeIssueCAS is concludeIssue with an optional expected status and SQLite
// age modifier. The reply-timeout sweeper uses both so a reply racing its stale
// snapshot wins cleanly instead of closing an answered issue.
func (s *Service) concludeIssueCAS(ctx context.Context, issueID int64, status, resolution, resolutionKind, expectedStatus, ageModifier string) (bool, error) {
	return s.concludeIssueAggregate(ctx, issueID, status, resolution, resolutionKind, issueClosureOptions{
		expectedStatus: expectedStatus,
		ageModifier:    ageModifier,
	})
}

type issueClosureOptions struct {
	expectedStatus      string
	expectedRunID       int64
	ageModifier         string
	conflictIfClosed    bool
	adminID             int64
	silentNotifications bool
}

// ResolveIssueByAdmin records a human-reviewed terminal disposition. It is
// intentionally separate from DismissIssue: the required note and admin actor
// are committed with aggregate closure under ResolutionAdminCompleted.
func (s *Service) ResolveIssueByAdmin(ctx context.Context, adminID, issueID int64, disposition AdminIssueDisposition, note string) (*Issue, error) {
	note = strings.TrimSpace(note)
	if disposition != AdminDispositionResolved && disposition != AdminDispositionWontFix {
		return nil, fmt.Errorf("disposition must be resolved or wont_fix")
	}
	if note == "" {
		return nil, fmt.Errorf("resolution note is required")
	}
	if len(note) > maxAdminResolutionNoteBytes {
		return nil, fmt.Errorf("resolution note is too long")
	}
	if adminID <= 0 {
		return nil, fmt.Errorf("admin identity is required")
	}
	note = secrets.RedactText(note)
	transitioned, err := s.concludeIssueAggregate(
		ctx, issueID, string(disposition), note, ResolutionAdminCompleted,
		issueClosureOptions{conflictIfClosed: true, adminID: adminID},
	)
	if err != nil {
		return nil, err
	}
	if !transitioned {
		return nil, fmt.Errorf("%w: issue is already closed or changed", ErrIssueCompletionConflict)
	}
	return s.GetIssue(issueID)
}

func (s *Service) concludeIssueAggregate(ctx context.Context, issueID int64, status, resolution, resolutionKind string, opts issueClosureOptions) (bool, error) {
	resolution = secrets.RedactText(resolution)
	if status != IssueResolved && status != IssueWontFix && status != IssueDismissed {
		status = IssueWontFix
	}
	// Agent/system conclusions flip the issue back to unread unless the resolved
	// preference overrides it. Explicit admin completion and dismissal are human
	// decisions already seen by an admin, so they close read.
	read := 0
	if status == IssueDismissed || resolutionKind == ResolutionAdminCompleted || (status == IssueResolved && s.Settings().MarkResolvedAsRead) {
		read = 1
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin conclude issue: %w", err)
	}
	defer tx.Rollback()
	if resolutionKind == ResolutionArrStateCleared || resolutionKind == ResolutionAdminDismissed || resolutionKind == ResolutionAdminCompleted {
		var executing int
		if err := tx.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM agent_actions WHERE issue_id = ? AND status = ?",
			issueID, ActionExecuting,
		).Scan(&executing); err != nil {
			return false, fmt.Errorf("check executing issue action: %w", err)
		}
		if executing > 0 {
			if resolutionKind == ResolutionAdminCompleted {
				return false, fmt.Errorf("%w: an approved fix is still executing", ErrIssueCompletionConflict)
			}
			return false, fmt.Errorf("an approved fix is still executing; wait for its outcome before closing the issue")
		}
	}
	if resolutionKind == ResolutionArrStateCleared {
		var unknown int
		if err := tx.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM agent_actions WHERE issue_id = ? AND status = ?",
			issueID, ActionOutcomeUnknown,
		).Scan(&unknown); err != nil {
			return false, fmt.Errorf("check unknown issue action: %w", err)
		}
		if unknown > 0 {
			// The original queue row disappearing is expected after some partial
			// fixes (for example remove succeeded, replacement grab failed). Preserve
			// the human-verification boundary and record the new observation once.
			const body = "The original queue signal has cleared, but an approved fix has an unknown or partial outcome. The issue remains open for an administrator to verify the current arr state."
			msgRes, err := tx.ExecContext(ctx,
				`INSERT INTO issue_messages (issue_id, author_kind, author_id, body)
				 SELECT ?, ?, NULL, ?
				 WHERE NOT EXISTS (
				   SELECT 1 FROM issue_messages WHERE issue_id = ? AND author_kind = ? AND body = ?
				 )`,
				issueID, AuthorSystem, body, issueID, AuthorSystem, body,
			)
			if err != nil {
				return false, fmt.Errorf("record cleared signal awaiting verification: %w", err)
			}
			if err := tx.Commit(); err != nil {
				return false, fmt.Errorf("commit cleared signal evidence: %w", err)
			}
			if inserted, _ := msgRes.RowsAffected(); inserted > 0 {
				s.pingIssueUpdated(issueID)
			}
			return false, nil
		}
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE issues SET status = ?, resolution = ?, resolution_kind = ?, read = ?,
		 active_run_id = NULL, closed_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND closed_at IS NULL
		   AND (? = '' OR status = ?)
		   AND (? = 0 OR (status = ? AND active_run_id = ? AND EXISTS (
		     SELECT 1 FROM agent_runs r WHERE r.id = ? AND r.issue_id = issues.id AND r.status = 'running'
		   )))
		   AND (? = '' OR updated_at <= datetime('now', ?))`,
		status, resolution, resolutionKind, read, issueID,
		opts.expectedStatus, opts.expectedStatus,
		opts.expectedRunID, IssueInvestigating, opts.expectedRunID, opts.expectedRunID,
		opts.ageModifier, opts.ageModifier,
	)
	if err != nil {
		return false, fmt.Errorf("conclude issue: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Already closed (or no such issue): treat as idempotent success when the
		// row exists, so a resumed/duplicated conclude does not error. No circuit-
		// breaker update here: the row did not transition, so this is not a fresh
		// terminal outcome (a double-conclude must never double-count a give-up).
		if opts.conflictIfClosed {
			var exists int
			if qerr := tx.QueryRowContext(ctx, "SELECT 1 FROM issues WHERE id = ?", issueID).Scan(&exists); qerr == sql.ErrNoRows {
				return false, fmt.Errorf("issue not found")
			} else if qerr != nil {
				return false, fmt.Errorf("reload issue after completion race: %w", qerr)
			}
			return false, nil
		}
		if opts.expectedStatus != "" || opts.expectedRunID != 0 || opts.ageModifier != "" {
			return false, nil
		}
		var exists int
		if qerr := tx.QueryRowContext(ctx, "SELECT 1 FROM issues WHERE id = ?", issueID).Scan(&exists); qerr == sql.ErrNoRows {
			return false, fmt.Errorf("issue not found")
		}
		return false, nil
	}

	actionRes, err := tx.ExecContext(ctx,
		`UPDATE agent_actions
		 SET status = ?, decided_at = COALESCE(decided_at, CURRENT_TIMESTAMP),
		     result_text = COALESCE(result_text, 'Superseded because the issue closed before approval; no fix was executed.')
		 WHERE issue_id = ? AND status = ?`,
		ActionSuperseded, issueID, ActionProposed,
	)
	if err != nil {
		return false, fmt.Errorf("supersede issue actions: %w", err)
	}
	superseded, _ := actionRes.RowsAffected()

	// An external observation/admin close can race a running or parked agent.
	// Terminalize those runs here; an agent-concluded run is finalized by the
	// Runner immediately after this transaction and must not be mislabeled.
	if resolutionKind == ResolutionArrStateCleared || resolutionKind == ResolutionAdminDismissed || resolutionKind == ResolutionReporterTimeout || resolutionKind == ResolutionAdminCompleted {
		stopReason := "external_resolution"
		if resolutionKind == ResolutionAdminDismissed {
			stopReason = "admin_dismissed"
		} else if resolutionKind == ResolutionAdminCompleted {
			stopReason = "admin_completed"
		} else if resolutionKind == ResolutionReporterTimeout {
			stopReason = stopUserUnresponsive
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE agent_runs SET status = 'aborted', stop_reason = ?, deadline_at = NULL,
				 finished_at = COALESCE(finished_at, CURRENT_TIMESTAMP)
				 WHERE issue_id = ? AND status IN ('running','waiting_user','waiting_approval','resume_pending')
				   AND (? = 0 OR id != ?)`,
			stopReason, issueID, opts.expectedRunID, opts.expectedRunID,
		); err != nil {
			return false, fmt.Errorf("abort issue runs: %w", err)
		}
	}
	if resolutionKind == ResolutionAdminCompleted {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO issue_messages (issue_id, author_kind, author_id, body)
			 VALUES (?, ?, ?, ?)`,
			issueID, AuthorAdmin, opts.adminID, resolution,
		); err != nil {
			return false, fmt.Errorf("record admin resolution note: %w", err)
		}
	}
	if resolutionKind == ResolutionReporterTimeout {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO issue_messages (issue_id, author_kind, author_id, body)
			 VALUES (?, ?, NULL, ?)`,
			issueID, AuthorAgent,
			"I didn't hear back, so I'm closing this for now. If it's still a problem, please report it again and I'll take another look.",
		); err != nil {
			return false, fmt.Errorf("record reporter-timeout message: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit conclude issue: %w", err)
	}

	// Feed the auto-dispatch circuit breaker exactly once per real terminal
	// transition: resolved resets the give-up streak, a non-resolved close bumps
	// it (and may disarm auto-dispatch). Manual admin closure is excluded because
	// it is neither an autonomous success nor an agent give-up.
	if resolutionKind != ResolutionAdminDismissed && resolutionKind != ResolutionAdminCompleted {
		s.noteAutoTerminal(issueID, status)
	}
	if !opts.silentNotifications {
		s.notifyIssueResolved(issueID, status)
		if superseded > 0 {
			s.notifyActionsChanged(issueID, "superseded")
		}
	}
	return true, nil
}

// EscalateIssue releases the agent claim but deliberately keeps the issue open
// for an administrator. Agent exhaustion or missing AI configuration is not a
// truthful "won't fix" outcome and must not disappear from the attention queue.
func (s *Service) EscalateIssue(ctx context.Context, issueID int64, reason string) error {
	_, err := s.GiveUpIssue(ctx, issueID, 0, "", "", reason)
	return err
}

// GiveUpIssue atomically terminalizes the active run and moves its issue to
// needs_admin with the human-readable message. A process loss can therefore
// never leave an investigating issue pointing at a gave_up run.
func (s *Service) GiveUpIssue(ctx context.Context, issueID, runID int64, stopReason, message, reason string) (bool, error) {
	message = secrets.RedactText(message)
	reason = secrets.RedactText(reason)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin give-up transition: %w", err)
	}
	defer tx.Rollback()
	if runID != 0 {
		res, err := tx.ExecContext(ctx,
			`UPDATE agent_runs SET status = ?, stop_reason = ?, deadline_at = NULL,
			 finished_at = COALESCE(finished_at, CURRENT_TIMESTAMP)
			 WHERE id = ? AND issue_id = ? AND status IN (?, ?)`,
			runStatusGaveUp, stopReason, runID, issueID, runStatusRunning, runStatusResumePending,
		)
		if err != nil {
			return false, fmt.Errorf("finalize give-up run: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return false, nil
		}
	}
	var res sql.Result
	if runID == 0 {
		// This path is used only when provider setup failed before a run could be
		// claimed. Do not let a stale queued job overwrite a reporter/admin gate
		// that another worker established in the meantime.
		res, err = tx.ExecContext(ctx,
			`UPDATE issues SET status = ?, resolution = ?, resolution_kind = '', read = 0,
			 active_run_id = NULL, updated_at = CURRENT_TIMESTAMP
			 WHERE id = ? AND closed_at IS NULL AND active_run_id IS NULL
			   AND status IN (?, ?)`,
			IssueNeedsAdmin, reason, issueID, IssueOpen, IssueInvestigating,
		)
	} else {
		res, err = tx.ExecContext(ctx,
			`UPDATE issues SET status = ?, resolution = ?, resolution_kind = '', read = 0,
			 active_run_id = NULL, updated_at = CURRENT_TIMESTAMP
			 WHERE id = ? AND closed_at IS NULL AND active_run_id = ?`,
			IssueNeedsAdmin, reason, issueID, runID,
		)
	}
	if err != nil {
		return false, fmt.Errorf("escalate give-up issue: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	actionRes, err := tx.ExecContext(ctx,
		`UPDATE agent_actions SET status = ?, decided_at = COALESCE(decided_at, CURRENT_TIMESTAMP),
		 result_text = COALESCE(result_text, 'Superseded because the agent could not durably finish its approval gate; no fix was executed.')
		 WHERE issue_id = ? AND status = ?`,
		ActionSuperseded, issueID, ActionProposed,
	)
	if err != nil {
		return false, fmt.Errorf("supersede give-up proposals: %w", err)
	}
	superseded, _ := actionRes.RowsAffected()
	if message != "" {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO issue_messages (issue_id, author_kind, author_id, body)
			 VALUES (?, ?, NULL, ?)`, issueID, AuthorAgent, message,
		); err != nil {
			return false, fmt.Errorf("record give-up message: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit give-up transition: %w", err)
	}
	// An unattended auto investigation that needs a human still feeds the
	// circuit breaker even though the incident remains open.
	s.noteAutoTerminal(issueID, IssueWontFix)
	if superseded > 0 {
		s.notifyActionsChanged(issueID, ActionSuperseded)
	} else {
		s.pingIssueUpdated(issueID)
	}
	return true, nil
}

// RemediationEnabled reports the master switch. Every agent-only tool and the
// Runner entry points consult it so nothing runs while the feature is off.
func (s *Service) RemediationEnabled(ctx context.Context) bool {
	return s.Settings().Enabled
}

// AskReporter validates that the issue has a reporter who can answer. The
// Runner later commits the question message, waiting_user run state, and
// awaiting_user issue state in one transaction after persisting the transcript.
//
// When the issue has NO reporter (an auto-detected issue), it returns
// hasReporter=false WITHOUT posting or notifying, so the caller does not park —
// the agent then decides itself or proposes a fix to the admin.
//
// The question body is the model's plain-language text; it is stored verbatim
// and never interpreted as an instruction. toolUseID is the ask_reporter
// tool_use.id: the Runner persists a placeholder tool_result keyed to it on park,
// and PostReply replaces that placeholder with the reply on resume — so the
// transcript stays well-formed (exactly one tool_result per tool_use).
func (s *Service) AskReporter(ctx context.Context, issueID int64, question, toolUseID string) (hasReporter bool, err error) {
	if len(question) > maxIssueReplyBytes {
		return false, fmt.Errorf("reporter question is too long")
	}
	question = secrets.RedactText(question)
	var reporterID sql.NullInt64
	var closedAt sql.NullTime
	if qerr := s.db.QueryRowContext(ctx, "SELECT reporter_id, closed_at FROM issues WHERE id = ?", issueID).Scan(&reporterID, &closedAt); qerr != nil {
		if qerr == sql.ErrNoRows {
			return false, fmt.Errorf("issue not found")
		}
		return false, fmt.Errorf("load issue: %w", qerr)
	}
	if closedAt.Valid {
		return false, fmt.Errorf("issue is already closed")
	}
	if !reporterID.Valid {
		// Auto-detected / reporter-less issue: do NOT park. The caller returns a
		// benign "no reporter to ask" result and the agent decides or proposes.
		return false, nil
	}

	return true, nil
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
	if len(rationale) > maxIssueDetailBytes {
		return 0, false, fmt.Errorf("proposal rationale is too long")
	}
	rationale = secrets.RedactText(rationale)
	kind := ActionKind(kindStr)
	canonical, verr := validateActionParams(kind, rawParams)
	if verr != nil {
		return 0, false, verr
	}
	if toolUseID == "" {
		return 0, false, fmt.Errorf("proposal requires its model tool gate id")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, fmt.Errorf("begin proposal: %w", err)
	}
	defer tx.Rollback()
	if err := validateActionScopeWith(tx, issueID, kind, canonical); err != nil {
		return 0, false, err
	}

	// Bind the proposal to the exact live run that owns this issue. The scope
	// validation and insert share this transaction, so a concurrent close/park
	// cannot leave a new proposal attached to a closed or unowned issue.
	var runID int64
	expectedRunID := mcp.AgentRunOwnership(ctx)
	if err := tx.QueryRowContext(ctx,
		`SELECT i.active_run_id FROM issues i JOIN agent_runs r ON r.id = i.active_run_id
		 WHERE i.id = ? AND i.status = ? AND i.closed_at IS NULL
		   AND i.active_run_id IS NOT NULL AND r.status = ?
		   AND (? = 0 OR i.active_run_id = ?)`,
		issueID, IssueInvestigating, runStatusRunning, expectedRunID, expectedRunID,
	).Scan(&runID); err != nil {
		if err == sql.ErrNoRows {
			return 0, false, fmt.Errorf("issue has no active investigation gate")
		}
		return 0, false, fmt.Errorf("load proposal run: %w", err)
	}
	fp := fingerprint(issueID, runID, toolUseID, kind, canonical)

	var pendingID int64
	var existingFP string
	if qerr := tx.QueryRowContext(ctx,
		"SELECT id, fingerprint FROM agent_actions WHERE issue_id = ? AND status = ? LIMIT 1",
		issueID, ActionProposed,
	).Scan(&pendingID, &existingFP); qerr == nil {
		if existingFP == fp {
			if err := tx.Commit(); err != nil {
				return 0, false, err
			}
			return pendingID, true, nil
		}
		return 0, false, fmt.Errorf("another proposal is already awaiting a decision for this issue")
	} else if qerr != sql.ErrNoRows {
		return 0, false, fmt.Errorf("check pending proposal: %w", qerr)
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO agent_actions (issue_id, run_id, tool_use_id, kind, params, rationale, risk, status, fingerprint)
		 SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?
		 WHERE NOT EXISTS (SELECT 1 FROM agent_actions WHERE fingerprint = ?)`,
		issueID, runID, toolUseID, kindStr, string(canonical), rationale, "mutating", ActionProposed, fp,
		fp,
	)
	if err != nil {
		return 0, false, fmt.Errorf("record proposal: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Already proposed/decided: return the existing row id, no new notification.
		var existingID int64
		if qerr := tx.QueryRowContext(ctx, "SELECT id FROM agent_actions WHERE fingerprint = ?", fp).Scan(&existingID); qerr != nil {
			return 0, true, nil
		}
		if err := tx.Commit(); err != nil {
			return 0, false, err
		}
		return existingID, true, nil
	}
	newID, err := res.LastInsertId()
	if err != nil {
		return 0, false, fmt.Errorf("proposal id: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, false, fmt.Errorf("commit proposal: %w", err)
	}
	return newID, false, nil
}

func (s *Service) notifyPendingActionForIssue(issueID int64) {
	var kind string
	if err := s.db.QueryRow(
		"SELECT kind FROM agent_actions WHERE issue_id = ? AND status = ? ORDER BY id DESC LIMIT 1",
		issueID, ActionProposed,
	).Scan(&kind); err == nil {
		s.notifyActionPending(issueID, kind)
	}
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
