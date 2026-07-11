package remediation

import (
	"encoding/json"
	"fmt"
)

// remediationSettingsKey is the settings-table key holding the global AI
// remediation configuration (JSON blob), mirroring the request_settings storage
// pattern (request/service.go GetGlobalSettings/SetGlobalSettings).
const remediationSettingsKey = "remediation_settings"

// Remediation modes control whether the agent may only investigate or may also
// record a proposal for an administrator to approve. There is deliberately no
// auto-execution mode: every current action is consequential and always crosses
// the human approval gate.
const (
	ModeInvestigateOnly = "investigate_only"
	ModeSupervised      = "supervised"

	legacyAutonomyPropose  = "propose"
	legacyAutonomyAutoSafe = "auto_safe"
)

// Absolute ceilings keep a typo or hand-edited settings payload from turning a
// guardrail into an effectively unbounded agent run. The app may offer tighter
// UX ranges; these are the authoritative server limits.
const (
	maxConfiguredSteps          = 50
	maxConfiguredTurnTokens     = 32768
	maxConfiguredWallClockSecs  = 1800
	maxConfiguredRunCostMicros  = 10_000_000
	maxConfiguredDailyRuns      = 1000
	maxConfiguredDailyCost      = 100_000_000
	maxConfiguredBreakerGiveups = 100
	maxConfiguredUserWaitHours  = 24 * 30
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
	MarkResolvedAsRead     bool   `json:"mark_resolved_as_read"`     // mark an issue read when it resolves (default ON)
	Mode                   string `json:"mode"`                      // investigate_only | supervised
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

// UnmarshalJSON accepts the pre-mode "autonomy" field so existing stored blobs
// and older clients retain their safety policy across the rename. mode wins when
// it is explicitly valid; otherwise a recognized legacy value is translated.
// Settings deliberately has no autonomy field, so marshaling emits only mode.
func (g *Settings) UnmarshalJSON(data []byte) error {
	type plainSettings Settings
	decoded := plainSettings(*g)
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	var compatibility struct {
		Mode     *string `json:"mode"`
		Autonomy *string `json:"autonomy"`
	}
	if err := json.Unmarshal(data, &compatibility); err != nil {
		return err
	}

	*g = Settings(decoded)
	if compatibility.Autonomy != nil && (compatibility.Mode == nil || !validMode(*compatibility.Mode)) {
		if mode, ok := legacyAutonomyMode(*compatibility.Autonomy); ok {
			g.Mode = mode
		}
	}
	return nil
}

func legacyAutonomyMode(autonomy string) (string, bool) {
	switch autonomy {
	case ModeInvestigateOnly:
		return ModeInvestigateOnly, true
	case legacyAutonomyPropose, legacyAutonomyAutoSafe, ModeSupervised:
		return ModeSupervised, true
	default:
		return "", false
	}
}

// Defaults returns the built-in remediation settings. Provider and Model are
// empty so the agent inherits the configured AI provider/model unless an admin
// overrides them; every mutation is admin-approved (mode "supervised"); the
// feature ships OFF. The cost ceilings are best-effort guardrails.
func Defaults() Settings {
	return Settings{
		Enabled:                false,
		AutoDispatch:           false,
		AllowReporting:         true,
		MarkResolvedAsRead:     true,
		Mode:                   ModeSupervised,
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

// validMode reports whether a stored/submitted mode is implemented.
func validMode(mode string) bool {
	switch mode {
	case ModeInvestigateOnly, ModeSupervised:
		return true
	}
	return false
}

// normalize coerces any out-of-range or unknown field back to a safe default so
// a hand-edited or partial blob can never disable the bounds. Mirrors the
// request settings' defensive validSeasonScope fallback.
func (g *Settings) normalize() {
	d := Defaults()
	if !validMode(g.Mode) {
		g.Mode = d.Mode
	}
	// Provider/Model are intentionally not defaulted: empty means "inherit the
	// configured AI provider/model", which is the shipped default.
	if g.MaxSteps <= 0 {
		g.MaxSteps = d.MaxSteps
	} else if g.MaxSteps > maxConfiguredSteps {
		g.MaxSteps = maxConfiguredSteps
	}
	if g.MaxTurnTokens <= 0 {
		g.MaxTurnTokens = d.MaxTurnTokens
	} else if g.MaxTurnTokens > maxConfiguredTurnTokens {
		g.MaxTurnTokens = maxConfiguredTurnTokens
	}
	if g.MaxWallClockSecs <= 0 {
		g.MaxWallClockSecs = d.MaxWallClockSecs
	} else if g.MaxWallClockSecs > maxConfiguredWallClockSecs {
		g.MaxWallClockSecs = maxConfiguredWallClockSecs
	}
	if g.MaxCostMicros <= 0 {
		g.MaxCostMicros = d.MaxCostMicros
	} else if g.MaxCostMicros > maxConfiguredRunCostMicros {
		g.MaxCostMicros = maxConfiguredRunCostMicros
	}
	if g.DailyRunCap <= 0 {
		g.DailyRunCap = d.DailyRunCap
	} else if g.DailyRunCap > maxConfiguredDailyRuns {
		g.DailyRunCap = maxConfiguredDailyRuns
	}
	if g.DailyCostCeilingMicros <= 0 {
		g.DailyCostCeilingMicros = d.DailyCostCeilingMicros
	} else if g.DailyCostCeilingMicros > maxConfiguredDailyCost {
		g.DailyCostCeilingMicros = maxConfiguredDailyCost
	}
	if g.CircuitBreakerGiveups <= 0 {
		g.CircuitBreakerGiveups = d.CircuitBreakerGiveups
	} else if g.CircuitBreakerGiveups > maxConfiguredBreakerGiveups {
		g.CircuitBreakerGiveups = maxConfiguredBreakerGiveups
	}
	if g.MaxUserWaitHours <= 0 {
		g.MaxUserWaitHours = d.MaxUserWaitHours
	} else if g.MaxUserWaitHours > maxConfiguredUserWaitHours {
		g.MaxUserWaitHours = maxConfiguredUserWaitHours
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
