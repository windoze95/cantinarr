// music.go — the Cantinarr-native music routes (GET /api/requests/music-status,
// /music-library, /music-recent, /music-artists, /music-artist) and the music
// request lifecycle shared with requests.go / requests_admin.go: create
// (approval park vs execute, the metadata-unresolved park), the three-phase
// download simulation, approve, and the history overlay.
//
// Album data comes from the D9 hooks in data_music.go (albumByForeignID,
// lidCanonicalForeignID, lidarrAlbumLiveStatus, lidarrOnAlbum*); request rows
// live in the request domain's reqLog under reqMu. Music emits NO
// download_progress and NO request_status_changed — production never does
// (music has no TMDB id); every transition is visible through music-status
// polling plus arr_queue_changed pings, exactly like the real poller.
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

// musicParkedMessage is the requester copy behind a music request Lidarr could
// not match (verbatim from the real server).
const musicParkedMessage = "This album couldn't be matched in the library, so it was saved as a request for an admin instead of being added automatically."

// musicBrowseMaxItems caps the artists row, applied after the requested sort
// (capping first would silently omit everyone below the cut).
const musicBrowseMaxItems = 200

// The orders the artists row can be read in. An unknown value falls back to
// the default rather than erroring.
const (
	musicSortByAlbums = "albums"
	musicSortByName   = "name"
	musicSortByAdded  = "added"
)

var (
	musicMu sync.Mutex
	// musicSimActive dedupes per-album download simulations, keyed by the
	// canonical foreignAlbumId.
	musicSimActive = map[string]bool{}
)

func registerMusic(r chi.Router) {
	r.Get("/requests/music-status", musicStatusHandler)
	r.Get("/requests/music-library", musicLibraryHandler)
	r.Get("/requests/music-recent", musicRecentHandler)
	r.Get("/requests/music-artists", musicArtistsHandler)
	r.Get("/requests/music-artist", musicArtistHandler)
}

// ─── Instance resolution ────────────────────────────────

// musicResolveInstance resolves the Lidarr instance a music call targets.
// Returns (nil, httpStatus, message) on failure with the real server's
// mapping: 400 invalid instance / 403 forbidden instance / 500 unconfigured.
// An explicit id is checked against the caller's visible set (grants are
// additive); an omitted id resolves the effective (granted) instance, with
// the first Lidarr as the admin fallback — Lidarr has no global default.
func musicResolveInstance(u *DemoUser, explicitID string) (*DemoInstance, int, string) {
	if explicitID != "" {
		inst := instanceByID(explicitID)
		if inst == nil || inst.ServiceType != serviceLidarr {
			return nil, http.StatusBadRequest, "invalid lidarr instance"
		}
		if u == nil || (u.Role != roleAdmin && !userCanSeeInstance(u, explicitID)) {
			return nil, http.StatusForbidden, "lidarr instance is not available to you"
		}
		return inst, 0, ""
	}
	inst := effectiveInstanceFor(u, serviceLidarr)
	if inst == nil && u != nil && u.Role == roleAdmin {
		for _, cand := range allInstances() {
			if cand.ServiceType == serviceLidarr {
				inst = cand
				break
			}
		}
	}
	if inst == nil {
		return nil, http.StatusInternalServerError, "lidarr is not configured for you"
	}
	return inst, 0, ""
}

// musicBrowseResolve resolves the caller's Lidarr access for a digest call.
//
// ok=false with a zero status means the caller simply has no Lidarr grant.
// The digests answer that with an empty payload — the row is absent for them,
// which is not a failure — while the artist page answers 403: they asked
// about a library they cannot see at all, and a 404 there would claim this
// library was searched and came up empty.
func musicBrowseResolve(u *DemoUser, r *http.Request) (ok bool, status int, msg string) {
	instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if instanceID != "" {
		if _, errStatus, errMsg := musicResolveInstance(u, instanceID); errStatus != 0 {
			return false, errStatus, errMsg
		}
		return true, 0, ""
	}
	if u.Role != roleAdmin && effectiveInstanceFor(u, serviceLidarr) == nil {
		return false, 0, ""
	}
	return true, 0, ""
}

// ─── Status projection ──────────────────────────────────

