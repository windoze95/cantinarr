// arr_lidarr.go — the fake Lidarr v1 API served behind the instance proxy:
// /api/instances/{lidarrID}/api/v1/... (D9).
//
// JSON shapes mirror what app/lib/features/lidarr/data/lidarr_api_service.dart
// and lidarr_models.dart read: camelCase fields, integer counters as JSON
// ints, arrays never null, bare arrays for every list except queue/history/
// wanted (a {page,pageSize,totalRecords,records} envelope), and no ratings
// key anywhere (a bare-int ratings.value blanks screens). Album covers and
// artist portraits are generated at runtime as deterministic 400×400 PNGs and
// served under the API-prefixed mediacover/{album|artist}/{id}/... route —
// the exact shape the requester allowlist admits and the Cantinarr digests
// emit, so the app path is identical to production.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// handleLidarrProxy dispatches one proxied Lidarr request. rest is the path
// after /api/instances/{id}/ (e.g. "api/v1/album/lookup"); the query string is
// still on r. Non-admins are limited to GET requests on the read allowlist
// against their effective (granted) Lidarr instance.
func handleLidarrProxy(w http.ResponseWriter, r *http.Request, inst *DemoInstance, isAdmin bool, rest string) {
	if !isAdmin {
		u := userFrom(r)
		eff := effectiveInstanceFor(u, serviceLidarr)
		if eff == nil || eff.ID != inst.ID || !lidNonAdminAllowed(r.Method, rest) {
			writeErr(w, http.StatusForbidden, "permission denied")
			return
		}
	}
	p, ok := strings.CutPrefix(rest, "api/v1/")
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	method := r.Method
	switch {
	case p == "system/status" && method == http.MethodGet:
		lidHandleSystemStatus(w)
	case p == "artist" && method == http.MethodGet:
		lidHandleArtistList(w)
	case p == "artist/lookup" && method == http.MethodGet:
		lidHandleArtistLookup(w, r)
	case strings.HasPrefix(p, "artist/") && method == http.MethodGet:
		lidHandleArtistGet(w, strings.TrimPrefix(p, "artist/"))
	case p == "album" && method == http.MethodGet:
		lidHandleAlbumList(w, r)
	case p == "album/lookup" && method == http.MethodGet:
		lidHandleAlbumLookup(w, r)
	case p == "album/monitor" && method == http.MethodPut:
		lidHandleAlbumMonitor(w, r)
	case strings.HasPrefix(p, "album/") && method == http.MethodGet:
		lidHandleAlbumGet(w, strings.TrimPrefix(p, "album/"))
	case p == "qualityprofile" && method == http.MethodGet:
		writeJSON(w, http.StatusOK, []map[string]any{{"id": 1, "name": "Lossless"}, {"id": 2, "name": "Standard"}})
	case p == "metadataprofile" && method == http.MethodGet:
		// Lidarr's hidden "None" profile is included so the real add path's
		// isNoneMetadataProfile rule has something to skip.
		writeJSON(w, http.StatusOK, []map[string]any{{"id": 1, "name": "Standard"}, {"id": 2, "name": "None"}})
	case p == "rootfolder" && method == http.MethodGet:
		lidHandleRootFolders(w)
	case p == "queue" && method == http.MethodGet:
		lidHandleQueue(w, r)
	case strings.HasPrefix(p, "queue/") && method == http.MethodDelete:
		lidHandleQueueDelete(w, r, strings.TrimPrefix(p, "queue/"))
	case p == "history" && method == http.MethodGet:
		lidHandleHistory(w, r)
	case p == "wanted/missing" && method == http.MethodGet:
		lidHandleWanted(w, r, false)
	case p == "wanted/cutoff" && method == http.MethodGet:
		lidHandleWanted(w, r, true)
	case p == "calendar" && method == http.MethodGet:
		lidHandleCalendar(w, r)
	case p == "track" && method == http.MethodGet:
		lidHandleTracks(w, r)
	case strings.HasPrefix(p, "track/") && method == http.MethodGet:
		lidHandleTrackGet(w, strings.TrimPrefix(p, "track/"))
	case p == "trackfile" && method == http.MethodGet:
		lidHandleTrackFiles(w, r)
	case strings.HasPrefix(p, "trackfile/") && method == http.MethodGet:
		lidHandleTrackFileGet(w, strings.TrimPrefix(p, "trackfile/"))
	case strings.HasPrefix(p, "trackfile/") && method == http.MethodDelete:
		lidHandleTrackFileDelete(w, strings.TrimPrefix(p, "trackfile/"))
	case p == "release" && method == http.MethodGet:
		lidHandleReleaseSearch(w, r)
	case p == "release" && method == http.MethodPost:
		lidHandleReleaseGrab(w, r)
	case p == "manualimport" && method == http.MethodGet:
		lidHandleManualImport(w, r)
	case p == "command" && method == http.MethodPost:
		lidHandleCommand(w, r)
	case strings.HasPrefix(p, "mediacover/album/") && method == http.MethodGet:
		lidWriteMediaCover(w, strings.TrimPrefix(p, "mediacover/album/"), 0)
	case strings.HasPrefix(p, "mediacover/artist/") && method == http.MethodGet:
		lidWriteMediaCover(w, strings.TrimPrefix(p, "mediacover/artist/"), lidArtistCoverSeed)
	case p == "diskspace" && method == http.MethodGet:
		writeJSON(w, http.StatusOK, []map[string]any{
			{"path": lidRootFolder, "label": "Music", "freeSpace": 812_000_000_000, "totalSpace": 2_000_000_000_000},
		})
	case p == "health" && method == http.MethodGet:
		writeJSON(w, http.StatusOK, []map[string]any{})
	default:
		writeErr(w, http.StatusNotFound, "not found")
	}
}

