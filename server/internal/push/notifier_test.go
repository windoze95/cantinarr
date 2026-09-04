package push

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
	// Movie/TV rows carry no foreign_id — the field is book-only and must not
	// appear in their payloads.
	if v, ok := data["foreign_id"]; ok {
		t.Errorf("data.foreign_id = %v, want absent for a movie decision", v)
	}
}

// A book decision carries its canonical identity and selected Chaptarr
// instance, and its visible copy names only the format that actually succeeded.
func TestNotifyUserBookRequestDecisionCarriesPinnedFormatScope(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (42, 'req', '', 'user')")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, request_decision) VALUES (42, 1)")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyUser(42, "request_decision", map[string]interface{}{
		"decision":    "approved",
		"tmdb_id":     0,
		"media_type":  "book",
		"foreign_id":  "29749107",
		"instance_id": "family-books",
		"book_format": "ebook",
		"title":       "Ahsoka (Star Wars)",
		"status":      "requested",
	})

	body := cap.waitForNotification(t)
	data, _ := body["data"].(map[string]any)
	if data["type"] != "request_decision" || data["media_type"] != "book" {
		t.Errorf("data = %v, want type/media_type request_decision/book", data)
	}
	if data["foreign_id"] != "29749107" {
		t.Errorf("data.foreign_id = %v, want 29749107", data["foreign_id"])
	}
	if data["instance_id"] != "family-books" || data["title"] != "Ahsoka (Star Wars)" || data["book_format"] != "ebook" {
		t.Errorf("book deep-link data = %v, want pinned instance/title/ebook scope", data)
	}
	notification, _ := body["notification"].(map[string]any)
	if notification["body"] != "Ahsoka (Star Wars) eBook is on the way" {
		t.Errorf("notification body = %q, want format-scoped approval", notification["body"])
	}
}

func TestDecisionMessageScopesBookDenialAndBothApproval(t *testing.T) {
	_, denied := decisionMessage(map[string]interface{}{
		"decision": "denied", "media_type": "book", "title": "Flock",
		"book_format": "audiobook", "reason": "not available",
	})
	if denied != "Flock Audiobook was denied: not available" {
		t.Fatalf("denied body = %q", denied)
	}
	_, approved := decisionMessage(map[string]interface{}{
		"decision": "approved", "media_type": "book", "title": "Flock",
		"book_format": "both",
	})
	if approved != "Flock eBook and Audiobook are on the way" {
		t.Fatalf("approved body = %q", approved)
	}
	_, movie := decisionMessage(map[string]interface{}{
		"decision": "approved", "media_type": "movie", "title": "The Matrix",
	})
	if movie != "The Matrix is on the way" {
		t.Fatalf("movie body changed = %q", movie)
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

func TestNotifyAdminsSetsBadgeFromPendingCount(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'admin1', '', 'admin')")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyAdmins("request_pending", map[string]interface{}{
		"tmdb_id":       603,
		"media_type":    "movie",
		"title":         "The Matrix",
		"pending_count": 3,
	})

	body := cap.waitForNotification(t)
	notif, _ := body["notification"].(map[string]any)
	// JSON numbers decode as float64.
	if got, _ := notif["badge"].(float64); got != 3 {
		t.Errorf("notification.badge = %v, want 3", notif["badge"])
	}
}

func TestNotifyAdminsUsesAutomaticIssueCopy(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'admin1', '', 'admin')")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyAdmins("issue_created", map[string]interface{}{
		"issue_id":   42,
		"source":     "auto",
		"open_count": 1,
	})

	body := cap.waitForNotification(t)
	notif, _ := body["notification"].(map[string]any)
	if notif["title"] != "Problem needs attention" {
		t.Errorf("title = %v, want automatic-incident copy", notif["title"])
	}
	if notif["body"] != "Cantinarr found a media problem that did not recover automatically" {
		t.Errorf("body = %v, want automatic-recovery copy", notif["body"])
	}
	if _, ok := notif["badge"]; ok {
		t.Errorf("issue notification unexpectedly overwrote the global app badge: %v", notif["badge"])
	}
}

