// arr_sonarr.go — the fake Sonarr v3 API served behind the instance proxy
// (/api/instances/{sonarrID}/api/v3/...). Pure handlers: authorization and
// the non-admin read allowlist are enforced by the proxy dispatcher before
// handleSonarrProxy runs. Statistics counters and sizeOnDisk MUST be JSON
// integers, and episode rows need id/seriesId/seasonNumber/episodeNumber as
// hard-required ints — see contract.md §1 and app-arr.md §5.
//
// Kids accounts: like the Radarr fake, every GET handler takes the gated
// user (nil for admins and unrestricted users) and applies the real proxy
// gate's rule through policyAllowsShowRecord — series rows and the queue,
// history, wanted, and calendar rows judged by their series are dropped, a
// blocked series answers the arr's own 404, and its episodes answer [].
package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// handleSonarrProxy routes rest (the path after /api/instances/{id}/, e.g.
// "api/v3/series") for a sonarr instance. Contract hook — exact signature.
func handleSonarrProxy(w http.ResponseWriter, r *http.Request, inst *DemoInstance, isAdmin bool, rest string) {
	arrEnsureSeeded()
	if !strings.HasPrefix(rest, "api/v3/") {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	path := strings.Trim(strings.TrimPrefix(rest, "api/v3/"), "/")

	// The content gate (see arr_radarr.go): nil for admins, who are never
	// kids accounts, and consulted per record for everyone else.
	var gate *DemoUser
	if !isAdmin {
		gate = userFrom(r)
	}

	switch r.Method {
	case http.MethodGet:
		switch {
		case path == "series":
			arrSServeSeries(w, inst, gate)
		case strings.HasPrefix(path, "series/"):
			arrSServeSeriesByID(w, gate, strings.TrimPrefix(path, "series/"))
		case path == "episode":
			arrSServeEpisodes(w, r, gate)
		case path == "queue":
			arrSServeQueue(w, r, gate)
		case path == "queue/details":
			arrSServeQueueDetails(w, r, gate)
		case path == "history":
			arrSServeHistory(w, r, gate)
		case path == "history/series":
			arrSServeSeriesHistory(w, r, gate)
		case path == "wanted/missing":
			arrSServeWanted(w, r, gate, false)
		case path == "wanted/cutoff":
			arrSServeWanted(w, r, gate, true)
		case path == "calendar":
			arrSServeCalendar(w, r, gate)
		case path == "release":
			arrSServeReleases(w, r)
		case path == "manualimport":
			arrSServeManualImport(w, r)
		case path == "qualityprofile":
			writeJSON(w, http.StatusOK, arrQualityProfilesJSON())
		case path == "tag":
			writeJSON(w, http.StatusOK, []map[string]any{
				{"id": 1, "label": "classics"},
				{"id": 2, "label": "documentary"},
			})
		case path == "rootfolder":
			writeJSON(w, http.StatusOK, []map[string]any{{
				"id": 1, "path": "/tv", "accessible": true,
				"freeSpace": int64(3_500_000_000_000), "unmappedFolders": []map[string]any{},
			}})
		default:
			writeErr(w, http.StatusNotFound, "not found")
		}
	case http.MethodPut:
		switch {
		case path == "episode/monitor":
			arrSMonitorEpisodes(w, r)
		case strings.HasPrefix(path, "series/"):
			arrSUpdateSeries(w, r, strings.TrimPrefix(path, "series/"))
		default:
			writeErr(w, http.StatusNotFound, "not found")
		}
	case http.MethodPost:
		switch path {
		case "release":
			arrSGrabRelease(w, r, inst)
		case "command":
			arrSCommand(w, r, inst)
		default:
			writeErr(w, http.StatusNotFound, "not found")
		}
	case http.MethodDelete:
		switch {
		case path == "episodefile/bulk":
			arrSDeleteEpisodeFiles(w, r)
		case strings.HasPrefix(path, "queue/"):
			arrSDeleteQueue(w, strings.TrimPrefix(path, "queue/"), inst)
		case strings.HasPrefix(path, "series/"):
			arrSDeleteSeries(w, strings.TrimPrefix(path, "series/"))
		default:
			writeErr(w, http.StatusNotFound, "not found")
		}
	default:
		writeErr(w, http.StatusNotFound, "not found")
	}
}

// ─── Content gate ───────────────────────────────────────

// arrSVisible reports whether a series record may be shown to gate (nil =
// no gate). Mirrors arrRVisible: the title is resolved through the catalog
// and judged by the policy; a record the catalog cannot resolve is hidden
// from a kids account (fail closed) and shown to everyone else; a missing
// record (a row whose series is gone) cannot be judged and is dropped.
func arrSVisible(gate *DemoUser, st *arrSonarrSeries) bool {
	if gate == nil {
		return true
	}
	if st == nil {
		return false
	}
	show, ok := findShow(st.TmdbID)
	if !ok {
		return !cpIsChild(gate.ID)
	}
	return policyAllowsShowRecord(gate, show)
}

// ─── Lookup helpers (call under arrMu) ──────────────────

func arrSSeriesByIDLocked(id int) *arrSonarrSeries {
	for _, st := range arrSSeries {
		if st.ID == id {
			return st
		}
	}
	return nil
}

// arrSFindEpisodeLocked locates an episode by id across the whole library.
func arrSFindEpisodeLocked(episodeID int) (*arrSonarrSeries, *DemoShow, int, DemoEpisode, bool) {
	for _, st := range arrSSeries {
		show, ok := findShow(st.TmdbID)
		if !ok {
			continue
		}
		for _, season := range show.Seasons {
			for _, ep := range season.Episodes {
				if ep.ID == episodeID {
					return st, show, season.SeasonNumber, ep, true
				}
			}
		}
	}
	return nil, nil, 0, DemoEpisode{}, false
}

// arrSEpMonitored reports an episode's monitored flag (default true).
func arrSEpMonitored(st *arrSonarrSeries, episodeID int) bool {
	if mon, held := st.EpMonitored[episodeID]; held {
		return mon
	}
	return true
}

// arrSAirUTC renders a catalog air date as the airDateUtc ISO string.
func arrSAirUTC(airDate string) string {
	t, ok := arrDate(airDate)
	if !ok {
		return ""
	}
	return arrISO(t.Add(2 * time.Hour))
}

// arrSAbsoluteNumber computes an episode's absolute number within its show.
func arrSAbsoluteNumber(show *DemoShow, episodeID int) int {
	n := 0
	for _, season := range show.Seasons {
		for _, ep := range season.Episodes {
			n++
			if ep.ID == episodeID {
				return n
			}
		}
	}
	return 0
}

// ─── Document renderers (call under arrMu) ──────────────

// arrSSeriesJSON builds one full, round-trippable Sonarr series document.
// Every statistics counter and sizeOnDisk is a JSON integer.
func arrSSeriesJSON(st *arrSonarrSeries) map[string]any {
	show, ok := findShow(st.TmdbID)
	if !ok {
		show = &DemoShow{TmdbID: st.TmdbID, Name: "Unknown", Seasons: []DemoSeason{}}
	}
	now := time.Now().UTC()
	genres := make([]string, 0, len(show.Genres))
	for _, g := range show.Genres {
		genres = append(genres, g.Name)
	}

	seasons := make([]map[string]any, 0, len(show.Seasons))
	totalFiles, totalAired, totalEps := 0, 0, 0
	var totalSize int64
	runtime := 30
	for _, season := range show.Seasons {
		files, aired, eps := 0, 0, 0
		var size int64
		nextAiring := ""
		for _, ep := range season.Episodes {
			eps++
			if ep.Runtime > 0 {
				runtime = ep.Runtime
			}
			air, ok := arrDate(ep.AirDate)
			if ok && !air.After(now) {
				aired++
			} else if ok && (nextAiring == "" || arrSAirUTC(ep.AirDate) < nextAiring) {
				nextAiring = arrSAirUTC(ep.AirDate)
			}
			if f := st.Files[ep.ID]; f != nil {
				files++
				size += f.Size
			}
		}
		totalFiles += files
		totalAired += aired
		totalEps += eps
		totalSize += size
		pct := 0.0
		if aired > 0 {
			pct = float64(files) / float64(aired) * 100
		}
		monitored := true
		if mon, held := st.SeasonMonitored[season.SeasonNumber]; held {
			monitored = mon
		}
		stats := map[string]any{
			"episodeFileCount":  files,
			"episodeCount":      aired,
			"totalEpisodeCount": eps,
			"sizeOnDisk":        size,
			"percentOfEpisodes": pct,
		}
		if nextAiring != "" {
			stats["nextAiring"] = nextAiring
		}
		seasons = append(seasons, map[string]any{
			"seasonNumber": season.SeasonNumber,
			"monitored":    monitored,
			"statistics":   stats,
		})
	}
	pct := 0.0
	if totalAired > 0 {
		pct = float64(totalFiles) / float64(totalAired) * 100
	}

	// Sonarr's status vocabulary: a series nothing of which has aired yet is
	// "upcoming" (the premiere is still ahead), otherwise ended/continuing.
	status := "continuing"
	switch {
	case totalAired == 0:
		status = "upcoming"
	case show.Status == "Ended":
		status = "ended"
	}

	doc := map[string]any{
		"id":        st.ID,
		"title":     show.Name,
		"sortTitle": strings.ToLower(show.Name),
		"status":    status,
		"ended":     status == "ended",
		"overview":  show.Overview,
		// The same network the TMDB detail and the Trakt show object name,
		// so the three surfaces agree.
		"network":           discShowNetworkName(show.TmdbID),
		"airTime":           "21:00",
		"images":            arrImages(show.PosterPath, show.BackdropPath),
		"seasons":           seasons,
		"year":              show.Year(),
		"path":              st.Path,
		"qualityProfileId":  st.QualityProfileID,
		"languageProfileId": 1,
		"seasonFolder":      st.SeasonFolder,
		"monitored":         st.Monitored,
		"useSceneNumbering": false,
		"runtime":           runtime,
		"tvdbId":            show.TvdbID,
		"tmdbId":            show.TmdbID,
		"firstAired":        arrSAirUTC(show.FirstAirDate),
		"seriesType":        st.SeriesType,
		"cleanTitle":        arrSlug(show.Name),
		"titleSlug":         arrSlug(show.Name),
		"genres":            genres,
		"tags":              append([]int{}, st.Tags...),
		"added":             arrISO(st.Added),
		"ratings": map[string]any{
			"votes": show.VoteCount,
			"value": arrFrac(show.VoteAverage),
		},
		"statistics": map[string]any{
			"seasonCount":       len(show.Seasons),
			"episodeFileCount":  totalFiles,
			"episodeCount":      totalAired,
			"totalEpisodeCount": totalEps,
			"sizeOnDisk":        totalSize,
			"percentOfEpisodes": pct,
		},
	}
	// The fictional shows carry no IMDb id (a synthesized one would resolve
	// to an unrelated real title), and Sonarr omits the key rather than
	// serving a blank; every consumer treats an absent id as "no link".
	if show.ImdbID != "" {
		doc["imdbId"] = show.ImdbID
	}
	return doc
}

// arrSEpisodeJSON builds one episode row (all four required ints present).
func arrSEpisodeJSON(st *arrSonarrSeries, show *DemoShow, seasonNumber int, ep DemoEpisode, includeFile bool) map[string]any {
	f := st.Files[ep.ID]
	fileID := 0
	if f != nil {
		fileID = arrEpisodeFileID(ep.ID)
	}
	doc := map[string]any{
		"id":                       ep.ID,
		"seriesId":                 st.ID,
		"seasonNumber":             seasonNumber,
		"episodeNumber":            ep.EpisodeNumber,
		"absoluteEpisodeNumber":    arrSAbsoluteNumber(show, ep.ID),
		"title":                    ep.Name,
		"overview":                 ep.Overview,
		"airDate":                  ep.AirDate,
		"airDateUtc":               arrSAirUTC(ep.AirDate),
		"hasFile":                  f != nil,
		"monitored":                arrSEpMonitored(st, ep.ID),
		"episodeFileId":            fileID,
		"unverifiedSceneNumbering": false,
		"runtime":                  ep.Runtime,
	}
	if includeFile && f != nil {
		doc["episodeFile"] = arrSEpisodeFileJSON(st, show, seasonNumber, ep, f)
	}
	return doc
}

func arrSEpisodeFileJSON(st *arrSonarrSeries, show *DemoShow, seasonNumber int, ep DemoEpisode, f *arrEpisodeFile) map[string]any {
	rel := strings.Join([]string{
		"Season " + strconv.Itoa(seasonNumber),
		arrDotted(show.Name) + "." +
			"S" + pad2(seasonNumber) + "E" + pad2(ep.EpisodeNumber) + "." +
			f.QualityName + ".mkv",
	}, "/")
	return map[string]any{
		"id":                  arrEpisodeFileID(ep.ID),
		"seriesId":            st.ID,
		"seasonNumber":        seasonNumber,
		"relativePath":        rel,
		"path":                st.Path + "/" + rel,
		"size":                f.Size,
		"dateAdded":           arrISO(f.DateAdded),
		"releaseGroup":        "DEMO",
		"quality":             arrQualityBlob(f.QualityName),
		"qualityCutoffNotMet": f.CutoffNotMet,
		"languages":           arrLangs(),
	}
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// arrSQueueItemJSON renders one queue row with series + episode joins.
func arrSQueueItemJSON(q *arrQueueItem) map[string]any {
	doc := arrQueueBaseJSON(q)
	doc["seriesId"] = q.SeriesID
	doc["episodeId"] = q.EpisodeID
	doc["seasonNumber"] = q.SeasonNumber
	if st := arrSSeriesByIDLocked(q.SeriesID); st != nil {
		doc["series"] = arrSSeriesJSON(st)
	}
	if st, show, seasonNumber, ep, ok := arrSFindEpisodeLocked(q.EpisodeID); ok {
		doc["episode"] = arrSEpisodeJSON(st, show, seasonNumber, ep, false)
	}
	return doc
}

func arrSHistoryJSON(rec *arrHistoryRec) map[string]any {
	doc := arrHistoryBaseJSON(rec)
	doc["seriesId"] = rec.SeriesID
	doc["episodeId"] = rec.EpisodeID
	return doc
}

// ─── GET handlers ───────────────────────────────────────

func arrSServeSeries(w http.ResponseWriter, inst *DemoInstance, gate *DemoUser) {
	arrMu.Lock()
	docs := make([]map[string]any, 0, len(arrSSeries))
	for _, st := range arrSSeries {
		if !arrInSiblingLibrary(inst, st.TmdbID) || !arrSVisible(gate, st) {
			continue
		}
		docs = append(docs, arrSSeriesJSON(st))
	}
	arrMu.Unlock()
	writeJSON(w, http.StatusOK, docs)
}

func arrSServeSeriesByID(w http.ResponseWriter, gate *DemoUser, idStr string) {
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	arrMu.Lock()
	st := arrSSeriesByIDLocked(id)
	var doc map[string]any
	// A blocked series answers exactly what an absent one does.
	if st != nil && arrSVisible(gate, st) {
		doc = arrSSeriesJSON(st)
	}
	arrMu.Unlock()
	if doc == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// arrSServeEpisodes serves GET /episode?seriesId=[&seasonNumber=]
// [&includeEpisodeFile=true] as a BARE array. Every row's parent is the one
// series, so a blocked series answers [] (every row drops).
func arrSServeEpisodes(w http.ResponseWriter, r *http.Request, gate *DemoUser) {
	seriesID := queryInt(r, "seriesId", 0)
	if seriesID == 0 {
		writeErr(w, http.StatusBadRequest, "seriesId is required")
		return
	}
	seasonFilter := queryInt(r, "seasonNumber", -1)
	includeFile := r.URL.Query().Get("includeEpisodeFile") == "true"
	arrMu.Lock()
	docs := []map[string]any{}
	if st := arrSSeriesByIDLocked(seriesID); st != nil && arrSVisible(gate, st) {
		if show, ok := findShow(st.TmdbID); ok {
			for _, season := range show.Seasons {
				if seasonFilter >= 0 && season.SeasonNumber != seasonFilter {
					continue
				}
				for _, ep := range season.Episodes {
					docs = append(docs, arrSEpisodeJSON(st, show, season.SeasonNumber, ep, includeFile))
				}
			}
		}
	}
	arrMu.Unlock()
	writeJSON(w, http.StatusOK, docs)
}

func arrSServeQueue(w http.ResponseWriter, r *http.Request, gate *DemoUser) {
	page, size := arrPageParams(r)
	arrMu.Lock()
	records := make([]map[string]any, 0, len(arrSQueue))
	for _, q := range arrSQueue {
		// A queue row is judged by its embedded series (the real gate forces
		// includeSeries=true for exactly this).
		if gate != nil && !arrSVisible(gate, arrSSeriesByIDLocked(q.SeriesID)) {
			continue
		}
		records = append(records, arrSQueueItemJSON(q))
	}
	arrMu.Unlock()
	writeJSON(w, http.StatusOK, arrPaged(records, page, size))
}

// arrSServeQueueDetails serves the unpaged per-series queue (BARE array).
func arrSServeQueueDetails(w http.ResponseWriter, r *http.Request, gate *DemoUser) {
	seriesID := queryInt(r, "seriesId", 0)
	arrMu.Lock()
	records := make([]map[string]any, 0, len(arrSQueue))
	for _, q := range arrSQueue {
		if seriesID != 0 && q.SeriesID != seriesID {
			continue
		}
		if gate != nil && !arrSVisible(gate, arrSSeriesByIDLocked(q.SeriesID)) {
			continue
		}
		records = append(records, arrSQueueItemJSON(q))
	}
	arrMu.Unlock()
	writeJSON(w, http.StatusOK, records)
}

// arrSServeHistory serves the paged history; the integer eventType query
// filter works (3 = downloadFolderImported) while bodies carry string names.
func arrSServeHistory(w http.ResponseWriter, r *http.Request, gate *DemoUser) {
	page, size := arrPageParams(r)
	eventFilter := ""
	switch r.URL.Query().Get("eventType") {
	case "1":
		eventFilter = "grabbed"
	case "3":
		eventFilter = "downloadFolderImported"
	}
	arrMu.Lock()
	sorted := arrHistorySorted(arrSHistory)
	records := make([]map[string]any, 0, len(sorted))
	for _, rec := range sorted {
		if eventFilter != "" && rec.EventType != eventFilter {
			continue
		}
		// History rows carry seriesId but no embedded series; judging by the
		// record is what the real gate gets by forcing includeSeries=true.
		if gate != nil && !arrSVisible(gate, arrSSeriesByIDLocked(rec.SeriesID)) {
			continue
		}
		records = append(records, arrSHistoryJSON(rec))
	}
	arrMu.Unlock()
	writeJSON(w, http.StatusOK, arrPaged(records, page, size))
}

// arrSServeSeriesHistory serves GET /history/series?seriesId=
// [&seasonNumber=] as a BARE array.
func arrSServeSeriesHistory(w http.ResponseWriter, r *http.Request, gate *DemoUser) {
	seriesID := queryInt(r, "seriesId", 0)
	seasonFilter := queryInt(r, "seasonNumber", -1)
	arrMu.Lock()
	sorted := arrHistorySorted(arrSHistory)
	records := []map[string]any{}
	if gate != nil && !arrSVisible(gate, arrSSeriesByIDLocked(seriesID)) {
		sorted = nil // a blocked series has no visible history
	}
	for _, rec := range sorted {
		if rec.SeriesID != seriesID {
			continue
		}
		if seasonFilter >= 0 && rec.EpisodeID != 0 {
			if _, _, seasonNumber, _, ok := arrSFindEpisodeLocked(rec.EpisodeID); ok && seasonNumber != seasonFilter {
				continue
			}
		}
		records = append(records, arrSHistoryJSON(rec))
	}
	arrMu.Unlock()
	writeJSON(w, http.StatusOK, records)
}

// arrSServeWanted serves wanted/missing and wanted/cutoff: paged episode
// records with the EMBEDDED full series document.
func arrSServeWanted(w http.ResponseWriter, r *http.Request, gate *DemoUser, cutoff bool) {
	page, size := arrPageParams(r)
	now := time.Now().UTC()
	type row struct {
		doc  map[string]any
		date string
	}
	arrMu.Lock()
	rows := []row{}
	for _, st := range arrSSeries {
		// Judged by the embedded series, like the real gate.
		if !st.Monitored || !arrSVisible(gate, st) {
			continue
		}
		show, ok := findShow(st.TmdbID)
		if !ok {
			continue
		}
		seriesDoc := arrSSeriesJSON(st)
		for _, season := range show.Seasons {
			for _, ep := range season.Episodes {
				f := st.Files[ep.ID]
				if cutoff {
					if f == nil || !f.CutoffNotMet {
						continue
					}
				} else {
					air, ok := arrDate(ep.AirDate)
					if f != nil || !ok || air.After(now) || !arrSEpMonitored(st, ep.ID) {
						continue
					}
				}
				doc := arrSEpisodeJSON(st, show, season.SeasonNumber, ep, cutoff)
				doc["series"] = seriesDoc
				rows = append(rows, row{doc: doc, date: arrSAirUTC(ep.AirDate)})
			}
		}
	}
	arrMu.Unlock()
	// sortKey=episodes.airDateUtc descending.
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].date > rows[j].date })
	records := make([]map[string]any, 0, len(rows))
	for _, rw := range rows {
		records = append(records, rw.doc)
	}
	writeJSON(w, http.StatusOK, arrPaged(records, page, size))
}

