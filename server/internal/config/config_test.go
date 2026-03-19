package config

import (
	"testing"
)

func TestAllowedOrigins_Empty(t *testing.T) {
	t.Setenv("CANTINARR_ALLOWED_ORIGINS", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AllowedOrigins != nil {
		t.Fatalf("expected nil, got %v", cfg.AllowedOrigins)
	}
}

func TestAllowedOrigins_Single(t *testing.T) {
	t.Setenv("CANTINARR_ALLOWED_ORIGINS", "http://localhost:3000")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.AllowedOrigins) != 1 || cfg.AllowedOrigins[0] != "http://localhost:3000" {
		t.Fatalf("expected [http://localhost:3000], got %v", cfg.AllowedOrigins)
	}
}

func TestAllowedOrigins_Multiple(t *testing.T) {
	t.Setenv("CANTINARR_ALLOWED_ORIGINS", "http://localhost:3000, https://app.example.com")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.AllowedOrigins) != 2 {
		t.Fatalf("expected 2 origins, got %d: %v", len(cfg.AllowedOrigins), cfg.AllowedOrigins)
	}
	if cfg.AllowedOrigins[0] != "http://localhost:3000" || cfg.AllowedOrigins[1] != "https://app.example.com" {
		t.Fatalf("unexpected origins: %v", cfg.AllowedOrigins)
	}
}

func TestAllowedOrigins_EmptyEntries(t *testing.T) {
	t.Setenv("CANTINARR_ALLOWED_ORIGINS", "http://localhost:3000,,, https://app.example.com,")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.AllowedOrigins) != 2 {
		t.Fatalf("expected 2 origins (empty entries ignored), got %d: %v", len(cfg.AllowedOrigins), cfg.AllowedOrigins)
	}
}
