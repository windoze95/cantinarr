// users_admin.go — admin user management: connect-token minting, device
// listing/revocation, the users list with role/auth-method/AI-access/deletion
// mutations, per-user default-instance overrides, test pushes, and one-tap
// Plex invites. Spec: srv-admin-users.md §1–12, app-admin.md §1–2.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// uaValidServiceTypes is the service-type enum accepted as keys of the
// default-instances map (srv-admin-users §10).
var uaValidServiceTypes = map[string]bool{
	serviceRadarr:       true,
	serviceSonarr:       true,
	serviceChaptarr:     true,
	serviceSabnzbd:      true,
	serviceQbittorrent:  true,
	serviceNzbget:       true,
	serviceTransmission: true,
	serviceTautulli:     true,
}

// registerUsersAdmin mounts the admin user/device management surface. Every
// route is admin-gated (users:manage in the real server; the demo's only
// admin holds it). Paths are registered individually — never a bare
// /admin/users subtree — so sibling domains can add e.g.
// /admin/users/{id}/request-settings without a mount collision.
func registerUsersAdmin(r chi.Router) {
	admin := r.With(requireAdmin)
	admin.Post("/admin/connect-token", uaConnectTokenHandler)
	admin.Get("/admin/devices", uaDevicesListHandler)
	admin.Delete("/admin/devices/{deviceID}", uaDeviceRevokeHandler)
	admin.Get("/admin/users", uaUsersListHandler)
	admin.Patch("/admin/users/{userID}", uaUserRoleHandler)
	admin.Delete("/admin/users/{userID}", uaUserDeleteHandler)
	admin.Patch("/admin/users/{userID}/auth-methods", uaAuthMethodsHandler)
	admin.Put("/admin/users/{userID}/ai-access", uaAIAccessHandler)
	admin.Post("/admin/users/{userID}/test-push", uaTestPushHandler)
	admin.Get("/admin/users/{userID}/default-instances", uaDefaultInstancesGetHandler)
	admin.Put("/admin/users/{userID}/default-instances", uaDefaultInstancesPutHandler)
	admin.Post("/admin/users/{userID}/plex-invite", uaPlexInviteHandler)
}

// uaUserID parses the {userID} path param; ok is false for non-numeric or
// non-positive values. The error MESSAGE varies per endpoint ("invalid user
// ID" vs "invalid user id"), so callers write their own.
func uaUserID(r *http.Request) (int, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "userID"))
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// ─── Connect links ──────────────────────────────────────

