package request

import (
	"testing"
)

// recordingNotifier records notifier events, mirroring the fake in
// internal/push/composite_test.go but keeping the payloads for assertions.
type recordingNotifier struct {
	userEvents  []notifierEvent
	adminEvents []notifierEvent
}

type notifierEvent struct {
	userID    int64
	eventType string
	data      map[string]interface{}
}

func (r *recordingNotifier) NotifyUser(userID int64, eventType string, data map[string]interface{}) {
	r.userEvents = append(r.userEvents, notifierEvent{userID: userID, eventType: eventType, data: data})
}

func (r *recordingNotifier) NotifyAdmins(eventType string, data map[string]interface{}) {
	r.adminEvents = append(r.adminEvents, notifierEvent{eventType: eventType, data: data})
}

// createTestAdmin inserts an admin user (decisions record approved_by, which
// references users.id) and returns its id.
func createTestAdmin(t *testing.T, s *Service) int64 {
	t.Helper()
	res, err := s.db.Exec("INSERT INTO users (username, password_hash, role) VALUES ('boss', '', 'admin')")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	adminID, _ := res.LastInsertId()
	return adminID
}

// requireApproval flips the global request policy to approval-required.
func requireApproval(t *testing.T, s *Service) {
	t.Helper()
	if err := s.SetGlobalSettings(GlobalSettings{
		RequireApproval:    true,
		AllowSeasonChoice:  true,
		DefaultSeasonScope: SeasonScopeAll,
	}); err != nil {
		t.Fatalf("SetGlobalSettings: %v", err)
	}
}

// TestCreateMediaRequestPendingDedupe covers the approval-required movie/TV
// path: the request queues as pending without touching any arr, an identical
// re-submit is deduped (one row, one admin notification), and a denied row
// does NOT block a fresh request — the user may re-request after a denial,
// creating a new pending row alongside the denied one.
func TestCreateMediaRequestPendingDedupe(t *testing.T) {
	s, uid := newHistoryTestService(t, "", "", "") // no arrs: pending must not need them
	rec := &recordingNotifier{}
	s.notifier = rec
	requireApproval(t, s)
	adminID := createTestAdmin(t, s)

	resp, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 550, MediaType: "movie", Title: "Fight Club"})
	if err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if resp.Status != StatusPending || resp.Title != "Fight Club" {
		t.Fatalf("response = %+v, want pending/Fight Club", resp)
	}
	if len(rec.adminEvents) != 1 || rec.adminEvents[0].eventType != "request_pending" {
		t.Fatalf("admin events = %+v, want one request_pending", rec.adminEvents)
	}
	if got := rec.adminEvents[0].data["pending_count"]; got != 1 {
		t.Errorf("pending_count = %v, want 1", got)
	}

	// Exact duplicate: same status back, no second row, no second notification.
	resp, err = s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 550, MediaType: "movie", Title: "Fight Club"})
	if err != nil {
		t.Fatalf("CreateMediaRequest dup: %v", err)
	}
	if resp.Status != StatusPending {
		t.Fatalf("dup status = %s, want pending", resp.Status)
	}
	countRows := func(status string) int {
		t.Helper()
		var n int
		if err := s.db.QueryRow(
			"SELECT COUNT(*) FROM request_log WHERE user_id = ? AND tmdb_id = 550 AND media_type = 'movie' AND status = ?",
			uid, status,
		).Scan(&n); err != nil {
			t.Fatalf("count %s rows: %v", status, err)
		}
		return n
	}
	if got := countRows(StatusPending); got != 1 {
		t.Fatalf("pending rows after dup = %d, want 1", got)
	}
	if len(rec.adminEvents) != 1 {
		t.Errorf("admin events after dup = %d, want still 1 (no duplicate notify)", len(rec.adminEvents))
	}

	// Deny it, then re-request: the denied row must not dedupe-block a fresh
	// pending row (denial is re-requestable).
	pending, err := s.ListPending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("ListPending = %+v err=%v, want exactly 1", pending, err)
	}
	if pending[0].UserID != uid || pending[0].TmdbID != 550 || pending[0].Username != "requester" {
		t.Errorf("pending row = %+v, want requester's tmdb 550", pending[0])
	}
	if err := s.DenyRequest(adminID, pending[0].ID, "not now"); err != nil {
		t.Fatalf("DenyRequest: %v", err)
	}

	resp, err = s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 550, MediaType: "movie", Title: "Fight Club"})
	if err != nil {
		t.Fatalf("CreateMediaRequest after deny: %v", err)
	}
	if resp.Status != StatusPending {
		t.Fatalf("re-request status = %s, want pending", resp.Status)
	}
	if got := countRows(StatusPending); got != 1 {
		t.Errorf("pending rows after re-request = %d, want 1", got)
	}
	if got := countRows(StatusDenied); got != 1 {
		t.Errorf("denied rows after re-request = %d, want 1 (kept alongside the new pending)", got)
	}
}

