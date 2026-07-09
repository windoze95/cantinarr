package api

import (
	"encoding/json"
	"net/http"

	"github.com/windoze95/cantinarr-server/internal/serversettings"
	"github.com/windoze95/cantinarr-server/internal/update"
)

// updateStatusResponse is the admin-only payload behind the "update available"
// banner: the latest-release comparison plus the optional management-portal URL
// an admin can point the banner's action button at.
type updateStatusResponse struct {
	Update        update.Status `json:"update"`
	ManagementURL string        `json:"management_url"`
}

// updateStatusHandler serves GET /api/admin/update-status.
func updateStatusHandler(checker *update.Checker, settings *serversettings.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(updateStatusResponse{
			Update:        checker.Status(),
			ManagementURL: settings.Get().ManagementURL,
		})
	}
}

// updateServerSettingsHandler serves PUT /api/admin/update-status. It sets the
// management-portal URL and returns the full status in one round-trip.
func updateServerSettingsHandler(checker *update.Checker, settings *serversettings.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			ManagementURL string `json:"management_url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
			return
		}
		saved, err := settings.Set(serversettings.Settings{ManagementURL: body.ManagementURL})
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(updateStatusResponse{
			Update:        checker.Status(),
			ManagementURL: saved.ManagementURL,
		})
	}
}
