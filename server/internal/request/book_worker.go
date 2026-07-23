package request

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

const (
	bookRequestJobPollInterval = time.Second
	bookRequestJobMaxParallel  = 4
)

var errDurableBookSearchTargetInvalid = errors.New("durable searched book target is no longer valid")

type bookRequestJob struct {
	ID                  int64
	UserID              int64
	RequestID           int64
	ApprovedBy          int64
	InstanceID          string
	ForeignID           string
	Title               string
	BookFormat          string
	BookSelectionJSON   string
	BookSelection       *BookSelection
	State               string
	Phase               string
	PhaseFormat         string
	AuthorID            int
	ForeignAuthorID     string
	AuthorName          string
	BookID              int
	SearchAcknowledged  bool
	EbookStatus         string
	AudiobookStatus     string
	SettingsFingerprint []byte
	PhaseStartedAt      time.Time
	AttemptCount        int
}

func newBookWorkerGeneration() string {
	var value [8]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("proc-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value[:])
}

// prepareDirectBookJob persists the direct request before any Chaptarr write.
// The caller already owns the per-work lock, which makes overlap detection and
// insertion one same-process idempotency boundary.
func (s *Service) prepareDirectBookJob(r *resolvedRequest) (*bookRequestJob, *chaptarr.Client, bool, error) {
	if s.db == nil || s.registry == nil {
		return nil, nil, false, errors.New("book request worker is unavailable")
	}
	var activeID int64
	err := s.db.QueryRow(
		`SELECT id FROM book_request_jobs
		 WHERE instance_id = ? AND foreign_id = ?
		   AND state IN ('running','retry_wait','outcome_unknown')
		 LIMIT 1`,
		r.instanceID, r.foreignID,
	).Scan(&activeID)
	if err == nil {
		return &bookRequestJob{ID: activeID}, nil, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, false, fmt.Errorf("check active book request: %w", err)
	}
	client, fingerprint, err := s.registry.GetFreshChaptarrClient(r.instanceID)
	if err != nil || client == nil {
		return nil, nil, false, ErrChaptarrInstanceInvalid
	}
	generation := s.bookWorkerGeneration
	if generation == "" {
		generation = "synchronous"
	}
	selectionJSON, err := encodeBookSelection(r.bookSelection, r.bookFormat)
	if err != nil {
		return nil, nil, false, err
	}
	var failed bookRequestJob
	failedRows, err := s.db.Query(
		`SELECT id, user_id, title, book_format, COALESCE(book_selection_json, ''), ebook_status, audiobook_status, settings_fingerprint
		 FROM book_request_jobs
		 WHERE instance_id = ? AND foreign_id = ? AND state = 'failed'
		   AND request_id IS NULL AND user_id = ?
		 ORDER BY updated_at DESC, id DESC`,
		r.instanceID, r.foreignID, r.userID,
	)
	if err != nil {
		return nil, nil, false, fmt.Errorf("check failed book request: %w", err)
	}
	for failedRows.Next() {
		var candidate bookRequestJob
		if err := failedRows.Scan(&candidate.ID, &candidate.UserID, &candidate.Title, &candidate.BookFormat, &candidate.BookSelectionJSON,
			&candidate.EbookStatus, &candidate.AudiobookStatus, &candidate.SettingsFingerprint); err != nil {
			_ = failedRows.Close()
			return nil, nil, false, fmt.Errorf("scan failed book request: %w", err)
		}
		if failedBookJobCanResetFor(candidate, r.bookFormat) {
			failed = candidate
			break
		}
	}
	if err := failedRows.Err(); err != nil {
		_ = failedRows.Close()
		return nil, nil, false, fmt.Errorf("read failed book request: %w", err)
	}
	_ = failedRows.Close()
	if failed.ID != 0 {
		// Reset the failed owner in place only after the fresh instance binding is
		// available. Delete-then-insert would briefly erase the user's durable
		// failure and could lose it permanently if the replacement write failed.
		// A completed sibling is endpoint-bound durable proof, so retain it only
		// when this exact owner's retry uses the same settings fingerprint.
		ebookStatus, audiobookStatus := "", ""
		failedSelection, selectionErr := requireDecodedBookSelection(failed.BookSelectionJSON, normalizeBookFormat(failed.BookFormat))
		if selectionErr != nil {
			return nil, nil, false, selectionErr
		}
		sameBinding := bytes.Equal(failed.SettingsFingerprint, fingerprint[:]) &&
			bookSelectionsEquivalent(failedSelection, r.bookSelection, r.bookFormat)
		if sameBinding {
			ebookStatus = failed.EbookStatus
			audiobookStatus = failed.AudiobookStatus
		}
		result, resetErr := s.db.Exec(
			`UPDATE book_request_jobs SET user_id = ?, title = ?, book_format = ?, book_selection_json = ?,
			 request_id = NULL, approved_by = NULL,
			 state = 'running', phase = 'queued', phase_format = '', author_id = 0,
			 foreign_author_id = '', author_name = '', book_id = 0,
			 search_acknowledged = 0, ebook_status = ?, audiobook_status = ?,
			 settings_fingerprint = ?, attempt_count = 0,
			 next_attempt_at = CURRENT_TIMESTAMP, proc_generation = ?,
			 last_error_code = '', last_error_text = '',
			 phase_started_at = CURRENT_TIMESTAMP, created_at = CURRENT_TIMESTAMP,
			 updated_at = CURRENT_TIMESTAMP
			 WHERE id = ? AND state = 'failed'`,
			r.userID, r.title, r.bookFormat, selectionJSON, ebookStatus, audiobookStatus,
			fingerprint[:], generation, failed.ID,
		)
		if resetErr != nil {
			return nil, nil, false, fmt.Errorf("reset failed book request: %w", resetErr)
		}
		if reset, _ := result.RowsAffected(); reset != 1 {
			return &bookRequestJob{ID: failed.ID}, nil, true, nil
		}
		return &bookRequestJob{
			ID: failed.ID, UserID: r.userID, InstanceID: r.instanceID,
			ForeignID: r.foreignID, Title: r.title, BookFormat: r.bookFormat,
			BookSelectionJSON: selectionJSON, BookSelection: r.bookSelection,
			EbookStatus: ebookStatus, AudiobookStatus: audiobookStatus,
			SettingsFingerprint: append([]byte(nil), fingerprint[:]...),
		}, client, false, nil
	}
	result, err := s.db.Exec(
		`INSERT INTO book_request_jobs
		 (user_id, request_id, approved_by, instance_id, foreign_id, title, book_format, book_selection_json, state, phase,
		  settings_fingerprint, proc_generation)
		 VALUES (?, NULL, NULL, ?, ?, ?, ?, ?, 'running', 'queued', ?, ?)`,
		r.userID, r.instanceID, r.foreignID, r.title, r.bookFormat, selectionJSON, fingerprint[:], generation,
	)
	if err != nil {
		// The partial active-owner index closes the cross-process race between the
		// read above and this insert. Treat its winner as the active owner.
		if activeErr := s.db.QueryRow(
			`SELECT id FROM book_request_jobs WHERE instance_id = ? AND foreign_id = ?
			 AND state IN ('running','retry_wait','outcome_unknown') LIMIT 1`,
			r.instanceID, r.foreignID,
		).Scan(&activeID); activeErr == nil {
			return &bookRequestJob{ID: activeID}, nil, true, nil
		}
		return nil, nil, false, fmt.Errorf("persist book request job: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, nil, false, fmt.Errorf("read book request job id: %w", err)
	}
	return &bookRequestJob{
		ID: id, UserID: r.userID, InstanceID: r.instanceID, ForeignID: r.foreignID,
		Title: r.title, BookFormat: r.bookFormat,
		BookSelectionJSON: selectionJSON, BookSelection: r.bookSelection,
		SettingsFingerprint: append([]byte(nil), fingerprint[:]...),
	}, client, false, nil
}

func failedBookJobCanResetFor(job bookRequestJob, requestedFormat string) bool {
	requested := make(map[string]bool, 2)
	for _, format := range expandBookFormat(requestedFormat) {
		requested[format] = true
	}
	checkpoints := map[string]string{
		BookFormatEbook: job.EbookStatus, BookFormatAudiobook: job.AudiobookStatus,
	}
	for _, format := range expandBookFormat(job.BookFormat) {
		if strings.TrimSpace(checkpoints[format]) != "" {
			continue
		}
		if !requested[format] {
			return false
		}
	}
	return true
}

// prepareApprovalBookJob durably links a still-pending approval row to the
// exact Chaptarr mutation before any remote write. The request row deliberately
// remains pending until completeApprovedBookJob commits the verified outcome.
func (s *Service) prepareApprovalBookJob(adminID, requestID int64, r *resolvedRequest) (*bookRequestJob, *chaptarr.Client, bool, error) {
	if s.db == nil || s.registry == nil || requestID == 0 {
		return nil, nil, false, errors.New("book approval worker is unavailable")
	}
	var activeID, activeRequestID int64
	err := s.db.QueryRow(
		`SELECT id, COALESCE(request_id, 0) FROM book_request_jobs
		 WHERE instance_id = ? AND foreign_id = ?
		   AND state IN ('running','retry_wait','outcome_unknown')
		 LIMIT 1`,
		r.instanceID, r.foreignID,
	).Scan(&activeID, &activeRequestID)
	if err == nil {
		return &bookRequestJob{ID: activeID, RequestID: activeRequestID}, nil, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, false, fmt.Errorf("check active book approval: %w", err)
	}
	client, fingerprint, err := s.registry.GetFreshChaptarrClient(r.instanceID)
	if err != nil || client == nil {
		return nil, nil, false, ErrChaptarrInstanceInvalid
	}
	generation := s.bookWorkerGeneration
	if generation == "" {
		generation = "synchronous"
	}
	selectionJSON, err := encodeBookSelection(r.bookSelection, r.bookFormat)
	if err != nil {
		return nil, nil, false, err
	}
	var failed bookRequestJob
	err = s.db.QueryRow(
		`SELECT id, user_id, title, book_format, COALESCE(book_selection_json, ''), ebook_status, audiobook_status,
		        settings_fingerprint
		 FROM book_request_jobs
		 WHERE request_id = ? AND state = 'failed' LIMIT 1`,
		requestID,
	).Scan(&failed.ID, &failed.UserID, &failed.Title, &failed.BookFormat, &failed.BookSelectionJSON,
		&failed.EbookStatus, &failed.AudiobookStatus, &failed.SettingsFingerprint)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, false, fmt.Errorf("check failed book approval: %w", err)
	}
	if failed.ID != 0 {
		ebookStatus, audiobookStatus := "", ""
		failedSelection, selectionErr := requireDecodedBookSelection(failed.BookSelectionJSON, normalizeBookFormat(failed.BookFormat))
		if selectionErr != nil {
			return nil, nil, false, selectionErr
		}
		if bytes.Equal(failed.SettingsFingerprint, fingerprint[:]) &&
			bookSelectionsEquivalent(failedSelection, r.bookSelection, r.bookFormat) {
			ebookStatus = failed.EbookStatus
			audiobookStatus = failed.AudiobookStatus
		}
		result, resetErr := s.db.Exec(
			`UPDATE book_request_jobs SET user_id = ?, approved_by = ?, title = ?, book_format = ?, book_selection_json = ?,
			 state = 'running', phase = 'queued', phase_format = '', author_id = 0,
			 foreign_author_id = '', author_name = '', book_id = 0,
			 search_acknowledged = 0, ebook_status = ?, audiobook_status = ?,
			 settings_fingerprint = ?, attempt_count = 0,
			 next_attempt_at = CURRENT_TIMESTAMP, proc_generation = ?,
			 last_error_code = '', last_error_text = '',
			 phase_started_at = CURRENT_TIMESTAMP, created_at = CURRENT_TIMESTAMP,
			 updated_at = CURRENT_TIMESTAMP
			 WHERE id = ? AND state = 'failed'
			   AND EXISTS (SELECT 1 FROM request_log WHERE id = ? AND status = ?)`,
			r.userID, adminID, r.title, r.bookFormat, selectionJSON, ebookStatus, audiobookStatus,
			fingerprint[:], generation, failed.ID, requestID, StatusPending,
		)
		if resetErr != nil {
			if s.activeBookJobExists(r.instanceID, r.foreignID) {
				return &bookRequestJob{ID: failed.ID, RequestID: requestID}, nil, true, nil
			}
			return nil, nil, false, fmt.Errorf("reset failed book approval: %w", resetErr)
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return nil, nil, false, fmt.Errorf("request is not pending")
		}
		return &bookRequestJob{
			ID: failed.ID, UserID: r.userID, RequestID: requestID, ApprovedBy: adminID,
			InstanceID: r.instanceID, ForeignID: r.foreignID, Title: r.title,
			BookFormat: r.bookFormat, BookSelectionJSON: selectionJSON, BookSelection: r.bookSelection,
			EbookStatus: ebookStatus, AudiobookStatus: audiobookStatus,
			SettingsFingerprint: append([]byte(nil), fingerprint[:]...),
		}, client, false, nil
	}
	result, err := s.db.Exec(
		`INSERT INTO book_request_jobs
		 (user_id, request_id, approved_by, instance_id, foreign_id, title, book_format, book_selection_json,
		  state, phase, settings_fingerprint, proc_generation)
		 SELECT ?, ?, ?, ?, ?, ?, ?, ?, 'running', 'queued', ?, ?
		 FROM request_log WHERE id = ? AND status = ?`,
		r.userID, requestID, adminID, r.instanceID, r.foreignID, r.title, r.bookFormat,
		selectionJSON, fingerprint[:], generation, requestID, StatusPending,
	)
	if err != nil {
		if s.activeBookJobExists(r.instanceID, r.foreignID) {
			return &bookRequestJob{RequestID: requestID}, nil, true, nil
		}
		return nil, nil, false, fmt.Errorf("persist book approval job: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return nil, nil, false, fmt.Errorf("request is not pending")
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, nil, false, fmt.Errorf("read book approval job id: %w", err)
	}
	return &bookRequestJob{
		ID: id, UserID: r.userID, RequestID: requestID, ApprovedBy: adminID,
		InstanceID: r.instanceID, ForeignID: r.foreignID, Title: r.title,
		BookFormat: r.bookFormat, BookSelectionJSON: selectionJSON, BookSelection: r.bookSelection,
		SettingsFingerprint: append([]byte(nil), fingerprint[:]...),
	}, client, false, nil
}

func (s *Service) activeBookJobExists(instanceID, foreignID string) bool {
	active, err := s.hasActiveBookRequestJob(instanceID, foreignID)
	return err == nil && active
}

func applyBookJobCheckpoints(r *resolvedRequest, job *bookRequestJob) {
	if r == nil || job == nil {
		return
	}
	if r.bookFormats == nil {
		r.bookFormats = make(map[string]string, 2)
	}
	if job.EbookStatus != "" {
		r.bookFormats[BookFormatEbook] = job.EbookStatus
	}
	if job.AudiobookStatus != "" {
		r.bookFormats[BookFormatAudiobook] = job.AudiobookStatus
	}
}

// setBookJobPhase is the commit-before-I/O boundary. A zero job id remains a
// compatibility no-op; every direct and approval request uses a durable id.
func (s *Service) setBookJobPhase(jobID int64, phase, format string, authorID int, foreignAuthorID, authorName string, bookID int, searchAcknowledged bool) error {
	if jobID == 0 || s.db == nil {
		return nil
	}
	ack := 0
	if searchAcknowledged {
		ack = 1
	}
	query := `UPDATE book_request_jobs SET phase = ?, phase_format = ?, author_id = ?,
		 foreign_author_id = ?, author_name = ?, book_id = ?, search_acknowledged = ?,
		 phase_started_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND state = 'running'`
	args := []any{phase, format, authorID, strings.TrimSpace(foreignAuthorID), strings.TrimSpace(authorName), bookID, ack, jobID}
	if phase == "converging" {
		// Preserve a same-format uncertain search guard while the convergent retry
		// re-verifies the row. A later format in a "both" job may advance it.
		query += ` AND NOT (phase = 'search_inflight' AND phase_format = ?)`
		args = append(args, format)
	}
	result, err := s.db.Exec(query, args...)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		var state, currentPhase, currentFormat string
		if err := s.db.QueryRow(
			"SELECT state, phase, phase_format FROM book_request_jobs WHERE id = ?", jobID,
		).Scan(&state, &currentPhase, &currentFormat); err != nil {
			return fmt.Errorf("book request job is no longer active")
		}
		if phase == "converging" && state == "running" && currentPhase == "search_inflight" && currentFormat == format {
			return nil
		}
		return fmt.Errorf("book request job cannot advance from state %s", state)
	}
	return nil
}

func (s *Service) deferDirectBookJob(jobID int64, cause error) bool {
	if jobID == 0 || s.db == nil {
		return false
	}
	var phase string
	var attemptCount int
	var userID, requestID int64
	var format, instanceID, foreignID, title string
	if err := s.db.QueryRow(
		`SELECT phase, attempt_count, user_id, COALESCE(request_id, 0),
		        book_format, instance_id, foreign_id, title
		 FROM book_request_jobs WHERE id = ?`,
		jobID,
	).Scan(&phase, &attemptCount, &userID, &requestID, &format, &instanceID, &foreignID, &title); err != nil {
		return false
	}
	if bookJobErrorIsTerminalAtPhase(cause, phase) {
		_, body := bookRequestErrorResponse(cause, format)
		code := body["code"]
		result, err := s.db.Exec(
			`UPDATE book_request_jobs SET state = 'failed', next_attempt_at = CURRENT_TIMESTAMP,
			 last_error_code = ?, last_error_text = ?, updated_at = CURRENT_TIMESTAMP
			 WHERE id = ?`,
			code, boundedBookJobError(cause), jobID,
		)
		if err != nil {
			return false
		}
		changed, _ := result.RowsAffected()
		if changed == 1 && s.notifier != nil {
			data := map[string]interface{}{
				"media_type": "book", "foreign_id": foreignID, "instance_id": instanceID,
				"title": title, "book_format": format, "failure_code": code,
			}
			if requestID != 0 {
				data["request_id"] = requestID
				s.notifier.NotifyAdmins("request_approval_failed", data)
			} else {
				s.notifier.NotifyUser(userID, "request_status_changed", data)
			}
		}
		return false
	}
	state := "retry_wait"
	if phase == "seed_inflight" || phase == "search_inflight" || errors.Is(cause, ErrBookOutcomePending) {
		state = "outcome_unknown"
	}
	delay := bookJobRetryDelay(attemptCount)
	result, err := s.db.Exec(
		`UPDATE book_request_jobs SET state = ?, next_attempt_at = datetime('now', ?),
		 last_error_code = ?, last_error_text = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		state, fmt.Sprintf("+%d seconds", int(delay/time.Second)), bookJobErrorCode(cause), boundedBookJobError(cause), jobID,
	)
	if err != nil {
		return false
	}
	retained, _ := result.RowsAffected()
	s.wakeBookRequestWorker()
	return retained == 1
}

func bookJobRetryDelay(attemptCount int) time.Duration {
	seconds := 30
	switch attemptCount {
	case 0:
		seconds = 1
	case 1:
		seconds = 2
	case 2:
		seconds = 4
	case 3:
		seconds = 8
	case 4:
		seconds = 16
	}
	return time.Duration(seconds) * time.Second
}

func bookJobErrorIsTerminal(err error) bool {
	if errors.Is(err, ErrBookConfigurationInvalid) || errors.Is(err, ErrBookSelectionInvalid) || errors.Is(err, ErrBookMatchNotFound) ||
		errors.Is(err, ErrBookEditionUnavailable) || errors.Is(err, ErrBookFormatUnresolved) ||
		errors.Is(err, ErrBookMultiWorkUnsupported) || errors.Is(err, ErrBookMutationRejected) ||
		errors.Is(err, ErrBookSearchRejected) || errors.Is(err, ErrChaptarrInstanceInvalid) ||
		errors.Is(err, ErrChaptarrInstanceForbidden) || errors.Is(err, errBookTitleRequired) {
		return true
	}
	return bookUpstreamAuthFailure(err)
}

func bookJobErrorIsTerminalAtPhase(err error, phase string) bool {
	if bookJobErrorIsTerminal(err) {
		return true
	}
	// Before any seed/search intent is persisted, identity and lookup
	// verification failures cannot hide a committed remote write. Retaining
	// them as outcome_unknown would wedge this title and instance forever.
	return phase == "queued" && errors.Is(err, ErrBookMutationUnverified)
}

func bookJobErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrBookOutcomePending):
		return "book_outcome_pending"
	case errors.Is(err, ErrBookCatalogPending):
		return "book_catalog_pending"
	case errors.Is(err, ErrBookSearchUnconfirmed):
		return "book_search_unconfirmed"
	case errors.Is(err, ErrBookMutationUnverified):
		return "book_request_unverified"
	default:
		return "book_request_retry"
	}
}

func boundedBookJobError(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	if len(text) > 500 {
		text = text[:500]
	}
	return text
}

// completeDirectBookJob commits history and removes the durable work item in
// one SQLite transaction. A crash before this transaction is harmless: the
// worker re-verifies the live result and retries this exact commit.
func (s *Service) completeDirectBookJob(jobID int64, r *resolvedRequest, title string) error {
	if jobID == 0 {
		return errors.New("missing book request job")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var settingsFingerprint []byte
	if err := tx.QueryRow("SELECT settings_fingerprint FROM book_request_jobs WHERE id = ?", jobID).Scan(&settingsFingerprint); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	for _, format := range []string{BookFormatEbook, BookFormatAudiobook} {
		status, ok := r.bookFormats[format]
		if !ok || status == StatusUnavailable {
			continue
		}
		selectionJSON, err := encodeBookSelection(r.bookSelection, format)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO request_log
			 (user_id, tmdb_id, foreign_id, book_format, instance_id, book_settings_fingerprint, book_selection_json, media_type, title, status)
			 VALUES (?, 0, ?, ?, ?, ?, ?, 'book', ?, ?)`,
			r.userID, r.foreignID, format, r.instanceID, settingsFingerprint, sqlNullStr(selectionJSON), title, status,
		); err != nil {
			return err
		}
	}
	materialized, err := clearVerifiedBookFailuresTx(tx, r.instanceID, r.foreignID, r.bookFormats)
	if err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM book_request_jobs WHERE id = ?", jobID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.invalidateBookCaches(r.instanceID)
	if s.notifier != nil {
		s.notifier.NotifyUser(r.userID, "request_status_changed", map[string]interface{}{
			"media_type": "book", "foreign_id": r.foreignID, "instance_id": r.instanceID,
			"title": title, "book_formats": r.bookFormats,
		})
		for ownerID, formats := range materialized {
			if ownerID == r.userID {
				continue
			}
			s.notifier.NotifyUser(ownerID, "request_status_changed", map[string]interface{}{
				"media_type": "book", "foreign_id": r.foreignID, "instance_id": r.instanceID,
				"title": title, "book_formats": formats,
			})
		}
	}
	return nil
}

