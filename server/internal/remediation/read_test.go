package remediation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
)

// movieReq builds a distinct-scope movie report so successive creates open
// separate issues (dedupe is per reporter+scope+category) rather than bumping
// occurrences.
func movieReq(tmdbID int) *CreateIssueRequest {
	return &CreateIssueRequest{InstanceID: testRadarrInstanceID, MediaType: "movie", TmdbID: tmdbID, Category: CategoryOther, Reason: "x"}
}

func readFlag(t *testing.T, svc *Service, issueID int64) bool {
	t.Helper()
	iss, err := svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("GetIssue(%d): %v", issueID, err)
	}
	return iss.Read
}

// TestNewIssueStartsUnread confirms the column default: a freshly reported issue
// is unread until an admin views it.
func TestNewIssueStartsUnread(t *testing.T) {
	svc, _, reporterID := setupTestService(t)
	r, err := svc.CreateUserIssue(reporterID, movieReq(1))
	if err != nil {
		t.Fatalf("CreateUserIssue: %v", err)
	}
	if readFlag(t, svc, r.IssueID) {
		t.Fatal("a new issue should start unread")
	}
}

// TestMarkIssueRead sets the read flag directly (the admin-view side effect).
func TestMarkIssueRead(t *testing.T) {
	svc, _, reporterID := setupTestService(t)
	r, err := svc.CreateUserIssue(reporterID, movieReq(1))
	if err != nil {
		t.Fatalf("CreateUserIssue: %v", err)
	}
	if err := svc.MarkIssueRead(r.IssueID); err != nil {
		t.Fatalf("MarkIssueRead: %v", err)
	}
	if !readFlag(t, svc, r.IssueID) {
		t.Fatal("MarkIssueRead should set read = 1")
	}
	// Idempotent + a no-op (no error) for a nonexistent issue.
	if err := svc.MarkIssueRead(r.IssueID); err != nil {
		t.Fatalf("MarkIssueRead (repeat): %v", err)
	}
	if err := svc.MarkIssueRead(999999); err != nil {
		t.Fatalf("MarkIssueRead (missing): %v", err)
	}
}

// TestDismissIssueMarksRead confirms an admin dismissal marks the issue read
// (an admin status change never re-flags unread).
func TestDismissIssueMarksRead(t *testing.T) {
	svc, _, reporterID := setupTestService(t)
	r, err := svc.CreateUserIssue(reporterID, movieReq(1))
	if err != nil {
		t.Fatalf("CreateUserIssue: %v", err)
	}
	if err := svc.DismissIssue(r.IssueID); err != nil {
		t.Fatalf("DismissIssue: %v", err)
	}
	if !readFlag(t, svc, r.IssueID) {
		t.Fatal("dismiss (an admin action) should mark the issue read")
	}
}

// TestConcludeIssueReadReflectsSetting drives the core rule: a conclude is a
// non-admin (agent/system) status change, so it flips to unread — UNLESS it
// resolved and "mark resolved issues as read" is on. wont_fix always flips
// unread regardless of the setting.
func TestConcludeIssueReadReflectsSetting(t *testing.T) {
	svc, _, reporterID := setupTestService(t)
	ctx := context.Background()

	// Default setting is ON: resolving marks the issue read.
	r1, err := svc.CreateUserIssue(reporterID, movieReq(1))
	if err != nil {
		t.Fatalf("CreateUserIssue: %v", err)
	}
	if err := svc.ConcludeIssue(ctx, r1.IssueID, IssueResolved, "done"); err != nil {
		t.Fatalf("ConcludeIssue resolved (setting on): %v", err)
	}
	if !readFlag(t, svc, r1.IssueID) {
		t.Fatal("resolved issue should be read when mark_resolved_as_read is on")
	}

	// Setting OFF: resolving flips back to unread even if it was read.
	if _, err := svc.SetSettings(Settings{MarkResolvedAsRead: false}); err != nil {
		t.Fatalf("SetSettings off: %v", err)
	}
	r2, err := svc.CreateUserIssue(reporterID, movieReq(2))
	if err != nil {
		t.Fatalf("CreateUserIssue: %v", err)
	}
	if err := svc.MarkIssueRead(r2.IssueID); err != nil {
		t.Fatalf("MarkIssueRead: %v", err)
	}
	if err := svc.ConcludeIssue(ctx, r2.IssueID, IssueResolved, "done"); err != nil {
		t.Fatalf("ConcludeIssue resolved (setting off): %v", err)
	}
	if readFlag(t, svc, r2.IssueID) {
		t.Fatal("resolved issue should be unread when mark_resolved_as_read is off")
	}

	// wont_fix ignores the setting (never a "clean resolution"): setting back ON,
	// concluding wont_fix on a read issue still flips it unread.
	if _, err := svc.SetSettings(Settings{MarkResolvedAsRead: true}); err != nil {
		t.Fatalf("SetSettings on: %v", err)
	}
	r3, err := svc.CreateUserIssue(reporterID, movieReq(3))
	if err != nil {
		t.Fatalf("CreateUserIssue: %v", err)
	}
	if err := svc.MarkIssueRead(r3.IssueID); err != nil {
		t.Fatalf("MarkIssueRead: %v", err)
	}
	if err := svc.ConcludeIssue(ctx, r3.IssueID, IssueWontFix, "nope"); err != nil {
		t.Fatalf("ConcludeIssue wont_fix: %v", err)
	}
	if readFlag(t, svc, r3.IssueID) {
		t.Fatal("wont_fix issue should be unread even when mark_resolved_as_read is on")
	}
}

