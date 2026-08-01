// remediation.go — admin remediation settings, the agent-action approval
// queue, run audit timelines, and standing auto-approval rules (srv-issues
// §5–§7, app-admin §12). Store, renderers, and seeds live in data_issues.go.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// registerRemediation mounts the remediation routes on the authenticated /api
// router (all admin-gated).
func registerRemediation(r chi.Router) {
	r.With(requireAdmin).Get("/admin/remediation-settings", issHandleGetSettings)
	r.With(requireAdmin).Put("/admin/remediation-settings", issHandlePutSettings)

	r.With(requireAdmin).Get("/admin/agent-actions", issHandleListActions)
	r.With(requireAdmin).Post("/admin/agent-actions/approve-batch", issHandleApproveBatch)
	r.With(requireAdmin).Get("/admin/agent-actions/{id}", issHandleGetAction)
	r.With(requireAdmin).Post("/admin/agent-actions/{id}/approve", issHandleApproveAction)
	r.With(requireAdmin).Post("/admin/agent-actions/{id}/deny", issHandleDenyAction)

	r.With(requireAdmin).Get("/admin/agent-runs/{id}", issHandleGetRun)

	r.With(requireAdmin).Get("/admin/agent-approval-rules", issHandleListRules)
	r.With(requireAdmin).Post("/admin/agent-approval-rules/{id}/pause", issHandlePauseRule)
	r.With(requireAdmin).Post("/admin/agent-approval-rules/{id}/resume", issHandleResumeRule)
	r.With(requireAdmin).Delete("/admin/agent-approval-rules/{id}", issHandleDeleteRule)
}

// ─── Remediation settings ───────────────────────────────

func issSettingsJSON(s issSettingsT) map[string]any {
	return map[string]any{
		"enabled":                    s.Enabled,
		"auto_dispatch":              s.AutoDispatch,
		"allow_reporting":            s.AllowReporting,
		"mark_resolved_as_read":      s.MarkResolvedAsRead,
		"mode":                       s.Mode,
		"provider":                   "",
		"model":                      "",
		"model_override":             s.ModelOverride,
		"model_override_provider":    s.ModelOverrideProvider,
		"max_steps":                  s.MaxSteps,
		"max_turn_tokens":            s.MaxTurnTokens,
		"max_wall_clock_secs":        s.MaxWallClockSecs,
		"daily_run_cap":              s.DailyRunCap,
		"circuit_breaker_giveups":    s.CircuitBreakerGiveups,
		"max_user_wait_hours":        s.MaxUserWaitHours,
		"observation_min_minutes":    s.ObservationMinMinutes,
		"observation_quiet_minutes":  s.ObservationQuietMinutes,
		"observation_settle_minutes": s.ObservationSettleMinutes,
	}
}

func issHandleGetSettings(w http.ResponseWriter, _ *http.Request) {
	issMu.Lock()
	payload := issSettingsJSON(issSettings)
	issMu.Unlock()
	writeJSON(w, http.StatusOK, payload)
}

type issSettingsBody struct {
	Enabled                  bool   `json:"enabled"`
	AutoDispatch             bool   `json:"auto_dispatch"`
	AllowReporting           bool   `json:"allow_reporting"`
	MarkResolvedAsRead       *bool  `json:"mark_resolved_as_read"`
	Mode                     string `json:"mode"`
	Autonomy                 string `json:"autonomy"` // legacy input, translated to mode
	Provider                 string `json:"provider"` // deprecated; normalized ""
	Model                    string `json:"model"`    // deprecated; normalized ""
	ModelOverride            string `json:"model_override"`
	ModelOverrideProvider    string `json:"model_override_provider"` // client value ignored
	MaxSteps                 int    `json:"max_steps"`
	MaxTurnTokens            int    `json:"max_turn_tokens"`
	MaxWallClockSecs         int    `json:"max_wall_clock_secs"`
	DailyRunCap              int    `json:"daily_run_cap"`
	CircuitBreakerGiveups    int    `json:"circuit_breaker_giveups"`
	MaxUserWaitHours         int    `json:"max_user_wait_hours"`
	ObservationMinMinutes    int    `json:"observation_min_minutes"`
	ObservationQuietMinutes  int    `json:"observation_quiet_minutes"`
	ObservationSettleMinutes int    `json:"observation_settle_minutes"`
}

