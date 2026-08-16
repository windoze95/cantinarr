package chaptarr

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type failingTransport struct{ err error }

func (f failingTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, f.err }

func TestGetBookFileUsesExactAuthenticatedEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/bookfile/55" || r.URL.RawQuery != "" {
			t.Errorf("request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if got := r.Header.Get("X-Api-Key"); got != "book-key" {
			t.Errorf("X-Api-Key = %q", got)
		}
		_, _ = w.Write([]byte(`{"id":55,"authorId":3,"bookId":9,"editionId":12,"path":"/library/Book.epub","size":98765}`))
	}))
	t.Cleanup(server.Close)

	file, err := NewClient(server.URL, "book-key").GetBookFile(55)
	if err != nil {
		t.Fatalf("GetBookFile() error = %v", err)
	}
	if file.ID != 55 || file.AuthorID != 3 || file.BookID != 9 || file.EditionID != 12 || file.Path != "/library/Book.epub" || file.Size != 98765 {
		t.Fatalf("file = %#v", file)
	}
}

func TestGetBookFileOmitsUpstreamErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`secret path /library/private and signed-token`))
	}))
	t.Cleanup(server.Close)

	_, err := NewClient(server.URL, "key").GetBookFile(55)
	if err == nil {
		t.Fatal("GetBookFile() error = nil")
	}
	if message := err.Error(); strings.Contains(message, "/library/private") || strings.Contains(message, "signed-token") {
		t.Fatalf("error leaked upstream body: %q", message)
	}
}

// TestTransportErrorOmitsHost pins the topology-privacy property: transport
// failures embed the full request URL (and DNS errors repeat the hostname),
// and these errors surface to requesters through book-request failures — so
// the client must summarize them host-free.
func TestTransportErrorOmitsHost(t *testing.T) {
	dnsFailure := &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: "chaptarr-internal"}}
	c := NewClient("http://chaptarr-internal:8787", "key")
	c.httpClient = &http.Client{Transport: failingTransport{dnsFailure}}

	if _, err := c.LookupAuthor("le guin"); err == nil {
		t.Fatal("LookupAuthor succeeded against a failing transport")
	} else if msg := err.Error(); strings.Contains(msg, "chaptarr-internal") || strings.Contains(msg, "8787") {
		t.Errorf("LookupAuthor error %q names the host", msg)
	} else if !strings.Contains(msg, "could not resolve host") {
		t.Errorf("LookupAuthor error %q lacks the failure summary", msg)
	}
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	var redirectedRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(destination.Close)

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/credential-sink", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)

	client := NewClient(source.URL, "chaptarr-secret")
	if _, err := client.GetAllAuthors(); err == nil {
		t.Fatal("GetAllAuthors accepted an upstream redirect")
	}
	if _, err := client.SearchReleases(42); err == nil {
		t.Fatal("SearchReleases accepted an upstream redirect")
	}
	if got := redirectedRequests.Load(); got != 0 {
		t.Fatalf("redirect destination received %d requests, want 0", got)
	}
}

