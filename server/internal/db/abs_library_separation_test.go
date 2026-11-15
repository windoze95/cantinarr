package db

import (
	"path/filepath"
	"testing"
)

func TestABSLibrarySeparationSchemaRoundTrip(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "abs-library-separation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err = database.Exec(`
		INSERT INTO users(id,username,password_hash,role) VALUES(41,'reader','hash','user');
		INSERT INTO service_instances(id,service_type,name,url,api_key)
			VALUES('abs-books','audiobookshelf','Books','http://abs.local:13378','test-key');
		INSERT INTO service_instances(id,service_type,name,url,api_key)
			VALUES('chaptarr-books','chaptarr','Chaptarr','http://chaptarr.local:8787','test-key');
		INSERT INTO request_log(id,user_id,tmdb_id,foreign_id,media_type,title,status,book_format,instance_id)
			VALUES(71,41,0,'book-1','book','Test Book','requested','ebook','chaptarr-books');
		INSERT INTO abs_library_separation_runs(instance_id,library_id,state,inventory_count,shared_count,requested_count,unresolved_count,plan_hash,plan_json,created_by)
			VALUES('abs-books','library-1','previewed',1,0,1,0,'sha256:test','[]',41);
		INSERT INTO abs_library_separation_items(instance_id,library_id,item_id,classification,requester_user_ids)
			VALUES('abs-books','library-1','item-1','requested','[41]');
		INSERT INTO abs_library_separation_jobs(request_id,user_id,format,instance_id)
			VALUES(71,41,'ebook','abs-books');
	`); err != nil {
		t.Fatalf("insert bridge state: %v", err)
	}
	var state, classification, requesterIDs, jobState string
	var inventory, requested, pending int
	if err = database.QueryRow(`SELECT state,inventory_count,requested_count FROM abs_library_separation_runs WHERE instance_id='abs-books' AND library_id='library-1'`).Scan(&state, &inventory, &requested); err != nil {
		t.Fatal(err)
	}
	if err = database.QueryRow(`SELECT classification,requester_user_ids FROM abs_library_separation_items WHERE item_id='item-1'`).Scan(&classification, &requesterIDs); err != nil {
		t.Fatal(err)
	}
	if err = database.QueryRow(`SELECT state FROM abs_library_separation_jobs WHERE request_id=71 AND user_id=41 AND format='ebook' AND instance_id='abs-books'`).Scan(&jobState); err != nil {
		t.Fatal(err)
	}
	if err = database.QueryRow(`SELECT COUNT(*) FROM abs_library_separation_items WHERE state='pending'`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if state != "previewed" || inventory != 1 || requested != 1 || classification != "requested" || requesterIDs != "[41]" || jobState != "waiting" || pending != 1 {
		t.Fatalf("unexpected persisted bridge state: run=(%q,%d,%d) item=(%q,%q) job=%q pending=%d", state, inventory, requested, classification, requesterIDs, jobState, pending)
	}
	if _, err = database.Exec(`INSERT INTO abs_library_separation_jobs(request_id,user_id,format,instance_id) VALUES(71,41,'ebook','abs-books')`); err == nil {
		t.Fatal("duplicate per-request bridge job was accepted")
	}
	if _, err = database.Exec(`INSERT INTO abs_library_separation_jobs(request_id,user_id,format,instance_id) VALUES(71,41,'other','abs-books')`); err == nil {
		t.Fatal("unsupported book format was accepted")
	}
}
