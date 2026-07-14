package ai

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	openai "github.com/openai/openai-go/v3"
	"google.golang.org/genai"

	"github.com/windoze95/cantinarr-server/internal/codexapp"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

const (
	aiValidationTimeout       = 60 * time.Second
	aiValidationMaxTokens     = 1024
	aiHealthMonitorPollPeriod = time.Hour
	aiValidationSystemPrompt  = "You are checking whether an AI provider is ready. Do not use tools. Return one short plain-text response."
	aiValidationUserPrompt    = "Reply with exactly: OK"
)

// ErrAIValidation is the safe error returned at settings boundaries. Raw
// provider errors stay in server logs and never expose credentials or upstream
// payloads to a client.
var ErrAIValidation = errors.New("the selected AI provider and model could not complete a test message")

type AIValidationFailureKind string

const (
	AIValidationFailureInvalidCredential AIValidationFailureKind = "invalid_credential"
	AIValidationFailureUnsupportedModel  AIValidationFailureKind = "unsupported_model"
	AIValidationFailureQuota             AIValidationFailureKind = "quota_or_rate_limit"
	AIValidationFailureTemporary         AIValidationFailureKind = "temporary_upstream"
	AIValidationFailureInvalidResponse   AIValidationFailureKind = "invalid_response"
)

// AIValidationFailure retains the provider error for server-side inspection
// while its Error string and user-facing message contain only a fixed,
// credential-safe classification.
type AIValidationFailure struct {
	Kind  AIValidationFailureKind
	cause error
}

func (e *AIValidationFailure) Error() string {
	return ErrAIValidation.Error() + ": " + aiValidationFailureDetail(e.Kind)
}

func (e *AIValidationFailure) Unwrap() []error {
	if e.cause == nil {
		return []error{ErrAIValidation}
	}
	return []error{ErrAIValidation, e.cause}
}

func (e *AIValidationFailure) SafeUserMessage() string {
	return AIValidationUserMessage(e)
}

// SafeDiagnostic preserves the upstream status/message operators need while
// scrubbing credential-bearing URLs, headers, JSON fields, and assignments.
func (e *AIValidationFailure) SafeDiagnostic() string {
	if e == nil {
		return ErrAIValidation.Error()
	}
	if e.cause == nil {
		return e.Error()
	}
	return e.Error() + ": " + secrets.RedactError(e.cause).Error()
}

func AIValidationDiagnostic(err error) string {
	if err == nil {
		return ""
	}
	var failure *AIValidationFailure
	if errors.As(err, &failure) {
		return failure.SafeDiagnostic()
	}
	return secrets.RedactError(err).Error()
}

// AIValidationUserMessage returns an actionable, sanitized settings response.
// It never includes an upstream response body, request metadata, or credential.
func AIValidationUserMessage(err error) string {
	kind := AIValidationFailureInvalidResponse
	var failure *AIValidationFailure
	if errors.As(err, &failure) {
		kind = failure.Kind
	}
	return aiValidationFailureDetail(kind) + " Nothing was saved."
}

func aiValidationFailureDetail(kind AIValidationFailureKind) string {
	switch kind {
	case AIValidationFailureInvalidCredential:
		return "The provider credential or account connection was rejected. Check or reconnect the provider credential."
	case AIValidationFailureUnsupportedModel:
		return "The selected model is unavailable for this API credential. Choose another model or check provider access."
	case AIValidationFailureQuota:
		return "The provider quota or rate limit was reached. Check billing and quota, or try again later."
	case AIValidationFailureTemporary:
		return "The AI provider is temporarily unavailable. Try again shortly."
	default:
		return "The selected AI provider and model did not return a usable test response."
	}
}

func newAIValidationFailure(cause error) error {
	return &AIValidationFailure{Kind: classifyAIValidationFailure(cause), cause: cause}
}

func classifyAIValidationFailure(err error) AIValidationFailureKind {
	switch {
	case errors.Is(err, codexapp.ErrNotConnected):
		return AIValidationFailureInvalidCredential
	case errors.Is(err, codexapp.ErrUsageLimit):
		return AIValidationFailureQuota
	case errors.Is(err, codexapp.ErrBusy), errors.Is(err, codexapp.ErrUnavailable):
		return AIValidationFailureTemporary
	}
	status := providerErrorStatus(err)
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return AIValidationFailureInvalidCredential
	case status == http.StatusNotFound:
		return AIValidationFailureUnsupportedModel
	case status == http.StatusTooManyRequests:
		return AIValidationFailureQuota
	case status == http.StatusRequestTimeout || status == http.StatusConflict || status >= http.StatusInternalServerError:
		return AIValidationFailureTemporary
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return AIValidationFailureTemporary
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return AIValidationFailureTemporary
	}
	return AIValidationFailureInvalidResponse
}

func providerErrorStatus(err error) int {
	var openAIErr *openai.Error
	if errors.As(err, &openAIErr) {
		return openAIErr.StatusCode
	}
	var anthropicErr *anthropic.Error
	if errors.As(err, &anthropicErr) {
		return anthropicErr.StatusCode
	}
	var geminiErr genai.APIError
	if errors.As(err, &geminiErr) {
		return geminiErr.Code
	}
	return 0
}

// SharedAIHealthIssueSink turns scheduled shared-provider health transitions
// into one deduplicated admin issue. The remediation service implements it.
type SharedAIHealthIssueSink interface {
	RecordSharedAIHealth(provider, model string, healthy bool) error
}

