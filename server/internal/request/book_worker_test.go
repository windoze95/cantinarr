package request

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

func prepareWorkerTestJob(t *testing.T, service *Service, userID int64, upstream *verifiedBookUpstream, format string) (*bookRequestJob, *resolvedRequest, string) {
	t.Helper()
	_, instanceID, err := service.resolveChaptarr(userID, "")
	if err != nil {
		t.Fatal(err)
	}
	resolved := &resolvedRequest{
		userID: userID, instanceID: instanceID, foreignID: upstream.foreignBookID,
		title: upstream.title, mediaType: "book", bookFormat: format,
	}
	job, _, active, err := service.prepareDirectBookJob(resolved)
	if err != nil {
		t.Fatalf("prepareDirectBookJob: %v", err)
	}
	if active {
		t.Fatal("new worker fixture unexpectedly found an active job")
	}
	resolved.bookJobID = job.ID
	return job, resolved, instanceID
}

func waitForBookJobCount(t *testing.T, service *Service, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := service.db.QueryRow("SELECT COUNT(*) FROM book_request_jobs").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	var count int
	_ = service.db.QueryRow("SELECT COUNT(*) FROM book_request_jobs").Scan(&count)
	t.Fatalf("book request job count = %d, want %d", count, want)
}

func waitForRecordedUserEvents(t *testing.T, recorder *recordingNotifier, want int) []notifierEvent {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		events := recorder.userEventsSnapshot()
		if len(events) >= want {
			return events
		}
		time.Sleep(5 * time.Millisecond)
	}
	events := recorder.userEventsSnapshot()
	t.Fatalf("recorded user event count = %d, want at least %d: %+v", len(events), want, events)
	return nil
}

func restartedBookWorker(service *Service) (*Service, context.CancelFunc) {
	restarted := NewService(service.db, service.registry, service.bridge, service.notifier)
	restarted.bookMutationTimeout = 250 * time.Millisecond
	restarted.bookSettleInterval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	restarted.StartBookRequestWorkers(ctx)
	return restarted, cancel
}

func prepareApprovalWorkerTestJob(t *testing.T, service *Service, userID int64, upstream *verifiedBookUpstream, format string) (*bookRequestJob, int64, int64, string) {
	t.Helper()
	_, instanceID, err := service.resolveChaptarr(userID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.createPending(&resolvedRequest{
		userID: userID, instanceID: instanceID, foreignID: upstream.foreignBookID,
		title: upstream.title, mediaType: "book", bookFormat: format,
	}); err != nil {
		t.Fatal(err)
	}
	var requestID int64
	if err := service.db.QueryRow(
		`SELECT id FROM request_log
		 WHERE user_id = ? AND instance_id = ? AND foreign_id = ? AND status = ?`,
		userID, instanceID, upstream.foreignBookID, StatusPending,
	).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	adminID := createTestAdmin(t, service)
	job, _, active, err := service.prepareApprovalBookJob(adminID, requestID, &resolvedRequest{
		userID: userID, instanceID: instanceID, foreignID: upstream.foreignBookID,
		title: upstream.title, mediaType: "book", bookFormat: format,
	})
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("new approval fixture unexpectedly found an active job")
	}
	return job, requestID, adminID, instanceID
}

func TestBookApprovalWorkerResumesVisibleSeedAndFinalizesPendingRow(t *testing.T) {
	upstream := newVerifiedBookUpstream("Approval Restart Seed", "approval-restart-seed")
	service, userID := newVerifiedMutationService(t, upstream)
	recorder := &recordingNotifier{}
	service.notifier = recorder
	job, requestID, adminID, _ := prepareApprovalWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
	if err := service.setBookJobPhase(
		job.ID, "seed_inflight", BookFormatEbook, 0,
		upstream.foreignAuthorID, upstream.authorName, 0, false,
	); err != nil {
		t.Fatal(err)
	}
	upstream.addExisting(BookFormatEbook, false)

	restarted, cancel := restartedBookWorker(service)
	defer cancel()
	waitForBookJobCount(t, restarted, 0)

	var status string
	var approvedBy int64
	var fingerprint []byte
	if err := restarted.db.QueryRow(
		`SELECT status, approved_by, book_settings_fingerprint
		 FROM request_log WHERE id = ?`, requestID,
	).Scan(&status, &approvedBy, &fingerprint); err != nil {
		t.Fatal(err)
	}
	if status != StatusRequested || approvedBy != adminID || len(fingerprint) == 0 {
		t.Fatalf("finalized approval status=%q admin=%d fingerprint=%x", status, approvedBy, fingerprint)
	}
	upstream.mu.Lock()
	seedCalls := len(upstream.seedBodies)
	monitorCalls := len(upstream.monitorIDs)
	searchCalls := upstream.searchCalls
	upstream.mu.Unlock()
	if seedCalls != 0 || monitorCalls != 1 || searchCalls != 1 {
		t.Fatalf("resumed approval seed=%d monitor=%d search=%d, want 0/1/1", seedCalls, monitorCalls, searchCalls)
	}
	userEvents := waitForRecordedUserEvents(t, recorder, 1)
	approvedEvents := 0
	for _, event := range userEvents {
		if event.userID == userID && event.eventType == "request_decision" && event.data["decision"] == "approved" {
			approvedEvents++
		}
	}
	if approvedEvents != 1 {
		t.Fatalf("approval events=%d, want exactly one: %+v", approvedEvents, userEvents)
	}
}