// uaConnectTokenHandler — POST /api/admin/connect-token. Mints a 7-day
// single-use connect link, find-or-creating the named account (which then
// shows has_pending_invite:true in the users list). Responds 201.
func uaConnectTokenHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		ServerURL string `json:"server_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.ServerURL == "" {
		writeErr(w, http.StatusBadRequest, "name and server_url required")
		return
	}
	// The demo always advertises DEMO_SERVER_URL in the link; the request's
	// server_url is validated but not echoed (contract.md, deliberate).
	link, expiresAt := mintConnectToken(req.Name)
	writeJSON(w, http.StatusCreated, map[string]any{
		"link":       link,
		"expires_at": expiresAt,
	})
}

// ─── Devices ────────────────────────────────────────────

// uaDevicesListHandler — GET /api/admin/devices. Bare array, last_seen_at
// DESC, exactly six fields per row (all six are hard-required by the app).
func uaDevicesListHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, devicesJSON())
}

// uaDeviceRevokeHandler — DELETE /api/admin/devices/{deviceID}. Revokes the
// device: it vanishes from the list, its refresh token answers 401 "device
// has been revoked", and its live access tokens die at requireAuth.
func uaDeviceRevokeHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "deviceID")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "device ID required")
		return
	}
	if !revokeDevice(id) {
		writeErr(w, http.StatusNotFound, "device not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// ─── Users list & mutations ─────────────────────────────

// uaUsersListHandler — GET /api/admin/users. Bare UserSummary array ordered
// by id ascending, Cache-Control: no-store.
func uaUsersListHandler(w http.ResponseWriter, _ *http.Request) {
	users := allUsers()
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		out = append(out, userSummaryJSON(u))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

// uaAdminCount counts current admin accounts (last-admin guards).
func uaAdminCount() int {
	n := 0
	for _, u := range allUsers() {
		if u.Role == roleAdmin {
			n++
		}
	}
	return n
}

// uaUserRoleHandler — PATCH /api/admin/users/{userID}. Changes the role and
// returns the full updated UserSummary (permissions recomputed). 409 when
// demoting the last admin.
func uaUserRoleHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := uaUserID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid user ID")
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Role != roleAdmin && req.Role != roleUser {
		writeErr(w, http.StatusBadRequest, "invalid role")
		return
	}
	u := userByID(id)
	if u == nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	if u.Role == roleAdmin && req.Role == roleUser && uaAdminCount() <= 1 {
		writeErr(w, http.StatusConflict, "cannot demote the last admin")
		return
	}
	withUser(id, func(u *DemoUser) { u.Role = req.Role })
	writeJSON(w, http.StatusOK, userSummaryJSON(u))
}

// uaUserDeleteHandler — DELETE /api/admin/users/{userID}. Removes the account
// plus devices and pending invites. 400 on self-delete, 409 for the last
// admin.
func uaUserDeleteHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := uaUserID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid user ID")
		return
	}
	if caller := userFrom(r); caller != nil && caller.ID == id {
		writeErr(w, http.StatusBadRequest, "you cannot delete your own account")
		return
	}
	switch err := deleteUser(id); err {
	case nil:
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	case errUserNotFound:
		writeErr(w, http.StatusNotFound, "user not found")
	case errLastAdmin:
		writeErr(w, http.StatusConflict, "cannot delete the last admin")
	default:
		writeErr(w, http.StatusInternalServerError, "failed to delete user")
	}
}

// uaAuthMethodsHandler — PATCH /api/admin/users/{userID}/auth-methods.
// Toggles password/passkey sign-in policy for a non-admin. Disabling password
// clears the stored password (has_password flips false); disabling passkey
// deletes passkeys. Returns the updated UserSummary.
func uaAuthMethodsHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := uaUserID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid user ID")
		return
	}
	var req struct {
		PasswordEnabled *bool `json:"password_enabled"`
		PasskeyEnabled  *bool `json:"passkey_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.PasswordEnabled == nil && req.PasskeyEnabled == nil {
		writeErr(w, http.StatusBadRequest, "no changes provided")
		return
	}
	u := userByID(id)
	if u == nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	if u.Role == roleAdmin {
		writeErr(w, http.StatusConflict, "admins always keep password and passkey sign-in")
		return
	}
	withUser(id, func(u *DemoUser) {
		if req.PasswordEnabled != nil {
			u.PasswordEnabled = *req.PasswordEnabled
			if !*req.PasswordEnabled {
				u.Password = ""
				u.HasPassword = false
			}
		}
		if req.PasskeyEnabled != nil {
			u.PasskeyEnabled = *req.PasskeyEnabled
		}
	})
	writeJSON(w, http.StatusOK, userSummaryJSON(u))
}

// uaAIAccessHandler — PUT /api/admin/users/{userID}/ai-access. Grants or
// revokes the admin-funded shared AI profile. Request key shared_ai_enabled,
// response key ai_shared_enabled — asymmetric on purpose.
func uaAIAccessHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := uaUserID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid user ID")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		SharedAIEnabled *bool `json:"shared_ai_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SharedAIEnabled == nil {
		writeErr(w, http.StatusBadRequest, "shared_ai_enabled is required")
		return
	}
	u := userByID(id)
	if u == nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	withUser(id, func(u *DemoUser) { u.AISharedEnabled = *req.SharedAIEnabled })
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, userSummaryJSON(u))
}

