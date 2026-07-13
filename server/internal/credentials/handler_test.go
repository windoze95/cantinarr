package credentials

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

func newCredentialHandlerTest(t *testing.T) (*Handler, *Registry) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x35}, 32))
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(database, cipher)
	return NewHandler(registry), registry
}

func updateCredentialSettings(t *testing.T, handler *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.Update(recorder, httptest.NewRequest(http.MethodPut, "/api/admin/credentials", strings.NewReader(body)))
	return recorder
}

func TestAISettingsValidationFailureLeavesSharedProfileUnchanged(t *testing.T) {
	handler, registry := newCredentialHandlerTest(t)
	if err := registry.SetCredential(KeyOpenAIKey, "old-secret"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAIConfig(AIProviderOpenAI, "old-model"); err != nil {
		t.Fatal(err)
	}
	var got AIProfile
	handler.SetSharedAIValidator(func(_ context.Context, profile AIProfile) error {
		got = profile
		return errors.New("upstream rejected candidate")
	}, nil)

	recorder := updateCredentialSettings(t, handler, `{"openai_key":"new-secret","ai_provider":"openai","ai_model":"new-model"}`)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if got.Config.Provider != AIProviderOpenAI || got.Config.Model != "new-model" || got.APIKey != "new-secret" {
		t.Fatalf("validated profile = %#v", got)
	}
	if key := registry.GetCredential(KeyOpenAIKey); key != "old-secret" {
		t.Fatalf("stored key = %q, want old key", key)
	}
	if config := registry.GetAIConfig(); config.Provider != AIProviderOpenAI || config.Model != "old-model" {
		t.Fatalf("stored config = %#v", config)
	}
}

func TestAISettingsValidationCommitsExactCandidateAtomically(t *testing.T) {
	handler, registry := newCredentialHandlerTest(t)
	var (
		profiles  []AIProfile
		validated AIConfig
	)
	handler.SetSharedAIValidator(func(_ context.Context, profile AIProfile) error {
		profiles = append(profiles, profile)
		return nil
	}, func(config AIConfig) { validated = config })

	recorder := updateCredentialSettings(t, handler, `{"openai_key":"candidate-secret","ai_provider":"openai","ai_model":"gpt-candidate"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(profiles) != 1 || profiles[0].Config.Provider != AIProviderOpenAI || profiles[0].Config.Model != "gpt-candidate" || profiles[0].APIKey != "candidate-secret" {
		t.Fatalf("validated profiles = %#v", profiles)
	}
	if key := registry.GetCredential(KeyOpenAIKey); key != "candidate-secret" {
		t.Fatalf("stored key = %q", key)
	}
	if config := registry.GetAIConfig(); config.Provider != AIProviderOpenAI || config.Model != "gpt-candidate" {
		t.Fatalf("stored config = %#v", config)
	}
	if validated.Provider != AIProviderOpenAI || validated.Model != "gpt-candidate" {
		t.Fatalf("post-commit callback config = %#v", validated)
	}
	var raw string
	if err := registry.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, KeyOpenAIKey).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw == "candidate-secret" || !secrets.IsEncrypted(raw) {
		t.Fatalf("credential was not encrypted at rest: %q", raw)
	}
}

func TestAIHealthToggleDisablesWithoutTurnAndValidatesWhenReenabled(t *testing.T) {
	handler, registry := newCredentialHandlerTest(t)
	if err := registry.SetCredential(KeyOpenAIKey, "shared-secret"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAIConfig(AIProviderOpenAI, "shared-model"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	handler.SetSharedAIValidator(func(_ context.Context, profile AIProfile) error {
		calls++
		if profile.Config.Model != "shared-model" || profile.APIKey != "shared-secret" {
			t.Fatalf("validated profile = %#v", profile)
		}
		return nil
	}, nil)

	recorder := updateCredentialSettings(t, handler, `{"ai_health_check_enabled":"false"}`)
	if recorder.Code != http.StatusOK || registry.AIHealthCheckEnabled() {
		t.Fatalf("disable status=%d enabled=%t body=%s", recorder.Code, registry.AIHealthCheckEnabled(), recorder.Body.String())
	}
	if calls != 0 {
		t.Fatalf("disable made %d provider calls, want 0", calls)
	}

	recorder = updateCredentialSettings(t, handler, `{"ai_health_check_enabled":"true"}`)
	if recorder.Code != http.StatusOK || !registry.AIHealthCheckEnabled() {
		t.Fatalf("enable status=%d enabled=%t body=%s", recorder.Code, registry.AIHealthCheckEnabled(), recorder.Body.String())
	}
	if calls != 1 {
		t.Fatalf("enable made %d provider calls, want 1", calls)
	}
}

func TestCredentialStatusIncludesDurableAIHealthMetadata(t *testing.T) {
	handler, registry := newCredentialHandlerTest(t)
	checked := time.Date(2026, 7, 12, 19, 30, 0, 0, time.UTC)
	if err := registry.RecordAIHealthCheck(checked); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.Get(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/credentials", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		AI struct {
			Health struct {
				Enabled       bool   `json:"enabled"`
				IntervalHours int    `json:"interval_hours"`
				LastCheckedAt string `json:"last_checked_at"`
			} `json:"health_check"`
		} `json:"ai"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if !response.AI.Health.Enabled || response.AI.Health.IntervalHours != 24 || response.AI.Health.LastCheckedAt != checked.Format(time.RFC3339) {
		t.Fatalf("health metadata = %#v", response.AI.Health)
	}
}
