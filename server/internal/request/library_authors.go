package request

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

// ErrBookAuthorNotFound means the requested foreignAuthorId does not name an
// author this Chaptarr library holds. It is a 404, not a server fault: the
// author page was opened for someone the library does not have.
var ErrBookAuthorNotFound = errors.New("author is not in this book library")

// bookAuthorsCacheTTL matches the recency digest rather than the 15s ownership
// digest. The authors row answers "whose books do we have", which changes only
// when an author is added or removed, and the Chaptarr webhook drops this key
// on any library change anyway.
const bookAuthorsCacheTTL = 60 * time.Second

// bookAuthorsMaxItems caps the browse row. This is a shelf to scan, not the
// library — a user looking for one specific author searches for them.
//
// The cap is applied *after* the requested sort, never before: capping first
// would make "by name" mean "the most-collected authors, alphabetised", which
// looks complete and silently omits everyone below the cut.
const bookAuthorsMaxItems = 200

// The orders the browse row can be read in. An unknown value is treated as
// [AuthorSortBooks] rather than rejected — a newer client asking for a sort
// this server does not have should get a usable row, not an error.
const (
	// AuthorSortBooks leads with the authors the library actually holds.
	AuthorSortBooks = "books"
	// AuthorSortName is alphabetical by the name the card displays.
	AuthorSortName = "name"
	// AuthorSortAdded is newest arrival in the library first.
	AuthorSortAdded = "added"
)

// normalizeAuthorSort maps a requested sort onto one this server implements.
func normalizeAuthorSort(sort string) string {
	switch strings.ToLower(strings.TrimSpace(sort)) {
	case AuthorSortName:
		return AuthorSortName
	case AuthorSortAdded:
		return AuthorSortAdded
	default:
		return AuthorSortBooks
	}
}

// LibraryAuthor is one author the Chaptarr library holds books for.
//
// Counts are reduced the same way the ownership digest reduces titles: a title
// whose ebook and audiobook are two records sharing a foreignBookId counts
// once. Chaptarr's own author statistics count file records, which double-counts
// exactly the titles a requester is most likely to own in both formats.
type LibraryAuthor struct {
	// ForeignAuthorID is the metadata-provider id, the identity clients address
	// an author by. It is empty for a record Chaptarr has not keyed yet, which
	// leaves the author visible but not openable — the same treatment a book
	// with no foreignBookId gets.
	ForeignAuthorID string `json:"foreign_author_id"`
	Name            string `json:"name"`
	// Image is a client-reachable author image: the relative /MediaCover path
	// (resolved by the app through the authenticated instance proxy) or the
	// metadata CDN copy. An arr-origin absolute URL is never passed through.
	Image string `json:"image"`
	// TitleCount is how many distinct titles by this author the library tracks.
	TitleCount int `json:"title_count"`
	// AvailableCount is how many of those have a file on disk in any format.
	AvailableCount int `json:"available_count"`
	// Added is when the author entered the library, for the "date added" order.
	// Nil when the record carries no date, which sorts last rather than as the
	// beginning of time.
	Added *time.Time `json:"added,omitempty"`
}

// BookAuthorsDigest is the authors browse row's payload. Authors is always a
// non-nil slice.
type BookAuthorsDigest struct {
	Authors []LibraryAuthor `json:"authors"`
	// Total is how many authors the library holds, before the row's cap. A
	// client that shows fewer than this is showing a truncated row and needs to
	// be able to say so — a shelf that just stops reads as the whole library.
	Total int `json:"total"`
}

// BookAuthorDetail is one author plus every title of theirs the library tracks,
// carrying the same per-format ownership the search digest does so the app can
// render the same pills and offer the same requests.
type BookAuthorDetail struct {
	Author LibraryAuthor  `json:"author"`
	Titles []LibraryTitle `json:"titles"`
}

