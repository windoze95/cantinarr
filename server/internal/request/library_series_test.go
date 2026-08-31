package request

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

func seriesBook(id, authorID int, foreignID, seriesTitle string, files int) chaptarr.Book {
	b := chaptarr.Book{
		ID:            id,
		AuthorID:      authorID,
		ForeignBookID: foreignID,
		Title:         foreignID,
		SeriesTitle:   seriesTitle,
		MediaType:     "ebook",
	}
	b.Statistics.BookFileCount = files
	return b
}

// TestParseSeriesTitleHandlesWhatRealLibrariesContain. Every one of these came
// out of a real library: positions that are not numbers, series whose names
// contain punctuation, and books stated without a position at all.
func TestParseSeriesTitleHandlesWhatRealLibrariesContain(t *testing.T) {
	cases := []struct {
		raw      string
		name     string
		position string
	}{
		{"Discworld #13", "Discworld", "13"},
		{"Discworld - Tiffany Aching #1", "Discworld - Tiffany Aching", "1"},
		{"Discworld Companion Books", "Discworld Companion Books", ""},
		{"Harry Potter Japanese Split-Volume Children's Edition #2A", "Harry Potter Japanese Split-Volume Children's Edition", "2A"},
		{"Wonder #1.5, 1.6, 1.7", "Wonder", "1.5, 1.6, 1.7"},
		{"Le Comte de Monte-Cristo / The Count of Monte Cristo #5/6", "Le Comte de Monte-Cristo / The Count of Monte Cristo", "5/6"},
		{"Dune #1, part 3 of 3", "Dune", "1, part 3 of 3"},
		{"プロジェクト・ヘイル・メアリー #下", "プロジェクト・ヘイル・メアリー", "下"},
		// The split takes the LAST " #", so a series carrying one in its own
		// name keeps it.
		{"Bands of Mourning #2 Reissue #3", "Bands of Mourning #2 Reissue", "3"},
		{"   ", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			name, position := parseSeriesTitle(tc.raw)
			if name != tc.name || position != tc.position {
				t.Errorf("parseSeriesTitle(%q) = (%q, %q), want (%q, %q)",
					tc.raw, name, position, tc.name, tc.position)
			}
		})
	}
}

// TestSeriesPositionKeyReadsTheLeadingNumber: an unparseable position must not
// claim position zero and lead the page.
func TestSeriesPositionKeyReadsTheLeadingNumber(t *testing.T) {
	cases := map[string]struct {
		value float64
		ok    bool
	}{
		"13":             {13, true},
		"1.5":            {1.5, true},
		"2A":             {2, true},
		"1.5, 1.6, 1.7":  {1.5, true},
		"5/6":            {5, true},
		"3, Part 1 of 2": {3, true},
		"下":              {0, false},
		"":               {0, false},
		// A leading-dot position is still a number, and 0.5 belongs before 1.
		".5": {0.5, true},
		// A lone dot is not.
		".": {0, false},
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			value, ok := seriesPositionKey(in)
			if ok != want.ok || (ok && value != want.value) {
				t.Errorf("seriesPositionKey(%q) = (%v, %v), want (%v, %v)", in, value, ok, want.value, want.ok)
			}
		})
	}
}

// TestBuildLibrarySeriesDropsSeriesWithNothingOnDisk: adding one author imports
// their whole bibliography, so a library knows about several times more series
// than it holds. A row of things you own must not be mostly things you do not.
func TestBuildLibrarySeriesDropsSeriesWithNothingOnDisk(t *testing.T) {
	books := []chaptarr.Book{
		seriesBook(1, 10, "fb-1", "Held #1", 1),
		seriesBook(2, 10, "fb-2", "Held #2", 0),
		seriesBook(3, 10, "fb-3", "Only Metadata #1", 0),
		seriesBook(4, 10, "fb-4", "Only Metadata #2", 0),
	}

	got := buildLibrarySeries(books)

	if len(got) != 1 || got[0].Name != "Held" {
		t.Fatalf("series = %+v, want only the one with a file", got)
	}
	if got[0].TitleCount != 2 || got[0].AvailableCount != 1 {
		t.Errorf("counts = %d/%d, want 1 of 2", got[0].AvailableCount, got[0].TitleCount)
	}
}

