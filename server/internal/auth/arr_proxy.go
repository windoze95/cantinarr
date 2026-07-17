package auth

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
)

// arrReadResources is the service-specific allowlist of Servarr resources a
// non-admin user may read (GET) through the instance proxy. It deliberately
// excludes every credential-bearing or privileged endpoint: indexers,
// download clients, notifications, import lists, and config/host (which carries
// the instance's own API key) are all absent, as are interactive indexer search
// ("release") and command endpoints. Those remain admin-only.
var arrReadResources = map[string]map[string]bool{
	"radarr": {
		"movie":    true,
		"calendar": true,
		"queue":    true,
		"history":  true,
		"wanted":   true,
	},
	"sonarr": {
		"series":   true,
		"episode":  true,
		"calendar": true,
		"queue":    true,
		"history":  true,
		"wanted":   true,
	},
	"chaptarr": {
		"author":     true,
		"book":       true,
		"bookfile":   true,
		"calendar":   true,
		"queue":      true,
		"history":    true,
		"wanted":     true,
		"MediaCover": true,
	},
}

// arrAPIPrefixes binds each supported arr service to the API version Cantinarr
// implements. A path for another version is denied rather than guessed.
var arrAPIPrefixes = map[string]string{
	"radarr":   "api/v3/",
	"sonarr":   "api/v3/",
	"chaptarr": "api/v1/",
}

// isArrReadResource reports whether forwardPath — the portion of an instance
// proxy request after the API-version marker — matches a complete allowlisted
// read route. Matching only the first segment would accidentally expose sibling
// operations such as movie/lookup or movie/editor.
func isArrReadResource(serviceType, forwardPath string) bool {
	// Reject residual escapes, backslashes, empty segments, and traversal before
	// applying route shapes. These are parsed inconsistently by Go, proxies, and
	// the .NET arr services and must never broaden an allowlisted route.
	if forwardPath == "" || strings.Contains(forwardPath, "%") || strings.Contains(forwardPath, "\\") || strings.Contains(forwardPath, "..") {
		return false
	}
	segments := strings.Split(forwardPath, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." {
			return false
		}
	}
	resource := segments[0]
	if !arrReadResources[serviceType][resource] {
		return false
	}

	if serviceType == "chaptarr" && resource == "MediaCover" {
		// The sole requester consumer is an owned-book cover returned as
		// /MediaCover/Books/{numeric-id}/... . Lookup covers and every other
		// MediaCover subtree remain admin-only.
		return len(segments) >= 4 && segments[1] == "Books" && isPositiveDecimalID(segments[2])
	}

	suffix := segments[1:]
	switch resource {
	case "movie", "series", "episode", "author", "book", "bookfile":
		if len(suffix) == 0 {
			return true
		}
		if serviceType == "chaptarr" && resource == "book" && len(suffix) == 1 && suffix[0] == "lookup" {
			// Dashboard book discovery has no Cantinarr metadata-provider
			// equivalent and intentionally uses this exact read-only lookup.
			return true
		}
		return len(suffix) == 1 && isPositiveDecimalID(suffix[0])
	case "calendar", "queue", "history":
		return len(suffix) == 0
	case "wanted":
		return len(suffix) == 1 && (suffix[0] == "missing" || suffix[0] == "cutoff")
	default:
		return false
	}
}

func isPositiveDecimalID(value string) bool {
	if value == "" || value == "0" {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

// isArrReadPath reports whether the proxy-relative wildcard path is an
// allowlisted read for the concrete service type. Requiring the version prefix
// at the beginning prevents an arr-looking marker embedded later in a path from
// being treated as authorization.
func isArrReadPath(serviceType, forwardPath string) bool {
	prefix, ok := arrAPIPrefixes[serviceType]
	if !ok || !strings.HasPrefix(forwardPath, prefix) {
		return false
	}
	return isArrReadResource(serviceType, strings.TrimPrefix(forwardPath, prefix))
}

// InstanceAccessChecker resolves an instance's service type and whether it is
// the effective instance exposed to a user. Implemented by *instance.Store; declared as an
// interface here so the auth package does not import instance (which would form
// an import cycle).
type InstanceAccessChecker interface {
	LookupServiceType(instanceID string) (string, bool, error)
	UserCanAccessInstance(userID int64, instanceID, serviceType string) (bool, error)
}

// RequireArrProxyAccess authorizes instance-proxy requests. A GET to an
// allowlisted Servarr read resource requires arr:browse (held by the user
// role), giving non-admins read-only access to their library, calendar, queue,
// history, and wanted lists. Every other request — any write, command,
// interactive search, config endpoint, or non-arr service — requires the
// admin-level instances:manage.
//
// Every requester read is bound to the same effective instance exposed in
// /api/config: a per-user Radarr/Sonarr pin or global fallback, or an explicit
// Chaptarr grant. The middleware must run after AuthMiddleware and on a route
// carrying the {instanceID} param.
func RequireArrProxyAccess(access InstanceAccessChecker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetClaims(r.Context())
			if claims == nil {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}

			// Admins may proxy every configured service and operation. In
			// particular, do not make their access depend on a redundant metadata
			// lookup; the proxy handler performs the authoritative instance load.
			if HasPermission(claims.Role, PermissionInstancesManage) {
				next.ServeHTTP(w, r)
				return
			}

			// A requester can only perform read-only arr browsing. Reject an
			// unrecognized role or write before consulting instance metadata.
			if !HasPermission(claims.Role, PermissionArrBrowse) || r.Method != http.MethodGet {
				http.Error(w, `{"error":"permission denied"}`, http.StatusForbidden)
				return
			}

			instanceID := chi.URLParam(r, "instanceID")
			if instanceID == "" {
				http.Error(w, `{"error":"instance ID required"}`, http.StatusBadRequest)
				return
			}

			serviceType, exists, err := access.LookupServiceType(instanceID)
			if err != nil {
				http.Error(w, `{"error":"temporarily unavailable, retry shortly"}`, http.StatusServiceUnavailable)
				return
			}
			if !exists {
				http.Error(w, `{"error":"instance not found"}`, http.StatusNotFound)
				return
			}

			// Chi matches against RawPath when one exists, so unescape the
			// wildcard before validating it. Otherwise an encoded ".." segment
			// could pass the textual allowlist and be decoded by the upstream.
			forwardPath, err := url.PathUnescape(chi.URLParam(r, "*"))
			if err != nil || !isArrReadPath(serviceType, forwardPath) {
				http.Error(w, `{"error":"permission denied"}`, http.StatusForbidden)
				return
			}

			// The visible config exposes exactly one effective Radarr/Sonarr
			// instance (the user's pin or the global default) and one explicitly
			// granted Chaptarr instance. Enforce that same boundary even when a
			// caller guesses or retains a hidden sibling instance UUID.
			allowed, err := access.UserCanAccessInstance(claims.UserID, instanceID, serviceType)
			if err != nil {
				http.Error(w, `{"error":"temporarily unavailable, retry shortly"}`, http.StatusServiceUnavailable)
				return
			}
			if !allowed {
				http.Error(w, `{"error":"permission denied"}`, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
