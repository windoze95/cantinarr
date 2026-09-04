// data_books.go — the public-domain book dataset behind the fake Chaptarr
// (D8), plus the cross-domain hooks other domains call (contract.md §7):
// bookByForeignID, allBooks, chaptarrOnBookRequested, chaptarrOnBookAvailable.
//
// Everything here is guarded by chapMu (domain-local; never touch stateMu).
// Seeding happens in init() with pure data only — no state accessors.
package main

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// ─── Domain-local types ─────────────────────────────────

// chapAuthorRec is one Readarr-shaped author. InLibrary=false authors exist
// only in the lookup corpus until a request/add pulls them into the library.
type chapAuthorRec struct {
	ID        int
	Name      string
	ForeignID string
	TitleSlug string
	Overview  string
	Path      string
	Genres    []string
	InLibrary bool
	// Added is when the author entered the library. Zero when the record
	// carries no date: the authors row omits it and sorts it last rather than
	// reading a missing date as the beginning of time.
	Added time.Time
}

// chapBookMeta supplements the shared DemoBook with Chaptarr-shape metadata
// (contract rule: never extend types.go — keep extras in domain maps).
// chapProviderIDs carries the provider ids a real Chaptarr states beside a
// book's foreignBookId, in its prefixed form: the Goodreads work id and the
// Open Library work id, both taken from Wikidata for these public-domain
// titles (Goodreads work ID P8383, Open Library ID P648). The app's book pages
// build their Links line from exactly these fields, so the classics link out
// to their own Goodreads editions page and Open Library work page. The four
// invented titles have no entry and therefore no Links line, which is the
// honest rendering for a book no provider knows.
var chapProviderIDs = map[string]struct{ GoodreadsWork, OpenLibraryWork string }{
	"1885":   {"gr:3060926", "ol:OL66554W"},    // Pride and Prejudice
	"153747": {"gr:2409320", "ol:OL21501229W"}, // Moby-Dick
	"17245":  {"gr:3165724", "ol:OL85892W"},    // Dracula
	"18490":  {"gr:4836639", "ol:OL450125W"},   // Frankenstein
	"102868": {"gr:1997473", "ol:OL262496W"},   // A Study in Scarlet
	"3590":   {"gr:1222101", "ol:OL18188726W"}, // The Adventures of Sherlock Holmes
	"8921":   {"gr:3311984", "ol:OL262454W"},   // The Hound of the Baskervilles
	"2493":   {"gr:3234863", "ol:OL52267W"},    // The Time Machine
	"295":    {"gr:3077988", "ol:OL24034W"},    // Treasure Island
	"13023":  {"gr:55548884", "ol:OL138052W"},  // Alice's Adventures in Wonderland
	"32829":  {"gr:1924715", "ol:OL1099513W"},  // Journey to the Center of the Earth
	"3352":   {"gr:1112418", "ol:OL1099280W"},  // Twenty Thousand Leagues Under the Seas
	"54479":  {"gr:4537271", "ol:OL1100007W"},  // Around the World in Eighty Days
	"236093": {"gr:1993810", "ol:OL18417W"},    // The Wonderful Wizard of Oz
	"236094": {"gr:21430714", "ol:OL18396W"},   // The Marvelous Land of Oz
}

type chapBookMeta struct {
	ForeignID   string
	TitleSlug   string
	ReleaseDate string // "YYYY-MM-DD"
	Genres      []string
	PageCount   int
	AuthorID    int
	// SeriesTitle is the raw Chaptarr series string ("Sherlock Holmes #3"):
	// the series name, then the position after the last " #". Empty for a
	// standalone title, which is most of the classics here.
	SeriesTitle string
}

// chapRecState is the per-book-record (per format) library state that does
// not fit on DemoBookFormat: whether the record is live in the fake Chaptarr
// library, and its file fixture when downloaded.
type chapRecState struct {
	InLibrary bool
	FileSize  int
	Quality   string // file quality when downloaded; the format's default quality otherwise
	FilePath  string
	DateAdded time.Time
}

// chapQueueItem is one row of the fake Chaptarr download queue.
type chapQueueItem struct {
	ID                    int
	AuthorID              int
	BookID                int
	Title                 string
	Status                string
	TrackedDownloadState  string
	TrackedDownloadStatus string
	Protocol              string
	Indexer               string
	DownloadClient        string
	Size                  int
	Sizeleft              int
	Timeleft              string
	DownloadID            string
	Quality               string
}

// chapHistoryRec is one fake Chaptarr history event.
type chapHistoryRec struct {
	ID          int
	AuthorID    int
	BookID      int
	SourceTitle string
	EventType   string
	Quality     string
	DownloadID  string
	MediaType   string
	Date        time.Time
}

// ─── Domain state (guarded by chapMu) ───────────────────