// lidNonAdminAllowed mirrors the real server's lidarr read allowlist
// (auth/arr_proxy.go): GET-only, exact paths, numeric ids, and artwork only
// under the lowercase API-prefixed mediacover route (the Lidarr root
// MediaCover/Albums form is rejected, as in production).
func lidNonAdminAllowed(method, rest string) bool {
	if method != http.MethodGet {
		return false
	}
	p, ok := strings.CutPrefix(rest, "api/v1/")
	if !ok {
		return false
	}
	switch p {
	case "artist", "album", "track", "trackfile", "calendar", "queue", "history",
		"artist/lookup", "album/lookup", "wanted/missing", "wanted/cutoff":
		return true
	}
	for _, prefix := range []string{"artist/", "album/", "track/", "trackfile/"} {
		if id, found := strings.CutPrefix(p, prefix); found {
			return lidPositiveInt(id)
		}
	}
	for _, prefix := range []string{"mediacover/artist/", "mediacover/album/"} {
		if tail, found := strings.CutPrefix(p, prefix); found {
			parts := strings.SplitN(tail, "/", 2)
			return len(parts) == 2 && lidPositiveInt(parts[0]) && parts[1] != ""
		}
	}
	return false
}

func lidPositiveInt(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n > 0
}

// ─── JSON builders (callers hold lidMu) ─────────────────

// lidQualityIDs are Lidarr's quality ids for the names the demo emits; only
// .quality.name is load-bearing app-side.
var lidQualityIDs = map[string]int{
	"MP3-256": 3, lidQualityMP3: 4, lidQualityFLAC: 6, "WAV": 7, lidQualityFLAC24: 21,
}

func lidQualityBlob(name string) map[string]any {
	id, ok := lidQualityIDs[name]
	if !ok {
		id, name = lidQualityIDs[lidQualityFLAC], lidQualityFLAC
	}
	return map[string]any{
		"quality":  map[string]any{"id": id, "name": name},
		"revision": map[string]any{"version": 1, "real": 0, "isRepack": false},
	}
}

// lidLockedArtistContextJSON is the lean artist object embedded in album,
// queue, and history records. A corpus-only artist carries id 0.
func lidLockedArtistContextJSON(a *DemoArtist) map[string]any {
	if a == nil {
		return map[string]any{"id": 0, "artistName": "", "foreignArtistId": "", "monitored": false, "images": []any{}}
	}
	id := 0
	if a.InLibrary {
		id = a.ID
	}
	return map[string]any{
		"id":              id,
		"artistName":      a.Name,
		"foreignArtistId": a.ForeignID,
		"monitored":       a.InLibrary,
		"images": []any{map[string]any{
			"coverType": "poster",
			"url":       lidArtistImagePath(a.ID),
		}},
	}
}

// lidLockedArtistJSON renders one Lidarr artist record. asLibrary=false
// renders the metadata-lookup view of an artist not in the library (id 0,
// unmonitored, zero statistics) while keeping the portrait path keyed on the
// reserved record id.
func lidLockedArtistJSON(a *DemoArtist, asLibrary bool) map[string]any {
	albumCount, files, total, size := 0, 0, 0, 0
	if asLibrary {
		for _, album := range lidAlbums {
			if album.ArtistID != a.ID || !album.InLibrary {
				continue
			}
			albumCount++
			f, t := lidLockedAlbumCounts(album)
			files += f
			total += t
			size += lidLockedAlbumSize(album)
		}
	}
	percent := 0.0
	if total > 0 {
		percent = float64(files) * 100 / float64(total)
	}
	id := 0
	if asLibrary {
		id = a.ID
	}
	m := map[string]any{
		"id":                id,
		"artistName":        a.Name,
		"foreignArtistId":   a.ForeignID,
		"overview":          a.Overview,
		"artistType":        a.ArtistType,
		"status":            "ended",
		"monitored":         asLibrary,
		"qualityProfileId":  1,
		"metadataProfileId": 1,
		"monitorNewItems":   "none",
		"statistics": map[string]any{
			"albumCount":      albumCount,
			"trackFileCount":  files,
			"trackCount":      total,
			"totalTrackCount": total,
			"sizeOnDisk":      size,
			"percentOfTracks": percent,
		},
		"images": []any{map[string]any{
			"coverType": "poster",
			"url":       lidArtistImagePath(a.ID),
		}},
		"genres": a.Genres,
	}
	if asLibrary {
		m["path"] = a.Path
		m["rootFolderPath"] = lidRootFolder
		if !a.Added.IsZero() {
			m["added"] = arrISO(a.Added)
		}
	}
	return m
}

