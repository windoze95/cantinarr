package instance

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/windoze95/cantinarr-server/internal/mediapath"
	"github.com/windoze95/cantinarr-server/internal/plex"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

var ErrPendingBookRequests = errors.New("instance has pending book requests")

const (
	MediaDownloadModeDisabled = "disabled"
	MediaDownloadModeMapped   = "mapped"

	// legacyMediaDownloadModeIdentity is the retired pre-launch bridge that
	// synthesized root→root mappings from the deployment roots. Stored rows
	// may still carry it; they read as disabled until next saved.
	legacyMediaDownloadModeIdentity = "identity"
)

// Instance represents a configured service instance (an arr, a download
// client, a watch-history provider, or a media server). Radarr/Sonarr/SABnzbd
// authenticate with an API key; qBittorrent with either an API key (5.2 and
// newer) or a username and password, and a row holds one shape or the other,
// never both; NZBGet and Transmission with a username and password; Deluge
// with its web UI password alone; ruTorrent with optional Basic-auth
// credentials.
type Instance struct {
	ID           string `json:"id"`
	ServiceType  string `json:"service_type"` // "radarr", "sonarr", "sabnzbd", "deluge", …
	Name         string `json:"name"`
	URL          string `json:"url"`
	APIKey       string `json:"api_key"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	IsDefault    bool   `json:"is_default"`
	AutoAddUsers bool   `json:"auto_add_users"`
	TagRequests  bool   `json:"tag_requests"`
	SortOrder    int    `json:"sort_order"`
	// MediaDownloadMode is disabled until an admin saves explicit per-instance
	// path mappings (mapped). Downloads have no implicit configuration.
	MediaDownloadMode string              `json:"-"`
	MediaPathMappings []mediapath.Mapping `json:"-"`
	// MediaServerConfig is the Jellyfin/Emby-only configuration (sign-in
	// address shown to granted users, shared library ids). Zero for every
	// other type. MediaServerConfigInvalid marks a stored document that could
	// not be decoded: account creation refuses until an admin re-saves.
	MediaServerConfig        MediaServerConfig `json:"-"`
	MediaServerConfigInvalid bool              `json:"-"`
	CreatedAt                time.Time         `json:"created_at"`
}

const instanceColumns = "id, service_type, name, url, api_key, username, password, is_default, sort_order, media_download_mode, media_path_mappings, media_server_config, created_at, tag_requests, auto_add_users"

// EffectiveMediaPathMappings returns the instance's current routing rules:
// exactly the mappings an admin saved, or nothing.
func (inst *Instance) EffectiveMediaPathMappings() []mediapath.Mapping {
	if inst.MediaDownloadMode != MediaDownloadModeMapped {
		return nil
	}
	return append([]mediapath.Mapping(nil), inst.MediaPathMappings...)
}

// MediaDownloadsConfigured is safe for requester-facing capability metadata:
// it reveals only whether this exact instance has at least one effective rule.
func (inst *Instance) MediaDownloadsConfigured(roots []string) bool {
	if len(roots) == 0 {
		return false
	}
	// Configuration may have changed since a mapping was saved. Advertise the
	// capability when at least one current rule still names an accessible target
	// inside the deployment's present allowlist; ticket-time resolution remains
	// the final file-specific authority.
	for _, mapping := range inst.EffectiveMediaPathMappings() {
		if _, err := mediapath.Validate([]mediapath.Mapping{mapping}, roots); err == nil {
			return true
		}
	}
	return false
}

// Store provides CRUD operations for service instances. API keys and
// passwords are encrypted at rest; legacy plaintext rows decrypt as-is.
type Store struct {
	db         *sql.DB
	cipher     *secrets.Cipher
	grantAdded func(userID int64, instanceID string)
}

// NewStore creates a new instance store.
func NewStore(db *sql.DB, cipher *secrets.Cipher) *Store {
	return &Store{db: db, cipher: cipher}
}

// SetGrantAddedObserver installs a startup-only hook for newly committed grant
// rows. Unlike the handler's reconciliation hook, unchanged grants and removals
// are silent. Calling it after commit also covers grants made by account links.
func (s *Store) SetGrantAddedObserver(observer func(int64, string)) {
	s.grantAdded = observer
}

type grant struct {
	userID     int64
	instanceID string
}

func existingGrants(tx *sql.Tx, where string, arg any) (map[grant]bool, error) {
	rows, err := tx.Query("SELECT user_id, instance_id FROM user_instance_grants WHERE "+where, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	previous := make(map[grant]bool)
	for rows.Next() {
		var g grant
		if err := rows.Scan(&g.userID, &g.instanceID); err != nil {
			return nil, err
		}
		previous[g] = true
	}
	return previous, rows.Err()
}

func (s *Store) notifyAddedGrants(added []grant) {
	if s.grantAdded != nil {
		for _, g := range added {
			s.grantAdded(g.userID, g.instanceID)
		}
	}
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

type rowScanner interface {
	Scan(dest ...any) error
}

func scanInstance(scanner rowScanner) (Instance, error) {
	var inst Instance
	var mappingsJSON, mediaServerJSON string
	if err := scanner.Scan(
		&inst.ID,
		&inst.ServiceType,
		&inst.Name,
		&inst.URL,
		&inst.APIKey,
		&inst.Username,
		&inst.Password,
		&inst.IsDefault,
		&inst.SortOrder,
		&inst.MediaDownloadMode,
		&mappingsJSON,
		&mediaServerJSON,
		&inst.CreatedAt,
		&inst.TagRequests, &inst.AutoAddUsers,
	); err != nil {
		return Instance{}, err
	}
	switch inst.MediaDownloadMode {
	case MediaDownloadModeDisabled, MediaDownloadModeMapped:
	case legacyMediaDownloadModeIdentity:
		// Retired bridge value; reads as disabled (silently — rows self-heal
		// on the instance's next save).
		inst.MediaDownloadMode = MediaDownloadModeDisabled
	default:
		log.Printf("instance: invalid media download mode for %s; downloads disabled", inst.ID)
		inst.MediaDownloadMode = MediaDownloadModeDisabled
	}
	if err := json.Unmarshal([]byte(mappingsJSON), &inst.MediaPathMappings); err != nil {
		// Paths are admin-repairable operational metadata. Fail closed for
		// downloads while keeping the instance visible in the editor.
		log.Printf("instance: invalid media path mappings for %s; downloads disabled", inst.ID)
		inst.MediaDownloadMode = MediaDownloadModeDisabled
		inst.MediaPathMappings = nil
	}
	if inst.MediaDownloadMode != MediaDownloadModeMapped {
		inst.MediaPathMappings = nil
	}
	if err := json.Unmarshal([]byte(mediaServerJSON), &inst.MediaServerConfig); err != nil {
		// Fail closed the cheap way: "share everything" would be the open
		// reading of a document nobody can decode. The instance stays visible
		// in the editor and a re-save repairs the row.
		log.Printf("instance: invalid media server config for %s; account creation refused until re-saved", inst.ID)
		inst.MediaServerConfig = MediaServerConfig{}
		inst.MediaServerConfigInvalid = true
	}
	normalizeMediaServerConfig(&inst)
	return inst, nil
}

func normalizeMediaDownloadConfig(inst *Instance) {
	if inst.ServiceType != "radarr" && inst.ServiceType != "sonarr" && inst.ServiceType != "chaptarr" {
		inst.MediaDownloadMode = MediaDownloadModeDisabled
		inst.MediaPathMappings = nil
		return
	}
	switch inst.MediaDownloadMode {
	case MediaDownloadModeMapped:
		if len(inst.MediaPathMappings) == 0 {
			inst.MediaDownloadMode = MediaDownloadModeDisabled
		}
	default:
		inst.MediaDownloadMode = MediaDownloadModeDisabled
		inst.MediaPathMappings = nil
	}
}

func encodeMediaPathMappings(inst *Instance) (string, error) {
	normalizeMediaDownloadConfig(inst)
	if len(inst.MediaPathMappings) == 0 {
		return "[]", nil
	}
	encoded, err := json.Marshal(inst.MediaPathMappings)
	if err != nil {
		return "", fmt.Errorf("encode media path mappings: %w", err)
	}
	return string(encoded), nil
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
	inst, err := scanInstance(s.db.QueryRow(
		"SELECT "+instanceColumns+" FROM service_instances WHERE id = ?",
		id,
	))
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

// Media-server libraries remain grant-only and have no routing default.
func normalizeDefault(inst *Instance) {
	if IsMediaServerType(inst.ServiceType) {
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
	if inst.TagRequests && inst.ServiceType != "radarr" && inst.ServiceType != "sonarr" && inst.ServiceType != "chaptarr" && inst.ServiceType != "lidarr" {
		return errors.New("requester tagging requires Radarr, Sonarr, Chaptarr or Lidarr")
	}
	if inst.ID == "" {
		inst.ID = inst.ServiceType + "-" + uuid.New().String()[:8]
	}
	inst.CreatedAt = time.Now()
	if inst.AutoAddUsers && !IsAutomationType(inst.ServiceType) {
		return errors.New("automatic assignment requires an automation instance")
	}
	normalizeDefault(inst)

	apiKey, password, err := s.encryptSecrets(inst)
	if err != nil {
		return err
	}
	mappingsJSON, err := encodeMediaPathMappings(inst)
	if err != nil {
		return err
	}
	mediaServerJSON, err := encodeMediaServerConfig(inst)
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
		"INSERT INTO service_instances ("+instanceColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		inst.ID, inst.ServiceType, inst.Name, inst.URL, apiKey, inst.Username, password, inst.IsDefault, inst.SortOrder, inst.MediaDownloadMode, mappingsJSON, mediaServerJSON, inst.CreatedAt, inst.TagRequests, inst.AutoAddUsers,
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
	var oldURL string
	err = tx.QueryRow(
		"SELECT service_type, url FROM service_instances WHERE id = ?", inst.ID,
	).Scan(&inst.ServiceType, &oldURL)
	if err == sql.ErrNoRows {
		return fmt.Errorf("instance not found: %s", inst.ID)
	}
	if err != nil {
		return fmt.Errorf("update instance: %w", err)
	}
	if inst.TagRequests && inst.ServiceType != "radarr" && inst.ServiceType != "sonarr" && inst.ServiceType != "chaptarr" && inst.ServiceType != "lidarr" {
		return errors.New("requester tagging requires Radarr, Sonarr, Chaptarr or Lidarr")
	}
	mappingsJSON, err := encodeMediaPathMappings(inst)
	if err != nil {
		return err
	}
	mediaServerJSON, err := encodeMediaServerConfig(inst)
	if err != nil {
		return err
	}
	if inst.AutoAddUsers && !IsAutomationType(inst.ServiceType) {
		return errors.New("automatic assignment requires an automation instance")
	}
	normalizeDefault(inst)
	if err := clearSiblingDefaults(tx, inst); err != nil {
		return err
	}
	if _, err := tx.Exec(
		"UPDATE service_instances SET name = ?, url = ?, api_key = ?, username = ?, password = ?, is_default = ?, sort_order = ?, media_download_mode = ?, media_path_mappings = ?, media_server_config = ?, tag_requests = ?, auto_add_users = ? WHERE id = ?",
		inst.Name, inst.URL, apiKey, inst.Username, password, inst.IsDefault, inst.SortOrder, inst.MediaDownloadMode, mappingsJSON, mediaServerJSON, inst.TagRequests, inst.AutoAddUsers, inst.ID,
	); err != nil {
		return fmt.Errorf("update instance: %w", err)
	}
	// Repointing an instance at a different arr invalidates the recorded queue
	// membership: the same record ids mean different media there, so the next
	// poll must re-seed rather than diff against the old server's queue.
	if oldURL != inst.URL {
		if _, err := tx.Exec("DELETE FROM arr_queue_witness WHERE instance_id = ?", inst.ID); err != nil {
			return fmt.Errorf("clear instance queue witness: %w", err)
		}
	}
	// Disabling or repointing cancels unfinished intent atomically. Re-enabling
	// cannot revive an old job, including one with an outstanding worker lease.
	if !inst.TagRequests || oldURL != inst.URL {
		if _, err := tx.Exec(`UPDATE request_tag_jobs SET state='cancelled', message='Requester tagging was disabled or the library destination changed.', lease_token='', lease_until=0, updated_at=CURRENT_TIMESTAMP WHERE state NOT IN ('applied','cancelled') AND request_id IN (SELECT id FROM request_log WHERE instance_id=?)`, inst.ID); err != nil {
			return fmt.Errorf("cancel requester tags: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("update instance: %w", err)
	}
	return nil
}

// SetPlexOwner records the plex.tv account a Plex instance's token belongs
// to, patching only those fields so a concurrent admin save of the rest of
// the config is never overwritten. It is how an instance linked before the
// owner was recorded, or one created from a pasted token, learns its owner.
func (s *Store) SetPlexOwner(id string, owner plex.Account) error {
	res, err := s.db.Exec(
		`UPDATE service_instances
		    SET media_server_config = json_set(media_server_config, '$.plex_owner_id', ?, '$.plex_owner_username', ?, '$.plex_owner_email', ?)
		  WHERE id = ? AND service_type = 'plex' AND json_valid(media_server_config)`,
		owner.ID, owner.Username, owner.Email, id,
	)
	if err != nil {
		return fmt.Errorf("set plex owner: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("set plex owner: no plex instance %s with a readable config", id)
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
	// Drop preferences and grants that pointed at the deleted instance.
	// user_instance_grants
	// also declares ON DELETE CASCADE; the explicit delete keeps the cleanup
	// independent of the foreign_keys pragma.
	if _, err := tx.Exec("DELETE FROM user_default_instances WHERE instance_id = ?", id); err != nil {
		return fmt.Errorf("delete instance user defaults: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM user_instance_grants WHERE instance_id = ?", id); err != nil {
		return fmt.Errorf("delete instance user grants: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM user_media_server_accounts WHERE instance_id = ?", id); err != nil {
		return fmt.Errorf("delete instance media server accounts: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM arr_queue_witness WHERE instance_id = ?", id); err != nil {
		return fmt.Errorf("delete instance queue witness: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM hardcover_instance_connections WHERE instance_id=?", id); err != nil {
		return err
	}
	if err := cleanupHardcoverConnections(tx); err != nil {
		return err
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
	inst, err := scanInstance(s.db.QueryRow(
		"SELECT "+instanceColumns+" FROM service_instances WHERE service_type = ? AND is_default = 1 ORDER BY sort_order, name, id LIMIT 1",
		serviceType,
	))
	if err == sql.ErrNoRows {
		// Fall back to first instance
		inst, err = scanInstance(s.db.QueryRow(
			"SELECT "+instanceColumns+" FROM service_instances WHERE service_type = ? ORDER BY sort_order, name, id LIMIT 1",
			serviceType,
		))
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

// GetUserDefault reads a routing preference. It never grants access; callers
// resolving requests use EffectiveDefaultInstanceID.
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
// type. Omitted types use automatic routing within the accessible set.
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
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var allowed bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM user_instance_grants WHERE user_id=? AND instance_id=?) OR (? AND EXISTS(SELECT 1 FROM users WHERE id=? AND role='admin'))`, userID, instanceID, !IsAutomationType(serviceType), userID).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return errors.New("assign this instance before choosing it as a preference")
	}
	_, err = tx.Exec(
		"INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (?, ?, ?) "+
			"ON CONFLICT(user_id, service_type) DO UPDATE SET instance_id = excluded.instance_id",
		userID, serviceType, instanceID,
	)
	if err != nil {
		return fmt.Errorf("set user default instance: %w", err)
	}
	return tx.Commit()
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

