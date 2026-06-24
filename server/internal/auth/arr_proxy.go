package auth

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// arrReadResources is the allowlist of Servarr resources a non-admin user may
// read (GET) through the instance proxy, keyed by the first path segment after
// the API-version marker ("/api/v3/" for Radarr/Sonarr, "/api/v1/" for
// Chaptarr). It is deliberately limited to browsing data and excludes every
// credential-bearing or privileged endpoint: indexers, download clients,
// notifications, import lists, and config/host (which carries the instance's own
// API key) are all absent, as are interactive indexer search ("release") and
// command endpoints. Those remain admin-only.
var arrReadResources = map[string]bool{
	"movie":           true, // Radarr: library list, detail, lookup
	"series":          true, // Sonarr: library list, detail, lookup
	"episode":         true, // Sonarr: episodes for a series
	"author":          true, // Chaptarr: library list, detail, lookup
	"book":            true, // Chaptarr: books for an author, detail, lookup
	"bookfile":        true, // Chaptarr: imported book files
	"calendar":        true, // upcoming / aired releases
	"queue":           true, // active download queue (progress only)
	"history":         true, // grab / import history
	"wanted":          true, // wanted/missing, wanted/cutoff
	"qualityprofile":  true, // profile names shown on items
	"metadataprofile": true, // Chaptarr: metadata profile names
	"rootfolder":      true, // root folder list
}

// arrAPIMarkers are the API-version path segments whose read resources are
// browsable by non-admins: v3 (Radarr/Sonarr) and v1 (Chaptarr/Readarr).
var arrAPIMarkers = []string{"/api/v3/", "/api/v1/"}

// isArrReadResource reports whether forwardPath — the portion of an instance
// proxy request after the API-version marker, e.g. "movie/123", "author/7", or
// "wanted/missing" — targets an allowlisted read resource.
func isArrReadResource(forwardPath string) bool {
	// Reject path traversal so an allowlisted prefix can't be used to reach a
	// non-allowlisted endpoint (e.g. "movie/../config/host") once the reverse
	// proxy forwards the path to the upstream service.
	if forwardPath == "" || strings.Contains(forwardPath, "..") {
		return false
	}
	resource := forwardPath
	if i := strings.IndexByte(forwardPath, '/'); i >= 0 {
		resource = forwardPath[:i]
	}
	return arrReadResources[resource]
}

// isArrReadPath reports whether urlPath is a Servarr read path. urlPath is the
// full incoming request path, e.g. "/api/instances/<id>/api/v3/movie" or
// ".../api/v1/author". Anything that is not a recognized arr API path (other
// services proxied through the same route, such as download clients) returns
// false and therefore requires admin access.
func isArrReadPath(urlPath string) bool {
	for _, marker := range arrAPIMarkers {
		if i := strings.Index(urlPath, marker); i >= 0 {
			return isArrReadResource(urlPath[i+len(marker):])
		}
	}
	return false
}

// InstanceAccessChecker resolves an instance's service type and whether a user
// has been granted access to it. Implemented by *instance.Store; declared as an
// interface here so the auth package does not import instance (which would form
// an import cycle).
type InstanceAccessChecker interface {
	LookupServiceType(instanceID string) (string, bool, error)
	UserHasInstanceAccess(userID int64, instanceID string) (bool, error)
}

// RequireArrProxyAccess authorizes instance-proxy requests. A GET to an
// allowlisted Servarr read resource requires arr:browse (held by the user
// role), giving non-admins read-only access to their library, calendar, queue,
// history, and wanted lists. Every other request — any write, command,
// interactive search, config endpoint, or non-arr service — requires the
// admin-level instances:manage.
//
// Service types with no global default (chaptarr) are additionally per-user
// access-gated: a non-admin may touch a chaptarr instance only if an admin has
// explicitly granted it to them (a user_default_instances row). The middleware
// must run after AuthMiddleware and on a route carrying the {instanceID} param.
func RequireArrProxyAccess(access InstanceAccessChecker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetClaims(r.Context())
			if claims == nil {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}

			isAdmin := HasPermission(claims.Role, PermissionInstancesManage)

			// Per-user access gate for service types without a global default: a
			// chaptarr instance is reachable by a non-admin only if an admin has
			// explicitly granted that instance to them. Admins bypass the gate.
			if !isAdmin {
				instanceID := chi.URLParam(r, "instanceID")
				if instanceID != "" {
					if serviceType, ok, err := access.LookupServiceType(instanceID); err == nil && ok && serviceType == "chaptarr" {
						granted, err := access.UserHasInstanceAccess(claims.UserID, instanceID)
						if err != nil || !granted {
							http.Error(w, `{"error":"permission denied"}`, http.StatusForbidden)
							return
						}
					}
				}
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
}
