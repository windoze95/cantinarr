package ai

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/codexapp"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

const (
	aiValidationTimeout       = 60 * time.Second
	aiValidationMaxTokens     = 256
	aiHealthMonitorPollPeriod = time.Hour
	aiValidationSystemPrompt  = "You are checking whether an AI provider is ready. Do not use tools. Return one short plain-text response."
	aiValidationUserPrompt    = "Reply with exactly: OK"
)

// ErrAIValidation is the safe error returned at settings boundaries. Raw
// provider errors stay in server logs and never expose credentials or upstream
// payloads to a client.
var ErrAIValidation = errors.New("the selected AI provider and model could not complete a test message")

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
			return fmt.Errorf("%w: %v", ErrAIValidation, err)
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
			return fmt.Errorf("%w: %v", ErrAIValidation, err)
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
		ForceNoTools: true,
		MaxTokens:    aiValidationMaxTokens,
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
		return fmt.Errorf("%w: %v", ErrAIValidation, err)
	}
	for _, block := range result.Message.Content {
		if block.Type == BlockText && strings.TrimSpace(block.Text) != "" {
			return nil
		}
	}
	return ErrAIValidation
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
		log.Printf("ai health: shared provider=%q model=%q failed: %v", config.Provider, config.Model, secrets.RedactError(probeErr))
	}
	if h.healthIssueSink != nil {
		if err := h.healthIssueSink.RecordSharedAIHealth(config.Provider, config.Model, probeErr == nil); err != nil {
			log.Printf("ai health: record admin issue transition: %v", err)
		}
	}
}
