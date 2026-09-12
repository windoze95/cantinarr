package push

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/db"
)

func TestPolicyAndPersonalMastersFilterEveryCategory(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, database, `INSERT INTO users(id,username,password_hash,role) VALUES
		(1,'admin','','admin'),(2,'user','','user'),(3,'muted','','admin'),(4,'category-muted','','admin')`)
	store := NewPrefsStore(database)
	for category, col := range categoryColumn {
		t.Run(category, func(t *testing.T) {
			mustExec(t, database, `DELETE FROM notification_prefs`)
			for id := 1; id <= 4; id++ {
				mustExec(t, database, `INSERT INTO notification_prefs(user_id) VALUES(?)`, id)
			}
			mustExec(t, database, `UPDATE notification_prefs SET `+col.column+`=1`)
			mustExec(t, database, `UPDATE notification_prefs SET push_enabled=0 WHERE user_id=3`)
			mustExec(t, database, `UPDATE notification_prefs SET `+col.column+`=0 WHERE user_id=4`)
			want := []int64{1, 2}
			if adminCategory(category) {
				want = []int64{1}
			}
			check := func(want []int64) {
				t.Helper()
				got, err := store.filterDelivery([]int64{1, 2, 3, 4, 999}, category)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("got %v, %v; want %v", got, err, want)
				}
			}
			check(want)
			if err := store.setPolicy(nil, map[string]bool{col.column: false}); err != nil {
				t.Fatal(err)
			}
			check(nil)
			if err := store.setPolicy(nil, map[string]bool{col.column: true}); err != nil {
				t.Fatal(err)
			}
			off := false
			if err := store.setPolicy(&off, nil); err != nil {
				t.Fatal(err)
			}
			check(nil)
			on := true
			if err := store.setPolicy(&on, nil); err != nil {
				t.Fatal(err)
			}
			check(want) // neither master rewrites an individual's saved choices.
		})
	}
	for _, event := range []string{EventIssueQuestion, EventIssueFixConfirm, EventIssueClosed, EventAutoDispatchDisabled} {
		if _, ok := categoryColumn[preferenceCategory(event)]; !ok {
			t.Fatalf("unmapped event %s", event)
		}
	}
	if _, err := store.filterDelivery([]int64{1}, "unknown"); err == nil {
		t.Fatal("unknown event allowed")
	}
	for _, invalid := range []string{`broken`, `null`, `{"categories":null}`, `{"enabled":"yes"}`} {
		mustExec(t, database, `UPDATE settings SET value=? WHERE key=?`, invalid, policyKey)
		if _, err := store.filterDelivery([]int64{1}, CategoryNewMovie); err == nil {
			t.Fatalf("unreadable policy allowed: %s", invalid)
		}
	}
}

func TestPreferencesPreserveNewFieldsAndCannotChangeServerPolicy(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, database, `INSERT INTO users(id,username,password_hash) VALUES(7,'alice','')`)
	h := NewHandler(database, nil, nil)
	for _, body := range []string{
		`{"push_enabled":false,"request_auto_approved":true,"new_movie":true}`,
		`{"new_episode":true,"server_policy":{"enabled":false,"categories":{"new_movie":false}}}`,
	} {
		w := httptest.NewRecorder()
		h.UpdatePreferences(w, withUser(httptest.NewRequest("PUT", "/", strings.NewReader(body)), 7))
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var got preferencesResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.PushEnabled || !got.RequestAutoApproved || !got.ServerPolicy.Enabled || !got.ServerPolicy.Categories[CategoryNewMovie] {
			t.Fatalf("wrong preferences: %+v", got)
		}
	}
	// Explicit false for the new category also persists through a legacy save.
	for _, body := range []string{`{"push_enabled":true,"request_auto_approved":false}`, `{"new_movie":true}`} {
		w := httptest.NewRecorder()
		h.UpdatePreferences(w, withUser(httptest.NewRequest("PUT", "/", strings.NewReader(body)), 7))
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	p, err := h.prefs.Get(7)
	if err != nil || !p.PushEnabled || p.RequestAutoApproved {
		t.Fatalf("prefs %+v, %v", p, err)
	}
}

