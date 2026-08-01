// data_arr.go — fixture state for the fake Radarr v3 and Sonarr v3 APIs
// (library docs, queues, history, releases, manual-import candidates) plus
// the cross-domain hooks arrOnRequestStarted / arrOnRequestCompleted
// (contract.md §7). Library documents are built FROM the shared catalog
// (demoMovies / demoShows via findMovie / findShow); this file only stores
// arr-side state (files, monitoring, queue, history) keyed by TMDB id.
//
// Locking: every piece of arr state is guarded by arrMu (domain-local — the
// contract forbids touching stateMu here). Seeding is lazy (arrEnsureSeeded)
// because cross-file init order is not guaranteed and the catalog is seeded
// by the discover domain's init.
package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ─── Shared arr-domain state ────────────────────────────

var (
	arrMu       sync.Mutex
	arrSeedOnce sync.Once

	// Radarr library, seed order = arr ids ascending.
	arrRMovies       []*arrRadarrMovie
	arrRMovieByTmdb  = map[int]*arrRadarrMovie{}
	arrRNextMovieID  = 1
	arrRQueue        []*arrQueueItem
	arrRHistory      []*arrHistoryRec
	arrRNextQueueID  = 1001
	arrSNextQueueID  = 2001
	arrNextHistoryID = 5001

	// Sonarr library, seed order = demoShows order (series id = index+1).
	arrSSeries       []*arrSonarrSeries
	arrSSeriesByTmdb = map[int]*arrSonarrSeries{}
	arrSQueue        []*arrQueueItem
	arrSHistory      []*arrHistoryRec
)

// arrRadarrMovie is the arr-side state of one library movie; catalog
// metadata (title, overview, artwork, ratings) is resolved live via
// findMovie so the data never drifts.
type arrRadarrMovie struct {
	ID           int
	TmdbID       int
	Monitored    bool
	HasFile      bool
	IsAvailable  bool
	QualityName  string // file quality when HasFile
	CutoffNotMet bool   // file quality below profile cutoff
	FileSize     int64
	FileDate     time.Time
	Added        time.Time
	InCinemas    string // RFC3339 or ""
	Digital      string // RFC3339 or ""
	Physical     string // RFC3339 or ""
}

// arrEpisodeFile is the arr-side state of one downloaded episode. The file
// id is derived (100000 + episode id) so it is unique and stable.
type arrEpisodeFile struct {
	Size         int64
	QualityName  string
	CutoffNotMet bool
	DateAdded    time.Time
}

// arrSonarrSeries is the arr-side state of one library series.
type arrSonarrSeries struct {
	ID               int
	TmdbID           int
	Monitored        bool
	SeasonFolder     bool
	Path             string
	QualityProfileID int
	SeriesType       string
	Tags             []int
	SeasonMonitored  map[int]bool            // season number -> monitored
	EpMonitored      map[int]bool            // episode id -> monitored (absent = true)
	Files            map[int]*arrEpisodeFile // episode id -> file
	Added            time.Time
}

// arrQueueItem is one download-queue row (shared shape; the radarr and
// sonarr renderers add their service-specific joins).
type arrQueueItem struct {
	ID                    int
	Title                 string
	Status                string
	TrackedDownloadStatus string
	TrackedDownloadState  string
	Protocol              string
	Indexer               string
	DownloadClient        string
	Size                  int64
	SizeLeft              int64
	TimeLeft              string
	DownloadID            string
	OutputPath            string
	ErrorMessage          string
	StatusMessages        []arrStatusMessage
	QualityName           string
	Added                 time.Time
	// Radarr join.
	MovieID int
	// Sonarr joins.
	SeriesID     int
	EpisodeID    int
	SeasonNumber int
}

type arrStatusMessage struct {
	Title    string
	Messages []string
}

// arrHistoryRec is one history row (shared shape).
type arrHistoryRec struct {
	ID          int
	MovieID     int
	SeriesID    int
	EpisodeID   int
	SourceTitle string
	EventType   string
	QualityName string
	DownloadID  string
	Date        time.Time
	Data        map[string]string
}

// ─── JSON-exactness helpers ─────────────────────────────

// arrFrac marshals a float64 that MUST render with a fractional part
// (Radarr/Sonarr ratings.value: a bare JSON integer blanks app screens).
type arrFrac float64

