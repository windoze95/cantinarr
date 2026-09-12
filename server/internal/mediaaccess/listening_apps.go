package mediaaccess

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/instance"
)

func (s *Service) listeningAppPreferences(userID int64) (instance.ListeningApps, error) {
	var prefs instance.ListeningApps
	err := s.db.QueryRow("SELECT ios, android FROM listening_app_preferences WHERE user_id=?", userID).Scan(&prefs.IOS, &prefs.Android)
	if errors.Is(err, sql.ErrNoRows) {
		return prefs, nil
	}
	return prefs, err
}

// ListeningAppPreferences reads or replaces only the authenticated caller's
// choices. There is no target user ID, and admins use the same personal route.
func (h *Handler) ListeningAppPreferences(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var prefs instance.ListeningApps
	var err error
	if r.Method == http.MethodPut {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
		decoder.DisallowUnknownFields()
		var submitted *instance.ListeningApps
		if decoder.Decode(&submitted) != nil || submitted == nil || decoder.Decode(new(any)) != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid listening app preferences"})
			return
		}
		prefs = *submitted
		if err = prefs.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		_, err = h.svc.db.Exec(`INSERT INTO listening_app_preferences (user_id, ios, android) VALUES (?, ?, ?)
			ON CONFLICT(user_id) DO UPDATE SET ios=excluded.ios, android=excluded.android`, claims.UserID, prefs.IOS, prefs.Android)
	} else {
		prefs, err = h.svc.listeningAppPreferences(claims.UserID)
	}
	if err != nil {
		h.logger.Error("mediaaccess: listening app preferences", "err", err, "user_id", claims.UserID)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "could not save or load listening app preferences"})
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}
