// Package grokoauth links xAI Grok subscriptions (SuperGrok or X Premium+)
// to Cantinarr with the OAuth 2.0 device-authorization flow xAI serves for
// coding agents. Unlike the Codex integration there is no external app-server:
// this package speaks the device flow to auth.x.ai directly and hands the
// resulting bearer token to the OpenAI-compatible api.x.ai chat client.
package grokoauth

import (
	"encoding/json"
	"errors"
	"time"
)

const (
	// sharedGrokClientID is xAI's public Grok CLI OAuth client. The device
	// flow uses no client secret, so this value is public metadata by design;
	// third-party agents authenticate with the same client id.
	sharedGrokClientID = "b1a00492-073a-47ea-816f-4c329264a828"

	// defaultAuthBaseURL is pinned; only tests may point elsewhere.
	defaultAuthBaseURL = "https://auth.x.ai"

	deviceCodePath = "/oauth2/device/code"
	tokenPath      = "/oauth2/token"

	oauthScope = "openid profile email offline_access grok-cli:access api:access"

	deviceLoginLifetime = 15 * time.Minute
	defaultPollInterval = 5 * time.Second
	maxPollInterval     = 30 * time.Second
	upstreamTimeout     = 15 * time.Second
	maxResponseBytes    = 64 << 10
	maxPendingFlows     = 100

	// refreshSkew refreshes ahead of expiry so a token stays valid for the
	// whole turn it was fetched for. Short-lived tokens shrink the skew so a
	// single-use refresh token is not burned on every resolution.
	refreshSkew        = 5 * time.Minute
	shortTokenLifetime = 15 * time.Minute
	shortTokenSkew     = 2 * time.Minute

	// defaultTokenLifetime is assumed when a token response carries neither
	// expires_in nor a readable JWT exp claim, keeping the refresh cycle
	// alive instead of pinning the account to its first access token.
	defaultTokenLifetime = time.Hour
)

// AccountRef identifies whose xAI authorization to use. It is deliberately
// separate from the Cantinarr actor whose role authorizes tools. The zero
// value is invalid.
type AccountRef struct {
	userID int64
	shared bool
}

func PersonalAccount(userID int64) AccountRef { return AccountRef{userID: userID} }
func SharedAccount() AccountRef               { return AccountRef{shared: true} }

func (r AccountRef) valid() bool {
	return (r.shared && r.userID == 0) || (!r.shared && r.userID > 0)
}

// AccountStatus contains only display-safe account metadata. Tokens never
// cross this boundary.
type AccountStatus struct {
	Connected bool      `json:"connected"`
	Email     string    `json:"email,omitempty"`
	PlanType  string    `json:"plan_type,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// DeviceLogin is safe to return to the user who started the flow. FlowID is a
// Cantinarr-generated capability; the upstream device_code never leaves the
// server.
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

// Code identifies a stable, display-safe class of failure.
type Code string

const (
	CodeUnavailable      Code = "unavailable"
	CodeNotConnected     Code = "not_connected"
	CodeFlowNotFound     Code = "flow_not_found"
	CodeFlowExpired      Code = "flow_expired"
	CodeLoginInProgress  Code = "login_in_progress"
	CodeAlreadyConnected Code = "already_connected"
	CodeReloginRequired  Code = "relogin_required"
	CodeProvider         Code = "provider_error"
	CodeStorage          Code = "storage_error"
	CodeInvalidInput     Code = "invalid_input"
)

// Error is intentionally small: it never wraps OAuth payloads, tokens, device
// codes, or raw upstream errors.
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
	ErrUnavailable      = &Error{Code: CodeUnavailable, message: "Grok OAuth is unavailable"}
	ErrNotConnected     = &Error{Code: CodeNotConnected, message: "xAI account is not connected"}
	ErrFlowNotFound     = &Error{Code: CodeFlowNotFound, message: "device login was not found"}
	ErrFlowExpired      = &Error{Code: CodeFlowExpired, message: "device login expired"}
	ErrLoginInProgress  = &Error{Code: CodeLoginInProgress, message: "a device login is already in progress"}
	ErrAlreadyConnected = &Error{Code: CodeAlreadyConnected, message: "an xAI account is already connected"}
	ErrReloginRequired  = &Error{Code: CodeReloginRequired, message: "xAI sign-in has expired; reconnect the account"}
	ErrProvider         = &Error{Code: CodeProvider, message: "xAI sign-in request failed"}
	ErrStorage          = &Error{Code: CodeStorage, message: "Grok account storage failed"}
	ErrInvalidInput     = &Error{Code: CodeInvalidInput, message: "invalid Grok request"}
)

// IsCode is a convenience for HTTP adapters that map safe error classes to
// stable status codes without inspecting messages.
func IsCode(err error, code Code) bool {
	return errors.Is(err, &Error{Code: code})
}

// storedAuth is the encrypted-at-rest authorization record.
type storedAuth struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	// ExpiresAt and ObtainedAt are unix seconds; together they recover the
	// token lifetime so the refresh skew can adapt to short-lived tokens.
	ExpiresAt  int64 `json:"expires_at"`
	ObtainedAt int64 `json:"obtained_at"`
}

func validAuthJSON(raw []byte) bool {
	var auth storedAuth
	if err := json.Unmarshal(raw, &auth); err != nil {
		return false
	}
	return auth.AccessToken != ""
}
