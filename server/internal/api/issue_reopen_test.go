package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

func TestIssueReopenRouteAndCapabilityAreAdminOnly(t *testing.T) {
	h := newRBACRouterHarness(t, false)
	id := seedConfirmableIssue(t, h)
	if _, err := h.database.Exec("UPDATE issues SET status = 'resolved', closed_at = CURRENT_TIMESTAMP WHERE id = ?", id); err != nil {
		t.Fatal(err)
	}
	readPath := "/api/issues/" + strconv.FormatInt(id, 10)
	path := "/api/admin/issues/" + strconv.FormatInt(id, 10) + "/reopen"
	for _, tc := range []struct {
		name, token string
		want        int
		canReopen   bool
	}{
		{"anonymous", "", http.StatusUnauthorized, false},
		{"reporter", h.requesterToken, http.StatusForbidden, false},
		{"admin", h.adminToken, http.StatusOK, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.token != "" {
				rec := serveRBACRequest(h.router, http.MethodGet, readPath, tc.token)
				var payload struct {
					Issue struct {
						CanReopen bool `json:"can_reopen"`
					} `json:"issue"`
				}
				if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &payload) != nil || payload.Issue.CanReopen != tc.canReopen {
					t.Fatalf("read capability: %d %s", rec.Code, rec.Body.String())
				}
			}
			rec := serveRBACRequest(h.router, http.MethodPost, path, tc.token)
			if rec.Code != tc.want {
				t.Fatalf("reopen=%d want=%d body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
	var status string
	var closed bool
	if err := h.database.QueryRow("SELECT status, closed_at IS NOT NULL FROM issues WHERE id = ?", id).Scan(&status, &closed); err != nil || status != "needs_admin" || closed {
		t.Fatalf("reopened state=%s closed=%v err=%v", status, closed, err)
	}
	rec := serveRBACRequest(h.router, http.MethodPost, path, h.adminToken)
	if rec.Code != http.StatusConflict {
		t.Fatalf("double reopen=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = serveRBACRequest(h.router, http.MethodPost, "/api/admin/issues/999999/reopen", h.adminToken)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing reopen=%d body=%s", rec.Code, rec.Body.String())
	}
	// The previous executed fix must not reactivate reporter confirmation.
	rec = serveRBACRequest(h.router, http.MethodGet, readPath, h.requesterToken)
	var payload struct {
		Issue struct {
			CanReopen       bool `json:"can_reopen"`
			CanConfirmFixed bool `json:"can_confirm_fixed"`
		} `json:"issue"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil || payload.Issue.CanReopen || payload.Issue.CanConfirmFixed {
		t.Fatalf("reopened reporter capabilities=%s err=%v", rec.Body.String(), err)
	}
}
