// Package mediaaccess provisions and tracks user access on media servers
// (Jellyfin, Emby, Plex). Eligibility is the instance grant: a granted user
// creates their own account — or, on an invite server, asks for their share
// — a revoked grant switches the access off, and a returning grant switches
// it back on. Cantinarr never stores the password it hands an account server
// and never deletes an account it did not just create.
package mediaaccess

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
	"github.com/windoze95/cantinarr-server/internal/plex"
)

// ProviderFactory builds the client for a media-server instance. In
// production it is instance.NewMediaServerProvider.
type ProviderFactory func(inst *instance.Instance) (mediaserver.Provider, error)

// Notifier is the WS+push fan-out (the push.Composite). Event types are the
// push package's category strings, passed as literals so this package stays
// free of the push dependency.
type Notifier interface {
	NotifyUser(userID int64, eventType string, data map[string]interface{})
	NotifyAdmins(eventType string, data map[string]interface{})
}

const (
	// eventInviteSent tells a user their invite went out (push category
	// plex_invite_sent: "check your email").
	eventInviteSent = "plex_invite_sent"
	// eventAccessRequest tells admins a user shared a Plex email (push
	// category plex_access_request); invite_state says whether anything is
	// left for them to do.
	eventAccessRequest = "plex_access_request"
)

var (
	// ErrNotAvailable is the one answer for every "not for you" case —
	// unknown instance, not a media server, no grant — so the endpoint is
	// never an existence oracle.
	ErrNotAvailable        = errors.New("media server is not available to this user")
	ErrAccountExists       = errors.New("user already has an account on this media server")
	ErrNameTaken           = errors.New("account name is already taken on the media server")
	ErrInvalidName         = errors.New("username is not a valid media server account name")
	ErrInvalidEmail        = errors.New("email is not a valid invite address")
	ErrWrongKind           = errors.New("that is not how this media server grants access")
	ErrConfigInvalid       = errors.New("media server configuration is unreadable; re-save the instance")
	ErrInstanceNotFound    = errors.New("instance not found")
	ErrNotMediaServer      = errors.New("not a media server instance")
	ErrUserNotFound        = errors.New("user not found")
	ErrRemoteUserNotFound  = errors.New("remote user not found")
	ErrRemoteAlreadyLinked = errors.New("remote account is already linked to another user")
	ErrNoAccount           = errors.New("no linked account")
	// ErrBadCredentials and ErrAccountRefused are the media server's answers
	// to a person's own username and password: wrong (or no such name), and
	// an account it will not sign in right now.
	ErrBadCredentials = errors.New("media server refused the username or password")
	ErrAccountRefused = errors.New("media server refused the account")
	// ErrPinNotFound is a Plex sign-in nobody began, one that expired, or
	// one that belongs to another user; all three read the same.
	ErrPinNotFound = errors.New("plex sign-in not found or expired")
	// ErrUpstream wraps a media-server failure. The wrapped text is host-free
	// by the Provider contract; handlers still answer with fixed bodies.
	ErrUpstream = errors.New("media server request failed")
)

const (
	verifyTimeout = 3 * time.Second
	// watchTimeout bounds one title lookup on one media server; the detail
	// page waits for the slowest of them.
	watchTimeout     = 5 * time.Second
	createTimeout    = 30 * time.Second
	reconcileTimeout = 10 * time.Second
	// driftSweepInterval paces the retry of switch-offs that could not reach
	// the media server. A pass costs nothing when nothing is drifted: the
	// candidate query is answered entirely from Cantinarr's own tables.
	driftSweepInterval = 5 * time.Minute
	// libraryPropagationBudget caps the whole re-scope pass. It runs inside
	// the admin's save, so a media server that accepts the library list and
	// then answers policy writes slowly must not hold the request open for
	// one timeout per account.
	libraryPropagationBudget = 30 * time.Second
	// inviteBudget caps one pass of the invites a grant write or a shared
	// email owes. Invites go to a hosted service the admin's request should
	// never wait on, so the pass runs off the request; the drift sweep
	// retries whatever the budget cut off.
	inviteBudget = 30 * time.Second
	// plexSignInShareBudget caps the share pass a Plex sign-in runs before
	// answering. Unlike a shared email, the person is waiting on the answer
	// (the app's read timeout is 15 seconds), so the pass is short and the
	// drift sweep sends whatever it cut off.
	plexSignInShareBudget = 8 * time.Second
	// plexTVTimeout bounds one plex.tv call.
	plexTVTimeout = 15 * time.Second
)

// Service owns the user_media_server_accounts table and every remote action.
type Service struct {
	db        *sql.DB
	store     *instance.Store
	providers ProviderFactory
	notifier  Notifier
	logger    *slog.Logger

	mu    sync.Mutex
	locks map[string]*sync.Mutex
	// sweepMu serializes drift sweeps so a slow pass cannot overlap the next
	// tick and reconcile the same user twice at once.
	sweepMu sync.Mutex
	// background runs the invite passes that must not hold a request open.
	// Tests replace it with a synchronous runner.
	background func(func())
	// signIns holds the Plex sign-ins in flight; plexBaseURL is where their
	// PIN calls go (plex.tv, or a test server).
	signIns     *plexSignIns
	plexBaseURL string
	// userCreator makes the Cantinarr users an import names; nil until main
	// wires the auth service.
	userCreator UserCreator
}

// NewService wires the service. providers is called per request: media
// server clients are stateless, and the store hands back decrypted keys.
func NewService(db *sql.DB, store *instance.Store, providers ProviderFactory, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		db: db, store: store, providers: providers, logger: logger,
		locks:       map[string]*sync.Mutex{},
		background:  func(fn func()) { go fn() },
		signIns:     newPlexSignIns(),
		plexBaseURL: plex.BaseURL,
	}
}

// SetNotifier installs the push/WS fan-out; nil keeps invites silent. Wired
// late by main because the composite needs the WebSocket hub.
func (s *Service) SetNotifier(n Notifier) {
	s.notifier = n
}

// AccountView is a user's own account on one server as the guide shows it.
type AccountView struct {
	Username string `json:"username"`
	Disabled bool   `json:"disabled"`
	// Pending is an invite the person has not accepted yet (invite servers).
	Pending bool `json:"pending"`
	// Administrator marks an account Cantinarr records and never changes: a
	// server administrator, or on Plex the server's owner.
	Administrator bool `json:"administrator"`
	// Verified is true when the server confirmed the account just now; false
	// means the answer came from Cantinarr's record because the server could
	// not be reached (blindness, said as such — never mistaken for absence).
	Verified bool `json:"verified"`
}

