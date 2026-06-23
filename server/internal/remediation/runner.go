package remediation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/mcp"
)

// Compile-time assertion: *Service implements the mcp.IssueStore write surface
// the agent-only tools call. Kept here because this file already imports mcp.
var _ mcp.IssueStore = (*Service)(nil)

// readToolAllowList is the EXACT, hardcoded set of read-only arr tools the
// remediation agent may use. THIS LIST — not an RBAC role — is the enforcement
// boundary. The Runner passes only these tool definitions (plus the two
// agent-only tools) to the model, AND its dispatch refuses any tool name not in
// this set before ever calling ExecuteTool. Every one of these is read-only;
// no mutating tool (grab_release, remove_queue_item, remediate_queue_item,
// execute_manual_import, rescan_media, trigger_search) is reachable by any path.
var readToolAllowList = []string{
	"diagnose_queue",
	"get_manual_import_candidates",
	"search_releases",
	"get_queue",
	"get_history",
	"get_library",
	"get_arr_health",
}

// readToolAllowSet is readToolAllowList as a set, for O(1) dispatch checks.
var readToolAllowSet = func() map[string]bool {
	m := make(map[string]bool, len(readToolAllowList))
	for _, n := range readToolAllowList {
		m[n] = true
	}
	return m
}()

// isReadToolAllowed reports whether name is a permitted read-only tool.
func isReadToolAllowed(name string) bool { return readToolAllowSet[name] }

// stepKind constants for the agent_steps audit ledger.
const (
	stepAssistant  = "assistant"
	stepToolCall   = "tool_call"
	stepToolResult = "tool_result"
	stepGiveup     = "giveup"
)

// agent_runs.status / stop_reason vocab used by the Runner.
const (
	runStatusRunning         = "running"
	runStatusSucceeded       = "succeeded"
	runStatusGaveUp          = "gave_up"
	runStatusWaitingApproval = "waiting_approval"

	stopResolved         = "resolved"
	stopMaxSteps         = "max_steps"
	stopTimeout          = "timeout"
	stopMaxCost          = "max_cost"
	stopModelError       = "model_error"
	stopNoDiagnosis      = "no_diagnosis"
	stopAwaitingApproval = "awaiting_approval"
	stepTruncateBytes    = 4000
)

// turnRunnerFactory builds an ai.TurnRunner for a provider/model/key. It is a
// field so tests can inject a fake provider without real network/credentials.
// The production factory (set in NewRunner) closes over the concrete ToolServer.
type turnRunnerFactory func(provider, apiKey, model string) (ai.TurnRunner, error)

// toolHost is the narrow tool-execution surface the Runner depends on. *mcp.ToolServer
// satisfies it; a fake satisfies it in tests so the enforcement boundary (which
// tools are offered, and that ExecuteTool is never reached for a non-allow-listed
// name) can be asserted directly. This is deliberately the ONLY way the Runner
// touches tools — it never holds anything that could mutate the arr.
type toolHost interface {
	// ToolsByName materializes named tool definitions (the read allow-list).
	ToolsByName(names []string) []mcp.Tool
	// ExecuteTool runs a read tool (called ONLY after the name clears the allow-list).
	ExecuteTool(ctx context.Context, name string, input json.RawMessage, callCtx mcp.CallContext) (*mcp.ToolResult, error)
	// ExecuteAgentTool runs an agent-only tool (writes issue rows, never arr). The
	// toolUseID is the model's tool_use.id, stored on a proposal so the resume
	// tool_result pairs back correctly.
	ExecuteAgentTool(ctx context.Context, name string, input json.RawMessage, issueID int64, toolUseID string) (*mcp.AgentToolResult, error)
}

// Runner drives the READ-ONLY investigation of a single issue and is the
// enforcement boundary that makes mutation architecturally impossible. It owns
// the outer loop (CAS claim -> seed/rehydrate transcript -> call the model one
// turn at a time -> dispatch tool calls through the read-tool allow-list ->
// persist audit + transcript -> check bounds -> terminate), all in Go and never
// trusted to the model. A single Runner is shared across worker goroutines; it
// holds no per-run mutable state (the TurnRunner is built per Run invocation and
// threaded through as a parameter).
type Runner struct {
	db         *sql.DB
	svc        *Service
	toolServer toolHost
	creds      *credentials.Registry
	newTurn    turnRunnerFactory
	procToken  string
}

