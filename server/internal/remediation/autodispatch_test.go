package remediation

import (
	"context"
	"sync"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/db"
)

func TestConcurrentCircuitBreakerIncrementsAreNotLost(t *testing.T) {
	svc, _, _ := setupTestService(t)
	const workers = 24
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc.bumpAutoGiveupStreak()
		}()
	}
	wg.Wait()
	if got := svc.readAutoGiveupStreak(); got != workers {
		t.Fatalf("concurrent streak = %d, want %d", got, workers)
	}
}

// enableAutoDispatch turns on both the master switch and the auto-dispatch
// sub-toggle with a known circuit-breaker threshold, returning the saved value.
func enableAutoDispatch(t *testing.T, svc *Service, breakerGiveups int) {
	t.Helper()
	s := Defaults()
	s.Enabled = true
	s.AutoDispatch = true
	s.CircuitBreakerGiveups = breakerGiveups
	if _, err := svc.SetSettings(s); err != nil {
		t.Fatalf("enable auto-dispatch: %v", err)
	}
}

// countOpenAutoIssues returns how many open (closed_at IS NULL) auto-sourced
// issues exist — the invariant the dedupe must hold to one per stuck download.
func countOpenAutoIssues(t *testing.T, svc *Service) int {
	t.Helper()
	var n int
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM issues WHERE source = ? AND closed_at IS NULL", SourceAuto).Scan(&n); err != nil {
		t.Fatalf("count open auto issues: %v", err)
	}
	return n
}

// drainJobs counts how many jobs are currently queued (non-blocking), draining
// the channel. Used to assert the Runner was enqueued exactly once for a new
// auto issue and never for a duplicate.
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

func stalledDiagnosis() arr.Diagnosis {
	return arr.Diagnose(arr.QueueSignal{
		TrackedDownloadStatus: "error",
		ErrorMessage:          "The download is stalled with no connections",
	})
}

// TestAutoDispatcherOpensExactlyOneIssueAcrossPolls is the core dedupe guarantee:
// the hub may call OpenAutoIssue once per confirming poll, but the DB partial-
// unique index collapses every repeat for the same stuck download into a SINGLE
// open issue, and the Runner is enqueued only for the first (genuinely new) one.
func TestAutoDispatcherOpensExactlyOneIssueAcrossPolls(t *testing.T) {
	svc, _, _ := setupTestService(t)
	enableAutoDispatch(t, svc, 5)
	ad := NewAutoDispatcher(svc)

	d := stalledDiagnosis()
	// Simulate many confirming polls of the SAME stuck download.
	for i := 0; i < 6; i++ {
		ad.OpenAutoIssue("radarr", "inst1", "stuckHash", arr.QueueMediaContext{}, d)
	}

	if got := countOpenAutoIssues(t, svc); got != 1 {
		t.Fatalf("open auto issues after repeated polls = %d, want exactly 1", got)
	}
	// Re-detecting the same ongoing problem each poll is not a new occurrence, so
	// the counter stays at 1 (it used to climb per poll, which was just a confusing
	// time-open counter).
	var occ int
	if err := svc.db.QueryRow("SELECT occurrences FROM issues WHERE source = ? AND closed_at IS NULL", SourceAuto).Scan(&occ); err != nil {
		t.Fatalf("read occurrences: %v", err)
	}
	if occ != 1 {
		t.Fatalf("occurrences = %d, want 1 (re-polls don't bump)", occ)
	}
	// The Runner was enqueued exactly once (only the create path enqueues).
	if jobs := drainJobs(svc); jobs != 1 {
		t.Fatalf("enqueued jobs = %d, want exactly 1 (only the new issue)", jobs)
	}
}

// TestOpenAutoIssueStoresMediaTypeNotServiceType pins the wire contract: the
// poller reports the *service* type, but the stored media_type must be the
// client-facing 'movie'|'tv' value (a raw "sonarr" made clients render the
// fallback "Movie" label on TV issues).
func TestOpenAutoIssueStoresMediaTypeNotServiceType(t *testing.T) {
	svc, _, _ := setupTestService(t)
	enableAutoDispatch(t, svc, 5)
	ad := NewAutoDispatcher(svc)

	d := stalledDiagnosis()
	ad.OpenAutoIssue("sonarr", "inst1", "tvHash", arr.QueueMediaContext{}, d)
	ad.OpenAutoIssue("radarr", "inst1", "movieHash", arr.QueueMediaContext{}, d)
	drainJobs(svc)

	for downloadID, want := range map[string]string{"tvHash": "tv", "movieHash": "movie"} {
		var got string
		if err := svc.db.QueryRow(
			"SELECT media_type FROM issues WHERE source = ? AND download_id = ?",
			SourceAuto, downloadID,
		).Scan(&got); err != nil {
			t.Fatalf("read media_type for %s: %v", downloadID, err)
		}
		if got != want {
			t.Fatalf("media_type for %s = %q, want %q", downloadID, got, want)
		}
	}
}