// ServerView is one media server a user is granted, with their account
// state. It carries the admin-typed public address and nothing else about
// the instance.
type ServerView struct {
	InstanceID  string `json:"instance_id"`
	ServiceType string `json:"service_type"`
	Name        string `json:"name"`
	// Kind says how access works here: "account" (create one with a
	// password) or "invite" (share an email, accept the invite).
	Kind          string       `json:"kind"`
	PublicAddress string       `json:"public_address"`
	Account       *AccountView `json:"account"`
	// ExistingAccount reports that the server confirmed an account named
	// like this Cantinarr user (case-insensitively, the rule the server
	// applies to a new name) that no Cantinarr user is linked to, while the
	// caller has no account here: creating one would collide, so the guide
	// leads with signing in to link it. False is "no match confirmed", which
	// covers absence and an unreachable server alike; nothing ever claims
	// absence from it. Account servers only.
	ExistingAccount bool `json:"existing_account"`
}

// CreatedAccount is what a user gets back after creating their account,
// asking for their invite, or linking an account that is already theirs.
type CreatedAccount struct {
	Username      string `json:"username"`
	PublicAddress string `json:"public_address"`
	Pending       bool   `json:"pending"`
	Administrator bool   `json:"administrator"`
}

// Account is an admin-facing row: which Cantinarr user is which remote
// account on which server.
type Account struct {
	UserID             int64     `json:"user_id"`
	InstanceID         string    `json:"instance_id"`
	InstanceName       string    `json:"instance_name"`
	ServiceType        string    `json:"service_type"`
	RemoteUserID       string    `json:"remote_user_id"`
	Username           string    `json:"username"`
	CreatedByCantinarr bool      `json:"created_by_cantinarr"`
	Disabled           bool      `json:"disabled"`
	CreatedAt          time.Time `json:"created_at"`
}

