package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

// The reporter's "This is fixed" control is only worth anything if it reaches a
// real route. This drives the fully wired router with real tokens: the reporter's
// own request closes their issue, an administrator's identical request does not,
// and an anonymous one never gets past auth.
//
// The admin 403 is deliberate rather than an oversight. Admins have
// /api/admin/issues/{id}/resolve, which records their own name and their own
// required note; letting them through here would file their verdict as the
// reporter's — a lie in the audit trail about who watched the content.
func TestIssueConfirmFixedRouteIsReporterOnly(t *testing.T) {
	harness := newRBACRouterHarness(t, false)
	issueID := seedConfirmableIssue(t, harness)
	path := "/api/issues/" + strconv.FormatInt(issueID, 10) + "/confirm-fixed"

	if rec := serveRBACRequest(harness.router, http.MethodPost, path, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous confirm status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
	if rec := serveRBACRequest(harness.router, http.MethodPost, path, harness.adminToken); rec.Code != http.StatusForbidden {
		t.Fatalf("admin confirm status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	var closed bool
	if err := harness.database.QueryRow(
		"SELECT closed_at IS NOT NULL FROM issues WHERE id = ?", issueID,
	).Scan(&closed); err != nil {
		t.Fatalf("load issue after refused confirmations: %v", err)
	}
	if closed {
		t.Fatal("a refused confirmation still closed the issue")
	}

	rec := serveRBACRequest(harness.router, http.MethodPost, path, harness.requesterToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("reporter confirm status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var status, kind string
	if err := harness.database.QueryRow(
		"SELECT status, resolution_kind, closed_at IS NOT NULL FROM issues WHERE id = ?", issueID,
	).Scan(&status, &kind, &closed); err != nil {
		t.Fatalf("load confirmed issue: %v", err)
	}
	if status != "resolved" || kind != "reporter_confirmed" || !closed {
		t.Fatalf("confirmed issue = %s/%s closed=%v, want resolved/reporter_confirmed/true", status, kind, closed)
	}
}

// The control's visibility rides on the single-issue read, so the flag has to
// survive the wire under the name the client looks for — and it has to answer
// per caller, not per issue.
func TestIssueReadCarriesCanConfirmFixedPerCaller(t *testing.T) {
	harness := newRBACRouterHarness(t, false)
	issueID := seedConfirmableIssue(t, harness)
	path := "/api/issues/" + strconv.FormatInt(issueID, 10)

	readFlag := func(token string) any {
		t.Helper()
		rec := serveRBACRequest(harness.router, http.MethodGet, path, token)
		if rec.Code != http.StatusOK {
			t.Fatalf("issue read status = %d; body=%s", rec.Code, rec.Body.String())
		}
		var payload struct {
			Issue map[string]any `json:"issue"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
			t.Fatalf("decode issue detail: %v", err)
		}
		flag, present := payload.Issue["can_confirm_fixed"]
		if !present {
			t.Fatalf("issue payload has no can_confirm_fixed key: %+v", payload.Issue)
		}
		return flag
	}

	if flag := readFlag(harness.requesterToken); flag != true {
		t.Fatalf("reporter's own read can_confirm_fixed = %v, want true", flag)
	}
	if flag := readFlag(harness.adminToken); flag != false {
		t.Fatalf("admin read can_confirm_fixed = %v, want false", flag)
	}
}

// seedConfirmableIssue writes the state the reporter's confirmation answers: the
// requester's own report, still open at needs_admin, with an approved fix that
// actually reached the arr.
func seedConfirmableIssue(t *testing.T, harness *rbacRouterHarness) int64 {
	t.Helper()
	res, err := harness.database.Exec(
		`INSERT INTO issues (source, status, category, media_type, tmdb_id, title, detail, instance_id, reporter_id)
		 VALUES ('user', 'needs_admin', 'wrong_content', 'movie', 42, 'Test Movie',
		         'this is the wrong cut of the film', 'radarr-main', ?)`,
		harness.requesterID,
	)
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issueID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed issue id: %v", err)
	}
	if _, err := harness.database.Exec(
		`INSERT INTO agent_actions (issue_id, kind, params, rationale, status, fingerprint, executed_at, result_text)
		 VALUES (?, 'delete_media_files', '{"media_type":"movie","tmdb_id":42}', 'wrong cut imported',
		         'executed', 'fp-reporter-confirm-fixed', CURRENT_TIMESTAMP, 'Deleted 1 file and searched again.')`,
		issueID,
	); err != nil {
		t.Fatalf("seed executed fix: %v", err)
	}
	return issueID
}