func TestBookApprovalLostSearchResponseResumesWithoutDuplicateSearch(t *testing.T) {
	upstream := newVerifiedBookUpstream("Approval Lost Search", "approval-lost-search")
	upstream.dropSearchResponse = true
	service, userID := newVerifiedMutationService(t, upstream)
	_, instanceID, err := service.resolveChaptarr(userID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.createPending(&resolvedRequest{
		userID: userID, instanceID: instanceID, foreignID: upstream.foreignBookID,
		title: upstream.title, mediaType: "book", bookFormat: BookFormatEbook,
	}); err != nil {
		t.Fatal(err)
	}
	var requestID int64
	if err := service.db.QueryRow(
		"SELECT id FROM request_log WHERE foreign_id = ? AND status = ?",
		upstream.foreignBookID, StatusPending,
	).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	adminID := createTestAdmin(t, service)
	if _, err := service.ApproveRequest(adminID, requestID, nil); !errors.Is(err, ErrBookOutcomePending) {
		t.Fatalf("ApproveRequest error=%v, want durable outcome pending", err)
	}
	var phase string
	var linkedRequestID int64
	if err := service.db.QueryRow(
		"SELECT phase, request_id FROM book_request_jobs WHERE instance_id = ? AND foreign_id = ?",
		instanceID, upstream.foreignBookID,
	).Scan(&phase, &linkedRequestID); err != nil {
		t.Fatal(err)
	}
	if phase != "search_inflight" || linkedRequestID != requestID {
		t.Fatalf("guarded approval phase=%q request=%d", phase, linkedRequestID)
	}
	upstream.mu.Lock()
	upstream.dropSearchResponse = false
	upstream.activeSearch = true
	upstream.mu.Unlock()
	restarted, cancel := restartedBookWorker(service)
	defer cancel()
	waitForBookJobCount(t, restarted, 0)
	upstream.mu.Lock()
	searchCalls := upstream.searchCalls
	upstream.mu.Unlock()
	if searchCalls != 1 {
		t.Fatalf("lost approval response queued %d searches, want exactly one", searchCalls)
	}
	var status string
	if err := restarted.db.QueryRow("SELECT status FROM request_log WHERE id = ?", requestID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != StatusRequested {
		t.Fatalf("resumed approval status=%q, want requested", status)
	}
}

func TestActiveBookApprovalRejectsSecondDecisionAndDenial(t *testing.T) {
	upstream := newVerifiedBookUpstream("Approval Decision Guard", "approval-decision-guard")
	service, userID := newVerifiedMutationService(t, upstream)
	job, requestID, adminID, _ := prepareApprovalWorkerTestJob(t, service, userID, upstream, BookFormatAudiobook)

	if _, err := service.ApproveRequest(adminID, requestID, nil); !errors.Is(err, ErrBookOutcomePending) {
		t.Fatalf("second approval error=%v, want outcome pending", err)
	}
	if err := service.DenyRequest(adminID, requestID, "changed mind"); !errors.Is(err, ErrBookOutcomePending) {
		t.Fatalf("denial error=%v, want outcome pending", err)
	}
	var requestStatus, jobState string
	if err := service.db.QueryRow("SELECT status FROM request_log WHERE id = ?", requestID).Scan(&requestStatus); err != nil {
		t.Fatal(err)
	}
	if err := service.db.QueryRow("SELECT state FROM book_request_jobs WHERE id = ?", job.ID).Scan(&jobState); err != nil {
		t.Fatal(err)
	}
	if requestStatus != StatusPending || jobState != "running" {
		t.Fatalf("guarded decision request=%q job=%q", requestStatus, jobState)
	}
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if len(upstream.seedBodies) != 0 || upstream.searchCalls != 0 {
		t.Fatalf("duplicate decision mutated Chaptarr: seed=%d search=%d", len(upstream.seedBodies), upstream.searchCalls)
	}
}

func TestTerminalBookApprovalRetriesSameLinkedJob(t *testing.T) {
	upstream := newVerifiedBookUpstream("Approval Retry", "approval-retry")
	upstream.emptyQualityProfiles = true
	service, userID := newVerifiedMutationService(t, upstream)
	_, instanceID, err := service.resolveChaptarr(userID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.createPending(&resolvedRequest{
		userID: userID, instanceID: instanceID, foreignID: upstream.foreignBookID,
		title: upstream.title, mediaType: "book", bookFormat: BookFormatEbook,
	}); err != nil {
		t.Fatal(err)
	}
	var requestID int64
	if err := service.db.QueryRow("SELECT id FROM request_log WHERE foreign_id = ? AND status = ?", upstream.foreignBookID, StatusPending).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	adminID := createTestAdmin(t, service)
	if _, err := service.ApproveRequest(adminID, requestID, nil); !errors.Is(err, ErrBookConfigurationInvalid) {
		t.Fatalf("first approval error=%v, want terminal configuration error", err)
	}
	var jobID int64
	var jobState, requestStatus string
	if err := service.db.QueryRow("SELECT id, state FROM book_request_jobs WHERE request_id = ?", requestID).Scan(&jobID, &jobState); err != nil {
		t.Fatal(err)
	}
	if err := service.db.QueryRow("SELECT status FROM request_log WHERE id = ?", requestID).Scan(&requestStatus); err != nil {
		t.Fatal(err)
	}
	if jobState != "failed" || requestStatus != StatusPending {
		t.Fatalf("terminal approval job=%q request=%q", jobState, requestStatus)
	}
	upstream.mu.Lock()
	upstream.emptyQualityProfiles = false
	upstream.mu.Unlock()
	response, err := service.ApproveRequest(adminID, requestID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.BookFormats[BookFormatEbook] != StatusRequested {
		t.Fatalf("retry response=%#v", response)
	}
	var remaining int
	if err := service.db.QueryRow("SELECT COUNT(*) FROM book_request_jobs WHERE id = ?", jobID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("successful retry left linked job %d", jobID)
	}
}

func TestMultipleTerminalBookFailuresAggregateWhileOneNewActionOwnsWork(t *testing.T) {
	upstream := newVerifiedBookUpstream("Aggregate Failures", "aggregate-failures")
	service, firstUserID := newVerifiedMutationService(t, upstream)
	firstJob, _, instanceID := prepareWorkerTestJob(t, service, firstUserID, upstream, BookFormatEbook)
	service.deferDirectBookJob(firstJob.ID, ErrBookEditionUnavailable)
	result, err := service.db.Exec("INSERT INTO users (username, password_hash, role) VALUES ('second-reader', '', 'user')")
	if err != nil {
		t.Fatal(err)
	}
	secondUserID, _ := result.LastInsertId()
	secondRequest := &resolvedRequest{
		userID: secondUserID, instanceID: instanceID, foreignID: upstream.foreignBookID,
		title: upstream.title, mediaType: "book", bookFormat: BookFormatAudiobook,
	}
	secondJob, _, active, err := service.prepareDirectBookJob(secondRequest)
	if err != nil || active {
		t.Fatalf("second action job=%#v active=%v err=%v", secondJob, active, err)
	}
	service.deferDirectBookJob(secondJob.ID, ErrBookSearchRejected)
	_, fingerprint, err := service.registry.GetFreshChaptarrClient(instanceID)
	if err != nil {
		t.Fatal(err)
	}
	failedFormat, code, _, _, err := service.bookRequestFailure(instanceID, upstream.foreignBookID, fingerprint[:], firstUserID)
	if err != nil {
		t.Fatal(err)
	}
	if failedFormat != BookFormatBoth || code == "" {
		t.Fatalf("aggregated failure format=%q code=%q, want both with a safe code", failedFormat, code)
	}
	thirdRequest := &resolvedRequest{
		userID: firstUserID, instanceID: instanceID, foreignID: upstream.foreignBookID,
		title: upstream.title, mediaType: "book", bookFormat: BookFormatBoth,
	}
	thirdJob, _, active, err := service.prepareDirectBookJob(thirdRequest)
	if err != nil || active || thirdJob.ID == 0 {
		t.Fatalf("new active action job=%#v active=%v err=%v", thirdJob, active, err)
	}
	if _, _, active, err := service.prepareDirectBookJob(secondRequest); err != nil || !active {
		t.Fatalf("parallel active owner active=%v err=%v", active, err)
	}
}

func TestBookRequestWorkerResumesVisibleSeedAfterRestartWithoutDuplicatePost(t *testing.T) {
	upstream := newVerifiedBookUpstream("Restart Seed", "restart-seed")
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, _ := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
	if err := service.setBookJobPhase(job.ID, "seed_inflight", BookFormatEbook, 0, upstream.foreignAuthorID, upstream.authorName, 0, false); err != nil {
		t.Fatal(err)
	}
	upstream.addExisting(BookFormatEbook, false)

	restarted, cancel := restartedBookWorker(service)
	defer cancel()
	waitForBookJobCount(t, restarted, 0)

	upstream.mu.Lock()
	seedCalls := len(upstream.seedBodies)
	monitorCalls := len(upstream.monitorIDs)
	searchCalls := upstream.searchCalls
	upstream.mu.Unlock()
	if seedCalls != 0 || monitorCalls != 1 || searchCalls != 1 {
		t.Fatalf("resume seed=%d monitor=%d search=%d, want 0/1/1", seedCalls, monitorCalls, searchCalls)
	}
	var format, status string
	if err := restarted.db.QueryRow(
		"SELECT book_format, status FROM request_log WHERE user_id = ? AND foreign_id = ?",
		userID, upstream.foreignBookID,
	).Scan(&format, &status); err != nil {
		t.Fatal(err)
	}
	if format != BookFormatEbook || status != StatusRequested {
		t.Fatalf("history = %s/%s, want ebook/requested", format, status)
	}
}

func TestBookRequestStatusOnlyObservesDurableJob(t *testing.T) {
	upstream := newVerifiedBookUpstream("Read Only Job", "read-only-job")
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, instanceID := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
	if err := service.setBookJobPhase(job.ID, "seed_inflight", BookFormatEbook, 0, upstream.foreignAuthorID, upstream.authorName, 0, false); err != nil {
		t.Fatal(err)
	}
	upstream.addExisting(BookFormatEbook, false)

	for i := 0; i < 3; i++ {
		status, err := service.GetUserBookStatusForInstance(userID, upstream.foreignBookID, instanceID)
		if err != nil {
			t.Fatal(err)
		}
		if status.StatusKnown == nil || *status.StatusKnown || status.UnknownReason != "outcome_pending" {
			t.Fatalf("status = %#v, want read-only outcome_pending", status)
		}
	}
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if len(upstream.bookPutIDs) != 0 || len(upstream.monitorIDs) != 0 || upstream.searchCalls != 0 {
		t.Fatalf("status read mutated Chaptarr: put=%v monitor=%v search=%d", upstream.bookPutIDs, upstream.monitorIDs, upstream.searchCalls)
	}
	var state, phase string
	var attempts int
	if err := service.db.QueryRow("SELECT state, phase, attempt_count FROM book_request_jobs WHERE id = ?", job.ID).Scan(&state, &phase, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != "running" || phase != "seed_inflight" || attempts != 0 {
		t.Fatalf("status read advanced durable job to %s/%s attempts=%d", state, phase, attempts)
	}
}

func TestTerminalBookJobFailureStaysVisibleAndRetryable(t *testing.T) {
	upstream := newVerifiedBookUpstream("Terminal Job", "terminal-job")
	service, userID := newVerifiedMutationService(t, upstream)
	job, resolved, instanceID := prepareWorkerTestJob(t, service, userID, upstream, BookFormatAudiobook)
	if retained := service.deferDirectBookJob(job.ID, ErrBookEditionUnavailable); retained {
		t.Fatal("terminal book failure was scheduled for automatic retry")
	}

	var state, code string
	if err := service.db.QueryRow(
		"SELECT state, last_error_code FROM book_request_jobs WHERE id = ?",
		job.ID,
	).Scan(&state, &code); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || code != "book_edition_unavailable" {
		t.Fatalf("terminal job = %s/%s, want failed/book_edition_unavailable", state, code)
	}
	active, err := service.hasActiveBookRequestJob(instanceID, upstream.foreignBookID)
	if err != nil || active {
		t.Fatalf("failed job active = %v err=%v", active, err)
	}
	status, err := service.GetUserBookStatusForInstance(userID, upstream.foreignBookID, instanceID)
	if err != nil {
		t.Fatal(err)
	}
	if status.StatusKnown == nil || *status.StatusKnown ||
		status.UnknownReason != "request_failed" ||
		status.FailureCode != "book_edition_unavailable" ||
		status.BookFormats[BookFormatAudiobook] != StatusUnavailable {
		t.Fatalf("failed job status = %#v", status)
	}

	retry, _, alreadyActive, err := service.prepareDirectBookJob(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if alreadyActive || retry.ID != job.ID {
		t.Fatalf("retry job = %#v active=%v, want the failed owner reset atomically", retry, alreadyActive)
	}
	var retryState, retryCode, retryPhase string
	var retryAttempts int
	if err := service.db.QueryRow(
		`SELECT state, last_error_code, phase, attempt_count
		 FROM book_request_jobs WHERE id = ?`, job.ID,
	).Scan(&retryState, &retryCode, &retryPhase, &retryAttempts); err != nil {
		t.Fatal(err)
	}
	if retryState != "running" || retryCode != "" || retryPhase != "queued" || retryAttempts != 0 {
		t.Fatalf("reset retry = state %q code %q phase %q attempts %d", retryState, retryCode, retryPhase, retryAttempts)
	}
}

func TestTerminalBookJobFailureIsVisibleAcrossAuthorizedUsers(t *testing.T) {
	upstream := newVerifiedBookUpstream("Shared Terminal Job", "shared-terminal-job")
	service, ownerID := newVerifiedMutationService(t, upstream)
	job, _, instanceID := prepareWorkerTestJob(t, service, ownerID, upstream, BookFormatAudiobook)
	if retained := service.deferDirectBookJob(job.ID, ErrBookEditionUnavailable); retained {
		t.Fatal("terminal book failure was scheduled for automatic retry")
	}

	result, err := service.db.Exec(
		"INSERT INTO users (username, password_hash, role) VALUES ('terminal-job-reader', '', 'user')",
	)
	if err != nil {
		t.Fatal(err)
	}
	secondUserID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(
		"INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (?, 'chaptarr', ?)",
		secondUserID, instanceID,
	); err != nil {
		t.Fatal(err)
	}

	status, err := service.GetUserBookStatusForInstance(secondUserID, upstream.foreignBookID, instanceID)
	if err != nil {
		t.Fatal(err)
	}
	if status.StatusKnown == nil || *status.StatusKnown ||
		status.UnknownReason != "request_failed" ||
		status.FailureCode != "book_edition_unavailable" ||
		status.BookFormats[BookFormatAudiobook] != StatusUnavailable {
		t.Fatalf("shared failed job status = %#v", status)
	}
	var storedOwnerID int64
	if err := service.db.QueryRow(
		"SELECT user_id FROM book_request_jobs WHERE id = ?", job.ID,
	).Scan(&storedOwnerID); err != nil {
		t.Fatal(err)
	}
	if storedOwnerID != ownerID {
		t.Fatalf("status read transferred failed owner to %d, want %d", storedOwnerID, ownerID)
	}
}

func TestTerminalBookJobFailureHealsOnlyWhenFailedFormatIsCovered(t *testing.T) {
	upstream := newVerifiedBookUpstream("Format Scoped Failure", "format-scoped-failure")
	ebookID := upstream.addExisting(BookFormatEbook, false)
	upstream.mu.Lock()
	upstream.rows[ebookID].book.Statistics.BookFileCount = 1
	upstream.mu.Unlock()
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, instanceID := prepareWorkerTestJob(t, service, userID, upstream, BookFormatAudiobook)
	if retained := service.deferDirectBookJob(job.ID, ErrBookEditionUnavailable); retained {
		t.Fatal("terminal book failure was scheduled for automatic retry")
	}

	status, err := service.GetUserBookStatusForInstance(userID, upstream.foreignBookID, instanceID)
	if err != nil {
		t.Fatal(err)
	}
	if status.StatusKnown == nil || *status.StatusKnown ||
		status.UnknownReason != "request_failed" ||
		status.FailureCode != "book_edition_unavailable" ||
		status.BookFormats[BookFormatEbook] != StatusAvailable ||
		status.BookFormats[BookFormatAudiobook] != StatusUnavailable {
		t.Fatalf("sibling-covered failed job status = %#v", status)
	}

	audiobookID := upstream.addExisting(BookFormatAudiobook, false)
	upstream.mu.Lock()
	upstream.rows[audiobookID].book.Statistics.BookFileCount = 1
	upstream.mu.Unlock()
	service.invalidateBookCaches(instanceID)

	healed, err := service.GetUserBookStatusForInstance(userID, upstream.foreignBookID, instanceID)
	if err != nil {
		t.Fatal(err)
	}
	if healed.StatusKnown != nil || healed.UnknownReason != "" || healed.FailureCode != "" ||
		healed.BookFormats[BookFormatEbook] != StatusAvailable ||
		healed.BookFormats[BookFormatAudiobook] != StatusAvailable {
		t.Fatalf("failed-format-covered status = %#v", healed)
	}
}

func TestTerminalBookJobFailureSurvivesUnreadableLiveStatusAndLaterHeals(t *testing.T) {
	upstream := newVerifiedBookUpstream("Unreadable Terminal Job", "unreadable-terminal-job")
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, instanceID := prepareWorkerTestJob(t, service, userID, upstream, BookFormatAudiobook)
	authFailure := &chaptarr.HTTPStatusError{
		Method:     http.MethodGet,
		Path:       "/api/v1/book",
		StatusCode: http.StatusUnauthorized,
	}
	if retained := service.deferDirectBookJob(job.ID, authFailure); retained {
		t.Fatal("terminal authentication failure was scheduled for automatic retry")
	}
	upstream.mu.Lock()
	upstream.bookListStatus = http.StatusUnauthorized
	upstream.mu.Unlock()

	status, err := service.GetUserBookStatusForInstance(userID, upstream.foreignBookID, instanceID)
	if err != nil {
		t.Fatalf("persisted terminal failure was replaced by live read error: %v", err)
	}
	if status.StatusKnown == nil || *status.StatusKnown ||
		status.UnknownReason != "request_failed" ||
		status.FailureCode != "book_connection_invalid" ||
		status.BookFormats[BookFormatAudiobook] != StatusUnavailable {
		t.Fatalf("unreadable live status = %#v", status)
	}

	upstream.mu.Lock()
	upstream.bookListStatus = 0
	upstream.mu.Unlock()
	audiobookID := upstream.addExisting(BookFormatAudiobook, false)
	upstream.mu.Lock()
	upstream.rows[audiobookID].book.Statistics.BookFileCount = 1
	upstream.mu.Unlock()
	service.invalidateBookCaches(instanceID)

	healed, err := service.GetUserBookStatusForInstance(userID, upstream.foreignBookID, instanceID)
	if err != nil {
		t.Fatal(err)
	}
	if healed.StatusKnown != nil || healed.UnknownReason != "" || healed.FailureCode != "" ||
		healed.BookFormats[BookFormatAudiobook] != StatusAvailable {
		t.Fatalf("healed terminal status = %#v", healed)
	}
}

func TestTerminalBothBookJobPreservesCompletedFormatWhileLiveStatusIsUnreadable(t *testing.T) {
	upstream := newVerifiedBookUpstream("Partial Both Terminal Job", "partial-both-terminal-job")
	service, userID := newVerifiedMutationService(t, upstream)
	job, resolved, instanceID := prepareWorkerTestJob(t, service, userID, upstream, BookFormatBoth)
	if err := service.completeBookJobFormat(resolved, BookFormatEbook, StatusRequested); err != nil {
		t.Fatal(err)
	}
	authFailure := &chaptarr.HTTPStatusError{
		Method:     http.MethodGet,
		Path:       "/api/v1/book",
		StatusCode: http.StatusUnauthorized,
	}
	if retained := service.deferDirectBookJob(job.ID, authFailure); retained {
		t.Fatal("terminal authentication failure was scheduled for automatic retry")
	}
	upstream.mu.Lock()
	upstream.bookListStatus = http.StatusUnauthorized
	upstream.mu.Unlock()

	status, err := service.GetUserBookStatusForInstance(userID, upstream.foreignBookID, instanceID)
	if err != nil {
		t.Fatalf("persisted partial terminal failure was replaced by live read error: %v", err)
	}
	if status.StatusKnown == nil || *status.StatusKnown ||
		status.UnknownReason != "request_failed" ||
		status.FailureCode != "book_connection_invalid" ||
		status.BookFormats[BookFormatEbook] != StatusRequested ||
		status.BookFormats[BookFormatAudiobook] != StatusUnavailable {
		t.Fatalf("partial both status = %#v", status)
	}

	upstream.mu.Lock()
	upstream.bookListStatus = 0
	upstream.mu.Unlock()
	audiobookID := upstream.addExisting(BookFormatAudiobook, false)
	upstream.mu.Lock()
	upstream.rows[audiobookID].book.Statistics.BookFileCount = 1
	upstream.mu.Unlock()
	service.invalidateBookCaches(instanceID)

	healed, err := service.GetUserBookStatusForInstance(userID, upstream.foreignBookID, instanceID)
	if err != nil {
		t.Fatal(err)
	}
	if healed.StatusKnown != nil || healed.UnknownReason != "" || healed.FailureCode != "" ||
		healed.BookFormats[BookFormatEbook] != StatusRequested ||
		healed.BookFormats[BookFormatAudiobook] != StatusAvailable {
		t.Fatalf("healed partial both status = %#v", healed)
	}
}

func TestApprovedBookDecisionSupersedesStaleFailedDirectJob(t *testing.T) {
	upstream := newVerifiedBookUpstream("Approved Supersession", "approved-supersession")
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, instanceID := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
	if retained := service.deferDirectBookJob(job.ID, ErrBookEditionUnavailable); retained {
		t.Fatal("terminal direct failure was scheduled for automatic retry")
	}
	_, err := service.createPending(&resolvedRequest{
		userID: userID, instanceID: instanceID, foreignID: upstream.foreignBookID,
		title: upstream.title, mediaType: "book", bookFormat: BookFormatEbook,
	})
	if err != nil {
		t.Fatal(err)
	}
	var requestID int64
	if err := service.db.QueryRow(
		"SELECT id FROM request_log WHERE foreign_id = ? AND instance_id = ? AND status = ?", upstream.foreignBookID, instanceID, StatusPending,
	).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	adminID := createTestAdmin(t, service)
	approved, err := service.ApproveRequest(adminID, requestID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if approved.BookFormats[BookFormatEbook] != StatusRequested {
		t.Fatalf("approved response = %#v", approved)
	}
	var jobs int
	if err := service.db.QueryRow("SELECT COUNT(*) FROM book_request_jobs WHERE id = ?", job.ID).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 {
		t.Fatalf("approval left %d stale failed jobs", jobs)
	}
	status, err := service.GetUserBookStatusForInstance(userID, upstream.foreignBookID, instanceID)
	if err != nil {
		t.Fatal(err)
	}
	if status.UnknownReason != "" || status.FailureCode != "" || status.BookFormats[BookFormatEbook] != StatusRequested {
		t.Fatalf("approved status was overwritten by stale failure: %#v", status)
	}
}

func TestDeniedBookDecisionDoesNotEraseStaleFailedDirectJob(t *testing.T) {
	upstream := newVerifiedBookUpstream("Denied Supersession", "denied-supersession")
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, instanceID := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
	if retained := service.deferDirectBookJob(job.ID, ErrBookEditionUnavailable); retained {
		t.Fatal("terminal direct failure was scheduled for automatic retry")
	}
	_, err := service.createPending(&resolvedRequest{
		userID: userID, instanceID: instanceID, foreignID: upstream.foreignBookID,
		title: upstream.title, mediaType: "book", bookFormat: BookFormatEbook,
	})
	if err != nil {
		t.Fatal(err)
	}
	var requestID int64
	if err := service.db.QueryRow(
		"SELECT id FROM request_log WHERE foreign_id = ? AND instance_id = ? AND status = ?", upstream.foreignBookID, instanceID, StatusPending,
	).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	adminID := createTestAdmin(t, service)
	if err := service.DenyRequest(adminID, requestID, "not now"); err != nil {
		t.Fatal(err)
	}
	var jobs int
	if err := service.db.QueryRow("SELECT COUNT(*) FROM book_request_jobs WHERE id = ?", job.ID).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 {
		t.Fatalf("denial changed global failed owner count to %d, want 1", jobs)
	}
	status, err := service.GetUserBookStatusForInstance(userID, upstream.foreignBookID, instanceID)
	if err != nil {
		t.Fatal(err)
	}
	if status.UnknownReason != "" || status.FailureCode != "" ||
		status.Status != StatusDenied || status.BookFormats[BookFormatEbook] != StatusDenied {
		t.Fatalf("denied status was overwritten by stale failure: %#v", status)
	}
}

func TestCrossUserDenialKeepsGlobalFailureButShowsDenialToDecidedUser(t *testing.T) {
	upstream := newVerifiedBookUpstream("Cross User Denial", "cross-user-denial")
	service, ownerID := newVerifiedMutationService(t, upstream)
	job, _, instanceID := prepareWorkerTestJob(t, service, ownerID, upstream, BookFormatEbook)
	if retained := service.deferDirectBookJob(job.ID, ErrBookEditionUnavailable); retained {
		t.Fatal("terminal direct failure was scheduled for automatic retry")
	}
	result, err := service.db.Exec("INSERT INTO users (username, password_hash, role) VALUES ('cross-user-denial', '', 'user')")
	if err != nil {
		t.Fatal(err)
	}
	deniedUserID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(
		"INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (?, 'chaptarr', ?)", deniedUserID, instanceID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.createPending(&resolvedRequest{
		userID: deniedUserID, instanceID: instanceID, foreignID: upstream.foreignBookID,
		title: upstream.title, mediaType: "book", bookFormat: BookFormatEbook,
	}); err != nil {
		t.Fatal(err)
	}
	var requestID int64
	if err := service.db.QueryRow(
		"SELECT id FROM request_log WHERE user_id = ? AND foreign_id = ? AND status = ?", deniedUserID, upstream.foreignBookID, StatusPending,
	).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	adminID := createTestAdmin(t, service)
	if err := service.DenyRequest(adminID, requestID, "not now"); err != nil {
		t.Fatal(err)
	}
	var failedOwnerID int64
	if err := service.db.QueryRow("SELECT user_id FROM book_request_jobs WHERE id = ?", job.ID).Scan(&failedOwnerID); err != nil {
		t.Fatal(err)
	}
	if failedOwnerID != ownerID {
		t.Fatalf("denial transferred global failed owner to %d, want %d", failedOwnerID, ownerID)
	}
	deniedStatus, err := service.GetUserBookStatusForInstance(deniedUserID, upstream.foreignBookID, instanceID)
	if err != nil {
		t.Fatal(err)
	}
	if deniedStatus.Status != StatusDenied || deniedStatus.UnknownReason != "" || deniedStatus.FailureCode != "" {
		t.Fatalf("cross-user denied status = %#v", deniedStatus)
	}
	ownerStatus, err := service.GetUserBookStatusForInstance(ownerID, upstream.foreignBookID, instanceID)
	if err != nil {
		t.Fatal(err)
	}
	if ownerStatus.UnknownReason != "request_failed" || ownerStatus.FailureCode != "book_edition_unavailable" {
		t.Fatalf("original owner's failure was hidden: %#v", ownerStatus)
	}
}

func TestVerifiedBookSupersessionMaterializesCheckpointAndKeepsUnrelatedFailure(t *testing.T) {
	upstream := newVerifiedBookUpstream("Partial Supersession", "partial-supersession")
	service, userID := newVerifiedMutationService(t, upstream)
	job, resolved, instanceID := prepareWorkerTestJob(t, service, userID, upstream, BookFormatBoth)
	if err := service.completeBookJobFormat(resolved, BookFormatEbook, StatusRequested); err != nil {
		t.Fatal(err)
	}
	if retained := service.deferDirectBookJob(job.ID, ErrBookEditionUnavailable); retained {
		t.Fatal("terminal direct failure was scheduled for automatic retry")
	}
	// A verified successful eBook decision materializes the checkpoint, but
	// must leave the unrelated failed audiobook owner intact.
	tx, err := service.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := supersedeFailedBookJobTx(tx, instanceID, upstream.foreignBookID, BookFormatEbook); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var failedFormat, ebookStatus, audiobookStatus string
	if err := service.db.QueryRow(
		"SELECT book_format, ebook_status, audiobook_status FROM book_request_jobs WHERE id = ?", job.ID,
	).Scan(&failedFormat, &ebookStatus, &audiobookStatus); err != nil {
		t.Fatal(err)
	}
	if failedFormat != BookFormatAudiobook || ebookStatus != "" || audiobookStatus != "" {
		t.Fatalf("narrowed failure format=%q ebook=%q audiobook=%q", failedFormat, ebookStatus, audiobookStatus)
	}
	var checkpointRows int
	if err := service.db.QueryRow(
		`SELECT COUNT(*) FROM request_log
		 WHERE user_id = ? AND foreign_id = ? AND instance_id = ?
		   AND book_format = ? AND status = ?`,
		userID, upstream.foreignBookID, instanceID, BookFormatEbook, StatusRequested,
	).Scan(&checkpointRows); err != nil {
		t.Fatal(err)
	}
	if checkpointRows != 1 {
		t.Fatalf("materialized checkpoint rows=%d, want 1", checkpointRows)
	}
}

func TestTerminalBookJobRetryPreservesFailureWhenFreshInstanceBindingFails(t *testing.T) {
	upstream := newVerifiedBookUpstream("Retry Binding Failure", "retry-binding-failure")
	service, userID := newVerifiedMutationService(t, upstream)
	job, resolved, instanceID := prepareWorkerTestJob(t, service, userID, upstream, BookFormatAudiobook)
	if retained := service.deferDirectBookJob(job.ID, ErrBookEditionUnavailable); retained {
		t.Fatal("terminal book failure was scheduled for automatic retry")
	}
	if _, err := service.db.Exec(
		"UPDATE service_instances SET service_type = 'radarr' WHERE id = ?", instanceID,
	); err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := service.prepareDirectBookJob(resolved); !errors.Is(err, ErrChaptarrInstanceInvalid) {
		t.Fatalf("retry error = %v, want ErrChaptarrInstanceInvalid", err)
	}
	var state, code, format string
	var storedUserID int64
	if err := service.db.QueryRow(
		`SELECT state, last_error_code, book_format, user_id
		 FROM book_request_jobs WHERE id = ?`, job.ID,
	).Scan(&state, &code, &format, &storedUserID); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || code != "book_edition_unavailable" ||
		format != BookFormatAudiobook || storedUserID != userID {
		t.Fatalf("preserved failed job = state %q code %q format %q user %d", state, code, format, storedUserID)
	}
}

func TestFailedBothBookRetryPreservesCompletedSiblingAcrossRestart(t *testing.T) {
	upstream := newVerifiedBookUpstream("Failed Both Retry", "failed-both-retry")
	ebookID := upstream.addExisting(BookFormatEbook, true)
	audiobookID := upstream.addExisting(BookFormatAudiobook, false)
	upstream.mu.Lock()
	upstream.author.EbookMonitorFuture = true
	upstream.searchCalls = 1
	upstream.searchBookIDs = []int{ebookID}
	upstream.mu.Unlock()
	service, userID := newVerifiedMutationService(t, upstream)
	job, resolved, instanceID := prepareWorkerTestJob(t, service, userID, upstream, BookFormatBoth)
	if err := service.completeBookJobFormat(resolved, BookFormatEbook, StatusRequested); err != nil {
		t.Fatal(err)
	}
	if retained := service.deferDirectBookJob(job.ID, ErrBookEditionUnavailable); retained {
		t.Fatal("terminal second-format failure was scheduled for automatic retry")
	}
	retryRequest := &resolvedRequest{
		userID: userID, instanceID: instanceID, foreignID: upstream.foreignBookID,
		title: upstream.title, mediaType: "book", bookFormat: BookFormatAudiobook,
	}
	retry, _, alreadyActive, err := service.prepareDirectBookJob(retryRequest)
	if err != nil {
		t.Fatal(err)
	}
	if alreadyActive || retry.ID != job.ID || retry.EbookStatus != StatusRequested || retry.AudiobookStatus != "" {
		t.Fatalf("retry = %#v active=%v, want preserved ebook-only checkpoint", retry, alreadyActive)
	}

	restarted, cancel := restartedBookWorker(service)
	defer cancel()
	waitForBookJobCount(t, restarted, 0)
	upstream.mu.Lock()
	searchCalls := upstream.searchCalls
	searchedIDs := append([]int(nil), upstream.searchBookIDs...)
	upstream.mu.Unlock()
	if searchCalls != 2 || len(searchedIDs) != 1 || searchedIDs[0] != audiobookID {
		t.Fatalf("restart searches=%d latest_ids=%v, want original ebook %d retained and only audiobook %d retried", searchCalls, searchedIDs, ebookID, audiobookID)
	}
	var ebookRows, audiobookRows int
	if err := restarted.db.QueryRow(
		`SELECT
		 SUM(CASE WHEN book_format = ? THEN 1 ELSE 0 END),
		 SUM(CASE WHEN book_format = ? THEN 1 ELSE 0 END)
		 FROM request_log WHERE user_id = ? AND foreign_id = ?`,
		BookFormatEbook, BookFormatAudiobook, userID, upstream.foreignBookID,
	).Scan(&ebookRows, &audiobookRows); err != nil {
		t.Fatal(err)
	}
	if ebookRows != 1 || audiobookRows != 1 {
		t.Fatalf("completed retry history ebook=%d audiobook=%d, want one durable row each", ebookRows, audiobookRows)
	}

	// Lose every process-local acknowledgement and command signal. Durable
	// requested history plus the exact still-monitored row must keep the sibling
	// requested and suppress a second eBook search.
	afterRestart := NewService(service.db, service.registry, service.bridge, service.notifier)
	afterRestart.bookMutationTimeout = 250 * time.Millisecond
	afterRestart.bookSettleInterval = time.Millisecond
	status, err := afterRestart.GetUserBookStatusForInstance(userID, upstream.foreignBookID, instanceID)
	if err != nil {
		t.Fatal(err)
	}
	if status.BookFormats[BookFormatEbook] != StatusRequested {
		t.Fatalf("post-restart sibling status = %#v, want durable requested", status)
	}
	if _, err := afterRestart.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title,
		BookFormat: BookFormatEbook,
	}); err != nil {
		t.Fatalf("post-restart sibling request: %v", err)
	}
	upstream.mu.Lock()
	postRestartSearches := upstream.searchCalls
	upstream.mu.Unlock()
	if postRestartSearches != 2 {
		t.Fatalf("post-restart sibling queued duplicate search; calls=%d", postRestartSearches)
	}
}

func TestFailedBookRetryClearsEndpointBoundCheckpointsAfterRepoint(t *testing.T) {
	upstream := newVerifiedBookUpstream("Failed Repoint Retry", "failed-repoint-retry")
	service, userID := newVerifiedMutationService(t, upstream)
	job, resolved, instanceID := prepareWorkerTestJob(t, service, userID, upstream, BookFormatBoth)
	if err := service.completeBookJobFormat(resolved, BookFormatEbook, StatusRequested); err != nil {
		t.Fatal(err)
	}
	if retained := service.deferDirectBookJob(job.ID, ErrBookEditionUnavailable); retained {
		t.Fatal("terminal second-format failure was scheduled for automatic retry")
	}
	if _, err := service.db.Exec("UPDATE book_request_jobs SET settings_fingerprint = zeroblob(32) WHERE id = ?", job.ID); err != nil {
		t.Fatal(err)
	}
	retry, _, alreadyActive, err := service.prepareDirectBookJob(&resolvedRequest{
		userID: userID, instanceID: instanceID, foreignID: upstream.foreignBookID,
		title: upstream.title, mediaType: "book", bookFormat: BookFormatAudiobook,
	})
	if err != nil {
		t.Fatal(err)
	}
	if alreadyActive || retry.EbookStatus != "" || retry.AudiobookStatus != "" {
		t.Fatalf("repointed retry = %#v active=%v, want cleared endpoint checkpoints", retry, alreadyActive)
	}
}

func TestBookStatusIgnoresFailedCheckpointFromDifferentInstanceBinding(t *testing.T) {
	upstream := newVerifiedBookUpstream("Stale Failed Binding", "stale-failed-binding")
	upstream.addExisting(BookFormatEbook, true)
	service, userID := newVerifiedMutationService(t, upstream)
	job, resolved, instanceID := prepareWorkerTestJob(t, service, userID, upstream, BookFormatBoth)
	if err := service.completeBookJobFormat(resolved, BookFormatEbook, StatusRequested); err != nil {
		t.Fatal(err)
	}
	if retained := service.deferDirectBookJob(job.ID, ErrBookEditionUnavailable); retained {
		t.Fatal("terminal second-format failure was scheduled for automatic retry")
	}
	if _, err := service.db.Exec("UPDATE book_request_jobs SET settings_fingerprint = zeroblob(32) WHERE id = ?", job.ID); err != nil {
		t.Fatal(err)
	}
	status, err := service.GetUserBookStatusForInstance(userID, upstream.foreignBookID, instanceID)
	if err != nil {
		t.Fatal(err)
	}
	if status.UnknownReason != "" || status.FailureCode != "" || status.BookFormats[BookFormatEbook] == StatusRequested {
		t.Fatalf("stale binding leaked failed checkpoint into status: %#v", status)
	}
}

func TestBookRequestedHistoryCannotSuppressSearchAcrossInstanceBinding(t *testing.T) {
	upstream := newVerifiedBookUpstream("Stale History Binding", "stale-history-binding")
	bookID := upstream.addExisting(BookFormatEbook, true)
	upstream.mu.Lock()
	upstream.author.EbookMonitorFuture = true
	upstream.mu.Unlock()
	service, userID := newVerifiedMutationService(t, upstream)
	job, resolved, instanceID := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
	resolved.bookFormats = map[string]string{BookFormatEbook: StatusRequested}
	if err := service.completeDirectBookJob(job.ID, resolved, upstream.title); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(
		"UPDATE request_log SET book_settings_fingerprint = zeroblob(32) WHERE foreign_id = ?", upstream.foreignBookID,
	); err != nil {
		t.Fatal(err)
	}
	afterRepoint := NewService(service.db, service.registry, service.bridge, service.notifier)
	afterRepoint.bookMutationTimeout = 250 * time.Millisecond
	afterRepoint.bookSettleInterval = time.Millisecond
	status, err := afterRepoint.GetUserBookStatusForInstance(userID, upstream.foreignBookID, instanceID)
	if err != nil {
		t.Fatal(err)
	}
	if status.BookFormats[BookFormatEbook] == StatusRequested {
		t.Fatalf("stale requested history remained authoritative: %#v", status)
	}
	if _, err := afterRepoint.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title,
		BookFormat: BookFormatEbook,
	}); err != nil {
		t.Fatal(err)
	}
	upstream.mu.Lock()
	searchCalls := upstream.searchCalls
	searchedIDs := append([]int(nil), upstream.searchBookIDs...)
	upstream.mu.Unlock()
	if searchCalls != 1 || len(searchedIDs) != 1 || searchedIDs[0] != bookID {
		t.Fatalf("stale binding search calls=%d ids=%v, want fresh search for %d", searchCalls, searchedIDs, bookID)
	}
}

func TestCrossUserFailedRetryKeepsCheckpointWithOriginalOwner(t *testing.T) {
	upstream := newVerifiedBookUpstream("Cross User Retry", "cross-user-retry")
	ebookID := upstream.addExisting(BookFormatEbook, true)
	audiobookID := upstream.addExisting(BookFormatAudiobook, false)
	upstream.mu.Lock()
	upstream.author.EbookMonitorFuture = true
	upstream.searchCalls = 1
	upstream.searchBookIDs = []int{ebookID}
	upstream.mu.Unlock()
	service, ownerID := newVerifiedMutationService(t, upstream)
	job, resolved, instanceID := prepareWorkerTestJob(t, service, ownerID, upstream, BookFormatBoth)
	if err := service.completeBookJobFormat(resolved, BookFormatEbook, StatusRequested); err != nil {
		t.Fatal(err)
	}
	if retained := service.deferDirectBookJob(job.ID, ErrBookEditionUnavailable); retained {
		t.Fatal("terminal second-format failure was scheduled for automatic retry")
	}
	result, err := service.db.Exec("INSERT INTO users (username, password_hash, role) VALUES ('cross-user-retry', '', 'user')")
	if err != nil {
		t.Fatal(err)
	}
	retryUserID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(
		"INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (?, 'chaptarr', ?)",
		retryUserID, instanceID,
	); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingNotifier{}
	service.notifier = recorder
	if _, err := service.CreateMediaRequest(retryUserID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title,
		BookFormat: BookFormatAudiobook,
	}); err != nil {
		t.Fatal(err)
	}
	var ownerEbook, ownerAudio, retryEbook, retryAudio int
	if err := service.db.QueryRow(
		`SELECT
		 SUM(CASE WHEN user_id = ? AND book_format = ? THEN 1 ELSE 0 END),
		 SUM(CASE WHEN user_id = ? AND book_format = ? THEN 1 ELSE 0 END),
		 SUM(CASE WHEN user_id = ? AND book_format = ? THEN 1 ELSE 0 END),
		 SUM(CASE WHEN user_id = ? AND book_format = ? THEN 1 ELSE 0 END)
		 FROM request_log WHERE foreign_id = ? AND instance_id = ?`,
		ownerID, BookFormatEbook, ownerID, BookFormatAudiobook,
		retryUserID, BookFormatEbook, retryUserID, BookFormatAudiobook,
		upstream.foreignBookID, instanceID,
	).Scan(&ownerEbook, &ownerAudio, &retryEbook, &retryAudio); err != nil {
		t.Fatal(err)
	}
	if ownerEbook != 1 || ownerAudio != 0 || retryEbook != 0 || retryAudio != 1 {
		t.Fatalf("history owner=%d/%d retry=%d/%d, want owner ebook and retry user audiobook", ownerEbook, ownerAudio, retryEbook, retryAudio)
	}
	upstream.mu.Lock()
	searchCalls := upstream.searchCalls
	searchedIDs := append([]int(nil), upstream.searchBookIDs...)
	upstream.mu.Unlock()
	if searchCalls != 2 || len(searchedIDs) != 1 || searchedIDs[0] != audiobookID {
		t.Fatalf("cross-user searches=%d ids=%v, want only audiobook %d after retained ebook", searchCalls, searchedIDs, audiobookID)
	}
	ownerNotified, retryNotified := false, false
	for _, event := range recorder.userEvents {
		formats, _ := event.data["book_formats"].(map[string]string)
		if event.userID == ownerID && formats[BookFormatEbook] == StatusRequested {
			ownerNotified = true
		}
		if event.userID == retryUserID && formats[BookFormatAudiobook] == StatusRequested {
			retryNotified = true
		}
	}
	if !ownerNotified || !retryNotified {
		t.Fatalf("checkpoint/retry notifications = %+v", recorder.userEvents)
	}
}

func TestFailedFormatRetryDoesNotTreatSiblingCheckpointAsSuccess(t *testing.T) {
	upstream := newVerifiedBookUpstream("Failed Format Only", "failed-format-only")
	upstream.addExisting(BookFormatEbook, true)
	upstream.physicalOnly[BookFormatAudiobook] = true
	service, userID := newVerifiedMutationService(t, upstream)
	job, resolved, _ := prepareWorkerTestJob(t, service, userID, upstream, BookFormatBoth)
	if err := service.completeBookJobFormat(resolved, BookFormatEbook, StatusRequested); err != nil {
		t.Fatal(err)
	}
	if retained := service.deferDirectBookJob(job.ID, ErrBookEditionUnavailable); retained {
		t.Fatal("terminal second-format failure was scheduled for automatic retry")
	}
	_, err := service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title,
		BookFormat: BookFormatAudiobook,
	})
	if !errors.Is(err, ErrBookEditionUnavailable) {
		t.Fatalf("failed-format retry error = %v, want edition unavailable", err)
	}
	var state, ebookStatus, audiobookStatus string
	if err := service.db.QueryRow(
		"SELECT state, ebook_status, audiobook_status FROM book_request_jobs WHERE id = ?", job.ID,
	).Scan(&state, &ebookStatus, &audiobookStatus); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || ebookStatus != StatusRequested || audiobookStatus != "" {
		t.Fatalf("failed retry state=%q ebook=%q audiobook=%q", state, ebookStatus, audiobookStatus)
	}
}

func TestBookRequestWorkerTrustsOldPersistedSearchAcknowledgement(t *testing.T) {
	upstream := newVerifiedBookUpstream("Old Ack", "old-ack")
	bookID := upstream.addExisting(BookFormatEbook, true)
	upstream.mu.Lock()
	upstream.author.EbookMonitorFuture = true
	upstream.mu.Unlock()
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, _ := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
	if err := service.setBookJobPhase(job.ID, "search_inflight", BookFormatEbook, upstream.author.ID, upstream.foreignAuthorID, upstream.authorName, bookID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec("UPDATE book_request_jobs SET phase_started_at = datetime('now', '-10 minutes') WHERE id = ?", job.ID); err != nil {
		t.Fatal(err)
	}

	restarted, cancel := restartedBookWorker(service)
	defer cancel()
	waitForBookJobCount(t, restarted, 0)
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if upstream.searchCalls != 0 {
		t.Fatalf("old persisted acknowledgement queued %d duplicate searches", upstream.searchCalls)
	}
}

func TestBookRequestWorkerGuardsUncertainSearchAcrossRestart(t *testing.T) {
	upstream := newVerifiedBookUpstream("Search Guard", "search-guard")
	bookID := upstream.addExisting(BookFormatEbook, true)
	upstream.mu.Lock()
	upstream.author.EbookMonitorFuture = true
	upstream.searchCalls = 1 // the pre-crash POST whose response was lost
	upstream.mu.Unlock()
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, _ := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
	if err := service.setBookJobPhase(job.ID, "search_inflight", BookFormatEbook, upstream.author.ID, upstream.foreignAuthorID, upstream.authorName, bookID, false); err != nil {
		t.Fatal(err)
	}

	restarted, cancel := restartedBookWorker(service)
	defer cancel()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		var attempts int
		_ = restarted.db.QueryRow("SELECT attempt_count FROM book_request_jobs WHERE id = ?", job.ID).Scan(&attempts)
		if attempts >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	upstream.mu.Lock()
	guardedCalls := upstream.searchCalls
	upstream.mu.Unlock()
	if guardedCalls != 1 {
		t.Fatalf("restart inside guard queued duplicate search; calls=%d", guardedCalls)
	}

	if _, err := restarted.db.Exec(
		"UPDATE book_request_jobs SET state = 'retry_wait', phase_started_at = datetime('now', '-3 minutes'), next_attempt_at = CURRENT_TIMESTAMP WHERE id = ?",
		job.ID,
	); err != nil {
		t.Fatal(err)
	}
	restarted.wakeBookRequestWorker()
	deadline = time.Now().Add(time.Second)
	var state string
	for time.Now().Before(deadline) {
		var attempts int
		_ = restarted.db.QueryRow(
			"SELECT state, attempt_count FROM book_request_jobs WHERE id = ?", job.ID,
		).Scan(&state, &attempts)
		if state == "outcome_unknown" && attempts >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	upstream.mu.Lock()
	guardExpiredCalls := upstream.searchCalls
	upstream.mu.Unlock()
	if state != "outcome_unknown" || guardExpiredCalls != 1 {
		t.Fatalf("elapsed evidence window state=%s searches=%d, want outcome_unknown/1", state, guardExpiredCalls)
	}

	if _, err := restarted.db.Exec(
		`UPDATE book_request_jobs SET created_at = datetime('now', '-31 minutes'),
		 state = 'retry_wait', next_attempt_at = datetime('now', '+1 day') WHERE id = ?`, job.ID,
	); err != nil {
		t.Fatal(err)
	}
	restarted.wakeBookRequestWorker()
	deadline = time.Now().Add(2 * time.Second)
	var code string
	for time.Now().Before(deadline) {
		_ = restarted.db.QueryRow(
			"SELECT state, last_error_code FROM book_request_jobs WHERE id = ?", job.ID,
		).Scan(&state, &code)
		if state == "failed" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	upstream.mu.Lock()
	finalSearchCalls := upstream.searchCalls
	upstream.mu.Unlock()
	if state != "failed" || code != "book_request_unverified" || finalSearchCalls != 1 {
		t.Fatalf("expired search = %s/%s searches=%d, want failed/book_request_unverified/1", state, code, finalSearchCalls)
	}
}

func TestElapsedInvalidDurableSearchGuardNeverReplays(t *testing.T) {
	for _, mode := range []string{"missing", "identity_changed"} {
		t.Run(mode, func(t *testing.T) {
			upstream := newVerifiedBookUpstream("Invalid Search Guard", "invalid-search-guard")
			bookID := upstream.addExisting(BookFormatEbook, true)
			upstream.mu.Lock()
			upstream.author.EbookMonitorFuture = true
			upstream.searchCalls = 1
			upstream.searchBookIDs = []int{bookID}
			if mode == "missing" {
				delete(upstream.rows, bookID)
			} else {
				upstream.driftIdentityOnRead = true
			}
			upstream.mu.Unlock()
			service, userID := newVerifiedMutationService(t, upstream)
			job, _, instanceID := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
			if err := service.setBookJobPhase(
				job.ID, "search_inflight", BookFormatEbook, upstream.author.ID,
				upstream.foreignAuthorID, upstream.authorName, bookID, false,
			); err != nil {
				t.Fatal(err)
			}
			if _, err := service.db.Exec(
				"UPDATE book_request_jobs SET phase_started_at = datetime('now', '-3 minutes') WHERE id = ?", job.ID,
			); err != nil {
				t.Fatal(err)
			}
			client, _, err := service.resolveChaptarr(userID, instanceID)
			if err != nil {
				t.Fatal(err)
			}
			job, err = service.loadBookRequestJob(job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := service.restoreDurableSearchGuard(context.Background(), client, job); !errors.Is(err, ErrBookSearchUnconfirmed) {
				t.Fatalf("elapsed evidence-window error = %v, want search unconfirmed", err)
			}
			var phase string
			var guardedBookID int
			if err := service.db.QueryRow("SELECT phase, book_id FROM book_request_jobs WHERE id = ?", job.ID).Scan(&phase, &guardedBookID); err != nil {
				t.Fatal(err)
			}
			if phase != "search_inflight" || guardedBookID != bookID {
				t.Fatalf("elapsed guard became phase=%q book=%d", phase, guardedBookID)
			}
			if _, err := service.db.Exec(
				"UPDATE book_request_jobs SET created_at = datetime('now', '-31 minutes') WHERE id = ?", job.ID,
			); err != nil {
				t.Fatal(err)
			}
			job, err = service.loadBookRequestJob(job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := service.restoreDurableSearchGuard(context.Background(), client, job); !errors.Is(err, errBookReconciliationExpired) {
				t.Fatalf("expired guard error = %v, want reconciliation expiry", err)
			}
			upstream.mu.Lock()
			searchCalls := upstream.searchCalls
			seedCalls := len(upstream.seedBodies)
			upstream.mu.Unlock()
			if searchCalls != 1 || seedCalls != 0 {
				t.Fatalf("invalid guard replayed work: searches=%d seeds=%d", searchCalls, seedCalls)
			}
		})
	}
}

func TestTransferredBookSearchGuardSurvivesOwnerDeletionAndRestart(t *testing.T) {
	upstream := newVerifiedBookUpstream("Transferred Search Guard", "transferred-search-guard")
	bookID := upstream.addExisting(BookFormatEbook, true)
	upstream.mu.Lock()
	upstream.author.EbookMonitorFuture = true
	upstream.searchCalls = 1 // the pre-restart search represented by the durable guard
	upstream.activeSearch = true
	upstream.mu.Unlock()
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, _ := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
	if err := service.setBookJobPhase(
		job.ID, "search_inflight", BookFormatEbook, upstream.author.ID,
		upstream.foreignAuthorID, upstream.authorName, bookID, false,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(
		"UPDATE book_request_jobs SET state = 'outcome_unknown', next_attempt_at = CURRENT_TIMESTAMP WHERE id = ?",
		job.ID,
	); err != nil {
		t.Fatal(err)
	}

	result, err := service.db.Exec(
		"INSERT INTO users (username, password_hash, role) VALUES ('book-job-admin', '', 'admin')",
	)
	if err != nil {
		t.Fatal(err)
	}
	adminID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(service.db, "book-job-test-secret")
	if err := authService.DeleteUser(adminID, userID); err != nil {
		t.Fatalf("delete original job owner: %v", err)
	}
	var storedOwnerID int64
	var state, phase string
	if err := service.db.QueryRow(
		"SELECT user_id, state, phase FROM book_request_jobs WHERE id = ?", job.ID,
	).Scan(&storedOwnerID, &state, &phase); err != nil {
		t.Fatal(err)
	}
	if storedOwnerID != adminID || state != "outcome_unknown" || phase != "search_inflight" {
		t.Fatalf("transferred guard = owner %d state %q phase %q", storedOwnerID, state, phase)
	}

	restarted, cancel := restartedBookWorker(service)
	defer cancel()
	waitForBookJobCount(t, restarted, 0)
	upstream.mu.Lock()
	searchCalls := upstream.searchCalls
	upstream.mu.Unlock()
	if searchCalls != 1 {
		t.Fatalf("transferred guard queued %d searches, want the original search only", searchCalls)
	}
	var historyOwnerID int64
	var historyStatus string
	if err := restarted.db.QueryRow(
		"SELECT user_id, status FROM request_log WHERE foreign_id = ? AND book_format = ?",
		upstream.foreignBookID, BookFormatEbook,
	).Scan(&historyOwnerID, &historyStatus); err != nil {
		t.Fatal(err)
	}
	if historyOwnerID != adminID || historyStatus != StatusRequested {
		t.Fatalf("transferred history = owner %d status %q", historyOwnerID, historyStatus)
	}
}

func TestBookRequestWorkerBothResumeSkipsCompletedFirstFormat(t *testing.T) {
	upstream := newVerifiedBookUpstream("Both Resume", "both-resume")
	ebookID := upstream.addExisting(BookFormatEbook, true)
	audioID := upstream.addExisting(BookFormatAudiobook, false)
	upstream.mu.Lock()
	upstream.author.EbookMonitorFuture = true
	upstream.mu.Unlock()
	service, userID := newVerifiedMutationService(t, upstream)
	job, resolved, _ := prepareWorkerTestJob(t, service, userID, upstream, BookFormatBoth)
	if err := service.completeBookJobFormat(resolved, BookFormatEbook, StatusRequested); err != nil {
		t.Fatal(err)
	}
	if err := service.setBookJobPhase(job.ID, "seed_inflight", BookFormatAudiobook, upstream.author.ID, upstream.foreignAuthorID, upstream.authorName, 0, false); err != nil {
		t.Fatal(err)
	}

	restarted, cancel := restartedBookWorker(service)
	defer cancel()
	waitForBookJobCount(t, restarted, 0)
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if upstream.searchCalls != 1 || len(upstream.searchBookIDs) != 1 || upstream.searchBookIDs[0] != audioID {
		t.Fatalf("resume searches=%d ids=%v, want only audiobook %d (ebook %d already complete)", upstream.searchCalls, upstream.searchBookIDs, audioID, ebookID)
	}
	var history int
	if err := restarted.db.QueryRow("SELECT COUNT(*) FROM request_log WHERE user_id = ? AND foreign_id = ?", userID, upstream.foreignBookID).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if history != 2 {
		t.Fatalf("history rows = %d, want one per completed format", history)
	}
}

func TestBookRequestWorkerClaimIsSingleOwner(t *testing.T) {
	upstream := newVerifiedBookUpstream("Single Claim", "single-claim")
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, _ := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
	if err := service.setBookJobPhase(job.ID, "seed_inflight", BookFormatEbook, 0, upstream.foreignAuthorID, upstream.authorName, 0, false); err != nil {
		t.Fatal(err)
	}
	upstream.addExisting(BookFormatEbook, false)
	service.bookWorkerGeneration = newBookWorkerGeneration()
	if _, err := service.db.Exec("UPDATE book_request_jobs SET state = 'retry_wait', next_attempt_at = CURRENT_TIMESTAMP WHERE id = ?", job.ID); err != nil {
		t.Fatal(err)
	}

	var workers sync.WaitGroup
	workers.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer workers.Done()
			service.runDueBookRequestJobs(context.Background())
		}()
	}
	workers.Wait()
	waitForBookJobCount(t, service, 0)
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if upstream.searchCalls != 1 {
		t.Fatalf("concurrent claims queued %d searches, want exactly one", upstream.searchCalls)
	}
}

func TestBookRequestJobBatchRunsUnrelatedWorkWithBoundedParallelism(t *testing.T) {
	candidates := make([]bookRequestJobCandidate, 9)
	for i := range candidates {
		candidates[i] = bookRequestJobCandidate{id: int64(i + 1), instanceID: "instance", foreignID: string(rune('a' + i))}
	}
	started := make(chan struct{}, len(candidates))
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseAll)
	done := make(chan struct{})
	var active atomic.Int32
	var peak atomic.Int32
	var completed atomic.Int32

	go func() {
		runBookRequestJobBatch(context.Background(), candidates, func(_ context.Context, _ bookRequestJobCandidate) {
			current := active.Add(1)
			for {
				seen := peak.Load()
				if current <= seen || peak.CompareAndSwap(seen, current) {
					break
				}
			}
			started <- struct{}{}
			<-release
			active.Add(-1)
			completed.Add(1)
		})
		close(done)
	}()

	for range bookRequestJobMaxParallel {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("unrelated jobs did not start in parallel")
		}
	}
	select {
	case <-started:
		t.Fatal("batch exceeded its four-job concurrency bound")
	default:
	}
	releaseAll()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("bounded batch did not finish")
	}
	if got := peak.Load(); got != bookRequestJobMaxParallel {
		t.Fatalf("peak parallel jobs = %d, want %d", got, bookRequestJobMaxParallel)
	}
	if got := completed.Load(); got != int32(len(candidates)) {
		t.Fatalf("completed jobs = %d, want %d", got, len(candidates))
	}
}

func TestBookRequestWorkerEarlyLoadErrorReleasesRunningClaim(t *testing.T) {
	for _, tc := range []struct {
		name      string
		phase     string
		wantState string
	}{
		{name: "before non-idempotent IO", phase: "queued", wantState: "retry_wait"},
		{name: "after seed intent", phase: "seed_inflight", wantState: "outcome_unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newVerifiedBookUpstream("Broken Claim", "broken-claim")
			service, userID := newVerifiedMutationService(t, upstream)
			job, _, _ := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
			service.bookWorkerGeneration = "current-generation"
			if _, err := service.db.Exec(
				`UPDATE book_request_jobs SET proc_generation = ?, phase = ?,
				 attempt_count = 'not-an-integer' WHERE id = ?`,
				service.bookWorkerGeneration, tc.phase, job.ID,
			); err != nil {
				t.Fatal(err)
			}

			service.runClaimedBookRequestJob(context.Background(), job.ID)

			var state, generation, code string
			if err := service.db.QueryRow(
				"SELECT state, proc_generation, last_error_code FROM book_request_jobs WHERE id = ?", job.ID,
			).Scan(&state, &generation, &code); err != nil {
				t.Fatal(err)
			}
			if state != tc.wantState || generation != "" || code != "book_request_retry" {
				t.Fatalf("released claim = state %q generation %q code %q", state, generation, code)
			}
		})
	}
}

func TestBookRequestWorkerLaterGenerationReclaimsStaleRunningClaim(t *testing.T) {
	upstream := newVerifiedBookUpstream("Stale Claim", "stale-claim")
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, _ := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
	if _, err := service.db.Exec(
		"UPDATE book_request_jobs SET proc_generation = 'old-generation' WHERE id = ?", job.ID,
	); err != nil {
		t.Fatal(err)
	}

	restarted := NewService(service.db, service.registry, service.bridge, service.notifier)
	restarted.bookMutationTimeout = 250 * time.Millisecond
	restarted.bookSettleInterval = time.Millisecond
	restarted.bookWorkerGeneration = "new-generation"
	if err := restarted.reclaimStaleBookRequestJobClaims(); err != nil {
		t.Fatal(err)
	}

	var state, generation string
	if err := restarted.db.QueryRow(
		"SELECT state, proc_generation FROM book_request_jobs WHERE id = ?", job.ID,
	).Scan(&state, &generation); err != nil {
		t.Fatal(err)
	}
	if state != "retry_wait" || generation != "" {
		t.Fatalf("reclaimed claim = state %q generation %q", state, generation)
	}

	restarted.runDueBookRequestJobs(context.Background())
	waitForBookJobCount(t, restarted, 0)
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if len(upstream.seedBodies) != 1 || upstream.searchCalls != 1 {
		t.Fatalf("reclaimed work seed=%d search=%d, want one completion", len(upstream.seedBodies), upstream.searchCalls)
	}
}

func TestBookRequestWorkerDoesNotReclaimCurrentGenerationClaim(t *testing.T) {
	upstream := newVerifiedBookUpstream("Live Claim", "live-claim")
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, _ := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
	service.bookWorkerGeneration = "live-generation"
	if _, err := service.db.Exec(
		"UPDATE book_request_jobs SET proc_generation = ? WHERE id = ?",
		service.bookWorkerGeneration, job.ID,
	); err != nil {
		t.Fatal(err)
	}
	if err := service.reclaimStaleBookRequestJobClaims(); err != nil {
		t.Fatal(err)
	}
	if err := service.reclaimExpiredBookRequestJobClaims(); err != nil {
		t.Fatal(err)
	}

	var state, generation string
	if err := service.db.QueryRow(
		"SELECT state, proc_generation FROM book_request_jobs WHERE id = ?", job.ID,
	).Scan(&state, &generation); err != nil {
		t.Fatal(err)
	}
	if state != "running" || generation != service.bookWorkerGeneration {
		t.Fatalf("live claim changed to state %q generation %q", state, generation)
	}
}

func TestBookRequestWorkerReclaimsExpiredCurrentGenerationClaim(t *testing.T) {
	for _, tc := range []struct {
		name      string
		phase     string
		wantState string
	}{
		{name: "queued", phase: "queued", wantState: "retry_wait"},
		{name: "uncertain seed", phase: "seed_inflight", wantState: "outcome_unknown"},
		{name: "uncertain search", phase: "search_inflight", wantState: "outcome_unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newVerifiedBookUpstream("Expired Live Claim", "expired-live-claim-"+tc.phase)
			service, userID := newVerifiedMutationService(t, upstream)
			job, _, _ := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
			service.bookWorkerGeneration = "live-generation"
			if _, err := service.db.Exec(
				`UPDATE book_request_jobs SET phase = ?, proc_generation = ?,
				 updated_at = datetime('now', '-6 minutes') WHERE id = ?`,
				tc.phase, service.bookWorkerGeneration, job.ID,
			); err != nil {
				t.Fatal(err)
			}

			if err := service.reclaimExpiredBookRequestJobClaims(); err != nil {
				t.Fatal(err)
			}
			var state, generation, code string
			if err := service.db.QueryRow(
				"SELECT state, proc_generation, last_error_code FROM book_request_jobs WHERE id = ?", job.ID,
			).Scan(&state, &generation, &code); err != nil {
				t.Fatal(err)
			}
			if state != tc.wantState || generation != "" || code != "book_request_retry" {
				t.Fatalf("expired live claim = %s generation=%q code=%s", state, generation, code)
			}
		})
	}
}

