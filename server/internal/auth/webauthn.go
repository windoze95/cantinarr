package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
)

// ─── Secure Context Check ────────────────────────────────

// isSecureContext checks whether the request is in a secure context
// (HTTPS, localhost, or behind a reverse proxy with X-Forwarded-Proto: https).
// WebAuthn requires a secure context except for localhost.
func isSecureContext(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	host := r.Host
	if colonIdx := strings.LastIndex(host, ":"); colonIdx != -1 {
		host = host[:colonIdx]
	}
	host = strings.ToLower(host)
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// ─── RP Config from Request ──────────────────────────────

// rpIDFromRequest extracts just the RP ID (hostname without port) from a request.
func rpIDFromRequest(r *http.Request) string {
	host := r.Host
	if colonIdx := strings.LastIndex(host, ":"); colonIdx != -1 {
		host = host[:colonIdx]
	}
	return host
}

// rpConfigFromRequest derives the WebAuthn relying party config from the
// incoming HTTP request. Each self-hosted deployment has a different domain.
func rpConfigFromRequest(r *http.Request) *webauthn.Config {
	host := r.Host

	// RP ID is the hostname without port
	rpID := host
	if colonIdx := strings.LastIndex(rpID, ":"); colonIdx != -1 {
		rpID = rpID[:colonIdx]
	}

	// Determine scheme
	scheme := "https"
	if r.TLS == nil {
		if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}

	origin := fmt.Sprintf("%s://%s", scheme, host)

	return &webauthn.Config{
		RPID:          rpID,
		RPDisplayName: "Cantinarr",
		RPOrigins:     []string{origin},
	}
}

// ─── WebAuthn User Adapter ───────────────────────────────

// WebAuthnUser wraps our User model to implement the webauthn.User interface.
type WebAuthnUser struct {
	user        *User
	credentials []webauthn.Credential
}

func (u *WebAuthnUser) WebAuthnID() []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(u.user.ID))
	return b
}

func (u *WebAuthnUser) WebAuthnName() string {
	return u.user.Username
}

func (u *WebAuthnUser) WebAuthnDisplayName() string {
	return u.user.Username
}

func (u *WebAuthnUser) WebAuthnCredentials() []webauthn.Credential {
	return u.credentials
}

// ─── Session Store ───────────────────────────────────────

type webauthnSession struct {
	data      webauthn.SessionData
	userID    int64
	expiresAt time.Time
}

// SessionStore holds WebAuthn ceremony sessions in memory with a 5-minute TTL.
type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]*webauthnSession
}

func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*webauthnSession),
	}
}

func (s *SessionStore) Save(userID int64, data webauthn.SessionData) string {
	id := uuid.New().String()
	s.mu.Lock()
	defer s.mu.Unlock()

	// Clean expired sessions
	now := time.Now()
	for k, v := range s.sessions {
		if now.After(v.expiresAt) {
			delete(s.sessions, k)
		}
	}

	s.sessions[id] = &webauthnSession{
		data:      data,
		userID:    userID,
		expiresAt: now.Add(5 * time.Minute),
	}
	return id
}

func (s *SessionStore) Get(sessionID string, userID int64) (*webauthn.SessionData, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok || time.Now().After(sess.expiresAt) || sess.userID != userID {
		delete(s.sessions, sessionID)
		return nil, false
	}
	delete(s.sessions, sessionID) // one-time use
	return &sess.data, true
}

// GetLogin retrieves a login session (userID 0 means discoverable login).
func (s *SessionStore) GetLogin(sessionID string) (*webauthn.SessionData, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok || time.Now().After(sess.expiresAt) {
		delete(s.sessions, sessionID)
		return nil, false
	}
	delete(s.sessions, sessionID)
	return &sess.data, true
}

// ─── Service Methods ─────────────────────────────────────

// loadWebAuthnUser loads a user and their credentials for the given RP ID.
func (s *Service) loadWebAuthnUser(userID int64, rpID string) (*WebAuthnUser, error) {
	user, err := s.getUserByID(userID)
	if err != nil {
		return nil, err
	}
	creds, err := s.loadCredentials(userID, rpID)
	if err != nil {
		return nil, err
	}
	return &WebAuthnUser{user: user, credentials: creds}, nil
}

