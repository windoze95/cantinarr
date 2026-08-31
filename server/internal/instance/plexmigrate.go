package instance

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
	"github.com/windoze95/cantinarr-server/internal/plex"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// The settings keys the pre-instance Plex integration kept, and the marker
// that says they have been carried over.
const (
	plexMigratedKey         = "plex_instance_migrated"
	legacyPlexClientID      = "plex_client_id"
	legacyPlexToken         = "plex_token"
	legacyPlexAccount       = "plex_account"
	legacyPlexMachineID     = "plex_machine_id"
	legacyPlexServerName    = "plex_server_name"
	legacyPlexLibraryIDs    = "plex_library_ids"
	legacyPlexAutoInvite    = "plex_auto_invite"
	legacyPlexInviteColumns = "id, plex_email, plex_invited_at"
)

// MigratePlexSingleton carries the pre-instance Plex integration into the
// instance model, once: the linked account and selected server become a
// plex instance, everyone Cantinarr had invited is granted it and gets an
// account row dated by their invite, and the old settings keys go — all in
// one transaction guarded by a marker, so a crash mid-way leaves nothing
// half-applied and a second boot changes nothing. The app-wide client
// identifier stays: the token was minted under it and moves with it.
func MigratePlexSingleton(db *sql.DB, cipher *secrets.Cipher, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	if _, done := getSetting(db, plexMigratedKey); done {
		return nil
	}
	stored, linked := getSetting(db, legacyPlexToken)
	machineID, _ := getSetting(db, legacyPlexMachineID)
	if !linked || stored == "" {
		return finishPlexMigration(db, nil, logger, "no linked Plex account")
	}
	if machineID == "" {
		logger.Warn("plex: a Plex account was linked but no server was ever selected; link it again as a Plex instance in Settings")
		return finishPlexMigration(db, nil, logger, "linked account without a server")
	}
	token, err := cipher.Decrypt(stored)
	if err != nil {
		// Leave everything as it is: nothing is lost, and the next boot with
		// the right key carries it over.
		logger.Error("plex: the stored Plex token does not decrypt; the Plex instance was not created", "err", err)
		return nil
	}

	clientID, _ := getSetting(db, legacyPlexClientID)
	if clientID == "" {
		clientID = uuid.NewString()
	}
	name, _ := getSetting(db, legacyPlexServerName)
	if strings.TrimSpace(name) == "" {
		name = "Plex"
	}
	var libraryIDs []string
	if raw, ok := getSetting(db, legacyPlexLibraryIDs); ok && raw != "" {
		var ids []int64
		if err := json.Unmarshal([]byte(raw), &ids); err == nil {
			for _, id := range ids {
				libraryIDs = append(libraryIDs, strconv.FormatInt(id, 10))
			}
		}
	}
	autoInvite, _ := getSetting(db, legacyPlexAutoInvite)

	inst := &Instance{
		ID:          "plex-" + uuid.New().String()[:8],
		ServiceType: "plex",
		Name:        name,
		URL:         plex.BaseURL,
		APIKey:      token,
		MediaServerConfig: MediaServerConfig{
			PublicAddress:     PlexPublicAddress,
			LibraryIDs:        tidyLibraryIDs(libraryIDs),
			MachineIdentifier: machineID,
			AutoApprove:       autoInvite == "1",
			ClientID:          clientID,
		},
		CreatedAt: time.Now(),
	}
	encryptedToken, err := cipher.Encrypt(token)
	if err != nil {
		return fmt.Errorf("plex migration: encrypt token: %w", err)
	}
	configJSON, err := json.Marshal(inst.MediaServerConfig)
	if err != nil {
		return fmt.Errorf("plex migration: encode config: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("plex migration: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		"INSERT INTO service_instances (id, service_type, name, url, api_key, username, password, is_default, sort_order, media_download_mode, media_path_mappings, media_server_config, created_at) VALUES (?, ?, ?, ?, ?, '', '', 0, 0, ?, '[]', ?, ?)",
		inst.ID, inst.ServiceType, inst.Name, inst.URL, encryptedToken, MediaDownloadModeDisabled, string(configJSON), inst.CreatedAt,
	); err != nil {
		return fmt.Errorf("plex migration: create instance: %w", err)
	}

	granted, skipped, err := migratePlexInvites(tx, inst.ID, logger)
	if err != nil {
		return err
	}
	if err := clearLegacyPlexSettings(tx); err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", plexMigratedKey, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("plex migration: mark done: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("plex migration: %w", err)
	}
	logger.Info("plex: the linked Plex account is now a Plex instance; everyone already invited holds its grant",
		"instance_id", inst.ID, "name", inst.Name, "granted", granted, "skipped", skipped, "auto_approve", inst.MediaServerConfig.AutoApprove)
	return nil
}

