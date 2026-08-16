package remediation

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"
)

// Standing auto-approval rules. A rule pre-approves exactly one
// (Import Doctor problem label, action kind, per-kind safety facet) triple for
// auto-detected issues, created only from an admin's explicit "remember this
// approval" on a fix they just reviewed. The facet keeps consequence classes
// apart: manual_import(force) and each remediate_queue sub-action are separate
// opt-ins, so a rule can never widen past what the admin actually saw.
//
// Rules are trusted only while their track record is clean: the first failed
// or unverifiable auto-approved outcome — and any non-resolved pipeline
// verdict of an issue a rule acted on — pauses the rule via
// pauseApprovalRuleTx, and re-arming is an explicit admin action.

// Rule status values (agent_approval_rules.status).
const (
	ApprovalRuleActive = "active"
	ApprovalRulePaused = "paused"
)

// Server-authored automatic pause reasons (fixed copy; shown on the rules
// surface and echoed in the issue-thread evidence message).
const (
	autoRulePausedExecutionFailed   = "An auto-approved fix failed to execute. Review the issue before re-arming this rule."
	autoRulePausedUnverifiedOutcome = "An auto-approved fix ended with an unverified outcome. Verify the arr state before re-arming this rule."
	autoRulePausedPreflightFailed   = "An auto-approved fix was stopped by a failed pre-dispatch safety check. Review the issue before re-arming this rule."
	autoRulePausedIssueUnresolved   = "An issue this rule acted on closed without being resolved. Review the outcome before re-arming this rule."
	// autoRulePausedNeedsAdmin is the give-up path's pause. It used to reuse
	// the "closed without being resolved" copy above, which was false twice
	// over on issue 859: the issue never closed (it parked needs_admin), and
	// the pause note therefore described an event that had not happened.
	autoRulePausedNeedsAdmin = "An issue this rule acted on had to be handed to an administrator. Review the outcome before re-arming this rule."
)

// executionFailedPauseCause is the issue-thread clause for every pause triggered
// by a bad dispatch outcome (failed, partial, unverifiable, unresolved closure).
// A rule stood down because a fix it approved did not work.
const executionFailedPauseCause = "this fix did not complete successfully"

// actionAutoFacet derives the rule key's per-kind safety discriminator from
// the CANONICAL params (validateActionParams output; struct-field order).
// ok=false means the params don't parse for the kind, which disqualifies the
// action from rule matching entirely — never a fallback to a broader rule.
func actionAutoFacet(kind ActionKind, canonical json.RawMessage) (string, bool) {
	switch kind {
	case ActionManualImport:
		var p ManualImportParams
		if err := json.Unmarshal(canonical, &p); err != nil {
			return "", false
		}
		if p.Force {
			return "force", true
		}
		return "", true
	case ActionRemediateQueue:
		var p RemediateQueueParams
		if err := json.Unmarshal(canonical, &p); err != nil || p.Action == "" {
			return "", false
		}
		return p.Action, true
	case ActionDeleteMediaFiles:
		// Deleting files is destructive either way, but blocklisting also stands
		// down a release for good and hands the replacement decision to the
		// service's own failed-download policy. An admin who is happy to
		// auto-approve one has not thereby approved the other.
		var p DeleteMediaFilesParams
		if err := json.Unmarshal(canonical, &p); err != nil {
			return "", false
		}
		if p.Blocklist {
			return "blocklist", true
		}
		return "files_only", true
	case ActionGrabRelease, ActionTriggerSearch, ActionRescan:
		return "", true
	default:
		return "", false
	}
}