func TestClientErrorDoesNotEchoUpstreamBody(t *testing.T) {
	const secret = "PROWLARR_DOWNLOAD_URL_API_KEY_SENTINEL"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"downloadUrl":"https://indexer.invalid/download?apikey=`+secret+`"}`, http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)

	_, err := NewClient(server.URL, "chaptarr-secret").GetAllAuthors()
	if err == nil {
		t.Fatal("GetAllAuthors returned nil error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error echoed upstream response secret: %v", err)
	}
}

// TestSetBookMonitored asserts SetBookMonitored PUTs the {bookIds, monitored}
// body Chaptarr's book/monitor endpoint expects (a POST returns 405 on the
// fork).
func TestSetBookMonitored(t *testing.T) {
	var gotPath, gotMethod string
	var body map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "key")
	if err := c.SetBookMonitored([]int{7, 9, 11}, true); err != nil {
		t.Fatalf("SetBookMonitored: %v", err)
	}

	if gotMethod != http.MethodPut {
		t.Errorf("method = %s, want PUT", gotMethod)
	}
	if gotPath != "/api/v1/book/monitor" {
		t.Errorf("path = %s, want /api/v1/book/monitor", gotPath)
	}
	if body["monitored"] != true {
		t.Errorf("monitored = %v, want true", body["monitored"])
	}
	ids, ok := body["bookIds"].([]any)
	if !ok || len(ids) != 3 {
		t.Fatalf("bookIds = %v, want 3 entries", body["bookIds"])
	}
	want := []int{7, 9, 11}
	for i, v := range ids {
		if int(v.(float64)) != want[i] {
			t.Errorf("bookIds[%d] = %v, want %d", i, v, want[i])
		}
	}
}

// TestExecuteManualImport asserts ExecuteManualImport posts a ManualImport
// command with a lowercase importMode and a files array whose bookId is set and
// whose Quality is preserved verbatim.
func TestExecuteManualImport(t *testing.T) {
	var gotPath string
	var body map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	quality := json.RawMessage(`{"quality":{"id":3,"name":"EPUB"},"revision":{"version":1}}`)
	files := []ManualImportFile{{
		Path:     "/downloads/book.epub",
		AuthorID: 4,
		BookID:   42,
		Quality:  quality,
	}}

	c := NewClient(srv.URL, "key")
	if err := c.ExecuteManualImport(files); err != nil {
		t.Fatalf("ExecuteManualImport: %v", err)
	}

	if gotPath != "/api/v1/command" {
		t.Errorf("path = %s, want /api/v1/command", gotPath)
	}
	if body["name"] != "ManualImport" {
		t.Errorf("name = %v, want ManualImport", body["name"])
	}
	if body["importMode"] != "auto" {
		t.Errorf("importMode = %v, want lowercase \"auto\"", body["importMode"])
	}

	gotFiles, ok := body["files"].([]any)
	if !ok || len(gotFiles) != 1 {
		t.Fatalf("files = %v, want a single-element array", body["files"])
	}
	file0 := gotFiles[0].(map[string]any)
	if int(file0["bookId"].(float64)) != 42 {
		t.Errorf("files[0].bookId = %v, want 42", file0["bookId"])
	}
	if int(file0["authorId"].(float64)) != 4 {
		t.Errorf("files[0].authorId = %v, want 4", file0["authorId"])
	}
	// Quality must round-trip verbatim: the nested name survives re-marshaling.
	q, ok := file0["quality"].(map[string]any)
	if !ok {
		t.Fatalf("files[0].quality = %v, want an object", file0["quality"])
	}
	inner, ok := q["quality"].(map[string]any)
	if !ok || inner["name"] != "EPUB" {
		t.Errorf("files[0].quality.quality.name = %v, want EPUB", q["quality"])
	}
}

// TestGetAllBooks asserts GetAllBooks hits /api/v1/book with no authorId filter
// and decodes the library books (including the book-level mediaType).
func TestGetAllBooks(t *testing.T) {
	var gotPath string
	var gotQuery url.Values

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"id":1,"title":"Heir to the Empire","foreignBookId":"fb-1","mediaType":"ebook","monitored":true,"statistics":{"bookFileCount":1}}]`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "key")
	books, err := c.GetAllBooks()
	if err != nil {
		t.Fatalf("GetAllBooks: %v", err)
	}

	if gotPath != "/api/v1/book" {
		t.Errorf("path = %s, want /api/v1/book", gotPath)
	}
	if gotQuery.Get("authorId") != "" {
		t.Errorf("authorId = %q, want empty (no per-author filter)", gotQuery.Get("authorId"))
	}
	if len(books) != 1 || books[0].MediaType != "ebook" || books[0].Statistics.BookFileCount != 1 {
		t.Fatalf("books = %+v, want one ebook with bookFileCount 1", books)
	}
}

// TestGetQueue asserts GetQueue reads the queue as one complete bounded page —
// explicit pageSize (the server's default page is 10 rows, a silent
// truncation) — with author context and the unknown-author rows Chaptarr
// would otherwise drop before the page is even assembled.
func TestGetQueue(t *testing.T) {
	var gotPath string
	var gotQuery url.Values

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"totalRecords":2,"records":[{"id":1,"bookId":42,"title":"Some Book"},{"id":2,"bookId":0,"title":"Unmatched.Release.epub"}]}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "key")
	items, err := c.GetQueue()
	if err != nil {
		t.Fatalf("GetQueue: %v", err)
	}

	if gotPath != "/api/v1/queue" {
		t.Errorf("path = %s, want /api/v1/queue", gotPath)
	}
	if gotQuery.Get("includeAuthor") != "true" {
		t.Errorf("includeAuthor = %q, want true (query %v)", gotQuery.Get("includeAuthor"), gotQuery)
	}
	if gotQuery.Get("pageSize") != "1000" {
		t.Errorf("pageSize = %q, want 1000 — an unpaged read is the server's 10-row default page", gotQuery.Get("pageSize"))
	}
	if gotQuery.Get("includeUnknownAuthorItems") != "true" {
		t.Errorf("includeUnknownAuthorItems = %q, want true (query %v)", gotQuery.Get("includeUnknownAuthorItems"), gotQuery)
	}
	if len(items) != 2 || items[0].BookID != 42 || items[1].BookID != 0 {
		t.Fatalf("items = %+v, want the matched item and the unknown-author item", items)
	}
}

