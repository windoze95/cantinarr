package db

// Followers are created only by an explicit missing-episode request. Inventory
// scans never create or wake delivery work. Native IDs are execution receipts,
// not catalog identity; canonical identity remains in the saved JSON.
const mediaFollowSQL = `
CREATE TABLE IF NOT EXISTS request_media_modes (
 request_id INTEGER PRIMARY KEY REFERENCES request_log(id) ON DELETE CASCADE,
 mode TEXT NOT NULL CHECK(mode IN ('missing','library_copy')),
 requires_media_coverage INTEGER NOT NULL DEFAULT 0,
 source_scope TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS request_tv_follows (
 request_id INTEGER PRIMARY KEY REFERENCES request_log(id) ON DELETE CASCADE,
 state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active','paused','stopped')),
 message TEXT NOT NULL DEFAULT '',
 pause_code TEXT NOT NULL DEFAULT '',
 series_id INTEGER NOT NULL DEFAULT 0,
 series_owned INTEGER NOT NULL DEFAULT 0,
 next_attempt_at INTEGER NOT NULL DEFAULT 0,
 lease_token TEXT NOT NULL DEFAULT '',
 lease_until INTEGER NOT NULL DEFAULT 0,
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tv_follows_due ON request_tv_follows(state,next_attempt_at);
CREATE TABLE IF NOT EXISTS request_tv_follow_episodes (
 request_id INTEGER NOT NULL REFERENCES request_tv_follows(request_id) ON DELETE CASCADE,
 episode_key TEXT NOT NULL,
 identity TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'queued' CHECK(state IN ('excluded','queued','monitored','searching','submitted','available','paused')),
 episode_id INTEGER NOT NULL DEFAULT 0,
 monitor_owned INTEGER NOT NULL DEFAULT 0,
 command_id INTEGER NOT NULL DEFAULT 0,
 searched_at INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(request_id,episode_key)
);
`
