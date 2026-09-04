// data_music.go — the public-domain music dataset behind the fake Lidarr
// (D9), plus the cross-domain hooks the request domain calls (contract.md §7):
// albumByForeignID, allAlbums, lidCanonicalForeignID, lidarrAlbumLiveStatus,
// lidarrOnAlbumRequested, lidarrOnAlbumDownloading, lidarrOnAlbumAvailable.
//
// Content rule: real historical acts whose recordings are public domain in
// the US (everything fixed before 1926), collected on compilation albums
// written for the demo; release-group dates are reissue dates so the calendar
// and album years read sanely. Ids are deterministic synthetic UUIDs — the app
// treats foreignAlbumId/foreignArtistId as opaque strings, and a real MBID the
// demo cannot verify would be a false claim of truth. The one invented act
// (The Harbor Street Quartet) has no record anywhere: it exists only as the
// seeded request Lidarr could not match.
//
// Everything here is guarded by lidMu (domain-local; never stateMu). Seeding
// happens in init() with pure data only — no state accessors.
package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ─── Domain-local types ─────────────────────────────────

// DemoArtist is one Lidarr-shaped artist. InLibrary=false artists exist only
// in the lookup corpus until a request pulls them into the library.
type DemoArtist struct {
	ID         int
	Name       string
	ForeignID  string
	Overview   string
	ArtistType string // "Person" | "Group"
	Genres     []string
	Path       string
	InLibrary  bool
	// Added is when the artist entered the library. Zero when the record
	// carries no date: the artists row omits it and sorts it last rather than
	// reading a missing date as the beginning of time.
	Added time.Time
}

// DemoTrack is one track of an album plus its on-disk file, when any.
type DemoTrack struct {
	ID        int
	AlbumID   int
	Number    int
	Title     string
	Duration  int // milliseconds
	FileID    int // trackfile id; 0 = no file on disk
	FilePath  string
	FileSize  int
	Quality   string // the file's quality ("FLAC" / "MP3-320") when FileID > 0
	DateAdded time.Time
}

// DemoAlbum is one Lidarr-shaped album (a MusicBrainz release group).
// InLibrary=false albums exist only in the lookup corpus.
type DemoAlbum struct {
	ID             int
	ArtistID       int
	Title          string
	ForeignID      string
	Overview       string
	ReleaseDate    string // "YYYY-MM-DD"
	AlbumType      string
	SecondaryTypes []string
	Genres         []string
	InLibrary      bool
	Monitored      bool
	Tracks         []*DemoTrack
}

// lidStatusMessage is one grouped warning Lidarr attaches to a queue item.
type lidStatusMessage struct {
	Title    string
	Messages []string
}

// lidQueueItem is one row of the fake Lidarr download queue.
type lidQueueItem struct {
	ID                    int
	ArtistID              int
	AlbumID               int
	Title                 string
	Status                string
	TrackedDownloadStatus string
	TrackedDownloadState  string
	StatusMessages        []lidStatusMessage
	ErrorMessage          string
	Protocol              string
	Indexer               string
	DownloadClient        string
	Size                  int
	Sizeleft              int
	Timeleft              string
	DownloadID            string
	Quality               string
	TrackFileCount        int
	TrackHasFileCount     int
	Added                 time.Time
}

// lidHistoryRec is one fake Lidarr history event.
type lidHistoryRec struct {
	ID          int
	ArtistID    int
	AlbumID     int
	EventType   string
	SourceTitle string
	Quality     string
	DownloadID  string
	Date        time.Time
	Data        map[string]string // extra data keys (reason, droppedPath, importedPath)
}

// ─── Domain state (guarded by lidMu) ────────────────────

var (
	lidMu sync.Mutex

	lidArtists      []*DemoArtist
	lidArtistsByID  = map[int]*DemoArtist{}
	lidArtistsByFID = map[string]*DemoArtist{}
	lidAlbums       []*DemoAlbum
	lidAlbumsByID   = map[int]*DemoAlbum{}
	lidAlbumsByFID  = map[string]*DemoAlbum{}
	// lidAliases maps a merged release-group id to the canonical id the
	// library files the album under (MusicBrainz merges release groups; the
	// provider answers the old id with the surviving record).
	lidAliases = map[string]string{}

	lidQueue         []*lidQueueItem
	lidHistory       []*lidHistoryRec
	lidNextQueueID   = 4001
	lidNextHistoryID = 6001

	// Album ids whose files are below the profile cutoff (drives GET
	// wanted/cutoff and the seeded bad-copy issue). Neapolitan Songs is MP3
	// under a Lossless profile.
	lidCutoffUnmet = map[int]bool{2: true}

	lidSeedTime = time.Now().UTC()
)

const (
	lidArtistFIDPrefix = "a0000000-d3a0-4000-8000-0000000000"
	lidAlbumFIDPrefix  = "b0000000-d3a0-4000-8000-0000000000"
	// lidAliasAlbum8 is the merged release-group id the provider now answers
	// with album 8 — the stage behind canonical_foreign_id.
	lidAliasAlbum8 = "b0000000-d3a0-4000-8000-000000000108"

	lidIndexerUsenet  = "DemoNZB (Prowlarr)"
	lidIndexerTorrent = "DemoTorrents (Prowlarr)"
	lidDownloadClient = "SABnzbd"
	lidRootFolder     = "/music"

	lidQualityFLAC   = "FLAC"
	lidQualityMP3    = "MP3-320"
	lidQualityFLAC24 = "FLAC 24bit"
)

