package mcp

import (
	"fmt"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// Multi-library targeting for the arr tools.
//
// Every arr tool historically read one hardwired target: callCtx.InstanceID
// when the remediation runner pinned a scoped read, else the service's global
// default. On a multi-library server the interactive assistant needs to name
// a library too, so the arr tools accept an optional model-supplied
// instance_id. Precedence is the get_service_config rule: a server-authored
// callCtx.InstanceID always overrides the model's input, which keeps
// remediation's scoping unforgeable. An id the model supplied that resolves
// to nothing gets the settings-family failure text instead of reading as an
// unconfigured service; a trusted id keeps the historic "not configured"
// refusal (an unknown id must never fall back to the default —
// TestArrToolsRefuseUnknownInstanceWithoutDefaultFallback pins that).

// arrToolInstanceID applies the precedence rule.
func arrToolInstanceID(modelID, callID string) string {
	if callID != "" {
		return callID
	}
	return modelID
}

// arrTargetLabel names the library a resolved call targeted, resolving the
// empty id to the service's default instance so the answer still says which
// library it read — on a multi-library server there is more than one it could
// have meant, and an unlabeled empty listing reads as absence when it may be
// blindness to the intended library.
func (s *ToolServer) arrTargetLabel(service, instanceID string) string {
	if instanceID == "" && s.registry != nil {
		if id, err := s.registry.GetDefaultInstanceID(service); err == nil {
			instanceID = id
		}
	}
	return s.arrInstanceLabel(service, instanceID)
}

// radarrTargetFor resolves the Radarr client one tool call targets, with the
// library's display label. A non-empty refusal is a complete user-facing
// answer.
func (s *ToolServer) radarrTargetFor(modelID, callID string) (*radarr.Client, string, string) {
	instanceID := arrToolInstanceID(modelID, callID)
	client := s.GetRadarrFor(instanceID)
	if client == nil {
		if callID == "" && modelID != "" && s.registry != nil {
			return nil, "", s.instanceResolveFailureText("radarr", modelID)
		}
		return nil, "", "Radarr is not configured."
	}
	return client, s.arrTargetLabel("radarr", instanceID), ""
}

// sonarrTargetFor is radarrTargetFor's Sonarr twin.
func (s *ToolServer) sonarrTargetFor(modelID, callID string) (*sonarr.Client, string, string) {
	instanceID := arrToolInstanceID(modelID, callID)
	client := s.GetSonarrFor(instanceID)
	if client == nil {
		if callID == "" && modelID != "" && s.registry != nil {
			return nil, "", s.instanceResolveFailureText("sonarr", modelID)
		}
		return nil, "", "Sonarr is not configured."
	}
	return client, s.arrTargetLabel("sonarr", instanceID), ""
}

// chaptarrTargetFor is radarrTargetFor's Chaptarr twin.
func (s *ToolServer) chaptarrTargetFor(modelID, callID string) (*chaptarr.Client, string, string) {
	instanceID := arrToolInstanceID(modelID, callID)
	client := s.GetChaptarrFor(instanceID)
	if client == nil {
		if callID == "" && modelID != "" && s.registry != nil {
			return nil, "", s.instanceResolveFailureText("chaptarr", modelID)
		}
		return nil, "", "Chaptarr is not configured."
	}
	return client, s.arrTargetLabel("chaptarr", instanceID), ""
}

// arrClientsFor resolves all three service clients for the helper-style tools
// that branch on media_type themselves. Only a model-supplied id matching no
// instance of any service earns a refusal here; a wrong-service id for the
// requested media type keeps each helper's historic "not configured" text,
// which is also the trusted-id behavior.
func (s *ToolServer) arrClientsFor(modelID, callID string) (*radarr.Client, *sonarr.Client, *chaptarr.Client, string) {
	instanceID := arrToolInstanceID(modelID, callID)
	radarrClient := s.GetRadarrFor(instanceID)
	sonarrClient := s.GetSonarrFor(instanceID)
	chaptarrClient := s.GetChaptarrFor(instanceID)
	if instanceID != "" && callID == "" && s.registry != nil &&
		radarrClient == nil && sonarrClient == nil && chaptarrClient == nil {
		for _, service := range []string{"radarr", "sonarr", "chaptarr"} {
			if s.arrInstanceName(service, instanceID) != "" {
				return nil, nil, nil, s.instanceResolveFailureText(service, instanceID)
			}
		}
		return nil, nil, nil, fmt.Sprintf("No Radarr, Sonarr, or Chaptarr instance with ID %q. Call list_arr_instances to see the configured instances.", instanceID)
	}
	return radarrClient, sonarrClient, chaptarrClient, ""
}
