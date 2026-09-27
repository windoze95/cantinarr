package request

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/requestquota"
	"github.com/windoze95/cantinarr-server/internal/transporterr"
)

// Eligibility is captured in the admission transaction, never inferred from
// history. In particular, toggling an instance on cannot backfill old requests.
func captureRequesterTag(tx *sql.Tx, id int64) error {
	_, err := tx.Exec(`INSERT INTO request_tag_jobs(request_id,user_id)
 SELECT r.id,r.user_id FROM request_log r JOIN service_instances i ON i.id=r.instance_id
 WHERE r.id=? AND i.tag_requests=1 AND ((r.media_type='movie' AND i.service_type='radarr') OR (r.media_type='tv' AND i.service_type='sonarr') OR (r.media_type='music' AND i.service_type='lidarr'))`, id)
	return err
}

type RequesterTagReceipt struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Format   string `json:"format,omitempty"`
	*RequesterTagStatus
}

type RequesterTagStatus struct {
	Recipients []RequesterTagReceipt `json:"recipients,omitempty"`
	Status     string                `json:"status"`
	Message    string                `json:"message,omitempty"`
	CanRetry   bool                  `json:"can_retry"`
	TagLabel   string                `json:"tag_label,omitempty"`
	AppliedAt  *time.Time            `json:"applied_at,omitempty"`
}

func requesterTagStatus(state, message, label string, applied sql.NullTime, canRetry bool) *RequesterTagStatus {
	if state == "" {
		return nil
	}
	if state == "processing" {
		state = "pending"
	}
	if state == "waiting" && message == "" {
		message = "Waiting for request approval or delivery."
	}
	status := &RequesterTagStatus{Status: state, Message: message, TagLabel: label, CanRetry: canRetry}
	if applied.Valid {
		status.AppliedAt = &applied.Time
	}
	return status
}

func requesterTagLabel(userID int64, username string) (string, string) {
	prefix := fmt.Sprintf("cantinarr-%d-", userID)
	var slug strings.Builder
	for _, ch := range strings.ToLower(username) {
		if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' {
			slug.WriteRune(ch)
		} else if slug.Len() > 0 && !strings.HasSuffix(slug.String(), "-") {
			slug.WriteByte('-')
		}
		if slug.Len() >= 48 {
			break
		}
	}
	suffix := strings.Trim(slug.String(), "-")
	if suffix == "" {
		suffix = "user"
	}
	return prefix, prefix + suffix
}

const tagEligibleSQL = `EXISTS(SELECT 1 FROM users u WHERE u.id=j.user_id)
 AND r.status!='denied' AND i.tag_requests=1
 AND ((r.media_type='movie' AND i.service_type='radarr') OR (r.media_type='tv' AND i.service_type='sonarr') OR (r.media_type='music' AND i.service_type='lidarr') OR (r.media_type='book' AND i.service_type='chaptarr'))
 AND ((r.media_type!='book' AND r.user_id=j.user_id AND j.format='') OR (r.media_type='book' AND EXISTS(SELECT 1 FROM book_request_waiters bw WHERE bw.request_id=r.id AND bw.user_id=j.user_id AND (bw.book_format=j.format OR bw.book_format='both'))))`

const tagDeliveredSQL = `EXISTS(SELECT 1 FROM request_dispatch d WHERE d.request_id=r.id AND d.format=j.format AND d.state='complete')`

var errRequesterTagStopped = errors.New("Requester tagging is no longer eligible or its worker lease expired.")
var errRequesterTagAccess = errors.New("The requester no longer has access to this library or title.")

func (s *Service) StartRequesterTagMaintenance(ctx context.Context) {
	go func() {
		s.SweepRequesterTags(ctx)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.SweepRequesterTags(ctx)
			}
		}
	}()
}

// Tag delivery has its own lifecycle: a tag error never changes request_log,
// approval, delivery, quota charging, or availability notifications.
func (s *Service) SweepRequesterTags(ctx context.Context) {
	s.requesterTagsMu.Lock()
	defer s.requesterTagsMu.Unlock()
	_, err := s.db.ExecContext(ctx, `UPDATE request_tag_jobs AS j SET state='cancelled',message='The request, requester or destination is no longer eligible for tagging.',lease_token='',lease_until=0,updated_at=CURRENT_TIMESTAMP
 WHERE state NOT IN ('applied','cancelled') AND NOT EXISTS(SELECT 1 FROM request_log r JOIN service_instances i ON i.id=r.instance_id WHERE r.id=j.request_id AND `+tagEligibleSQL+`)`)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("request: reconcile requester tags: %v", err)
		}
		return
	}
	now := time.Now().Unix()
	rows, err := s.db.QueryContext(ctx, `SELECT j.id FROM request_tag_jobs j JOIN request_log r ON r.id=j.request_id JOIN service_instances i ON i.id=r.instance_id
 WHERE `+tagEligibleSQL+` AND `+tagDeliveredSQL+`
 AND ((j.state IN ('waiting','pending','retrying') AND j.next_attempt_at<=?) OR (j.state='processing' AND j.lease_until<=?))
 ORDER BY j.next_attempt_at,j.id LIMIT 32`, now, now)
	if err != nil {
		return
	}
	var ids []int64
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
		s.applyRequesterTag(ctx, id)
	}
}

