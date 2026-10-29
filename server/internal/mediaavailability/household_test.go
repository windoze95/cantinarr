package mediaavailability

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

type householdFixtureProvider struct {
	mediaserver.Provider
	onLibraries func()
	err         error
}

func (f *householdFixtureProvider) InventoryLibraries(context.Context) ([]mediaserver.Library, error) {
	if f.onLibraries != nil {
		f.onLibraries()
	}
	return []mediaserver.Library{{ID: "movies", Name: "Movies", CollectionType: "movies"}, {ID: "tv", Name: "TV", CollectionType: "tvshows"}, {ID: "private", Name: "Private", CollectionType: "movies"}}, f.err
}
func (f *householdFixtureProvider) Inventory(context.Context, []string, string, *mediaserver.ItemQuery) ([]mediaserver.InventoryItem, error) {
	return nil, errors.New("association must not read titles")
}

type householdFixture struct {
	svc         *Service
	db          *sql.DB
	store       *instance.Store
	inst        *instance.Instance
	provider    *householdFixtureProvider
	user, admin int64
}

func newHouseholdFixture(t *testing.T) *householdFixture {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x51}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := instance.NewStore(database, cipher)
	inst := &instance.Instance{Name: "Household Plex", ServiceType: "plex", URL: "http://plex.invalid", APIKey: "fixture", MediaServerConfig: instance.MediaServerConfig{AvailabilityScan: &instance.AvailabilityScanConfig{Enabled: true, LibraryIDs: []string{"movies", "tv"}}}}
	if err = store.Create(inst); err != nil {
		t.Fatal(err)
	}
	newUser := func(name, role string) int64 {
		r, e := database.Exec("INSERT INTO users(username,password_hash,role) VALUES(?,'',?)", name, role)
		if e != nil {
			t.Fatal(e)
		}
		id, _ := r.LastInsertId()
		return id
	}
	f := &householdFixture{db: database, store: store, inst: inst, provider: &householdFixtureProvider{}, user: newUser("family", "user"), admin: newUser("owner", "admin")}
	f.svc = NewService(database, store, func(*instance.Instance) (mediaserver.Provider, error) { return f.provider, nil }, nil)
	return f
}

func (f *householdFixture) selection(ids ...string) []HouseholdAssociation {
	return []HouseholdAssociation{{InstanceID: f.inst.ID, LibraryIDs: ids}}
}
func (f *householdFixture) count(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestHouseholdAssociationIsExplicitLocalPermission(t *testing.T) {
	f := newHouseholdFixture(t)
	view, err := f.svc.HouseholdAccess(context.Background(), f.user)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Associations) != 0 || len(view.Servers) != 1 {
		t.Fatalf("unexpected editor: %+v", view)
	}
	if f.count(t, "media_availability_households") != 0 {
		t.Fatal("reading choices created access")
	}
	if err = f.svc.SetHouseholdAccess(context.Background(), f.user, f.selection("tv", "movies", "tv")); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.households(f.user)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0].LibraryIDs, []string{"movies", "tv"}) {
		t.Fatalf("selection=%+v", got)
	}
	for _, table := range []string{"user_instance_grants", "user_media_server_accounts", "request_log", "request_dispatch"} {
		if f.count(t, table) != 0 {
			t.Fatalf("association changed %s", table)
		}
	}
	// New scan libraries do not become household permissions.
	f.inst.MediaServerConfig.AvailabilityScan.LibraryIDs = append(f.inst.MediaServerConfig.AvailabilityScan.LibraryIDs, "private")
	if err = f.store.Update(f.inst); err != nil {
		t.Fatal(err)
	}
	view, err = f.svc.HouseholdAccess(context.Background(), f.user)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(view.Associations, got) {
		t.Fatal("scan selection expanded household access")
	}
	// An outage does not prevent retaining or removing the exact saved choice.
	f.provider.err = errors.New("offline")
	if err = f.svc.SetHouseholdAccess(context.Background(), f.user, got); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.SetHouseholdAccess(context.Background(), f.user, nil); err != nil {
		t.Fatal(err)
	}
	if f.count(t, "media_availability_households") != 0 {
		t.Fatal("explicit removal failed")
	}
}

