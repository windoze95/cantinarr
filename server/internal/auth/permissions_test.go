package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHasPermission_UserAndAdmin(t *testing.T) {
	if !HasPermission(RoleUser, PermissionMediaRequest) {
		t.Fatal("user should be able to request media")
	}
	if HasPermission(RoleUser, PermissionDownloadsManage) {
		t.Fatal("user should not be able to manage downloads")
	}
	if !HasPermission(RoleAdmin, PermissionDownloadsManage) {
		t.Fatal("admin should have all permissions")
	}
	for _, role := range []string{RoleAdmin, RoleUser, "unknown"} {
		if HasPermission(role, "") {
			t.Fatalf("role %q received an undeclared permission", role)
		}
	}
}

func TestRolePermissionMatrixIsExact(t *testing.T) {
	permissions := []Permission{
		PermissionAdmin,
		PermissionMediaDiscover,
		PermissionMediaRequest,
		PermissionAIChat,
		PermissionMCPAccess,
		PermissionUsersManage,
		PermissionRequestsManage,
		PermissionCredentialsManage,
		PermissionAIToolsManage,
		PermissionInstancesManage,
		PermissionRemediationManage,
		PermissionArrRead,
		PermissionArrSearch,
		PermissionArrBrowse,
		PermissionDownloadsRead,
		PermissionDownloadsManage,
		PermissionMonitoringRead,
		PermissionSystemRead,
	}
	registered := allPermissions()
	if len(registered) != len(permissions) {
		t.Fatalf("registered permission count = %d, want explicit matrix count %d", len(registered), len(permissions))
	}
	userAllowed := map[Permission]bool{
		PermissionMediaDiscover: true,
		PermissionMediaRequest:  true,
		PermissionAIChat:        true,
		PermissionMCPAccess:     true,
		PermissionArrBrowse:     true,
	}
	for _, permission := range permissions {
		if !registered[permission] {
			t.Errorf("permission %q is missing from the registry", permission)
		}
		if !HasPermission(RoleAdmin, permission) {
			t.Errorf("admin missing permission %q", permission)
		}
		if got := HasPermission(RoleUser, permission); got != userAllowed[permission] {
			t.Errorf("user permission %q = %t, want %t", permission, got, userAllowed[permission])
		}
		if HasPermission("unknown", permission) {
			t.Errorf("unknown role received permission %q", permission)
		}
	}

	listedUser := make(map[Permission]bool)
	for _, permission := range PermissionsForRole(RoleUser) {
		listedUser[permission] = true
	}
	if len(listedUser) != len(userAllowed) {
		t.Fatalf("listed user permission count = %d, want %d", len(listedUser), len(userAllowed))
	}
	for permission := range userAllowed {
		if !listedUser[permission] {
			t.Errorf("PermissionsForRole(user) omitted %q", permission)
		}
	}
	if got := PermissionsForRole("unknown"); len(got) != 0 {
		t.Fatalf("unknown role permission list = %v", got)
	}
}

