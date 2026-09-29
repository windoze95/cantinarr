package mcp

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/request"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

func capabilityServer(t *testing.T, role string, serviceTypes ...string) (*ToolServer, *instance.Store, *sql.DB, map[string]string) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x35}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO users (id, username, password_hash, role) VALUES (1, 'welcome-user', '', ?)`, role); err != nil {
		t.Fatal(err)
	}
	creds := credentials.NewRegistry(database, cipher)
	if err := creds.SetCredential(credentials.KeyTMDBAccessToken, "synthetic-token"); err != nil {
		t.Fatal(err)
	}
	store := instance.NewStore(database, cipher)
	ids := map[string]string{}
	for _, serviceType := range serviceTypes {
		inst := &instance.Instance{ServiceType: serviceType, Name: "Private " + serviceType, URL: "http://private-instance:1234", APIKey: "private-key", IsDefault: serviceType == "radarr" || serviceType == "sonarr"}
		if err := store.Create(inst); err != nil {
			t.Fatal(err)
		}
		ids[serviceType] = inst.ID
	}
	registry := instance.NewRegistry(store)
	server := NewToolServer(creds, request.NewService(database, registry, nil, nil), registry, nil)
	server.SetCallAuthorizer(func(context.Context, CallContext) (string, error) { return role, nil })
	return server, store, database, ids
}

func readCapabilities(t *testing.T, server *ToolServer) *ChatCapabilities {
	t.Helper()
	// A stale admin claim must not override the role returned by authorization.
	caps, err := server.ChatCapabilities(context.Background(), CallContext{UserID: 1, Role: auth.RoleAdmin, DeviceID: "device", Origin: OriginInteractiveChat})
	if err != nil {
		t.Fatal(err)
	}
	return caps
}

func TestChatCapabilitiesRequireConfiguredAccessibleLibraries(t *testing.T) {
	for _, tc := range []struct {
		name, role         string
		services, requests []string
		adminActions       bool
	}{
		{"no instances", auth.RoleAdmin, nil, []string{}, false},
		{"movies only", auth.RoleAdmin, []string{"radarr"}, []string{"movie"}, true},
		{"unassigned libraries", auth.RoleUser, []string{"radarr", "sonarr", "chaptarr", "lidarr"}, []string{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, _, _, _ := capabilityServer(t, tc.role, tc.services...)
			caps := readCapabilities(t, server)
			if !reflect.DeepEqual(caps.DiscoverMediaTypes, []string{"movie", "tv"}) || !reflect.DeepEqual(caps.RequestMediaTypes, tc.requests) {
				t.Fatalf("media capabilities = %+v", caps)
			}
			if caps.BrowseLibraries != tc.adminActions || caps.ManageDownloads != tc.adminActions || caps.Troubleshoot != tc.adminActions || caps.ConfigureServices != tc.adminActions {
				t.Fatalf("admin capabilities = %+v", caps)
			}
		})
	}
}

func TestChatCapabilitiesReflectGrantAndInstanceChanges(t *testing.T) {
	server, store, _, ids := capabilityServer(t, auth.RoleUser, "chaptarr", "lidarr")
	if err := store.SetUserGrants(1, map[string][]string{"chaptarr": {ids["chaptarr"]}, "lidarr": {ids["lidarr"]}}); err != nil {
		t.Fatal(err)
	}
	caps := readCapabilities(t, server)
	if !reflect.DeepEqual(caps.DiscoverMediaTypes, []string{"movie", "tv", "book", "music"}) || !reflect.DeepEqual(caps.RequestMediaTypes, []string{"book", "music"}) {
		t.Fatalf("granted capabilities = %+v", caps)
	}
	if err := store.SetUserGrants(1, map[string][]string{"chaptarr": {}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ids["lidarr"]); err != nil {
		t.Fatal(err)
	}
	caps = readCapabilities(t, server)
	if !reflect.DeepEqual(caps.DiscoverMediaTypes, []string{"movie", "tv"}) || len(caps.RequestMediaTypes) != 0 || caps.CheckAvailability {
		t.Fatalf("revoked capabilities = %+v", caps)
	}
}

func TestChatCapabilitiesHonorDisabledToolsAndReadOnlyAccess(t *testing.T) {
	server, _, _, _ := capabilityServer(t, auth.RoleAdmin, "chaptarr")
	for _, name := range []string{"search_books", "request_media", "check_request_status", "get_library", "remove_queue_item", "remediate_queue_item", "diagnose_queue", "get_arr_health", "apply_profile_change", "upsert_custom_format"} {
		if err := server.SetToolEnabled(name, false); err != nil {
			t.Fatal(err)
		}
	}
	caps := readCapabilities(t, server)
	if !reflect.DeepEqual(caps.DiscoverMediaTypes, []string{"movie", "tv"}) || len(caps.RequestMediaTypes) != 0 || caps.CheckAvailability || caps.BrowseLibraries || caps.ManageDownloads || caps.Troubleshoot || caps.ConfigureServices || !caps.CheckDownloads {
		t.Fatalf("disabled capabilities = %+v", caps)
	}
	if err := server.SetToolEnabled("get_queue", false); err != nil {
		t.Fatal(err)
	}
	if readCapabilities(t, server).CheckDownloads {
		t.Fatal("disabled queue still advertised")
	}
	if err := server.SetToolEnabled("apply_profile_change", true); err != nil {
		t.Fatal(err)
	}
	if !readCapabilities(t, server).ConfigureServices {
		t.Fatal("enabled profile workflow not advertised")
	}
	if err := server.SetToolEnabled("preview_profile_change", false); err != nil {
		t.Fatal(err)
	}
	if readCapabilities(t, server).ConfigureServices {
		t.Fatal("incomplete profile workflow advertised")
	}
}

func TestChatCapabilitiesDistinguishUnavailableInventoryAndAuthorization(t *testing.T) {
	server, _, database, _ := capabilityServer(t, auth.RoleUser)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if caps, err := server.ChatCapabilities(context.Background(), CallContext{UserID: 1}); err == nil || caps != nil {
		t.Fatalf("unreadable inventory = %+v, %v", caps, err)
	}
	server.SetCallAuthorizer(func(context.Context, CallContext) (string, error) { return "", errors.New("revoked") })
	if caps, err := server.ChatCapabilities(context.Background(), CallContext{UserID: 1}); !errors.Is(err, ErrToolAuthorization) || caps != nil {
		t.Fatalf("revoked account = %+v, %v", caps, err)
	}
}
