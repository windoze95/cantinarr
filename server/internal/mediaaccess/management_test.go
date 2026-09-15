package mediaaccess

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/instance"
)

func TestExistingAccountsAreLinkedOnlyUntilExplicitlyManaged(t *testing.T) {
	for _, kind := range []string{"jellyfin", "emby", "audiobookshelf", "plex"} {
		t.Run(kind, func(t *testing.T) {
			e := newEnv(t)
			ctx := context.Background()
			user := e.user("alice")
			id := e.mediaServer(kind, "Library", instance.MediaServerConfig{})
			remoteID := e.provider.addUser("alice", false, false)
			active := func() bool { return !e.provider.user(remoteID).IsDisabled }
			if kind == "plex" {
				p := newFakeInviteProvider()
				remoteID = "alice@example.com"
				p.share(remoteID, false)
				e.providers[id] = p
				active = func() bool { return p.has(remoteID) }
			}
			account, err := e.svc.LinkAccount(ctx, user, id, remoteID)
			if err != nil || account.ManageAccess || !account.Granted || !active() {
				t.Fatalf("default link = %+v, %v", account, err)
			}
			e.grantType(user, kind)
			e.svc.OnGrantsChanged([]int64{user})
			e.svc.SweepAccountDrift(ctx)
			if !active() || e.row(user, id).AccessSyncPending {
				t.Fatal("passive revoke or maintenance changed the remote account")
			}
			account, err = e.svc.SetManagement(ctx, user, id, true)
			if err != nil || !account.ManageAccess || account.Granted || account.AccessSyncPending || active() {
				t.Fatalf("adopt revoked account = %+v, %v", account, err)
			}
			e.grantType(user, kind, id)
			e.svc.OnGrantsChanged([]int64{user})
			if !active() {
				t.Fatal("managed regrant did not restore access")
			}
			if _, err := e.svc.SetManagement(ctx, user, id, false); err != nil {
				t.Fatal(err)
			}
			e.grantType(user, kind)
			e.svc.OnGrantsChanged([]int64{user})
			commit, release := e.svc.BeforeUserDelete(user)
			commit()
			release()
			if !active() {
				t.Fatal("stopping management did not protect remote access from revoke/delete")
			}
		})
	}
}

func TestManagementAdoptionRetriesPersistedIntentAndStopCancelsIt(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.jellyfin("Library", instance.MediaServerConfig{})
	user := e.user("alice")
	rid := e.provider.addUser("alice", false, true)
	if _, err := e.svc.LinkAccount(ctx, user, id, rid); err != nil {
		t.Fatal(err)
	}
	if !e.provider.user(rid).IsDisabled || e.provider.disables != 0 {
		t.Fatal("passive link enabled existing disabled account")
	}
	e.provider.setErr = errors.New("write failed after successful read")
	row, err := e.svc.SetManagement(ctx, user, id, true)
	if err != nil || !row.AccessSyncPending || !row.ManageAccess {
		t.Fatalf("pending adoption = %+v, %v", row, err)
	}
	// No in-memory work queue survives this restart: the DB owns the intent.
	e.svc = NewService(e.db, e.store, e.svc.providers, e.svc.logger)
	e.provider.setErr = nil
	e.svc.SweepAccountDrift(ctx)
	if e.provider.user(rid).IsDisabled || e.row(user, id).AccessSyncPending {
		t.Fatal("restart did not retry adoption")
	}
	e.provider.getErr = errors.New("offline")
	e.grant(user)
	e.svc.reconcileUser(ctx, user)
	if !e.row(user, id).AccessSyncPending {
		t.Fatal("offline revoke lost pending intent")
	}
	reads, writes := e.provider.gets, e.provider.disables
	row, err = e.svc.SetManagement(ctx, user, id, false)
	if err != nil || row.ManageAccess || row.AccessSyncPending || e.provider.gets != reads {
		t.Fatalf("offline stop = %+v, %v", row, err)
	}
	e.provider.getErr = nil
	e.svc.SweepAccountDrift(ctx)
	if e.provider.disables != writes || e.provider.user(rid).IsDisabled {
		t.Fatal("canceled retry still changed account")
	}
}

