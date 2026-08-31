package credentials

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestLocalOpenAIMetadataIsSharedOnlyAndKeyOptional(t *testing.T) {
	for _, option := range AIProviders {
		want := option.ID == AIProviderLocalOpenAI
		if option.SharedOnly != want {
			t.Errorf("%s shared_only = %t, want %t", option.ID, option.SharedOnly, want)
		}
		encoded, err := json.Marshal(option)
		if err != nil {
			t.Fatalf("marshal %s metadata: %v", option.ID, err)
		}
		var metadata map[string]any
		if err := json.Unmarshal(encoded, &metadata); err != nil {
			t.Fatalf("decode %s metadata: %v", option.ID, err)
		}
		flag, present := metadata["shared_only"]
		if want && (!present || flag != true) {
			t.Errorf("%s JSON shared_only = %v (present=%t), want true", option.ID, flag, present)
		}
		if !want && present {
			t.Errorf("%s JSON unexpectedly carries shared_only: %s", option.ID, encoded)
		}
		if got := AIProviderKeyOptional(option.ID); got != want {
			t.Errorf("AIProviderKeyOptional(%s) = %t, want %t", option.ID, got, want)
		}
	}
	for _, option := range PersonalAIProviders() {
		if option.SharedOnly {
			t.Fatalf("PersonalAIProviders leaked shared-only provider %s", option.ID)
		}
	}
	// No catalog means no implicit default: the model must be explicit.
	if got := DefaultAIModel(AIProviderLocalOpenAI); got != "" {
		t.Fatalf("DefaultAIModel(local_openai) = %q, want empty", got)
	}
}

func TestLocalOpenAISelectionRequiresBaseURLAndModel(t *testing.T) {
	handler, registry := newCredentialHandlerTest(t)
	calls := 0
	handler.SetSharedAIValidator(func(context.Context, AIProfile) error {
		calls++
		return nil
	}, nil)

	// No base URL stored and none supplied: refused before any turn runs.
	recorder := updateCredentialSettings(t, handler, `{"ai_provider":"local_openai","ai_model":"qwen3.6:35b-a3b"}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("no base URL: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	// Base URL supplied but no model: the local provider has no catalog to
	// fall back on.
	recorder = updateCredentialSettings(t, handler, `{"ai_provider":"local_openai","local_openai_base_url":"http://llm-host:11434/v1"}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("no model: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if calls != 0 {
		t.Fatalf("invalid selections triggered %d validation turns", calls)
	}
	if got := registry.GetSetting(KeyLocalOpenAIBaseURL); got != "" {
		t.Fatalf("refused save stored base URL %q", got)
	}
}

func TestLocalOpenAIBaseURLInvalidRejectedNothingSaved(t *testing.T) {
	handler, registry := newCredentialHandlerTest(t)
	calls := 0
	handler.SetSharedAIValidator(func(context.Context, AIProfile) error {
		calls++
		return nil
	}, nil)

	for _, invalid := range []string{
		"://bad",
		"ftp://host/v1",
		"host-without-scheme/v1",
		"http://",
		"https://" + strings.Repeat("x", maxAIBaseURLLength) + "/v1",
	} {
		body, err := json.Marshal(map[string]string{KeyLocalOpenAIBaseURL: invalid})
		if err != nil {
			t.Fatal(err)
		}
		recorder := updateCredentialSettings(t, handler, string(body))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("value %q: status=%d body=%s", invalid, recorder.Code, recorder.Body.String())
		}
	}
	if calls != 0 {
		t.Fatalf("invalid base URLs triggered %d validation turns", calls)
	}
	if got := registry.GetSetting(KeyLocalOpenAIBaseURL); got != "" {
		t.Fatalf("invalid base URL was stored: %q", got)
	}
}

