package db

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
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
    ai_shared_enabled BOOLEAN NOT NULL DEFAULT 0,
    plex_email TEXT NOT NULL DEFAULT '',
    plex_invited_at DATETIME,
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
    instance_id TEXT REFERENCES service_instances(id) ON DELETE SET NULL,
    approved_by INTEGER REFERENCES users(id),
    decided_at DATETIME,
    deny_reason TEXT,
    requested_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    completed_at DATETIME
);

-- Users sharing one library may subscribe to the same pending book mutation.
-- The request row retains its original owner/history; waiters receive the
-- eventual decision without inheriting another user's denial history.
CREATE TABLE IF NOT EXISTS book_request_waiters (
    request_id INTEGER NOT NULL REFERENCES request_log(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_format TEXT NOT NULL DEFAULT 'both',
    PRIMARY KEY (request_id, user_id)
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
    media_download_mode TEXT NOT NULL DEFAULT 'disabled',
    media_path_mappings TEXT NOT NULL DEFAULT '[]',
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
    agent_action_pending INTEGER NOT NULL DEFAULT 1,
    plex_access_request INTEGER NOT NULL DEFAULT 1,
    plex_invite_sent INTEGER NOT NULL DEFAULT 1
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

-- Per-user ChatGPT/Codex account state for the interactive AI assistant.
-- auth_blob is the complete Codex auth.json encrypted with Cantinarr's
-- AES-256-GCM secrets cipher. It is materialized only in a server-owned tmpfs
-- session directory during an operation, removed on normal completion, and
-- scrubbed as stale session state on the next startup after a crash.
-- Display metadata and the last rate-limit snapshot contain no usable
-- credential, but remain user-owned and disappear with the account.
CREATE TABLE IF NOT EXISTS user_codex_accounts (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    auth_blob TEXT NOT NULL,
    email TEXT NOT NULL DEFAULT '',
    plan_type TEXT NOT NULL DEFAULT '',
    rate_limits_json TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- A user's explicit AI provider override. No row means the user may use the
-- admin-funded shared provider when granted. A row is intentionally
-- fail-closed: its provider never silently falls through to shared billing.
CREATE TABLE IF NOT EXISTS user_ai_settings (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Write-only, per-user API credentials. credential_blob is always encrypted
-- with Cantinarr's AES-256-GCM secrets cipher; plaintext has no legacy format.
CREATE TABLE IF NOT EXISTS user_ai_credentials (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    credential_blob TEXT NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, provider)
);

-- One server-wide ChatGPT authorization for the admin-funded shared provider.
-- The fixed primary key enforces the singleton independently of any admin
-- account, so deleting or demoting the admin who linked it cannot orphan it.
CREATE TABLE IF NOT EXISTS shared_codex_account (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    auth_blob TEXT NOT NULL,
    email TEXT NOT NULL DEFAULT '',
    plan_type TEXT NOT NULL DEFAULT '',
    rate_limits_json TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
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
    source TEXT NOT NULL,                       -- 'auto' | 'user' | 'system'
    status TEXT NOT NULL DEFAULT 'open',        -- observing|recovering|open|investigating|awaiting_user|awaiting_approval|needs_admin|resolved|wont_fix|failed|dismissed
    category TEXT,                              -- user pick: wrong_content|bad_copy|wrong_audio|other ; NULL for auto
    reporter_id INTEGER REFERENCES users(id),  -- NULL for auto-detected
    tmdb_id INTEGER NOT NULL,
    tvdb_id INTEGER,
    media_type TEXT NOT NULL,                   -- 'movie' | 'tv' | 'book' | 'system'
    title TEXT NOT NULL DEFAULT '',
    season_number INTEGER NOT NULL DEFAULT 0,   -- TV scope (0 = whole series unless episode_number > 0, which means Specials)
    episode_number INTEGER NOT NULL DEFAULT 0,  -- TV scope (0 = whole season / movie; >0 = exact episode)
    instance_id TEXT,                          -- exact owning arr instance (auto or user report)
    download_id TEXT,                          -- stable download-client hash (auto); keys dedupe + doctor tools
    arr_queue_id INTEGER,                      -- arr queue row observed for this exact incident (auto)
    detail TEXT NOT NULL DEFAULT '',            -- user free-text reason OR doctor diagnosis summary (UNTRUSTED)
    dedupe_key TEXT,                           -- stable idempotency key (auto); NULL allowed (user reports)
    occurrences INTEGER NOT NULL DEFAULT 1,     -- bumped when a duplicate signal/report attaches
    read INTEGER NOT NULL DEFAULT 0,            -- admin read/unread flag; any non-admin status change re-flags unread, an admin viewing marks read
    active_run_id INTEGER,                      -- CAS claim: at most one running run per issue
    resolution TEXT,                           -- short closing note on a terminal state
    resolution_kind TEXT NOT NULL DEFAULT '',  -- why work ended; exposed for audit provenance
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

-- Durable retry-aware observation state. An issue can exist here while it is
-- intentionally invisible to the admin attention queue: the arr still has a
-- live download/retry for the exact media scope, so Cantinarr observes before
-- asking either an agent or a human to intervene.
CREATE TABLE IF NOT EXISTS issue_observations (
    issue_id INTEGER PRIMARY KEY REFERENCES issues(id) ON DELETE CASCADE,
    service_type TEXT NOT NULL,                  -- radarr|sonarr
    scope_key TEXT NOT NULL,                     -- sha256(instance + exact media scope)
    state TEXT NOT NULL DEFAULT 'observing',     -- observing|recovering|settling
    signature TEXT NOT NULL DEFAULT '',          -- last complete matching queue signature
    first_seen_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    problem_since_at DATETIME,
    last_seen_at DATETIME,
    last_activity_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    settling_since DATETIME,
    promoted_at DATETIME,
    baseline_has_file INTEGER,
    baseline_file_id INTEGER,
    baseline_captured_at DATETIME,
    import_history_id INTEGER,
    import_download_id TEXT,
    import_file_id INTEGER,
    recovery_proven_at DATETIME,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_issue_observations_scope
    ON issue_observations(scope_key, issue_id);
CREATE INDEX IF NOT EXISTS idx_issue_observations_service
    ON issue_observations(service_type, issue_id);

CREATE TABLE IF NOT EXISTS issue_observation_downloads (
    issue_id INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    download_id TEXT NOT NULL,
    first_seen_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    arr_added_at DATETIME,                         -- attempt boundary from the arr queue (arr clock)
	queue_file_id INTEGER CHECK (queue_file_id >= 0), -- exact media file: NULL unknown, 0 absent, positive present
    PRIMARY KEY(issue_id, download_id)
);

-- Most recent successful COMPLETE queue snapshot per instance. This small,
-- bounded cache lets a user report join an already-observed arr retry without
-- putting a network round trip on the report request path. Failed queue reads
-- never update it, so absence in a stale/failed snapshot cannot close or hide
-- work.
CREATE TABLE IF NOT EXISTS remediation_queue_snapshots (
    instance_id TEXT PRIMARY KEY,
    service_type TEXT NOT NULL,
    observed_at DATETIME NOT NULL,
    items_json TEXT NOT NULL DEFAULT '[]'
);

CREATE TABLE IF NOT EXISTS remediation_observation_failures (
    instance_id TEXT PRIMARY KEY,
    service_type TEXT NOT NULL,
    first_failed_at DATETIME NOT NULL,
    last_failed_at DATETIME NOT NULL,
    error_text TEXT NOT NULL DEFAULT ''
);

-- Monotonic per-instance processing watermark shared by websocket snapshots,
-- synchronous execution preflights, and the restart sweeper. Older queued
-- work can never overwrite or reconcile after a newer success/failure.
CREATE TABLE IF NOT EXISTS remediation_observation_watermarks (
    instance_id TEXT PRIMARY KEY,
    service_type TEXT NOT NULL,
    observed_at DATETIME NOT NULL
);

-- Transition-only audit trail. Repeated identical polls do not append rows;
-- each entry explains why the observer changed phase or promoted the issue.
CREATE TABLE IF NOT EXISTS issue_observation_attempts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    issue_id INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    state TEXT NOT NULL,
    signature TEXT NOT NULL DEFAULT '',
    download_id TEXT NOT NULL DEFAULT '',
    arr_queue_id INTEGER,
    note TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_issue_observation_attempts_issue
    ON issue_observation_attempts(issue_id, id);

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
    status TEXT NOT NULL DEFAULT 'running',      -- running|waiting_user|waiting_approval|resume_pending|succeeded|gave_up|failed|aborted
    model TEXT NOT NULL DEFAULT '',
    proc_generation TEXT NOT NULL DEFAULT '',    -- process-start token; watchdog uses it to tell crashed-mid-run from parked
    step_count INTEGER NOT NULL DEFAULT 0,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    active_seconds INTEGER NOT NULL DEFAULT 0,   -- wall-clock excluding paused waits
    deadline_at DATETIME,                        -- active-work deadline; NULL while parked
    stop_reason TEXT,                            -- resolved|max_steps|timeout|repeated_failure|awaiting_approval|awaiting_user|tool_error
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
    approved_params TEXT,                        -- immutable admin override; NULL means original params
    rationale TEXT NOT NULL DEFAULT '',          -- agent's plain-language justification (UNTRUSTED — render as text)
    risk TEXT NOT NULL DEFAULT 'mutating',       -- retained for audit compatibility; every current action is gated
    status TEXT NOT NULL DEFAULT 'proposed',     -- proposed|executing|executed|denied|failed|superseded|outcome_unknown
    fingerprint TEXT NOT NULL,                   -- sha256(issue|run|tool gate|kind|params) — retry-idempotent, later re-proposals allowed
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

-- Durable ledger for supported settings mutations Cantinarr performs in an
-- external application through the AI/MCP settings tools.
-- The raw before/after snapshots are server-only rollback material; API
-- handlers expose the bounded, human-readable changes_json projection instead.
-- A row is inserted before remote I/O so a process loss can never leave an
-- unrecorded write attempt. Startup repairs an interrupted 'executing' row to
-- 'outcome_unknown' rather than guessing whether the remote service accepted it.
CREATE TABLE IF NOT EXISTS external_setting_changes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    parent_id INTEGER REFERENCES external_setting_changes(id),
    actor_user_id INTEGER NOT NULL DEFAULT 0,
    actor_device_id TEXT NOT NULL DEFAULT '',
    actor_name TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL,                         -- ai_chat|external_mcp|system|admin_revert
    service_type TEXT NOT NULL,                   -- radarr|sonarr|chaptarr
    instance_id TEXT NOT NULL,
    instance_name TEXT NOT NULL,
    resource_type TEXT NOT NULL,                  -- quality_profile|custom_format
    resource_id TEXT NOT NULL,
    resource_name TEXT NOT NULL,
    operation TEXT NOT NULL,                      -- update|create|revert
    status TEXT NOT NULL DEFAULT 'executing',     -- executing|applied|failed|outcome_unknown
    summary TEXT NOT NULL DEFAULT '',
    changes_json TEXT NOT NULL DEFAULT '[]',
    before_json TEXT NOT NULL DEFAULT '',          -- exact server-only snapshot
    after_json TEXT NOT NULL DEFAULT '',           -- exact server-only snapshot
    before_hash TEXT NOT NULL DEFAULT '',
    after_hash TEXT NOT NULL DEFAULT '',
    dependency_hash TEXT NOT NULL DEFAULT '',
    instance_binding BLOB NOT NULL,
    error_text TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_external_setting_changes_created
    ON external_setting_changes(id DESC);
CREATE INDEX IF NOT EXISTS idx_external_setting_changes_target
    ON external_setting_changes(service_type, instance_id, resource_type, resource_id, id DESC);
`

type schemaMigration struct {
	alter    string
	backfill []string
	after    func(*sql.Tx) error
}

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
	migrations := []schemaMigration{
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
		{
			// Shared AI access is opt-in for newly created accounts. Preserve the
			// shipped global-provider behavior for every account that predates the
			// grant, and preserve Codex links created by the per-user OAuth release
			// as explicit personal overrides.
			alter: "ALTER TABLE users ADD COLUMN ai_shared_enabled BOOLEAN NOT NULL DEFAULT 0",
			backfill: []string{
				"UPDATE users SET ai_shared_enabled = 1",
			},
			after: backfillLegacyPersonalCodex,
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
		// New book requests pin the Chaptarr instance the requester was viewing.
		// Legacy rows remain NULL: assigning them to today's user default would
		// invent provenance and could misroute an old approval/history row.
		{alter: "ALTER TABLE request_log ADD COLUMN instance_id TEXT REFERENCES service_instances(id) ON DELETE SET NULL"},
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
		// Candidate credential accepted alongside the current one while Cantinarr
		// updates the remote arr Connect record. A failed/ambiguous remote update
		// therefore cannot break the previously working webhook.
		{alter: "ALTER TABLE service_instances ADD COLUMN webhook_pending_token TEXT NOT NULL DEFAULT ''"},
		{
			// Completed-media downloads were originally one global identity-path
			// switch. Preserve that exact behavior for instances that predate the
			// per-instance mapper; newly-created instances explicitly start disabled.
			alter: "ALTER TABLE service_instances ADD COLUMN media_download_mode TEXT NOT NULL DEFAULT 'disabled'",
			backfill: []string{
				"UPDATE service_instances SET media_download_mode = 'identity' WHERE service_type IN ('radarr', 'sonarr', 'chaptarr')",
			},
		},
		{alter: "ALTER TABLE service_instances ADD COLUMN media_path_mappings TEXT NOT NULL DEFAULT '[]'"},
		// Plex access requests: the email a user shares so an admin can invite
		// them to the Plex server. Empty = not yet shared.
		{alter: "ALTER TABLE users ADD COLUMN plex_email TEXT NOT NULL DEFAULT ''"},
		// Admins are notified when a user shares their Plex email for an
		// invite, on by default. New on existing databases.
		{alter: "ALTER TABLE notification_prefs ADD COLUMN plex_access_request INTEGER NOT NULL DEFAULT 1"},
		// When Cantinarr sends the user's Plex invite (one-tap or auto), the
		// stamp records that it went out and the user is told to check email.
		{alter: "ALTER TABLE users ADD COLUMN plex_invited_at DATETIME"},
		{alter: "ALTER TABLE notification_prefs ADD COLUMN plex_invite_sent INTEGER NOT NULL DEFAULT 1"},
		{
			// AI remediation: per-issue admin read/unread flag. New issues start
			// unread (default 0); a non-admin status change re-flags unread, an
			// admin viewing the thread marks read. Backfill the pre-existing
			// backlog to read so it isn't a wall of unread on upgrade.
			alter: "ALTER TABLE issues ADD COLUMN read INTEGER NOT NULL DEFAULT 0",
			backfill: []string{
				"UPDATE issues SET read = 1",
			},
		},
		{alter: "ALTER TABLE issues ADD COLUMN arr_queue_id INTEGER"},
		{alter: "ALTER TABLE issues ADD COLUMN resolution_kind TEXT NOT NULL DEFAULT ''"},
		{alter: "ALTER TABLE agent_actions ADD COLUMN approved_params TEXT"},
		{alter: "ALTER TABLE issue_observation_downloads ADD COLUMN arr_added_at DATETIME"},
		{alter: "ALTER TABLE issue_observation_downloads ADD COLUMN queue_file_id INTEGER CHECK (queue_file_id >= 0)"},
	}
	for _, m := range migrations {
		if err := applySchemaMigration(db, m); err != nil {
			db.Close()
			return nil, err
		}
	}
	// Monetary estimates were briefly stored on remediation runs using a
	// hardcoded model-price table. They are not reliable audit data, so erase
	// legacy values without dropping the column: retaining the unused column
	// keeps rollback to an older server binary schema-compatible.
	if err := clearLegacyAgentRunCost(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("clear legacy agent-run cost estimates: %w", err)
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

	// Auto-detected issues used to store the *arr service type in media_type
	// (e.g. 'sonarr'), which clients rendered under the fallback "Movie" label.
	// Normalize legacy rows to the 'movie'|'tv'|'book' contract. Runs every
	// boot; idempotent and the table is tiny.
	if _, err := db.Exec(
		`UPDATE issues SET media_type = CASE media_type
			WHEN 'sonarr' THEN 'tv'
			WHEN 'radarr' THEN 'movie'
			WHEN 'chaptarr' THEN 'book'
		 END
		 WHERE media_type IN ('sonarr', 'radarr', 'chaptarr')`,
	); err != nil {
		db.Close()
		return nil, fmt.Errorf("normalize issue media types: %w", err)
	}
	if err := repairAutoIssueDedupe(db); err != nil {
		db.Close()
		return nil, err
	}

	// Repair unsafe remediation states from older builds. Terminal issues cannot
	// retain live approvals or parked runs, and an action found mid-execution has
	// an unknowable external outcome, so it is never retried blindly.
	repairs := []string{
		`UPDATE external_setting_changes
		 SET status = 'outcome_unknown', completed_at = COALESCE(completed_at, CURRENT_TIMESTAMP),
		     error_text = CASE WHEN error_text = ''
		       THEN 'Cantinarr restarted while this settings change was executing; compare the live value before taking further action.'
		       ELSE error_text END
		 WHERE status = 'executing'`,
		`UPDATE issues SET resolution_kind = CASE
		   WHEN source = 'auto' AND COALESCE(resolution,'') LIKE 'Auto-resolved:%' THEN 'arr_state_cleared'
		   WHEN resolution = 'user_unresponsive' THEN 'reporter_timeout'
		   WHEN status = 'dismissed' THEN 'admin_dismissed'
		   ELSE 'legacy_unknown'
		 END
		 WHERE closed_at IS NOT NULL AND resolution_kind = ''`,
		`UPDATE agent_actions
		 SET status = 'superseded', decided_at = COALESCE(decided_at, CURRENT_TIMESTAMP),
		     result_text = COALESCE(result_text, 'Superseded because the issue was already closed; no fix was executed.')
		 WHERE status = 'proposed'
		   AND issue_id IN (SELECT id FROM issues WHERE closed_at IS NOT NULL)`,
		`UPDATE agent_actions
		 SET status = 'outcome_unknown',
		     result_text = COALESCE(result_text, 'Cantinarr restarted while this action was executing; verify the arr state manually. It will not be retried.')
		 WHERE status = 'executing'`,
		`UPDATE issues
		 SET status = 'needs_admin', read = 0, active_run_id = NULL,
		     resolution = 'An approved action was interrupted while executing. Verify the arr state manually; Cantinarr will not retry it.',
		     resolution_kind = '', updated_at = CURRENT_TIMESTAMP
		 WHERE closed_at IS NULL AND id IN (
		   SELECT issue_id FROM agent_actions WHERE status = 'outcome_unknown'
		 )`,
		`UPDATE agent_runs
		 SET status = 'aborted', stop_reason = 'action_outcome_unknown', deadline_at = NULL,
		     finished_at = COALESCE(finished_at, CURRENT_TIMESTAMP)
		 WHERE status IN ('running','waiting_user','waiting_approval','resume_pending')
		   AND issue_id IN (SELECT issue_id FROM agent_actions WHERE status = 'outcome_unknown')`,
		`UPDATE agent_runs
		 SET status = 'aborted', stop_reason = 'issue_closed', deadline_at = NULL,
		     finished_at = COALESCE(finished_at, CURRENT_TIMESTAMP)
		 WHERE status IN ('running','waiting_user','waiting_approval','resume_pending')
		   AND issue_id IN (SELECT id FROM issues WHERE closed_at IS NOT NULL)`,
		`UPDATE issues SET active_run_id = NULL
		 WHERE closed_at IS NOT NULL AND active_run_id IS NOT NULL`,
		`UPDATE agent_actions
		 SET status = 'superseded', decided_at = COALESCE(decided_at, CURRENT_TIMESTAMP),
		     result_text = COALESCE(result_text, 'Superseded because its approval gate was not active after restart.')
		 WHERE status = 'proposed' AND NOT EXISTS (
		   SELECT 1 FROM issues i JOIN agent_runs r ON r.id = agent_actions.run_id
		   WHERE i.id = agent_actions.issue_id AND i.closed_at IS NULL
		     AND i.status = 'awaiting_approval' AND r.status = 'waiting_approval'
		 )`,
		`UPDATE agent_actions
		 SET status = 'superseded', decided_at = COALESCE(decided_at, CURRENT_TIMESTAMP),
		     result_text = COALESCE(result_text, 'Superseded by a newer proposal for the same issue.')
		 WHERE status = 'proposed' AND id NOT IN (
		   SELECT MAX(id) FROM agent_actions WHERE status = 'proposed' GROUP BY issue_id
		 )`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_actions_one_pending_per_issue
		 ON agent_actions(issue_id) WHERE status = 'proposed'`,
	}
	for _, stmt := range repairs {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("repair remediation state: %w", err)
		}
	}
	if err := repairReleaseActionReferences(db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

// backfillLegacyPersonalCodex runs only when the shared-AI grant column is
// first added. It preserves personal links from the original Codex release,
// including installs whose global selection/model came only from environment
// defaults. Running it on every startup would resurrect an override a user
// intentionally disabled.
// applySchemaMigration keeps the schema marker (the added column), its
// compatibility backfills, and any data-dependent post-step in one SQLite
// transaction. If the process stops or a step fails, the ALTER rolls back too,
// so the next startup can retry instead of mistaking a partial migration for a
// completed one.
func applySchemaMigration(db *sql.DB, migration schemaMigration) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration %q: %w", migration.alter, err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(migration.alter); err != nil {
		if strings.Contains(err.Error(), "duplicate column") {
			return nil // already applied; skip the one-time backfill
		}
		return fmt.Errorf("apply migration %q: %w", migration.alter, err)
	}
	for _, stmt := range migration.backfill {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("apply backfill %q: %w", stmt, err)
		}
	}
	if migration.after != nil {
		if err := migration.after(tx); err != nil {
			return fmt.Errorf("apply post-migration step for %q: %w", migration.alter, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %q: %w", migration.alter, err)
	}
	return nil
}

func backfillLegacyPersonalCodex(tx *sql.Tx) error {
	var provider, model string
	_ = tx.QueryRow(`SELECT value FROM settings WHERE key = 'ai_provider'`).Scan(&provider)
	_ = tx.QueryRow(`SELECT value FROM settings WHERE key = 'ai_model'`).Scan(&model)
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" {
		provider = strings.TrimSpace(os.Getenv("CANTINARR_AI_PROVIDER"))
	}
	if model == "" {
		model = strings.TrimSpace(os.Getenv("CANTINARR_AI_MODEL"))
	}
	if provider != "codex" {
		return nil
	}
	if model == "" {
		model = "default"
	}
	_, err := tx.Exec(`
		INSERT OR IGNORE INTO user_ai_settings (user_id, provider, model)
		SELECT user_id, 'codex', ? FROM user_codex_accounts`, model)
	return err
}

func clearLegacyAgentRunCost(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(agent_runs)")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if name == "cost_micros" {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !found {
		return nil
	}
	_, err = db.Exec("UPDATE agent_runs SET cost_micros = 0 WHERE cost_micros != 0")
	return err
}

// repairReleaseActionReferences removes legacy raw release capabilities from
// action JSON. Approval resolves this one-way fingerprint against a fresh,
// issue-scoped interactive search, so no signed URL/API credential needs to
// remain in SQLite after an upgrade.
func repairReleaseActionReferences(db *sql.DB) error {
	type row struct {
		id       int64
		issueID  int64
		status   string
		params   string
		approved sql.NullString
	}
	rows, err := db.Query(
		"SELECT id, issue_id, status, params, approved_params FROM agent_actions WHERE kind = 'grab_release'",
	)
	if err != nil {
		return fmt.Errorf("query release action references: %w", err)
	}
	var actions []row
	for rows.Next() {
		var action row
		if err := rows.Scan(&action.id, &action.issueID, &action.status, &action.params, &action.approved); err != nil {
			rows.Close()
			return fmt.Errorf("scan release action reference: %w", err)
		}
		actions = append(actions, action)
	}
	if err := rows.Close(); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, action := range actions {
		params, hasMetadata, err := safeReleaseActionJSON(action.params)
		if err != nil {
			params = `{"redacted":"[REDACTED invalid release params]"}`
			hasMetadata = false
		}
		var approved any
		if action.approved.Valid {
			value, _, err := safeReleaseActionJSON(action.approved.String)
			if err != nil {
				value = `{"redacted":"[REDACTED invalid release params]"}`
			}
			approved = value
		}
		if _, err := tx.Exec(
			"UPDATE agent_actions SET params = ?, approved_params = ? WHERE id = ?",
			params, approved, action.id,
		); err != nil {
			return fmt.Errorf("store safe release action %d: %w", action.id, err)
		}
		if action.status == "proposed" && !hasMetadata {
			if _, err := tx.Exec(
				`UPDATE agent_actions SET status = 'superseded', decided_at = COALESCE(decided_at, CURRENT_TIMESTAMP),
				 result_text = COALESCE(result_text, 'Superseded during upgrade because the legacy proposal lacks verified release metadata; no fix was executed.')
				 WHERE id = ? AND status = 'proposed'`, action.id,
			); err != nil {
				return fmt.Errorf("supersede legacy release action %d: %w", action.id, err)
			}
			if _, err := tx.Exec(
				`UPDATE issues SET status = 'needs_admin', read = 0, active_run_id = NULL,
				 resolution = 'A legacy release proposal lacked enough verified metadata to approve safely. Review the issue and run a fresh investigation.',
				 resolution_kind = '', updated_at = CURRENT_TIMESTAMP
				 WHERE id = ? AND closed_at IS NULL AND status = 'awaiting_approval'`, action.issueID,
			); err != nil {
				return fmt.Errorf("escalate legacy release issue %d: %w", action.issueID, err)
			}
			if _, err := tx.Exec(
				`UPDATE agent_runs SET status = 'aborted', stop_reason = 'legacy_release_metadata',
				 deadline_at = NULL, finished_at = COALESCE(finished_at, CURRENT_TIMESTAMP)
				 WHERE issue_id = ? AND status = 'waiting_approval'`, action.issueID,
			); err != nil {
				return fmt.Errorf("abort legacy release run for issue %d: %w", action.issueID, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit release action reference repair: %w", err)
	}
	return nil
}

func safeReleaseActionJSON(raw string) (string, bool, error) {
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		return "", false, err
	}
	guid, ok := params["guid"].(string)
	if !ok || guid == "" {
		return "", false, fmt.Errorf("missing guid")
	}
	if !isCanonicalReleaseFingerprint(guid) {
		digest := sha256.Sum256([]byte(guid))
		params["guid"] = fmt.Sprintf("[REDACTED release sha256:%x]", digest[:8])
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return "", false, err
	}
	title, titleOK := params["release_title"].(string)
	protocol, protocolOK := params["protocol"].(string)
	indexer, indexerOK := params["indexer"].(string)
	size, sizeOK := params["size"].(float64)
	hasMetadata := titleOK && title != "" && protocolOK && protocol != "" &&
		indexerOK && indexer != "" && sizeOK && size >= 0
	return string(encoded), hasMetadata, nil
}

func isCanonicalReleaseFingerprint(value string) bool {
	const prefix = "[REDACTED release sha256:"
	const digestHexLen = 16
	if len(value) != len(prefix)+digestHexLen+1 || !strings.HasPrefix(value, prefix) || value[len(value)-1] != ']' {
		return false
	}
	for _, char := range value[len(prefix) : len(value)-1] {
		if char < '0' || char > '9' {
			if char < 'a' || char > 'f' {
				return false
			}
		}
	}
	return true
}

// repairAutoIssueDedupe migrates the pre-scope key (which included a diagnosis
// label) to one incident per instance/download. Any already-open duplicates are
// terminalized before the canonical newest row is re-keyed; the normal repair
// pass above then invalidates their proposals/runs.
func repairAutoIssueDedupe(db *sql.DB) error {
	rows, err := db.Query(
		`SELECT i.id, i.instance_id, i.download_id,
		        EXISTS (SELECT 1 FROM agent_actions a
		                WHERE a.issue_id = i.id AND a.status IN ('executing','outcome_unknown')) AS hazardous
		 FROM issues i
		 WHERE source = 'auto' AND closed_at IS NULL
		   AND COALESCE(instance_id,'') != '' AND COALESCE(download_id,'') != ''
		 ORDER BY hazardous DESC, i.id DESC`,
	)
	if err != nil {
		return fmt.Errorf("query auto-issue dedupe repair: %w", err)
	}
	type incident struct {
		winner int64
		rows   []struct {
			id        int64
			hazardous bool
		}
	}
	groups := map[string]*incident{}
	for rows.Next() {
		var id int64
		var instanceID, downloadID string
		var hazardous bool
		if err := rows.Scan(&id, &instanceID, &downloadID, &hazardous); err != nil {
			rows.Close()
			return fmt.Errorf("scan auto-issue dedupe repair: %w", err)
		}
		key := instanceID + "\x00" + downloadID
		group := groups[key]
		if group == nil {
			group = &incident{winner: id}
			groups[key] = group
		}
		group.rows = append(group.rows, struct {
			id        int64
			hazardous bool
		}{id: id, hazardous: hazardous})
	}
	if err := rows.Close(); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, group := range groups {
		for _, duplicate := range group.rows[1:] {
			// Never hide a second action whose remote outcome may already have
			// changed state. It remains a separate needs-admin record.
			if duplicate.hazardous {
				continue
			}
			if _, err := tx.Exec(
				`UPDATE issues SET status = 'dismissed',
				 resolution = 'Superseded by the canonical record for this same detected incident during upgrade.',
				 resolution_kind = 'legacy_unknown', closed_at = CURRENT_TIMESTAMP,
				 active_run_id = NULL, updated_at = CURRENT_TIMESTAMP
				 WHERE id = ? AND closed_at IS NULL`, duplicate.id,
			); err != nil {
				return fmt.Errorf("close duplicate auto issue: %w", err)
			}
		}
		parts := strings.SplitN(key, "\x00", 2)
		sum := sha256.Sum256([]byte(parts[0] + "|" + parts[1]))
		if _, err := tx.Exec("UPDATE issues SET dedupe_key = ? WHERE id = ?", hex.EncodeToString(sum[:]), group.winner); err != nil {
			return fmt.Errorf("rekey auto issue: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit auto-issue dedupe repair: %w", err)
	}
	return nil
}
