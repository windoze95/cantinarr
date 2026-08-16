package remediation

import (
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
)

func TestBookImportStallDedupesPerInstanceSystemIssueAndRecovers(t *testing.T) {
	service, notifier, reporterID := setupTestService(t)
	titles := []string{"The CEO Mindset"}
	if err := service.RecordBookImportStall("chaptarr-abc", "Books", titles, false); err != nil {
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
	// IssueWaiting, not IssueNeedsAdmin: the stall resolves itself, so clients
	// must present it as passive tracking rather than demand an admin verdict.
	if issue.Source != SourceSystem || issue.Status != IssueWaiting || issue.MediaType != "system" || issue.InstanceID != "chaptarr-abc" {
		t.Fatalf("system issue=%#v", issue)
	}
	if !strings.Contains(issue.Title, "Books") {
		t.Fatalf("issue title=%q, want the safe instance name", issue.Title)
	}
	// The list names book titles, never "the author": at park time the author
	// is exactly what Chaptarr doesn't have yet, so the copy must not present
	// a book title as an author name.
	if !strings.Contains(issue.Detail, `Waiting: "The CEO Mindset"`) {
		t.Fatalf("issue detail=%q, want the waiting title labeled as a waiting request", issue.Detail)
	}
	if strings.Contains(issue.Detail, "the author:") {
		t.Fatalf("issue detail=%q, must not label book titles as the author", issue.Detail)
	}
	if len(notifier.adminEvents) != 1 || notifier.adminEvents[0] != "issue_created" {
		t.Fatalf("admin events=%v", notifier.adminEvents)
	}
	if canAccessIssue(&auth.Claims{UserID: reporterID, Role: auth.RoleUser}, &issue) {
		t.Fatal("regular user could access a system health issue")
	}

	// A second stalled pass refreshes the same issue instead of opening another.
	if err := service.RecordBookImportStall("chaptarr-abc", "Books", titles, false); err != nil {
		t.Fatal(err)
	}
	issues, _, err = service.ListIssues("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].ID != issue.ID || issues[0].Occurrences != 2 {
		t.Fatalf("deduped issues=%#v", issues)
	}

	// A different instance stalls independently.
	if err := service.RecordBookImportStall("chaptarr-def", "Yana's Books", []string{"Another Book"}, false); err != nil {
		t.Fatal(err)
	}
	issues, _, err = service.ListIssues("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 2 {
		t.Fatalf("issues after second instance=%#v", issues)
	}

	// The first instance clears; only its issue resolves.
	if err := service.RecordBookImportStall("chaptarr-abc", "Books", nil, true); err != nil {
		t.Fatal(err)
	}
	recovered, err := service.GetIssue(issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != IssueResolved || recovered.ResolutionKind != ResolutionBookImportCleared || recovered.ClosedAt == nil {
		t.Fatalf("recovered issue=%#v", recovered)
	}
	// A healthy pass with no open issue is a no-op, not an error.
	if err := service.RecordBookImportStall("chaptarr-abc", "Books", nil, true); err != nil {
		t.Fatal(err)
	}
}

func TestBookImportStallBoundsTitleList(t *testing.T) {
	service, _, _ := setupTestService(t)
	titles := []string{"One", "Two", "Three", "Four", "Five", "Six", "Seven"}
	if err := service.RecordBookImportStall("chaptarr-abc", "Books", titles, false); err != nil {
		t.Fatal(err)
	}
	issues, _, err := service.ListIssues("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("issues=%#v", issues)
	}
	if !strings.Contains(issues[0].Detail, "and 2 more") {
		t.Fatalf("detail=%q, want the overflow collapsed into a count", issues[0].Detail)
	}
	if strings.Contains(issues[0].Detail, `"Six"`) {
		t.Fatalf("detail=%q, want at most %d named titles", issues[0].Detail, bookImportStallMaxTitles)
	}
}
