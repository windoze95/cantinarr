package remediation

import (
	"database/sql"
	"log"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/arr"
)

// autoGiveupStreakKey is the settings-table key holding the consecutive
// auto-dispatch give-up counter (a plain integer string). It is kept OUT of the
// remediation_settings JSON blob on purpose: it is operational state, not admin
// configuration, and keeping it separate avoids a read-modify-write race with
// the blob and keeps settings.go untouched.
const autoGiveupStreakKey = "remediation_auto_giveup_streak"

// AutoDispatcher is the IssueOpener the websocket hub calls when its poller finds
// a stuck/blocked download. It is the gate + glue between the read-only poll path
// and the remediation Service: it re-checks the live toggles per call (so an
// admin flipping Enabled/AutoDispatch takes effect without a restart), opens a
// deduped issue, and — on a genuinely new issue — enqueues the read-only Runner
// OFF the poll goroutine so the 30s loop never blocks on the agent.
//
// It is a thin wrapper around *Service (not the Service itself) so the hub's
// optional-interface contract is explicit: main.go passes a non-nil
// *AutoDispatcher only when remediation is wired, and a nil *AutoDispatcher (or
// leaving the hub's opener unset) cleanly disables the whole path.
type AutoDispatcher struct {
	svc              *Service
	now              func() time.Time
	snapshotMu       sync.Mutex
	pendingSnapshots map[string][]queueSnapshotJob
	snapshotWake     chan struct{}
	startOnce        sync.Once
}

// NewAutoDispatcher wraps a remediation Service as the hub's IssueOpener.
func NewAutoDispatcher(svc *Service) *AutoDispatcher {
	return &AutoDispatcher{
		svc: svc, now: time.Now,
		pendingSnapshots: make(map[string][]queueSnapshotJob),
		snapshotWake:     make(chan struct{}, 1),
	}
}

// ObserveQueueSnapshot accepts one successful, complete detailed-queue read.
// It only copies and queues the value: DB reconciliation and exact-library
// witnesses run on the observation worker, never on the websocket poller.
func (a *AutoDispatcher) ObserveQueueSnapshot(serviceType, instanceID string, items []arr.QueueObservation) {
	if a == nil || a.svc == nil {
		return
	}
	copyItems := append([]arr.QueueObservation(nil), items...)
	now := time.Now().UTC()
	if a.now != nil {
		now = a.now().UTC()
	}
	job := queueSnapshotJob{serviceType: serviceType, instanceID: instanceID, items: copyItems, observedAt: now}
	a.enqueueSnapshotJob(job)
}

func (a *AutoDispatcher) enqueueSnapshotJob(job queueSnapshotJob) {
	a.snapshotMu.Lock()
	key := job.serviceType + "\x00" + job.instanceID
	// Arrival order is not observation order: a slow older fetch can complete
	// after a newer websocket/sweeper read. Retain the chronologically latest
	// success and latest failure only, then preserve success -> failure when the
	// failure actually happened later. This keeps the queue small without
	// dropping newer evidence before the durable DB watermark can inspect it.
	candidates := append(append([]queueSnapshotJob(nil), a.pendingSnapshots[key]...), job)
	var latestSuccess, latestFailure queueSnapshotJob
	hasSuccess, hasFailure := false, false
	for _, candidate := range candidates {
		if candidate.failure == nil {
			if !hasSuccess || candidate.observedAt.After(latestSuccess.observedAt) {
				latestSuccess, hasSuccess = candidate, true
			}
			continue
		}
		if !hasFailure || candidate.observedAt.After(latestFailure.observedAt) {
			latestFailure, hasFailure = candidate, true
		}
	}
	pending := make([]queueSnapshotJob, 0, 2)
	if hasSuccess {
		pending = append(pending, latestSuccess)
		if hasFailure && latestFailure.observedAt.After(latestSuccess.observedAt) {
			pending = append(pending, latestFailure)
		}
	} else if hasFailure {
		pending = append(pending, latestFailure)
	}
	a.pendingSnapshots[key] = pending
	a.snapshotMu.Unlock()
	select {
	case a.snapshotWake <- struct{}{}:
	default:
		// A wake is already pending. The newest per-instance snapshot replaced
		// any older pending value above, so recovery/progress evidence wins.
	}
}

