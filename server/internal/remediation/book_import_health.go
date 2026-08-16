package remediation

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const (
	bookImportStallDedupePrefix = "system:book-import-stall:"
	bookImportStallMediaType    = "system"
	// bookImportStallMaxTitles bounds how many waiting titles the issue detail
	// names; the rest collapse into a count so the detail stays readable.
	bookImportStallMaxTitles = 5
)

// RecordBookImportStall implements request.BookImportStallSink. A stalled
// instance opens or refreshes one admin-only system issue keyed to that
// instance; a healthy pass resolves it automatically, whichever way the stall
// cleared (the import landed, the request demoted to the approval queue, or an
// admin denied it). The issue lives at IssueWaiting, not IssueNeedsAdmin:
// every exit is automatic and the sink itself is the witness, so there is no
// honest outcome for an admin to record — asking for one would demand a
// verdict on work nobody performed.
func (s *Service) RecordBookImportStall(instanceID, instanceName string, waitingTitles []string, healthy bool) error {
	instanceID = boundedHealthField(instanceID)
	instanceName = boundedHealthField(instanceName)
	dedupeKey := bookImportStallDedupePrefix + instanceID

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin book import stall transition: %w", err)
	}
	defer tx.Rollback()

	var issueID int64
	err = tx.QueryRow(`
		SELECT id FROM issues
		WHERE dedupe_key = ? AND closed_at IS NULL`, dedupeKey).Scan(&issueID)
	if healthy {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("find book import stall issue: %w", err)
		}
		resolution := "The waiting book requests completed or were handed to the approval queue."
		if _, err := tx.Exec(`
			UPDATE issues SET status = ?, read = 0, resolution = ?, resolution_kind = ?,
				updated_at = CURRENT_TIMESTAMP, closed_at = CURRENT_TIMESTAMP
			WHERE id = ? AND closed_at IS NULL`,
			IssueResolved, resolution, ResolutionBookImportCleared, issueID); err != nil {
			return fmt.Errorf("resolve book import stall issue: %w", err)
		}
		if _, err := tx.Exec(`
			INSERT INTO issue_messages (issue_id, author_kind, body)
			VALUES (?, ?, ?)`, issueID, AuthorSystem, resolution); err != nil {
			return fmt.Errorf("record book import stall recovery: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit book import stall recovery: %w", err)
		}
		s.notifyIssueResolved(issueID, IssueResolved)
		return nil
	}

	title := fmt.Sprintf("Book requests are waiting on %s's metadata import", instanceName)
	detail := fmt.Sprintf(
		"%s has book requests parked for more than a day because its metadata service is still importing their authors. Waiting: %s. The instance retries the import on its own schedule; Cantinarr watches it and completes the requests automatically when it lands, or hands them to the approval queue if the import fails or is cancelled.",
		instanceName, boundedTitleList(waitingTitles),
	)
	resolution := "Check the Chaptarr instance's queued author imports (System page / logs). Nothing is required on the Cantinarr side; this issue resolves itself when the imports land."
	created := false
	if errors.Is(err, sql.ErrNoRows) {
		result, insertErr := tx.Exec(`
			INSERT INTO issues
				(source, status, tmdb_id, media_type, title, detail, dedupe_key, read, resolution, instance_id)
			VALUES (?, ?, 0, ?, ?, ?, ?, 0, ?, ?)`,
			SourceSystem, IssueWaiting, bookImportStallMediaType, title,
			detail, dedupeKey, resolution, instanceID)
		if insertErr != nil {
			return fmt.Errorf("create book import stall issue: %w", insertErr)
		}
		issueID, err = result.LastInsertId()
		if err != nil {
			return fmt.Errorf("read book import stall issue id: %w", err)
		}
		created = true
		if _, err := tx.Exec(`
			INSERT INTO issue_messages (issue_id, author_kind, body)
			VALUES (?, ?, ?)`, issueID, AuthorSystem, detail+" "+resolution); err != nil {
			return fmt.Errorf("record book import stall: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("find book import stall issue: %w", err)
	} else {
		if _, err := tx.Exec(`
			UPDATE issues SET status = ?, read = 0, detail = ?, resolution = ?,
				occurrences = occurrences + 1, updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND closed_at IS NULL`,
			IssueWaiting, detail, resolution, issueID); err != nil {
			return fmt.Errorf("refresh book import stall issue: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit book import stall: %w", err)
	}
	if created {
		s.notifyIssueCreatedWithSource(issueID, title, SourceSystem)
	} else {
		s.pingIssueUpdated(issueID)
	}
	return nil
}

// boundedTitleList renders up to bookImportStallMaxTitles quoted titles and
// collapses the remainder into a count.
func boundedTitleList(titles []string) string {
	if len(titles) == 0 {
		return "(no titles recorded)"
	}
	shown := titles
	extra := 0
	if len(shown) > bookImportStallMaxTitles {
		extra = len(shown) - bookImportStallMaxTitles
		shown = shown[:bookImportStallMaxTitles]
	}
	quoted := make([]string, 0, len(shown))
	for _, title := range shown {
		quoted = append(quoted, fmt.Sprintf("%q", boundedHealthField(title)))
	}
	out := strings.Join(quoted, ", ")
	if extra > 0 {
		out = fmt.Sprintf("%s, and %d more", out, extra)
	}
	return out
}
