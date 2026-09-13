package request

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/transporterr"
	"log"
	"time"
)

// StartDispatchMaintenance resumes persisted jobs after a restart. Leases are
// reclaimed only after expiry; no in-memory channel is the source of truth.
func (s *Service) StartDispatchMaintenance(ctx context.Context) {
	go func() {
		s.SweepDispatch(ctx)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.SweepDispatch(ctx)
			case <-s.dispatchWake:
				s.SweepDispatch(ctx)
			}
		}
	}()
}

// Wakeups coalesce; persisted jobs and the periodic sweep own recovery.
func (s *Service) wakeDispatch() {
	select {
	case s.dispatchWake <- struct{}{}:
	default:
	}
}

func (s *Service) SweepDispatch(ctx context.Context) {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	rows, err := s.db.Query(`SELECT DISTINCT d.request_id FROM request_dispatch d JOIN request_log r ON r.id=d.request_id WHERE r.status='pending' AND r.park_reason='delivery' AND ((d.state IN ('queued','retry') AND d.next_attempt_at<=?) OR (d.state='processing' AND d.lease_until<=?)) ORDER BY d.request_id LIMIT 32`, time.Now().Unix(), time.Now().Unix())
	if err != nil {
		log.Printf("request: read delivery queue: %v", err)
		return
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	readErr := rows.Err()
	rows.Close()
	if err != nil || readErr != nil {
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		s.dispatchRequest(ctx, id)
	}
	s.reconcileBookApprovals(ctx)
	s.reconcileMusicApprovals(ctx)
}

func (s *Service) dispatchRequest(ctx context.Context, id int64) {
	states, err := s.deliveryStates(id)
	if err != nil {
		return
	}
	for _, d := range states {
		if ctx.Err() != nil {
			return
		}
		if d.State == "queued" || d.State == "retry" || d.State == "processing" {
			s.dispatchFormat(ctx, id, d.Format)
		}
	}
}

func leaseToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// claimDelivery also owns an instance-wide lease. Alias and source/native
// requests cannot concurrently mutate the same library through different IDs.
func (s *Service) claimDelivery(id int64, format string) (string, string, bool) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", "", false
	}
	defer tx.Rollback()
	var instanceID, park, status string
	if err = tx.QueryRow(`SELECT COALESCE(instance_id,''),COALESCE(park_reason,''),status FROM request_log WHERE id=?`, id).Scan(&instanceID, &park, &status); err != nil || status != StatusPending || park != "delivery" {
		return "", "", false
	}
	now := time.Now().Unix()
	token := leaseToken()
	until := time.Now().Add(dispatchLease).Unix()
	res, err := tx.Exec(`INSERT INTO request_dispatch_locks(instance_id,lease_token,lease_until) VALUES (?,?,?) ON CONFLICT(instance_id) DO UPDATE SET lease_token=excluded.lease_token,lease_until=excluded.lease_until WHERE request_dispatch_locks.lease_until<=?`, instanceID, token, until, now)
	if err != nil {
		return "", "", false
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return "", "", false
	}
	res, err = tx.Exec(`UPDATE request_dispatch SET state='processing',lease_token=?,lease_until=?,attempts=attempts+1,last_attempt_at=? WHERE request_id=? AND format=? AND ((state IN ('queued','retry') AND next_attempt_at<=?) OR (state='processing' AND lease_until<=?))`, token, until, now, id, format, now, now)
	if err != nil {
		return "", "", false
	}
	n, _ = res.RowsAffected()
	if n == 0 {
		return "", "", false
	}
	if tx.Commit() != nil {
		return "", "", false
	}
	return token, instanceID, true
}

