package instance

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// Instance represents a configured service instance (Radarr, Sonarr, SABnzbd,
// or qBittorrent). Radarr/Sonarr/SABnzbd authenticate with an API key;
// qBittorrent authenticates with username/password.
type Instance struct {
	ID          string    `json:"id"`
	ServiceType string    `json:"service_type"` // "radarr", "sonarr", "sabnzbd", or "qbittorrent"
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	APIKey      string    `json:"api_key"`
	Username    string    `json:"username"`
	Password    string    `json:"password"`
	IsDefault   bool      `json:"is_default"`
	SortOrder   int       `json:"sort_order"`
	CreatedAt   time.Time `json:"created_at"`
}

const instanceColumns = "id, service_type, name, url, api_key, username, password, is_default, sort_order, created_at"

// Store provides CRUD operations for service instances. API keys and
// passwords are encrypted at rest; legacy plaintext rows decrypt as-is.
type Store struct {
	db     *sql.DB
	cipher *secrets.Cipher
}

// NewStore creates a new instance store.
func NewStore(db *sql.DB, cipher *secrets.Cipher) *Store {
	return &Store{db: db, cipher: cipher}
}

// decryptSecrets resolves stored secret fields to plaintext for callers.
func (s *Store) decryptSecrets(inst *Instance) error {
	apiKey, err := s.cipher.Decrypt(inst.APIKey)
	if err != nil {
		return fmt.Errorf("decrypt api key for %s (wrong encryption key?): %w", inst.ID, err)
	}
	password, err := s.cipher.Decrypt(inst.Password)
	if err != nil {
		return fmt.Errorf("decrypt password for %s (wrong encryption key?): %w", inst.ID, err)
	}
	inst.APIKey, inst.Password = apiKey, password
	return nil
}

// encryptSecrets returns the at-rest representations of the secret fields.
func (s *Store) encryptSecrets(inst *Instance) (apiKey, password string, err error) {
	if apiKey, err = s.cipher.Encrypt(inst.APIKey); err != nil {
		return "", "", fmt.Errorf("encrypt api key: %w", err)
	}
	if password, err = s.cipher.Encrypt(inst.Password); err != nil {
		return "", "", fmt.Errorf("encrypt password: %w", err)
	}
	return apiKey, password, nil
}

// List returns all instances of the given service type, ordered by sort_order.
func (s *Store) List(serviceType string) ([]Instance, error) {
	rows, err := s.db.Query(
		"SELECT "+instanceColumns+" FROM service_instances WHERE service_type = ? ORDER BY sort_order, name",
		serviceType,
	)
	if err != nil {
		return nil, fmt.Errorf("list instances: %w", err)
	}
	defer rows.Close()
	return s.scanInstances(rows)
}

// ListAll returns all instances across all service types.
func (s *Store) ListAll() ([]Instance, error) {
	rows, err := s.db.Query(
		"SELECT " + instanceColumns + " FROM service_instances ORDER BY service_type, sort_order, name",
	)
	if err != nil {
		return nil, fmt.Errorf("list all instances: %w", err)
	}
	defer rows.Close()
	return s.scanInstances(rows)
}

// Get returns a single instance by ID.
func (s *Store) Get(id string) (*Instance, error) {
	var inst Instance
	err := s.db.QueryRow(
		"SELECT "+instanceColumns+" FROM service_instances WHERE id = ?",
		id,
	).Scan(&inst.ID, &inst.ServiceType, &inst.Name, &inst.URL, &inst.APIKey, &inst.Username, &inst.Password, &inst.IsDefault, &inst.SortOrder, &inst.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get instance: %w", err)
	}
	if err := s.decryptSecrets(&inst); err != nil {
		return nil, err
	}
	return &inst, nil
}

// Create inserts a new instance and returns it with a generated ID.
func (s *Store) Create(inst *Instance) error {
	if inst.ID == "" {
		inst.ID = inst.ServiceType + "-" + uuid.New().String()[:8]
	}
	inst.CreatedAt = time.Now()

	apiKey, password, err := s.encryptSecrets(inst)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		"INSERT INTO service_instances (id, service_type, name, url, api_key, username, password, is_default, sort_order, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		inst.ID, inst.ServiceType, inst.Name, inst.URL, apiKey, inst.Username, password, inst.IsDefault, inst.SortOrder, inst.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create instance: %w", err)
	}
	return nil
}