func TestHouseholdSaveRechecksAdministratorAfterRemoteRead(t *testing.T) {
	f := newHouseholdFixture(t)
	f.provider.onLibraries = func() {
		if _, err := f.db.Exec("UPDATE users SET role='user' WHERE id=?", f.admin); err != nil {
			t.Fatal(err)
		}
	}
	err := f.svc.SaveHouseholdAccess(context.Background(), f.admin, f.user, f.selection("movies"))
	if !errors.Is(err, ErrHouseholdAdmin) || f.count(t, "media_availability_households") != 0 {
		t.Fatalf("demoted administrator saved access: %v", err)
	}
}

func TestHouseholdRejectsUnselectedLibrariesAndLinkedAccounts(t *testing.T) {
	f := newHouseholdFixture(t)
	if err := f.svc.SetHouseholdAccess(context.Background(), f.user, f.selection("private")); !errors.Is(err, ErrHouseholdSelection) {
		t.Fatalf("private selection: %v", err)
	}
	if err := f.svc.SetHouseholdAccess(context.Background(), f.user, f.selection("movies")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec("INSERT INTO user_media_server_accounts(user_id,instance_id,remote_user_id,remote_username) VALUES(?,?,?,?)", f.user, f.inst.ID, "remote", "Shared TV"); err != nil {
		t.Fatal(err)
	}
	if f.count(t, "media_availability_households") != 0 {
		t.Fatal("link did not retire household association")
	}
	if err := f.svc.SetHouseholdAccess(context.Background(), f.user, f.selection("movies")); !errors.Is(err, ErrHouseholdLinked) {
		t.Fatalf("linked selection: %v", err)
	}
	if _, err := f.db.Exec("DELETE FROM user_media_server_accounts WHERE user_id=?", f.user); err != nil {
		t.Fatal(err)
	}
	if f.count(t, "media_availability_households") != 0 {
		t.Fatal("unlink restored household access")
	}
}

func TestHouseholdSaveRejectsConcurrentConnectionOrAccountChanges(t *testing.T) {
	for _, kind := range []string{"connection", "account", "association"} {
		t.Run(kind, func(t *testing.T) {
			f := newHouseholdFixture(t)
			f.provider.onLibraries = func() {
				f.provider.onLibraries = nil
				var err error
				switch kind {
				case "connection":
					_, err = f.db.Exec("UPDATE service_instances SET url='http://changed.invalid' WHERE id=?", f.inst.ID)
				case "account":
					_, err = f.db.Exec("INSERT INTO user_media_server_accounts(user_id,instance_id,remote_user_id,remote_username) VALUES(?,?,?,?)", f.user, f.inst.ID, "remote", "Shared TV")
				case "association":
					_, err = f.db.Exec("INSERT INTO media_availability_households(user_id,instance_id,library_ids,revision) VALUES(?,?,?,1)", f.user, f.inst.ID, `["tv"]`)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			err := f.svc.SetHouseholdAccess(context.Background(), f.user, f.selection("movies"))
			if !errors.Is(err, ErrHouseholdChanged) && !errors.Is(err, ErrHouseholdLinked) {
				t.Fatalf("changed access accepted: %v", err)
			}
		})
	}
}

func TestHouseholdHTTPChecksLiveAdministratorAndRequiresExplicitArray(t *testing.T) {
	f := newHouseholdFixture(t)
	h := NewHandler(f.svc, nil)
	router := chi.NewRouter()
	router.Put("/users/{userID}/media-availability", h.Household)
	request := func(caller int64, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPut, "/users/1/media-availability", strings.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), auth.ClaimsKey, &auth.Claims{UserID: caller, Role: "admin"}))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	if w := request(f.user, `{"associations":[]}`); w.Code != http.StatusForbidden {
		t.Fatalf("stale admin claim accepted: %d %s", w.Code, w.Body.String())
	}
	for _, body := range []string{`{}`, `{"associations":null}`, `{"associations":[]} {}`, `{"associations":[],"grants":[]}`} {
		if w := request(f.admin, body); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid body %s: %d %s", body, w.Code, w.Body.String())
		}
	}
	if w := request(f.admin, `{"associations":[]}`); w.Code != http.StatusOK {
		t.Fatalf("explicit empty selection: %d %s", w.Code, w.Body.String())
	}
}
