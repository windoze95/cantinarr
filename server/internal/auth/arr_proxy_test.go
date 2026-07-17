package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// mockAccess is a stub InstanceAccessChecker for the proxy-gate tests.
type mockAccess struct {
	serviceType string
	exists      bool
	granted     bool
	lookupErr   error
	grantErr    error
	lookupCalls int
	grantCalls  int
}

func (m *mockAccess) LookupServiceType(instanceID string) (string, bool, error) {
	m.lookupCalls++
	return m.serviceType, m.exists, m.lookupErr
}

func (m *mockAccess) UserCanAccessInstance(userID int64, instanceID, serviceType string) (bool, error) {
	m.grantCalls++
	return m.granted, m.grantErr
}

// AUTH-023: Requester arr proxy access is service-, version-, and resource-bound.
func TestIsArrReadPath(t *testing.T) {
	tests := []struct {
		name        string
		serviceType string
		path        string
		want        bool
	}{
		// Allowlisted Radarr v3 read resources.
		{"radarr movie list", "radarr", "api/v3/movie", true},
		{"radarr movie detail", "radarr", "api/v3/movie/123", true},
		{"radarr calendar", "radarr", "api/v3/calendar", true},
		{"radarr queue", "radarr", "api/v3/queue", true},
		{"radarr history", "radarr", "api/v3/history", true},
		{"radarr wanted", "radarr", "api/v3/wanted/missing", true},

		// Allowlisted Sonarr v3 read resources.
		{"sonarr series", "sonarr", "api/v3/series", true},
		{"sonarr episode", "sonarr", "api/v3/episode", true},
		{"sonarr calendar", "sonarr", "api/v3/calendar", true},
		{"sonarr queue", "sonarr", "api/v3/queue", true},
		{"sonarr history", "sonarr", "api/v3/history", true},
		{"sonarr wanted", "sonarr", "api/v3/wanted/cutoff", true},

		// Allowlisted Chaptarr v1 read resources.
		{"chaptarr owned cover", "chaptarr", "api/v1/MediaCover/Books/9/cover.jpg", true},
		{"chaptarr author", "chaptarr", "api/v1/author", true},
		{"chaptarr author detail", "chaptarr", "api/v1/author/7", true},
		{"chaptarr book", "chaptarr", "api/v1/book/123", true},
		{"chaptarr book lookup", "chaptarr", "api/v1/book/lookup", true},
		{"chaptarr book file", "chaptarr", "api/v1/bookfile", true},
		{"chaptarr calendar", "chaptarr", "api/v1/calendar", true},
		{"chaptarr queue", "chaptarr", "api/v1/queue", true},
		{"chaptarr history", "chaptarr", "api/v1/history", true},
		{"chaptarr wanted", "chaptarr", "api/v1/wanted/missing", true},

		// Service identity and version are part of the allowlist.
		{"radarr cannot read sonarr resource", "radarr", "api/v3/series", false},
		{"sonarr cannot read radarr resource", "sonarr", "api/v3/movie", false},
		{"chaptarr cannot read radarr resource", "chaptarr", "api/v1/movie", false},
		{"radarr v1 rejected", "radarr", "api/v1/movie", false},
		{"sonarr v1 rejected", "sonarr", "api/v1/series", false},
		{"chaptarr v3 rejected", "chaptarr", "api/v3/author", false},
		{"download client rejected", "sabnzbd", "api/v3/movie", false},
		{"unknown service rejected", "future-arr", "api/v3/movie", false},

		// Privileged / credential-bearing resources are never requester-readable.
		{"config", "radarr", "api/v3/config/host", false},
		{"notification", "radarr", "api/v3/notification", false},
		{"download client config", "sonarr", "api/v3/downloadclient", false},
		{"indexer", "sonarr", "api/v3/indexer", false},
		{"import list", "radarr", "api/v3/importlist", false},
		{"system status", "sonarr", "api/v3/system/status", false},
		{"command", "radarr", "api/v3/command", false},
		{"release search", "sonarr", "api/v3/release", false},
		{"radarr external metadata lookup", "radarr", "api/v3/movie/lookup", false},
		{"sonarr external metadata lookup", "sonarr", "api/v3/series/lookup", false},
		{"chaptarr author metadata lookup", "chaptarr", "api/v1/author/lookup", false},
		{"radarr editor subroute", "radarr", "api/v3/movie/editor", false},
		{"history arbitrary subroute", "sonarr", "api/v3/history/series", false},
		{"chaptarr config", "chaptarr", "api/v1/config/host", false},
		{"chaptarr command", "chaptarr", "api/v1/command", false},
		{"chaptarr release search", "chaptarr", "api/v1/release", false},
		{"radarr quality profiles", "radarr", "api/v3/qualityprofile", false},
		{"radarr root folders", "radarr", "api/v3/rootfolder", false},
		{"sonarr quality profiles", "sonarr", "api/v3/qualityprofile", false},
		{"sonarr root folders", "sonarr", "api/v3/rootfolder", false},
		{"chaptarr quality profiles", "chaptarr", "api/v1/qualityprofile", false},
		{"chaptarr metadata profiles", "chaptarr", "api/v1/metadataprofile", false},
		{"chaptarr root folders", "chaptarr", "api/v1/rootfolder", false},
		{"chaptarr lookup cover proxy", "chaptarr", "api/v1/MediaCoverProxy/cover.jpg", false},
		{"chaptarr bare media cover", "chaptarr", "api/v1/MediaCover", false},
		{"chaptarr authors media cover", "chaptarr", "api/v1/MediaCover/Authors/9/fanart.jpg", false},
		{"chaptarr config media cover", "chaptarr", "api/v1/MediaCover/Config/9/secret.txt", false},
		{"chaptarr media cover without id", "chaptarr", "api/v1/MediaCover/Books/cover.jpg", false},
		{"chaptarr media cover zero id", "chaptarr", "api/v1/MediaCover/Books/0/cover.jpg", false},
		{"chaptarr lowercase media cover", "chaptarr", "api/v1/mediacover/Books/9/cover.jpg", false},
		{"radarr media cover remains admin only", "radarr", "api/v3/MediaCover/1/poster.jpg", false},

		// Traversal, embedded markers, and empty resources fail closed.
		{"radarr traversal", "radarr", "api/v3/movie/../config/host", false},
		{"chaptarr traversal", "chaptarr", "api/v1/author/../config/host", false},
		{"chaptarr cover traversal", "chaptarr", "api/v1/MediaCover/Books/9/../config.xml", false},
		{"marker embedded later", "radarr", "prefix/api/v3/movie", false},
		{"wrong API family", "radarr", "api/v2/torrents/info", false},
		{"empty path", "radarr", "", false},
		{"empty v3 resource", "radarr", "api/v3/", false},
		{"empty v1 resource", "chaptarr", "api/v1/", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isArrReadPath(tt.serviceType, tt.path); got != tt.want {
				t.Errorf("isArrReadPath(%q, %q) = %v, want %v", tt.serviceType, tt.path, got, tt.want)
			}
		})
	}
}

