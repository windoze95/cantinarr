package db

import (
	"path/filepath"
	"testing"
)

func TestBookCatalogRetirementPreservesBindingsApprovalsAndHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.db")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`INSERT INTO users(id,username,password_hash,role) VALUES(1,'reader','','user');
INSERT INTO request_log(tmdb_id,id,user_id,media_type,title,foreign_id,book_format,status,catalog_provider,catalog_id,match_confirmed,park_reason,book_record_id) VALUES
(0,1,1,'book','Unresolved',NULL,'ebook','pending','openlibrary','OL1W',0,'delivery',NULL),
(0,2,1,'book','Approval',NULL,'ebook','pending','openlibrary','OL2W',0,NULL,NULL),
(0,3,1,'book','Confirmed','gr:48297245','ebook','pending','openlibrary','OL3W',1,'delivery',NULL),
(0,4,1,'book','Imported','native','ebook','pending','openlibrary','OL4W',0,'delivery',7),
(0,5,1,'book','Unverified title guess','summary','ebook','pending','openlibrary','OL5W',0,'delivery',NULL),
(0,6,1,'book','History',NULL,'ebook','denied','openlibrary','OL6W',0,NULL,NULL),
(0,7,1,'book','Native','gr:7','ebook','pending',NULL,NULL,0,'delivery',NULL),
(0,8,1,'book','Accepted native import','gr:8','ebook','pending','openlibrary','OL8W',0,'author_import',NULL),
(0,9,1,'music','Album',NULL,NULL,'pending','musicbrainz','album',0,'delivery',NULL);
INSERT INTO request_dispatch(request_id,format,state) VALUES
(1,'ebook','needs_match'),(2,'ebook','approval'),(3,'ebook','queued'),(4,'ebook','queued'),(5,'ebook','retry'),(6,'ebook','cancelled'),(7,'ebook','queued'),(8,'ebook','waiting_library'),(9,'','queued');`)
	if err != nil {
		t.Fatal(err)
	}
	database.Close()
	for boot := 0; boot < 2; boot++ {
		database, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		for id, want := range map[int]string{1: "attention", 2: "attention", 3: "queued", 4: "queued", 5: "attention", 6: "cancelled", 7: "queued", 8: "waiting_library", 9: "queued"} {
			var state, code string
			if err = database.QueryRow(`SELECT state,code FROM request_dispatch WHERE request_id=?`, id).Scan(&state, &code); err != nil || state != want {
				t.Fatalf("boot %d request %d: %s %v", boot, id, state, err)
			}
			if (want == "attention") != (code == "catalog_retired") {
				t.Fatalf("unexpected retirement of %d: %s", id, code)
			}
		}
		var approval, history, count int
		database.QueryRow(`SELECT COUNT(*) FROM request_log WHERE id=2 AND status='pending' AND park_reason IS NULL AND approved_by IS NULL`).Scan(&approval)
		database.QueryRow(`SELECT COUNT(*) FROM request_log WHERE id=6 AND status='denied'`).Scan(&history)
		database.QueryRow(`SELECT COUNT(*) FROM request_log`).Scan(&count)
		if approval != 1 || history != 1 || count != 9 {
			t.Fatal("retirement changed approval or history")
		}
		database.Close()
	}
}
