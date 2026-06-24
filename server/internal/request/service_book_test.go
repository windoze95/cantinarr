package request

import (
	"testing"

	"github.com/windoze95/cantinarr-server/internal/db"
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

// TestBookRequestStatusAndDedup covers the request_log book path: status is
// keyed by foreignBookId, a duplicate pending request does not create a second
// row, distinct books are independent, and a directly-logged book reads back as
// requested.
func TestBookRequestStatusAndDedup(t *testing.T) {
	s, uid := newBookTestService(t)
	const fid = "goodreads:12345"

	if st, err := s.GetUserBookStatus(uid, fid); err != nil || st.Status != StatusUnavailable {
		t.Fatalf("empty status = %+v err=%v, want unavailable", st, err)
	}

	r := &resolvedRequest{userID: uid, mediaType: "book", foreignID: fid, title: "Some Book"}
	if _, err := s.createPending(r); err != nil {
		t.Fatalf("createPending: %v", err)
	}
	if st, _ := s.GetUserBookStatus(uid, fid); st.Status != StatusPending {
		t.Fatalf("status after pending = %s, want pending", st.Status)
	}

	// A duplicate pending request must NOT create a second row.
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
		t.Fatalf("pending book rows = %d, want 1 (dedup by foreign_id)", count)
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
