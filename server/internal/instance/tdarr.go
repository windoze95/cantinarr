package instance

import (
	"context"
	"fmt"
	"time"

	"github.com/windoze95/cantinarr-server/internal/tdarr"
)

func (r *Registry) GetTdarrClient(id string) (*tdarr.Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c := r.tdarrClients[id]; c != nil {
		return c, nil
	}
	inst, err := r.getInstanceOfType(id, "tdarr")
	if err != nil {
		return nil, err
	}
	c := tdarr.NewClient(inst.URL, inst.APIKey)
	r.tdarrClients[id] = c
	return c, nil
}

func validateTdarr(inst *Instance) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return tdarr.NewClient(inst.URL, inst.APIKey).Validate(ctx)
}

func applyClearAPIKey(inst *Instance, request *instanceRequest) error {
	if !request.ClearAPIKey {
		return nil
	}
	if inst.ServiceType != "tdarr" {
		return fmt.Errorf("clear_api_key is supported only for Tdarr")
	}
	if request.APIKey != "" {
		return fmt.Errorf("cannot supply and remove an API key together")
	}
	inst.APIKey = ""
	return nil
}
