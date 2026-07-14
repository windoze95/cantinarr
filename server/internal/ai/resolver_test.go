package ai

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/codexapp"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/mcp"
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
	handler := &Handler{
		creds: registry,
		authorizePermission: func(context.Context, int64, string, auth.Permission) error {
			return nil
		},
	}
	handler.validationProbe = func(context.Context, credentials.AIProfile, codexapp.AccountRef) error { return nil }
	return handler, registry, database, userID
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

func TestResolveAIProviderScopeReadinessAndGrantMatrix(t *testing.T) {
	providers := []string{
		credentials.AIProviderAnthropic,
		credentials.AIProviderOpenAI,
		credentials.AIProviderGemini,
		credentials.AIProviderCodex,
	}

	for _, provider := range providers {
		provider := provider
		for _, scope := range []string{aiSourcePersonal, aiSourceShared} {
			scope := scope
			for _, ready := range []bool{false, true} {
				ready := ready
				t.Run(provider+"/"+scope+"/ready="+boolName(ready), func(t *testing.T) {
					h, registry, database, userID := newResolverTestHandler(t)
					cipher := enableResolverCodexManager(t, h, registry, database)
					model := provider + "-model"

					if scope == aiSourcePersonal {
						// A usable included provider makes this an explicit proof that
						// a broken personal choice fails closed instead of spending it.
						grantResolverSharedAccess(t, database, userID, true)
						configureResolverProfile(t, registry, database, cipher, userID, aiSourceShared, credentials.AIProviderOpenAI, true)
						configureResolverProfile(t, registry, database, cipher, userID, scope, provider, ready)
					} else {
						grantResolverSharedAccess(t, database, userID, true)
						configureResolverProfile(t, registry, database, cipher, userID, scope, provider, ready)
					}

					resolved := h.resolveAI(context.Background(), userID)
					wantReason := ""
					if !ready {
						if provider == credentials.AIProviderCodex {
							wantReason = scope + "_codex_disconnected"
						} else {
							wantReason = scope + "_credential_missing"
						}
					}
					if resolved.Available != ready || resolved.Source != scope || resolved.Provider != provider || resolved.Model != model || resolved.Reason != wantReason {
						t.Fatalf("resolved = %#v, want available=%t source=%q provider=%q model=%q reason=%q", resolved, ready, scope, provider, model, wantReason)
					}
					if provider != credentials.AIProviderCodex {
						wantKey := ""
						if ready {
							wantKey = scope + "-" + provider + "-secret"
						}
						if resolved.APIKey != wantKey {
							t.Fatalf("API key = %q, want %q", resolved.APIKey, wantKey)
						}
					}
				})
			}
		}

		t.Run(provider+"/shared/grant=false", func(t *testing.T) {
			h, registry, database, userID := newResolverTestHandler(t)
			cipher := enableResolverCodexManager(t, h, registry, database)
			configureResolverProfile(t, registry, database, cipher, userID, aiSourceShared, provider, true)
			resolved := h.resolveAI(context.Background(), userID)
			if resolved.Available || resolved.Source != aiSourceNone || resolved.Reason != "shared_access_disabled" || resolved.Provider != "" || resolved.APIKey != "" {
				t.Fatalf("ungranted shared profile escaped its grant: %#v", resolved)
			}
		})

		t.Run(provider+"/personal/grant=false", func(t *testing.T) {
			h, registry, database, userID := newResolverTestHandler(t)
			cipher := enableResolverCodexManager(t, h, registry, database)
			configureResolverProfile(t, registry, database, cipher, userID, aiSourcePersonal, provider, true)
			resolved := h.resolveAI(context.Background(), userID)
			if !resolved.Available || resolved.Source != aiSourcePersonal || resolved.Provider != provider {
				t.Fatalf("personal profile incorrectly depended on shared grant: %#v", resolved)
			}
		})
	}

	for _, scope := range []string{aiSourcePersonal, aiSourceShared} {
		scope := scope
		t.Run("codex/"+scope+"/runtime_unavailable", func(t *testing.T) {
			h, registry, database, userID := newResolverTestHandler(t)
			cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x27}, 32))
			if err != nil {
				t.Fatal(err)
			}
			grantResolverSharedAccess(t, database, userID, true)
			configureResolverProfile(t, registry, database, cipher, userID, scope, credentials.AIProviderCodex, true)
			resolved := h.resolveAI(context.Background(), userID)
			if resolved.Available || resolved.Source != scope || resolved.Provider != credentials.AIProviderCodex || resolved.Reason != "codex_unavailable" {
				t.Fatalf("unavailable Codex runtime resolved = %#v", resolved)
			}
		})
	}
}

func boolName(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func enableResolverCodexManager(t *testing.T, h *Handler, registry *credentials.Registry, database *sql.DB) *secrets.Cipher {
	t.Helper()
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x27}, 32))
	if err != nil {
		t.Fatal(err)
	}
	runtimeDir := t.TempDir()
	if err := os.Chmod(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	h.codex = codexapp.NewManager(database, cipher, mcp.NewToolServer(registry, nil, nil, nil), codexapp.Options{
		Binary:                   os.Args[0],
		RuntimeDir:               runtimeDir,
		AllowDiskRuntimeForTests: true,
	})
	if !h.codex.Available() {
		t.Fatal("test Codex manager is unavailable")
	}
	return cipher
}

func grantResolverSharedAccess(t *testing.T, database *sql.DB, userID int64, enabled bool) {
	t.Helper()
	if _, err := database.Exec(`UPDATE users SET ai_shared_enabled = ? WHERE id = ?`, enabled, userID); err != nil {
		t.Fatal(err)
	}
}

func configureResolverProfile(
	t *testing.T,
	registry *credentials.Registry,
	database *sql.DB,
	cipher *secrets.Cipher,
	userID int64,
	scope string,
	provider string,
	ready bool,
) {
	t.Helper()
	model := provider + "-model"
	if scope == aiSourcePersonal {
		if err := registry.SetUserAIConfig(userID, provider, model); err != nil {
			t.Fatal(err)
		}
	} else if err := registry.SetAIConfig(provider, model); err != nil {
		t.Fatal(err)
	}
	if !ready {
		return
	}
	if provider != credentials.AIProviderCodex {
		secret := scope + "-" + provider + "-secret"
		if scope == aiSourcePersonal {
			if err := registry.SetUserAICredential(userID, provider, secret); err != nil {
				t.Fatal(err)
			}
		} else if err := registry.SetCredential(credentials.AIKeyCredentialKey(provider), secret); err != nil {
			t.Fatal(err)
		}
		return
	}
	authBlob, err := cipher.Encrypt(`{"tokens":{"access_token":"test-access","refresh_token":"test-refresh"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if scope == aiSourcePersonal {
		_, err = database.Exec(`INSERT INTO user_codex_accounts (user_id, auth_blob) VALUES (?, ?)`, userID, authBlob)
	} else {
		_, err = database.Exec(`INSERT INTO shared_codex_account (singleton, auth_blob) VALUES (1, ?)`, authBlob)
	}
	if err != nil {
		t.Fatal(err)
	}
}
