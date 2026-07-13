package remediation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// This file is the safety-critical approve→execute→resume core. Two guarantees
// make an approved action run AT MOST ONCE, ever:
//
//  1. UNIQUE(fingerprint) on agent_actions — the agent can't even record the same
//     action twice (a re-proposal is an idempotent no-op).
//  2. A CAS `UPDATE ... SET status='executing' WHERE id=? AND status='proposed'`
//     before the Executor runs — only the single caller that flips proposed→
//     executing proceeds; a concurrent or repeat approval sees RowsAffected==0 and
//     returns without executing. Once executing, the row never returns to
//     proposed, so a failed execution is marked failed (never silently re-run).
//
// The model is entirely out of the loop here: ApproveAction passes the STORED,
// canonical proposal to the Executor, which revalidates it against fresh arr state
// before dispatch. The only model-facing effect is the resume tool_result appended
// to the transcript so the investigation can continue.

// ErrActionDecisionConflict means another admin decision, issue closure, or
// gate transition won before this decision could be recorded. Handlers map it
// to HTTP 409 so clients re-read the durable action instead of claiming their
// attempted decision succeeded.
var ErrActionDecisionConflict = errors.New("action decision conflict")

// ApproveAction claims a proposed action for at-most-once dispatch. An optional
// override is stored separately from the immutable proposal after re-validation.
// On success the action is marked executed. A definitive preflight failure may
// resume so the agent can try another tack; an ambiguous/partial outcome stops
// at needs_admin and never resumes the model before a human verifies remote
// state. No outcome ever returns to proposed, preserving at-most-once dispatch.
func (s *Service) ApproveAction(adminID, actionID int64, override *json.RawMessage) (*AgentAction, error) {
	act, err := s.loadActionForDecision(actionID)
	if err != nil {
		return nil, err
	}
	switch act.Status {
	case ActionExecuting, ActionExecuted, ActionFailed:
		// Safe retry after a lost HTTP response: the at-most-once claim already
		// won, so return the durable outcome without dispatching again.
		return s.GetAction(actionID)
	case ActionOutcomeUnknown:
		// Also repair legacy/crash-gap rows before returning the durable result;
		// an unknown outcome must never leave a resumable model gate behind.
		result := "The approved action's remote outcome is unknown. Verify the arr state manually; it will not be retried."
		if act.ResultText != nil && *act.ResultText != "" {
			result = *act.ResultText
		}
		if err := s.markActionOutcomeUnknown(act, result); err == nil {
			s.notifyActionsChanged(act.IssueID, ActionOutcomeUnknown)
		}
		return s.GetAction(actionID)
	case ActionProposed:
		// Continue below.
	default:
		return nil, fmt.Errorf("action is not awaiting a decision")
	}
	if !act.CanDecide {
		s.supersedeInvalidAction(actionID)
		return s.GetAction(actionID)
	}
	if recovering, preflightErr := s.preflightArrRecovery(act.IssueID); preflightErr != nil {
		if issue, issueErr := s.GetIssue(act.IssueID); issueErr == nil {
			_, _ = s.moveIssueToObservationNeedsAdmin(issue, observationNeedsCloserLook, time.Now().UTC())
		}
		return nil, fmt.Errorf("%w: could not verify whether the arr is already retrying: %v", ErrActionDecisionConflict, preflightErr)
	} else if recovering {
		return nil, fmt.Errorf("%w: live media state changed or is still active; no fix was executed", ErrActionDecisionConflict)
	}

	// An admin may edit the proposal before approving. Re-validate the override
	// against the kind's schema; the original proposal and fingerprint stay
	// immutable while approved_params records exactly what the admin authorized.
	paramsToRun, verr := validateActionParams(ActionKind(act.Kind), act.Params)
	if verr != nil {
		return nil, fmt.Errorf("stored proposal is no longer valid: %w", verr)
	}
	if override != nil && len(*override) > 0 && string(*override) != "null" {
		canonical, verr := validateActionParams(ActionKind(act.Kind), *override)
		if verr != nil {
			return nil, fmt.Errorf("invalid override: %w", verr)
		}
		paramsToRun = canonical
	}
	if err := s.validateActionScope(act.IssueID, ActionKind(act.Kind), paramsToRun); err != nil {
		return nil, fmt.Errorf("proposal no longer matches its issue: %w", err)
	}

	// Atomically persist the exact admin-approved params and claim the execution.
	// The original proposal params remain immutable for audit.
	cas, err := s.db.Exec(
		`UPDATE agent_actions
		 SET approved_params = ?, status = ?, decided_by = ?, decided_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status = ?
		   AND EXISTS (
		     SELECT 1 FROM issues i JOIN agent_runs r ON r.id = agent_actions.run_id
		     WHERE i.id = agent_actions.issue_id AND i.closed_at IS NULL
		       AND i.status = ? AND r.status = 'waiting_approval'
		   )`,
		string(paramsToRun), ActionExecuting, adminID, actionID, ActionProposed, IssueAwaitingApproval,
	)
	if err != nil {
		return nil, fmt.Errorf("claim action for execution: %w", err)
	}
	if n, _ := cas.RowsAffected(); n == 0 {
		// Another decision/closure won the race. Repair a terminal-parent proposal
		// and return the authoritative state; never execute.
		s.supersedeInvalidAction(actionID)
		return s.GetAction(actionID)
	}

	// Close the only local TOCTOU gap: recovery may have started after the first
	// read but before this action won its execution claim. Re-read immediately
	// before Executor; a positive result cancels the claim as superseded, and a
	// failed safety read records a definitive zero-mutation failure.
	if s.registry != nil || s.recoveryProbe != nil {
		issue, issueErr := s.GetIssue(act.IssueID)
		if issueErr != nil {
			_ = s.failExecutingRecoveryPreflight(act, issueErr)
			return nil, fmt.Errorf("%w: final arr-recovery safety check failed; no fix was executed", ErrActionDecisionConflict)
		}
		probe, probeErr := s.probeArrRecovery(issue)
		if probeErr != nil {
			_ = s.failExecutingRecoveryPreflight(act, probeErr)
			return nil, fmt.Errorf("%w: final arr-recovery safety check failed; no fix was executed", ErrActionDecisionConflict)
		}
		if probe.needsAdmin {
			if err := s.cancelExecutingForObservationReview(act, probe.reason); err != nil {
				return nil, fmt.Errorf("cancel action for changed media state: %w", err)
			}
			return nil, fmt.Errorf("%w: live media state changed before dispatch; no fix was executed", ErrActionDecisionConflict)
		}
		if probe.active || probe.completed {
			if err := s.cancelExecutingForRecovery(act, probe); err != nil {
				return nil, fmt.Errorf("cancel action for arr recovery: %w", err)
			}
			return nil, fmt.Errorf("%w: the arr began recovering before dispatch; no fix was executed", ErrActionDecisionConflict)
		}
	}

	// Replay the approved action against the arr. This is the ONLY mutation path.
	resultText, execErr := s.executor.Execute(context.Background(), act.IssueID, ActionKind(act.Kind), paramsToRun)
	resultText = secrets.RedactText(resultText)

	resumeText := ""
	finalStatus := ActionExecuted
	finalResult := resultText
	if execErr != nil {
		execErrText := secrets.RedactText(execErr.Error())
		var partial interface{ PartialMutation() bool }
		var notStarted interface{ MutationNotStarted() bool }
		if errors.As(execErr, &partial) && partial.PartialMutation() {
			// A compound action changed remote state before its follow-up failed.
			// Never call that a clean failure or offer an automatic whole-action retry.
			finalStatus = ActionOutcomeUnknown
			finalResult = "Partially executed; verify the arr state: " + execErrText
			resumeText = "Admin approved, but only part of the fix completed. Verify the current arr state before proposing anything else: " + execErrText
		} else if errors.As(execErr, &notStarted) && notStarted.MutationNotStarted() {
			// Live scope/candidate validation is read-only and happens before the
			// helper can dispatch a mutation. This is a definitive safe failure,
			// unlike a transport error after dispatch.
			finalStatus = ActionFailed
			finalResult = "Not executed: " + execErrText
			resumeText = "Admin approved, but the fix was not executed because its live preconditions failed: " + execErrText
		} else {
			// A transport error after a mutating request cannot prove the arr rejected
			// it. Be conservative: require verification and never retry automatically.
			finalStatus = ActionOutcomeUnknown
			finalResult = "Execution could not be confirmed; verify the arr state: " + execErrText
			resumeText = "Admin approved, but Cantinarr could not confirm whether the arr accepted the fix. Verify current state before proposing anything else: " + execErrText
		}
	} else {
		resumeText = "Approved and executed: " + resultText
	}
	if finalStatus == ActionOutcomeUnknown {
		// An unknown/partial outcome is a hard human-verification boundary. If we
		// resumed the model here it could propose a second mutation based on stale
		// state while the first may already have changed the arr.
		if err := s.markActionOutcomeUnknown(act, finalResult); err != nil {
			return nil, fmt.Errorf("record unknown action outcome: %w", err)
		}
		s.notifyActionsChanged(act.IssueID, ActionOutcomeUnknown)
		return s.GetAction(actionID)
	}
	finalizeRes, finalizeErr := s.db.Exec(
		"UPDATE agent_actions SET status = ?, executed_at = CURRENT_TIMESTAMP, result_text = ? WHERE id = ? AND status = ?",
		finalStatus, finalResult, actionID, ActionExecuting,
	)
	finalized := finalizeErr == nil
	if finalized {
		if n, _ := finalizeRes.RowsAffected(); n != 1 {
			finalized = false
		}
	}
	if !finalized {
		// Remote state may already have changed. Do not feed an unpersisted claim
		// back to the model or pretend it failed cleanly; stop at needs_admin.
		log.Printf("remediation: could not finalize action %d as %s: %v", actionID, finalStatus, finalizeErr)
		unknownText := "The approved action may have changed the arr, but Cantinarr could not persist its final outcome. Verify the arr state manually; it will not be retried."
		if err := s.markActionOutcomeUnknown(act, unknownText); err != nil {
			return nil, fmt.Errorf("approved action may have run, but its outcome could not be persisted: %w", err)
		}
		s.notifyActionsChanged(act.IssueID, ActionOutcomeUnknown)
		return s.GetAction(actionID)
	}

	// Feed the outcome back into the run transcript (keyed to the proposal's
	// tool_use_id) + the audit ledger, then enqueue the resume.
	resumeReady, resumeErr := s.appendResumeResult(act, resumeText)
	if resumeErr != nil {
		// The action outcome is already durable. Leave the run at its parked gate;
		// recoverWork reconstructs the handoff without replaying the mutation.
		log.Printf("remediation: stage approval resume for action %d: %v", actionID, resumeErr)
		_ = s.stopUnresumableDecision(act, "The action outcome was saved, but the agent transcript could not be resumed. An administrator needs to verify the arr state.")
	}
	s.notifyActionsChanged(act.IssueID, finalStatus)
	if resumeReady {
		s.EnqueueResume(act.IssueID)
	}

	return s.GetAction(actionID)
}