func TestManagedCreatedAccountsOnlyReceiveLibraryUpdatesAndDelete(t *testing.T) {
	for _, kind := range []string{"jellyfin", "emby", "audiobookshelf"} {
		t.Run(kind, func(t *testing.T) {
			e := newEnv(t)
			id := e.mediaServer(kind, "Library", instance.MediaServerConfig{})
			user := e.user("alice")
			e.grantType(user, kind, id)
			if _, err := e.svc.CreateAccount(context.Background(), user, id, "alice-password"); err != nil {
				t.Fatal(err)
			}
			row := e.row(user, id)
			if !row.ManageAccess || !row.CreatedByCantinarr {
				t.Fatal("new account was not managed")
			}
			e.svc.OnSharedLibrariesChanged(id, []string{"books"})
			if len(e.provider.libraryWrites) != 1 {
				t.Fatal("managed account missed library update")
			}
			if _, err := e.svc.SetManagement(context.Background(), user, id, false); err != nil {
				t.Fatal(err)
			}
			e.svc.OnSharedLibrariesChanged(id, []string{"other"})
			if len(e.provider.libraryWrites) != 1 {
				t.Fatal("unmanaged created account was rescoped")
			}
			if _, err := e.svc.SetManagement(context.Background(), user, id, true); err != nil {
				t.Fatal(err)
			}
			commit, release := e.svc.BeforeUserDelete(user)
			commit()
			release()
			if !e.provider.user(row.RemoteUserID).IsDisabled {
				t.Fatal("managed delete did not disable remote account")
			}
		})
	}
}

func TestManagementNeverRescopesAdoptedAccountsOrChangesAdministrators(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.jellyfin("Library", instance.MediaServerConfig{})
	user := e.user("alice")
	rid := e.provider.addUser("alice", false, false)
	if _, err := e.svc.LinkAccount(ctx, user, id, rid, true); err != nil {
		t.Fatal(err)
	}
	e.svc.OnSharedLibrariesChanged(id, []string{"different"})
	if len(e.provider.libraryWrites) != 0 {
		t.Fatal("adoption changed existing library policy")
	}
	e.provider.mu.Lock()
	e.provider.users[rid].IsAdministrator = true
	e.provider.mu.Unlock()
	if _, err := e.svc.SetManagement(ctx, user, id, true); !errors.Is(err, ErrProtectedAccount) {
		t.Fatalf("administrator adoption = %v", err)
	}
	e.grant(user)
	e.svc.reconcileUser(ctx, user)
	if e.provider.disables != 0 || e.row(user, id).ManageAccess || e.row(user, id).AccessSyncPending {
		t.Fatal("promoted administrator was managed")
	}
}

