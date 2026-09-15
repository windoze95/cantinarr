package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/config"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/serversettings"
)

func TestConfigDiscoverVisibilityUsesFullInventoryAndRestoresAutomatically(t *testing.T) {
	for mediaType, serviceType := range serversettings.DiscoverServices() {
		t.Run(mediaType, func(t *testing.T) {
			store, creds, remediationSvc, userID := newConfigHandlerTestState(t)
			settings, _ := newDiscoverySettingsEnv(t, false)
			if _, err := settings.UpdateDiscovery(serversettings.DiscoveryPatch{HiddenWhenUnconfigured: map[string]bool{mediaType: true}}); err != nil {
				t.Fatal(err)
			}
			read := func(role string) map[string]json.RawMessage {
				t.Helper()
				req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
				req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: userID, Role: role}))
				rec := httptest.NewRecorder()
				configHandler(&config.Config{}, store, creds, nil, remediationSvc, settings)(rec, req)
				if rec.Code != 200 {
					t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
				}
				var got map[string]json.RawMessage
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				return got
			}
			for _, role := range []string{auth.RoleAdmin, auth.RoleUser} {
				if got := string(read(role)["hidden_discover_tabs"]); got != `["`+mediaType+`"]` {
					t.Fatalf("%s: got %s", role, got)
				}
			}
			// This unreachable instance still restores the tab. No upstream health
			// or library call participates in configuration visibility.
			inst := createConfigInstance(t, store, serviceType, "Offline", false)
			for _, role := range []string{auth.RoleAdmin, auth.RoleUser} {
				got := read(role)
				if string(got["hidden_discover_tabs"]) != "[]" {
					t.Fatalf("configured %s hidden: %s", serviceType, got["hidden_discover_tabs"])
				}
				if role == auth.RoleUser && (serviceType == "chaptarr" || serviceType == "lidarr") && string(got["instances"]) != "[]" {
					t.Fatalf("visibility granted unauthorized access: %s", got["instances"])
				}
			}
			if err := store.Delete(inst.ID); err != nil {
				t.Fatal(err)
			}
			if got := string(read(auth.RoleAdmin)["hidden_discover_tabs"]); got != `["`+mediaType+`"]` {
				t.Fatalf("removed service did not hide again: %s", got)
			}
		})
	}
}

type unavailableInventory struct{ configInstanceStore }

func (unavailableInventory) ListAll() ([]instance.Instance, error) {
	return nil, errors.New("inventory unavailable")
}

func TestConfigInventoryFailureDoesNotReportMissingServices(t *testing.T) {
	store, creds, remediationSvc, _ := newConfigHandlerTestState(t)
	rec := httptest.NewRecorder()
	configHandler(&config.Config{}, unavailableInventory{store}, creds, nil, remediationSvc, nil)(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
	}
}
