package push

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/contentnotify"
)

func TestRequesterOnlyEnabledDuringDeliveryExcludesUnmatchedBroadRecipient(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, database, `INSERT INTO users(id,username,password_hash,role) VALUES(1,'requester','','user'),(2,'broad','','user')`)
	mustExec(t, database, `INSERT INTO notification_prefs(user_id,requester_only) VALUES(1,1),(2,0)`)
	mgr, capture := newNotifierTestGateway(t, database)
	instance := seedPushLibrary(t, database, "sonarr")
	n := NewNotifier(database, mgr, nil)
	var calls atomic.Int32
	n.SetRequestMatcher(requestMatcherFunc(func(_ context.Context, _ contentnotify.Import, candidates []int64) ([]int64, error) {
		if calls.Add(1) == 2 {
			// User 2 was broad when the delivery guard began. Their saved
			// preference changes while user 1's provider read is in flight.
			if _, err := database.Exec(`UPDATE notification_prefs SET requester_only=1 WHERE user_id=2`); err != nil {
				t.Errorf("enable requester-only during delivery: %v", err)
				return nil, err
			}
		}
		if slices.Contains(candidates, int64(1)) {
			return []int64{1}, nil
		}
		return nil, nil
	}))

	n.NotifyNewEpisodes("Requested show", 123, instance, []int{42})
	if got := userIDsOf(t, capture.waitForNotification(t)); !slices.Equal(got, []string{"1"}) {
		t.Fatalf("delivery after preference change = %v, want only matching user 1", got)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("request match reads = %d, want preparation and delivery reads", got)
	}
}

func TestRequesterOnlyEnabledAfterBroadSendDoesNotReplayImport(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, database, `INSERT INTO users(id,username,password_hash,role) VALUES(1,'viewer','','user')`)
	mgr, capture := newNotifierTestGateway(t, database)
	instance := seedPushLibrary(t, database, "radarr")
	n := NewNotifier(database, mgr, nil)
	n.NotifyNewMovie("Requested movie", 123, instance)
	if got := userIDsOf(t, capture.waitForNotification(t)); !slices.Equal(got, []string{"1"}) {
		t.Fatalf("first broad delivery = %v, want user 1", got)
	}
	mustExec(t, database, `INSERT INTO notification_prefs(user_id,requester_only) VALUES(1,1)`)

	// A process restart between the webhook and poll witness must retain the
	// receipt even though the recipient has changed their audience preference.
	n = NewNotifier(database, mgr, nil)
	matched := make(chan contentnotify.Import, 2)
	n.SetRequestMatcher(requestMatcherFunc(func(_ context.Context, event contentnotify.Import, candidates []int64) ([]int64, error) {
		matched <- event
		return candidates, nil
	}))
	n.NotifyNewMovie("Requested movie", 123, instance)
	waitRequesterMatch(t, matched)
	expectNoRequesterAlert(t, capture)
}

func TestRequesterUpgradeEvidenceSuppressesCompletionRewitness(t *testing.T) {
	for _, tc := range []struct {
		name, service string
		upgrade       func(*Notifier, string)
		completion    func(*Notifier, string)
	}{
		{
			name: "movie", service: "radarr",
			upgrade:    func(n *Notifier, id string) { n.NotifyUpgradedMovie("Requested movie", 123, id) },
			completion: func(n *Notifier, id string) { n.NotifyNewMovie("Requested movie", 123, id) },
		},
		{
			name: "ebook", service: "chaptarr",
			upgrade:    func(n *Notifier, id string) { n.NotifyUpgradedBook("Requested book", "book-123", id, "ebook") },
			completion: func(n *Notifier, id string) { n.NotifyNewBook("Requested book", "book-123", id, "ebook") },
		},
		{
			name: "audiobook", service: "chaptarr",
			upgrade:    func(n *Notifier, id string) { n.NotifyUpgradedBook("Requested book", "book-123", id, "audiobook") },
			completion: func(n *Notifier, id string) { n.NotifyNewBook("Requested book", "book-123", id, "audiobook") },
		},
		{
			name: "music", service: "lidarr",
			upgrade:    func(n *Notifier, id string) { n.NotifyUpgradedMusic("Requested album", "Artist", "album-123", id) },
			completion: func(n *Notifier, id string) { n.NotifyNewMusic("Requested album", "Artist", "album-123", id) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database, err := dbOpen(t)
			if err != nil {
				t.Fatal(err)
			}
			mustExec(t, database, `INSERT INTO users(id,username,password_hash,role) VALUES(1,'requester','','user')`)
			mustExec(t, database, `INSERT INTO notification_prefs(user_id,requester_only) VALUES(1,1)`)
			mgr, capture := newNotifierTestGateway(t, database)
			instance := seedPushLibrary(t, database, tc.service)
			n := NewNotifier(database, mgr, nil)
			tc.upgrade(n, instance)

			// Persisted upgrade evidence must also protect request-only
			// recipients when the queue poller resumes in a new process.
			n = NewNotifier(database, mgr, nil)
			matched := make(chan contentnotify.Import, 2)
			n.SetRequestMatcher(requestMatcherFunc(func(_ context.Context, event contentnotify.Import, candidates []int64) ([]int64, error) {
				matched <- event
				return candidates, nil
			}))
			tc.completion(n, instance)
			waitRequesterMatch(t, matched)
			expectNoRequesterAlert(t, capture)
		})
	}
}

