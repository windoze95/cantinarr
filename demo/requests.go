// requests.go — the user-facing request lifecycle (srv-requests §2,
// app-requests §2–§5): POST /api/requests (movie/tv/book), the user's own
// history, request options, the per-title TMDB status endpoint, and the
// download-progress simulation (gap-plan §4.2/§4.3).
//
// Exports per contract.md §7 (D2): requestStatusForTmdb,
// startDownloadSimulation.
package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

const reqParkedBookMessage = "This book couldn't be matched in the library, so it was saved as a request for an admin instead of being added automatically."

func registerRequests(r chi.Router) {
	r.Post("/requests", reqCreateHandler)
	r.Get("/requests", reqHistoryHandler)
	r.Get("/requests/options", reqOptionsHandler)
	r.Get("/requests/{tmdb_id}/status", reqTmdbStatusHandler)
}

// ─── Effective policy resolution ────────────────────────

// reqPolicy is the effective request policy for one user: global settings →
// per-user overrides (non-nil wins; quality overrides of 0 ignored) → admin
// bypass (never requires approval, may always choose season/quality).
type reqPolicy struct {
	RequireApproval    bool
	AllowSeasonChoice  bool
	AllowQualityChoice bool
	DefaultSeasonScope string
	QualityRadarr      int
	QualitySonarr      int
}

func reqEffectivePolicy(u *DemoUser) reqPolicy {
	reqMu.Lock()
	p := reqPolicy{
		RequireApproval:    reqGlobal.RequireApproval,
		AllowSeasonChoice:  reqGlobal.AllowSeasonChoice,
		AllowQualityChoice: reqGlobal.AllowQualityChoice,
		DefaultSeasonScope: reqGlobal.DefaultSeasonScope,
		QualityRadarr:      reqGlobal.DefaultQualityRadarr,
		QualitySonarr:      reqGlobal.DefaultQualitySonarr,
	}
	if u != nil {
		if ov := reqUserOverrides[u.ID]; ov != nil {
			if ov.AllowSeasonChoice != nil {
				p.AllowSeasonChoice = *ov.AllowSeasonChoice
			}
			if ov.SeasonScope != nil && *ov.SeasonScope != "" {
				p.DefaultSeasonScope = *ov.SeasonScope
			}
			if ov.AllowQualityChoice != nil {
				p.AllowQualityChoice = *ov.AllowQualityChoice
			}
			if ov.QualityProfileRadarr != nil && *ov.QualityProfileRadarr != 0 {
				p.QualityRadarr = *ov.QualityProfileRadarr
			}
			if ov.QualityProfileSonarr != nil && *ov.QualityProfileSonarr != 0 {
				p.QualitySonarr = *ov.QualityProfileSonarr
			}
		}
	}
	reqMu.Unlock()
	if u != nil {
		if u.RequireApproval != nil {
			p.RequireApproval = *u.RequireApproval
		}
		if u.Role == roleAdmin {
			p.RequireApproval = false
			p.AllowSeasonChoice = true
			p.AllowQualityChoice = true
		}
	}
	if !reqValidCoarseScope(p.DefaultSeasonScope) {
		p.DefaultSeasonScope = "all"
	}
	return p
}

// ─── POST /api/requests ─────────────────────────────────

type reqCreateBody struct {
	TmdbID           int    `json:"tmdb_id"`
	MediaType        string `json:"media_type"`
	Title            string `json:"title"`
	TvdbID           int    `json:"tvdb_id"`
	ForeignID        string `json:"foreign_id"`
	BookFormat       string `json:"book_format"`
	SearchTerm       string `json:"search_term"`
	InstanceID       string `json:"instance_id"`
	SeasonScope      string `json:"season_scope"`
	QualityProfileID int    `json:"quality_profile_id"`
	Seasons          []int  `json:"seasons"`
}

func reqCreateHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var body reqCreateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	switch body.MediaType {
	case "":
		writeErr(w, http.StatusBadRequest, "media_type required")
	case mediaTypeBook:
		reqCreateBook(w, u, &body)
	case mediaTypeMusic:
		reqCreateMusic(w, u, &body)
	case mediaTypeMovie, mediaTypeTV:
		if body.TmdbID == 0 {
			writeErr(w, http.StatusBadRequest, "tmdb_id required")
			return
		}
		reqCreateMovieTV(w, u, &body)
	default:
		if body.TmdbID == 0 {
			writeErr(w, http.StatusBadRequest, "tmdb_id required")
			return
		}
		writeErr(w, http.StatusInternalServerError, "unsupported media type: "+body.MediaType)
	}
}