// TestReplyReadFlip separates the two actors that reach the awaiting_user resume
// path: a reporter's reply re-flags the issue unread (a non-admin status change),
// while an admin's reply on the same issue does not (an admin action).
func TestReplyReadFlip(t *testing.T) {
	svc, _, reporterID := setupTestService(t)
	r, err := svc.CreateUserIssue(reporterID, movieReq(1))
	if err != nil {
		t.Fatalf("CreateUserIssue: %v", err)
	}
	// Simulate the agent having asked the reporter a question and an admin having
	// already viewed it: awaiting_user + read.
	if _, err := svc.db.Exec("UPDATE issues SET status = ?, read = 1 WHERE id = ?", IssueAwaitingUser, r.IssueID); err != nil {
		t.Fatalf("seed awaiting_user: %v", err)
	}

	// An admin reply (also routed through resumeOnReporterReply) must NOT re-flag.
	if err := svc.PostReply(r.IssueID, AuthorAdmin, 0, "on it"); err != nil {
		t.Fatalf("PostReply admin: %v", err)
	}
	if !readFlag(t, svc, r.IssueID) {
		t.Fatal("an admin reply must not re-flag the issue unread")
	}

	// A reporter reply re-flags it unread so the admin sees the response.
	if err := svc.PostReply(r.IssueID, AuthorUser, reporterID, "still broken"); err != nil {
		t.Fatalf("PostReply reporter: %v", err)
	}
	if readFlag(t, svc, r.IssueID) {
		t.Fatal("a reporter reply should re-flag the issue unread")
	}
}

// getIssueDetail invokes the Get handler with injected claims + the {id} chi URL
// param, mirroring how the real router would dispatch it.
func getIssueDetail(t *testing.T, h *Handler, issueID int64, claims *auth.Claims) IssueDetail {
	t.Helper()
	id := strconv.FormatInt(issueID, 10)
	req := httptest.NewRequest(http.MethodGet, "/api/issues/"+id, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, auth.ClaimsKey, claims)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Get status = %d, body %s", rec.Code, rec.Body.String())
	}
	var detail IssueDetail
	if err := json.NewDecoder(rec.Body).Decode(&detail); err != nil {
		t.Fatalf("decode issue detail: %v", err)
	}
	return detail
}

// TestAdminGetMarksReadReporterDoesNot confirms the handler side effect: an admin
// opening the thread marks the issue read (and the payload reflects it), while the
// reporter viewing their own issue leaves it unread.
func TestAdminGetMarksReadReporterDoesNot(t *testing.T) {
	svc, _, reporterID := setupTestService(t)
	h := NewHandler(svc)
	r, err := svc.CreateUserIssue(reporterID, movieReq(1))
	if err != nil {
		t.Fatalf("CreateUserIssue: %v", err)
	}

	// The reporter viewing their own issue must NOT mark it read.
	reporterView := getIssueDetail(t, h, r.IssueID, &auth.Claims{UserID: reporterID, Role: auth.RoleUser})
	if reporterView.Issue.Read {
		t.Fatal("reporter view payload should report unread")
	}
	if readFlag(t, svc, r.IssueID) {
		t.Fatal("reporter view must not mark the issue read")
	}

	// An admin opening the thread marks it read, reflected in the payload and DB.
	adminView := getIssueDetail(t, h, r.IssueID, &auth.Claims{UserID: 9999, Role: auth.RoleAdmin})
	if !adminView.Issue.Read {
		t.Fatal("admin view payload should report read")
	}
	if !readFlag(t, svc, r.IssueID) {
		t.Fatal("admin view must mark the issue read")
	}
}
