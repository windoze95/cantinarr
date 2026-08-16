package remediation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/db"
	"strings"
)

// confirmFixture is the shape the reporter-confirmation feature exists for: a
// user report the agent investigated, applied an approved fix to, could not
// itself prove, and handed to an administrator. Before the reporter had a
// button, every such report ended here — and the admin's only move was to
// rubber-stamp a judgment about content they had never watched.
type confirmFixture struct {
	svc        *Service
	notifier   *fakeNotifier
	issueID    int64
	runID      int64
	actionID   int64 // the approved fix that actually reached the arr
	reporterID int64
	otherID    int64 // a second non-admin who is NOT this issue's reporter
}

func reporterConfirmFixture(t *testing.T) *confirmFixture {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	notifier := &fakeNotifier{}
	svc := NewService(database, nil, nil, notifier)

	reporterID := seedUser(t, database, "reporter")
	otherID := seedUser(t, database, "bystander")
	if _, err := database.Exec(
		"INSERT INTO users (id, username, password_hash, role) VALUES (?, 'admin', '', 'admin')", testAdminID,
	); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO service_instances (id, service_type, name, url, api_key)
		 VALUES ('radarr-main', 'radarr', 'Main Movies', 'http://radarr.test', 'key')`,
	); err != nil {
		t.Fatalf("seed target instance: %v", err)
	}

	// needs_admin with an open closed_at: the terminus every user report used to
	// reach once the agent had done everything it could prove.
	issueRes, err := database.Exec(
		`INSERT INTO issues (source, status, category, media_type, tmdb_id, title, detail, instance_id, reporter_id)
		 VALUES (?, ?, ?, 'movie', 42, 'Test Movie', 'this is the wrong cut of the film', 'radarr-main', ?)`,
		SourceUser, IssueNeedsAdmin, CategoryWrongContent, reporterID,
	)
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issueID, _ := issueRes.LastInsertId()

	runRes, err := database.Exec(
		`INSERT INTO agent_runs (issue_id, trigger, status, model, step_count, stop_reason, finished_at)
		 VALUES (?, 'user_report', ?, 'claude-haiku-4-5', 6, ?, CURRENT_TIMESTAMP)`,
		issueID, runStatusGaveUp, stopUnverifiedClose,
	)
	if err != nil {
		t.Fatalf("seed run: %v", err)
	}
	runID, _ := runRes.LastInsertId()

	fx := &confirmFixture{
		svc: svc, notifier: notifier, issueID: issueID, runID: runID,
		reporterID: reporterID, otherID: otherID,
	}
	fx.actionID = seedConfirmAction(t, fx, "toolu_fix_1", ActionExecuted)
	return fx
}

// seedConfirmAction inserts one agent_actions row for the fixture's issue in the
// given lifecycle state. executed_at is stamped only for the states that mean a
// dispatch actually left the building, because executed_at IS NOT NULL is half
// of what issueHasExecutedFix reads.
func seedConfirmAction(t *testing.T, fx *confirmFixture, toolUseID, status string) int64 {
	t.Helper()
	const params = `{"media_type":"movie","tmdb_id":42}`
	fp := fingerprint(fx.issueID, fx.runID, toolUseID, ActionDeleteMediaFiles, json.RawMessage(params))
	var executedAt any
	if status == ActionExecuted || status == ActionOutcomeUnknown {
		executedAt = time.Now().UTC()
	}
	res, err := fx.svc.db.Exec(
		`INSERT INTO agent_actions
		   (issue_id, run_id, tool_use_id, kind, params, rationale, risk, status, fingerprint,
		    decided_by, decided_at, executed_at, result_text)
		 VALUES (?, ?, ?, ?, ?, 'the imported file is the wrong cut', 'mutating', ?, ?, ?,
		         CURRENT_TIMESTAMP, ?, 'Deleted 1 file and started a fresh search.')`,
		fx.issueID, fx.runID, toolUseID, string(ActionDeleteMediaFiles), params,
		status, fp, testAdminID, executedAt,
	)
	if err != nil {
		t.Fatalf("seed %s action: %v", status, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func confirmExec(t *testing.T, fx *confirmFixture, query string, args ...any) {
	t.Helper()
	if _, err := fx.svc.db.Exec(query, args...); err != nil {
		t.Fatalf("seed (%s): %v", query, err)
	}
}

// assertCanConfirm reloads the issue (these tests mutate rows under it) and
// checks the gate's verdict.
func assertCanConfirm(t *testing.T, fx *confirmFixture, want bool, what string) {
	t.Helper()
	issue, err := fx.svc.GetIssue(fx.issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	got, err := fx.svc.CanReporterConfirmFix(issue)
	if err != nil {
		t.Fatalf("CanReporterConfirmFix: %v", err)
	}
	if got != want {
		t.Fatalf("CanReporterConfirmFix(%s) = %v, want %v", what, got, want)
	}
}

func confirmThread(t *testing.T, fx *confirmFixture) []IssueMessage {
	t.Helper()
	thread, err := fx.svc.IssueThread(fx.issueID)
	if err != nil {
		t.Fatalf("IssueThread: %v", err)
	}
	return thread
}

func confirmIssue(t *testing.T, fx *confirmFixture) *Issue {
	t.Helper()
	issue, err := fx.svc.GetIssue(fx.issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	return issue
}

// ---------------------------------------------------------------------------
// The gate: CanReporterConfirmFix
// ---------------------------------------------------------------------------

func TestCanReporterConfirmFixAllowsAnOpenUserReportWithAnAppliedFix(t *testing.T) {
	fx := reporterConfirmFixture(t)
	assertCanConfirm(t, fx, true, "an open user report whose approved fix reached the arr")
}

// A nil issue is a caller bug, not a permission question: the gate answers no
// rather than dereferencing it.
func TestCanReporterConfirmFixRefusesANilIssue(t *testing.T) {
	fx := reporterConfirmFixture(t)
	allowed, err := fx.svc.CanReporterConfirmFix(nil)
	if err != nil || allowed {
		t.Fatalf("CanReporterConfirmFix(nil) = (%v, %v), want (false, nil)", allowed, err)
	}
}

// An auto-detected incident carries its own typed proof — the exact queue target
// is gone, the exact file is present — so it never needed anyone's opinion. It
// also has nobody to ask.
func TestCanReporterConfirmFixRefusesAnAutoDetectedIssue(t *testing.T) {
	fx := reporterConfirmFixture(t)
	confirmExec(t, fx, "UPDATE issues SET source = ? WHERE id = ?", SourceAuto, fx.issueID)
	assertCanConfirm(t, fx, false, "an auto-detected incident")
}

// A closed issue is done. Re-confirming it would rewrite a settled provenance
// with a different one.
func TestCanReporterConfirmFixRefusesAClosedIssue(t *testing.T) {
	for name, status := range map[string]string{
		"resolved":  IssueResolved,
		"wont_fix":  IssueWontFix,
		"dismissed": IssueDismissed,
	} {
		t.Run(name, func(t *testing.T) {
			fx := reporterConfirmFixture(t)
			confirmExec(t, fx,
				"UPDATE issues SET status = ?, closed_at = CURRENT_TIMESTAMP WHERE id = ?", status, fx.issueID)
			assertCanConfirm(t, fx, false, "an issue closed as "+status)
		})
	}
}

// The condition that stops "confirmed fixed" from recording approval of
// something that never happened. Every row below is a fix that never reached the
// arr — including the two that LOOK applied at a glance: a definitive preflight
// failure (nothing was sent) and an executed row whose dispatch stamp is missing.
func TestCanReporterConfirmFixRefusesWhenNoFixReachedTheArr(t *testing.T) {
	for name, seed := range map[string]func(t *testing.T, fx *confirmFixture){
		"no fix was ever proposed": func(t *testing.T, fx *confirmFixture) {
			confirmExec(t, fx, "DELETE FROM agent_actions WHERE id = ?", fx.actionID)
		},
		"the fix is still awaiting approval": func(t *testing.T, fx *confirmFixture) {
			confirmExec(t, fx,
				"UPDATE agent_actions SET status = ?, decided_at = NULL, executed_at = NULL WHERE id = ?",
				ActionProposed, fx.actionID)
		},
		"the admin denied the fix": func(t *testing.T, fx *confirmFixture) {
			confirmExec(t, fx,
				"UPDATE agent_actions SET status = ?, executed_at = NULL WHERE id = ?", ActionDenied, fx.actionID)
		},
		"the fix failed definitively before dispatch": func(t *testing.T, fx *confirmFixture) {
			confirmExec(t, fx,
				"UPDATE agent_actions SET status = ?, executed_at = NULL WHERE id = ?", ActionFailed, fx.actionID)
		},
		"the fix was superseded": func(t *testing.T, fx *confirmFixture) {
			confirmExec(t, fx,
				"UPDATE agent_actions SET status = ?, executed_at = NULL WHERE id = ?", ActionSuperseded, fx.actionID)
		},
		"an executed row was never stamped as dispatched": func(t *testing.T, fx *confirmFixture) {
			confirmExec(t, fx, "UPDATE agent_actions SET executed_at = NULL WHERE id = ?", fx.actionID)
		},
	} {
		t.Run(name, func(t *testing.T) {
			fx := reporterConfirmFixture(t)
			seed(t, fx)
			assertCanConfirm(t, fx, false, name)
		})
	}
}

// A confirmation racing a dispatch would close the issue over an outcome nobody
// has seen yet — not even the arr. An already-applied earlier fix does not
// excuse it: the reporter is judging content the in-flight action may be about
// to change under them.
func TestCanReporterConfirmFixRefusesWhileAFixIsExecuting(t *testing.T) {
	fx := reporterConfirmFixture(t)
	assertCanConfirm(t, fx, true, "the applied fix alone")

	inFlight := seedConfirmAction(t, fx, "toolu_fix_2", ActionExecuting)
	assertCanConfirm(t, fx, false, "a second fix mid-dispatch")

	// Once that dispatch lands, the gate reopens on its own.
	confirmExec(t, fx,
		"UPDATE agent_actions SET status = ?, executed_at = CURRENT_TIMESTAMP WHERE id = ?",
		ActionExecuted, inFlight)
	assertCanConfirm(t, fx, true, "the dispatch having landed")
}

// PINNED SEMANTICS: issueHasExecutedFix counts outcome_unknown alongside
// executed, so a fix whose dispatch response was lost DOES open this gate.
//
// That is the intended reading. outcome_unknown means Cantinarr cannot tell
// whether the arr accepted the mutation — and the single witness who can settle
// it is the reporter looking at the actual content. Refusing them the button
// would leave the case the server is least sure about with the fewest ways to
// close, which is exactly backwards. It is also not the loose end it looks like:
// the row still proves a dispatch was attempted, so "confirmed fixed" cannot
// record approval of something that was never even tried.
func TestCanReporterConfirmFixCountsAnUnknownOutcomeAsAnAppliedFix(t *testing.T) {
	t.Run("a prior unknown outcome does not veto a later executed fix", func(t *testing.T) {
		fx := reporterConfirmFixture(t)
		seedConfirmAction(t, fx, "toolu_fix_0", ActionOutcomeUnknown)
		assertCanConfirm(t, fx, true, "an unknown outcome beside an executed fix")
	})
	t.Run("an unknown outcome alone opens the gate", func(t *testing.T) {
		fx := reporterConfirmFixture(t)
		confirmExec(t, fx, "UPDATE agent_actions SET status = ? WHERE id = ?", ActionOutcomeUnknown, fx.actionID)
		assertCanConfirm(t, fx, true, "an unknown outcome as the only fix")
	})
}

// ---------------------------------------------------------------------------
// The transition: ReporterConfirmFix
// ---------------------------------------------------------------------------

func TestReporterConfirmFixClosesTheIssueOnTheReportersWord(t *testing.T) {
	fx := reporterConfirmFixture(t)
	// With the resolved-as-read preference OFF, a read issue can only have come
	// from the reporter_confirmed clause: this is a human decision, not an agent
	// conclusion an admin still has to look at.
	if _, err := fx.svc.SetSettings(Settings{MarkResolvedAsRead: false}); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}

	if err := fx.svc.ReporterConfirmFix(context.Background(), fx.issueID, fx.reporterID); err != nil {
		t.Fatalf("ReporterConfirmFix: %v", err)
	}

	issue := confirmIssue(t, fx)
	if issue.Status != IssueResolved || issue.ResolutionKind != ResolutionReporterConfirmed || issue.ClosedAt == nil {
		t.Fatalf("confirmed issue = status %q kind %q closed %v; want resolved/reporter_confirmed/closed",
			issue.Status, issue.ResolutionKind, issue.ClosedAt)
	}
	if issue.Resolution != reporterConfirmedResolution {
		t.Fatalf("closing note = %q, want the server-authored %q", issue.Resolution, reporterConfirmedResolution)
	}
	if !issue.Read {
		t.Fatal("a reporter confirmation is a human decision and must close read")
	}

	// The confirmation is threaded, attributed to the reporter, in their name —
	// it is their judgment, not the agent's and not the server's.
	thread := confirmThread(t, fx)
	if len(thread) != 1 {
		t.Fatalf("thread = %d messages, want exactly the confirmation: %+v", len(thread), thread)
	}
	if thread[0].AuthorKind != AuthorUser || thread[0].Body != reporterConfirmedMessage {
		t.Fatalf("confirmation message = %+v, want a user-authored %q", thread[0], reporterConfirmedMessage)
	}
	if thread[0].AuthorName == nil || *thread[0].AuthorName != "reporter" {
		t.Fatalf("confirmation author = %v, want the reporter", thread[0].AuthorName)
	}
	var authorID int64
	if err := fx.svc.db.QueryRow(
		"SELECT author_id FROM issue_messages WHERE issue_id = ?", fx.issueID,
	).Scan(&authorID); err != nil {
		t.Fatalf("load confirmation author: %v", err)
	}
	if authorID != fx.reporterID {
		t.Fatalf("confirmation author_id = %d, want the reporter %d", authorID, fx.reporterID)
	}

	// ...and it is on a CLOSED issue, which is the whole reason the write
	// precedes the close: the ordinary reply path refuses a closed issue
	// outright, so a confirmation recorded after the transition would have
	// nowhere to live.
	if err := fx.svc.PostReply(fx.issueID, AuthorUser, fx.reporterID, "one more thought"); err == nil {
		t.Fatal("PostReply accepted a message on a closed issue; the ordering this test relies on no longer means anything")
	}
	if got := len(confirmThread(t, fx)); got != 1 {
		t.Fatalf("thread grew to %d messages after a refused reply", got)
	}
}

// Only the reporter's own verdict may be recorded as the reporter's. An admin is
// covered here too, but the deliberate part of that decision lives in the
// handler test below.
func TestReporterConfirmFixRefusesANonReporter(t *testing.T) {
	for name, caller := range map[string]func(fx *confirmFixture) int64{
		"another requester": func(fx *confirmFixture) int64 { return fx.otherID },
		"an administrator":  func(fx *confirmFixture) int64 { return testAdminID },
	} {
		t.Run(name, func(t *testing.T) {
			fx := reporterConfirmFixture(t)
			if err := fx.svc.ReporterConfirmFix(context.Background(), fx.issueID, caller(fx)); err == nil {
				t.Fatal("a non-reporter closed the issue as reporter-confirmed")
			}
			issue := confirmIssue(t, fx)
			if issue.ClosedAt != nil || issue.Status != IssueNeedsAdmin || issue.ResolutionKind != "" {
				t.Fatalf("refused caller still moved the issue: %+v", issue)
			}
			if got := len(confirmThread(t, fx)); got != 0 {
				t.Fatalf("refused caller wrote %d thread messages, want 0", got)
			}
		})
	}

	// An issue with no reporter at all has nobody who could satisfy the check, so
	// no caller id — not even the original reporter's — gets through.
	t.Run("an issue with no reporter", func(t *testing.T) {
		fx := reporterConfirmFixture(t)
		confirmExec(t, fx, "UPDATE issues SET reporter_id = NULL WHERE id = ?", fx.issueID)
		for _, caller := range []int64{fx.reporterID, fx.otherID, testAdminID, 0} {
			if err := fx.svc.ReporterConfirmFix(context.Background(), fx.issueID, caller); err == nil {
				t.Fatalf("caller %d confirmed a reporter-less issue", caller)
			}
		}
		if confirmIssue(t, fx).ClosedAt != nil {
			t.Fatal("a reporter-less issue was closed as reporter-confirmed")
		}
	})
}

// The gate is not advisory: a refused state changes nothing at all — no close,
// no thread message, and no collateral damage to the in-flight fix.
func TestReporterConfirmFixRefusesWhenTheGateSaysNoAndChangesNothing(t *testing.T) {
	fx := reporterConfirmFixture(t)
	inFlight := seedConfirmAction(t, fx, "toolu_fix_2", ActionExecuting)

	if err := fx.svc.ReporterConfirmFix(context.Background(), fx.issueID, fx.reporterID); err == nil {
		t.Fatal("confirmation succeeded while a fix was mid-dispatch")
	}

	issue := confirmIssue(t, fx)
	if issue.ClosedAt != nil || issue.Status != IssueNeedsAdmin {
		t.Fatalf("refused confirmation still closed the issue: %+v", issue)
	}
	if got := len(confirmThread(t, fx)); got != 0 {
		t.Fatalf("refused confirmation wrote %d thread messages, want 0", got)
	}
	var status string
	if err := fx.svc.db.QueryRow("SELECT status FROM agent_actions WHERE id = ?", inFlight).Scan(&status); err != nil {
		t.Fatalf("load in-flight action: %v", err)
	}
	if status != ActionExecuting {
		t.Fatalf("in-flight action = %q after a refused confirmation, want it untouched", status)
	}
}

// A double tap must not double-record. The second call finds a closed issue, is
// refused by the gate before it writes anything, and leaves the first
// confirmation exactly as it was.
func TestReporterConfirmFixIsIdempotentSafe(t *testing.T) {
	fx := reporterConfirmFixture(t)
	ctx := context.Background()
	if err := fx.svc.ReporterConfirmFix(ctx, fx.issueID, fx.reporterID); err != nil {
		t.Fatalf("first confirmation: %v", err)
	}
	first := confirmIssue(t, fx)

	if err := fx.svc.ReporterConfirmFix(ctx, fx.issueID, fx.reporterID); err == nil {
		t.Fatal("a second confirmation on the closed issue succeeded")
	}

	after := confirmIssue(t, fx)
	if !after.ClosedAt.Equal(*first.ClosedAt) || after.Status != first.Status ||
		after.ResolutionKind != first.ResolutionKind || after.Resolution != first.Resolution {
		t.Fatalf("second confirmation rewrote the close: before=%+v after=%+v", first, after)
	}
	thread := confirmThread(t, fx)
	if len(thread) != 1 || thread[0].Body != reporterConfirmedMessage {
		t.Fatalf("thread after a repeated confirmation = %+v, want exactly one confirmation", thread)
	}
}

// A closing issue must never retain an executable approval. The realistic shape:
// the first fix executed, the agent proposed a SECOND one and parked, and the
// reporter — looking at media the first fix already repaired — says so.
func TestReporterConfirmFixSupersedesAStillProposedFix(t *testing.T) {
	fx := reporterConfirmFixture(t)
	proposalID := seedConfirmAction(t, fx, "toolu_fix_2", ActionProposed)
	confirmExec(t, fx, "UPDATE agent_actions SET decided_by = NULL, decided_at = NULL WHERE id = ?", proposalID)
	confirmExec(t, fx, "UPDATE agent_runs SET status = ?, finished_at = NULL, stop_reason = NULL WHERE id = ?",
		runStatusWaitingApproval, fx.runID)
	confirmExec(t, fx, "UPDATE issues SET status = ? WHERE id = ?", IssueAwaitingApproval, fx.issueID)

	if err := fx.svc.ReporterConfirmFix(context.Background(), fx.issueID, fx.reporterID); err != nil {
		t.Fatalf("ReporterConfirmFix: %v", err)
	}

	proposal, err := fx.svc.GetAction(proposalID)
	if err != nil {
		t.Fatalf("GetAction: %v", err)
	}
	if proposal.Status != ActionSuperseded || proposal.CanDecide {
		t.Fatalf("open proposal after confirmation = %q can_decide=%v, want superseded/false",
			proposal.Status, proposal.CanDecide)
	}
	// The fix that DID run keeps its history: superseding is for undecided work.
	applied, err := fx.svc.GetAction(fx.actionID)
	if err != nil {
		t.Fatalf("GetAction applied: %v", err)
	}
	if applied.Status != ActionExecuted {
		t.Fatalf("the applied fix was rewritten to %q", applied.Status)
	}
}

// A confirmation can arrive while an agent run is still live or parked. Every
// non-terminal run state is terminalized with the close, so nothing resumes
// against an issue that is already answered.
func TestReporterConfirmFixTerminalizesALiveAgentRun(t *testing.T) {
	for _, runStatus := range []string{
		runStatusRunning, runStatusWaitingUser, runStatusWaitingApproval, runStatusResumePending,
	} {
		t.Run(runStatus, func(t *testing.T) {
			fx := reporterConfirmFixture(t)
			confirmExec(t, fx,
				`UPDATE agent_runs SET status = ?, finished_at = NULL, stop_reason = NULL WHERE id = ?`,
				runStatus, fx.runID)

			if err := fx.svc.ReporterConfirmFix(context.Background(), fx.issueID, fx.reporterID); err != nil {
				t.Fatalf("ReporterConfirmFix: %v", err)
			}

			var status, stopReason string
			var finished bool
			if err := fx.svc.db.QueryRow(
				`SELECT status, COALESCE(stop_reason,''), finished_at IS NOT NULL
				 FROM agent_runs WHERE id = ?`,
				fx.runID,
			).Scan(&status, &stopReason, &finished); err != nil {
				t.Fatalf("load run: %v", err)
			}
			if status != "aborted" || stopReason != "external_resolution" {
				t.Fatalf("run after confirmation = %s/%s, want aborted/external_resolution", status, stopReason)
			}
			if !finished {
				t.Fatal("aborted run never got its finished_at stamp")
			}
			// The issue's claim is released with it.
			if confirmIssue(t, fx).ClosedAt == nil {
				t.Fatal("issue not closed alongside the aborted run")
			}
		})
	}
}

// Standing-rule accounting: "as long as issues keep closing out successfully"
// includes the reporter saying so. Without reporter_confirmed in the success
// branch this close falls through to the default arm and PAUSES the rule —
// punishing an automation for a fix that demonstrably worked.
//
// The auto_rule_id is stitched on directly because the auto-approval sweep only
// ever matches auto-detected issues, so a user report cannot acquire one through
// the normal path today. That makes the branch defensive rather than live, and
// this test is what keeps it correct if the sweep's scope ever widens.
func TestReporterConfirmFixCountsAsStandingRuleSuccess(t *testing.T) {
	fx := reporterConfirmFixture(t)
	ruleID, err := fx.svc.createOrReactivateApprovalRule(
		testAdminID, 0, "Wrong content imported", ActionDeleteMediaFiles, "")
	if err != nil {
		t.Fatalf("arm rule: %v", err)
	}
	confirmExec(t, fx,
		"UPDATE agent_actions SET auto_rule_id = ?, decided_by = NULL WHERE id = ?", ruleID, fx.actionID)

	if err := fx.svc.ReporterConfirmFix(context.Background(), fx.issueID, fx.reporterID); err != nil {
		t.Fatalf("ReporterConfirmFix: %v", err)
	}

	rule := ruleRow(t, fx.svc, ruleID)
	if rule.Status != ApprovalRuleActive {
		t.Fatalf("rule after a confirmed fix = %q (%v), want it left active", rule.Status, rule.PausedReason)
	}
	if rule.ResolvedCount != 1 || rule.LastResolvedAt == nil {
		t.Fatalf("rule counters = %d/%v, want the confirmation counted as one success",
			rule.ResolvedCount, rule.LastResolvedAt)
	}
	if n := countAdminEvents(fx.notifier, "agent_autoapproval_paused"); n != 0 {
		t.Fatalf("a confirmed fix notified %d rule pauses, want 0", n)
	}
}

// ---------------------------------------------------------------------------
// The HTTP surface: POST /api/issues/{id}/confirm-fixed and the Get enrichment
// ---------------------------------------------------------------------------

// postConfirmFixed drives the handler the way the router would: {id} in the chi
// route context, the caller's claims in the request context.
func postConfirmFixed(t *testing.T, h *Handler, id string, claims *auth.Claims) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/"+id+"/confirm-fixed", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	if claims != nil {
		ctx = context.WithValue(ctx, auth.ClaimsKey, claims)
	}
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ConfirmFixed(rec, req)
	return rec
}

func confirmFixedAs(t *testing.T, fx *confirmFixture, userID int64, role string) *httptest.ResponseRecorder {
	t.Helper()
	return postConfirmFixed(t, NewHandler(fx.svc), strconv.FormatInt(fx.issueID, 10),
		&auth.Claims{UserID: userID, Role: role})
}

func TestConfirmFixedEndpointClosesForTheReporter(t *testing.T) {
	fx := reporterConfirmFixture(t)
	rec := confirmFixedAs(t, fx, fx.reporterID, auth.RoleUser)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, body %s; want 200", rec.Code, rec.Body.String())
	}
	var body map[string]bool
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode confirm response: %v", err)
	}
	if !body["ok"] {
		t.Fatalf("confirm response = %+v, want ok", body)
	}
	issue := confirmIssue(t, fx)
	if issue.Status != IssueResolved || issue.ResolutionKind != ResolutionReporterConfirmed || issue.ClosedAt == nil {
		t.Fatalf("issue after the endpoint = %+v", issue)
	}
}

func TestConfirmFixedEndpointRefusesADifferentRequester(t *testing.T) {
	fx := reporterConfirmFixture(t)
	rec := confirmFixedAs(t, fx, fx.otherID, auth.RoleUser)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("bystander confirm status = %d, body %s; want 403", rec.Code, rec.Body.String())
	}
	if confirmIssue(t, fx).ClosedAt != nil {
		t.Fatal("a bystander closed someone else's issue")
	}
}

// An administrator who is not the reporter is refused DELIBERATELY, and not for
// lack of privilege: this endpoint is the one place where more authority is the
// wrong qualification. Admins have /api/admin/issues/{id}/resolve, which records
// their own name and their own required note. Letting the same admin through
// here would file their verdict as the reporter's — a lie in the audit trail
// about who watched the content and judged it right.
func TestConfirmFixedEndpointRefusesAnAdminWhoIsNotTheReporter(t *testing.T) {
	fx := reporterConfirmFixture(t)
	rec := confirmFixedAs(t, fx, testAdminID, auth.RoleAdmin)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("admin confirm status = %d, body %s; want 403", rec.Code, rec.Body.String())
	}
	issue := confirmIssue(t, fx)
	if issue.ClosedAt != nil || issue.ResolutionKind != "" {
		t.Fatalf("an admin closed a report as the reporter: %+v", issue)
	}
	if got := len(confirmThread(t, fx)); got != 0 {
		t.Fatalf("an admin's refused confirmation wrote %d messages as the reporter", got)
	}

	// The admin route is still open to them, and records the truth: their own
	// completion, under their own name.
	if _, err := fx.svc.ResolveIssueByAdmin(context.Background(), testAdminID, fx.issueID,
		AdminDispositionResolved, "Watched it myself; the right cut is in the library."); err != nil {
		t.Fatalf("ResolveIssueByAdmin: %v", err)
	}
	if kind := confirmIssue(t, fx).ResolutionKind; kind != ResolutionAdminCompleted {
		t.Fatalf("admin completion recorded as %q, want admin_completed", kind)
	}
}

func TestConfirmFixedEndpointStatusCodes(t *testing.T) {
	fx := reporterConfirmFixture(t)
	h := NewHandler(fx.svc)
	reporter := &auth.Claims{UserID: fx.reporterID, Role: auth.RoleUser}

	if rec := postConfirmFixed(t, h, strconv.FormatInt(fx.issueID, 10), nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want 401", rec.Code)
	}
	if rec := postConfirmFixed(t, h, "not-a-number", reporter); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed id status = %d, want 400", rec.Code)
	}
	if rec := postConfirmFixed(t, h, "987654", reporter); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown issue status = %d, want 404", rec.Code)
	}

	// A state the gate refuses is a conflict, not a permission problem: the
	// reporter may confirm this issue, just not while a fix is mid-dispatch.
	seedConfirmAction(t, fx, "toolu_fix_2", ActionExecuting)
	rec := postConfirmFixed(t, h, strconv.FormatInt(fx.issueID, 10), reporter)
	if rec.Code != http.StatusConflict {
		t.Fatalf("mid-dispatch confirm status = %d, body %s; want 409", rec.Code, rec.Body.String())
	}
	if confirmIssue(t, fx).ClosedAt != nil {
		t.Fatal("a 409 still closed the issue")
	}
}

// can_confirm_fixed is what makes the button appear, so it must answer the same
// question the endpoint does — for the same caller, in the same state.
func TestGetIssueExposesCanConfirmFixed(t *testing.T) {
	fx := reporterConfirmFixture(t)
	h := NewHandler(fx.svc)

	reporterView := getIssueDetail(t, h, fx.issueID, &auth.Claims{UserID: fx.reporterID, Role: auth.RoleUser})
	if !reporterView.Issue.CanConfirmFixed {
		t.Fatal("the reporter's own read did not offer the confirmation")
	}

	// An admin reading the same issue is offered nothing: the control is not
	// theirs, and the endpoint behind it would refuse them anyway.
	adminView := getIssueDetail(t, h, fx.issueID, &auth.Claims{UserID: testAdminID, Role: auth.RoleAdmin})
	if adminView.Issue.CanConfirmFixed {
		t.Fatal("an admin's read offered the reporter's confirmation")
	}

	// A state the gate refuses hides the control rather than showing one that
	// 409s when tapped.
	seedConfirmAction(t, fx, "toolu_fix_2", ActionExecuting)
	midDispatch := getIssueDetail(t, h, fx.issueID, &auth.Claims{UserID: fx.reporterID, Role: auth.RoleUser})
	if midDispatch.Issue.CanConfirmFixed {
		t.Fatal("the confirmation was offered while a fix was mid-dispatch")
	}
}

// The reporter inbox returns only the caller's own reports, and the
// requester-copy boundary guarantees a non-admin reader never sees admin-facing
// resolution diagnostics — executor text and give-up reasons are the right
// words for the admin queue and the wrong words for the person who reported a
// wrong episode.
func TestReporterInboxScopeAndCopyBoundary(t *testing.T) {
	svc, _, _ := setupTestService(t)
	if _, err := svc.db.Exec("INSERT INTO users (id, username, password_hash, role) VALUES (7, 'me', '', 'user'), (8, 'them', '', 'user')"); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	if _, err := svc.db.Exec(
		`INSERT INTO issues (source, status, category, reporter_id, media_type, tmdb_id, title, detail, resolution)
		 VALUES ('user', 'needs_admin', 'wrong_content', 7, 'movie', 1, 'Mine', 'wrong', 'Executor said: sonarr DELETE /api/v3/queue/41 returned 500'),
		        ('user', 'open', 'bad_copy', 8, 'movie', 2, 'Theirs', 'grainy', '')`,
	); err != nil {
		t.Fatalf("seed issues: %v", err)
	}

	mine, err := svc.ListIssuesForReporter(7)
	if err != nil {
		t.Fatalf("ListIssuesForReporter: %v", err)
	}
	if len(mine) != 1 || mine[0].Title != "Mine" {
		t.Fatalf("inbox = %+v, want exactly the caller's own report", mine)
	}

	applyRequesterCopy(&mine[0])
	if strings.Contains(mine[0].Resolution, "Executor") || strings.Contains(mine[0].Resolution, "sonarr") {
		t.Fatalf("requester copy leaked admin diagnostics: %q", mine[0].Resolution)
	}
	if mine[0].Resolution != "An administrator is taking a closer look at this." {
		t.Fatalf("needs_admin requester copy = %q", mine[0].Resolution)
	}

	confirm := Issue{Status: IssueAwaitingConfirmation}
	applyRequesterCopy(&confirm)
	if !strings.Contains(confirm.Resolution, "confirm") {
		t.Fatalf("awaiting_confirmation requester copy = %q", confirm.Resolution)
	}
}