func reqCreateMovieTV(w http.ResponseWriter, u *DemoUser, body *reqCreateBody) {
	mt := body.MediaType
	// Kids accounts: a title outside the account's content limits cannot be
	// requested, and the refusal comes before any library or settings
	// lookup, where the real CreateMediaRequest checks the policy.
	if !cpAllowsTmdb(u, mt, body.TmdbID) {
		writeErr(w, http.StatusNotFound, "that title is not available for this account")
		return
	}
	pol := reqEffectivePolicy(u)

	instanceID, errStatus, errMsg := reqResolveArrInstance(u, mt, body.InstanceID)
	if errMsg != "" {
		writeErr(w, errStatus, errMsg)
		return
	}

	// Canonical title + tvdb bridge from the catalog.
	canonicalTitle := strings.TrimSpace(body.Title)
	tvdbID := body.TvdbID
	if mt == mediaTypeMovie {
		if m, ok := findMovie(body.TmdbID); ok {
			canonicalTitle = m.Title
		}
	} else {
		if s, ok := findShow(body.TmdbID); ok {
			canonicalTitle = s.Name
			if tvdbID == 0 {
				tvdbID = s.TvdbID
			}
		}
	}

	// Season + quality choices are honored only when the policy allows them.
	scope := pol.DefaultSeasonScope
	var seasons []int
	if mt == mediaTypeTV && pol.AllowSeasonChoice {
		if len(body.Seasons) > 0 {
			seasons = body.Seasons
		} else if reqValidCoarseScope(body.SeasonScope) {
			scope = body.SeasonScope
		}
	}
	qualityID := 0
	if pol.AllowQualityChoice {
		qualityID = body.QualityProfileID
	}
	scopeStr := ""
	if mt == mediaTypeTV {
		if len(seasons) > 0 {
			b, _ := json.Marshal(seasons)
			scopeStr = string(b)
		} else {
			scopeStr = scope
		}
	}

	if pol.RequireApproval {
		pendingTitle := strings.TrimSpace(body.Title)
		if pendingTitle == "" {
			pendingTitle = canonicalTitle
		}
		duplicate := false
		pendingCount := 0
		reqMu.Lock()
		for _, row := range reqLog {
			if row.UserID == u.ID && row.TmdbID == body.TmdbID &&
				row.MediaType == mt && row.Status == statusPending {
				duplicate = true
				break
			}
		}
		if !duplicate {
			reqLog = append(reqLog, &reqLogRow{
				ID: reqNextID, UserID: u.ID, TmdbID: body.TmdbID, TvdbID: tvdbID,
				MediaType: mt, Title: pendingTitle, Status: statusPending,
				SeasonScope: scopeStr, QualityProfileID: qualityID,
				// The library this request is waiting FOR. Books always
				// carried it; movies and TV do now too, so the approval queue
				// names which library an approval would act on.
				InstanceID:  instanceID,
				RequestedAt: time.Now(), Waiters: map[int]string{},
			})
			reqNextID++
			pendingCount = reqLockedPendingCount()
		}
		reqMu.Unlock()
		if !duplicate {
			wsToAdmins(evtRequestPending, map[string]any{
				"tmdb_id":       body.TmdbID,
				"media_type":    mt,
				"title":         pendingTitle,
				"pending_count": pendingCount,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "status": statusPending, "title": pendingTitle,
		})
		return
	}

	// Direct execution: honest live status, then kick the simulation.
	if mt == mediaTypeTV && len(seasons) == 0 {
		seasons = reqSeasonsForScope(body.TmdbID, scope)
	}
	status := reqKickTitle(body.TmdbID, mt, instanceID, seasons)
	reqUpsertTitleRow(u.ID, body.TmdbID, tvdbID, mt, canonicalTitle, scopeStr, qualityID, status)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "status": status, "title": canonicalTitle,
		// The library the request was routed to, so the app can show which
		// one answered without guessing.
		"instance_id": instanceID,
	})
}