// TestBuildLibrarySeriesCountsTitlesNotRecords mirrors the authors row: a title
// held as both an ebook and an audiobook is two records sharing a
// foreignBookId, and counting records would double it.
func TestBuildLibrarySeriesCountsTitlesNotRecords(t *testing.T) {
	books := []chaptarr.Book{
		seriesBook(1, 10, "fb-1", "Discworld #1", 1),
		seriesBook(2, 10, "fb-1", "Discworld #1", 1),
		seriesBook(3, 10, "fb-2", "Discworld #2", 0),
	}

	got := buildLibrarySeries(books)

	if len(got) != 1 {
		t.Fatalf("series = %+v, want one", got)
	}
	if got[0].TitleCount != 2 || got[0].AvailableCount != 1 {
		t.Errorf("counts = %d of %d, want 1 of 2 distinct titles",
			got[0].AvailableCount, got[0].TitleCount)
	}
}

// TestBuildLibrarySeriesUnifiesASeriesAcrossAuthors: series really do span
// authors, and the name is what identifies one — a per-author key would split
// them into look-alike halves.
func TestBuildLibrarySeriesUnifiesASeriesAcrossAuthors(t *testing.T) {
	books := []chaptarr.Book{
		seriesBook(1, 10, "fb-1", "Realms of the Elderlings #1", 1),
		seriesBook(2, 11, "fb-2", "Realms of the Elderlings #2", 1),
	}

	got := buildLibrarySeries(books)

	if len(got) != 1 || got[0].TitleCount != 2 {
		t.Fatalf("series = %+v, want one series holding both authors' books", got)
	}
}

// TestBuildLibrarySeriesStacksTheEarliestCovers: a series is recognised by
// where it starts, so the card stacks its earliest books — and the stack must
// stay put as later ones arrive.
func TestBuildLibrarySeriesStacksTheEarliestCovers(t *testing.T) {
	cover := func(id int, foreignID, series, url string) chaptarr.Book {
		b := seriesBook(id, 10, foreignID, series, 1)
		b.Images = []chaptarr.Image{{CoverType: "cover", URL: url}}
		return b
	}
	// Fed in the "wrong" order on purpose: position decides, not arrival.
	got := buildLibrarySeries([]chaptarr.Book{
		cover(1, "fb-5", "Discworld #5", "/MediaCover/5.jpg"),
		cover(2, "fb-1", "Discworld #1", "/MediaCover/1.jpg"),
		cover(3, "fb-9", "Discworld #9", "/MediaCover/9.jpg"),
		cover(4, "fb-2", "Discworld #2", "/MediaCover/2.jpg"),
	})

	if len(got) != 1 {
		t.Fatalf("series = %+v, want one", got)
	}
	want := []string{"/MediaCover/1.jpg", "/MediaCover/2.jpg", "/MediaCover/5.jpg"}
	if len(got[0].Covers) != len(want) {
		t.Fatalf("covers = %v, want the three earliest %v", got[0].Covers, want)
	}
	for i, url := range want {
		if got[0].Covers[i] != url {
			t.Fatalf("covers = %v, want %v", got[0].Covers, want)
		}
	}
}

// TestSeriesCoversDropDuplicateArt: a title's ebook and audiobook records share
// one cover, so an unguarded stack would draw the same image three times and
// read as a rendering fault rather than a series.
func TestSeriesCoversDropDuplicateArt(t *testing.T) {
	same := func(id int, foreignID, series string) chaptarr.Book {
		b := seriesBook(id, 10, foreignID, series, 1)
		b.Images = []chaptarr.Image{{CoverType: "cover", URL: "/MediaCover/1.jpg"}}
		return b
	}
	different := seriesBook(3, 10, "fb-2", "Discworld #2", 1)
	different.Images = []chaptarr.Image{{CoverType: "cover", URL: "/MediaCover/2.jpg"}}

	covers := seriesCovers([]chaptarr.Book{
		same(1, "fb-1", "Discworld #1"),
		same(2, "fb-1", "Discworld #1"),
		different,
	})

	if len(covers) != 2 || covers[0] != "/MediaCover/1.jpg" || covers[1] != "/MediaCover/2.jpg" {
		t.Errorf("covers = %v, want each distinct cover once", covers)
	}
}