// arrSServeCalendar returns a BARE array of episodes airing inside
// [start, end], each with the embedded series document.
func arrSServeCalendar(w http.ResponseWriter, r *http.Request, gate *DemoUser) {
	start, end := arrParseWindow(r.URL.Query().Get("start"), r.URL.Query().Get("end"))
	arrMu.Lock()
	docs := []map[string]any{}
	for _, st := range arrSSeries {
		show, ok := findShow(st.TmdbID)
		if !ok || !arrSVisible(gate, st) {
			continue
		}
		var seriesDoc map[string]any
		for _, season := range show.Seasons {
			for _, ep := range season.Episodes {
				air, ok := arrDate(ep.AirDate)
				if !ok || air.Before(start) || air.After(end) {
					continue
				}
				if seriesDoc == nil {
					seriesDoc = arrSSeriesJSON(st)
				}
				doc := arrSEpisodeJSON(st, show, season.SeasonNumber, ep, false)
				doc["series"] = seriesDoc
				docs = append(docs, doc)
			}
		}
	}
	arrMu.Unlock()
	writeJSON(w, http.StatusOK, docs)
}

// arrSServeReleases serves the interactive search for
// ?seriesId=&seasonNumber= (season pack) or ?episodeId= (single episode).
func arrSServeReleases(w http.ResponseWriter, r *http.Request) {
	seriesID := queryInt(r, "seriesId", 0)
	seasonNumber := queryInt(r, "seasonNumber", -1)
	episodeID := queryInt(r, "episodeId", 0)

	arrMu.Lock()
	label := ""
	runtime := 40
	if episodeID != 0 {
		if st, show, sn, ep, ok := arrSFindEpisodeLocked(episodeID); ok {
			seriesID = st.ID
			label = arrDotted(show.Name) + ".S" + pad2(sn) + "E" + pad2(ep.EpisodeNumber)
			runtime = ep.Runtime
		}
	} else if st := arrSSeriesByIDLocked(seriesID); st != nil {
		if show, ok := findShow(st.TmdbID); ok {
			label = arrDotted(show.Name)
			if seasonNumber >= 0 {
				label += ".S" + pad2(seasonNumber)
				runtime = 40 * 10 // season pack
			}
		}
	}
	arrMu.Unlock()
	if label == "" {
		writeJSON(w, http.StatusOK, []map[string]any{})
		return
	}

	type relSeed struct {
		quality  string
		protocol string
		seeders  int
		leechers int
		ageDays  int
		rejected bool
		rejects  []any
	}
	seeds := []relSeed{
		{quality: "Bluray-1080p", protocol: "torrent", seeders: 35, leechers: 2, ageDays: 60, rejects: []any{}},
		{quality: "WEBDL-1080p", protocol: "usenet", ageDays: 21, rejects: []any{}},
		{quality: "WEBDL-720p", protocol: "usenet", ageDays: 90,
			rejected: true, rejects: []any{"Existing file meets cutoff"}},
		{quality: "HDTV-720p", protocol: "torrent", seeders: 4, leechers: 0, ageDays: 200,
			rejected: true, rejects: []any{map[string]any{"reason": "Not an upgrade for existing file"}}},
	}
	releases := make([]map[string]any, 0, len(seeds))
	for i, s := range seeds {
		rel := map[string]any{
			"guid": "demo-sonarr-" + strconv.Itoa(seriesID) + "-" +
				strconv.Itoa(seasonNumber) + "-" + strconv.Itoa(i+1),
			"indexerId":   i%2 + 1,
			"indexer":     map[bool]string{true: "DemoTorrents", false: "DemoNZB"}[s.protocol == "torrent"],
			"title":       label + "." + s.quality + ".x264-DEMO",
			"size":        arrEpisodeSize(runtime, s.quality),
			"age":         s.ageDays,
			"ageHours":    float64(s.ageDays)*24 + 0.5,
			"protocol":    s.protocol,
			"quality":     arrQualityBlob(s.quality),
			"languages":   arrLangs(),
			"rejected":    s.rejected,
			"rejections":  s.rejects,
			"publishDate": arrISO(time.Now().UTC().AddDate(0, 0, -s.ageDays)),
		}
		if s.protocol == "torrent" {
			rel["seeders"] = s.seeders
			rel["leechers"] = s.leechers
		}
		releases = append(releases, rel)
	}
	writeJSON(w, http.StatusOK, releases)
}