func reqCreateBook(w http.ResponseWriter, u *DemoUser, body *reqCreateBody) {
	foreignID := strings.TrimSpace(body.ForeignID)
	title := strings.TrimSpace(body.Title)
	searchTerm := strings.TrimSpace(body.SearchTerm)
	if foreignID == "" {
		writeErr(w, http.StatusBadRequest, "foreign_id required for book requests")
		return
	}
	if title == "" {
		writeErr(w, http.StatusBadRequest, "title required for book requests")
		return
	}
	format := body.BookFormat
	if format == "" {
		format = bookFormatBoth
	}
	if format != bookFormatEbook && format != bookFormatAudiobook && format != bookFormatBoth {
		writeErr(w, http.StatusBadRequest, "book_format must be ebook, audiobook, or both")
		return
	}
	inst, errStatus, errMsg := bookResolveInstance(u, strings.TrimSpace(body.InstanceID))
	if inst == nil {
		writeErr(w, errStatus, errMsg)
		return
	}
	requested := bookConcreteFormats(format)
	pol := reqEffectivePolicy(u)
	book, found := bookByForeignID(foreignID)

	if !found {
		// Metadata record can't be found: PARK as pending for an admin. The
		// add already ran and failed, which the queue card says out loud.
		reqBookPark(w, u, nil, foreignID, title, requested, inst.ID, searchTerm,
			reqParkedBookMessage, "", reqAddFailureMetadataUnresolved)
		return
	}
	if pol.RequireApproval {
		reqBookPark(w, u, book, foreignID, book.Title, requested, inst.ID, searchTerm, "", "", "")
		return
	}
	if bookAuthorImportPending(foreignID) {
		// The library's metadata service still owes this book's author an
		// import, so the add is refused. Only an auto-approved create can
		// reach this refusal, and it parks SERVER-OWNED: no admin is paged,
		// the sweep watches it, and the requester reads "requested" plus the
		// wait that says why.
		reqBookPark(w, u, book, foreignID, book.Title, requested, inst.ID, searchTerm,
			reqBookAuthorImportingMessage, reqParkReasonAuthorImport, "")
		return
	}

	// Direct per-format execution with a live idempotency preflight.
	results := map[string]string{}
	missing := 0
	for _, f := range requested {
		rec := book.Formats[f]
		if rec == nil {
			results[f] = statusUnavailable
			missing++
			continue
		}
		if rec.Downloaded {
			results[f] = statusAvailable
			continue
		}
		if bookIsDownloading(foreignID, f) {
			results[f] = statusDownloading
			continue
		}
		chaptarrOnBookRequested(foreignID, f)
		bookStartFormatSimulation(foreignID, f, inst.ID)
		results[f] = statusRequested
	}
	if missing == len(requested) {
		msg := "no ebook edition is available for this book"
		if format == bookFormatAudiobook {
			msg = "no audiobook edition is available for this book"
		}
		writeErr(w, http.StatusInternalServerError, msg)
		return
	}
	collapsed := bookCollapse(results)
	reqUpsertBookRow(u.ID, foreignID, inst.ID, book.Title, format, searchTerm, collapsed)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "status": collapsed, "title": book.Title,
		"book_formats": results,
	})
}

// reqBookPark parks a book request in the approval queue: pending rows dedupe
// per foreign_id + instance across ALL users — overlapping formats subscribe
// the caller as a waiter; only the missing formats insert a new row.
//
// parkReason == reqParkReasonAuthorImport makes the park SERVER-OWNED: the
// insert skips the admin page/badge, and the response narrates the wait —
// pending formats read "requested" with a book_format_waits explanation
// alongside (the real create's author-import lane). addFailure records an add
// that already ran and failed (the metadata_unresolved lane).
func reqBookPark(w http.ResponseWriter, u *DemoUser, book *DemoBook, foreignID, title string, requested []string, instanceID, searchTerm, message, parkReason, addFailure string) {
	requestedSet := map[string]bool{}
	for _, f := range requested {
		requestedSet[f] = true
	}
	var inserted *reqLogRow
	insertedAt := time.Now().UTC().Truncate(time.Second)
	pendingCount := 0
	reqMu.Lock()
	covered := map[string]bool{}
	for _, row := range reqLog {
		if row.Status != statusPending || row.MediaType != mediaTypeBook {
			continue
		}
		if row.ForeignID != foreignID || row.InstanceID != instanceID {
			continue
		}
		overlap := []string{}
		for _, f := range bookConcreteFormats(reqNormalizeBookFormat(row.BookFormat)) {
			covered[f] = true
			if requestedSet[f] {
				overlap = append(overlap, f)
			}
		}
		if len(overlap) > 0 && row.UserID != u.ID {
			if row.Waiters == nil {
				row.Waiters = map[int]string{}
			}
			row.Waiters[u.ID] = bookFormatFromSlice(
				bookMergeFormats(row.Waiters[u.ID], overlap))
		}
	}
	newFormats := []string{}
	for _, f := range requested {
		if !covered[f] {
			newFormats = append(newFormats, f)
		}
	}
	if len(newFormats) > 0 {
		inserted = &reqLogRow{
			ID: reqNextID, UserID: u.ID, ForeignID: foreignID,
			BookFormat: bookFormatFromSlice(newFormats), InstanceID: instanceID,
			MediaType: mediaTypeBook, Title: title, Status: statusPending,
			SearchTerm: searchTerm, ParkReason: parkReason,
			AddFailureReason: addFailure,
			RequestedAt:      insertedAt,
			Waiters:          map[int]string{},
		}
		reqLog = append(reqLog, inserted)
		reqNextID++
		pendingCount = reqLockedPendingCount()
	}
	reqMu.Unlock()
	// A server-owned park is not an admin work item — nothing pages until the
	// watch ends and the row is demoted to a real approval.
	if inserted != nil && parkReason == "" {
		wsToAdmins(evtRequestPending, map[string]any{
			"tmdb_id":       0,
			"media_type":    mediaTypeBook,
			"title":         title,
			"pending_count": pendingCount,
			"foreign_id":    foreignID,
			"book_format":   inserted.BookFormat,
			"instance_id":   instanceID,
		})
	}
	formats := map[string]string{}
	for _, f := range requested {
		live := ""
		if book != nil {
			live = bookLiveFormatStatus(book, foreignID, f)
		}
		if live != "" {
			formats[f] = live
		} else {
			formats[f] = statusPending
		}
	}
	resp := map[string]any{
		"success": true, "title": title,
		"book_formats": formats,
	}
	if parkReason == reqParkReasonAuthorImport {
		// Neither stored word is the truth here: "pending" narrates an
		// approval that is not happening, so the pending formats read
		// "requested" and the wait alongside carries what that leaves out.
		waits := map[string]any{}
		for f, status := range formats {
			if status == statusPending {
				formats[f] = statusRequested
				waits[f] = reqBookWaitJSON(insertedAt)
			}
		}
		if len(waits) > 0 {
			resp["book_format_waits"] = waits
		}
	}
	resp["status"] = bookCollapse(formats)
	if message != "" {
		resp["message"] = message
	}
	writeJSON(w, http.StatusOK, resp)
}

