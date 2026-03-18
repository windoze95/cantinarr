package instance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// Handler provides REST endpoints for instance CRUD.
type Handler struct {
	store    *Store
	registry *Registry
}

// NewHandler creates a new instance handler.
func NewHandler(store *Store, registry *Registry) *Handler {
	return &Handler{store: store, registry: registry}
}

// List returns all service instances.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	instances, err := h.store.ListAll()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}
	if instances == nil {
		instances = []Instance{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(instances)
}

// Create adds a new service instance.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var inst Instance
	if err := json.NewDecoder(r.Body).Decode(&inst); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if inst.ServiceType != "radarr" && inst.ServiceType != "sonarr" {
		http.Error(w, `{"error":"service_type must be 'radarr' or 'sonarr'"}`, http.StatusBadRequest)
		return
	}
	if inst.Name == "" || inst.URL == "" || inst.APIKey == "" {
		http.Error(w, `{"error":"name, url, and api_key are required"}`, http.StatusBadRequest)
		return
	}

	// Normalize URL
	inst.URL = strings.TrimRight(inst.URL, "/")

	// Validate URL reachability
	if err := validateInstanceURL(inst.URL, inst.APIKey); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"connection test failed: %s"}`, err), http.StatusBadRequest)
		return
	}

	if err := h.store.Create(&inst); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(inst)
}

// Update modifies an existing service instance.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "instanceID")

	var inst Instance
	if err := json.NewDecoder(r.Body).Decode(&inst); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	inst.ID = instanceID

	if inst.Name == "" || inst.URL == "" || inst.APIKey == "" {
		http.Error(w, `{"error":"name, url, and api_key are required"}`, http.StatusBadRequest)
		return
	}

	inst.URL = strings.TrimRight(inst.URL, "/")

	if err := validateInstanceURL(inst.URL, inst.APIKey); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"connection test failed: %s"}`, err), http.StatusBadRequest)
		return
	}

	if err := h.store.Update(&inst); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}

	h.registry.InvalidateClient(instanceID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(inst)
}

// Delete removes a service instance.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "instanceID")

	if err := h.store.Delete(instanceID); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}

	h.registry.InvalidateClient(instanceID)

	w.WriteHeader(http.StatusNoContent)
}

// validateInstanceURL checks that the instance is reachable by hitting its system/status endpoint.
func validateInstanceURL(baseURL, apiKey string) error {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", baseURL+"/api/v3/system/status", nil)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	req.Header.Set("X-Api-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return fmt.Errorf("invalid API key")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}
	return nil
}