// uaTestPushHandler — POST /api/admin/users/{userID}/test-push. The demo has
// no push gateway and setup-status reports the push item unconfigured, so the
// truthful answer is always 503 "push not configured".
func uaTestPushHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := uaUserID(r); !ok {
		writeErr(w, http.StatusBadRequest, "invalid user id")
		return
	}
	writeErr(w, http.StatusServiceUnavailable, "push not configured")
}

// ─── Per-user default instances ─────────────────────────

// uaDefaultInstancesMap copies the user's DefaultInstances map under the
// state lock; nil when the user does not exist. Never returns a nil map for
// an existing user (JSON must be {}, not null).
func uaDefaultInstancesMap(id int) map[string]string {
	var out map[string]string
	ok := withUser(id, func(u *DemoUser) {
		out = make(map[string]string, len(u.DefaultInstances))
		for k, v := range u.DefaultInstances {
			out[k] = v
		}
	})
	if !ok {
		return nil
	}
	return out
}

// uaDefaultInstancesGetHandler — GET .../default-instances. Bare
// {service_type: instance_id} object, {} when none.
func uaDefaultInstancesGetHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := uaUserID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid user id")
		return
	}
	m := uaDefaultInstancesMap(id)
	if m == nil {
		// Unknown user has no override rows — same as none stored.
		m = map[string]string{}
	}
	writeJSON(w, http.StatusOK, m)
}

// uaDefaultInstancesPutHandler — PUT .../default-instances. Sets or clears
// per-user overrides. null/"" clears an override; clearing chaptarr revokes
// that user's Books access (chaptarr has no global default). Unknown
// service-type keys and mismatched instances are rejected before anything
// applies. Returns the full updated map.
func uaDefaultInstancesPutHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := uaUserID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid user id")
		return
	}
	var req map[string]*string
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Validate every entry up front, before any mutation.
	for key, val := range req {
		if !uaValidServiceTypes[key] {
			writeErr(w, http.StatusBadRequest, "unknown service_type: "+key)
			return
		}
		if val == nil || *val == "" {
			continue
		}
		inst := instanceByID(*val)
		if inst == nil {
			writeErr(w, http.StatusBadRequest, "instance not found: "+*val)
			return
		}
		if inst.ServiceType != key {
			writeErr(w, http.StatusBadRequest,
				fmt.Sprintf("instance %s is %q, not %q", *val, inst.ServiceType, key))
			return
		}
	}
	applied := withUser(id, func(u *DemoUser) {
		for key, val := range req {
			if val == nil || *val == "" {
				delete(u.DefaultInstances, key)
			} else {
				u.DefaultInstances[key] = *val
			}
		}
	})
	if !applied {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	writeJSON(w, http.StatusOK, uaDefaultInstancesMap(id))
}

// ─── Plex invite ────────────────────────────────────────

// uaPlexInviteHandler — POST /api/admin/users/{userID}/plex-invite. One-tap
// Plex invite for the user's shared email. Both success statuses stamp
// plex_invited_at; only a fresh "invited" pushes the plex_invite_sent event
// to that user.
func uaPlexInviteHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := uaUserID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid user ID")
		return
	}
	u := userByID(id)
	if u == nil {
		// Matches the real handler: a failed user load lands in the default
		// branch, not a 404.
		writeErr(w, http.StatusBadGateway, "Plex invite failed")
		return
	}
	if u.PlexEmail == "" {
		writeErr(w, http.StatusConflict, "user has not shared a Plex email")
		return
	}
	if !plexConfigured() {
		writeErr(w, http.StatusConflict, "Plex invites are not configured")
		return
	}
	status := "invited"
	if u.PlexInvitedAt != nil {
		// Re-inviting an already-invited account: plex.tv reports the share
		// already exists. Still success, still stamps plex_invited_at.
		status = "already_shared"
	}
	now := time.Now()
	withUser(id, func(u *DemoUser) { u.PlexInvitedAt = &now })
	if status == "invited" {
		wsToUser(id, evtPlexInviteSent, map[string]any{})
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status": status,
		"email":  u.PlexEmail,
	})
}