// approvalRuleLabel is the fixed, server-authored display name for a rule:
// "<fix> · <problem>". Both halves are code constants (action vocabulary here,
// doctor labels in arr/doctor.go), so the label is safe anywhere fixed copy is.
func approvalRuleLabel(problemKind string, kind ActionKind, facet string) string {
	action := string(kind)
	switch kind {
	case ActionGrabRelease:
		action = "Grab release"
	case ActionRemediateQueue:
		switch facet {
		case "remove":
			action = "Remove from queue"
		// The facet STRINGS are persisted rule keys — renaming one orphans every
		// rule armed on it. Only these display labels may change.
		case "blocklist_search":
			action = "Blocklist the release"
		case "blocklist_only":
			action = "Blocklist, no replacement"
		case "change_category":
			action = "Change download category"
		default:
			action = "Remediate queue"
		}
	case ActionManualImport:
		if facet == "force" {
			action = "Manual import (force)"
		} else {
			action = "Manual import"
		}
	case ActionTriggerSearch:
		action = "Search again"
	case ActionRescan:
		action = "Rescan"
	case ActionDeleteMediaFiles:
		if facet == "blocklist" {
			action = "Delete the wrong files and block that release"
		} else {
			action = "Delete the wrong files"
		}
	}
	return action + " · " + problemKind
}