// issClamp normalizes a numeric setting: non-positive falls back to the
// default, values above the server ceiling clamp to the ceiling.
func issClamp(v, def, ceiling int) int {
	if v <= 0 {
		return def
	}
	if v > ceiling {
		return ceiling
	}
	return v
}

func issHandlePutSettings(w http.ResponseWriter, r *http.Request) {
	var body issSettingsBody
	if err := issDecodeJSON(r, &body, false); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.ModelOverride) > 256 {
		writeErr(w, http.StatusBadRequest, "remediation model override is too long")
		return
	}

	mode := body.Mode
	if mode != "investigate_only" && mode != "supervised" {
		// Legacy compatibility: autonomy propose/auto_safe → supervised.
		mode = "supervised"
	}

	s := issSettingsT{
		Enabled:            body.Enabled,
		AutoDispatch:       body.AutoDispatch,
		AllowReporting:     body.AllowReporting,
		MarkResolvedAsRead: body.MarkResolvedAsRead == nil || *body.MarkResolvedAsRead,
		Mode:               mode,
		ModelOverride:      body.ModelOverride,

		MaxSteps:                 issClamp(body.MaxSteps, 12, 50),
		MaxTurnTokens:            issClamp(body.MaxTurnTokens, 4096, 32768),
		MaxWallClockSecs:         issClamp(body.MaxWallClockSecs, 300, 1800),
		DailyRunCap:              issClamp(body.DailyRunCap, 50, 1000),
		CircuitBreakerGiveups:    issClamp(body.CircuitBreakerGiveups, 5, 100),
		MaxUserWaitHours:         issClamp(body.MaxUserWaitHours, 72, 720),
		ObservationMinMinutes:    issClamp(body.ObservationMinMinutes, 10, 1440),
		ObservationQuietMinutes:  issClamp(body.ObservationQuietMinutes, 5, 360),
		ObservationSettleMinutes: issClamp(body.ObservationSettleMinutes, 2, 60),
	}
	// The client's model_override_provider is ignored (server-owned binding).
	// The demo "validates" any non-empty override against the shared provider.
	if s.ModelOverride != "" {
		s.ModelOverrideProvider = "anthropic"
	}

	issMu.Lock()
	issSettings = s
	payload := issSettingsJSON(issSettings)
	issMu.Unlock()
	writeJSON(w, http.StatusOK, payload)
}

// ─── Agent actions ──────────────────────────────────────

func issHandleListActions(w http.ResponseWriter, r *http.Request) {
	statusFilter := r.URL.Query().Get("status")
	issMu.Lock()
	out := make([]map[string]any, 0)
	for _, a := range issLockedActionsSorted() {
		switch statusFilter {
		case "":
			// The approval queue: only genuinely decidable proposals.
			if can, _ := issLockedCanDecide(a); !can {
				continue
			}
		case "all":
			// history view — everything
		default:
			if a.Status != statusFilter {
				continue
			}
		}
		out = append(out, issLockedActionJSON(a))
	}
	issMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"actions": out})
}

func issHandleGetAction(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid action id")
		return
	}
	issMu.Lock()
	a := issActions[id]
	if a == nil {
		issMu.Unlock()
		writeErr(w, http.StatusNotFound, "action not found")
		return
	}
	payload := issLockedActionJSON(a)
	issMu.Unlock()
	writeJSON(w, http.StatusOK, payload)
}

// Sentinel errors for the shared approve core. issErrDecisionConflict maps to
// the batch "skipped" verdict; everything else is a 400/"error" verdict.
var (
	issErrActionNotFound   = errors.New("action not found")
	issErrNotAwaiting      = errors.New("action is not awaiting a decision")
	issErrDecisionConflict = errors.New("action decision conflict: live media state changed or is still active; no fix was executed")
)

// issExecutionResult builds the demo's deterministic execution outcome text.
func issExecutionResult(kind string, params map[string]any, title string) string {
	if title == "" {
		title = "the reported title"
	}
	switch kind {
	case "remediate_queue":
		action, _ := params["action"].(string)
		switch action {
		case "blocklist_search":
			return fmt.Sprintf("Removed the stuck download for %s from the queue, blocklisted the release, and started a search for a replacement. A healthier release was grabbed and is downloading.", title)
		case "remove":
			return fmt.Sprintf("Removed the download for %s from the queue.", title)
		case "change_category":
			return fmt.Sprintf("Moved the download for %s to the correct category; the client resumed processing it.", title)
		}
		return fmt.Sprintf("Remediated the queue item for %s.", title)
	case "grab_release":
		release, _ := params["release_title"].(string)
		indexer, _ := params["indexer"].(string)
		if release == "" {
			release = "the proposed release"
		}
		if indexer == "" {
			indexer = "the indexer"
		}
		return fmt.Sprintf("Grabbed %s from %s and blocklisted the previous release. The import completed and the file verified.", release, indexer)
	case "manual_import":
		return fmt.Sprintf("Imported the completed download for %s into the library.", title)
	case "trigger_search":
		return fmt.Sprintf("Triggered a new search for %s; the best available release was grabbed.", title)
	case "rescan":
		return fmt.Sprintf("Rescanned %s; the library entry now matches the files on disk.", title)
	}
	return "The approved fix was applied."
}

