// arr_chaptarr.go — the fake Chaptarr v1 API (Readarr lineage) served behind
// the instance proxy: /api/instances/{chaptarrID}/api/v1/... (D8).
//
// JSON shapes mirror app-arr.md §6 / app-requests-books.md §8 exactly:
// camelCase fields, integer counters as JSON ints, arrays never null, and
// ebook/audiobook as SEPARATE book records sharing foreignBookId. Book covers
// and author portraits are generated at runtime as deterministic PNG
// placeholders (public-domain safe) and served under MediaCover/Books/{bookId}/...
// and MediaCover/{authorId}/... with an image content type.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// handleChaptarrProxy dispatches one proxied Chaptarr request. rest is the
// path after /api/instances/{id}/ (e.g. "api/v1/book/lookup"); the query
// string is still on r. Non-admins are limited to GET requests on the read
// allowlist against their effective (granted) chaptarr instance.
func handleChaptarrProxy(w http.ResponseWriter, r *http.Request, inst *DemoInstance, isAdmin bool, rest string) {
	if !isAdmin {
		u := userFrom(r)
		eff := effectiveInstanceFor(u, serviceChaptarr)
		if eff == nil || eff.ID != inst.ID || !chapNonAdminAllowed(r.Method, rest) {
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
	case p == "author" && method == http.MethodGet:
		chapHandleAuthorList(w)
	case strings.HasPrefix(p, "author/") && method == http.MethodGet:
		chapHandleAuthorGet(w, strings.TrimPrefix(p, "author/"))
	case strings.HasPrefix(p, "author/") && method == http.MethodDelete:
		chapHandleAuthorDelete(w, r, strings.TrimPrefix(p, "author/"))
	case p == "book/lookup" && method == http.MethodGet:
		chapHandleBookLookup(w, r)
	case p == "book/monitor" && method == http.MethodPut:
		chapHandleBookMonitor(w, r)
	case p == "book" && method == http.MethodGet:
		chapHandleBookList(w, r)
	case p == "book" && method == http.MethodPost:
		chapHandleBookAdd(w, r)
	case strings.HasPrefix(p, "book/") && method == http.MethodGet:
		chapHandleBookGet(w, strings.TrimPrefix(p, "book/"))
	case p == "bookfile" && method == http.MethodGet:
		chapHandleBookFiles(w, r)
	case p == "queue" && method == http.MethodGet:
		chapHandleQueue(w, r)
	case strings.HasPrefix(p, "queue/") && method == http.MethodDelete:
		chapHandleQueueDelete(w, strings.TrimPrefix(p, "queue/"))
	case p == "history" && method == http.MethodGet:
		chapHandleHistory(w, r)
	case p == "wanted/missing" && method == http.MethodGet:
		chapHandleWanted(w, r, false)
	case p == "wanted/cutoff" && method == http.MethodGet:
		chapHandleWanted(w, r, true)
	case p == "release" && method == http.MethodGet:
		chapHandleReleaseSearch(w, r)
	case p == "release" && method == http.MethodPost:
		chapHandleReleaseGrab(w, r)
	case p == "command" && method == http.MethodPost:
		chapHandleCommand(w, r)
	case p == "manualimport" && method == http.MethodGet:
		chapHandleManualImport(w, r)
	case p == "rootfolder" && method == http.MethodGet:
		chapHandleRootFolders(w)
	case strings.HasPrefix(p, "MediaCover/Books/") && method == http.MethodGet:
		chapHandleMediaCover(w, strings.TrimPrefix(p, "MediaCover/Books/"))
	case strings.HasPrefix(p, "MediaCover/") && method == http.MethodGet:
		chapHandleAuthorMediaCover(w, strings.TrimPrefix(p, "MediaCover/"))
	default:
		writeErr(w, http.StatusNotFound, "not found")
	}
}

// chapNonAdminAllowed mirrors the real server's chaptarr read allowlist
// (srv-instances §2 / app-arr §9): GET-only, exact paths, numeric ids.
func chapNonAdminAllowed(method, rest string) bool {
	if method != http.MethodGet {
		return false
	}
	p, ok := strings.CutPrefix(rest, "api/v1/")
	if !ok {
		return false
	}
	switch p {
	case "author", "book", "book/lookup", "bookfile", "calendar",
		"queue", "history", "wanted/missing", "wanted/cutoff":
		return true
	}
	for _, prefix := range []string{"author/", "book/", "bookfile/"} {
		if id, found := strings.CutPrefix(p, prefix); found {
			return chapPositiveInt(id)
		}
	}
	if tail, found := strings.CutPrefix(p, "MediaCover/Books/"); found {
		parts := strings.SplitN(tail, "/", 2)
		return len(parts) == 2 && chapPositiveInt(parts[0]) && parts[1] != ""
	}
	// Author portraits: Chaptarr files them at MediaCover/{authorId}/<file>,
	// with no Authors/ subtree, so the id sits directly under MediaCover.
	if tail, found := strings.CutPrefix(p, "MediaCover/"); found {
		parts := strings.SplitN(tail, "/", 2)
		return len(parts) == 2 && chapPositiveInt(parts[0]) && parts[1] != ""
	}
	return false
}

func chapPositiveInt(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n > 0
}

func chapArrQueueChangedPing() {
	wsBroadcast(evtArrQueueChanged, map[string]any{
		"instance_id": instChaptarr, "service_type": serviceChaptarr,
	})
}

// ─── JSON builders (callers hold chapMu) ────────────────

var chapQualityIDs = map[string]int{
	"PDF": 1, "MOBI": 2, "EPUB": 3, "AZW3": 4, "MP3": 10, "M4B": 11,
}

func chapQualityBlob(name string) map[string]any {
	return map[string]any{
		"quality":  map[string]any{"id": chapQualityIDs[name], "name": name},
		"revision": map[string]any{"version": 1, "real": 0, "isRepack": false},
	}
}

func chapLockedAuthorJSON(a *chapAuthorRec) map[string]any {
	bookCount, fileCount, size := 0, 0, 0
	for id, st := range chapRecStates {
		if !st.InLibrary {
			continue
		}
		b := chapRecToBook[id]
		if b == nil || chapMetaByFID[b.ForeignID].AuthorID != a.ID {
			continue
		}
		bookCount++
		if bf := b.Formats[chapRecFormat[id]]; bf != nil && bf.Downloaded {
			fileCount++
			size += st.FileSize
		}
	}
	percent := 0.0
	if bookCount > 0 {
		percent = float64(fileCount) * 100 / float64(bookCount)
	}
	return map[string]any{
		"id":                a.ID,
		"authorName":        a.Name,
		"foreignAuthorId":   a.ForeignID,
		"titleSlug":         a.TitleSlug,
		"overview":          a.Overview,
		"status":            "ended",
		"monitored":         true,
		"path":              a.Path,
		"qualityProfileId":  1,
		"metadataProfileId": 1,
		"statistics": map[string]any{
			"bookCount":          bookCount,
			"bookFileCount":      fileCount,
			"availableBookCount": fileCount,
			"totalBookCount":     bookCount,
			"sizeOnDisk":         size,
			"percentOfBooks":     percent,
		},
		"images": []any{map[string]any{
			"coverType": "poster",
			"url":       chapAuthorImagePath(a.ID),
		}},
		"genres": a.Genres,
	}
}

// chapLockedBookJSON renders one Chaptarr book record. asLibrary=false
// renders the metadata-lookup view of a record not in the library (id 0,
// unmonitored, zero statistics) while keeping the cover path keyed on the
// reserved record id.
func chapLockedBookJSON(bookID int, asLibrary bool) map[string]any {
	b := chapRecToBook[bookID]
	if b == nil {
		return nil
	}
	format := chapRecFormat[bookID]
	bf := b.Formats[format]
	meta := chapMetaByFID[b.ForeignID]
	st := chapRecStates[bookID]
	id, monitored, fileCount, size := 0, false, 0, 0
	if asLibrary && bf != nil {
		id = bookID
		monitored = bf.Monitored
		if bf.Downloaded && st != nil {
			fileCount = 1
			size = st.FileSize
		}
	}
	percent := 0.0
	if fileCount > 0 {
		percent = 100.0
	}
	quality := "EPUB"
	if format == bookFormatAudiobook {
		quality = "M4B"
	}
	if st != nil && st.Quality != "" {
		quality = st.Quality
	}
	edition := map[string]any{
		"id":        1000 + bookID,
		"bookId":    id,
		"title":     fmt.Sprintf("%s (%s)", b.Title, quality),
		"format":    quality,
		"publisher": "Demo Classics Press",
		"pageCount": meta.PageCount,
		"overview":  b.Overview,
		"monitored": true,
		"manualAdd": false,
		"isEbook":   format != bookFormatAudiobook,
		"images":    []any{},
	}
	return map[string]any{
		"id":            id,
		"title":         b.Title,
		"authorId":      meta.AuthorID,
		"foreignBookId": b.ForeignID,
		"titleSlug":     meta.TitleSlug,
		"overview":      b.Overview,
		"releaseDate":   meta.ReleaseDate + "T00:00:00Z",
		"monitored":     monitored,
		"mediaType":     format,
		"seriesTitle":   meta.SeriesTitle,
		"anyEditionOk":  true,
		"pageCount":     meta.PageCount,
		"author": map[string]any{
			"id":              meta.AuthorID,
			"authorName":      b.AuthorName,
			"foreignAuthorId": b.AuthorForeignID,
		},
		"statistics": map[string]any{
			"bookFileCount":  fileCount,
			"bookCount":      1,
			"sizeOnDisk":     size,
			"percentOfBooks": percent,
		},
		"editions": []any{edition},
		"images": []any{map[string]any{
			"coverType": "cover",
			"url":       fmt.Sprintf("/MediaCover/Books/%d/cover.jpg", bookID),
		}},
		"genres": meta.Genres,
	}
}

func chapLockedQueueItemJSON(it *chapQueueItem, includeAuthor, includeBook bool) map[string]any {
	m := map[string]any{
		"id":                    it.ID,
		"authorId":              it.AuthorID,
		"bookId":                it.BookID,
		"title":                 it.Title,
		"status":                it.Status,
		"trackedDownloadState":  it.TrackedDownloadState,
		"trackedDownloadStatus": it.TrackedDownloadStatus,
		"protocol":              it.Protocol,
		"indexer":               it.Indexer,
		"downloadClient":        it.DownloadClient,
		"size":                  it.Size,
		"sizeleft":              it.Sizeleft,
		"timeleft":              it.Timeleft,
		"statusMessages":        []any{},
		"downloadId":            it.DownloadID,
		"quality":               chapQualityBlob(it.Quality),
	}
	if includeAuthor {
		if a := chapAuthorsByID[it.AuthorID]; a != nil {
			m["author"] = map[string]any{
				"id": a.ID, "authorName": a.Name, "foreignAuthorId": a.ForeignID,
			}
		}
	}
	if includeBook {
		if b := chapRecToBook[it.BookID]; b != nil {
			meta := chapMetaByFID[b.ForeignID]
			m["book"] = map[string]any{
				"id":          it.BookID,
				"title":       b.Title,
				"mediaType":   chapRecFormat[it.BookID],
				"releaseDate": meta.ReleaseDate + "T00:00:00Z",
			}
		}
	}
	return m
}

func chapHistoryJSON(rec *chapHistoryRec) map[string]any {
	return map[string]any{
		"id":          rec.ID,
		"sourceTitle": rec.SourceTitle,
		"eventType":   rec.EventType,
		"date":        rec.Date.UTC().Format(time.RFC3339),
		"quality":     chapQualityBlob(rec.Quality),
		"downloadId":  rec.DownloadID,
		"authorId":    rec.AuthorID,
		"bookId":      rec.BookID,
		"data": map[string]any{
			"indexer":      "DemoNZB (Prowlarr)",
			"releaseGroup": "DEMO",
			"mediaType":    rec.MediaType,
		},
	}
}

// ─── Handlers ───────────────────────────────────────────

func chapHandleAuthorList(w http.ResponseWriter) {
	chapMu.Lock()
	out := []map[string]any{}
	for _, a := range chapAuthors {
		if a.InLibrary {
			out = append(out, chapLockedAuthorJSON(a))
		}
	}
	chapMu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func chapHandleAuthorGet(w http.ResponseWriter, idStr string) {
	id, _ := strconv.Atoi(idStr)
	chapMu.Lock()
	a := chapAuthorsByID[id]
	var out map[string]any
	if a != nil && a.InLibrary {
		out = chapLockedAuthorJSON(a)
	}
	chapMu.Unlock()
	if out == nil {
		writeErr(w, http.StatusNotFound, "author not found")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func chapHandleAuthorDelete(w http.ResponseWriter, r *http.Request, idStr string) {
	id, _ := strconv.Atoi(idStr)
	deleteFiles := r.URL.Query().Get("deleteFiles") == "true"
	chapMu.Lock()
	a := chapAuthorsByID[id]
	if a == nil {
		chapMu.Unlock()
		writeErr(w, http.StatusNotFound, "author not found")
		return
	}
	a.InLibrary = false
	for recID, st := range chapRecStates {
		b := chapRecToBook[recID]
		if b == nil || chapMetaByFID[b.ForeignID].AuthorID != id {
			continue
		}
		st.InLibrary = false
		if bf := b.Formats[chapRecFormat[recID]]; bf != nil {
			bf.Monitored = false
			if deleteFiles {
				bf.Downloaded = false
				bf.FileID = 0
				st.FilePath = ""
				st.FileSize = 0
			}
		}
	}
	// Drop any queue items for this author.
	kept := chapQueue[:0]
	for _, it := range chapQueue {
		if it.AuthorID != id {
			kept = append(kept, it)
		}
	}
	chapQueue = kept
	chapMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{})
}

func chapHandleBookList(w http.ResponseWriter, r *http.Request) {
	authorID := queryInt(r, "authorId", 0)
	chapMu.Lock()
	out := []map[string]any{}
	for _, b := range chapBooks {
		meta := chapMetaByFID[b.ForeignID]
		if authorID > 0 && meta.AuthorID != authorID {
			continue
		}
		for _, format := range []string{bookFormatEbook, bookFormatAudiobook} {
			bf := b.Formats[format]
			if bf == nil {
				continue
			}
			if st := chapRecStates[bf.BookID]; st != nil && st.InLibrary {
				out = append(out, chapLockedBookJSON(bf.BookID, true))
			}
		}
	}
	chapMu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func chapHandleBookGet(w http.ResponseWriter, idStr string) {
	id, _ := strconv.Atoi(idStr)
	chapMu.Lock()
	var out map[string]any
	if st := chapRecStates[id]; st != nil && st.InLibrary {
		out = chapLockedBookJSON(id, true)
	}
	chapMu.Unlock()
	if out == nil {
		writeErr(w, http.StatusNotFound, "book not found")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func chapHandleBookLookup(w http.ResponseWriter, r *http.Request) {
	term := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("term")))
	out := []map[string]any{}
	if term == "" {
		writeJSON(w, http.StatusOK, out)
		return
	}
	chapMu.Lock()
	for _, b := range chapBooks {
		if !strings.Contains(strings.ToLower(b.Title), term) &&
			!strings.Contains(strings.ToLower(b.AuthorName), term) {
			continue
		}
		for _, format := range []string{bookFormatEbook, bookFormatAudiobook} {
			bf := b.Formats[format]
			if bf == nil {
				continue
			}
			st := chapRecStates[bf.BookID]
			out = append(out, chapLockedBookJSON(bf.BookID, st != nil && st.InLibrary))
		}
	}
	chapMu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func chapHandleBookMonitor(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BookIDs   []int `json:"bookIds"`
		Monitored bool  `json:"monitored"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	chapMu.Lock()
	out := []map[string]any{}
	for _, id := range body.BookIDs {
		b := chapRecToBook[id]
		st := chapRecStates[id]
		if b == nil || st == nil || !st.InLibrary {
			continue
		}
		if bf := b.Formats[chapRecFormat[id]]; bf != nil {
			bf.Monitored = body.Monitored
		}
		out = append(out, chapLockedBookJSON(id, true))
	}
	chapMu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func chapHandleBookAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ForeignBookID string `json:"foreignBookId"`
		MediaType     string `json:"mediaType"`
		AddOptions    struct {
			SearchForNewBook bool `json:"searchForNewBook"`
		} `json:"addOptions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.MediaType != bookFormatEbook && body.MediaType != bookFormatAudiobook {
		writeErr(w, http.StatusBadRequest, "invalid mediaType")
		return
	}
	changed := false
	chapMu.Lock()
	b := chapBooksByFID[body.ForeignBookID]
	if b == nil {
		chapMu.Unlock()
		writeErr(w, http.StatusNotFound, "book not found")
		return
	}
	bf := b.Formats[body.MediaType]
	if bf == nil {
		chapMu.Unlock()
		writeErr(w, http.StatusBadRequest, "no "+body.MediaType+" edition is available for this book")
		return
	}
	meta := chapMetaByFID[b.ForeignID]
	bf.Monitored = true
	if st := chapRecStates[bf.BookID]; st != nil {
		st.InLibrary = true
	}
	chapLockedJoinLibrary(chapAuthorsByID[meta.AuthorID])
	if body.AddOptions.SearchForNewBook && !bf.Downloaded && !chapLockedQueueHas(bf.BookID) {
		chapLockedEnqueue(bf.BookID)
		changed = true
	}
	out := chapLockedBookJSON(bf.BookID, true)
	chapMu.Unlock()
	if changed {
		chapArrQueueChangedPing()
	}
	writeJSON(w, http.StatusCreated, out)
}

func chapHandleBookFiles(w http.ResponseWriter, r *http.Request) {
	bookID := queryInt(r, "bookId", 0)
	authorID := queryInt(r, "authorId", 0)
	chapMu.Lock()
	out := []map[string]any{}
	for _, b := range chapBooks {
		meta := chapMetaByFID[b.ForeignID]
		if authorID > 0 && meta.AuthorID != authorID {
			continue
		}
		for _, format := range []string{bookFormatEbook, bookFormatAudiobook} {
			bf := b.Formats[format]
			if bf == nil || !bf.Downloaded || bf.FileID == 0 {
				continue
			}
			if bookID > 0 && bf.BookID != bookID {
				continue
			}
			st := chapRecStates[bf.BookID]
			if st == nil || !st.InLibrary {
				continue
			}
			out = append(out, map[string]any{
				"id":        bf.FileID,
				"authorId":  meta.AuthorID,
				"bookId":    bf.BookID,
				"editionId": 1000 + bf.BookID,
				"path":      st.FilePath,
				"size":      st.FileSize,
				"dateAdded": st.DateAdded.UTC().Format(time.RFC3339),
				"quality":   chapQualityBlob(st.Quality),
			})
		}
	}
	chapMu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func chapHandleQueue(w http.ResponseWriter, r *http.Request) {
	includeAuthor := r.URL.Query().Get("includeAuthor") == "true"
	includeBook := r.URL.Query().Get("includeBook") == "true"
	chapMu.Lock()
	records := []map[string]any{}
	for _, it := range chapQueue {
		records = append(records, chapLockedQueueItemJSON(it, includeAuthor, includeBook))
	}
	chapMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"page":         1,
		"pageSize":     queryInt(r, "pageSize", 100),
		"totalRecords": len(records),
		"records":      records,
	})
}

func chapHandleQueueDelete(w http.ResponseWriter, idStr string) {
	id, _ := strconv.Atoi(idStr)
	found := false
	chapMu.Lock()
	kept := chapQueue[:0]
	for _, it := range chapQueue {
		if it.ID == id {
			found = true
			continue
		}
		kept = append(kept, it)
	}
	chapQueue = kept
	chapMu.Unlock()
	if !found {
		writeErr(w, http.StatusNotFound, "queue item not found")
		return
	}
	chapArrQueueChangedPing()
	writeJSON(w, http.StatusOK, map[string]any{})
}

func chapHandleHistory(w http.ResponseWriter, r *http.Request) {
	bookID := queryInt(r, "bookId", 0)
	chapMu.Lock()
	rows := make([]*chapHistoryRec, 0, len(chapHistory))
	for _, rec := range chapHistory {
		if bookID > 0 && rec.BookID != bookID {
			continue
		}
		rows = append(rows, rec)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Date.After(rows[j].Date) })
	total := len(rows)
	page, size, start, end := chapPageBounds(r, total)
	records := []map[string]any{}
	for _, rec := range rows[start:end] {
		records = append(records, chapHistoryJSON(rec))
	}
	chapMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"page":         page,
		"pageSize":     size,
		"totalRecords": total,
		"records":      records,
	})
}