// ListApprovalRules returns every standing rule, newest first, with the
// creator's username joined for display.
func (s *Service) ListApprovalRules() ([]AgentApprovalRule, error) {
	rows, err := s.db.Query(
		`SELECT r.id, r.problem_kind, r.action_kind, r.action_facet, r.status,
		        r.paused_reason, r.paused_at, r.paused_by_issue_id, r.created_by, u.username, r.seed_action_id,
		        r.approved_count, r.resolved_count, r.last_approved_at, r.last_resolved_at,
		        r.created_at, r.updated_at
		 FROM agent_approval_rules r
		 LEFT JOIN users u ON u.id = r.created_by
		 ORDER BY r.id DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("query approval rules: %w", err)
	}
	defer rows.Close()
	out := []AgentApprovalRule{}
	for rows.Next() {
		rule, err := scanApprovalRule(rows)
		if err != nil {
			return nil, fmt.Errorf("scan approval rule: %w", err)
		}
		out = append(out, *rule)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Status == ApprovalRulePaused && out[i].PausedAt != nil {
			out[i].ApprovedSincePause = s.manualApprovalsSince(&out[i])
		}
	}
	return out, nil
}

// manualApprovalsSince counts HUMAN approvals of a paused rule's exact triple
// since the pause — the server-computed argument for resuming ("you have
// automated this and keep doing it by hand"). Best-effort: zero on any read
// error, facet derived by the same function the sweep matches with.
func (s *Service) manualApprovalsSince(rule *AgentApprovalRule) int64 {
	rows, err := s.db.Query(
		`SELECT COALESCE(NULLIF(a.approved_params, ''), a.params)
		 FROM agent_actions a
		 JOIN issues i ON i.id = a.issue_id
		 WHERE a.kind = ? AND i.problem_kind = ?
		   AND a.decided_by IS NOT NULL AND a.executed_at IS NOT NULL
		   AND a.decided_at >= ?`,
		rule.ActionKind, rule.ProblemKind, rule.PausedAt.UTC(),
	)
	if err != nil {
		return 0
	}
	defer rows.Close()
	var n int64
	for rows.Next() {
		var params string
		if err := rows.Scan(&params); err != nil {
			continue
		}
		facet, ok := actionAutoFacet(ActionKind(rule.ActionKind), json.RawMessage(params))
		if ok && facet == rule.ActionFacet {
			n++
		}
	}
	return n
}

// GetApprovalRule loads one rule with its creator name.
func (s *Service) GetApprovalRule(ruleID int64) (*AgentApprovalRule, error) {
	row := s.db.QueryRow(
		`SELECT r.id, r.problem_kind, r.action_kind, r.action_facet, r.status,
		        r.paused_reason, r.paused_at, r.paused_by_issue_id, r.created_by, u.username, r.seed_action_id,
		        r.approved_count, r.resolved_count, r.last_approved_at, r.last_resolved_at,
		        r.created_at, r.updated_at
		 FROM agent_approval_rules r
		 LEFT JOIN users u ON u.id = r.created_by
		 WHERE r.id = ?`,
		ruleID,
	)
	rule, err := scanApprovalRule(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("approval rule not found")
	}
	if err != nil {
		return nil, fmt.Errorf("load approval rule: %w", err)
	}
	return rule, nil
}

func scanApprovalRule(row rowScanner) (*AgentApprovalRule, error) {
	var (
		rule           AgentApprovalRule
		pausedReason   sql.NullString
		pausedAt       sql.NullTime
		pausedByIssue  sql.NullInt64
		createdBy      sql.NullInt64
		createdByName  sql.NullString
		seedActionID   sql.NullInt64
		lastApprovedAt sql.NullTime
		lastResolvedAt sql.NullTime
	)
	if err := row.Scan(
		&rule.ID, &rule.ProblemKind, &rule.ActionKind, &rule.ActionFacet, &rule.Status,
		&pausedReason, &pausedAt, &pausedByIssue, &createdBy, &createdByName, &seedActionID,
		&rule.ApprovedCount, &rule.ResolvedCount, &lastApprovedAt, &lastResolvedAt,
		&rule.CreatedAt, &rule.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if pausedByIssue.Valid {
		v := pausedByIssue.Int64
		rule.PausedByIssueID = &v
	}
	if pausedReason.Valid && pausedReason.String != "" {
		v := pausedReason.String
		rule.PausedReason = &v
	}
	if pausedAt.Valid {
		v := pausedAt.Time
		rule.PausedAt = &v
	}
	if createdBy.Valid {
		v := createdBy.Int64
		rule.CreatedBy = &v
	}
	if createdByName.Valid && createdByName.String != "" {
		v := createdByName.String
		rule.CreatedByName = &v
	}
	if seedActionID.Valid {
		v := seedActionID.Int64
		rule.SeedActionID = &v
	}
	if lastApprovedAt.Valid {
		v := lastApprovedAt.Time
		rule.LastApprovedAt = &v
	}
	if lastResolvedAt.Valid {
		v := lastResolvedAt.Time
		rule.LastResolvedAt = &v
	}
	rule.Label = approvalRuleLabel(rule.ProblemKind, ActionKind(rule.ActionKind), rule.ActionFacet)
	return &rule, nil
}

// createOrReactivateApprovalRule is the "remember this approval" write. A
// fresh triple inserts an active rule seeded by the approving action; an
// existing (possibly paused) triple only re-arms — creator, seed, and counters
// are preserved so the audit trail survives pause/resume cycles.
func (s *Service) createOrReactivateApprovalRule(adminID, seedActionID int64, problemKind string, kind ActionKind, facet string) (int64, error) {
	if _, err := s.db.Exec(
		`INSERT INTO agent_approval_rules
		   (problem_kind, action_kind, action_facet, status, created_by, seed_action_id)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(problem_kind, action_kind, action_facet) DO UPDATE SET
		   status = ?, paused_reason = NULL, paused_at = NULL, updated_at = CURRENT_TIMESTAMP`,
		problemKind, string(kind), facet, ApprovalRuleActive, sqlNullInt64(adminID), sqlNullInt64(seedActionID),
		ApprovalRuleActive,
	); err != nil {
		return 0, fmt.Errorf("arm approval rule: %w", err)
	}
	var ruleID int64
	if err := s.db.QueryRow(
		"SELECT id FROM agent_approval_rules WHERE problem_kind = ? AND action_kind = ? AND action_facet = ?",
		problemKind, string(kind), facet,
	).Scan(&ruleID); err != nil {
		return 0, fmt.Errorf("load armed approval rule: %w", err)
	}
	return ruleID, nil
}

// PauseApprovalRule is the admin REST pause. Idempotent: pausing an already
// paused rule returns it unchanged (an automatic pause reason is preserved).
func (s *Service) PauseApprovalRule(ruleID int64) (*AgentApprovalRule, error) {
	if _, err := s.db.Exec(
		`UPDATE agent_approval_rules
		 SET status = ?, paused_reason = 'Paused by an administrator.',
		     paused_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status = ?`,
		ApprovalRulePaused, ruleID, ApprovalRuleActive,
	); err != nil {
		return nil, fmt.Errorf("pause approval rule: %w", err)
	}
	return s.GetApprovalRule(ruleID)
}

// ResumeApprovalRule re-arms a paused rule. Idempotent on an active rule.
func (s *Service) ResumeApprovalRule(ruleID int64) (*AgentApprovalRule, error) {
	if _, err := s.db.Exec(
		`UPDATE agent_approval_rules
		 SET status = ?, paused_reason = NULL, paused_at = NULL, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status = ?`,
		ApprovalRuleActive, ruleID, ApprovalRulePaused,
	); err != nil {
		return nil, fmt.Errorf("resume approval rule: %w", err)
	}
	return s.GetApprovalRule(ruleID)
}

// DeleteApprovalRule hard-deletes a rule. Historical attribution survives:
// agent_actions.auto_rule_id is a plain integer, and DTO decoration tolerates
// a missing rule (the label simply becomes null).
func (s *Service) DeleteApprovalRule(ruleID int64) error {
	res, err := s.db.Exec("DELETE FROM agent_approval_rules WHERE id = ?", ruleID)
	if err != nil {
		return fmt.Errorf("delete approval rule: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("approval rule not found")
	}
	return nil
}

// pauseApprovalRuleTx is the single chokepoint for automatic disarm, called
// inside the transaction that records the failure it reacts to, so a failed
// outcome and its pause commit atomically (a crash can never leave a bad
// outcome durable with the rule still armed). It reports whether THIS call
// transitioned the rule so callers notify exactly once. One failure pauses
// today; a future N-strikes policy changes only this helper's body.
func pauseApprovalRuleTx(tx *sql.Tx, ruleID int64, reason string) (bool, error) {
	return pauseApprovalRuleForIssueTx(tx, ruleID, 0, reason)
}

// pauseApprovalRuleForIssueTx records WHICH issue's outcome stood the rule
// down beside the pause itself — the evidence link the rules screen renders.
func pauseApprovalRuleForIssueTx(tx *sql.Tx, ruleID, issueID int64, reason string) (bool, error) {
	res, err := tx.Exec(
		`UPDATE agent_approval_rules
		 SET status = ?, paused_reason = ?, paused_at = CURRENT_TIMESTAMP,
		     paused_by_issue_id = CASE WHEN ? > 0 THEN ? ELSE paused_by_issue_id END,
		     updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status = ?`,
		ApprovalRulePaused, reason, issueID, issueID, ruleID, ApprovalRuleActive,
	)
	if err != nil {
		return false, fmt.Errorf("pause approval rule %d: %w", ruleID, err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// approvalRuleLabelTx recomputes a rule's fixed display label inside the
// pausing transaction (the rule row is guaranteed present there: the pause CAS
// just transitioned it).
func approvalRuleLabelTx(tx *sql.Tx, ruleID int64) (string, error) {
	var problemKind, kind, facet string
	if err := tx.QueryRow(
		"SELECT problem_kind, action_kind, action_facet FROM agent_approval_rules WHERE id = ?",
		ruleID,
	).Scan(&problemKind, &kind, &facet); err != nil {
		return "", fmt.Errorf("load rule label: %w", err)
	}
	return approvalRuleLabel(problemKind, ActionKind(kind), facet), nil
}

// pauseRuleForFailureTx pauses a rule and records the issue-thread evidence
// atomically with the failure that triggered it. Returns whether this call
// transitioned the rule (false = it was already paused or deleted), so callers
// notify post-commit exactly once per real transition.
func pauseRuleForFailureTx(tx *sql.Tx, ruleID, issueID int64, reason string) (bool, error) {
	paused, err := pauseApprovalRuleForIssueTx(tx, ruleID, issueID, reason)
	if err != nil || !paused {
		return false, err
	}
	label, err := approvalRuleLabelTx(tx, ruleID)
	if err != nil {
		return false, err
	}
	if err := insertRulePausedMessageTx(tx, issueID, label, executionFailedPauseCause); err != nil {
		return false, err
	}
	return true, nil
}

// noteRuleApproved bumps a rule's usage counters right after its auto-approval
// wins the execution claim. Best-effort by design: a lost bump under a crash
// is cosmetic and never affects matching.
func (s *Service) noteRuleApproved(ruleID int64) {
	_, _ = s.db.Exec(
		`UPDATE agent_approval_rules
		 SET approved_count = approved_count + 1, last_approved_at = CURRENT_TIMESTAMP,
		     updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		ruleID,
	)
}