// arrSServeManualImport serves Import Doctor candidates keyed by series.id
// and episodes[] for ?downloadId=.
func arrSServeManualImport(w http.ResponseWriter, r *http.Request) {
	downloadID := r.URL.Query().Get("downloadId")
	arrMu.Lock()
	var item *arrQueueItem
	for _, q := range arrSQueue {
		if q.DownloadID == downloadID {
			item = q
			break
		}
	}
	candidates := []map[string]any{}
	if item != nil {
		if st, show, seasonNumber, ep, ok := arrSFindEpisodeLocked(item.EpisodeID); ok {
			candidates = append(candidates, map[string]any{
				"id":           1,
				"path":         item.OutputPath + "/" + item.Title + ".mkv",
				"relativePath": item.Title + ".mkv",
				"name":         item.Title + ".mkv",
				"folderName":   item.Title,
				"size":         item.Size,
				"series":       arrSSeriesJSON(st),
				"seasonNumber": seasonNumber,
				"episodes": []map[string]any{{
					"id":            ep.ID,
					"seasonNumber":  seasonNumber,
					"episodeNumber": ep.EpisodeNumber,
					"title":         ep.Name,
				}},
				"quality":      arrQualityBlob(item.QualityName),
				"languages":    arrLangs(),
				"releaseGroup": "DEMO",
				"downloadId":   item.DownloadID,
				"indexerFlags": 0,
				"releaseType":  "singleEpisode",
				"rejections":   []map[string]any{},
			})
			_ = show
		}
	}
	arrMu.Unlock()
	writeJSON(w, http.StatusOK, candidates)
}

