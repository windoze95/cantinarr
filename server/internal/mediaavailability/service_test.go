package mediaavailability

import (
	"context"
	"errors"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type inventoryFixture struct {
	mediaserver.Provider
	mu           sync.Mutex
	items        []mediaserver.InventoryItem
	err          error
	hook         func()
	remote       string
	libs         []string
	candidates   int
	catalogCalls atomic.Int32
	verifyCalls  atomic.Int32
	mutations    atomic.Int32
	since        time.Time
}

func (f *inventoryFixture) read() ([]mediaserver.InventoryItem, error) {
	f.mu.Lock()
	items := append([]mediaserver.InventoryItem{}, f.items...)
	err, hook := f.err, f.hook
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return items, err
}
func (f *inventoryFixture) InventoryLibraries(context.Context) ([]mediaserver.Library, error) {
	return []mediaserver.Library{{ID: "movies", CollectionType: "movies"}, {ID: "tv", CollectionType: "tvshows"}}, nil
}
func (f *inventoryFixture) Inventory(context.Context, []string, string, *mediaserver.ItemQuery) ([]mediaserver.InventoryItem, error) {
	return f.read()
}
func (f *inventoryFixture) InventorySince(_ context.Context, _ []string, since time.Time) ([]mediaserver.InventoryItem, error) {
	f.mu.Lock()
	f.since = since
	f.mu.Unlock()
	return f.read()
}
func (f *inventoryFixture) InventoryCatalog(context.Context, []string, string) ([]mediaserver.InventoryItem, error) {
	f.catalogCalls.Add(1)
	return f.read()
}
func (f *inventoryFixture) VerifyInventory(_ context.Context, libs []string, remote string, q mediaserver.ItemQuery, candidates []mediaserver.InventoryItem) ([]mediaserver.InventoryItem, error) {
	f.verifyCalls.Add(1)
	f.mu.Lock()
	f.libs = append([]string{}, libs...)
	f.remote = remote
	f.candidates = len(candidates)
	f.mu.Unlock()
	items, err := f.read()
	out := []mediaserver.InventoryItem{}
	for _, i := range items {
		if mediaserver.InventoryMatches(i, q) {
			out = append(out, i)
		}
	}
	return out, err
}
func (f *inventoryFixture) set(items []mediaserver.InventoryItem, err error, hook func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items, f.err, f.hook = items, err, hook
}
func scanFixture(t *testing.T) (*householdFixture, *inventoryFixture) {
	f := newHouseholdFixture(t)
	p := &inventoryFixture{}
	f.svc.providers = func(*instance.Instance) (mediaserver.Provider, error) { return p, nil }
	return f, p
}
func movieInventory(id string, tmdb int64) mediaserver.InventoryItem {
	return mediaserver.InventoryItem{LibraryID: "movies", ItemID: id, MediaType: "movie", TMDBID: tmdb, Movie: true}
}
func grantHousehold(t *testing.T, f *householdFixture) {
	t.Helper()
	if _, e := f.db.Exec("INSERT INTO media_availability_households(user_id,instance_id,library_ids) VALUES(?,?,?)", f.user, f.inst.ID, `["movies","tv"]`); e != nil {
		t.Fatal(e)
	}
}
func TestScanPublishesAtomicGenerationsAndOnlyFullPrunes(t *testing.T) {
	f, p := scanFixture(t)
	p.set([]mediaserver.InventoryItem{movieInventory("one", 1), movieInventory("two", 2)}, nil, nil)
	var changed atomic.Int32
	f.svc.SetChangedObserver(func() { changed.Add(1) })
	if e := f.svc.Scan(context.Background(), f.inst.ID); e != nil {
		t.Fatal(e)
	}
	p.set([]mediaserver.InventoryItem{movieInventory("partial", 3)}, errors.New("page two failed"), nil)
	if e := f.svc.Scan(context.Background(), f.inst.ID); e == nil {
		t.Fatal("incomplete scan succeeded")
	}
	if n := f.count(t, "media_availability_items"); n != 2 {
		t.Fatalf("failed scan replaced good inventory: %d", n)
	}
	status, e := f.svc.Status(f.inst.ID)
	if e != nil || status.State != "failed" || status.LastSuccessAt == nil {
		t.Fatalf("status=%+v err=%v", status, e)
	}
	p.set([]mediaserver.InventoryItem{movieInventory("three", 3)}, nil, nil)
	if e := f.svc.scan(context.Background(), f.inst.ID, true); e != nil {
		t.Fatal(e)
	}
	if n := f.count(t, "media_availability_items"); n != 3 {
		t.Fatalf("incremental inferred removal: %d", n)
	}
	if p.since.IsZero() {
		t.Fatal("incremental omitted cursor")
	}
	if e := f.svc.Scan(context.Background(), f.inst.ID); e != nil {
		t.Fatal(e)
	}
	if n := f.count(t, "media_availability_items"); n != 1 {
		t.Fatalf("full did not remove disappeared items: %d", n)
	}
	if changed.Load() != 4 {
		t.Fatalf("changes=%d", changed.Load())
	}
}
func TestScanCannotPublishAfterConnectionChangeAndOverlap(t *testing.T) {
	f, p := scanFixture(t)
	p.set([]mediaserver.InventoryItem{movieInventory("one", 1)}, nil, nil)
	if e := f.svc.Scan(context.Background(), f.inst.ID); e != nil {
		t.Fatal(e)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	p.set([]mediaserver.InventoryItem{movieInventory("stale", 2)}, nil, func() { close(entered); <-release })
	done := make(chan error, 1)
	go func() { done <- f.svc.Scan(context.Background(), f.inst.ID) }()
	<-entered
	if e := f.svc.Scan(context.Background(), f.inst.ID); !errors.Is(e, ErrScanRunning) {
		t.Fatalf("overlap=%v", e)
	}
	f.inst.URL = "http://replacement.invalid"
	if e := f.store.Update(f.inst); e != nil {
		t.Fatal(e)
	}
	close(release)
	if e := <-done; !errors.Is(e, ErrUnavailable) {
		t.Fatalf("stale publication=%v", e)
	}
	var id string
	if e := f.db.QueryRow("SELECT item_id FROM media_availability_items").Scan(&id); e != nil || id != "one" {
		t.Fatalf("stale changed generation: %q %v", id, e)
	}
}
func TestLookupHouseholdLinkedPrecedenceAndFreshChecks(t *testing.T) {
	f, p := scanFixture(t)
	grantHousehold(t, f)
	p.set([]mediaserver.InventoryItem{movieInventory("one", 1)}, nil, nil)
	if e := f.svc.Scan(context.Background(), f.inst.ID); e != nil {
		t.Fatal(e)
	}
	q := mediaserver.ItemQuery{MediaType: "movie", TMDBID: 1}
	r, e := f.svc.Lookup(context.Background(), f.user, q, false)
	if e != nil || !r.Movie || !r.Known || p.remote != "" || p.candidates != 1 || r.Sources[0].AccessMode != "household" {
		t.Fatalf("lookup=%+v err=%v", r, e)
	}
	p.set(nil, errors.New("offline"), nil)
	r, e = f.svc.Lookup(context.Background(), f.user, q, true)
	if e != nil || r.Movie || r.Known || r.Sources[0].State != "unreachable" {
		t.Fatalf("fresh reused old result: %+v %v", r, e)
	}
	p.set([]mediaserver.InventoryItem{movieInventory("one", 1)}, nil, nil)
	if _, e = f.db.Exec("INSERT INTO user_media_server_accounts(user_id,instance_id,remote_user_id,remote_username) VALUES(?,?,?,?)", f.user, f.inst.ID, "recipient", "Alice"); e != nil {
		t.Fatal(e)
	}
	r, e = f.svc.Lookup(context.Background(), f.user, q, true)
	if e != nil || r.Enabled {
		t.Fatalf("link without grant exposed source: %+v %v", r, e)
	}
	if _, e = f.db.Exec("INSERT INTO user_instance_grants(user_id,instance_id) VALUES(?,?)", f.user, f.inst.ID); e != nil {
		t.Fatal(e)
	}
	r, e = f.svc.Lookup(context.Background(), f.user, q, true)
	if e != nil || !r.Movie || p.remote != "recipient" || r.Sources[0].AccessMode != "linked_account" {
		t.Fatalf("recipient lookup=%+v %v", r, e)
	}
}
func TestLookupRechecksAuthorityAfterProviderRead(t *testing.T) {
	for _, kind := range []string{"household", "grant", "link", "connection", "scan selection"} {
		t.Run(kind, func(t *testing.T) {
			f, p := scanFixture(t)
			grantHousehold(t, f)
			if kind == "grant" || kind == "link" {
				if _, e := f.db.Exec("INSERT INTO user_media_server_accounts(user_id,instance_id,remote_user_id,remote_username) VALUES(?,?,?,?)", f.user, f.inst.ID, "remote", "Alice"); e != nil {
					t.Fatal(e)
				}
				if _, e := f.db.Exec("INSERT INTO user_instance_grants(user_id,instance_id) VALUES(?,?)", f.user, f.inst.ID); e != nil {
					t.Fatal(e)
				}
			}
			p.set([]mediaserver.InventoryItem{movieInventory("one", 1)}, nil, nil)
			if e := f.svc.Scan(context.Background(), f.inst.ID); e != nil {
				t.Fatal(e)
			}
			p.set([]mediaserver.InventoryItem{movieInventory("one", 1)}, nil, func() {
				var e error
				switch kind {
				case "household":
					_, e = f.db.Exec("DELETE FROM media_availability_households WHERE user_id=?", f.user)
				case "grant":
					_, e = f.db.Exec("DELETE FROM user_instance_grants WHERE user_id=?", f.user)
				case "link":
					_, e = f.db.Exec("UPDATE user_media_server_accounts SET remote_user_id='different' WHERE user_id=?", f.user)
				case "connection":
					f.inst.URL = "http://changed.invalid"
					e = f.store.Update(f.inst)
				case "scan selection":
					f.inst.MediaServerConfig.AvailabilityScan.LibraryIDs = []string{"tv"}
					e = f.store.Update(f.inst)
				}
				if e != nil {
					t.Error(e)
				}
			})
			r, e := f.svc.Lookup(context.Background(), f.user, mediaserver.ItemQuery{MediaType: "movie", TMDBID: 1}, true)
			if e != nil || r.Movie || r.Known || len(r.Sources) != 0 {
				t.Fatalf("stale access leaked: %+v %v", r, e)
			}
		})
	}
}
func TestCompleteCatalogProvesAbsentWithoutPerTitleCatalogWalk(t *testing.T) {
	f, p := scanFixture(t)
	grantHousehold(t, f)
	p.set(nil, nil, nil)
	for id := int64(1); id <= 4; id++ {
		r, e := f.svc.Lookup(context.Background(), f.user, mediaserver.ItemQuery{MediaType: "movie", TMDBID: id}, false)
		if e != nil || !r.Enabled || !r.Known || r.Movie {
			t.Fatalf("absence=%+v %v", r, e)
		}
	}
	if p.catalogCalls.Load() != 1 || p.verifyCalls.Load() != 0 {
		t.Fatalf("catalog=%d verify=%d", p.catalogCalls.Load(), p.verifyCalls.Load())
	}
	r, e := f.svc.Lookup(context.Background(), f.user, mediaserver.ItemQuery{MediaType: "tv", TMDBID: 9}, true)
	if e != nil || !r.Known || p.catalogCalls.Load() != 2 {
		t.Fatalf("fresh absence=%+v %v", r, e)
	}
	p.set(nil, errors.New("incomplete catalog"), nil)
	r, e = f.svc.Lookup(context.Background(), f.user, mediaserver.ItemQuery{MediaType: "tv", TMDBID: 9}, true)
	if e != nil || r.Known {
		t.Fatalf("failed catalog became absence: %+v %v", r, e)
	}
}
func TestCatalogMissesCoalesceAndDisabledScanningDoesNoIO(t *testing.T) {
	f, p := scanFixture(t)
	grantHousehold(t, f)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	p.set(nil, nil, func() { once.Do(func() { close(entered) }); <-release })
	var wg sync.WaitGroup
	for id := int64(1); id <= 8; id++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			r, e := f.svc.Lookup(context.Background(), f.user, mediaserver.ItemQuery{MediaType: "movie", TMDBID: id}, false)
			if e != nil || !r.Known {
				t.Errorf("lookup=%+v %v", r, e)
			}
		}(id)
	}
	<-entered
	close(release)
	wg.Wait()
	if p.catalogCalls.Load() != 1 {
		t.Fatalf("catalogs=%d", p.catalogCalls.Load())
	}
	f.inst.MediaServerConfig.AvailabilityScan.Enabled = false
	if e := f.store.Update(f.inst); e != nil {
		t.Fatal(e)
	}
	r, e := f.svc.Lookup(context.Background(), f.user, mediaserver.ItemQuery{MediaType: "movie", TMDBID: 1}, false)
	if e != nil || r.Enabled || p.catalogCalls.Load() != 1 {
		t.Fatalf("disabled=%+v %v", r, e)
	}
}

