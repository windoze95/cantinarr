package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/mediaserver"
	"github.com/windoze95/cantinarr-server/internal/plex"
)

var (
	ErrPlexUnavailable = errors.New("Plex could not be reached or verified; please try again")
	ErrPlexDenied      = errors.New("this Plex account is not linked to a permitted Cantinarr account; ask an administrator to review its sign-in link")
	ErrPlexConflict    = errors.New("this Plex sign-in identity or Cantinarr user already has a different link; ask an administrator to review Linked sign-in")
	ErrPlexFlow        = errors.New("this Plex sign-in attempt is invalid or expired; please start again")
	ErrPlexDisabled    = errors.New("Plex sign-in is disabled")
)

type PlexConfig struct {
	Enabled    bool `json:"enabled"`
	AutoCreate bool `json:"auto_create"`
}
type plexConfiguration struct {
	PlexConfig
	raw, ssoOnly string
}

func (s *Service) plexConfiguration() (plexConfiguration, error) {
	var c plexConfiguration
	if err := s.db.QueryRow("SELECT COALESCE((SELECT value FROM settings WHERE key='plex_auth'),'{}'), COALESCE((SELECT value FROM settings WHERE key='oidc_sso_only'),'false')").Scan(&c.raw, &c.ssoOnly); err != nil {
		return c, ErrAuthUnavailable
	}
	if c.raw == "null" || json.Unmarshal([]byte(c.raw), &c.PlexConfig) != nil {
		return c, ErrAuthUnavailable
	}
	return c, nil
}
func (s *Service) savePlexConfig(c PlexConfig, actor *Claims) (PlexConfig, error) {
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return c, ErrAuthUnavailable
	}
	defer tx.Rollback()
	if err = oidcActorInTransaction(tx, actor, true); err != nil {
		return c, err
	}
	raw, _ := json.Marshal(c)
	enabled := "false"
	if c.Enabled {
		enabled = "true"
	}
	if _, err = tx.Exec("INSERT OR REPLACE INTO settings(key,value) VALUES ('plex_auth',?),('plex_auth_enabled',?)", string(raw), enabled); err != nil {
		return c, ErrAuthUnavailable
	}
	if !c.Enabled {
		if _, err = tx.Exec("UPDATE devices SET revoked_at=? WHERE auth_method='plex' AND revoked_at IS NULL", time.Now()); err != nil {
			return c, ErrAuthUnavailable
		}
		if _, err = tx.Exec("DELETE FROM oauth_authorization_codes WHERE auth_method='plex'"); err != nil {
			return c, ErrAuthUnavailable
		}
	}
	if err = tx.Commit(); err != nil {
		return c, ErrAuthUnavailable
	}
	s.plexFlows.clear()
	s.clearPlexConsents()
	return c, nil
}

