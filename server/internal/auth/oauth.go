package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	oauthAccessTokenLifetime  = 15 * time.Minute
	oauthRefreshTokenLifetime = 365 * 24 * time.Hour
	oauthAuthorizationCodeTTL = 5 * time.Minute
	defaultOAuthScope         = "mcp"
)

var (
	ErrOAuthInvalidClient       = errors.New("invalid oauth client")
	ErrOAuthInvalidRedirectURI  = errors.New("invalid redirect uri")
	ErrOAuthInvalidCode         = errors.New("invalid authorization code")
	ErrOAuthInvalidPKCE         = errors.New("invalid pkce verifier")
	ErrOAuthInvalidRefreshToken = errors.New("invalid refresh token")
	ErrOAuthInvalidResource     = errors.New("invalid resource")
)

type OAuthClient struct {
	ClientID      string
	ClientName    string
	RedirectURIs  []string
	GrantTypes    []string
	ResponseTypes []string
	Scope         string
	CreatedAt     time.Time
}

type OAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

type oauthAuthorizationCode struct {
	CodeHash      string
	ClientID      string
	UserID        int64
	RedirectURI   string
	CodeChallenge string
	Resource      string
	Scope         string
	ExpiresAt     time.Time
}

type oauthRefreshToken struct {
	TokenHash string
	ClientID  string
	UserID    int64
	DeviceID  string
	Resource  string
	Scope     string
	ExpiresAt time.Time
}