// TestCreateTVRequestPendingCachesTvdbID: a pending TV request stores the
// supplied TVDB id mapping so status checks resolve while it waits, and the
// resolved season scope rides along for the approval queue.
func TestCreateTVRequestPendingCachesTvdbID(t *testing.T) {
	s, uid := newHistoryTestService(t, "", "", "")
	requireApproval(t, s)

	resp, err := s.CreateMediaRequest(uid, &CreateRequest{
		TmdbID: 1399, TvdbID: 121361, MediaType: "tv", Title: "Andor",
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if resp.Status != StatusPending {
		t.Fatalf("status = %s, want pending", resp.Status)
	}

	var cachedTvdb int
	if err := s.db.QueryRow("SELECT tvdb_id FROM tmdb_tvdb_cache WHERE tmdb_id = 1399").Scan(&cachedTvdb); err != nil {
		t.Fatalf("read tmdb_tvdb_cache: %v", err)
	}
	if cachedTvdb != 121361 {
		t.Errorf("cached tvdb_id = %d, want 121361", cachedTvdb)
	}

	pending, err := s.ListPending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("ListPending = %+v err=%v, want exactly 1", pending, err)
	}
	if pending[0].TvdbID != 121361 || pending[0].SeasonScope != SeasonScopeAll {
		t.Errorf("pending row = %+v, want tvdb 121361 + season scope all", pending[0])
	}
}

// TestApproveRequestPerformsArrAdd covers the approve transition: the arr add
// actually runs (Radarr receives the movie), the row flips pending ->
// requested with the canonical title and decision audit fields, the requester
// is notified, and a second approve of the same row fails.
func TestApproveRequestPerformsArrAdd(t *testing.T) {
	f := &fakeRadarr{
		lookupJSON: `[{"title":"Fight Club","tmdbId":550,"year":1999}]`,
	}
	srv := newFakeRadarrServer(t, f)

	s, uid := newHistoryTestService(t, srv.URL, "", "")
	rec := &recordingNotifier{}
	s.notifier = rec
	requireApproval(t, s)
	adminID := createTestAdmin(t, s)

	if _, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 550, MediaType: "movie", Title: "fight club (client title)"}); err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if f.addBody != nil {
		t.Fatal("pending request must not touch Radarr before approval")
	}
	pending, err := s.ListPending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("ListPending = %+v err=%v, want exactly 1", pending, err)
	}

	resp, err := s.ApproveRequest(adminID, pending[0].ID, nil)
	if err != nil {
		t.Fatalf("ApproveRequest: %v", err)
	}
	if !resp.Success || resp.Status != StatusRequested || resp.Title != "Fight Club" {
		t.Fatalf("response = %+v, want requested/Fight Club", resp)
	}
	if f.addBody == nil {
		t.Fatal("approval did not add the movie to Radarr")
	}
	if f.addBody["tmdbId"] != float64(550) || f.addBody["qualityProfileId"] != float64(1) {
		t.Errorf("add body = %#v, want tmdbId 550 with first-profile fallback (no stored profile)", f.addBody)
	}

	var status, title string
	var approvedBy int64
	var decidedAt any
	if err := s.db.QueryRow(
		"SELECT status, title, approved_by, decided_at FROM request_log WHERE id = ?", pending[0].ID,
	).Scan(&status, &title, &approvedBy, &decidedAt); err != nil {
		t.Fatalf("read decided row: %v", err)
	}
	if status != StatusRequested || title != "Fight Club" || approvedBy != adminID || decidedAt == nil {
		t.Errorf("decided row = %s/%s by %d at %v, want requested/Fight Club by admin with a decided_at",
			status, title, approvedBy, decidedAt)
	}

	if len(rec.userEvents) != 1 {
		t.Fatalf("user events = %+v, want exactly one decision", rec.userEvents)
	}
	ev := rec.userEvents[0]
	if ev.userID != uid || ev.eventType != "request_decision" {
		t.Errorf("event = %+v, want request_decision to the requester", ev)
	}
	if ev.data["decision"] != "approved" || ev.data["status"] != StatusRequested || ev.data["title"] != "Fight Club" {
		t.Errorf("event data = %#v, want approved/requested/Fight Club", ev.data)
	}

	// The row is no longer pending: a second decision must be rejected.
	if _, err := s.ApproveRequest(adminID, pending[0].ID, nil); err == nil {
		t.Error("second ApproveRequest succeeded, want 'request is not pending' error")
	}
}

