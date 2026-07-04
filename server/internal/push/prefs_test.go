package push

import (
	"reflect"
	"sort"
	"testing"
)

func TestPrefsGetDefaultsForMissingRow(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'alice', '', 'user')")

	store := NewPrefsStore(database)
	got, err := store.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := Prefs{RequestDecision: false, RequestPending: true, NewMovie: true, NewEpisode: true, IssueCreated: true, AgentActionPending: true, PlexAccessRequest: true, PlexInviteSent: true}
	if got != want {
		t.Errorf("default prefs = %+v, want %+v", got, want)
	}
}

func TestPrefsSetThenGet(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'alice', '', 'user')")

	store := NewPrefsStore(database)
	want := Prefs{RequestDecision: true, RequestPending: false, NewMovie: false, NewEpisode: true, PlexAccessRequest: true}
	if err := store.Set(1, want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := store.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != want {
		t.Errorf("after Set, prefs = %+v, want %+v", got, want)
	}

	// Set is an upsert: a second call replaces the row.
	want2 := Prefs{RequestDecision: false, RequestPending: true, NewMovie: true, NewEpisode: false}
	if err := store.Set(1, want2); err != nil {
		t.Fatalf("Set (upsert): %v", err)
	}
	got2, err := store.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got2 != want2 {
		t.Errorf("after upsert, prefs = %+v, want %+v", got2, want2)
	}
}

func TestUsersOptedIntoDefaultBehavior(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// alice: no row (defaults). bob: opts out of new_movie. An admin with no
	// row is opted into request_pending by default; a regular user never is.
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'alice', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (2, 'bob', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (3, 'admin', '', 'admin')")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, new_movie) VALUES (2, 0)")

	store := NewPrefsStore(database)

	// new_movie on by default => alice + admin included, bob excluded.
	got, err := store.usersOptedInto(CategoryNewMovie)
	if err != nil {
		t.Fatalf("usersOptedInto(new_movie): %v", err)
	}
	if !equalIDs(got, []int64{1, 3}) {
		t.Errorf("new_movie opted-in = %v, want [1 3]", got)
	}

	// new_episode on by default and untouched => everyone included.
	got, err = store.usersOptedInto(CategoryNewEpisode)
	if err != nil {
		t.Fatalf("usersOptedInto(new_episode): %v", err)
	}
	if !equalIDs(got, []int64{1, 2, 3}) {
		t.Errorf("new_episode opted-in = %v, want [1 2 3]", got)
	}

	// request_pending: admin-only, on by default => just the admin.
	got, err = store.usersOptedInto(CategoryRequestPending)
	if err != nil {
		t.Fatalf("usersOptedInto(request_pending): %v", err)
	}
	if !equalIDs(got, []int64{3}) {
		t.Errorf("request_pending opted-in = %v, want [3]", got)
	}

	// request_decision: off by default => nobody without an explicit opt-in.
	got, err = store.usersOptedInto(CategoryRequestDecision)
	if err != nil {
		t.Fatalf("usersOptedInto(request_decision): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("request_decision opted-in = %v, want none", got)
	}
}

func TestOptedInSingleUser(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'alice', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (2, 'bob', '', 'user')")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, request_decision) VALUES (1, 1)")

	store := NewPrefsStore(database)

	if !store.optedIn(1, CategoryRequestDecision) {
		t.Error("alice opted into request_decision, want true")
	}
	// bob has no row: request_decision defaults off.
	if store.optedIn(2, CategoryRequestDecision) {
		t.Error("bob has no row, request_decision defaults off, want false")
	}
	// new_movie is on by default for a user without a row.
	if !store.optedIn(2, CategoryNewMovie) {
		t.Error("bob has no row, new_movie defaults on, want true")
	}
	// Unknown category fails closed.
	if store.optedIn(1, "bogus") {
		t.Error("unknown category should be false")
	}
}

func TestUsersOptedIntoUnknownCategory(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	store := NewPrefsStore(database)
	if _, err := store.usersOptedInto("bogus"); err == nil {
		t.Error("expected error for unknown category")
	}
}

// equalIDs compares two id slices order-independently.
func equalIDs(got, want []int64) bool {
	g := append([]int64(nil), got...)
	w := append([]int64(nil), want...)
	sort.Slice(g, func(i, j int) bool { return g[i] < g[j] })
	sort.Slice(w, func(i, j int) bool { return w[i] < w[j] })
	return reflect.DeepEqual(g, w)
}
