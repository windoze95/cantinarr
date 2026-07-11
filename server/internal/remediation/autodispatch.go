package remediation

import (
	"database/sql"
	"log"

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
	svc *Service
}

// NewAutoDispatcher wraps a remediation Service as the hub's IssueOpener.
func NewAutoDispatcher(svc *Service) *AutoDispatcher {
	return &AutoDispatcher{svc: svc}
}

// OpenAutoIssue is the poller hook (implements websocket.IssueOpener). It gates
// on the LIVE settings (read fresh each call), records a deduped auto issue via
// the Service, and enqueues the Runner when a NEW issue was created. The DB
// partial-unique index in Service.OpenAutoIssue is the real one-issue-per-stuck-
// download guarantee; the hub's 2-poll debounce only reduces noise upstream.
//
// It never runs the agent inline: Enqueue pushes onto the worker channel and
// returns immediately, so a slow investigation can't stall the poll goroutine.
func (a *AutoDispatcher) OpenAutoIssue(serviceType, instanceID, downloadID string, media arr.QueueMediaContext, d arr.Diagnosis) {
	if a == nil || a.svc == nil {
		return
	}
	s := a.svc.Settings()
	// Gate at CALL time on BOTH switches, read fresh so toggling takes effect
	// without a restart. AutoDispatch independently gates only this poll path; the
	// circuit breaker flips AutoDispatch off (not Enabled) when it trips.
	if !s.Enabled || !s.AutoDispatch {
		return
	}

	created, id := a.svc.OpenAutoIssue(serviceType, instanceID, downloadID, media, d)
	if !created {
		return // existing open issue (or a write error): nothing new to run.
	}
	// Kick off the read-only investigation off the poll goroutine.
	a.svc.Enqueue(id)
}

// ReconcileAutoIssues closes database incidents absent from a successful full
// queue diagnosis snapshot. It is intentionally not gated by Enabled or
// AutoDispatch: those switches control opening new work, not keeping already
// recorded incident state truthful across restarts.
func (a *AutoDispatcher) ReconcileAutoIssues(serviceType, instanceID string, activeDownloadIDs []string) {
	if a == nil || a.svc == nil {
		return
	}
	a.svc.ReconcileAutoIssues(instanceID, activeDownloadIDs)
}

// CloseAutoIssue resolves the open auto issue for a download the poller no longer
// flags (it recovered or left the queue). Gated only on the master switch — NOT
// AutoDispatch — so a recovered issue still auto-closes even after the circuit
// breaker disarmed dispatch. A no-op when there's no matching open issue.
func (a *AutoDispatcher) CloseAutoIssue(serviceType, instanceID, downloadID string) {
	if a == nil || a.svc == nil {
		return
	}
	if !a.svc.Settings().Enabled {
		return
	}
	a.svc.CloseAutoIssueForDownload(instanceID, downloadID)
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
