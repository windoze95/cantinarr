package grokoauth

import (
	"database/sql"
	"errors"
	"time"

	"github.com/windoze95/cantinarr-server/internal/secrets"
)

type accountRecord struct {
	auth     storedAuth
	email    string
	planType string
}

// AccountExists reports whether encrypted authorization material exists for
// one personal or shared account. Storage failures remain distinguishable
// from absence so provider resolution can fail closed.
func (m *Manager) AccountExists(account AccountRef) (bool, error) {
	if m == nil || m.db == nil || m.cipher == nil || !account.valid() {
		return false, ErrInvalidInput
	}
	_, found, err := m.loadAccount(account)
	if err != nil {
		return false, err
	}
	return found, nil
}

func (m *Manager) loadAccount(account AccountRef) (accountRecord, bool, error) {
	var storedBlob, email, planType string
	var err error
	if account.shared {
		err = m.db.QueryRow(`
			SELECT auth_blob, email, plan_type
			FROM shared_grok_account WHERE singleton = 1`,
		).Scan(&storedBlob, &email, &planType)
	} else {
		err = m.db.QueryRow(`
			SELECT auth_blob, email, plan_type
			FROM user_grok_accounts WHERE user_id = ?`, account.userID,
		).Scan(&storedBlob, &email, &planType)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return accountRecord{}, false, nil
	}
	if err != nil {
		return accountRecord{}, false, ErrStorage
	}
	// This table has never had a plaintext format. Fail closed if a row was
	// manually inserted or corrupted rather than trusting it.
	if storedBlob == "" || !secrets.IsEncrypted(storedBlob) {
		return accountRecord{}, false, ErrStorage
	}
	plain, err := m.cipher.Decrypt(storedBlob)
	if err != nil || !validAuthJSON([]byte(plain)) {
		return accountRecord{}, false, ErrStorage
	}
	record := accountRecord{email: email, planType: planType}
	if err := unmarshalAuth([]byte(plain), &record.auth); err != nil {
		return accountRecord{}, false, ErrStorage
	}
	return record, true, nil
}

func (m *Manager) accountMetadata(account AccountRef) (AccountStatus, bool, error) {
	var storedBlob, email, planType string
	var updatedUnix int64
	var err error
	if account.shared {
		err = m.db.QueryRow(`
			SELECT auth_blob, email, plan_type,
				COALESCE(CAST(strftime('%s', updated_at) AS INTEGER), 0)
			FROM shared_grok_account WHERE singleton = 1`,
		).Scan(&storedBlob, &email, &planType, &updatedUnix)
	} else {
		err = m.db.QueryRow(`
			SELECT auth_blob, email, plan_type,
				COALESCE(CAST(strftime('%s', updated_at) AS INTEGER), 0)
			FROM user_grok_accounts WHERE user_id = ?`, account.userID,
		).Scan(&storedBlob, &email, &planType, &updatedUnix)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return AccountStatus{}, false, nil
	}
	if err != nil {
		return AccountStatus{}, false, ErrStorage
	}
	if storedBlob == "" || !secrets.IsEncrypted(storedBlob) {
		return AccountStatus{}, false, ErrStorage
	}
	status := AccountStatus{Connected: true, Email: email, PlanType: planType}
	if updatedUnix > 0 {
		status.UpdatedAt = time.Unix(updatedUnix, 0).UTC()
	}
	return status, true, nil
}

func (m *Manager) saveAccount(account AccountRef, auth storedAuth, email, planType string) error {
	blob, err := marshalAuth(auth)
	if err != nil {
		return ErrStorage
	}
	encrypted, err := m.cipher.Encrypt(string(blob))
	if err != nil || !secrets.IsEncrypted(encrypted) {
		return ErrStorage
	}
	if account.shared {
		_, err = m.db.Exec(`
			INSERT INTO shared_grok_account
				(singleton, auth_blob, email, plan_type, updated_at)
			VALUES (1, ?, ?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(singleton) DO UPDATE SET
				auth_blob = excluded.auth_blob,
				email = excluded.email,
				plan_type = excluded.plan_type,
				updated_at = CURRENT_TIMESTAMP`, encrypted, email, planType)
	} else {
		_, err = m.db.Exec(`
			INSERT INTO user_grok_accounts
				(user_id, auth_blob, email, plan_type, updated_at)
			VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(user_id) DO UPDATE SET
				auth_blob = excluded.auth_blob,
				email = excluded.email,
				plan_type = excluded.plan_type,
				updated_at = CURRENT_TIMESTAMP`,
			account.userID, encrypted, email, planType)
	}
	if err != nil {
		return ErrStorage
	}
	return nil
}

// saveRefreshedAuth persists a rotated token pair for an existing account.
// Refresh tokens are single-use upstream, so the update must land before the
// new pair is handed to any caller.
func (m *Manager) saveRefreshedAuth(account AccountRef, auth storedAuth) error {
	blob, err := marshalAuth(auth)
	if err != nil {
		return ErrStorage
	}
	encrypted, err := m.cipher.Encrypt(string(blob))
	if err != nil || !secrets.IsEncrypted(encrypted) {
		return ErrStorage
	}
	var result sql.Result
	if account.shared {
		result, err = m.db.Exec(`UPDATE shared_grok_account SET auth_blob = ? WHERE singleton = 1`, encrypted)
	} else {
		result, err = m.db.Exec(`UPDATE user_grok_accounts SET auth_blob = ? WHERE user_id = ?`, encrypted, account.userID)
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
		_, err = m.db.Exec(`DELETE FROM shared_grok_account WHERE singleton = 1`)
	} else {
		_, err = m.db.Exec(`DELETE FROM user_grok_accounts WHERE user_id = ?`, account.userID)
	}
	if err != nil {
		return ErrStorage
	}
	return nil
}
