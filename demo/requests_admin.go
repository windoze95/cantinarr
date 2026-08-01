// requests_admin.go — the admin request surfaces (srv-requests §3,
// app-requests §10–§11): the pending approval queue, approve/deny (gap-plan
// §4.3), global request settings, and per-user request-settings overrides.
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

func registerRequestsAdmin(r chi.Router) {
	r.With(requireAdmin).Get("/admin/requests", reqAdminQueueHandler)
	r.With(requireAdmin).Post("/admin/requests/{id}/approve", reqAdminApproveHandler)
	r.With(requireAdmin).Post("/admin/requests/{id}/deny", reqAdminDenyHandler)
	r.With(requireAdmin).Get("/admin/request-settings", reqAdminSettingsGetHandler)
	r.With(requireAdmin).Put("/admin/request-settings", reqAdminSettingsPutHandler)
	r.With(requireAdmin).Get("/admin/users/{userID}/request-settings", reqAdminUserSettingsGetHandler)
	r.With(requireAdmin).Put("/admin/users/{userID}/request-settings", reqAdminUserSettingsPutHandler)
}

// ─── GET /api/admin/requests ────────────────────────────

func reqAdminQueueHandler(w http.ResponseWriter, _ *http.Request) {
	type queueEntry struct {
		row         reqLogRow
		waiterCount int
	}
	entries := []queueEntry{}
	reqMu.Lock()
	for _, row := range reqLog {
		if row.Status != statusPending {
			continue
		}
		entries = append(entries, queueEntry{row: *row, waiterCount: len(row.Waiters)})
	}
	reqMu.Unlock()
	// Oldest first.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].row.RequestedAt.Equal(entries[j].row.RequestedAt) {
			return entries[i].row.ID < entries[j].row.ID
		}
		return entries[i].row.RequestedAt.Before(entries[j].row.RequestedAt)
	})
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		row := e.row
		username := ""
		if u := userByID(row.UserID); u != nil {
			username = u.Username
		}
		posterPath := ""
		tvdbID := row.TvdbID
		switch row.MediaType {
		case mediaTypeMovie:
			if m, ok := findMovie(row.TmdbID); ok {
				posterPath = m.PosterPath
			}
		case mediaTypeTV:
			if s, ok := findShow(row.TmdbID); ok {
				posterPath = s.PosterPath
				if tvdbID == 0 {
					tvdbID = s.TvdbID
				}
			}
		}
		m := map[string]any{
			"id":                 row.ID,
			"user_id":            row.UserID,
			"username":           username,
			"tmdb_id":            row.TmdbID,
			"tvdb_id":            tvdbID,
			"media_type":         row.MediaType,
			"title":              row.Title,
			"book_format":        reqNormalizeBookFormat(row.BookFormat),
			"requester_count":    1 + e.waiterCount,
			"season_scope":       row.SeasonScope,
			"quality_profile_id": row.QualityProfileID,
			"requested_at":       row.RequestedAt,
		}
		if row.ForeignID != "" {
			m["foreign_id"] = row.ForeignID
		}
		if posterPath != "" {
			m["poster_path"] = posterPath
		}
		if row.InstanceID != "" {
			m["instance_id"] = row.InstanceID
			if inst := instanceByID(row.InstanceID); inst != nil {
				m["instance_name"] = inst.Name
			}
		}
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, out)
}

// ─── POST /api/admin/requests/{id}/approve ──────────────

// reqDecisionOverride is the optional approve body (empty body tolerated).
type reqDecisionOverride struct {
	SeasonScope      string `json:"season_scope"`
	QualityProfileID int    `json:"quality_profile_id"`
	BookFormat       string `json:"book_format"`
}

