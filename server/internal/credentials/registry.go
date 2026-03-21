package credentials

import (
	"database/sql"
	"sync"

	"github.com/windoze95/cantinarr-server/internal/tmdb"
	"github.com/windoze95/cantinarr-server/internal/trakt"
)

// Credential keys stored in the settings table.
const (
	KeyTMDBAccessToken   = "tmdb_access_token"
	KeyAnthropicKey      = "anthropic_key"
	KeyTraktClientID = "trakt_client_id"
)

// AllKeys lists every credential key the system manages.
var AllKeys = []string{KeyTMDBAccessToken, KeyAnthropicKey, KeyTraktClientID}

// Registry lazily creates and caches TMDB/Trakt clients from DB-stored credentials.
type Registry struct {
	db *sql.DB

	mu          sync.RWMutex
	cachedTMDB  *tmdb.Client
	cachedTrakt *trakt.Client
	loaded      bool // true once we've attempted to load from DB
}

// NewRegistry creates a new credentials registry.
func NewRegistry(db *sql.DB) *Registry {
	return &Registry{db: db}
}

// TMDB returns the cached TMDB client, creating it lazily from the DB credential.
// Returns nil if the credential is not set.
func (r *Registry) TMDB() *tmdb.Client {
	r.mu.RLock()
	if r.loaded {
		c := r.cachedTMDB
		r.mu.RUnlock()
		return c
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loaded {
		return r.cachedTMDB
	}
	r.load()
	return r.cachedTMDB
}

// Trakt returns the cached Trakt client, creating it lazily from the DB credential.
// Returns nil if the credential is not set.
func (r *Registry) Trakt() *trakt.Client {
	r.mu.RLock()
	if r.loaded {
		c := r.cachedTrakt
		r.mu.RUnlock()
		return c
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loaded {
		return r.cachedTrakt
	}
	r.load()
	return r.cachedTrakt
}

// GetCredential reads a raw credential value from the DB.
// Returns empty string if not set.
func (r *Registry) GetCredential(key string) string {
	var value string
	err := r.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err != nil {
		return ""
	}
	return value
}

// SetCredential writes a credential to the DB (upsert).
func (r *Registry) SetCredential(key, value string) error {
	_, err := r.db.Exec(
		"INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)",
		key, value,
	)
	return err
}

// DeleteCredential removes a credential from the DB.
func (r *Registry) DeleteCredential(key string) error {
	_, err := r.db.Exec("DELETE FROM settings WHERE key = ?", key)
	return err
}

// IsConfigured checks whether a credential key has a value in the DB.
func (r *Registry) IsConfigured(key string) bool {
	var count int
	r.db.QueryRow("SELECT COUNT(*) FROM settings WHERE key = ?", key).Scan(&count)
	return count > 0
}

// Invalidate clears all cached clients, forcing recreation on next access.
func (r *Registry) Invalidate() {
	r.mu.Lock()
	r.cachedTMDB = nil
	r.cachedTrakt = nil
	r.loaded = false
	r.mu.Unlock()
}

// load reads credentials from DB and creates clients. Must be called under write lock.
func (r *Registry) load() {
	r.loaded = true

	if token := r.getSettingLocked(KeyTMDBAccessToken); token != "" {
		r.cachedTMDB = tmdb.NewClient(token)
	}
	if clientID := r.getSettingLocked(KeyTraktClientID); clientID != "" {
		r.cachedTrakt = trakt.NewClient(clientID)
	}
}

func (r *Registry) getSettingLocked(key string) string {
	var value string
	r.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	return value
}
