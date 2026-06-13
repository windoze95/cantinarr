package instance

import (
	"fmt"
	"sync"

	"github.com/windoze95/cantinarr-server/internal/nzbget"
	"github.com/windoze95/cantinarr-server/internal/qbittorrent"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sabnzbd"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
	"github.com/windoze95/cantinarr-server/internal/tautulli"
	"github.com/windoze95/cantinarr-server/internal/transmission"
)

// Registry lazily creates and caches service clients keyed by instance ID.
type Registry struct {
	store               *Store
	mu                  sync.RWMutex
	radarrClients       map[string]*radarr.Client
	sonarrClients       map[string]*sonarr.Client
	sabnzbdClients      map[string]*sabnzbd.Client
	qbittorrentClients  map[string]*qbittorrent.Client
	nzbgetClients       map[string]*nzbget.Client
	transmissionClients map[string]*transmission.Client
	tautulliClients     map[string]*tautulli.Client
}

// NewRegistry creates a new client registry.
func NewRegistry(store *Store) *Registry {
	return &Registry{
		store:               store,
		radarrClients:       make(map[string]*radarr.Client),
		sonarrClients:       make(map[string]*sonarr.Client),
		sabnzbdClients:      make(map[string]*sabnzbd.Client),
		qbittorrentClients:  make(map[string]*qbittorrent.Client),
		nzbgetClients:       make(map[string]*nzbget.Client),
		transmissionClients: make(map[string]*transmission.Client),
		tautulliClients:     make(map[string]*tautulli.Client),
	}
}

// GetRadarrClient returns a cached or new Radarr client for the given instance ID.
func (r *Registry) GetRadarrClient(instanceID string) (*radarr.Client, error) {
	r.mu.RLock()
	if client, ok := r.radarrClients[instanceID]; ok {
		r.mu.RUnlock()
		return client, nil
	}
	r.mu.RUnlock()

	inst, err := r.getInstanceOfType(instanceID, "radarr")
	if err != nil {
		return nil, err
	}

	client := radarr.NewClient(inst.URL, inst.APIKey)

	r.mu.Lock()
	r.radarrClients[instanceID] = client
	r.mu.Unlock()

	return client, nil
}

// GetSonarrClient returns a cached or new Sonarr client for the given instance ID.
func (r *Registry) GetSonarrClient(instanceID string) (*sonarr.Client, error) {
	r.mu.RLock()
	if client, ok := r.sonarrClients[instanceID]; ok {
		r.mu.RUnlock()
		return client, nil
	}
	r.mu.RUnlock()

	inst, err := r.getInstanceOfType(instanceID, "sonarr")
	if err != nil {
		return nil, err
	}

	client := sonarr.NewClient(inst.URL, inst.APIKey)

	r.mu.Lock()
	r.sonarrClients[instanceID] = client
	r.mu.Unlock()

	return client, nil
}

// GetSabnzbdClient returns a cached or new SABnzbd client for the given instance ID.
func (r *Registry) GetSabnzbdClient(instanceID string) (*sabnzbd.Client, error) {
	r.mu.RLock()
	if client, ok := r.sabnzbdClients[instanceID]; ok {
		r.mu.RUnlock()
		return client, nil
	}
	r.mu.RUnlock()

	inst, err := r.getInstanceOfType(instanceID, "sabnzbd")
	if err != nil {
		return nil, err
	}

	client := sabnzbd.NewClient(inst.URL, inst.APIKey)

	r.mu.Lock()
	r.sabnzbdClients[instanceID] = client
	r.mu.Unlock()

	return client, nil
}

// GetQbittorrentClient returns a cached or new qBittorrent client for the given instance ID.
func (r *Registry) GetQbittorrentClient(instanceID string) (*qbittorrent.Client, error) {
	r.mu.RLock()
	if client, ok := r.qbittorrentClients[instanceID]; ok {
		r.mu.RUnlock()
		return client, nil
	}
	r.mu.RUnlock()

	inst, err := r.getInstanceOfType(instanceID, "qbittorrent")
	if err != nil {
		return nil, err
	}

	client := qbittorrent.NewClient(inst.URL, inst.Username, inst.Password)

	r.mu.Lock()
	r.qbittorrentClients[instanceID] = client
	r.mu.Unlock()

	return client, nil
}

// GetNzbgetClient returns a cached or new NZBGet client for the given instance ID.
func (r *Registry) GetNzbgetClient(instanceID string) (*nzbget.Client, error) {
	r.mu.RLock()
	if client, ok := r.nzbgetClients[instanceID]; ok {
		r.mu.RUnlock()
		return client, nil
	}
	r.mu.RUnlock()

	inst, err := r.getInstanceOfType(instanceID, "nzbget")
	if err != nil {
		return nil, err
	}

	client := nzbget.NewClient(inst.URL, inst.Username, inst.Password)

	r.mu.Lock()
	r.nzbgetClients[instanceID] = client
	r.mu.Unlock()

	return client, nil
}

// GetTransmissionClient returns a cached or new Transmission client for the given instance ID.
func (r *Registry) GetTransmissionClient(instanceID string) (*transmission.Client, error) {
	r.mu.RLock()
	if client, ok := r.transmissionClients[instanceID]; ok {
		r.mu.RUnlock()
		return client, nil
	}
	r.mu.RUnlock()

	inst, err := r.getInstanceOfType(instanceID, "transmission")
	if err != nil {
		return nil, err
	}

	client := transmission.NewClient(inst.URL, inst.Username, inst.Password)

	r.mu.Lock()
	r.transmissionClients[instanceID] = client
	r.mu.Unlock()

	return client, nil
}

