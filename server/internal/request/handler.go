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
	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/secrets"
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
		if req.MediaType == "book" {
			status, body := bookRequestErrorResponse(err, req.BookFormat)
			log.Printf(
				"request: book request failed user_id=%d instance_id=%q format=%q code=%q: %v",
				claims.UserID,
				req.InstanceID,
				req.BookFormat,
				body["code"],
				secrets.RedactError(err),
			)
			writeJSON(w, status, body)
			return
		}
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
		status, body := bookReadErrorResponse(err, "status")
		log.Printf(
			"request: book status failed user_id=%d instance_id=%q code=%q: %v",
			claims.UserID,
			r.URL.Query().Get("instance_id"),
			body["code"],
			secrets.RedactError(err),
		)
		writeJSON(w, status, body)
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
		status, body := bookReadErrorResponse(err, "library")
		log.Printf(
			"request: book library failed user_id=%d instance_id=%q code=%q: %v",
			claims.UserID,
			r.URL.Query().Get("instance_id"),
			body["code"],
			secrets.RedactError(err),
		)
		writeJSON(w, status, body)
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
	case errors.Is(err, ErrBookSelectionInvalid):
		return http.StatusBadRequest
	case errors.Is(err, ErrBookFormatUnresolved):
		return http.StatusConflict
	case errors.Is(err, ErrBookMatchNotFound):
		return http.StatusConflict
	case bookUpstreamAuthFailure(err):
		return http.StatusServiceUnavailable
	case errors.Is(err, ErrBookEditionUnavailable),
		errors.Is(err, ErrBookMultiWorkUnsupported),
		errors.Is(err, ErrBookMutationRejected),
		errors.Is(err, ErrBookSearchRejected):
		return http.StatusUnprocessableEntity
	case errors.Is(err, ErrBookCatalogPending),
		errors.Is(err, ErrBookOutcomePending),
		errors.Is(err, ErrBookConfigurationInvalid):
		return http.StatusServiceUnavailable
	case errors.Is(err, ErrBookMutationUnverified),
		errors.Is(err, ErrBookSearchUnconfirmed):
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

// bookRequestErrorResponse is the requester-facing boundary for Chaptarr
// failures. Service errors may contain useful step detail for server logs and
// tests, but upstream URLs, response bodies, and arr terminology must not leak
// through the public API. Stable codes also let the app explain a known
// rejection without mistaking an interrupted response for a safe retry.
func bookRequestErrorResponse(err error, format string) (int, map[string]string) {
	code := "book_request_failed"
	message := "This book could not be requested. Try again."

	switch {
	case errors.Is(err, ErrChaptarrInstanceForbidden):
		code = "book_instance_forbidden"
		message = "This book library is not available to your account."
	case errors.Is(err, ErrChaptarrInstanceInvalid):
		code = "book_instance_invalid"
		message = "This book library is no longer available. Refresh and try again."
	case errors.Is(err, ErrBookSelectionInvalid):
		code = "book_selection_invalid"
		message = "This book version choice is invalid. Refresh the catalog and try again."
	case errors.Is(err, ErrBookEditionUnavailable):
		code = "book_edition_unavailable"
		switch format {
		case BookFormatEbook:
			message = "No eBook edition is available for this title."
		case BookFormatAudiobook:
			message = "No audiobook edition is available for this title."
		default:
			message = "One or more requested formats have no usable edition for this title."
		}
	case errors.Is(err, ErrBookFormatUnresolved):
		code = "book_format_unresolved"
		message = "This edition is not identified as an eBook or audiobook."
	case errors.Is(err, ErrBookMatchNotFound):
		code = "book_match_not_found"
		message = "Cantinarr couldn’t verify this book match. Try again."
	case errors.Is(err, ErrBookMultiWorkUnsupported):
		code = "book_multi_work_unsupported"
		message = "This result contains multiple books. Choose an individual title instead."
	case errors.Is(err, ErrBookConfigurationInvalid):
		code = "book_configuration_invalid"
		message = "An admin needs to check this book library’s profiles and folders."
	case bookUpstreamAuthFailure(err):
		code = "book_connection_invalid"
		message = "An admin needs to check this book library’s connection."
	case errors.Is(err, ErrBookCatalogPending):
		code = "book_catalog_pending"
		message = "The book library is still preparing this title. Try again in a moment."
	case errors.Is(err, ErrBookOutcomePending):
		code = "book_outcome_pending"
		message = "The book library is still confirming this request. Cantinarr will keep checking it."
	case errors.Is(err, ErrBookMutationRejected):
		code = "book_request_rejected"
		message = "The book library rejected this title or edition. Refresh the catalog and try again, or ask an admin to check the book library."
	case errors.Is(err, ErrBookMutationUnverified):
		code = "book_request_unverified"
		message = "Cantinarr could not verify the selected edition, so no download search was started. Try again or ask an admin to check the book library."
	case errors.Is(err, ErrBookSearchRejected):
		code = "book_search_rejected"
		message = "The book was prepared, but the book library rejected its download search. Ask an admin to check the book library."
	case errors.Is(err, ErrBookSearchUnconfirmed):
		code = "book_search_unconfirmed"
		message = "The book was prepared, but its download search could not be confirmed. Try again or ask an admin to check the book library."
	}

	return bookRequestErrorStatus(err), map[string]string{
		"code":  code,
		"error": message,
	}
}

func bookUpstreamAuthFailure(err error) bool {
	var statusErr *chaptarr.HTTPStatusError
	return errors.As(err, &statusErr) &&
		(statusErr.StatusCode == http.StatusUnauthorized || statusErr.StatusCode == http.StatusForbidden)
}

func isBookRequestError(err error) bool {
	return errors.Is(err, ErrChaptarrInstanceForbidden) ||
		errors.Is(err, ErrChaptarrInstanceInvalid) ||
		errors.Is(err, ErrBookSelectionInvalid) ||
		errors.Is(err, ErrBookEditionUnavailable) ||
		errors.Is(err, ErrBookFormatUnresolved) ||
		errors.Is(err, ErrBookMatchNotFound) ||
		errors.Is(err, ErrBookMultiWorkUnsupported) ||
		errors.Is(err, ErrBookConfigurationInvalid) ||
		errors.Is(err, ErrBookCatalogPending) ||
		errors.Is(err, ErrBookOutcomePending) ||
		errors.Is(err, ErrBookMutationRejected) ||
		errors.Is(err, ErrBookMutationUnverified) ||
		errors.Is(err, ErrBookSearchRejected) ||
		errors.Is(err, ErrBookSearchUnconfirmed) ||
		bookUpstreamAuthFailure(err)
}

// bookReadErrorResponse keeps Chaptarr URLs, API paths, and implementation
// wording behind the server boundary. Known request-state errors retain their
// stable code; unexpected read failures get an endpoint-specific retry message.
func bookReadErrorResponse(err error, operation string) (int, map[string]string) {
	if isBookRequestError(err) {
		return bookRequestErrorResponse(err, "")
	}
	code := "book_status_unavailable"
	message := "The book status could not be checked. Try again."
	if operation == "library" {
		code = "book_library_unavailable"
		message = "The book library could not be loaded. Try again."
	}
	return http.StatusBadGateway, map[string]string{
		"code":  code,
		"error": message,
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
		if isBookRequestError(err) {
			status, body := bookRequestErrorResponse(err, "")
			log.Printf(
				"request: book approval failed admin_user_id=%d request_id=%d code=%q: %v",
				claims.UserID,
				id,
				body["code"],
				secrets.RedactError(err),
			)
			writeJSON(w, status, body)
			return
		}
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
