package auth

import (
	"database/sql"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/db"
)

func TestOnboardingAssignmentsAreTransactionalAndOnlyForNewAccounts(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := database.Exec(q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec("INSERT INTO users(id,username,password_hash,role) VALUES(1,'admin','','admin')")
	for _, service := range []string{"radarr", "sonarr", "chaptarr", "lidarr"} {
		exec("INSERT INTO service_instances(id,service_type,name,url,api_key,auto_add_users) VALUES(?,?,?,'http://example','key',1)", service, service, service)
	}
	s := NewService(database, "secret", WebAuthnConfig{})
	calls := 0
	s.SetGrantAddedObserver(func(uid int64, id string) {
		var n int
		if err := database.QueryRow("SELECT COUNT(*) FROM user_instance_grants WHERE user_id=? AND instance_id=?", uid, id).Scan(&n); err != nil || n != 1 {
			t.Error("observer ran before commit")
		}
		calls++
	})
	if _, err = s.CreateConnectToken(1, "invited", "http://example"); err != nil {
		t.Fatal(err)
	}
	var uid int64
	database.QueryRow("SELECT id FROM users WHERE username='invited'").Scan(&uid)
	count := func(id int64, want int) {
		t.Helper()
		var got int
		if err := database.QueryRow("SELECT COUNT(*) FROM user_instance_grants WHERE user_id=?", id).Scan(&got); err != nil || got != want {
			t.Fatalf("grants=%d,%v want %d", got, err, want)
		}
	}
	count(uid, 4)
	if calls != 4 {
		t.Fatal(calls)
	}
	exec("DELETE FROM user_instance_grants WHERE user_id=?", uid)
	if _, err = s.CreateConnectToken(1, "invited", "http://example"); err != nil {
		t.Fatal(err)
	}
	count(uid, 0)
	if calls != 4 {
		t.Fatal("invite reissue replayed grants")
	}
	exec("UPDATE service_instances SET auto_add_users=0 WHERE id='lidarr'")
	imported, _, err := s.CreateImportUser(1, "imported", "http://example")
	if err != nil {
		t.Fatal(err)
	}
	count(imported, 3)
	if _, err = s.CreateConnectToken(9999, "rolled-back", "http://example"); err == nil {
		t.Fatal("invalid inviter accepted")
	}
	var missing int
	err = database.QueryRow("SELECT id FROM users WHERE username='rolled-back'").Scan(&missing)
	if err != sql.ErrNoRows {
		t.Fatalf("failed invite left user: %v", err)
	}
}

func seedAutomaticLibraries(t *testing.T, s *Service) {
	t.Helper()
	for _, service := range []string{"radarr", "sonarr", "chaptarr", "lidarr"} {
		for _, suffix := range []string{"one", "two"} {
			if _, err := s.db.Exec("INSERT INTO service_instances(id,service_type,name,url,api_key,auto_add_users) VALUES(?,?,?,'http://example','key',1)", service+suffix, service, suffix); err != nil {
				t.Fatal(err)
			}
		}
	}
}
func assertAutomaticLibraries(t *testing.T, s *Service, userID int64, count int) {
	t.Helper()
	var got int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM user_instance_grants g JOIN service_instances si ON si.id=g.instance_id WHERE g.user_id=? AND si.service_type IN ('radarr','sonarr','chaptarr','lidarr')", userID).Scan(&got); err != nil || got != count {
		t.Fatalf("automation grants=%d,%v want %d", got, err, count)
	}
}
func TestOIDCAutomaticAssignmentsRunOnlyOnAccountCreation(t *testing.T) {
	f := newOIDCFixture(t)
	seedAutomaticLibraries(t, f.s)
	f.update(t, func(c *OIDCConfig) { c.AutoCreate = true })
	result, err := f.complete(t, "login", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	uid := result.(*TokenResponse).User.ID
	assertAutomaticLibraries(t, f.s, uid, 8)
	if _, err = f.s.db.Exec("DELETE FROM user_instance_grants WHERE user_id=?", uid); err != nil {
		t.Fatal(err)
	}
	if _, err = f.complete(t, "login", nil, ""); err != nil {
		t.Fatal(err)
	}
	assertAutomaticLibraries(t, f.s, uid, 0)
	// Linking an existing account (including an administrator) cannot enroll it.
	f.p.claims["sub"] = "linked-admin"
	if _, err = f.complete(t, "link", f.admin, ""); err != nil {
		t.Fatal(err)
	}
	assertAutomaticLibraries(t, f.s, f.admin.UserID, 0)
}
func TestPlexAutomaticAssignmentsRunOnlyOnAccountCreation(t *testing.T) {
	f := newPlexFixture(t)
	f.config(t, true)
	f.server(t, true)
	seedAutomaticLibraries(t, f.s)
	result, err := f.complete(t)
	if err != nil {
		t.Fatal(err)
	}
	uid := result.(*TokenResponse).User.ID
	assertAutomaticLibraries(t, f.s, uid, 8)
	if _, err = f.s.db.Exec("DELETE FROM user_instance_grants WHERE user_id=? AND instance_id!='plex-test'", uid); err != nil {
		t.Fatal(err)
	}
	if _, err = f.complete(t); err != nil {
		t.Fatal(err)
	}
	assertAutomaticLibraries(t, f.s, uid, 0)
	var playback int
	if err = f.s.db.QueryRow("SELECT COUNT(*) FROM user_instance_grants WHERE user_id=? AND instance_id='plex-test'", uid).Scan(&playback); err != nil || playback != 1 {
		t.Fatalf("verified Plex access lost: %d,%v", playback, err)
	}
}
