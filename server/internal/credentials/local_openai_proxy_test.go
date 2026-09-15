package credentials

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestAIProviderMetadataAdvertisesProxyOptInOnlyForLocalProvider(t *testing.T) {
	for _, option := range AIProviders {
		// Only the local provider's endpoint is admin-typed, so it is the
		// only one whose transport class is not knowable up front.
		want := option.ID == AIProviderLocalOpenAI
		if option.SupportsProxyOptIn != want {
			t.Errorf("%s supports_proxy_opt_in = %t, want %t", option.ID, option.SupportsProxyOptIn, want)
		}
		encoded, err := json.Marshal(option)
		if err != nil {
			t.Fatalf("marshal %s metadata: %v", option.ID, err)
		}
		var metadata map[string]any
		if err := json.Unmarshal(encoded, &metadata); err != nil {
			t.Fatalf("decode %s metadata: %v", option.ID, err)
		}
		flag, present := metadata["supports_proxy_opt_in"]
		if want && (!present || flag != true) {
			t.Errorf("%s JSON supports_proxy_opt_in = %v (present=%t), want true", option.ID, flag, present)
		}
		// omitempty keeps every other provider's wire shape byte-identical,
		// which is what lets an older app ignore the flag entirely.
		if !want && present {
			t.Errorf("%s JSON unexpectedly carries supports_proxy_opt_in: %s", option.ID, encoded)
		}
	}
}

func TestLocalOpenAIProxyOptInDefaultsOffAndPersists(t *testing.T) {
	handler, registry := newCredentialHandlerTest(t)
	var profiles []AIProfile
	handler.SetSharedAIValidator(func(_ context.Context, profile AIProfile) error {
		profiles = append(profiles, profile)
		return nil
	}, nil)

	// An install that never saw this setting keeps dialing the endpoint
	// directly, which is what every install before it assumed.
	if registry.LocalOpenAIUseProxy() {
		t.Fatal("an absent row must read as off")
	}
	if got := getCredentialsAIStatus(t, handler)["local_openai_use_proxy"]; got != false {
		t.Fatalf("default echoed local_openai_use_proxy = %v, want false", got)
	}

	recorder := updateCredentialSettings(t, handler, `{"ai_provider":"local_openai","ai_model":"qwen3.6:35b-a3b","local_openai_base_url":"https://llm.example.com/v1","local_openai_use_proxy":"true"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(profiles) != 1 || !profiles[0].UseProxy {
		t.Fatalf("validated profiles = %#v", profiles)
	}
	var raw string
	if err := registry.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, KeyLocalOpenAIUseProxy).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != "true" {
		t.Fatalf("stored proxy opt-in = %q, want \"true\"", raw)
	}
	if !registry.LocalOpenAIUseProxy() {
		t.Fatal("registry did not read back the saved opt-in")
	}
	if got := getCredentialsAIStatus(t, handler)["local_openai_use_proxy"]; got != true {
		t.Fatalf("echoed local_openai_use_proxy = %v, want true", got)
	}
}

func TestLocalOpenAIProxyOptInChangeAloneRerunsValidationTurn(t *testing.T) {
	handler, registry := newCredentialHandlerTest(t)
	if err := registry.SetAIConfig(AIProviderLocalOpenAI, "stored-model"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetSetting(KeyLocalOpenAIBaseURL, "https://llm.example.com/v1"); err != nil {
		t.Fatal(err)
	}
	var profiles []AIProfile
	handler.SetSharedAIValidator(func(_ context.Context, profile AIProfile) error {
		profiles = append(profiles, profile)
		return nil
	}, nil)

	// Flipping it changes how the endpoint is reached, so the save has to
	// prove the endpoint still answers over the new route.
	recorder := updateCredentialSettings(t, handler, `{"local_openai_use_proxy":"true"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(profiles) != 1 || !profiles[0].UseProxy || profiles[0].BaseURL != "https://llm.example.com/v1" || profiles[0].Config.Model != "stored-model" {
		t.Fatalf("validated profiles = %#v", profiles)
	}

	// Turning it back off is a change too, and stores the explicit false
	// rather than leaving the endpoint's class to a missing row.
	profiles = nil
	recorder = updateCredentialSettings(t, handler, `{"local_openai_use_proxy":"false"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("off status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(profiles) != 1 || profiles[0].UseProxy {
		t.Fatalf("off validated profiles = %#v", profiles)
	}
	if registry.LocalOpenAIUseProxy() {
		t.Fatal("opt-in survived being turned off")
	}
}

func TestLocalOpenAIProxyOptInInvalidRejectedNothingSaved(t *testing.T) {
	handler, registry := newCredentialHandlerTest(t)
	if err := registry.SetAIConfig(AIProviderLocalOpenAI, "stored-model"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetSetting(KeyLocalOpenAIBaseURL, "https://llm.example.com/v1"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	handler.SetSharedAIValidator(func(context.Context, AIProfile) error {
		calls++
		return nil
	}, nil)

	for _, invalid := range []string{"maybe", "", "2"} {
		body, err := json.Marshal(map[string]string{KeyLocalOpenAIUseProxy: invalid})
		if err != nil {
			t.Fatal(err)
		}
		recorder := updateCredentialSettings(t, handler, string(body))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("value %q: status=%d body=%s", invalid, recorder.Code, recorder.Body.String())
		}
	}
	if calls != 0 {
		t.Fatalf("invalid opt-in values triggered %d validation turns", calls)
	}
	var count int
	if err := registry.db.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = ?`, KeyLocalOpenAIUseProxy).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("a rejected value was stored anyway (%d rows)", count)
	}
}
