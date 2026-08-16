package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/serversettings"
	"github.com/windoze95/cantinarr-server/internal/update"
)

// newUpdateStatusEnv builds the handlers over a real settings service and a
// disabled checker (no network). Authorization is covered by the RBAC route
// sweep, which walks every /api/admin/ pattern; these tests cover the payload
// contract.
func newUpdateStatusEnv(t *testing.T) (*update.Checker, *serversettings.Service) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	settings := serversettings.NewService(database, func() bool { return false })
	return update.NewChecker("1.2.3", true), settings
}

func decodeUpdateStatus(t *testing.T, body string) updateStatusResponse {
	t.Helper()
	var out updateStatusResponse
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode update status: %v (body = %s)", err, body)
	}
	return out
}

// TestUpdateStatusRoundTrip proves the GET/PUT contract behind the banner and
// the Update Portal dialog: the running version is always reported, and a
// saved portal URL reads back (and clears) exactly as written.
func TestUpdateStatusRoundTrip(t *testing.T) {
	checker, settings := newUpdateStatusEnv(t)

	rec := httptest.NewRecorder()
	updateStatusHandler(checker, settings)(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	got := decodeUpdateStatus(t, rec.Body.String())
	if got.Update.Current != "1.2.3" || got.Update.Available {
		t.Fatalf("update = %+v, want current 1.2.3 and no availability from a disabled checker", got.Update)
	}
	if got.ManagementURL != "" {
		t.Fatalf("management_url = %q on a fresh server, want empty", got.ManagementURL)
	}

	rec = httptest.NewRecorder()
	updateServerSettingsHandler(checker, settings)(
		rec, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"management_url":"http://tower.local/docker"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if saved := decodeUpdateStatus(t, rec.Body.String()); saved.ManagementURL != "http://tower.local/docker" {
		t.Fatalf("management_url = %q, want the saved URL echoed back", saved.ManagementURL)
	}

	rec = httptest.NewRecorder()
	updateStatusHandler(checker, settings)(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if reread := decodeUpdateStatus(t, rec.Body.String()); reread.ManagementURL != "http://tower.local/docker" {
		t.Fatalf("re-read management_url = %q, want the saved URL", reread.ManagementURL)
	}

	rec = httptest.NewRecorder()
	updateServerSettingsHandler(checker, settings)(
		rec, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"management_url":""}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if cleared := decodeUpdateStatus(t, rec.Body.String()); cleared.ManagementURL != "" {
		t.Fatalf("management_url = %q after clearing, want empty", cleared.ManagementURL)
	}
}

// TestUpdateStatusRejectsBadInput keeps malformed bodies and non-http(s)
// portal URLs out of the settings blob.
func TestUpdateStatusRejectsBadInput(t *testing.T) {
	checker, settings := newUpdateStatusEnv(t)

	seed := httptest.NewRecorder()
	updateServerSettingsHandler(checker, settings)(
		seed, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"management_url":"http://tower.local/docker"}`)))
	if seed.Code != http.StatusOK {
		t.Fatalf("seed status = %d, body = %s", seed.Code, seed.Body.String())
	}

	for name, body := range map[string]string{
		"malformed body": `{"management_url":`,
		"ftp scheme":     `{"management_url":"ftp://tower.local"}`,
		"javascript url": `{"management_url":"javascript:alert(1)"}`,
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			updateServerSettingsHandler(checker, settings)(
				rec, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body)))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
			if got := settings.Get().ManagementURL; got != "http://tower.local/docker" {
				t.Errorf("stored management_url = %q, want the rejected write to have changed nothing", got)
			}
		})
	}
}