// NewRunner constructs the remediation Runner. creds resolves the AI
// provider/model/key (remediation Settings may override provider/model; empty
// means inherit the server's configured AI). procToken is a process-start token
// stamped on agent_runs so a watchdog can tell crashed-mid-run from parked.
func NewRunner(db *sql.DB, svc *Service, toolServer *mcp.ToolServer, creds *credentials.Registry, procToken string) *Runner {
	return &Runner{
		db:         db,
		svc:        svc,
		toolServer: toolServer,
		creds:      creds,
		procToken:  procToken,
		// Production factory: build a real provider TurnRunner against the concrete
		// tool server (which the provider services use to convert tool defs).
		newTurn: func(provider, apiKey, model string) (ai.TurnRunner, error) {
			return ai.NewTurnRunner(provider, apiKey, model, toolServer)
		},
	}
}

// Run investigates one issue end to end (read-only). It is safe to call from a
// worker goroutine; the CAS claim guarantees at most one concurrent run per
// issue. It returns nil on a normal terminal outcome (resolved, gave up, or
// already claimed); an error only signals an unexpected infrastructure failure.
func (r *Runner) Run(ctx context.Context, issueID int64) error {
	settings := r.svc.Settings()
	if !settings.Enabled {
		return nil // feature off; nothing to do.
	}

	// Daily run cap (a coarse global guardrail; counts runs started today).
	if over, err := r.dailyRunCapExceeded(settings.DailyRunCap); err == nil && over {
		log.Printf("remediation: daily run cap (%d) reached; skipping issue %d", settings.DailyRunCap, issueID)
		return nil
	}
	// Global daily-cost ceiling.
	if over, err := r.dailyCostCeilingExceeded(int64(settings.DailyCostCeilingMicros)); err == nil && over {
		log.Printf("remediation: daily cost ceiling reached; skipping issue %d", issueID)
		return nil
	}

	issue, err := r.svc.GetIssue(issueID)
	if err != nil {
		return fmt.Errorf("load issue %d: %w", issueID, err)
	}
	if isTerminalStatus(issue.Status) {
		return nil // already closed.
	}
	// A parked issue (a proposal awaiting approval, or a reporter question) is
	// owned by the resume path, not a fresh investigation: Run must never start a
	// second run over a pending proposal. Resume re-enters those.
	if issue.Status == IssueAwaitingApproval || issue.Status == IssueAwaitingUser {
		return nil
	}

	turn, model, err := r.resolveTurn(settings)
	if err != nil {
		// No key / provider setup failed: cannot run. Park the issue with a clear
		// admin-facing note.
		return r.giveUp(ctx, issueID, 0, model, stopModelError,
			"I couldn't investigate this automatically because the AI provider isn't configured. Flagging for an admin.")
	}

	// CAS-claim the issue and create the run row.
	runID, claimed, err := r.claim(issueID, model)
	if err != nil {
		return fmt.Errorf("claim issue %d: %w", issueID, err)
	}
	if !claimed {
		return nil // another worker won the race.
	}

	// Bound active wall-clock with a context timeout.
	wall := time.Duration(settings.MaxWallClockSecs) * time.Second
	runCtx, cancel := context.WithTimeout(ctx, wall)
	defer cancel()

	st := &loopState{
		runID:     runID,
		costKnown: true,
		history: ai.Transcript{ai.TranscriptMessage{
			Role:    ai.RoleUser,
			Content: []ai.TranscriptBlock{{Type: ai.BlockText, Text: initialUserTurn(issue)}},
		}},
	}
	if err := r.loop(runCtx, turn, issue, st, model, settings); err != nil {
		return err
	}
	return nil
}