func (s *Service) loadCredentials(userID int64, rpID string) ([]webauthn.Credential, error) {
	rows, err := s.db.Query(
		"SELECT id, public_key, attestation_type, aaguid, sign_count, backup_eligible, backup_state FROM webauthn_credentials WHERE user_id = ? AND rp_id = ?",
		userID, rpID,
	)
	if err != nil {
		return nil, fmt.Errorf("query credentials: %w", err)
	}
	defer rows.Close()

	var creds []webauthn.Credential
	for rows.Next() {
		var (
			idHex          string
			pubKey         []byte
			attType        string
			aaguid         []byte
			signCnt        uint32
			backupEligible bool
			backupState    bool
		)
		if err := rows.Scan(&idHex, &pubKey, &attType, &aaguid, &signCnt, &backupEligible, &backupState); err != nil {
			return nil, fmt.Errorf("scan credential: %w", err)
		}
		credID, _ := hex.DecodeString(idHex)
		creds = append(creds, webauthn.Credential{
			ID:              credID,
			PublicKey:       pubKey,
			AttestationType: attType,
			Flags: webauthn.CredentialFlags{
				BackupEligible: backupEligible,
				BackupState:    backupState,
			},
			Authenticator: webauthn.Authenticator{
				AAGUID:    aaguid,
				SignCount: signCnt,
			},
		})
	}
	return creds, nil
}

// loadAllCredentials loads all credentials for a user across all RP IDs (for discoverable login).
func (s *Service) loadAllCredentials(userID int64) ([]webauthn.Credential, error) {
	rows, err := s.db.Query(
		"SELECT id, public_key, attestation_type, aaguid, sign_count, backup_eligible, backup_state FROM webauthn_credentials WHERE user_id = ?",
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("query credentials: %w", err)
	}
	defer rows.Close()

	var creds []webauthn.Credential
	for rows.Next() {
		var (
			idHex          string
			pubKey         []byte
			attType        string
			aaguid         []byte
			signCnt        uint32
			backupEligible bool
			backupState    bool
		)
		if err := rows.Scan(&idHex, &pubKey, &attType, &aaguid, &signCnt, &backupEligible, &backupState); err != nil {
			return nil, fmt.Errorf("scan credential: %w", err)
		}
		credID, _ := hex.DecodeString(idHex)
		creds = append(creds, webauthn.Credential{
			ID:              credID,
			PublicKey:       pubKey,
			AttestationType: attType,
			Flags: webauthn.CredentialFlags{
				BackupEligible: backupEligible,
				BackupState:    backupState,
			},
			Authenticator: webauthn.Authenticator{
				AAGUID:    aaguid,
				SignCount: signCnt,
			},
		})
	}
	return creds, nil
}

func (s *Service) BeginPasskeyRegistration(userID int64, r *http.Request) (interface{}, string, error) {
	cfg := rpConfigFromRequest(r)
	wa, err := webauthn.New(cfg)
	if err != nil {
		return nil, "", fmt.Errorf("create webauthn: %w", err)
	}

	waUser, err := s.loadWebAuthnUser(userID, cfg.RPID)
	if err != nil {
		return nil, "", fmt.Errorf("load user: %w", err)
	}

	options, session, err := wa.BeginRegistration(waUser,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
	)
	if err != nil {
		return nil, "", fmt.Errorf("begin registration: %w", err)
	}

	sessionID := s.webauthnSessions.Save(userID, *session)
	return options, sessionID, nil
}

func (s *Service) FinishPasskeyRegistration(userID int64, sessionID, credentialName string, r *http.Request) error {
	cfg := rpConfigFromRequest(r)
	wa, err := webauthn.New(cfg)
	if err != nil {
		return fmt.Errorf("create webauthn: %w", err)
	}

	session, ok := s.webauthnSessions.Get(sessionID, userID)
	if !ok {
		return fmt.Errorf("session expired or invalid")
	}

	waUser, err := s.loadWebAuthnUser(userID, cfg.RPID)
	if err != nil {
		return fmt.Errorf("load user: %w", err)
	}

	cred, err := wa.FinishRegistration(waUser, *session, r)
	if err != nil {
		return fmt.Errorf("finish registration: %w", err)
	}

	credID := hex.EncodeToString(cred.ID)
	if credentialName == "" {
		credentialName = "Passkey"
	}

	_, err = s.db.Exec(
		"INSERT INTO webauthn_credentials (id, user_id, public_key, attestation_type, aaguid, sign_count, backup_eligible, backup_state, rp_id, name) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		credID, userID, cred.PublicKey, cred.AttestationType, cred.Authenticator.AAGUID, cred.Authenticator.SignCount, cred.Flags.BackupEligible, cred.Flags.BackupState, cfg.RPID, credentialName,
	)
	if err != nil {
		return fmt.Errorf("store credential: %w", err)
	}

	return nil
}

