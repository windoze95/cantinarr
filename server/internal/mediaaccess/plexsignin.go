package mediaaccess

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
	"github.com/windoze95/cantinarr-server/internal/plex"
)

// A Plex sign-in is how a person proves a Plex share is theirs: they approve
// a plex.tv PIN with their own account, Cantinarr reads that account's email
// through the token the approval yields, signs the token out again, and the
// verified email drives the same share machinery a typed email does. The
// token is proof, never a credential Cantinarr keeps.

// plexSignInTTL is how long a sign-in stays checkable. plex.tv's own PIN
// expires sooner; the cached answer lives out the rest of the window so the
// app's polling always lands on the same result.
const plexSignInTTL = 15 * time.Minute

// PlexSignInStart is what begins a sign-in: the PIN to poll and the page
// the person opens to approve it.
type PlexSignInStart struct {
	PinID int64  `json:"pin_id"`
	Code  string `json:"code"`
	URL   string `json:"url"`
}

// PlexSignInResult is a poll's answer. Linked is false until the person has
// approved the PIN; once true, Email is the verified address (canonical) and
// InviteState says what it led to: "sent" (an invite to accept), "adopted"
// (access already there), "failed" (an invite that could not go out yet; the
// drift sweep retries it), "claimed" (the account belongs to another user's
// row here; an admin has to sort out whose it is), or "" (nobody has granted
// this user Plex yet, and the admins were told).
type PlexSignInResult struct {
	Linked      bool   `json:"linked"`
	Username    string `json:"username,omitempty"`
	Email       string `json:"email,omitempty"`
	InviteState string `json:"invite_state,omitempty"`
}

// plexSignIn is one sign-in in flight, owned by the user who began it.
type plexSignIn struct {
	userID   int64
	clientID string
	expires  time.Time
	// mu serializes checks of one pin: a poll that arrives while another is
	// linking answers "not yet" instead of linking twice.
	mu     sync.Mutex
	result *PlexSignInResult
}

// plexSignIns holds sign-ins by PIN id.
type plexSignIns struct {
	mu    sync.Mutex
	byPin map[int64]*plexSignIn
}

func newPlexSignIns() *plexSignIns {
	return &plexSignIns{byPin: map[int64]*plexSignIn{}}
}

func (l *plexSignIns) put(pin int64, entry *plexSignIn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune()
	l.byPin[pin] = entry
}

func (l *plexSignIns) get(pin int64) *plexSignIn {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune()
	return l.byPin[pin]
}

func (l *plexSignIns) prune() {
	now := time.Now()
	for pin, entry := range l.byPin {
		if now.After(entry.expires) {
			delete(l.byPin, pin)
		}
	}
}

// SetPlexBaseURL points the sign-in's PIN calls at another plex.tv (tests).
func (s *Service) SetPlexBaseURL(baseURL string) {
	s.plexBaseURL = strings.TrimRight(baseURL, "/")
}

