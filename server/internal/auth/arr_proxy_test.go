package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// mockAccess is a stub InstanceAccessChecker for the proxy-gate tests.
type mockAccess struct {
	serviceType string
	exists      bool
	granted     bool
}

func (m mockAccess) LookupServiceType(instanceID string) (string, bool, error) {
	return m.serviceType, m.exists, nil
}

func (m mockAccess) UserHasInstanceAccess(userID int64, instanceID string) (bool, error) {
	return m.granted, nil
}

// AUTH-023: Requester arr proxy access is limited to the read allowlist.
func TestIsArrReadPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		// Allowlisted Radarr/Sonarr v3 read resources.
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

		// Allowlisted Chaptarr v1 read resources.
		{"/api/instances/abc/api/v1/author", true},
		{"/api/instances/abc/api/v1/author/7", true},
		{"/api/instances/abc/api/v1/book/123", true},
		{"/api/instances/abc/api/v1/book/lookup", true},
		{"/api/instances/abc/api/v1/bookfile", true},
		{"/api/instances/abc/api/v1/queue", true},
		{"/api/instances/abc/api/v1/history", true},
		{"/api/instances/abc/api/v1/wanted/missing", true},
		{"/api/instances/abc/api/v1/qualityprofile", true},
		{"/api/instances/abc/api/v1/metadataprofile", true},
		{"/api/instances/abc/api/v1/rootfolder", true},

		// Privileged / credential-bearing — must NOT be user-readable (v3 or v1).
		{"/api/instances/abc/api/v3/config/host", false},
		{"/api/instances/abc/api/v3/notification", false},
		{"/api/instances/abc/api/v3/downloadclient", false},
		{"/api/instances/abc/api/v3/indexer", false},
		{"/api/instances/abc/api/v3/importlist", false},
		{"/api/instances/abc/api/v3/system/status", false},
		{"/api/instances/abc/api/v3/command", false},
		{"/api/instances/abc/api/v3/release", false},
		{"/api/instances/abc/api/v1/config/host", false},
		{"/api/instances/abc/api/v1/command", false},
		{"/api/instances/abc/api/v1/release", false},

		// Path traversal must not escape the allowlist.
		{"/api/instances/abc/api/v3/movie/../config/host", false},
		{"/api/instances/abc/api/v1/author/../config/host", false},

		// Non-arr proxy paths (e.g. download clients) are not reads.
		{"/api/instances/abc/api/v2/torrents/info", false},
		{"/api/instances/abc/", false},
		{"/api/instances/abc/api/v3/", false},
		{"/api/instances/abc/api/v1/", false},
	}
	for _, tt := range tests {
		if got := isArrReadPath(tt.path); got != tt.want {
			t.Errorf("isArrReadPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

// withRouteContext attaches a chi route context carrying the {instanceID} URL
// param, as the real router would, so the middleware can resolve the instance.
func withRouteContext(req *http.Request, instanceID string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("instanceID", instanceID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// AUTH-023: Requester writes and privileged arr reads are denied.
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

	// For radarr/sonarr the access checker reports a non-gated service type, so
	// only the resource/permission rules apply.
	access := mockAccess{serviceType: "sonarr", exists: true}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextCalled := false
			h := RequireArrProxyAccess(access)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			}))

			req := withRouteContext(httptest.NewRequest(tt.method, tt.path, nil), "abc")
			req = req.WithContext(context.WithValue(req.Context(), ClaimsKey, &Claims{Role: tt.role, UserID: 1}))
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

// TestRequireArrProxyAccess_ChaptarrGate verifies the per-user access grant for
// chaptarr, which has no global default: a non-admin needs an explicit grant to
// touch the instance at all, admins bypass the grant, and even a granted
// non-admin still cannot perform writes (those require instances:manage).
// AUTH-023: Per-instance grants never broaden requester proxy access to writes.
func TestRequireArrProxyAccess_ChaptarrGate(t *testing.T) {
	tests := []struct {
		name    string
		role    string
		method  string
		path    string
		granted bool
		want    int
	}{
		{"granted user reads chaptarr", RoleUser, http.MethodGet, "/api/instances/chaptarr-1/api/v1/author", true, http.StatusOK},
		{"non-granted user blocked", RoleUser, http.MethodGet, "/api/instances/chaptarr-1/api/v1/author", false, http.StatusForbidden},
		{"granted user cannot write", RoleUser, http.MethodPost, "/api/instances/chaptarr-1/api/v1/release", true, http.StatusForbidden},
		{"admin reads without grant", RoleAdmin, http.MethodGet, "/api/instances/chaptarr-1/api/v1/author", false, http.StatusOK},
		{"admin writes without grant", RoleAdmin, http.MethodPost, "/api/instances/chaptarr-1/api/v1/release", false, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			access := mockAccess{serviceType: "chaptarr", exists: true, granted: tt.granted}
			nextCalled := false
			h := RequireArrProxyAccess(access)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			}))

			req := withRouteContext(httptest.NewRequest(tt.method, tt.path, nil), "chaptarr-1")
			req = req.WithContext(context.WithValue(req.Context(), ClaimsKey, &Claims{Role: tt.role, UserID: 7}))
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

// AUTH-023: Anonymous proxy access fails closed.
func TestRequireArrProxyAccess_RequiresClaims(t *testing.T) {
	h := RequireArrProxyAccess(mockAccess{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not run without claims")
	}))
	req := withRouteContext(httptest.NewRequest(http.MethodGet, "/api/instances/abc/api/v3/movie", nil), "abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