type PlexIdentity struct {
	AccountID int64     `json:"plex_account_id"`
	Email     string    `json:"email"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Service) plexIdentities(userID int64) ([]PlexIdentity, error) {
	rows, err := s.db.Query("SELECT plex_account_id,email,username,created_at FROM plex_identities WHERE user_id=?", userID)
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	defer rows.Close()
	out := []PlexIdentity{}
	for rows.Next() {
		var p PlexIdentity
		if rows.Scan(&p.AccountID, &p.Email, &p.Username, &p.CreatedAt) != nil {
			return nil, ErrAuthUnavailable
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func linkPlexIdentity(tx *sql.Tx, userID int64, p plex.Account) error {
	if userID <= 0 || p.ID <= 0 {
		return ErrPlexDenied
	}
	var conflict bool
	if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM plex_identities WHERE (user_id=? AND plex_account_id!=?) OR (plex_account_id=? AND user_id!=?))", userID, p.ID, p.ID, userID).Scan(&conflict); err != nil {
		return ErrAuthUnavailable
	}
	if conflict {
		return ErrPlexConflict
	}
	if _, err := tx.Exec("INSERT INTO plex_identities(plex_account_id,user_id,email,username) VALUES (?,?,?,?) ON CONFLICT(plex_account_id) DO UPDATE SET email=excluded.email,username=excluded.username", p.ID, userID, p.Email, p.Username); err != nil {
		return ErrAuthUnavailable
	}
	return nil
}

// PlexDirectory supplies fresh account/share proof, never a stored email or a
// friendship. Unavailable servers remain explicit so review cannot claim absence.
type PlexDirectory interface {
	PlexAccounts(context.Context) ([]PlexServerAccounts, error)
}
type PlexServerAccounts struct {
	InstanceID string              `json:"instance_id"`
	Name       string              `json:"name"`
	Error      string              `json:"error,omitempty"`
	Revision   string              `json:"-"`
	Accounts   []PlexSharedAccount `json:"-"`
}
type PlexSharedAccount struct {
	Account  plex.Account
	Accepted bool
}

func (s *Service) SetPlexDirectory(d PlexDirectory) { s.plexDirectory = d }

// Hash the encrypted storage representation, not plaintext credentials. Recheck
// it in the identity transaction so an edited/deleted instance cannot qualify.
func PlexInstanceRevision(q interface{ QueryRow(string, ...any) *sql.Row }, id string) (string, error) {
	var rawURL, key, config string
	if err := q.QueryRow("SELECT url,api_key,media_server_config FROM service_instances WHERE id=? AND service_type='plex'", id).Scan(&rawURL, &key, &config); err != nil {
		return "", ErrPlexUnavailable
	}
	return hashToken(rawURL + "\x00" + key + "\x00" + config), nil
}
func (s *Service) plexAccounts(ctx context.Context) ([]PlexServerAccounts, error) {
	if s.plexDirectory == nil {
		return nil, ErrPlexUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return s.plexDirectory.PlexAccounts(ctx)
}

type plexMediaLink struct {
	userID                         int64
	username, instanceID, remoteID string
}
type PlexCandidate struct {
	UserID       int64                `json:"user_id"`
	Username     string               `json:"username"`
	AccountID    int64                `json:"plex_account_id"`
	Email        string               `json:"email"`
	PlexUsername string               `json:"plex_username"`
	Servers      []PlexServerAccounts `json:"servers"`
	Reason       string               `json:"reason,omitempty"`
	Confirmed    bool                 `json:"confirmed"`
	links        []plexMediaLink
}

func (s *Service) plexCandidates(ctx context.Context) ([]PlexCandidate, error) {
	servers, err := s.plexAccounts(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT u.id,u.username,a.instance_id,a.remote_user_id FROM user_media_server_accounts a JOIN users u ON u.id=a.user_id JOIN service_instances si ON si.id=a.instance_id WHERE si.service_type='plex' ORDER BY u.id,a.instance_id`)
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	links := []plexMediaLink{}
	for rows.Next() {
		var l plexMediaLink
		if rows.Scan(&l.userID, &l.username, &l.instanceID, &l.remoteID) != nil {
			rows.Close()
			return nil, ErrAuthUnavailable
		}
		links = append(links, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	byUser := map[int64]*PlexCandidate{}
	for _, l := range links {
		c := byUser[l.userID]
		if c == nil {
			c = &PlexCandidate{UserID: l.userID, Username: l.username, Servers: []PlexServerAccounts{}}
			byUser[l.userID] = c
		}
		c.links = append(c.links, l)
		var server *PlexServerAccounts
		for i := range servers {
			if servers[i].InstanceID == l.instanceID {
				server = &servers[i]
				break
			}
		}
		if server == nil {
			c.Reason = "The linked Plex server could not be verified."
			continue
		}
		c.Servers = append(c.Servers, *server)
		if server.Error != "" {
			c.Reason = "Plex account data is unavailable for a linked server."
			continue
		}
		if !mediaserver.ValidEmail(mediaserver.CanonicalEmail(l.remoteID)) {
			c.Reason = "This media link has no verified Plex email to review."
			continue
		}
		matches := map[int64]plex.Account{}
		for _, a := range server.Accounts {
			if a.Account.ID > 0 && mediaserver.CanonicalEmail(a.Account.Email) == mediaserver.CanonicalEmail(l.remoteID) {
				matches[a.Account.ID] = a.Account
			}
		}
		if len(matches) != 1 {
			c.Reason = "No unique Plex account matches this existing media link."
			continue
		}
		for id, p := range matches {
			if c.AccountID != 0 && c.AccountID != id {
				c.Reason = "The existing server links identify different Plex accounts."
			} else {
				c.AccountID = id
				c.Email = p.Email
				c.PlexUsername = p.Username
			}
		}
	}
	out := []PlexCandidate{}
	for _, c := range byUser {
		for _, other := range byUser {
			if c.UserID != other.UserID && c.AccountID > 0 && c.AccountID == other.AccountID {
				c.Reason = "More than one Cantinarr user has links to this Plex account."
			}
		}
		identities, err := s.plexIdentities(c.UserID)
		if err != nil {
			return nil, err
		}
		if len(identities) > 0 {
			c.Confirmed = identities[0].AccountID == c.AccountID
			if !c.Confirmed {
				c.Reason = ErrPlexConflict.Error()
			}
		}
		var claimed bool
		if c.AccountID > 0 {
			if s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM plex_identities WHERE plex_account_id=? AND user_id!=?)", c.AccountID, c.UserID).Scan(&claimed) != nil {
				return nil, ErrAuthUnavailable
			}
			if claimed {
				c.Reason = ErrPlexConflict.Error()
			}
		}
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UserID < out[j].UserID })
	return out, nil
}

