package plex

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// newInviteHandlerEnv wires the real service — linked account (token
// "secret-token" via link), configured server, seeded user 7 with a shared
// email — behind the production route pattern, so tests exercise the same chi
// param extraction InviteUser sees in NewRouter.
func newInviteHandlerEnv(t *testing.T) (*fakeAPI, http.Handler) {
	t.Helper()
	svc, api, _ := newTestService(t)
	link(t, svc, api)
	if err := svc.UpdateSettings("machine-1", "Media", []int64{1, 2}, false); err != nil {
		t.Fatalf("configure invites: %v", err)
	}
	seedUser(t, svc, 7, "alice", "alice@example.com")
	h := NewHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	router := chi.NewRouter()
	router.Post("/api/admin/users/{userID}/plex-invite", h.InviteUser)
	return api, router
}

// TestInviteUserHandlerResponseMapping pins the handler's HTTP translation of
// invite outcomes. The client maps plex.tv's 422 duplicate-share answer to
// ErrAlreadyShared (client_test.go); the handler must present that as a 200
// "already_shared" — an idempotent success, not an error — while a generic
// upstream failure stays 502 with a fixed body that never echoes upstream
// detail. No response may contain the linked Plex token.
func TestInviteUserHandlerResponseMapping(t *testing.T) {
	upstreamDetail := "plex.tv exploded; token=secret-token request-id=abc123"
	tests := []struct {
		name       string
		inviteErr  error
		wantStatus int
		wantBody   map[string]string
	}{
		{
			name:       "fresh invite",
			inviteErr:  nil,
			wantStatus: http.StatusOK,
			wantBody:   map[string]string{"status": "invited", "email": "alice@example.com"},
		},
		{
			name:       "duplicate share is an idempotent success",
			inviteErr:  ErrAlreadyShared,
			wantStatus: http.StatusOK,
			wantBody:   map[string]string{"status": "already_shared", "email": "alice@example.com"},
		},
		{
			name:       "generic upstream failure",
			inviteErr:  errors.New(upstreamDetail),
			wantStatus: http.StatusBadGateway,
			wantBody:   map[string]string{"error": "Plex invite failed"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, router := newInviteHandlerEnv(t)
			api.inviteErr = tt.inviteErr

			req := httptest.NewRequest(http.MethodPost, "/api/admin/users/7/plex-invite", strings.NewReader("{}"))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", got)
			}
			raw := rec.Body.String()
			for _, sentinel := range []string{"secret-token", "request-id=abc123", "exploded"} {
				if strings.Contains(raw, sentinel) {
					t.Fatalf("response leaked %q: %s", sentinel, raw)
				}
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response %q: %v", raw, err)
			}
			if len(body) != len(tt.wantBody) {
				t.Fatalf("body = %#v, want exactly %#v", body, tt.wantBody)
			}
			for key, want := range tt.wantBody {
				if body[key] != want {
					t.Fatalf("body[%q] = %q, want %q", key, body[key], want)
				}
			}
		})
	}
}
