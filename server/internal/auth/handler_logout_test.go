package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestHandleLogout_RevokesOwnDevice pins the self-serve sign-out: a plain
// (non-admin) user's logout kills their own refresh token, and repeating it
// stays 200 — the already-revoked device is the goal state.
func TestHandleLogout_RevokesOwnDevice(t *testing.T) {
	svc := setupTestService(t)
	handler := NewHandler(svc)

	connect, err := svc.CreateConnectToken(1, "guest", "http://example.com")
	if err != nil {
		t.Fatalf("create connect token: %v", err)
	}
	connectURL, err := url.Parse(connect.Link)
	if err != nil {
		t.Fatalf("parse connect link: %v", err)
	}
	userLogin, err := svc.RedeemConnectToken(connectURL.Query().Get("token"), "Guest Phone", "guest-hw")
	if err != nil {
		t.Fatalf("redeem connect token: %v", err)
	}

	logout := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/auth/logout", nil)
		req = req.WithContext(context.WithValue(req.Context(), ClaimsKey, &Claims{
			UserID:   userLogin.User.ID,
			Username: userLogin.User.Username,
			Role:     RoleUser,
			DeviceID: userLogin.DeviceID,
		}))
		w := httptest.NewRecorder()
		handler.HandleLogout(w, req)
		return w
	}

	if w := logout(); w.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	if _, err := svc.Refresh(userLogin.RefreshToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("refresh after logout: err = %v, want ErrInvalidCredentials", err)
	}
	if w := logout(); w.Code != http.StatusOK {
		t.Fatalf("repeat logout status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
}

// A token without a device claim identifies no session to end.
func TestHandleLogout_NoDeviceClaimIs401(t *testing.T) {
	svc := setupTestService(t)
	handler := NewHandler(svc)

	req := httptest.NewRequest("POST", "/api/auth/logout", nil)
	req = req.WithContext(context.WithValue(req.Context(), ClaimsKey, &Claims{
		UserID: 1, Username: "admin", Role: RoleAdmin,
	}))
	w := httptest.NewRecorder()
	handler.HandleLogout(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("logout without device claim: status = %d, want 401", w.Code)
	}
}
