// Package codexapp provides a narrow, per-user adapter for the supported
// Codex app-server protocol. It deliberately exposes only Cantinarr's dynamic
// tools; app-server's local-computer tool surfaces are disabled.
package codexapp

import (
	"encoding/json"
	"errors"
	"time"
)

const (
	deviceLoginLifetime    = 15 * time.Minute
	devicePollInterval     = 2 * time.Second
	maxAuthFileBytes       = 1 << 20
	maxProtocolBytes       = 4 << 20
	maxConcurrentApps      = 8
	maxConcurrentLogins    = 4
	maxSharedWaiters       = 16
	maxServerRequests      = 4
	maxGlobalRequests      = 16
	maxQueuedNotifications = 64
	maxNotificationBytes   = 256 << 10
	maxDynamicToolCalls    = 15
	maxRunDuration         = 5 * time.Minute
	maxAccountOperation    = 30 * time.Second
	maxServerWrite         = 5 * time.Second
	accountStatusTTL       = 60 * time.Second
)

// Options controls app-server discovery and the memory-backed directory used
// while an app-server process is alive. Binary and RuntimeDir are normally
// empty. Args exists for direct app-server wrappers and focused tests.
type Options struct {
	Binary     string
	RuntimeDir string
	Args       []string
	// AllowDiskRuntimeForTests is an explicit escape hatch for fake-process
	// tests. Production wiring must leave it false: auth.json may only exist on
	// a memory-backed filesystem.
	AllowDiskRuntimeForTests bool
}

// AccountRef identifies whose ChatGPT authorization app-server should use.
// It is deliberately separate from the Cantinarr actor whose role and user ID
// authorize tools. The zero value is invalid.
type AccountRef struct {
	userID int64
	shared bool
}

func PersonalAccount(userID int64) AccountRef { return AccountRef{userID: userID} }
func SharedAccount() AccountRef               { return AccountRef{shared: true} }

func (r AccountRef) valid() bool {
	return (r.shared && r.userID == 0) || (!r.shared && r.userID > 0)
}

// AccountStatus contains only display-safe account metadata. Authentication
// tokens and the ChatGPT account routing identifier never cross this boundary.
type AccountStatus struct {
	Connected  bool            `json:"connected"`
	Email      string          `json:"email,omitempty"`
	PlanType   string          `json:"plan_type,omitempty"`
	RateLimits json.RawMessage `json:"rate_limits,omitempty"`
	UpdatedAt  time.Time       `json:"updated_at,omitempty"`
	Stale      bool            `json:"stale"`
}

// DeviceLogin is safe to return to the user who started the flow. FlowID is a
// Cantinarr-generated capability, not app-server's upstream login identifier.
type DeviceLogin struct {
	FlowID          string    `json:"flow_id"`
	VerificationURI string    `json:"verification_uri"`
	UserCode        string    `json:"user_code"`
	ExpiresAt       time.Time `json:"expires_at"`
	IntervalSeconds int       `json:"interval"`
}

// LoginStatus describes the locally observed state of a device login.
type LoginStatus string

const (
	LoginPending   LoginStatus = "pending"
	LoginConnected LoginStatus = "connected"
	LoginExpired   LoginStatus = "expired"
	LoginFailed    LoginStatus = "failed"
)

// DeviceLoginCheck is returned when polling a device login.
type DeviceLoginCheck struct {
	Status  LoginStatus   `json:"status"`
	Account AccountStatus `json:"account,omitempty"`
	Error   string        `json:"error,omitempty"`
}

// Callbacks receives user-visible streaming and Cantinarr tool activity.
// Implementations must be safe for serialized calls from the Run invocation.
type Callbacks struct {
	OnText       func(text string)
	OnToolStart  func(name string)
	OnToolEnd    func(name string, ok bool)
	OnToolResult func(name string, data any)
	// OnToolRecord preserves provider-neutral tool grounding for follow-up
	// turns. input and result are already bounded/redacted at this boundary.
	OnToolRecord func(name string, input json.RawMessage, result string, isError bool)
}

// TurnToolCall is a dynamic-tool request emitted by one autonomous Codex turn.
// The manager validates the tool name and JSON input but deliberately does not
// execute it. The remediation Runner remains the sole dispatcher and applies
// its own scope, budget, evidence, and human-approval gates.
type TurnToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// AutonomousTurnUsage is the per-turn accounting app-server reports for the
// ephemeral remediation thread.
type AutonomousTurnUsage struct {
	InputTokens           int64 `json:"inputTokens"`
	CachedInputTokens     int64 `json:"cachedInputTokens"`
	OutputTokens          int64 `json:"outputTokens"`
	ReasoningOutputTokens int64 `json:"reasoningOutputTokens"`
}

// AutonomousTurnResult is the provider-neutral portion of one Codex turn used
// by Cantinarr's server-owned remediation worker.
type AutonomousTurnResult struct {
	Text               string
	ToolCalls          []TurnToolCall
	Usage              AutonomousTurnUsage
	OutputLimitReached bool
}

// Code identifies a stable, display-safe class of adapter failure.
type Code string

const (
	CodeUnavailable      Code = "unavailable"
	CodeNotConnected     Code = "not_connected"
	CodeFlowNotFound     Code = "flow_not_found"
	CodeFlowExpired      Code = "flow_expired"
	CodeLoginInProgress  Code = "login_in_progress"
	CodeAlreadyConnected Code = "already_connected"
	CodeUsageLimit       Code = "usage_limit"
	CodeProvider         Code = "provider_error"
	CodeStorage          Code = "storage_error"
	CodeInvalidInput     Code = "invalid_input"
	CodeBusy             Code = "busy"
)

// Error is intentionally small: it never wraps process stderr, OAuth payloads,
// auth.json, device codes, or raw upstream errors.
type Error struct {
	Code    Code
	message string
}

func (e *Error) Error() string { return e.message }

func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e.Code == other.Code
}

var (
	ErrUnavailable      = &Error{Code: CodeUnavailable, message: "Codex app-server is unavailable"}
	ErrNotConnected     = &Error{Code: CodeNotConnected, message: "ChatGPT account is not connected"}
	ErrFlowNotFound     = &Error{Code: CodeFlowNotFound, message: "device login was not found"}
	ErrFlowExpired      = &Error{Code: CodeFlowExpired, message: "device login expired"}
	ErrLoginInProgress  = &Error{Code: CodeLoginInProgress, message: "a device login is already in progress"}
	ErrAlreadyConnected = &Error{Code: CodeAlreadyConnected, message: "a ChatGPT account is already connected"}
	ErrUsageLimit       = &Error{Code: CodeUsageLimit, message: "ChatGPT usage limit reached"}
	ErrProvider         = &Error{Code: CodeProvider, message: "Codex app-server request failed"}
	ErrStorage          = &Error{Code: CodeStorage, message: "Codex account storage failed"}
	ErrInvalidInput     = &Error{Code: CodeInvalidInput, message: "invalid Codex request"}
	ErrBusy             = &Error{Code: CodeBusy, message: "Codex account is busy"}
)

// IsCode is a convenience for HTTP adapters that map safe error classes to
// stable status codes without inspecting messages.
func IsCode(err error, code Code) bool {
	return errors.Is(err, &Error{Code: code})
}
