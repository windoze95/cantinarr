package mediaaccess

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

// UserCreator finds or creates the passwordless Cantinarr user an import
// names and issues its connect link. auth.Service is the production one;
// the import never touches the users table's identity columns itself.
type UserCreator interface {
	CreateConnectToken(createdBy int64, name, serverURL string) (*auth.CreateConnectTokenResponse, error)
}

// SetUserCreator wires the user creation an import relies on. Wired late by
// main like the notifier; without it the import answers ErrImportUnavailable.
func (s *Service) SetUserCreator(c UserCreator) {
	s.userCreator = c
}

// ErrImportUnavailable reports an import with no user creation wired.
var ErrImportUnavailable = errors.New("media server import is not available")

// maxImportAccounts bounds one import request.
const maxImportAccounts = 200

// Import error codes, one per row, in the order they are checked.
const (
	// ImportNotFound: the server lists no such account (or share).
	ImportNotFound = "not_found"
	// ImportAlreadyLinked: a Cantinarr user is already linked to it.
	ImportAlreadyLinked = "already_linked"
	// ImportUserFailed: the Cantinarr user could not be created.
	ImportUserFailed = "user_failed"
	// ImportUserHasAccount: the Cantinarr user of that name already has an
	// account on this server.
	ImportUserHasAccount = "user_has_account"
	// ImportLinkFailed: the user exists (and has their link) but the account
	// could not be linked; the admin can link it by hand.
	ImportLinkFailed = "link_failed"
)

// ImportResult is one requested account's outcome. Created says a Cantinarr
// user was made for it (an existing user of the same name is reused and
// gets no new connect link); Linked says the account is now that user's.
type ImportResult struct {
	RemoteUserID   string `json:"remote_user_id"`
	RemoteUsername string `json:"remote_username"`
	UserID         int64  `json:"user_id,omitempty"`
	Username       string `json:"username,omitempty"`
	Created        bool   `json:"created"`
	Linked         bool   `json:"linked"`
	Link           string `json:"link,omitempty"`
	OriginSource   string `json:"origin_source,omitempty"`
	Error          string `json:"error,omitempty"`
}

// ImportAccounts turns accounts the media server lists into Cantinarr users:
// for each requested id, a user named after the account (found or created;
// a new one gets a connect link for the admin to hand out), the instance
// grant, and the account linked to them, exactly as the admin's link picker
// does it. The admin's pick is the mapping; nothing on the server changes.
// Rows are best-effort and independent, each carrying its own outcome, so
// one failure never stops the rest; only a server that cannot list its
// accounts fails the whole call.
func (s *Service) ImportAccounts(ctx context.Context, adminID int64, instanceID, serverURL string, remoteIDs []string) ([]ImportResult, error) {
	if s.userCreator == nil {
		return nil, ErrImportUnavailable
	}
	inst, err := s.mediaServerInstance(instanceID)
	if err != nil {
		return nil, err
	}
	provider, err := s.providers(inst)
	if err != nil {
		return nil, ErrNotMediaServer
	}
	invite := mediaserver.KindOf(provider) == mediaserver.KindInvite
	listCtx, cancel := context.WithTimeout(ctx, createTimeout)
	remotes, err := provider.Users(listCtx)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	byID := make(map[string]mediaserver.RemoteUser, len(remotes))
	for _, remote := range remotes {
		id := remote.ID
		if invite {
			id = mediaserver.CanonicalEmail(id)
		}
		byID[id] = remote
	}

	results := make([]ImportResult, 0, len(remoteIDs))
	seen := map[string]bool{}
	for _, raw := range remoteIDs {
		id := strings.TrimSpace(raw)
		if invite {
			id = mediaserver.CanonicalEmail(id)
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		results = append(results, s.importOne(ctx, adminID, inst.ID, serverURL, invite, id, byID))
	}
	return results, nil
}

// importOne is one row of an import; every outcome is a result, never an
// error, so the caller's loop goes on.
func (s *Service) importOne(ctx context.Context, adminID int64, instanceID, serverURL string, invite bool, id string, byID map[string]mediaserver.RemoteUser) ImportResult {
	res := ImportResult{RemoteUserID: id}
	remote, ok := byID[id]
	if !ok {
		res.Error = ImportNotFound
		return res
	}
	res.RemoteUsername = remote.Name
	claimed, err := s.identityClaimed(instanceID, remote.ID, 0)
	if err != nil {
		s.logger.Error("mediaaccess: import: check link", "err", err, "instance_id", instanceID)
		res.Error = ImportLinkFailed
		return res
	}
	if claimed {
		res.Error = ImportAlreadyLinked
		return res
	}
	username := strings.TrimSpace(remote.Name)
	if username == "" {
		username = id
	}
	userID, err := s.userIDByName(username)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		token, err := s.userCreator.CreateConnectToken(adminID, username, serverURL)
		if err != nil {
			s.logger.Error("mediaaccess: import: create user", "err", err, "instance_id", instanceID)
			res.Error = ImportUserFailed
			return res
		}
		if userID, err = s.userIDByName(username); err != nil {
			s.logger.Error("mediaaccess: import: load created user", "err", err, "instance_id", instanceID)
			res.Error = ImportUserFailed
			return res
		}
		res.Created, res.Link, res.OriginSource = true, token.Link, token.OriginSource
	case err != nil:
		s.logger.Error("mediaaccess: import: load user", "err", err, "instance_id", instanceID)
		res.Error = ImportUserFailed
		return res
	}
	res.UserID, res.Username = userID, username
	if invite {
		// The share's email is the identity the guide and the invite pass key
		// by; an address the person shared themselves is never overwritten.
		if email, err := s.plexEmail(userID); err == nil && email == "" {
			if err := s.rememberEmail(userID, id); err != nil {
				s.logger.Warn("mediaaccess: import: remember email", "err", err, "user_id", userID, "instance_id", instanceID)
			}
		}
	}
	_, err = s.LinkAccount(ctx, userID, instanceID, id)
	switch {
	case err == nil:
		res.Linked = true
	case errors.Is(err, ErrAccountExists):
		res.Error = ImportUserHasAccount
	default:
		s.logger.Warn("mediaaccess: import: link account", "err", err, "user_id", userID, "instance_id", instanceID)
		res.Error = ImportLinkFailed
	}
	return res
}

// userIDByName resolves a Cantinarr user by exact username, the rule the
// connect-link path applies when it finds or creates one.
func (s *Service) userIDByName(username string) (int64, error) {
	var id int64
	err := s.db.QueryRow("SELECT id FROM users WHERE username = ?", username).Scan(&id)
	return id, err
}
