package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
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
	ErrInvalidCredentials   = errors.New("invalid credentials")
	ErrUserExists           = errors.New("username already taken")
	ErrTokenExpired         = errors.New("connect token has expired")
	ErrTokenRedeemed        = errors.New("connect token has already been used")
	ErrTokenNotFound        = errors.New("connect token not found")
	ErrDeviceRevoked        = errors.New("device has been revoked")
	ErrDeviceNotFound       = errors.New("device not found")
	ErrSetupAlreadyComplete = errors.New("setup has already been completed")
	ErrUserNotFound         = errors.New("user not found")
	ErrInvalidRole          = errors.New("invalid role")
	ErrLastAdmin            = errors.New("cannot remove the last admin")
	ErrCannotDeleteSelf     = errors.New("cannot delete your own account")
	ErrPasswordTooShort     = errors.New("password is too short")
	ErrPasswordNotAllowed   = errors.New("password sign-in is not enabled for this account")
	ErrPasskeyNotAllowed    = errors.New("passkeys are not enabled for this account")
	ErrCannotModifyAdmin    = errors.New("cannot change sign-in methods for an admin")
)

// minPasswordLength is the minimum length for an account password. It matches
// the check enforced during first-run setup.
const minPasswordLength = 8

const (
	// refreshTokenTTL is how long an idle session survives before it must be
	// re-established with a fresh connect link. Each refresh issues a new token
	// with a fresh TTL, so an actively used session effectively never expires.
	refreshTokenTTL = 365 * 24 * time.Hour
	// refreshRotationGrace is a short window after a refresh token is rotated
	// during which the just-superseded token is still accepted. It prevents a
	// client that fails to persist the rotated token (crash, dropped response)
	// from being logged out, without leaving tokens replayable indefinitely.
	refreshRotationGrace = 60 * time.Second
)

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
	_, err = s.db.Exec("INSERT INTO users (username, password_hash, role, password_enabled, passkey_enabled) VALUES (?, ?, ?, 1, 1)", "admin", string(hash), "admin")
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
		"INSERT INTO users (username, password_hash, role, password_enabled, passkey_enabled) VALUES (?, ?, ?, 1, 1)",
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

