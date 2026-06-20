package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/config"
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
