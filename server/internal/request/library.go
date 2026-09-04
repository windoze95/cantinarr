package request

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

// bookLibraryCacheTTL bounds how long a user's reduced Chaptarr library digest
// is served from cache before a fresh GetAllBooks. Short enough that a just-added
// book shows as owned soon, long enough to spare Chaptarr a full library fetch on
// every search keystroke.
const bookLibraryCacheTTL = 15 * time.Second

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
	// AuthorForeignID is the library's OWN author identity — the id
	// /detail/author/{id} is addressed by, and the same one the Books page's
	// authors row already navigates with. It is deliberately taken from the
	// library's author record rather than from a metadata lookup or a live
	// book record: those disagree across providers (a Hardcover-seeded
	// library row against Goodreads lookup results), and the library's own
	// answer is the only one its author page can resolve. Empty when the
	// library states no author for this title.
	AuthorForeignID string `json:"author_foreign_id"`
	Year            int    `json:"year"`
	// Series is the series name parsed by parseSeriesTitle (library_series.go)
	// from the record's raw seriesTitle string — the identity
	// /api/requests/book-series-detail is addressed by. Empty when the library
	// states no series for this title.
	Series string `json:"series"`
	// SeriesPosition is the raw position string passed through exactly as the
	// library states it ("2", "2A", "1.5, 1.6, 1.7"), never normalised. Note
	// SeriesTitle (library_series.go) keeps its own "position" key on the
	// book-series-detail row — that redundancy is deliberate; renaming or
	// moving SeriesTitle.Position would change an existing wire key and break
	// the series page.
	SeriesPosition string `json:"series_position"`
	// ForeignBookID lets the app request the missing format of an owned book: the
	// request carries it back and the backend completes the existing record.
	ForeignBookID string `json:"foreign_book_id"`
	// StatusKnown is false when Chaptarr returned an exact canonical record whose
	// format cannot be classified. The row and canonical ID remain in the digest
	// so clients can map search identity, but must not offer a request for it.
	StatusKnown bool `json:"status_known"`
	// Cover is a client-reachable cover reference: the owned record's relative
	// /MediaCover path (which the app resolves through the authenticated
	// instance proxy), or the metadata CDN copy when the record carries only an
	// absolute URL. An arr-origin absolute URL is never passed through —
	// clients must not dereference arr-origin URLs.
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
			g = &group{title: &LibraryTitle{StatusKnown: true}}
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
		// A record's release date is a fact about the record, not about whether
		// the payload happened to embed an author object. Chaptarr's per-author
		// book list (`/book?authorId=`) omits that object entirely — even with
		// includeAuthor=true — so gating the year on it drops every year on that
		// path, which in turn collapses any newest-first ordering into
		// "everything is undated". First date wins, matching how the block above
		// treats the first record carrying metadata.
		if g.title.Year == 0 && book.ReleaseDate != nil {
			g.title.Year = book.ReleaseDate.Year()
		}
		// Take the cover from the first record in the group that has one.
		if g.title.Cover == "" {
			g.title.Cover = clientReachableCover(book)
		}
		if g.title.ForeignBookID == "" {
			g.title.ForeignBookID = book.ForeignBookID
		}
		// First record in the group that states a series wins; a later record
		// simply not repeating it is not a claim that there is none — the two
		// records are the same work. Guarded on the parsed name, not the raw
		// string, so a record whose seriesTitle fails to parse to a name never
		// clears what an earlier record already established.
		if g.title.Series == "" {
			if name, position := parseSeriesTitle(book.SeriesTitle); name != "" {
				g.title.Series = name
				g.title.SeriesPosition = position
			}
		}

		own := FormatOwnership{
			Monitored:  book.Monitored,
			Downloaded: book.Statistics.BookFileCount > 0,
		}
		switch recordFormat(book) {
		case chaptarr.FormatEbook:
			g.title.Ebook.Monitored = g.title.Ebook.Monitored || own.Monitored
			g.title.Ebook.Downloaded = g.title.Ebook.Downloaded || own.Downloaded
		case chaptarr.FormatAudiobook:
			g.title.Audiobook.Monitored = g.title.Audiobook.Monitored || own.Monitored
			g.title.Audiobook.Downloaded = g.title.Audiobook.Downloaded || own.Downloaded
		default:
			g.title.StatusKnown = false
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

// recordFormat resolves the single format a Chaptarr book record represents.
// The classifier lives in the chaptarr package so every consumer (library
// digests, issue-report resolution) agrees on the same semantics.
func recordFormat(book chaptarr.Book) string { return chaptarr.RecordFormat(book) }

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

// recordsForForeignID preserves every same-format record so live truth can be
// aggregated deterministically and mutations can update all duplicates rather
// than whichever record happened to arrive last.
func recordsForForeignID(books []chaptarr.Book, foreignID string) (string, map[string][]chaptarr.Book, bool) {
	byFormat := make(map[string][]chaptarr.Book)
	title := ""
	unresolved := false
	for _, book := range books {
		if foreignID == "" || book.ForeignBookID != foreignID {
			continue
		}
		if title == "" {
			title = book.Title
		}
		switch format := recordFormat(book); format {
		case chaptarr.FormatEbook, chaptarr.FormatAudiobook:
			byFormat[format] = append(byFormat[format], book)
		default:
			unresolved = true
		}
	}
	return title, byFormat, unresolved
}

// selectBookRoot chooses one unambiguous accessible root. Current Chaptarr
// releases expose per-format effective-default flags; older releases are
// resolved conservatively from the root name/path, with one generic root kept
// as a compatible fallback. Multiple candidates fail closed.
func selectBookRoot(folders []chaptarr.RootFolder, format string) (chaptarr.RootFolder, bool) {
	accessible := make([]chaptarr.RootFolder, 0, len(folders))
	effectiveDefaults := make([]chaptarr.RootFolder, 0, len(folders))
	explicitMatches := make([]chaptarr.RootFolder, 0, len(folders))
	for _, folder := range folders {
		if !folder.IsAccessible() || strings.TrimSpace(folder.Path) == "" {
			continue
		}
		accessible = append(accessible, folder)
		if format == BookFormatAudiobook && folder.IsEffectiveDefaultAudiobook {
			effectiveDefaults = append(effectiveDefaults, folder)
		} else if format == BookFormatEbook && folder.IsEffectiveDefaultEbook {
			effectiveDefaults = append(effectiveDefaults, folder)
		}
		if format == BookFormatAudiobook && folder.Audiobook {
			explicitMatches = append(explicitMatches, folder)
		} else if format == BookFormatEbook && folder.Ebook {
			explicitMatches = append(explicitMatches, folder)
		}
	}
	if len(effectiveDefaults) == 1 {
		return effectiveDefaults[0], true
	}
	if len(effectiveDefaults) > 1 {
		return chaptarr.RootFolder{}, false
	}
	if len(explicitMatches) == 1 {
		return explicitMatches[0], true
	}
	if len(explicitMatches) > 1 {
		return chaptarr.RootFolder{}, false
	}

	inferred := make([]chaptarr.RootFolder, 0, len(accessible))
	generic := make([]chaptarr.RootFolder, 0, len(accessible))
	for _, folder := range accessible {
		label := strings.ToLower(strings.TrimSpace(folder.Name + " " + folder.Path))
		isAudio := strings.Contains(label, "audio") ||
			strings.Contains(label, "listen")
		isEbook := !isAudio && (strings.Contains(label, "ebook") ||
			strings.Contains(label, "e-book") ||
			strings.Contains(label, "e book"))
		if !isAudio && !isEbook {
			generic = append(generic, folder)
		}
		if (format == BookFormatAudiobook && isAudio) || (format == BookFormatEbook && isEbook) {
			inferred = append(inferred, folder)
		}
	}
	if len(inferred) == 1 {
		return inferred[0], true
	}
	if len(inferred) > 1 {
		return chaptarr.RootFolder{}, false
	}
	if len(generic) == 1 {
		return generic[0], true
	}
	if len(generic) > 1 {
		return chaptarr.RootFolder{}, false
	}
	if len(accessible) == 1 {
		return accessible[0], true
	}
	return chaptarr.RootFolder{}, false
}

// GetBookLibraryDigest returns the requesting user's reduced, cached Chaptarr
// library digest. A user with no Chaptarr access gets an empty (non-nil) digest
// rather than an error, so the app can degrade gracefully to "nothing owned".
// The digest is cached per resolved Chaptarr instance for bookLibraryCacheTTL.
func (s *Service) GetBookLibraryDigest(userID int64) (*BookLibraryDigest, error) {
	return s.GetBookLibraryDigestForInstance(userID, "")
}

// GetBookLibraryDigestForInstance returns the digest for an explicitly selected
// authorized Chaptarr instance, or the user's effective instance when omitted.
func (s *Service) GetBookLibraryDigestForInstance(userID int64, requestedInstanceID string) (*BookLibraryDigest, error) {
	client, instanceID, err := s.resolveChaptarr(userID, requestedInstanceID)
	if err != nil {
		return nil, err
	}
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

	// The full-library book list carries no embedded author object (Chaptarr
	// only nests it on per-author reads), so the name is joined from the
	// library's own author records. This is the opposite of
	// GetLibraryAuthorsForInstance, which fails closed on the same read: there,
	// a missing book list would yield a confident wrong *count* ("0 books" on
	// an author whose shelf is full). Here, a missing author list yields an
	// absent author *name* — no claim at all. Ownership truth is this digest's
	// job and is unaffected, so the error is not returned (AGENTS.md
	// absence-vs-blindness; D-04). Nothing derived from it reaches the
	// response, so an upstream host in an error string can never surface here.
	if authors, err := client.GetAllAuthors(); err == nil {
		stampAuthorsFromLibrary(digest.Titles, books, authors)
	}

	if s.libraryCache != nil {
		if data, err := json.Marshal(digest); err == nil {
			s.libraryCache.Set(cacheKey, data, bookLibraryCacheTTL)
		}
	}
	return &digest, nil
}

// stampAuthorsFromLibrary fills each reduced title's empty Author by joining
// the books it was reduced from to the library's author records via
// authorId. Per D-04, an author name a record already stated is never
// overwritten — mirrors stampAuthorName's guard.
//
// The join is keyed on ForeignBookID only: a record with no foreignBookId
// groups under the per-record "id:" key (see groupKey), which LibraryTitle
// does not carry, so that title is left with an empty author rather than
// being matched by slice position — an absent name is honest, a
// positionally guessed one is not.
func stampAuthorsFromLibrary(titles []LibraryTitle, books []chaptarr.Book, authors []chaptarr.Author) {
	authorIDByForeignBookID := make(map[string]int, len(books))
	for _, book := range books {
		id := strings.TrimSpace(book.ForeignBookID)
		if id == "" || book.AuthorID <= 0 {
			continue
		}
		if _, seen := authorIDByForeignBookID[id]; !seen {
			authorIDByForeignBookID[id] = book.AuthorID
		}
	}
	nameByAuthorID := make(map[int]string, len(authors))
	foreignIDByAuthorID := make(map[int]string, len(authors))
	for _, author := range authors {
		nameByAuthorID[author.ID] = strings.TrimSpace(author.AuthorName)
		foreignIDByAuthorID[author.ID] = strings.TrimSpace(author.ForeignAuthorID)
	}
	for i := range titles {
		// The name and the foreign id are stamped independently: a title that
		// already carries an embedded author NAME still needs the id before its
		// author line can be tapped, so an early `continue` on a non-empty name
		// would silently leave those rows unlinkable.
		haveName := strings.TrimSpace(titles[i].Author) != ""
		haveForeignID := strings.TrimSpace(titles[i].AuthorForeignID) != ""
		if haveName && haveForeignID {
			continue
		}
		id := strings.TrimSpace(titles[i].ForeignBookID)
		if id == "" {
			continue
		}
		authorID, ok := authorIDByForeignBookID[id]
		if !ok {
			continue
		}
		if !haveName {
			if name := nameByAuthorID[authorID]; name != "" {
				titles[i].Author = name
			}
		}
		if !haveForeignID {
			if foreignID := foreignIDByAuthorID[authorID]; foreignID != "" {
				titles[i].AuthorForeignID = foreignID
			}
		}
	}
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
