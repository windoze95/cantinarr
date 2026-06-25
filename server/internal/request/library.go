package request

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

// bookLibraryCacheTTL bounds how long a user's reduced Chaptarr library digest
// is served from cache before a fresh GetAllBooks. Short enough that a just-added
// book shows as owned soon, long enough to spare Chaptarr a full library fetch on
// every search keystroke.
const bookLibraryCacheTTL = 120 * time.Second

// FormatOwnership is one format's (ebook or audiobook) ownership state for a
// title: whether Chaptarr is monitoring that format and whether a file is on
// disk for it.
type FormatOwnership struct {
	Monitored  bool `json:"monitored"`
	Downloaded bool `json:"downloaded"`
}

// LibraryTitle is one title in the owned-books digest, reduced from the (up to
// two) Chaptarr records that share a foreignBookId. Both Ebook and Audiobook are
// always present; a format with no record is the zero {false,false}.
type LibraryTitle struct {
	Title  string `json:"title"`
	Author string `json:"author"`
	Year   int    `json:"year"`
	// ForeignBookID lets the app request the missing format of an owned book: the
	// request carries it back and the backend completes the existing record.
	ForeignBookID string `json:"foreign_book_id"`
	// Cover is the owned record's relative cover path (e.g. /MediaCover/...),
	// which loads with the API key — so an owned search result can show real art
	// without the login-session-gated /MediaCoverProxy lookup cover.
	Cover     string          `json:"cover"`
	Ebook     FormatOwnership `json:"ebook"`
	Audiobook FormatOwnership `json:"audiobook"`
}

// BookLibraryDigest is the lean per-title ownership digest the app uses to mark
// search results as already-owned. Titles is always a non-nil slice.
type BookLibraryDigest struct {
	Titles []LibraryTitle `json:"titles"`
}

// reduceLibrary collapses a flat Chaptarr library into one entry per title.
// Chaptarr stores a title's ebook and audiobook as separate records sharing a
// foreignBookId, so records are grouped by groupKey (foreignBookId, else the
// record id) — mirroring the Dart ChaptarrBook.groupKey — and each record is
// routed to the ebook or audiobook slot by its format. A record's format is its
// book-level mediaType when set, else the format of its lone edition (else
// unknown), matching the Dart ChaptarrBook.format fallback. Downloaded is keyed
// off Statistics.BookFileCount (the hasFiles field is unreliable here).
func reduceLibrary(books []chaptarr.Book) BookLibraryDigest {
	type group struct {
		title    *LibraryTitle
		haveMeta bool // title/author/year already filled from a record with an author
	}
	groups := make(map[string]*group)
	order := make([]string, 0, len(books))

	for i := range books {
		book := books[i]
		key := groupKey(book)
		g, ok := groups[key]
		if !ok {
			g = &group{title: &LibraryTitle{}}
			groups[key] = g
			order = append(order, key)
		}

		// Fill the title/author/year from the first record in the group that
		// carries an author name, mirroring the Dart "first record with metadata"
		// behavior.
		if !g.haveMeta && book.Author != nil && book.Author.AuthorName != "" {
			g.title.Title = book.Title
			g.title.Author = book.Author.AuthorName
			if book.ReleaseDate != nil {
				g.title.Year = book.ReleaseDate.Year()
			}
			g.haveMeta = true
		}
		// Always keep a title even if no record had an author, so the digest never
		// emits a blank title for a group that has records.
		if g.title.Title == "" {
			g.title.Title = book.Title
		}
		// Take the cover from the first record in the group that has one.
		if g.title.Cover == "" {
			g.title.Cover = coverOf(book)
		}
		if g.title.ForeignBookID == "" {
			g.title.ForeignBookID = book.ForeignBookID
		}

		own := FormatOwnership{
			Monitored:  book.Monitored,
			Downloaded: book.Statistics.BookFileCount > 0,
		}
		switch recordFormat(book) {
		case chaptarr.FormatEbook:
			g.title.Ebook = own
		case chaptarr.FormatAudiobook:
			g.title.Audiobook = own
		}
	}

	titles := make([]LibraryTitle, 0, len(order))
	for _, key := range order {
		titles = append(titles, *groups[key].title)
	}
	return BookLibraryDigest{Titles: titles}
}

