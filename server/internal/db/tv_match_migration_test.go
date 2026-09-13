package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestTVMatchUpgradeInvalidatesLegacyCacheOnceAndPreservesOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tv.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.Exec(`CREATE TABLE tmdb_tvdb_cache(tmdb_id INTEGER PRIMARY KEY,tvdb_id INTEGER,imdb_id TEXT,cached_at DATETIME DEFAULT CURRENT_TIMESTAMP); INSERT INTO tmdb_tvdb_cache(tmdb_id,tvdb_id) VALUES(299939,12345)`)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Close()
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	_ = database.QueryRow(`SELECT COUNT(*) FROM tmdb_tvdb_cache`).Scan(&count)
	if count != 0 {
		t.Fatal("legacy unverified cache survived")
	}
	_, err = database.Exec(`INSERT INTO tv_match_overrides(tmdb_id,mode,tvdb_id,season_map,revision) VALUES(299939,'custom',389492,'{"1":3}',7); INSERT INTO tmdb_tvdb_cache(tmdb_id,tvdb_id) VALUES(123,456)`)
	if err != nil {
		t.Fatal(err)
	}
	database.Close()
	for boot := 0; boot < 2; boot++ {
		database, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var revision int
		var mapping string
		if err = database.QueryRow(`SELECT season_map,revision FROM tv_match_overrides WHERE tmdb_id=299939`).Scan(&mapping, &revision); err != nil || mapping != `{"1":3}` || revision != 7 {
			t.Fatalf("override changed on boot %d: %s %d %v", boot, mapping, revision, err)
		}
		_ = database.QueryRow(`SELECT COUNT(*) FROM tmdb_tvdb_cache WHERE tmdb_id=123 AND tvdb_id=456`).Scan(&count)
		if count != 1 {
			t.Fatal("fresh bridge cache cleared again")
		}
		_ = database.QueryRow(`SELECT COUNT(*) FROM request_log`).Scan(&count)
		if count != 0 {
			t.Fatal("upgrade re-requested content")
		}
		database.Close()
	}
}