func chapHandleWanted(w http.ResponseWriter, r *http.Request, cutoff bool) {
	chapMu.Lock()
	type wantedRow struct {
		json    map[string]any
		release string
	}
	rows := []wantedRow{}
	for _, b := range chapBooks {
		meta := chapMetaByFID[b.ForeignID]
		for _, format := range []string{bookFormatEbook, bookFormatAudiobook} {
			bf := b.Formats[format]
			if bf == nil {
				continue
			}
			st := chapRecStates[bf.BookID]
			if st == nil || !st.InLibrary || !bf.Monitored {
				continue
			}
			match := false
			if cutoff {
				match = bf.Downloaded && chapCutoffUnmet[bf.BookID]
			} else {
				match = !bf.Downloaded
			}
			if !match {
				continue
			}
			rows = append(rows, wantedRow{
				json:    chapLockedBookJSON(bf.BookID, true),
				release: meta.ReleaseDate,
			})
		}
	}
	chapMu.Unlock()
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].release > rows[j].release })
	total := len(rows)
	page, size, start, end := chapPageBounds(r, total)
	records := []map[string]any{}
	for _, row := range rows[start:end] {
		records = append(records, row.json)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"page":         page,
		"pageSize":     size,
		"totalRecords": total,
		"records":      records,
	})
}