// --- circuit breaker ---
//
// The breaker bounds how many unattended auto-dispatch investigations can fail
// in a row before auto-dispatch shuts itself off, so a flapping or misconfigured
// indexer can't fan out into dozens of give-ups. It is driven entirely from the
// terminal outcomes of AUTO-sourced issues (user-reported issues never touch the
// streak):
//
//   - an auto issue resolves              -> streak reset to 0
//   - an auto issue terminates non-resolved (wont_fix/failed/dismissed)
//                                          -> streak++; at the threshold,
//                                             AutoDispatch is persisted OFF and
//                                             admins are notified.
//
// noteAutoTerminal is invoked from ConcludeIssue (the single chokepoint every
// terminal transition funnels through) ONLY when the row actually transitioned,
// so a double-conclude can never double-count. DismissIssue is an admin action,
// not an agent give-up, and deliberately does not feed the breaker.

// noteAutoTerminal updates the circuit-breaker streak for a terminal issue
// outcome. It is a no-op for user-reported issues. resolved means the agent
// closed the issue successfully; anything else counts as a give-up.
func (s *Service) noteAutoTerminal(issueID int64, status string) {
	var source string
	if err := s.db.QueryRow("SELECT source FROM issues WHERE id = ?", issueID).Scan(&source); err != nil || source != SourceAuto {
		return
	}

	if status == IssueResolved {
		s.resetAutoGiveupStreak()
		return
	}

	streak := s.bumpAutoGiveupStreak()
	threshold := s.Settings().CircuitBreakerGiveups
	if threshold > 0 && streak >= threshold {
		s.tripCircuitBreaker(streak, threshold)
	}
}

// readAutoGiveupStreak returns the persisted consecutive-give-up counter (0 when
// unset/unparsable).
func (s *Service) readAutoGiveupStreak() int {
	var v sql.NullInt64
	if err := s.db.QueryRow("SELECT CAST(value AS INTEGER) FROM settings WHERE key = ?", autoGiveupStreakKey).Scan(&v); err != nil {
		return 0
	}
	if !v.Valid || v.Int64 < 0 {
		return 0
	}
	return int(v.Int64)
}

// resetAutoGiveupStreak clears the counter (an auto issue resolved). Best-effort.
func (s *Service) resetAutoGiveupStreak() {
	s.db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, '0')", autoGiveupStreakKey)
}

// bumpAutoGiveupStreak increments and persists the counter in one SQL statement,
// so concurrent terminalizations cannot lose an increment between a separate
// read and write.
func (s *Service) bumpAutoGiveupStreak() int {
	var n int
	err := s.db.QueryRow(
		`INSERT INTO settings (key, value) VALUES (?, '1')
		 ON CONFLICT(key) DO UPDATE SET value = CAST(
		   CASE WHEN CAST(settings.value AS INTEGER) < 0 THEN 1
		        ELSE CAST(settings.value AS INTEGER) + 1 END AS TEXT
		 )
		 RETURNING CAST(value AS INTEGER)`,
		autoGiveupStreakKey,
	).Scan(&n)
	if err != nil {
		log.Printf("remediation: bump auto give-up streak: %v", err)
	}
	return n
}

// tripCircuitBreaker persists AutoDispatch=off and notifies admins. It then
// resets the streak so a re-enable starts from a clean slate. Enabled (the
// master switch) is left untouched: only the poller path is disarmed.
func (s *Service) tripCircuitBreaker(streak, threshold int) {
	cur := s.Settings()
	if !cur.AutoDispatch {
		// Already off (e.g. a concurrent trip): just clear the streak.
		s.resetAutoGiveupStreak()
		return
	}
	cur.AutoDispatch = false
	if _, err := s.SetSettings(cur); err != nil {
		log.Printf("remediation: circuit breaker could not disable auto-dispatch: %v", err)
		return
	}
	log.Printf("remediation: circuit breaker tripped after %d consecutive auto-dispatch give-ups (threshold %d); auto-dispatch disabled", streak, threshold)
	if s.notifier != nil {
		// Fixed-template event: only structured fields travel, no model text.
		s.notifier.NotifyAdmins("remediation_autodispatch_disabled", map[string]interface{}{
			"reason":    "circuit_breaker",
			"giveups":   streak,
			"threshold": threshold,
		})
	}
	s.resetAutoGiveupStreak()
}
