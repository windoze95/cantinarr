package remediation

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/arr"
)

// Remediation memory. A fix is only "already tried" against the exact arr
// download it acted on, so every executed queue-scoped action records that
// download id and every later reader keys on it.
//
// The motivating incident: a stalled torrent was removed + blocklisted, the arr
// re-grabbed the IDENTICAL release 48 seconds later (its blocklist matches on
// the release title, which differed by punctuation between the two indexer
// listings), and a standing rule then auto-approved the same fix again on the
// same release. Nothing in the system could see that the first fix had not
// held, because nothing recorded what a fix had acted on.
//
// Two consumers, deliberately separate:
//   - autoApprovalWouldRepeatFailedRemedy is the ENFORCEMENT boundary. A rule
//     may not silently replay a remedy against a release it already ran on;
//     that proposal waits for a human who can see the history.
//   - priorRemediationAttempts is the agent's CONTEXT. It carries only
//     server-authored identity and clock fields into the system prompt, never
//     arr free text (release names, error strings), which stays at user-role
//     trust exactly as buildSystemPrompt documents.

// errRemedyAlreadyApplied means a standing rule's proposal repeats a fix that
// already executed against the download the issue is still holding. It is an
// expected outcome, not a failure: the sweep skips quietly and the proposal
// stays visible for manual approval.
var errRemedyAlreadyApplied = errors.New("remedy already applied to this download")

// autoRulePausedRepeatIneffective is the fixed pause copy for a rule whose fix
// would have been replayed against a release it already ran on.
const autoRulePausedRepeatIneffective = "A fix this rule approved was already applied to this exact download and the problem came back. Review it before re-arming this rule."

// repeatPauseCause is the issue-thread clause for the same case. The generic
// "did not complete successfully" copy would be wrong here: the fix completed,
// it just did not hold.
const repeatPauseCause = "the same fix had already been applied to this exact download and the problem came back"

// actsOnOneDownload reports whether an action of this kind, with these canonical
// params, targets exactly ONE arr download. Only those actions can be attributed
// to a release, so only those are recorded and matched. Library-wide kinds
// (trigger_search, rescan) deliberately never gain a target.
func actsOnOneDownload(kind ActionKind, canonical json.RawMessage) bool {
	switch kind {
	case ActionRemediateQueue:
		var p RemediateQueueParams
		return json.Unmarshal(canonical, &p) == nil && p.QueueID > 0
	case ActionManualImport:
		var p ManualImportParams
		return json.Unmarshal(canonical, &p) == nil && p.QueueID > 0
	case ActionGrabRelease:
		// Only the replaced queue item is an existing download this action acts
		// on; the release it grabs afterwards is a new one the arr assigns.
		var p GrabReleaseParams
		return json.Unmarshal(canonical, &p) == nil && p.QueueIDToReplace > 0
	default:
		return false
	}
}

// issueDownloadIdentity reads the release an issue is currently pinned to. Call
// it IMMEDIATELY BEFORE dispatch, never after: the observation sweeper rewrites
// issues.download_id whenever the arr swaps the release, and an arr round-trip
// is long enough for that to land. Reading before means the recorded target is
// the same value the Executor's identity gate is about to validate against.
func (s *Service) issueDownloadIdentity(issueID int64) string {
	var downloadID sql.NullString
	if err := s.db.QueryRow("SELECT download_id FROM issues WHERE id = ?", issueID).Scan(&downloadID); err != nil {
		log.Printf("remediation: read issue %d download identity: %v", issueID, err)
		return ""
	}
	return strings.TrimSpace(downloadID.String)
}

// noteActionTargetDownload records the arr download a dispatched action acted on.
//
// Why the issue's download identity is the right source: the Executor's gate
// (validateDownloadIdentity) REFUSES to dispatch a queue-scoped action whose
// live queue row carries any other download id, so an action that reached the
// arr at all provably acted on that download. Copying it onto the action freezes
// the fact; issues.download_id itself keeps tracking whatever the arr holds now
// and can never answer "what did that past fix touch?".
//
// Best-effort by design: a lost stamp costs the repeat guard one lap, never the
// correctness of the dispatch that just happened.
func (s *Service) noteActionTargetDownload(actionID int64, kind ActionKind, canonical json.RawMessage, downloadID string) {
	if downloadID == "" || !actsOnOneDownload(kind, canonical) {
		return
	}
	if _, err := s.db.Exec(
		"UPDATE agent_actions SET target_download_id = ? WHERE id = ? AND target_download_id IS NULL",
		downloadID, actionID,
	); err != nil {
		log.Printf("remediation: record action %d target download: %v", actionID, err)
	}
}

