package request

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.MediaType == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "media_type required"})
		return
	}
	// Books are keyed by the Readarr foreignBookId (no tmdb_id); everything else
	// is keyed by tmdb_id.
	if req.MediaType == "book" {
		req.ForeignID = strings.TrimSpace(req.ForeignID)
		req.Title = strings.TrimSpace(req.Title)
		req.SearchTerm = strings.TrimSpace(req.SearchTerm)
		if req.ForeignID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "foreign_id required for book requests"})
			return
		}
		if req.Title == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "title required for book requests"})
			return
		}
		if req.BookFormat != "" && !validBookFormat(req.BookFormat) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "book_format must be ebook, audiobook, or both"})
			return
		}
	} else if req.TmdbID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tmdb_id required"})
		return
	}

	resp, err := h.service.CreateMediaRequest(claims.UserID, &req)
	if err != nil {
		// Service errors are host-free by construction, so the one line that
		// makes a failed create diagnosable from the container log is safe.
		log.Printf("request: create %s request failed: %v", req.MediaType, err)
		writeJSON(w, bookRequestErrorStatus(err), map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) GetStatus(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	tmdbIDStr := chi.URLParam(r, "tmdb_id")
	tmdbID, err := strconv.Atoi(tmdbIDStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tmdb_id"})
		return
	}

	mediaType := r.URL.Query().Get("media_type")
	if mediaType == "" {
		mediaType = "movie" // default
	}

	resp, err := h.service.GetUserStatus(claims.UserID, tmdbID, mediaType)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// GetBookStatus reports the current user's request state for a book, keyed by
// the Readarr foreignBookId (books have no tmdb_id).
func (h *Handler) GetBookStatus(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	foreignID := strings.TrimSpace(r.URL.Query().Get("foreign_id"))
	if foreignID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "foreign_id required"})
		return
	}
	resp, err := h.service.GetUserBookStatusForInstance(claims.UserID, foreignID, r.URL.Query().Get("instance_id"))
	if err != nil {
		writeJSON(w, bookRequestErrorStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetBookLibrary returns the current user's reduced, cached Chaptarr library
// digest (one entry per title with per-format ownership), so the app can mark
// search results as already-owned. A user with no Chaptarr access gets an empty
// digest, not an error.
func (h *Handler) GetBookLibrary(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	digest, err := h.service.GetBookLibraryDigestForInstance(claims.UserID, r.URL.Query().Get("instance_id"))
	if err != nil {
		writeJSON(w, bookRequestErrorStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, digest)
}

// GetBookRecent returns the newest book-file imports for the Chaptarr instance
// this user may see, so the Books tab can show what recently landed.
//
// A user with no Chaptarr access gets an empty list, not an error: the row is
// simply absent for them.
func (h *Handler) GetBookRecent(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit := recentBooksDefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > recentBooksMaxItems {
		limit = recentBooksMaxItems
	}
	digest, err := h.service.GetRecentBooksForInstance(claims.UserID, r.URL.Query().Get("instance_id"), limit)
	if err != nil {
		writeJSON(w, bookRequestErrorStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, digest)
}

func bookRequestErrorStatus(err error) int {
	switch {
	case errors.Is(err, ErrChaptarrInstanceForbidden):
		return http.StatusForbidden
	case errors.Is(err, ErrChaptarrInstanceInvalid):
		return http.StatusBadRequest
	case errors.Is(err, ErrBookFormatUnresolved):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	requests, err := h.service.GetRequests(claims.UserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch requests"})
		return
	}

	if requests == nil {
		requests = []RequestLog{}
	}

	writeJSON(w, http.StatusOK, requests)
}

// Options reports the option set the current user may choose for a request.
func (h *Handler) Options(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	mediaType := r.URL.Query().Get("media_type")
	if mediaType == "" {
		mediaType = "movie"
	}

	isAdmin := auth.HasPermission(claims.Role, auth.PermissionAdmin)
	opts, err := h.service.GetRequestOptions(claims.UserID, isAdmin, mediaType)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, opts)
}

// ListPending returns the admin approval queue.
func (h *Handler) ListPending(w http.ResponseWriter, r *http.Request) {
	pending, err := h.service.ListPending()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch pending requests"})
		return
	}
	if pending == nil {
		pending = []PendingRequest{}
	}
	writeJSON(w, http.StatusOK, pending)
}

// ListWaiting returns the requests the server owns and is retrying itself.
// Informational only: these rows carry no decision, which is exactly why they
// are served apart from the approval queue rather than mixed into it.
func (h *Handler) ListWaiting(w http.ResponseWriter, r *http.Request) {
	waiting, err := h.service.ListWaiting()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch waiting requests"})
		return
	}
	if waiting == nil {
		waiting = []PendingRequest{}
	}
	writeJSON(w, http.StatusOK, waiting)
}

// Approve fulfills a pending request, optionally overriding its options.
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request id"})
		return
	}
	var override DecisionOverride
	if err := decodeOptional(r, &override); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	resp, err := h.service.ApproveRequest(claims.UserID, id, &override)
	if err != nil {
		log.Printf("request: approve request %d failed: %v", id, err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// Wait resumes the watch on a demoted author-import book request: the admin's
// "try again", the opposite verb to closing it. The service replays the add
// once and either completes the request (the author landed), re-parks it for
// the sweep to watch, or surfaces the real failure.
func (h *Handler) Wait(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request id"})
		return
	}
	resp, err := h.service.ExtendBookWait(claims.UserID, id)
	if err != nil {
		log.Printf("request: resume wait on request %d failed: %v", id, err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// Deny rejects a pending request with an optional reason.
func (h *Handler) Deny(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request id"})
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := decodeOptional(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if err := h.service.DenyRequest(claims.UserID, id, body.Reason); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// GetSettings returns the global request defaults + arr quality profiles.
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.service.GetAdminSettings())
}

// UpdateSettings persists the global request defaults.
func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var settings GlobalSettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if err := h.service.SetGlobalSettings(settings); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, h.service.GetAdminSettings())
}

// GetUserSettings returns one user's per-user request overrides.
func (h *Handler) GetUserSettings(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user id"})
		return
	}
	dto, err := h.service.GetUserSettingsDTO(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

// UpdateUserSettings persists one user's per-user request overrides.
func (h *Handler) UpdateUserSettings(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user id"})
		return
	}
	var dto UserSettingsDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if err := h.service.SetUserSettings(id, dto); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, dto)
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
