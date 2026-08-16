package remediation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/auth"
)

// seedExtraProposal seeds another issue with its own parked run and proposed
// action, mirroring approvalFixture's rows, so batch tests can operate on
// several independent proposals against the shared fixture service.
func seedExtraProposal(t *testing.T, svc *Service, n int) (issueID, actionID int64) {
	t.Helper()
	res, err := svc.db.Exec(
		"INSERT INTO issues (source, status, media_type, tmdb_id, title, detail, instance_id) VALUES ('user','awaiting_approval','movie',?,?,'wrong content','radarr-main')",
		42+n, fmt.Sprintf("Test Movie %d", n),
	)
	if err != nil {
		t.Fatalf("seed extra issue: %v", err)
	}
	issueID, _ = res.LastInsertId()

	toolUseID := fmt.Sprintf("toolu_propose_batch_%d", n)
	history := []map[string]any{
		{"role": "user", "content": []map[string]any{{"type": "text", "text": "investigate"}}},
		{"role": "assistant", "content": []map[string]any{{"type": "tool_use", "id": toolUseID, "name": "propose_action", "input": map[string]any{}}}},
		{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": toolUseID, "name": "propose_action", "content": "Proposal recorded; awaiting admin approval."}}},
	}
	htData, _ := json.Marshal(history)
	runRes, err := svc.db.Exec(
		"INSERT INTO agent_runs (issue_id, trigger, status, model, step_count, transcript_json) VALUES (?, 'user_report', ?, 'claude-haiku-4-5', 3, ?)",
		issueID, runStatusWaitingApproval, string(htData),
	)
	if err != nil {
		t.Fatalf("seed extra run: %v", err)
	}
	runID, _ := runRes.LastInsertId()

	params := fmt.Sprintf(`{"media_type":"movie","queue_id":%d,"action":"blocklist_search"}`, 100+n)
	fp := fingerprint(issueID, runID, toolUseID, ActionRemediateQueue, json.RawMessage(params))
	actRes, err := svc.db.Exec(
		"INSERT INTO agent_actions (issue_id, run_id, tool_use_id, kind, params, rationale, risk, status, fingerprint) VALUES (?, ?, ?, ?, ?, 'because', 'mutating', ?, ?)",
		issueID, runID, toolUseID, string(ActionRemediateQueue), params, ActionProposed, fp,
	)
	if err != nil {
		t.Fatalf("seed extra action: %v", err)
	}
	actionID, _ = actRes.LastInsertId()
	return issueID, actionID
}

// TestBatchApproveExecutesEachExactlyOnce: a batch over N proposals dispatches
// each underlying mutation exactly once, attributes every decision to the
// admin, and reports each item executed in request order.
func TestBatchApproveExecutesEachExactlyOnce(t *testing.T) {
	svc, fx, _, action1 := approvalFixture(t)
	_, action2 := seedExtraProposal(t, svc, 1)
	_, action3 := seedExtraProposal(t, svc, 2)

	ids := []int64{action1, action2, action3}
	results := svc.ApproveActions(testAdminID, ids)

	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	for i, r := range results {
		if r.ID != ids[i] {
			t.Fatalf("result[%d].ID = %d, want %d (request order preserved)", i, r.ID, ids[i])
		}
		if r.Status != ActionExecuted {
			t.Fatalf("result[%d] = %+v, want executed", i, r)
		}
	}
	if fx.count() != 3 {
		t.Fatalf("executor ran %d times, want exactly 3", fx.count())
	}
	for _, id := range ids {
		var decidedBy int64
		if err := svc.db.QueryRow("SELECT decided_by FROM agent_actions WHERE id = ?", id).Scan(&decidedBy); err != nil {
			t.Fatalf("read decided_by for %d: %v", id, err)
		}
		if decidedBy != testAdminID {
			t.Fatalf("action %d decided_by = %d, want admin %d", id, decidedBy, testAdminID)
		}
	}
}

// TestBatchApproveMixedOutcomesDoNotStopTheBatch: an already-decided id
// returns its durable outcome without re-running, an unknown id reports
// "error", and both leave the rest of the batch untouched.
func TestBatchApproveMixedOutcomesDoNotStopTheBatch(t *testing.T) {
	svc, fx, _, decided := approvalFixture(t)
	_, fresh := seedExtraProposal(t, svc, 1)

	// Decide the first proposal ahead of the batch (executor call #1).
	if _, err := svc.ApproveAction(testAdminID, decided, nil); err != nil {
		t.Fatalf("pre-approve: %v", err)
	}

	results := svc.ApproveActions(testAdminID, []int64{decided, 999999, fresh})
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	if results[0].Status != ActionExecuted {
		t.Fatalf("already-decided item = %+v, want durable executed", results[0])
	}
	if results[1].Status != "error" || results[1].Detail == "" {
		t.Fatalf("unknown-id item = %+v, want error with detail", results[1])
	}
	if results[2].Status != ActionExecuted {
		t.Fatalf("fresh item = %+v, want executed", results[2])
	}
	// One pre-batch execution + the fresh item; the already-decided id must
	// not have re-run (at-most-once holds inside a batch too).
	if fx.count() != 2 {
		t.Fatalf("executor ran %d times, want exactly 2", fx.count())
	}
}

// TestBatchApproveSkipsRecoveringConflictAndContinues: an item whose arr
// began recovering between the preflight and its execution claim is reported
// "skipped" with the conflict reason, executes nothing, and the batch moves
// on to the next proposal.
func TestBatchApproveSkipsRecoveringConflictAndContinues(t *testing.T) {
	svc, fx, _, action1 := approvalFixture(t)
	skipIssue, skipAction := seedExtraProposal(t, svc, 1)

	// Mirror TestPostClaimRecoveryCancelsApprovalBeforeExecutor: the skip
	// issue's first probe (pre-CAS preflight) passes, its post-claim re-read
	// reports the arr actively recovering. Every other issue stays quiet.
	calls := map[int64]int{}
	svc.recoveryProbe = func(iss *Issue) (arrRecoveryProbe, error) {
		calls[iss.ID]++
		if iss.ID == skipIssue && calls[iss.ID] > 1 {
			return arrRecoveryProbe{active: true, item: arr.QueueObservation{
				DownloadID: "retry-batch",
				Media:      arr.QueueMediaContext{QueueID: 101, TmdbID: 43},
			}}, nil
		}
		return arrRecoveryProbe{}, nil
	}

	results := svc.ApproveActions(testAdminID, []int64{skipAction, action1})
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].Status != "skipped" || !strings.Contains(results[0].Detail, "recovering") {
		t.Fatalf("recovering item = %+v, want skipped with recovery detail", results[0])
	}
	if results[1].Status != ActionExecuted {
		t.Fatalf("second item = %+v, want executed (batch continued past the conflict)", results[1])
	}
	if fx.count() != 1 {
		t.Fatalf("executor ran %d times, want exactly 1 (skipped item must not mutate)", fx.count())
	}
}

