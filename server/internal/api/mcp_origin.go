package api

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/config"
)

// requireValidMCPOrigin enforces the Streamable HTTP DNS-rebinding boundary.
// Native and server-side clients normally omit Origin and are unaffected;
// browser callers must use an explicitly configured trusted origin.
func requireValidMCPOrigin(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimSpace(r.Header.Get("Origin"))
			if origin != "" && !mcpOriginAllowed(cfg, origin) {
				http.Error(w, "invalid MCP origin", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func mcpOriginAllowed(cfg *config.Config, rawOrigin string) bool {
	origin, ok := normalizeMCPHTTPOrigin(rawOrigin)
	if !ok || cfg == nil {
		return false
	}

	if cfg.OAuthIssuer != "" {
		issuer, valid := normalizeMCPHTTPOrigin(cfg.OAuthIssuer)
		if valid && origin == issuer {
			return true
		}
	}
	for _, allowed := range cfg.MCPAllowedOrigins {
		normalized, valid := normalizeMCPHTTPOrigin(allowed)
		if valid && origin == normalized {
			return true
		}
	}
	return false
}

func normalizeMCPHTTPOrigin(value string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	hostname := strings.ToLower(u.Hostname())
	if hostname == "" {
		return "", false
	}
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	host := hostname
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	return scheme + "://" + host, true
}