func (s *Service) lock(userID int64, instanceID string) func() {
	key := fmt.Sprintf("%d:%s", userID, instanceID)
	s.mu.Lock()
	l := s.locks[key]
	if l == nil {
		l = &sync.Mutex{}
		s.locks[key] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// grantedMediaServers returns the media-server instance ids a user holds a
// grant on, in the store's deterministic order. Grants only: a pin is never
// media-server eligibility.
func (s *Service) grantedMediaServers(userID int64) ([]string, error) {
	grants, err := s.store.ListUserGrants(userID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, serviceType := range instance.MediaServerTypes() {
		ids = append(ids, grants[serviceType]...)
	}
	return ids, nil
}

func contains(ids []string, id string) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

// kindOf reports how an instance grants access. The provider is built
// without dialing anything; an instance no client can be built for reads as
// an account server, the conservative answer.
func (s *Service) kindOf(inst *instance.Instance) mediaserver.Kind {
	provider, err := s.providers(inst)
	if err != nil {
		return mediaserver.KindAccount
	}
	return mediaserver.KindOf(provider)
}

// ListForUser lists the media servers a user is granted with their account
// on each, confirming each existing account against the live server with a
// short timeout, concurrently, so a dead server costs one wait rather than
// one per server.
func (s *Service) ListForUser(ctx context.Context, userID int64) ([]ServerView, error) {
	ids, err := s.grantedMediaServers(userID)
	if err != nil {
		return nil, err
	}
	var username string
	if err := s.db.QueryRow("SELECT username FROM users WHERE id = ?", userID).Scan(&username); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("load user: %w", err)
	}
	views := make([]ServerView, 0, len(ids))
	// A pending check is either the linked account to confirm (row set) or,
	// on an account server with no linked account, the look for one already
	// named like the user.
	type pending struct {
		index int
		inst  *instance.Instance
		row   *accountRow
	}
	var checks []pending
	for _, id := range ids {
		inst, err := s.store.Get(id)
		if err != nil {
			return nil, err
		}
		if inst == nil || !instance.IsMediaServerType(inst.ServiceType) {
			continue
		}
		row, err := s.getAccount(userID, id)
		if err != nil {
			return nil, err
		}
		kind := s.kindOf(inst)
		views = append(views, ServerView{
			InstanceID:    inst.ID,
			ServiceType:   inst.ServiceType,
			Name:          inst.Name,
			Kind:          string(kind),
			PublicAddress: inst.MediaServerConfig.PublicAddress,
		})
		switch {
		case row != nil:
			checks = append(checks, pending{index: len(views) - 1, inst: inst, row: row})
		case kind == mediaserver.KindAccount:
			checks = append(checks, pending{index: len(views) - 1, inst: inst})
		}
	}

	var wg sync.WaitGroup
	results := make([]*AccountView, len(views))
	existing := make([]bool, len(views))
	for _, check := range checks {
		wg.Add(1)
		go func(check pending) {
			defer wg.Done()
			if check.row != nil {
				results[check.index] = s.verifyAccount(ctx, check.inst, check.row)
				return
			}
			existing[check.index] = s.existingAccount(ctx, check.inst, userID, username)
		}(check)
	}
	wg.Wait()
	for i := range views {
		views[i].Account = results[i]
		views[i].ExistingAccount = existing[i]
	}
	return views, nil
}

// existingAccount reports whether the server holds an unlinked account named
// like the Cantinarr user. It reads the live account list under
// verifyTimeout; a server that cannot answer reads as no match, logged, and
// the guide keeps its plain layout: the flag is only ever a confirmed
// presence. An account another Cantinarr user is linked to is not offered,
// since signing in with it could only end in "linked to someone else".
func (s *Service) existingAccount(ctx context.Context, inst *instance.Instance, userID int64, username string) bool {
	if strings.TrimSpace(username) == "" {
		return false
	}
	provider, err := s.providers(inst)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	users, err := provider.Users(ctx)
	if err != nil {
		s.logger.Info("mediaaccess: could not check for an existing account", "err", err, "user_id", userID, "instance_id", inst.ID)
		return false
	}
	for _, u := range users {
		if !strings.EqualFold(u.Name, username) {
			continue
		}
		claimed, err := s.identityClaimed(inst.ID, u.ID, userID)
		if err != nil {
			s.logger.Warn("mediaaccess: could not check whether an account is linked", "err", err, "user_id", userID, "instance_id", inst.ID)
			return false
		}
		if !claimed {
			return true
		}
	}
	return false
}

// verifyAccount reads the live account. A confirmed 404 is definitive
// absence and reads as no account; an unreachable server falls back to the
// stored row with verified=false.
func (s *Service) verifyAccount(ctx context.Context, inst *instance.Instance, row *accountRow) *AccountView {
	stored := &AccountView{Username: row.RemoteUsername, Disabled: row.DisabledAt.Valid, Verified: false}
	provider, err := s.providers(inst)
	if err != nil {
		return stored
	}
	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	live, err := provider.GetUser(ctx, row.RemoteUserID)
	if errors.Is(err, mediaserver.ErrUserNotFound) {
		s.logger.Info("mediaaccess: linked account no longer exists on the server", "user_id", row.UserID, "instance_id", inst.ID)
		return nil
	}
	if err != nil {
		s.logger.Warn("mediaaccess: could not confirm account", "err", err, "user_id", row.UserID, "instance_id", inst.ID)
		return stored
	}
	return &AccountView{Username: live.Name, Disabled: live.IsDisabled, Pending: live.Pending, Administrator: live.IsAdministrator, Verified: true}
}

// Watch-link states: what the media server answered about one title.
const (
	// WatchFound: the server holds the title and the account can see it.
	WatchFound = "found"
	// WatchMissing: the server confirmed it has no such title the account
	// can see (absence: not imported yet, or in a library not shared).
	WatchMissing = "missing"
	// WatchUnreachable: no answer (blindness); never read as absence.
	WatchUnreachable = "unreachable"
	// WatchUnverified: no unique title match was established. This includes
	// accounts not yet linked, pending shares, and bounded/ambiguous searches.
	WatchUnverified = "unverified"
)

// WatchLink is where one title can be watched on one of the user's media
// servers, as the server answered just now. URL is the item's page at the
// admin-typed public address (Plex: hosted Plex Web), set only when found.
// FallbackURL is a generic sign-in shortcut, never evidence of availability.
type WatchLink struct {
	InstanceID  string `json:"instance_id"`
	Name        string `json:"name"`
	ServiceType string `json:"service_type"`
	State       string `json:"state"`
	URL         string `json:"url,omitempty"`
	FallbackURL string `json:"fallback_url,omitempty"`
}

// WatchLinks looks a title up on every media server the user can watch on:
// a granted server with a sign-in address. Exact lookups require a linked
// account and apply its live library access, concurrently and bounded.
// Plex also offers a generic shortcut when an exact lookup is unavailable.
// Every result states whether a title was found, absent, or not verified.
func (s *Service) WatchLinks(ctx context.Context, userID int64, q mediaserver.ItemQuery) ([]WatchLink, error) {
	ids, err := s.grantedMediaServers(userID)
	if err != nil {
		return nil, err
	}
	type target struct {
		inst   *instance.Instance
		row    *accountRow
		finder mediaserver.ItemFinder
	}
	var targets []target
	for _, id := range ids {
		inst, err := s.store.Get(id)
		if err != nil {
			return nil, err
		}
		if inst == nil || !instance.IsMediaServerType(inst.ServiceType) || inst.MediaServerConfigInvalid || inst.MediaServerConfig.PublicAddress == "" {
			continue
		}
		provider, err := s.providers(inst)
		if err != nil {
			continue
		}
		finder, ok := provider.(mediaserver.ItemFinder)
		if !ok && inst.ServiceType != "plex" {
			continue
		}
		row, err := s.getAccount(userID, id)
		if err != nil {
			return nil, err
		}
		if (row == nil || row.DisabledAt.Valid) && inst.ServiceType != "plex" {
			continue
		}
		targets = append(targets, target{inst: inst, row: row, finder: finder})
	}

	links := make([]WatchLink, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t target) {
			defer wg.Done()
			links[i] = s.watchLink(ctx, t.inst, t.row, t.finder, q)
		}(i, t)
	}
	wg.Wait()
	return links, nil
}

// watchLink is one server's answer for one title. A confirmed absence and an
// unanswered lookup are different states; only the latter is logged, with
// ids.
func (s *Service) watchLink(ctx context.Context, inst *instance.Instance, row *accountRow, finder mediaserver.ItemFinder, q mediaserver.ItemQuery) WatchLink {
	link := WatchLink{InstanceID: inst.ID, Name: inst.Name, ServiceType: inst.ServiceType}
	if inst.ServiceType == "plex" {
		link.FallbackURL = inst.MediaServerConfig.PublicAddress
	}
	if row == nil || row.DisabledAt.Valid || finder == nil {
		link.State = WatchUnverified
		return link
	}
	ctx, cancel := context.WithTimeout(ctx, watchTimeout)
	defer cancel()
	item, err := finder.FindItem(ctx, row.RemoteUserID, q)
	switch {
	case errors.Is(err, mediaserver.ErrItemUnverified):
		link.State = WatchUnverified
	case errors.Is(err, mediaserver.ErrItemNotFound):
		link.State = WatchMissing
	case err != nil:
		s.logger.Info("mediaaccess: could not look a title up on the media server", "err", err, "user_id", row.UserID, "instance_id", inst.ID)
		link.State = WatchUnreachable
	default:
		link.State = WatchFound
		base := inst.MediaServerConfig.PublicAddress
		if inst.ServiceType == "plex" {
			base = instance.PlexPublicAddress
		}
		link.URL = strings.TrimRight(base, "/") + item.WebPath
		link.FallbackURL = ""
	}
	return link
}

// eligibleProvider is the prologue of every self-service write: the instance
// must exist, be a media server, be granted to the user, and have a readable
// config. Every refusal but the unreadable config is ErrNotAvailable, so a
// caller learns nothing about instances they are not granted.
func (s *Service) eligibleProvider(userID int64, instanceID string) (*instance.Instance, mediaserver.Provider, error) {
	inst, err := s.store.Get(instanceID)
	if err != nil {
		return nil, nil, err
	}
	if inst == nil || !instance.IsMediaServerType(inst.ServiceType) {
		return nil, nil, ErrNotAvailable
	}
	granted, err := s.grantedMediaServers(userID)
	if err != nil {
		return nil, nil, err
	}
	if !contains(granted, instanceID) {
		return nil, nil, ErrNotAvailable
	}
	if inst.MediaServerConfigInvalid {
		return nil, nil, ErrConfigInvalid
	}
	provider, err := s.providers(inst)
	if err != nil {
		return nil, nil, ErrNotAvailable
	}
	return inst, provider, nil
}

// CreateAccount creates the caller's account on a granted account server,
// named after their Cantinarr username, restricted to the shared libraries.
// The password is handed to the server once and never kept.
func (s *Service) CreateAccount(ctx context.Context, userID int64, instanceID, password string) (CreatedAccount, error) {
	unlock := s.lock(userID, instanceID)
	defer unlock()
	ctx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()

	inst, provider, err := s.eligibleProvider(userID, instanceID)
	if err != nil {
		return CreatedAccount{}, err
	}
	if mediaserver.KindOf(provider) != mediaserver.KindAccount {
		return CreatedAccount{}, ErrWrongKind
	}

	if row, err := s.getAccount(userID, instanceID); err != nil {
		return CreatedAccount{}, err
	} else if row != nil {
		// A row is a claim, not proof: confirm it before refusing. A server
		// that no longer has the account lets the user create a fresh one.
		_, liveErr := provider.GetUser(ctx, row.RemoteUserID)
		switch {
		case liveErr == nil:
			return CreatedAccount{}, ErrAccountExists
		case errors.Is(liveErr, mediaserver.ErrUserNotFound):
			if _, err := s.deleteAccount(userID, instanceID); err != nil {
				return CreatedAccount{}, err
			}
		default:
			return CreatedAccount{}, fmt.Errorf("%w: %v", ErrUpstream, liveErr)
		}
	}

	var username string
	if err := s.db.QueryRow("SELECT username FROM users WHERE id = ?", userID).Scan(&username); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CreatedAccount{}, ErrUserNotFound
		}
		return CreatedAccount{}, fmt.Errorf("load user: %w", err)
	}

	remote, err := provider.CreateUser(ctx, username, password, inst.MediaServerConfig.LibraryIDs)
	switch {
	case errors.Is(err, mediaserver.ErrInvalidName):
		return CreatedAccount{}, ErrInvalidName
	case errors.Is(err, mediaserver.ErrUserExists):
		return CreatedAccount{}, ErrNameTaken
	case err != nil:
		return CreatedAccount{}, fmt.Errorf("%w: %v", ErrUpstream, err)
	}

	inserted, err := s.insertAccount(accountRow{
		UserID: userID, InstanceID: instanceID,
		RemoteUserID: remote.ID, RemoteUsername: remote.Name, CreatedByCantinarr: true,
	}, true)
	if err != nil || !inserted {
		// The grant vanished, the instance was deleted, or a concurrent link
		// claimed the slot while the server was creating the account. Undo
		// the remote side so nothing unrestricted survives.
		s.rollbackCreate(ctx, provider, remote.ID, userID, instanceID)
		switch {
		case errors.Is(err, errAccountConflict), errors.Is(err, errRemoteConflict):
			return CreatedAccount{}, ErrAccountExists
		case err != nil && !errors.Is(err, errRowReference):
			return CreatedAccount{}, err
		}
		return CreatedAccount{}, ErrNotAvailable
	}
	return CreatedAccount{Username: remote.Name, PublicAddress: inst.MediaServerConfig.PublicAddress}, nil
}