type PlexMapping struct {
	UserID    int64 `json:"user_id"`
	AccountID int64 `json:"plex_account_id"`
}

func verifyPlexCandidate(tx *sql.Tx, c PlexCandidate) error {
	if c.Reason != "" || c.AccountID <= 0 {
		return ErrPlexConflict
	}
	var count int
	if tx.QueryRow("SELECT COUNT(*) FROM user_media_server_accounts a JOIN service_instances si ON si.id=a.instance_id WHERE a.user_id=? AND si.service_type='plex'", c.UserID).Scan(&count) != nil || count != len(c.links) {
		return ErrPlexFlow
	}
	for _, l := range c.links {
		var found bool
		if tx.QueryRow("SELECT EXISTS(SELECT 1 FROM user_media_server_accounts WHERE user_id=? AND instance_id=? AND remote_user_id=?)", l.userID, l.instanceID, l.remoteID).Scan(&found) != nil || !found {
			return ErrPlexFlow
		}
	}
	for _, server := range c.Servers {
		revision, err := PlexInstanceRevision(tx, server.InstanceID)
		if err != nil || revision != server.Revision {
			return ErrPlexFlow
		}
	}
	return nil
}
func (s *Service) confirmPlexMappings(ctx context.Context, actor *Claims, selected []PlexMapping) error {
	if len(selected) == 0 || len(selected) > 200 {
		return ErrPlexFlow
	}
	candidates, err := s.plexCandidates(ctx)
	if err != nil {
		return err
	}
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return ErrAuthUnavailable
	}
	defer tx.Rollback()
	if err = oidcActorInTransaction(tx, actor, true); err != nil {
		return err
	}
	for _, m := range selected {
		var c *PlexCandidate
		for i := range candidates {
			if candidates[i].UserID == m.UserID && candidates[i].AccountID == m.AccountID {
				c = &candidates[i]
				break
			}
		}
		if c == nil {
			return ErrPlexConflict
		}
		if err = verifyPlexCandidate(tx, *c); err != nil {
			return err
		}
		if err = linkPlexIdentity(tx, c.UserID, plex.Account{ID: c.AccountID, Email: c.Email, Username: c.PlexUsername}); err != nil {
			return err
		}
	}
	if tx.Commit() != nil {
		return ErrAuthUnavailable
	}
	return nil
}

