package push

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/db"
)

// dbOpen opens an in-memory database with the full schema, registered for
// cleanup. The schema includes the users table the admin query relies on.
func dbOpen(t *testing.T) (*sql.DB, error) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { database.Close() })
	return database, nil
}

// mustExec runs a statement, failing the test on error.
func mustExec(t *testing.T, database *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := database.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// notificationCapture records POST /v1/notifications bodies as they arrive,
// signaling a channel so a test can wait out the fire-and-forget goroutine.
type notificationCapture struct {
	ch chan map[string]any
}

func newNotifierTestGateway(t *testing.T) (*Client, *notificationCapture) {
	t.Helper()
	cap := &notificationCapture{ch: make(chan map[string]any, 4)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/notifications" {
			raw, _ := io.ReadAll(r.Body)
			body := map[string]any{}
			_ = json.Unmarshal(raw, &body)
			cap.ch <- body
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"sent":1,"failed":0}`)
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, "pgk_test"), cap
}

// waitForNotification returns the next captured notification body, failing if
// none arrives in time.
func (c *notificationCapture) waitForNotification(t *testing.T) map[string]any {
	t.Helper()
	select {
	case body := <-c.ch:
		return body
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for notification")
		return nil
	}
}

// userIDsOf extracts to.user_ids from a captured notification body.
func userIDsOf(t *testing.T, body map[string]any) []string {
	t.Helper()
	to, _ := body["to"].(map[string]any)
	raw, _ := to["user_ids"].([]any)
	ids := make([]string, len(raw))
	for i, v := range raw {
		ids[i], _ = v.(string)
	}
	return ids
}

func TestNotifyUserRequestDecision(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	client, cap := newNotifierTestGateway(t)
	n := NewNotifier(database, client, nil)

	n.NotifyUser(42, "request_decision", map[string]interface{}{
		"decision":   "approved",
		"tmdb_id":    603,
		"media_type": "movie",
		"title":      "The Matrix",
		"status":     "requested",
	})

	body := cap.waitForNotification(t)

	ids := userIDsOf(t, body)
	if len(ids) != 1 || ids[0] != "42" {
		t.Errorf("user_ids = %v, want [\"42\"]", ids)
	}
	notif, _ := body["notification"].(map[string]any)
	if notif["title"] == "" || notif["title"] == nil {
		t.Errorf("expected non-empty title, got %v", notif["title"])
	}
	data, _ := body["data"].(map[string]any)
	if data["type"] != "request_decision" {
		t.Errorf("data.type = %v, want request_decision", data["type"])
	}
}

func TestNotifyUserIgnoresOtherEvents(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	client, cap := newNotifierTestGateway(t)
	n := NewNotifier(database, client, nil)

	n.NotifyUser(42, "request_status_changed", map[string]interface{}{"title": "X"})

	select {
	case <-cap.ch:
		t.Fatal("unexpected notification for non-decision event")
	case <-time.After(200 * time.Millisecond):
		// expected: nothing sent
	}
}

func TestNotifyAdminsResolvesAdminIDs(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// Two admins and one regular user; only admins should be targeted.
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'admin1', '', 'admin')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (2, 'admin2', '', 'admin')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (3, 'bob', '', 'user')")

	client, cap := newNotifierTestGateway(t)
	n := NewNotifier(database, client, nil)

	n.NotifyAdmins("request_pending", map[string]interface{}{
		"tmdb_id":    603,
		"media_type": "movie",
		"title":      "The Matrix",
	})

	body := cap.waitForNotification(t)
	ids := userIDsOf(t, body)
	if len(ids) != 2 {
		t.Fatalf("admin user_ids = %v, want 2 ids", ids)
	}
	want := map[string]bool{"1": true, "2": true}
	for _, id := range ids {
		if !want[id] {
			t.Errorf("unexpected admin id %q in %v", id, ids)
		}
	}
}

func TestNotifierDisabledClientIsNoop(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// A nil client must make every call a harmless no-op (no panic).
	n := NewNotifier(database, nil, nil)
	n.NotifyUser(1, "request_decision", map[string]interface{}{"decision": "approved", "title": "X"})
	n.NotifyAdmins("request_pending", map[string]interface{}{"title": "X"})
}
