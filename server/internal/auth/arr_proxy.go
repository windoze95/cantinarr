package auth

import (
	"net/http"
	"strings"
)

// arrReadResources is the allowlist of Radarr/Sonarr v3 resources a non-admin
// user may read (GET) through the instance proxy. It is deliberately limited to
// browsing data and excludes every credential-bearing or privileged endpoint:
// indexers, download clients, notifications, import lists, and config/host
// (which carries the instance's own API key) are all absent, as are interactive
// indexer search ("release") and command endpoints. Those remain admin-only.
var arrReadResources = map[string]bool{
	"movie":          true, // Radarr: library list, detail, lookup
	"series":         true, // Sonarr: library list, detail, lookup
	"episode":        true, // Sonarr: episodes for a series
	"calendar":       true, // upcoming / aired releases
	"queue":          true, // active download queue (progress only)
	"history":        true, // grab / import history
	"wanted":         true, // wanted/missing, wanted/cutoff
	"qualityprofile": true, // profile names shown on items
	"rootfolder":     true, // root folder list
}

// isArrReadResource reports whether forwardPath — the portion of an instance
// proxy request after the "/api/v3/" marker, e.g. "movie/123" or
// "wanted/missing" — targets an allowlisted read resource.
func isArrReadResource(forwardPath string) bool {
	// Reject path traversal so an allowlisted prefix can't be used to reach a
	// non-allowlisted endpoint (e.g. "movie/../config/host") once the reverse
	// proxy forwards the path to Radarr/Sonarr.
	if forwardPath == "" || strings.Contains(forwardPath, "..") {
		return false
	}
	resource := forwardPath
	if i := strings.IndexByte(forwardPath, '/'); i >= 0 {
		resource = forwardPath[:i]
	}
	return arrReadResources[resource]
}

// isArrReadPath reports whether urlPath is a Radarr/Sonarr read path. urlPath is
// the full incoming request path, e.g. "/api/instances/<id>/api/v3/movie".
// Anything that is not a v3 path (other services proxied through the same route,
// such as download clients) returns false and therefore requires admin access.
func isArrReadPath(urlPath string) bool {
	const marker = "/api/v3/"
	i := strings.Index(urlPath, marker)
	if i < 0 {
		return false
	}
	return isArrReadResource(urlPath[i+len(marker):])
}

// RequireArrProxyAccess authorizes instance-proxy requests. A GET to an
// allowlisted Radarr/Sonarr read resource requires arr:browse (held by the user
// role), giving non-admins read-only access to their library, calendar, queue,
// history, and wanted lists. Every other request — any write, command,
// interactive search, config endpoint, or non-arr service — requires the
// admin-level instances:manage. It must run after AuthMiddleware.
func RequireArrProxyAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetClaims(r.Context())
		if claims == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		required := PermissionInstancesManage
		if r.Method == http.MethodGet && isArrReadPath(r.URL.Path) {
			required = PermissionArrBrowse
		}

		if !HasPermission(claims.Role, required) {
			http.Error(w, `{"error":"permission denied"}`, http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}
