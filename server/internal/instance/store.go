package instance

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/windoze95/cantinarr-server/internal/secrets"
)

var ErrPendingBookRequests = errors.New("instance has pending book requests")

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
		"SELECT "+instanceColumns+" FROM service_instances WHERE service_type = ? ORDER BY sort_order, name, id",
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
		"SELECT " + instanceColumns + " FROM service_instances ORDER BY service_type, sort_order, name, id",
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

// normalizeDefault applies the service-type default rules before persisting:
// Chaptarr has no global default — its instances are granted per user — so the
// flag is forced off for it.
func normalizeDefault(inst *Instance) {
	if inst.ServiceType == "chaptarr" {
		inst.IsDefault = false
	}
}

// clearSiblingDefaults keeps at most one default per service type: saving an
// instance as default flips the flag off on every other instance of that type,
// within the caller's transaction.
func clearSiblingDefaults(tx *sql.Tx, inst *Instance) error {
	if !inst.IsDefault {
		return nil
	}
	if _, err := tx.Exec(
		"UPDATE service_instances SET is_default = 0 WHERE service_type = ? AND id <> ?",
		inst.ServiceType, inst.ID,
	); err != nil {
		return fmt.Errorf("clear previous default: %w", err)
	}
	return nil
}

// Create inserts a new instance and returns it with a generated ID.
func (s *Store) Create(inst *Instance) error {
	if inst.ID == "" {
		inst.ID = inst.ServiceType + "-" + uuid.New().String()[:8]
	}
	inst.CreatedAt = time.Now()
	normalizeDefault(inst)

	apiKey, password, err := s.encryptSecrets(inst)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("create instance: %w", err)
	}
	defer tx.Rollback()
	if err := clearSiblingDefaults(tx, inst); err != nil {
		return err
	}
	if _, err := tx.Exec(
		"INSERT INTO service_instances (id, service_type, name, url, api_key, username, password, is_default, sort_order, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		inst.ID, inst.ServiceType, inst.Name, inst.URL, apiKey, inst.Username, password, inst.IsDefault, inst.SortOrder, inst.CreatedAt,
	); err != nil {
		return fmt.Errorf("create instance: %w", err)
	}
	if err := tx.Commit(); err != nil {
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
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("update instance: %w", err)
	}
	defer tx.Rollback()
	// The stored service type is authoritative (it is immutable and the
	// caller's copy may be unset); the default rules key off it.
	err = tx.QueryRow(
		"SELECT service_type FROM service_instances WHERE id = ?", inst.ID,
	).Scan(&inst.ServiceType)
	if err == sql.ErrNoRows {
		return fmt.Errorf("instance not found: %s", inst.ID)
	}
	if err != nil {
		return fmt.Errorf("update instance: %w", err)
	}
	normalizeDefault(inst)
	if err := clearSiblingDefaults(tx, inst); err != nil {
		return err
	}
	if _, err := tx.Exec(
		"UPDATE service_instances SET name = ?, url = ?, api_key = ?, username = ?, password = ?, is_default = ?, sort_order = ? WHERE id = ?",
		inst.Name, inst.URL, apiKey, inst.Username, password, inst.IsDefault, inst.SortOrder, inst.ID,
	); err != nil {
		return fmt.Errorf("update instance: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("update instance: %w", err)
	}
	return nil
}

// WebhookToken returns the instance's webhook callback credential, generating and
// persisting one on first use (which also backfills instances that predate the
// column). It is used as the server-managed webhook's Basic Auth password;
// callbacks carry no user session, so this credential is the auth. It is
// encrypted at rest like the other instance secrets and never returned by the
// instance API.
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

// WebhookTokens returns the currently accepted callback credentials without
// generating one. During a managed rotation both current and pending are valid,
// so a failed or ambiguous remote update cannot break the old webhook.
func (s *Store) WebhookTokens(id string) ([]string, error) {
	var current, pending string
	if err := s.db.QueryRow(
		"SELECT webhook_token, webhook_pending_token FROM service_instances WHERE id = ?", id,
	).Scan(&current, &pending); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("instance not found: %s", id)
		}
		return nil, fmt.Errorf("get webhook credentials: %w", err)
	}
	out := make([]string, 0, 2)
	for _, stored := range []string{current, pending} {
		if stored == "" {
			continue
		}
		token, err := s.cipher.Decrypt(stored)
		if err != nil {
			return nil, fmt.Errorf("decrypt webhook credential for %s (wrong encryption key?): %w", id, err)
		}
		out = append(out, token)
	}
	return out, nil
}

