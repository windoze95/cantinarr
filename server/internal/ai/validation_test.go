package ai

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/codexapp"
	"github.com/windoze95/cantinarr-server/internal/credentials"
)

type recordingAIHealthSink struct {
	provider string
	model    string
	healthy  bool
	calls    int
}

func (s *recordingAIHealthSink) RecordSharedAIHealth(provider, model string, healthy bool) error {
	s.provider = provider
	s.model = model
	s.healthy = healthy
	s.calls++
	return nil
}

func TestValidateSharedAISettingsUsesSharedAccount(t *testing.T) {
	h, _, _, _ := newResolverTestHandler(t)
	want := credentials.AIProfile{
		Config:            credentials.AIConfig{Provider: credentials.AIProviderCodex, Model: "gpt-5.6-luna"},
		CredentialPresent: true,
	}
	h.validationProbe = func(_ context.Context, profile credentials.AIProfile, account codexapp.AccountRef) error {
		if profile != want {
			t.Fatalf("profile=%#v, want %#v", profile, want)
		}
		if account != codexapp.SharedAccount() {
			t.Fatalf("account=%#v, want shared", account)
		}
		return nil
	}
	if err := h.ValidateSharedAISettings(context.Background(), want); err != nil {
		t.Fatal(err)
	}
}

func TestSharedAIHealthCheckSkipsUntilConfiguredEnabledAndDue(t *testing.T) {
	t.Setenv("CANTINARR_AI_PROVIDER", "")
	t.Setenv("CANTINARR_AI_MODEL", "")
	h, registry, _, _ := newResolverTestHandler(t)
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	calls := 0
	h.validationProbe = func(context.Context, credentials.AIProfile, codexapp.AccountRef) error {
		calls++
		return nil
	}
	h.runSharedAIHealthCheck(context.Background(), now)
	if calls != 0 {
		t.Fatal("untouched install ran a provider probe")
	}
	if err := registry.SetCredential(credentials.KeyOpenAIKey, "shared-secret"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAIConfig(credentials.AIProviderOpenAI, "shared-model"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetSetting(credentials.KeyAIHealthCheckEnabled, "false"); err != nil {
		t.Fatal(err)
	}
	h.runSharedAIHealthCheck(context.Background(), now)
	if calls != 0 {
		t.Fatal("disabled monitor ran a provider probe")
	}
	if err := registry.SetSetting(credentials.KeyAIHealthCheckEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	if err := registry.RecordAIHealthCheck(now); err != nil {
		t.Fatal(err)
	}
	h.runSharedAIHealthCheck(context.Background(), now.Add(time.Hour))
	if calls != 0 {
		t.Fatal("not-due monitor ran a provider probe")
	}
	h.runSharedAIHealthCheck(context.Background(), now.Add(credentials.AIHealthCheckInterval))
	if calls != 1 {
		t.Fatalf("due monitor calls=%d, want 1", calls)
	}
}

func TestSharedAIHealthCheckRecordsFailureOncePerInterval(t *testing.T) {
	h, registry, _, _ := newResolverTestHandler(t)
	if err := registry.SetCredential(credentials.KeyOpenAIKey, "shared-secret"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAIConfig(credentials.AIProviderOpenAI, "shared-model"); err != nil {
		t.Fatal(err)
	}
	probeCalls := 0
	h.validationProbe = func(context.Context, credentials.AIProfile, codexapp.AccountRef) error {
		probeCalls++
		return errors.New("upstream unavailable")
	}
	sink := &recordingAIHealthSink{}
	h.SetSharedAIHealthIssueSink(sink)
	now := time.Date(2026, 7, 13, 13, 0, 0, 0, time.UTC)
	h.runSharedAIHealthCheck(context.Background(), now)
	if probeCalls != 1 || sink.calls != 1 || sink.healthy || sink.provider != credentials.AIProviderOpenAI || sink.model != "shared-model" {
		t.Fatalf("probeCalls=%d sink=%#v", probeCalls, sink)
	}
	if got := registry.AIHealthLastCheck(); !got.Equal(now) {
		t.Fatalf("last check=%s, want %s", got, now)
	}
	h.runSharedAIHealthCheck(context.Background(), now.Add(time.Hour))
	if probeCalls != 1 || sink.calls != 1 {
		t.Fatalf("hourly poll repeated failed turn: probes=%d sink calls=%d", probeCalls, sink.calls)
	}
}

func TestSharedAIHealthCheckRecordsRecovery(t *testing.T) {
	h, registry, _, _ := newResolverTestHandler(t)
	if err := registry.SetCredential(credentials.KeyGeminiKey, "shared-secret"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAIConfig(credentials.AIProviderGemini, "gemini-test"); err != nil {
		t.Fatal(err)
	}
	h.validationProbe = func(_ context.Context, profile credentials.AIProfile, account codexapp.AccountRef) error {
		if profile.Config.Provider != credentials.AIProviderGemini || profile.Config.Model != "gemini-test" || profile.APIKey != "shared-secret" {
			t.Fatalf("profile=%#v", profile)
		}
		if account != codexapp.SharedAccount() {
			t.Fatalf("account=%#v", account)
		}
		return nil
	}
	sink := &recordingAIHealthSink{}
	h.SetSharedAIHealthIssueSink(sink)
	h.runSharedAIHealthCheck(context.Background(), time.Date(2026, 7, 13, 14, 0, 0, 0, time.UTC))
	if sink.calls != 1 || !sink.healthy || sink.provider != credentials.AIProviderGemini || sink.model != "gemini-test" {
		t.Fatalf("sink=%#v", sink)
	}
}