// Update modifies an existing instance.
func (s *Store) Update(inst *Instance) error {
	apiKey, password, err := s.encryptSecrets(inst)
	if err != nil {
		return err
	}
	result, err := s.db.Exec(
		"UPDATE service_instances SET name = ?, url = ?, api_key = ?, username = ?, password = ?, is_default = ?, sort_order = ? WHERE id = ?",
		inst.Name, inst.URL, apiKey, inst.Username, password, inst.IsDefault, inst.SortOrder, inst.ID,
	)
	if err != nil {
		return fmt.Errorf("update instance: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("instance not found: %s", inst.ID)
	}
	return nil
}

// WebhookToken returns the instance's webhook bearer token, generating and
// persisting one on first use (which also backfills instances that predate the
// column). The token authorizes the arrs' Connect→Webhook callbacks — those
// requests carry no user session, so the token IS the auth. Encrypted at rest
// like the other instance secrets.
func (s *Store) WebhookToken(id string) (string, error) {
	var stored string
	err := s.db.QueryRow(
		"SELECT webhook_token FROM service_instances WHERE id = ?", id,
	).Scan(&stored)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("instance not found: %s", id)
	}
	if err != nil {
		return "", fmt.Errorf("get webhook token: %w", err)
	}
	if stored != "" {
		token, err := s.cipher.Decrypt(stored)
		if err != nil {
			return "", fmt.Errorf("decrypt webhook token for %s (wrong encryption key?): %w", id, err)
		}
		return token, nil
	}

	token, err := newWebhookToken()
	if err != nil {
		return "", err
	}
	encrypted, err := s.cipher.Encrypt(token)
	if err != nil {
		return "", fmt.Errorf("encrypt webhook token: %w", err)
	}
	// Claim only the empty slot; a concurrent first read may have won.
	res, err := s.db.Exec(
		"UPDATE service_instances SET webhook_token = ? WHERE id = ? AND webhook_token = ''",
		encrypted, id,
	)
	if err != nil {
		return "", fmt.Errorf("store webhook token: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return token, nil
	}
	// Lost the race: return the winner's token.
	return s.WebhookToken(id)
}

func newWebhookToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate webhook token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// Delete removes an instance by ID.
func (s *Store) Delete(id string) error {
	result, err := s.db.Exec("DELETE FROM service_instances WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete instance: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("instance not found: %s", id)
	}
	// Drop any per-user defaults/grants that pointed at this instance so a
	// deleted instance neither lingers as someone's default nor (for chaptarr)
	// keeps granting access to a now-removed instance.
	if _, err := s.db.Exec("DELETE FROM user_default_instances WHERE instance_id = ?", id); err != nil {
		return fmt.Errorf("delete instance user defaults: %w", err)
	}
	return nil
}

// GetDefault returns the default instance for a service type.
func (s *Store) GetDefault(serviceType string) (*Instance, error) {
	var inst Instance
	err := s.db.QueryRow(
		"SELECT "+instanceColumns+" FROM service_instances WHERE service_type = ? AND is_default = 1 LIMIT 1",
		serviceType,
	).Scan(&inst.ID, &inst.ServiceType, &inst.Name, &inst.URL, &inst.APIKey, &inst.Username, &inst.Password, &inst.IsDefault, &inst.SortOrder, &inst.CreatedAt)
	if err == sql.ErrNoRows {
		// Fall back to first instance
		err = s.db.QueryRow(
			"SELECT "+instanceColumns+" FROM service_instances WHERE service_type = ? ORDER BY sort_order LIMIT 1",
			serviceType,
		).Scan(&inst.ID, &inst.ServiceType, &inst.Name, &inst.URL, &inst.APIKey, &inst.Username, &inst.Password, &inst.IsDefault, &inst.SortOrder, &inst.CreatedAt)
	}
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get default instance: %w", err)
	}
	if err := s.decryptSecrets(&inst); err != nil {
		return nil, err
	}
	return &inst, nil
}

