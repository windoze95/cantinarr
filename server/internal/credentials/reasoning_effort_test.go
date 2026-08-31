package credentials

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestAIProviderMetadataAdvertisesReasoningEffortSupportOnlyForOpenAI(t *testing.T) {
	for _, option := range AIProviders {
		want := option.ID == AIProviderOpenAI || option.ID == AIProviderLocalOpenAI
		if option.SupportsReasoningEffort != want {
			t.Errorf("%s supports_reasoning_effort = %t, want %t", option.ID, option.SupportsReasoningEffort, want)
		}
		encoded, err := json.Marshal(option)
		if err != nil {
			t.Fatalf("marshal %s metadata: %v", option.ID, err)
		}
		var metadata map[string]any
		if err := json.Unmarshal(encoded, &metadata); err != nil {
			t.Fatalf("decode %s metadata: %v", option.ID, err)
		}
		flag, present := metadata["supports_reasoning_effort"]
		if want && (!present || flag != true) {
			t.Errorf("%s JSON supports_reasoning_effort = %v (present=%t), want true", option.ID, flag, present)
		}
		// omitempty keeps every other provider's wire shape byte-identical.
		if !want && present {
			t.Errorf("%s JSON unexpectedly carries supports_reasoning_effort: %s", option.ID, encoded)
		}
	}
}