func TestRequesterEpisodeUpgradeDoesNotSuppressDifferentLaterEpisode(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, database, `INSERT INTO users(id,username,password_hash,role) VALUES(1,'requester','','user')`)
	mustExec(t, database, `INSERT INTO notification_prefs(user_id,requester_only) VALUES(1,1)`)
	mgr, capture := newNotifierTestGateway(t, database)
	instance := seedPushLibrary(t, database, "sonarr")
	n := NewNotifier(database, mgr, nil)
	n.NotifyUpgradedEpisodes("Requested show", 123, instance, []int{42})

	n = NewNotifier(database, mgr, nil)
	matched := make(chan contentnotify.Import, 4)
	n.SetRequestMatcher(requestMatcherFunc(func(_ context.Context, event contentnotify.Import, candidates []int64) ([]int64, error) {
		matched <- event
		if slices.Contains(event.EpisodeIDs, 42) || slices.Contains(event.EpisodeIDs, 43) {
			return candidates, nil
		}
		return nil, nil
	}))
	n.NotifyNewEpisodes("Requested show", 123, instance, []int{42})
	expectNoRequesterAlert(t, capture)
	select {
	case event := <-matched:
		t.Fatalf("upgraded episode reached request matching: %v", event.EpisodeIDs)
	default:
	}

	// The title's global claim is still within the aggregation window. Exact
	// upgrade evidence must not suppress a different requested episode.
	n.NotifyNewEpisodes("Requested show", 123, instance, []int{43})
	if got := userIDsOf(t, capture.waitForNotification(t)); !slices.Equal(got, []string{"1"}) {
		t.Fatalf("later new episode delivery = %v, want user 1", got)
	}
	for range 2 {
		if event := waitRequesterMatch(t, matched); !slices.Equal(event.EpisodeIDs, []int{43}) {
			t.Fatalf("later request match episode evidence = %v, want [43]", event.EpisodeIDs)
		}
	}
}

func TestRequesterMutedEpisodeDoesNotReplayAfterEnabling(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, database, `INSERT INTO users(id,username,password_hash,role) VALUES(1,'requester','','user')`)
	mustExec(t, database, `INSERT INTO notification_prefs(user_id,requester_only,push_enabled) VALUES(1,1,0)`)
	mgr, capture := newNotifierTestGateway(t, database)
	instance := seedPushLibrary(t, database, "sonarr")
	n := NewNotifier(database, mgr, nil)
	n.NotifyNewEpisodes("Requested show", 123, instance, []int{42})
	mustExec(t, database, `UPDATE notification_prefs SET push_enabled=1 WHERE user_id=1`)

	// Muting is recorded per episode and survives a process restart, so the
	// other import witness cannot replay after the user enables notifications.
	n = NewNotifier(database, mgr, nil)
	var calls atomic.Int32
	n.SetRequestMatcher(requestMatcherFunc(func(_ context.Context, event contentnotify.Import, candidates []int64) ([]int64, error) {
		calls.Add(1)
		var out []int64
		for _, user := range candidates {
			if ids, ok := event.RecipientEpisodeIDs[user]; ok && slices.Contains(ids, 43) {
				out = append(out, user)
			}
		}
		return out, nil
	}))
	n.NotifyNewEpisodes("Requested show", 123, instance, []int{42})
	expectNoRequesterAlert(t, capture)
	if calls.Load() != 0 {
		t.Fatal("muted episode reached request matching")
	}
	n.NotifyNewEpisodes("Requested show", 123, instance, []int{43})
	if got := userIDsOf(t, capture.waitForNotification(t)); !slices.Equal(got, []string{"1"}) {
		t.Fatalf("new episode after enabling = %v, want user 1", got)
	}
}

