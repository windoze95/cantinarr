package request

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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

	r := &resolvedRequest{userID: uid, mediaType: "book", foreignID: fid, title: "Some Book"}
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

func TestBookRequestFormatMonitorsRequestedEditions(t *testing.T) {
	var addBody map[string]any
	chaptarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
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
						{"id":1,"foreignEditionId":"edition-ebook","titleSlug":"ebook","title":"Kindle Edition","format":"Kindle Edition"},
						{"id":2,"foreignEditionId":"edition-audio","titleSlug":"audio","title":"Audiobook","format":"Audible Audio"}
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
			_, _ = w.Write([]byte(`{"id":9,"title":"Star Wars: Heir to the Empire","foreignBookId":"book-123"}`))
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
	if got := addBody["anyEditionOk"]; got != false {
		t.Fatalf("anyEditionOk = %v, want false for audiobook-only", got)
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
	editions, ok := addBody["editions"].([]any)
	if !ok || len(editions) != 2 {
		t.Fatalf("editions = %#v, want 2 editions", addBody["editions"])
	}
	ebook := editions[0].(map[string]any)
	audio := editions[1].(map[string]any)
	if ebook["monitored"] != false {
		t.Fatalf("ebook monitored = %v, want false", ebook["monitored"])
	}
	if audio["monitored"] != true {
		t.Fatalf("audiobook monitored = %v, want true", audio["monitored"])
	}
	if audio["manualAdd"] != true {
		t.Fatalf("audiobook manualAdd = %v, want true", audio["manualAdd"])
	}
	if audio["foreignEditionId"] != "edition-audio" || audio["titleSlug"] != "audio" {
		t.Fatalf("audiobook edition = %#v, want foreignEditionId/titleSlug preserved", audio)
	}

	var stored string
	if err := svc.db.QueryRow(
		"SELECT COALESCE(book_format, '') FROM request_log WHERE user_id = ? AND foreign_id = ?",
		uid,
		"book-123",
	).Scan(&stored); err != nil {
		t.Fatalf("read stored format: %v", err)
	}
	if stored != BookFormatAudiobook {
		t.Fatalf("stored book_format = %q, want %q", stored, BookFormatAudiobook)
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