// SetSharedAIHealthIssueSink wires the admin issue surface after both services
// exist. It must be called before StartSharedAIHealthMonitor.
func (h *Handler) SetSharedAIHealthIssueSink(sink SharedAIHealthIssueSink) {
	h.healthIssueSink = sink
}

// ValidateSharedAISettings proves the exact candidate shared provider/model and
// credential/account with a real, small message-response turn before it is
// persisted.
func (h *Handler) ValidateSharedAISettings(ctx context.Context, profile credentials.AIProfile) error {
	return h.validateAIProfile(ctx, profile, codexapp.SharedAccount())
}

// ValidatePersonalAISettings applies the same save-time invariant to a user's
// own provider. Personal and shared account references remain explicit.
func (h *Handler) ValidatePersonalAISettings(ctx context.Context, userID int64, profile credentials.AIProfile) error {
	return h.validateAIProfile(ctx, profile, codexapp.PersonalAccount(userID))
}

func (h *Handler) validateAIProfile(ctx context.Context, profile credentials.AIProfile, account codexapp.AccountRef) error {
	ctx, cancel := context.WithTimeout(ctx, aiValidationTimeout)
	defer cancel()
	if h.validationProbe != nil {
		if err := h.validationProbe(ctx, profile, account); err != nil {
			return newAIValidationFailure(err)
		}
		return nil
	}
	if !credentials.IsValidAIProvider(profile.Config.Provider) || strings.TrimSpace(profile.Config.Model) == "" {
		return ErrAIValidation
	}
	if profile.Config.Provider == credentials.AIProviderCodex {
		if h.codex == nil || !h.codex.Available() {
			return ErrAIValidation
		}
		model := profile.Config.Model
		if model == "default" {
			model = ""
		}
		if err := h.codex.ProbeAccount(ctx, account, model); err != nil {
			return newAIValidationFailure(err)
		}
		return nil
	}
	if strings.TrimSpace(profile.APIKey) == "" {
		return ErrAIValidation
	}

	params := TurnParams{
		System: aiValidationSystemPrompt,
		History: Transcript{{
			Role: RoleUser,
			Content: []TranscriptBlock{{
				Type: BlockText,
				Text: aiValidationUserPrompt,
			}},
		}},
		ForceNoTools:     true,
		DisableReasoning: true,
		MaxTokens:        aiValidationMaxTokens,
	}
	var runner TurnRunner
	switch profile.Config.Provider {
	case credentials.AIProviderAnthropic:
		runner = NewService(profile.APIKey, profile.Config.Model, h.toolServer)
	case credentials.AIProviderOpenAI:
		runner = NewOpenAIService(profile.APIKey, profile.Config.Model, h.toolServer)
	case credentials.AIProviderGemini:
		runner = NewGeminiService(profile.APIKey, profile.Config.Model, h.toolServer)
	default:
		return ErrAIValidation
	}
	result, err := runner.NextTurn(ctx, params)
	if err != nil {
		return newAIValidationFailure(err)
	}
	for _, block := range result.Message.Content {
		if block.Type == BlockText && strings.TrimSpace(block.Text) != "" {
			return nil
		}
	}
	return newAIValidationFailure(nil)
}

// SharedAISettingsValidated records a successful save-time probe and clears a
// prior health alert. Call it only after the candidate settings commit.
func (h *Handler) SharedAISettingsValidated(config credentials.AIConfig) {
	if err := h.creds.RecordAIHealthCheck(time.Now()); err != nil {
		log.Printf("ai health: record save-time check: %v", err)
	}
	if h.healthIssueSink != nil {
		if err := h.healthIssueSink.RecordSharedAIHealth(config.Provider, config.Model, true); err != nil {
			log.Printf("ai health: resolve issue after settings validation: %v", err)
		}
	}
}

// StartSharedAIHealthMonitor checks once an hour whether the optional daily
// turn is due. The durable last-check timestamp prevents restart-driven drain.
func (h *Handler) StartSharedAIHealthMonitor(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(aiHealthMonitorPollPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				h.runSharedAIHealthCheck(ctx, now)
			}
		}
	}()
}

func (h *Handler) runSharedAIHealthCheck(ctx context.Context, now time.Time) {
	if h == nil || h.creds == nil || !h.creds.AISelectionConfigured() || !h.creds.AIHealthCheckDue(now) {
		return
	}
	resolved := h.resolveSharedAI(ctx)
	config := credentials.AIConfig{Provider: resolved.Provider, Model: resolved.Model}
	var probeErr error
	if !resolved.Available {
		probeErr = fmt.Errorf("shared AI unavailable: %s", resolved.Reason)
	} else {
		probeErr = h.ValidateSharedAISettings(ctx, credentials.AIProfile{
			Config:            config,
			APIKey:            resolved.APIKey,
			CredentialPresent: resolved.APIKey != "" || resolved.Provider == credentials.AIProviderCodex,
		})
	}
	// Record both success and failure. A failing provider should create one
	// issue per day, not be hammered every hourly scheduler tick.
	if err := h.creds.RecordAIHealthCheck(now); err != nil {
		log.Printf("ai health: record scheduled check: %v", err)
	}
	if probeErr != nil {
		log.Printf("ai health: shared provider=%q model=%q failed: %s", config.Provider, config.Model, AIValidationDiagnostic(probeErr))
	}
	if h.healthIssueSink != nil {
		if err := h.healthIssueSink.RecordSharedAIHealth(config.Provider, config.Model, probeErr == nil); err != nil {
			log.Printf("ai health: record admin issue transition: %v", err)
		}
	}
}
