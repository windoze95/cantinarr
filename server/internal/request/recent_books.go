package request

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

// bookRecentCacheTTL is longer than the other book digests because building it
// fans out across authors. The Chaptarr webhook drops this key on import, so a
// book that lands still surfaces immediately rather than waiting out the TTL.
const bookRecentCacheTTL = 60 * time.Second

// recentBooksDefaultLimit is what the Books row asks for when the caller does
// not say.
const recentBooksDefaultLimit = 20

// recentBooksMaxItems caps what is cached and therefore what any caller can
// ask for. This row answers "what landed lately"; a longer list is the library.
const recentBooksMaxItems = 50

// recentBooksFanOut bounds concurrent per-author file reads against one arr.
const recentBooksFanOut = 4

// RecentBook is one library record that recently gained a file.
type RecentBook struct {
	BookID        int       `json:"book_id"`
	ForeignBookID string    `json:"foreign_book_id"`
	Title         string    `json:"title"`
	Format        string    `json:"format"`
	Cover         string    `json:"cover"`
	ImportedAt    time.Time `json:"imported_at"`
}

// BookRecentDigest is the newest-first list of book records that gained a file.
type BookRecentDigest struct {
	Items []RecentBook `json:"items"`
}

// GetRecentBooksForInstance returns the newest book-file imports for the
// Chaptarr instance this user may see, newest first.
//
// A user with no Chaptarr grant gets an empty list rather than an error: the
// books row is simply absent for them, which is not a failure.
func (s *Service) GetRecentBooksForInstance(userID int64, requestedInstanceID string, limit int) (*BookRecentDigest, error) {
	client, instanceID, err := s.resolveChaptarr(userID, requestedInstanceID)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return &BookRecentDigest{Items: []RecentBook{}}, nil
	}
	if limit <= 0 || limit > recentBooksMaxItems {
		limit = recentBooksMaxItems
	}

	cacheKey := "book-recent:" + instanceID
	if s.libraryCache != nil {
		if data, ok := s.libraryCache.Get(cacheKey); ok {
			var digest BookRecentDigest
			if err := json.Unmarshal(data, &digest); err == nil {
				return &BookRecentDigest{Items: takeRecentBooks(digest.Items, limit)}, nil
			}
		}
	}

	books, err := client.GetAllBooks()
	if err != nil {
		return nil, err
	}
	filesByBook, err := recentBookFiles(client, books)
	if err != nil {
		// Fail closed. A partial list would silently omit the very import the
		// user opened the tab to find, which is worse than showing no row.
		return nil, err
	}

	items := buildRecentBooks(books, filesByBook, recentBooksMaxItems)
	if s.libraryCache != nil {
		if data, err := json.Marshal(BookRecentDigest{Items: items}); err == nil {
			s.libraryCache.Set(cacheKey, data, bookRecentCacheTTL)
		}
	}
	return &BookRecentDigest{Items: takeRecentBooks(items, limit)}, nil
}

