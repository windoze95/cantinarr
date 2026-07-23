package request

import (
	"context"
	"net/http"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

func TestRecoveryAuthFailureRespectsInflightSafetyGuard(t *testing.T) {
	tests := []struct {
		name       string
		phase      string
		format     string
		statusCode int
	}{
		{name: "seed unauthorized", phase: "seed_inflight", format: BookFormatEbook, statusCode: http.StatusUnauthorized},
		{name: "search forbidden", phase: "search_inflight", format: BookFormatAudiobook, statusCode: http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newVerifiedBookUpstream("Guarded Recovery Auth", "guarded-recovery-auth-"+tc.phase)
			bookID := 0
			if tc.phase == "search_inflight" {
				bookID = upstream.addExisting(tc.format, true)
			}
			service, userID := newVerifiedMutationService(t, upstream)
			job, _, instanceID := prepareWorkerTestJob(t, service, userID, upstream, tc.format)
			if err := service.setBookJobPhase(
				job.ID, tc.phase, tc.format, upstream.author.ID,
				upstream.foreignAuthorID, upstream.authorName, bookID, false,
			); err != nil {
				t.Fatal(err)
			}
			authFailure := &chaptarr.HTTPStatusError{
				Method: http.MethodGet, Path: "/api/v1/book", StatusCode: tc.statusCode,
			}

			if retained := service.deferDirectBookJob(job.ID, authFailure); !retained {
				t.Fatal("fresh inflight GET authentication failure released uncertain work")
			}
			var state, code string
			if err := service.db.QueryRow(
				"SELECT state, last_error_code FROM book_request_jobs WHERE id = ?", job.ID,
			).Scan(&state, &code); err != nil {
				t.Fatal(err)
			}
			if state != "outcome_unknown" {
				t.Fatalf("fresh auth recovery = %s/%s, want outcome_unknown", state, code)
			}
			active, err := service.hasActiveBookRequestJob(instanceID, upstream.foreignBookID)
			if err != nil {
				t.Fatal(err)
			}
			if !active {
				t.Fatal("fresh inflight auth failure did not retain title ownership")
			}

			if _, err := service.db.Exec(
				`UPDATE book_request_jobs SET created_at = datetime('now', '-31 minutes'),
				 phase_started_at = datetime('now', '-31 minutes') WHERE id = ?`, job.ID,
			); err != nil {
				t.Fatal(err)
			}
			if retained := service.deferDirectBookJob(job.ID, authFailure); retained {
				t.Fatal("expired inflight GET authentication failure retained title ownership")
			}
			if err := service.db.QueryRow(
				"SELECT state, last_error_code FROM book_request_jobs WHERE id = ?", job.ID,
			).Scan(&state, &code); err != nil {
				t.Fatal(err)
			}
			if state != "failed" || code != "book_request_unverified" {
				t.Fatalf("expired auth recovery = %s/%s, want failed/book_request_unverified", state, code)
			}
			active, err = service.hasActiveBookRequestJob(instanceID, upstream.foreignBookID)
			if err != nil {
				t.Fatal(err)
			}
			if active {
				t.Fatal("expired inflight authentication failure still owns the title")
			}
		})
	}
}

