package bookdiscovery

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/httpx"
)

// hardcoverAssetHost is the one host Hardcover serves cover art from. Unlike
// the Trakt relay this is not a caller-supplied parameter: there is a single
// asset CDN, so pinning it here is what keeps the endpoint a Hardcover-artwork
// relay rather than an open proxy.
const hardcoverAssetHost = "assets.hardcover.app"

// coverImageClient rides the internet-bound transport class — Hardcover's CDN
// is exactly the kind of host an admin proxies. The timeout matches the Trakt
// relay's; cover files are the same order of magnitude.
var coverImageClient = &http.Client{Transport: httpx.External(), Timeout: 15 * time.Second}

// CoverImage relays one Hardcover cover image to the client. The CDN is public,
// but it sends no Access-Control-Allow-Origin header, so a browser-rendered
// client may not read its bytes cross-origin.
//
// Without this relay the web app can still *display* a cover by handing the URL
// to a DOM <img> element, which needs no CORS grant — but that makes every
// cover a platform view, and Flutter web composites only a handful of those
// correctly against canvas-drawn content. Past that handful the availability
// badge painted over the artwork lost the z-order fight and vanished. Native
// clients keep hitting the CDN directly; the web client routes the same URL
// through here, same-origin, so covers decode on the canvas path like TMDB's.
func CoverImage(w http.ResponseWriter, r *http.Request) {
	path := chi.URLParam(r, "*")
	if !validHardcoverImagePath(path) {
		http.Error(w, `{"error":"image not found"}`, http.StatusNotFound)
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet,
		fmt.Sprintf("https://%s/%s", hardcoverAssetHost, path), nil)
	if err != nil {
		http.Error(w, `{"error":"image fetch failed"}`, http.StatusBadGateway)
		return
	}
	upstream, err := coverImageClient.Do(req)
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
	// Cover files are immutable-in-practice: a day of client caching keeps
	// reloads off both this relay and the CDN.
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if upstream.ContentLength > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", upstream.ContentLength))
	}
	io.Copy(w, upstream.Body)
}

// validHardcoverImagePath accepts any path of plain file segments. It
// deliberately pins no directory prefix: Hardcover serves covers under both
// `edition/` and `editions/` today, and the Trakt relay's comment records what
// a pinned allowlist costs when a CDN reorganizes — every poster, silently.
// The host is already fixed to one pure asset CDN, so the segment charset
// (which excludes '@', ':' and '/', so the fetched URL cannot be steered to
// another origin) is what this needs to enforce.
func validHardcoverImagePath(path string) bool {
	if path == "" || len(path) > 512 {
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
