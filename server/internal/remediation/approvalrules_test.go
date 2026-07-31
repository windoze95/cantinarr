package remediation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/db"
)

// autoApprovalFixture is approvalFixture's shape for the surface standing rules
// act on: an AUTO-detected issue carrying an Import Doctor problem label, whose
// parked run proposed a manual_import fix. Returns the notifier so tests can
// assert exactly which admin events fired.
func autoApprovalFixture(t *testing.T) (*Service, *fakeExecutor, *fakeNotifier, int64, int64) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	notifier := &fakeNotifier{}
	svc := NewService(database, nil, nil, notifier)
	fx := &fakeExecutor{out: "Imported the downloaded files."}
	svc.executor = fx

	if _, err := database.Exec("INSERT INTO users (id, username, password_hash, role) VALUES (?, 'admin', '', 'admin')", testAdminID); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO service_instances (id, service_type, name, url, api_key)
		 VALUES ('radarr-main', 'radarr', 'Main Movies', 'http://radarr.test', 'key')`,
	); err != nil {
		t.Fatalf("seed target instance: %v", err)
	}
	if _, err := svc.SetSettings(Settings{Enabled: true, Mode: ModeSupervised, MaxSteps: 12, MaxTurnTokens: 1024, MaxWallClockSecs: 30, DailyRunCap: 50}); err != nil {
		t.Fatalf("set settings: %v", err)
	}

	res, err := database.Exec(
		`INSERT INTO issues (source, status, media_type, tmdb_id, title, detail, problem_kind, instance_id, download_id, arr_queue_id)
		 VALUES ('auto','awaiting_approval','movie',42,'Test Movie','stuck waiting to import','Waiting to import','radarr-main','dl-1',7)`,
	)
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issueID, _ := res.LastInsertId()

	toolUseID := "toolu_propose_auto_1"
	history := []map[string]any{
		{"role": "user", "content": []map[string]any{{"type": "text", "text": "investigate"}}},
		{"role": "assistant", "content": []map[string]any{{"type": "tool_use", "id": toolUseID, "name": "propose_action", "input": map[string]any{}}}},
		{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": toolUseID, "name": "propose_action", "content": "Proposal #1 recorded; awaiting admin approval."}}},
	}
	htData, _ := json.Marshal(history)
	runRes, err := database.Exec(
		"INSERT INTO agent_runs (issue_id, trigger, status, model, step_count, transcript_json) VALUES (?, 'auto', ?, 'claude-haiku-4-5', 3, ?)",
		issueID, runStatusWaitingApproval, string(htData),
	)
	if err != nil {
		t.Fatalf("seed run: %v", err)
	}
	runID, _ := runRes.LastInsertId()

	params := `{"media_type":"movie","queue_id":7}`
	fp := fingerprint(issueID, runID, toolUseID, ActionManualImport, json.RawMessage(params))
	actRes, err := database.Exec(
		"INSERT INTO agent_actions (issue_id, run_id, tool_use_id, kind, params, rationale, risk, status, fingerprint) VALUES (?, ?, ?, ?, ?, 'stuck import', 'mutating', ?, ?)",
		issueID, runID, toolUseID, string(ActionManualImport), params, ActionProposed, fp,
	)
	if err != nil {
		t.Fatalf("seed action: %v", err)
	}
	actionID, _ := actRes.LastInsertId()
	return svc, fx, notifier, issueID, actionID
}

func ruleRow(t *testing.T, svc *Service, ruleID int64) AgentApprovalRule {
	t.Helper()
	rule, err := svc.GetApprovalRule(ruleID)
	if err != nil {
		t.Fatalf("GetApprovalRule(%d): %v", ruleID, err)
	}
	return *rule
}

func countAdminEvents(notifier *fakeNotifier, event string) int {
	n := 0
	for _, e := range notifier.adminEvents {
		if e == event {
			n++
		}
	}
	return n
}

func TestActionAutoFacetDerivation(t *testing.T) {
	cases := []struct {
		name   string
		kind   ActionKind
		params string
		facet  string
		ok     bool
	}{
		{"manual import plain", ActionManualImport, `{"media_type":"movie","queue_id":7}`, "", true},
		{"manual import force", ActionManualImport, `{"media_type":"movie","queue_id":7,"force":true}`, "force", true},
		{"manual import malformed", ActionManualImport, `{`, "", false},
		{"remediate remove", ActionRemediateQueue, `{"media_type":"movie","queue_id":7,"action":"remove"}`, "remove", true},
		{"remediate blocklist", ActionRemediateQueue, `{"media_type":"movie","queue_id":7,"action":"blocklist_search"}`, "blocklist_search", true},
		{"remediate change category", ActionRemediateQueue, `{"media_type":"movie","queue_id":7,"action":"change_category"}`, "change_category", true},
		{"remediate missing action", ActionRemediateQueue, `{"media_type":"movie","queue_id":7}`, "", false},
		{"grab release", ActionGrabRelease, `{"media_type":"movie","guid":"x","indexer_id":1}`, "", true},
		{"trigger search", ActionTriggerSearch, `{"media_type":"movie","tmdb_id":42}`, "", true},
		{"rescan", ActionRescan, `{"media_type":"movie","tmdb_id":42}`, "", true},
		{"unknown kind", ActionKind("weird"), `{}`, "", false},
	}
	for _, tc := range cases {
		facet, ok := actionAutoFacet(tc.kind, json.RawMessage(tc.params))
		if facet != tc.facet || ok != tc.ok {
			t.Errorf("%s: actionAutoFacet = (%q, %v), want (%q, %v)", tc.name, facet, ok, tc.facet, tc.ok)
		}
	}
}

// The seed approval is a human decision: remember arms the rule, but the action
// that seeded it must stay attributed to the admin, never to the new rule.
func TestApproveRememberCreatesRuleAndApprovesSeedAsHuman(t *testing.T) {
	svc, fx, _, _, actionID := autoApprovalFixture(t)

	act, err := svc.ApproveActionRemembering(testAdminID, actionID, nil)
	if err != nil {
		t.Fatalf("ApproveActionRemembering: %v", err)
	}
	if act.Status != ActionExecuted || fx.count() != 1 {
		t.Fatalf("seed approval status=%q execs=%d, want executed once", act.Status, fx.count())
	}
	if act.DecidedBy == nil || *act.DecidedBy != testAdminID || act.AutoRuleID != nil || act.AutoApproved {
		t.Fatalf("seed approval attribution = decidedBy %v autoRule %v, want the admin", act.DecidedBy, act.AutoRuleID)
	}
	rules, err := svc.ListApprovalRules()
	if err != nil || len(rules) != 1 {
		t.Fatalf("rules = %v (%v), want exactly one", rules, err)
	}
	rule := rules[0]
	if rule.ProblemKind != "Waiting to import" || rule.ActionKind != string(ActionManualImport) || rule.ActionFacet != "" {
		t.Fatalf("rule triple = %q/%q/%q", rule.ProblemKind, rule.ActionKind, rule.ActionFacet)
	}
	if rule.Status != ApprovalRuleActive || rule.CreatedBy == nil || *rule.CreatedBy != testAdminID ||
		rule.SeedActionID == nil || *rule.SeedActionID != actionID {
		t.Fatalf("rule provenance = %+v", rule)
	}
	if rule.Label != "Manual import · Waiting to import" {
		t.Fatalf("rule label = %q", rule.Label)
	}
	if rule.ApprovedCount != 0 || rule.ResolvedCount != 0 {
		t.Fatalf("fresh rule counters = %d/%d, want 0/0 (seed approval is human)", rule.ApprovedCount, rule.ResolvedCount)
	}
}

// Overriding to force and remembering must seed the force rule: the admin
// authorized the overridden shape, not the agent's original.
func TestApproveRememberWithOverrideSeedsFacetFromOverride(t *testing.T) {
	svc, _, _, _, actionID := autoApprovalFixture(t)
	override := json.RawMessage(`{"media_type":"movie","queue_id":7,"force":true}`)

	if _, err := svc.ApproveActionRemembering(testAdminID, actionID, &override); err != nil {
		t.Fatalf("ApproveActionRemembering: %v", err)
	}
	rules, _ := svc.ListApprovalRules()
	if len(rules) != 1 || rules[0].ActionFacet != "force" {
		t.Fatalf("rules = %+v, want one force-facet rule", rules)
	}
}

func TestApproveRememberReactivatesPausedRulePreservingCountsAndCreator(t *testing.T) {
	svc, _, _, _, actionID := autoApprovalFixture(t)
	const otherAdmin = int64(120)
	if _, err := svc.db.Exec("INSERT INTO users (id, username, password_hash, role) VALUES (?, 'admin2', '', 'admin')", otherAdmin); err != nil {
		t.Fatalf("seed second admin: %v", err)
	}
	if _, err := svc.db.Exec(
		`INSERT INTO agent_approval_rules (problem_kind, action_kind, action_facet, status, paused_reason, paused_at, created_by, approved_count, resolved_count)
		 VALUES ('Waiting to import', 'manual_import', '', 'paused', 'failed earlier', CURRENT_TIMESTAMP, ?, 3, 2)`,
		testAdminID,
	); err != nil {
		t.Fatalf("seed paused rule: %v", err)
	}

	if _, err := svc.ApproveActionRemembering(otherAdmin, actionID, nil); err != nil {
		t.Fatalf("ApproveActionRemembering: %v", err)
	}
	rules, _ := svc.ListApprovalRules()
	if len(rules) != 1 {
		t.Fatalf("rules = %+v, want the one reactivated rule", rules)
	}
	rule := rules[0]
	if rule.Status != ApprovalRuleActive || rule.PausedReason != nil || rule.PausedAt != nil {
		t.Fatalf("rule not cleanly re-armed: %+v", rule)
	}
	if rule.ApprovedCount != 3 || rule.ResolvedCount != 2 || rule.CreatedBy == nil || *rule.CreatedBy != testAdminID {
		t.Fatalf("reactivation clobbered provenance/counters: %+v", rule)
	}
}

// A user report never carries a problem label, so remember can arm nothing —
// but the approval itself must still go through untouched.
func TestApproveRememberDroppedForUserIssue(t *testing.T) {
	svc, fx, _, actionID := approvalFixture(t)

	act, err := svc.ApproveActionRemembering(testAdminID, actionID, nil)
	if err != nil {
		t.Fatalf("ApproveActionRemembering: %v", err)
	}
	if act.Status != ActionExecuted || fx.count() != 1 {
		t.Fatalf("approval status=%q execs=%d, want executed once", act.Status, fx.count())
	}
	var rules int
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM agent_approval_rules").Scan(&rules); err != nil || rules != 0 {
		t.Fatalf("rules = %d (%v), want none for a user report", rules, err)
	}
}

// The core promise: a proposal parked BEFORE the rule existed is approved by
// the next sweep (retroactivity), executed exactly once, and attributed to the
// rule with decided_by null.
func TestSweepAutoApprovesMatchingProposalRetroactively(t *testing.T) {
	svc, fx, notifier, _, actionID := autoApprovalFixture(t)
	ruleID, err := svc.createOrReactivateApprovalRule(testAdminID, 0, "Waiting to import", ActionManualImport, "")
	if err != nil {
		t.Fatalf("arm rule: %v", err)
	}

	svc.sweepAutoApprovals(time.Now().UTC())

	if fx.count() != 1 {
		t.Fatalf("executor ran %d times, want 1", fx.count())
	}
	act, err := svc.GetAction(actionID)
	if err != nil {
		t.Fatalf("GetAction: %v", err)
	}
	if act.Status != ActionExecuted {
		t.Fatalf("action status = %q, want executed", act.Status)
	}
	if !act.AutoApproved || act.AutoRuleID == nil || *act.AutoRuleID != ruleID || act.DecidedBy != nil {
		t.Fatalf("attribution = autoApproved %v rule %v decidedBy %v, want the rule with no admin", act.AutoApproved, act.AutoRuleID, act.DecidedBy)
	}
	if act.AutoRuleLabel == nil || *act.AutoRuleLabel != "Manual import · Waiting to import" {
		t.Fatalf("auto rule label = %v", act.AutoRuleLabel)
	}
	rule := ruleRow(t, svc, ruleID)
	if rule.ApprovedCount != 1 || rule.LastApprovedAt == nil {
		t.Fatalf("rule usage counters = %+v", rule)
	}
	if countAdminEvents(notifier, "agent_action_decided") == 0 {
		t.Fatalf("no agent_action_decided fired for live UI refresh: %v", notifier.adminEvents)
	}
	if countAdminEvents(notifier, "agent_autoapproval_paused") != 0 {
		t.Fatalf("successful auto-approval paused something: %v", notifier.adminEvents)
	}
}

// The facet is a safety boundary: a force rule must never approve a plain
// manual import (or vice versa), and a different problem label never matches.
func TestSweepFacetAndProblemKindMustMatchExactly(t *testing.T) {
	svc, fx, _, _, actionID := autoApprovalFixture(t)
	if _, err := svc.createOrReactivateApprovalRule(testAdminID, 0, "Waiting to import", ActionManualImport, "force"); err != nil {
		t.Fatalf("arm force rule: %v", err)
	}
	if _, err := svc.createOrReactivateApprovalRule(testAdminID, 0, "Matched by ID — needs manual import", ActionManualImport, ""); err != nil {
		t.Fatalf("arm other-problem rule: %v", err)
	}

	svc.sweepAutoApprovals(time.Now().UTC())

	if fx.count() != 0 {
		t.Fatalf("executor ran %d times, want 0 (neither rule matches the plain proposal)", fx.count())
	}
	act, _ := svc.GetAction(actionID)
	if act.Status != ActionProposed {
		t.Fatalf("action status = %q, want still proposed", act.Status)
	}
}

func TestSweepSkipsPausedRulesUserIssuesAndDisabledModes(t *testing.T) {
	t.Run("paused rule", func(t *testing.T) {
		svc, fx, _, _, _ := autoApprovalFixture(t)
		ruleID, _ := svc.createOrReactivateApprovalRule(testAdminID, 0, "Waiting to import", ActionManualImport, "")
		if _, err := svc.PauseApprovalRule(ruleID); err != nil {
			t.Fatalf("pause: %v", err)
		}
		svc.sweepAutoApprovals(time.Now().UTC())
		if fx.count() != 0 {
			t.Fatalf("paused rule approved something")
		}
	})
	t.Run("user issue never matches", func(t *testing.T) {
		svc, fx, _, actionID := approvalFixture(t)
		if _, err := svc.createOrReactivateApprovalRule(testAdminID, 0, "Waiting to import", ActionRemediateQueue, "blocklist_search"); err != nil {
			t.Fatalf("arm rule: %v", err)
		}
		svc.sweepAutoApprovals(time.Now().UTC())
		if fx.count() != 0 {
			t.Fatalf("user-report proposal was auto-approved")
		}
		act, _ := svc.GetAction(actionID)
		if act.Status != ActionProposed {
			t.Fatalf("user proposal status = %q, want proposed", act.Status)
		}
	})
	t.Run("master switch off", func(t *testing.T) {
		svc, fx, _, _, _ := autoApprovalFixture(t)
		svc.createOrReactivateApprovalRule(testAdminID, 0, "Waiting to import", ActionManualImport, "")
		if _, err := svc.SetSettings(Settings{Enabled: false, Mode: ModeSupervised, MaxSteps: 12, MaxTurnTokens: 1024, MaxWallClockSecs: 30, DailyRunCap: 50}); err != nil {
			t.Fatalf("set settings: %v", err)
		}
		svc.sweepAutoApprovals(time.Now().UTC())
		if fx.count() != 0 {
			t.Fatalf("sweep ran with remediation disabled")
		}
	})
	t.Run("investigate-only mode", func(t *testing.T) {
		svc, fx, _, _, _ := autoApprovalFixture(t)
		svc.createOrReactivateApprovalRule(testAdminID, 0, "Waiting to import", ActionManualImport, "")
		if _, err := svc.SetSettings(Settings{Enabled: true, Mode: ModeInvestigateOnly, MaxSteps: 12, MaxTurnTokens: 1024, MaxWallClockSecs: 30, DailyRunCap: 50}); err != nil {
			t.Fatalf("set settings: %v", err)
		}
		svc.sweepAutoApprovals(time.Now().UTC())
		if fx.count() != 0 {
			t.Fatalf("sweep ran in investigate-only mode")
		}
	})
}

// A gate that broke between park and sweep must be repaired (superseded), never
// executed and never treated as a rule failure.
func TestAutoApproveSkipsBrokenGateWithoutExecuting(t *testing.T) {
	svc, fx, notifier, issueID, actionID := autoApprovalFixture(t)
	ruleID, _ := svc.createOrReactivateApprovalRule(testAdminID, 0, "Waiting to import", ActionManualImport, "")
	if _, err := svc.db.Exec("UPDATE issues SET status = 'investigating' WHERE id = ?", issueID); err != nil {
		t.Fatalf("break gate: %v", err)
	}

	if _, err := svc.autoApproveAction(ruleID, actionID); err != nil {
		t.Fatalf("autoApproveAction: %v", err)
	}
	if fx.count() != 0 {
		t.Fatalf("executor ran on a broken gate")
	}
	act, _ := svc.GetAction(actionID)
	if act.Status != ActionSuperseded {
		t.Fatalf("action status = %q, want superseded", act.Status)
	}
	rule := ruleRow(t, svc, ruleID)
	if rule.Status != ApprovalRuleActive {
		t.Fatalf("supersession paused the rule: %+v", rule)
	}
	if countAdminEvents(notifier, "agent_autoapproval_paused") != 0 {
		t.Fatalf("supersession notified a pause")
	}
}

// One strike: an auto-approved fix that fails execution pauses its rule in the
// same commit, posts the thread evidence, and alerts admins exactly once.
func TestAutoApproveFailedOutcomePausesRuleAtomically(t *testing.T) {
	svc, fx, notifier, issueID, actionID := autoApprovalFixture(t)
	fx.err = notStartedTestError{}
	ruleID, _ := svc.createOrReactivateApprovalRule(testAdminID, 0, "Waiting to import", ActionManualImport, "")

	svc.sweepAutoApprovals(time.Now().UTC())

	act, _ := svc.GetAction(actionID)
	if act.Status != ActionFailed {
		t.Fatalf("action status = %q, want failed", act.Status)
	}
	rule := ruleRow(t, svc, ruleID)
	if rule.Status != ApprovalRulePaused || rule.PausedReason == nil || *rule.PausedReason != autoRulePausedExecutionFailed || rule.PausedAt == nil {
		t.Fatalf("rule after failed execution = %+v, want paused with the execution-failed reason", rule)
	}
	var msgs int
	if err := svc.db.QueryRow(
		"SELECT COUNT(*) FROM issue_messages WHERE issue_id = ? AND author_kind = ? AND body LIKE '%was paused%'",
		issueID, AuthorSystem,
	).Scan(&msgs); err != nil || msgs != 1 {
		t.Fatalf("system pause messages = %d (%v), want 1", msgs, err)
	}
	if n := countAdminEvents(notifier, "agent_autoapproval_paused"); n != 1 {
		t.Fatalf("agent_autoapproval_paused fired %d times, want 1: %v", n, notifier.adminEvents)
	}
	for i, event := range notifier.adminEvents {
		if event == "agent_autoapproval_paused" {
			data := notifier.adminData[i]
			if data["label"] != "Manual import · Waiting to import" || data["issue_id"] != issueID {
				t.Fatalf("pause payload = %v", data)
			}
		}
	}
	// A definitive failure still resumes the model so it can try another tack;
	// the durable handoff is the resume_pending run state.
	var runStatus string
	if err := svc.db.QueryRow("SELECT status FROM agent_runs WHERE issue_id = ?", issueID).Scan(&runStatus); err != nil || runStatus != runStatusResumePending {
		t.Fatalf("run status = %q (%v), want resume_pending", runStatus, err)
	}
}

// An unverifiable outcome is worse than a clean failure: the rule pauses and
// the issue parks at needs_admin without resuming the model.
func TestAutoApproveUnknownOutcomePausesRuleAndParksNeedsAdmin(t *testing.T) {
	svc, fx, notifier, issueID, actionID := autoApprovalFixture(t)
	fx.err = errors.New("connection reset mid-request")
	ruleID, _ := svc.createOrReactivateApprovalRule(testAdminID, 0, "Waiting to import", ActionManualImport, "")

	svc.sweepAutoApprovals(time.Now().UTC())

	act, _ := svc.GetAction(actionID)
	if act.Status != ActionOutcomeUnknown {
		t.Fatalf("action status = %q, want outcome_unknown", act.Status)
	}
	rule := ruleRow(t, svc, ruleID)
	if rule.Status != ApprovalRulePaused || rule.PausedReason == nil || *rule.PausedReason != autoRulePausedUnverifiedOutcome {
		t.Fatalf("rule after unknown outcome = %+v", rule)
	}
	issue, err := svc.GetIssue(issueID)
	if err != nil || issue.Status != IssueNeedsAdmin {
		t.Fatalf("issue status = %v (%v), want needs_admin", issue, err)
	}
	if n := countAdminEvents(notifier, "agent_autoapproval_paused"); n != 1 {
		t.Fatalf("pause notified %d times, want 1", n)
	}
}

// The rule judges only its own decisions: a HUMAN approval that fails must
// never pause a matching rule.
func TestHumanApprovalFailureNeverPausesRule(t *testing.T) {
	svc, fx, notifier, _, actionID := autoApprovalFixture(t)
	fx.err = notStartedTestError{}
	ruleID, _ := svc.createOrReactivateApprovalRule(testAdminID, 0, "Waiting to import", ActionManualImport, "")

	act, err := svc.ApproveAction(testAdminID, actionID, nil)
	if err != nil {
		t.Fatalf("ApproveAction: %v", err)
	}
	if act.Status != ActionFailed {
		t.Fatalf("action status = %q, want failed", act.Status)
	}
	rule := ruleRow(t, svc, ruleID)
	if rule.Status != ApprovalRuleActive {
		t.Fatalf("human failure paused the rule: %+v", rule)
	}
	if countAdminEvents(notifier, "agent_autoapproval_paused") != 0 {
		t.Fatalf("human failure notified a pause")
	}
}

// Repair/retry paths re-record an outcome that already went through the pause
// chokepoint once; they must never re-pause a rule an admin has since resumed.
func TestDurableRetryDoesNotRePauseResumedRule(t *testing.T) {
	svc, fx, _, _, actionID := autoApprovalFixture(t)
	fx.err = errors.New("connection reset mid-request")
	ruleID, _ := svc.createOrReactivateApprovalRule(testAdminID, 0, "Waiting to import", ActionManualImport, "")
	svc.sweepAutoApprovals(time.Now().UTC())
	if rule := ruleRow(t, svc, ruleID); rule.Status != ApprovalRulePaused {
		t.Fatalf("setup: rule not paused: %+v", rule)
	}
	if _, err := svc.ResumeApprovalRule(ruleID); err != nil {
		t.Fatalf("resume: %v", err)
	}

	// A lost-response retry lands on the durable outcome_unknown branch.
	act, err := svc.ApproveAction(testAdminID, actionID, nil)
	if err != nil {
		t.Fatalf("retry ApproveAction: %v", err)
	}
	if act.Status != ActionOutcomeUnknown {
		t.Fatalf("retry status = %q, want the durable outcome_unknown", act.Status)
	}
	rule := ruleRow(t, svc, ruleID)
	if rule.Status != ApprovalRuleActive {
		t.Fatalf("durable retry re-paused the resumed rule: %+v", rule)
	}
}

// "As long as the issue closes out successfully": a pipeline-verified resolved
// close counts the success; a wont_fix verdict pauses; give-up pauses; the
// admin's own dispositions are neutral.
func TestIssueTerminalAccounting(t *testing.T) {
	executedFixture := func(t *testing.T) (*Service, *fakeNotifier, int64, int64) {
		svc, fx, notifier, issueID, _ := autoApprovalFixture(t)
		ruleID, _ := svc.createOrReactivateApprovalRule(testAdminID, 0, "Waiting to import", ActionManualImport, "")
		svc.sweepAutoApprovals(time.Now().UTC())
		if fx.count() != 1 {
			t.Fatalf("setup: sweep did not execute")
		}
		return svc, notifier, issueID, ruleID
	}

	t.Run("resolved increments", func(t *testing.T) {
		svc, notifier, issueID, ruleID := executedFixture(t)
		if err := svc.ConcludeIssue(context.Background(), issueID, IssueResolved, "The import completed and the file is present."); err != nil {
			t.Fatalf("ConcludeIssue: %v", err)
		}
		rule := ruleRow(t, svc, ruleID)
		if rule.Status != ApprovalRuleActive || rule.ResolvedCount != 1 || rule.LastResolvedAt == nil {
			t.Fatalf("rule after resolved close = %+v, want active with resolved_count 1", rule)
		}
		if countAdminEvents(notifier, "agent_autoapproval_paused") != 0 {
			t.Fatalf("resolved close notified a pause")
		}
	})
	t.Run("wont_fix pauses", func(t *testing.T) {
		svc, notifier, issueID, ruleID := executedFixture(t)
		if err := svc.ConcludeIssue(context.Background(), issueID, IssueWontFix, "Could not repair this download."); err != nil {
			t.Fatalf("ConcludeIssue: %v", err)
		}
		rule := ruleRow(t, svc, ruleID)
		if rule.Status != ApprovalRulePaused || rule.PausedReason == nil || *rule.PausedReason != autoRulePausedIssueUnresolved {
			t.Fatalf("rule after wont_fix = %+v, want paused", rule)
		}
		if rule.ResolvedCount != 0 {
			t.Fatalf("wont_fix incremented resolved_count: %+v", rule)
		}
		if countAdminEvents(notifier, "agent_autoapproval_paused") != 1 {
			t.Fatalf("wont_fix pause notified %d times, want 1", countAdminEvents(notifier, "agent_autoapproval_paused"))
		}
	})
	t.Run("give-up pauses", func(t *testing.T) {
		svc, notifier, issueID, ruleID := executedFixture(t)
		var runID int64
		if err := svc.db.QueryRow("SELECT id FROM agent_runs WHERE issue_id = ?", issueID).Scan(&runID); err != nil {
			t.Fatalf("load run: %v", err)
		}
		// Re-stage the exact claimed shape GiveUpIssue finalizes: a running run
		// holding the issue's active claim.
		if _, err := svc.db.Exec("UPDATE agent_runs SET status = 'running' WHERE id = ?", runID); err != nil {
			t.Fatalf("stage run: %v", err)
		}
		if _, err := svc.db.Exec("UPDATE issues SET status = 'investigating', active_run_id = ? WHERE id = ?", runID, issueID); err != nil {
			t.Fatalf("stage issue: %v", err)
		}
		gaveUp, err := svc.GiveUpIssue(context.Background(), issueID, runID, "budget_exhausted", "", "The agent ran out of budget.")
		if err != nil || !gaveUp {
			t.Fatalf("GiveUpIssue = %v, %v", gaveUp, err)
		}
		rule := ruleRow(t, svc, ruleID)
		if rule.Status != ApprovalRulePaused || rule.PausedReason == nil || *rule.PausedReason != autoRulePausedIssueUnresolved {
			t.Fatalf("rule after give-up = %+v, want paused", rule)
		}
		if countAdminEvents(notifier, "agent_autoapproval_paused") != 1 {
			t.Fatalf("give-up pause notified %d times, want 1", countAdminEvents(notifier, "agent_autoapproval_paused"))
		}
	})
	t.Run("admin dismiss is neutral", func(t *testing.T) {
		svc, notifier, issueID, ruleID := executedFixture(t)
		if err := svc.DismissIssue(issueID); err != nil {
			t.Fatalf("DismissIssue: %v", err)
		}
		rule := ruleRow(t, svc, ruleID)
		if rule.Status != ApprovalRuleActive || rule.ResolvedCount != 0 {
			t.Fatalf("dismiss changed the rule: %+v", rule)
		}
		if countAdminEvents(notifier, "agent_autoapproval_paused") != 0 {
			t.Fatalf("dismiss notified a pause")
		}
	})
	t.Run("admin completion is neutral", func(t *testing.T) {
		svc, notifier, issueID, ruleID := executedFixture(t)
		if _, err := svc.ResolveIssueByAdmin(context.Background(), testAdminID, issueID, AdminDispositionResolved, "Verified in Radarr by hand."); err != nil {
			t.Fatalf("ResolveIssueByAdmin: %v", err)
		}
		rule := ruleRow(t, svc, ruleID)
		if rule.Status != ApprovalRuleActive || rule.ResolvedCount != 0 {
			t.Fatalf("admin completion changed the rule: %+v", rule)
		}
		if countAdminEvents(notifier, "agent_autoapproval_paused") != 0 {
			t.Fatalf("admin completion notified a pause")
		}
	})
}

// The DTO offer appears exactly when remember could arm something, and flips to
// reactivation wording over a paused rule.
func TestActionDTOAutoApprovalOffer(t *testing.T) {
	svc, _, _, _, actionID := autoApprovalFixture(t)

	act, err := svc.GetAction(actionID)
	if err != nil {
		t.Fatalf("GetAction: %v", err)
	}
	offer := act.AutoApprovalOffer
	if offer == nil || offer.Label != "Manual import · Waiting to import" || offer.ReactivatesPausedRule {
		t.Fatalf("fresh proposal offer = %+v, want a plain offer", offer)
	}
	if offer.ProblemKind != "Waiting to import" || offer.ActionKind != string(ActionManualImport) || offer.ActionFacet != "" {
		t.Fatalf("offer triple = %+v", offer)
	}

	ruleID, _ := svc.createOrReactivateApprovalRule(testAdminID, 0, "Waiting to import", ActionManualImport, "")
	act, _ = svc.GetAction(actionID)
	if act.AutoApprovalOffer != nil {
		t.Fatalf("offer still present with an active rule: %+v", act.AutoApprovalOffer)
	}

	if _, err := svc.PauseApprovalRule(ruleID); err != nil {
		t.Fatalf("pause: %v", err)
	}
	act, _ = svc.GetAction(actionID)
	if act.AutoApprovalOffer == nil || !act.AutoApprovalOffer.ReactivatesPausedRule {
		t.Fatalf("paused-rule offer = %+v, want a reactivation offer", act.AutoApprovalOffer)
	}
}

func TestActionDTOOfferAbsentForUserIssue(t *testing.T) {
	svc, _, _, actionID := approvalFixture(t)
	act, err := svc.GetAction(actionID)
	if err != nil {
		t.Fatalf("GetAction: %v", err)
	}
	if act.AutoApprovalOffer != nil {
		t.Fatalf("user-report proposal carried an offer: %+v", act.AutoApprovalOffer)
	}
}

// Deleting a rule keeps the audit attribution readable: auto_approved stays
// true, only the label disappears.
func TestRuleDeletionKeepsActionAttribution(t *testing.T) {
	svc, _, _, _, actionID := autoApprovalFixture(t)
	ruleID, _ := svc.createOrReactivateApprovalRule(testAdminID, 0, "Waiting to import", ActionManualImport, "")
	svc.sweepAutoApprovals(time.Now().UTC())

	if err := svc.DeleteApprovalRule(ruleID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	act, err := svc.GetAction(actionID)
	if err != nil {
		t.Fatalf("GetAction after delete: %v", err)
	}
	if !act.AutoApproved || act.AutoRuleID == nil || *act.AutoRuleID != ruleID {
		t.Fatalf("attribution lost after delete: %+v", act)
	}
	if act.AutoRuleLabel != nil {
		t.Fatalf("deleted rule still labeled: %v", *act.AutoRuleLabel)
	}
	if err := svc.DeleteApprovalRule(ruleID); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("second delete = %v, want not found", err)
	}
}

func TestRulesCRUDLifecycle(t *testing.T) {
	svc, _, _, _, _ := autoApprovalFixture(t)
	ruleID, err := svc.createOrReactivateApprovalRule(testAdminID, 0, "Waiting to import", ActionManualImport, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rules, err := svc.ListApprovalRules()
	if err != nil || len(rules) != 1 {
		t.Fatalf("list = %v (%v)", rules, err)
	}
	if rules[0].CreatedByName == nil || *rules[0].CreatedByName != "admin" {
		t.Fatalf("creator name = %v", rules[0].CreatedByName)
	}

	paused, err := svc.PauseApprovalRule(ruleID)
	if err != nil || paused.Status != ApprovalRulePaused || paused.PausedReason == nil {
		t.Fatalf("pause = %+v (%v)", paused, err)
	}
	// Idempotent: pausing again keeps the original reason.
	again, err := svc.PauseApprovalRule(ruleID)
	if err != nil || again.Status != ApprovalRulePaused || *again.PausedReason != *paused.PausedReason {
		t.Fatalf("double pause = %+v (%v)", again, err)
	}
	resumed, err := svc.ResumeApprovalRule(ruleID)
	if err != nil || resumed.Status != ApprovalRuleActive || resumed.PausedReason != nil || resumed.PausedAt != nil {
		t.Fatalf("resume = %+v (%v)", resumed, err)
	}
	if _, err := svc.GetApprovalRule(999); err == nil {
		t.Fatalf("get missing rule succeeded")
	}
}

// A rule approval inside the hold-down drops the owed push: automated fixes
// never page anyone, while an unmatched sibling proposal still does.
func TestSweepSuppressesOwedActionAlert(t *testing.T) {
	base := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)

	t.Run("matched proposal never pages", func(t *testing.T) {
		svc, _, notifier, issueID, _ := autoApprovalFixture(t)
		svc.createOrReactivateApprovalRule(testAdminID, 0, "Waiting to import", ActionManualImport, "")
		svc.queueActionAlert(issueID, base)
		svc.sweepAutoApprovals(base.Add(30 * time.Second))
		svc.flushActionAlerts(base.Add(4 * time.Minute))
		if n := countAdminEvents(notifier, "agent_action_pending"); n != 0 {
			t.Fatalf("auto-approved proposal paged the admin %d times", n)
		}
	})
	t.Run("unmatched proposal still pages", func(t *testing.T) {
		svc, _, notifier, issueID, _ := autoApprovalFixture(t)
		svc.queueActionAlert(issueID, base)
		svc.sweepAutoApprovals(base.Add(30 * time.Second))
		svc.flushActionAlerts(base.Add(4 * time.Minute))
		if n := countAdminEvents(notifier, "agent_action_pending"); n != 1 {
			t.Fatalf("unmatched proposal paged %d times, want 1", n)
		}
	})
}

// problem_kind is written at auto-issue creation and refreshed on matching
// snapshots — it is the rule key's issue half.
func TestProblemKindPersistedForAutoIssues(t *testing.T) {
	svc, _, _ := setupObservationService(t, false)
	enableAutoDispatch(t, svc, 5)
	base := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	problem := observedProblem("download-a", 7, 100)
	if problem.Diagnosis.Problem == "" {
		t.Fatalf("fixture diagnosis has no problem label")
	}

	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{problem}, base); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := svc.db.QueryRow("SELECT COALESCE(problem_kind, '') FROM issues").Scan(&stored); err != nil {
		t.Fatalf("read problem_kind: %v", err)
	}
	if stored != problem.Diagnosis.Problem {
		t.Fatalf("problem_kind = %q, want %q", stored, problem.Diagnosis.Problem)
	}

	// A matching refresh keeps the label current with the latest snapshot.
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{problem}, base.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.QueryRow("SELECT COALESCE(problem_kind, '') FROM issues").Scan(&stored); err != nil || stored != problem.Diagnosis.Problem {
		t.Fatalf("problem_kind after refresh = %q (%v)", stored, err)
	}
}
