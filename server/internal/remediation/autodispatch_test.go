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
	issues, _, _ := svc.ListIssues("", 0)
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
	deliverIssueAlerts(svc, base.Add(11*time.Minute))
	if len(notifier.adminEvents) != 1 || notifier.adminEvents[0] != "issue_created" || drainJobs(svc) != 1 {
		t.Fatalf("promotion events/jobs = %v", notifier.adminEvents)
	}
	if err := svc.observeQueueSnapshot("radarr", instanceID, []arr.QueueObservation{item}, base.Add(12*time.Minute)); err != nil {
		t.Fatal(err)
	}
	deliverIssueAlerts(svc, base.Add(12*time.Minute))
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
	issues, _, _ := svc.ListIssues("", 0)
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

// Two reads inside one wall-clock tick carry the SAME observedAt: snapshots are
// stamped with time.Now().UTC(), and .UTC() strips Go's monotonic reading, so
// the clock cannot separate them. The newer snapshot must still win — it is the
// recovery evidence the coalescer exists to preserve. Frozen clock, because the
// real one only produces this tie by luck.
func TestDispatcherKeepsNewestSnapshotWhenTimestampsTie(t *testing.T) {
	svc, _, _ := setupTestService(t)
	dispatcher := NewAutoDispatcher(svc)
	frozen := time.Date(2026, 8, 2, 4, 0, 0, 0, time.UTC)
	dispatcher.now = func() time.Time { return frozen }

	stale := observedProblem("old", 1, 100)
	recovered := stale
	recovered.DownloadID = "replacement"
	recovered.Signal = arr.QueueSignal{Status: "downloading", TrackedDownloadStatus: "ok", Size: 100, SizeLeft: 75}
	recovered.Diagnosis = arr.Diagnose(recovered.Signal)

	dispatcher.ObserveQueueSnapshot("radarr", testRadarrInstanceID, []arr.QueueObservation{stale})
	dispatcher.ObserveQueueSnapshot("radarr", testRadarrInstanceID, []arr.QueueObservation{recovered})

	dispatcher.snapshotMu.Lock()
	defer dispatcher.snapshotMu.Unlock()
	pending := dispatcher.pendingSnapshots["radarr\x00"+testRadarrInstanceID]
	if len(pending) != 1 || len(pending[0].items) != 1 || pending[0].items[0].DownloadID != "replacement" {
		t.Fatalf("tied timestamps kept the stale snapshot: %+v", pending)
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

// Autonomy never stands down silently: the breaker trip opens ONE durable
// admin issue and re-enabling auto-dispatch is what closes it.
func TestBreakerTripOpensDurableIssueAndReenableResolves(t *testing.T) {
	svc, _, _ := setupTestService(t)
	if _, err := svc.SetSettings(Settings{Enabled: true, AutoDispatch: true, Mode: ModeSupervised}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	svc.tripCircuitBreaker(5, 5)

	var issueID int64
	var status string
	if err := svc.db.QueryRow(
		"SELECT id, status FROM issues WHERE dedupe_key = ? AND closed_at IS NULL",
		autoDispatchBreakerDedupeKey,
	).Scan(&issueID, &status); err != nil {
		t.Fatalf("breaker issue missing: %v", err)
	}
	if status != IssueNeedsAdmin {
		t.Fatalf("breaker issue status = %q, want %q", status, IssueNeedsAdmin)
	}
	if svc.Settings().AutoDispatch {
		t.Fatalf("auto-dispatch still on after trip")
	}

	cur := svc.Settings()
	cur.AutoDispatch = true
	if _, err := svc.SetSettings(cur); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	var kind string
	var closed bool
	if err := svc.db.QueryRow(
		"SELECT resolution_kind, closed_at IS NOT NULL FROM issues WHERE id = ?", issueID,
	).Scan(&kind, &closed); err != nil {
		t.Fatalf("read breaker issue: %v", err)
	}
	if !closed || kind == "" {
		t.Fatalf("breaker issue after re-enable = closed %v kind %q, want auto-resolved", closed, kind)
	}
}

// The boot repair's rule pause is announced exactly once at worker start —
// the last silent stand-down.
func TestBootPausedRulesAnnounceOnce(t *testing.T) {
	svc, notif, _ := setupTestService(t)
	if _, err := svc.db.Exec(
		`INSERT INTO agent_approval_rules (problem_kind, action_kind, action_facet, status, paused_reason, paused_at, created_at, updated_at)
		 VALUES ('Download stalled', 'remediate_queue', 'blocklist_search', 'paused',
		         'Cantinarr restarted while an auto-approved fix was executing; verify the arr state before re-arming this rule.',
		         CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("seed boot-paused rule: %v", err)
	}
	svc.announceBootPausedRules()
	found := 0
	for _, event := range notif.adminEvents {
		if event == "agent_autoapproval_paused" {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("boot pause announcements = %d, want exactly 1", found)
	}
}

// TestStalledIncidentWaitsOutTrackerWarmup pins the dwell that issue 859
// (2026-08-13) lacked: a torrent flagged "stalled" 34 seconds after its grab
// became an incident, promoted at 10 minutes, and a standing rule destroyed the
// only release in existence before the torrent ever had a chance to find a
// peer. "Stalled" from a torrent client means "no data moving right now" —
// which is every torrent during tracker warmup — so a young stalled flag must
// not be able to open the incident that starts that pipeline.
func TestStalledIncidentWaitsOutTrackerWarmup(t *testing.T) {
	base := time.Date(2026, 8, 13, 13, 53, 0, 0, time.UTC)

	observe := func(t *testing.T, svc *Service, added *time.Time, at time.Time) {
		t.Helper()
		item := observedProblem("stall-dwell", 7, 100)
		item.AddedAt = added
		if err := svc.observeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{item}, at); err != nil {
			t.Fatal(err)
		}
	}
	issueCount := func(t *testing.T, svc *Service) int {
		t.Helper()
		issues, _, err := svc.ListIssues("", 0)
		if err != nil {
			t.Fatal(err)
		}
		return len(issues)
	}

	t.Run("young stalled is not an incident yet", func(t *testing.T) {
		svc, _, _ := setupObservationService(t, false)
		enableAutoDispatch(t, svc, 5)
		added := base
		// 34 seconds after the grab — 859's exact shape.
		observe(t, svc, &added, base.Add(34*time.Second))
		if n := issueCount(t, svc); n != 0 {
			t.Fatalf("issues after a 34s-old stalled flag = %d, want 0 (tracker warmup is not an incident)", n)
		}
		// Still stalled past the dwell: the same download now IS an incident.
		// Nothing was lost by waiting — a stalled torrent makes no progress.
		observe(t, svc, &added, base.Add(stalledIncidentDwell+time.Minute))
		if n := issueCount(t, svc); n != 1 {
			t.Fatalf("issues after outlasting the dwell = %d, want 1 (a genuinely dead torrent must still surface)", n)
		}
	})

	t.Run("an unknown added time keeps today's behavior", func(t *testing.T) {
		svc, _, _ := setupObservationService(t, false)
		enableAutoDispatch(t, svc, 5)
		// All three arrs supply `added`; nil is the degenerate case, and
		// inventing a birth time would suppress real incidents on evidence we
		// do not have.
		observe(t, svc, nil, base)
		if n := issueCount(t, svc); n != 1 {
			t.Fatalf("issues for a stalled item with no added time = %d, want 1", n)
		}
	})

	t.Run("a different problem in the same group still opens the incident", func(t *testing.T) {
		svc, _, _ := setupObservationService(t, false)
		enableAutoDispatch(t, svc, 5)
		added := base
		young := observedProblem("stall-dwell", 7, 100)
		young.AddedAt = &added
		hard := arr.QueueSignal{
			TrackedDownloadStatus: "error",
			ErrorMessage:          "qBittorrent is reporting an error",
			Size:                  100, SizeLeft: 100,
		}
		sibling := arr.QueueObservation{
			DownloadID: "stall-dwell",
			AddedAt:    &added,
			Media:      arr.QueueMediaContext{QueueID: 7, Title: "Example", TmdbID: 42},
			Signal:     hard, Diagnosis: arr.Diagnose(hard),
		}
		if err := svc.observeQueueSnapshot("radarr", "radarr-observe",
			[]arr.QueueObservation{young, sibling}, base.Add(34*time.Second)); err != nil {
			t.Fatal(err)
		}
		if n := issueCount(t, svc); n != 1 {
			t.Fatalf("issues with a hard client error alongside a young stall = %d, want 1 (the dwell gates only the stalled signal)", n)
		}
	})
}
