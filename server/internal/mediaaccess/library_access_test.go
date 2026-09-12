package mediaaccess

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

func TestABSLibraryAccessHTTPValidationAndPermission(t *testing.T) {
	e, id, _ := absLibraryEnv(t)
	h := NewHandler(e.svc, e.svc.logger)
	changes := 0
	h.SetConfigChangedObserver(func() { changes++ })
	router := chi.NewRouter()
	router.With(auth.RequirePermission(auth.PermissionInstancesManage)).Get("/api/admin/instances/{instanceID}/media-access", h.GetLibraryAccess)
	router.With(auth.RequirePermission(auth.PermissionInstancesManage)).Put("/api/admin/instances/{instanceID}/media-access", h.UpdateLibraryAccess)
	for _, tt := range []struct {
		role, method, body string
		status             int
	}{
		{"", "GET", "", http.StatusUnauthorized},
		{auth.RoleUser, "GET", "", http.StatusForbidden},
		{auth.RoleUser, "PUT", `{"default_library_ids":[],"user_ids":[]}`, http.StatusForbidden},
		{auth.RoleAdmin, "PUT", `{}`, http.StatusBadRequest},
		{auth.RoleAdmin, "PUT", `{"default_library_ids":[],"user_ids":[],"unexpected":true}`, http.StatusBadRequest},
		{auth.RoleAdmin, "PUT", `{"default_library_ids":[],"user_ids":[],"policies":{"1":{"mode":"selected","library_ids":[]}}}`, http.StatusBadRequest},
		{auth.RoleAdmin, "PUT", `{"default_library_ids":["yana"],"user_ids":[]}`, http.StatusOK},
		{auth.RoleAdmin, "GET", "", http.StatusOK},
	} {
		req := httptest.NewRequest(tt.method, "/api/admin/instances/"+id+"/media-access", strings.NewReader(tt.body))
		if tt.role != "" {
			req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{Role: tt.role}))
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != tt.status {
			t.Fatalf("%s %s %s: %d, want %d: %s", tt.role, tt.method, tt.body, rec.Code, tt.status, rec.Body.String())
		}
		if tt.status == http.StatusOK && rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("library settings must not be cached")
		}
	}
	if changes != 1 {
		t.Fatalf("successful saves broadcast once; got %d", changes)
	}
	got, err := e.svc.LibraryAccess(id)
	if err != nil || !reflect.DeepEqual(got.DefaultLibraryIDs, []string{"yana"}) {
		t.Fatalf("saved settings: %+v, %v", got, err)
	}
}

type libraryProvider struct {
	*fakeProvider
	libraryErr error
}

