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
	if _, err := svc.Login("admin", "testpass123", "", ""); err != nil {
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
	if admin.Role != RoleAdmin || admin.DeviceCount != 1 || !admin.HasPassword || admin.HasPendingInvite ||
		!admin.PasswordEnabled || !admin.PasskeyEnabled {
		t.Fatalf("unexpected admin summary: %+v", admin)
	}

	// New invite users default to no password/passkey ability — just a session.
	guest := byName["guest"]
	if guest.Role != RoleUser || guest.DeviceCount != 0 || guest.HasPassword || !guest.HasPendingInvite ||
		guest.PasswordEnabled || guest.PasskeyEnabled {
		t.Fatalf("unexpected guest summary: %+v", guest)
	}
}

// inviteGuest creates a connect-link "guest" user (password/passkey disabled by
// default) and returns its ID.
func inviteGuest(t *testing.T, svc *Service) int64 {
	t.Helper()
	if _, err := svc.CreateConnectToken(1, "guest", "http://example.com"); err != nil {
		t.Fatalf("create connect token: %v", err)
	}
	var guestID int64
	if err := svc.db.QueryRow("SELECT id FROM users WHERE username = ?", "guest").Scan(&guestID); err != nil {
		t.Fatalf("load guest: %v", err)
	}
	return guestID
}

