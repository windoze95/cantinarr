package remediation

import (
	"fmt"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/arr"
)

// recoveringObservation is the same exact scope as observedProblem, but with a
// signal the observer reads as the arr making progress — the transition that
// returns a promoted incident to passive tracking.
func recoveringObservation(downloadID string, queueID, tmdbID int) arr.QueueObservation {
	signal := arr.QueueSignal{
		TrackedDownloadStatus: "ok", TrackedDownloadState: "downloading",
		Size: 100, SizeLeft: 40,
	}
	return arr.QueueObservation{
		DownloadID: downloadID,
		Media:      arr.QueueMediaContext{QueueID: queueID, Title: "Example", TmdbID: tmdbID},
		Signal:     signal, Diagnosis: arr.Diagnose(signal),
	}
}

func problemWithTmdb(downloadID string, queueID, tmdbID int) arr.QueueObservation {
	item := observedProblem(downloadID, queueID, 100)
	item.Media.TmdbID = tmdbID
	return item
}

// issueAlerts is the admin *pushes* among the notifier's events. NotifyAdmins
// also carries WebSocket-only pings (issue_updated when an incident returns to
// tracking), which are not lock-screen alerts and must not be counted as one.
func issueAlerts(notifier *fakeNotifier) []map[string]interface{} {
	var alerts []map[string]interface{}
	for i, event := range notifier.adminEvents {
		if event == "issue_created" {
			alerts = append(alerts, notifier.adminData[i])
		}
	}
	return alerts
}

// A promotion is an edge, not a verdict. An incident that leaves tracking and is
// handed straight back by the next snapshot must never page an admin — that is
// the season-sized burst of "did not recover automatically" alerts for downloads
// that were, in fact, recovering.
func TestPromotionThatFallsBackToTrackingNeverPages(t *testing.T) {
	svc, notifier, _ := setupObservationService(t, false)
	enableAutoDispatch(t, svc, 5)
	base := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	problem := observedProblem("download-a", 7, 100)

	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{problem}, base); err != nil {
		t.Fatal(err)
	}
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{problem}, base.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	issues, _ := svc.ListIssues("")
	if len(issues) != 1 || issues[0].Status != IssueOpen {
		t.Fatalf("promotion did not happen: %+v", issues)
	}
	// Inside the hold-down nothing has been earned yet.
	svc.flushIssueAlerts(base.Add(12 * time.Minute))
	if n := len(issueAlerts(notifier)); n != 0 {
		t.Fatalf("paged inside the hold-down: %v", notifier.adminEvents)
	}

	// The download resumes: back to passive tracking, exactly the case that
	// produced identical lock-screen alerts for healthy downloads.
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe",
		[]arr.QueueObservation{recoveringObservation("download-a", 7, 42)}, base.Add(12*time.Minute)); err != nil {
		t.Fatal(err)
	}
	issue, _ := svc.GetIssue(issues[0].ID)
	if issue.Status != IssueObserving && issue.Status != IssueRecovering {
		t.Fatalf("issue did not return to tracking: %+v", issue)
	}
	deliverIssueAlerts(svc, base.Add(30*time.Minute))
	if n := len(issueAlerts(notifier)); n != 0 {
		t.Fatalf("flapped promotion paged anyway: %v", notifier.adminEvents)
	}
}