// ─── Mutating handlers ──────────────────────────────────

// arrSUpdateSeries accepts the app's whole-document PUT and applies the
// editable fields (monitored, seasonFolder, qualityProfileId, seriesType,
// path, tags, seasons[].monitored), echoing the updated document.
func arrSUpdateSeries(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid series body")
		return
	}
	arrMu.Lock()
	st := arrSSeriesByIDLocked(id)
	var doc map[string]any
	if st != nil {
		if v, ok := body["monitored"].(bool); ok {
			st.Monitored = v
		}
		if v, ok := body["seasonFolder"].(bool); ok {
			st.SeasonFolder = v
		}
		if v, ok := body["qualityProfileId"].(float64); ok && int(v) > 0 {
			st.QualityProfileID = int(v)
		}
		if v, ok := body["seriesType"].(string); ok && v != "" {
			st.SeriesType = v
		}
		if v, ok := body["path"].(string); ok && v != "" {
			st.Path = v
		}
		if raw, ok := body["tags"].([]any); ok {
			tags := []int{}
			for _, t := range raw {
				if n, ok := t.(float64); ok {
					tags = append(tags, int(n))
				}
			}
			st.Tags = tags
		}
		if raw, ok := body["seasons"].([]any); ok {
			for _, s := range raw {
				season, ok := s.(map[string]any)
				if !ok {
					continue
				}
				num, ok := season["seasonNumber"].(float64)
				if !ok {
					continue
				}
				if mon, ok := season["monitored"].(bool); ok {
					st.SeasonMonitored[int(num)] = mon
				}
			}
		}
		doc = arrSSeriesJSON(st)
	}
	arrMu.Unlock()
	if doc == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func arrSDeleteSeries(w http.ResponseWriter, idStr string) {
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	arrMu.Lock()
	kept := arrSSeries[:0]
	for _, st := range arrSSeries {
		if st.ID == id {
			delete(arrSSeriesByTmdb, st.TmdbID)
			continue
		}
		kept = append(kept, st)
	}
	arrSSeries = kept
	arrMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{})
}

