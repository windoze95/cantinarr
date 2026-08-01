// books.go — book-side request surfaces (srv-requests §2, app-requests
// §5–§7): GET /api/requests/book-status, /book-library, /book-recent, plus
// the per-format book request lifecycle (gap-plan §4.4) shared with
// requests.go and requests_admin.go.
//
// Book data comes from the D8 hooks (bookByForeignID / allBooks /
// chaptarrOnBookRequested / chaptarrOnBookAvailable); the per-format request
// state kept here (downloading flags, active sims, recent imports) is guarded
// by the request domain's reqMu (see data_requests.go).
package main

import (
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// ─── Domain-local state (guarded by reqMu) ──────────────

var (
	// bookDownloadingSet marks formats mid-download, keyed "<foreignID>|<format>".
	bookDownloadingSet = map[string]bool{}
	// bookSimActive dedupes per-format download simulations (same key).
	bookSimActive = map[string]bool{}
	// bookRecentItems backs GET /api/requests/book-recent (newest appended last).
	bookRecentItems []bookRecentItem
)

var bookRecentSeedOnce sync.Once

type bookRecentItem struct {
	BookID     int
	ForeignID  string
	Title      string
	Format     string
	Cover      string
	ImportedAt time.Time
}

func registerBooks(r chi.Router) {
	r.Get("/requests/book-status", bookStatusHandler)
	r.Get("/requests/book-library", bookLibraryHandler)
	r.Get("/requests/book-recent", bookRecentHandler)
}

// ─── Format helpers ─────────────────────────────────────

// bookConcreteFormats expands a format value to concrete formats
// ("both" → ebook + audiobook).
func bookConcreteFormats(format string) []string {
	switch format {
	case bookFormatEbook, bookFormatAudiobook:
		return []string{format}
	}
	return []string{bookFormatEbook, bookFormatAudiobook}
}

// bookFormatFromSlice collapses a concrete-format list back to a stored
// format value.
func bookFormatFromSlice(formats []string) string {
	hasEbook, hasAudio := false, false
	for _, f := range formats {
		switch f {
		case bookFormatEbook:
			hasEbook = true
		case bookFormatAudiobook:
			hasAudio = true
		}
	}
	switch {
	case hasEbook && hasAudio:
		return bookFormatBoth
	case hasAudio:
		return bookFormatAudiobook
	case hasEbook:
		return bookFormatEbook
	}
	return bookFormatBoth
}

// bookMergeFormats merges an existing stored format value with additional
// concrete formats into one concrete-format list.
func bookMergeFormats(existing string, add []string) []string {
	set := map[string]bool{}
	if existing != "" {
		for _, f := range bookConcreteFormats(reqNormalizeBookFormat(existing)) {
			set[f] = true
		}
	}
	for _, f := range add {
		set[f] = true
	}
	out := []string{}
	for _, f := range []string{bookFormatEbook, bookFormatAudiobook} {
		if set[f] {
			out = append(out, f)
		}
	}
	return out
}

// bookCollapse folds a per-format status map into one collapsed status
// (gap-plan §4.4 order): all-available → available; any-available → partial;
// downloading; requested; pending; denied; else unavailable.
func bookCollapse(formats map[string]string) string {
	if len(formats) == 0 {
		return statusUnavailable
	}
	allAvailable := true
	var anyAvailable, anyDownloading, anyRequested, anyPending, anyDenied bool
	for _, s := range formats {
		if s != statusAvailable {
			allAvailable = false
		}
		switch s {
		case statusAvailable:
			anyAvailable = true
		case statusDownloading:
			anyDownloading = true
		case statusRequested, statusPartial:
			anyRequested = true
		case statusPending:
			anyPending = true
		case statusDenied:
			anyDenied = true
		}
	}
	switch {
	case allAvailable:
		return statusAvailable
	case anyAvailable:
		return statusPartial
	case anyDownloading:
		return statusDownloading
	case anyRequested:
		return statusRequested
	case anyPending:
		return statusPending
	case anyDenied:
		return statusDenied
	}
	return statusUnavailable
}

// bookIsDownloading reports whether a format is mid-download.
func bookIsDownloading(foreignID, format string) bool {
	reqMu.Lock()
	defer reqMu.Unlock()
	return bookDownloadingSet[foreignID+"|"+format]
}

// bookLiveFormatStatus is the live projection for one format:
// file on disk → available; active download → downloading; monitored →
// requested; "" when the live library holds no evidence. Never call while
// holding reqMu.
func bookLiveFormatStatus(book *DemoBook, foreignID, format string) string {
	rec := book.Formats[format]
	if rec != nil && rec.Downloaded {
		return statusAvailable
	}
	if bookIsDownloading(foreignID, format) {
		return statusDownloading
	}
	if rec != nil && rec.Monitored {
		return statusRequested
	}
	return ""
}

// ─── Instance resolution ────────────────────────────────

// bookResolveInstance resolves the Chaptarr instance a book call targets.
// Returns (nil, httpStatus, message) on failure using the srv-requests error
// mapping: 403 forbidden instance / 400 invalid instance / 500 unconfigured.
func bookResolveInstance(u *DemoUser, explicitID string) (*DemoInstance, int, string) {
	if explicitID != "" {
		inst := instanceByID(explicitID)
		if inst == nil || inst.ServiceType != serviceChaptarr {
			return nil, http.StatusBadRequest, "invalid chaptarr instance"
		}
		if u == nil || u.Role != roleAdmin {
			eff := effectiveInstanceFor(u, serviceChaptarr)
			if eff == nil || eff.ID != explicitID {
				return nil, http.StatusForbidden, "chaptarr instance is not available to you"
			}
		}
		return inst, 0, ""
	}
	inst := effectiveInstanceFor(u, serviceChaptarr)
	if inst == nil && u != nil && u.Role == roleAdmin {
		for _, cand := range allInstances() {
			if cand.ServiceType == serviceChaptarr {
				inst = cand
				break
			}
		}
	}
	if inst == nil {
		return nil, http.StatusInternalServerError, "chaptarr is not configured for you"
	}
	return inst, 0, ""
}

// ─── GET /api/requests/book-status ──────────────────────

func bookStatusHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	foreignID := strings.TrimSpace(r.URL.Query().Get("foreign_id"))
	if foreignID == "" {
		writeErr(w, http.StatusBadRequest, "foreign_id required")
		return
	}
	instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if instanceID != "" {
		if _, errStatus, errMsg := bookResolveInstance(u, instanceID); errStatus != 0 {
			writeErr(w, errStatus, errMsg)
			return
		}
	}
	book, found := bookByForeignID(foreignID)
	formats := bookFormatStatusesFor(u, book, found, foreignID, instanceID)
	resp := map[string]any{
		"status":   bookCollapse(formats),
		"progress": 0,
	}
	if len(formats) > 0 {
		resp["book_formats"] = formats
	}
	writeJSON(w, http.StatusOK, resp)
}