// The flap must cancel the page, not the issue's right to one. An incident that
// stops recovering has to reach an admin even though its first promotion was
// withdrawn — otherwise damping the burst would silence real problems forever.
func TestFlappedIssueStillPagesOnceItSticks(t *testing.T) {
	svc, notifier, _ := setupObservationService(t, false)
	enableAutoDispatch(t, svc, 5)
	base := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	problem := observedProblem("download-a", 7, 100)

	for _, at := range []time.Time{base, base.Add(11 * time.Minute)} {
		if err := svc.observeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{problem}, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe",
		[]arr.QueueObservation{recoveringObservation("download-a", 7, 42)}, base.Add(12*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// A sweep lands while the incident is back in tracking; it restarts the
	// hold-down rather than dropping the owed alert.
	svc.flushIssueAlerts(base.Add(15 * time.Minute))
	if n := len(issueAlerts(notifier)); n != 0 {
		t.Fatalf("tracking incident paged: %v", notifier.adminEvents)
	}

	// It stops recovering and re-promotes. promoted_at is already set, so only
	// the surviving queue row can still earn this alert.
	for _, at := range []time.Time{base.Add(20 * time.Minute), base.Add(40 * time.Minute)} {
		if err := svc.observeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{problem}, at); err != nil {
			t.Fatal(err)
		}
	}
	issue, _ := svc.GetIssue(1)
	if issue.Status == IssueObserving || issue.Status == IssueRecovering {
		t.Fatalf("issue never re-promoted: %+v", issue)
	}
	deliverIssueAlerts(svc, base.Add(40*time.Minute))
	if n := len(issueAlerts(notifier)); n != 1 {
		t.Fatalf("stuck incident paged %d times, want 1: %v", n, notifier.adminEvents)
	}
	deliverIssueAlerts(svc, base.Add(60*time.Minute))
	if n := len(issueAlerts(notifier)); n != 1 {
		t.Fatalf("alert re-sent after delivery: %v", notifier.adminEvents)
	}
}

// A sustained promotion pages once, and the payload is unchanged from the
// pre-hold-down contract: a single issue still deep-links to its own thread.
func TestSustainedPromotionPagesOnceWithIssueID(t *testing.T) {
	svc, notifier, _ := setupObservationService(t, false)
	enableAutoDispatch(t, svc, 5)
	base := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	problem := observedProblem("download-a", 7, 100)
	for _, at := range []time.Time{base, base.Add(11 * time.Minute)} {
		if err := svc.observeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{problem}, at); err != nil {
			t.Fatal(err)
		}
	}
	deliverIssueAlerts(svc, base.Add(11*time.Minute))
	alerts := issueAlerts(notifier)
	if len(alerts) != 1 {
		t.Fatalf("events=%v", notifier.adminEvents)
	}
	data := alerts[0]
	if data["issue_id"] == nil {
		t.Fatalf("single alert dropped its deep link: %v", data)
	}
	if _, batched := data["count"]; batched {
		t.Fatalf("single alert claimed to be a batch: %v", data)
	}
	if data["source"] != SourceAuto {
		t.Fatalf("single alert source = %v, want auto", data["source"])
	}
}

// One arr problem is scoped per incident, so a batch cause (a season's worth of
// downloads stalling together) used to page once per incident with identical
// text. Everything clearing the hold-down on the same sweep is one alert.
func TestPromotionWaveCoalescesIntoOneAlert(t *testing.T) {
	svc, notifier, _ := setupObservationService(t, false)
	enableAutoDispatch(t, svc, 5)
	base := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	wave := []arr.QueueObservation{
		problemWithTmdb("download-a", 1, 42),
		problemWithTmdb("download-b", 2, 43),
		problemWithTmdb("download-c", 3, 44),
	}
	for _, at := range []time.Time{base, base.Add(11 * time.Minute)} {
		if err := svc.observeQueueSnapshot("radarr", "radarr-observe", wave, at); err != nil {
			t.Fatal(err)
		}
	}
	issues, _ := svc.ListIssues("")
	if len(issues) != 3 {
		t.Fatalf("wave = %d incidents, want 3 (one per exact media scope)", len(issues))
	}
	for _, issue := range issues {
		if issue.Status == IssueObserving || issue.Status == IssueRecovering {
			t.Fatalf("incident %d never promoted: %+v", issue.ID, issue)
		}
	}
	deliverIssueAlerts(svc, base.Add(11*time.Minute))
	alerts := issueAlerts(notifier)
	if len(alerts) != 1 {
		t.Fatalf("wave paged %d times, want 1: %v", len(alerts), notifier.adminEvents)
	}
	data := alerts[0]
	if count, _ := data["count"].(int); count != 3 {
		t.Fatalf("coalesced count = %v, want 3", data["count"])
	}
	if data["issue_id"] != nil {
		t.Fatalf("coalesced alert deep-linked one incident: %v", data)
	}
}

// actionAlerts is the admin approval pushes among the notifier's events.
func actionAlerts(notifier *fakeNotifier) []map[string]interface{} {
	var alerts []map[string]interface{}
	for i, event := range notifier.adminEvents {
		if event == "agent_action_pending" {
			alerts = append(alerts, notifier.adminData[i])
		}
	}
	return alerts
}

// deliverActionAlerts flushes the approval-alert queue far enough past `at`
// that an alert queued at `at` has served its hold-down.
func deliverActionAlerts(svc *Service, at time.Time) {
	svc.flushActionAlerts(at.Add(issueAlertHoldDown))
}

