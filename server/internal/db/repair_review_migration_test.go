package db

import (
	"path/filepath"
	"testing"
)

func TestLegacyRepairWaitsMoveToAdminsWithoutClosing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`INSERT INTO issues(id,source,status,media_type,tmdb_id,title,read,resolution,closed_at) VALUES
		(1,'user','awaiting_confirmation','movie',1,'Open wait',1,'',NULL),
		(2,'user','resolved','movie',2,'Already resolved',1,'Keep this',CURRENT_TIMESTAMP),
		(3,'user','needs_admin','movie',3,'Already assigned',1,'Admin note',NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	database.Close()
	for pass := 0; pass < 2; pass++ {
		database, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var status, resolution string
		var read, closed bool
		if err := database.QueryRow(`SELECT status,read,closed_at IS NOT NULL FROM issues WHERE id=1`).Scan(&status, &read, &closed); err != nil {
			t.Fatal(err)
		}
		if status != "needs_admin" || closed || read != (pass == 1) {
			t.Fatalf("reassigned wait: %s read=%v closed=%v", status, read, closed)
		}
		if err := database.QueryRow(`SELECT resolution FROM issues WHERE id=2`).Scan(&resolution); err != nil || resolution != "Keep this" {
			t.Fatalf("closed report changed: %q %v", resolution, err)
		}
		if err := database.QueryRow(`SELECT resolution FROM issues WHERE id=3`).Scan(&resolution); err != nil || resolution != "Admin note" {
			t.Fatalf("existing review changed: %q %v", resolution, err)
		}
		if _, err := database.Exec(`UPDATE issues SET read=1 WHERE id=1`); err != nil {
			t.Fatal(err)
		}
		database.Close()
	}
}