// autoRuleIDsForIssueTx returns the distinct rules whose auto-approved fixes
// EXECUTED on this issue — the population issue-terminal accounting judges.
// Failed/unknown auto outcomes are deliberately excluded: they already paused
// their rule inline at transition time, and re-judging them here could
// re-pause a rule an admin resumed in the meantime.
func autoRuleIDsForIssueTx(tx *sql.Tx, issueID int64) ([]int64, error) {
	rows, err := tx.Query(
		`SELECT DISTINCT auto_rule_id FROM agent_actions
		 WHERE issue_id = ? AND auto_rule_id IS NOT NULL AND status = ?`,
		issueID, ActionExecuted,
	)
	if err != nil {
		return nil, fmt.Errorf("query issue auto rules: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// insertRulePausedMessageTx posts the fixed system-authored thread message
// explaining that automation stood down on this issue. Committed atomically
// with the pause itself. cause is a code constant, never caller free text — the
// whole message must stay server-authored copy.
func insertRulePausedMessageTx(tx *sql.Tx, issueID int64, label, cause string) error {
	_, err := tx.Exec(
		`INSERT INTO issue_messages (issue_id, author_kind, author_id, body)
		 VALUES (?, ?, NULL, ?)`,
		issueID, AuthorSystem,
		fmt.Sprintf("The standing auto-approval rule %q was paused because %s. Matching fixes will wait for manual approval until an administrator re-arms it.", label, cause),
	)
	if err != nil {
		return fmt.Errorf("record rule-paused message: %w", err)
	}
	return nil
}

// notifyAutoApprovalPaused emits the admin alert for an automatic disarm,
// after the pausing transaction commits. Every field is server-authored
// (doctor labels + fixed action vocabulary); the push side still renders a
// fully fixed template and uses issue_id only for the deep link.
func (s *Service) notifyAutoApprovalPaused(ruleID, issueID int64) {
	if s.notifier == nil {
		return
	}
	data := map[string]interface{}{
		"rule_id":  ruleID,
		"issue_id": issueID,
	}
	if rule, err := s.GetApprovalRule(ruleID); err == nil {
		data["problem_kind"] = rule.ProblemKind
		data["action_kind"] = rule.ActionKind
		data["action_facet"] = rule.ActionFacet
		data["label"] = rule.Label
		if rule.PausedReason != nil {
			data["paused_reason"] = *rule.PausedReason
		}
	}
	s.notifier.NotifyAdmins("agent_autoapproval_paused", data)
}

// decorateActionsAutoApproval fills the standing-rule DTO fields: the label of
// the rule that made a decision, and the offer inviting an admin to arm (or
// re-arm) a rule from a decidable proposal. One read of the tiny rules table
// serves the whole list; eligibility is computed server-side so clients never
// derive it from untrusted action content.
func (s *Service) decorateActionsAutoApproval(actions []AgentAction) {
	if len(actions) == 0 {
		return
	}
	type ruleInfo struct {
		status string
		label  string
	}
	byID := map[int64]ruleInfo{}
	byKey := map[string]ruleInfo{}
	rows, err := s.db.Query("SELECT id, problem_kind, action_kind, action_facet, status FROM agent_approval_rules")
	if err != nil {
		// Degraded but safe: attribution flags (AutoApproved) came from the row
		// scan; only labels and offers go missing.
		log.Printf("remediation: load approval rules for action decoration: %v", err)
		return
	}
	for rows.Next() {
		var (
			id                       int64
			problemKind, kind, facet string
			info                     ruleInfo
		)
		if rows.Scan(&id, &problemKind, &kind, &facet, &info.status) != nil {
			continue
		}
		info.label = approvalRuleLabel(problemKind, ActionKind(kind), facet)
		byID[id] = info
		byKey[approvalRuleKey(problemKind, kind, facet)] = info
	}
	rows.Close()
	for i := range actions {
		act := &actions[i]
		if act.AutoRuleID != nil {
			if info, ok := byID[*act.AutoRuleID]; ok {
				label := info.label
				act.AutoRuleLabel = &label
			}
		}
		// D2: a user report whose diagnosis landed on a persisted problem label
		// may seed a rule exactly like an auto incident — the subjectivity is in
		// the TRIGGER, and the reporter still owns the close. No label, no offer.
		if act.Status != ActionProposed || !act.CanDecide || act.IssueProblemKind == "" {
			continue
		}
		facet, ok := actionAutoFacet(ActionKind(act.Kind), act.Params)
		if !ok {
			continue
		}
		existing, exists := byKey[approvalRuleKey(act.IssueProblemKind, act.Kind, facet)]
		if exists && existing.status == ApprovalRuleActive {
			// Already automated; the sweep will pick it up — nothing to offer.
			continue
		}
		act.AutoApprovalOffer = &AutoApprovalOffer{
			ProblemKind:           act.IssueProblemKind,
			ActionKind:            act.Kind,
			ActionFacet:           facet,
			Label:                 approvalRuleLabel(act.IssueProblemKind, ActionKind(act.Kind), facet),
			ReactivatesPausedRule: exists && existing.status == ApprovalRulePaused,
		}
	}
}

// approvalRuleKey is the in-memory map key for a rule triple (NUL-joined; none
// of the parts can contain NUL).
func approvalRuleKey(problemKind, kind, facet string) string {
	return problemKind + "\x00" + kind + "\x00" + facet
}

// ApproveActionRemembering arms (creates or reactivates) the standing rule
// seeded by this action, then approves it exactly like ApproveAction. The rule
// is armed FIRST because it is durable intent: even if the immediate approval
// loses a race, the sweep gives the rule effect. The triple derives from the
// params that will actually run (a validated override wins), so overriding to
// force=true and remembering seeds the force rule, never the plain one. A
// remember flag on an action whose triple cannot be derived (user report, no
// problem label, unparseable params — unreachable through the app, which only
// shows the checkbox when the server sent an offer) is dropped with a log
// line: the admin's primary intent is the approval itself.
func (s *Service) ApproveActionRemembering(adminID, actionID int64, override *json.RawMessage) (*AgentAction, error) {
	act, err := s.loadActionForDecision(actionID)
	if err != nil {
		return nil, err
	}
	remembered := false
	// D2: any diagnosis that landed on a persisted label may seed a rule —
	// auto incident or user report alike. No label, nothing to remember.
	if act.Status == ActionProposed && act.IssueProblemKind != "" {
		paramsForRule := act.Params
		if override != nil && len(*override) > 0 && string(*override) != "null" {
			if canonical, verr := validateActionParams(ActionKind(act.Kind), *override); verr == nil {
				paramsForRule = canonical
			}
		}
		if facet, ok := actionAutoFacet(ActionKind(act.Kind), paramsForRule); ok {
			if _, err := s.createOrReactivateApprovalRule(adminID, actionID, act.IssueProblemKind, ActionKind(act.Kind), facet); err != nil {
				return nil, err
			}
			remembered = true
		}
	}
	if !remembered {
		log.Printf("remediation: remember flag dropped for action %d (no derivable rule triple)", actionID)
	}
	return s.ApproveAction(adminID, actionID, override)
}

// sweepAutoApprovals approves every gated proposal a standing rule matches. It
// runs on the observation sweeper's clock, ordered before flushActionAlerts,
// so a proposal approved here also drops its owed admin push (flushActionAlerts
// discards queue rows whose issue no longer has a 'proposed' action, and the
// hold-down guarantees the drop happens before delivery). Rule creation is
// therefore retroactive for free: matching proposals already parked when the
// admin arms a rule are approved on the next tick.
func (s *Service) sweepAutoApprovals(now time.Time) {
	settings := s.Settings()
	if !settings.Enabled || settings.Mode != ModeSupervised {
		return
	}
	var haveRules bool
	if err := s.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM agent_approval_rules WHERE status = ?)", ApprovalRuleActive,
	).Scan(&haveRules); err != nil || !haveRules {
		return
	}
	// The LIMIT bounds serial arr I/O per tick (the alert flushes share this
	// goroutine); leftovers are picked up next tick. The one-proposed-per-issue
	// invariant means at most one approval per issue per tick, which naturally
	// serializes multi-step issues.
	rows, err := s.db.Query(
		`SELECT a.id, a.kind, a.params, i.problem_kind
		 FROM agent_actions a JOIN issues i ON i.id = a.issue_id
		 WHERE a.status = ? AND i.closed_at IS NULL AND i.status = ?
		   AND i.source IN (?, ?) AND i.problem_kind IS NOT NULL AND i.problem_kind != ''
		   AND EXISTS (SELECT 1 FROM agent_runs r WHERE r.id = a.run_id AND r.status = 'waiting_approval')
		   AND EXISTS (SELECT 1 FROM agent_approval_rules ru
		               WHERE ru.status = ? AND ru.problem_kind = i.problem_kind AND ru.action_kind = a.kind)
		 ORDER BY a.id LIMIT 25`,
		ActionProposed, IssueAwaitingApproval, SourceAuto, SourceUser, ApprovalRuleActive,
	)
	if err != nil {
		log.Printf("remediation: query auto-approval candidates: %v", err)
		return
	}
	type candidate struct {
		actionID    int64
		kind        string
		params      string
		problemKind string
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if rows.Scan(&c.actionID, &c.kind, &c.params, &c.problemKind) == nil {
			candidates = append(candidates, c)
		}
	}
	rows.Close()
	for _, c := range candidates {
		// The facet must be derived from the typed params, never SQL JSON
		// probing, so matching can only ever be as broad as the canonical form.
		facet, ok := actionAutoFacet(ActionKind(c.kind), json.RawMessage(c.params))
		if !ok {
			continue
		}
		var ruleID int64
		err := s.db.QueryRow(
			`SELECT id FROM agent_approval_rules
			 WHERE status = ? AND problem_kind = ? AND action_kind = ? AND action_facet = ?`,
			ApprovalRuleActive, c.problemKind, c.kind, facet,
		).Scan(&ruleID)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			log.Printf("remediation: match auto-approval rule for action %d: %v", c.actionID, err)
			continue
		}
		if _, err := s.autoApproveAction(ruleID, c.actionID); err != nil {
			// A decision/closure/recovery race is normal: someone else owned the
			// proposal first and the CAS or preflight said so. A repeated remedy
			// is an expected stand-down, already recorded on the rule and in the
			// issue thread. Anything else is worth a log line, but never stops
			// the rest of the sweep.
			if errors.Is(err, ErrActionDecisionConflict) || errors.Is(err, errRemedyAlreadyApplied) {
				continue
			}
			log.Printf("remediation: auto-approve action %d via rule %d: %v", c.actionID, ruleID, err)
		}
	}
}