// issResolutionText is the requester-facing resolution stored on the issue
// after an approved fix executes and verifies.
func issResolutionText(kind string) string {
	switch kind {
	case "remediate_queue":
		return "The stuck download was removed and blocklisted, and a verified replacement release was downloaded and imported."
	case "grab_release":
		return "The problematic copy was blocklisted and replaced with a verified release."
	case "manual_import":
		return "The completed download was imported and the file verified."
	case "trigger_search":
		return "A new search grabbed a healthy release, which imported and verified."
	case "rescan":
		return "The library entry was rescanned and now matches the files on disk."
	}
	return "The approved fix was applied and verified."
}

// issLockedArmRule creates (or reactivates) the standing auto-approval rule
// for a triple after an approve-with-remember. Call with issMu held.
func issLockedArmRule(adminID, seedActionID int, problemKind, actionKind, facet string) {
	now := time.Now().UTC()
	for _, rule := range issRules {
		if rule.ProblemKind == problemKind && rule.ActionKind == actionKind && rule.ActionFacet == facet {
			if rule.Status != "active" {
				rule.Status = "active"
				rule.PausedReason = ""
				rule.PausedAt = nil
				rule.UpdatedAt = now
			}
			return
		}
	}
	adminName := ""
	if u := userByID(adminID); u != nil {
		adminName = u.Username
	}
	rule := &issRule{
		ID: issNextRuleID, ProblemKind: problemKind, ActionKind: actionKind,
		ActionFacet: facet, Status: "active",
		CreatedBy: adminID, CreatedByName: adminName, SeedActionID: seedActionID,
		CreatedAt: now, UpdatedAt: now,
	}
	issNextRuleID++
	issRules[rule.ID] = rule
}

// issDecideApprove is the single-approve core, shared with approve-batch. It
// executes a decidable proposal (the demo's execution always succeeds),
// appends the agent's outcome messages to the issue thread, resolves the
// issue, and emits agent_action_decided + issue_updated. Durable
// (executing/executed/failed/outcome_unknown) retries return 200 with the
// stored action and never dispatch twice.
func issDecideApprove(adminID, actionID int, override map[string]any, remember bool) (map[string]any, error) {
	issMu.Lock()
	a := issActions[actionID]
	if a == nil {
		issMu.Unlock()
		return nil, issErrActionNotFound
	}
	switch a.Status {
	case "executing", "executed", "failed", "outcome_unknown":
		payload := issLockedActionJSON(a)
		issMu.Unlock()
		return payload, nil
	case "denied", "superseded":
		issMu.Unlock()
		return nil, issErrNotAwaiting
	}
	if can, _ := issLockedCanDecide(a); !can {
		issMu.Unlock()
		return nil, issErrDecisionConflict
	}

	paramsToRun := a.Params
	if override != nil {
		if problem := issValidateParams(a.Kind, override); problem != "" {
			issMu.Unlock()
			return nil, fmt.Errorf("invalid override: %s", problem)
		}
		paramsToRun = override
	}

	issue := issIssues[a.IssueID]
	// A remember-armed rule is created before approving; an undeliverable
	// remember (no problem label, unusable params) is silently dropped.
	if remember && issue != nil && issue.ProblemKind != "" {
		if facet, ok := issActionFacet(a.Kind, paramsToRun); ok {
			issLockedArmRule(adminID, a.ID, issue.ProblemKind, a.Kind, facet)
		}
	}

	now := time.Now().UTC()
	title := ""
	if issue != nil {
		title = issue.Title
	}
	result := issExecutionResult(a.Kind, paramsToRun, title)

	a.Status = "executed"
	a.ApprovedParams = paramsToRun
	a.DecidedBy = adminID
	decided := now
	a.DecidedAt = &decided
	executed := now
	a.ExecutedAt = &executed
	a.ResultText = result

	if a.RunID != 0 {
		if run := issRuns[a.RunID]; run != nil {
			run.Status = "succeeded"
			run.StopReason = "resolved"
			finished := now
			run.FinishedAt = &finished
		}
	}

	reporterID := 0
	if issue != nil && issue.ClosedAt == nil {
		issLockedAppendMsg(issue.ID, "agent", "", "Approved and executed: "+result, now)
		issLockedAppendMsg(issue.ID, "agent", "",
			"The fix completed and the media state now checks out healthy. Marking this issue resolved — thanks for the report.",
			now.Add(time.Second))
		issue.Status = "resolved"
		issue.Resolution = issResolutionText(a.Kind)
		issue.ResolutionKind = "agent_concluded"
		closed := now
		issue.ClosedAt = &closed
		issue.UpdatedAt = now
		issue.Read = issSettings.MarkResolvedAsRead
		reporterID = issue.ReporterID
	}

	pending := issLockedPendingCount()
	payload := issLockedActionJSON(a)
	issueID := a.IssueID
	issMu.Unlock()

	wsToAdmins(evtAgentActionDecided, map[string]any{
		"issue_id": issueID, "status": "executed", "pending_count": pending,
	})
	wsToAdmins(evtIssueUpdated, map[string]any{"issue_id": issueID})
	if reporterID != 0 {
		wsToUser(reporterID, evtIssueUpdated, map[string]any{"issue_id": issueID})
	}
	return payload, nil
}

