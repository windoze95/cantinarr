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
	Source                 string          `json:"source"`
	EnglishOnly            bool            `json:"english_only"`
	Sources                []string        `json:"sources"`
	TraktConfigured        bool            `json:"trakt_configured"`
	HiddenWhenUnconfigured map[string]bool `json:"hidden_when_unconfigured"`
}

func discoverySettingsPayload(current serversettings.Settings, creds *credentials.Registry) discoverySettingsResponse {
	hidden := map[string]bool{}
	for mediaType := range serversettings.DiscoverServices() {
		hidden[mediaType] = current.HiddenWhenUnconfigured[mediaType]
	}
	return discoverySettingsResponse{
		Source:                 current.DiscoverySource,
		HiddenWhenUnconfigured: hidden,
		EnglishOnly:            current.DiscoveryEnglishOnly,
		Sources:                serversettings.DiscoverySources(),
		TraktConfigured:        creds.Trakt() != nil,
	}
}

// discoverySettingsHandler serves GET /api/admin/discovery-settings.
func discoverySettingsHandler(settings *serversettings.Service, creds *credentials.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		current, err := settings.Read()
		if err != nil {
			http.Error(w, `{"error":"temporarily unavailable, retry shortly"}`, http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(discoverySettingsPayload(current, creds))
	}
}

// updateDiscoverySettingsHandler serves PUT /api/admin/discovery-settings.
func updateDiscoverySettingsHandler(settings *serversettings.Service, creds *credentials.Registry, configChanged func()) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body serversettings.DiscoveryPatch
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
			return
		}
		if err := body.Validate(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		saved, err := settings.UpdateDiscovery(body)
		if err != nil {
			http.Error(w, `{"error":"could not save settings, retry shortly"}`, http.StatusServiceUnavailable)
			return
		}
		if len(body.HiddenWhenUnconfigured) > 0 && configChanged != nil {
			configChanged()
		}
		_ = json.NewEncoder(w).Encode(discoverySettingsPayload(saved, creds))
	}
}
