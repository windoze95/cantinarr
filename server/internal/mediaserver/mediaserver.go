// Package mediaserver defines the provider-neutral contract Cantinarr uses to
// manage user access on a media server (Jellyfin, Emby, Plex). It is a leaf
// package: value types, sentinel errors, and the Provider interface the
// per-server clients implement. Nothing here dials anything.
package mediaserver

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"unicode"
)

// Kind is how a media server grants access.
type Kind string

const (
	// KindAccount servers (Jellyfin, Emby) hold local accounts: Cantinarr
	// creates one named after the Cantinarr user, with a password the user
	// picks, and switches it off and on with the grant.
	KindAccount Kind = "account"
	// KindInvite servers (Plex) hold no accounts of Cantinarr's. Access is a
	// share the server's owner extends to an identity the user supplies
	// (their Plex email), which the person accepts on the server's side. The
	// share is the account: revoking removes it, re-granting sends a new
	// invite the person has to accept again.
	KindInvite Kind = "invite"
)

// Kinded is implemented by providers that are not KindAccount. KindOf reads
// it; a provider that does not implement it is an account server.
type Kinded interface {
	Kind() Kind
}

// KindOf reports how a provider grants access.
func KindOf(p Provider) Kind {
	if k, ok := p.(Kinded); ok {
		return k.Kind()
	}
	return KindAccount
}

// Authenticator is implemented by account servers that can check a person's
// own username and password, so a user can prove an existing account is
// theirs. The check sends none of Cantinarr's stored credentials: the
// password travels once, is never kept, never appears in an error, and the
// session the check opens on the server is closed again before returning.
type Authenticator interface {
	// Authenticate answers the account the credentials belong to,
	// ErrBadCredentials when the server refuses the password (or knows no
	// such name), and ErrAccountRefused when the server refuses the account
	// itself (switched off, or a network or schedule rule).
	Authenticate(ctx context.Context, username, password string) (RemoteUser, error)
}

// ItemQuery names one title to look for on a media server: the media type
// ("movie" or "tv"), its provider ids, and the year and title that narrow
// the search where the server cannot filter by provider id itself.
type ItemQuery struct {
	MediaType string
	TMDBID    int64
	TVDBID    int64
	Year      int
	Title     string
}

// Item is a library item the media server holds. WebPath is the path and
// fragment of its page under the server's root (for example
// "/web/#/details?id=…&serverId=…"); the caller joins it to the admin-typed
// public address, so the client never learns the address Cantinarr dials and
// the caller never learns the server's URL scheme.
type Item struct {
	ID      string
	WebPath string
}

// ErrItemNotFound reports that the server confirmed it has no such item the
// account can see: absence, as opposed to a server that could not answer.
var ErrItemNotFound = errors.New("media server has no such item")

// ItemFinder is implemented by account servers that can look a title up by
// its provider ids. The lookup runs as the linked account, so the server
// applies that account's library access, and a match is by provider id,
// never by name alone.
type ItemFinder interface {
	FindItem(ctx context.Context, remoteUserID string, q ItemQuery) (Item, error)
}

// ItemMatches reports whether a server item's provider ids (keyed the way
// Jellyfin and Emby key them: "Tmdb", "Tvdb", in any case) name the queried
// title: the TMDB id for a movie; the TVDB id or, failing that, the TMDB id
// for a series.
func ItemMatches(providerIDs map[string]string, q ItemQuery) bool {
	var tmdb, tvdb string
	for key, value := range providerIDs {
		switch strings.ToLower(key) {
		case "tmdb":
			tmdb = strings.TrimSpace(value)
		case "tvdb":
			tvdb = strings.TrimSpace(value)
		}
	}
	if q.TMDBID > 0 && tmdb == strconv.FormatInt(q.TMDBID, 10) {
		return true
	}
	return q.MediaType == "tv" && q.TVDBID > 0 && tvdb == strconv.FormatInt(q.TVDBID, 10)
}

// SystemInfo identifies a media server; the connection test reads it.
type SystemInfo struct {
	ServerName string
	Version    string
	ID         string
}

// Library is one library the media server reports. ID is the identifier the
// server's user policy expects when restricting an account to specific
// libraries — never a filesystem path.
type Library struct {
	ID             string
	Name           string
	CollectionType string
}

// RemoteUser is the subset of a media-server account Cantinarr acts on. On an
// invite server it is a share: ID is the canonical identity the invite went
// to (CanonicalEmail), Pending reports an invite the person has not accepted
// yet, and IsDisabled is never set — a share that is gone is absence
// (ErrUserNotFound), not a disabled account.
type RemoteUser struct {
	ID              string
	Name            string
	IsAdministrator bool
	IsDisabled      bool
	Pending         bool
}

