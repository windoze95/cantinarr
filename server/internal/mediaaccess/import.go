package mediaaccess

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

// UserCreator creates the passwordless Cantinarr user an import names and
// issues its connect link. auth.Service is the production one;
// the import never touches the users table's identity columns itself.
type UserCreator interface {
	CreateImportUser(createdBy int64, name, serverURL string) (int64, *auth.CreateConnectTokenResponse, error)
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
	// ImportUserHasAccount: the new Cantinarr user acquired another account
	// on this server before the import linked its remote account.
	ImportUserHasAccount = "user_has_account"
	// ImportUsernameConflict: an import cannot reuse a local namesake.
	ImportUsernameConflict = "username_conflict"
	// ImportLinkFailed: the user exists (and has their link) but the account
	// could not be linked; the admin can link it by hand.
	ImportLinkFailed = "link_failed"
)

// ImportResult is one requested account's outcome. Created says a Cantinarr
// user was made for it. Every provider requires a new user or an explicit
// account link; a remote display name never selects an existing local user.
// Linked says the media account is now that user's.
type ImportResult struct {
	PlexIdentityError string `json:"plex_identity_error,omitempty"`
	RemoteUserID      string `json:"remote_user_id"`
	RemoteUsername    string `json:"remote_username"`
	UserID            int64  `json:"user_id,omitempty"`
	Username          string `json:"username,omitempty"`
	Created           bool   `json:"created"`
	Linked            bool   `json:"linked"`
	Link              string `json:"link,omitempty"`
	OriginSource      string `json:"origin_source,omitempty"`
	Error             string `json:"error,omitempty"`
}

// ImportAccounts turns accounts the media server lists into Cantinarr users:
// for each requested id, a new user named after the account (with a connect
// link for the admin to hand out), the instance grant, and the account linked
// to them, exactly as the admin's link picker does it. The remote changes only
// when access management is explicitly requested.
// Rows are best-effort and independent, each carrying its own outcome, so
// one failure never stops the rest; only a server that cannot list its
// accounts fails the whole call.
func (s *Service) ImportAccounts(ctx context.Context, adminID int64, instanceID, serverURL string, remoteIDs []string, manage ...bool) ([]ImportResult, error) {
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
		results = append(results, s.importOne(ctx, adminID, inst.ID, serverURL, invite, id, byID, len(manage) > 0 && manage[0]))
	}
	return results, nil
}

// importOne is one row of an import; every outcome is a result, never an
// error, so the caller's loop goes on.
func (s *Service) importOne(ctx context.Context, adminID int64, instanceID, serverURL string, invite bool, id string, byID map[string]mediaserver.RemoteUser, manage bool) ImportResult {
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
	// Selecting a remote account does not select an existing local namesake.
	// The creation transaction also refuses a concurrently created namesake.
	userID, token, err := s.userCreator.CreateImportUser(adminID, username, serverURL)
	if err != nil {
		if errors.Is(err, auth.ErrUserExists) {
			res.Error = ImportUsernameConflict
			return res
		}
		s.logger.Error("mediaaccess: import: create user", "err", err, "instance_id", instanceID)
		res.Error = ImportUserFailed
		return res
	}
	res.Created, res.Link, res.OriginSource = true, token.Link, token.OriginSource
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
	account, err := s.LinkAccount(ctx, userID, instanceID, id, manage)
	res.PlexIdentityError = account.PlexIdentityError
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
