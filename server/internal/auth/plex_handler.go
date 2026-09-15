package auth

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

func plexHTTPError(w http.ResponseWriter, err error) {
	oidcHeaders(w)
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, ErrPlexUnavailable), errors.Is(err, ErrAuthUnavailable):
		status = http.StatusServiceUnavailable
	case errors.Is(err, ErrPlexDenied), errors.Is(err, ErrPlexDisabled), errors.Is(err, ErrSSORequired), errors.Is(err, ErrPermissionDenied), errors.Is(err, ErrInvalidCredentials), errors.Is(err, ErrDeviceRevoked):
		status = http.StatusForbidden
	case errors.Is(err, ErrPlexConflict):
		status = http.StatusConflict
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
func (h *Handler) PlexBegin(w http.ResponseWriter, r *http.Request)     { h.plexBegin(w, r, "login") }
func (h *Handler) PlexLinkBegin(w http.ResponseWriter, r *http.Request) { h.plexBegin(w, r, "link") }
func (h *Handler) plexBegin(w http.ResponseWriter, r *http.Request, purpose string) {
	var req oidcBeginRequest
	if !oidcDecode(w, r, &req) {
		return
	}
	out, err := h.service.beginPlex(r.Context(), req, purpose, GetClaims(r.Context()))
	if err != nil {
		plexHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type plexCheckRequest struct {
	Flow     string `json:"flow"`
	Verifier string `json:"verifier"`
	Code     string `json:"code"`
}

func (h *Handler) PlexCheck(w http.ResponseWriter, r *http.Request) {
	var req plexCheckRequest
	if !oidcDecode(w, r, &req) {
		return
	}
	out, err := h.service.checkPlex(r.Context(), req.Flow, req.Verifier)
	if err != nil {
		plexHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (h *Handler) PlexExchange(w http.ResponseWriter, r *http.Request) {
	var req plexCheckRequest
	if !oidcDecode(w, r, &req) {
		return
	}
	out, err := h.service.exchangePlex(r.Context(), req.Flow, req.Code, req.Verifier)
	if err != nil {
		plexHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (h *Handler) PlexCancel(w http.ResponseWriter, r *http.Request) {
	var req plexCheckRequest
	if !oidcDecode(w, r, &req) {
		return
	}
	if err := h.service.cancelPlex(req.Flow, req.Verifier); err != nil {
		plexHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}
func (h *Handler) PlexConfig(w http.ResponseWriter, r *http.Request) {
	oidcHeaders(w)
	c, err := h.service.plexConfiguration()
	if err != nil {
		plexHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c.PlexConfig)
}
func (h *Handler) PlexConfigSave(w http.ResponseWriter, r *http.Request) {
	var req PlexConfig
	if !oidcDecode(w, r, &req) {
		return
	}
	c, err := h.service.savePlexConfig(req, GetClaims(r.Context()))
	if err != nil {
		plexHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}
func (h *Handler) PlexCandidates(w http.ResponseWriter, r *http.Request) {
	oidcHeaders(w)
	candidates, err := h.service.plexCandidates(r.Context())
	if err != nil {
		plexHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidates": candidates})
}
func (h *Handler) PlexConfirm(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mappings []PlexMapping `json:"mappings"`
	}
	if !oidcDecode(w, r, &req) {
		return
	}
	if err := h.service.confirmPlexMappings(r.Context(), GetClaims(r.Context()), req.Mappings); err != nil {
		plexHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "confirmed"})
}
func plexIdentityUser(r *http.Request) (int64, error) {
	actor := GetClaims(r.Context())
	if actor == nil {
		return 0, ErrInvalidCredentials
	}
	if raw := chi.URLParam(r, "userID"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return 0, ErrUserNotFound
		}
		return id, nil
	}
	return actor.UserID, nil
}
func (h *Handler) PlexIdentities(w http.ResponseWriter, r *http.Request) {
	oidcHeaders(w)
	id, err := plexIdentityUser(r)
	if err != nil {
		plexHTTPError(w, err)
		return
	}
	identities, err := h.service.plexIdentities(id)
	if err != nil {
		plexHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"identities": identities})
}
func (h *Handler) PlexUnlink(w http.ResponseWriter, r *http.Request) {
	oidcHeaders(w)
	id, err := plexIdentityUser(r)
	if err != nil {
		plexHTTPError(w, err)
		return
	}
	if err = h.service.unlinkPlex(GetClaims(r.Context()), id); err != nil {
		plexHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "unlinked"})
}
func (h *OAuthHandler) BeginPlex(w http.ResponseWriter, r *http.Request) {
	var req oidcBeginRequest
	if !oidcDecode(w, r, &req) {
		return
	}
	if req.Client != "mcp" {
		plexHTTPError(w, ErrPlexFlow)
		return
	}
	req.OAuth = oidcOAuthValues(req.OAuth)
	check := r.Clone(r.Context())
	check.Form = req.OAuth
	if _, err := h.validateAuthorizeRequest(check); err != nil {
		plexHTTPError(w, err)
		return
	}
	out, err := h.service.beginPlex(r.Context(), req, "mcp", nil)
	if err != nil {
		plexHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