var (
	chapMu sync.Mutex

	chapBooks         []*DemoBook              // catalog, seed order
	chapBooksByFID    = map[string]*DemoBook{} // foreignBookId -> book
	chapMetaByFID     = map[string]*chapBookMeta{}
	chapAuthors       []*chapAuthorRec
	chapAuthorsByID   = map[int]*chapAuthorRec{}
	chapRecStates     = map[int]*chapRecState{} // book record id -> state
	chapRecToBook     = map[int]*DemoBook{}     // book record id -> catalog book
	chapRecFormat     = map[int]string{}        // book record id -> ebook|audiobook
	chapQueue         []*chapQueueItem
	chapHistory       []*chapHistoryRec
	chapNextQueueID   = 9001
	chapNextHistoryID = 5001

	// Book record ids whose on-disk file is below the quality cutoff
	// (drives GET wanted/cutoff). Moby-Dick's PDF ebook wants an EPUB.
	chapCutoffUnmet = map[int]bool{3: true}

	// chapAuthorImportState marks authors the fake Chaptarr's metadata service
	// has a queued import for, keyed by author foreignAuthorId. Any add for one
	// of their books is refused with the author-pending-import answer (the
	// request domain parks those requests server-side). Neither import ever
	// lands in the demo: "pending" keeps retrying forever, "failed" is the
	// declared-terminal verdict behind the demoted approval-queue row.
	chapAuthorImportState = map[string]string{
		"9931": chapAuthorImportPendingState,
		"9954": chapAuthorImportFailedState,
	}

	chapSeedTime = time.Now()
)

// Author-import states for chapAuthorImportState.
const (
	chapAuthorImportPendingState = "pending"
	chapAuthorImportFailedState  = "failed"
)

// ─── Seeding ────────────────────────────────────────────

type chapFormatSeed struct {
	Format     string
	BookID     int
	InLibrary  bool
	Monitored  bool
	Downloaded bool
	Quality    string
	FileSize   int
}

// chapSeedAuthor seeds one author. addedDaysAgo of 0 means the record carries
// no added date at all — a real state for records that predate the field, and
// the one the authors row has to sort last.
func chapSeedAuthor(id int, name, foreignID, slug, overview string, genres []string, inLibrary bool, addedDaysAgo float64) *chapAuthorRec {
	a := &chapAuthorRec{
		ID: id, Name: name, ForeignID: foreignID, TitleSlug: slug,
		Overview: overview, Path: "/books/" + name, Genres: genres,
		InLibrary: inLibrary,
	}
	if addedDaysAgo > 0 {
		a.Added = chapSeedTime.Add(-time.Duration(addedDaysAgo * 24 * float64(time.Hour)))
	}
	chapAuthors = append(chapAuthors, a)
	chapAuthorsByID[id] = a
	return a
}

// chapSeedTitle seeds one title and its per-format records. series is the raw
// Chaptarr series string ("Oz #2"), empty for a standalone title.
func chapSeedTitle(a *chapAuthorRec, title, foreignID, slug, overview, release string, pages int, genres []string, series string, formats ...chapFormatSeed) {
	year := yearOf(release)
	b := &DemoBook{
		ForeignID: foreignID, Title: title,
		AuthorName: a.Name, AuthorForeignID: a.ForeignID,
		Overview: overview, Year: year,
		Formats: map[string]*DemoBookFormat{},
	}
	for _, fs := range formats {
		fileID := 0
		if fs.Downloaded {
			fileID = 100 + fs.BookID
		}
		b.Formats[fs.Format] = &DemoBookFormat{
			Monitored: fs.Monitored, Downloaded: fs.Downloaded,
			BookID: fs.BookID, FileID: fileID,
		}
		st := &chapRecState{InLibrary: fs.InLibrary, Quality: fs.Quality, FileSize: fs.FileSize}
		if fs.Downloaded {
			st.FilePath = fmt.Sprintf("%s/%s/%s (%d).%s", a.Path, title, title, year, strings.ToLower(fs.Quality))
			st.DateAdded = chapSeedTime.Add(-time.Duration(fs.BookID) * 26 * time.Hour)
		}
		chapRecStates[fs.BookID] = st
		chapRecToBook[fs.BookID] = b
		chapRecFormat[fs.BookID] = fs.Format
	}
	chapBooks = append(chapBooks, b)
	chapBooksByFID[foreignID] = b
	chapMetaByFID[foreignID] = &chapBookMeta{
		ForeignID: foreignID, TitleSlug: slug, ReleaseDate: release,
		Genres: genres, PageCount: pages, AuthorID: a.ID,
		SeriesTitle: series,
	}
}

func chapSeedHistory(eventType string, bookID int, downloadID string, hoursAgo float64) {
	b := chapRecToBook[bookID]
	if b == nil {
		return
	}
	meta := chapMetaByFID[b.ForeignID]
	st := chapRecStates[bookID]
	quality := "EPUB"
	if st != nil && st.Quality != "" {
		quality = st.Quality
	}
	chapHistory = append(chapHistory, &chapHistoryRec{
		ID: chapNextHistoryID, AuthorID: meta.AuthorID, BookID: bookID,
		SourceTitle: fmt.Sprintf("%s (%d) %s-DEMO", b.Title, b.Year, quality),
		EventType:   eventType, Quality: quality, DownloadID: downloadID,
		MediaType: chapRecFormat[bookID],
		Date:      chapSeedTime.Add(-time.Duration(hoursAgo * float64(time.Hour))),
	})
	chapNextHistoryID++
}