type issDecisionBody struct {
	Override json.RawMessage `json:"override"`
	Remember bool            `json:"remember"`
}

func issHandleApproveAction(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid action id")
		return
	}
	var body issDecisionBody
	if err := issDecodeJSON(r, &body, true); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var override map[string]any
	if len(body.Override) > 0 && string(body.Override) != "null" {
		if err := json.Unmarshal(body.Override, &override); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid override: not a JSON object")
			return
		}
	}
	payload, err := issDecideApprove(u.ID, id, override, body.Remember)
	if err != nil {
		// All approve-path errors are 400 (deny uses 409 for conflicts).
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

type issBatchBody struct {
	IDs []int `json:"ids"`
}

func issHandleApproveBatch(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var body issBatchBody
	if err := issDecodeJSON(r, &body, false); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.IDs) == 0 {
		writeErr(w, http.StatusBadRequest, "ids is required")
		return
	}
	if len(body.IDs) > 100 {
		writeErr(w, http.StatusBadRequest, "at most 100 ids per request")
		return
	}

	results := make([]map[string]any, 0, len(body.IDs))
	seen := map[int]bool{}
	for _, id := range body.IDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		payload, err := issDecideApprove(u.ID, id, nil, false)
		switch {
		case errors.Is(err, issErrDecisionConflict):
			results = append(results, map[string]any{"id": id, "status": "skipped", "detail": err.Error()})
		case err != nil:
			results = append(results, map[string]any{"id": id, "status": "error", "detail": err.Error()})
		default:
			item := map[string]any{"id": id}
			status, _ := payload["status"].(string)
			item["status"] = status
			if status != "executed" {
				if rt, ok := payload["result_text"].(string); ok && rt != "" {
					item["detail"] = rt
				}
			}
			results = append(results, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

type issDenyBody struct {
	Note string `json:"note"`
}

func issHandleDenyAction(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid action id")
		return
	}
	var body issDenyBody
	if err := issDecodeJSON(r, &body, true); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.Note) > 8192 {
		writeErr(w, http.StatusBadRequest, "denial note is too long")
		return
	}

	issMu.Lock()
	a := issActions[id]
	if a == nil {
		issMu.Unlock()
		writeErr(w, http.StatusBadRequest, "action not found")
		return
	}
	switch a.Status {
	case "denied":
		// Idempotent: denying an already-denied action returns it unchanged.
		payload := issLockedActionJSON(a)
		issMu.Unlock()
		writeJSON(w, http.StatusOK, payload)
		return
	case "executed":
		issMu.Unlock()
		writeErr(w, http.StatusConflict, "action decision conflict: action is already executed")
		return
	case "executing":
		issMu.Unlock()
		writeErr(w, http.StatusConflict, "action decision conflict: action is now executing")
		return
	case "proposed":
		// fall through to the denial below
	default: // superseded, failed, outcome_unknown
		issMu.Unlock()
		writeErr(w, http.StatusConflict, "action decision conflict: action is no longer awaiting an active approval gate")
		return
	}

	now := time.Now().UTC()
	a.Status = "denied"
	a.DecidedBy = u.ID
	decided := now
	a.DecidedAt = &decided
	a.DenyReason = body.Note

	// A denial resumes the investigation: the issue returns to investigating
	// so the agent can try something else.
	reporterID := 0
	issueID := a.IssueID
	if issue := issIssues[issueID]; issue != nil && issue.ClosedAt == nil {
		if issue.Status == "awaiting_approval" {
			issue.Status = "investigating"
		}
		issue.UpdatedAt = now
		issue.Read = true
		issLockedAppendMsg(issue.ID, "agent", "",
			"Understood — I won't apply that fix. Let me look for an alternative approach.", now)
		reporterID = issue.ReporterID
	}
	if a.RunID != 0 {
		if run := issRuns[a.RunID]; run != nil && run.Status == "waiting_approval" {
			run.Status = "running"
			run.StopReason = ""
		}
	}
	pending := issLockedPendingCount()
	payload := issLockedActionJSON(a)
	issMu.Unlock()

	wsToAdmins(evtAgentActionDecided, map[string]any{
		"issue_id": issueID, "status": "denied", "pending_count": pending,
	})
	wsToAdmins(evtIssueUpdated, map[string]any{"issue_id": issueID})
	if reporterID != 0 {
		wsToUser(reporterID, evtIssueUpdated, map[string]any{"issue_id": issueID})
	}
	writeJSON(w, http.StatusOK, payload)
}

// ─── Agent runs ─────────────────────────────────────────

func issHandleGetRun(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid run id")
		return
	}
	issMu.Lock()
	run := issRuns[id]
	if run == nil {
		issMu.Unlock()
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	steps := make([]map[string]any, 0, len(issSteps[id]))
	for _, s := range issSteps[id] {
		steps = append(steps, issLockedStepJSON(s))
	}
	payload := map[string]any{"run": issLockedRunJSON(run), "steps": steps}
	issMu.Unlock()
	writeJSON(w, http.StatusOK, payload)
}

// ─── Standing auto-approval rules ───────────────────────

func issHandleListRules(w http.ResponseWriter, _ *http.Request) {
	issMu.Lock()
	out := make([]map[string]any, 0, len(issRules))
	for _, rule := range issLockedRulesSorted() {
		out = append(out, issLockedRuleJSON(rule))
	}
	issMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"rules": out})
}