// lidArtistFID / lidAlbumFID build the deterministic synthetic ids.
func lidArtistFID(n int) string { return fmt.Sprintf("%s%02d", lidArtistFIDPrefix, n) }
func lidAlbumFID(n int) string  { return fmt.Sprintf("%s%02d", lidAlbumFIDPrefix, n) }

// lidTrackID / lidTrackFileID are the numeric id conventions: tracks are
// albumID*100+n, track files 200000+trackID.
func lidTrackID(albumID, n int) int  { return albumID*100 + n }
func lidTrackFileID(trackID int) int { return 200000 + trackID }

// lidPathName makes a title safe for a file path the way Lidarr's default
// naming does: slashes become dashes and colons drop out.
func lidPathName(s string) string {
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, ": ", " - ")
	s = strings.ReplaceAll(s, ":", "")
	return s
}

// lidDotted renders a scene-style release name: every run of non-alphanumeric
// characters becomes one dot (unlike arrDotted, dashes inside years survive
// as separators rather than merging the numbers).
func lidDotted(s string) string {
	var b strings.Builder
	sep := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			if sep && b.Len() > 0 {
				b.WriteByte('.')
			}
			sep = false
			b.WriteRune(r)
		default:
			sep = true
		}
	}
	return b.String()
}

func lidQualityExt(quality string) string {
	if strings.HasPrefix(quality, "MP3") {
		return "mp3"
	}
	return "flac"
}

// lidTrackFileSize is a deterministic per-track size: FLAC ≈ 26–32 MB,
// MP3-320 ≈ 8–10 MB.
func lidTrackFileSize(trackID int, quality string) int {
	if strings.HasPrefix(quality, "MP3") {
		return 8_000_000 + (trackID*7919)%2_500_000
	}
	return 26_000_000 + (trackID*7919)%6_000_000
}

// lidTrackDuration is a deterministic 2:30–4:40 track length in ms.
func lidTrackDuration(trackID int) int {
	return 150_000 + (trackID*104_729)%130_000
}

func lidAlbumCoverPath(albumID int) string {
	return fmt.Sprintf("/mediacover/album/%d/cover.jpg", albumID)
}

func lidArtistImagePath(artistID int) string {
	return fmt.Sprintf("/mediacover/artist/%d/poster.jpg", artistID)
}

// ─── Seeding ────────────────────────────────────────────

func lidSeedArtist(id int, name, overview, artistType string, genres []string, addedDaysAgo float64) *DemoArtist {
	a := &DemoArtist{
		ID: id, Name: name, ForeignID: lidArtistFID(id), Overview: overview,
		ArtistType: artistType, Genres: genres,
		Path: lidRootFolder + "/" + lidPathName(name), InLibrary: true,
	}
	if addedDaysAgo > 0 {
		a.Added = lidSeedTime.Add(-time.Duration(addedDaysAgo * 24 * float64(time.Hour)))
	}
	lidArtists = append(lidArtists, a)
	lidArtistsByID[id] = a
	lidArtistsByFID[a.ForeignID] = a
	return a
}

// lidAlbumSeed is the library state one seeded album starts in.
type lidAlbumSeed struct {
	InLibrary bool
	Monitored bool
	Files     int    // leading tracks holding a file; -1 = every track
	Quality   string // the files' quality
	AddedAgo  time.Duration
}

func lidSeedAlbum(a *DemoArtist, id int, title, overview, release, albumType string, secondary, genres []string, st lidAlbumSeed, tracks ...string) *DemoAlbum {
	if secondary == nil {
		secondary = []string{}
	}
	album := &DemoAlbum{
		ID: id, ArtistID: a.ID, Title: title, ForeignID: lidAlbumFID(id),
		Overview: overview, ReleaseDate: release, AlbumType: albumType,
		SecondaryTypes: secondary, Genres: genres,
		InLibrary: st.InLibrary, Monitored: st.Monitored,
		Tracks: make([]*DemoTrack, 0, len(tracks)),
	}
	quality := st.Quality
	if quality == "" {
		quality = lidQualityFLAC
	}
	for i, name := range tracks {
		n := i + 1
		t := &DemoTrack{
			ID: lidTrackID(id, n), AlbumID: id, Number: n, Title: name,
			Duration: lidTrackDuration(lidTrackID(id, n)),
		}
		if st.Files < 0 || i < st.Files {
			lidGiveFile(a, album, t, quality, lidSeedTime.Add(-st.AddedAgo).Add(time.Duration(i)*7*time.Second))
		}
		album.Tracks = append(album.Tracks, t)
	}
	lidAlbums = append(lidAlbums, album)
	lidAlbumsByID[id] = album
	lidAlbumsByFID[album.ForeignID] = album
	return album
}

// lidGiveFile lands one track's file on disk.
func lidGiveFile(a *DemoArtist, album *DemoAlbum, t *DemoTrack, quality string, at time.Time) {
	t.FileID = lidTrackFileID(t.ID)
	t.Quality = quality
	t.FileSize = lidTrackFileSize(t.ID, quality)
	t.FilePath = fmt.Sprintf("%s/%s/%02d - %s.%s", a.Path, lidPathName(album.Title), t.Number, lidPathName(t.Title), lidQualityExt(quality))
	t.DateAdded = at
}