func TestOldJobWithFreshInflightPhaseIsNotHotClaimed(t *testing.T) {
	tests := []struct {
		name   string
		phase  string
		format string
	}{
		{name: "seed", phase: "seed_inflight", format: BookFormatEbook},
		{name: "unacknowledged search", phase: "search_inflight", format: BookFormatAudiobook},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newVerifiedBookUpstream("Fresh Phase Guard", "fresh-phase-guard-"+tc.phase)
			bookID := 0
			if tc.phase == "search_inflight" {
				bookID = upstream.addExisting(tc.format, true)
				upstream.mu.Lock()
				upstream.searchCalls = 1
				upstream.mu.Unlock()
			}
			service, userID := newVerifiedMutationService(t, upstream)
			job, _, _ := prepareWorkerTestJob(t, service, userID, upstream, tc.format)
			if err := service.setBookJobPhase(
				job.ID, tc.phase, tc.format, upstream.author.ID,
				upstream.foreignAuthorID, upstream.authorName, bookID, false,
			); err != nil {
				t.Fatal(err)
			}
			if _, err := service.db.Exec(
				`UPDATE book_request_jobs SET created_at = datetime('now', '-31 minutes'),
				 phase_started_at = CURRENT_TIMESTAMP, attempt_count = 70,
				 state = 'outcome_unknown', next_attempt_at = datetime('now', '+1 day') WHERE id = ?`, job.ID,
			); err != nil {
				t.Fatal(err)
			}
			service.bookWorkerGeneration = newBookWorkerGeneration()
			service.runDueBookRequestJobs(context.Background())

			var state, phase string
			var attempts int
			if err := service.db.QueryRow(
				"SELECT state, phase, attempt_count FROM book_request_jobs WHERE id = ?", job.ID,
			).Scan(&state, &phase, &attempts); err != nil {
				t.Fatal(err)
			}
			if state != "outcome_unknown" || phase != tc.phase || attempts != 70 {
				t.Fatalf("fresh %s guard became %s/%s attempts=%d", tc.phase, state, phase, attempts)
			}
			upstream.mu.Lock()
			seedCalls, searchCalls := len(upstream.seedBodies), upstream.searchCalls
			upstream.mu.Unlock()
			wantSearchCalls := 0
			if tc.phase == "search_inflight" {
				wantSearchCalls = 1
			}
			if seedCalls != 0 || searchCalls != wantSearchCalls {
				t.Fatalf("fresh %s guard mutated Chaptarr: seeds=%d searches=%d", tc.phase, seedCalls, searchCalls)
			}
		})
	}
}