// remediationAttempt is one already-dispatched fix on an issue, paired with the
// arr's own answer to whether it held.
type remediationAttempt struct {
	kind       ActionKind
	facet      string
	downloadID string
	executedAt time.Time
	// reAddedAt is set when the arr put this SAME download back after the fix
	// ran (issue_observation_downloads tracks the newest arr Added boundary per
	// download). That is the machine-checkable proof the fix did not hold.
	reAddedAt time.Time
}

// heldQuestionable reports whether the arr re-added this exact download after
// the fix dispatched.
func (a remediationAttempt) recurred() bool {
	return !a.reAddedAt.IsZero() && a.reAddedAt.After(a.executedAt)
}

// priorRemediationAttempts returns every dispatched, download-attributed fix on
// an issue, oldest first, joined to the arr's re-add boundary for that same
// download. Failed actions are excluded: a fix that never reached the arr is
// not something the agent already tried.
func (s *Service) priorRemediationAttempts(issueID int64) ([]remediationAttempt, error) {
	rows, err := s.db.Query(
		`SELECT a.kind, COALESCE(NULLIF(a.approved_params, ''), a.params),
		        a.target_download_id, a.executed_at, d.arr_added_at
		 FROM agent_actions a
		 LEFT JOIN issue_observation_downloads d
		   ON d.issue_id = a.issue_id AND lower(d.download_id) = lower(a.target_download_id)
		 WHERE a.issue_id = ? AND a.status IN (?, ?)
		   AND a.executed_at IS NOT NULL
		   AND a.target_download_id IS NOT NULL AND a.target_download_id != ''
		 ORDER BY a.executed_at, a.id`,
		issueID, ActionExecuted, ActionOutcomeUnknown,
	)
	if err != nil {
		return nil, fmt.Errorf("query prior remediation attempts: %w", err)
	}
	defer rows.Close()
	var out []remediationAttempt
	for rows.Next() {
		var kind, params, downloadID string
		var executedAt sql.NullTime
		var reAddedAt sql.NullTime
		if err := rows.Scan(&kind, &params, &downloadID, &executedAt, &reAddedAt); err != nil {
			return nil, fmt.Errorf("scan prior remediation attempt: %w", err)
		}
		attempt := remediationAttempt{
			kind:       ActionKind(kind),
			downloadID: downloadID,
			executedAt: executedAt.Time,
			reAddedAt:  reAddedAt.Time,
		}
		if facet, ok := actionAutoFacet(attempt.kind, json.RawMessage(params)); ok {
			attempt.facet = facet
		}
		out = append(out, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read prior remediation attempts: %w", err)
	}
	return out, nil
}

// autoApprovalWouldRepeatFailedRemedy reports whether a proposed action replays
// a (kind, facet) that already dispatched against the download the issue is
// STILL holding. That pairing is the loop: the fix ran, the same release is
// back, and repeating it can only produce the same outcome.
//
// It compares against issues.download_id — the release the arr holds now — so a
// genuinely new release that inherited the same problem is never mistaken for a
// repeat. An issue with no download identity can't be judged and never matches.
func (s *Service) autoApprovalWouldRepeatFailedRemedy(actionID int64) (bool, error) {
	var issueID int64
	var kind, params string
	err := s.db.QueryRow(
		`SELECT issue_id, kind, COALESCE(NULLIF(approved_params, ''), params)
		 FROM agent_actions WHERE id = ?`, actionID,
	).Scan(&issueID, &kind, &params)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load action %d for repeat check: %w", actionID, err)
	}
	facet, ok := actionAutoFacet(ActionKind(kind), json.RawMessage(params))
	if !ok {
		return false, nil
	}
	rows, err := s.db.Query(
		`SELECT COALESCE(NULLIF(a.approved_params, ''), a.params)
		 FROM agent_actions a JOIN issues i ON i.id = a.issue_id
		 WHERE a.issue_id = ? AND a.id != ? AND a.kind = ? AND a.status IN (?, ?)
		   AND a.target_download_id IS NOT NULL AND a.target_download_id != ''
		   AND i.download_id IS NOT NULL AND i.download_id != ''
		   AND lower(a.target_download_id) = lower(i.download_id)`,
		issueID, actionID, kind, ActionExecuted, ActionOutcomeUnknown,
	)
	if err != nil {
		return false, fmt.Errorf("query repeated remedies: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var priorParams string
		if err := rows.Scan(&priorParams); err != nil {
			return false, fmt.Errorf("scan repeated remedy: %w", err)
		}
		// The facet must come from the typed params, exactly as rule matching
		// derives it, so "already tried" can never be broader than the opt-in
		// an admin actually armed.
		if prior, ok := actionAutoFacet(ActionKind(kind), json.RawMessage(priorParams)); ok && prior == facet {
			return true, nil
		}
	}
	return false, rows.Err()
}

// issueHadExecutedFacet reports whether a fix of this exact kind and facet ever
// dispatched on an issue. The facet comes from the typed params, never a SQL
// JSON probe, so a match can only ever be as narrow as the canonical form —
// the same discipline rule matching follows.
func (s *Service) issueHadExecutedFacet(issueID int64, kind ActionKind, facet string) (bool, error) {
	rows, err := s.db.Query(
		`SELECT COALESCE(NULLIF(approved_params, ''), params) FROM agent_actions
		 WHERE issue_id = ? AND kind = ? AND status = ? AND executed_at IS NOT NULL`,
		issueID, string(kind), ActionExecuted,
	)
	if err != nil {
		return false, fmt.Errorf("query executed %s fixes: %w", kind, err)
	}
	defer rows.Close()
	for rows.Next() {
		var params string
		if err := rows.Scan(&params); err != nil {
			return false, fmt.Errorf("scan executed %s fix: %w", kind, err)
		}
		if got, ok := actionAutoFacet(kind, json.RawMessage(params)); ok && got == facet {
			return true, nil
		}
	}
	return false, rows.Err()
}

// issueHasExecutedFix reports whether ANY approved fix has actually reached the
// arr for this issue, of any kind.
//
// Deliberately broader than priorRemediationAttempts, which only sees fixes
// attributable to one download. A fix that acts on the library rather than on a
// queue row — deleting files the arr already imported — has no download to
// attribute, so it is invisible there. The difference matters exactly once: at
// the point where a user-reported issue is handed to an admin, where "I applied
// the fix, please confirm" and "I could not do anything" must not read alike.
func (s *Service) issueHasExecutedFix(issueID int64) (bool, error) {
	var count int
	if err := s.db.QueryRow(
		`SELECT COUNT(1) FROM agent_actions
		 WHERE issue_id = ? AND status IN (?, ?) AND executed_at IS NOT NULL`,
		issueID, ActionExecuted, ActionOutcomeUnknown,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("count executed fixes: %w", err)
	}
	return count > 0, nil
}

// upgradeAbandonProven reports that this incident ended the way a deliberately
// abandoned upgrade is supposed to end: the server dropped a dead release that
// was only ever going to replace a file the library already had, and that file
// is still exactly the file it was.
//
// The ordinary recovery proof cannot see this. It was written for missing media,
// where recovery means a NEW file arrives with an import receipt to bind it — so
// it reads "the library file never changed" as failure and escalates. For an
// abandoned upgrade the unchanged file IS the successful outcome: nothing was
// ever unwatchable, and the point of the fix was to stop chasing a replacement.
//
// Three things must all hold, and each is typed state rather than judgment:
// the incident's baseline recorded a real file, the library still holds that
// exact file, and the server itself dispatched a blocklist_only fix here. The
// last one is what keeps this honest — without it, "queue row gone, file
// unchanged" also describes a download that quietly died, which must still reach
// an administrator. Callers must additionally have proven the exact queue target
// is absent; this answers only "was the outcome the intended one".
func (s *Service) upgradeAbandonProven(issue *Issue) (bool, error) {
	var baselineHasFile sql.NullBool
	var baselineFileID sql.NullInt64
	var captured sql.NullTime
	if err := s.db.QueryRow(
		"SELECT baseline_has_file, baseline_file_id, baseline_captured_at FROM issue_observations WHERE issue_id = ?",
		issue.ID,
	).Scan(&baselineHasFile, &baselineFileID, &captured); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("read baseline for upgrade abandon: %w", err)
	}
	if !captured.Valid || !baselineHasFile.Valid || !baselineHasFile.Bool ||
		!baselineFileID.Valid || baselineFileID.Int64 <= 0 {
		return false, nil // no proven pre-incident copy: this was never an upgrade.
	}
	abandoned, err := s.issueHadExecutedFacet(issue.ID, ActionRemediateQueue, arr.ActionBlocklistOnly)
	if err != nil || !abandoned {
		return false, err
	}
	current, err := s.exactIssueFileState(issue)
	if err != nil {
		return false, err
	}
	if !current.known || !current.hasFile || current.fileID <= 0 {
		return false, nil
	}
	return current.fileID == baselineFileID.Int64, nil
}

