package credentials

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// ErrAIStorage means Cantinarr could not safely evaluate stored AI settings or
// credentials. Callers must fail closed rather than silently choosing a
// different account or billing source.
var ErrAIStorage = errors.New("AI credential storage unavailable")

// AIProfile is one coherent provider/model/credential snapshot. APIKey never
// crosses the credentials package except to the request-scoped provider
// runner. CredentialPresent distinguishes a missing key from storage failure.
type AIProfile struct {
	Config            AIConfig
	APIKey            string
	CredentialPresent bool
}

// LoadUserAIProfile resolves selection and matching key from one read
// transaction. found=true is an explicit override even if its credential is
// absent; callers must not fall through to the shared profile.
func (r *Registry) LoadUserAIProfile(ctx context.Context, userID int64) (profile AIProfile, found bool, err error) {
	if r == nil || r.db == nil || r.cipher == nil || userID <= 0 {
		return AIProfile{}, false, ErrAIStorage
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return AIProfile{}, false, ErrAIStorage
	}
	defer tx.Rollback()
	var stored sql.NullString
	err = tx.QueryRow(`
		SELECT s.provider, s.model, c.credential_blob
		FROM user_ai_settings s
		LEFT JOIN user_ai_credentials c
			ON c.user_id = s.user_id AND c.provider = s.provider
		WHERE s.user_id = ?`, userID).
		Scan(&profile.Config.Provider, &profile.Config.Model, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return AIProfile{}, false, nil
	}
	if err != nil {
		return AIProfile{}, false, ErrAIStorage
	}
	profile.Config.Provider = strings.TrimSpace(profile.Config.Provider)
	profile.Config.Model = strings.TrimSpace(profile.Config.Model)
	if !IsValidAIProvider(profile.Config.Provider) || profile.Config.Model == "" {
		return profile, true, ErrAIStorage
	}
	if key := AIKeyCredentialKey(profile.Config.Provider); key != "" && stored.Valid {
		if stored.String == "" || !secrets.IsEncrypted(stored.String) {
			return profile, true, ErrAIStorage
		}
		profile.APIKey, err = r.cipher.Decrypt(stored.String)
		if err != nil || strings.TrimSpace(profile.APIKey) == "" {
			profile.APIKey = ""
			return profile, true, ErrAIStorage
		}
		profile.CredentialPresent = true
	}
	if err := tx.Commit(); err != nil {
		return AIProfile{}, true, ErrAIStorage
	}
	return profile, true, nil
}

// LoadSharedAIProfileForUser reads the user's grant plus the shared selection
// and matching API key from one SQLite snapshot.
func (r *Registry) LoadSharedAIProfileForUser(ctx context.Context, userID int64) (AIProfile, bool, error) {
	if r == nil || r.db == nil || r.cipher == nil || userID <= 0 {
		return AIProfile{}, false, ErrAIStorage
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return AIProfile{}, false, ErrAIStorage
	}
	defer tx.Rollback()
	var granted bool
	if err := tx.QueryRow(`SELECT ai_shared_enabled FROM users WHERE id = ?`, userID).Scan(&granted); err != nil {
		return AIProfile{}, false, ErrAIStorage
	}
	profile, err := r.loadSharedAIProfileTx(tx)
	if err != nil {
		return profile, granted, err
	}
	if err := tx.Commit(); err != nil {
		return AIProfile{}, granted, ErrAIStorage
	}
	return profile, granted, nil
}

// LoadSharedAIProfile reads the admin-funded selection and matching API key in
// one snapshot without a per-user grant check (admin/setup surfaces).
func (r *Registry) LoadSharedAIProfile(ctx context.Context) (AIProfile, error) {
	if r == nil || r.db == nil || r.cipher == nil {
		return AIProfile{}, ErrAIStorage
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return AIProfile{}, ErrAIStorage
	}
	defer tx.Rollback()
	profile, err := r.loadSharedAIProfileTx(tx)
	if err != nil {
		return profile, err
	}
	if err := tx.Commit(); err != nil {
		return AIProfile{}, ErrAIStorage
	}
	return profile, nil
}

func (r *Registry) loadSharedAIProfileTx(tx *sql.Tx) (AIProfile, error) {
	provider, providerFound, err := settingTx(tx, KeyAIProvider)
	if err != nil {
		return AIProfile{}, err
	}
	model, modelFound, err := settingTx(tx, KeyAIModel)
	if err != nil {
		return AIProfile{}, err
	}
	if !modelFound || model == "" {
		model = os.Getenv("CANTINARR_AI_MODEL")
	}
	model = strings.TrimSpace(model)
	if !providerFound || provider == "" {
		provider = os.Getenv("CANTINARR_AI_PROVIDER")
	}
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = inferAIProvider(model)
		if provider == "" {
			provider = DefaultAIProvider
		}
	} else if !IsValidAIProvider(provider) {
		return AIProfile{Config: AIConfig{Provider: provider, Model: model}}, ErrAIStorage
	}
	if model == "" {
		model = DefaultAIModel(provider)
	}
	profile := AIProfile{Config: AIConfig{Provider: provider, Model: model}}
	key := AIKeyCredentialKey(provider)
	if key == "" {
		return profile, nil
	}
	var stored string
	err = tx.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return profile, nil
	}
	if err != nil || strings.TrimSpace(stored) == "" || !secrets.IsEncrypted(stored) {
		return profile, ErrAIStorage
	}
	profile.APIKey, err = r.cipher.Decrypt(stored)
	if err != nil || strings.TrimSpace(profile.APIKey) == "" {
		profile.APIKey = ""
		return profile, ErrAIStorage
	}
	profile.CredentialPresent = true
	return profile, nil
}

