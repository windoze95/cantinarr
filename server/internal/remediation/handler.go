package remediation

import (
	"context"
	"encoding/json"
	"errors"
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
	if req.TmdbID == 0 && req.TvdbID == 0 {
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

// ListAdmin handles GET /api/admin/issues?status= (PermissionRemediationManage).
func (h *Handler) ListAdmin(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	issues, err := h.service.ListIssues(status)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ListIssuesResponse{Issues: issues})
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
// (PermissionRemediationManage). Body {override?} optionally edits the params.
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
	action, err := h.service.ApproveAction(claims.UserID, id, body.Override)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, action)
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
