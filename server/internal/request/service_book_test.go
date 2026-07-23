package request

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// newBookTestService builds a Service backed by an in-memory DB with one user
// row (so request_log's user_id FK is satisfied). The book request_log path
// (createPending / insertRequest / GetUserBookStatus) needs only the DB, so the
// registry/bridge/notifier are nil.
func newBookTestService(t *testing.T) (*Service, int64) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	res, err := database.Exec(
		"INSERT INTO users (username, password_hash, role) VALUES ('reader', '', 'user')",
	)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	uid, _ := res.LastInsertId()
	return NewService(database, nil, nil, nil), uid
}

func TestBookRequestTrimsAndRejectsBlankForeignID(t *testing.T) {
	svc, uid := newBookTestService(t)
	request := &CreateRequest{MediaType: "book", ForeignID: " \t\n ", Title: "Flock", BookFormat: BookFormatAudiobook}
	if _, err := svc.CreateMediaRequest(uid, request); err == nil || err.Error() != "foreign_id is required for book requests" {
		t.Fatalf("CreateMediaRequest error = %v, want requester-safe foreign_id validation", err)
	}
	if request.ForeignID != "" {
		t.Fatalf("normalized foreign_id = %q, want blank", request.ForeignID)
	}
}

func TestBookRequestRequiresTitleOnlyForNewCanonicalBook(t *testing.T) {
	t.Run("new book", func(t *testing.T) {
		lookupCalls := 0
		chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/book":
				_, _ = w.Write([]byte(`[]`))
			case "/api/v1/book/lookup":
				lookupCalls++
				http.Error(w, "lookup should not be reached", http.StatusInternalServerError)
			default:
				http.NotFound(w, r)
			}
		}))
		defer chaptarrServer.Close()
		svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
		request := &CreateRequest{MediaType: "book", ForeignID: "  new-book  ", Title: " \t ", BookFormat: BookFormatEbook}
		if _, err := svc.CreateMediaRequest(uid, request); err == nil || err.Error() != "title is required to add a new book" {
			t.Fatalf("CreateMediaRequest error = %v, want title validation", err)
		}
		if lookupCalls != 0 {
			t.Fatalf("blank title reached lookup %d times", lookupCalls)
		}
		if request.ForeignID != "new-book" || request.Title != "" {
			t.Fatalf("normalized request = %+v", request)
		}
	})

	t.Run("owned canonical record", func(t *testing.T) {
		lookupCalls := 0
		chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/book":
				_, _ = w.Write([]byte(`[{"id":1,"title":"  Existing Title  ","foreignBookId":"existing-book","mediaType":"ebook","monitored":true,"statistics":{"bookFileCount":1}}]`))
			case "/api/v1/book/lookup":
				lookupCalls++
				http.Error(w, "lookup should not be reached", http.StatusInternalServerError)
			default:
				http.NotFound(w, r)
			}
		}))
		defer chaptarrServer.Close()
		svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
		resp, err := svc.CreateMediaRequest(uid, &CreateRequest{
			MediaType: "book", ForeignID: " existing-book ", Title: "  ", BookFormat: BookFormatEbook,
		})
		if err != nil {
			t.Fatalf("CreateMediaRequest: %v", err)
		}
		if resp.Status != StatusAvailable || resp.Title != "Existing Title" {
			t.Fatalf("response = %+v, want canonical trimmed title", resp)
		}
		if lookupCalls != 0 {
			t.Fatalf("owned canonical record reached lookup %d times", lookupCalls)
		}
		var foreignID, title string
		if err := svc.db.QueryRow("SELECT foreign_id, title FROM request_log WHERE user_id = ?", uid).Scan(&foreignID, &title); err != nil {
			t.Fatal(err)
		}
		if foreignID != "existing-book" || title != "Existing Title" {
			t.Fatalf("stored foreign_id/title = %q/%q", foreignID, title)
		}
	})
}

