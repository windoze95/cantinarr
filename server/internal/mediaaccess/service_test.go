package mediaaccess

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// fakeProvider is an in-memory media server.
type fakeProvider struct {
	mu             sync.Mutex
	users          map[string]*mediaserver.RemoteUser
	nextID         int
	hang           bool   // GetUser blocks until the context ends
	getErr         error  // GetUser fails with this
	createErr      error  // CreateUser fails with this before creating anything
	onCreate       func() // runs inside CreateUser, after the remote account exists
	creates        int
	deletes        int
	gets           int
	libraryWrites  []libraryWrite
	lastLibraryIDs []string
	lastPassword   string
	// passwords is what Authenticate checks against, by remote id; addUser
	// gives every account "<name>-pass-1". authErr fails the check outright;
	// onAuth runs inside a successful check, before the answer.
	passwords map[string]string
	authCalls int
	authErr   error
	onAuth    func()
	usersErr  error // Users fails with this
}

// libraryWrite records one SetLibraries call.
type libraryWrite struct {
	remoteID   string
	libraryIDs []string
}

func newFakeProvider() *fakeProvider {
	return &fakeProvider{users: map[string]*mediaserver.RemoteUser{}, passwords: map[string]string{}}
}

func (f *fakeProvider) addUser(name string, admin, disabled bool) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	id := fmt.Sprintf("remote-%d", f.nextID)
	f.users[id] = &mediaserver.RemoteUser{ID: id, Name: name, IsAdministrator: admin, IsDisabled: disabled}
	f.passwords[id] = name + "-pass-1"
	return id
}

// Authenticate implements mediaserver.Authenticator the way Jellyfin does:
// 401 (ErrBadCredentials) for a wrong password or an unknown name, 403
// (ErrAccountRefused) for a disabled account.
func (f *fakeProvider) Authenticate(_ context.Context, username, password string) (mediaserver.RemoteUser, error) {
	f.mu.Lock()
	f.authCalls++
	if f.authErr != nil {
		f.mu.Unlock()
		return mediaserver.RemoteUser{}, f.authErr
	}
	var found *mediaserver.RemoteUser
	var id string
	for candidate, u := range f.users {
		if strings.EqualFold(u.Name, username) {
			found, id = u, candidate
			break
		}
	}
	if found == nil || password == "" || f.passwords[id] != password {
		f.mu.Unlock()
		return mediaserver.RemoteUser{}, mediaserver.ErrBadCredentials
	}
	if found.IsDisabled {
		f.mu.Unlock()
		return mediaserver.RemoteUser{}, mediaserver.ErrAccountRefused
	}
	out := *found
	hook := f.onAuth
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return out, nil
}

func (f *fakeProvider) user(id string) mediaserver.RemoteUser {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u := f.users[id]; u != nil {
		return *u
	}
	return mediaserver.RemoteUser{}
}

func (f *fakeProvider) SystemInfo(context.Context) (mediaserver.SystemInfo, error) {
	return mediaserver.SystemInfo{ServerName: "Fake"}, nil
}

func (f *fakeProvider) Libraries(context.Context) ([]mediaserver.Library, error) { return nil, nil }

func (f *fakeProvider) Users(context.Context) ([]mediaserver.RemoteUser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.usersErr != nil {
		return nil, f.usersErr
	}
	out := []mediaserver.RemoteUser{}
	for _, u := range f.users {
		out = append(out, *u)
	}
	return out, nil
}

func (f *fakeProvider) GetUser(ctx context.Context, id string) (mediaserver.RemoteUser, error) {
	f.mu.Lock()
	f.gets++
	f.mu.Unlock()
	if f.hang {
		<-ctx.Done()
		return mediaserver.RemoteUser{}, fmt.Errorf("fake: %s", ctx.Err())
	}
	if f.getErr != nil {
		return mediaserver.RemoteUser{}, f.getErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	u := f.users[id]
	if u == nil {
		return mediaserver.RemoteUser{}, mediaserver.ErrUserNotFound
	}
	return *u, nil
}

func (f *fakeProvider) CreateUser(ctx context.Context, name, password string, libraryIDs []string) (mediaserver.RemoteUser, error) {
	if f.createErr != nil {
		return mediaserver.RemoteUser{}, f.createErr
	}
	if !mediaserver.ValidUsername(name) {
		return mediaserver.RemoteUser{}, mediaserver.ErrInvalidName
	}
	f.mu.Lock()
	for _, u := range f.users {
		if strings.EqualFold(u.Name, name) {
			f.mu.Unlock()
			return mediaserver.RemoteUser{}, mediaserver.ErrUserExists
		}
	}
	f.creates++
	f.lastLibraryIDs = append([]string(nil), libraryIDs...)
	f.lastPassword = password
	f.nextID++
	id := fmt.Sprintf("remote-%d", f.nextID)
	f.users[id] = &mediaserver.RemoteUser{ID: id, Name: name}
	hook := f.onCreate
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return mediaserver.RemoteUser{ID: id, Name: name}, nil
}

func (f *fakeProvider) SetLibraries(_ context.Context, id string, libraryIDs []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.users[id] == nil {
		return mediaserver.ErrUserNotFound
	}
	f.libraryWrites = append(f.libraryWrites, libraryWrite{remoteID: id, libraryIDs: append([]string(nil), libraryIDs...)})
	return nil
}

func (f *fakeProvider) SetDisabled(_ context.Context, id string, disabled bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := f.users[id]
	if u == nil {
		return mediaserver.ErrUserNotFound
	}
	u.IsDisabled = disabled
	return nil
}

func (f *fakeProvider) DeleteUser(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes++
	delete(f.users, id)
	return nil
}

type env struct {
	t         *testing.T
	db        *sql.DB
	store     *instance.Store
	svc       *Service
	provider  *fakeProvider
	providers map[string]mediaserver.Provider // per instance id; falls back to provider
	real      map[string]bool                 // instance ids served by the production provider factory
	logs      *bytes.Buffer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, db: database, store: instance.NewStore(database, cipher), provider: newFakeProvider(), providers: map[string]mediaserver.Provider{}, real: map[string]bool{}, logs: &bytes.Buffer{}}
	logger := slog.New(slog.NewTextHandler(e.logs, nil))
	e.svc = NewService(database, e.store, func(inst *instance.Instance) (mediaserver.Provider, error) {
		if e.real[inst.ID] {
			return instance.NewMediaServerProvider(inst)
		}
		if p, ok := e.providers[inst.ID]; ok {
			return p, nil
		}
		return e.provider, nil
	}, logger)
	// Invite passes run off the request in production; here they run inline
	// so a test can assert right after the call that triggered them.
	e.svc.background = func(fn func()) { fn() }
	return e
}

