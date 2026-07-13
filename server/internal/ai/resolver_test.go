package ai

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

func newResolverTestHandler(t *testing.T) (*Handler, *credentials.Registry, *sql.DB, int64) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x27}, 32))
	if err != nil {
		t.Fatal(err)
	}
	result, err := database.Exec(`INSERT INTO users (username, password_hash, role) VALUES ('resolver-user', '', 'user')`)
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := result.LastInsertId()
	registry := credentials.NewRegistry(database, cipher)
	return &Handler{creds: registry}, registry, database, userID
}

func TestResolveAIPersonalOverridesShared(t *testing.T) {
	h, registry, database, userID := newResolverTestHandler(t)
	if _, err := database.Exec(`UPDATE users SET ai_shared_enabled = 1 WHERE id = ?`, userID); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetCredential(credentials.KeyOpenAIKey, "shared-secret"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAIConfig(credentials.AIProviderOpenAI, "shared-model"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetUserAIConfig(userID, credentials.AIProviderAnthropic, "personal-model"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetUserAICredential(userID, credentials.AIProviderAnthropic, "personal-secret"); err != nil {
		t.Fatal(err)
	}
	resolved := h.resolveAI(context.Background(), userID)
	if !resolved.Available || resolved.Source != aiSourcePersonal || resolved.Provider != credentials.AIProviderAnthropic || resolved.APIKey != "personal-secret" {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestResolveAIPersonalProviderIsIndependentOfGlobalProviderAndGrant(t *testing.T) {
	h, registry, _, userID := newResolverTestHandler(t)
	// The admin's global profile uses Codex and this user has no included-access
	// grant. Their own API-key provider is still available and need not match it.
	if err := registry.SetAIConfig(credentials.AIProviderCodex, "default"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetUserAIConfig(userID, credentials.AIProviderAnthropic, "personal-model"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetUserAICredential(userID, credentials.AIProviderAnthropic, "personal-secret"); err != nil {
		t.Fatal(err)
	}

	resolved := h.resolveAI(context.Background(), userID)
	if !resolved.Available || resolved.Source != aiSourcePersonal || resolved.Provider != credentials.AIProviderAnthropic || resolved.Model != "personal-model" || resolved.APIKey != "personal-secret" {
		t.Fatalf("resolved independent personal provider = %#v", resolved)
	}
}

func TestResolveAIExplicitMissingPersonalNeverFallsThrough(t *testing.T) {
	h, registry, database, userID := newResolverTestHandler(t)
	if _, err := database.Exec(`UPDATE users SET ai_shared_enabled = 1 WHERE id = ?`, userID); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetCredential(credentials.KeyOpenAIKey, "shared-secret"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAIConfig(credentials.AIProviderOpenAI, "shared-model"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetUserAIConfig(userID, credentials.AIProviderAnthropic, "personal-model"); err != nil {
		t.Fatal(err)
	}
	resolved := h.resolveAI(context.Background(), userID)
	if resolved.Available || resolved.Source != aiSourcePersonal || resolved.Provider != credentials.AIProviderAnthropic || resolved.Reason != "personal_credential_missing" {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestResolveAIUsesSharedOnlyWithoutPersonalSelectionAndGrant(t *testing.T) {
	h, registry, database, userID := newResolverTestHandler(t)
	if err := registry.SetCredential(credentials.KeyGeminiKey, "shared-gemini"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAIConfig(credentials.AIProviderGemini, "shared-model"); err != nil {
		t.Fatal(err)
	}
	resolved := h.resolveAI(context.Background(), userID)
	if resolved.Available || resolved.Source != aiSourceNone || resolved.Reason != "shared_access_disabled" {
		t.Fatalf("ungranted resolved = %#v", resolved)
	}
	if _, err := database.Exec(`UPDATE users SET ai_shared_enabled = 1 WHERE id = ?`, userID); err != nil {
		t.Fatal(err)
	}
	resolved = h.resolveAI(context.Background(), userID)
	if !resolved.Available || resolved.Source != aiSourceShared || resolved.Provider != credentials.AIProviderGemini || resolved.APIKey != "shared-gemini" {
		t.Fatalf("granted resolved = %#v", resolved)
	}
}

func TestResolveAICorruptCredentialPreservesSelectedSource(t *testing.T) {
	h, registry, database, userID := newResolverTestHandler(t)
	if err := registry.SetUserAIConfig(userID, credentials.AIProviderOpenAI, "personal-model"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO user_ai_credentials (user_id, provider, credential_blob) VALUES (?, 'openai', 'plaintext')`, userID); err != nil {
		t.Fatal(err)
	}
	resolved := h.resolveAI(context.Background(), userID)
	if resolved.Available || resolved.Source != aiSourcePersonal || resolved.Provider != credentials.AIProviderOpenAI || resolved.Reason != "storage_error" {
		t.Fatalf("personal corrupt resolved = %#v", resolved)
	}
	if err := registry.DeleteUserAIConfig(userID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE users SET ai_shared_enabled = 1 WHERE id = ?`, userID); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAIConfig(credentials.AIProviderOpenAI, "shared-model"); err != nil {
		t.Fatal(err)
	}
	otherCipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x72}, 32))
	if err != nil {
		t.Fatal(err)
	}
	wrongKeyCiphertext, err := otherCipher.Encrypt("shared-secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`, credentials.KeyOpenAIKey, wrongKeyCiphertext); err != nil {
		t.Fatal(err)
	}
	resolved = h.resolveAI(context.Background(), userID)
	if resolved.Available || resolved.Source != aiSourceShared || resolved.Provider != credentials.AIProviderOpenAI || resolved.Reason != "storage_error" {
		t.Fatalf("shared corrupt resolved = %#v", resolved)
	}
}
