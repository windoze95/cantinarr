package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/instance"
)

type Handler struct {
	store    *instance.Store
	sessions *sessionCache
}

func NewHandler(store *instance.Store) *Handler {
	return &Handler{store: store, sessions: newSessionCache()}
}

// InstanceProxy proxies requests to a specific service instance by ID.
func (h *Handler) InstanceProxy() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceID := chi.URLParam(r, "instanceID")
		if instanceID == "" {
			http.Error(w, `{"error":"instance ID required"}`, http.StatusBadRequest)
			return
		}

		inst, err := h.store.Get(instanceID)
		if err != nil {
			http.Error(w, `{"error":"failed to get instance"}`, http.StatusInternalServerError)
			return
		}
		if inst == nil {
			http.Error(w, `{"error":"instance not found"}`, http.StatusNotFound)
			return
		}

		target, err := url.Parse(inst.URL)
		if err != nil {
			http.Error(w, `{"error":"invalid instance URL"}`, http.StatusInternalServerError)
			return
		}

		stripPrefix := "/api/instances/" + instanceID
		h.proxyRequest(w, r, target, inst.APIKey, stripPrefix)
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

// TestWebLogin verifies cover fetching end-to-end with the given (or, for an
// existing instance with blank fields, the stored) credentials: it performs the
// forms login AND samples an actual /MediaCoverProxy cover, so the admin learns
// not just that login works but whether covers really load (Chaptarr's cover
// proxy can 500 server-side even with a valid session). Always 200; the JSON
// body carries success/error plus cover_ok/cover_detail.
func (h *Handler) TestWebLogin() http.HandlerFunc {
	type request struct {
		URL        string `json:"url"`
		Username   string `json:"username"`
		Password   string `json:"password"`
		APIKey     string `json:"api_key"`
		InstanceID string `json:"instance_id"`
	}
	write := func(w http.ResponseWriter, body map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		url, username, password, apiKey := req.URL, req.Username, req.Password, req.APIKey
		// Editing with blank secrets: fall back to the stored credentials.
		if (password == "" || apiKey == "") && req.InstanceID != "" {
			if inst, _ := h.store.Get(req.InstanceID); inst != nil {
				if url == "" {
					url = inst.URL
				}
				if username == "" {
					username = inst.Username
				}
				if password == "" {
					password = inst.Password
				}
				if apiKey == "" {
					apiKey = inst.APIKey
				}
			}
		}
		if url == "" || username == "" || password == "" {
			write(w, map[string]any{"success": false, "error": "URL, username, and password are required"})
			return
		}
		cookie, err := h.sessions.login(url, username, password)
		if err != nil {
			write(w, map[string]any{"success": false, "error": err.Error()})
			return
		}
		coverOK, coverDetail := true, ""
		if apiKey != "" {
			coverOK, coverDetail = h.sessions.checkProxyCover(url, apiKey, cookie)
		}
		write(w, map[string]any{"success": true, "cover_ok": coverOK, "cover_detail": coverDetail})
	}
}

// CoverProxy streams a Chaptarr cover image. Chaptarr serves /MediaCover and
// /MediaCoverProxy from its web layer (login-session gated), not the API key, so
// when the instance has web credentials we fetch with a logged-in session
// cookie; otherwise we fall back to the API-key route (/api/v1/MediaCover, which
// only covers owned books). The image path is taken from a ?path= query param so
// it can carry its own query string (e.g. ?lastWrite=...).
func (h *Handler) CoverProxy() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceID := chi.URLParam(r, "instanceID")
		inst, err := h.store.Get(instanceID)
		if err != nil {
			http.Error(w, `{"error":"failed to get instance"}`, http.StatusInternalServerError)
			return
		}
		if inst == nil {
			http.Error(w, `{"error":"instance not found"}`, http.StatusNotFound)
			return
		}

		coverPath, ok := sanitizeCoverPath(r.URL.Query().Get("path"))
		if !ok {
			http.Error(w, `{"error":"invalid cover path"}`, http.StatusBadRequest)
			return
		}
		base := strings.TrimRight(inst.URL, "/")

		var resp *http.Response
		if inst.Username != "" && inst.Password != "" {
			resp, err = h.sessions.fetchCover(inst, base+coverPath)
		} else {
			resp, err = h.sessions.fetchWithKey(base+"/api/v1"+coverPath, inst.APIKey)
		}
		if err != nil {
			http.Error(w, `{"error":"cover unavailable"}`, http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		if ct := resp.Header.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		if cl := resp.Header.Get("Content-Length"); cl != "" {
			w.Header().Set("Content-Length", cl)
		}
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, resp.Body)
	}
}
