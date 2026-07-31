package remediation

import (
	"log"
	"sort"
	"strings"
	"time"
)

// issueAlertHoldDown is how long an issue must stay out of passive tracking
// before the admin push it earned is actually sent. The approval-alert queue
// reuses the same window: it exists to gather a simultaneous wave of proposals
// into one page, and to swallow a proposal that is decided or superseded
// moments after it parks.
//
// Promotion is an edge, not a verdict. An incident crosses the observation
// window, leaves tracking, and the very next complete queue snapshot can hand
// it straight back: a stalled download resumes, the arr grabs a replacement,
// suspendIssueForRecovery returns it to recovering. The agent can also close it
// outright within the same minute. Paging on the edge announced "did not
// recover automatically" about downloads that were, in fact, recovering.
//
// Sized above two observation sweeps (observationSweepPeriod) so a single
// sweep's flap can never slip an alert out, and short enough that a genuinely
// stuck incident still reaches an admin promptly.
const issueAlertHoldDown = 3 * time.Minute

// queuedIssueAlert is one owed push: the issue and the source that decides the
// alert's wording.
type queuedIssueAlert struct {
	id     int64
	source string
	title  string
}

// queueIssueAlert records that issueID has left passive tracking and owes its
// admins a push once the hold-down lapses. Idempotent — the issue id is the
// primary key, so re-queueing an already-pending alert cannot double-page. The
// clock is the caller's observation clock, never CURRENT_TIMESTAMP, so the
// hold-down is measured on the same timeline as every other observation window.
func (s *Service) queueIssueAlert(issueID int64, now time.Time) {
	if s.notifier == nil {
		return // Nothing can deliver it; do not accumulate rows.
	}
	if _, err := s.db.Exec(
		`INSERT INTO issue_alert_queue (issue_id, queued_at) VALUES (?, ?)
		 ON CONFLICT(issue_id) DO NOTHING`, issueID, now,
	); err != nil {
		log.Printf("remediation: queue issue alert %d: %v", issueID, err)
	}
}