func TestTestPushHonorsBothMastersWithoutGatewayTraffic(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, database, `INSERT INTO users(id,username,password_hash) VALUES(7,'alice','')`)
	mgr, capture := newNotifierTestGateway(t, database)
	h := NewHandler(database, mgr, nil)
	off := false
	if err := h.prefs.setPolicy(&off, nil); err != nil {
		t.Fatal(err)
	}
	for _, personal := range []bool{false, true} {
		if personal {
			on := true
			if err := h.prefs.setPolicy(&on, nil); err != nil {
				t.Fatal(err)
			}
			mustExec(t, database, `INSERT INTO notification_prefs(user_id,push_enabled) VALUES(7,0)`)
		}
		w := httptest.NewRecorder()
		h.TestPush(w, withUser(httptest.NewRequest("POST", "/", nil), 7))
		if w.Code != 409 {
			t.Fatalf("self test: %d %s", w.Code, w.Body.String())
		}
		w = httptest.NewRecorder()
		h.runTestPush(w, httptest.NewRequest("POST", "/", nil), 7)
		if w.Code != 409 {
			t.Fatalf("admin test: %d %s", w.Code, w.Body.String())
		}
	}
	select {
	case got := <-capture.ch:
		t.Fatalf("muted test sent: %v", got)
	default:
	}
}

func TestAutomaticRequestPushIsOptInAndDoesNotRequestReview(t *testing.T) {
	for _, kind := range []string{"movie", "tv", "book", "music"} {
		t.Run(kind, func(t *testing.T) {
			database, err := dbOpen(t)
			if err != nil {
				t.Fatal(err)
			}
			mustExec(t, database, `INSERT INTO users(id,username,password_hash,role) VALUES
				(1,'admin','','admin'),(2,'requester','','user'),(3,'muted-admin','','admin')`)
			mustExec(t, database, `INSERT INTO request_log(id,user_id,media_type,tmdb_id,title,foreign_id,book_format)
				VALUES(1,2,?,550,'A title','catalog-id','both')`, kind)
			mgr, capture := newNotifierTestGateway(t, database)
			n := NewNotifier(database, mgr, nil)
			n.RequestCreated(1, false)
			select {
			case <-capture.ch:
				t.Fatal("default sent")
			default:
			}
			mustExec(t, database, `INSERT INTO notification_prefs(user_id,request_auto_approved,push_enabled) VALUES(1,1,1),(2,1,1),(3,1,0)`)
			n.RequestCreated(1, true) // the actionable path owns this event instead.
			n.RequestCreated(1, false)
			got := capture.waitForNotification(t)
			if !reflect.DeepEqual(userIDsOf(t, got), []string{"1"}) {
				t.Fatalf("audience %v", got)
			}
			message := got["notification"].(map[string]any)
			if message["title"] != "Request automatically approved" || message["body"] != "requester requested A title" {
				t.Fatalf("message %v", got)
			}
			data := got["data"].(map[string]any)
			if data["type"] != CategoryRequestAutoApproved || data["media_type"] != kind || data["foreign_id"] != "catalog-id" {
				t.Fatalf("identity %v", data)
			}
			if got["options"].(map[string]any)["badge"] != nil {
				t.Fatalf("unexpected approval badge: %v", got)
			}
			select {
			case extra := <-capture.ch:
				t.Fatalf("extra %v", extra)
			case <-time.After(20 * time.Millisecond):
			}
		})
	}
}

func TestMutedNotificationsDoNotReplayOrReportFailures(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, database, `INSERT INTO users(id,username,password_hash) VALUES(1,'alice','')`)
	mgr, capture := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)
	off := false
	if err := n.prefs.setPolicy(&off, nil); err != nil {
		t.Fatal(err)
	}
	if !n.ContentReady() {
		t.Fatal("muting must not pause library observation")
	}
	n.NotifyNewMovie("Muted movie", 550, "")
	on := true
	if err := n.prefs.setPolicy(&on, nil); err != nil {
		t.Fatal(err)
	}
	n.NotifyNewMovie("Muted movie", 550, "") // same import, still witnessed/deduped.
	n.NotifyNewMovie("Next movie", 551, "")
	got := capture.waitForNotification(t)
	if got["notification"].(map[string]any)["body"] != "Next movie is ready to watch" {
		t.Fatalf("replayed muted alert: %v", got)
	}
	n.healthMu.Lock()
	fails := n.consecutiveFails
	n.healthMu.Unlock()
	if fails != 0 {
		t.Fatal("suppression counted as delivery failure")
	}
	select {
	case extra := <-capture.ch:
		t.Fatalf("extra %v", extra)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestNotificationControlMigrationPreservesChoices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, database, `INSERT INTO users(id,username,password_hash) VALUES(1,'alice','')`)
	mustExec(t, database, `INSERT INTO notification_prefs(user_id,request_pending,new_movie) VALUES(1,0,0)`)
	mustExec(t, database, `ALTER TABLE notification_prefs DROP COLUMN push_enabled`)
	mustExec(t, database, `ALTER TABLE notification_prefs DROP COLUMN request_auto_approved`)
	database.Close()
	database, err = db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	p, err := NewPrefsStore(database).Get(1)
	if err != nil || !p.PushEnabled || p.RequestAutoApproved || p.RequestPending || p.NewMovie {
		t.Fatalf("migrated %+v %v", p, err)
	}
}
