package request

import (
	"fmt"
	"github.com/windoze95/cantinarr-server/internal/requestquota"
)

// A requester cancels their subscription. Only an admin cancels shared work
// for everyone. Ownership moves to a remaining subscriber so subsequent
// delivery continues to run with a requester's current service grant.
func (s *Service) cancelDelivery(userID, id int64, r *resolvedRequest, admin bool) (*CreateResponse, error) {
	tx, err := requestquota.Begin(s.db)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	role, err := requestquota.Role(tx, userID)
	if err != nil {
		return nil, err
	}
	admin = role == "admin"
	var status, provider, sourceID string
	if err = tx.QueryRow(`SELECT user_id,COALESCE(book_format,''),status,COALESCE(catalog_provider,''),COALESCE(catalog_id,'') FROM request_log WHERE id=?`, id).Scan(&r.userID, &r.bookFormat, &status, &provider, &sourceID); err != nil {
		return nil, err
	}
	var total, processing, active int
	if err = tx.QueryRow(`SELECT COUNT(*),COALESCE(SUM(state='processing'),0),COALESCE(SUM(state NOT IN ('complete','cancelled')),0) FROM request_dispatch WHERE request_id=?`, id).Scan(&total, &processing, &active); err != nil {
		return nil, err
	}
	if total == 0 {
		return nil, fmt.Errorf("this request has no saved delivery")
	}
	if processing > 0 {
		return nil, fmt.Errorf("delivery is in progress; refresh before changing this request")
	}
	if status != StatusPending || active == 0 {
		return nil, fmt.Errorf("this request is no longer waiting for delivery")
	}
	requested := r.bookFormat
	remaining := []bookRequestSubscriber{}
	if r.mediaType == "book" {
		rows, e := tx.Query(`SELECT user_id,book_format FROM book_request_waiters WHERE request_id=? ORDER BY user_id`, id)
		if e != nil {
			return nil, e
		}
		found := false
		for rows.Next() {
			var sub bookRequestSubscriber
			if e = rows.Scan(&sub.UserID, &sub.BookFormat); e != nil {
				rows.Close()
				return nil, e
			}
			if sub.UserID == userID {
				requested, found = sub.BookFormat, true
			} else {
				remaining = append(remaining, sub)
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
		if !admin && r.userID != userID && !found {
			return nil, fmt.Errorf("request is not available to you")
		}
	} else if !admin && r.userID != userID {
		return nil, fmt.Errorf("request is not available to you")
	}
	responseID := id
	shared := !admin && len(remaining) > 0
	if shared {
		if _, err = tx.Exec(`DELETE FROM book_request_waiters WHERE request_id=? AND user_id=?`, id, userID); err != nil {
			return nil, err
		}
		owner := r.userID
		if owner == userID {
			owner = remaining[0].UserID
		}
		needed := remaining[0].BookFormat
		for _, sub := range remaining[1:] {
			needed = mergeBookFormats(needed, sub.BookFormat)
		}
		if _, err = tx.Exec(`UPDATE request_log SET user_id=?,book_format=? WHERE id=?`, owner, needed, id); err != nil {
			return nil, err
		}
		for _, format := range []string{BookFormatEbook, BookFormatAudiobook} {
			if !bookFormatIncludes(needed, format) {
				if _, err = tx.Exec(`UPDATE request_dispatch SET state='cancelled',message='',next_attempt_at=0 WHERE request_id=? AND format=? AND state!='complete'`, id, format); err != nil {
					return nil, err
				}
			}
		}
		if _, err = tx.Exec(`UPDATE request_log SET status='requested',park_reason=NULL,completed_at=CURRENT_TIMESTAMP WHERE id=? AND NOT EXISTS(SELECT 1 FROM request_dispatch WHERE request_id=? AND state NOT IN ('complete','cancelled'))`, id, id); err != nil {
			return nil, err
		}
		// Keep the departing requester's cancellation in their own history;
		// it is never visible as a pending job or another person's cancellation.
		res, e := tx.Exec(`INSERT INTO request_log(user_id,tmdb_id,foreign_id,instance_id,media_type,title,book_format,status,deny_reason,decided_at,catalog_provider,catalog_id) VALUES (?,0,?,?,'book',?,?,'denied','Cancelled',CURRENT_TIMESTAMP,?,?)`, userID, sqlNullStr(r.foreignID), r.instanceID, r.title, requested, sqlNullStr(provider), sqlNullStr(sourceID))
		if e != nil {
			return nil, e
		}
		responseID, e = res.LastInsertId()
		if e != nil {
			return nil, e
		}
		for _, format := range expandBookFormat(requested) {
			if _, err = tx.Exec(`INSERT INTO request_dispatch(request_id,format,state,code,message) SELECT ?,format,CASE WHEN state='complete' THEN state ELSE 'cancelled' END,code,'' FROM request_dispatch WHERE request_id=? AND format=?`, responseID, id, format); err != nil {
				return nil, err
			}
		}
	} else {
		if _, err = tx.Exec(`UPDATE request_dispatch SET state='cancelled',message='',next_attempt_at=0 WHERE request_id=? AND state!='complete'`, id); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`UPDATE request_log SET status='denied',deny_reason='Cancelled',park_reason=NULL,decided_at=CURRENT_TIMESTAMP WHERE id=? AND status='pending'`, id); err != nil {
			return nil, err
		}
	}
	refundUser := int64(0)
	if !admin {
		refundUser = userID
	}
	if err = s.Quotas.Release(tx, id, refundUser, "*", "", "cancelled"); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.quotaRequestChanged(id)
	if !shared && r.parkReason == bookParkReasonAuthorImport {
		s.cancelAuthorImportForDeniedRequest(id, r)
	}
	if shared {
		s.notifyDelivery(id, "subscription_changed")
	} else {
		s.notifyDelivery(id, "cancelled")
	}
	var ref *CatalogRef
	if provider != "" {
		ref = &CatalogRef{Provider: provider, ID: sourceID}
	}
	return s.deliveryResponse(userID, []int64{responseID}, r.title, r.instanceID, ref)
}