// chapLockedEnqueue seeds a downloading queue item (plus a "grabbed" history
// record) for a monitored-but-missing book record. Caller holds chapMu.
func chapLockedEnqueue(bookID int) *chapQueueItem {
	b := chapRecToBook[bookID]
	if b == nil {
		return nil
	}
	meta := chapMetaByFID[b.ForeignID]
	format := chapRecFormat[bookID]
	quality := "EPUB"
	size := 2_400_000
	if format == bookFormatAudiobook {
		quality = "M4B"
		size = 384_000_000
	}
	item := &chapQueueItem{
		ID: chapNextQueueID, AuthorID: meta.AuthorID, BookID: bookID,
		Title:                 fmt.Sprintf("%s (%d) Unabridged %s-DEMO", b.Title, b.Year, quality),
		Status:                "downloading",
		TrackedDownloadState:  "downloading",
		TrackedDownloadStatus: "ok",
		Protocol:              "usenet",
		Indexer:               "DemoNZB (Prowlarr)",
		DownloadClient:        "SABnzbd",
		Size:                  size, Sizeleft: size * 13 / 20,
		Timeleft:   "00:38:12",
		DownloadID: fmt.Sprintf("SABnzbd_nzo_demo_book_%d", bookID),
		Quality:    quality,
	}
	chapNextQueueID++
	chapQueue = append(chapQueue, item)
	chapSeedHistoryNow("grabbed", bookID, item.DownloadID)
	return item
}

func chapSeedHistoryNow(eventType string, bookID int, downloadID string) {
	chapSeedHistory(eventType, bookID, downloadID, 0)
	if n := len(chapHistory); n > 0 {
		chapHistory[n-1].Date = time.Now()
	}
}

