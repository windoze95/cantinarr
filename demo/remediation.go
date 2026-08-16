// remediation.go — admin remediation settings, the agent-action approval
// queue, run audit timelines, standing auto-approval rules, the rule-candidate
// catalog with arm-from-catalog, and the agent digest scoreboard (srv-issues
// §5–§7, app-admin §12). Store, renderers, and seeds live in data_issues.go;
// the decorations this file adds on top of those shared renderers (approval-
// card identity, prior attempts, pause metadata) live here.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// registerRemediation mounts the remediation routes on the authenticated /api
// router (all admin-gated).
func registerRemediation(r chi.Router) {
	r.With(requireAdmin).Get("/admin/remediation-settings", issHandleGetSettings)
	r.With(requireAdmin).Put("/admin/remediation-settings", issHandlePutSettings)

	r.With(requireAdmin).Get("/admin/agent-digest", issHandleAgentDigest)

	r.With(requireAdmin).Get("/admin/agent-actions", issHandleListActions)
	r.With(requireAdmin).Post("/admin/agent-actions/approve-batch", issHandleApproveBatch)
	r.With(requireAdmin).Get("/admin/agent-actions/{id}", issHandleGetAction)
	r.With(requireAdmin).Post("/admin/agent-actions/{id}/approve", issHandleApproveAction)
	r.With(requireAdmin).Post("/admin/agent-actions/{id}/deny", issHandleDenyAction)

	r.With(requireAdmin).Get("/admin/agent-runs/{id}", issHandleGetRun)

	r.With(requireAdmin).Get("/admin/agent-approval-rules", issHandleListRules)
	r.With(requireAdmin).Post("/admin/agent-approval-rules", issHandleArmRule)
	r.With(requireAdmin).Get("/admin/agent-approval-rules/candidates", issHandleListRuleCandidates)
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

// ─── Agent digest (the scoreboard) ──────────────────────

// issDigestBase is the fictional background week the tiny seeded store cannot
// carry on its own: work the digest attributes to a wider history whose closed
// issues have already aged out of the bounded closed list. The live store's
// own contribution is computed per request and ADDED to these, so a fix the
// reviewer approves during the tour moves the scoreboard exactly like the real
// server's would. Numbers are chosen so the outcome arithmetic the app renders
// always balances: resolved lanes (by agent / by rules / by admin) partition
// issues_resolved, and the "on their own" remainder equals self_cleared.
var issDigestBase = struct {
	IssuesOpened    int
	IssuesResolved  int // real work: 1 by agent + 1 by rules + 1 by admin
	SelfCleared     int
	ResolvedByAgent int
	ResolvedByAdmin int
	ClosedNoFix     int
	Dismissed       int
	ZeroTouch       int
	ActionsExecuted int
	RuleApproved    int
	ReporterClosed  int
	TokensIn        int
	TokensOut       int
}{
	IssuesOpened: 4, IssuesResolved: 3, SelfCleared: 2,
	ResolvedByAgent: 1, ResolvedByAdmin: 1,
	ClosedNoFix: 1, Dismissed: 1,
	ZeroTouch: 1, ActionsExecuted: 2, RuleApproved: 1, ReporterClosed: 1,
	TokensIn: 49500, TokensOut: 6400,
}

// issHandleAgentDigest serves GET /api/admin/agent-digest?days=7 — the agent
// scoreboard (outcome-first "resolved" with disjoint attribution lanes; the
// needs-a-human / pending / paused numbers are state NOW, not window facts).
// The window numbers are issDigestBase plus whatever the live store actually
// did inside the requested cutoff; the base itself always tells the trailing-
// week story whatever `days` says, which only ever diverges for hand-crafted
// query strings (the app requests days=7).
func issHandleAgentDigest(w http.ResponseWriter, r *http.Request) {
	days := queryInt(r, "days", 7)
	if days <= 0 {
		days = 7
	}
	if days > 90 {
		days = 90
	}
	now := time.Now().UTC()
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)

	issMu.Lock()
	var (
		opened, resolved, byAgent, byAdmin  int
		closedNoFix, dismissed              int
		reporterClosed, executed, zeroTouch int
		ruleApproved                        int
		tokensIn, tokensOut                 int
		needsAdmin, pausedRules             int
	)
	for _, i := range issIssues {
		if i.CreatedAt.After(cutoff) {
			opened++
		}
		if i.ClosedAt == nil {
			if i.Status == "needs_admin" {
				needsAdmin++
			}
			continue
		}
		if !i.ClosedAt.After(cutoff) {
			continue
		}
		if i.ResolutionKind == "reporter_confirmed" {
			reporterClosed++
		}
		switch i.Status {
		case "resolved":
			resolved++
			hasExecuted, humanDecided, ruleCarried := false, false, false
			for _, a := range issActions {
				if a.IssueID != i.ID {
					continue
				}
				// Zero-touch means no human decided ANY action on the issue
				// (a denial counts as touching it), matching the real query.
				if a.DecidedBy != 0 {
					humanDecided = true
				}
				if a.Status != "executed" {
					continue
				}
				hasExecuted = true
				if a.AutoRuleID != 0 && a.DecidedBy == 0 {
					ruleCarried = true
				}
			}
			switch {
			case ruleCarried:
				ruleApproved++
			case hasExecuted:
				byAgent++
			case i.ResolutionKind == "admin_completed":
				byAdmin++
			}
			if hasExecuted && !humanDecided {
				zeroTouch++
			}
		case "wont_fix":
			closedNoFix++
		case "dismissed":
			dismissed++
		}
	}
	for _, a := range issActions {
		if a.Status == "executed" && a.ExecutedAt != nil && a.ExecutedAt.After(cutoff) {
			executed++
		}
	}
	for _, run := range issRuns {
		if run.StartedAt.After(cutoff) {
			tokensIn += run.InputTokens
			tokensOut += run.OutputTokens
		}
	}
	for _, rule := range issRules {
		if rule.Status == "paused" {
			pausedRules++
		}
	}
	pendingProposals := issLockedPendingCount()
	// The one background rule execution this week belongs to seeded rule 1; the
	// real query joins the live rules table, so a deleted rule drops out of the
	// per-rule breakdown (its executions stay in the totals).
	ruleCounts := make([]map[string]any, 0, 1)
	if rule := issRules[1]; rule != nil {
		ruleCounts = append(ruleCounts, map[string]any{
			"label": issRuleLabel(rule.ProblemKind, rule.ActionKind, rule.ActionFacet),
			"count": issDigestBase.RuleApproved,
		})
	}
	issMu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"days":              days,
		"issues_opened":     issDigestBase.IssuesOpened + opened,
		"issues_resolved":   issDigestBase.IssuesResolved + resolved,
		"self_cleared":      issDigestBase.SelfCleared,
		"resolved_by_agent": issDigestBase.ResolvedByAgent + byAgent,
		"resolved_by_admin": issDigestBase.ResolvedByAdmin + byAdmin,
		"closed_no_fix":     issDigestBase.ClosedNoFix + closedNoFix,
		"dismissed":         issDigestBase.Dismissed + dismissed,
		"zero_touch":        issDigestBase.ZeroTouch + zeroTouch,
		"actions_executed":  issDigestBase.ActionsExecuted + executed,
		"rule_approved":     issDigestBase.RuleApproved + ruleApproved,
		"reporter_closed":   issDigestBase.ReporterClosed + reporterClosed,
		"tokens_in":         issDigestBase.TokensIn + tokensIn,
		"tokens_out":        issDigestBase.TokensOut + tokensOut,
		"needs_admin_open":  needsAdmin,
		"pending_proposals": pendingProposals,
		"paused_rules":      pausedRules,
		"rule_counts":       ruleCounts,
		"generated_at":      now,
	})
}