func (p *libraryProvider) Libraries(context.Context) ([]mediaserver.Library, error) {
	return []mediaserver.Library{{ID: "books", Name: "Audiobooks"}, {ID: "yana", Name: "Yana"}, {ID: "shared", Name: "Shared"}}, p.libraryErr
}
func (p *libraryProvider) SetLibraries(ctx context.Context, id string, ids []string) error {
	if p.libraryErr != nil {
		return p.libraryErr
	}
	return p.fakeProvider.SetLibraries(ctx, id, ids)
}
func absLibraryEnv(t *testing.T) (*env, string, *libraryProvider) {
	e := newEnv(t)
	id := e.mediaServer("audiobookshelf", "Books", instance.MediaServerConfig{LibraryIDs: []string{"books"}})
	p := &libraryProvider{fakeProvider: e.provider}
	e.providers[id] = p
	return e, id, p
}
func selectedLibraries(ids ...string) LibraryPolicy {
	return LibraryPolicy{Mode: "selected", LibraryIDs: ids}
}
func saveLibraries(t *testing.T, e *env, id string, users []int64, defaults []string, policies map[int64]LibraryPolicy) LibraryAccessSettings {
	t.Helper()
	out, err := e.svc.SaveLibraryAccess(context.Background(), id, LibraryAccessSettings{UserIDs: users, DefaultLibraryIDs: defaults, Policies: policies})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func createABS(t *testing.T, e *env, user int64, id string) {
	t.Helper()
	if _, err := e.svc.CreateAccount(context.Background(), user, id, "long-password"); err != nil {
		t.Fatal(err)
	}
}

func TestABSLibraryChoicesBeforeAccountsAndAcrossInstances(t *testing.T) {
	e, id, _ := absLibraryEnv(t)
	julian, yana := e.user("julian"), e.user("Yana")
	saveLibraries(t, e, id, []int64{julian, yana}, []string{"books"}, map[int64]LibraryPolicy{yana: selectedLibraries("yana")})
	createABS(t, e, julian, id)
	if !reflect.DeepEqual(e.provider.lastLibraryIDs, []string{"books"}) {
		t.Fatal(e.provider.lastLibraryIDs)
	}
	createABS(t, e, yana, id)
	if !reflect.DeepEqual(e.provider.lastLibraryIDs, []string{"yana"}) {
		t.Fatal(e.provider.lastLibraryIDs)
	}
	other := e.mediaServer("audiobookshelf", "Other", instance.MediaServerConfig{LibraryIDs: []string{"shared"}})
	otherProvider := newFakeProvider()
	e.providers[other] = otherProvider
	e.grantType(yana, "audiobookshelf", id, other)
	createABS(t, e, yana, other)
	if !reflect.DeepEqual(otherProvider.lastLibraryIDs, []string{"shared"}) {
		t.Fatal(e.provider.lastLibraryIDs)
	}
	// A new service object reads durable choices; no process-local cache owns them.
	restarted := NewService(e.db, e.store, e.svc.providers, nil)
	got, err := restarted.LibraryAccess(id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Policies[yana].LibraryIDs, []string{"yana"}) {
		t.Fatal(got)
	}
}

func TestABSDefaultChangesRespectIndividualChoicesAndAllLibraries(t *testing.T) {
	e, id, _ := absLibraryEnv(t)
	a, b, c := e.user("a"), e.user("b"), e.user("c")
	users := []int64{a, b, c}
	saveLibraries(t, e, id, users, []string{"books"}, map[int64]LibraryPolicy{b: selectedLibraries("yana", "shared"), c: {Mode: "all"}})
	for _, u := range users {
		createABS(t, e, u, id)
	}
	out := saveLibraries(t, e, id, users, []string{"shared"}, nil)
	if len(e.provider.libraryWrites) != 1 || !reflect.DeepEqual(e.provider.libraryWrites[0].libraryIDs, []string{"shared"}) {
		t.Fatal(e.provider.libraryWrites)
	}
	if out.Policies[a].SyncPending {
		t.Fatal("default update still pending")
	}
	// The original instance editor/observer also resolves per-user choices.
	inst, _ := e.store.Get(id)
	inst.MediaServerConfig.LibraryIDs = []string{"books"}
	if err := e.store.Update(inst); err != nil {
		t.Fatal(err)
	}
	e.svc.OnSharedLibrariesChanged(id, []string{"books"})
	if len(e.provider.libraryWrites) != 2 {
		t.Fatal(e.provider.libraryWrites)
	}
	row, _ := e.svc.getAccount(a, id)
	for _, write := range e.provider.libraryWrites {
		if write.remoteID != row.RemoteUserID {
			t.Fatal("overrode an individual choice")
		}
	}
}

func TestABSLibraryFailureRetryRevocationAndCancellation(t *testing.T) {
	for _, action := range []string{"retry", "regrant", "stop", "unlink"} {
		t.Run(action, func(t *testing.T) {
			e, id, p := absLibraryEnv(t)
			u := e.user("reader")
			saveLibraries(t, e, id, []int64{u}, []string{"books"}, nil)
			createABS(t, e, u, id)
			p.libraryErr = errors.New("offline")
			out := saveLibraries(t, e, id, []int64{u}, []string{"books"}, map[int64]LibraryPolicy{u: selectedLibraries("yana")})
			if !out.Policies[u].SyncPending {
				t.Fatal("failed library update reported complete")
			}
			switch action {
			case "regrant":
				saveLibraries(t, e, id, []int64{}, []string{"books"}, nil)
				row, _ := e.svc.getAccount(u, id)
				if !e.provider.user(row.RemoteUserID).IsDisabled {
					t.Fatal("revoke must disable even with pending libraries")
				}
				p.libraryErr = nil
				saveLibraries(t, e, id, []int64{u}, []string{"books"}, nil)
			case "stop":
				if _, err := e.svc.SetManagement(context.Background(), u, id, false); err != nil {
					t.Fatal(err)
				}
			case "unlink":
				if err := e.svc.UnlinkAccount(u, id); err != nil {
					t.Fatal(err)
				}
			}
			p.libraryErr = nil
			e.svc.SweepAccountDrift(context.Background())
			out, err := e.svc.LibraryAccess(id)
			if err != nil {
				t.Fatal(err)
			}
			if out.Policies[u].SyncPending {
				t.Fatal("still pending")
			}
			expected := 1
			if action == "stop" || action == "unlink" {
				expected = 0
			}
			if len(e.provider.libraryWrites) != expected {
				t.Fatalf("writes=%v", e.provider.libraryWrites)
			}
			if expected == 1 && !reflect.DeepEqual(e.provider.libraryWrites[0].libraryIDs, []string{"yana"}) {
				t.Fatal(e.provider.libraryWrites)
			}
		})
	}
}

func TestABSLinkedAccountsRequireExplicitLibraryEdit(t *testing.T) {
	e, id, _ := absLibraryEnv(t)
	u := e.user("linked")
	remote := e.provider.addUser("existing", false, false)
	if _, err := e.svc.LinkAccount(context.Background(), u, id, remote); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.SaveLibraryAccess(context.Background(), id, LibraryAccessSettings{UserIDs: []int64{u}, DefaultLibraryIDs: []string{"books"}, Policies: map[int64]LibraryPolicy{u: selectedLibraries("yana")}})
	if !errors.Is(err, ErrProtectedAccount) {
		t.Fatal(err)
	}
	if _, err = e.svc.SetManagement(context.Background(), u, id, true); err != nil {
		t.Fatal(err)
	}
	saveLibraries(t, e, id, []int64{u}, []string{"shared"}, nil)
	if len(e.provider.libraryWrites) != 0 {
		t.Fatal("management/default change modified linked libraries")
	}
	out := saveLibraries(t, e, id, []int64{u}, []string{"shared"}, map[int64]LibraryPolicy{u: {Mode: "default"}})
	if len(e.provider.libraryWrites) != 1 || !out.Policies[u].ManagesLibraries {
		t.Fatal(out, e.provider.libraryWrites)
	}
	saveLibraries(t, e, id, []int64{u}, []string{"books"}, nil)
	if len(e.provider.libraryWrites) != 2 {
		t.Fatal("explicitly adopted default did not follow changes")
	}
}

func TestABSLibraryValidationIsAtomicAndNeverFallsBackToAll(t *testing.T) {
	e, id, _ := absLibraryEnv(t)
	u := e.user("a")
	saveLibraries(t, e, id, []int64{u}, []string{"books"}, nil)
	createABS(t, e, u, id)
	for _, policy := range []LibraryPolicy{{Mode: "selected"}, {Mode: "all", LibraryIDs: []string{"books"}}, {Mode: "other"}, selectedLibraries("../secret")} {
		_, err := e.svc.SaveLibraryAccess(context.Background(), id, LibraryAccessSettings{UserIDs: []int64{}, DefaultLibraryIDs: []string{}, Policies: map[int64]LibraryPolicy{u: policy}})
		if !errors.Is(err, ErrLibrarySelection) {
			t.Fatal(err)
		}
		current, _ := e.svc.LibraryAccess(id)
		if !reflect.DeepEqual(current.UserIDs, []int64{u}) || !reflect.DeepEqual(current.DefaultLibraryIDs, []string{"books"}) {
			t.Fatal(current)
		}
	}
	out := saveLibraries(t, e, id, []int64{u}, []string{"books"}, map[int64]LibraryPolicy{u: selectedLibraries("deleted-library")})
	e.svc.SweepAccountDrift(context.Background())
	if !out.Policies[u].SyncPending || len(e.provider.libraryWrites) != 0 {
		t.Fatal("unknown library became an unrestricted account")
	}
	// A promotion on ABS is authoritative even if Cantinarr still records management.
	row, _ := e.svc.getAccount(u, id)
	e.provider.users[row.RemoteUserID].IsAdministrator = true
	e.svc.SweepAccountDrift(context.Background())
	got, _ := e.svc.libraryPolicy(u, id)
	if got.SyncPending || got.remoteUserID != "" {
		t.Fatal(got)
	}
}

func TestABSLibraryUnknownUserDoesNotPartiallySave(t *testing.T) {
	e, id, _ := absLibraryEnv(t)
	u := e.user("reader")
	saveLibraries(t, e, id, []int64{u}, []string{"books"}, nil)
	_, err := e.svc.SaveLibraryAccess(context.Background(), id, LibraryAccessSettings{UserIDs: []int64{999999}, DefaultLibraryIDs: []string{}, Policies: map[int64]LibraryPolicy{u: {Mode: "all"}}})
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatal(err)
	}
	after, _ := e.svc.LibraryAccess(id)
	if len(after.Policies) != 0 || !reflect.DeepEqual(after.DefaultLibraryIDs, []string{"books"}) || !reflect.DeepEqual(after.UserIDs, []int64{u}) {
		t.Fatal(after)
	}
}

