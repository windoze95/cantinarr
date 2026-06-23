package remediation

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
)

// Handler exposes the Wave-1 issue-reporting REST surface. It clones the shape
// of request.Handler (claims gate, JSON helpers, decodeOptional).
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// Create handles POST /api/issues (PermissionMediaRequest). The reporter is the
// authenticated user; reason/title are UNTRUSTED.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var req CreateIssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.MediaType == "" || req.Category == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "media_type and category required"})
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
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
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

// GetSettings handles GET /api/admin/remediation-settings (PermissionRemediationManage).
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.service.Settings())
}

// UpdateSettings handles PUT /api/admin/remediation-settings (PermissionRemediationManage).
// Returns the normalized stored settings.
func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var settings Settings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
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

// decodeOptional decodes a JSON body, tolerating an empty body.
func decodeOptional(r *http.Request, v interface{}) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
