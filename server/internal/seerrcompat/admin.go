package seerrcompat

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
)

// keyResponse is what an administrator sees of the key under Settings: the
// key itself, because it is a credential Cantinarr issues for the admin to
// paste elsewhere (as Radarr and Seerr show theirs), not one it holds for
// another service. Only the admin routes answer with it; the compat surface
// never echoes it.
type keyResponse struct {
	Configured bool       `json:"configured"`
	APIKey     string     `json:"api_key,omitempty"`
	IssuedBy   string     `json:"issued_by,omitempty"`
	CreatedAt  *time.Time `json:"created_at,omitempty"`
}

func (h *Handler) keyPayload() (keyResponse, error) {
	issued, err := h.settings.SeerrAPIKey()
	if err != nil {
		return keyResponse{}, err
	}
	if !issued.Configured() {
		return keyResponse{}, nil
	}
	out := keyResponse{Configured: true, APIKey: issued.Key, CreatedAt: &issued.CreatedAt}
	var username string
	if err := h.db.QueryRow("SELECT username FROM users WHERE id = ?", issued.UserID).Scan(&username); err == nil {
		out.IssuedBy = username
	} else if errors.Is(err, sql.ErrNoRows) {
		out.IssuedBy = "a deleted administrator"
	}
	return out, nil
}

func writeAdminError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// AdminKey serves GET /api/admin/seerr-api (the current key), POST (issue a
// key, replacing any earlier one at once) and DELETE (revoke). Requires the
// instances:manage permission, applied by the router.
func (h *Handler) AdminKey(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
		claims := auth.GetClaims(r.Context())
		if claims == nil {
			writeAdminError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if _, err := h.settings.IssueSeerrAPIKey(claims.UserID); err != nil {
			log.Printf("seerr-api: issue key: %v", err)
			writeAdminError(w, http.StatusInternalServerError, "the API key could not be issued")
			return
		}
		log.Printf("seerr-api: %s issued the Seerr-compatible API key", claims.Username)
	case http.MethodDelete:
		if err := h.settings.RevokeSeerrAPIKey(); err != nil {
			log.Printf("seerr-api: revoke key: %v", err)
			writeAdminError(w, http.StatusInternalServerError, "the API key could not be revoked")
			return
		}
		if claims := auth.GetClaims(r.Context()); claims != nil {
			log.Printf("seerr-api: %s revoked the Seerr-compatible API key", claims.Username)
		}
	default:
		writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	payload, err := h.keyPayload()
	if err != nil {
		log.Printf("seerr-api: read key: %v", err)
		writeAdminError(w, http.StatusInternalServerError, "the stored API key could not be read")
		return
	}
	writeJSON(w, http.StatusOK, payload)
}