func TestFreshTVRefreshesAllCopiesAndRemovedMovieIsRequestable(t *testing.T) {
	f, p := scanFixture(t)
	grantHousehold(t, f)
	old := mediaserver.InventoryItem{LibraryID: "tv", ItemID: "old", MediaType: "tv", TMDBID: 10}
	p.set([]mediaserver.InventoryItem{movieInventory("movie", 1), old}, nil, nil)
	if err := f.svc.Scan(context.Background(), f.inst.ID); err != nil {
		t.Fatal(err)
	}
	newer := old
	newer.ItemID = "new"
	p.set([]mediaserver.InventoryItem{old, newer}, nil, nil)
	r, err := f.svc.Lookup(context.Background(), f.user, mediaserver.ItemQuery{MediaType: "tv", TMDBID: 10}, true)
	if err != nil || !r.Known || p.candidates != 2 || p.catalogCalls.Load() != 1 {
		t.Fatalf("fresh TV missed current copies: %+v %v candidates=%d catalog=%d", r, err, p.candidates, p.catalogCalls.Load())
	}
	r, err = f.svc.Lookup(context.Background(), f.user, mediaserver.ItemQuery{MediaType: "movie", TMDBID: 1}, true)
	if err != nil || !r.Known || r.Movie || r.Sources[0].State != "missing" {
		t.Fatalf("removed title not proven missing: %+v %v", r, err)
	}
	if p.catalogCalls.Load() != 2 {
		t.Fatal("removed indexed native ID did not refresh catalog")
	}
	if r.ScopeRevision == "" {
		t.Fatal("scope revision omitted")
	}
	before := r.ScopeRevision
	f.inst.MediaServerConfig.AvailabilityScan.LibraryIDs = []string{"movies"}
	if err = f.store.Update(f.inst); err != nil {
		t.Fatal(err)
	}
	r, err = f.svc.Lookup(context.Background(), f.user, mediaserver.ItemQuery{MediaType: "movie", TMDBID: 1}, true)
	if err != nil || r.ScopeRevision == before {
		t.Fatalf("changed selection kept accepted scope revision: %+v %v", r, err)
	}
}
func TestScanObserverSeesFinalState(t *testing.T) {
	f, p := scanFixture(t)
	states := []string{}
	f.svc.SetChangedObserver(func() {
		status, err := f.svc.Status(f.inst.ID)
		if err != nil {
			t.Error(err)
		}
		states = append(states, status.State)
	})
	if err := f.svc.Scan(context.Background(), f.inst.ID); err != nil {
		t.Fatal(err)
	}
	p.set(nil, errors.New("offline"), nil)
	if err := f.svc.Scan(context.Background(), f.inst.ID); err == nil {
		t.Fatal("failed scan succeeded")
	}
	if len(states) != 2 || states[0] != "idle" || states[1] != "failed" {
		t.Fatalf("observer raced unfinished state: %v", states)
	}
}

