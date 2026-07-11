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
		if target.User != nil || target.RawQuery != "" || target.Fragment != "" {
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
			// The inbound Cantinarr session and every credential-like client header
			// terminate at this trust boundary. Only the instance's own API key is
			// added after stripping; a user's bearer JWT/cookies must never reach an
			// administrator-configured upstream host.
			for name := range req.Header {
				if isSensitiveQueryName(name) {
					req.Header.Del(name)
				}
			}
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = joinURLPath(target.Path, strings.TrimPrefix(req.URL.Path, stripPrefix))
			req.URL.RawPath = ""
			req.Host = target.Host
			req.Header.Set("X-Api-Key", apiKey)
			// JSON responses must be inspectable before they reach a client. Ask
			// the arr for an identity representation so a compressed body cannot
			// bypass the response sanitizer.
			req.Header.Set("Accept-Encoding", "identity")
		},
		ModifyResponse: sanitizeProxyResponse,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			// ModifyResponse errors are intentionally opaque: an upstream parse
			// error must never reflect the response body (and any embedded secret)
			// back to the client or into the default ReverseProxy error log.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"unsafe upstream response"}`))
		},
	}
	// The /api router pre-sets Content-Type: application/json on every
	// response, and ReverseProxy appends the upstream's header rather than
	// replacing it. Browsers merge the duplicates into a value Flutter web
	// can't parse as JSON, so drop the default and let the upstream header
	// through verbatim.
	w.Header().Del("Content-Type")
	proxy.ServeHTTP(w, r)
}

func joinURLPath(base, path string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}