func (s *Service) dispatchFormat(ctx context.Context, id int64, format string) {
	token, instanceID, ok := s.claimDelivery(id, format)
	if !ok {
		return
	}
	done := make(chan struct{})
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				until := time.Now().Add(dispatchLease).Unix()
				s.db.Exec(`UPDATE request_dispatch SET lease_until=? WHERE request_id=? AND format=? AND lease_token=? AND state='processing'`, until, id, format, token)
				s.db.Exec(`UPDATE request_dispatch_locks SET lease_until=? WHERE instance_id=? AND lease_token=?`, until, instanceID, token)
			}
		}
	}()
	defer func() {
		close(done)
		<-heartbeatDone
		s.db.Exec(`DELETE FROM request_dispatch_locks WHERE instance_id=? AND lease_token=?`, instanceID, token)
	}()
	r, _, err := s.loadRequest(id)
	if err != nil {
		s.finishDelivery(id, format, token, "attention", "request_unavailable", nil)
		return
	}
	// Deleting or repointing an instance never silently routes saved work to a
	// new default. Access is checked under the original requester's authority.
	if instanceID == "" {
		s.finishDelivery(id, format, token, "attention", "instance_missing", nil)
		return
	}
	if _, err = s.deliveryInstance(r.userID, r.mediaType, instanceID); err != nil {
		s.finishDelivery(id, format, token, "attention", "access_unavailable", nil)
		return
	}
	if r.mediaType == "tv" {
		s.dispatchTV(ctx, id, token, r)
		return
	}
	var provider, sourceID string
	var confirmed bool
	err = s.db.QueryRow(`SELECT COALESCE(catalog_provider,''),COALESCE(catalog_id,''),match_confirmed FROM request_log WHERE id=?`, id).Scan(&provider, &sourceID, &confirmed)
	if err != nil {
		s.finishDelivery(id, format, token, "attention", "request_unavailable", nil)
		return
	}
	if provider == "openlibrary" {
		if !s.verifiedBookBinding(id, r.foreignID, confirmed) {
			s.finishDelivery(id, format, token, "attention", "catalog_retired", nil)
			return
		}
	} else if provider == "musicbrainz" || provider == "musicbrainz_release" {
		album, e := s.MusicCatalog.ResolveAlbum(ctx, sourceID, provider == "musicbrainz_release")
		if e != nil {
			state := "attention"
			if retry, _ := transporterr.Retry(e); retry {
				state = "retry"
			}
			s.finishDelivery(id, format, token, state, "catalog_unavailable", e)
			return
		}
		r.foreignID = album.ForeignID
		r.title = album.Title
	}
	if _, err = s.db.Exec(`UPDATE request_log SET foreign_id=?,title=? WHERE id=? AND status='pending' AND park_reason='delivery'`, r.foreignID, r.title, id); err != nil {
		return
	}
	// Recheck after public metadata reads and before any service mutation.
	if _, err = s.deliveryInstance(r.userID, r.mediaType, instanceID); err != nil {
		s.finishDelivery(id, format, token, "attention", "access_unavailable", nil)
		return
	}
	r.requestedBookFormats = r.bookFormat
	r.bookFormat = format
	// A lost POST response can hide an accepted native author import. Check it
	// before replaying an add; Chaptarr owns that retry loop once accepted.
	if r.mediaType == "book" {
		var attempts int
		var retryCode string
		s.db.QueryRow(`SELECT attempts,code FROM request_dispatch WHERE request_id=? AND format=?`, id, format).Scan(&attempts, &retryCode)
		manualImportRetry := retryCode == "manual_import_retry"
		if attempts > 1 || manualImportRetry {
			client, _, e := s.resolveChaptarr(r.userID, instanceID)
			if e != nil || client == nil {
				s.finishDelivery(id, format, token, "attention", "access_unavailable", nil)
				return
			}
			client = client.WithMutationGuard(func() error {
				_, _, err := s.resolveChaptarr(r.userID, instanceID)
				return err
			})
			books, e := client.GetAllBooks()
			if e != nil {
				s.finishDelivery(id, format, token, "retry", "library_unavailable", e)
				return
			}
			_, records, unresolved := recordsForForeignID(books, r.foreignID)
			if unresolved {
				s.finishDelivery(id, format, token, "attention", "library_identity_unresolved", nil)
				return
			}
			if len(records) == 0 {
				author, known := s.lookupParkedAuthorID(client, r.foreignID)
				if !known {
					s.finishDelivery(id, format, token, "retry", "import_identity_unavailable", nil)
					return
				}
				if author == "" {
					s.finishDelivery(id, format, token, "attention", "import_identity_unresolved", nil)
					return
				}
				imported, e := client.GetAuthorImportStatus(author)
				if e != nil && !errors.Is(e, chaptarr.ErrPendingImportAPIUnavailable) {
					s.finishDelivery(id, format, token, "retry", "import_status_unavailable", e)
					return
				}
				if e == nil && imported.Pending {
					state := "waiting_library"
					code := "author_import"
					if chaptarr.AuthorImportStatusConcluded(imported.Status) || imported.Status == chaptarr.AuthorImportStatusFailed {
						if manualImportRetry && imported.PendingID > 0 {
							if e = client.RetryPendingAuthorImport(imported.PendingID); e != nil {
								s.finishDelivery(id, format, token, "retry", "manual_import_retry", e)
								return
							}
						} else {
							state, code = "attention", "import_failed"
						}
					}
					s.finishDelivery(id, format, token, state, code, nil)
					return
				}
			}
		}
	}
	var held int
	if ctx.Err() != nil || s.db.QueryRow(`SELECT COUNT(*) FROM request_dispatch d JOIN request_dispatch_locks l ON l.instance_id=? AND l.lease_token=d.lease_token WHERE d.request_id=? AND d.format=? AND d.lease_token=? AND d.state='processing' AND d.lease_until>? AND l.lease_until>?`, instanceID, id, format, token, time.Now().Unix(), time.Now().Unix()).Scan(&held) != nil || held != 1 {
		return
	}
	status, title, err := s.addToArr(r)
	if err != nil {
		if _, accessErr := s.deliveryInstance(r.userID, r.mediaType, instanceID); accessErr != nil {
			s.finishDelivery(id, format, token, "attention", "access_unavailable", nil)
			return
		}
		switch {
		case errors.Is(err, chaptarr.ErrAuthorPendingImport):
			s.finishDelivery(id, format, token, "waiting_library", "author_import", nil)
		case errors.Is(err, chaptarr.ErrEditionsNotHydrated):
			s.finishDelivery(id, format, token, "retry", "editions_unavailable", err)
		case errors.Is(err, ErrBookMetadataUnresolved), errors.Is(err, ErrMusicMetadataUnresolved):
			s.finishDelivery(id, format, token, "attention", "metadata_unresolved", nil)
		default:
			retry, _ := transporterr.Retry(err)
			state := "attention"
			if retry {
				state = "retry"
			}
			s.finishDelivery(id, format, token, state, "service_unavailable", err)
		}
		return
	}
	if r.canonicalForeignID != "" {
		r.foreignID = r.canonicalForeignID
	}
	if title != "" {
		r.title = title
	}
	if _, err = s.db.Exec(`UPDATE request_log SET foreign_id=CASE WHEN catalog_provider IS NOT NULL THEN ? ELSE foreign_id END,title=CASE WHEN media_type='book' AND catalog_provider IS NULL AND title!='' THEN title ELSE ? END,book_record_id=COALESCE(NULLIF(?,0),book_record_id) WHERE id=? AND status='pending'`, r.foreignID, r.title, r.bookRecordIDs[format], id); err != nil {
		return
	}
	if _, err = s.db.Exec(`UPDATE request_dispatch SET canonical_foreign_id=?,book_record_id=? WHERE request_id=? AND format=? AND lease_token=?`, r.foreignID, r.bookRecordIDs[format], id, format, token); err != nil {
		return
	}
	s.finishDelivery(id, format, token, "complete", status, nil)
}

