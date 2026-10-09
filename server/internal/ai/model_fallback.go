package ai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"unicode"

	"github.com/anthropics/anthropic-sdk-go"
	openai "github.com/openai/openai-go/v3"
	"google.golang.org/genai"

	"github.com/windoze95/cantinarr-server/internal/codexapp"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// ModelFallback contains only safe display data, never the provider response,
// endpoint, key, OAuth identity, or prompt. EventKey is an opaque dedupe key.
type ModelFallback struct {
	Status               string `json:"status"`
	EventKey             string `json:"-"`
	Source               string `json:"source"`
	UserID               int64  `json:"user_id,omitempty"`
	Scope                string `json:"scope"`
	Provider             string `json:"provider"`
	SelectedModel        string `json:"selected_model"`
	ReplacementModel     string `json:"replacement_model"`
	Reason               string `json:"reason"`
	RecommendationSource string `json:"recommendation_source"`
	Differences          string `json:"differences"`
	SettingsPath         string `json:"settings_path"`
	Succeeded            bool   `json:"succeeded"`
}

type ModelFallbackSink interface {
	RecordModelFallback(ModelFallback) error
}

func (h *Handler) SetModelFallbackSink(sink ModelFallbackSink) { h.modelFallbackSink = sink }

type modelFallbackError struct{ message string }

func (e *modelFallbackError) Error() string { return e.message }