func TestAuthMiddleware_RehydratesCurrentUserRole(t *testing.T) {
	svc := setupTestService(t)

	loginResp, err := svc.Login("admin", "testpass123", "", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := svc.db.Exec("UPDATE users SET role = ? WHERE id = ?", RoleUser, loginResp.User.ID); err != nil {
		t.Fatalf("update role: %v", err)
	}

	handler := svc.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetClaims(r.Context())
		if claims == nil {
			t.Fatal("missing claims")
		}
		if claims.Role != RoleUser {
			t.Fatalf("claims role = %q, want %q", claims.Role, RoleUser)
		}
		user := GetUserFromContext(r.Context())
		if user == nil {
			t.Fatal("missing user")
		}
		if user.Role != RoleUser {
			t.Fatalf("context user role = %q, want %q", user.Role, RoleUser)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestAuthMiddleware_RejectsRevokedDeviceAccessToken(t *testing.T) {
	svc := setupTestService(t)

	loginResp, err := svc.Login("admin", "testpass123", "", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if err := svc.RevokeDevice(loginResp.DeviceID); err != nil {
		t.Fatalf("revoke device: %v", err)
	}

	handler := svc.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestOAuthTokensAreAudienceBoundAndRefreshable(t *testing.T) {
	svc := setupTestService(t)
	loginResp, err := svc.Login("admin", "testpass123", "", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	redirectURI := "http://127.0.0.1:12345/callback"
	resource := "http://example.com/mcp"
	verifier := "test-verifier-value"
	client, err := svc.RegisterOAuthClient("Test Agent", []string{redirectURI})
	if err != nil {
		t.Fatalf("register oauth client: %v", err)
	}
	code, err := svc.CreateOAuthAuthorizationCode(client, loginResp.User.ID, redirectURI, pkceChallenge(verifier), resource, defaultOAuthScope)
	if err != nil {
		t.Fatalf("create auth code: %v", err)
	}

	tokenResp, err := svc.ExchangeOAuthAuthorizationCode(client.ClientID, code, redirectURI, verifier, resource)
	if err != nil {
		t.Fatalf("exchange code: %v", err)
	}
	if tokenResp.RefreshToken == "" {
		t.Fatal("missing refresh token")
	}

	claims, _, err := svc.AuthenticateTokenForAudience(tokenResp.AccessToken, resource)
	if err != nil {
		t.Fatalf("authenticate oauth access token: %v", err)
	}
	if claims.DeviceID == "" {
		t.Fatal("oauth access token missing device id")
	}
	if _, _, err := svc.AuthenticateToken(tokenResp.AccessToken); err == nil {
		t.Fatal("oauth access token should not authenticate as an app session token")
	}

	refreshResp, err := svc.RefreshOAuthToken(client.ClientID, tokenResp.RefreshToken, resource)
	if err != nil {
		t.Fatalf("refresh oauth token: %v", err)
	}
	if refreshResp.AccessToken == "" || refreshResp.RefreshToken == "" {
		t.Fatal("refresh response missing tokens")
	}
	if _, err := svc.RefreshOAuthToken(client.ClientID, tokenResp.RefreshToken, resource); err == nil {
		t.Fatal("rotated oauth refresh token should not be reusable")
	}
}

func TestOAuthRefreshWrongResourceDoesNotConsumeToken(t *testing.T) {
	svc := setupTestService(t)
	loginResp, err := svc.Login("admin", "testpass123", "", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	redirectURI := "http://127.0.0.1:54321/callback"
	resource := "http://example.com/mcp"
	verifier := "resource-mismatch-test-verifier"
	client, err := svc.RegisterOAuthClient("Resource Test Agent", []string{redirectURI})
	if err != nil {
		t.Fatalf("register oauth client: %v", err)
	}
	code, err := svc.CreateOAuthAuthorizationCode(client, loginResp.User.ID, redirectURI, pkceChallenge(verifier), resource, defaultOAuthScope)
	if err != nil {
		t.Fatalf("create auth code: %v", err)
	}
	tokenResp, err := svc.ExchangeOAuthAuthorizationCode(client.ClientID, code, redirectURI, verifier, resource)
	if err != nil {
		t.Fatalf("exchange code: %v", err)
	}

	if _, err := svc.RefreshOAuthToken(client.ClientID, tokenResp.RefreshToken, "http://example.com/not-mcp"); err == nil {
		t.Fatal("refresh with wrong resource should fail")
	}
	if _, err := svc.RefreshOAuthToken(client.ClientID, tokenResp.RefreshToken, resource); err != nil {
		t.Fatalf("refresh token should remain valid after wrong resource attempt: %v", err)
	}
}

func TestOAuthRefreshTokenRespectsDeviceRevocation(t *testing.T) {
	svc := setupTestService(t)
	loginResp, err := svc.Login("admin", "testpass123", "", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	redirectURI := "http://localhost:12345/callback"
	resource := "http://example.com/mcp"
	verifier := "another-test-verifier-value"
	client, err := svc.RegisterOAuthClient("Revoked Agent", []string{redirectURI})
	if err != nil {
		t.Fatalf("register oauth client: %v", err)
	}
	code, err := svc.CreateOAuthAuthorizationCode(client, loginResp.User.ID, redirectURI, pkceChallenge(verifier), resource, defaultOAuthScope)
	if err != nil {
		t.Fatalf("create auth code: %v", err)
	}
	tokenResp, err := svc.ExchangeOAuthAuthorizationCode(client.ClientID, code, redirectURI, verifier, resource)
	if err != nil {
		t.Fatalf("exchange code: %v", err)
	}
	claims, _, err := svc.AuthenticateTokenForAudience(tokenResp.AccessToken, resource)
	if err != nil {
		t.Fatalf("authenticate oauth access token: %v", err)
	}
	if err := svc.RevokeDevice(claims.DeviceID); err != nil {
		t.Fatalf("revoke oauth device: %v", err)
	}

	if _, err := svc.RefreshOAuthToken(client.ClientID, tokenResp.RefreshToken, resource); err == nil {
		t.Fatal("refresh should fail after oauth device revocation")
	}
	if _, _, err := svc.AuthenticateTokenForAudience(tokenResp.AccessToken, resource); err == nil {
		t.Fatal("access token should fail after oauth device revocation")
	}
}

func TestPasskeySetupTokenRespectsDeviceRevocation(t *testing.T) {
	svc := setupTestService(t)

	loginResp, err := svc.Login("admin", "testpass123", "", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	token, _, err := svc.CreatePasskeySetupToken(loginResp.User.ID, loginResp.DeviceID)
	if err != nil {
		t.Fatalf("create passkey setup token: %v", err)
	}
	claims, err := svc.ValidateToken(token)
	if err != nil {
		t.Fatalf("validate passkey setup token: %v", err)
	}
	if !hasAudience(claims, passkeySetupAudience) {
		t.Fatal("passkey setup token missing setup audience")
	}

	if err := svc.RevokeDevice(loginResp.DeviceID); err != nil {
		t.Fatalf("revoke device: %v", err)
	}
	req := httptest.NewRequest("POST", "https://example.com/api/auth/passkey/setup/begin", nil)
	if _, _, err := svc.BeginPasskeySetup(token, req); err == nil {
		t.Fatal("passkey setup token should fail after device revocation")
	}
}

func TestMCPAuthMiddlewareAdvertisesOAuthMetadata(t *testing.T) {
	svc := setupTestService(t)
	handler := NewOAuthHandler(svc).MCPAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest("POST", "http://example.com/mcp", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	got := rec.Header().Get("WWW-Authenticate")
	if !strings.Contains(got, `resource_metadata="http://example.com/.well-known/oauth-protected-resource/mcp"`) {
		t.Fatalf("WWW-Authenticate missing resource metadata: %q", got)
	}
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