func TestExplicitPlexUnlinkSurvivesMaintenanceAndEmailChanges(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.mediaServer("plex", "Plex", instance.MediaServerConfig{})
	p := newFakeInviteProvider()
	e.providers[id] = p
	user := e.user("alice")
	e.grantType(user, "plex", id)
	if _, err := e.svc.RequestInvite(ctx, user, id, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	if !e.row(user, id).ManageAccess {
		t.Fatal("new share not managed")
	}
	if err := e.svc.UnlinkAccount(user, id); err != nil {
		t.Fatal(err)
	}
	e.svc.OnGrantsChanged([]int64{user})
	e.svc.OnPlexEmailShared(user, "alice")
	if outcome := e.svc.handleEmailShared(ctx, user, "alice"); outcome.userState() != "unlinked" {
		t.Fatalf("suppressed sign-in claimed access was set up: %+v", outcome)
	}
	e.svc.SweepAccountDrift(ctx)
	if e.row(user, id) != nil || p.invites != 1 || p.removals != 0 {
		t.Fatal("automatic work undid explicit unlink")
	}
	views, err := e.svc.ListForUser(ctx, user)
	if err != nil || len(views) != 1 || !views[0].AutoLinkSuppressed {
		t.Fatalf("unlink guide = %+v, %v", views, err)
	}
	// Per-server explicit linking clears suppression, adopts existing access passively.
	if _, err := e.svc.RequestInvite(ctx, user, id, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	if suppressed, _ := e.svc.autoLinkSuppressed(user, id); suppressed || e.row(user, id).ManageAccess {
		t.Fatal("explicit relink not passive or still suppressed")
	}
	e.grantType(user, "plex")
	e.svc.OnGrantsChanged([]int64{user})
	if !p.has("alice@example.com") {
		t.Fatal("relinked share was removed")
	}
}

func TestStopManagementProtectsCreatedPlexShareFromEmailAndLibraryChanges(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.mediaServer("plex", "Plex", instance.MediaServerConfig{})
	p := newFakeInviteProvider()
	e.providers[id] = p
	user := e.user("alice")
	e.grantType(user, "plex", id)
	if _, err := e.svc.RequestInvite(ctx, user, id, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetManagement(ctx, user, id, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RequestInvite(ctx, user, id, "new@example.com"); !errors.Is(err, ErrAccountExists) {
		t.Fatalf("replacement = %v", err)
	}
	e.svc.dropSharesToOtherAddresses(ctx, user, "new@example.com")
	e.svc.OnSharedLibrariesChanged(id, []string{"42"})
	if p.removals != 0 || len(p.libraryWrites) != 0 || e.row(user, id).RemoteUserID != "alice@example.com" {
		t.Fatal("stopped share was mutated")
	}
}

func TestStopManagementSerializesWithInFlightReconcile(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.jellyfin("Library", instance.MediaServerConfig{})
	user := e.user("alice")
	rid := e.provider.addUser("alice", false, false)
	if _, err := e.svc.LinkAccount(ctx, user, id, rid, true); err != nil {
		t.Fatal(err)
	}
	e.grant(user)
	entered, finish := make(chan struct{}), make(chan struct{})
	e.provider.beforeSet = func() { close(entered); <-finish }
	done := make(chan struct{})
	go func() { e.svc.reconcileUser(ctx, user); close(done) }()
	<-entered
	stopped := make(chan error, 1)
	go func() { _, err := e.svc.SetManagement(ctx, user, id, false); stopped <- err }()
	select {
	case err := <-stopped:
		t.Fatalf("stop returned while old write was in flight: %v", err)
	default:
	}
	close(finish)
	<-done
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	e.provider.beforeSet = nil
	writes := e.provider.disables
	e.grant(user, id)
	e.svc.reconcileUser(ctx, user)
	e.svc.SweepAccountDrift(ctx)
	if e.provider.disables != writes || !e.provider.user(rid).IsDisabled {
		t.Fatal("write followed completed stop")
	}
}

func TestManagementHandlerRequiresIntentAndProtectsRemoteAdmin(t *testing.T) {
	e, router := newHandlerEnv(t)
	ctx := context.Background()
	id := e.jellyfin("Library", instance.MediaServerConfig{})
	user := e.user("alice")
	rid := e.provider.addUser("root", true, false)
	if _, err := e.svc.LinkAccount(ctx, user, id, rid); err != nil {
		t.Fatal(err)
	}
	path := "/api/admin/users/" + itoa(user) + "/media-servers/" + id + "/account/management"
	for _, body := range []string{`{}`, `{"manage_access":null}`, `{"manage_access":"yes"}`, `{"manage_access":true,"other":true}`} {
		if r := serve(router, "PATCH", path, user, body); r.Code != http.StatusBadRequest {
			t.Fatalf("body %s: %d", body, r.Code)
		}
	}
	if r := serve(router, "PATCH", path, user, `{"manage_access":true}`); r.Code != http.StatusConflict {
		t.Fatalf("admin adoption: %d %s", r.Code, r.Body.String())
	}
	if r := serve(router, "PATCH", path, user, `{"manage_access":false}`); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"manage_access":false`) {
		t.Fatalf("stop: %d %s", r.Code, r.Body.String())
	}
}

func TestImportDefaultsAndSelfLinkCannotAdoptManagement(t *testing.T) {
	for _, kind := range []string{"jellyfin", "emby", "audiobookshelf"} {
		for _, manage := range []bool{false, true} {
			t.Run(kind+"/"+map[bool]string{false: "linked", true: "managed"}[manage], func(t *testing.T) {
				e := newEnv(t)
				e.svc.SetUserCreator(&fakeCreator{e: e})
				id := e.mediaServer(kind, "Library", instance.MediaServerConfig{})
				admin := e.user("admin")
				rid := e.provider.addUser("alice", false, true)
				root := e.provider.addUser("root", true, false)
				rows, err := e.svc.ImportAccounts(context.Background(), admin, id, "https://example.test", []string{rid, root}, manage)
				if err != nil || len(rows) != 2 || !rows[0].Linked || !rows[1].Linked {
					t.Fatalf("import = %+v, %v", rows, err)
				}
				if e.row(rows[0].UserID, id).ManageAccess != manage || e.provider.user(rid).IsDisabled == manage || e.row(rows[1].UserID, id).ManageAccess {
					t.Fatal("import management ignored choice or adopted admin")
				}
			})
		}
	}
	e, router := newHandlerEnv(t)
	id := e.jellyfin("Library", instance.MediaServerConfig{})
	user := e.user("alice")
	e.grant(user, id)
	e.provider.addUser("alice", false, false)
	r := serve(router, "POST", "/api/media-servers/"+id+"/account/link", user, `{"username":"alice","password":"alice-pass-1","manage_access":true}`)
	if r.Code != http.StatusCreated || e.row(user, id).ManageAccess {
		t.Fatalf("self link escalated management: %d %s", r.Code, r.Body.String())
	}
}