// ─── Execution helpers ──────────────────────────────────

// reqKickTitle checks live availability and (unless already fully available)
// starts the download simulation. Returns the immediate REST status.
func reqKickTitle(tmdbID int, mediaType, instanceID string, seasons []int) string {
	key := reqTitleKey(mediaType, tmdbID)
	reqMu.Lock()
	cur := ""
	if st := reqTitleStates[key]; st != nil {
		cur = st.Status
	}
	reqMu.Unlock()
	if cur == statusAvailable {
		return statusAvailable
	}
	if instanceID == "" {
		instanceID = instRadarr
		if mediaType == mediaTypeTV {
			instanceID = instSonarr
		}
	}
	reqMu.Lock()
	reqTitleInstances[key] = instanceID
	reqMu.Unlock()
	startDownloadSimulation(tmdbID, mediaType, instanceID, seasons)
	return statusRequested
}

// reqResolveArrInstance resolves the library a movie/TV request names. An
// empty instance_id means the caller's effective default. A library the
// caller does not hold is refused rather than quietly swapped for theirs.
func reqResolveArrInstance(u *DemoUser, mediaType, requested string) (instanceID string, errStatus int, errMsg string) {
	serviceType := serviceRadarr
	if mediaType == mediaTypeTV {
		serviceType = serviceSonarr
	}
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return effectiveInstanceIDFor(u, serviceType), 0, ""
	}
	inst := instanceByID(requested)
	if inst == nil || inst.ServiceType != serviceType {
		return "", http.StatusBadRequest, "invalid " + serviceType + " instance"
	}
	if !userCanSeeInstance(u, requested) {
		return "", http.StatusForbidden, serviceType + " instance is not available to you"
	}
	return requested, 0, ""
}

// reqUpsertTitleRow records a movie/TV request in the log: the user's latest
// non-pending row for the title is refreshed, else a new row is appended.
func reqUpsertTitleRow(userID, tmdbID, tvdbID int, mediaType, title, scopeStr string, qualityID int, status string) {
	now := time.Now()
	reqMu.Lock()
	defer reqMu.Unlock()
	for i := len(reqLog) - 1; i >= 0; i-- {
		row := reqLog[i]
		if row.UserID == userID && row.TmdbID == tmdbID &&
			row.MediaType == mediaType && row.Status != statusPending {
			row.Status = status
			row.DenyReason = ""
			row.SeasonScope = scopeStr
			if qualityID != 0 {
				row.QualityProfileID = qualityID
			}
			row.RequestedAt = now
			return
		}
	}
	reqLog = append(reqLog, &reqLogRow{
		ID: reqNextID, UserID: userID, TmdbID: tmdbID, TvdbID: tvdbID,
		MediaType: mediaType, Title: title, Status: status,
		SeasonScope: scopeStr, QualityProfileID: qualityID,
		RequestedAt: now, Waiters: map[int]string{},
	})
	reqNextID++
}

// reqUpsertBookRow records a directly-executed book request.
func reqUpsertBookRow(userID int, foreignID, instanceID, title, format, searchTerm, status string) {
	now := time.Now()
	reqMu.Lock()
	defer reqMu.Unlock()
	for i := len(reqLog) - 1; i >= 0; i-- {
		row := reqLog[i]
		if row.UserID == userID && row.MediaType == mediaTypeBook &&
			row.ForeignID == foreignID && row.InstanceID == instanceID &&
			row.Status != statusPending {
			row.Status = status
			row.DenyReason = ""
			row.BookFormat = format
			if searchTerm != "" {
				row.SearchTerm = searchTerm
			}
			row.RequestedAt = now
			return
		}
	}
	reqLog = append(reqLog, &reqLogRow{
		ID: reqNextID, UserID: userID, ForeignID: foreignID,
		BookFormat: format, InstanceID: instanceID, MediaType: mediaTypeBook,
		Title: title, Status: status, SearchTerm: searchTerm,
		RequestedAt: now, Waiters: map[int]string{},
	})
	reqNextID++
}

