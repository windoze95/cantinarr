package instance

import (
	"database/sql"
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