func init() {
	chapMu.Lock()
	defer chapMu.Unlock()

	// Library authors. addedDaysAgo is staggered so the authors row's
	// "date added" order is visibly its own — neither most-collected nor
	// alphabetical. Herman Melville deliberately carries no date: records that
	// predate the field have none, and the row has to show one sorting last.
	austen := chapSeedAuthor(1, "Jane Austen", "1265", "jane-austen",
		"English novelist (1775–1817) known for her wit and social observation across six major novels.",
		[]string{"Classics", "Romance"}, true, 412)
	melville := chapSeedAuthor(2, "Herman Melville", "1624", "herman-melville",
		"American novelist and poet (1819–1891), author of sea narratives crowned by Moby-Dick.",
		[]string{"Classics", "Adventure"}, true, 0)
	stoker := chapSeedAuthor(3, "Bram Stoker", "6988", "bram-stoker",
		"Irish author (1847–1912) best remembered for the Gothic landmark Dracula.",
		[]string{"Horror", "Gothic"}, true, 96)
	shelley := chapSeedAuthor(4, "Mary Shelley", "11139", "mary-shelley",
		"English novelist (1797–1851) whose Frankenstein founded modern science fiction.",
		[]string{"Horror", "Science Fiction"}, true, 265)
	doyle := chapSeedAuthor(5, "Arthur Conan Doyle", "2448", "arthur-conan-doyle",
		"British writer (1859–1930), creator of the consulting detective Sherlock Holmes.",
		[]string{"Mystery", "Crime"}, true, 31)
	wells := chapSeedAuthor(6, "H. G. Wells", "880", "h-g-wells",
		"English author (1866–1946), a father of science fiction: time travel, invasion, invisibility.",
		[]string{"Science Fiction"}, true, 178)
	stevenson := chapSeedAuthor(7, "Robert Louis Stevenson", "854", "robert-louis-stevenson",
		"Scottish novelist (1850–1894) of adventure and duality: Treasure Island, Jekyll and Hyde.",
		[]string{"Adventure", "Classics"}, true, 349)
	carroll := chapSeedAuthor(8, "Lewis Carroll", "8164", "lewis-carroll",
		"English author and mathematician (1832–1898), creator of the Alice books.",
		[]string{"Fantasy", "Classics"}, false, 0)
	verne := chapSeedAuthor(11, "Jules Verne", "696805", "jules-verne",
		"French novelist (1828–1905) whose Extraordinary Voyages went under the sea, into the earth, and around the world.",
		[]string{"Science Fiction", "Adventure"}, true, 12)
	baum := chapSeedAuthor(12, "L. Frank Baum", "5158478", "l-frank-baum",
		"American children's author (1856–1919) who followed the first Oz book with thirteen more.",
		[]string{"Fantasy", "Classics"}, true, 58)

	// Fully downloaded in both formats.
	chapSeedTitle(austen, "Pride and Prejudice", "1885", "pride-and-prejudice",
		"Elizabeth Bennet navigates manners, marriage, and Mr. Darcy in Regency England.",
		"1813-01-28", 432, []string{"Classics", "Romance"}, "",
		chapFormatSeed{Format: bookFormatEbook, BookID: 1, InLibrary: true, Monitored: true, Downloaded: true, Quality: "EPUB", FileSize: 1_830_000},
		chapFormatSeed{Format: bookFormatAudiobook, BookID: 2, InLibrary: true, Monitored: true, Downloaded: true, Quality: "M4B", FileSize: 341_000_000},
	)
	// Both formats downloaded; the PDF ebook is below cutoff (wanted/cutoff row).
	chapSeedTitle(melville, "Moby-Dick", "153747", "moby-dick",
		"Captain Ahab drags the Pequod across the oceans in pursuit of the white whale.",
		"1851-10-18", 720, []string{"Classics", "Adventure", "Sea Stories"}, "",
		chapFormatSeed{Format: bookFormatEbook, BookID: 3, InLibrary: true, Monitored: true, Downloaded: true, Quality: "PDF", FileSize: 6_120_000},
		chapFormatSeed{Format: bookFormatAudiobook, BookID: 4, InLibrary: true, Monitored: true, Downloaded: true, Quality: "MP3", FileSize: 612_000_000},
	)
	// Ebook downloaded; audiobook monitored, not downloaded — currently in the queue.
	chapSeedTitle(stoker, "Dracula", "17245", "dracula",
		"An epistolary Gothic chase from a Transylvanian castle to the streets of London.",
		"1897-05-26", 418, []string{"Horror", "Gothic"}, "",
		chapFormatSeed{Format: bookFormatEbook, BookID: 5, InLibrary: true, Monitored: true, Downloaded: true, Quality: "EPUB", FileSize: 1_240_000},
		chapFormatSeed{Format: bookFormatAudiobook, BookID: 6, InLibrary: true, Monitored: true, Downloaded: false, Quality: "M4B"},
	)
	// Ebook-only in the library; the audiobook edition exists but is untracked
	// (requestable / "track missing format" demo).
	chapSeedTitle(shelley, "Frankenstein", "18490", "frankenstein",
		"Victor Frankenstein animates a creature and reaps the consequences of abandoning it.",
		"1818-01-01", 280, []string{"Horror", "Science Fiction", "Gothic"}, "",
		chapFormatSeed{Format: bookFormatEbook, BookID: 7, InLibrary: true, Monitored: true, Downloaded: true, Quality: "EPUB", FileSize: 990_000},
		chapFormatSeed{Format: bookFormatAudiobook, BookID: 8, Quality: "M4B"},
	)
	// The Sherlock Holmes run: three titles, the last of them a gap, so the
	// series and author rows both read "2 of 3 books available".
	chapSeedTitle(doyle, "A Study in Scarlet", "102868", "a-study-in-scarlet",
		"An army surgeon back from Afghanistan takes rooms with a stranger who reads him at a glance.",
		"1887-11-01", 174, []string{"Mystery", "Crime"}, "Sherlock Holmes #1",
		chapFormatSeed{Format: bookFormatEbook, BookID: 20, InLibrary: true, Monitored: true, Downloaded: true, Quality: "EPUB", FileSize: 780_000},
		chapFormatSeed{Format: bookFormatAudiobook, BookID: 21, InLibrary: true, Monitored: true, Downloaded: true, Quality: "M4B", FileSize: 268_000_000},
	)
	// Fully downloaded in both formats.
	chapSeedTitle(doyle, "The Adventures of Sherlock Holmes", "3590", "the-adventures-of-sherlock-holmes",
		"Twelve cases from Baker Street, narrated by the ever-loyal Dr. Watson.",
		"1892-10-14", 307, []string{"Mystery", "Short Stories"}, "Sherlock Holmes #3",
		chapFormatSeed{Format: bookFormatEbook, BookID: 9, InLibrary: true, Monitored: true, Downloaded: true, Quality: "EPUB", FileSize: 1_410_000},
		chapFormatSeed{Format: bookFormatAudiobook, BookID: 10, InLibrary: true, Monitored: true, Downloaded: true, Quality: "M4B", FileSize: 402_000_000},
	)
	// Monitored ebook with no file (wanted/missing row) and an untracked
	// audiobook — the hole in an otherwise complete series.
	chapSeedTitle(doyle, "The Hound of the Baskervilles", "8921", "the-hound-of-the-baskervilles",
		"A moor, a family curse, and a hound whose footprints stop short of the body.",
		"1902-04-01", 256, []string{"Mystery", "Crime"}, "Sherlock Holmes #5",
		chapFormatSeed{Format: bookFormatEbook, BookID: 22, InLibrary: true, Monitored: true, Downloaded: false, Quality: "EPUB"},
		chapFormatSeed{Format: bookFormatAudiobook, BookID: 23, Quality: "M4B"},
	)
	// Ebook only — no audiobook edition exists for this title at all.
	chapSeedTitle(wells, "The Time Machine", "2493", "the-time-machine",
		"A Victorian inventor rides his machine to the year 802,701 and beyond.",
		"1895-05-07", 118, []string{"Science Fiction"}, "",
		chapFormatSeed{Format: bookFormatEbook, BookID: 11, InLibrary: true, Monitored: true, Downloaded: true, Quality: "EPUB", FileSize: 640_000},
	)
	// Ebook downloaded; audiobook monitored but missing (wanted/missing row).
	chapSeedTitle(stevenson, "Treasure Island", "295", "treasure-island",
		"Jim Hawkins, a treasure map, and the most charming mutineer in fiction.",
		"1883-11-14", 292, []string{"Adventure", "Classics"}, "",
		chapFormatSeed{Format: bookFormatEbook, BookID: 12, InLibrary: true, Monitored: true, Downloaded: true, Quality: "EPUB", FileSize: 1_050_000},
		chapFormatSeed{Format: bookFormatAudiobook, BookID: 13, InLibrary: true, Monitored: true, Downloaded: false, Quality: "M4B"},
	)
	// Totally absent from the library — requestable end to end.
	chapSeedTitle(carroll, "Alice's Adventures in Wonderland", "13023", "alices-adventures-in-wonderland",
		"Alice follows a hurried white rabbit down a hole into a world of unhinged logic.",
		"1865-11-26", 176, []string{"Fantasy", "Classics"}, "",
		chapFormatSeed{Format: bookFormatEbook, BookID: 14, Quality: "EPUB"},
		chapFormatSeed{Format: bookFormatAudiobook, BookID: 15, Quality: "M4B"},
	)
	// The Extraordinary Voyages: a complete series, so the series row can show
	// "all available" beside one that is missing a book.
	chapSeedTitle(verne, "Journey to the Center of the Earth", "32829", "journey-to-the-center-of-the-earth",
		"A professor reads a runic note and takes his nephew down an Icelandic volcano.",
		"1864-11-25", 183, []string{"Science Fiction", "Adventure"}, "Extraordinary Voyages #3",
		chapFormatSeed{Format: bookFormatEbook, BookID: 24, InLibrary: true, Monitored: true, Downloaded: true, Quality: "EPUB", FileSize: 1_120_000},
		chapFormatSeed{Format: bookFormatAudiobook, BookID: 25, InLibrary: true, Monitored: true, Downloaded: true, Quality: "M4B", FileSize: 298_000_000},
	)
	chapSeedTitle(verne, "Twenty Thousand Leagues Under the Seas", "3352", "twenty-thousand-leagues-under-the-seas",
		"Three castaways are taken aboard the Nautilus by a captain who has renounced the land.",
		"1870-06-20", 424, []string{"Science Fiction", "Adventure", "Sea Stories"}, "Extraordinary Voyages #6",
		chapFormatSeed{Format: bookFormatEbook, BookID: 26, InLibrary: true, Monitored: true, Downloaded: true, Quality: "EPUB", FileSize: 1_640_000},
	)
	chapSeedTitle(verne, "Around the World in Eighty Days", "54479", "around-the-world-in-eighty-days",
		"Phileas Fogg wagers half his fortune that the timetable will hold.",
		"1873-01-30", 256, []string{"Adventure", "Classics"}, "Extraordinary Voyages #11",
		chapFormatSeed{Format: bookFormatEbook, BookID: 27, InLibrary: true, Monitored: true, Downloaded: true, Quality: "EPUB", FileSize: 1_020_000},
	)
	// A short complete series, so the row holds more than one shape.
	chapSeedTitle(baum, "The Wonderful Wizard of Oz", "236093", "the-wonderful-wizard-of-oz",
		"A Kansas cyclone drops Dorothy on a road she has to follow to get home.",
		"1900-05-17", 154, []string{"Fantasy", "Classics"}, "Oz #1",
		chapFormatSeed{Format: bookFormatEbook, BookID: 28, InLibrary: true, Monitored: true, Downloaded: true, Quality: "EPUB", FileSize: 860_000},
		chapFormatSeed{Format: bookFormatAudiobook, BookID: 29, InLibrary: true, Monitored: true, Downloaded: true, Quality: "M4B", FileSize: 214_000_000},
	)
	chapSeedTitle(baum, "The Marvelous Land of Oz", "236094", "the-marvelous-land-of-oz",
		"A boy named Tip runs away from a witch with a pumpkin-headed man he built himself.",
		"1904-07-05", 287, []string{"Fantasy", "Classics"}, "Oz #2",
		chapFormatSeed{Format: bookFormatEbook, BookID: 30, InLibrary: true, Monitored: true, Downloaded: true, Quality: "EPUB", FileSize: 910_000},
	)

	// Invented authors behind the author-import park demo (see
	// chapAuthorImportState): lookup-able metadata records whose library
	// imports never conclude, so any add for their books parks server-side.
	// Their series carry positions like any other, but the library holds no
	// record of either one — an author it never imported cannot have books on
	// disk — so both series stay out of the browse row and out of the authors
	// row. That absence IS the parked state, seen from the library side.
	foxcroft := chapSeedAuthor(9, "Wilhelmina Foxcroft", "9931", "wilhelmina-foxcroft",
		"Victorian novelist of fog-bound coastal mysteries, remembered for the Wintermere trilogy.",
		[]string{"Gothic", "Mystery"}, false, 0)
	thistlewood := chapSeedAuthor(10, "Barnaby Thistlewood", "9954", "barnaby-thistlewood",
		"Serial adventure writer of the river towns; every novel ships with a fold-out map.",
		[]string{"Adventure", "Classics"}, false, 0)

	// Wilhelmina Foxcroft's import is still pending — The Lighthouse at
	// Wintermere backs the seeded "waiting for library" request.
	chapSeedTitle(foxcroft, "The Salt Glass", "60398", "the-salt-glass",
		"A glazier's apprentice grinds a lens that shows the coast as it will be at high tide.",
		"1886-06-19", 302, []string{"Gothic", "Mystery"}, "The Wintermere Chronicles #1",
		chapFormatSeed{Format: bookFormatEbook, BookID: 31, Quality: "EPUB"},
		chapFormatSeed{Format: bookFormatAudiobook, BookID: 32, Quality: "M4B"},
	)
	chapSeedTitle(foxcroft, "The Lighthouse at Wintermere", "60401", "the-lighthouse-at-wintermere",
		"A keeper's daughter charts the wrecks that only ever happen while the lamp is lit.",
		"1889-10-03", 356, []string{"Gothic", "Mystery"}, "The Wintermere Chronicles #2",
		chapFormatSeed{Format: bookFormatEbook, BookID: 16, Quality: "EPUB"},
		chapFormatSeed{Format: bookFormatAudiobook, BookID: 17, Quality: "M4B"},
	)
	// Barnaby Thistlewood's import was declared failed — The Clockwork Ferryman
	// backs the demoted approval-queue row whose verbs are "Try again" and deny.
	chapSeedTitle(thistlewood, "The Ninefold Crossing", "60531", "the-ninefold-crossing",
		"Nine tolls, nine passengers, and a ferryman who has only ever counted to eight.",
		"1891-08-30", 264, []string{"Adventure", "Classics"}, "The Ferryman Cycle #1",
		chapFormatSeed{Format: bookFormatEbook, BookID: 33, Quality: "EPUB"},
		chapFormatSeed{Format: bookFormatAudiobook, BookID: 34, Quality: "M4B"},
	)
	chapSeedTitle(thistlewood, "The Clockwork Ferryman", "60544", "the-clockwork-ferryman",
		"A mechanical ferryman remembers every crossing but one, and a stowaway means to find it.",
		"1894-04-12", 288, []string{"Adventure", "Classics"}, "The Ferryman Cycle #2",
		chapFormatSeed{Format: bookFormatEbook, BookID: 18, Quality: "EPUB"},
		chapFormatSeed{Format: bookFormatAudiobook, BookID: 19, Quality: "M4B"},
	)

	// History: a grabbed+imported pair per downloaded file, staggered into the
	// past, plus one failed audiobook attempt (Treasure Island).
	for i, bookID := range []int{1, 2, 3, 4, 5, 7, 9, 10, 11, 12, 20, 21, 24, 25, 26, 27, 28, 29, 30} {
		hoursAgo := float64(30 + i*41)
		dlID := fmt.Sprintf("SABnzbd_nzo_demo_hist_%d", bookID)
		chapSeedHistory("bookImported", bookID, dlID, hoursAgo)
		chapSeedHistory("grabbed", bookID, dlID, hoursAgo+2.5)
	}
	chapSeedHistory("downloadFailed", 13, "SABnzbd_nzo_demo_hist_13", 49)
	chapSeedHistory("grabbed", 13, "SABnzbd_nzo_demo_hist_13", 52)

	// One live queue item: the Dracula audiobook, mid-download.
	chapLockedEnqueue(6)
}

