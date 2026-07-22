package db

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestBookInstanceMigrationLeavesLegacyRowsUnscoped(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`
		CREATE TABLE service_instances (id TEXT PRIMARY KEY, service_type TEXT NOT NULL);
		CREATE TABLE user_default_instances (
			user_id INTEGER NOT NULL,
			service_type TEXT NOT NULL,
			instance_id TEXT NOT NULL,
			PRIMARY KEY (user_id, service_type)
		);
		CREATE TABLE request_log (
			id INTEGER PRIMARY KEY,
			user_id INTEGER,
			media_type TEXT NOT NULL
		);
		INSERT INTO service_instances VALUES ('chap-1', 'chaptarr'), ('rad-1', 'radarr');
		INSERT INTO user_default_instances VALUES (1, 'chaptarr', 'chap-1'), (2, 'radarr', 'rad-1');
		INSERT INTO request_log VALUES (1, 1, 'book'), (2, 2, 'book'), (3, 1, 'movie');
	`); err != nil {
		t.Fatal(err)
	}
	if err := applySchemaMigration(database, schemaMigration{
		alter: "ALTER TABLE request_log ADD COLUMN instance_id TEXT REFERENCES service_instances(id) ON DELETE SET NULL",
	}); err != nil {
		t.Fatal(err)
	}

	for _, id := range []int{1, 2, 3} {
		var got string
		if err := database.QueryRow("SELECT COALESCE(instance_id, '') FROM request_log WHERE id = ?", id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != "" {
			t.Fatalf("request %d instance = %q, want legacy provenance left NULL", id, got)
		}
	}
}
