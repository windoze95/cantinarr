package mediaaccess

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

// fakeInviteProvider is an in-memory invite server, Plex-shaped: shares are
// keyed by canonical email, pending until accepted, and a missing share is
// absence. It implements mediaserver.Kinded so the service treats it as an
// invite server whatever service type the instance row carries.
type fakeInviteProvider struct {
	mu             sync.Mutex
	shares         map[string]*mediaserver.RemoteUser
	invites        int
	removals       int
	getErr         error
	createErr      error
	libraryWrites  []libraryWrite
	lastLibraryIDs []string
}

func newFakeInviteProvider() *fakeInviteProvider {
	return &fakeInviteProvider{shares: map[string]*mediaserver.RemoteUser{}}
}

func (f *fakeInviteProvider) Kind() mediaserver.Kind { return mediaserver.KindInvite }

// share adds a share the owner made by hand on the server.
func (f *fakeInviteProvider) share(email string, pending bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := mediaserver.CanonicalEmail(email)
	f.shares[id] = &mediaserver.RemoteUser{ID: id, Name: strings.SplitN(id, "@", 2)[0], Pending: pending}
}

func (f *fakeInviteProvider) accept(email string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u := f.shares[mediaserver.CanonicalEmail(email)]; u != nil {
		u.Pending = false
	}
}

// vanish is the owner removing the share on the server, behind Cantinarr's back.
func (f *fakeInviteProvider) vanish(email string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.shares, mediaserver.CanonicalEmail(email))
}

func (f *fakeInviteProvider) has(email string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shares[mediaserver.CanonicalEmail(email)] != nil
}

func (f *fakeInviteProvider) SystemInfo(context.Context) (mediaserver.SystemInfo, error) {
	return mediaserver.SystemInfo{ServerName: "Fake Plex"}, nil
}

func (f *fakeInviteProvider) Libraries(context.Context) ([]mediaserver.Library, error) {
	return nil, nil
}

func (f *fakeInviteProvider) Users(context.Context) ([]mediaserver.RemoteUser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []mediaserver.RemoteUser{}
	for _, u := range f.shares {
		out = append(out, *u)
	}
	return out, nil
}

func (f *fakeInviteProvider) GetUser(_ context.Context, id string) (mediaserver.RemoteUser, error) {
	if f.getErr != nil {
		return mediaserver.RemoteUser{}, f.getErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	u := f.shares[mediaserver.CanonicalEmail(id)]
	if u == nil {
		return mediaserver.RemoteUser{}, mediaserver.ErrUserNotFound
	}
	return *u, nil
}

func (f *fakeInviteProvider) CreateUser(_ context.Context, identity, password string, libraryIDs []string) (mediaserver.RemoteUser, error) {
	if password != "" {
		return mediaserver.RemoteUser{}, errors.New("fake plex: invites carry no password")
	}
	if f.createErr != nil {
		return mediaserver.RemoteUser{}, f.createErr
	}
	if !mediaserver.ValidEmail(identity) {
		return mediaserver.RemoteUser{}, mediaserver.ErrInvalidName
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id := mediaserver.CanonicalEmail(identity)
	if u := f.shares[id]; u != nil {
		// plex.tv's 422: already shared, answered with the existing share.
		return *u, nil
	}
	f.invites++
	f.lastLibraryIDs = append([]string(nil), libraryIDs...)
	f.shares[id] = &mediaserver.RemoteUser{ID: id, Name: strings.SplitN(id, "@", 2)[0], Pending: true}
	return *f.shares[id], nil
}

func (f *fakeInviteProvider) SetLibraries(_ context.Context, id string, libraryIDs []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.shares[mediaserver.CanonicalEmail(id)] == nil {
		return nil
	}
	f.libraryWrites = append(f.libraryWrites, libraryWrite{remoteID: mediaserver.CanonicalEmail(id), libraryIDs: append([]string(nil), libraryIDs...)})
	return nil
}

func (f *fakeInviteProvider) SetDisabled(_ context.Context, id string, disabled bool) error {
	if !disabled {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.shares[mediaserver.CanonicalEmail(id)] == nil {
		return mediaserver.ErrUserNotFound
	}
	delete(f.shares, mediaserver.CanonicalEmail(id))
	f.removals++
	return nil
}

func (f *fakeInviteProvider) DeleteUser(ctx context.Context, id string) error {
	err := f.SetDisabled(ctx, id, true)
	if errors.Is(err, mediaserver.ErrUserNotFound) {
		return nil
	}
	return err
}

// fakeNotifier records the pushes the service asked for.
type fakeNotifier struct {
	mu    sync.Mutex
	user  []notified
	admin []notified
}

type notified struct {
	userID int64
	event  string
	data   map[string]interface{}
}

func (n *fakeNotifier) NotifyUser(userID int64, eventType string, data map[string]interface{}) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.user = append(n.user, notified{userID: userID, event: eventType, data: data})
}

func (n *fakeNotifier) NotifyAdmins(eventType string, data map[string]interface{}) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.admin = append(n.admin, notified{event: eventType, data: data})
}

func (n *fakeNotifier) userEvents(userID int64, event string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	count := 0
	for _, e := range n.user {
		if e.userID == userID && e.event == event {
			count++
		}
	}
	return count
}

func (n *fakeNotifier) lastAdminState() (string, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.admin) == 0 {
		return "", false
	}
	state, _ := n.admin[len(n.admin)-1].data["invite_state"].(string)
	return state, true
}

