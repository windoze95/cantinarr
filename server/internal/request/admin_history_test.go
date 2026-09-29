package request

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

func seedAdminHistory(t *testing.T) (*Service, int64, int64) {
	t.Helper()
	s, uid := newHistoryTestService(t, "", "", "")
	res, err := s.db.Exec(`INSERT INTO users (username,password_hash,role) VALUES ('reviewer','','admin')`)
	if err != nil {
		t.Fatal(err)
	}
	admin, _ := res.LastInsertId()
	return s, uid, admin
}

func historyInsert(t *testing.T, s *Service, user int64, media, title, status, park, reason string, reviewer any) int64 {
	t.Helper()
	res, err := s.db.Exec(`INSERT INTO request_log (user_id,tmdb_id,media_type,title,status,park_reason,deny_reason,approved_by,requested_at)
 VALUES (?,0,?,?,?,?,?,?, '2026-09-25 12:00:00')`, user, media, title, status, sqlNullStr(park), sqlNullStr(reason), reviewer)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestAdminHistoryDecisionsDoNotUseAvailability(t *testing.T) {
	s, uid, admin := seedAdminHistory(t)
	for _, tc := range []struct {
		title, media, stored, park, reason, decision string
		reviewer                                     any
	}{
		{"Policy hold", "movie", "pending", "", "", "pending", nil},
		{"Approved awaiting delivery", "tv", "pending", "delivery", "", "approved", admin},
		{"Automatic delivery", "music", "pending", "delivery", "", "approved", nil},
		{"Author importing", "book", "pending", "author_import", "", "approved", nil},
		{"Imported", "movie", "available", "", "", "approved", nil},
		{"Removed", "tv", "unavailable", "", "", "approved", admin},
		{"Declined", "book", "denied", "", "Already have a copy", "denied", admin},
		{"Cancelled", "music", "denied", "", "Cancelled", "cancelled", nil},
		{"Unknown park", "book", "pending", "future_reason", "", "pending", nil},
		{"Reviewed retry", "movie", "pending", "", "", "approved", admin},
		{"Future status", "movie", "future", "", "", "unknown", nil},
	} {
		historyInsert(t, s, uid, tc.media, tc.title, tc.stored, tc.park, tc.reason, tc.reviewer)
		t.Run(tc.title, func(t *testing.T) {
			page, err := s.adminHistory(context.Background(), adminHistoryFilter{Query: tc.title, Limit: 50})
			if err != nil || len(page.Requests) != 1 {
				t.Fatalf("history = %+v, %v", page, err)
			}
			item := page.Requests[0]
			if item.Decision != tc.decision || (item.DecidedBy == "reviewer") != (tc.reviewer != nil) {
				t.Fatalf("decision = %+v", item)
			}
			if tc.reason != item.DenyReason || item.Requesters[0].Username != "requester" {
				t.Fatalf("record metadata lost: %+v", item)
			}
		})
	}
}

func TestAdminHistoryPaginationAndCombinedFilters(t *testing.T) {
	s, uid, admin := seedAdminHistory(t)
	a := historyInsert(t, s, uid, "movie", "Match 100%", "requested", "", "", nil)
	b := historyInsert(t, s, uid, "movie", "Match 100%", "denied", "", "No", admin)
	c := historyInsert(t, s, admin, "book", "Match 100%", "pending", "", "", nil)
	first, err := s.adminHistory(context.Background(), adminHistoryFilter{Limit: 2})
	if err != nil || len(first.Requests) != 2 || first.NextBefore != b || first.Requests[0].ID != c {
		t.Fatalf("first page = %+v, %v", first, err)
	}
	// An insertion and deletion between pages cannot shift a keyset boundary.
	historyInsert(t, s, uid, "tv", "New request", "pending", "", "", nil)
	if _, err = s.db.Exec(`DELETE FROM request_log WHERE id=?`, b); err != nil {
		t.Fatal(err)
	}
	second, err := s.adminHistory(context.Background(), adminHistoryFilter{Before: first.NextBefore, Limit: 2})
	if err != nil || len(second.Requests) != 1 || second.Requests[0].ID != a || second.NextBefore != 0 {
		t.Fatalf("second page = %+v, %v", second, err)
	}
	filtered, err := s.adminHistory(context.Background(), adminHistoryFilter{Query: "100%", UserID: uid, MediaType: "movie", Decision: "approved", Limit: 10})
	if err != nil || len(filtered.Requests) != 1 || filtered.Requests[0].ID != a || len(filtered.Requesters) != 2 {
		t.Fatalf("combined filters = %+v, %v", filtered, err)
	}
	empty, err := s.adminHistory(context.Background(), adminHistoryFilter{Query: "%_", Limit: 10})
	if err != nil || len(empty.Requests) != 0 || empty.Requests == nil {
		t.Fatalf("literal wildcard query = %+v, %v", empty, err)
	}
}

func TestAdminHistoryIncludesSharedBookRequestersWithoutMergingRows(t *testing.T) {
	s, uid, admin := seedAdminHistory(t)
	id := historyInsert(t, s, uid, "book", "Shared", "pending", "", "", nil)
	if _, err := s.db.Exec(`INSERT INTO book_request_waiters(request_id,user_id,book_format) VALUES (?,?, 'audiobook'),(?,?,'ebook')`, id, admin, id, uid); err != nil {
		t.Fatal(err)
	}
	other := historyInsert(t, s, admin, "book", "Shared", "requested", "", "", nil)
	page, err := s.adminHistory(context.Background(), adminHistoryFilter{UserID: admin, Limit: 50})
	if err != nil || len(page.Requests) != 2 || page.Requests[0].ID != other || page.Requests[1].ID != id {
		t.Fatalf("subscriber history = %+v, %v", page, err)
	}
	if got := page.Requests[1].Requesters; len(got) != 2 || got[1].Username != "reviewer" || got[1].BookFormat != "audiobook" {
		t.Fatalf("shared requesters = %+v", got)
	}
	// Legacy completion materialized each subscriber's own row. The old waiter
	// relation must not make that subscriber inherit the owner's history too.
	if _, err := s.db.Exec(`UPDATE request_log SET status='requested' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	page, err = s.adminHistory(context.Background(), adminHistoryFilter{UserID: admin, Limit: 50})
	if err != nil || len(page.Requests) != 1 || page.Requests[0].ID != other {
		t.Fatalf("materialized subscriber history = %+v, %v", page, err)
	}
}

func TestAdminHistoryHandlerAccessValidationAndEmptyResponse(t *testing.T) {
	s, uid, admin := seedAdminHistory(t)
	h := NewHandler(s)
	for _, tc := range []struct {
		user   int64
		query  string
		status int
	}{
		{0, "", http.StatusUnauthorized},
		{uid, "", http.StatusForbidden},
		{admin, "", http.StatusOK},
		{admin, "?media_type=radarr", http.StatusBadRequest},
		{admin, "?decision=available", http.StatusBadRequest},
		{admin, "?limit=101", http.StatusBadRequest},
		{admin, "?limit=0", http.StatusBadRequest},
		{admin, "?before=-1", http.StatusBadRequest},
		{admin, "?user_id=no", http.StatusBadRequest},
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/admin/requests/history"+tc.query, nil)
		if tc.user > 0 {
			// Deliberately stale claims: current role in the DB still decides.
			r = r.WithContext(context.WithValue(r.Context(), auth.ClaimsKey, &auth.Claims{UserID: tc.user, Role: "admin"}))
		}
		w := httptest.NewRecorder()
		h.ListHistory(w, r)
		if w.Code != tc.status {
			t.Fatalf("user %d %s: %d %s", tc.user, tc.query, w.Code, w.Body.String())
		}
		if w.Code == http.StatusOK {
			var data map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(data["requests"], []any{}) {
				t.Fatalf("empty history must be an array: %v", data)
			}
		}
	}
	s.db.Close()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r = r.WithContext(context.WithValue(r.Context(), auth.ClaimsKey, &auth.Claims{UserID: admin}))
	w := httptest.NewRecorder()
	h.ListHistory(w, r)
	if w.Code == http.StatusOK {
		t.Fatal("database failure must not read as empty history")
	}
}

type historyRoleChangePoster struct {
	fakePosterSource
	changeRole func()
}

func (p *historyRoleChangePoster) GetMovieDetails(id int) (*tmdb.MovieDetails, error) {
	p.changeRole()
	return p.fakePosterSource.GetMovieDetails(id)
}

func TestAdminHistoryRechecksRoleAfterArtwork(t *testing.T) {
	s, uid, admin := seedAdminHistory(t)
	id := historyInsert(t, s, uid, "movie", "Private history", "requested", "", "", nil)
	if _, err := s.db.Exec(`UPDATE request_log SET tmdb_id=603 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	s.posterLookupOverride = &historyRoleChangePoster{changeRole: func() {
		if _, err := s.db.Exec(`UPDATE users SET role='user' WHERE id=?`, admin); err != nil {
			t.Error(err)
		}
	}}
	r := httptest.NewRequest(http.MethodGet, "/api/admin/requests/history", nil)
	r = r.WithContext(context.WithValue(r.Context(), auth.ClaimsKey, &auth.Claims{UserID: admin, Role: "admin"}))
	w := httptest.NewRecorder()
	NewHandler(s).ListHistory(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("demoted admin read = %d %s", w.Code, w.Body.String())
	}
}
