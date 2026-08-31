package request

import (
	"bytes"
	"errors"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// newTwoRadarrTestService builds a Service whose user is granted BOTH of two
// Radarr instances (primary is the global default), returning the store so
// tests can edit grants. This is the HD/4K shape issue #493 asks for.
func newTwoRadarrTestService(t *testing.T, primaryURL, siblingURL string) (*Service, int64, *instance.Store, string, string) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	res, err := database.Exec(
		"INSERT INTO users (username, password_hash, role) VALUES ('requester', '', 'user')",
	)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	uid, _ := res.LastInsertId()

	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	store := instance.NewStore(database, cipher)
	primary := &instance.Instance{ServiceType: "radarr", Name: "Movies", URL: primaryURL, APIKey: "key", IsDefault: true}
	if err := store.Create(primary); err != nil {
		t.Fatalf("create primary radarr: %v", err)
	}
	sibling := &instance.Instance{ServiceType: "radarr", Name: "4K Movies", URL: siblingURL, APIKey: "key"}
	if err := store.Create(sibling); err != nil {
		t.Fatalf("create sibling radarr: %v", err)
	}
	if err := store.SetUserGrants(uid, map[string][]string{
		"radarr": {primary.ID, sibling.ID},
	}); err != nil {
		t.Fatalf("grant both radarrs: %v", err)
	}

	return NewService(database, instance.NewRegistry(store), nil, nil), uid, store, primary.ID, sibling.ID
}

