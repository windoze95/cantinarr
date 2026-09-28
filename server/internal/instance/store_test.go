package instance

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/mediapath"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return NewStore(database, cipher)
}

// createUser inserts a row directly so the user_default_instances FK is
// satisfied (foreign_keys is ON). White-box: this test lives in package
// instance to reach the unexported db handle.
func createUser(t *testing.T, s *Store, username string) int64 {
	t.Helper()
	res, err := s.db.Exec(
		"INSERT INTO users (username, password_hash, role) VALUES (?, '', 'user')",
		username,
	)
	if err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func mkInstance(t *testing.T, s *Store, serviceType, name string) string {
	t.Helper()
	inst := &Instance{ServiceType: serviceType, Name: name, URL: "http://localhost", APIKey: "key"}
	if err := s.Create(inst); err != nil {
		t.Fatalf("create %s instance: %v", serviceType, err)
	}
	return inst.ID
}

func TestMediaPathMappingsRoundTripAndFailClosed(t *testing.T) {
	s := newTestStore(t)
	inst := &Instance{
		ServiceType:       "chaptarr",
		Name:              "Books",
		URL:               "http://localhost",
		APIKey:            "key",
		MediaDownloadMode: MediaDownloadModeMapped,
		MediaPathMappings: []mediapath.Mapping{
			{ArrPath: "/ebooks", CantinarrPath: "/media/books/ebooks"},
			{ArrPath: `Z:\Audiobooks`, CantinarrPath: "/media/books/audio"},
		},
	}
	if err := s.Create(inst); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MediaDownloadMode != MediaDownloadModeMapped || len(got.MediaPathMappings) != 2 ||
		got.MediaPathMappings[1].ArrPath != `Z:\Audiobooks` {
		t.Fatalf("stored media config = mode %q mappings %#v", got.MediaDownloadMode, got.MediaPathMappings)
	}
	listed, err := s.List("chaptarr")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || len(listed[0].MediaPathMappings) != 2 {
		t.Fatalf("listed mappings = %#v", listed)
	}

	got.MediaDownloadMode = MediaDownloadModeDisabled
	got.MediaPathMappings = nil
	if err := s.Update(got); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MediaDownloadMode != MediaDownloadModeDisabled || len(got.MediaPathMappings) != 0 {
		t.Fatalf("disabled media config = mode %q mappings %#v", got.MediaDownloadMode, got.MediaPathMappings)
	}

	if _, err := s.db.Exec(
		"UPDATE service_instances SET media_download_mode = 'mapped', media_path_mappings = 'not-json' WHERE id = ?",
		inst.ID,
	); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MediaDownloadMode != MediaDownloadModeDisabled || len(got.MediaPathMappings) != 0 {
		t.Fatalf("corrupt media config did not fail closed: %+v", got)
	}
}

func TestMediaDownloadsConfiguredUsesCurrentAccessibleRoots(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "books")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	inst := &Instance{
		ServiceType:       "chaptarr",
		MediaDownloadMode: MediaDownloadModeMapped,
		MediaPathMappings: []mediapath.Mapping{{
			ArrPath:       "/ebooks",
			CantinarrPath: target,
		}},
	}
	if !inst.MediaDownloadsConfigured([]string{root}) {
		t.Fatal("accessible in-root mapping was not advertised")
	}
	if inst.MediaDownloadsConfigured([]string{t.TempDir()}) {
		t.Fatal("mapping outside the current allowlist was advertised")
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if inst.MediaDownloadsConfigured([]string{root}) {
		t.Fatal("mapping with an unavailable target was advertised")
	}

	// The retired identity bridge advertises nothing: only explicitly saved
	// mappings count, and a stored legacy row reads back as disabled.
	legacy := &Instance{
		ServiceType:       "radarr",
		MediaDownloadMode: "identity",
	}
	if legacy.MediaDownloadsConfigured([]string{root}) {
		t.Fatal("retired identity mode was advertised")
	}

	s := newTestStore(t)
	storedID := mkInstance(t, s, "radarr", "Legacy")
	if _, err := s.db.Exec(
		"UPDATE service_instances SET media_download_mode = 'identity' WHERE id = ?",
		storedID,
	); err != nil {
		t.Fatal(err)
	}
	stored, err := s.Get(storedID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.MediaDownloadMode != MediaDownloadModeDisabled || len(stored.MediaPathMappings) != 0 {
		t.Fatalf("stored legacy identity read back as %q %+v, want disabled with no mappings",
			stored.MediaDownloadMode, stored.MediaPathMappings)
	}
}

func TestDeleteRejectsPinnedPendingBookRequests(t *testing.T) {
	s := newTestStore(t)
	uid := createUser(t, s, "pending-books")
	instanceID := mkInstance(t, s, "chaptarr", "Books")
	if grants, e := s.ListUserGrants(uid); e != nil {
		t.Fatal(e)
	} else {
		grants["chaptarr"] = append(grants["chaptarr"], instanceID)
		if e = s.SetUserGrants(uid, grants); e != nil {
			t.Fatal(e)
		}
	}
	if err := s.SetUserDefault(uid, "chaptarr", instanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, instance_id, media_type, title, status)
		 VALUES (?, 0, 'book-1', 'ebook', ?, 'book', 'Pending', 'pending')`,
		uid, instanceID,
	); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(instanceID); err == nil {
		t.Fatal("deleted instance with a pinned pending book request")
	}
	if inst, err := s.Get(instanceID); err != nil || inst == nil {
		t.Fatalf("failed atomic delete removed instance: inst=%+v err=%v", inst, err)
	}
	if allowed, err := s.UserCanAccessInstance(uid, instanceID, "chaptarr"); err != nil || !allowed {
		t.Fatalf("failed atomic delete removed grant: allowed=%v err=%v", allowed, err)
	}
	if _, err := s.db.Exec("UPDATE request_log SET status='denied' WHERE instance_id=?", instanceID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(instanceID); err != nil {
		t.Fatalf("delete after resolving pending request: %v", err)
	}
}

// AUTH-023: Proxy authorization classifies instances without decrypting secrets.
func TestLookupServiceTypeUsesServiceMetadata(t *testing.T) {
	s := newTestStore(t)
	serviceTypes := []string{
		"radarr",
		"sonarr",
		"chaptarr",
		"sabnzbd",
		"qbittorrent",
		"nzbget",
		"transmission",
		"deluge",
		"rutorrent",
		"tautulli",
		"tracearr",
		"jellyfin",
		"emby",
		"plex",
	}

	for _, serviceType := range serviceTypes {
		instanceID := mkInstance(t, s, serviceType, "Lookup "+serviceType)
		got, exists, err := s.LookupServiceType(instanceID)
		if err != nil {
			t.Fatalf("LookupServiceType(%s): %v", serviceType, err)
		}
		if !exists || got != serviceType {
			t.Fatalf("LookupServiceType(%s) = (%q, %v), want (%q, true)", serviceType, got, exists, serviceType)
		}
	}

	if got, exists, err := s.LookupServiceType("missing-instance"); err != nil || exists || got != "" {
		t.Fatalf("LookupServiceType(missing) = (%q, %v, %v), want (\"\", false, nil)", got, exists, err)
	}

	corruptID := mkInstance(t, s, "sonarr", "Undecryptable secrets")
	if _, err := s.db.Exec(
		"UPDATE service_instances SET api_key = 'enc:v1:not-valid-base64!' WHERE id = ?",
		corruptID,
	); err != nil {
		t.Fatalf("corrupt encrypted API key: %v", err)
	}
	if _, err := s.Get(corruptID); err == nil {
		t.Fatal("Get with corrupt encrypted API key unexpectedly succeeded")
	}
	if got, exists, err := s.LookupServiceType(corruptID); err != nil || !exists || got != "sonarr" {
		t.Fatalf("metadata lookup with corrupt secret = (%q, %v, %v), want (sonarr, true, nil)", got, exists, err)
	}

	if err := s.db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	if _, _, err := s.LookupServiceType(corruptID); err == nil {
		t.Fatal("LookupServiceType on closed database unexpectedly succeeded")
	}
}

// AUTH-023: requester proxy access follows the same deterministic effective instance shown in config.

func TestUserDefaultInstances(t *testing.T) {
	s := newTestStore(t)
	user := createUser(t, s, "alice")
	sonarrID := mkInstance(t, s, "sonarr", "Main Sonarr")
	chaptarrID := mkInstance(t, s, "chaptarr", "Books")

	// No override yet -> not found.
	if _, ok, err := s.GetUserDefault(user, "sonarr"); err != nil || ok {
		t.Fatalf("GetUserDefault (empty) = ok=%v err=%v, want false/nil", ok, err)
	}

	// Set + read back.
	if grants, e := s.ListUserGrants(user); e != nil {
		t.Fatal(e)
	} else {
		grants["sonarr"] = append(grants["sonarr"], sonarrID)
		if e = s.SetUserGrants(user, grants); e != nil {
			t.Fatal(e)
		}
	}
	if err := s.SetUserDefault(user, "sonarr", sonarrID); err != nil {
		t.Fatalf("SetUserDefault: %v", err)
	}
	if id, ok, err := s.GetUserDefault(user, "sonarr"); err != nil || !ok || id != sonarrID {
		t.Fatalf("GetUserDefault = (%q,%v,%v), want (%q,true,nil)", id, ok, err, sonarrID)
	}

	// A mismatched service type is rejected (the instance is sonarr).
	if err := s.SetUserDefault(user, "radarr", sonarrID); err == nil {
		t.Fatal("SetUserDefault with mismatched service_type should error")
	}
	// An unknown instance id is rejected.
	if err := s.SetUserDefault(user, "sonarr", "nope-12345678"); err == nil {
		t.Fatal("SetUserDefault with unknown instance should error")
	}

	// Chaptarr grant: the granted user has access, a different user does not.
	if grants, e := s.ListUserGrants(user); e != nil {
		t.Fatal(e)
	} else {
		grants["chaptarr"] = append(grants["chaptarr"], chaptarrID)
		if e = s.SetUserGrants(user, grants); e != nil {
			t.Fatal(e)
		}
	}
	if err := s.SetUserDefault(user, "chaptarr", chaptarrID); err != nil {
		t.Fatalf("grant chaptarr: %v", err)
	}
	if ok, err := s.UserHasInstanceAccess(user, chaptarrID); err != nil || !ok {
		t.Fatalf("granted user should have access: ok=%v err=%v", ok, err)
	}
	other := createUser(t, s, "bob")
	if ok, err := s.UserHasInstanceAccess(other, chaptarrID); err != nil || ok {
		t.Fatalf("non-granted user must NOT have access: ok=%v err=%v", ok, err)
	}

	// ListUserDefaults returns every override for the user.
	defs, err := s.ListUserDefaults(user)
	if err != nil {
		t.Fatalf("ListUserDefaults: %v", err)
	}
	if defs["sonarr"] != sonarrID || defs["chaptarr"] != chaptarrID {
		t.Fatalf("ListUserDefaults = %v, want sonarr=%s chaptarr=%s", defs, sonarrID, chaptarrID)
	}

	// Upsert: re-pinning the same service type replaces the instance.
	sonarr2 := mkInstance(t, s, "sonarr", "Second Sonarr")
	if grants, e := s.ListUserGrants(user); e != nil {
		t.Fatal(e)
	} else {
		grants["sonarr"] = append(grants["sonarr"], sonarr2)
		if e = s.SetUserGrants(user, grants); e != nil {
			t.Fatal(e)
		}
	}
	if err := s.SetUserDefault(user, "sonarr", sonarr2); err != nil {
		t.Fatalf("re-pin sonarr: %v", err)
	}
	if id, _, _ := s.GetUserDefault(user, "sonarr"); id != sonarr2 {
		t.Fatalf("upsert: GetUserDefault = %q, want %q", id, sonarr2)
	}

	// Clear reverts to no override.
	if err := s.ClearUserDefault(user, "sonarr"); err != nil {
		t.Fatalf("ClearUserDefault: %v", err)
	}
	if _, ok, _ := s.GetUserDefault(user, "sonarr"); ok {
		t.Fatal("ClearUserDefault should remove the override")
	}

	// Deleting an instance drops the per-user grant (revokes chaptarr access).
	if err := s.Delete(chaptarrID); err != nil {
		t.Fatalf("Delete instance: %v", err)
	}
	if ok, _ := s.UserHasInstanceAccess(user, chaptarrID); ok {
		t.Fatal("deleting an instance must revoke its per-user grant")
	}
}

// A grant widens a user's reachable set beside their default instead of
// replacing it, and revoking a grant never moves the default.

func TestSetInstanceGrantUsers(t *testing.T) {
	s := newTestStore(t)
	alice := createUser(t, s, "ig-alice")
	bob := createUser(t, s, "ig-bob")
	hd := mkDefaultInstance(t, s, "radarr", "Movies")
	uhd := mkInstance(t, s, "radarr", "4K Movies")

	if err := s.SetInstanceGrantUsers(uhd, []int64{alice, bob}); err != nil {
		t.Fatalf("grant 4K to both: %v", err)
	}
	if grants, e := s.ListUserGrants(alice); e != nil {
		t.Fatal(e)
	} else {
		grants["radarr"] = append(grants["radarr"], hd)
		if e = s.SetUserGrants(alice, grants); e != nil {
			t.Fatal(e)
		}
	}
	if err := s.SetUserDefault(alice, "radarr", hd); err != nil {
		t.Fatalf("pin Alice to HD: %v", err)
	}

	grants, err := s.ListTypeUserGrants("radarr")
	if err != nil {
		t.Fatalf("ListTypeUserGrants: %v", err)
	}
	if len(grants[alice]) != 2 || grants[alice][0] != uhd || len(grants[bob]) != 1 {
		t.Fatalf("ListTypeUserGrants = %v, want both users granted %s", grants, uhd)
	}

	// Dropping Bob from the list revokes only Bob's grant; Alice's grant and
	// pin survive.
	if err := s.SetInstanceGrantUsers(uhd, []int64{alice}); err != nil {
		t.Fatalf("revoke Bob: %v", err)
	}
	if ok, _ := s.UserCanAccessInstance(bob, uhd, "radarr"); ok {
		t.Fatal("revoked grant must remove access")
	}
	if ok, _ := s.UserCanAccessInstance(alice, uhd, "radarr"); !ok {
		t.Fatal("Alice must keep her grant")
	}
	if id, _, _ := s.GetUserDefault(alice, "radarr"); id != hd {
		t.Fatalf("Alice's pin moved to %q, want untouched %q", id, hd)
	}

	// ListUserGrants keys by service type and skips users with no rows.
	byType, err := s.ListUserGrants(alice)
	if err != nil {
		t.Fatalf("ListUserGrants: %v", err)
	}
	if len(byType["radarr"]) != 2 || byType["radarr"][0] != uhd {
		t.Fatalf("ListUserGrants = %v, want radarr=[%s]", byType, uhd)
	}

	// An uncheck is a real revocation even for a legacy pin-based assignment:
	// omitting a user whose only tie to this instance is a PIN clears that
	// pin too, or the library would keep granting itself.
	if grants, e := s.ListUserGrants(bob); e != nil {
		t.Fatal(e)
	} else {
		grants["radarr"] = append(grants["radarr"], uhd)
		if e = s.SetUserGrants(bob, grants); e != nil {
			t.Fatal(e)
		}
	}
	if err := s.SetUserDefault(bob, "radarr", uhd); err != nil {
		t.Fatalf("pin Bob to 4K: %v", err)
	}
	if err := s.SetInstanceGrantUsers(uhd, []int64{alice}); err != nil {
		t.Fatalf("re-save grants without Bob: %v", err)
	}
	if _, pinned, _ := s.GetUserDefault(bob, "radarr"); pinned {
		t.Fatal("unchecking a pinned user must clear their pin on this instance")
	}
	if ok, _ := s.UserCanAccessInstance(bob, uhd, "radarr"); ok {
		t.Fatal("unchecked pinned user must lose access to this instance")
	}
	// A checked user's pin survives the same save.
	if grants, e := s.ListUserGrants(alice); e != nil {
		t.Fatal(e)
	} else {
		grants["radarr"] = append(grants["radarr"], uhd)
		if e = s.SetUserGrants(alice, grants); e != nil {
			t.Fatal(e)
		}
	}
	if err := s.SetUserDefault(alice, "radarr", uhd); err != nil {
		t.Fatalf("pin Alice to 4K: %v", err)
	}
	if err := s.SetInstanceGrantUsers(uhd, []int64{alice}); err != nil {
		t.Fatalf("re-save grants with Alice: %v", err)
	}
	if id, pinned, _ := s.GetUserDefault(alice, "radarr"); !pinned || id != uhd {
		t.Fatalf("checked user's pin = (%q, %v), want kept %q", id, pinned, uhd)
	}
	if err := s.ClearUserDefault(alice, "radarr"); err != nil {
		t.Fatalf("clear Alice pin: %v", err)
	}
	if grants, e := s.ListUserGrants(alice); e != nil {
		t.Fatal(e)
	} else {
		grants["radarr"] = append(grants["radarr"], hd)
		if e = s.SetUserGrants(alice, grants); e != nil {
			t.Fatal(e)
		}
	}
	if err := s.SetUserDefault(alice, "radarr", hd); err != nil {
		t.Fatalf("re-pin Alice to HD: %v", err)
	}

	// Unknown instances are rejected; deleting an instance drops its grants.
	if err := s.SetInstanceGrantUsers("nope-12345678", []int64{alice}); err == nil {
		t.Fatal("SetInstanceGrantUsers with unknown instance should error")
	}
	if err := s.Delete(uhd); err != nil {
		t.Fatalf("Delete 4K: %v", err)
	}
	if ok, _ := s.UserHasInstanceAccess(alice, uhd); ok {
		t.Fatal("deleting an instance must revoke its grants")
	}
}

func mkDefaultInstance(t *testing.T, s *Store, serviceType, name string) string {
	t.Helper()
	inst := &Instance{ServiceType: serviceType, Name: name, URL: "http://localhost", APIKey: "key", IsDefault: true}
	if err := s.Create(inst); err != nil {
		t.Fatalf("create default %s instance: %v", serviceType, err)
	}
	return inst.ID
}

func isDefault(t *testing.T, s *Store, id string) bool {
	t.Helper()
	inst, err := s.Get(id)
	if err != nil || inst == nil {
		t.Fatalf("Get %s = %v, %v", id, inst, err)
	}
	return inst.IsDefault
}

func TestSingleDefaultPerServiceType(t *testing.T) {
	s := newTestStore(t)
	radarrA := mkDefaultInstance(t, s, "radarr", "Radarr A")
	sonarr := mkDefaultInstance(t, s, "sonarr", "Sonarr")

	// Creating a second default flips the first one off.
	radarrB := mkDefaultInstance(t, s, "radarr", "Radarr B")
	if isDefault(t, s, radarrA) {
		t.Fatal("creating Radarr B as default must clear Radarr A's flag")
	}
	if def, err := s.GetDefault("radarr"); err != nil || def == nil || def.ID != radarrB {
		t.Fatalf("GetDefault(radarr) = %v, %v, want %s", def, err, radarrB)
	}
	// A sibling service type is untouched.
	if !isDefault(t, s, sonarr) {
		t.Fatal("radarr default changes must not touch the sonarr default")
	}

	// Updating an instance to default flips the current default off.
	instA, err := s.Get(radarrA)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	instA.IsDefault = true
	// Update resolves the service type from storage, so a stale caller copy
	// must not defeat the invariant.
	instA.ServiceType = ""
	if err := s.Update(instA); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if isDefault(t, s, radarrB) {
		t.Fatal("updating Radarr A to default must clear Radarr B's flag")
	}
	if def, _ := s.GetDefault("radarr"); def == nil || def.ID != radarrA {
		t.Fatalf("GetDefault(radarr) after update = %v, want %s", def, radarrA)
	}
}

func TestSetInstanceUsers(t *testing.T) {
	s := newTestStore(t)
	alice := createUser(t, s, "alice")
	bob := createUser(t, s, "bob")
	carol := createUser(t, s, "carol")
	r1 := mkInstance(t, s, "radarr", "R1")
	r2 := mkInstance(t, s, "radarr", "R2")

	// Alice is pinned to a sibling instance; assigning R1 to others must not
	// touch her.
	if grants, e := s.ListUserGrants(alice); e != nil {
		t.Fatal(e)
	} else {
		grants["radarr"] = append(grants["radarr"], r2)
		if e = s.SetUserGrants(alice, grants); e != nil {
			t.Fatal(e)
		}
	}
	if err := s.SetUserDefault(alice, "radarr", r2); err != nil {
		t.Fatalf("SetUserDefault: %v", err)
	}
	if err := s.ChangeAssignments(r1, []int64{alice, bob, carol}, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetInstanceUsers(r1, []int64{bob, carol}); err != nil {
		t.Fatalf("SetInstanceUsers: %v", err)
	}
	pins, err := s.ListTypeUserDefaults("radarr")
	if err != nil {
		t.Fatalf("ListTypeUserDefaults: %v", err)
	}
	if pins[alice] != r2 || pins[bob] != r1 || pins[carol] != r1 {
		t.Fatalf("pins = %v, want alice=%s bob=%s carol=%s", pins, r2, r1, r1)
	}

	// Dropping carol from the list clears her pin; alice still untouched.
	if err := s.SetInstanceUsers(r1, []int64{bob}); err != nil {
		t.Fatalf("SetInstanceUsers (shrink): %v", err)
	}
	pins, _ = s.ListTypeUserDefaults("radarr")
	if _, ok := pins[carol]; ok {
		t.Fatal("carol's pin must be cleared when she is removed from the list")
	}
	if pins[alice] != r2 || pins[bob] != r1 {
		t.Fatalf("pins = %v, want alice=%s bob=%s", pins, r2, r1)
	}

	// Listing alice moves her off the sibling instance.
	if err := s.SetInstanceUsers(r1, []int64{alice, bob}); err != nil {
		t.Fatalf("SetInstanceUsers (move): %v", err)
	}
	pins, _ = s.ListTypeUserDefaults("radarr")
	if pins[alice] != r1 {
		t.Fatalf("alice pin = %q, want moved to %s", pins[alice], r1)
	}

	// Unknown instance and unknown user are rejected.
	if err := s.SetInstanceUsers("radarr-missing", []int64{bob}); err == nil {
		t.Fatal("SetInstanceUsers with unknown instance should error")
	}
	if err := s.SetInstanceUsers(r1, []int64{999999}); err == nil {
		t.Fatal("SetInstanceUsers with unknown user should error (FK)")
	}
	// The failed call must not have wiped the existing assignments.
	pins, _ = s.ListTypeUserDefaults("radarr")
	if pins[alice] != r1 || pins[bob] != r1 {
		t.Fatalf("pins after failed call = %v, want alice/bob still on %s", pins, r1)
	}
}

func TestMediaServerConfigRoundTripAndFailClosed(t *testing.T) {
	s := newTestStore(t)
	inst := &Instance{
		ServiceType: "jellyfin", Name: "Home", URL: "http://localhost", APIKey: "key",
		MediaServerConfig: MediaServerConfig{PublicAddress: "https://jf.example.com", LibraryIDs: []string{"lib-shows", "lib-movies", "lib-shows"}},
	}
	if err := s.Create(inst); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := MediaServerConfig{PublicAddress: "https://jf.example.com", LibraryIDs: []string{"lib-movies", "lib-shows"}}
	if !reflect.DeepEqual(got.MediaServerConfig, want) || got.MediaServerConfigInvalid {
		t.Fatalf("stored config = %+v (invalid %t), want %+v", got.MediaServerConfig, got.MediaServerConfigInvalid, want)
	}
	listed, err := s.List("jellyfin")
	if err != nil || len(listed) != 1 || !reflect.DeepEqual(listed[0].MediaServerConfig, want) {
		t.Fatalf("listed config = %+v, %v", listed, err)
	}

	got.MediaServerConfig = MediaServerConfig{}
	if err := s.Update(got); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(inst.ID)
	if got.MediaServerConfig.PublicAddress != "" || len(got.MediaServerConfig.LibraryIDs) != 0 || got.MediaServerConfig.LibraryIDs == nil {
		t.Fatalf("cleared config = %+v, want empty with [] ids", got.MediaServerConfig)
	}

	// A document nobody can decode fails closed: zero config, flagged, still listed.
	if _, err := s.db.Exec("UPDATE service_instances SET media_server_config = 'not-json' WHERE id = ?", inst.ID); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.MediaServerConfigInvalid || got.MediaServerConfig.PublicAddress != "" || len(got.MediaServerConfig.LibraryIDs) != 0 {
		t.Fatalf("corrupt config did not fail closed: %+v invalid=%t", got.MediaServerConfig, got.MediaServerConfigInvalid)
	}
	// Re-saving repairs the row.
	got.MediaServerConfig = want
	if err := s.Update(got); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(inst.ID)
	if got.MediaServerConfigInvalid || !reflect.DeepEqual(got.MediaServerConfig, want) {
		t.Fatalf("re-save did not repair: %+v invalid=%t", got.MediaServerConfig, got.MediaServerConfigInvalid)
	}

	// Other types store '{}' whatever the struct carried.
	radarr := &Instance{ServiceType: "radarr", Name: "Movies", URL: "http://localhost", APIKey: "key",
		MediaServerConfig: MediaServerConfig{PublicAddress: "https://leak.example.com", LibraryIDs: []string{"x"}}}
	if err := s.Create(radarr); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := s.db.QueryRow("SELECT media_server_config FROM service_instances WHERE id = ?", radarr.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != "{}" {
		t.Fatalf("radarr media_server_config = %q, want {}", raw)
	}
}

// TestLidarrGrantOnlyLikeChaptarr pins the music access model: no global
// default ever persists, an ungranted user resolves to no instance at all
// (never a first-instance fallback that would leak a library), and the pin or
// grant is the entire access story.

func TestMediaServerNeverGlobalDefault(t *testing.T) {
	s := newTestStore(t)
	inst := &Instance{ServiceType: "jellyfin", Name: "Home", URL: "http://localhost", APIKey: "key", IsDefault: true}
	if err := s.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if inst.IsDefault || isDefault(t, s, inst.ID) {
		t.Fatal("jellyfin instance must not be stored as default")
	}
	got, _ := s.Get(inst.ID)
	got.IsDefault = true
	if err := s.Update(got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if isDefault(t, s, inst.ID) {
		t.Fatal("Update must not store a jellyfin default flag")
	}
}

// A media-server grant makes the instance visible to its user for the
// account guide, never proxyable, and never falls back to "the first
// instance" for anyone else.
func TestMediaServerVisibilityIsGrantOnlyAndNeverProxyable(t *testing.T) {
	s := newTestStore(t)
	alice := createUser(t, s, "ms-alice")
	bob := createUser(t, s, "ms-bob")
	home := mkInstance(t, s, "jellyfin", "Home")
	mkInstance(t, s, "jellyfin", "Other")
	if err := s.SetUserGrants(alice, map[string][]string{"jellyfin": {home}}); err != nil {
		t.Fatal(err)
	}

	visible, err := s.VisibleInstanceIDs(alice, "jellyfin")
	if err != nil || !reflect.DeepEqual(visible, []string{home}) {
		t.Fatalf("alice visible = %v, %v, want [%s]", visible, err, home)
	}
	if def, _ := s.EffectiveDefaultInstanceID(alice, "jellyfin"); def != home {
		t.Fatalf("alice effective default = %q, want %s", def, home)
	}
	if visible, _ := s.VisibleInstanceIDs(bob, "jellyfin"); len(visible) != 0 {
		t.Fatalf("ungranted bob sees %v, want nothing", visible)
	}
	if def, _ := s.EffectiveDefaultInstanceID(bob, "jellyfin"); def != "" {
		t.Fatalf("ungranted bob effective default = %q, want none", def)
	}
	if ok, _ := s.UserCanAccessInstance(alice, home, "jellyfin"); ok {
		t.Fatal("a media-server grant must never open the arr proxy")
	}
}

func TestDeleteInstanceDropsMediaServerAccounts(t *testing.T) {
	s := newTestStore(t)
	alice := createUser(t, s, "acct-alice")
	home := mkInstance(t, s, "jellyfin", "Home")
	if _, err := s.db.Exec(
		"INSERT INTO user_media_server_accounts (user_id, instance_id, remote_user_id, remote_username) VALUES (?, ?, 'r1', 'alice')",
		alice, home,
	); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(home); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM user_media_server_accounts").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("account rows after instance delete = %d, want 0", count)
	}
}