func (e *env) user(name string) int64 {
	e.t.Helper()
	res, err := e.db.Exec("INSERT INTO users (username, password_hash, role) VALUES (?, '', 'user')", name)
	if err != nil {
		e.t.Fatalf("create user: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

const testInstanceKey = "instance-key-SENTINEL"

func itoa(id int64) string { return fmt.Sprintf("%d", id) }

func (e *env) jellyfin(name string, cfg instance.MediaServerConfig) string {
	return e.mediaServer("jellyfin", name, cfg)
}

func (e *env) mediaServer(serviceType, name string, cfg instance.MediaServerConfig) string {
	e.t.Helper()
	inst := &instance.Instance{ServiceType: serviceType, Name: name, URL: "http://" + serviceType + ".internal:8096", APIKey: testInstanceKey, MediaServerConfig: cfg}
	if err := e.store.Create(inst); err != nil {
		e.t.Fatalf("create %s: %v", serviceType, err)
	}
	return inst.ID
}

func (e *env) grant(userID int64, instanceIDs ...string) {
	e.t.Helper()
	e.grantType(userID, "jellyfin", instanceIDs...)
}

// grantType is grant for instances of another media-server type.
func (e *env) grantType(userID int64, serviceType string, instanceIDs ...string) {
	e.t.Helper()
	if err := e.store.SetUserGrants(userID, map[string][]string{serviceType: instanceIDs}); err != nil {
		e.t.Fatalf("grant: %v", err)
	}
}

func (e *env) row(userID int64, instanceID string) *accountRow {
	e.t.Helper()
	row, err := e.svc.getAccount(userID, instanceID)
	if err != nil {
		e.t.Fatal(err)
	}
	return row
}

func TestCreateAccountRequiresGrant(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	radarr := &instance.Instance{ServiceType: "radarr", Name: "Movies", URL: "http://radarr:7878", APIKey: "k"}
	if err := e.store.Create(radarr); err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetUserGrants(alice, map[string][]string{"radarr": {radarr.ID}}); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"unknown": "jellyfin-nope", "radarr": radarr.ID, "ungranted jellyfin": jf} {
		if _, err := e.svc.CreateAccount(context.Background(), alice, id, "alice-pass-1"); !errors.Is(err, ErrNotAvailable) {
			t.Errorf("%s: err = %v, want ErrNotAvailable", name, err)
		}
	}
	if e.provider.creates != 0 {
		t.Fatalf("provider created %d accounts, want 0", e.provider.creates)
	}
}

func TestCreateAccountUsesCantinarrUsernameAndLibraryIDs(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	jf := e.jellyfin("Home", instance.MediaServerConfig{PublicAddress: "https://jf.example.com", LibraryIDs: []string{"lib-movies"}})
	e.grant(alice, jf)

	created, err := e.svc.CreateAccount(context.Background(), alice, jf, "alice-pass-1")
	if err != nil {
		t.Fatal(err)
	}
	if created.Username != "alice" || created.PublicAddress != "https://jf.example.com" {
		t.Fatalf("created = %+v", created)
	}
	if e.provider.lastPassword != "alice-pass-1" || len(e.provider.lastLibraryIDs) != 1 || e.provider.lastLibraryIDs[0] != "lib-movies" {
		t.Fatalf("provider got password %q libraries %v", e.provider.lastPassword, e.provider.lastLibraryIDs)
	}
	row := e.row(alice, jf)
	if row == nil || !row.CreatedByCantinarr || row.RemoteUsername != "alice" || row.DisabledAt.Valid {
		t.Fatalf("row = %+v", row)
	}
	var stored string
	if err := e.db.QueryRow("SELECT remote_user_id || remote_username FROM user_media_server_accounts").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "alice-pass-1") {
		t.Fatal("the password was stored")
	}

	views, err := e.svc.ListForUser(context.Background(), alice)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].Account == nil || !views[0].Account.Verified || views[0].Account.Username != "alice" || views[0].PublicAddress != "https://jf.example.com" {
		t.Fatalf("views = %+v", views)
	}
}

func TestCreateAccountInvalidNameLeavesNoAccount(t *testing.T) {
	e := newEnv(t)
	weird := e.user("alice/1")
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	e.grant(weird, jf)
	if _, err := e.svc.CreateAccount(context.Background(), weird, jf, "alice-pass-1"); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("err = %v, want ErrInvalidName", err)
	}
	if e.provider.creates != 0 || e.row(weird, jf) != nil {
		t.Fatal("an invalid name created something")
	}
}

