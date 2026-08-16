package remediation

import (
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
)

func TestPushDeliveryHealthDedupesAdminOnlySystemIssueAndRecovers(t *testing.T) {
	service, notifier, reporterID := setupTestService(t)

	if err := service.RecordPushDeliveryHealth(false, "2 push notifications in a row failed to reach the gateway."); err != nil {
		t.Fatal(err)
	}
	issues, _, err := service.ListIssues("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("issues=%#v", issues)
	}
	issue := issues[0]
	if issue.Source != SourceSystem || issue.Status != IssueNeedsAdmin || issue.MediaType != "system" ||
		issue.ReporterID != nil || issue.InstanceID != "" || issue.TmdbID != 0 {
		t.Fatalf("system issue=%#v", issue)
	}
	if !strings.Contains(issue.Detail, "failed to reach the gateway") {
		t.Fatalf("issue detail=%q", issue.Detail)
	}

	// A push outage is admin business: a requester must not see it, and it must
	// not leak into anyone's issue list.
	if canAccessIssue(&auth.Claims{UserID: reporterID, Role: auth.RoleUser}, &issue) {
		t.Fatal("regular user could access a system health issue")
	}
	if !canAccessIssue(&auth.Claims{UserID: 999, Role: auth.RoleAdmin}, &issue) {
		t.Fatal("admin could not access a system health issue")
	}

	// Every further failed send refreshes the one issue rather than opening
	// another, and counts what the outage swallowed.
	if err := service.RecordPushDeliveryHealth(false, "5 push notifications in a row failed to reach the gateway."); err != nil {
		t.Fatal(err)
	}
	issues, _, err = service.ListIssues("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("a second failure opened another issue: %#v", issues)
	}
	if issues[0].Occurrences != 2 {
		t.Errorf("occurrences = %d, want 2", issues[0].Occurrences)
	}
	// A refresh pings the open issue over the WebSocket but must not raise a
	// second alert; one outage is one thing to be told about.
	created := 0
	for _, event := range notifier.adminEvents {
		if event == "issue_created" {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("admin events=%v, want exactly one issue_created", notifier.adminEvents)
	}

	// One successful send closes it.
	if err := service.RecordPushDeliveryHealth(true, ""); err != nil {
		t.Fatal(err)
	}
	issues, _, err = service.ListIssues("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Status != IssueResolved {
		t.Fatalf("issue after recovery=%#v", issues)
	}
	if issues[0].ResolutionKind != ResolutionPushDeliveryRestored {
		t.Errorf("resolution kind = %q, want %q", issues[0].ResolutionKind, ResolutionPushDeliveryRestored)
	}
	// The admin has to be told that alerts were lost, not just that push works
	// again — nothing sent during the outage is retried.
	if !strings.Contains(issues[0].Resolution, "not retried") {
		t.Errorf("resolution = %q, want it to say the lost alerts are not coming", issues[0].Resolution)
	}
}

// TestPushDeliveryHealthSuccessWithoutAnIssueIsFree pins the cheap path: the
// Notifier reports every success so a restart mid-outage cannot strand an open
// issue, which means this runs per notification and must do nothing when there
// is nothing open.
func TestPushDeliveryHealthSuccessWithoutAnIssueIsFree(t *testing.T) {
	service, notifier, _ := setupTestService(t)

	if err := service.RecordPushDeliveryHealth(true, ""); err != nil {
		t.Fatal(err)
	}
	issues, _, err := service.ListIssues("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("a healthy send created issues: %#v", issues)
	}
	if len(notifier.adminEvents) != 0 {
		t.Fatalf("a healthy send notified admins: %v", notifier.adminEvents)
	}
}
