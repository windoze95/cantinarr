package instance

import (
	"context"
	"fmt"
	"time"

	"github.com/windoze95/cantinarr-server/internal/tautulli"
	"github.com/windoze95/cantinarr-server/internal/tracearr"
	"github.com/windoze95/cantinarr-server/internal/watchhistory"
)

// IsWatchHistoryType reports whether serviceType is a watch-history provider
// (Tautulli or Tracearr): an admin surface with a global default, never
// granted per user.
func IsWatchHistoryType(serviceType string) bool {
	return watchhistory.IsServiceType(serviceType)
}

// WatchHistoryTypes returns the watch-history service types in a stable order.
func WatchHistoryTypes() []string {
	return watchhistory.ServiceTypes()
}

// NewWatchHistoryProvider builds the provider for a watch-history instance.
// It is the one place a watch-history service type maps to a client package:
// a new source is a case here plus an entry in watchhistory's service types.
func NewWatchHistoryProvider(inst *Instance) (watchhistory.Provider, error) {
	switch inst.ServiceType {
	case "tautulli":
		return tautulli.NewProvider(inst.URL, inst.APIKey), nil
	case "tracearr":
		return tracearr.NewProvider(inst.URL, inst.APIKey), nil
	default:
		return nil, fmt.Errorf("not a watch-history instance: %s", inst.ServiceType)
	}
}

// validateWatchHistoryConnection is the connection test for watch-history
// providers: each provider's own identity call answers only to a working
// credential.
func validateWatchHistoryConnection(inst *Instance) error {
	provider, err := NewWatchHistoryProvider(inst)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = provider.ServerInfo(ctx)
	return err
}

// GetWatchHistoryProvider returns a cached or new provider for the instance.
// Construction happens under the write lock so a provider (and any cache it
// carries) is built once.
func (r *Registry) GetWatchHistoryProvider(instanceID string) (watchhistory.Provider, error) {
	r.mu.RLock()
	if provider, ok := r.watchHistoryProviders[instanceID]; ok {
		r.mu.RUnlock()
		return provider, nil
	}
	r.mu.RUnlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if provider, ok := r.watchHistoryProviders[instanceID]; ok {
		return provider, nil
	}

	inst, err := r.store.Get(instanceID)
	if err != nil {
		return nil, fmt.Errorf("get instance: %w", err)
	}
	if inst == nil {
		return nil, fmt.Errorf("instance not found: %s", instanceID)
	}
	if !IsWatchHistoryType(inst.ServiceType) {
		return nil, fmt.Errorf("instance %s is not a watch-history instance", instanceID)
	}
	provider, err := NewWatchHistoryProvider(inst)
	if err != nil {
		return nil, err
	}
	r.watchHistoryProviders[instanceID] = provider
	return provider, nil
}