// An explicit instance selection routes the arr add to that library, records
// it on the history row, and leaves the default library untouched.
func TestCreateMovieRequestRoutesToSelectedInstance(t *testing.T) {
	primaryFake := &fakeRadarr{lookupJSON: `[{"title":"Dune","tmdbId":438631,"year":2021}]`}
	siblingFake := &fakeRadarr{lookupJSON: `[{"title":"Dune","tmdbId":438631,"year":2021}]`}
	primarySrv := newFakeRadarrServer(t, primaryFake)
	siblingSrv := newFakeRadarrServer(t, siblingFake)

	s, uid, _, _, siblingID := newTwoRadarrTestService(t, primarySrv.URL, siblingSrv.URL)

	resp, err := s.CreateMediaRequest(uid, &CreateRequest{
		TmdbID:     438631,
		MediaType:  "movie",
		Title:      "Dune",
		InstanceID: siblingID,
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if resp.Status != StatusRequested {
		t.Fatalf("status = %q, want %q", resp.Status, StatusRequested)
	}
	if resp.InstanceID != siblingID {
		t.Fatalf("response instance = %q, want selected %q", resp.InstanceID, siblingID)
	}
	if siblingFake.addBody == nil {
		t.Fatal("selected library never received the add")
	}
	if primaryFake.addBody != nil {
		t.Fatal("default library must not receive an add routed to the sibling")
	}

	var stored string
	if err := s.db.QueryRow(
		"SELECT COALESCE(instance_id, '') FROM request_log WHERE user_id = ? AND tmdb_id = 438631", uid,
	).Scan(&stored); err != nil {
		t.Fatalf("read history row: %v", err)
	}
	if stored != siblingID {
		t.Fatalf("history instance_id = %q, want %q", stored, siblingID)
	}
}

// A selection outside the user's granted set is refused before anything is
// written — the request must not silently fall back to the default library.
func TestCreateMovieRequestRefusesForbiddenInstance(t *testing.T) {
	primaryFake := &fakeRadarr{}
	siblingFake := &fakeRadarr{}
	primarySrv := newFakeRadarrServer(t, primaryFake)
	siblingSrv := newFakeRadarrServer(t, siblingFake)
	s, _, store, primaryID, siblingID := newTwoRadarrTestService(t, primarySrv.URL, siblingSrv.URL)

	// A second requester holds only the default library.
	res, err := s.db.Exec("INSERT INTO users (username, password_hash, role) VALUES ('narrow', '', 'user')")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	narrow, _ := res.LastInsertId()
	if err := store.SetUserGrants(narrow, map[string][]string{"radarr": {primaryID}}); err != nil {
		t.Fatalf("grant default only: %v", err)
	}

	for _, selection := range []string{siblingID, "not-a-real-instance"} {
		if _, err := s.CreateMediaRequest(narrow, &CreateRequest{
			TmdbID: 1, MediaType: "movie", Title: "X", InstanceID: selection,
		}); !errors.Is(err, ErrArrInstanceForbidden) {
			t.Fatalf("selection %q = %v, want ErrArrInstanceForbidden", selection, err)
		}
	}

	var rows int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM request_log WHERE user_id = ?", narrow).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 0 {
		t.Fatalf("refused requests wrote %d history rows, want 0", rows)
	}
	if primaryFake.addBody != nil || siblingFake.addBody != nil {
		t.Fatal("refused requests must not touch any arr")
	}
}

// The duplicate guard is per target library: the same title may be pending on
// two instances at once, while a re-submit to the same library still dedupes,
// and legacy NULL rows are absorbed only by the default library.
func TestPendingDedupePerInstance(t *testing.T) {
	primaryFake := &fakeRadarr{}
	siblingFake := &fakeRadarr{}
	primarySrv := newFakeRadarrServer(t, primaryFake)
	siblingSrv := newFakeRadarrServer(t, siblingFake)
	s, uid, _, primaryID, siblingID := newTwoRadarrTestService(t, primarySrv.URL, siblingSrv.URL)
	requireApproval(t, s)

	countPending := func() int {
		t.Helper()
		var n int
		if err := s.db.QueryRow(
			"SELECT COUNT(*) FROM request_log WHERE user_id = ? AND tmdb_id = 550 AND status = ?", uid, StatusPending,
		).Scan(&n); err != nil {
			t.Fatalf("count pending: %v", err)
		}
		return n
	}
	submit := func(instanceID string) {
		t.Helper()
		if _, err := s.CreateMediaRequest(uid, &CreateRequest{
			TmdbID: 550, MediaType: "movie", Title: "Fight Club", InstanceID: instanceID,
		}); err != nil {
			t.Fatalf("CreateMediaRequest(%q): %v", instanceID, err)
		}
	}

	// Default, then sibling: two distinct pending rows. Re-submits dedupe.
	submit("")
	if got := countPending(); got != 1 {
		t.Fatalf("pending after default submit = %d, want 1", got)
	}
	submit(siblingID)
	if got := countPending(); got != 2 {
		t.Fatalf("pending after sibling submit = %d, want 2 (distinct libraries)", got)
	}
	submit(siblingID)
	submit(primaryID)
	if got := countPending(); got != 2 {
		t.Fatalf("pending after re-submits = %d, want still 2", got)
	}

	// A legacy pending row (no stamped instance) is absorbed by a default-
	// library submit but never blocks a sibling one.
	if _, err := s.db.Exec(
		"INSERT INTO request_log (user_id, tmdb_id, media_type, title, status) VALUES (?, 42, 'movie', 'Legacy', ?)",
		uid, StatusPending,
	); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	if _, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 42, MediaType: "movie", Title: "Legacy"}); err != nil {
		t.Fatalf("default re-submit over legacy: %v", err)
	}
	var legacyRows int
	if err := s.db.QueryRow(
		"SELECT COUNT(*) FROM request_log WHERE user_id = ? AND tmdb_id = 42 AND status = ?", uid, StatusPending,
	).Scan(&legacyRows); err != nil {
		t.Fatalf("count legacy pending: %v", err)
	}
	if legacyRows != 1 {
		t.Fatalf("default submit over a legacy row = %d pending, want absorbed into 1", legacyRows)
	}
	if _, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 42, MediaType: "movie", Title: "Legacy", InstanceID: siblingID}); err != nil {
		t.Fatalf("sibling submit over legacy: %v", err)
	}
	if err := s.db.QueryRow(
		"SELECT COUNT(*) FROM request_log WHERE user_id = ? AND tmdb_id = 42 AND status = ?", uid, StatusPending,
	).Scan(&legacyRows); err != nil {
		t.Fatalf("count legacy pending: %v", err)
	}
	if legacyRows != 2 {
		t.Fatalf("sibling submit over a legacy row = %d pending, want a distinct 2nd row", legacyRows)
	}
}

