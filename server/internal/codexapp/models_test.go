package codexapp

import (
	"context"
	"testing"
)

func TestCatalogFallbackCannotFollowUnrelatedCodexErrors(t *testing.T) {
	for _, message := range []string{
		"model is not supported with this tool schema",
		"model unavailable due to rate_limit_exceeded",
		"model unavailable due to quota",
		"model unavailable: invalid_api_key",
		"model unavailable temporarily",
		"model not found at this endpoint",
	} {
		if codexModelUnavailableError(message) {
			t.Errorf("unrelated failure classified as model unavailable: %s", message)
		}
	}
}

func TestRecommendedModelReadsPaginatedAccountCatalog(t *testing.T) {
	for _, account := range []AccountRef{SharedAccount(), PersonalAccount(2)} {
		manager, _, _, runtimeDir, _ := fakeManager(t)
		if err := manager.saveAccount(account, []byte(`{"tokens":{"access_token":"account-secret"}}`), AccountStatus{Connected: true}); err != nil {
			t.Fatal(err)
		}
		model, source, err := manager.RecommendedModel(context.Background(), account, "old")
		if err != nil || model != "replacement" || source != "OpenAI recommended upgrade" {
			t.Fatalf("model=%s source=%s err=%v", model, source, err)
		}
		assertRuntimeEmpty(t, runtimeDir)
	}
}

func TestSelectRecommendedModelRequiresProviderRecommendationAndText(t *testing.T) {
	for _, tc := range []struct {
		name           string
		models         []catalogModel
		selected, want string
	}{
		{"upgrade", []catalogModel{{Model: "old", Upgrade: "up"}, {Model: "up"}, {Model: "default", IsDefault: true}}, "old", "up"},
		{"default", []catalogModel{{Model: "old"}, {Model: "default", IsDefault: true}}, "old", "default"},
		{"never_newest", []catalogModel{{Model: "newest"}}, "old", ""},
		{"hidden", []catalogModel{{Model: "hidden", IsDefault: true, Hidden: true}}, "old", ""},
		{"audio_only", []catalogModel{{Model: "audio", IsDefault: true, InputModalities: []string{"audio"}}}, "old", ""},
		{"already_recommended", []catalogModel{{Model: "old", IsDefault: true}}, "old", ""},
		{"already_default_selector", []catalogModel{{Model: "new", IsDefault: true}}, "default", ""},
		{"ambiguous_default", []catalogModel{{Model: "one", IsDefault: true}, {Model: "two", IsDefault: true}}, "old", ""},
		{"missing_upgrade_falls_to_default", []catalogModel{{Model: "old", Upgrade: "gone"}, {Model: "default", IsDefault: true}}, "old", "default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model, _, err := selectRecommendedModel(tc.models, tc.selected)
			if model != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("model=%s err=%v", model, err)
			}
		})
	}
}