// TestBookRequestStatusAndDedup covers the request_log book path: status is
// keyed by foreignBookId, exact duplicate pending requests do not create a
// second row, different requested formats are independent, distinct books are
// independent, and a directly-logged book reads back as requested.
func TestBookRequestStatusAndDedup(t *testing.T) {
	s, uid := newBookTestService(t)
	const fid = "goodreads:12345"

	if st, err := s.GetUserBookStatus(uid, fid); err != nil || st.Status != StatusUnavailable {
		t.Fatalf("empty status = %+v err=%v, want unavailable", st, err)
	}

	r := &resolvedRequest{userID: uid, mediaType: "book", foreignID: fid, title: "Some Book", bookFormat: BookFormatEbook}
	if _, err := s.createPending(r); err != nil {
		t.Fatalf("createPending: %v", err)
	}
	if st, _ := s.GetUserBookStatus(uid, fid); st.Status != StatusPending {
		t.Fatalf("status after pending = %s, want pending", st.Status)
	}

	// A duplicate pending request for the same format must NOT create a second row.
	if _, err := s.createPending(r); err != nil {
		t.Fatalf("createPending dup: %v", err)
	}
	var count int
	if err := s.db.QueryRow(
		"SELECT COUNT(*) FROM request_log WHERE user_id=? AND foreign_id=? AND media_type='book' AND status='pending'",
		uid, fid,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("pending book rows = %d, want 1 (dedup by foreign_id + format)", count)
	}

	// A different format for the same book is a distinct admin-queue request.
	audio := &resolvedRequest{userID: uid, mediaType: "book", foreignID: fid, title: "Some Book", bookFormat: BookFormatAudiobook}
	if _, err := s.createPending(audio); err != nil {
		t.Fatalf("createPending audio: %v", err)
	}
	if err := s.db.QueryRow(
		"SELECT COUNT(*) FROM request_log WHERE user_id=? AND foreign_id=? AND media_type='book' AND status='pending'",
		uid, fid,
	).Scan(&count); err != nil {
		t.Fatalf("count after audio: %v", err)
	}
	if count != 2 {
		t.Fatalf("pending book rows = %d, want 2 for two requested formats", count)
	}
	// A later "both" overlaps both concrete rows and must not add a third
	// approval item for either format.
	both := &resolvedRequest{userID: uid, mediaType: "book", foreignID: fid, title: "Some Book", bookFormat: BookFormatBoth}
	if _, err := s.createPending(both); err != nil {
		t.Fatalf("createPending both: %v", err)
	}
	if err := s.db.QueryRow(
		"SELECT COUNT(*) FROM request_log WHERE user_id=? AND foreign_id=? AND media_type='book' AND status='pending'",
		uid, fid,
	).Scan(&count); err != nil {
		t.Fatalf("count after both: %v", err)
	}
	if count != 2 {
		t.Fatalf("pending book rows = %d, want overlapping both request deduped", count)
	}

	// A different book is independent of the first.
	other := &resolvedRequest{userID: uid, mediaType: "book", foreignID: "goodreads:999", title: "Other"}
	if _, err := s.createPending(other); err != nil {
		t.Fatalf("createPending other: %v", err)
	}
	if st, _ := s.GetUserBookStatus(uid, "goodreads:999"); st.Status != StatusPending {
		t.Fatalf("other book status = %s, want pending", st.Status)
	}

	// A directly-logged (auto-approved) book reads back as requested — proves
	// insertRequest persists foreign_id so the status lookup finds it.
	direct := &resolvedRequest{userID: uid, mediaType: "book", foreignID: "goodreads:777", title: "Direct"}
	if _, err := s.insertRequest(direct, "Direct", StatusRequested); err != nil {
		t.Fatalf("insertRequest: %v", err)
	}
	if st, _ := s.GetUserBookStatus(uid, "goodreads:777"); st.Status != StatusRequested {
		t.Fatalf("direct book status = %s, want requested", st.Status)
	}
}

