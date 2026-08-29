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
}

// libraryWrite records one SetLibraries call.
type libraryWrite struct {
	remoteID   string
	libraryIDs []string
}

func newFakeProvider() *fakeProvider {
	return &fakeProvider{users: map[string]*mediaserver.RemoteUser{}}
}

func (f *fakeProvider) addUser(name string, admin, disabled bool) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	id := fmt.Sprintf("remote-%d", f.nextID)
	f.users[id] = &mediaserver.RemoteUser{ID: id, Name: name, IsAdministrator: admin, IsDisabled: disabled}
	return id
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
	e := &env{t: t, db: database, store: instance.NewStore(database, cipher), provider: newFakeProvider(), providers: map[string]mediaserver.Provider{}, logs: &bytes.Buffer{}}
	logger := slog.New(slog.NewTextHandler(e.logs, nil))
	e.svc = NewService(database, e.store, func(inst *instance.Instance) (mediaserver.Provider, error) {
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
	if err := e.store.SetUserGrants(userID, map[string][]string{"jellyfin": instanceIDs}); err != nil {
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

	// A linked administrator is never touched.
	bob := e.user("bob")
	adminID := e.provider.addUser("bob", true, false)
	if _, err := e.svc.insertAccount(accountRow{UserID: bob, InstanceID: jf, RemoteUserID: adminID, RemoteUsername: "bob"}, false); err != nil {
		t.Fatal(err)
	}
	e.svc.OnGrantsChanged([]int64{bob})
	if e.provider.user(adminID).IsDisabled {
		t.Fatal("reconcile disabled an administrator account")
	}
	if !strings.Contains(e.logs.String(), "administrator") {
		t.Fatal("skipping an administrator was not logged")
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

func TestLinkAccountRejectsAdministratorAndDuplicateRemote(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.user("alice"), e.user("bob")
	jf := e.jellyfin("Home", instance.MediaServerConfig{})
	adminID := e.provider.addUser("root", true, false)
	aliceID := e.provider.addUser("alice", false, false)
	ctx := context.Background()

	if _, err := e.svc.LinkAccount(ctx, alice, jf, adminID); !errors.Is(err, ErrAdministratorAccount) {
		t.Fatalf("link admin = %v, want ErrAdministratorAccount", err)
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