func TestCreateAccountNameTaken(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	e.grant(alice, jf)
	e.provider.addUser("Alice", false, false)
	if _, err := e.svc.CreateAccount(context.Background(), alice, jf, "alice-pass-1"); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("err = %v, want ErrNameTaken", err)
	}
	if e.row(alice, jf) != nil || e.provider.creates != 0 {
		t.Fatal("name collision created something")
	}
}

func TestCreateAccountRollsBackWhenGrantVanishes(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	e.grant(alice, jf)
	e.provider.onCreate = func() { e.grant(alice) }

	if _, err := e.svc.CreateAccount(context.Background(), alice, jf, "alice-pass-1"); !errors.Is(err, ErrNotAvailable) {
		t.Fatalf("err = %v, want ErrNotAvailable", err)
	}
	if e.provider.deletes != 1 || len(e.provider.users) != 0 {
		t.Fatalf("remote account not rolled back: deletes=%d users=%d", e.provider.deletes, len(e.provider.users))
	}
	if e.row(alice, jf) != nil {
		t.Fatal("a row survived the revoked grant")
	}
}

func TestCreateAccountRollsBackWhenInstanceDeleted(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	e.grant(alice, jf)
	e.provider.onCreate = func() {
		if err := e.store.Delete(jf); err != nil {
			t.Errorf("delete instance: %v", err)
		}
	}
	if _, err := e.svc.CreateAccount(context.Background(), alice, jf, "alice-pass-1"); !errors.Is(err, ErrNotAvailable) {
		t.Fatalf("err = %v, want ErrNotAvailable", err)
	}
	if e.provider.deletes != 1 {
		t.Fatalf("remote account not rolled back: deletes=%d", e.provider.deletes)
	}
}

func TestCreateAccountDoubleTapYieldsOneAccount(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	e.grant(alice, jf)
	e.provider.onCreate = func() { time.Sleep(30 * time.Millisecond) }

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = e.svc.CreateAccount(context.Background(), alice, jf, "alice-pass-1")
		}(i)
	}
	wg.Wait()
	var ok, exists int
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrAccountExists):
			exists++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || exists != 1 || e.provider.creates != 1 {
		t.Fatalf("ok=%d exists=%d creates=%d, want 1/1/1", ok, exists, e.provider.creates)
	}
}

func TestCreateAccountReplacesRowWhenRemoteUserGone(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	e.grant(alice, jf)
	if _, err := e.svc.CreateAccount(context.Background(), alice, jf, "alice-pass-1"); err != nil {
		t.Fatal(err)
	}
	first := e.row(alice, jf).RemoteUserID
	if _, err := e.svc.CreateAccount(context.Background(), alice, jf, "alice-pass-1"); !errors.Is(err, ErrAccountExists) {
		t.Fatalf("second create = %v, want ErrAccountExists", err)
	}
	// The admin deleted the account on the server: the stale row must not
	// block the user forever.
	e.provider.mu.Lock()
	delete(e.provider.users, first)
	e.provider.mu.Unlock()
	if _, err := e.svc.CreateAccount(context.Background(), alice, jf, "alice-pass-2"); err != nil {
		t.Fatalf("recreate after remote deletion: %v", err)
	}
	if got := e.row(alice, jf).RemoteUserID; got == first {
		t.Fatal("row still points at the deleted remote account")
	}
}