// TestApproveRequestQualityOverride: an admin quality override replaces the
// stored profile in both the arr payload and the decided row.
func TestApproveRequestQualityOverride(t *testing.T) {
	f := &fakeRadarr{
		lookupJSON: `[{"title":"Fight Club","tmdbId":550,"year":1999}]`,
	}
	srv := newFakeRadarrServer(t, f)

	s, uid := newHistoryTestService(t, srv.URL, "", "")
	requireApproval(t, s)
	adminID := createTestAdmin(t, s)

	if _, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 550, MediaType: "movie", Title: "Fight Club"}); err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	pending, _ := s.ListPending()
	if len(pending) != 1 {
		t.Fatalf("pending = %+v, want exactly 1", pending)
	}

	if _, err := s.ApproveRequest(adminID, pending[0].ID, &DecisionOverride{QualityProfileID: 7}); err != nil {
		t.Fatalf("ApproveRequest: %v", err)
	}
	if got := f.addBody["qualityProfileId"]; got != float64(7) {
		t.Errorf("qualityProfileId = %v, want the override (7)", got)
	}
	var storedProfile int
	if err := s.db.QueryRow("SELECT quality_profile_id FROM request_log WHERE id = ?", pending[0].ID).Scan(&storedProfile); err != nil {
		t.Fatalf("read quality_profile_id: %v", err)
	}
	if storedProfile != 7 {
		t.Errorf("stored quality_profile_id = %d, want 7", storedProfile)
	}
}

// TestApproveRequestSeasonScopeOverrideReplacesExplicitSeasons: an admin
// choosing a coarse scope on approval discards the requester's explicit season
// list — the add uses addOptions.monitor, not per-season flags — and the
// decided row stores the coarse scope.
func TestApproveRequestSeasonScopeOverrideReplacesExplicitSeasons(t *testing.T) {
	f := &fakeSonarrTV{
		lookupJSON: `[{"title":"Andor","tvdbId":121361,"year":2022,"seasons":[
			{"seasonNumber":1},{"seasonNumber":2},{"seasonNumber":3}]}]`,
	}
	srv := newFakeSonarrServer(t, f)

	s, uid := newHistoryTestService(t, "", srv.URL, "")
	requireApproval(t, s)
	adminID := createTestAdmin(t, s)

	if _, err := s.CreateMediaRequest(uid, &CreateRequest{
		TmdbID: 1399, TvdbID: 121361, MediaType: "tv", Title: "Andor", Seasons: []int{2, 3},
	}); err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	pending, _ := s.ListPending()
	if len(pending) != 1 {
		t.Fatalf("pending = %+v, want exactly 1", pending)
	}
	if pending[0].SeasonScope != "[2,3]" {
		t.Fatalf("stored season_scope = %q, want the explicit list [2,3]", pending[0].SeasonScope)
	}

	resp, err := s.ApproveRequest(adminID, pending[0].ID, &DecisionOverride{SeasonScope: SeasonScopeFirst})
	if err != nil {
		t.Fatalf("ApproveRequest: %v", err)
	}
	if resp.Status != StatusRequested || resp.Title != "Andor" {
		t.Fatalf("response = %+v, want requested/Andor", resp)
	}

	addOptions, _ := f.addBody["addOptions"].(map[string]any)
	if addOptions["monitor"] != "firstSeason" {
		t.Errorf("addOptions.monitor = %v, want firstSeason (override replaces the explicit list)", addOptions["monitor"])
	}
	if _, present := f.addBody["seasons"]; present {
		t.Errorf("seasons = %v, want omitted once the explicit list is overridden", f.addBody["seasons"])
	}
	var storedScope string
	if err := s.db.QueryRow("SELECT season_scope FROM request_log WHERE id = ?", pending[0].ID).Scan(&storedScope); err != nil {
		t.Fatalf("read season_scope: %v", err)
	}
	if storedScope != SeasonScopeFirst {
		t.Errorf("stored season_scope = %q, want first", storedScope)
	}
}

