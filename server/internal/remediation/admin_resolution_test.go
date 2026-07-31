package remediation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
)

func TestAdminResolutionClosesAggregateWithDistinctAudit(t *testing.T) {
	svc, fx, issueID, actionID := approvalFixture(t)
	const note = "Verified in Radarr that the replacement imported successfully."

	issue, err := svc.ResolveIssueByAdmin(
		context.Background(), testAdminID, issueID, AdminDispositionResolved, note,
	)
	if err != nil {
		t.Fatalf("ResolveIssueByAdmin: %v", err)
	}
	if issue.Status != IssueResolved || issue.Resolution != note || issue.ResolutionKind != ResolutionAdminCompleted || issue.ClosedAt == nil || !issue.Read {
		t.Fatalf("resolved issue = %+v", issue)
	}
	if fx.count() != 0 {
		t.Fatalf("executor ran %d times during admin completion", fx.count())
	}

	action, err := svc.GetAction(actionID)
	if err != nil {
		t.Fatalf("GetAction: %v", err)
	}
	if action.Status != ActionSuperseded || action.CanDecide {
		t.Fatalf("proposal after completion = %q can_decide=%v", action.Status, action.CanDecide)
	}
	var runStatus, stopReason string
	if err := svc.db.QueryRow(
		"SELECT status, COALESCE(stop_reason,'') FROM agent_runs WHERE issue_id = ?", issueID,
	).Scan(&runStatus, &stopReason); err != nil {
		t.Fatalf("load run: %v", err)
	}
	if runStatus != "aborted" || stopReason != "admin_completed" {
		t.Fatalf("run after completion = %s/%s", runStatus, stopReason)
	}

	thread, err := svc.IssueThread(issueID)
	if err != nil {
		t.Fatalf("IssueThread: %v", err)
	}
	if len(thread) != 1 || thread[0].AuthorKind != AuthorAdmin || thread[0].AuthorName == nil || *thread[0].AuthorName != "admin" || thread[0].Body != note {
		t.Fatalf("admin completion audit = %+v", thread)
	}

	// Dismiss remains a separate provenance and does not masquerade as a
	// reviewed resolution.
	dismissSvc, _, dismissIssueID, _ := approvalFixture(t)
	if err := dismissSvc.DismissIssue(dismissIssueID); err != nil {
		t.Fatalf("DismissIssue: %v", err)
	}
	dismissed, err := dismissSvc.GetIssue(dismissIssueID)
	if err != nil {
		t.Fatalf("GetIssue dismissed: %v", err)
	}
	if dismissed.Status != IssueDismissed || dismissed.ResolutionKind != ResolutionAdminDismissed {
		t.Fatalf("dismissed provenance = %q/%q", dismissed.Status, dismissed.ResolutionKind)
	}
}

func TestAdminResolutionAllowsVerifiedUnknownOutcome(t *testing.T) {
	svc, _, issueID, actionID := approvalFixture(t)
	if _, err := svc.db.Exec(
		"UPDATE agent_actions SET status = ?, result_text = 'remote outcome unknown' WHERE id = ?",
		ActionOutcomeUnknown, actionID,
	); err != nil {
		t.Fatalf("seed unknown action: %v", err)
	}
	if _, err := svc.db.Exec(
		"UPDATE issues SET status = ?, resolution = 'Verify the arr manually' WHERE id = ?",
		IssueNeedsAdmin, issueID,
	); err != nil {
		t.Fatalf("seed needs_admin: %v", err)
	}
	if _, err := svc.db.Exec(
		"UPDATE agent_runs SET status = 'aborted', stop_reason = 'action_outcome_unknown' WHERE issue_id = ?",
		issueID,
	); err != nil {
		t.Fatalf("seed aborted run: %v", err)
	}

	issue, err := svc.ResolveIssueByAdmin(
		context.Background(), testAdminID, issueID, AdminDispositionWontFix,
		"Checked Radarr and the library manually; the intended change cannot be confirmed, so no further automatic fix will run.",
	)
	if err != nil {
		t.Fatalf("ResolveIssueByAdmin: %v", err)
	}
	if issue.Status != IssueWontFix || issue.ResolutionKind != ResolutionAdminCompleted {
		t.Fatalf("completion = %q/%q", issue.Status, issue.ResolutionKind)
	}
	action, err := svc.GetAction(actionID)
	if err != nil {
		t.Fatalf("GetAction: %v", err)
	}
	if action.Status != ActionOutcomeUnknown {
		t.Fatalf("unknown outcome history changed to %q", action.Status)
	}
}

