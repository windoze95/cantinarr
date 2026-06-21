package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsArrReadPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		// Allowlisted read resources.
		{"/api/instances/abc/api/v3/movie", true},
		{"/api/instances/abc/api/v3/movie/123", true},
		{"/api/instances/abc/api/v3/movie/lookup", true},
		{"/api/instances/abc/api/v3/series", true},
		{"/api/instances/abc/api/v3/episode", true},
		{"/api/instances/abc/api/v3/calendar", true},
		{"/api/instances/abc/api/v3/queue", true},
		{"/api/instances/abc/api/v3/history", true},
		{"/api/instances/abc/api/v3/wanted/missing", true},
		{"/api/instances/abc/api/v3/qualityprofile", true},
		{"/api/instances/abc/api/v3/rootfolder", true},

		// Privileged / credential-bearing — must NOT be user-readable.
		{"/api/instances/abc/api/v3/config/host", false},
		{"/api/instances/abc/api/v3/notification", false},
		{"/api/instances/abc/api/v3/downloadclient", false},
		{"/api/instances/abc/api/v3/indexer", false},
		{"/api/instances/abc/api/v3/importlist", false},
		{"/api/instances/abc/api/v3/system/status", false},
		{"/api/instances/abc/api/v3/command", false},
		{"/api/instances/abc/api/v3/release", false},

		// Path traversal must not escape the allowlist.
		{"/api/instances/abc/api/v3/movie/../config/host", false},

		// Non-arr / non-v3 proxy paths (e.g. download clients) are not reads.
		{"/api/instances/abc/api/v2/torrents/info", false},
		{"/api/instances/abc/", false},
		{"/api/instances/abc/api/v3/", false},
	}
	for _, tt := range tests {
		if got := isArrReadPath(tt.path); got != tt.want {
			t.Errorf("isArrReadPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestRequireArrProxyAccess(t *testing.T) {
	tests := []struct {
		name   string
		role   string
		method string
		path   string
		want   int
	}{
		{"user reads movie library", RoleUser, http.MethodGet, "/api/instances/abc/api/v3/movie", http.StatusOK},
		{"user reads calendar", RoleUser, http.MethodGet, "/api/instances/abc/api/v3/calendar", http.StatusOK},
		{"user reads series", RoleUser, http.MethodGet, "/api/instances/abc/api/v3/series", http.StatusOK},
		{"user reads queue", RoleUser, http.MethodGet, "/api/instances/abc/api/v3/queue", http.StatusOK},
		{"user cannot read config", RoleUser, http.MethodGet, "/api/instances/abc/api/v3/config/host", http.StatusForbidden},
		{"user cannot add movie", RoleUser, http.MethodPost, "/api/instances/abc/api/v3/movie", http.StatusForbidden},
		{"user cannot delete movie", RoleUser, http.MethodDelete, "/api/instances/abc/api/v3/movie/1", http.StatusForbidden},
		{"user cannot interactive search", RoleUser, http.MethodGet, "/api/instances/abc/api/v3/release", http.StatusForbidden},
		{"admin reads config", RoleAdmin, http.MethodGet, "/api/instances/abc/api/v3/config/host", http.StatusOK},
		{"admin adds movie", RoleAdmin, http.MethodPost, "/api/instances/abc/api/v3/movie", http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextCalled := false
			h := RequireArrProxyAccess(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(tt.method, tt.path, nil)
			req = req.WithContext(context.WithValue(req.Context(), ClaimsKey, &Claims{Role: tt.role}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
			if wantNext := tt.want == http.StatusOK; nextCalled != wantNext {
				t.Fatalf("nextCalled = %v, want %v", nextCalled, wantNext)
			}
		})
	}
}

func TestRequireArrProxyAccess_RequiresClaims(t *testing.T) {
	h := RequireArrProxyAccess(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not run without claims")
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/instances/abc/api/v3/movie", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
