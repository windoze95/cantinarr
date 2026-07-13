package codexapp

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/windoze95/cantinarr-server/internal/secrets"
)

type accountRecord struct {
	authJSON   []byte
	email      string
	planType   string
	rateLimits json.RawMessage
}

func (m *Manager) loadAccount(userID int64) (accountRecord, bool, error) {
	var storedAuth, email, planType, rateLimits string
	err := m.db.QueryRow(`
		SELECT auth_blob, email, plan_type, rate_limits_json
		FROM user_codex_accounts
		WHERE user_id = ?`, userID,
	).Scan(&storedAuth, &email, &planType, &rateLimits)
	if errors.Is(err, sql.ErrNoRows) {
		return accountRecord{}, false, nil
	}
	if err != nil {
		return accountRecord{}, false, ErrStorage
	}
	// This table has never had a plaintext format. Fail closed if a row was
	// manually inserted or corrupted rather than materializing it on disk.
	if storedAuth == "" || !secrets.IsEncrypted(storedAuth) {
		return accountRecord{}, false, ErrStorage
	}
	plain, err := m.cipher.Decrypt(storedAuth)
	if err != nil || !validAuthJSON([]byte(plain)) {
		return accountRecord{}, false, ErrStorage
	}
	record := accountRecord{
		authJSON: []byte(plain),
		email:    email,
		planType: planType,
	}
	if rateLimits != "" && json.Valid([]byte(rateLimits)) {
		record.rateLimits = json.RawMessage(rateLimits)
	}
	return record, true, nil
}

func (m *Manager) accountMetadata(userID int64) (AccountStatus, bool, error) {
	var storedAuth, email, planType, rateLimits string
	var updatedUnix int64
	err := m.db.QueryRow(`
		SELECT auth_blob, email, plan_type, rate_limits_json,
			COALESCE(CAST(strftime('%s', updated_at) AS INTEGER), 0)
		FROM user_codex_accounts
		WHERE user_id = ?`, userID,
	).Scan(&storedAuth, &email, &planType, &rateLimits, &updatedUnix)
	if errors.Is(err, sql.ErrNoRows) {
		return AccountStatus{}, false, nil
	}
	if err != nil {
		return AccountStatus{}, false, ErrStorage
	}
	if storedAuth == "" || !secrets.IsEncrypted(storedAuth) {
		return AccountStatus{}, false, ErrStorage
	}
	status := AccountStatus{Connected: true, Email: email, PlanType: planType, Stale: true}
	if rateLimits != "" && json.Valid([]byte(rateLimits)) {
		status.RateLimits = json.RawMessage(rateLimits)
	}
	if updatedUnix > 0 {
		status.UpdatedAt = time.Unix(updatedUnix, 0).UTC()
		if len(status.RateLimits) != 0 {
			status.Stale = time.Since(status.UpdatedAt) >= accountStatusTTL
		}
	}
	return status, true, nil
}

func (m *Manager) saveAccount(userID int64, authJSON []byte, status AccountStatus) error {
	if !validAuthJSON(authJSON) {
		return ErrStorage
	}
	encrypted, err := m.cipher.Encrypt(string(authJSON))
	if err != nil || !secrets.IsEncrypted(encrypted) {
		return ErrStorage
	}
	rateLimits := ""
	if len(status.RateLimits) != 0 {
		if !json.Valid(status.RateLimits) {
			return ErrStorage
		}
		rateLimits = string(status.RateLimits)
	}
	_, err = m.db.Exec(`
		INSERT INTO user_codex_accounts
			(user_id, auth_blob, email, plan_type, rate_limits_json, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(user_id) DO UPDATE SET
			auth_blob = excluded.auth_blob,
			email = excluded.email,
			plan_type = excluded.plan_type,
			rate_limits_json = excluded.rate_limits_json,
			updated_at = CURRENT_TIMESTAMP`,
		userID, encrypted, status.Email, status.PlanType, rateLimits,
	)
	if err != nil {
		return ErrStorage
	}
	return nil
}

func (m *Manager) saveRefreshedAuth(userID int64, authJSON []byte) error {
	if !validAuthJSON(authJSON) {
		return ErrStorage
	}
	encrypted, err := m.cipher.Encrypt(string(authJSON))
	if err != nil || !secrets.IsEncrypted(encrypted) {
		return ErrStorage
	}
	result, err := m.db.Exec(`
		UPDATE user_codex_accounts
		SET auth_blob = ?
		WHERE user_id = ?`, encrypted, userID)
	if err != nil {
		return ErrStorage
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return ErrStorage
	}
	return nil
}

func (m *Manager) deleteAccount(userID int64) error {
	if _, err := m.db.Exec(`DELETE FROM user_codex_accounts WHERE user_id = ?`, userID); err != nil {
		return ErrStorage
	}
	return nil
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}
