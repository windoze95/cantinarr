package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// hashToken returns a hex-encoded SHA-256 hash of the given token string.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

var (
	ErrInvalidCredentials    = errors.New("invalid credentials")
	ErrUserExists            = errors.New("username already taken")
	ErrTokenExpired          = errors.New("connect token has expired")
	ErrTokenRedeemed         = errors.New("connect token has already been used")
	ErrTokenNotFound         = errors.New("connect token not found")
	ErrDeviceRevoked         = errors.New("device has been revoked")
	ErrDeviceNotFound        = errors.New("device not found")
	ErrSetupAlreadyComplete  = errors.New("setup has already been completed")
	ErrUserNotFound          = errors.New("user not found")
	ErrInvalidRole           = errors.New("invalid role")
	ErrLastAdmin             = errors.New("cannot remove the last admin")
	ErrCannotDeleteSelf      = errors.New("cannot delete your own account")
	ErrPasswordTooShort      = errors.New("password is too short")
	ErrPasswordNotAllowed    = errors.New("password sign-in is not enabled for this account")
	ErrPasskeyNotAllowed     = errors.New("passkeys are not enabled for this account")
	ErrCannotModifyAdmin     = errors.New("cannot change sign-in methods for an admin")
	ErrInvalidPlexEmail      = errors.New("invalid email address")
	ErrSharedAIAccessRevoked = errors.New("shared AI access has been revoked")
	ErrPermissionDenied      = errors.New("permission denied")
	// ErrAuthUnavailable marks a failure to *evaluate* credentials (DB error,
	// signing error) as opposed to a rejection of them. Handlers must map it to
	// a 5xx, never a 401: clients treat a 401 as "this session is dead" and
	// erase their stored tokens, so answering a transient fault with 401 logs
	// the user out permanently.
	ErrAuthUnavailable = errors.New("authentication temporarily unavailable")
)

// minPasswordLength is the minimum length for an account password. It matches
// the check enforced during first-run setup.
const minPasswordLength = 8

const (
	// opaqueRefreshPrefix marks the current refresh-token scheme: an opaque
	// random secret validated purely against the refresh_tokens table. Device
	// sessions built on it never expire, never rotate, and do not depend on
	// the JWT secret — a session survives server restarts, upgrades, JWT
	// secret changes, and any amount of idle time. The ONLY ways to end one
	// are explicit: revoke the device or delete the user. That is the product
	// contract for passwordless household accounts (a connect link is
	// redeemed once; the resulting session must never silently die).
	opaqueRefreshPrefix = "cnr1."

	// legacyRefreshMinLifetime separates legacy JWT *refresh* tokens (issued
	// with 30- or 365-day expiries by older versions) from access tokens
	// (always 15 minutes). Only tokens minted with a lifetime above this bar
	// are accepted on the refresh amnesty path, so a leaked short-lived
	// access token can never be laundered into a permanent session.
	legacyRefreshMinLifetime = 24 * time.Hour
)

// refreshNeverExpires is the expires_at sentinel stored for opaque refresh
// tokens. The column is NOT NULL in existing databases, so "no expiry" is a
// date the periodic cleanup can never reach.
var refreshNeverExpires = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

// passwordAllowed reports whether the account may use password sign-in. Admins
// are always allowed so an install can never be locked out of its own server.
func passwordAllowed(u *User) bool {
	return u.Role == RoleAdmin || u.PasswordEnabled
}

// passkeyAllowed reports whether the account may register and use passkeys.
// Admins are always allowed.
func passkeyAllowed(u *User) bool {
	return u.Role == RoleAdmin || u.PasskeyEnabled
}

type Claims struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	DeviceID string `json:"device_id,omitempty"`
	Scope    string `json:"scope,omitempty"`
	jwt.RegisteredClaims
}

type Service struct {
	db                      *sql.DB
	jwtSecret               []byte
	webauthnSessions        *SessionStore
	webauthnOrigins         []string
	appleAppIDs             []string
	androidCertFingerprints []string
}

type WebAuthnConfig struct {
	ExtraOrigins            []string
	AppleAppIDs             []string
	AndroidCertFingerprints []string
}

func NewService(db *sql.DB, jwtSecret string, webauthnConfig ...WebAuthnConfig) *Service {
	var extraOrigins []string
	var appleAppIDs []string
	var androidCertFingerprints []string
	if len(webauthnConfig) > 0 {
		cfg := webauthnConfig[0]
		extraOrigins = append(extraOrigins, cfg.ExtraOrigins...)
		appleAppIDs = append(appleAppIDs, cfg.AppleAppIDs...)
		androidCertFingerprints = append(
			androidCertFingerprints,
			cfg.AndroidCertFingerprints...,
		)
	}
	return &Service{
		db:                      db,
		jwtSecret:               []byte(jwtSecret),
		webauthnSessions:        NewSessionStore(),
		webauthnOrigins:         extraOrigins,
		appleAppIDs:             appleAppIDs,
		androidCertFingerprints: androidCertFingerprints,
	}
}

