package instance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/nzbget"
	"github.com/windoze95/cantinarr-server/internal/qbittorrent"
	"github.com/windoze95/cantinarr-server/internal/sabnzbd"
	"github.com/windoze95/cantinarr-server/internal/tautulli"
	"github.com/windoze95/cantinarr-server/internal/transmission"
)

// allowedServiceTypes is the set of supported service types.
var allowedServiceTypes = map[string]bool{
	"radarr":       true,
	"sonarr":       true,
	"chaptarr":     true,
	"sabnzbd":      true,
	"qbittorrent":  true,
	"nzbget":       true,
	"transmission": true,
	"tautulli":     true,
}

// instanceResponse is the JSON shape returned to clients. All credentials are
// write-only, including the token used to authenticate arr webhook callbacks.
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
	store        *Store
	registry     *Registry
	webhookMu    sync.Mutex
	webhookLocks map[string]*sync.Mutex
	publicURL    string
}

// NewHandler creates a new instance handler.
func NewHandler(store *Store, registry *Registry, publicURL ...string) *Handler {
	h := &Handler{store: store, registry: registry, webhookLocks: make(map[string]*sync.Mutex)}
	if len(publicURL) > 0 {
		h.publicURL = strings.TrimRight(publicURL[0], "/")
	}
	return h
}

func (h *Handler) lockWebhookConfiguration(instanceID string) func() {
	h.webhookMu.Lock()
	lock := h.webhookLocks[instanceID]
	if lock == nil {
		lock = &sync.Mutex{}
		h.webhookLocks[instanceID] = lock
	}
	h.webhookMu.Unlock()
	lock.Lock()
	return lock.Unlock
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
		http.Error(w, `{"error":"service_type must be one of 'radarr', 'sonarr', 'chaptarr', 'sabnzbd', 'qbittorrent', 'nzbget', 'transmission', 'tautulli'"}`, http.StatusBadRequest)
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

	// Credentials are write-only: a blank value keeps the stored one.
	if inst.APIKey == "" {
		inst.APIKey = existing.APIKey
	}
	if inst.Username == "" {
		inst.Username = existing.Username
	}
	if inst.Password == "" {
		inst.Password = existing.Password
	}

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

// TestConnection validates a candidate configuration's reachability and
// credentials from the server — the host that actually dials instance URLs,
// so cluster-internal names the admin's device cannot resolve still test
// truthfully — without persisting anything. For an existing instance (id set
// in the body), blank credentials fall back to the stored ones, mirroring
// Update's write-only semantics.
func (h *Handler) TestConnection(w http.ResponseWriter, r *http.Request) {
	var inst Instance
	if err := json.NewDecoder(r.Body).Decode(&inst); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if inst.ID != "" {
		existing, err := h.store.Get(inst.ID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		if existing == nil {
			http.Error(w, `{"error":"instance not found"}`, http.StatusNotFound)
			return
		}
		inst.ServiceType = existing.ServiceType
		if inst.APIKey == "" {
			inst.APIKey = existing.APIKey
		}
		if inst.Username == "" {
			inst.Username = existing.Username
		}
		if inst.Password == "" {
			inst.Password = existing.Password
		}
	}

	if !allowedServiceTypes[inst.ServiceType] {
		http.Error(w, `{"error":"service_type must be one of 'radarr', 'sonarr', 'chaptarr', 'sabnzbd', 'qbittorrent', 'nzbget', 'transmission', 'tautulli'"}`, http.StatusBadRequest)
		return
	}
	// The test doesn't need a name; default it so the shared validation only
	// enforces the URL and credentials.
	if inst.Name == "" {
		inst.Name = inst.ServiceType
	}
	if err := validateRequiredFields(&inst); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
		return
	}

	inst.URL = strings.TrimRight(inst.URL, "/")

	if err := validateConnection(&inst); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"connection test failed: %s"}`, err), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
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

// GetUserDefaultInstances returns a user's per-user default instance overrides
// as a {service_type: instance_id} map (admin-only). Service types absent from
// the map inherit the global default.
func (h *Handler) GetUserDefaultInstances(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid user id"}`, http.StatusBadRequest)
		return
	}
	defaults, err := h.store.ListUserDefaults(userID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}
	if defaults == nil {
		defaults = map[string]string{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(defaults)
}

// UpdateUserDefaultInstances sets or clears a user's per-user default instances
// (admin-only). Body is a {service_type: instance_id|null} map; a null/empty
// value clears that override (for chaptarr, this revokes access). Each instance
// id must exist and match its service type. Returns the updated map.
func (h *Handler) UpdateUserDefaultInstances(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid user id"}`, http.StatusBadRequest)
		return
	}
	var body map[string]*string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	// Reject unknown service types up front so a typo never partially applies.
	for serviceType := range body {
		if !allowedServiceTypes[serviceType] {
			http.Error(w, fmt.Sprintf(`{"error":"unknown service_type: %s"}`, serviceType), http.StatusBadRequest)
			return
		}
	}
	for serviceType, instanceID := range body {
		if instanceID == nil || *instanceID == "" {
			if err := h.store.ClearUserDefault(userID, serviceType); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
				return
			}
			continue
		}
		if err := h.store.SetUserDefault(userID, serviceType, *instanceID); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
			return
		}
	}
	defaults, err := h.store.ListUserDefaults(userID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}
	if defaults == nil {
		defaults = map[string]string{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(defaults)
}

// instanceUserPin is one user's per-user default row within a service type,
// as served by the instance-centric assignment endpoints.
type instanceUserPin struct {
	UserID     int64  `json:"user_id"`
	InstanceID string `json:"instance_id"`
}

// writeInstanceUsers responds with every per-user default pin for the service
// type — not just the addressed instance — so the admin UI can also show which
// users are currently pinned to a sibling instance.
func (h *Handler) writeInstanceUsers(w http.ResponseWriter, serviceType string) {
	pins, err := h.store.ListTypeUserDefaults(serviceType)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}
	resp := make([]instanceUserPin, 0, len(pins))
	for userID, instanceID := range pins {
		resp = append(resp, instanceUserPin{UserID: userID, InstanceID: instanceID})
	}
	sort.Slice(resp, func(i, j int) bool { return resp[i].UserID < resp[j].UserID })
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// GetInstanceUsers returns the per-user default pins for the addressed
// instance's service type (admin-only).
func (h *Handler) GetInstanceUsers(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "instanceID")
	serviceType, err := h.store.ServiceTypeOf(instanceID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}
	if serviceType == "" {
		http.Error(w, `{"error":"instance not found"}`, http.StatusNotFound)
		return
	}
	h.writeInstanceUsers(w, serviceType)
}