// Approval replays the add on the library stored at submission — under the
// approving admin's authority, so revoking the requester's grant between
// submission and approval cannot reroute the request to a library the admin
// never saw on the queue row.
func TestApproveRequestReplaysStoredInstance(t *testing.T) {
	primaryFake := &fakeRadarr{}
	siblingFake := &fakeRadarr{lookupJSON: `[{"title":"Dune","tmdbId":438631,"year":2021}]`}
	primarySrv := newFakeRadarrServer(t, primaryFake)
	siblingSrv := newFakeRadarrServer(t, siblingFake)
	s, uid, store, primaryID, siblingID := newTwoRadarrTestService(t, primarySrv.URL, siblingSrv.URL)
	requireApproval(t, s)
	adminID := createTestAdmin(t, s)

	if _, err := s.CreateMediaRequest(uid, &CreateRequest{
		TmdbID: 438631, MediaType: "movie", Title: "Dune", InstanceID: siblingID,
	}); err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	pending, err := s.ListPending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("ListPending = %v, %v; want 1 row", pending, err)
	}
	if pending[0].InstanceID != siblingID || pending[0].InstanceName != "4K Movies" {
		t.Fatalf("queue row library = (%q, %q), want (%q, 4K Movies)", pending[0].InstanceID, pending[0].InstanceName, siblingID)
	}

	// The requester loses the sibling grant before the decision; the stored
	// routing must still hold under the admin's authority.
	if err := store.SetUserGrants(uid, map[string][]string{"radarr": {primaryID}}); err != nil {
		t.Fatalf("revoke sibling grant: %v", err)
	}

	if _, err := s.ApproveRequest(adminID, pending[0].ID, nil); err != nil {
		t.Fatalf("ApproveRequest: %v", err)
	}
	if siblingFake.addBody == nil {
		t.Fatal("approval never added on the stored library")
	}
	if primaryFake.addBody != nil {
		t.Fatal("approval must not fall back to the default library")
	}
	var stored, status string
	if err := s.db.QueryRow(
		"SELECT COALESCE(instance_id, ''), status FROM request_log WHERE id = ?", pending[0].ID,
	).Scan(&stored, &status); err != nil {
		t.Fatalf("read decided row: %v", err)
	}
	if stored != siblingID || status != StatusRequested {
		t.Fatalf("decided row = (%q, %q), want (%q, %q)", stored, status, siblingID, StatusRequested)
	}
}

// The status endpoint reads the selected library, never absorbs a sibling's
// pending rows into an explicit selection, and — because the user holds two
// granted libraries — carries a digest-grade status chip per library, with
// each library's own pending request surfacing in its chip.
func TestGetUserStatusPerInstance(t *testing.T) {
	// The title is on disk in the default library and absent from the sibling.
	primaryFake := &fakeRadarr{libraryJSON: `[{"id":9,"tmdbId":550,"title":"Fight Club","hasFile":true,"monitored":true}]`}
	siblingFake := &fakeRadarr{}
	primarySrv := newFakeRadarrServer(t, primaryFake)
	siblingSrv := newFakeRadarrServer(t, siblingFake)
	s, uid, store, primaryID, siblingID := newTwoRadarrTestService(t, primarySrv.URL, siblingSrv.URL)
	requireApproval(t, s)

	// A pending request on the sibling library only.
	if _, err := s.CreateMediaRequest(uid, &CreateRequest{
		TmdbID: 550, MediaType: "movie", Title: "Fight Club", InstanceID: siblingID,
	}); err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}

	// Default selection: the sibling's pending row must not bleed into the
	// default library's headline, which reads the live file on disk.
	resp, err := s.GetUserStatus(uid, 550, "movie", "")
	if err != nil {
		t.Fatalf("GetUserStatus(default): %v", err)
	}
	if resp.Status != StatusAvailable {
		t.Fatalf("default headline = %q, want %q (sibling pending must not bleed)", resp.Status, StatusAvailable)
	}
	if len(resp.InstanceStatuses) != 2 {
		t.Fatalf("instance_statuses = %+v, want both granted libraries", resp.InstanceStatuses)
	}
	if got := resp.InstanceStatuses[primaryID].Status; got != StatusAvailable {
		t.Fatalf("primary chip = %q, want %q", got, StatusAvailable)
	}
	if got := resp.InstanceStatuses[siblingID].Status; got != StatusPending {
		t.Fatalf("sibling chip = %q, want %q (its own pending request)", got, StatusPending)
	}

	// Explicit sibling selection: the pending row owns the headline.
	resp, err = s.GetUserStatus(uid, 550, "movie", siblingID)
	if err != nil {
		t.Fatalf("GetUserStatus(sibling): %v", err)
	}
	if resp.Status != StatusPending {
		t.Fatalf("sibling headline = %q, want %q", resp.Status, StatusPending)
	}

	// A selection outside the granted set is refused.
	if err := store.SetUserGrants(uid, map[string][]string{"radarr": {primaryID}}); err != nil {
		t.Fatalf("revoke sibling: %v", err)
	}
	if _, err := s.GetUserStatus(uid, 550, "movie", siblingID); !errors.Is(err, ErrArrInstanceForbidden) {
		t.Fatalf("revoked selection = %v, want ErrArrInstanceForbidden", err)
	}
	// Down to one granted library, the chips disappear.
	resp, err = s.GetUserStatus(uid, 550, "movie", "")
	if err != nil {
		t.Fatalf("GetUserStatus(single grant): %v", err)
	}
	if resp.InstanceStatuses != nil {
		t.Fatalf("instance_statuses with one grant = %+v, want omitted", resp.InstanceStatuses)
	}
}

