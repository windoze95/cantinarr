package contentpolicy

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// Handler serves the admin routes: a user's policy and the certification
// schemes the editor offers. Authorization (users:manage) is the router's.
type Handler struct {
	svc *Service
}

// NewHandler wraps the service.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func userIDParam(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	return id, err == nil && id > 0
}

// GetUserPolicy answers the policy, or 404 when the account is not a kids
// account.
func (h *Handler) GetUserPolicy(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDParam(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user ID"})
		return
	}
	p, err := h.svc.Store.Get(userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load content policy"})
		return
	}
	if p == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not a kids account"})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// PutUserPolicy creates or replaces the policy, turning the account into a
// kids account. The body is validated against the live schemes first, so a
// cap the region does not know is a 400 rather than a silent "hide
// everything".
func (h *Handler) PutUserPolicy(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDParam(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user ID"})
		return
	}
	var p Policy
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if err := h.svc.Validate(r.Context(), &p); err != nil {
		var ve *ValidationError
		switch {
		case errors.As(err, &ve):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": ve.Message})
		case errors.Is(err, ErrUnavailable):
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ratings lists are temporarily unavailable, retry shortly"})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to validate content policy"})
		}
		return
	}
	if err := h.svc.Store.Set(userID, p); err != nil {
		switch {
		case errors.Is(err, ErrUserNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		case errors.Is(err, ErrAdminAccount):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "admin accounts cannot be kids accounts"})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save content policy"})
		}
		return
	}
	stored, err := h.svc.Store.Get(userID)
	if err != nil || stored == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load content policy"})
		return
	}
	writeJSON(w, http.StatusOK, stored)
}

// DeleteUserPolicy turns the kids account off. Clearing an account that has
// none is fine: the state the admin asked for is the state they get.
func (h *Handler) DeleteUserPolicy(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDParam(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user ID"})
		return
	}
	if err := h.svc.Store.Clear(userID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to clear content policy"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}

// Certifications lists every region's movie and TV schemes for the editor.
func (h *Handler) Certifications(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.svc.Certifications(r.Context()))
}
