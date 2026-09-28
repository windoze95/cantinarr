package remediation

import (
	"context"
	"testing"
)

type recordedReportEvent struct {
	kind                         string
	issueID, actorID, occurrence int64
}
type reportRecorder struct{ events []recordedReportEvent }

func (r *reportRecorder) ReportChanged(kind string, id, actorID, occurrence int64) {
	r.events = append(r.events, recordedReportEvent{kind, id, actorID, occurrence})
}

func TestReportObserverTracksOnlyCommittedLifecycleChanges(t *testing.T) {
	s, _, id, _ := approvalFixture(t)
	r := &reportRecorder{}
	s.SetReportObserver(r)
	if err := s.PostReply(id, AuthorAdmin, testAdminID, "Private reply text"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveIssueByAdmin(context.Background(), testAdminID, id, AdminDispositionResolved, "Private resolution"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReopenIssueByAdmin(context.Background(), testAdminID, id); err != nil {
		t.Fatal(err)
	}
	if err := s.DismissIssue(id); err != nil {
		t.Fatal(err)
	}
	if err := s.PostReply(id, AuthorAdmin, testAdminID, "Rejected reply"); err == nil {
		t.Fatal("closed issue accepted reply")
	}
	if len(r.events) != 3 {
		t.Fatalf("events: %+v", r.events)
	}
	for i, kind := range []string{"issue_comment", "issue_resolved", "issue_reopened"} {
		if r.events[i].kind != kind || r.events[i].actorID != testAdminID || r.events[i].occurrence <= 0 {
			t.Fatalf("event: %+v", r.events[i])
		}
	}
	if r.events[0].occurrence >= r.events[2].occurrence {
		t.Fatal("reopen occurrence was reused")
	}
}
