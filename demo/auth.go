// auth.go — /api/auth/* per srv-auth.md: status, login, refresh, connect,
// me, password, plex-email, setup (409), passkey stubs. Part of Stage A.
package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// registerAuth mounts /auth/* on the PUBLIC /api router; it applies
// requireAuth itself to the endpoints that need a session.
func registerAuth(r chi.Router) {
	r.Route("/auth", func(r chi.Router) {
		r.Get("/status", authStatusHandler)
		r.Post("/login", authLoginHandler)
		r.Post("/refresh", authRefreshHandler)
		r.Post("/connect", authConnectHandler)
		r.Post("/setup", authSetupHandler)

		// Passkeys are disabled demo-wide (auth/status reports
		// webauthn_available:false + native_passkeys all-false); the begin
		// ceremonies answer the 403 shape the app already handles.
		r.Post("/passkey/login/begin", authPasskeysDisabledHandler)
		r.Post("/passkey/setup/begin", authPasskeysDisabledHandler)

		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			r.Get("/me", authMeHandler)
			r.Post("/logout", authLogoutHandler)
			r.Post("/password", authPasswordHandler)
			r.Post("/plex-email", authPlexEmailHandler)
			r.Get("/passkeys", authListPasskeysHandler)
			r.Post("/passkey/register/begin", authPasskeysDisabledHandler)
		})
	})
}

// GET /api/auth/status — public. needs_setup MUST be false (absence defaults
// to true and the app shows the setup wizard).
func authStatusHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"needs_setup":        false,
		"webauthn_available": false,
		"native_passkeys": map[string]any{
			"apple_configured":       false,
			"android_configured":     false,
			"windows_origin_trusted": false,
		},
	})
}

type authSessionRequest struct {
	Username     string `json:"username"`
	Password     string `json:"password"`
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	DeviceName   string `json:"device_name"`
	HardwareID   string `json:"hardware_id"`
	Platform     string `json:"platform"`
}

// POST /api/auth/login — public.
func authLoginHandler(w http.ResponseWriter, r *http.Request) {
	var req authSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Username == "" || req.Password == "" {
		writeErr(w, http.StatusBadRequest, "username and password required")
		return
	}
	u := userByName(req.Username)
	// Unknown user, wrong password, and password sign-in disabled are
	// deliberately indistinguishable. Admins may always use passwords.
	if u == nil || u.Password != req.Password ||
		(u.Role != roleAdmin && (!u.PasswordEnabled || !u.HasPassword)) {
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	d := deviceUpsert(u.ID, req.HardwareID, req.DeviceName, req.Platform)
	writeJSON(w, http.StatusOK, issueSession(u, d))
}

// POST /api/auth/refresh — public; the refresh token is the credential.
// 401 ONLY for genuine revocation (the app erases its session on 401).
func authRefreshHandler(w http.ResponseWriter, r *http.Request) {
	var req authSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.RefreshToken == "" {
		writeErr(w, http.StatusBadRequest, "refresh_token required")
		return
	}
	resp, err := refreshSession(req.RefreshToken)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// POST /api/auth/connect — public. Redeems a connect link; error strings are
// shown verbatim in the app.
func authConnectHandler(w http.ResponseWriter, r *http.Request) {
	var req authSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Token == "" || req.DeviceName == "" {
		writeErr(w, http.StatusBadRequest, "token and device_name required")
		return
	}
	u, err := redeemConnectToken(req.Token)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	d := deviceUpsert(u.ID, req.HardwareID, req.DeviceName, req.Platform)
	writeJSON(w, http.StatusOK, issueSession(u, d))
}

// POST /api/auth/logout — self-serve sign-out: revokes the calling device's
// own session, mirroring the real server (idempotent; an already-gone device
// is the goal state; 401 without a device claim). requireAuth stores only the
// user in the context, so the device id is re-read from the bearer token it
// already validated.
func authLogoutHandler(w http.ResponseWriter, r *http.Request) {
	claims, err := parseAccessClaims(
		strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if err != nil || claims.DeviceID == "" {
		writeErr(w, http.StatusUnauthorized, "no device session")
		return
	}
	revokeDevice(claims.DeviceID)
	writeJSON(w, http.StatusOK, map[string]any{"status": "signed_out"})
}

// POST /api/auth/setup — the demo reports needs_setup:false, so setup always
// answers the exact 409.
func authSetupHandler(w http.ResponseWriter, _ *http.Request) {
	writeErr(w, http.StatusConflict, "setup has already been completed")
}

// GET /api/auth/me — every key always present; plex_invited_at is an
// explicit null when unset (unlike the TokenResponse user, which omits it).
func authMeHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	out := map[string]any{
		"id":               u.ID,
		"username":         u.Username,
		"role":             u.Role,
		"permissions":      permissionsFor(u.Role),
		"has_password":     u.HasPassword,
		"password_enabled": u.PasswordEnabled,
		"passkey_enabled":  u.PasskeyEnabled,
		"plex_email":       u.PlexEmail,
		"plex_invited_at":  nil,
	}
	if at := userPlexInvitedAt(u); at != nil {
		out["plex_invited_at"] = *at
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /api/auth/password — set/replace own password (no current-password
// check; this is the connect-link password-reset path).
func authPasswordHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Password) < 8 {
		writeErr(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	if u.Role != roleAdmin && !u.PasswordEnabled {
		writeErr(w, http.StatusForbidden, "password sign-in is not enabled for your account")
		return
	}
	setUserPassword(u.ID, req.Password)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// POST /api/auth/plex-email — stores the shared email; a changed address
// clears plex_invited_at and notifies admins (plex_access_request WS).
// Resubmitting the same address is an idempotent success.
func authPlexEmailHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !validPlexEmail(req.Email) {
		writeErr(w, http.StatusBadRequest, "enter a valid email address")
		return
	}
	changed, ok := setUserPlexEmail(u.ID, req.Email)
	if !ok {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	if changed {
		wsToAdmins(evtPlexAccessRequest, map[string]any{
			"user_id":      u.ID,
			"username":     u.Username,
			"invite_state": "",
		})
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// validPlexEmail mirrors the real server's shape-only validation: non-empty,
// ≤254 chars, no whitespace, contains "@" not at either end.
func validPlexEmail(email string) bool {
	if email == "" || len(email) > 254 {
		return false
	}
	if strings.ContainsAny(email, " \t\r\n") {
		return false
	}
	at := strings.Index(email, "@")
	return at > 0 && at < len(email)-1
}

// GET /api/auth/passkeys — always the empty array (never null).
func authListPasskeysHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, []any{})
}

// Passkey ceremonies are disabled demo-wide.
func authPasskeysDisabledHandler(w http.ResponseWriter, _ *http.Request) {
	writeErr(w, http.StatusForbidden, "passkeys are not enabled for your account")
}