// A system issue is a condition on the server, and there are several distinct
// ones. This copy used to name shared-AI health outright, which was wrong on
// every push-delivery and book-import-stall alert it ever sent. It must stay
// generic: the only fields that could tell them apart are the issue's own
// untrusted title and detail, which never reach a lock screen.
func TestNotifyAdminsUsesGenericSystemIssueCopy(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'admin1', '', 'admin')")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyAdmins("issue_created", map[string]interface{}{
		"issue_id":   7,
		"source":     "system",
		"open_count": 1,
	})

	body := cap.waitForNotification(t)
	notif, _ := body["notification"].(map[string]any)
	if notif["title"] != "Cantinarr needs attention" {
		t.Errorf("title = %v, want generic server-condition copy", notif["title"])
	}
	text, _ := notif["body"].(string)
	if text != "Something on the server needs an administrator" {
		t.Errorf("body = %v, want generic server-condition copy", text)
	}
	// The specific regression: a push-delivery or book-import issue must never
	// announce itself as a shared-AI failure.
	if strings.Contains(strings.ToLower(text), "ai") {
		t.Errorf("system copy still names one particular condition: %q", text)
	}
}

// Two server conditions clearing the hold-down on the same tick really do
// coalesce, so the system branch has to carry the count the way the auto branch
// does. It used to discard it and send the singular shared-AI line twice over.
func TestNotifyAdminsCoalescesSystemConditions(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'admin1', '', 'admin')")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyAdmins("issue_created", map[string]interface{}{
		"source":     "system",
		"count":      2,
		"open_count": 2,
	})

	body := cap.waitForNotification(t)
	notif, _ := body["notification"].(map[string]any)
	if notif["body"] != "2 server conditions need an administrator" {
		t.Errorf("body = %v, want the counted system copy", notif["body"])
	}
	opts, _ := body["options"].(map[string]any)
	if opts["collapse_id"] != "issue_created:system" {
		t.Errorf("collapse_id = %v, want a per-source summary collapse", opts["collapse_id"])
	}
}

// A batch cause produces one incident per exact media scope, so a coalesced
// alert has to say how many rather than fan out one identical line per episode.
// It also collapses by source: the summary is a state, and a later summary
// replaces it instead of stacking.
func TestNotifyAdminsCoalescesAnIssueWave(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'admin1', '', 'admin')")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyAdmins("issue_created", map[string]interface{}{
		"source":     "auto",
		"count":      13,
		"open_count": 13,
	})

	body := cap.waitForNotification(t)
	notif, _ := body["notification"].(map[string]any)
	if notif["title"] != "Problems need attention" {
		t.Errorf("title = %v, want plural automatic-incident copy", notif["title"])
	}
	if notif["body"] != "Cantinarr found 13 media problems that did not recover automatically" {
		t.Errorf("body = %v, want a counted summary", notif["body"])
	}
	data, _ := body["data"].(map[string]any)
	if _, ok := data["issue_id"]; ok {
		t.Errorf("coalesced alert deep-linked a single incident: %v", data)
	}
	opts, _ := body["options"].(map[string]any)
	if opts["collapse_id"] != "issue_created:auto" {
		t.Errorf("collapse_id = %v, want issue_created:auto", opts["collapse_id"])
	}
}

// The singular path must not gain a collapse id: distinct one-off reports are
// distinct alerts, and collapsing them would hide one behind the next.
func TestNotifyAdminsKeepsSingleIssueAlertUncollapsed(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'admin1', '', 'admin')")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyAdmins("issue_created", map[string]interface{}{"issue_id": 7, "count": 1})

	body := cap.waitForNotification(t)
	notif, _ := body["notification"].(map[string]any)
	if notif["title"] != "New problem reported" {
		t.Errorf("title = %v, want singular reporter copy", notif["title"])
	}
	opts, _ := body["options"].(map[string]any)
	if _, ok := opts["collapse_id"]; ok {
		t.Errorf("single issue alert collapsed: %v", opts["collapse_id"])
	}
}

