// arr_radarr.go — the fake Radarr v3 API served behind the instance proxy
// (/api/instances/{radarrID}/api/v3/...). Pure handlers: authorization and
// the non-admin read allowlist are enforced by the proxy dispatcher before
// handleRadarrProxy runs. Every response is application/json; JSON exactness
// rules (integer counters, fractional ratings.value, non-null arrays) are
// the whole game — see contract.md §1 and app-arr.md §4.
//
// Kids accounts: the real server's proxy gate (proxy/contentgate.go) cuts a
// child's Radarr reads to the records their content policy allows — array
// elements and paged records are dropped, a single blocked record answers
// the arr's own 404. The GET handlers here take the gated user (nil for
// admins and unrestricted users) and apply the same rule through
// policyAllowsMovieRecord, resolving each record's title via findMovie.
package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// handleRadarrProxy routes rest (the path after /api/instances/{id}/, e.g.
// "api/v3/movie") for a radarr instance. Contract hook — exact signature.
func handleRadarrProxy(w http.ResponseWriter, r *http.Request, inst *DemoInstance, isAdmin bool, rest string) {
	arrEnsureSeeded()
	if !strings.HasPrefix(rest, "api/v3/") {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	path := strings.Trim(strings.TrimPrefix(rest, "api/v3/"), "/")

	// The content gate. Admins are never kids accounts and unrestricted
	// users pass every check, so the handlers only ever consult the policy
	// for a non-admin — the policy hooks answer "allowed" for everyone else.
	var gate *DemoUser
	if !isAdmin {
		gate = userFrom(r)
	}

	switch r.Method {
	case http.MethodGet:
		switch {
		case path == "movie":
			arrRServeMovies(w, inst, gate)
		case strings.HasPrefix(path, "movie/"):
			arrRServeMovieByID(w, inst, gate, strings.TrimPrefix(path, "movie/"))
		case path == "queue":
			arrRServeQueue(w, r, gate)
		case path == "queue/details":
			arrRServeQueueDetails(w, r, gate)
		case path == "history":
			arrRServeHistory(w, r, gate)
		case path == "history/movie":
			arrRServeMovieHistory(w, r, gate)
		case path == "wanted/missing":
			arrRServeWanted(w, r, gate, false)
		case path == "wanted/cutoff":
			arrRServeWanted(w, r, gate, true)
		case path == "calendar":
			arrRServeCalendar(w, r, gate)
		case path == "release":
			arrRServeReleases(w, r)
		case path == "manualimport":
			arrRServeManualImport(w, r)
		case path == "qualityprofile":
			writeJSON(w, http.StatusOK, arrQualityProfilesJSON())
		case path == "tag":
			writeJSON(w, http.StatusOK, arrRadarrTagsJSON())
		case path == "rootfolder":
			writeJSON(w, http.StatusOK, []map[string]any{{
				"id": 1, "path": "/movies", "accessible": true,
				"freeSpace": int64(3_500_000_000_000), "unmappedFolders": []map[string]any{},
			}})
		default:
			writeErr(w, http.StatusNotFound, "not found")
		}
	case http.MethodPut:
		switch {
		case strings.HasPrefix(path, "movie/"):
			arrRUpdateMovie(w, r, inst, strings.TrimPrefix(path, "movie/"))
		default:
			writeErr(w, http.StatusNotFound, "not found")
		}
	case http.MethodPost:
		switch path {
		case "release":
			arrRGrabRelease(w, r, inst)
		case "command":
			arrRCommand(w, r, inst)
		default:
			writeErr(w, http.StatusNotFound, "not found")
		}
	case http.MethodDelete:
		switch {
		case strings.HasPrefix(path, "queue/"):
			arrRDeleteQueue(w, strings.TrimPrefix(path, "queue/"), inst)
		case strings.HasPrefix(path, "movie/"):
			arrRDeleteMovie(w, strings.TrimPrefix(path, "movie/"))
		default:
			writeErr(w, http.StatusNotFound, "not found")
		}
	default:
		writeErr(w, http.StatusNotFound, "not found")
	}
}

// arrMovieReleaseDates is the cross-domain read of a movie's theatrical and
// digital dates, as plain YYYY-MM-DD calendar dates. Both are "" when the
// library holds no record for the title — an unadded movie has no arr record
// to read dates off, which is absence of a record, not absence of a release.
func arrMovieReleaseDates(tmdbID int) (inCinemas, digital string) {
	arrEnsureSeeded()
	arrMu.Lock()
	defer arrMu.Unlock()
	m := arrRMovieByTmdb[tmdbID]
	if m == nil {
		return "", ""
	}
	return arrCalendarDate(m.InCinemas), arrCalendarDate(m.Digital)
}

// arrMovieReleaseMilestones is the cross-domain read of every release
// milestone the Radarr fixture holds for a title (theatrical, digital,
// physical) as plain YYYY-MM-DD dates, "" when absent. The title page's
// release-dates section reads these so it agrees with the Radarr calendar.
func arrMovieReleaseMilestones(tmdbID int) (inCinemas, digital, physical string) {
	arrEnsureSeeded()
	arrMu.Lock()
	defer arrMu.Unlock()
	m := arrRMovieByTmdb[tmdbID]
	if m == nil {
		return "", "", ""
	}
	return arrCalendarDate(m.InCinemas), arrCalendarDate(m.Digital), arrCalendarDate(m.Physical)
}

// arrCalendarDate trims an RFC3339 instant to its YYYY-MM-DD date. A release
// date has no time of day; serialising one as an instant invites a client to
// localise it and land a day early or late.
func arrCalendarDate(iso string) string {
	if len(iso) < 10 {
		return ""
	}
	return iso[:10]
}

// ─── Content gate ───────────────────────────────────────

// arrRVisible reports whether a library record may be shown to gate (nil =
// no gate, everything is visible). The real proxy gate judges the record's
// own certification and genres; the demo resolves the same title through the
// catalog and asks the policy. A record the catalog cannot resolve has
// nothing to judge: it is hidden from a kids account (fail closed) and shown
// to everyone else. A missing record (a queue or history row whose movie is
// gone) cannot be judged either and is dropped, like the real gate drops
// parentless rows.
func arrRVisible(gate *DemoUser, m *arrRadarrMovie) bool {
	if gate == nil {
		return true
	}
	if m == nil {
		return false
	}
	mv, ok := findMovie(m.TmdbID)
	if !ok {
		return !cpIsChild(gate.ID)
	}
	return policyAllowsMovieRecord(gate, mv)
}

// ─── Document renderers ─────────────────────────────────

// arrRMovieJSON builds one full, round-trippable Radarr movie document
// (the app GETs it, merges its edits, and PUTs the whole thing back). Call
// under arrMu.
func arrRMovieJSON(m *arrRadarrMovie) map[string]any {
	mv, ok := findMovie(m.TmdbID)
	if !ok {
		mv = &DemoMovie{TmdbID: m.TmdbID, Title: "Unknown"}
	}
	genres := make([]string, 0, len(mv.Genres))
	for _, g := range mv.Genres {
		genres = append(genres, g.Name)
	}
	path := m.Path
	if path == "" {
		path = arrMoviePath(mv)
	}
	availability := m.MinimumAvailability
	if availability == "" {
		availability = "released"
	}
	profileID := m.QualityProfileID
	if profileID == 0 {
		profileID = arrDefaultQualityProfileID
	}
	doc := map[string]any{
		"id":                  m.ID,
		"title":               mv.Title,
		"originalTitle":       mv.OriginalTitle,
		"sortTitle":           strings.ToLower(mv.Title),
		"year":                mv.Year(),
		"tmdbId":              m.TmdbID,
		"imdbId":              mv.ImdbID,
		"overview":            mv.Overview,
		"titleSlug":           strconv.Itoa(m.TmdbID),
		"monitored":           m.Monitored,
		"hasFile":             m.HasFile,
		"isAvailable":         m.IsAvailable,
		"path":                path,
		"folderName":          path,
		"runtime":             mv.Runtime,
		"minimumAvailability": availability,
		"qualityProfileId":    profileID,
		"sizeOnDisk":          m.FileSize,
		"status":              "released",
		"added":               arrISO(m.Added),
		"studio":              "",
		"genres":              genres,
		"tags":                append([]int{}, m.Tags...),
		"images":              arrImages(mv.PosterPath, mv.BackdropPath),
		"ratings": map[string]any{
			"votes": mv.VoteCount,
			"value": arrFrac(mv.VoteAverage),
			"type":  "user",
		},
	}
	if m.InCinemas != "" {
		doc["inCinemas"] = m.InCinemas
	}
	if m.Digital != "" {
		doc["digitalRelease"] = m.Digital
	}
	if m.Physical != "" {
		doc["physicalRelease"] = m.Physical
	}
	if m.HasFile {
		doc["movieFile"] = arrRMovieFileJSON(m, mv)
	}
	return doc
}

// arrRMovieFileJSON builds the movieFile object (id is a hard cast
// app-side, path feeds the media-files coverage check).
func arrRMovieFileJSON(m *arrRadarrMovie, mv *DemoMovie) map[string]any {
	name := arrMovieFileName(mv, m.QualityName)
	path := m.Path
	if path == "" {
		path = arrMoviePath(mv)
	}
	return map[string]any{
		"id":                  9000 + m.ID,
		"movieId":             m.ID,
		"relativePath":        name,
		"path":                path + "/" + name,
		"size":                m.FileSize,
		"dateAdded":           arrISO(m.FileDate),
		"quality":             arrQualityBlob(m.QualityName),
		"qualityCutoffNotMet": m.CutoffNotMet,
		"releaseGroup":        "DEMO",
		"languages":           arrLangs(),
	}
}

// arrRQueueItemJSON renders one queue row with the movie join. Call under arrMu.
func arrRQueueItemJSON(q *arrQueueItem) map[string]any {
	doc := arrQueueBaseJSON(q)
	doc["movieId"] = q.MovieID
	if m := arrRMovieByIDLocked(q.MovieID); m != nil {
		doc["movie"] = arrRMovieJSON(m)
	}
	return doc
}

func arrRMovieByIDLocked(id int) *arrRadarrMovie {
	for _, m := range arrRMovies {
		if m.ID == id {
			return m
		}
	}
	return nil
}

func arrRHistoryJSON(rec *arrHistoryRec) map[string]any {
	doc := arrHistoryBaseJSON(rec)
	doc["movieId"] = rec.MovieID
	return doc
}

// ─── GET handlers ───────────────────────────────────────

func arrRServeMovies(w http.ResponseWriter, inst *DemoInstance, gate *DemoUser) {
	arrMu.Lock()
	docs := make([]map[string]any, 0, len(arrRMovies))
	for _, m := range arrRMovies {
		if !arrInSiblingLibrary(inst, m.TmdbID) || !arrRVisible(gate, m) {
			continue
		}
		docs = append(docs, arrRMovieJSON(m))
	}
	arrMu.Unlock()
	writeJSON(w, http.StatusOK, docs)
}

// arrInSiblingLibrary decides whether a title is in THIS library. The demo
// runs one fixture set, so a second library of a type would otherwise be an
// exact copy of the first and the Library chooser would have nothing to say.
// A non-default sibling therefore holds a stable subset: enough to look like
// a real second library, and enough for the per-library chips to differ.
func arrInSiblingLibrary(inst *DemoInstance, id int) bool {
	if inst == nil || inst.IsDefault {
		return true
	}
	switch inst.ServiceType {
	case serviceRadarr, serviceSonarr:
		return id%3 == 0
	}
	return true
}

// arrRMovieInLibraryLocked resolves a movie id within THIS library: nil when
// the id is unknown or the title is not part of the sibling's subset. Call
// under arrMu.
func arrRMovieInLibraryLocked(inst *DemoInstance, id int) *arrRadarrMovie {
	m := arrRMovieByIDLocked(id)
	// A library that does not list a title must not serve it by id either,
	// or the drill-down contradicts the list it was opened from.
	if m != nil && !arrInSiblingLibrary(inst, m.TmdbID) {
		return nil
	}
	return m
}

func arrRServeMovieByID(w http.ResponseWriter, inst *DemoInstance, gate *DemoUser, idStr string) {
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	arrMu.Lock()
	m := arrRMovieInLibraryLocked(inst, id)
	var doc map[string]any
	// A blocked record answers exactly what an absent one does, so a kids
	// account cannot tell the two apart (the real gate's 404 is byte-identical
	// to the arr's own).
	if m != nil && arrRVisible(gate, m) {
		doc = arrRMovieJSON(m)
	}
	arrMu.Unlock()
	if doc == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func arrRServeQueue(w http.ResponseWriter, r *http.Request, gate *DemoUser) {
	page, size := arrPageParams(r)
	arrMu.Lock()
	records := make([]map[string]any, 0, len(arrRQueue))
	for _, q := range arrRQueue {
		// A queue row is judged by its embedded movie (the real gate forces
		// includeMovie=true for exactly this).
		if gate != nil && !arrRVisible(gate, arrRMovieByIDLocked(q.MovieID)) {
			continue
		}
		records = append(records, arrRQueueItemJSON(q))
	}
	arrMu.Unlock()
	writeJSON(w, http.StatusOK, arrPaged(records, page, size))
}

func arrRServeQueueDetails(w http.ResponseWriter, r *http.Request, gate *DemoUser) {
	movieID := queryInt(r, "movieId", 0)
	arrMu.Lock()
	records := make([]map[string]any, 0, len(arrRQueue))
	for _, q := range arrRQueue {
		if movieID != 0 && q.MovieID != movieID {
			continue
		}
		if gate != nil && !arrRVisible(gate, arrRMovieByIDLocked(q.MovieID)) {
			continue
		}
		records = append(records, arrRQueueItemJSON(q))
	}
	arrMu.Unlock()
	writeJSON(w, http.StatusOK, records)
}

func arrRServeHistory(w http.ResponseWriter, r *http.Request, gate *DemoUser) {
	page, size := arrPageParams(r)
	arrMu.Lock()
	sorted := arrHistorySorted(arrRHistory)
	records := make([]map[string]any, 0, len(sorted))
	for _, rec := range sorted {
		// History rows carry movieId but no embedded movie; judging by the
		// record is what the real gate gets by forcing includeMovie=true.
		if gate != nil && !arrRVisible(gate, arrRMovieByIDLocked(rec.MovieID)) {
			continue
		}
		records = append(records, arrRHistoryJSON(rec))
	}
	arrMu.Unlock()
	writeJSON(w, http.StatusOK, arrPaged(records, page, size))
}

func arrRServeMovieHistory(w http.ResponseWriter, r *http.Request, gate *DemoUser) {
	movieID := queryInt(r, "movieId", 0)
	arrMu.Lock()
	sorted := arrHistorySorted(arrRHistory)
	records := make([]map[string]any, 0, 8)
	if gate == nil || arrRVisible(gate, arrRMovieByIDLocked(movieID)) {
		for _, rec := range sorted {
			if rec.MovieID == movieID {
				records = append(records, arrRHistoryJSON(rec))
			}
		}
	}
	arrMu.Unlock()
	writeJSON(w, http.StatusOK, records) // bare array, not paged
}

// arrRServeWanted serves wanted/missing (cutoff=false) and wanted/cutoff
// (cutoff=true); records are full movie documents.
func arrRServeWanted(w http.ResponseWriter, r *http.Request, gate *DemoUser, cutoff bool) {
	page, size := arrPageParams(r)
	arrMu.Lock()
	matched := make([]*arrRadarrMovie, 0, len(arrRMovies))
	for _, m := range arrRMovies {
		if !arrRVisible(gate, m) {
			continue
		}
		if cutoff {
			if m.HasFile && m.CutoffNotMet {
				matched = append(matched, m)
			}
		} else if m.Monitored && !m.HasFile && m.IsAvailable {
			matched = append(matched, m)
		}
	}
	// sortKey=movieMetadata.inCinemas descending.
	for i := 0; i < len(matched); i++ {
		for j := i + 1; j < len(matched); j++ {
			if matched[j].InCinemas > matched[i].InCinemas {
				matched[i], matched[j] = matched[j], matched[i]
			}
		}
	}
	records := make([]map[string]any, 0, len(matched))
	for _, m := range matched {
		records = append(records, arrRMovieJSON(m))
	}
	arrMu.Unlock()
	writeJSON(w, http.StatusOK, arrPaged(records, page, size))
}

// arrRServeCalendar returns a BARE array of movie documents with a release
// date inside [start, end].
func arrRServeCalendar(w http.ResponseWriter, r *http.Request, gate *DemoUser) {
	start, end := arrParseWindow(r.URL.Query().Get("start"), r.URL.Query().Get("end"))
	inWindow := func(iso string) bool {
		if iso == "" {
			return false
		}
		t, err := time.Parse(time.RFC3339, iso)
		if err != nil {
			return false
		}
		return !t.Before(start) && !t.After(end)
	}
	arrMu.Lock()
	docs := make([]map[string]any, 0, len(arrRMovies))
	for _, m := range arrRMovies {
		if !arrRVisible(gate, m) {
			continue
		}
		if inWindow(m.InCinemas) || inWindow(m.Digital) || inWindow(m.Physical) {
			docs = append(docs, arrRMovieJSON(m))
		}
	}
	arrMu.Unlock()
	writeJSON(w, http.StatusOK, docs)
}

// arrRServeReleases serves the interactive search: a bare array of release
// objects for ?movieId=. Rejections deliberately exercise BOTH accepted
// shapes (["reason"] and [{"reason": …}]).
func arrRServeReleases(w http.ResponseWriter, r *http.Request) {
	movieID := queryInt(r, "movieId", 0)
	arrMu.Lock()
	m := arrRMovieByIDLocked(movieID)
	var tmdbID int
	if m != nil {
		tmdbID = m.TmdbID
	}
	arrMu.Unlock()
	if tmdbID == 0 {
		writeJSON(w, http.StatusOK, []map[string]any{})
		return
	}
	mv, ok := findMovie(tmdbID)
	if !ok {
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
		{quality: "Bluray-1080p", protocol: "torrent", seeders: 42, leechers: 3, ageDays: 120, rejects: []any{}},
		{quality: "WEBDL-1080p", protocol: "usenet", ageDays: 45, rejects: []any{}},
		{quality: "Bluray-720p", protocol: "torrent", seeders: 7, leechers: 1, ageDays: 300,
			rejected: true, rejects: []any{"Existing file meets cutoff"}},
		{quality: "HDTV-720p", protocol: "usenet", ageDays: 12,
			rejected: true, rejects: []any{map[string]any{"reason": "Not an upgrade for existing file"}}},
	}
	releases := make([]map[string]any, 0, len(seeds))
	for i, s := range seeds {
		rel := map[string]any{
			"guid":        "demo-radarr-" + strconv.Itoa(tmdbID) + "-" + strconv.Itoa(i+1),
			"indexerId":   i%2 + 1,
			"indexer":     map[bool]string{true: "DemoTorrents", false: "DemoNZB"}[s.protocol == "torrent"],
			"title":       arrReleaseTitle(mv.Title, mv.Year(), s.quality),
			"size":        arrMovieSize(mv.Runtime, s.quality),
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

// arrRServeManualImport serves Import Doctor candidates for ?downloadId=.
// The quality/languages blobs round-trip verbatim through POST /command
// ManualImport.
func arrRServeManualImport(w http.ResponseWriter, r *http.Request) {
	downloadID := r.URL.Query().Get("downloadId")
	arrMu.Lock()
	var item *arrQueueItem
	for _, q := range arrRQueue {
		if q.DownloadID == downloadID {
			item = q
			break
		}
	}
	candidates := make([]map[string]any, 0, 2)
	if item != nil {
		if m := arrRMovieByIDLocked(item.MovieID); m != nil {
			movieDoc := arrRMovieJSON(m)
			candidates = append(candidates,
				map[string]any{
					"id":           1,
					"path":         item.OutputPath + "/" + item.Title + ".mkv",
					"relativePath": item.Title + ".mkv",
					"name":         item.Title + ".mkv",
					"folderName":   item.Title,
					"size":         item.Size,
					"movie":        movieDoc,
					"quality":      arrQualityBlob(item.QualityName),
					"languages":    arrLangs(),
					"releaseGroup": "DEMO",
					"downloadId":   item.DownloadID,
					"indexerFlags": 0,
					"rejections":   []map[string]any{},
				},
				map[string]any{
					"id":           2,
					"path":         item.OutputPath + "/sample.mkv",
					"relativePath": "sample.mkv",
					"name":         "sample.mkv",
					"folderName":   item.Title,
					"size":         int64(52_428_800),
					"movie":        movieDoc,
					"quality":      arrQualityBlob(item.QualityName),
					"languages":    arrLangs(),
					"releaseGroup": "DEMO",
					"downloadId":   item.DownloadID,
					"indexerFlags": 0,
					"rejections": []map[string]any{
						{"reason": "Sample file", "type": "permanent"},
					},
				},
			)
		}
	}
	arrMu.Unlock()
	writeJSON(w, http.StatusOK, candidates)
}

// ─── Mutating handlers ──────────────────────────────────

// arrRUpdateMovie handles the app's whole-document PUT /movie/{id}
// (updateMovieFields GETs the document, merges its edits, and PUTs the whole
// thing back, movieFile/images/ratings included). Unknown keys are ignored;
// the editable fields — monitored, qualityProfileId, minimumAvailability,
// path, tags — are applied, and the re-rendered document answers with
// Radarr's own 202. Nothing is emitted on WS: the movie page reloads itself.
func arrRUpdateMovie(w http.ResponseWriter, r *http.Request, inst *DemoInstance, idStr string) {
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid movie body")
		return
	}
	arrMu.Lock()
	m := arrRMovieInLibraryLocked(inst, id)
	var doc map[string]any
	if m != nil {
		if v, ok := body["monitored"].(bool); ok {
			m.Monitored = v
		}
		if v, ok := body["qualityProfileId"].(float64); ok && int(v) > 0 {
			m.QualityProfileID = int(v)
		}
		if v, ok := body["minimumAvailability"].(string); ok && v != "" {
			m.MinimumAvailability = v
		}
		if v, ok := body["path"].(string); ok && v != "" {
			m.Path = v
		}
		if raw, ok := body["tags"].([]any); ok {
			tags := []int{}
			for _, t := range raw {
				if n, ok := t.(float64); ok {
					tags = append(tags, int(n))
				}
			}
			m.Tags = tags
		}
		doc = arrRMovieJSON(m)
	}
	arrMu.Unlock()
	if doc == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusAccepted, doc)
}

// arrRGrabRelease handles POST /release {guid, indexerId}: a no-op grab
// that seeds a queue item so the Queue tab shows the grabbed release.
func arrRGrabRelease(w http.ResponseWriter, r *http.Request, inst *DemoInstance) {
	var body struct {
		GUID      string `json:"guid"`
		IndexerID int    `json:"indexerId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.GUID == "" {
		writeErr(w, http.StatusBadRequest, "guid is required")
		return
	}
	// guid format: demo-radarr-<tmdbID>-<n>.
	changed := false
	if parts := strings.Split(body.GUID, "-"); len(parts) == 4 && parts[0] == "demo" && parts[1] == "radarr" {
		if tmdbID, err := strconv.Atoi(parts[2]); err == nil {
			changed = arrStartMovieDownload(tmdbID)
			if changed {
				arrMu.Lock()
				if m := arrRMovieByTmdb[tmdbID]; m != nil {
					mv, ok := findMovie(tmdbID)
					if ok {
						arrRHistory = append(arrRHistory, arrNewHistory(&arrHistoryRec{
							MovieID:     m.ID,
							SourceTitle: arrReleaseTitle(mv.Title, mv.Year(), "WEBDL-1080p"),
							EventType:   "grabbed", QualityName: "WEBDL-1080p",
							DownloadID: "SABnzbd_nzo_grab" + strconv.Itoa(m.ID),
							Date:       time.Now().UTC(),
						}))
					}
				}
				arrMu.Unlock()
			}
		}
	}
	if changed {
		arrEmitQueueChanged(inst.ID, serviceRadarr)
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

// arrRCommand handles POST /command. ManualImport and
// ProcessMonitoredDownloads actually import (so the Import Doctor showcase
// resolves the stuck item); the search/refresh/rescan commands are accepted
// no-ops.
func arrRCommand(w http.ResponseWriter, r *http.Request, inst *DemoInstance) {
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
		changed = arrRApplyManualImport(files)
	case "ProcessMonitoredDownloads":
		changed = arrRProcessMonitored()
	case "MoviesSearch", "RefreshMovie", "RescanMovie":
		// Accepted no-ops.
	}
	if changed {
		arrEmitQueueChanged(inst.ID, serviceRadarr)
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

// arrRApplyManualImport imports the ManualImport command's files: each entry
// with a movieId flips that movie to hasFile and clears its queue item.
func arrRApplyManualImport(files []any) bool {
	changed := false
	arrMu.Lock()
	for _, f := range files {
		entry, ok := f.(map[string]any)
		if !ok {
			continue
		}
		movieID := 0
		if v, ok := entry["movieId"].(float64); ok {
			movieID = int(v)
		}
		downloadID, _ := entry["downloadId"].(string)
		if movieID == 0 && downloadID != "" {
			for _, q := range arrRQueue {
				if q.DownloadID == downloadID {
					movieID = q.MovieID
					break
				}
			}
		}
		if movieID == 0 {
			continue
		}
		if arrRImportLocked(movieID, downloadID) {
			changed = true
		}
	}
	arrMu.Unlock()
	return changed
}

// arrRProcessMonitored imports every import-pending queue item.
func arrRProcessMonitored() bool {
	changed := false
	arrMu.Lock()
	pending := make([]*arrQueueItem, 0, len(arrRQueue))
	for _, q := range arrRQueue {
		if q.TrackedDownloadState == "importPending" && q.MovieID != 0 {
			pending = append(pending, q)
		}
	}
	for _, q := range pending {
		if arrRImportLocked(q.MovieID, q.DownloadID) {
			changed = true
		}
	}
	arrMu.Unlock()
	return changed
}

// arrRImportLocked marks a movie imported, drops its queue items, and
// appends the import history record. Call under arrMu.
func arrRImportLocked(movieID int, downloadID string) bool {
	m := arrRMovieByIDLocked(movieID)
	if m == nil {
		return false
	}
	mv, ok := findMovie(m.TmdbID)
	if !ok {
		return false
	}
	now := time.Now().UTC()
	quality := "WEBDL-1080p"
	title := arrReleaseTitle(mv.Title, mv.Year(), quality)
	kept := arrRQueue[:0]
	for _, q := range arrRQueue {
		if q.MovieID == movieID {
			quality = q.QualityName
			title = q.Title
			if downloadID == "" {
				downloadID = q.DownloadID
			}
			continue
		}
		kept = append(kept, q)
	}
	arrRQueue = kept
	m.HasFile = true
	m.IsAvailable = true
	m.QualityName = quality
	m.CutoffNotMet = false
	m.FileSize = arrMovieSize(mv.Runtime, quality)
	m.FileDate = now
	arrRHistory = append(arrRHistory, arrNewHistory(&arrHistoryRec{
		MovieID: movieID, SourceTitle: title, EventType: "downloadFolderImported",
		QualityName: quality, DownloadID: downloadID, Date: now,
	}))
	return true
}

// arrRDeleteQueue removes a queue item (removeFromClient / blocklist /
// skipRedownload / changeCategory flags are accepted and ignored) and pings
// arr_queue_changed so open Queue screens refetch.
func arrRDeleteQueue(w http.ResponseWriter, idStr string, inst *DemoInstance) {
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	arrMu.Lock()
	kept := arrRQueue[:0]
	removed := false
	for _, q := range arrRQueue {
		if q.ID == id {
			removed = true
			continue
		}
		kept = append(kept, q)
	}
	arrRQueue = kept
	arrMu.Unlock()
	if removed {
		arrEmitQueueChanged(inst.ID, serviceRadarr)
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

// arrRDeleteMovie removes a movie from the library (deleteFiles /
// addImportListExclusion flags accepted and ignored).
func arrRDeleteMovie(w http.ResponseWriter, idStr string) {
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	arrMu.Lock()
	kept := arrRMovies[:0]
	for _, m := range arrRMovies {
		if m.ID == id {
			delete(arrRMovieByTmdb, m.TmdbID)
			continue
		}
		kept = append(kept, m)
	}
	arrRMovies = kept
	arrMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{})
}