func enableMethod(t *testing.T, svc *Service, userID int64, password, passkey *bool) {
	t.Helper()
	if _, err := svc.SetUserAuthMethods(userID, password, passkey); err != nil {
		t.Fatalf("set auth methods: %v", err)
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
	resp, err := svc.RedeemConnectToken(token, "Guest Phone", "")
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

func TestRedeemConnectToken_DedupesByHardwareID(t *testing.T) {
	svc := setupTestService(t)

	// Redeems a fresh connect link for "guest" and returns the device id. Each
	// connect token is single-use, so a reconnect needs a brand-new link.
	redeem := func(name, hardwareID string) string {
		t.Helper()
		tok, err := svc.CreateConnectToken(1, "guest", "http://example.com")
		if err != nil {
			t.Fatalf("create connect token: %v", err)
		}
		token := tok.Link[strings.Index(tok.Link, "token=")+len("token=") : strings.Index(tok.Link, "&server=")]
		resp, err := svc.RedeemConnectToken(token, name, hardwareID)
		if err != nil {
			t.Fatalf("redeem token: %v", err)
		}
		return resp.DeviceID
	}

	guestDeviceCount := func() int {
		t.Helper()
		devices, err := svc.ListDevices()
		if err != nil {
			t.Fatalf("list devices: %v", err)
		}
		n := 0
		for _, d := range devices {
			if d.Username == "guest" {
				n++
			}
		}
		return n
	}

	// Same physical device reconnects (new link, same hardware id): the row is
	// reused and its name refreshed to the newest — not duplicated.
	first := redeem("iPhone 15", "HW-AAA")
	second := redeem("Apple iPhone 16 Pro Max", "HW-AAA")
	if first != second {
		t.Fatalf("reconnect should reuse device id %q, got %q", first, second)
	}
	if n := guestDeviceCount(); n != 1 {
		t.Fatalf("expected 1 deduped device, got %d", n)
	}

	devices, _ := svc.ListDevices()
	for _, d := range devices {
		if d.ID == first && d.DeviceName != "Apple iPhone 16 Pro Max" {
			t.Fatalf("device name should refresh to newest, got %q", d.DeviceName)
		}
	}

	// A different physical device (distinct hardware id) is its own row.
	redeem("Apple iPad Pro 11", "HW-BBB")
	if n := guestDeviceCount(); n != 2 {
		t.Fatalf("expected 2 devices after a distinct hardware id, got %d", n)
	}

	// No hardware id (e.g. web) never dedupes: each redeem is its own row.
	redeem("Chrome on macOS", "")
	redeem("Chrome on macOS", "")
	if n := guestDeviceCount(); n != 4 {
		t.Fatalf("expected 4 devices (2 deduped + 2 web), got %d", n)
	}
}

func TestLogin_DedupesDeviceByHardwareID(t *testing.T) {
	svc := setupTestService(t)

	adminDeviceCount := func() int {
		t.Helper()
		devices, err := svc.ListDevices()
		if err != nil {
			t.Fatalf("list devices: %v", err)
		}
		n := 0
		for _, d := range devices {
			if d.Username == "admin" {
				n++
			}
		}
		return n
	}

	// Password login routes through the same shared upsert as connect links, so
	// re-logging in on the same physical device reuses its row (newest name)
	// instead of stacking duplicates.
	first, err := svc.Login("admin", "testpass123", "Yana's Mac", "HW-MAC")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	second, err := svc.Login("admin", "testpass123", "Apple MacBook Pro", "HW-MAC")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if first.DeviceID != second.DeviceID {
		t.Fatalf("same-hardware login should reuse device %q, got %q", first.DeviceID, second.DeviceID)
	}
	if n := adminDeviceCount(); n != 1 {
		t.Fatalf("expected 1 deduped admin device, got %d", n)
	}

	// A login with no hardware id (older client) never dedupes.
	if _, err := svc.Login("admin", "testpass123", "Admin", ""); err != nil {
		t.Fatalf("login: %v", err)
	}
	if n := adminDeviceCount(); n != 2 {
		t.Fatalf("expected 2 admin devices after a no-hardware login, got %d", n)
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

func TestSetPassword_EnablesLoginForInviteUser(t *testing.T) {
	svc := setupTestService(t)

	// Invite a user via connect link — they start with no password.
	if _, err := svc.CreateConnectToken(1, "guest", "http://example.com"); err != nil {
		t.Fatalf("create connect token: %v", err)
	}
	var guestID int64
	if err := svc.db.QueryRow("SELECT id FROM users WHERE username = ?", "guest").Scan(&guestID); err != nil {
		t.Fatalf("load guest: %v", err)
	}

	// An admin must enable password sign-in before the user can set one.
	enabled := true
	enableMethod(t, svc, guestID, &enabled, nil)

	// Before setting a password, neither password login path should work.
	if _, err := svc.Login("guest", "hunter2!", "", ""); err == nil {
		t.Fatal("login should fail before a password is set")
	}
	if _, err := svc.AuthenticatePassword("guest", "hunter2!"); err == nil {
		t.Fatal("password auth should fail before a password is set")
	}

	if err := svc.SetPassword(guestID, "hunter2!"); err != nil {
		t.Fatalf("set password: %v", err)
	}

	// Both the app login and the MCP/OAuth password path should now succeed.
	if _, err := svc.Login("guest", "hunter2!", "", ""); err != nil {
		t.Fatalf("login after set password: %v", err)
	}
	if _, err := svc.AuthenticatePassword("guest", "hunter2!"); err != nil {
		t.Fatalf("authenticate password after set password: %v", err)
	}

	// The admin user list should now report the account as having a password.
	users, err := svc.ListUsers()
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	var found bool
	for _, u := range users {
		if u.ID == guestID {
			found = true
			if !u.HasPassword {
				t.Fatalf("guest summary HasPassword = false, want true")
			}
		}
	}
	if !found {
		t.Fatal("guest user missing from list")
	}
}

func TestSetPassword_RejectsTooShort(t *testing.T) {
	svc := setupTestService(t)
	if _, err := svc.CreateConnectToken(1, "guest", "http://example.com"); err != nil {
		t.Fatalf("create connect token: %v", err)
	}
	var guestID int64
	if err := svc.db.QueryRow("SELECT id FROM users WHERE username = ?", "guest").Scan(&guestID); err != nil {
		t.Fatalf("load guest: %v", err)
	}

	enabled := true
	enableMethod(t, svc, guestID, &enabled, nil)

	if err := svc.SetPassword(guestID, "short"); err != ErrPasswordTooShort {
		t.Fatalf("expected ErrPasswordTooShort, got %v", err)
	}
	// A too-short password must not have been written.
	if _, err := svc.AuthenticatePassword("guest", "short"); err == nil {
		t.Fatal("too-short password should not have been stored")
	}
}

func TestSetPassword_ReplacesExisting(t *testing.T) {
	svc := setupTestService(t)

	var adminID int64
	if err := svc.db.QueryRow("SELECT id FROM users WHERE username = ?", "admin").Scan(&adminID); err != nil {
		t.Fatalf("load admin: %v", err)
	}

	if err := svc.SetPassword(adminID, "rotated-secret"); err != nil {
		t.Fatalf("set password: %v", err)
	}

	// The old password must stop working, the new one must work.
	if _, err := svc.Login("admin", "testpass123", "", ""); err == nil {
		t.Fatal("old password should no longer authenticate")
	}
	if _, err := svc.Login("admin", "rotated-secret", "", ""); err != nil {
		t.Fatalf("login with rotated password: %v", err)
	}
}

func TestSetPassword_UnknownUser(t *testing.T) {
	svc := setupTestService(t)
	if err := svc.SetPassword(999999, "long-enough"); err != ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestSetPassword_RequiresEnabled(t *testing.T) {
	svc := setupTestService(t)
	guestID := inviteGuest(t, svc)

	// Disabled by default: the user cannot create a password.
	if err := svc.SetPassword(guestID, "hunter2!"); err != ErrPasswordNotAllowed {
		t.Fatalf("expected ErrPasswordNotAllowed, got %v", err)
	}
}

func TestSetUserAuthMethods_EnableAndRevokePassword(t *testing.T) {
	svc := setupTestService(t)
	guestID := inviteGuest(t, svc)

	enabled, disabled := true, false
	enableMethod(t, svc, guestID, &enabled, nil)

	if err := svc.SetPassword(guestID, "hunter2!"); err != nil {
		t.Fatalf("set password after enable: %v", err)
	}
	if _, err := svc.Login("guest", "hunter2!", "", ""); err != nil {
		t.Fatalf("login after enable+set: %v", err)
	}

	// Disabling is a real revoke: it clears the password and blocks login.
	enableMethod(t, svc, guestID, &disabled, nil)
	if _, err := svc.Login("guest", "hunter2!", "", ""); err == nil {
		t.Fatal("login should fail after password disabled")
	}
	var hash string
	if err := svc.db.QueryRow("SELECT password_hash FROM users WHERE id = ?", guestID).Scan(&hash); err != nil {
		t.Fatalf("load hash: %v", err)
	}
	if hash != "" {
		t.Fatal("password hash should be cleared on disable")
	}
}

func TestAuthenticatePassword_RequiresEnabled(t *testing.T) {
	svc := setupTestService(t)
	guestID := inviteGuest(t, svc)

	enabled, disabled := true, false
	enableMethod(t, svc, guestID, &enabled, nil)
	if err := svc.SetPassword(guestID, "hunter2!"); err != nil {
		t.Fatalf("set password: %v", err)
	}

	// The MCP/OAuth password path works while enabled.
	if _, err := svc.AuthenticatePassword("guest", "hunter2!"); err != nil {
		t.Fatalf("authenticate password: %v", err)
	}

	// Disabling password revokes MCP password access too.
	enableMethod(t, svc, guestID, &disabled, nil)
	if _, err := svc.AuthenticatePassword("guest", "hunter2!"); err == nil {
		t.Fatal("MCP password auth should fail after disable")
	}
}

func TestBeginPasskeyRegistration_RequiresEnabled(t *testing.T) {
	svc := setupTestService(t)
	guestID := inviteGuest(t, svc)

	req := httptest.NewRequest("POST", "https://example.com/api/auth/passkey/register/begin", nil)
	if _, _, err := svc.BeginPasskeyRegistration(guestID, req); err != ErrPasskeyNotAllowed {
		t.Fatalf("expected ErrPasskeyNotAllowed, got %v", err)
	}

	enabled := true
	enableMethod(t, svc, guestID, nil, &enabled)
	if _, _, err := svc.BeginPasskeyRegistration(guestID, req); err != nil {
		t.Fatalf("registration should begin once enabled: %v", err)
	}
}

func TestSetUserAuthMethods_RevokePasskeyDeletesCredentials(t *testing.T) {
	svc := setupTestService(t)
	guestID := inviteGuest(t, svc)

	// Seed a passkey credential as if the user had registered one.
	if _, err := svc.db.Exec(
		"INSERT INTO webauthn_credentials (id, user_id, public_key, attestation_type, rp_id) VALUES (?, ?, ?, ?, ?)",
		"credid", guestID, []byte("pk"), "none", "example.com",
	); err != nil {
		t.Fatalf("seed credential: %v", err)
	}

	enabled, disabled := true, false
	enableMethod(t, svc, guestID, nil, &enabled)
	enableMethod(t, svc, guestID, nil, &disabled)

	var count int
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?", guestID).Scan(&count); err != nil {
		t.Fatalf("count credentials: %v", err)
	}
	if count != 0 {
		t.Fatal("passkeys should be deleted on disable")
	}
}

func TestSetUserAuthMethods_CannotModifyAdmin(t *testing.T) {
	svc := setupTestService(t)
	var adminID int64
	if err := svc.db.QueryRow("SELECT id FROM users WHERE username = ?", "admin").Scan(&adminID); err != nil {
		t.Fatalf("load admin: %v", err)
	}
	disabled := false
	if _, err := svc.SetUserAuthMethods(adminID, &disabled, &disabled); err != ErrCannotModifyAdmin {
		t.Fatalf("expected ErrCannotModifyAdmin, got %v", err)
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

	loginResp, err := svc.Login("admin", "testpass123", "", "")
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

	loginResp, err := svc.Login("admin", "testpass123", "", "")
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

	// Age the just-superseded token past the rotation grace window so a replay
	// is treated as a replay rather than a benign retry.
	if _, err := svc.db.Exec(
		"UPDATE refresh_tokens SET superseded_at = ? WHERE token_hash = ?",
		time.Now().Add(-2*refreshRotationGrace), hashToken(oldRefreshToken),
	); err != nil {
		t.Fatalf("age token: %v", err)
	}

	// Replaying the old token beyond the grace window should fail
	if _, err := svc.Refresh(oldRefreshToken); err == nil {
		t.Fatal("replay of rotated refresh token beyond grace should fail")
	}
}

// TestRefreshRotation_GraceAllowsRetry verifies that a client which fails to
// persist the rotated token can retry with the original within the grace window
// and stay signed in.
func TestRefreshRotation_GraceAllowsRetry(t *testing.T) {
	svc := setupTestService(t)

	loginResp, err := svc.Login("admin", "testpass123", "", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	old := loginResp.RefreshToken
	time.Sleep(time.Second)

	if _, err := svc.Refresh(old); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	// Immediate retry with the same token is within grace, so it still works.
	if _, err := svc.Refresh(old); err != nil {
		t.Fatalf("in-grace retry should succeed: %v", err)
	}
}

func TestDeviceRevocation_InvalidatesRefreshTokens(t *testing.T) {
	svc := setupTestService(t)

	loginResp, err := svc.Login("admin", "testpass123", "", "")
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
