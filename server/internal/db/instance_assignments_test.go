package db

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAssignmentUpgradePreservesSeparateBookAndMusicLibraries(t *testing.T) {
	for _, service := range []string{"chaptarr", "lidarr"} {
		t.Run(service, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "upgrade.db")
			database, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { database.Close() })
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := database.Exec(query, args...); err != nil {
					t.Fatal(err)
				}
			}
			// Older settings saved either explicit grants, personal default pins,
			// or both. None may be lost or expanded to an unassigned sibling.
			exec("INSERT INTO users(id,username,password_hash,role) VALUES (1,'assigned','','user'),(2,'legacy-pin','','user'),(3,'both-libraries','','user'),(4,'admin','','admin')")
			exec("INSERT INTO service_instances(id,service_type,name,url,api_key) VALUES ('mine',?,'My library','http://mine','k'),('theirs',?,'Their library','http://theirs','k')", service, service)
			exec("INSERT INTO user_instance_grants(user_id,instance_id) VALUES (1,'theirs'),(3,'mine'),(3,'theirs')")
			exec("INSERT INTO user_default_instances(user_id,service_type,instance_id) VALUES (2,?,'mine'),(3,?,'theirs'),(4,?,'mine')", service, service, service)
			exec("DELETE FROM settings WHERE key='instance_assignments_v1'")
			reopen := func() {
				t.Helper()
				if err := database.Close(); err != nil {
					t.Fatal(err)
				}
				database, err = Open(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			assignments := func() []string {
				t.Helper()
				rows, err := database.Query("SELECT user_id,instance_id FROM user_instance_grants ORDER BY user_id,instance_id")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				var pairs []string
				for rows.Next() {
					var uid int
					var id string
					if err := rows.Scan(&uid, &id); err != nil {
						t.Fatal(err)
					}
					pairs = append(pairs, fmt.Sprintf("%d:%s", uid, id))
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				return pairs
			}
			want := []string{"1:theirs", "2:mine", "3:mine", "3:theirs", "4:mine"}
			for restart := 0; restart < 2; restart++ {
				reopen()
				if got := assignments(); !reflect.DeepEqual(got, want) {
					t.Fatalf("restart %d assignments=%v, want %v", restart, got, want)
				}
			}
			exec("DELETE FROM user_instance_grants WHERE user_id=1")
			reopen()
			if got := assignments(); !reflect.DeepEqual(got, want[1:]) {
				t.Fatalf("restart restored revoked assignment: %v", got)
			}
		})
	}
}

func TestInstanceAssignmentUpgradePreservesAccessOnce(t *testing.T) {
	database, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	exec := func(q string) {
		t.Helper()
		if _, err := database.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO users(id,username,password_hash,role) VALUES(1,'inherited','','user'),(2,'pinned','','user'),(3,'admin','','admin'),(4,'broken-preference','','user')")
	exec("INSERT INTO service_instances(id,service_type,name,url,api_key) VALUES('a','radarr','A','http://a','k'),('b','radarr','B','http://b','k'),('books','chaptarr','Books','http://books','k'),('playback','plex','Playback','http://plex','k')")
	exec("INSERT INTO user_default_instances(user_id,service_type,instance_id) VALUES(2,'radarr','b'),(2,'chaptarr','books'),(2,'plex','playback'),(4,'radarr','deleted')")
	exec("INSERT INTO user_instance_grants(user_id,instance_id) VALUES (4,'a')")
	exec("INSERT INTO request_log(user_id,tmdb_id,media_type,title,status) VALUES(1,10,'movie','First','pending'),(2,11,'movie','Second','pending'),(4,12,'movie','Unknown target','pending')")
	exec("DELETE FROM settings WHERE key='instance_assignments_v1'")
	if err = migrateInstanceAssignments(database); err != nil {
		t.Fatal(err)
	}
	rows, err := database.Query("SELECT user_id,instance_id FROM user_instance_grants ORDER BY user_id,instance_id")
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64][]string{}
	for rows.Next() {
		var uid int64
		var id string
		rows.Scan(&uid, &id)
		got[uid] = append(got[uid], id)
	}
	rows.Close()
	if !reflect.DeepEqual(got, map[int64][]string{1: {"a"}, 2: {"b", "books", "playback"}, 4: {"a"}}) {
		t.Fatalf("upgraded access=%v", got)
	}
	var dest string
	database.QueryRow("SELECT instance_id FROM request_log WHERE user_id=2").Scan(&dest)
	if dest != "b" {
		t.Fatalf("pending destination=%q", dest)
	}
	var unresolved int
	if err := database.QueryRow("SELECT COUNT(*) FROM request_log WHERE user_id=4 AND instance_id IS NULL").Scan(&unresolved); err != nil || unresolved != 1 {
		t.Fatalf("unverified legacy target was guessed: %d,%v", unresolved, err)
	}

	var def, auto bool
	database.QueryRow("SELECT is_default,auto_add_users FROM service_instances WHERE id='a'").Scan(&def, &auto)
	if !def || auto {
		t.Fatalf("default=%v auto=%v", def, auto)
	}
	exec("DELETE FROM user_instance_grants WHERE user_id=1")
	exec("UPDATE service_instances SET is_default=0")
	if err = migrateInstanceAssignments(database); err != nil {
		t.Fatal(err)
	}
	var n int
	database.QueryRow("SELECT COUNT(*) FROM user_instance_grants WHERE user_id=1").Scan(&n)
	if n != 0 {
		t.Fatal("migration replayed assignment")
	}
	database.QueryRow("SELECT COUNT(*) FROM service_instances WHERE is_default=1").Scan(&n)
	if n != 0 {
		t.Fatal("migration replayed default")
	}
}

func TestAssignmentMigrationRollsBackOnFailure(t *testing.T) {
	database, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, stmt := range []string{
		"INSERT INTO users(id,username,password_hash,role) VALUES(1,'reader','','user')",
		"INSERT INTO service_instances(id,service_type,name,url,api_key) VALUES('a','radarr','Movies','http://a','k')",
		"DELETE FROM settings WHERE key='instance_assignments_v1'",
		"CREATE TRIGGER reject_assignment BEFORE INSERT ON user_instance_grants BEGIN SELECT RAISE(ABORT,'fixture failure'); END",
	} {
		if _, err = database.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err = migrateInstanceAssignments(database); err == nil {
		t.Fatal("migration ignored grant failure")
	}
	var defaults, markers int
	if err = database.QueryRow("SELECT COUNT(*) FROM service_instances WHERE is_default=1").Scan(&defaults); err != nil {
		t.Fatal(err)
	}
	if err = database.QueryRow("SELECT COUNT(*) FROM settings WHERE key='instance_assignments_v1'").Scan(&markers); err != nil {
		t.Fatal(err)
	}
	if defaults != 0 || markers != 0 {
		t.Fatalf("partial migration: defaults=%d marker=%d", defaults, markers)
	}
	if _, err = database.Exec("DROP TRIGGER reject_assignment"); err != nil {
		t.Fatal(err)
	}
	if err = migrateInstanceAssignments(database); err != nil {
		t.Fatal(err)
	}
	var grants int
	if err = database.QueryRow("SELECT COUNT(*) FROM user_instance_grants").Scan(&grants); err != nil || grants != 1 {
		t.Fatalf("retry grants=%d,%v", grants, err)
	}
}
