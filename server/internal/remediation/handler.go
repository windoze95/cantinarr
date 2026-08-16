package remediation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/auth"
)

const maxRemediationRequestBytes = 64 << 10

// Handler exposes the Wave-1 issue-reporting REST surface. It clones the shape
// of request.Handler (claims gate, JSON helpers, decodeOptional).
type Handler struct {
	service                     *Service
	validateSharedModelOverride func(context.Context, string) (string, error)
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// SetSharedModelOverrideValidator wires the real shared-provider response test
// used before a remediation-only model override is committed.
func (h *Handler) SetSharedModelOverrideValidator(validate func(context.Context, string) (string, error)) {
	h.validateSharedModelOverride = validate
}

// Create handles POST /api/issues (PermissionMediaRequest). The reporter is the
// authenticated user; reason/title are UNTRUSTED.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !h.service.Settings().AllowReporting {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "problem reporting is disabled"})
		return
	}

	var req CreateIssueRequest
	if err := decodeJSON(w, r, &req, false); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.InstanceID == "" || req.MediaType == "" || req.Category == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "instance_id, media_type, and category required"})
		return
	}
	if req.MediaType == "book" {
		if strings.TrimSpace(req.ForeignID) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "foreign_id required for a book report"})
			return
		}
	} else if req.TmdbID == 0 && req.TvdbID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tmdb_id or tvdb_id required"})
		return
	}

	resp, err := h.service.CreateUserIssue(claims.UserID, &req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ListRuleCandidates handles GET /api/admin/agent-approval-rules/candidates.
func (h *Handler) ListRuleCandidates(w http.ResponseWriter, r *http.Request) {
	candidates, err := h.service.ListRuleCandidates()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidates": candidates})
}

// ArmRule handles POST /api/admin/agent-approval-rules — grounded server-side:
// only triples the admin has actually hand-approved can be armed.
func (h *Handler) ArmRule(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var body struct {
		ProblemKind string `json:"problem_kind"`
		ActionKind  string `json:"action_kind"`
		ActionFacet string `json:"action_facet"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	ruleID, err := h.service.ArmRuleFromCatalog(claims.UserID, body.ProblemKind, body.ActionKind, body.ActionFacet)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rule_id": ruleID})
}

// Digest handles GET /api/admin/agent-digest?days=7 — the agent scoreboard.
func (h *Handler) Digest(w http.ResponseWriter, r *http.Request) {
	days := 7
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v > 0 {
		days = v
	}
	digest, err := h.service.Digest(days)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, digest)
}

// ListMine handles GET /api/issues — the reporter inbox: the caller's OWN
// reports, newest first. Reporter-visible copy only: the requester-copy
// boundary rewrites admin-facing resolution text before it leaves the server.
func (h *Handler) ListMine(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	issues, err := h.service.ListIssuesForReporter(claims.UserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	for i := range issues {
		applyRequesterCopy(&issues[i])
	}
	writeJSON(w, http.StatusOK, ListIssuesResponse{Issues: issues})
}

// applyRequesterCopy is the requester-copy boundary: an issue leaving the
// server to its REPORTER carries only server-authored requester vocabulary.
// The resolution column accumulates admin-facing diagnostics — executor result
// text, give-up reasons, "verify the current arr state" — which are the right
// words for the admin queue and the wrong words for the person who reported a
// wrong episode. Every (status, resolution_kind) pair maps to fixed copy here;
// the admin surfaces keep the raw fields. Rewriting at the read boundary,
// rather than at each write site, means a new admin-side message can never
// leak by default.
func applyRequesterCopy(issue *Issue) {
	switch issue.Status {
	case IssueNeedsAdmin:
		issue.Resolution = "An administrator is taking a closer look at this."
	case IssueAwaitingConfirmation:
		issue.Resolution = "A fix was applied — please open the report and confirm whether it's right now."
	case IssueWontFix:
		switch issue.ResolutionKind {
		case ResolutionReporterTimeout:
			issue.Resolution = "Closed after no reply. If this is still a problem, report it again."
		default:
			issue.Resolution = "This was closed without a fix. If it still looks wrong, report it again."
		}
	case IssueResolved:
		switch issue.ResolutionKind {
		case ResolutionReporterConfirmed:
			issue.Resolution = "You confirmed this is fixed."
		default:
			issue.Resolution = "This was resolved. If it still looks wrong, report it again."
		}
	case IssueDismissed:
		issue.Resolution = "An administrator closed this report."
	}
}

// Get handles GET /api/issues/{id} (the issue's reporter or an admin). Returns
// the issue plus its thread.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid issue id"})
		return
	}

	issue, err := h.service.GetIssue(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if !canAccessIssue(claims, issue) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	// Whether the reporter may close this themselves is a live question about
	// dispatch state, so it is answered on the read that renders the control
	// rather than cached on the row.
	if issue.ReporterID != nil && *issue.ReporterID == claims.UserID {
		if allowed, err := h.service.CanReporterConfirmFix(issue); err == nil {
			issue.CanConfirmFixed = allowed
		}
	}
	// The requester-copy boundary: a non-admin reader never sees admin-facing
	// resolution diagnostics.
	if !auth.HasPermission(claims.Role, auth.PermissionRemediationManage) {
		applyRequesterCopy(issue)
	}

	// An admin opening the thread marks the issue read (clears the unread dot);
	// the reporter viewing their own issue must NOT. Reflect it in this payload
	// too so the caller sees the new state immediately.
	if auth.HasPermission(claims.Role, auth.PermissionRemediationManage) {
		_ = h.service.MarkIssueRead(id)
		issue.Read = true
	}

	thread, err := h.service.IssueThread(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, IssueDetail{Issue: *issue, Thread: thread})
}

// Reply handles POST /api/issues/{id}/reply (the issue's reporter or an admin).
// authorKind is derived from the caller's role; body is UNTRUSTED.
func (h *Handler) Reply(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid issue id"})
		return
	}

	issue, err := h.service.GetIssue(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if !canAccessIssue(claims, issue) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	var body struct {
		Body string `json:"body"`
	}
	if err := decodeJSON(w, r, &body, false); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if body.Body == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body required"})
		return
	}

	authorKind := AuthorUser
	if auth.HasPermission(claims.Role, auth.PermissionAdmin) {
		authorKind = AuthorAdmin
	}
	if err := h.service.PostReply(id, authorKind, claims.UserID, body.Body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ConfirmFixed handles POST /api/issues/{id}/confirm-fixed — the reporter's own
// verdict that the applied fix worked, and the only way a subjective report ever
// closes without an administrator adjudicating someone else's opinion.
func (h *Handler) ConfirmFixed(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid issue id"})
		return
	}
	issue, err := h.service.GetIssue(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	// Deliberately NOT canAccessIssue: that also admits any admin, and an
	// administrator recording their verdict as the reporter's would be a lie in
	// the audit trail. Admins have /api/admin/issues/{id}/resolve.
	if issue.ReporterID == nil || *issue.ReporterID != claims.UserID {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	if err := h.service.ReporterConfirmFix(r.Context(), id, claims.UserID); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ListAdmin handles GET /api/admin/issues?status=&closed_limit=
// (PermissionRemediationManage). Open issues always come back in full; the
// closed tail is bounded, and closed_total says how much history exists.
func (h *Handler) ListAdmin(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	closedLimit := 0
	if v, err := strconv.Atoi(r.URL.Query().Get("closed_limit")); err == nil {
		closedLimit = v
	}
	issues, closedTotal, err := h.service.ListIssues(status, closedLimit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ListIssuesResponse{Issues: issues, ClosedTotal: closedTotal})
}

// Dismiss handles POST /api/admin/issues/{id}/dismiss (PermissionRemediationManage).
func (h *Handler) Dismiss(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid issue id"})
		return
	}
	if err := h.service.DismissIssue(id); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ResolveIssue handles POST /api/admin/issues/{id}/resolve. This is a human
// completion with an explicit resolved/wont_fix disposition and required audit
// note; it is deliberately distinct from dismissal.
func (h *Handler) ResolveIssue(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid issue id"})
		return
	}
	var body AdminIssueResolutionRequest
	if err := decodeJSON(w, r, &body, false); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	issue, err := h.service.ResolveIssueByAdmin(r.Context(), claims.UserID, id, body.Disposition, body.Note)
	if err != nil {
		if errors.Is(err, ErrIssueCompletionConflict) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, issue)
}

// ListActions handles GET /api/admin/agent-actions?status=proposed
// (PermissionRemediationManage). Default (no status) returns the approval queue
// (proposed). Each row carries the issue title + kind + rationale + params.
func (h *Handler) ListActions(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = ActionProposed
	}
	actions, err := h.service.ListActions(status)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ListActionsResponse{Actions: actions})
}

// GetAction returns one durable action outcome. It lets clients reconcile an
// approval request whose HTTP response was lost without risking a second
// execution attempt.
func (h *Handler) GetAction(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid action id"})
		return
	}
	action, err := h.service.GetAction(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, action)
}

// GetIssueActivity returns permanent action/run history for one issue.
func (h *Handler) GetIssueActivity(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid issue id"})
		return
	}
	activity, err := h.service.GetIssueActivity(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, activity)
}

// ApproveAction handles POST /api/admin/agent-actions/{id}/approve
// (PermissionRemediationManage). Body {override?, remember?}: override
// optionally edits the params; remember additionally arms a standing
// auto-approval rule for this action's (problem, fix, facet) triple.
func (h *Handler) ApproveAction(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid action id"})
		return
	}
	var body ActionDecision
	if err := decodeJSON(w, r, &body, true); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	var action *AgentAction
	if body.Remember {
		action, err = h.service.ApproveActionRemembering(claims.UserID, id, body.Override)
	} else {
		action, err = h.service.ApproveAction(claims.UserID, id, body.Override)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, action)
}

// BatchApproveActions handles POST /api/admin/agent-actions/approve-batch
// (PermissionRemediationManage). Body {ids}: the explicit proposals the admin
// reviewed. Items are decided sequentially by the same core as single
// approve, and the response is HTTP 200 with a per-item verdict — one
// recovering download must not fail the admin's whole gesture.
func (h *Handler) BatchApproveActions(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var body BatchApproveRequest
	if err := decodeJSON(w, r, &body, false); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if len(body.IDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ids is required"})
		return
	}
	if len(body.IDs) > batchApproveMaxIDs {
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": fmt.Sprintf("at most %d ids per request", batchApproveMaxIDs)})
		return
	}
	writeJSON(w, http.StatusOK, BatchApproveResponse{Results: h.service.ApproveActions(claims.UserID, body.IDs)})
}

// ListApprovalRules handles GET /api/admin/agent-approval-rules
// (PermissionRemediationManage): every standing auto-approval rule with its
// status, counters, and fixed display label.
func (h *Handler) ListApprovalRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.service.ListApprovalRules()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ListApprovalRulesResponse{Rules: rules})
}

// PauseApprovalRule handles POST /api/admin/agent-approval-rules/{id}/pause
// (PermissionRemediationManage). Idempotent; returns the updated rule.
func (h *Handler) PauseApprovalRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid rule id"})
		return
	}
	rule, err := h.service.PauseApprovalRule(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

// ResumeApprovalRule handles POST /api/admin/agent-approval-rules/{id}/resume
// (PermissionRemediationManage). Idempotent; returns the updated rule.
func (h *Handler) ResumeApprovalRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid rule id"})
		return
	}
	rule, err := h.service.ResumeApprovalRule(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

// DeleteApprovalRule handles DELETE /api/admin/agent-approval-rules/{id}
// (PermissionRemediationManage). History keeps its attribution via
// agent_actions.auto_rule_id.
func (h *Handler) DeleteApprovalRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid rule id"})
		return
	}
	if err := h.service.DeleteApprovalRule(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// DenyAction handles POST /api/admin/agent-actions/{id}/deny
// (PermissionRemediationManage). Body {note}. A denial resumes the investigation
// (issue back to investigating), not a terminal failure.
func (h *Handler) DenyAction(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid action id"})
		return
	}
	var body ActionDenyRequest
	if err := decodeJSON(w, r, &body, true); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	action, err := h.service.DenyAction(claims.UserID, id, body.Note)
	if err != nil {
		if errors.Is(err, ErrActionDecisionConflict) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, action)
}

// GetRun handles GET /api/admin/agent-runs/{id} (PermissionRemediationManage):
// the run row plus its ordered audit steps.
func (h *Handler) GetRun(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid run id"})
		return
	}
	detail, err := h.service.GetRunDetail(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// GetSettings handles GET /api/admin/remediation-settings (PermissionRemediationManage).
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.service.Settings())
}

// UpdateSettings handles PUT /api/admin/remediation-settings (PermissionRemediationManage).
// Returns the normalized stored settings.
func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var settings Settings
	if err := decodeJSON(w, r, &settings, false); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	current := h.service.Settings()
	settings.ModelOverride = strings.TrimSpace(settings.ModelOverride)
	// The provider binding is server-owned metadata. Ignore a client attempt to
	// rewrite it unless a changed model is successfully tested below.
	requestedBinding := strings.TrimSpace(settings.ModelOverrideProvider)
	settings.ModelOverrideProvider = current.ModelOverrideProvider
	modelChanged := settings.ModelOverride != current.ModelOverride
	bindingChanged := settings.ModelOverride != "" && requestedBinding != current.ModelOverrideProvider
	if settings.ModelOverride == "" {
		settings.ModelOverrideProvider = ""
	} else if len(settings.ModelOverride) > maxModelOverrideLength {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "remediation model override is too long"})
		return
	} else if modelChanged || bindingChanged {
		if h.validateSharedModelOverride == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "AI model validation is unavailable"})
			return
		}
		provider, err := h.validateSharedModelOverride(r.Context(), settings.ModelOverride)
		if err != nil {
			log.Printf("remediation model validation failed provider=%q: %s", provider, ai.AIValidationDiagnostic(err))
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": ai.AIValidationUserMessage(err)})
			return
		}
		settings.ModelOverrideProvider = provider
	}
	saved, err := h.service.SetSettings(settings)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

// canAccessIssue allows an admin, or the issue's own reporter, to view/reply.
func canAccessIssue(claims *auth.Claims, issue *Issue) bool {
	if auth.HasPermission(claims.Role, auth.PermissionAdmin) {
		return true
	}
	return issue.ReporterID != nil && *issue.ReporterID == claims.UserID
}

// decodeJSON bounds remediation request bodies, rejects unknown/trailing data,
// and optionally tolerates an empty body for decision endpoints.
func decodeJSON(w http.ResponseWriter, r *http.Request, v interface{}, optional bool) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRemediationRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		if optional && errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain exactly one JSON value")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