// TestSearchReleasesEnvelope asserts the interactive-search response is parsed
// whether Chaptarr returns its {"releases":[...]} envelope (this fork) or a
// bare array (stock Servarr). A bare-array assumption here surfaced in the app
// as "type '_Map<String, dynamic>' is not a subtype of type 'List<dynamic>'".
func TestSearchReleasesEnvelope(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"envelope", `{"releases":[{"guid":"a","indexerId":2,"title":"R","protocol":"torrent"}],"hiddenReleases":[],"filterSummary":{}}`},
		{"bare array", `[{"guid":"a","indexerId":2,"title":"R","protocol":"torrent"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			releases, err := NewClient(srv.URL, "key").SearchReleases(42)
			if err != nil {
				t.Fatalf("SearchReleases: %v", err)
			}
			if len(releases) != 1 || releases[0].GUID != "a" || releases[0].IndexerID != 2 {
				t.Fatalf("releases = %+v, want one release guid=a indexerId=2", releases)
			}
		})
	}
}

// TestGenreListUnmarshal asserts genres decode from both a comma-separated
// string (this Chaptarr fork) and a JSON array (stock Servarr).
func TestGenreListUnmarshal(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty string", `""`, []string{}},
		{"comma string", `"Science Fiction, Fantasy"`, []string{"Science Fiction", "Fantasy"}},
		{"single string", `"Fiction"`, []string{"Fiction"}},
		{"array", `["A","B"]`, []string{"A", "B"}},
		{"null", `null`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var g genreList
			if err := json.Unmarshal([]byte(tc.in), &g); err != nil {
				t.Fatalf("unmarshal %q: %v", tc.in, err)
			}
			if len(g) != len(tc.want) {
				t.Fatalf("got %#v, want %#v", []string(g), tc.want)
			}
			for i := range tc.want {
				if g[i] != tc.want[i] {
					t.Fatalf("got %#v, want %#v", []string(g), tc.want)
				}
			}
		})
	}
}

// TestAddBookToleratesStringGenres guards the add-book response decode: Chaptarr
// returns the created book with genres as a string, which previously surfaced a
// successful add as "cannot unmarshal string into Go struct field Book.genres of
// type []string".
func TestAddBookToleratesStringGenres(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":51,"title":"Dune Messiah","foreignBookId":"x","monitored":true,`+
			`"genres":"Science Fiction, Fantasy","releaseDate":null,"editions":null,`+
			`"author":{"id":9,"authorName":"Frank Herbert"},"statistics":{"bookCount":1}}`)
	}))
	defer srv.Close()

	book, err := NewClient(srv.URL, "key").AddBook(AddBookRequest{ForeignBookID: "x"})
	if err != nil {
		t.Fatalf("AddBook: %v", err)
	}
	if book.ID != 51 || !book.Monitored {
		t.Fatalf("book = id %d monitored %v, want id 51 monitored true", book.ID, book.Monitored)
	}
	if len(book.Genres) != 2 || book.Genres[0] != "Science Fiction" {
		t.Fatalf("genres = %#v, want [Science Fiction, Fantasy]", []string(book.Genres))
	}
}

// TestAddBookClassifiesAuthorPendingImport pins the 0.9.879+ refusal: the fork
// queues an unknown author for an asynchronous metadata import and 400s the add
// until it lands. The body below is the live validation payload shape; the
// classified error must be errors.Is-able without echoing the upstream text.
func TestAddBookClassifiesAuthorPendingImport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `[{"propertyName":"Author","errorMessage":"Author 'Shiv Shivakumar' isn't available yet on our metadata server. It has been queued for import (pending ID: 3) and will be imported automatically when it becomes available."}]`)
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, "key").AddBook(AddBookRequest{ForeignBookID: "gr:253739298"})
	if !errors.Is(err, ErrAuthorPendingImport) {
		t.Fatalf("AddBook error = %v, want ErrAuthorPendingImport", err)
	}
	if strings.Contains(err.Error(), "Shivakumar") {
		t.Fatalf("error echoed upstream validation text: %v", err)
	}
}

