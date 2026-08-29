package instance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/windoze95/cantinarr-server/internal/emby"
	"github.com/windoze95/cantinarr-server/internal/jellyfin"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
	"github.com/windoze95/cantinarr-server/internal/plex"
)

// mediaServerTypes are the service types that are media servers Cantinarr
// manages user access on. They follow the Chaptarr rule: never a global
// default, granted per user, invisible to arr routing. Jellyfin and Emby
// hold accounts Cantinarr creates; Plex holds shares Cantinarr sends.
var mediaServerTypes = []string{"jellyfin", "emby", "plex"}

// PlexPublicAddress is where anyone signs in to any Plex server, so it is the
// sign-in address a Plex instance shows unless the admin typed another.
const PlexPublicAddress = "https://app.plex.tv"

// IsMediaServerType reports whether serviceType is a media server.
func IsMediaServerType(serviceType string) bool {
	for _, t := range mediaServerTypes {
		if t == serviceType {
			return true
		}
	}
	return false
}

// MediaServerTypes returns the media-server service types in a stable order.
func MediaServerTypes() []string {
	return append([]string(nil), mediaServerTypes...)
}

// mediaServerTypeList renders the media-server types for error messages:
// 'jellyfin', 'emby'.
func mediaServerTypeList() string {
	quoted := make([]string, 0, len(mediaServerTypes))
	for _, t := range mediaServerTypes {
		quoted = append(quoted, "'"+t+"'")
	}
	return strings.Join(quoted, ", ")
}

// MediaServerConfig is the per-instance configuration of a media server.
// PublicAddress is the client-reachable address shown to granted users so
// they know where to sign in — the only instance field a requester ever
// receives, and only because the admin typed it. LibraryIDs are the server's
// library identifiers new accounts may see; empty shares every library,
// including ones added later.
type MediaServerConfig struct {
	PublicAddress string   `json:"public_address"`
	LibraryIDs    []string `json:"library_ids"`
	// MachineIdentifier names the Plex Media Server whose shares the instance
	// manages (plex.tv's machineIdentifier). Empty for every other type.
	MachineIdentifier string `json:"machine_identifier,omitempty"`
	// AutoApprove (Plex) grants this server to anyone who shares a Plex email
	// and sends their invite at once, instead of waiting for an admin to
	// grant them. Off unless an admin switches it on.
	AutoApprove bool `json:"auto_approve,omitempty"`
	// ClientID (Plex) is the X-Plex-Client-Identifier the instance's token
	// was minted under; plex.tv ties a token to the device that linked it.
	// Server-managed: set by the PIN link, never taken from a request, and
	// never served.
	ClientID string `json:"client_id,omitempty"`
}

func (c MediaServerConfig) clone() MediaServerConfig {
	return MediaServerConfig{
		PublicAddress:     c.PublicAddress,
		LibraryIDs:        append([]string{}, c.LibraryIDs...),
		MachineIdentifier: c.MachineIdentifier,
		AutoApprove:       c.AutoApprove,
		ClientID:          c.ClientID,
	}
}

// public is the config as served to clients: everything but the
// server-managed client id.
func (c MediaServerConfig) public() MediaServerConfig {
	out := c.clone()
	out.ClientID = ""
	return out
}

// NewMediaServerProvider builds the client for a media-server instance. It is
// the one place a service type maps to a client package: a new server is a
// case here plus an entry in mediaServerTypes.
func NewMediaServerProvider(inst *Instance) (mediaserver.Provider, error) {
	switch inst.ServiceType {
	case "jellyfin":
		return jellyfin.NewClient(inst.URL, inst.APIKey), nil
	case "emby":
		return emby.NewClient(inst.URL, inst.APIKey), nil
	case "plex":
		cfg := inst.MediaServerConfig
		return plex.NewProvider(plex.NewClientAt(inst.URL), cfg.ClientID, inst.APIKey, cfg.MachineIdentifier), nil
	default:
		return nil, fmt.Errorf("not a media server instance: %s", inst.ServiceType)
	}
}