// completeApprovedBookJob is the sole pending -> approved boundary for a
// durable book approval. The audience history, replacement pending rows for a
// partial result, failure healing, and job deletion commit atomically.
func (s *Service) completeApprovedBookJob(job *bookRequestJob, r *resolvedRequest, title, status string) (*CreateResponse, error) {
	if job == nil || job.ID == 0 || job.RequestID == 0 {
		return nil, errors.New("missing durable book approval")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin request approval: %w", err)
	}
	defer tx.Rollback()
	var ownerID int64
	var ownerFormat, requestStatus string
	if err := tx.QueryRow(
		`SELECT user_id, COALESCE(book_format, ''), status
		 FROM request_log WHERE id = ? AND media_type = 'book'`,
		job.RequestID,
	).Scan(&ownerID, &ownerFormat, &requestStatus); err != nil {
		return nil, fmt.Errorf("load durable book approval: %w", err)
	}
	if requestStatus != StatusPending {
		return nil, fmt.Errorf("book approval request is no longer pending")
	}
	ownerFormat = normalizeBookFormat(ownerFormat)
	audience, err := bookRequestAudienceTx(tx, job.RequestID, ownerID, ownerFormat)
	if err != nil {
		return nil, err
	}
	primaryFormat, primaryStatus := "", ""
	for _, format := range []string{BookFormatEbook, BookFormatAudiobook} {
		if formatStatus := strings.TrimSpace(r.bookFormats[format]); formatStatus != "" && formatStatus != StatusUnavailable {
			primaryFormat, primaryStatus = format, formatStatus
			break
		}
	}
	if primaryFormat == "" {
		return nil, errors.New("verified book approval has no successful format")
	}
	var approvedBy any
	if job.ApprovedBy != 0 {
		approvedBy = job.ApprovedBy
	}
	primarySelectionJSON, err := encodeBookSelection(r.bookSelection, primaryFormat)
	if err != nil {
		return nil, err
	}
	result, err := tx.Exec(
		`UPDATE request_log SET status = ?, title = ?, book_format = ?, instance_id = ?,
		 book_settings_fingerprint = ?, book_selection_json = ?, approved_by = ?, decided_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status = ?`,
		primaryStatus, title, primaryFormat, r.instanceID, job.SettingsFingerprint, sqlNullStr(primarySelectionJSON),
		approvedBy, job.RequestID, StatusPending,
	)
	if err != nil {
		return nil, fmt.Errorf("update durable book approval: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return nil, fmt.Errorf("book approval request changed concurrently")
	}
	for _, format := range []string{BookFormatEbook, BookFormatAudiobook} {
		formatStatus := strings.TrimSpace(r.bookFormats[format])
		if format == primaryFormat || formatStatus == "" || formatStatus == StatusUnavailable {
			continue
		}
		selectionJSON, encodeErr := encodeBookSelection(r.bookSelection, format)
		if encodeErr != nil {
			return nil, encodeErr
		}
		if _, err := tx.Exec(
			`INSERT INTO request_log
			 (user_id, tmdb_id, foreign_id, book_format, instance_id,
			  book_settings_fingerprint, book_selection_json, media_type, title, status, approved_by, decided_at)
			 VALUES (?, 0, ?, ?, ?, ?, ?, 'book', ?, ?, ?, CURRENT_TIMESTAMP)`,
			ownerID, r.foreignID, format, r.instanceID, job.SettingsFingerprint,
			sqlNullStr(selectionJSON), title, formatStatus, approvedBy,
		); err != nil {
			return nil, fmt.Errorf("store approved book format: %w", err)
		}
	}
	for _, subscriber := range audience {
		if subscriber.UserID == ownerID {
			continue
		}
		for _, format := range expandBookFormat(subscriber.BookFormat) {
			formatStatus := strings.TrimSpace(r.bookFormats[format])
			if formatStatus == "" || formatStatus == StatusUnavailable {
				continue
			}
			selectionJSON, encodeErr := encodeBookSelection(r.bookSelection, format)
			if encodeErr != nil {
				return nil, encodeErr
			}
			if _, err := tx.Exec(
				`INSERT INTO request_log
				 (user_id, tmdb_id, foreign_id, book_format, instance_id,
				  book_settings_fingerprint, book_selection_json, media_type, title, status, approved_by, decided_at)
				 VALUES (?, 0, ?, ?, ?, ?, ?, 'book', ?, ?, ?, CURRENT_TIMESTAMP)`,
				subscriber.UserID, r.foreignID, format, r.instanceID,
				job.SettingsFingerprint, sqlNullStr(selectionJSON), title, formatStatus, approvedBy,
			); err != nil {
				return nil, fmt.Errorf("store subscriber book format: %w", err)
			}
		}
	}
	for _, format := range []string{BookFormatEbook, BookFormatAudiobook} {
		if r.bookFormats[format] != StatusUnavailable {
			continue
		}
		selectionJSON, encodeErr := encodeBookSelection(r.bookSelection, format)
		if encodeErr != nil {
			return nil, encodeErr
		}
		failedResult, err := tx.Exec(
			`INSERT INTO request_log
			 (user_id, tmdb_id, foreign_id, book_format, instance_id, book_selection_json, media_type, title, status)
			 VALUES (?, 0, ?, ?, ?, ?, 'book', ?, ?)`,
			ownerID, r.foreignID, format, r.instanceID, sqlNullStr(selectionJSON), title, StatusPending,
		)
		if err != nil {
			return nil, fmt.Errorf("retain failed book format: %w", err)
		}
		failedRequestID, err := failedResult.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("read failed book request id: %w", err)
		}
		for _, subscriber := range audience {
			if !bookFormatIncludes(subscriber.BookFormat, format) {
				continue
			}
			if _, err := tx.Exec(
				"INSERT INTO book_request_waiters (request_id, user_id, book_format) VALUES (?, ?, ?)",
				failedRequestID, subscriber.UserID, format,
			); err != nil {
				return nil, fmt.Errorf("retain failed book subscriber: %w", err)
			}
		}
	}
	materialized, err := clearVerifiedBookFailuresTx(tx, r.instanceID, r.foreignID, r.bookFormats)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec("DELETE FROM book_request_jobs WHERE id = ?", job.ID); err != nil {
		return nil, fmt.Errorf("clear durable book approval: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit durable book approval: %w", err)
	}
	s.invalidateBookCaches(r.instanceID)
	if s.notifier != nil {
		for _, subscriber := range audience {
			succeeded := make(map[string]string, 2)
			for _, format := range expandBookFormat(subscriber.BookFormat) {
				if formatStatus := r.bookFormats[format]; formatStatus != "" && formatStatus != StatusUnavailable {
					succeeded[format] = formatStatus
				}
			}
			if len(succeeded) == 0 {
				continue
			}
			s.notifier.NotifyUser(subscriber.UserID, "request_decision", map[string]interface{}{
				"decision": "approved", "tmdb_id": r.tmdbID, "media_type": "book",
				"title": title, "status": collapseBookStatuses(succeeded, StatusRequested),
				"foreign_id": r.foreignID, "book_format": concreteBookFormat(succeeded),
				"book_formats": succeeded, "instance_id": r.instanceID,
			})
		}
		for checkpointOwner, formats := range materialized {
			s.notifier.NotifyUser(checkpointOwner, "request_status_changed", map[string]interface{}{
				"media_type": "book", "foreign_id": r.foreignID, "instance_id": r.instanceID,
				"title": title, "book_formats": formats,
			})
		}
	}
	return &CreateResponse{Success: true, Status: status, Title: title, BookFormats: r.bookFormats}, nil
}