// TestAddBookClassifiesEditionsNotHydrated pins the second named 0.9.879+
// refusal: an add whose payload carried no editions before the fork's metadata
// service could hydrate them.
func TestAddBookClassifiesEditionsNotHydrated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `[{"propertyName":"Editions","errorMessage":"Cannot add book: no editions were supplied. Retry the add so Chaptarr can hydrate edition metadata from the metadata server."}]`)
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, "key").AddBook(AddBookRequest{ForeignBookID: "x"})
	if !errors.Is(err, ErrEditionsNotHydrated) {
		t.Fatalf("AddBook error = %v, want ErrEditionsNotHydrated", err)
	}
}

// TestAddBookOtherRejectionsStayGenericStatusErrors keeps every other non-2xx —
// an unmatched validation failure, a non-array error body, a plain 500 — on the
// existing sanitized status-only error, never a classified verdict.
func TestAddBookOtherRejectionsStayGenericStatusErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"unmatched validation", http.StatusBadRequest, `[{"propertyName":"QualityProfileId","errorMessage":"At least one quality profile must be selected"}]`},
		{"non-array body", http.StatusBadRequest, `{"message":"Validation failed"}`},
		{"server error", http.StatusInternalServerError, `{"message":"Object reference not set to an instance of an object."}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			_, err := NewClient(srv.URL, "key").AddBook(AddBookRequest{ForeignBookID: "x"})
			if err == nil {
				t.Fatal("AddBook returned nil error")
			}
			if errors.Is(err, ErrAuthorPendingImport) || errors.Is(err, ErrEditionsNotHydrated) {
				t.Fatalf("error = %v, misclassified as a named rejection", err)
			}
			want := fmt.Sprintf("returned status %d", tc.status)
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want it to carry %q", err, want)
			}
		})
	}
}

// TestFormatOf asserts the quality-name classifier maps representative ebook
// and audiobook formats and falls back to "unknown".
func TestFormatOf(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"EPUB", "ebook"},
		{"epub", "ebook"},
		{"AZW3", "ebook"},
		{"PDF", "ebook"},
		{"Kindle Edition", "ebook"},
		{"eBook", "ebook"},
		{"MP3-320", "audiobook"},
		{"M4B", "audiobook"},
		{"FLAC", "audiobook"},
		{"Audible Audio", "audiobook"},
		{"Audio CD", "audiobook"},
		{"AudioBook", "audiobook"},
		{"Unknown Quality", "unknown"},
		{"", "unknown"},
	}
	for _, tc := range cases {
		if got := FormatOf(tc.name); got != tc.want {
			t.Errorf("FormatOf(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestGetImportHistoryIsBoundedAndServerFiltered(t *testing.T) {
	var gotQuery url.Values
	total := 1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"page":1,"pageSize":20,"totalRecords":%d,"records":[{"id":88,"eventType":"bookFileImported","downloadId":"down-1","authorId":4,"bookId":42}]}`, total)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "key")
	records, err := c.GetImportHistory(42, "down-1", 20)
	if err != nil {
		t.Fatalf("GetImportHistory: %v", err)
	}
	if gotQuery.Get("eventType") != "3" || gotQuery.Get("bookId") != "42" || gotQuery.Get("downloadId") != "down-1" {
		t.Fatalf("import history query = %v", gotQuery)
	}
	if len(records) != 1 || records[0].DownloadID != "down-1" || records[0].BookID != 42 {
		t.Fatalf("records = %+v", records)
	}

	// A fork that ignores the filters overflows the bound and must fail closed
	// instead of yielding a partial (and therefore untrustworthy) witness.
	total = 21
	if _, err := c.GetImportHistory(42, "down-1", 20); err == nil {
		t.Fatal("overflowing import history did not fail closed")
	}
}

