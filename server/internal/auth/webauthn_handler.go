package auth

import (
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (h *Handler) BeginPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	options, sessionID, err := h.service.BeginPasskeyRegistration(claims.UserID, r)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to begin registration"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"options":    options,
		"session_id": sessionID,
	})
}

func (h *Handler) FinishPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	// The request body contains the WebAuthn credential creation response
	// plus our session_id and credential_name in the query params or a wrapper.
	// We need to read session_id and credential_name from query params,
	// then pass the raw body as the HTTP request for webauthn to parse.
	sessionID := r.URL.Query().Get("session_id")
	credentialName := r.URL.Query().Get("credential_name")

	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id required"})
		return
	}

	err := h.service.FinishPasskeyRegistration(claims.UserID, sessionID, credentialName, r)
	if err != nil {
		log.Printf("FinishPasskeyRegistration error: %v", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"status": "registered"})
}

func (h *Handler) BeginPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	options, sessionID, err := h.service.BeginPasskeyLogin(r)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to begin login"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"options":    options,
		"session_id": sessionID,
	})
}

func (h *Handler) FinishPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		// Try reading from JSON body wrapper
		// For login finish, the body IS the assertion response
		// session_id comes from query param
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id query parameter required"})
		return
	}

	resp, err := h.service.FinishPasskeyLogin(sessionID, r)
	if err != nil {
		log.Printf("FinishPasskeyLogin error: %v", err)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "passkey authentication failed"})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) ListPasskeys(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	passkeys, err := h.service.ListPasskeys(claims.UserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list passkeys"})
		return
	}

	writeJSON(w, http.StatusOK, passkeys)
}

func (h *Handler) DeletePasskey(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	credentialID := chi.URLParam(r, "credentialID")
	if credentialID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "credential ID required"})
		return
	}

	err := h.service.DeletePasskey(claims.UserID, credentialID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "passkey not found"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
