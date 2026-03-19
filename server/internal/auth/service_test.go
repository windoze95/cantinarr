package auth

import (
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
