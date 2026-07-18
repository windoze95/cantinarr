package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests pin the CORS posture the /api router actually ships (#231).
//
// The middleware is configured with cors.Handler(AllowedOrigins: []string{})
// under a "same-origin only" comment, but go-chi/cors v1.2.2 treats an empty
// origin allowlist as ALLOW ALL ORIGINS (allowedOriginsAll), so cross-origin
// requests are answered with Access-Control-Allow-Origin: * — the comment and
// the behavior disagree. What keeps the wildcard from being a session-riding
// hole is AllowCredentials: false plus Bearer-token auth (no cookies): a
// cross-origin page can never attach a victim's credentials, so the wildcard
// only makes non-credentialed responses readable cross-origin. These tests
// pin exactly that shipped shape; tightening the allowlist to genuinely
// same-origin is a deliberate change that must flip these assertions.
// (The separate /mcp mount configures AllowedOrigins: ["*"] explicitly for
// external MCP clients — that wildcard is intended, not covered here.)

// corsRequest performs one request against the full router and returns the
// recorder. token and origin are optional ("" omits the header).
func corsRequest(router http.Handler, method, path, token, origin string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// assertNoCORSHeaders fails if any Access-Control-* header is present.
func assertNoCORSHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	for key := range rec.Header() {
		if strings.HasPrefix(key, "Access-Control-") {
			t.Errorf("unexpected CORS header %s: %q", key, rec.Header().Get(key))
		}
	}
}

// TestAPICORSActualRequestPosture pins the headers a cross-origin (and an
// origin-less) request receives on a representative public route and a
// representative authenticated route.
func TestAPICORSActualRequestPosture(t *testing.T) {
	harness := newRBACRouterHarness(t, false)

	routes := []struct {
		name  string
		path  string
		token string
	}{
		{"public health", "/api/health", ""},
		{"authenticated config", "/api/config", harness.adminToken},
	}
	for _, route := range routes {
		t.Run(route.name+" cross-origin", func(t *testing.T) {
			rec := corsRequest(harness.router, http.MethodGet, route.path, route.token, "https://attacker.example", nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			// Shipped behavior: the empty allowlist is allow-all, so the
			// wildcard is reflected (see the file comment; #231).
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
				t.Fatalf("Access-Control-Allow-Origin = %q, want %q (go-chi/cors treats the empty allowlist as allow-all)", got, "*")
			}
			// The compensating control: the wildcard must never come with
			// permission to send credentials.
			if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
				t.Fatalf("Access-Control-Allow-Credentials = %q, want unset", got)
			}
			if got := rec.Header().Get("Access-Control-Expose-Headers"); got != "Link" {
				t.Fatalf("Access-Control-Expose-Headers = %q, want %q", got, "Link")
			}
		})

		// Native clients and the same-origin web build send no Origin header
		// (browsers omit it on same-origin GETs): the response must carry no
		// Access-Control-* headers at all.
		t.Run(route.name+" without origin", func(t *testing.T) {
			rec := corsRequest(harness.router, http.MethodGet, route.path, route.token, "", nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			assertNoCORSHeaders(t, rec)
		})
	}
}

// TestAPICORSPreflightPosture pins preflight handling: the CORS middleware
// answers OPTIONS before auth ever runs (a browser preflight carries no
// credentials, so 200-not-401 on a protected route is the standard shape),
// reflecting the allow-all wildcard without a credential grant.
func TestAPICORSPreflightPosture(t *testing.T) {
	harness := newRBACRouterHarness(t, false)

	for _, path := range []string{"/api/health", "/api/config"} {
		t.Run(path, func(t *testing.T) {
			rec := corsRequest(harness.router, http.MethodOptions, path, "", "https://attacker.example", map[string]string{
				"Access-Control-Request-Method": http.MethodGet,
			})
			if rec.Code != http.StatusOK {
				t.Fatalf("preflight status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
				t.Fatalf("Access-Control-Allow-Origin = %q, want %q (go-chi/cors treats the empty allowlist as allow-all)", got, "*")
			}
			if got := rec.Header().Get("Access-Control-Allow-Methods"); got != http.MethodGet {
				t.Fatalf("Access-Control-Allow-Methods = %q, want %q", got, http.MethodGet)
			}
			if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
				t.Fatalf("Access-Control-Allow-Credentials = %q, want unset", got)
			}
			if got := rec.Header().Get("Access-Control-Max-Age"); got != "300" {
				t.Fatalf("Access-Control-Max-Age = %q, want %q", got, "300")
			}
		})
	}
}
