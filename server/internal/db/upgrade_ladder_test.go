package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// TestOpenUpgradesOldestShippedSchema proves the in-code ALTER ladder end to
// end: a database created by the oldest representative shipped schema (see
// testdata/oldest_schema.sql for why that snapshot) opens with today's code,
// keeps every seeded row and value, gains the new columns and tables, and a
// second Open is a harmless no-op.
func TestOpenUpgradesOldestShippedSchema(t *testing.T) {
	// backfillLegacyPersonalCodex consults these; pin them so a developer's
	// shell exports cannot change what the ladder does.
	t.Setenv("CANTINARR_AI_PROVIDER", "")
	t.Setenv("CANTINARR_AI_MODEL", "")

	schema, err := os.ReadFile(filepath.Join("testdata", "oldest_schema.sql"))
	if err != nil {
		t.Fatalf("read oldest schema fixture: %v", err)
	}

	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("create legacy database: %v", err)
	}
	if _, err := legacy.Exec(string(schema)); err != nil {
		legacy.Close()
		t.Fatalf("apply legacy schema: %v", err)
	}
	// Seed rows exactly as the old schema allowed — no columns that did not
	// exist yet. The api key is a fake test value, not a credential.
	if _, err := legacy.Exec(`
		INSERT INTO users (id, username, password_hash, role) VALUES (1, 'admin', 'legacy-admin-hash', 'admin');
		INSERT INTO users (id, username, password_hash, role) VALUES (2, 'requester', '', 'user');
		INSERT INTO service_instances (id, service_type, name, url, api_key, is_default, sort_order)
			VALUES ('radarr-main', 'radarr', 'Radarr', 'http://radarr.local:7878', 'fake-legacy-api-key', 1, 0);
		INSERT INTO request_log (id, user_id, tmdb_id, media_type, title, status)
			VALUES (1, 2, 603, 'movie', 'The Matrix', 'requested');
		INSERT INTO settings (key, value) VALUES ('request_settings', '{"require_approval":true}');
		INSERT INTO devices (id, user_id, device_name) VALUES ('device-1', 2, 'Legacy iPhone');`,
	); err != nil {
		legacy.Close()
		t.Fatalf("seed legacy rows: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	// First Open climbs the whole ladder; the second proves it is idempotent
	// on an already-upgraded database. Assertions run after the second Open so
	// a re-run that clobbered or re-backfilled data would fail them.
	upgraded, err := Open(path)
	if err != nil {
		t.Fatalf("Open legacy database: %v", err)
	}
	if err := upgraded.Close(); err != nil {
		t.Fatalf("close after first upgrade: %v", err)
	}
	database, err := Open(path)
	if err != nil {
		t.Fatalf("re-Open upgraded database: %v", err)
	}
	defer database.Close()

	// Users survive with their values, and the one-time backfills applied:
	// password/passkey stay enabled for admins, shared AI access is
	// grandfathered for everyone who predates the grant column.
	for _, want := range []struct {
		username        string
		passwordHash    string
		role            string
		passwordEnabled bool
		passkeyEnabled  bool
	}{
		{username: "admin", passwordHash: "legacy-admin-hash", role: "admin", passwordEnabled: true, passkeyEnabled: true},
		{username: "requester", passwordHash: "", role: "user", passwordEnabled: false, passkeyEnabled: false},
	} {
		var passwordHash, role string
		var passwordEnabled, passkeyEnabled, aiShared bool
		if err := database.QueryRow(
			`SELECT password_hash, role, password_enabled, passkey_enabled, ai_shared_enabled FROM users WHERE username = ?`,
			want.username,
		).Scan(&passwordHash, &role, &passwordEnabled, &passkeyEnabled, &aiShared); err != nil {
			t.Fatalf("read upgraded user %q: %v", want.username, err)
		}
		if passwordHash != want.passwordHash || role != want.role {
			t.Fatalf("user %q = (%q, %q), want (%q, %q)", want.username, passwordHash, role, want.passwordHash, want.role)
		}
		if passwordEnabled != want.passwordEnabled || passkeyEnabled != want.passkeyEnabled {
			t.Fatalf("user %q gates = (password %t, passkey %t), want (%t, %t)",
				want.username, passwordEnabled, passkeyEnabled, want.passwordEnabled, want.passkeyEnabled)
		}
		if !aiShared {
			t.Fatalf("user %q lost the grandfathered shared AI grant", want.username)
		}
	}

	// The instance keeps its api key and default flag; the columns added by
	// the ladder exist and carry their defaults.
	var apiKey, instanceURL, instanceUsername, webhookToken string
	var isDefault bool
	if err := database.QueryRow(
		`SELECT api_key, url, username, webhook_token, is_default FROM service_instances WHERE id = 'radarr-main'`,
	).Scan(&apiKey, &instanceURL, &instanceUsername, &webhookToken, &isDefault); err != nil {
		t.Fatalf("read upgraded service instance: %v", err)
	}
	if apiKey != "fake-legacy-api-key" || instanceURL != "http://radarr.local:7878" || !isDefault {
		t.Fatalf("service instance = (%q, %q, default %t), want seeded values intact", apiKey, instanceURL, isDefault)
	}
	if instanceUsername != "" || webhookToken != "" {
		t.Fatalf("new instance columns = (username %q, webhook_token %q), want empty defaults", instanceUsername, webhookToken)
	}

	// The request row survives; the approval-era columns exist and are NULL.
	var tmdbID int
	var title, status string
	var tvdbID, approvedBy sql.NullInt64
	var foreignID sql.NullString
	if err := database.QueryRow(
		`SELECT tmdb_id, title, status, tvdb_id, foreign_id, approved_by FROM request_log WHERE id = 1`,
	).Scan(&tmdbID, &title, &status, &tvdbID, &foreignID, &approvedBy); err != nil {
		t.Fatalf("read upgraded request: %v", err)
	}
	if tmdbID != 603 || title != "The Matrix" || status != "requested" {
		t.Fatalf("request = (%d, %q, %q), want (603, \"The Matrix\", \"requested\")", tmdbID, title, status)
	}
	if tvdbID.Valid || foreignID.Valid || approvedBy.Valid {
		t.Fatalf("new request columns = (tvdb %v, foreign %v, approved_by %v), want all NULL", tvdbID, foreignID, approvedBy)
	}

	var settingValue string
	if err := database.QueryRow(`SELECT value FROM settings WHERE key = 'request_settings'`).Scan(&settingValue); err != nil {
		t.Fatalf("read upgraded setting: %v", err)
	}
	if settingValue != `{"require_approval":true}` {
		t.Fatalf("setting value = %q, want seeded JSON intact", settingValue)
	}

	var deviceName, hardwareID string
	if err := database.QueryRow(`SELECT device_name, hardware_id FROM devices WHERE id = 'device-1'`).Scan(&deviceName, &hardwareID); err != nil {
		t.Fatalf("read upgraded device: %v", err)
	}
	if deviceName != "Legacy iPhone" || hardwareID != "" {
		t.Fatalf("device = (%q, hardware %q), want (\"Legacy iPhone\", \"\")", deviceName, hardwareID)
	}

	// Nothing was invented or dropped by the two upgrade passes.
	for table, want := range map[string]int{"users": 2, "service_instances": 1, "request_log": 1, "settings": 1, "devices": 1} {
		var count int
		if err := database.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != want {
			t.Fatalf("%s row count = %d after upgrade, want %d", table, count, want)
		}
	}

	// Spot-check tables that did not exist in the old schema: created by
	// today's initSQL and empty.
	for _, table := range []string{"issues", "notification_prefs", "user_default_instances", "webauthn_credentials", "user_ai_settings"} {
		var count int
		if err := database.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatalf("new table %s missing after upgrade: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("new table %s has %d unexpected rows", table, count)
		}
	}
}