func TestBookRequestWorkerPollReclaimsExpiredRunningClaim(t *testing.T) {
	upstream := newVerifiedBookUpstream("Polled Expired Claim", "polled-expired-claim")
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, _ := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
	service.bookWorkerGeneration = "live-generation"
	if _, err := service.db.Exec(
		`UPDATE book_request_jobs SET proc_generation = ?,
		 updated_at = datetime('now', '-6 minutes') WHERE id = ?`,
		service.bookWorkerGeneration, job.ID,
	); err != nil {
		t.Fatal(err)
	}

	service.runDueBookRequestJobs(context.Background())
	waitForBookJobCount(t, service, 0)
	upstream.mu.Lock()
	seedCalls, searchCalls := len(upstream.seedBodies), upstream.searchCalls
	upstream.mu.Unlock()
	if seedCalls != 1 || searchCalls != 1 {
		t.Fatalf("polled expired claim seed=%d search=%d, want 1/1", seedCalls, searchCalls)
	}
}

func TestBookRequestWorkerReclaimsStaleInflightIntentAsOutcomeUnknown(t *testing.T) {
	upstream := newVerifiedBookUpstream("Stale Seed Intent", "stale-seed-intent")
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, _ := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
	if _, err := service.db.Exec(
		`UPDATE book_request_jobs SET phase = 'seed_inflight', phase_format = ?,
		 proc_generation = 'dead-generation' WHERE id = ?`,
		BookFormatEbook, job.ID,
	); err != nil {
		t.Fatal(err)
	}
	service.bookWorkerGeneration = "replacement-generation"
	if err := service.reclaimStaleBookRequestJobClaims(); err != nil {
		t.Fatal(err)
	}

	var state, generation string
	if err := service.db.QueryRow(
		"SELECT state, proc_generation FROM book_request_jobs WHERE id = ?", job.ID,
	).Scan(&state, &generation); err != nil {
		t.Fatal(err)
	}
	if state != "outcome_unknown" || generation != "" {
		t.Fatalf("stale seed claim changed to state %q generation %q", state, generation)
	}
}

