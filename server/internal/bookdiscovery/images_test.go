package bookdiscovery

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
)

// coverUpstream stands in for assets.hardcover.app. The relay's client holds an
// httpx.External() transport, which with no admin proxy configured delegates to
// http.DefaultTransport per request — so swapping that intercepts the CDN call
// and records what would have been dialed.
type coverUpstream struct {
	mu      sync.Mutex
	hits    []*http.Request
	status  int
	body    string
	ctype   string
	respond func(*http.Request) (int, string)
}

func (u *coverUpstream) RoundTrip(req *http.Request) (*http.Response, error) {
	u.mu.Lock()
	u.hits = append(u.hits, req)
	status, body, ctype := u.status, u.body, u.ctype
	respond := u.respond
	u.mu.Unlock()
	if respond != nil {
		status, body = respond(req)
	}
	header := http.Header{}
	if ctype != "" {
		header.Set("Content-Type", ctype)
	}
	return &http.Response{
		StatusCode:    status,
		Body:          io.NopCloser(strings.NewReader(body)),
		Header:        header,
		ContentLength: int64(len(body)),
		Request:       req,
	}, nil
}

func (u *coverUpstream) hitCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.hits)
}

func (u *coverUpstream) hit(t *testing.T, i int) *http.Request {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	if i >= len(u.hits) {
		t.Fatalf("upstream hit %d requested, only %d recorded", i, len(u.hits))
	}
	return u.hits[i]
}

func newCoverEnv(t *testing.T) (*chi.Mux, *coverUpstream) {
	t.Helper()
	upstream := &coverUpstream{status: http.StatusOK, body: "COVERBYTES", ctype: "image/jpeg"}
	previous := http.DefaultTransport
	http.DefaultTransport = upstream
	t.Cleanup(func() { http.DefaultTransport = previous })

	router := chi.NewRouter()
	router.Get("/discover/books/images/*", CoverImage)
	return router, upstream
}

func doCover(router *chi.Mux, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// TestCoverImageRelaysHardcoverAssets pins the relay contract: the request path
// maps onto the CDN path verbatim, bytes and Content-Type pass through, the
// response is client-cacheable, and no query parameters are forwarded.
func TestCoverImageRelaysHardcoverAssets(t *testing.T) {
	router, upstream := newCoverEnv(t)

	// Hardcover serves covers under both `edition/` and `editions/`, and its
	// filenames carry dots and underscores.
	paths := []string{
		"/edition/31601422/3a01ea07-01f4-45a2-9940-6fc7e00e0d08.jpeg",
		"/editions/3274049/8741341047797682-91mYu67RfUL._SL1500_.jpg",
	}
	for i, path := range paths {
		rec := doCover(router, "/discover/books/images"+path+"?w=300")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, body = %s", path, rec.Code, rec.Body.String())
		}
		if rec.Body.String() != "COVERBYTES" {
			t.Errorf("%s: body = %q, want the upstream bytes verbatim", path, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); got != "image/jpeg" {
			t.Errorf("%s: content-type = %q, want the upstream's passed through", path, got)
		}
		if got := rec.Header().Get("Cache-Control"); got != "public, max-age=86400" {
			t.Errorf("%s: cache-control = %q, want a day of client caching", path, got)
		}

		hit := upstream.hit(t, i)
		if hit.URL.Host != hardcoverAssetHost {
			t.Errorf("upstream host = %s, want %s", hit.URL.Host, hardcoverAssetHost)
		}
		if hit.URL.Path != path {
			t.Errorf("upstream path = %s, want %s verbatim", hit.URL.Path, path)
		}
		if hit.URL.RawQuery != "" {
			t.Errorf("upstream query = %q, want none forwarded", hit.URL.RawQuery)
		}
	}
}

// TestCoverImageRejectsNonAssetPaths pins the not-a-proxy contract. The host is
// hardcoded, so the path is the only caller-controlled input: traversal in raw
// and encoded forms, and any character a CDN filename never carries, must be
// refused before an upstream connection is attempted.
func TestCoverImageRejectsNonAssetPaths(t *testing.T) {
	router, upstream := newCoverEnv(t)

	for _, path := range []string{
		"/discover/books/images/",
		"/discover/books/images/../oauth/token",
		"/discover/books/images/%2e%2e/oauth/token",
		"/discover/books/images/edition/../../etc/passwd",
		"/discover/books/images/edition/a%20b.jpg",
		// A '@' or ':' in a segment is what would let a crafted path graft
		// userinfo or a port onto the built URL and steer it off-host.
		"/discover/books/images/evil.com@assets.hardcover.app/a.jpg",
		"/discover/books/images/edition/1/a:80.jpg",
	} {
		rec := doCover(router, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", path, rec.Code)
		}
	}
	if upstream.hitCount() != 0 {
		t.Fatalf("upstream hits = %d, want 0 — a rejected target must never be dialed", upstream.hitCount())
	}
}

// TestCoverImageUpstreamFailures maps CDN answers onto client statuses: a
// missing cover is the client's 404, anything else upstream is a 502, and
// neither leaks the upstream body.
func TestCoverImageUpstreamFailures(t *testing.T) {
	router, upstream := newCoverEnv(t)
	const path = "/discover/books/images/edition/1/cover.jpg"

	upstream.respond = func(*http.Request) (int, string) { return http.StatusNotFound, "hardcover says no" }
	rec := doCover(router, path)
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "hardcover says no") {
		t.Errorf("missing cover: status = %d body = %s, want a clean 404", rec.Code, rec.Body.String())
	}

	upstream.respond = func(*http.Request) (int, string) { return http.StatusInternalServerError, "cdn guts" }
	rec = doCover(router, path)
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "cdn guts") {
		t.Errorf("cdn error: status = %d body = %s, want a clean 502", rec.Code, rec.Body.String())
	}
}
