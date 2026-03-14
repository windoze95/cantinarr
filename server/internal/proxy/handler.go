package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

type Handler struct {
	radarrURL *url.URL
	radarrKey string
	sonarrURL *url.URL
	sonarrKey string
}

func NewHandler(radarrURL, radarrKey, sonarrURL, sonarrKey string) *Handler {
	h := &Handler{
		radarrKey: radarrKey,
		sonarrKey: sonarrKey,
	}
	if radarrURL != "" {
		h.radarrURL, _ = url.Parse(radarrURL)
	}
	if sonarrURL != "" {
		h.sonarrURL, _ = url.Parse(sonarrURL)
	}
	return h
}

func (h *Handler) RadarrProxy() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.radarrURL == nil {
			http.Error(w, `{"error":"radarr not configured"}`, http.StatusServiceUnavailable)
			return
		}
		h.proxyRequest(w, r, h.radarrURL, h.radarrKey, "/api/radarr")
	}
}

func (h *Handler) SonarrProxy() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.sonarrURL == nil {
			http.Error(w, `{"error":"sonarr not configured"}`, http.StatusServiceUnavailable)
			return
		}
		h.proxyRequest(w, r, h.sonarrURL, h.sonarrKey, "/api/sonarr")
	}
}

func (h *Handler) proxyRequest(w http.ResponseWriter, r *http.Request, target *url.URL, apiKey, stripPrefix string) {
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = strings.TrimPrefix(req.URL.Path, stripPrefix)
			req.Host = target.Host
			req.Header.Set("X-Api-Key", apiKey)
		},
	}
	proxy.ServeHTTP(w, r)
}