func bookRequestAudienceTx(tx *sql.Tx, requestID, ownerID int64, ownerFormat string) ([]bookRequestSubscriber, error) {
	audience := map[int64]string{ownerID: ownerFormat}
	rows, err := tx.Query(
		"SELECT user_id, COALESCE(book_format, 'both') FROM book_request_waiters WHERE request_id = ?",
		requestID,
	)
	if err != nil {
		return nil, fmt.Errorf("query book request subscribers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var userID int64
		var format string
		if err := rows.Scan(&userID, &format); err != nil {
			return nil, fmt.Errorf("scan book request subscriber: %w", err)
		}
		format = normalizeBookFormat(format)
		if !validBookFormat(format) {
			return nil, fmt.Errorf("book request subscriber has unsupported book_format %q", format)
		}
		if current, ok := audience[userID]; ok {
			audience[userID] = mergeBookFormats(current, format)
		} else {
			audience[userID] = format
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read book request subscribers: %w", err)
	}
	result := make([]bookRequestSubscriber, 0, len(audience))
	for userID, format := range audience {
		result = append(result, bookRequestSubscriber{UserID: userID, BookFormat: format})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UserID < result[j].UserID })
	return result, nil
}

// completeBookJobFormat records a concrete format before the caller may move
// to the next format. A crash in the second half of a "both" request therefore
// resumes without repeating the first format's search.
func (s *Service) completeBookJobFormat(r *resolvedRequest, format, status string) error {
	if r.bookFormats == nil {
		r.bookFormats = make(map[string]string)
	}
	r.bookFormats[format] = status
	if r.bookJobID == 0 {
		return nil
	}
	column := ""
	switch format {
	case BookFormatEbook:
		column = "ebook_status"
	case BookFormatAudiobook:
		column = "audiobook_status"
	default:
		return fmt.Errorf("unsupported completed book format %q", format)
	}
	result, err := s.db.Exec(
		`UPDATE book_request_jobs SET `+column+` = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND state = 'running'`,
		status, r.bookJobID,
	)
	if err != nil {
		return fmt.Errorf("persist completed %s request: %w", format, err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errors.New("book request job is no longer active")
	}
	return nil
}

func (s *Service) wakeBookRequestWorker() {
	if s.bookJobWake == nil {
		return
	}
	select {
	case s.bookJobWake <- struct{}{}:
	default:
	}
}

// StartBookRequestWorkers owns all continuation after the request fast path.
// The database is the queue; the channel only shortens latency.
func (s *Service) StartBookRequestWorkers(ctx context.Context) {
	if s.db == nil || s.registry == nil {
		return
	}
	s.bookWorkerGeneration = newBookWorkerGeneration()
	reclaimPending := s.reclaimStaleBookRequestJobClaims() != nil
	go func() {
		ticker := time.NewTicker(bookRequestJobPollInterval)
		defer ticker.Stop()
		for {
			if reclaimPending {
				reclaimPending = s.reclaimStaleBookRequestJobClaims() != nil
			} else {
				s.runDueBookRequestJobs(ctx)
			}
			select {
			case <-ctx.Done():
				return
			case <-s.bookJobWake:
			case <-ticker.C:
			}
		}
	}()
}

// reclaimStaleBookRequestJobClaims makes a new worker generation the durable
// owner after a process restart. Claims from this generation are left alone;
// every older running claim is made due again before polling begins.
func (s *Service) reclaimStaleBookRequestJobClaims() error {
	if s.db == nil || s.bookWorkerGeneration == "" {
		return nil
	}
	_, err := s.db.Exec(
		`UPDATE book_request_jobs SET
		 state = CASE
		   WHEN phase IN ('seed_inflight','search_inflight') THEN 'outcome_unknown'
		   ELSE 'retry_wait'
		 END,
		 proc_generation = '',
		 next_attempt_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		 WHERE state = 'running' AND proc_generation <> ?`,
		s.bookWorkerGeneration,
	)
	return err
}

func (s *Service) runDueBookRequestJobs(ctx context.Context) {
	rows, err := s.db.Query(
		`SELECT id, instance_id, foreign_id FROM book_request_jobs
		 WHERE state IN ('retry_wait','outcome_unknown') AND next_attempt_at <= CURRENT_TIMESTAMP
		 ORDER BY id LIMIT 16`,
	)
	if err != nil {
		return
	}
	var candidates []bookRequestJobCandidate
	for rows.Next() {
		var item bookRequestJobCandidate
		if rows.Scan(&item.id, &item.instanceID, &item.foreignID) == nil {
			candidates = append(candidates, item)
		}
	}
	_ = rows.Close()
	runBookRequestJobBatch(ctx, candidates, s.runDueBookRequestJob)
}

type bookRequestJobCandidate struct {
	id                    int64
	instanceID, foreignID string
}

// runBookRequestJobBatch prevents one slow Chaptarr request from globally
// stalling unrelated durable jobs while keeping remote pressure deliberately
// small. The callback remains synchronous per candidate; its existing
// configuration, work, and author locks retain the mutation boundaries.
func runBookRequestJobBatch(ctx context.Context, candidates []bookRequestJobCandidate, run func(context.Context, bookRequestJobCandidate)) {
	workerCount := min(len(candidates), bookRequestJobMaxParallel)
	if workerCount == 0 {
		return
	}
	jobs := make(chan bookRequestJobCandidate)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for item := range jobs {
				run(ctx, item)
			}
		}()
	}

sendLoop:
	for _, item := range candidates {
		select {
		case <-ctx.Done():
			break sendLoop
		case jobs <- item:
		}
	}
	close(jobs)
	workers.Wait()
}

