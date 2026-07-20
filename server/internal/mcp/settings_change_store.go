package mcp

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/instance"
)

const (
	settingChangeStatusExecuting      = "executing"
	settingChangeStatusApplied        = "applied"
	settingChangeStatusFailed         = "failed"
	settingChangeStatusOutcomeUnknown = "outcome_unknown"
	maxSettingChangeFields            = 512
	maxSettingChangeTextBytes         = 4 << 10
)

// SettingFieldChange is the bounded, safe projection shown to administrators.
// Raw before/after snapshots never leave the server.
type SettingFieldChange struct {
	Key          string  `json:"key"`
	Label        string  `json:"label"`
	Before       string  `json:"before"`
	After        string  `json:"after"`
	Current      *string `json:"current,omitempty"`
	CurrentState string  `json:"current_state,omitempty"`
}

// ExternalSettingChange is the public audit record. CurrentStatus and
// CanRevert are populated only by the detail endpoint after a live read.
type ExternalSettingChange struct {
	ID            int64                `json:"id"`
	ParentID      *int64               `json:"parent_id,omitempty"`
	ActorUserID   int64                `json:"actor_user_id"`
	ActorName     string               `json:"actor_name"`
	Source        string               `json:"source"`
	ServiceType   string               `json:"service_type"`
	InstanceID    string               `json:"instance_id"`
	InstanceName  string               `json:"instance_name"`
	ResourceType  string               `json:"resource_type"`
	ResourceID    string               `json:"resource_id"`
	ResourceName  string               `json:"resource_name"`
	Operation     string               `json:"operation"`
	Status        string               `json:"status"`
	Summary       string               `json:"summary"`
	Changes       []SettingFieldChange `json:"changes"`
	ErrorText     string               `json:"error_text,omitempty"`
	CreatedAt     time.Time            `json:"created_at"`
	CompletedAt   *time.Time           `json:"completed_at,omitempty"`
	CurrentStatus string               `json:"current_status,omitempty"`
	CurrentError  string               `json:"current_error,omitempty"`
	CanRevert     bool                 `json:"can_revert"`
}

type storedSettingChange struct {
	ExternalSettingChange
	ActorDeviceID   string
	BeforeRaw       json.RawMessage
	AfterRaw        json.RawMessage
	BeforeHash      string
	AfterHash       string
	DependencyHash  string
	InstanceBinding instance.ArrSettingsFingerprint
}

type newSettingChange struct {
	ParentID        *int64
	ActorUserID     int64
	ActorDeviceID   string
	Source          string
	ServiceType     string
	InstanceID      string
	InstanceName    string
	ResourceType    string
	ResourceID      string
	ResourceName    string
	Operation       string
	Summary         string
	Changes         []SettingFieldChange
	BeforeRaw       json.RawMessage
	AfterRaw        json.RawMessage
	BeforeHash      [sha256.Size]byte
	AfterHash       [sha256.Size]byte
	DependencyHash  [sha256.Size]byte
	InstanceBinding instance.ArrSettingsFingerprint
}

type settingChangeStore struct {
	db *sql.DB
}

func newSettingChangeStore(database *sql.DB) *settingChangeStore {
	if database == nil {
		return nil
	}
	return &settingChangeStore{db: database}
}

func (s *settingChangeStore) create(change newSettingChange) (storedSettingChange, error) {
	if s == nil || s.db == nil {
		return storedSettingChange{}, fmt.Errorf("external settings change history is unavailable")
	}
	if err := validateNewSettingChange(change); err != nil {
		return storedSettingChange{}, err
	}
	changesJSON, err := json.Marshal(change.Changes)
	if err != nil {
		return storedSettingChange{}, fmt.Errorf("encode settings change fields: %w", err)
	}
	var parent any
	if change.ParentID != nil {
		parent = *change.ParentID
	}
	result, err := s.db.Exec(`
		INSERT INTO external_setting_changes (
			parent_id, actor_user_id, actor_device_id, actor_name, source,
			service_type, instance_id, instance_name, resource_type, resource_id,
			resource_name, operation, status, summary, changes_json,
			before_json, after_json, before_hash, after_hash, dependency_hash,
			instance_binding
		) VALUES (
			?, ?, ?, COALESCE((SELECT username FROM users WHERE id = ?), CASE WHEN ? = 0 THEN 'Cantinarr' ELSE 'Administrator' END), ?,
			?, ?, ?, ?, ?,
			?, ?, 'executing', ?, ?,
			?, ?, ?, ?, ?,
			?
		)`,
		parent, change.ActorUserID, change.ActorDeviceID, change.ActorUserID, change.ActorUserID, change.Source,
		change.ServiceType, change.InstanceID, change.InstanceName, change.ResourceType, change.ResourceID,
		change.ResourceName, change.Operation, change.Summary, string(changesJSON),
		string(change.BeforeRaw), string(change.AfterRaw), hex.EncodeToString(change.BeforeHash[:]),
		hex.EncodeToString(change.AfterHash[:]), hex.EncodeToString(change.DependencyHash[:]),
		change.InstanceBinding[:],
	)
	if err != nil {
		return storedSettingChange{}, fmt.Errorf("record external settings change before write: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return storedSettingChange{}, fmt.Errorf("read external settings change id: %w", err)
	}
	return s.get(id)
}

func validateNewSettingChange(change newSettingChange) error {
	for name, value := range map[string]string{
		"source": change.Source, "service_type": change.ServiceType,
		"instance_id": change.InstanceID, "instance_name": change.InstanceName,
		"resource_type": change.ResourceType, "resource_id": change.ResourceID,
		"resource_name": change.ResourceName, "operation": change.Operation,
		"summary": change.Summary,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("settings change %s is required", name)
		}
		if len(value) > maxSettingChangeTextBytes {
			return fmt.Errorf("settings change %s is too large", name)
		}
	}
	if change.ActorUserID < 0 || (change.ActorUserID == 0 && change.Source != "system") ||
		(change.ActorUserID > 0 && strings.TrimSpace(change.ActorDeviceID) == "") {
		return fmt.Errorf("settings change actor is required")
	}
	if err := validateSettingFieldChanges(change.Changes); err != nil {
		return err
	}
	for name, raw := range map[string]json.RawMessage{"before": change.BeforeRaw, "after": change.AfterRaw} {
		if len(raw) == 0 || len(raw) > maxProfileSettingsSnapshotBytes || !json.Valid(raw) {
			return fmt.Errorf("settings change %s snapshot is invalid", name)
		}
	}
	return nil
}