// History rows overlay live state from the library stamped on each row, not
// from one shared default digest.
func TestHistoryOverlayFollowsRowInstance(t *testing.T) {
	// The sibling library has the file; the default library has never heard of
	// the title.
	primaryFake := &fakeRadarr{}
	siblingFake := &fakeRadarr{libraryJSON: `[{"id":9,"tmdbId":550,"title":"Fight Club","hasFile":true,"monitored":true}]`}
	primarySrv := newFakeRadarrServer(t, primaryFake)
	siblingSrv := newFakeRadarrServer(t, siblingFake)
	s, uid, _, _, siblingID := newTwoRadarrTestService(t, primarySrv.URL, siblingSrv.URL)

	if _, err := s.db.Exec(
		"INSERT INTO request_log (user_id, tmdb_id, instance_id, media_type, title, status) VALUES (?, 550, ?, 'movie', 'Fight Club', ?)",
		uid, siblingID, StatusRequested,
	); err != nil {
		t.Fatalf("insert fulfilled row: %v", err)
	}

	requests, err := s.GetRequests(uid)
	if err != nil {
		t.Fatalf("GetRequests: %v", err)
	}
	if got := statusOf(t, requests, "Fight Club"); got != StatusAvailable {
		t.Fatalf("row status = %q, want %q from the row's own library", got, StatusAvailable)
	}
}

// Request options scope quality profiles to the selected library, and refuse a
// selection outside the granted set instead of answering with the default's
// profiles.
func TestRequestOptionsProfilesFollowSelectedInstance(t *testing.T) {
	primaryFake := &fakeRadarr{}
	primarySrv := newFakeRadarrServer(t, primaryFake)
	siblingSrv := jsonServer(t, map[string]string{
		"/api/v3/qualityprofile": `[{"id":42,"name":"4K Remux"}]`,
	})
	s, uid, store, _, siblingID := newTwoRadarrTestService(t, primarySrv.URL, siblingSrv.URL)
	if err := s.SetGlobalSettings(GlobalSettings{
		AllowSeasonChoice:  true,
		DefaultSeasonScope: SeasonScopeAll,
		AllowQualityChoice: true,
	}); err != nil {
		t.Fatalf("SetGlobalSettings: %v", err)
	}

	opts, err := s.GetRequestOptions(uid, false, "movie", siblingID)
	if err != nil {
		t.Fatalf("GetRequestOptions(sibling): %v", err)
	}
	if len(opts.QualityProfiles) != 1 || opts.QualityProfiles[0].ID != 42 || opts.QualityProfiles[0].Name != "4K Remux" {
		t.Fatalf("profiles = %+v, want the sibling's [42 4K Remux]", opts.QualityProfiles)
	}

	// Default (no selection) answers with the default library's profiles.
	opts, err = s.GetRequestOptions(uid, false, "movie", "")
	if err != nil {
		t.Fatalf("GetRequestOptions(default): %v", err)
	}
	if len(opts.QualityProfiles) != 2 {
		t.Fatalf("default profiles = %+v, want the fakeRadarr pair", opts.QualityProfiles)
	}

	// Revoke the sibling: the same selection is now refused outright.
	if err := store.SetUserGrants(uid, map[string][]string{"radarr": nil}); err != nil {
		t.Fatalf("clear grants: %v", err)
	}
	if _, err := s.GetRequestOptions(uid, false, "movie", siblingID); !errors.Is(err, ErrArrInstanceForbidden) {
		t.Fatalf("revoked selection = %v, want ErrArrInstanceForbidden", err)
	}
}
