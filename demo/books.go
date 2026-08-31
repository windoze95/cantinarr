// books.go — book-side request surfaces (srv-requests §2, app-requests
// §5–§7): GET /api/requests/book-status, /book-library, /book-recent, the
// authors/series browse rows and their detail pages, plus the per-format book
// request lifecycle (gap-plan §4.4) shared with requests.go and
// requests_admin.go.
//
// Book data comes from the D8 hooks (bookByForeignID / allBooks /
// chaptarrOnBookRequested / chaptarrOnBookAvailable); the per-format request
// state kept here (downloading flags, active sims, recent imports) is guarded
// by the request domain's reqMu (see data_requests.go).
package main

import (
	"net/http"
	"sort"
	"strconv"
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
	r.Get("/requests/book-authors", bookAuthorsHandler)
	r.Get("/requests/book-author", bookAuthorHandler)
	r.Get("/requests/book-series", bookSeriesHandler)
	r.Get("/requests/book-series-detail", bookSeriesDetailHandler)
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
	formats, waits := bookFormatStatusesFor(u, book, found, foreignID, instanceID)
	resp := map[string]any{
		"status":   bookCollapse(formats),
		"progress": 0,
	}
	if len(formats) > 0 {
		resp["book_formats"] = formats
	}
	if len(waits) > 0 {
		resp["book_format_waits"] = waits
	}
	writeJSON(w, http.StatusOK, resp)
}

// bookFormatStatusesFor computes the per-format status map: live overlay
// (file → available, active download → downloading, monitored → requested)
// outranks the logged status; pending/denied survive while there is no live
// work; anything else heals to unavailable.
//
// The second result explains the waits: a format whose governing row is a
// server-owned author-import park reads "requested" (the system finishes it
// unattended, so no approval story is narrated) and carries a
// book_format_waits entry saying why the library has no record yet. It is
// applied strictly after the live overlay, so a format the library has
// actually taken carries no wait.
func bookFormatStatusesFor(u *DemoUser, book *DemoBook, found bool, foreignID, instanceID string) (map[string]string, map[string]any) {
	logged := map[string]string{}
	parked := map[string]bool{}
	parkedSince := map[string]time.Time{}
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
		isImportPark := row.Status == statusPending && row.ParkReason == reqParkReasonAuthorImport
		for _, f := range bookConcreteFormats(reqNormalizeBookFormat(row.BookFormat)) {
			if _, ok := logged[f]; !ok {
				logged[f] = row.Status
				parked[f] = isImportPark
				if isImportPark {
					parkedSince[f] = row.RequestedAt
				}
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
	waits := map[string]any{}
	for _, f := range keys {
		live := ""
		if found {
			live = bookLiveFormatStatus(book, foreignID, f)
		}
		switch {
		case live != "":
			out[f] = live
		case logged[f] == statusPending && parked[f]:
			out[f] = statusRequested
			waits[f] = reqBookWaitJSON(parkedSince[f])
		case logged[f] == statusPending || logged[f] == statusDenied:
			out[f] = logged[f]
		default:
			out[f] = statusUnavailable
		}
	}
	return out, waits
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
		titles = append(titles, bookLibraryTitleJSON(b))
	}
	writeJSON(w, http.StatusOK, map[string]any{"titles": titles})
}

// bookLibraryTitleJSON renders one LibraryTitle — the per-title ownership row
// book-library, book-author and book-series-detail all emit. year is an int so
// it marshals as a JSON integer; the app parses it as an int and a float
// throws.
func bookLibraryTitleJSON(b *DemoBook) map[string]any {
	return map[string]any{
		"title":           b.Title,
		"author":          b.AuthorName,
		"year":            b.Year,
		"foreign_book_id": b.ForeignID,
		"status_known":    true,
		"cover":           b.CoverPath(),
		"ebook":           bookOwnershipJSON(b.Formats[bookFormatEbook]),
		"audiobook":       bookOwnershipJSON(b.Formats[bookFormatAudiobook]),
	}
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

// ─── Authors & series browse rows ───────────────────────

// The orders the browse rows can be read in. An unknown value falls back to
// the default rather than erroring: a newer client asking for a sort this
// server does not have should get a usable row, not a 400.
//
// There is deliberately no "date added" order for series. An author record
// carries an added date; a series is not a record at all — it exists only as a
// string on each book — so the only dates in reach are publication and import
// dates, and neither is "when this series entered your library".
const (
	bookSortByBooks = "books"
	bookSortByName  = "name"
	bookSortByAdded = "added"
)

// bookBrowseMaxItems caps both browse rows. These are shelves to scan, not the
// library — someone after one specific author or series searches for them.
//
// The cap is applied AFTER the requested sort, never before: capping first
// would make "by name" mean "the most-collected, alphabetised", which looks
// complete while silently omitting everyone below the cut.
const bookBrowseMaxItems = 200

// bookSeriesCoverDepth is how many covers a series card stacks. A deeper stack
// renders as a smudge.
const bookSeriesCoverDepth = 3

// bookBrowseResolve resolves the caller's Chaptarr access for a browse call.
//
// ok=false with a zero status means the caller simply has no Chaptarr grant.
// The browse rows answer that with an empty digest — the row is absent for
// them, which is not a failure — while the detail pages answer 403: they asked
// about a library they cannot see at all, and a 404 there would claim this
// library was searched and came up empty.
func bookBrowseResolve(u *DemoUser, r *http.Request) (ok bool, status int, msg string) {
	instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if instanceID != "" {
		if _, errStatus, errMsg := bookResolveInstance(u, instanceID); errStatus != 0 {
			return false, errStatus, errMsg
		}
		return true, 0, ""
	}
	if u.Role != roleAdmin && effectiveInstanceFor(u, serviceChaptarr) == nil {
		return false, 0, ""
	}
	return true, 0, ""
}

// ─── GET /api/requests/book-authors ─────────────────────

func bookAuthorsHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	ok, status, msg := bookBrowseResolve(u, r)
	if !ok {
		if status != 0 {
			writeErr(w, status, msg)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"authors": []map[string]any{}, "total": 0})
		return
	}
	rows := chapLibraryAuthors()
	// Total counts the whole library, before the cap: a client showing fewer
	// than this is showing a truncated row and has to be able to say so.
	total := len(rows)
	bookSortAuthorRows(rows, r.URL.Query().Get("sort"))
	if len(rows) > bookBrowseMaxItems {
		rows = rows[:bookBrowseMaxItems]
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, bookAuthorJSON(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"authors": out, "total": total})
}