// TestBatchApproveCollapsesDuplicateIDs: repeating an id in one request
// decides it once and yields one result row.
func TestBatchApproveCollapsesDuplicateIDs(t *testing.T) {
	svc, fx, _, actionID := approvalFixture(t)

	results := svc.ApproveActions(testAdminID, []int64{actionID, actionID})
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1 (duplicates collapse)", len(results))
	}
	if results[0].Status != ActionExecuted {
		t.Fatalf("result = %+v, want executed", results[0])
	}
	if fx.count() != 1 {
		t.Fatalf("executor ran %d times, want exactly 1", fx.count())
	}
}

// batchApproveHTTP posts the given body to the approve-batch handler as the
// seeded admin and returns the recorder.
func batchApproveHTTP(t *testing.T, svc *Service, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/agent-actions/approve-batch", strings.NewReader(body))
	ctx := context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: testAdminID, Role: auth.RoleAdmin})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	NewHandler(svc).BatchApproveActions(rec, req)
	return rec
}

// TestBatchApproveHandler pins the wire contract: a valid body returns 200
// with per-item results; an empty/absent id list and an oversized one are
// rejected with 400 before any decision is attempted.
func TestBatchApproveHandler(t *testing.T) {
	svc, fx, _, actionID := approvalFixture(t)

	rec := batchApproveHTTP(t, svc, fmt.Sprintf(`{"ids":[%d]}`, actionID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want 200", rec.Code, rec.Body.String())
	}
	var resp BatchApproveResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].ID != actionID || resp.Results[0].Status != ActionExecuted {
		t.Fatalf("response = %+v, want one executed result for %d", resp, actionID)
	}
	if fx.count() != 1 {
		t.Fatalf("executor ran %d times, want 1", fx.count())
	}

	if rec := batchApproveHTTP(t, svc, `{"ids":[]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty ids status = %d, want 400", rec.Code)
	}
	if rec := batchApproveHTTP(t, svc, `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing ids status = %d, want 400", rec.Code)
	}
	over := make([]string, batchApproveMaxIDs+1)
	for i := range over {
		over[i] = fmt.Sprint(i + 1)
	}
	if rec := batchApproveHTTP(t, svc, `{"ids":[`+strings.Join(over, ",")+`]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized ids status = %d, want 400", rec.Code)
	}
	// Validation rejections must not have decided anything.
	if fx.count() != 1 {
		t.Fatalf("executor ran %d times after rejected requests, want still 1", fx.count())
	}
}

// D6: a destructive fix never rides a batch — the server refuses it per-item
// whatever client sent the list, and the rest of the batch still runs.
func TestBatchApproveRefusesDestructiveKinds(t *testing.T) {
	svc, _, _ := setupTestService(t)
	res, err := svc.db.Exec(
		`INSERT INTO issues (source, status, media_type, tmdb_id, title, detail)
		 VALUES ('auto', 'awaiting_approval', 'tv', 615, 'Show', 'pre-air')`,
	)
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issueID, _ := res.LastInsertId()
	if _, err := svc.db.Exec(
		`INSERT INTO agent_runs (issue_id, trigger, status, model) VALUES (?, 'auto', 'waiting_approval', 'test')`,
		issueID,
	); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	var runID int64
	_ = svc.db.QueryRow("SELECT id FROM agent_runs WHERE issue_id = ?", issueID).Scan(&runID)
	if _, err := svc.db.Exec(
		`INSERT INTO agent_actions (issue_id, run_id, kind, params, rationale, risk, status, fingerprint, tool_use_id)
		 VALUES (?, ?, 'delete_media_files', '{"media_type":"tv","tmdb_id":615,"season":11,"episodes":[1],"blocklist":true}', 'impossible files', 'mutating', 'proposed', 'fp-del', 'tu-del')`,
		issueID, runID,
	); err != nil {
		t.Fatalf("seed destructive proposal: %v", err)
	}
	var actionID int64
	_ = svc.db.QueryRow("SELECT id FROM agent_actions WHERE issue_id = ?", issueID).Scan(&actionID)

	results := svc.ApproveActions(1, []int64{actionID})
	if len(results) != 1 || results[0].Status != "skipped" {
		t.Fatalf("batch over a destructive kind = %+v, want a per-item skip", results)
	}
	var status string
	_ = svc.db.QueryRow("SELECT status FROM agent_actions WHERE id = ?", actionID).Scan(&status)
	if status != ActionProposed {
		t.Fatalf("destructive proposal after batch = %q, want untouched %q", status, ActionProposed)
	}
}
