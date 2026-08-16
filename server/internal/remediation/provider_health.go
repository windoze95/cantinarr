package remediation

import (
	"database/sql"
	"errors"
	"fmt"
)

const (
	remediationProviderDedupeKey = "system:remediation-provider"
	remediationProviderTitle     = "Remediation is on, but no AI provider is available"
)

// RecordRemediationProviderHealth tracks the standing "enabled but providerless"
// condition the Runner hits when it cannot resolve a shared turn. Unavailable
// opens or refreshes ONE admin-only system issue; the first successful resolve
// closes it. The detected issues themselves stay open and are re-enqueued by
// recoverWork until a provider exists, and none of this feeds the auto-dispatch
// circuit breaker: the agent did not fail — it was never given a provider.
//
// Same transactional shape as RecordSharedAIHealth: find-or-open under one tx,
// refresh occurrences on repeats (at most hourly), resolve-with-note on
// recovery. The two sinks stay separate because they answer different
// questions — the daily health turn says "the configured model stopped
// answering"; this says "remediation has nothing configured to ask".
func (s *Service) RecordRemediationProviderHealth(available bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin remediation provider transition: %w", err)
	}
	defer tx.Rollback()

	var issueID int64
	var recentlyRefreshed bool
	err = tx.QueryRow(`
		SELECT id, COALESCE(datetime(updated_at) >= datetime('now', '-1 hour'), 0)
		FROM issues
		WHERE dedupe_key = ? AND closed_at IS NULL`, remediationProviderDedupeKey).Scan(&issueID, &recentlyRefreshed)
	if available {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("find remediation provider issue: %w", err)
		}
		resolution := "A shared AI provider is available again; waiting investigations resume automatically."
		if _, err := tx.Exec(`
			UPDATE issues SET status = ?, read = 0, resolution = ?, resolution_kind = ?,
				updated_at = CURRENT_TIMESTAMP, closed_at = CURRENT_TIMESTAMP
			WHERE id = ? AND closed_at IS NULL`,
			IssueResolved, resolution, ResolutionRemediationProviderConfigured, issueID); err != nil {
			return fmt.Errorf("resolve remediation provider issue: %w", err)
		}
		if _, err := tx.Exec(`
			INSERT INTO issue_messages (issue_id, author_kind, body)
			VALUES (?, ?, ?)`, issueID, AuthorSystem, resolution); err != nil {
			return fmt.Errorf("record remediation provider recovery: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit remediation provider recovery: %w", err)
		}
		s.notifyIssueResolved(issueID, IssueResolved)
		return nil
	}

	detail := "Autonomous remediation is enabled, but no shared AI provider is available, so detected problems are waiting instead of being investigated. They are retried automatically and will be picked up as soon as a provider works."
	resolution := "Open Settings > Providers & Credentials and configure or repair the shared AI provider — or turn remediation off under Settings > AI Remediation."
	created := false
	if errors.Is(err, sql.ErrNoRows) {
		result, insertErr := tx.Exec(`
			INSERT INTO issues
				(source, status, tmdb_id, media_type, title, detail, dedupe_key, read, resolution)
			VALUES (?, ?, 0, ?, ?, ?, ?, 0, ?)`,
			SourceSystem, IssueNeedsAdmin, sharedAIHealthMediaType, remediationProviderTitle,
			detail, remediationProviderDedupeKey, resolution)
		if insertErr != nil {
			return fmt.Errorf("create remediation provider issue: %w", insertErr)
		}
		issueID, err = result.LastInsertId()
		if err != nil {
			return fmt.Errorf("read remediation provider issue id: %w", err)
		}
		created = true
		if _, err := tx.Exec(`
			INSERT INTO issue_messages (issue_id, author_kind, body)
			VALUES (?, ?, ?)`, issueID, AuthorSystem, detail+" "+resolution); err != nil {
			return fmt.Errorf("record remediation provider failure: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("find remediation provider issue: %w", err)
	} else {
		// recoverWork re-runs every waiting issue once a minute, and each pass
		// lands here while the provider is missing. Refreshing at most hourly
		// keeps occurrences a readable "hours in this state" instead of a
		// per-minute counter, and spares a write transaction per issue per tick.
		if recentlyRefreshed {
			return nil
		}
		if _, err := tx.Exec(`
			UPDATE issues SET status = ?, occurrences = occurrences + 1, updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND closed_at IS NULL`,
			IssueNeedsAdmin, issueID); err != nil {
			return fmt.Errorf("refresh remediation provider issue: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit remediation provider failure: %w", err)
	}
	if created {
		s.notifyIssueCreatedWithSource(issueID, remediationProviderTitle, SourceSystem)
	}
	return nil
}

const (
	autoDispatchBreakerDedupeKey = "system:remediation-breaker"
	autoDispatchBreakerTitle     = "Automatic problem detection switched itself off"
)

// RecordAutoDispatchBreaker mirrors the provider sink for the circuit breaker:
// tripped opens or refreshes ONE durable admin issue (the push and snackbar
// are transient; an admin who was away learns nothing from them), and
// re-enabling auto-dispatch resolves it. Autonomy never stands down silently.
func (s *Service) RecordAutoDispatchBreaker(tripped bool, streak, threshold int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin breaker transition: %w", err)
	}
	defer tx.Rollback()

	var issueID int64
	err = tx.QueryRow(`
		SELECT id FROM issues
		WHERE dedupe_key = ? AND closed_at IS NULL`, autoDispatchBreakerDedupeKey).Scan(&issueID)
	if !tripped {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("find breaker issue: %w", err)
		}
		resolution := "Auto-dispatch was re-enabled; automatic problem detection is running again."
		if _, err := tx.Exec(`
			UPDATE issues SET status = ?, read = 0, resolution = ?, resolution_kind = ?,
				updated_at = CURRENT_TIMESTAMP, closed_at = CURRENT_TIMESTAMP
			WHERE id = ? AND closed_at IS NULL`,
			IssueResolved, resolution, ResolutionRemediationProviderConfigured, issueID); err != nil {
			return fmt.Errorf("resolve breaker issue: %w", err)
		}
		if _, err := tx.Exec(`
			INSERT INTO issue_messages (issue_id, author_kind, body)
			VALUES (?, ?, ?)`, issueID, AuthorSystem, resolution); err != nil {
			return fmt.Errorf("record breaker recovery: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit breaker recovery: %w", err)
		}
		s.notifyIssueResolved(issueID, IssueResolved)
		return nil
	}

	detail := fmt.Sprintf("After %d consecutive investigations ended without a resolution (threshold %d), Cantinarr switched automatic problem detection off so it would stop opening issues it cannot finish.", streak, threshold)
	resolution := "Review the recent needs-admin issues for the underlying cause, then re-enable auto-dispatch under Settings > AI Remediation — re-enabling closes this notice."
	if errsql := err; errors.Is(errsql, sql.ErrNoRows) {
		result, insertErr := tx.Exec(`
			INSERT INTO issues
				(source, status, tmdb_id, media_type, title, detail, dedupe_key, read, resolution)
			VALUES (?, ?, 0, ?, ?, ?, ?, 0, ?)`,
			SourceSystem, IssueNeedsAdmin, sharedAIHealthMediaType, autoDispatchBreakerTitle,
			detail, autoDispatchBreakerDedupeKey, resolution)
		if insertErr != nil {
			return fmt.Errorf("create breaker issue: %w", insertErr)
		}
		issueID, err = result.LastInsertId()
		if err != nil {
			return fmt.Errorf("read breaker issue id: %w", err)
		}
		if _, err := tx.Exec(`
			INSERT INTO issue_messages (issue_id, author_kind, body)
			VALUES (?, ?, ?)`, issueID, AuthorSystem, detail+" "+resolution); err != nil {
			return fmt.Errorf("record breaker trip: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit breaker trip: %w", err)
		}
		s.notifyIssueCreatedWithSource(issueID, autoDispatchBreakerTitle, SourceSystem)
		return nil
	} else if err != nil {
		return fmt.Errorf("find breaker issue: %w", err)
	}
	if _, err := tx.Exec(`
		UPDATE issues SET status = ?, read = 0, detail = ?, occurrences = occurrences + 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND closed_at IS NULL`,
		IssueNeedsAdmin, detail, issueID); err != nil {
		return fmt.Errorf("refresh breaker issue: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit breaker refresh: %w", err)
	}
	s.pingIssueUpdated(issueID)
	return nil
}
