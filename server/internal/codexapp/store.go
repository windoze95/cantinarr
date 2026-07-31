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

// AccountExists reports whether encrypted authorization material exists for
// one personal or shared account. Storage failures remain distinguishable from
// absence so provider resolution can fail closed.
func (m *Manager) AccountExists(account AccountRef) (bool, error) {
	if m == nil || m.db == nil || !account.valid() {
		return false, ErrInvalidInput
	}
	if m.cipher == nil {
		return false, ErrInvalidInput
	}
	var storedAuth string
	var err error
	if account.shared {
		err = m.db.QueryRow(`SELECT auth_blob FROM shared_codex_account WHERE singleton = 1`).Scan(&storedAuth)
	} else {
		err = m.db.QueryRow(`SELECT auth_blob FROM user_codex_accounts WHERE user_id = ?`, account.userID).Scan(&storedAuth)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, ErrStorage
	}
	if storedAuth == "" || !secrets.IsEncrypted(storedAuth) {
		return false, ErrStorage
	}
	plain, err := m.cipher.Decrypt(storedAuth)
	if err != nil || !validAuthJSON([]byte(plain)) {
		return false, ErrStorage
	}
	return true, nil
}

func (m *Manager) loadAccount(account AccountRef) (accountRecord, bool, error) {
	var storedAuth, email, planType, rateLimits string
	var err error
	if account.shared {
		err = m.db.QueryRow(`
			SELECT auth_blob, email, plan_type, rate_limits_json
			FROM shared_codex_account WHERE singleton = 1`,
		).Scan(&storedAuth, &email, &planType, &rateLimits)
	} else {
		err = m.db.QueryRow(`
			SELECT auth_blob, email, plan_type, rate_limits_json
			FROM user_codex_accounts WHERE user_id = ?`, account.userID,
		).Scan(&storedAuth, &email, &planType, &rateLimits)
	}
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

func (m *Manager) accountMetadata(account AccountRef) (AccountStatus, bool, error) {
	var storedAuth, email, planType, rateLimits string
	var updatedUnix int64
	var err error
	if account.shared {
		err = m.db.QueryRow(`
			SELECT auth_blob, email, plan_type, rate_limits_json,
				COALESCE(CAST(strftime('%s', updated_at) AS INTEGER), 0)
			FROM shared_codex_account WHERE singleton = 1`,
		).Scan(&storedAuth, &email, &planType, &rateLimits, &updatedUnix)
	} else {
		err = m.db.QueryRow(`
			SELECT auth_blob, email, plan_type, rate_limits_json,
				COALESCE(CAST(strftime('%s', updated_at) AS INTEGER), 0)
			FROM user_codex_accounts WHERE user_id = ?`, account.userID,
		).Scan(&storedAuth, &email, &planType, &rateLimits, &updatedUnix)
	}
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

func (m *Manager) saveAccount(account AccountRef, authJSON []byte, status AccountStatus) error {
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
	if account.shared {
		_, err = m.db.Exec(`
			INSERT INTO shared_codex_account
				(singleton, auth_blob, email, plan_type, rate_limits_json, updated_at)
			VALUES (1, ?, ?, ?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(singleton) DO UPDATE SET
				auth_blob = excluded.auth_blob,
				email = excluded.email,
				plan_type = excluded.plan_type,
				rate_limits_json = excluded.rate_limits_json,
				updated_at = CURRENT_TIMESTAMP`, encrypted, status.Email, status.PlanType, rateLimits)
	} else {
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
			account.userID, encrypted, status.Email, status.PlanType, rateLimits)
	}
	if err != nil {
		return ErrStorage
	}
	return nil
}

func (m *Manager) saveRefreshedAuth(account AccountRef, authJSON []byte) error {
	if !validAuthJSON(authJSON) {
		return ErrStorage
	}
	encrypted, err := m.cipher.Encrypt(string(authJSON))
	if err != nil || !secrets.IsEncrypted(encrypted) {
		return ErrStorage
	}
	var result sql.Result
	if account.shared {
		result, err = m.db.Exec(`UPDATE shared_codex_account SET auth_blob = ? WHERE singleton = 1`, encrypted)
	} else {
		result, err = m.db.Exec(`UPDATE user_codex_accounts SET auth_blob = ? WHERE user_id = ?`, encrypted, account.userID)
	}
	if err != nil {
		return ErrStorage
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return ErrStorage
	}
	return nil
}

func (m *Manager) deleteAccount(account AccountRef) error {
	var err error
	if account.shared {
		_, err = m.db.Exec(`DELETE FROM shared_codex_account WHERE singleton = 1`)
	} else {
		_, err = m.db.Exec(`DELETE FROM user_codex_accounts WHERE user_id = ?`, account.userID)
	}
	if err != nil {
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
