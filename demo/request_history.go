package main

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
)

// Request history is saved request intent, independent of current library
// availability. The demo keeps one failed native tag receipt so an admin can
// exercise the retry control without contacting an arr provider.
var reqTagMu sync.Mutex
var reqTagStates = map[int64]string{2: "applied", 3: "failed"}

func reqHistoryDecision(row reqLogRow) string {
	if row.Status == statusDenied {
		if row.DenyReason == "Cancelled" {
			return "cancelled"
		}
		return "denied"
	}
	if row.Status == statusPending && row.ParkReason == "" {
		return "pending"
	}
	return "approved"
}

func reqHistoryTag(row reqLogRow) map[string]any {
	reqTagMu.Lock()
	state := reqTagStates[row.ID]
	reqTagMu.Unlock()
	if state == "" {
		return nil
	}
	username := ""
	if user := userByID(row.UserID); user != nil {
		username = user.Username
	}
	label := "cantinarr-" + strconv.Itoa(row.UserID) + "-" + strings.ToLower(username)
	status := map[string]any{"status": state,
		"can_retry": (state == "failed" || state == "retrying") && reqTagEligible(row),
		"tag_label": label}
	switch state {
	case "applied":
		status["message"] = "Applied in the simulated library."
		status["applied_at"] = row.RequestedAt.Add(time.Minute)
	case "failed":
		status["message"] = "The simulated library rejected the tag."
	case "pending":
		status["message"] = "Tag retry queued in the demo."
	}
	return status
}

func reqTagEligible(row reqLogRow) bool {
	if row.Status == statusDenied || row.Status != statusAvailable || userByID(row.UserID) == nil {
		return false
	}
	inst := instanceByID(row.InstanceID)
	if inst == nil || !inst.TagRequests {
		return false
	}
	switch row.MediaType {
	case mediaTypeMovie:
		return inst.ServiceType == serviceRadarr
	case mediaTypeTV:
		return inst.ServiceType == serviceSonarr
	case mediaTypeBook:
		return inst.ServiceType == serviceChaptarr
	case mediaTypeMusic:
		return inst.ServiceType == serviceLidarr
	}
	return false
}

func reqAdminHistoryHandler(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	mediaType, decision := r.URL.Query().Get("media_type"), r.URL.Query().Get("decision")
	valid := utf8.RuneCountInString(q) <= 200
	switch mediaType {
	case "", mediaTypeMovie, mediaTypeTV, mediaTypeBook, mediaTypeMusic:
	default:
		valid = false
	}
	switch decision {
	case "", "pending", "approved", "denied", "cancelled", "unknown":
	default:
		valid = false
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			valid = false
		}
	}
	userID, before := 0, int64(0)
	if raw := r.URL.Query().Get("user_id"); raw != "" {
		var err error
		userID, err = strconv.Atoi(raw)
		if err != nil || userID <= 0 {
			valid = false
		}
	}
	if raw := r.URL.Query().Get("before"); raw != "" {
		var err error
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before <= 0 {
			valid = false
		}
	}
	if !valid {
		writeErr(w, http.StatusBadRequest, "invalid history filter")
		return
	}

	rows := []reqLogRow{}
	reqMu.Lock()
	for _, row := range reqLog {
		cp := *row
		cp.Waiters = map[int]string{}
		for uid, format := range row.Waiters {
			cp.Waiters[uid] = format
		}
		rows = append(rows, cp)
	}
	reqMu.Unlock()
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID > rows[j].ID })

	requesterSet := map[int]bool{}
	for _, row := range rows {
		requesterSet[row.UserID] = true
		for uid := range row.Waiters {
			requesterSet[uid] = true
		}
	}
	requesters := []map[string]any{}
	for uid := range requesterSet {
		if user := userByID(uid); user != nil {
			requesters = append(requesters, map[string]any{"user_id": uid, "username": user.Username})
		}
	}
	sort.Slice(requesters, func(i, j int) bool {
		return strings.ToLower(requesters[i]["username"].(string)) < strings.ToLower(requesters[j]["username"].(string))
	})
	items := []map[string]any{}
	for _, row := range rows {
		if before > 0 && row.ID >= before || mediaType != "" && row.MediaType != mediaType ||
			decision != "" && reqHistoryDecision(row) != decision ||
			q != "" && !strings.Contains(strings.ToLower(row.Title), strings.ToLower(q)) {
			continue
		}
		if userID > 0 && row.UserID != userID {
			if _, ok := row.Waiters[userID]; !ok {
				continue
			}
		}
		ownerName := ""
		if owner := userByID(row.UserID); owner != nil {
			ownerName = owner.Username
		}
		owner := map[string]any{"user_id": row.UserID, "username": ownerName}
		if row.MediaType == mediaTypeBook {
			owner["book_format"] = reqNormalizeBookFormat(row.BookFormat)
		}
		users := []map[string]any{owner}
		for uid, format := range row.Waiters {
			name := ""
			if user := userByID(uid); user != nil {
				name = user.Username
			}
			users = append(users, map[string]any{"user_id": uid, "username": name, "book_format": format})
		}
		instName := ""
		if inst := instanceByID(row.InstanceID); inst != nil {
			instName = inst.Name
		}
		item := map[string]any{
			"id": row.ID, "tmdb_id": row.TmdbID, "foreign_id": row.ForeignID,
			"media_type": row.MediaType, "title": row.Title,
			"instance_id": row.InstanceID, "instance_name": instName,
			"season_scope": row.SeasonScope, "book_format": row.BookFormat,
			"decision": reqHistoryDecision(row), "deny_reason": row.DenyReason,
			"requested_at": row.RequestedAt, "requesters": users,
		}
		switch row.MediaType {
		case mediaTypeMovie:
			if movie, ok := findMovie(row.TmdbID); ok && movie.PosterPath != "" {
				item["poster_path"] = movie.PosterPath
			}
		case mediaTypeTV:
			if show, ok := findShow(row.TmdbID); ok && show.PosterPath != "" {
				item["poster_path"] = show.PosterPath
			}
		}
		if row.MediaType == mediaTypeBook {
			item["catalog_provider"] = "chaptarr"
		}
		if tag := reqHistoryTag(row); tag != nil {
			item["requester_tagging"] = tag
		}
		items = append(items, item)
		if len(items) > limit {
			break
		}
	}
	page := map[string]any{"requests": items, "requesters": requesters}
	if len(items) > limit {
		items = items[:limit]
		page["requests"] = items
		page["next_before"] = items[len(items)-1]["id"]
	}
	writeJSON(w, http.StatusOK, page)
}

func reqTagRetryHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid request ID")
		return
	}
	var row *reqLogRow
	reqMu.Lock()
	for _, candidate := range reqLog {
		if candidate.ID == id {
			cp := *candidate
			row = &cp
			break
		}
	}
	reqMu.Unlock()
	if row == nil || !reqTagEligible(*row) {
		writeErr(w, http.StatusConflict, "this request has no eligible requester tag to retry; refresh History")
		return
	}
	reqTagMu.Lock()
	if reqTagStates[id] != "failed" && reqTagStates[id] != "retrying" {
		reqTagMu.Unlock()
		writeErr(w, http.StatusConflict, "this request has no eligible requester tag to retry; refresh History")
		return
	}
	reqTagStates[id] = "pending"
	reqTagMu.Unlock()
	writeJSON(w, http.StatusAccepted, reqHistoryTag(*row))
}
