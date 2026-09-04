package instance

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/mediapath"
	"github.com/windoze95/cantinarr-server/internal/nzbget"
	"github.com/windoze95/cantinarr-server/internal/plex"
	"github.com/windoze95/cantinarr-server/internal/sabnzbd"
	"github.com/windoze95/cantinarr-server/internal/transmission"
)

// allowedServiceTypes is the set of supported service types.
var allowedServiceTypes = map[string]bool{
	"radarr":       true,
	"sonarr":       true,
	"chaptarr":     true,
	"lidarr":       true,
	"sabnzbd":      true,
	"qbittorrent":  true,
	"nzbget":       true,
	"transmission": true,
	"tautulli":     true,
	"tracearr":     true,
	"jellyfin":     true,
	"emby":         true,
	"plex":         true,
}

const serviceTypeListError = `{"error":"service_type must be one of 'radarr', 'sonarr', 'chaptarr', 'lidarr', 'sabnzbd', 'qbittorrent', 'nzbget', 'transmission', 'tautulli', 'tracearr', 'jellyfin', 'emby', 'plex'"}`

// grantableServiceTypes is the subset a user can hold access-grant rows for.
// Download clients and watch-history providers (Tautulli, Tracearr) are admin
// surfaces with no per-user routing, so granting them would only invite
// confusion. For media servers a grant is
// the eligibility to create an account there.
var grantableServiceTypes = map[string]bool{
	"radarr":   true,
	"sonarr":   true,
	"chaptarr": true,
	"lidarr":   true,
	"jellyfin": true,
	"emby":     true,
	"plex":     true,
}

