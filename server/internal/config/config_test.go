package config

import (
	"encoding/base64"
	"testing"
)

func TestLoadWebAuthnNativeConfig(t *testing.T) {
	t.Setenv("CANTINARR_WEBAUTHN_EXTRA_ORIGINS", "android:apk-key-hash:manual, https://example.com ")
	t.Setenv("CANTINARR_APPLE_APP_IDS", "TEAMID.codes.julian.cantinarr")
	t.Setenv("CANTINARR_ANDROID_PACKAGE_NAME", "codes.julian.cantinarr")
	t.Setenv("CANTINARR_ANDROID_CERT_SHA256_FINGERPRINTS", "000102030405060708090A0B0C0D0E0F101112131415161718191A1B1C1D1E1F")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.AndroidPackageName != "codes.julian.cantinarr" {
		t.Fatalf("AndroidPackageName = %q", cfg.AndroidPackageName)
	}
	if len(cfg.AppleAppIDs) != 1 || cfg.AppleAppIDs[0] != "TEAMID.codes.julian.cantinarr" {
		t.Fatalf("AppleAppIDs = %#v", cfg.AppleAppIDs)
	}
	expectedFingerprint := "00:01:02:03:04:05:06:07:08:09:0A:0B:0C:0D:0E:0F:10:11:12:13:14:15:16:17:18:19:1A:1B:1C:1D:1E:1F"
	if len(cfg.AndroidCertFingerprints) != 1 || cfg.AndroidCertFingerprints[0] != expectedFingerprint {
		t.Fatalf("AndroidCertFingerprints = %#v", cfg.AndroidCertFingerprints)
	}

	digest := make([]byte, 32)
	for i := range digest {
		digest[i] = byte(i)
	}
	expectedOrigin := "android:apk-key-hash:" + base64.RawURLEncoding.EncodeToString(digest)
	if !contains(cfg.WebAuthnExtraOrigins, expectedOrigin) {
		t.Fatalf("WebAuthnExtraOrigins = %#v, want %q", cfg.WebAuthnExtraOrigins, expectedOrigin)
	}
	if !contains(cfg.WebAuthnExtraOrigins, "android:apk-key-hash:manual") {
		t.Fatalf("WebAuthnExtraOrigins = %#v, want manual origin", cfg.WebAuthnExtraOrigins)
	}
}

func TestLoadRejectsInvalidAndroidFingerprint(t *testing.T) {
	t.Setenv("CANTINARR_ANDROID_CERT_SHA256_FINGERPRINTS", "not-a-fingerprint")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid fingerprint error")
	}
}

func TestLoadValidatesPublicURL(t *testing.T) {
	t.Setenv("CANTINARR_PUBLIC_URL", "https://cantinarr.example:8443/")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.PublicURL != "https://cantinarr.example:8443" {
		t.Fatalf("PublicURL = %q", cfg.PublicURL)
	}

	for _, invalid := range []string{
		"javascript://attacker.example",
		"https://user:password@example.com",
		"https://example.com/subpath",
		"https://example.com?token=secret",
	} {
		t.Run(invalid, func(t *testing.T) {
			t.Setenv("CANTINARR_PUBLIC_URL", invalid)
			if _, err := Load(); err == nil {
				t.Fatal("Load() error = nil, want invalid public URL error")
			}
		})
	}
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
