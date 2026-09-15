package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMediaAccountManagementMigrationPreservesLegacyAndOnlyRunsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.Exec(`
CREATE TABLE user_media_server_accounts (
 user_id INTEGER NOT NULL, instance_id TEXT NOT NULL,
 remote_user_id TEXT NOT NULL, remote_username TEXT NOT NULL,
 created_by_cantinarr INTEGER NOT NULL DEFAULT 1,
 created_at DATETIME DEFAULT CURRENT_TIMESTAMP, disabled_at DATETIME,
 PRIMARY KEY (user_id, instance_id), UNIQUE(instance_id, remote_user_id));
INSERT INTO user_media_server_accounts (user_id,instance_id,remote_user_id,remote_username,created_by_cantinarr)
 VALUES (1,'jf','remote-1','linked',0);
INSERT INTO user_media_server_accounts (user_id,instance_id,remote_user_id,remote_username,created_by_cantinarr,disabled_at)
 VALUES (2,'abs','remote-2','created',1,CURRENT_TIMESTAMP);`)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Close()
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var managed int
	if err := database.QueryRow(`SELECT COUNT(*) FROM user_media_server_accounts WHERE manage_access=1 AND access_sync_pending=0`).Scan(&managed); err != nil || managed != 2 {
		t.Fatalf("legacy management = %d, %v", managed, err)
	}
	if _, err := database.Exec(`UPDATE user_media_server_accounts SET manage_access=0 WHERE user_id=1;
INSERT INTO user_media_server_accounts (user_id,instance_id,remote_user_id,remote_username) VALUES (3,'jf','new','new');`); err != nil {
		t.Fatal(err)
	}
	database.Close()
	database, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.QueryRow(`SELECT COUNT(*) FROM user_media_server_accounts WHERE manage_access=1`).Scan(&managed); err != nil || managed != 1 {
		t.Fatalf("reopen re-adopted accounts or new default unsafe: %d, %v", managed, err)
	}
}

func TestFreshMediaAccountDefaultsArePassive(t *testing.T) {
	database, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	_, err = database.Exec(`INSERT INTO users(id,username,password_hash) VALUES(1,'alice','');
INSERT INTO service_instances(id,service_type,name,url,api_key) VALUES('jf','jellyfin','Home','http://example.test','');
INSERT INTO user_media_server_accounts(user_id,instance_id,remote_user_id,remote_username) VALUES(1,'jf','1','alice');`)
	if err != nil {
		t.Fatal(err)
	}
	var managed, pending bool
	if err := database.QueryRow(`SELECT manage_access,access_sync_pending FROM user_media_server_accounts`).Scan(&managed, &pending); err != nil || managed || pending {
		t.Fatalf("fresh default: %v, %v, %v", managed, pending, err)
	}
}
