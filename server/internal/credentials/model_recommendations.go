package credentials

// ModelRecommendation describes a provider-owned dynamic fallback mechanism.
// Model is populated at request time, never with a Cantinarr-selected version.
type ModelRecommendation struct {
	Model       string `json:"model"`
	Source      string `json:"source"`
	Description string `json:"description"`
}

// Only integrations with provider-owned defaults or rolling aliases advertise
// this capability. Model lists and version-scoped aliases alone are insufficient.
// See docs/ai-provider-model-catalogs.md for the upstream contracts.
var modelRecommendations = map[string]ModelRecommendation{
	AIProviderCodex:     {"", "OpenAI model catalog", "OpenAI chooses the linked account's recommended upgrade or default. Plan usage, latency, and capabilities may change."},
	AIProviderGemini:    {"", "Google latest alias", "Google chooses the latest model in the selected Flash, Flash-Lite, or Pro family. This may be a preview or experimental release. Cost, latency, and capabilities may change."},
	AIProviderGrok:      {"", "xAI latest alias", "xAI chooses the model behind its rolling grok-build-latest alias. Cost, latency, and capabilities may change."},
	AIProviderGrokOAuth: {"", "xAI remote default", "xAI chooses the linked Grok Build account's remote default. Plan usage, latency, and capabilities may change."},
}

func RecommendedAIModel(provider string) (ModelRecommendation, bool) {
	r, ok := modelRecommendations[provider]
	return r, ok
}

func init() {
	for i := range AIProviders {
		if recommendation, ok := modelRecommendations[AIProviders[i].ID]; ok {
			AIProviders[i].ModelFallback = &recommendation
		}
	}
}
