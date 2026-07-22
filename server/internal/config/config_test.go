package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
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
		"https://:443",
	} {
		t.Run(invalid, func(t *testing.T) {
			t.Setenv("CANTINARR_PUBLIC_URL", invalid)
			if _, err := Load(); err == nil {
				t.Fatal("Load() error = nil, want invalid public URL error")
			}
		})
	}
}

func TestLoadValidatesMCPOriginConfig(t *testing.T) {
	t.Setenv("CANTINARR_OAUTH_ISSUER", "https://Media.Example.com:8443/")
	t.Setenv("CANTINARR_MCP_ALLOWED_ORIGINS", " https://Chat.Example.com, http://localhost:3000/ ")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.OAuthIssuer != "https://media.example.com:8443" {
		t.Fatalf("OAuthIssuer = %q", cfg.OAuthIssuer)
	}
	wantOrigins := []string{"https://chat.example.com", "http://localhost:3000"}
	if len(cfg.MCPAllowedOrigins) != len(wantOrigins) {
		t.Fatalf("MCPAllowedOrigins = %#v", cfg.MCPAllowedOrigins)
	}
	for i, want := range wantOrigins {
		if cfg.MCPAllowedOrigins[i] != want {
			t.Fatalf("MCPAllowedOrigins[%d] = %q, want %q", i, cfg.MCPAllowedOrigins[i], want)
		}
	}

	for name, value := range map[string]string{
		"origin query":      "https://chat.example.com?token=secret",
		"origin scheme":     "javascript://chat.example.com",
		"origin empty host": "https://:443",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CANTINARR_OAUTH_ISSUER", "")
			t.Setenv("CANTINARR_MCP_ALLOWED_ORIGINS", value)
			if _, err := Load(); err == nil {
				t.Fatal("Load() error = nil, want invalid MCP origin error")
			}
		})
	}

	for name, issuer := range map[string]string{
		"path":       "https://media.example.com/oauth",
		"insecure":   "http://media.example.com",
		"empty host": "https://:443",
	} {
		t.Run("issuer "+name, func(t *testing.T) {
			t.Setenv("CANTINARR_OAUTH_ISSUER", issuer)
			t.Setenv("CANTINARR_MCP_ALLOWED_ORIGINS", "")
			if _, err := Load(); err == nil {
				t.Fatal("Load() error = nil, want invalid OAuth issuer error")
			}
		})
	}
}

func TestLoadCodexRuntimeConfig(t *testing.T) {
	t.Setenv("CANTINARR_CODEX_BIN", " /opt/codex-app-server ")
	t.Setenv("CANTINARR_CODEX_RUNTIME_DIR", "/dev/shm/cantinarr-test")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.CodexBin != "/opt/codex-app-server" {
		t.Fatalf("CodexBin = %q", cfg.CodexBin)
	}
	if cfg.CodexRuntimeDir != "/dev/shm/cantinarr-test" {
		t.Fatalf("CodexRuntimeDir = %q", cfg.CodexRuntimeDir)
	}

	t.Setenv("CANTINARR_CODEX_RUNTIME_DIR", "relative/codex")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want relative Codex runtime dir rejection")
	}
}

func TestLoadMediaDownloadRoots(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	alias := filepath.Join(t.TempDir(), "media-link")
	if err := os.Symlink(first, alias); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	t.Setenv("CANTINARR_MEDIA_ROOTS", first+", "+second+", "+alias)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.MediaDownloadRoots) != 3 {
		t.Fatalf("MediaDownloadRoots = %#v, want three lexical roots", cfg.MediaDownloadRoots)
	}
	if cfg.MediaDownloadRoots[0] != filepath.Clean(first) ||
		cfg.MediaDownloadRoots[1] != filepath.Clean(second) ||
		cfg.MediaDownloadRoots[2] != filepath.Clean(alias) {
		t.Fatalf("MediaDownloadRoots = %#v, want lexical paths [%q %q %q]", cfg.MediaDownloadRoots, first, second, alias)
	}
}

func TestLoadRejectsUnsafeMediaDownloadRoots(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	rootAlias := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(string(filepath.Separator), rootAlias); err != nil {
		t.Fatalf("Symlink(root) error = %v", err)
	}

	for name, value := range map[string]string{
		"relative":        "media",
		"filesystem root": string(filepath.Separator),
		"root symlink":    rootAlias,
		"regular file":    file,
		"missing":         missing,
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CANTINARR_MEDIA_ROOTS", value)
			if _, err := Load(); err == nil {
				t.Fatal("Load() error = nil, want invalid media root error")
			}
		})
	}
}

func TestLoadPort(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		serviceHost string
		servicePort string
		want        int
		wantErr     bool
	}{
		{name: "default", value: "", want: 8585},
		{name: "numeric override", value: "8586", want: 8586},
		{name: "Kubernetes IPv4 service link", value: "tcp://10.43.161.118:8585", serviceHost: "10.43.161.118", servicePort: "8585", want: 8585},
		{name: "Kubernetes IPv6 service link", value: "tcp://[fd00::1]:8585", serviceHost: "fd00::1", servicePort: "8585", want: 8585},
		{name: "numeric override beside service link companions", value: "8586", serviceHost: "10.43.161.118", servicePort: "8585", want: 8586},
		{name: "invalid value", value: "not-a-port", wantErr: true},
		{name: "malformed TCP URI", value: "tcp://localhost:not-a-port", wantErr: true},
		{name: "non-service TCP URI", value: "tcp://example.com:9999", wantErr: true},
		{name: "manual IP TCP URI", value: "tcp://127.0.0.1:9000", wantErr: true},
		{name: "mismatched service link host", value: "tcp://10.43.161.119:8585", serviceHost: "10.43.161.118", servicePort: "8585", wantErr: true},
		{name: "mismatched service link port", value: "tcp://10.43.161.118:8585", serviceHost: "10.43.161.118", servicePort: "9999", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CANTINARR_PORT", tt.value)
			t.Setenv("CANTINARR_SERVICE_HOST", tt.serviceHost)
			t.Setenv("CANTINARR_SERVICE_PORT", tt.servicePort)
			cfg, err := Load()
			if tt.wantErr {
				if err == nil {
					t.Fatal("Load() error = nil, want invalid port error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.Port != tt.want {
				t.Fatalf("Port = %d, want %d", cfg.Port, tt.want)
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