// withRouteContext attaches a chi route context carrying the {instanceID} URL
// param, as the real router would, so the middleware can resolve the instance.
func withRouteContext(req *http.Request, instanceID string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("instanceID", instanceID)
	prefix := "/api/instances/" + instanceID + "/"
	rctx.URLParams.Add("*", strings.TrimPrefix(req.URL.EscapedPath(), prefix))
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// AUTH-023: Requester writes and privileged arr reads are denied.
func TestRequireArrProxyAccess(t *testing.T) {
	tests := []struct {
		name        string
		role        string
		serviceType string
		method      string
		path        string
		want        int
	}{
		{"user reads movie library", RoleUser, "radarr", http.MethodGet, "/api/instances/abc/api/v3/movie", http.StatusOK},
		{"user reads calendar", RoleUser, "radarr", http.MethodGet, "/api/instances/abc/api/v3/calendar", http.StatusOK},
		{"user reads series", RoleUser, "sonarr", http.MethodGet, "/api/instances/abc/api/v3/series", http.StatusOK},
		{"user reads queue", RoleUser, "sonarr", http.MethodGet, "/api/instances/abc/api/v3/queue", http.StatusOK},
		{"user cannot read config", RoleUser, "radarr", http.MethodGet, "/api/instances/abc/api/v3/config/host", http.StatusForbidden},
		{"user cannot add movie", RoleUser, "radarr", http.MethodPost, "/api/instances/abc/api/v3/movie", http.StatusForbidden},
		{"user cannot replace movie", RoleUser, "radarr", http.MethodPut, "/api/instances/abc/api/v3/movie/1", http.StatusForbidden},
		{"user cannot patch movie", RoleUser, "radarr", http.MethodPatch, "/api/instances/abc/api/v3/movie/1", http.StatusForbidden},
		{"user cannot delete movie", RoleUser, "radarr", http.MethodDelete, "/api/instances/abc/api/v3/movie/1", http.StatusForbidden},
		{"user cannot run command", RoleUser, "sonarr", http.MethodPost, "/api/instances/abc/api/v3/command", http.StatusForbidden},
		{"user cannot interactive search", RoleUser, "sonarr", http.MethodGet, "/api/instances/abc/api/v3/release", http.StatusForbidden},
		{"unknown role cannot browse", "future-role", "radarr", http.MethodGet, "/api/instances/abc/api/v3/movie", http.StatusForbidden},
		{"admin reads config", RoleAdmin, "radarr", http.MethodGet, "/api/instances/abc/api/v3/config/host", http.StatusOK},
		{"admin adds movie", RoleAdmin, "radarr", http.MethodPost, "/api/instances/abc/api/v3/movie", http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			access := &mockAccess{serviceType: tt.serviceType, exists: true, granted: true}
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
			if tt.role == RoleAdmin && access.lookupCalls != 0 {
				t.Fatalf("admin lookup calls = %d, want 0", access.lookupCalls)
			}
			if tt.role == RoleUser && tt.method != http.MethodGet && access.lookupCalls != 0 {
				t.Fatalf("write lookup calls = %d, want 0", access.lookupCalls)
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
		name     string
		role     string
		method   string
		path     string
		granted  bool
		grantErr error
		want     int
	}{
		{"granted user reads chaptarr", RoleUser, http.MethodGet, "/api/instances/chaptarr-1/api/v1/author", true, nil, http.StatusOK},
		{"granted user reads owned cover", RoleUser, http.MethodGet, "/api/instances/chaptarr-1/api/v1/MediaCover/Books/9/cover.jpg", true, nil, http.StatusOK},
		{"non-granted user blocked", RoleUser, http.MethodGet, "/api/instances/chaptarr-1/api/v1/author", false, nil, http.StatusForbidden},
		{"grant lookup fault is retryable", RoleUser, http.MethodGet, "/api/instances/chaptarr-1/api/v1/author", false, errors.New("database unavailable"), http.StatusServiceUnavailable},
		{"granted user cannot write", RoleUser, http.MethodPost, "/api/instances/chaptarr-1/api/v1/release", true, nil, http.StatusForbidden},
		{"admin reads without grant", RoleAdmin, http.MethodGet, "/api/instances/chaptarr-1/api/v1/author", false, nil, http.StatusOK},
		{"admin writes without grant", RoleAdmin, http.MethodPost, "/api/instances/chaptarr-1/api/v1/release", false, nil, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			access := &mockAccess{serviceType: "chaptarr", exists: true, granted: tt.granted, grantErr: tt.grantErr}
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
			if tt.role == RoleAdmin && (access.lookupCalls != 0 || access.grantCalls != 0) {
				t.Fatalf("admin access calls = lookup:%d grant:%d, want zero", access.lookupCalls, access.grantCalls)
			}
			if tt.method != http.MethodGet && tt.role != RoleAdmin && (access.lookupCalls != 0 || access.grantCalls != 0) {
				t.Fatalf("write access calls = lookup:%d grant:%d, want zero", access.lookupCalls, access.grantCalls)
			}
		})
	}
}

// AUTH-023: Non-arr, missing, unknown, and unavailable instance lookups fail closed.
func TestRequireArrProxyAccess_ServiceBoundary(t *testing.T) {
	tests := []struct {
		name        string
		serviceType string
		exists      bool
		lookupErr   error
		path        string
		want        int
	}{
		{"radarr read admitted", "radarr", true, nil, "/api/instances/abc/api/v3/movie", http.StatusOK},
		{"sonarr read admitted", "sonarr", true, nil, "/api/instances/abc/api/v3/series", http.StatusOK},
		{"radarr rejects sonarr resource", "radarr", true, nil, "/api/instances/abc/api/v3/series", http.StatusForbidden},
		{"radarr rejects v1", "radarr", true, nil, "/api/instances/abc/api/v1/movie", http.StatusForbidden},
		{"sonarr rejects v1", "sonarr", true, nil, "/api/instances/abc/api/v1/series", http.StatusForbidden},
		{"sabnzbd rejects arr-shaped path", "sabnzbd", true, nil, "/api/instances/abc/api/v3/movie", http.StatusForbidden},
		{"qbittorrent rejects arr-shaped path", "qbittorrent", true, nil, "/api/instances/abc/api/v3/movie", http.StatusForbidden},
		{"nzbget rejects arr-shaped path", "nzbget", true, nil, "/api/instances/abc/api/v3/movie", http.StatusForbidden},
		{"transmission rejects arr-shaped path", "transmission", true, nil, "/api/instances/abc/api/v3/movie", http.StatusForbidden},
		{"tautulli rejects arr-shaped path", "tautulli", true, nil, "/api/instances/abc/api/v3/movie", http.StatusForbidden},
		{"unknown service rejects arr-shaped path", "future-service", true, nil, "/api/instances/abc/api/v3/movie", http.StatusForbidden},
		{"empty service rejects arr-shaped path", "", true, nil, "/api/instances/abc/api/v3/movie", http.StatusForbidden},
		{"missing instance is not found", "", false, nil, "/api/instances/abc/api/v3/movie", http.StatusNotFound},
		{"lookup fault is retryable", "", false, errors.New("database unavailable"), "/api/instances/abc/api/v3/movie", http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			access := &mockAccess{
				serviceType: tt.serviceType,
				exists:      tt.exists,
				lookupErr:   tt.lookupErr,
				granted:     tt.want == http.StatusOK,
			}
			nextCalled := false
			h := RequireArrProxyAccess(access)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			}))

			req := withRouteContext(httptest.NewRequest(http.MethodGet, tt.path, nil), "abc")
			req = req.WithContext(context.WithValue(req.Context(), ClaimsKey, &Claims{Role: RoleUser, UserID: 7}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.want, rec.Body.String())
			}
			if wantNext := tt.want == http.StatusOK; nextCalled != wantNext {
				t.Fatalf("nextCalled = %v, want %v", nextCalled, wantNext)
			}
			if access.lookupCalls != 1 {
				t.Fatalf("lookup calls = %d, want 1", access.lookupCalls)
			}
			wantGrantCalls := 0
			if tt.want == http.StatusOK {
				wantGrantCalls = 1
			}
			if access.grantCalls != wantGrantCalls {
				t.Fatalf("effective-instance calls = %d, want %d", access.grantCalls, wantGrantCalls)
			}
			if tt.lookupErr != nil && strings.Contains(rec.Body.String(), tt.lookupErr.Error()) {
				t.Fatalf("lookup error leaked in response: %s", rec.Body.String())
			}
		})
	}

	t.Run("missing route parameter", func(t *testing.T) {
		access := &mockAccess{serviceType: "radarr", exists: true}
		h := RequireArrProxyAccess(access)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("handler should not run without an instance ID")
		}))
		req := withRouteContext(httptest.NewRequest(http.MethodGet, "/api/instances/abc/api/v3/movie", nil), "")
		req = req.WithContext(context.WithValue(req.Context(), ClaimsKey, &Claims{Role: RoleUser, UserID: 7}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
		}
		if access.lookupCalls != 0 {
			t.Fatalf("lookup calls = %d, want 0", access.lookupCalls)
		}
	})

	t.Run("admin bypasses failing lookup", func(t *testing.T) {
		access := &mockAccess{serviceType: "sabnzbd", exists: true, lookupErr: errors.New("database unavailable")}
		nextCalled := false
		h := RequireArrProxyAccess(access)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nextCalled = true
			w.WriteHeader(http.StatusOK)
		}))
		req := withRouteContext(httptest.NewRequest(http.MethodPost, "/api/instances/abc/api", nil), "abc")
		req = req.WithContext(context.WithValue(req.Context(), ClaimsKey, &Claims{Role: RoleAdmin, UserID: 1}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK || !nextCalled {
			t.Fatalf("status = %d nextCalled = %v, want 200/true", rec.Code, nextCalled)
		}
		if access.lookupCalls != 0 || access.grantCalls != 0 {
			t.Fatalf("admin access calls = lookup:%d grant:%d, want zero", access.lookupCalls, access.grantCalls)
		}
	})
}

