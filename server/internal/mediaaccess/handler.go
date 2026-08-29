package mediaaccess

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
)

const (
	minPasswordLength = 8
	maxPasswordLength = 1024
	maxBodyBytes      = 1 << 20
)

// Handler serves the user-facing and admin media-server account routes.
// Every error body is fixed text: nothing a media server says is echoed.
type Handler struct {
	svc    *Service
	logger *slog.Logger
}

// NewHandler creates the handler.
func NewHandler(svc *Service, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{svc: svc, logger: logger}
}

// List answers GET /api/media-servers: the caller's granted media servers
// and their account on each.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	views, err := h.svc.ListForUser(r.Context(), claims.UserID)
	if err != nil {
		h.logger.Error("mediaaccess: list for user", "err", err, "user_id", claims.UserID)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "temporarily unavailable, retry shortly"})
		return
	}
	if views == nil {
		views = []ServerView{}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, views)
}

// CreateAccount answers POST /api/media-servers/{instanceID}/account. The
// body carries a password (account servers: create an account) or an email
// (invite servers: send the invite), never both. The 403 is the same bytes
// for an unknown instance and an ungranted one.
func (h *Handler) CreateAccount(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	instanceID := chi.URLParam(r, "instanceID")
	var body struct {
		Password string `json:"password"`
		Email    string `json:"email"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	body.Email = strings.TrimSpace(body.Email)
	switch {
	case body.Email != "" && body.Password != "":
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "send a password or an email, not both"})
		return
	case body.Email != "":
		h.requestInvite(w, r, claims.UserID, instanceID, body.Email)
		return
	}
	if len(body.Password) < minPasswordLength {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("password must be at least %d characters", minPasswordLength)})
		return
	}
	if len(body.Password) > maxPasswordLength {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password is too long"})
		return
	}

	created, err := h.svc.CreateAccount(r.Context(), claims.UserID, instanceID, body.Password)
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, created)
	case errors.Is(err, ErrNameTaken):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "that name is already taken on this server; ask your admin to link it to you", "code": "name_taken"})
	case errors.Is(err, ErrInvalidName):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "your Cantinarr username can't be used as a name on this server; ask your admin to link an account for you", "code": "invalid_name"})
	case errors.Is(err, ErrWrongKind):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "this server invites by email; share the email your invite should go to", "code": "wrong_kind"})
	default:
		h.writeCreateError(w, err, claims.UserID, instanceID)
	}
}

// requestInvite is the invite-server half of CreateAccount.
func (h *Handler) requestInvite(w http.ResponseWriter, r *http.Request, userID int64, instanceID, email string) {
	if len(email) > 254 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "enter a valid email address"})
		return
	}
	created, err := h.svc.RequestInvite(r.Context(), userID, instanceID, email)
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, created)
	case errors.Is(err, ErrInvalidEmail):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "enter a valid email address", "code": "invalid_email"})
	case errors.Is(err, ErrNameTaken):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "that email already has access through another account; ask your admin", "code": "name_taken"})
	case errors.Is(err, ErrWrongKind):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "this server takes a password, not an email", "code": "wrong_kind"})
	default:
		h.writeCreateError(w, err, userID, instanceID)
	}
}

// writeCreateError maps the outcomes CreateAccount and RequestInvite share.
func (h *Handler) writeCreateError(w http.ResponseWriter, err error, userID int64, instanceID string) {
	switch {
	case errors.Is(err, ErrNotAvailable), errors.Is(err, ErrUserNotFound):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "that server is not available to you"})
	case errors.Is(err, ErrAccountExists):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "you already have an account on this server", "code": "account_exists"})
	case errors.Is(err, ErrConfigInvalid):
		h.logger.Warn("mediaaccess: create account refused: stored media server config is unreadable", "user_id", userID, "instance_id", instanceID)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "temporarily unavailable, retry shortly"})
	case errors.Is(err, ErrUpstream):
		h.logger.Warn("mediaaccess: create account failed upstream", "err", err, "user_id", userID, "instance_id", instanceID)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "couldn't create the account right now; try again later"})
	default:
		h.logger.Error("mediaaccess: create account", "err", err, "user_id", userID, "instance_id", instanceID)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "temporarily unavailable, retry shortly"})
	}
}

// ListAccounts answers GET /api/admin/media-servers/accounts.
func (h *Handler) ListAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := h.svc.ListAccounts()
	if err != nil {
		h.logger.Error("mediaaccess: list accounts", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list accounts"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, accounts)
}

type remoteUserResponse struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	IsAdministrator bool   `json:"is_administrator"`
	IsDisabled      bool   `json:"is_disabled"`
	Pending         bool   `json:"pending"`
}

// RemoteUsers answers GET /api/admin/media-servers/{instanceID}/users.
func (h *Handler) RemoteUsers(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "instanceID")
	users, err := h.svc.RemoteUsers(r.Context(), instanceID)
	switch {
	case err == nil:
	case errors.Is(err, ErrInstanceNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "instance not found"})
		return
	case errors.Is(err, ErrNotMediaServer):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a media server instance"})
		return
	case errors.Is(err, ErrUpstream):
		h.logger.Warn("mediaaccess: list remote users", "err", err, "instance_id", instanceID)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not reach the media server"})
		return
	default:
		h.logger.Error("mediaaccess: list remote users", "err", err, "instance_id", instanceID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list accounts"})
		return
	}
	out := make([]remoteUserResponse, 0, len(users))
	for _, u := range users {
		out = append(out, remoteUserResponse{ID: u.ID, Name: u.Name, IsAdministrator: u.IsAdministrator, IsDisabled: u.IsDisabled, Pending: u.Pending})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func userAndInstance(w http.ResponseWriter, r *http.Request) (int64, string, bool) {
	userID, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user ID"})
		return 0, "", false
	}
	return userID, chi.URLParam(r, "instanceID"), true
}

// LinkAccount answers PUT /api/admin/users/{userID}/media-servers/{instanceID}/account.
func (h *Handler) LinkAccount(w http.ResponseWriter, r *http.Request) {
	userID, instanceID, ok := userAndInstance(w, r)
	if !ok {
		return
	}
	var body struct {
		RemoteUserID string `json:"remote_user_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	body.RemoteUserID = strings.TrimSpace(body.RemoteUserID)
	if body.RemoteUserID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "remote_user_id required"})
		return
	}
	account, err := h.svc.LinkAccount(r.Context(), userID, instanceID, body.RemoteUserID)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, account)
	case errors.Is(err, ErrUserNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
	case errors.Is(err, ErrInstanceNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "instance not found"})
	case errors.Is(err, ErrNotMediaServer):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a media server instance"})
	case errors.Is(err, ErrRemoteUserNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "remote user not found"})
	case errors.Is(err, ErrAdministratorAccount):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "administrator accounts can't be linked"})
	case errors.Is(err, ErrAccountExists):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "user already has an account on this server", "code": "account_exists"})
	case errors.Is(err, ErrRemoteAlreadyLinked):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "that account is already linked to another user", "code": "remote_already_linked"})
	case errors.Is(err, ErrUpstream):
		h.logger.Warn("mediaaccess: link account", "err", err, "user_id", userID, "instance_id", instanceID)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not reach the media server"})
	default:
		h.logger.Error("mediaaccess: link account", "err", err, "user_id", userID, "instance_id", instanceID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to link account"})
	}
}

// UnlinkAccount answers DELETE /api/admin/users/{userID}/media-servers/{instanceID}/account.
func (h *Handler) UnlinkAccount(w http.ResponseWriter, r *http.Request) {
	userID, instanceID, ok := userAndInstance(w, r)
	if !ok {
		return
	}
	err := h.svc.UnlinkAccount(userID, instanceID)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrNoAccount):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no linked account"})
	default:
		h.logger.Error("mediaaccess: unlink account", "err", err, "user_id", userID, "instance_id", instanceID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to unlink account"})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