// EnsureAdmin creates a default "admin" user from the CANTINARR_ADMIN_PASSWORD env var.
// Deprecated: Use the interactive setup wizard instead. This is kept for backward
// compatibility and will be removed in a future version.
func (s *Service) EnsureAdmin(adminPassword string) error {
	if adminPassword == "" {
		return nil
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return fmt.Errorf("check users: %w", err)
	}
	if count > 0 {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(adminPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	_, err = s.db.Exec("INSERT INTO users (username, password_hash, role, password_enabled, passkey_enabled, ai_shared_enabled) VALUES (?, ?, ?, 1, 1, 1)", "admin", string(hash), "admin")
	if err != nil {
		return fmt.Errorf("create admin: %w", err)
	}
	// Mark setup as complete since we created a user via env var
	_, _ = s.db.Exec("INSERT OR IGNORE INTO settings (key, value) VALUES ('setup_completed', 'true')")
	return nil
}

// IsSetupComplete checks whether initial setup has been completed.
func (s *Service) IsSetupComplete() bool {
	var val string
	err := s.db.QueryRow("SELECT value FROM settings WHERE key = 'setup_completed'").Scan(&val)
	return err == nil && val == "true"
}

// Setup creates the initial admin account during first-run setup.
// Returns JWT tokens so the user is automatically logged in.
// The entire operation is wrapped in a transaction to prevent race conditions.
func (s *Service) Setup(username, password, deviceName, hardwareID string) (*TokenResponse, error) {
	if s.IsSetupComplete() {
		return nil, ErrSetupAlreadyComplete
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Re-check inside the transaction (SQLite serializes writes, so this is safe)
	var setupVal string
	if err := tx.QueryRow("SELECT value FROM settings WHERE key = 'setup_completed'").Scan(&setupVal); err == nil && setupVal == "true" {
		return nil, ErrSetupAlreadyComplete
	}

	// Mark setup as complete FIRST to prevent concurrent setup attempts
	_, err = tx.Exec("INSERT INTO settings (key, value) VALUES ('setup_completed', 'true')")
	if err != nil {
		return nil, fmt.Errorf("mark setup complete: %w", err)
	}

	result, err := tx.Exec(
		"INSERT INTO users (username, password_hash, role, password_enabled, passkey_enabled, ai_shared_enabled) VALUES (?, ?, ?, 1, 1, 1)",
		username, string(hash), "admin",
	)
	if err != nil {
		return nil, ErrUserExists
	}
	userID, _ := result.LastInsertId()

	if deviceName == "" {
		deviceName = "Unknown Device"
	}
	deviceID := uuid.New().String()
	_, err = tx.Exec(
		"INSERT INTO devices (id, user_id, device_name, hardware_id) VALUES (?, ?, ?, ?)",
		deviceID, userID, deviceName, hardwareID,
	)
	if err != nil {
		return nil, fmt.Errorf("create device: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit setup: %w", err)
	}

	user := &User{
		ID:       userID,
		Username: username,
		Role:     "admin",
	}
	resp, err := s.generateTokens(user, deviceID)
	if err != nil {
		return nil, err
	}
	resp.DeviceID = deviceID
	return resp, nil
}

// MigrateSetupState ensures the setup_completed flag is consistent.
// If users exist but setup_completed is not set, it inserts it.
// This handles existing deployments upgrading to the setup wizard.
func (s *Service) MigrateSetupState() error {
	if s.IsSetupComplete() {
		return nil
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return fmt.Errorf("check users: %w", err)
	}
	if count > 0 {
		_, err := s.db.Exec("INSERT OR IGNORE INTO settings (key, value) VALUES ('setup_completed', 'true')")
		if err != nil {
			return fmt.Errorf("migrate setup state: %w", err)
		}
	}
	return nil
}

func (s *Service) Login(username, password, deviceName, hardwareID string) (*TokenResponse, error) {
	user, err := s.getUserByUsername(username)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	if !passwordAllowed(user) {
		return nil, ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	deviceID, err := s.upsertDevice(user.ID, deviceName, hardwareID)
	if err != nil {
		return nil, err
	}

	resp, err := s.generateTokens(user, deviceID)
	if err != nil {
		return nil, err
	}
	resp.DeviceID = deviceID
	return resp, nil
}

// SetPassword creates or replaces a user's password. It backs self-service
// password creation — so users on plain HTTP, where passkeys require a secure
// context and are unavailable, can sign in with a password and authorize MCP
// clients — and password resets after an admin-issued connect link is redeemed.
//
// A valid session (enforced by the auth middleware) is sufficient to set the
// password; no current password is required. This matches passkey registration,
// which also re-uses the existing session without re-auth, and it is what lets
// the connect-link reset flow recover a user who has forgotten their password.
func (s *Service) SetPassword(userID int64, newPassword string) error {
	user, err := s.getUserByID(userID)
	if err != nil {
		return ErrUserNotFound
	}
	if !passwordAllowed(user) {
		return ErrPasswordNotAllowed
	}
	if len(newPassword) < minPasswordLength {
		return ErrPasswordTooShort
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	if _, err := s.db.Exec(
		"UPDATE users SET password_hash = ? WHERE id = ?",
		string(hash), userID,
	); err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	return nil
}

// SetPlexEmail stores the email the user wants their Plex invite sent to and
// reports whether it actually changed, so callers can skip re-notifying admins
// when a user resubmits the same address. Validation is deliberately shallow
// (shape + length): the address is only ever displayed to an admin who pastes
// it into Plex, never used for delivery by us.
func (s *Service) SetPlexEmail(userID int64, email string) (bool, error) {
	email = strings.TrimSpace(email)
	if !plexEmailValid(email) {
		return false, ErrInvalidPlexEmail
	}

	user, err := s.getUserByID(userID)
	if err != nil {
		return false, ErrUserNotFound
	}
	if user.PlexEmail == email {
		return false, nil
	}

	// A changed address also clears the invited stamp: any invite already
	// sent went to the OLD email, so the user is back to "waiting".
	if _, err := s.db.Exec(
		"UPDATE users SET plex_email = ?, plex_invited_at = NULL WHERE id = ?",
		email, userID,
	); err != nil {
		return false, fmt.Errorf("update plex email: %w", err)
	}
	return true, nil
}

// plexEmailValid is a shape check, not RFC validation: something@something
// with no spaces, short enough for a users-table column.
func plexEmailValid(email string) bool {
	if email == "" || len(email) > 254 || strings.ContainsAny(email, " \t\n") {
		return false
	}
	at := strings.Index(email, "@")
	return at > 0 && at < len(email)-1
}

// Refresh exchanges a refresh token for a fresh access token. This is the one
// call that keeps a passwordless session alive, so its failure contract is
// strict: it returns ErrInvalidCredentials / ErrDeviceRevoked ONLY when the
// session is genuinely dead (token forged or revoked, device revoked, user
// deleted). Every fault in *evaluating* the token wraps ErrAuthUnavailable so
// the handler answers 503 and the client retries instead of logging out.
func (s *Service) Refresh(refreshToken string) (*TokenResponse, error) {
	if strings.HasPrefix(refreshToken, opaqueRefreshPrefix) {
		return s.refreshOpaque(refreshToken)
	}
	return s.refreshLegacyJWT(refreshToken)
}

// refreshOpaque validates a current-scheme refresh token against the store and
// issues a new access token. The refresh token itself is returned unchanged:
// no rotation means there is no rotated value the client can fail to persist,
// so a crash, a lost response, or server downtime mid-refresh can never strand
// a device. Replay containment comes from device revocation, not rotation.
func (s *Service) refreshOpaque(refreshToken string) (*TokenResponse, error) {
	var (
		deviceID string
		userID   int64
	)
	err := s.db.QueryRow(
		"SELECT device_id, user_id FROM refresh_tokens WHERE token_hash = ?",
		hashToken(refreshToken),
	).Scan(&deviceID, &userID)
	if errors.Is(err, sql.ErrNoRows) {
		// Never issued, or deleted by device revocation / user deletion.
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("%w: look up refresh token: %v", ErrAuthUnavailable, err)
	}

	if err := s.requireActiveDevice(deviceID, userID); err != nil {
		return nil, err
	}
	user, err := s.getUserForAuth(userID)
	if err != nil {
		return nil, err
	}

	accessToken, err := s.signAccessToken(user, deviceID)
	if err != nil {
		return nil, err
	}
	return &TokenResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		User:         userWithPermissions(user),
		DeviceID:     deviceID,
	}, nil
}

// refreshLegacyJWT is the amnesty path for refresh tokens issued before the
// opaque scheme: JWTs whose 30/365-day expiry, single-use rotation, and store
// row made sessions die from idle time, crash-vs-rotation races, restores, or
// restarts. A legacy token is accepted when its signature verifies and its
// device is still authorized — deliberately ignoring its baked-in expiry and
// whether its store row was rotated or lost, because the device row is the
// real authority and admins revoke devices, not tokens. On success the client
// is migrated to an opaque token and never touches this path again.
//
// The gate is strict about what counts as a refresh token: audience-free,
// device-bound, and minted with a multi-day lifetime. Access tokens (15 min)
// and OAuth tokens (audience-bound) can never pass, and forgeries still fail
// the signature check.
func (s *Service) refreshLegacyJWT(tokenStr string) (*TokenResponse, error) {
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithoutClaimsValidation(),
	)
	token, err := parser.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		return s.jwtSecret, nil
	})
	if err != nil || !token.Valid {
		return nil, ErrInvalidCredentials
	}
	claims, ok := token.Claims.(*Claims)
	if !ok {
		return nil, ErrInvalidCredentials
	}
	if len(claims.Audience) > 0 || claims.Scope != "" || claims.DeviceID == "" {
		return nil, ErrInvalidCredentials
	}
	if claims.ExpiresAt == nil || claims.IssuedAt == nil ||
		claims.ExpiresAt.Sub(claims.IssuedAt.Time) < legacyRefreshMinLifetime {
		return nil, ErrInvalidCredentials
	}

	if err := s.requireActiveDevice(claims.DeviceID, claims.UserID); err != nil {
		return nil, err
	}
	user, err := s.getUserForAuth(claims.UserID)
	if err != nil {
		return nil, err
	}

	// Migrate: issue the opaque pair. The legacy row (if any) is left to age
	// out via the expires_at cleanup; nothing consults it anymore.
	resp, err := s.generateTokens(user, claims.DeviceID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAuthUnavailable, err)
	}
	resp.DeviceID = claims.DeviceID
	return resp, nil
}