func TestListForUserVerifiedAndBlindStates(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	okServer := e.jellyfin("A Fine", instance.MediaServerConfig{})
	deadServer := e.jellyfin("B Dead", instance.MediaServerConfig{PublicAddress: "https://dead.example.com"})
	goneServer := e.jellyfin("C Gone", instance.MediaServerConfig{})
	noAccount := e.jellyfin("D Empty", instance.MediaServerConfig{})
	e.grant(alice, okServer, deadServer, goneServer, noAccount)
	fine, dead, gone := newFakeProvider(), newFakeProvider(), newFakeProvider()
	dead.hang = true
	e.providers[okServer], e.providers[deadServer], e.providers[goneServer] = fine, dead, gone
	fineID := fine.addUser("alice", false, true)
	for _, pair := range []struct {
		inst, remote string
	}{{okServer, fineID}, {deadServer, "remote-dead"}, {goneServer, "remote-gone"}} {
		if _, err := e.svc.insertAccount(accountRow{UserID: alice, InstanceID: pair.inst, RemoteUserID: pair.remote, RemoteUsername: "alice", CreatedByCantinarr: true}, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.svc.setDisabledAt(alice, deadServer, true); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	views, err := e.svc.ListForUser(context.Background(), alice)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("ListForUser took %s with one dead server, want a bounded wait", elapsed)
	}
	byName := map[string]ServerView{}
	for _, v := range views {
		byName[v.Name] = v
	}
	if a := byName["A Fine"].Account; a == nil || !a.Verified || !a.Disabled || a.Username != "alice" {
		t.Fatalf("live account = %+v, want verified and disabled as the server says", a)
	}
	if b := byName["B Dead"].Account; b == nil || b.Verified || !b.Disabled || b.Username != "alice" {
		t.Fatalf("unreachable server account = %+v, want the stored row with verified=false", b)
	}
	if byName["B Dead"].PublicAddress != "https://dead.example.com" {
		t.Fatal("public address dropped from the view")
	}
	if c := byName["C Gone"].Account; c != nil {
		t.Fatalf("account gone on the server = %+v, want nil (definitive absence)", c)
	}
	if d := byName["D Empty"].Account; d != nil {
		t.Fatalf("never-created account = %+v, want nil", d)
	}
}

// The guide leads with signing in when the server already holds an account
// named like the user: the flag is a confirmed, unlinked, same-named account
// on an account server the user has no account on, and nothing else.
func TestListForUserFlagsExistingSameNamedAccount(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	bob := e.user("bob")
	same := e.jellyfin("A Same", instance.MediaServerConfig{})
	linked := e.jellyfin("B Linked", instance.MediaServerConfig{})
	other := e.jellyfin("C Other", instance.MediaServerConfig{})
	dead := e.jellyfin("D Dead", instance.MediaServerConfig{})
	claimed := e.jellyfin("E Claimed", instance.MediaServerConfig{})
	plex := e.mediaServer("plex", "F Plex", instance.MediaServerConfig{PublicAddress: instance.PlexPublicAddress})
	e.grant(alice, same, linked, other, dead, claimed)
	e.grantType(alice, "plex", plex)

	sameP, linkedP, otherP, deadP, claimedP := newFakeProvider(), newFakeProvider(), newFakeProvider(), newFakeProvider(), newFakeProvider()
	sameP.addUser("Alice", false, true) // a different case, and switched off: still hers to sign in with
	linkedID := linkedP.addUser("alice", false, false)
	otherP.addUser("bob", false, false)
	deadP.addUser("alice", false, false)
	deadP.usersErr = errors.New("jellyfin list users: status 503")
	claimedID := claimedP.addUser("alice", false, false)
	invite := newFakeInviteProvider()
	invite.share("alice@example.com", false)
	e.providers[same], e.providers[linked], e.providers[other], e.providers[dead], e.providers[claimed], e.providers[plex] = sameP, linkedP, otherP, deadP, claimedP, invite
	for _, pair := range []struct {
		user   int64
		inst   string
		remote string
	}{{alice, linked, linkedID}, {bob, claimed, claimedID}} {
		if _, err := e.svc.insertAccount(accountRow{UserID: pair.user, InstanceID: pair.inst, RemoteUserID: pair.remote, RemoteUsername: "alice", CreatedByCantinarr: false}, false); err != nil {
			t.Fatal(err)
		}
	}

	views, err := e.svc.ListForUser(context.Background(), alice)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]ServerView{}
	for _, v := range views {
		byName[v.Name] = v
	}
	if v := byName["A Same"]; !v.ExistingAccount || v.Account != nil {
		t.Fatalf("same-named account: view = %+v, want existing_account=true and no linked account", v)
	}
	if v := byName["B Linked"]; v.ExistingAccount || v.Account == nil {
		t.Fatalf("linked account: view = %+v, want existing_account=false with the account", v)
	}
	if v := byName["C Other"]; v.ExistingAccount {
		t.Fatalf("other names only: view = %+v, want existing_account=false", v)
	}
	if v := byName["D Dead"]; v.ExistingAccount {
		t.Fatalf("unreachable server: view = %+v, want existing_account=false (blindness is not presence)", v)
	}
	if v := byName["E Claimed"]; v.ExistingAccount {
		t.Fatalf("account linked to another user: view = %+v, want existing_account=false", v)
	}
	if v := byName["F Plex"]; v.ExistingAccount {
		t.Fatalf("invite server: view = %+v, want existing_account=false", v)
	}
	if logs := e.logs.String(); !strings.Contains(logs, "could not check for an existing account") || strings.Contains(logs, "alice") {
		t.Fatalf("logs = %q, want the unreachable server noted with ids only", logs)
	}
}

func TestReconcileDisablesOnRevokeAndEnablesOnRegrant(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	e.grant(alice, jf)
	if _, err := e.svc.CreateAccount(context.Background(), alice, jf, "alice-pass-1"); err != nil {
		t.Fatal(err)
	}
	remote := e.row(alice, jf).RemoteUserID

	e.grant(alice)
	e.svc.OnGrantsChanged([]int64{alice})
	if !e.provider.user(remote).IsDisabled || !e.row(alice, jf).DisabledAt.Valid {
		t.Fatal("revoked grant did not disable the account")
	}

	e.grant(alice, jf)
	e.svc.OnGrantsChanged([]int64{alice})
	if e.provider.user(remote).IsDisabled || e.row(alice, jf).DisabledAt.Valid {
		t.Fatal("returned grant did not re-enable the account")
	}

	// A linked administrator is never touched on the server; its row still
	// follows the grant, so the drift sweep has nothing to retry.
	bob := e.user("bob")
	adminID := e.provider.addUser("bob", true, false)
	if _, err := e.svc.insertAccount(accountRow{UserID: bob, InstanceID: jf, RemoteUserID: adminID, RemoteUsername: "bob"}, false); err != nil {
		t.Fatal(err)
	}
	e.svc.OnGrantsChanged([]int64{bob})
	if e.provider.user(adminID).IsDisabled {
		t.Fatal("reconcile disabled an administrator account")
	}
	if !e.row(bob, jf).DisabledAt.Valid {
		t.Fatal("an ungranted administrator row was not stamped")
	}
	if !strings.Contains(e.logs.String(), "administrator") {
		t.Fatal("skipping an administrator was not logged")
	}
	if drifted, err := e.svc.listDriftedAccountUsers(); err != nil || len(drifted) != 0 {
		t.Fatalf("drift candidates after the admin skip = %v, %v; want none", drifted, err)
	}
	e.grant(bob, jf)
	e.svc.OnGrantsChanged([]int64{bob})
	if e.row(bob, jf).DisabledAt.Valid || e.provider.user(adminID).IsDisabled {
		t.Fatal("re-granting the administrator did not clear the stamp, or touched the account")
	}
}

