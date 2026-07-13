package credentials

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

const (
	maxCredentialSettingsBody = 128 << 10
	maxAIModelLength          = 256
	maxAIKeyLength            = 32 << 10
)

// Handler provides admin-only REST endpoints for credential management.
type Handler struct {
	registry           *Registry
	sharedAIConfigured func() bool
	validateSharedAI   func(context.Context, AIProfile) error
	sharedAIValidated  func(AIConfig)
	updateMu           sync.Mutex
}

// SetSharedAIConfigured supplies the runtime-aware shared readiness check after
// the AI/Codex adapter has been constructed. It is wired once at startup.
func (h *Handler) SetSharedAIConfigured(check func() bool) {
	h.sharedAIConfigured = check
}

// SetSharedAIValidator makes a real response turn a mandatory precondition for
// shared API-key, provider, and model writes. validated runs only after commit.
func (h *Handler) SetSharedAIValidator(validate func(context.Context, AIProfile) error, validated func(AIConfig)) {
	h.validateSharedAI = validate
	h.sharedAIValidated = validated
}

// NewHandler creates a new credentials handler.
func NewHandler(registry *Registry) *Handler {
	return &Handler{registry: registry}
}

// Get returns which credentials are configured (booleans, never values).
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	status := make(map[string]any, len(AllKeys)+1)
	credentials := make(map[string]bool, len(AllKeys))
	for _, key := range AllKeys {
		configured := h.registry.IsConfigured(key)
		status[key] = configured
		credentials[key] = configured
	}
	status["credentials"] = credentials
	configured := h.registry.IsAIConfigured()
	if h.sharedAIConfigured != nil {
		configured = h.sharedAIConfigured()
	}
	config := h.registry.GetAIConfig()
	status["ai"] = map[string]any{
		"config":    config,
		"providers": AIProviders,
		"health_check": map[string]any{
			"enabled":        h.registry.AIHealthCheckEnabled(),
			"interval_hours": int(AIHealthCheckInterval / time.Hour),
			"last_checked_at": func() any {
				checked := h.registry.AIHealthLastCheck()
				if checked.IsZero() {
					return nil
				}
				return checked.Format(time.RFC3339)
			}(),
		},
		"shared": map[string]any{
			"config":     config,
			"configured": configured,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// Update sets one or more credentials and non-secret AI settings. Only
// non-empty fields are written.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var body map[string]string
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCredentialSettingsBody)).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	// Keep the validated snapshot and its transaction indivisible from another
	// admin settings request in this process. Provider turns are intentionally
	// inside this lock because committing a different concurrent key/model pair
	// would invalidate the exact-candidate guarantee.
	h.updateMu.Lock()
	defer h.updateMu.Unlock()

	valid := make(map[string]bool, len(AllKeys))
	for _, k := range AllKeys {
		valid[k] = true
	}
	valid[KeyAIProvider] = true
	valid[KeyAIModel] = true
	valid[KeyAIHealthCheckEnabled] = true

	for key := range body {
		if !valid[key] {
			http.Error(w, `{"error":"unknown credential key: `+key+`"}`, http.StatusBadRequest)
			return
		}
		if key == KeyAIProvider || key == KeyAIModel || key == KeyAIHealthCheckEnabled {
			continue
		}
	}

	current := h.registry.GetAIConfig()
	provider, providerSet := body[KeyAIProvider]
	model, modelSet := body[KeyAIModel]
	candidate := current
	if providerSet || modelSet {
		provider = strings.TrimSpace(provider)
		model = strings.TrimSpace(model)
		if !providerSet || provider == "" {
			provider = current.Provider
		}
		if !IsValidAIProvider(provider) {
			http.Error(w, `{"error":"unknown AI provider"}`, http.StatusBadRequest)
			return
		}
		if !modelSet || model == "" {
			if provider != current.Provider {
				model = DefaultAIModel(provider)
			} else {
				model = current.Model
			}
		}
		if len(model) > maxAIModelLength {
			http.Error(w, `{"error":"AI model is too long"}`, http.StatusBadRequest)
			return
		}
		candidate = AIConfig{Provider: provider, Model: model}
	}

	healthEnabled := h.registry.AIHealthCheckEnabled()
	healthValue, healthSet := body[KeyAIHealthCheckEnabled]
	if healthSet {
		parsed, err := strconv.ParseBool(strings.TrimSpace(healthValue))
		if err != nil {
			http.Error(w, `{"error":"ai_health_check_enabled must be true or false"}`, http.StatusBadRequest)
			return
		}
		healthEnabled = parsed
	}

	profiles := make(map[string]AIProfile)
	for _, option := range AIProviders {
		if option.CredentialKey == "" {
			continue
		}
		if value := strings.TrimSpace(body[option.CredentialKey]); value != "" {
			if len(value) > maxAIKeyLength {
				http.Error(w, `{"error":"AI credential is too long"}`, http.StatusBadRequest)
				return
			}
			config := AIConfig{Provider: option.ID, Model: DefaultAIModel(option.ID)}
			if option.ID == candidate.Provider {
				config.Model = candidate.Model
			}
			profiles[option.ID] = AIProfile{Config: config, APIKey: value, CredentialPresent: true}
			body[option.CredentialKey] = value
		}
	}
	mustTestSelected := providerSet || modelSet || (healthSet && healthEnabled && !h.registry.AIHealthCheckEnabled())
	if key := AIKeyCredentialKey(candidate.Provider); key != "" && strings.TrimSpace(body[key]) != "" {
		mustTestSelected = true
	}
	if mustTestSelected {
		profile, ok := profiles[candidate.Provider]
		if !ok {
			profile = AIProfile{Config: candidate}
			if key := AIKeyCredentialKey(candidate.Provider); key != "" {
				profile.APIKey = h.registry.GetCredential(key)
				profile.CredentialPresent = strings.TrimSpace(profile.APIKey) != ""
			} else {
				profile.CredentialPresent = candidate.Provider == AIProviderCodex
			}
			profiles[candidate.Provider] = profile
		}
	}
	if len(profiles) > 0 && h.validateSharedAI == nil {
		http.Error(w, `{"error":"AI settings validation is unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	for _, profile := range profiles {
		if err := h.validateSharedAI(r.Context(), profile); err != nil {
			log.Printf("credentials: shared AI validation failed provider=%q model=%q: %v", profile.Config.Provider, profile.Config.Model, secrets.RedactError(err))
			http.Error(w, `{"error":"The selected AI provider and model could not complete a test message. Nothing was saved."}`, http.StatusUnprocessableEntity)
			return
		}
	}

	if err := h.applyUpdate(body, candidate, providerSet || modelSet, healthEnabled, healthSet); err != nil {
		http.Error(w, `{"error":"failed to save settings"}`, http.StatusInternalServerError)
		return
	}

	h.registry.Invalidate()
	if mustTestSelected && h.sharedAIValidated != nil {
		h.sharedAIValidated(candidate)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Handler) applyUpdate(body map[string]string, config AIConfig, configSet bool, healthEnabled, healthSet bool) error {
	tx, err := h.registry.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, key := range AllKeys {
		value := strings.TrimSpace(body[key])
		if value == "" {
			continue
		}
		if isSecretKey(key) {
			value, err = h.registry.cipher.Encrypt(value)
			if err != nil {
				return fmt.Errorf("encrypt %s: %w", key, err)
			}
		}
		if _, err := tx.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", key, value); err != nil {
			return err
		}
	}
	if configSet {
		for key, value := range map[string]string{KeyAIProvider: config.Provider, KeyAIModel: config.Model} {
			if _, err := tx.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", key, value); err != nil {
				return err
			}
		}
	}
	if healthSet {
		if _, err := tx.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", KeyAIHealthCheckEnabled, strconv.FormatBool(healthEnabled)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Delete removes a single credential.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	h.updateMu.Lock()
	defer h.updateMu.Unlock()
	key := chi.URLParam(r, "key")

	valid := false
	for _, k := range AllKeys {
		if k == key {
			valid = true
			break
		}
	}
	if !valid {
		http.Error(w, `{"error":"unknown credential key"}`, http.StatusBadRequest)
		return
	}

	if err := h.registry.DeleteCredential(key); err != nil {
		http.Error(w, `{"error":"failed to delete credential"}`, http.StatusInternalServerError)
		return
	}

	h.registry.Invalidate()
	w.WriteHeader(http.StatusNoContent)
}