// UpdateInstanceUsers pins the addressed instance as the per-user default for
// exactly the posted user ids (admin-only). Users previously pinned to this
// instance but absent from the list revert to the global default (for
// chaptarr: access revoked). Returns the updated pins for the service type.
func (h *Handler) UpdateInstanceUsers(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "instanceID")
	serviceType, err := h.store.ServiceTypeOf(instanceID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}
	if serviceType == "" {
		http.Error(w, `{"error":"instance not found"}`, http.StatusNotFound)
		return
	}
	var body struct {
		UserIDs []int64 `json:"user_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if err := h.store.SetInstanceUsers(instanceID, body.UserIDs); err != nil {
		// Covers unknown user ids too (the user_id foreign key rejects them).
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
		return
	}
	h.writeInstanceUsers(w, serviceType)
}

// validateRequiredFields enforces per-service-type required fields.
func validateRequiredFields(inst *Instance) error {
	if inst.Name == "" || inst.URL == "" {
		return fmt.Errorf("name and url are required")
	}
	parsed, err := url.Parse(inst.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("url must be an absolute http or https URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("url must not contain credentials, a query string, or a fragment")
	}
	switch inst.ServiceType {
	case "qbittorrent", "nzbget":
		if inst.Username == "" || inst.Password == "" {
			return fmt.Errorf("username and password are required for %s", inst.ServiceType)
		}
	case "transmission":
		// Username/password are optional: Transmission RPC may run without auth.
	default: // radarr, sonarr, chaptarr, sabnzbd, tautulli
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
		return validateArrURL(inst.URL, inst.APIKey, "v3")
	case "chaptarr":
		// Chaptarr is a Readarr fork speaking the Servarr /api/v1 API.
		return validateArrURL(inst.URL, inst.APIKey, "v1")
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
	case "nzbget":
		_, err := nzbget.NewClient(inst.URL, inst.Username, inst.Password).Version()
		return err
	case "transmission":
		_, err := transmission.NewClient(inst.URL, inst.Username, inst.Password).SessionGet()
		return err
	case "tautulli":
		_, err := tautulli.NewClient(inst.URL, inst.APIKey).GetServerInfo()
		return err
	default:
		return fmt.Errorf("unknown service type: %s", inst.ServiceType)
	}
}

// validateArrURL checks that a Servarr instance (Radarr/Sonarr on v3, Chaptarr
// on v1) is reachable by hitting its system/status endpoint.
func validateArrURL(baseURL, apiKey, apiVersion string) error {
	client := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequest("GET", baseURL+"/api/"+apiVersion+"/system/status", nil)
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
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		// Admin-only surface: naming the Location makes http→https fronting
		// and URL-base redirects self-diagnosing from the connection test.
		return fmt.Errorf("server returned redirect status %d to %q (redirects are not followed; use the service's final URL)", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}
	return nil
}