// A wave of parked proposals pages once with the plural approval copy and a
// collapse id so a later summary replaces rather than stacks; no issue_id rides
// along because no single thread represents the batch (the app's approval queue
// is the destination either way).
func TestNotifyAdminsCoalescesAnApprovalWave(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'admin1', '', 'admin')")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyAdmins("agent_action_pending", map[string]interface{}{
		"count":         13,
		"pending_count": 13,
	})

	body := cap.waitForNotification(t)
	notif, _ := body["notification"].(map[string]any)
	if notif["title"] != "Fixes need your approval" {
		t.Errorf("title = %v, want plural approval copy", notif["title"])
	}
	if notif["body"] != "The assistant proposed fixes for 13 problems and needs you to approve them" {
		t.Errorf("body = %v, want a counted summary", notif["body"])
	}
	data, _ := body["data"].(map[string]any)
	if _, ok := data["issue_id"]; ok {
		t.Errorf("coalesced approval push deep-linked a single incident: %v", data)
	}
	opts, _ := body["options"].(map[string]any)
	if opts["collapse_id"] != "agent_action_pending" {
		t.Errorf("collapse_id = %v, want agent_action_pending", opts["collapse_id"])
	}
}

// A single proposal keeps the original copy, its deep link, and no collapse id.
func TestNotifyAdminsKeepsSingleApprovalPushUncollapsed(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'admin1', '', 'admin')")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyAdmins("agent_action_pending", map[string]interface{}{"issue_id": 7})

	body := cap.waitForNotification(t)
	notif, _ := body["notification"].(map[string]any)
	if notif["title"] != "A fix needs your approval" {
		t.Errorf("title = %v, want singular approval copy", notif["title"])
	}
	data, _ := body["data"].(map[string]any)
	if got, ok := data["issue_id"]; !ok || fmt.Sprint(got) != "7" {
		t.Errorf("single approval push issue_id = %v, want 7", got)
	}
	opts, _ := body["options"].(map[string]any)
	if _, ok := opts["collapse_id"]; ok {
		t.Errorf("single approval push collapsed: %v", opts["collapse_id"])
	}
}

// PUSH-010: Requester opt-in cannot bypass admin-only recipient selection.
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
	n.NotifyNewMovie("The Matrix", 603, "")
	n.NotifyNewEpisode("Severance", 95396, "")
	n.NotifyNewBook("Ahsoka", "29749107", "books-a", "ebook")
	n.NotifyUpgradedMovie("The Matrix", 603, "")
	n.NotifyUpgradedEpisode("Severance", 95396, "")
	n.NotifyUpgradedBook("Ahsoka", "29749107", "books-a", "ebook")
}

// TestNotifyUpgradedClaimsBroadcastKeyEvenWithNilClient pins the one part of
// an upgrade alert that must run while the push gateway is unenrolled: the
// silent claim of the broadcast key. Without it, a boot-resumption hold could
// later broadcast an upgrade the webhook already proved.
func TestNotifyUpgradedClaimsBroadcastKeyEvenWithNilClient(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	n := NewNotifier(database, nil, nil)

	n.NotifyUpgradedMovie("The Matrix", 603, "")
	if n.claimContentAlert(CategoryNewMovie, "movie", "603", "The Matrix") {
		t.Error("the broadcast key was not silently claimed, so the poller would page everyone")
	}
	n.NotifyUpgradedEpisode("Severance", 95396, "")
	if n.claimContentAlert(CategoryNewEpisode, "tv", "95396", "Severance") {
		t.Error("the episode broadcast key was not silently claimed")
	}
	n.NotifyUpgradedBook("Ahsoka", "29749107", "books-a", "ebook")
	if n.claimContentAlert(CategoryNewBook, "book", contentClaimID("books-a", "29749107|ebook"), "Ahsoka") {
		t.Error("the book broadcast key was not silently claimed")
	}
	// Library-scoped claims: an upgrade on one library silences the poller
	// for THAT library only — a sibling's identical title still alerts.
	n.NotifyUpgradedMovie("Dune", 438631, "radarr-4k")
	if n.claimContentAlert(CategoryNewMovie, "movie", contentClaimID("radarr-4k", "438631"), "Dune") {
		t.Error("the 4K broadcast key was not silently claimed")
	}
	if !n.claimContentAlert(CategoryNewMovie, "movie", contentClaimID("radarr-hd", "438631"), "Dune") {
		t.Error("a sibling library's key must stay claimable")
	}
}

