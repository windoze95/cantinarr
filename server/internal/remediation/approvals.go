package remediation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"

	"github.com/windoze95/cantinarr-server/internal/ai"
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
// The model is entirely out of the loop here: ApproveAction replays the STORED,
// canonical params verbatim via the Executor. The only model-facing effect is the
// resume tool_result appended to the transcript so the investigation can continue.

// ApproveAction approves a proposed action and executes it exactly once. An
// optional override replaces the stored params (admin edit) after re-validation.
// On success the action is marked executed; on any execution error it is marked
// failed (never reverted to proposed — that preserves at-most-once). Either way
// the decision outcome is appended to the run transcript and the Runner is
// enqueued to resume so the agent can verify (on success) or try another tack.
func (s *Service) ApproveAction(adminID, actionID int64, override *json.RawMessage) (*AgentAction, error) {
	act, err := s.loadActionForDecision(actionID)
	if err != nil {
		return nil, err
	}
	if act.Status != ActionProposed {
		return nil, fmt.Errorf("action is not awaiting a decision")
	}

	// An admin may edit the proposal before approving. Re-validate against the
	// kind's schema and persist the canonical override as the params that will be
	// replayed (and re-fingerprint so the stored fingerprint stays consistent).
	paramsToRun := act.Params
	if override != nil && len(*override) > 0 && string(*override) != "null" {
		canonical, verr := validateActionParams(ActionKind(act.Kind), *override)
		if verr != nil {
			return nil, fmt.Errorf("invalid override: %w", verr)
		}
		newFP := fingerprint(act.IssueID, ActionKind(act.Kind), canonical)
		if _, err := s.db.Exec(
			"UPDATE agent_actions SET params = ?, fingerprint = ? WHERE id = ? AND status = ?",
			string(canonical), newFP, actionID, ActionProposed,
		); err != nil {
			return nil, fmt.Errorf("apply override: %w", err)
		}
		paramsToRun = canonical
	}

	// CAS proposed -> executing. This is the at-most-once gate: exactly one caller
	// wins; a lost race returns without executing.
	cas, err := s.db.Exec(
		"UPDATE agent_actions SET status = ? WHERE id = ? AND status = ?",
		ActionExecuting, actionID, ActionProposed,
	)
	if err != nil {
		return nil, fmt.Errorf("claim action for execution: %w", err)
	}
	if n, _ := cas.RowsAffected(); n == 0 {
		// Another decision won the race (or the row moved on); do not execute.
		return s.GetAction(actionID)
	}

	// Replay the approved action against the arr. This is the ONLY mutation path.
	resultText, execErr := s.executor.Execute(context.Background(), act.IssueID, ActionKind(act.Kind), paramsToRun)

	resumeText := ""
	finalStatus := ActionExecuted
	finalResult := resultText
	if execErr != nil {
		// A failed execution stays failed (no silent re-exec): the row never goes
		// back to proposed once it was claimed for execution.
		finalStatus = ActionFailed
		finalResult = "Execution failed: " + execErr.Error()
		resumeText = "Admin approved, but the fix failed to execute: " + execErr.Error()
	} else {
		resumeText = "Approved and executed: " + resultText
	}
	if _, err := s.db.Exec(
		"UPDATE agent_actions SET status = ?, decided_by = ?, decided_at = CURRENT_TIMESTAMP, executed_at = CURRENT_TIMESTAMP, result_text = ? WHERE id = ?",
		finalStatus, adminID, finalResult, actionID,
	); err != nil {
		// The mutation already ran (or definitively failed); we MUST record the
		// terminal status so the row never sits in 'executing' and gets re-claimed.
		log.Printf("remediation: finalize action %d as %s: %v", actionID, finalStatus, err)
	}

	// Feed the outcome back into the run transcript (keyed to the proposal's
	// tool_use_id) + the audit ledger, then enqueue the resume.
	s.appendResumeResult(act, resumeText)
	s.notifyActionDecided(act.IssueID, act.Kind, "approved")
	s.EnqueueResume(act.IssueID)

	return s.GetAction(actionID)
}