var (
	// ErrUserExists reports that an account with the requested name already
	// exists on the media server (names compare case-insensitively).
	ErrUserExists = errors.New("media server user already exists")
	// ErrUserNotFound reports that no account has the given remote id.
	ErrUserNotFound = errors.New("media server user not found")
	// ErrInvalidName reports a name the media server would refuse.
	ErrInvalidName = errors.New("name is not valid on the media server")
	// ErrBadCredentials reports a username and password the media server
	// refused: a wrong password, or no such account.
	ErrBadCredentials = errors.New("media server refused the username or password")
	// ErrAccountRefused reports an account the media server will not sign in
	// right now: switched off, or blocked by a network or schedule rule.
	ErrAccountRefused = errors.New("media server refused the account")
)

// Provider is what a media-server client must offer. Implementations keep
// hosts and credentials out of every error they return: these errors can
// reach requesters.
//
// Invite servers (KindInvite) implement the same interface with these
// meanings, chosen so the callers' self-heal paths work unchanged:
//   - CreateUser(ctx, identity, "", libraryIDs) sends the share invite to
//     identity; the password must be empty. A server that reports the
//     identity as already shared answers with the existing share rather
//     than an error — the caller's pre-check makes that a race, never the
//     normal path.
//   - GetUser(identity) answers ErrUserNotFound when there is neither an
//     accepted share nor a pending invite. Absence is absence, exactly as
//     for a deleted account.
//   - SetDisabled(ctx, identity, true) removes the share or cancels the
//     pending invite; SetDisabled(ctx, identity, false) is a no-op — access
//     comes back as a new invite, which the caller sends with CreateUser.
//   - Users lists accepted shares and pending invites; every ID is the
//     canonical identity and matching is case-insensitive.
//   - SetLibraries re-scopes an existing share and does nothing for an
//     identity that has none.
//   - DeleteUser is SetDisabled(true).
type Provider interface {
	SystemInfo(ctx context.Context) (SystemInfo, error)
	Libraries(ctx context.Context) ([]Library, error)
	Users(ctx context.Context) ([]RemoteUser, error)
	GetUser(ctx context.Context, remoteID string) (RemoteUser, error)
	// CreateUser validates the name (ErrInvalidName), refuses a name that is
	// already taken (ErrUserExists), creates the account with the password,
	// and restricts it to libraryIDs (empty = every library) as a
	// non-administrator. When any step after creation fails the half-made
	// account is deleted, so no unrestricted or passwordless account is left
	// behind.
	CreateUser(ctx context.Context, name, password string, libraryIDs []string) (RemoteUser, error)
	SetLibraries(ctx context.Context, remoteID string, libraryIDs []string) error
	SetDisabled(ctx context.Context, remoteID string, disabled bool) error
	// DeleteUser exists for rollback only: Cantinarr never deletes an account
	// it did not just create.
	DeleteUser(ctx context.Context, remoteID string) error
}

// ValidUsername mirrors Jellyfin's rule for account names
// (^(?!\s)[\w\ \-'._@+]+(?<!\s)$ with .NET's Unicode \w): at least one
// character, no leading or trailing whitespace, and every rune a letter,
// digit, combining mark, connector punctuation, or one of space - ' . _ @ +.
// Cantinarr usernames are validated only for emptiness, so this is checked
// before any account is created rather than discovered as a remote 400. Emby's
// own rule is closed-source; the last open-source one refused only < and >,
// so this mirror is the stricter of the two and never admits a name Emby
// would refuse on that rule.
func ValidUsername(name string) bool {
	if name == "" {
		return false
	}
	runes := []rune(name)
	if unicode.IsSpace(runes[0]) || unicode.IsSpace(runes[len(runes)-1]) {
		return false
	}
	for _, r := range runes {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), unicode.Is(unicode.Mn, r), unicode.Is(unicode.Pc, r):
		case r == ' ', r == '-', r == '\'', r == '.', r == '_', r == '@', r == '+':
		default:
			return false
		}
	}
	return true
}

// ValidEmail is the shape check for an invite identity, not RFC validation:
// something@something with no whitespace, short enough for a users-table
// column. The server's own answer is the real validation; this only keeps
// obvious typos from becoming an invite nobody receives.
func ValidEmail(email string) bool {
	if email == "" || len(email) > 254 || strings.ContainsAny(email, " \t\r\n") {
		return false
	}
	at := strings.Index(email, "@")
	return at > 0 && at < len(email)-1
}

// CanonicalEmail is the one spelling an invite identity is stored and
// compared under: trimmed and lower-cased, so the same address typed twice
// with different capitals is one share, not two.
func CanonicalEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
