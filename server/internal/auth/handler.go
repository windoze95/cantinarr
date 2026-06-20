package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.Username == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username and password required"})
		return
	}

	resp, err := h.service.Login(req.Username, req.Password)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req RefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.RefreshToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "refresh_token required"})
		return
	}

	resp, err := h.service.Refresh(req.RefreshToken)
	if err != nil {
		if errors.Is(err, ErrDeviceRevoked) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "device has been revoked"})
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid refresh token"})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) HandleCreateConnectToken(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var req CreateConnectTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.Name == "" || req.ServerURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and server_url required"})
		return
	}

	resp, err := h.service.CreateConnectToken(claims.UserID, req.Name, req.ServerURL)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create connect token"})
		return
	}

	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) HandleRedeemConnectToken(w http.ResponseWriter, r *http.Request) {
	var req RedeemConnectTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.Token == "" || req.DeviceName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token and device_name required"})
		return
	}

	resp, err := h.service.RedeemConnectToken(req.Token, req.DeviceName)
	if err != nil {
		switch {
		case errors.Is(err, ErrTokenNotFound):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "connect token not found"})
		case errors.Is(err, ErrTokenRedeemed):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "this link has already been used"})
		case errors.Is(err, ErrTokenExpired):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "this link has expired"})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		}
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) HandleListDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := h.service.ListDevices()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list devices"})
		return
	}
	writeJSON(w, http.StatusOK, devices)
}

func (h *Handler) HandleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	deviceID := chi.URLParam(r, "deviceID")
	if deviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "device ID required"})
		return
	}

	err := h.service.RevokeDevice(deviceID)
	if err != nil {
		if errors.Is(err, ErrDeviceNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "device not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to revoke device"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func (h *Handler) HandleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.service.ListUsers()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list users"})
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (h *Handler) HandleUpdateUserRole(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user ID"})
		return
	}

	var req UpdateUserRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	user, err := h.service.UpdateUserRole(userID, req.Role)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidRole):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid role"})
		case errors.Is(err, ErrUserNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		case errors.Is(err, ErrLastAdmin):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot demote the last admin"})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update role"})
		}
		return
	}

	writeJSON(w, http.StatusOK, user)
}

func (h *Handler) HandleDeleteUser(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	userID, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user ID"})
		return
	}

	if err := h.service.DeleteUser(claims.UserID, userID); err != nil {
		switch {
		case errors.Is(err, ErrUserNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		case errors.Is(err, ErrCannotDeleteSelf):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "you cannot delete your own account"})
		case errors.Is(err, ErrLastAdmin):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot delete the last admin"})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete user"})
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) AuthStatus(w http.ResponseWriter, r *http.Request) {
	resp := AuthStatusResponse{
		NeedsSetup:        !h.service.IsSetupComplete(),
		WebAuthnAvailable: isSecureContext(r),
		NativePasskeys:    h.service.nativePasskeyStatusFromRequest(r),
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) HandleSetup(w http.ResponseWriter, r *http.Request) {
	var req SetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.Username == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username and password required"})
		return
	}

	if len(req.Password) < 8 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
		return
	}

	resp, err := h.service.Setup(req.Username, req.Password)
	if err != nil {
		if errors.Is(err, ErrSetupAlreadyComplete) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "setup has already been completed"})
			return
		}
		if errors.Is(err, ErrUserExists) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "username already taken"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	user, err := h.service.GetUser(claims.UserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":           user.ID,
		"username":     user.Username,
		"role":         user.Role,
		"permissions":  user.Permissions,
		"has_password": user.PasswordHash != "",
	})
}

// SetPassword creates or replaces the authenticated user's password. It lets a
// user who signed in via a connect link or passkey add a password so they can
// log in (and authorize MCP clients) on deployments without HTTPS, where
// passkeys are unavailable.
func (h *Handler) SetPassword(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var req SetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if err := h.service.SetPassword(claims.UserID, req.Password); err != nil {
		switch {
		case errors.Is(err, ErrPasswordTooShort):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
		case errors.Is(err, ErrUserNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