// TestNotifyUpgradedMovieReachesOptedInAdminsOnly pins the audience and the
// payload of the admin upgrade alert: default off, role-scoped in SQL, and a
// tap payload shaped exactly like new_movie's so the app's existing routing
// applies.
func TestNotifyUpgradedMovieReachesOptedInAdminsOnly(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// alice: regular user OPTED IN (must still be excluded). root: admin opted
	// in. dora: admin with no row (default off, excluded).
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'alice', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (2, 'root', '', 'admin')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (3, 'dora', '', 'admin')")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, content_upgraded) VALUES (1, 1)")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, content_upgraded) VALUES (2, 1)")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyUpgradedMovie("The Matrix", 603, "")

	body := cap.waitForNotification(t)
	ids := userIDsOf(t, body)
	if len(ids) != 1 || ids[0] != "2" {
		t.Errorf("content_upgraded recipients = %v, want only the opted-in admin [\"2\"]", ids)
	}
	notif, _ := body["notification"].(map[string]any)
	if notif["title"] != "Movie upgraded" {
		t.Errorf("title = %v, want \"Movie upgraded\"", notif["title"])
	}
	if notif["body"] != "The Matrix was replaced with a better version" {
		t.Errorf("body = %v", notif["body"])
	}
	data, _ := body["data"].(map[string]any)
	if data["type"] != "content_upgraded" || data["media_type"] != "movie" {
		t.Errorf("data = %v, want type content_upgraded, media_type movie", data)
	}
	if num, ok := data["tmdb_id"].(float64); !ok || int(num) != 603 {
		t.Errorf("data.tmdb_id = %v, want 603", data["tmdb_id"])
	}
	opts, _ := body["options"].(map[string]any)
	if opts["collapse_id"] != "content_upgraded:603" {
		t.Errorf("collapse_id = %v, want content_upgraded:603", opts["collapse_id"])
	}

	// The matching broadcast key was claimed silently: the poller's later
	// re-witness of this import must not page the household.
	if n.claimContentAlert(CategoryNewMovie, "movie", "603", "The Matrix") {
		t.Error("new_movie key was claimable after the upgrade alert — the poller would double-page")
	}
}

// TestNotifyUpgradedBookIsAdminScopedNotInstanceScoped pins the deliberate
// asymmetry with new_book: an upgrade is operational oversight, not a "ready
// to read" call to action, so it takes the standard admin path — the
// instance's assigned reader (a regular user) is NOT paged — while the payload
// still carries the full book deep-link identity.
func TestNotifyUpgradedBookIsAdminScopedNotInstanceScoped(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// alice: books-a's assigned reader, opted into everything (still excluded
	// — not an admin). root: admin opted in, no books assignment needed.
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'alice', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (2, 'root', '', 'admin')")
	mustExec(t, database, "INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (1, 'chaptarr', 'books-a')")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, new_book, content_upgraded) VALUES (1, 1, 1)")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, content_upgraded) VALUES (2, 1)")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyUpgradedBook("Ahsoka", "29749107", "books-a", "ebook")

	body := cap.waitForNotification(t)
	ids := userIDsOf(t, body)
	if len(ids) != 1 || ids[0] != "2" {
		t.Errorf("book upgrade recipients = %v, want only the admin [\"2\"]", ids)
	}
	notif, _ := body["notification"].(map[string]any)
	if notif["title"] != "Book upgraded" {
		t.Errorf("title = %v, want \"Book upgraded\"", notif["title"])
	}
	if notif["body"] != "Ahsoka eBook was upgraded" {
		t.Errorf("body = %v, want \"Ahsoka eBook was upgraded\"", notif["body"])
	}
	data, _ := body["data"].(map[string]any)
	if data["type"] != "content_upgraded" || data["media_type"] != "book" ||
		data["foreign_id"] != "29749107" || data["instance_id"] != "books-a" ||
		data["title"] != "Ahsoka" || data["book_format"] != "ebook" {
		t.Errorf("data = %v, want the full book deep-link identity", data)
	}
	opts, _ := body["options"].(map[string]any)
	if opts["collapse_id"] != "content_upgraded:29749107:ebook" {
		t.Errorf("collapse_id = %v, want content_upgraded:29749107:ebook", opts["collapse_id"])
	}
	if n.claimContentAlert(CategoryNewBook, "book", contentClaimID("books-a", "29749107|ebook"), "Ahsoka") {
		t.Error("new_book key was claimable after the upgrade alert — the poller would double-page")
	}
}

