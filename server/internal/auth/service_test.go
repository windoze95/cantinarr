package auth

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/db"
)

func setupTestService(t *testing.T) *Service {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	svc := NewService(database, "test-secret-key")
	if err := svc.EnsureAdmin("testpass123"); err != nil {
		t.Fatalf("ensure admin: %v", err)
	}
	return svc
}

func TestNativePasskeyStatusFromRequest(t *testing.T) {
	svc := NewService(nil, "test-secret-key", WebAuthnConfig{
		AppleAppIDs:             []string{"TEAMID.codes.julian.cantinarr"},
		AndroidCertFingerprints: []string{"AA:BB"},
	})

	req := httptest.NewRequest("GET", "https://example.com/api/auth/status", nil)
	status := svc.nativePasskeyStatusFromRequest(req)
	if !status.AppleConfigured || !status.AndroidConfigured || !status.WindowsOriginTrusted {
		t.Fatalf("status = %+v, want all native passkey surfaces configured", status)
	}

	req = httptest.NewRequest("GET", "https://127.0.0.1/api/auth/status", nil)
	status = svc.nativePasskeyStatusFromRequest(req)
	if status.AppleConfigured || status.AndroidConfigured {
		t.Fatalf("status = %+v, want Apple and Android unavailable on IP hosts", status)
	}

	req = httptest.NewRequest("GET", "https://example.com:8585/api/auth/status", nil)
	status = svc.nativePasskeyStatusFromRequest(req)
	if status.WindowsOriginTrusted {
		t.Fatalf("WindowsOriginTrusted = true for non-default port without extra origin")
	}

	svc = NewService(nil, "test-secret-key", WebAuthnConfig{
		ExtraOrigins: []string{"https://example.com"},
	})
	status = svc.nativePasskeyStatusFromRequest(req)
	if !status.WindowsOriginTrusted {
		t.Fatalf("WindowsOriginTrusted = false with matching extra origin")
	}

	req = httptest.NewRequest("GET", "http://example.com/api/auth/status", nil)
	status = svc.nativePasskeyStatusFromRequest(req)
	if status.AppleConfigured || status.AndroidConfigured || status.WindowsOriginTrusted {
		t.Fatalf("status = %+v, want native passkeys unavailable over insecure origin", status)
	}
}

func TestListUsers_ReportsDeviceAndInviteState(t *testing.T) {
	svc := setupTestService(t)

	// admin logs in -> gets an active device
	if _, err := svc.Login("admin", "testpass123"); err != nil {
		t.Fatalf("login: %v", err)
	}

	// Invite a new user via connect link (unredeemed -> pending invite)
	if _, err := svc.CreateConnectToken(1, "guest", "http://example.com"); err != nil {
		t.Fatalf("create connect token: %v", err)
	}

	users, err := svc.ListUsers()
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}

	byName := map[string]UserSummary{}
	for _, u := range users {
		byName[u.Username] = u
	}

	admin := byName["admin"]
	if admin.Role != RoleAdmin || admin.DeviceCount != 1 || !admin.HasPassword || admin.HasPendingInvite {
		t.Fatalf("unexpected admin summary: %+v", admin)
	}

	guest := byName["guest"]
	if guest.Role != RoleUser || guest.DeviceCount != 0 || guest.HasPassword || !guest.HasPendingInvite {
		t.Fatalf("unexpected guest summary: %+v", guest)
	}
}

func TestUpdateUserRole_PromoteAndDemote(t *testing.T) {
	svc := setupTestService(t)
	if _, err := svc.CreateConnectToken(1, "guest", "http://example.com"); err != nil {
		t.Fatalf("create connect token: %v", err)
	}

	var guestID int64
	if err := svc.db.QueryRow("SELECT id FROM users WHERE username = ?", "guest").Scan(&guestID); err != nil {
		t.Fatalf("load guest: %v", err)
	}

	updated, err := svc.UpdateUserRole(guestID, RoleAdmin)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if updated.Role != RoleAdmin {
		t.Fatalf("role = %q, want %q", updated.Role, RoleAdmin)
	}

	if _, err := svc.UpdateUserRole(guestID, "superuser"); err != ErrInvalidRole {
		t.Fatalf("expected ErrInvalidRole, got %v", err)
	}

	if _, err := svc.UpdateUserRole(guestID, RoleUser); err != nil {
		t.Fatalf("demote: %v", err)
	}
}