// migratePlexInvites grants the new instance to every user Cantinarr had
// invited and records their share. A second user with the same address
// (case-insensitively) is skipped and logged: one share, one row, and a
// grant with no row would have the drift sweep re-inviting them forever.
func migratePlexInvites(tx *sql.Tx, instanceID string, logger *slog.Logger) (granted, skipped int, err error) {
	rows, err := tx.Query("SELECT " + legacyPlexInviteColumns + " FROM users WHERE plex_invited_at IS NOT NULL AND plex_email != '' ORDER BY plex_invited_at, id")
	if err != nil {
		return 0, 0, fmt.Errorf("plex migration: list invited users: %w", err)
	}
	type invited struct {
		userID    int64
		email     string
		invitedAt time.Time
	}
	var users []invited
	for rows.Next() {
		var u invited
		var at sql.NullTime
		if err := rows.Scan(&u.userID, &u.email, &at); err != nil {
			rows.Close()
			return 0, 0, fmt.Errorf("plex migration: scan invited user: %w", err)
		}
		if at.Valid {
			u.invitedAt = at.Time
		} else {
			u.invitedAt = time.Now()
		}
		users = append(users, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("plex migration: list invited users: %w", err)
	}

	seen := map[string]int64{}
	for _, u := range users {
		email := mediaserver.CanonicalEmail(u.email)
		if !mediaserver.ValidEmail(email) {
			skipped++
			continue
		}
		if first, dup := seen[email]; dup {
			logger.Warn("plex: two users share one Plex email; only the first keeps the grant", "email_owner", first, "skipped_user", u.userID)
			skipped++
			continue
		}
		seen[email] = u.userID
		if _, err := tx.Exec("INSERT OR IGNORE INTO user_instance_grants (user_id, instance_id) VALUES (?, ?)", u.userID, instanceID); err != nil {
			return 0, 0, fmt.Errorf("plex migration: grant user %d: %w", u.userID, err)
		}
		if _, err := tx.Exec(
			"INSERT INTO user_media_server_accounts (user_id, instance_id, remote_user_id, remote_username, created_by_cantinarr, created_at) VALUES (?, ?, ?, ?, 1, ?)",
			u.userID, instanceID, email, strings.TrimSpace(u.email), u.invitedAt,
		); err != nil {
			return 0, 0, fmt.Errorf("plex migration: record share for user %d: %w", u.userID, err)
		}
		granted++
	}
	return granted, skipped, nil
}

func clearLegacyPlexSettings(tx *sql.Tx) error {
	for _, key := range []string{legacyPlexToken, legacyPlexAccount, legacyPlexMachineID, legacyPlexServerName, legacyPlexLibraryIDs, legacyPlexAutoInvite} {
		if _, err := tx.Exec("DELETE FROM settings WHERE key = ?", key); err != nil {
			return fmt.Errorf("plex migration: clear %s: %w", key, err)
		}
	}
	return nil
}

// finishPlexMigration marks the migration done when there was nothing to
// carry over, clearing whatever legacy keys were left.
func finishPlexMigration(db *sql.DB, _ *Instance, logger *slog.Logger, reason string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("plex migration: %w", err)
	}
	defer tx.Rollback()
	if err := clearLegacyPlexSettings(tx); err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", plexMigratedKey, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("plex migration: mark done: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("plex migration: %w", err)
	}
	logger.Debug("plex: nothing to migrate", "reason", reason)
	return nil
}

func getSetting(db *sql.DB, key string) (string, bool) {
	var value string
	err := db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return "", false
	}
	return value, true
}
