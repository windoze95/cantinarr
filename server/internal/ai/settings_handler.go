package ai

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/codexapp"
	"github.com/windoze95/cantinarr-server/internal/credentials"
)

const (
	maxAISettingsBody = 64 << 10
	maxAIModelLength  = 256
	maxAIAPIKeyLength = 32 << 10
)

var errAISettingsAuthorizationUnavailable = errors.New("AI settings authorization is unavailable")

type updatePersonalAISettingsRequest struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	APIKey   string `json:"api_key,omitempty"`
}

type updatePersonalAICredentialRequest struct {
	APIKey string `json:"api_key"`
	Model  string `json:"model,omitempty"`
}

// AISettings returns only caller-owned credential presence plus the safe
// shared provider configuration. Shared account identity, usage, and key
// presence by provider are deliberately absent.
func (h *Handler) AISettings(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeAISettingsError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	h.writeAISettings(w, r, claims.UserID)
}

func (h *Handler) UpdateAISettings(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeAISettingsError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req updatePersonalAISettingsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAISettingsBody)).Decode(&req); err != nil {
		writeAISettingsError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Provider = strings.TrimSpace(req.Provider)
	req.Model = strings.TrimSpace(req.Model)
	req.APIKey = strings.TrimSpace(req.APIKey)
	if !credentials.IsValidAIProvider(req.Provider) || len(req.Model) > maxAIModelLength || len(req.APIKey) > maxAIAPIKeyLength {
		writeAISettingsError(w, http.StatusBadRequest, "invalid AI provider or model")
		return
	}
	if req.Model == "" {
		req.Model = credentials.DefaultAIModel(req.Provider)
	}
	h.settingsMu.Lock()
	defer h.settingsMu.Unlock()
	profile := credentials.AIProfile{Config: credentials.AIConfig{Provider: req.Provider, Model: req.Model}}
	if req.Provider == credentials.AIProviderCodex {
		if req.APIKey != "" {
			writeAISettingsError(w, http.StatusBadRequest, "OAuth providers do not accept API keys")
			return
		}
		profile.CredentialPresent = true
	} else if req.APIKey != "" {
		profile.APIKey = req.APIKey
		profile.CredentialPresent = true
	} else {
		key, found, err := h.creds.UserAICredential(claims.UserID, req.Provider)
		if err != nil {
			writeAISettingsError(w, http.StatusInternalServerError, "failed to load personal AI credential")
			return
		}
		profile.APIKey, profile.CredentialPresent = key, found
	}
	if err := h.ValidatePersonalAISettings(r.Context(), claims.UserID, profile); err != nil {
		log.Printf("personal AI validation failed user_id=%d provider=%q: %s", claims.UserID, req.Provider, AIValidationDiagnostic(err))
		writeAISettingsError(w, http.StatusUnprocessableEntity, AIValidationUserMessage(err))
		return
	}
	if !h.reauthorizePersonalAIWrite(w, r, claims) {
		return
	}
	if err := h.creds.SetUserAIProfile(claims.UserID, req.Provider, req.Model, req.APIKey); err != nil {
		writeAISettingsError(w, http.StatusInternalServerError, "failed to save personal AI settings")
		return
	}
	h.writeAISettings(w, r, claims.UserID)
}

func (h *Handler) DeleteAISettings(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeAISettingsError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	h.settingsMu.Lock()
	defer h.settingsMu.Unlock()
	setAINoStore(w)
	if err := h.creds.DeleteUserAIConfig(claims.UserID); err != nil {
		writeAISettingsError(w, http.StatusInternalServerError, "failed to disable personal AI settings")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) UpdatePersonalAICredential(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeAISettingsError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	provider := strings.TrimSpace(chi.URLParam(r, "provider"))
	if credentials.AIKeyCredentialKey(provider) == "" {
		writeAISettingsError(w, http.StatusBadRequest, "provider does not accept an API key")
		return
	}
	var req updatePersonalAICredentialRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAISettingsBody)).Decode(&req); err != nil {
		writeAISettingsError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.APIKey = strings.TrimSpace(req.APIKey)
	req.Model = strings.TrimSpace(req.Model)
	if req.APIKey == "" || len(req.APIKey) > maxAIAPIKeyLength || len(req.Model) > maxAIModelLength {
		writeAISettingsError(w, http.StatusBadRequest, "api_key is required")
		return
	}
	h.settingsMu.Lock()
	defer h.settingsMu.Unlock()
	if selected, found, err := h.creds.GetUserAIConfig(claims.UserID); err != nil {
		writeAISettingsError(w, http.StatusInternalServerError, "failed to load personal AI settings")
		return
	} else if found && selected.Provider == provider {
		// A key rotation changes the active profile even though its model row is
		// untouched. Test the pair that will actually serve the next request.
		req.Model = selected.Model
	} else if req.Model == "" {
		req.Model = credentials.DefaultAIModel(provider)
	}
	profile := credentials.AIProfile{
		Config:            credentials.AIConfig{Provider: provider, Model: req.Model},
		APIKey:            req.APIKey,
		CredentialPresent: true,
	}
	if err := h.ValidatePersonalAISettings(r.Context(), claims.UserID, profile); err != nil {
		log.Printf("personal AI credential validation failed user_id=%d provider=%q: %s", claims.UserID, provider, AIValidationDiagnostic(err))
		writeAISettingsError(w, http.StatusUnprocessableEntity, AIValidationUserMessage(err))
		return
	}
	if !h.reauthorizePersonalAIWrite(w, r, claims) {
		return
	}
	setAINoStore(w)
	if err := h.creds.SetUserAICredential(claims.UserID, provider, req.APIKey); err != nil {
		writeAISettingsError(w, http.StatusInternalServerError, "failed to save personal AI credential")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) authorizeSettingsWrite(ctx context.Context, userID int64, deviceID string, permission auth.Permission) error {
	if h.authorizePermission == nil {
		return errAISettingsAuthorizationUnavailable
	}
	return h.authorizePermission(ctx, userID, deviceID, permission)
}

func (h *Handler) reauthorizePersonalAIWrite(w http.ResponseWriter, r *http.Request, claims *auth.Claims) bool {
	err := h.authorizeSettingsWrite(r.Context(), claims.UserID, claims.DeviceID, auth.PermissionAIChat)
	if err == nil {
		return true
	}
	if errors.Is(err, auth.ErrAuthUnavailable) || errors.Is(err, errAISettingsAuthorizationUnavailable) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		writeAISettingsError(w, http.StatusServiceUnavailable, "AI settings authorization is temporarily unavailable")
	} else {
		writeAISettingsError(w, http.StatusForbidden, "permission denied")
	}
	return false
}

