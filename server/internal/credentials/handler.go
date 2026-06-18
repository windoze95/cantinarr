package credentials

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Handler provides admin-only REST endpoints for credential management.
type Handler struct {
	registry *Registry
}

// NewHandler creates a new credentials handler.
func NewHandler(registry *Registry) *Handler {
	return &Handler{registry: registry}
}

// Get returns which credentials are configured (booleans, never values).
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	status := make(map[string]any, len(AllKeys)+1)
	credentials := make(map[string]bool, len(AllKeys))
	for _, key := range AllKeys {
		configured := h.registry.IsConfigured(key)
		status[key] = configured
		credentials[key] = configured
	}
	status["credentials"] = credentials
	status["ai"] = map[string]any{
		"config":    h.registry.GetAIConfig(),
		"providers": AIProviders,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// Update sets one or more credentials and non-secret AI settings. Only
// non-empty fields are written.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	valid := make(map[string]bool, len(AllKeys))
	for _, k := range AllKeys {
		valid[k] = true
	}
	valid[KeyAIProvider] = true
	valid[KeyAIModel] = true

	for key, value := range body {
		if !valid[key] {
			http.Error(w, `{"error":"unknown credential key: `+key+`"}`, http.StatusBadRequest)
			return
		}
		if key == KeyAIProvider || key == KeyAIModel {
			continue
		}
		if value == "" {
			continue
		}
		if err := h.registry.SetCredential(key, value); err != nil {
			http.Error(w, `{"error":"failed to save credential"}`, http.StatusInternalServerError)
			return
		}
	}

	provider, providerSet := body[KeyAIProvider]
	model, modelSet := body[KeyAIModel]
	if providerSet || modelSet {
		current := h.registry.GetAIConfig()
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
		if err := h.registry.SetAIConfig(provider, model); err != nil {
			http.Error(w, `{"error":"failed to save AI settings"}`, http.StatusInternalServerError)
			return
		}
	}

	h.registry.Invalidate()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// Delete removes a single credential.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
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
