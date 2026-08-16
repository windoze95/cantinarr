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
}

// chapBookMeta supplements the shared DemoBook with Chaptarr-shape metadata
// (contract rule: never extend types.go — keep extras in domain maps).
type chapBookMeta struct {
	ForeignID   string
	TitleSlug   string
	ReleaseDate string // "YYYY-MM-DD"
	Genres      []string
	PageCount   int
	AuthorID    int
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

func chapSeedAuthor(id int, name, foreignID, slug, overview string, genres []string, inLibrary bool) *chapAuthorRec {
	a := &chapAuthorRec{
		ID: id, Name: name, ForeignID: foreignID, TitleSlug: slug,
		Overview: overview, Path: "/books/" + name, Genres: genres,
		InLibrary: inLibrary,
	}
	chapAuthors = append(chapAuthors, a)
	chapAuthorsByID[id] = a
	return a
}

func chapSeedTitle(a *chapAuthorRec, title, foreignID, slug, overview, release string, pages int, genres []string, formats ...chapFormatSeed) {
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

	austen := chapSeedAuthor(1, "Jane Austen", "1265", "jane-austen",
		"English novelist (1775–1817) known for her wit and social observation across six major novels.",
		[]string{"Classics", "Romance"}, true)
	melville := chapSeedAuthor(2, "Herman Melville", "1624", "herman-melville",
		"American novelist and poet (1819–1891), author of sea narratives crowned by Moby-Dick.",
		[]string{"Classics", "Adventure"}, true)
	stoker := chapSeedAuthor(3, "Bram Stoker", "6988", "bram-stoker",
		"Irish author (1847–1912) best remembered for the Gothic landmark Dracula.",
		[]string{"Horror", "Gothic"}, true)
	shelley := chapSeedAuthor(4, "Mary Shelley", "11139", "mary-shelley",
		"English novelist (1797–1851) whose Frankenstein founded modern science fiction.",
		[]string{"Horror", "Science Fiction"}, true)
	doyle := chapSeedAuthor(5, "Arthur Conan Doyle", "2448", "arthur-conan-doyle",
		"British writer (1859–1930), creator of the consulting detective Sherlock Holmes.",
		[]string{"Mystery", "Crime"}, true)
	wells := chapSeedAuthor(6, "H. G. Wells", "880", "h-g-wells",
		"English author (1866–1946), a father of science fiction: time travel, invasion, invisibility.",
		[]string{"Science Fiction"}, true)
	stevenson := chapSeedAuthor(7, "Robert Louis Stevenson", "854", "robert-louis-stevenson",
		"Scottish novelist (1850–1894) of adventure and duality: Treasure Island, Jekyll and Hyde.",
		[]string{"Adventure", "Classics"}, true)
	carroll := chapSeedAuthor(8, "Lewis Carroll", "8164", "lewis-carroll",
		"English author and mathematician (1832–1898), creator of the Alice books.",
		[]string{"Fantasy", "Classics"}, false)

	// Fully downloaded in both formats.
	chapSeedTitle(austen, "Pride and Prejudice", "1885", "pride-and-prejudice",
		"Elizabeth Bennet navigates manners, marriage, and Mr. Darcy in Regency England.",
		"1813-01-28", 432, []string{"Classics", "Romance"},
		chapFormatSeed{Format: bookFormatEbook, BookID: 1, InLibrary: true, Monitored: true, Downloaded: true, Quality: "EPUB", FileSize: 1_830_000},
		chapFormatSeed{Format: bookFormatAudiobook, BookID: 2, InLibrary: true, Monitored: true, Downloaded: true, Quality: "M4B", FileSize: 341_000_000},
	)
	// Both formats downloaded; the PDF ebook is below cutoff (wanted/cutoff row).
	chapSeedTitle(melville, "Moby-Dick", "153747", "moby-dick",
		"Captain Ahab drags the Pequod across the oceans in pursuit of the white whale.",
		"1851-10-18", 720, []string{"Classics", "Adventure", "Sea Stories"},
		chapFormatSeed{Format: bookFormatEbook, BookID: 3, InLibrary: true, Monitored: true, Downloaded: true, Quality: "PDF", FileSize: 6_120_000},
		chapFormatSeed{Format: bookFormatAudiobook, BookID: 4, InLibrary: true, Monitored: true, Downloaded: true, Quality: "MP3", FileSize: 612_000_000},
	)
	// Ebook downloaded; audiobook monitored, not downloaded — currently in the queue.
	chapSeedTitle(stoker, "Dracula", "17245", "dracula",
		"An epistolary Gothic chase from a Transylvanian castle to the streets of London.",
		"1897-05-26", 418, []string{"Horror", "Gothic"},
		chapFormatSeed{Format: bookFormatEbook, BookID: 5, InLibrary: true, Monitored: true, Downloaded: true, Quality: "EPUB", FileSize: 1_240_000},
		chapFormatSeed{Format: bookFormatAudiobook, BookID: 6, InLibrary: true, Monitored: true, Downloaded: false, Quality: "M4B"},
	)
	// Ebook-only in the library; the audiobook edition exists but is untracked
	// (requestable / "track missing format" demo).
	chapSeedTitle(shelley, "Frankenstein", "18490", "frankenstein",
		"Victor Frankenstein animates a creature and reaps the consequences of abandoning it.",
		"1818-01-01", 280, []string{"Horror", "Science Fiction", "Gothic"},
		chapFormatSeed{Format: bookFormatEbook, BookID: 7, InLibrary: true, Monitored: true, Downloaded: true, Quality: "EPUB", FileSize: 990_000},
		chapFormatSeed{Format: bookFormatAudiobook, BookID: 8, Quality: "M4B"},
	)
	// Fully downloaded in both formats.
	chapSeedTitle(doyle, "The Adventures of Sherlock Holmes", "3590", "the-adventures-of-sherlock-holmes",
		"Twelve cases from Baker Street, narrated by the ever-loyal Dr. Watson.",
		"1892-10-14", 307, []string{"Mystery", "Short Stories"},
		chapFormatSeed{Format: bookFormatEbook, BookID: 9, InLibrary: true, Monitored: true, Downloaded: true, Quality: "EPUB", FileSize: 1_410_000},
		chapFormatSeed{Format: bookFormatAudiobook, BookID: 10, InLibrary: true, Monitored: true, Downloaded: true, Quality: "M4B", FileSize: 402_000_000},
	)
	// Ebook only — no audiobook edition exists for this title at all.
	chapSeedTitle(wells, "The Time Machine", "2493", "the-time-machine",
		"A Victorian inventor rides his machine to the year 802,701 and beyond.",
		"1895-05-07", 118, []string{"Science Fiction"},
		chapFormatSeed{Format: bookFormatEbook, BookID: 11, InLibrary: true, Monitored: true, Downloaded: true, Quality: "EPUB", FileSize: 640_000},
	)
	// Ebook downloaded; audiobook monitored but missing (wanted/missing row).
	chapSeedTitle(stevenson, "Treasure Island", "295", "treasure-island",
		"Jim Hawkins, a treasure map, and the most charming mutineer in fiction.",
		"1883-11-14", 292, []string{"Adventure", "Classics"},
		chapFormatSeed{Format: bookFormatEbook, BookID: 12, InLibrary: true, Monitored: true, Downloaded: true, Quality: "EPUB", FileSize: 1_050_000},
		chapFormatSeed{Format: bookFormatAudiobook, BookID: 13, InLibrary: true, Monitored: true, Downloaded: false, Quality: "M4B"},
	)
	// Totally absent from the library — requestable end to end.
	chapSeedTitle(carroll, "Alice's Adventures in Wonderland", "13023", "alices-adventures-in-wonderland",
		"Alice follows a hurried white rabbit down a hole into a world of unhinged logic.",
		"1865-11-26", 176, []string{"Fantasy", "Classics"},
		chapFormatSeed{Format: bookFormatEbook, BookID: 14, Quality: "EPUB"},
		chapFormatSeed{Format: bookFormatAudiobook, BookID: 15, Quality: "M4B"},
	)

	// Invented authors behind the author-import park demo (see
	// chapAuthorImportState): lookup-able metadata records whose library
	// imports never conclude, so any add for their books parks server-side.
	foxcroft := chapSeedAuthor(9, "Wilhelmina Foxcroft", "9931", "wilhelmina-foxcroft",
		"Victorian novelist of fog-bound coastal mysteries, remembered for the Wintermere trilogy.",
		[]string{"Gothic", "Mystery"}, false)
	thistlewood := chapSeedAuthor(10, "Barnaby Thistlewood", "9954", "barnaby-thistlewood",
		"Serial adventure writer of the river towns; every novel ships with a fold-out map.",
		[]string{"Adventure", "Classics"}, false)

	// Wilhelmina Foxcroft's import is still pending — her book backs the
	// seeded "waiting for library" request.
	chapSeedTitle(foxcroft, "The Lighthouse at Wintermere", "60401", "the-lighthouse-at-wintermere",
		"A keeper's daughter charts the wrecks that only ever happen while the lamp is lit.",
		"1889-10-03", 356, []string{"Gothic", "Mystery"},
		chapFormatSeed{Format: bookFormatEbook, BookID: 16, Quality: "EPUB"},
		chapFormatSeed{Format: bookFormatAudiobook, BookID: 17, Quality: "M4B"},
	)
	// Barnaby Thistlewood's import was declared failed — his book backs the
	// demoted approval-queue row whose verbs are "Try again" and deny.
	chapSeedTitle(thistlewood, "The Clockwork Ferryman", "60544", "the-clockwork-ferryman",
		"A mechanical ferryman remembers every crossing but one, and a stowaway means to find it.",
		"1894-04-12", 288, []string{"Adventure", "Classics"},
		chapFormatSeed{Format: bookFormatEbook, BookID: 18, Quality: "EPUB"},
		chapFormatSeed{Format: bookFormatAudiobook, BookID: 19, Quality: "M4B"},
	)

	// History: a grabbed+imported pair per downloaded file, staggered into the
	// past, plus one failed audiobook attempt (Treasure Island).
	for i, bookID := range []int{1, 2, 3, 4, 5, 7, 9, 10, 11, 12} {
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
			if a := chapAuthorsByID[meta.AuthorID]; a != nil {
				a.InLibrary = true
			}
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
	if a := chapAuthorsByID[meta.AuthorID]; a != nil {
		a.InLibrary = true
	}
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
