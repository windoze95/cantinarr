package remediation

import (
	"fmt"

	"github.com/windoze95/cantinarr-server/internal/ai"
)

// RecordModelFallback creates one durable administrator notification for a
// profile/model/replacement event, including after the issue was closed or the
// server restarted. Repeated requests do not reopen it or send another push.
func (s *Service) RecordModelFallback(event ai.ModelFallback) error {
	if len(event.EventKey) != 64 {
		return fmt.Errorf("invalid model fallback event key")
	}
	key := "system:ai-model-fallback:" + event.Source + ":" + event.EventKey
	owner := "the shared profile"
	if event.Source == "personal" {
		owner = fmt.Sprintf("user #%d's personal profile", event.UserID)
	}
	outcome := "The replacement completed the request."
	if !event.Succeeded {
		outcome = "The replacement attempt also failed. No further replacement was attempted."
	}
	detail := fmt.Sprintf("AI model fallback for %s (%s). Provider: %s. Selected model: %s. Replacement: %s. %s Recommendation: %s. %s %s",
		owner, event.Scope, event.Provider, event.SelectedModel, event.ReplacementModel, event.Reason, event.RecommendationSource, event.Differences, outcome)
	resolution := "Review the saved selection in Settings > Providers & Credentials. The saved model was not changed."
	if event.Source == "personal" {
		resolution = fmt.Sprintf("Ask user #%d to review Settings > AI Access in their own account. Administrators cannot edit another user's personal provider. The saved model was not changed.", event.UserID)
	}
	resolution += " Provider settings: " + event.SettingsPath
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`INSERT OR IGNORE INTO issues
		(source, status, tmdb_id, media_type, title, detail, dedupe_key, resolution)
		VALUES (?, ?, 0, 'system', 'AI model fallback', ?, ?, ?)`, SourceSystem, IssueNeedsAdmin, detail, key, resolution)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 0 {
		return nil
	}
	issueID, err := result.LastInsertId()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO issue_messages (issue_id, author_kind, body) VALUES (?, ?, ?)`, issueID, AuthorSystem, detail+" "+resolution); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.notifyIssueCreatedWithSource(issueID, "AI model fallback", SourceSystem)
	return nil
}
