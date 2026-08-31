package request

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

// ErrBookSeriesNotFound means the requested series name matches nothing this
// Chaptarr library holds. It is a 404, not a server fault.
var ErrBookSeriesNotFound = errors.New("series is not in this book library")

// bookSeriesCacheTTL matches the other browse digests. The Chaptarr webhook
// drops this key on any library change.
const bookSeriesCacheTTL = 60 * time.Second

// seriesCoverDepth is how many covers a series card stacks.
const seriesCoverDepth = 3

// bookSeriesMaxItems caps the browse row, applied after the requested order for
// the same reason the authors row does it.
const bookSeriesMaxItems = 200

// The orders the series row can be read in. There is deliberately no
// "date added" here, unlike the authors row: an author record carries an
// `added` date, a series is not a record at all — it exists only as a string on
// each book — and no book carries an added date either. The only dates in reach
// are publication dates and file-import dates, and neither is "when this series
// entered your library". Offering the option backed by a proxy date would put a
// confident wrong order behind a control the user can't check.
const (
	// SeriesSortBooks leads with the series the library holds most of.
	SeriesSortBooks = "books"
	// SeriesSortName is alphabetical by series name.
	SeriesSortName = "name"
)

func normalizeSeriesSort(sort string) string {
	if strings.EqualFold(strings.TrimSpace(sort), SeriesSortName) {
		return SeriesSortName
	}
	return SeriesSortBooks
}

// LibrarySeries is one series the library holds at least one book of.
type LibrarySeries struct {
	// Name is the series' identity here. Chaptarr stores no series record for a
	// library-wide read — `GET /series` returns nothing without an author — so
	// the name parsed off each book is all there is to address one by. It also
	// unifies the 25-odd series that legitimately span authors, which a
	// per-author id could not.
	Name string `json:"name"`
	// Covers are the earliest books' covers, in reading order, under the same
	// client-reachable rule as every other image the server hands out. The row
	// stacks them so a series looks like a run of books rather than one of
	// them; a series with a single cover simply has one. Capped at
	// [seriesCoverDepth] because a deeper stack renders as a smudge.
	Covers []string `json:"covers"`
	// TitleCount is how many distinct titles of the series the library tracks.
	TitleCount int `json:"title_count"`
	// AvailableCount is how many of those have a file on disk in any format.
	AvailableCount int `json:"available_count"`
}

// BookSeriesDigest is the series browse row's payload.
type BookSeriesDigest struct {
	Series []LibrarySeries `json:"series"`
	// Total is how many series the library holds before the row's cap, so a
	// client showing fewer can say so.
	Total int `json:"total"`
}

// SeriesTitle is one title of a series: the same per-format ownership shape the
// rest of the book surfaces use, plus where it falls in the series.
type SeriesTitle struct {
	LibraryTitle
	// Position is the raw position string from the library ("13", "1.5",
	// "2A", "3, Part 1 of 2"), passed through rather than normalised — it is
	// the label a reader recognises, and only its numeric prefix is used for
	// ordering. Empty when the series names no position for this title.
	Position string `json:"position"`
}

// BookSeriesDetail is one series plus every title of it the library tracks,
// in reading order.
type BookSeriesDetail struct {
	Series LibrarySeries `json:"series"`
	Titles []SeriesTitle `json:"titles"`
}

// parseSeriesTitle splits a library seriesTitle ("Discworld #13") into the
// series name and the position within it.
//
// The split is on the LAST " #" so a series whose own name contains one keeps
// it. A title with no " #" is a series with no stated position, not a title
// with a blank name.
func parseSeriesTitle(raw string) (name, position string) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", ""
	}
	i := strings.LastIndex(s, " #")
	if i < 0 {
		return s, ""
	}
	return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+2:])
}