// TestNotifyUpgradedMusicIsAdminScopedNotInstanceScoped pins the same
// deliberate asymmetry for music: the instance's assigned listener (a regular
// user) is NOT paged for an upgrade, while the payload still carries the full
// album deep-link identity.
func TestNotifyUpgradedMusicIsAdminScopedNotInstanceScoped(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'alice', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (2, 'root', '', 'admin')")
	mustExec(t, database, "INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (1, 'lidarr', 'music-a')")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, new_music, content_upgraded) VALUES (1, 1, 1)")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, content_upgraded) VALUES (2, 1)")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyUpgradedMusic("Fear Inoculum", "Tool", "1f4a9e6b", "music-a")

	body := cap.waitForNotification(t)
	ids := userIDsOf(t, body)
	if len(ids) != 1 || ids[0] != "2" {
		t.Errorf("album upgrade recipients = %v, want only the admin [\"2\"]", ids)
	}
	notif, _ := body["notification"].(map[string]any)
	if notif["title"] != "Album upgraded" {
		t.Errorf("title = %v, want \"Album upgraded\"", notif["title"])
	}
	if notif["body"] != "Fear Inoculum by Tool was upgraded" {
		t.Errorf("body = %v, want the artist-named upgrade copy", notif["body"])
	}
	data, _ := body["data"].(map[string]any)
	if data["type"] != "content_upgraded" || data["media_type"] != "music" ||
		data["foreign_id"] != "1f4a9e6b" || data["instance_id"] != "music-a" ||
		data["title"] != "Fear Inoculum" {
		t.Errorf("data = %v, want the full album deep-link identity", data)
	}
	opts, _ := body["options"].(map[string]any)
	if opts["collapse_id"] != "content_upgraded:1f4a9e6b" {
		t.Errorf("collapse_id = %v, want content_upgraded:1f4a9e6b", opts["collapse_id"])
	}
	if n.claimContentAlert(CategoryNewMusic, "music", contentClaimID("music-a", "1f4a9e6b"), "Fear Inoculum") {
		t.Error("new_music key was claimable after the upgrade alert — the poller would double-page")
	}
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

	n.NotifyNewMovie("The Matrix", 603, "")

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

	n.NotifyNewEpisode("Severance", 95396, "")

	body := cap.waitForNotification(t)
	ids := userIDsOf(t, body)
	if len(ids) != 1 || ids[0] != "1" {
		t.Errorf("new_episode recipients = %v, want [\"1\"]", ids)
	}
	notif, _ := body["notification"].(map[string]any)
	if notif["title"] != "New episodes available" {
		t.Errorf("title = %v, want \"New episodes available\"", notif["title"])
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

// A library-scoped new_movie pages only the users who can see that library,
// carries the library in the payload, and — because the dedupe claim is
// per-library — the same title landing on the sibling inside the window still
// alerts the sibling's own audience.
func TestNotifyNewMovieScopesToLibraryAndAlertsSiblings(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO service_instances (id, service_type, name, url, api_key, is_default) VALUES ('radarr-hd', 'radarr', 'Movies', 'http://hd', 'k', 1)")
	mustExec(t, database, "INSERT INTO service_instances (id, service_type, name, url, api_key) VALUES ('radarr-4k', 'radarr', '4K Movies', 'http://4k', 'k')")
	// alice(1): default library only. bob(2): pinned 4K.
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'alice', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (2, 'bob', '', 'user')")
	mustExec(t, database, "INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (2, 'radarr', 'radarr-4k')")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyNewMovie("Dune", 438631, "radarr-hd")
	body := cap.waitForNotification(t)
	if ids := userIDsOf(t, body); len(ids) != 1 || ids[0] != "1" {
		t.Errorf("HD recipients = %v, want alice(1) only", ids)
	}
	data, _ := body["data"].(map[string]any)
	if data["instance_id"] != "radarr-hd" {
		t.Errorf("data.instance_id = %v, want radarr-hd", data["instance_id"])
	}

	// The same title importing on the 4K library moments later still alerts
	// its own audience — one shared claim would have dropped it.
	n.NotifyNewMovie("Dune", 438631, "radarr-4k")
	body = cap.waitForNotification(t)
	if ids := userIDsOf(t, body); len(ids) != 1 || ids[0] != "2" {
		t.Errorf("4K recipients = %v, want bob(2) only", ids)
	}
	if data, _ := body["data"].(map[string]any); data["instance_id"] != "radarr-4k" {
		t.Errorf("data.instance_id = %v, want radarr-4k", data["instance_id"])
	}
}

// A book import alerts the users who can actually open it: opted-in holders
// of that instance's grant plus admins — never a sibling instance's users —
// and the payload carries the book deep-link identity the app already routes.
func TestNotifyNewBookScopesRecipientsAndCarriesBookIdentity(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// admin(1) has implicit access; alice(2) is granted books-a; carol(3) is
	// granted the sibling books-b and must not hear about books-a imports.
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'admin', '', 'admin')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (2, 'alice', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (3, 'carol', '', 'user')")
	mustExec(t, database, "INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (2, 'chaptarr', 'books-a')")
	mustExec(t, database, "INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (3, 'chaptarr', 'books-b')")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyNewBook("Ahsoka (Star Wars)", "29749107", "books-a", "ebook")

	body := cap.waitForNotification(t)
	ids := userIDsOf(t, body)
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	if len(ids) != 2 || !got["1"] || !got["2"] {
		t.Errorf("new_book recipients = %v, want admin(1) and alice(2)", ids)
	}
	notif, _ := body["notification"].(map[string]any)
	if notif["title"] != "New book available" {
		t.Errorf("title = %v, want \"New book available\"", notif["title"])
	}
	if notif["body"] != "Ahsoka (Star Wars) eBook is ready to read" {
		t.Errorf("body = %v, want format-scoped availability copy", notif["body"])
	}
	data, _ := body["data"].(map[string]any)
	if data["type"] != "new_book" || data["media_type"] != "book" {
		t.Errorf("data = %v, want type/media_type new_book/book", data)
	}
	if data["foreign_id"] != "29749107" || data["instance_id"] != "books-a" ||
		data["title"] != "Ahsoka (Star Wars)" || data["book_format"] != "ebook" {
		t.Errorf("book deep-link data = %v, want foreign_id/instance/title/format", data)
	}
	opts, _ := body["options"].(map[string]any)
	if opts["collapse_id"] != "new_book:29749107:ebook" {
		t.Errorf("collapse_id = %v, want new_book:29749107:ebook", opts["collapse_id"])
	}
}

// The audiobook record of the same title alerts independently with its own
// copy — and a user without any chaptarr grant is never targeted.
func TestNotifyNewBookAudiobookCopyAndNoUngrantedRecipients(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (2, 'alice', '', 'user')")
	mustExec(t, database, "INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (2, 'chaptarr', 'books-a')")
	// dave has new_book on by default but no grant: with alice as the only
	// grantee, he must not widen the audience.
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (5, 'dave', '', 'user')")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyNewBook("Ahsoka (Star Wars)", "29749107", "books-a", "audiobook")

	body := cap.waitForNotification(t)
	ids := userIDsOf(t, body)
	if len(ids) != 1 || ids[0] != "2" {
		t.Errorf("new_book recipients = %v, want just granted alice(2)", ids)
	}
	notif, _ := body["notification"].(map[string]any)
	if notif["body"] != "Ahsoka (Star Wars) Audiobook is ready to play" {
		t.Errorf("body = %v, want audiobook availability copy", notif["body"])
	}
}

func TestNotifyNewBookNoEligibleRecipientsIsNoop(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// dave is opted in by default but holds no chaptarr grant, so a book
	// import has no eligible audience and nothing is sent.
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (5, 'dave', '', 'user')")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyNewBook("Ahsoka (Star Wars)", "29749107", "books-a", "ebook")

	select {
	case <-cap.ch:
		t.Fatal("unexpected notification: nobody can see books-a")
	case <-time.After(200 * time.Millisecond):
		// expected: nothing sent
	}
}

// A music import alerts the users who can actually play it: opted-in holders
// of that instance's assignment plus admins — never a sibling instance's
// users — and the payload carries the album deep-link identity the app
// already routes.
func TestNotifyNewMusicScopesRecipientsAndCarriesMusicIdentity(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// admin(1) has implicit access; alice(2) is assigned music-a; carol(3) is
	// assigned the sibling music-b and must not hear about music-a imports.
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'admin', '', 'admin')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (2, 'alice', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (3, 'carol', '', 'user')")
	mustExec(t, database, "INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (2, 'lidarr', 'music-a')")
	mustExec(t, database, "INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (3, 'lidarr', 'music-b')")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyNewMusic("Fear Inoculum", "Tool", "1f4a9e6b", "music-a")

	body := cap.waitForNotification(t)
	ids := userIDsOf(t, body)
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	if len(ids) != 2 || !got["1"] || !got["2"] {
		t.Errorf("new_music recipients = %v, want admin(1) and alice(2)", ids)
	}
	notif, _ := body["notification"].(map[string]any)
	if notif["title"] != "New music available" {
		t.Errorf("title = %v, want \"New music available\"", notif["title"])
	}
	if notif["body"] != "Fear Inoculum by Tool is ready to play" {
		t.Errorf("body = %v, want the artist-named availability copy", notif["body"])
	}
	data, _ := body["data"].(map[string]any)
	if data["type"] != "new_music" || data["media_type"] != "music" {
		t.Errorf("data = %v, want type/media_type new_music/music", data)
	}
	if data["foreign_id"] != "1f4a9e6b" || data["instance_id"] != "music-a" ||
		data["title"] != "Fear Inoculum" {
		t.Errorf("album deep-link data = %v, want foreign_id/instance/title", data)
	}
	// Music has no format axis: the book-only key must never appear.
	if _, hasFormat := data["book_format"]; hasFormat {
		t.Errorf("data = %v, want no book_format on a music alert", data)
	}
	opts, _ := body["options"].(map[string]any)
	if opts["collapse_id"] != "new_music:1f4a9e6b" {
		t.Errorf("collapse_id = %v, want new_music:1f4a9e6b", opts["collapse_id"])
	}
}

func TestNotifyNewMusicNoEligibleRecipientsIsNoop(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// dave is opted in by default but holds no lidarr assignment, so an album
	// import has no eligible audience and nothing is sent.
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (5, 'dave', '', 'user')")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyNewMusic("Fear Inoculum", "Tool", "1f4a9e6b", "music-a")

	select {
	case <-cap.ch:
		t.Fatal("unexpected notification: nobody can see music-a")
	case <-time.After(200 * time.Millisecond):
		// expected: nothing sent
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
	n.NotifyNewMovie("The Matrix", 603, "")

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

	n.NotifyNewMovie("The Matrix", 603, "")

	select {
	case <-cap.ch:
		t.Fatal("unexpected notification: no users opted into new_movie")
	case <-time.After(200 * time.Millisecond):
		// expected: nothing sent
	}
}

// A paused standing rule pages admins with a fixed template, deep-links the
// evidence issue, collapses per rule, and — by design — is gated by the same
// preference column as agent_action_pending.
func TestNotifyAdminsAutoApprovalPausedFixedTemplateAndSharedPref(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// admin1: default prefs (on). admin2: opted out of agent_action_pending,
	// which must also silence pause alerts (shared column). user3: never paged
	// (admin-scoped category).
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'admin1', '', 'admin')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (2, 'admin2', '', 'admin')")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, agent_action_pending) VALUES (2, 0)")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (3, 'user3', '', 'user')")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyAdmins("agent_autoapproval_paused", map[string]interface{}{
		"rule_id":       5,
		"issue_id":      7,
		"label":         "Manual import · Waiting to import",
		"paused_reason": "An auto-approved fix failed to execute.",
	})

	body := cap.waitForNotification(t)
	ids := userIDsOf(t, body)
	if len(ids) != 1 || ids[0] != "1" {
		t.Errorf("user_ids = %v, want only the opted-in admin", ids)
	}
	notif, _ := body["notification"].(map[string]any)
	if notif["title"] != "Auto-approval paused" {
		t.Errorf("title = %v", notif["title"])
	}
	if notif["body"] != "An automatic fix did not complete successfully, so its auto-approval rule was paused" {
		t.Errorf("body = %v, want the fixed template", notif["body"])
	}
	data, _ := body["data"].(map[string]any)
	if got, ok := data["issue_id"]; !ok || fmt.Sprint(got) != "7" {
		t.Errorf("issue_id = %v, want 7 for the deep link", got)
	}
	if _, ok := data["label"]; ok {
		t.Errorf("rule label leaked into the push payload: %v", data)
	}
	opts, _ := body["options"].(map[string]any)
	if opts["collapse_id"] != "agent_autoapproval_paused:5" {
		t.Errorf("collapse_id = %v, want per-rule collapse", opts["collapse_id"])
	}
}

