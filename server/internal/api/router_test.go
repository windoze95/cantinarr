package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/config"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/remediation"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

func TestAppleAppSiteAssociationHandler(t *testing.T) {
	cfg := &config.Config{
		AppleAppIDs: []string{"TEAMID.codes.julian.cantinarr"},
	}
	req := httptest.NewRequest(http.MethodGet, "/.well-known/apple-app-site-association", nil)
	rec := httptest.NewRecorder()

	appleAppSiteAssociationHandler(cfg)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]map[string][]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	apps := body["webcredentials"]["apps"]
	if len(apps) != 1 || apps[0] != "TEAMID.codes.julian.cantinarr" {
		t.Fatalf("apps = %#v", apps)
	}
}

func TestAppleAppSiteAssociationHandlerNotConfigured(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/.well-known/apple-app-site-association", nil)
	rec := httptest.NewRecorder()

	appleAppSiteAssociationHandler(&config.Config{})(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestAndroidAssetLinksHandler(t *testing.T) {
	cfg := &config.Config{
		AndroidPackageName:      "codes.julian.cantinarr",
		AndroidCertFingerprints: []string{"AA:BB"},
	}
	req := httptest.NewRequest(http.MethodGet, "/.well-known/assetlinks.json", nil)
	rec := httptest.NewRecorder()

	androidAssetLinksHandler(cfg)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body []struct {
		Relation []string `json:"relation"`
		Target   struct {
			Namespace              string   `json:"namespace"`
			PackageName            string   `json:"package_name"`
			SHA256CertFingerprints []string `json:"sha256_cert_fingerprints"`
		} `json:"target"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("len(body) = %d", len(body))
	}
	if len(body[0].Relation) != 1 ||
		body[0].Relation[0] != "delegate_permission/common.get_login_creds" {
		t.Fatalf("relation = %#v", body[0].Relation)
	}
	if body[0].Target.PackageName != "codes.julian.cantinarr" {
		t.Fatalf("package_name = %q", body[0].Target.PackageName)
	}
	if len(body[0].Target.SHA256CertFingerprints) != 1 ||
		body[0].Target.SHA256CertFingerprints[0] != "AA:BB" {
		t.Fatalf("fingerprints = %#v", body[0].Target.SHA256CertFingerprints)
	}
}

func TestConfigHandlerFiltersInstancesForNonAdmin(t *testing.T) {
	store, creds, remediationSvc, userID := newConfigHandlerTestState(t)
	mainRadarr := createConfigInstance(t, store, "radarr", "Main Radarr", true)
	fourKRadarr := createConfigInstance(t, store, "radarr", "4K Radarr", false)
	mainSonarr := createConfigInstance(t, store, "sonarr", "Main Sonarr", true)
	createConfigInstance(t, store, "sonarr", "Anime Sonarr", false)
	books := createConfigInstance(t, store, "chaptarr", "Books", false)
	createConfigInstance(t, store, "chaptarr", "Private Books", false)
	createConfigInstance(t, store, "sabnzbd", "Downloads", false)
	createConfigInstance(t, store, "tautulli", "Tautulli", false)

	if err := store.SetUserDefault(userID, "radarr", fourKRadarr.ID); err != nil {
		t.Fatalf("pin radarr: %v", err)
	}
	if err := store.SetUserDefault(userID, "chaptarr", books.ID); err != nil {
		t.Fatalf("grant chaptarr: %v", err)
	}

	resp := requestConfig(t, store, creds, remediationSvc, &auth.Claims{
		UserID:   userID,
		Username: "alice",
		Role:     auth.RoleUser,
	})

	want := map[string]string{
		fourKRadarr.ID: "radarr",
		mainSonarr.ID:  "sonarr",
		books.ID:       "chaptarr",
	}
	if len(resp.Instances) != len(want) {
		t.Fatalf("instances = %#v, want %d visible instances", resp.Instances, len(want))
	}
	for _, inst := range resp.Instances {
		if want[inst.ID] != inst.ServiceType {
			t.Fatalf("unexpected visible instance: %#v", inst)
		}
		if inst.ID == mainRadarr.ID {
			t.Fatal("non-admin should not see the non-pinned Radarr instance")
		}
		if (inst.ServiceType == "radarr" || inst.ServiceType == "sonarr" || inst.ServiceType == "chaptarr") && !inst.IsDefault {
			t.Fatalf("visible user instance should be marked default: %#v", inst)
		}
	}
	if !resp.Services["radarr"] || !resp.Services["sonarr"] || !resp.Services["chaptarr"] {
		t.Fatalf("services = %#v, want radarr/sonarr/chaptarr visible", resp.Services)
	}
}

func TestConfigHandlerShowsAllInstancesForAdmin(t *testing.T) {
	store, creds, remediationSvc, userID := newConfigHandlerTestState(t)
	createConfigInstance(t, store, "radarr", "Main Radarr", true)
	createConfigInstance(t, store, "radarr", "4K Radarr", false)
	createConfigInstance(t, store, "sonarr", "Main Sonarr", true)
	createConfigInstance(t, store, "chaptarr", "Books", false)
	createConfigInstance(t, store, "sabnzbd", "Downloads", false)
	createConfigInstance(t, store, "tautulli", "Tautulli", false)

	resp := requestConfig(t, store, creds, remediationSvc, &auth.Claims{
		UserID:   userID,
		Username: "admin",
		Role:     auth.RoleAdmin,
	})

	if len(resp.Instances) != 6 {
		t.Fatalf("instances = %#v, want all 6 instances", resp.Instances)
	}
}

type configHandlerResponse struct {
	Services  map[string]bool `json:"services"`
	Instances []struct {
		ID          string `json:"id"`
		ServiceType string `json:"service_type"`
		Name        string `json:"name"`
		IsDefault   bool   `json:"is_default"`
	} `json:"instances"`
}

func newConfigHandlerTestState(t *testing.T) (*instance.Store, *credentials.Registry, *remediation.Service, int64) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	store := instance.NewStore(database, cipher)
	creds := credentials.NewRegistry(database, cipher)
	remediationSvc := remediation.NewService(database, nil, nil, nil)

	res, err := database.Exec(
		"INSERT INTO users (username, password_hash, role) VALUES (?, '', ?)",
		"alice",
		auth.RoleUser,
	)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	userID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	return store, creds, remediationSvc, userID
}

func createConfigInstance(t *testing.T, store *instance.Store, serviceType, name string, isDefault bool) instance.Instance {
	t.Helper()
	inst := instance.Instance{
		ServiceType: serviceType,
		Name:        name,
		URL:         "http://localhost",
		APIKey:      "key",
		IsDefault:   isDefault,
	}
	if err := store.Create(&inst); err != nil {
		t.Fatalf("create %s instance: %v", serviceType, err)
	}
	return inst
}

func requestConfig(
	t *testing.T,
	store *instance.Store,
	creds *credentials.Registry,
	remediationSvc *remediation.Service,
	claims *auth.Claims,
) configHandlerResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, claims))
	rec := httptest.NewRecorder()

	configHandler(&config.Config{}, store, creds, remediationSvc)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp configHandlerResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	return resp
}
