package remediation

import (
	"database/sql"
	"errors"
	"fmt"
)

const (
	pushDeliveryHealthDedupeKey = "system:push-delivery"
	pushDeliveryHealthTitle     = "Push notifications are not being delivered"
	pushDeliveryHealthMediaType = "system"
)

// RecordPushDeliveryHealth implements push.DeliveryHealthSink. A run of failed
// sends opens or refreshes one admin-only system issue; the next successful
// send resolves it automatically.
//
// Unlike the shared-AI check this needs no probe. Sends already happen and
// already report success or failure — the signal was being logged and thrown
// away. Watching the real traffic is both cheaper than a synthetic test and
// more honest: it reports notifications that actually failed rather than a
// probe's opinion about whether they would.
func (s *Service) RecordPushDeliveryHealth(healthy bool, detail string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin push delivery health transition: %w", err)
	}
	defer tx.Rollback()

	var issueID int64
	err = tx.QueryRow(`
		SELECT id FROM issues
		WHERE dedupe_key = ? AND closed_at IS NULL`, pushDeliveryHealthDedupeKey).Scan(&issueID)
	if healthy {
		// The common case by far: nothing is open, so a successful send costs
		// one indexed lookup and returns.
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("find push delivery health issue: %w", err)
		}
		resolution := "A later notification reached the push gateway successfully. Alerts raised while delivery was failing were not retried, so check the Issues list and pending approvals for anything you were not told about."
		if _, err := tx.Exec(`
			UPDATE issues SET status = ?, read = 0, resolution = ?, resolution_kind = ?,
				updated_at = CURRENT_TIMESTAMP, closed_at = CURRENT_TIMESTAMP
			WHERE id = ? AND closed_at IS NULL`,
			IssueResolved, resolution, ResolutionPushDeliveryRestored, issueID); err != nil {
			return fmt.Errorf("resolve push delivery health issue: %w", err)
		}
		if _, err := tx.Exec(`
			INSERT INTO issue_messages (issue_id, author_kind, body)
			VALUES (?, ?, ?)`, issueID, AuthorSystem, resolution); err != nil {
			return fmt.Errorf("record push delivery recovery: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit push delivery recovery: %w", err)
		}
		s.notifyIssueResolved(issueID, IssueResolved)
		return nil
	}

	detail = boundedHealthField(detail)
	resolution := "Check that the push gateway is reachable from the server and that CANTINARR_PUSH_GATEWAY_URL still points at it. Delivery recovers on its own once it does; no notification sent during the outage is retried."
	created := false
	if errors.Is(err, sql.ErrNoRows) {
		result, insertErr := tx.Exec(`
			INSERT INTO issues
				(source, status, tmdb_id, media_type, title, detail, dedupe_key, read, resolution)
			VALUES (?, ?, 0, ?, ?, ?, ?, 0, ?)`,
			SourceSystem, IssueNeedsAdmin, pushDeliveryHealthMediaType, pushDeliveryHealthTitle,
			detail, pushDeliveryHealthDedupeKey, resolution)
		if insertErr != nil {
			return fmt.Errorf("create push delivery health issue: %w", insertErr)
		}
		issueID, err = result.LastInsertId()
		if err != nil {
			return fmt.Errorf("read push delivery health issue id: %w", err)
		}
		created = true
		if _, err := tx.Exec(`
			INSERT INTO issue_messages (issue_id, author_kind, body)
			VALUES (?, ?, ?)`, issueID, AuthorSystem, detail+" "+resolution); err != nil {
			return fmt.Errorf("record push delivery failure: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("find push delivery health issue: %w", err)
	} else {
		// occurrences counts the notifications the outage swallowed, which is
		// the number an admin actually wants when they come back to this.
		if _, err := tx.Exec(`
			UPDATE issues SET status = ?, read = 0, detail = ?, resolution = ?,
				occurrences = occurrences + 1, updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND closed_at IS NULL`,
			IssueNeedsAdmin, detail, resolution, issueID); err != nil {
			return fmt.Errorf("refresh push delivery health issue: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit push delivery failure: %w", err)
	}
	// Announcing a push outage over push is not the contradiction it looks
	// like: the issue is already visible in the app without it, and the alert
	// itself is queued behind the usual hold-down, so it lands once delivery
	// recovers and tells the admin their alerts were lost.
	if created {
		s.notifyIssueCreatedWithSource(issueID, pushDeliveryHealthTitle, SourceSystem)
	} else {
		s.pingIssueUpdated(issueID)
	}
	return nil
}