// resolveTurn resolves the provider/model/key from remediation settings (empty
// inherits the server's configured AI, mirroring ai/handler.go) and builds the
// single-turn runner. It returns the model id alongside so callers can record it
// on a give-up even when no run row was created. An error means the AI is
// unconfigured or the provider failed to construct.
func (r *Runner) resolveTurn(settings Settings) (ai.TurnRunner, string, error) {
	cfg := r.creds.GetAIConfig()
	provider := settings.Provider
	if provider == "" {
		provider = cfg.Provider
	}
	model := settings.Model
	if model == "" {
		if settings.Provider != "" {
			model = credentials.DefaultAIModel(provider)
		} else {
			model = cfg.Model
		}
	}
	apiKey := r.creds.GetCredential(credentials.AIKeyCredentialKey(provider))
	if apiKey == "" {
		return nil, model, fmt.Errorf("AI provider not configured")
	}
	turn, err := r.newTurn(provider, apiKey, model)
	if err != nil {
		return nil, model, fmt.Errorf("build turn runner: %w", err)
	}
	return turn, model, nil
}

// Resume re-enters the SAME parked run after an admin decision (approve/deny) has
// appended the decision tool_result to the run's transcript. It re-claims the
// issue, rehydrates the untruncated transcript and the bounds spent so far
// (step_count/cost_micros are NOT reset), and re-enters the loop with the
// remaining budget. Approval → the agent verifies (read-only) and concludes or
// proposes again; denial → it tries another tack, still bounded. Safe to call
// from a worker goroutine; the CAS re-claim guards against a double-resume.
func (r *Runner) Resume(ctx context.Context, issueID int64) error {
	settings := r.svc.Settings()
	if !settings.Enabled {
		return nil // feature off; leave the issue parked for an admin.
	}

	issue, err := r.svc.GetIssue(issueID)
	if err != nil {
		return fmt.Errorf("load issue %d: %w", issueID, err)
	}
	if isTerminalStatus(issue.Status) {
		return nil // already closed (e.g. admin dismissed while parked).
	}

	// Find the parked run for this issue: the most recent run left waiting for an
	// approval. If there is none, there is nothing to resume.
	runID, prevSteps, prevCost, transcriptJSON, ok, err := r.loadParkedRun(issueID)
	if err != nil {
		return fmt.Errorf("load parked run for issue %d: %w", issueID, err)
	}
	if !ok {
		return nil
	}

	turn, model, err := r.resolveTurn(settings)
	if err != nil {
		return r.giveUp(ctx, issueID, runID, model, stopModelError,
			"I couldn't continue after the decision because the AI provider isn't configured. Flagging for an admin.")
	}

	// Re-claim the issue (status->investigating, active_run_id->this run) only if
	// it isn't already claimed. Zero rows = another worker is already resuming, or
	// the issue moved on; bail without disturbing it.
	cas, err := r.db.Exec(
		"UPDATE issues SET status = ?, active_run_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND active_run_id IS NULL AND closed_at IS NULL",
		IssueInvestigating, runID, issueID,
	)
	if err != nil {
		return fmt.Errorf("reclaim issue %d: %w", issueID, err)
	}
	if n, _ := cas.RowsAffected(); n == 0 {
		return nil
	}
	// Flip the run back to running so the watchdog and audit reflect live work.
	r.db.Exec("UPDATE agent_runs SET status = ?, stop_reason = NULL WHERE id = ?", runStatusRunning, runID)

	history, err := rehydrateTranscript(transcriptJSON)
	if err != nil || len(history) == 0 {
		// A corrupt/empty transcript can't be safely continued; give up cleanly
		// rather than re-seeding (which would lose the proposal context).
		return r.giveUp(ctx, issueID, runID, model, stopModelError,
			giveUpMessage(issue, true))
	}

	wall := time.Duration(settings.MaxWallClockSecs) * time.Second
	runCtx, cancel := context.WithTimeout(ctx, wall)
	defer cancel()

	st := &loopState{
		runID:        runID,
		history:      history,
		seq:          r.nextSeq(runID),
		stepCount:    prevSteps,
		costAccum:    prevCost,
		costKnown:    true,
		postedAnyMsg: r.hasAgentMessage(issueID),
	}
	return r.loop(runCtx, turn, issue, st, model, settings)
}