func TestOpenAutoIssuePreservesMediaContext(t *testing.T) {
	svc, _, _ := setupTestService(t)
	enableAutoDispatch(t, svc, 5)
	ad := NewAutoDispatcher(svc)
	media := arr.QueueMediaContext{
		QueueID: 77, Title: "Example Series", TmdbID: 1920, TvdbID: 70533,
		SeasonNumber: 1, EpisodeNumber: 1,
	}
	ad.OpenAutoIssue("sonarr", "sonarr-a", "download-a", media, stalledDiagnosis())
	var title string
	var tmdbID, tvdbID, season, episode, queueID int
	if err := svc.db.QueryRow(
		`SELECT title, tmdb_id, tvdb_id, season_number, episode_number, arr_queue_id
		 FROM issues WHERE download_id = 'download-a'`,
	).Scan(&title, &tmdbID, &tvdbID, &season, &episode, &queueID); err != nil {
		t.Fatalf("load auto issue: %v", err)
	}
	if title != "Example Series" || tmdbID != 1920 || tvdbID != 70533 || season != 1 || episode != 1 || queueID != 77 {
		t.Fatalf("stored context = %q/%d/%d S%dE%d queue=%d", title, tmdbID, tvdbID, season, episode, queueID)
	}
}

func TestReconcileAutoIssuesClosesAcrossRestartSnapshot(t *testing.T) {
	svc, _, _ := setupTestService(t)
	enableAutoDispatch(t, svc, 5)
	ad := NewAutoDispatcher(svc)
	ad.OpenAutoIssue("sonarr", "sonarr-a", "gone", arr.QueueMediaContext{}, stalledDiagnosis())
	ad.ReconcileAutoIssues("sonarr", "sonarr-a", nil)
	issue, err := svc.ListIssues(IssueResolved)
	if err != nil || len(issue) != 1 {
		t.Fatalf("resolved issues = %d, err=%v", len(issue), err)
	}
	if issue[0].ResolutionKind != ResolutionArrStateCleared {
		t.Fatalf("resolution_kind = %q, want %q", issue[0].ResolutionKind, ResolutionArrStateCleared)
	}
}

// TestCloseAutoIssueResolvesRecoveredDownload proves that when the poller stops
// flagging a download, the matching open auto issue is resolved and closed.
func TestCloseAutoIssueResolvesRecoveredDownload(t *testing.T) {
	svc, _, _ := setupTestService(t)
	enableAutoDispatch(t, svc, 5)
	ad := NewAutoDispatcher(svc)

	d := stalledDiagnosis()
	ad.OpenAutoIssue("radarr", "inst1", "stuckHash", arr.QueueMediaContext{}, d)
	ad.OpenAutoIssue("radarr", "inst1", "stuckHash", arr.QueueMediaContext{}, d) // dedupe, still one open
	drainJobs(svc)
	if got := countOpenAutoIssues(t, svc); got != 1 {
		t.Fatalf("open auto issues = %d, want 1", got)
	}

	// The download recovered / left the queue → close.
	ad.CloseAutoIssue("radarr", "inst1", "stuckHash")
	if got := countOpenAutoIssues(t, svc); got != 0 {
		t.Fatalf("open auto issues after recovery = %d, want 0", got)
	}
	var n int
	if err := svc.db.QueryRow(
		"SELECT COUNT(*) FROM issues WHERE source = ? AND status = ? AND closed_at IS NOT NULL",
		SourceAuto, IssueResolved,
	).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 1 {
		t.Fatalf("resolved+closed auto issues = %d, want 1", n)
	}

	// A second close for a download with no open issue is a harmless no-op.
	ad.CloseAutoIssue("radarr", "inst1", "stuckHash")
}