// ─── Contract hooks (contract.md §7, D8) ────────────────

// bookByForeignID returns the catalog book with the given Chaptarr
// foreignBookId, and whether it exists.
func bookByForeignID(foreignID string) (*DemoBook, bool) {
	chapMu.Lock()
	defer chapMu.Unlock()
	b := chapBooksByFID[foreignID]
	return b, b != nil
}

// allBooks returns every catalog book in seed order (including titles not yet
// in the fake Chaptarr library).
func allBooks() []*DemoBook {
	chapMu.Lock()
	defer chapMu.Unlock()
	out := make([]*DemoBook, len(chapBooks))
	copy(out, chapBooks)
	return out
}

// bookAuthorImportPending reports whether an add for this book would be
// refused because the library's metadata service still owes its author an
// import (pending or declared failed — either way the author is absent and a
// replayed add re-queues the import rather than completing). The request
// domain parks such requests server-side instead of executing them.
func bookAuthorImportPending(foreignID string) bool {
	chapMu.Lock()
	defer chapMu.Unlock()
	b := chapBooksByFID[foreignID]
	if b == nil {
		return false
	}
	return chapAuthorImportState[b.AuthorForeignID] != ""
}

func chapExpandFormats(format string) []string {
	switch format {
	case bookFormatBoth:
		return []string{bookFormatEbook, bookFormatAudiobook}
	case bookFormatEbook, bookFormatAudiobook:
		return []string{format}
	default:
		return []string{}
	}
}