func (f *inventoryFixture) CreateUser(context.Context, string, string, []string) (mediaserver.RemoteUser, error) {
	f.mutations.Add(1)
	return mediaserver.RemoteUser{}, errors.New("unexpected account mutation")
}
func (f *inventoryFixture) SetLibraries(context.Context, string, []string) error {
	f.mutations.Add(1)
	return errors.New("unexpected library sharing mutation")
}
func (f *inventoryFixture) SetDisabled(context.Context, string, bool) error {
	f.mutations.Add(1)
	return errors.New("unexpected account mutation")
}
func (f *inventoryFixture) DeleteUser(context.Context, string) error {
	f.mutations.Add(1)
	return errors.New("unexpected account mutation")
}
func TestPassiveScansNeverCreateRequestsOrChangeAccounts(t *testing.T) {
	f, p := scanFixture(t)
	grantHousehold(t, f)
	now := time.Now()
	f.svc.now = func() time.Time { return now }
	show := mediaserver.InventoryItem{LibraryID: "tv", ItemID: "ongoing", MediaType: "tv", TMDBID: 10, Episodes: []mediaserver.InventoryEpisode{{SeasonNumber: 1, EpisodeNumber: 1, TMDBID: 101}}}
	p.set([]mediaserver.InventoryItem{show}, nil, nil)
	if err := f.svc.Scan(context.Background(), f.inst.ID); err != nil {
		t.Fatal(err)
	}
	show.Episodes = append(show.Episodes, mediaserver.InventoryEpisode{SeasonNumber: 1, EpisodeNumber: 2, TMDBID: 102})
	p.set([]mediaserver.InventoryItem{show, movieInventory("new-movie", 20)}, nil, nil)
	now = now.Add(6 * time.Minute)
	f.svc.Sweep(context.Background())
	now = now.Add(25 * time.Hour)
	f.svc.Sweep(context.Background())
	if count := f.count(t, "media_availability_items"); count != 2 {
		t.Fatalf("maintenance did not index new content: %d", count)
	}
	for _, table := range []string{"request_log", "request_dispatch", "request_tv_follows", "request_tv_follow_episodes", "user_media_server_accounts", "user_instance_grants"} {
		if count := f.count(t, table); count != 0 {
			t.Fatalf("passive scan mutated %s: %d", table, count)
		}
	}
	if p.mutations.Load() != 0 {
		t.Fatalf("passive scan made %d provider mutations", p.mutations.Load())
	}
}

