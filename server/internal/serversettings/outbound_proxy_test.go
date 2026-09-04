package serversettings

import (
	"bytes"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

func newCipherService(t *testing.T) (*Service, *secrets.Cipher) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x24}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return NewService(database, func() bool { return false }, WithCipher(cipher)), cipher
}

func TestNormalizeProxyURL(t *testing.T) {
	accepted := map[string]string{
		"":                          "",
		"  http://proxy:8118/ ":     "http://proxy:8118",
		"https://proxy.example.com": "https://proxy.example.com",
		"socks5://10.0.0.5:1080":    "socks5://10.0.0.5:1080",
		"socks5h://gluetun:1080":    "socks5h://gluetun:1080",
		"http://[fd00::1]:3128":     "http://[fd00::1]:3128",
		"HTTP://Proxy.Local:8118":   "http://Proxy.Local:8118",
	}
	for raw, want := range accepted {
		got, err := normalizeProxyURL(raw)
		if err != nil {
			t.Errorf("normalizeProxyURL(%q) = error %v, want %q", raw, err, want)
			continue
		}
		if got != want {
			t.Errorf("normalizeProxyURL(%q) = %q, want %q", raw, got, want)
		}
	}
	rejected := []string{
		"proxy:8118",              // no scheme
		"192.168.1.5:8118",        // no scheme
		"ftp://proxy:21",          // scheme the transport cannot dial through
		"socks4://proxy:1080",     // Go has no SOCKS4
		"http://",                 // no host
		"http://proxy:8118/path",  // a path
		"http://proxy:8118?x=1",   // a query
		"http://proxy:8118#frag",  // a fragment
		"http://user:pw@proxy:80", // credentials belong in their own fields
		"just some text",
	}
	for _, raw := range rejected {
		if _, err := normalizeProxyURL(raw); err == nil {
			t.Errorf("normalizeProxyURL(%q) = nil, want error", raw)
		}
	}
	if _, err := normalizeProxyURL("http://user:pw@proxy:80"); err == nil || !strings.Contains(err.Error(), "username and password fields") {
		t.Errorf("credentials in the address should point at the fields, got %v", err)
	}
}

// TestSetOutboundProxyRoundTrip pins storage: one encrypted row holding the
// credential-bearing URL, read back split into address, username, password.
func TestSetOutboundProxyRoundTrip(t *testing.T) {
	s, _ := newCipherService(t)

	saved, err := s.SetOutboundProxy(OutboundProxy{URL: "http://proxy:8118/", Username: "alice", Password: "s3cret pw"})
	if err != nil {
		t.Fatalf("SetOutboundProxy: %v", err)
	}
	if saved.URL != "http://proxy:8118" || saved.Username != "alice" || saved.Password != "s3cret pw" {
		t.Fatalf("saved = %+v", saved)
	}
	if got := saved.ProxyURL(); got != "http://alice:s3cret%20pw@proxy:8118" {
		t.Errorf("ProxyURL = %q", got)
	}

	var stored string
	if err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", KeyOutboundProxyURL).Scan(&stored); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if !strings.HasPrefix(stored, "enc:v1:") || strings.Contains(stored, "s3cret") {
		t.Fatalf("row is not encrypted at rest: %q", stored)
	}

	read, err := s.OutboundProxy()
	if err != nil {
		t.Fatalf("OutboundProxy: %v", err)
	}
	if read != saved {
		t.Fatalf("read back %+v, want %+v", read, saved)
	}

	if _, err := s.SetOutboundProxy(OutboundProxy{}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	cleared, err := s.OutboundProxy()
	if err != nil || cleared.Configured() {
		t.Fatalf("after clearing: %+v, %v", cleared, err)
	}
	var count int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM settings WHERE key = ?", KeyOutboundProxyURL).Scan(&count)
	if count != 0 {
		t.Errorf("clearing should delete the row, found %d", count)
	}
}

// TestResolveOutboundProxyKeepsStoredPassword is the write-only credential
// contract: a blank password reuses the stored one for the same username, a
// different username drops it, and no username means no credentials at all.
func TestResolveOutboundProxyKeepsStoredPassword(t *testing.T) {
	s, _ := newCipherService(t)
	if _, err := s.SetOutboundProxy(OutboundProxy{URL: "http://proxy:8118", Username: "alice", Password: "pw1"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	moved, err := s.ResolveOutboundProxy(OutboundProxy{URL: "http://other:3128", Username: " alice ", Password: ""})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if moved.Password != "pw1" || moved.URL != "http://other:3128" || moved.Username != "alice" {
		t.Errorf("blank password with the same username should keep the stored one: %+v", moved)
	}
	if stored, _ := s.OutboundProxy(); stored.URL != "http://proxy:8118" {
		t.Errorf("Resolve must not save; stored is now %+v", stored)
	}

	renamed, err := s.ResolveOutboundProxy(OutboundProxy{URL: "http://proxy:8118", Username: "bob", Password: ""})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if renamed.Password != "" {
		t.Errorf("a changed username must not inherit the old password: %+v", renamed)
	}

	anonymous, err := s.ResolveOutboundProxy(OutboundProxy{URL: "http://proxy:8118", Username: "", Password: "ignored"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if anonymous.Username != "" || anonymous.Password != "" {
		t.Errorf("no username means no credentials: %+v", anonymous)
	}

	if _, err := s.ResolveOutboundProxy(OutboundProxy{URL: "ftp://proxy:21"}); err == nil {
		t.Error("an invalid address must be rejected before any merge")
	}
}

// TestOutboundProxyWithoutCipherStoresPlaintext keeps the test-only path
// honest and proves the read side tolerates an unencrypted row.
func TestOutboundProxyWithoutCipherStoresPlaintext(t *testing.T) {
	s := newTestService(t, false)
	if _, err := s.SetOutboundProxy(OutboundProxy{URL: "socks5://proxy:1080"}); err != nil {
		t.Fatalf("SetOutboundProxy: %v", err)
	}
	var stored string
	if err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", KeyOutboundProxyURL).Scan(&stored); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if stored != "socks5://proxy:1080" {
		t.Errorf("stored = %q", stored)
	}
	read, err := s.OutboundProxy()
	if err != nil || read.URL != "socks5://proxy:1080" || read.Username != "" {
		t.Errorf("read = %+v, %v", read, err)
	}
}

// TestOutboundProxyRejectsUnreadableRow: an undecryptable row is an error the
// caller sees, never an empty proxy.
func TestOutboundProxyRejectsUnreadableRow(t *testing.T) {
	s, _ := newCipherService(t)
	if _, err := s.db.Exec("INSERT INTO settings (key, value) VALUES (?, ?)", KeyOutboundProxyURL, "enc:v1:not-really-ciphertext"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := s.OutboundProxy(); err == nil {
		t.Fatal("expected a decrypt error")
	}
	if _, err := s.db.Exec("UPDATE settings SET value = ? WHERE key = ?", "ftp://nope:21", KeyOutboundProxyURL); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := s.OutboundProxy(); err == nil {
		t.Fatal("expected a validation error for a hand-edited row")
	}
}