func lidSeedHistory(eventType string, albumID int, sourceTitle, quality, downloadID string, at time.Time, data map[string]string) {
	album := lidAlbumsByID[albumID]
	if album == nil {
		return
	}
	lidHistory = append(lidHistory, &lidHistoryRec{
		ID: lidNextHistoryID, ArtistID: album.ArtistID, AlbumID: albumID,
		EventType: eventType, SourceTitle: sourceTitle, Quality: quality,
		DownloadID: downloadID, Date: at, Data: data,
	})
	lidNextHistoryID++
}

// lidReleaseName is the scene-style name a grab of this album carries.
func lidReleaseName(album *DemoAlbum, quality string) string {
	artist := lidArtistsByID[album.ArtistID]
	name := album.Title
	if artist != nil {
		name = artist.Name + " " + album.Title
	}
	return lidDotted(name) + "." + strings.ReplaceAll(quality, " ", ".") + "-DEMO"
}

func lidDownloadID(albumID int) string {
	return fmt.Sprintf("SABnzbd_nzo_demo_music_%d", albumID)
}

// lidLockedEnqueue seeds a healthy downloading queue item (plus a "grabbed"
// history record) for a monitored album. Caller holds lidMu.
func lidLockedEnqueue(album *DemoAlbum) *lidQueueItem {
	files, total := lidLockedAlbumCounts(album)
	size := 0
	for _, t := range album.Tracks {
		size += lidTrackFileSize(t.ID, lidQualityFLAC)
	}
	item := &lidQueueItem{
		ID: lidNextQueueID, ArtistID: album.ArtistID, AlbumID: album.ID,
		Title:                 lidReleaseName(album, lidQualityFLAC),
		Status:                "downloading",
		TrackedDownloadStatus: "ok",
		TrackedDownloadState:  "downloading",
		StatusMessages:        []lidStatusMessage{},
		Protocol:              "usenet",
		Indexer:               lidIndexerUsenet,
		DownloadClient:        lidDownloadClient,
		Size:                  size, Sizeleft: size * 13 / 20,
		Timeleft:          "00:07:41",
		DownloadID:        lidDownloadID(album.ID),
		Quality:           lidQualityFLAC,
		TrackFileCount:    total,
		TrackHasFileCount: files,
		Added:             time.Now().UTC(),
	}
	lidNextQueueID++
	lidQueue = append(lidQueue, item)
	lidSeedHistory("grabbed", album.ID, item.Title, lidQualityFLAC, item.DownloadID, time.Now().UTC(), nil)
	return item
}

