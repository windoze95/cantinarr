package auth

import "time"

type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
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
	Username string `json:"username"`
	Password string `json:"password"`
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
	Username string `json:"username"`
	Password string `json:"password"`
}

type AuthStatusResponse struct {
	NeedsSetup        bool `json:"needs_setup"`
	WebAuthnAvailable bool `json:"webauthn_available"`
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