// lidLockedAlbumJSON renders one Lidarr album record. asLibrary=false renders
// the metadata-lookup view of a record not in the library (id 0, unmonitored,
// zero statistics) while keeping the cover path keyed on the reserved record
// id.
func lidLockedAlbumJSON(a *DemoAlbum, asLibrary bool) map[string]any {
	artist := lidArtistsByID[a.ArtistID]
	id, artistID, monitored := 0, 0, false
	files, total, size := 0, 0, 0
	duration := 0
	for _, t := range a.Tracks {
		duration += t.Duration
	}
	if asLibrary {
		id = a.ID
		monitored = a.Monitored
		files, total = lidLockedAlbumCounts(a)
		size = lidLockedAlbumSize(a)
	}
	if artist != nil && artist.InLibrary {
		artistID = artist.ID
	}
	percent := 0.0
	if total > 0 {
		percent = float64(files) * 100 / float64(total)
	}
	return map[string]any{
		"id":             id,
		"title":          a.Title,
		"artistId":       artistID,
		"foreignAlbumId": a.ForeignID,
		"overview":       a.Overview,
		"disambiguation": "",
		"releaseDate":    a.ReleaseDate + "T00:00:00Z",
		"monitored":      monitored,
		"anyReleaseOk":   true,
		"albumType":      a.AlbumType,
		"secondaryTypes": a.SecondaryTypes,
		"profileId":      1,
		"duration":       duration,
		"genres":         a.Genres,
		"statistics": map[string]any{
			"trackFileCount":  files,
			"trackCount":      total,
			"totalTrackCount": total,
			"sizeOnDisk":      size,
			"percentOfTracks": percent,
		},
		"images": []any{map[string]any{
			"coverType": "cover",
			"url":       lidAlbumCoverPath(a.ID),
		}},
		"artist": lidLockedArtistContextJSON(artist),
	}
}

// lidLockedAlbumContextJSON is the lean album object embedded in queue and
// history records.
func lidLockedAlbumContextJSON(a *DemoAlbum) map[string]any {
	if a == nil {
		return nil
	}
	return map[string]any{
		"id":             a.ID,
		"title":          a.Title,
		"foreignAlbumId": a.ForeignID,
		"releaseDate":    a.ReleaseDate + "T00:00:00Z",
	}
}

func lidLockedQueueItemJSON(it *lidQueueItem, includeArtist, includeAlbum bool) map[string]any {
	msgs := make([]any, 0, len(it.StatusMessages))
	for _, sm := range it.StatusMessages {
		messages := sm.Messages
		if messages == nil {
			messages = []string{}
		}
		msgs = append(msgs, map[string]any{"title": sm.Title, "messages": messages})
	}
	m := map[string]any{
		"id":                    it.ID,
		"artistId":              it.ArtistID,
		"albumId":               it.AlbumID,
		"title":                 it.Title,
		"status":                it.Status,
		"trackedDownloadStatus": it.TrackedDownloadStatus,
		"trackedDownloadState":  it.TrackedDownloadState,
		"statusMessages":        msgs,
		"protocol":              it.Protocol,
		"indexer":               it.Indexer,
		"downloadClient":        it.DownloadClient,
		"size":                  it.Size,
		"sizeleft":              it.Sizeleft,
		"timeleft":              it.Timeleft,
		"downloadId":            it.DownloadID,
		"quality":               lidQualityBlob(it.Quality),
		"trackFileCount":        it.TrackFileCount,
		"trackHasFileCount":     it.TrackHasFileCount,
		"added":                 arrISO(it.Added),
	}
	if it.ErrorMessage != "" {
		m["errorMessage"] = it.ErrorMessage
	}
	if includeArtist {
		if a := lidArtistsByID[it.ArtistID]; a != nil {
			m["artist"] = map[string]any{"id": a.ID, "artistName": a.Name, "foreignArtistId": a.ForeignID}
		}
	}
	if includeAlbum {
		if album := lidAlbumsByID[it.AlbumID]; album != nil {
			m["album"] = lidLockedAlbumContextJSON(album)
		}
	}
	return m
}

func lidLockedHistoryJSON(rec *lidHistoryRec) map[string]any {
	data := map[string]any{"indexer": lidIndexerUsenet, "releaseGroup": "DEMO"}
	for k, v := range rec.Data {
		data[k] = v
	}
	m := map[string]any{
		"id":          rec.ID,
		"eventType":   rec.EventType,
		"sourceTitle": rec.SourceTitle,
		"date":        arrISO(rec.Date),
		"quality":     lidQualityBlob(rec.Quality),
		"artistId":    rec.ArtistID,
		"albumId":     rec.AlbumID,
		"downloadId":  rec.DownloadID,
		"data":        data,
	}
	if a := lidArtistsByID[rec.ArtistID]; a != nil {
		m["artist"] = map[string]any{"id": a.ID, "artistName": a.Name, "foreignArtistId": a.ForeignID}
	}
	if album := lidAlbumsByID[rec.AlbumID]; album != nil {
		m["album"] = lidLockedAlbumContextJSON(album)
	}
	return m
}

func lidTrackJSON(album *DemoAlbum, t *DemoTrack) map[string]any {
	return map[string]any{
		"id":                  t.ID,
		"albumId":             album.ID,
		"artistId":            album.ArtistID,
		"trackFileId":         t.FileID,
		"absoluteTrackNumber": t.Number,
		"trackNumber":         strconv.Itoa(t.Number),
		"mediumNumber":        1,
		"title":               t.Title,
		"duration":            t.Duration,
		"hasFile":             t.FileID > 0,
		"monitored":           true,
		"explicit":            false,
		"foreignTrackId":      fmt.Sprintf("c0000000-d3a0-4000-8000-%012d", t.ID),
	}
}