func init() {
	lidMu.Lock()
	defer lidMu.Unlock()

	// Library artists. addedDaysAgo is staggered so the artists row's "date
	// added" order is visibly its own; the Fisk Jubilee Singers deliberately
	// carry no date, so the row has one record sorting last.
	caruso := lidSeedArtist(1, "Enrico Caruso",
		"Italian operatic tenor (1873–1921) whose Victor recordings, made between 1902 and 1920, were the first to sell in the millions and fixed the sound of the tenor voice for a generation.",
		"Person", []string{"Opera", "Classical"}, 380)
	joplin := lidSeedArtist(2, "Scott Joplin",
		"American composer and pianist (1868–1917), the King of Ragtime. His own playing survives only through the piano rolls he cut in 1916.",
		"Person", []string{"Ragtime"}, 210)
	odjb := lidSeedArtist(3, "Original Dixieland Jass Band",
		"New Orleans quintet whose Victor sides of 1917 were the first jazz records ever issued; their 1917–1921 recordings are the foundation of the recorded jazz repertoire.",
		"Group", []string{"Jazz"}, 41)
	bessie := lidSeedArtist(4, "Bessie Smith",
		"American blues singer (1894–1937), the Empress of the Blues, whose Columbia sides from 1923 onward set the standard for classic blues singing.",
		"Person", []string{"Blues"}, 9)
	fisk := lidSeedArtist(5, "Fisk Jubilee Singers",
		"Vocal ensemble from Fisk University in Nashville, formed in 1871, who brought spirituals to the concert stage and recorded them for Victor between 1909 and 1916.",
		"Group", []string{"Spiritual", "Gospel"}, 0)
	sousa := lidSeedArtist(6, "Sousa's Band",
		"The concert band John Philip Sousa led on tour from 1892, which recorded his marches for Victor across the acoustic era (1900–1920).",
		"Group", []string{"March", "Brass Band"}, 150)

	compilation := []string{"Compilation"}
	all := -1

	// 1 — on disk, complete, FLAC: the plain "Available" album.
	lidSeedAlbum(caruso, 1, "Acoustic Recordings, Vol. 1 (1902–1908)",
		"Ten early Victor sides from Caruso's first American sessions, recorded between 1902 and 1908 and restored from original discs.",
		"2021-03-05", "Album", compilation, []string{"Opera", "Classical"},
		lidAlbumSeed{InLibrary: true, Monitored: true, Files: all, Quality: lidQualityFLAC, AddedAgo: 48 * time.Hour},
		"Vesti la giubba", "Una furtiva lagrima", "Celeste Aida", "La donna è mobile", "Questa o quella",
		"E lucevan le stelle", "M'apparì tutt'amor", "Core 'ngrato", "Santa Lucia", "O sole mio")
	// 2 — on disk, complete, MP3-320 under a Lossless profile: the
	// wanted/cutoff row and the seeded bad-copy issue's target.
	lidSeedAlbum(caruso, 2, "Neapolitan Songs",
		"Eight Neapolitan songs recorded between 1909 and 1920: the popular repertoire Caruso carried alongside opera.",
		"2019-11-15", "Album", compilation, []string{"Classical", "Neapolitan Song"},
		lidAlbumSeed{InLibrary: true, Monitored: true, Files: all, Quality: lidQualityMP3, AddedAgo: 33 * 24 * time.Hour},
		"O sole mio", "Santa Lucia", "Torna a Surriento", "Funiculì, Funiculà", "'A vucchella",
		"Mattinata", "Musica proibita", "Addio a Napoli")
	// 3 — not in the library, in the lookup corpus: requestable end to end.
	lidSeedAlbum(caruso, 3, "Vesti la giubba and Other Arias",
		"Six signature arias from Caruso's 1907–1916 Victor sessions, led by the Pagliacci recording that became the first million-selling disc.",
		"2023-06-02", "Album", compilation, []string{"Opera"},
		lidAlbumSeed{},
		"Vesti la giubba", "Celeste Aida", "Recondita armonia", "Che gelida manina", "Ah! Manon, mi tradisce", "Salut, demeure")
	// 4 — plain owned.
	lidSeedAlbum(joplin, 4, "Piano Rolls, 1916",
		"Six rags played by Joplin himself on Connorized piano rolls in 1916, the only record of his own performances.",
		"2018-09-21", "Album", compilation, []string{"Ragtime"},
		lidAlbumSeed{InLibrary: true, Monitored: true, Files: all, Quality: lidQualityFLAC, AddedAgo: 9 * 24 * time.Hour},
		"Maple Leaf Rag", "Magnetic Rag", "Ole Miss Rag", "Something Doing", "Weeping Willow", "Pleasant Moments")
	// 5 — monitored, no files, released next week: wanted/missing, the
	// calendar's upcoming row, a failed download in history, and user 2's
	// "requested" row.
	lidSeedAlbum(joplin, 5, "Ragtime Classics",
		"Eight of Joplin's best-known rags in new transfers from period rolls and early discs.",
		lidSeedTime.AddDate(0, 0, 9).Format("2006-01-02"), "Album", compilation, []string{"Ragtime"},
		lidAlbumSeed{InLibrary: true, Monitored: true},
		"The Entertainer", "Elite Syncopations", "The Easy Winners", "Solace", "Peacherine Rag",
		"Sunflower Slow Drag", "The Chrysanthemum", "Gladiolus Rag")
	// 6 — monitored, no files, mid-download (queue item 4001).
	lidSeedAlbum(odjb, 6, "Livery Stable Blues: The 1917 Sessions",
		"The band's first Victor session of February 1917 and its follow-ups: the sides that put jazz on record.",
		"2022-02-25", "Album", compilation, []string{"Jazz", "Dixieland"},
		lidAlbumSeed{InLibrary: true, Monitored: true},
		"Livery Stable Blues", "Dixie Jass Band One-Step", "Ostrich Walk", "At the Jazz Band Ball", "Fidgety Feet", "Sensation Rag")
	// 7 — plain owned.
	lidSeedAlbum(odjb, 7, "Tiger Rag and Other Sides (1918–1921)",
		"Eight Victor sides from 1918 to 1921, including the band's Tiger Rag and Clarinet Marmalade.",
		"2020-07-10", "Album", compilation, []string{"Jazz", "Dixieland"},
		lidAlbumSeed{InLibrary: true, Monitored: true, Files: all, Quality: lidQualityFLAC, AddedAgo: 5 * 24 * time.Hour},
		"Tiger Rag", "Bluin' the Blues", "Clarinet Marmalade", "Lazy Daddy", "Skeleton Jangle", "Mournin' Blues", "Palesteena", "Margie")
	// 8 — the newest import (top of Recently Added), the admin's finished
	// request, and the target of the merged-id alias.
	lidSeedAlbum(bessie, 8, "Downhearted Blues: The 1923 Sessions",
		"Bessie Smith's first year on Columbia: ten sides from 1923, opening with the Downhearted Blues that launched her career.",
		"2024-01-12", "Album", compilation, []string{"Blues"},
		lidAlbumSeed{InLibrary: true, Monitored: true, Files: all, Quality: lidQualityFLAC, AddedAgo: 6 * time.Hour},
		"Downhearted Blues", "Gulf Coast Blues", "Aggravatin' Papa", "Beale Street Mama", "Baby Won't You Please Come Home",
		"Oh Daddy Blues", "'Tain't Nobody's Bizness If I Do", "Keeps On A-Rainin'", "Mama's Got the Blues", "Outside of That")
	// 9 — partial: three of eight tracks on disk, a second grab stuck on
	// import (queue item 4002 → Import Doctor), released this week.
	lidSeedAlbum(bessie, 9, "St. Louis Blues: The 1925 Sessions",
		"Eight 1925 Columbia sides, including St. Louis Blues with Louis Armstrong on cornet.",
		lidSeedTime.AddDate(0, 0, -3).Format("2006-01-02"), "Album", compilation, []string{"Blues"},
		lidAlbumSeed{InLibrary: true, Monitored: true, Files: 3, Quality: lidQualityFLAC, AddedAgo: 24 * time.Hour},
		"St. Louis Blues", "Reckless Blues", "Sobbin' Hearted Blues", "Cold in Hand Blues", "You've Been a Good Ole Wagon",
		"Careless Love Blues", "Nashville Women's Blues", "J.C. Holmes Blues")
	// 10 — plain owned; the artist carries no added date.
	lidSeedAlbum(fisk, 10, "Swing Low: Recordings 1909–1916",
		"Eight spirituals from the Fisk Jubilee Singers' Victor sessions between 1909 and 1916.",
		"2019-04-19", "Album", compilation, []string{"Spiritual", "Gospel"},
		lidAlbumSeed{InLibrary: true, Monitored: true, Files: all, Quality: lidQualityFLAC, AddedAgo: 14 * 24 * time.Hour},
		"Swing Low, Sweet Chariot", "Roll, Jordan, Roll", "Steal Away", "Go Down, Moses", "Little David, Play on Your Harp",
		"Ezekiel Saw the Wheel", "Good News, the Chariot's Coming", "Peter, Go Ring dem Bells")
	// 11 — plain owned.
	lidSeedAlbum(sousa, 11, "Marches (Victor Recordings 1900–1920)",
		"Ten Sousa marches as recorded by his own band for Victor across the acoustic era.",
		"2020-11-06", "Album", compilation, []string{"March", "Brass Band"},
		lidAlbumSeed{InLibrary: true, Monitored: true, Files: all, Quality: lidQualityFLAC, AddedAgo: 20 * 24 * time.Hour},
		"The Stars and Stripes Forever", "The Washington Post", "Semper Fidelis", "The Liberty Bell", "El Capitan",
		"Hands Across the Sea", "The Thunderer", "King Cotton", "Manhattan Beach", "The Gladiator")
	// 12 — in the library but unmonitored with no files: a request monitors
	// it in place rather than adding a second record; user 2's denied row.
	lidSeedAlbum(sousa, 12, "Stars and Stripes Forever: Rare Sides",
		"Six less-recorded Sousa marches and character pieces from the band's Victor catalogue.",
		"2022-08-19", "Album", compilation, []string{"March", "Brass Band"},
		lidAlbumSeed{InLibrary: true, Monitored: false},
		"The Fairest of the Fair", "The Invincible Eagle", "Jack Tar", "The Bride Elect", "The Diplomat", "Powhatan's Daughter")

	// The merged release-group id: MusicBrainz folded it into album 8, and
	// the provider answers the old id with the surviving record.
	lidAliases[lidAliasAlbum8] = lidAlbumFID(8)

	// History: a grabbed + trackFileImported pair per file-bearing album at
	// the time its files landed, one past MP3→FLAC upgrade (album 1), one
	// retag (album 11), and a failed grab for Ragtime Classics.
	for _, album := range lidAlbums {
		files, _ := lidLockedAlbumCounts(album)
		if files == 0 {
			continue
		}
		newest := time.Time{}
		quality := lidQualityFLAC
		for _, t := range album.Tracks {
			if t.FileID > 0 && t.DateAdded.After(newest) {
				newest = t.DateAdded
				quality = t.Quality
			}
		}
		release := lidReleaseName(album, quality)
		dlID := fmt.Sprintf("SABnzbd_nzo_demo_hist_%d", album.ID)
		lidSeedHistory("grabbed", album.ID, release, quality, dlID, newest.Add(-150*time.Minute), nil)
		lidSeedHistory("trackFileImported", album.ID, release, quality, dlID, newest, map[string]string{
			"droppedPath":  fmt.Sprintf("/downloads/complete/%s/01 - %s.%s", release, lidPathName(album.Tracks[0].Title), lidQualityExt(quality)),
			"importedPath": album.Tracks[0].FilePath,
		})
	}
	if album := lidAlbumsByID[1]; album != nil {
		lidSeedHistory("trackFileDeleted", 1, lidReleaseName(album, lidQualityMP3), lidQualityMP3, "",
			album.Tracks[0].DateAdded.Add(-40*time.Second), map[string]string{"reason": "Upgrade"})
	}
	if album := lidAlbumsByID[11]; album != nil {
		lidSeedHistory("trackFileRetagged", 11, album.Tracks[0].FilePath, lidQualityFLAC, "",
			lidSeedTime.Add(-19*24*time.Hour), map[string]string{"tagsScrubbed": "false", "diff": "Track title, Album artist"})
	}
	if album := lidAlbumsByID[5]; album != nil {
		release := lidReleaseName(album, lidQualityFLAC)
		lidSeedHistory("grabbed", 5, release, lidQualityFLAC, "SABnzbd_nzo_demo_hist_5", lidSeedTime.Add(-52*time.Hour), nil)
		lidSeedHistory("downloadFailed", 5, release, lidQualityFLAC, "SABnzbd_nzo_demo_hist_5", lidSeedTime.Add(-49*time.Hour),
			map[string]string{"message": "Download failed: the NZB was removed by the indexer before the client finished it"})
	}

	// Queue: a healthy download for Livery Stable Blues, then a completed
	// download for St. Louis Blues that Lidarr could not import (the Import
	// Doctor stage).
	if album := lidAlbumsByID[6]; album != nil {
		item := lidLockedEnqueue(album)
		item.Added = lidSeedTime.Add(-25 * time.Minute)
		lidHistory[len(lidHistory)-1].Date = item.Added
	}
	if album := lidAlbumsByID[9]; album != nil {
		files, total := lidLockedAlbumCounts(album)
		size := 0
		for _, t := range album.Tracks {
			size += lidTrackFileSize(t.ID, lidQualityFLAC)
		}
		title := "Bessie.Smith.St.Louis.Blues.1925.FLAC-DEMO"
		item := &lidQueueItem{
			ID: lidNextQueueID, ArtistID: album.ArtistID, AlbumID: album.ID,
			Title:                 title,
			Status:                "completed",
			TrackedDownloadStatus: "warning",
			TrackedDownloadState:  "importBlocked",
			StatusMessages: []lidStatusMessage{{
				Title:    title,
				Messages: []string{"No files found are eligible for import in /downloads/complete/" + title},
			}},
			Protocol:       "usenet",
			Indexer:        lidIndexerUsenet,
			DownloadClient: lidDownloadClient,
			Size:           size, Sizeleft: 0,
			Timeleft:          "00:00:00",
			DownloadID:        lidDownloadID(album.ID),
			Quality:           lidQualityFLAC,
			TrackFileCount:    total,
			TrackHasFileCount: files,
			Added:             lidSeedTime.Add(-70 * time.Minute),
		}
		lidNextQueueID++
		lidQueue = append(lidQueue, item)
		lidSeedHistory("grabbed", album.ID, title, lidQualityFLAC, item.DownloadID, item.Added, nil)
	}
}