func (f arrFrac) MarshalJSON() ([]byte, error) {
	s := strconv.FormatFloat(float64(f), 'f', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return []byte(s), nil
}

// arrQualityMeta maps a display quality name to its fake numeric id and
// source/resolution metadata (only .quality.name is load-bearing app-side).
var arrQualityMeta = map[string]struct {
	ID         int
	Source     string
	Resolution int
}{
	"Bluray-1080p": {7, "bluray", 1080},
	"WEBDL-1080p":  {3, "webdl", 1080},
	"Bluray-720p":  {6, "bluray", 720},
	"WEBDL-720p":   {5, "webdl", 720},
	"HDTV-720p":    {4, "television", 720},
}

// arrQualityBlob builds the nested quality object both fake arrs emit and
// round-trip verbatim through manual import.
func arrQualityBlob(name string) map[string]any {
	meta, ok := arrQualityMeta[name]
	if !ok {
		meta = arrQualityMeta["WEBDL-1080p"]
		if name == "" {
			name = "WEBDL-1080p"
		}
	}
	return map[string]any{
		"quality": map[string]any{
			"id":         meta.ID,
			"name":       name,
			"source":     meta.Source,
			"resolution": meta.Resolution,
		},
		"revision": map[string]any{"version": 1, "real": 0, "isRepack": false},
	}
}

// arrLangs is the languages array round-tripped through manual import.
func arrLangs() []map[string]any {
	return []map[string]any{{"id": 1, "name": "English"}}
}

// arrQualityProfilesJSON serves GET /qualityprofile for both fakes
// (id AND name are hard-required by the app).
func arrQualityProfilesJSON() []map[string]any {
	return []map[string]any{
		{"id": 4, "name": "HD-720p"},
		{"id": 6, "name": "HD-1080p"},
		{"id": 7, "name": "Ultra-HD"},
	}
}

// arrImages builds the images array; the app renders remoteUrl only.
func arrImages(posterPath, backdropPath string) []map[string]any {
	imgs := make([]map[string]any, 0, 2)
	if posterPath != "" {
		u := "https://image.tmdb.org/t/p/w500" + posterPath
		imgs = append(imgs, map[string]any{"coverType": "poster", "url": u, "remoteUrl": u})
	}
	if backdropPath != "" {
		u := "https://image.tmdb.org/t/p/w1280" + backdropPath
		imgs = append(imgs, map[string]any{"coverType": "fanart", "url": u, "remoteUrl": u})
	}
	return imgs
}

// arrDotted converts a title to scene-style dotted form.
func arrDotted(title string) string {
	var b strings.Builder
	for _, r := range title {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), ".")
}

// arrSlug converts a title to a lowercase hyphen slug.
func arrSlug(title string) string {
	return strings.ToLower(strings.ReplaceAll(arrDotted(title), ".", "-"))
}