func takeRecentBooks(items []RecentBook, limit int) []RecentBook {
	if items == nil {
		return []RecentBook{}
	}
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

// recentBookFiles reads the file records for every author owning something on
// disk. Chaptarr has no library-wide bookfile read — its API requires an author,
// book, or id filter — so this is a bounded fan-out, which is exactly why it
// lives on the server rather than in the app.
func recentBookFiles(client *chaptarr.Client, books []chaptarr.Book) (map[int][]chaptarr.BookFile, error) {
	authorIDs := make([]int, 0, 8)
	seen := make(map[int]struct{})
	for _, book := range books {
		if book.Statistics.BookFileCount == 0 || book.AuthorID <= 0 {
			continue
		}
		if _, dup := seen[book.AuthorID]; dup {
			continue
		}
		seen[book.AuthorID] = struct{}{}
		authorIDs = append(authorIDs, book.AuthorID)
	}

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
		byBook   = make(map[int][]chaptarr.BookFile)
	)
	sem := make(chan struct{}, recentBooksFanOut)
	for _, authorID := range authorIDs {
		wg.Add(1)
		go func(authorID int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			files, err := client.GetBookFiles(authorID)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			// Keyed on the book record, so a fork that ignores the author
			// filter and returns everything is harmless.
			for _, f := range files {
				byBook[f.BookID] = append(byBook[f.BookID], f)
			}
		}(authorID)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return byBook, nil
}

// buildRecentBooks reduces the library to one card per title, ordered by when
// that title's files landed.
//
// Recency is the newest bookFile.dateAdded, never the book record's own added
// date: a book requested months ago and downloaded today belongs at the top.
//
// A title's ebook and audiobook are separate Chaptarr records sharing a
// foreignBookId, and they are merged into one card. They were deliberately kept
// apart when each card announced its own format ("eBook"/"Audiobook") — two
// arrivals, two cards, each saying something the other did not. The card now
// leads with the title's *ownership* instead ("eBook + Audiobook", plus an
// availability pill), which is a fact about the title rather than the record,
// so both cards render identical text and, since the detail route is keyed by
// foreignBookId, identical links. Nothing is hidden by merging them: the card
// carries the newest arrival's date, so a late-arriving audiobook still floats
// its title to the front of the row.
//
// Grouping is the same groupKey the ownership digest uses, so a record with no
// foreignBookId can never merge into another title — and two records that do
// share one are, by the library's own identity rule, the same book.
func buildRecentBooks(books []chaptarr.Book, filesByBook map[int][]chaptarr.BookFile, limit int) []RecentBook {
	type group struct {
		item    RecentBook
		formats map[string]struct{}
		// coverFrom is the arrival time of the record g.item.Cover came from,
		// so a later record can only replace it by being *older* — see the
		// cover selection below.
		coverFrom time.Time
		coverID   int
	}
	groups := make(map[string]*group, len(books))
	order := make([]string, 0, len(books))

	for _, book := range books {
		// One record is one arrival even when a multi-part audiobook has many
		// files; the record is the unit the library and detail page address.
		var newest time.Time
		for _, f := range filesByBook[book.ID] {
			if f.DateAdded == nil {
				continue
			}
			if f.DateAdded.After(newest) {
				newest = *f.DateAdded
			}
		}
		// No timestamp means no recency claim, whatever the statistics say.
		if newest.IsZero() {
			continue
		}

		key := groupKey(book)
		g, ok := groups[key]
		if !ok {
			g = &group{formats: make(map[string]struct{}, 2)}
			groups[key] = g
			order = append(order, key)
		}
		g.formats[recordFormat(book)] = struct{}{}

		// The card is dated and identified by the title's newest arrival, so a
		// format that lands today leads the row even when its sibling landed
		// months ago.
		if newest.After(g.item.ImportedAt) {
			g.item.ImportedAt = newest
			g.item.BookID = book.ID
		}
		if g.item.Title == "" {
			g.item.Title = book.Title
		}
		if g.item.ForeignBookID == "" {
			g.item.ForeignBookID = book.ForeignBookID
		}
		// Cover selection, deliberately not "first usable in library order".
		//
		// Chaptarr emits /MediaCover/Books/{id}/cover.jpg for a record whether
		// or not the art behind it has been downloaded, so a path being present
		// is not evidence it resolves — and nothing here can tell the two apart.
		// A record created minutes ago is the one most likely to have art that
		// does not exist yet.
		//
		// So prefer the art of the *established* record: the one whose files
		// landed earliest. When a second format merges into a title, the card
		// then keeps the cover it was already rendering instead of adopting the
		// new arrival's possibly-empty one and losing its art for good (the
		// arrival's own path never becomes correct on a later refresh, so this
		// was permanent). Ties break on the lower record id to stay stable
		// across fetches.
		if cover := clientReachableCover(book); cover != "" {
			better := g.item.Cover == "" ||
				newest.Before(g.coverFrom) ||
				(newest.Equal(g.coverFrom) && book.ID < g.coverID)
			if better {
				g.item.Cover = cover
				g.coverFrom = newest
				g.coverID = book.ID
			}
		}
	}

	items := make([]RecentBook, 0, len(order))
	for _, key := range order {
		g := groups[key]
		// A format label describes the whole card, so it survives only when
		// every record behind it agrees. A merged eBook+Audiobook card has no
		// single format, and claiming either one would be wrong — the card's
		// ownership line already names both.
		if len(g.formats) == 1 {
			for format := range g.formats {
				g.item.Format = format
			}
		}
		items = append(items, g.item)
	}

	sort.Slice(items, func(i, j int) bool {
		if !items[i].ImportedAt.Equal(items[j].ImportedAt) {
			return items[i].ImportedAt.After(items[j].ImportedAt)
		}
		// A freshly scanned library can stamp every file identically; without a
		// tie-break the row would reshuffle on every fetch.
		return items[i].BookID > items[j].BookID
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}

// clientReachableCover returns a cover the app can actually load.
func clientReachableCover(book chaptarr.Book) string {
	return clientReachableImage(book.Images, "cover")
}

// clientReachableImage picks one image from an arr record that a client is
// allowed to dereference.
//
// Clients must never dereference an arr-origin URL, so only the relative
// /MediaCover path (which the app resolves through the authenticated instance
// proxy) is passed through. An absolute arr-origin URL falls back to the
// metadata provider's CDN copy, and anything else yields "" so the app draws
// its placeholder instead of a broken image. The preferred cover type wins when
// the record carries it; otherwise the first usable image does.
func clientReachableImage(images []chaptarr.Image, preferred string) string {
	pick := func(match func(chaptarr.Image) bool) (chaptarr.Image, bool) {
		for _, img := range images {
			if img.URL != "" && match(img) {
				return img, true
			}
		}
		return chaptarr.Image{}, false
	}
	img, ok := pick(func(i chaptarr.Image) bool { return i.CoverType == preferred })
	if !ok {
		img, ok = pick(func(chaptarr.Image) bool { return true })
	}
	if !ok {
		return ""
	}
	if strings.HasPrefix(img.URL, "/") {
		return img.URL
	}
	if strings.HasPrefix(img.RemoteURL, "http") {
		return img.RemoteURL
	}
	return ""
}