func chapHandleReleaseSearch(w http.ResponseWriter, r *http.Request) {
	bookID := queryInt(r, "bookId", 0)
	chapMu.Lock()
	out := chapLockedReleases(bookID)
	chapMu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

// chapLockedReleases builds the canned interactive-search results for one
// book record: two grabbable releases (usenet + torrent) and one rejected
// lower-quality release. ageHours values are deliberately fractional.
func chapLockedReleases(bookID int) []map[string]any {
	b := chapRecToBook[bookID]
	if b == nil {
		return []map[string]any{}
	}
	format := chapRecFormat[bookID]
	q1, q2 := "EPUB", "MOBI"
	size1, size2 := 2_100_000, 1_700_000
	if format == bookFormatAudiobook {
		q1, q2 = "M4B", "MP3"
		size1, size2 = 388_000_000, 540_000_000
	}
	dotted := strings.ReplaceAll(b.Title, " ", ".")
	return []map[string]any{
		{
			"guid":       fmt.Sprintf("demo-chaptarr-rel-%d-1", bookID),
			"indexerId":  1,
			"title":      fmt.Sprintf("%s (%d) Unabridged %s-DEMO", b.Title, b.Year, q1),
			"quality":    chapQualityBlob(q1),
			"size":       size1,
			"age":        4,
			"ageHours":   96.5,
			"indexer":    "DemoNZB (Prowlarr)",
			"protocol":   "usenet",
			"rejected":   false,
			"rejections": []any{},
		},
		{
			"guid":       fmt.Sprintf("demo-chaptarr-rel-%d-2", bookID),
			"indexerId":  2,
			"title":      fmt.Sprintf("%s.%d.Retail.%s-DEMO", dotted, b.Year, q1),
			"quality":    chapQualityBlob(q1),
			"size":       size1 + size1/8,
			"age":        11,
			"ageHours":   270.3,
			"indexer":    "DemoTorrents (Prowlarr)",
			"protocol":   "torrent",
			"seeders":    42,
			"leechers":   3,
			"rejected":   false,
			"rejections": []any{},
		},
		{
			"guid":       fmt.Sprintf("demo-chaptarr-rel-%d-3", bookID),
			"indexerId":  1,
			"title":      fmt.Sprintf("%s (%d) %s-DEMO", b.Title, b.Year, q2),
			"quality":    chapQualityBlob(q2),
			"size":       size2,
			"age":        30,
			"ageHours":   723.8,
			"indexer":    "DemoNZB (Prowlarr)",
			"protocol":   "usenet",
			"rejected":   true,
			"rejections": []any{"Quality below cutoff"},
		},
	}
}

func chapHandleReleaseGrab(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Guid      string `json:"guid"`
		IndexerID int    `json:"indexerId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	changed := false
	if tail, ok := strings.CutPrefix(body.Guid, "demo-chaptarr-rel-"); ok {
		if idStr, _, found := strings.Cut(tail, "-"); found {
			if bookID, err := strconv.Atoi(idStr); err == nil {
				chapMu.Lock()
				if b := chapRecToBook[bookID]; b != nil {
					bf := b.Formats[chapRecFormat[bookID]]
					if st := chapRecStates[bookID]; st != nil {
						st.InLibrary = true
					}
					if bf != nil {
						bf.Monitored = true
						if !bf.Downloaded && !chapLockedQueueHas(bookID) {
							chapLockedEnqueue(bookID)
							changed = true
						}
					}
					meta := chapMetaByFID[b.ForeignID]
					chapLockedJoinLibrary(chapAuthorsByID[meta.AuthorID])
				}
				chapMu.Unlock()
			}
		}
	}
	if changed {
		chapArrQueueChangedPing()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"guid": body.Guid, "indexerId": body.IndexerID,
	})
}

func chapHandleCommand(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		BookIDs  []int  `json:"bookIds"`
		AuthorID int    `json:"authorId"`
		Files    []struct {
			BookID int `json:"bookId"`
		} `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	changed := false
	switch body.Name {
	case "BookSearch":
		chapMu.Lock()
		for _, id := range body.BookIDs {
			b := chapRecToBook[id]
			st := chapRecStates[id]
			if b == nil || st == nil || !st.InLibrary {
				continue
			}
			if bf := b.Formats[chapRecFormat[id]]; bf != nil && !bf.Downloaded && !chapLockedQueueHas(id) {
				chapLockedEnqueue(id)
				changed = true
			}
		}
		chapMu.Unlock()
	case "AuthorSearch":
		chapMu.Lock()
		for id, st := range chapRecStates {
			if !st.InLibrary {
				continue
			}
			b := chapRecToBook[id]
			if b == nil || chapMetaByFID[b.ForeignID].AuthorID != body.AuthorID {
				continue
			}
			if bf := b.Formats[chapRecFormat[id]]; bf != nil && bf.Monitored && !bf.Downloaded && !chapLockedQueueHas(id) {
				chapLockedEnqueue(id)
				changed = true
			}
		}
		chapMu.Unlock()
	case "ManualImport":
		chapMu.Lock()
		for _, f := range body.Files {
			b := chapRecToBook[f.BookID]
			if b == nil {
				continue
			}
			format := chapRecFormat[f.BookID]
			if bf := b.Formats[format]; bf != nil && !bf.Downloaded {
				chapLockedMakeAvailable(b, chapMetaByFID[b.ForeignID], format, bf)
				changed = true
			}
		}
		chapMu.Unlock()
	case "ProcessMonitoredDownloads", "RescanAuthor":
		// No-op in the demo.
	}
	if changed {
		chapArrQueueChangedPing()
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": 1, "name": body.Name, "status": "queued",
	})
}

