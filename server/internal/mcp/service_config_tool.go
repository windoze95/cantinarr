package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/arr"
)

// get_service_config: the settings the prevention catalog names but nothing
// could see. Every entry is a bounded, secret-free summary built INSIDE the
// arr client — indexer and download-client payloads carry API keys and
// passwords, and those die at the client boundary, unread.
func (s *ToolServer) getServiceConfig(input json.RawMessage, callInstanceID string) (*ToolResult, error) {
	var params struct {
		Service    string `json:"service"`
		InstanceID string `json:"instance_id"`
		Section    string `json:"section"`
	}
	if err := json.Unmarshal(nonEmptyJSON(input), &params); err != nil {
		return nil, fmt.Errorf("invalid get_service_config input: %w", err)
	}
	if callInstanceID != "" {
		params.InstanceID = callInstanceID
	}
	if !arr.ValidConfigSection(params.Section) {
		return &ToolResult{Text: "Unknown section. Valid sections: indexers, delay_profiles, release_profiles, download_clients, remote_path_mappings. (Radarr has no release profiles.)"}, nil
	}
	reader, label, refusal := s.settingsReaderFor(params.Service, params.InstanceID)
	if refusal != "" {
		return &ToolResult{Text: refusal}, nil
	}
	entries, err := reader.GetConfigSummary(params.Section)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return &ToolResult{Text: fmt.Sprintf("%s has no %s configured. This read listed the section's own records on the live service; an empty answer here is genuine absence.", label, strings.ReplaceAll(params.Section, "_", " "))}, nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s %s (%d):\n", label, strings.ReplaceAll(params.Section, "_", " "), len(entries))
	for _, e := range entries {
		fmt.Fprintf(&sb, "- %s: %s\n", e.Name, e.Detail)
	}
	sb.WriteString("Values are read-only summaries; credentials and URLs are never included.")
	return &ToolResult{Text: sb.String()}, nil
}