// ─── Agent actions ──────────────────────────────────────

// issLockedActionJSONFull decorates the shared action renderer
// (data_issues.go) with the approval-card fields the current app parses on
// this file's surfaces: the joined issue identity (poster key + exact
// season/episode scope + recurrence count) and, on decidable proposals only,
// the prior-attempts memory. Call with issMu held.
func issLockedActionJSONFull(a *issAction) map[string]any {
	m := issLockedActionJSON(a)
	tmdbID, season, episode, occurrences := 0, 0, 0, 0
	if issue := issIssues[a.IssueID]; issue != nil {
		tmdbID = issue.TmdbID
		season = issue.SeasonNumber
		episode = issue.EpisodeNumber
		occurrences = issue.Occurrences
	}
	m["issue_tmdb_id"] = tmdbID
	m["issue_season"] = season
	m["issue_episode"] = episode
	m["issue_occurrences"] = occurrences
	// prior_attempts mirrors the real omitempty decoration: present only on a
	// decidable proposal whose issue already saw executed fixes.
	if can, _ := issLockedCanDecide(a); can {
		if attempts := issLockedPriorAttempts(a); len(attempts) > 0 {
			m["prior_attempts"] = attempts
		}
	}
	return m
}

// issLockedPriorAttempts renders the executed sibling fixes on the same issue,
// oldest first — the record the approving human reads so they never decide
// with less evidence than the model. The demo's executions always hold, so
// recurred (the arr re-added the SAME download after the fix ran) stays false.
// Call with issMu held.
func issLockedPriorAttempts(a *issAction) []map[string]any {
	type attempt struct {
		kind, facet string
		at          time.Time
	}
	var prior []attempt
	for _, sib := range issActions {
		if sib.IssueID != a.IssueID || sib.ID == a.ID || sib.Status != "executed" || sib.ExecutedAt == nil {
			continue
		}
		params := sib.ApprovedParams
		if params == nil {
			params = sib.Params
		}
		facet, ok := issActionFacet(sib.Kind, params)
		if !ok {
			continue
		}
		prior = append(prior, attempt{kind: sib.Kind, facet: facet, at: *sib.ExecutedAt})
	}
	sort.Slice(prior, func(i, j int) bool { return prior[i].at.Before(prior[j].at) })
	out := make([]map[string]any, 0, len(prior))
	for _, p := range prior {
		item := map[string]any{
			"kind":        p.kind,
			"executed_at": p.at,
			"recurred":    false,
		}
		if p.facet != "" {
			item["facet"] = p.facet
		}
		out = append(out, item)
	}
	return out
}

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
		out = append(out, issLockedActionJSONFull(a))
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
	payload := issLockedActionJSONFull(a)
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
// for a triple — approve-with-remember and arm-from-catalog share it, exactly
// like the real createOrReactivateApprovalRule. An existing (possibly paused)
// triple only re-arms: creator, seed, and counters survive pause/resume
// cycles. Returns the armed rule's id. Call with issMu held.
func issLockedArmRule(adminID, seedActionID int, problemKind, actionKind, facet string) int {
	now := time.Now().UTC()
	for _, rule := range issRules {
		if rule.ProblemKind == problemKind && rule.ActionKind == actionKind && rule.ActionFacet == facet {
			if rule.Status != "active" {
				rule.Status = "active"
				rule.PausedReason = ""
				rule.PausedAt = nil
				rule.UpdatedAt = now
				// The seeded since-pause tally belongs to the seeded pause; a
				// later runtime pause starts a fresh (empty) count.
				delete(issRuleApprovedSincePause, rule.ID)
			}
			return rule.ID
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
	return rule.ID
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
		payload := issLockedActionJSONFull(a)
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
	payload := issLockedActionJSONFull(a)
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
		payload := issLockedActionJSONFull(a)
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
	payload := issLockedActionJSONFull(a)
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

// issRuleApprovedSincePause seeds the paused rules' "you keep doing this by
// hand" nudge: manual approvals of the exact triple since the pause, which the
// real server recomputes per read. Seeded rule 2 ("Waiting to import" ×
// manual import) paused five days ago and the admin hand-approved the same
// fix twice since — 2 of the 3 hand-approvals its catalog candidate carries.
// Runtime hand-repeats cannot occur (each demo issue holds at most one
// decidable proposal and no new ones spawn), so the map stays authoritative.
var issRuleApprovedSincePause = map[int]int{2: 2}

// issLockedRuleJSONFull decorates the shared rule renderer (data_issues.go)
// with the pause-metadata fields the current app parses. Call with issMu held.
func issLockedRuleJSONFull(rule *issRule) map[string]any {
	m := issLockedRuleJSON(rule)
	// The demo's pauses are seeded or admin-initiated; the real server records
	// an issue link only on automatic failure pauses, so this stays null.
	m["paused_by_issue_id"] = nil
	count := 0
	if rule.Status == "paused" && rule.PausedAt != nil {
		count = issRuleApprovedSincePause[rule.ID]
	}
	m["approved_since_pause"] = count
	return m
}

// issCandidateSeed is one fictional-background triple the admin has approved
// by hand and could automate (the "Ready to automate" catalog).
type issCandidateSeed struct {
	ProblemKind   string
	ActionKind    string
	ActionFacet   string
	ApprovedCount int
	LastApproved  time.Time
}

// issCandidateSeeds carries the background week's hand-approved triples. The
// first shares seeded PAUSED rule 2's exact triple — the real catalog keeps
// paused triples listed, and arming from it reactivates the rule, same as
// remember. The second has no rule yet. Live hand-approvals made during the
// tour join these at read time (issLockedRuleCandidates).
var issCandidateSeeds []issCandidateSeed

func init() {
	now := time.Now().UTC()
	issCandidateSeeds = []issCandidateSeed{
		{ProblemKind: "Waiting to import", ActionKind: "manual_import", ActionFacet: "",
			ApprovedCount: 3, LastApproved: now.Add(-48 * time.Hour)},
		{ProblemKind: "Download failed", ActionKind: "trigger_search", ActionFacet: "",
			ApprovedCount: 2, LastApproved: now.Add(-5 * 24 * time.Hour)},
	}
}

// issLockedRuleCandidates aggregates every triple with at least one
// hand-approved execution — seeded background plus the live store's own
// human-decided executed actions — minus triples an ACTIVE rule already
// covers (paused ones stay listed; arming reactivates). Mirrors the real
// ListRuleCandidates: most-approved first. Call with issMu held.
func issLockedRuleCandidates() []map[string]any {
	type agg struct {
		problemKind, actionKind, actionFacet string
		count                                int
		last                                 *time.Time
	}
	key := func(problemKind, actionKind, facet string) string {
		return problemKind + "\x00" + actionKind + "\x00" + facet
	}
	byKey := map[string]*agg{}
	note := func(problemKind, actionKind, facet string, n int, last *time.Time) {
		k := key(problemKind, actionKind, facet)
		c := byKey[k]
		if c == nil {
			c = &agg{problemKind: problemKind, actionKind: actionKind, actionFacet: facet}
			byKey[k] = c
		}
		c.count += n
		if last != nil && (c.last == nil || last.After(*c.last)) {
			t := *last
			c.last = &t
		}
	}
	for i := range issCandidateSeeds {
		s := &issCandidateSeeds[i]
		t := s.LastApproved
		note(s.ProblemKind, s.ActionKind, s.ActionFacet, s.ApprovedCount, &t)
	}
	for _, a := range issActions {
		if a.Status != "executed" || a.DecidedBy == 0 || a.ExecutedAt == nil {
			continue
		}
		issue := issIssues[a.IssueID]
		if issue == nil || issue.ProblemKind == "" {
			continue
		}
		params := a.ApprovedParams
		if params == nil {
			params = a.Params
		}
		facet, ok := issActionFacet(a.Kind, params)
		if !ok {
			continue
		}
		note(issue.ProblemKind, a.Kind, facet, 1, a.DecidedAt)
	}
	for _, rule := range issRules {
		if rule.Status == "active" {
			delete(byKey, key(rule.ProblemKind, rule.ActionKind, rule.ActionFacet))
		}
	}
	candidates := make([]*agg, 0, len(byKey))
	for _, c := range byKey {
		candidates = append(candidates, c)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].count != candidates[j].count {
			return candidates[i].count > candidates[j].count
		}
		return issRuleLabel(candidates[i].problemKind, candidates[i].actionKind, candidates[i].actionFacet) <
			issRuleLabel(candidates[j].problemKind, candidates[j].actionKind, candidates[j].actionFacet)
	})
	out := make([]map[string]any, 0, len(candidates))
	for _, c := range candidates {
		var last any
		if c.last != nil {
			last = c.last.UTC()
		}
		out = append(out, map[string]any{
			"problem_kind":     c.problemKind,
			"action_kind":      c.actionKind,
			"action_facet":     c.actionFacet,
			"label":            issRuleLabel(c.problemKind, c.actionKind, c.actionFacet),
			"approved_count":   c.count,
			"last_approved_at": last,
		})
	}
	return out
}

// issHandleListRuleCandidates serves GET /admin/agent-approval-rules/candidates.
func issHandleListRuleCandidates(w http.ResponseWriter, _ *http.Request) {
	issMu.Lock()
	out := issLockedRuleCandidates()
	issMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"candidates": out})
}