// inviteServer creates a media-server instance served by an invite fake. The
// row carries the jellyfin type so the shared grant helper applies; the
// provider, not the type, is what makes it an invite server here.
func (e *env) inviteServer(name string, cfg instance.MediaServerConfig) (string, *fakeInviteProvider) {
	e.t.Helper()
	id := e.jellyfin(name, cfg)
	p := newFakeInviteProvider()
	e.providers[id] = p
	return id, p
}

func (e *env) notifier() *fakeNotifier {
	e.t.Helper()
	n := &fakeNotifier{}
	e.svc.SetNotifier(n)
	return n
}

func (e *env) setEmail(userID int64, email string) {
	e.t.Helper()
	if _, err := e.db.Exec("UPDATE users SET plex_email = ? WHERE id = ?", email, userID); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) email(userID int64) string {
	e.t.Helper()
	var email string
	if err := e.db.QueryRow("SELECT plex_email FROM users WHERE id = ?", userID).Scan(&email); err != nil {
		e.t.Fatal(err)
	}
	return email
}

func TestRequestInviteSendsInviteAndRecordsPendingRow(t *testing.T) {
	e := newEnv(t)
	pushes := e.notifier()
	alice := e.user("alice")
	plex, fake := e.inviteServer("Den Plex", instance.MediaServerConfig{PublicAddress: "https://app.plex.tv", LibraryIDs: []string{"11", "12"}})
	e.grant(alice, plex)

	created, err := e.svc.RequestInvite(context.Background(), alice, plex, "  Alice@Example.COM ")
	if err != nil {
		t.Fatal(err)
	}
	if !created.Pending || created.Username != "alice" || created.PublicAddress != "https://app.plex.tv" {
		t.Fatalf("created = %+v", created)
	}
	if fake.invites != 1 || !reflect.DeepEqual(fake.lastLibraryIDs, []string{"11", "12"}) {
		t.Fatalf("invites = %d, libraries = %v", fake.invites, fake.lastLibraryIDs)
	}
	row := e.row(alice, plex)
	if row == nil || row.RemoteUserID != "alice@example.com" || !row.CreatedByCantinarr || row.DisabledAt.Valid {
		t.Fatalf("row = %+v", row)
	}
	if e.email(alice) != "alice@example.com" {
		t.Fatalf("remembered email = %q, want canonical", e.email(alice))
	}
	if pushes.userEvents(alice, eventInviteSent) != 1 {
		t.Fatalf("invite push count = %d, want 1", pushes.userEvents(alice, eventInviteSent))
	}

	servers, err := e.svc.ListForUser(context.Background(), alice)
	if err != nil || len(servers) != 1 {
		t.Fatalf("ListForUser = %+v, %v", servers, err)
	}
	if servers[0].Kind != "invite" || servers[0].Account == nil || !servers[0].Account.Pending || !servers[0].Account.Verified {
		t.Fatalf("view = %+v account = %+v", servers[0], servers[0].Account)
	}
	fake.accept("alice@example.com")
	servers, _ = e.svc.ListForUser(context.Background(), alice)
	if servers[0].Account.Pending {
		t.Fatal("accepted share still reads as pending")
	}

	// Asking again while the share stands is the same answer as an existing account.
	if _, err := e.svc.RequestInvite(context.Background(), alice, plex, "alice@example.com"); !errors.Is(err, ErrAccountExists) {
		t.Fatalf("second request err = %v, want ErrAccountExists", err)
	}
	if fake.invites != 1 {
		t.Fatalf("second request sent another invite (%d)", fake.invites)
	}
}