// bookFormatStatusesFor computes the per-format status map: live overlay
// (file → available, active download → downloading, monitored → requested)
// outranks the logged status; pending/denied survive while there is no live
// work; anything else heals to unavailable.
func bookFormatStatusesFor(u *DemoUser, book *DemoBook, found bool, foreignID, instanceID string) map[string]string {
	logged := map[string]string{}
	reqMu.Lock()
	for i := len(reqLog) - 1; i >= 0; i-- {
		row := reqLog[i]
		if row.MediaType != mediaTypeBook || row.ForeignID != foreignID {
			continue
		}
		if instanceID != "" && row.InstanceID != "" && row.InstanceID != instanceID {
			continue
		}
		// The caller's own rows, plus ANYONE's pending rows.
		if row.UserID != u.ID && row.Status != statusPending {
			continue
		}
		for _, f := range bookConcreteFormats(reqNormalizeBookFormat(row.BookFormat)) {
			if _, ok := logged[f]; !ok {
				logged[f] = row.Status
			}
		}
	}
	reqMu.Unlock()

	keys := []string{}
	if found {
		keys = []string{bookFormatEbook, bookFormatAudiobook}
	} else {
		for _, f := range []string{bookFormatEbook, bookFormatAudiobook} {
			if _, ok := logged[f]; ok {
				keys = append(keys, f)
			}
		}
	}
	out := map[string]string{}
	for _, f := range keys {
		live := ""
		if found {
			live = bookLiveFormatStatus(book, foreignID, f)
		}
		switch {
		case live != "":
			out[f] = live
		case logged[f] == statusPending || logged[f] == statusDenied:
			out[f] = logged[f]
		default:
			out[f] = statusUnavailable
		}
	}
	return out
}