// requireActiveDevice confirms the device row exists, belongs to userID, and
// is not revoked, then bumps last_seen_at. Missing/mismatched rows are a
// genuine rejection; query faults are ErrAuthUnavailable.
func (s *Service) requireActiveDevice(deviceID string, userID int64) error {
	var (
		ownerID   int64
		revokedAt sql.NullTime
	)
	err := s.db.QueryRow(
		"SELECT user_id, revoked_at FROM devices WHERE id = ?", deviceID,
	).Scan(&ownerID, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidCredentials
	}
	if err != nil {
		return fmt.Errorf("%w: look up device: %v", ErrAuthUnavailable, err)
	}
	if ownerID != userID {
		return ErrInvalidCredentials
	}
	if revokedAt.Valid {
		return ErrDeviceRevoked
	}
	_, _ = s.db.Exec(
		"UPDATE devices SET last_seen_at = ? WHERE id = ?",
		time.Now(), deviceID,
	)
	return nil
}

// getUserForAuth loads a user for a credential check: a missing row is a
// rejection (account deleted), any other failure is ErrAuthUnavailable.
func (s *Service) getUserForAuth(userID int64) (*User, error) {
	user, err := s.getUserByID(userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("%w: load user: %v", ErrAuthUnavailable, err)
	}
	return user, nil
}