// AUTH-023: A valid read path is still bound to the requester's one effective instance.
func TestRequireArrProxyAccess_EffectiveInstanceBoundary(t *testing.T) {
	tests := []struct {
		name      string
		allowed   bool
		accessErr error
		want      int
	}{
		{name: "effective instance admitted", allowed: true, want: http.StatusOK},
		{name: "hidden sibling denied", want: http.StatusForbidden},
		{name: "effective instance lookup fault is retryable", accessErr: errors.New("database unavailable"), want: http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			access := &mockAccess{
				serviceType: "radarr",
				exists:      true,
				granted:     tt.allowed,
				grantErr:    tt.accessErr,
			}
			nextCalled := false
			h := RequireArrProxyAccess(access)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			}))
			req := withRouteContext(
				httptest.NewRequest(http.MethodGet, "/api/instances/radarr-sibling/api/v3/movie", nil),
				"radarr-sibling",
			)
			req = req.WithContext(context.WithValue(req.Context(), ClaimsKey, &Claims{Role: RoleUser, UserID: 7}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.want, rec.Body.String())
			}
			if nextCalled != (tt.want == http.StatusOK) {
				t.Fatalf("nextCalled = %v, want %v", nextCalled, tt.want == http.StatusOK)
			}
			if access.lookupCalls != 1 || access.grantCalls != 1 {
				t.Fatalf("access calls = lookup:%d effective:%d, want 1/1", access.lookupCalls, access.grantCalls)
			}
			if tt.accessErr != nil && strings.Contains(rec.Body.String(), tt.accessErr.Error()) {
				t.Fatalf("effective-instance error leaked in response: %s", rec.Body.String())
			}
		})
	}
}

