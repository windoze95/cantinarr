package instance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/qbittorrent"
	"github.com/windoze95/cantinarr-server/internal/sabnzbd"
)

// allowedServiceTypes is the set of supported service types.
var allowedServiceTypes = map[string]bool{
	"radarr":      true,
	"sonarr":      true,
	"sabnzbd":     true,
	"qbittorrent": true,
}

// instanceResponse is the JSON shape returned to clients — API keys and
// passwords are write-only.
type instanceResponse struct {
	ID          string `json:"id"`
	ServiceType string `json:"service_type"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Username    string `json:"username,omitempty"`
	IsDefault   bool   `json:"is_default"`
	SortOrder   int    `json:"sort_order"`
}

func toResponse(inst *Instance) instanceResponse {
	return instanceResponse{
		ID:          inst.ID,
		ServiceType: inst.ServiceType,
		Name:        inst.Name,
		URL:         inst.URL,
		Username:    inst.Username,
		IsDefault:   inst.IsDefault,
		SortOrder:   inst.SortOrder,
	}
}

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
	resp := make([]instanceResponse, 0, len(instances))
	for _, inst := range instances {
		resp = append(resp, toResponse(&inst))
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// Create adds a new service instance.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var inst Instance
	if err := json.NewDecoder(r.Body).Decode(&inst); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if !allowedServiceTypes[inst.ServiceType] {
		http.Error(w, `{"error":"service_type must be one of 'radarr', 'sonarr', 'sabnzbd', 'qbittorrent'"}`, http.StatusBadRequest)
		return
	}
	if err := validateRequiredFields(&inst); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
		return
	}

	// Normalize URL
	inst.URL = strings.TrimRight(inst.URL, "/")

	// Validate reachability/credentials against the actual service
	if err := validateConnection(&inst); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"connection test failed: %s"}`, err), http.StatusBadRequest)
		return
	}

	if err := h.store.Create(&inst); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(toResponse(&inst))
}

// Update modifies an existing service instance.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "instanceID")

	existing, err := h.store.Get(instanceID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}
	if existing == nil {
		http.Error(w, `{"error":"instance not found"}`, http.StatusNotFound)
		return
	}

	var inst Instance
	if err := json.NewDecoder(r.Body).Decode(&inst); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	inst.ID = instanceID
	// Service type is immutable; validate against the stored type.
	inst.ServiceType = existing.ServiceType

	if err := validateRequiredFields(&inst); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
		return
	}

	inst.URL = strings.TrimRight(inst.URL, "/")

	if err := validateConnection(&inst); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"connection test failed: %s"}`, err), http.StatusBadRequest)
		return
	}

	if err := h.store.Update(&inst); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}

	h.registry.InvalidateClient(instanceID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toResponse(&inst))
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

// validateRequiredFields enforces per-service-type required fields.
func validateRequiredFields(inst *Instance) error {
	if inst.Name == "" || inst.URL == "" {
		return fmt.Errorf("name and url are required")
	}
	switch inst.ServiceType {
	case "qbittorrent":
		if inst.Username == "" || inst.Password == "" {
			return fmt.Errorf("username and password are required for qbittorrent")
		}
	default: // radarr, sonarr, sabnzbd
		if inst.APIKey == "" {
			return fmt.Errorf("name, url, and api_key are required")
		}
	}
	return nil
}

// validateConnection performs a service-type-specific connectivity check.
func validateConnection(inst *Instance) error {
	switch inst.ServiceType {
	case "radarr", "sonarr":
		return validateArrURL(inst.URL, inst.APIKey)
	case "sabnzbd":
		_, err := sabnzbd.NewClient(inst.URL, inst.APIKey).Version()
		return err
	case "qbittorrent":
		client := qbittorrent.NewClient(inst.URL, inst.Username, inst.Password)
		if err := client.Login(); err != nil {
			return err
		}
		_, err := client.Version()
		return err
	default:
		return fmt.Errorf("unknown service type: %s", inst.ServiceType)
	}
}

// validateArrURL checks that a Radarr/Sonarr instance is reachable by hitting
// its system/status endpoint.
func validateArrURL(baseURL, apiKey string) error {
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