func lidTrackFileJSON(album *DemoAlbum, t *DemoTrack) map[string]any {
	codec, bitrate := "FLAC", "1 411 kbps"
	if strings.HasPrefix(t.Quality, "MP3") {
		codec, bitrate = "MP3", "320 kbps"
	}
	return map[string]any{
		"id":        t.FileID,
		"albumId":   album.ID,
		"artistId":  album.ArtistID,
		"path":      t.FilePath,
		"size":      t.FileSize,
		"dateAdded": arrISO(t.DateAdded),
		"quality":   lidQualityBlob(t.Quality),
		"mediaInfo": map[string]any{
			"audioChannels":   2,
			"audioBitRate":    bitrate,
			"audioCodec":      codec,
			"audioBits":       "16bit",
			"audioSampleRate": "44.1kHz",
		},
	}
}

// ─── Handlers ───────────────────────────────────────────

func lidHandleSystemStatus(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":        "2.9.6.4552",
		"instanceName":   "Lidarr",
		"appName":        "Lidarr",
		"branch":         "master",
		"startTime":      arrISO(lidSeedTime),
		"isProduction":   true,
		"runtimeVersion": "6.0.36",
	})
}

func lidHandleRootFolders(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, []map[string]any{{
		"id":                       1,
		"name":                     "Music",
		"path":                     lidRootFolder,
		"accessible":               true,
		"freeSpace":                812_000_000_000,
		"unmappedFolders":          []any{},
		"defaultQualityProfileId":  1,
		"defaultMetadataProfileId": 1,
		"defaultMonitorOption":     "none",
	}})
}

