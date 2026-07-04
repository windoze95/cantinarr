package plex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// Settings-table keys. The token is the only secret and is stored encrypted;
// the rest is plain configuration. The client identifier is generated once
// and kept across unlink/relink so plex.tv sees one stable device.
const (
	settingClientID   = "plex_client_id"
	settingToken      = "plex_token" // encrypted
	settingAccount    = "plex_account"
	settingMachineID  = "plex_machine_id"
	settingServerName = "plex_server_name"
	settingLibraryIDs = "plex_library_ids" // JSON array
	settingAutoInvite = "plex_auto_invite" // "1" / "0"
)

var (
	// ErrNotLinked: no Plex account has been linked yet.
	ErrNotLinked = errors.New("no plex account linked")
	// ErrNotConfigured: linked, but no server selected to invite to.
	ErrNotConfigured = errors.New("plex invites are not configured")
	// ErrNoEmail: the target user never shared a Plex email.
	ErrNoEmail = errors.New("user has not shared a plex email")
)

// api is the plex.tv surface the service uses; *Client satisfies it. An
// interface so service tests can fake plex.tv without HTTP.
type api interface {
	CreatePin(ctx context.Context, clientID string) (*Pin, error)
	CheckPin(ctx context.Context, clientID string, id int64) (*Pin, error)
	AuthURL(clientID, code string) string
	GetUser(ctx context.Context, clientID, token string) (*Account, error)
	ListServers(ctx context.Context, clientID, token string) ([]Server, error)
	ListLibraries(ctx context.Context, clientID, token, machineID string) ([]Library, error)
	InviteEmail(ctx context.Context, clientID, token, machineID, email string, sectionIDs []int64) error
}

// notifier is the WS+push fan-out (the push.Composite). Event types are the
// push package's category strings, passed as literals to avoid the import.
type notifier interface {
	NotifyUser(userID int64, eventType string, data map[string]interface{})
	NotifyAdmins(eventType string, data map[string]interface{})
}

// Service owns the linked Plex account (token encrypted at rest), the invite
// configuration, and the invite flows: one-tap from the Users screen and
// auto-invite when a user shares their email.
type Service struct {
	db       *sql.DB
	cipher   *secrets.Cipher
	api      api
	notifier notifier
	logger   *slog.Logger
}

func NewService(db *sql.DB, cipher *secrets.Cipher, api api, notifier notifier, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{db: db, cipher: cipher, api: api, notifier: notifier, logger: logger}
}

// Status is the admin-facing view of the integration. The token is never
// included — linked/account are all a client learns about the credential.
type Status struct {
	Linked            bool    `json:"linked"`
	Account           string  `json:"account,omitempty"`
	MachineIdentifier string  `json:"machine_identifier,omitempty"`
	ServerName        string  `json:"server_name,omitempty"`
	LibrarySectionIDs []int64 `json:"library_section_ids"`
	AutoInvite        bool    `json:"auto_invite"`
	// Configured: invites can actually be sent (linked + server selected).
	Configured bool `json:"configured"`
}

func (s *Service) Status() Status {
	st := Status{LibrarySectionIDs: []int64{}}
	_, linked := s.token()
	st.Linked = linked
	if !linked {
		return st
	}
	st.Account, _ = s.getSetting(settingAccount)
	st.MachineIdentifier, _ = s.getSetting(settingMachineID)
	st.ServerName, _ = s.getSetting(settingServerName)
	st.LibrarySectionIDs = s.libraryIDs()
	if v, ok := s.getSetting(settingAutoInvite); ok && v == "1" {
		st.AutoInvite = true
	}
	st.Configured = st.MachineIdentifier != ""
	return st
}

// BeginLink starts the PIN flow: returns the PIN id the app polls with and
// the plex.tv URL the admin opens to approve the link.
func (s *Service) BeginLink(ctx context.Context) (pinID int64, code, authURL string, err error) {
	clientID, err := s.clientID()
	if err != nil {
		return 0, "", "", err
	}
	pin, err := s.api.CreatePin(ctx, clientID)
	if err != nil {
		return 0, "", "", err
	}
	return pin.ID, pin.Code, s.api.AuthURL(clientID, pin.Code), nil
}

