package mediaaccess

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

var ErrLibrarySelection = errors.New("invalid library selection")

// LibraryPolicy is a person's choice, not a snapshot of ABS's live permissions.
// Omitted policies use the instance default when creating an account. Only an
// explicit edit adopts library management for an existing linked account.
type LibraryPolicy struct {
	Mode             string   `json:"mode"`
	LibraryIDs       []string `json:"library_ids"`
	SyncPending      bool     `json:"sync_pending"`
	ManagesLibraries bool     `json:"manages_libraries"`
	remoteUserID     string
}

type LibraryAccessSettings struct {
	DefaultLibraryIDs []string `json:"default_library_ids"`
	UserIDs           []int64  `json:"user_ids"`
	// On writes, include only explicitly edited policies. Omission keeps a
	// choice, including choices for people whose grant is being removed.
	Policies map[int64]LibraryPolicy `json:"policies"`
}

func (s *Service) libraryPolicy(userID int64, instanceID string) (LibraryPolicy, error) {
	p := LibraryPolicy{Mode: "default", LibraryIDs: []string{}}
	var raw string
	err := s.db.QueryRow(`SELECT mode, library_ids, remote_user_id, sync_pending
 FROM user_media_library_policies WHERE user_id=? AND instance_id=?`, userID, instanceID).
		Scan(&p.Mode, &raw, &p.remoteUserID, &p.SyncPending)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal([]byte(raw), &p.LibraryIDs); err != nil {
		return p, err
	}
	if err = validateLibraryPolicy(p); err != nil {
		return p, err
	}
	return p, nil
}

func validateLibraryIDs(ids []string) error {
	if len(ids) > 1000 {
		return ErrLibrarySelection
	}
	for _, id := range ids {
		if id == "" || len(id) > 128 || strings.TrimSpace(id) != id || strings.ContainsAny(id, "/\\\n\r\t ") {
			return ErrLibrarySelection
		}
	}
	return nil
}
func validateLibraryPolicy(p LibraryPolicy) error {
	if err := validateLibraryIDs(p.LibraryIDs); err != nil {
		return err
	}
	switch p.Mode {
	case "default", "all":
		if len(p.LibraryIDs) != 0 {
			return ErrLibrarySelection
		}
	case "selected":
		if len(p.LibraryIDs) == 0 {
			return ErrLibrarySelection
		}
	default:
		return ErrLibrarySelection
	}
	return nil
}
func (s *Service) accountLibraryIDs(userID int64, inst *instance.Instance) ([]string, error) {
	if inst.ServiceType != "audiobookshelf" {
		return inst.MediaServerConfig.LibraryIDs, nil
	}
	p, err := s.libraryPolicy(userID, inst.ID)
	if err != nil {
		return nil, err
	}
	switch p.Mode {
	case "all":
		return []string{}, nil
	case "selected":
		return p.LibraryIDs, nil
	default:
		return inst.MediaServerConfig.LibraryIDs, nil
	}
}

