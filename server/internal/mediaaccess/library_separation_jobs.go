package mediaaccess

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

const (
	absSeparationJobLease = 3 * time.Minute
	absSeparationRetry    = 5 * time.Minute
)

// StartABSLibrarySeparationMaintenance captures completed book deliveries
// after the initial privacy migration and retries ABS scanner/tag failures.
func (s *Service) StartABSLibrarySeparationMaintenance(ctx context.Context) {
	go func() {
		s.SweepABSLibrarySeparation(ctx)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.SweepABSLibrarySeparation(ctx)
			}
		}
	}()
}

// SweepABSLibrarySeparation is safe to call from tests and maintenance tools.
// It creates one durable job per actual requester, format, and managed ABS
// account, then independently retries each tag operation.
func (s *Service) SweepABSLibrarySeparation(ctx context.Context) {
	s.separationMu.Lock()
	defer s.separationMu.Unlock()
	if ctx.Err() != nil {
		return
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO abs_library_separation_jobs(request_id,user_id,format,instance_id)
 SELECT DISTINCT r.id,w.user_id,d.format,a.instance_id
 FROM request_log r JOIN request_dispatch d ON d.request_id=r.id
 JOIN book_request_waiters w ON w.request_id=r.id AND (w.book_format=d.format OR w.book_format='both')
 JOIN user_media_server_accounts a ON a.user_id=w.user_id AND a.manage_access=1 AND a.disabled_at IS NULL
 JOIN service_instances abs ON abs.id=a.instance_id AND abs.service_type='audiobookshelf'
 JOIN user_instance_grants g ON g.user_id=a.user_id AND g.instance_id=a.instance_id
 WHERE r.media_type='book' AND r.status!='denied' AND d.state='complete'
 AND d.format IN ('ebook','audiobook')
 AND EXISTS(SELECT 1 FROM abs_library_separation_runs sep WHERE sep.instance_id=a.instance_id AND sep.state='enabled')`)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("mediaaccess: capture ABS library tagging jobs: %v", err)
		}
		return
	}
	for n := 0; n < 20 && ctx.Err() == nil; n++ {
		id, token, ok := s.claimABSSeparationJob(ctx)
		if !ok {
			return
		}
		if err := s.applyABSSeparationJob(ctx, id, token); err != nil {
			s.retryABSSeparationJob(ctx, id, token, err)
		}
	}
}

func (s *Service) claimABSSeparationJob(ctx context.Context) (int64, string, bool) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, "", false
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM abs_library_separation_jobs
 WHERE (state IN ('waiting','retrying') AND next_attempt_at<=?) OR (state='processing' AND lease_until<=?)
 ORDER BY next_attempt_at,id LIMIT 1`, now, now).Scan(&id)
	if err != nil {
		return 0, "", false
	}
	var raw [16]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return 0, "", false
	}
	token := hex.EncodeToString(raw[:])
	res, err := tx.ExecContext(ctx, `UPDATE abs_library_separation_jobs SET state='processing',attempts=attempts+1,
 lease_token=?,lease_until=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND ((state IN ('waiting','retrying') AND next_attempt_at<=?) OR (state='processing' AND lease_until<=?))`,
		token, time.Now().Add(absSeparationJobLease).Unix(), id, now, now)
	if err != nil {
		return 0, "", false
	}
	changed, _ := res.RowsAffected()
	if changed != 1 || tx.Commit() != nil {
		return 0, "", false
	}
	return id, token, true
}

