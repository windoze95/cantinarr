package serversettings

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// KeySeerrAPIKey is the settings row holding the Seerr-compatible API key:
// the one credential Cantinarr issues (rather than holds for another
// service), so that apps built against Seerr's API can read the request
// ledger. Encrypted at rest like every other secret; its own row so the
// server_settings blob stays plaintext.
const KeySeerrAPIKey = "seerr_api_key"

// seerrAPIKeyPrefix makes a key recognisable in a pasted config or a log
// scrubber without saying anything about its bearer.
const seerrAPIKeyPrefix = "cantinarr-"

// SeerrAPIKey is the issued key and who it acts as. Every call made with it
// is authorized as UserID, the administrator who generated it, so the key
// dies with that account and with its admin role.
type SeerrAPIKey struct {
	Key       string    `json:"key"`
	UserID    int64     `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
}

// Configured reports whether a key is issued.
func (k SeerrAPIKey) Configured() bool { return k.Key != "" }

// SeerrAPIKey reads the issued key. A row that cannot be decrypted or parsed
// is an error, never "no key": the admin API answers 500 and the compat
// surface stays closed, rather than either side pretending nothing is set.
func (s *Service) SeerrAPIKey() (SeerrAPIKey, error) {
	var stored string
	err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", KeySeerrAPIKey).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return SeerrAPIKey{}, nil
	}
	if err != nil {
		return SeerrAPIKey{}, fmt.Errorf("read seerr api key: %w", err)
	}
	plain := stored
	if s.cipher != nil {
		if plain, err = s.cipher.Decrypt(stored); err != nil {
			return SeerrAPIKey{}, fmt.Errorf("decrypt seerr api key: %w", err)
		}
	}
	var key SeerrAPIKey
	if err := json.Unmarshal([]byte(plain), &key); err != nil {
		return SeerrAPIKey{}, fmt.Errorf("parse seerr api key: %w", err)
	}
	return key, nil
}

// IssueSeerrAPIKey generates a fresh key acting as userID and stores it,
// replacing any earlier key at once: the old value stops authenticating on
// the next request.
func (s *Service) IssueSeerrAPIKey(userID int64) (SeerrAPIKey, error) {
	if userID <= 0 {
		return SeerrAPIKey{}, errors.New("an issuing administrator is required")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return SeerrAPIKey{}, fmt.Errorf("generate seerr api key: %w", err)
	}
	key := SeerrAPIKey{
		Key:       seerrAPIKeyPrefix + base64.RawURLEncoding.EncodeToString(raw),
		UserID:    userID,
		CreatedAt: time.Now().UTC().Truncate(time.Second),
	}
	encoded, err := json.Marshal(key)
	if err != nil {
		return SeerrAPIKey{}, err
	}
	value := string(encoded)
	if s.cipher != nil {
		if value, err = s.cipher.Encrypt(value); err != nil {
			return SeerrAPIKey{}, fmt.Errorf("encrypt seerr api key: %w", err)
		}
	}
	if _, err := s.db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", KeySeerrAPIKey, value); err != nil {
		return SeerrAPIKey{}, fmt.Errorf("save seerr api key: %w", err)
	}
	return key, nil
}

// RevokeSeerrAPIKey deletes the key; the compat surface answers 401 until a
// new one is issued.
func (s *Service) RevokeSeerrAPIKey() error {
	if _, err := s.db.Exec("DELETE FROM settings WHERE key = ?", KeySeerrAPIKey); err != nil {
		return fmt.Errorf("revoke seerr api key: %w", err)
	}
	return nil
}