// RequestInvite is CreateAccount for an invite server: it records the email
// the caller wants their share sent to and sends the invite. An address that
// already has a share (someone shared it by hand) is adopted as a linked
// account instead of invited again; an address another Cantinarr user holds
// is refused. A new address replaces the share Cantinarr sent to the old one.
func (s *Service) RequestInvite(ctx context.Context, userID int64, instanceID, email string) (CreatedAccount, error) {
	unlock := s.lock(userID, instanceID)
	defer unlock()
	ctx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()

	inst, provider, err := s.eligibleProvider(userID, instanceID)
	if err != nil {
		return CreatedAccount{}, err
	}
	if mediaserver.KindOf(provider) != mediaserver.KindInvite {
		return CreatedAccount{}, ErrWrongKind
	}
	email = mediaserver.CanonicalEmail(email)
	if !mediaserver.ValidEmail(email) {
		return CreatedAccount{}, ErrInvalidEmail
	}
	// An address another user's row holds is refused before anything else
	// moves: the caller's own share and row stay exactly as they were.
	claimed, err := s.identityClaimed(instanceID, email, userID)
	if err != nil {
		return CreatedAccount{}, err
	}
	if claimed {
		return CreatedAccount{}, ErrNameTaken
	}

	if row, err := s.getAccount(userID, instanceID); err != nil {
		return CreatedAccount{}, err
	} else if row != nil {
		if row.RemoteUserID == email {
			// Same address: the row is a claim, the share is the proof.
			_, liveErr := provider.GetUser(ctx, email)
			switch {
			case liveErr == nil:
				return CreatedAccount{}, ErrAccountExists
			case errors.Is(liveErr, mediaserver.ErrUserNotFound):
				if _, err := s.deleteAccount(userID, instanceID); err != nil {
					return CreatedAccount{}, err
				}
			default:
				return CreatedAccount{}, fmt.Errorf("%w: %v", ErrUpstream, liveErr)
			}
		} else {
			// A new address. The share Cantinarr sent goes with it; one an
			// admin linked is the admin's to unlink first.
			if !row.CreatedByCantinarr {
				return CreatedAccount{}, ErrAccountExists
			}
			if err := provider.SetDisabled(ctx, row.RemoteUserID, true); err != nil && !errors.Is(err, mediaserver.ErrUserNotFound) {
				return CreatedAccount{}, fmt.Errorf("%w: %v", ErrUpstream, err)
			}
			if _, err := s.deleteAccount(userID, instanceID); err != nil {
				return CreatedAccount{}, err
			}
		}
	}

	if err := s.rememberEmail(userID, email); err != nil {
		return CreatedAccount{}, err
	}

	remote, err := provider.GetUser(ctx, email)
	created := false
	switch {
	case err == nil:
		// Shared by hand already: adopt it, send nothing.
	case errors.Is(err, mediaserver.ErrUserNotFound):
		remote, err = provider.CreateUser(ctx, email, "", inst.MediaServerConfig.LibraryIDs)
		switch {
		case errors.Is(err, mediaserver.ErrInvalidName):
			return CreatedAccount{}, ErrInvalidEmail
		case errors.Is(err, mediaserver.ErrUserExists):
			return CreatedAccount{}, ErrNameTaken
		case err != nil:
			return CreatedAccount{}, fmt.Errorf("%w: %v", ErrUpstream, err)
		}
		created = true
	default:
		return CreatedAccount{}, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	name := remote.Name
	if name == "" {
		name = email
	}

	inserted, err := s.insertAccount(accountRow{
		UserID: userID, InstanceID: instanceID,
		RemoteUserID: email, RemoteUsername: name, CreatedByCantinarr: created,
	}, true)
	if err != nil || !inserted {
		// A conflict means another row owns this share — it is theirs, not
		// ours to remove. Only a grant or instance that vanished under a
		// fresh invite rolls it back.
		if created && !errors.Is(err, errAccountConflict) && !errors.Is(err, errRemoteConflict) {
			s.rollbackCreate(ctx, provider, email, userID, instanceID)
		}
		switch {
		case errors.Is(err, errAccountConflict), errors.Is(err, errRemoteConflict):
			return CreatedAccount{}, ErrAccountExists
		case err != nil && !errors.Is(err, errRowReference):
			return CreatedAccount{}, err
		}
		return CreatedAccount{}, ErrNotAvailable
	}
	// "Check your email" only when there is an email to check: plex.tv
	// accepts a share at once for an account still connected to the owner.
	if created && remote.Pending {
		s.notifyUser(userID, eventInviteSent)
	}
	return CreatedAccount{Username: name, PublicAddress: inst.MediaServerConfig.PublicAddress, Pending: remote.Pending}, nil
}

// LinkOwnAccount links an existing account on a granted account server to
// the caller, on proof that it is theirs: the server checks the username
// and password, which travel once and are never kept. An administrator
// account is accepted as it is (recorded, never changed). The row check runs
// before the credentials are sent, so a request that will be refused moves
// no lockout counter on the server.
func (s *Service) LinkOwnAccount(ctx context.Context, userID int64, instanceID, username, password string) (CreatedAccount, error) {
	unlock := s.lock(userID, instanceID)
	defer unlock()
	ctx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()

	inst, provider, err := s.eligibleProvider(userID, instanceID)
	if err != nil {
		return CreatedAccount{}, err
	}
	authenticator, ok := provider.(mediaserver.Authenticator)
	if !ok || mediaserver.KindOf(provider) != mediaserver.KindAccount {
		return CreatedAccount{}, ErrWrongKind
	}

	if row, err := s.getAccount(userID, instanceID); err != nil {
		return CreatedAccount{}, err
	} else if row != nil {
		// A row is a claim, not proof: confirm it before refusing. A server
		// that no longer has that account lets the user link another.
		_, liveErr := provider.GetUser(ctx, row.RemoteUserID)
		switch {
		case liveErr == nil:
			return CreatedAccount{}, ErrAccountExists
		case errors.Is(liveErr, mediaserver.ErrUserNotFound):
			if _, err := s.deleteAccount(userID, instanceID); err != nil {
				return CreatedAccount{}, err
			}
		default:
			return CreatedAccount{}, fmt.Errorf("%w: %v", ErrUpstream, liveErr)
		}
	}

	remote, err := authenticator.Authenticate(ctx, username, password)
	switch {
	case errors.Is(err, mediaserver.ErrBadCredentials):
		s.logger.Info("mediaaccess: link own account: the server refused the password", "user_id", userID, "instance_id", instanceID)
		return CreatedAccount{}, ErrBadCredentials
	case errors.Is(err, mediaserver.ErrAccountRefused):
		s.logger.Info("mediaaccess: link own account: the server refused the account", "user_id", userID, "instance_id", instanceID)
		return CreatedAccount{}, ErrAccountRefused
	case err != nil:
		return CreatedAccount{}, fmt.Errorf("%w: %v", ErrUpstream, err)
	case remote.ID == "":
		return CreatedAccount{}, fmt.Errorf("%w: the sign-in check answered no account id", ErrUpstream)
	}
	claimed, err := s.identityClaimed(instanceID, remote.ID, userID)
	if err != nil {
		return CreatedAccount{}, err
	}
	if claimed {
		return CreatedAccount{}, ErrRemoteAlreadyLinked
	}
	name := remote.Name
	if name == "" {
		name = username
	}

	inserted, err := s.insertAccount(accountRow{
		UserID: userID, InstanceID: instanceID,
		RemoteUserID: remote.ID, RemoteUsername: name, CreatedByCantinarr: false,
	}, true)
	switch {
	case errors.Is(err, errAccountConflict):
		return CreatedAccount{}, ErrAccountExists
	case errors.Is(err, errRemoteConflict):
		return CreatedAccount{}, ErrRemoteAlreadyLinked
	case errors.Is(err, errRowReference):
		return CreatedAccount{}, ErrNotAvailable
	case err != nil:
		return CreatedAccount{}, err
	case !inserted:
		// The grant went away while the server was checking the password.
		return CreatedAccount{}, ErrNotAvailable
	}
	return CreatedAccount{Username: name, PublicAddress: inst.MediaServerConfig.PublicAddress, Administrator: remote.IsAdministrator}, nil
}

func (s *Service) rollbackCreate(ctx context.Context, provider mediaserver.Provider, remoteID string, userID int64, instanceID string) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reconcileTimeout)
	defer cancel()
	if err := provider.DeleteUser(cleanupCtx, remoteID); err != nil {
		s.logger.Error("mediaaccess: could not roll back a half-created account", "err", err, "user_id", userID, "instance_id", instanceID)
	}
}

