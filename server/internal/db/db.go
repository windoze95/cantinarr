package db

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const initSQL = `
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'user',
    password_enabled BOOLEAN NOT NULL DEFAULT 0,
    passkey_enabled BOOLEAN NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS request_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER REFERENCES users(id),
    tmdb_id INTEGER NOT NULL,
    tvdb_id INTEGER,
    media_type TEXT NOT NULL,
    title TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'requested',
    season_scope TEXT,
    quality_profile_id INTEGER,
    approved_by INTEGER REFERENCES users(id),
    decided_at DATETIME,
    deny_reason TEXT,
    requested_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    completed_at DATETIME
);

-- Per-user request policy overrides. Any NULL column means "inherit the
-- global default" (stored in the settings table under 'request_settings').
-- Out of the box a user has no row here, so every option inherits.
CREATE TABLE IF NOT EXISTS user_request_settings (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    require_approval BOOLEAN,
    allow_season_choice BOOLEAN,
    season_scope_override TEXT,
    allow_quality_choice BOOLEAN,
    quality_profile_radarr INTEGER,
    quality_profile_sonarr INTEGER
);

CREATE TABLE IF NOT EXISTS tmdb_tvdb_cache (
    tmdb_id INTEGER PRIMARY KEY,
    tvdb_id INTEGER,
    imdb_id TEXT,
    cached_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS service_instances (
    id TEXT PRIMARY KEY,
    service_type TEXT NOT NULL,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    api_key TEXT NOT NULL,
    username TEXT NOT NULL DEFAULT '',
    password TEXT NOT NULL DEFAULT '',
    is_default BOOLEAN DEFAULT 0,
    sort_order INTEGER DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS devices (
    id TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id),
    device_name TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_seen_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    revoked_at DATETIME
);

CREATE TABLE IF NOT EXISTS connect_tokens (
    token TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id),
    created_by INTEGER NOT NULL REFERENCES users(id),
    expires_at DATETIME NOT NULL,
    redeemed_at DATETIME
);

CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS refresh_tokens (
    token_hash TEXT PRIMARY KEY,
    device_id TEXT NOT NULL,
    user_id INTEGER NOT NULL REFERENCES users(id),
    expires_at DATETIME NOT NULL,
    superseded_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS webauthn_credentials (
    id TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    public_key BLOB NOT NULL,
    attestation_type TEXT NOT NULL,
    aaguid BLOB,
    sign_count INTEGER NOT NULL DEFAULT 0,
    backup_eligible BOOLEAN NOT NULL DEFAULT 0,
    backup_state BOOLEAN NOT NULL DEFAULT 0,
    rp_id TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT 'Passkey',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_used_at DATETIME
);

CREATE TABLE IF NOT EXISTS oauth_clients (
    client_id TEXT PRIMARY KEY,
    client_name TEXT NOT NULL DEFAULT '',
    redirect_uris TEXT NOT NULL,
    grant_types TEXT NOT NULL,
    response_types TEXT NOT NULL,
    scope TEXT NOT NULL DEFAULT 'mcp',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS oauth_authorization_codes (
    code_hash TEXT PRIMARY KEY,
    client_id TEXT NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    redirect_uri TEXT NOT NULL,
    code_challenge TEXT NOT NULL,
    resource TEXT NOT NULL,
    scope TEXT NOT NULL,
    expires_at DATETIME NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS oauth_refresh_tokens (
    token_hash TEXT PRIMARY KEY,
    client_id TEXT NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    resource TEXT NOT NULL,
    scope TEXT NOT NULL,
    expires_at DATETIME NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_used_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
`

func Open(dbPath string) (*sql.DB, error) {
	dir := filepath.Dir(dbPath)
	if dir != "." && dir != "" {
		// Parent directory should exist; caller creates it.
	}

	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db.SetMaxOpenConns(1) // SQLite is single-writer

	if _, err := db.Exec(initSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	// Additive column migrations for databases created by older versions:
	// CREATE TABLE IF NOT EXISTS does not add new columns to existing tables,
	// so each new column gets a tolerant ALTER TABLE (duplicate-column errors
	// are ignored). Backfill statements run only when the column is first added
	// so they execute exactly once per database.
	migrations := []struct {
		alter    string
		backfill []string
	}{
		{alter: "ALTER TABLE service_instances ADD COLUMN username TEXT NOT NULL DEFAULT ''"},
		{alter: "ALTER TABLE service_instances ADD COLUMN password TEXT NOT NULL DEFAULT ''"},
		{
			// Self-service password creation is admin-gated. Preserve access for
			// accounts that already rely on a password and for all admins.
			alter: "ALTER TABLE users ADD COLUMN password_enabled BOOLEAN NOT NULL DEFAULT 0",
			backfill: []string{
				"UPDATE users SET password_enabled = 1 WHERE password_hash != '' OR role = 'admin'",
			},
		},
		{
			// Passkey registration is admin-gated. Preserve access for accounts
			// that already have a passkey and for all admins.
			alter: "ALTER TABLE users ADD COLUMN passkey_enabled BOOLEAN NOT NULL DEFAULT 0",
			backfill: []string{
				"UPDATE users SET passkey_enabled = 1 WHERE role = 'admin' OR id IN (SELECT DISTINCT user_id FROM webauthn_credentials)",
			},
		},
		{alter: "ALTER TABLE refresh_tokens ADD COLUMN superseded_at DATETIME"},
		// Media-request approval queue + per-request option capture. All
		// nullable so existing rows (already fulfilled) keep their meaning.
		{alter: "ALTER TABLE request_log ADD COLUMN tvdb_id INTEGER"},
		{alter: "ALTER TABLE request_log ADD COLUMN season_scope TEXT"},
		{alter: "ALTER TABLE request_log ADD COLUMN quality_profile_id INTEGER"},
		{alter: "ALTER TABLE request_log ADD COLUMN approved_by INTEGER REFERENCES users(id)"},
		{alter: "ALTER TABLE request_log ADD COLUMN decided_at DATETIME"},
		{alter: "ALTER TABLE request_log ADD COLUMN deny_reason TEXT"},
	}
	for _, m := range migrations {
		if _, err := db.Exec(m.alter); err != nil {
			if strings.Contains(err.Error(), "duplicate column") {
				continue // already applied; skip the one-time backfill
			}
			db.Close()
			return nil, fmt.Errorf("apply migration %q: %w", m.alter, err)
		}
		for _, stmt := range m.backfill {
			if _, err := db.Exec(stmt); err != nil {
				db.Close()
				return nil, fmt.Errorf("apply backfill %q: %w", stmt, err)
			}
		}
	}

	return db, nil
}