func (s *Service) claimRequesterTag(id int64) (token, instanceID string, ok bool) {
	tx, err := requestquota.Begin(s.db)
	if err != nil {
		return
	}
	defer tx.Rollback()
	err = tx.QueryRow(`SELECT r.instance_id FROM request_tag_jobs j JOIN request_log r ON r.id=j.request_id JOIN service_instances i ON i.id=r.instance_id WHERE j.id=? AND `+tagEligibleSQL+` AND `+tagDeliveredSQL, id).Scan(&instanceID)
	if err != nil {
		return
	}
	now, until := time.Now().Unix(), time.Now().Add(dispatchLease).Unix()
	token = leaseToken()
	// Share the native mutation lease with media delivery. It is held only
	// during this attempt, never throughout retry backoff or manual attention.
	res, err := tx.Exec(`INSERT INTO request_dispatch_locks(instance_id,lease_token,lease_until) VALUES (?,?,?) ON CONFLICT(instance_id) DO UPDATE SET lease_token=excluded.lease_token,lease_until=excluded.lease_until WHERE request_dispatch_locks.lease_until<=?`, instanceID, token, until, now)
	if err != nil {
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return
	}
	res, err = tx.Exec(`UPDATE request_tag_jobs SET state='processing',attempts=attempts+1,lease_token=?,lease_until=?,updated_at=CURRENT_TIMESTAMP
 WHERE id=? AND ((state IN ('waiting','pending','retrying') AND next_attempt_at<=?) OR (state='processing' AND lease_until<=?))`, token, until, id, now, now)
	if err != nil {
		return
	}
	n, _ = res.RowsAffected()
	if n == 1 && tx.Commit() == nil {
		ok = true
	}
	return
}