func TestABSStoppingManagementCancelsLibraryIntentAtomically(t *testing.T) {
	e, id, p := absLibraryEnv(t)
	u := e.user("reader")
	saveLibraries(t, e, id, []int64{u}, []string{"books"}, nil)
	createABS(t, e, u, id)
	p.libraryErr = errors.New("offline")
	saveLibraries(t, e, id, []int64{u}, []string{"books"}, map[int64]LibraryPolicy{u: selectedLibraries("yana")})
	// Fail the second write: the account must not appear unmanaged while an
	// old library intent remains capable of reviving on later adoption.
	if _, err := e.db.Exec(`CREATE TRIGGER fail_library_cancel BEFORE UPDATE OF sync_pending
 ON user_media_library_policies BEGIN SELECT RAISE(FAIL, 'cancel failed'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetManagement(context.Background(), u, id, false); err == nil {
		t.Fatal("failed cancellation reported success")
	}
	row, err := e.svc.getAccount(u, id)
	if err != nil || !row.ManageAccess {
		t.Fatalf("management partially saved: %+v, %v", row, err)
	}
	if _, err := e.db.Exec("DROP TRIGGER fail_library_cancel"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetManagement(context.Background(), u, id, false); err != nil {
		t.Fatal(err)
	}
	policy, err := e.svc.libraryPolicy(u, id)
	if err != nil || policy.SyncPending || policy.remoteUserID != "" {
		t.Fatalf("library intent not canceled: %+v, %v", policy, err)
	}
}