func (s *Service) GetUser(userID int64) (*User, error) {
	user, err := s.getUserByID(userID)
	if err != nil {
		return nil, err
	}
	withPerms := userWithPermissions(user)
	return &withPerms, nil
}

func (s *Service) CreateConnectToken(createdBy int64, name, serverURL string) (*CreateConnectTokenResponse, error) {
	// Find or create the passwordless user this invite links to. Two admins
	// (or one admin double-submitting) creating the first invite for the same
	// new username must converge on one user row rather than racing the
	// username UNIQUE constraint and failing one caller with a 500.
	user, err := s.getUserByUsername(name)
	if err != nil {
		if _, err := s.db.Exec(
			"INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?) ON CONFLICT(username) DO NOTHING",
			name, "", "user",
		); err != nil {
			return nil, fmt.Errorf("create user: %w", err)
		}
		user, err = s.getUserByUsername(name)
		if err != nil {
			return nil, fmt.Errorf("load connect user: %w", err)
		}
	}
	userID := user.ID

	// Generate 32-byte random token (64 hex chars)
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)

	expiresAt := time.Now().Add(7 * 24 * time.Hour)
	_, err = s.db.Exec(
		"INSERT INTO connect_tokens (token, user_id, created_by, expires_at) VALUES (?, ?, ?, ?)",
		token, userID, createdBy, expiresAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert connect token: %w", err)
	}

	link := fmt.Sprintf("cantinarr://connect?token=%s&server=%s", token, url.QueryEscape(serverURL))
	return &CreateConnectTokenResponse{Link: link, ExpiresAt: expiresAt}, nil
}

// upsertDevice binds a session to a device row for userID. When hardwareID is
// non-empty it reuses the user's existing non-revoked row for the same physical
// device (refreshing its name and last_seen) so reconnects don't accumulate
// duplicate entries; otherwise it always inserts. deviceName falls back to a
// generic label so every row is non-empty. Returns the device id. Shared by all
// app auth paths (connect link, password login, setup, passkey) so a device is
// named and deduped identically no matter how the session was established.
func (s *Service) upsertDevice(userID int64, deviceName, hardwareID string) (string, error) {
	if deviceName == "" {
		deviceName = "Unknown Device"
	}
	if hardwareID != "" {
		var existingID string
		err := s.db.QueryRow(
			`SELECT id FROM devices
			 WHERE user_id = ? AND hardware_id = ? AND revoked_at IS NULL
			 ORDER BY last_seen_at DESC LIMIT 1`,
			userID, hardwareID,
		).Scan(&existingID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("lookup device: %w", err)
		}
		if existingID != "" {
			if _, err := s.db.Exec(
				"UPDATE devices SET device_name = ?, last_seen_at = ? WHERE id = ?",
				deviceName, time.Now(), existingID,
			); err != nil {
				return "", fmt.Errorf("update device: %w", err)
			}
			return existingID, nil
		}
	}
	deviceID := uuid.New().String()
	if _, err := s.db.Exec(
		"INSERT INTO devices (id, user_id, device_name, hardware_id) VALUES (?, ?, ?, ?)",
		deviceID, userID, deviceName, hardwareID,
	); err != nil {
		return "", fmt.Errorf("create device: %w", err)
	}
	return deviceID, nil
}

