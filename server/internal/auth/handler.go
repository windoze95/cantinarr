package auth

import (
	"encoding/json"
	"errors"
	"net/http"

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

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.Username == "" || req.Password == "" || req.InviteCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username, password, and invite_code required"})
		return
	}

	resp, err := h.service.Register(req.Username, req.Password, req.InviteCode)
	if err != nil {
		switch {
		case errors.Is(err, ErrInviteRequired), errors.Is(err, ErrInviteExpired):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, ErrUserExists):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "username already taken"})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		}
		return
	}

	writeJSON(w, http.StatusCreated, resp)
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

func (h *Handler) CreateInvite(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	resp, err := h.service.CreateInvite(claims.UserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create invite"})
		return
	}

	writeJSON(w, http.StatusCreated, resp)
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
		"id":       user.ID,
		"username": user.Username,
		"role":     user.Role,
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
