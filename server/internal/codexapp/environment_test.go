package codexapp

import (
	"strings"
	"testing"
)

func envValue(env []string, key string) (string, bool) {
	for _, entry := range env {
		if k, v, ok := strings.Cut(entry, "="); ok && k == key {
			return v, true
		}
	}
	return "", false
}

// TestIsolatedEnvironmentPassesProxyVariablesThrough: with no in-app proxy the
// standard variables reach the app-server exactly as they reach the server.
func TestIsolatedEnvironmentPassesProxyVariablesThrough(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://env-proxy:3128")
	t.Setenv("no_proxy", "localhost")
	env := isolatedEnvironment("/home", "/tmp", "")
	if got, _ := envValue(env, "HTTPS_PROXY"); got != "http://env-proxy:3128" {
		t.Errorf("HTTPS_PROXY = %q, want the parent's value", got)
	}
	if got, _ := envValue(env, "no_proxy"); got != "localhost" {
		t.Errorf("no_proxy = %q, want the parent's value", got)
	}
}

// TestIsolatedEnvironmentAdminProxyReplacesInherited: the in-app setting wins
// over the environment, and nothing inherited (not even NO_PROXY) can carve
// the app-server's one destination out of it.
func TestIsolatedEnvironmentAdminProxyReplacesInherited(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://env-proxy:3128")
	t.Setenv("http_proxy", "http://env-proxy:3128")
	t.Setenv("ALL_PROXY", "socks5://env-proxy:1080")
	t.Setenv("NO_PROXY", "api.openai.com")
	env := isolatedEnvironment("/home", "/tmp", "http://admin-proxy:8118")
	for _, entry := range env {
		if strings.Contains(entry, "env-proxy") {
			t.Errorf("inherited proxy variable leaked: %s", entry)
		}
	}
	for _, key := range []string{"NO_PROXY", "ALL_PROXY"} {
		if _, ok := envValue(env, key); ok {
			t.Errorf("%s should be dropped when the admin proxy is set", key)
		}
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		if got, _ := envValue(env, key); got != "http://admin-proxy:8118" {
			t.Errorf("%s = %q, want the admin proxy", key, got)
		}
	}
	if got, _ := envValue(env, "HOME"); got != "/home" {
		t.Errorf("HOME = %q, want the sandbox home", got)
	}
}