func TestReconcileOnlyTouchesAffectedUsers(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.user("alice"), e.user("bob")
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	e.grant(alice, jf)
	e.grant(bob, jf)
	for _, u := range []int64{alice, bob} {
		if _, err := e.svc.CreateAccount(context.Background(), u, jf, "pass-word-1"); err != nil {
			t.Fatal(err)
		}
	}
	bobRemote := e.row(bob, jf).RemoteUserID
	// An admin disabled bob on the server by hand. Reconciling alice must
	// not "fix" bob.
	if err := e.provider.SetDisabled(context.Background(), bobRemote, true); err != nil {
		t.Fatal(err)
	}
	e.grant(alice)
	e.svc.OnGrantsChanged([]int64{alice})
	if !e.provider.user(bobRemote).IsDisabled {
		t.Fatal("reconciling alice re-enabled bob")
	}
	if !e.provider.user(e.row(alice, jf).RemoteUserID).IsDisabled {
		t.Fatal("alice was not disabled")
	}
}

func TestLinkAccountAcceptsAdministratorsAndRefusesDuplicateRemote(t *testing.T) {
	e := newEnv(t)
	alice, bob, root := e.user("alice"), e.user("bob"), e.user("root")
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	adminID := e.provider.addUser("root", true, false)
	aliceID := e.provider.addUser("alice", false, false)
	ctx := context.Background()

	// An administrator links like anyone else and is then left alone: the
	// grant the link adds reconciles without a write to the server.
	linked, err := e.svc.LinkAccount(ctx, root, jf, adminID)
	if err != nil || linked.CreatedByCantinarr || linked.Username != "root" || linked.Disabled {
		t.Fatalf("link admin = %+v, %v", linked, err)
	}
	if e.provider.user(adminID).IsDisabled {
		t.Fatal("linking an administrator touched the account")
	}
	views, err := e.svc.ListForUser(ctx, root)
	if err != nil || len(views) != 1 || views[0].Account == nil || !views[0].Account.Administrator || !views[0].Account.Verified {
		t.Fatalf("root's guide = %+v, %v; want an administrator account", views, err)
	}
	if _, err := e.svc.LinkAccount(ctx, alice, jf, "remote-nope"); !errors.Is(err, ErrRemoteUserNotFound) {
		t.Fatalf("link unknown = %v, want ErrRemoteUserNotFound", err)
	}
	if _, err := e.svc.LinkAccount(ctx, alice, jf, aliceID); err != nil {
		t.Fatalf("link alice: %v", err)
	}
	if _, err := e.svc.LinkAccount(ctx, bob, jf, aliceID); !errors.Is(err, ErrRemoteAlreadyLinked) {
		t.Fatalf("link same remote to bob = %v, want ErrRemoteAlreadyLinked", err)
	}
	carolID := e.provider.addUser("carol", false, false)
	if _, err := e.svc.LinkAccount(ctx, alice, jf, carolID); !errors.Is(err, ErrAccountExists) {
		t.Fatalf("second link for alice = %v, want ErrAccountExists", err)
	}
	if _, err := e.svc.LinkAccount(ctx, 999, jf, aliceID); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("link unknown user = %v, want ErrUserNotFound", err)
	}
	if _, err := e.svc.LinkAccount(ctx, alice, "jellyfin-nope", aliceID); !errors.Is(err, ErrInstanceNotFound) {
		t.Fatalf("link unknown instance = %v, want ErrInstanceNotFound", err)
	}
}

func TestLinkAccountGrantsAndEnables(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	remote := e.provider.addUser("alice", false, true)

	account, err := e.svc.LinkAccount(context.Background(), alice, jf, remote)
	if err != nil {
		t.Fatal(err)
	}
	if account.CreatedByCantinarr || account.Username != "alice" || account.InstanceName != "Home" || account.ServiceType != "jellyfin" || account.Disabled {
		t.Fatalf("account = %+v", account)
	}
	grants, _ := e.store.ListUserGrants(alice)
	if len(grants["jellyfin"]) != 1 || grants["jellyfin"][0] != jf {
		t.Fatalf("grants after link = %v, want the instance", grants)
	}
	if e.provider.user(remote).IsDisabled {
		t.Fatal("linking a granted user left the account disabled")
	}
	all, err := e.svc.ListAccounts()
	if err != nil || len(all) != 1 || all[0].UserID != alice {
		t.Fatalf("ListAccounts = %+v, %v", all, err)
	}
}

func TestUnlinkLeavesRemoteAndGrant(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	e.grant(alice, jf)
	if _, err := e.svc.CreateAccount(context.Background(), alice, jf, "alice-pass-1"); err != nil {
		t.Fatal(err)
	}
	remote := e.row(alice, jf).RemoteUserID
	if err := e.svc.UnlinkAccount(alice, jf); err != nil {
		t.Fatal(err)
	}
	if e.row(alice, jf) != nil {
		t.Fatal("row survived unlink")
	}
	if u := e.provider.user(remote); u.ID == "" || u.IsDisabled {
		t.Fatalf("remote account after unlink = %+v, want untouched", u)
	}
	if grants, _ := e.store.ListUserGrants(alice); len(grants["jellyfin"]) != 1 {
		t.Fatal("unlink removed the grant")
	}
	if err := e.svc.UnlinkAccount(alice, jf); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("second unlink = %v, want ErrNoAccount", err)
	}
}

