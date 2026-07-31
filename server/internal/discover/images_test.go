package discover

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestTraktImageRelaysAllowedHosts pins the relay contract: the request path
// maps onto the CDN host verbatim, bytes and Content-Type pass through, the
// response is client-cacheable, and no query parameters are forwarded.
func TestTraktImageRelaysAllowedHosts(t *testing.T) {
	e := newEnv(t, true)
	e.upstream.setRespond(func(*http.Request) (int, string) { return http.StatusOK, "IMAGEBYTES" })

	// media.trakt.tv is what Trakt serves today; the walter hosts are what it
	// served before July 2026 — the relay must follow their CDN migrations.
	for i, host := range []string{"media.trakt.tv", "walter.trakt.tv", "walter-r2.trakt.tv"} {
		path := fmt.Sprintf("/trakt/images/%s/images/movies/000/337/posters/thumb/faaa819377.jpg.webp?width=300", host)
		rec := e.do(t, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, body = %s", host, rec.Code, rec.Body.String())
		}
		if rec.Body.String() != "IMAGEBYTES" {
			t.Errorf("%s: body = %q, want the upstream bytes verbatim", host, rec.Body.String())
		}
		// The fake upstream stamps application/json on everything; seeing it
		// here proves the relay copies the upstream type instead of inventing
		// one or leaving the router group's default in place.
		if got := rec.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("%s: content-type = %q, want the upstream's passed through", host, got)
		}
		if got := rec.Header().Get("Cache-Control"); got != "public, max-age=86400" {
			t.Errorf("%s: cache-control = %q, want a day of client caching", host, got)
		}

		hit := e.upstream.hit(t, i)
		if hit.host != host {
			t.Errorf("upstream host = %s, want %s", hit.host, host)
		}
		if hit.path != "/images/movies/000/337/posters/thumb/faaa819377.jpg.webp" {
			t.Errorf("upstream path = %s, want the image path verbatim", hit.path)
		}
		if len(hit.query) != 0 {
			t.Errorf("upstream query = %v, want none forwarded", hit.query)
		}
	}
}

// TestTraktImageRejectsNonAllowlistedTargets pins the not-a-proxy contract:
// any host outside the walter allowlist and any path that is not a plain
// images/… file path is refused before an upstream connection is attempted.
func TestTraktImageRejectsNonAllowlistedTargets(t *testing.T) {
	e := newEnv(t, true)

	requests := []string{
		// Hosts outside *.trakt.tv, including lookalikes: a domain merely
		// ending in the letters, a trakt.tv prefix on an attacker's domain,
		// and the bare apex.
		"/trakt/images/image.tmdb.org/images/a.jpg",
		"/trakt/images/evil.example.com/images/a.jpg",
		"/trakt/images/notrakt.tv/images/a.jpg",
		"/trakt/images/media.trakt.tv.evil.com/images/a.jpg",
		"/trakt/images/trakt.tv/images/a.jpg",
		// The suffix check rejects a host whose real origin is the attacker's
		// ("media.trakt.tv" as userinfo), and the charset rejects '@' even
		// when the suffix looks right, so the built URL can never carry
		// userinfo, a port, or an odd label.
		"/trakt/images/media.trakt.tv@evil.com/images/a.jpg",
		"/trakt/images/evil.com@media.trakt.tv/images/a.jpg",
		"/trakt/images/MEDIA.TRAKT.TV/images/a.jpg",
		// Paths outside images/, traversal in both raw and encoded forms,
		// and characters walter filenames never contain.
		"/trakt/images/walter-r2.trakt.tv/posters/a.jpg",
		"/trakt/images/walter-r2.trakt.tv/images/../oauth/token",
		"/trakt/images/walter-r2.trakt.tv/images/%2e%2e/oauth/token",
		"/trakt/images/walter-r2.trakt.tv/images/a%20b.jpg",
		"/trakt/images/walter-r2.trakt.tv/",
	}
	for _, path := range requests {
		rec := e.do(t, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", path, rec.Code)
		}
	}
	if e.upstream.hitCount() != 0 {
		t.Fatalf("upstream hits = %d, want 0 — a rejected target must never be dialed", e.upstream.hitCount())
	}
}

// TestTraktImageUpstreamFailures maps CDN answers onto client statuses: a
// missing image is the client's 404, anything else upstream is a 502, and
// neither leaks the upstream body.
func TestTraktImageUpstreamFailures(t *testing.T) {
	e := newEnv(t, true)

	e.upstream.setRespond(func(*http.Request) (int, string) { return http.StatusNotFound, "walter says no" })
	rec := e.do(t, "/trakt/images/walter-r2.trakt.tv/images/movies/000/1/posters/a.jpg")
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "walter says no") {
		t.Errorf("missing image: status = %d body = %s, want a clean 404", rec.Code, rec.Body.String())
	}

	e.upstream.setRespond(func(*http.Request) (int, string) { return http.StatusInternalServerError, "cdn guts" })
	rec = e.do(t, "/trakt/images/walter-r2.trakt.tv/images/movies/000/1/posters/a.jpg")
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "cdn guts") {
		t.Errorf("cdn error: status = %d body = %s, want a clean 502", rec.Code, rec.Body.String())
	}
}

// TestTraktImageRequiresTraktConfigured mirrors the sibling /trakt/* posture:
// with no Trakt credential nothing hands out walter URLs, so the relay answers
// 503 without dialing anywhere.
func TestTraktImageRequiresTraktConfigured(t *testing.T) {
	e := newEnv(t, false)
	rec := e.do(t, "/trakt/images/walter-r2.trakt.tv/images/movies/000/1/posters/a.jpg")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "trakt not configured") {
		t.Errorf("status = %d body = %s, want 503 not-configured", rec.Code, rec.Body.String())
	}
	if e.upstream.hitCount() != 0 {
		t.Fatalf("upstream hits = %d, want 0 without credentials", e.upstream.hitCount())
	}
}
