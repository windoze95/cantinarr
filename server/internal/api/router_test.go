package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

// SEC-018: An unflagged Radarr/Sonarr fallback is still the requester's effective default.
func TestConfigHandlerMarksDeterministicFallbackAsEffectiveDefault(t *testing.T) {
	store, creds, remediationSvc, userID := newConfigHandlerTestState(t)
	// Deliberately insert the lexically later siblings first and leave every
	// global default flag unset. ListAll's stable order and proxy authorization's
	// metadata-only fallback both select Alpha.
	createConfigInstance(t, store, "radarr", "Zulu Radarr", false)
	alphaRadarr := createConfigInstance(t, store, "radarr", "Alpha Radarr", false)
	createConfigInstance(t, store, "sonarr", "Zulu Sonarr", false)
	alphaSonarr := createConfigInstance(t, store, "sonarr", "Alpha Sonarr", false)

	resp := requestConfig(t, store, creds, remediationSvc, &auth.Claims{
		UserID: userID,
		Role:   auth.RoleUser,
	})
	want := map[string]string{
		alphaRadarr.ID: "radarr",
		alphaSonarr.ID: "sonarr",
	}
	if len(resp.Instances) != len(want) {
		t.Fatalf("instances = %#v, want exactly the two deterministic fallbacks", resp.Instances)
	}
	for _, inst := range resp.Instances {
		if want[inst.ID] != inst.ServiceType {
			t.Fatalf("unexpected fallback instance: %#v", inst)
		}
		if !inst.IsDefault {
			t.Fatalf("effective fallback must be marked default: %#v", inst)
		}
	}
}