// A parked profile-change proposal pages the same audience as an agent fix
// awaiting approval — it shares the agent_action_pending preference column —
// with a fixed body (profile/instance names ride only as data fields) and a
// per-target collapse so a superseding proposal replaces the stale alert.
func TestNotifyAdminsProfileChangePendingSharedPrefAndCollapse(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// admin1: default prefs (on). admin2: opted out of agent_action_pending,
	// which must also silence proposal alerts (shared column). user3: never
	// paged (admin-scoped category).
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'admin1', '', 'admin')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (2, 'admin2', '', 'admin')")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, agent_action_pending) VALUES (2, 0)")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (3, 'user3', '', 'user')")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	n.NotifyAdmins("profile_change_pending", map[string]interface{}{
		"proposal_id":  int64(12),
		"service":      "radarr",
		"instance_id":  "movies-a",
		"profile_id":   6,
		"profile_name": "HD-1080p",
	})

	body := cap.waitForNotification(t)
	ids := userIDsOf(t, body)
	if len(ids) != 1 || ids[0] != "1" {
		t.Errorf("user_ids = %v, want only the opted-in admin", ids)
	}
	notif, _ := body["notification"].(map[string]any)
	if notif["title"] != "A settings change needs your approval" {
		t.Errorf("title = %v", notif["title"])
	}
	if notif["body"] != "An external assistant proposed a quality-profile change and needs you to approve it" {
		t.Errorf("body = %v, want the fixed template", notif["body"])
	}
	data, _ := body["data"].(map[string]any)
	if data["type"] != "profile_change_pending" {
		t.Errorf("data.type = %v", data["type"])
	}
	if got, ok := data["proposal_id"]; !ok || fmt.Sprint(got) != "12" {
		t.Errorf("proposal_id = %v, want 12 for the deep link", got)
	}
	if _, ok := data["profile_name"]; ok {
		t.Errorf("profile name leaked into the push payload: %v", data)
	}
	opts, _ := body["options"].(map[string]any)
	if opts["collapse_id"] != "profile_change_pending:radarr:movies-a:6" {
		t.Errorf("collapse_id = %v, want per-target collapse", opts["collapse_id"])
	}
}

