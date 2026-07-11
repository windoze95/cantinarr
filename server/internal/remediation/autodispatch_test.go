package remediation

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/db"
)

func enableAutoDispatch(t *testing.T, svc *Service, breakerGiveups int) {
	t.Helper()
	settings := Defaults()
	settings.Enabled = true
	settings.AutoDispatch = true
	settings.CircuitBreakerGiveups = breakerGiveups
	if _, err := svc.SetSettings(settings); err != nil {
		t.Fatalf("enable auto-dispatch: %v", err)
	}
}

func drainJobs(svc *Service) int {
	n := 0
	for {
		select {
		case <-svc.jobs:
			n++
		default:
			return n
		}
	}
}

func observedProblem(downloadID string, queueID int, sizeLeft float64) arr.QueueObservation {
	signal := arr.QueueSignal{
		TrackedDownloadStatus: "error", TrackedDownloadState: "importPending",
		ErrorMessage: "The download is stalled with no connections", Size: 100, SizeLeft: sizeLeft,
	}
	return arr.QueueObservation{
		DownloadID: downloadID,
		Media:      arr.QueueMediaContext{QueueID: queueID, Title: "Example", TmdbID: 42},
		Signal:     signal, Diagnosis: arr.Diagnose(signal),
	}
}

func TestObservationStartsSilentAndPromotesExactlyOnce(t *testing.T) {
	svc, notifier, _ := setupObservationService(t, false)
	const instanceID = "radarr-observe"
	enableAutoDispatch(t, svc, 5)
	settings := svc.Settings()
	settings.ObservationMinMinutes = 10
	settings.ObservationQuietMinutes = 5
	if _, err := svc.SetSettings(settings); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	item := observedProblem("download-a", 7, 100)
	if err := svc.observeQueueSnapshot("radarr", instanceID, []arr.QueueObservation{item}, base); err != nil {
		t.Fatal(err)
	}
	issues, _ := svc.ListIssues("")
	if len(issues) != 1 || issues[0].Status != IssueObserving || !issues[0].Read {
		t.Fatalf("initial issue = %+v, want silent observing/read", issues)
	}
	if count, _ := svc.OpenIssueCount(); count != 0 {
		t.Fatalf("attention count = %d, want 0", count)
	}
	if len(notifier.adminEvents) != 0 || drainJobs(svc) != 0 {
		t.Fatalf("silent observation emitted events/jobs: %v", notifier.adminEvents)
	}
	if err := svc.observeQueueSnapshot("radarr", instanceID, []arr.QueueObservation{item}, base.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	issue, _ := svc.GetIssue(issues[0].ID)
	if issue.Status != IssueOpen || issue.Read {
		t.Fatalf("promoted issue = %+v", issue)
	}
	if count, _ := svc.OpenIssueCount(); count != 1 {
		t.Fatalf("promoted attention count = %d, want 1", count)
	}
	if len(notifier.adminEvents) != 1 || notifier.adminEvents[0] != "issue_created" || drainJobs(svc) != 1 {
		t.Fatalf("promotion events/jobs = %v", notifier.adminEvents)
	}
	if err := svc.observeQueueSnapshot("radarr", instanceID, []arr.QueueObservation{item}, base.Add(12*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(notifier.adminEvents) != 1 || drainJobs(svc) != 0 {
		t.Fatalf("repeat poll re-promoted: events=%v", notifier.adminEvents)
	}
}

func TestReplacementStaysOneSilentRecoveringIncident(t *testing.T) {
	svc, notifier, _ := setupTestService(t)
	enableAutoDispatch(t, svc, 5)
	base := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	problem := observedProblem("old", 7, 100)
	if err := svc.observeQueueSnapshot("radarr", testRadarrInstanceID, []arr.QueueObservation{problem}, base); err != nil {
		t.Fatal(err)
	}
	replacement := problem
	replacement.DownloadID = "replacement"
	replacement.Media.QueueID = 8
	replacement.Signal = arr.QueueSignal{Status: "downloading", TrackedDownloadStatus: "ok", Size: 100, SizeLeft: 80}
	replacement.Diagnosis = arr.Diagnose(replacement.Signal)
	if err := svc.observeQueueSnapshot("radarr", testRadarrInstanceID, []arr.QueueObservation{replacement}, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	issues, _ := svc.ListIssues("")
	if len(issues) != 1 || issues[0].Status != IssueRecovering || issues[0].DownloadID != "replacement" {
		t.Fatalf("replacement incident = %+v", issues)
	}
	if len(notifier.adminEvents) != 0 || drainJobs(svc) != 0 {
		t.Fatalf("recovery emitted attention: %v", notifier.adminEvents)
	}
}

func TestDispatcherCoalescesToNewestCompleteSnapshotPerInstance(t *testing.T) {
	svc, _, _ := setupTestService(t)
	dispatcher := NewAutoDispatcher(svc)
	old := observedProblem("old", 1, 100)
	newer := old
	newer.DownloadID = "replacement"
	newer.Signal = arr.QueueSignal{Status: "downloading", TrackedDownloadStatus: "ok", Size: 100, SizeLeft: 75}
	newer.Diagnosis = arr.Diagnose(newer.Signal)
	dispatcher.ObserveQueueSnapshot("radarr", testRadarrInstanceID, []arr.QueueObservation{old})
	dispatcher.ObserveQueueSnapshot("radarr", testRadarrInstanceID, []arr.QueueObservation{newer})
	dispatcher.snapshotMu.Lock()
	defer dispatcher.snapshotMu.Unlock()
	if len(dispatcher.pendingSnapshots) != 1 {
		t.Fatalf("pending snapshots=%d, want one latest value", len(dispatcher.pendingSnapshots))
	}
	pending := dispatcher.pendingSnapshots["radarr\x00"+testRadarrInstanceID]
	if len(pending) != 1 {
		t.Fatalf("pending event sequence=%d, want one newest success", len(pending))
	}
	got := pending[0]
	if len(got.items) != 1 || got.items[0].DownloadID != "replacement" {
		t.Fatalf("coalesced snapshot=%+v, want newest recovery evidence", got)
	}
}

func TestDispatcherPreservesSuccessResetBeforeLatestFailure(t *testing.T) {
	svc, _, _ := setupTestService(t)
	dispatcher := NewAutoDispatcher(svc)
	now := time.Now().UTC()
	dispatcher.enqueueSnapshotJob(queueSnapshotJob{serviceType: "radarr", instanceID: testRadarrInstanceID, failure: context.DeadlineExceeded, observedAt: now})
	dispatcher.enqueueSnapshotJob(queueSnapshotJob{serviceType: "radarr", instanceID: testRadarrInstanceID, items: []arr.QueueObservation{}, observedAt: now.Add(time.Second)})
	dispatcher.enqueueSnapshotJob(queueSnapshotJob{serviceType: "radarr", instanceID: testRadarrInstanceID, failure: context.DeadlineExceeded, observedAt: now.Add(2 * time.Second)})
	dispatcher.snapshotMu.Lock()
	defer dispatcher.snapshotMu.Unlock()
	pending := dispatcher.pendingSnapshots["radarr\x00"+testRadarrInstanceID]
	if len(pending) != 2 || pending[0].failure != nil || pending[1].failure == nil {
		t.Fatalf("pending sequence=%+v, want success reset then latest failure", pending)
	}
}

func TestDispatcherCoalescesByObservationTimeNotArrivalOrder(t *testing.T) {
	svc, _, _ := setupTestService(t)
	dispatcher := NewAutoDispatcher(svc)
	base := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	newer := observedProblem("newer", 1, 100)
	older := observedProblem("older", 1, 100)

	// Simulate a newer success/failure already queued, followed by an older slow
	// success and failure arriving late. The older arrivals must not replace or
	// discard either newer event before the DB watermark sees them.
	dispatcher.enqueueSnapshotJob(queueSnapshotJob{
		serviceType: "radarr", instanceID: testRadarrInstanceID,
		items: []arr.QueueObservation{newer}, observedAt: base.Add(2 * time.Second),
	})
	dispatcher.enqueueSnapshotJob(queueSnapshotJob{
		serviceType: "radarr", instanceID: testRadarrInstanceID,
		failure: context.DeadlineExceeded, observedAt: base.Add(3 * time.Second),
	})
	dispatcher.enqueueSnapshotJob(queueSnapshotJob{
		serviceType: "radarr", instanceID: testRadarrInstanceID,
		items: []arr.QueueObservation{older}, observedAt: base.Add(time.Second),
	})
	dispatcher.enqueueSnapshotJob(queueSnapshotJob{
		serviceType: "radarr", instanceID: testRadarrInstanceID,
		failure: context.Canceled, observedAt: base,
	})

	dispatcher.snapshotMu.Lock()
	defer dispatcher.snapshotMu.Unlock()
	pending := dispatcher.pendingSnapshots["radarr\x00"+testRadarrInstanceID]
	if len(pending) != 2 || pending[0].failure != nil || pending[1].failure == nil {
		t.Fatalf("pending sequence=%+v, want newer success then newer failure", pending)
	}
	if len(pending[0].items) != 1 || pending[0].items[0].DownloadID != "newer" ||
		!pending[0].observedAt.Equal(base.Add(2*time.Second)) ||
		!pending[1].observedAt.Equal(base.Add(3*time.Second)) {
		t.Fatalf("out-of-order arrivals regressed pending evidence: %+v", pending)
	}
}

func TestObservationWatermarkRejectsOlderSuccessAndFailure(t *testing.T) {
	svc, _, _ := setupTestService(t)
	base := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	newer := base.Add(2 * time.Minute)
	if err := svc.observeQueueSnapshot("radarr", testRadarrInstanceID, nil, newer); err != nil {
		t.Fatal(err)
	}
	svc.noteObservationFailure("radarr", testRadarrInstanceID, context.DeadlineExceeded, base.Add(time.Minute))
	var failures int
	_ = svc.db.QueryRow("SELECT COUNT(*) FROM remediation_observation_failures WHERE instance_id=?", testRadarrInstanceID).Scan(&failures)
	if failures != 0 {
		t.Fatalf("older failure overwrote newer success")
	}

	latest := base.Add(3 * time.Minute)
	svc.noteObservationFailure("radarr", testRadarrInstanceID, context.DeadlineExceeded, latest)
	if err := svc.observeQueueSnapshot("radarr", testRadarrInstanceID, []arr.QueueObservation{observedProblem("old", 1, 100)}, newer.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	var failedAt time.Time
	if err := svc.db.QueryRow("SELECT last_failed_at FROM remediation_observation_failures WHERE instance_id=?", testRadarrInstanceID).Scan(&failedAt); err != nil {
		t.Fatal(err)
	}
	if !failedAt.Equal(latest) {
		t.Fatalf("older success cleared newer failure: failed_at=%s want=%s", failedAt, latest)
	}
}

func TestConcurrentCircuitBreakerIncrementsAreNotLost(t *testing.T) {
	svc, _, _ := setupTestService(t)
	const workers = 24
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); svc.bumpAutoGiveupStreak() }()
	}
	wg.Wait()
	if got := svc.readAutoGiveupStreak(); got != workers {
		t.Fatalf("concurrent streak = %d, want %d", got, workers)
	}
}

func seedAutoIssue(t *testing.T, svc *Service, downloadID string) int64 {
	t.Helper()
	res, err := svc.db.Exec(
		"INSERT INTO issues (source,status,media_type,tmdb_id,title,instance_id,download_id,dedupe_key) VALUES (?,?,?,?,?,?,?,?)",
		SourceAuto, IssueOpen, "movie", 0, "Stuck", "inst1", downloadID, downloadID,
	)
	if err != nil {
		t.Fatalf("seed auto issue: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestCircuitBreakerDisablesAutoDispatchAfterNGiveups(t *testing.T) {
	svc, notifier, reporterID := setupTestService(t)
	enableAutoDispatch(t, svc, 3)
	ctx := context.Background()
	userIssue, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{InstanceID: testRadarrInstanceID, MediaType: "movie", TmdbID: 99, Category: CategoryOther})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ConcludeIssue(ctx, userIssue.IssueID, IssueWontFix, "user give-up"); err != nil {
		t.Fatal(err)
	}
	if svc.readAutoGiveupStreak() != 0 {
		t.Fatal("user issue fed auto breaker")
	}
	for i := 0; i < 3; i++ {
		if err := svc.ConcludeIssue(ctx, seedAutoIssue(t, svc, string(rune('a'+i))), IssueWontFix, "gave up"); err != nil {
			t.Fatal(err)
		}
	}
	if svc.Settings().AutoDispatch || !svc.Settings().Enabled || svc.readAutoGiveupStreak() != 0 {
		t.Fatalf("breaker state = %+v streak=%d", svc.Settings(), svc.readAutoGiveupStreak())
	}
	found := false
	for _, event := range notifier.adminEvents {
		if event == "remediation_autodispatch_disabled" {
			found = true
		}
	}
	if !found {
		t.Fatalf("events=%v", notifier.adminEvents)
	}
}

func TestCircuitBreakerResetAndIdempotence(t *testing.T) {
	svc, _, _ := setupTestService(t)
	enableAutoDispatch(t, svc, 5)
	ctx := context.Background()
	first := seedAutoIssue(t, svc, "first")
	if err := svc.ConcludeIssue(ctx, first, IssueWontFix, "gave up"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ConcludeIssue(ctx, first, IssueWontFix, "again"); err != nil {
		t.Fatal(err)
	}
	if svc.readAutoGiveupStreak() != 1 {
		t.Fatal("double conclude double-counted")
	}
	if err := svc.ConcludeIssue(ctx, seedAutoIssue(t, svc, "fixed"), IssueResolved, "fixed"); err != nil {
		t.Fatal(err)
	}
	if svc.readAutoGiveupStreak() != 0 {
		t.Fatal("resolution did not reset breaker")
	}
}

func TestAutoIssueSchemaSanity(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("INSERT INTO issues (source,status,media_type,tmdb_id,title,instance_id,download_id,dedupe_key) VALUES ('auto','observing','movie',0,'x','i','d','k')"); err != nil {
		t.Fatal(err)
	}
}