// SEC-018: A defaults/grants lookup failure cannot expose global or hidden sibling metadata.
func TestConfigHandlerFailsClosedWhenUserDefaultsUnavailable(t *testing.T) {
	_, creds, remediationSvc, userID := newConfigHandlerTestState(t)
	store := &failingConfigInstanceStore{
		defaultsErr: errors.New("database detail must stay private"),
	}
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{
		UserID: userID,
		Role:   auth.RoleUser,
	}))
	rec := httptest.NewRecorder()

	configHandler(&config.Config{}, store, creds, nil, remediationSvc)(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if store.listAllCalls != 0 {
		t.Fatalf("ListAll calls = %d, want 0 after defaults lookup failure", store.listAllCalls)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(store.defaultsErr.Error())) {
		t.Fatalf("config error leaked storage detail: %s", rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestConfigHandlerReportsMediaDownloadCapabilityWithoutExposingRoots(t *testing.T) {
	store, creds, remediationSvc, userID := newConfigHandlerTestState(t)
	rootSentinel := "/private/library/path-must-not-cross-api"
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{
		UserID: userID,
		Role:   auth.RoleUser,
	}))
	rec := httptest.NewRecorder()

	configHandler(&config.Config{MediaDownloadRoots: []string{rootSentinel}}, store, creds, nil, remediationSvc)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response configHandlerResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if !response.Services["media_downloads"] {
		t.Fatalf("services = %#v, want media_downloads capability", response.Services)
	}
	if strings.Contains(rec.Body.String(), rootSentinel) {
		t.Fatalf("config exposed media root: %s", rec.Body.String())
	}
}

// SEC-018: requester and admin config responses expose only effective, non-secret configuration.
func TestConfigHandlerResponsesUseLeastPrivilegeSecretFreeShapes(t *testing.T) {
	store, creds, remediationSvc, userID := newConfigHandlerTestState(t)
	instances := []instance.Instance{
		{
			ServiceType: "radarr", Name: "Main Radarr", IsDefault: true,
			URL: "https://main-radarr-url-secret.invalid", APIKey: "main-radarr-api-secret",
			Username: "main-radarr-user-secret", Password: "main-radarr-password-secret",
		},
		{
			ServiceType: "radarr", Name: "Pinned Radarr",
			URL: "https://pinned-radarr-url-secret.invalid", APIKey: "pinned-radarr-api-secret",
			Username: "pinned-radarr-user-secret", Password: "pinned-radarr-password-secret",
		},
		{
			ServiceType: "sonarr", Name: "Main Sonarr", IsDefault: true,
			URL: "https://main-sonarr-url-secret.invalid", APIKey: "main-sonarr-api-secret",
			Username: "main-sonarr-user-secret", Password: "main-sonarr-password-secret",
		},
		{
			ServiceType: "chaptarr", Name: "Granted Books",
			URL: "https://books-url-secret.invalid", APIKey: "books-api-secret",
			Username: "books-user-secret", Password: "books-password-secret",
		},
		{
			ServiceType: "chaptarr", Name: "Private Books",
			URL: "https://private-books-url-secret.invalid", APIKey: "private-books-api-secret",
			Username: "private-books-user-secret", Password: "private-books-password-secret",
		},
		{
			ServiceType: "qbittorrent", Name: "Admin Download Client",
			URL: "https://download-url-secret.invalid", APIKey: "download-api-secret",
			Username: "download-user-secret", Password: "download-password-secret",
		},
	}
	secretSentinels := make([]string, 0, len(instances)*4+len(credentials.AllKeys)+8)
	for i := range instances {
		if err := store.Create(&instances[i]); err != nil {
			t.Fatalf("create %s instance: %v", instances[i].ServiceType, err)
		}
		secretSentinels = append(secretSentinels,
			instances[i].URL,
			instances[i].APIKey,
			instances[i].Username,
			instances[i].Password,
		)
	}
	if err := store.SetUserDefault(userID, "radarr", instances[1].ID); err != nil {
		t.Fatalf("pin requester Radarr: %v", err)
	}
	if err := store.SetUserDefault(userID, "chaptarr", instances[3].ID); err != nil {
		t.Fatalf("grant requester Chaptarr: %v", err)
	}
	webhookToken, err := store.WebhookToken(instances[1].ID)
	if err != nil {
		t.Fatalf("create webhook token: %v", err)
	}
	pendingWebhookToken, err := store.PrepareWebhookToken(instances[1].ID)
	if err != nil {
		t.Fatalf("prepare webhook token: %v", err)
	}
	secretSentinels = append(secretSentinels, webhookToken, pendingWebhookToken)

	for _, key := range credentials.AllKeys {
		value := "credential-secret-" + key
		if err := creds.SetCredential(key, value); err != nil {
			t.Fatalf("seed credential %s: %v", key, err)
		}
		secretSentinels = append(secretSentinels, value)
	}

	cfg := &config.Config{
		JWTSecret:         "config-jwt-secret",
		ServerName:        "Coverage Lab",
		PublicURL:         "https://config-public-url-secret.invalid",
		EncryptionKeyFile: "/config/encryption-key-path-secret",
		PushGatewayURL:    "https://config-push-gateway-secret.invalid",
		PushAPIKey:        "config-push-api-key-secret",
		PushEnrollToken:   "config-push-enroll-token-secret",
		CodexBin:          "/config/codex-bin-secret",
		CodexRuntimeDir:   "/config/codex-runtime-secret",
	}
	secretSentinels = append(secretSentinels,
		cfg.JWTSecret,
		cfg.PublicURL,
		cfg.EncryptionKeyFile,
		cfg.PushGatewayURL,
		cfg.PushAPIKey,
		cfg.PushEnrollToken,
		cfg.CodexBin,
		cfg.CodexRuntimeDir,
	)

	tests := []struct {
		name      string
		role      string
		instances map[string]string
	}{
		{
			name: "requester sees only effective defaults and grants",
			role: auth.RoleUser,
			instances: map[string]string{
				instances[1].ID: "radarr",
				instances[2].ID: "sonarr",
				instances[3].ID: "chaptarr",
			},
		},
		{
			name: "admin sees all instance metadata",
			role: auth.RoleAdmin,
			instances: map[string]string{
				instances[0].ID: "radarr",
				instances[1].ID: "radarr",
				instances[2].ID: "sonarr",
				instances[3].ID: "chaptarr",
				instances[4].ID: "chaptarr",
				instances[5].ID: "qbittorrent",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
			req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{
				UserID: userID,
				Role:   tt.role,
			}))
			rec := httptest.NewRecorder()
			configHandler(cfg, store, creds, nil, remediationSvc)(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}

			raw := rec.Body.Bytes()
			for _, secret := range secretSentinels {
				if secret != "" && bytes.Contains(raw, []byte(secret)) {
					t.Fatalf("config response exposed sentinel %q: %s", secret, raw)
				}
			}
			if bytes.Contains(raw, []byte("enc:v1:")) {
				t.Fatalf("config response exposed an encrypted credential blob: %s", raw)
			}

			var payload map[string]json.RawMessage
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatalf("decode config response: %v", err)
			}
			assertExactMapKeys(t, payload,
				"server_name", "version", "services", "instances", "issues_enabled", "allow_reporting",
			)

			var services map[string]bool
			if err := json.Unmarshal(payload["services"], &services); err != nil {
				t.Fatalf("decode services: %v", err)
			}
			assertExactMapKeys(t, services, "radarr", "sonarr", "chaptarr", "media_downloads", "ai", "tmdb", "trakt")
			for _, serviceType := range []string{"radarr", "sonarr", "chaptarr", "ai", "tmdb", "trakt"} {
				if !services[serviceType] {
					t.Errorf("services[%q] = false, want true", serviceType)
				}
			}
			if services["media_downloads"] {
				t.Error("services[\"media_downloads\"] = true with no configured roots")
			}

			var gotInstances []map[string]json.RawMessage
			if err := json.Unmarshal(payload["instances"], &gotInstances); err != nil {
				t.Fatalf("decode instances: %v", err)
			}
			if len(gotInstances) != len(tt.instances) {
				t.Fatalf("instances = %s, want %d entries", payload["instances"], len(tt.instances))
			}
			seen := make(map[string]bool, len(gotInstances))
			for _, got := range gotInstances {
				assertExactMapKeys(t, got, "id", "service_type", "name", "is_default")
				var id, serviceType string
				if err := json.Unmarshal(got["id"], &id); err != nil {
					t.Fatalf("decode instance id: %v", err)
				}
				if err := json.Unmarshal(got["service_type"], &serviceType); err != nil {
					t.Fatalf("decode instance service type: %v", err)
				}
				wantServiceType, ok := tt.instances[id]
				if !ok || wantServiceType != serviceType {
					t.Errorf("unexpected config instance id=%q service_type=%q", id, serviceType)
				}
				if seen[id] {
					t.Errorf("config instance id=%q appeared more than once", id)
				}
				seen[id] = true
			}
		})
	}
}

func assertExactMapKeys[V any](t *testing.T, got map[string]V, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("JSON keys = %#v, want exactly %v", got, want)
	}
	for _, key := range want {
		if _, ok := got[key]; !ok {
			t.Fatalf("JSON keys = %#v, missing %q", got, key)
		}
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

type failingConfigInstanceStore struct {
	defaultsErr  error
	listAllCalls int
}

func (s *failingConfigInstanceStore) ListUserDefaults(int64) (map[string]string, error) {
	return nil, s.defaultsErr
}

func (s *failingConfigInstanceStore) ListAll() ([]instance.Instance, error) {
	s.listAllCalls++
	return []instance.Instance{{
		ID:          "hidden-radarr",
		ServiceType: "radarr",
		Name:        "Hidden sibling",
		IsDefault:   true,
	}}, nil
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

	configHandler(&config.Config{}, store, creds, nil, remediationSvc)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp configHandlerResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	return resp
}