// TestAutoDispatcherGatedOff proves the opener is a no-op unless BOTH the master
// switch and the auto-dispatch sub-toggle are on — checked at call time so a live
// toggle takes effect without a restart.
func TestAutoDispatcherGatedOff(t *testing.T) {
	svc, _, _ := setupTestService(t)
	ad := NewAutoDispatcher(svc)
	d := stalledDiagnosis()

	// Default settings: Enabled=false, AutoDispatch=false -> no-op.
	ad.OpenAutoIssue("radarr", "inst1", "h1", arr.QueueMediaContext{}, d)
	if got := countOpenAutoIssues(t, svc); got != 0 {
		t.Fatalf("with feature off, opened %d issue(s), want 0", got)
	}

	// Enabled but AutoDispatch off -> still no-op (the sub-toggle independently
	// gates the poll path).
	s := Defaults()
	s.Enabled = true
	s.AutoDispatch = false
	if _, err := svc.SetSettings(s); err != nil {
		t.Fatalf("set settings: %v", err)
	}
	ad.OpenAutoIssue("radarr", "inst1", "h1", arr.QueueMediaContext{}, d)
	if got := countOpenAutoIssues(t, svc); got != 0 {
		t.Fatalf("with AutoDispatch off, opened %d issue(s), want 0", got)
	}

	// Both on -> opens.
	enableAutoDispatch(t, svc, 5)
	ad.OpenAutoIssue("radarr", "inst1", "h1", arr.QueueMediaContext{}, d)
	if got := countOpenAutoIssues(t, svc); got != 1 {
		t.Fatalf("with both on, opened %d issue(s), want 1", got)
	}
}

