package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

func TestPersonalAISettingsResponseAndWriteOnlyCredentials(t *testing.T) {
	h, registry, database, userID := newResolverTestHandler(t)
	if _, err := database.Exec(`UPDATE users SET ai_shared_enabled = 1 WHERE id = ?`, userID); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetCredential(credentials.KeyOpenAIKey, "admin-shared-secret"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAIConfig(credentials.AIProviderOpenAI, "shared-model"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetUserAICredential(userID, credentials.AIProviderAnthropic, "user-personal-secret"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/ai/settings", strings.NewReader(`{"provider":"anthropic","model":"personal-model"}`))
	req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: userID, Role: auth.RoleUser}))
	rec := httptest.NewRecorder()
	h.UpdateAISettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	if body := rec.Body.String(); strings.Contains(body, "admin-shared-secret") || strings.Contains(body, "user-personal-secret") {
		t.Fatalf("settings leaked a credential: %s", body)
	}
	var response struct {
		Personal struct {
			Selected    bool                 `json:"selected"`
			Config      credentials.AIConfig `json:"config"`
			Credentials map[string]bool      `json:"credentials"`
		} `json:"personal"`
		Shared struct {
			Granted    bool                 `json:"granted"`
			Configured bool                 `json:"configured"`
			Config     credentials.AIConfig `json:"config"`
		} `json:"shared"`
		Effective struct {
			Source string `json:"source"`
		} `json:"effective"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if !response.Personal.Selected || response.Personal.Config.Provider != credentials.AIProviderAnthropic || !response.Personal.Credentials[credentials.AIProviderAnthropic] {
		t.Fatalf("personal = %#v", response.Personal)
	}
	if !response.Shared.Granted || !response.Shared.Configured || response.Shared.Config.Provider != credentials.AIProviderOpenAI {
		t.Fatalf("shared = %#v", response.Shared)
	}
	if response.Effective.Source != aiSourcePersonal {
		t.Fatalf("effective source = %q", response.Effective.Source)
	}
}

func TestAISettingsRemainsManageableWhenSharedCredentialIsCorrupt(t *testing.T) {
	h, registry, database, userID := newResolverTestHandler(t)
	if err := registry.SetUserAIConfig(userID, credentials.AIProviderAnthropic, "personal-model"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetUserAICredential(userID, credentials.AIProviderAnthropic, "personal-secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE users SET ai_shared_enabled = 1 WHERE id = ?`, userID); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAIConfig(credentials.AIProviderOpenAI, "shared-model"); err != nil {
		t.Fatal(err)
	}
	otherCipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x73}, 32))
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
	req := httptest.NewRequest(http.MethodGet, "/api/ai/settings", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: userID, Role: auth.RoleUser}))
	rec := httptest.NewRecorder()
	h.AISettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"source":"personal"`) || !strings.Contains(rec.Body.String(), `"reason":"storage_error"`) {
		t.Fatalf("unexpected settings body: %s", rec.Body.String())
	}
}