// GetLibraryAuthorsForInstance returns the authors of the Chaptarr instance
// this user may see, most-collected first.
//
// A user with no Chaptarr grant gets an empty list rather than an error: the
// authors row is simply absent for them, which is not a failure.
func (s *Service) GetLibraryAuthorsForInstance(userID int64, requestedInstanceID, sort string) (*BookAuthorsDigest, error) {
	client, instanceID, err := s.resolveChaptarr(userID, requestedInstanceID)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return &BookAuthorsDigest{Authors: []LibraryAuthor{}}, nil
	}

	order := normalizeAuthorSort(sort)

	// The cache holds every author in no particular order: building the list is
	// what costs two library reads, while ordering it is free. One entry
	// therefore serves all three orders, and switching order in the app never
	// refetches the library.
	cacheKey := "book-authors:" + instanceID
	if s.libraryCache != nil {
		if data, ok := s.libraryCache.Get(cacheKey); ok {
			var digest BookAuthorsDigest
			if err := json.Unmarshal(data, &digest); err == nil {
				return &BookAuthorsDigest{
					Authors: sortLibraryAuthors(digest.Authors, order, bookAuthorsMaxItems),
					Total:   len(digest.Authors),
				}, nil
			}
		}
	}

	authors, err := client.GetAllAuthors()
	if err != nil {
		return nil, err
	}
	books, err := client.GetAllBooks()
	if err != nil {
		// Fail closed rather than shipping a row of authors with no counts:
		// "0 books" on an author whose shelf is full is a wrong answer, and a
		// row that says nothing is better than one that says something false.
		return nil, err
	}

	all := buildLibraryAuthors(authors, books)
	if s.libraryCache != nil {
		if data, err := json.Marshal(BookAuthorsDigest{Authors: all}); err == nil {
			s.libraryCache.Set(cacheKey, data, bookAuthorsCacheTTL)
		}
	}
	return &BookAuthorsDigest{
		Authors: sortLibraryAuthors(all, order, bookAuthorsMaxItems),
		Total:   len(all),
	}, nil
}

// GetLibraryAuthorDetailForInstance returns one author and their library titles.
//
// Unlike the browse row this is deliberately uncached: it is opened to decide
// what to request, so it must reflect a book requested seconds ago. The per-
// author cache key could not be dropped by the webhook invalidation anyway,
// which only knows the instance.
func (s *Service) GetLibraryAuthorDetailForInstance(userID int64, foreignAuthorID, requestedInstanceID string) (*BookAuthorDetail, error) {
	client, _, err := s.resolveChaptarr(userID, requestedInstanceID)
	if err != nil {
		return nil, err
	}
	// No Chaptarr access is not "author missing" — the caller asked about a
	// library they cannot see at all, and saying "not found" would claim this
	// library was searched.
	if client == nil {
		return nil, ErrChaptarrInstanceForbidden
	}
	wanted := strings.TrimSpace(foreignAuthorID)
	if wanted == "" {
		return nil, ErrBookAuthorNotFound
	}

	authors, err := client.GetAllAuthors()
	if err != nil {
		return nil, err
	}
	var match *chaptarr.Author
	for i := range authors {
		if strings.TrimSpace(authors[i].ForeignAuthorID) == wanted {
			match = &authors[i]
			break
		}
	}
	if match == nil {
		return nil, ErrBookAuthorNotFound
	}

	books, err := client.GetBooks(match.ID)
	if err != nil {
		return nil, err
	}
	titles := reduceLibrary(books).Titles
	// The reduction fills the author name from each record's embedded author
	// object, which this list does not carry. We looked this author up by id,
	// so stamp what we already know rather than emitting blank authors.
	stampAuthorName(titles, match.AuthorName)
	sortAuthorTitles(titles)

	author := libraryAuthorFrom(*match)
	author.TitleCount, author.AvailableCount = countAuthorTitles(books)
	return &BookAuthorDetail{Author: author, Titles: titles}, nil
}

