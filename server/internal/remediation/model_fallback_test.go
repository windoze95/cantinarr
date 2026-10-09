package remediation

import (
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/auth"
)

func TestModelFallbackNotificationDurableDedupeAndSettingsLink(t *testing.T) {
	svc, notifier, userID := setupTestService(t)
	event := ai.ModelFallback{EventKey: strings.Repeat("a", 64), Source: "shared", Scope: "remediation_override", Provider: "openai", SelectedModel: "retired-model", ReplacementModel: "gpt-4.1-mini", Reason: "The provider reported the selected model unavailable.", RecommendationSource: "Cantinarr recommendation", Differences: "No reasoning controls; pricing differences are not available.", SettingsPath: "/settings/credentials", Succeeded: true}
	for i := 0; i < 3; i++ {
		if err := svc.RecordModelFallback(event); err != nil {
			t.Fatal(err)
		}
	}
	issues, _, err := svc.ListIssues("", 0)
	if err != nil || len(issues) != 1 {
		t.Fatalf("issues=%+v err=%v", issues, err)
	}
	issue := issues[0]
	for _, want := range []string{"retired-model", "gpt-4.1-mini", "unavailable", "Cantinarr recommendation", "pricing differences"} {
		if !strings.Contains(issue.Detail, want) {
			t.Fatalf("missing %s in %s", want, issue.Detail)
		}
	}
	if issue.SettingsPath != "/settings/credentials" || !strings.Contains(issue.Resolution, "/settings/credentials") {
		t.Fatalf("settings link=%+v", issue)
	}
	if canAccessIssue(&auth.Claims{UserID: userID, Role: auth.RoleUser}, &issue) {
		t.Fatal("regular user can see admin notification")
	}
	if len(notifier.adminEvents) != 1 {
		t.Fatalf("events=%v", notifier.adminEvents)
	}
	if err := svc.DismissIssue(issue.ID); err != nil {
		t.Fatal(err)
	}
	before := len(notifier.adminEvents)
	// A new service value over the same DB models a process restart.
	restarted := NewService(svc.db, nil, nil, notifier)
	if err := restarted.RecordModelFallback(event); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM issues").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 || len(notifier.adminEvents) != before {
		t.Fatal("closed event notified again")
	}
	event.EventKey = strings.Repeat("b", 64)
	event.Source = "personal"
	event.UserID = userID
	event.SettingsPath = "/settings/ai"
	event.Succeeded = false
	if err := svc.RecordModelFallback(event); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := svc.db.QueryRow("SELECT MAX(id) FROM issues").Scan(&id); err != nil {
		t.Fatal(err)
	}
	personal, err := svc.GetIssue(id)
	if err != nil {
		t.Fatal(err)
	}
	if personal.SettingsPath != "/settings/ai" || !strings.Contains(personal.Detail, "also failed") || !strings.Contains(personal.Resolution, "own account") {
		t.Fatalf("personal=%+v", personal)
	}
}
