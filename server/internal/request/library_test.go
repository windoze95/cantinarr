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
