package db

const mediaAvailabilitySQL = `
CREATE TABLE IF NOT EXISTS media_availability_scans (
 instance_id TEXT PRIMARY KEY REFERENCES service_instances(id) ON DELETE CASCADE,
 generation INTEGER NOT NULL DEFAULT 0,
 config_fingerprint TEXT NOT NULL DEFAULT '',
 last_success_at INTEGER NOT NULL DEFAULT 0,
 last_full_at INTEGER NOT NULL DEFAULT 0,
 last_attempt_at INTEGER NOT NULL DEFAULT 0,
 item_count INTEGER NOT NULL DEFAULT 0,
 error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS media_availability_items (
 instance_id TEXT NOT NULL REFERENCES service_instances(id) ON DELETE CASCADE,
 library_id TEXT NOT NULL,
 item_id TEXT NOT NULL,
 generation INTEGER NOT NULL,
 media_type TEXT NOT NULL,
 tmdb_id INTEGER NOT NULL DEFAULT 0,
 tvdb_id INTEGER NOT NULL DEFAULT 0,
 payload TEXT NOT NULL,
 PRIMARY KEY(instance_id,library_id,item_id)
);
CREATE INDEX IF NOT EXISTS media_availability_title ON media_availability_items(instance_id,media_type,tmdb_id,tvdb_id);
-- A household association authorizes local availability only. It is not a
-- remote account link and never participates in invitation/account management.
CREATE TABLE IF NOT EXISTS media_availability_households (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 instance_id TEXT NOT NULL REFERENCES service_instances(id) ON DELETE CASCADE,
 library_ids TEXT NOT NULL,
 revision INTEGER NOT NULL DEFAULT 1,
 PRIMARY KEY(user_id,instance_id)
);
CREATE TRIGGER IF NOT EXISTS media_availability_link_retires_household
AFTER INSERT ON user_media_server_accounts BEGIN
 DELETE FROM media_availability_households WHERE user_id=NEW.user_id AND instance_id=NEW.instance_id;
END;
`
