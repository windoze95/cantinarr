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
			_, _ = w.Write([]byte(`[{"id":1,"path":"/books","accessible":true}]`))
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