func (s *Service) markActionOutcomeUnknown(act *AgentAction, result string) error {
	result = secrets.RedactText(result)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(
		`UPDATE agent_actions SET status = ?, executed_at = COALESCE(executed_at, CURRENT_TIMESTAMP),
		 result_text = ? WHERE id = ? AND status IN (?, ?)`,
		ActionOutcomeUnknown, result, act.ID, ActionExecuting, ActionOutcomeUnknown,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("action execution claim changed unexpectedly")
	}
	if _, err := tx.Exec(
		`UPDATE issues SET status = ?, read = 0, active_run_id = NULL,
		 resolution = ?, resolution_kind = '', updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND closed_at IS NULL`,
		IssueNeedsAdmin, result, act.IssueID,
	); err != nil {
		return err
	}
	if act.RunID != nil {
		if _, err := tx.Exec(
			`UPDATE agent_runs SET status = 'aborted', stop_reason = 'action_outcome_unknown',
			 deadline_at = NULL, finished_at = COALESCE(finished_at, CURRENT_TIMESTAMP)
			 WHERE id = ? AND status IN (?, ?, ?, ?)`,
			*act.RunID, runStatusWaitingApproval, runStatusWaitingUser, runStatusRunning, runStatusResumePending,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) stopUnresumableDecision(act *AgentAction, reason string) error {
	reason = secrets.RedactText(reason)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`UPDATE issues SET status = ?, read = 0, active_run_id = NULL,
		 resolution = ?, resolution_kind = '', updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND closed_at IS NULL`,
		IssueNeedsAdmin, reason, act.IssueID,
	); err != nil {
		return err
	}
	if act.RunID != nil {
		if _, err := tx.Exec(
			`UPDATE agent_runs SET status = 'aborted', stop_reason = 'unresumable_transcript',
			 deadline_at = NULL, finished_at = COALESCE(finished_at, CURRENT_TIMESTAMP)
			 WHERE id = ? AND status IN (?, ?, ?, ?)`,
			*act.RunID, runStatusWaitingApproval, runStatusWaitingUser, runStatusResumePending, runStatusRunning,
		); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.pingIssueUpdated(act.IssueID)
	return nil
}

// DenyAction denies a proposed action and resumes the investigation so the agent
// can try a different approach within its remaining budget. A denial returns the
// issue to investigating (via the resume), NOT to a terminal state — that is what
// enables the multi-step "propose → denied → try something else" loop.
func (s *Service) DenyAction(adminID, actionID int64, note string) (*AgentAction, error) {
	if len(note) > maxIssueReplyBytes {
		return nil, fmt.Errorf("denial note is too long")
	}
	note = secrets.RedactText(note)
	act, err := s.loadActionForDecision(actionID)
	if err != nil {
		return nil, err
	}
	if act.Status == ActionDenied {
		return s.GetAction(actionID)
	}
	if act.Status != ActionProposed {
		return nil, fmt.Errorf("%w: action is already %s", ErrActionDecisionConflict, act.Status)
	}
	if !act.CanDecide {
		s.supersedeInvalidAction(actionID)
		return nil, fmt.Errorf("%w: action is no longer awaiting an active approval gate", ErrActionDecisionConflict)
	}

	// Guarded transition proposed -> denied (idempotent under a concurrent decide).
	cas, err := s.db.Exec(
		`UPDATE agent_actions SET status = ?, decided_by = ?, decided_at = CURRENT_TIMESTAMP, deny_reason = ?
		 WHERE id = ? AND status = ?
		   AND EXISTS (
		     SELECT 1 FROM issues i JOIN agent_runs r ON r.id = agent_actions.run_id
		     WHERE i.id = agent_actions.issue_id AND i.closed_at IS NULL
		       AND i.status = ? AND r.status = 'waiting_approval'
		   )`,
		ActionDenied, adminID, sqlNullStr(note), actionID, ActionProposed, IssueAwaitingApproval,
	)
	if err != nil {
		return nil, fmt.Errorf("deny action: %w", err)
	}
	if n, _ := cas.RowsAffected(); n == 0 {
		s.supersedeInvalidAction(actionID)
		current, currentErr := s.GetAction(actionID)
		if currentErr != nil {
			return nil, currentErr
		}
		// Two simultaneous denials are idempotent. Any other winner (most
		// importantly an approval that may already be executing remotely) is a
		// conflict, never a successful denial response.
		if current.Status == ActionDenied {
			return current, nil
		}
		return nil, fmt.Errorf("%w: action is now %s", ErrActionDecisionConflict, current.Status)
	}

	denyMsg := "Admin denied"
	if note != "" {
		denyMsg += ": " + note
	}
	resumeReady, resumeErr := s.appendResumeResult(act, denyMsg)
	if resumeErr != nil {
		log.Printf("remediation: stage denial resume for action %d: %v", actionID, resumeErr)
		_ = s.stopUnresumableDecision(act, "The denial was saved, but the agent transcript could not be resumed. An administrator needs to review the issue.")
	}
	s.notifyActionsChanged(act.IssueID, ActionDenied)
	if resumeReady {
		s.EnqueueResume(act.IssueID)
	}

	return s.GetAction(actionID)
}

func (s *Service) supersedeInvalidAction(actionID int64) {
	s.db.Exec(
		`UPDATE agent_actions
		 SET status = ?, decided_at = COALESCE(decided_at, CURRENT_TIMESTAMP),
		     result_text = COALESCE(result_text, 'Superseded because its approval gate is no longer active; no fix was executed.')
		 WHERE id = ? AND status = ?
		   AND NOT EXISTS (
		     SELECT 1 FROM issues i JOIN agent_runs r ON r.id = agent_actions.run_id
		     WHERE i.id = agent_actions.issue_id AND i.closed_at IS NULL
		       AND i.status = ? AND r.status = ?
		   )`,
		ActionSuperseded, actionID, ActionProposed, IssueAwaitingApproval, runStatusWaitingApproval,
	)
}

// appendResumeResult records the admin's decision back into the run so the resume
// continues exactly the tool_use -> tool_result cycle across the human gate.
//
// When the Runner parked it ALREADY persisted a tool_result block for the
// propose_action tool_use (a placeholder, "Proposal #N recorded; awaiting admin
// approval…"). So we must NOT append a second tool_result for the same
// tool_use_id — that would leave two tool_results for one tool_use across two
// user turns, which a real provider rejects (a tool_result must answer a
// tool_use in the immediately-preceding assistant turn). Instead we REPLACE that
// placeholder block's content in place with the decision outcome, keeping exactly
// one tool_result. Sibling read-tool results that shared the park turn are left
// untouched. Best-effort: a missing run/tool_use_id only means the resume re-
// seeds, never a crash.
func (s *Service) appendResumeResult(act *AgentAction, outcome string) (bool, error) {
	if act.RunID == nil || act.ToolUseID == "" {
		return false, fmt.Errorf("action has no resumable run/tool gate")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	ready, err := stageResumeResultTx(tx, act.IssueID, *act.RunID,
		IssueAwaitingApproval, runStatusWaitingApproval,
		"propose_action", act.ToolUseID, outcome, false)
	if err != nil || !ready {
		return ready, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// recoverDecisionHandoffs reconstructs the only non-atomic edge in approval:
// the arr mutation happens outside SQLite, so the process can stop after the
// durable action outcome but before the parked transcript is updated. It stages
// that already-decided outcome for resume without dispatching the action again.
func (s *Service) recoverDecisionHandoffs() {
	// Unknown/partial outcomes are never model-resumable. Repair rows written by
	// older builds or a crash gap into the same needs_admin + aborted-run boundary
	// used by the live approval path before considering ordinary handoffs.
	unknownRows, unknownErr := s.db.Query(
		`SELECT a.id, a.issue_id, COALESCE(a.run_id, 0), COALESCE(a.result_text, '')
		 FROM agent_actions a JOIN issues i ON i.id = a.issue_id
		 WHERE a.status = ? AND i.closed_at IS NULL
		   AND (i.status <> ? OR i.active_run_id IS NOT NULL OR EXISTS (
		     SELECT 1 FROM agent_runs r WHERE r.id = a.run_id
		       AND r.status IN (?, ?, ?, ?)
		   ))`,
		ActionOutcomeUnknown, IssueNeedsAdmin,
		runStatusWaitingApproval, runStatusWaitingUser, runStatusRunning, runStatusResumePending,
	)
	if unknownErr != nil {
		log.Printf("remediation: query unknown outcome repairs: %v", unknownErr)
	} else {
		type unknownRepair struct {
			actionID, issueID, runID int64
			result                   string
		}
		var repairs []unknownRepair
		for unknownRows.Next() {
			var repair unknownRepair
			if unknownRows.Scan(&repair.actionID, &repair.issueID, &repair.runID, &repair.result) == nil {
				repairs = append(repairs, repair)
			}
		}
		unknownRows.Close()
		for _, repair := range repairs {
			act := &AgentAction{ID: repair.actionID, IssueID: repair.issueID}
			if repair.runID > 0 {
				act.RunID = &repair.runID
			}
			result := repair.result
			if result == "" {
				result = "The approved action's remote outcome is unknown. Verify the arr state manually; it will not be retried."
			}
			if err := s.markActionOutcomeUnknown(act, result); err != nil {
				log.Printf("remediation: repair unknown action %d: %v", repair.actionID, err)
				continue
			}
			s.pingIssueUpdated(repair.issueID)
		}
	}

	rows, err := s.db.Query(
		`SELECT a.id, a.issue_id, a.run_id, COALESCE(a.tool_use_id,''), a.status,
		        COALESCE(a.deny_reason,''), COALESCE(a.result_text,'')
		 FROM agent_actions a
		 JOIN issues i ON i.id = a.issue_id
		 JOIN agent_runs r ON r.id = a.run_id
		 WHERE a.status IN (?, ?, ?) AND i.closed_at IS NULL AND i.status = ?
		   AND r.status = ?
		   AND a.id = (SELECT MAX(latest.id) FROM agent_actions latest WHERE latest.run_id = a.run_id)
		   AND NOT EXISTS (
		     SELECT 1 FROM agent_actions pending
		     WHERE pending.run_id = a.run_id AND pending.status = ?
		   )
		 ORDER BY a.id`,
		ActionExecuted, ActionFailed, ActionDenied,
		IssueAwaitingApproval, runStatusWaitingApproval, ActionProposed,
	)
	if err != nil {
		log.Printf("remediation: query decision handoffs: %v", err)
		return
	}
	type handoff struct {
		actionID, issueID, runID int64
		toolUseID, status        string
		denyReason, resultText   string
	}
	var pending []handoff
	for rows.Next() {
		var h handoff
		if rows.Scan(&h.actionID, &h.issueID, &h.runID, &h.toolUseID, &h.status, &h.denyReason, &h.resultText) == nil {
			pending = append(pending, h)
		}
	}
	rows.Close()
	for _, h := range pending {
		outcome := "Admin denied"
		switch h.status {
		case ActionExecuted:
			outcome = "Approved and executed: " + h.resultText
		case ActionFailed:
			detail := strings.TrimPrefix(h.resultText, "Execution failed: ")
			outcome = "Admin approved, but the fix failed to execute: " + detail
		case ActionDenied:
			if h.denyReason != "" {
				outcome += ": " + h.denyReason
			}
		}
		act := &AgentAction{
			ID: h.actionID, IssueID: h.issueID, RunID: &h.runID,
			ToolUseID: h.toolUseID,
		}
		if _, err := s.appendResumeResult(act, outcome); err != nil {
			log.Printf("remediation: recover decision handoff for action %d: %v", h.actionID, err)
			_ = s.stopUnresumableDecision(act, "The saved action decision could not be restored into the agent transcript. An administrator needs to review the issue and verify the arr state.")
		}
	}
}

// stageResumeResultTx atomically replaces a parked gate's placeholder result,
// records the human decision/reply in the audit ledger, and moves both the run
// and issue to a durable resume_pending handoff. A queue overflow or process
// restart can then lose only an in-memory hint, never the resume itself.
func stageResumeResultTx(tx *sql.Tx, issueID, runID int64, expectedIssueStatus, expectedRunStatus,
	toolName, toolUseID, outcome string, markUnread bool,
) (bool, error) {
	outcome = secrets.RedactText(outcome)
	if toolUseID == "" {
		return false, fmt.Errorf("missing tool gate id")
	}
	var transcriptJSON, runStatus string
	if err := tx.QueryRow(
		"SELECT transcript_json, status FROM agent_runs WHERE id = ? AND issue_id = ?",
		runID, issueID,
	).Scan(&transcriptJSON, &runStatus); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if runStatus == runStatusResumePending {
		return true, nil
	}
	if runStatus != expectedRunStatus {
		return false, nil
	}

	var history ai.Transcript
	if transcriptJSON != "" {
		if err := json.Unmarshal([]byte(transcriptJSON), &history); err != nil {
			return false, fmt.Errorf("decode parked transcript: %w", err)
		}
	}
	history = redactTranscript(history)
	if !replaceToolResult(history, toolUseID, outcome) {
		return false, fmt.Errorf("parked transcript has no matching %s result gate", toolName)
	}
	encoded, err := json.Marshal(history)
	if err != nil {
		return false, fmt.Errorf("encode resume transcript: %w", err)
	}

	issueRes, err := tx.Exec(
		`UPDATE issues SET status = ?, read = CASE WHEN ? THEN 0 ELSE read END,
		 active_run_id = NULL, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status = ? AND closed_at IS NULL AND active_run_id IS NULL`,
		IssueInvestigating, markUnread, issueID, expectedIssueStatus,
	)
	if err != nil {
		return false, err
	}
	if n, _ := issueRes.RowsAffected(); n == 0 {
		return false, nil
	}
	runRes, err := tx.Exec(
		`UPDATE agent_runs SET status = ?, stop_reason = NULL, deadline_at = NULL, transcript_json = ?
		 WHERE id = ? AND issue_id = ? AND status = ?`,
		runStatusResumePending, string(encoded), runID, issueID, expectedRunStatus,
	)
	if err != nil {
		return false, err
	}
	if n, _ := runRes.RowsAffected(); n == 0 {
		return false, fmt.Errorf("parked run changed during resume handoff")
	}

	var nextSeq int
	if err := tx.QueryRow("SELECT COALESCE(MAX(seq),0)+1 FROM agent_steps WHERE run_id = ?", runID).Scan(&nextSeq); err != nil {
		return false, err
	}
	if _, err := tx.Exec(
		`INSERT INTO agent_steps (run_id, issue_id, seq, kind, tool_name, tool_use_id, tool_output)
		 VALUES (?, ?, ?, 'tool_result', ?, ?, ?)`,
		runID, issueID, nextSeq, toolName, toolUseID, outcome,
	); err != nil {
		return false, err
	}
	return true, nil
}

// replaceToolResult rewrites the Content of the tool_result block matching
// toolUseID (the parked placeholder) with outcome, in place. Returns true when a
// block was found and replaced.
func replaceToolResult(history ai.Transcript, toolUseID, outcome string) bool {
	for i := range history {
		for j := range history[i].Content {
			b := &history[i].Content[j]
			if b.Type == ai.BlockToolResult && b.ToolUseID == toolUseID {
				b.Content = outcome
				b.IsError = false
				return true
			}
		}
	}
	return false
}

// askReporterToolName is the agent-only tool whose result a reporter reply
// answers. Kept as a literal (mirroring this file's "propose_action" usage) so
// the resume helpers don't pull internal/mcp into the resume path.
const askReporterToolName = "ask_reporter"

// stageReporterReplyTx turns a reporter/admin reply into a durable resume gate
// inside the same transaction that stores the thread message. Either both the
// answer and its model-facing tool result commit, or neither does.
func stageReporterReplyTx(tx *sql.Tx, issueID int64, reply string, markUnread bool) (bool, error) {
	var runID int64
	var transcriptJSON string
	err := tx.QueryRow(
		"SELECT id, transcript_json FROM agent_runs WHERE issue_id = ? AND status = ? ORDER BY id DESC LIMIT 1",
		issueID, runStatusWaitingUser,
	).Scan(&runID, &transcriptJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	var history ai.Transcript
	if transcriptJSON != "" {
		if err := json.Unmarshal([]byte(transcriptJSON), &history); err != nil {
			return false, fmt.Errorf("decode awaiting-user transcript: %w", err)
		}
	}
	askToolUseID, found := findAskReporterToolUse(history)
	if !found {
		return false, fmt.Errorf("parked run %d has no ask_reporter gate", runID)
	}
	return stageResumeResultTx(tx, issueID, runID,
		IssueAwaitingUser, runStatusWaitingUser,
		askReporterToolName, askToolUseID, "The reporter replied: "+reply, markUnread)
}

// findAskReporterToolUse returns the tool_use_id of the LAST ask_reporter
// tool_result block in the transcript (the freshly-parked placeholder). The last
// one is the currently-awaited question: any earlier ask_reporter result already
// carries a prior reply, so scanning newest-first keeps the right one even across
// multiple ask/reply cycles in one run.
func findAskReporterToolUse(history ai.Transcript) (string, bool) {
	for i := len(history) - 1; i >= 0; i-- {
		for j := range history[i].Content {
			b := history[i].Content[j]
			if b.Type == ai.BlockToolResult && b.Name == askReporterToolName && b.ToolUseID != "" {
				return b.ToolUseID, true
			}
		}
	}
	return "", false
}

// loadActionForDecision loads the fields ApproveAction/DenyAction need, including
// tool_use_id and run_id (for the resume) which the list/get DTO also carries.
func (s *Service) loadActionForDecision(actionID int64) (*AgentAction, error) {
	row := s.db.QueryRow(
		`SELECT a.id, a.issue_id, a.run_id, a.tool_use_id, a.kind, a.params,
		        a.approved_params, a.rationale, a.risk, a.status, i.status, i.closed_at,
		        EXISTS (SELECT 1 FROM agent_runs r WHERE r.id = a.run_id AND r.status = 'waiting_approval')
		 FROM agent_actions a JOIN issues i ON i.id = a.issue_id WHERE a.id = ?`,
		actionID,
	)
	var (
		act            AgentAction
		runID          sql.NullInt64
		toolUseID      sql.NullString
		params         string
		approvedParams sql.NullString
		closedAt       sql.NullTime
	)
	if err := row.Scan(&act.ID, &act.IssueID, &runID, &toolUseID, &act.Kind, &params,
		&approvedParams, &act.Rationale, &act.Risk, &act.Status, &act.IssueStatus, &closedAt, &act.GateValid); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("action not found")
		}
		return nil, fmt.Errorf("load action: %w", err)
	}
	if runID.Valid {
		v := runID.Int64
		act.RunID = &v
	}
	act.ToolUseID = toolUseID.String
	act.Params = json.RawMessage(params)
	if approvedParams.Valid {
		v := json.RawMessage(approvedParams.String)
		act.ApprovedParams = &v
	}
	if closedAt.Valid {
		v := closedAt.Time
		act.IssueClosedAt = &v
	}
	setActionDecisionState(&act)
	return &act, nil
}

// ListPendingActions returns the admin approval queue (status='proposed'),
// joined to each action's issue for the list view (title + media type + category
// + the agent's rationale + params). Newest first.
func (s *Service) ListPendingActions() ([]AgentAction, error) {
	return s.listActions(ActionProposed)
}

// ListActions returns actions filtered by status (empty = all), for the admin
// surface and tests.
func (s *Service) ListActions(status string) ([]AgentAction, error) {
	return s.listActions(status)
}

func (s *Service) listActions(status string) ([]AgentAction, error) {
	if status == "all" {
		status = ""
	}
	query := `SELECT a.id, a.issue_id, a.run_id, a.kind, a.params, a.approved_params, a.rationale, a.risk, a.status,
	                 a.decided_by, a.decided_at, a.deny_reason, a.executed_at, a.result_text, a.created_at,
	                 i.title, i.media_type, i.category, i.status, i.closed_at,
	                 COALESCE(i.instance_id, ''), COALESCE(si.name, ''), COALESCE(si.service_type, ''),
	                 EXISTS (SELECT 1 FROM agent_runs r WHERE r.id = a.run_id AND r.status = 'waiting_approval')
	          FROM agent_actions a JOIN issues i ON i.id = a.issue_id
	          LEFT JOIN service_instances si ON si.id = i.instance_id`
	var (
		rows *sql.Rows
		err  error
	)
	if status != "" {
		if status == ActionProposed {
			rows, err = s.db.Query(query+` WHERE a.status = ? AND i.closed_at IS NULL AND i.status = ?
			 AND EXISTS (SELECT 1 FROM agent_runs r WHERE r.id = a.run_id AND r.status = 'waiting_approval')
			 ORDER BY a.id DESC`, status, IssueAwaitingApproval)
		} else {
			rows, err = s.db.Query(query+" WHERE a.status = ? ORDER BY a.id DESC", status)
		}
	} else {
		rows, err = s.db.Query(query + " ORDER BY a.id DESC")
	}
	if err != nil {
		return nil, fmt.Errorf("query actions: %w", err)
	}
	defer rows.Close()

	out := []AgentAction{}
	for rows.Next() {
		act, err := scanAction(rows)
		if err != nil {
			return nil, fmt.Errorf("scan action: %w", err)
		}
		out = append(out, *act)
	}
	return out, rows.Err()
}

// GetIssueActivity returns every action and run linked to one issue, including
// terminal history. It is the permanent audit surface; queue endpoints are only
// operational views.
func (s *Service) GetIssueActivity(issueID int64) (*IssueActivity, error) {
	if _, err := s.GetIssue(issueID); err != nil {
		return nil, err
	}
	query := `SELECT a.id, a.issue_id, a.run_id, a.kind, a.params, a.approved_params, a.rationale, a.risk, a.status,
	                 a.decided_by, a.decided_at, a.deny_reason, a.executed_at, a.result_text, a.created_at,
	                 i.title, i.media_type, i.category, i.status, i.closed_at,
	                 COALESCE(i.instance_id, ''), COALESCE(si.name, ''), COALESCE(si.service_type, ''),
	                 EXISTS (SELECT 1 FROM agent_runs r WHERE r.id = a.run_id AND r.status = 'waiting_approval')
	          FROM agent_actions a JOIN issues i ON i.id = a.issue_id
	          LEFT JOIN service_instances si ON si.id = i.instance_id
	          WHERE a.issue_id = ? ORDER BY a.id DESC`
	rows, err := s.db.Query(query, issueID)
	if err != nil {
		return nil, fmt.Errorf("query issue actions: %w", err)
	}
	actions := []AgentAction{}
	for rows.Next() {
		act, scanErr := scanAction(rows)
		if scanErr != nil {
			rows.Close()
			return nil, fmt.Errorf("scan issue action: %w", scanErr)
		}
		actions = append(actions, *act)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	runRows, err := s.db.Query(
		`SELECT id, issue_id, trigger, status, model, step_count,
		 input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
		 stop_reason, started_at, finished_at
		 FROM agent_runs WHERE issue_id = ? ORDER BY id DESC`, issueID,
	)
	if err != nil {
		return nil, fmt.Errorf("query issue runs: %w", err)
	}
	runs := []AgentRun{}
	for runRows.Next() {
		var run AgentRun
		var stop sql.NullString
		var finished sql.NullTime
		if err := runRows.Scan(&run.ID, &run.IssueID, &run.Trigger, &run.Status, &run.Model, &run.StepCount,
			&run.InputTokens, &run.OutputTokens, &run.CacheCreationTokens, &run.CacheReadTokens,
			&stop, &run.StartedAt, &finished); err != nil {
			runRows.Close()
			return nil, fmt.Errorf("scan issue run: %w", err)
		}
		if stop.Valid {
			v := stop.String
			run.StopReason = &v
		}
		if finished.Valid {
			v := finished.Time
			run.FinishedAt = &v
		}
		runs = append(runs, run)
	}
	if err := runRows.Close(); err != nil {
		return nil, err
	}
	return &IssueActivity{Actions: actions, Runs: runs}, nil
}

// GetAction loads one action (with its issue join) for the API result.
func (s *Service) GetAction(actionID int64) (*AgentAction, error) {
	row := s.db.QueryRow(
		`SELECT a.id, a.issue_id, a.run_id, a.kind, a.params, a.approved_params, a.rationale, a.risk, a.status,
		        a.decided_by, a.decided_at, a.deny_reason, a.executed_at, a.result_text, a.created_at,
		        i.title, i.media_type, i.category, i.status, i.closed_at,
		        COALESCE(i.instance_id, ''), COALESCE(si.name, ''), COALESCE(si.service_type, ''),
		        EXISTS (SELECT 1 FROM agent_runs r WHERE r.id = a.run_id AND r.status = 'waiting_approval')
		 FROM agent_actions a JOIN issues i ON i.id = a.issue_id
		 LEFT JOIN service_instances si ON si.id = i.instance_id
		 WHERE a.id = ?`,
		actionID,
	)
	act, err := scanAction(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("action not found")
	}
	if err != nil {
		return nil, fmt.Errorf("load action: %w", err)
	}
	return act, nil
}

// PendingActionCount counts proposals awaiting a decision (the approval badge).
func (s *Service) PendingActionCount() (int, error) {
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM agent_actions a JOIN issues i ON i.id = a.issue_id
		 WHERE a.status = ? AND i.closed_at IS NULL AND i.status = ?
		   AND EXISTS (SELECT 1 FROM agent_runs r WHERE r.id = a.run_id AND r.status = 'waiting_approval')`,
		ActionProposed, IssueAwaitingApproval,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("count pending actions: %w", err)
	}
	return n, nil
}

// scanAction reads one agent_actions row joined to its issue.
func scanAction(row rowScanner) (*AgentAction, error) {
	var (
		act            AgentAction
		runID          sql.NullInt64
		params         string
		approvedParams sql.NullString
		decidedBy      sql.NullInt64
		decidedAt      sql.NullTime
		denyReason     sql.NullString
		executedAt     sql.NullTime
		resultText     sql.NullString
		category       sql.NullString
		issueClosedAt  sql.NullTime
	)
	if err := row.Scan(
		&act.ID, &act.IssueID, &runID, &act.Kind, &params, &approvedParams, &act.Rationale, &act.Risk, &act.Status,
		&decidedBy, &decidedAt, &denyReason, &executedAt, &resultText, &act.CreatedAt,
		&act.IssueTitle, &act.IssueMediaType, &category, &act.IssueStatus, &issueClosedAt,
		&act.InstanceID, &act.InstanceName, &act.InstanceServiceType, &act.GateValid,
	); err != nil {
		return nil, err
	}
	act.Params = json.RawMessage(params)
	if approvedParams.Valid {
		v := json.RawMessage(approvedParams.String)
		act.ApprovedParams = &v
	}
	if runID.Valid {
		v := runID.Int64
		act.RunID = &v
	}
	if decidedBy.Valid {
		v := decidedBy.Int64
		act.DecidedBy = &v
	}
	if decidedAt.Valid {
		v := decidedAt.Time
		act.DecidedAt = &v
	}
	if denyReason.Valid && denyReason.String != "" {
		v := denyReason.String
		act.DenyReason = &v
	}
	if executedAt.Valid {
		v := executedAt.Time
		act.ExecutedAt = &v
	}
	if resultText.Valid && resultText.String != "" {
		v := resultText.String
		act.ResultText = &v
	}
	if category.Valid && category.String != "" {
		v := category.String
		act.IssueCategory = &v
	}
	if issueClosedAt.Valid {
		v := issueClosedAt.Time
		act.IssueClosedAt = &v
	}
	setActionDecisionState(&act)
	return &act, nil
}

func setActionDecisionState(act *AgentAction) {
	act.CanDecide = act.Status == ActionProposed && act.IssueClosedAt == nil &&
		act.IssueStatus == IssueAwaitingApproval && act.GateValid
	if act.CanDecide {
		act.BlockedReason = ""
		return
	}
	switch {
	case act.Status == ActionSuperseded:
		act.BlockedReason = "This proposal was superseded and cannot be executed."
	case act.IssueClosedAt != nil || isTerminalStatus(act.IssueStatus):
		act.BlockedReason = "The issue is already closed, so this proposal cannot be executed."
	case act.Status != ActionProposed:
		act.BlockedReason = "This proposal has already been decided."
	default:
		act.BlockedReason = "This proposal cannot be decided."
	}
}

// GetRunDetail returns one agent run plus its ordered audit steps (the run audit
// view). It powers GET /api/admin/agent-runs/{id}.
func (s *Service) GetRunDetail(runID int64) (*AgentRunDetail, error) {
	var (
		run        AgentRun
		stopReason sql.NullString
		finishedAt sql.NullTime
	)
	err := s.db.QueryRow(
		`SELECT id, issue_id, trigger, status, model, step_count,
		        input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
		        stop_reason, started_at, finished_at
		 FROM agent_runs WHERE id = ?`,
		runID,
	).Scan(
		&run.ID, &run.IssueID, &run.Trigger, &run.Status, &run.Model, &run.StepCount,
		&run.InputTokens, &run.OutputTokens, &run.CacheCreationTokens, &run.CacheReadTokens,
		&stopReason, &run.StartedAt, &finishedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("run not found")
	}
	if err != nil {
		return nil, fmt.Errorf("load run: %w", err)
	}
	if stopReason.Valid {
		v := stopReason.String
		run.StopReason = &v
	}
	if finishedAt.Valid {
		v := finishedAt.Time
		run.FinishedAt = &v
	}

	steps, err := s.runSteps(runID)
	if err != nil {
		return nil, err
	}
	return &AgentRunDetail{Run: run, Steps: steps}, nil
}

func (s *Service) runSteps(runID int64) ([]AgentStep, error) {
	rows, err := s.db.Query(
		`SELECT id, seq, kind, tool_name, tool_input, tool_output, text, is_error, created_at
		 FROM agent_steps WHERE run_id = ? ORDER BY seq ASC`,
		runID,
	)
	if err != nil {
		return nil, fmt.Errorf("query run steps: %w", err)
	}
	defer rows.Close()

	out := []AgentStep{}
	for rows.Next() {
		var (
			st         AgentStep
			toolName   sql.NullString
			toolInput  sql.NullString
			toolOutput sql.NullString
			text       sql.NullString
		)
		if err := rows.Scan(&st.ID, &st.Seq, &st.Kind, &toolName, &toolInput, &toolOutput, &text, &st.IsError, &st.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan run step: %w", err)
		}
		if toolName.Valid {
			v := toolName.String
			st.ToolName = &v
		}
		if toolInput.Valid {
			v := toolInput.String
			st.ToolInput = &v
		}
		if toolOutput.Valid {
			v := toolOutput.String
			st.ToolOutput = &v
		}
		if text.Valid {
			v := text.String
			st.Text = &v
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// notifyActionsChanged invalidates both the issue aggregate and approval queue.
// It carries the authoritative pending count so badges cannot remain stale when
// external resolution supersedes a proposal.
func (s *Service) notifyActionsChanged(issueID int64, status string) {
	if s.notifier == nil {
		return
	}
	data := map[string]interface{}{
		"issue_id": issueID,
		"status":   status,
	}
	if count, err := s.PendingActionCount(); err == nil {
		data["pending_count"] = count
	}
	s.notifier.NotifyAdmins("agent_action_decided", data)
	s.pingIssueUpdated(issueID)
}