// ─── Locked helpers ─────────────────────────────────────

// lidLockedAlbumByFID resolves a foreignAlbumId (alias-aware) to its album.
// Caller holds lidMu.
func lidLockedAlbumByFID(fid string) *DemoAlbum {
	if canonical, ok := lidAliases[fid]; ok {
		fid = canonical
	}
	return lidAlbumsByFID[fid]
}

// lidLockedAlbumCounts reports (files on disk, tracks) for one album.
func lidLockedAlbumCounts(album *DemoAlbum) (files, total int) {
	for _, t := range album.Tracks {
		total++
		if t.FileID > 0 {
			files++
		}
	}
	return files, total
}

func lidLockedAlbumSize(album *DemoAlbum) int {
	size := 0
	for _, t := range album.Tracks {
		if t.FileID > 0 {
			size += t.FileSize
		}
	}
	return size
}

// lidLockedAlbumComplete mirrors the server's albumComplete: every track on
// disk (a record with files but no tracks counts as complete).
func lidLockedAlbumComplete(album *DemoAlbum) bool {
	files, total := lidLockedAlbumCounts(album)
	if files <= 0 {
		return false
	}
	return total <= 0 || files >= total
}

// lidQueueItemHealthy mirrors the server's musicQueueItemDownloading: an item
// counts as downloading only when nothing about it reads as stuck.
func lidQueueItemHealthy(it *lidQueueItem) bool {
	state := strings.ToLower(it.TrackedDownloadStatus + " " + it.TrackedDownloadState + " " + it.Status)
	for _, token := range []string{"paused", "unavailable", "problem", "warning", "error", "failed", "blocked", "stalled"} {
		if strings.Contains(state, token) {
			return false
		}
	}
	return true
}