func TestLocalOpenAISelectionValidatesWithoutKeyAndPersists(t *testing.T) {
	handler, registry := newCredentialHandlerTest(t)
	var profiles []AIProfile
	handler.SetSharedAIValidator(func(_ context.Context, profile AIProfile) error {
		profiles = append(profiles, profile)
		return nil
	}, nil)

	recorder := updateCredentialSettings(t, handler, `{"ai_provider":"local_openai","ai_model":"qwen3.6:35b-a3b","local_openai_base_url":"http://llm-host:11434/v1","local_openai_reasoning_effort":"none"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(profiles) != 1 {
		t.Fatalf("validated %d profiles, want 1", len(profiles))
	}
	profile := profiles[0]
	if profile.Config.Provider != AIProviderLocalOpenAI || profile.Config.Model != "qwen3.6:35b-a3b" ||
		profile.BaseURL != "http://llm-host:11434/v1" || profile.ReasoningEffort != "none" {
		t.Fatalf("validated profile = %#v", profile)
	}
	// Key optional: the candidate is credential-complete with no stored key.
	if profile.APIKey != "" || !profile.CredentialPresent {
		t.Fatalf("keyless local profile = APIKey %q CredentialPresent %t", profile.APIKey, profile.CredentialPresent)
	}

	if got := registry.GetSetting(KeyLocalOpenAIBaseURL); got != "http://llm-host:11434/v1" {
		t.Fatalf("stored base URL = %q", got)
	}
	if got := registry.GetSetting(KeyLocalOpenAIReasoningEffort); got != "none" {
		t.Fatalf("stored effort = %q", got)
	}
	status := getCredentialsAIStatus(t, handler)
	if got := status["local_openai_base_url"]; got != "http://llm-host:11434/v1" {
		t.Fatalf("echoed base URL = %v", got)
	}
	if got := status["local_openai_reasoning_effort"]; got != "none" {
		t.Fatalf("echoed effort = %v", got)
	}
}

func TestLocalOpenAIEndpointChangeAloneRerunsValidationTurn(t *testing.T) {
	handler, registry := newCredentialHandlerTest(t)
	if err := registry.SetAIConfig(AIProviderLocalOpenAI, "qwen3.6:35b-a3b"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetSetting(KeyLocalOpenAIBaseURL, "http://old-host:11434/v1"); err != nil {
		t.Fatal(err)
	}
	var profiles []AIProfile
	handler.SetSharedAIValidator(func(_ context.Context, profile AIProfile) error {
		profiles = append(profiles, profile)
		return nil
	}, nil)

	recorder := updateCredentialSettings(t, handler, `{"local_openai_base_url":"http://new-host:11434/v1"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(profiles) != 1 || profiles[0].BaseURL != "http://new-host:11434/v1" || profiles[0].Config.Model != "qwen3.6:35b-a3b" {
		t.Fatalf("validated profiles = %#v", profiles)
	}

	// The scoped keys never bleed into the hosted openai effort pin.
	if got := registry.GetSetting(KeyOpenAIReasoningEffort); got != "" {
		t.Fatalf("hosted openai effort contaminated: %q", got)
	}
}

func TestLocalOpenAIKeyStoredUnvalidatedWhenNotSelected(t *testing.T) {
	handler, registry := newCredentialHandlerTest(t)
	var providers []string
	handler.SetSharedAIValidator(func(_ context.Context, profile AIProfile) error {
		providers = append(providers, profile.Config.Provider)
		return nil
	}, nil)

	// A local key saved while another provider is the candidate cannot be
	// probed (no endpoint+model context) — it persists and is proven the
	// moment the local provider is selected.
	recorder := updateCredentialSettings(t, handler, `{"gemini_key":"gemini-secret","ai_provider":"gemini","ai_model":"gemini-model","local_openai_key":"proxy-token"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	for _, provider := range providers {
		if provider == AIProviderLocalOpenAI {
			t.Fatalf("unselected local key triggered a validation turn: %v", providers)
		}
	}
	if got := registry.GetCredential(KeyLocalOpenAIKey); got != "proxy-token" {
		t.Fatalf("stored local key = %q", got)
	}
}

func TestSharedAIProfileCarriesLocalEndpointOnlyForLocalProvider(t *testing.T) {
	registry, userID, _ := newUserAIRegistry(t)
	if _, err := registry.db.Exec(`UPDATE users SET ai_shared_enabled = 1 WHERE id = ?`, userID); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetSetting(KeyLocalOpenAIBaseURL, "http://llm-host:11434/v1"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetSetting(KeyLocalOpenAIReasoningEffort, "none"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAIConfig(AIProviderLocalOpenAI, "qwen3.6:35b-a3b"); err != nil {
		t.Fatal(err)
	}

	shared, err := registry.LoadSharedAIProfile(context.Background())
	if err != nil || shared.BaseURL != "http://llm-host:11434/v1" || shared.ReasoningEffort != "none" {
		t.Fatalf("shared local profile = %#v, err=%v", shared, err)
	}
	// Keyless local profiles are credential-complete.
	if shared.APIKey != "" || !shared.CredentialPresent {
		t.Fatalf("keyless shared profile = APIKey %q CredentialPresent %t", shared.APIKey, shared.CredentialPresent)
	}

	// The local endpoint belongs to the local provider: hosted openai never
	// inherits it even while the rows stay stored.
	if err := registry.SetCredential(KeyOpenAIKey, "hosted-secret"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAIConfig(AIProviderOpenAI, "gpt-5.6-sol"); err != nil {
		t.Fatal(err)
	}
	shared, err = registry.LoadSharedAIProfile(context.Background())
	if err != nil || shared.BaseURL != "" || shared.ReasoningEffort != "" {
		t.Fatalf("hosted openai profile inherited local endpoint: %#v, err=%v", shared, err)
	}
}