func (s *Service) LibraryAccess(instanceID string) (LibraryAccessSettings, error) {
	out := LibraryAccessSettings{UserIDs: []int64{}, Policies: map[int64]LibraryPolicy{}}
	inst, err := s.mediaServerInstance(instanceID)
	if err != nil {
		return out, err
	}
	if inst.ServiceType != "audiobookshelf" {
		return out, ErrNotMediaServer
	}
	if inst.MediaServerConfigInvalid {
		return out, ErrConfigInvalid
	}
	out.DefaultLibraryIDs = append([]string{}, inst.MediaServerConfig.LibraryIDs...)
	rows, err := s.db.Query("SELECT user_id FROM user_instance_grants WHERE instance_id=? ORDER BY user_id", instanceID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		out.UserIDs = append(out.UserIDs, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = s.db.Query(`SELECT p.user_id, p.mode, p.library_ids,
 p.sync_pending AND EXISTS(SELECT 1 FROM user_media_server_accounts a
 WHERE a.user_id=p.user_id AND a.instance_id=p.instance_id AND a.manage_access=1 AND a.remote_user_id=p.remote_user_id)
  , EXISTS(SELECT 1 FROM user_media_server_accounts a WHERE a.user_id=p.user_id
 AND a.instance_id=p.instance_id AND a.manage_access=1 AND a.remote_user_id=p.remote_user_id)
 FROM user_media_library_policies p WHERE p.instance_id=? ORDER BY p.user_id`, instanceID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var p LibraryPolicy
		var raw string
		if err = rows.Scan(&id, &p.Mode, &raw, &p.SyncPending, &p.ManagesLibraries); err != nil {
			return out, err
		}
		if err = json.Unmarshal([]byte(raw), &p.LibraryIDs); err != nil {
			return out, err
		}
		out.Policies[id] = p
	}
	return out, rows.Err()
}

// SaveLibraryAccess commits defaults, choices and grants together before any
// recipient can create an account with the new grant. Account locks fence
// creation, explicit library edits, unlinking and stopping management.
func (s *Service) SaveLibraryAccess(ctx context.Context, instanceID string, settings LibraryAccessSettings) (LibraryAccessSettings, error) {
	if err := validateLibraryIDs(settings.DefaultLibraryIDs); err != nil {
		return LibraryAccessSettings{}, err
	}
	for _, p := range settings.Policies {
		if err := validateLibraryPolicy(p); err != nil {
			return LibraryAccessSettings{}, err
		}
	}
	// Lock in user order, the same order for every bulk save. Read the directory
	// outside the transaction so the existing deletion locks remain effective.
	rows, err := s.db.Query("SELECT id FROM users ORDER BY id")
	if err != nil {
		return LibraryAccessSettings{}, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return LibraryAccessSettings{}, err
	}
	for _, id := range settings.UserIDs {
		if !slices.Contains(ids, id) {
			return LibraryAccessSettings{}, ErrUserNotFound
		}
	}
	for id := range settings.Policies {
		if !slices.Contains(ids, id) {
			return LibraryAccessSettings{}, ErrUserNotFound
		}
	}
	unlocks := []func(){}
	for _, id := range ids {
		unlocks = append(unlocks, s.lock(id, instanceID))
	}
	locked := true
	unlock := func() {
		if locked {
			for i := len(unlocks) - 1; i >= 0; i-- {
				unlocks[i]()
			}
			locked = false
		}
	}
	defer unlock()
	inst, err := s.mediaServerInstance(instanceID)
	if err != nil {
		return LibraryAccessSettings{}, err
	}
	if inst.ServiceType != "audiobookshelf" {
		return LibraryAccessSettings{}, ErrNotMediaServer
	}
	if inst.MediaServerConfigInvalid {
		return LibraryAccessSettings{}, ErrConfigInvalid
	}
	before, err := s.LibraryAccess(instanceID)
	if err != nil {
		return LibraryAccessSettings{}, err
	}
	bindings := map[int64]string{}
	for id := range settings.Policies {
		row, e := s.getAccount(id, instanceID)
		if e != nil {
			return LibraryAccessSettings{}, e
		}
		if row != nil {
			if !row.ManageAccess {
				return LibraryAccessSettings{}, ErrProtectedAccount
			}
			// Existing management authority permits saving intent during an
			// outage. Reconciliation verifies the live identity/admin status
			// before any write, within one bounded retry budget.
			bindings[id] = row.RemoteUserID
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LibraryAccessSettings{}, err
	}
	defer tx.Rollback()
	raw, _ := json.Marshal(append([]string{}, settings.DefaultLibraryIDs...))
	if _, err = tx.Exec(`UPDATE service_instances SET media_server_config=json_set(media_server_config,'$.library_ids',json(?)) WHERE id=?`, string(raw), instanceID); err != nil {
		return LibraryAccessSettings{}, err
	}
	for id, p := range settings.Policies {
		raw, _ = json.Marshal(append([]string{}, p.LibraryIDs...))
		if _, err = tx.Exec(`INSERT INTO user_media_library_policies(user_id,instance_id,mode,library_ids,remote_user_id,sync_pending)
  VALUES(?,?,?,?,?,?) ON CONFLICT(user_id,instance_id) DO UPDATE SET mode=excluded.mode,library_ids=excluded.library_ids,remote_user_id=excluded.remote_user_id,sync_pending=excluded.sync_pending`,
			id, instanceID, p.Mode, string(raw), bindings[id], bindings[id] != ""); err != nil {
			return LibraryAccessSettings{}, err
		}
	}
	if !sameLibrarySelection(before.DefaultLibraryIDs, settings.DefaultLibraryIDs) {
		if err = queueDefaultLibraries(tx, instanceID); err != nil {
			return LibraryAccessSettings{}, err
		}
	}
	if _, err = tx.Exec("DELETE FROM user_instance_grants WHERE instance_id=?", instanceID); err != nil {
		return LibraryAccessSettings{}, err
	}
	for _, id := range settings.UserIDs {
		if _, err = tx.Exec("INSERT OR IGNORE INTO user_instance_grants(user_id,instance_id) VALUES(?,?)", id, instanceID); err != nil {
			return LibraryAccessSettings{}, err
		}
	}
	if _, err = tx.Exec(`DELETE FROM user_default_instances WHERE instance_id=? AND user_id NOT IN
 (SELECT user_id FROM user_instance_grants WHERE instance_id=?)`, instanceID, instanceID); err != nil {
		return LibraryAccessSettings{}, err
	}
	if err = tx.Commit(); err != nil {
		return LibraryAccessSettings{}, err
	}
	unlock()
	for _, id := range settings.UserIDs {
		if !slices.Contains(before.UserIDs, id) {
			s.OnGrantAdded(id, instanceID)
		}
	}
	// One shared budget, with durable pending state for everything it cannot do.
	pass, cancel := context.WithTimeout(ctx, reconcileTimeout)
	defer cancel()
	for _, id := range ids {
		if pass.Err() != nil {
			break
		}
		u := s.lock(id, instanceID)
		s.reconcileAccountLocked(pass, id, instanceID)
		u()
	}
	return s.LibraryAccess(instanceID)
}

func sameLibrarySelection(a, b []string) bool {
	a = slices.Clone(a)
	b = slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func queueDefaultLibraries(tx *sql.Tx, instanceID string) error {
	_, err := tx.Exec(`INSERT INTO user_media_library_policies(user_id,instance_id,mode,library_ids,remote_user_id,sync_pending)
 SELECT a.user_id,a.instance_id,'default','[]',a.remote_user_id,1
 FROM user_media_server_accounts a LEFT JOIN user_media_library_policies p
 ON p.user_id=a.user_id AND p.instance_id=a.instance_id
 WHERE a.instance_id=? AND a.manage_access=1 AND COALESCE(p.mode,'default')='default'
 AND (a.created_by_cantinarr=1 OR p.remote_user_id=a.remote_user_id)
 ON CONFLICT(user_id,instance_id) DO UPDATE SET remote_user_id=excluded.remote_user_id,sync_pending=1`, instanceID)
	return err
}

func (s *Service) cancelLibrarySync(userID int64, instanceID string) error {
	_, err := s.db.Exec("UPDATE user_media_library_policies SET sync_pending=0,remote_user_id='' WHERE user_id=? AND instance_id=?", userID, instanceID)
	return err
}

// Called with the account lock held, only after live administrator protection.
func (s *Service) syncLibrariesLocked(ctx context.Context, inst *instance.Instance, row *accountRow, provider mediaserver.Provider) error {
	if inst.ServiceType != "audiobookshelf" {
		return nil
	}
	p, err := s.libraryPolicy(row.UserID, inst.ID)
	if err != nil {
		return err
	}
	if !p.SyncPending || p.remoteUserID != row.RemoteUserID {
		return nil
	}
	ids, err := s.accountLibraryIDs(row.UserID, inst)
	if err != nil {
		return err
	}
	// A deleted/unknown library must never become all-library access. Recheck
	// identifiers on retries as libraries may have changed during an outage.
	if len(ids) > 0 {
		libs, e := provider.Libraries(ctx)
		if e != nil {
			return e
		}
		for _, id := range ids {
			if !slices.ContainsFunc(libs, func(l mediaserver.Library) bool { return l.ID == id }) {
				return ErrLibrarySelection
			}
		}
	}
	if err = provider.SetLibraries(ctx, row.RemoteUserID, ids); err != nil {
		return err
	}
	_, err = s.db.Exec("UPDATE user_media_library_policies SET sync_pending=0 WHERE user_id=? AND instance_id=? AND remote_user_id=?", row.UserID, inst.ID, row.RemoteUserID)
	return err
}

func (h *Handler) GetLibraryAccess(w http.ResponseWriter, r *http.Request) {
	value, err := h.svc.LibraryAccess(chi.URLParam(r, "instanceID"))
	h.writeLibraryAccess(w, value, err)
}
func (h *Handler) UpdateLibraryAccess(w http.ResponseWriter, r *http.Request) {
	var body LibraryAccessSettings
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body.DefaultLibraryIDs == nil || body.UserIDs == nil {
		h.writeLibraryAccess(w, LibraryAccessSettings{}, ErrLibrarySelection)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), createTimeout)
	defer cancel()
	value, err := h.svc.SaveLibraryAccess(ctx, chi.URLParam(r, "instanceID"), body)
	if err == nil && h.configChanged != nil {
		h.configChanged()
	}
	h.writeLibraryAccess(w, value, err)
}
func (h *Handler) writeLibraryAccess(w http.ResponseWriter, value LibraryAccessSettings, err error) {
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, value)
	case errors.Is(err, ErrInstanceNotFound), errors.Is(err, ErrUserNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "instance or user not found"})
	case errors.Is(err, ErrLibrarySelection):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Choose at least one library for a specific selection, or choose all libraries or the server default."})
	case errors.Is(err, ErrProtectedAccount):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Enable account management before changing libraries. Administrator accounts cannot be changed."})
	case errors.Is(err, ErrNotMediaServer):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Library assignments require an Audiobookshelf instance."})
	default:
		h.logger.Warn("mediaaccess: library settings", "err", fmt.Sprint(err))
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Could not save library access. Retry shortly."})
	}
}