func TestBookPendingDedupAddsOnlyUncoveredConcreteFormat(t *testing.T) {
	svc, uid := newBookTestService(t)
	const fid = "overlap-1"
	if _, err := svc.createPending(&resolvedRequest{
		userID: uid, mediaType: "book", foreignID: fid, title: "Overlap", bookFormat: BookFormatEbook,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.createPending(&resolvedRequest{
		userID: uid, mediaType: "book", foreignID: fid, title: "Overlap", bookFormat: BookFormatBoth,
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := svc.db.Query(
		"SELECT book_format FROM request_log WHERE user_id=? AND foreign_id=? AND status='pending' ORDER BY id", uid, fid,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var formats []string
	for rows.Next() {
		var format string
		if err := rows.Scan(&format); err != nil {
			t.Fatal(err)
		}
		formats = append(formats, format)
	}
	if len(formats) != 2 || formats[0] != BookFormatEbook || formats[1] != BookFormatAudiobook {
		t.Fatalf("pending formats = %#v, want ebook then only uncovered audiobook", formats)
	}

	const reverseFID = "overlap-2"
	if _, err := svc.createPending(&resolvedRequest{
		userID: uid, mediaType: "book", foreignID: reverseFID, title: "Reverse", bookFormat: BookFormatBoth,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.createPending(&resolvedRequest{
		userID: uid, mediaType: "book", foreignID: reverseFID, title: "Reverse", bookFormat: BookFormatEbook,
	}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := svc.db.QueryRow(
		"SELECT COUNT(*) FROM request_log WHERE user_id=? AND foreign_id=? AND status='pending'", uid, reverseFID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("both then ebook created %d rows, want one both row", count)
	}
}

func TestBookPendingWaiterKeepsConcreteFormatOnDenial(t *testing.T) {
	svc, ownerID := newBookTestService(t)
	res, err := svc.db.Exec("INSERT INTO users (username, password_hash, role) VALUES ('ebook-waiter', '', 'user')")
	if err != nil {
		t.Fatal(err)
	}
	waiterID, _ := res.LastInsertId()
	recorder := &recordingNotifier{}
	svc.notifier = recorder

	if _, err := svc.createPending(&resolvedRequest{
		userID: ownerID, mediaType: "book", foreignID: "shared-both", title: "Shared Both", bookFormat: BookFormatBoth,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.createPending(&resolvedRequest{
		userID: waiterID, mediaType: "book", foreignID: "shared-both", title: "Shared Both", bookFormat: BookFormatEbook,
	}); err != nil {
		t.Fatal(err)
	}

	var requestID int64
	var waiterFormat string
	if err := svc.db.QueryRow(
		`SELECT r.id, bw.book_format FROM request_log r
		 JOIN book_request_waiters bw ON bw.request_id = r.id
		 WHERE r.foreign_id = 'shared-both' AND bw.user_id = ?`, waiterID,
	).Scan(&requestID, &waiterFormat); err != nil {
		t.Fatal(err)
	}
	if waiterFormat != BookFormatEbook {
		t.Fatalf("waiter coverage = %q, want ebook", waiterFormat)
	}

	adminID := createTestAdmin(t, svc)
	if err := svc.DenyRequest(adminID, requestID, "not now"); err != nil {
		t.Fatal(err)
	}
	var deniedFormat string
	if err := svc.db.QueryRow(
		"SELECT book_format FROM request_log WHERE user_id = ? AND foreign_id = 'shared-both' AND status = 'denied'",
		waiterID,
	).Scan(&deniedFormat); err != nil {
		t.Fatal(err)
	}
	if deniedFormat != BookFormatEbook {
		t.Fatalf("waiter denial coverage = %q, want ebook", deniedFormat)
	}
	found := false
	for _, event := range recorder.userEvents {
		if event.userID == waiterID && event.data["book_format"] == BookFormatEbook {
			found = true
		}
	}
	if !found {
		t.Fatalf("waiter did not receive ebook-scoped decision: %+v", recorder.userEvents)
	}
}

func TestListPendingCountsLegacyOwnerOutsideWaiterTable(t *testing.T) {
	svc, ownerID := newBookTestService(t)
	res, err := svc.db.Exec("INSERT INTO users (username, password_hash, role) VALUES ('legacy-waiter', '', 'user')")
	if err != nil {
		t.Fatal(err)
	}
	waiterID, _ := res.LastInsertId()
	res, err = svc.db.Exec(
		`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, media_type, title, status)
		 VALUES (?, 0, 'legacy-shared', 'ebook', 'book', 'Legacy Shared', 'pending')`, ownerID,
	)
	if err != nil {
		t.Fatal(err)
	}
	requestID, _ := res.LastInsertId()
	if _, err := svc.db.Exec(
		"INSERT INTO book_request_waiters (request_id, user_id, book_format) VALUES (?, ?, 'ebook')",
		requestID, waiterID,
	); err != nil {
		t.Fatal(err)
	}
	pending, err := svc.ListPending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].RequesterCount != 2 {
		t.Fatalf("pending = %+v, want legacy owner plus one waiter", pending)
	}
}

func TestCreatePendingBookRollsBackWhenSubscriptionFails(t *testing.T) {
	svc, uid := newBookTestService(t)
	if _, err := svc.db.Exec("DROP TABLE book_request_waiters"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.createPending(&resolvedRequest{
		userID: uid, mediaType: "book", foreignID: "atomic", title: "Atomic", bookFormat: BookFormatEbook,
	}); err == nil {
		t.Fatal("createPending succeeded without subscriber table")
	}
	var count int
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM request_log WHERE foreign_id = 'atomic'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rolled-back pending rows = %d, want zero", count)
	}
}

func TestApproveLegacyUnpinnedBookFailsClosed(t *testing.T) {
	svc, uid := newBookTestService(t)
	if _, err := svc.createPending(&resolvedRequest{
		userID: uid, mediaType: "book", foreignID: "legacy", title: "Legacy", bookFormat: BookFormatEbook,
	}); err != nil {
		t.Fatal(err)
	}
	adminID := createTestAdmin(t, svc)
	var requestID int64
	if err := svc.db.QueryRow("SELECT id FROM request_log WHERE foreign_id = 'legacy'").Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApproveRequest(adminID, requestID, nil); err == nil || !strings.Contains(err.Error(), "no pinned Chaptarr instance") {
		t.Fatalf("ApproveRequest error = %v, want pinned-instance failure", err)
	}
	var status string
	if err := svc.db.QueryRow("SELECT status FROM request_log WHERE id = ?", requestID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != StatusPending {
		t.Fatalf("legacy request status = %q, want pending", status)
	}
}

func TestBookAudienceReadFailureAbortsDecision(t *testing.T) {
	svc, uid := newBookTestService(t)
	if _, err := svc.db.Exec(
		"INSERT INTO service_instances (id, service_type, name, url, api_key) VALUES ('books', 'chaptarr', 'Books', 'http://unused', 'secret')",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.createPending(&resolvedRequest{
		userID: uid, mediaType: "book", foreignID: "audience-error", title: "Audience", bookFormat: BookFormatEbook, instanceID: "books",
	}); err != nil {
		t.Fatal(err)
	}
	adminID := createTestAdmin(t, svc)
	var requestID int64
	if err := svc.db.QueryRow("SELECT id FROM request_log WHERE foreign_id = 'audience-error'").Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec("DROP TABLE book_request_waiters"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApproveRequest(adminID, requestID, nil); err == nil || !strings.Contains(err.Error(), "subscribers") {
		t.Fatalf("ApproveRequest error = %v, want subscriber read failure", err)
	}
	if err := svc.DenyRequest(adminID, requestID, "no"); err == nil || !strings.Contains(err.Error(), "subscribers") {
		t.Fatalf("DenyRequest error = %v, want subscriber read failure", err)
	}
	var status string
	if err := svc.db.QueryRow("SELECT status FROM request_log WHERE id = ?", requestID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != StatusPending {
		t.Fatalf("request status after failed decisions = %q, want pending", status)
	}
}

func TestBookPendingPreflightUsesLiveAndSharedPendingState(t *testing.T) {
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/book":
			_, _ = w.Write([]byte(`[{"id":7,"title":"Flock","foreignBookId":"flock","monitored":true,"grabbed":true,"mediaType":"audiobook","statistics":{"bookFileCount":0}}]`))
		case "/api/v1/queue":
			_, _ = w.Write([]byte(`{"totalRecords":0,"records":[]}`))
		case "/api/v1/command":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer chaptarrServer.Close()
	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	settings := svc.GetGlobalSettings()
	settings.RequireApproval = true
	if err := svc.SetGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}

	resp, err := svc.CreateMediaRequest(uid, &CreateRequest{MediaType: "book", ForeignID: "flock", Title: "Flock", BookFormat: BookFormatBoth})
	if err != nil {
		t.Fatal(err)
	}
	if resp.BookFormats[BookFormatAudiobook] != StatusRequested || resp.BookFormats[BookFormatEbook] != StatusPending {
		t.Fatalf("pending response = %#v", resp)
	}
	var pendingFormat string
	if err := svc.db.QueryRow("SELECT book_format FROM request_log WHERE foreign_id='flock' AND status='pending'").Scan(&pendingFormat); err != nil {
		t.Fatal(err)
	}
	if pendingFormat != BookFormatEbook {
		t.Fatalf("queued format = %q, want only uncovered ebook", pendingFormat)
	}

	res, err := svc.db.Exec("INSERT INTO users (username, password_hash, role) VALUES ('second-reader', '', 'user')")
	if err != nil {
		t.Fatal(err)
	}
	secondUID, _ := res.LastInsertId()
	var instanceID string
	if err := svc.db.QueryRow("SELECT id FROM service_instances WHERE service_type='chaptarr'").Scan(&instanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec("INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (?, 'chaptarr', ?)", secondUID, instanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateMediaRequest(uid, &CreateRequest{MediaType: "book", ForeignID: "shared", Title: "Shared", BookFormat: BookFormatEbook}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateMediaRequest(secondUID, &CreateRequest{MediaType: "book", ForeignID: "shared", Title: "Shared", BookFormat: BookFormatEbook}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM request_log WHERE foreign_id='shared' AND status='pending'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("shared pending rows = %d, want one across users", count)
	}
	pending, err := svc.ListPending()
	if err != nil {
		t.Fatal(err)
	}
	foundShared := false
	var sharedRequestID int64
	for _, request := range pending {
		if request.Title == "Shared" {
			foundShared = true
			sharedRequestID = request.ID
			if request.RequesterCount != 2 || request.InstanceName != "Books" {
				t.Fatalf("shared pending = %+v, want two requesters and safe instance name", request)
			}
		}
	}
	if !foundShared {
		t.Fatal("shared pending request not listed")
	}
	adminID := createTestAdmin(t, svc)
	if err := svc.DenyRequest(adminID, sharedRequestID, "not now"); err != nil {
		t.Fatal(err)
	}
	if status, err := svc.GetUserBookStatusForInstance(secondUID, "shared", instanceID); err != nil || status.Status != StatusDenied {
		t.Fatalf("waiter denial status = %+v err=%v, want personal denied history", status, err)
	}
}

// TestGetUserBookStatusPerFormat covers the per-format breakdown that lets the
// dashboard offer the other format after one is requested: a format-specific row
// covers only that format, a "both" row covers both, denied stays re-requestable,
// and the collapsed Status is preserved for back-compat.
func TestGetUserBookStatusPerFormat(t *testing.T) {
	svc, uid := newBookTestService(t)
	insert := func(fid, format, status string) {
		t.Helper()
		if _, err := svc.db.Exec(
			"INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, media_type, title, status) VALUES (?, 0, ?, ?, 'book', 'T', ?)",
			uid, fid, format, status,
		); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Ebook requested only: ebook covered, audiobook still open (absent).
	insert("b-ebook", BookFormatEbook, StatusRequested)
	st, _ := svc.GetUserBookStatus(uid, "b-ebook")
	if st.BookFormats[BookFormatEbook] != StatusRequested {
		t.Fatalf("ebook = %v, want requested", st.BookFormats[BookFormatEbook])
	}
	if _, ok := st.BookFormats[BookFormatAudiobook]; ok {
		t.Fatalf("audiobook should be absent (still requestable), got %v", st.BookFormats)
	}

	// Two separate format rows.
	insert("b-two", BookFormatEbook, StatusRequested)
	insert("b-two", BookFormatAudiobook, StatusPending)
	st, _ = svc.GetUserBookStatus(uid, "b-two")
	if st.BookFormats[BookFormatEbook] != StatusRequested ||
		st.BookFormats[BookFormatAudiobook] != StatusPending {
		t.Fatalf("two-format = %#v, want ebook requested + audiobook pending", st.BookFormats)
	}

	// A single "both" row expands to both concrete formats.
	insert("b-both", BookFormatBoth, StatusRequested)
	st, _ = svc.GetUserBookStatus(uid, "b-both")
	if st.BookFormats[BookFormatEbook] != StatusRequested ||
		st.BookFormats[BookFormatAudiobook] != StatusRequested {
		t.Fatalf("both = %#v, want ebook+audiobook requested", st.BookFormats)
	}

	// Denied ebook, no audiobook: collapsed Status preserved; audiobook still open.
	insert("b-denied", BookFormatEbook, StatusDenied)
	st, _ = svc.GetUserBookStatus(uid, "b-denied")
	if st.BookFormats[BookFormatEbook] != StatusDenied {
		t.Fatalf("denied ebook = %#v, want ebook denied", st.BookFormats)
	}
	if _, ok := st.BookFormats[BookFormatAudiobook]; ok {
		t.Fatalf("audiobook should be absent for denied-ebook book, got %#v", st.BookFormats)
	}
	if st.Status != StatusDenied {
		t.Fatalf("collapsed status = %v, want denied (back-compat)", st.Status)
	}

	// Unknown foreign id: unavailable, no per-format map.
	st, _ = svc.GetUserBookStatus(uid, "nope")
	if st.Status != StatusUnavailable || len(st.BookFormats) != 0 {
		t.Fatalf("unknown = %#v, want unavailable + no formats", st)
	}
}

func TestBookRequestErrorStatus(t *testing.T) {
	if got := bookRequestErrorStatus(ErrChaptarrInstanceForbidden); got != http.StatusForbidden {
		t.Fatalf("forbidden status = %d, want 403", got)
	}
	if got := bookRequestErrorStatus(ErrChaptarrInstanceInvalid); got != http.StatusBadRequest {
		t.Fatalf("invalid status = %d, want 400", got)
	}
	if got := bookRequestErrorStatus(ErrBookFormatUnresolved); got != http.StatusConflict {
		t.Fatalf("unresolved status = %d, want 409", got)
	}
	if got := bookRequestErrorStatus(errors.New("upstream failed")); got != http.StatusInternalServerError {
		t.Fatalf("generic status = %d, want 500", got)
	}
}

func TestBookRequestOptionsDoNotOfferIgnoredQualityChoice(t *testing.T) {
	svc, uid := newBookTestService(t)
	settings := svc.GetGlobalSettings()
	settings.AllowQualityChoice = true
	if err := svc.SetGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}
	opts, err := svc.GetRequestOptions(uid, false, "book")
	if err != nil {
		t.Fatal(err)
	}
	if opts.CanChooseQuality || len(opts.QualityProfiles) != 0 {
		t.Fatalf("book options = %+v, want no ignored quality choice", opts)
	}
}

func TestSelectBookProfilesRequiresOneOrUniqueDefault(t *testing.T) {
	if id, ok := selectBookQualityProfile([]chaptarr.QualityProfile{{ID: 4, Name: "Only"}}, BookFormatEbook); !ok || id != 4 {
		t.Fatalf("single quality = %d ok=%v", id, ok)
	}
	if id, ok := selectBookQualityProfile([]chaptarr.QualityProfile{{ID: 1, Name: "Books"}, {ID: 2, Name: "DEFAULT"}}, BookFormatEbook); !ok || id != 2 {
		t.Fatalf("default quality = %d ok=%v", id, ok)
	}
	if _, ok := selectBookQualityProfile([]chaptarr.QualityProfile{{ID: 1, Name: "One"}, {ID: 2, Name: "Two"}}, BookFormatEbook); ok {
		t.Fatal("ambiguous quality profiles were guessed")
	}
	if id, ok := selectBookMetadataProfile([]chaptarr.MetadataProfile{{ID: 7, Name: "Default"}, {ID: 8, Name: "Other"}}, BookFormatEbook); !ok || id != 7 {
		t.Fatalf("default metadata = %d ok=%v", id, ok)
	}
	if _, ok := selectBookMetadataProfile([]chaptarr.MetadataProfile{{ID: 7, Name: "Default"}, {ID: 8, Name: "default"}}, BookFormatEbook); ok {
		t.Fatal("duplicate default metadata profiles were guessed")
	}
}

// TestBookRequestEbookFormatAddsRealisticEdition guards the "no ebook edition
// available" regression: Chaptarr's real lookup editions all report
// isEbook=false / format=null, so the old per-edition format gate rejected every
// ebook request. An ebook request must now add the book and set ebook-format
// book-level flags instead of erroring.
func TestBookStatusUsesLiveProjectionAndCache(t *testing.T) {
	bookCalls, queueCalls := 0, 0
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/book":
			bookCalls++
			_, _ = w.Write([]byte(`[
				{"id":1,"title":"Flock","foreignBookId":"flock","monitored":true,"grabbed":true,"mediaType":"audiobook","statistics":{"bookFileCount":0}},
				{"id":2,"title":"Queued","foreignBookId":"queued","monitored":true,"mediaType":"ebook","statistics":{"bookFileCount":0}},
				{"id":3,"title":"Here","foreignBookId":"here","monitored":false,"mediaType":"ebook","statistics":{"bookFileCount":1}}
				,{"id":4,"title":"Blocked","foreignBookId":"blocked","monitored":true,"grabbed":true,"mediaType":"ebook","statistics":{"bookFileCount":0}}
			]`))
		case "/api/v1/queue":
			queueCalls++
			_, _ = w.Write([]byte(`{"totalRecords":2,"records":[
				{"id":8,"bookId":2,"status":"downloading","size":100,"sizeleft":50},
				{"id":9,"bookId":4,"status":"downloading","trackedDownloadStatus":"warning","trackedDownloadState":"importBlocked"}
			]}`))
		case "/api/v1/command":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer chaptarrServer.Close()
	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)

	for fid, want := range map[string]string{"flock": StatusRequested, "queued": StatusDownloading, "here": StatusAvailable, "blocked": StatusRequested} {
		st, err := svc.GetUserBookStatus(uid, fid)
		if err != nil || st.Status != want {
			t.Fatalf("status %s = %#v err=%v, want %s", fid, st, err, want)
		}
	}
	if bookCalls != 1 || queueCalls != 1 {
		t.Fatalf("projection fetches book=%d queue=%d, want one of each across three status calls", bookCalls, queueCalls)
	}
}

func TestBookQueueItemDownloadingClassification(t *testing.T) {
	tests := []struct {
		name string
		item chaptarr.QueueItem
		want bool
	}{
		{name: "queued", item: chaptarr.QueueItem{Status: "queued", TrackedDownloadStatus: "ok"}, want: true},
		{name: "downloading", item: chaptarr.QueueItem{Status: "downloading", TrackedDownloadStatus: "ok"}, want: true},
		{name: "importing", item: chaptarr.QueueItem{Status: "importing", TrackedDownloadStatus: "ok"}, want: true},
		{name: "completed import pending", item: chaptarr.QueueItem{Status: "completed", TrackedDownloadStatus: "ok", TrackedDownloadState: "importPending"}, want: true},
		{name: "blank status downloading state", item: chaptarr.QueueItem{TrackedDownloadStatus: "ok", TrackedDownloadState: "downloading"}, want: true},
		{name: "completed imported", item: chaptarr.QueueItem{Status: "completed", TrackedDownloadStatus: "ok", TrackedDownloadState: "imported"}},
		{name: "paused", item: chaptarr.QueueItem{Status: "paused", TrackedDownloadStatus: "ok"}},
		{name: "client unavailable", item: chaptarr.QueueItem{Status: "downloadClientUnavailable", TrackedDownloadStatus: "ok"}},
		{name: "warning", item: chaptarr.QueueItem{Status: "downloading", TrackedDownloadStatus: "warning"}},
		{name: "import blocked", item: chaptarr.QueueItem{Status: "completed", TrackedDownloadStatus: "ok", TrackedDownloadState: "importBlocked"}},
		{name: "failed", item: chaptarr.QueueItem{Status: "failed", TrackedDownloadStatus: "error"}},
		{name: "problem message", item: chaptarr.QueueItem{Status: "downloading", TrackedDownloadStatus: "ok", StatusMessages: []chaptarr.StatusMessage{{Messages: []string{"problem"}}}}},
		{name: "unknown blank", item: chaptarr.QueueItem{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := bookQueueItemDownloading(tc.item); got != tc.want {
				t.Fatalf("bookQueueItemDownloading(%+v) = %v, want %v", tc.item, got, tc.want)
			}
		})
	}
}

func TestPhysicalRecordDoesNotImpersonateRequestedFormat(t *testing.T) {
	mutations := 0
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/book":
			_, _ = w.Write([]byte(`[{"id":5,"title":"Unknown","foreignBookId":"unknown","monitored":false,"mediaType":"paperback","editions":[]}]`))
		case "/api/v1/queue":
			_, _ = w.Write([]byte(`{"totalRecords":0,"records":[]}`))
		case "/api/v1/command":
			_, _ = w.Write([]byte(`[]`))
		default:
			if r.Method != http.MethodGet {
				mutations++
			}
			http.NotFound(w, r)
		}
	}))
	defer chaptarrServer.Close()
	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	status, err := svc.GetUserBookStatus(uid, "unknown")
	if err != nil || status.Status != StatusUnavailable {
		t.Fatalf("physical-only status = %+v err=%v, want unavailable", status, err)
	}
	if _, err := svc.CreateMediaRequest(uid, &CreateRequest{MediaType: "book", ForeignID: "unknown", Title: "Unknown", BookFormat: BookFormatEbook}); err == nil {
		t.Fatal("physical record was accepted as an ebook")
	}
	if mutations != 0 {
		t.Fatalf("physical record caused %d mutations", mutations)
	}
}

func TestBookLiveProjectionColdCacheSingleflight(t *testing.T) {
	var mu sync.Mutex
	bookCalls := 0
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/book":
			mu.Lock()
			bookCalls++
			mu.Unlock()
			time.Sleep(25 * time.Millisecond)
			_, _ = w.Write([]byte(`[{"id":1,"title":"Flock","foreignBookId":"flock","monitored":true,"grabbed":true,"mediaType":"audiobook"}]`))
		case "/api/v1/queue":
			_, _ = w.Write([]byte(`{"totalRecords":0,"records":[]}`))
		case "/api/v1/command":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer chaptarrServer.Close()
	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if st, err := svc.GetUserBookStatus(uid, "flock"); err != nil || st.Status != StatusRequested {
				t.Errorf("status=%#v err=%v", st, err)
			}
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if bookCalls != 1 {
		t.Fatalf("cold projection fetched library %d times, want one", bookCalls)
	}
}

func TestCanonicalSiblingFailsClosedOnConflictingAuthors(t *testing.T) {
	mutations := 0
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/book" {
			_, _ = w.Write([]byte(`[
				{"id":1,"authorId":10,"title":"Conflict","foreignBookId":"conflict","mediaType":"audiobook","monitored":true},
				{"id":2,"authorId":11,"title":"Conflict","foreignBookId":"conflict","mediaType":"audiobook","monitored":true}
			]`))
			return
		}
		if r.Method != http.MethodGet {
			mutations++
		}
		http.NotFound(w, r)
	}))
	defer chaptarrServer.Close()
	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	if _, err := svc.CreateMediaRequest(uid, &CreateRequest{MediaType: "book", ForeignID: "conflict", Title: "Conflict", BookFormat: BookFormatEbook}); err == nil {
		t.Fatal("conflicting canonical authors allowed sibling mutation")
	}
	if mutations != 0 {
		t.Fatalf("conflicting authors caused %d mutations", mutations)
	}
}

func TestBookRequestFailsClosedWhenPreflightUnavailable(t *testing.T) {
	lookupCalls := 0
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/book":
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		case "/api/v1/book/lookup":
			lookupCalls++
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer chaptarrServer.Close()
	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	if _, err := svc.CreateMediaRequest(uid, &CreateRequest{
		MediaType: "book", ForeignID: "x", Title: "X", BookFormat: BookFormatEbook,
	}); err == nil {
		t.Fatal("request succeeded without authoritative preflight")
	}
	if lookupCalls != 0 {
		t.Fatalf("lookup called %d times after failed preflight, want zero duplicate-risk mutations", lookupCalls)
	}
}

// TestBookRequestMonitorsAndSearchesNewAuthorBook covers the new-author case:
// Chaptarr returns the freshly added book unmonitored (its author's async
// refresh hasn't applied monitoring), so the request must monitor it (PUT
// /book/monitor) and kick off a search (BookSearch command) explicitly, or the
// request would silently fetch nothing.
func TestDenyBookRequestNotifiesWithForeignID(t *testing.T) {
	s, uid := newBookTestService(t)
	rec := &recordingNotifier{}
	s.notifier = rec
	adminID := createTestAdmin(t, s)

	const fid = "goodreads:12345"
	r := &resolvedRequest{userID: uid, mediaType: "book", foreignID: fid, title: "Some Book"}
	if _, err := s.createPending(r); err != nil {
		t.Fatalf("createPending: %v", err)
	}
	pending, err := s.ListPending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("ListPending = %+v err=%v, want exactly 1", pending, err)
	}

	if err := s.DenyRequest(adminID, pending[0].ID, "not now"); err != nil {
		t.Fatalf("DenyRequest: %v", err)
	}
	if len(rec.userEvents) != 1 {
		t.Fatalf("user events = %+v, want exactly one decision", rec.userEvents)
	}
	ev := rec.userEvents[0]
	if ev.data["decision"] != "denied" || ev.data["media_type"] != "book" {
		t.Errorf("event = %+v, want a denied book decision", ev)
	}
	if ev.data["foreign_id"] != fid {
		t.Errorf("event foreign_id = %v, want %s", ev.data["foreign_id"], fid)
	}
}

func TestAdminBookRequestsUseDefaultChaptarrWithoutUserGrant(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	res, err := database.Exec(
		"INSERT INTO users (username, password_hash, role) VALUES ('admin', '', 'admin')",
	)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	uid, _ := res.LastInsertId()

	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x24}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	store := instance.NewStore(database, cipher)
	if err := store.Create(&instance.Instance{
		ServiceType: "chaptarr",
		Name:        "Books",
		URL:         "http://chaptarr.local:8787",
		APIKey:      "key",
		IsDefault:   true,
	}); err != nil {
		t.Fatalf("create chaptarr instance: %v", err)
	}

	svc := NewService(database, instance.NewRegistry(store), nil, nil)
	if client := svc.getChaptarr(uid); client == nil {
		t.Fatal("admin getChaptarr returned nil without per-user grant; want default Chaptarr client")
	}
}

func TestFallbackTitleSlug(t *testing.T) {
	if got := fallbackTitleSlug("Ahsoka (Star Wars)"); got != "ahsoka-star-wars" {
		t.Fatalf("fallbackTitleSlug = %q, want ahsoka-star-wars", got)
	}
}

func newChaptarrBookTestService(t *testing.T, chaptarrURL string) (*Service, int64) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	res, err := database.Exec(
		"INSERT INTO users (username, password_hash, role) VALUES ('reader', '', 'user')",
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
	inst := &instance.Instance{
		ServiceType: "chaptarr",
		Name:        "Books",
		URL:         chaptarrURL,
		APIKey:      "key",
	}
	if err := store.Create(inst); err != nil {
		t.Fatalf("create chaptarr instance: %v", err)
	}
	if err := store.SetUserDefault(uid, "chaptarr", inst.ID); err != nil {
		t.Fatalf("grant chaptarr: %v", err)
	}

	service := NewService(database, instance.NewRegistry(store), nil, nil)
	service.bookMutationTimeout = 250 * time.Millisecond
	service.bookSettleInterval = time.Millisecond
	return service, uid
}