type issArmRuleBody struct {
	ProblemKind string `json:"problem_kind"`
	ActionKind  string `json:"action_kind"`
	ActionFacet string `json:"action_facet"`
}

// issHandleArmRule serves POST /admin/agent-approval-rules — arm a standing
// rule from the catalog. Grounded server-side like the real handler: only a
// triple the catalog currently lists (hand-approved, no active rule) can be
// armed, whatever the client claimed.
func issHandleArmRule(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var body issArmRuleBody
	// The real handler decodes leniently (unknown fields tolerated).
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	issMu.Lock()
	grounded := false
	for _, c := range issLockedRuleCandidates() {
		if c["problem_kind"] == body.ProblemKind && c["action_kind"] == body.ActionKind &&
			c["action_facet"] == body.ActionFacet {
			grounded = true
			break
		}
	}
	if !grounded {
		issMu.Unlock()
		writeErr(w, http.StatusConflict,
			"this exact fix has never been approved by hand; approve it once on a real proposal first")
		return
	}
	ruleID := issLockedArmRule(u.ID, 0, body.ProblemKind, body.ActionKind, body.ActionFacet)
	issMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"rule_id": ruleID})
}

func issHandleListRules(w http.ResponseWriter, _ *http.Request) {
	issMu.Lock()
	out := make([]map[string]any, 0, len(issRules))
	for _, rule := range issLockedRulesSorted() {
		out = append(out, issLockedRuleJSONFull(rule))
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
	payload := issLockedRuleJSONFull(rule)
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
		// The seeded since-pause tally belongs to the seeded pause; a later
		// runtime pause starts a fresh (empty) count.
		delete(issRuleApprovedSincePause, rule.ID)
	}
	payload := issLockedRuleJSONFull(rule)
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