func lidLockedQueueFor(albumID int) *lidQueueItem {
	for _, it := range lidQueue {
		if it.AlbumID == albumID {
			return it
		}
	}
	return nil
}

// lidLockedLiveStatus is the live projection for one record: complete →
// available; healthy queue item → downloading; any file → partial; monitored
// → requested; "" when the library holds no claim (not in the library, or an
// unmonitored record with nothing on disk). Caller holds lidMu.
func lidLockedLiveStatus(album *DemoAlbum) string {
	if album == nil || !album.InLibrary {
		return ""
	}
	if lidLockedAlbumComplete(album) {
		return statusAvailable
	}
	if it := lidLockedQueueFor(album.ID); it != nil && lidQueueItemHealthy(it) {
		return statusDownloading
	}
	if files, _ := lidLockedAlbumCounts(album); files > 0 {
		return statusPartial
	}
	if album.Monitored {
		return statusRequested
	}
	return ""
}

// lidLockedJoinLibrary pulls an artist into the library, stamping when they
// arrived. An artist already in the library keeps the date they arrived with.
func lidLockedJoinLibrary(a *DemoArtist) {
	if a == nil || a.InLibrary {
		return
	}
	a.InLibrary = true
	a.Added = time.Now().UTC()
}

// lidLockedLandTracks lands files for the album's tracks: every track in ids,
// or every missing track when ids is nil. Returns how many landed.
func lidLockedLandTracks(album *DemoAlbum, ids map[int]bool, now time.Time) int {
	artist := lidArtistsByID[album.ArtistID]
	if artist == nil {
		return 0
	}
	landed := 0
	for i, t := range album.Tracks {
		if t.FileID > 0 {
			continue
		}
		if ids != nil && !ids[t.ID] {
			continue
		}
		lidGiveFile(artist, album, t, lidQualityFLAC, now.Add(time.Duration(i)*time.Second))
		landed++
	}
	return landed
}

// lidLockedFinishImport drops the album's queue item and logs the import once
// files landed. Returns whether anything changed.
func lidLockedFinishImport(album *DemoAlbum, landed int) bool {
	if landed == 0 {
		return false
	}
	downloadID := lidDownloadID(album.ID)
	sourceTitle := lidReleaseName(album, lidQualityFLAC)
	kept := lidQueue[:0]
	for _, it := range lidQueue {
		if it.AlbumID == album.ID {
			downloadID, sourceTitle = it.DownloadID, it.Title
			continue
		}
		kept = append(kept, it)
	}
	lidQueue = kept
	first := ""
	for _, t := range album.Tracks {
		if t.FileID > 0 {
			first = t.FilePath
			break
		}
	}
	lidSeedHistory("trackFileImported", album.ID, sourceTitle, lidQualityFLAC, downloadID, time.Now().UTC(), map[string]string{
		"droppedPath":  fmt.Sprintf("/downloads/complete/%s/%s", sourceTitle, lidPathName(album.Tracks[0].Title)+".flac"),
		"importedPath": first,
	})
	return true
}