func TestAcknowledgedSearchIsClaimedAtLifetimeWithoutGuardDelay(t *testing.T) {
	upstream := newVerifiedBookUpstream("Acknowledged Immediate Expiry", "acknowledged-immediate-expiry")
	bookID := upstream.addExisting(BookFormatAudiobook, true)
	upstream.mu.Lock()
	upstream.author.AudiobookMonitorFuture = true
	upstream.searchCalls = 1
	upstream.searchBookIDs = []int{bookID}
	upstream.mu.Unlock()
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, _ := prepareWorkerTestJob(t, service, userID, upstream, BookFormatAudiobook)
	if err := service.setBookJobPhase(
		job.ID, "search_inflight", BookFormatAudiobook, upstream.author.ID,
		upstream.foreignAuthorID, upstream.authorName, bookID, true,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(
		`UPDATE book_request_jobs SET created_at = datetime('now', '-31 minutes'),
		 phase_started_at = CURRENT_TIMESTAMP, state = 'outcome_unknown', attempt_count = 20,
		 next_attempt_at = datetime('now', '+1 day') WHERE id = ?`, job.ID,
	); err != nil {
		t.Fatal(err)
	}
	service.bookWorkerGeneration = newBookWorkerGeneration()
	service.runDueBookRequestJobs(context.Background())
	waitForBookJobCount(t, service, 0)

	upstream.mu.Lock()
	searchCalls := upstream.searchCalls
	upstream.mu.Unlock()
	if searchCalls != 1 {
		t.Fatalf("acknowledged expired search replayed %d times, want original only", searchCalls)
	}
	var requested int
	if err := service.db.QueryRow(
		`SELECT COUNT(*) FROM request_log
		 WHERE user_id = ? AND foreign_id = ? AND book_format = ? AND status = ?`,
		userID, upstream.foreignBookID, BookFormatAudiobook, StatusRequested,
	).Scan(&requested); err != nil {
		t.Fatal(err)
	}
	if requested != 1 {
		t.Fatalf("acknowledged lifetime claim materialized %d requested rows, want 1", requested)
	}
}

func TestExpiredAcknowledgedSearchWithMissingTargetNeverSeeds(t *testing.T) {
	upstream := newVerifiedBookUpstream("Missing Acknowledged Target", "missing-acknowledged-target")
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, instanceID := prepareWorkerTestJob(t, service, userID, upstream, BookFormatAudiobook)
	if err := service.setBookJobPhase(
		job.ID, "search_inflight", BookFormatAudiobook, upstream.author.ID,
		upstream.foreignAuthorID, upstream.authorName, 40404, true,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(
		`UPDATE book_request_jobs SET created_at = datetime('now', '-31 minutes'),
		 phase_started_at = CURRENT_TIMESTAMP, state = 'outcome_unknown',
		 next_attempt_at = datetime('now', '+1 day') WHERE id = ?`, job.ID,
	); err != nil {
		t.Fatal(err)
	}
	service.bookWorkerGeneration = newBookWorkerGeneration()
	service.runDueBookRequestJobs(context.Background())

	var state, code, audiobookStatus string
	if err := service.db.QueryRow(
		`SELECT state, last_error_code, audiobook_status
		 FROM book_request_jobs WHERE id = ?`, job.ID,
	).Scan(&state, &code, &audiobookStatus); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || code != "book_request_unverified" || audiobookStatus != StatusRequested {
		t.Fatalf("missing acknowledged target = %s/%s checkpoint=%s", state, code, audiobookStatus)
	}
	active, err := service.hasActiveBookRequestJob(instanceID, upstream.foreignBookID)
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("missing acknowledged target still owns the title")
	}
	upstream.mu.Lock()
	seedCalls, searchCalls := len(upstream.seedBodies), upstream.searchCalls
	upstream.mu.Unlock()
	if seedCalls != 0 || searchCalls != 0 {
		t.Fatalf("missing acknowledged target mutated Chaptarr: seeds=%d searches=%d", seedCalls, searchCalls)
	}
}

func TestExpiredUnacknowledgedSearchStopsWithoutReplay(t *testing.T) {
	tests := []struct {
		name          string
		invalidTarget bool
	}{
		{name: "no evidence"},
		{name: "invalid target", invalidTarget: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newVerifiedBookUpstream("Expired Unacknowledged Search", "expired-unack-search-"+tc.name)
			bookID := upstream.addExisting(BookFormatAudiobook, true)
			upstream.mu.Lock()
			upstream.author.AudiobookMonitorFuture = true
			upstream.searchCalls = 1
			upstream.searchBookIDs = []int{bookID}
			upstream.mu.Unlock()
			guardedBookID := bookID
			if tc.invalidTarget {
				guardedBookID += 10000
			}
			service, userID := newVerifiedMutationService(t, upstream)
			job, _, instanceID := prepareWorkerTestJob(t, service, userID, upstream, BookFormatAudiobook)
			if err := service.setBookJobPhase(
				job.ID, "search_inflight", BookFormatAudiobook, upstream.author.ID,
				upstream.foreignAuthorID, upstream.authorName, guardedBookID, false,
			); err != nil {
				t.Fatal(err)
			}
			if _, err := service.db.Exec(
				`UPDATE book_request_jobs SET created_at = datetime('now', '-31 minutes'),
				 phase_started_at = datetime('now', '-3 minutes'), state = 'outcome_unknown', attempt_count = 70,
				 next_attempt_at = datetime('now', '+1 day') WHERE id = ?`, job.ID,
			); err != nil {
				t.Fatal(err)
			}
			service.bookWorkerGeneration = newBookWorkerGeneration()
			service.runDueBookRequestJobs(context.Background())

			var state, code string
			var attempts int
			if err := service.db.QueryRow(
				"SELECT state, last_error_code, attempt_count FROM book_request_jobs WHERE id = ?", job.ID,
			).Scan(&state, &code, &attempts); err != nil {
				t.Fatal(err)
			}
			if state != "failed" || code != "book_request_unverified" || attempts != 71 {
				t.Fatalf("expired unacknowledged search = %s/%s attempts=%d", state, code, attempts)
			}
			active, err := service.hasActiveBookRequestJob(instanceID, upstream.foreignBookID)
			if err != nil {
				t.Fatal(err)
			}
			if active {
				t.Fatal("expired unacknowledged search still owns the title")
			}
			upstream.mu.Lock()
			searchCalls := upstream.searchCalls
			upstream.mu.Unlock()
			if searchCalls != 1 {
				t.Fatalf("expired unacknowledged search replayed %d times, want original only", searchCalls)
			}

			response, err := service.CreateMediaRequest(userID, &CreateRequest{
				MediaType: "book", ForeignID: upstream.foreignBookID,
				Title: upstream.title, BookFormat: BookFormatAudiobook,
			})
			if err != nil {
				t.Fatalf("explicit search retry: %v", err)
			}
			if response.Status != StatusRequested {
				t.Fatalf("explicit search retry status=%s, want requested", response.Status)
			}
			upstream.mu.Lock()
			searchCalls = upstream.searchCalls
			seedCalls := len(upstream.seedBodies)
			upstream.mu.Unlock()
			if searchCalls != 2 || seedCalls != 0 {
				t.Fatalf("explicit search retry mutated searches=%d seeds=%d, want 2/0", searchCalls, seedCalls)
			}
		})
	}
}