// instanceResponse is the JSON shape returned to clients. All credentials are
// write-only, including the token used to authenticate arr webhook callbacks.
type instanceResponse struct {
	ID          string `json:"id"`
	ServiceType string `json:"service_type"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Username    string `json:"username,omitempty"`
	// HasAPIKey says which of qBittorrent's two credential shapes is stored
	// (a key, or the username above with a password) without revealing the
	// key. Only qBittorrent rows carry it.
	HasAPIKey         bool                `json:"has_api_key,omitempty"`
	IsDefault         bool                `json:"is_default"`
	SortOrder         int                 `json:"sort_order"`
	MediaDownloads    bool                `json:"media_downloads"`
	MediaPathMappings []mediapath.Mapping `json:"media_path_mappings"`
	// MediaServerConfig is present only for media-server instances.
	MediaServerConfig *MediaServerConfig `json:"media_server_config,omitempty"`
}

type instanceRequest struct {
	Instance
	// Pointer distinguishes an old client that omitted this new field from an
	// admin explicitly sending [] to disable downloads on an existing instance.
	MediaPathMappings *[]mediapath.Mapping `json:"media_path_mappings"`
	// Same omitted-vs-cleared distinction for the media-server settings.
	MediaServerConfig *MediaServerConfig `json:"media_server_config"`
	// PlexLinkPin names an approved PIN link (PlexLinkBegin/PlexLinkCheck)
	// whose token this Plex instance should use. The token itself never
	// travels through the app.
	PlexLinkPin int64 `json:"plex_link_pin"`
}

func (h *Handler) toResponse(inst *Instance) instanceResponse {
	mappings := inst.EffectiveMediaPathMappings()
	if mappings == nil {
		mappings = []mediapath.Mapping{}
	}
	resp := instanceResponse{
		ID:                inst.ID,
		ServiceType:       inst.ServiceType,
		Name:              inst.Name,
		URL:               inst.URL,
		Username:          inst.Username,
		IsDefault:         inst.IsDefault,
		SortOrder:         inst.SortOrder,
		MediaDownloads:    inst.MediaDownloadsConfigured(h.mediaRoots),
		MediaPathMappings: mappings,
	}
	if inst.ServiceType == "qbittorrent" {
		resp.HasAPIKey = inst.APIKey != ""
	}
	if IsMediaServerType(inst.ServiceType) {
		cfg := inst.MediaServerConfig.public()
		resp.MediaServerConfig = &cfg
	}
	return resp
}

// Handler provides REST endpoints for instance CRUD.
type Handler struct {
	store          *Store
	registry       *Registry
	webhookMu      sync.Mutex
	webhookLocks   map[string]*sync.Mutex
	arrCallbackURL string
	mediaRoots     []string
	grantObserver  GrantObserver
	// sharedLibrariesObserver is told when a media server's shared-library
	// selection changes, so existing accounts follow the new set.
	sharedLibrariesObserver SharedLibrariesObserver
	// plexLinks holds approved PIN links until the admin saves the instance;
	// plexBaseURL is where the link flow talks to (plex.tv, or a test).
	plexLinks   *plexLinks
	plexBaseURL string
}

// NewHandler creates a new instance handler.
func NewHandler(store *Store, registry *Registry, arrCallbackURL ...string) *Handler {
	h := &Handler{store: store, registry: registry, webhookLocks: make(map[string]*sync.Mutex), plexLinks: newPlexLinks(), plexBaseURL: plex.BaseURL}
	if len(arrCallbackURL) > 0 {
		h.arrCallbackURL = strings.TrimRight(arrCallbackURL[0], "/")
	}
	return h
}

// SetMediaDownloadRoots supplies the deployment-owned outer filesystem
// boundary. It is called during process construction before the router starts.
func (h *Handler) SetMediaDownloadRoots(roots []string) {
	h.mediaRoots = append([]string(nil), roots...)
}

func (h *Handler) applyMediaPathMappings(inst *Instance, provided *[]mediapath.Mapping, existing *Instance) error {
	if provided == nil {
		if existing != nil {
			inst.MediaDownloadMode = existing.MediaDownloadMode
			inst.MediaPathMappings = append([]mediapath.Mapping(nil), existing.MediaPathMappings...)
		} else {
			inst.MediaDownloadMode = MediaDownloadModeDisabled
			inst.MediaPathMappings = nil
		}
		return nil
	}

	if inst.ServiceType != "radarr" && inst.ServiceType != "sonarr" && inst.ServiceType != "chaptarr" && inst.ServiceType != "lidarr" {
		if len(*provided) > 0 {
			return fmt.Errorf("media path mappings are supported only for Radarr, Sonarr, Chaptarr, and Lidarr")
		}
		inst.MediaDownloadMode = MediaDownloadModeDisabled
		inst.MediaPathMappings = nil
		return nil
	}
	// Clearing an instance must remain possible even if a deployment mount has
	// disappeared since startup. There is nothing to validate when no mapping
	// will remain.
	if len(*provided) == 0 {
		inst.MediaDownloadMode = MediaDownloadModeDisabled
		inst.MediaPathMappings = nil
		return nil
	}
	normalized, err := mediapath.Validate(*provided, h.mediaRoots)
	if err != nil {
		return fmt.Errorf("invalid media path mappings: %w", err)
	}
	inst.MediaDownloadMode = MediaDownloadModeMapped
	inst.MediaPathMappings = normalized
	return nil
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
		resp = append(resp, h.toResponse(&inst))
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// MediaRoots returns the deployment-authorized local roots to administrators
// configuring an instance. Requester config never includes these server paths.
func (h *Handler) MediaRoots(w http.ResponseWriter, _ *http.Request) {
	roots := append([]string(nil), h.mediaRoots...)
	if roots == nil {
		roots = []string{}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(roots)
}

// Create adds a new service instance.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var request instanceRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	inst := request.Instance

	if !allowedServiceTypes[inst.ServiceType] {
		http.Error(w, serviceTypeListError, http.StatusBadRequest)
		return
	}
	plexCred, err := h.applyPlexLink(&inst, request.PlexLinkPin, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
		return
	}
	applyQbittorrentAuthMode(&inst, &request.Instance)
	if err := validateRequiredFields(&inst); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
		return
	}
	if err := h.applyMediaPathMappings(&inst, request.MediaPathMappings, nil); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
		return
	}
	if err := h.applyMediaServerConfig(&inst, request.MediaServerConfig, nil); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
		return
	}
	if err := applyPlexConfig(&inst, plexCred); err != nil {
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
	json.NewEncoder(w).Encode(h.toResponse(&inst))
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

	var request instanceRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	inst := request.Instance
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
	applyQbittorrentAuthMode(&inst, &request.Instance)
	plexCred, err := h.applyPlexLink(&inst, request.PlexLinkPin, existing)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
		return
	}

	if err := validateRequiredFields(&inst); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
		return
	}
	if err := h.applyMediaPathMappings(&inst, request.MediaPathMappings, existing); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
		return
	}
	if err := h.applyMediaServerConfig(&inst, request.MediaServerConfig, existing); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
		return
	}
	if err := applyPlexConfig(&inst, plexCred); err != nil {
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

	// The shared-library selection is access, not just a default for new
	// accounts: when it changes, the accounts Cantinarr created here follow
	// it. Only on a real change, so an unrelated save costs no policy writes.
	if IsMediaServerType(inst.ServiceType) &&
		!sameLibraryIDs(existing.MediaServerConfig.LibraryIDs, inst.MediaServerConfig.LibraryIDs) {
		h.notifySharedLibrariesObserver(instanceID, inst.MediaServerConfig.LibraryIDs)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h.toResponse(&inst))
}

// TestConnection validates a candidate configuration's reachability and
// credentials from the server — the host that actually dials instance URLs,
// so cluster-internal names the admin's device cannot resolve still test
// truthfully — without persisting anything. For an existing instance (id set
// in the body), blank credentials fall back to the stored ones, mirroring
// Update's write-only semantics.
func (h *Handler) TestConnection(w http.ResponseWriter, r *http.Request) {
	inst, ok := h.resolveTestInstance(w, r)
	if !ok {
		return
	}
	if err := validateConnection(inst); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"connection test failed: %s"}`, err), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// resolveTestInstance decodes a candidate configuration for the test-style