// SetInstanceUsers replaces preferences for this instance only. Every selected
// regular user must already be assigned; no access is granted or revoked here.
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
		var allowed bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM user_instance_grants WHERE user_id=? AND instance_id=?) OR EXISTS(SELECT 1 FROM users WHERE id=? AND role='admin')`, userID, instanceID, userID).Scan(&allowed); err != nil {
			return err
		}
		if !allowed {
			return errors.New("assign this instance before choosing it as a preference")
		}
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

// ClearUserDefault restores automatic routing without changing access.
func (s *Store) ClearUserDefault(userID int64, serviceType string) error {
	if _, err := s.db.Exec(
		"DELETE FROM user_default_instances WHERE user_id = ? AND service_type = ?",
		userID, serviceType,
	); err != nil {
		return fmt.Errorf("clear user default instance: %w", err)
	}
	return nil
}

// IsAutomationType identifies the four requester library services.
func IsAutomationType(serviceType string) bool {
	return serviceType == "radarr" || serviceType == "sonarr" || serviceType == "chaptarr" || serviceType == "lidarr"
}

// UserHasInstanceAccess reads explicit assignments, never routing preferences.
func (s *Store) UserHasInstanceAccess(userID int64, instanceID string) (bool, error) {
	var allowed bool
	err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM user_instance_grants WHERE user_id=? AND instance_id=?)", userID, instanceID).Scan(&allowed)
	return allowed, err
}

// GrantedInstanceIDs returns explicit assignments in configured order.
func (s *Store) GrantedInstanceIDs(userID int64, serviceType string) ([]string, error) {
	rows, err := s.db.Query(`SELECT si.id FROM service_instances si JOIN user_instance_grants g ON g.instance_id=si.id
 WHERE g.user_id=? AND si.service_type=? ORDER BY si.sort_order,si.name,si.id`, userID, serviceType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// EffectiveDefaultInstanceID chooses only from the user's accessible set:
// preference, global default, then first assignment. Administrator routing
// retains its all-instance scope; media-server eligibility remains explicit.
func (s *Store) EffectiveDefaultInstanceID(userID int64, serviceType string) (string, error) {
	return s.defaultInstanceID(userID, serviceType, true)
}

// AssignedDefaultInstanceID resolves personal request routing without administrator visibility.
func (s *Store) AssignedDefaultInstanceID(userID int64, serviceType string) (string, error) {
	return s.defaultInstanceID(userID, serviceType, false)
}

func (s *Store) defaultInstanceID(userID int64, serviceType string, administratorAccess bool) (string, error) {
	var id string
	err := s.db.QueryRow(`SELECT si.id FROM service_instances si
 WHERE si.service_type=? AND (EXISTS(SELECT 1 FROM user_instance_grants g WHERE g.user_id=? AND g.instance_id=si.id)
 OR (? AND EXISTS(SELECT 1 FROM users u WHERE u.id=? AND u.role='admin')))
 ORDER BY EXISTS(SELECT 1 FROM user_default_instances d WHERE d.user_id=? AND d.instance_id=si.id AND d.service_type=si.service_type) DESC,
 si.is_default DESC,si.sort_order,si.name,si.id LIMIT 1`, serviceType, userID, administratorAccess && IsAutomationType(serviceType), userID, userID).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

// VisibleInstanceIDs never adds access through a global default or preference.
func (s *Store) VisibleInstanceIDs(userID int64, serviceType string) ([]string, error) {
	return s.GrantedInstanceIDs(userID, serviceType)
}

// ListUserGrants returns explicit assignments keyed by service type, in
// configured order. Preferences never alter this set.
func (s *Store) ListUserGrants(userID int64) (map[string][]string, error) {
	rows, err := s.db.Query(
		`SELECT si.service_type, si.id FROM user_instance_grants g
		 JOIN service_instances si ON si.id = g.instance_id
		 WHERE g.user_id = ?
		 ORDER BY si.service_type, si.sort_order, si.name, si.id`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list user instance grants: %w", err)
	}
	defer rows.Close()
	out := make(map[string][]string)
	for rows.Next() {
		var serviceType, instanceID string
		if err := rows.Scan(&serviceType, &instanceID); err != nil {
			return nil, fmt.Errorf("scan user instance grant: %w", err)
		}
		out[serviceType] = append(out[serviceType], instanceID)
	}
	return out, rows.Err()
}

// SetUserGrants replaces a user's grant rows per service type. Only the
// service types present as keys are touched; an empty (or nil) list clears
// that type's grants. Every instance id must exist and match its keyed
// service type so a single admin endpoint can accept a {service_type: [ids]}
// map without risking a mismatched grant.
func (s *Store) SetUserGrants(userID int64, grants map[string][]string) error {
	for serviceType, ids := range grants {
		for _, instanceID := range ids {
			actual, err := s.ServiceTypeOf(instanceID)
			if err != nil {
				return err
			}
			if actual == "" {
				return fmt.Errorf("instance not found: %s", instanceID)
			}
			if actual != serviceType {
				return fmt.Errorf("instance %s is %q, not %q", instanceID, actual, serviceType)
			}
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("set user instance grants: %w", err)
	}
	defer tx.Rollback()
	previous, err := existingGrants(tx, "user_id = ?", userID)
	if err != nil {
		return fmt.Errorf("read previous user grants: %w", err)
	}
	var added []grant
	for serviceType, ids := range grants {
		if _, err := tx.Exec(
			`DELETE FROM user_instance_grants WHERE user_id = ? AND instance_id IN (
			     SELECT id FROM service_instances WHERE service_type = ?)`,
			userID, serviceType,
		); err != nil {
			return fmt.Errorf("clear user instance grants: %w", err)
		}
		for _, instanceID := range ids {
			if _, err := tx.Exec(
				"INSERT INTO user_instance_grants (user_id, instance_id) VALUES (?, ?) "+
					"ON CONFLICT(user_id, instance_id) DO NOTHING",
				userID, instanceID,
			); err != nil {
				// Covers unknown user ids too (the user_id foreign key rejects them).
				return fmt.Errorf("grant instance for user %d: %w", userID, err)
			}
			g := grant{userID, instanceID}
			if !previous[g] {
				added = append(added, g)
				previous[g] = true
			}
		}
	}
	if _, err := tx.Exec(`DELETE FROM user_default_instances WHERE user_id=? AND service_type IN ('radarr','sonarr','chaptarr','lidarr')
 AND NOT EXISTS(SELECT 1 FROM user_instance_grants g WHERE g.user_id=user_default_instances.user_id AND g.instance_id=user_default_instances.instance_id)`, userID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set user instance grants: %w", err)
	}
	s.notifyAddedGrants(added)
	return nil
}

// ListTypeUserGrants returns every access-grant row for a service type as a
// user id → granted instance ids map, so the instance admin UI can show who
// holds a grant on this instance and who holds one on a sibling.
func (s *Store) ListTypeUserGrants(serviceType string) (map[int64][]string, error) {
	rows, err := s.db.Query(
		`SELECT g.user_id, si.id FROM user_instance_grants g
		 JOIN service_instances si ON si.id = g.instance_id
		 WHERE si.service_type = ?
		 ORDER BY g.user_id, si.sort_order, si.name, si.id`,
		serviceType,
	)
	if err != nil {
		return nil, fmt.Errorf("list user grants for type: %w", err)
	}
	defer rows.Close()
	out := make(map[int64][]string)
	for rows.Next() {
		var userID int64
		var instanceID string
		if err := rows.Scan(&userID, &instanceID); err != nil {
			return nil, fmt.Errorf("scan user grant for type: %w", err)
		}
		out[userID] = append(out[userID], instanceID)
	}
	return out, rows.Err()
}

// SetInstanceGrantUsers replaces all grants on this instance and clears
// preferences to revoked grants. Sibling assignments remain unchanged.
// New filtered directory clients must use ChangeAssignments instead.
func (s *Store) SetInstanceGrantUsers(instanceID string, userIDs []int64) error {
	serviceType, err := s.ServiceTypeOf(instanceID)
	if err != nil {
		return err
	}
	if serviceType == "" {
		return fmt.Errorf("instance not found: %s", instanceID)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("set instance grant users: %w", err)
	}
	defer tx.Rollback()
	previous, err := existingGrants(tx, "instance_id = ?", instanceID)
	if err != nil {
		return fmt.Errorf("read previous instance grants: %w", err)
	}
	var added []grant
	if _, err := tx.Exec(
		"DELETE FROM user_instance_grants WHERE instance_id = ?", instanceID,
	); err != nil {
		return fmt.Errorf("set instance grant users: %w", err)
	}
	keep := make([]interface{}, 0, len(userIDs)+1)
	keep = append(keep, instanceID)
	placeholders := ""
	for i, userID := range userIDs {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		keep = append(keep, userID)
	}
	pinClear := "DELETE FROM user_default_instances WHERE instance_id = ?"
	if len(userIDs) > 0 {
		pinClear += " AND user_id NOT IN (" + placeholders + ")"
	}
	if _, err := tx.Exec(pinClear, keep...); err != nil {
		return fmt.Errorf("clear revoked instance pins: %w", err)
	}
	for _, userID := range userIDs {
		if _, err := tx.Exec(
			"INSERT INTO user_instance_grants (user_id, instance_id) VALUES (?, ?) "+
				"ON CONFLICT(user_id, instance_id) DO NOTHING",
			userID, instanceID,
		); err != nil {
			// Covers unknown user ids too (the user_id foreign key rejects them).
			return fmt.Errorf("grant instance for user %d: %w", userID, err)
		}
		g := grant{userID, instanceID}
		if !previous[g] {
			added = append(added, g)
			previous[g] = true
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set instance grant users: %w", err)
	}
	s.notifyAddedGrants(added)
	return nil
}

// UserCanAccessInstance checks an explicit automation assignment of the
// required service type without reading credentials. Administrators bypass
// this check at the authorization boundary.
func (s *Store) UserCanAccessInstance(userID int64, instanceID, serviceType string) (bool, error) {
	if serviceType != "radarr" && serviceType != "sonarr" && serviceType != "chaptarr" && serviceType != "lidarr" {
		return false, nil
	}
	visible, err := s.VisibleInstanceIDs(userID, serviceType)
	if err != nil {
		return false, err
	}
	for _, id := range visible {
		if id == instanceID {
			return true, nil
		}
	}
	return false, nil
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
		inst, err := scanInstance(rows)
		if err != nil {
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