// chaptarrOnBookRequested marks the requested format(s) monitored + live in
// the fake Chaptarr library, seeds a downloading queue item for each missing
// format, and pings arr_queue_changed.
func chaptarrOnBookRequested(foreignID, format string) {
	changed := false
	chapMu.Lock()
	if b := chapBooksByFID[foreignID]; b != nil {
		meta := chapMetaByFID[foreignID]
		for _, f := range chapExpandFormats(format) {
			bf := b.Formats[f]
			if bf == nil {
				continue
			}
			bf.Monitored = true
			if st := chapRecStates[bf.BookID]; st != nil {
				st.InLibrary = true
			}
			chapLockedJoinLibrary(chapAuthorsByID[meta.AuthorID])
			if !bf.Downloaded && !chapLockedQueueHas(bf.BookID) {
				chapLockedEnqueue(bf.BookID)
			}
			changed = true
		}
	}
	chapMu.Unlock()
	if changed {
		wsBroadcast(evtArrQueueChanged, map[string]any{
			"instance_id": instChaptarr, "service_type": serviceChaptarr,
		})
	}
}

// chaptarrOnBookAvailable completes the format(s): the bookfile appears, the
// wanted row clears (Downloaded=true), the queue item is removed, history
// records the import, and the library digest flips. Pings arr_queue_changed.
func chaptarrOnBookAvailable(foreignID, format string) {
	changed := false
	chapMu.Lock()
	if b := chapBooksByFID[foreignID]; b != nil {
		meta := chapMetaByFID[foreignID]
		for _, f := range chapExpandFormats(format) {
			bf := b.Formats[f]
			if bf == nil {
				continue
			}
			chapLockedMakeAvailable(b, meta, f, bf)
			changed = true
		}
	}
	chapMu.Unlock()
	if changed {
		wsBroadcast(evtArrQueueChanged, map[string]any{
			"instance_id": instChaptarr, "service_type": serviceChaptarr,
		})
	}
}