// announceBootPausedRules closes the one silent stand-down left: the schema
// layer pauses rules whose auto-approved fix a restart interrupted, but the db
// package has no notifier, so until now the only trace was the rules screen.
// Called once at worker start (never on the ticker), it announces rules whose
// pause the boot repair just wrote — recognized by the fixed restart copy and
// a paused_at within the boot window — through the same thread-message + push
// path every runtime pause uses. Autonomy never stands down silently.
func (s *Service) announceBootPausedRules() {
	rows, err := s.db.Query(
		`SELECT id FROM agent_approval_rules
		 WHERE status = 'paused'
		   AND paused_reason LIKE 'Cantinarr restarted while an auto-approved fix%'
		   AND paused_at >= datetime('now', '-10 minutes')`,
	)
	if err != nil {
		return
	}
	var ruleIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			ruleIDs = append(ruleIDs, id)
		}
	}
	rows.Close()
	for _, ruleID := range ruleIDs {
		s.notifyAutoApprovalPaused(ruleID, 0)
	}
}

// RuleCandidate is one (problem, fix, facet) triple the admin has actually
// approved by hand and could automate — the arm-from-catalog surface, so
// trusting a repeat fix no longer waits for the next proposal's checkbox.
type RuleCandidate struct {
	ProblemKind   string     `json:"problem_kind"`
	ActionKind    string     `json:"action_kind"`
	ActionFacet   string     `json:"action_facet"`
	Label         string     `json:"label"`
	ApprovedCount int64      `json:"approved_count"`
	LastApproved  *time.Time `json:"last_approved_at"`
}