// reqDecodeOptionalBody decodes an optional JSON body; an empty body yields
// the zero value. Reports false (after writing 400) on malformed JSON.
func reqDecodeOptionalBody(w http.ResponseWriter, r *http.Request, v any) bool {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return true
	}
	if err := json.Unmarshal(data, v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

func reqAdminApproveHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request id")
		return
	}
	var override reqDecisionOverride
	if !reqDecodeOptionalBody(w, r, &override) {
		return
	}
	var target *reqLogRow
	var snapshot reqLogRow
	reqMu.Lock()
	for _, row := range reqLog {
		if row.ID == id {
			target = row
			break
		}
	}
	if target != nil {
		snapshot = *target
	}
	reqMu.Unlock()
	if target == nil {
		writeErr(w, http.StatusBadRequest, "request not found")
		return
	}
	if snapshot.Status != statusPending {
		writeErr(w, http.StatusBadRequest, "request is not pending")
		return
	}
	if snapshot.MediaType == mediaTypeBook {
		if override.BookFormat != "" &&
			override.BookFormat != reqNormalizeBookFormat(snapshot.BookFormat) {
			writeErr(w, http.StatusBadRequest, "book format cannot be changed during approval")
			return
		}
		reqAdminApproveBook(w, target, &snapshot)
		return
	}
	reqAdminApproveTitle(w, target, &snapshot, &override)
}

func reqAdminApproveTitle(w http.ResponseWriter, target *reqLogRow, snapshot *reqLogRow, override *reqDecisionOverride) {
	// Resolve the seasons to execute: an override coarse scope replaces any
	// stored explicit list; otherwise the stored scope/list applies.
	scope := ""
	var explicit []int
	if override.SeasonScope != "" {
		if !reqValidCoarseScope(override.SeasonScope) {
			writeErr(w, http.StatusBadRequest, "invalid season scope: "+override.SeasonScope)
			return
		}
		scope = override.SeasonScope
	} else if strings.HasPrefix(snapshot.SeasonScope, "[") {
		_ = json.Unmarshal([]byte(snapshot.SeasonScope), &explicit)
	} else if reqValidCoarseScope(snapshot.SeasonScope) {
		scope = snapshot.SeasonScope
	} else {
		scope = "all"
	}
	var seasons []int
	if snapshot.MediaType == mediaTypeTV {
		if len(explicit) > 0 {
			seasons = explicit
		} else {
			seasons = reqSeasonsForScope(snapshot.TmdbID, scope)
		}
	}
	status := reqKickTitle(snapshot.TmdbID, snapshot.MediaType, seasons)
	reqMu.Lock()
	target.Status = status
	if override.QualityProfileID != 0 {
		target.QualityProfileID = override.QualityProfileID
	}
	if override.SeasonScope != "" && snapshot.MediaType == mediaTypeTV {
		target.SeasonScope = override.SeasonScope
	}
	reqMu.Unlock()
	wsToUser(snapshot.UserID, evtRequestDecision, map[string]any{
		"decision":   "approved",
		"tmdb_id":    snapshot.TmdbID,
		"media_type": snapshot.MediaType,
		"title":      snapshot.Title,
		"status":     status,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "status": status, "title": snapshot.Title,
	})
}