func (s *Service) applyRequesterTag(parent context.Context, id int64) {
	token, instanceID, ok := s.claimRequesterTag(id)
	if !ok {
		return
	}
	defer s.db.Exec(`DELETE FROM request_dispatch_locks WHERE instance_id=? AND lease_token=?`, instanceID, token)
	// Bound the entire attempt below its durable lease. Provider reads that
	// predate context-aware clients still face a final context/lease check.
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	var requestID, requesterID int64
	var format string
	if s.db.QueryRow(`SELECT request_id,user_id,format FROM request_tag_jobs WHERE id=? AND lease_token=?`, id, token).Scan(&requestID, &requesterID, &format) != nil {
		return
	}
	r, _, err := s.loadRequest(requestID)
	if err != nil || r.instanceID != instanceID {
		s.finishRequesterTag(id, token, "", errRequesterTagStopped)
		return
	}
	var username string
	if err = s.db.QueryRow(`SELECT username FROM users WHERE id=?`, requesterID).Scan(&username); err != nil {
		s.finishRequesterTag(id, token, "", errRequesterTagStopped)
		return
	}
	var client *arr.RequesterTags
	var checkDestination func() error
	externalID := r.tmdbID
	var applyParent func(string, string, func() error) (string, error)
	if r.mediaType == "movie" {
		native, fingerprint, e := s.registry.GetFreshRadarrClient(instanceID)
		if e != nil {
			s.finishRequesterTag(id, token, "", errRequesterTagStopped)
			return
		}
		client = native.RequesterTags()
		checkDestination = func() error {
			_, current, err := s.registry.GetFreshRadarrClient(instanceID)
			if err != nil || current != fingerprint {
				return errRequesterTagStopped
			}
			return nil
		}
	} else if r.mediaType == "tv" {
		s.tvMatchMu.Lock()
		defer s.tvMatchMu.Unlock()
		target, _, _, e := s.loadTVTarget(requestID)
		if e != nil {
			s.finishRequesterTag(id, token, "", arr.ErrTagIdentity)
			return
		}
		externalID = target.Match.TVDBID
		native, fingerprint, e := s.registry.GetFreshSonarrClient(instanceID)
		if e != nil {
			s.finishRequesterTag(id, token, "", errRequesterTagStopped)
			return
		}
		client = native.RequesterTags()
		checkDestination = func() error {
			_, current, err := s.registry.GetFreshSonarrClient(instanceID)
			if err != nil || current != fingerprint {
				return errRequesterTagStopped
			}
			match, err := s.resolveTVMatch(native, r.tmdbID)
			if err != nil {
				return err
			}
			if match.Revision != target.Match.Revision || match.TVDBID != target.Match.TVDBID {
				return ErrTVMatchStale
			}
			return nil
		}
	} else if r.mediaType == "book" || r.mediaType == "music" {
		applyParent, checkDestination, err = s.parentRequesterTag(ctx, r, requestID, format)
		if err != nil {
			s.finishRequesterTag(id, token, "", err)
			return
		}
	} else {
		s.finishRequesterTag(id, token, "", errRequesterTagStopped)
		return
	}

	guard := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		authority, err := requestAuthority(s.db, requesterID)
		if err != nil {
			return errRequesterTagAccess
		}
		if err := checkDestination(); err != nil {
			return err
		}
		actor := requesterID
		// Movie approval can explicitly select an admin-only destination. Keep
		// that delivery authority while always attributing the original user.
		if r.mediaType == "movie" {
			var approver int64
			if s.db.QueryRow(`SELECT COALESCE(approved_by,0) FROM request_log WHERE id=?`, requestID).Scan(&approver) == nil && s.userIsAdmin(approver) {
				actor = approver
			}
		}
		if _, err := s.deliveryInstance(actor, r.mediaType, instanceID); err != nil {
			return errRequesterTagAccess
		}
		if err := s.checkContentPolicy(requesterID, s.userIsAdmin(requesterID), r.mediaType, r.tmdbID); err != nil {
			return err
		}
		// Provider lookups above can outlast a grant, role or kids-policy
		// change. Refuse a write authorized by a mixed snapshot.
		currentAuthority, err := requestAuthority(s.db, requesterID)
		if err != nil || authority != currentAuthority || (actor != requesterID && !s.userIsAdmin(actor)) {
			return errRequesterTagAccess
		}
		var held int
		now := time.Now().Unix()
		err = s.db.QueryRow(`SELECT COUNT(*) FROM request_tag_jobs j JOIN request_log r ON r.id=j.request_id JOIN service_instances i ON i.id=r.instance_id
 JOIN request_dispatch_locks l ON l.instance_id=i.id AND l.lease_token=j.lease_token
 WHERE j.id=? AND j.state='processing' AND j.lease_token=? AND j.lease_until>? AND l.lease_until>?
 AND r.instance_id=? AND j.user_id=? AND r.tmdb_id=? AND COALESCE(r.foreign_id,'')=? AND `+tagEligibleSQL+` AND `+tagDeliveredSQL, id, token, now, now, instanceID, requesterID, r.tmdbID, r.foreignID).Scan(&held)
		if err != nil || held != 1 {
			return errRequesterTagStopped
		}
		return ctx.Err()
	}
	if err = guard(); err != nil {
		s.finishRequesterTag(id, token, "", err)
		return
	}
	prefix, label := requesterTagLabel(requesterID, username)
	if applyParent != nil {
		label, err = applyParent(prefix, label, guard)
	} else {
		label, err = client.Apply(ctx, externalID, prefix, label, guard)
	}
	s.finishRequesterTag(id, token, label, err)
}

func requesterTagFailure(err error) (message string, retry bool, retryAfter time.Duration) {
	retry, retryAfter = transporterr.Retry(err)
	var upstream *transporterr.Upstream
	var matchErr *tvMatchError
	switch {
	case errors.As(err, &upstream):
		message = upstream.Message
	case errors.Is(err, arr.ErrTagIdentity), errors.Is(err, errRequesterTagAccess), errors.Is(err, errRequesterTagStopped):
		message = err.Error()
	case errors.Is(err, ErrTVMatchStale):
		message = "The TV match changed after this request. Review the saved request and current match before retrying."
	case errors.As(err, &matchErr):
		message = "The saved TV match could not be verified. Review the current TV match before retrying."
		retry = matchErr.code == "tv_metadata_unavailable"
	case errors.Is(err, ErrContentPolicyUnavailable):
		message, retry = "Title access could not be verified. Tagging will retry.", true
	case errors.Is(err, ErrTitleNotAvailable):
		message = "The requester no longer has access to this title."
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		message = "The tagging attempt was interrupted."
	default:
		message = "Requester tagging could not finish. Check library access and connection settings, then retry."
	}
	return
}

