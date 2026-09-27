package instance

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

var ErrAssignmentSelection = errors.New("select existing regular users and an automation instance")

type Assignment struct {
	UserID              int64  `json:"user_id"`
	Assigned            bool   `json:"assigned"`
	PreferredInstanceID string `json:"preferred_instance_id"`
	EffectiveDefaultID  string `json:"effective_default_id"`
}

func (s *Store) Assignments(instanceID string) ([]Assignment, error) {
	service, err := s.ServiceTypeOf(instanceID)
	if err != nil {
		return nil, err
	}
	if !IsAutomationType(service) {
		return nil, ErrAssignmentSelection
	}
	rows, err := s.db.Query(`SELECT u.id,EXISTS(SELECT 1 FROM user_instance_grants g WHERE g.user_id=u.id AND g.instance_id=?),
 COALESCE((SELECT d.instance_id FROM user_default_instances d WHERE d.user_id=u.id AND d.service_type=?),'') FROM users u ORDER BY u.id`, instanceID, service)
	if err != nil {
		return nil, err
	}
	out := []Assignment{}
	for rows.Next() {
		var a Assignment
		if err = rows.Scan(&a.UserID, &a.Assigned, &a.PreferredInstanceID); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].EffectiveDefaultID, err = s.EffectiveDefaultInstanceID(out[i].UserID, service)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ChangeAssignments changes only named pairs. A filtered or stale client list
// cannot revoke anyone omitted from the operation. Validation is all-or-nothing.
func (s *Store) ChangeAssignments(instanceID string, userIDs []int64, add bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var service string
	if err = tx.QueryRow("SELECT service_type FROM service_instances WHERE id=?", instanceID).Scan(&service); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAssignmentSelection
		}
		return err
	}
	if !IsAutomationType(service) {
		return ErrAssignmentSelection
	}
	seen := map[int64]bool{}
	for _, id := range userIDs {
		var role string
		if err = tx.QueryRow("SELECT role FROM users WHERE id=?", id).Scan(&role); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrAssignmentSelection
			}
			return err
		}
		if role != "user" {
			return ErrAssignmentSelection
		}
		seen[id] = true
	}
	added := []grant{}
	for id := range seen {
		if add {
			res, e := tx.Exec("INSERT OR IGNORE INTO user_instance_grants(user_id,instance_id) VALUES (?,?)", id, instanceID)
			if e != nil {
				return e
			}
			n, e := res.RowsAffected()
			if e != nil {
				return e
			}
			if n > 0 {
				added = append(added, grant{id, instanceID})
			}
		} else {
			if _, err = tx.Exec("DELETE FROM user_instance_grants WHERE user_id=? AND instance_id=?", id, instanceID); err != nil {
				return err
			}
			if _, err = tx.Exec("DELETE FROM user_default_instances WHERE user_id=? AND instance_id=?", id, instanceID); err != nil {
				return err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.notifyAddedGrants(added)
	return nil
}

func (h *Handler) GetAssignments(w http.ResponseWriter, r *http.Request) {
	out, err := h.store.Assignments(chi.URLParam(r, "instanceID"))
	if err != nil {
		assignmentError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
func (h *Handler) ChangeAssignments(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action  string  `json:"action"`
		UserIDs []int64 `json:"user_ids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil || (body.Action != "add" && body.Action != "remove") || len(body.UserIDs) == 0 {
		http.Error(w, `{"error":"provide add or remove and selected user_ids"}`, http.StatusBadRequest)
		return
	}
	if err := h.store.ChangeAssignments(chi.URLParam(r, "instanceID"), body.UserIDs, body.Action == "add"); err != nil {
		assignmentError(w, err)
		return
	}
	h.notifyConfigChanged()
	h.GetAssignments(w, r)
}
func assignmentError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrAssignmentSelection) {
		http.Error(w, `{"error":"select existing regular users and an automation instance"}`, http.StatusBadRequest)
		return
	}
	http.Error(w, `{"error":"assignments temporarily unavailable"}`, http.StatusServiceUnavailable)
}