// TestSeriesCoversFallBackToUnpositionedBooks: a series whose numbered books
// carry no art still gets a stack rather than an empty frame.
func TestSeriesCoversFallBackToUnpositionedBooks(t *testing.T) {
	numbered := seriesBook(1, 10, "fb-1", "Discworld #1", 1) // no images
	companion := seriesBook(2, 10, "fb-2", "Discworld Companion", 1)
	companion.Images = []chaptarr.Image{{CoverType: "cover", URL: "/MediaCover/c.jpg"}}

	covers := seriesCovers([]chaptarr.Book{numbered, companion})

	if len(covers) != 1 || covers[0] != "/MediaCover/c.jpg" {
		t.Errorf("covers = %v, want the companion's art", covers)
	}
}

// TestSeriesCoversStopAtTheStackDepth keeps a long series from shipping art the
// card will never draw.
func TestSeriesCoversStopAtTheStackDepth(t *testing.T) {
	var books []chaptarr.Book
	for i := 1; i <= 10; i++ {
		b := seriesBook(i, 10, fmt.Sprintf("fb-%d", i), fmt.Sprintf("Discworld #%d", i), 1)
		b.Images = []chaptarr.Image{{CoverType: "cover", URL: fmt.Sprintf("/MediaCover/%d.jpg", i)}}
		books = append(books, b)
	}

	if got := seriesCovers(books); len(got) != seriesCoverDepth {
		t.Errorf("covers = %v, want %d", got, seriesCoverDepth)
	}
}