func validateSettingFieldChanges(changes []SettingFieldChange) error {
	if len(changes) == 0 || len(changes) > maxSettingChangeFields {
		return fmt.Errorf("settings change must contain 1 to %d fields", maxSettingChangeFields)
	}
	for _, field := range changes {
		if strings.TrimSpace(field.Key) == "" || strings.TrimSpace(field.Label) == "" ||
			len(field.Key) > maxSettingChangeTextBytes || len(field.Label) > maxSettingChangeTextBytes ||
			len(field.Before) > maxSettingChangeTextBytes || len(field.After) > maxSettingChangeTextBytes {
			return fmt.Errorf("settings change contains an invalid field projection")
		}
	}
	return nil
}

// settingChangeSummary is written before the remote outcome is known, so it
// names the operation without claiming that it succeeded.
func settingChangeSummary(resourceType, operation, resourceName string) string {
	label := "Connected-app settings change"
	switch {
	case resourceType == "quality_profile" && operation == "update":
		label = "Quality profile update"
	case resourceType == "quality_profile" && operation == "revert":
		label = "Quality profile restore"
	case resourceType == "custom_format" && operation == "create":
		label = "Custom format creation"
	case resourceType == "custom_format" && operation == "update":
		label = "Custom format update"
	}
	return fmt.Sprintf("%s: %q", label, resourceName)
}