func chapHandleManualImport(w http.ResponseWriter, r *http.Request) {
	downloadID := r.URL.Query().Get("downloadId")
	chapMu.Lock()
	out := []map[string]any{}
	for _, it := range chapQueue {
		if downloadID != "" && it.DownloadID != downloadID {
			continue
		}
		b := chapRecToBook[it.BookID]
		if b == nil {
			continue
		}
		a := chapAuthorsByID[it.AuthorID]
		authorName := b.AuthorName
		if a != nil {
			authorName = a.Name
		}
		ext := strings.ToLower(it.Quality)
		fileName := fmt.Sprintf("%s (%d).%s", b.Title, b.Year, ext)
		out = append(out, map[string]any{
			"id":           it.ID,
			"path":         fmt.Sprintf("/downloads/complete/%s/%s", it.Title, fileName),
			"folderName":   it.Title,
			"name":         fileName,
			"relativePath": fileName,
			"size":         it.Size,
			"author":       map[string]any{"id": it.AuthorID, "authorName": authorName},
			"book": map[string]any{
				"id": it.BookID, "title": b.Title,
				"mediaType": chapRecFormat[it.BookID],
			},
			"quality":      chapQualityBlob(it.Quality),
			"releaseGroup": "DEMO",
			"downloadId":   it.DownloadID,
			"rejections":   []any{},
		})
	}
	chapMu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func chapHandleRootFolders(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, []map[string]any{
		{"id": 1, "path": "/books", "accessible": true, "freeSpace": 734003200000},
	})
}