func (s *Service) notifyUser(userID int64, eventType string) {
	if s.notifier == nil {
		return
	}
	s.notifier.NotifyUser(userID, eventType, map[string]interface{}{})
}

// ListAccounts returns every linked account for the admin Users screen.
func (s *Service) ListAccounts() ([]Account, error) {
	return s.listAccounts()
}

func (s *Service) mediaServerInstance(instanceID string) (*instance.Instance, error) {
	inst, err := s.store.Get(instanceID)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		return nil, ErrInstanceNotFound
	}
	if !instance.IsMediaServerType(inst.ServiceType) {
		return nil, ErrNotMediaServer
	}
	return inst, nil
}

// RemoteUsers lists the accounts on a media server, for the admin link picker.
func (s *Service) RemoteUsers(ctx context.Context, instanceID string) ([]mediaserver.RemoteUser, error) {
	inst, err := s.mediaServerInstance(instanceID)
	if err != nil {
		return nil, err
	}
	provider, err := s.providers(inst)
	if err != nil {
		return nil, ErrNotMediaServer
	}
	ctx, cancel := context.WithTimeout(ctx, reconcileTimeout)
	defer cancel()
	users, err := provider.Users(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	if users == nil {
		users = []mediaserver.RemoteUser{}
	}
	return users, nil
}

// LinkAccount records that a Cantinarr user is an existing remote account,
// grants them the instance if they lack it, and brings the account's
// disabled state in line with that grant. The account's libraries are left
// exactly as the admin configured them on the server.
func (s *Service) LinkAccount(ctx context.Context, userID int64, instanceID, remoteUserID string) (Account, error) {
	unlock := s.lock(userID, instanceID)
	defer unlock()
	ctx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()

	inst, err := s.mediaServerInstance(instanceID)
	if err != nil {
		return Account{}, err
	}
	var exists int
	if err := s.db.QueryRow("SELECT 1 FROM users WHERE id = ?", userID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Account{}, ErrUserNotFound
		}
		return Account{}, fmt.Errorf("load user: %w", err)
	}
	provider, err := s.providers(inst)
	if err != nil {
		return Account{}, ErrNotMediaServer
	}
	if mediaserver.KindOf(provider) == mediaserver.KindInvite {
		remoteUserID = mediaserver.CanonicalEmail(remoteUserID)
	}
	// An administrator account links like any other: recorded, and from
	// then on left alone by every path that changes accounts.
	remote, err := provider.GetUser(ctx, remoteUserID)
	switch {
	case errors.Is(err, mediaserver.ErrUserNotFound):
		return Account{}, ErrRemoteUserNotFound
	case err != nil:
		return Account{}, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	remoteID := remote.ID
	if remoteID == "" {
		remoteID = remoteUserID
	}
	name := remote.Name
	if name == "" {
		name = remoteID
	}

	_, err = s.insertAccount(accountRow{
		UserID: userID, InstanceID: instanceID,
		RemoteUserID: remoteID, RemoteUsername: name, CreatedByCantinarr: false,
	}, false)
	switch {
	case errors.Is(err, errAccountConflict):
		return Account{}, ErrAccountExists
	case errors.Is(err, errRemoteConflict):
		return Account{}, ErrRemoteAlreadyLinked
	case errors.Is(err, errRowReference):
		return Account{}, ErrInstanceNotFound
	case err != nil:
		return Account{}, err
	}

	grants, err := s.store.ListUserGrants(userID)
	if err != nil {
		return Account{}, err
	}
	if !contains(grants[inst.ServiceType], instanceID) {
		if err := s.store.SetUserGrants(userID, map[string][]string{
			inst.ServiceType: append(grants[inst.ServiceType], instanceID),
		}); err != nil {
			return Account{}, err
		}
	}
	s.reconcileUser(ctx, userID)

	accounts, err := s.listAccounts()
	if err != nil {
		return Account{}, err
	}
	for _, a := range accounts {
		if a.UserID == userID && a.InstanceID == instanceID {
			return a, nil
		}
	}
	return Account{}, ErrNoAccount
}

// UnlinkAccount forgets the row. The remote account and the grant stay as
// they are: unlinking is "stop managing this", not revocation.
func (s *Service) UnlinkAccount(userID int64, instanceID string) error {
	deleted, err := s.deleteAccount(userID, instanceID)
	if err != nil {
		return err
	}
	if !deleted {
		return ErrNoAccount
	}
	return nil
}

// OnGrantsChanged is the instance handler's grant observer: every affected
// user's accounts are reconciled against their grants, and the invites a
// returning or new grant owes go out off the request.
func (s *Service) OnGrantsChanged(userIDs []int64) {
	for _, userID := range userIDs {
		ctx, cancel := context.WithTimeout(context.Background(), reconcileTimeout)
		s.reconcileUser(ctx, userID)
		cancel()
	}
	ids := append([]int64(nil), userIDs...)
	s.background(func() {
		ctx, cancel := context.WithTimeout(context.Background(), inviteBudget)
		defer cancel()
		for _, userID := range ids {
			if ctx.Err() != nil {
				s.logger.Error("mediaaccess: invites owed to a grant change ran out of time; the drift sweep retries them")
				return
			}
			s.inviteGranted(ctx, userID)
		}
	})
}

// OnPlexEmailShared is the auth handler's access-request hook, run after a
// user shares a new or changed Plex email. Off the request: the share
// Cantinarr sent to a previous address is removed, every invite server that
// auto-approves grants the user, the invites they are owed go out, and
// admins hear about it with the outcome, so the push says whether anything
// is left for them to do.
func (s *Service) OnPlexEmailShared(userID int64, username string) {
	s.background(func() {
		ctx, cancel := context.WithTimeout(context.Background(), inviteBudget)
		defer cancel()
		s.handleEmailShared(ctx, userID, username)
	})
}

// emailSharedOutcome is what one shared email led to.
type emailSharedOutcome struct {
	granted int // invite servers the user holds a grant on
	invites inviteOutcome
}

// adminState is the push's invite_state: whether anything is left for an
// admin to do. Empty means nobody has granted this user an invite server;
// "claimed" means the address belongs to another user's row, which only an
// admin can untangle (the push reads it as waiting on them).
func (o emailSharedOutcome) adminState() string {
	switch {
	case o.invites.failed > 0:
		return "failed"
	case o.invites.sent > 0 || o.invites.adopted > 0:
		return "sent"
	case o.invites.claimed > 0:
		return "claimed"
	case o.granted > 0:
		return "sent"
	}
	return ""
}

// userState is what the person who signed in is told: an invite to accept
// ("sent"), access already there ("adopted"), an invite that could not go
// out yet ("failed"), an address that belongs to another user's row
// ("claimed"), or nothing until an admin grants them ("").
func (o emailSharedOutcome) userState() string {
	switch {
	case o.invites.failed > 0:
		return "failed"
	case o.invites.sent > 0:
		return "sent"
	case o.invites.adopted > 0:
		return "adopted"
	case o.invites.claimed > 0:
		return "claimed"
	case o.granted > 0:
		return "adopted"
	}
	return ""
}

// handleEmailShared is OnPlexEmailShared's synchronous body.
func (s *Service) handleEmailShared(ctx context.Context, userID int64, username string) emailSharedOutcome {
	var out emailSharedOutcome
	email, err := s.plexEmail(userID)
	if err != nil {
		s.logger.Error("mediaaccess: email shared: load user", "err", err, "user_id", userID)
		return out
	}
	if email == "" {
		return out
	}
	s.dropSharesToOtherAddresses(ctx, userID, email)
	if err := s.autoApprove(userID); err != nil {
		s.logger.Error("mediaaccess: email shared: auto-approve", "err", err, "user_id", userID)
	}
	granted, err := s.grantedInviteServers(userID)
	if err != nil {
		s.logger.Error("mediaaccess: email shared: list grants", "err", err, "user_id", userID)
		return out
	}
	out.granted = len(granted)
	out.invites = s.inviteGranted(ctx, userID)
	if s.notifier != nil {
		s.notifier.NotifyAdmins(eventAccessRequest, map[string]interface{}{
			"user_id":      userID,
			"username":     username,
			"invite_state": out.adminState(),
		})
	}
	return out
}

// dropSharesToOtherAddresses removes the shares Cantinarr sent to an address
// the user has moved away from. Shares an admin linked are left alone: the
// admin decided that identity, and unlinking it is theirs to do.
func (s *Service) dropSharesToOtherAddresses(ctx context.Context, userID int64, email string) {
	rows, err := s.listAccountsForUser(userID)
	if err != nil {
		s.logger.Error("mediaaccess: email shared: list accounts", "err", err, "user_id", userID)
		return
	}
	for _, row := range rows {
		if row.RemoteUserID == email || !row.CreatedByCantinarr {
			continue
		}
		inst, err := s.store.Get(row.InstanceID)
		if err != nil || inst == nil {
			continue
		}
		provider, err := s.providers(inst)
		if err != nil || mediaserver.KindOf(provider) != mediaserver.KindInvite {
			continue
		}
		unlock := s.lock(userID, row.InstanceID)
		if err := provider.SetDisabled(ctx, row.RemoteUserID, true); err != nil && !errors.Is(err, mediaserver.ErrUserNotFound) {
			s.logger.Error("mediaaccess: email shared: remove share to previous address", "err", err, "user_id", userID, "instance_id", row.InstanceID)
			unlock()
			continue
		}
		if _, err := s.deleteAccount(userID, row.InstanceID); err != nil {
			s.logger.Error("mediaaccess: email shared: forget previous address", "err", err, "user_id", userID, "instance_id", row.InstanceID)
		}
		unlock()
	}
}

// autoApprove grants the user every invite server whose admin switched
// auto-approve on. The grant is what makes the invite go out; nothing here
// dials the server.
func (s *Service) autoApprove(userID int64) error {
	instances, err := s.store.ListAll()
	if err != nil {
		return err
	}
	grants, err := s.store.ListUserGrants(userID)
	if err != nil {
		return err
	}
	added := map[string][]string{}
	for i := range instances {
		inst := &instances[i]
		if !instance.IsMediaServerType(inst.ServiceType) || !inst.MediaServerConfig.AutoApprove || inst.MediaServerConfigInvalid {
			continue
		}
		if s.kindOf(inst) != mediaserver.KindInvite || contains(grants[inst.ServiceType], inst.ID) {
			continue
		}
		added[inst.ServiceType] = append(added[inst.ServiceType], inst.ID)
	}
	if len(added) == 0 {
		return nil
	}
	for serviceType, ids := range added {
		added[serviceType] = append(append([]string(nil), grants[serviceType]...), ids...)
	}
	if err := s.store.SetUserGrants(userID, added); err != nil {
		return err
	}
	s.logger.Info("mediaaccess: auto-approved a shared email", "user_id", userID, "instances", len(added))
	return nil
}

// grantedInviteServers returns the invite-kind instances a user is granted.
func (s *Service) grantedInviteServers(userID int64) ([]*instance.Instance, error) {
	ids, err := s.grantedMediaServers(userID)
	if err != nil {
		return nil, err
	}
	var out []*instance.Instance
	for _, id := range ids {
		inst, err := s.store.Get(id)
		if err != nil {
			return nil, err
		}
		if inst == nil || inst.MediaServerConfigInvalid || s.kindOf(inst) != mediaserver.KindInvite {
			continue
		}
		out = append(out, inst)
	}
	return out, nil
}

// inviteOutcome counts one pass of inviteGranted. claimed counts the servers
// where the user's address belongs to another user's row: nothing can be
// sent, and the person needs an admin.
type inviteOutcome struct {
	sent, adopted, failed, claimed int
}

// inviteGranted sends the invites a user is owed: one per granted invite
// server where they have shared an email and hold no share yet. Each goes
// through RequestInvite, so the same pre-checks, lock, and rollback apply as
// when the user asks themselves. A user who never shared an email is owed
// nothing; the guide asks them for it.
func (s *Service) inviteGranted(ctx context.Context, userID int64) inviteOutcome {
	var out inviteOutcome
	email, err := s.plexEmail(userID)
	if err != nil {
		s.logger.Error("mediaaccess: invite: load user", "err", err, "user_id", userID)
		return out
	}
	if email == "" {
		return out
	}
	servers, err := s.grantedInviteServers(userID)
	if err != nil {
		s.logger.Error("mediaaccess: invite: list grants", "err", err, "user_id", userID)
		return out
	}
	for _, inst := range servers {
		if ctx.Err() != nil {
			return out
		}
		row, err := s.getAccount(userID, inst.ID)
		if err != nil || row != nil {
			continue
		}
		created, err := s.RequestInvite(ctx, userID, inst.ID, email)
		switch {
		case err == nil && created.Pending:
			out.sent++
		case err == nil:
			out.adopted++
		case errors.Is(err, ErrNameTaken):
			// The address belongs to someone else's row: nothing to send,
			// and only an admin can sort out whose it is.
			out.claimed++
			s.logger.Info("mediaaccess: invite: nothing to send", "reason", err, "user_id", userID, "instance_id", inst.ID)
		case errors.Is(err, ErrAccountExists), errors.Is(err, ErrNotAvailable):
			// Nothing owed here: the share exists, or the grant went away
			// meanwhile.
			s.logger.Info("mediaaccess: invite: nothing to send", "reason", err, "user_id", userID, "instance_id", inst.ID)
		default:
			out.failed++
			s.logger.Error("mediaaccess: invite: send", "err", err, "user_id", userID, "instance_id", inst.ID)
		}
	}
	return out
}

// SweepAccountDrift retries the account switch-offs and switch-ons that never
// reached the media server, and the invites a grant still owes. A grant
// write reconciles synchronously and, by design, does not fail when the
// server is down — which used to mean a grant revoked during an outage was
// applied to Cantinarr and never to the server, leaving the account
// signed-in-able forever with only a WARN line to say so. Each pass
// re-derives the intent from the grants (the account rows whose disabled
// stamp disagrees, and the granted users with an email and no row) and acts
// on those users, so the write lands as soon as the server is reachable
// again.
func (s *Service) SweepAccountDrift(ctx context.Context) {
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()

	userIDs, err := s.listDriftedAccountUsers()
	if err != nil {
		s.logger.Error("mediaaccess: drift sweep: list candidates", "err", err)
		return
	}
	if len(userIDs) > 0 {
		s.logger.Info("mediaaccess: retrying media server account changes that did not land", "users", len(userIDs))
	}
	for _, userID := range userIDs {
		if ctx.Err() != nil {
			return
		}
		passCtx, cancel := context.WithTimeout(ctx, reconcileTimeout)
		s.reconcileUser(passCtx, userID)
		cancel()
	}

	owed, err := s.listUninvitedGrantedUsers()
	if err != nil {
		s.logger.Error("mediaaccess: drift sweep: list owed invites", "err", err)
		return
	}
	for _, userID := range owed {
		if ctx.Err() != nil {
			return
		}
		passCtx, cancel := context.WithTimeout(ctx, inviteBudget)
		s.inviteGranted(passCtx, userID)
		cancel()
	}
}

// StartAccountMaintenance sweeps once now — a switch-off can be owed from
// before this process started — and then on a fixed cadence until ctx ends.
func (s *Service) StartAccountMaintenance(ctx context.Context) {
	go func() {
		s.SweepAccountDrift(ctx)
		ticker := time.NewTicker(driftSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.SweepAccountDrift(ctx)
			}
		}
	}()
}