func reqAdminApproveBook(w http.ResponseWriter, target *reqLogRow, snapshot *reqLogRow) {
	if snapshot.InstanceID == "" {
		writeErr(w, http.StatusBadRequest, "pending book request has no pinned Chaptarr instance")
		return
	}
	book, found := bookByForeignID(snapshot.ForeignID)
	if !found {
		// Leaves the row pending for retry (or deny + re-request).
		writeErr(w, http.StatusBadRequest, "book not found in the library")
		return
	}
	formats := bookConcreteFormats(reqNormalizeBookFormat(snapshot.BookFormat))
	results := map[string]string{}
	executed := 0
	for _, f := range formats {
		rec := book.Formats[f]
		if rec == nil {
			results[f] = statusUnavailable
			continue
		}
		executed++
		if rec.Downloaded {
			results[f] = statusAvailable
			continue
		}
		if bookIsDownloading(snapshot.ForeignID, f) {
			results[f] = statusDownloading
			continue
		}
		chaptarrOnBookRequested(snapshot.ForeignID, f)
		bookStartFormatSimulation(snapshot.ForeignID, f, snapshot.InstanceID)
		results[f] = statusRequested
	}
	if executed == 0 {
		msg := "no ebook edition is available for this book"
		if reqNormalizeBookFormat(snapshot.BookFormat) == bookFormatAudiobook {
			msg = "no audiobook edition is available for this book"
		}
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	collapsed := bookCollapse(results)
	var waiters map[int]string
	reqMu.Lock()
	target.Status = collapsed
	target.Title = book.Title
	waiters = map[int]string{}
	for uid, slice := range target.Waiters {
		waiters[uid] = slice
	}
	reqMu.Unlock()

	// Notify the owner and every subscribed waiter with their own slice.
	notify := map[int]string{snapshot.UserID: reqNormalizeBookFormat(snapshot.BookFormat)}
	for uid, slice := range waiters {
		notify[uid] = reqNormalizeBookFormat(slice)
	}
	for uid, slice := range notify {
		sub := map[string]string{}
		for _, f := range bookConcreteFormats(slice) {
			if s, ok := results[f]; ok {
				sub[f] = s
			}
		}
		if len(sub) == 0 {
			continue
		}
		wsToUser(uid, evtRequestDecision, map[string]any{
			"decision":     "approved",
			"tmdb_id":      0,
			"media_type":   mediaTypeBook,
			"title":        book.Title,
			"status":       bookCollapse(sub),
			"foreign_id":   snapshot.ForeignID,
			"book_format":  slice,
			"book_formats": sub,
			"instance_id":  snapshot.InstanceID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "status": collapsed, "title": book.Title,
		"book_formats": results,
	})
}

// ─── POST /api/admin/requests/{id}/deny ─────────────────

func reqAdminDenyHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request id")
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if !reqDecodeOptionalBody(w, r, &body) {
		return
	}
	var target *reqLogRow
	var snapshot reqLogRow
	waiters := map[int]string{}
	reqMu.Lock()
	for _, row := range reqLog {
		if row.ID == id {
			target = row
			break
		}
	}
	if target == nil {
		reqMu.Unlock()
		writeErr(w, http.StatusBadRequest, "request not found")
		return
	}
	if target.Status == statusDenied {
		// Concurrent double-decide is a silent success.
		reqMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
		return
	}
	if target.Status != statusPending {
		reqMu.Unlock()
		writeErr(w, http.StatusBadRequest, "request is not pending")
		return
	}
	target.Status = statusDenied
	target.DenyReason = body.Reason
	for uid, slice := range target.Waiters {
		waiters[uid] = slice
	}
	target.Waiters = map[int]string{}
	// Shared book requests materialize a personal denied row per waiter.
	for uid, slice := range waiters {
		reqLog = append(reqLog, &reqLogRow{
			ID: reqNextID, UserID: uid, ForeignID: target.ForeignID,
			BookFormat: reqNormalizeBookFormat(slice), InstanceID: target.InstanceID,
			MediaType: mediaTypeBook, Title: target.Title, Status: statusDenied,
			DenyReason: body.Reason, RequestedAt: target.RequestedAt,
			Waiters: map[int]string{},
		})
		reqNextID++
	}
	snapshot = *target
	reqMu.Unlock()

	recipients := map[int]string{snapshot.UserID: reqNormalizeBookFormat(snapshot.BookFormat)}
	for uid, slice := range waiters {
		recipients[uid] = reqNormalizeBookFormat(slice)
	}
	for uid, slice := range recipients {
		data := map[string]any{
			"decision":   "denied",
			"tmdb_id":    snapshot.TmdbID,
			"media_type": snapshot.MediaType,
			"title":      snapshot.Title,
			"reason":     body.Reason,
			"status":     statusDenied,
		}
		if snapshot.MediaType == mediaTypeBook {
			data["foreign_id"] = snapshot.ForeignID
			data["book_format"] = slice
			data["instance_id"] = snapshot.InstanceID
		}
		wsToUser(uid, evtRequestDecision, data)
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// ─── GET/PUT /api/admin/request-settings ────────────────

func reqAdminSettingsView() map[string]any {
	reqMu.Lock()
	settings := reqGlobal
	reqMu.Unlock()
	return map[string]any{
		"settings":        settings,
		"radarr_profiles": reqRadarrProfiles,
		"sonarr_profiles": reqSonarrProfiles,
	}
}

func reqAdminSettingsGetHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, reqAdminSettingsView())
}

func reqAdminSettingsPutHandler(w http.ResponseWriter, r *http.Request) {
	var body reqGlobalSettingsT
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !reqValidCoarseScope(body.DefaultSeasonScope) {
		body.DefaultSeasonScope = "all"
	}
	reqMu.Lock()
	reqGlobal = body
	reqMu.Unlock()
	writeJSON(w, http.StatusOK, reqAdminSettingsView())
}

// ─── GET/PUT /api/admin/users/{userID}/request-settings ─

// reqUserSettingsDTO is the six-field per-user override document. Every field
// is nullable and ALWAYS serialized; null means "inherit the global default".
type reqUserSettingsDTO struct {
	RequireApproval      *bool   `json:"require_approval"`
	AllowSeasonChoice    *bool   `json:"allow_season_choice"`
	SeasonScope          *string `json:"season_scope"`
	AllowQualityChoice   *bool   `json:"allow_quality_choice"`
	QualityProfileRadarr *int    `json:"quality_profile_radarr"`
	QualityProfileSonarr *int    `json:"quality_profile_sonarr"`
}

func reqAdminUserSettingsGetHandler(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.Atoi(chi.URLParam(r, "userID"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid user id")
		return
	}
	var dto reqUserSettingsDTO
	if u := userByID(userID); u != nil && u.RequireApproval != nil {
		v := *u.RequireApproval
		dto.RequireApproval = &v
	}
	reqMu.Lock()
	if ov := reqUserOverrides[userID]; ov != nil {
		if ov.AllowSeasonChoice != nil {
			v := *ov.AllowSeasonChoice
			dto.AllowSeasonChoice = &v
		}
		if ov.SeasonScope != nil {
			v := *ov.SeasonScope
			dto.SeasonScope = &v
		}
		if ov.AllowQualityChoice != nil {
			v := *ov.AllowQualityChoice
			dto.AllowQualityChoice = &v
		}
		if ov.QualityProfileRadarr != nil {
			v := *ov.QualityProfileRadarr
			dto.QualityProfileRadarr = &v
		}
		if ov.QualityProfileSonarr != nil {
			v := *ov.QualityProfileSonarr
			dto.QualityProfileSonarr = &v
		}
	}
	reqMu.Unlock()
	writeJSON(w, http.StatusOK, dto)
}

func reqAdminUserSettingsPutHandler(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.Atoi(chi.URLParam(r, "userID"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid user id")
		return
	}
	var dto reqUserSettingsDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if dto.SeasonScope != nil && *dto.SeasonScope != "" && !reqValidCoarseScope(*dto.SeasonScope) {
		writeErr(w, http.StatusBadRequest, "invalid season scope: "+*dto.SeasonScope)
		return
	}
	// require_approval lives on the shared DemoUser; explicit null CLEARS it.
	ok := withUser(userID, func(u *DemoUser) {
		if dto.RequireApproval == nil {
			u.RequireApproval = nil
		} else {
			v := *dto.RequireApproval
			u.RequireApproval = &v
		}
	})
	if !ok {
		writeErr(w, http.StatusBadRequest, "user not found")
		return
	}
	reqMu.Lock()
	if dto.AllowSeasonChoice == nil && dto.SeasonScope == nil &&
		dto.AllowQualityChoice == nil && dto.QualityProfileRadarr == nil &&
		dto.QualityProfileSonarr == nil {
		delete(reqUserOverrides, userID)
	} else {
		ov := &reqUserOverride{}
		if dto.AllowSeasonChoice != nil {
			v := *dto.AllowSeasonChoice
			ov.AllowSeasonChoice = &v
		}
		if dto.SeasonScope != nil {
			v := *dto.SeasonScope
			ov.SeasonScope = &v
		}
		if dto.AllowQualityChoice != nil {
			v := *dto.AllowQualityChoice
			ov.AllowQualityChoice = &v
		}
		if dto.QualityProfileRadarr != nil {
			v := *dto.QualityProfileRadarr
			ov.QualityProfileRadarr = &v
		}
		if dto.QualityProfileSonarr != nil {
			v := *dto.QualityProfileSonarr
			ov.QualityProfileSonarr = &v
		}
		reqUserOverrides[userID] = ov
	}
	reqMu.Unlock()
	// Echo the submitted DTO back verbatim.
	writeJSON(w, http.StatusOK, dto)
}
