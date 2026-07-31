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

// buildRecentBooks orders library records by when their files landed.
//
// Recency is the newest file's dateAdded, never the book record's own added
// date: a book requested months ago and downloaded today belongs at the top.
// Ebook and audiobook are separate records sharing a foreignBookId and stay
// separate here — they are two distinct arrivals at two distinct times, and
// merging them would hide the second one entirely.
func buildRecentBooks(books []chaptarr.Book, filesByBook map[int][]chaptarr.BookFile, limit int) []RecentBook {
	items := make([]RecentBook, 0, len(books))
	for _, book := range books {
		// One record is one card even when a multi-part audiobook has many
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
		items = append(items, RecentBook{
			BookID:        book.ID,
			ForeignBookID: book.ForeignBookID,
			Title:         book.Title,
			Format:        recordFormat(book),
			Cover:         clientReachableCover(book),
			ImportedAt:    newest,
		})
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
//
// Clients must never dereference an arr-origin URL, so only the relative
// /MediaCover path (which the app resolves through the authenticated instance
// proxy) is passed through. An absolute arr-origin URL falls back to the
// metadata provider's CDN copy, and anything else yields "" so the app draws
// its placeholder instead of a broken image.
func clientReachableCover(book chaptarr.Book) string {
	pick := func(match func(chaptarr.Image) bool) (chaptarr.Image, bool) {
		for _, img := range book.Images {
			if img.URL != "" && match(img) {
				return img, true
			}
		}
		return chaptarr.Image{}, false
	}
	img, ok := pick(func(i chaptarr.Image) bool { return i.CoverType == "cover" })
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
