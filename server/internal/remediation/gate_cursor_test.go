package remediation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/db"
)

func seedRunningGate(t *testing.T, toolName, toolUseID string, withProposal bool) (*Runner, *Service, int64, int64) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	reporterID := seedUser(t, database, "reporter")
	svc := NewService(database, nil, nil, &fakeNotifier{})
	issueRes, err := database.Exec(
		`INSERT INTO issues (source, status, reporter_id, media_type, tmdb_id, title, detail)
		 VALUES (?, ?, ?, 'movie', 42, 'Movie', 'original')`,
		SourceUser, IssueInvestigating, reporterID,
	)
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issueID, _ := issueRes.LastInsertId()
	history := ai.Transcript{
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{{Type: ai.BlockToolUse, ID: toolUseID, Name: toolName}}},
		{Role: ai.RoleUser, Content: []ai.TranscriptBlock{{Type: ai.BlockToolResult, ToolUseID: toolUseID, Name: toolName, Content: "gate recorded"}}},
	}
	encoded, _ := json.Marshal(history)
	runRes, err := database.Exec(
		`INSERT INTO agent_runs (issue_id, trigger, status, model, transcript_json)
		 VALUES (?, 'user_report', ?, 'test', ?)`,
		issueID, runStatusRunning, string(encoded),
	)
	if err != nil {
		t.Fatalf("seed run: %v", err)
	}
	runID, _ := runRes.LastInsertId()
	if _, err := database.Exec("UPDATE issues SET active_run_id = ? WHERE id = ?", runID, issueID); err != nil {
		t.Fatalf("claim issue: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO agent_steps (run_id, issue_id, seq, kind, tool_name, tool_use_id, tool_output)
		 VALUES (?, ?, 1, ?, ?, ?, 'gate recorded')`,
		runID, issueID, stepToolResult, toolName, toolUseID,
	); err != nil {
		t.Fatalf("seed gate step: %v", err)
	}
	if withProposal {
		if _, err := database.Exec(
			`INSERT INTO agent_actions
			 (issue_id, run_id, tool_use_id, kind, params, rationale, status, fingerprint)
			 VALUES (?, ?, ?, ?, '{"media_type":"movie","queue_id":7,"action":"remove"}', 'test', ?, ?)`,
			issueID, runID, toolUseID, string(ActionRemediateQueue), ActionProposed, "cursor-test-"+toolUseID,
		); err != nil {
			t.Fatalf("seed proposal: %v", err)
		}
	}
	return &Runner{db: database, svc: svc}, svc, issueID, runID
}

func TestProposalParkCursorLossSupersedesGateAndContinues(t *testing.T) {
	runner, svc, issueID, runID := seedRunningGate(t, "propose_action", "proposal-1", true)
	if err := svc.PostReply(issueID, AuthorUser, 1, "The symptoms changed after your last read."); err != nil {
		t.Fatalf("PostReply: %v", err)
	}
	committed, err := runner.park(issueID, runID, 0, "proposal-1")
	if err != nil {
		t.Fatalf("park: %v", err)
	}
	if committed {
		t.Fatal("stale approval gate committed despite a newer thread message")
	}
	var issueStatus, runStatus, actionStatus, transcript string
	var activeRun int64
	if err := svc.db.QueryRow(
		`SELECT i.status, i.active_run_id, r.status, a.status, r.transcript_json
		 FROM issues i JOIN agent_runs r ON r.id = i.active_run_id
		 JOIN agent_actions a ON a.run_id = r.id WHERE i.id = ?`, issueID,
	).Scan(&issueStatus, &activeRun, &runStatus, &actionStatus, &transcript); err != nil {
		t.Fatalf("load invalidated proposal: %v", err)
	}
	if issueStatus != IssueInvestigating || activeRun != runID || runStatus != runStatusRunning || actionStatus != ActionSuperseded {
		t.Fatalf("invalidated state = issue %s active %d run %s action %s", issueStatus, activeRun, runStatus, actionStatus)
	}
	if !strings.Contains(transcript, "Gate cancelled because new issue-thread information arrived") {
		t.Fatalf("transcript kept stale gate result: %s", transcript)
	}
	history, err := rehydrateTranscript(transcript)
	if err != nil {
		t.Fatalf("decode transcript: %v", err)
	}
	state := &loopState{runID: runID, history: history}
	if changed, err := runner.syncThreadUpdates(state, issueID); err != nil || !changed {
		t.Fatalf("sync winner reply changed=%v err=%v", changed, err)
	}
	encoded, _ := json.Marshal(state.history)
	if !strings.Contains(string(encoded), "The symptoms changed") {
		t.Fatalf("next turn did not consume winning reply: %s", encoded)
	}
}

func TestAskReporterParkCursorLossDoesNotPostQuestion(t *testing.T) {
	runner, svc, issueID, runID := seedRunningGate(t, "ask_reporter", "ask-1", false)
	if err := svc.PostReply(issueID, AuthorUser, 1, "I already found the relevant detail."); err != nil {
		t.Fatalf("PostReply: %v", err)
	}
	committed, err := runner.parkAwaitingUser(issueID, runID, "Which language?", 0, "ask-1")
	if err != nil {
		t.Fatalf("parkAwaitingUser: %v", err)
	}
	if committed {
		t.Fatal("stale awaiting-user gate committed despite a newer reply")
	}
	var issueStatus, runStatus string
	if err := svc.db.QueryRow(
		`SELECT i.status, r.status FROM issues i JOIN agent_runs r ON r.id = i.active_run_id
		 WHERE i.id = ?`, issueID,
	).Scan(&issueStatus, &runStatus); err != nil {
		t.Fatalf("load invalidated ask: %v", err)
	}
	if issueStatus != IssueInvestigating || runStatus != runStatusRunning {
		t.Fatalf("invalidated ask state = issue %s run %s", issueStatus, runStatus)
	}
	var questions int
	if err := svc.db.QueryRow(
		"SELECT COUNT(*) FROM issue_messages WHERE issue_id = ? AND author_kind = ?",
		issueID, AuthorAgent,
	).Scan(&questions); err != nil {
		t.Fatalf("count questions: %v", err)
	}
	if questions != 0 {
		t.Fatalf("posted %d stale reporter question(s)", questions)
	}
}

func TestReplyAfterApprovalParkInvalidatesAndStagesResume(t *testing.T) {
	svc, _, issueID, actionID := approvalFixture(t)
	if err := svc.PostReply(issueID, AuthorAdmin, testAdminID, "New evidence: do not use that release."); err != nil {
		t.Fatalf("PostReply: %v", err)
	}
	action, err := svc.GetAction(actionID)
	if err != nil {
		t.Fatalf("GetAction: %v", err)
	}
	if action.Status != ActionSuperseded {
		t.Fatalf("action status = %q, want superseded", action.Status)
	}
	var issueStatus, runStatus string
	if err := svc.db.QueryRow(
		`SELECT i.status, r.status FROM issues i JOIN agent_runs r ON r.issue_id = i.id
		 WHERE i.id = ?`, issueID,
	).Scan(&issueStatus, &runStatus); err != nil {
		t.Fatalf("load staged resume: %v", err)
	}
	if issueStatus != IssueInvestigating || runStatus != runStatusResumePending {
		t.Fatalf("reply handoff = issue %s run %s", issueStatus, runStatus)
	}
	select {
	case job := <-svc.jobs:
		if !job.resume || job.issueID != issueID {
			t.Fatalf("resume job = %+v", job)
		}
	default:
		t.Fatal("reply invalidation did not enqueue resume")
	}
}