func (s *Service) RedeemConnectToken(token, deviceName, hardwareID string) (*TokenResponse, error) {
	var ct ConnectToken
	err := s.db.QueryRow(
		"SELECT token, user_id, created_by, expires_at, redeemed_at FROM connect_tokens WHERE token = ?", token,
	).Scan(&ct.Token, &ct.UserID, &ct.CreatedBy, &ct.ExpiresAt, &ct.RedeemedAt)
	if err != nil {
		return nil, ErrTokenNotFound
	}
	if ct.RedeemedAt != nil {
		return nil, ErrTokenRedeemed
	}
	if time.Now().After(ct.ExpiresAt) {
		return nil, ErrTokenExpired
	}

	// Claim the single-use token atomically. The redeemed_at IS NULL guard means
	// only the first of two concurrent redemptions of the same leaked link marks
	// the row; the loser affects zero rows and is rejected before it can mint a
	// second authenticated device session.
	now := time.Now()
	result, err := s.db.Exec(
		"UPDATE connect_tokens SET redeemed_at = ? WHERE token = ? AND redeemed_at IS NULL", now, token,
	)
	if err != nil {
		return nil, fmt.Errorf("mark token redeemed: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		return nil, ErrTokenRedeemed
	}

	deviceID, err := s.upsertDevice(ct.UserID, deviceName, hardwareID)
	if err != nil {
		return nil, err
	}

	user, err := s.getUserByID(ct.UserID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	resp, err := s.generateTokens(user, deviceID)
	if err != nil {
		return nil, err
	}
	resp.DeviceID = deviceID
	return resp, nil
}

func (s *Service) ListDevices() ([]DeviceInfo, error) {
	rows, err := s.db.Query(`
		SELECT d.id, d.user_id, u.username, d.device_name, d.created_at, d.last_seen_at
		FROM devices d
		JOIN users u ON u.id = d.user_id
		WHERE d.revoked_at IS NULL
		ORDER BY d.last_seen_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query devices: %w", err)
	}
	defer rows.Close()

	var devices []DeviceInfo
	for rows.Next() {
		var d DeviceInfo
		if err := rows.Scan(&d.ID, &d.UserID, &d.Username, &d.DeviceName, &d.CreatedAt, &d.LastSeenAt); err != nil {
			return nil, fmt.Errorf("scan device: %w", err)
		}
		devices = append(devices, d)
	}
	if devices == nil {
		devices = []DeviceInfo{}
	}
	return devices, nil
}

func (s *Service) RevokeDevice(deviceID string) error {
	result, err := s.db.Exec(
		"UPDATE devices SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL",
		time.Now(), deviceID,
	)
	if err != nil {
		return fmt.Errorf("revoke device: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrDeviceNotFound
	}
	// Invalidate all refresh tokens for this device
	_, _ = s.db.Exec("DELETE FROM refresh_tokens WHERE device_id = ?", deviceID)
	_, _ = s.db.Exec("DELETE FROM oauth_refresh_tokens WHERE device_id = ?", deviceID)
	return nil
}

// ListUsers returns every account enriched with device counts, password state,
// and whether an unredeemed connect-link invite is still outstanding.
func (s *Service) ListUsers() ([]UserSummary, error) {
	rows, err := s.db.Query(`
		SELECT
			u.id,
			u.username,
			u.role,
			u.created_at,
			u.password_hash != '' AS has_password,
			u.password_enabled,
			u.passkey_enabled,
			u.ai_shared_enabled,
			u.plex_email,
			u.plex_invited_at,
			(SELECT COUNT(*) FROM devices d WHERE d.user_id = u.id AND d.revoked_at IS NULL) AS device_count,
			EXISTS(
				SELECT 1 FROM connect_tokens ct
				WHERE ct.user_id = u.id AND ct.redeemed_at IS NULL AND ct.expires_at > ?
			) AS has_pending_invite
		FROM users u
		ORDER BY u.id
	`, time.Now())
	if err != nil {
		return nil, fmt.Errorf("query users: %w", err)
	}
	defer rows.Close()

	var users []UserSummary
	for rows.Next() {
		var u UserSummary
		var invitedAt sql.NullTime
		if err := rows.Scan(
			&u.ID, &u.Username, &u.Role, &u.CreatedAt,
			&u.HasPassword, &u.PasswordEnabled, &u.PasskeyEnabled, &u.AISharedEnabled, &u.PlexEmail, &invitedAt,
			&u.DeviceCount, &u.HasPendingInvite,
		); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		if invitedAt.Valid {
			u.PlexInvitedAt = &invitedAt.Time
		}
		u.Permissions = PermissionsForRole(u.Role)
		users = append(users, u)
	}
	if users == nil {
		users = []UserSummary{}
	}
	return users, nil
}

// SetUserAISharedAccess grants or revokes use of the administrator-funded AI
// profile. It deliberately does not delete or modify the user's personal AI
// settings and credentials.
func (s *Service) SetUserAISharedAccess(userID int64, enabled bool) (*UserSummary, error) {
	result, err := s.db.Exec("UPDATE users SET ai_shared_enabled = ? WHERE id = ?", enabled, userID)
	if err != nil {
		return nil, fmt.Errorf("update shared AI access: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("count shared AI access update: %w", err)
	}
	if affected != 1 {
		return nil, ErrUserNotFound
	}
	return s.userSummaryByID(userID)
}

// UpdateUserRole changes a user's role. It rejects unknown roles and refuses to
// demote the last remaining admin so an install can never be locked out.
func (s *Service) UpdateUserRole(userID int64, role string) (*UserSummary, error) {
	if role != RoleAdmin && role != RoleUser {
		return nil, ErrInvalidRole
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var currentRole string
	if err := tx.QueryRow("SELECT role FROM users WHERE id = ?", userID).Scan(&currentRole); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("load user: %w", err)
	}

	if currentRole == RoleAdmin && role != RoleAdmin {
		var adminCount int
		if err := tx.QueryRow("SELECT COUNT(*) FROM users WHERE role = ?", RoleAdmin).Scan(&adminCount); err != nil {
			return nil, fmt.Errorf("count admins: %w", err)
		}
		if adminCount <= 1 {
			return nil, ErrLastAdmin
		}
	}

	if _, err := tx.Exec("UPDATE users SET role = ? WHERE id = ?", role, userID); err != nil {
		return nil, fmt.Errorf("update role: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit role change: %w", err)
	}

	return s.userSummaryByID(userID)
}

// SetUserAuthMethods enables or disables password and/or passkey sign-in for a
// user. Enabling lets the user create that credential; disabling is a real
// revoke — it clears the stored password / deletes the user's passkeys so the
// method stops working immediately (the user's existing device session is left
// intact). Admins always retain both methods and cannot be modified here, so an
// install can never be locked out of password and passkey sign-in at once.
func (s *Service) SetUserAuthMethods(userID int64, passwordEnabled, passkeyEnabled *bool) (*UserSummary, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var role string
	if err := tx.QueryRow("SELECT role FROM users WHERE id = ?", userID).Scan(&role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("load user: %w", err)
	}
	if role == RoleAdmin {
		return nil, ErrCannotModifyAdmin
	}

	if passwordEnabled != nil {
		if _, err := tx.Exec("UPDATE users SET password_enabled = ? WHERE id = ?", *passwordEnabled, userID); err != nil {
			return nil, fmt.Errorf("update password_enabled: %w", err)
		}
		if !*passwordEnabled {
			if _, err := tx.Exec("UPDATE users SET password_hash = '' WHERE id = ?", userID); err != nil {
				return nil, fmt.Errorf("clear password: %w", err)
			}
		}
	}

	if passkeyEnabled != nil {
		if _, err := tx.Exec("UPDATE users SET passkey_enabled = ? WHERE id = ?", *passkeyEnabled, userID); err != nil {
			return nil, fmt.Errorf("update passkey_enabled: %w", err)
		}
		if !*passkeyEnabled {
			if _, err := tx.Exec("DELETE FROM webauthn_credentials WHERE user_id = ?", userID); err != nil {
				return nil, fmt.Errorf("clear passkeys: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit auth methods: %w", err)
	}

	return s.userSummaryByID(userID)
}

// DeleteUser removes a user and all of their dependent records (devices,
// tokens, passkeys). It refuses to delete the acting admin's own account or the
// last remaining admin.
func (s *Service) DeleteUser(actorID, userID int64) error {
	if actorID == userID {
		return ErrCannotDeleteSelf
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var role string
	if err := tx.QueryRow("SELECT role FROM users WHERE id = ?", userID).Scan(&role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUserNotFound
		}
		return fmt.Errorf("load user: %w", err)
	}

	if role == RoleAdmin {
		var adminCount int
		if err := tx.QueryRow("SELECT COUNT(*) FROM users WHERE role = ?", RoleAdmin).Scan(&adminCount); err != nil {
			return fmt.Errorf("count admins: %w", err)
		}
		if adminCount <= 1 {
			return ErrLastAdmin
		}
	}

	// Clear references that lack ON DELETE CASCADE before removing the user.
	// Deleting the user then cascades passkeys and OAuth grants automatically.
	if _, err := tx.Exec("DELETE FROM refresh_tokens WHERE user_id = ?", userID); err != nil {
		return fmt.Errorf("delete refresh tokens: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM connect_tokens WHERE user_id = ? OR created_by = ?", userID, userID); err != nil {
		return fmt.Errorf("delete connect tokens: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM devices WHERE user_id = ?", userID); err != nil {
		return fmt.Errorf("delete devices: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM users WHERE id = ?", userID); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}

	return tx.Commit()
}

func (s *Service) userSummaryByID(userID int64) (*UserSummary, error) {
	var u UserSummary
	var invitedAt sql.NullTime
	err := s.db.QueryRow(`
		SELECT
			u.id,
			u.username,
			u.role,
			u.created_at,
			u.password_hash != '' AS has_password,
			u.password_enabled,
			u.passkey_enabled,
			u.ai_shared_enabled,
			u.plex_email,
			u.plex_invited_at,
			(SELECT COUNT(*) FROM devices d WHERE d.user_id = u.id AND d.revoked_at IS NULL) AS device_count,
			EXISTS(
				SELECT 1 FROM connect_tokens ct
				WHERE ct.user_id = u.id AND ct.redeemed_at IS NULL AND ct.expires_at > ?
			) AS has_pending_invite
		FROM users u
		WHERE u.id = ?
	`, time.Now(), userID).Scan(
		&u.ID, &u.Username, &u.Role, &u.CreatedAt,
		&u.HasPassword, &u.PasswordEnabled, &u.PasskeyEnabled, &u.AISharedEnabled, &u.PlexEmail, &invitedAt,
		&u.DeviceCount, &u.HasPendingInvite,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("load user summary: %w", err)
	}
	if invitedAt.Valid {
		u.PlexInvitedAt = &invitedAt.Time
	}
	u.Permissions = PermissionsForRole(u.Role)
	return &u, nil
}

func (s *Service) ValidateToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.jwtSecret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

// AuthenticateToken validates a bearer token and rehydrates its identity from
// the database so current role and device state are enforced for each request.
func (s *Service) AuthenticateToken(tokenStr string) (*Claims, *User, error) {
	claims, err := s.ValidateToken(tokenStr)
	if err != nil {
		return nil, nil, err
	}
	if len(claims.Audience) > 0 {
		return nil, nil, ErrInvalidCredentials
	}

	return s.authenticateClaims(claims)
}

// AuthenticateTokenForAudience validates an OAuth access token bound to a
// specific resource audience, then rehydrates its current user and device.
func (s *Service) AuthenticateTokenForAudience(tokenStr, audience string) (*Claims, *User, error) {
	claims, err := s.ValidateToken(tokenStr)
	if err != nil {
		return nil, nil, err
	}
	if audience == "" || !hasAudience(claims, audience) {
		return nil, nil, ErrInvalidCredentials
	}

	return s.authenticateClaims(claims)
}

func (s *Service) authenticateClaims(claims *Claims) (*Claims, *User, error) {
	user, err := s.getUserForAuth(claims.UserID)
	if err != nil {
		return nil, nil, err
	}

	if claims.DeviceID != "" {
		if err := s.requireActiveDevice(claims.DeviceID, claims.UserID); err != nil {
			return nil, nil, err
		}
	}

	withPerms := userWithPermissions(user)
	claims.Username = withPerms.Username
	claims.Role = withPerms.Role
	return claims, &withPerms, nil
}

// AuthorizeInteractiveToolCall re-checks the authoritative user, device, role,
// and (for an administrator-funded turn) shared-AI grant immediately before an
// interactive AI or MCP tool executes. A model turn can outlive the request's
// initial middleware check, so relying on its role snapshot would let a device
// revocation, role demotion, or grant revocation take effect one tool too late.
func (s *Service) AuthorizeInteractiveToolCall(
	ctx context.Context,
	userID int64,
	deviceID string,
	requireSharedAI bool,
) (string, error) {
	snapshot, err := s.authoritativeSession(ctx, userID, deviceID)
	if err != nil {
		return "", err
	}
	if snapshot.role != RoleAdmin && snapshot.role != RoleUser {
		return "", ErrInvalidCredentials
	}
	if requireSharedAI && !snapshot.sharedAIEnabled {
		return "", ErrSharedAIAccessRevoked
	}
	return snapshot.role, nil
}

// AuthorizePermission re-checks the current database role and device state for
// one already-authenticated request. It is intended for handlers that spend a
// meaningful amount of time awaiting an external provider before committing a
// credential or setting: middleware admission is not enough across that gap.
func (s *Service) AuthorizePermission(ctx context.Context, userID int64, deviceID string, permission Permission) error {
	snapshot, err := s.authoritativeSession(ctx, userID, deviceID)
	if err != nil {
		return err
	}
	if !HasPermission(snapshot.role, permission) {
		return ErrPermissionDenied
	}
	return nil
}

type authoritativeSessionSnapshot struct {
	role            string
	sharedAIEnabled bool
}

func (s *Service) authoritativeSession(ctx context.Context, userID int64, deviceID string) (authoritativeSessionSnapshot, error) {
	if userID <= 0 || deviceID == "" {
		return authoritativeSessionSnapshot{}, ErrInvalidCredentials
	}

	var (
		snapshot  authoritativeSessionSnapshot
		revokedAt sql.NullTime
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT u.role, u.ai_shared_enabled, d.revoked_at
		FROM users u
		JOIN devices d ON d.user_id = u.id
		WHERE u.id = ? AND d.id = ?
	`, userID, deviceID).Scan(&snapshot.role, &snapshot.sharedAIEnabled, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return authoritativeSessionSnapshot{}, ErrInvalidCredentials
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return authoritativeSessionSnapshot{}, ctxErr
		}
		return authoritativeSessionSnapshot{}, fmt.Errorf("%w: authorize session: %v", ErrAuthUnavailable, err)
	}
	if revokedAt.Valid {
		return authoritativeSessionSnapshot{}, ErrDeviceRevoked
	}
	return snapshot, nil
}

func hasAudience(claims *Claims, audience string) bool {
	for _, aud := range claims.Audience {
		if aud == audience {
			return true
		}
	}
	return false
}

// signAccessToken mints the short-lived bearer JWT. Signing failures are
// ErrAuthUnavailable: the credential was already accepted, so failing to mint
// its successor is a server fault, never a rejection.
func (s *Service) signAccessToken(user *User, deviceID string) (string, error) {
	now := time.Now()
	accessClaims := &Claims{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		DeviceID: deviceID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	accessToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims).SignedString(s.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("%w: sign access token: %v", ErrAuthUnavailable, err)
	}
	return accessToken, nil
}

// newOpaqueRefreshToken mints a current-scheme refresh token: 32 random bytes
// behind a versioned prefix. The value is a pure bearer secret — nothing about
// the session is derivable from it, and only its SHA-256 hash is stored.
func newOpaqueRefreshToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate refresh token: %w", err)
	}
	return opaqueRefreshPrefix + hex.EncodeToString(b), nil
}