func TestConflictingCatalogIdentityCannotProveAbsence(t *testing.T) {
	f, p := scanFixture(t)
	grantHousehold(t, f)
	p.set([]mediaserver.InventoryItem{{LibraryID: "tv", ItemID: "conflict", MediaType: "tv", TMDBID: 1, TVDBID: 99}}, nil, nil)
	result, err := f.svc.Lookup(context.Background(), f.user, mediaserver.ItemQuery{MediaType: "tv", TMDBID: 1, TVDBID: 2}, true)
	if err != nil || result.Known || len(result.Episodes) > 0 {
		t.Fatalf("conflicting title became known absence: %+v %v", result, err)
	}
}

func TestScopeRevisionTracksCurrentLocalAuthorizationWithoutProviderTraffic(t *testing.T) {
	f, _ := scanFixture(t)
	f.svc.providers = func(*instance.Instance) (mediaserver.Provider, error) {
		t.Fatal("local scope guard contacted provider")
		return nil, ErrUnavailable
	}
	scope := func() string {
		t.Helper()
		revision, err := f.svc.ScopeRevision(f.user)
		if err != nil {
			t.Fatal(err)
		}
		return revision
	}
	empty := scope()
	grantHousehold(t, f)
	household := scope()
	if household == empty {
		t.Fatal("household authorization absent from scope")
	}
	if _, err := f.db.Exec("UPDATE media_availability_households SET library_ids=?,revision=revision+1 WHERE user_id=?", `["movies"]`, f.user); err != nil {
		t.Fatal(err)
	}
	changedHousehold := scope()
	if changedHousehold == household {
		t.Fatal("household library edit did not change scope")
	}
	f.inst.MediaServerConfig.AvailabilityScan.LibraryIDs = []string{"movies"}
	if err := f.store.Update(f.inst); err != nil {
		t.Fatal(err)
	}
	configured := scope()
	if configured == changedHousehold {
		t.Fatal("scan selection edit did not change scope")
	}
	if _, err := f.db.Exec("INSERT INTO user_media_server_accounts(user_id,instance_id,remote_user_id,remote_username) VALUES(?,?,?,?)", f.user, f.inst.ID, "remote", "Reader"); err != nil {
		t.Fatal(err)
	}
	if got := scope(); got != empty {
		t.Fatal("link without grant retained household scope")
	}
	if _, err := f.db.Exec("INSERT INTO user_instance_grants(user_id,instance_id) VALUES(?,?)", f.user, f.inst.ID); err != nil {
		t.Fatal(err)
	}
	linked := scope()
	if linked == empty || linked == configured {
		t.Fatal("linked account/grant did not establish distinct scope")
	}
	if _, err := f.db.Exec("DELETE FROM user_instance_grants WHERE user_id=? AND instance_id=?", f.user, f.inst.ID); err != nil {
		t.Fatal(err)
	}
	if got := scope(); got != empty {
		t.Fatal("revoked grant retained source scope")
	}
}
