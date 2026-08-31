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
	want := Prefs{RequestDecision: false, RequestPending: true, NewMovie: true, NewEpisode: true, NewBook: true, IssueCreated: true, AgentActionPending: true, PlexAccessRequest: true, PlexInviteSent: true, IssueReportUpdate: true, AgentDigest: true, ContentUpgraded: false}
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
	want := Prefs{RequestDecision: true, RequestPending: false, NewMovie: false, NewEpisode: true, NewBook: true, PlexAccessRequest: true, ContentUpgraded: true}
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

// PUSH-010: Admin-category recipient queries remain role-scoped.
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

	// new_movie/new_episode are per-library truth now: the unscoped query
	// refuses them the way it refuses new_book, and the scoped query's
	// empty-instance fallback answers with the legacy unscoped audience.
	if _, err := store.usersOptedInto(CategoryNewMovie); err == nil {
		t.Error("usersOptedInto(new_movie) should refuse; it requires usersOptedIntoNewVideo")
	}
	if _, err := store.usersOptedInto(CategoryNewEpisode); err == nil {
		t.Error("usersOptedInto(new_episode) should refuse; it requires usersOptedIntoNewVideo")
	}

	// new_movie on by default => alice + admin included, bob excluded.
	got, err := store.usersOptedIntoNewVideo(CategoryNewMovie, "radarr", "")
	if err != nil {
		t.Fatalf("usersOptedIntoNewVideo(new_movie, \"\"): %v", err)
	}
	if !equalIDs(got, []int64{1, 3}) {
		t.Errorf("new_movie opted-in = %v, want [1 3]", got)
	}

	// new_episode on by default and untouched => everyone included.
	got, err = store.usersOptedIntoNewVideo(CategoryNewEpisode, "sonarr", "")
	if err != nil {
		t.Fatalf("usersOptedIntoNewVideo(new_episode, \"\"): %v", err)
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

	// content_upgraded: admin-scoped AND off by default => nobody yet.
	got, err = store.usersOptedInto(CategoryContentUpgraded)
	if err != nil {
		t.Fatalf("usersOptedInto(content_upgraded): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("content_upgraded opted-in = %v, want none by default", got)
	}

	// An opted-in regular user stays excluded — the role scope is enforced in
	// SQL, not just by the default — while an opted-in admin is included.
	mustExec(t, database, "UPDATE notification_prefs SET content_upgraded = 1 WHERE user_id = 2")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, content_upgraded) VALUES (3, 1)")
	got, err = store.usersOptedInto(CategoryContentUpgraded)
	if err != nil {
		t.Fatalf("usersOptedInto(content_upgraded): %v", err)
	}
	if !equalIDs(got, []int64{3}) {
		t.Errorf("content_upgraded opted-in = %v, want only the admin [3]", got)
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
	// new_book must go through the instance-scoped query: an unscoped audience
	// would leak book alerts to users without any chaptarr grant.
	if _, err := store.usersOptedInto(CategoryNewBook); err == nil {
		t.Error("expected error for unscoped new_book recipient query")
	}
}

// A new_book audience follows the per-instance assignment that doubles as the
// chaptarr access grant — for admins too, so a household running one library
// per person is never cross-paged ("ready to read" goes to the person who
// will read it). The one exception: an admin with no books assignment at all
// browses Books through the default-instance fallback and keeps hearing every
// instance rather than being silently muted.
func TestUsersOptedIntoNewBookScopesToInstanceAccess(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// admin(1): no books assignment. alice(2): assigned books-a. bob(3):
	// assigned books-a but opted out. carol(4): assigned sibling books-b.
	// dave(5): no chaptarr assignment at all. erin(6): ADMIN assigned
	// books-a — the two-library household case.
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'admin', '', 'admin')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (2, 'alice', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (3, 'bob', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (4, 'carol', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (5, 'dave', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (6, 'erin', '', 'admin')")
	mustExec(t, database, "INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (2, 'chaptarr', 'books-a')")
	mustExec(t, database, "INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (3, 'chaptarr', 'books-a')")
	mustExec(t, database, "INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (4, 'chaptarr', 'books-b')")
	mustExec(t, database, "INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (6, 'chaptarr', 'books-a')")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, new_book) VALUES (3, 0)")

	store := NewPrefsStore(database)

	got, err := store.usersOptedIntoNewBook("books-a")
	if err != nil {
		t.Fatalf("usersOptedIntoNewBook(books-a): %v", err)
	}
	if !equalIDs(got, []int64{1, 2, 6}) {
		t.Errorf("books-a audience = %v, want [1 2 6] (unassigned admin + assigned alice + assigned admin erin)", got)
	}

	// The sibling instance reaches its own assignee (and the unassigned
	// admin) — never books-a's users, and NOT the admin assigned to books-a:
	// an assignment scopes an admin exactly like anyone else.
	got, err = store.usersOptedIntoNewBook("books-b")
	if err != nil {
		t.Fatalf("usersOptedIntoNewBook(books-b): %v", err)
	}
	if !equalIDs(got, []int64{1, 4}) {
		t.Errorf("books-b audience = %v, want [1 4] (unassigned admin + assigned carol)", got)
	}

	// A radarr assignment is not a books assignment: it must neither admit a
	// user to a book audience nor strip an admin of the unassigned fallback.
	mustExec(t, database, "INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (1, 'radarr', 'movies-a')")
	got, err = store.usersOptedIntoNewBook("books-b")
	if err != nil {
		t.Fatalf("usersOptedIntoNewBook(books-b) after radarr row: %v", err)
	}
	if !equalIDs(got, []int64{1, 4}) {
		t.Errorf("books-b audience after radarr row = %v, want [1 4] unchanged", got)
	}

	// An admin who opts out is excluded like anyone else.
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, new_book) VALUES (1, 0)")
	got, err = store.usersOptedIntoNewBook("books-a")
	if err != nil {
		t.Fatalf("usersOptedIntoNewBook(books-a): %v", err)
	}
	if !equalIDs(got, []int64{2, 6}) {
		t.Errorf("books-a audience after admin opt-out = %v, want [2 6]", got)
	}

	// An access GRANT is an assignment too: a granted user joins that
	// instance's audience without holding the pin.
	mustExec(t, database, "INSERT INTO service_instances (id, service_type, name, url, api_key) VALUES ('books-a', 'chaptarr', 'Books A', 'http://books-a', 'k')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (7, 'frank', '', 'user')")
	mustExec(t, database, "INSERT INTO user_instance_grants (user_id, instance_id) VALUES (7, 'books-a')")
	got, err = store.usersOptedIntoNewBook("books-a")
	if err != nil {
		t.Fatalf("usersOptedIntoNewBook(books-a) with grant: %v", err)
	}
	if !equalIDs(got, []int64{2, 6, 7}) {
		t.Errorf("books-a audience with a granted reader = %v, want [2 6 7]", got)
	}
}

// A new_movie/new_episode audience is the users who can SEE the importing
// library — the same visible set request routing and the status chips use:
// pin-or-grant rows, the global default for unpinned users (a pin stays
// exclusive), and the books-style admin fallback (an admin with no rows for
// the type hears every library).
func TestUsersOptedIntoNewVideoScopesToVisibleLibraries(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO service_instances (id, service_type, name, url, api_key, is_default, sort_order) VALUES ('radarr-hd', 'radarr', 'Movies', 'http://hd', 'k', 1, 0)")
	mustExec(t, database, "INSERT INTO service_instances (id, service_type, name, url, api_key, is_default, sort_order) VALUES ('radarr-4k', 'radarr', '4K Movies', 'http://4k', 'k', 0, 1)")
	// alice(1): no rows — the default library only. bob(2): granted 4K
	// beside the default. carol(3): PINNED 4K — exclusive, loses the
	// default. dave(4): opted out of new_movie. erin(5): admin pinned HD —
	// scoped like anyone. frank(6): admin with no rows — hears everything.
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'alice', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (2, 'bob', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (3, 'carol', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (4, 'dave', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (5, 'erin', '', 'admin')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (6, 'frank', '', 'admin')")
	mustExec(t, database, "INSERT INTO user_instance_grants (user_id, instance_id) VALUES (2, 'radarr-4k')")
	mustExec(t, database, "INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (3, 'radarr', 'radarr-4k')")
	mustExec(t, database, "INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (5, 'radarr', 'radarr-hd')")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, new_movie) VALUES (4, 0)")

	store := NewPrefsStore(database)

	got, err := store.usersOptedIntoNewVideo(CategoryNewMovie, "radarr", "radarr-hd")
	if err != nil {
		t.Fatalf("usersOptedIntoNewVideo(hd): %v", err)
	}
	if !equalIDs(got, []int64{1, 2, 5, 6}) {
		t.Errorf("HD audience = %v, want [1 2 5 6] (default users + pinned admin + rowless admin; never the 4K-pinned)", got)
	}

	got, err = store.usersOptedIntoNewVideo(CategoryNewMovie, "radarr", "radarr-4k")
	if err != nil {
		t.Fatalf("usersOptedIntoNewVideo(4k): %v", err)
	}
	if !equalIDs(got, []int64{2, 3, 6}) {
		t.Errorf("4K audience = %v, want [2 3 6] (granted bob + pinned carol + rowless admin)", got)
	}

	// With no explicit global default, the deterministic first-by-sort chain
	// is the default the rowless users hear — matching VisibleInstanceIDs.
	mustExec(t, database, "UPDATE service_instances SET is_default = 0 WHERE id = 'radarr-hd'")
	got, err = store.usersOptedIntoNewVideo(CategoryNewMovie, "radarr", "radarr-hd")
	if err != nil {
		t.Fatalf("usersOptedIntoNewVideo(hd, no flag): %v", err)
	}
	if !equalIDs(got, []int64{1, 2, 5, 6}) {
		t.Errorf("HD audience without the flag = %v, want [1 2 5 6] via the first-by-sort chain", got)
	}

	// A sonarr import never borrows radarr rows: with no sonarr instances
	// configured, only the rowless-admin fallback answers.
	got, err = store.usersOptedIntoNewVideo(CategoryNewEpisode, "sonarr", "sonarr-x")
	if err != nil {
		t.Fatalf("usersOptedIntoNewVideo(sonarr-x): %v", err)
	}
	if !equalIDs(got, []int64{5, 6}) {
		t.Errorf("unknown sonarr audience = %v, want [5 6] (admins with no sonarr rows)", got)
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