// confirmedModelUnavailable is deliberately stricter than save-time error
// presentation: an arbitrary 404 (wrong route) cannot authorize extra billing.
func confirmedModelUnavailable(err error, selected string) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, codexapp.ErrModelUnavailable) {
		return true
	}
	status := providerErrorStatus(err)
	if status != http.StatusBadRequest && status != http.StatusNotFound && status != http.StatusGone && status != http.StatusUnprocessableEntity {
		return false
	}
	var code, message string
	var oa *openai.Error
	var response *responsesStreamError
	var ant *anthropic.Error
	var gem genai.APIError
	switch {
	case errors.As(err, &oa):
		code, message = oa.Code, oa.Message
	case errors.As(err, &response):
		code, message = response.Code, response.Message
	case errors.As(err, &ant):
		var body struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(ant.RawJSON()), &body) != nil {
			return false
		}
		message = body.Error.Message
	case errors.As(err, &gem):
		message = gem.Message
	default:
		return false
	}
	// Provider messages must identify the selected model. Never interpret
	// quota/auth/schema/endpoint errors merely because they mention a model.
	message = strings.ToLower(message)
	for _, excluded := range []string{"api key", "api_key", "credential", "quota", "rate limit", "overload", "temporarily", "tool", "parameter", "endpoint", "route", "schema"} {
		if strings.Contains(message, excluded) {
			return false
		}
	}
	switch strings.ToLower(code) {
	case "model_not_found", "model_not_available", "model_unavailable", "model_retired", "model_deprecated":
		return true
	}
	selected = strings.ToLower(strings.TrimSpace(selected))
	if selected == "" || !strings.Contains(message, selected) {
		return false
	}
	if status == http.StatusNotFound && strings.TrimSpace(message) == "model: "+selected {
		return true
	}
	if !strings.Contains(message, "model") {
		return false
	}
	for _, marker := range []string{"not found", "does not exist", "not available", "unavailable", "retired", "deprecated", "no longer supported", "not supported for this account"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func (h *Handler) fallbackRecommendation(ctx context.Context, r resolvedAI) (credentials.ModelRecommendation, error) {
	recommendation, ok := credentials.RecommendedAIModel(r.Provider)
	if !ok || (r.Provider == credentials.AIProviderOpenAI && !usesOpenAIAPICatalogSemantics(r.BaseURL)) {
		return recommendation, &modelFallbackError{"The selected model is unavailable and this endpoint has no compatible recommendation. Choose a replacement in AI settings."}
	}
	if r.Provider == credentials.AIProviderCodex {
		if h.codex == nil {
			return recommendation, &modelFallbackError{"The selected model is unavailable and the OpenAI recommendation could not be read. Check AI settings."}
		}
		model, source, err := h.codex.RecommendedModel(ctx, r.Account, r.Model)
		if err != nil {
			return recommendation, &modelFallbackError{"The selected model is unavailable and no compatible OpenAI recommendation could be confirmed. Check AI settings."}
		}
		recommendation.Model, recommendation.Source = model, source
	}
	if recommendation.Model == "" || recommendation.Model == r.Model {
		return recommendation, &modelFallbackError{"The selected model is unavailable and there is no different compatible recommended model. Choose a replacement in AI settings."}
	}
	return recommendation, nil
}

// retryUnavailableModel runs at most one replacement, using an immutable
// profile snapshot. Chat calls this only before output/tool execution. A
// TurnRunner can call it for one model request because it never executes tools.
func (h *Handler) retryUnavailableModel(ctx context.Context, r resolvedAI, scope string, original error, notify func(ModelFallback), run func(string) error) (*ModelFallback, error) {
	if original == nil || !r.ModelFallbackEnabled || ctx.Err() != nil || !confirmedModelUnavailable(original, r.Model) {
		return nil, original
	}
	recommendation, err := h.fallbackRecommendation(ctx, r)
	if err != nil {
		return nil, err
	}
	event := ModelFallback{
		Status: "attempting",
		Source: r.Source, UserID: r.Account.UserID(), Scope: scope, Provider: r.Provider,
		SelectedModel: safeModelLabel(r.Model, r.APIKey), ReplacementModel: safeModelLabel(recommendation.Model, r.APIKey),
		Reason:               "The provider reported that the selected model is unavailable for this account.",
		RecommendationSource: recommendation.Source, Differences: recommendation.Description,
		SettingsPath: "/settings/credentials",
	}
	if r.Source == aiSourcePersonal {
		event.SettingsPath = "/settings/ai"
	}
	identity, _ := json.Marshal([]any{r.Source, event.UserID, scope, r.Provider, r.Model, recommendation.Model, r.BaseURL, r.APIKey})
	sum := sha256.Sum256(identity)
	event.EventKey = hex.EncodeToString(sum[:])
	if notify != nil {
		notify(event)
	}
	err = run(recommendation.Model)
	event.Succeeded = err == nil
	event.Status = "used"
	if err != nil {
		event.Status = "failed"
	}
	if notify != nil {
		notify(event)
	}
	if h.modelFallbackSink != nil {
		if recordErr := h.modelFallbackSink.RecordModelFallback(event); recordErr != nil {
			log.Printf("AI model fallback notification could not be recorded")
		}
	}
	if err != nil {
		return &event, &modelFallbackError{fmt.Sprintf("The selected model %q is unavailable. The one fallback attempt with %q also failed. Check AI settings; no further model was tried.", event.SelectedModel, event.ReplacementModel)}
	}
	return &event, nil
}

func safeModelLabel(model, key string) string {
	if key != "" {
		model = strings.ReplaceAll(model, key, "[redacted]")
	}
	model = secrets.RedactText(model)
	model = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, model)
	runes := []rune(model)
	if len(runes) > 256 {
		model = string(runes[:256])
	}
	return model
}

type fallbackTurnRunner struct {
	handler   *Handler
	resolved  resolvedAI
	scope     string
	delegate  TurnRunner
	build     func(string) TurnRunner
	fallback  *ModelFallback
	attempted bool
}

func (r *fallbackTurnRunner) NextTurn(ctx context.Context, p TurnParams) (TurnResult, error) {
	result, err := r.delegate.NextTurn(ctx, p)
	if err != nil && !r.attempted {
		var event *ModelFallback
		event, err = r.handler.retryUnavailableModel(ctx, r.resolved, r.scope, err, nil, func(model string) error {
			r.attempted = true
			r.delegate = r.build(model)
			var retryErr error
			result, retryErr = r.delegate.NextTurn(ctx, p)
			return retryErr
		})
		if event != nil {
			r.fallback = event
		}
	}
	result.ModelFallback = r.fallback
	return result, err
}