func (s *Service) runDueBookRequestJob(ctx context.Context, item bookRequestJobCandidate) {
	if ctx.Err() != nil {
		return
	}
	unlockConfig := func() {}
	if s.registry != nil {
		// Match the direct/approval lock order: configuration first, then the
		// canonical work. Instance Update/Delete take the write side, so the
		// fresh client and every remote write below stay bound to one URL/key.
		unlockConfig = s.registry.LockInstanceConfigRead(item.instanceID)
	}
	defer unlockConfig()
	lock := s.bookLock(item.instanceID + "\x00" + item.foreignID)
	lock.Lock()
	defer lock.Unlock()
	// Claim only after obtaining the work lock. The synchronous owner may have
	// completed/deleted the row while this worker was waiting.
	result, err := s.db.Exec(
		`UPDATE book_request_jobs SET state = 'running', proc_generation = ?,
		 attempt_count = attempt_count + 1, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND state IN ('retry_wait','outcome_unknown')
		   AND next_attempt_at <= CURRENT_TIMESTAMP`,
		s.bookWorkerGeneration, item.id,
	)
	claimed := int64(0)
	if err == nil {
		claimed, _ = result.RowsAffected()
	}
	if claimed == 1 {
		s.runClaimedBookRequestJob(ctx, item.id)
	}
}