// GetUserDefault returns the instance ID a user has pinned as their default for
// a service type, or ("", false, nil) when the user has no per-user override
// (the caller should fall back to the global default). For service types with no
// global default (chaptarr), the returned ID is also the access grant.
func (s *Store) GetUserDefault(userID int64, serviceType string) (string, bool, error) {
	var instanceID string
	err := s.db.QueryRow(
		"SELECT instance_id FROM user_default_instances WHERE user_id = ? AND service_type = ?",
		userID, serviceType,
	).Scan(&instanceID)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get user default instance: %w", err)
	}
	return instanceID, true, nil
}

// ListUserDefaults returns a user's per-user default overrides keyed by service
// type. Service types absent from the map inherit the global default.
func (s *Store) ListUserDefaults(userID int64) (map[string]string, error) {
	rows, err := s.db.Query(
		"SELECT service_type, instance_id FROM user_default_instances WHERE user_id = ?",
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list user default instances: %w", err)
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var serviceType, instanceID string
		if err := rows.Scan(&serviceType, &instanceID); err != nil {
			return nil, fmt.Errorf("scan user default instance: %w", err)
		}
		out[serviceType] = instanceID
	}
	return out, rows.Err()
}

// SetUserDefault pins instanceID as userID's default for serviceType. The
// instance must exist and its stored service type must match serviceType, so a
// single admin endpoint can accept a {service_type: instance_id} map without
// risking a mismatched pin.
func (s *Store) SetUserDefault(userID int64, serviceType, instanceID string) error {
	inst, err := s.Get(instanceID)
	if err != nil {
		return err
	}
	if inst == nil {
		return fmt.Errorf("instance not found: %s", instanceID)
	}
	if inst.ServiceType != serviceType {
		return fmt.Errorf("instance %s is %q, not %q", instanceID, inst.ServiceType, serviceType)
	}
	_, err = s.db.Exec(
		"INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (?, ?, ?) "+
			"ON CONFLICT(user_id, service_type) DO UPDATE SET instance_id = excluded.instance_id",
		userID, serviceType, instanceID,
	)
	if err != nil {
		return fmt.Errorf("set user default instance: %w", err)
	}
	return nil
}

// ClearUserDefault removes a user's per-user override for a service type, so it
// reverts to the global default (or, for chaptarr, revokes access).
func (s *Store) ClearUserDefault(userID int64, serviceType string) error {
	if _, err := s.db.Exec(
		"DELETE FROM user_default_instances WHERE user_id = ? AND service_type = ?",
		userID, serviceType,
	); err != nil {
		return fmt.Errorf("clear user default instance: %w", err)
	}
	return nil
}

// UserHasInstanceAccess reports whether a user has been granted a specific
// instance via a per-user default row. Used to gate access to service types that
// have no global default (chaptarr); admins bypass this check at the caller.
func (s *Store) UserHasInstanceAccess(userID int64, instanceID string) (bool, error) {
	var one int
	err := s.db.QueryRow(
		"SELECT 1 FROM user_default_instances WHERE user_id = ? AND instance_id = ? LIMIT 1",
		userID, instanceID,
	).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check user instance access: %w", err)
	}
	return true, nil
}

// Count returns the number of instances for a service type.
func (s *Store) Count(serviceType string) (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM service_instances WHERE service_type = ?", serviceType).Scan(&count)
	return count, err
}

func (s *Store) scanInstances(rows *sql.Rows) ([]Instance, error) {
	var instances []Instance
	for rows.Next() {
		var inst Instance
		if err := rows.Scan(&inst.ID, &inst.ServiceType, &inst.Name, &inst.URL, &inst.APIKey, &inst.Username, &inst.Password, &inst.IsDefault, &inst.SortOrder, &inst.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan instance: %w", err)
		}
		if err := s.decryptSecrets(&inst); err != nil {
			// Degrade rather than fail the whole listing: an undecryptable
			// row would otherwise brick the instances admin UI, leaving no
			// way to view, fix, or delete the broken entry. Secrets are
			// blanked; paths that need the plaintext (client construction
			// via Get/GetDefault) still fail loudly.
			log.Printf("instance: %v — listing %s with blanked credentials", err, inst.ID)
			inst.APIKey, inst.Password = "", ""
		}
		instances = append(instances, inst)
	}
	return instances, rows.Err()
}