// DenyAction denies a proposed action and resumes the investigation so the agent
// can try a different approach within its remaining budget. A denial returns the
// issue to investigating (via the resume), NOT to a terminal state — that is what
// enables the multi-step "propose → denied → try something else" loop.
func (s *Service) DenyAction(adminID, actionID int64, note string) (*AgentAction, error) {
	act, err := s.loadActionForDecision(actionID)
	if err != nil {
		return nil, err
	}
	if act.Status != ActionProposed {
		return nil, fmt.Errorf("action is not awaiting a decision")
	}

	// Guarded transition proposed -> denied (idempotent under a concurrent decide).
	cas, err := s.db.Exec(
		"UPDATE agent_actions SET status = ?, decided_by = ?, decided_at = CURRENT_TIMESTAMP, deny_reason = ? WHERE id = ? AND status = ?",
		ActionDenied, adminID, sqlNullStr(note), actionID, ActionProposed,
	)
	if err != nil {
		return nil, fmt.Errorf("deny action: %w", err)
	}
	if n, _ := cas.RowsAffected(); n == 0 {
		return s.GetAction(actionID)
	}

	denyMsg := "Admin denied"
	if note != "" {
		denyMsg += ": " + note
	}
	s.appendResumeResult(act, denyMsg)
	s.notifyActionDecided(act.IssueID, act.Kind, "denied")
	s.EnqueueResume(act.IssueID)

	return s.GetAction(actionID)
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
func (s *Service) appendResumeResult(act *AgentAction, outcome string) {
	if act.RunID == nil || act.ToolUseID == "" {
		return
	}
	runID := *act.RunID

	var transcriptJSON string
	if err := s.db.QueryRow("SELECT transcript_json FROM agent_runs WHERE id = ?", runID).Scan(&transcriptJSON); err != nil {
		log.Printf("remediation: load transcript for resume (run %d): %v", runID, err)
		return
	}
	var history ai.Transcript
	if transcriptJSON != "" {
		if err := json.Unmarshal([]byte(transcriptJSON), &history); err != nil {
			log.Printf("remediation: decode transcript for resume (run %d): %v", runID, err)
			return
		}
	}

	// Replace the placeholder tool_result for this propose_action tool_use_id with
	// the decision outcome. If (defensively) no such block exists, append one so
	// the resume still has a result to react to.
	if !replaceToolResult(history, act.ToolUseID, outcome) {
		history = append(history, ai.TranscriptMessage{
			Role: ai.RoleUser,
			Content: []ai.TranscriptBlock{{
				Type:      ai.BlockToolResult,
				ToolUseID: act.ToolUseID,
				Name:      "propose_action",
				Content:   outcome,
			}},
		})
	}
	if data, err := json.Marshal(history); err == nil {
		s.db.Exec("UPDATE agent_runs SET transcript_json = ? WHERE id = ?", string(data), runID)
	}

	// Mirror to the audit ledger as the next step (the human-readable ledger is
	// append-only and not used to rehydrate the transcript, so a separate row here
	// is correct and does not affect provider validity).
	var nextSeq int
	s.db.QueryRow("SELECT COALESCE(MAX(seq),0)+1 FROM agent_steps WHERE run_id = ?", runID).Scan(&nextSeq)
	s.db.Exec(
		`INSERT INTO agent_steps (run_id, issue_id, seq, kind, tool_name, tool_use_id, tool_output)
		 VALUES (?, ?, ?, 'tool_result', 'propose_action', ?, ?)`,
		runID, act.IssueID, nextSeq, act.ToolUseID, outcome,
	)
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

// resumeOnReporterReply is the W4 counterpart to appendResumeResult: it feeds a
// reporter/admin reply back into a run parked on ask_reporter as that tool's
// tool_result and enqueues a resume. It REPLACES the placeholder ask_reporter
// tool_result in place (keyed to the ask_reporter tool_use_id recovered from the
// parked transcript) so the transcript stays well-formed — exactly one
// tool_result per tool_use, no two consecutive user turns (which a real provider
// rejects). Best-effort: if no parked run or ask tool_use can be found it only
// logs (the message is still threaded) rather than crashing. Called from
// PostReply when an awaiting_user issue receives a reporter/admin reply.
func (s *Service) resumeOnReporterReply(issueID int64, reply string) {
	runID, history, ok := s.loadAwaitingUserTranscript(issueID)
	if !ok {
		return
	}
	askToolUseID, found := findAskReporterToolUse(history)
	if !found {
		log.Printf("remediation: no ask_reporter tool_use to resume on issue %d (run %d)", issueID, runID)
		return
	}

	// The reporter's reply is UNTRUSTED data fenced into the tool_result the model
	// reads; it is never interpreted as an instruction. Prefix it so the agent
	// treats it as the reporter's answer to its question.
	outcome := "The reporter replied: " + reply
	if !replaceToolResult(history, askToolUseID, outcome) {
		// Defensive: the placeholder should exist (the Runner persisted it on park),
		// but if not, append one so the resume still has a result to react to.
		history = append(history, ai.TranscriptMessage{
			Role: ai.RoleUser,
			Content: []ai.TranscriptBlock{{
				Type:      ai.BlockToolResult,
				ToolUseID: askToolUseID,
				Name:      askReporterToolName,
				Content:   outcome,
			}},
		})
	}
	if data, err := json.Marshal(history); err == nil {
		s.db.Exec("UPDATE agent_runs SET transcript_json = ? WHERE id = ?", string(data), runID)
	}

	// Mirror to the audit ledger (append-only; not used to rehydrate the transcript).
	var nextSeq int
	s.db.QueryRow("SELECT COALESCE(MAX(seq),0)+1 FROM agent_steps WHERE run_id = ?", runID).Scan(&nextSeq)
	s.db.Exec(
		`INSERT INTO agent_steps (run_id, issue_id, seq, kind, tool_name, tool_use_id, tool_output)
		 VALUES (?, ?, ?, 'tool_result', ?, ?, ?)`,
		runID, issueID, nextSeq, askReporterToolName, askToolUseID, outcome,
	)

	s.EnqueueResume(issueID)
}

// loadAwaitingUserTranscript loads the most recent run for an issue parked
// waiting for a reporter reply (waiting_user) and decodes its transcript.
func (s *Service) loadAwaitingUserTranscript(issueID int64) (runID int64, history ai.Transcript, ok bool) {
	var transcriptJSON string
	err := s.db.QueryRow(
		"SELECT id, transcript_json FROM agent_runs WHERE issue_id = ? AND status = ? ORDER BY id DESC LIMIT 1",
		issueID, runStatusWaitingUser,
	).Scan(&runID, &transcriptJSON)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("remediation: load awaiting_user run for issue %d: %v", issueID, err)
		}
		return 0, nil, false
	}
	if transcriptJSON != "" {
		if err := json.Unmarshal([]byte(transcriptJSON), &history); err != nil {
			log.Printf("remediation: decode awaiting_user transcript (run %d): %v", runID, err)
			return 0, nil, false
		}
	}
	return runID, history, true
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
		`SELECT id, issue_id, run_id, tool_use_id, kind, params, rationale, risk, status
		 FROM agent_actions WHERE id = ?`,
		actionID,
	)
	var (
		act       AgentAction
		runID     sql.NullInt64
		toolUseID sql.NullString
		params    string
	)
	if err := row.Scan(&act.ID, &act.IssueID, &runID, &toolUseID, &act.Kind, &params, &act.Rationale, &act.Risk, &act.Status); err != nil {
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
	query := `SELECT a.id, a.issue_id, a.run_id, a.kind, a.params, a.rationale, a.risk, a.status,
	                 a.decided_by, a.decided_at, a.deny_reason, a.executed_at, a.result_text, a.created_at,
	                 i.title, i.media_type, i.category
	          FROM agent_actions a JOIN issues i ON i.id = a.issue_id`
	var (
		rows *sql.Rows
		err  error
	)
	if status != "" {
		rows, err = s.db.Query(query+" WHERE a.status = ? ORDER BY a.id DESC", status)
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

// GetAction loads one action (with its issue join) for the API result.
func (s *Service) GetAction(actionID int64) (*AgentAction, error) {
	row := s.db.QueryRow(
		`SELECT a.id, a.issue_id, a.run_id, a.kind, a.params, a.rationale, a.risk, a.status,
		        a.decided_by, a.decided_at, a.deny_reason, a.executed_at, a.result_text, a.created_at,
		        i.title, i.media_type, i.category
		 FROM agent_actions a JOIN issues i ON i.id = a.issue_id
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
	if err := s.db.QueryRow("SELECT COUNT(*) FROM agent_actions WHERE status = ?", ActionProposed).Scan(&n); err != nil {
		return 0, fmt.Errorf("count pending actions: %w", err)
	}
	return n, nil
}

// scanAction reads one agent_actions row joined to its issue.
func scanAction(row rowScanner) (*AgentAction, error) {
	var (
		act        AgentAction
		runID      sql.NullInt64
		params     string
		decidedBy  sql.NullInt64
		decidedAt  sql.NullTime
		denyReason sql.NullString
		executedAt sql.NullTime
		resultText sql.NullString
		category   sql.NullString
	)
	if err := row.Scan(
		&act.ID, &act.IssueID, &runID, &act.Kind, &params, &act.Rationale, &act.Risk, &act.Status,
		&decidedBy, &decidedAt, &denyReason, &executedAt, &resultText, &act.CreatedAt,
		&act.IssueTitle, &act.IssueMediaType, &category,
	); err != nil {
		return nil, err
	}
	act.Params = json.RawMessage(params)
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
	return &act, nil
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
		        cost_micros, stop_reason, started_at, finished_at
		 FROM agent_runs WHERE id = ?`,
		runID,
	).Scan(
		&run.ID, &run.IssueID, &run.Trigger, &run.Status, &run.Model, &run.StepCount,
		&run.InputTokens, &run.OutputTokens, &run.CacheCreationTokens, &run.CacheReadTokens,
		&run.CostMicros, &stopReason, &run.StartedAt, &finishedAt,
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

// notifyActionDecided fires the agent_action_decided admin event after an
// approve/deny. Fixed-template body on the push side; only ids + kind + decision
// travel (never the untrusted rationale/result text).
func (s *Service) notifyActionDecided(issueID int64, kind, decision string) {
	if s.notifier == nil {
		return
	}
	s.notifier.NotifyAdmins("agent_action_decided", map[string]interface{}{
		"issue_id": issueID,
		"kind":     kind,
		"decision": decision,
	})
}