func chapPageBounds(r *http.Request, total int) (page, size, start, end int) {
	page = queryInt(r, "page", 1)
	if page < 1 {
		page = 1
	}
	size = queryInt(r, "pageSize", 50)
	if size < 1 {
		size = 50
	}
	start = (page - 1) * size
	if start > total {
		start = total
	}
	end = start + size
	if end > total {
		end = total
	}
	return page, size, start, end
}

// ─── MediaCover placeholder covers ──────────────────────

// chapAuthorCoverSeed lifts author ids clear of the book-record id space so a
// generated portrait never comes out identical to the cover of the book record
// that happens to share its number.
const chapAuthorCoverSeed = 90000

// chapHandleMediaCover serves a book cover: MediaCover/Books/{bookId}/<file>.
func chapHandleMediaCover(w http.ResponseWriter, tail string) {
	chapWriteMediaCover(w, tail, 0)
}

// chapHandleAuthorMediaCover serves an author portrait:
// MediaCover/{authorId}/<file>. Chaptarr has no Authors/ subtree — the author
// id sits directly under MediaCover — so this path must not be confused with a
// book cover's.
func chapHandleAuthorMediaCover(w http.ResponseWriter, tail string) {
	chapWriteMediaCover(w, tail, chapAuthorCoverSeed)
}

