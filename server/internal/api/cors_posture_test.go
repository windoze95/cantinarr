package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests pin the /api CORS posture: genuinely same-origin only (#231).
//
// No CORS middleware is mounted on /api, so no request — whatever its Origin —
// ever receives an Access-Control-* header. Browsers therefore enforce their
// default same-origin policy: a cross-origin page can neither read responses
// nor complete a preflight. The same-origin web build, native apps, and
// server-side MCP clients never need CORS headers, so their flows are
// unaffected. (The separate /mcp mount configures AllowedOrigins: ["*"]
// explicitly for external MCP clients — that wildcard is intended, not
// covered here.)
//
// History: an empty go-chi/cors allowlist previously sat on /api under a
// "same-origin only" comment, but go-chi/cors treats an empty allowlist as
// ALLOW ALL ORIGINS, reflecting Access-Control-Allow-Origin: * to anyone.
// The exposure was bounded (AllowCredentials: false plus cookie-less Bearer
// auth meant no credentialed cross-origin reads), but behavior and intent
// disagreed; the middleware was removed to make them agree.

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

// TestAPICORSActualRequestPosture pins that neither cross-origin nor
// origin-less requests receive any Access-Control-* header on a
// representative public route and a representative authenticated route.
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
			// The request itself succeeds (the server does not reject on
			// Origin), but without an Access-Control-Allow-Origin grant the
			// browser refuses to hand the response to cross-origin callers.
			assertNoCORSHeaders(t, rec)
		})

		t.Run(route.name+" without origin", func(t *testing.T) {
			rec := corsRequest(harness.router, http.MethodGet, route.path, route.token, "", nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			assertNoCORSHeaders(t, rec)
		})
	}
}

// TestAPICORSPreflightPosture pins that browser preflights get no CORS grant:
// with no middleware answering OPTIONS, the router's method handling responds
// (405 for these GET routes) and no Access-Control-* header appears, so a
// cross-origin preflight can never succeed.
func TestAPICORSPreflightPosture(t *testing.T) {
	harness := newRBACRouterHarness(t, false)

	for _, path := range []string{"/api/health", "/api/config"} {
		t.Run(path, func(t *testing.T) {
			rec := corsRequest(harness.router, http.MethodOptions, path, "", "https://attacker.example", map[string]string{
				"Access-Control-Request-Method": http.MethodGet,
			})
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("preflight status = %d, want %d; body=%s", rec.Code, http.StatusMethodNotAllowed, rec.Body.String())
			}
			assertNoCORSHeaders(t, rec)
		})
	}
}
