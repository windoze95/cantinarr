package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
)

// disabledToolsKey is the settings-table key holding a JSON array of disabled
// tool names.
const disabledToolsKey = "ai_disabled_tools"

// ToolStatus describes a tool plus its admin-configurable enabled state.
type ToolStatus struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	AdminOnly   bool   `json:"admin_only"`
}

// loadTogglesLocked populates disabledTools from the settings table. Must be
// called with toggleMu held for writing.
func (s *ToolServer) loadTogglesLocked() {
	s.togglesLoaded = true
	s.disabledTools = map[string]bool{}
	if s.creds == nil {
		return
	}
	raw := s.creds.GetSetting(disabledToolsKey)
	if raw == "" {
		return
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return
	}
	for _, name := range names {
		s.disabledTools[name] = true
	}
}

// IsToolEnabled reports whether the named tool is currently enabled.
// Unknown names report true; ExecuteTool rejects them separately.
func (s *ToolServer) IsToolEnabled(name string) bool {
	s.toggleMu.RLock()
	if s.togglesLoaded {
		disabled := s.disabledTools[name]
		s.toggleMu.RUnlock()
		return !disabled
	}
	s.toggleMu.RUnlock()

	s.toggleMu.Lock()
	defer s.toggleMu.Unlock()
	if !s.togglesLoaded {
		s.loadTogglesLocked()
	}
	return !s.disabledTools[name]
}

// ListToolStatuses returns every tool with its enabled and admin-only state.
func (s *ToolServer) ListToolStatuses() []ToolStatus {
	statuses := make([]ToolStatus, 0, len(toolDefinitions))
	for _, t := range toolDefinitions {
		statuses = append(statuses, ToolStatus{
			Name:        t.Name,
			Description: t.Description,
			Enabled:     s.IsToolEnabled(t.Name),
			AdminOnly:   t.AdminOnly,
		})
	}
	return statuses
}

// SetToolEnabled enables or disables a tool by name and persists the change.
func (s *ToolServer) SetToolEnabled(name string, enabled bool) error {
	if findToolDefinition(name) == nil {
		return fmt.Errorf("unknown tool: %s", name)
	}

	s.toggleMu.Lock()
	defer s.toggleMu.Unlock()
	if !s.togglesLoaded {
		s.loadTogglesLocked()
	}

	if enabled {
		delete(s.disabledTools, name)
	} else {
		s.disabledTools[name] = true
	}

	if s.creds == nil {
		return nil
	}
	names := make([]string, 0, len(s.disabledTools))
	for n := range s.disabledTools {
		names = append(names, n)
	}
	sort.Strings(names)
	data, err := json.Marshal(names)
	if err != nil {
		return fmt.Errorf("marshal disabled tools: %w", err)
	}
	if err := s.creds.SetSetting(disabledToolsKey, string(data)); err != nil {
		return fmt.Errorf("persist disabled tools: %w", err)
	}
	return nil
}
