package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestLimiter creates a RateLimiter without the background cleanup goroutine.
func newTestLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		requests: make(map[string][]time.Time),
		limit:    limit,
		window:   window,
	}
}

func TestAllow_UnderLimit(t *testing.T) {
	rl := newTestLimiter(5, time.Minute)
	for i := 0; i < 5; i++ {
		if !rl.Allow("192.168.1.1") {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
}

func TestAllow_AtLimit(t *testing.T) {
	rl := newTestLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		rl.Allow("192.168.1.1")
	}
	if rl.Allow("192.168.1.1") {
		t.Fatal("request over limit should be rejected")
	}
}

func TestAllow_PerIP(t *testing.T) {
	rl := newTestLimiter(1, time.Minute)
	if !rl.Allow("10.0.0.1") {
		t.Fatal("first IP should be allowed")
	}
	if !rl.Allow("10.0.0.2") {
		t.Fatal("second IP should be allowed (independent limit)")
	}
	if rl.Allow("10.0.0.1") {
		t.Fatal("first IP should now be rejected")
	}
}

func TestMiddleware_Returns429(t *testing.T) {
	rl := newTestLimiter(1, time.Minute)

	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/login", nil)
	req.RemoteAddr = "1.2.3.4"

	// First request passes
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first request: got %d, want 200", rec.Code)
	}

	// Second request is rate-limited
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: got %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "60" {
		t.Fatalf("Retry-After: got %q, want \"60\"", got)
	}
}

func TestMiddleware_PassesThrough(t *testing.T) {
	rl := newTestLimiter(10, time.Minute)

	called := false
	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "5.6.7.8"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("next handler was not called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
}