// chapLockedMakeAvailable flips one book record to downloaded. Caller holds
// chapMu.
func chapLockedMakeAvailable(b *DemoBook, meta *chapBookMeta, format string, bf *DemoBookFormat) {
	bf.Monitored = true
	bf.Downloaded = true
	if bf.FileID == 0 {
		bf.FileID = 100 + bf.BookID
	}
	chapLockedJoinLibrary(chapAuthorsByID[meta.AuthorID])
	st := chapRecStates[bf.BookID]
	if st == nil {
		st = &chapRecState{}
		chapRecStates[bf.BookID] = st
	}
	st.InLibrary = true
	if st.Quality == "" {
		if format == bookFormatAudiobook {
			st.Quality = "M4B"
		} else {
			st.Quality = "EPUB"
		}
	}
	if st.FileSize == 0 {
		if format == bookFormatAudiobook {
			st.FileSize = 384_000_000
		} else {
			st.FileSize = 1_500_000
		}
	}
	author := chapAuthorsByID[meta.AuthorID]
	authorPath := "/books/" + b.AuthorName
	if author != nil {
		authorPath = author.Path
	}
	st.FilePath = fmt.Sprintf("%s/%s/%s (%d).%s", authorPath, b.Title, b.Title, b.Year, strings.ToLower(st.Quality))
	st.DateAdded = time.Now()
	// Drop any queue items for this record and log the import.
	downloadID := fmt.Sprintf("SABnzbd_nzo_demo_book_%d", bf.BookID)
	kept := chapQueue[:0]
	for _, item := range chapQueue {
		if item.BookID == bf.BookID {
			downloadID = item.DownloadID
			continue
		}
		kept = append(kept, item)
	}
	chapQueue = kept
	chapSeedHistoryNow("bookImported", bf.BookID, downloadID)
}

func chapLockedQueueHas(bookID int) bool {
	for _, item := range chapQueue {
		if item.BookID == bookID {
			return true
		}
	}
	return false
}

// ─── Library browse views (authors / series) ────────────

// chapAuthorView is one author the fake Chaptarr library holds book records
// for, joined to the distinct titles behind them.
//
// Titles are reduced the way the ownership digest reduces them — an ebook and
// an audiobook sharing a foreignBookId are one title — because Chaptarr's own
// author statistics count records, which double-counts exactly the titles a
// requester is most likely to own in both formats.
type chapAuthorView struct {
	ForeignID string
	Name      string
	Image     string
	Added     time.Time
	Titles    []*DemoBook // distinct titles, seed order
	Available int         // how many of Titles have a file in any format
}

// chapSeriesView is one series the library holds book records of. Positions
// are keyed by foreignBookId — the same key the titles are grouped by — so a
// title the series states no position for keeps an empty one rather than
// borrowing another's.
type chapSeriesView struct {
	Name      string
	Titles    []*DemoBook // distinct titles, seed order
	Positions map[string]string
	Available int
}

