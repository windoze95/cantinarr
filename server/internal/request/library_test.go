package request

import (
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

// findTitle returns the digest title whose Title matches, or fails.
func findTitle(t *testing.T, d BookLibraryDigest, title string) LibraryTitle {
	t.Helper()
	for _, lt := range d.Titles {
		if lt.Title == title {
			return lt
		}
	}
	t.Fatalf("title %q not found in digest %+v", title, d.Titles)
	return LibraryTitle{}
}

// TestReduceLibraryMergesFormatsByForeignBookID asserts the two records of one
// title (same foreignBookId, distinct mediaType) collapse into a single title
// with each format routed to its slot, and that downloaded comes from
// Statistics.BookFileCount.
func TestReduceLibraryMergesFormatsByForeignBookID(t *testing.T) {
	rel := time.Date(1991, 5, 1, 0, 0, 0, 0, time.UTC)
	books := []chaptarr.Book{
		{
			ID:            1,
			Title:         "Heir to the Empire",
			ForeignBookID: "fb-1",
			MediaType:     "ebook",
			Monitored:     true,
			ReleaseDate:   &rel,
			Author:        &chaptarr.AuthorContext{AuthorName: "Timothy Zahn"},
			Statistics:    chaptarr.BookStatistics{BookFileCount: 1},
			Images: []chaptarr.Image{
				{CoverType: "cover", URL: "/MediaCover/Books/1/cover.jpg"},
			},
		},
		{
			ID:            2,
			Title:         "Heir to the Empire",
			ForeignBookID: "fb-1",
			MediaType:     "audiobook",
			Monitored:     true,
			Author:        &chaptarr.AuthorContext{AuthorName: "Timothy Zahn"},
			Statistics:    chaptarr.BookStatistics{BookFileCount: 0},
		},
	}

	digest := reduceLibrary(books)
	if len(digest.Titles) != 1 {
		t.Fatalf("len(titles) = %d, want 1 (digest %+v)", len(digest.Titles), digest.Titles)
	}
	lt := digest.Titles[0]
	if lt.Title != "Heir to the Empire" || lt.Author != "Timothy Zahn" || lt.Year != 1991 {
		t.Fatalf("title meta = %+v, want Heir to the Empire / Timothy Zahn / 1991", lt)
	}
	if !lt.Ebook.Monitored || !lt.Ebook.Downloaded {
		t.Errorf("ebook = %+v, want monitored && downloaded", lt.Ebook)
	}
	if !lt.Audiobook.Monitored || lt.Audiobook.Downloaded {
		t.Errorf("audiobook = %+v, want monitored && !downloaded", lt.Audiobook)
	}
	if lt.Cover != "/MediaCover/Books/1/cover.jpg" {
		t.Errorf("cover = %q, want /MediaCover/Books/1/cover.jpg", lt.Cover)
	}
	if lt.ForeignBookID != "fb-1" {
		t.Errorf("foreignBookID = %q, want fb-1", lt.ForeignBookID)
	}
}

func TestRecordsByForeignID(t *testing.T) {
	books := []chaptarr.Book{
		{ID: 1, Title: "Ahsoka", ForeignBookID: "fb-9", MediaType: "ebook"},
		{ID: 2, Title: "Ahsoka", ForeignBookID: "fb-9", MediaType: "audiobook"},
		{ID: 3, Title: "Other", ForeignBookID: "fb-x", MediaType: "ebook"},
	}
	title, byFormat := recordsByForeignID(books, "fb-9")
	if title != "Ahsoka" {
		t.Fatalf("title = %q, want Ahsoka", title)
	}
	if byFormat[chaptarr.FormatEbook] == nil || byFormat[chaptarr.FormatEbook].ID != 1 {
		t.Errorf("ebook = %+v, want id 1", byFormat[chaptarr.FormatEbook])
	}
	if byFormat[chaptarr.FormatAudiobook] == nil || byFormat[chaptarr.FormatAudiobook].ID != 2 {
		t.Errorf("audiobook = %+v, want id 2", byFormat[chaptarr.FormatAudiobook])
	}
	if title, _ := recordsByForeignID(books, ""); title != "" {
		t.Errorf("empty foreignID matched %q, want nothing", title)
	}
}

// TestReduceLibraryRoutesByLoneEditionFormat asserts a record with no mediaType
// is routed by the format of its single edition (EPUB -> ebook).
func TestReduceLibraryRoutesByLoneEditionFormat(t *testing.T) {
	books := []chaptarr.Book{
		{
			ID:            5,
			Title:         "Some Ebook",
			ForeignBookID: "fb-5",
			MediaType:     "", // no book-level format
			Monitored:     true,
			Author:        &chaptarr.AuthorContext{AuthorName: "An Author"},
			Statistics:    chaptarr.BookStatistics{BookFileCount: 1},
			Editions:      []chaptarr.Edition{{Format: "EPUB"}},
		},
	}

	digest := reduceLibrary(books)
	lt := findTitle(t, digest, "Some Ebook")
	if !lt.Ebook.Monitored || !lt.Ebook.Downloaded {
		t.Errorf("ebook = %+v, want monitored && downloaded (routed from lone EPUB edition)", lt.Ebook)
	}
	if lt.Audiobook != (FormatOwnership{}) {
		t.Errorf("audiobook = %+v, want zero (no audiobook record)", lt.Audiobook)
	}
}

// TestReduceLibraryDoesNotMergeWithoutForeignBookID asserts two records that
// both lack a foreignBookId stay distinct (keyed by their record id), so
// unrelated records never collapse into one title.
func TestReduceLibraryDoesNotMergeWithoutForeignBookID(t *testing.T) {
	books := []chaptarr.Book{
		{ID: 10, Title: "Book Ten", MediaType: "ebook", Author: &chaptarr.AuthorContext{AuthorName: "A"}},
		{ID: 11, Title: "Book Eleven", MediaType: "ebook", Author: &chaptarr.AuthorContext{AuthorName: "B"}},
	}

	digest := reduceLibrary(books)
	if len(digest.Titles) != 2 {
		t.Fatalf("len(titles) = %d, want 2 (digest %+v)", len(digest.Titles), digest.Titles)
	}
}

// TestReduceLibraryEmptyInputNonNilSlice asserts an empty library reduces to a
// non-nil empty Titles slice (so it serializes as [] not null).
func TestReduceLibraryEmptyInputNonNilSlice(t *testing.T) {
	digest := reduceLibrary(nil)
	if digest.Titles == nil {
		t.Fatalf("Titles is nil, want non-nil empty slice")
	}
	if len(digest.Titles) != 0 {
		t.Fatalf("len(Titles) = %d, want 0", len(digest.Titles))
	}
}

// TestReduceLibraryDownloadedUsesBookFileCount asserts downloaded is derived
// from Statistics.BookFileCount, not from any hasFiles-style flag: a record with
// BookFileCount == 0 is not downloaded even though it is monitored.
func TestReduceLibraryDownloadedUsesBookFileCount(t *testing.T) {
	books := []chaptarr.Book{
		{
			ID:            20,
			Title:         "Monitored Not Downloaded",
			ForeignBookID: "fb-20",
			MediaType:     "ebook",
			Monitored:     true,
			Author:        &chaptarr.AuthorContext{AuthorName: "A"},
			Statistics:    chaptarr.BookStatistics{BookFileCount: 0, BookCount: 1},
		},
	}

	digest := reduceLibrary(books)
	lt := findTitle(t, digest, "Monitored Not Downloaded")
	if !lt.Ebook.Monitored {
		t.Errorf("ebook.monitored = false, want true")
	}
	if lt.Ebook.Downloaded {
		t.Errorf("ebook.downloaded = true, want false (BookFileCount is 0)")
	}
}

func TestReduceLibraryAggregatesDuplicateFormatTruth(t *testing.T) {
	books := []chaptarr.Book{
		{ID: 31, Title: "Duplicate", ForeignBookID: "dup", MediaType: "ebook", Monitored: true, Statistics: chaptarr.BookStatistics{BookFileCount: 1}},
		{ID: 32, Title: "Duplicate", ForeignBookID: "dup", MediaType: "ebook", Monitored: false, Statistics: chaptarr.BookStatistics{BookFileCount: 0}},
	}
	digest := reduceLibrary(books)
	if len(digest.Titles) != 1 {
		t.Fatalf("titles = %d, want one grouped title", len(digest.Titles))
	}
	if !digest.Titles[0].Ebook.Monitored || !digest.Titles[0].Ebook.Downloaded {
		t.Fatalf("ebook = %+v, want OR-reduced monitored and downloaded truth", digest.Titles[0].Ebook)
	}
}

// TestReduceLibraryExtractsSeriesFirstStatingRecordWins asserts the digest
// splits a record's seriesTitle into name+position via parseSeriesTitle, and
// that an audiobook record stating no series does not clear what the ebook
// record already established — the two records are the same work, and one of
// them simply not repeating the series is not a claim that there is none.
func TestReduceLibraryExtractsSeriesFirstStatingRecordWins(t *testing.T) {
	books := []chaptarr.Book{
		{
			ID:            1,
			Title:         "Lady of Shadows",
			ForeignBookID: "fb-lady",
			MediaType:     "ebook",
			SeriesTitle:   "Lady of Darkness #2",
		},
		{
			ID:            2,
			Title:         "Lady of Shadows",
			ForeignBookID: "fb-lady",
			MediaType:     "audiobook",
			SeriesTitle:   "",
		},
	}

	digest := reduceLibrary(books)
	lt := findTitle(t, digest, "Lady of Shadows")
	if lt.Series != "Lady of Darkness" || lt.SeriesPosition != "2" {
		t.Fatalf("series = %+v, want Lady of Darkness / 2", lt)
	}
}

// TestReduceLibraryNoSeriesLeavesFieldsEmpty asserts a book with no
// SeriesTitle leaves both digest fields empty rather than inventing a value.
func TestReduceLibraryNoSeriesLeavesFieldsEmpty(t *testing.T) {
	books := []chaptarr.Book{
		{ID: 40, Title: "Standalone", ForeignBookID: "fb-standalone", MediaType: "ebook"},
	}

	digest := reduceLibrary(books)
	lt := findTitle(t, digest, "Standalone")
	if lt.Series != "" || lt.SeriesPosition != "" {
		t.Fatalf("series = %+v, want both empty", lt)
	}
}

// TestStampAuthorsFromLibraryFillsBlankAuthor asserts the join fills a title
// whose reduction left Author blank, using the book's authorId to find the
// matching author record.
func TestStampAuthorsFromLibraryFillsBlankAuthor(t *testing.T) {
	books := []chaptarr.Book{
		{ID: 1, Title: "Lady of Shadows", ForeignBookID: "fb-lady", MediaType: "ebook", AuthorID: 7},
	}
	titles := reduceLibrary(books).Titles
	authors := []chaptarr.Author{{ID: 7, AuthorName: "Melissa K. Roehrich"}}

	stampAuthorsFromLibrary(titles, books, authors)

	lt := titles[0]
	if lt.Author != "Melissa K. Roehrich" {
		t.Fatalf("author = %q, want Melissa K. Roehrich", lt.Author)
	}
}

// TestStampAuthorsFromLibraryNeverOverwritesStatedName asserts a title whose
// reduction already filled Author (the record embedded an author object)
// keeps that name — the join never overwrites a name the record itself
// stated.
func TestStampAuthorsFromLibraryNeverOverwritesStatedName(t *testing.T) {
	books := []chaptarr.Book{
		{
			ID: 1, Title: "Heir to the Empire", ForeignBookID: "fb-1", MediaType: "ebook",
			AuthorID: 9, Author: &chaptarr.AuthorContext{AuthorName: "Timothy Zahn"},
		},
	}
	titles := reduceLibrary(books).Titles
	authors := []chaptarr.Author{{ID: 9, AuthorName: "Wrong Name"}}

	stampAuthorsFromLibrary(titles, books, authors)

	if titles[0].Author != "Timothy Zahn" {
		t.Fatalf("author = %q, want the record's own stated name unchanged", titles[0].Author)
	}
}

// TestStampAuthorsFromLibraryUnmatchedAuthorIDStaysBlank asserts a title
// whose authorId matches no author record stays blank rather than guessing.
func TestStampAuthorsFromLibraryUnmatchedAuthorIDStaysBlank(t *testing.T) {
	books := []chaptarr.Book{
		{ID: 1, Title: "Orphaned", ForeignBookID: "fb-orphan", MediaType: "ebook", AuthorID: 99},
	}
	titles := reduceLibrary(books).Titles
	authors := []chaptarr.Author{{ID: 1, AuthorName: "Someone Else"}}

	stampAuthorsFromLibrary(titles, books, authors)

	if titles[0].Author != "" {
		t.Fatalf("author = %q, want empty (authorId 99 matches no author record)", titles[0].Author)
	}
}

// TestStampAuthorsFromLibraryFillsForeignAuthorID asserts the join stamps the
// library's own author identity alongside the name. That id is what
// /detail/author/{id} resolves, so without it the author line cannot be tapped.
func TestStampAuthorsFromLibraryFillsForeignAuthorID(t *testing.T) {
	books := []chaptarr.Book{
		{ID: 1, Title: "Lady of Shadows", ForeignBookID: "fb-lady", MediaType: "ebook", AuthorID: 7},
	}
	titles := reduceLibrary(books).Titles
	authors := []chaptarr.Author{
		{ID: 7, AuthorName: "Melissa K. Roehrich", ForeignAuthorID: "hc:auth-42"},
	}

	stampAuthorsFromLibrary(titles, books, authors)

	if got := titles[0].AuthorForeignID; got != "hc:auth-42" {
		t.Fatalf("AuthorForeignID = %q, want hc:auth-42", got)
	}
}

// TestStampAuthorsFromLibraryStampsForeignIDWhenNameAlreadyStated pins the
// independence of the two stamps. A record that embedded its own author object
// arrives with Author already filled but never with the library's author
// identity, so an early return on a non-empty name would leave exactly those
// rows unlinkable — the name renders, the tap target silently does not.
func TestStampAuthorsFromLibraryStampsForeignIDWhenNameAlreadyStated(t *testing.T) {
	books := []chaptarr.Book{
		{
			ID: 1, Title: "Heir to the Empire", ForeignBookID: "fb-1", MediaType: "ebook",
			AuthorID: 9, Author: &chaptarr.AuthorContext{AuthorName: "Timothy Zahn"},
		},
	}
	titles := reduceLibrary(books).Titles
	authors := []chaptarr.Author{
		{ID: 9, AuthorName: "Wrong Name", ForeignAuthorID: "gr:auth-9"},
	}

	stampAuthorsFromLibrary(titles, books, authors)

	if titles[0].Author != "Timothy Zahn" {
		t.Fatalf("author = %q, want the record's own stated name unchanged", titles[0].Author)
	}
	if got := titles[0].AuthorForeignID; got != "gr:auth-9" {
		t.Fatalf("AuthorForeignID = %q, want gr:auth-9 stamped despite the stated name", got)
	}
}

// TestStampAuthorsFromLibraryUnmatchedAuthorIDLeavesForeignIDBlank asserts an
// unresolvable author yields no id rather than a guess — an author line with a
// wrong id would navigate to someone else's page.
func TestStampAuthorsFromLibraryUnmatchedAuthorIDLeavesForeignIDBlank(t *testing.T) {
	books := []chaptarr.Book{
		{ID: 1, Title: "Orphaned", ForeignBookID: "fb-orphan", MediaType: "ebook", AuthorID: 99},
	}
	titles := reduceLibrary(books).Titles
	authors := []chaptarr.Author{{ID: 1, AuthorName: "Someone Else", ForeignAuthorID: "hc:auth-1"}}

	stampAuthorsFromLibrary(titles, books, authors)

	if got := titles[0].AuthorForeignID; got != "" {
		t.Fatalf("AuthorForeignID = %q, want empty (authorId 99 matches no author record)", got)
	}
}

func TestSelectBookRootFailsClosedOnAmbiguity(t *testing.T) {
	if _, ok := selectBookRoot([]chaptarr.RootFolder{
		{Path: "/one/books", Accessible: true},
		{Path: "/two/books", Accessible: true},
	}, BookFormatEbook); ok {
		t.Fatal("multiple generic roots were guessed instead of rejected")
	}
	root, ok := selectBookRoot([]chaptarr.RootFolder{
		{Path: "/books", Accessible: true},
		{Path: "/audiobooks", Accessible: true},
	}, BookFormatEbook)
	if !ok || root.Path != "/books" {
		t.Fatalf("ebook generic root = %+v ok=%v, want sole compatible /books", root, ok)
	}
	if _, ok := selectBookRoot([]chaptarr.RootFolder{
		{Path: "/audio-one", Accessible: true},
		{Path: "/audio-two", Accessible: true},
	}, BookFormatAudiobook); ok {
		t.Fatal("multiple audiobook roots were guessed instead of rejected")
	}
}

func TestReduceLibraryPreservesMixedKnownAndUnknownCanonicalRows(t *testing.T) {
	digest := reduceLibrary([]chaptarr.Book{
		{ID: 1, Title: "Known", ForeignBookID: "known", MediaType: "ebook", Monitored: true},
		{ID: 2, Title: "Unknown", ForeignBookID: "unknown", MediaType: "paperback", Monitored: true},
	})
	if len(digest.Titles) != 2 {
		t.Fatalf("titles = %+v, want both known and unresolved canonical rows", digest.Titles)
	}
	known := findTitle(t, digest, "Known")
	unknown := findTitle(t, digest, "Unknown")
	if !known.StatusKnown || known.ForeignBookID != "known" {
		t.Fatalf("known row = %+v", known)
	}
	if unknown.StatusKnown || unknown.ForeignBookID != "unknown" {
		t.Fatalf("unknown row = %+v, want canonical ID with status_known=false", unknown)
	}
}

// TestReduceLibraryCoverIsClientReachable pins the digest to the same rule as
// the recent-books feed: the cover is the relative /MediaCover path or the
// metadata CDN remoteUrl — an arr-origin absolute URL (internal hostname,
// possibly credential-bearing) is never handed to a non-admin client.
func TestReduceLibraryCoverIsClientReachable(t *testing.T) {
	books := []chaptarr.Book{
		{
			ID: 1, Title: "Relative", ForeignBookID: "fb-r", MediaType: "ebook",
			Images: []chaptarr.Image{{CoverType: "cover", URL: "/MediaCover/Books/1/cover.jpg"}},
		},
		{
			ID: 2, Title: "ArrOrigin", ForeignBookID: "fb-a", MediaType: "ebook",
			Images: []chaptarr.Image{{
				CoverType: "cover",
				URL:       "http://library-user:library-pass@chaptarr.internal:8787/MediaCover/2/cover.jpg?apikey=library-key",
				RemoteURL: "https://cdn.invalid/2/cover.jpg",
			}},
		},
		{
			ID: 3, Title: "ArrOnly", ForeignBookID: "fb-o", MediaType: "ebook",
			Images: []chaptarr.Image{{CoverType: "cover", URL: "http://chaptarr.internal:8787/MediaCover/3/cover.jpg"}},
		},
	}

	digest := reduceLibrary(books)
	if got := findTitle(t, digest, "Relative").Cover; got != "/MediaCover/Books/1/cover.jpg" {
		t.Errorf("relative cover = %q, want the proxied /MediaCover path", got)
	}
	if got := findTitle(t, digest, "ArrOrigin").Cover; got != "https://cdn.invalid/2/cover.jpg" {
		t.Errorf("arr-origin absolute cover = %q, want the CDN remoteUrl fallback", got)
	}
	if got := findTitle(t, digest, "ArrOnly").Cover; got != "" {
		t.Errorf("arr-origin-only cover = %q, want empty so the app draws its placeholder", got)
	}
}