// loadParkedRun returns the most recent run for an issue that is parked waiting
// for an approval, along with the bounds spent so far and its stored transcript.
func (r *Runner) loadParkedRun(issueID int64) (runID int64, stepCount int, costMicros int64, transcriptJSON string, ok bool, err error) {
	row := r.db.QueryRow(
		`SELECT id, step_count, cost_micros, transcript_json
		 FROM agent_runs
		 WHERE issue_id = ? AND status = ?
		 ORDER BY id DESC LIMIT 1`,
		issueID, runStatusWaitingApproval,
	)
	err = row.Scan(&runID, &stepCount, &costMicros, &transcriptJSON)
	if err == sql.ErrNoRows {
		return 0, 0, 0, "", false, nil
	}
	if err != nil {
		return 0, 0, 0, "", false, err
	}
	return runID, stepCount, costMicros, transcriptJSON, true, nil
}

// nextSeq returns the next audit-step sequence number for a run (continuing the
// ledger across a resume).
func (r *Runner) nextSeq(runID int64) int {
	var n int
	r.db.QueryRow("SELECT COALESCE(MAX(seq),0) FROM agent_steps WHERE run_id = ?", runID).Scan(&n)
	return n
}

// hasAgentMessage reports whether any agent-authored message already exists on
// the issue thread (so a resume doesn't re-post a fallback diagnosis).
func (r *Runner) hasAgentMessage(issueID int64) bool {
	var n int
	r.db.QueryRow("SELECT COUNT(*) FROM issue_messages WHERE issue_id = ? AND author_kind = 'agent'", issueID).Scan(&n)
	return n > 0
}

// rehydrateTranscript decodes the untruncated provider-neutral transcript stored
// on a run.
func rehydrateTranscript(transcriptJSON string) (ai.Transcript, error) {
	if strings.TrimSpace(transcriptJSON) == "" {
		return nil, nil
	}
	var history ai.Transcript
	if err := json.Unmarshal([]byte(transcriptJSON), &history); err != nil {
		return nil, err
	}
	return history, nil
}

// loopState is the mutable per-run state the turn loop carries. A fresh Run seeds
// it; Resume rehydrates it from the persisted run so the SAME bounds (step count,
// cost) continue across a human-gated pause — they are never reset.
type loopState struct {
	runID        int64
	history      ai.Transcript
	seq          int   // next audit-step sequence number
	stepCount    int   // total TOOL calls so far (the MaxSteps bound)
	costAccum    int64 // accumulated cost in micros
	costKnown    bool  // false once an unknown-model turn disables the cost check
	postedAnyMsg bool  // whether any agent message has been posted to the thread
}