func (s *Service) finishRequesterTag(id int64, token, label string, failure error) {
	state, message, next := "applied", "", int64(0)
	var applied any = time.Now().UTC()
	if failure != nil {
		applied = nil
		var attempts int
		if s.db.QueryRow(`SELECT attempts FROM request_tag_jobs WHERE id=? AND lease_token=?`, id, token).Scan(&attempts) != nil {
			return
		}
		var retry bool
		var retryAfter time.Duration
		message, retry, retryAfter = requesterTagFailure(failure)
		state = "failed"
		if retry && attempts < 50 {
			state = "retrying"
			delay := time.Minute * time.Duration(1<<min(max(attempts-1, 0), 9))
			delay = min(delay, 6*time.Hour)
			delay = max(delay, retryAfter)
			next = time.Now().Add(delay).Unix()
		} else if retry {
			message = "Automatic retries exhausted. " + message
		}
	}
	_, err := s.db.Exec(`UPDATE request_tag_jobs SET state=?,message=?,next_attempt_at=?,tag_label=?,applied_at=?,lease_token='',lease_until=0,updated_at=CURRENT_TIMESTAMP
 WHERE id=? AND state='processing' AND lease_token=? AND lease_until>?`, state, message, next, label, applied, id, token, time.Now().Unix())
	if err != nil {
		log.Printf("request: save requester tag result: %v", err)
	}
}

// RetryRequesterTag requeues only an existing eligible failed/retrying job. It
// cannot create historical eligibility or rerun the media request itself.
func (h *Handler) RetryRequesterTag(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !h.service.userIsAdmin(claims.UserID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "requestID"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request ID"})
		return
	}
	res, err := h.service.db.ExecContext(r.Context(), `UPDATE request_tag_jobs AS j SET state='pending',attempts=0,next_attempt_at=0,message='',lease_token='',lease_until=0,updated_at=CURRENT_TIMESTAMP
 WHERE request_id=? AND state IN ('failed','retrying') AND EXISTS(SELECT 1 FROM users WHERE id=? AND role='admin')
 AND EXISTS(SELECT 1 FROM request_log r JOIN service_instances i ON i.id=r.instance_id WHERE r.id=j.request_id AND `+tagEligibleSQL+` AND `+tagDeliveredSQL+`)`, id, claims.UserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not retry requester tagging"})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "this request has no eligible requester tag to retry; refresh History"})
		return
	}
	tx, err := h.service.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read requester tagging"})
		return
	}
	defer tx.Rollback()
	status, err := loadRequesterTagStatus(r.Context(), tx, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read requester tagging"})
		return
	}
	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read requester tagging"})
		return
	}
	if !h.service.userIsAdmin(claims.UserID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
		return
	}
	writeJSON(w, http.StatusAccepted, status)
}

// Load after paginating request rows, so shared subscribers cannot duplicate
// history rows or consume a page's limit. Older clients keep the summary.
func loadRequesterTagStatus(ctx context.Context, tx *sql.Tx, requestID int64) (*RequesterTagStatus, error) {
	rows, err := tx.QueryContext(ctx, `SELECT j.user_id,COALESCE(u.username,''),j.format,j.state,j.message,j.tag_label,j.applied_at,
 COALESCE(j.state IN ('failed','retrying') AND `+tagEligibleSQL+` AND `+tagDeliveredSQL+`,0)
 FROM request_tag_jobs j JOIN request_log r ON r.id=j.request_id LEFT JOIN service_instances i ON i.id=r.instance_id
 LEFT JOIN users u ON u.id=j.user_id WHERE j.request_id=? ORDER BY j.user_id,j.format`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var receipts []RequesterTagReceipt
	for rows.Next() {
		var receipt RequesterTagReceipt
		var state, message, label string
		var applied sql.NullTime
		var canRetry bool
		if err = rows.Scan(&receipt.UserID, &receipt.Username, &receipt.Format, &state, &message, &label, &applied, &canRetry); err != nil {
			return nil, err
		}
		receipt.RequesterTagStatus = requesterTagStatus(state, message, label, applied, canRetry)
		receipts = append(receipts, receipt)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(receipts) == 0 {
		return nil, nil
	}
	if len(receipts) == 1 && receipts[0].Format == "" {
		return receipts[0].RequesterTagStatus, nil
	}
	summary := &RequesterTagStatus{Status: receipts[0].Status, Recipients: receipts}
	for _, receipt := range receipts {
		summary.CanRetry = summary.CanRetry || receipt.CanRetry
		if receipt.Status != summary.Status {
			summary.Status = "partial"
		}
	}
	return summary, nil
}