// endpoints (TestConnection, MediaServerLibraries), applying the stored
// credential fallback and the shared validation. It writes the error response
// itself and returns ok=false when the request cannot proceed.
func (h *Handler) resolveTestInstance(w http.ResponseWriter, r *http.Request) (*Instance, bool) {
	var request instanceRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return nil, false
	}
	inst := request.Instance

	var existing *Instance
	if inst.ID != "" {
		var err error
		existing, err = h.store.Get(inst.ID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return nil, false
		}
		if existing == nil {
			http.Error(w, `{"error":"instance not found"}`, http.StatusNotFound)
			return nil, false
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
		inst.MediaServerConfig = existing.MediaServerConfig.clone()
		applyQbittorrentAuthMode(&inst, &request.Instance)
	}

	if !allowedServiceTypes[inst.ServiceType] {
		http.Error(w, serviceTypeListError, http.StatusBadRequest)
		return nil, false
	}
	// A Plex test needs the server the candidate names (the machine
	// identifier rides in the config); the token comes from the link or the
	// stored instance.
	if request.MediaServerConfig != nil {
		inst.MediaServerConfig.MachineIdentifier = strings.TrimSpace(request.MediaServerConfig.MachineIdentifier)
	}
	plexCred, err := h.applyPlexLink(&inst, request.PlexLinkPin, existing)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
		return nil, false
	}
	if inst.ServiceType == "plex" {
		inst.MediaServerConfig.ClientID = plexCred.clientID
	}
	// The test doesn't need a name; default it so the shared validation only
	// enforces the URL and credentials.
	if inst.Name == "" {
		inst.Name = inst.ServiceType
	}
	if err := validateRequiredFields(&inst); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
		return nil, false
	}

	inst.URL = strings.TrimRight(inst.URL, "/")
	return &inst, true
}

