package request

import (
	"context"
	"errors"
	"time"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

// New jobs retain format checkpoints while observing the same authoritative
// native import states as the legacy worker. Pending imports never age out.
func (s *Service) sweepDeliveryImports() {
	rows, err := s.db.Query(`SELECT DISTINCT r.id FROM request_log r JOIN request_dispatch d ON d.request_id=r.id WHERE r.status='pending' AND d.state='waiting_library' ORDER BY r.id`)
	if err != nil {
		return
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if rows.Scan(&id) != nil {
			rows.Close()
			return
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return
	}
	for _, id := range ids {
		r, _, err := s.loadRequest(id)
		if err != nil {
			continue
		}
		client, _, err := s.resolveChaptarr(r.userID, r.instanceID)
		state, code := "waiting_library", "author_import"
		if err != nil || client == nil {
			state, code = "attention", "access_unavailable"
		} else {
			author, known := s.lookupParkedAuthorID(client, r.foreignID)
			if !known {
				continue
			}
			if author == "" {
				state, code = "attention", "import_identity_unresolved"
			} else {
				status, e := client.GetAuthorImportStatus(author)
				switch {
				case errors.Is(e, chaptarr.ErrPendingImportAPIUnavailable):
					state = "queued" // older services retain their supported add-probe behavior
				case errors.Is(e, chaptarr.ErrAuthorProviderAmbiguous):
					state, code = "attention", "import_identity_ambiguous"
				case e != nil:
					continue
				case status.Exists:
					state = "queued"
				case status.Pending && !chaptarr.AuthorImportStatusConcluded(status.Status) && status.Status != chaptarr.AuthorImportStatusFailed:
				case status.Pending:
					state, code = "attention", "import_failed"
				default:
					state, code = "attention", "import_cancelled"
				}
			}
		}
		tx, err := s.db.Begin()
		if err != nil {
			continue
		}
		_, err = tx.Exec(`UPDATE request_dispatch SET state=?,code=?,message=?,next_attempt_at=0,last_attempt_at=? WHERE request_id=? AND state='waiting_library' AND EXISTS(SELECT 1 FROM request_log WHERE id=? AND status='pending')`, state, code, deliveryMessage(state, code), time.Now().Unix(), id, id)
		if err == nil && state != "waiting_library" {
			_, err = tx.Exec(`UPDATE request_log SET park_reason='delivery' WHERE id=? AND status='pending' AND park_reason='author_import'`, id)
		}
		if err != nil {
			tx.Rollback()
			continue
		}
		if tx.Commit() != nil {
			continue
		}
		if state != "waiting_library" {
			s.notifyDelivery(id, state)
		}
		if state == "queued" {
			s.dispatchRequest(context.Background(), id)
		}
	}
}