func (s *Service) RegisterOAuthClient(clientName string, redirectURIs []string) (*OAuthClient, error) {
	if len(redirectURIs) == 0 {
		return nil, ErrOAuthInvalidRedirectURI
	}
	for _, redirectURI := range redirectURIs {
		if !isAllowedRedirectURI(redirectURI) {
			return nil, ErrOAuthInvalidRedirectURI
		}
	}

	clientID, err := randomURLToken(24)
	if err != nil {
		return nil, fmt.Errorf("generate client id: %w", err)
	}
	if clientName == "" {
		clientName = "MCP Client"
	}
	client := &OAuthClient{
		ClientID:      clientID,
		ClientName:    clientName,
		RedirectURIs:  redirectURIs,
		GrantTypes:    []string{"authorization_code", "refresh_token"},
		ResponseTypes: []string{"code"},
		Scope:         defaultOAuthScope,
		CreatedAt:     time.Now(),
	}

	redirectJSON, _ := json.Marshal(client.RedirectURIs)
	grantJSON, _ := json.Marshal(client.GrantTypes)
	responseJSON, _ := json.Marshal(client.ResponseTypes)
	_, err = s.db.Exec(
		`INSERT INTO oauth_clients (client_id, client_name, redirect_uris, grant_types, response_types, scope)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		client.ClientID, client.ClientName, string(redirectJSON), string(grantJSON), string(responseJSON), client.Scope,
	)
	if err != nil {
		return nil, fmt.Errorf("insert oauth client: %w", err)
	}
	return client, nil
}

func (s *Service) GetOAuthClient(clientID string) (*OAuthClient, error) {
	var (
		client       OAuthClient
		redirectJSON string
		grantJSON    string
		responseJSON string
	)
	err := s.db.QueryRow(
		`SELECT client_id, client_name, redirect_uris, grant_types, response_types, scope, created_at
		 FROM oauth_clients WHERE client_id = ?`,
		clientID,
	).Scan(&client.ClientID, &client.ClientName, &redirectJSON, &grantJSON, &responseJSON, &client.Scope, &client.CreatedAt)
	if err != nil {
		return nil, ErrOAuthInvalidClient
	}
	_ = json.Unmarshal([]byte(redirectJSON), &client.RedirectURIs)
	_ = json.Unmarshal([]byte(grantJSON), &client.GrantTypes)
	_ = json.Unmarshal([]byte(responseJSON), &client.ResponseTypes)
	return &client, nil
}

func (s *Service) AuthenticatePassword(username, password string) (*User, error) {
	user, err := s.getUserByUsername(username)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	// Password sign-in must be enabled for this account. Because passkeys need a
	// secure context (HTTPS), this flag is effectively what authorizes MCP
	// clients on plain-HTTP deployments, where the password form is the only
	// usable credential on the OAuth authorize page.
	if !passwordAllowed(user) {
		return nil, ErrInvalidCredentials
	}
	if user.PasswordHash == "" {
		return nil, ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}
	withPerms := userWithPermissions(user)
	return &withPerms, nil
}

func (s *Service) CreateOAuthAuthorizationCode(client *OAuthClient, userID int64, redirectURI, codeChallenge, resource, scope string) (string, error) {
	if !clientAllowsRedirect(client, redirectURI) {
		return "", ErrOAuthInvalidRedirectURI
	}
	if codeChallenge == "" {
		return "", ErrOAuthInvalidPKCE
	}
	if scope == "" {
		scope = defaultOAuthScope
	}

	code, err := randomURLToken(32)
	if err != nil {
		return "", fmt.Errorf("generate auth code: %w", err)
	}
	codeHash := hashToken(code)
	_, err = s.db.Exec(
		`INSERT INTO oauth_authorization_codes
		 (code_hash, client_id, user_id, redirect_uri, code_challenge, resource, scope, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		codeHash, client.ClientID, userID, redirectURI, codeChallenge, resource, scope, time.Now().Add(oauthAuthorizationCodeTTL),
	)
	if err != nil {
		return "", fmt.Errorf("insert auth code: %w", err)
	}
	return code, nil
}

func (s *Service) ExchangeOAuthAuthorizationCode(clientID, code, redirectURI, codeVerifier, resource string) (*OAuthTokenResponse, error) {
	client, err := s.GetOAuthClient(clientID)
	if err != nil {
		return nil, err
	}
	if !clientAllowsRedirect(client, redirectURI) {
		return nil, ErrOAuthInvalidRedirectURI
	}

	// Claim the code atomically, matched to this client and redirect URI. A
	// wrong-client or replayed attempt matches zero rows and never deletes a
	// still-valid code, so it can neither burn a victim's pending grant nor
	// mint a second token set from one code.
	stored, err := s.consumeOAuthAuthorizationCode(code, clientID, redirectURI)
	if err != nil {
		return nil, err
	}
	if time.Now().After(stored.ExpiresAt) {
		return nil, ErrOAuthInvalidCode
	}
	if resource != "" && resource != stored.Resource {
		return nil, ErrOAuthInvalidResource
	}
	if !verifyPKCES256(codeVerifier, stored.CodeChallenge) {
		return nil, ErrOAuthInvalidPKCE
	}

	user, err := s.getUserByID(stored.UserID)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	deviceID, err := s.createOAuthDevice(user.ID, client.ClientName)
	if err != nil {
		return nil, err
	}
	refreshToken, err := s.createOAuthRefreshToken(clientID, user.ID, deviceID, stored.Resource, stored.Scope)
	if err != nil {
		return nil, err
	}
	accessToken, err := s.generateOAuthAccessToken(user, deviceID, stored.Resource, stored.Scope)
	if err != nil {
		return nil, err
	}
	return &OAuthTokenResponse{
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		ExpiresIn:    int64(oauthAccessTokenLifetime.Seconds()),
		RefreshToken: refreshToken,
		Scope:        stored.Scope,
	}, nil
}

func (s *Service) RefreshOAuthToken(clientID, refreshToken, resource string) (*OAuthTokenResponse, error) {
	stored, tokenHash, err := s.loadOAuthRefreshToken(clientID, refreshToken)
	if err != nil {
		return nil, err
	}
	if resource != "" && resource != stored.Resource {
		return nil, ErrOAuthInvalidResource
	}
	if time.Now().After(stored.ExpiresAt) {
		return nil, ErrOAuthInvalidRefreshToken
	}

	user, err := s.getUserByID(stored.UserID)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	if err := s.requireActiveDevice(stored.DeviceID, stored.UserID); err != nil {
		return nil, err
	}

	// Rotate atomically: only the caller whose DELETE actually removes the row
	// may mint a successor. A concurrent refresh of the same token deletes zero
	// rows and is rejected here instead of minting a second valid successor and
	// silently defeating refresh-token reuse detection.
	result, err := s.db.Exec("DELETE FROM oauth_refresh_tokens WHERE token_hash = ?", tokenHash)
	if err != nil {
		return nil, fmt.Errorf("rotate refresh token: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		return nil, ErrOAuthInvalidRefreshToken
	}
	nextRefreshToken, err := s.createOAuthRefreshToken(stored.ClientID, stored.UserID, stored.DeviceID, stored.Resource, stored.Scope)
	if err != nil {
		return nil, err
	}
	accessToken, err := s.generateOAuthAccessToken(user, stored.DeviceID, stored.Resource, stored.Scope)
	if err != nil {
		return nil, err
	}
	return &OAuthTokenResponse{
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		ExpiresIn:    int64(oauthAccessTokenLifetime.Seconds()),
		RefreshToken: nextRefreshToken,
		Scope:        stored.Scope,
	}, nil
}

// consumeOAuthAuthorizationCode deletes and returns the authorization code in a
// single statement, matched to the presenting client and redirect URI. Doing
// the match in the DELETE (rather than selecting, then checking, then deleting)
// makes redemption a single-use atomic claim: only one concurrent caller wins
// the row, and a caller with the wrong client_id/redirect_uri deletes nothing.
func (s *Service) consumeOAuthAuthorizationCode(code, clientID, redirectURI string) (*oauthAuthorizationCode, error) {
	codeHash := hashToken(code)
	var stored oauthAuthorizationCode
	err := s.db.QueryRow(
		`DELETE FROM oauth_authorization_codes
		 WHERE code_hash = ? AND client_id = ? AND redirect_uri = ?
		 RETURNING code_hash, client_id, user_id, redirect_uri, code_challenge, resource, scope, expires_at`,
		codeHash, clientID, redirectURI,
	).Scan(&stored.CodeHash, &stored.ClientID, &stored.UserID, &stored.RedirectURI, &stored.CodeChallenge, &stored.Resource, &stored.Scope, &stored.ExpiresAt)
	if err != nil {
		return nil, ErrOAuthInvalidCode
	}
	return &stored, nil
}

func (s *Service) loadOAuthRefreshToken(clientID, refreshToken string) (*oauthRefreshToken, string, error) {
	tokenHash := hashToken(refreshToken)
	var stored oauthRefreshToken
	err := s.db.QueryRow(
		`SELECT token_hash, client_id, user_id, device_id, resource, scope, expires_at
		 FROM oauth_refresh_tokens WHERE token_hash = ? AND client_id = ?`,
		tokenHash, clientID,
	).Scan(&stored.TokenHash, &stored.ClientID, &stored.UserID, &stored.DeviceID, &stored.Resource, &stored.Scope, &stored.ExpiresAt)
	if err != nil {
		return nil, "", ErrOAuthInvalidRefreshToken
	}
	return &stored, tokenHash, nil
}

func (s *Service) createOAuthDevice(userID int64, clientName string) (string, error) {
	deviceID := uuid.New().String()
	deviceName := strings.TrimSpace(clientName)
	if deviceName == "" {
		deviceName = "MCP Client"
	}
	if !strings.HasPrefix(deviceName, "MCP: ") {
		deviceName = "MCP: " + deviceName
	}
	_, err := s.db.Exec(
		"INSERT INTO devices (id, user_id, device_name) VALUES (?, ?, ?)",
		deviceID, userID, deviceName,
	)
	if err != nil {
		return "", fmt.Errorf("create oauth device: %w", err)
	}
	return deviceID, nil
}

func (s *Service) createOAuthRefreshToken(clientID string, userID int64, deviceID, resource, scope string) (string, error) {
	refreshToken, err := randomURLToken(48)
	if err != nil {
		return "", fmt.Errorf("generate oauth refresh token: %w", err)
	}
	tokenHash := hashToken(refreshToken)
	now := time.Now()
	_, err = s.db.Exec(
		`INSERT INTO oauth_refresh_tokens
		 (token_hash, client_id, user_id, device_id, resource, scope, expires_at, last_used_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		tokenHash, clientID, userID, deviceID, resource, scope, now.Add(oauthRefreshTokenLifetime), now,
	)
	if err != nil {
		return "", fmt.Errorf("insert oauth refresh token: %w", err)
	}
	_, _ = s.db.Exec("DELETE FROM oauth_refresh_tokens WHERE expires_at < ?", now)
	return refreshToken, nil
}

func (s *Service) generateOAuthAccessToken(user *User, deviceID, audience, scope string) (string, error) {
	now := time.Now()
	claims := &Claims{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		DeviceID: deviceID,
		Scope:    scope,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(oauthAccessTokenLifetime)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	if audience != "" {
		claims.Audience = jwt.ClaimStrings{audience}
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return token, nil
}

func isAllowedRedirectURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.Fragment != "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		host := strings.ToLower(u.Hostname())
		return host == "localhost" || host == "127.0.0.1" || host == "::1"
	default:
		return false
	}
}

func clientAllowsRedirect(client *OAuthClient, redirectURI string) bool {
	for _, allowed := range client.RedirectURIs {
		if redirectURI == allowed {
			return true
		}
	}
	return false
}

func verifyPKCES256(verifier, challenge string) bool {
	if verifier == "" || challenge == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:]) == challenge
}

func randomURLToken(byteLen int) (string, error) {
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func normalizeOAuthScope(scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return defaultOAuthScope
	}
	if scope == defaultOAuthScope {
		return scope
	}
	for _, part := range strings.Fields(scope) {
		if part == defaultOAuthScope {
			return defaultOAuthScope
		}
	}
	return defaultOAuthScope
}