// reqUpsertMusicRow records a directly-executed music request (one row per
// user and album, like reqUpsertBookRow without formats).
func reqUpsertMusicRow(userID int, foreignID, instanceID, title, searchTerm, status string) {
	now := time.Now()
	reqMu.Lock()
	defer reqMu.Unlock()
	for i := len(reqLog) - 1; i >= 0; i-- {
		row := reqLog[i]
		if row.UserID == userID && row.MediaType == mediaTypeMusic &&
			row.ForeignID == foreignID && row.InstanceID == instanceID &&
			row.Status != statusPending {
			row.Status = status
			row.DenyReason = ""
			row.AddFailureReason = ""
			if searchTerm != "" {
				row.SearchTerm = searchTerm
			}
			row.RequestedAt = now
			return
		}
	}
	reqLog = append(reqLog, &reqLogRow{
		ID: reqNextID, UserID: userID, ForeignID: foreignID,
		InstanceID: instanceID, MediaType: mediaTypeMusic,
		Title: title, Status: status, SearchTerm: searchTerm,
		RequestedAt: now, Waiters: map[int]string{},
	})
	reqNextID++
}

// reqSeasonsForScope resolves a coarse scope to concrete season numbers
// (real season_numbers from the catalog; never season 0).
func reqSeasonsForScope(tmdbID int, scope string) []int {
	show, ok := findShow(tmdbID)
	if !ok || len(show.Seasons) == 0 {
		return nil
	}
	nums := make([]int, 0, len(show.Seasons))
	for _, se := range show.Seasons {
		nums = append(nums, se.SeasonNumber)
	}
	switch scope {
	case "first", "pilot":
		return nums[:1]
	case "latest":
		return nums[len(nums)-1:]
	}
	return nums
}

// ─── Contract hooks (contract.md §7, D2) ────────────────

// requestStatusForTmdb returns the live availability of a movie/TV title in
// REST spellings, progress 0..1. Other domains (the arr fakes) derive
// hasFile/episode stats from it.
func requestStatusForTmdb(tmdbID int, mediaType string) (status string, progress float64) {
	reqMu.Lock()
	defer reqMu.Unlock()
	st := reqTitleStates[reqTitleKey(mediaType, tmdbID)]
	if st == nil || st.Status == "" {
		return statusUnavailable, 0
	}
	return st.Status, st.Progress
}

// startDownloadSimulation runs the movie/TV download ticker (gap-plan §4.2):
// ~10 s "requested" → 20 × 1.5 s download_progress broadcasts (progress a
// 0..1 fraction, status "downloading") → terminal request_status_changed
// ("available", or "partially_available" WS-spelling for incomplete TV),
// calling arrOnRequestStarted/arrOnRequestCompleted in lockstep. A nil/empty
// seasons list means every season.
func startDownloadSimulation(tmdbID int, mediaType string, instanceID string, seasons []int) {
	if mediaType != mediaTypeMovie && mediaType != mediaTypeTV {
		return
	}
	if instanceID == "" {
		if mediaType == mediaTypeTV {
			instanceID = instSonarr
		} else {
			instanceID = instRadarr
		}
	}
	key := reqTitleKey(mediaType, tmdbID)
	reqMu.Lock()
	if reqActiveSims[key] {
		reqMu.Unlock()
		return
	}
	reqActiveSims[key] = true
	reqMu.Unlock()
	go reqRunDownloadSim(key, tmdbID, mediaType, instanceID, seasons)
}