// bookAuthorJSON renders one author row. added is OMITTED when the record
// carries no date: a missing date makes no recency claim, so the "added" order
// trails it rather than leading with it as the beginning of time.
func bookAuthorJSON(v chapAuthorView) map[string]any {
	m := map[string]any{
		"foreign_author_id": v.ForeignID,
		"name":              v.Name,
		"image":             v.Image,
		"title_count":       len(v.Titles),
		"available_count":   v.Available,
	}
	if !v.Added.IsZero() {
		m["added"] = v.Added
	}
	return m
}

// bookSortAuthorRows orders the authors row in place. Every order ends in the
// same name tie-break so an unchanged library never reshuffles between fetches.
func bookSortAuthorRows(rows []chapAuthorView, order string) {
	byName := func(i, j int) bool {
		return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name)
	}
	switch strings.ToLower(strings.TrimSpace(order)) {
	case bookSortByName:
		sort.Slice(rows, byName)
	case bookSortByAdded:
		sort.Slice(rows, func(i, j int) bool {
			a, b := rows[i].Added, rows[j].Added
			if a.IsZero() != b.IsZero() {
				return b.IsZero()
			}
			if !a.IsZero() && !a.Equal(b) {
				return a.After(b)
			}
			return byName(i, j)
		})
	default:
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Available != rows[j].Available {
				return rows[i].Available > rows[j].Available
			}
			if len(rows[i].Titles) != len(rows[j].Titles) {
				return len(rows[i].Titles) > len(rows[j].Titles)
			}
			return byName(i, j)
		})
	}
}

// ─── GET /api/requests/book-author ──────────────────────

func bookAuthorHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	foreignID := strings.TrimSpace(r.URL.Query().Get("foreign_id"))
	if foreignID == "" {
		writeErr(w, http.StatusBadRequest, "foreign_id required")
		return
	}
	if ok, status, msg := bookBrowseResolve(u, r); !ok {
		if status == 0 {
			status, msg = http.StatusForbidden, "chaptarr instance is not available to you"
		}
		writeErr(w, status, msg)
		return
	}
	view, found := chapLibraryAuthorByForeignID(foreignID)
	if !found {
		writeErr(w, http.StatusNotFound, "author is not in this book library")
		return
	}
	titles := make([]*DemoBook, len(view.Titles))
	copy(titles, view.Titles)
	bookSortBibliography(titles)
	out := make([]map[string]any, 0, len(titles))
	for _, b := range titles {
		out = append(out, bookLibraryTitleJSON(b))
	}
	writeJSON(w, http.StatusOK, map[string]any{"author": bookAuthorJSON(view), "titles": out})
}

// bookSortBibliography orders an author's titles newest-first. Undated records
// sort last rather than leading the page as year zero.
func bookSortBibliography(titles []*DemoBook) {
	sort.SliceStable(titles, func(i, j int) bool {
		a, b := titles[i], titles[j]
		if (a.Year > 0) != (b.Year > 0) {
			return a.Year > 0
		}
		if a.Year != b.Year {
			return a.Year > b.Year
		}
		return strings.ToLower(a.Title) < strings.ToLower(b.Title)
	})
}

// ─── GET /api/requests/book-series ──────────────────────

func bookSeriesHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	ok, status, msg := bookBrowseResolve(u, r)
	if !ok {
		if status != 0 {
			writeErr(w, status, msg)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"series": []map[string]any{}, "total": 0})
		return
	}
	rows := []chapSeriesView{}
	for _, v := range chapLibrarySeries() {
		// A series the library holds no file of is dropped: adding one author
		// imports their whole bibliography, so a library knows about several
		// times more series than it holds, and listing them all would make a
		// shelf of things you own out of mostly things you do not.
		if v.Available == 0 {
			continue
		}
		rows = append(rows, v)
	}
	total := len(rows)
	bookSortSeriesRows(rows, r.URL.Query().Get("sort"))
	if len(rows) > bookBrowseMaxItems {
		rows = rows[:bookBrowseMaxItems]
	}
	out := make([]map[string]any, 0, len(rows))
	for _, v := range rows {
		out = append(out, bookSeriesJSON(v))
	}
	writeJSON(w, http.StatusOK, map[string]any{"series": out, "total": total})
}

func bookSeriesJSON(v chapSeriesView) map[string]any {
	return map[string]any{
		"name":            v.Name,
		"covers":          bookSeriesCovers(v),
		"title_count":     len(v.Titles),
		"available_count": v.Available,
	}
}

// bookSortSeriesRows orders the series row in place, with the same name
// tie-break the authors row uses.
func bookSortSeriesRows(rows []chapSeriesView, order string) {
	byName := func(i, j int) bool {
		return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name)
	}
	if strings.EqualFold(strings.TrimSpace(order), bookSortByName) {
		sort.Slice(rows, byName)
		return
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Available != rows[j].Available {
			return rows[i].Available > rows[j].Available
		}
		if len(rows[i].Titles) != len(rows[j].Titles) {
			return len(rows[i].Titles) > len(rows[j].Titles)
		}
		return byName(i, j)
	})
}

// bookSeriesCovers picks the covers a series card stacks: the first book the
// library actually holds, then the run in order behind it.
//
// Ownership outranks position, because this row is about a library rather than
// a bibliography — showing book one's art for a book nobody has is a picture of
// something you do not own, and the count beside it already says how much of
// the series is missing. Duplicates are dropped: several records of one title
// share its art, and the same cover stacked three times reads as a rendering
// fault rather than a series. The final tie-break is the title, so a series
// that files several titles at one position picks the same cover every fetch.
func bookSeriesCovers(v chapSeriesView) []string {
	type candidate struct {
		owned bool
		tier  int
		key   float64
		title string
		cover string
	}
	cands := make([]candidate, 0, len(v.Titles))
	for _, b := range v.Titles {
		cover := b.CoverPath()
		if cover == "" {
			continue
		}
		owned := false
		for _, f := range []string{bookFormatEbook, bookFormatAudiobook} {
			if rec := b.Formats[f]; rec != nil && rec.Downloaded {
				owned = true
				break
			}
		}
		tier, key := bookSeriesCoverRank(v.Positions[b.ForeignID])
		cands = append(cands, candidate{
			owned: owned, tier: tier, key: key,
			title: strings.ToLower(strings.TrimSpace(b.Title)), cover: cover,
		})
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].owned != cands[j].owned {
			return cands[i].owned
		}
		if cands[i].tier != cands[j].tier {
			return cands[i].tier < cands[j].tier
		}
		if cands[i].key != cands[j].key {
			return cands[i].key < cands[j].key
		}
		return cands[i].title < cands[j].title
	})
	covers := make([]string, 0, bookSeriesCoverDepth)
	seen := map[string]bool{}
	for _, c := range cands {
		if seen[c.cover] {
			continue
		}
		seen[c.cover] = true
		covers = append(covers, c.cover)
		if len(covers) == bookSeriesCoverDepth {
			break
		}
	}
	return covers
}

