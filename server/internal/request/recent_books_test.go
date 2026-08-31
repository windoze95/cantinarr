package request

import (
	"context"
	"fmt"
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

// TestBuildRecentBooksMergesTheFormatsOfOneTitle: a title's ebook and audiobook
// are separate records sharing a foreignBookId. They were once two cards,
// because each announced its own format. The card now leads with the title's
// ownership — the same text and the same link for both records — so a second
// card says nothing the first did not.
func TestBuildRecentBooksMergesTheFormatsOfOneTitle(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	books := []chaptarr.Book{
		recentBook(1, "fb-1", "Ahsoka", "ebook"),
		recentBook(2, "fb-1", "Ahsoka", "audiobook"),
	}
	files := map[int][]chaptarr.BookFile{
		1: {{BookID: 1, DateAdded: at(now.AddDate(0, 0, -30))}},
		2: {{BookID: 2, DateAdded: at(now)}},
	}

	items := buildRecentBooks(books, files, 10)

	if len(items) != 1 {
		t.Fatalf("items = %+v, want one card for the title", items)
	}
	// Merging must not bury the late arrival: the card carries the newest
	// date, so an audiobook landing today floats a title whose ebook arrived a
	// month ago back to the front of the row.
	if !items[0].ImportedAt.Equal(now) {
		t.Errorf("ImportedAt = %v, want the newest arrival %v", items[0].ImportedAt, now)
	}
	// No single format describes the card, and naming either would be wrong.
	if items[0].Format != "" {
		t.Errorf("Format = %q, want empty for a card covering both formats", items[0].Format)
	}
	if items[0].ForeignBookID != "fb-1" {
		t.Errorf("ForeignBookID = %q, want the shared id", items[0].ForeignBookID)
	}
}

// TestBuildRecentBooksMergesDuplicateRecordsOfOneFormat: a library really does
// hold two ebook records of the same title. They share a foreignBookId, so they
// are the same book by the library's own identity rule — and they render the
// same text behind the same link.
func TestBuildRecentBooksMergesDuplicateRecordsOfOneFormat(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	books := []chaptarr.Book{
		recentBook(11185, "fb-hobbit", "The Hobbit", "ebook"),
		recentBook(11179, "fb-hobbit", "The Hobbit", "ebook"),
	}
	files := map[int][]chaptarr.BookFile{
		11185: {{BookID: 11185, DateAdded: at(now)}},
		11179: {{BookID: 11179, DateAdded: at(now)}},
	}

	items := buildRecentBooks(books, files, 10)

	if len(items) != 1 {
		t.Fatalf("items = %+v, want one card", items)
	}
	// Every record agreed on the format, so the card can still name it.
	if items[0].Format != "ebook" {
		t.Errorf("Format = %q, want ebook", items[0].Format)
	}
}

// TestBuildRecentBooksNeverMergesRecordsWithoutAForeignBookID: an unkeyed record
// has no identity to share, so it can never be folded into another title.
func TestBuildRecentBooksNeverMergesRecordsWithoutAForeignBookID(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	books := []chaptarr.Book{
		recentBook(1, "", "One Unkeyed Book", "ebook"),
		recentBook(2, "", "Another Unkeyed Book", "ebook"),
	}
	files := map[int][]chaptarr.BookFile{
		1: {{BookID: 1, DateAdded: at(now)}},
		2: {{BookID: 2, DateAdded: at(now.AddDate(0, 0, -1))}},
	}

	if items := buildRecentBooks(books, files, 10); len(items) != 2 {
		t.Fatalf("items = %+v, want both unkeyed records kept apart", items)
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
		// Distinct ids: same-id records are one title now, so a shared id here
		// would be measuring the merge rather than the limit.
		books = append(books, recentBook(i, fmt.Sprintf("fb-%d", i), "Book", "ebook"))
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

// TestBuildRecentBooksMergeTakesNewArtWhenEstablishedRecordHasNone guards the
// other direction of the age preference: preferring the established record must
// not mean discarding the only art the title has.
func TestBuildRecentBooksMergeTakesNewArtWhenEstablishedRecordHasNone(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	older := now.Add(-72 * time.Hour)
	newArt := chaptarr.Image{CoverType: "cover", URL: "/MediaCover/Books/12/cover.jpg"}

	books := []chaptarr.Book{
		recentBook(11, "fb-y", "Art Only On The New One", "ebook"),
		recentBook(12, "fb-y", "Art Only On The New One", "audiobook", newArt),
	}
	files := map[int][]chaptarr.BookFile{
		11: {{BookID: 11, DateAdded: &older}},
		12: {{BookID: 12, DateAdded: &now}},
	}
	items := buildRecentBooks(books, files, recentBooksMaxItems)
	if len(items) != 1 {
		t.Fatalf("expected one merged card, got %d", len(items))
	}
	if items[0].Cover != newArt.URL {
		t.Errorf("cover = %q, want %q — the age preference must not drop the only art available",
			items[0].Cover, newArt.URL)
	}
}

// TestBuildRecentBooksMergeKeepsAUsableCover is the consolidation defect: a
// title whose cover rendered fine as a single-format card lost its art the
// moment a second format arrived and merged into it.
//
// A newly created Chaptarr record is emitted with a /MediaCover path before the
// cover file behind it exists, and it may carry no usable image at all. The
// merge used to take the first NON-EMPTY cover in library order, so whichever
// record happened to come first decided the card — and once that was the new,
// art-less arrival, the sibling's perfectly good cover was never consulted
// again on any refresh. The card is dated by the newest arrival, but its art
// must come from whichever record actually has some.
func TestBuildRecentBooksMergeKeepsAUsableCover(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	older := now.Add(-72 * time.Hour)

	good := chaptarr.Image{CoverType: "cover", URL: "/MediaCover/Books/11/cover.jpg"}

	cases := []struct {
		name  string
		books []chaptarr.Book
	}{
		{
			// The new audiobook record sorts first and has no art at all.
			name: "art-less new arrival listed first",
			books: []chaptarr.Book{
				recentBook(12, "fb-x", "Consolidated Title", "audiobook"),
				recentBook(11, "fb-x", "Consolidated Title", "ebook", good),
			},
		},
		{
			name: "art-less new arrival listed last",
			books: []chaptarr.Book{
				recentBook(11, "fb-x", "Consolidated Title", "ebook", good),
				recentBook(12, "fb-x", "Consolidated Title", "audiobook"),
			},
		},
		{
			// The new record carries an image the client may not dereference,
			// so clientReachableImage yields "" for it — the sibling must win.
			name: "new arrival carries an unusable arr-origin image",
			books: []chaptarr.Book{
				recentBook(12, "fb-x", "Consolidated Title", "audiobook",
					chaptarr.Image{CoverType: "cover", URL: "http://chaptarr:8787/MediaCover/Books/12/cover.jpg"}),
				recentBook(11, "fb-x", "Consolidated Title", "ebook", good),
			},
		},
		{
			// THE REPORTED CASE. The new arrival carries a perfectly
			// well-formed /MediaCover path whose file does not exist, because
			// Chaptarr emits the path for every record regardless. Nothing here
			// can tell it from a live one, so the established record's art has
			// to win on age — otherwise the card silently swaps good art for a
			// permanent placeholder the moment a second format lands.
			name: "new arrival carries a well-formed path to art that does not exist",
			books: []chaptarr.Book{
				recentBook(12, "fb-x", "Consolidated Title", "audiobook",
					chaptarr.Image{CoverType: "cover", URL: "/MediaCover/Books/12/cover.jpg"}),
				recentBook(11, "fb-x", "Consolidated Title", "ebook", good),
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := map[int][]chaptarr.BookFile{
				11: {{BookID: 11, DateAdded: &older}},
				12: {{BookID: 12, DateAdded: &now}},
			}
			items := buildRecentBooks(c.books, files, recentBooksMaxItems)
			if len(items) != 1 {
				t.Fatalf("expected the two formats to merge into one card, got %d", len(items))
			}
			if items[0].Cover != good.URL {
				t.Errorf("cover = %q, want %q — the merged card must keep the art one of its records actually has",
					items[0].Cover, good.URL)
			}
			// The card is still dated and identified by the newest arrival.
			if !items[0].ImportedAt.Equal(now) {
				t.Errorf("importedAt = %v, want %v", items[0].ImportedAt, now)
			}
			if items[0].BookID != 12 {
				t.Errorf("bookID = %d, want 12 (the newest arrival)", items[0].BookID)
			}
		})
	}
}