// loop is the bounded outer turn loop. It repeatedly calls one model turn,
// dispatches every tool_use through the read-tool allow-list, persists audit +
// transcript, checks the Go-enforced bounds, and terminates on conclude_issue, a
// tool-less reply, a tripped bound, or PARKS on propose_action (pausing for an
// admin approval; the loop exits and no goroutine is held during the wait). It
// operates on st so Run and Resume share one implementation.
func (r *Runner) loop(ctx context.Context, turn ai.TurnRunner, issue *Issue, st *loopState, model string, settings Settings) error {
	system := buildSystemPrompt(issue)
	tools := append(r.toolServer.ToolsByName(readToolAllowList), mcp.AgentTools()...)

	for {
		// Bound: max steps (tool calls). Checked before each turn so a run that has
		// already spent its budget gives up instead of taking another turn.
		if st.stepCount >= settings.MaxSteps {
			return r.giveUp(ctx, issue.ID, st.runID, model, stopMaxSteps,
				giveUpMessage(issue, st.postedAnyMsg))
		}
		// Bound: cost ceiling (soft, bounded by one turn of <= MaxTurnTokens).
		if st.costKnown && st.costAccum >= int64(settings.MaxCostMicros) {
			return r.giveUp(ctx, issue.ID, st.runID, model, stopMaxCost,
				giveUpMessage(issue, st.postedAnyMsg))
		}
		// Context deadline (wall clock) — surface as a give-up, not a crash.
		if ctx.Err() != nil {
			return r.giveUp(context.Background(), issue.ID, st.runID, model, stopTimeout,
				giveUpMessage(issue, st.postedAnyMsg))
		}

		res, err := turn.NextTurn(ctx, ai.TurnParams{
			System:    system,
			Tools:     tools,
			History:   st.history,
			MaxTokens: settings.MaxTurnTokens,
		})
		if err != nil {
			if ctx.Err() != nil {
				return r.giveUp(context.Background(), issue.ID, st.runID, model, stopTimeout,
					giveUpMessage(issue, st.postedAnyMsg))
			}
			return r.giveUp(context.Background(), issue.ID, st.runID, model, stopModelError,
				giveUpMessage(issue, st.postedAnyMsg))
		}

		// Accumulate usage/cost onto the run (best-effort). An unknown model means
		// the cost bound can't be enforced — skip the check, never crash.
		turnCost, ok := costMicros(model, res.Usage)
		if ok {
			st.costAccum += turnCost
		} else {
			st.costKnown = false
		}
		r.bumpRunUsage(st.runID, res.Usage, turnCost, st.stepCount)

		// Append the assistant turn to the transcript and persist it as an audit
		// step. Text-only turns and tool-calling turns both land here.
		st.history = append(st.history, res.Message)
		st.seq++
		assistantText := blocksText(res.Message)
		r.persistStep(st.runID, issue.ID, st.seq, stepAssistant, "", "", "", assistantText, false)

		toolUses := toolUseBlocks(res.Message)
		if len(toolUses) == 0 {
			// No tool calls: the model is done. Ensure a diagnosis was posted; if
			// the model wrote a final message but never posted it to the thread,
			// post the assistant text so the reporter sees something, then resolve.
			if !st.postedAnyMsg {
				body := assistantText
				if strings.TrimSpace(body) == "" {
					body = "I looked into this but didn't find anything conclusive."
				}
				_ = r.svc.PostIssueMessage(ctx, issue.ID, body)
			}
			return r.conclude(ctx, issue.ID, st.runID, IssueResolved, "Investigation complete.")
		}

		// Dispatch every tool_use through the allow-list, building tool_result
		// blocks for the next turn and persisting an audit step per call. EVERY
		// tool_use gets a result block even when we are about to park, so the
		// persisted transcript stays valid for the resume.
		var resultBlocks []ai.TranscriptBlock
		concluded := false
		concludeStatus := ""
		parked := false
		for _, tu := range toolUses {
			st.stepCount++
			st.seq++

			out, isErr, ctrl := r.dispatchTool(ctx, issue.ID, tu)
			if ctrl.postedMessage {
				st.postedAnyMsg = true
			}
			if ctrl.concluded {
				concluded = true
				concludeStatus = ctrl.concludeStatus
			}
			if ctrl.parked {
				parked = true
			}
			r.persistStep(st.runID, issue.ID, st.seq, stepToolResult, tu.Name, tu.ID, string(tu.Input), out, isErr)
			resultBlocks = append(resultBlocks, ai.TranscriptBlock{
				Type:      ai.BlockToolResult,
				ToolUseID: tu.ID,
				Name:      tu.Name,
				Content:   out,
				IsError:   isErr,
			})
		}

		st.history = append(st.history, ai.TranscriptMessage{Role: ai.RoleUser, Content: resultBlocks})
		r.persistTranscript(st.runID, st.history)

		if concluded {
			// The conclude_issue tool sets the terminal issue state via IssueStore.
			// Re-assert it here too (idempotent — ConcludeIssue is a no-op once
			// closed) so the issue is guaranteed terminal even if the tool's side
			// effect path changes, then finalize the run and stop the loop.
			status := IssueWontFix
			if concludeStatus == mcp.ConcludeResolved {
				status = IssueResolved
			}
			if err := r.svc.ConcludeIssue(ctx, issue.ID, status, "Investigation complete."); err != nil {
				log.Printf("remediation: finalize conclude issue %d: %v", issue.ID, err)
			}
			return r.finalizeRun(st.runID, runStatusSucceeded, stopResolved)
		}

		if parked {
			// A NEW proposal was recorded. Park: finalize the run as waiting for an
			// approval, set the issue awaiting_approval, release the run claim, and
			// EXIT the loop (no goroutine held during the human wait). The bounds
			// spent so far are already persisted on the run and carry over on resume.
			return r.park(issue.ID, st.runID)
		}
	}
}