// GetTautulliClient returns a cached or new Tautulli client for the given instance ID.
func (r *Registry) GetTautulliClient(instanceID string) (*tautulli.Client, error) {
	r.mu.RLock()
	if client, ok := r.tautulliClients[instanceID]; ok {
		r.mu.RUnlock()
		return client, nil
	}
	r.mu.RUnlock()

	inst, err := r.getInstanceOfType(instanceID, "tautulli")
	if err != nil {
		return nil, err
	}

	client := tautulli.NewClient(inst.URL, inst.APIKey)

	r.mu.Lock()
	r.tautulliClients[instanceID] = client
	r.mu.Unlock()

	return client, nil
}

// GetDefaultRadarrClient returns the client for the default Radarr instance.
func (r *Registry) GetDefaultRadarrClient() (*radarr.Client, string, error) {
	inst, err := r.store.GetDefault("radarr")
	if err != nil {
		return nil, "", fmt.Errorf("get default radarr: %w", err)
	}
	if inst == nil {
		return nil, "", nil
	}
	client, err := r.GetRadarrClient(inst.ID)
	return client, inst.ID, err
}

// GetDefaultSonarrClient returns the client for the default Sonarr instance.
func (r *Registry) GetDefaultSonarrClient() (*sonarr.Client, string, error) {
	inst, err := r.store.GetDefault("sonarr")
	if err != nil {
		return nil, "", fmt.Errorf("get default sonarr: %w", err)
	}
	if inst == nil {
		return nil, "", nil
	}
	client, err := r.GetSonarrClient(inst.ID)
	return client, inst.ID, err
}

// GetDefaultSabnzbdClient returns the client for the default SABnzbd instance.
func (r *Registry) GetDefaultSabnzbdClient() (*sabnzbd.Client, string, error) {
	inst, err := r.store.GetDefault("sabnzbd")
	if err != nil {
		return nil, "", fmt.Errorf("get default sabnzbd: %w", err)
	}
	if inst == nil {
		return nil, "", nil
	}
	client, err := r.GetSabnzbdClient(inst.ID)
	return client, inst.ID, err
}

// GetDefaultQbittorrentClient returns the client for the default qBittorrent instance.
func (r *Registry) GetDefaultQbittorrentClient() (*qbittorrent.Client, string, error) {
	inst, err := r.store.GetDefault("qbittorrent")
	if err != nil {
		return nil, "", fmt.Errorf("get default qbittorrent: %w", err)
	}
	if inst == nil {
		return nil, "", nil
	}
	client, err := r.GetQbittorrentClient(inst.ID)
	return client, inst.ID, err
}

// GetDefaultNzbgetClient returns the client for the default NZBGet instance.
func (r *Registry) GetDefaultNzbgetClient() (*nzbget.Client, string, error) {
	inst, err := r.store.GetDefault("nzbget")
	if err != nil {
		return nil, "", fmt.Errorf("get default nzbget: %w", err)
	}
	if inst == nil {
		return nil, "", nil
	}
	client, err := r.GetNzbgetClient(inst.ID)
	return client, inst.ID, err
}

// GetDefaultTransmissionClient returns the client for the default Transmission instance.
func (r *Registry) GetDefaultTransmissionClient() (*transmission.Client, string, error) {
	inst, err := r.store.GetDefault("transmission")
	if err != nil {
		return nil, "", fmt.Errorf("get default transmission: %w", err)
	}
	if inst == nil {
		return nil, "", nil
	}
	client, err := r.GetTransmissionClient(inst.ID)
	return client, inst.ID, err
}

// GetDefaultTautulliClient returns the client for the default Tautulli instance.
func (r *Registry) GetDefaultTautulliClient() (*tautulli.Client, string, error) {
	inst, err := r.store.GetDefault("tautulli")
	if err != nil {
		return nil, "", fmt.Errorf("get default tautulli: %w", err)
	}
	if inst == nil {
		return nil, "", nil
	}
	client, err := r.GetTautulliClient(inst.ID)
	return client, inst.ID, err
}

// InvalidateClient removes a cached client, forcing recreation on next access.
func (r *Registry) InvalidateClient(instanceID string) {
	r.mu.Lock()
	delete(r.radarrClients, instanceID)
	delete(r.sonarrClients, instanceID)
	delete(r.sabnzbdClients, instanceID)
	delete(r.qbittorrentClients, instanceID)
	delete(r.nzbgetClients, instanceID)
	delete(r.transmissionClients, instanceID)
	delete(r.tautulliClients, instanceID)
	r.mu.Unlock()
}

// LookupServiceType returns an instance's service type and whether the
// instance exists. It lets leaf service packages (e.g. tautulli, whose client
// type this package caches) resolve instances without importing this package.
func (s *Store) LookupServiceType(instanceID string) (string, bool, error) {
	inst, err := s.Get(instanceID)
	if err != nil {
		return "", false, err
	}
	if inst == nil {
		return "", false, nil
	}
	return inst.ServiceType, true, nil
}

// getInstanceOfType loads an instance and verifies its service type.
func (r *Registry) getInstanceOfType(instanceID, serviceType string) (*Instance, error) {
	inst, err := r.store.Get(instanceID)
	if err != nil {
		return nil, fmt.Errorf("get instance: %w", err)
	}
	if inst == nil {
		return nil, fmt.Errorf("instance not found: %s", instanceID)
	}
	if inst.ServiceType != serviceType {
		return nil, fmt.Errorf("instance %s is not a %s instance", instanceID, serviceType)
	}
	return inst, nil
}
