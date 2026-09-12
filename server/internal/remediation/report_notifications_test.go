package remediation

import (
	"context"
	"testing"
)

func TestReporterSuccessEventOnlyAfterSuccessfulCommittedClose(t *testing.T) {
	for _, disposition := range []AdminIssueDisposition{AdminDispositionResolved, AdminDispositionWontFix} {
		t.Run(string(disposition), func(t *testing.T) {
			f := reporterConfirmFixture(t)
			if _, err := f.svc.ResolveIssueByAdmin(context.Background(), testAdminID, f.issueID, disposition, "Checked the media server."); err != nil {
				t.Fatal(err)
			}
			want := 0
			if disposition == AdminDispositionResolved {
				want = 1
			}
			if got := countEvents(f.notifier.userEvents, "issue_closed"); got != want {
				t.Fatalf("success events: %d, want %d", got, want)
			}
		})
	}
	t.Run("dismissal", func(t *testing.T) {
		f := reporterConfirmFixture(t)
		if err := f.svc.DismissIssue(f.issueID); err != nil {
			t.Fatal(err)
		}
		if countEvents(f.notifier.userEvents, "issue_closed") != 0 {
			t.Fatal("dismissal announced success")
		}
	})
	t.Run("rollback", func(t *testing.T) {
		f := reporterConfirmFixture(t)
		if _, err := f.svc.db.Exec(`CREATE TRIGGER refuse_close BEFORE UPDATE OF closed_at ON issues BEGIN SELECT RAISE(ABORT,'test rollback'); END`); err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.ResolveIssueByAdmin(context.Background(), testAdminID, f.issueID, AdminDispositionResolved, "Checked the media server."); err == nil {
			t.Fatal("close succeeded despite rollback")
		}
		if countEvents(f.notifier.userEvents, "issue_closed") != 0 {
			t.Fatal("rolled-back close announced success")
		}
	})
}
