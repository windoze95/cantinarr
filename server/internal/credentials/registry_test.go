package credentials

import (
	"bytes"
	"encoding/json"
	"testing"

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
	if codex.Label != "ChatGPT (Codex)" {
		t.Fatalf("Codex label = %q", codex.Label)
	}
	if codex.AuthType != AIAuthTypeUserOAuth {
		t.Fatalf("Codex auth_type = %q, want %q", codex.AuthType, AIAuthTypeUserOAuth)
	}
	if codex.CredentialKey != "" {
		t.Fatalf("Codex credential_key = %q, want empty", codex.CredentialKey)
	}
	if len(codex.Models) != 1 || codex.Models[0].ID != "default" || codex.Models[0].Label != "Codex default" {
		t.Fatalf("Codex models = %+v", codex.Models)
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
