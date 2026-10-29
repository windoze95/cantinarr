package push

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/contentnotify"
)

type requestMatcherFunc func(context.Context, contentnotify.Import, []int64) ([]int64, error)

func (f requestMatcherFunc) RequestedContentRecipients(ctx context.Context, event contentnotify.Import, users []int64) ([]int64, error) {
	return f(ctx, event, users)
}

func TestRequesterOnlyPreferencesSurviveOldClients(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, database, `INSERT INTO users(id,username,password_hash,role) VALUES(1,'one','','user')`)
	h := NewHandler(database, nil, nil)
	for _, tc := range []struct {
		body string
		want bool
	}{{`{"requester_only":true}`, true}, {`{"new_movie":true}`, true}, {`{"requester_only":false}`, false}} {
		rr := httptest.NewRecorder()
		h.UpdatePreferences(rr, withUser(httptest.NewRequest(http.MethodPut, "/", strings.NewReader(tc.body)), 1))
		var got map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &got)
		if rr.Code != 200 || got["requester_only"] != tc.want {
			t.Fatalf("save %s: %d %v", tc.body, rr.Code, got)
		}
	}
	prefs, err := NewPrefsStore(database).Get(2)
	if err != nil || prefs.RequesterOnly {
		t.Fatalf("default: %+v %v", prefs, err)
	}
}

func TestRequesterClaimsDoNotSuppressAnotherUsersLaterEpisode(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatal(err)
	}
	for id := 1; id <= 3; id++ {
		mustExec(t, database, `INSERT INTO users(id,username,password_hash,role) VALUES(?,?,'','user')`, id, fmt.Sprint(id))
	}
	mustExec(t, database, `INSERT INTO notification_prefs(user_id,requester_only) VALUES(1,1),(2,1)`)
	mgr, capture := newNotifierTestGateway(t, database)
	instance := seedPushLibrary(t, database, "sonarr")
	n := NewNotifier(database, mgr, nil)
	n.SetRequestMatcher(requestMatcherFunc(func(_ context.Context, e contentnotify.Import, users []int64) ([]int64, error) {
		var out []int64
		for _, u := range users {
			if slices.Contains(e.EpisodeIDs, int(u)) {
				out = append(out, u, u)
			}
		}
		return out, nil
	}))
	n.NotifyNewEpisodes("Show", 123, instance, []int{1})
	if got := userIDsOf(t, capture.waitForNotification(t)); fmt.Sprint(got) != "[1 3]" {
		t.Fatalf("first audience: %v", got)
	}
	// The first global title claim is still fresh, but user 2's different
	// requested episode must deliver. Broad user 3 must not get a second push.
	n.NotifyNewEpisodes("Show", 123, instance, []int{2})
	if got := userIDsOf(t, capture.waitForNotification(t)); fmt.Sprint(got) != "[2]" {
		t.Fatalf("second audience: %v", got)
	}
	n.NotifyNewEpisodes("Show", 123, instance, []int{1, 2})
	select {
	case got := <-capture.ch:
		t.Fatalf("duplicate: %v", got)
	case <-time.After(50 * time.Millisecond):
	}
	// A server restart retains the per-user claim, while a later completion
	// after the normal aggregation window is eligible again.
	n = NewNotifier(database, mgr, nil)
	n.SetRequestMatcher(requestMatcherFunc(func(_ context.Context, _ contentnotify.Import, u []int64) ([]int64, error) { return u, nil }))
	mustExec(t, database, `UPDATE content_alert_claims SET claimed_at=datetime('now','-11 minutes')`)
	n.NotifyNewEpisodes("Show", 123, instance, []int{3})
	if got := userIDsOf(t, capture.waitForNotification(t)); len(got) != 3 {
		t.Fatalf("later completion: %v", got)
	}
}

func TestRequesterContentRechecksAtAsyncDelivery(t *testing.T) {
	for _, change := range []string{"grant", "personal", "server", "request"} {
		t.Run(change, func(t *testing.T) {
			database, err := dbOpen(t)
			if err != nil {
				t.Fatal(err)
			}
			mustExec(t, database, `INSERT INTO users(id,username,password_hash,role) VALUES(1,'one','','user')`)
			mustExec(t, database, `INSERT INTO notification_prefs(user_id,requester_only) VALUES(1,1)`)
			mgr, capture := newNotifierTestGateway(t, database)
			instance := seedPushLibrary(t, database, "sonarr")
			n := NewNotifier(database, mgr, nil)
			var calls atomic.Int32
			checked := make(chan struct{})
			n.SetRequestMatcher(requestMatcherFunc(func(_ context.Context, _ contentnotify.Import, u []int64) ([]int64, error) {
				if calls.Add(1) == 2 {
					switch change {
					case "grant":
						mustExec(t, database, `DELETE FROM user_instance_grants WHERE user_id=1`)
					case "personal":
						mustExec(t, database, `UPDATE notification_prefs SET push_enabled=0 WHERE user_id=1`)
					case "server":
						disabled := false
						if err := n.prefs.setPolicy(&disabled, nil); err != nil {
							t.Error(err)
						}
					case "request":
						close(checked)
						return nil, nil
					}
					close(checked)
				}
				return u, nil
			}))
			n.NotifyNewEpisodes("Show", 123, instance, []int{1})
			select {
			case <-checked:
			case <-time.After(time.Second):
				t.Fatal("delivery did not recheck")
			}
			select {
			case got := <-capture.ch:
				t.Fatalf("revoked recipient received %v", got)
			case <-time.After(50 * time.Millisecond):
			}
		})
	}
}

func TestRequesterOnlyFailsClosedWithoutMatcher(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, database, `INSERT INTO users(id,username,password_hash,role) VALUES(1,'one','','user')`)
	mustExec(t, database, `INSERT INTO notification_prefs(user_id,requester_only) VALUES(1,1)`)
	mgr, capture := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)
	n.NotifyNewEpisode("Show", 123, seedPushLibrary(t, database, "sonarr"))
	select {
	case got := <-capture.ch:
		t.Fatalf("unverified request received %v", got)
	case <-time.After(50 * time.Millisecond):
	}
}
