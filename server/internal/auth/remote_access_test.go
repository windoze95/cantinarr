package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	projectdb "github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/remoteaccess"
)

func TestAuthStatusReportsRemoteAccessSecurity(t *testing.T) {
	service := setupTestService(t)
	remoteAccess, err := remoteaccess.NewService(service.db, remoteaccess.EnvironmentOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(service, remoteAccess)
	req := httptest.NewRequest(http.MethodGet, "http://cantinarr.test/api/auth/status", nil)
	req.RemoteAddr = "203.0.113.8:4000"
	recorder := httptest.NewRecorder()
	handler.AuthStatus(recorder, req)
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}

	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["remote_access_security_available"] != true || body["connection_security"] != "remote_http" {
		t.Fatalf("status body = %#v", body)
	}
	if _, present := body["https_required"]; present {
		t.Fatalf("disabled https_required should retain the additive false default: %#v", body)
	}
}

func TestSetupRemoteAccessChoiceIsAtomicAndCannotLockOutRequester(t *testing.T) {
	database, err := projectdb.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	service := NewService(database, "test-jwt-secret")
	remoteAccess, err := remoteaccess.NewService(database, remoteaccess.EnvironmentOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(service, remoteAccess)
	body := `{"username":"admin","password":"correct-horse","require_https_outside_local_network":true}`

	remote := httptest.NewRequest(http.MethodPost, "http://cantinarr.test/api/auth/setup", strings.NewReader(body))
	remote.RemoteAddr = "203.0.113.8:4000"
	remoteRecorder := httptest.NewRecorder()
	handler.HandleSetup(remoteRecorder, remote)
	if remoteRecorder.Code != http.StatusConflict || service.IsSetupComplete() {
		t.Fatalf("remote setup status=%d complete=%v body=%s", remoteRecorder.Code, service.IsSetupComplete(), remoteRecorder.Body.String())
	}

	local := httptest.NewRequest(http.MethodPost, "http://cantinarr.test/api/auth/setup", strings.NewReader(body))
	local.RemoteAddr = "192.168.1.8:4000"
	localRecorder := httptest.NewRecorder()
	handler.HandleSetup(localRecorder, local)
	if localRecorder.Code != http.StatusCreated {
		t.Fatalf("local setup status=%d body=%s", localRecorder.Code, localRecorder.Body.String())
	}
	status := remoteAccess.StatusFor(local)
	if !status.RequireHTTPSOutsideLocalNetwork || status.ConnectionSecurity != remoteaccess.ConnectionLocalHTTP {
		t.Fatalf("remote access status = %+v", status)
	}
}