func TestRequestInviteAndCreateAccountRefuseTheWrongKind(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	plex, fake := e.inviteServer("Den Plex", instance.MediaServerConfig{})
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	e.grant(alice, plex, jf)

	if _, err := e.svc.CreateAccount(context.Background(), alice, plex, "alice-pass-1"); !errors.Is(err, ErrWrongKind) {
		t.Fatalf("password on an invite server err = %v, want ErrWrongKind", err)
	}
	if _, err := e.svc.RequestInvite(context.Background(), alice, jf, "alice@example.com"); !errors.Is(err, ErrWrongKind) {
		t.Fatalf("email on an account server err = %v, want ErrWrongKind", err)
	}
	for _, bad := range []string{"", "nope", "@x", "x@", "has space@example.com"} {
		if _, err := e.svc.RequestInvite(context.Background(), alice, plex, bad); !errors.Is(err, ErrInvalidEmail) {
			t.Errorf("email %q err = %v, want ErrInvalidEmail", bad, err)
		}
	}
	// An ungranted invite server is the same answer as an ungranted account server.
	bob := e.user("bob")
	if _, err := e.svc.RequestInvite(context.Background(), bob, plex, "bob@example.com"); !errors.Is(err, ErrNotAvailable) {
		t.Fatalf("ungranted err = %v, want ErrNotAvailable", err)
	}
	if fake.invites != 0 || e.provider.creates != 0 || e.email(alice) != "" {
		t.Fatalf("refusals had side effects: invites=%d creates=%d email=%q", fake.invites, e.provider.creates, e.email(alice))
	}
}