func (s *Service) BeginPasskeyLogin(r *http.Request) (interface{}, string, error) {
	cfg := rpConfigFromRequest(r)
	wa, err := webauthn.New(cfg)
	if err != nil {
		return nil, "", fmt.Errorf("create webauthn: %w", err)
	}

	options, session, err := wa.BeginDiscoverableLogin()
	if err != nil {
		return nil, "", fmt.Errorf("begin login: %w", err)
	}

	// Use userID 0 for discoverable login sessions
	sessionID := s.webauthnSessions.Save(0, *session)
	return options, sessionID, nil
}

func (s *Service) FinishPasskeyLogin(sessionID string, r *http.Request) (*TokenResponse, error) {
	cfg := rpConfigFromRequest(r)
	wa, err := webauthn.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("create webauthn: %w", err)
	}

	session, ok := s.webauthnSessions.GetLogin(sessionID)
	if !ok {
		return nil, fmt.Errorf("session expired or invalid")
	}

	// Discoverable credential handler: look up user by their WebAuthn user handle
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		if len(userHandle) < 8 {
			return nil, fmt.Errorf("invalid user handle")
		}
		userID := int64(binary.BigEndian.Uint64(userHandle))
		waUser, err := s.loadWebAuthnUser(userID, cfg.RPID)
		if err != nil {
			return nil, err
		}
		return waUser, nil
	}

	user, cred, err := wa.FinishPasskeyLogin(handler, *session, r)
	if err != nil {
		return nil, fmt.Errorf("finish login: %w", err)
	}

	// Update sign count and last_used_at
	credID := hex.EncodeToString(cred.ID)
	_, _ = s.db.Exec(
		"UPDATE webauthn_credentials SET sign_count = ?, last_used_at = ? WHERE id = ?",
		cred.Authenticator.SignCount, time.Now(), credID,
	)

	// Extract user ID from the WebAuthn user
	waUser := user.(*WebAuthnUser)

	// Create device and generate tokens
	deviceID := uuid.New().String()
	_, err = s.db.Exec(
		"INSERT INTO devices (id, user_id, device_name) VALUES (?, ?, ?)",
		deviceID, waUser.user.ID, "Passkey",
	)
	if err != nil {
		return nil, fmt.Errorf("create device: %w", err)
	}

	resp, err := s.generateTokens(waUser.user, deviceID)
	if err != nil {
		return nil, err
	}
	resp.DeviceID = deviceID
	return resp, nil
}

func (s *Service) ListPasskeys(userID int64, rpID string) ([]PasskeyInfo, error) {
	rows, err := s.db.Query(
		"SELECT id, name, created_at, last_used_at FROM webauthn_credentials WHERE user_id = ? AND rp_id = ? ORDER BY created_at DESC",
		userID, rpID,
	)
	if err != nil {
		return nil, fmt.Errorf("query passkeys: %w", err)
	}
	defer rows.Close()

	var passkeys []PasskeyInfo
	for rows.Next() {
		var p PasskeyInfo
		if err := rows.Scan(&p.ID, &p.Name, &p.CreatedAt, &p.LastUsedAt); err != nil {
			return nil, fmt.Errorf("scan passkey: %w", err)
		}
		passkeys = append(passkeys, p)
	}
	if passkeys == nil {
		passkeys = []PasskeyInfo{}
	}
	return passkeys, nil
}

func (s *Service) DeletePasskey(userID int64, credentialID, rpID string) error {
	result, err := s.db.Exec(
		"DELETE FROM webauthn_credentials WHERE id = ? AND user_id = ? AND rp_id = ?",
		credentialID, userID, rpID,
	)
	if err != nil {
		return fmt.Errorf("delete passkey: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("passkey not found")
	}
	return nil
}

// findUserByCredentialID looks up which user owns a given credential ID.
func (s *Service) findUserByCredentialID(credentialID []byte) (int64, error) {
	credIDHex := hex.EncodeToString(credentialID)
	var userID int64
	err := s.db.QueryRow(
		"SELECT user_id FROM webauthn_credentials WHERE id = ?", credIDHex,
	).Scan(&userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, fmt.Errorf("credential not found")
		}
		return 0, fmt.Errorf("query credential: %w", err)
	}
	return userID, nil
}

// generateSessionID creates a random session ID.
func generateSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