func TestBeforeUserDeleteCommitsOnlyAfterDelete(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	e.grant(alice, jf)
	if _, err := e.svc.CreateAccount(context.Background(), alice, jf, "alice-pass-1"); err != nil {
		t.Fatal(err)
	}
	remote := e.row(alice, jf).RemoteUserID

	committed := e.svc.BeforeUserDelete(alice)
	if e.provider.user(remote).IsDisabled {
		t.Fatal("prepare must not touch the server (the delete can still refuse)")
	}
	if _, err := e.db.Exec("DELETE FROM users WHERE id = ?", alice); err != nil {
		t.Fatal(err)
	}
	if e.row(alice, jf) != nil {
		t.Fatal("row did not cascade with the user")
	}
	committed()
	if u := e.provider.user(remote); u.ID == "" || !u.IsDisabled {
		t.Fatalf("remote account after delete = %+v, want present and disabled", u)
	}
}

func TestRemoteUsersRequiresMediaServer(t *testing.T) {
	e := newEnv(t)
	radarr := &instance.Instance{ServiceType: "radarr", Name: "Movies", URL: "http://radarr:7878", APIKey: "k"}
	if err := e.store.Create(radarr); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RemoteUsers(context.Background(), radarr.ID); !errors.Is(err, ErrNotMediaServer) {
		t.Fatalf("radarr = %v, want ErrNotMediaServer", err)
	}
	if _, err := e.svc.RemoteUsers(context.Background(), "jellyfin-nope"); !errors.Is(err, ErrInstanceNotFound) {
		t.Fatalf("unknown = %v, want ErrInstanceNotFound", err)
	}
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	e.provider.addUser("alice", false, false)
	users, err := e.svc.RemoteUsers(context.Background(), jf)
	if err != nil || len(users) != 1 {
		t.Fatalf("RemoteUsers = %+v, %v", users, err)
	}
}

// A grant write never fails because the media server is down, so the
// switch-off it decided on can be owed to the server long after the admin saw
// a success. The sweep is what pays that debt: until it existed, a grant
// revoked during an outage left the account signed-in-able forever.
func TestSweepRetriesASwitchOffLostToAnOutage(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	e.grant(alice, jf)
	if _, err := e.svc.CreateAccount(context.Background(), alice, jf, "alice-pass-1"); err != nil {
		t.Fatal(err)
	}
	remote := e.row(alice, jf).RemoteUserID

	// The admin revokes the grant while the server is unreachable.
	e.provider.getErr = errors.New("dial tcp: no route to host")
	e.grant(alice)
	e.svc.OnGrantsChanged([]int64{alice})
	if e.provider.user(remote).IsDisabled {
		t.Fatal("the fake disabled the account despite being unreachable")
	}
	if e.row(alice, jf).DisabledAt.Valid {
		t.Fatal("a switch-off that never reached the server was stamped as done")
	}

	// The server comes back; the next sweep applies what was owed.
	e.provider.getErr = nil
	e.svc.SweepAccountDrift(context.Background())
	if !e.provider.user(remote).IsDisabled {
		t.Fatal("the sweep did not disable the account whose grant was revoked")
	}
	if !e.row(alice, jf).DisabledAt.Valid {
		t.Fatal("the sweep disabled the account without stamping the row")
	}

	// Nothing is owed now, so a second pass must not touch the server again.
	before := e.provider.gets
	e.svc.SweepAccountDrift(context.Background())
	if e.provider.gets != before {
		t.Fatalf("a settled sweep made %d extra calls", e.provider.gets-before)
	}
}

// The same debt in the other direction: access handed back during an outage.
func TestSweepRetriesASwitchOnLostToAnOutage(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	e.grant(alice, jf)
	if _, err := e.svc.CreateAccount(context.Background(), alice, jf, "alice-pass-1"); err != nil {
		t.Fatal(err)
	}
	remote := e.row(alice, jf).RemoteUserID
	e.grant(alice)
	e.svc.OnGrantsChanged([]int64{alice})

	e.provider.getErr = errors.New("dial tcp: no route to host")
	e.grant(alice, jf)
	e.svc.OnGrantsChanged([]int64{alice})
	if !e.provider.user(remote).IsDisabled {
		t.Fatal("the fake re-enabled the account despite being unreachable")
	}

	e.provider.getErr = nil
	e.svc.SweepAccountDrift(context.Background())
	if e.provider.user(remote).IsDisabled || e.row(alice, jf).DisabledAt.Valid {
		t.Fatal("the sweep did not re-enable the account whose grant returned")
	}
}

// The sweep asks Cantinarr's own tables which accounts it still owes the
// server, so a fleet that agrees with its grants costs no media-server calls
// at all — and an account an admin disabled on the server side is left alone
// rather than fought over every five minutes.
func TestSweepIgnoresAccountsThatAgreeWithTheirGrants(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	bob := e.user("bob")
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	e.grant(alice, jf)
	e.grant(bob, jf)
	if _, err := e.svc.CreateAccount(context.Background(), alice, jf, "alice-pass-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateAccount(context.Background(), bob, jf, "bob-pass-123"); err != nil {
		t.Fatal(err)
	}
	// An admin switches bob off in the media server's own UI.
	bobRemote := e.row(bob, jf).RemoteUserID
	if err := e.provider.SetDisabled(context.Background(), bobRemote, true); err != nil {
		t.Fatal(err)
	}

	before := e.provider.gets
	e.svc.SweepAccountDrift(context.Background())
	if e.provider.gets != before {
		t.Fatalf("sweep made %d media-server calls with nothing owed", e.provider.gets-before)
	}
	if !e.provider.user(bobRemote).IsDisabled {
		t.Fatal("the sweep undid an admin's own change on the server")
	}
}