// musicStatusFor is the shared projection behind music-status, the history
// overlay, and the approval preflight: the alias resolves to the canonical
// record, the live library truth outranks the log, pending/denied survive
// while the library holds no claim, anything else heals to unavailable. The
// second result is the canonical id, set only when the queried id is an
// alias of a record the library holds.
func musicStatusFor(u *DemoUser, foreignID, instanceID string) (status, canonical string) {
	foreignID = strings.TrimSpace(foreignID)
	resolved, aliased := lidCanonicalForeignID(foreignID)
	live := ""
	// Live truth needs a library the caller can see.
	if inst, errStatus, _ := musicResolveInstance(u, instanceID); errStatus == 0 && inst != nil {
		live = lidarrAlbumLiveStatus(resolved)
		if aliased && lidAlbumInLibrary(resolved) {
			canonical = resolved
		}
	}
	logged := musicLoggedStatus(u, foreignID, resolved, instanceID)
	switch {
	case live != "":
		return live, canonical
	case logged == statusPending || logged == statusDenied:
		return logged, canonical
	}
	return statusUnavailable, canonical
}

// musicLoggedStatus is the newest request_log answer for this album: the
// caller's own rows plus ANYONE's pending rows (the real query is
// user_id = ? OR status = 'pending'), narrowed to the named instance when the
// caller selected one.
func musicLoggedStatus(u *DemoUser, foreignID, canonical, instanceID string) string {
	var latest *reqLogRow
	reqMu.Lock()
	defer reqMu.Unlock()
	for _, row := range reqLog {
		if row.MediaType != mediaTypeMusic {
			continue
		}
		if row.ForeignID != foreignID && row.ForeignID != canonical {
			continue
		}
		if instanceID != "" && row.InstanceID != "" && row.InstanceID != instanceID {
			continue
		}
		if row.UserID != u.ID && row.Status != statusPending {
			continue
		}
		if latest == nil || row.RequestedAt.After(latest.RequestedAt) ||
			(row.RequestedAt.Equal(latest.RequestedAt) && row.ID > latest.ID) {
			latest = row
		}
	}
	if latest == nil {
		return ""
	}
	return latest.Status
}

// ─── GET /api/requests/music-status ─────────────────────

func musicStatusHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	foreignID := strings.TrimSpace(r.URL.Query().Get("foreign_id"))
	if foreignID == "" {
		writeErr(w, http.StatusBadRequest, "foreign_id required")
		return
	}
	instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if instanceID != "" {
		if _, errStatus, errMsg := musicResolveInstance(u, instanceID); errStatus != 0 {
			writeErr(w, errStatus, errMsg)
			return
		}
	}
	status, canonical := musicStatusFor(u, foreignID, instanceID)
	// Only the seven REST status words ever leave here: an unknown word reads
	// as "unknown" in the app and hides the Request button.
	resp := map[string]any{"status": status, "progress": 0}
	if canonical != "" {
		resp["canonical_foreign_id"] = canonical
	}
	writeJSON(w, http.StatusOK, resp)
}

// ─── Digest renderers ───────────────────────────────────

// musicTitleJSON renders one MusicLibraryTitle — the per-album ownership row
// music-library and music-artist both emit. year is an int so it marshals as
// a JSON integer (the app parses it as int; a float throws).
func musicTitleJSON(t lidTitleView) map[string]any {
	return map[string]any{
		"title":            t.Title,
		"artist":           t.Artist,
		"year":             t.Year,
		"foreign_album_id": t.ForeignID,
		"cover":            t.Cover,
		"monitored":        t.Monitored,
		"downloaded":       t.Downloaded,
	}
}

// musicArtistJSON renders one LibraryArtist row. added is OMITTED when the
// record carries no date: a missing date makes no recency claim, so the
// "added" order trails it rather than leading with it as the beginning of
// time.
func musicArtistJSON(v lidArtistView) map[string]any {
	m := map[string]any{
		"foreign_artist_id": v.ForeignID,
		"name":              v.Name,
		"image":             v.Image,
		"album_count":       v.AlbumCount,
		"available_count":   v.AvailableCount,
	}
	if !v.Added.IsZero() {
		m["added"] = v.Added
	}
	return m
}

// ─── GET /api/requests/music-library ────────────────────

func musicLibraryHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	ok, status, msg := musicBrowseResolve(u, r)
	if !ok {
		if status != 0 {
			writeErr(w, status, msg)
			return
		}
		// No Lidarr access: an empty digest, NOT an error.
		writeJSON(w, http.StatusOK, map[string]any{"titles": []map[string]any{}})
		return
	}
	titles := []map[string]any{}
	// One row per library record — including an unmonitored record with
	// nothing on disk, exactly as the real reduction includes every Lidarr
	// record.
	for _, t := range lidLibraryTitles() {
		titles = append(titles, musicTitleJSON(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"titles": titles})
}