// park finalizes a run that proposed an action: it marks the run
// waiting_approval, moves the issue to awaiting_approval, and releases the issue
// claim so the worker goroutine is free during the (possibly long) human wait.
// The Runner re-enters via Resume once an admin decides.
func (r *Runner) park(issueID, runID int64) error {
	if _, err := r.db.Exec(
		"UPDATE agent_runs SET status = ?, stop_reason = ?, deadline_at = NULL WHERE id = ?",
		runStatusWaitingApproval, stopAwaitingApproval, runID,
	); err != nil {
		log.Printf("remediation: park run %d: %v", runID, err)
	}
	// Move the issue to awaiting_approval and release the active_run_id claim so a
	// resume can re-claim it. Guard on the issue not already being closed.
	if _, err := r.db.Exec(
		"UPDATE issues SET status = ?, active_run_id = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND closed_at IS NULL",
		IssueAwaitingApproval, issueID,
	); err != nil {
		log.Printf("remediation: park issue %d: %v", issueID, err)
	}
	return nil
}

// dispatchControl carries side-effect signals from a single tool dispatch back
// to the loop without threading many return values.
type dispatchControl struct {
	postedMessage  bool
	concluded      bool
	concludeStatus string
	parked         bool // propose_action recorded a NEW proposal: pause for admin approval
}

// dispatchTool is the central enforcement check. For a read tool that cleared the
// allow-list it calls ExecuteTool with an ADMIN CallContext (so the RBAC check
// passes — but ONLY because the name already passed the allow-list). For an
// agent-only tool it calls ExecuteAgentTool (which writes issue rows, never arr).
// For ANY other name (every mutating tool) it returns a benign refusal and NEVER
// calls ExecuteTool.
func (r *Runner) dispatchTool(ctx context.Context, issueID int64, tu ai.TranscriptBlock) (out string, isErr bool, ctrl dispatchControl) {
	switch {
	case mcp.IsAgentTool(tu.Name):
		res, err := r.toolServer.ExecuteAgentTool(ctx, tu.Name, tu.Input, issueID, tu.ID)
		if err != nil {
			return "Error: " + err.Error(), true, ctrl
		}
		if tu.Name == mcp.ToolPostIssueMessage {
			ctrl.postedMessage = true
		}
		if res.Concluded {
			ctrl.concluded = true
			ctrl.concludeStatus = res.Status
		}
		if res.Parked {
			ctrl.parked = true
		}
		return res.Text, false, ctrl

	case isReadToolAllowed(tu.Name):
		// Admin CallContext so the read tool's permission check passes — reached
		// ONLY because the name is in the hardcoded read allow-list above.
		res, err := r.toolServer.ExecuteTool(ctx, tu.Name, nonEmptyInput(tu.Input),
			mcp.CallContext{UserID: 0, Role: auth.RoleAdmin})
		if err != nil {
			return "Error: " + err.Error(), true, ctrl
		}
		return res.Text, false, ctrl

	default:
		// Belt-and-suspenders: a tool the model should never have seen (every
		// mutating tool). Refuse WITHOUT calling ExecuteTool — mutation is
		// architecturally impossible.
		log.Printf("remediation: refused non-allow-listed tool %q on issue %d (read-only agent)", tu.Name, issueID)
		return "This tool is not available to the remediation agent (read-only investigation only).", true, ctrl
	}
}

