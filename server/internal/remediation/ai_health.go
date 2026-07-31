package remediation

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	sharedAIHealthDedupeKey = "system:shared-ai-health"
	sharedAIHealthTitle     = "Shared AI model is unavailable"
	sharedAIHealthMediaType = "system"
)

// RecordSharedAIHealth implements ai.SharedAIHealthIssueSink. Failures open or
// refresh one admin-only system issue; the next successful save-time or daily
// turn resolves it automatically.
func (s *Service) RecordSharedAIHealth(provider, model string, healthy bool) error {
	provider = boundedHealthField(provider)
	model = boundedHealthField(model)
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin shared AI health transition: %w", err)
	}
	defer tx.Rollback()

	var issueID int64
	err = tx.QueryRow(`
		SELECT id FROM issues
		WHERE dedupe_key = ? AND closed_at IS NULL`, sharedAIHealthDedupeKey).Scan(&issueID)
	if healthy {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("find shared AI health issue: %w", err)
		}
		resolution := "A later message-response test completed successfully with the configured shared AI provider and model."
		if _, err := tx.Exec(`
			UPDATE issues SET status = ?, read = 0, resolution = ?, resolution_kind = ?,
				updated_at = CURRENT_TIMESTAMP, closed_at = CURRENT_TIMESTAMP
			WHERE id = ? AND closed_at IS NULL`,
			IssueResolved, resolution, ResolutionAIHealthRestored, issueID); err != nil {
			return fmt.Errorf("resolve shared AI health issue: %w", err)
		}
		if _, err := tx.Exec(`
			INSERT INTO issue_messages (issue_id, author_kind, body)
			VALUES (?, ?, ?)`, issueID, AuthorSystem, resolution); err != nil {
			return fmt.Errorf("record shared AI recovery: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit shared AI recovery: %w", err)
		}
		s.notifyIssueResolved(issueID, IssueResolved)
		return nil
	}

	detail := fmt.Sprintf("Cantinarr's daily test message did not receive a usable response from provider %q with model %q.", provider, model)
	resolution := "Open Settings > Providers & Credentials, verify the shared authorization and model, then save again to run a fresh test."
	created := false
	if errors.Is(err, sql.ErrNoRows) {
		result, insertErr := tx.Exec(`
			INSERT INTO issues
				(source, status, tmdb_id, media_type, title, detail, dedupe_key, read, resolution)
			VALUES (?, ?, 0, ?, ?, ?, ?, 0, ?)`,
			SourceSystem, IssueNeedsAdmin, sharedAIHealthMediaType, sharedAIHealthTitle,
			detail, sharedAIHealthDedupeKey, resolution)
		if insertErr != nil {
			return fmt.Errorf("create shared AI health issue: %w", insertErr)
		}
		issueID, err = result.LastInsertId()
		if err != nil {
			return fmt.Errorf("read shared AI health issue id: %w", err)
		}
		created = true
		if _, err := tx.Exec(`
			INSERT INTO issue_messages (issue_id, author_kind, body)
			VALUES (?, ?, ?)`, issueID, AuthorSystem, detail+" "+resolution); err != nil {
			return fmt.Errorf("record shared AI health failure: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("find shared AI health issue: %w", err)
	} else {
		if _, err := tx.Exec(`
			UPDATE issues SET status = ?, read = 0, detail = ?, resolution = ?,
				occurrences = occurrences + 1, updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND closed_at IS NULL`,
			IssueNeedsAdmin, detail, resolution, issueID); err != nil {
			return fmt.Errorf("refresh shared AI health issue: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit shared AI health failure: %w", err)
	}
	if created {
		s.notifyIssueCreatedWithSource(issueID, sharedAIHealthTitle, SourceSystem)
	} else {
		s.pingIssueUpdated(issueID)
	}
	return nil
}

func boundedHealthField(value string) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, "?"))
	if len(value) <= 256 {
		return value
	}
	value = value[:256]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
