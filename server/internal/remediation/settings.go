package remediation

import (
	"encoding/json"
	"fmt"
)

// remediationSettingsKey is the settings-table key holding the global AI
// remediation configuration (JSON blob), mirroring the request_settings storage
// pattern (request/service.go GetGlobalSettings/SetGlobalSettings).
const remediationSettingsKey = "remediation_settings"

// Autonomy tiers controlling the mutation policy (enforced in later waves).
const (
	AutonomyInvestigateOnly = "investigate_only"
	AutonomyPropose         = "propose"
	AutonomyAutoSafe        = "auto_safe"
)

// Settings is the global AI remediation configuration. It is stored as the
// remediation_settings JSON blob and unmarshalled over Defaults(), so adding a
// field later is migration-free (older blobs simply keep the default for it).
//
// The remediation agent (later waves) is provider-agnostic: it reuses
// Cantinarr's existing multi-provider AI layer. Provider/Model are plain
// configurable overrides — empty means "inherit the server's globally-configured
// AI provider/model" (the same one the AI assistant uses), so no provider is
// ever hardcoded here.
//
// Wave 1 only stores and serves these values; no agent run consumes the bound
// fields yet. The two switches that matter in Wave 1 are Enabled (master) and
// AllowReporting (the user-visible "Report a problem" affordance, surfaced to
// non-admin clients via /api/config).
type Settings struct {
	Enabled                bool   `json:"enabled"`                   // master switch — ships OFF
	AutoDispatch           bool   `json:"auto_dispatch"`             // poller may open auto issues — ships OFF
	AllowReporting         bool   `json:"allow_reporting"`           // user-visible "Report a problem" affordance
	Autonomy               string `json:"autonomy"`                  // investigate_only | propose | auto_safe
	Provider               string `json:"provider"`                  // "" = inherit the configured AI provider
	Model                  string `json:"model"`                     // "" = inherit the configured AI model
	MaxSteps               int    `json:"max_steps"`                 // total tool calls per investigation
	MaxTurnTokens          int    `json:"max_turn_tokens"`           // per-turn output cap
	MaxWallClockSecs       int    `json:"max_wall_clock_secs"`       // active wall-clock budget
	MaxCostMicros          int    `json:"max_cost_micros"`           // per-run cost ceiling (millionths USD)
	DailyRunCap            int    `json:"daily_run_cap"`             // max runs/day
	DailyCostCeilingMicros int    `json:"daily_cost_ceiling_micros"` // global cost ceiling/day (millionths USD)
	CircuitBreakerGiveups  int    `json:"circuit_breaker_giveups"`   // consecutive auto give-ups -> auto-dispatch off
	MaxUserWaitHours       int    `json:"max_user_wait_hours"`       // W4: reply-TTL — close an awaiting_user issue with no reply within this window
}

// Defaults returns the built-in remediation settings. Provider and Model are
// empty so the agent inherits the configured AI provider/model unless an admin
// overrides them; every mutation is admin-approved (autonomy "propose"); the
// feature ships OFF. The cost ceilings are best-effort guardrails.
func Defaults() Settings {
	return Settings{
		Enabled:                false,
		AutoDispatch:           false,
		AllowReporting:         true,
		Autonomy:               AutonomyPropose,
		Provider:               "",
		Model:                  "",
		MaxSteps:               12,
		MaxTurnTokens:          4096,
		MaxWallClockSecs:       300,
		MaxCostMicros:          500000, // ~$0.50/run
		DailyRunCap:            50,
		DailyCostCeilingMicros: 5000000, // ~$5/day, global
		CircuitBreakerGiveups:  5,
		MaxUserWaitHours:       72, // W4: 3 days for a reporter to answer before wont_fix(user_unresponsive)
	}
}

// validAutonomy reports whether a stored/submitted autonomy value is one of the
// known tiers.
func validAutonomy(a string) bool {
	switch a {
	case AutonomyInvestigateOnly, AutonomyPropose, AutonomyAutoSafe:
		return true
	}
	return false
}

// normalize coerces any out-of-range or unknown field back to a safe default so
// a hand-edited or partial blob can never disable the bounds. Mirrors the
// request settings' defensive validSeasonScope fallback.
func (g *Settings) normalize() {
	d := Defaults()
	if !validAutonomy(g.Autonomy) {
		g.Autonomy = d.Autonomy
	}
	// Provider/Model are intentionally not defaulted: empty means "inherit the
	// configured AI provider/model", which is the shipped default.
	if g.MaxSteps <= 0 {
		g.MaxSteps = d.MaxSteps
	}
	if g.MaxTurnTokens <= 0 {
		g.MaxTurnTokens = d.MaxTurnTokens
	}
	if g.MaxWallClockSecs <= 0 {
		g.MaxWallClockSecs = d.MaxWallClockSecs
	}
	if g.MaxCostMicros <= 0 {
		g.MaxCostMicros = d.MaxCostMicros
	}
	if g.DailyRunCap <= 0 {
		g.DailyRunCap = d.DailyRunCap
	}
	if g.DailyCostCeilingMicros <= 0 {
		g.DailyCostCeilingMicros = d.DailyCostCeilingMicros
	}
	if g.CircuitBreakerGiveups <= 0 {
		g.CircuitBreakerGiveups = d.CircuitBreakerGiveups
	}
	if g.MaxUserWaitHours <= 0 {
		g.MaxUserWaitHours = d.MaxUserWaitHours
	}
}

// Settings returns the stored global remediation settings, falling back to the
// built-in defaults for any missing field (read exactly like
// request.GetGlobalSettings).
func (s *Service) Settings() Settings {
	g := Defaults()
	var v string
	if err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", remediationSettingsKey).Scan(&v); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &g)
	}
	g.normalize()
	return g
}

// SetSettings persists the global remediation settings (written exactly like
// request.SetGlobalSettings) and returns the normalized value that was stored.
func (s *Service) SetSettings(g Settings) (Settings, error) {
	g.normalize()
	data, err := json.Marshal(g)
	if err != nil {
		return Settings{}, fmt.Errorf("encode remediation settings: %w", err)
	}
	if _, err := s.db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", remediationSettingsKey, string(data)); err != nil {
		return Settings{}, fmt.Errorf("save remediation settings: %w", err)
	}
	return g, nil
}
