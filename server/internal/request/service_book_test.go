package request

import (
	"bytes"
	"encoding/json"
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
	if id, ok := selectBookQualityProfile([]chaptarr.QualityProfile{{ID: 4, Name: "Only"}}); !ok || id != 4 {
		t.Fatalf("single quality = %d ok=%v", id, ok)
	}
	if id, ok := selectBookQualityProfile([]chaptarr.QualityProfile{{ID: 1, Name: "Books"}, {ID: 2, Name: "DEFAULT"}}); !ok || id != 2 {
		t.Fatalf("default quality = %d ok=%v", id, ok)
	}
	if _, ok := selectBookQualityProfile([]chaptarr.QualityProfile{{ID: 1, Name: "One"}, {ID: 2, Name: "Two"}}); ok {
		t.Fatal("ambiguous quality profiles were guessed")
	}
	if id, ok := selectBookMetadataProfile([]chaptarr.MetadataProfile{{ID: 7, Name: "Default"}, {ID: 8, Name: "Other"}}); !ok || id != 7 {
		t.Fatalf("default metadata = %d ok=%v", id, ok)
	}
	if _, ok := selectBookMetadataProfile([]chaptarr.MetadataProfile{{ID: 7, Name: "Default"}, {ID: 8, Name: "default"}}); ok {
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