// PrepareWebhookToken returns a stable pending rotation candidate. Concurrent
// calls and retries reuse the same candidate until PromoteWebhookToken commits
// it, avoiding racing credentials in the remote arr configuration.
func (s *Store) PrepareWebhookToken(id string) (string, error) {
	var stored string
	if err := s.db.QueryRow(
		"SELECT webhook_pending_token FROM service_instances WHERE id = ?", id,
	).Scan(&stored); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("instance not found: %s", id)
		}
		return "", fmt.Errorf("get pending webhook credential: %w", err)
	}
	if stored != "" {
		token, err := s.cipher.Decrypt(stored)
		if err != nil {
			return "", fmt.Errorf("decrypt pending webhook credential: %w", err)
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
	res, err := s.db.Exec(
		"UPDATE service_instances SET webhook_pending_token = ? WHERE id = ? AND webhook_pending_token = ''",
		encrypted, id,
	)
	if err != nil {
		return "", fmt.Errorf("prepare webhook credential: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return token, nil
	}
	return s.PrepareWebhookToken(id)
}

// PromoteWebhookToken commits the exact pending candidate after the arr accepts
// it. The operation is idempotent for a lost HTTP response.
func (s *Store) PromoteWebhookToken(id, token string) error {
	var currentStored, pendingStored string
	if err := s.db.QueryRow(
		"SELECT webhook_token, webhook_pending_token FROM service_instances WHERE id = ?", id,
	).Scan(&currentStored, &pendingStored); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("instance not found: %s", id)
		}
		return fmt.Errorf("get webhook rotation state: %w", err)
	}
	if pendingStored == "" {
		if currentStored != "" {
			current, err := s.cipher.Decrypt(currentStored)
			if err == nil && subtle.ConstantTimeCompare([]byte(current), []byte(token)) == 1 {
				return nil
			}
		}
		return fmt.Errorf("webhook rotation candidate is no longer pending")
	}
	pending, err := s.cipher.Decrypt(pendingStored)
	if err != nil {
		return fmt.Errorf("decrypt pending webhook credential: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(pending), []byte(token)) != 1 {
		return fmt.Errorf("webhook rotation candidate changed")
	}
	res, err := s.db.Exec(
		`UPDATE service_instances SET webhook_token = webhook_pending_token, webhook_pending_token = ''
		 WHERE id = ? AND webhook_pending_token = ?`,
		id, pendingStored,
	)
	if err != nil {
		return fmt.Errorf("promote webhook credential: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("webhook rotation changed concurrently")
	}
	return nil
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
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin instance deletion: %w", err)
	}
	defer tx.Rollback()
	var pending int
	err = tx.QueryRow(
		"SELECT COUNT(*) FROM request_log WHERE instance_id = ? AND media_type = 'book' AND status = 'pending'",
		id,
	).Scan(&pending)
	if err != nil {
		return fmt.Errorf("check pending book requests: %w", err)
	}
	if pending > 0 {
		return fmt.Errorf("%w: cannot delete instance while %d book request(s) await approval", ErrPendingBookRequests, pending)
	}
	result, err := tx.Exec("DELETE FROM service_instances WHERE id = ?", id)
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
	if _, err := tx.Exec("DELETE FROM user_default_instances WHERE instance_id = ?", id); err != nil {
		return fmt.Errorf("delete instance user defaults: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit instance deletion: %w", err)
	}
	return nil
}

// GetDefault returns the default instance for a service type. Create/Update
// keep at most one default per type; the ORDER BY makes the pick deterministic
// for legacy rows written before that invariant existed.
func (s *Store) GetDefault(serviceType string) (*Instance, error) {
	var inst Instance
	err := s.db.QueryRow(
		"SELECT "+instanceColumns+" FROM service_instances WHERE service_type = ? AND is_default = 1 ORDER BY sort_order, name, id LIMIT 1",
		serviceType,
	).Scan(&inst.ID, &inst.ServiceType, &inst.Name, &inst.URL, &inst.APIKey, &inst.Username, &inst.Password, &inst.IsDefault, &inst.SortOrder, &inst.CreatedAt)
	if err == sql.ErrNoRows {
		// Fall back to first instance
		err = s.db.QueryRow(
			"SELECT "+instanceColumns+" FROM service_instances WHERE service_type = ? ORDER BY sort_order, name, id LIMIT 1",
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

// ListTypeUserDefaults returns every per-user default row for a service type
// as a user id → pinned instance id map, so the instance admin UI can show who
// is assigned to this instance and who is pinned to a sibling.
func (s *Store) ListTypeUserDefaults(serviceType string) (map[int64]string, error) {
	rows, err := s.db.Query(
		"SELECT user_id, instance_id FROM user_default_instances WHERE service_type = ?",
		serviceType,
	)
	if err != nil {
		return nil, fmt.Errorf("list user defaults for type: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]string)
	for rows.Next() {
		var userID int64
		var instanceID string
		if err := rows.Scan(&userID, &instanceID); err != nil {
			return nil, fmt.Errorf("scan user default for type: %w", err)
		}
		out[userID] = instanceID
	}
	return out, rows.Err()
}

// ServiceTypeOf returns the stored service type for an instance id, or ""
// when the instance does not exist. Unlike Get it never touches the encrypted
// secret columns, so it works even for rows with undecryptable credentials.
func (s *Store) ServiceTypeOf(instanceID string) (string, error) {
	var serviceType string
	err := s.db.QueryRow(
		"SELECT service_type FROM service_instances WHERE id = ?", instanceID,
	).Scan(&serviceType)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get instance service type: %w", err)
	}
	return serviceType, nil
}

// SetInstanceUsers pins instanceID as the per-user default for exactly
// userIDs: listed users are pinned to it (moving off a sibling instance if
// needed), and users previously pinned to THIS instance but absent from the
// list revert to the global default (for chaptarr: access revoked). Pins to
// sibling instances are otherwise untouched.
func (s *Store) SetInstanceUsers(instanceID string, userIDs []int64) error {
	serviceType, err := s.ServiceTypeOf(instanceID)
	if err != nil {
		return err
	}
	if serviceType == "" {
		return fmt.Errorf("instance not found: %s", instanceID)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("set instance users: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		"DELETE FROM user_default_instances WHERE instance_id = ?", instanceID,
	); err != nil {
		return fmt.Errorf("set instance users: %w", err)
	}
	for _, userID := range userIDs {
		if _, err := tx.Exec(
			"INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (?, ?, ?) "+
				"ON CONFLICT(user_id, service_type) DO UPDATE SET instance_id = excluded.instance_id",
			userID, serviceType, instanceID,
		); err != nil {
			return fmt.Errorf("pin instance for user %d: %w", userID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set instance users: %w", err)
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

// UserCanAccessInstance reports whether instanceID is the service instance
// exposed to a requester: their per-user pin when present, otherwise the global
// Radarr/Sonarr default. Chaptarr deliberately has no global fallback, so its
// per-user row is an explicit grant. All lookups are metadata-only and never
// decrypt the instance's credentials.
func (s *Store) UserCanAccessInstance(userID int64, instanceID, serviceType string) (bool, error) {
	pinnedID, pinned, err := s.GetUserDefault(userID, serviceType)
	if err != nil {
		return false, err
	}
	if pinned {
		return pinnedID == instanceID, nil
	}
	if serviceType == "chaptarr" {
		return false, nil
	}
	if serviceType != "radarr" && serviceType != "sonarr" {
		return false, nil
	}

	defaultID, err := s.defaultInstanceID(serviceType)
	if err != nil {
		return false, err
	}
	return defaultID != "" && defaultID == instanceID, nil
}

// defaultInstanceID mirrors GetDefault's explicit-default-then-first fallback
// without selecting or decrypting any credential columns.
func (s *Store) defaultInstanceID(serviceType string) (string, error) {
	var instanceID string
	err := s.db.QueryRow(
		"SELECT id FROM service_instances WHERE service_type = ? AND is_default = 1 ORDER BY sort_order, name, id LIMIT 1",
		serviceType,
	).Scan(&instanceID)
	if err == sql.ErrNoRows {
		err = s.db.QueryRow(
			"SELECT id FROM service_instances WHERE service_type = ? ORDER BY sort_order, name, id LIMIT 1",
			serviceType,
		).Scan(&instanceID)
	}
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get default instance id: %w", err)
	}
	return instanceID, nil
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