// normalizeMediaServerConfig zeroes the config for every non-media-server
// type and tidies ids (trimmed, de-duplicated, sorted) so equal configs
// encode identically.
func normalizeMediaServerConfig(inst *Instance) {
	if !IsMediaServerType(inst.ServiceType) {
		inst.MediaServerConfig = MediaServerConfig{}
		inst.MediaServerConfigInvalid = false
		return
	}
	inst.MediaServerConfig.PublicAddress = strings.TrimRight(strings.TrimSpace(inst.MediaServerConfig.PublicAddress), "/")
	inst.MediaServerConfig.LibraryIDs = tidyLibraryIDs(inst.MediaServerConfig.LibraryIDs)
	inst.MediaServerConfig.MachineIdentifier = strings.TrimSpace(inst.MediaServerConfig.MachineIdentifier)
	if inst.ServiceType == "plex" && inst.MediaServerConfig.PublicAddress == "" {
		inst.MediaServerConfig.PublicAddress = PlexPublicAddress
	}
}

// tidyLibraryIDs trims, drops empties, de-duplicates, and sorts, so equal
// selections always encode identically. Never nil: the wire shape is [].
func tidyLibraryIDs(raw []string) []string {
	seen := map[string]bool{}
	ids := make([]string, 0, len(raw))
	for _, id := range raw {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func encodeMediaServerConfig(inst *Instance) (string, error) {
	normalizeMediaServerConfig(inst)
	if !IsMediaServerType(inst.ServiceType) {
		return "{}", nil
	}
	encoded, err := json.Marshal(inst.MediaServerConfig)
	if err != nil {
		return "", fmt.Errorf("encode media server config: %w", err)
	}
	return string(encoded), nil
}

// validateMediaServerConfig checks an admin-submitted config and returns the
// normalized copy that will be stored.
func validateMediaServerConfig(cfg MediaServerConfig) (MediaServerConfig, error) {
	out := MediaServerConfig{LibraryIDs: []string{}}
	address := strings.TrimSpace(cfg.PublicAddress)
	if address != "" {
		parsed, err := url.Parse(address)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return out, fmt.Errorf("public_address must be an absolute http or https URL")
		}
		if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return out, fmt.Errorf("public_address must not contain credentials, a query string, or a fragment")
		}
		out.PublicAddress = strings.TrimRight(address, "/")
	}
	for _, id := range cfg.LibraryIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if len(id) > 128 {
			return out, fmt.Errorf("library id is too long")
		}
		for _, r := range id {
			if unicode.IsSpace(r) || unicode.IsControl(r) {
				return out, fmt.Errorf("library id contains invalid characters")
			}
		}
	}
	out.LibraryIDs = tidyLibraryIDs(cfg.LibraryIDs)
	machine := strings.TrimSpace(cfg.MachineIdentifier)
	if len(machine) > 128 {
		return out, fmt.Errorf("machine identifier is too long")
	}
	for _, r := range machine {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return out, fmt.Errorf("machine identifier contains invalid characters")
		}
	}
	out.MachineIdentifier = machine
	out.AutoApprove = cfg.AutoApprove
	return out, nil
}

// applyMediaServerConfig resolves the request's media_server_config the way
// applyMediaPathMappings resolves mappings: an omitted field (nil pointer)
// keeps the stored config on update and means "share everything" on create;
// a present field replaces it after validation. Non-media types may only send
// an empty config.
func (h *Handler) applyMediaServerConfig(inst *Instance, provided *MediaServerConfig, existing *Instance) error {
	if !IsMediaServerType(inst.ServiceType) {
		if provided != nil && (strings.TrimSpace(provided.PublicAddress) != "" || len(provided.LibraryIDs) > 0) {
			return fmt.Errorf("media_server_config is supported only for media servers (%s)", mediaServerTypeList())
		}
		inst.MediaServerConfig = MediaServerConfig{}
		return nil
	}
	if provided == nil {
		if existing != nil {
			inst.MediaServerConfig = existing.MediaServerConfig.clone()
		} else {
			inst.MediaServerConfig = MediaServerConfig{LibraryIDs: []string{}}
		}
		return nil
	}
	normalized, err := validateMediaServerConfig(*provided)
	if err != nil {
		return fmt.Errorf("invalid media_server_config: %w", err)
	}
	if existing != nil {
		// Server-managed; a request can neither set nor clear it.
		normalized.ClientID = existing.MediaServerConfig.ClientID
	}
	inst.MediaServerConfig = normalized
	inst.MediaServerConfigInvalid = false
	return nil
}