// OnSharedLibrariesChanged is the instance handler's shared-libraries
// observer: it re-applies the instance's library selection to the accounts
// Cantinarr created there, so unticking a library actually takes it away from
// the people who already have accounts instead of only from future ones.
// Accounts an admin linked are left alone — Cantinarr never edits a policy it
// did not write — and so is every other server. Failures are logged and the
// next save retries them; the admin has just read this server's library list
// to reach this screen, so a server that answers the list and then refuses
// the write is the narrow case.
func (s *Service) OnSharedLibrariesChanged(instanceID string, libraryIDs []string) {
	inst, err := s.mediaServerInstance(instanceID)
	if err != nil {
		s.logger.Error("mediaaccess: shared libraries changed: load instance", "err", err, "instance_id", instanceID)
		return
	}
	rows, err := s.listAccountsCreatedOn(instanceID)
	if err != nil {
		s.logger.Error("mediaaccess: shared libraries changed: list accounts", "err", err, "instance_id", instanceID)
		return
	}
	if len(rows) == 0 {
		return
	}
	provider, err := s.providers(inst)
	if err != nil {
		s.logger.Error("mediaaccess: shared libraries changed: build client", "err", err, "instance_id", instanceID)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), libraryPropagationBudget)
	defer cancel()
	for i, row := range rows {
		if ctx.Err() != nil {
			s.logger.Error("mediaaccess: shared libraries changed: out of time",
				"instance_id", instanceID, "not_rescoped", len(rows)-i, "of", len(rows))
			return
		}
		if err := provider.SetLibraries(ctx, row.RemoteUserID, libraryIDs); err != nil {
			s.logger.Error("mediaaccess: shared libraries changed: re-scope account",
				"err", err, "user_id", row.UserID, "instance_id", instanceID)
		}
	}
}