func TestDurableAuthorSeedBlocksDifferentTitleFromDuplicateAuthorPost(t *testing.T) {
	upstream := newVerifiedBookUpstream("First Work", "first-work")
	upstream.dropAddWithoutCommit[BookFormatEbook] = true
	service, userID := newVerifiedMutationService(t, upstream)
	service.bookMutationTimeout = 20 * time.Millisecond

	_, err := service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatEbook,
	})
	if !errors.Is(err, ErrBookOutcomePending) {
		t.Fatalf("first request error = %v, want durable outcome_pending", err)
	}
	upstream.mu.Lock()
	upstream.title = "Second Work"
	upstream.foreignBookID = "second-work"
	upstream.mu.Unlock()

	_, err = service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: "second-work", Title: "Second Work", BookFormat: BookFormatAudiobook,
	})
	if !errors.Is(err, ErrBookOutcomePending) {
		t.Fatalf("second request error = %v, want durable author-owner outcome_pending", err)
	}
	upstream.mu.Lock()
	seedCalls := len(upstream.seedBodies)
	upstream.mu.Unlock()
	if seedCalls != 1 {
		t.Fatalf("same unresolved author received %d seed POSTs, want only the first", seedCalls)
	}
	var jobs int
	if err := service.db.QueryRow("SELECT COUNT(*) FROM book_request_jobs").Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 2 {
		t.Fatalf("durable jobs = %d, want first owner plus blocked second request", jobs)
	}
}

