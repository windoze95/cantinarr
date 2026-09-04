package auth

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// AccessRequestHook runs after a user shares a new or changed Plex email.
// Wired by main to the media-access service, which sends the invites the
// user's grants owe (or auto-approves them) and notifies admins with the
// outcome either way. A hook (not a notifier) so auth stays free of both
// push and media-server dependencies.
type AccessRequestHook func(userID int64, username string)

// UserDeleteHook is asked, before a user is deleted, to prepare whatever must
// happen once they are gone (switching off their media-server accounts). It
// returns the commit step, which the handler runs only after the delete
// succeeded — the delete can still refuse (self-delete, last admin), and a
// refused delete must leave the user untouched everywhere.
type UserDeleteHook func(userID int64) (committed func())

type Handler struct {
	service           *Service
	accessRequestHook AccessRequestHook
	userDeleteHook    UserDeleteHook
	externalURL       func() string
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// SetAccessRequestHook wires the Plex access-request side effect after
// construction: the media-access service gets its notifier later in startup
// than the auth handler (the composite needs the WebSocket hub).
func (h *Handler) SetAccessRequestHook(hook AccessRequestHook) {
	h.accessRequestHook = hook
}

// SetUserDeleteHook wires the media-server side effect of deleting a user
// (see UserDeleteHook). Same late-binding shape as SetAccessRequestHook.
func (h *Handler) SetUserDeleteHook(hook UserDeleteHook) {
	h.userDeleteHook = hook
}

// SetExternalURLSource wires the admin-configured external address after
// construction (the settings service is built later in startup, mirroring
// SetAccessRequestHook). When it returns a non-empty origin, outward links —
// connect invites and passkey setup links — are built from it instead of the
// requesting client's own address, so links work for invitees who cannot
// reach the admin's LAN.
func (h *Handler) SetExternalURLSource(source func() string) {
	h.externalURL = source
}

// resolvedExternalURL returns the configured external address, or "" when
// unset or unwired.
func (h *Handler) resolvedExternalURL() string {
	if h.externalURL == nil {
		return ""
	}
	return h.externalURL()
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

	resp, err := h.service.Login(req.Username, req.Password, req.DeviceName, req.HardwareID)
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
		// 401 is reserved for a genuine rejection: clients erase their stored
		// session on it. Every other failure (DB hiccup, signing fault) is a
		// 503 so the client keeps the token and retries later.
		switch {
		case errors.Is(err, ErrDeviceRevoked):
			log.Printf("auth: refresh 401 from %s: device revoked (client will clear its session)", r.RemoteAddr)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "device has been revoked"})
		case errors.Is(err, ErrInvalidCredentials):
			log.Printf("auth: refresh 401 from %s: refresh token unknown (client will clear its session)", r.RemoteAddr)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid refresh token"})
		default:
			log.Printf("auth: refresh answered 503 (client will retry, session kept): %v", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "temporarily unavailable, retry shortly"})
		}
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

	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}

	// The admin-configured external address wins over the client-sent URL:
	// the client can only offer the address its own connection uses, which on
	// a LAN is unreachable for the invitee this link is for.
	serverURL, originSource := req.ServerURL, originSourceApp
	if ext := h.resolvedExternalURL(); ext != "" {
		serverURL, originSource = ext, originSourceExternalAddress
	}
	if serverURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "server_url required"})
		return
	}

	resp, err := h.service.CreateConnectToken(claims.UserID, req.Name, serverURL)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create connect token"})
		return
	}
	resp.OriginSource = originSource

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

	resp, err := h.service.RedeemConnectToken(req.Token, req.DeviceName, req.HardwareID)
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

// HandleLogout is the self-serve counterpart to HandleRevokeDevice: it revokes
// only the calling device's own session, identified by the token's device
// claim. Idempotent — an already-revoked or missing device is the goal state.
func (h *Handler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil || claims.DeviceID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no device session"})
		return
	}

	if err := h.service.RevokeDevice(claims.DeviceID); err != nil && !errors.Is(err, ErrDeviceNotFound) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to sign out"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "signed_out"})
}

func (h *Handler) HandleListUsers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
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
		case errors.Is(err, ErrChildCannotBeAdmin):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "turn off the kids account first"})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update role"})
		}
		return
	}

	writeJSON(w, http.StatusOK, user)
}

// HandleUpdateUserAuthMethods toggles a user's password / passkey sign-in
// ability. Both default off, so admins use this to grant a credential — notably
// a password, which is the prerequisite for using MCP on a non-HTTPS server.
func (h *Handler) HandleUpdateUserAuthMethods(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user ID"})
		return
	}

	var req UpdateUserAuthMethodsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.PasswordEnabled == nil && req.PasskeyEnabled == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no changes provided"})
		return
	}

	user, err := h.service.SetUserAuthMethods(userID, req.PasswordEnabled, req.PasskeyEnabled)
	if err != nil {
		switch {
		case errors.Is(err, ErrUserNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		case errors.Is(err, ErrCannotModifyAdmin):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "admins always keep password and passkey sign-in"})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update sign-in methods"})
		}
		return
	}

	writeJSON(w, http.StatusOK, user)
}

// HandleUpdateUserAIAccess changes only the user's grant to the shared AI
// profile; personal provider credentials are owned and managed by that user.
func (h *Handler) HandleUpdateUserAIAccess(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	userID, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil || userID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user ID"})
		return
	}
	var req UpdateUserAIAccessRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil || req.SharedEnabled == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "shared_ai_enabled is required"})
		return
	}
	user, err := h.service.SetUserAISharedAccess(userID, *req.SharedEnabled)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		} else {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update AI access"})
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

	// Snapshot what the delete must switch off while the rows still exist;
	// nothing is touched until the delete has actually gone through.
	var committed func()
	if h.userDeleteHook != nil {
		committed = h.userDeleteHook(userID)
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
	if committed != nil {
		committed()
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

	resp, err := h.service.Setup(req.Username, req.Password, req.DeviceName, req.HardwareID)
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
		"id":               user.ID,
		"username":         user.Username,
		"role":             user.Role,
		"permissions":      user.Permissions,
		"has_password":     user.PasswordHash != "",
		"password_enabled": user.PasswordEnabled,
		"passkey_enabled":  user.PasskeyEnabled,
		"plex_email":       user.PlexEmail,
		"plex_invited_at":  user.PlexInvitedAt,
		"child":            user.Child,
		"content_limits":   user.ContentLimits,
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
		case errors.Is(err, ErrPasswordNotAllowed):
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "password sign-in is not enabled for your account"})
		case errors.Is(err, ErrUserNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// SetPlexEmail stores the email the authenticated user wants their Plex invite
// sent to, and — when the address is new or changed — notifies admins so they
// can send the invite from Plex.
func (h *Handler) SetPlexEmail(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var req SetPlexEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	changed, err := h.service.SetPlexEmail(claims.UserID, req.Email)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidPlexEmail):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "enter a valid email address"})
		case errors.Is(err, ErrUserNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		}
		return
	}

	if changed && h.accessRequestHook != nil {
		h.accessRequestHook(claims.UserID, claims.Username)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