// flushIssueAlerts advances every owed alert by one tick: closed issues are
// dropped unannounced, issues back in tracking restart their hold-down, and
// whatever has stayed out of tracking for the full window is announced — one
// push per source, carrying a count rather than one push per incident.
//
// The queue row, not issue_observations.promoted_at, is the once-per-issue
// ledger. An incident that flaps out of tracking and back keeps its pending row
// with a restarted clock, so a problem that eventually sticks still pages even
// though its first promotion was cancelled.
func (s *Service) flushIssueAlerts(now time.Time) {
	if s.notifier == nil {
		return
	}
	// The queue is empty on almost every tick, so pay one cheap read before the
	// write statements below. It doubles as the quiet path for a closed database
	// during shutdown, which must not log on every sweep.
	var pending bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM issue_alert_queue)`).Scan(&pending); err != nil || !pending {
		return
	}
	// A closed issue needs no admin. This runs ahead of the hold-down check so a
	// self-resolving incident never lingers as a pending page.
	if _, err := s.db.Exec(
		`DELETE FROM issue_alert_queue WHERE issue_id IN (
		   SELECT q.issue_id FROM issue_alert_queue q
		   LEFT JOIN issues i ON i.id = q.issue_id
		   WHERE i.id IS NULL OR i.closed_at IS NOT NULL)`,
	); err != nil {
		log.Printf("remediation: drop closed issue alerts: %v", err)
		return
	}
	// Back in tracking means Cantinarr is watching the arr's own recovery again,
	// which is precisely what must not page. Restart the clock rather than drop
	// the row: the incident may still be owed an alert if it stops recovering.
	if _, err := s.db.Exec(
		`UPDATE issue_alert_queue SET queued_at = ? WHERE issue_id IN (
		   SELECT q.issue_id FROM issue_alert_queue q JOIN issues i ON i.id = q.issue_id
		   WHERE i.status IN (?, ?))`,
		now, IssueObserving, IssueRecovering,
	); err != nil {
		log.Printf("remediation: re-arm tracking issue alerts: %v", err)
		return
	}

	cutoff := now.Add(-issueAlertHoldDown)
	rows, err := s.db.Query(
		`SELECT q.issue_id, i.source, i.title FROM issue_alert_queue q
		 JOIN issues i ON i.id = q.issue_id
		 WHERE q.queued_at <= ? ORDER BY q.issue_id`,
		cutoff,
	)
	if err != nil {
		log.Printf("remediation: read due issue alerts: %v", err)
		return
	}
	var due []queuedIssueAlert
	for rows.Next() {
		var alert queuedIssueAlert
		if err := rows.Scan(&alert.id, &alert.source, &alert.title); err != nil {
			rows.Close()
			log.Printf("remediation: scan due issue alert: %v", err)
			return
		}
		due = append(due, alert)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Printf("remediation: iterate due issue alerts: %v", err)
		return
	}
	if len(due) == 0 {
		return
	}

	// Clear the whole batch in one statement before sending. Sends are
	// fire-and-forget, so a failure after this point costs one alert; leaving
	// the rows would instead re-page every tick. Deleting by the hold-down
	// cutoff (rather than per id) keeps the clear atomic with what was read: a
	// row queued after the SELECT is newer than the cutoff and survives.
	if _, err := s.db.Exec(`DELETE FROM issue_alert_queue WHERE queued_at <= ?`, cutoff); err != nil {
		log.Printf("remediation: clear delivered issue alerts: %v", err)
		return
	}

	bySource := make(map[string][]queuedIssueAlert)
	for _, alert := range due {
		bySource[alert.source] = append(bySource[alert.source], alert)
	}
	sources := make([]string, 0, len(bySource))
	for source := range bySource {
		sources = append(sources, source)
	}
	sort.Strings(sources) // Deterministic order so tests and logs are stable.
	for _, source := range sources {
		batch := bySource[source]
		if len(batch) == 1 {
			s.notifyIssueCreatedWithSource(batch[0].id, batch[0].title, source)
			continue
		}
		s.notifyIssueBatch(source, len(batch))
	}
}

// queueActionAlert records that issueID parked a proposal and owes its admins
// an approval push once the hold-down lapses. Idempotent — the issue id is the
// primary key, so a resume that parks a follow-up proposal while one is already
// owed cannot double-page.
func (s *Service) queueActionAlert(issueID int64, now time.Time) {
	if s.notifier == nil {
		return // Nothing can deliver it; do not accumulate rows.
	}
	if _, err := s.db.Exec(
		`INSERT INTO agent_action_alert_queue (issue_id, queued_at) VALUES (?, ?)
		 ON CONFLICT(issue_id) DO NOTHING`, issueID, now,
	); err != nil {
		log.Printf("remediation: queue action alert %d: %v", issueID, err)
	}
}

// actionAlertMaxDelay bounds how long the approval queue may keep waiting for
// a wave to stop growing. A batch cause parks its proposals in a trickle (a
// dozen runs across two workers spread over several minutes), so delivery
// waits for a full hold-down with no new arrival — but a continuous trickle
// must not defer the page forever, so the oldest owed alert caps the wait.
const actionAlertMaxDelay = 15 * time.Minute

// flushActionAlerts advances every owed approval push by one tick: rows whose
// proposal was decided, superseded, or whose issue closed are dropped
// unannounced, and once the wave has stayed quiet for the hold-down (or the
// oldest row hits the delivery ceiling) everything owed is announced together —
// one push carrying a count for a wave, the usual deep-linked push for a single
// proposal. Delivering per-tick instead of per-quiesce chopped one straggling
// wave into several identical pages. Unlike issue alerts there is no re-arm
// rule: a superseded proposal deletes its row here, and a later re-proposal
// queues a fresh one.
func (s *Service) flushActionAlerts(now time.Time) {
	if s.notifier == nil {
		return
	}
	// The queue is empty on almost every tick; one cheap read guards the writes
	// and keeps a closed database quiet during shutdown.
	var pending bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM agent_action_alert_queue)`).Scan(&pending); err != nil || !pending {
		return
	}
	if _, err := s.db.Exec(
		`DELETE FROM agent_action_alert_queue WHERE issue_id IN (
		   SELECT q.issue_id FROM agent_action_alert_queue q
		   LEFT JOIN issues i ON i.id = q.issue_id
		   WHERE i.id IS NULL OR i.closed_at IS NOT NULL
		      OR NOT EXISTS (SELECT 1 FROM agent_actions a
		                     WHERE a.issue_id = q.issue_id AND a.status = ?))`,
		ActionProposed,
	); err != nil {
		log.Printf("remediation: drop stale action alerts: %v", err)
		return
	}

	var forming, ceiling bool
	if err := s.db.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM agent_action_alert_queue WHERE queued_at > ?),
		        EXISTS(SELECT 1 FROM agent_action_alert_queue WHERE queued_at <= ?)`,
		now.Add(-issueAlertHoldDown), now.Add(-actionAlertMaxDelay),
	).Scan(&forming, &ceiling); err != nil {
		log.Printf("remediation: read action alert window: %v", err)
		return
	}
	if forming && !ceiling {
		return // The wave is still forming; deliver it whole once it goes quiet.
	}
	rows, err := s.db.Query(`SELECT issue_id FROM agent_action_alert_queue ORDER BY issue_id`)
	if err != nil {
		log.Printf("remediation: read due action alerts: %v", err)
		return
	}
	var due []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			log.Printf("remediation: scan due action alert: %v", err)
			return
		}
		due = append(due, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Printf("remediation: iterate due action alerts: %v", err)
		return
	}
	if len(due) == 0 {
		return
	}
	// Clear before sending, by the exact ids read: a failure after this point
	// costs one alert, while leaving the rows would re-page every tick, and a
	// row queued after the SELECT is untouched and starts its own wave.
	placeholders := strings.Repeat("?,", len(due))
	args := make([]any, len(due))
	for i, id := range due {
		args[i] = id
	}
	if _, err := s.db.Exec(
		`DELETE FROM agent_action_alert_queue WHERE issue_id IN (`+placeholders[:len(placeholders)-1]+`)`, args...,
	); err != nil {
		log.Printf("remediation: clear delivered action alerts: %v", err)
		return
	}

	if len(due) == 1 {
		s.notifyPendingActionForIssue(due[0])
		return
	}
	data := map[string]interface{}{"count": len(due)}
	if count, err := s.PendingActionCount(); err == nil {
		data["pending_count"] = count
	}
	s.notifier.NotifyAdmins("agent_action_pending", data)
}
