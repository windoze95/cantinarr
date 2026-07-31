package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestSchemaMigrationRollsBackAlterWhenBackfillFails(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE migration_probe (id INTEGER PRIMARY KEY); INSERT INTO migration_probe (id) VALUES (1)`); err != nil {
		t.Fatal(err)
	}

	err = applySchemaMigration(database, schemaMigration{
		alter:    `ALTER TABLE migration_probe ADD COLUMN migrated INTEGER NOT NULL DEFAULT 0`,
		backfill: []string{`UPDATE table_that_does_not_exist SET migrated = 1`},
	})
	if err == nil {
		t.Fatal("migration unexpectedly succeeded")
	}
	if migrationProbeHasColumn(t, database, "migrated") {
		t.Fatal("ALTER survived a failed backfill")
	}

	if err := applySchemaMigration(database, schemaMigration{
		alter:    `ALTER TABLE migration_probe ADD COLUMN migrated INTEGER NOT NULL DEFAULT 0`,
		backfill: []string{`UPDATE migration_probe SET migrated = 1`},
	}); err != nil {
		t.Fatal(err)
	}
	var migrated int
	if err := database.QueryRow(`SELECT migrated FROM migration_probe WHERE id = 1`).Scan(&migrated); err != nil {
		t.Fatal(err)
	}
	if migrated != 1 {
		t.Fatalf("migrated = %d, want 1", migrated)
	}
}

func migrationProbeHasColumn(t *testing.T, database *sql.DB, name string) bool {
	t.Helper()
	rows, err := database.Query(`PRAGMA table_info(migration_probe)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var columnName, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &columnName, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		if columnName == name {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return false
}

func TestFreshUsersDefaultToNoSharedAIGrant(t *testing.T) {
	database, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	result, err := database.Exec(`INSERT INTO users (username, password_hash, role) VALUES ('new-user', '', 'user')`)
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := result.LastInsertId()
	var enabled bool
	if err := database.QueryRow(`SELECT ai_shared_enabled FROM users WHERE id = ?`, userID).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Fatal("new user unexpectedly received shared AI access")
	}
}

func TestAIGrantMigrationPreservesGlobalAccessAndOnlyActiveCodexLinks(t *testing.T) {
	for _, tc := range []struct {
		name             string
		storedProvider   string
		storedModel      string
		envProvider      string
		envModel         string
		wantModel        string
		wantPersonalLink bool
	}{
		{name: "active codex custom model", storedProvider: "codex", storedModel: "gpt-custom", wantModel: "gpt-custom", wantPersonalLink: true},
		{name: "env-only codex", envProvider: "codex", envModel: "gpt-env", wantModel: "gpt-env", wantPersonalLink: true},
		{name: "dormant codex under openai", storedProvider: "openai", wantPersonalLink: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CANTINARR_AI_PROVIDER", tc.envProvider)
			t.Setenv("CANTINARR_AI_MODEL", tc.envModel)
			path := filepath.Join(t.TempDir(), "legacy.db")
			legacy, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = legacy.Exec(`
				CREATE TABLE users (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					username TEXT UNIQUE NOT NULL,
					password_hash TEXT NOT NULL,
					role TEXT NOT NULL DEFAULT 'user',
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP
				);
				CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
				CREATE TABLE user_codex_accounts (
					user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
					auth_blob TEXT NOT NULL,
					email TEXT NOT NULL DEFAULT '',
					plan_type TEXT NOT NULL DEFAULT '',
					rate_limits_json TEXT NOT NULL DEFAULT '',
					updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
				);
				INSERT INTO users (id, username, password_hash, role) VALUES (1, 'legacy', '', 'user');
				INSERT INTO user_codex_accounts (user_id, auth_blob) VALUES (1, 'encrypted-placeholder');`)
			if err != nil {
				legacy.Close()
				t.Fatal(err)
			}
			if tc.storedProvider != "" {
				if _, err := legacy.Exec(`INSERT INTO settings (key, value) VALUES ('ai_provider', ?)`, tc.storedProvider); err != nil {
					legacy.Close()
					t.Fatal(err)
				}
			}
			if tc.storedModel != "" {
				if _, err := legacy.Exec(`INSERT INTO settings (key, value) VALUES ('ai_model', ?)`, tc.storedModel); err != nil {
					legacy.Close()
					t.Fatal(err)
				}
			}
			if err := legacy.Close(); err != nil {
				t.Fatal(err)
			}

			database, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			var enabled bool
			if err := database.QueryRow(`SELECT ai_shared_enabled FROM users WHERE id = 1`).Scan(&enabled); err != nil {
				t.Fatal(err)
			}
			if !enabled {
				t.Fatal("legacy user did not retain shared AI access")
			}
			var count int
			if err := database.QueryRow(`SELECT COUNT(*) FROM user_ai_settings WHERE user_id = 1 AND provider = 'codex' AND model = ?`, tc.wantModel).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if got := count == 1; got != tc.wantPersonalLink {
				t.Fatalf("personal Codex backfill = %t, want %t", got, tc.wantPersonalLink)
			}
		})
	}
}
