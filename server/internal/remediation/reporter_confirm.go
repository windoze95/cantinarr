package remediation

import (
	"context"
	"fmt"
)

// Letting the person who reported a problem be the one who says it is fixed.
//
// A user-reported issue can never close itself. That is deliberate and correct:
// "this is the wrong episode" is a judgment, and the server has no way to prove
// a judgment satisfied — runner.go's conclusion gate refuses every non-auto
// source for exactly that reason. But the consequence was that EVERY user
// report ended at needs_admin, including the ones the agent diagnosed, repaired,
// and verified. The admin's job became rubber-stamping someone else's opinion
// about content they had not watched.
//
// The reporter is the one who can answer. The agent already tells them so —
// "have a look, and close this out if the content is what you expected" — and
// until now there was no way for them to do it. This is that way.
//
// It is an explicit action, never an inference from a reply. A free-text "yeah
// looks good" read by a model is not a closure decision; a button the reporter
// pressed is.

// reporterConfirmedResolution is the closing note. Server-authored: the reporter
// supplies the decision, not the wording.
const reporterConfirmedResolution = "The reporter confirmed the fix worked."

// reporterConfirmedMessage is the thread message recording the confirmation,
// attributed to the reporter because it is their judgment.
const reporterConfirmedMessage = "I checked, and this is fixed."

// CanReporterConfirmFix reports whether the reporter of this issue may close it
// as fixed right now.
//
// Three conditions, and each rules out a different wrong closure:
//   - the issue is a user report and still open — an auto incident has its own
//     typed proof and does not need an opinion, and a closed issue is done
//   - a fix actually reached the arr — otherwise "confirmed fixed" would record
//     approval of something that never happened
//   - nothing is mid-dispatch — a confirmation racing an executing action would
//     close over an outcome nobody has seen yet
func (s *Service) CanReporterConfirmFix(issue *Issue) (bool, error) {
	if issue == nil || issue.Source != SourceUser || issue.ClosedAt != nil {
		return false, nil
	}
	applied, err := s.issueHasExecutedFix(issue.ID)
	if err != nil || !applied {
		return false, err
	}
	var executing int
	if err := s.db.QueryRow(
		"SELECT COUNT(1) FROM agent_actions WHERE issue_id = ? AND status = ?",
		issue.ID, ActionExecuting,
	).Scan(&executing); err != nil {
		return false, fmt.Errorf("count executing fixes: %w", err)
	}
	return executing == 0, nil
}

// ReporterConfirmFix closes an issue on the reporter's own word that the fix
// worked. reporterID must be the issue's reporter — an administrator has
// /resolve, and recording their verdict as the reporter's would be a lie in the
// audit trail.
func (s *Service) ReporterConfirmFix(ctx context.Context, issueID, reporterID int64) error {
	issue, err := s.GetIssue(issueID)
	if err != nil {
		return err
	}
	if issue.ReporterID == nil || *issue.ReporterID != reporterID {
		return fmt.Errorf("only the reporter can confirm this issue is fixed")
	}
	allowed, err := s.CanReporterConfirmFix(issue)
	if err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("this issue cannot be closed as fixed right now")
	}
	// The confirmation message is written inside concludeIssueAggregate, not
	// here: it must land in the same transaction as the close, or a double tap
	// threads it twice and a confirmation that loses a race with a starting
	// dispatch leaves it on a still-open issue. PostReply is not an option
	// either way — it refuses a closed issue outright.
	transitioned, err := s.concludeIssueAggregate(ctx, issueID, IssueResolved,
		reporterConfirmedResolution, ResolutionReporterConfirmed,
		issueClosureOptions{conflictIfClosed: true, reporterID: reporterID})
	if err != nil {
		return err
	}
	if !transitioned {
		return fmt.Errorf("this issue was already closed")
	}
	return nil
}
