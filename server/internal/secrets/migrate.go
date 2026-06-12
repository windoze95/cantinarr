package secrets

import (
	"database/sql"
	"fmt"
)

// EncryptExisting encrypts legacy plaintext secrets in place: the given
// settings keys plus the api_key and password columns of service_instances.
// It is idempotent — already-encrypted values are skipped — and returns the
// number of values rewritten.
func EncryptExisting(db *sql.DB, c *Cipher, settingsKeys []string) (int, error) {
	migrated := 0

	for _, key := range settingsKeys {
		var value string
		err := db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return migrated, fmt.Errorf("read setting %s: %w", key, err)
		}
		if value == "" || IsEncrypted(value) {
			continue
		}
		enc, err := c.Encrypt(value)
		if err != nil {
			return migrated, fmt.Errorf("encrypt setting %s: %w", key, err)
		}
		if _, err := db.Exec("UPDATE settings SET value = ? WHERE key = ?", enc, key); err != nil {
			return migrated, fmt.Errorf("rewrite setting %s: %w", key, err)
		}
		migrated++
	}

	rows, err := db.Query("SELECT id, api_key, password FROM service_instances")
	if err != nil {
		return migrated, fmt.Errorf("scan instances: %w", err)
	}
	type instRow struct{ id, apiKey, password string }
	var pending []instRow
	for rows.Next() {
		var r instRow
		if err := rows.Scan(&r.id, &r.apiKey, &r.password); err != nil {
			rows.Close()
			return migrated, fmt.Errorf("scan instance row: %w", err)
		}
		if (r.apiKey != "" && !IsEncrypted(r.apiKey)) || (r.password != "" && !IsEncrypted(r.password)) {
			pending = append(pending, r)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return migrated, fmt.Errorf("iterate instances: %w", err)
	}

	for _, r := range pending {
		apiKey, password := r.apiKey, r.password
		if apiKey != "" && !IsEncrypted(apiKey) {
			if apiKey, err = c.Encrypt(apiKey); err != nil {
				return migrated, fmt.Errorf("encrypt instance %s api key: %w", r.id, err)
			}
		}
		if password != "" && !IsEncrypted(password) {
			if password, err = c.Encrypt(password); err != nil {
				return migrated, fmt.Errorf("encrypt instance %s password: %w", r.id, err)
			}
		}
		if _, err := db.Exec("UPDATE service_instances SET api_key = ?, password = ? WHERE id = ?", apiKey, password, r.id); err != nil {
			return migrated, fmt.Errorf("rewrite instance %s: %w", r.id, err)
		}
		migrated++
	}

	return migrated, nil
}
