package mediaavailability

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

// Handler exposes only inventory administration. It has no acquisition or
// provider-account mutation dependency.
type Handler struct {
	svc     *Service
	logger  *slog.Logger
	changed func()
}

func NewHandler(svc *Service, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{svc: svc, logger: logger}
}

func (h *Handler) SetConfigChangedObserver(fn func()) { h.changed = fn }
func (h *Handler) Invalidate()                        { h.svc.Invalidate("") }

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (h *Handler) admin(w http.ResponseWriter, r *http.Request) bool {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return false
	}
	var role string
	if err := h.svc.db.QueryRowContext(r.Context(), "SELECT role FROM users WHERE id=?", claims.UserID).Scan(&role); err != nil {
		respond(w, http.StatusServiceUnavailable, map[string]string{"error": "Could not verify administrator access."})
		return false
	}
	if role != "admin" {
		respond(w, http.StatusForbidden, map[string]string{"error": "Administrator access required."})
		return false
	}
	return true
}

func (h *Handler) failure(w http.ResponseWriter, err error) {
	status, message, code := http.StatusServiceUnavailable, "Could not verify library availability. Retry shortly.", "availability_unavailable"
	switch {
	case errors.Is(err, ErrHouseholdAdmin):
		status, message, code = http.StatusForbidden, "Administrator access required.", "administrator_access_changed"
	case errors.Is(err, ErrHouseholdUser):
		status, message, code = http.StatusNotFound, "User not found.", "user_not_found"
	case errors.Is(err, ErrHouseholdSelection):
		status, message, code = http.StatusBadRequest, "Select libraries currently enabled for availability scanning.", "invalid_libraries"
	case errors.Is(err, ErrHouseholdLinked):
		status, message, code = http.StatusConflict, "This user's linked account controls their access. Remove it before choosing library-only access.", "account_linked"
	case errors.Is(err, ErrHouseholdChanged):
		status, message, code = http.StatusConflict, "Library access changed. Refresh and review the selection.", "access_changed"
	case errors.Is(err, ErrScanDisabled):
		status, message, code = http.StatusConflict, "Enable availability scanning and select libraries first.", "scan_disabled"
	case errors.Is(err, ErrUnavailable):
		status, message, code = http.StatusServiceUnavailable, "The media server's libraries could not be verified.", "availability_unavailable"
	}
	respond(w, status, map[string]string{"error": message, "code": code})
}

func (h *Handler) Libraries(w http.ResponseWriter, r *http.Request) {
	if !h.admin(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	libraries, err := h.svc.ScanLibraries(ctx, chi.URLParam(r, "instanceID"))
	if err != nil {
		h.failure(w, err)
		return
	}
	if libraries == nil {
		libraries = []mediaserver.Library{}
	}
	if !h.admin(w, r) {
		return
	}
	respond(w, http.StatusOK, map[string]any{"libraries": libraries})
}

func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	if !h.admin(w, r) {
		return
	}
	status, err := h.svc.Status(chi.URLParam(r, "instanceID"))
	if err != nil {
		h.failure(w, err)
		return
	}
	respond(w, http.StatusOK, status)
}

func (h *Handler) Scan(w http.ResponseWriter, r *http.Request) {
	if !h.admin(w, r) {
		return
	}
	id := chi.URLParam(r, "instanceID")
	status, err := h.svc.Status(id)
	if err != nil {
		h.failure(w, err)
		return
	}
	if status.State == "disabled" {
		h.failure(w, ErrScanDisabled)
		return
	}
	if status.State != "running" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			if err := h.svc.Scan(ctx, id); err != nil && !errors.Is(err, ErrScanRunning) {
				h.logger.Warn("media availability scan could not finish", "instance_id", id)
			}
			if h.changed != nil {
				h.changed()
			}
		}()
	}
	status.State = "running"
	respond(w, http.StatusAccepted, status)
}

func householdUserParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil || id <= 0 {
		respond(w, http.StatusBadRequest, map[string]string{"error": "Invalid user ID."})
		return 0, false
	}
	return id, true
}

func (h *Handler) Household(w http.ResponseWriter, r *http.Request) {
	if !h.admin(w, r) {
		return
	}
	id, ok := householdUserParam(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPut {
		var body struct {
			Associations *[]HouseholdAssociation `json:"associations"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil || body.Associations == nil {
			respond(w, http.StatusBadRequest, map[string]string{"error": "Invalid library association request."})
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			respond(w, http.StatusBadRequest, map[string]string{"error": "Invalid library association request."})
			return
		}
		if err := h.svc.SaveHouseholdAccess(r.Context(), auth.GetClaims(r.Context()).UserID, id, *body.Associations); err != nil {
			h.failure(w, err)
			return
		}
		if h.changed != nil {
			h.changed()
		}
		// Saving never depends on a second remote read succeeding.
		associations, err := h.svc.households(id)
		if err != nil {
			h.failure(w, err)
			return
		}
		respond(w, http.StatusOK, map[string]any{"associations": associations})
		return
	}
	view, err := h.svc.HouseholdAccess(r.Context(), id)
	if err != nil {
		h.failure(w, err)
		return
	}
	if !h.admin(w, r) {
		return
	}
	respond(w, http.StatusOK, view)
}