func (s *Service) generateTokens(user *User, deviceID string) (*TokenResponse, error) {
	accessToken, err := s.signAccessToken(user, deviceID)
	if err != nil {
		return nil, err
	}

	refreshToken, err := newOpaqueRefreshToken()
	if err != nil {
		return nil, err
	}

	// The INSERT must succeed before the token is handed out: an unstored
	// refresh token is a session that dies on its first refresh, silently.
	if _, err := s.db.Exec(
		"INSERT INTO refresh_tokens (token_hash, device_id, user_id, expires_at) VALUES (?, ?, ?, ?)",
		hashToken(refreshToken), deviceID, user.ID, refreshNeverExpires,
	); err != nil {
		return nil, fmt.Errorf("store refresh token: %w", err)
	}

	// Best-effort sweep of legacy rows: expired JWT-era tokens and rotation
	// bookkeeping. Opaque rows carry the far-future sentinel and never match.
	now := time.Now()
	_, _ = s.db.Exec("DELETE FROM refresh_tokens WHERE expires_at < ?", now)
	_, _ = s.db.Exec("DELETE FROM refresh_tokens WHERE superseded_at IS NOT NULL")

	return &TokenResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		User:         userWithPermissions(user),
	}, nil
}

func userWithPermissions(user *User) User {
	out := *user
	out.Permissions = PermissionsForRole(user.Role)
	return out
}

