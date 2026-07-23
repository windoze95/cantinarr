package request

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

// verifiedBookUpstream is a stateful Chaptarr double. Unlike the old request
// fixtures, POST /book is only an acknowledgement: the service must discover
// the local row, read authoritative editions, write the complete selection,
// monitor it, verify every read-back, and obtain a command acknowledgement.
type verifiedBookUpstream struct {
	mu sync.Mutex

	title           string
	foreignBookID   string
	authorName      string
	foreignAuthorID string
	author          chaptarr.Author
	authorExists    bool
	rows            map[int]*verifiedBookRow
	rowOrder        []int
	nextBookID      int
	nextEditionID   int

	seedBodies    []map[string]any
	bookPutIDs    []int
	monitorIDs    []int
	searchBookIDs []int
	searchCalls   int

	duplicatePocket           bool
	seedBothFormats           bool
	physicalOnly              map[string]bool
	catalogAlwaysBusy         bool
	commandDelay              time.Duration
	leanAuthorList            bool
	dropMonitorWrite          bool
	invalidSearchAck          bool
	searchAppearsAfterMonitor bool
	activeSearch              bool
	activeSearchBookID        int
	driftIdentityOnRead       bool
	failAddFormat             map[string]bool
	rejectAddStatus           map[string]int
	dropAddResponse           map[string]bool
	dropAddWithoutCommit      map[string]bool
	addResponseStatus         map[string]int
	dropSearchResponse        bool
	searchResponseStatus      int
	queueFailure              bool
	bookListStatus            int
	qualityProfileStatus      int
	emptyQualityProfiles      bool
	authorReadFailures        int
	authorReadStatus          int
	authorUpdateStatus        int
	bookUpdateStatus          int
	monitorUpdateStatus       int
	lookupResults             []map[string]any
}

type verifiedBookRow struct {
	book     chaptarr.Book
	editions []chaptarr.Edition
}

func newVerifiedBookUpstream(title, foreignBookID string) *verifiedBookUpstream {
	return &verifiedBookUpstream{
		title:           title,
		foreignBookID:   foreignBookID,
		authorName:      "Mara Vale",
		foreignAuthorID: "hc:author-2001",
		author: chaptarr.Author{
			ID:                         2101,
			AuthorName:                 "Mara Vale",
			ForeignAuthorID:            "hc:author-2001",
			Monitored:                  true,
			EbookQualityProfileID:      11,
			AudiobookQualityProfileID:  12,
			EbookMetadataProfileID:     21,
			AudiobookMetadataProfileID: 22,
			EbookRootFolderPath:        "/library/ebooks",
			AudiobookRootFolderPath:    "/library/audiobooks",
		},
		rows:                 make(map[int]*verifiedBookRow),
		nextBookID:           4101,
		nextEditionID:        5101,
		physicalOnly:         make(map[string]bool),
		failAddFormat:        make(map[string]bool),
		rejectAddStatus:      make(map[string]int),
		dropAddResponse:      make(map[string]bool),
		dropAddWithoutCommit: make(map[string]bool),
		addResponseStatus:    make(map[string]int),
	}
}

func (u *verifiedBookUpstream) addExisting(format string, monitored bool) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.authorExists = true
	return u.addRowLocked(format, monitored, false)
}

func (u *verifiedBookUpstream) addRowLocked(format string, monitored, pocketWithoutRequestedEdition bool) int {
	bookID := u.nextBookID
	u.nextBookID++
	releaseDate := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	book := chaptarr.Book{
		ID:                 bookID,
		AuthorID:           u.author.ID,
		Title:              u.title,
		TitleSlug:          fallbackTitleSlug(u.title),
		ForeignBookID:      u.foreignBookID,
		ForeignEditionID:   fmt.Sprintf("local-edition-%d", bookID),
		MediaType:          format,
		ReleaseDate:        &releaseDate,
		Images:             []chaptarr.Image{{CoverType: "cover", URL: "/cover.jpg"}},
		Monitored:          monitored,
		AnyEditionOk:       false,
		EbookMonitored:     monitored && format == BookFormatEbook,
		AudiobookMonitored: monitored && format == BookFormatAudiobook,
	}
	editionFormat := "Ebook"
	if format == BookFormatAudiobook {
		editionFormat = "Audiobook"
	}
	if pocketWithoutRequestedEdition || u.physicalOnly[format] {
		editionFormat = "Physical"
	}
	edition := chaptarr.Edition{
		ID:               u.nextEditionID,
		BookID:           bookID,
		ForeignEditionID: fmt.Sprintf("edition-%d", u.nextEditionID),
		Title:            u.title,
		Format:           editionFormat,
		Language:         "English",
		Images:           []chaptarr.Image{},
		Monitored:        monitored && editionFormat != "Physical",
		ManualAdd:        monitored && editionFormat != "Physical",
	}
	u.nextEditionID++
	u.rows[bookID] = &verifiedBookRow{book: book, editions: []chaptarr.Edition{edition}}
	u.rowOrder = append(u.rowOrder, bookID)
	return bookID
}