// TestSortSeriesTitlesReadsInSeriesOrder: positions place the books, and a
// title the series states no position for trails rather than leading.
func TestSortSeriesTitlesReadsInSeriesOrder(t *testing.T) {
	titles := []SeriesTitle{
		{LibraryTitle: LibraryTitle{Title: "Companion"}, Position: ""},
		{LibraryTitle: LibraryTitle{Title: "Tenth"}, Position: "10"},
		{LibraryTitle: LibraryTitle{Title: "Second"}, Position: "2"},
		{LibraryTitle: LibraryTitle{Title: "Second and a half"}, Position: "2.5"},
		{LibraryTitle: LibraryTitle{Title: "Second, part A"}, Position: "2A"},
		{LibraryTitle: LibraryTitle{Title: "Unnumbered"}, Position: "下"},
	}

	sortSeriesTitles(titles)

	// 2 before 2A before 2.5: the numeric prefix places them, and the raw
	// position breaks the tie between "2" and "2A".
	want := []string{"Second", "Second, part A", "Second and a half", "Tenth", "Companion", "Unnumbered"}
	for i, name := range want {
		if titles[i].Title != name {
			got := make([]string, len(titles))
			for j, x := range titles {
				got[j] = x.Title + "(" + x.Position + ")"
			}
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// TestSortLibrarySeriesCapsAfterOrdering: same rule as the authors row — the
// cap must never decide which series a name sort can show.
func TestSortLibrarySeriesCapsAfterOrdering(t *testing.T) {
	items := []LibrarySeries{
		{Name: "Zed Holds Many", TitleCount: 90, AvailableCount: 90},
		{Name: "Yves Holds Some", TitleCount: 50, AvailableCount: 50},
		{Name: "Aaron Holds One", TitleCount: 1, AvailableCount: 1},
	}

	byName := sortLibrarySeries(items, SeriesSortName, 2)

	if len(byName) != 2 || byName[0].Name != "Aaron Holds One" {
		t.Fatalf("byName = %+v, want the alphabetically first series to survive the cap", byName)
	}
	if items[0].Name != "Zed Holds Many" {
		t.Error("sorting mutated the cached slice")
	}
}

func TestNormalizeSeriesSortFallsBack(t *testing.T) {
	for in, want := range map[string]string{
		"": SeriesSortBooks, "NAME": SeriesSortName, " name ": SeriesSortName,
		"added": SeriesSortBooks, "nonsense": SeriesSortBooks,
	} {
		if got := normalizeSeriesSort(in); got != want {
			t.Errorf("normalizeSeriesSort(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestGetLibrarySeriesWithoutChaptarrGrantReturnsEmpty: no access is no row,
// not a failure.
func TestGetLibrarySeriesWithoutChaptarrGrantReturnsEmpty(t *testing.T) {
	digest, err := (&Service{}).GetLibrarySeriesForInstance(1, "", "")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if digest == nil || digest.Series == nil || len(digest.Series) != 0 {
		t.Fatalf("digest = %+v, want an empty non-nil list", digest)
	}
}

// TestGetLibrarySeriesDetailWithoutGrantIsForbiddenNotMissing keeps "this
// library has no such series" apart from "you cannot see this library".
func TestGetLibrarySeriesDetailWithoutGrantIsForbiddenNotMissing(t *testing.T) {
	_, err := (&Service{}).GetLibrarySeriesDetailForInstance(1, "Discworld", "")
	if err == nil || requestErrorStatus(err) != http.StatusForbidden {
		t.Fatalf("err = %v (status %d), want 403", err, requestErrorStatus(err))
	}
}

func TestBookSeriesNotFoundMapsTo404(t *testing.T) {
	if got := requestErrorStatus(ErrBookSeriesNotFound); got != http.StatusNotFound {
		t.Errorf("status = %d, want 404", got)
	}
}

// coverBook is an owned series book with art, for the front-cover tests.
func coverBook(id int, foreignID, seriesTitle, url string) chaptarr.Book {
	b := seriesBook(id, 10, foreignID, seriesTitle, 1)
	b.Title = url // distinct, deterministic titles for the final tie-break
	b.Images = []chaptarr.Image{{CoverType: "cover", URL: url}}
	return b
}

// unownedCoverBook is the same, for a book the library knows about but holds
// no file for.
func unownedCoverBook(id int, foreignID, seriesTitle, url string) chaptarr.Book {
	b := coverBook(id, foreignID, seriesTitle, url)
	b.Statistics.BookFileCount = 0
	return b
}

// TestSeriesCoversLeadWithBookOne is the rule a real library made necessary.
// Boxed sets, companions and omnibus collections are filed at position 0, and
// 0 sorts before 1 — so ranking on the raw number put a photograph of book
// spines on the front of Discworld instead of its first book.
func TestSeriesCoversLeadWithBookOne(t *testing.T) {
	covers := seriesCovers([]chaptarr.Book{
		coverBook(1, "fb-box", "Discworld #0", "/MediaCover/boxed-set.jpg"),
		coverBook(2, "fb-2", "Discworld #2", "/MediaCover/2.jpg"),
		coverBook(3, "fb-1", "Discworld #1", "/MediaCover/1.jpg"),
	})

	want := []string{"/MediaCover/1.jpg", "/MediaCover/2.jpg", "/MediaCover/boxed-set.jpg"}
	if len(covers) != len(want) {
		t.Fatalf("covers = %v, want %v", covers, want)
	}
	for i, url := range want {
		if covers[i] != url {
			t.Fatalf("covers = %v, want %v — book one leads, the collection trails", covers, want)
		}
	}
}

// TestSeriesCoversFallToTheLowestNumberedBook: a library that holds book 4 but
// not books 1-3 leads with 4, not with whatever unnumbered extra it happens to
// have.
func TestSeriesCoversFallToTheLowestNumberedBook(t *testing.T) {
	covers := seriesCovers([]chaptarr.Book{
		coverBook(1, "fb-companion", "Discworld Companion", "/MediaCover/companion.jpg"),
		coverBook(2, "fb-9", "Discworld #9", "/MediaCover/9.jpg"),
		coverBook(3, "fb-box", "Discworld #0", "/MediaCover/boxed-set.jpg"),
		coverBook(4, "fb-4", "Discworld #4", "/MediaCover/4.jpg"),
	})

	if len(covers) == 0 || covers[0] != "/MediaCover/4.jpg" {
		t.Fatalf("covers = %v, want the lowest numbered book (4) at the front", covers)
	}
	if covers[1] != "/MediaCover/9.jpg" {
		t.Errorf("covers = %v, want the rest of the run before the extras", covers)
	}
}

// TestSeriesCoversSkipABookOneWithNoArt: book one leads only if the library can
// actually draw it. A missing cover hands the front to the next in the run
// rather than leaving an empty frame.
func TestSeriesCoversSkipABookOneWithNoArt(t *testing.T) {
	first := seriesBook(1, 10, "fb-1", "Discworld #1", 1) // no images
	covers := seriesCovers([]chaptarr.Book{
		first,
		coverBook(2, "fb-3", "Discworld #3", "/MediaCover/3.jpg"),
		coverBook(3, "fb-2", "Discworld #2", "/MediaCover/2.jpg"),
	})

	if len(covers) != 2 || covers[0] != "/MediaCover/2.jpg" {
		t.Fatalf("covers = %v, want book two at the front", covers)
	}
}

// TestSeriesCoversPreferAPrequelOverAnUnnumberedExtra: a 0.5 novella is still
// part of the story; a record with no stated position is the last resort.
func TestSeriesCoversPreferAPrequelOverAnUnnumberedExtra(t *testing.T) {
	covers := seriesCovers([]chaptarr.Book{
		coverBook(1, "fb-x", "Discworld Companion Books", "/MediaCover/extra.jpg"),
		coverBook(2, "fb-half", "Discworld #0.5", "/MediaCover/half.jpg"),
	})

	if len(covers) != 2 || covers[0] != "/MediaCover/half.jpg" {
		t.Errorf("covers = %v, want the numbered prequel ahead of the extra", covers)
	}
}

func TestSeriesCoverRankTiers(t *testing.T) {
	cases := map[string]int{
		"1": 0, "2": 0, "13": 0, "1.5": 0, "2A": 0,
		"0": 1, "0.5": 1,
		"": 2, "下": 2,
	}
	for position, want := range cases {
		t.Run(position, func(t *testing.T) {
			if tier, _ := seriesCoverRank(position); tier != want {
				t.Errorf("seriesCoverRank(%q) tier = %d, want %d", position, tier, want)
			}
		})
	}
}

// TestSeriesCoversLeadWithTheFirstBookOnDisk: this row is a library, not a
// bibliography. Discworld's front cover was a 1520-page omnibus the library
// holds no file for, while the book one it does hold sat behind it — a picture
// of something you do not own, next to a count that already says what is
// missing.
func TestSeriesCoversLeadWithTheFirstBookOnDisk(t *testing.T) {
	covers := seriesCovers([]chaptarr.Book{
		unownedCoverBook(1, "fb-1", "Discworld #1", "/MediaCover/omnibus.jpg"),
		coverBook(2, "fb-4", "Discworld #4", "/MediaCover/4.jpg"),
		coverBook(3, "fb-2", "Discworld #2", "/MediaCover/2.jpg"),
	})

	if len(covers) == 0 || covers[0] != "/MediaCover/2.jpg" {
		t.Fatalf("covers = %v, want the lowest-numbered owned book at the front", covers)
	}
	// The un-owned one still has a place in the stack; it just does not lead.
	if covers[len(covers)-1] != "/MediaCover/omnibus.jpg" {
		t.Errorf("covers = %v, want the un-owned cover to trail", covers)
	}
}

// TestSeriesCoversFallBackToUnownedArt: a series page can be opened for a
// series the library holds no files of at all, and an empty frame says less
// than the metadata's own art.
func TestSeriesCoversFallBackToUnownedArt(t *testing.T) {
	covers := seriesCovers([]chaptarr.Book{
		unownedCoverBook(1, "fb-2", "Discworld #2", "/MediaCover/2.jpg"),
		unownedCoverBook(2, "fb-1", "Discworld #1", "/MediaCover/1.jpg"),
	})

	if len(covers) != 2 || covers[0] != "/MediaCover/1.jpg" {
		t.Fatalf("covers = %v, want book one even though nothing is on disk", covers)
	}
}

// TestSeriesCoversAreStableAcrossFetches: Discworld files twenty records at
// position 1. Nothing in the library says which is "really" book one, so the
// choice must at least be the same one every time rather than following
// whatever order the arr answered in.
func TestSeriesCoversAreStableAcrossFetches(t *testing.T) {
	forward := []chaptarr.Book{
		coverBook(1, "fb-a", "Discworld #1", "/MediaCover/equal-rites.jpg"),
		coverBook(2, "fb-b", "Discworld #1", "/MediaCover/guards.jpg"),
		coverBook(3, "fb-c", "Discworld #1", "/MediaCover/colour.jpg"),
	}
	reversed := []chaptarr.Book{forward[2], forward[1], forward[0]}

	first := seriesCovers(forward)
	second := seriesCovers(reversed)

	if len(first) == 0 || len(second) == 0 || first[0] != second[0] {
		t.Fatalf("front cover = %v then %v, want the same either way", first, second)
	}
}