// seriesPositionKey reads the leading number off a position for ordering.
//
// Real libraries carry positions like "2A", "1.5, 1.6, 1.7", "5/6" and
// "3, Part 1 of 2". Their numeric prefix is what places them; the rest is
// display detail, and a position with no number at all sorts last rather than
// claiming position zero.
func seriesPositionKey(position string) (float64, bool) {
	s := strings.TrimSpace(position)
	end := 0
	seenDot := false
	for end < len(s) {
		c := s[end]
		if c >= '0' && c <= '9' {
			end++
			continue
		}
		if c == '.' && !seenDot && end+1 < len(s) && s[end+1] >= '0' && s[end+1] <= '9' {
			seenDot = true
			end++
			continue
		}
		break
	}
	if end == 0 {
		return 0, false
	}
	value, err := strconv.ParseFloat(s[:end], 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

// GetLibrarySeriesForInstance returns the series this user's Chaptarr library
// actually holds books of.
//
// A user with no Chaptarr grant gets an empty list rather than an error.
func (s *Service) GetLibrarySeriesForInstance(userID int64, requestedInstanceID, sortOrder string) (*BookSeriesDigest, error) {
	client, instanceID, err := s.resolveChaptarr(userID, requestedInstanceID)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return &BookSeriesDigest{Series: []LibrarySeries{}}, nil
	}

	order := normalizeSeriesSort(sortOrder)
	cacheKey := "book-series:" + instanceID
	if s.libraryCache != nil {
		if data, ok := s.libraryCache.Get(cacheKey); ok {
			var digest BookSeriesDigest
			if err := json.Unmarshal(data, &digest); err == nil {
				return &BookSeriesDigest{
					Series: sortLibrarySeries(digest.Series, order, bookSeriesMaxItems),
					Total:  len(digest.Series),
				}, nil
			}
		}
	}

	books, err := client.GetAllBooks()
	if err != nil {
		return nil, err
	}
	all := buildLibrarySeries(books)
	if s.libraryCache != nil {
		if data, err := json.Marshal(BookSeriesDigest{Series: all}); err == nil {
			s.libraryCache.Set(cacheKey, data, bookSeriesCacheTTL)
		}
	}
	return &BookSeriesDigest{
		Series: sortLibrarySeries(all, order, bookSeriesMaxItems),
		Total:  len(all),
	}, nil
}

// GetLibrarySeriesDetailForInstance returns one series and its titles in
// reading order, including the ones the library only knows about — the gap is
// the reason to open the page.
//
// Uncached for the same reason the author page is: it is read to decide what to
// request, so it must reflect a request made seconds ago.
func (s *Service) GetLibrarySeriesDetailForInstance(userID int64, name, requestedInstanceID string) (*BookSeriesDetail, error) {
	client, _, err := s.resolveChaptarr(userID, requestedInstanceID)
	if err != nil {
		return nil, err
	}
	// No access is not "series missing": the caller asked about a library they
	// cannot see, and 404 would claim this library was searched.
	if client == nil {
		return nil, ErrChaptarrInstanceForbidden
	}
	wanted := strings.TrimSpace(name)
	if wanted == "" {
		return nil, ErrBookSeriesNotFound
	}

	books, err := client.GetAllBooks()
	if err != nil {
		return nil, err
	}

	matched := make([]chaptarr.Book, 0, 16)
	positions := make(map[string]string, 16)
	authorIDs := make(map[string]int, 16)
	for _, book := range books {
		seriesName, position := parseSeriesTitle(book.SeriesTitle)
		if !strings.EqualFold(seriesName, wanted) {
			continue
		}
		matched = append(matched, book)
		// Keyed by foreignBookId, which is the group key for every record that
		// has one; a record without one keeps an empty position rather than
		// borrowing another title's.
		if id := strings.TrimSpace(book.ForeignBookID); id != "" {
			if _, seen := positions[id]; !seen && position != "" {
				positions[id] = position
			}
			if _, seen := authorIDs[id]; !seen && book.AuthorID > 0 {
				authorIDs[id] = book.AuthorID
			}
		}
	}
	if len(matched) == 0 {
		return nil, ErrBookSeriesNotFound
	}

	// No book record on this fork embeds its author, so the names come from the
	// author list and are joined on authorId. A series that spans authors —
	// and 25 of them do — would otherwise list its books with no author at all.
	names := map[int]string{}
	if authors, err := client.GetAllAuthors(); err == nil {
		for _, a := range authors {
			names[a.ID] = strings.TrimSpace(a.AuthorName)
		}
	}

	reduced := reduceLibrary(matched).Titles
	titles := make([]SeriesTitle, 0, len(reduced))
	for _, title := range reduced {
		id := strings.TrimSpace(title.ForeignBookID)
		if title.Author == "" {
			title.Author = names[authorIDs[id]]
		}
		titles = append(titles, SeriesTitle{LibraryTitle: title, Position: positions[id]})
	}
	sortSeriesTitles(titles)

	series := buildLibrarySeries(matched)
	detail := &BookSeriesDetail{Titles: titles}
	if len(series) > 0 {
		detail.Series = series[0]
	} else {
		// Every book of the series is metadata the library has never held a
		// file for, so buildLibrarySeries drops it. The page still exists and
		// still lists the gap; only the counts have to be derived here.
		detail.Series = LibrarySeries{
			Name:       wanted,
			TitleCount: len(titles),
			Covers:     seriesCovers(matched),
		}
	}
	return detail, nil
}

// buildLibrarySeries reduces the library to the series it actually holds.
//
// A series with nothing on disk is dropped. Adding one author imports their
// whole bibliography, so a real library "knows about" several times more series
// than it holds — on the library this was built against, 817 series exist and
// 143 have a file. Listing all of them would make a shelf of things you own out
// of mostly things you do not.
func buildLibrarySeries(books []chaptarr.Book) []LibrarySeries {
	type group struct {
		name    string
		titles  map[string]bool // group key -> has a file
		records []chaptarr.Book
	}
	groups := make(map[string]*group)
	order := make([]string, 0, 32)

	for _, book := range books {
		name, _ := parseSeriesTitle(book.SeriesTitle)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		g, ok := groups[key]
		if !ok {
			g = &group{name: name, titles: map[string]bool{}}
			groups[key] = g
			order = append(order, key)
		}
		titleKey := groupKey(book)
		if book.Statistics.BookFileCount > 0 {
			g.titles[titleKey] = true
		} else if _, seen := g.titles[titleKey]; !seen {
			g.titles[titleKey] = false
		}
		g.records = append(g.records, book)
	}

	items := make([]LibrarySeries, 0, len(order))
	for _, key := range order {
		g := groups[key]
		available := 0
		for _, hasFile := range g.titles {
			if hasFile {
				available++
			}
		}
		if available == 0 {
			continue
		}
		items = append(items, LibrarySeries{
			Name:           g.name,
			Covers:         seriesCovers(g.records),
			TitleCount:     len(g.titles),
			AvailableCount: available,
		})
	}
	return items
}

// seriesCoverRank places a book in the running for the front of the stack.
//
// A series is recognised by book one, so the main numbered run leads: any
// position of 1 or more sorts ahead of everything else, ascending. Everything
// outside that run follows, and that matters more than it sounds — real
// libraries file boxed sets, companions and omnibus collections at position 0,
// and 0 sorts before 1, so ranking on the raw number alone put a photograph of
// book spines on the front of Discworld and "The Complete Wheel of Time" on
// the front of that one.
//
// Tier 0 is the numbered run (>= 1), tier 1 the sub-one positions (0 for
// collections, 0.5 for the occasional prequel novella), tier 2 the records the
// series states no position for at all.
func seriesCoverRank(position string) (tier int, key float64) {
	value, ok := seriesPositionKey(position)
	switch {
	case !ok:
		return 2, 0
	case value >= 1:
		return 0, value
	default:
		return 1, value
	}
}

// seriesCovers picks the covers a series card stacks: the first book the
// library actually holds, then the run in order behind it.
//
// Ownership outranks position, because this row is about a library rather than
// a bibliography — showing book one's art for a book nobody has is a picture of
// something you do not own, and the count beside it already says how much of
// the series is missing. So book one leads whenever it is on disk; otherwise
// the lowest-numbered book that is; and only if the library holds none of them
// with art does an un-owned cover fill the frame.
//
// Duplicates are dropped — several records of one title share its art, and
// stacking the same cover three times reads as a rendering fault rather than a
// series. The final tie-break is the title, so a series whose metadata files
// several records at the same position (Discworld has twenty at #1, including
// two omnibuses) picks the same one on every fetch instead of following
// whatever order the arr happened to answer in.
func seriesCovers(books []chaptarr.Book) []string {
	type candidate struct {
		owned bool
		tier  int
		key   float64
		title string
		cover string
	}
	candidates := make([]candidate, 0, len(books))
	for _, book := range books {
		cover := clientReachableCover(book)
		if cover == "" {
			continue
		}
		_, position := parseSeriesTitle(book.SeriesTitle)
		tier, key := seriesCoverRank(position)
		candidates = append(candidates, candidate{
			owned: book.Statistics.BookFileCount > 0,
			tier:  tier,
			key:   key,
			title: strings.ToLower(strings.TrimSpace(book.Title)),
			cover: cover,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].owned != candidates[j].owned {
			return candidates[i].owned
		}
		if candidates[i].tier != candidates[j].tier {
			return candidates[i].tier < candidates[j].tier
		}
		if candidates[i].key != candidates[j].key {
			return candidates[i].key < candidates[j].key
		}
		return candidates[i].title < candidates[j].title
	})

	covers := make([]string, 0, seriesCoverDepth)
	seen := make(map[string]struct{}, seriesCoverDepth)
	for _, c := range candidates {
		if _, dup := seen[c.cover]; dup {
			continue
		}
		seen[c.cover] = struct{}{}
		covers = append(covers, c.cover)
		if len(covers) == seriesCoverDepth {
			break
		}
	}
	return covers
}

// sortLibrarySeries orders the row and then caps it, with the same name
// tie-break every other browse row uses so an unchanged library never
// reshuffles between fetches.
func sortLibrarySeries(items []LibrarySeries, order string, limit int) []LibrarySeries {
	if items == nil {
		return []LibrarySeries{}
	}
	sorted := make([]LibrarySeries, len(items))
	copy(sorted, items)

	byName := func(i, j int) bool {
		return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name)
	}
	if normalizeSeriesSort(order) == SeriesSortName {
		sort.Slice(sorted, byName)
	} else {
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

// sortSeriesTitles orders a series in reading order: by the numeric prefix of
// each position, then by the raw position so "2A" and "2B" stay in order, and
// finally by title. Titles the series states no position for trail the rest
// rather than claiming the front.
func sortSeriesTitles(titles []SeriesTitle) {
	sort.SliceStable(titles, func(i, j int) bool {
		a, aOK := seriesPositionKey(titles[i].Position)
		b, bOK := seriesPositionKey(titles[j].Position)
		if aOK != bOK {
			return aOK
		}
		if aOK && a != b {
			return a < b
		}
		if titles[i].Position != titles[j].Position {
			return titles[i].Position < titles[j].Position
		}
		return strings.ToLower(titles[i].Title) < strings.ToLower(titles[j].Title)
	})
}