func (s *Service) finishDelivery(id int64, format, token, state, code string, cause error) {
	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	var attempts int
	if err = tx.QueryRow(`SELECT attempts FROM request_dispatch WHERE request_id=? AND format=? AND lease_token=? AND state='processing'`, id, format, token).Scan(&attempts); err != nil {
		return
	}
	next := int64(0)
	if state == "retry" {
		if attempts >= 50 {
			state = "attention"
			code = "retry_limit"
		} else {
			delay := time.Minute
			for i := 1; i < attempts && delay < 6*time.Hour; i++ {
				delay *= 2
			}
			if delay > 6*time.Hour {
				delay = 6 * time.Hour
			}
			_, after := transporterr.Retry(cause)
			if after > delay {
				delay = after
			}
			next = time.Now().Add(delay).Unix()
		}
	}
	message := deliveryMessage(state, code)
	if cause != nil {
		var upstream *transporterr.Upstream
		if errors.As(cause, &upstream) {
			log.Printf("request: delivery %d format %s: %s, upstream HTTP %d (attempt %d)", id, format, code, upstream.Status, attempts)
		} else {
			log.Printf("request: delivery %d format %s: %s (attempt %d)", id, format, code, attempts)
		}
	}
	if _, err = tx.Exec(`UPDATE request_dispatch SET state=?,code=?,message=?,next_attempt_at=?,lease_until=0,lease_token='' WHERE request_id=? AND format=? AND lease_token=?`, state, code, message, next, id, format, token); err != nil {
		return
	}
	var unfinished, waiting int
	if err = tx.QueryRow(`SELECT COUNT(*),COALESCE(SUM(CASE WHEN state='waiting_library' THEN 1 ELSE 0 END),0) FROM request_dispatch WHERE request_id=? AND state NOT IN ('complete','cancelled')`, id).Scan(&unfinished, &waiting); err != nil {
		return
	}
	if unfinished == 0 {
		_, err = tx.Exec(`UPDATE request_log SET status='requested',park_reason=NULL,completed_at=CURRENT_TIMESTAMP WHERE id=? AND status='pending' AND park_reason='delivery'`, id)
	} else if unfinished == waiting {
		_, err = tx.Exec(`UPDATE request_log SET park_reason='author_import' WHERE id=? AND status='pending' AND park_reason='delivery'`, id)
	}
	if err != nil {
		return
	}
	if err = tx.Commit(); err != nil {
		return
	}
	s.notifyDelivery(id, state)
}