func TestUpdateUserRole_CannotDemoteLastAdmin(t *testing.T) {
	svc := setupTestService(t)

	var adminID int64
	if err := svc.db.QueryRow("SELECT id FROM users WHERE username = ?", "admin").Scan(&adminID); err != nil {
		t.Fatalf("load admin: %v", err)
	}

	if _, err := svc.UpdateUserRole(adminID, RoleUser); err != ErrLastAdmin {
		t.Fatalf("expected ErrLastAdmin, got %v", err)
	}
}

func TestDeleteUser_RemovesUserAndDevices(t *testing.T) {
	svc := setupTestService(t)

	// Invite a guest and redeem the link so they have a device + refresh token.
	tok, err := svc.CreateConnectToken(1, "guest", "http://example.com")
	if err != nil {
		t.Fatalf("create connect token: %v", err)
	}
	token := tok.Link[strings.Index(tok.Link, "token=")+len("token=") : strings.Index(tok.Link, "&server=")]
	resp, err := svc.RedeemConnectToken(token, "Guest Phone")
	if err != nil {
		t.Fatalf("redeem token: %v", err)
	}

	if err := svc.DeleteUser(1, resp.User.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	var count int
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM users WHERE id = ?", resp.User.ID).Scan(&count); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 0 {
		t.Fatal("user was not deleted")
	}
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM devices WHERE user_id = ?", resp.User.ID).Scan(&count); err != nil {
		t.Fatalf("count devices: %v", err)
	}
	if count != 0 {
		t.Fatal("devices were not cleaned up")
	}
}

func TestDeleteUser_Guards(t *testing.T) {
	svc := setupTestService(t)

	var adminID int64
	if err := svc.db.QueryRow("SELECT id FROM users WHERE username = ?", "admin").Scan(&adminID); err != nil {
		t.Fatalf("load admin: %v", err)
	}

	if err := svc.DeleteUser(adminID, adminID); err != ErrCannotDeleteSelf {
		t.Fatalf("expected ErrCannotDeleteSelf, got %v", err)
	}

	// Promote a second admin so the self-delete guard isn't what's tripping,
	// then ensure the last-admin guard still protects the remaining admin.
	if _, err := svc.CreateConnectToken(adminID, "second", "http://example.com"); err != nil {
		t.Fatalf("create token: %v", err)
	}
	var secondID int64
	if err := svc.db.QueryRow("SELECT id FROM users WHERE username = ?", "second").Scan(&secondID); err != nil {
		t.Fatalf("load second: %v", err)
	}
	if err := svc.DeleteUser(secondID, adminID); err != ErrLastAdmin {
		t.Fatalf("expected ErrLastAdmin, got %v", err)
	}
}

func TestHashToken_Deterministic(t *testing.T) {
	h1 := hashToken("my-token-value")
	h2 := hashToken("my-token-value")
	if h1 != h2 {
		t.Fatalf("same input produced different hashes: %s vs %s", h1, h2)
	}

	h3 := hashToken("different-token")
	if h1 == h3 {
		t.Fatal("different inputs produced the same hash")
	}
}

func TestRefreshRotation_FirstUseSucceeds(t *testing.T) {
	svc := setupTestService(t)

	loginResp, err := svc.Login("admin", "testpass123")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	refreshResp, err := svc.Refresh(loginResp.RefreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if refreshResp.AccessToken == "" || refreshResp.RefreshToken == "" {
		t.Fatal("refresh response missing tokens")
	}
}

func TestRefreshRotation_ReplayFails(t *testing.T) {
	svc := setupTestService(t)

	loginResp, err := svc.Login("admin", "testpass123")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	oldRefreshToken := loginResp.RefreshToken

	// JWT NumericDate has second precision; ensure the rotated token gets a
	// different IssuedAt so it produces a distinct hash from the original.
	time.Sleep(time.Second)

	// First refresh succeeds and rotates the token
	_, err = svc.Refresh(oldRefreshToken)
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	// Replaying the old token should fail
	_, err = svc.Refresh(oldRefreshToken)
	if err == nil {
		t.Fatal("replay of rotated refresh token should fail")
	}
}

func TestDeviceRevocation_InvalidatesRefreshTokens(t *testing.T) {
	svc := setupTestService(t)

	loginResp, err := svc.Login("admin", "testpass123")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	if err := svc.RevokeDevice(loginResp.DeviceID); err != nil {
		t.Fatalf("revoke device: %v", err)
	}

	// Refresh should fail after device revocation
	_, err = svc.Refresh(loginResp.RefreshToken)
	if err == nil {
		t.Fatal("refresh after device revocation should fail")
	}
}