func chapWriteMediaCover(w http.ResponseWriter, tail string, seedOffset int) {
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
	data := chapCoverPNG(seedOffset + id)
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

var (
	chapCoverMu    sync.Mutex
	chapCoverCache = map[int][]byte{}
)

// chapCoverPNG generates (and caches) a deterministic 300x450 placeholder
// cover: a solid color derived from the book id plus a simple geometric
// variation, so every record is visually distinct without shipping assets.
func chapCoverPNG(bookID int) []byte {
	chapCoverMu.Lock()
	if data, ok := chapCoverCache[bookID]; ok {
		chapCoverMu.Unlock()
		return data
	}
	chapCoverMu.Unlock()

	const width, height = 300, 450
	seed := uint32(bookID)*2654435761 + 40503
	base := chapHSVColor(float64(seed%360), 0.52, 0.42)
	accent := chapHSVColor(float64((seed/360)%360), 0.58, 0.78)
	trim := chapHSVColor(float64(seed%360), 0.60, 0.22)

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, base)
		}
	}
	switch bookID % 4 {
	case 0: // horizontal band
		for y := height / 3; y < height/3+90 && y < height; y++ {
			for x := 0; x < width; x++ {
				img.SetRGBA(x, y, accent)
			}
		}
	case 1: // centered disc
		cx, cy, radius := width/2, height/2-30, 88
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				dx, dy := x-cx, y-cy
				if dx*dx+dy*dy <= radius*radius {
					img.SetRGBA(x, y, accent)
				}
			}
		}
	case 2: // diagonal stripes
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				if ((x+y)/48)%2 == 0 {
					img.SetRGBA(x, y, accent)
				}
			}
		}
	case 3: // bottom panel + top-left square
		for y := height - 130; y < height; y++ {
			for x := 0; x < width; x++ {
				img.SetRGBA(x, y, accent)
			}
		}
		for y := 40; y < 110; y++ {
			for x := 40; x < 110; x++ {
				img.SetRGBA(x, y, accent)
			}
		}
	}
	// Frame.
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if x < 12 || x >= width-12 || y < 12 || y >= height-12 {
				img.SetRGBA(x, y, trim)
			}
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	data := buf.Bytes()
	chapCoverMu.Lock()
	chapCoverCache[bookID] = data
	chapCoverMu.Unlock()
	return data
}

// chapHSVColor converts an HSV triple (h in degrees, s/v in 0..1) to RGBA.
func chapHSVColor(h, s, v float64) color.RGBA {
	h = math.Mod(h, 360)
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return color.RGBA{
		R: uint8((r + m) * 255),
		G: uint8((g + m) * 255),
		B: uint8((b + m) * 255),
		A: 255,
	}
}