// seedAutoIssue inserts an open auto-sourced issue and returns its id, so the
// circuit-breaker tests can drive terminal outcomes through ConcludeIssue.
func seedAutoIssue(t *testing.T, svc *Service, downloadID string) int64 {
	t.Helper()
	res, err := svc.db.Exec(
		"INSERT INTO issues (source, status, media_type, tmdb_id, title, instance_id, download_id, dedupe_key) VALUES (?,?,?,?,?,?,?,?)",
		SourceAuto, IssueOpen, "movie", 0, "Stuck", "inst1", downloadID, downloadID,
	)
	if err != nil {
		t.Fatalf("seed auto issue: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// TestCircuitBreakerDisablesAutoDispatchAfterNGiveups proves that N consecutive
// auto-dispatch give-ups (auto issues concluded non-resolved) flip AutoDispatch
// OFF (persisted) and fire the admin notification, while leaving the master
// Enabled switch untouched. User-reported give-ups never feed the breaker.
func TestCircuitBreakerDisablesAutoDispatchAfterNGiveups(t *testing.T) {
	svc, notif, reporterID := setupTestService(t)
	const threshold = 3
	enableAutoDispatch(t, svc, threshold)
	ctx := context.Background()

	// A user-reported give-up must NOT count toward the breaker.
	userIssue, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{
		InstanceID: testRadarrInstanceID, MediaType: "movie", TmdbID: 99, Category: CategoryOther,
	})
	if err != nil {
		t.Fatalf("create user issue: %v", err)
	}
	if err := svc.ConcludeIssue(ctx, userIssue.IssueID, IssueWontFix, "user give-up"); err != nil {
		t.Fatalf("conclude user issue: %v", err)
	}
	if svc.readAutoGiveupStreak() != 0 {
		t.Fatalf("user give-up bumped the streak to %d, want 0", svc.readAutoGiveupStreak())
	}

	// threshold-1 auto give-ups: AutoDispatch stays ON.
	for i := 0; i < threshold-1; i++ {
		id := seedAutoIssue(t, svc, "hash"+string(rune('a'+i)))
		if err := svc.ConcludeIssue(ctx, id, IssueWontFix, "agent gave up"); err != nil {
			t.Fatalf("conclude auto issue: %v", err)
		}
	}
	if !svc.Settings().AutoDispatch {
		t.Fatalf("AutoDispatch flipped off too early (after %d give-ups, threshold %d)", threshold-1, threshold)
	}
	if svc.readAutoGiveupStreak() != threshold-1 {
		t.Fatalf("streak = %d, want %d", svc.readAutoGiveupStreak(), threshold-1)
	}

	// The threshold-th auto give-up trips the breaker.
	tripID := seedAutoIssue(t, svc, "tripHash")
	if err := svc.ConcludeIssue(ctx, tripID, IssueWontFix, "agent gave up"); err != nil {
		t.Fatalf("conclude tripping issue: %v", err)
	}

	final := svc.Settings()
	if final.AutoDispatch {
		t.Fatalf("AutoDispatch still on after %d consecutive give-ups, want off", threshold)
	}
	if !final.Enabled {
		t.Fatalf("circuit breaker disabled the master Enabled switch, want only AutoDispatch off")
	}
	// The streak reset to 0 after tripping (a clean slate for a re-enable).
	if svc.readAutoGiveupStreak() != 0 {
		t.Fatalf("streak after trip = %d, want 0 (reset)", svc.readAutoGiveupStreak())
	}
	// An admin notification fired for the trip.
	found := false
	for _, e := range notif.adminEvents {
		if e == "remediation_autodispatch_disabled" {
			found = true
		}
	}
	if !found {
		t.Fatalf("admin events = %v, want a remediation_autodispatch_disabled event", notif.adminEvents)
	}
}

// TestCircuitBreakerResetOnResolve proves a successful auto resolution clears the
// give-up streak, so an intermittent problem that the agent sometimes fixes never
// trips the breaker.
func TestCircuitBreakerResetOnResolve(t *testing.T) {
	svc, _, _ := setupTestService(t)
	const threshold = 3
	enableAutoDispatch(t, svc, threshold)
	ctx := context.Background()

	// Two give-ups, then a resolve, then two more give-ups: the resolve resets the
	// streak so the breaker (threshold 3) never trips across this sequence.
	g1 := seedAutoIssue(t, svc, "g1")
	g2 := seedAutoIssue(t, svc, "g2")
	if err := svc.ConcludeIssue(ctx, g1, IssueWontFix, "gave up"); err != nil {
		t.Fatalf("conclude g1: %v", err)
	}
	if err := svc.ConcludeIssue(ctx, g2, IssueWontFix, "gave up"); err != nil {
		t.Fatalf("conclude g2: %v", err)
	}
	if svc.readAutoGiveupStreak() != 2 {
		t.Fatalf("streak before resolve = %d, want 2", svc.readAutoGiveupStreak())
	}

	r1 := seedAutoIssue(t, svc, "r1")
	if err := svc.ConcludeIssue(ctx, r1, IssueResolved, "fixed"); err != nil {
		t.Fatalf("conclude r1 resolved: %v", err)
	}
	if svc.readAutoGiveupStreak() != 0 {
		t.Fatalf("streak after resolve = %d, want 0", svc.readAutoGiveupStreak())
	}

	g3 := seedAutoIssue(t, svc, "g3")
	g4 := seedAutoIssue(t, svc, "g4")
	if err := svc.ConcludeIssue(ctx, g3, IssueWontFix, "gave up"); err != nil {
		t.Fatalf("conclude g3: %v", err)
	}
	if err := svc.ConcludeIssue(ctx, g4, IssueWontFix, "gave up"); err != nil {
		t.Fatalf("conclude g4: %v", err)
	}
	if !svc.Settings().AutoDispatch {
		t.Fatalf("AutoDispatch tripped despite a reset (streak should be 2, threshold 3)")
	}
	if svc.readAutoGiveupStreak() != 2 {
		t.Fatalf("final streak = %d, want 2", svc.readAutoGiveupStreak())
	}
}

// TestConcludeIdempotentDoesNotDoubleCountBreaker proves a double-conclude of the
// same auto issue bumps the give-up streak only once (the second conclude is a
// no-op transition).
func TestConcludeIdempotentDoesNotDoubleCountBreaker(t *testing.T) {
	svc, _, _ := setupTestService(t)
	enableAutoDispatch(t, svc, 5)
	ctx := context.Background()

	id := seedAutoIssue(t, svc, "once")
	if err := svc.ConcludeIssue(ctx, id, IssueWontFix, "gave up"); err != nil {
		t.Fatalf("first conclude: %v", err)
	}
	if err := svc.ConcludeIssue(ctx, id, IssueWontFix, "gave up again"); err != nil {
		t.Fatalf("second conclude (idempotent): %v", err)
	}
	if svc.readAutoGiveupStreak() != 1 {
		t.Fatalf("streak after double-conclude = %d, want 1 (no double count)", svc.readAutoGiveupStreak())
	}
}

// Ensure the in-memory DB schema actually has the columns the seed uses (guards
// against an initSQL drift breaking these tests silently).
func TestAutoIssueSchemaSanity(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if _, err := database.Exec(
		"INSERT INTO issues (source, status, media_type, tmdb_id, title, instance_id, download_id, dedupe_key) VALUES ('auto','open','movie',0,'x','i','d','k')",
	); err != nil {
		t.Fatalf("insert auto issue with dedupe_key: %v", err)
	}
}
