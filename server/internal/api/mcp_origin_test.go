package api

import (
	"testing"

	"github.com/windoze95/cantinarr-server/internal/config"
)

func TestMCPOriginAllowedUsesCanonicalAndExplicitOrigins(t *testing.T) {
	cfg := &config.Config{
		OAuthIssuer:       "https://media.example.com",
		MCPAllowedOrigins: []string{"https://chat.example.com"},
	}

	for _, origin := range []string{"https://media.example.com", "https://chat.example.com"} {
		if !mcpOriginAllowed(cfg, origin) {
			t.Errorf("trusted origin %q was rejected", origin)
		}
	}
	for _, origin := range []string{"https://attacker.example", "null", "file://local"} {
		if mcpOriginAllowed(cfg, origin) {
			t.Errorf("untrusted origin %q was accepted", origin)
		}
	}
}

func TestMCPOriginAllowedRejectsUnconfiguredBrowserOrigins(t *testing.T) {
	for _, origin := range []string{"http://cantinarr.test", "https://cantinarr.test"} {
		if mcpOriginAllowed(&config.Config{}, origin) {
			t.Fatalf("unconfigured browser origin %q was accepted", origin)
		}
	}
}

func TestNormalizeMCPHTTPOrigin(t *testing.T) {
	for raw, want := range map[string]string{
		"HTTPS://Example.COM:443/": "https://example.com",
		"http://Example.COM:8080":  "http://example.com:8080",
		"http://[::1]:80":          "http://[::1]",
	} {
		got, ok := normalizeMCPHTTPOrigin(raw)
		if !ok || got != want {
			t.Errorf("normalizeMCPHTTPOrigin(%q) = %q, %v; want %q, true", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"null", "file://local", "https://example.com/path", "https://user@example.com"} {
		if got, ok := normalizeMCPHTTPOrigin(raw); ok {
			t.Errorf("normalizeMCPHTTPOrigin(%q) = %q, true; want rejection", raw, got)
		}
	}
}
