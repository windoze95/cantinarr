package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A direct connection carries "ip:port" in RemoteAddr with a fresh port per
// connection; the limit is per IP, so the port must not split the budget.
func TestRateLimiterKeysByIPNotByConnection(t *testing.T) {
	rl := NewRateLimiter(2, time.Minute)
	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	serve := func(remote string) int {
		req := httptest.NewRequest(http.MethodPost, "/login", nil)
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := serve("10.0.0.1:1111"); got != http.StatusNoContent {
		t.Fatalf("first = %d", got)
	}
	if got := serve("10.0.0.1:2222"); got != http.StatusNoContent {
		t.Fatalf("second = %d", got)
	}
	if got := serve("10.0.0.1:3333"); got != http.StatusTooManyRequests {
		t.Fatalf("third from the same IP on a new connection = %d, want 429", got)
	}
	// Another IP has its own budget; so does a proxy-reported bare address.
	if got := serve("10.0.0.2:1111"); got != http.StatusNoContent {
		t.Fatalf("other ip = %d", got)
	}
	if got := serve("203.0.113.9"); got != http.StatusNoContent {
		t.Fatalf("bare address = %d", got)
	}
}