// seedProposal inserts an awaiting-approval issue holding one live proposed
// action — the durable state the runner leaves behind when it parks a fix.
func seedProposal(t *testing.T, svc *Service, title, fingerprint string) int64 {
	t.Helper()
	res, err := svc.db.Exec(
		`INSERT INTO issues (source, status, media_type, tmdb_id, title)
		 VALUES (?, ?, 'tv', 42, ?)`, SourceAuto, IssueAwaitingApproval, title,
	)
	if err != nil {
		t.Fatal(err)
	}
	issueID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(
		`INSERT INTO agent_actions (issue_id, kind, params, rationale, status, fingerprint)
		 VALUES (?, ?, '{}', 'r', ?, ?)`,
		issueID, string(ActionTriggerSearch), ActionProposed, fingerprint,
	); err != nil {
		t.Fatal(err)
	}
	return issueID
}

// A batch cause dispatches a batch of runs, and each parks its own proposal —
// one stuck season pack is 13 parked fixes inside the same minute. Everything
// clearing the hold-down together is one approval push carrying a count.
func TestProposalWaveCoalescesIntoOneApprovalPush(t *testing.T) {
	svc, notifier, _ := setupTestService(t)
	base := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		id := seedProposal(t, svc, "Tremors", string(rune('a'+i)))
		svc.queueActionAlert(id, base)
	}
	// Inside the hold-down nothing has been earned yet.
	svc.flushActionAlerts(base.Add(time.Minute))
	if n := len(actionAlerts(notifier)); n != 0 {
		t.Fatalf("paged inside the hold-down: %v", notifier.adminEvents)
	}
	deliverActionAlerts(svc, base)
	alerts := actionAlerts(notifier)
	if len(alerts) != 1 {
		t.Fatalf("wave paged %d times, want 1: %v", len(alerts), notifier.adminEvents)
	}
	data := alerts[0]
	if count, _ := data["count"].(int); count != 3 {
		t.Fatalf("coalesced count = %v, want 3", data["count"])
	}
	if data["issue_id"] != nil {
		t.Fatalf("coalesced approval push deep-linked one incident: %v", data)
	}
	deliverActionAlerts(svc, base.Add(time.Hour))
	if n := len(actionAlerts(notifier)); n != 1 {
		t.Fatalf("alert re-sent after delivery: %v", notifier.adminEvents)
	}
}

// A single proposal keeps the pre-batching contract: the push deep-links its
// issue and claims no count.
func TestSingleProposalPushKeepsItsDeepLink(t *testing.T) {
	svc, notifier, _ := setupTestService(t)
	base := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	id := seedProposal(t, svc, "Tremors", "solo")
	svc.queueActionAlert(id, base)
	deliverActionAlerts(svc, base)
	alerts := actionAlerts(notifier)
	if len(alerts) != 1 {
		t.Fatalf("events=%v", notifier.adminEvents)
	}
	data := alerts[0]
	if got, _ := data["issue_id"].(int64); got != id {
		t.Fatalf("single approval push issue_id = %v, want %d", data["issue_id"], id)
	}
	if _, batched := data["count"]; batched {
		t.Fatalf("single approval push claimed to be a batch: %v", data)
	}
}

// A batch cause parks its proposals in a trickle — a dozen runs across two
// workers spread over several minutes — so delivery must wait for the wave to
// stop growing. Delivering whatever happened to age past the hold-down on each
// tick chopped one wave into several identical pages minutes apart.
func TestStragglingProposalWaveStillPagesOnce(t *testing.T) {
	svc, notifier, _ := setupTestService(t)
	base := time.Date(2026, 7, 29, 8, 30, 0, 0, time.UTC)
	for i, offset := range []time.Duration{0, 2 * time.Minute, 4 * time.Minute} {
		id := seedProposal(t, svc, "Tremors", fmt.Sprintf("straggler-%d", i))
		svc.queueActionAlert(id, base.Add(offset))
	}
	// The oldest row has aged past the hold-down, but the wave is still
	// growing — nothing may deliver yet.
	for _, at := range []time.Time{base.Add(3 * time.Minute), base.Add(5 * time.Minute)} {
		svc.flushActionAlerts(at)
		if n := len(actionAlerts(notifier)); n != 0 {
			t.Fatalf("paged at %v while the wave was still forming: %v", at, notifier.adminEvents)
		}
	}
	// Quiet for a full hold-down after the last arrival: one push, whole wave.
	svc.flushActionAlerts(base.Add(7 * time.Minute))
	alerts := actionAlerts(notifier)
	if len(alerts) != 1 {
		t.Fatalf("straggling wave paged %d times, want 1: %v", len(alerts), notifier.adminEvents)
	}
	if count, _ := alerts[0]["count"].(int); count != 3 {
		t.Fatalf("coalesced count = %v, want 3", alerts[0]["count"])
	}
}