func (s *Service) Refresh(refreshToken string) (*TokenResponse, error) {
	claims, err := s.ValidateToken(refreshToken)
	if err != nil {
		return nil, ErrInvalidCredentials
	}

	// Verify the refresh token exists in our store (rotation check).
	oldHash := hashToken(refreshToken)
	var (
		storedDeviceID string
		supersededAt   sql.NullTime
	)
	err = s.db.QueryRow(
		"SELECT device_id, superseded_at FROM refresh_tokens WHERE token_hash = ?", oldHash,
	).Scan(&storedDeviceID, &supersededAt)
	if err != nil {
		// Token not in store — never issued, expired and cleaned up, or replayed.
		return nil, ErrInvalidCredentials
	}
	if supersededAt.Valid {
		// Already rotated. Accept it only within the grace window so a client
		// that failed to persist the rotated token isn't logged out; beyond the
		// window treat it as a replay.
		if time.Since(supersededAt.Time) > refreshRotationGrace {
			return nil, ErrInvalidCredentials
		}
	} else {
		// First use: mark the token superseded (one-time use) but keep the row
		// so a retry within the grace window still succeeds. Cleanup in
		// generateTokens removes it once the grace window has passed.
		_, _ = s.db.Exec(
			"UPDATE refresh_tokens SET superseded_at = ? WHERE token_hash = ?",
			time.Now(), oldHash,
		)
	}

	// Check device revocation if the token has a device ID
	if claims.DeviceID != "" {
		var revokedAt *time.Time
		err := s.db.QueryRow(
			"SELECT revoked_at FROM devices WHERE id = ?", claims.DeviceID,
		).Scan(&revokedAt)
		if err != nil {
			return nil, ErrInvalidCredentials
		}
		if revokedAt != nil {
			return nil, ErrDeviceRevoked
		}
		// Update last_seen_at
		_, _ = s.db.Exec(
			"UPDATE devices SET last_seen_at = ? WHERE id = ?",
			time.Now(), claims.DeviceID,
		)
	}

	user, err := s.getUserByID(claims.UserID)
	if err != nil {
		return nil, ErrInvalidCredentials
	}

	resp, err := s.generateTokens(user, claims.DeviceID)
	if err != nil {
		return nil, err
	}
	if claims.DeviceID != "" {
		resp.DeviceID = claims.DeviceID
	}
	return resp, nil
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
	// Find or create user
	var userID int64
	user, err := s.getUserByUsername(name)
	if err != nil {
		// Create new user with empty password hash (no password login)
		result, err := s.db.Exec(
			"INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)",
			name, "", "user",
		)
		if err != nil {
			return nil, fmt.Errorf("create user: %w", err)
		}
		userID, _ = result.LastInsertId()
	} else {
		userID = user.ID
	}

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

	// Mark as redeemed
	now := time.Now()
	_, err = s.db.Exec("UPDATE connect_tokens SET redeemed_at = ? WHERE token = ?", now, token)
	if err != nil {
		return nil, fmt.Errorf("mark token redeemed: %w", err)
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
		if err := rows.Scan(
			&u.ID, &u.Username, &u.Role, &u.CreatedAt,
			&u.HasPassword, &u.PasswordEnabled, &u.PasskeyEnabled,
			&u.DeviceCount, &u.HasPendingInvite,
		); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		u.Permissions = PermissionsForRole(u.Role)
		users = append(users, u)
	}
	if users == nil {
		users = []UserSummary{}
	}
	return users, nil
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
	err := s.db.QueryRow(`
		SELECT
			u.id,
			u.username,
			u.role,
			u.created_at,
			u.password_hash != '' AS has_password,
			u.password_enabled,
			u.passkey_enabled,
			(SELECT COUNT(*) FROM devices d WHERE d.user_id = u.id AND d.revoked_at IS NULL) AS device_count,
			EXISTS(
				SELECT 1 FROM connect_tokens ct
				WHERE ct.user_id = u.id AND ct.redeemed_at IS NULL AND ct.expires_at > ?
			) AS has_pending_invite
		FROM users u
		WHERE u.id = ?
	`, time.Now(), userID).Scan(
		&u.ID, &u.Username, &u.Role, &u.CreatedAt,
		&u.HasPassword, &u.PasswordEnabled, &u.PasskeyEnabled,
		&u.DeviceCount, &u.HasPendingInvite,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("load user summary: %w", err)
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
	user, err := s.getUserByID(claims.UserID)
	if err != nil {
		return nil, nil, ErrInvalidCredentials
	}

	if claims.DeviceID != "" {
		if err := s.validateActiveDevice(claims); err != nil {
			return nil, nil, err
		}
	}

	withPerms := userWithPermissions(user)
	claims.Username = withPerms.Username
	claims.Role = withPerms.Role
	return claims, &withPerms, nil
}

func hasAudience(claims *Claims, audience string) bool {
	for _, aud := range claims.Audience {
		if aud == audience {
			return true
		}
	}
	return false
}

func (s *Service) validateActiveDevice(claims *Claims) error {
	var (
		userID    int64
		revokedAt sql.NullTime
	)
	err := s.db.QueryRow(
		"SELECT user_id, revoked_at FROM devices WHERE id = ?",
		claims.DeviceID,
	).Scan(&userID, &revokedAt)
	if err != nil {
		return ErrInvalidCredentials
	}
	if userID != claims.UserID {
		return ErrInvalidCredentials
	}
	if revokedAt.Valid {
		return ErrDeviceRevoked
	}
	_, _ = s.db.Exec(
		"UPDATE devices SET last_seen_at = ? WHERE id = ?",
		time.Now(), claims.DeviceID,
	)
	return nil
}

func (s *Service) generateTokens(user *User, deviceID string) (*TokenResponse, error) {
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
		return nil, fmt.Errorf("sign access token: %w", err)
	}

	refreshExpiry := refreshTokenTTL
	refreshClaims := &Claims{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		DeviceID: deviceID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(refreshExpiry)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	refreshToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims).SignedString(s.jwtSecret)
	if err != nil {
		return nil, fmt.Errorf("sign refresh token: %w", err)
	}

	// Store refresh token hash for rotation tracking
	tokenHash := hashToken(refreshToken)
	_, _ = s.db.Exec(
		"INSERT INTO refresh_tokens (token_hash, device_id, user_id, expires_at) VALUES (?, ?, ?, ?)",
		tokenHash, deviceID, user.ID, now.Add(refreshExpiry),
	)

	// Clean up expired refresh tokens, and ones left over past the rotation
	// grace window, periodically (best-effort).
	_, _ = s.db.Exec("DELETE FROM refresh_tokens WHERE expires_at < ?", now)
	_, _ = s.db.Exec(
		"DELETE FROM refresh_tokens WHERE superseded_at IS NOT NULL AND superseded_at < ?",
		now.Add(-refreshRotationGrace),
	)

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
	err := s.db.QueryRow(
		"SELECT id, username, password_hash, role, password_enabled, passkey_enabled, created_at FROM users WHERE username = ?", username,
	).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Role, &user.PasswordEnabled, &user.PasskeyEnabled, &user.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Service) getUserByID(id int64) (*User, error) {
	var user User
	err := s.db.QueryRow(
		"SELECT id, username, password_hash, role, password_enabled, passkey_enabled, created_at FROM users WHERE id = ?", id,
	).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Role, &user.PasswordEnabled, &user.PasskeyEnabled, &user.CreatedAt)
	if err != nil {
		return nil, err
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
