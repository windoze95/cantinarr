package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

func oidcHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}
func oidcHTTPError(w http.ResponseWriter, err error) {
	oidcHeaders(w)
	status := http.StatusBadRequest
	if errors.Is(err, ErrAuthUnavailable) || errors.Is(err, ErrOIDCUnavailable) || errors.Is(err, ErrOIDCConfig) {
		status = http.StatusServiceUnavailable
	}
	if errors.Is(err, ErrSSORequired) || errors.Is(err, ErrPermissionDenied) || errors.Is(err, ErrOIDCGroups) {
		status = http.StatusForbidden
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
func oidcDecode(w http.ResponseWriter, r *http.Request, out any) bool {
	oidcHeaders(w)
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768)).Decode(out) != nil {
		oidcHTTPError(w, ErrOIDCFlow)
		return false
	}
	return true
}
func (h *Handler) OIDCBegin(w http.ResponseWriter, r *http.Request)     { h.oidcBegin(w, r, "login") }
func (h *Handler) OIDCLinkBegin(w http.ResponseWriter, r *http.Request) { h.oidcBegin(w, r, "link") }
func (h *Handler) OIDCTestBegin(w http.ResponseWriter, r *http.Request) { h.oidcBegin(w, r, "test") }
func (h *Handler) oidcBegin(w http.ResponseWriter, r *http.Request, purpose string) {
	var req oidcBeginRequest
	if !oidcDecode(w, r, &req) {
		return
	}
	result, err := h.service.beginOIDC(req, purpose, GetClaims(r.Context()))
	if err != nil {
		oidcHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (h *Handler) OIDCExchange(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code     string `json:"code"`
		Verifier string `json:"verifier"`
		Flow     string `json:"flow"`
	}
	if !oidcDecode(w, r, &req) {
		return
	}
	result, err := h.service.exchangeOIDC(req.Code, req.Verifier, req.Flow)
	if err != nil {
		oidcHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (h *Handler) OIDCConfig(w http.ResponseWriter, r *http.Request) {
	oidcHeaders(w)
	c, err := h.service.oidcConfiguration()
	if err != nil {
		oidcHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c.OIDCConfig)
}
func (h *Handler) OIDCConfigSave(w http.ResponseWriter, r *http.Request) {
	var req OIDCConfig
	if !oidcDecode(w, r, &req) {
		return
	}
	c, err := h.service.saveOIDCConfig(req, GetClaims(r.Context()))
	if err != nil {
		oidcHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}
func (h *Handler) OIDCValidate(w http.ResponseWriter, r *http.Request) {
	oidcHeaders(w)
	c, err := h.service.oidcConfiguration()
	if err != nil {
		oidcHTTPError(w, err)
		return
	}
	_, _, _, err = c.provider(r.Context())
	if err != nil {
		oidcHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "valid", "message": "Discovery succeeded. Complete Test sign-in to verify credentials and claims."})
}
func (h *Handler) OIDCIdentities(w http.ResponseWriter, r *http.Request) {
	oidcHeaders(w)
	userID := GetClaims(r.Context()).UserID
	if raw := chi.URLParam(r, "userID"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			oidcHTTPError(w, ErrUserNotFound)
			return
		}
		userID = id
	}
	identities, err := h.service.oidcIdentities(userID)
	if err != nil {
		oidcHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"identities": identities})
}
func (h *Handler) OIDCUnlink(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Issuer string `json:"issuer"`
	}
	if !oidcDecode(w, r, &req) {
		return
	}
	actor := GetClaims(r.Context())
	userID := actor.UserID
	if raw := chi.URLParam(r, "userID"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			oidcHTTPError(w, ErrUserNotFound)
			return
		}
		userID = id
	}
	if err := h.service.unlinkOIDC(actor, userID, req.Issuer); err != nil {
		oidcHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "unlinked"})
}
