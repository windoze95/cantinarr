package proxy

import (
	"io"
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
		// CONNECT is a tunnel protocol, while TRACE/TRACK can reflect the stored
		// upstream API key injected by this proxy. None is an ordinary arr API
		// method. Reject them before even resolving the instance so the all-callers
		// credential and body-inspection boundary cannot be bypassed.
		if isUnsafeProxyMethod(r.Method) {
			w.Header().Set("Content-Type", "application/json")
			enforcePrivateProxyCachePolicy(w.Header())
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = io.WriteString(w, `{"error":"proxy method is not supported"}`)
			return
		}

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

func isUnsafeProxyMethod(method string) bool {
	return strings.EqualFold(method, http.MethodConnect) ||
		strings.EqualFold(method, http.MethodTrace) ||
		strings.EqualFold(method, "TRACK")
}

func (h *Handler) proxyRequest(w http.ResponseWriter, r *http.Request, target *url.URL, apiKey, stripPrefix string) {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(proxyRequest *httputil.ProxyRequest) {
			req := proxyRequest.Out
			// The inbound Cantinarr session and every credential-like client header
			// terminate at this trust boundary. Only the instance's own API key is
			// added after stripping; a user's bearer JWT/cookies must never reach an
			// administrator-configured upstream host. Rewrite runs after Go removes
			// hop-by-hop and Connection-nominated headers, so a client cannot arrange
			// for those removals to discard the upstream credentials added below.
			for name := range req.Header {
				if isSensitiveQueryName(name) || isClientForwardingHeader(name) || isClientRoutingMetadataHeader(name) || isClientIdentityCredentialHeader(name) {
					req.Header.Del(name)
				}
			}
			// ReverseProxy intentionally re-adds requested upgrade headers before
			// Rewrite runs. Cantinarr cannot inspect bytes after a 101 switch, so no
			// client (including an admin) may turn this HTTP proxy into an arbitrary
			// bidirectional tunnel.
			req.Header.Del("Connection")
			req.Header.Del("Upgrade")
			req.Header.Del("HTTP2-Settings")
			// Client trailers arrive only after the outbound body has been read, so
			// deleting their declaration is not enough. Remove the trailer map as
			// well, and do not advertise trailer support to the upstream.
			req.Header.Del("Trailer")
			req.Header.Del("Te")
			req.Trailer = nil
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
		ModifyResponse: sanitizeProxyTransportResponse,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			// ModifyResponse errors are intentionally opaque: an upstream parse
			// error must never reflect the response body (and any embedded secret)
			// back to the client or into the default ReverseProxy error log.
			sanitizeResponseHeaders(w.Header())
			w.Header().Set("Content-Type", "application/json")
			enforcePrivateProxyCachePolicy(w.Header())
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
	proxy.ServeHTTP(&informationalResponseWriter{ResponseWriter: w}, r)
}

// informationalResponseWriter sanitizes upstream informational headers before
// ReverseProxy writes them. ModifyResponse only observes the final response;
// without this wrapper a 103 Early Hints response bypasses every response-header
// scrubber. Unwrap preserves streaming, deadlines, and protocol upgrades through
// http.ResponseController.
type informationalResponseWriter struct {
	http.ResponseWriter
}

func (w *informationalResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *informationalResponseWriter) WriteHeader(statusCode int) {
	if statusCode >= 100 && statusCode < 200 && statusCode != http.StatusSwitchingProtocols {
		sanitizeInformationalResponseHeaders(w.Header())
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

// Early Hints is useful for Link preload metadata. No other informational
// response header is needed by the app, so fail closed instead of forwarding an
// unrecognized extension header whose value may contain an upstream credential.
func sanitizeInformationalResponseHeaders(header http.Header) {
	sanitizeResponseHeaders(header)
	for name := range header {
		if !strings.EqualFold(name, "Link") {
			header.Del(name)
		}
	}
}

// sanitizeProxyTransportResponse applies the content scrubber and then removes
// response trailers. Transport populates Response.Trailer only after Body reaches
// EOF, so the body wrapper clears it after every read as well as at Close.
func sanitizeProxyTransportResponse(resp *http.Response) error {
	if resp.StatusCode == http.StatusSwitchingProtocols {
		sanitizeResponseHeaders(resp.Header)
		return errUnsanitizableUpgrade
	}
	if err := sanitizeProxyResponse(resp); err != nil {
		return err
	}

	resp.Header.Del("Trailer")
	resp.Trailer = nil
	if resp.Body != nil && resp.Body != http.NoBody {
		resp.Body = &trailerDroppingResponseBody{ReadCloser: resp.Body, response: resp}
	}
	return nil
}

type trailerDroppingResponseBody struct {
	io.ReadCloser
	response *http.Response
}

func (b *trailerDroppingResponseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.response.Trailer = nil
	return n, err
}

func (b *trailerDroppingResponseBody) Close() error {
	err := b.ReadCloser.Close()
	b.response.Trailer = nil
	return err
}

func joinURLPath(base, path string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

func isClientForwardingHeader(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "forwarded" || name == "x-forwarded" || strings.HasPrefix(name, "x-forwarded-") {
		return true
	}
	switch name {
	case "cf-connecting-ip",
		"client-ip",
		"fastly-client-ip",
		"fly-client-ip",
		"true-client-ip",
		"via",
		"x-cluster-client-ip",
		"x-envoy-external-address",
		"x-original-forwarded-for",
		"x-real-ip":
		return true
	default:
		return false
	}
}

func isClientRoutingMetadataHeader(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if strings.HasPrefix(name, "x-original-") || strings.HasPrefix(name, "x-rewrite-") || strings.HasPrefix(name, "sec-websocket-") {
		return true
	}
	switch name {
	case "origin",
		"referer",
		"x-envoy-original-path",
		"x-http-method",
		"x-http-method-override",
		"x-method-override",
		"x-override-method":
		return true
	default:
		return false
	}
}

// Identity-aware reverse proxies inject signed assertions and identity
// metadata after the public client connection is authenticated. Those values
// belong to Cantinarr's trust boundary and must not be forwarded to an arr,
// even when their header names do not use a generic token/credential suffix.
func isClientIdentityCredentialHeader(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if strings.HasPrefix(name, "cf-access-") ||
		strings.HasPrefix(name, "remote-") ||
		strings.HasPrefix(name, "ssl-client-") ||
		strings.HasPrefix(name, "ssl_client_") ||
		strings.HasPrefix(name, "x-amzn-oidc-") ||
		strings.HasPrefix(name, "x-auth-") ||
		strings.HasPrefix(name, "x-auth-request-") ||
		strings.HasPrefix(name, "x-authenticated-") ||
		strings.HasPrefix(name, "x-authentik-") ||
		strings.HasPrefix(name, "x-client-cert-") ||
		strings.HasPrefix(name, "x-envoy-peer-metadata") ||
		strings.HasPrefix(name, "x-goog-authenticated-user-") ||
		strings.HasPrefix(name, "x-goog-iap-") ||
		strings.HasPrefix(name, "x-identity-") ||
		strings.HasPrefix(name, "x-keycloak-") ||
		strings.HasPrefix(name, "x-ms-client-principal") ||
		strings.HasPrefix(name, "x-ms-token-aad-") ||
		strings.HasPrefix(name, "x-pomerium-") ||
		strings.HasPrefix(name, "x-remote-") ||
		strings.HasPrefix(name, "x-spiffe-") ||
		strings.HasPrefix(name, "x-ssl-client-") ||
		strings.HasPrefix(name, "x-tls-client-") ||
		strings.HasPrefix(name, "x-user-") ||
		strings.HasPrefix(name, "x_ssl_client_") {
		return true
	}
	switch name {
	case "dpop",
		"x-client-cert",
		"x-client-dn",
		"x-email",
		"x-groups",
		"x-principal",
		"x-roles",
		"x-subject",
		"x-user",
		"x-username":
		return true
	default:
		return false
	}
}