func TestActiveBookJobBlocksEveryFormatAndHouseholdUser(t *testing.T) {
	upstream := newVerifiedBookUpstream("Shared Work", "shared-work")
	service, firstUserID := newVerifiedMutationService(t, upstream)
	_, _, instanceID := prepareWorkerTestJob(t, service, firstUserID, upstream, BookFormatEbook)
	result, err := service.db.Exec("INSERT INTO users (username, password_hash, role) VALUES ('second-reader', '', 'user')")
	if err != nil {
		t.Fatal(err)
	}
	secondUserID, _ := result.LastInsertId()
	if _, err := service.db.Exec(
		"INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (?, 'chaptarr', ?)",
		secondUserID, instanceID,
	); err != nil {
		t.Fatal(err)
	}

	_, err = service.CreateMediaRequest(secondUserID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatAudiobook,
	})
	if !errors.Is(err, ErrBookOutcomePending) {
		t.Fatalf("second user/format error = %v, want active work owner", err)
	}
	var jobs int
	if err := service.db.QueryRow("SELECT COUNT(*) FROM book_request_jobs").Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 {
		t.Fatalf("active work created %d durable jobs, want one owner", jobs)
	}
}

func TestApprovalStaysPendingWhileDirectJobOwnsWork(t *testing.T) {
	upstream := newVerifiedBookUpstream("Approval Collision", "approval-collision")
	service, userID := newVerifiedMutationService(t, upstream)
	_, _, instanceID := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
	result, err := service.db.Exec("INSERT INTO users (username, password_hash, role) VALUES ('book-admin', '', 'admin')")
	if err != nil {
		t.Fatal(err)
	}
	adminID, _ := result.LastInsertId()
	result, err = service.db.Exec(
		`INSERT INTO request_log
		 (user_id, tmdb_id, foreign_id, book_format, instance_id, media_type, title, status)
		 VALUES (?, 0, ?, ?, ?, 'book', ?, ?)`,
		userID, upstream.foreignBookID, BookFormatAudiobook, instanceID, upstream.title, StatusPending,
	)
	if err != nil {
		t.Fatal(err)
	}
	requestID, _ := result.LastInsertId()

	if _, err := service.ApproveRequest(adminID, requestID, nil); !errors.Is(err, ErrBookOutcomePending) {
		t.Fatalf("ApproveRequest error = %v, want active direct owner", err)
	}
	var status string
	if err := service.db.QueryRow("SELECT status FROM request_log WHERE id = ?", requestID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != StatusPending {
		t.Fatalf("approval row status = %q, want pending", status)
	}
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if len(upstream.seedBodies) != 0 || upstream.searchCalls != 0 {
		t.Fatalf("blocked approval mutated Chaptarr: seeds=%d searches=%d", len(upstream.seedBodies), upstream.searchCalls)
	}
}

func TestInflightBookJobSurvivesInstanceFingerprintDrift(t *testing.T) {
	upstream := newVerifiedBookUpstream("Drift Work", "drift-work")
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, _ := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
	if err := service.setBookJobPhase(job.ID, "seed_inflight", BookFormatEbook, 0, upstream.foreignAuthorID, upstream.authorName, 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(
		"UPDATE book_request_jobs SET settings_fingerprint = zeroblob(32), state = 'retry_wait', next_attempt_at = CURRENT_TIMESTAMP WHERE id = ?",
		job.ID,
	); err != nil {
		t.Fatal(err)
	}
	service.bookWorkerGeneration = newBookWorkerGeneration()
	service.runDueBookRequestJobs(context.Background())

	var state, code string
	if err := service.db.QueryRow("SELECT state, last_error_code FROM book_request_jobs WHERE id = ?", job.ID).Scan(&state, &code); err != nil {
		t.Fatal(err)
	}
	if state != "outcome_unknown" || code != "book_instance_changed" {
		t.Fatalf("drifted inflight job = %s/%s, want retained outcome_unknown/book_instance_changed", state, code)
	}
	if _, err := service.db.Exec(
		`UPDATE book_request_jobs SET created_at = datetime('now', '-31 minutes'),
		 phase_started_at = datetime('now', '-31 minutes'), state = 'retry_wait',
		 next_attempt_at = CURRENT_TIMESTAMP WHERE id = ?`,
		job.ID,
	); err != nil {
		t.Fatal(err)
	}
	service.runDueBookRequestJobs(context.Background())
	if err := service.db.QueryRow("SELECT state, last_error_code FROM book_request_jobs WHERE id = ?", job.ID).Scan(&state, &code); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || code != "book_request_unverified" {
		t.Fatalf("expired drifted job = %s/%s, want failed/book_request_unverified", state, code)
	}
}

func TestDurableSeedRejectsSameWorkRowWithWrongAuthor(t *testing.T) {
	upstream := newVerifiedBookUpstream("Wrong Author Work", "wrong-author-work")
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, _ := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
	if err := service.setBookJobPhase(job.ID, "seed_inflight", BookFormatEbook, 0, upstream.foreignAuthorID, upstream.authorName, 0, false); err != nil {
		t.Fatal(err)
	}
	bookID := upstream.addExisting(BookFormatEbook, false)
	upstream.mu.Lock()
	upstream.author.AuthorName = "Different Author"
	upstream.author.ForeignAuthorID = "hc:different-author"
	upstream.rows[bookID].book.AuthorID = upstream.author.ID
	upstream.mu.Unlock()
	if _, err := service.db.Exec("UPDATE book_request_jobs SET state = 'retry_wait', next_attempt_at = CURRENT_TIMESTAMP WHERE id = ?", job.ID); err != nil {
		t.Fatal(err)
	}
	service.bookWorkerGeneration = newBookWorkerGeneration()
	service.runDueBookRequestJobs(context.Background())

	var phase, state string
	if err := service.db.QueryRow("SELECT phase, state FROM book_request_jobs WHERE id = ?", job.ID).Scan(&phase, &state); err != nil {
		t.Fatal(err)
	}
	if phase != "seed_inflight" || state != "outcome_unknown" {
		t.Fatalf("wrong-author seed advanced to %s/%s", phase, state)
	}
	upstream.mu.Lock()
	monitorCalls, searchCalls := len(upstream.monitorIDs), upstream.searchCalls
	upstream.mu.Unlock()
	if monitorCalls != 0 || searchCalls != 0 {
		t.Fatalf("wrong-author seed mutated monitor=%d search=%d", monitorCalls, searchCalls)
	}
	if _, err := service.db.Exec(
		`UPDATE book_request_jobs SET created_at = datetime('now', '-31 minutes'),
		 phase_started_at = datetime('now', '-31 minutes'), state = 'retry_wait',
		 next_attempt_at = CURRENT_TIMESTAMP WHERE id = ?`,
		job.ID,
	); err != nil {
		t.Fatal(err)
	}
	service.runDueBookRequestJobs(context.Background())
	var code string
	if err := service.db.QueryRow("SELECT state, last_error_code FROM book_request_jobs WHERE id = ?", job.ID).Scan(&state, &code); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || code != "book_request_unverified" {
		t.Fatalf("expired wrong-author seed = %s/%s, want failed/book_request_unverified", state, code)
	}
}

func forceBookJobDue(t *testing.T, service *Service, jobID int64) {
	t.Helper()
	if _, err := service.db.Exec(
		"UPDATE book_request_jobs SET state = 'retry_wait', next_attempt_at = CURRENT_TIMESTAMP WHERE id = ?",
		jobID,
	); err != nil {
		t.Fatal(err)
	}
	service.bookWorkerGeneration = newBookWorkerGeneration()
	service.runDueBookRequestJobs(context.Background())
}

func TestBookRequestWorkerStopsReconcilingAbsentSeedAtLifetime(t *testing.T) {
	upstream := newVerifiedBookUpstream("Expired Seed", "expired-seed")
	upstream.dropAddWithoutCommit[BookFormatEbook] = true
	service, userID := newVerifiedMutationService(t, upstream)
	service.bookMutationTimeout = 20 * time.Millisecond
	_, instanceID, err := service.resolveChaptarr(userID, "")
	if err != nil {
		t.Fatal(err)
	}

	if _, err = service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatEbook,
	}); !errors.Is(err, ErrBookOutcomePending) {
		t.Fatalf("initial request error = %v, want outcome_pending", err)
	}
	var jobID int64
	var initialPhaseAt time.Time
	if err := service.db.QueryRow(
		"SELECT id, phase_started_at FROM book_request_jobs WHERE foreign_id = ?",
		upstream.foreignBookID,
	).Scan(&jobID, &initialPhaseAt); err != nil {
		t.Fatal(err)
	}

	forceBookJobDue(t, service, jobID)
	upstream.mu.Lock()
	preExpirySeeds := len(upstream.seedBodies)
	upstream.mu.Unlock()
	if preExpirySeeds != 1 {
		t.Fatalf("seed POSTs before TTL = %d, want original only", preExpirySeeds)
	}
	var guardedPhase, expiredState string
	var guardedPhaseAt time.Time
	if err := service.db.QueryRow(
		"SELECT phase, phase_started_at FROM book_request_jobs WHERE id = ?", jobID,
	).Scan(&guardedPhase, &guardedPhaseAt); err != nil {
		t.Fatal(err)
	}
	if guardedPhase != "seed_inflight" || !guardedPhaseAt.Equal(initialPhaseAt) {
		t.Fatalf("pre-TTL guard advanced phase=%s at=%s; want unchanged seed intent at %s", guardedPhase, guardedPhaseAt, initialPhaseAt)
	}

	if _, err := service.db.Exec(
		"UPDATE book_request_jobs SET phase_started_at = datetime('now', '-31 minutes') WHERE id = ?",
		jobID,
	); err != nil {
		t.Fatal(err)
	}
	forceBookJobDue(t, service, jobID)
	upstream.mu.Lock()
	oldPhaseSeeds := len(upstream.seedBodies)
	upstream.mu.Unlock()
	if oldPhaseSeeds != 1 {
		t.Fatalf("old seed phase POSTs = %d, want original only", oldPhaseSeeds)
	}
	if err := service.db.QueryRow(
		"SELECT phase, state FROM book_request_jobs WHERE id = ?", jobID,
	).Scan(&guardedPhase, &expiredState); err != nil {
		t.Fatal(err)
	}
	if guardedPhase != "seed_inflight" || expiredState != "outcome_unknown" {
		t.Fatalf("old seed phase = %s/%s, want guarded seed_inflight/outcome_unknown", guardedPhase, expiredState)
	}

	if _, err := service.db.Exec(
		`UPDATE book_request_jobs SET phase_started_at = CURRENT_TIMESTAMP,
		 created_at = datetime('now', '-31 minutes'), attempt_count = 130,
		 state = 'retry_wait', next_attempt_at = CURRENT_TIMESTAMP WHERE id = ?`,
		jobID,
	); err != nil {
		t.Fatal(err)
	}
	service.runDueBookRequestJobs(context.Background())
	upstream.mu.Lock()
	latePhaseSeeds := len(upstream.seedBodies)
	upstream.mu.Unlock()
	if latePhaseSeeds != 1 {
		t.Fatalf("fresh seed guard POSTs = %d, want original only", latePhaseSeeds)
	}
	var guardedAttempts int
	if err := service.db.QueryRow(
		"SELECT state, attempt_count FROM book_request_jobs WHERE id = ?", jobID,
	).Scan(&expiredState, &guardedAttempts); err != nil {
		t.Fatal(err)
	}
	if expiredState != "outcome_unknown" || guardedAttempts != 131 {
		t.Fatalf("fresh seed guard = %s attempts=%d, want outcome_unknown at 131", expiredState, guardedAttempts)
	}

	if _, err := service.db.Exec(
		`UPDATE book_request_jobs SET phase_started_at = datetime('now', '-31 minutes'),
		 next_attempt_at = datetime('now', '+1 day') WHERE id = ?`, jobID,
	); err != nil {
		t.Fatal(err)
	}
	service.runDueBookRequestJobs(context.Background())
	upstream.mu.Lock()
	postExpirySeeds := len(upstream.seedBodies)
	upstream.mu.Unlock()
	if postExpirySeeds != 1 {
		t.Fatalf("seed POSTs after lifetime = %d, want original only", postExpirySeeds)
	}
	var failureCode string
	var expiredAttempts int
	if err := service.db.QueryRow(
		"SELECT state, last_error_code, attempt_count FROM book_request_jobs WHERE id = ?", jobID,
	).Scan(&expiredState, &failureCode, &expiredAttempts); err != nil {
		t.Fatal(err)
	}
	if expiredState != "failed" || failureCode != "book_request_unverified" || expiredAttempts != 132 {
		t.Fatalf("expired seed = %s/%s attempts=%d, want failed/book_request_unverified at 132", expiredState, failureCode, expiredAttempts)
	}
	active, err := service.hasActiveBookRequestJob(instanceID, upstream.foreignBookID)
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("expired seed still owns the title")
	}
	service.runDueBookRequestJobs(context.Background())
	var unchangedAttempts int
	if err := service.db.QueryRow("SELECT attempt_count FROM book_request_jobs WHERE id = ?", jobID).Scan(&unchangedAttempts); err != nil {
		t.Fatal(err)
	}
	if unchangedAttempts != expiredAttempts {
		t.Fatalf("terminal seed attempts advanced from %d to %d", expiredAttempts, unchangedAttempts)
	}

	upstream.mu.Lock()
	upstream.dropAddWithoutCommit[BookFormatEbook] = false
	upstream.mu.Unlock()
	service.bookMutationTimeout = 250 * time.Millisecond
	response, err := service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatEbook,
	})
	if err != nil {
		t.Fatalf("explicit retry: %v", err)
	}
	if response.Status != StatusRequested {
		t.Fatalf("explicit retry status = %s, want requested", response.Status)
	}
	upstream.mu.Lock()
	retrySeeds := len(upstream.seedBodies)
	upstream.mu.Unlock()
	if retrySeeds != 2 {
		t.Fatalf("explicit retry seed POSTs = %d, want original plus one deliberate retry", retrySeeds)
	}
}