func lidHandleArtistList(w http.ResponseWriter) {
	lidMu.Lock()
	out := []map[string]any{}
	for _, a := range lidArtists {
		if a.InLibrary {
			out = append(out, lidLockedArtistJSON(a, true))
		}
	}
	lidMu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func lidHandleArtistGet(w http.ResponseWriter, idStr string) {
	id, _ := strconv.Atoi(idStr)
	lidMu.Lock()
	var out map[string]any
	if a := lidArtistsByID[id]; a != nil && a.InLibrary {
		out = lidLockedArtistJSON(a, true)
	}
	lidMu.Unlock()
	if out == nil {
		writeErr(w, http.StatusNotFound, "artist not found")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// lidHandleArtistLookup answers the metadata search for artists: a name
// substring, or "lidarr:<id>" for an exact fetch. Library artists carry their
// real id, corpus-only artists id 0.
func lidHandleArtistLookup(w http.ResponseWriter, r *http.Request) {
	term := strings.TrimSpace(r.URL.Query().Get("term"))
	out := []map[string]any{}
	if term == "" {
		writeJSON(w, http.StatusOK, out)
		return
	}
	lidMu.Lock()
	if id, ok := strings.CutPrefix(term, "lidarr:"); ok {
		if a := lidArtistsByFID[strings.TrimSpace(id)]; a != nil {
			out = append(out, lidLockedArtistJSON(a, a.InLibrary))
		}
	} else {
		needle := strings.ToLower(term)
		for _, a := range lidArtists {
			if strings.Contains(strings.ToLower(a.Name), needle) {
				out = append(out, lidLockedArtistJSON(a, a.InLibrary))
			}
		}
	}
	lidMu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func lidHandleAlbumList(w http.ResponseWriter, r *http.Request) {
	artistID := queryInt(r, "artistId", 0)
	lidMu.Lock()
	out := []map[string]any{}
	for _, a := range lidAlbums {
		if !a.InLibrary || (artistID > 0 && a.ArtistID != artistID) {
			continue
		}
		out = append(out, lidLockedAlbumJSON(a, true))
	}
	lidMu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func lidHandleAlbumGet(w http.ResponseWriter, idStr string) {
	id, _ := strconv.Atoi(idStr)
	lidMu.Lock()
	var out map[string]any
	if a := lidAlbumsByID[id]; a != nil && a.InLibrary {
		out = lidLockedAlbumJSON(a, true)
	}
	lidMu.Unlock()
	if out == nil {
		writeErr(w, http.StatusNotFound, "album not found")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// lidHandleAlbumLookup answers the metadata search for albums: a title or
// artist substring, or "lidarr:<foreignAlbumId>" for an exact fetch. The
// exact fetch follows a merged release-group id to the record the library
// files it under — the provider itself declaring the two ids one album, which
// is what makes canonical_foreign_id truthful.
func lidHandleAlbumLookup(w http.ResponseWriter, r *http.Request) {
	term := strings.TrimSpace(r.URL.Query().Get("term"))
	out := []map[string]any{}
	if term == "" {
		writeJSON(w, http.StatusOK, out)
		return
	}
	lidMu.Lock()
	if id, ok := strings.CutPrefix(term, "lidarr:"); ok {
		if a := lidLockedAlbumByFID(strings.TrimSpace(id)); a != nil {
			out = append(out, lidLockedAlbumJSON(a, a.InLibrary))
		}
	} else {
		needle := strings.ToLower(term)
		for _, a := range lidAlbums {
			artistName := ""
			if artist := lidArtistsByID[a.ArtistID]; artist != nil {
				artistName = artist.Name
			}
			if !strings.Contains(strings.ToLower(a.Title), needle) &&
				!strings.Contains(strings.ToLower(artistName), needle) {
				continue
			}
			out = append(out, lidLockedAlbumJSON(a, a.InLibrary))
		}
	}
	lidMu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func lidHandleAlbumMonitor(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AlbumIDs  []int `json:"albumIds"`
		Monitored bool  `json:"monitored"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	changed := false
	lidMu.Lock()
	for _, id := range body.AlbumIDs {
		if a := lidAlbumsByID[id]; a != nil && a.InLibrary {
			a.Monitored = body.Monitored
			changed = true
		}
	}
	lidMu.Unlock()
	if changed {
		lidArrQueueChangedPing()
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func lidHandleQueue(w http.ResponseWriter, r *http.Request) {
	includeArtist := r.URL.Query().Get("includeArtist") == "true"
	includeAlbum := r.URL.Query().Get("includeAlbum") == "true"
	lidMu.Lock()
	records := []map[string]any{}
	for _, it := range lidQueue {
		records = append(records, lidLockedQueueItemJSON(it, includeArtist, includeAlbum))
	}
	lidMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"page":         1,
		"pageSize":     queryInt(r, "pageSize", 100),
		"totalRecords": len(records),
		"records":      records,
	})
}

// lidHandleQueueDelete removes a queue item; blocklist=true also records the
// failed download in history (the release will not be grabbed again).
func lidHandleQueueDelete(w http.ResponseWriter, r *http.Request, idStr string) {
	id, _ := strconv.Atoi(idStr)
	blocklist := r.URL.Query().Get("blocklist") == "true"
	found := false
	lidMu.Lock()
	kept := lidQueue[:0]
	for _, it := range lidQueue {
		if it.ID == id {
			found = true
			if blocklist {
				lidSeedHistory("downloadFailed", it.AlbumID, it.Title, it.Quality, it.DownloadID, time.Now().UTC(),
					map[string]string{"message": "Marked as failed and blocklisted by an administrator"})
			}
			continue
		}
		kept = append(kept, it)
	}
	lidQueue = kept
	lidMu.Unlock()
	if !found {
		writeErr(w, http.StatusNotFound, "queue item not found")
		return
	}
	lidArrQueueChangedPing()
	writeJSON(w, http.StatusOK, map[string]any{})
}

// lidHistoryEventTypes maps Lidarr's numeric eventType filter to the names
// the records carry.
var lidHistoryEventTypes = map[int]string{
	1: "grabbed", 2: "artistFolderImported", 3: "trackFileImported", 4: "downloadFailed",
	5: "trackFileDeleted", 6: "trackFileRenamed", 7: "albumImportIncomplete",
	8: "downloadImported", 9: "trackFileRetagged", 10: "downloadIgnored",
}

func lidHandleHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	albumID := queryInt(r, "albumId", 0)
	downloadID := q.Get("downloadId")
	eventType := ""
	if n := queryInt(r, "eventType", 0); n > 0 {
		eventType = lidHistoryEventTypes[n]
	}
	lidMu.Lock()
	rows := make([]*lidHistoryRec, 0, len(lidHistory))
	for _, rec := range lidHistory {
		if albumID > 0 && rec.AlbumID != albumID {
			continue
		}
		if downloadID != "" && rec.DownloadID != downloadID {
			continue
		}
		if eventType != "" && rec.EventType != eventType {
			continue
		}
		rows = append(rows, rec)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Date.After(rows[j].Date) })
	records := make([]map[string]any, 0, len(rows))
	for _, rec := range rows {
		records = append(records, lidLockedHistoryJSON(rec))
	}
	lidMu.Unlock()
	page, size := arrPageParams(r)
	writeJSON(w, http.StatusOK, arrPaged(records, page, size))
}

// lidHandleWanted serves wanted/missing (monitored albums with tracks
// missing) and wanted/cutoff (monitored complete albums whose files are below
// the profile cutoff), newest release date first, artist embedded.
func lidHandleWanted(w http.ResponseWriter, r *http.Request, cutoff bool) {
	type row struct {
		json    map[string]any
		release string
	}
	lidMu.Lock()
	rows := []row{}
	for _, a := range lidAlbums {
		if !a.InLibrary || !a.Monitored {
			continue
		}
		match := false
		if cutoff {
			match = lidLockedAlbumComplete(a) && lidCutoffUnmet[a.ID]
		} else {
			files, total := lidLockedAlbumCounts(a)
			match = files < total
		}
		if !match {
			continue
		}
		rows = append(rows, row{json: lidLockedAlbumJSON(a, true), release: a.ReleaseDate})
	}
	lidMu.Unlock()
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].release > rows[j].release })
	records := make([]map[string]any, 0, len(rows))
	for _, rw := range rows {
		records = append(records, rw.json)
	}
	page, size := arrPageParams(r)
	writeJSON(w, http.StatusOK, arrPaged(records, page, size))
}

// lidHandleCalendar lists the library albums whose release date falls inside
// [start, end]; unmonitored=false (the app's request) drops unmonitored
// records. Album release dates are calendar dates, so the comparison is by
// day.
func lidHandleCalendar(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	start, end := arrParseWindow(q.Get("start"), q.Get("end"))
	includeUnmonitored := q.Get("unmonitored") == "true"
	startDay := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	endExclusive := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	type row struct {
		json map[string]any
		date time.Time
	}
	lidMu.Lock()
	rows := []row{}
	for _, a := range lidAlbums {
		if !a.InLibrary || (!a.Monitored && !includeUnmonitored) {
			continue
		}
		d, ok := arrDate(a.ReleaseDate)
		if !ok || d.Before(startDay) || !d.Before(endExclusive) {
			continue
		}
		rows = append(rows, row{json: lidLockedAlbumJSON(a, true), date: d})
	}
	lidMu.Unlock()
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].date.Before(rows[j].date) })
	out := make([]map[string]any, 0, len(rows))
	for _, rw := range rows {
		out = append(out, rw.json)
	}
	writeJSON(w, http.StatusOK, out)
}

func lidHandleTracks(w http.ResponseWriter, r *http.Request) {
	albumID := queryInt(r, "albumId", 0)
	lidMu.Lock()
	out := []map[string]any{}
	if a := lidAlbumsByID[albumID]; a != nil && a.InLibrary {
		for _, t := range a.Tracks {
			out = append(out, lidTrackJSON(a, t))
		}
	}
	lidMu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func lidHandleTrackGet(w http.ResponseWriter, idStr string) {
	id, _ := strconv.Atoi(idStr)
	lidMu.Lock()
	var out map[string]any
	if a := lidAlbumsByID[id/100]; a != nil && a.InLibrary {
		for _, t := range a.Tracks {
			if t.ID == id {
				out = lidTrackJSON(a, t)
				break
			}
		}
	}
	lidMu.Unlock()
	if out == nil {
		writeErr(w, http.StatusNotFound, "track not found")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// lidHandleTrackFiles lists the files on disk for one album or one artist.
// Lidarr's trackfile read requires a filter; neither → an empty list.
func lidHandleTrackFiles(w http.ResponseWriter, r *http.Request) {
	albumID := queryInt(r, "albumId", 0)
	artistID := queryInt(r, "artistId", 0)
	out := []map[string]any{}
	if albumID <= 0 && artistID <= 0 {
		writeJSON(w, http.StatusOK, out)
		return
	}
	lidMu.Lock()
	for _, a := range lidAlbums {
		if !a.InLibrary {
			continue
		}
		if albumID > 0 && a.ID != albumID {
			continue
		}
		if artistID > 0 && a.ArtistID != artistID {
			continue
		}
		for _, t := range a.Tracks {
			if t.FileID > 0 {
				out = append(out, lidTrackFileJSON(a, t))
			}
		}
	}
	lidMu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

// lidLockedTrackByFileID resolves a trackfile id to its album and track.
func lidLockedTrackByFileID(fileID int) (*DemoAlbum, *DemoTrack) {
	for _, a := range lidAlbums {
		for _, t := range a.Tracks {
			if t.FileID == fileID && fileID > 0 {
				return a, t
			}
		}
	}
	return nil, nil
}

func lidHandleTrackFileGet(w http.ResponseWriter, idStr string) {
	id, _ := strconv.Atoi(idStr)
	lidMu.Lock()
	var out map[string]any
	if a, t := lidLockedTrackByFileID(id); t != nil && a.InLibrary {
		out = lidTrackFileJSON(a, t)
	}
	lidMu.Unlock()
	if out == nil {
		writeErr(w, http.StatusNotFound, "track file not found")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// lidHandleTrackFileDelete removes one file from disk and the library and
// records the deletion (the wrong-album repair's primitive).
func lidHandleTrackFileDelete(w http.ResponseWriter, idStr string) {
	id, _ := strconv.Atoi(idStr)
	lidMu.Lock()
	a, t := lidLockedTrackByFileID(id)
	if t == nil {
		lidMu.Unlock()
		writeErr(w, http.StatusNotFound, "track file not found")
		return
	}
	path := t.FilePath
	t.FileID, t.FilePath, t.FileSize, t.Quality, t.DateAdded = 0, "", 0, "", time.Time{}
	lidSeedHistory("trackFileDeleted", a.ID, path, lidQualityFLAC, "", time.Now().UTC(), map[string]string{"reason": "Manual"})
	lidMu.Unlock()
	lidArrQueueChangedPing()
	writeJSON(w, http.StatusOK, map[string]any{})
}

func lidHandleReleaseSearch(w http.ResponseWriter, r *http.Request) {
	albumID := queryInt(r, "albumId", 0)
	lidMu.Lock()
	out := lidLockedReleases(albumID)
	lidMu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

// lidLockedReleases builds the canned interactive-search results for one
// album: two usenet FLAC releases, one torrent MP3-320 rejected below cutoff,
// and one usenet 24-bit FLAC. Usenet rows carry null seeders/leechers exactly
// as Lidarr does.
func lidLockedReleases(albumID int) []map[string]any {
	a := lidAlbumsByID[albumID]
	if a == nil {
		return []map[string]any{}
	}
	flacSize, mp3Size := 0, 0
	for _, t := range a.Tracks {
		flacSize += lidTrackFileSize(t.ID, lidQualityFLAC)
		mp3Size += lidTrackFileSize(t.ID, lidQualityMP3)
	}
	base := lidReleaseName(a, lidQualityFLAC)
	return []map[string]any{
		{
			"guid": fmt.Sprintf("demo-lidarr-rel-%d-1", albumID), "indexerId": 1, "indexer": lidIndexerUsenet,
			"title": base, "size": flacSize, "seeders": nil, "leechers": nil, "protocol": "usenet",
			"age": 3, "ageHours": 78.4, "quality": lidQualityBlob(lidQualityFLAC),
			"rejected": false, "rejections": []any{},
		},
		{
			"guid": fmt.Sprintf("demo-lidarr-rel-%d-2", albumID), "indexerId": 1, "indexer": lidIndexerUsenet,
			"title": strings.Replace(base, "-DEMO", ".REMASTERED-DEMO", 1), "size": flacSize + flacSize/10,
			"seeders": nil, "leechers": nil, "protocol": "usenet",
			"age": 12, "ageHours": 291.7, "quality": lidQualityBlob(lidQualityFLAC),
			"rejected": false, "rejections": []any{},
		},
		{
			"guid": fmt.Sprintf("demo-lidarr-rel-%d-3", albumID), "indexerId": 2, "indexer": lidIndexerTorrent,
			"title": lidReleaseName(a, lidQualityMP3), "size": mp3Size, "seeders": 27, "leechers": 4, "protocol": "torrent",
			"age": 21, "ageHours": 510.2, "quality": lidQualityBlob(lidQualityMP3),
			"rejected": true, "rejections": []any{"Quality MP3-320 is below cutoff"},
		},
		{
			"guid": fmt.Sprintf("demo-lidarr-rel-%d-4", albumID), "indexerId": 1, "indexer": lidIndexerUsenet,
			"title": lidReleaseName(a, "FLAC.24bit"), "size": flacSize * 2, "seeders": nil, "leechers": nil, "protocol": "usenet",
			"age": 0, "ageHours": 9.6, "quality": lidQualityBlob(lidQualityFLAC24),
			"rejected": false, "rejections": []any{},
		},
	}
}

// lidHandleReleaseGrab sends a searched release to the download client: the
// album becomes monitored (joining the library if needed) and a healthy
// queue item appears.
func lidHandleReleaseGrab(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Guid      string `json:"guid"`
		IndexerID int    `json:"indexerId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	changed := false
	if tail, ok := strings.CutPrefix(body.Guid, "demo-lidarr-rel-"); ok {
		if idStr, _, found := strings.Cut(tail, "-"); found {
			if albumID, err := strconv.Atoi(idStr); err == nil {
				lidMu.Lock()
				if a := lidAlbumsByID[albumID]; a != nil {
					a.InLibrary = true
					a.Monitored = true
					lidLockedJoinLibrary(lidArtistsByID[a.ArtistID])
					if lidLockedQueueFor(a.ID) == nil {
						lidLockedEnqueue(a)
						changed = true
					}
				}
				lidMu.Unlock()
			}
		}
	}
	if changed {
		lidArrQueueChangedPing()
	}
	writeJSON(w, http.StatusOK, map[string]any{"guid": body.Guid, "indexerId": body.IndexerID})
}

// lidHandleManualImport lists the importable files Lidarr found for a
// download: one mapped candidate per missing track of the queued album, so
// "Manual import" in the Import Doctor completes the album.
func lidHandleManualImport(w http.ResponseWriter, r *http.Request) {
	downloadID := r.URL.Query().Get("downloadId")
	lidMu.Lock()
	out := []map[string]any{}
	for _, it := range lidQueue {
		if downloadID != "" && it.DownloadID != downloadID {
			continue
		}
		a := lidAlbumsByID[it.AlbumID]
		if a == nil {
			continue
		}
		artistName := ""
		if artist := lidArtistsByID[a.ArtistID]; artist != nil {
			artistName = artist.Name
		}
		for _, t := range a.Tracks {
			if t.FileID > 0 {
				continue
			}
			name := fmt.Sprintf("%02d - %s.flac", t.Number, lidPathName(t.Title))
			out = append(out, map[string]any{
				"id":             7000 + t.ID,
				"path":           fmt.Sprintf("/downloads/complete/%s/%s", it.Title, name),
				"folderName":     it.Title,
				"name":           name,
				"relativePath":   name,
				"size":           lidTrackFileSize(t.ID, lidQualityFLAC),
				"artist":         map[string]any{"id": a.ArtistID, "artistName": artistName},
				"album":          map[string]any{"id": a.ID, "title": a.Title, "foreignAlbumId": a.ForeignID},
				"albumReleaseId": 5000 + a.ID,
				"tracks":         []any{map[string]any{"id": t.ID, "title": t.Title, "trackNumber": strconv.Itoa(t.Number)}},
				"quality":        lidQualityBlob(lidQualityFLAC),
				"releaseGroup":   "DEMO",
				"downloadId":     it.DownloadID,
				"rejections":     []any{},
			})
		}
	}
	lidMu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

// lidHandleCommand runs the command vocabulary the app posts: ArtistSearch,
// AlbumSearch, ProcessMonitoredDownloads, RescanFolders, RefreshArtist, and
// ManualImport. An unknown name is still accepted (201), like Lidarr's queue
// of a command it has nothing to do for.
func lidHandleCommand(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name       string `json:"name"`
		ArtistID   int    `json:"artistId"`
		AlbumIDs   []int  `json:"albumIds"`
		ImportMode string `json:"importMode"`
		Files      []struct {
			Path           string `json:"path"`
			ArtistID       int    `json:"artistId"`
			AlbumID        int    `json:"albumId"`
			AlbumReleaseID int    `json:"albumReleaseId"`
			TrackIDs       []int  `json:"trackIds"`
			DownloadID     string `json:"downloadId"`
		} `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	changed := false
	switch body.Name {
	case "AlbumSearch":
		lidMu.Lock()
		for _, id := range body.AlbumIDs {
			if lidLockedSearchAlbum(lidAlbumsByID[id]) {
				changed = true
			}
		}
		lidMu.Unlock()
	case "ArtistSearch":
		lidMu.Lock()
		for _, a := range lidAlbums {
			if a.ArtistID == body.ArtistID && lidLockedSearchAlbum(a) {
				changed = true
			}
		}
		lidMu.Unlock()
	case "ManualImport":
		now := time.Now().UTC()
		lidMu.Lock()
		byAlbum := map[int]map[int]bool{}
		order := []int{}
		for _, f := range body.Files {
			if f.AlbumID <= 0 {
				continue
			}
			ids, seen := byAlbum[f.AlbumID]
			if !seen {
				ids = map[int]bool{}
				byAlbum[f.AlbumID] = ids
				order = append(order, f.AlbumID)
			}
			for _, tid := range f.TrackIDs {
				ids[tid] = true
			}
		}
		for _, albumID := range order {
			a := lidAlbumsByID[albumID]
			if a == nil {
				continue
			}
			ids := byAlbum[albumID]
			if len(ids) == 0 {
				ids = nil // a file with no matched tracks imports whatever is missing
			}
			landed := lidLockedLandTracks(a, ids, now)
			if lidLockedFinishImport(a, landed) {
				changed = true
			}
		}
		lidMu.Unlock()
	case "ProcessMonitoredDownloads", "RescanFolders", "RefreshArtist":
		// No-op in the demo.
	}
	if changed {
		lidArrQueueChangedPing()
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": 1, "name": body.Name, "status": "queued",
	})
}

// ─── MediaCover placeholder art ─────────────────────────

// lidArtistCoverSeed lifts artist ids clear of the album id space so a
// generated portrait never comes out identical to the cover of the album that
// happens to share its number.
const lidArtistCoverSeed = 60000

// lidWriteMediaCover serves mediacover/{album|artist}/{id}/<file> as PNG
// bytes, overriding the router's JSON content type.
func lidWriteMediaCover(w http.ResponseWriter, tail string, seedOffset int) {
	parts := strings.SplitN(tail, "/", 2)
	if len(parts) != 2 || parts[1] == "" {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	id, err := strconv.Atoi(parts[0])
	if err != nil || id <= 0 {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	data := lidCoverPNG(seedOffset + id)
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

var (
	lidCoverMu    sync.Mutex
	lidCoverCache = map[int][]byte{}
)

// lidCoverPNG generates (and caches) a deterministic 400×400 placeholder
// cover — square, because album tiles crop with BoxFit.cover — with a
// vinyl-flavoured motif chosen by the id: concentric grooves, a centre label,
// quadrant blocks, or diagonal bands. Colours come from chapHSVColor.
func lidCoverPNG(seedID int) []byte {
	lidCoverMu.Lock()
	if data, ok := lidCoverCache[seedID]; ok {
		lidCoverMu.Unlock()
		return data
	}
	lidCoverMu.Unlock()

	const size = 400
	seed := uint32(seedID)*2654435761 + 7919
	base := chapHSVColor(float64(seed%360), 0.55, 0.40)
	accent := chapHSVColor(float64((seed/360)%360), 0.62, 0.80)
	trim := chapHSVColor(float64(seed%360), 0.65, 0.20)

	img := image.NewRGBA(image.Rect(0, 0, size, size))
	cx, cy := size/2, size/2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.SetRGBA(x, y, base)
			dx, dy := float64(x-cx), float64(y-cy)
			d := math.Sqrt(dx*dx + dy*dy)
			switch seedID % 4 {
			case 0: // concentric grooves around a dark centre
				if d < 170 && int(d/18)%2 == 0 {
					img.SetRGBA(x, y, accent)
				}
				if d < 40 {
					img.SetRGBA(x, y, trim)
				}
			case 1: // centre label
				if d <= 120 {
					img.SetRGBA(x, y, accent)
				}
				if d <= 28 {
					img.SetRGBA(x, y, trim)
				}
			case 2: // quadrant blocks
				if (x < cx) != (y < cy) {
					img.SetRGBA(x, y, accent)
				}
			case 3: // diagonal bands
				if ((x+y)/56)%2 == 0 {
					img.SetRGBA(x, y, accent)
				}
			}
			if x < 10 || x >= size-10 || y < 10 || y >= size-10 {
				img.SetRGBA(x, y, trim)
			}
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	data := buf.Bytes()
	lidCoverMu.Lock()
	lidCoverCache[seedID] = data
	lidCoverMu.Unlock()
	return data
}
