package auth

import "time"

type User struct {
	ID           int64        `json:"id"`
	Username     string       `json:"username"`
	PasswordHash string       `json:"-"`
	Role         string       `json:"role"`
	Permissions  []Permission `json:"permissions,omitempty"`
	// PasswordEnabled / PasskeyEnabled are admin-controlled policy: whether the
	// account may create a password / register a passkey. Both default off for
	// new users so the default sign-in is a connect link.
	PasswordEnabled bool `json:"password_enabled"`
	PasskeyEnabled  bool `json:"passkey_enabled"`
	// PlexEmail is the email the user shared so an admin can invite them to
	// the Plex server. Empty until the user submits one. PlexInvitedAt is
	// when Cantinarr last sent their invite (one-tap or auto) — a record of
	// our action, not a claim about current access in Plex.
	PlexEmail     string     `json:"plex_email"`
	PlexInvitedAt *time.Time `json:"plex_invited_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// UserSummary is an enriched view of a user for admin user-management screens.
// It augments the base account with device and invite state so admins can tell
// active accounts apart from pending invites at a glance.
type UserSummary struct {
	ID               int64        `json:"id"`
	Username         string       `json:"username"`
	Role             string       `json:"role"`
	Permissions      []Permission `json:"permissions"`
	CreatedAt        time.Time    `json:"created_at"`
	DeviceCount      int          `json:"device_count"`
	HasPassword      bool         `json:"has_password"`
	PasswordEnabled  bool         `json:"password_enabled"`
	PasskeyEnabled   bool         `json:"passkey_enabled"`
	HasPendingInvite bool         `json:"has_pending_invite"`
	PlexEmail        string       `json:"plex_email"`
	PlexInvitedAt    *time.Time   `json:"plex_invited_at,omitempty"`
}

// UpdateUserRoleRequest changes a user's role.
type UpdateUserRoleRequest struct {
	Role string `json:"role"`
}

// UpdateUserAuthMethodsRequest toggles whether a user may use a password and/or
// passkeys. Omitted (nil) fields are left unchanged.
type UpdateUserAuthMethodsRequest struct {
	PasswordEnabled *bool `json:"password_enabled"`
	PasskeyEnabled  *bool `json:"passkey_enabled"`
}

type Device struct {
	ID         string     `json:"id"`
	UserID     int64      `json:"user_id"`
	DeviceName string     `json:"device_name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt time.Time  `json:"last_seen_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type ConnectToken struct {
	Token      string     `json:"token"`
	UserID     int64      `json:"user_id"`
	CreatedBy  int64      `json:"created_by"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RedeemedAt *time.Time `json:"redeemed_at,omitempty"`
}

type LoginRequest struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	DeviceName string `json:"device_name"`
	HardwareID string `json:"hardware_id"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	User         User   `json:"user"`
	DeviceID     string `json:"device_id,omitempty"`
}

type CreateConnectTokenRequest struct {
	Name      string `json:"name"`
	ServerURL string `json:"server_url"`
}

type CreateConnectTokenResponse struct {
	Link      string    `json:"link"`
	ExpiresAt time.Time `json:"expires_at"`
}

type RedeemConnectTokenRequest struct {
	Token      string `json:"token"`
	DeviceName string `json:"device_name"`
	// HardwareID is a stable per-device identifier (e.g. iOS
	// identifierForVendor) used to dedupe reconnects of the same physical
	// device. Optional: empty when the client can't provide one (e.g. web).
	HardwareID string `json:"hardware_id"`
}

type DeviceInfo struct {
	ID         string    `json:"id"`
	UserID     int64     `json:"user_id"`
	Username   string    `json:"username"`
	DeviceName string    `json:"device_name"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

type SetupRequest struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	DeviceName string `json:"device_name"`
	HardwareID string `json:"hardware_id"`
}

// SetPasswordRequest sets or replaces the authenticated user's password.
type SetPasswordRequest struct {
	Password string `json:"password"`
}

// SetPlexEmailRequest sets or replaces the email the authenticated user wants
// their Plex invite sent to.
type SetPlexEmailRequest struct {
	Email string `json:"email"`
}

type AuthStatusResponse struct {
	NeedsSetup        bool                        `json:"needs_setup"`
	WebAuthnAvailable bool                        `json:"webauthn_available"`
	NativePasskeys    NativePasskeyStatusResponse `json:"native_passkeys"`
}

type NativePasskeyStatusResponse struct {
	AppleConfigured      bool `json:"apple_configured"`
	AndroidConfigured    bool `json:"android_configured"`
	WindowsOriginTrusted bool `json:"windows_origin_trusted"`
}

type PasskeyInfo struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

type FinishRegistrationRequest struct {
	SessionID      string `json:"session_id"`
	CredentialName string `json:"credential_name"`
}