// bookSeriesCoverRank places a book in the running for the front of the stack:
// tier 0 is the numbered run (position >= 1), tier 1 the sub-one positions
// (0 for boxed sets and companions, 0.5 for a prequel novella), tier 2 the
// records the series states no position for. Ranking on the raw number alone
// would put the collections — filed at 0 — in front of book one.
func bookSeriesCoverRank(position string) (tier int, key float64) {
	value, ok := bookSeriesPositionKey(position)
	switch {
	case !ok:
		return 2, 0
	case value >= 1:
		return 0, value
	default:
		return 1, value
	}
}

// bookSeriesPositionKey reads the leading number off a position for ordering.
//
// Real libraries carry positions like "2A", "1.5, 1.6, 1.7", "5/6" and
// "3, Part 1 of 2". Their numeric prefix is what places them; the rest is
// display detail, and a position with no number at all sorts last rather than
// claiming position zero.
func bookSeriesPositionKey(position string) (float64, bool) {
	s := strings.TrimSpace(position)
	end, seenDot := 0, false
	for end < len(s) {
		c := s[end]
		if c >= '0' && c <= '9' {
			end++
			continue
		}
		if c == '.' && !seenDot && end+1 < len(s) && s[end+1] >= '0' && s[end+1] <= '9' {
			seenDot = true
			end++
			continue
		}
		break
	}
	if end == 0 {
		return 0, false
	}
	value, err := strconv.ParseFloat(s[:end], 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

// ─── GET /api/requests/book-series-detail ───────────────

func bookSeriesDetailHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		writeErr(w, http.StatusBadRequest, "name required")
		return
	}
	if ok, status, msg := bookBrowseResolve(u, r); !ok {
		if status == 0 {
			status, msg = http.StatusForbidden, "chaptarr instance is not available to you"
		}
		writeErr(w, status, msg)
		return
	}
	view, found := chapLibrarySeriesByName(name)
	if !found {
		writeErr(w, http.StatusNotFound, "series is not in this book library")
		return
	}
	titles := make([]*DemoBook, len(view.Titles))
	copy(titles, view.Titles)
	bookSortSeriesTitles(titles, view.Positions)
	out := make([]map[string]any, 0, len(titles))
	for _, b := range titles {
		// Flattened: position sits alongside the title's own fields, not
		// nested under one of them.
		row := bookLibraryTitleJSON(b)
		row["position"] = view.Positions[b.ForeignID]
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"series": bookSeriesJSON(view), "titles": out})
}

// bookSortSeriesTitles orders a series in reading order: by the numeric prefix
// of each position, then by the raw position so "2A" and "2B" stay in order,
// and finally by title. Titles the series states no position for trail the
// rest rather than claiming the front.
func bookSortSeriesTitles(titles []*DemoBook, positions map[string]string) {
	sort.SliceStable(titles, func(i, j int) bool {
		pi, pj := positions[titles[i].ForeignID], positions[titles[j].ForeignID]
		a, aOK := bookSeriesPositionKey(pi)
		b, bOK := bookSeriesPositionKey(pj)
		if aOK != bOK {
			return aOK
		}
		if aOK && a != b {
			return a < b
		}
		if pi != pj {
			return pi < pj
		}
		return strings.ToLower(titles[i].Title) < strings.ToLower(titles[j].Title)
	})
}
