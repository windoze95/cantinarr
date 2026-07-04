package instance

import (
	"bytes"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/db"
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

func TestChaptarrNeverGlobalDefault(t *testing.T) {
	s := newTestStore(t)
	inst := &Instance{ServiceType: "chaptarr", Name: "Books", URL: "http://localhost", APIKey: "key", IsDefault: true}
	if err := s.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if inst.IsDefault {
		t.Fatal("Create must normalize chaptarr IsDefault to false on the struct")
	}
	if isDefault(t, s, inst.ID) {
		t.Fatal("chaptarr instance must not be stored as default")
	}

	got, _ := s.Get(inst.ID)
	got.IsDefault = true
	if err := s.Update(got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if isDefault(t, s, inst.ID) {
		t.Fatal("Update must not store a chaptarr default flag")
	}

	// The admin/AI fallback still resolves an instance — by sort order.
	if def, err := s.GetDefault("chaptarr"); err != nil || def == nil || def.ID != inst.ID {
		t.Fatalf("GetDefault(chaptarr) = %v, %v, want fallback to %s", def, err, inst.ID)
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
	if err := s.SetUserDefault(alice, "radarr", r2); err != nil {
		t.Fatalf("SetUserDefault: %v", err)
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
