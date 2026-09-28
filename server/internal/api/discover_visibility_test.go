package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/config"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/serversettings"
)

func TestConfigDiscoveryRequiresAssignmentsAfterInitialSetup(t *testing.T) {
	allTabs := []string{"movie", "tv", "book", "music"}
	for mediaType, serviceType := range serversettings.DiscoverServices() {
		t.Run(mediaType, func(t *testing.T) {
			store, creds, remediationSvc, userID := newConfigHandlerTestState(t)
			settings, _ := newDiscoverySettingsEnv(t, false)
			read := func(role string, wantHidden []string, wantInitial bool) configHandlerResponse {
				t.Helper()
				req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
				req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: userID, Role: role}))
				rec := httptest.NewRecorder()
				configHandler(&config.Config{}, store, creds, nil, remediationSvc, settings)(rec, req)
				var got struct {
					configHandlerResponse
					Hidden  []string `json:"hidden_discover_tabs"`
					Initial bool     `json:"initial_instance_setup"`
				}
				if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
					t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
				}
				if !reflect.DeepEqual(got.Hidden, wantHidden) || got.Initial != wantInitial {
					t.Fatalf("%s hidden=%v initial=%v, want %v/%v", role, got.Hidden, got.Initial, wantHidden, wantInitial)
				}
				return got.configHandlerResponse
			}
			// A fresh admin sees every navigation option. Users never inherit this
			// onboarding exception, and intentional Hide preferences still apply.
			read(auth.RoleAdmin, []string{}, true)
			read(auth.RoleUser, allTabs, false)
			if _, err := settings.UpdateDiscovery(serversettings.DiscoveryPatch{HiddenWhenUnconfigured: map[string]bool{mediaType: true}}); err != nil {
				t.Fatal(err)
			}
			read(auth.RoleAdmin, []string{mediaType}, true)
			// Configuration and a global default alone do not grant discovery, even
			// for admins. The full inventory stays available to admin navigation.
			inst := createConfigInstance(t, store, serviceType, "Offline", true)
			got := read(auth.RoleAdmin, allTabs, false)
			if len(got.Instances) != 1 || got.Instances[0].Assigned || !got.Services[serviceType] {
				t.Fatalf("admin management inventory changed: %+v", got)
			}
			if got := read(auth.RoleUser, allTabs, false); len(got.Instances) != 0 {
				t.Fatalf("unassigned user inventory=%+v", got.Instances)
			}
			if err := store.SetUserGrants(userID, map[string][]string{serviceType: {inst.ID}}); err != nil {
				t.Fatal(err)
			}
			hidden := []string{}
			for _, tab := range allTabs {
				if tab != mediaType {
					hidden = append(hidden, tab)
				}
			}
			// Offline instances still count: discovery follows saved assignment,
			// not a provider's availability or a health check.
			for _, role := range []string{auth.RoleAdmin, auth.RoleUser} {
				got := read(role, hidden, false)
				if len(got.Instances) != 1 || !got.Instances[0].Assigned {
					t.Fatalf("assignment missing: %+v", got)
				}
			}
			if err := store.SetUserGrants(userID, map[string][]string{serviceType: {}}); err != nil {
				t.Fatal(err)
			}
			read(auth.RoleAdmin, allTabs, false)
			read(auth.RoleUser, allTabs, false)
			if err := store.Delete(inst.ID); err != nil {
				t.Fatal(err)
			}
			// Removing the last instance must never reset the first-install exception.
			read(auth.RoleAdmin, allTabs, false)
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

// An admin's 4K badges switch reaches every role's config, so a requester's
// covers follow it without a setting of their own.
func TestConfigCarriesTheAdmin4KBadgesSwitchForEveryRole(t *testing.T) {
	store, creds, remediationSvc, userID := newConfigHandlerTestState(t)
	settings, _ := newDiscoverySettingsEnv(t, false)
	read := func(role string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
		req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: userID, Role: role}))
		rec := httptest.NewRecorder()
		configHandler(&config.Config{}, store, creds, nil, remediationSvc, settings)(rec, req)
		var got map[string]json.RawMessage
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
			t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
		}
		return string(got["cover_4k_badges"])
	}
	for _, role := range []string{auth.RoleAdmin, auth.RoleUser} {
		if got := read(role); got != "false" {
			t.Fatalf("%s before: %s", role, got)
		}
	}
	on := true
	if _, err := settings.UpdateDiscovery(serversettings.DiscoveryPatch{Cover4KBadges: &on}); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{auth.RoleAdmin, auth.RoleUser} {
		if got := read(role); got != "true" {
			t.Fatalf("%s after: %s", role, got)
		}
	}
}

type unavailableSetupHistory struct{ configInstanceStore }

func (unavailableSetupHistory) HasConfiguredInstances() (bool, error) {
	return false, errors.New("setup history unavailable")
}
func TestConfigSetupHistoryFailureDoesNotReopenOnboarding(t *testing.T) {
	store, creds, remediationSvc, userID := newConfigHandlerTestState(t)
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: userID, Role: auth.RoleAdmin}))
	rec := httptest.NewRecorder()
	configHandler(&config.Config{}, unavailableSetupHistory{store}, creds, nil, remediationSvc, nil)(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
	}
}