func TestExpiredBookSeedReadsVisibleOutcomeWithoutContinuingWrites(t *testing.T) {
	upstream := newVerifiedBookUpstream("Visible Expired Seed", "visible-expired-seed")
	upstream.addExisting(BookFormatEbook, false)
	upstream.mu.Lock()
	upstream.seedBodies = append(upstream.seedBodies, map[string]any{"mediaType": BookFormatEbook})
	upstream.mu.Unlock()
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, _ := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
	if err := service.setBookJobPhase(
		job.ID, "seed_inflight", BookFormatEbook, 0,
		upstream.foreignAuthorID, upstream.authorName, 0, false,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(
		`UPDATE book_request_jobs SET created_at = datetime('now', '-31 minutes'),
		 phase_started_at = datetime('now', '-31 minutes'), state = 'retry_wait',
		 next_attempt_at = datetime('now', '+1 day') WHERE id = ?`, job.ID,
	); err != nil {
		t.Fatal(err)
	}
	service.bookWorkerGeneration = newBookWorkerGeneration()
	service.runDueBookRequestJobs(context.Background())
	var state, code string
	if err := service.db.QueryRow(
		"SELECT state, last_error_code FROM book_request_jobs WHERE id = ?", job.ID,
	).Scan(&state, &code); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || code != "book_request_unverified" {
		t.Fatalf("expired visible seed = %s/%s, want failed/book_request_unverified", state, code)
	}

	upstream.mu.Lock()
	seedCalls := len(upstream.seedBodies)
	monitorCalls := len(upstream.monitorIDs)
	searchCalls := upstream.searchCalls
	upstream.mu.Unlock()
	if seedCalls != 1 || monitorCalls != 0 || searchCalls != 0 {
		t.Fatalf("expired visible seed mutated Chaptarr: seed=%d monitor=%d search=%d", seedCalls, monitorCalls, searchCalls)
	}
}

func TestExpiredAcknowledgedSearchPreservesProofAcrossRetry(t *testing.T) {
	upstream := newVerifiedBookUpstream("Acknowledged Expiry", "acknowledged-expiry")
	bookID := upstream.addExisting(BookFormatAudiobook, true)
	upstream.mu.Lock()
	upstream.author.AudiobookMonitorFuture = true
	upstream.searchCalls = 1
	upstream.searchBookIDs = []int{bookID}
	upstream.mu.Unlock()
	service, userID := newVerifiedMutationService(t, upstream)
	job, resolved, _ := prepareWorkerTestJob(t, service, userID, upstream, BookFormatAudiobook)
	if err := service.setBookJobPhase(
		job.ID, "search_inflight", BookFormatAudiobook, upstream.author.ID,
		upstream.foreignAuthorID, upstream.authorName, bookID, true,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(
		`UPDATE book_request_jobs SET created_at = datetime('now', '-31 minutes'),
		 phase_started_at = datetime('now', '-3 minutes') WHERE id = ?`, job.ID,
	); err != nil {
		t.Fatal(err)
	}
	if retained := service.deferDirectBookJob(job.ID, errors.New("temporary upstream failure")); retained {
		t.Fatal("expired acknowledged search remained active")
	}

	var state, code, audiobookStatus string
	if err := service.db.QueryRow(
		"SELECT state, last_error_code, audiobook_status FROM book_request_jobs WHERE id = ?", job.ID,
	).Scan(&state, &code, &audiobookStatus); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || code != "book_request_unverified" || audiobookStatus != StatusRequested {
		t.Fatalf("expired acknowledged search = %s/%s checkpoint=%s", state, code, audiobookStatus)
	}

	response, err := service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: resolved.foreignID, Title: resolved.title, BookFormat: BookFormatAudiobook,
	})
	if err != nil {
		t.Fatalf("retry acknowledged search: %v", err)
	}
	if response.Status != StatusRequested {
		t.Fatalf("retry acknowledged status = %s, want requested", response.Status)
	}
	upstream.mu.Lock()
	searchCalls := upstream.searchCalls
	upstream.mu.Unlock()
	if searchCalls != 1 {
		t.Fatalf("acknowledged search replayed %d times, want original only", searchCalls)
	}
}