func TestAdminResolutionValidatesAndRollsBackAuditFailure(t *testing.T) {
	svc, _, issueID, actionID := approvalFixture(t)
	for name, tc := range map[string]struct {
		disposition AdminIssueDisposition
		note        string
	}{
		"invalid disposition": {AdminIssueDisposition("dismissed"), "Reviewed"},
		"missing note":        {AdminDispositionResolved, "   "},
		"oversized note":      {AdminDispositionResolved, strings.Repeat("x", maxAdminResolutionNoteBytes+1)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.ResolveIssueByAdmin(context.Background(), testAdminID, issueID, tc.disposition, tc.note); err == nil {
				t.Fatal("invalid completion succeeded")
			}
		})
	}

	// The issue update and audit message share one transaction. A missing admin
	// FK makes the audit insert fail, so closure and proposal supersession must
	// both roll back.
	if _, err := svc.ResolveIssueByAdmin(
		context.Background(), 123456, issueID, AdminDispositionResolved, "Reviewed manually.",
	); err == nil {
		t.Fatal("completion with missing admin row succeeded")
	}
	issue, err := svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.ClosedAt != nil || issue.Status != IssueAwaitingApproval {
		t.Fatalf("issue partially closed after audit failure: %+v", issue)
	}
	action, err := svc.GetAction(actionID)
	if err != nil {
		t.Fatalf("GetAction: %v", err)
	}
	if action.Status != ActionProposed || !action.CanDecide {
		t.Fatalf("proposal partially superseded after audit failure: %+v", action)
	}
}

func TestAdminResolutionRaceReturnsConflict(t *testing.T) {
	svc, _, issueID, _ := approvalFixture(t)
	first, err := svc.ResolveIssueByAdmin(
		context.Background(), testAdminID, issueID, AdminDispositionResolved, "Verified imported.",
	)
	if err != nil {
		t.Fatalf("first completion: %v", err)
	}
	if _, err := svc.ResolveIssueByAdmin(
		context.Background(), testAdminID, issueID, AdminDispositionWontFix, "Conflicting judgment.",
	); !errors.Is(err, ErrIssueCompletionConflict) {
		t.Fatalf("second completion error = %v, want conflict", err)
	}
	after, err := svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if after.Status != first.Status || after.Resolution != first.Resolution {
		t.Fatalf("losing completion overwrote winner: before=%+v after=%+v", first, after)
	}
}

func TestAdminResolutionRejectsExecutingAction(t *testing.T) {
	svc, _, issueID, actionID := approvalFixture(t)
	if _, err := svc.db.Exec("UPDATE agent_actions SET status = ? WHERE id = ?", ActionExecuting, actionID); err != nil {
		t.Fatalf("seed executing action: %v", err)
	}
	if _, err := svc.ResolveIssueByAdmin(
		context.Background(), testAdminID, issueID, AdminDispositionResolved, "Verified manually.",
	); !errors.Is(err, ErrIssueCompletionConflict) {
		t.Fatalf("completion error = %v, want conflict", err)
	}
	issue, err := svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.ClosedAt != nil {
		t.Fatal("issue closed while approved action was executing")
	}
}

func TestAdminResolutionAPIAndConflictStatus(t *testing.T) {
	svc, _, issueID, _ := approvalFixture(t)
	h := NewHandler(svc)

	first := postAdminResolution(t, h, issueID, `{"disposition":"resolved","note":"Verified imported in Radarr."}`)
	if first.Code != http.StatusOK {
		t.Fatalf("first resolution status = %d, body %s", first.Code, first.Body.String())
	}
	var issue Issue
	if err := json.NewDecoder(first.Body).Decode(&issue); err != nil {
		t.Fatalf("decode resolution response: %v", err)
	}
	if issue.Status != IssueResolved || issue.ResolutionKind != ResolutionAdminCompleted {
		t.Fatalf("resolution response = %+v", issue)
	}

	second := postAdminResolution(t, h, issueID, `{"disposition":"wont_fix","note":"Too late."}`)
	if second.Code != http.StatusConflict {
		t.Fatalf("raced resolution status = %d, body %s; want 409", second.Code, second.Body.String())
	}
}

func postAdminResolution(t *testing.T, h *Handler, issueID int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	id := strconv.FormatInt(issueID, 10)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/issues/"+id+"/resolve", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, auth.ClaimsKey, &auth.Claims{UserID: testAdminID, Role: auth.RoleAdmin})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ResolveIssue(rec, req)
	return rec
}
