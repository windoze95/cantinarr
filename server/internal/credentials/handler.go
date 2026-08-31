package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

const (
	maxCredentialSettingsBody = 128 << 10
	maxAIModelLength          = 256
	maxAIKeyLength            = 32 << 10
	maxAIBaseURLLength        = 2048
)

// Handler provides admin-only REST endpoints for credential management.
type Handler struct {
	registry            *Registry
	sharedAIConfigured  func() bool
	validateSharedAI    func(context.Context, AIProfile) error
	sharedAIValidated   func(AIConfig)
	authorizePermission auth.PermissionAuthorizer
	updateMu            sync.Mutex
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

// SetPermissionAuthorizer supplies the authoritative user/device permission
// check repeated after a provider probe and immediately before persistence.
func (h *Handler) SetPermissionAuthorizer(authorize auth.PermissionAuthorizer) {
	h.authorizePermission = authorize
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
	// Distinct from the per-key booleans: whether TMDB is currently running on
	// the built-in public token (no admin token stored).
	status["tmdb_using_builtin"] = h.registry.TMDBUsingBuiltIn()
	status["trakt_using_builtin"] = h.registry.TraktUsingBuiltIn()
	configured := h.registry.IsAIConfigured()
	if h.sharedAIConfigured != nil {
		configured = h.sharedAIConfigured()
	}
	config := h.registry.GetAIConfig()
	status["ai"] = map[string]any{
		"config": config,
		// Flat sibling of config on purpose: AIConfig also serializes into
		// non-admin payloads, and a LAN endpoint belongs only in this
		// admin-gated response.
		// Flat siblings of config on purpose: AIConfig also serializes into
		// non-admin payloads, and endpoint configuration belongs only in this
		// admin-gated response.
		"openai_reasoning_effort":       strings.TrimSpace(h.registry.GetSetting(KeyOpenAIReasoningEffort)),
		"local_openai_base_url":         strings.TrimSpace(h.registry.GetSetting(KeyLocalOpenAIBaseURL)),
		"local_openai_reasoning_effort": strings.TrimSpace(h.registry.GetSetting(KeyLocalOpenAIReasoningEffort)),
		"providers":                     AIProviders,
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
		writeJSONError(w, "invalid request body", http.StatusBadRequest)
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
	valid[KeyOpenAIReasoningEffort] = true
	valid[KeyLocalOpenAIBaseURL] = true
	valid[KeyLocalOpenAIReasoningEffort] = true

	for key := range body {
		if !valid[key] {
			writeJSONError(w, "unknown credential key: "+key, http.StatusBadRequest)
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
			writeJSONError(w, "unknown AI provider", http.StatusBadRequest)
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
			writeJSONError(w, "AI model is too long", http.StatusBadRequest)
			return
		}
		candidate = AIConfig{Provider: provider, Model: model}
	}

	healthEnabled := h.registry.AIHealthCheckEnabled()
	healthValue, healthSet := body[KeyAIHealthCheckEnabled]
	if healthSet {
		parsed, err := strconv.ParseBool(strings.TrimSpace(healthValue))
		if err != nil {
			writeJSONError(w, "ai_health_check_enabled must be true or false", http.StatusBadRequest)
			return
		}
		healthEnabled = parsed
	}

	// The effective endpoint settings for this save: the body's value when a
	// key is present (empty string is a deliberate clear), else the stored
	// value. Candidate profiles must always carry the effective values so a
	// key rotation with no endpoint fields still validates against the
	// configured endpoint, not the provider default. The pairs are
	// provider-scoped: hosted openai and the local provider never share a
	// slot.
	openaiEndpoint, ok := h.effectiveEndpointSettings(w, body, "", KeyOpenAIReasoningEffort)
	if !ok {
		return
	}
	localEndpoint, ok := h.effectiveEndpointSettings(w, body, KeyLocalOpenAIBaseURL, KeyLocalOpenAIReasoningEffort)
	if !ok {
		return
	}
	// The local provider is unusable without an endpoint and has no model
	// catalog to fall back on: selecting it demands both, explicitly.
	if candidate.Provider == AIProviderLocalOpenAI {
		if localEndpoint.baseURL == "" {
			writeJSONError(w, "local_openai_base_url is required for the local provider", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(candidate.Model) == "" {
			writeJSONError(w, "a model ID is required for the local provider", http.StatusBadRequest)
			return
		}
	}

	profiles := make(map[string]AIProfile)
	for _, option := range AIProviders {
		if option.CredentialKey == "" {
			continue
		}
		if value := strings.TrimSpace(body[option.CredentialKey]); value != "" {
			if len(value) > maxAIKeyLength {
				writeJSONError(w, "AI credential is too long", http.StatusBadRequest)
				return
			}
			if option.ID == AIProviderLocalOpenAI && option.ID != candidate.Provider {
				// The optional local key cannot be probed without the local
				// endpoint and an explicit model, which only a local
				// candidate carries. It persists now and is proven the
				// moment the local provider is selected.
				body[option.CredentialKey] = value
				continue
			}
			config := AIConfig{Provider: option.ID, Model: DefaultAIModel(option.ID)}
			if option.ID == candidate.Provider {
				config.Model = candidate.Model
			}
			profile := AIProfile{Config: config, APIKey: value, CredentialPresent: true}
			switch option.ID {
			case AIProviderOpenAI:
				profile.ReasoningEffort = openaiEndpoint.effort
			case AIProviderLocalOpenAI:
				profile.BaseURL = localEndpoint.baseURL
				profile.ReasoningEffort = localEndpoint.effort
			}
			profiles[option.ID] = profile
			body[option.CredentialKey] = value
		}
	}
	mustTestSelected := providerSet || modelSet || (healthSet && healthEnabled && !h.registry.AIHealthCheckEnabled())
	if key := AIKeyCredentialKey(candidate.Provider); key != "" && strings.TrimSpace(body[key]) != "" {
		mustTestSelected = true
	}
	// An endpoint change (or clear) alone must re-prove the selected
	// provider against the new configuration. With another provider
	// selected the values persist untested; selecting the provider later
	// forces the probe via providerSet.
	if openaiEndpoint.effortSet && candidate.Provider == AIProviderOpenAI {
		mustTestSelected = true
	}
	if (localEndpoint.baseURLSet || localEndpoint.effortSet) && candidate.Provider == AIProviderLocalOpenAI {
		mustTestSelected = true
	}
	if mustTestSelected {
		profile, ok := profiles[candidate.Provider]
		if !ok {
			profile = AIProfile{Config: candidate}
			if key := AIKeyCredentialKey(candidate.Provider); key != "" {
				profile.APIKey = h.registry.GetCredential(key)
				profile.CredentialPresent = strings.TrimSpace(profile.APIKey) != "" || AIProviderKeyOptional(candidate.Provider)
			} else {
				profile.CredentialPresent = IsOAuthAIProvider(candidate.Provider)
			}
			switch candidate.Provider {
			case AIProviderOpenAI:
				profile.ReasoningEffort = openaiEndpoint.effort
			case AIProviderLocalOpenAI:
				profile.BaseURL = localEndpoint.baseURL
				profile.ReasoningEffort = localEndpoint.effort
			}
			profiles[candidate.Provider] = profile
		}
	}
	if len(profiles) > 0 && h.validateSharedAI == nil {
		writeJSONError(w, "AI settings validation is unavailable", http.StatusServiceUnavailable)
		return
	}
	for _, profile := range profiles {
		if err := h.validateSharedAI(r.Context(), profile); err != nil {
			log.Printf("credentials: shared AI validation failed provider=%q: %s", profile.Config.Provider, credentialValidationDiagnostic(err))
			writeCredentialValidationError(w, err)
			return
		}
	}
	if len(profiles) > 0 && !h.reauthorizeSharedAIWrite(w, r) {
		return
	}

	plainWrites := []plainSettingWrite{
		{key: KeyOpenAIReasoningEffort, value: openaiEndpoint.effort, set: openaiEndpoint.effortSet},
		{key: KeyLocalOpenAIBaseURL, value: localEndpoint.baseURL, set: localEndpoint.baseURLSet},
		{key: KeyLocalOpenAIReasoningEffort, value: localEndpoint.effort, set: localEndpoint.effortSet},
	}
	if err := h.applyUpdate(body, candidate, providerSet || modelSet, healthEnabled, healthSet, plainWrites); err != nil {
		writeJSONError(w, "failed to save settings", http.StatusInternalServerError)
		return
	}

	h.registry.Invalidate()
	if mustTestSelected && h.sharedAIValidated != nil {
		h.sharedAIValidated(candidate)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Handler) reauthorizeSharedAIWrite(w http.ResponseWriter, r *http.Request) bool {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSONError(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	if h.authorizePermission == nil {
		writeJSONError(w, "credential authorization is temporarily unavailable", http.StatusServiceUnavailable)
		return false
	}
	if err := h.authorizePermission(r.Context(), claims.UserID, claims.DeviceID, auth.PermissionCredentialsManage); err != nil {
		if errors.Is(err, auth.ErrAuthUnavailable) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			writeJSONError(w, "credential authorization is temporarily unavailable", http.StatusServiceUnavailable)
		} else {
			writeJSONError(w, "permission denied", http.StatusForbidden)
		}
		return false
	}
	return true
}

// writeJSONError sends {"error": message} with a JSON content type. http.Error
// would label the body text/plain, which the app's client refuses to decode —
// every failure on this handler then rendered as a generic "Failed to save
// settings." instead of the real reason (an expired key sat behind exactly
// that copy in the #497 report).
func writeJSONError(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func writeCredentialValidationError(w http.ResponseWriter, err error) {
	message := "The selected AI provider and model could not complete a test message. Nothing was saved."
	var safe interface{ SafeUserMessage() string }
	if errors.As(err, &safe) && strings.TrimSpace(safe.SafeUserMessage()) != "" {
		message = safe.SafeUserMessage()
	}
	writeJSONError(w, message, http.StatusUnprocessableEntity)
}

func credentialValidationDiagnostic(err error) string {
	if err == nil {
		return ""
	}
	var safe interface{ SafeDiagnostic() string }
	if errors.As(err, &safe) && strings.TrimSpace(safe.SafeDiagnostic()) != "" {
		return safe.SafeDiagnostic()
	}
	return secrets.RedactError(err).Error()
}

// endpointSettings is the effective provider-scoped base-URL/effort pair for
// one save, with per-field presence so an untouched field never rewrites the
// stored row.
type endpointSettings struct {
	baseURL    string
	baseURLSet bool
	effort     string
	effortSet  bool
}

// effectiveEndpointSettings resolves one provider's endpoint pair from the
// request body over the stored values, writing the HTTP error itself and
// returning ok=false when a supplied value is invalid.
func (h *Handler) effectiveEndpointSettings(w http.ResponseWriter, body map[string]string, baseURLKey, effortKey string) (endpointSettings, bool) {
	settings := endpointSettings{effort: strings.TrimSpace(h.registry.GetSetting(effortKey))}
	if baseURLKey != "" {
		settings.baseURL = strings.TrimSpace(h.registry.GetSetting(baseURLKey))
	}
	if value, set := body[baseURLKey]; baseURLKey != "" && set {
		settings.baseURLSet = true
		settings.baseURL = strings.TrimSpace(value)
		if len(settings.baseURL) > maxAIBaseURLLength {
			writeJSONError(w, baseURLKey+" is too long", http.StatusBadRequest)
			return settings, false
		}
		if settings.baseURL != "" {
			parsed, err := url.Parse(settings.baseURL)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
				writeJSONError(w, baseURLKey+" must be an absolute http or https URL", http.StatusBadRequest)
				return settings, false
			}
		}
	}
	if value, set := body[effortKey]; set {
		settings.effortSet = true
		settings.effort = strings.ToLower(strings.TrimSpace(value))
		if !IsValidAIReasoningEffort(settings.effort) {
			writeJSONError(w, effortKey+" must be one of none, minimal, low, medium, high, or empty for auto", http.StatusBadRequest)
			return settings, false
		}
	}
	return settings, true
}

// plainSettingWrite is one plaintext settings row to persist in applyUpdate:
// empty value deletes the row (deliberate clear), set=false skips it.
type plainSettingWrite struct {
	key   string
	value string
	set   bool
}

func (h *Handler) applyUpdate(body map[string]string, config AIConfig, configSet bool, healthEnabled, healthSet bool, plain []plainSettingWrite) error {
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
	for _, write := range plain {
		if !write.set {
			continue
		}
		// Stored plaintext on purpose: endpoint configuration is not a
		// secret and stays out of the AllKeys encryption loop above. An
		// empty value is a deliberate clear.
		if write.value == "" {
			if _, err := tx.Exec("DELETE FROM settings WHERE key = ?", write.key); err != nil {
				return err
			}
		} else if _, err := tx.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", write.key, write.value); err != nil {
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
		writeJSONError(w, "unknown credential key", http.StatusBadRequest)
		return
	}

	if err := h.registry.DeleteCredential(key); err != nil {
		writeJSONError(w, "failed to delete credential", http.StatusInternalServerError)
		return
	}

	h.registry.Invalidate()
	w.WriteHeader(http.StatusNoContent)
}
