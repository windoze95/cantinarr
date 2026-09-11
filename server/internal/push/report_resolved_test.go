package push

import (
	"reflect"
	"testing"
	"time"
)

func TestReadyToTryAgainRequiresSuccessfulOwnReport(t *testing.T) {
	for _, tc := range []struct {
		name, source, status, kind, event string
		closed                            bool
		recipient                         int64
		want                              bool
	}{
		{"success", "user", "resolved", "admin_completed", EventIssueClosed, true, 7, true},
		{"rejected", "user", "wont_fix", "admin_completed", EventIssueClosed, true, 7, false},
		{"dismissed", "user", "dismissed", "admin_dismissed", EventIssueClosed, true, 7, false},
		{"failed", "user", "failed", "", EventIssueClosed, true, 7, false},
		{"unfinished", "user", "needs_admin", "", EventIssueClosed, false, 7, false},
		{"no close", "user", "resolved", "", EventIssueClosed, false, 7, false},
		{"another person's report", "user", "resolved", "admin_completed", EventIssueClosed, true, 8, false},
		{"automatic incident", "auto", "resolved", "arr_state_cleared", EventIssueClosed, true, 7, false},
		{"system incident", "system", "resolved", "push_delivery_restored", EventIssueClosed, true, 7, false},
		{"self confirmed", "user", "resolved", "reporter_confirmed", EventIssueClosed, true, 7, false},
		{"question", "user", "awaiting_user", "", EventIssueQuestion, false, 7, false},
		{"repair applied", "user", "needs_admin", "", EventIssueFixConfirm, false, 7, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database, err := dbOpen(t)
			if err != nil {
				t.Fatal(err)
			}
			mustExec(t, database, "INSERT INTO users(id,username,password_hash) VALUES(7,'reporter',''),(8,'bystander','')")
			mustExec(t, database, `INSERT INTO issues(id,source,status,resolution_kind,reporter_id,tmdb_id,media_type,closed_at)
				VALUES(12,?,?,?,7,550,'movie',CASE WHEN ? THEN CURRENT_TIMESTAMP ELSE NULL END)`, tc.source, tc.status, tc.kind, tc.closed)
			manager, capture := newNotifierTestGateway(t, database)
			n := NewNotifier(database, manager, nil)
			// The event's claimed status and private text cannot prove success.
			n.NotifyUser(tc.recipient, tc.event, map[string]interface{}{"issue_id": int64(12), "status": "resolved", "title": "PRIVATE TITLE", "resolution": "PRIVATE DIAGNOSTICS"})
			if !tc.want {
				select {
				case got := <-capture.ch:
					t.Fatalf("unexpected push: %+v", got)
				case <-time.After(30 * time.Millisecond):
				}
				return
			}
			got := capture.waitForNotification(t)
			if !reflect.DeepEqual(userIDsOf(t, got), []string{"7"}) {
				t.Fatalf("wrong audience: %+v", got)
			}
			if !reflect.DeepEqual(got["notification"], map[string]any{"title": "Ready to try again", "body": "The problem you reported has been resolved. Give it another try."}) {
				t.Fatalf("unexpected message: %+v", got)
			}
			if !reflect.DeepEqual(got["data"], map[string]any{"type": EventIssueClosed, "issue_id": float64(12)}) {
				t.Fatalf("unsafe or missing report link: %+v", got)
			}
		})
	}
}

func TestProblemResolvedRespectsEveryNotificationSwitch(t *testing.T) {
	for _, mode := range []string{"personal category", "server category", "personal master", "server master"} {
		t.Run(mode, func(t *testing.T) {
			database, err := dbOpen(t)
			if err != nil {
				t.Fatal(err)
			}
			mustExec(t, database, "INSERT INTO users(id,username,password_hash) VALUES(7,'reporter','')")
			mustExec(t, database, `INSERT INTO issues(id,source,status,reporter_id,tmdb_id,media_type,closed_at) VALUES(12,'user','resolved',7,1,'movie',CURRENT_TIMESTAMP)`)
			store := NewPrefsStore(database)
			switch mode {
			case "personal category":
				mustExec(t, database, `INSERT INTO notification_prefs(user_id,issue_report_update) VALUES(7,0)`)
			case "personal master":
				mustExec(t, database, `INSERT INTO notification_prefs(user_id,push_enabled) VALUES(7,0)`)
			case "server category":
				if err := store.setPolicy(nil, map[string]bool{CategoryIssueReportUpdate: false}); err != nil {
					t.Fatal(err)
				}
			case "server master":
				off := false
				if err := store.setPolicy(&off, nil); err != nil {
					t.Fatal(err)
				}
			}
			manager, capture := newNotifierTestGateway(t, database)
			NewNotifier(database, manager, nil).NotifyUser(7, EventIssueClosed, map[string]interface{}{"issue_id": 12})
			select {
			case got := <-capture.ch:
				t.Fatalf("muted push: %+v", got)
			case <-time.After(30 * time.Millisecond):
			}
		})
	}
}

func TestRepairQuestionsAndReviewsNotifyOnlyOptedInAdmins(t *testing.T) {
	for _, event := range []string{EventIssueQuestion, EventIssueFixConfirm} {
		t.Run(event, func(t *testing.T) {
			database, err := dbOpen(t)
			if err != nil {
				t.Fatal(err)
			}
			mustExec(t, database, `INSERT INTO users(id,username,password_hash,role) VALUES (1,'admin','','admin'),(2,'viewer','','user'),(3,'muted','','admin'),(4,'off','','admin')`)
			mustExec(t, database, `INSERT INTO notification_prefs(user_id,issue_created,agent_action_pending,push_enabled) VALUES(3,0,0,1),(4,1,1,0)`)
			manager, capture := newNotifierTestGateway(t, database)
			n := NewNotifier(database, manager, nil)
			n.NotifyAdmins(event, map[string]interface{}{"issue_id": 12})
			got := capture.waitForNotification(t)
			if !reflect.DeepEqual(userIDsOf(t, got), []string{"1"}) {
				t.Fatalf("admin audience: %+v", got)
			}
			wantTitle := "Report needs information"
			if event == EventIssueFixConfirm {
				wantTitle = "Repair needs review"
			}
			if got["notification"].(map[string]any)["title"] != wantTitle {
				t.Fatalf("wrong admin copy: %+v", got)
			}
			if err := NewPrefsStore(database).setPolicy(nil, map[string]bool{preferenceCategory(event): false}); err != nil {
				t.Fatal(err)
			}
			n.NotifyAdmins(event, map[string]interface{}{"issue_id": 13})
			select {
			case extra := <-capture.ch:
				t.Fatalf("server-muted admin alert: %+v", extra)
			case <-time.After(30 * time.Millisecond):
			}
		})
	}
}