func (h *Handler) DeletePersonalAICredential(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeAISettingsError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	provider := strings.TrimSpace(chi.URLParam(r, "provider"))
	if credentials.AIKeyCredentialKey(provider) == "" {
		writeAISettingsError(w, http.StatusBadRequest, "provider does not accept an API key")
		return
	}
	h.settingsMu.Lock()
	defer h.settingsMu.Unlock()
	setAINoStore(w)
	if err := h.creds.DeleteUserAICredential(claims.UserID, provider); err != nil {
		writeAISettingsError(w, http.StatusInternalServerError, "failed to delete personal AI credential")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) writeAISettings(w http.ResponseWriter, r *http.Request, userID int64) {
	setAINoStore(w)
	personalConfig, selected, err := h.creds.GetUserAIConfig(userID)
	personalStorageOK := err == nil
	// An invalid-but-present personal selection is intentionally repairable:
	// return its non-secret provider/model plus storage_error so the caller can
	// delete or replace it. A general lookup failure still fails closed.
	if err != nil && !selected {
		writeAISettingsError(w, http.StatusInternalServerError, "failed to load personal AI settings")
		return
	}
	personalCredentials := map[string]bool{}
	for _, provider := range []string{
		credentials.AIProviderAnthropic,
		credentials.AIProviderOpenAI,
		credentials.AIProviderGemini,
	} {
		configured, err := h.creds.UserAICredentialConfigured(userID, provider)
		if err != nil {
			writeAISettingsError(w, http.StatusInternalServerError, "failed to load personal AI settings")
			return
		}
		personalCredentials[provider] = configured
	}
	codexConnected := false
	if h.codex != nil {
		codexConnected, err = h.codex.AccountExists(codexapp.PersonalAccount(userID))
		if err != nil {
			writeAISettingsError(w, http.StatusInternalServerError, "failed to load personal AI settings")
			return
		}
	}
	personalCredentials[credentials.AIProviderCodex] = codexConnected

	sharedProfile, granted, err := h.creds.LoadSharedAIProfileForUser(r.Context(), userID)
	sharedStorageOK := err == nil
	sharedConfigured := sharedStorageOK && sharedProfile.CredentialPresent
	if sharedProfile.Config.Provider == credentials.AIProviderCodex {
		sharedConfigured = false
		if h.codex != nil && h.codex.Available() {
			sharedConfigured, err = h.codex.AccountExists(codexapp.SharedAccount())
			if err != nil {
				writeAISettingsError(w, http.StatusInternalServerError, "failed to load shared AI settings")
				return
			}
		}
	}
	resolved := h.resolveAI(r.Context(), userID)
	var personalConfigJSON any
	if selected {
		personalConfigJSON = personalConfig
	}
	response := map[string]any{
		"providers": credentials.AIProviders,
		"personal": map[string]any{
			"selected":    selected,
			"config":      personalConfigJSON,
			"credentials": personalCredentials,
			"reason": func() string {
				if !personalStorageOK {
					return "storage_error"
				}
				return ""
			}(),
		},
		"shared": map[string]any{
			"granted":    granted,
			"configured": sharedConfigured,
			"config":     sharedProfile.Config,
			"reason": func() string {
				if !sharedStorageOK {
					return "storage_error"
				}
				return ""
			}(),
		},
		"effective": map[string]any{
			"available": resolved.Available,
			"source":    resolved.Source,
			"provider":  resolved.Provider,
			"model":     resolved.Model,
			"reason":    resolved.Reason,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func setAINoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}

func writeAISettingsError(w http.ResponseWriter, status int, message string) {
	setAINoStore(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