func (s *Service) getUserByUsername(username string) (*User, error) {
	var user User
	var invitedAt sql.NullTime
	err := s.db.QueryRow(
		"SELECT id, username, password_hash, role, password_enabled, passkey_enabled, plex_email, plex_invited_at, created_at FROM users WHERE username = ?", username,
	).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Role, &user.PasswordEnabled, &user.PasskeyEnabled, &user.PlexEmail, &invitedAt, &user.CreatedAt)
	if err != nil {
		return nil, err
	}
	if invitedAt.Valid {
		user.PlexInvitedAt = &invitedAt.Time
	}
	return &user, nil
}

func (s *Service) getUserByID(id int64) (*User, error) {
	var user User
	var invitedAt sql.NullTime
	err := s.db.QueryRow(
		"SELECT id, username, password_hash, role, password_enabled, passkey_enabled, plex_email, plex_invited_at, created_at FROM users WHERE id = ?", id,
	).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Role, &user.PasswordEnabled, &user.PasskeyEnabled, &user.PlexEmail, &invitedAt, &user.CreatedAt)
	if err != nil {
		return nil, err
	}
	if invitedAt.Valid {
		user.PlexInvitedAt = &invitedAt.Time
	}
	return &user, nil
}

func generateCode(length int) (string, error) {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b)[:length], nil
}
