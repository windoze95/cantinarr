package mediaaccess

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/instance"
)

type videoAppPreferences map[string]instance.VideoApps

func emptyVideoAppPreferences() videoAppPreferences {
	return videoAppPreferences{"plex": {}, "jellyfin": {}, "emby": {}}
}

func (s *Service) videoAppPreferences(userID int64) (videoAppPreferences, error) {
	prefs := emptyVideoAppPreferences()
	rows, err := s.db.Query("SELECT service_type, ios FROM video_app_preferences WHERE user_id=?", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var serviceType string
		var apps instance.VideoApps
		if err := rows.Scan(&serviceType, &apps.IOS); err != nil {
			return nil, err
		}
		prefs[serviceType] = apps
	}
	return prefs, rows.Err()
}

// VideoAppPreferences reads or replaces the caller's choices by service type.
// Omitting a service on PUT resets it to inheritance. Admins use the same route.
func (h *Handler) VideoAppPreferences(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	prefs := emptyVideoAppPreferences()
	var err error
	if r.Method == http.MethodPut {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
		decoder.DisallowUnknownFields()
		var submitted map[string]*instance.VideoApps
		if decoder.Decode(&submitted) != nil || submitted == nil || decoder.Decode(new(any)) != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid video app preferences"})
			return
		}
		for serviceType, apps := range submitted {
			if !instance.IsVideoServerType(serviceType) || apps == nil || apps.Validate() != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid video app preferences"})
				return
			}
			prefs[serviceType] = *apps
		}
		err = h.svc.saveVideoAppPreferences(claims.UserID, prefs)
	} else {
		prefs, err = h.svc.videoAppPreferences(claims.UserID)
	}
	if err != nil {
		h.logger.Error("mediaaccess: video app preferences", "err", err, "user_id", claims.UserID)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "could not save or load video app preferences"})
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}

func (s *Service) saveVideoAppPreferences(userID int64, prefs videoAppPreferences) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, serviceType := range []string{"plex", "jellyfin", "emby"} {
		if _, err := tx.Exec(`INSERT INTO video_app_preferences (user_id, service_type, ios) VALUES (?, ?, ?)
			ON CONFLICT(user_id, service_type) DO UPDATE SET ios=excluded.ios`, userID, serviceType, prefs[serviceType].IOS); err != nil {
			return err
		}
	}
	return tx.Commit()
}