// groupKey keys the records of one title: its foreignBookId when present, else a
// per-record id key so records without a foreignBookId never merge. Mirrors the
// Dart ChaptarrBook.groupKey.
func groupKey(book chaptarr.Book) string {
	if book.ForeignBookID != "" {
		return book.ForeignBookID
	}
	return fmt.Sprintf("id:%d", book.ID)
}

// coverOf returns a book's cover image path, preferring the "cover" type, else
// the first image with a URL.
func coverOf(book chaptarr.Book) string {
	for _, img := range book.Images {
		if img.URL != "" && img.CoverType == "cover" {
			return img.URL
		}
	}
	for _, img := range book.Images {
		if img.URL != "" {
			return img.URL
		}
	}
	return ""
}

// recordFormat resolves the single format a Chaptarr book record represents: its
// book-level mediaType when "ebook"/"audiobook", else the format of its lone
// edition via chaptarr.FormatOf (a book with anything other than exactly one
// edition is "unknown"). Mirrors the Dart ChaptarrBook.format fallback.
func recordFormat(book chaptarr.Book) string {
	switch book.MediaType {
	case chaptarr.FormatEbook:
		return chaptarr.FormatEbook
	case chaptarr.FormatAudiobook:
		return chaptarr.FormatAudiobook
	}
	if len(book.Editions) == 1 {
		return chaptarr.FormatOf(book.Editions[0].Format)
	}
	return chaptarr.FormatUnknown
}

// recordsByForeignID indexes a foreignBookId's library records by format
// ("ebook"/"audiobook") and returns the title. Used to complete the missing
// format of an owned book — the request carries a library foreignBookId the
// metadata lookup can't match, so we act on the existing records instead.
func recordsByForeignID(books []chaptarr.Book, foreignID string) (string, map[string]*chaptarr.Book) {
	byFormat := make(map[string]*chaptarr.Book)
	title := ""
	for i := range books {
		if books[i].ForeignBookID != foreignID || foreignID == "" {
			continue
		}
		if title == "" {
			title = books[i].Title
		}
		switch recordFormat(books[i]) {
		case chaptarr.FormatEbook:
			byFormat[chaptarr.FormatEbook] = &books[i]
		case chaptarr.FormatAudiobook:
			byFormat[chaptarr.FormatAudiobook] = &books[i]
		}
	}
	return title, byFormat
}

// GetBookLibraryDigest returns the requesting user's reduced, cached Chaptarr
// library digest. A user with no Chaptarr access gets an empty (non-nil) digest
// rather than an error, so the app can degrade gracefully to "nothing owned".
// The digest is cached per resolved Chaptarr instance for bookLibraryCacheTTL.
func (s *Service) GetBookLibraryDigest(userID int64) (*BookLibraryDigest, error) {
	client, instanceID := s.getChaptarrWithID(userID)
	if client == nil {
		return &BookLibraryDigest{Titles: []LibraryTitle{}}, nil
	}

	cacheKey := "book-library:" + instanceID
	if s.libraryCache != nil {
		if data, ok := s.libraryCache.Get(cacheKey); ok {
			var digest BookLibraryDigest
			if err := json.Unmarshal(data, &digest); err == nil {
				if digest.Titles == nil {
					digest.Titles = []LibraryTitle{}
				}
				return &digest, nil
			}
		}
	}

	books, err := client.GetAllBooks()
	if err != nil {
		return nil, err
	}
	digest := reduceLibrary(books)

	if s.libraryCache != nil {
		if data, err := json.Marshal(digest); err == nil {
			s.libraryCache.Set(cacheKey, data, bookLibraryCacheTTL)
		}
	}
	return &digest, nil
}

// getChaptarrWithID resolves the same Chaptarr client as getChaptarr but also
// returns the instance id it resolved to, for cache keying. The id mirrors
// getChaptarr's resolution: the user's granted instance, else (for admins) the
// default Chaptarr instance.
func (s *Service) getChaptarrWithID(userID int64) (*chaptarr.Client, string) {
	if s.registry == nil {
		return nil, ""
	}
	if client, id, err := s.registry.GetUserChaptarrClient(userID); err == nil && client != nil {
		return client, id
	}
	if s.userIsAdmin(userID) {
		if client, id, err := s.registry.GetDefaultChaptarrClient(); err == nil && client != nil {
			return client, id
		}
	}
	return nil, ""
}