// PlexSignInBegin mints the PIN a user approves with their own Plex account.
// Each sign-in gets its own client identifier: plex.tv ties the token to it,
// and SignOut removes exactly that device again.
func (s *Service) PlexSignInBegin(ctx context.Context, userID int64) (PlexSignInStart, error) {
	clientID := uuid.NewString()
	client := plex.NewClientAt(s.plexBaseURL)
	ctx, cancel := context.WithTimeout(ctx, plexTVTimeout)
	defer cancel()
	pin, err := client.CreatePin(ctx, clientID)
	if err != nil {
		return PlexSignInStart{}, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	s.signIns.put(pin.ID, &plexSignIn{userID: userID, clientID: clientID, expires: time.Now().Add(plexSignInTTL)})
	return PlexSignInStart{PinID: pin.ID, Code: pin.Code, URL: client.AuthURL(clientID, pin.Code)}, nil
}

// PlexSignInCheck polls a sign-in. Before approval it answers Linked=false.
// After it, once: the account is read and its token signed out, the email
// is remembered on the user, the Plex instances that never recorded their
// owner learn it, and the share pass runs (a short budget, since the person
// is waiting; the drift sweep sends whatever it cut off). The answer is
// cached on the pin, so every later poll gets the same result. A pin
// another user began, an unknown one, and an expired one all read as
// ErrPinNotFound.
func (s *Service) PlexSignInCheck(ctx context.Context, userID int64, pinID int64, username string) (PlexSignInResult, error) {
	entry := s.signIns.get(pinID)
	if entry == nil || entry.userID != userID {
		return PlexSignInResult{}, ErrPinNotFound
	}
	if !entry.mu.TryLock() {
		// Another poll of this pin is linking right now; it will cache the
		// answer for the next one.
		return PlexSignInResult{Linked: false}, nil
	}
	defer entry.mu.Unlock()
	if entry.result != nil {
		return *entry.result, nil
	}

	client := plex.NewClientAt(s.plexBaseURL)
	pinCtx, cancel := context.WithTimeout(ctx, plexTVTimeout)
	pin, err := client.CheckPin(pinCtx, entry.clientID, pinID)
	cancel()
	if err != nil {
		return PlexSignInResult{}, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	if pin.AuthToken == "" {
		return PlexSignInResult{Linked: false}, nil
	}
	acctCtx, cancel := context.WithTimeout(ctx, plexTVTimeout)
	account, err := client.GetUser(acctCtx, entry.clientID, pin.AuthToken)
	if err == nil {
		// The token has done its one job. A sign-out that fails leaves an
		// idle "Cantinarr" device on the person's plex.tv account, which
		// they can remove themselves; nothing here keeps the token.
		removed, signOutErr := client.SignOut(acctCtx, entry.clientID, pin.AuthToken)
		switch {
		case signOutErr != nil:
			s.logger.Warn("mediaaccess: plex sign-in: could not sign the token out", "err", signOutErr, "user_id", userID)
		case !removed:
			s.logger.Info("mediaaccess: plex sign-in: token revoked, but plex.tv never listed the device to remove; an idle Cantinarr entry stays on the account", "user_id", userID)
		}
	}
	cancel()
	if err != nil {
		return PlexSignInResult{}, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	email := mediaserver.CanonicalEmail(account.Email)
	if !mediaserver.ValidEmail(email) {
		s.logger.Warn("mediaaccess: plex sign-in: plex.tv reported no usable email", "user_id", userID)
		return PlexSignInResult{}, ErrInvalidEmail
	}
	if err := s.rememberEmail(userID, email); err != nil {
		return PlexSignInResult{}, err
	}
	s.backfillPlexOwners(ctx)

	shareCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), plexSignInShareBudget)
	defer cancel()
	outcome := s.handleEmailShared(shareCtx, userID, username)
	result := PlexSignInResult{Linked: true, Username: account.Username, Email: email, InviteState: outcome.userState()}
	entry.result = &result
	s.logger.Info("mediaaccess: plex sign-in linked a verified email", "user_id", userID, "invite_state", result.InviteState)
	return result, nil
}

// backfillPlexOwners records the owner of every Plex instance that has none
// yet (linked before owners were recorded, or created from a pasted token),
// reading it from the stored token once. Without it the owner's own sign-in
// would look like a stranger to invite, which plex.tv refuses. Failures are
// logged and the next sign-in tries again.
func (s *Service) backfillPlexOwners(ctx context.Context) {
	instances, err := s.store.ListAll()
	if err != nil {
		s.logger.Error("mediaaccess: plex sign-in: list instances", "err", err)
		return
	}
	for i := range instances {
		inst := &instances[i]
		if inst.ServiceType != "plex" || inst.MediaServerConfigInvalid || inst.MediaServerConfig.PlexOwnerEmail != "" {
			continue
		}
		ownerCtx, cancel := context.WithTimeout(ctx, plexTVTimeout)
		account, err := plex.NewClientAt(inst.URL).GetUser(ownerCtx, inst.MediaServerConfig.ClientID, inst.APIKey)
		cancel()
		if err != nil {
			s.logger.Warn("mediaaccess: plex sign-in: could not read the instance's owner; relink the Plex account if this persists", "err", err, "instance_id", inst.ID)
			continue
		}
		if err := s.store.SetPlexOwner(inst.ID, *account); err != nil {
			s.logger.Error("mediaaccess: plex sign-in: record owner", "err", err, "instance_id", inst.ID)
			continue
		}
		s.logger.Info("mediaaccess: recorded the Plex instance's owner", "instance_id", inst.ID)
	}
}
