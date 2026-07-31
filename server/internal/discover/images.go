package discover

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// validTraktImageHost accepts any *.trakt.tv subdomain rather than naming CDN
// hosts, because Trakt migrates them: payloads moved from walter-r2.trakt.tv
// (still what their docs show) to media.trakt.tv in July 2026, and a pinned
// allowlist silently lost every poster when they did. The relay exists because
// these CDNs send no CORS headers, so the web renderer cannot read their bytes
// directly the way it can TMDB's. The suffix plus the strict charset (which
// also excludes '@', ':' and '/', so the fetched URL cannot be steered to
// another origin) keeps the endpoint a Trakt-artwork relay rather than an
// open proxy.
func validTraktImageHost(host string) bool {
	if len(host) < len("x.trakt.tv") || len(host) > 64 {
		return false
	}
	if !strings.HasSuffix(host, ".trakt.tv") || strings.Contains(host, "..") {
		return false
	}
	for _, c := range host {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '.', c == '-':
		default:
			return false
		}
	}
	return true
}

// traktImageClient has a zero Transport on purpose: it consults
// http.DefaultTransport per request, which is how the test harness intercepts
// outbound traffic. The timeout is generous because fanart files run larger
// than poster thumbs.
var traktImageClient = &http.Client{Timeout: 15 * time.Second}

// TraktImage relays one Trakt CDN image to the client. Trakt artwork URLs are
// public CDN assets, but unlike TMDB's CDN they carry no
// Access-Control-Allow-Origin header, so a browser-rendered client is not
// allowed to fetch them cross-origin. Native clients keep hitting the CDN
// directly; the web client routes the same URL through here, same-origin.
func (h *Handler) TraktImage(w http.ResponseWriter, r *http.Request) {
	// Same posture as every other /trakt/* endpoint: without a configured
	// Trakt credential nothing hands out walter URLs, so nothing should be
	// relayed either.
	if h.creds.Trakt() == nil {
		http.Error(w, `{"error":"trakt not configured"}`, http.StatusServiceUnavailable)
		return
	}
	host := chi.URLParam(r, "host")
	if !validTraktImageHost(host) {
		http.Error(w, `{"error":"image not found"}`, http.StatusNotFound)
		return
	}
	path := chi.URLParam(r, "*")
	if !validTraktImagePath(path) {
		http.Error(w, `{"error":"image not found"}`, http.StatusNotFound)
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet,
		fmt.Sprintf("https://%s/%s", host, path), nil)
	if err != nil {
		http.Error(w, `{"error":"image fetch failed"}`, http.StatusBadGateway)
		return
	}
	upstream, err := traktImageClient.Do(req)
	if err != nil {
		http.Error(w, `{"error":"image fetch failed"}`, http.StatusBadGateway)
		return
	}
	defer upstream.Body.Close()

	switch {
	case upstream.StatusCode == http.StatusOK:
	case upstream.StatusCode == http.StatusNotFound:
		http.Error(w, `{"error":"image not found"}`, http.StatusNotFound)
		return
	default:
		http.Error(w, `{"error":"image fetch failed"}`, http.StatusBadGateway)
		return
	}

	contentType := upstream.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	// Overwrites the router group's blanket application/json header.
	w.Header().Set("Content-Type", contentType)
	// Artwork files are immutable-in-practice: a day of client caching keeps
	// reloads off both this relay and the CDN.
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if upstream.ContentLength > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", upstream.ContentLength))
	}
	io.Copy(w, upstream.Body)
}

// validTraktImagePath accepts only the shape Trakt artwork URLs actually have:
// an images/… path of simple hyphen/dot/underscore file segments. Everything
// else — traversal, encodings, queries smuggled into a segment — is rejected
// rather than normalized.
func validTraktImagePath(path string) bool {
	if path == "" || len(path) > 512 || !strings.HasPrefix(path, "images/") {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		for _, c := range segment {
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			case c == '.', c == '_', c == '-':
			default:
				return false
			}
		}
	}
	return true
}