// arrDate parses a "YYYY-MM-DD" catalog date; ok=false when absent/bad.
func arrDate(d string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// arrISO renders a time as RFC3339 UTC; "" for the zero time.
func arrISO(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// arrParseWindow parses the calendar start/end params (Dart emits local ISO
// strings WITHOUT a zone suffix). Falls back to now-7d .. now+30d.
func arrParseWindow(startS, endS string) (time.Time, time.Time) {
	parse := func(s string) (time.Time, bool) {
		for _, layout := range []string{
			time.RFC3339Nano, time.RFC3339,
			"2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05", "2006-01-02",
		} {
			if t, err := time.Parse(layout, s); err == nil {
				return t, true
			}
		}
		return time.Time{}, false
	}
	now := time.Now().UTC()
	start, ok := parse(startS)
	if !ok {
		start = now.AddDate(0, 0, -7)
	}
	end, ok := parse(endS)
	if !ok {
		end = now.AddDate(0, 0, 30)
	}
	return start, end
}

// arrPageParams reads page/pageSize with the arr defaults.
func arrPageParams(r interface {
	FormValue(string) string
}) (int, int) {
	page, _ := strconv.Atoi(r.FormValue("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(r.FormValue("pageSize"))
	if size < 1 {
		size = 50
	}
	return page, size
}

// arrPaged slices records for the requested page and wraps them in the
// paged envelope {page, pageSize, totalRecords, records}.
func arrPaged(records []map[string]any, page, size int) map[string]any {
	total := len(records)
	start := (page - 1) * size
	if start > total {
		start = total
	}
	end := start + size
	if end > total {
		end = total
	}
	pageRecs := records[start:end]
	if pageRecs == nil {
		pageRecs = []map[string]any{}
	}
	return map[string]any{
		"page":          page,
		"pageSize":      size,
		"sortDirection": "descending",
		"totalRecords":  total,
		"records":       pageRecs,
	}
}

// ─── Seeding ────────────────────────────────────────────

// arrEnsureSeeded lazily seeds the arr fixtures on first use (init order
// across parallel-landed files is not guaranteed, so nothing here runs at
// package init).
func arrEnsureSeeded() {
	arrSeedOnce.Do(arrSeedFixtures)
}

func arrSeedFixtures() {
	arrMu.Lock()
	defer arrMu.Unlock()
	now := time.Now().UTC()

	arrSeedRadarr(now)
	arrSeedSonarr(now)
}

// arrSeedRadarr builds the Radarr library from the shared catalog: 8 films
// on disk (two below cutoff), 3 monitored-and-missing, near-now digital /
// physical release dates for the calendar, a downloading queue item, and
// the "stuck" import-pending item that showcases the Import Doctor.
func arrSeedRadarr(now time.Time) {
	type seed struct {
		tmdb      int
		hasFile   bool
		quality   string
		cutoff    bool
		digital   time.Time
		physical  time.Time
		available bool
	}
	seeds := []seed{
		{tmdb: 19, hasFile: true, quality: "Bluray-1080p"},                                  // Metropolis
		{tmdb: 961, hasFile: true, quality: "Bluray-1080p"},                                 // The General
		{tmdb: 653, hasFile: true, quality: "Bluray-720p", cutoff: true},                    // Nosferatu
		{tmdb: 10331, hasFile: true, quality: "WEBDL-1080p"},                                // Night of the Living Dead
		{tmdb: 3085, hasFile: true, quality: "WEBDL-1080p"},                                 // His Girl Friday
		{tmdb: 234, hasFile: true, quality: "HDTV-720p", cutoff: true},                      // The Cabinet of Dr. Caligari
		{tmdb: 775, hasFile: true, quality: "Bluray-1080p"},                                 // A Trip to the Moon
		{tmdb: 4808, hasFile: true, quality: "WEBDL-1080p", digital: now.AddDate(0, 0, -2)}, // Charade
		{tmdb: 964, available: true, digital: now.AddDate(0, 0, 3)},                         // The Phantom of the Opera — downloading
		{tmdb: 16093, available: true, physical: now.AddDate(0, 0, 9)},                      // Carnival of Souls — stuck import
		{tmdb: 18995, available: true, digital: now.AddDate(0, 0, 20)},                      // D.O.A. — wanted/missing
	}

	for i, s := range seeds {
		mv, ok := findMovie(s.tmdb)
		if !ok {
			continue
		}
		m := &arrRadarrMovie{
			ID:          arrRNextMovieID,
			TmdbID:      s.tmdb,
			Monitored:   true,
			HasFile:     s.hasFile,
			IsAvailable: s.hasFile || s.available,
			Added:       now.AddDate(0, 0, -60+i*3),
			Digital:     arrISO(s.digital),
			Physical:    arrISO(s.physical),
		}
		arrRNextMovieID++
		if rel, ok := arrDate(mv.ReleaseDate); ok {
			m.InCinemas = arrISO(rel)
		}
		if s.hasFile {
			m.QualityName = s.quality
			m.CutoffNotMet = s.cutoff
			m.FileSize = arrMovieSize(mv.Runtime, s.quality)
			m.FileDate = now.AddDate(0, 0, -30+i*2)
		}
		arrRMovies = append(arrRMovies, m)
		arrRMovieByTmdb[s.tmdb] = m
	}

	// Queue: one healthy download plus the stuck import-pending item.
	if m := arrRMovieByTmdb[964]; m != nil {
		mv, _ := findMovie(964)
		title := arrReleaseTitle(mv.Title, mv.Year(), "Bluray-1080p")
		arrRQueue = append(arrRQueue, &arrQueueItem{
			ID: arrRNextQueueID, MovieID: m.ID, Title: title,
			Status: "downloading", TrackedDownloadStatus: "ok",
			TrackedDownloadState: "downloading",
			Protocol:             "usenet", Indexer: "DemoNZB", DownloadClient: "SABnzbd",
			Size: 2_400_000_000, SizeLeft: 1_150_000_000, TimeLeft: "00:18:42",
			DownloadID:  "SABnzbd_nzo_demo03",
			OutputPath:  "/downloads/incomplete/" + title,
			QualityName: "Bluray-1080p", Added: now.Add(-25 * time.Minute),
			StatusMessages: []arrStatusMessage{},
		})
		arrRNextQueueID++
	}
	if m := arrRMovieByTmdb[16093]; m != nil {
		mv, _ := findMovie(16093)
		title := arrReleaseTitle(mv.Title, mv.Year(), "WEBDL-1080p")
		arrRQueue = append(arrRQueue, &arrQueueItem{
			ID: arrRNextQueueID, MovieID: m.ID, Title: title,
			Status: "completed", TrackedDownloadStatus: "warning",
			TrackedDownloadState: "importPending",
			Protocol:             "usenet", Indexer: "DemoNZB", DownloadClient: "SABnzbd",
			Size: 1_900_000_000, SizeLeft: 0,
			DownloadID:  "SABnzbd_nzo_demo01",
			OutputPath:  "/downloads/complete/" + title,
			QualityName: "WEBDL-1080p", Added: now.Add(-3 * time.Hour),
			StatusMessages: []arrStatusMessage{{
				Title:    title,
				Messages: []string{"No files found are eligible for import in /downloads/complete/" + title},
			}},
		})
		arrRNextQueueID++
		// The stuck download's grab record.
		arrRHistory = append(arrRHistory, arrNewHistory(&arrHistoryRec{
			MovieID: m.ID, SourceTitle: title, EventType: "grabbed",
			QualityName: "WEBDL-1080p", DownloadID: "SABnzbd_nzo_demo01",
			Date: now.Add(-4 * time.Hour),
		}))
	}

	// History: grab + import pairs for the on-disk films, one failure.
	i := 0
	for _, m := range arrRMovies {
		if !m.HasFile {
			continue
		}
		mv, ok := findMovie(m.TmdbID)
		if !ok {
			continue
		}
		title := arrReleaseTitle(mv.Title, mv.Year(), m.QualityName)
		dlID := fmt.Sprintf("SABnzbd_nzo_hist%02d", i)
		grabbed := now.Add(-time.Duration(20+i*11) * time.Hour)
		arrRHistory = append(arrRHistory,
			arrNewHistory(&arrHistoryRec{
				MovieID: m.ID, SourceTitle: title, EventType: "grabbed",
				QualityName: m.QualityName, DownloadID: dlID, Date: grabbed,
			}),
			arrNewHistory(&arrHistoryRec{
				MovieID: m.ID, SourceTitle: title, EventType: "downloadFolderImported",
				QualityName: m.QualityName, DownloadID: dlID,
				Date: grabbed.Add(40 * time.Minute),
			}),
		)
		i++
	}
	if m := arrRMovieByTmdb[18995]; m != nil {
		mv, _ := findMovie(18995)
		title := arrReleaseTitle(mv.Title, mv.Year(), "HDTV-720p")
		arrRHistory = append(arrRHistory, arrNewHistory(&arrHistoryRec{
			MovieID: m.ID, SourceTitle: title, EventType: "downloadFailed",
			QualityName: "HDTV-720p", DownloadID: "SABnzbd_nzo_fail01",
			Date: now.Add(-9 * time.Hour),
		}))
	}
}

// arrSeedSonarr builds the Sonarr library from every catalog show: aired
// episodes get files except a deterministic sprinkle of missing ones
// (wanted/missing rows), some files sit below cutoff (wanted/cutoff), and
// series 1 carries a downloading and a stuck queue item.
func arrSeedSonarr(now time.Time) {
	for i, show := range demoShows {
		st := &arrSonarrSeries{
			ID:               i + 1,
			TmdbID:           show.TmdbID,
			Monitored:        true,
			SeasonFolder:     true,
			Path:             "/tv/" + show.Name,
			QualityProfileID: 6,
			SeriesType:       "standard",
			Tags:             []int{},
			SeasonMonitored:  map[int]bool{},
			EpMonitored:      map[int]bool{},
			Files:            map[int]*arrEpisodeFile{},
			Added:            now.AddDate(0, 0, -90+i*7),
		}
		if i == 0 {
			st.Tags = []int{1}
		}
		airedIdx, filedIdx := 0, 0
		for _, season := range show.Seasons {
			st.SeasonMonitored[season.SeasonNumber] = true
			for _, ep := range season.Episodes {
				air, ok := arrDate(ep.AirDate)
				if !ok || air.After(now) {
					continue // unaired — no file
				}
				airedIdx++
				if airedIdx%9 == 4 {
					continue // deterministic missing episode (wanted/missing row)
				}
				filedIdx++
				quality := "WEBDL-1080p"
				cutoff := false
				if filedIdx%11 == 6 {
					quality = "HDTV-720p"
					cutoff = true
				}
				st.Files[ep.ID] = &arrEpisodeFile{
					Size:         arrEpisodeSize(ep.Runtime, quality),
					QualityName:  quality,
					CutoffNotMet: cutoff,
					DateAdded:    air.Add(26 * time.Hour),
				}
			}
		}
		arrSSeries = append(arrSSeries, st)
		arrSSeriesByTmdb[show.TmdbID] = st
	}

	// Queue seeds hang off the first series' missing aired episodes.
	if len(arrSSeries) == 0 {
		return
	}
	st := arrSSeries[0]
	show, ok := findShow(st.TmdbID)
	if !ok {
		return
	}
	missing := arrMissingAiredEpisodes(st, show, now)
	if len(missing) > 0 {
		ep := missing[0]
		title := fmt.Sprintf("%s.S%02dE%02d.%s.WEBDL-1080p-DEMO",
			arrDotted(show.Name), ep.season, ep.ep.EpisodeNumber, arrDotted(ep.ep.Name))
		arrSQueue = append(arrSQueue, &arrQueueItem{
			ID: arrSNextQueueID, SeriesID: st.ID, EpisodeID: ep.ep.ID, SeasonNumber: ep.season,
			Title:  title,
			Status: "downloading", TrackedDownloadStatus: "ok",
			TrackedDownloadState: "downloading",
			Protocol:             "usenet", Indexer: "DemoNZB", DownloadClient: "SABnzbd",
			Size: 900_000_000, SizeLeft: 320_000_000, TimeLeft: "00:07:15",
			DownloadID:  "SABnzbd_nzo_demo04",
			OutputPath:  "/downloads/incomplete/" + title,
			QualityName: "WEBDL-1080p", Added: now.Add(-12 * time.Minute),
			StatusMessages: []arrStatusMessage{},
		})
		arrSNextQueueID++
	}
	if len(missing) > 1 {
		ep := missing[1]
		title := fmt.Sprintf("%s.S%02dE%02d.%s.WEBDL-1080p-DEMO",
			arrDotted(show.Name), ep.season, ep.ep.EpisodeNumber, arrDotted(ep.ep.Name))
		arrSQueue = append(arrSQueue, &arrQueueItem{
			ID: arrSNextQueueID, SeriesID: st.ID, EpisodeID: ep.ep.ID, SeasonNumber: ep.season,
			Title:  title,
			Status: "completed", TrackedDownloadStatus: "warning",
			TrackedDownloadState: "importPending",
			Protocol:             "usenet", Indexer: "DemoNZB", DownloadClient: "SABnzbd",
			Size: 850_000_000, SizeLeft: 0,
			DownloadID:  "SABnzbd_nzo_demo02",
			OutputPath:  "/downloads/complete/" + title,
			QualityName: "WEBDL-1080p", Added: now.Add(-2 * time.Hour),
			StatusMessages: []arrStatusMessage{{
				Title:    title,
				Messages: []string{"No files found are eligible for import in /downloads/complete/" + title},
			}},
		})
		arrSNextQueueID++
		arrSHistory = append(arrSHistory, arrNewHistory(&arrHistoryRec{
			SeriesID: st.ID, EpisodeID: ep.ep.ID, SourceTitle: title,
			EventType: "grabbed", QualityName: "WEBDL-1080p",
			DownloadID: "SABnzbd_nzo_demo02", Date: now.Add(-3 * time.Hour),
		}))
	}

	// History: recent grab + import pairs across the first two series.
	pairIdx := 0
	for _, st := range arrSSeries {
		if st.ID > 2 {
			break
		}
		show, ok := findShow(st.TmdbID)
		if !ok {
			continue
		}
		count := 0
		for si := len(show.Seasons) - 1; si >= 0 && count < 5; si-- {
			season := show.Seasons[si]
			for ei := len(season.Episodes) - 1; ei >= 0 && count < 5; ei-- {
				ep := season.Episodes[ei]
				f := st.Files[ep.ID]
				if f == nil {
					continue
				}
				title := fmt.Sprintf("%s.S%02dE%02d.%s-DEMO",
					arrDotted(show.Name), season.SeasonNumber, ep.EpisodeNumber, f.QualityName)
				dlID := fmt.Sprintf("SABnzbd_nzo_tv%02d", pairIdx)
				grabbed := time.Now().UTC().Add(-time.Duration(8+pairIdx*13) * time.Hour)
				arrSHistory = append(arrSHistory,
					arrNewHistory(&arrHistoryRec{
						SeriesID: st.ID, EpisodeID: ep.ID, SourceTitle: title,
						EventType: "grabbed", QualityName: f.QualityName,
						DownloadID: dlID, Date: grabbed,
					}),
					arrNewHistory(&arrHistoryRec{
						SeriesID: st.ID, EpisodeID: ep.ID, SourceTitle: title,
						EventType: "downloadFolderImported", QualityName: f.QualityName,
						DownloadID: dlID, Date: grabbed.Add(25 * time.Minute),
					}),
				)
				pairIdx++
				count++
			}
		}
	}
}

// arrNewHistory assigns the next history id (call under arrMu).
func arrNewHistory(rec *arrHistoryRec) *arrHistoryRec {
	rec.ID = arrNextHistoryID
	arrNextHistoryID++
	if rec.Data == nil {
		rec.Data = map[string]string{
			"indexer":        "DemoNZB",
			"releaseGroup":   "DEMO",
			"downloadClient": "SABnzbd",
		}
	}
	return rec
}

type arrMissingEp struct {
	season int
	ep     DemoEpisode
}

// arrMissingAiredEpisodes lists a series' monitored aired episodes without a
// file, in air order (call under arrMu).
func arrMissingAiredEpisodes(st *arrSonarrSeries, show *DemoShow, now time.Time) []arrMissingEp {
	out := []arrMissingEp{}
	for _, season := range show.Seasons {
		for _, ep := range season.Episodes {
			air, ok := arrDate(ep.AirDate)
			if !ok || air.After(now) {
				continue
			}
			if st.Files[ep.ID] != nil {
				continue
			}
			if mon, held := st.EpMonitored[ep.ID]; held && !mon {
				continue
			}
			out = append(out, arrMissingEp{season: season.SeasonNumber, ep: ep})
		}
	}
	return out
}

// ─── Size / title helpers ───────────────────────────────

func arrMovieSize(runtime int, quality string) int64 {
	if runtime <= 0 {
		runtime = 90
	}
	perMin := int64(28_000_000)
	if strings.Contains(quality, "720") {
		perMin = 14_000_000
	}
	return int64(runtime) * perMin
}

func arrEpisodeSize(runtime int, quality string) int64 {
	if runtime <= 0 {
		runtime = 40
	}
	perMin := int64(22_000_000)
	if strings.Contains(quality, "720") {
		perMin = 11_000_000
	}
	return int64(runtime) * perMin
}

func arrReleaseTitle(title string, year int, quality string) string {
	return fmt.Sprintf("%s.%d.%s.x264-DEMO", arrDotted(title), year, quality)
}

// arrMoviePath is the arr-side folder for a library movie ("/movies" maps to
// "/media/movies" cantinarr-side).
func arrMoviePath(mv *DemoMovie) string {
	return fmt.Sprintf("/movies/%s (%d)", mv.Title, mv.Year())
}

func arrMovieFileName(mv *DemoMovie, quality string) string {
	return fmt.Sprintf("%s.%d.%s.mkv", arrDotted(mv.Title), mv.Year(), quality)
}

// arrEpisodeFileID derives the stable episode-file id for an episode.
func arrEpisodeFileID(episodeID int) int { return 100000 + episodeID }

// arrEpisodeFromFileID reverses arrEpisodeFileID; 0 when out of range.
func arrEpisodeFromFileID(fileID int) int {
	if fileID <= 100000 {
		return 0
	}
	return fileID - 100000
}

// ─── Shared renderers ───────────────────────────────────

// arrQueueBaseJSON renders the service-agnostic part of a queue row; the
// radarr/sonarr handlers add their joins (movieId/movie vs seriesId/series/
// episode). Call under arrMu.
func arrQueueBaseJSON(q *arrQueueItem) map[string]any {
	msgs := make([]map[string]any, 0, len(q.StatusMessages))
	for _, m := range q.StatusMessages {
		lines := m.Messages
		if lines == nil {
			lines = []string{}
		}
		msgs = append(msgs, map[string]any{"title": m.Title, "messages": lines})
	}
	doc := map[string]any{
		"id":             q.ID,
		"title":          q.Title,
		"status":         q.Status,
		"protocol":       q.Protocol,
		"indexer":        q.Indexer,
		"downloadClient": q.DownloadClient,
		"size":           q.Size,
		"sizeleft":       q.SizeLeft,
		"downloadId":     q.DownloadID,
		"outputPath":     q.OutputPath,
		"statusMessages": msgs,
		"quality":        arrQualityBlob(q.QualityName),
		"added":          arrISO(q.Added),
	}
	if q.TrackedDownloadStatus != "" {
		doc["trackedDownloadStatus"] = q.TrackedDownloadStatus
	}
	if q.TrackedDownloadState != "" {
		doc["trackedDownloadState"] = q.TrackedDownloadState
	}
	if q.TimeLeft != "" {
		doc["timeleft"] = q.TimeLeft
	}
	if q.ErrorMessage != "" {
		doc["errorMessage"] = q.ErrorMessage
	}
	return doc
}

// arrHistoryBaseJSON renders the service-agnostic part of a history row.
func arrHistoryBaseJSON(rec *arrHistoryRec) map[string]any {
	return map[string]any{
		"id":          rec.ID,
		"sourceTitle": rec.SourceTitle,
		"eventType":   rec.EventType,
		"date":        arrISO(rec.Date),
		"downloadId":  rec.DownloadID,
		"quality":     arrQualityBlob(rec.QualityName),
		"languages":   arrLangs(),
		"data":        rec.Data,
	}
}

// arrEmitQueueChanged broadcasts the arr_queue_changed ping every arr Queue
// screen debounces on. Call OUTSIDE arrMu.
func arrEmitQueueChanged(instanceID, serviceType string) {
	wsBroadcast(evtArrQueueChanged, map[string]any{
		"instance_id":  instanceID,
		"service_type": serviceType,
	})
}

// ─── History sorting ────────────────────────────────────

// arrHistorySorted returns a date-descending copy (call under arrMu).
func arrHistorySorted(recs []*arrHistoryRec) []*arrHistoryRec {
	out := make([]*arrHistoryRec, len(recs))
	copy(out, recs)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Date.After(out[j].Date) })
	return out
}

// ─── Cross-domain hooks (contract.md §7) ────────────────

// arrOnRequestStarted seeds (or refreshes) the fake arr queue item for a
// started movie/TV request. Repeated calls shrink sizeleft so the queue
// looks alive during the download simulation.
func arrOnRequestStarted(tmdbID int, mediaType string) {
	arrEnsureSeeded()
	switch mediaType {
	case mediaTypeMovie:
		if arrStartMovieDownload(tmdbID) {
			wsBroadcast(evtArrQueueChanged, map[string]any{
				"instance_id": instRadarr, "service_type": serviceRadarr,
			})
		}
	case mediaTypeTV:
		if arrStartSeriesDownload(tmdbID) {
			wsBroadcast(evtArrQueueChanged, map[string]any{
				"instance_id": instSonarr, "service_type": serviceSonarr,
			})
		}
	}
}

func arrStartMovieDownload(tmdbID int) bool {
	mv, ok := findMovie(tmdbID)
	if !ok {
		return false
	}
	arrMu.Lock()
	defer arrMu.Unlock()
	m := arrRMovieByTmdb[tmdbID]
	if m == nil {
		m = &arrRadarrMovie{
			ID: arrRNextMovieID, TmdbID: tmdbID,
			Monitored: true, IsAvailable: true,
			Added: time.Now().UTC(),
		}
		if rel, ok := arrDate(mv.ReleaseDate); ok {
			m.InCinemas = arrISO(rel)
		}
		arrRNextMovieID++
		arrRMovies = append(arrRMovies, m)
		arrRMovieByTmdb[tmdbID] = m
	}
	for _, q := range arrRQueue {
		if q.MovieID == m.ID {
			// Refresh tick: shrink the remaining bytes.
			q.SizeLeft = int64(float64(q.SizeLeft) * 0.72)
			if q.SizeLeft < q.Size/20 {
				q.SizeLeft = q.Size / 20
			}
			return true
		}
	}
	title := arrReleaseTitle(mv.Title, mv.Year(), "WEBDL-1080p")
	size := arrMovieSize(mv.Runtime, "WEBDL-1080p")
	arrRQueue = append(arrRQueue, &arrQueueItem{
		ID: arrRNextQueueID, MovieID: m.ID, Title: title,
		Status: "downloading", TrackedDownloadStatus: "ok",
		TrackedDownloadState: "downloading",
		Protocol:             "usenet", Indexer: "DemoNZB", DownloadClient: "SABnzbd",
		Size: size, SizeLeft: size * 17 / 20, TimeLeft: "00:12:30",
		DownloadID:  fmt.Sprintf("SABnzbd_nzo_req%d", arrRNextQueueID),
		OutputPath:  "/downloads/incomplete/" + title,
		QualityName: "WEBDL-1080p", Added: time.Now().UTC(),
		StatusMessages: []arrStatusMessage{},
	})
	arrRNextQueueID++
	return true
}

func arrStartSeriesDownload(tmdbID int) bool {
	show, ok := findShow(tmdbID)
	if !ok {
		return false
	}
	arrMu.Lock()
	defer arrMu.Unlock()
	st := arrSSeriesByTmdb[tmdbID]
	if st == nil {
		st = &arrSonarrSeries{
			ID: len(arrSSeries) + 1, TmdbID: tmdbID,
			Monitored: true, SeasonFolder: true,
			Path:             "/tv/" + show.Name,
			QualityProfileID: 6, SeriesType: "standard",
			Tags:            []int{},
			SeasonMonitored: map[int]bool{}, EpMonitored: map[int]bool{},
			Files: map[int]*arrEpisodeFile{},
			Added: time.Now().UTC(),
		}
		for _, season := range show.Seasons {
			st.SeasonMonitored[season.SeasonNumber] = true
		}
		arrSSeries = append(arrSSeries, st)
		arrSSeriesByTmdb[tmdbID] = st
	}
	for _, q := range arrSQueue {
		if q.SeriesID == st.ID && q.TrackedDownloadState == "downloading" {
			q.SizeLeft = int64(float64(q.SizeLeft) * 0.72)
			if q.SizeLeft < q.Size/20 {
				q.SizeLeft = q.Size / 20
			}
			return true
		}
	}
	// Target the first missing aired episode, else episode 1 of season 1.
	seasonNum, epID, epNum, epName := 1, 0, 1, ""
	if missing := arrMissingAiredEpisodes(st, show, time.Now().UTC()); len(missing) > 0 {
		seasonNum = missing[0].season
		epID = missing[0].ep.ID
		epNum = missing[0].ep.EpisodeNumber
		epName = missing[0].ep.Name
	} else if len(show.Seasons) > 0 && len(show.Seasons[0].Episodes) > 0 {
		seasonNum = show.Seasons[0].SeasonNumber
		epID = show.Seasons[0].Episodes[0].ID
		epNum = show.Seasons[0].Episodes[0].EpisodeNumber
		epName = show.Seasons[0].Episodes[0].Name
	}
	title := fmt.Sprintf("%s.S%02dE%02d.%s.WEBDL-1080p-DEMO",
		arrDotted(show.Name), seasonNum, epNum, arrDotted(epName))
	arrSQueue = append(arrSQueue, &arrQueueItem{
		ID: arrSNextQueueID, SeriesID: st.ID, EpisodeID: epID, SeasonNumber: seasonNum,
		Title:  title,
		Status: "downloading", TrackedDownloadStatus: "ok",
		TrackedDownloadState: "downloading",
		Protocol:             "usenet", Indexer: "DemoNZB", DownloadClient: "SABnzbd",
		Size: 900_000_000, SizeLeft: 760_000_000, TimeLeft: "00:10:05",
		DownloadID:  fmt.Sprintf("SABnzbd_nzo_req%d", arrSNextQueueID),
		OutputPath:  "/downloads/incomplete/" + title,
		QualityName: "WEBDL-1080p", Added: time.Now().UTC(),
		StatusMessages: []arrStatusMessage{},
	})
	arrSNextQueueID++
	return true
}

// arrOnRequestCompleted flips library availability when a request finishes:
// Radarr hasFile / Sonarr episode files and season statistics update so
// search chips and library rows change immediately.
func arrOnRequestCompleted(tmdbID int, mediaType string, seasons []int) {
	arrEnsureSeeded()
	switch mediaType {
	case mediaTypeMovie:
		if arrCompleteMovie(tmdbID) {
			wsBroadcast(evtArrQueueChanged, map[string]any{
				"instance_id": instRadarr, "service_type": serviceRadarr,
			})
		}
	case mediaTypeTV:
		if arrCompleteSeries(tmdbID, seasons) {
			wsBroadcast(evtArrQueueChanged, map[string]any{
				"instance_id": instSonarr, "service_type": serviceSonarr,
			})
		}
	}
}

func arrCompleteMovie(tmdbID int) bool {
	mv, ok := findMovie(tmdbID)
	if !ok {
		return false
	}
	arrMu.Lock()
	defer arrMu.Unlock()
	m := arrRMovieByTmdb[tmdbID]
	if m == nil {
		m = &arrRadarrMovie{
			ID: arrRNextMovieID, TmdbID: tmdbID, Monitored: true,
			Added: time.Now().UTC(),
		}
		arrRNextMovieID++
		arrRMovies = append(arrRMovies, m)
		arrRMovieByTmdb[tmdbID] = m
	}
	now := time.Now().UTC()
	m.HasFile = true
	m.IsAvailable = true
	m.Monitored = true
	m.QualityName = "WEBDL-1080p"
	m.CutoffNotMet = false
	m.FileSize = arrMovieSize(mv.Runtime, "WEBDL-1080p")
	m.FileDate = now
	// Drop this movie's queue items and record the grab + import.
	title := arrReleaseTitle(mv.Title, mv.Year(), "WEBDL-1080p")
	dlID := fmt.Sprintf("SABnzbd_nzo_done%d", m.ID)
	kept := arrRQueue[:0]
	for _, q := range arrRQueue {
		if q.MovieID == m.ID {
			dlID = q.DownloadID
			title = q.Title
			continue
		}
		kept = append(kept, q)
	}
	arrRQueue = kept
	arrRHistory = append(arrRHistory,
		arrNewHistory(&arrHistoryRec{
			MovieID: m.ID, SourceTitle: title, EventType: "grabbed",
			QualityName: "WEBDL-1080p", DownloadID: dlID, Date: now.Add(-2 * time.Minute),
		}),
		arrNewHistory(&arrHistoryRec{
			MovieID: m.ID, SourceTitle: title, EventType: "downloadFolderImported",
			QualityName: "WEBDL-1080p", DownloadID: dlID, Date: now,
		}),
	)
	return true
}

func arrCompleteSeries(tmdbID int, seasons []int) bool {
	show, ok := findShow(tmdbID)
	if !ok {
		return false
	}
	arrMu.Lock()
	defer arrMu.Unlock()
	st := arrSSeriesByTmdb[tmdbID]
	if st == nil {
		return false
	}
	now := time.Now().UTC()
	wanted := map[int]bool{}
	for _, s := range seasons {
		wanted[s] = true
	}
	for _, season := range show.Seasons {
		if len(wanted) > 0 && !wanted[season.SeasonNumber] {
			continue
		}
		imported := false
		for _, ep := range season.Episodes {
			air, ok := arrDate(ep.AirDate)
			if !ok || air.After(now) {
				continue // unaired episodes stay missing (partial season)
			}
			if st.Files[ep.ID] != nil {
				continue
			}
			st.Files[ep.ID] = &arrEpisodeFile{
				Size:        arrEpisodeSize(ep.Runtime, "WEBDL-1080p"),
				QualityName: "WEBDL-1080p",
				DateAdded:   now,
			}
			imported = true
		}
		if imported {
			title := fmt.Sprintf("%s.S%02d.WEBDL-1080p-DEMO", arrDotted(show.Name), season.SeasonNumber)
			arrSHistory = append(arrSHistory, arrNewHistory(&arrHistoryRec{
				SeriesID: st.ID, SourceTitle: title,
				EventType: "downloadFolderImported", QualityName: "WEBDL-1080p",
				DownloadID: fmt.Sprintf("SABnzbd_nzo_tvdone%d-%d", st.ID, season.SeasonNumber),
				Date:       now,
			}))
		}
	}
	// Drop this series' queue items.
	kept := arrSQueue[:0]
	for _, q := range arrSQueue {
		if q.SeriesID == st.ID {
			continue
		}
		kept = append(kept, q)
	}
	arrSQueue = kept
	return true
}