// ConfirmPlexMediaLink is called only after an admin explicitly selects an
// existing remote account. A typed invite address never enters this seam.
func (s *Service) ConfirmPlexMediaLink(ctx context.Context, userID int64) error {
	candidates, err := s.plexCandidates(ctx)
	if err != nil {
		return err
	}
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return ErrAuthUnavailable
	}
	defer tx.Rollback()
	for _, c := range candidates {
		if c.UserID == userID {
			if err = verifyPlexCandidate(tx, c); err != nil {
				return err
			}
			if err = linkPlexIdentity(tx, userID, plex.Account{ID: c.AccountID, Email: c.Email, Username: c.PlexUsername}); err != nil {
				return err
			}
			if tx.Commit() != nil {
				return ErrAuthUnavailable
			}
			return nil
		}
	}
	return ErrPlexDenied
}

// Existing authenticated media-link PINs prove the same numeric identity, but
// their initiating session and unlink generation must still be alive at commit.
func (s *Service) BeginPlexIdentityProof(actor *Claims) (uint64, error) {
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	if actor == nil {
		return 0, ErrInvalidCredentials
	}
	if _, err := s.authoritativeSession(context.Background(), actor.UserID, actor.DeviceID); err != nil {
		return 0, err
	}
	return s.plexFlows.currentGeneration(), nil
}
func (s *Service) CompletePlexIdentityProof(actor *Claims, generation uint64, p plex.Account) error {
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	if generation != s.plexFlows.currentGeneration() {
		return ErrPlexFlow
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ErrAuthUnavailable
	}
	defer tx.Rollback()
	if err = oidcActorInTransaction(tx, actor, false); err != nil {
		return err
	}
	if err = linkPlexIdentity(tx, actor.UserID, p); err != nil {
		return err
	}
	if tx.Commit() != nil {
		return ErrAuthUnavailable
	}
	return nil
}

func (s *Service) unlinkPlex(actor *Claims, userID int64) error {
	if actor == nil {
		return ErrInvalidCredentials
	}
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return ErrAuthUnavailable
	}
	defer tx.Rollback()
	if err = oidcActorInTransaction(tx, actor, actor.UserID != userID); err != nil {
		return err
	}
	if actor.UserID == userID {
		var local bool
		if tx.QueryRow(`SELECT (role='admin' OR COALESCE((SELECT value FROM settings WHERE key='oidc_sso_only'),'false')='false') AND ((password_hash!='' AND (password_enabled=1 OR role='admin')) OR ((passkey_enabled=1 OR role='admin') AND EXISTS(SELECT 1 FROM webauthn_credentials WHERE user_id=?))) FROM users WHERE id=?`, userID, userID).Scan(&local) != nil {
			return ErrAuthUnavailable
		}
		var raw string
		if tx.QueryRow("SELECT COALESCE((SELECT value FROM settings WHERE key='oidc_config'),'{}')").Scan(&raw) != nil {
			return ErrAuthUnavailable
		}
		var oidc oidcStoredConfig
		var linked bool
		if json.Unmarshal([]byte(raw), &oidc) == nil && oidc.Config.Enabled && strings.TrimSpace(oidc.Config.Issuer) != "" {
			if tx.QueryRow("SELECT EXISTS(SELECT 1 FROM oidc_identities WHERE user_id=? AND issuer=?)", userID, oidc.Config.Issuer).Scan(&linked) != nil {
				return ErrAuthUnavailable
			}
		}
		if !local && !linked {
			return errors.New("add a permitted password, passkey, or single sign-on identity before unlinking Plex")
		}
	}
	if _, err = tx.Exec("UPDATE devices SET revoked_at=? WHERE user_id=? AND auth_method='plex' AND revoked_at IS NULL", time.Now(), userID); err != nil {
		return ErrAuthUnavailable
	}
	if _, err = tx.Exec("DELETE FROM oauth_authorization_codes WHERE user_id=? AND auth_method='plex'", userID); err != nil {
		return ErrAuthUnavailable
	}
	if _, err = tx.Exec("DELETE FROM plex_identities WHERE user_id=?", userID); err != nil {
		return ErrAuthUnavailable
	}
	if tx.Commit() != nil {
		return ErrAuthUnavailable
	}
	s.plexFlows.clear()
	s.clearPlexConsents()
	return nil
}
