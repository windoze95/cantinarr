package mediaavailability

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

var (
	ErrHouseholdUser      = errors.New("user not found")
	ErrHouseholdSelection = errors.New("select current scanned libraries")
	ErrHouseholdLinked    = errors.New("a linked account already controls this user's access")
	ErrHouseholdChanged   = errors.New("library access changed; refresh before saving")
	ErrHouseholdAdmin     = errors.New("administrator access changed")
)

// HouseholdAssociation is local permission to report inventory, never a remote
// account, provider sharing grant, or authority to control playback.
type HouseholdAssociation struct {
	InstanceID string   `json:"instance_id"`
	LibraryIDs []string `json:"library_ids"`
}

type HouseholdServer struct {
	InstanceID  string                `json:"instance_id"`
	Name        string                `json:"name"`
	ServiceType string                `json:"service_type"`
	Enabled     bool                  `json:"enabled"`
	LibraryIDs  []string              `json:"library_ids"`
	Libraries   []mediaserver.Library `json:"libraries"`
	Linked      bool                  `json:"linked"`
	Error       string                `json:"error,omitempty"`
}

type HouseholdView struct {
	Associations []HouseholdAssociation `json:"associations"`
	Servers      []HouseholdServer      `json:"servers"`
}

func videoServer(kind string) bool { return kind == "plex" || kind == "jellyfin" || kind == "emby" }

func (s *Service) householdUser(userID int64) error {
	var exists int
	err := s.db.QueryRow("SELECT 1 FROM users WHERE id=?", userID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrHouseholdUser
	}
	return err
}

