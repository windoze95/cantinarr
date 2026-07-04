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
    foreign_id TEXT,
    media_type TEXT NOT NULL,
    title TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'requested',
    season_scope TEXT,
    quality_profile_id INTEGER,
    book_format TEXT,
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

-- Per-user default *arr instance override (admin-managed). A row pins which
-- instance is THIS user's default source for a service type, overriding the
-- global service_instances.is_default. For service types that have NO global
-- default (chaptarr), a row is ALSO the per-user access grant: without one the
-- user can neither see nor proxy to that instance. Absent row = inherit the
-- global default (or, for chaptarr, no access). At most one row per
-- (user, service_type). Mirrors user_request_settings (admin-managed per-user).
CREATE TABLE IF NOT EXISTS user_default_instances (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    service_type TEXT NOT NULL,
    instance_id TEXT NOT NULL,
    PRIMARY KEY (user_id, service_type)
);

CREATE TABLE IF NOT EXISTS devices (
    id TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id),
    device_name TEXT NOT NULL,
    hardware_id TEXT NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_seen_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    revoked_at DATETIME
);

CREATE TABLE IF NOT EXISTS push_tokens (
    id TEXT PRIMARY KEY,
    device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL,
    platform TEXT NOT NULL DEFAULT 'ios',
    token TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_seen_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(device_id)
);

