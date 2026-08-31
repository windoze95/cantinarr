// external_address.go — the origin invite and passkey links are built from.
//
// It lives beside the connect-token surface because that is what it exists
// for: without it a link carries whatever address the admin's own app happens
// to be connected with, which is often a LAN address the invitee cannot reach.
// Setting it once makes every link built afterwards use it instead.
package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
)

var (
	extAddrMu sync.Mutex
	// extAddrURL is the admin-configured external address; "" means unset,
	// and links then fall back to the address the app connected with.
	extAddrURL string
)

// externalAddress returns the saved external address ("" when unset).
func externalAddress() string {
	extAddrMu.Lock()
	defer extAddrMu.Unlock()
	return extAddrURL
}

// connectLinkOrigin is the address a freshly minted connect link advertises:
// the configured external address when there is one, otherwise the demo's own
// advertised URL. origin_source names which of the two answered.
func connectLinkOrigin() (origin, source string) {
	if addr := externalAddress(); addr != "" {
		return addr, "external_address"
	}
	return demoServerURL, "app"
}

// registerExternalAddress mounts the admin external-address surface
// (users:manage in the real server; admin-gated here).
func registerExternalAddress(r chi.Router) {
	admin := r.With(requireAdmin)
	admin.Get("/admin/external-address", extAddrGetHandler)
	admin.Put("/admin/external-address", extAddrPutHandler)
}

// extAddrGetHandler — GET /api/admin/external-address. Always an object; the
// app hard-casts the body to a map.
func extAddrGetHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"external_url": externalAddress()})
}

// extAddrPutHandler — PUT /api/admin/external-address. Stores the normalized
// value and echoes what was actually saved, so the dialog shows the truth
// rather than what was typed. An empty string clears it.
func extAddrPutHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ExternalURL string `json:"external_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	value := strings.TrimRight(strings.TrimSpace(req.ExternalURL), "/")
	if value != "" {
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			writeErr(w, http.StatusBadRequest, "external_url must be an http(s) URL")
			return
		}
	}
	extAddrMu.Lock()
	extAddrURL = value
	extAddrMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"external_url": value})
}