// bookRemoveWithoutReplacementProven reports that a book incident ended the way
// a removed dead download with no available replacement is supposed to end: the
// want had no file before the incident, the server itself dispatched a
// blocklisting fix, and the want still has no file now that the queue is empty.
//
// The ordinary recovery proof cannot see this outcome any more than it can see
// an abandoned upgrade — it was written for missing media, where recovery means
// a NEW file arrives with an import receipt to bind it. Here no file arriving
// IS the outcome: the dispatched fix removed the only live attempt, the arr's
// own replacement search ran and found nothing to grab, and the want stays
// monitored. Without a terminal for that shape the incident re-promoted
// forever and finally told an admin the fix "could not be verified" — the one
// claim in the story that was false (issue 859, 2026-08-13).
//
// Every condition is typed state rather than judgment, mirroring
// upgradeAbandonProven: a captured baseline with NO file (this was a want, not
// an upgrade), a dispatched blocklist facet (plain remove is excluded — an
// unblocklisted release can simply be re-grabbed, which is the #359 repeat
// loop, not an ending), and a known current file state that is still empty. A
// replacement the arr did grab appears as a live queue row for the same scope,
// so callers must additionally have proven the exact queue target absent; this
// answers only "was the outcome the intended one".
//
// Book-only on purpose. Movies and TV can end in the same shape, but their
// stalled downloads travel other proven paths (upgrade abandonment, replacement
// grabs their richer indexer pool actually finds), and this proof was audited
// against Chaptarr's behavior specifically — its failed-download handling
// re-searches immediately and definitively. Widening it is a deliberate later
// change, not a default.
func (s *Service) bookRemoveWithoutReplacementProven(issue *Issue) (bool, error) {
	if issue == nil || issue.MediaType != "book" || issue.Source != SourceAuto {
		return false, nil
	}
	var baselineHasFile sql.NullBool
	var captured sql.NullTime
	if err := s.db.QueryRow(
		"SELECT baseline_has_file, baseline_captured_at FROM issue_observations WHERE issue_id = ?",
		issue.ID,
	).Scan(&baselineHasFile, &captured); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("read baseline for removed-no-replacement: %w", err)
	}
	if !captured.Valid || !baselineHasFile.Valid || baselineHasFile.Bool {
		return false, nil // no baseline, or the library had a copy: not this shape.
	}
	for _, facet := range []string{arr.ActionBlocklistSearch, arr.ActionBlocklistOnly} {
		dispatched, err := s.issueHadExecutedFacet(issue.ID, ActionRemediateQueue, facet)
		if err != nil {
			return false, err
		}
		if !dispatched {
			continue
		}
		current, err := s.exactIssueFileState(issue)
		if err != nil {
			return false, err
		}
		return current.known && !current.hasFile, nil
	}
	return false, nil
}

// pauseRuleForRepeatedRemedy disarms a rule that was about to replay an
// ineffective fix, recording the thread evidence in the same transaction.
func (s *Service) pauseRuleForRepeatedRemedy(ruleID, issueID int64) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	paused, err := pauseApprovalRuleTx(tx, ruleID, autoRulePausedRepeatIneffective)
	if err != nil || !paused {
		return false, err
	}
	label, err := approvalRuleLabelTx(tx, ruleID)
	if err != nil {
		return false, err
	}
	if err := insertRulePausedMessageTx(tx, issueID, label, repeatPauseCause); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
