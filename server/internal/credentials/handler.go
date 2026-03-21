package credentials

import (
	"encoding/json"
	"net/http"

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
	status := make(map[string]bool, len(AllKeys))
	for _, key := range AllKeys {
		status[key] = h.registry.IsConfigured(key)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// Update sets one or more credentials. Only non-empty fields are written.
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

	for key, value := range body {
		if !valid[key] {
			http.Error(w, `{"error":"unknown credential key: `+key+`"}`, http.StatusBadRequest)
			return
		}
		if value == "" {
			continue
		}
		if err := h.registry.SetCredential(key, value); err != nil {
			http.Error(w, `{"error":"failed to save credential"}`, http.StatusInternalServerError)
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