// ListRuleCandidates aggregates every triple with at least one HUMAN-approved
// execution and no active rule. Grounded by construction: a triple nobody has
// approved by hand cannot appear, so arming from here is "remember, later" —
// never inventing a rule from thin air.
func (s *Service) ListRuleCandidates() ([]RuleCandidate, error) {
	rows, err := s.db.Query(
		`SELECT i.problem_kind, a.kind, COALESCE(NULLIF(a.approved_params, ''), a.params), a.decided_at
		 FROM agent_actions a JOIN issues i ON i.id = a.issue_id
		 WHERE a.decided_by IS NOT NULL AND a.executed_at IS NOT NULL AND a.status = ?
		   AND i.problem_kind IS NOT NULL AND i.problem_kind != ''`,
		ActionExecuted,
	)
	if err != nil {
		return nil, fmt.Errorf("query rule candidates: %w", err)
	}
	defer rows.Close()
	type agg struct {
		count int64
		last  *time.Time
	}
	byKey := map[string]*agg{}
	meta := map[string]RuleCandidate{}
	for rows.Next() {
		var problem, kind, params string
		var decidedAt sql.NullTime
		if err := rows.Scan(&problem, &kind, &params, &decidedAt); err != nil {
			return nil, err
		}
		facet, ok := actionAutoFacet(ActionKind(kind), json.RawMessage(params))
		if !ok {
			continue
		}
		key := approvalRuleKey(problem, kind, facet)
		if _, seen := byKey[key]; !seen {
			byKey[key] = &agg{}
			meta[key] = RuleCandidate{
				ProblemKind: problem, ActionKind: kind, ActionFacet: facet,
				Label: approvalRuleLabel(problem, ActionKind(kind), facet),
			}
		}
		byKey[key].count++
		if decidedAt.Valid && (byKey[key].last == nil || decidedAt.Time.After(*byKey[key].last)) {
			v := decidedAt.Time
			byKey[key].last = &v
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Subtract triples that already have an ACTIVE rule (paused ones stay
	// listed — arming from the catalog reactivates, same as remember).
	ruleRows, err := s.db.Query(
		"SELECT problem_kind, action_kind, action_facet FROM agent_approval_rules WHERE status = ?",
		ApprovalRuleActive,
	)
	if err != nil {
		return nil, fmt.Errorf("query active rules: %w", err)
	}
	defer ruleRows.Close()
	for ruleRows.Next() {
		var problem, kind, facet string
		if err := ruleRows.Scan(&problem, &kind, &facet); err != nil {
			return nil, err
		}
		delete(byKey, approvalRuleKey(problem, kind, facet))
	}
	out := make([]RuleCandidate, 0, len(byKey))
	for key, a := range byKey {
		c := meta[key]
		c.ApprovedCount = a.count
		c.LastApproved = a.last
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ApprovedCount > out[j].ApprovedCount })
	return out, nil
}

// ArmRuleFromCatalog creates (or reactivates) a rule for a triple the admin
// has actually approved by hand — the grounding is re-checked here, server
// side, whatever the client claimed.
func (s *Service) ArmRuleFromCatalog(adminID int64, problemKind, actionKind, actionFacet string) (int64, error) {
	candidates, err := s.ListRuleCandidates()
	if err != nil {
		return 0, err
	}
	grounded := false
	for _, c := range candidates {
		if c.ProblemKind == problemKind && c.ActionKind == actionKind && c.ActionFacet == actionFacet {
			grounded = true
			break
		}
	}
	if !grounded {
		return 0, fmt.Errorf("this exact fix has never been approved by hand; approve it once on a real proposal first")
	}
	return s.createOrReactivateApprovalRule(adminID, 0, problemKind, ActionKind(actionKind), actionFacet)
}