// Unticking a shared library has to take it away from the people who already
// have accounts, not only from future ones — but only for accounts Cantinarr
// created. A linked account's policy stays the admin's business.
func TestSharedLibrariesChangeRescopesOnlyAccountsCantinarrCreated(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	bob := e.user("bob")
	jf := e.jellyfin("Home", instance.MediaServerConfig{LibraryIDs: []string{"lib-movies", "lib-shows"}})
	other := e.jellyfin("Cabin", instance.MediaServerConfig{LibraryIDs: []string{"lib-movies"}})
	otherFake := newFakeProvider()
	e.providers[other] = otherFake
	e.grant(alice, jf)
	e.grant(bob, jf)
	if _, err := e.svc.CreateAccount(context.Background(), alice, jf, "alice-pass-1"); err != nil {
		t.Fatal(err)
	}
	aliceRemote := e.row(alice, jf).RemoteUserID
	// bob's account was made on the server by hand and linked afterwards.
	bobRemote := e.provider.addUser("bob-on-jellyfin", false, false)
	if _, err := e.svc.LinkAccount(context.Background(), bob, jf, bobRemote); err != nil {
		t.Fatal(err)
	}
	e.provider.libraryWrites = nil

	e.svc.OnSharedLibrariesChanged(jf, []string{"lib-movies"})

	if len(e.provider.libraryWrites) != 1 {
		t.Fatalf("library writes = %+v, want exactly one", e.provider.libraryWrites)
	}
	write := e.provider.libraryWrites[0]
	if write.remoteID != aliceRemote {
		t.Fatalf("re-scoped %s, want the account Cantinarr created (%s)", write.remoteID, aliceRemote)
	}
	if !reflect.DeepEqual(write.libraryIDs, []string{"lib-movies"}) {
		t.Fatalf("re-scoped to %v, want the new selection", write.libraryIDs)
	}
	if writes := otherFake.libraryWrites; len(writes) != 0 {
		t.Fatalf("another instance was re-scoped: %+v", writes)
	}
}

// The service never names a media server: everything it decides comes from
// IsMediaServerType and the grant, so each registered type must get the whole
// create / switch-off / switch-on cycle. A type missing from any list fails
// here before it fails for an admin.
func TestCreateAndReconcileForEveryMediaServerType(t *testing.T) {
	for _, serviceType := range instance.MediaServerTypes() {
		t.Run(serviceType, func(t *testing.T) {
			e := newEnv(t)
			alice := e.user("alice")
			inst := e.mediaServer(serviceType, "Home", instance.MediaServerConfig{LibraryIDs: []string{"lib-1"}})
			grant := func(ids ...string) {
				t.Helper()
				if err := e.store.SetUserGrants(alice, map[string][]string{serviceType: ids}); err != nil {
					t.Fatal(err)
				}
			}

			if _, err := e.svc.CreateAccount(context.Background(), alice, inst, "alice-pass-1"); !errors.Is(err, ErrNotAvailable) {
				t.Fatalf("ungranted create err = %v, want ErrNotAvailable", err)
			}
			grant(inst)
			created, err := e.svc.CreateAccount(context.Background(), alice, inst, "alice-pass-1")
			if err != nil {
				t.Fatal(err)
			}
			if created.Username != "alice" || !reflect.DeepEqual(e.provider.lastLibraryIDs, []string{"lib-1"}) {
				t.Fatalf("created = %+v, libraries = %v", created, e.provider.lastLibraryIDs)
			}
			remote := e.row(alice, inst).RemoteUserID

			servers, err := e.svc.ListForUser(context.Background(), alice)
			if err != nil || len(servers) != 1 || servers[0].ServiceType != serviceType || servers[0].Account == nil {
				t.Fatalf("ListForUser = %+v, %v", servers, err)
			}

			grant()
			e.svc.OnGrantsChanged([]int64{alice})
			if !e.provider.user(remote).IsDisabled || !e.row(alice, inst).DisabledAt.Valid {
				t.Fatal("revoked grant did not disable the account")
			}
			grant(inst)
			e.svc.OnGrantsChanged([]int64{alice})
			if e.provider.user(remote).IsDisabled || e.row(alice, inst).DisabledAt.Valid {
				t.Fatal("returned grant did not re-enable the account")
			}
		})
	}
}

func TestLinkOwnAccountLinksAVerifiedAccount(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.user("alice"), e.user("bob")
	jf := e.jellyfin("Home", instance.MediaServerConfig{PublicAddress: "https://jf.example.com"})
	e.grant(alice, jf)
	e.grant(bob, jf)
	aliceID := e.provider.addUser("Alice", false, false)
	adminID := e.provider.addUser("bob", true, false)
	ctx := context.Background()

	linked, err := e.svc.LinkOwnAccount(ctx, alice, jf, "alice", "Alice-pass-1")
	if err != nil {
		t.Fatal(err)
	}
	if linked.Username != "Alice" || linked.PublicAddress != "https://jf.example.com" || linked.Administrator || linked.Pending {
		t.Fatalf("linked = %+v", linked)
	}
	row := e.row(alice, jf)
	if row == nil || row.RemoteUserID != aliceID || row.CreatedByCantinarr || row.DisabledAt.Valid {
		t.Fatalf("row = %+v", row)
	}
	if e.provider.authCalls != 1 || e.provider.creates != 0 {
		t.Fatalf("auth calls = %d, creates = %d", e.provider.authCalls, e.provider.creates)
	}

	// An administrator's own account links too, and reads as one.
	linked, err = e.svc.LinkOwnAccount(ctx, bob, jf, "bob", "bob-pass-1")
	if err != nil || !linked.Administrator {
		t.Fatalf("admin link = %+v, %v", linked, err)
	}
	views, err := e.svc.ListForUser(ctx, bob)
	if err != nil || len(views) != 1 || views[0].Account == nil || !views[0].Account.Administrator {
		t.Fatalf("bob's guide = %+v, %v", views, err)
	}
	// The grant coming and going never touches the administrator account.
	e.grant(bob)
	e.svc.OnGrantsChanged([]int64{bob})
	if e.provider.user(adminID).IsDisabled {
		t.Fatal("revoking the grant disabled the administrator")
	}
	if drifted, _ := e.svc.listDriftedAccountUsers(); len(drifted) != 0 {
		t.Fatalf("the sweep still has work after an administrator revoke: %v", drifted)
	}
	// The remote id is what the row holds; the password never is.
	var stored int
	if err := e.db.QueryRow("SELECT COUNT(*) FROM user_media_server_accounts WHERE remote_user_id LIKE '%pass%' OR remote_username LIKE '%pass%'").Scan(&stored); err != nil || stored != 0 {
		t.Fatalf("a password reached the database (%d, %v)", stored, err)
	}
	if strings.Contains(e.logs.String(), "pass-1") {
		t.Fatal("a password reached the logs")
	}
}

