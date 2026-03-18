package auth

import "time"

type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
}

type InviteCode struct {
	Code      string     `json:"code"`
	CreatedBy int64      `json:"created_by"`
	UsedBy    *int64     `json:"used_by,omitempty"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
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

type RegisterRequest struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	InviteCode string `json:"invite_code"`
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

type InviteResponse struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
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