// AUTH-023: Encoded path traversal cannot escape an allowlisted arr resource.
func TestRequireArrProxyAccess_RejectsEncodedTraversal(t *testing.T) {
	for _, path := range []string{
		"/api/instances/abc/api/v3/movie/%2e%2e/config/host",
		"/api/instances/abc/api/v3/movie/%2E%2E/config/host",
		"/api/instances/abc/api/v3/movie%2f..%2fconfig/host",
		"/api/instances/abc/api/v3/movie/%252e%252e/config/host",
		"/api/instances/abc/api/v3/movie%252f..%252fconfig/host",
	} {
		t.Run(path, func(t *testing.T) {
			access := &mockAccess{serviceType: "radarr", exists: true, granted: true}
			nextCalled := false
			router := chi.NewRouter()
			router.With(RequireArrProxyAccess(access)).Handle("/api/instances/{instanceID}/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, path, nil)
			req = req.WithContext(context.WithValue(req.Context(), ClaimsKey, &Claims{Role: RoleUser, UserID: 7}))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
			}
			if nextCalled {
				t.Fatal("handler ran for encoded traversal")
			}
			if access.lookupCalls != 1 || access.grantCalls != 0 {
				t.Fatalf(
					"access calls = lookup:%d effective:%d, want 1/0 (path classification must reject before the effective-instance lookup)",
					access.lookupCalls,
					access.grantCalls,
				)
			}
		})
	}
}

// AUTH-023: Anonymous proxy access fails closed.
func TestRequireArrProxyAccess_RequiresClaims(t *testing.T) {
	h := RequireArrProxyAccess(&mockAccess{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not run without claims")
	}))
	req := withRouteContext(httptest.NewRequest(http.MethodGet, "/api/instances/abc/api/v3/movie", nil), "abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
