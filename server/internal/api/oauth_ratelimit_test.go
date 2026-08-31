package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestOAuthCredentialEndpointsAreRateLimited pins the /oauth front-door
// posture: every unauthenticated endpoint that accepts a credential or mints
// server-side state shares one 10/minute/IP budget, matching /api/auth. The
// password form on POST /oauth/authorize is the load-bearing case — without a
// limiter it is a second, uncapped login form for brute-forcing accounts.
func TestOAuthCredentialEndpointsAreRateLimited(t *testing.T) {
	harness := newRBACRouterHarness(t, false)

	junkAuthorize := url.Values{
		"response_type": {"code"},
		"client_id":     {"missing-client"},
		"username":      {"admin"},
		"password":      {"wrong-password"},
	}.Encode()

	postForm := func(path string) int {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(junkAuthorize))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		recorder := httptest.NewRecorder()
		harness.router.ServeHTTP(recorder, request)
		return recorder.Code
	}

	// The shared budget admits exactly 10 requests per IP per window.
	for attempt := 1; attempt <= 10; attempt++ {
		if status := postForm("/oauth/authorize"); status == http.StatusTooManyRequests {
			t.Fatalf("authorize attempt %d was limited before the 10-request budget was spent", attempt)
		}
	}

	// Once spent, every credential endpoint answers 429 from that same budget.
	for _, path := range []string{
		"/oauth/authorize",
		"/oauth/register",
		"/oauth/passkey/login/begin",
		"/oauth/passkey/login/finish",
		"/oauth/token",
	} {
		if status := postForm(path); status != http.StatusTooManyRequests {
			t.Errorf("POST %s after spent budget = %d, want 429", path, status)
		}
	}

	// Discovery metadata and the login form render stay unlimited: MCP client
	// discovery must keep working from an IP that just exhausted the budget.
	for _, path := range []string{
		"/.well-known/oauth-authorization-server",
		"/.well-known/oauth-protected-resource/mcp",
		"/oauth/authorize",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		recorder := httptest.NewRecorder()
		harness.router.ServeHTTP(recorder, request)
		if recorder.Code == http.StatusTooManyRequests {
			t.Errorf("GET %s was rate limited; discovery and the form render must stay reachable", path)
		}
	}
}
