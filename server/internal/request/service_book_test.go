package request

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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
				_, _ = w.Write([]byte(`[{"id":1,"title":"  Existing Title  ","foreignBookId":"existing-book","mediaType":"ebook","monitored":true,"statistics":{"bookFileCount":0}}]`))
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
		if resp.Status != StatusRequested || resp.Title != "Existing Title" {
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
			_, _ = w.Write([]byte(`[{"id":7,"title":"Flock","foreignBookId":"flock","monitored":true,"mediaType":"audiobook","statistics":{"bookFileCount":0}}]`))
		case "/api/v1/queue":
			_, _ = w.Write([]byte(`{"totalRecords":0,"records":[]}`))
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

func TestBookRequestFormatMonitorsRequestedEditions(t *testing.T) {
	var addBody map[string]any
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
			_, _ = w.Write([]byte(`[
				{
					"title":"Star Wars: Heir to the Empire",
					"titleSlug":"star-wars-heir-to-the-empire",
					"foreignBookId":"book-123",
					"author":{
						"authorName":"Timothy Zahn",
						"foreignAuthorId":"author-456"
					},
					"editions":[
						{"id":1,"foreignEditionId":"edition-ebook","titleSlug":"ebook","title":"Kindle Edition","format":"Kindle Edition","links":[{"url":"https://example.com","name":"Goodreads"}],"images":[{"url":"/cover.jpg","coverType":"cover"}]},
						{"id":2,"foreignEditionId":"edition-audio","titleSlug":"audio","title":"Audiobook","format":"Audible Audio","links":null}
					]
				}
			]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":11,"name":"Any"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/metadataprofile":
			_, _ = w.Write([]byte(`[{"id":22,"name":"Standard"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/rootfolder":
			_, _ = w.Write([]byte(`[{"id":33,"path":"/books","accessible":true}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/book":
			if err := json.NewDecoder(r.Body).Decode(&addBody); err != nil {
				t.Errorf("decode add book body: %v", err)
			}
			_, _ = w.Write([]byte(`{"id":9,"title":"Star Wars: Heir to the Empire","foreignBookId":"book-123","monitored":true}`))
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer chaptarrServer.Close()

	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	resp, err := svc.CreateMediaRequest(uid, &CreateRequest{
		MediaType:  "book",
		ForeignID:  "book-123",
		Title:      "Star Wars: Heir to the Empire",
		BookFormat: BookFormatAudiobook,
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if resp.Status != StatusRequested {
		t.Fatalf("status = %s, want requested", resp.Status)
	}
	if addBody == nil {
		t.Fatal("AddBook was not called")
	}
	// Format intent is carried by Chaptarr's book-level flags (this fork tracks
	// ebook vs audiobook per book, not per edition).
	if got := addBody["anyEditionOk"]; got != false {
		t.Fatalf("anyEditionOk = %v, want false for audiobook-only", got)
	}
	if got := addBody["mediaType"]; got != "audiobook" {
		t.Fatalf("mediaType = %v, want audiobook", got)
	}
	if got := addBody["audiobookMonitored"]; got != true {
		t.Fatalf("audiobookMonitored = %v, want true for audiobook request", got)
	}
	if got := addBody["ebookMonitored"]; got != false {
		t.Fatalf("ebookMonitored = %v, want false for audiobook request", got)
	}
	if addBody["title"] != "Star Wars: Heir to the Empire" {
		t.Fatalf("title = %v, want Star Wars: Heir to the Empire", addBody["title"])
	}
	if addBody["titleSlug"] != "star-wars-heir-to-the-empire" {
		t.Fatalf("titleSlug = %v, want star-wars-heir-to-the-empire", addBody["titleSlug"])
	}
	author := addBody["author"].(map[string]any)
	if author["authorName"] != "Timothy Zahn" || author["foreignAuthorId"] != "author-456" {
		t.Fatalf("author = %#v, want nested lookup author carried into add payload", author)
	}
	// Editions are round-tripped verbatim (all monitored) so Chaptarr's NOT NULL
	// links/images columns are satisfied; format is no longer chosen per edition.
	editions, ok := addBody["editions"].([]any)
	if !ok || len(editions) != 2 {
		t.Fatalf("editions = %#v, want 2 editions", addBody["editions"])
	}
	ebook := editions[0].(map[string]any)
	audio := editions[1].(map[string]any)
	for i, ed := range []map[string]any{ebook, audio} {
		if ed["monitored"] != true {
			t.Fatalf("edition[%d] monitored = %v, want true (monitor all editions)", i, ed["monitored"])
		}
		if ed["manualAdd"] != true {
			t.Fatalf("edition[%d] manualAdd = %v, want true", i, ed["manualAdd"])
		}
	}
	if audio["foreignEditionId"] != "edition-audio" || audio["titleSlug"] != "audio" {
		t.Fatalf("audiobook edition = %#v, want foreignEditionId/titleSlug preserved", audio)
	}
	// Regression guard for the SQLite "NOT NULL constraint failed: Editions.Links/Images"
	// add failure: links/images must survive the round-trip, and be defaulted to
	// [] (never null/absent) for editions the lookup omitted them on.
	ebookLinks, ok := ebook["links"].([]any)
	if !ok || len(ebookLinks) != 1 {
		t.Fatalf("ebook links = %#v, want the lookup's 1 link preserved", ebook["links"])
	}
	if ebookImages, ok := ebook["images"].([]any); !ok || len(ebookImages) != 1 {
		t.Fatalf("ebook images = %#v, want the lookup's 1 image preserved", ebook["images"])
	}
	if audioLinks, ok := audio["links"].([]any); !ok || len(audioLinks) != 0 {
		t.Fatalf("audio links = %#v, want [] coerced (lookup sent links:null)", audio["links"])
	}
	if audioImages, ok := audio["images"].([]any); !ok || len(audioImages) != 0 {
		t.Fatalf("audio images = %#v, want [] injected (lookup omitted images)", audio["images"])
	}

	var stored, storedInstance string
	if err := svc.db.QueryRow(
		"SELECT COALESCE(book_format, ''), COALESCE(instance_id, '') FROM request_log WHERE user_id = ? AND foreign_id = ?",
		uid,
		"book-123",
	).Scan(&stored, &storedInstance); err != nil {
		t.Fatalf("read stored format: %v", err)
	}
	if stored != BookFormatAudiobook {
		t.Fatalf("stored book_format = %q, want %q", stored, BookFormatAudiobook)
	}
	if storedInstance == "" {
		t.Fatal("instance_id was not pinned on fulfilled book request")
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
func TestBookRequestEbookFormatAddsRealisticEdition(t *testing.T) {
	var addBody map[string]any
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
			// Mirrors real Chaptarr metadata: a single edition with no format,
			// isEbook=false, and a present-but-empty links array.
			_, _ = w.Write([]byte(`[
				{
					"title":"Ahsoka (Star Wars)",
					"titleSlug":"ahsoka-star-wars",
					"foreignBookId":"29749107",
					"author":{"authorName":"E.K. Johnston","foreignAuthorId":"gr:7418796"},
					"editions":[
						{"foreignEditionId":"29749107","title":"Ahsoka (Star Wars)","format":null,"isEbook":false,"links":[],"images":[{"url":"/c.jpg","coverType":"cover"}]}
					]
				}
			]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"E-Book"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/metadataprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Standard"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/rootfolder":
			_, _ = w.Write([]byte(`[{"id":1,"path":"/books","accessible":true}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/book":
			if err := json.NewDecoder(r.Body).Decode(&addBody); err != nil {
				t.Errorf("decode add book body: %v", err)
			}
			_, _ = w.Write([]byte(`{"id":42,"title":"Ahsoka (Star Wars)","foreignBookId":"29749107","monitored":true}`))
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer chaptarrServer.Close()

	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	resp, err := svc.CreateMediaRequest(uid, &CreateRequest{
		MediaType:  "book",
		ForeignID:  "29749107",
		Title:      "Ahsoka (Star Wars)",
		BookFormat: BookFormatEbook,
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest (ebook): %v", err)
	}
	if resp.Status != StatusRequested {
		t.Fatalf("status = %s, want requested (ebook must not be rejected)", resp.Status)
	}
	if addBody == nil {
		t.Fatal("AddBook was not called for ebook request")
	}
	if got := addBody["mediaType"]; got != "ebook" {
		t.Fatalf("mediaType = %v, want ebook", got)
	}
	if got := addBody["ebookMonitored"]; got != true {
		t.Fatalf("ebookMonitored = %v, want true", got)
	}
	if got := addBody["audiobookMonitored"]; got != false {
		t.Fatalf("audiobookMonitored = %v, want false", got)
	}
	editions, ok := addBody["editions"].([]any)
	if !ok || len(editions) != 1 {
		t.Fatalf("editions = %#v, want 1 edition round-tripped", addBody["editions"])
	}
	ed := editions[0].(map[string]any)
	if ed["monitored"] != true {
		t.Fatalf("edition monitored = %v, want true", ed["monitored"])
	}
	if links, ok := ed["links"].([]any); !ok {
		t.Fatalf("edition links = %#v, want an array (never null)", ed["links"])
	} else if len(links) != 0 {
		t.Fatalf("edition links = %#v, want the lookup's empty array preserved", links)
	}
	if images, ok := ed["images"].([]any); !ok || len(images) != 1 {
		t.Fatalf("edition images = %#v, want the lookup's image preserved", ed["images"])
	}
}

// TestBookRequestBothFormatAddsEbookAndAudiobookRecords covers the "both" path.
// Chaptarr stores a title's ebook and audiobook as separate records (same
// foreignBookId, different mediaType), so a "both" request must POST the book
// twice — once as ebook, once as audiobook — each pinned to its own mediaType.
func TestBookRequestBothFormatAddsEbookAndAudiobookRecords(t *testing.T) {
	var addBodies []map[string]any
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
			_, _ = w.Write([]byte(`[
				{
					"title":"Ahsoka (Star Wars)",
					"titleSlug":"ahsoka-star-wars",
					"foreignBookId":"29749107",
					"author":{"authorName":"E.K. Johnston","foreignAuthorId":"gr:7418796"},
					"editions":[
						{"foreignEditionId":"29749107","title":"Ahsoka (Star Wars)","format":null,"isEbook":false,"links":[],"images":[]}
					]
				}
			]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"E-Book"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/metadataprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Standard"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/rootfolder":
			_, _ = w.Write([]byte(`[
				{"id":1,"path":"/library/ebooks","accessible":true,"freeSpace":10},
				{"id":2,"path":"/library/audiobooks","accessible":true,"freeSpace":10}
			]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/book":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode add book body: %v", err)
			}
			addBodies = append(addBodies, body)
			_, _ = w.Write([]byte(`{"id":50,"title":"Ahsoka (Star Wars)","foreignBookId":"29749107","monitored":true}`))
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer chaptarrServer.Close()

	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	resp, err := svc.CreateMediaRequest(uid, &CreateRequest{
		MediaType:  "book",
		ForeignID:  "29749107",
		Title:      "Ahsoka (Star Wars)",
		BookFormat: BookFormatBoth,
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest (both): %v", err)
	}
	if resp.Status != StatusRequested {
		t.Fatalf("status = %s, want requested", resp.Status)
	}
	if len(addBodies) != 2 {
		t.Fatalf("AddBook called %d times, want 2 (one ebook record + one audiobook record)", len(addBodies))
	}
	byFormat := map[string]map[string]any{}
	for _, b := range addBodies {
		mt, _ := b["mediaType"].(string)
		byFormat[mt] = b
	}
	ebook, ok := byFormat["ebook"]
	if !ok {
		t.Fatalf("no ebook record added; bodies = %#v", addBodies)
	}
	audio, ok := byFormat["audiobook"]
	if !ok {
		t.Fatalf("no audiobook record added; bodies = %#v", addBodies)
	}
	// Each record is pinned to its own format and shares the foreignBookId.
	if ebook["ebookMonitored"] != true || ebook["audiobookMonitored"] != false {
		t.Fatalf("ebook record flags = %#v, want ebookMonitored only", ebook)
	}
	if audio["audiobookMonitored"] != true || audio["ebookMonitored"] != false {
		t.Fatalf("audiobook record flags = %#v, want audiobookMonitored only", audio)
	}
	if ebook["foreignBookId"] != "29749107" || audio["foreignBookId"] != "29749107" {
		t.Fatalf("records must share foreignBookId 29749107; got ebook=%v audio=%v", ebook["foreignBookId"], audio["foreignBookId"])
	}
	if got := ebook["author"].(map[string]any)["rootFolderPath"]; got != "/library/ebooks" {
		t.Fatalf("ebook root = %v, want format-specific /library/ebooks", got)
	}
	if got := audio["author"].(map[string]any)["rootFolderPath"]; got != "/library/audiobooks" {
		t.Fatalf("audiobook root = %v, want format-specific /library/audiobooks", got)
	}
	for format, body := range byFormat {
		author := body["author"].(map[string]any)
		if author["ebookMonitorFuture"] != true || author["audiobookMonitorFuture"] != true {
			t.Fatalf("%s author future-monitor gates = %#v, want both requested formats enabled", format, author)
		}
	}
}

func TestBookRequestBothReportsAndStoresPartialPerFormat(t *testing.T) {
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
			_, _ = w.Write([]byte(`[{"title":"Partial","foreignBookId":"partial-1","author":{"authorName":"A","foreignAuthorId":"a"},"editions":[]}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Any"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/metadataprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Standard"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/rootfolder":
			_, _ = w.Write([]byte(`[{"id":1,"path":"/books","accessible":true}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/book":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["mediaType"] == BookFormatAudiobook {
				http.Error(w, "audio add failed", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`{"id":10,"title":"Partial","foreignBookId":"partial-1","monitored":true}`))
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer chaptarrServer.Close()

	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	resp, err := svc.CreateMediaRequest(uid, &CreateRequest{
		MediaType: "book", ForeignID: "partial-1", Title: "Partial", BookFormat: BookFormatBoth,
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if resp.Status != StatusPartial || resp.BookFormats[BookFormatEbook] != StatusRequested || resp.BookFormats[BookFormatAudiobook] != StatusUnavailable {
		t.Fatalf("response = %#v, want concrete requested/unavailable partial", resp)
	}
	rows, err := svc.db.Query("SELECT book_format, status FROM request_log WHERE user_id=? AND foreign_id=?", uid, "partial-1")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	stored := map[string]string{}
	for rows.Next() {
		var format, status string
		if err := rows.Scan(&format, &status); err != nil {
			t.Fatal(err)
		}
		stored[format] = status
	}
	if len(stored) != 1 || stored[BookFormatEbook] != StatusRequested {
		t.Fatalf("stored outcomes = %#v, want only successful ebook", stored)
	}
}

func TestPartialApprovalKeepsFailedWaiterPending(t *testing.T) {
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
			_, _ = w.Write([]byte(`[{"title":"Partial Approval","foreignBookId":"partial-approval","author":{"authorName":"A","foreignAuthorId":"a"},"editions":[]}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Any"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/metadataprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Standard"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/rootfolder":
			_, _ = w.Write([]byte(`[{"id":1,"path":"/books","accessible":true}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/book":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["mediaType"] == BookFormatAudiobook {
				http.Error(w, "audio add failed", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`{"id":10,"title":"Partial Approval","foreignBookId":"partial-approval","monitored":true}`))
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer chaptarrServer.Close()

	svc, ownerID := newChaptarrBookTestService(t, chaptarrServer.URL)
	_, instanceID, err := svc.resolveChaptarr(ownerID, "")
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.db.Exec("INSERT INTO users (username, password_hash, role) VALUES ('audio-waiter', '', 'user')")
	if err != nil {
		t.Fatal(err)
	}
	waiterID, _ := res.LastInsertId()
	res, err = svc.db.Exec("INSERT INTO users (username, password_hash, role) VALUES ('ebook-waiter', '', 'user')")
	if err != nil {
		t.Fatal(err)
	}
	ebookWaiterID, _ := res.LastInsertId()
	recorder := &recordingNotifier{}
	svc.notifier = recorder
	if _, err := svc.createPending(&resolvedRequest{
		userID: ownerID, mediaType: "book", foreignID: "partial-approval", title: "Partial Approval", bookFormat: BookFormatBoth, instanceID: instanceID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.createPending(&resolvedRequest{
		userID: waiterID, mediaType: "book", foreignID: "partial-approval", title: "Partial Approval", bookFormat: BookFormatAudiobook, instanceID: instanceID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.createPending(&resolvedRequest{
		userID: ebookWaiterID, mediaType: "book", foreignID: "partial-approval", title: "Partial Approval", bookFormat: BookFormatEbook, instanceID: instanceID,
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		userID int64
		format string
	}{
		{waiterID, BookFormatAudiobook},
		{ebookWaiterID, BookFormatEbook},
	} {
		history, historyErr := svc.GetRequests(tc.userID)
		if historyErr != nil {
			t.Fatal(historyErr)
		}
		if len(history) != 1 || history[0].Status != StatusPending || history[0].BookFormat != tc.format {
			t.Fatalf("waiter %d pending history = %+v, want one concrete %s row", tc.userID, history, tc.format)
		}
	}
	adminID := createTestAdmin(t, svc)
	var requestID int64
	if err := svc.db.QueryRow("SELECT id FROM request_log WHERE foreign_id = 'partial-approval' AND status = 'pending'").Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	response, err := svc.ApproveRequest(adminID, requestID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != StatusPartial {
		t.Fatalf("approval response = %+v, want partial", response)
	}
	var pendingFormat, waiterFormat string
	if err := svc.db.QueryRow(
		`SELECT r.book_format, bw.book_format FROM request_log r
		 JOIN book_request_waiters bw ON bw.request_id = r.id
		 WHERE r.foreign_id = 'partial-approval' AND r.status = 'pending' AND bw.user_id = ?`, waiterID,
	).Scan(&pendingFormat, &waiterFormat); err != nil {
		t.Fatal(err)
	}
	if pendingFormat != BookFormatAudiobook || waiterFormat != BookFormatAudiobook {
		t.Fatalf("retained pending=%q waiter=%q, want audiobook", pendingFormat, waiterFormat)
	}
	audioHistory, err := svc.GetRequests(waiterID)
	if err != nil {
		t.Fatal(err)
	}
	if len(audioHistory) != 1 || audioHistory[0].Status != StatusPending || audioHistory[0].BookFormat != BookFormatAudiobook {
		t.Fatalf("failed-only waiter history = %+v, want audiobook pending only", audioHistory)
	}
	ebookHistory, err := svc.GetRequests(ebookWaiterID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ebookHistory) != 1 || ebookHistory[0].Status != StatusRequested || ebookHistory[0].BookFormat != BookFormatEbook {
		t.Fatalf("successful waiter history = %+v, want personal ebook requested", ebookHistory)
	}
	foundEbookApproval := false
	for _, event := range recorder.userEvents {
		if event.userID == waiterID && event.data["decision"] == "approved" {
			t.Fatalf("audio-only waiter received false approval: %+v", event)
		}
		if event.userID == ebookWaiterID && event.data["decision"] == "approved" && event.data["book_format"] == BookFormatEbook {
			foundEbookApproval = true
		}
	}
	if !foundEbookApproval {
		t.Fatalf("ebook waiter did not receive format-scoped approval: %+v", recorder.userEvents)
	}
}

func TestBookStatusUsesLiveProjectionAndCache(t *testing.T) {
	bookCalls, queueCalls := 0, 0
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/book":
			bookCalls++
			_, _ = w.Write([]byte(`[
				{"id":1,"title":"Flock","foreignBookId":"flock","monitored":true,"mediaType":"audiobook","statistics":{"bookFileCount":0}},
				{"id":2,"title":"Queued","foreignBookId":"queued","monitored":true,"mediaType":"ebook","statistics":{"bookFileCount":0}},
				{"id":3,"title":"Here","foreignBookId":"here","monitored":false,"mediaType":"ebook","statistics":{"bookFileCount":1}}
				,{"id":4,"title":"Blocked","foreignBookId":"blocked","monitored":true,"mediaType":"ebook","statistics":{"bookFileCount":0}}
			]`))
		case "/api/v1/queue":
			queueCalls++
			_, _ = w.Write([]byte(`{"totalRecords":2,"records":[
				{"id":8,"bookId":2,"status":"downloading","size":100,"sizeleft":50},
				{"id":9,"bookId":4,"status":"downloading","trackedDownloadStatus":"warning","trackedDownloadState":"importBlocked"}
			]}`))
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

func TestUnknownFormatExactRecordFailsClosed(t *testing.T) {
	mutations := 0
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/book":
			_, _ = w.Write([]byte(`[{"id":5,"title":"Unknown","foreignBookId":"unknown","monitored":false,"mediaType":"paperback","editions":[]}]`))
		case "/api/v1/queue":
			_, _ = w.Write([]byte(`{"totalRecords":0,"records":[]}`))
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
	if err != nil || status.Status != StatusUnavailable || status.StatusKnown == nil || *status.StatusKnown {
		t.Fatalf("unknown exact status = %+v err=%v, want unavailable status_known=false", status, err)
	}
	if _, err := svc.CreateMediaRequest(uid, &CreateRequest{MediaType: "book", ForeignID: "unknown", Title: "Unknown", BookFormat: BookFormatEbook}); err == nil {
		t.Fatal("unknown exact format allowed mutation")
	}
	if mutations != 0 {
		t.Fatalf("unknown format caused %d mutations", mutations)
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
			_, _ = w.Write([]byte(`[{"id":1,"title":"Flock","foreignBookId":"flock","monitored":true,"mediaType":"audiobook"}]`))
		case "/api/v1/queue":
			_, _ = w.Write([]byte(`{"totalRecords":0,"records":[]}`))
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

func TestConcurrentBookRequestsSerializePreflightAndAdd(t *testing.T) {
	var mu sync.Mutex
	added := false
	postCalls := 0
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			mu.Lock()
			isAdded := added
			mu.Unlock()
			if isAdded {
				_, _ = w.Write([]byte(`[{"id":31,"title":"Race","foreignBookId":"race","monitored":true,"mediaType":"ebook"}]`))
			} else {
				_, _ = w.Write([]byte(`[]`))
			}
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
			_, _ = w.Write([]byte(`[{"title":"Race","foreignBookId":"race","author":{"authorName":"A","foreignAuthorId":"a"},"editions":[]}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Any"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/metadataprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Standard"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/rootfolder":
			_, _ = w.Write([]byte(`[{"id":1,"path":"/books","accessible":true}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/book":
			mu.Lock()
			postCalls++
			added = true
			mu.Unlock()
			_, _ = w.Write([]byte(`{"id":31,"title":"Race","foreignBookId":"race","monitored":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer chaptarrServer.Close()
	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.CreateMediaRequest(uid, &CreateRequest{MediaType: "book", ForeignID: "race", Title: "Race", BookFormat: BookFormatEbook}); err != nil {
				t.Errorf("CreateMediaRequest: %v", err)
			}
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if postCalls != 1 {
		t.Fatalf("concurrent requests added book %d times, want one", postCalls)
	}
}

func TestBookRequestAddsCanonicalSiblingWhenLookupIDDiffers(t *testing.T) {
	lookupCalls := 0
	var addBody map[string]any
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			_, _ = w.Write([]byte(`[{"id":4,"authorId":12,"title":"Flock","titleSlug":"flock","foreignBookId":"library-flock","monitored":true,"mediaType":"audiobook"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/author/12":
			_, _ = w.Write([]byte(`{"id":12,"authorName":"Kate Stewart","foreignAuthorId":"author-kate","qualityProfileId":3,"metadataProfileId":4,"path":"/library/books"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
			lookupCalls++
			_, _ = w.Write([]byte(`[{"title":"Flock","foreignBookId":"lookup-flock"}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/book":
			_ = json.NewDecoder(r.Body).Decode(&addBody)
			_, _ = w.Write([]byte(`{"id":5,"title":"Flock","foreignBookId":"library-flock","monitored":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer chaptarrServer.Close()
	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	resp, err := svc.CreateMediaRequest(uid, &CreateRequest{MediaType: "book", ForeignID: "library-flock", Title: "Flock", BookFormat: BookFormatEbook})
	if err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if resp.BookFormats[BookFormatEbook] != StatusRequested || addBody == nil {
		t.Fatalf("response=%#v add=%#v", resp, addBody)
	}
	if addBody["foreignBookId"] != "library-flock" || addBody["mediaType"] != BookFormatEbook {
		t.Fatalf("canonical sibling body = %#v", addBody)
	}
	if author := addBody["author"].(map[string]any); author["rootFolderPath"] != "/library/books" || author["qualityProfileId"] != float64(3) || author["metadataProfileId"] != float64(4) {
		t.Fatalf("canonical sibling author = %#v", author)
	}
	if lookupCalls != 0 {
		t.Fatalf("metadata lookup called %d times despite canonical existing group", lookupCalls)
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

func TestBookRequestMonitoredRecordIsIdempotent(t *testing.T) {
	mutations := 0
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			_, _ = w.Write([]byte(`[{"id":7,"title":"Flock","foreignBookId":"flock","monitored":true,"mediaType":"audiobook","statistics":{"bookFileCount":0}}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
			_, _ = w.Write([]byte(`[]`))
		default:
			if r.Method != http.MethodGet {
				mutations++
			}
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer chaptarrServer.Close()
	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	resp, err := svc.CreateMediaRequest(uid, &CreateRequest{
		MediaType: "book", ForeignID: "flock", Title: "Flock", BookFormat: BookFormatAudiobook,
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if resp.Status != StatusRequested || resp.BookFormats[BookFormatAudiobook] != StatusRequested {
		t.Fatalf("response = %#v, want already requested audiobook", resp)
	}
	if mutations != 0 {
		t.Fatalf("monitored record caused %d mutations, want idempotent no-op", mutations)
	}
}

func TestBookRequestMonitorSuccessSurvivesImmediateSearchFailure(t *testing.T) {
	monitored := false
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			_, _ = w.Write([]byte(`[{"id":9,"title":"Later","foreignBookId":"later","monitored":false,"mediaType":"ebook","statistics":{"bookFileCount":0}}]`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/book/monitor":
			monitored = true
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/command":
			http.Error(w, "command unavailable", http.StatusServiceUnavailable)
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer chaptarrServer.Close()
	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	resp, err := svc.CreateMediaRequest(uid, &CreateRequest{
		MediaType: "book", ForeignID: "later", Title: "Later", BookFormat: BookFormatEbook,
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if !monitored || resp.Status != StatusRequested || resp.BookFormats[BookFormatEbook] != StatusRequested {
		t.Fatalf("monitored=%v response=%#v, want durable requested despite search failure", monitored, resp)
	}
}

func TestBookRequestAddedUnmonitoredRequiresMonitoringSuccess(t *testing.T) {
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
			_, _ = w.Write([]byte(`[{"title":"New","foreignBookId":"new","author":{"authorName":"A","foreignAuthorId":"a"},"editions":[]}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Any"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/metadataprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Standard"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/rootfolder":
			_, _ = w.Write([]byte(`[{"id":1,"path":"/books","accessible":true}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/book":
			_, _ = w.Write([]byte(`{"id":22,"title":"New","foreignBookId":"new","monitored":false}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/book/monitor":
			http.Error(w, "monitor failed", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	defer chaptarrServer.Close()
	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	if _, err := svc.CreateMediaRequest(uid, &CreateRequest{MediaType: "book", ForeignID: "new", Title: "New", BookFormat: BookFormatEbook}); err == nil {
		t.Fatal("unmonitored add reported success after required monitoring failed")
	}
	var count int
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM request_log WHERE foreign_id='new'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed monitor wrote %d successful request rows", count)
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
func TestBookRequestMonitorsAndSearchesNewAuthorBook(t *testing.T) {
	var monitorIDs []any
	var searchCmd map[string]any
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
			_, _ = w.Write([]byte(`[
				{
					"title":"Ahsoka (Star Wars)","titleSlug":"ahsoka","foreignBookId":"29749107",
					"author":{"authorName":"E.K. Johnston","foreignAuthorId":"gr:7418796"},
					"editions":[{"foreignEditionId":"29749107","title":"Ahsoka (Star Wars)","links":[],"images":[]}]
				}
			]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"E-Book"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/metadataprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Standard"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/rootfolder":
			_, _ = w.Write([]byte(`[{"id":1,"path":"/books","accessible":true}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/book":
			// New-author add: Chaptarr returns the book unmonitored.
			_, _ = w.Write([]byte(`{"id":44,"title":"Ahsoka (Star Wars)","foreignBookId":"29749107","monitored":false}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/book/monitor":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			monitorIDs, _ = body["bookIds"].([]any)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/command":
			_ = json.NewDecoder(r.Body).Decode(&searchCmd)
			w.WriteHeader(http.StatusCreated)
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer chaptarrServer.Close()

	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	resp, err := svc.CreateMediaRequest(uid, &CreateRequest{
		MediaType: "book", ForeignID: "29749107", Title: "Ahsoka (Star Wars)", BookFormat: BookFormatEbook,
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if resp.Status != StatusRequested {
		t.Fatalf("status = %s, want requested", resp.Status)
	}
	if len(monitorIDs) != 1 || int(monitorIDs[0].(float64)) != 44 {
		t.Fatalf("monitor bookIds = %v, want [44] (unmonitored add must be monitored)", monitorIDs)
	}
	if searchCmd["name"] != "BookSearch" {
		t.Fatalf("command = %v, want a BookSearch", searchCmd["name"])
	}
	ids, _ := searchCmd["bookIds"].([]any)
	if len(ids) != 1 || int(ids[0].(float64)) != 44 {
		t.Fatalf("BookSearch bookIds = %v, want [44]", searchCmd["bookIds"])
	}
}

// TestApproveBookRequestNotifiesWithForeignID: approving a pending book request
// notifies the requester with the Chaptarr foreignBookId in the event data —
// books store tmdb_id 0, so foreign_id is the only identity a client can
// deep-link the decision tap to.
func TestApproveBookRequestNotifiesWithForeignID(t *testing.T) {
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
			_, _ = w.Write([]byte(`[
				{
					"title":"Ahsoka (Star Wars)","titleSlug":"ahsoka","foreignBookId":"29749107",
					"author":{"authorName":"E.K. Johnston","foreignAuthorId":"gr:7418796"},
					"editions":[{"foreignEditionId":"29749107","title":"Ahsoka (Star Wars)","links":[],"images":[]}]
				}
			]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"E-Book"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/metadataprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Standard"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/rootfolder":
			_, _ = w.Write([]byte(`[{"id":1,"path":"/books","accessible":true}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/book":
			_, _ = w.Write([]byte(`{"id":42,"title":"Ahsoka (Star Wars)","foreignBookId":"29749107","monitored":true}`))
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer chaptarrServer.Close()

	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	rec := &recordingNotifier{}
	svc.notifier = rec
	requireApproval(t, svc)
	adminID := createTestAdmin(t, svc)

	if _, err := svc.CreateMediaRequest(uid, &CreateRequest{
		MediaType: "book", ForeignID: "29749107", Title: "Ahsoka (Star Wars)", BookFormat: BookFormatEbook,
	}); err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	pending, err := svc.ListPending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("ListPending = %+v err=%v, want exactly 1", pending, err)
	}
	if pending[0].InstanceName != "Books" {
		t.Fatalf("pending instance_name = %q, want safe library name Books", pending[0].InstanceName)
	}
	if _, err := svc.ApproveRequest(adminID, pending[0].ID, &DecisionOverride{BookFormat: BookFormatAudiobook}); err == nil {
		t.Fatal("approval changed the requester's stored book format")
	}

	if _, err := svc.ApproveRequest(adminID, pending[0].ID, nil); err != nil {
		t.Fatalf("ApproveRequest: %v", err)
	}
	if len(rec.userEvents) != 1 {
		t.Fatalf("user events = %+v, want exactly one decision", rec.userEvents)
	}
	ev := rec.userEvents[0]
	if ev.userID != uid || ev.eventType != "request_decision" || ev.data["decision"] != "approved" {
		t.Errorf("event = %+v, want an approved request_decision to the requester", ev)
	}
	if ev.data["media_type"] != "book" || ev.data["tmdb_id"] != 0 {
		t.Errorf("event data = %#v, want media_type book with tmdb_id 0", ev.data)
	}
	if ev.data["foreign_id"] != "29749107" {
		t.Errorf("event foreign_id = %v, want 29749107", ev.data["foreign_id"])
	}
}

// TestDenyBookRequestNotifiesWithForeignID: the deny event carries the same
// book identity (denial touches no arr, so only the DB path is exercised).
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

	return NewService(database, instance.NewRegistry(store), nil, nil), uid
}

// TestBookStatusFollowsRekeyedRecord reproduces the field failure where
// Chaptarr files the record a request created under its own canonical
// foreignBookId: the logged id stops matching live truth, and the status read
// used to demote the accepted request to "not requested" — re-offering a
// duplicate request for a book that was already downloading. The persisted
// record id is the identity that survives the re-key.
func TestBookStatusFollowsRekeyedRecord(t *testing.T) {
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/book" {
			_, _ = w.Write([]byte(`[{"id":43,"title":"Flock","foreignBookId":"canon-777","mediaType":"audiobook","monitored":true,"statistics":{"bookFileCount":0}}]`))
			return
		}
		http.Error(w, "unexpected route", http.StatusNotFound)
	}))
	defer chaptarrServer.Close()

	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	var instanceID string
	if err := svc.db.QueryRow("SELECT id FROM service_instances").Scan(&instanceID); err != nil {
		t.Fatalf("read instance id: %v", err)
	}
	if _, err := svc.db.Exec(
		`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, book_record_id, instance_id, media_type, title, status)
		 VALUES (?, 0, 'lookup-777', 'audiobook', 43, ?, 'book', 'Flock', 'requested')`,
		uid, instanceID,
	); err != nil {
		t.Fatalf("seed request row: %v", err)
	}

	st, err := svc.GetUserBookStatus(uid, "lookup-777")
	if err != nil {
		t.Fatalf("GetUserBookStatus: %v", err)
	}
	if st.BookFormats[BookFormatAudiobook] != StatusRequested {
		t.Fatalf("audiobook = %q, want requested via the stored record id", st.BookFormats[BookFormatAudiobook])
	}
	if st.CanonicalForeignID != "canon-777" {
		t.Fatalf("canonical_foreign_id = %q, want canon-777", st.CanonicalForeignID)
	}
	if st.Status != StatusRequested {
		t.Fatalf("collapsed status = %q, want requested", st.Status)
	}
}

// TestBookStatusHealsWhenStoredRecordIsGone keeps the healing property intact:
// a stored record id whose record no longer exists is proof the request is
// gone, so the format returns to requestable instead of sticking forever.
func TestBookStatusHealsWhenStoredRecordIsGone(t *testing.T) {
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/book" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		http.Error(w, "unexpected route", http.StatusNotFound)
	}))
	defer chaptarrServer.Close()

	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	var instanceID string
	if err := svc.db.QueryRow("SELECT id FROM service_instances").Scan(&instanceID); err != nil {
		t.Fatalf("read instance id: %v", err)
	}
	if _, err := svc.db.Exec(
		`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, book_record_id, instance_id, media_type, title, status)
		 VALUES (?, 0, 'lookup-888', 'audiobook', 99, ?, 'book', 'Flock', 'requested')`,
		uid, instanceID,
	); err != nil {
		t.Fatalf("seed request row: %v", err)
	}

	st, err := svc.GetUserBookStatus(uid, "lookup-888")
	if err != nil {
		t.Fatalf("GetUserBookStatus: %v", err)
	}
	if st.BookFormats[BookFormatAudiobook] != StatusUnavailable {
		t.Fatalf("audiobook = %q, want unavailable once the record is gone", st.BookFormats[BookFormatAudiobook])
	}
	if st.CanonicalForeignID != "" {
		t.Fatalf("canonical_foreign_id = %q, want empty for a vanished record", st.CanonicalForeignID)
	}
}

// TestBookRequestPersistsCreatedRecordIdentity drives the full request flow
// against a Chaptarr that files the created record under a different
// foreignBookId than the lookup id: the created record id must be persisted
// with the history row, the canonical id returned to the client, and the very
// next status read must resolve live truth instead of demoting the request.
func TestBookRequestPersistsCreatedRecordIdentity(t *testing.T) {
	added := false
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			if added {
				_, _ = w.Write([]byte(`[{"id":51,"title":"Flock","foreignBookId":"canon-999","mediaType":"ebook","monitored":true,"statistics":{"bookFileCount":0}}]`))
			} else {
				_, _ = w.Write([]byte(`[]`))
			}
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
			_, _ = w.Write([]byte(`[
				{
					"title":"Flock",
					"titleSlug":"flock",
					"foreignBookId":"lookup-999",
					"author":{"authorName":"Kate Stewart","foreignAuthorId":"author-9"},
					"editions":[{"id":1,"foreignEditionId":"edition-1","title":"Flock","format":"Kindle Edition","links":null}]
				}
			]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":11,"name":"Any"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/metadataprofile":
			_, _ = w.Write([]byte(`[{"id":22,"name":"Standard"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/rootfolder":
			_, _ = w.Write([]byte(`[{"id":33,"path":"/books","accessible":true}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/book":
			added = true
			// Chaptarr resolves the submitted lookup id to its own canonical
			// work id at creation time.
			_, _ = w.Write([]byte(`{"id":51,"title":"Flock","foreignBookId":"canon-999","monitored":true}`))
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer chaptarrServer.Close()

	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	resp, err := svc.CreateMediaRequest(uid, &CreateRequest{
		MediaType:  "book",
		ForeignID:  "lookup-999",
		Title:      "Flock",
		BookFormat: BookFormatEbook,
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if resp.Status != StatusRequested {
		t.Fatalf("status = %s, want requested", resp.Status)
	}
	if resp.CanonicalForeignID != "canon-999" {
		t.Fatalf("canonical_foreign_id = %q, want canon-999", resp.CanonicalForeignID)
	}

	var recordID int
	if err := svc.db.QueryRow(
		"SELECT COALESCE(book_record_id, 0) FROM request_log WHERE foreign_id = 'lookup-999'",
	).Scan(&recordID); err != nil {
		t.Fatalf("read persisted record id: %v", err)
	}
	if recordID != 51 {
		t.Fatalf("book_record_id = %d, want 51", recordID)
	}

	st, err := svc.GetUserBookStatus(uid, "lookup-999")
	if err != nil {
		t.Fatalf("GetUserBookStatus: %v", err)
	}
	if st.BookFormats[BookFormatEbook] != StatusRequested {
		t.Fatalf("ebook = %q, want requested (resolved through the created record)", st.BookFormats[BookFormatEbook])
	}
	if st.CanonicalForeignID != "canon-999" {
		t.Fatalf("status canonical_foreign_id = %q, want canon-999", st.CanonicalForeignID)
	}
}

// TestBookRequestRecoversWhenFullTitleLookupMissesTheID reproduces the field
// failure: a requester finds a book in search, opens it, taps Request, and the
// add is refused because re-searching the *full* title no longer returns the
// very record the search produced. Chaptarr's lookup is fuzzy text matching, so
// a long title carrying a subtitle and parenthetical suffixes routinely misses.
// The exact foreignBookId is still the only thing that may satisfy the lookup.
func TestBookRequestRecoversWhenFullTitleLookupMissesTheID(t *testing.T) {
	const fullTitle = "10 Algorithms Every Forward Deployed Engineer Should Know: Foundational Data and Workflow Mapping (Part 1) (Guide to Forward-Deployed Engineering)"
	var terms []string
	var addBody map[string]any
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
			term := r.URL.Query().Get("term")
			terms = append(terms, term)
			if term == "10 Algorithms Every Forward Deployed Engineer Should Know" {
				_, _ = w.Write([]byte(`[
					{
						"title":"10 Algorithms Every Forward Deployed Engineer Should Know",
						"titleSlug":"10-algorithms",
						"foreignBookId":"fde-1",
						"author":{"authorName":"Tyler Wyselaskie","foreignAuthorId":"gr:99"},
						"editions":[{"foreignEditionId":"fde-1","title":"10 Algorithms","links":[],"images":[]}]
					}
				]`))
				return
			}
			// Every other term answers, but with other books — exactly the shape
			// that used to end the request with "book not found for foreign id".
			_, _ = w.Write([]byte(`[{"title":"Some Other Book","foreignBookId":"other-77"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"E-Book"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/metadataprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Standard"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/rootfolder":
			_, _ = w.Write([]byte(`[{"id":1,"path":"/books","accessible":true}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/book":
			if err := json.NewDecoder(r.Body).Decode(&addBody); err != nil {
				t.Errorf("decode add book body: %v", err)
			}
			_, _ = w.Write([]byte(`{"id":7,"title":"10 Algorithms","foreignBookId":"fde-1","monitored":true}`))
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer chaptarrServer.Close()

	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	resp, err := svc.CreateMediaRequest(uid, &CreateRequest{
		MediaType:  "book",
		ForeignID:  "fde-1",
		Title:      fullTitle,
		BookFormat: BookFormatEbook,
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if resp.Status != StatusRequested {
		t.Fatalf("status = %s, want requested (the book must be added and monitored)", resp.Status)
	}
	if addBody == nil {
		t.Fatal("AddBook was never called; the request was refused instead of added")
	}
	if len(terms) < 2 || terms[0] != "fde-1" || terms[1] != fullTitle {
		t.Fatalf("lookup terms = %#v, want the foreignBookId fetch first, then the exact title", terms)
	}
	if got := addBody["mediaType"]; got != "ebook" {
		t.Fatalf("mediaType = %v, want ebook", got)
	}
}

// TestBookRequestOnlyAcceptsAnExactForeignIDMatch guards the property that makes
// trying several search terms safe: no term may promote a different book.
func TestBookRequestOnlyAcceptsAnExactForeignIDMatch(t *testing.T) {
	var added bool
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
			// A near-miss: same title and author, different edition id.
			_, _ = w.Write([]byte(`[
				{"title":"Flock","foreignBookId":"other-edition","author":{"authorName":"A. Writer","foreignAuthorId":"gr:1"},"editions":[]}
			]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/book":
			added = true
			_, _ = w.Write([]byte(`{"id":1}`))
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer chaptarrServer.Close()

	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	resp, err := svc.CreateMediaRequest(uid, &CreateRequest{
		MediaType:  "book",
		ForeignID:  "wanted-edition",
		Title:      "Flock",
		BookFormat: BookFormatEbook,
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if added {
		t.Fatal("a lookup row with a different foreignBookId was added; only an exact id match may add")
	}
	if resp.Status != StatusPending {
		t.Fatalf("status = %s, want pending (unresolved metadata parks the request)", resp.Status)
	}
}

// TestBookRequestParksInsteadOfDroppingWhenMetadataUnresolved covers the floor:
// a request whose metadata record cannot be re-found must not evaporate with an
// error toast. It lands in the approval queue, where an admin can add the author
// in Chaptarr and approve it, or deny it — either way it is not forgotten.
func TestBookRequestParksInsteadOfDroppingWhenMetadataUnresolved(t *testing.T) {
	var added bool
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/book":
			added = true
			_, _ = w.Write([]byte(`{"id":1}`))
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer chaptarrServer.Close()

	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	resp, err := svc.CreateMediaRequest(uid, &CreateRequest{
		MediaType:  "book",
		ForeignID:  "ghost-1",
		Title:      "A Book The Provider Forgot",
		BookFormat: BookFormatEbook,
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest returned an error instead of parking the request: %v", err)
	}
	if added {
		t.Fatal("AddBook was called without a resolved metadata record")
	}
	if resp.Status != StatusPending {
		t.Fatalf("status = %s, want pending", resp.Status)
	}
	if resp.Message == "" {
		t.Fatal("parked request carried no message; the requester would read pending as normal approval")
	}
	if got := resp.BookFormats[BookFormatEbook]; got != StatusPending {
		t.Fatalf("book_formats[ebook] = %q, want pending", got)
	}
	var count int
	if err := svc.db.QueryRow(
		"SELECT COUNT(*) FROM request_log WHERE user_id=? AND foreign_id='ghost-1' AND media_type='book' AND book_format='ebook' AND status='pending'",
		uid,
	).Scan(&count); err != nil {
		t.Fatalf("count parked rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("parked pending rows = %d, want 1 (the request must survive the failed add)", count)
	}

	// This row goes to a human, so it belongs in the approval queue and in the
	// badge — but it is not a policy question, and rendered as one it invited an
	// Approve that replays the same failed add.
	pending, err := svc.ListPending()
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("ListPending rows = %d, want the parked row awaiting a decision", len(pending))
	}
	if pending[0].AddFailureReason != bookAddFailureMetadataUnresolved {
		t.Fatalf("add_failure_reason = %q, want %q — an ordinary-looking row is the defect", pending[0].AddFailureReason, bookAddFailureMetadataUnresolved)
	}
	// park_reason must stay NULL. It answers a different question (who owns the
	// row), and its NULL is the guard that keeps the sweep from bypassing
	// approval policy; a value here would hide this row from the queue.
	var parkReason sql.NullString
	if err := svc.db.QueryRow(
		"SELECT park_reason FROM request_log WHERE id = ?", pending[0].ID,
	).Scan(&parkReason); err != nil {
		t.Fatalf("read park_reason: %v", err)
	}
	if parkReason.Valid {
		t.Fatalf("park_reason = %q, want NULL (a human decides this one)", parkReason.String)
	}
	if waiting, err := svc.ListWaiting(); err != nil || len(waiting) != 0 {
		t.Fatalf("ListWaiting = %+v err=%v, want empty (the server is not retrying this)", waiting, err)
	}
	if count, err := svc.PendingCount(); err != nil || count != 1 {
		t.Fatalf("PendingCount = %d err=%v, want 1 (a person really must act)", count, err)
	}

	// Approving replays the same add against the same unresolved metadata. The
	// bare error read as a transient glitch; the admin needs the one action that
	// actually moves this.
	adminID := createTestAdmin(t, svc)
	_, approveErr := svc.ApproveRequest(adminID, pending[0].ID, nil)
	if approveErr == nil {
		t.Fatal("ApproveRequest succeeded; the metadata record is still unresolvable")
	}
	if !errors.Is(approveErr, ErrBookMetadataUnresolved) {
		t.Fatalf("approve error = %v, want it to wrap ErrBookMetadataUnresolved", approveErr)
	}
	if !strings.Contains(approveErr.Error(), "add this book in the library first") {
		t.Fatalf("approve error = %q, want the next step named", approveErr)
	}
}

// TestBookRequestParksWhenAuthorImportIsPending covers the 0.9.879+ Chaptarr
// behavior of queuing an unknown author for an asynchronous metadata import and
// rejecting the add until it lands. The park is server-owned: the requester is
// told "requested, finishes automatically", no admin surface counts or pages
// it, an early approval is refused with the plan, and the maintenance sweep
// completes it silently once the import lands.
func TestBookRequestParksWhenAuthorImportIsPending(t *testing.T) {
	var authorImported atomic.Bool
	var addSucceeded atomic.Bool
	var addAttempts atomic.Int32
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			// The added record appears in the live library only once the add
			// succeeded, so post-sweep status reads resolve live truth.
			if addSucceeded.Load() {
				_, _ = w.Write([]byte(`[{"id":42,"title":"The CEO Mindset","foreignBookId":"gr:253739298","monitored":true,"mediaType":"ebook","author":{"id":7,"authorName":"Shiv Shivakumar"},"authorId":7,"statistics":{"bookFileCount":0}}]`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
			_, _ = w.Write([]byte(`[
				{
					"title":"The CEO Mindset","titleSlug":"the-ceo-mindset","foreignBookId":"gr:253739298",
					"author":{"authorName":"Shiv Shivakumar","foreignAuthorId":"gr:21186439"},
					"editions":[{"foreignEditionId":"gr:e1","title":"The CEO Mindset","links":[],"images":[]}]
				}
			]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"E-Book"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/metadataprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Standard"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/rootfolder":
			_, _ = w.Write([]byte(`[{"id":1,"path":"/books","accessible":true}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/book":
			addAttempts.Add(1)
			if !authorImported.Load() {
				// The live 0.9.879 refusal, verbatim shape.
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`[{"propertyName":"Author","errorMessage":"Author 'Shiv Shivakumar' isn't available yet on our metadata server. It has been queued for import (pending ID: 3) and will be imported automatically when it becomes available."}]`))
				return
			}
			addSucceeded.Store(true)
			_, _ = w.Write([]byte(`{"id":42,"title":"The CEO Mindset","foreignBookId":"gr:253739298","monitored":true}`))
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer chaptarrServer.Close()

	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	rec := &recordingNotifier{}
	svc.notifier = rec
	resp, err := svc.CreateMediaRequest(uid, &CreateRequest{
		MediaType:  "book",
		ForeignID:  "gr:253739298",
		Title:      "The CEO Mindset",
		BookFormat: BookFormatEbook,
		SearchTerm: "the ceo mindset",
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest returned an error instead of parking the request: %v", err)
	}
	if addAttempts.Load() == 0 {
		t.Fatal("AddBook was never attempted; the park must come from the live refusal")
	}
	if resp.Status != StatusRequested {
		t.Fatalf("status = %s, want requested (pending would narrate an approval that is not happening)", resp.Status)
	}
	if got := resp.BookFormats[BookFormatEbook]; got != StatusRequested {
		t.Fatalf("book_formats[ebook] = %q, want requested", got)
	}
	if resp.Message != bookAuthorImportingMessage {
		t.Fatalf("message = %q, want the author-importing explanation", resp.Message)
	}
	// The message alone is a one-shot: it is shown once at submission and then
	// gone forever, leaving "requested" as the only durable word for a book the
	// library does not have. The wait is the durable half.
	createWait, ok := resp.BookFormatWaits[BookFormatEbook]
	if !ok {
		t.Fatalf("book_format_waits = %+v, want an ebook wait alongside the requested status", resp.BookFormatWaits)
	}
	if createWait.Reason != bookParkReasonAuthorImport {
		t.Fatalf("wait reason = %q, want %q", createWait.Reason, bookParkReasonAuthorImport)
	}
	if createWait.WaitingSince.IsZero() {
		t.Fatal("wait waiting_since is zero; the requester is owed how long this has been going")
	}
	if createWait.LastAttemptAt == nil {
		t.Fatal("wait last_attempt_at is absent; the failed add that parked the row IS an attempt this process made")
	}
	var requestID int64
	var storedSearch, storedPark string
	if err := svc.db.QueryRow(
		"SELECT id, COALESCE(search_term,''), COALESCE(park_reason,'') FROM request_log WHERE user_id=? AND foreign_id='gr:253739298' AND media_type='book' AND status='pending'",
		uid,
	).Scan(&requestID, &storedSearch, &storedPark); err != nil {
		t.Fatalf("read parked row: %v", err)
	}
	if storedSearch != "the ceo mindset" {
		t.Fatalf("parked search_term = %q, want the requester's search preserved for replay", storedSearch)
	}
	if storedPark != bookParkReasonAuthorImport {
		t.Fatalf("park_reason = %q, want %q", storedPark, bookParkReasonAuthorImport)
	}
	if len(rec.adminEvents) != 0 {
		t.Fatalf("admin events = %+v, want none (a server-owned park pages nobody)", rec.adminEvents)
	}
	if pending, err := svc.ListPending(); err != nil || len(pending) != 0 {
		t.Fatalf("ListPending = %+v err=%v, want empty (no decision exists)", pending, err)
	}
	if count, err := svc.PendingCount(); err != nil || count != 0 {
		t.Fatalf("PendingCount = %d err=%v, want 0", count, err)
	}
	status, err := svc.GetUserBookStatusForInstance(uid, "gr:253739298", "")
	if err != nil {
		t.Fatalf("GetUserBookStatusForInstance: %v", err)
	}
	if got := status.BookFormats[BookFormatEbook]; got != StatusRequested {
		t.Fatalf("status read = %q, want requested (pending would narrate an approval that is not happening)", got)
	}
	if _, ok := status.BookFormatWaits[BookFormatEbook]; !ok {
		t.Fatalf("status book_format_waits = %+v, want the wait to survive a fresh read, not just the submission", status.BookFormatWaits)
	}

	// The requester's own history carries the same explanation; scrolling back
	// to a request must not show it as finished work.
	history, err := svc.GetRequests(uid)
	if err != nil {
		t.Fatalf("GetRequests: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history rows = %d, want 1", len(history))
	}
	if history[0].Status != StatusRequested {
		t.Fatalf("history status = %q, want requested", history[0].Status)
	}
	if history[0].BookFormatWait == nil || history[0].BookFormatWait.Reason != bookParkReasonAuthorImport {
		t.Fatalf("history wait = %+v, want the author-import wait", history[0].BookFormatWait)
	}

	// The admin surface the first 24 hours previously had none of: informational,
	// separate from the approval queue, and never counted as work.
	waiting, err := svc.ListWaiting()
	if err != nil {
		t.Fatalf("ListWaiting: %v", err)
	}
	if len(waiting) != 1 {
		t.Fatalf("ListWaiting rows = %d, want the parked request visible to admins immediately", len(waiting))
	}
	if waiting[0].ID != requestID {
		t.Fatalf("ListWaiting id = %d, want the parked row %d", waiting[0].ID, requestID)
	}
	if waiting[0].WaitReason != bookParkReasonAuthorImport {
		t.Fatalf("ListWaiting wait_reason = %q, want %q", waiting[0].WaitReason, bookParkReasonAuthorImport)
	}
	if waiting[0].LastAttemptAt == nil {
		t.Fatal("ListWaiting last_attempt_at is absent; an admin cannot tell retrying from wedged without it")
	}
	if waiting[0].InstanceName == "" {
		t.Fatal("ListWaiting instance_name is empty; the admin needs to know which library is stuck")
	}

	// Approving before the import lands is a refused non-event: the row stays
	// parked and the admin is handed the plan.
	adminID := createTestAdmin(t, svc)
	if _, err := svc.ApproveRequest(adminID, requestID, nil); err == nil ||
		!strings.Contains(err.Error(), "completes automatically") {
		t.Fatalf("early ApproveRequest error = %v, want the still-importing plan", err)
	}

	// The import lands; one maintenance pass completes the request silently.
	authorImported.Store(true)
	rec.userEvents = nil
	svc.SweepParkedBookRequests()
	var finalStatus string
	var parkAfter sql.NullString
	var approvedBy sql.NullInt64
	if err := svc.db.QueryRow(
		"SELECT status, park_reason, approved_by FROM request_log WHERE id = ?", requestID,
	).Scan(&finalStatus, &parkAfter, &approvedBy); err != nil {
		t.Fatalf("read completed row: %v", err)
	}
	if finalStatus != StatusRequested {
		t.Fatalf("swept status = %q, want requested", finalStatus)
	}
	if parkAfter.Valid {
		t.Fatalf("park_reason = %q after completion, want NULL", parkAfter.String)
	}
	if approvedBy.Valid {
		t.Fatalf("approved_by = %d, want NULL (nobody decided a system completion)", approvedBy.Int64)
	}
	// Owner silence survives the durable wait, on the narrower ground that no
	// decision happened: the owner watches waiting become requested in-app and
	// still gets the content alert when the file lands. Non-owner waiters keep
	// their push.
	for _, ev := range rec.userEvents {
		if ev.userID == uid {
			t.Fatalf("owner received %+v; a system completion invents no approval", ev)
		}
	}
	status, err = svc.GetUserBookStatusForInstance(uid, "gr:253739298", "")
	if err != nil {
		t.Fatalf("GetUserBookStatusForInstance after sweep: %v", err)
	}
	if got := status.BookFormats[BookFormatEbook]; got != StatusRequested {
		t.Fatalf("post-sweep status = %q, want requested from live truth", got)
	}
	// The wait is an explanation for an absence. Once the library really holds
	// the record, the absence is gone and so is the explanation — otherwise the
	// app would keep apologising for a book it already has.
	if len(status.BookFormatWaits) != 0 {
		t.Fatalf("post-sweep book_format_waits = %+v, want none once live truth has the record", status.BookFormatWaits)
	}
	history, err = svc.GetRequests(uid)
	if err != nil {
		t.Fatalf("GetRequests after sweep: %v", err)
	}
	if len(history) != 1 || history[0].BookFormatWait != nil {
		t.Fatalf("post-sweep history = %+v, want the wait cleared", history)
	}
	if waiting, err := svc.ListWaiting(); err != nil || len(waiting) != 0 {
		t.Fatalf("post-sweep ListWaiting = %+v err=%v, want empty", waiting, err)
	}
}

// TestParkLastAttemptIsDerivedNotStored pins the reasoning that kept a column
// out of the schema. The sweep retries every parked row on every pass, so a
// per-row park_last_attempt_at could never differ from one process-level
// timestamp — it would only add a write to every parked row every five minutes
// against a pool holding a single connection.
//
// The one case that is not a max() is a park older than this process: its last
// real attempt was made by a predecessor and recorded nowhere, so the answer is
// "unknown", never the request time. Reporting a three-day-old request time as
// the last attempt would read as wedged when the next retry is 5 minutes out —
// the exact misreading this change exists to remove.
func TestParkLastAttemptIsDerivedNotStored(t *testing.T) {
	start := time.Date(2026, 8, 14, 20, 0, 0, 0, time.UTC)
	sweep := start.Add(42 * time.Minute)
	thisLife := start.Add(10 * time.Minute)
	previousLife := start.Add(-72 * time.Hour)

	for _, tc := range []struct {
		name        string
		lastSweep   time.Time
		requestedAt time.Time
		want        *time.Time
	}{
		{
			name:        "parked this process, no pass yet: the failed add is the attempt",
			requestedAt: thisLife,
			want:        &thisLife,
		},
		{
			name:        "a completed pass outranks the create attempt",
			lastSweep:   sweep,
			requestedAt: thisLife,
			want:        &sweep,
		},
		{
			name:        "a park created after the last pass has not been retried since",
			lastSweep:   start.Add(5 * time.Minute),
			requestedAt: thisLife,
			want:        &thisLife,
		},
		{
			name:        "park predates this process, no pass yet: unknown, not days ago",
			requestedAt: previousLife,
			want:        nil,
		},
		{
			name:        "park predates this process but a pass has run: that pass",
			lastSweep:   sweep,
			requestedAt: previousLife,
			want:        &sweep,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &Service{startedAt: start}
			if !tc.lastSweep.IsZero() {
				svc.markParkSweep(tc.lastSweep)
			}
			wait := svc.bookFormatWaitFor(bookParkReasonAuthorImport, tc.requestedAt)
			if !wait.WaitingSince.Equal(tc.requestedAt) {
				t.Fatalf("waiting_since = %s, want the row's own request time %s", wait.WaitingSince, tc.requestedAt)
			}
			switch {
			case tc.want == nil && wait.LastAttemptAt != nil:
				t.Fatalf("last_attempt_at = %s, want absent (this process cannot vouch for it)", wait.LastAttemptAt)
			case tc.want != nil && wait.LastAttemptAt == nil:
				t.Fatalf("last_attempt_at absent, want %s", tc.want)
			case tc.want != nil && !wait.LastAttemptAt.Equal(*tc.want):
				t.Fatalf("last_attempt_at = %s, want %s", wait.LastAttemptAt, tc.want)
			}
		})
	}
}

// TestSweepAdvancesTheReportedLastAttempt proves the derived timestamp actually
// moves when the loop runs: an admin reading a waiting row must be able to see
// that Cantinarr is still trying, which is the whole reason the row is shown.
func TestSweepAdvancesTheReportedLastAttempt(t *testing.T) {
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
			_, _ = w.Write([]byte(`[
				{
					"title":"The CEO Mindset","titleSlug":"the-ceo-mindset","foreignBookId":"gr:253739298",
					"author":{"authorName":"Shiv Shivakumar","foreignAuthorId":"gr:21186439"},
					"editions":[{"foreignEditionId":"gr:e1","title":"The CEO Mindset","links":[],"images":[]}]
				}
			]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"E-Book"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/metadataprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Standard"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/rootfolder":
			_, _ = w.Write([]byte(`[{"id":1,"path":"/books","accessible":true}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/book":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`[{"propertyName":"Author","errorMessage":"Author 'Shiv Shivakumar' isn't available yet on our metadata server. It has been queued for import (pending ID: 3) and will be imported automatically when it becomes available."}]`))
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer chaptarrServer.Close()

	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	if _, err := svc.CreateMediaRequest(uid, &CreateRequest{
		MediaType:  "book",
		ForeignID:  "gr:253739298",
		Title:      "The CEO Mindset",
		BookFormat: BookFormatEbook,
	}); err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}

	before, err := svc.ListWaiting()
	if err != nil || len(before) != 1 || before[0].LastAttemptAt == nil {
		t.Fatalf("ListWaiting before sweep = %+v err=%v, want one row with an attempt", before, err)
	}

	// The import is still pending, so the pass retries and leaves the park in
	// place — the state where "is anything happening?" is unanswerable without
	// this timestamp.
	svc.SweepParkedBookRequests()

	after, err := svc.ListWaiting()
	if err != nil || len(after) != 1 {
		t.Fatalf("ListWaiting after sweep = %+v err=%v, want the row still waiting", after, err)
	}
	if after[0].LastAttemptAt == nil || !after[0].LastAttemptAt.After(*before[0].LastAttemptAt) {
		t.Fatalf("last_attempt_at did not advance across a sweep: before=%s after=%v", before[0].LastAttemptAt, after[0].LastAttemptAt)
	}
	if !after[0].RequestedAt.Equal(before[0].RequestedAt) {
		t.Fatalf("waiting-since moved from %s to %s; the wait started when the requester asked", before[0].RequestedAt, after[0].RequestedAt)
	}
	// Still nobody's decision to make.
	if pending, err := svc.ListPending(); err != nil || len(pending) != 0 {
		t.Fatalf("ListPending = %+v err=%v, want the waiting row kept out of the actionable queue", pending, err)
	}
	if count, err := svc.PendingCount(); err != nil || count != 0 {
		t.Fatalf("PendingCount = %d err=%v, want 0 (a wait is not work)", count, err)
	}
}

// TestUnknownParkReasonStaysWithTheHumans is the guard the park_reason column
// never had. Its meaning — "the server owns this row" — lived only in a comment,
// and the two halves of the system read it differently: visibility treated any
// non-NULL value as server-owned, while the sweep only ever retried the literal
// 'author_import'. A row carrying any other value was hidden from the approval
// queue and the badge, listed under "Waiting for library" as being retried
// automatically, and touched by nothing — stranded under a label claiming it was
// handled, which is the failure the waiting list exists to prevent.
//
// Nothing writes such a value today. The point is that the next reason someone
// adds cannot strand a request by being added to one half and not the other:
// unrecognised means a person still sees it.
func TestUnknownParkReasonStaysWithTheHumans(t *testing.T) {
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer chaptarrServer.Close()

	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	instanceID := testInstanceID(t, svc)
	if _, err := svc.db.Exec(
		`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, instance_id, media_type, title, status, park_reason)
		 VALUES (?, 0, 'gr:future', 'ebook', ?, 'book', 'A Reason From The Future', ?, 'some_future_reason')`,
		uid, instanceID, StatusPending,
	); err != nil {
		t.Fatalf("insert unknown park: %v", err)
	}

	pending, err := svc.ListPending()
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 1 || pending[0].Title != "A Reason From The Future" {
		t.Fatalf("ListPending = %+v, want the row visible to a human — nothing else will touch it", pending)
	}
	if count, err := svc.PendingCount(); err != nil || count != 1 {
		t.Fatalf("PendingCount = %d err=%v, want 1; an uncounted row is one nobody is asked to look at", count, err)
	}
	// And it must NOT be advertised as work in progress, because no sweep pass
	// will ever pick it up.
	waiting, err := svc.ListWaiting()
	if err != nil {
		t.Fatalf("ListWaiting: %v", err)
	}
	if len(waiting) != 0 {
		t.Fatalf("ListWaiting = %+v, want empty: claiming a retry nothing performs is the defect", waiting)
	}

	// Prove the claim the lists are making. A full pass leaves the row exactly
	// as it was — no retry, no demotion — so the approval queue really is its
	// only route to a human.
	svc.SweepParkedBookRequests()
	var status, parkReason string
	if err := svc.db.QueryRow(
		"SELECT status, COALESCE(park_reason, '') FROM request_log WHERE foreign_id = 'gr:future'",
	).Scan(&status, &parkReason); err != nil {
		t.Fatalf("read row after sweep: %v", err)
	}
	if status != StatusPending || parkReason != "some_future_reason" {
		t.Fatalf("after sweep: status=%q park_reason=%q, want the row untouched", status, parkReason)
	}
	if pending, err := svc.ListPending(); err != nil || len(pending) != 1 {
		t.Fatalf("ListPending after sweep = %+v err=%v, want the row still with the humans", pending, err)
	}

	// The two lists partition the pending set: every row is in exactly one, so
	// no future reason can fall between them.
	var total int
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM request_log WHERE status = ?", StatusPending).Scan(&total); err != nil {
		t.Fatalf("count pending rows: %v", err)
	}
	queue, err := svc.ListPending()
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	held, err := svc.ListWaiting()
	if err != nil {
		t.Fatalf("ListWaiting: %v", err)
	}
	if len(queue)+len(held) != total {
		t.Fatalf("queue %d + waiting %d != %d pending rows; the filters are not complements", len(queue), len(held), total)
	}
}

// TestSweepDemotesParksWhoseProbeFailsBeyondTheImport pins the legacy-lane
// hand-off: on a build without the pending-import API (this stub answers the
// probe's lookup with nothing), a park whose replayed add fails for a reason
// other than the still-pending import demotes to an ordinary approval-queue
// row, firing the request_pending page that was withheld at park time — the
// moment a human decision first exists. There is deliberately NO age-based
// demotion to test: the wait itself never expires (Chaptarr's own retry loop
// is unbounded), which is why the decade-old park below demotes for the
// vanished record, not for its age.
func TestSweepDemotesParksWhoseProbeFailsBeyondTheImport(t *testing.T) {
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
			// The record has vanished from the provider: the retry fails with
			// unresolved metadata, which is not the pending import.
			_, _ = w.Write([]byte(`[]`))
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer chaptarrServer.Close()

	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	rec := &recordingNotifier{}
	svc.notifier = rec

	insertPark := func(foreignID, title, requestedAt string) int64 {
		res, err := svc.db.Exec(
			`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, instance_id, media_type, title, status, park_reason, requested_at)
			 VALUES (?, 0, ?, 'ebook', ?, 'book', ?, ?, ?, ?)`,
			uid, foreignID, testInstanceID(t, svc), title, StatusPending, bookParkReasonAuthorImport, requestedAt,
		)
		if err != nil {
			t.Fatalf("insert park: %v", err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	// One park is a decade old, one is fresh: both must demote the same way —
	// through the failed probe — proving age plays no part.
	oldID := insertPark("gr:old", "An Abandoned Wait", "2020-01-01 00:00:00")
	freshID := insertPark("gr:fresh", "A Vanished Record", "2049-01-01 00:00:00")
	if _, err := svc.db.Exec("UPDATE request_log SET requested_at = datetime('now') WHERE id = ?", freshID); err != nil {
		t.Fatalf("normalize requested_at: %v", err)
	}

	svc.SweepParkedBookRequests()

	for _, id := range []int64{oldID, freshID} {
		var status string
		var park sql.NullString
		if err := svc.db.QueryRow("SELECT status, park_reason FROM request_log WHERE id = ?", id).Scan(&status, &park); err != nil {
			t.Fatalf("read row %d: %v", id, err)
		}
		if status != StatusPending || park.Valid {
			t.Fatalf("row %d = status %q park %v, want an ordinary pending row", id, status, park)
		}
	}
	pending, err := svc.ListPending()
	if err != nil || len(pending) != 2 {
		t.Fatalf("ListPending = %+v err=%v, want both demoted rows visible", pending, err)
	}
	// Demotion is the hand-off, so it must be a move and not a copy: a row that
	// now needs a person must stop being advertised as something the server is
	// handling on its own.
	if waiting, err := svc.ListWaiting(); err != nil || len(waiting) != 0 {
		t.Fatalf("ListWaiting = %+v err=%v, want empty once both rows demoted", waiting, err)
	}
	// The hand-off carries its history. A request retried to exhaustion is not a
	// fresh decision, and the page fired below is the admin's first sight of it.
	for _, row := range pending {
		if row.AddFailureReason != bookAddFailureImportAbandoned {
			t.Fatalf("demoted row %d add_failure_reason = %q, want %q", row.ID, row.AddFailureReason, bookAddFailureImportAbandoned)
		}
	}
	pages := 0
	for _, ev := range rec.adminEvents {
		if ev.eventType == "request_pending" {
			pages++
		}
	}
	if pages != 2 {
		t.Fatalf("request_pending pages = %d (%+v), want one per demoted row", pages, rec.adminEvents)
	}
}

// TestDemotedParkApproveDoesNotPromiseAutomaticRetries pins the approve copy
// on a demoted row. While a row is parked, refusing an early approval with
// "completes automatically once the import lands" is the truth — the sweep is
// watching. A demoted row is no longer watched, so an approve that still hits
// the pending-import refusal must name the real verbs (Try again / close)
// instead of re-promising a watch that is not running.
func TestDemotedParkApproveDoesNotPromiseAutomaticRetries(t *testing.T) {
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
			_, _ = w.Write([]byte(`[
				{
					"title":"The CEO Mindset","titleSlug":"the-ceo-mindset","foreignBookId":"gr:253739298",
					"author":{"authorName":"Shiv Shivakumar","foreignAuthorId":"gr:21186439"},
					"editions":[{"foreignEditionId":"gr:e1","title":"The CEO Mindset","links":[],"images":[]}]
				}
			]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"E-Book"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/metadataprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Standard"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/rootfolder":
			_, _ = w.Write([]byte(`[{"id":1,"path":"/books","accessible":true}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/book":
			// The import never lands: every add attempt gets the live refusal.
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`[{"propertyName":"Author","errorMessage":"Author 'Shiv Shivakumar' isn't available yet on our metadata server. It has been queued for import (pending ID: 3) and will be imported automatically when it becomes available."}]`))
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer chaptarrServer.Close()

	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	rec := &recordingNotifier{}
	svc.notifier = rec

	res, err := svc.db.Exec(
		`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, instance_id, media_type, title, status, park_reason, search_term, requested_at)
		 VALUES (?, 0, 'gr:253739298', 'ebook', ?, 'book', 'The CEO Mindset', ?, ?, 'the ceo mindset', '2020-01-01 00:00:00')`,
		uid, testInstanceID(t, svc), StatusPending, bookParkReasonAuthorImport,
	)
	if err != nil {
		t.Fatalf("insert park: %v", err)
	}
	requestID, _ := res.LastInsertId()
	svc.demoteParkedBookRequest(requestID, bookAddFailureImportFailed)

	adminID := createTestAdmin(t, svc)
	_, err = svc.ApproveRequest(adminID, requestID, nil)
	if err == nil || !strings.Contains(err.Error(), "Try again") {
		t.Fatalf("post-demotion ApproveRequest error = %v, want the Try again / close copy", err)
	}
	if strings.Contains(err.Error(), "completes automatically") {
		t.Fatalf("post-demotion ApproveRequest error = %v, promises a watch that is not running", err)
	}
	// The refusal is a non-event: the row stays an ordinary pending decision
	// and is not silently re-parked back out of the admin's sight.
	var status string
	var park sql.NullString
	if err := svc.db.QueryRow("SELECT status, park_reason FROM request_log WHERE id = ?", requestID).Scan(&status, &park); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if status != StatusPending || park.Valid {
		t.Fatalf("row = status %q park %v, want an untouched ordinary pending row", status, park)
	}
}

// pendingImportAPIStub is a Chaptarr stub whose pending-import read API is
// live. Lookup answers any gr: id term with a record owned by one shared
// author, the add refuses with the author-pending validation failure while
// refuseAdds holds, and the pending-import endpoints record their hits so a
// test can prove what the server did — and did not — ask the arr to do.
type pendingImportAPIStub struct {
	server      *httptest.Server
	existsJSON  atomic.Value
	existsCode  atomic.Int32 // 0 means 200; set 409 for the ambiguity answer
	detailJSON  atomic.Value // GET /pendingauthorimport/{id}; "" means 404
	addAttempts atomic.Int32
	retryHits   atomic.Int32
	deleteHits  atomic.Int32
	refuseAdds  atomic.Bool
}

func newPendingImportAPIStub(t *testing.T) *pendingImportAPIStub {
	t.Helper()
	stub := &pendingImportAPIStub{}
	stub.refuseAdds.Store(true)
	stub.existsJSON.Store(`{"exists":false,"pending":true,"pendingId":3,"status":"Retrying","attemptCount":4}`)
	stub.detailJSON.Store("")
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		switch {
		case r.Method == http.MethodGet && path == "/api/v1/book":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && path == "/api/v1/book/lookup":
			term := r.URL.Query().Get("term")
			if !strings.HasPrefix(term, "gr:") {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[
				{
					"title":"Waiting Book","titleSlug":"waiting-book","foreignBookId":"` + term + `",
					"foreignAuthorId":"gr:21186439",
					"author":{"authorName":"Shiv Shivakumar","foreignAuthorId":"gr:21186439"},
					"editions":[{"foreignEditionId":"gr:e1","title":"Waiting Book","links":[],"images":[]}]
				}
			]`))
		case r.Method == http.MethodGet && path == "/api/v1/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"E-Book"}]`))
		case r.Method == http.MethodGet && path == "/api/v1/metadataprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Standard"}]`))
		case r.Method == http.MethodGet && path == "/api/v1/rootfolder":
			_, _ = w.Write([]byte(`[{"id":1,"path":"/books","accessible":true}]`))
		case r.Method == http.MethodPost && path == "/api/v1/book":
			stub.addAttempts.Add(1)
			if stub.refuseAdds.Load() {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`[{"propertyName":"Author","errorMessage":"Author 'Shiv Shivakumar' isn't available yet on our metadata server. It has been queued for import (pending ID: 3) and will be imported automatically when it becomes available."}]`))
				return
			}
			_, _ = w.Write([]byte(`{"id":42,"title":"Waiting Book","foreignBookId":"gr:253739298","monitored":true}`))
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/api/v1/pendingauthorimport/author/exists/"):
			if code := stub.existsCode.Load(); code != 0 {
				w.WriteHeader(int(code))
				_, _ = w.Write([]byte(`{"message":"resolves to multiple local authors"}`))
				return
			}
			_, _ = w.Write([]byte(stub.existsJSON.Load().(string)))
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/api/v1/pendingauthorimport/"):
			detail := stub.detailJSON.Load().(string)
			if detail == "" {
				http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(detail))
		case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/v1/pendingauthorimport/") && strings.HasSuffix(path, "/retry"):
			stub.retryHits.Add(1)
			_, _ = w.Write([]byte(`{"message":"Retry scheduled"}`))
		case r.Method == http.MethodDelete && strings.HasPrefix(path, "/api/v1/pendingauthorimport/"):
			stub.deleteHits.Add(1)
			_, _ = w.Write([]byte(`{"message":"Pending import cancelled"}`))
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

// insertAuthorImportPark plants one server-owned park directly, so probe tests
// exercise the sweep without replaying the whole create flow.
func insertAuthorImportPark(t *testing.T, svc *Service, uid int64, foreignID, title string) int64 {
	t.Helper()
	res, err := svc.db.Exec(
		`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, instance_id, media_type, title, status, park_reason, search_term)
		 VALUES (?, 0, ?, 'ebook', ?, 'book', ?, ?, ?, ?)`,
		uid, foreignID, testInstanceID(t, svc), title, StatusPending, bookParkReasonAuthorImport, title,
	)
	if err != nil {
		t.Fatalf("insert park: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// TestSweepWatchesChaptarrsOwnImportRetries pins the core of the alignment:
// while Chaptarr's pending import is still active, a sweep pass reads that
// answer and does NOTHING else — no replayed add (which would merge into the
// arr's pending row and force-bump its own retry schedule), no demotion, no
// admin page. Chaptarr owns the retry; Cantinarr watches.
func TestSweepWatchesChaptarrsOwnImportRetries(t *testing.T) {
	stub := newPendingImportAPIStub(t)
	svc, uid := newChaptarrBookTestService(t, stub.server.URL)
	rec := &recordingNotifier{}
	svc.notifier = rec

	if _, err := svc.CreateMediaRequest(uid, &CreateRequest{
		MediaType:  "book",
		ForeignID:  "gr:253739298",
		Title:      "Waiting Book",
		BookFormat: BookFormatEbook,
		SearchTerm: "waiting book",
	}); err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	addsAtPark := stub.addAttempts.Load()
	if addsAtPark == 0 {
		t.Fatal("the park must come from a live add refusal")
	}

	svc.SweepParkedBookRequests()
	svc.SweepParkedBookRequests()

	if got := stub.addAttempts.Load(); got != addsAtPark {
		t.Fatalf("add attempts = %d after two sweeps, want %d: the sweep must read the pending import, not replay the add", got, addsAtPark)
	}
	var park sql.NullString
	if err := svc.db.QueryRow(
		"SELECT park_reason FROM request_log WHERE foreign_id = 'gr:253739298'",
	).Scan(&park); err != nil {
		t.Fatalf("read park: %v", err)
	}
	if !park.Valid || park.String != bookParkReasonAuthorImport {
		t.Fatalf("park_reason = %v, want the row still watched", park)
	}
	for _, ev := range rec.adminEvents {
		if ev.eventType == "request_pending" {
			t.Fatalf("admin events = %+v, want no page while the arr is still importing", rec.adminEvents)
		}
	}
}

// TestSweepDemotesOnChaptarrsDeclaredVerdicts pins the exits the probe acts
// on, labelled by what actually happened in the arr (verified against
// Chaptarr's source): a declared-terminal failure demotes as failed; a cancel
// in the arr's UI — which Chaptarr records as Failed with LastError
// "Cancelled by user", it has no cancelled status — demotes as cancelled; a
// concluded row (PartialSuccess/Succeeded) whose author never landed demotes
// as failed because the arr's scheduler will never touch it again; an
// ambiguous author id (409) demotes because every future sweep reads the same
// answer; and a vanished row (removed out-of-band) demotes as cancelled. All
// demote immediately — a verdict exists, so a human sees it now, not after
// any timer.
func TestSweepDemotesOnChaptarrsDeclaredVerdicts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		existsJSON string
		existsCode int
		detailJSON string
		wantReason string
	}{
		{
			name:       "declared failed",
			existsJSON: `{"exists":false,"pending":true,"pendingId":3,"status":"Failed","attemptCount":12}`,
			detailJSON: `{"id":3,"overallStatus":"Failed","lastError":"Author lookup returned a typed 404"}`,
			wantReason: bookAddFailureImportFailed,
		},
		{
			name:       "cancelled in the arr",
			existsJSON: `{"exists":false,"pending":true,"pendingId":3,"status":"Failed","attemptCount":12}`,
			detailJSON: `{"id":3,"overallStatus":"Failed","lastError":"Cancelled by user"}`,
			wantReason: bookAddFailureImportCancelled,
		},
		{
			// The row's LastError is unreadable (older build without the by-id
			// route): the failed label is the fail-closed default.
			name:       "declared failed, detail unreadable",
			existsJSON: `{"exists":false,"pending":true,"pendingId":3,"status":"Failed","attemptCount":12}`,
			detailJSON: "",
			wantReason: bookAddFailureImportFailed,
		},
		{
			name:       "concluded without the author",
			existsJSON: `{"exists":false,"pending":true,"pendingId":3,"status":"PartialSuccess","attemptCount":12}`,
			wantReason: bookAddFailureImportFailed,
		},
		{
			name:       "ambiguous author id",
			existsCode: 409,
			wantReason: bookAddFailureImportFailed,
		},
		{
			name:       "vanished",
			existsJSON: `{"exists":false,"pending":false}`,
			wantReason: bookAddFailureImportCancelled,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := newPendingImportAPIStub(t)
			if tc.existsJSON != "" {
				stub.existsJSON.Store(tc.existsJSON)
			}
			stub.existsCode.Store(int32(tc.existsCode))
			stub.detailJSON.Store(tc.detailJSON)
			svc, uid := newChaptarrBookTestService(t, stub.server.URL)
			rec := &recordingNotifier{}
			svc.notifier = rec
			requestID := insertAuthorImportPark(t, svc, uid, "gr:111", "A Judged Wait")

			svc.SweepParkedBookRequests()

			var park, failure sql.NullString
			var status string
			if err := svc.db.QueryRow(
				"SELECT status, park_reason, add_failure_reason FROM request_log WHERE id = ?", requestID,
			).Scan(&status, &park, &failure); err != nil {
				t.Fatalf("read row: %v", err)
			}
			if status != StatusPending || park.Valid || !failure.Valid || failure.String != tc.wantReason {
				t.Fatalf("row = status %q park %v failure %v, want a demoted row with %q", status, park, failure, tc.wantReason)
			}
			pages := 0
			for _, ev := range rec.adminEvents {
				if ev.eventType == "request_pending" {
					pages++
				}
			}
			if pages != 1 {
				t.Fatalf("request_pending pages = %d, want the demotion to page once", pages)
			}
		})
	}
}

// TestSweepCompletesOncePendingImportSucceeds: the author landing (the exists
// endpoint's library answer) is what triggers the fulfill, which completes the
// request through the normal add path.
func TestSweepCompletesOncePendingImportSucceeds(t *testing.T) {
	stub := newPendingImportAPIStub(t)
	stub.existsJSON.Store(`{"exists":true,"authorId":7,"authorName":"Shiv Shivakumar","pending":false}`)
	stub.refuseAdds.Store(false)
	svc, uid := newChaptarrBookTestService(t, stub.server.URL)
	requestID := insertAuthorImportPark(t, svc, uid, "gr:253739298", "Waiting Book")

	svc.SweepParkedBookRequests()

	var park sql.NullString
	var status string
	if err := svc.db.QueryRow(
		"SELECT status, park_reason FROM request_log WHERE id = ?", requestID,
	).Scan(&status, &park); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if status == StatusPending || park.Valid {
		t.Fatalf("row = status %q park %v, want the request completed once the author landed", status, park)
	}
}

// TestExtendBookWaitResumesTheWatch: the admin's "try again" on a demoted row
// re-parks it for the sweep, clears the failure note, and — because this wait
// ended in the arr's declared-terminal Failed state — asks Chaptarr to reopen
// the import, which the arr never does on its own.
func TestExtendBookWaitResumesTheWatch(t *testing.T) {
	stub := newPendingImportAPIStub(t)
	stub.existsJSON.Store(`{"exists":false,"pending":true,"pendingId":3,"status":"Failed","attemptCount":12}`)
	svc, uid := newChaptarrBookTestService(t, stub.server.URL)
	requestID := insertAuthorImportPark(t, svc, uid, "gr:222", "A Second Chance")
	if _, err := svc.db.Exec(
		"UPDATE request_log SET park_reason = NULL, add_failure_reason = ? WHERE id = ?",
		bookAddFailureImportFailed, requestID,
	); err != nil {
		t.Fatalf("demote row: %v", err)
	}

	adminID := createTestAdmin(t, svc)
	resp, err := svc.ExtendBookWait(adminID, requestID)
	if err != nil {
		t.Fatalf("ExtendBookWait: %v", err)
	}
	if resp.Status != StatusRequested || !strings.Contains(resp.Message, "Waiting resumed") {
		t.Fatalf("resp = %+v, want the waiting-resumed confirmation", resp)
	}
	var park, failure sql.NullString
	if err := svc.db.QueryRow(
		"SELECT park_reason, add_failure_reason FROM request_log WHERE id = ?", requestID,
	).Scan(&park, &failure); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if !park.Valid || park.String != bookParkReasonAuthorImport || failure.Valid {
		t.Fatalf("row = park %v failure %v, want a watched park with the failure note cleared", park, failure)
	}
	if got := stub.retryHits.Load(); got != 1 {
		t.Fatalf("chaptarr retry hits = %d, want the failed import asked to reopen exactly once", got)
	}

	// An ordinary pending row is a decision, not a wait: no resume verb.
	res, err := svc.db.Exec(
		`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, instance_id, media_type, title, status)
		 VALUES (?, 0, 'gr:333', 'ebook', ?, 'book', 'A Real Decision', ?)`,
		uid, testInstanceID(t, svc), StatusPending,
	)
	if err != nil {
		t.Fatalf("insert ordinary pending row: %v", err)
	}
	ordinaryID, _ := res.LastInsertId()
	if _, err := svc.ExtendBookWait(adminID, ordinaryID); err == nil {
		t.Fatal("ExtendBookWait accepted an ordinary pending row")
	}
}

// TestDenyCancelsQueuedAuthorImport: closing the request is the one exit that
// must reach into the arr — the queued import carries the add intent (the
// monitored book, the search flag), so left armed it would deliver the book
// whenever the import lands, contradicting the denial. The one guard: a
// sibling request still waiting on the same author keeps the import alive.
func TestDenyCancelsQueuedAuthorImport(t *testing.T) {
	stub := newPendingImportAPIStub(t)
	svc, uid := newChaptarrBookTestService(t, stub.server.URL)
	adminID := createTestAdmin(t, svc)

	// Two books, one author (the stub files every gr: id under the same
	// author): denying the first must leave the import queued for the second.
	firstID := insertAuthorImportPark(t, svc, uid, "gr:one", "First Book")
	secondID := insertAuthorImportPark(t, svc, uid, "gr:two", "Second Book")

	if err := svc.DenyRequest(adminID, firstID, "not this one"); err != nil {
		t.Fatalf("deny first: %v", err)
	}
	if got := stub.deleteHits.Load(); got != 0 {
		t.Fatalf("cancel hits = %d after first denial, want 0: a sibling still waits on this author", got)
	}

	if err := svc.DenyRequest(adminID, secondID, "and not this one"); err != nil {
		t.Fatalf("deny second: %v", err)
	}
	if got := stub.deleteHits.Load(); got != 1 {
		t.Fatalf("cancel hits = %d after last denial, want the queued import cancelled exactly once", got)
	}
}

// TestReportBookImportStallsTransitionsPerInstance drives the stall sink: a
// park past the stall horizon reports unhealthy with its waiting title, and
// once no stalled parks remain the same instance reports healthy so the issue
// auto-resolves.
func TestReportBookImportStallsTransitionsPerInstance(t *testing.T) {
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer chaptarrServer.Close()

	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	sink := &recordingStallSink{}
	svc.SetBookImportStallSink(sink)
	instanceID := testInstanceID(t, svc)

	if _, err := svc.db.Exec(
		`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, instance_id, media_type, title, status, park_reason, requested_at)
		 VALUES (?, 0, 'gr:stuck', 'ebook', ?, 'book', 'A Stuck Book', ?, ?, datetime('now', '-2 days'))`,
		uid, instanceID, StatusPending, bookParkReasonAuthorImport,
	); err != nil {
		t.Fatalf("insert stalled park: %v", err)
	}

	svc.reportBookImportStalls()
	if len(sink.calls) != 1 {
		t.Fatalf("sink calls = %+v, want one per chaptarr instance", sink.calls)
	}
	if sink.calls[0].healthy || len(sink.calls[0].titles) != 1 || sink.calls[0].titles[0] != "A Stuck Book" {
		t.Fatalf("stall call = %+v, want unhealthy with the waiting title", sink.calls[0])
	}
	if sink.calls[0].instanceID != instanceID {
		t.Fatalf("stall instance = %q, want %q", sink.calls[0].instanceID, instanceID)
	}

	// The park cleared (denied here); the next pass reports healthy.
	if _, err := svc.db.Exec("UPDATE request_log SET status = ? WHERE foreign_id = 'gr:stuck'", StatusDenied); err != nil {
		t.Fatalf("clear park: %v", err)
	}
	sink.calls = nil
	svc.reportBookImportStalls()
	if len(sink.calls) != 1 || !sink.calls[0].healthy {
		t.Fatalf("post-clear calls = %+v, want one healthy transition", sink.calls)
	}
}

// TestReportBookImportStallsDoesNotDeadlockTheSingleConnection reproduces the
// 2026-08-01 production deadlock: the pool is capped at one connection
// (SQLite is single-writer), so reporting from inside an open instance cursor
// left the sink's transaction waiting for a connection that could never be
// freed. The goroutine hung forever holding that connection, and every later
// query in the process — token refreshes above all — blocked behind it, which
// is why the server went dark a few minutes after every restart while
// static files and token-invalid requests still answered instantly.
//
// The sink here does real transactional DB work, exactly like the production
// issue store. On the buggy code this test times out instead of passing.
func TestReportBookImportStallsDoesNotDeadlockTheSingleConnection(t *testing.T) {
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer chaptarrServer.Close()

	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	if _, err := svc.db.Exec(
		`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, instance_id, media_type, title, status, park_reason, requested_at)
		 VALUES (?, 0, 'gr:stuck', 'ebook', ?, 'book', 'A Stuck Book', ?, ?, datetime('now', '-2 days'))`,
		uid, testInstanceID(t, svc), StatusPending, bookParkReasonAuthorImport,
	); err != nil {
		t.Fatalf("insert stalled park: %v", err)
	}
	sink := &transactionalStallSink{db: svc.db}
	svc.SetBookImportStallSink(sink)

	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.reportBookImportStalls()
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("reportBookImportStalls deadlocked: it reported while its instance cursor still held the pool's only connection")
	}
	if sink.calls() == 0 {
		t.Fatal("sink was never called; the reporter must still report after draining its cursor")
	}
}

// transactionalStallSink mirrors the production sink's shape: it opens a
// transaction, which is precisely the operation that cannot obtain a
// connection while a caller's cursor is still open.
type transactionalStallSink struct {
	db    *sql.DB
	mu    sync.Mutex
	count int
}

func (s *transactionalStallSink) RecordBookImportStall(instanceID, instanceName string, titles []string, healthy bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow("SELECT COUNT(*) FROM issues").Scan(&n); err != nil {
		return err
	}
	s.mu.Lock()
	s.count++
	s.mu.Unlock()
	return tx.Commit()
}

func (s *transactionalStallSink) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}

type stallCall struct {
	instanceID   string
	instanceName string
	titles       []string
	healthy      bool
}

type recordingStallSink struct {
	calls []stallCall
}

func (r *recordingStallSink) RecordBookImportStall(instanceID, instanceName string, titles []string, healthy bool) error {
	r.calls = append(r.calls, stallCall{instanceID: instanceID, instanceName: instanceName, titles: titles, healthy: healthy})
	return nil
}

// testInstanceID returns the id of the test fixture's single Chaptarr instance.
func testInstanceID(t *testing.T, s *Service) string {
	t.Helper()
	var id string
	if err := s.db.QueryRow("SELECT id FROM service_instances WHERE service_type = 'chaptarr'").Scan(&id); err != nil {
		t.Fatalf("read chaptarr instance id: %v", err)
	}
	return id
}

// TestBookRequestLookupTransportFailureStaysAnError separates "the provider does
// not know this book" from "the provider could not be asked". Only the former is
// parked; an unreachable Chaptarr must still tell the requester to retry.
func TestBookRequestLookupTransportFailureStaysAnError(t *testing.T) {
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/book" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
			return
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer chaptarrServer.Close()

	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	_, err := svc.CreateMediaRequest(uid, &CreateRequest{
		MediaType:  "book",
		ForeignID:  "unreachable-1",
		Title:      "Flock",
		BookFormat: BookFormatEbook,
	})
	if err == nil {
		t.Fatal("CreateMediaRequest succeeded; a lookup that could not be performed must not be parked as pending")
	}
	if errors.Is(err, ErrBookMetadataUnresolved) {
		t.Fatalf("error = %v, want a lookup failure rather than an unresolved-metadata verdict", err)
	}
	var count int
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM request_log WHERE foreign_id='unreachable-1'").Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("request_log rows = %d, want 0", count)
	}
}

func TestBookLookupTermsTryIDThenSearchThenTitleThenHeadline(t *testing.T) {
	terms := bookLookupTerms("gr:297977925", "Ten Algorithms: A Guide (Part 1) (A Series)", "a guide part 1")
	want := []string{
		"gr:297977925",
		"a guide part 1",
		"Ten Algorithms: A Guide (Part 1) (A Series)",
		"Ten Algorithms",
	}
	if len(terms) != len(want) {
		t.Fatalf("terms = %#v, want %#v", terms, want)
	}
	for i := range want {
		if terms[i] != want[i] {
			t.Fatalf("terms[%d] = %q, want %q (full list %#v)", i, terms[i], want[i], terms)
		}
	}
	// A title that is already its own headline must not be searched twice, and a
	// search term equal to the title must not be repeated either.
	if got := bookLookupTerms("", "Flock", ""); len(got) != 1 || got[0] != "Flock" {
		t.Fatalf("terms = %#v, want a single Flock term", got)
	}
	if got := bookLookupTerms("", "Flock", "flock"); len(got) != 1 {
		t.Fatalf("terms = %#v, want the duplicate search term collapsed", got)
	}
	// A search term that happens to equal the headline collapses too: the point
	// is to search each distinct string once, not to preserve a fixed shape.
	if got := bookLookupTerms("", "Ten Algorithms: A Guide", "ten algorithms"); len(got) != 2 {
		t.Fatalf("terms = %#v, want search term + full title only", got)
	}
	// A requester who pasted the id as their search collapses onto the id fetch.
	if got := bookLookupTerms("gr:1", "A Book", "gr:1"); len(got) != 2 {
		t.Fatalf("terms = %#v, want id + title only", got)
	}
}

func TestMainBookTitleReducesToTheSearchableHeadline(t *testing.T) {
	cases := map[string]string{
		"Ten Algorithms: A Guide (Part 1) (A Series)": "Ten Algorithms",
		"Ahsoka (Star Wars)":                          "Ahsoka",
		"Flock":                                       "Flock",
		"  Dune: Messiah  ":                           "Dune",
		"(Untitled)":                                  "(Untitled)",
		":leading colon":                              ":leading colon",
	}
	for input, want := range cases {
		if got := mainBookTitle(input); got != want {
			t.Fatalf("mainBookTitle(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestPendingBookApprovalReplaysTheRequestersSearchTerm covers the household
// where approval is required: the add happens minutes or days after the search,
// under an admin who never typed anything. The term that found the book has to
// survive on the row, or approval would fall back to searching the full title —
// the exact query that fails for the titles this whole path exists to rescue.
func TestPendingBookApprovalReplaysTheRequestersSearchTerm(t *testing.T) {
	const fullTitle = "Ten Algorithms: Foundational Data and Workflow Mapping (Part 1) (A Series)"
	var terms []string
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
			term := r.URL.Query().Get("term")
			terms = append(terms, term)
			if term != "ten algorithms every" {
				// Only the requester's own search returns this record, exactly as
				// the provider behaved when they found it.
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[
				{"title":"Ten Algorithms","titleSlug":"ten","foreignBookId":"replay-1","author":{"authorName":"A. Writer","foreignAuthorId":"gr:5"},"editions":[{"foreignEditionId":"replay-1","links":[],"images":[]}]}
			]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"E-Book"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/metadataprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Standard"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/rootfolder":
			_, _ = w.Write([]byte(`[{"id":1,"path":"/books","accessible":true}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/book":
			_, _ = w.Write([]byte(`{"id":11,"title":"Ten Algorithms","foreignBookId":"replay-1","monitored":true}`))
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer chaptarrServer.Close()

	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	_, instanceID, err := svc.resolveChaptarr(uid, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.createPending(&resolvedRequest{
		userID:     uid,
		mediaType:  "book",
		foreignID:  "replay-1",
		title:      fullTitle,
		bookFormat: BookFormatEbook,
		instanceID: instanceID,
		searchTerm: "ten algorithms every",
	}); err != nil {
		t.Fatal(err)
	}

	adminID := createTestAdmin(t, svc)
	var requestID int64
	if err := svc.db.QueryRow("SELECT id FROM request_log WHERE foreign_id = 'replay-1' AND status = 'pending'").Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	resp, err := svc.ApproveRequest(adminID, requestID, nil)
	if err != nil {
		t.Fatalf("ApproveRequest: %v", err)
	}
	if resp.Status != StatusRequested {
		t.Fatalf("approval status = %s, want requested", resp.Status)
	}
	if len(terms) < 2 || terms[0] != "replay-1" || terms[1] != "ten algorithms every" {
		t.Fatalf("approval lookup terms = %#v, want the id fetch then the requester's stored search", terms)
	}
}

// TestBookRequestDeepLinkResolvesByIdFetchAlone encodes the live-verified
// Chaptarr behavior (0.9.720): lookup answers a foreignBookId term with an
// exact fetch of that record. A notification tap or deep link carries no search
// term and its stored title may defeat the fuzzy search entirely — the id fetch
// is what makes those requests deterministic, Radarr-style.
func TestBookRequestDeepLinkResolvesByIdFetchAlone(t *testing.T) {
	var terms []string
	var addBody map[string]any
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
			term := r.URL.Query().Get("term")
			terms = append(terms, term)
			if term != "gr:424242" {
				// Every text search misses, exactly like the field failure.
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[
				{"title":"An Unsearchable Title: With Subtitle (Part 9) (Series)","titleSlug":"unsearchable","foreignBookId":"gr:424242","author":{"authorName":"A. Writer","foreignAuthorId":"gr:7"},"editions":[{"foreignEditionId":"e1","links":[],"images":[]}]}
			]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"E-Book"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/metadataprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Standard"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/rootfolder":
			_, _ = w.Write([]byte(`[{"id":1,"path":"/books","accessible":true}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/book":
			if err := json.NewDecoder(r.Body).Decode(&addBody); err != nil {
				t.Errorf("decode add book body: %v", err)
			}
			_, _ = w.Write([]byte(`{"id":9,"title":"An Unsearchable Title","foreignBookId":"gr:424242","monitored":true}`))
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer chaptarrServer.Close()

	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	resp, err := svc.CreateMediaRequest(uid, &CreateRequest{
		MediaType:  "book",
		ForeignID:  "gr:424242",
		Title:      "An Unsearchable Title: With Subtitle (Part 9) (Series)",
		BookFormat: BookFormatEbook,
		// No SearchTerm: this arrival had no search behind it.
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if resp.Status != StatusRequested {
		t.Fatalf("status = %s, want requested via the id fetch", resp.Status)
	}
	if len(terms) == 0 || terms[0] != "gr:424242" {
		t.Fatalf("lookup terms = %#v, want the id fetch tried first", terms)
	}
	if addBody == nil {
		t.Fatal("AddBook was not called")
	}
}

// TestBookRequestAliasIdIsNotSubstitutedByCanonicalSibling encodes the second
// live-verified behavior: the provider keeps two works for one title and
// resolves an id fetch of the alias to the CANONICAL sibling. The exact-id gate
// must refuse that substitute — the requester chose a specific row — and the
// requester's own search term then re-finds the alias row itself.
func TestBookRequestAliasIdIsNotSubstitutedByCanonicalSibling(t *testing.T) {
	var addBody map[string]any
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
			switch r.URL.Query().Get("term") {
			case "gr:297978618":
				// Id-fetching the alias returns the canonical sibling, as the
				// live provider does.
				_, _ = w.Write([]byte(`[
					{"title":"Part One","titleSlug":"part-one","foreignBookId":"gr:297977925","author":{"authorName":"A. Writer","foreignAuthorId":"gr:7"},"editions":[{"foreignEditionId":"c1","links":[],"images":[]}]}
				]`))
			case "ten algorithms":
				// The requester's own search returns both rows, alias included.
				_, _ = w.Write([]byte(`[
					{"title":"Part One","titleSlug":"part-one","foreignBookId":"gr:297977925","author":{"authorName":"A. Writer","foreignAuthorId":"gr:7"},"editions":[{"foreignEditionId":"c1","links":[],"images":[]}]},
					{"title":"Part One","titleSlug":"part-one-2","foreignBookId":"gr:297978618","author":{"authorName":"A. Writer","foreignAuthorId":"gr:7"},"editions":[{"foreignEditionId":"a1","links":[],"images":[]}]}
				]`))
			default:
				_, _ = w.Write([]byte(`[]`))
			}
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"E-Book"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/metadataprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Standard"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/rootfolder":
			_, _ = w.Write([]byte(`[{"id":1,"path":"/books","accessible":true}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/book":
			if err := json.NewDecoder(r.Body).Decode(&addBody); err != nil {
				t.Errorf("decode add book body: %v", err)
			}
			_, _ = w.Write([]byte(`{"id":13,"title":"Part One","foreignBookId":"gr:297978618","monitored":true}`))
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer chaptarrServer.Close()

	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	resp, err := svc.CreateMediaRequest(uid, &CreateRequest{
		MediaType:  "book",
		ForeignID:  "gr:297978618",
		Title:      "Part One",
		BookFormat: BookFormatEbook,
		SearchTerm: "ten algorithms",
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if resp.Status != StatusRequested {
		t.Fatalf("status = %s, want requested", resp.Status)
	}
	if addBody == nil {
		t.Fatal("AddBook was not called")
	}
	if got := addBody["foreignBookId"]; got != "gr:297978618" {
		t.Fatalf("added foreignBookId = %v, want the alias row the requester chose, never the canonical substitute", got)
	}
}

// TestBookRequestAliasAttachesToCanonicalRecordAlreadyInLibrary is the sequel
// to the no-substitute rule above: once the library DOES track the canonical
// record, a request for its alias id must complete that record, not create a
// twin. The provider's id fetch is the authority linking the two ids; a
// requester tapping the duplicate listing means "I want this book", not
// "track it twice".
func TestBookRequestAliasAttachesToCanonicalRecordAlreadyInLibrary(t *testing.T) {
	var monitorBody map[string]any
	addCalls := 0
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			// The canonical record is already tracked: an unmonitored ebook.
			_, _ = w.Write([]byte(`[
				{"id":21,"title":"Part One","foreignBookId":"gr:297977925","mediaType":"ebook","monitored":false,"authorId":7,"author":{"id":7,"authorName":"A. Writer"},"statistics":{"bookFileCount":0}}
			]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
			if r.URL.Query().Get("term") == "gr:297978618" {
				// Id-fetching the alias returns the canonical sibling.
				_, _ = w.Write([]byte(`[
					{"title":"Part One","titleSlug":"part-one","foreignBookId":"gr:297977925","author":{"authorName":"A. Writer","foreignAuthorId":"gr:7"},"editions":[{"foreignEditionId":"c1","links":[],"images":[]}]}
				]`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/book/monitor":
			if err := json.NewDecoder(r.Body).Decode(&monitorBody); err != nil {
				t.Errorf("decode monitor body: %v", err)
			}
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/command":
			_, _ = w.Write([]byte(`{"id":1}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/book":
			addCalls++
			_, _ = w.Write([]byte(`{"id":99,"title":"Part One","foreignBookId":"gr:297978618","monitored":true}`))
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer chaptarrServer.Close()

	svc, uid := newChaptarrBookTestService(t, chaptarrServer.URL)
	resp, err := svc.CreateMediaRequest(uid, &CreateRequest{
		MediaType:  "book",
		ForeignID:  "gr:297978618",
		Title:      "Part One",
		BookFormat: BookFormatEbook,
		SearchTerm: "ten algorithms",
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if resp.Status != StatusRequested {
		t.Fatalf("status = %s, want requested via the existing record", resp.Status)
	}
	if addCalls != 0 {
		t.Fatalf("AddBook was called %d times; the existing canonical record must be completed, not duplicated", addCalls)
	}
	if monitorBody == nil {
		t.Fatal("the existing canonical record was not monitored")
	}
	ids, _ := monitorBody["bookIds"].([]any)
	if len(ids) != 1 || ids[0] != float64(21) {
		t.Fatalf("monitored bookIds = %v, want the canonical record 21", monitorBody["bookIds"])
	}
	// The client asked with the alias id; the response must re-address it to
	// the id the library will report from now on, and the history row must
	// carry the fulfilling record id so status reads survive re-keying.
	if resp.CanonicalForeignID != "gr:297977925" {
		t.Fatalf("canonical_foreign_id = %q, want gr:297977925", resp.CanonicalForeignID)
	}
	var loggedForeignID string
	var recordID int
	if err := svc.db.QueryRow(
		"SELECT foreign_id, COALESCE(book_record_id, 0) FROM request_log WHERE user_id = ? AND media_type = 'book'",
		uid,
	).Scan(&loggedForeignID, &recordID); err != nil {
		t.Fatal(err)
	}
	if loggedForeignID != "gr:297978618" {
		t.Fatalf("logged foreign_id = %q, want the id the requester used", loggedForeignID)
	}
	if recordID != 21 {
		t.Fatalf("logged book_record_id = %d, want the canonical record 21", recordID)
	}
}