func issRuleFromRequest(w http.ResponseWriter, r *http.Request) (*issRule, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid rule id")
		return nil, false
	}
	rule := issRules[id]
	if rule == nil {
		writeErr(w, http.StatusNotFound, "approval rule not found")
		return nil, false
	}
	return rule, true
}

func issHandlePauseRule(w http.ResponseWriter, r *http.Request) {
	issMu.Lock()
	rule, ok := issRuleFromRequest(w, r)
	if !ok {
		issMu.Unlock()
		return
	}
	// Idempotent: an already-paused rule keeps its (possibly automatic)
	// pause reason.
	if rule.Status != "paused" {
		now := time.Now().UTC()
		rule.Status = "paused"
		rule.PausedReason = "Paused by an administrator."
		paused := now
		rule.PausedAt = &paused
		rule.UpdatedAt = now
	}
	payload := issLockedRuleJSON(rule)
	issMu.Unlock()
	writeJSON(w, http.StatusOK, payload)
}

func issHandleResumeRule(w http.ResponseWriter, r *http.Request) {
	issMu.Lock()
	rule, ok := issRuleFromRequest(w, r)
	if !ok {
		issMu.Unlock()
		return
	}
	if rule.Status != "active" {
		rule.Status = "active"
		rule.PausedReason = ""
		rule.PausedAt = nil
		rule.UpdatedAt = time.Now().UTC()
	}
	payload := issLockedRuleJSON(rule)
	issMu.Unlock()
	writeJSON(w, http.StatusOK, payload)
}

func issHandleDeleteRule(w http.ResponseWriter, r *http.Request) {
	issMu.Lock()
	rule, ok := issRuleFromRequest(w, r)
	if !ok {
		issMu.Unlock()
		return
	}
	// Hard delete. Historical actions keep auto_rule_id; their
	// auto_rule_label renders null from now on (rule lookup misses).
	delete(issRules, rule.ID)
	issMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
