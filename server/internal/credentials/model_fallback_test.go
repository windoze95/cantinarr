package credentials

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestSharedFallbackPreferenceDefaultsOffAndCanSaveAfterRetirement(t *testing.T) {
	h, registry := newCredentialHandlerTest(t)
	if err := registry.SetAIConfig(AIProviderGrok, "retired"); err != nil {
		t.Fatal(err)
	}
	if registry.GetAIConfig().ModelFallbackEnabled {
		t.Fatal("fallback defaulted on")
	}
	calls := 0
	h.SetSharedAIValidator(func(context.Context, AIProfile) error { calls++; return errors.New("retired") }, nil)
	for _, body := range []string{`{"ai_model_fallback_enabled":"true"}`, `{"ai_model_fallback_enabled":"false"}`, `{"ai_model_fallback_enabled":"true"}`} {
		r := updateCredentialSettings(t, h, body)
		if r.Code != http.StatusOK {
			t.Fatalf("save=%d %s", r.Code, r.Body.String())
		}
	}
	if calls != 0 {
		t.Fatal("preference-only save probed retired model")
	}
	p, err := registry.LoadSharedAIProfile(context.Background())
	if err != nil || !p.Config.ModelFallbackEnabled || p.Config.Model != "retired" {
		t.Fatalf("profile=%+v err=%v", p, err)
	}
	r := updateCredentialSettings(t, h, `{"ai_model_fallback_enabled":"maybe"}`)
	if r.Code != 400 || !registry.GetAIConfig().ModelFallbackEnabled {
		t.Fatal("invalid preference changed saved state")
	}
	r = updateCredentialSettings(t, h, `{"ai_model":"new-model","ai_model_fallback_enabled":"true"}`)
	if r.Code == 200 || calls != 1 || registry.GetAIConfig().Model != "retired" {
		t.Fatal("new model escaped validation")
	}
	h.SetSharedAIValidator(func(context.Context, AIProfile) error { return nil }, nil)
	r = updateCredentialSettings(t, h, `{"ai_provider":"codex","ai_model":"default"}`)
	if r.Code != 200 || registry.GetAIConfig().ModelFallbackEnabled {
		t.Fatalf("new provider inherited opt-in: %d %+v", r.Code, registry.GetAIConfig())
	}
}

func TestOnlyProviderOwnedDynamicFallbacksAreAdvertised(t *testing.T) {
	supported := map[string]bool{AIProviderCodex: true, AIProviderGemini: true, AIProviderGrok: true, AIProviderGrokOAuth: true}
	for _, p := range AIProviders {
		r, ok := RecommendedAIModel(p.ID)
		if ok != supported[p.ID] || (p.ModelFallback != nil) != supported[p.ID] {
			t.Fatalf("incorrect fallback capability for %s", p.ID)
		}
		if ok && (r.Source == "" || r.Description == "" || r.Model != "") {
			t.Fatalf("fallback must describe provider metadata without pinning a model: %+v", r)
		}
	}
}

func TestSharedUnsupportedFallbackPreferenceRejected(t *testing.T) {
	for _, provider := range []string{AIProviderAnthropic, AIProviderOpenAI, AIProviderLocalOpenAI} {
		t.Run(provider, func(t *testing.T) {
			h, registry := newCredentialHandlerTest(t)
			if err := registry.SetAIConfig(provider, "retired"); err != nil {
				t.Fatal(err)
			}
			response := updateCredentialSettings(t, h, `{"ai_model_fallback_enabled":"true"}`)
			if response.Code != 400 || registry.GetAIConfig().ModelFallbackEnabled {
				t.Fatalf("unsupported provider opted in: %d", response.Code)
			}
		})
	}
}