// reconcileUser makes each of a user's linked accounts enabled exactly when
// the user holds the instance's grant. It compares against the LIVE state,
// not the row's stamp, so an account an admin re-enabled or disabled on the
// server side converges too. Failures are logged (ids only) and skipped: a
// grant write must never fail because a media server is down.
func (s *Service) reconcileUser(ctx context.Context, userID int64) {
	rows, err := s.listAccountsForUser(userID)
	if err != nil {
		s.logger.Error("mediaaccess: reconcile: list accounts", "err", err, "user_id", userID)
		return
	}
	if len(rows) == 0 {
		return
	}
	granted, err := s.grantedMediaServers(userID)
	if err != nil {
		s.logger.Error("mediaaccess: reconcile: list grants", "err", err, "user_id", userID)
		return
	}
	for _, row := range rows {
		inst, err := s.store.Get(row.InstanceID)
		if err != nil || inst == nil {
			continue
		}
		provider, err := s.providers(inst)
		if err != nil {
			continue
		}
		wantDisabled := !contains(granted, row.InstanceID)
		live, err := provider.GetUser(ctx, row.RemoteUserID)
		if errors.Is(err, mediaserver.ErrUserNotFound) && mediaserver.KindOf(provider) == mediaserver.KindInvite {
			s.reconcileMissingShare(ctx, provider, inst, row, wantDisabled)
			continue
		}
		if err != nil {
			s.logger.Warn("mediaaccess: reconcile: read account", "err", err, "user_id", userID, "instance_id", row.InstanceID)
			continue
		}
		if live.IsAdministrator {
			// Never touched on the server. The stamp still follows the
			// grant, so the drift sweep does not re-read this row forever.
			if row.DisabledAt.Valid != wantDisabled {
				if err := s.setDisabledAt(userID, row.InstanceID, wantDisabled); err != nil {
					s.logger.Error("mediaaccess: reconcile: stamp", "err", err, "user_id", userID, "instance_id", row.InstanceID)
				}
			}
			s.logger.Info("mediaaccess: reconcile: linked account is an administrator; leaving it alone", "user_id", userID, "instance_id", row.InstanceID)
			continue
		}
		if live.IsDisabled != wantDisabled {
			if err := provider.SetDisabled(ctx, row.RemoteUserID, wantDisabled); err != nil {
				s.logger.Error("mediaaccess: reconcile: set disabled", "err", err, "user_id", userID, "instance_id", row.InstanceID, "disabled", wantDisabled)
				continue
			}
		}
		if row.DisabledAt.Valid != wantDisabled {
			if err := s.setDisabledAt(userID, row.InstanceID, wantDisabled); err != nil {
				s.logger.Error("mediaaccess: reconcile: stamp", "err", err, "user_id", userID, "instance_id", row.InstanceID)
			}
		}
	}
}