// chapAuthorImagePath is the arr-relative author portrait path. Chaptarr files
// author images at /MediaCover/{authorId}/<file> — there is no Authors/
// subtree — and the app resolves the relative path through the authenticated
// instance proxy, never against the arr's own origin.
func chapAuthorImagePath(authorID int) string {
	return fmt.Sprintf("/MediaCover/%d/poster.png", authorID)
}

// chapLibraryAuthors returns every author of the fake Chaptarr library, in
// seed order.
//
// An author the library holds no book record for is omitted: the browse row
// exists to be opened, and such an author opens onto an empty page. That is
// exactly what a pending or failed metadata import leaves behind (see
// chapAuthorImportState), so it is a state to omit rather than render as an
// empty shelf.
func chapLibraryAuthors() []chapAuthorView {
	chapMu.Lock()
	defer chapMu.Unlock()
	out := []chapAuthorView{}
	for _, a := range chapAuthors {
		if !a.InLibrary {
			continue
		}
		if v, ok := chapLockedAuthorView(a); ok {
			out = append(out, v)
		}
	}
	return out
}

// chapLibraryAuthorByForeignID returns one library author's view, and whether
// the library holds them at all.
func chapLibraryAuthorByForeignID(foreignID string) (chapAuthorView, bool) {
	chapMu.Lock()
	defer chapMu.Unlock()
	for _, a := range chapAuthors {
		if !a.InLibrary || a.ForeignID != foreignID {
			continue
		}
		return chapLockedAuthorView(a)
	}
	return chapAuthorView{}, false
}

// chapLockedAuthorView joins one author to the library's records. Caller holds
// chapMu.
func chapLockedAuthorView(a *chapAuthorRec) (chapAuthorView, bool) {
	v := chapAuthorView{
		ForeignID: a.ForeignID, Name: a.Name,
		Image: chapAuthorImagePath(a.ID), Added: a.Added,
		Titles: []*DemoBook{},
	}
	for _, b := range chapBooks {
		if chapMetaByFID[b.ForeignID].AuthorID != a.ID {
			continue
		}
		held, downloaded := chapLockedTitleState(b)
		if !held {
			continue
		}
		v.Titles = append(v.Titles, b)
		if downloaded {
			v.Available++
		}
	}
	return v, len(v.Titles) > 0
}

// chapLibrarySeries returns every series the library holds book records of, in
// seed order — including the ones it holds no file of, which the browse row
// drops and the detail page still answers for.
func chapLibrarySeries() []chapSeriesView {
	chapMu.Lock()
	defer chapMu.Unlock()
	out := []chapSeriesView{}
	index := map[string]int{}
	for _, b := range chapBooks {
		name, position := chapParseSeriesTitle(chapMetaByFID[b.ForeignID].SeriesTitle)
		if name == "" {
			continue
		}
		held, downloaded := chapLockedTitleState(b)
		if !held {
			continue
		}
		key := strings.ToLower(name)
		i, seen := index[key]
		if !seen {
			out = append(out, chapSeriesView{
				Name: name, Titles: []*DemoBook{}, Positions: map[string]string{},
			})
			i = len(out) - 1
			index[key] = i
		}
		out[i].Titles = append(out[i].Titles, b)
		if position != "" {
			out[i].Positions[b.ForeignID] = position
		}
		if downloaded {
			out[i].Available++
		}
	}
	return out
}

// chapLibrarySeriesByName returns one series' view. The match is
// case-insensitive because the name IS the identity — Chaptarr stores no
// library-wide series record to carry an id.
func chapLibrarySeriesByName(name string) (chapSeriesView, bool) {
	wanted := strings.TrimSpace(name)
	for _, v := range chapLibrarySeries() {
		if strings.EqualFold(v.Name, wanted) {
			return v, true
		}
	}
	return chapSeriesView{}, false
}

// chapLockedTitleState reports whether the library holds any record of this
// title, and whether any of those records has a file. Caller holds chapMu.
func chapLockedTitleState(b *DemoBook) (held, downloaded bool) {
	for _, format := range []string{bookFormatEbook, bookFormatAudiobook} {
		bf := b.Formats[format]
		if bf == nil {
			continue
		}
		if st := chapRecStates[bf.BookID]; st == nil || !st.InLibrary {
			continue
		}
		held = true
		if bf.Downloaded {
			downloaded = true
		}
	}
	return held, downloaded
}

// chapLockedJoinLibrary pulls an author into the library, stamping when they
// arrived so the authors row's "date added" order has a date for them. An
// author already in the library keeps the date they arrived with — a second
// book by them is not a second arrival. Caller holds chapMu.
func chapLockedJoinLibrary(a *chapAuthorRec) {
	if a == nil || a.InLibrary {
		return
	}
	a.InLibrary = true
	a.Added = time.Now()
}

// chapParseSeriesTitle splits a Chaptarr seriesTitle ("Sherlock Holmes #3")
// into the series name and the position within it.
//
// The split is on the LAST " #" so a series whose own name contains one keeps
// it. A title with no " #" is a series that states no position, not a title
// with a blank series name.
func chapParseSeriesTitle(raw string) (name, position string) {
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