// arrSMonitorEpisodes handles PUT /episode/monitor {episodeIds, monitored}.
func arrSMonitorEpisodes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		EpisodeIDs []int `json:"episodeIds"`
		Monitored  bool  `json:"monitored"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	arrMu.Lock()
	docs := []map[string]any{}
	for _, epID := range body.EpisodeIDs {
		if st, show, seasonNumber, ep, ok := arrSFindEpisodeLocked(epID); ok {
			st.EpMonitored[epID] = body.Monitored
			docs = append(docs, arrSEpisodeJSON(st, show, seasonNumber, ep, false))
		}
	}
	arrMu.Unlock()
	writeJSON(w, http.StatusOK, docs)
}

// arrSDeleteEpisodeFiles handles DELETE /episodefile/bulk {episodeFileIds}.
func arrSDeleteEpisodeFiles(w http.ResponseWriter, r *http.Request) {
	var body struct {
		EpisodeFileIDs []int `json:"episodeFileIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	arrMu.Lock()
	for _, fileID := range body.EpisodeFileIDs {
		epID := arrEpisodeFromFileID(fileID)
		if epID == 0 {
			continue
		}
		for _, st := range arrSSeries {
			delete(st.Files, epID)
		}
	}
	arrMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{})
}

func arrSDeleteQueue(w http.ResponseWriter, idStr string, inst *DemoInstance) {
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	arrMu.Lock()
	kept := arrSQueue[:0]
	removed := false
	for _, q := range arrSQueue {
		if q.ID == id {
			removed = true
			continue
		}
		kept = append(kept, q)
	}
	arrSQueue = kept
	arrMu.Unlock()
	if removed {
		arrEmitQueueChanged(inst.ID, serviceSonarr)
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

// arrSGrabRelease handles POST /release {guid, indexerId}: a no-op grab
// that seeds a queue item for the targeted series.
func arrSGrabRelease(w http.ResponseWriter, r *http.Request, inst *DemoInstance) {
	var body struct {
		GUID      string `json:"guid"`
		IndexerID int    `json:"indexerId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.GUID == "" {
		writeErr(w, http.StatusBadRequest, "guid is required")
		return
	}
	// guid format: demo-sonarr-<seriesID>-<seasonNumber>-<n>.
	changed := false
	if parts := strings.Split(body.GUID, "-"); len(parts) == 5 && parts[0] == "demo" && parts[1] == "sonarr" {
		if seriesID, err := strconv.Atoi(parts[2]); err == nil {
			arrMu.Lock()
			st := arrSSeriesByIDLocked(seriesID)
			var tmdbID int
			if st != nil {
				tmdbID = st.TmdbID
			}
			arrMu.Unlock()
			if tmdbID != 0 {
				changed = arrStartSeriesDownload(tmdbID)
			}
		}
	}
	if changed {
		arrEmitQueueChanged(inst.ID, serviceSonarr)
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

// arrSCommand handles POST /command. ManualImport and
// ProcessMonitoredDownloads actually import; the search/refresh/rescan
// commands are accepted no-ops.
func arrSCommand(w http.ResponseWriter, r *http.Request, inst *DemoInstance) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid command body")
		return
	}
	name, _ := body["name"].(string)
	if name == "" {
		writeErr(w, http.StatusBadRequest, "command name is required")
		return
	}
	changed := false
	switch name {
	case "ManualImport":
		files, _ := body["files"].([]any)
		changed = arrSApplyManualImport(files)
	case "ProcessMonitoredDownloads":
		changed = arrSProcessMonitored()
	case "SeriesSearch", "RefreshSeries", "SeasonSearch", "EpisodeSearch", "RescanSeries":
		// Accepted no-ops.
	}
	if changed {
		arrEmitQueueChanged(inst.ID, serviceSonarr)
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":          1,
		"name":        name,
		"commandName": name,
		"status":      "queued",
		"queued":      arrISO(time.Now().UTC()),
		"trigger":     "manual",
	})
}

// arrSApplyManualImport imports the ManualImport command's files: each entry
// with episodeIds gains episode files and clears matching queue items.
func arrSApplyManualImport(files []any) bool {
	changed := false
	arrMu.Lock()
	for _, f := range files {
		entry, ok := f.(map[string]any)
		if !ok {
			continue
		}
		downloadID, _ := entry["downloadId"].(string)
		rawIDs, _ := entry["episodeIds"].([]any)
		for _, raw := range rawIDs {
			n, ok := raw.(float64)
			if !ok {
				continue
			}
			if arrSImportEpisodeLocked(int(n), downloadID) {
				changed = true
			}
		}
	}
	arrMu.Unlock()
	return changed
}

// arrSProcessMonitored imports every import-pending sonarr queue item.
func arrSProcessMonitored() bool {
	changed := false
	arrMu.Lock()
	pending := make([]*arrQueueItem, 0, len(arrSQueue))
	for _, q := range arrSQueue {
		if q.TrackedDownloadState == "importPending" && q.EpisodeID != 0 {
			pending = append(pending, q)
		}
	}
	for _, q := range pending {
		if arrSImportEpisodeLocked(q.EpisodeID, q.DownloadID) {
			changed = true
		}
	}
	arrMu.Unlock()
	return changed
}

// arrSImportEpisodeLocked gives an episode a file, drops matching queue
// items, and appends the import history record. Call under arrMu.
func arrSImportEpisodeLocked(episodeID int, downloadID string) bool {
	st, show, seasonNumber, ep, ok := arrSFindEpisodeLocked(episodeID)
	if !ok {
		return false
	}
	now := time.Now().UTC()
	quality := "WEBDL-1080p"
	title := arrDotted(show.Name) + ".S" + pad2(seasonNumber) + "E" + pad2(ep.EpisodeNumber) + "." + quality + "-DEMO"
	kept := arrSQueue[:0]
	for _, q := range arrSQueue {
		if q.EpisodeID == episodeID || (downloadID != "" && q.DownloadID == downloadID) {
			quality = q.QualityName
			title = q.Title
			if downloadID == "" {
				downloadID = q.DownloadID
			}
			continue
		}
		kept = append(kept, q)
	}
	arrSQueue = kept
	st.Files[episodeID] = &arrEpisodeFile{
		Size:        arrEpisodeSize(ep.Runtime, quality),
		QualityName: quality,
		DateAdded:   now,
	}
	arrSHistory = append(arrSHistory, arrNewHistory(&arrHistoryRec{
		SeriesID: st.ID, EpisodeID: episodeID, SourceTitle: title,
		EventType: "downloadFolderImported", QualityName: quality,
		DownloadID: downloadID, Date: now,
	}))
	return true
}