func (s *Service) applyABSSeparationJob(ctx context.Context, id int64, token string) error {
	var userID int64
	var format, absInstanceID, remoteID, sourceInstanceID, foreignID string
	var recordID int
	err := s.db.QueryRowContext(ctx, `SELECT j.user_id,j.format,j.instance_id,a.remote_user_id,
 r.instance_id,COALESCE(NULLIF(d.canonical_foreign_id,''),r.foreign_id,''),d.book_record_id
 FROM abs_library_separation_jobs j JOIN request_log r ON r.id=j.request_id
 JOIN request_dispatch d ON d.request_id=j.request_id AND d.format=j.format AND d.state='complete'
 JOIN user_media_server_accounts a ON a.user_id=j.user_id AND a.instance_id=j.instance_id AND a.manage_access=1
 JOIN user_instance_grants g ON g.user_id=a.user_id AND g.instance_id=a.instance_id
 JOIN abs_library_separation_runs sep ON sep.instance_id=j.instance_id AND sep.state='enabled'
 WHERE j.id=? AND j.state='processing' AND j.lease_token=? AND r.media_type='book' AND r.status!='denied'
 AND EXISTS(SELECT 1 FROM book_request_waiters w WHERE w.request_id=r.id AND w.user_id=j.user_id AND (w.book_format=j.format OR w.book_format='both'))`, id, token).
		Scan(&userID, &format, &absInstanceID, &remoteID, &sourceInstanceID, &foreignID, &recordID)
	if errors.Is(err, sql.ErrNoRows) {
		_, _ = s.db.ExecContext(ctx, `UPDATE abs_library_separation_jobs SET state='cancelled',lease_token='',lease_until=0,message='Request or managed ABS access is no longer active.',updated_at=CURRENT_TIMESTAMP WHERE id=? AND lease_token=?`, id, token)
		return nil
	}
	if err != nil {
		return err
	}
	unlock := s.lock(userID, absInstanceID)
	defer unlock()
	var stillManaged int
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_media_server_accounts a
 JOIN user_instance_grants g ON g.user_id=a.user_id AND g.instance_id=a.instance_id
 WHERE a.user_id=? AND a.instance_id=? AND a.remote_user_id=? AND a.manage_access=1 AND a.disabled_at IS NULL`, userID, absInstanceID, remoteID).Scan(&stillManaged); err != nil || stillManaged != 1 {
		return errors.New("Audiobookshelf access is no longer managed")
	}
	if sourceInstanceID == "" || foreignID == "" {
		return errors.New("book request does not have an exact Chaptarr identity")
	}
	source, err := s.store.Get(sourceInstanceID)
	if err != nil || source == nil || source.ServiceType != "chaptarr" {
		return errors.New("book request source is unavailable")
	}
	native := chaptarr.NewClient(source.URL, source.APIKey).WithContext(ctx)
	var book *chaptarr.Book
	if recordID > 0 {
		book, err = native.GetBookContext(ctx, recordID)
		if err != nil {
			return errors.New("Chaptarr book identity is temporarily unavailable")
		}
	} else {
		books, e := native.GetAllBooks()
		if e != nil {
			return errors.New("Chaptarr book identity is temporarily unavailable")
		}
		for i := range books {
			if books[i].ForeignBookID == foreignID && normalizeRequestFormat(books[i].MediaType) == format {
				if book != nil {
					return errors.New("Chaptarr book identity is ambiguous")
				}
				book = &books[i]
			}
		}
	}
	if book == nil || book.ID != recordID && recordID > 0 || book.ForeignBookID != foreignID || normalizeRequestFormat(book.MediaType) != format {
		return errors.New("Chaptarr book identity could not be verified")
	}
	identity := absBookRecordIdentity(absBookRequestIdentity{Format: format, UserIDs: []int64{userID}}, book)
	if len(identity.ASINs) == 0 && len(identity.ISBNs) == 0 {
		return errors.New("Chaptarr book has no exact ISBN or ASIN identity")
	}
	absInstance, err := s.store.Get(absInstanceID)
	if err != nil || absInstance == nil || absInstance.ServiceType != "audiobookshelf" {
		return errors.New("Audiobookshelf instance is unavailable")
	}
	client, err := absProvider(s, absInstance)
	if err != nil {
		return err
	}
	var libraryIDs []string
	rows, err := s.db.QueryContext(ctx, `SELECT library_id FROM abs_library_separation_runs WHERE instance_id=? AND state='enabled' ORDER BY library_id`, absInstanceID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var libraryID string
		if err = rows.Scan(&libraryID); err != nil {
			rows.Close()
			return err
		}
		libraryIDs = append(libraryIDs, libraryID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(libraryIDs) == 0 {
		return errors.New("Audiobookshelf library separation is not enabled")
	}
	itemIDs := map[string]bool{}
	for _, libraryID := range libraryIDs {
		inventory, e := client.BookInventory(ctx, libraryID)
		if e != nil {
			return e
		}
		for _, item := range inventory {
			if !absFormatSupports(item, format) {
				continue
			}
			asin := strings.ToUpper(strings.TrimSpace(item.ASIN))
			isbn := normalizeISBNSet(item.ISBN)
			matchASIN := false
			for _, id := range identity.ASINs {
				matchASIN = matchASIN || asin == strings.ToUpper(id)
			}
			matchISBN := len(isbn) > 0 && slicesOverlap(isbn, identity.ISBNs)
			if matchASIN || matchISBN {
				itemIDs[item.ID] = true
			}
		}
	}
	if len(itemIDs) == 0 {
		return errors.New("Audiobookshelf has not indexed the delivered book yet")
	}
	if !s.absSeparationJobLeaseHeld(ctx, id, token) {
		return errors.New("Audiobookshelf tagging job lease expired")
	}
	orderedIDs := make([]string, 0, len(itemIDs))
	for itemID := range itemIDs {
		orderedIDs = append(orderedIDs, itemID)
	}
	slices.Sort(orderedIDs)
	for _, itemID := range orderedIDs {
		if !s.absSeparationJobLeaseHeld(ctx, id, token) {
			return errors.New("Audiobookshelf tagging job lease expired")
		}
		if err = client.SetItemTag(ctx, itemID, ABSUserTag(userID), true); err != nil {
			return err
		}
		if err = client.SetItemTag(ctx, itemID, absSharedLibraryTag, false); err != nil {
			return err
		}
	}
	_, err = s.db.ExecContext(ctx, `UPDATE abs_library_separation_jobs SET state='applied',tag_label=?,message='',lease_token='',lease_until=0,
 applied_at=CURRENT_TIMESTAMP,updated_at=CURRENT_TIMESTAMP WHERE id=? AND state='processing' AND lease_token=?`, ABSUserTag(userID), id, token)
	return err
}

func slicesOverlap(a, b []string) bool {
	for _, left := range a {
		for _, right := range b {
			if left == right {
				return true
			}
		}
	}
	return false
}

func (s *Service) absSeparationJobLeaseHeld(ctx context.Context, id int64, token string) bool {
	var count int
	return s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM abs_library_separation_jobs WHERE id=? AND state='processing' AND lease_token=? AND lease_until>?`, id, token, time.Now().Unix()).Scan(&count) == nil && count == 1
}

func (s *Service) retryABSSeparationJob(ctx context.Context, id int64, token string, cause error) {
	if ctx.Err() != nil {
		return
	}
	if errors.Is(cause, sql.ErrNoRows) {
		_, _ = s.db.ExecContext(ctx, `UPDATE abs_library_separation_jobs SET state='cancelled',lease_token='',lease_until=0,message='Request or managed ABS access is no longer active.',updated_at=CURRENT_TIMESTAMP WHERE id=? AND lease_token=?`, id, token)
		return
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE abs_library_separation_jobs SET state='retrying',next_attempt_at=?,lease_token='',lease_until=0,
 message='Waiting for the exact book identity or Audiobookshelf scan/tag update.',updated_at=CURRENT_TIMESTAMP WHERE id=? AND state='processing' AND lease_token=?`, time.Now().Add(absSeparationRetry).Unix(), id, token)
	s.logger.Warn("mediaaccess: ABS book tagging will retry", "job_id", id, "err", cause)
}
