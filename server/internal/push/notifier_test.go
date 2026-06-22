package push

import (
	"context"
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

// newNotifierTestGateway stands up a mock gateway and returns an already-
// enrolled push.Manager wired to it (explicit key, so resolveAPIKey never
// touches the cipher or settings) plus a capture of POST /v1/notifications
// bodies. The manager shares the test's database so the notifier's token
// pruning hits the same rows.
func newNotifierTestGateway(t *testing.T, database *sql.DB) (*Manager, *notificationCapture) {
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
	mgr := NewManager(database, nil, srv.URL, "pgk_test", "", "Cantinarr", nil)
	if mgr.Ensure(context.Background()) == nil {
		t.Fatal("Ensure returned nil client for an explicit-key manager")
	}
	return mgr, cap
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
	// request_decision is off by default, so the requester must opt in to be
	// notified about their own decision.
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (42, 'req', '', 'user')")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, request_decision) VALUES (42, 1)")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

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

func TestNotifyUserRequestDecisionSuppressedByDefault(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// No prefs row: request_decision defaults to off, so nothing is sent.
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (42, 'req', '', 'user')")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyUser(42, "request_decision", map[string]interface{}{
		"decision": "approved",
		"title":    "The Matrix",
	})

	select {
	case <-cap.ch:
		t.Fatal("unexpected notification: request_decision is off by default")
	case <-time.After(200 * time.Millisecond):
		// expected: suppressed
	}
}

func TestNotifyUserRequestDecisionSuppressedWhenOff(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// Explicitly opted out.
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (42, 'req', '', 'user')")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, request_decision) VALUES (42, 0)")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyUser(42, "request_decision", map[string]interface{}{
		"decision": "approved",
		"title":    "The Matrix",
	})

	select {
	case <-cap.ch:
		t.Fatal("unexpected notification: request_decision pref is off")
	case <-time.After(200 * time.Millisecond):
		// expected: suppressed
	}
}

func TestNotifyUserIgnoresOtherEvents(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

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
	// Two admins and one regular user, none with a prefs row. request_pending
	// defaults on for admins, so both admins (and only admins) are targeted.
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'admin1', '', 'admin')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (2, 'admin2', '', 'admin')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (3, 'bob', '', 'user')")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

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

func TestNotifyAdminsHonorsOptOutAndRole(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// admin1 opts out of request_pending; admin2 keeps the default (on). A
	// non-admin who opts in must still be excluded (request_pending is
	// admin-only). Only admin2 should be targeted.
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'admin1', '', 'admin')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (2, 'admin2', '', 'admin')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (3, 'bob', '', 'user')")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, request_pending) VALUES (1, 0)")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, request_pending) VALUES (3, 1)")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyAdmins("request_pending", map[string]interface{}{"title": "The Matrix"})

	body := cap.waitForNotification(t)
	ids := userIDsOf(t, body)
	if len(ids) != 1 || ids[0] != "2" {
		t.Errorf("request_pending recipients = %v, want [\"2\"]", ids)
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
	n.NotifyNewMovie("The Matrix", 603)
	n.NotifyNewEpisode("Severance", 95396)
}

func TestNotifyNewMovieReachesOptedInUsers(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// alice keeps the default (new_movie on), bob opts out, carol opts in.
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'alice', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (2, 'bob', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (3, 'carol', '', 'user')")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, new_movie) VALUES (2, 0)")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, new_movie) VALUES (3, 1)")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyNewMovie("The Matrix", 603)

	body := cap.waitForNotification(t)
	ids := userIDsOf(t, body)
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	if len(ids) != 2 || !got["1"] || !got["3"] {
		t.Errorf("new_movie recipients = %v, want alice(1) and carol(3)", ids)
	}
	notif, _ := body["notification"].(map[string]any)
	if notif["title"] != "New movie available" {
		t.Errorf("title = %v, want \"New movie available\"", notif["title"])
	}
	if notif["body"] != "The Matrix is ready to watch" {
		t.Errorf("body = %v, want \"The Matrix is ready to watch\"", notif["body"])
	}
	data, _ := body["data"].(map[string]any)
	if data["type"] != "new_movie" || data["media_type"] != "movie" {
		t.Errorf("data = %v, want type/media_type new_movie/movie", data)
	}
	// tmdb_id arrives as a JSON number.
	if num, ok := data["tmdb_id"].(float64); !ok || int(num) != 603 {
		t.Errorf("data.tmdb_id = %v, want 603", data["tmdb_id"])
	}
	opts, _ := body["options"].(map[string]any)
	if opts["collapse_id"] != "new_movie:603" {
		t.Errorf("collapse_id = %v, want new_movie:603", opts["collapse_id"])
	}
}

func TestNotifyNewEpisodeReachesOptedInUsers(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// Default (no row) means new_episode on for a regular user.
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'alice', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (2, 'bob', '', 'user')")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, new_episode) VALUES (2, 0)")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyNewEpisode("Severance", 95396)

	body := cap.waitForNotification(t)
	ids := userIDsOf(t, body)
	if len(ids) != 1 || ids[0] != "1" {
		t.Errorf("new_episode recipients = %v, want [\"1\"]", ids)
	}
	notif, _ := body["notification"].(map[string]any)
	if notif["title"] != "New episode available" {
		t.Errorf("title = %v, want \"New episode available\"", notif["title"])
	}
	if notif["body"] != "New on Severance" {
		t.Errorf("body = %v, want \"New on Severance\"", notif["body"])
	}
	data, _ := body["data"].(map[string]any)
	if data["type"] != "new_episode" || data["media_type"] != "tv" {
		t.Errorf("data = %v, want type/media_type new_episode/tv", data)
	}
	opts, _ := body["options"].(map[string]any)
	if opts["collapse_id"] != "new_episode:95396" {
		t.Errorf("collapse_id = %v, want new_episode:95396", opts["collapse_id"])
	}
}

func TestNotifierPrunesDeadTokenOnPrunedResult(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// One opted-in user with a stored token the gateway will report as pruned.
	seedDeviceToken(t, database, 1, "dev-dead", "tok-dead")

	// A mock gateway that reports the token as pruned on send.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"sent":0,"failed":1,"results":[{"device_id":"dev-dead","token":"tok-dead","platform":"ios","ok":false,"pruned":true,"error":"unregistered"}]}`)
	}))
	t.Cleanup(srv.Close)

	mgr := NewManager(database, nil, srv.URL, "pgk_test", "", "Cantinarr", nil)
	if mgr.Ensure(context.Background()) == nil {
		t.Fatal("Ensure returned nil")
	}
	n := NewNotifier(database, mgr, nil)

	// new_movie is on by default, so user 1 is targeted and a send happens.
	n.NotifyNewMovie("The Matrix", 603)

	// The pruned token's local row must be deleted (fire-and-forget, so poll).
	deadline := time.After(2 * time.Second)
	for {
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM push_tokens WHERE token = 'tok-dead'").Scan(&count); err != nil {
			t.Fatalf("count tokens: %v", err)
		}
		if count == 0 {
			return // pruned as expected
		}
		select {
		case <-deadline:
			t.Fatal("dead token was not pruned from push_tokens")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestNotifyNewContentNoRecipientsIsNoop(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// The only user has opted out, so no push is sent.
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'alice', '', 'user')")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, new_movie) VALUES (1, 0)")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyNewMovie("The Matrix", 603)

	select {
	case <-cap.ch:
		t.Fatal("unexpected notification: no users opted into new_movie")
	case <-time.After(200 * time.Millisecond):
		// expected: nothing sent
	}
}
