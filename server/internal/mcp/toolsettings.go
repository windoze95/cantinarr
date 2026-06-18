package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// disabledToolsKey is the settings-table key holding a JSON array of disabled
// tool names.
const disabledToolsKey = "ai_disabled_tools"

const aiDebugUntilKey = "ai_debug_until"

// ToolStatus describes a tool plus its admin-configurable enabled state.
type ToolStatus struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	AdminOnly   bool   `json:"admin_only"`
}

// AIDebugStatus describes whether verbose AI/tool logging is temporarily on.
type AIDebugStatus struct {
	Enabled          bool   `json:"enabled"`
	EnabledUntil     string `json:"enabled_until,omitempty"`
	RemainingSeconds int64  `json:"remaining_seconds"`
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

// AIDebugStatus reports the current debug logging state.
func (s *ToolServer) AIDebugStatus() AIDebugStatus {
	until := s.aiDebugUntil()
	now := time.Now()
	if until.IsZero() || !until.After(now) {
		return AIDebugStatus{Enabled: false}
	}
	return AIDebugStatus{
		Enabled:          true,
		EnabledUntil:     until.Format(time.RFC3339),
		RemainingSeconds: int64(time.Until(until).Seconds()),
	}
}

// IsAIDebugEnabled reports whether verbose AI/tool logging is currently active.
func (s *ToolServer) IsAIDebugEnabled() bool {
	until := s.aiDebugUntil()
	return !until.IsZero() && until.After(time.Now())
}

// ExtendAIDebug enables or extends debug logging by the given whole hours.
func (s *ToolServer) ExtendAIDebug(hours int) (AIDebugStatus, error) {
	if hours < 1 {
		hours = 1
	}
	if hours > 24 {
		hours = 24
	}
	base := time.Now()
	if current := s.aiDebugUntil(); current.After(base) {
		base = current
	}
	until := base.Add(time.Duration(hours) * time.Hour).UTC()
	if s.creds != nil {
		if err := s.creds.SetSetting(aiDebugUntilKey, until.Format(time.RFC3339)); err != nil {
			return AIDebugStatus{}, err
		}
	}
	return s.AIDebugStatus(), nil
}

// DisableAIDebug turns off verbose AI/tool logging immediately.
func (s *ToolServer) DisableAIDebug() (AIDebugStatus, error) {
	if s.creds != nil {
		if err := s.creds.DeleteCredential(aiDebugUntilKey); err != nil {
			return AIDebugStatus{}, err
		}
	}
	return s.AIDebugStatus(), nil
}

func (s *ToolServer) aiDebugUntil() time.Time {
	if s.creds == nil {
		return time.Time{}
	}
	raw := s.creds.GetSetting(aiDebugUntilKey)
	if raw == "" {
		return time.Time{}
	}
	until, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return until
}