func lidArrQueueChangedPing() {
	wsBroadcast(evtArrQueueChanged, map[string]any{
		"instance_id": instLidarr, "service_type": serviceLidarr,
	})
}

// ─── Contract hooks (contract.md §7, D9) ────────────────

// albumByForeignID returns a copy of the album filed under the given
// foreignAlbumId (alias-aware), and whether it exists in the corpus.
func albumByForeignID(foreignID string) (*DemoAlbum, bool) {
	lidMu.Lock()
	defer lidMu.Unlock()
	a := lidLockedAlbumByFID(strings.TrimSpace(foreignID))
	if a == nil {
		return nil, false
	}
	cp := *a
	return &cp, true
}

// allAlbums returns copies of every corpus album in seed order (including
// titles not in the fake Lidarr library).
func allAlbums() []*DemoAlbum {
	lidMu.Lock()
	defer lidMu.Unlock()
	out := make([]*DemoAlbum, 0, len(lidAlbums))
	for _, a := range lidAlbums {
		cp := *a
		out = append(out, &cp)
	}
	return out
}

// lidCanonicalForeignID resolves a merged release-group id to the id the
// library files the album under; aliased reports whether it changed.
func lidCanonicalForeignID(foreignID string) (canonical string, aliased bool) {
	foreignID = strings.TrimSpace(foreignID)
	lidMu.Lock()
	defer lidMu.Unlock()
	if canonical, ok := lidAliases[foreignID]; ok {
		return canonical, true
	}
	return foreignID, false
}

// lidAlbumInLibrary reports whether the library holds a record for the id.
func lidAlbumInLibrary(foreignID string) bool {
	lidMu.Lock()
	defer lidMu.Unlock()
	a := lidLockedAlbumByFID(strings.TrimSpace(foreignID))
	return a != nil && a.InLibrary
}

// lidarrAlbumLiveStatus is the live library projection for one album (REST
// spellings): available / downloading / partial / requested, or "" when the
// library holds no claim.
func lidarrAlbumLiveStatus(foreignID string) string {
	lidMu.Lock()
	defer lidMu.Unlock()
	return lidLockedLiveStatus(lidLockedAlbumByFID(strings.TrimSpace(foreignID)))
}

// lidarrOnAlbumRequested marks the album monitored and live in the library
// (the artist joins the library if needed) and pings arr_queue_changed. It
// never creates a second record: an existing unmonitored record is monitored
// in place. Reports whether the album exists.
func lidarrOnAlbumRequested(foreignID string) bool {
	lidMu.Lock()
	a := lidLockedAlbumByFID(strings.TrimSpace(foreignID))
	if a == nil {
		lidMu.Unlock()
		return false
	}
	a.InLibrary = true
	a.Monitored = true
	lidLockedJoinLibrary(lidArtistsByID[a.ArtistID])
	lidMu.Unlock()
	lidArrQueueChangedPing()
	return true
}

// lidarrOnAlbumDownloading enqueues a healthy download for the album (with
// its grabbed history record) and pings arr_queue_changed.
func lidarrOnAlbumDownloading(foreignID string) {
	lidMu.Lock()
	a := lidLockedAlbumByFID(strings.TrimSpace(foreignID))
	if a == nil {
		lidMu.Unlock()
		return
	}
	if lidLockedQueueFor(a.ID) == nil {
		lidLockedEnqueue(a)
	}
	lidMu.Unlock()
	lidArrQueueChangedPing()
}

// lidarrQueueAdvance moves the album's queue item along: sizeleft shrinks to
// the given remaining fraction and the ETA follows. Pings arr_queue_changed.
func lidarrQueueAdvance(foreignID string, remaining float64) {
	lidMu.Lock()
	a := lidLockedAlbumByFID(strings.TrimSpace(foreignID))
	if a == nil {
		lidMu.Unlock()
		return
	}
	if it := lidLockedQueueFor(a.ID); it != nil && lidQueueItemHealthy(it) {
		it.Sizeleft = int(float64(it.Size) * remaining)
		secs := int(remaining * 21)
		it.Timeleft = fmt.Sprintf("00:00:%02d", secs)
	}
	lidMu.Unlock()
	lidArrQueueChangedPing()
}

// lidarrOnAlbumAvailable completes the album: every track gains a file, the
// queue item is dropped, history records the import, and the library digests
// flip. Pings arr_queue_changed.
func lidarrOnAlbumAvailable(foreignID string) {
	lidMu.Lock()
	a := lidLockedAlbumByFID(strings.TrimSpace(foreignID))
	if a == nil {
		lidMu.Unlock()
		return
	}
	a.InLibrary = true
	a.Monitored = true
	lidLockedJoinLibrary(lidArtistsByID[a.ArtistID])
	landed := lidLockedLandTracks(a, nil, time.Now().UTC())
	changed := lidLockedFinishImport(a, landed)
	lidMu.Unlock()
	if changed {
		lidArrQueueChangedPing()
	}
}

