package credentials

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

func newUserAIRegistry(t *testing.T) (*Registry, int64, int64) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x63}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, 2)
	for _, username := range []string{"one", "two"} {
		result, err := database.Exec(`INSERT INTO users (username, password_hash, role) VALUES (?, '', 'user')`, username)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := result.LastInsertId()
		ids = append(ids, id)
	}
	return NewRegistry(database, cipher), ids[0], ids[1]
}

func TestPersonalAIProfileEncryptedAndIsolated(t *testing.T) {
	registry, userOne, userTwo := newUserAIRegistry(t)
	if err := registry.SetUserAIConfig(userOne, AIProviderOpenAI, "gpt-personal"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetUserAICredential(userOne, AIProviderOpenAI, "personal-secret"); err != nil {
		t.Fatal(err)
	}
	profile, found, err := registry.LoadUserAIProfile(context.Background(), userOne)
	if err != nil || !found || !profile.CredentialPresent || profile.APIKey != "personal-secret" || profile.Config.Model != "gpt-personal" {
		t.Fatalf("profile = %#v, found=%t, err=%v", profile, found, err)
	}
	var stored string
	if err := registry.db.QueryRow(`SELECT credential_blob FROM user_ai_credentials WHERE user_id = ?`, userOne).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !secrets.IsEncrypted(stored) || stored == "personal-secret" {
		t.Fatalf("personal credential was not encrypted: %q", stored)
	}
	if _, found, err := registry.LoadUserAIProfile(context.Background(), userTwo); err != nil || found {
		t.Fatalf("other user profile found=%t err=%v", found, err)
	}
	if _, found, err := registry.UserAICredential(userTwo, AIProviderOpenAI); err != nil || found {
		t.Fatalf("other user credential found=%t err=%v", found, err)
	}
}

func TestPersonalAIProfileMissingOrCorruptCredentialRemainsExplicit(t *testing.T) {
	registry, userID, _ := newUserAIRegistry(t)
	if err := registry.SetUserAIConfig(userID, AIProviderAnthropic, "claude-personal"); err != nil {
		t.Fatal(err)
	}
	profile, found, err := registry.LoadUserAIProfile(context.Background(), userID)
	if err != nil || !found || profile.CredentialPresent {
		t.Fatalf("missing-key profile = %#v, found=%t, err=%v", profile, found, err)
	}
	if _, err := registry.db.Exec(`INSERT INTO user_ai_credentials (user_id, provider, credential_blob) VALUES (?, ?, 'plaintext')`, userID, AIProviderAnthropic); err != nil {
		t.Fatal(err)
	}
	profile, found, err = registry.LoadUserAIProfile(context.Background(), userID)
	if !found || !errors.Is(err, ErrAIStorage) || profile.Config.Provider != AIProviderAnthropic {
		t.Fatalf("corrupt profile = %#v, found=%t, err=%v", profile, found, err)
	}
}

func TestSharedAIProfileReadsGrantConfigAndMatchingKeyCoherently(t *testing.T) {
	registry, userID, _ := newUserAIRegistry(t)
	if _, err := registry.db.Exec(`UPDATE users SET ai_shared_enabled = 1 WHERE id = ?`, userID); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetCredential(KeyOpenAIKey, "shared-openai-secret"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetCredential(KeyAnthropicKey, "other-secret"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAIConfig(AIProviderOpenAI, "gpt-shared"); err != nil {
		t.Fatal(err)
	}
	profile, granted, err := registry.LoadSharedAIProfileForUser(context.Background(), userID)
	if err != nil || !granted || !profile.CredentialPresent || profile.APIKey != "shared-openai-secret" || profile.Config.Provider != AIProviderOpenAI {
		t.Fatalf("shared profile = %#v, granted=%t, err=%v", profile, granted, err)
	}
}

func TestSharedAIProfileRejectsInvalidStoredOrEnvironmentProvider(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stored bool
	}{
		{name: "stored", stored: true},
		{name: "environment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry, userID, _ := newUserAIRegistry(t)
			if _, err := registry.db.Exec(`UPDATE users SET ai_shared_enabled = 1 WHERE id = ?`, userID); err != nil {
				t.Fatal(err)
			}
			if err := registry.SetCredential(KeyAnthropicKey, "must-not-be-used"); err != nil {
				t.Fatal(err)
			}
			if tc.stored {
				if _, err := registry.db.Exec(`INSERT INTO settings (key, value) VALUES (?, 'typo-provider')`, KeyAIProvider); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Setenv("CANTINARR_AI_PROVIDER", "typo-provider")
			}
			profile, granted, err := registry.LoadSharedAIProfileForUser(context.Background(), userID)
			if !granted || !errors.Is(err, ErrAIStorage) || profile.Config.Provider != "typo-provider" || profile.CredentialPresent || profile.APIKey != "" {
				t.Fatalf("profile=%#v granted=%t err=%v", profile, granted, err)
			}
		})
	}
}