// ─── GET /api/requests/music-recent ─────────────────────

func musicRecentHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	ok, status, msg := musicBrowseResolve(u, r)
	if !ok {
		if status != 0 {
			writeErr(w, status, msg)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": []map[string]any{}})
		return
	}
	// Default 20, hard cap 50; a non-positive or unparseable value falls back
	// to the default.
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 50 {
		limit = 50
	}
	items := lidRecentTitles()
	if len(items) > limit {
		items = items[:limit]
	}
	out := make([]map[string]any, 0, len(items))
	for _, t := range items {
		out = append(out, map[string]any{
			"album_id":         t.AlbumID,
			"foreign_album_id": t.ForeignID,
			"title":            t.Title,
			"artist":           t.Artist,
			"cover":            t.Cover,
			"imported_at":      t.NewestFile,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// ─── GET /api/requests/music-artists ────────────────────

func musicArtistsHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	ok, status, msg := musicBrowseResolve(u, r)
	if !ok {
		if status != 0 {
			writeErr(w, status, msg)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"artists": []map[string]any{}, "total": 0})
		return
	}
	rows := lidLibraryArtists()
	// Total counts the whole library, before the cap: a client showing fewer
	// than this is showing a truncated row and has to be able to say so.
	total := len(rows)
	musicSortArtistRows(rows, r.URL.Query().Get("sort"))
	if len(rows) > musicBrowseMaxItems {
		rows = rows[:musicBrowseMaxItems]
	}
	out := make([]map[string]any, 0, len(rows))
	for _, v := range rows {
		out = append(out, musicArtistJSON(v))
	}
	writeJSON(w, http.StatusOK, map[string]any{"artists": out, "total": total})
}

// musicSortArtistRows orders the artists row in place (mirrors the server's
// sortLibraryArtists). Every order ends in the same name tie-break so an
// unchanged library never reshuffles between fetches.
func musicSortArtistRows(rows []lidArtistView, order string) {
	byName := func(i, j int) bool {
		return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name)
	}
	switch strings.ToLower(strings.TrimSpace(order)) {
	case musicSortByName:
		sort.SliceStable(rows, byName)
	case musicSortByAdded:
		sort.SliceStable(rows, func(i, j int) bool {
			a, b := rows[i].Added, rows[j].Added
			// A record with no date makes no recency claim, so it trails the
			// ones that do rather than leading as the beginning of time.
			if a.IsZero() != b.IsZero() {
				return b.IsZero()
			}
			if !a.IsZero() && !a.Equal(b) {
				return a.After(b)
			}
			return byName(i, j)
		})
	default:
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].AvailableCount != rows[j].AvailableCount {
				return rows[i].AvailableCount > rows[j].AvailableCount
			}
			if rows[i].AlbumCount != rows[j].AlbumCount {
				return rows[i].AlbumCount > rows[j].AlbumCount
			}
			return byName(i, j)
		})
	}
}

// ─── GET /api/requests/music-artist ─────────────────────

func musicArtistHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	foreignID := strings.TrimSpace(r.URL.Query().Get("foreign_id"))
	if foreignID == "" {
		writeErr(w, http.StatusBadRequest, "foreign_id required")
		return
	}
	if ok, status, msg := musicBrowseResolve(u, r); !ok {
		if status == 0 {
			// No Lidarr access is not "artist missing": saying not found would
			// claim this library was searched.
			status, msg = http.StatusForbidden, "lidarr instance is not available to you"
		}
		writeErr(w, status, msg)
		return
	}
	view, found := lidLibraryArtistByForeignID(foreignID)
	if !found {
		writeErr(w, http.StatusNotFound, "artist is not in this music library")
		return
	}
	titles := make([]lidTitleView, len(view.Titles))
	copy(titles, view.Titles)
	// Newest first; undated records sort last rather than leading as year
	// zero.
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
	out := make([]map[string]any, 0, len(titles))
	for _, t := range titles {
		out = append(out, musicTitleJSON(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"artist": musicArtistJSON(view), "titles": out})
}

// ─── POST /api/requests (media_type "music") ────────────

