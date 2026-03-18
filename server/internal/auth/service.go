package auth

import (
	"crypto/rand"
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

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInviteRequired     = errors.New("valid invite code required")
	ErrInviteExpired      = errors.New("invite code expired or already used")
	ErrUserExists         = errors.New("username already taken")
	ErrTokenExpired       = errors.New("connect token has expired")
	ErrTokenRedeemed      = errors.New("connect token has already been used")
	ErrTokenNotFound      = errors.New("connect token not found")
	ErrDeviceRevoked      = errors.New("device has been revoked")
	ErrDeviceNotFound     = errors.New("device not found")
)

type Claims struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	DeviceID string `json:"device_id,omitempty"`
	jwt.RegisteredClaims
}

type Service struct {
	db        *sql.DB
	jwtSecret []byte
}

func NewService(db *sql.DB, jwtSecret string) *Service {
	return &Service{
		db:        db,
		jwtSecret: []byte(jwtSecret),
	}
}

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
	_, err = s.db.Exec("INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)", "admin", string(hash), "admin")
	if err != nil {
		return fmt.Errorf("create admin: %w", err)
	}
	return nil
}

func (s *Service) Login(username, password string) (*TokenResponse, error) {
	user, err := s.getUserByUsername(username)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	// Auto-create a device record for admin login
	deviceID := uuid.New().String()
	_, err = s.db.Exec(
		"INSERT INTO devices (id, user_id, device_name) VALUES (?, ?, ?)",
		deviceID, user.ID, "Admin",
	)
	if err != nil {
		return nil, fmt.Errorf("create device: %w", err)
	}

	resp, err := s.generateTokens(user, deviceID)
	if err != nil {
		return nil, err
	}
	resp.DeviceID = deviceID
	return resp, nil
}

func (s *Service) Register(username, password, inviteCode string) (*TokenResponse, error) {
	// Validate invite code
	var code InviteCode
	err := s.db.QueryRow(
		"SELECT code, created_by, expires_at, used_at FROM invite_codes WHERE code = ?", inviteCode,
	).Scan(&code.Code, &code.CreatedBy, &code.ExpiresAt, &code.UsedAt)
	if err != nil {
		return nil, ErrInviteRequired
	}
	if code.UsedAt != nil || time.Now().After(code.ExpiresAt) {
		return nil, ErrInviteExpired
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	result, err := s.db.Exec("INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)", username, string(hash), "user")
	if err != nil {
		return nil, ErrUserExists
	}
	userID, _ := result.LastInsertId()

	now := time.Now()
	_, err = s.db.Exec("UPDATE invite_codes SET used_by = ?, used_at = ? WHERE code = ?", userID, now, inviteCode)
	if err != nil {
		return nil, fmt.Errorf("mark invite used: %w", err)
	}

	user := &User{
		ID:       userID,
		Username: username,
		Role:     "user",
	}
	return s.generateTokens(user, "")
}

func (s *Service) Refresh(refreshToken string) (*TokenResponse, error) {
	claims, err := s.ValidateToken(refreshToken)
	if err != nil {
		return nil, ErrInvalidCredentials
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
	return s.getUserByID(userID)
}

func (s *Service) CreateInvite(createdBy int64) (*InviteResponse, error) {
	code, err := generateCode(6)
	if err != nil {
		return nil, fmt.Errorf("generate code: %w", err)
	}
	expiresAt := time.Now().Add(7 * 24 * time.Hour)
	_, err = s.db.Exec("INSERT INTO invite_codes (code, created_by, expires_at) VALUES (?, ?, ?)", code, createdBy, expiresAt)
	if err != nil {
		return nil, fmt.Errorf("insert invite: %w", err)
	}
	return &InviteResponse{Code: code, ExpiresAt: expiresAt}, nil
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

func (s *Service) RedeemConnectToken(token, deviceName string) (*TokenResponse, error) {
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

	// Create device record
	deviceID := uuid.New().String()
	_, err = s.db.Exec(
		"INSERT INTO devices (id, user_id, device_name) VALUES (?, ?, ?)",
		deviceID, ct.UserID, deviceName,
	)
	if err != nil {
		return nil, fmt.Errorf("create device: %w", err)
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
	return nil
}

func (s *Service) ListUsers() ([]User, error) {
	rows, err := s.db.Query("SELECT id, username, password_hash, role, created_at FROM users ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("query users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, u)
	}
	if users == nil {
		users = []User{}
	}
	return users, nil
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

	refreshClaims := &Claims{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		DeviceID: deviceID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(365 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	refreshToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims).SignedString(s.jwtSecret)
	if err != nil {
		return nil, fmt.Errorf("sign refresh token: %w", err)
	}

	return &TokenResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		User:         *user,
	}, nil
}

func (s *Service) getUserByUsername(username string) (*User, error) {
	var user User
	err := s.db.QueryRow(
		"SELECT id, username, password_hash, role, created_at FROM users WHERE username = ?", username,
	).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Role, &user.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Service) getUserByID(id int64) (*User, error) {
	var user User
	err := s.db.QueryRow(
		"SELECT id, username, password_hash, role, created_at FROM users WHERE id = ?", id,
	).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Role, &user.CreatedAt)
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