func TestRequesterMixedEpisodeBatchKeepsRecipientMuteScopes(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, database, `INSERT INTO users(id,username,password_hash,role) VALUES(1,'one','','user'),(2,'two','','user')`)
	mustExec(t, database, `INSERT INTO notification_prefs(user_id,requester_only) VALUES(1,1),(2,1)`)
	mgr, capture := newNotifierTestGateway(t, database)
	instance := seedPushLibrary(t, database, "sonarr")
	n := NewNotifier(database, mgr, nil)
	n.recordMutedImport(1, contentnotify.Import{MediaType: "tv", InstanceID: instance, TmdbID: 123, EpisodeIDs: []int{42}}, CategoryNewEpisode)
	n.recordMutedImport(2, contentnotify.Import{MediaType: "tv", InstanceID: instance, TmdbID: 123, EpisodeIDs: []int{43}}, CategoryNewEpisode)
	var calls atomic.Int32
	n.SetRequestMatcher(requestMatcherFunc(func(_ context.Context, event contentnotify.Import, candidates []int64) ([]int64, error) {
		calls.Add(1)
		if !slices.Equal(event.RecipientEpisodeIDs[1], []int{43}) || !slices.Equal(event.RecipientEpisodeIDs[2], []int{42}) {
			t.Errorf("recipient evidence = %v, want user 1 [43], user 2 [42]", event.RecipientEpisodeIDs)
		}
		var out []int64
		// Both users requested episode 42, but only user 2 can hear this
		// witness. Episode 43 in the shared batch must not broaden user 1.
		for _, user := range candidates {
			if slices.Contains(event.RecipientEpisodeIDs[user], 42) {
				out = append(out, user)
			}
		}
		return out, nil
	}))
	n.NotifyNewEpisodes("Requested show", 123, instance, []int{42, 43})
	if got := userIDsOf(t, capture.waitForNotification(t)); !slices.Equal(got, []string{"2"}) {
		t.Fatalf("mixed batch delivery = %v, want only user 2", got)
	}
	if calls.Load() != 2 {
		t.Fatal("recipient evidence was not checked during preparation and delivery")
	}
}

func TestRequesterUpgradeArrivingDuringDeliverySuppressesAvailability(t *testing.T) {
	for _, tc := range []struct {
		name, service string
		completion    func(*Notifier, string)
		upgrade       func(*Notifier, string)
	}{
		{"movie", "radarr", func(n *Notifier, id string) { n.NotifyNewMovie("Requested movie", 123, id) }, func(n *Notifier, id string) { n.NotifyUpgradedMovie("Requested movie", 123, id) }},
		{"tv", "sonarr", func(n *Notifier, id string) { n.NotifyNewEpisodes("Requested show", 123, id, []int{42}) }, func(n *Notifier, id string) { n.NotifyUpgradedEpisodes("Requested show", 123, id, []int{42}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database, err := dbOpen(t)
			if err != nil {
				t.Fatal(err)
			}
			mustExec(t, database, `INSERT INTO users(id,username,password_hash,role) VALUES(1,'requester','','user'),(2,'broad','','user')`)
			mustExec(t, database, `INSERT INTO notification_prefs(user_id,requester_only) VALUES(1,1)`)
			mgr, capture := newNotifierTestGateway(t, database)
			instance := seedPushLibrary(t, database, tc.service)
			n := NewNotifier(database, mgr, nil)
			var calls atomic.Int32
			upgraded := make(chan struct{})
			n.SetRequestMatcher(requestMatcherFunc(func(_ context.Context, _ contentnotify.Import, candidates []int64) ([]int64, error) {
				if calls.Add(1) == 2 {
					tc.upgrade(n, instance)
					close(upgraded)
				}
				return candidates, nil
			}))
			tc.completion(n, instance)
			select {
			case <-upgraded:
			case <-time.After(2 * time.Second):
				t.Fatal("delivery did not recheck request matching")
			}
			expectNoRequesterAlert(t, capture)
		})
	}
}

func waitRequesterMatch(t *testing.T, matched <-chan contentnotify.Import) contentnotify.Import {
	t.Helper()
	select {
	case event := <-matched:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for request matching")
		return contentnotify.Import{}
	}
}

func expectNoRequesterAlert(t *testing.T, capture *notificationCapture) {
	t.Helper()
	select {
	case body := <-capture.ch:
		t.Fatalf("unexpected notification: %v", body)
	case <-time.After(75 * time.Millisecond):
	}
}
