package push

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
)

// Handler serves the device push-token endpoints. A nil client disables
// gateway registration (the local row is still stored), so the handler works
// even when push is not configured.
type Handler struct {
	db     *sql.DB
	client *Client
	logger *slog.Logger
}

// NewHandler builds the push-token endpoint handler.
func NewHandler(db *sql.DB, client *Client, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{db: db, client: client, logger: logger}
}

// RegisterTokenRequest is the POST /api/devices/push-token body.
type RegisterTokenRequest struct {
	DeviceID  string `json:"device_id"`
	APNSToken string `json:"apns_token"`
	Platform  string `json:"platform"`
}

// Register upserts the caller's APNs token for one of their own devices and
// registers it with the gateway. Gateway errors are logged but do not fail the
// request once the local row is stored.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var req RegisterTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.DeviceID == "" || req.APNSToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "device_id and apns_token required"})
		return
	}
	platform := req.Platform
	if platform == "" {
		platform = "ios"
	}

	// The device must exist and belong to the caller. This both authorizes the
	// write and guarantees the push_tokens FK (device_id -> devices.id) holds.
	if !h.deviceBelongsToUser(req.DeviceID, claims.UserID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "device not found"})
		return
	}

	if err := h.upsertToken(claims.UserID, req.DeviceID, platform, req.APNSToken); err != nil {
		h.logger.Error("push: upsert token", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save token"})
		return
	}

	// Register with the gateway. Failures here are non-fatal: the token is
	// stored locally and will be retried on the next registration.
	if h.client != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := h.client.RegisterDevice(ctx, claims.UserID, req.DeviceID, platform, req.APNSToken); err != nil {
			h.logger.Error("push: register device with gateway", "err", err, "device_id", req.DeviceID)
		}
	}

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// Delete removes the caller's push token for one of their own devices and
// deregisters it from the gateway.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	deviceID := chi.URLParam(r, "deviceID")
	if deviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "device_id required"})
		return
	}

	// Ownership-checked delete: only remove a row that belongs to the caller.
	res, err := h.db.Exec(
		"DELETE FROM push_tokens WHERE device_id = ? AND user_id = ?",
		deviceID, claims.UserID,
	)
	if err != nil {
		h.logger.Error("push: delete token", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete token"})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "token not found"})
		return
	}

	if h.client != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := h.client.DeleteDevice(ctx, deviceID); err != nil {
			h.logger.Error("push: delete device from gateway", "err", err, "device_id", deviceID)
		}
	}

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// deviceBelongsToUser reports whether device_id names a (non-revoked) device
// owned by the user.
func (h *Handler) deviceBelongsToUser(deviceID string, userID int64) bool {
	var owner int64
	err := h.db.QueryRow(
		"SELECT user_id FROM devices WHERE id = ? AND revoked_at IS NULL",
		deviceID,
	).Scan(&owner)
	return err == nil && owner == userID
}

// upsertToken stores (or refreshes) the APNs token for a device. The UNIQUE
// (device_id) constraint makes re-registration replace the prior token.
func (h *Handler) upsertToken(userID int64, deviceID, platform, token string) error {
	id, err := newID()
	if err != nil {
		return err
	}
	_, err = h.db.Exec(
		`INSERT INTO push_tokens (id, device_id, user_id, platform, token, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(device_id) DO UPDATE SET
			user_id = excluded.user_id,
			platform = excluded.platform,
			token = excluded.token,
			last_seen_at = CURRENT_TIMESTAMP`,
		id, deviceID, userID, platform, token,
	)
	return err
}

// newID returns a random hex id for a push_tokens row.
func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