// CheckLink polls a PIN. Once approved it verifies the token, persists it
// encrypted, and records the account name. linked=false with nil error means
// "still waiting".
func (s *Service) CheckLink(ctx context.Context, pinID int64) (linked bool, account string, err error) {
	clientID, err := s.clientID()
	if err != nil {
		return false, "", err
	}
	pin, err := s.api.CheckPin(ctx, clientID, pinID)
	if err != nil {
		return false, "", err
	}
	if pin.AuthToken == "" {
		return false, "", nil
	}
	acct, err := s.api.GetUser(ctx, clientID, pin.AuthToken)
	if err != nil {
		return false, "", fmt.Errorf("verify token: %w", err)
	}
	enc, err := s.cipher.Encrypt(pin.AuthToken)
	if err != nil {
		return false, "", fmt.Errorf("encrypt token: %w", err)
	}
	if err := s.setSetting(settingToken, enc); err != nil {
		return false, "", err
	}
	if err := s.setSetting(settingAccount, acct.Username); err != nil {
		return false, "", err
	}
	return true, acct.Username, nil
}

// Unlink forgets the token and invite configuration (keeps the client id so
// plex.tv keeps seeing one stable device across relinks).
func (s *Service) Unlink() error {
	for _, key := range []string{settingToken, settingAccount, settingMachineID, settingServerName, settingLibraryIDs, settingAutoInvite} {
		if _, err := s.db.Exec("DELETE FROM settings WHERE key = ?", key); err != nil {
			return fmt.Errorf("unlink plex: %w", err)
		}
	}
	return nil
}

// Servers lists the linked account's owned Plex Media Servers.
func (s *Service) Servers(ctx context.Context) ([]Server, error) {
	clientID, token, err := s.credentials()
	if err != nil {
		return nil, err
	}
	return s.api.ListServers(ctx, clientID, token)
}

// Libraries lists a server's sections (plex.tv-global ids) for the picker.
func (s *Service) Libraries(ctx context.Context, machineID string) ([]Library, error) {
	clientID, token, err := s.credentials()
	if err != nil {
		return nil, err
	}
	return s.api.ListLibraries(ctx, clientID, token, machineID)
}

// UpdateSettings selects the server and libraries invites share, and whether
// invites go out automatically when a user shares their email. An empty
// library list means "all libraries" (plex.tv semantics).
func (s *Service) UpdateSettings(machineID, serverName string, libraryIDs []int64, autoInvite bool) error {
	if _, linked := s.token(); !linked {
		return ErrNotLinked
	}
	if machineID == "" {
		return ErrNotConfigured
	}
	if libraryIDs == nil {
		libraryIDs = []int64{}
	}
	encoded, err := json.Marshal(libraryIDs)
	if err != nil {
		return err
	}
	auto := "0"
	if autoInvite {
		auto = "1"
	}
	for key, value := range map[string]string{
		settingMachineID:  machineID,
		settingServerName: serverName,
		settingLibraryIDs: string(encoded),
		settingAutoInvite: auto,
	} {
		if err := s.setSetting(key, value); err != nil {
			return err
		}
	}
	return nil
}

// InviteOutcome reports what an invite attempt did.
type InviteOutcome struct {
	Email string
	// AlreadyShared: plex.tv says the account already has access; treated as
	// success (invited-at is stamped) but the user is not pushed "check your
	// email" for an invite that never arrives.
	AlreadyShared bool
}

