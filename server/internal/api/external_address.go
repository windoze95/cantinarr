package api

import (
	"encoding/json"
	"net/http"

	"github.com/windoze95/cantinarr-server/internal/serversettings"
)

// externalAddressResponse carries the admin-configured external address:
// the origin invitees' devices use to reach this server, which connect
// invite links and passkey setup links are built from. Empty means links
// fall back to the address the generating admin's own app is connected with.
type externalAddressResponse struct {
	ExternalURL string `json:"external_url"`
}

// externalAddressHandler serves GET /api/admin/external-address.
func externalAddressHandler(settings *serversettings.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(externalAddressResponse{
			ExternalURL: settings.Get().ExternalURL,
		})
	}
}

// updateExternalAddressHandler serves PUT /api/admin/external-address. An
// empty value clears the setting.
func updateExternalAddressHandler(settings *serversettings.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body externalAddressResponse
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
			return
		}
		saved, err := settings.SetExternalURL(body.ExternalURL)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(externalAddressResponse{
			ExternalURL: saved.ExternalURL,
		})
	}
}