func TestExpiredSearchProofDiscoveredDuringFinalReconciliationIsDurable(t *testing.T) {
	upstream := newVerifiedBookUpstream("Discovered Search Proof", "discovered-search-proof")
	bookID := upstream.addExisting(BookFormatAudiobook, true)
	upstream.mu.Lock()
	upstream.author.AudiobookMonitorFuture = true
	upstream.searchCalls = 1
	upstream.searchBookIDs = []int{bookID}
	upstream.activeSearch = true
	upstream.activeSearchBookID = bookID
	upstream.bookListStatus = http.StatusBadGateway
	upstream.mu.Unlock()
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, _ := prepareWorkerTestJob(t, service, userID, upstream, BookFormatAudiobook)
	if err := service.setBookJobPhase(
		job.ID, "search_inflight", BookFormatAudiobook, upstream.author.ID,
		upstream.foreignAuthorID, upstream.authorName, bookID, false,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(
		`UPDATE book_request_jobs SET created_at = datetime('now', '-31 minutes'),
		 phase_started_at = datetime('now', '-3 minutes'), state = 'retry_wait',
		 next_attempt_at = datetime('now', '+1 day') WHERE id = ?`, job.ID,
	); err != nil {
		t.Fatal(err)
	}
	service.bookWorkerGeneration = newBookWorkerGeneration()
	service.runDueBookRequestJobs(context.Background())

	var jobCount, requested int
	if err := service.db.QueryRow(
		"SELECT COUNT(*) FROM book_request_jobs WHERE id = ?", job.ID,
	).Scan(&jobCount); err != nil {
		t.Fatal(err)
	}
	if err := service.db.QueryRow(
		`SELECT COUNT(*) FROM request_log
		 WHERE user_id = ? AND foreign_id = ? AND book_format = ? AND status = ?`,
		userID, upstream.foreignBookID, BookFormatAudiobook, StatusRequested,
	).Scan(&requested); err != nil {
		t.Fatal(err)
	}
	if jobCount != 0 || requested != 1 {
		t.Fatalf("discovered search proof left jobs=%d requested=%d, want 0/1", jobCount, requested)
	}
	upstream.mu.Lock()
	searchCalls := upstream.searchCalls
	upstream.mu.Unlock()
	if searchCalls != 1 {
		t.Fatalf("discovered acknowledged search replayed %d times, want original only", searchCalls)
	}
}

func TestExpiredMalformedBookJobIgnoresFutureRetryTime(t *testing.T) {
	upstream := newVerifiedBookUpstream("Malformed Expiry", "malformed-expiry")
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, _ := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
	if _, err := service.db.Exec(
		`UPDATE book_request_jobs SET book_selection_json = '{',
		 created_at = datetime('now', '-31 minutes'), state = 'retry_wait',
		 next_attempt_at = datetime('now', '+1 day') WHERE id = ?`, job.ID,
	); err != nil {
		t.Fatal(err)
	}
	service.bookWorkerGeneration = newBookWorkerGeneration()
	service.runDueBookRequestJobs(context.Background())

	var state, code string
	if err := service.db.QueryRow(
		"SELECT state, last_error_code FROM book_request_jobs WHERE id = ?", job.ID,
	).Scan(&state, &code); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || code != "book_request_unverified" {
		t.Fatalf("expired malformed job = %s/%s, want failed/book_request_unverified", state, code)
	}
}

func TestBookRequestWorkerNeverReplaysOldProvisionalSeedFootprint(t *testing.T) {
	upstream := newVerifiedBookUpstream("Provisional Seed", "provisional-seed")
	bookID := upstream.addExisting(BookFormatEbook, false)
	upstream.mu.Lock()
	row := upstream.rows[bookID]
	row.book.ReleaseDate = nil
	row.book.Images = nil
	row.book.ForeignEditionID = "default-provisional"
	upstream.seedBodies = append(upstream.seedBodies, map[string]any{"mediaType": BookFormatEbook})
	upstream.mu.Unlock()
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, _ := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
	if err := service.setBookJobPhase(
		job.ID, "seed_inflight", BookFormatEbook, upstream.author.ID,
		upstream.foreignAuthorID, upstream.authorName, 0, false,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(
		`UPDATE book_request_jobs SET phase_started_at = datetime('now', '-31 minutes'),
		 created_at = datetime('now', '-31 minutes'), attempt_count = 130 WHERE id = ?`, job.ID,
	); err != nil {
		t.Fatal(err)
	}

	forceBookJobDue(t, service, job.ID)
	upstream.mu.Lock()
	seedCalls := len(upstream.seedBodies)
	upstream.mu.Unlock()
	if seedCalls != 1 {
		t.Fatalf("old provisional footprint caused %d seed POSTs, want original only", seedCalls)
	}
	var state, code string
	if err := service.db.QueryRow("SELECT state, last_error_code FROM book_request_jobs WHERE id = ?", job.ID).Scan(&state, &code); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || code != "book_request_unverified" {
		t.Fatalf("expired provisional footprint = %s/%s, want failed/book_request_unverified", state, code)
	}
}

func TestBookRequestWorkerRebindsQueuedJobImmediately(t *testing.T) {
	upstream := newVerifiedBookUpstream("Queued Rebind", "queued-rebind")
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, _ := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
	if _, err := service.db.Exec("UPDATE book_request_jobs SET settings_fingerprint = zeroblob(32) WHERE id = ?", job.ID); err != nil {
		t.Fatal(err)
	}

	forceBookJobDue(t, service, job.ID)
	waitForBookJobCount(t, service, 0)
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if len(upstream.seedBodies) != 1 || upstream.searchCalls != 1 {
		t.Fatalf("queued rebind seed=%d search=%d, want one exact request", len(upstream.seedBodies), upstream.searchCalls)
	}
}

func TestBookRequestWorkerExpiredSeedRebindRequiresExplicitRetry(t *testing.T) {
	upstream := newVerifiedBookUpstream("Seed Rebind", "seed-rebind")
	upstream.dropAddWithoutCommit[BookFormatEbook] = true
	service, userID := newVerifiedMutationService(t, upstream)
	service.bookMutationTimeout = 20 * time.Millisecond
	if _, err := service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatEbook,
	}); !errors.Is(err, ErrBookOutcomePending) {
		t.Fatalf("initial request error = %v, want outcome_pending", err)
	}
	var jobID int64
	if err := service.db.QueryRow("SELECT id FROM book_request_jobs WHERE foreign_id = ?", upstream.foreignBookID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec("UPDATE book_request_jobs SET settings_fingerprint = zeroblob(32) WHERE id = ?", jobID); err != nil {
		t.Fatal(err)
	}

	forceBookJobDue(t, service, jobID)
	upstream.mu.Lock()
	preExpirySeeds := len(upstream.seedBodies)
	upstream.mu.Unlock()
	if preExpirySeeds != 1 {
		t.Fatalf("drifted seed replayed before TTL; POSTs=%d", preExpirySeeds)
	}

	if _, err := service.db.Exec(
		`UPDATE book_request_jobs SET created_at = datetime('now', '-31 minutes'),
		 phase_started_at = datetime('now', '-31 minutes'),
		 next_attempt_at = datetime('now', '+1 day') WHERE id = ?`, jobID,
	); err != nil {
		t.Fatal(err)
	}
	service.runDueBookRequestJobs(context.Background())
	upstream.mu.Lock()
	postExpirySeeds := len(upstream.seedBodies)
	upstream.mu.Unlock()
	if postExpirySeeds != 1 {
		t.Fatalf("expired drifted seed POSTs=%d, want original only", postExpirySeeds)
	}
	var state, code string
	if err := service.db.QueryRow(
		"SELECT state, last_error_code FROM book_request_jobs WHERE id = ?", jobID,
	).Scan(&state, &code); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || code != "book_request_unverified" {
		t.Fatalf("expired drifted seed = %s/%s, want failed/book_request_unverified", state, code)
	}

	upstream.mu.Lock()
	upstream.dropAddWithoutCommit[BookFormatEbook] = false
	upstream.mu.Unlock()
	service.bookMutationTimeout = 250 * time.Millisecond
	response, err := service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID,
		Title: upstream.title, BookFormat: BookFormatEbook,
	})
	if err != nil {
		t.Fatalf("explicit rebind retry: %v", err)
	}
	if response.Status != StatusRequested {
		t.Fatalf("explicit rebind retry status=%s, want requested", response.Status)
	}
	upstream.mu.Lock()
	retrySeeds := len(upstream.seedBodies)
	upstream.mu.Unlock()
	if retrySeeds != 2 {
		t.Fatalf("explicit rebind retry seed POSTs=%d, want original plus one retry", retrySeeds)
	}
}

