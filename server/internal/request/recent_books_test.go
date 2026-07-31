package request

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

func at(t time.Time) *time.Time { return &t }

func recentBook(id int, foreignID, title, mediaType string, images ...chaptarr.Image) chaptarr.Book {
	b := chaptarr.Book{
		ID:            id,
		AuthorID:      100 + id,
		ForeignBookID: foreignID,
		Title:         title,
		MediaType:     mediaType,
		Images:        images,
	}
	b.Statistics.BookFileCount = 1
	return b
}

// TestBuildRecentBooksOrdersByFileImportNotRecordAge is the whole point of the
// row: a book requested long ago and downloaded today must lead. Ordering by
// anything record-shaped reproduces the movies-tab defect with a books skin.
func TestBuildRecentBooksOrdersByFileImportNotRecordAge(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	books := []chaptarr.Book{
		recentBook(1, "fb-1", "Requested Long Ago", "ebook"),
		recentBook(2, "fb-2", "Added Recently", "ebook"),
	}
	files := map[int][]chaptarr.BookFile{
		1: {{BookID: 1, DateAdded: at(now)}},
		2: {{BookID: 2, DateAdded: at(now.AddDate(0, 0, -30))}},
	}

	items := buildRecentBooks(books, files, 10)
	if len(items) != 2 || items[0].BookID != 1 {
		t.Fatalf("order = %+v, want the newly imported record first", items)
	}
}

// TestBuildRecentBooksKeepsEbookAndAudiobookSeparate: the two formats are
// distinct arrivals at distinct times. Merging them would hide the second.
func TestBuildRecentBooksKeepsEbookAndAudiobookSeparate(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	books := []chaptarr.Book{
		recentBook(1, "fb-1", "Ahsoka", "ebook"),
		recentBook(2, "fb-1", "Ahsoka", "audiobook"),
	}
	files := map[int][]chaptarr.BookFile{
		1: {{BookID: 1, DateAdded: at(now.AddDate(0, 0, -2))}},
		2: {{BookID: 2, DateAdded: at(now)}},
	}

	items := buildRecentBooks(books, files, 10)
	if len(items) != 2 {
		t.Fatalf("items = %+v, want both formats", items)
	}
	if items[0].Format != "audiobook" || items[1].Format != "ebook" {
		t.Errorf("formats = %q,%q, want audiobook then ebook", items[0].Format, items[1].Format)
	}
	if items[0].ForeignBookID != items[1].ForeignBookID {
		t.Error("the two records should share one foreignBookId")
	}
}

// TestBuildRecentBooksUsesNewestFileForMultiPartRecord: a multi-part audiobook
// is one library record, so it is one card dated by its newest file.
func TestBuildRecentBooksUsesNewestFileForMultiPartRecord(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	newest := now.AddDate(0, 0, -1)
	books := []chaptarr.Book{recentBook(1, "fb-1", "Long Audiobook", "audiobook")}
	files := map[int][]chaptarr.BookFile{
		1: {
			{BookID: 1, DateAdded: at(now.AddDate(0, 0, -3))},
			{BookID: 1, DateAdded: at(newest)},
			{BookID: 1, DateAdded: at(now.AddDate(0, 0, -2))},
		},
	}

	items := buildRecentBooks(books, files, 10)
	if len(items) != 1 {
		t.Fatalf("items = %+v, want one card per record", items)
	}
	if !items[0].ImportedAt.Equal(newest) {
		t.Errorf("importedAt = %v, want the newest file %v", items[0].ImportedAt, newest)
	}
}

// TestBuildRecentBooksSkipsRecordsWithoutFileTimestamp: no timestamp means no
// recency claim. Zero-valuing one would also sort it to the bottom forever.
func TestBuildRecentBooksSkipsRecordsWithoutFileTimestamp(t *testing.T) {
	books := []chaptarr.Book{
		recentBook(1, "fb-1", "No Timestamp", "ebook"),
		recentBook(2, "fb-2", "No Files At All", "ebook"),
	}
	files := map[int][]chaptarr.BookFile{
		1: {{BookID: 1, DateAdded: nil}},
	}

	if items := buildRecentBooks(books, files, 10); len(items) != 0 {
		t.Errorf("items = %+v, want none", items)
	}
}