-- Per-user push notification preferences. A missing row means "all defaults",
-- so a user only gets a row once they change something. Defaults match the
-- self-service API: request_decision off, request_pending/new_movie/new_episode
-- on. Kept separate from user_request_settings (admin-managed request policy).
CREATE TABLE IF NOT EXISTS notification_prefs (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    request_decision INTEGER NOT NULL DEFAULT 0,
    request_pending  INTEGER NOT NULL DEFAULT 1,
    new_movie        INTEGER NOT NULL DEFAULT 1,
    new_episode      INTEGER NOT NULL DEFAULT 1,
    issue_created    INTEGER NOT NULL DEFAULT 1,
    agent_action_pending INTEGER NOT NULL DEFAULT 1
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

-- AI remediation / issue reporting (Wave 1 ships issues + issue_messages; the
-- agent_* tables are created now so later waves need no migration). One row per
-- problem to work (auto-detected or user-reported). Media-scoped like
-- request_log (tmdb_id + media_type), optionally narrowed to a season/episode
-- for TV (0 = whole series/season, Overseerr's sentinel convention). The detail
-- column and any user reason are UNTRUSTED free text: stored verbatim, never
-- interpreted.
CREATE TABLE IF NOT EXISTS issues (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source TEXT NOT NULL,                       -- 'auto' | 'user'
    status TEXT NOT NULL DEFAULT 'open',        -- open|investigating|awaiting_user|awaiting_approval|resolved|wont_fix|failed|dismissed
    category TEXT,                              -- user pick: wrong_content|bad_copy|wrong_audio|other ; NULL for auto
    reporter_id INTEGER REFERENCES users(id),  -- NULL for auto-detected
    tmdb_id INTEGER NOT NULL,
    tvdb_id INTEGER,
    media_type TEXT NOT NULL,                   -- 'movie' | 'tv'
    title TEXT NOT NULL DEFAULT '',
    season_number INTEGER NOT NULL DEFAULT 0,   -- TV scope (0 = whole series / movie)
    episode_number INTEGER NOT NULL DEFAULT 0,  -- TV scope (0 = whole season / movie)
    instance_id TEXT,                          -- arr instance the fault came from (auto)
    download_id TEXT,                          -- stable download-client hash (auto); keys dedupe + doctor tools
    detail TEXT NOT NULL DEFAULT '',            -- user free-text reason OR doctor diagnosis summary (UNTRUSTED)
    dedupe_key TEXT,                           -- stable idempotency key (auto); NULL allowed (user reports)
    occurrences INTEGER NOT NULL DEFAULT 1,     -- bumped when a duplicate signal/report attaches
    active_run_id INTEGER,                      -- CAS claim: at most one running run per issue
    resolution TEXT,                           -- short closing note on a terminal state
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    closed_at DATETIME
);
-- At most one OPEN issue per dedupe_key, so the poller can INSERT ... WHERE NOT
-- EXISTS exactly like createPending. This partial unique index is the SOLE
-- idempotency guarantee for auto-dispatch.
CREATE UNIQUE INDEX IF NOT EXISTS idx_issues_open_dedupe
    ON issues(dedupe_key) WHERE dedupe_key IS NOT NULL AND closed_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_issues_status ON issues(status);

-- The user <-> agent <-> admin thread. Append-only. author_kind tags provenance
-- so agent code NEVER treats a 'user'/'system' message as an instruction. body
-- is UNTRUSTED when author_kind='user'.
CREATE TABLE IF NOT EXISTS issue_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    issue_id INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    author_kind TEXT NOT NULL,                  -- 'user' | 'agent' | 'admin' | 'system'
    author_id INTEGER REFERENCES users(id),     -- set for user/admin; NULL for agent/system
    body TEXT NOT NULL DEFAULT '',              -- UNTRUSTED when author_kind='user'
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_issue_messages_issue ON issue_messages(issue_id, id);

-- One bounded investigation of one issue (later waves). Bounds + disposition +
-- the resumable transcript live here.
CREATE TABLE IF NOT EXISTS agent_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    issue_id INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    trigger TEXT NOT NULL,                       -- 'auto'|'user_report'|'user_reply'|'approval_granted'|'approval_denied'
    status TEXT NOT NULL DEFAULT 'running',      -- running|waiting_user|waiting_approval|succeeded|gave_up|failed|aborted
    model TEXT NOT NULL DEFAULT '',
    proc_generation TEXT NOT NULL DEFAULT '',    -- process-start token; watchdog uses it to tell crashed-mid-run from parked
    step_count INTEGER NOT NULL DEFAULT 0,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    cost_micros INTEGER NOT NULL DEFAULT 0,      -- accumulated cost in millionths of a USD
    active_seconds INTEGER NOT NULL DEFAULT 0,   -- wall-clock excluding paused waits
    deadline_at DATETIME,                        -- active-work deadline; NULL while parked
    stop_reason TEXT,                            -- resolved|max_steps|max_cost|timeout|repeated_failure|awaiting_approval|awaiting_user|tool_error
    transcript_json TEXT NOT NULL DEFAULT '',    -- UNTRUNCATED provider-neutral transcript for resume (NOT the audit ledger)
    started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    finished_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_agent_runs_issue ON agent_runs(issue_id);

-- Per-step audit ledger (human-readable; truncated) for later waves. One row per
-- model turn and per tool call. NOT used to rehydrate the transcript.
CREATE TABLE IF NOT EXISTS agent_steps (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id INTEGER NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    issue_id INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    seq INTEGER NOT NULL,
    kind TEXT NOT NULL,                          -- 'assistant'|'tool_call'|'tool_result'|'system'|'giveup'
    tool_name TEXT,
    tool_use_id TEXT,                           -- links a tool_result row to its originating tool_use
    tool_input TEXT,                            -- JSON verbatim, truncated for display (UNTRUSTED if it echoes arr data)
    tool_output TEXT,                           -- JSON/text verbatim, truncated
    text TEXT,
    is_error BOOLEAN NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_agent_steps_run ON agent_steps(run_id, seq);

-- Admin-approvable proposed mutations (later waves; the heart of
-- propose->approve->execute). Idempotency: UNIQUE(fingerprint) + a CAS UPDATE
-- guarantee an action runs at most once.
CREATE TABLE IF NOT EXISTS agent_actions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    issue_id INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    run_id INTEGER REFERENCES agent_runs(id),
    tool_use_id TEXT,                           -- the propose_action tool_use.id, so the resume tool_result pairs correctly
    kind TEXT NOT NULL,                          -- grab_release|remediate_queue|manual_import|trigger_search|rescan
    params TEXT NOT NULL DEFAULT '{}',           -- JSON: the exact typed args to replay on approval
    rationale TEXT NOT NULL DEFAULT '',          -- agent's plain-language justification (UNTRUSTED — render as text)
    risk TEXT NOT NULL DEFAULT 'mutating',       -- 'mutating' (always gated) | 'safe' (auto-exec only if opted in)
    status TEXT NOT NULL DEFAULT 'proposed',     -- proposed|approved|executing|executed|denied|failed|superseded
    fingerprint TEXT NOT NULL,                   -- sha256(issue_id|kind|canonical(params)) — UNIQUE
    decided_by INTEGER REFERENCES users(id),
    decided_at DATETIME,
    deny_reason TEXT,
    executed_at DATETIME,
    result_text TEXT,                            -- execution outcome, mirrored back into agent_steps + transcript
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_actions_fingerprint ON agent_actions(fingerprint);
CREATE INDEX IF NOT EXISTS idx_agent_actions_status ON agent_actions(status);
CREATE INDEX IF NOT EXISTS idx_agent_actions_issue ON agent_actions(issue_id);
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
		{alter: "ALTER TABLE request_log ADD COLUMN book_format TEXT"},
		{alter: "ALTER TABLE request_log ADD COLUMN approved_by INTEGER REFERENCES users(id)"},
		{alter: "ALTER TABLE request_log ADD COLUMN decided_at DATETIME"},
		{alter: "ALTER TABLE request_log ADD COLUMN deny_reason TEXT"},
		// Books (Chaptarr) have no TMDB id; they are keyed by the Readarr
		// foreignBookId stored here (tmdb_id is left 0 for book rows).
		{alter: "ALTER TABLE request_log ADD COLUMN foreign_id TEXT"},
		// Stable per-device hardware id (e.g. iOS identifierForVendor) so a
		// reconnect from the same physical device updates its existing row
		// instead of creating a duplicate. Empty for rows created before this
		// column or by clients that can't provide one (e.g. web).
		{alter: "ALTER TABLE devices ADD COLUMN hardware_id TEXT NOT NULL DEFAULT ''"},
		// AI remediation: admins are notified of new issues by default (on),
		// matching request_pending. New on existing databases.
		{alter: "ALTER TABLE notification_prefs ADD COLUMN issue_created INTEGER NOT NULL DEFAULT 1"},
		// AI remediation (Wave 3): admins are notified when the agent proposes a
		// fix that needs approval, on by default. New on existing databases.
		{alter: "ALTER TABLE notification_prefs ADD COLUMN agent_action_pending INTEGER NOT NULL DEFAULT 1"},
		// Arr webhook receiver: per-instance bearer token for Sonarr/Radarr
		// Connect→Webhook callbacks. Empty = not yet issued; generated lazily
		// on first read (instance.Store.WebhookToken) and encrypted at rest.
		{alter: "ALTER TABLE service_instances ADD COLUMN webhook_token TEXT NOT NULL DEFAULT ''"},
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

	// Chaptarr has no global default — instances are granted per user — but
	// older versions let the flag be set. Zero any legacy rows so the
	// admin/AI fallback (GetDefault) resolves purely by sort order. Runs every
	// boot; idempotent and the table is tiny.
	if _, err := db.Exec(
		"UPDATE service_instances SET is_default = 0 WHERE service_type = 'chaptarr' AND is_default = 1",
	); err != nil {
		db.Close()
		return nil, fmt.Errorf("clear chaptarr default flags: %w", err)
	}

	return db, nil
}