// reconcileMissingShare handles an invite-server row whose share is gone.
// Gone and revoked is the settled state: stamp it. Gone and granted is a
// re-invite only when Cantinarr itself removed the share (the row is
// stamped); a share that vanished any other way — declined, expired, taken
// away by the owner on the server — reads as absence, which the guide shows
// and the user can act on, so an unrelated grant write never emails anyone.
func (s *Service) reconcileMissingShare(ctx context.Context, provider mediaserver.Provider, inst *instance.Instance, row accountRow, wantDisabled bool) {
	if wantDisabled {
		if !row.DisabledAt.Valid {
			if err := s.setDisabledAt(row.UserID, row.InstanceID, true); err != nil {
				s.logger.Error("mediaaccess: reconcile: stamp", "err", err, "user_id", row.UserID, "instance_id", row.InstanceID)
			}
		}
		return
	}
	if !row.DisabledAt.Valid {
		return
	}
	remote, err := provider.CreateUser(ctx, row.RemoteUserID, "", inst.MediaServerConfig.LibraryIDs)
	if err != nil {
		s.logger.Error("mediaaccess: reconcile: re-invite", "err", err, "user_id", row.UserID, "instance_id", row.InstanceID)
		return
	}
	if err := s.setDisabledAt(row.UserID, row.InstanceID, false); err != nil {
		s.logger.Error("mediaaccess: reconcile: stamp", "err", err, "user_id", row.UserID, "instance_id", row.InstanceID)
	}
	// An account plex.tv still knows gets the share back accepted at once;
	// only a real invite is worth a "check your email".
	if remote.Pending {
		s.notifyUser(row.UserID, eventInviteSent)
	}
}

// BeforeUserDelete is the auth handler's delete hook. Called before the
// user is deleted, it snapshots what would need switching off; the returned
// closure does it and must run only after the delete succeeded (the delete
// can still refuse: last admin, self-delete). Rows are gone by cascade at
// that point, which is fine — the snapshot already holds the remote ids.
func (s *Service) BeforeUserDelete(userID int64) (committed func()) {
	rows, err := s.listAccountsForUser(userID)
	if err != nil {
		s.logger.Error("mediaaccess: delete hook: list accounts", "err", err, "user_id", userID)
		return func() {}
	}
	type target struct {
		inst *instance.Instance
		row  accountRow
	}
	var targets []target
	for _, row := range rows {
		inst, err := s.store.Get(row.InstanceID)
		if err != nil || inst == nil {
			continue
		}
		targets = append(targets, target{inst: inst, row: row})
	}
	return func() {
		for _, t := range targets {
			provider, err := s.providers(t.inst)
			if err != nil {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), reconcileTimeout)
			live, err := provider.GetUser(ctx, t.row.RemoteUserID)
			switch {
			case errors.Is(err, mediaserver.ErrUserNotFound):
			case err != nil:
				s.logger.Warn("mediaaccess: delete hook: read account", "err", err, "user_id", userID, "instance_id", t.inst.ID)
			case live.IsAdministrator:
				s.logger.Warn("mediaaccess: delete hook: linked account is an administrator; leaving it alone", "user_id", userID, "instance_id", t.inst.ID)
			case !live.IsDisabled:
				if err := provider.SetDisabled(ctx, t.row.RemoteUserID, true); err != nil {
					s.logger.Error("mediaaccess: delete hook: disable account", "err", err, "user_id", userID, "instance_id", t.inst.ID)
				}
			}
			cancel()
		}
	}
}