func TestLinkOwnAccountRefusals(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol := e.user("alice"), e.user("bob"), e.user("carol")
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	plex, _ := e.inviteServer("Den Plex", instance.MediaServerConfig{})
	e.grant(alice, jf, plex)
	e.grant(bob, jf)
	e.grant(carol, jf)
	aliceID := e.provider.addUser("alice", false, false)
	e.provider.addUser("dave", false, true)
	ctx := context.Background()

	// Not granted: refused before any password travels.
	if _, err := e.svc.LinkOwnAccount(ctx, e.user("nobody"), jf, "alice", "alice-pass-1"); !errors.Is(err, ErrNotAvailable) {
		t.Fatalf("ungranted = %v, want ErrNotAvailable", err)
	}
	if _, err := e.svc.LinkOwnAccount(ctx, alice, "jellyfin-nope", "alice", "alice-pass-1"); !errors.Is(err, ErrNotAvailable) {
		t.Fatalf("unknown instance = %v, want ErrNotAvailable", err)
	}
	if e.provider.authCalls != 0 {
		t.Fatalf("refusals sent %d passwords", e.provider.authCalls)
	}
	// An invite server takes no password.
	if _, err := e.svc.LinkOwnAccount(ctx, alice, plex, "alice", "alice-pass-1"); !errors.Is(err, ErrWrongKind) {
		t.Fatalf("invite server = %v, want ErrWrongKind", err)
	}
	// Wrong password, unknown name, disabled account: no row, said apart.
	if _, err := e.svc.LinkOwnAccount(ctx, alice, jf, "alice", "not-it"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("wrong password = %v, want ErrBadCredentials", err)
	}
	if _, err := e.svc.LinkOwnAccount(ctx, alice, jf, "zed", "zed-pass-1"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("unknown name = %v, want ErrBadCredentials", err)
	}
	if _, err := e.svc.LinkOwnAccount(ctx, alice, jf, "dave", "dave-pass-1"); !errors.Is(err, ErrAccountRefused) {
		t.Fatalf("disabled account = %v, want ErrAccountRefused", err)
	}
	if e.row(alice, jf) != nil {
		t.Fatal("a refused link left a row")
	}
	// The server down: upstream, without its words.
	e.provider.authErr = errors.New("jellyfin sign-in check: host=jellyfin.internal")
	if _, err := e.svc.LinkOwnAccount(ctx, alice, jf, "alice", "alice-pass-1"); !errors.Is(err, ErrUpstream) {
		t.Fatalf("server down = %v, want ErrUpstream", err)
	}
	e.provider.authErr = nil

	// Claimed by another user's row: refused after the check, no row.
	if _, err := e.svc.LinkAccount(ctx, bob, jf, aliceID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.LinkOwnAccount(ctx, alice, jf, "alice", "alice-pass-1"); !errors.Is(err, ErrRemoteAlreadyLinked) {
		t.Fatalf("claimed remote = %v, want ErrRemoteAlreadyLinked", err)
	}
	if err := e.svc.UnlinkAccount(bob, jf); err != nil {
		t.Fatal(err)
	}

	// A live row already: refused without sending the password. A stale
	// row (the account is gone from the server) is replaced.
	if _, err := e.svc.CreateAccount(ctx, carol, jf, "carol-pass-1"); err != nil {
		t.Fatal(err)
	}
	before := e.provider.authCalls
	if _, err := e.svc.LinkOwnAccount(ctx, carol, jf, "alice", "alice-pass-1"); !errors.Is(err, ErrAccountExists) {
		t.Fatalf("live row = %v, want ErrAccountExists", err)
	}
	if e.provider.authCalls != before {
		t.Fatal("a refused link for a live row still sent the password")
	}
	if err := e.provider.DeleteUser(ctx, e.row(carol, jf).RemoteUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.LinkOwnAccount(ctx, carol, jf, "alice", "alice-pass-1"); err != nil {
		t.Fatalf("stale row = %v, want replaced", err)
	}
	if row := e.row(carol, jf); row == nil || row.RemoteUserID != aliceID || row.CreatedByCantinarr {
		t.Fatalf("row after stale replace = %+v", row)
	}
	if err := e.svc.UnlinkAccount(carol, jf); err != nil {
		t.Fatal(err)
	}

	// The grant vanishing while the server checks the password: no row.
	e.provider.onAuth = func() { e.grant(alice) }
	if _, err := e.svc.LinkOwnAccount(ctx, alice, jf, "alice", "alice-pass-1"); !errors.Is(err, ErrNotAvailable) {
		t.Fatalf("grant vanished = %v, want ErrNotAvailable", err)
	}
	if e.row(alice, jf) != nil {
		t.Fatal("a link whose grant vanished left a row")
	}
}