// nonEmptyInput normalizes an empty/null tool input to "{}" for ExecuteTool.
func nonEmptyInput(input json.RawMessage) json.RawMessage {
	if len(input) == 0 || string(input) == "null" {
		return json.RawMessage("{}")
	}
	return input
}

// --- claim / run-row lifecycle ---

// claim CAS-claims the issue (status->investigating, active_run_id set only when
// currently NULL) and creates the agent_runs row. Returns claimed=false when
// another worker already holds the claim.
func (r *Runner) claim(issueID int64, model string) (runID int64, claimed bool, err error) {
	res, err := r.db.Exec(
		"INSERT INTO agent_runs (issue_id, trigger, status, model, proc_generation) VALUES (?, ?, ?, ?, ?)",
		issueID, "user_report", runStatusRunning, model, r.procToken,
	)
	if err != nil {
		return 0, false, fmt.Errorf("create run: %w", err)
	}
	runID, err = res.LastInsertId()
	if err != nil {
		return 0, false, fmt.Errorf("run id: %w", err)
	}

	cas, err := r.db.Exec(
		"UPDATE issues SET status = ?, active_run_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND active_run_id IS NULL AND closed_at IS NULL",
		IssueInvestigating, runID, issueID,
	)
	if err != nil {
		return 0, false, fmt.Errorf("cas claim: %w", err)
	}
	if n, _ := cas.RowsAffected(); n == 0 {
		// Lost the race (or issue closed): mark this run aborted and bail.
		r.db.Exec("UPDATE agent_runs SET status = 'aborted', finished_at = CURRENT_TIMESTAMP WHERE id = ?", runID)
		return 0, false, nil
	}
	return runID, true, nil
}

// finalizeRun stamps a terminal status + stop_reason + finished_at on the run.
func (r *Runner) finalizeRun(runID int64, status, stopReason string) error {
	_, err := r.db.Exec(
		"UPDATE agent_runs SET status = ?, stop_reason = ?, finished_at = CURRENT_TIMESTAMP WHERE id = ?",
		status, stopReason, runID,
	)
	return err
}

// bumpRunUsage accumulates token usage + cost + step count onto the run row.
func (r *Runner) bumpRunUsage(runID int64, u ai.Usage, costMic int64, stepCount int) {
	r.db.Exec(
		`UPDATE agent_runs SET
			input_tokens = input_tokens + ?,
			output_tokens = output_tokens + ?,
			cache_creation_tokens = cache_creation_tokens + ?,
			cache_read_tokens = cache_read_tokens + ?,
			cost_micros = cost_micros + ?,
			step_count = ?
		 WHERE id = ?`,
		u.InputTokens, u.OutputTokens, u.CacheCreationTokens, u.CacheReadTokens, costMic, stepCount, runID,
	)
}