// reqCreateMusic handles a music request: validation with the real server's
// strings, the approval park (or the live truth when the library already
// answers), the metadata-unresolved park for an id Lidarr cannot match, and
// direct execution — which monitors an existing unmonitored record in place
// and never creates a second one.
func reqCreateMusic(w http.ResponseWriter, u *DemoUser, body *reqCreateBody) {
	foreignID := strings.TrimSpace(body.ForeignID)
	title := strings.TrimSpace(body.Title)
	searchTerm := strings.TrimSpace(body.SearchTerm)
	if foreignID == "" {
		writeErr(w, http.StatusBadRequest, "foreign_id required for music requests")
		return
	}
	if title == "" {
		writeErr(w, http.StatusBadRequest, "title required for music requests")
		return
	}
	if body.BookFormat != "" {
		writeErr(w, http.StatusBadRequest, "music requests carry no book_format")
		return
	}
	inst, errStatus, errMsg := musicResolveInstance(u, strings.TrimSpace(body.InstanceID))
	if inst == nil {
		writeErr(w, errStatus, errMsg)
		return
	}
	pol := reqEffectivePolicy(u)
	album, found := albumByForeignID(foreignID)
	canonical, aliased := lidCanonicalForeignID(foreignID)

	if !found {
		// No metadata record to add from: the add already ran and failed,
		// which the queue card says out loud. Parks whether or not approval
		// is required (there is no author-import analogue for music — a
		// Lidarr add is synchronous and fails loudly).
		musicPark(w, u, foreignID, title, inst.ID, searchTerm, reqAddFailureMetadataUnresolved, musicParkedMessage)
		return
	}
	if pol.RequireApproval {
		// The request boundary is the idempotency boundary: when the library
		// already answers, the create returns the live truth instead of
		// queueing a no-op.
		if live := lidarrAlbumLiveStatus(canonical); live != "" {
			resp := map[string]any{"success": true, "status": live, "title": album.Title, "instance_id": inst.ID}
			if aliased && lidAlbumInLibrary(canonical) {
				resp["canonical_foreign_id"] = canonical
			}
			writeJSON(w, http.StatusOK, resp)
			return
		}
		musicPark(w, u, foreignID, album.Title, inst.ID, searchTerm, "", "")
		return
	}

	status := musicExecute(canonical, inst.ID)
	reqUpsertMusicRow(u.ID, foreignID, inst.ID, album.Title, searchTerm, status)
	resp := map[string]any{"success": true, "status": status, "title": album.Title, "instance_id": inst.ID}
	if aliased && lidAlbumInLibrary(canonical) {
		resp["canonical_foreign_id"] = canonical
	}
	writeJSON(w, http.StatusOK, resp)
}

// musicPark parks a music request in the approval queue. Music pending rows
// are per user (no waiters): the same user re-requesting the same album on the
// same instance is a duplicate. addFailure records an add that already ran
// and failed (the metadata_unresolved lane); message rides the response.
func musicPark(w http.ResponseWriter, u *DemoUser, foreignID, title, instanceID, searchTerm, addFailure, message string) {
	duplicate := false
	pendingCount := 0
	reqMu.Lock()
	for _, row := range reqLog {
		if row.UserID == u.ID && row.MediaType == mediaTypeMusic &&
			row.ForeignID == foreignID && row.InstanceID == instanceID && row.Status == statusPending {
			duplicate = true
			break
		}
	}
	if !duplicate {
		reqLog = append(reqLog, &reqLogRow{
			ID: reqNextID, UserID: u.ID, ForeignID: foreignID, InstanceID: instanceID,
			MediaType: mediaTypeMusic, Title: title, Status: statusPending,
			SearchTerm: searchTerm, AddFailureReason: addFailure,
			RequestedAt: time.Now().UTC().Truncate(time.Second),
			Waiters:     map[int]string{},
		})
		reqNextID++
		pendingCount = reqLockedPendingCount()
	}
	reqMu.Unlock()
	if !duplicate {
		wsToAdmins(evtRequestPending, map[string]any{
			"tmdb_id":       0,
			"media_type":    mediaTypeMusic,
			"title":         title,
			"pending_count": pendingCount,
			"foreign_id":    foreignID,
			"instance_id":   instanceID,
		})
	}
	resp := map[string]any{"success": true, "status": statusPending, "title": title, "instance_id": instanceID}
	if message != "" {
		resp["message"] = message
	}
	writeJSON(w, http.StatusOK, resp)
}