func (s *Service) runClaimedBookRequestJob(parent context.Context, jobID int64) {
	// Every normal exit deletes the row or moves it out of running. This
	// generation-fenced fallback covers load/reload errors and failed defer
	// writes so no claimed row is abandoned forever.
	defer s.releaseClaimedBookRequestJob(jobID)
	job, err := s.loadBookRequestJob(jobID)
	if err != nil {
		return
	}
	timeout := s.bookMutationTimeout
	if timeout <= 0 {
		timeout = defaultBookMutationTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	client, fingerprint, err := s.registry.GetFreshChaptarrClient(job.InstanceID)
	if err != nil || client == nil {
		s.deferBookJobForInstanceDrift(job)
		return
	}
	if !bytes.Equal(job.SettingsFingerprint, fingerprint[:]) {
		resumed, rebindErr := s.rebindBookJobForFreshInstance(ctx, client, job, fingerprint[:])
		if rebindErr != nil {
			s.deferDirectBookJob(job.ID, rebindErr)
			return
		}
		if !resumed {
			s.deferBookJobForInstanceDrift(job)
			return
		}
		job, err = s.loadBookRequestJob(job.ID)
		if err != nil {
			return
		}
	}
	if job.Phase == "seed_inflight" {
		if err := s.reconcileDurableSeed(ctx, client, job); err != nil {
			s.deferDirectBookJob(job.ID, err)
			return
		}
		job, err = s.loadBookRequestJob(job.ID)
		if err != nil {
			return
		}
	}
	if job.Phase == "search_inflight" {
		if err := s.restoreDurableSearchGuard(ctx, client, job); err != nil {
			s.deferDirectBookJob(job.ID, err)
			return
		}
	}
	r := &resolvedRequest{
		userID: job.UserID, actorID: job.ApprovedBy, foreignID: job.ForeignID, bookFormat: job.BookFormat,
		bookSelection: job.BookSelection, instanceID: job.InstanceID, mediaType: "book", title: job.Title, bookJobID: job.ID,
		bookFormats: map[string]string{},
	}
	applyBookJobCheckpoints(r, job)
	status, title, err := s.addToChaptarrWithClientContext(parent, r, client, job.InstanceID)
	if err != nil {
		s.deferDirectBookJob(job.ID, err)
		return
	}
	if job.RequestID != 0 {
		_, err = s.completeApprovedBookJob(job, r, title, status)
	} else {
		err = s.completeDirectBookJob(job.ID, r, title)
	}
	if err != nil {
		s.deferDirectBookJob(job.ID, err)
	}
}

func (s *Service) releaseClaimedBookRequestJob(jobID int64) {
	if s.db == nil || jobID == 0 || s.bookWorkerGeneration == "" {
		return
	}
	_, _ = s.db.Exec(
		`UPDATE book_request_jobs SET
		 state = CASE
		   WHEN phase IN ('seed_inflight','search_inflight') THEN 'outcome_unknown'
		   ELSE 'retry_wait'
		 END,
		 proc_generation = '', next_attempt_at = datetime('now', '+1 second'),
		 last_error_code = CASE WHEN last_error_code = '' THEN 'book_request_retry' ELSE last_error_code END,
		 last_error_text = CASE WHEN last_error_text = '' THEN 'Book request worker released an incomplete claim' ELSE last_error_text END,
		 updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND state = 'running' AND proc_generation = ?`,
		jobID, s.bookWorkerGeneration,
	)
}

func (s *Service) loadBookRequestJob(id int64) (*bookRequestJob, error) {
	var job bookRequestJob
	var ack int
	err := s.db.QueryRow(
		`SELECT id, user_id, COALESCE(request_id, 0), COALESCE(approved_by, 0),
		 instance_id, foreign_id, title, book_format, COALESCE(book_selection_json, ''), state, phase,
		 phase_format, author_id, foreign_author_id, author_name, book_id,
		 search_acknowledged, ebook_status, audiobook_status, settings_fingerprint,
		 phase_started_at, attempt_count
		 FROM book_request_jobs WHERE id = ?`, id,
	).Scan(&job.ID, &job.UserID, &job.RequestID, &job.ApprovedBy,
		&job.InstanceID, &job.ForeignID, &job.Title, &job.BookFormat, &job.BookSelectionJSON,
		&job.State, &job.Phase, &job.PhaseFormat, &job.AuthorID, &job.ForeignAuthorID,
		&job.AuthorName, &job.BookID, &ack, &job.EbookStatus, &job.AudiobookStatus,
		&job.SettingsFingerprint, &job.PhaseStartedAt, &job.AttemptCount)
	if err != nil {
		return &job, err
	}
	job.BookFormat = normalizeBookFormat(job.BookFormat)
	job.BookSelection, err = requireDecodedBookSelection(job.BookSelectionJSON, job.BookFormat)
	job.SearchAcknowledged = ack != 0
	return &job, err
}

func (s *Service) reconcileDurableSeed(ctx context.Context, client *chaptarr.Client, job *bookRequestJob) error {
	books, err := client.GetAllBooksContext(ctx)
	if err != nil {
		return fmt.Errorf("read seeded book catalog: %w", err)
	}
	matches := make([]chaptarr.Book, 0, 1)
	plausibleFootprint := false
	for _, book := range books {
		plausibleFootprint = plausibleFootprint || bookJobHasPlausibleTargetFootprint(book, job)
		if book.ForeignBookID != job.ForeignID || recordFormat(book) != job.PhaseFormat ||
			!bookTitlesMatch(book.Title, job.Title) || chaptarrTitleIsMultiWork(book.Title) ||
			!chaptarrBookResolved(book) {
			continue
		}
		if job.AuthorID > 0 && book.AuthorID != job.AuthorID {
			continue
		}
		matches = append(matches, book)
	}
	if len(matches) == 0 {
		if !plausibleFootprint && time.Since(job.PhaseStartedAt) >= defaultBookSeedOutcomeTTL {
			reset, resetErr := s.resetBookJobForExactPreflight(job, nil, false, defaultBookSeedOutcomeTTL)
			if resetErr != nil {
				return resetErr
			}
			if reset {
				s.clearBookJobTransientIdentity(job)
				return nil
			}
		}
		return fmt.Errorf("%w: seeded %s row is not visible yet", ErrBookCatalogPending, job.PhaseFormat)
	}
	authorID, err := chaptarrRecordAuthorID(matches)
	if err != nil {
		return err
	}
	author, err := client.GetAuthorContext(ctx, authorID)
	if err != nil {
		return fmt.Errorf("verify seeded author: %w", err)
	}
	target := chaptarrBookTarget{
		authorID: authorID, foreignAuthorID: job.ForeignAuthorID, authorName: job.AuthorName,
		foreignBookID: job.ForeignID, title: job.Title, mediaType: job.PhaseFormat,
		publication:       job.BookSelection.publication(job.PhaseFormat),
		explicitSelection: job.BookSelection != nil,
		selection:         bookSelectionForFormat(job.BookSelection, job.PhaseFormat),
	}
	if !chaptarrAuthorMatches(*author, target) {
		return fmt.Errorf("%w: seeded author identity changed", ErrBookMutationUnverified)
	}
	if err := s.setBookJobPhase(job.ID, "converging", job.PhaseFormat, authorID, author.ForeignAuthorID, author.AuthorName, matches[0].ID, false); err != nil {
		return err
	}
	s.clearUncertainBookSeed(s.bookSeedOutcomeKey(job.InstanceID, job.ForeignID, job.PhaseFormat))
	return nil
}

func (s *Service) restoreDurableSearchGuard(ctx context.Context, client *chaptarr.Client, job *bookRequestJob) error {
	if job.BookID <= 0 || job.PhaseFormat == "" {
		return fmt.Errorf("%w: durable search identity is incomplete", ErrBookMutationUnverified)
	}
	age := time.Since(job.PhaseStartedAt)
	if job.SearchAcknowledged {
		// The persisted acknowledgement is durable proof. Rehydrate it as fresh
		// process-local suppression even after a long outage.
		s.recordBookSearchAckAt(job.InstanceID, job.BookID, time.Now())
		return nil
	}
	proved, err := s.durableSearchEvidence(ctx, client, job)
	if err != nil {
		if !errors.Is(err, errDurableBookSearchTargetInvalid) {
			return err
		}
		if age < defaultBookSearchAckTTL {
			s.recordUncertainBookSearchAt(job.InstanceID, job.BookID, job.PhaseStartedAt)
			return fmt.Errorf("%w: guarded searched book cannot be re-resolved yet: %w", ErrBookSearchUnconfirmed, err)
		}
		reset, resetErr := s.resetBookJobForExactPreflight(job, nil, false, defaultBookSearchAckTTL)
		if resetErr != nil {
			return resetErr
		}
		if !reset {
			return fmt.Errorf("%w: expired searched book guard changed concurrently", ErrBookOutcomePending)
		}
		s.clearBookJobTransientIdentity(job)
		return nil
	}
	if proved {
		s.recordBookSearchAckAt(job.InstanceID, job.BookID, time.Now())
		return nil
	}
	if age < defaultBookSearchAckTTL {
		s.recordUncertainBookSearchAt(job.InstanceID, job.BookID, job.PhaseStartedAt)
		return nil
	}
	s.clearUncertainBookSearch(job.InstanceID, job.BookID)
	return s.setBookJobPhase(job.ID, "converging", job.PhaseFormat, job.AuthorID, job.ForeignAuthorID, job.AuthorName, job.BookID, false)
}

func (s *Service) durableSearchEvidence(ctx context.Context, client *chaptarr.Client, job *bookRequestJob) (bool, error) {
	book, err := client.GetBookContext(ctx, job.BookID)
	if err != nil {
		var statusErr *chaptarr.HTTPStatusError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound {
			return false, fmt.Errorf("%w: searched book no longer exists: %w", errDurableBookSearchTargetInvalid, err)
		}
		return false, fmt.Errorf("read searched book: %w", err)
	}
	if book.ForeignBookID != job.ForeignID || recordFormat(*book) != job.PhaseFormat ||
		!bookTitlesMatch(book.Title, job.Title) || (job.AuthorID > 0 && book.AuthorID != job.AuthorID) {
		return false, fmt.Errorf("%w: %w: searched book identity changed", errDurableBookSearchTargetInvalid, ErrBookMutationUnverified)
	}
	if publication := job.BookSelection.publication(job.PhaseFormat); publication != nil {
		editions, editionErr := client.GetEditionsContext(ctx, job.BookID)
		if editionErr != nil {
			return false, fmt.Errorf("read searched book publication: %w", editionErr)
		}
		target := chaptarrBookTarget{
			foreignBookID: job.ForeignID, title: job.Title, mediaType: job.PhaseFormat,
			publication: publication, explicitSelection: true,
		}
		matching, _, multiWork, matchErr := selectChaptarrEditionsForTarget(editions, *book, target)
		if multiWork {
			return false, fmt.Errorf("%w: %w", errDurableBookSearchTargetInvalid, ErrBookMultiWorkUnsupported)
		}
		if matchErr != nil || len(matching) != 1 || !matching[0].Monitored {
			return false, fmt.Errorf("%w: %w: searched publication changed", errDurableBookSearchTargetInvalid, ErrBookMutationUnverified)
		}
	}
	if chaptarrBookAvailable(*book) || book.Grabbed {
		return true, nil
	}
	commands, err := client.GetCommandsContext(ctx)
	if err != nil {
		return false, fmt.Errorf("read searched book commands: %w", err)
	}
	if chaptarrExactBookSearchActive(commands, job.BookID) {
		return true, nil
	}
	queue, err := client.GetQueueDetailedContext(ctx, 1, 100)
	if err != nil {
		return false, fmt.Errorf("read searched book queue: %w", err)
	}
	for _, item := range queue {
		if item.BookID == job.BookID && bookQueueItemDownloading(item) {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) recordBookSearchAckAt(instanceID string, bookID int, at time.Time) {
	s.bookSearchAckMu.Lock()
	s.bookSearchAcks[s.bookSearchAckKey(instanceID, bookID)] = at
	s.bookSearchAckMu.Unlock()
}

func (s *Service) recordUncertainBookSearchAt(instanceID string, bookID int, at time.Time) {
	s.bookSearchOutcomeMu.Lock()
	s.bookUncertainSearches[s.bookSearchAckKey(instanceID, bookID)] = at
	s.bookSearchOutcomeMu.Unlock()
}

func (s *Service) hasActiveBookRequestJob(instanceID, foreignID string) (bool, error) {
	if s.db == nil || instanceID == "" || foreignID == "" {
		return false, nil
	}
	var exists int
	err := s.db.QueryRow(
		`SELECT 1 FROM book_request_jobs
		 WHERE instance_id = ? AND foreign_id = ?
		   AND state IN ('running','retry_wait','outcome_unknown') LIMIT 1`,
		instanceID, foreignID,
	).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *Service) bookRequestFailure(instanceID, foreignID string, settingsFingerprint []byte, userID int64) (format, code string, checkpoints map[string]string, updatedAt int64, err error) {
	checkpoints = make(map[string]string, 2)
	if s.db == nil || instanceID == "" || foreignID == "" || len(settingsFingerprint) == 0 {
		return "", "", checkpoints, 0, nil
	}
	rows, err := s.db.Query(
		`SELECT user_id, book_format, last_error_code, ebook_status, audiobook_status,
		        CAST(strftime('%s', updated_at) AS INTEGER)
		 FROM book_request_jobs
		 WHERE instance_id = ? AND foreign_id = ? AND state = 'failed'
		   AND settings_fingerprint = ?
		 ORDER BY updated_at DESC, id DESC`,
		instanceID, foreignID, settingsFingerprint,
	)
	if err != nil {
		return "", "", checkpoints, 0, err
	}
	defer rows.Close()
	failed := make(map[string]bool, 2)
	for rows.Next() {
		var ownerID int64
		var rowFormat, rowCode, ebookStatus, audiobookStatus string
		var rowUpdatedAt int64
		if err := rows.Scan(&ownerID, &rowFormat, &rowCode, &ebookStatus, &audiobookStatus, &rowUpdatedAt); err != nil {
			return "", "", nil, 0, err
		}
		rowCheckpoints := map[string]string{
			BookFormatEbook: ebookStatus, BookFormatAudiobook: audiobookStatus,
		}
		if ownerID == userID {
			for checkpointFormat, status := range rowCheckpoints {
				if strings.TrimSpace(status) != "" {
					if _, exists := checkpoints[checkpointFormat]; !exists {
						checkpoints[checkpointFormat] = status
					}
				}
			}
		}
		rowHasFailure := false
		for _, failedFormat := range expandBookFormat(rowFormat) {
			if strings.TrimSpace(rowCheckpoints[failedFormat]) != "" {
				continue
			}
			failed[failedFormat] = true
			rowHasFailure = true
		}
		if rowHasFailure && (updatedAt == 0 || rowUpdatedAt > updatedAt) {
			updatedAt = rowUpdatedAt
			code = rowCode
		}
	}
	if err := rows.Err(); err != nil {
		return "", "", nil, 0, err
	}
	failedStatuses := make(map[string]string, len(failed))
	for failedFormat := range failed {
		failedStatuses[failedFormat] = StatusUnavailable
	}
	return concreteBookFormat(failedStatuses), code, checkpoints, updatedAt, nil
}

// clearVerifiedBookFailuresTx heals only formats that a current verified
// result actually covered. A denial or an unavailable sibling is not proof and
// must leave another requester's terminal failure intact. Completed checkpoints
// are first materialized for their original owner with their original binding.
func clearVerifiedBookFailuresTx(tx *sql.Tx, instanceID, foreignID string, successful map[string]string) (map[int64]map[string]string, error) {
	materialized := make(map[int64]map[string]string)
	if tx == nil || instanceID == "" || foreignID == "" {
		return materialized, nil
	}
	type failedRow struct {
		id, userID                                     int64
		title, format, selectionJSON, ebook, audiobook string
		fingerprint                                    []byte
	}
	rows, err := tx.Query(
		`SELECT id, user_id, title, book_format, COALESCE(book_selection_json, ''), ebook_status, audiobook_status, settings_fingerprint
		 FROM book_request_jobs
		 WHERE instance_id = ? AND foreign_id = ? AND state = 'failed'
		 ORDER BY updated_at, id`,
		instanceID, foreignID,
	)
	if err != nil {
		return nil, fmt.Errorf("read failed book requests: %w", err)
	}
	var failed []failedRow
	for rows.Next() {
		var row failedRow
		if err := rows.Scan(&row.id, &row.userID, &row.title, &row.format, &row.selectionJSON, &row.ebook, &row.audiobook, &row.fingerprint); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan failed book request: %w", err)
		}
		failed = append(failed, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("read failed book requests: %w", err)
	}
	_ = rows.Close()
	for _, row := range failed {
		checkpoints := map[string]string{
			BookFormatEbook: row.ebook, BookFormatAudiobook: row.audiobook,
		}
		for _, format := range []string{BookFormatEbook, BookFormatAudiobook} {
			status := strings.TrimSpace(checkpoints[format])
			if status == "" {
				continue
			}
			selection, decodeErr := requireDecodedBookSelection(row.selectionJSON, normalizeBookFormat(row.format))
			if decodeErr != nil {
				return nil, decodeErr
			}
			selectionJSON, encodeErr := encodeBookSelection(selection, format)
			if encodeErr != nil {
				return nil, encodeErr
			}
			if err := materializeBookCheckpointTx(tx, row.userID, instanceID, foreignID, row.title, format, status, row.fingerprint, selectionJSON); err != nil {
				return nil, err
			}
			if materialized[row.userID] == nil {
				materialized[row.userID] = make(map[string]string)
			}
			materialized[row.userID][format] = status
		}
		remaining := make([]string, 0, 2)
		for _, format := range expandBookFormat(row.format) {
			if strings.TrimSpace(checkpoints[format]) != "" {
				continue
			}
			if status := strings.TrimSpace(successful[format]); status != "" && status != StatusUnavailable {
				continue
			}
			remaining = append(remaining, format)
		}
		if len(remaining) == 0 {
			if _, err := tx.Exec("DELETE FROM book_request_jobs WHERE id = ? AND state = 'failed'", row.id); err != nil {
				return nil, fmt.Errorf("clear verified failed book request: %w", err)
			}
			continue
		}
		remainingFormat := remaining[0]
		if len(remaining) > 1 {
			remainingFormat = BookFormatBoth
		}
		selection, decodeErr := requireDecodedBookSelection(row.selectionJSON, normalizeBookFormat(row.format))
		if decodeErr != nil {
			return nil, decodeErr
		}
		selectionJSON, encodeErr := encodeBookSelection(selection, remainingFormat)
		if encodeErr != nil {
			return nil, encodeErr
		}
		if _, err := tx.Exec(
			`UPDATE book_request_jobs
			 SET book_format = ?, book_selection_json = ?, ebook_status = '', audiobook_status = '', updated_at = CURRENT_TIMESTAMP
			 WHERE id = ? AND state = 'failed'`,
			remainingFormat, selectionJSON, row.id,
		); err != nil {
			return nil, fmt.Errorf("narrow verified failed book request: %w", err)
		}
	}
	return materialized, nil
}

// supersedeFailedBookJobTx remains as a narrow compatibility helper for tests
// and older callers; its argument is treated as verified coverage, not merely a
// decision.
func supersedeFailedBookJobTx(tx *sql.Tx, instanceID, foreignID, verifiedFormat string) error {
	successful := make(map[string]string, 2)
	for _, format := range expandBookFormat(verifiedFormat) {
		successful[format] = StatusRequested
	}
	_, err := clearVerifiedBookFailuresTx(tx, instanceID, foreignID, successful)
	return err
}

func materializeBookCheckpointTx(tx *sql.Tx, userID int64, instanceID, foreignID, title, format, status string, settingsFingerprint []byte, selectionJSON ...string) error {
	storedSelection := ""
	if len(selectionJSON) > 0 {
		storedSelection = selectionJSON[0]
	}
	if _, err := tx.Exec(
		`INSERT INTO request_log
		 (user_id, tmdb_id, foreign_id, book_format, instance_id, book_settings_fingerprint, book_selection_json, media_type, title, status)
		 SELECT ?, 0, ?, ?, ?, ?, ?, 'book', ?, ?
		 WHERE NOT EXISTS (
		   SELECT 1 FROM request_log
		   WHERE user_id = ? AND foreign_id = ? AND media_type = 'book'
		     AND COALESCE(instance_id, '') = ? AND COALESCE(book_format, 'both') = ?
		     AND book_settings_fingerprint = ?
		     AND COALESCE(book_selection_json, '') = ?
		     AND status IN (?, ?, ?)
		 )`,
		userID, foreignID, format, instanceID, settingsFingerprint, sqlNullStr(storedSelection), title, status,
		userID, foreignID, instanceID, format, settingsFingerprint, storedSelection,
		StatusAvailable, StatusDownloading, StatusRequested,
	); err != nil {
		return fmt.Errorf("materialize completed failed-job checkpoint: %w", err)
	}
	return nil
}

func bookJobFingerprintRebindPolicy(job *bookRequestJob) (time.Duration, error) {
	switch job.Phase {
	case "queued":
		return 0, nil
	case "search_inflight":
		return defaultBookSearchAckTTL, nil
	case "seed_inflight", "converging":
		return defaultBookSeedOutcomeTTL, nil
	default:
		return defaultBookSeedOutcomeTTL, nil
	}
}

// rebindBookJobForFreshInstance bounds a settings-drift guard without carrying
// stale local IDs into the new configuration. The reset is one CAS and the
// caller always re-enters addToChaptarr's exact read-only preflight afterward.
func (s *Service) rebindBookJobForFreshInstance(ctx context.Context, client *chaptarr.Client, job *bookRequestJob, fingerprint []byte) (bool, error) {
	wait, err := bookJobFingerprintRebindPolicy(job)
	if err != nil {
		return false, err
	}
	if job.Phase != "queued" {
		footprint, inspectErr := s.inspectFreshBookJobTarget(ctx, client, job)
		if inspectErr != nil {
			return false, inspectErr
		}
		if footprint == bookJobTargetExact && job.Phase != "search_inflight" {
			wait = 0
		} else if footprint == bookJobTargetPlausible {
			// A provisional, conflicting, or otherwise odd footprint might be the
			// prior POST materializing. Never manufacture a duplicate around it.
			return false, nil
		}
	}
	if wait > 0 && (job.PhaseStartedAt.IsZero() || time.Since(job.PhaseStartedAt) < wait) {
		return false, nil
	}
	reset, err := s.resetBookJobForExactPreflight(job, fingerprint, true, wait)
	if err != nil || !reset {
		return reset, err
	}
	s.clearBookJobTransientIdentity(job)
	return true, nil
}

// resetBookJobForExactPreflight atomically discards only the current target's
// stale local identity. Opaque fingerprint changes also clear endpoint-bound
// format checkpoints; same-endpoint seed expiry preserves completed siblings.
func (s *Service) resetBookJobForExactPreflight(job *bookRequestJob, fingerprint []byte, clearCheckpoints bool, minAge time.Duration) (bool, error) {
	set := `phase = 'queued', phase_format = '', author_id = 0,
		 foreign_author_id = '', author_name = '', book_id = 0,
		 search_acknowledged = 0, phase_started_at = CURRENT_TIMESTAMP,
		 next_attempt_at = CURRENT_TIMESTAMP, last_error_code = '', last_error_text = '',
		 updated_at = CURRENT_TIMESTAMP`
	args := make([]any, 0, 8)
	if len(fingerprint) > 0 {
		set += ", settings_fingerprint = ?"
		args = append(args, fingerprint)
	}
	if clearCheckpoints {
		set += ", ebook_status = '', audiobook_status = ''"
	}
	query := `UPDATE book_request_jobs SET ` + set + `
		 WHERE id = ? AND state = 'running' AND phase = ? AND phase_format = ?
		   AND settings_fingerprint = ?`
	args = append(args, job.ID, job.Phase, job.PhaseFormat, job.SettingsFingerprint)
	if minAge > 0 {
		query += ` AND phase_started_at <= datetime('now', ?)`
		args = append(args, fmt.Sprintf("-%d seconds", int(minAge/time.Second)))
	}
	result, err := s.db.Exec(query, args...)
	if err != nil {
		return false, fmt.Errorf("reset book request for exact preflight: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return changed == 1, nil
}

type bookJobTargetFootprint int

const (
	bookJobTargetAbsent bookJobTargetFootprint = iota
	bookJobTargetPlausible
	bookJobTargetExact
)

func bookJobHasPlausibleTargetFootprint(book chaptarr.Book, job *bookRequestJob) bool {
	format := recordFormat(book)
	formatCouldMatch := format == job.PhaseFormat || format == chaptarr.FormatUnknown
	if !formatCouldMatch {
		return false
	}
	if strings.TrimSpace(book.ForeignBookID) == job.ForeignID {
		return true
	}
	return strings.TrimSpace(book.ForeignBookID) == "" && bookTitlesMatch(book.Title, job.Title)
}

func (s *Service) inspectFreshBookJobTarget(ctx context.Context, client *chaptarr.Client, job *bookRequestJob) (bookJobTargetFootprint, error) {
	if job.PhaseFormat != BookFormatEbook && job.PhaseFormat != BookFormatAudiobook {
		return bookJobTargetPlausible, nil
	}
	books, err := client.GetAllBooksContext(ctx)
	if err != nil {
		return bookJobTargetPlausible, fmt.Errorf("preflight rebound book catalog: %w", err)
	}
	footprint := bookJobTargetAbsent
	for _, book := range books {
		if !bookJobHasPlausibleTargetFootprint(book, job) {
			continue
		}
		footprint = bookJobTargetPlausible
		if book.ForeignBookID != job.ForeignID || recordFormat(book) != job.PhaseFormat ||
			!bookTitlesMatch(book.Title, job.Title) || chaptarrTitleIsMultiWork(book.Title) ||
			!chaptarrBookResolved(book) {
			continue
		}
		authorID := book.AuthorID
		if authorID == 0 && book.Author != nil {
			authorID = book.Author.ID
		}
		if authorID <= 0 {
			continue
		}
		author, authorErr := client.GetAuthorContext(ctx, authorID)
		if authorErr != nil {
			return bookJobTargetPlausible, fmt.Errorf("preflight rebound book author: %w", authorErr)
		}
		if bookJobFreshAuthorMatches(*author, job) {
			return bookJobTargetExact, nil
		}
	}
	return footprint, nil
}

func bookJobFreshAuthorMatches(author chaptarr.Author, job *bookRequestJob) bool {
	expectedProvider := strings.TrimSpace(job.ForeignAuthorID)
	actualProvider := strings.TrimSpace(author.ForeignAuthorID)
	if expectedProvider != "" {
		if actualProvider != "" {
			return actualProvider == expectedProvider
		}
		return job.AuthorName != "" && normalizeBookIdentity(author.AuthorName) == normalizeBookIdentity(job.AuthorName)
	}
	return job.AuthorName != "" && normalizeBookIdentity(author.AuthorName) == normalizeBookIdentity(job.AuthorName)
}

func (s *Service) clearBookJobTransientIdentity(job *bookRequestJob) {
	if job.PhaseFormat != "" {
		s.clearUncertainBookSeed(s.bookSeedOutcomeKey(job.InstanceID, job.ForeignID, job.PhaseFormat))
	}
	if job.BookID > 0 {
		s.clearUncertainBookSearch(job.InstanceID, job.BookID)
	}
}

// hasDurableAuthorSeedOwner extends the in-process author lock across request
// lifetimes and restarts. An unresolved seed may be creating the author as part
// of POST /book, so another title must not race a second nested-author POST.
func (s *Service) hasDurableAuthorSeedOwner(instanceID, foreignAuthorID, authorName string, exceptJobID int64) (bool, error) {
	if s.db == nil || instanceID == "" {
		return false, nil
	}
	rows, err := s.db.Query(
		`SELECT id, foreign_author_id, author_name FROM book_request_jobs
		 WHERE instance_id = ? AND phase = 'seed_inflight'
		   AND state IN ('running','retry_wait','outcome_unknown') AND id <> ?`,
		instanceID, exceptJobID,
	)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	foreignAuthorID = strings.TrimSpace(foreignAuthorID)
	authorName = normalizeBookIdentity(authorName)
	for rows.Next() {
		var id int64
		var candidateProvider, candidateName string
		if err := rows.Scan(&id, &candidateProvider, &candidateName); err != nil {
			return false, err
		}
		providerMatch := foreignAuthorID != "" && strings.TrimSpace(candidateProvider) == foreignAuthorID
		nameMatch := authorName != "" && normalizeBookIdentity(candidateName) == authorName
		if providerMatch || nameMatch {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Service) deferBookJobForInstanceDrift(job *bookRequestJob) {
	modifier := fmt.Sprintf("+%d seconds", int(bookJobRetryDelay(job.AttemptCount)/time.Second))
	_, _ = s.db.Exec(
		`UPDATE book_request_jobs SET state = 'outcome_unknown',
		 next_attempt_at = datetime('now', ?), last_error_code = 'book_instance_changed',
		 last_error_text = 'Chaptarr instance settings changed while the request was in progress',
		 updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		modifier, job.ID,
	)
}