func (s *settingChangeStore) finish(id int64, status, errorText string) (storedSettingChange, error) {
	if s == nil || s.db == nil {
		return storedSettingChange{}, fmt.Errorf("external settings change history is unavailable")
	}
	if status != settingChangeStatusApplied && status != settingChangeStatusFailed && status != settingChangeStatusOutcomeUnknown {
		return storedSettingChange{}, fmt.Errorf("invalid settings change outcome")
	}
	if len(errorText) > maxSettingChangeTextBytes {
		errorText = errorText[:maxSettingChangeTextBytes]
	}
	result, err := s.db.Exec(`
		UPDATE external_setting_changes
		SET status = ?, error_text = ?, completed_at = CURRENT_TIMESTAMP
		WHERE id = ? AND status = 'executing'`, status, errorText, id)
	if err != nil {
		return storedSettingChange{}, fmt.Errorf("finish external settings change: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return storedSettingChange{}, fmt.Errorf("external settings change outcome was already decided")
	}
	return s.get(id)
}

func (s *settingChangeStore) finishAppliedVerified(id int64, resourceID, resourceName string, changes []SettingFieldChange, afterRaw json.RawMessage, afterHash [sha256.Size]byte) (storedSettingChange, error) {
	if s == nil || s.db == nil {
		return storedSettingChange{}, fmt.Errorf("external settings change history is unavailable")
	}
	if id <= 0 || strings.TrimSpace(resourceID) == "" || strings.TrimSpace(resourceName) == "" ||
		len(resourceID) > maxSettingChangeTextBytes || len(resourceName) > maxSettingChangeTextBytes ||
		len(afterRaw) == 0 || len(afterRaw) > maxProfileSettingsSnapshotBytes || !json.Valid(afterRaw) {
		return storedSettingChange{}, fmt.Errorf("verified settings change result is invalid")
	}
	calculated, err := canonicalJSONHash(afterRaw)
	if err != nil || calculated != afterHash {
		return storedSettingChange{}, fmt.Errorf("verified settings change result failed integrity validation")
	}
	if err := validateSettingFieldChanges(changes); err != nil {
		return storedSettingChange{}, err
	}
	changesJSON, err := json.Marshal(changes)
	if err != nil {
		return storedSettingChange{}, fmt.Errorf("encode verified settings change fields: %w", err)
	}
	result, err := s.db.Exec(`
		UPDATE external_setting_changes
		SET resource_id = ?, resource_name = ?, changes_json = ?, after_json = ?, after_hash = ?,
		    status = 'applied', error_text = '', completed_at = CURRENT_TIMESTAMP
		WHERE id = ? AND status = 'executing'`,
		resourceID, resourceName, string(changesJSON), string(afterRaw), hashString(afterHash), id)
	if err != nil {
		return storedSettingChange{}, fmt.Errorf("finish verified external settings change: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return storedSettingChange{}, fmt.Errorf("external settings change outcome was already decided")
	}
	return s.get(id)
}

func (s *settingChangeStore) list(limit int, beforeID int64) ([]ExternalSettingChange, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("external settings change history is unavailable")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.db.Query(`
		SELECT id, parent_id, actor_user_id, actor_name, source,
		       service_type, instance_id, instance_name, resource_type, resource_id,
		       resource_name, operation, status, summary, error_text, created_at, completed_at
		FROM external_setting_changes
		WHERE (? = 0 OR id < ?)
		ORDER BY id DESC LIMIT ?`, beforeID, beforeID, limit)
	if err != nil {
		return nil, fmt.Errorf("list external settings changes: %w", err)
	}
	defer rows.Close()
	changes := make([]ExternalSettingChange, 0)
	for rows.Next() {
		change, err := scanExternalSettingChange(rows)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate external settings changes: %w", err)
	}
	return changes, nil
}

func (s *settingChangeStore) get(id int64) (storedSettingChange, error) {
	if s == nil || s.db == nil || id <= 0 {
		return storedSettingChange{}, sql.ErrNoRows
	}
	row := s.db.QueryRow(`
		SELECT id, parent_id, actor_user_id, actor_device_id, actor_name, source,
		       service_type, instance_id, instance_name, resource_type, resource_id,
		       resource_name, operation, status, summary, changes_json,
		       before_json, after_json, before_hash, after_hash, dependency_hash,
		       instance_binding, error_text, created_at, completed_at
		FROM external_setting_changes WHERE id = ?`, id)
	return scanStoredSettingChange(row)
}

type settingChangeScanner interface {
	Scan(...any) error
}

// scanExternalSettingChange intentionally reads only timeline metadata. Field
// projections and raw snapshots can be large and are loaded only for one
// administrator-selected detail record.
func scanExternalSettingChange(scanner settingChangeScanner) (ExternalSettingChange, error) {
	var (
		change    ExternalSettingChange
		parent    sql.NullInt64
		completed sql.NullTime
	)
	if err := scanner.Scan(
		&change.ID, &parent, &change.ActorUserID, &change.ActorName, &change.Source,
		&change.ServiceType, &change.InstanceID, &change.InstanceName, &change.ResourceType, &change.ResourceID,
		&change.ResourceName, &change.Operation, &change.Status, &change.Summary,
		&change.ErrorText, &change.CreatedAt, &completed,
	); err != nil {
		return ExternalSettingChange{}, err
	}
	if parent.Valid {
		change.ParentID = &parent.Int64
	}
	if completed.Valid {
		change.CompletedAt = &completed.Time
	}
	change.Changes = make([]SettingFieldChange, 0)
	return change, nil
}

func scanStoredSettingChange(scanner settingChangeScanner) (storedSettingChange, error) {
	var (
		change       storedSettingChange
		parent       sql.NullInt64
		completed    sql.NullTime
		changesJSON  string
		beforeJSON   string
		afterJSON    string
		bindingBytes []byte
	)
	err := scanner.Scan(
		&change.ID, &parent, &change.ActorUserID, &change.ActorDeviceID, &change.ActorName, &change.Source,
		&change.ServiceType, &change.InstanceID, &change.InstanceName, &change.ResourceType, &change.ResourceID,
		&change.ResourceName, &change.Operation, &change.Status, &change.Summary, &changesJSON,
		&beforeJSON, &afterJSON, &change.BeforeHash, &change.AfterHash, &change.DependencyHash,
		&bindingBytes, &change.ErrorText, &change.CreatedAt, &completed,
	)
	if err != nil {
		return storedSettingChange{}, err
	}
	if parent.Valid {
		change.ParentID = &parent.Int64
	}
	if completed.Valid {
		change.CompletedAt = &completed.Time
	}
	if err := json.Unmarshal([]byte(changesJSON), &change.Changes); err != nil {
		return storedSettingChange{}, fmt.Errorf("decode external settings change fields: %w", err)
	}
	if len(bindingBytes) != len(change.InstanceBinding) {
		return storedSettingChange{}, fmt.Errorf("external settings change has an invalid instance binding")
	}
	copy(change.InstanceBinding[:], bindingBytes)
	change.BeforeRaw = json.RawMessage(beforeJSON)
	change.AfterRaw = json.RawMessage(afterJSON)
	return change, nil
}

func hashString(hash [sha256.Size]byte) string {
	return hex.EncodeToString(hash[:])
}
