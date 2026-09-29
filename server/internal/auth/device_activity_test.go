package auth

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func recordDeviceActivityWrites(t *testing.T, svc *Service) func() int {
	t.Helper()
	_, err := svc.db.Exec(`
		CREATE TABLE device_activity_writes (device_id TEXT NOT NULL);
		CREATE TRIGGER record_device_activity AFTER UPDATE OF last_seen_at ON devices
		BEGIN INSERT INTO device_activity_writes VALUES (NEW.id); END;
	`)
	if err != nil {
		t.Fatal(err)
	}
	return func() int {
		t.Helper()
		var count int
		if err := svc.db.QueryRow("SELECT COUNT(*) FROM device_activity_writes").Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
}

func authenticatedBurst(t *testing.T, svc *Service, token string) {
	t.Helper()
	handler := svc.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusNoContent {
				t.Errorf("authenticated request = %d, want 204", rec.Code)
			}
		})
	}
	wg.Wait()
}

// A homepage fans out into API and authenticated artwork reads. None of them
// should commit another timestamp when this device was just seen, including
// after a server restart. Authorization still runs for every request.
func TestAuthMiddlewareRecentActivityDoesNotWrite(t *testing.T) {
	svc := setupTestService(t)
	login, err := svc.Login("admin", "testpass123", "Browser", "browser")
	if err != nil {
		t.Fatal(err)
	}
	writes := recordDeviceActivityWrites(t, svc)
	for _, service := range []*Service{svc, NewService(svc.db, "test-secret-key")} {
		authenticatedBurst(t, service, login.AccessToken)
		if got := writes(); got != 0 {
			t.Fatalf("recent device activity wrote %d timestamps, want 0", got)
		}
	}
}

func TestAuthMiddlewareStaleActivityWritesOnce(t *testing.T) {
	for _, previous := range []struct {
		name  string
		value any
	}{
		{"go UTC timestamp", time.Now().UTC().Add(-2 * time.Minute)},
		{"go local timestamp", time.Now().In(time.FixedZone("CEST", 2*60*60)).Add(-2 * time.Minute)},
		{"SQLite timestamp", time.Now().UTC().Add(-2 * time.Minute).Format("2006-01-02 15:04:05")},
		{"missing timestamp", nil},
		{"clock moved backwards", time.Now().UTC().Add(time.Hour)},
	} {
		t.Run(previous.name, func(t *testing.T) {
			svc := setupTestService(t)
			login, err := svc.Login("admin", "testpass123", "Browser", "browser")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.db.Exec("UPDATE devices SET last_seen_at=? WHERE id=?", previous.value, login.DeviceID); err != nil {
				t.Fatal(err)
			}
			writes := recordDeviceActivityWrites(t, svc)
			started := time.Now()
			authenticatedBurst(t, svc, login.AccessToken)
			if got := writes(); got != 1 {
				t.Fatalf("stale device activity wrote %d timestamps, want 1", got)
			}
			var seen time.Time
			if err := svc.db.QueryRow("SELECT last_seen_at FROM devices WHERE id=?", login.DeviceID).Scan(&seen); err != nil {
				t.Fatal(err)
			}
			if seen.Before(started) || seen.After(time.Now()) {
				t.Fatalf("last seen = %s, want this request's time", seen)
			}
		})
	}
}

func TestDeviceActivityWriteFailureDoesNotRejectSession(t *testing.T) {
	svc := setupTestService(t)
	login, err := svc.Login("admin", "testpass123", "Browser", "browser")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec("UPDATE devices SET last_seen_at=NULL WHERE id=?", login.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`CREATE TRIGGER fail_device_activity BEFORE UPDATE OF last_seen_at ON devices
		BEGIN SELECT RAISE(FAIL, 'activity write unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.AuthenticateToken(login.AccessToken); err != nil {
		t.Fatalf("activity metadata failure rejected a valid session: %v", err)
	}
	if _, err := svc.db.Exec("DROP TRIGGER fail_device_activity"); err != nil {
		t.Fatal(err)
	}
	writes := recordDeviceActivityWrites(t, svc)
	if _, _, err := svc.AuthenticateToken(login.AccessToken); err != nil {
		t.Fatal(err)
	}
	if got := writes(); got != 1 {
		t.Fatalf("activity write did not retry after recovery: %d writes", got)
	}
}

func TestDeviceActivityIsTrackedPerDevice(t *testing.T) {
	svc := setupTestService(t)
	first, err := svc.Login("admin", "testpass123", "First", "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Login("admin", "testpass123", "Second", "second")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec("UPDATE devices SET last_seen_at=NULL"); err != nil {
		t.Fatal(err)
	}
	writes := recordDeviceActivityWrites(t, svc)
	for _, login := range []*TokenResponse{first, second} {
		authenticatedBurst(t, svc, login.AccessToken)
		var count int
		if err := svc.db.QueryRow("SELECT COUNT(*) FROM device_activity_writes WHERE device_id=?", login.DeviceID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("device %s activity writes = %d, want 1", login.DeviceID, count)
		}
	}
	if got := writes(); got != 2 {
		t.Fatalf("activity writes = %d, want 2", got)
	}
}