func TestBookRequestWorkerRepointRevalidatesAcknowledgedSearchAndCompletedSibling(t *testing.T) {
	upstream := newVerifiedBookUpstream("Ack Rebind", "ack-rebind")
	upstream.addExisting(BookFormatEbook, true)
	audioID := upstream.addExisting(BookFormatAudiobook, true)
	upstream.mu.Lock()
	upstream.author.EbookMonitorFuture = true
	upstream.author.AudiobookMonitorFuture = true
	upstream.mu.Unlock()
	service, userID := newVerifiedMutationService(t, upstream)
	job, resolved, _ := prepareWorkerTestJob(t, service, userID, upstream, BookFormatBoth)
	if err := service.completeBookJobFormat(resolved, BookFormatEbook, StatusRequested); err != nil {
		t.Fatal(err)
	}
	if err := service.setBookJobPhase(
		job.ID, "search_inflight", BookFormatAudiobook, upstream.author.ID,
		upstream.foreignAuthorID, upstream.authorName, audioID, true,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec("UPDATE book_request_jobs SET settings_fingerprint = zeroblob(32) WHERE id = ?", job.ID); err != nil {
		t.Fatal(err)
	}

	forceBookJobDue(t, service, job.ID)
	upstream.mu.Lock()
	preExpirySearches := upstream.searchCalls
	upstream.mu.Unlock()
	if preExpirySearches != 0 {
		t.Fatalf("acknowledged search was revalidated before drift guard expired; searches=%d", preExpirySearches)
	}
	if _, err := service.db.Exec(
		"UPDATE book_request_jobs SET phase_started_at = datetime('now', '-3 minutes') WHERE id = ?", job.ID,
	); err != nil {
		t.Fatal(err)
	}
	forceBookJobDue(t, service, job.ID)
	waitForBookJobCount(t, service, 0)
	upstream.mu.Lock()
	searches := upstream.searchCalls
	upstream.mu.Unlock()
	if searches != 2 {
		t.Fatalf("repointed acknowledged work queued %d searches, want both formats revalidated", searches)
	}
	var ebookRows, audiobookRows int
	if err := service.db.QueryRow(
		`SELECT
		 SUM(CASE WHEN book_format = ? THEN 1 ELSE 0 END),
		 SUM(CASE WHEN book_format = ? THEN 1 ELSE 0 END)
		 FROM request_log WHERE user_id = ? AND foreign_id = ?`,
		BookFormatEbook, BookFormatAudiobook, userID, upstream.foreignBookID,
	).Scan(&ebookRows, &audiobookRows); err != nil {
		t.Fatal(err)
	}
	if ebookRows != 1 || audiobookRows != 1 {
		t.Fatalf("checkpointed history ebook=%d audiobook=%d, want one each", ebookRows, audiobookRows)
	}
}

func TestBookRequestWorkerWaitsThenRebindsUnacknowledgedSearch(t *testing.T) {
	upstream := newVerifiedBookUpstream("Guarded Rebind", "guarded-rebind")
	ebookID := upstream.addExisting(BookFormatEbook, true)
	const oldAudioID = 99999
	upstream.mu.Lock()
	upstream.author.EbookMonitorFuture = true
	upstream.author.AudiobookMonitorFuture = true
	upstream.searchCalls = 1 // the old-instance POST with an unknown outcome
	upstream.mu.Unlock()
	service, userID := newVerifiedMutationService(t, upstream)
	job, resolved, _ := prepareWorkerTestJob(t, service, userID, upstream, BookFormatBoth)
	if err := service.completeBookJobFormat(resolved, BookFormatEbook, StatusRequested); err != nil {
		t.Fatal(err)
	}
	if err := service.setBookJobPhase(
		job.ID, "search_inflight", BookFormatAudiobook, upstream.author.ID,
		upstream.foreignAuthorID, upstream.authorName, oldAudioID, false,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec("UPDATE book_request_jobs SET settings_fingerprint = zeroblob(32) WHERE id = ?", job.ID); err != nil {
		t.Fatal(err)
	}

	forceBookJobDue(t, service, job.ID)
	upstream.mu.Lock()
	preExpirySearches := upstream.searchCalls
	upstream.mu.Unlock()
	if preExpirySearches != 1 {
		t.Fatalf("unacknowledged rebind replayed before guard expiry; searches=%d", preExpirySearches)
	}
	var phase, ebookStatus, audioStatus string
	if err := service.db.QueryRow(
		"SELECT phase, ebook_status, audiobook_status FROM book_request_jobs WHERE id = ?", job.ID,
	).Scan(&phase, &ebookStatus, &audioStatus); err != nil {
		t.Fatal(err)
	}
	if phase != "search_inflight" || ebookStatus != StatusRequested || audioStatus != "" {
		t.Fatalf("pre-expiry job phase=%s ebook=%s audio=%s", phase, ebookStatus, audioStatus)
	}

	if _, err := service.db.Exec(
		"UPDATE book_request_jobs SET phase_started_at = datetime('now', '-3 minutes') WHERE id = ?", job.ID,
	); err != nil {
		t.Fatal(err)
	}
	forceBookJobDue(t, service, job.ID)
	waitForBookJobCount(t, service, 0)
	upstream.mu.Lock()
	postExpirySearches := upstream.searchCalls
	searchedIDs := append([]int(nil), upstream.searchBookIDs...)
	upstream.mu.Unlock()
	if postExpirySearches != 3 || len(searchedIDs) != 1 || searchedIDs[0] == ebookID {
		t.Fatalf("post-expiry searches=%d ids=%v, want fresh-server revalidation of ebook %d plus new audiobook", postExpirySearches, searchedIDs, ebookID)
	}
	var history int
	if err := service.db.QueryRow(
		"SELECT COUNT(*) FROM request_log WHERE user_id = ? AND foreign_id = ?", userID, upstream.foreignBookID,
	).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if history != 2 {
		t.Fatalf("history rows = %d, want preserved ebook plus resumed audiobook", history)
	}
}

func TestBookRequestWorkerExactSearchFootprintKeepsFullRebindGuard(t *testing.T) {
	for _, acknowledged := range []bool{false, true} {
		name := "unacknowledged"
		if acknowledged {
			name = "acknowledged"
		}
		t.Run(name, func(t *testing.T) {
			upstream := newVerifiedBookUpstream("Exact Search Rebind", "exact-search-rebind")
			bookID := upstream.addExisting(BookFormatAudiobook, true)
			upstream.mu.Lock()
			upstream.author.AudiobookMonitorFuture = true
			upstream.mu.Unlock()
			service, userID := newVerifiedMutationService(t, upstream)
			job, _, instanceID := prepareWorkerTestJob(t, service, userID, upstream, BookFormatAudiobook)
			if err := service.setBookJobPhase(
				job.ID, "search_inflight", BookFormatAudiobook, upstream.author.ID,
				upstream.foreignAuthorID, upstream.authorName, bookID, acknowledged,
			); err != nil {
				t.Fatal(err)
			}
			if _, err := service.db.Exec(
				`UPDATE book_request_jobs SET settings_fingerprint = zeroblob(32),
				 phase_started_at = datetime('now', '-1 minute') WHERE id = ?`,
				job.ID,
			); err != nil {
				t.Fatal(err)
			}
			job, err := service.loadBookRequestJob(job.ID)
			if err != nil {
				t.Fatal(err)
			}
			client, fingerprint, err := service.registry.GetFreshChaptarrClient(instanceID)
			if err != nil {
				t.Fatal(err)
			}
			resumed, err := service.rebindBookJobForFreshInstance(context.Background(), client, job, fingerprint[:])
			if err != nil {
				t.Fatal(err)
			}
			if resumed {
				t.Fatal("exact search footprint bypassed the uncertain-outcome guard")
			}
			var phase string
			var bookIDAfter, stillOldFingerprint int
			if err := service.db.QueryRow(
				`SELECT phase, book_id, settings_fingerprint = zeroblob(32)
				 FROM book_request_jobs WHERE id = ?`, job.ID,
			).Scan(&phase, &bookIDAfter, &stillOldFingerprint); err != nil {
				t.Fatal(err)
			}
			if phase != "search_inflight" || bookIDAfter != bookID || stillOldFingerprint != 1 {
				t.Fatalf("guarded exact search became phase=%s book=%d old_fingerprint=%d", phase, bookIDAfter, stillOldFingerprint)
			}

			if _, err := service.db.Exec(
				"UPDATE book_request_jobs SET phase_started_at = datetime('now', '-3 minutes') WHERE id = ?",
				job.ID,
			); err != nil {
				t.Fatal(err)
			}
			job, err = service.loadBookRequestJob(job.ID)
			if err != nil {
				t.Fatal(err)
			}
			resumed, err = service.rebindBookJobForFreshInstance(context.Background(), client, job, fingerprint[:])
			if err != nil {
				t.Fatal(err)
			}
			if !resumed {
				t.Fatal("expired exact search guard did not reset for fresh preflight")
			}
			var acknowledgedAfter int
			if err := service.db.QueryRow(
				`SELECT phase, book_id, search_acknowledged,
				 settings_fingerprint = ? FROM book_request_jobs WHERE id = ?`,
				fingerprint[:], job.ID,
			).Scan(&phase, &bookIDAfter, &acknowledgedAfter, &stillOldFingerprint); err != nil {
				t.Fatal(err)
			}
			if phase != "queued" || bookIDAfter != 0 || acknowledgedAfter != 0 || stillOldFingerprint != 1 {
				t.Fatalf("expired exact search reset to phase=%s book=%d ack=%d fresh_fingerprint=%d", phase, bookIDAfter, acknowledgedAfter, stillOldFingerprint)
			}
		})
	}
}

func TestBookJobFingerprintRebindPolicy(t *testing.T) {
	tests := []struct {
		name     string
		job      bookRequestJob
		wantWait time.Duration
	}{
		{name: "queued", job: bookRequestJob{Phase: "queued"}},
		{name: "seed", job: bookRequestJob{Phase: "seed_inflight"}, wantWait: defaultBookSeedOutcomeTTL},
		{name: "converging", job: bookRequestJob{Phase: "converging"}, wantWait: defaultBookSeedOutcomeTTL},
		{name: "unacknowledged search", job: bookRequestJob{Phase: "search_inflight", PhaseFormat: BookFormatEbook}, wantWait: defaultBookSearchAckTTL},
		{name: "acknowledged search", job: bookRequestJob{Phase: "search_inflight", PhaseFormat: BookFormatAudiobook, SearchAcknowledged: true}, wantWait: defaultBookSearchAckTTL},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wait, err := bookJobFingerprintRebindPolicy(&tc.job)
			if err != nil {
				t.Fatal(err)
			}
			if wait != tc.wantWait {
				t.Fatalf("policy wait=%s, want %s", wait, tc.wantWait)
			}
		})
	}
}

func TestBookJobRetryDelayIsBoundedExponential(t *testing.T) {
	wants := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	for attempt, want := range wants {
		if got := bookJobRetryDelay(attempt); got != want {
			t.Fatalf("attempt %d delay = %s, want %s", attempt, got, want)
		}
	}
}
