package remediation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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

// A freshly reported issue is passive/read while live arr state is observed.
func TestNewIssueStartsPassiveRead(t *testing.T) {
	svc, _, reporterID := setupTestService(t)
	r, err := svc.CreateUserIssue(reporterID, movieReq(1))
	if err != nil {
		t.Fatalf("CreateUserIssue: %v", err)
	}
	if !readFlag(t, svc, r.IssueID) {
		t.Fatal("an observing issue should stay read until promotion")
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

// A non-admin report against a library outside their visible set is a 403 in
// requester vocabulary — never a 400 that would confirm what exists.
func TestCreateIssueForbiddenInstanceIs403(t *testing.T) {
	svc, _, _ := setupTestService(t)
	rowless := seedUser(t, svc.db, "http-rowless")
	h := NewHandler(svc)

	body := strings.NewReader(fmt.Sprintf(
		`{"instance_id":%q,"media_type":"movie","tmdb_id":1,"category":%q}`,
		testRadarrInstanceID2, CategoryOther,
	))
	req := httptest.NewRequest(http.MethodPost, "/api/issues", body)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{
		UserID: rowless,
		Role:   auth.RoleUser,
	}))

	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("forbidden-instance report = %d %s, want 403", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not available to you") {
		t.Fatalf("forbidden body = %q, want requester vocabulary", rec.Body.String())
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
// reporter viewing their own issue leaves its passive read state unchanged.
func TestAdminGetMarksReadReporterDoesNot(t *testing.T) {
	svc, _, reporterID := setupTestService(t)
	h := NewHandler(svc)
	r, err := svc.CreateUserIssue(reporterID, movieReq(1))
	if err != nil {
		t.Fatalf("CreateUserIssue: %v", err)
	}

	// The reporter viewing their own issue must NOT mark it read.
	reporterView := getIssueDetail(t, h, r.IssueID, &auth.Claims{UserID: reporterID, Role: auth.RoleUser})
	if !reporterView.Issue.Read {
		t.Fatal("reporter view payload should preserve passive read state")
	}
	if !readFlag(t, svc, r.IssueID) {
		t.Fatal("reporter view must not alter the issue read state")
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

// TestGetRedactsThreadBodiesForNonAdmins pins the read-time thread boundary:
// even if a thread writer forgets secrets.RedactText (simulated here with a
// direct insert), a credential quoted in a message body never reaches the
// reporter — while the admin view keeps the raw text for diagnosis.
func TestGetRedactsThreadBodiesForNonAdmins(t *testing.T) {
	svc, _, reporterID := setupTestService(t)
	h := NewHandler(svc)
	r, err := svc.CreateUserIssue(reporterID, movieReq(1))
	if err != nil {
		t.Fatalf("CreateUserIssue: %v", err)
	}
	const raw = "Retrying via http://thread-user:thread-pass@qbit.invalid:8080/api?apikey=thread-key shortly"
	if _, err := svc.db.Exec(
		`INSERT INTO issue_messages (issue_id, author_kind, author_id, body) VALUES (?, 'agent', NULL, ?)`,
		r.IssueID, raw,
	); err != nil {
		t.Fatalf("seed unredacted message: %v", err)
	}

	reporterView := getIssueDetail(t, h, r.IssueID, &auth.Claims{UserID: reporterID, Role: auth.RoleUser})
	var reporterBody string
	for _, m := range reporterView.Thread {
		if m.AuthorKind == "agent" {
			reporterBody = m.Body
		}
	}
	if reporterBody == "" {
		t.Fatalf("agent message missing from reporter thread: %+v", reporterView.Thread)
	}
	for _, secret := range []string{"thread-user", "thread-pass", "thread-key"} {
		if strings.Contains(reporterBody, secret) {
			t.Fatalf("reporter thread leaked %q: %q", secret, reporterBody)
		}
	}
	if !strings.Contains(reporterBody, "qbit.invalid") {
		t.Fatalf("redaction destroyed the message instead of scrubbing it: %q", reporterBody)
	}

	adminView := getIssueDetail(t, h, r.IssueID, &auth.Claims{UserID: 9999, Role: auth.RoleAdmin})
	adminSawRaw := false
	for _, m := range adminView.Thread {
		if m.Body == raw {
			adminSawRaw = true
		}
	}
	if !adminSawRaw {
		t.Fatalf("admin view no longer carries the raw diagnostic body: %+v", adminView.Thread)
	}
}