func (s *Service) notifyDelivery(id int64, state string) {
	if s.notifier == nil {
		return
	}
	r, _, err := s.loadRequest(id)
	if err != nil {
		return
	}
	audience := []bookRequestSubscriber{{UserID: r.userID, BookFormat: r.bookFormat}}
	if r.mediaType == "book" {
		if audience, err = s.bookRequestAudience(id, r.userID, r.bookFormat); err != nil {
			return
		}
	}
	for _, subscriber := range audience {
		s.notifier.NotifyUser(subscriber.UserID, "request_updated", map[string]interface{}{"request_id": id, "delivery_state": state, "media_type": r.mediaType, "instance_id": r.instanceID, "foreign_id": r.foreignID, "tmdb_id": r.tmdbID, "title": r.title})
	}
}

func (s *Service) verifiedBookBinding(id int64, foreignID string, confirmed bool) bool {
	if foreignID == "" {
		return false
	}
	if confirmed {
		return true
	}
	var n int
	return s.db.QueryRow(`SELECT COUNT(*) FROM request_log r WHERE r.id=? AND (COALESCE(r.book_record_id,0)>0 OR EXISTS(SELECT 1 FROM request_dispatch d WHERE d.request_id=r.id AND (d.canonical_foreign_id!='' OR d.book_record_id>0 OR d.state='waiting_library')))`, id).Scan(&n) == nil && n > 0
}