func TestOpenAIReasoningEffortSaveValidatesPersistsAndEchoes(t *testing.T) {
	handler, registry := newCredentialHandlerTest(t)
	var profiles []AIProfile
	handler.SetSharedAIValidator(func(_ context.Context, profile AIProfile) error {
		profiles = append(profiles, profile)
		return nil
	}, nil)

	// Mixed case and padding normalize to the canonical lowercase value.
	recorder := updateCredentialSettings(t, handler, `{"openai_key":"local-secret","ai_provider":"openai","ai_model":"local-model","openai_reasoning_effort":" Medium "}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(profiles) != 1 || profiles[0].ReasoningEffort != "medium" {
		t.Fatalf("validated profiles = %#v", profiles)
	}
	var raw string
	if err := registry.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, KeyOpenAIReasoningEffort).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != "medium" {
		t.Fatalf("stored reasoning effort = %q", raw)
	}
	if got := getCredentialsAIStatus(t, handler)["openai_reasoning_effort"]; got != "medium" {
		t.Fatalf("echoed reasoning effort = %v", got)
	}
}

func TestOpenAIReasoningEffortChangeAloneRerunsValidationTurn(t *testing.T) {
	handler, registry := newCredentialHandlerTest(t)
	if err := registry.SetCredential(KeyOpenAIKey, "stored-secret"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAIConfig(AIProviderOpenAI, "stored-model"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetSetting(KeyOpenAIReasoningEffort, "low"); err != nil {
		t.Fatal(err)
	}
	var profiles []AIProfile
	handler.SetSharedAIValidator(func(_ context.Context, profile AIProfile) error {
		profiles = append(profiles, profile)
		return nil
	}, nil)

	recorder := updateCredentialSettings(t, handler, `{"openai_reasoning_effort":"none"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(profiles) != 1 || profiles[0].ReasoningEffort != "none" || profiles[0].APIKey != "stored-secret" || profiles[0].Config.Model != "stored-model" {
		t.Fatalf("validated profiles = %#v", profiles)
	}

	// Clearing is a change too: it must re-prove auto behavior and remove
	// the row.
	profiles = nil
	recorder = updateCredentialSettings(t, handler, `{"openai_reasoning_effort":""}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("clear status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(profiles) != 1 || profiles[0].ReasoningEffort != "" || profiles[0].APIKey != "stored-secret" {
		t.Fatalf("clear validated profiles = %#v", profiles)
	}
	var count int
	if err := registry.db.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = ?`, KeyOpenAIReasoningEffort).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("cleared reasoning effort still stored (%d rows)", count)
	}
}

func TestOpenAIReasoningEffortInvalidRejectedNothingSaved(t *testing.T) {
	handler, registry := newCredentialHandlerTest(t)
	if err := registry.SetCredential(KeyOpenAIKey, "stored-secret"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAIConfig(AIProviderOpenAI, "stored-model"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	handler.SetSharedAIValidator(func(context.Context, AIProfile) error {
		calls++
		return nil
	}, nil)

	for _, invalid := range []string{"max", "ultra", "0.5", "yes"} {
		body, err := json.Marshal(map[string]string{KeyOpenAIReasoningEffort: invalid})
		if err != nil {
			t.Fatal(err)
		}
		recorder := updateCredentialSettings(t, handler, string(body))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("value %q: status=%d body=%s", invalid, recorder.Code, recorder.Body.String())
		}
	}
	if calls != 0 {
		t.Fatalf("invalid efforts triggered %d validation turns", calls)
	}
	var count int
	if err := registry.db.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = ?`, KeyOpenAIReasoningEffort).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("invalid effort was stored (%d rows)", count)
	}
}

func TestOpenAIKeyRotationValidatesAgainstStoredReasoningEffort(t *testing.T) {
	handler, registry := newCredentialHandlerTest(t)
	if err := registry.SetAIConfig(AIProviderOpenAI, "stored-model"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetSetting(KeyOpenAIReasoningEffort, "low"); err != nil {
		t.Fatal(err)
	}
	var profiles []AIProfile
	handler.SetSharedAIValidator(func(_ context.Context, profile AIProfile) error {
		profiles = append(profiles, profile)
		return nil
	}, nil)

	// A rotation body carries no effort field; the candidate must still be
	// proven with the pinned effort, not auto.
	recorder := updateCredentialSettings(t, handler, `{"openai_key":"rotated-secret"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(profiles) != 1 || profiles[0].ReasoningEffort != "low" || profiles[0].APIKey != "rotated-secret" {
		t.Fatalf("validated profiles = %#v", profiles)
	}
	if got := registry.GetSetting(KeyOpenAIReasoningEffort); got != "low" {
		t.Fatalf("stored reasoning effort changed to %q", got)
	}
}

func TestNonOpenAICandidatesNeverCarryReasoningEffort(t *testing.T) {
	handler, registry := newCredentialHandlerTest(t)
	if err := registry.SetSetting(KeyOpenAIReasoningEffort, "low"); err != nil {
		t.Fatal(err)
	}
	var profiles []AIProfile
	handler.SetSharedAIValidator(func(_ context.Context, profile AIProfile) error {
		profiles = append(profiles, profile)
		return nil
	}, nil)

	recorder := updateCredentialSettings(t, handler, `{"gemini_key":"gemini-secret","ai_provider":"gemini","ai_model":"gemini-model"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(profiles) != 1 || profiles[0].Config.Provider != AIProviderGemini || profiles[0].ReasoningEffort != "" {
		t.Fatalf("validated profiles = %#v", profiles)
	}
	if got := registry.GetSetting(KeyOpenAIReasoningEffort); got != "low" {
		t.Fatalf("stored reasoning effort disturbed: %q", got)
	}
}

func TestSharedAIProfileCarriesReasoningEffortOnlyForOpenAI(t *testing.T) {
	registry, userID, _ := newUserAIRegistry(t)
	if _, err := registry.db.Exec(`UPDATE users SET ai_shared_enabled = 1 WHERE id = ?`, userID); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetSetting(KeyOpenAIReasoningEffort, "low"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetCredential(KeyOpenAIKey, "shared-openai-secret"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAIConfig(AIProviderOpenAI, "local-model"); err != nil {
		t.Fatal(err)
	}
	profile, granted, err := registry.LoadSharedAIProfileForUser(context.Background(), userID)
	if err != nil || !granted || profile.ReasoningEffort != "low" {
		t.Fatalf("shared openai profile = %#v, granted=%t, err=%v", profile, granted, err)
	}
	shared, err := registry.LoadSharedAIProfile(context.Background())
	if err != nil || shared.ReasoningEffort != "low" {
		t.Fatalf("shared profile = %#v, err=%v", shared, err)
	}

	// The pin belongs to the openai provider: any other selection must leave
	// it behind even while the row stays stored.
	if err := registry.SetCredential(KeyGeminiKey, "gemini-secret"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAIConfig(AIProviderGemini, "gemini-model"); err != nil {
		t.Fatal(err)
	}
	profile, _, err = registry.LoadSharedAIProfileForUser(context.Background(), userID)
	if err != nil || profile.ReasoningEffort != "" {
		t.Fatalf("gemini profile = %#v, err=%v", profile, err)
	}

	// Personal profiles never carry the shared pin: a user's own OpenAI key
	// keeps the provider's default behavior.
	if err := registry.SetUserAIConfig(userID, AIProviderOpenAI, "gpt-personal"); err != nil {
		t.Fatal(err)
	}
	personal, found, err := registry.LoadUserAIProfile(context.Background(), userID)
	if err != nil || !found || personal.ReasoningEffort != "" {
		t.Fatalf("personal profile = %#v, found=%t, err=%v", personal, found, err)
	}
}