// A continuous trickle of proposals must not defer the page forever: once the
// oldest owed alert hits the delivery ceiling, everything owed goes out even
// though the wave never went quiet.
func TestContinuousProposalTrickleHitsDeliveryCeiling(t *testing.T) {
	svc, notifier, _ := setupTestService(t)
	base := time.Date(2026, 7, 29, 8, 30, 0, 0, time.UTC)
	for i := 0; i < 8; i++ {
		id := seedProposal(t, svc, "Tremors", fmt.Sprintf("trickle-%d", i))
		svc.queueActionAlert(id, base.Add(time.Duration(2*i)*time.Minute))
	}
	// Newest row is 2 minutes old (inside the hold-down) but the oldest has
	// waited out the ceiling.
	svc.flushActionAlerts(base.Add(16 * time.Minute))
	alerts := actionAlerts(notifier)
	if len(alerts) != 1 {
		t.Fatalf("trickle paged %d times, want 1: %v", len(alerts), notifier.adminEvents)
	}
	if count, _ := alerts[0]["count"].(int); count != 8 {
		t.Fatalf("ceiling delivery count = %v, want 8", alerts[0]["count"])
	}
	var queued int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM agent_action_alert_queue`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 0 {
		t.Fatalf("ceiling delivery left %d owed alert(s)", queued)
	}
}

// A proposal decided or superseded inside the hold-down, or whose issue closed,
// no longer stands for anything an admin must approve — the owed push is
// dropped unannounced instead of paging about work that is already done.
func TestProposalSettledInsideHoldDownNeverPages(t *testing.T) {
	svc, notifier, _ := setupTestService(t)
	base := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	decided := seedProposal(t, svc, "Tremors", "decided")
	superseded := seedProposal(t, svc, "Tremors", "superseded")
	closed := seedProposal(t, svc, "Tremors", "closed")
	for _, id := range []int64{decided, superseded, closed} {
		svc.queueActionAlert(id, base)
	}
	if _, err := svc.db.Exec(
		`UPDATE agent_actions SET status=? WHERE issue_id=?`, ActionExecuted, decided,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(
		`UPDATE agent_actions SET status=? WHERE issue_id=?`, ActionSuperseded, superseded,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(
		`UPDATE issues SET status=?, closed_at=? WHERE id=?`, IssueResolved, base.Add(time.Minute), closed,
	); err != nil {
		t.Fatal(err)
	}
	deliverActionAlerts(svc, base)
	if n := len(actionAlerts(notifier)); n != 0 {
		t.Fatalf("settled proposals paged: %v", notifier.adminEvents)
	}
	var queued int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM agent_action_alert_queue`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 0 {
		t.Fatalf("settled proposals left %d owed alert(s)", queued)
	}
}

// An incident the agent (or the arr) settles inside the hold-down never needed
// an admin at all, so the owed alert is dropped rather than delivered late.
func TestIssueClosedInsideHoldDownNeverPages(t *testing.T) {
	svc, notifier, _ := setupObservationService(t, false)
	enableAutoDispatch(t, svc, 5)
	base := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	problem := observedProblem("download-a", 7, 100)
	for _, at := range []time.Time{base, base.Add(11 * time.Minute)} {
		if err := svc.observeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{problem}, at); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.db.Exec(
		`UPDATE issues SET status=?, closed_at=? WHERE id=1`, IssueResolved, base.Add(12*time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	deliverIssueAlerts(svc, base.Add(30*time.Minute))
	if n := len(issueAlerts(notifier)); n != 0 {
		t.Fatalf("closed issue paged: %v", notifier.adminEvents)
	}
	var queued int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM issue_alert_queue`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 0 {
		t.Fatalf("closed issue left %d owed alert(s)", queued)
	}
}