func settingTx(tx *sql.Tx, key string) (string, bool, error) {
	var value string
	err := tx.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, ErrAIStorage
	}
	return strings.TrimSpace(value), true, nil
}

// GetUserAIConfig returns the user's explicit personal selection. found=false
// is the only state in which the shared profile may be considered.
func (r *Registry) GetUserAIConfig(userID int64) (cfg AIConfig, found bool, err error) {
	if r == nil || r.db == nil || userID <= 0 {
		return AIConfig{}, false, ErrAIStorage
	}
	err = r.db.QueryRow(`SELECT provider, model FROM user_ai_settings WHERE user_id = ?`, userID).
		Scan(&cfg.Provider, &cfg.Model)
	if errors.Is(err, sql.ErrNoRows) {
		return AIConfig{}, false, nil
	}
	if err != nil {
		return AIConfig{}, false, ErrAIStorage
	}
	cfg.Provider = strings.TrimSpace(cfg.Provider)
	cfg.Model = strings.TrimSpace(cfg.Model)
	if !IsValidAIProvider(cfg.Provider) || cfg.Model == "" {
		return AIConfig{}, true, ErrAIStorage
	}
	return cfg, true, nil
}

// SetUserAIConfig upserts one explicit, fail-closed personal provider choice.
func (r *Registry) SetUserAIConfig(userID int64, provider, model string) error {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if userID <= 0 || !IsValidAIProvider(provider) {
		return fmt.Errorf("invalid personal AI settings")
	}
	if model == "" {
		model = DefaultAIModel(provider)
	}
	_, err := r.db.Exec(`
		INSERT INTO user_ai_settings (user_id, provider, model, updated_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(user_id) DO UPDATE SET
			provider = excluded.provider,
			model = excluded.model,
			updated_at = CURRENT_TIMESTAMP`, userID, provider, model)
	return err
}

// DeleteUserAIConfig disables the personal override without deleting retained
// personal credentials. The user can explicitly erase each credential.
func (r *Registry) DeleteUserAIConfig(userID int64) error {
	if userID <= 0 {
		return fmt.Errorf("invalid user")
	}
	_, err := r.db.Exec(`DELETE FROM user_ai_settings WHERE user_id = ?`, userID)
	return err
}

// SetUserAICredential stores a personal API key encrypted at rest.
func (r *Registry) SetUserAICredential(userID int64, provider, value string) error {
	provider = strings.TrimSpace(provider)
	value = strings.TrimSpace(value)
	if userID <= 0 || AIKeyCredentialKey(provider) == "" || value == "" {
		return fmt.Errorf("invalid personal AI credential")
	}
	encrypted, err := r.cipher.Encrypt(value)
	if err != nil || !secrets.IsEncrypted(encrypted) {
		return ErrAIStorage
	}
	_, err = r.db.Exec(`
		INSERT INTO user_ai_credentials (user_id, provider, credential_blob, updated_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(user_id, provider) DO UPDATE SET
			credential_blob = excluded.credential_blob,
			updated_at = CURRENT_TIMESTAMP`, userID, provider, encrypted)
	return err
}

// UserAICredential returns a decrypted personal API key. A corrupt or
// plaintext row is a storage error, never an absent credential.
func (r *Registry) UserAICredential(userID int64, provider string) (value string, found bool, err error) {
	if r == nil || r.db == nil || r.cipher == nil || userID <= 0 || AIKeyCredentialKey(provider) == "" {
		return "", false, ErrAIStorage
	}
	var stored string
	err = r.db.QueryRow(`
		SELECT credential_blob FROM user_ai_credentials
		WHERE user_id = ? AND provider = ?`, userID, provider).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil || stored == "" || !secrets.IsEncrypted(stored) {
		return "", true, ErrAIStorage
	}
	value, err = r.cipher.Decrypt(stored)
	if err != nil || strings.TrimSpace(value) == "" {
		return "", true, ErrAIStorage
	}
	return value, true, nil
}

// UserAICredentialConfigured reports only row presence and never decrypts or
// exposes a secret. Resolution still decrypts and fails closed on corruption.
func (r *Registry) UserAICredentialConfigured(userID int64, provider string) (bool, error) {
	if r == nil || r.db == nil || userID <= 0 || AIKeyCredentialKey(provider) == "" {
		return false, ErrAIStorage
	}
	var one int
	err := r.db.QueryRow(`SELECT 1 FROM user_ai_credentials WHERE user_id = ? AND provider = ?`, userID, provider).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, ErrAIStorage
	}
	return one == 1, nil
}

func (r *Registry) DeleteUserAICredential(userID int64, provider string) error {
	if userID <= 0 || AIKeyCredentialKey(provider) == "" {
		return fmt.Errorf("invalid personal AI credential")
	}
	_, err := r.db.Exec(`DELETE FROM user_ai_credentials WHERE user_id = ? AND provider = ?`, userID, provider)
	return err
}
