package request

import (
	"context"
	"github.com/windoze95/cantinarr-server/internal/requestquota"
)

// Already-owned formats need no approval or library mutation. Reconcile them
// in the worker so saving an approval request never waits on Chaptarr.
func (s *Service) reconcileBookApprovals(ctx context.Context) {
	rows, err := s.db.Query(`SELECT DISTINCT r.id FROM request_log r JOIN request_dispatch d ON d.request_id=r.id
 WHERE r.media_type='book' AND r.status='pending' AND r.park_reason IS NULL AND d.state='approval' ORDER BY r.id LIMIT 32`)
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
		if ctx.Err() != nil {
			return
		}
		r, _, err := s.loadRequest(id)
		if err != nil {
			continue
		}
		client, instanceID, err := s.resolveChaptarr(r.userID, r.instanceID)
		if err != nil || client == nil {
			continue
		}
		projection, err := s.freshLiveBookProjection(client, instanceID)
		if err != nil {
			continue
		}
		live, canonicalID, err := projection.resolveFormatsWithLookup(client, r.foreignID, []string{r.title, r.searchTerm})
		if err != nil {
			continue
		}
		if _, _, err = s.resolveChaptarr(r.userID, instanceID); err != nil {
			continue
		}
		tx, err := requestquota.Begin(s.db)
		if err != nil {
			continue
		}
		changed := false
		for format, status := range live {
			if status != StatusAvailable && status != StatusDownloading && status != StatusRequested {
				continue
			}
			res, e := tx.Exec(`UPDATE request_dispatch SET state='complete',code=?,canonical_foreign_id=?,book_record_id=?,message='' WHERE request_id=? AND format=? AND state='approval'
       AND EXISTS(SELECT 1 FROM request_log WHERE id=? AND status='pending' AND park_reason IS NULL)`, status, canonicalID, projection.recordForFormat(canonicalID, format, status), id, format, id)
			err = e
			if err != nil {
				break
			}
			n, _ := res.RowsAffected()
			changed = changed || n > 0
			if n > 0 {
				err = s.refundNoOp(tx, id, format)
				if err != nil {
					break
				}
			}
		}
		if err == nil {
			_, err = tx.Exec(`UPDATE request_log SET status='requested',completed_at=CURRENT_TIMESTAMP WHERE id=? AND status='pending' AND park_reason IS NULL
       AND NOT EXISTS(SELECT 1 FROM request_dispatch WHERE request_id=? AND state NOT IN ('complete','cancelled'))`, id, id)
		}
		if err != nil {
			tx.Rollback()
			continue
		}
		if tx.Commit() == nil && changed {
			s.notifyDelivery(id, "approval")
			s.quotaRequestChanged(id)
		}
	}
}

// Music approval is also acknowledged before library reads. Existing complete
// or monitored releases need no mutation and can finish in the worker.
func (s *Service) reconcileMusicApprovals(ctx context.Context) {
	rows, err := s.db.Query(`SELECT DISTINCT r.id FROM request_log r JOIN request_dispatch d ON d.request_id=r.id WHERE r.media_type='music' AND r.status='pending' AND r.park_reason IS NULL AND d.state='approval' ORDER BY r.id LIMIT 32`)
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
		if ctx.Err() != nil {
			return
		}
		r, _, err := s.loadRequest(id)
		if err != nil {
			continue
		}
		client, instanceID, err := s.resolveLidarr(r.userID, r.instanceID)
		if err != nil || client == nil {
			continue
		}
		foreign := r.foreignID
		if foreign == "" {
			s.db.QueryRow(`SELECT catalog_id FROM request_log WHERE id=? AND catalog_provider='musicbrainz'`, id).Scan(&foreign)
		}
		albums, err := client.WithContext(ctx).GetAllAlbums()
		if err != nil {
			continue
		}
		recordID := 0
		status := ""
		for _, a := range albums {
			if a.ForeignAlbumID != foreign {
				continue
			}
			if albumComplete(a) {
				status = StatusAvailable
				recordID = a.ID
				break
			}
			if a.Monitored {
				status = StatusRequested
				recordID = a.ID
			}
		}
		if status == "" {
			continue
		}
		if _, _, err = s.resolveLidarr(r.userID, instanceID); err != nil {
			continue
		}
		tx, err := requestquota.Begin(s.db)
		if err != nil {
			continue
		}
		result, err := tx.Exec(`UPDATE request_dispatch SET state='complete',code=?,book_record_id=?,canonical_foreign_id=?,message='' WHERE request_id=? AND state='approval' AND EXISTS(SELECT 1 FROM request_log WHERE id=? AND status='pending' AND park_reason IS NULL)`, status, recordID, foreign, id, id)
		if err == nil {
			err = s.refundNoOp(tx, id, "")
		}
		if err == nil {
			_, err = tx.Exec(`UPDATE request_log SET status='requested',book_record_id=?,completed_at=CURRENT_TIMESTAMP WHERE id=? AND status='pending' AND park_reason IS NULL AND NOT EXISTS(SELECT 1 FROM request_dispatch WHERE request_id=? AND state NOT IN ('complete','cancelled'))`, recordID, id, id)
		}
		if err != nil {
			tx.Rollback()
			continue
		}
		changed, _ := result.RowsAffected()
		if tx.Commit() == nil && changed > 0 {
			s.notifyDelivery(id, "approval")
			s.quotaRequestChanged(id)
		}
	}
}