func reqRunDownloadSim(key string, tmdbID int, mediaType, instanceID string, seasons []int) {
	serviceType := serviceRadarr
	if mediaType == mediaTypeTV {
		serviceType = serviceSonarr
		if len(seasons) == 0 {
			seasons = reqSeasonsForScope(tmdbID, "all")
		}
	}

	// Phase 1 — requested.
	reqMu.Lock()
	st := reqTitleStates[key]
	if st == nil {
		st = &reqTitleState{}
		reqTitleStates[key] = st
	}
	if st.SeasonFiles == nil {
		st.SeasonFiles = map[int]int{}
	}
	if st.MonitoredSeasons == nil {
		st.MonitoredSeasons = map[int]bool{}
	}
	if st.Status == "" || st.Status == statusUnavailable {
		st.Status = statusRequested
		st.Progress = 0
	}
	for _, n := range seasons {
		st.MonitoredSeasons[n] = true
	}
	reqMu.Unlock()

	arrOnRequestStarted(tmdbID, mediaType)
	wsBroadcast(evtArrQueueChanged, map[string]any{
		"instance_id": instanceID, "service_type": serviceType,
	})
	time.Sleep(10 * time.Second)

	// Phase 2 — downloading: 20 steps, 1.5 s apart, progress 0..1 fraction.
	reqMu.Lock()
	st.Status = statusDownloading
	st.Progress = 0
	reqMu.Unlock()
	for i := 1; i <= 20; i++ {
		time.Sleep(1500 * time.Millisecond)
		p := float64(i) / 20.0
		reqMu.Lock()
		st.Progress = p
		reqMu.Unlock()
		wsBroadcast(evtDownloadProgress, map[string]any{
			"tmdb_id":     tmdbID,
			"media_type":  mediaType,
			"progress":    p,
			"status":      statusDownloading,
			"instance_id": instanceID,
		})
		if i%5 == 0 {
			wsBroadcast(evtArrQueueChanged, map[string]any{
				"instance_id": instanceID, "service_type": serviceType,
			})
		}
	}

	// Phase 3 — terminal.
	restStatus, wsStatus, progress := statusAvailable, statusAvailable, 1.0
	if mediaType == mediaTypeTV {
		if show, ok := findShow(tmdbID); ok {
			reqMu.Lock()
			for _, n := range seasons {
				for _, se := range show.Seasons {
					if se.SeasonNumber == n {
						st.SeasonFiles[n] = se.EpisodeCount
					}
				}
			}
			total, files := 0, 0
			for _, se := range show.Seasons {
				total += se.EpisodeCount
				files += st.SeasonFiles[se.SeasonNumber]
			}
			reqMu.Unlock()
			if total > 0 && files < total {
				restStatus = statusPartial
				wsStatus = wsStatusPartiallyAvailable
				progress = float64(files) / float64(total)
			}
		}
	}
	reqMu.Lock()
	st.Status = restStatus
	st.Progress = progress
	for _, row := range reqLog {
		if row.TmdbID == tmdbID && row.MediaType == mediaType &&
			(row.Status == statusRequested || row.Status == statusDownloading) {
			row.Status = restStatus
		}
	}
	delete(reqActiveSims, key)
	reqMu.Unlock()

	arrOnRequestCompleted(tmdbID, mediaType, seasons)
	wsBroadcast(evtRequestStatusChanged, map[string]any{
		"tmdb_id":     tmdbID,
		"media_type":  mediaType,
		"status":      wsStatus,
		"instance_id": instanceID,
	})
	wsBroadcast(evtArrQueueChanged, map[string]any{
		"instance_id": instanceID, "service_type": serviceType,
	})
}

// ─── GET /api/requests ──────────────────────────────────

func reqHistoryHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	type histEntry struct {
		row          reqLogRow
		formatSlice  string // the format slice this user sees (waiter rows)
		isWaiterView bool
	}
	entries := []histEntry{}
	reqMu.Lock()
	for _, row := range reqLog {
		if row.UserID == u.ID {
			entries = append(entries, histEntry{row: *row, formatSlice: row.BookFormat})
			continue
		}
		if row.Status == statusPending && row.MediaType == mediaTypeBook {
			if slice, ok := row.Waiters[u.ID]; ok {
				entries = append(entries, histEntry{row: *row, formatSlice: slice, isWaiterView: true})
			}
		}
	}
	reqMu.Unlock()
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].row.RequestedAt.Equal(entries[j].row.RequestedAt) {
			return entries[i].row.ID > entries[j].row.ID
		}
		return entries[i].row.RequestedAt.After(entries[j].row.RequestedAt)
	})
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		row := e.row
		// Kids accounts: a movie or show outside the account's limits (a
		// request made before the limits were set, say) is left out of its
		// own history. Book and music rows carry no rating and stay.
		if (row.MediaType == mediaTypeMovie || row.MediaType == mediaTypeTV) &&
			!cpAllowsTmdb(u, row.MediaType, row.TmdbID) {
			continue
		}
		status := reqOverlayRowStatus(&row, e.formatSlice)
		bookFormat := reqNormalizeBookFormat(row.BookFormat)
		if row.MediaType == mediaTypeBook && e.formatSlice != "" {
			bookFormat = reqNormalizeBookFormat(e.formatSlice)
		}
		m := map[string]any{
			"tmdb_id":      row.TmdbID,
			"media_type":   row.MediaType,
			"title":        row.Title,
			"status":       status,
			"status_known": true,
			"requested_at": row.RequestedAt,
		}
		// book_format rides only on book rows; the app reads it as "" elsewhere.
		if row.MediaType == mediaTypeBook {
			m["book_format"] = bookFormat
		}
		// A server-owned author-import park reads as requested — same mapping
		// as the detail status endpoint — and carries the same wait, so the
		// history says why instead of showing a request that looks finished.
		if status == statusPending && row.ParkReason == reqParkReasonAuthorImport {
			m["status"] = statusRequested
			m["book_format_wait"] = reqBookWaitJSON(row.RequestedAt)
		}
		if row.ForeignID != "" {
			m["foreign_id"] = row.ForeignID
		}
		if row.InstanceID != "" {
			m["instance_id"] = row.InstanceID
		}
		if status == statusDenied && row.DenyReason != "" {
			m["deny_reason"] = row.DenyReason
		}
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, out)
}

