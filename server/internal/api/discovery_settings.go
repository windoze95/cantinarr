package api

import (
	"encoding/json"
	"net/http"

	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/serversettings"
)

// discoverySettingsResponse is the admin payload behind the Discovery settings
// screen: the stored preferences plus the choices the UI can offer. Trakt is an
// optional integration, so the screen needs to know whether picking it would
// actually work before it lets an admin pick it.
type discoverySettingsResponse struct {
	Source          string   `json:"source"`
	EnglishOnly     bool     `json:"english_only"`
	Sources         []string `json:"sources"`
	TraktConfigured bool     `json:"trakt_configured"`
}

func discoverySettingsPayload(settings *serversettings.Service, creds *credentials.Registry) discoverySettingsResponse {
	current := settings.Get()
	return discoverySettingsResponse{
		Source:          current.DiscoverySource,
		EnglishOnly:     current.DiscoveryEnglishOnly,
		Sources:         serversettings.DiscoverySources(),
		TraktConfigured: creds.Trakt() != nil,
	}
}

// discoverySettingsHandler serves GET /api/admin/discovery-settings.
func discoverySettingsHandler(settings *serversettings.Service, creds *credentials.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(discoverySettingsPayload(settings, creds))
	}
}

// updateDiscoverySettingsHandler serves PUT /api/admin/discovery-settings.
func updateDiscoverySettingsHandler(settings *serversettings.Service, creds *credentials.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			Source      string `json:"source"`
			EnglishOnly bool   `json:"english_only"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
			return
		}
		if _, err := settings.SetDiscovery(body.Source, body.EnglishOnly); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(discoverySettingsPayload(settings, creds))
	}
}
