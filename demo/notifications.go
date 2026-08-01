// notifications.go — per-user notification preferences, push-token
// registration, and the self test push. Any signed-in user; no admin gate.
// Spec: srv-realtime.md §2, app-admin.md §13.
package main

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"
)

// notifPrefsRow is the flat 9-boolean preferences object (Go push.Prefs JSON
// tags). Field order matches the real server's serialization. The tenth
// category, agent_autoapproval_paused, deliberately shares the
// agent_action_pending preference and never appears in this JSON.
type notifPrefsRow struct {
	RequestDecision    bool `json:"request_decision"`
	RequestPending     bool `json:"request_pending"`
	NewMovie           bool `json:"new_movie"`
	NewEpisode         bool `json:"new_episode"`
	NewBook            bool `json:"new_book"`
	IssueCreated       bool `json:"issue_created"`
	AgentActionPending bool `json:"agent_action_pending"`
	PlexAccessRequest  bool `json:"plex_access_request"`
	PlexInviteSent     bool `json:"plex_invite_sent"`
}

// notifDefaultPrefs — request_decision off, everything else on.
func notifDefaultPrefs() notifPrefsRow {
	return notifPrefsRow{
		RequestDecision:    false,
		RequestPending:     true,
		NewMovie:           true,
		NewEpisode:         true,
		NewBook:            true,
		IssueCreated:       true,
		AgentActionPending: true,
		PlexAccessRequest:  true,
		PlexInviteSent:     true,
	}
}

// notifPushTokenRec is one registered push token (device id keyed).
type notifPushTokenRec struct {
	UserID   int
	Token    string
	Platform string
}

var (
	notifMu         sync.Mutex
	notifPrefsStore = map[int]notifPrefsRow{}        // user id -> stored prefs row
	notifPushTokens = map[string]notifPushTokenRec{} // device id -> token rec
)

// registerNotifications mounts the notification-preference and push-token
// surface on the authenticated /api router.
func registerNotifications(r chi.Router) {
	r.Get("/notifications/preferences", notifPrefsGetHandler)
	r.Put("/notifications/preferences", notifPrefsPutHandler)
	r.Post("/notifications/test", notifTestHandler)
	r.Post("/devices/push-token", notifPushTokenRegisterHandler)
	r.Delete("/devices/push-token/{deviceID}", notifPushTokenDeleteHandler)
}

// notifPrefsGetHandler — GET /api/notifications/preferences. A user with no
// stored row gets the defaults.
func notifPrefsGetHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	notifMu.Lock()
	row, ok := notifPrefsStore[u.ID]
	notifMu.Unlock()
	if !ok {
		row = notifDefaultPrefs()
	}
	writeJSON(w, http.StatusOK, row)
}

// notifPrefsPutHandler — PUT /api/notifications/preferences. Full-row
// replace: unknown fields are ignored, missing fields become false (Go zero
// value). Echoes the stored row.
func notifPrefsPutHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var row notifPrefsRow
	if err := json.NewDecoder(r.Body).Decode(&row); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	notifMu.Lock()
	notifPrefsStore[u.ID] = row
	notifMu.Unlock()
	writeJSON(w, http.StatusOK, row)
}

// notifTestHandler — POST /api/notifications/test. The demo runs no push
// gateway (setup-status reports the push item unconfigured), so the truthful
// answer is always 503.
func notifTestHandler(w http.ResponseWriter, _ *http.Request) {
	writeErr(w, http.StatusServiceUnavailable, "push not configured")
}

// notifDeviceOwnedBy reports whether deviceID is a live (non-revoked) device
// owned by userID. Scans the shared device list via its accessor — domain
// files never touch stateMu directly.
func notifDeviceOwnedBy(deviceID string, userID int) bool {
	for _, row := range devicesJSON() {
		if row["id"] == deviceID && row["user_id"] == userID {
			return true
		}
	}
	return false
}

// notifPushTokenRegisterHandler — POST /api/devices/push-token. Registers
// this device's APNs token; upsert keyed on device_id. The device must be an
// existing, non-revoked device owned by the caller.
func notifPushTokenRegisterHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var req struct {
		DeviceID  string `json:"device_id"`
		APNSToken string `json:"apns_token"`
		Platform  string `json:"platform"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.DeviceID == "" || req.APNSToken == "" {
		writeErr(w, http.StatusBadRequest, "device_id and apns_token required")
		return
	}
	if req.Platform == "" {
		req.Platform = "ios"
	}
	if !notifDeviceOwnedBy(req.DeviceID, u.ID) {
		writeErr(w, http.StatusForbidden, "device not found")
		return
	}
	notifMu.Lock()
	notifPushTokens[req.DeviceID] = notifPushTokenRec{
		UserID: u.ID, Token: req.APNSToken, Platform: req.Platform,
	}
	notifMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// notifPushTokenDeleteHandler — DELETE /api/devices/push-token/{deviceID}.
// Ownership-checked delete; 404 when no row matches (deviceID, caller).
func notifPushTokenDeleteHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	deviceID := chi.URLParam(r, "deviceID")
	notifMu.Lock()
	rec, ok := notifPushTokens[deviceID]
	if ok && rec.UserID == u.ID {
		delete(notifPushTokens, deviceID)
	}
	notifMu.Unlock()
	if !ok || rec.UserID != u.ID {
		writeErr(w, http.StatusNotFound, "token not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
