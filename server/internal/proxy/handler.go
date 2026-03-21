package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/instance"
)

type Handler struct {
	store *instance.Store
}

func NewHandler(store *instance.Store) *Handler {
	return &Handler{store: store}
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