// lidarrOnAlbumSearch is the fake's AlbumSearch: a monitored library album
// that is missing files (or below cutoff) and not already queued gains a
// healthy download. Reports whether anything was enqueued.
func lidarrOnAlbumSearch(albumID int) bool {
	lidMu.Lock()
	changed := lidLockedSearchAlbum(lidAlbumsByID[albumID])
	lidMu.Unlock()
	if changed {
		lidArrQueueChangedPing()
	}
	return changed
}

// lidLockedSearchAlbum enqueues one album when a search would grab something.
func lidLockedSearchAlbum(a *DemoAlbum) bool {
	if a == nil || !a.InLibrary || !a.Monitored {
		return false
	}
	if lidLockedQueueFor(a.ID) != nil {
		return false
	}
	if lidLockedAlbumComplete(a) && !lidCutoffUnmet[a.ID] {
		return false
	}
	lidLockedEnqueue(a)
	return true
}

// lidarrOnArtistSearch is the fake's ArtistSearch: every monitored missing
// album of the artist is enqueued.
func lidarrOnArtistSearch(artistID int) bool {
	lidMu.Lock()
	changed := false
	for _, a := range lidAlbums {
		if a.ArtistID != artistID {
			continue
		}
		if lidLockedSearchAlbum(a) {
			changed = true
		}
	}
	lidMu.Unlock()
	if changed {
		lidArrQueueChangedPing()
	}
	return changed
}

// ─── Library views (the Cantinarr-native digests) ───────

// lidTitleView is one library album reduced the way the ownership digest
// reduces it.
type lidTitleView struct {
	AlbumID         int
	ArtistID        int
	Title           string
	Artist          string
	ArtistForeignID string
	ForeignID       string
	Cover           string
	Year            int
	Monitored       bool
	Downloaded      bool
	Complete        bool
	NewestFile      time.Time
}

// lidArtistView is one artist the library holds album records for.
type lidArtistView struct {
	ID             int
	ForeignID      string
	Name           string
	Image          string
	Added          time.Time
	AlbumCount     int
	AvailableCount int
	Titles         []lidTitleView
}

func lidLockedTitleView(a *DemoAlbum) lidTitleView {
	files, _ := lidLockedAlbumCounts(a)
	v := lidTitleView{
		AlbumID: a.ID, ArtistID: a.ArtistID, Title: a.Title, ForeignID: a.ForeignID,
		Cover: lidAlbumCoverPath(a.ID), Year: yearOf(a.ReleaseDate),
		Monitored: a.Monitored, Downloaded: files > 0, Complete: lidLockedAlbumComplete(a),
	}
	if artist := lidArtistsByID[a.ArtistID]; artist != nil {
		v.Artist = artist.Name
		v.ArtistForeignID = artist.ForeignID
	}
	for _, t := range a.Tracks {
		if t.FileID > 0 && t.DateAdded.After(v.NewestFile) {
			v.NewestFile = t.DateAdded
		}
	}
	return v
}

// lidLibraryTitles returns one view per library record, in seed order.
func lidLibraryTitles() []lidTitleView {
	lidMu.Lock()
	defer lidMu.Unlock()
	out := []lidTitleView{}
	for _, a := range lidAlbums {
		if a.InLibrary {
			out = append(out, lidLockedTitleView(a))
		}
	}
	return out
}

func lidLockedArtistView(artist *DemoArtist) (lidArtistView, bool) {
	v := lidArtistView{
		ID: artist.ID, ForeignID: artist.ForeignID, Name: artist.Name,
		Image: lidArtistImagePath(artist.ID), Added: artist.Added, Titles: []lidTitleView{},
	}
	for _, a := range lidAlbums {
		if a.ArtistID != artist.ID || !a.InLibrary {
			continue
		}
		t := lidLockedTitleView(a)
		v.Titles = append(v.Titles, t)
		v.AlbumCount++
		if t.Complete {
			v.AvailableCount++
		}
	}
	return v, len(v.Titles) > 0
}

// lidLibraryArtists returns every artist the library holds album records
// for, in seed order. An artist with no library album is omitted: the row
// exists to be opened, and such an artist opens onto an empty page.
func lidLibraryArtists() []lidArtistView {
	lidMu.Lock()
	defer lidMu.Unlock()
	out := []lidArtistView{}
	for _, artist := range lidArtists {
		if !artist.InLibrary {
			continue
		}
		if v, ok := lidLockedArtistView(artist); ok {
			out = append(out, v)
		}
	}
	return out
}

// lidLibraryArtistByForeignID returns one library artist's view.
func lidLibraryArtistByForeignID(foreignID string) (lidArtistView, bool) {
	lidMu.Lock()
	defer lidMu.Unlock()
	artist := lidArtistsByFID[strings.TrimSpace(foreignID)]
	if artist == nil || !artist.InLibrary {
		return lidArtistView{}, false
	}
	return lidLockedArtistView(artist)
}

// lidRecentTitles returns the library albums holding at least one file,
// newest file first (tie: higher album id first).
func lidRecentTitles() []lidTitleView {
	out := []lidTitleView{}
	for _, t := range lidLibraryTitles() {
		if !t.NewestFile.IsZero() {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].NewestFile.Equal(out[j].NewestFile) {
			return out[i].NewestFile.After(out[j].NewestFile)
		}
		return out[i].AlbumID > out[j].AlbumID
	})
	return out
}
