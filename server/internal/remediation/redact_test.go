package remediation

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/ai"
)

func TestPersistedAuditAndTranscriptRedactCredentialBearingText(t *testing.T) {
	runner, _, issueID := newTestRunner(t, &fakeToolHost{}, &scriptedTurn{})
	res, err := runner.db.Exec(
		`INSERT INTO agent_runs (issue_id, trigger, status, model, transcript_json)
		 VALUES (?, 'user_report', ?, 'test-model', '[]')`,
		issueID, runStatusRunning,
	)
	if err != nil {
		t.Fatalf("insert run: %v", err)
	}
	runID, _ := res.LastInsertId()

	inputSecret := "audit-input-secret"
	outputSecret := "audit-output-secret"
	transcriptSecret := "transcript-secret"
	runner.persistStep(runID, issueID, 1, stepToolResult, "get_history", "tool-1",
		`{"nested":{"downloadUrl":"https://idx.invalid/get?apiKey=`+inputSecret+`&id=7"}}`,
		"upstream body: Authorization: Bearer "+outputSecret, true,
	)
	runner.persistTranscript(runID, ai.Transcript{
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{
			{Type: ai.BlockText, Text: "diagnosis at https://user:" + transcriptSecret + "@arr.invalid/path"},
			{Type: ai.BlockToolUse, ID: "tool-2", Name: "get_history", Input: json.RawMessage(`{"headers":{"X-Api-Key":"` + transcriptSecret + `"},"safe":4}`)},
		}},
		{Role: ai.RoleUser, Content: []ai.TranscriptBlock{{
			Type: ai.BlockToolResult, ToolUseID: "tool-2", Content: "error token=" + transcriptSecret + " detail=kept",
		}}},
	})

	var toolInput, toolOutput, transcript string
	if err := runner.db.QueryRow(
		"SELECT COALESCE(tool_input,''), COALESCE(tool_output,'') FROM agent_steps WHERE run_id = ? AND seq = 1",
		runID,
	).Scan(&toolInput, &toolOutput); err != nil {
		t.Fatalf("load audit: %v", err)
	}
	if err := runner.db.QueryRow("SELECT transcript_json FROM agent_runs WHERE id = ?", runID).Scan(&transcript); err != nil {
		t.Fatalf("load transcript: %v", err)
	}
	combined := toolInput + toolOutput + transcript
	for _, secret := range []string{inputSecret, outputSecret, transcriptSecret} {
		if strings.Contains(combined, secret) {
			t.Fatalf("persisted remediation data leaked %q: %s", secret, combined)
		}
	}
	for _, want := range []string{"id=7", "detail=kept", `"safe":4`, "arr.invalid/path", "[REDACTED]"} {
		if !strings.Contains(combined, want) {
			t.Errorf("persisted remediation data lost useful value %q: %s", want, combined)
		}
	}
}

func TestThreadSyncRedactsModelCopyButPreservesReporterMessage(t *testing.T) {
	runner, _, issueID := newTestRunner(t, &fakeToolHost{}, &scriptedTurn{})
	res, err := runner.db.Exec(
		`INSERT INTO agent_runs (issue_id, trigger, status, model, transcript_json)
		 VALUES (?, 'user_report', ?, 'test-model', '[]')`,
		issueID, runStatusRunning,
	)
	if err != nil {
		t.Fatalf("insert run: %v", err)
	}
	runID, _ := res.LastInsertId()
	secret := "reporter-query-secret"
	body := "Sonarr said https://idx.invalid/get?apikey=" + secret + "&release=useful"
	if _, err := runner.db.Exec(
		"INSERT INTO issue_messages (issue_id, author_kind, body) VALUES (?, ?, ?)",
		issueID, AuthorUser, body,
	); err != nil {
		t.Fatalf("insert thread message: %v", err)
	}
	state := &loopState{runID: runID}
	changed, err := runner.syncThreadUpdates(state, issueID)
	if err != nil || !changed {
		t.Fatalf("syncThreadUpdates changed=%v err=%v", changed, err)
	}

	var transcript, storedBody string
	if err := runner.db.QueryRow("SELECT transcript_json FROM agent_runs WHERE id = ?", runID).Scan(&transcript); err != nil {
		t.Fatal(err)
	}
	if err := runner.db.QueryRow("SELECT body FROM issue_messages WHERE issue_id = ? ORDER BY id DESC LIMIT 1", issueID).Scan(&storedBody); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(transcript, secret) {
		t.Fatalf("model-facing thread transcript leaked secret: %s", transcript)
	}
	if !strings.Contains(transcript, "release=useful") || !strings.Contains(transcript, "REDACTED") {
		t.Fatalf("model-facing thread transcript lost useful diagnosis: %s", transcript)
	}
	if storedBody != body {
		t.Fatalf("reporter-visible source message changed: %q", storedBody)
	}
}

