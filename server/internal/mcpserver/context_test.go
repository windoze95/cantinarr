package mcpserver

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
)

func TestAuthContextFuncCarriesDeviceBoundIdentity(t *testing.T) {
	req := httptest.NewRequest("POST", "/mcp", nil)
	claims := &auth.Claims{
		UserID:   17,
		Role:     auth.RoleUser,
		DeviceID: "device-17",
	}
	req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, claims))

	ctx := AuthContextFunc(context.Background(), req)
	if got := GetUserIDFromContext(ctx); got != claims.UserID {
		t.Fatalf("user id = %d, want %d", got, claims.UserID)
	}
	if got := GetRoleFromContext(ctx); got != claims.Role {
		t.Fatalf("role = %q, want %q", got, claims.Role)
	}
	if got := GetDeviceIDFromContext(ctx); got != claims.DeviceID {
		t.Fatalf("device id = %q, want %q", got, claims.DeviceID)
	}
}