// ─── GET /api/requests/book-library ─────────────────────

func bookLibraryHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if instanceID != "" {
		if _, errStatus, errMsg := bookResolveInstance(u, instanceID); errStatus != 0 {
			writeErr(w, errStatus, errMsg)
			return
		}
	} else if u.Role != roleAdmin && effectiveInstanceFor(u, serviceChaptarr) == nil {
		// No Chaptarr access: an empty digest, NOT an error.
		writeJSON(w, http.StatusOK, map[string]any{"titles": []map[string]any{}})
		return
	}
	titles := []map[string]any{}
	for _, b := range allBooks() {
		tracked := false
		for _, rec := range b.Formats {
			if rec != nil && (rec.Monitored || rec.Downloaded) {
				tracked = true
				break
			}
		}
		if !tracked {
			continue
		}
		titles = append(titles, map[string]any{
			"title":           b.Title,
			"author":          b.AuthorName,
			"year":            b.Year,
			"foreign_book_id": b.ForeignID,
			"status_known":    true,
			"cover":           b.CoverPath(),
			"ebook":           bookOwnershipJSON(b.Formats[bookFormatEbook]),
			"audiobook":       bookOwnershipJSON(b.Formats[bookFormatAudiobook]),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"titles": titles})
}

// bookOwnershipJSON renders one {monitored,downloaded} block — ALWAYS
// present, zero-valued when the format has no record.
func bookOwnershipJSON(rec *DemoBookFormat) map[string]any {
	if rec == nil {
		return map[string]any{"monitored": false, "downloaded": false}
	}
	return map[string]any{"monitored": rec.Monitored, "downloaded": rec.Downloaded}
}

// ─── GET /api/requests/book-recent ──────────────────────

func bookRecentHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if instanceID != "" {
		if _, errStatus, errMsg := bookResolveInstance(u, instanceID); errStatus != 0 {
			writeErr(w, errStatus, errMsg)
			return
		}
	} else if u.Role != roleAdmin && effectiveInstanceFor(u, serviceChaptarr) == nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": []map[string]any{}})
		return
	}
	limit := queryInt(r, "limit", 20)
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	bookRecentEnsureSeeded()
	reqMu.Lock()
	items := make([]bookRecentItem, len(bookRecentItems))
	copy(items, bookRecentItems)
	reqMu.Unlock()
	// Newest-first by imported_at; tie-break: higher book_id first.
	sort.Slice(items, func(i, j int) bool {
		if items[i].ImportedAt.Equal(items[j].ImportedAt) {
			return items[i].BookID > items[j].BookID
		}
		return items[i].ImportedAt.After(items[j].ImportedAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]any{
			"book_id":         it.BookID,
			"foreign_book_id": it.ForeignID,
			"title":           it.Title,
			"format":          it.Format,
			"cover":           it.Cover,
			"imported_at":     it.ImportedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// bookRecentEnsureSeeded lazily derives the seed "recently added" list from
// the catalog's downloaded formats (hooks must not run from init()).
func bookRecentEnsureSeeded() {
	bookRecentSeedOnce.Do(func() {
		seeded := []bookRecentItem{}
		now := time.Now()
		for _, b := range allBooks() {
			for _, f := range []string{bookFormatEbook, bookFormatAudiobook} {
				rec := b.Formats[f]
				if rec == nil || !rec.Downloaded {
					continue
				}
				seeded = append(seeded, bookRecentItem{
					BookID:    rec.BookID,
					ForeignID: b.ForeignID,
					Title:     b.Title,
					Format:    f,
					Cover:     b.CoverPath(),
					// Staggered into the past, matching the chaptarr fixtures'
					// per-record cadence.
					ImportedAt: now.Add(-time.Duration(rec.BookID) * 26 * time.Hour),
				})
			}
		}
		reqMu.Lock()
		bookRecentItems = append(seeded, bookRecentItems...)
		reqMu.Unlock()
	})
}

// bookRecentAppend records a fresh import (called on simulation completion).
func bookRecentAppend(foreignID, format string) {
	book, found := bookByForeignID(foreignID)
	if !found {
		return
	}
	rec := book.Formats[format]
	if rec == nil {
		return
	}
	bookRecentEnsureSeeded()
	reqMu.Lock()
	bookRecentItems = append(bookRecentItems, bookRecentItem{
		BookID:     rec.BookID,
		ForeignID:  book.ForeignID,
		Title:      book.Title,
		Format:     format,
		Cover:      book.CoverPath(),
		ImportedAt: time.Now(),
	})
	reqMu.Unlock()
}

// ─── Per-format download simulation (gap-plan §4.4) ─────

// bookStartFormatSimulation runs one format's download machine:
// requested (~10 s) → downloading (~21 s, arr_queue_changed pings) →
// available (library digest flip + book-recent append + invalidation ping).
// Transitions are visible by book-status polling alone. The caller has
// already invoked chaptarrOnBookRequested for this format.
func bookStartFormatSimulation(foreignID, format, instanceID string) {
	key := foreignID + "|" + format
	reqMu.Lock()
	if bookSimActive[key] {
		reqMu.Unlock()
		return
	}
	bookSimActive[key] = true
	reqMu.Unlock()
	go func() {
		// Phase 1 — requested.
		time.Sleep(10 * time.Second)
		reqMu.Lock()
		bookDownloadingSet[key] = true
		reqMu.Unlock()
		wsBroadcast(evtArrQueueChanged, map[string]any{
			"instance_id": instanceID, "service_type": serviceChaptarr,
		})
		// Phase 2 — downloading.
		time.Sleep(21 * time.Second)
		// Phase 3 — available: the bookfile lands, the digest flips.
		chaptarrOnBookAvailable(foreignID, format)
		reqMu.Lock()
		delete(bookDownloadingSet, key)
		delete(bookSimActive, key)
		reqMu.Unlock()
		bookRecentAppend(foreignID, format)
		bookRefreshLogRows(foreignID)
		wsBroadcast(evtRequestStatusChanged, map[string]any{
			"media_type":  mediaTypeBook,
			"foreign_id":  foreignID,
			"status":      statusAvailable,
			"instance_id": instanceID,
		})
	}()
}

// bookRefreshLogRows recomputes stored non-pending/non-denied book rows for a
// title from the live projection (so history rows flip to available).
func bookRefreshLogRows(foreignID string) {
	book, found := bookByForeignID(foreignID)
	if !found {
		return
	}
	live := map[string]string{}
	for _, f := range []string{bookFormatEbook, bookFormatAudiobook} {
		live[f] = bookLiveFormatStatus(book, foreignID, f)
	}
	reqMu.Lock()
	defer reqMu.Unlock()
	for _, row := range reqLog {
		if row.MediaType != mediaTypeBook || row.ForeignID != foreignID {
			continue
		}
		if row.Status == statusPending || row.Status == statusDenied {
			continue
		}
		statuses := map[string]string{}
		for _, f := range bookConcreteFormats(reqNormalizeBookFormat(row.BookFormat)) {
			if live[f] != "" {
				statuses[f] = live[f]
			} else {
				statuses[f] = statusUnavailable
			}
		}
		row.Status = bookCollapse(statuses)
	}
}