func (s *Service) households(userID int64) ([]HouseholdAssociation, error) {
	rows, err := s.db.Query("SELECT instance_id,library_ids FROM media_availability_households WHERE user_id=? ORDER BY instance_id", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HouseholdAssociation{}
	for rows.Next() {
		var a HouseholdAssociation
		var raw string
		if err := rows.Scan(&a.InstanceID, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &a.LibraryIDs); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// HouseholdAccess is an admin-only editor projection. Disabled and unreachable
// servers remain present so their saved associations can be reviewed or removed.
func (s *Service) HouseholdAccess(ctx context.Context, userID int64) (HouseholdView, error) {
	out := HouseholdView{Associations: []HouseholdAssociation{}, Servers: []HouseholdServer{}}
	if err := s.householdUser(userID); err != nil {
		return out, err
	}
	var err error
	out.Associations, err = s.households(userID)
	if err != nil {
		return out, err
	}
	instances, err := s.store.ListAll()
	if err != nil {
		return out, err
	}
	for _, inst := range instances {
		if !videoServer(inst.ServiceType) {
			continue
		}
		v := HouseholdServer{InstanceID: inst.ID, Name: inst.Name, ServiceType: inst.ServiceType,
			LibraryIDs: []string{}, Libraries: []mediaserver.Library{}}
		var linked int
		if err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM user_media_server_accounts WHERE user_id=? AND instance_id=?)", userID, inst.ID).Scan(&linked); err != nil {
			return out, err
		}
		v.Linked = linked != 0
		if cfg := inst.MediaServerConfig.AvailabilityScan; cfg != nil && cfg.Enabled && !inst.MediaServerConfigInvalid {
			v.Enabled = true
			v.LibraryIDs = append(v.LibraryIDs, cfg.LibraryIDs...)
			readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			libraries, err := s.ScanLibraries(readCtx, inst.ID)
			cancel()
			if err != nil {
				v.Error = "Could not verify this server's libraries. Retry before assigning access."
			} else {
				selected := stringSet(cfg.LibraryIDs)
				v.LibraryIDs = []string{}
				for _, library := range libraries {
					if selected[library.ID] {
						v.Libraries = append(v.Libraries, library)
						v.LibraryIDs = append(v.LibraryIDs, library.ID)
					}
				}
			}
		}
		out.Servers = append(out.Servers, v)
	}
	return out, nil
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func normalizedHousehold(a HouseholdAssociation) (HouseholdAssociation, error) {
	a.InstanceID = strings.TrimSpace(a.InstanceID)
	if a.InstanceID == "" || len(a.InstanceID) > 256 || len(a.LibraryIDs) == 0 || len(a.LibraryIDs) > 1000 {
		return a, ErrHouseholdSelection
	}
	ids := map[string]bool{}
	for _, id := range a.LibraryIDs {
		id = strings.TrimSpace(id)
		if id == "" || len(id) > 256 {
			return a, ErrHouseholdSelection
		}
		ids[id] = true
	}
	a.LibraryIDs = make([]string, 0, len(ids))
	for id := range ids {
		a.LibraryIDs = append(a.LibraryIDs, id)
	}
	sort.Strings(a.LibraryIDs)
	return a, nil
}

// SetHouseholdAccess replaces only local inventory associations. Provider calls
// are reads; this deliberately never calls AddUserGrant or account reconciliation.
func (s *Service) SetHouseholdAccess(ctx context.Context, userID int64, selected []HouseholdAssociation) error {
	return s.setHouseholdAccess(ctx, 0, userID, selected)
}

// SaveHouseholdAccess requires the administrator to retain that role through
// provider verification and the local permission transaction.
func (s *Service) SaveHouseholdAccess(ctx context.Context, actorID, userID int64, selected []HouseholdAssociation) error {
	if actorID <= 0 {
		return ErrHouseholdAdmin
	}
	return s.setHouseholdAccess(ctx, actorID, userID, selected)
}

func (s *Service) setHouseholdAccess(ctx context.Context, actorID, userID int64, selected []HouseholdAssociation) error {
	if err := s.householdUser(userID); err != nil {
		return err
	}
	if len(selected) > 100 {
		return ErrHouseholdSelection
	}
	previous, err := s.households(userID)
	if err != nil {
		return err
	}
	old := map[string]HouseholdAssociation{}
	for _, a := range previous {
		a, err = normalizedHousehold(a)
		if err != nil {
			return err
		}
		old[a.InstanceID] = a
	}
	type verified struct {
		association HouseholdAssociation
		config      string
	}
	checks := []verified{}
	seen := map[string]bool{}
	for _, a := range selected {
		a, err = normalizedHousehold(a)
		if err != nil || seen[a.InstanceID] {
			return ErrHouseholdSelection
		}
		seen[a.InstanceID] = true
		var raw string
		if err := s.db.QueryRow("SELECT json_array(service_type,url,api_key,media_server_config) FROM service_instances WHERE id=?", a.InstanceID).Scan(&raw); err != nil {
			return err
		}
		inst, err := s.store.Get(a.InstanceID)
		if err != nil {
			return err
		}
		if inst == nil || !videoServer(inst.ServiceType) {
			return ErrHouseholdSelection
		}
		// Preserve unchanged explicit assignments when a provider is unavailable;
		// they confer no effective access outside the currently selected scan set.
		if !reflect.DeepEqual(old[a.InstanceID], a) {
			cfg := inst.MediaServerConfig.AvailabilityScan
			if inst.MediaServerConfigInvalid || cfg == nil || !cfg.Enabled {
				return ErrHouseholdSelection
			}
			allowed := stringSet(cfg.LibraryIDs)
			readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			libraries, err := s.ScanLibraries(readCtx, a.InstanceID)
			cancel()
			if err != nil {
				var currentStamp string
				if stampErr := s.db.QueryRow("SELECT json_array(service_type,url,api_key,media_server_config) FROM service_instances WHERE id=?", a.InstanceID).Scan(&currentStamp); stampErr == nil && currentStamp != raw {
					return ErrHouseholdChanged
				}
				return fmt.Errorf("verify household library selection: %w", ErrUnavailable)
			}
			live := map[string]bool{}
			for _, library := range libraries {
				live[library.ID] = true
			}
			for _, id := range a.LibraryIDs {
				if !allowed[id] || !live[id] {
					return ErrHouseholdSelection
				}
			}
		}
		checks = append(checks, verified{a, raw})
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if actorID > 0 {
		var role string
		if err := tx.QueryRowContext(ctx, "SELECT role FROM users WHERE id=?", actorID).Scan(&role); err != nil || role != "admin" {
			return ErrHouseholdAdmin
		}
	}
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT 1 FROM users WHERE id=?", userID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrHouseholdUser
		}
		return err
	}
	// Do not overwrite a concurrent administrator's selection or revive a
	// household association retired by an intervening account link/unlink.
	currentRows, err := tx.QueryContext(ctx, "SELECT instance_id,library_ids FROM media_availability_households WHERE user_id=? ORDER BY instance_id", userID)
	if err != nil {
		return err
	}
	current := map[string]HouseholdAssociation{}
	for currentRows.Next() {
		var a HouseholdAssociation
		var raw string
		if err = currentRows.Scan(&a.InstanceID, &raw); err == nil {
			err = json.Unmarshal([]byte(raw), &a.LibraryIDs)
		}
		if err == nil {
			a, err = normalizedHousehold(a)
		}
		if err != nil {
			currentRows.Close()
			return err
		}
		current[a.InstanceID] = a
	}
	err = currentRows.Err()
	currentRows.Close()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, old) {
		return ErrHouseholdChanged
	}
	for _, check := range checks {
		var raw string
		if err := tx.QueryRowContext(ctx, "SELECT json_array(service_type,url,api_key,media_server_config) FROM service_instances WHERE id=?", check.association.InstanceID).Scan(&raw); err != nil || raw != check.config {
			return ErrHouseholdChanged
		}
		var linked int
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM user_media_server_accounts WHERE user_id=? AND instance_id=?)", userID, check.association.InstanceID).Scan(&linked); err != nil {
			return err
		}
		if linked != 0 {
			return ErrHouseholdLinked
		}
	}
	// Delete only removed rows; retained rows keep their revision unless edited.
	for _, a := range previous {
		if !seen[a.InstanceID] {
			if _, err := tx.ExecContext(ctx, "DELETE FROM media_availability_households WHERE user_id=? AND instance_id=?", userID, a.InstanceID); err != nil {
				return err
			}
		}
	}
	for _, check := range checks {
		a := check.association
		if reflect.DeepEqual(old[a.InstanceID], a) {
			continue
		}
		raw, _ := json.Marshal(a.LibraryIDs)
		if _, err := tx.ExecContext(ctx, `INSERT INTO media_availability_households(user_id,instance_id,library_ids,revision) VALUES(?,?,?,1)
			ON CONFLICT(user_id,instance_id) DO UPDATE SET library_ids=excluded.library_ids,revision=media_availability_households.revision+1`, userID, a.InstanceID, string(raw)); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.Invalidate("")
	return nil
}