// buildLibraryAuthors joins the author list to the library's books so every
// count comes from the same records the ownership digest reduces.
//
// An author with no book records is dropped: the row exists to be browsed into,
// and an author with nothing behind them opens onto an empty page. That happens
// for real — a failed or still-pending metadata import leaves the author record
// behind — so it is a state to omit, not one to render as an empty shelf.
func buildLibraryAuthors(authors []chaptarr.Author, books []chaptarr.Book) []LibraryAuthor {
	byAuthor := make(map[int][]chaptarr.Book, len(authors))
	for _, book := range books {
		if book.AuthorID <= 0 {
			continue
		}
		byAuthor[book.AuthorID] = append(byAuthor[book.AuthorID], book)
	}

	items := make([]LibraryAuthor, 0, len(authors))
	for _, a := range authors {
		owned := byAuthor[a.ID]
		if len(owned) == 0 {
			continue
		}
		entry := libraryAuthorFrom(a)
		if entry.Name == "" {
			continue
		}
		entry.TitleCount, entry.AvailableCount = countAuthorTitles(owned)
		items = append(items, entry)
	}

	return items
}

// sortLibraryAuthors orders the row and then caps it. Every order ends in the
// same name tie-break so an unchanged library never reshuffles between fetches.
func sortLibraryAuthors(items []LibraryAuthor, order string, limit int) []LibraryAuthor {
	if items == nil {
		return []LibraryAuthor{}
	}
	sorted := make([]LibraryAuthor, len(items))
	copy(sorted, items)

	byName := func(i, j int) bool {
		return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name)
	}
	switch normalizeAuthorSort(order) {
	case AuthorSortName:
		sort.Slice(sorted, byName)
	case AuthorSortAdded:
		sort.Slice(sorted, func(i, j int) bool {
			a, b := sorted[i].Added, sorted[j].Added
			// A record with no date makes no recency claim, so it trails the
			// ones that do rather than leading as the beginning of time.
			if (a == nil) != (b == nil) {
				return b == nil
			}
			if a != nil && !a.Equal(*b) {
				return a.After(*b)
			}
			return byName(i, j)
		})
	default:
		sort.Slice(sorted, func(i, j int) bool {
			if sorted[i].AvailableCount != sorted[j].AvailableCount {
				return sorted[i].AvailableCount > sorted[j].AvailableCount
			}
			if sorted[i].TitleCount != sorted[j].TitleCount {
				return sorted[i].TitleCount > sorted[j].TitleCount
			}
			return byName(i, j)
		})
	}

	if limit > 0 && len(sorted) > limit {
		sorted = sorted[:limit]
	}
	return sorted
}

func libraryAuthorFrom(a chaptarr.Author) LibraryAuthor {
	return LibraryAuthor{
		ForeignAuthorID: strings.TrimSpace(a.ForeignAuthorID),
		Name:            strings.TrimSpace(a.AuthorName),
		Image:           clientReachableAuthorImage(a),
		Added:           a.Added,
	}
}

// countAuthorTitles reduces an author's records to distinct titles and how many
// of them have a file, using the same grouping as the ownership digest so the
// two never disagree about what "a book" is.
func countAuthorTitles(books []chaptarr.Book) (titles, available int) {
	hasFile := make(map[string]bool, len(books))
	for _, book := range books {
		key := groupKey(book)
		if _, seen := hasFile[key]; !seen {
			hasFile[key] = false
		}
		if book.Statistics.BookFileCount > 0 {
			hasFile[key] = true
		}
	}
	for _, downloaded := range hasFile {
		titles++
		if downloaded {
			available++
		}
	}
	return titles, available
}

// stampAuthorName fills in the author on titles the reduction left blank.
func stampAuthorName(titles []LibraryTitle, name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	for i := range titles {
		if strings.TrimSpace(titles[i].Author) == "" {
			titles[i].Author = name
		}
	}
}

// sortAuthorTitles orders a bibliography newest-first. Undated records sort
// last rather than leading the page as year zero.
func sortAuthorTitles(titles []LibraryTitle) {
	sort.SliceStable(titles, func(i, j int) bool {
		a, b := titles[i], titles[j]
		if (a.Year > 0) != (b.Year > 0) {
			return a.Year > 0
		}
		if a.Year != b.Year {
			return a.Year > b.Year
		}
		return strings.ToLower(a.Title) < strings.ToLower(b.Title)
	})
}

// clientReachableAuthorImage returns an author image the app can load, under
// the same rule as book covers: a relative /MediaCover path or the metadata
// CDN copy, never an arr-origin absolute URL.
func clientReachableAuthorImage(a chaptarr.Author) string {
	return clientReachableImage(a.Images, "poster")
}
