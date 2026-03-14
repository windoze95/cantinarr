package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInviteRequired     = errors.New("valid invite code required")
	ErrInviteExpired      = errors.New("invite code expired or already used")
	ErrUserExists         = errors.New("username already taken")
)

type Claims struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
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
	return s.generateTokens(user)
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
	return s.generateTokens(user)
}

func (s *Service) Refresh(refreshToken string) (*TokenResponse, error) {
	claims, err := s.ValidateToken(refreshToken)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	user, err := s.getUserByID(claims.UserID)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	return s.generateTokens(user)
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

func (s *Service) generateTokens(user *User) (*TokenResponse, error) {
	now := time.Now()

	accessClaims := &Claims{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(7 * 24 * time.Hour)),
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
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(30 * 24 * time.Hour)),
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