func (u *verifiedBookUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	defer u.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
		if u.lookupResults != nil {
			_ = json.NewEncoder(w).Encode(u.lookupResults)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"title": u.title, "titleSlug": fallbackTitleSlug(u.title), "foreignBookId": u.foreignBookID,
			"author": map[string]any{"authorName": u.authorName, "foreignAuthorId": u.foreignAuthorID},
			// Lookup editions are intentionally misleading discovery hints. The
			// mutation must ignore isEbook and use local /edition format truth.
			"editions": []map[string]any{{
				"foreignEditionId": "lookup-only", "isEbook": false, "format": nil,
				"links": []any{}, "images": []any{}, "futureEditionField": "keep-me",
			}},
		}})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/qualityprofile":
		if u.qualityProfileStatus != 0 {
			http.Error(w, "synthetic quality-profile failure", u.qualityProfileStatus)
			return
		}
		if u.emptyQualityProfiles {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`[{"id":11,"name":"Ebook","profileType":"ebook"},{"id":12,"name":"Audiobook","profileType":"audiobook"}]`))
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/metadataprofile":
		_, _ = w.Write([]byte(`[{"id":21,"name":"Ebook","profileType":2},{"id":22,"name":"Audiobook","profileType":1}]`))
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/rootfolder":
		_, _ = w.Write([]byte(`[{"id":31,"path":"/library/ebooks","accessible":true,"isEffectiveDefaultEbook":true},{"id":32,"path":"/library/audiobooks","accessible":true,"isEffectiveDefaultAudiobook":true}]`))
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/author":
		if u.authorExists {
			if u.leanAuthorList {
				_ = json.NewEncoder(w).Encode([]map[string]any{{"id": u.author.ID, "authorName": u.author.AuthorName, "foreignAuthorId": u.author.ForeignAuthorID}})
			} else {
				_ = json.NewEncoder(w).Encode([]chaptarr.Author{u.author})
			}
		} else {
			_, _ = w.Write([]byte(`[]`))
		}
	case strings.HasPrefix(r.URL.Path, "/api/v1/author/"):
		u.serveAuthor(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/book":
		u.serveAddBook(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
		if u.bookListStatus != 0 {
			http.Error(w, "synthetic book-list failure", u.bookListStatus)
			return
		}
		_ = json.NewEncoder(w).Encode(u.booksLocked())
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/queue":
		if u.queueFailure {
			http.Error(w, "synthetic queue failure", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"totalRecords":0,"records":[]}`))
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/edition":
		bookID, _ := strconv.Atoi(r.URL.Query().Get("bookId"))
		row := u.rows[bookID]
		if row == nil {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(row.editions)
	case r.Method == http.MethodPut && r.URL.Path == "/api/v1/book/monitor":
		u.serveMonitor(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/book/"):
		u.serveBook(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/command":
		if u.commandDelay > 0 {
			time.Sleep(u.commandDelay)
		}
		if (u.searchAppearsAfterMonitor && len(u.monitorIDs) > 0) || (u.activeSearch && len(u.rows) > 0) {
			bookID := 0
			if len(u.monitorIDs) > 0 {
				bookID = u.monitorIDs[0]
			} else if u.activeSearchBookID > 0 {
				bookID = u.activeSearchBookID
			} else {
				for id := range u.rows {
					bookID = id
					break
				}
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 92, "name": "BookSearch", "status": "started", "body": map[string]any{"bookIds": []int{bookID}}}})
		} else if u.catalogAlwaysBusy {
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 91, "name": "RefreshAuthor", "status": "started", "body": map[string]any{"authorId": u.author.ID}}})
		} else {
			_, _ = w.Write([]byte(`[]`))
		}
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/command":
		u.serveBookSearch(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (u *verifiedBookUpstream) booksLocked() []chaptarr.Book {
	books := make([]chaptarr.Book, 0, len(u.rows))
	for _, id := range u.rowOrder {
		if row := u.rows[id]; row != nil {
			books = append(books, row.book)
		}
	}
	return books
}

func (u *verifiedBookUpstream) serveAddBook(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var captured map[string]any
	_ = json.Unmarshal(body, &captured)
	u.seedBodies = append(u.seedBodies, captured)
	var add chaptarr.AddBookRequest
	if err := json.Unmarshal(body, &add); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(add.Editions) == 0 {
		http.Error(w, "editions are required", http.StatusUnprocessableEntity)
		return
	}
	if status := u.rejectAddStatus[add.MediaType]; status != 0 {
		http.Error(w, "synthetic add rejection", status)
		return
	}
	if u.failAddFormat[add.MediaType] {
		http.Error(w, "synthetic add failure", http.StatusUnprocessableEntity)
		return
	}
	if u.dropAddWithoutCommit[add.MediaType] {
		dropHTTPResponse(w)
		return
	}
	if add.AuthorID > 0 && add.AuthorID != u.author.ID {
		http.Error(w, "wrong author", http.StatusConflict)
		return
	}
	u.authorExists = true
	u.author.EbookMonitorFuture = add.Author.EbookMonitorFuture
	u.author.AudiobookMonitorFuture = add.Author.AudiobookMonitorFuture
	bookID := u.addRowLocked(add.MediaType, false, false)
	if u.duplicatePocket {
		u.addRowLocked(add.MediaType, false, true)
	}
	if u.seedBothFormats && len(u.rows) == 1 {
		otherFormat := BookFormatAudiobook
		if add.MediaType == BookFormatAudiobook {
			otherFormat = BookFormatEbook
		}
		u.addRowLocked(otherFormat, false, false)
	}
	if status := u.addResponseStatus[add.MediaType]; status != 0 {
		w.WriteHeader(status)
		return
	}
	if u.dropAddResponse[add.MediaType] {
		dropHTTPResponse(w)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": bookID, "authorId": u.author.ID, "title": u.title,
		"foreignBookId": u.foreignBookID, "mediaType": add.MediaType, "monitored": false,
	})
}

func (u *verifiedBookUpstream) serveAuthor(w http.ResponseWriter, r *http.Request) {
	if !u.authorExists {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		if u.authorReadStatus != 0 {
			http.Error(w, "synthetic author read failure", u.authorReadStatus)
			return
		}
		if u.authorReadFailures > 0 {
			u.authorReadFailures--
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(u.author)
		return
	}
	if r.Method != http.MethodPut {
		http.NotFound(w, r)
		return
	}
	var updated chaptarr.Author
	if err := json.NewDecoder(r.Body).Decode(&updated); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if u.authorUpdateStatus != 0 {
		http.Error(w, "synthetic author update rejection", u.authorUpdateStatus)
		return
	}
	u.author.Monitored = updated.Monitored
	u.author.EbookMonitorFuture = updated.EbookMonitorFuture
	u.author.AudiobookMonitorFuture = updated.AudiobookMonitorFuture
	_ = json.NewEncoder(w).Encode(u.author)
}

func (u *verifiedBookUpstream) serveBook(w http.ResponseWriter, r *http.Request) {
	bookID, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/v1/book/"))
	row := u.rows[bookID]
	if row == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		if u.driftIdentityOnRead {
			row.book.ForeignBookID = "different-work"
		}
		book := row.book
		book.Editions = nil // Chaptarr's book resource is not edition truth.
		_ = json.NewEncoder(w).Encode(book)
		return
	}
	if r.Method != http.MethodPut {
		http.NotFound(w, r)
		return
	}
	var updated chaptarr.Book
	if err := json.NewDecoder(r.Body).Decode(&updated); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if u.bookUpdateStatus != 0 {
		http.Error(w, "synthetic book update rejection", u.bookUpdateStatus)
		return
	}
	u.bookPutIDs = append(u.bookPutIDs, bookID)
	row.book.AnyEditionOk = updated.AnyEditionOk
	row.editions = updated.Editions
	_ = json.NewEncoder(w).Encode(row.book)
}

func (u *verifiedBookUpstream) serveMonitor(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BookIDs   []int `json:"bookIds"`
		Monitored bool  `json:"monitored"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if u.monitorUpdateStatus != 0 {
		http.Error(w, "synthetic monitor rejection", u.monitorUpdateStatus)
		return
	}
	u.monitorIDs = append(u.monitorIDs, body.BookIDs...)
	if !u.dropMonitorWrite {
		for _, id := range body.BookIDs {
			if row := u.rows[id]; row != nil {
				row.book.Monitored = body.Monitored
				row.book.EbookMonitored = body.Monitored && row.book.MediaType == BookFormatEbook
				row.book.AudiobookMonitored = body.Monitored && row.book.MediaType == BookFormatAudiobook
			}
		}
	}
	w.WriteHeader(http.StatusAccepted)
}

func (u *verifiedBookUpstream) serveBookSearch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string `json:"name"`
		BookIDs []int  `json:"bookIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	u.searchCalls++
	u.searchBookIDs = append([]int(nil), body.BookIDs...)
	if u.searchResponseStatus != 0 {
		http.Error(w, "synthetic search rejection", u.searchResponseStatus)
		return
	}
	if u.dropSearchResponse {
		dropHTTPResponse(w)
		return
	}
	if u.invalidSearchAck {
		_, _ = w.Write([]byte(`{"id":0,"name":"BookSearch","status":"unknown"}`))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": 7000 + u.searchCalls, "name": "BookSearch", "status": "queued", "body": map[string]any{"bookIds": body.BookIDs},
	})
}

func dropHTTPResponse(w http.ResponseWriter) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		panic("test response writer cannot hijack")
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		panic(err)
	}
	_ = conn.Close()
}

func newVerifiedMutationService(t *testing.T, upstream *verifiedBookUpstream) (*Service, int64) {
	t.Helper()
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	service, userID := newChaptarrBookTestService(t, server.URL)
	service.bookMutationTimeout = 250 * time.Millisecond
	service.bookSettleInterval = time.Millisecond
	return service, userID
}

func TestVerifiedBookMutationSelectsAuthoritativeRequestedFormat(t *testing.T) {
	for _, format := range []string{BookFormatEbook, BookFormatAudiobook} {
		t.Run(format, func(t *testing.T) {
			upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
			service, userID := newVerifiedMutationService(t, upstream)
			response, err := service.CreateMediaRequest(userID, &CreateRequest{
				MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: format,
			})
			if err != nil {
				t.Fatalf("CreateMediaRequest: %v", err)
			}
			if response.Status != StatusRequested || response.BookFormats[format] != StatusRequested {
				t.Fatalf("response = %#v, want verified requested", response)
			}

			upstream.mu.Lock()
			defer upstream.mu.Unlock()
			if len(upstream.seedBodies) != 1 {
				t.Fatalf("seed calls = %d, want 1", len(upstream.seedBodies))
			}
			seed := upstream.seedBodies[0]
			if seed["monitored"] != false || seed["ebookMonitored"] != false || seed["audiobookMonitored"] != false {
				t.Fatalf("seed monitoring = %#v, want all false", seed)
			}
			if options := seed["addOptions"].(map[string]any); options["searchForNewBook"] != false {
				t.Fatalf("seed addOptions = %#v, want no search", options)
			}
			if editions, ok := seed["editions"].([]any); !ok || len(editions) == 0 {
				t.Fatalf("seed editions = %#v, want lookup contract editions", seed["editions"])
			} else {
				edition, ok := editions[0].(map[string]any)
				if !ok || edition["foreignEditionId"] != "lookup-only" || edition["isEbook"] != false ||
					edition["format"] != nil || edition["futureEditionField"] != "keep-me" {
					t.Fatalf("seed edition changed shape: %#v", editions[0])
				}
				if links, ok := edition["links"].([]any); !ok || len(links) != 0 {
					t.Fatalf("seed edition links = %#v", edition["links"])
				}
				if images, ok := edition["images"].([]any); !ok || len(images) != 0 {
					t.Fatalf("seed edition images = %#v", edition["images"])
				}
			}
			if author := seed["author"].(map[string]any); author["monitorNewItems"] != "none" {
				t.Fatalf("seed author = %#v, want monitorNewItems none", author)
			}
			if len(upstream.monitorIDs) != 1 || len(upstream.searchBookIDs) != 1 || upstream.monitorIDs[0] != upstream.searchBookIDs[0] {
				t.Fatalf("monitor=%v search=%v, want one identical selected row", upstream.monitorIDs, upstream.searchBookIDs)
			}
			row := upstream.rows[upstream.monitorIDs[0]]
			if row == nil || len(row.editions) != 1 || chaptarrEditionFormat(row.editions[0]) != format || !row.editions[0].Monitored || !row.editions[0].ManualAdd {
				t.Fatalf("selected row = %#v, want one monitored %s edition", row, format)
			}
		})
	}
}

func TestVerifiedBookMutationDistinguishesConfigurationAbsenceFromFetchFailure(t *testing.T) {
	t.Run("upstream failure is not reported as invalid configuration", func(t *testing.T) {
		upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
		upstream.qualityProfileStatus = http.StatusBadGateway
		service, userID := newVerifiedMutationService(t, upstream)
		_, err := service.CreateMediaRequest(userID, &CreateRequest{
			MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatAudiobook,
		})
		if err == nil || errors.Is(err, ErrBookConfigurationInvalid) {
			t.Fatalf("error = %v, want transport failure without ErrBookConfigurationInvalid", err)
		}
	})

	t.Run("successful empty catalog is invalid configuration", func(t *testing.T) {
		upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
		upstream.emptyQualityProfiles = true
		service, userID := newVerifiedMutationService(t, upstream)
		_, err := service.CreateMediaRequest(userID, &CreateRequest{
			MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatAudiobook,
		})
		if !errors.Is(err, ErrBookConfigurationInvalid) {
			t.Fatalf("error = %v, want ErrBookConfigurationInvalid", err)
		}
	})
}

func TestVerifiedBookMutationChoosesUsableDuplicatePocket(t *testing.T) {
	upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
	upstream.duplicatePocket = true
	service, userID := newVerifiedMutationService(t, upstream)
	if _, err := service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatAudiobook,
	}); err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if len(upstream.monitorIDs) != 1 {
		t.Fatalf("monitor ids = %v, want one pocket", upstream.monitorIDs)
	}
	selected := upstream.rows[upstream.monitorIDs[0]]
	if selected == nil || chaptarrEditionFormat(selected.editions[0]) != BookFormatAudiobook {
		t.Fatalf("selected pocket = %#v, want pocket with usable audiobook", selected)
	}
}

func TestVerifiedBookMutationRanksMultiWorkAcrossDuplicatePockets(t *testing.T) {
	t.Run("safe pocket wins even when bundle pocket arrives first", func(t *testing.T) {
		upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
		safeID := upstream.addExisting(BookFormatAudiobook, false)
		bundleID := upstream.addExisting(BookFormatAudiobook, false)
		upstream.mu.Lock()
		upstream.rows[bundleID].editions[0].Title = "The Clockwork Orchard: Books 1-3"
		upstream.rowOrder = []int{bundleID, safeID}
		upstream.mu.Unlock()

		service, userID := newVerifiedMutationService(t, upstream)
		if _, err := service.CreateMediaRequest(userID, &CreateRequest{
			MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatAudiobook,
		}); err != nil {
			t.Fatalf("CreateMediaRequest: %v", err)
		}

		upstream.mu.Lock()
		defer upstream.mu.Unlock()
		if len(upstream.monitorIDs) != 1 || upstream.monitorIDs[0] != safeID {
			t.Fatalf("monitor ids = %v, want only safe pocket %d", upstream.monitorIDs, safeID)
		}
	})

	t.Run("blank-title safe pocket wins over stronger bundle title", func(t *testing.T) {
		upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
		safeID := upstream.addExisting(BookFormatAudiobook, false)
		bundleID := upstream.addExisting(BookFormatAudiobook, false)
		upstream.mu.Lock()
		upstream.rows[safeID].editions[0].Title = ""
		upstream.rows[bundleID].editions[0].Title = "The Clockwork Orchard: Books 1-3"
		upstream.rowOrder = []int{bundleID, safeID}
		upstream.mu.Unlock()

		service, userID := newVerifiedMutationService(t, upstream)
		if _, err := service.CreateMediaRequest(userID, &CreateRequest{
			MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatAudiobook,
		}); err != nil {
			t.Fatalf("CreateMediaRequest: %v", err)
		}

		upstream.mu.Lock()
		defer upstream.mu.Unlock()
		if len(upstream.monitorIDs) != 1 || upstream.monitorIDs[0] != safeID {
			t.Fatalf("monitor ids = %v, want only safe blank-title pocket %d", upstream.monitorIDs, safeID)
		}
	})

	t.Run("bundle-only pocket is rejected", func(t *testing.T) {
		upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
		bundleID := upstream.addExisting(BookFormatAudiobook, false)
		upstream.mu.Lock()
		upstream.rows[bundleID].editions[0].Title = "The Clockwork Orchard: Books 1-3"
		upstream.mu.Unlock()

		service, userID := newVerifiedMutationService(t, upstream)
		_, err := service.CreateMediaRequest(userID, &CreateRequest{
			MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatAudiobook,
		})
		if !errors.Is(err, ErrBookMultiWorkUnsupported) {
			t.Fatalf("error = %v, want ErrBookMultiWorkUnsupported", err)
		}
		if len(upstream.bookPutIDs) != 0 || len(upstream.monitorIDs) != 0 || upstream.searchCalls != 0 {
			t.Fatalf("bundle-only pocket mutated put=%v monitor=%v search=%d", upstream.bookPutIDs, upstream.monitorIDs, upstream.searchCalls)
		}
	})
}

func TestVerifiedBookMutationRejectsPhysicalAsAudiobook(t *testing.T) {
	upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
	upstream.physicalOnly[BookFormatAudiobook] = true
	upstream.addExisting(BookFormatAudiobook, false)
	service, userID := newVerifiedMutationService(t, upstream)
	_, err := service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatAudiobook,
	})
	if !errors.Is(err, ErrBookEditionUnavailable) {
		t.Fatalf("error = %v, want ErrBookEditionUnavailable", err)
	}
	if len(upstream.monitorIDs) != 0 || upstream.searchCalls != 0 {
		t.Fatalf("physical fallback mutated monitor=%v search=%d", upstream.monitorIDs, upstream.searchCalls)
	}
}

func TestVerifiedBookMutationDoesNotRejectWhileDuplicatePocketIsPlaceholder(t *testing.T) {
	upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
	upstream.physicalOnly[BookFormatAudiobook] = true
	upstream.addExisting(BookFormatAudiobook, false)
	placeholderID := upstream.addExisting(BookFormatAudiobook, false)
	upstream.mu.Lock()
	placeholder := upstream.rows[placeholderID]
	placeholder.book.ReleaseDate = nil
	placeholder.book.Images = nil
	placeholder.book.ForeignEditionID = "default-placeholder"
	placeholder.editions = nil
	upstream.mu.Unlock()
	service, userID := newVerifiedMutationService(t, upstream)
	service.bookMutationTimeout = 20 * time.Millisecond
	_, err := service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatAudiobook,
	})
	if !errors.Is(err, ErrBookOutcomePending) {
		t.Fatalf("error = %v, want durable outcome_pending while one pocket remains a placeholder", err)
	}
}

func TestVerifiedBookMutationIgnoresPhysicalSiblingBesideValidAudiobook(t *testing.T) {
	upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
	audioID := upstream.addExisting(BookFormatAudiobook, false)
	upstream.physicalOnly["physical"] = true
	physicalID := upstream.addExisting("physical", false)
	upstream.rows[physicalID].book.Title = "The Clockwork Orchard (Hardcover)"
	service, userID := newVerifiedMutationService(t, upstream)
	if _, err := service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatAudiobook,
	}); err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if len(upstream.seedBodies) != 0 {
		t.Fatalf("valid audiobook plus physical sibling caused %d seed calls", len(upstream.seedBodies))
	}
	if len(upstream.monitorIDs) != 1 || upstream.monitorIDs[0] != audioID {
		t.Fatalf("monitor ids = %v, want only audiobook row %d", upstream.monitorIDs, audioID)
	}
}

func TestVerifiedBookMutationFailsClosedOnCatalogTimeoutAndReadback(t *testing.T) {
	t.Run("catalog timeout", func(t *testing.T) {
		upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
		upstream.catalogAlwaysBusy = true
		service, userID := newVerifiedMutationService(t, upstream)
		service.bookMutationTimeout = 20 * time.Millisecond
		_, err := service.CreateMediaRequest(userID, &CreateRequest{
			MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatEbook,
		})
		if !errors.Is(err, ErrBookOutcomePending) {
			t.Fatalf("error = %v, want retained durable outcome_pending", err)
		}
		if upstream.searchCalls != 0 {
			t.Fatalf("timed-out catalog queued %d searches", upstream.searchCalls)
		}
	})

	t.Run("monitor readback", func(t *testing.T) {
		upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
		upstream.dropMonitorWrite = true
		service, userID := newVerifiedMutationService(t, upstream)
		_, err := service.CreateMediaRequest(userID, &CreateRequest{
			MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatEbook,
		})
		if !errors.Is(err, ErrBookOutcomePending) {
			t.Fatalf("error = %v, want retained durable outcome_pending", err)
		}
		if upstream.searchCalls != 0 {
			t.Fatalf("failed readback queued %d searches", upstream.searchCalls)
		}
	})

	t.Run("search acknowledgement", func(t *testing.T) {
		upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
		upstream.invalidSearchAck = true
		service, userID := newVerifiedMutationService(t, upstream)
		_, err := service.CreateMediaRequest(userID, &CreateRequest{
			MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatEbook,
		})
		if !errors.Is(err, ErrBookOutcomePending) {
			t.Fatalf("error = %v, want retained durable outcome_pending", err)
		}
	})

	t.Run("identity drift before put", func(t *testing.T) {
		upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
		upstream.driftIdentityOnRead = true
		service, userID := newVerifiedMutationService(t, upstream)
		service.bookMutationTimeout = 25 * time.Millisecond
		_, err := service.CreateMediaRequest(userID, &CreateRequest{
			MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatEbook,
		})
		if err == nil {
			t.Fatal("identity drift was accepted")
		}
		if len(upstream.bookPutIDs) != 0 || upstream.searchCalls != 0 {
			t.Fatalf("identity drift wrote books=%v search=%d", upstream.bookPutIDs, upstream.searchCalls)
		}
	})
}

func TestVerifiedBookMutationExistingSiblingAndRetryConverge(t *testing.T) {
	upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
	upstream.author.EbookMonitorFuture = true
	upstream.addExisting(BookFormatEbook, true)
	service, userID := newVerifiedMutationService(t, upstream)
	request := &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatAudiobook,
	}
	if _, err := service.CreateMediaRequest(userID, request); err != nil {
		t.Fatalf("first CreateMediaRequest: %v", err)
	}
	if _, err := service.CreateMediaRequest(userID, request); err != nil {
		t.Fatalf("retry CreateMediaRequest: %v", err)
	}
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if len(upstream.seedBodies) != 1 {
		t.Fatalf("seed calls = %d, want one missing sibling add", len(upstream.seedBodies))
	}
	if got := upstream.seedBodies[0]["authorId"]; got != float64(upstream.author.ID) {
		t.Fatalf("sibling authorId = %v, want %d", got, upstream.author.ID)
	}
	if upstream.searchCalls != 1 {
		t.Fatalf("search calls = %d, want recent acknowledgement to suppress duplicate", upstream.searchCalls)
	}
}

func TestVerifiedBookMutationBothReusesSecondRowMaterializedByFirstSeed(t *testing.T) {
	upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
	upstream.seedBothFormats = true
	service, userID := newVerifiedMutationService(t, upstream)
	response, err := service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatBoth,
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if response.Status != StatusRequested || response.BookFormats[BookFormatEbook] != StatusRequested || response.BookFormats[BookFormatAudiobook] != StatusRequested {
		t.Fatalf("response = %#v, want both formats verified", response)
	}
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if len(upstream.seedBodies) != 1 {
		t.Fatalf("seed calls = %d, want one; first seed already materialized both rows", len(upstream.seedBodies))
	}
	seedAuthor := upstream.seedBodies[0]["author"].(map[string]any)
	if seedAuthor["ebookMonitorFuture"] != true || seedAuthor["audiobookMonitorFuture"] != false {
		t.Fatalf("first seed gates = %#v, want only current ebook gate", seedAuthor)
	}
	if upstream.searchCalls != 2 {
		t.Fatalf("search calls = %d, want one per verified format row", upstream.searchCalls)
	}
}

func TestVerifiedBookMutationBothCommitsCompletedFormatBeforeSiblingSetupFailure(t *testing.T) {
	upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
	upstream.addExisting(BookFormatEbook, false)
	upstream.author.AudiobookRootFolderPath = ""
	service, userID := newVerifiedMutationService(t, upstream)

	response, err := service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatBoth,
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if response.Status != StatusPartial ||
		response.BookFormats[BookFormatEbook] != StatusRequested ||
		response.BookFormats[BookFormatAudiobook] != StatusUnavailable {
		t.Fatalf("response = %#v, want committed ebook plus unavailable audiobook", response)
	}

	var jobs int
	if err := service.db.QueryRow("SELECT COUNT(*) FROM book_request_jobs").Scan(&jobs); err != nil {
		t.Fatalf("count book jobs: %v", err)
	}
	if jobs != 0 {
		t.Fatalf("book request jobs = %d, want completed partial job removed", jobs)
	}
	var format, status string
	if err := service.db.QueryRow(
		"SELECT book_format, status FROM request_log WHERE user_id = ? AND foreign_id = ? AND media_type = 'book'",
		userID,
		upstream.foreignBookID,
	).Scan(&format, &status); err != nil {
		t.Fatalf("read completed format history: %v", err)
	}
	if format != BookFormatEbook || status != StatusRequested {
		t.Fatalf("history = %s/%s, want ebook/requested", format, status)
	}
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if upstream.searchCalls != 1 || len(upstream.seedBodies) != 0 {
		t.Fatalf("search calls = %d seed calls = %d, want one ebook search and no sibling seed", upstream.searchCalls, len(upstream.seedBodies))
	}
}

func TestVerifiedBookMutationBothDoesNotSeedMissingFirstFormatAfterExistingSiblingBecomesUnverified(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*verifiedBookUpstream, *Service)
	}{
		{
			name: "monitor readback is unverified",
			configure: func(upstream *verifiedBookUpstream, _ *Service) {
				upstream.dropMonitorWrite = true
			},
		},
		{
			name: "catalog times out",
			configure: func(upstream *verifiedBookUpstream, service *Service) {
				upstream.catalogAlwaysBusy = true
				service.bookMutationTimeout = 20 * time.Millisecond
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newVerifiedBookUpstream("Existing Audio", "existing-audio")
			upstream.addExisting(BookFormatAudiobook, false)
			service, userID := newVerifiedMutationService(t, upstream)
			tc.configure(upstream, service)

			_, err := service.CreateMediaRequest(userID, &CreateRequest{
				MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatBoth,
			})
			if !errors.Is(err, ErrBookOutcomePending) {
				t.Fatalf("error = %v, want retained durable outcome_pending", err)
			}
			if len(upstream.seedBodies) != 0 {
				t.Fatalf("seed calls = %d, want zero ebook POSTs after the existing audiobook became outcome-unknown", len(upstream.seedBodies))
			}
		})
	}
}

func TestVerifiedBookMutationBothPreservesFirstGateAcrossTwoSeeds(t *testing.T) {
	upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
	service, userID := newVerifiedMutationService(t, upstream)
	if _, err := service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatBoth,
	}); err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if len(upstream.seedBodies) != 2 {
		t.Fatalf("seed calls = %d, want one per absent format", len(upstream.seedBodies))
	}
	secondAuthor := upstream.seedBodies[1]["author"].(map[string]any)
	if secondAuthor["ebookMonitorFuture"] != true || secondAuthor["audiobookMonitorFuture"] != true {
		t.Fatalf("second seed gates = %#v, want first ebook gate preserved", secondAuthor)
	}
	if !upstream.author.EbookMonitorFuture || !upstream.author.AudiobookMonitorFuture {
		t.Fatalf("final author gates = ebook %t audiobook %t, want both", upstream.author.EbookMonitorFuture, upstream.author.AudiobookMonitorFuture)
	}
}

func TestVerifiedBookMutationResolvesExistingAuthorBeforeNewTitlePost(t *testing.T) {
	upstream := newVerifiedBookUpstream("A New Work", "hc:work-new")
	upstream.authorExists = true
	upstream.leanAuthorList = true
	service, userID := newVerifiedMutationService(t, upstream)
	if _, err := service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatEbook,
	}); err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if len(upstream.seedBodies) != 1 || upstream.seedBodies[0]["authorId"] != float64(upstream.author.ID) {
		t.Fatalf("seed bodies = %#v, want existing local authorId %d", upstream.seedBodies, upstream.author.ID)
	}
}

func TestVerifiedBookMutationRejectsMissingAuthorIdentityAndMultiWork(t *testing.T) {
	t.Run("missing author identity", func(t *testing.T) {
		upstream := newVerifiedBookUpstream("A New Work", "hc:work-new")
		upstream.authorName = ""
		upstream.foreignAuthorID = ""
		service, userID := newVerifiedMutationService(t, upstream)
		_, err := service.CreateMediaRequest(userID, &CreateRequest{
			MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatEbook,
		})
		if !errors.Is(err, ErrBookMutationUnverified) || errors.Is(err, ErrBookOutcomePending) {
			t.Fatalf("error = %v, want terminal pre-write identity failure", err)
		}
		if len(upstream.seedBodies) != 0 {
			t.Fatalf("missing author identity caused %d POSTs", len(upstream.seedBodies))
		}
	})

	for _, title := range []string{
		"Realm Omnibus Edition", "The Realm Box Set", "The Realm Bundle", "The Realm Trilogy",
		"The Complete Realm Series", "Realm Books 1-3",
	} {
		t.Run(title, func(t *testing.T) {
			upstream := newVerifiedBookUpstream(title, "hc:multi")
			service, userID := newVerifiedMutationService(t, upstream)
			_, err := service.CreateMediaRequest(userID, &CreateRequest{
				MediaType: "book", ForeignID: upstream.foreignBookID, Title: title, BookFormat: BookFormatEbook,
			})
			if !errors.Is(err, ErrBookMultiWorkUnsupported) {
				t.Fatalf("error = %v, want ErrBookMultiWorkUnsupported", err)
			}
			if len(upstream.seedBodies) != 0 {
				t.Fatalf("multi-work title caused %d POSTs", len(upstream.seedBodies))
			}
		})
	}
	for _, title := range []string{"The Collection", "The Realm Series"} {
		if chaptarrTitleIsMultiWork(title) {
			t.Fatalf("legitimate single-work title %q was rejected", title)
		}
	}
}

func TestVerifiedBookMutationAggregatesEvidenceAcrossUsablePockets(t *testing.T) {
	upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
	preferredID := upstream.addExisting(BookFormatAudiobook, false)
	activeID := upstream.addExisting(BookFormatAudiobook, false)
	upstream.mu.Lock()
	firstEdition := upstream.rows[preferredID].editions[0]
	firstEdition.ID = upstream.nextEditionID
	firstEdition.ForeignEditionID = fmt.Sprintf("edition-%d", upstream.nextEditionID)
	upstream.nextEditionID++
	upstream.rows[preferredID].editions = append(upstream.rows[preferredID].editions, firstEdition)
	upstream.rows[activeID].book.Grabbed = true
	upstream.rows[activeID].editions[0].Format = "Physical"
	upstream.mu.Unlock()
	service, userID := newVerifiedMutationService(t, upstream)
	if _, err := service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatAudiobook,
	}); err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if len(upstream.bookPutIDs) != 0 || len(upstream.monitorIDs) != 0 || upstream.searchCalls != 0 {
		t.Fatalf("existing evidence caused put=%v monitor=%v search=%d", upstream.bookPutIDs, upstream.monitorIDs, upstream.searchCalls)
	}
}

func TestVerifiedBookMutationPreservesCompatibleSubtitlePocketEvidence(t *testing.T) {
	tests := []struct {
		name       string
		configure  func(*verifiedBookRow)
		recordAck  bool
		wantStatus string
	}{
		{
			name: "file",
			configure: func(row *verifiedBookRow) {
				row.book.HasFiles = true
			},
			wantStatus: StatusAvailable,
		},
		{
			name: "grab",
			configure: func(row *verifiedBookRow) {
				row.book.Grabbed = true
			},
			wantStatus: StatusRequested,
		},
		{
			name:       "recent search acknowledgement",
			recordAck:  true,
			wantStatus: StatusRequested,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
			upstream.addExisting(BookFormatAudiobook, false)
			subtitleID := upstream.addExisting(BookFormatAudiobook, false)
			upstream.mu.Lock()
			subtitle := upstream.rows[subtitleID]
			subtitle.book.Title = "The Clockwork Orchard: A Novel"
			subtitle.editions[0].Title = subtitle.book.Title
			if tc.configure != nil {
				tc.configure(subtitle)
			}
			upstream.mu.Unlock()

			service, userID := newVerifiedMutationService(t, upstream)
			if tc.recordAck {
				_, instanceID, err := service.resolveChaptarr(userID, "")
				if err != nil {
					t.Fatal(err)
				}
				service.recordBookSearchAck(instanceID, subtitleID)
			}
			response, err := service.CreateMediaRequest(userID, &CreateRequest{
				MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title,
				BookFormat: BookFormatAudiobook,
			})
			if err != nil {
				t.Fatalf("CreateMediaRequest: %v", err)
			}
			if response.Status != tc.wantStatus || response.BookFormats[BookFormatAudiobook] != tc.wantStatus {
				t.Fatalf("response = %#v, want %s from compatible subtitle pocket", response, tc.wantStatus)
			}
			upstream.mu.Lock()
			defer upstream.mu.Unlock()
			if len(upstream.bookPutIDs) != 0 || len(upstream.monitorIDs) != 0 || upstream.searchCalls != 0 {
				t.Fatalf("existing subtitle evidence caused put=%v monitor=%v search=%d", upstream.bookPutIDs, upstream.monitorIDs, upstream.searchCalls)
			}
		})
	}
}

func TestVerifiedBookMutationUsesCompatibleSubtitlePocketWithOnlyUsableEdition(t *testing.T) {
	upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
	exactID := upstream.addExisting(BookFormatAudiobook, false)
	subtitleID := upstream.addExisting(BookFormatAudiobook, false)
	upstream.mu.Lock()
	upstream.rows[exactID].editions[0].Format = "Physical"
	upstream.rows[subtitleID].book.Title = "The Clockwork Orchard: A Novel"
	upstream.rows[subtitleID].editions[0].Title = upstream.rows[subtitleID].book.Title
	upstream.mu.Unlock()

	service, userID := newVerifiedMutationService(t, upstream)
	if _, err := service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title,
		BookFormat: BookFormatAudiobook,
	}); err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if len(upstream.bookPutIDs) != 1 || upstream.bookPutIDs[0] != subtitleID ||
		len(upstream.monitorIDs) != 1 || upstream.monitorIDs[0] != subtitleID ||
		upstream.searchCalls != 1 || len(upstream.searchBookIDs) != 1 || upstream.searchBookIDs[0] != subtitleID {
		t.Fatalf("put=%v monitor=%v search=%d ids=%v, want only usable subtitle pocket %d", upstream.bookPutIDs, upstream.monitorIDs, upstream.searchCalls, upstream.searchBookIDs, subtitleID)
	}
}

func TestVerifiedBookMutationDoesNotDuplicateSubtitlePocketActiveSearch(t *testing.T) {
	upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
	exactID := upstream.addExisting(BookFormatAudiobook, false)
	subtitleID := upstream.addExisting(BookFormatAudiobook, false)
	upstream.mu.Lock()
	upstream.rows[subtitleID].book.Title = "The Clockwork Orchard: A Novel"
	upstream.rows[subtitleID].editions[0].Title = upstream.rows[subtitleID].book.Title
	upstream.activeSearch = true
	upstream.activeSearchBookID = subtitleID
	upstream.mu.Unlock()

	service, userID := newVerifiedMutationService(t, upstream)
	if _, err := service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title,
		BookFormat: BookFormatAudiobook,
	}); err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if len(upstream.bookPutIDs) != 1 || upstream.bookPutIDs[0] != exactID ||
		len(upstream.monitorIDs) != 1 || upstream.monitorIDs[0] != exactID {
		t.Fatalf("put=%v monitor=%v, want strongest exact-title pocket %d", upstream.bookPutIDs, upstream.monitorIDs, exactID)
	}
	if upstream.searchCalls != 0 {
		t.Fatalf("queued %d duplicate searches beside compatible subtitle pocket search", upstream.searchCalls)
	}
}

func TestVerifiedBookMutationDoesNotDuplicateSearchAppearingDuringWrites(t *testing.T) {
	upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
	upstream.searchAppearsAfterMonitor = true
	service, userID := newVerifiedMutationService(t, upstream)
	if _, err := service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatEbook,
	}); err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if upstream.searchCalls != 0 {
		t.Fatalf("queued %d duplicate searches after exact command appeared", upstream.searchCalls)
	}
}

func TestVerifiedBookMutationConcurrentAuthorGatesConverge(t *testing.T) {
	upstream := newVerifiedBookUpstream("First Work", "hc:first")
	ebookID := upstream.addExisting(BookFormatEbook, false)
	audioID := upstream.addExisting(BookFormatAudiobook, false)
	upstream.mu.Lock()
	upstream.rows[ebookID].book.Title = "First Work"
	upstream.rows[ebookID].book.ForeignBookID = "hc:first"
	upstream.rows[ebookID].editions[0].Title = "First Work"
	upstream.rows[audioID].book.Title = "Second Work"
	upstream.rows[audioID].book.ForeignBookID = "hc:second"
	upstream.rows[audioID].editions[0].Title = "Second Work"
	upstream.mu.Unlock()
	service, userID := newVerifiedMutationService(t, upstream)
	client, instanceID, err := service.resolveChaptarr(userID, "")
	if err != nil {
		t.Fatal(err)
	}
	targets := []chaptarrBookTarget{
		{authorID: upstream.author.ID, foreignBookID: "hc:first", title: "First Work", mediaType: BookFormatEbook},
		{authorID: upstream.author.ID, foreignBookID: "hc:second", title: "Second Work", mediaType: BookFormatAudiobook},
	}
	var wg sync.WaitGroup
	for _, target := range targets {
		target := target
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer cancel()
			if _, err := service.ensureChaptarrBookRequest(ctx, client, instanceID, target); err != nil {
				t.Errorf("ensureChaptarrBookRequest(%s): %v", target.mediaType, err)
			}
		}()
	}
	wg.Wait()
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if !upstream.author.EbookMonitorFuture || !upstream.author.AudiobookMonitorFuture {
		t.Fatalf("author gates = ebook %t audiobook %t, want both after concurrent formats", upstream.author.EbookMonitorFuture, upstream.author.AudiobookMonitorFuture)
	}
}

func TestVerifiedBookMutationTimeoutAndFailureClearProjection(t *testing.T) {
	t.Run("delayed command call is catalog pending", func(t *testing.T) {
		upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
		upstream.commandDelay = 50 * time.Millisecond
		service, userID := newVerifiedMutationService(t, upstream)
		service.bookMutationTimeout = 10 * time.Millisecond
		_, err := service.CreateMediaRequest(userID, &CreateRequest{
			MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatEbook,
		})
		if !errors.Is(err, ErrBookOutcomePending) {
			t.Fatalf("error = %v, want retained durable outcome_pending", err)
		}
	})

	t.Run("unknown search outcome clears cache", func(t *testing.T) {
		upstream := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
		upstream.invalidSearchAck = true
		service, userID := newVerifiedMutationService(t, upstream)
		_, instanceID, err := service.resolveChaptarr(userID, "")
		if err != nil {
			t.Fatal(err)
		}
		cacheKey := "book-live:" + instanceID
		service.cacheBookProjection(cacheKey, &bookLiveProjection{Formats: map[string]map[string]string{
			upstream.foreignBookID: {BookFormatEbook: StatusUnavailable},
		}})
		_, err = service.CreateMediaRequest(userID, &CreateRequest{
			MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatEbook,
		})
		if !errors.Is(err, ErrBookOutcomePending) {
			t.Fatalf("error = %v, want retained durable outcome_pending", err)
		}
		if _, ok := service.cachedBookProjection(cacheKey); ok {
			t.Fatal("failed mutation left stale live projection cached")
		}
	})
}

func TestChooseChaptarrEditionRanksIdentityBeforeAllocationID(t *testing.T) {
	editions := []chaptarr.Edition{
		{ID: 99, Title: "Different Work", Format: "Ebook", Monitored: true, ForeignEditionID: "old"},
		{ID: 7, Title: "Selected Work", Format: "Ebook", ForeignEditionID: "exact"},
	}
	if got, err := chooseChaptarrEdition(editions, "Selected Work", BookFormatEbook); err != nil || got == nil || got.ID != 7 {
		t.Fatalf("chosen = %#v, want exact title edition 7", got)
	}
	tied := []chaptarr.Edition{
		{ID: 99, Title: "Selected Work", Format: "Ebook", ForeignEditionID: "first"},
		{ID: 1, Title: "Selected Work", Format: "Ebook", ForeignEditionID: "second"},
	}
	if got, err := chooseChaptarrEdition(tied, "Selected Work", BookFormatEbook); err != nil || got == nil || got.ID != 99 {
		t.Fatalf("chosen = %#v, want authoritative response-order edition 99", got)
	}
}

func TestVerifiedBookMutationBothStoresOnlySuccessfulFormat(t *testing.T) {
	upstream := newVerifiedBookUpstream("Partial", "partial-1")
	upstream.failAddFormat[BookFormatAudiobook] = true
	service, userID := newVerifiedMutationService(t, upstream)
	response, err := service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatBoth,
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if response.Status != StatusPartial || response.BookFormats[BookFormatEbook] != StatusRequested || response.BookFormats[BookFormatAudiobook] != StatusUnavailable {
		t.Fatalf("response = %#v, want concrete requested/unavailable partial", response)
	}
	rows, err := service.db.Query("SELECT book_format, status FROM request_log WHERE user_id=? AND foreign_id=?", userID, upstream.foreignBookID)
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

func TestVerifiedBookPartialApprovalKeepsFailedWaiterPending(t *testing.T) {
	upstream := newVerifiedBookUpstream("Partial Approval", "partial-approval")
	upstream.failAddFormat[BookFormatAudiobook] = true
	service, ownerID := newVerifiedMutationService(t, upstream)
	_, instanceID, err := service.resolveChaptarr(ownerID, "")
	if err != nil {
		t.Fatal(err)
	}
	res, err := service.db.Exec("INSERT INTO users (username, password_hash, role) VALUES ('audio-waiter', '', 'user')")
	if err != nil {
		t.Fatal(err)
	}
	waiterID, _ := res.LastInsertId()
	res, err = service.db.Exec("INSERT INTO users (username, password_hash, role) VALUES ('ebook-waiter', '', 'user')")
	if err != nil {
		t.Fatal(err)
	}
	ebookWaiterID, _ := res.LastInsertId()
	recorder := &recordingNotifier{}
	service.notifier = recorder
	for _, pending := range []*resolvedRequest{
		{userID: ownerID, mediaType: "book", foreignID: upstream.foreignBookID, title: upstream.title, bookFormat: BookFormatBoth, instanceID: instanceID},
		{userID: waiterID, mediaType: "book", foreignID: upstream.foreignBookID, title: upstream.title, bookFormat: BookFormatAudiobook, instanceID: instanceID},
		{userID: ebookWaiterID, mediaType: "book", foreignID: upstream.foreignBookID, title: upstream.title, bookFormat: BookFormatEbook, instanceID: instanceID},
	} {
		if _, err := service.createPending(pending); err != nil {
			t.Fatal(err)
		}
	}
	adminID := createTestAdmin(t, service)
	var requestID int64
	if err := service.db.QueryRow("SELECT id FROM request_log WHERE foreign_id = ? AND status = 'pending'", upstream.foreignBookID).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	response, err := service.ApproveRequest(adminID, requestID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != StatusPartial {
		t.Fatalf("approval response = %+v, want partial", response)
	}
	var pendingFormat, waiterFormat string
	if err := service.db.QueryRow(
		`SELECT r.book_format, bw.book_format FROM request_log r
		 JOIN book_request_waiters bw ON bw.request_id = r.id
		 WHERE r.foreign_id = ? AND r.status = 'pending' AND bw.user_id = ?`, upstream.foreignBookID, waiterID,
	).Scan(&pendingFormat, &waiterFormat); err != nil {
		t.Fatal(err)
	}
	if pendingFormat != BookFormatAudiobook || waiterFormat != BookFormatAudiobook {
		t.Fatalf("retained pending=%q waiter=%q, want audiobook", pendingFormat, waiterFormat)
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

func TestVerifiedBookConcurrentRequestsSeedOnce(t *testing.T) {
	upstream := newVerifiedBookUpstream("Race", "race")
	service, userID := newVerifiedMutationService(t, upstream)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := service.CreateMediaRequest(userID, &CreateRequest{
				MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatEbook,
			}); err != nil {
				t.Errorf("CreateMediaRequest: %v", err)
			}
		}()
	}
	wg.Wait()
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if len(upstream.seedBodies) != 1 || upstream.searchCalls != 1 {
		t.Fatalf("seed=%d search=%d, want one converged mutation", len(upstream.seedBodies), upstream.searchCalls)
	}
}

func TestVerifiedBookApprovalNotifiesWithForeignID(t *testing.T) {
	upstream := newVerifiedBookUpstream("Ahsoka", "hc:ahsoka")
	service, userID := newVerifiedMutationService(t, upstream)
	recorder := &recordingNotifier{}
	service.notifier = recorder
	requireApproval(t, service)
	adminID := createTestAdmin(t, service)
	if _, err := service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatEbook,
	}); err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	pending, err := service.ListPending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("ListPending = %+v err=%v, want exactly one", pending, err)
	}
	if _, err := service.ApproveRequest(adminID, pending[0].ID, nil); err != nil {
		t.Fatalf("ApproveRequest: %v", err)
	}
	if len(recorder.userEvents) != 1 {
		t.Fatalf("events = %+v, want one approval", recorder.userEvents)
	}
	event := recorder.userEvents[0]
	if event.data["foreign_id"] != upstream.foreignBookID || event.data["decision"] != "approved" {
		t.Fatalf("event = %+v, want approved foreign book identity", event)
	}
}

func TestVerifiedBookLookupAndLibraryStayPinnedToSelectedWork(t *testing.T) {
	t.Run("lookup ranks exact title instead of response order", func(t *testing.T) {
		results := []chaptarr.LookupResult{
			{ForeignBookID: "same", Title: "Different Work", AuthorName: "Wrong Author"},
			{ForeignBookID: "same", Title: "Selected Work", AuthorName: "Right Author", ForeignAuthorID: "author:right"},
		}
		got, err := selectChaptarrLookupResult(results, "same", "Selected Work")
		if err != nil || got == nil || got.Title != "Selected Work" {
			t.Fatalf("select lookup = %#v, err=%v", got, err)
		}
	})

	t.Run("same provider id wrong local work fails before availability", func(t *testing.T) {
		upstream := newVerifiedBookUpstream("Selected Work", "same")
		id := upstream.addExisting(BookFormatEbook, true)
		upstream.rows[id].book.Title = "Different Work"
		upstream.rows[id].book.HasFiles = true
		service, userID := newVerifiedMutationService(t, upstream)
		_, err := service.CreateMediaRequest(userID, &CreateRequest{
			MediaType: "book", ForeignID: "same", Title: "Selected Work", BookFormat: BookFormatEbook,
		})
		if !errors.Is(err, ErrBookMutationUnverified) || errors.Is(err, ErrBookOutcomePending) {
			t.Fatalf("error = %v, want terminal pre-write conflicting work", err)
		}
		if len(upstream.seedBodies) != 0 || upstream.searchCalls != 0 {
			t.Fatalf("wrong work mutated seed=%d search=%d", len(upstream.seedBodies), upstream.searchCalls)
		}
	})

	t.Run("resolved local bundle cannot bypass clean request title", func(t *testing.T) {
		upstream := newVerifiedBookUpstream("Realm", "realm")
		id := upstream.addExisting(BookFormatEbook, true)
		upstream.rows[id].book.Title = "Realm: Books 1-3"
		service, userID := newVerifiedMutationService(t, upstream)
		_, err := service.CreateMediaRequest(userID, &CreateRequest{
			MediaType: "book", ForeignID: "realm", Title: "Realm", BookFormat: BookFormatEbook,
		})
		if !errors.Is(err, ErrBookMultiWorkUnsupported) {
			t.Fatalf("error = %v, want ErrBookMultiWorkUnsupported", err)
		}
	})
}

func TestVerifiedBookAuthorProviderAndNameDriftPinLocalIdentity(t *testing.T) {
	for _, tc := range []struct {
		name             string
		lookupName       string
		lookupProviderID string
		localName        string
		localProviderID  string
	}{
		{name: "provider drift", lookupName: "Mara Vale", lookupProviderID: "provider:new", localName: "Mara Vale", localProviderID: "provider:local"},
		{name: "display alias", lookupName: "M. Vale", lookupProviderID: "provider:same", localName: "Mara Vale", localProviderID: "provider:same"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newVerifiedBookUpstream("New Work", "new-work")
			upstream.authorExists = true
			upstream.authorName = tc.lookupName
			upstream.foreignAuthorID = tc.lookupProviderID
			upstream.author.AuthorName = tc.localName
			upstream.author.ForeignAuthorID = tc.localProviderID
			service, userID := newVerifiedMutationService(t, upstream)
			if _, err := service.CreateMediaRequest(userID, &CreateRequest{
				MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatEbook,
			}); err != nil {
				t.Fatalf("CreateMediaRequest: %v", err)
			}
			if len(upstream.seedBodies) != 1 {
				t.Fatalf("seed calls = %d, want 1", len(upstream.seedBodies))
			}
			author := upstream.seedBodies[0]["author"].(map[string]any)
			if author["foreignAuthorId"] != tc.localProviderID || author["authorName"] != tc.localName {
				t.Fatalf("seed author = %#v, want pinned local identity", author)
			}
		})
	}
}

func TestVerifiedBookSeedOutcomeUnknownReconcilesWithoutDuplicatePost(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*verifiedBookUpstream)
	}{
		{name: "lost response", configure: func(u *verifiedBookUpstream) { u.dropAddResponse[BookFormatEbook] = true }},
		{name: "committed 500", configure: func(u *verifiedBookUpstream) { u.addResponseStatus[BookFormatEbook] = http.StatusInternalServerError }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newVerifiedBookUpstream("Unknown Seed", "unknown-seed")
			tc.configure(upstream)
			service, userID := newVerifiedMutationService(t, upstream)
			if _, err := service.CreateMediaRequest(userID, &CreateRequest{
				MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatEbook,
			}); err != nil {
				t.Fatalf("CreateMediaRequest: %v", err)
			}
			if len(upstream.seedBodies) != 1 || upstream.searchCalls != 1 {
				t.Fatalf("seed=%d search=%d, want one reconciled mutation", len(upstream.seedBodies), upstream.searchCalls)
			}
		})
	}

	t.Run("uncommitted first format stops both", func(t *testing.T) {
		upstream := newVerifiedBookUpstream("Unknown Seed", "unknown-seed")
		upstream.dropAddWithoutCommit[BookFormatEbook] = true
		service, userID := newVerifiedMutationService(t, upstream)
		service.bookMutationTimeout = 20 * time.Millisecond
		_, err := service.CreateMediaRequest(userID, &CreateRequest{
			MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatBoth,
		})
		if !errors.Is(err, ErrBookOutcomePending) {
			t.Fatalf("error = %v, want retained durable outcome_pending", err)
		}
		if len(upstream.seedBodies) != 1 {
			t.Fatalf("seed calls = %d, want no second-format POST", len(upstream.seedBodies))
		}
	})
}

func TestVerifiedBookSearchOutcomeUnknownDoesNotQueueDuplicate(t *testing.T) {
	upstream := newVerifiedBookUpstream("Unknown Search", "unknown-search")
	upstream.dropSearchResponse = true
	service, userID := newVerifiedMutationService(t, upstream)
	request := &CreateRequest{MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatEbook}
	if _, err := service.CreateMediaRequest(userID, request); !errors.Is(err, ErrBookOutcomePending) {
		t.Fatalf("first error = %v, want durable outcome_pending", err)
	}
	if _, err := service.CreateMediaRequest(userID, request); !errors.Is(err, ErrBookOutcomePending) {
		t.Fatalf("retry error = %v, want same durable outcome_pending owner", err)
	}
	var phase, phaseFormat string
	var bookID, acknowledged int
	if err := service.db.QueryRow(
		`SELECT phase, phase_format, book_id, search_acknowledged
		 FROM book_request_jobs WHERE foreign_id = ?`,
		upstream.foreignBookID,
	).Scan(&phase, &phaseFormat, &bookID, &acknowledged); err != nil {
		t.Fatalf("read durable search guard: %v", err)
	}
	if phase != "search_inflight" || phaseFormat != BookFormatEbook || bookID <= 0 || acknowledged != 0 {
		t.Fatalf("durable search guard = phase %q format %q book %d ack %d", phase, phaseFormat, bookID, acknowledged)
	}
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if upstream.searchCalls != 1 || len(upstream.seedBodies) != 1 {
		t.Fatalf("search=%d seed=%d, want no duplicate outcome-unknown POST", upstream.searchCalls, len(upstream.seedBodies))
	}
}

func TestVerifiedBookWaitsForAcknowledgedAuthorToBecomeReadable(t *testing.T) {
	upstream := newVerifiedBookUpstream("Slow Author", "slow-author")
	upstream.authorReadFailures = 3
	service, userID := newVerifiedMutationService(t, upstream)
	if _, err := service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatEbook,
	}); err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if len(upstream.seedBodies) != 1 || upstream.searchCalls != 1 {
		t.Fatalf("seed=%d search=%d, want one mutation after delayed author read", len(upstream.seedBodies), upstream.searchCalls)
	}
}

func TestVerifiedBookAuthorAuthenticationFailureIsDefinitive(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			upstream := newVerifiedBookUpstream("Protected Author", "protected-author")
			upstream.authorReadStatus = status
			service, userID := newVerifiedMutationService(t, upstream)
			started := time.Now()
			_, err := service.CreateMediaRequest(userID, &CreateRequest{
				MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatEbook,
			})
			if !bookUpstreamAuthFailure(err) || !errors.Is(err, ErrBookMutationUnverified) || errors.Is(err, ErrBookCatalogPending) {
				t.Fatalf("error = %v, want immediate typed authentication failure", err)
			}
			statusCode, body := bookRequestErrorResponse(err, BookFormatEbook)
			if statusCode != http.StatusServiceUnavailable || body["code"] != "book_connection_invalid" {
				t.Fatalf("public error = %d %#v", statusCode, body)
			}
			if upstream.searchCalls != 0 {
				t.Fatalf("authentication failure queued %d searches", upstream.searchCalls)
			}
			if time.Since(started) >= service.bookMutationTimeout {
				t.Fatalf("authentication failure waited for mutation timeout")
			}
		})
	}
}

func TestVerifiedBookDefinitiveRejectionsPreserveAuthenticationStatus(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		want      error
		configure func(*verifiedBookUpstream)
	}{
		{
			name: "add", status: http.StatusUnauthorized, want: ErrBookMutationRejected,
			configure: func(upstream *verifiedBookUpstream) {
				upstream.rejectAddStatus[BookFormatEbook] = http.StatusUnauthorized
			},
		},
		{
			name: "search", status: http.StatusForbidden, want: ErrBookSearchRejected,
			configure: func(upstream *verifiedBookUpstream) {
				upstream.searchResponseStatus = http.StatusForbidden
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newVerifiedBookUpstream("Protected Mutation", "protected-mutation")
			tc.configure(upstream)
			service, userID := newVerifiedMutationService(t, upstream)
			_, err := service.CreateMediaRequest(userID, &CreateRequest{
				MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatEbook,
			})
			var statusErr *chaptarr.HTTPStatusError
			if !errors.Is(err, tc.want) || !errors.As(err, &statusErr) || statusErr.StatusCode != tc.status {
				t.Fatalf("error = %v, want %v with HTTP %d", err, tc.want, tc.status)
			}
			statusCode, body := bookRequestErrorResponse(err, BookFormatEbook)
			if statusCode != http.StatusServiceUnavailable || body["code"] != "book_connection_invalid" {
				t.Fatalf("public error = %d %#v", statusCode, body)
			}
		})
	}
}

func TestVerifiedBookDefinitiveUpdateRejectionsAreTerminal(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*verifiedBookUpstream)
	}{
		{name: "author", configure: func(upstream *verifiedBookUpstream) {
			upstream.authorUpdateStatus = http.StatusUnprocessableEntity
		}},
		{name: "book", configure: func(upstream *verifiedBookUpstream) {
			upstream.author.EbookMonitorFuture = true
			upstream.bookUpdateStatus = http.StatusBadRequest
		}},
		{name: "monitor", configure: func(upstream *verifiedBookUpstream) {
			upstream.author.EbookMonitorFuture = true
			upstream.monitorUpdateStatus = http.StatusNotFound
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newVerifiedBookUpstream("Rejected Update", "rejected-update")
			upstream.addExisting(BookFormatEbook, false)
			tc.configure(upstream)
			service, userID := newVerifiedMutationService(t, upstream)
			_, err := service.CreateMediaRequest(userID, &CreateRequest{
				MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title,
				BookFormat: BookFormatEbook,
			})
			if !errors.Is(err, ErrBookMutationRejected) || errors.Is(err, ErrBookOutcomePending) {
				t.Fatalf("error = %v, want definitive mutation rejection", err)
			}
			var state, code string
			if err := service.db.QueryRow(
				"SELECT state, last_error_code FROM book_request_jobs WHERE foreign_id = ?", upstream.foreignBookID,
			).Scan(&state, &code); err != nil {
				t.Fatal(err)
			}
			if state != "failed" || code != "book_request_rejected" {
				t.Fatalf("durable rejection state=%q code=%q", state, code)
			}
			if upstream.searchCalls != 0 {
				t.Fatalf("definitive update rejection queued %d searches", upstream.searchCalls)
			}
		})
	}
}

func TestVerifiedBookMutationRejectsAmbiguousProviderlessCompatibleTitles(t *testing.T) {
	upstream := newVerifiedBookUpstream("The Long Road", "hc:long-road")
	firstID := upstream.addExisting(BookFormatAudiobook, false)
	secondID := upstream.addExisting(BookFormatAudiobook, false)
	upstream.mu.Lock()
	upstream.rows[firstID].book.ForeignBookID = ""
	upstream.rows[firstID].book.Title = "The Long Road: A Novel"
	upstream.rows[firstID].editions[0].Title = "The Long Road: A Novel"
	upstream.rows[secondID].book.ForeignBookID = ""
	upstream.rows[secondID].book.Title = "The Long Road - Anniversary Edition"
	upstream.rows[secondID].book.Ratings.Popularity = 100
	upstream.rows[secondID].editions[0].Title = "The Long Road - Anniversary Edition"
	upstream.mu.Unlock()

	service, userID := newVerifiedMutationService(t, upstream)
	client, instanceID, err := service.resolveChaptarr(userID, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	_, err = service.ensureChaptarrBookRequest(ctx, client, instanceID, chaptarrBookTarget{
		authorID: upstream.author.ID, foreignBookID: upstream.foreignBookID,
		title: upstream.title, mediaType: BookFormatAudiobook,
	})
	if !errors.Is(err, ErrBookMutationUnverified) {
		t.Fatalf("error = %v, want ambiguous provider-less identity rejection", err)
	}
	if len(upstream.bookPutIDs) != 0 || len(upstream.monitorIDs) != 0 || upstream.searchCalls != 0 {
		t.Fatalf("ambiguous records mutated put=%v monitor=%v search=%d", upstream.bookPutIDs, upstream.monitorIDs, upstream.searchCalls)
	}
}

func TestVerifiedBookQueuedIdentityFailuresBecomeRetryableTerminalState(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*verifiedBookUpstream)
	}{
		{name: "provider title collision", configure: func(upstream *verifiedBookUpstream) {
			bookID := upstream.addExisting(BookFormatEbook, false)
			upstream.rows[bookID].book.Title = "A Different Work"
		}},
		{name: "ambiguous lookup authors", configure: func(upstream *verifiedBookUpstream) {
			upstream.lookupResults = []map[string]any{
				{"title": upstream.title, "foreignBookId": upstream.foreignBookID, "author": map[string]any{"authorName": "First Author", "foreignAuthorId": "author:first"}},
				{"title": upstream.title, "foreignBookId": upstream.foreignBookID, "author": map[string]any{"authorName": "Second Author", "foreignAuthorId": "author:second"}},
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newVerifiedBookUpstream("Queued Identity", "queued-identity")
			tc.configure(upstream)
			service, userID := newVerifiedMutationService(t, upstream)
			request := &CreateRequest{
				MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title,
				BookFormat: BookFormatEbook,
			}
			_, requestErr := service.CreateMediaRequest(userID, request)
			if !errors.Is(requestErr, ErrBookMutationUnverified) || errors.Is(requestErr, ErrBookOutcomePending) {
				t.Fatalf("error = %v, want terminal queued identity failure", requestErr)
			}
			statusCode, body := bookRequestErrorResponse(requestErr, BookFormatEbook)
			if statusCode != http.StatusBadGateway || body["code"] != "book_request_unverified" {
				t.Fatalf("retry response = %d %#v", statusCode, body)
			}
			var jobID int64
			var state, phase string
			if err := service.db.QueryRow(
				"SELECT id, state, phase FROM book_request_jobs WHERE foreign_id = ?", upstream.foreignBookID,
			).Scan(&jobID, &state, &phase); err != nil {
				t.Fatal(err)
			}
			if state != "failed" || phase != "queued" {
				t.Fatalf("identity job state=%q phase=%q", state, phase)
			}
			active, err := service.hasActiveBookRequestJob(request.InstanceID, upstream.foreignBookID)
			if err != nil {
				t.Fatal(err)
			}
			// Resolve the pinned instance for the active-owner assertion because the
			// request DTO intentionally omits the default instance ID.
			_, instanceID, err := service.resolveChaptarr(userID, "")
			if err != nil {
				t.Fatal(err)
			}
			active, err = service.hasActiveBookRequestJob(instanceID, upstream.foreignBookID)
			if err != nil || active {
				t.Fatalf("terminal identity owner active=%v err=%v", active, err)
			}
			retry, _, alreadyActive, err := service.prepareDirectBookJob(&resolvedRequest{
				userID: userID, instanceID: instanceID, foreignID: upstream.foreignBookID,
				title: upstream.title, mediaType: "book", bookFormat: BookFormatEbook,
			})
			if err != nil || alreadyActive || retry.ID != jobID {
				t.Fatalf("retry owner=%#v active=%v err=%v", retry, alreadyActive, err)
			}
			if retained := service.deferDirectBookJob(retry.ID, requestErr); retained {
				t.Fatal("same deterministic retry became nonterminal")
			}
			if len(upstream.seedBodies) != 0 || len(upstream.bookPutIDs) != 0 || len(upstream.monitorIDs) != 0 || upstream.searchCalls != 0 {
				t.Fatalf("queued identity failure mutated seed=%d put=%v monitor=%v search=%d", len(upstream.seedBodies), upstream.bookPutIDs, upstream.monitorIDs, upstream.searchCalls)
			}
		})
	}
}

func TestBookMutationChildContextsHonorParentCancellation(t *testing.T) {
	service := &Service{bookMutationTimeout: time.Minute}
	parent, cancelParent := context.WithCancel(context.Background())
	setup, cancelSetup := newBookSetupContext(parent)
	defer cancelSetup()
	mutation, cancelMutation := service.newBookMutationContext(parent)
	defer cancelMutation()

	cancelParent()
	for name, ctx := range map[string]context.Context{"setup": setup, "mutation": mutation} {
		select {
		case <-ctx.Done():
			if !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatalf("%s context error = %v", name, ctx.Err())
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("%s context ignored parent cancellation", name)
		}
	}
}

func TestAddToChaptarrWithClientHonorsCanceledParent(t *testing.T) {
	upstream := newVerifiedBookUpstream("Canceled Work", "canceled-work")
	service, userID := newVerifiedMutationService(t, upstream)
	client, instanceID, err := service.resolveChaptarr(userID, "")
	if err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = service.addToChaptarrWithClientContext(parent, &resolvedRequest{
		userID: userID, foreignID: upstream.foreignBookID, title: upstream.title,
		mediaType: "book", bookFormat: BookFormatEbook,
	}, client, instanceID)
	if err == nil {
		t.Fatal("canceled parent was ignored")
	}
	if len(upstream.seedBodies) != 0 || len(upstream.monitorIDs) != 0 || upstream.searchCalls != 0 {
		t.Fatalf("canceled parent mutated seed=%d monitor=%v search=%d", len(upstream.seedBodies), upstream.monitorIDs, upstream.searchCalls)
	}
}

func TestVerifiedBookRepairsUnmonitoredRowWhileActiveSearchSuppressesDuplicate(t *testing.T) {
	upstream := newVerifiedBookUpstream("Active Repair", "active-repair")
	bookID := upstream.addExisting(BookFormatEbook, false)
	upstream.activeSearch = true
	service, userID := newVerifiedMutationService(t, upstream)
	if _, err := service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatEbook,
	}); err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	row := upstream.rows[bookID]
	if row == nil || !row.book.Monitored || !row.editions[0].Monitored {
		t.Fatalf("row = %#v, want repaired monitored selection", row)
	}
	if upstream.searchCalls != 0 {
		t.Fatalf("queued %d duplicate searches", upstream.searchCalls)
	}
}

func TestVerifiedBookEditionIdentityIsSafeAndCompatible(t *testing.T) {
	t.Run("blank title is safe fallback", func(t *testing.T) {
		editions := []chaptarr.Edition{{ID: 1, Format: "Audiobook", ForeignEditionID: "blank"}}
		got, err := chooseChaptarrEdition(editions, "Selected Work", BookFormatAudiobook)
		if err != nil || got == nil || got.ID != 1 {
			t.Fatalf("chosen=%#v err=%v, want blank-title fallback", got, err)
		}
	})
	t.Run("exact title outranks blank", func(t *testing.T) {
		editions := []chaptarr.Edition{
			{ID: 1, Format: "Ebook", ForeignEditionID: "blank", Monitored: true},
			{ID: 2, Title: "Selected Work", Format: "Ebook", ForeignEditionID: "exact"},
		}
		got, err := chooseChaptarrEdition(editions, "Selected Work", BookFormatEbook)
		if err != nil || got == nil || got.ID != 2 {
			t.Fatalf("chosen=%#v err=%v, want exact edition 2", got, err)
		}
	})
	t.Run("unrelated named edition is unavailable", func(t *testing.T) {
		editions := []chaptarr.Edition{{ID: 1, Title: "Different Work", Format: "Ebook", ForeignEditionID: "wrong"}}
		got, err := chooseChaptarrEdition(editions, "Selected Work", BookFormatEbook)
		if err != nil || got != nil {
			t.Fatalf("chosen=%#v err=%v, want no unrelated edition", got, err)
		}
	})
	t.Run("exact safe edition ignores weaker bundle", func(t *testing.T) {
		editions := []chaptarr.Edition{
			{ID: 1, Title: "Selected Work: Books 1-3", Format: "Ebook", ForeignEditionID: "bundle"},
			{ID: 2, Title: "Selected Work", Format: "Ebook", ForeignEditionID: "exact"},
		}
		got, err := chooseChaptarrEdition(editions, "Selected Work", BookFormatEbook)
		if err != nil || got == nil || got.ID != 2 {
			t.Fatalf("chosen=%#v err=%v, want safe exact edition", got, err)
		}
	})
	t.Run("blank safe edition ignores stronger bundle title", func(t *testing.T) {
		editions := []chaptarr.Edition{
			{ID: 1, Format: "Audiobook", ForeignEditionID: "blank"},
			{ID: 2, Title: "Selected Work: Books 1-3", Format: "Audiobook", ForeignEditionID: "bundle"},
		}
		got, err := chooseChaptarrEdition(editions, "Selected Work", BookFormatAudiobook)
		if err != nil || got == nil || got.ID != 1 {
			t.Fatalf("chosen=%#v err=%v, want safe blank-title edition", got, err)
		}
	})
}

func TestChaptarrEditionFormatUsesOnlyAuthoritativeEnum(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{name: "ebook case variant", in: "eBoOk", want: BookFormatEbook},
		{name: "audiobook case variant", in: "AUDIOBOOK", want: BookFormatAudiobook},
		{name: "audio cd is not inferred", in: "Audio CD", want: ""},
		{name: "mp3 is not inferred", in: "MP3", want: ""},
		{name: "physical is not inferred", in: "Physical", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := chaptarrEditionFormat(chaptarr.Edition{Format: tc.in}); got != tc.want {
				t.Fatalf("chaptarrEditionFormat(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestVerifiedBookCandidateRankingPreservesExistingSelection(t *testing.T) {
	releaseDate := time.Now()
	selected := chaptarrBookCandidate{
		book: chaptarr.Book{ID: 2, Monitored: true, MediaType: BookFormatEbook, ReleaseDate: &releaseDate, Images: []chaptarr.Image{{URL: "cover"}}, ForeignEditionID: "selected"}, identityTier: 22, editionIdentityTier: 3,
		editions: []chaptarr.Edition{{ID: 20, Format: "Ebook", Monitored: true}},
		usable:   []chaptarr.Edition{{ID: 20, Format: "Ebook", Monitored: true}},
	}
	more := chaptarrBookCandidate{
		book: chaptarr.Book{ID: 1, MediaType: BookFormatEbook, ReleaseDate: &releaseDate, Images: []chaptarr.Image{{URL: "cover"}}, ForeignEditionID: "more"}, identityTier: 22, editionIdentityTier: 3,
		editions: []chaptarr.Edition{{ID: 10, Format: "Ebook"}, {ID: 11, Format: "Ebook"}},
		usable:   []chaptarr.Edition{{ID: 10, Format: "Ebook"}, {ID: 11, Format: "Ebook"}},
	}
	got := selectChaptarrBookCandidate([]chaptarrBookCandidate{more, selected}, BookFormatEbook)
	if got == nil || got.book.ID != 2 {
		t.Fatalf("selected candidate = %#v, want existing monitored pocket 2", got)
	}
}

func TestVerifiedBookLegacySeedMarkerDoesNotControlEmptyCatalogStatus(t *testing.T) {
	upstream := newVerifiedBookUpstream("Pending Seed", "pending-seed")
	service, userID := newVerifiedMutationService(t, upstream)
	_, instanceID, err := service.resolveChaptarr(userID, "")
	if err != nil {
		t.Fatal(err)
	}
	service.recordUncertainBookSeed(service.bookSeedOutcomeKey(instanceID, upstream.foreignBookID, BookFormatEbook), upstream.title)
	status, err := service.GetUserBookStatusForInstance(userID, upstream.foreignBookID, instanceID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != StatusUnavailable || status.StatusKnown != nil || status.UnknownReason != "" {
		t.Fatalf("status = %#v, want ordinary unavailable without an active durable job", status)
	}
	if !service.hasUncertainBookSeed(service.bookSeedOutcomeKey(instanceID, upstream.foreignBookID, BookFormatEbook)) {
		t.Fatal("status GET mutated the legacy seed marker")
	}
}

func TestVerifiedBookStatusReadDoesNotStartLateMutation(t *testing.T) {
	upstream := newVerifiedBookUpstream("Late Seed", "late-seed")
	service, userID := newVerifiedMutationService(t, upstream)
	_, instanceID, err := service.resolveChaptarr(userID, "")
	if err != nil {
		t.Fatal(err)
	}
	service.recordUncertainBookSeed(
		service.bookSeedOutcomeKey(instanceID, upstream.foreignBookID, BookFormatEbook),
		upstream.title,
	)
	upstream.addExisting(BookFormatEbook, false)

	status, err := service.GetUserBookStatusForInstance(userID, upstream.foreignBookID, instanceID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != StatusUnavailable || status.StatusKnown != nil || status.UnknownReason != "" {
		t.Fatalf("status = %#v, want unavailable because no durable job exists", status)
	}
	// Status polling is automatic and can occur from any open client. It may
	// observe durable work, but must never be the event that performs the remote
	// edition, monitor, or search mutations.
	time.Sleep(20 * time.Millisecond)
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if len(upstream.bookPutIDs) != 0 || len(upstream.monitorIDs) != 0 || upstream.searchCalls != 0 {
		t.Fatalf("status GET mutated Chaptarr: put=%v monitor=%v search=%d", upstream.bookPutIDs, upstream.monitorIDs, upstream.searchCalls)
	}
}

func TestVerifiedBookActiveSearchWithoutUsableEditionRemainsOutcomePending(t *testing.T) {
	upstream := newVerifiedBookUpstream("Pending Audio", "pending-audio")
	upstream.physicalOnly[BookFormatAudiobook] = true
	upstream.addExisting(BookFormatAudiobook, true)
	upstream.activeSearch = true
	service, userID := newVerifiedMutationService(t, upstream)

	_, err := service.CreateMediaRequest(userID, &CreateRequest{
		MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title, BookFormat: BookFormatAudiobook,
	})
	if !errors.Is(err, ErrBookOutcomePending) {
		t.Fatalf("error = %v, want ErrBookOutcomePending", err)
	}
	status, err := service.GetUserBookStatus(userID, upstream.foreignBookID)
	if err != nil {
		t.Fatalf("GetUserBookStatus: %v", err)
	}
	if status.StatusKnown == nil || *status.StatusKnown || status.UnknownReason != "outcome_pending" {
		t.Fatalf("status = %#v, want unknown outcome_pending instead of requested", status)
	}
	if len(upstream.seedBodies) != 0 || len(upstream.monitorIDs) != 0 || upstream.searchCalls != 0 {
		t.Fatalf("pending active search caused seed=%d monitor=%v search=%d", len(upstream.seedBodies), upstream.monitorIDs, upstream.searchCalls)
	}
}

func TestVerifiedBookLiveProjectionFailsClosedWhenQueueCannotBeRead(t *testing.T) {
	upstream := newVerifiedBookUpstream("Queue Unknown", "queue-unknown")
	upstream.addExisting(BookFormatEbook, true)
	upstream.queueFailure = true
	service, userID := newVerifiedMutationService(t, upstream)

	status, err := service.GetUserBookStatus(userID, upstream.foreignBookID)
	if err == nil || !strings.Contains(err.Error(), "check live book queue") {
		t.Fatalf("status = %#v, error = %v, want fail-closed queue read error", status, err)
	}
}