// The weekly digest speaks outcome vocabulary: "resolved" is every problem
// that ended well, with attribution glued to the number so automation claims
// only its own work. A week where everything cleared on its own says exactly
// that, and open work stays in its own "Right now" clause.
func TestNotifyAgentDigestOutcomeBody(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'boss', '', 'admin')")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)

	// The live shape that motivated the change: 438 self-cleared, nothing else.
	n.NotifyAdmins(CategoryAgentDigest, map[string]interface{}{
		"issues_resolved":   0,
		"self_cleared":      438,
		"rule_approved":     0,
		"resolved_by_agent": 0,
		"resolved_by_admin": 0,
		"needs_admin_open":  0,
		"pending_proposals": 0,
	})
	body := cap.waitForNotification(t)
	notif, _ := body["notification"].(map[string]any)
	if notif["body"] != "Last 7 days: 438 resolved — all on their own" {
		t.Errorf("self-cleared week body = %q", notif["body"])
	}

	// A busier week attributes each lane, and one open item reads "needs you".
	n.NotifyAdmins(CategoryAgentDigest, map[string]interface{}{
		"issues_resolved":   4,
		"self_cleared":      37,
		"rule_approved":     1,
		"resolved_by_agent": 2,
		"resolved_by_admin": 1,
		"needs_admin_open":  1,
		"pending_proposals": 0,
	})
	body = cap.waitForNotification(t)
	notif, _ = body["notification"].(map[string]any)
	want := "Last 7 days: 41 resolved — 2 by the agent · 1 by your rules · 1 by you · 37 on their own. Right now: 1 needs you"
	if notif["body"] != want {
		t.Errorf("attributed week body = %q, want %q", notif["body"], want)
	}

	// A lone self-cleared incident resolved on ITS own.
	n.NotifyAdmins(CategoryAgentDigest, map[string]interface{}{
		"issues_resolved": 0,
		"self_cleared":    1,
	})
	body = cap.waitForNotification(t)
	notif, _ = body["notification"].(map[string]any)
	if notif["body"] != "Last 7 days: 1 resolved — on its own" {
		t.Errorf("singular body = %q", notif["body"])
	}
}