// persistStep writes one human-readable audit row (truncated like the chat path).
func (r *Runner) persistStep(runID, issueID int64, seq int, kind, toolName, toolUseID, toolInput, text string, isErr bool) {
	r.db.Exec(
		`INSERT INTO agent_steps (run_id, issue_id, seq, kind, tool_name, tool_use_id, tool_input, tool_output, text, is_error)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, issueID, seq, kind,
		nullIfEmpty(toolName), nullIfEmpty(toolUseID),
		nullIfEmpty(truncate(toolInput, stepTruncateBytes)),
		nullIfEmpty(truncate(textForOutput(kind, text), stepTruncateBytes)),
		nullIfEmpty(text2(kind, text)),
		boolToInt(isErr),
	)
}

// persistTranscript writes the full (untruncated) provider-neutral transcript to
// agent_runs.transcript_json for a future resume.
func (r *Runner) persistTranscript(runID int64, history ai.Transcript) {
	data, err := json.Marshal(history)
	if err != nil {
		return
	}
	r.db.Exec("UPDATE agent_runs SET transcript_json = ? WHERE id = ?", string(data), runID)
}

// --- terminal helpers ---

// conclude finalizes a resolved/wont_fix run when the model finished without an
// explicit conclude_issue call (tool-less reply). It sets the terminal issue
// state via the same IssueStore path and finalizes the run row.
func (r *Runner) conclude(ctx context.Context, issueID, runID int64, status, resolution string) error {
	if err := r.svc.ConcludeIssue(ctx, issueID, status, resolution); err != nil {
		log.Printf("remediation: conclude issue %d: %v", issueID, err)
	}
	stop := stopResolved
	if status != IssueResolved {
		stop = stopNoDiagnosis
	}
	return r.finalizeRun(runID, runStatusSucceeded, stop)
}

// giveUp is the first-class terminal failure path: a giveup audit step, a
// terminal issue state, a plain-language thread message, an admin notification,
// and a finalized run row. ctx may be context.Background() when the run ctx has
// already expired.
func (r *Runner) giveUp(ctx context.Context, issueID, runID int64, model, stopReason, message string) error {
	// Record the give-up reason as an audit step (best-effort; runID may be 0 if
	// we never managed to claim/create a run).
	if runID != 0 {
		var nextSeq int
		r.db.QueryRow("SELECT COALESCE(MAX(seq),0)+1 FROM agent_steps WHERE run_id = ?", runID).Scan(&nextSeq)
		r.persistStep(runID, issueID, nextSeq, stepGiveup, "", "", "", "give up: "+stopReason, true)
		r.finalizeRun(runID, runStatusGaveUp, stopReason)
	}

	// Post the human-readable explanation to the thread, then mark the issue
	// terminal (wont_fix) and release the claim.
	_ = r.svc.PostIssueMessage(ctx, issueID, message)
	if err := r.svc.ConcludeIssue(ctx, issueID, IssueWontFix, "Agent could not resolve this read-only: "+stopReason); err != nil {
		log.Printf("remediation: giveUp conclude issue %d: %v", issueID, err)
	}

	// Notify admins with a fixed-template event (no model text on the wire).
	if r.svc.notifier != nil {
		r.svc.notifier.NotifyAdmins("issue_updated", map[string]interface{}{
			"issue_id": issueID,
			"status":   IssueWontFix,
		})
	}
	return nil
}

// --- bounds helpers ---

func (r *Runner) dailyRunCapExceeded(cap int) (bool, error) {
	if cap <= 0 {
		return false, nil
	}
	var n int
	err := r.db.QueryRow(
		"SELECT COUNT(*) FROM agent_runs WHERE started_at >= datetime('now','start of day')",
	).Scan(&n)
	if err != nil {
		return false, err
	}
	return n >= cap, nil
}

func (r *Runner) dailyCostCeilingExceeded(ceilingMicros int64) (bool, error) {
	if ceilingMicros <= 0 {
		return false, nil
	}
	var spent sql.NullInt64
	err := r.db.QueryRow(
		"SELECT COALESCE(SUM(cost_micros),0) FROM agent_runs WHERE started_at >= datetime('now','start of day')",
	).Scan(&spent)
	if err != nil {
		return false, err
	}
	return spent.Valid && spent.Int64 >= ceilingMicros, nil
}

// --- small pure helpers ---

func isTerminalStatus(s string) bool {
	switch s {
	case IssueResolved, IssueWontFix, IssueFailed, IssueDismissed:
		return true
	}
	return false
}

// toolUseBlocks extracts tool_use blocks from an assistant turn.
func toolUseBlocks(m ai.TranscriptMessage) []ai.TranscriptBlock {
	var out []ai.TranscriptBlock
	for _, b := range m.Content {
		if b.Type == ai.BlockToolUse {
			out = append(out, b)
		}
	}
	return out
}

// blocksText concatenates the text blocks of a message.
func blocksText(m ai.TranscriptMessage) string {
	var sb strings.Builder
	for _, b := range m.Content {
		if b.Type == ai.BlockText {
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…(truncated)"
}

func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// textForOutput selects which audit column carries the tool output vs the
// assistant text: tool rows put content in tool_output, assistant rows in text.
func textForOutput(kind, text string) string {
	if kind == stepToolResult || kind == stepToolCall {
		return text
	}
	return ""
}

func text2(kind, text string) string {
	if kind == stepAssistant || kind == stepGiveup {
		return text
	}
	return ""
}