// TestApproveRequestArrFailureLeavesPending: when the arr add fails the row
// must stay pending (so the admin can retry after fixing config) and the
// requester must not be notified of a decision.
func TestApproveRequestArrFailureLeavesPending(t *testing.T) {
	s, uid := newHistoryTestService(t, "", "", "") // no radarr: the add must fail
	rec := &recordingNotifier{}
	s.notifier = rec
	requireApproval(t, s)
	adminID := createTestAdmin(t, s)

	if _, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 550, MediaType: "movie", Title: "Fight Club"}); err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	pending, _ := s.ListPending()
	if len(pending) != 1 {
		t.Fatalf("pending = %+v, want exactly 1", pending)
	}

	if _, err := s.ApproveRequest(adminID, pending[0].ID, nil); err == nil {
		t.Fatal("ApproveRequest succeeded without a configured Radarr, want error")
	}
	var status string
	if err := s.db.QueryRow("SELECT status FROM request_log WHERE id = ?", pending[0].ID).Scan(&status); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if status != StatusPending {
		t.Errorf("status after failed approve = %s, want still pending (admin can retry)", status)
	}
	if len(rec.userEvents) != 0 {
		t.Errorf("user events = %+v, want none for a failed approval", rec.userEvents)
	}
}

// TestDenyRequest covers the deny transition: the row flips pending -> denied
// with the reason and audit fields, the requester is notified with the reason,
// the user-facing status reads denied, and a second decision is rejected.
func TestDenyRequest(t *testing.T) {
	s, uid := newHistoryTestService(t, "", "", "")
	rec := &recordingNotifier{}
	s.notifier = rec
	requireApproval(t, s)
	adminID := createTestAdmin(t, s)

	if _, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 550, MediaType: "movie", Title: "Fight Club"}); err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	pending, _ := s.ListPending()
	if len(pending) != 1 {
		t.Fatalf("pending = %+v, want exactly 1", pending)
	}

	if err := s.DenyRequest(adminID, pending[0].ID, "library full"); err != nil {
		t.Fatalf("DenyRequest: %v", err)
	}

	var status, reason string
	var approvedBy int64
	if err := s.db.QueryRow(
		"SELECT status, COALESCE(deny_reason, ''), approved_by FROM request_log WHERE id = ?", pending[0].ID,
	).Scan(&status, &reason, &approvedBy); err != nil {
		t.Fatalf("read denied row: %v", err)
	}
	if status != StatusDenied || reason != "library full" || approvedBy != adminID {
		t.Errorf("denied row = %s/%q by %d, want denied/library full by admin", status, reason, approvedBy)
	}

	if len(rec.userEvents) != 1 {
		t.Fatalf("user events = %+v, want exactly one decision", rec.userEvents)
	}
	ev := rec.userEvents[0]
	if ev.userID != uid || ev.data["decision"] != "denied" || ev.data["reason"] != "library full" {
		t.Errorf("event = %+v, want denied with the reason", ev)
	}
	// foreign_id is book-only: a movie decision payload must not carry it.
	if v, ok := ev.data["foreign_id"]; ok {
		t.Errorf("event foreign_id = %v, want absent for a movie decision", v)
	}

	// The requester's own status now reads denied (nothing landed in any arr).
	if st, err := s.GetUserStatus(uid, 550, "movie"); err != nil || st.Status != StatusDenied {
		t.Errorf("GetUserStatus = %+v err=%v, want denied", st, err)
	}

	// Already decided: a second deny is rejected.
	if err := s.DenyRequest(adminID, pending[0].ID, "again"); err == nil {
		t.Error("second DenyRequest succeeded, want 'request is not pending' error")
	}
}
