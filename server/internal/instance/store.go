package instance

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Instance represents a configured Radarr or Sonarr service instance.
type Instance struct {
	ID          string    `json:"id"`
	ServiceType string    `json:"service_type"` // "radarr" or "sonarr"
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	APIKey      string    `json:"api_key"`
	IsDefault   bool      `json:"is_default"`
	SortOrder   int       `json:"sort_order"`
	CreatedAt   time.Time `json:"created_at"`
}

// Store provides CRUD operations for service instances.
type Store struct {
	db *sql.DB
}

// NewStore creates a new instance store.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// List returns all instances of the given service type, ordered by sort_order.
func (s *Store) List(serviceType string) ([]Instance, error) {
	rows, err := s.db.Query(
		"SELECT id, service_type, name, url, api_key, is_default, sort_order, created_at FROM service_instances WHERE service_type = ? ORDER BY sort_order, name",
		serviceType,
	)
	if err != nil {
		return nil, fmt.Errorf("list instances: %w", err)
	}
	defer rows.Close()
	return scanInstances(rows)
}

// ListAll returns all instances across all service types.
func (s *Store) ListAll() ([]Instance, error) {
	rows, err := s.db.Query(
		"SELECT id, service_type, name, url, api_key, is_default, sort_order, created_at FROM service_instances ORDER BY service_type, sort_order, name",
	)
	if err != nil {
		return nil, fmt.Errorf("list all instances: %w", err)
	}
	defer rows.Close()
	return scanInstances(rows)
}

// Get returns a single instance by ID.
func (s *Store) Get(id string) (*Instance, error) {
	var inst Instance
	err := s.db.QueryRow(
		"SELECT id, service_type, name, url, api_key, is_default, sort_order, created_at FROM service_instances WHERE id = ?",
		id,
	).Scan(&inst.ID, &inst.ServiceType, &inst.Name, &inst.URL, &inst.APIKey, &inst.IsDefault, &inst.SortOrder, &inst.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get instance: %w", err)
	}
	return &inst, nil
}

// Create inserts a new instance and returns it with a generated ID.
func (s *Store) Create(inst *Instance) error {
	if inst.ID == "" {
		inst.ID = inst.ServiceType + "-" + uuid.New().String()[:8]
	}
	inst.CreatedAt = time.Now()

	_, err := s.db.Exec(
		"INSERT INTO service_instances (id, service_type, name, url, api_key, is_default, sort_order, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		inst.ID, inst.ServiceType, inst.Name, inst.URL, inst.APIKey, inst.IsDefault, inst.SortOrder, inst.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create instance: %w", err)
	}
	return nil
}

// Update modifies an existing instance.
func (s *Store) Update(inst *Instance) error {
	result, err := s.db.Exec(
		"UPDATE service_instances SET name = ?, url = ?, api_key = ?, is_default = ?, sort_order = ? WHERE id = ?",
		inst.Name, inst.URL, inst.APIKey, inst.IsDefault, inst.SortOrder, inst.ID,
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
		"SELECT id, service_type, name, url, api_key, is_default, sort_order, created_at FROM service_instances WHERE service_type = ? AND is_default = 1 LIMIT 1",
		serviceType,
	).Scan(&inst.ID, &inst.ServiceType, &inst.Name, &inst.URL, &inst.APIKey, &inst.IsDefault, &inst.SortOrder, &inst.CreatedAt)
	if err == sql.ErrNoRows {
		// Fall back to first instance
		err = s.db.QueryRow(
			"SELECT id, service_type, name, url, api_key, is_default, sort_order, created_at FROM service_instances WHERE service_type = ? ORDER BY sort_order LIMIT 1",
			serviceType,
		).Scan(&inst.ID, &inst.ServiceType, &inst.Name, &inst.URL, &inst.APIKey, &inst.IsDefault, &inst.SortOrder, &inst.CreatedAt)
	}
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get default instance: %w", err)
	}
	return &inst, nil
}

// Count returns the number of instances for a service type.
func (s *Store) Count(serviceType string) (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM service_instances WHERE service_type = ?", serviceType).Scan(&count)
	return count, err
}

func scanInstances(rows *sql.Rows) ([]Instance, error) {
	var instances []Instance
	for rows.Next() {
		var inst Instance
		if err := rows.Scan(&inst.ID, &inst.ServiceType, &inst.Name, &inst.URL, &inst.APIKey, &inst.IsDefault, &inst.SortOrder, &inst.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan instance: %w", err)
		}
		instances = append(instances, inst)
	}
	return instances, rows.Err()
}