func TestExpiredConvergingAbsentTargetFailsWithoutSeed(t *testing.T) {
	upstream := newVerifiedBookUpstream("Expired Absent Convergence", "expired-absent-convergence")
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, instanceID := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
	if err := service.setBookJobPhase(
		job.ID, "converging", BookFormatEbook, upstream.author.ID,
		upstream.foreignAuthorID, upstream.authorName, 0, false,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(
		`UPDATE book_request_jobs SET created_at = datetime('now', '-31 minutes'),
		 state = 'retry_wait', next_attempt_at = datetime('now', '+1 day') WHERE id = ?`, job.ID,
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
		t.Fatalf("expired absent convergence = %s/%s, want failed/book_request_unverified", state, code)
	}
	active, err := service.hasActiveBookRequestJob(instanceID, upstream.foreignBookID)
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("expired absent convergence still owns the title")
	}
	upstream.mu.Lock()
	seedCalls, searchCalls := len(upstream.seedBodies), upstream.searchCalls
	upstream.mu.Unlock()
	if seedCalls != 0 || searchCalls != 0 {
		t.Fatalf("expired absent convergence mutated Chaptarr: seeds=%d searches=%d", seedCalls, searchCalls)
	}
}

func TestExpiredQueuedJobStopsBeforeMutation(t *testing.T) {
	upstream := newVerifiedBookUpstream("Expired Queued Request", "expired-queued-request")
	service, userID := newVerifiedMutationService(t, upstream)
	job, _, instanceID := prepareWorkerTestJob(t, service, userID, upstream, BookFormatEbook)
	if _, err := service.db.Exec(
		`UPDATE book_request_jobs SET created_at = datetime('now', '-31 minutes'),
		 state = 'retry_wait', attempt_count = 40,
		 next_attempt_at = datetime('now', '+1 day') WHERE id = ?`, job.ID,
	); err != nil {
		t.Fatal(err)
	}
	service.bookWorkerGeneration = newBookWorkerGeneration()
	service.runDueBookRequestJobs(context.Background())

	var state, code string
	var attempts int
	if err := service.db.QueryRow(
		"SELECT state, last_error_code, attempt_count FROM book_request_jobs WHERE id = ?", job.ID,
	).Scan(&state, &code, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || code != "book_request_unverified" || attempts != 41 {
		t.Fatalf("expired queued job = %s/%s attempts=%d", state, code, attempts)
	}
	active, err := service.hasActiveBookRequestJob(instanceID, upstream.foreignBookID)
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("expired queued job still owns the title")
	}
	upstream.mu.Lock()
	seedCalls, searchCalls := len(upstream.seedBodies), upstream.searchCalls
	upstream.mu.Unlock()
	if seedCalls != 0 || searchCalls != 0 {
		t.Fatalf("expired queued job mutated Chaptarr: seeds=%d searches=%d", seedCalls, searchCalls)
	}
}