func TestApprovalResumeRedactsTranscriptAndAuditOutcome(t *testing.T) {
	runner, _, issueID := newTestRunner(t, &fakeToolHost{}, &scriptedTurn{})
	const toolUseID = "proposal-gate"
	history := ai.Transcript{
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{{
			Type: ai.BlockToolUse, ID: toolUseID, Name: "propose_action", Input: json.RawMessage(`{"kind":"rescan"}`),
		}}},
		{Role: ai.RoleUser, Content: []ai.TranscriptBlock{{
			Type: ai.BlockToolResult, ToolUseID: toolUseID, Name: "propose_action", Content: "awaiting approval",
		}}},
	}
	encoded, _ := json.Marshal(history)
	res, err := runner.db.Exec(
		`INSERT INTO agent_runs (issue_id, trigger, status, model, transcript_json)
		 VALUES (?, 'user_report', ?, 'test-model', ?)`,
		issueID, runStatusWaitingApproval, string(encoded),
	)
	if err != nil {
		t.Fatalf("insert run: %v", err)
	}
	runID, _ := res.LastInsertId()
	if _, err := runner.db.Exec("UPDATE issues SET status = ?, active_run_id = NULL WHERE id = ?", IssueAwaitingApproval, issueID); err != nil {
		t.Fatal(err)
	}

	secret := "approval-error-secret"
	tx, err := runner.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ready, err := stageResumeResultTx(tx, issueID, runID,
		IssueAwaitingApproval, runStatusWaitingApproval,
		"propose_action", toolUseID,
		`Admin approved, upstream body={"downloadUrl":"https://idx.invalid/get?token=`+secret+`&id=9"}`,
		false,
	)
	if err != nil || !ready {
		tx.Rollback()
		t.Fatalf("stageResumeResultTx ready=%v err=%v", ready, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var transcript, audit string
	if err := runner.db.QueryRow("SELECT transcript_json FROM agent_runs WHERE id = ?", runID).Scan(&transcript); err != nil {
		t.Fatal(err)
	}
	if err := runner.db.QueryRow("SELECT COALESCE(tool_output,'') FROM agent_steps WHERE run_id = ? ORDER BY seq DESC LIMIT 1", runID).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	combined := transcript + audit
	if strings.Contains(combined, secret) {
		t.Fatalf("approval resume persistence leaked secret: %s", combined)
	}
	if !strings.Contains(combined, "id=9") || !strings.Contains(combined, "REDACTED") {
		t.Fatalf("approval resume lost useful outcome: %s", combined)
	}
}

func TestReleaseReferenceOnlyTrustsCanonicalFingerprint(t *testing.T) {
	canonical := releaseGUIDFingerprint("opaque-capability")
	if !isReleaseGUIDFingerprint(canonical) || normalizeReleaseGUIDReference(canonical) != canonical {
		t.Fatalf("canonical fingerprint was not idempotent: %q", canonical)
	}

	for _, unsafe := range []string{
		"[REDACTED release sha256:opaque-secret]",
		"https://idx.invalid/[REDACTED]?unrecognized=opaque-secret",
		"[REDACTED release sha256:0123456789abcdef]suffix",
	} {
		normalized := normalizeReleaseGUIDReference(unsafe)
		if normalized == unsafe || strings.Contains(normalized, "opaque-secret") || !isReleaseGUIDFingerprint(normalized) {
			t.Fatalf("unsafe release reference survived normalization: input=%q output=%q", unsafe, normalized)
		}
		wire := string(actionParamsForWire(string(ActionGrabRelease), json.RawMessage(`{"guid":`+strconv.Quote(unsafe)+`}`)))
		if strings.Contains(wire, "opaque-secret") || strings.Contains(wire, "unrecognized") {
			t.Fatalf("unsafe release reference reached wire JSON: %s", wire)
		}
	}
}

func TestAgentActionWirePayloadsFingerprintReleaseGUIDs(t *testing.T) {
	proposedSecret := "proposed-guid-api-secret"
	approvedSecret := "approved-guid-token-secret"
	proposed := json.RawMessage(`{"media_type":"movie","guid":"https://user:pass@idx.invalid/get?apiKey=` + proposedSecret + `&id=4","indexer_id":7}`)
	approved := json.RawMessage(`{"media_type":"movie","guid":"https://idx.invalid/get?token=` + approvedSecret + `&id=8","indexer_id":9}`)
	legacyTextSecret := "legacy-action-text-secret"
	deny := "Authorization: Bearer " + legacyTextSecret
	result := "failed at https://idx.invalid/get?password=" + legacyTextSecret + "&status=bad"
	action := AgentAction{
		ID:             1,
		IssueID:        2,
		Kind:           string(ActionGrabRelease),
		Params:         proposed,
		ApprovedParams: &approved,
		Rationale:      "try https://idx.invalid/get?apikey=" + legacyTextSecret + "&quality=good",
		DenyReason:     &deny,
		ResultText:     &result,
		IssueTitle:     "title token=" + legacyTextSecret + " useful",
	}

	payloads := []any{
		action,
		ListActionsResponse{Actions: []AgentAction{action}},
		IssueActivity{Actions: []AgentAction{action}},
	}
	for i, payload := range payloads {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload %d: %v", i, err)
		}
		text := string(encoded)
		for _, secret := range []string{proposedSecret, approvedSecret, legacyTextSecret, "user:pass"} {
			if strings.Contains(text, secret) {
				t.Fatalf("wire payload %d leaked %q: %s", i, secret, text)
			}
		}
		if got := strings.Count(text, "REDACTED release sha256:"); got != 2 {
			t.Fatalf("wire payload %d release fingerprints = %d, want proposed + approved: %s", i, got, text)
		}
		for _, want := range []string{`"indexer_id":7`, `"indexer_id":9`, "quality=good", "status=bad"} {
			if !strings.Contains(text, want) {
				t.Errorf("wire payload %d lost useful field %q: %s", i, want, text)
			}
		}
	}

	// JSON serialization is a view only. Approval/execution still sees the exact
	// server-side values loaded from SQLite.
	if !strings.Contains(string(action.Params), proposedSecret) || !strings.Contains(string(*action.ApprovedParams), approvedSecret) {
		t.Fatal("wire redaction mutated the stored/executable action params")
	}
}
