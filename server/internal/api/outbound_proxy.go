package api

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/httpx"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"github.com/windoze95/cantinarr-server/internal/serversettings"
)

// outboundProxyResponse is what admins see of the proxy: the address, the
// username, and whether a password is stored. The password itself is
// write-only -- once saved it never leaves the server.
type outboundProxyResponse struct {
	URL         string `json:"url"`
	Username    string `json:"username"`
	HasPassword bool   `json:"has_password"`
}

// outboundProxyRequest is the PUT and test body. An empty url clears the
// proxy; a blank password keeps the stored one when the username is unchanged.
type outboundProxyRequest struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (r outboundProxyRequest) proxy() serversettings.OutboundProxy {
	return serversettings.OutboundProxy{URL: r.URL, Username: r.Username, Password: r.Password}
}

func outboundProxyPayload(p serversettings.OutboundProxy) outboundProxyResponse {
	return outboundProxyResponse{URL: p.URL, Username: p.Username, HasPassword: p.Password != ""}
}

func writeOutboundProxyError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// outboundProxyHandler serves GET /api/admin/outbound-proxy.
func outboundProxyHandler(settings *serversettings.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stored, err := settings.OutboundProxy()
		if err != nil {
			writeOutboundProxyError(w, http.StatusInternalServerError, "the stored outbound proxy could not be read")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(outboundProxyPayload(stored))
	}
}

// updateOutboundProxyHandler serves PUT /api/admin/outbound-proxy. It
// validates, stores, and installs the proxy in the same request, so the next
// internet-bound call already rides it; the reply is what a fresh GET would
// say.
func updateOutboundProxyHandler(settings *serversettings.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body outboundProxyRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeOutboundProxyError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		saved, err := settings.SetOutboundProxy(body.proxy())
		if err != nil {
			writeOutboundProxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := httpx.SetOutboundProxyString(saved.ProxyURL()); err != nil {
			// Unreachable after validation, but a stored proxy the transport
			// cannot use must never pass silently.
			writeOutboundProxyError(w, http.StatusInternalServerError, "the saved proxy could not be installed")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(outboundProxyPayload(saved))
	}
}

// testOutboundProxyHandler serves POST /api/admin/outbound-proxy/test: it
// fetches TMDB's configuration through the candidate proxy -- the typed
// values, plus the stored password when the field was left blank -- without
// saving anything. 204 means the proxy carried the request; 400 carries the
// reason with credentials scrubbed. Like the instance connection test it runs
// on the server, the host that will actually dial the proxy.
func testOutboundProxyHandler(settings *serversettings.Service, creds *credentials.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body outboundProxyRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeOutboundProxyError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		candidate, err := settings.ResolveOutboundProxy(body.proxy())
		if err != nil {
			writeOutboundProxyError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !candidate.Configured() {
			writeOutboundProxyError(w, http.StatusBadRequest, "proxy address is required")
			return
		}
		tmdbClient := creds.TMDB()
		if tmdbClient == nil {
			writeOutboundProxyError(w, http.StatusBadRequest, "TMDB is not configured, so there is nothing to test the proxy against")
			return
		}
		proxyURL, err := url.Parse(candidate.ProxyURL())
		if err != nil {
			writeOutboundProxyError(w, http.StatusBadRequest, "proxy address could not be parsed")
			return
		}
		transport := httpx.ProxyTransport(proxyURL)
		defer transport.CloseIdleConnections()
		if err := tmdbClient.Probe(r.Context(), transport); err != nil {
			writeOutboundProxyError(w, http.StatusBadRequest, "proxy test failed: "+secrets.RedactError(err).Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