// musicExecute is the auto-approved add path over the live library: a
// complete album is already available, a downloading or partial one reports
// that, a monitored record is already requested (the real search is a
// best-effort accelerator), and anything else — not in the library, or an
// existing unmonitored record — is monitored in place and simulated to
// completion. Returns the immediate REST status.
func musicExecute(canonical, instanceID string) string {
	switch live := lidarrAlbumLiveStatus(canonical); live {
	case statusAvailable, statusDownloading, statusPartial, statusRequested:
		return live
	}
	if !lidarrOnAlbumRequested(canonical) {
		return statusUnavailable
	}
	musicStartSimulation(canonical, instanceID)
	return statusRequested
}

// musicStartSimulation runs one album's download machine: requested (~10 s)
// → downloading (~21 s, the queue item's sizeleft shrinking) → available
// (files land, history records the import, the digests flip). Every phase
// pings arr_queue_changed and nothing else: the app's library-changed
// listeners refresh the Music tab, the album page, and the artist page on
// that ping, and music-status polling sees each state. The caller has already
// invoked lidarrOnAlbumRequested.
func musicStartSimulation(canonical, instanceID string) {
	musicMu.Lock()
	if musicSimActive[canonical] {
		musicMu.Unlock()
		return
	}
	musicSimActive[canonical] = true
	musicMu.Unlock()
	go func() {
		// Phase 1 — requested (the monitor already pinged).
		time.Sleep(10 * time.Second)
		// Phase 2 — downloading.
		lidarrOnAlbumDownloading(canonical)
		time.Sleep(7 * time.Second)
		lidarrQueueAdvance(canonical, 0.43)
		time.Sleep(7 * time.Second)
		lidarrQueueAdvance(canonical, 0.12)
		time.Sleep(7 * time.Second)
		// Phase 3 — available: the files land, the digests flip.
		lidarrOnAlbumAvailable(canonical)
		musicMu.Lock()
		delete(musicSimActive, canonical)
		musicMu.Unlock()
		musicRefreshLogRows(canonical)
	}()
}

// musicRefreshLogRows recomputes stored non-pending/non-denied music rows for
// an album from the live projection (so history rows flip to available).
func musicRefreshLogRows(canonical string) {
	live := lidarrAlbumLiveStatus(canonical)
	if live == "" {
		return
	}
	reqMu.Lock()
	defer reqMu.Unlock()
	for _, row := range reqLog {
		if row.MediaType != mediaTypeMusic || row.Status == statusPending || row.Status == statusDenied {
			continue
		}
		rowCanonical, _ := lidCanonicalForeignID(row.ForeignID)
		if rowCanonical != canonical {
			continue
		}
		row.Status = live
	}
}

// ─── POST /api/admin/requests/{id}/approve (music) ──────

// reqAdminApproveMusic executes a pending music request: the auto-approved
// path over the library the request named. An id Lidarr still cannot match
// answers 400 and leaves the row pending (deny, or re-request once the album
// is in the library).
func reqAdminApproveMusic(w http.ResponseWriter, target *reqLogRow, snapshot *reqLogRow) {
	if snapshot.InstanceID == "" {
		writeErr(w, http.StatusBadRequest, "pending music request has no pinned Lidarr instance")
		return
	}
	album, found := albumByForeignID(snapshot.ForeignID)
	if !found {
		writeErr(w, http.StatusBadRequest, "album not found for foreign id "+snapshot.ForeignID)
		return
	}
	canonical, _ := lidCanonicalForeignID(snapshot.ForeignID)
	status := musicExecute(canonical, snapshot.InstanceID)
	reqMu.Lock()
	target.Status = status
	target.Title = album.Title
	target.AddFailureReason = ""
	reqMu.Unlock()
	wsToUser(snapshot.UserID, evtRequestDecision, map[string]any{
		"decision":    "approved",
		"tmdb_id":     0,
		"media_type":  mediaTypeMusic,
		"title":       album.Title,
		"status":      status,
		"foreign_id":  snapshot.ForeignID,
		"instance_id": snapshot.InstanceID,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "status": status, "title": album.Title,
		"instance_id": snapshot.InstanceID,
	})
}

// ─── History overlay ────────────────────────────────────

// musicRowLiveStatus is the history overlay for one music request row: the
// library's live truth for the album, else the row's denied, else
// unavailable. Unlike movie rows, music rows DO show downloading — the real
// overlay is the same per-instance projection the status endpoint uses.
func musicRowLiveStatus(row *reqLogRow) string {
	canonical, _ := lidCanonicalForeignID(row.ForeignID)
	if live := lidarrAlbumLiveStatus(canonical); live != "" {
		return live
	}
	if row.Status == statusDenied {
		return statusDenied
	}
	return statusUnavailable
}
