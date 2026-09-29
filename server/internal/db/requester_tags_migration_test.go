package db

import (
	"path/filepath"
	"testing"
)

func TestRequesterTagPreviewUpgradePreservesReceipts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tags.db")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`INSERT INTO users(id,username,password_hash,role) VALUES(7,'reader','','user');
 INSERT INTO request_log(id,user_id,tmdb_id,media_type,title,status) VALUES(11,7,123,'movie','Movie','requested'),(12,7,456,'movie','Old untagged','requested');
 DROP TABLE request_tag_jobs;
 CREATE TABLE request_tag_jobs(request_id INTEGER PRIMARY KEY REFERENCES request_log(id) ON DELETE CASCADE,state TEXT NOT NULL DEFAULT 'waiting',attempts INTEGER NOT NULL DEFAULT 0,next_attempt_at INTEGER NOT NULL DEFAULT 0,lease_until INTEGER NOT NULL DEFAULT 0,lease_token TEXT NOT NULL DEFAULT '',message TEXT NOT NULL DEFAULT '',tag_label TEXT NOT NULL DEFAULT '',applied_at DATETIME,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
 INSERT INTO request_tag_jobs(request_id,state,attempts,tag_label,applied_at) VALUES(11,'applied',2,'cantinarr-7-reader','2026-09-27 12:00:00');`)
	if err != nil {
		t.Fatal(err)
	}
	database.Close()
	for boot := 0; boot < 2; boot++ {
		database, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var count, uid, attempts int
		var state, format, label string
		if err = database.QueryRow(`SELECT COUNT(*),user_id,format,state,attempts,tag_label FROM request_tag_jobs`).Scan(&count, &uid, &format, &state, &attempts, &label); err != nil {
			t.Fatal(err)
		}
		if count != 1 || uid != 7 || format != "" || state != "applied" || attempts != 2 || label != "cantinarr-7-reader" {
			t.Fatalf("receipt changed: %d %d %q %s %d %s", count, uid, format, state, attempts, label)
		}
		database.Close()
	}
}