// validateMediaServerConnection is the connection test for media servers.
func validateMediaServerConnection(inst *Instance) error {
	provider, err := NewMediaServerProvider(inst)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = provider.SystemInfo(ctx)
	return err
}

// GrantObserver is told which users' media-server grants may have changed.
// Wired by main to the media-access service, which reconciles the affected
// users' accounts against their grants. Runs synchronously after the grant
// write and must never fail it.
type GrantObserver func(userIDs []int64)

// SetGrantObserver installs the observer; nil disables it.
func (h *Handler) SetGrantObserver(observer GrantObserver) {
	h.grantObserver = observer
}

func (h *Handler) notifyGrantObserver(userIDs []int64) {
	if h.grantObserver == nil || len(userIDs) == 0 {
		return
	}
	seen := make(map[int64]bool, len(userIDs))
	unique := make([]int64, 0, len(userIDs))
	for _, id := range userIDs {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	sort.Slice(unique, func(i, j int) bool { return unique[i] < unique[j] })
	h.grantObserver(unique)
}

// SharedLibrariesObserver is told that an instance's shared-library set
// changed, with the new set. Wired by main to the media-access service, which
// re-applies the set to the accounts Cantinarr created on that instance.
// Runs synchronously after the save and must never fail it.
type SharedLibrariesObserver func(instanceID string, libraryIDs []string)

// SetSharedLibrariesObserver installs the observer; nil disables it.
func (h *Handler) SetSharedLibrariesObserver(observer SharedLibrariesObserver) {
	h.sharedLibrariesObserver = observer
}

func (h *Handler) notifySharedLibrariesObserver(instanceID string, libraryIDs []string) {
	if h.sharedLibrariesObserver == nil {
		return
	}
	h.sharedLibrariesObserver(instanceID, append([]string(nil), libraryIDs...))
}

// sameLibraryIDs compares two normalized (tidyLibraryIDs) selections.
func sameLibraryIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// usersGrantedInstance lists the users currently holding a grant on one
// instance, read from the type-wide grant map.
func usersGrantedInstance(grants map[int64][]string, instanceID string) []int64 {
	var users []int64
	for userID, instanceIDs := range grants {
		for _, id := range instanceIDs {
			if id == instanceID {
				users = append(users, userID)
				break
			}
		}
	}
	return users
}

type mediaServerLibraryResponse struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	CollectionType string `json:"collection_type"`
}

type mediaServerLibrariesResponse struct {
	ServerName string                       `json:"server_name"`
	Version    string                       `json:"version"`
	Libraries  []mediaServerLibraryResponse `json:"libraries"`
}

// MediaServerLibraries reports the libraries a media server offers, for the
// admin's shared-libraries picker. It accepts the same candidate body as
// TestConnection (an id falls back to the stored credentials), so the picker
// works both before the instance is saved and while editing it. Names and
// ids only: the server's filesystem paths are never read.
func (h *Handler) MediaServerLibraries(w http.ResponseWriter, r *http.Request) {
	inst, ok := h.resolveTestInstance(w, r)
	if !ok {
		return
	}
	if !IsMediaServerType(inst.ServiceType) {
		http.Error(w, fmt.Sprintf(`{"error":"service_type must be a media server type (%s)"}`, mediaServerTypeList()), http.StatusBadRequest)
		return
	}
	provider, err := NewMediaServerProvider(inst)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	info, err := provider.SystemInfo(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"connection test failed: %s"}`, err), http.StatusBadRequest)
		return
	}
	libraries, err := provider.Libraries(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"connection test failed: %s"}`, err), http.StatusBadRequest)
		return
	}
	resp := mediaServerLibrariesResponse{
		ServerName: info.ServerName,
		Version:    info.Version,
		Libraries:  make([]mediaServerLibraryResponse, 0, len(libraries)),
	}
	for _, lib := range libraries {
		resp.Libraries = append(resp.Libraries, mediaServerLibraryResponse{ID: lib.ID, Name: lib.Name, CollectionType: lib.CollectionType})
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
