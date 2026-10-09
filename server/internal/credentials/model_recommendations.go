package credentials

// ModelRecommendation is an explicitly maintained, tool-capable replacement
// for one integration. It is not inferred from ordering or model ID recency.
type ModelRecommendation struct {
	Model       string `json:"model"`
	Source      string `json:"source"`
	Description string `json:"description"`
}

// Reviewed with the endpoint contracts in docs/ai-provider-model-catalogs.md.
// Codex resolves its actual replacement from the linked account's model/list.
var modelRecommendations = map[string]ModelRecommendation{
	AIProviderAnthropic:   {"claude-sonnet-5-5", "Cantinarr recommendation", "Tool use and adaptive reasoning. Cost and latency depend on usage; pricing differences are not available."},
	AIProviderOpenAI:      {"gpt-4.1-mini", "Cantinarr recommendation", "Tool use on Chat Completions and Responses. No reasoning controls; a saved reasoning effort is omitted. Cost and latency may change; pricing differences are not available."},
	AIProviderGemini:      {"gemini-3.8-flash", "Cantinarr recommendation", "Text and function calling. Cost, latency, and reasoning behavior may change; pricing differences are not available."},
	AIProviderGrok:        {"grok-4.7", "Cantinarr recommendation", "Tool use on the xAI public API. Cost and latency may change; pricing differences are not available."},
	AIProviderGrokOAuth:   {"grok-4.6", "Cantinarr recommendation", "Tool use through the existing Grok Build subscription. Plan usage and latency may change; pricing differences are not available."},
	AIProviderCodex:       {"", "OpenAI model catalog", "Uses the linked account's recommended upgrade or default. Plan usage, latency, and capabilities may change; pricing differences are not available."},
	AIProviderLocalOpenAI: {"", "No recommendation available", "Local endpoints have no standard recommendation or tool capability metadata. Choose a replacement model manually."},
}

func RecommendedAIModel(provider string) (ModelRecommendation, bool) {
	r, ok := modelRecommendations[provider]
	return r, ok && provider != AIProviderLocalOpenAI
}

func init() {
	for i := range AIProviders {
		if recommendation, ok := modelRecommendations[AIProviders[i].ID]; ok {
			AIProviders[i].ModelFallback = &recommendation
		}
	}
}
