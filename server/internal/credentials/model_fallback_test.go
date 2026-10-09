package credentials

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestSharedFallbackPreferenceDefaultsOffAndCanSaveAfterRetirement(t *testing.T) {
	h, registry := newCredentialHandlerTest(t)
	if err := registry.SetAIConfig(AIProviderOpenAI, "retired"); err != nil {
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

func TestRecommendationsAreExplicitCompatibleCatalogEntries(t *testing.T) {
	for _, p := range AIProviders {
		r, ok := RecommendedAIModel(p.ID)
		if p.ModelFallback == nil {
			t.Fatalf("missing capability for %s", p.ID)
		}
		if p.ID == AIProviderLocalOpenAI {
			if ok {
				t.Fatal("local gained hosted fallback")
			}
			continue
		}
		if !ok || r.Source == "" || r.Description == "" {
			t.Fatalf("missing recommendation: %s", p.ID)
		}
		if p.ID == AIProviderCodex {
			if r.Model != "" {
				t.Fatal("Codex recommendation was pinned")
			}
			continue
		}
		found := false
		for _, m := range p.Models {
			found = found || m.ID == r.Model
		}
		if !found {
			t.Fatalf("recommendation %s absent from %s contracts", r.Model, p.ID)
		}
	}
}
