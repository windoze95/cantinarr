package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
)

var errProviderRecommendation = errors.New("no compatible provider recommendation")

// These are provider-owned, generation-independent aliases, not model versions
// selected by Cantinarr. Specialized image/audio/live/embedding models cannot
// inherit a text-model fallback just because their names contain "flash".
var geminiTextModel = regexp.MustCompile(`^gemini-(?:[0-9]+(?:\.[0-9]+)*-)?(flash-lite|flash|pro)(?:-(?:[0-9]{3}|latest|preview(?:-[0-9-]+)?|exp(?:-[0-9-]+)?))?$`)
var providerModelID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,255}$`)

func geminiFallbackModel(ctx context.Context, r resolvedAI) (string, error) {
	selected := strings.TrimPrefix(r.Model, "models/")
	match := geminiTextModel.FindStringSubmatch(selected)
	if match == nil {
		return "", errProviderRecommendation
	}
	alias := "gemini-" + match[1] + "-latest"
	if alias == selected {
		return "", errProviderRecommendation
	}
	var model struct {
		Name    string   `json:"name"`
		Methods []string `json:"supportedGenerationMethods"`
	}
	base := recommendationBaseURL("GOOGLE_GEMINI_BASE_URL", "https://generativelanguage.googleapis.com")
	err := readRecommendationJSON(ctx, base+"/v1beta/models/"+alias, http.Header{"X-Goog-Api-Key": {r.APIKey}}, &model)
	resolved := strings.TrimPrefix(model.Name, "models/")
	resolvedFamily := geminiTextModel.FindStringSubmatch(resolved)
	if err != nil || resolvedFamily == nil || resolvedFamily[1] != match[1] || resolved == selected || !slices.Contains(model.Methods, "generateContent") {
		return "", errProviderRecommendation
	}
	return alias, nil
}

func grokFallbackModel(ctx context.Context, r resolvedAI) (string, error) {
	const alias = "grok-build-latest"
	if r.Model == alias {
		return "", errProviderRecommendation
	}
	type languageModel struct {
		ID      string   `json:"id"`
		Aliases []string `json:"aliases"`
		Input   []string `json:"input_modalities"`
		Output  []string `json:"output_modalities"`
	}
	var catalog struct {
		Models []languageModel `json:"models"`
	}
	base := recommendationBaseURL("XAI_BASE_URL", "https://api.x.ai/v1")
	if err := readRecommendationJSON(ctx, base+"/language-models", http.Header{"Authorization": {"Bearer " + r.APIKey}}, &catalog); err != nil {
		return "", errProviderRecommendation
	}
	matched := false
	for _, model := range catalog.Models {
		if model.ID != alias && !slices.Contains(model.Aliases, alias) {
			continue
		}
		if matched || !providerModelID.MatchString(model.ID) || model.ID == r.Model ||
			!slices.Contains(model.Input, "text") || !slices.Contains(model.Output, "text") {
			return "", errProviderRecommendation
		}
		matched = true
	}
	if !matched {
		return "", errProviderRecommendation
	}
	return alias, nil
}

// Grok Build's default lives in /settings, independently of /v1/models.
// Both reads use the same linked account and endpoint as the failed turn.
// Neither the bundled CLI default nor the first catalog entry is a substitute.
func (h *Handler) grokOAuthFallbackModel(ctx context.Context, r resolvedAI) (string, error) {
	if h.grok == nil {
		return "", errProviderRecommendation
	}
	token, err := h.grok.AccessToken(ctx, grokAccountFor(r.Account))
	if err != nil {
		return "", err
	}
	base := recommendationBaseURL("GROK_OAUTH_BASE_URL", "https://cli-chat-proxy.grok.com/v1")
	headers := http.Header{
		"Authorization":            {"Bearer " + token},
		"X-Xai-Token-Auth":         {"xai-grok-cli"},
		"X-Grok-Client-Version":    {grokOAuthProtocolVersion},
		"X-Grok-Client-Identifier": {"cantinarr"},
		"X-Grok-Client-Mode":       {"headless"},
	}
	// The subject is a routing hint to the same provider, never local authority.
	// The provider authenticates the bearer token. Opaque tokens omit the hint.
	if parts := strings.Split(token, "."); len(parts) == 3 {
		if payload, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
			var claims struct {
				Subject string `json:"sub"`
			}
			if json.Unmarshal(payload, &claims) == nil && providerModelID.MatchString(claims.Subject) {
				headers.Set("x-userid", claims.Subject)
			}
		}
	}
	var settings struct {
		Default string `json:"default_model"`
	}
	if err := readRecommendationJSON(ctx, strings.TrimSuffix(base, "/v1")+"/settings", headers, &settings); err != nil || !providerModelID.MatchString(settings.Default) {
		return "", errProviderRecommendation
	}
	var catalog struct {
		Data []grokRecommendedModel `json:"data"`
	}
	if err := readRecommendationJSON(ctx, base+"/models", headers, &catalog); err != nil {
		return "", errProviderRecommendation
	}
	return selectGrokDefault(catalog.Data, settings.Default, r.Model, base)
}

type grokRecommendedModel struct {
	ID         string                `json:"id"`
	Model      string                `json:"model"`
	ModelID    string                `json:"modelId"`
	Hidden     bool                  `json:"hidden"`
	Backend    string                `json:"apiBackend"`
	BackendV2  string                `json:"api_backend"`
	BaseURL    string                `json:"baseUrl"`
	BaseURLV2  string                `json:"base_url"`
	APIBaseURL string                `json:"apiBaseUrl"`
	APIBaseV2  string                `json:"api_base_url"`
	Meta       *grokRecommendedModel `json:"_meta"`
}

func selectGrokDefault(models []grokRecommendedModel, recommended, selected, base string) (string, error) {
	var result string
	for _, m := range models {
		model := m.Model
		if model == "" {
			model = m.ModelID
		}
		if model == "" {
			model = m.ID
		}
		if model == "" && m.Meta != nil {
			model = m.Meta.Model
			if model == "" {
				model = m.Meta.ModelID
			}
		}
		if m.ID != recommended && model != recommended {
			continue
		}
		backend := m.Backend
		if backend == "" {
			backend = m.BackendV2
		}
		// supported_in_api is an API-key restriction in Grok Build's catalog:
		// false means OAuth-only, not unavailable to this linked account.
		incompatible := m.Hidden
		if m.Meta != nil {
			incompatible = incompatible || m.Meta.Hidden
		}
		for _, endpoint := range []string{m.BaseURL, m.BaseURLV2, m.APIBaseURL, m.APIBaseV2} {
			if endpoint != "" && strings.TrimRight(endpoint, "/") != base {
				incompatible = true
			}
		}
		if incompatible || backend != "responses" || !providerModelID.MatchString(model) || model == selected || result != "" {
			return "", errProviderRecommendation
		}
		result = model
	}
	if result == "" {
		return "", errProviderRecommendation
	}
	return result, nil
}

// Environment overrides are the same test seams used by the inference adapters.
// No URL or credential supplied by catalog metadata is followed.
func recommendationBaseURL(env, fallback string) string {
	if base := strings.TrimSpace(os.Getenv(env)); base != "" {
		return strings.TrimRight(base, "/")
	}
	return fallback
}

func readRecommendationJSON(ctx context.Context, endpoint string, headers http.Header, result any) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errProviderRecommendation
	}
	req.Header = headers.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req.Header.Set("Accept", "application/json")
	client := newHostedProviderHTTPClient(15 * time.Second)
	defer client.CloseIdleConnections()
	response, err := client.Do(req)
	if err != nil {
		return errProviderRecommendation
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errProviderRecommendation
	}
	const maxBytes = 1 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil || len(body) > maxBytes || json.Unmarshal(body, result) != nil {
		return errProviderRecommendation
	}
	return nil
}