// TestBuildRecentBooksCoverStaysClientReachable: clients cannot dereference an
// arr-origin URL, so only a proxied relative path or a CDN copy may ship.
func TestBuildRecentBooksCoverStaysClientReachable(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		images []chaptarr.Image
		want   string
	}{
		{
			name: "prefers the cover type over other art",
			images: []chaptarr.Image{
				{CoverType: "fanart", URL: "/MediaCover/Books/7/fanart.jpg"},
				{CoverType: "cover", URL: "/MediaCover/Books/7/cover.jpg"},
			},
			want: "/MediaCover/Books/7/cover.jpg",
		},
		{
			name: "falls back to the CDN copy for an arr-origin url",
			images: []chaptarr.Image{{
				CoverType: "cover",
				URL:       "http://chaptarr:8787/MediaCover/Books/7/cover.jpg",
				RemoteURL: "https://images.example/cover.jpg",
			}},
			want: "https://images.example/cover.jpg",
		},
		{
			name: "drops an arr-origin url with no CDN copy",
			images: []chaptarr.Image{{
				CoverType: "cover",
				URL:       "http://chaptarr:8787/MediaCover/Books/7/cover.jpg",
			}},
			want: "",
		},
		{
			name:   "no images at all",
			images: nil,
			want:   "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			books := []chaptarr.Book{recentBook(7, "fb-7", "Cover Test", "ebook", c.images...)}
			files := map[int][]chaptarr.BookFile{7: {{BookID: 7, DateAdded: at(now)}}}

			items := buildRecentBooks(books, files, 10)
			if len(items) != 1 {
				t.Fatalf("items = %+v, want one", items)
			}
			if items[0].Cover != c.want {
				t.Errorf("cover = %q, want %q", items[0].Cover, c.want)
			}
		})
	}
}

// TestBuildRecentBooksTieBreakIsDeterministic: a bulk-scanned library stamps
// every file identically, and the row must not reshuffle between fetches.
func TestBuildRecentBooksTieBreakIsDeterministic(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	books := []chaptarr.Book{
		recentBook(2, "fb-2", "Two", "ebook"),
		recentBook(3, "fb-3", "Three", "ebook"),
		recentBook(1, "fb-1", "One", "ebook"),
	}
	files := map[int][]chaptarr.BookFile{
		1: {{BookID: 1, DateAdded: at(now)}},
		2: {{BookID: 2, DateAdded: at(now)}},
		3: {{BookID: 3, DateAdded: at(now)}},
	}

	first := buildRecentBooks(books, files, 10)
	if len(first) != 3 || first[0].BookID != 3 || first[2].BookID != 1 {
		t.Fatalf("order = %+v, want descending book id on a tie", first)
	}
	shuffled := []chaptarr.Book{books[2], books[0], books[1]}
	second := buildRecentBooks(shuffled, files, 10)
	for i := range first {
		if first[i].BookID != second[i].BookID {
			t.Fatalf("order depends on input order: %+v vs %+v", first, second)
		}
	}
}

// TestBuildRecentBooksRespectsLimit keeps the row a row.
func TestBuildRecentBooksRespectsLimit(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	var books []chaptarr.Book
	files := map[int][]chaptarr.BookFile{}
	for i := 1; i <= 12; i++ {
		books = append(books, recentBook(i, "fb", "Book", "ebook"))
		files[i] = []chaptarr.BookFile{{BookID: i, DateAdded: at(now.Add(-time.Duration(i) * time.Hour))}}
	}

	if got := buildRecentBooks(books, files, 5); len(got) != 5 {
		t.Errorf("len = %d, want 5", len(got))
	}
}

// TestTakeRecentBooksNeverReturnsNil keeps the JSON body an array, not null,
// so the app's list decode never has to special-case it.
func TestTakeRecentBooksNeverReturnsNil(t *testing.T) {
	if got := takeRecentBooks(nil, 10); got == nil || len(got) != 0 {
		t.Errorf("takeRecentBooks(nil) = %#v, want an empty slice", got)
	}
}

// TestGetRecentBooksWithoutChaptarrGrantReturnsEmpty: a user with no Chaptarr
// access has no row, which is not a failure. An error here would surface as a
// broken tab for every non-books user.
func TestGetRecentBooksWithoutChaptarrGrantReturnsEmpty(t *testing.T) {
	// A nil registry is how resolveChaptarr reports "no client for this user".
	svc := &Service{}

	digest, err := svc.GetRecentBooksForInstance(1, "", 20)
	if err != nil {
		t.Fatalf("err = %v, want nil for an ungranted user", err)
	}
	if digest == nil || digest.Items == nil {
		t.Fatal("digest must carry an empty list, never nil")
	}
	if len(digest.Items) != 0 {
		t.Errorf("items = %+v, want none", digest.Items)
	}
}

// TestGetBookRecentClampsLimit keeps a caller from asking for the library.
func TestGetBookRecentClampsLimit(t *testing.T) {
	handler := NewHandler(&Service{})
	for _, raw := range []string{"0", "999", "abc", "-5", ""} {
		t.Run("limit="+raw, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/requests/book-recent?limit="+raw, nil)
			req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey,
				&auth.Claims{UserID: 1, Role: auth.RoleUser}))
			resp := httptest.NewRecorder()
			handler.GetBookRecent(resp, req)
			// A nil service means no Chaptarr client, so this is the empty path;
			// what matters is that no limit value can panic or 500.
			if resp.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
			}
			if !strings.Contains(resp.Body.String(), `"items":[]`) {
				t.Errorf("body = %s, want an empty items array", resp.Body.String())
			}
		})
	}
}

// TestGetBookRecentRequiresAuthentication guards the claims check.
func TestGetBookRecentRequiresAuthentication(t *testing.T) {
	resp := httptest.NewRecorder()
	NewHandler(nil).GetBookRecent(resp, httptest.NewRequest(http.MethodGet, "/api/requests/book-recent", nil))
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.Code)
	}
}
