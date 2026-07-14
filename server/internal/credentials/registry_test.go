package credentials

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

func TestAIProviderMetadataIncludesAuthType(t *testing.T) {
	wantAPIKeys := map[string]string{
		AIProviderAnthropic: KeyAnthropicKey,
		AIProviderOpenAI:    KeyOpenAIKey,
		AIProviderGemini:    KeyGeminiKey,
	}
	for provider, credentialKey := range wantAPIKeys {
		option := aiProviderForTest(t, provider)
		if option.AuthType != AIAuthTypeAPIKey {
			t.Errorf("%s auth_type = %q, want %q", provider, option.AuthType, AIAuthTypeAPIKey)
		}
		if option.CredentialKey != credentialKey {
			t.Errorf("%s credential_key = %q, want %q", provider, option.CredentialKey, credentialKey)
		}
	}

	codex := aiProviderForTest(t, AIProviderCodex)
	if codex.Label != "OpenAI (OAuth)" {
		t.Fatalf("Codex label = %q", codex.Label)
	}
	if codex.AuthType != AIAuthTypeUserOAuth {
		t.Fatalf("Codex auth_type = %q, want %q", codex.AuthType, AIAuthTypeUserOAuth)
	}
	if codex.CredentialKey != "" {
		t.Fatalf("Codex credential_key = %q, want empty", codex.CredentialKey)
	}
	wantCodexModels := []string{"default", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"}
	if len(codex.Models) != len(wantCodexModels) {
		t.Fatalf("Codex models = %+v", codex.Models)
	}
	for i, want := range wantCodexModels {
		if codex.Models[i].ID != want {
			t.Fatalf("Codex model[%d] = %q, want %q", i, codex.Models[i].ID, want)
		}
	}
	if codex.Models[0].Label != "OpenAI recommended" {
		t.Fatalf("Codex default label = %q", codex.Models[0].Label)
	}
	encoded, err := json.Marshal(codex)
	if err != nil {
		t.Fatalf("marshal Codex metadata: %v", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(encoded, &metadata); err != nil {
		t.Fatalf("decode Codex metadata: %v", err)
	}
	if metadata["auth_type"] != AIAuthTypeUserOAuth {
		t.Fatalf("Codex JSON auth_type = %v", metadata["auth_type"])
	}
	if key, present := metadata["credential_key"]; !present || key != "" {
		t.Fatalf("Codex JSON credential_key = %v (present=%t), want an empty compatibility field: %s", key, present, encoded)
	}
}

func TestAIProviderDefaultsAndInference(t *testing.T) {
	if !IsValidAIProvider(AIProviderCodex) {
		t.Fatal("Codex is not recognized as a valid AI provider")
	}
	if got := DefaultAIModel(AIProviderCodex); got != "default" {
		t.Fatalf("DefaultAIModel(codex) = %q, want default", got)
	}

	tests := map[string]string{
		"":                  "",
		"claude-sonnet-4-6": AIProviderAnthropic,
		"gpt-5.4-mini":      AIProviderOpenAI,
		"o3":                AIProviderOpenAI,
		"gemini-2.5-flash":  AIProviderGemini,
		"default":           "",
		"unknown-model":     "",
	}
	for model, want := range tests {
		if got := inferAIProvider(model); got != want {
			t.Errorf("inferAIProvider(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestAIKeyCredentialKeyNeverFallsBack(t *testing.T) {
	if got := AIKeyCredentialKey(AIProviderAnthropic); got != KeyAnthropicKey {
		t.Fatalf("Anthropic credential key = %q", got)
	}
	if got := AIKeyCredentialKey(AIProviderCodex); got != "" {
		t.Fatalf("Codex credential key = %q, want empty", got)
	}
	if got := AIKeyCredentialKey("unknown-provider"); got != "" {
		t.Fatalf("unknown provider credential key = %q, want empty", got)
	}
}

func TestAIConfigInvalidProviderIsVisibleAndRejected(t *testing.T) {
	t.Setenv("CANTINARR_AI_PROVIDER", "")
	t.Setenv("CANTINARR_AI_MODEL", "")
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x68}, 32))
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(database, cipher)
	if err := registry.SetSetting(KeyAIProvider, "corrupt-provider"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetSetting(KeyAIModel, "corrupt-model"); err != nil {
		t.Fatal(err)
	}

	if got := registry.GetAIConfig(); got.Provider != "corrupt-provider" || got.Model != "corrupt-model" {
		t.Fatalf("invalid stored config was masked as %#v", got)
	}
	if registry.IsAIConfigured() {
		t.Fatal("invalid provider reported as configured")
	}
	if err := registry.SetAIConfig("still-invalid", "model"); err == nil {
		t.Fatal("SetAIConfig silently accepted an invalid provider")
	}
	if got := registry.GetAIConfig(); got.Provider != "corrupt-provider" || got.Model != "corrupt-model" {
		t.Fatalf("rejected write changed config to %#v", got)
	}
}

func TestIsAIConfiguredExcludesUserOAuth(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	registry := NewRegistry(database, cipher)
	if err := registry.SetCredential(KeyOpenAIKey, "openai-key"); err != nil {
		t.Fatalf("set OpenAI key: %v", err)
	}
	if err := registry.SetAIConfig(AIProviderCodex, ""); err != nil {
		t.Fatalf("select Codex: %v", err)
	}
	if registry.IsAIConfigured() {
		t.Fatal("shared AI reported API-key configured for an OAuth-backed provider")
	}

	if err := registry.SetAIConfig(AIProviderOpenAI, ""); err != nil {
		t.Fatalf("select OpenAI: %v", err)
	}
	if !registry.IsAIConfigured() {
		t.Fatal("shared AI did not report configured for selected API-key provider")
	}
}

func TestAIHealthCheckScheduleDefaultsOnAndSurvivesRestart(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x45}, 32))
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(database, cipher)
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	if !registry.AIHealthCheckEnabled() || !registry.AIHealthCheckDue(now) {
		t.Fatal("a never-checked install should default to a due daily health check")
	}
	if err := registry.RecordAIHealthCheck(now); err != nil {
		t.Fatal(err)
	}
	restarted := NewRegistry(database, cipher)
	if restarted.AIHealthLastCheck() != now {
		t.Fatalf("last check = %s, want %s", restarted.AIHealthLastCheck(), now)
	}
	if restarted.AIHealthCheckDue(now.Add(23 * time.Hour)) {
		t.Fatal("health check became due before 24 hours")
	}
	if !restarted.AIHealthCheckDue(now.Add(24 * time.Hour)) {
		t.Fatal("health check did not become due at 24 hours")
	}
	if err := restarted.SetSetting(KeyAIHealthCheckEnabled, "false"); err != nil {
		t.Fatal(err)
	}
	if restarted.AIHealthCheckEnabled() || restarted.AIHealthCheckDue(now.Add(48*time.Hour)) {
		t.Fatal("disabled health checks must never be due")
	}
}

func TestAISelectionConfiguredSkipsUntouchedInstall(t *testing.T) {
	t.Setenv("CANTINARR_AI_PROVIDER", "")
	t.Setenv("CANTINARR_AI_MODEL", "")
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x46}, 32))
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(database, cipher)
	if registry.AISelectionConfigured() {
		t.Fatal("untouched default selection reported configured")
	}
	if err := registry.SetAIConfig(AIProviderCodex, "gpt-5.6-luna"); err != nil {
		t.Fatal(err)
	}
	if !registry.AISelectionConfigured() {
		t.Fatal("explicit OAuth selection did not report configured")
	}
}

func aiProviderForTest(t *testing.T, id string) AIProviderOption {
	t.Helper()
	for _, provider := range AIProviders {
		if provider.ID == id {
			return provider
		}
	}
	t.Fatalf("provider %q not found", id)
	return AIProviderOption{}
}