func TestRequestInviteAdoptsAShareMadeByHand(t *testing.T) {
	e := newEnv(t)
	pushes := e.notifier()
	alice := e.user("alice")
	plex, fake := e.inviteServer("Den Plex", instance.MediaServerConfig{})
	e.grant(alice, plex)
	fake.share("Alice@example.com", false)

	created, err := e.svc.RequestInvite(context.Background(), alice, plex, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if created.Pending || fake.invites != 0 {
		t.Fatalf("adopting a share sent an invite: created=%+v invites=%d", created, fake.invites)
	}
	if row := e.row(alice, plex); row == nil || row.CreatedByCantinarr {
		t.Fatalf("row = %+v, want a linked (not created) row", row)
	}
	if pushes.userEvents(alice, eventInviteSent) != 0 {
		t.Fatal("adopting a share pushed 'check your email'")
	}
}

func TestRequestInviteRefusesAnEmailAnotherUserHolds(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.user("alice"), e.user("bob")
	plex, fake := e.inviteServer("Den Plex", instance.MediaServerConfig{})
	e.grant(alice, plex)
	e.grant(bob, plex)
	if _, err := e.svc.RequestInvite(context.Background(), bob, plex, "shared@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RequestInvite(context.Background(), alice, plex, "SHARED@example.com"); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("err = %v, want ErrNameTaken", err)
	}
	if fake.invites != 1 || !fake.has("shared@example.com") {
		t.Fatalf("bob's share was touched: invites=%d has=%v", fake.invites, fake.has("shared@example.com"))
	}
	if e.row(alice, plex) != nil {
		t.Fatal("alice got a row for bob's email")
	}
}

func TestRequestInviteWithANewAddressReplacesTheShare(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	plex, fake := e.inviteServer("Den Plex", instance.MediaServerConfig{})
	e.grant(alice, plex)
	if _, err := e.svc.RequestInvite(context.Background(), alice, plex, "old@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RequestInvite(context.Background(), alice, plex, "new@example.com"); err != nil {
		t.Fatal(err)
	}
	if fake.has("old@example.com") || !fake.has("new@example.com") || fake.removals != 1 || fake.invites != 2 {
		t.Fatalf("old=%v new=%v removals=%d invites=%d", fake.has("old@example.com"), fake.has("new@example.com"), fake.removals, fake.invites)
	}
	if row := e.row(alice, plex); row.RemoteUserID != "new@example.com" {
		t.Fatalf("row = %+v", row)
	}

	// A share an admin linked is the admin's to unlink: the user cannot move it.
	bob := e.user("bob")
	e.grant(bob, plex)
	fake.share("bob@example.com", false)
	if _, err := e.svc.LinkAccount(context.Background(), bob, plex, "bob@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RequestInvite(context.Background(), bob, plex, "bob2@example.com"); !errors.Is(err, ErrAccountExists) {
		t.Fatalf("moving a linked share err = %v, want ErrAccountExists", err)
	}
	if !fake.has("bob@example.com") || fake.has("bob2@example.com") {
		t.Fatal("a linked share was moved by the user")
	}
}

func TestRequestInviteAgainAfterTheShareVanishedSendsAFreshOne(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	plex, fake := e.inviteServer("Den Plex", instance.MediaServerConfig{})
	e.grant(alice, plex)
	if _, err := e.svc.RequestInvite(context.Background(), alice, plex, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	fake.vanish("alice@example.com")
	servers, _ := e.svc.ListForUser(context.Background(), alice)
	if servers[0].Account != nil {
		t.Fatalf("a vanished share still reads as an account: %+v", servers[0].Account)
	}
	created, err := e.svc.RequestInvite(context.Background(), alice, plex, "alice@example.com")
	if err != nil || !created.Pending {
		t.Fatalf("re-request = %+v, %v", created, err)
	}
	if fake.invites != 2 || e.row(alice, plex) == nil {
		t.Fatalf("invites = %d, row = %+v", fake.invites, e.row(alice, plex))
	}
}

func TestReconcileRemovesShareOnRevokeAndReinvitesOnRegrant(t *testing.T) {
	e := newEnv(t)
	pushes := e.notifier()
	alice := e.user("alice")
	plex, fake := e.inviteServer("Den Plex", instance.MediaServerConfig{LibraryIDs: []string{"11"}})
	e.grant(alice, plex)
	if _, err := e.svc.RequestInvite(context.Background(), alice, plex, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	fake.accept("alice@example.com")

	e.grant(alice)
	e.svc.OnGrantsChanged([]int64{alice})
	if fake.has("alice@example.com") || !e.row(alice, plex).DisabledAt.Valid {
		t.Fatal("revoked grant did not remove the share")
	}

	e.grant(alice, plex)
	e.svc.OnGrantsChanged([]int64{alice})
	if !fake.has("alice@example.com") || e.row(alice, plex).DisabledAt.Valid || fake.invites != 2 {
		t.Fatalf("returned grant did not re-invite: has=%v row=%+v invites=%d", fake.has("alice@example.com"), e.row(alice, plex), fake.invites)
	}
	if !reflect.DeepEqual(fake.lastLibraryIDs, []string{"11"}) {
		t.Fatalf("re-invite libraries = %v", fake.lastLibraryIDs)
	}
	if pushes.userEvents(alice, eventInviteSent) != 2 {
		t.Fatalf("invite pushes = %d, want 2 (first invite + re-invite)", pushes.userEvents(alice, eventInviteSent))
	}
}

func TestReconcileNeverReinvitesAShareThatVanishedOnItsOwn(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	plex, fake := e.inviteServer("Den Plex", instance.MediaServerConfig{})
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	e.grant(alice, plex, jf)
	if _, err := e.svc.RequestInvite(context.Background(), alice, plex, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	// The owner took the share away in Plex (or the invite expired). An
	// unrelated grant write for the user must not email them a new one.
	fake.vanish("alice@example.com")
	e.grant(alice, plex)
	e.svc.OnGrantsChanged([]int64{alice})
	if fake.invites != 1 || fake.has("alice@example.com") {
		t.Fatalf("a vanished share was re-invited: invites=%d", fake.invites)
	}
	if e.row(alice, plex).DisabledAt.Valid {
		t.Fatal("a vanished share was stamped as switched off by Cantinarr")
	}
}

func TestGrantSendsTheInviteAnEmailIsWaitingFor(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.user("alice"), e.user("bob")
	plex, fake := e.inviteServer("Den Plex", instance.MediaServerConfig{})
	e.setEmail(alice, "alice@example.com")

	e.grant(alice, plex)
	e.grant(bob, plex)
	e.svc.OnGrantsChanged([]int64{alice, bob})
	if fake.invites != 1 || !fake.has("alice@example.com") || e.row(alice, plex) == nil {
		t.Fatalf("grant did not invite the waiting email: invites=%d row=%+v", fake.invites, e.row(alice, plex))
	}
	if e.row(bob, plex) != nil {
		t.Fatal("a user with no email was invited to nothing, yet has a row")
	}
}

func TestSweepSendsInvitesAGrantStillOwes(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	plex, fake := e.inviteServer("Den Plex", instance.MediaServerConfig{})
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	e.setEmail(alice, "alice@example.com")
	e.grant(alice, plex, jf)

	fake.createErr = errors.New("plex.tv answered 503")
	e.svc.OnGrantsChanged([]int64{alice})
	if fake.invites != 0 || e.row(alice, plex) != nil {
		t.Fatal("an invite went out during the outage")
	}

	fake.createErr = nil
	e.svc.SweepAccountDrift(context.Background())
	if fake.invites != 1 || e.row(alice, plex) == nil {
		t.Fatalf("the sweep did not send the owed invite: invites=%d row=%+v", fake.invites, e.row(alice, plex))
	}
	// A Jellyfin grant plus a Plex email is not an owed invite: the account
	// server was never asked to create anything.
	if e.provider.creates != 0 {
		t.Fatalf("the account server saw %d creates", e.provider.creates)
	}
	e.svc.SweepAccountDrift(context.Background())
	if fake.invites != 1 {
		t.Fatal("a settled invite was sent again")
	}
}

func TestEmailSharedAutoApprovesAndTellsAdmins(t *testing.T) {
	e := newEnv(t)
	pushes := e.notifier()
	alice, bob := e.user("alice"), e.user("bob")
	auto, fake := e.inviteServer("Auto Plex", instance.MediaServerConfig{AutoApprove: true})
	manual, manualFake := e.inviteServer("Manual Plex", instance.MediaServerConfig{})

	e.setEmail(alice, "alice@example.com")
	e.svc.OnPlexEmailShared(alice, "alice")
	grants, _ := e.store.ListUserGrants(alice)
	if !contains(grants["jellyfin"], auto) || contains(grants["jellyfin"], manual) {
		t.Fatalf("auto-approve grants = %v", grants)
	}
	if fake.invites != 1 || e.row(alice, auto) == nil || manualFake.invites != 0 {
		t.Fatalf("auto=%d manual=%d row=%+v", fake.invites, manualFake.invites, e.row(alice, auto))
	}
	if state, ok := pushes.lastAdminState(); !ok || state != "sent" {
		t.Fatalf("admin push state = %q, %v; want sent", state, ok)
	}

	// Without auto-approve and without a grant the admins are told there is
	// something to do, and nothing is sent.
	e.setEmail(bob, "bob@example.com")
	if _, err := e.db.Exec("UPDATE service_instances SET media_server_config = json_set(media_server_config, '$.auto_approve', json('false')) WHERE id = ?", auto); err != nil {
		t.Fatal(err)
	}
	e.svc.OnPlexEmailShared(bob, "bob")
	if state, ok := pushes.lastAdminState(); !ok || state != "" {
		t.Fatalf("admin push state = %q, want waiting", state)
	}
	if fake.invites != 1 || e.row(bob, auto) != nil {
		t.Fatal("an ungranted user was invited")
	}

	// A granted user whose invite fails is reported as failed.
	e.grant(bob, manual)
	manualFake.createErr = errors.New("plex.tv answered 503")
	e.svc.OnPlexEmailShared(bob, "bob")
	if state, _ := pushes.lastAdminState(); state != "failed" {
		t.Fatalf("admin push state = %q, want failed", state)
	}
}

func TestEmailSharedMovesCantinarrSentSharesToTheNewAddress(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	plex, fake := e.inviteServer("Den Plex", instance.MediaServerConfig{})
	e.grant(alice, plex)
	if _, err := e.svc.RequestInvite(context.Background(), alice, plex, "old@example.com"); err != nil {
		t.Fatal(err)
	}
	e.setEmail(alice, "new@example.com") // what auth.SetPlexEmail stores before the hook fires
	e.svc.OnPlexEmailShared(alice, "alice")
	if fake.has("old@example.com") || !fake.has("new@example.com") {
		t.Fatalf("old=%v new=%v", fake.has("old@example.com"), fake.has("new@example.com"))
	}
	if row := e.row(alice, plex); row == nil || row.RemoteUserID != "new@example.com" || !row.CreatedByCantinarr {
		t.Fatalf("row = %+v", row)
	}
}

func TestLinkAccountAcceptsAnEmailOnInviteServers(t *testing.T) {
	e := newEnv(t)
	carol := e.user("carol")
	plex, fake := e.inviteServer("Den Plex", instance.MediaServerConfig{})
	fake.share("Carol@Example.com", false)

	account, err := e.svc.LinkAccount(context.Background(), carol, plex, "CAROL@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if account.RemoteUserID != "carol@example.com" || account.CreatedByCantinarr {
		t.Fatalf("account = %+v", account)
	}
	grants, _ := e.store.ListUserGrants(carol)
	if !contains(grants["jellyfin"], plex) {
		t.Fatal("linking did not grant the server")
	}
	dave := e.user("dave")
	if _, err := e.svc.LinkAccount(context.Background(), dave, plex, "nobody@example.com"); !errors.Is(err, ErrRemoteUserNotFound) {
		t.Fatalf("unknown email err = %v, want ErrRemoteUserNotFound", err)
	}
	if fake.invites != 0 {
		t.Fatal("linking sent an invite")
	}
}

func TestSharedLibrariesChangeRescopesInviteSharesAndUserDeleteRemovesThem(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	plex, fake := e.inviteServer("Den Plex", instance.MediaServerConfig{LibraryIDs: []string{"11"}})
	e.grant(alice, plex)
	if _, err := e.svc.RequestInvite(context.Background(), alice, plex, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	e.svc.OnSharedLibrariesChanged(plex, []string{"11", "12"})
	if len(fake.libraryWrites) != 1 || !reflect.DeepEqual(fake.libraryWrites[0].libraryIDs, []string{"11", "12"}) {
		t.Fatalf("library writes = %+v", fake.libraryWrites)
	}

	commit := e.svc.BeforeUserDelete(alice)
	if _, err := e.db.Exec("DELETE FROM users WHERE id = ?", alice); err != nil {
		t.Fatal(err)
	}
	commit()
	if fake.has("alice@example.com") {
		t.Fatal("deleting the user left the share in place")
	}
}