// InviteUser shares the configured libraries with the user's Plex email and
// stamps users.plex_invited_at. On a fresh invite the user gets a
// "plex_invite_sent" push so they know to check their inbox.
func (s *Service) InviteUser(ctx context.Context, userID int64) (*InviteOutcome, error) {
	clientID, token, err := s.credentials()
	if err != nil {
		return nil, err
	}
	machineID, ok := s.getSetting(settingMachineID)
	if !ok || machineID == "" {
		return nil, ErrNotConfigured
	}

	var email string
	if err := s.db.QueryRow("SELECT plex_email FROM users WHERE id = ?", userID).Scan(&email); err != nil {
		return nil, fmt.Errorf("load user: %w", err)
	}
	if email == "" {
		return nil, ErrNoEmail
	}

	outcome := &InviteOutcome{Email: email}
	err = s.api.InviteEmail(ctx, clientID, token, machineID, email, s.libraryIDs())
	switch {
	case errors.Is(err, ErrAlreadyShared):
		outcome.AlreadyShared = true
	case err != nil:
		return nil, err
	}

	if _, err := s.db.Exec("UPDATE users SET plex_invited_at = CURRENT_TIMESTAMP WHERE id = ?", userID); err != nil {
		s.logger.Error("plex: stamp invited_at", "err", err, "user_id", userID)
	}
	if !outcome.AlreadyShared && s.notifier != nil {
		s.notifier.NotifyUser(userID, "plex_invite_sent", map[string]interface{}{})
	}
	return outcome, nil
}

// OnAccessRequest runs after a user shares a new or changed Plex email (wired
// to auth's access-request hook). Fire-and-forget: auto-invites when
// configured, then notifies admins with the outcome so the push tells them
// whether anything is left to do.
func (s *Service) OnAccessRequest(userID int64, username string) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.logger.Error("plex: access request handling panicked", "err", fmt.Sprint(r))
			}
		}()
		s.handleAccessRequest(userID, username)
	}()
}

// handleAccessRequest is OnAccessRequest's synchronous body (split for tests).
func (s *Service) handleAccessRequest(userID int64, username string) {
	inviteState := "" // "" = needs a manual invite
	st := s.Status()
	if st.Configured && st.AutoInvite {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := s.InviteUser(ctx, userID); err != nil {
			s.logger.Error("plex: auto-invite failed", "err", err, "user_id", userID)
			inviteState = "failed"
		} else {
			inviteState = "sent"
		}
	}
	if s.notifier != nil {
		s.notifier.NotifyAdmins("plex_access_request", map[string]interface{}{
			"user_id":      userID,
			"username":     username,
			"invite_state": inviteState,
		})
	}
}

// credentials returns the client id and decrypted token, or ErrNotLinked.
func (s *Service) credentials() (clientID, token string, err error) {
	clientID, err = s.clientID()
	if err != nil {
		return "", "", err
	}
	token, ok := s.token()
	if !ok {
		return "", "", ErrNotLinked
	}
	return clientID, token, nil
}

// clientID returns the stable X-Plex-Client-Identifier, creating and
// persisting it on first use.
func (s *Service) clientID() (string, error) {
	if id, ok := s.getSetting(settingClientID); ok && id != "" {
		return id, nil
	}
	id := uuid.NewString()
	if err := s.setSetting(settingClientID, id); err != nil {
		return "", err
	}
	return id, nil
}

// token loads and decrypts the linked account token.
func (s *Service) token() (string, bool) {
	stored, ok := s.getSetting(settingToken)
	if !ok || stored == "" {
		return "", false
	}
	token, err := s.cipher.Decrypt(stored)
	if err != nil {
		s.logger.Error("plex: decrypt token", "err", err)
		return "", false
	}
	return token, true
}

func (s *Service) libraryIDs() []int64 {
	raw, ok := s.getSetting(settingLibraryIDs)
	if !ok || raw == "" {
		return []int64{}
	}
	var ids []int64
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return []int64{}
	}
	return ids
}

func (s *Service) getSetting(key string) (string, bool) {
	var value string
	if err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value); err != nil {
		return "", false
	}
	return value, true
}

func (s *Service) setSetting(key, value string) error {
	if _, err := s.db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", key, value); err != nil {
		return fmt.Errorf("store %s: %w", key, err)
	}
	return nil
}