// reqOverlayRowStatus applies the live-status overlay to one history row:
// pending as-is; movies/tv from the live title state; books collapsed over
// the row's format slice; denied preserved unless live truth is
// non-unavailable.
func reqOverlayRowStatus(row *reqLogRow, formatSlice string) string {
	if row.Status == statusPending {
		return statusPending
	}
	if row.MediaType == mediaTypeBook {
		slice := formatSlice
		if slice == "" {
			slice = row.BookFormat
		}
		book, found := bookByForeignID(row.ForeignID)
		statuses := map[string]string{}
		for _, f := range bookConcreteFormats(reqNormalizeBookFormat(slice)) {
			live := ""
			if found {
				live = bookLiveFormatStatus(book, row.ForeignID, f)
			}
			switch {
			case live != "":
				statuses[f] = live
			case row.Status == statusDenied:
				statuses[f] = statusDenied
			default:
				statuses[f] = statusUnavailable
			}
		}
		return bookCollapse(statuses)
	}
	if row.MediaType == mediaTypeMusic {
		return musicRowLiveStatus(row)
	}
	live, _ := requestStatusForTmdb(row.TmdbID, row.MediaType)
	// History rows never show "downloading" (the digest has no queue view).
	if live == statusDownloading {
		live = statusRequested
	}
	if row.Status == statusDenied {
		if live != statusUnavailable {
			return live
		}
		return statusDenied
	}
	return live
}

// ─── GET /api/requests/options ──────────────────────────

func reqOptionsHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	mediaType := r.URL.Query().Get("media_type")
	if mediaType == "" {
		mediaType = mediaTypeMovie
	}
	// Quality profiles are per-library, so a named instance scopes them. A
	// library the caller does not hold is refused rather than answered with
	// their default library's profiles under someone else's name.
	if requested := strings.TrimSpace(r.URL.Query().Get("instance_id")); requested != "" && mediaType != mediaTypeBook && mediaType != mediaTypeMusic {
		if _, errStatus, errMsg := reqResolveArrInstance(u, mediaType, requested); errMsg != "" {
			writeErr(w, errStatus, errMsg)
			return
		}
	}
	pol := reqEffectivePolicy(u)
	canSeason := pol.AllowSeasonChoice && mediaType == mediaTypeTV
	canQuality := pol.AllowQualityChoice && mediaType != mediaTypeBook && mediaType != mediaTypeMusic
	profiles := []reqQualityProfile{}
	if canQuality {
		if mediaType == mediaTypeTV {
			profiles = append(profiles, reqSonarrProfiles...)
		} else {
			profiles = append(profiles, reqRadarrProfiles...)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"can_choose_season":    canSeason,
		"can_choose_quality":   canQuality,
		"default_season_scope": pol.DefaultSeasonScope,
		"quality_profiles":     profiles,
	})
}

// ─── GET /api/requests/{tmdb_id}/status ─────────────────

func reqTmdbStatusHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	tmdbID, err := strconv.Atoi(chi.URLParam(r, "tmdb_id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid tmdb_id")
		return
	}
	mediaType := r.URL.Query().Get("media_type")
	if mediaType == "" {
		mediaType = mediaTypeMovie
	}
	if mediaType != mediaTypeMovie && mediaType != mediaTypeTV {
		writeJSON(w, http.StatusOK, map[string]any{"status": statusUnavailable, "progress": 0})
		return
	}

	// The user's latest own row decides the pending/denied overlays.
	var latest *reqLogRow
	var stCopy *reqTitleState
	reqMu.Lock()
	for _, row := range reqLog {
		if row.UserID != u.ID || row.TmdbID != tmdbID || row.MediaType != mediaType {
			continue
		}
		if latest == nil || row.RequestedAt.After(latest.RequestedAt) ||
			(row.RequestedAt.Equal(latest.RequestedAt) && row.ID > latest.ID) {
			latest = row
		}
	}
	if st := reqTitleStates[reqTitleKey(mediaType, tmdbID)]; st != nil && st.Status != "" {
		cp := reqTitleState{
			Status: st.Status, Progress: st.Progress,
			SeasonFiles:      map[int]int{},
			MonitoredSeasons: map[int]bool{},
		}
		for k, v := range st.SeasonFiles {
			cp.SeasonFiles[k] = v
		}
		for k, v := range st.MonitoredSeasons {
			cp.MonitoredSeasons[k] = v
		}
		stCopy = &cp
	}
	rowStatus := ""
	if latest != nil {
		rowStatus = latest.Status
	}
	reqMu.Unlock()

	liveNonUnavailable := stCopy != nil && stCopy.Status != statusUnavailable
	resp := map[string]any{"status": statusUnavailable, "progress": 0}
	switch {
	case rowStatus == statusPending:
		resp["status"] = statusPending
	case rowStatus == statusDenied && !liveNonUnavailable:
		resp["status"] = statusDenied
	case stCopy != nil:
		resp["status"] = stCopy.Status
		resp["progress"] = stCopy.Progress
	}
	if mediaType == mediaTypeTV && stCopy != nil {
		if show, ok := findShow(tmdbID); ok {
			resp["seasons"] = reqSeasonRows(show, stCopy)
		}
	}

	// The requested library, when the caller named one. A library they do not
	// hold is a 403, not a silent fall back to their default — answering with
	// someone else's library would be a quiet lie about what they can see.
	serviceType := serviceRadarr
	if mediaType == mediaTypeTV {
		serviceType = serviceSonarr
	}
	requested := r.URL.Query().Get("instance_id")
	if requested != "" {
		inst := instanceByID(requested)
		if inst == nil || inst.ServiceType != serviceType {
			writeErr(w, http.StatusBadRequest, "invalid "+serviceType+" instance")
			return
		}
		if !userCanSeeInstance(u, requested) {
			writeErr(w, http.StatusForbidden, serviceType+" instance is not available to you")
			return
		}
	}

	// A title a kids account may not see has no state to report: it reads
	// unavailable, the same as a title the library does not hold, and
	// carries nothing else (no seasons, releases, or sibling libraries).
	if !cpAllowsTmdb(u, mediaType, tmdbID) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status": statusUnavailable, "progress": 0, "status_known": true,
		})
		return
	}

	// Release dates let a title that reads "Requested" say it is simply not
	// out yet rather than looking like a stalled download. Movies only, and
	// only for one already in the library — an unadded title has no arr
	// record to read dates off.
	if mediaType == mediaTypeMovie {
		if releases := reqMovieReleases(tmdbID); releases != nil {
			resp["releases"] = releases
		}
	}

	// Sibling libraries, present only when the user actually holds more than
	// one of this type. Digest grade: the headline status stays the selected
	// library's full live read, these are the chips beside it.
	visible := visibleInstanceIDs(u, serviceType)
	if len(visible) > 1 {
		defaultID := effectiveInstanceIDFor(u, serviceType)
		statuses := map[string]any{}
		for _, id := range visible {
			statuses[id] = map[string]any{
				"status": reqInstanceStatus(id, defaultID, tmdbID, mediaType, resp["status"]),
			}
		}
		resp["instance_statuses"] = statuses
	}
	writeJSON(w, http.StatusOK, resp)
}

// reqInstanceStatus is one library's digest-grade answer for a title. The
// demo tracks availability per title rather than per library, so the library
// the title was actually routed to reports the live status and every sibling
// reports what it really holds: nothing.
func reqInstanceStatus(instanceID, defaultID string, tmdbID int, mediaType string, headline any) string {
	reqMu.Lock()
	routed := reqTitleInstances[reqTitleKey(mediaType, tmdbID)]
	reqMu.Unlock()
	if routed == "" {
		// Never routed anywhere. THIS user's effective default is the library
		// that would take it, so it carries the headline and the siblings
		// hold nothing — anchoring on the global default instead would credit
		// the wrong library for anyone holding a pin.
		if instanceID == defaultID {
			if s, ok := headline.(string); ok {
				return s
			}
		}
		return statusUnavailable
	}
	if routed != instanceID {
		return statusUnavailable
	}
	if s, ok := headline.(string); ok {
		return s
	}
	return statusUnavailable
}

// reqMovieReleases reports a movie's theatrical and digital dates as plain
// YYYY-MM-DD calendar dates — deliberately not timestamps, because a release
// date has no time of day and serialising one as an instant invites a client
// to localise it and land a day early. Nil when the library knows neither,
// so the key drops out entirely.
func reqMovieReleases(tmdbID int) map[string]string {
	inCinemas, digital := arrMovieReleaseDates(tmdbID)
	if inCinemas == "" && digital == "" {
		return nil
	}
	out := map[string]string{}
	if inCinemas != "" {
		out["in_cinemas"] = inCinemas
	}
	if digital != "" {
		out["digital"] = digital
	}
	return out
}

// reqSeasonRows builds the TV seasons array: real season_numbers (no season
// 0), statuses limited to available|partial|requested|unavailable.
func reqSeasonRows(show *DemoShow, st *reqTitleState) []map[string]any {
	out := make([]map[string]any, 0, len(show.Seasons))
	for _, se := range show.Seasons {
		files := st.SeasonFiles[se.SeasonNumber]
		if st.Status == statusAvailable {
			files = se.EpisodeCount
		}
		status := statusUnavailable
		progress := 0.0
		switch {
		case se.EpisodeCount > 0 && files >= se.EpisodeCount:
			status = statusAvailable
			progress = 1
		case files > 0:
			status = statusPartial
			progress = float64(files) / float64(se.EpisodeCount)
		case st.MonitoredSeasons[se.SeasonNumber] || st.Status == statusAvailable:
			status = statusRequested
		}
		out = append(out, map[string]any{
			"season_number":      se.SeasonNumber,
			"episode_file_count": files,
			"episode_count":      se.EpisodeCount,
			"status":             status,
			"progress":           progress,
		})
	}
	return out
}
