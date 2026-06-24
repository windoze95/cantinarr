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