// Delete removes a service instance.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "instanceID")

	if err := h.store.Delete(instanceID); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrPendingBookRequests) {
			status = http.StatusConflict
		}
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), status)
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
		// A pin must never masquerade as media-server eligibility.
		if IsMediaServerType(serviceType) {
			http.Error(w, fmt.Sprintf(`{"error":"%s instances have no default; use access grants"}`, serviceType), http.StatusBadRequest)
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

// GetUserInstanceGrants returns a user's access-grant rows as a
// {service_type: [instance_ids]} map (admin-only). Grants are additive to the
// user's default: they never move it, they widen what the user may select.
func (h *Handler) GetUserInstanceGrants(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid user id"}`, http.StatusBadRequest)
		return
	}
	grants, err := h.store.ListUserGrants(userID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}
	if grants == nil {
		grants = map[string][]string{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(grants)
}

// UpdateUserInstanceGrants replaces a user's access-grant rows per service
// type (admin-only). Body is a {service_type: [instance_id]|null} map; only
// the listed service types are touched, and a null/empty list clears that
// type's grants. Each instance id must exist and match its keyed service
// type. Returns the updated map.
func (h *Handler) UpdateUserInstanceGrants(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid user id"}`, http.StatusBadRequest)
		return
	}
	var body map[string][]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	// Reject unknown service types up front so a typo never partially applies.
	for serviceType := range body {
		if !grantableServiceTypes[serviceType] {
			http.Error(w, fmt.Sprintf(`{"error":"unknown service_type: %s"}`, serviceType), http.StatusBadRequest)
			return
		}
	}
	if err := h.store.SetUserGrants(userID, body); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
		return
	}
	for serviceType := range body {
		if IsMediaServerType(serviceType) {
			h.notifyGrantObserver([]int64{userID})
			break
		}
	}
	grants, err := h.store.ListUserGrants(userID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}
	if grants == nil {
		grants = map[string][]string{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(grants)
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
	if IsMediaServerType(serviceType) {
		http.Error(w, fmt.Sprintf(`{"error":"%s instances have no default; use access grants"}`, serviceType), http.StatusBadRequest)
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

// instanceUserGrant is one user's access-grant row within a service type, as
// served by the instance-centric grant endpoints. A user appears once per
// granted instance.
type instanceUserGrant struct {
	UserID     int64  `json:"user_id"`
	InstanceID string `json:"instance_id"`
}

// writeInstanceGrants responds with every access-grant row for the service
// type — not just the addressed instance — so the admin UI can also show
// which users hold a grant on a sibling instance.
func (h *Handler) writeInstanceGrants(w http.ResponseWriter, serviceType string) {
	grants, err := h.store.ListTypeUserGrants(serviceType)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}
	resp := make([]instanceUserGrant, 0)
	for userID, instanceIDs := range grants {
		for _, instanceID := range instanceIDs {
			resp = append(resp, instanceUserGrant{UserID: userID, InstanceID: instanceID})
		}
	}
	sort.Slice(resp, func(i, j int) bool {
		if resp[i].UserID != resp[j].UserID {
			return resp[i].UserID < resp[j].UserID
		}
		return resp[i].InstanceID < resp[j].InstanceID
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// GetInstanceGrantUsers returns the access-grant rows for the addressed
// instance's service type (admin-only).
func (h *Handler) GetInstanceGrantUsers(w http.ResponseWriter, r *http.Request) {
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
	h.writeInstanceGrants(w, serviceType)
}

// UpdateInstanceGrantUsers grants the addressed instance to exactly the posted
// user ids (admin-only). Users previously granted this instance but absent
// from the list lose the grant; per-user default pins and grants on sibling
// instances are untouched. Returns the updated grants for the service type.
func (h *Handler) UpdateInstanceGrantUsers(w http.ResponseWriter, r *http.Request) {
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
	if !grantableServiceTypes[serviceType] {
		http.Error(w, fmt.Sprintf(`{"error":"service_type %s does not support grants"}`, serviceType), http.StatusBadRequest)
		return
	}
	var body struct {
		UserIDs []int64 `json:"user_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	// Users losing the grant are known only before the replace-set write.
	var affected []int64
	if IsMediaServerType(serviceType) {
		previous, err := h.store.ListTypeUserGrants(serviceType)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		affected = append(usersGrantedInstance(previous, instanceID), body.UserIDs...)
	}
	if err := h.store.SetInstanceGrantUsers(instanceID, body.UserIDs); err != nil {
		// Covers unknown user ids too (the user_id foreign key rejects them).
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
		return
	}
	h.notifyGrantObserver(affected)
	h.writeInstanceGrants(w, serviceType)
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
	case "qbittorrent":
		// Either shape: an API key (qBittorrent 5.2+) or the WebUI username
		// and password. Storage holds one or the other, never both (see
		// applyQbittorrentAuthMode).
		if inst.APIKey == "" && (inst.Username == "" || inst.Password == "") {
			return fmt.Errorf("an API key, or a username and password, is required for qbittorrent")
		}
	case "nzbget":
		if inst.Username == "" || inst.Password == "" {
			return fmt.Errorf("username and password are required for %s", inst.ServiceType)
		}
	case "transmission":
		// Username/password are optional: Transmission RPC may run without auth.
	case "plex":
		if inst.APIKey == "" {
			return fmt.Errorf("link a Plex account first")
		}
	default: // radarr, sonarr, chaptarr, lidarr, sabnzbd, tautulli, tracearr
		if inst.APIKey == "" {
			return fmt.Errorf("name, url, and api_key are required")
		}
	}
	return nil
}

// applyQbittorrentAuthMode keeps a qBittorrent instance on exactly one of its
// two credential shapes. Credentials are write-only and a blank field keeps
// the stored value, which on its own could never move an instance from a
// password to an API key or back: the stale credential would survive every
// edit and the client would keep choosing it. So the shape the admin sent
// wins: a key clears the stored username and password, a username or
// password clears the stored key, and a request carrying neither (an edit
// that only moved the URL) keeps what is stored. inst already carries the
// stored fallbacks; submitted is the request exactly as it arrived.
func applyQbittorrentAuthMode(inst, submitted *Instance) {
	if inst.ServiceType != "qbittorrent" {
		return
	}
	switch {
	case submitted.APIKey != "":
		inst.Username, inst.Password = "", ""
	case submitted.Username != "" || submitted.Password != "":
		inst.APIKey = ""
	}
}

// validateConnection performs a service-type-specific connectivity check.
func validateConnection(inst *Instance) error {
	switch inst.ServiceType {
	case "radarr", "sonarr":
		return validateArrURL(inst.URL, inst.APIKey, "v3")
	case "chaptarr", "lidarr":
		// Chaptarr (a Readarr fork) and Lidarr speak the Servarr /api/v1 API.
		return validateArrURL(inst.URL, inst.APIKey, "v1")
	case "sabnzbd":
		_, err := sabnzbd.NewClient(inst.URL, inst.APIKey).Version()
		return err
	case "qbittorrent":
		client := newQbittorrentClient(inst)
		if inst.APIKey == "" {
			if err := client.Login(); err != nil {
				return err
			}
		}
		_, err := client.Version()
		return err
	case "nzbget":
		_, err := nzbget.NewClient(inst.URL, inst.Username, inst.Password).Version()
		return err
	case "transmission":
		_, err := transmission.NewClient(inst.URL, inst.Username, inst.Password).SessionGet()
		return err
	case "tautulli", "tracearr":
		return validateWatchHistoryConnection(inst)
	default:
		if IsMediaServerType(inst.ServiceType) {
			return validateMediaServerConnection(inst)
		}
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
