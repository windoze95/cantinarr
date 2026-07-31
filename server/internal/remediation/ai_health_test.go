package remediation

import (
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
)

func TestSharedAIHealthFailureDedupesAdminOnlySystemIssueAndRecovers(t *testing.T) {
	service, notifier, reporterID := setupTestService(t)
	if err := service.RecordSharedAIHealth("codex", "gpt-5.6-luna", false); err != nil {
		t.Fatal(err)
	}
	issues, err := service.ListIssues("")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("issues=%#v", issues)
	}
	issue := issues[0]
	if issue.Source != SourceSystem || issue.Status != IssueNeedsAdmin || issue.MediaType != "system" || issue.ReporterID != nil || issue.InstanceID != "" || issue.TmdbID != 0 {
		t.Fatalf("system issue=%#v", issue)
	}
	if !strings.Contains(issue.Detail, `provider "codex"`) || !strings.Contains(issue.Detail, `model "gpt-5.6-luna"`) {
		t.Fatalf("issue detail=%q", issue.Detail)
	}
	if len(notifier.adminEvents) != 1 || notifier.adminEvents[0] != "issue_created" {
		t.Fatalf("admin events=%v", notifier.adminEvents)
	}
	if canAccessIssue(&auth.Claims{UserID: reporterID, Role: auth.RoleUser}, &issue) {
		t.Fatal("regular user could access a system health issue")
	}
	if !canAccessIssue(&auth.Claims{UserID: 999, Role: auth.RoleAdmin}, &issue) {
		t.Fatal("admin could not access a system health issue")
	}

	if err := service.RecordSharedAIHealth("codex", "gpt-5.6-luna", false); err != nil {
		t.Fatal(err)
	}
	issues, err = service.ListIssues("")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].ID != issue.ID || issues[0].Occurrences != 2 {
		t.Fatalf("deduped issues=%#v", issues)
	}

	if err := service.RecordSharedAIHealth("codex", "gpt-5.6-luna", true); err != nil {
		t.Fatal(err)
	}
	recovered, err := service.GetIssue(issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != IssueResolved || recovered.ResolutionKind != ResolutionAIHealthRestored || recovered.ClosedAt == nil {
		t.Fatalf("recovered issue=%#v", recovered)
	}
	thread, err := service.IssueThread(issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(thread) != 2 || thread[0].AuthorKind != AuthorSystem || thread[1].AuthorKind != AuthorSystem {
		t.Fatalf("health thread=%#v", thread)
	}
}

func TestSharedAIHealthBoundsInvalidProviderMetadata(t *testing.T) {
	service, _, _ := setupTestService(t)
	provider := strings.Repeat("p", 300) + string([]byte{0xff})
	model := strings.Repeat("🌙", 100)
	if err := service.RecordSharedAIHealth(provider, model, false); err != nil {
		t.Fatal(err)
	}
	issues, err := service.ListIssues("")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || !strings.Contains(issues[0].Detail, `provider "`) {
		t.Fatalf("issues=%#v", issues)
	}
}