func TestGetBookFilesForBookRefiltersClientSide(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		// A fork ignoring ?bookId= returns every file; the client must refilter.
		_, _ = io.WriteString(w, `[{"id":5,"authorId":4,"bookId":42},{"id":6,"authorId":4,"bookId":99}]`)
	}))
	defer srv.Close()

	files, err := NewClient(srv.URL, "key").GetBookFilesForBook(42)
	if err != nil {
		t.Fatalf("GetBookFilesForBook: %v", err)
	}
	if gotQuery.Get("bookId") != "42" {
		t.Fatalf("bookfile query = %v", gotQuery)
	}
	if len(files) != 1 || files[0].ID != 5 {
		t.Fatalf("files = %+v, want only book 42's file", files)
	}
}

func TestQueueItemDecodesAddedTimestamp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"totalRecords":1,"records":[{"id":1,"bookId":42,"added":"2026-07-10T09:00:00Z"}]}`)
	}))
	defer srv.Close()

	items, err := NewClient(srv.URL, "key").GetQueueDetailed()
	if err != nil {
		t.Fatalf("GetQueueDetailed: %v", err)
	}
	if len(items) != 1 || items[0].Added == nil || !items[0].Added.Equal(time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("items = %+v, want added timestamp decoded", items)
	}
}

// TestGetQueueDetailedFailsClosedOnIncompleteSnapshots pins the snapshot
// contract remediation observation depends on: truncation, oversize, and
// duplicate ids are errors, never a silently shortened queue.
func TestGetQueueDetailedFailsClosedOnIncompleteSnapshots(t *testing.T) {
	body := `{"totalRecords":2,"records":[{"id":1,"bookId":42}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "key")

	if _, err := c.GetQueueDetailed(); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("short page error = %v", err)
	}
	body = `{"totalRecords":1001,"records":[]}`
	if _, err := c.GetQueueDetailed(); err == nil || !strings.Contains(err.Error(), "oversized") {
		t.Fatalf("oversized total error = %v", err)
	}
	body = `{"totalRecords":2,"records":[{"id":7,"bookId":42},{"id":7,"bookId":43}]}`
	if _, err := c.GetQueueDetailed(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate id error = %v", err)
	}
}

func TestGetBookDistinguishesMissingFromError(t *testing.T) {
	status := http.StatusNotFound
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"message":"NotFound"}`)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "key")

	book, err := c.GetBook(123)
	if err != nil || book != nil {
		t.Fatalf("deleted book = (%+v, %v), want (nil, nil)", book, err)
	}
	status = http.StatusBadGateway
	if _, err := c.GetBook(123); err == nil {
		t.Fatal("non-2xx book read did not error")
	}
}

func TestGetBooksRefiltersByAuthorClientSide(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// A fork ignoring ?authorId= returns every book; the client must refilter.
		_, _ = io.WriteString(w, `[{"id":1,"authorId":4,"title":"Mine"},{"id":2,"authorId":9,"title":"Other"}]`)
	}))
	defer srv.Close()

	books, err := NewClient(srv.URL, "key").GetBooks(4)
	if err != nil {
		t.Fatalf("GetBooks: %v", err)
	}
	if len(books) != 1 || books[0].ID != 1 {
		t.Fatalf("books = %+v, want only author 4's book", books)
	}
}

// TestGetImportHistorySinceSkipsUndatedRecords pins the Readarr-lineage
// catch-up reader: the v1 endpoint with the import event filter, cursor
// windowing, and that a record without a date is skipped — it can neither be
// windowed nor prove the page reached past the cursor.
func TestGetImportHistorySinceSkipsUndatedRecords(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/history" || r.URL.Query().Get("eventType") != "3" {
			t.Errorf("request = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"page":1,"pageSize":10,"totalRecords":3,"records":[
			{"id":3,"bookId":7,"eventType":"bookFileImported","date":"2026-07-25T12:30:00Z"},
			{"id":2,"bookId":8,"eventType":"bookFileImported"},
			{"id":1,"bookId":9,"eventType":"bookFileImported","date":"2026-07-25T11:00:00Z"}]}`)
	}))
	t.Cleanup(server.Close)

	since, err := time.Parse(time.RFC3339, "2026-07-25T12:00:00Z")
	if err != nil {
		t.Fatalf("parse since: %v", err)
	}
	records, complete, err := NewClient(server.URL, "key").GetImportHistorySince(since, 10)
	if err != nil {
		t.Fatalf("GetImportHistorySince() error = %v", err)
	}
	if !complete {
		t.Fatal("a dated record at or before the cursor must prove the window complete")
	}
	if len(records) != 1 || records[0].BookID != 7 {
		t.Fatalf("in-window records = %+v, want only book 7", records)
	}
}
