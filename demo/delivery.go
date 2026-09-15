// delivery.go — saved request intent and its delivery states. Intent is
// independent of library availability: a saved book or album keeps its
// receipt even while Chaptarr or Lidarr is stalled, so a reader is never told
// their request vanished because the arr went quiet.
//
// A delivery state is one of `queued`, `working`, `retry`, `attention`,
// `complete`, `cancelled`. The two the app offers buttons for are `retry`
// and `attention`; `cancel` is offered while anything is still open.
//
// Prefix: dlv…
package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

var (
	dlvMu sync.Mutex

	// dlvCancelled records request ids a requester withdrew this session, so
	// the saved receipt reports `cancelled` instead of reappearing.
	dlvCancelled = map[int64]bool{}
)

func registerDelivery(r chi.Router) {
	r.Get("/requests/delivery-status", dlvStatusHandler)
	r.Post("/requests/{id}/delivery", dlvActionHandler)
	r.Get("/requests/music-saved", dlvSavedMusicHandler)
}

// ─── Views ──────────────────────────────────────────────

// dlvState is the delivery state one saved row is in, derived from the
// request's own status. Nothing here consults the arr: that is the point.
func dlvState(row *reqLogRow) string {
	if dlvCancelled[row.ID] {
		return "cancelled"
	}
	switch row.Status {
	case statusDenied:
		return "cancelled"
	case statusAvailable:
		return "complete"
	case statusPending:
		if row.AddFailureReason != "" {
			return "attention"
		}
		if row.ParkReason != "" {
			return "working"
		}
		return "queued"
	case statusDownloading, statusPartial:
		return "working"
	}
	return "queued"
}

// dlvMessage explains the state in the requester's vocabulary — never arr
// jargon, and never silence where an explanation exists.
func dlvMessage(row *reqLogRow, state string) string {
	switch state {
	case "cancelled":
		if row.Status == statusDenied && row.DenyReason != "" {
			return row.DenyReason
		}
		return "This request was withdrawn."
	case "complete":
		return "Delivered to your library."
	case "attention":
		return "This needs an admin to look at it before it can continue."
	case "working":
		if row.ParkReason != "" {
			return "Still working on this one; the server is retrying on its own."
		}
		return "Downloading now."
	}
	return "Waiting to be picked up."
}

// dlvStateView is one DeliveryState. `can_manage` gates the retry affordance
// and `can_cancel` the withdraw one, so a finished row offers neither.
func dlvStateView(row *reqLogRow) map[string]any {
	state := dlvState(row)
	open := state != "complete" && state != "cancelled"
	out := map[string]any{
		"request_id": row.ID,
		"state":      state,
		"attempts":   1,
		"message":    dlvMessage(row, state),
		"can_manage": state == "retry" || state == "attention",
		"can_cancel": open,
	}
	if row.MediaType == mediaTypeBook {
		out["format"] = reqNormalizeBookFormat(row.BookFormat)
	}
	if !row.RequestedAt.IsZero() {
		out["last_attempt_at"] = row.RequestedAt.UTC().Format(time.RFC3339)
	}
	if row.AddFailureReason != "" {
		out["code"] = row.AddFailureReason
	}
	return out
}

// dlvResponse is the CreateResponse shape every delivery surface answers in.
func dlvResponse(rows []*reqLogRow, title, instanceID string) map[string]any {
	delivery := []map[string]any{}
	status := statusUnavailable
	var requestID int64
	for _, row := range rows {
		delivery = append(delivery, dlvStateView(row))
		if requestID == 0 {
			requestID = row.ID
			status = row.Status
			if title == "" {
				title = row.Title
			}
			if instanceID == "" {
				instanceID = row.InstanceID
			}
		}
		if dlvCancelled[row.ID] {
			status = statusUnavailable
		}
	}
	out := map[string]any{
		"success":  len(rows) > 0,
		"status":   status,
		"title":    title,
		"delivery": delivery,
	}
	if requestID != 0 {
		out["request_id"] = requestID
	}
	if instanceID != "" {
		out["instance_id"] = instanceID
	}
	return out
}

// ─── Reads ──────────────────────────────────────────────

// dlvRowsFor collects one user's saved rows for a title. A copy is taken
// under reqMu so nothing here holds the request lock while rendering.
func dlvRowsFor(userID int, mediaType, foreignID string, tmdbID int, instanceID string) []*reqLogRow {
	reqMu.Lock()
	defer reqMu.Unlock()
	out := []*reqLogRow{}
	for i := len(reqLog) - 1; i >= 0; i-- {
		row := reqLog[i]
		if row.UserID != userID || row.MediaType != mediaType {
			continue
		}
		if foreignID != "" && row.ForeignID != foreignID {
			continue
		}
		if tmdbID != 0 && row.TmdbID != tmdbID {
			continue
		}
		if instanceID != "" && row.InstanceID != "" && row.InstanceID != instanceID {
			continue
		}
		copied := *row
		out = append(out, &copied)
	}
	return out
}

func dlvRowByID(id int64) *reqLogRow {
	reqMu.Lock()
	defer reqMu.Unlock()
	for _, row := range reqLog {
		if row.ID == id {
			copied := *row
			return &copied
		}
	}
	return nil
}

// dlvStatusHandler answers "what did I save for this, and where is it?" —
// intent only. include_live is accepted and ignored: the demo never has a
// live provider read to skip.
func dlvStatusHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	q := r.URL.Query()

	if raw := q.Get("request_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id < 1 {
			writeErr(w, http.StatusBadRequest, "invalid request id")
			return
		}
		row := dlvRowByID(id)
		if row == nil || row.UserID != u.ID {
			writeErr(w, http.StatusForbidden, "that request is not yours")
			return
		}
		dlvMu.Lock()
		defer dlvMu.Unlock()
		writeJSON(w, http.StatusOK, dlvResponse([]*reqLogRow{row}, "", ""))
		return
	}

	mediaType := q.Get("media_type")
	switch mediaType {
	case mediaTypeBook, mediaTypeMusic, mediaTypeMovie, mediaTypeTV:
	default:
		writeErr(w, http.StatusBadRequest, "unknown media type")
		return
	}
	instanceID := strings.TrimSpace(q.Get("instance_id"))
	foreignID := strings.TrimSpace(q.Get("foreign_id"))
	tmdbID := 0
	if mediaType == mediaTypeMovie || mediaType == mediaTypeTV {
		tmdbID = queryInt(r, "foreign_id", 0)
		foreignID = ""
	}

	// A merged music release group is addressed by either id; the saved row
	// is filed under the canonical one.
	canonical := ""
	if mediaType == mediaTypeMusic && foreignID != "" {
		if resolved, aliased := lidCanonicalForeignID(foreignID); aliased {
			canonical = resolved
			foreignID = resolved
		}
	}

	rows := dlvRowsFor(u.ID, mediaType, foreignID, tmdbID, instanceID)
	dlvMu.Lock()
	defer dlvMu.Unlock()
	out := dlvResponse(rows, "", instanceID)
	if canonical != "" {
		out["canonical_foreign_id"] = canonical
	}
	if provider := q.Get("catalog_provider"); provider != "" {
		out["catalog_ref"] = map[string]any{"provider": provider, "id": q.Get("catalog_id")}
	}
	writeJSON(w, http.StatusOK, out)
}

// dlvSavedMusicHandler lists every album this user saved on one Lidarr,
// newest first. Saved intent, not library truth: a delivered album still
// appears with its receipt.
func dlvSavedMusicHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	inst, errStatus, errMsg := musicResolveInstance(u, strings.TrimSpace(r.URL.Query().Get("instance_id")))
	if errStatus != 0 {
		writeErr(w, errStatus, errMsg)
		return
	}
	rows := dlvRowsFor(u.ID, mediaTypeMusic, "", 0, inst.ID)
	dlvMu.Lock()
	defer dlvMu.Unlock()
	out := []map[string]any{}
	for _, row := range rows {
		entry := map[string]any{
			"request_id": row.ID,
			"foreign_id": row.ForeignID,
			// A saved album reads as "requested" whatever the library says:
			// the receipt is the intent, and availability is read separately.
			"status":   statusRequested,
			"delivery": []map[string]any{dlvStateView(row)},
		}
		if dlvCancelled[row.ID] {
			entry["status"] = statusUnavailable
		}
		if canonical, aliased := lidCanonicalForeignID(row.ForeignID); aliased {
			entry["canonical_foreign_id"] = canonical
		}
		out = append(out, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": out})
}

// ─── Actions ────────────────────────────────────────────

// dlvActionHandler performs one delivery action on the caller's own request.
// `cancel` withdraws it; `retry` puts a stuck row back in the queue.
func dlvActionHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id < 1 {
		writeErr(w, http.StatusBadRequest, "invalid request id")
		return
	}
	var body struct {
		Action     string `json:"action"`
		BookFormat string `json:"book_format"`
		ForeignID  string `json:"foreign_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil {
		writeErr(w, http.StatusBadRequest, "invalid delivery action")
		return
	}
	row := dlvRowByID(id)
	if row == nil || row.UserID != u.ID {
		writeErr(w, http.StatusForbidden, "that request is not yours")
		return
	}

	dlvMu.Lock()
	defer dlvMu.Unlock()
	switch body.Action {
	case "cancel":
		dlvCancelled[id] = true
	case "retry":
		delete(dlvCancelled, id)
		dlvClearFailure(id)
		row = dlvRowByID(id)
	default:
		writeErr(w, http.StatusBadRequest, "unknown delivery action")
		return
	}
	writeJSON(w, http.StatusOK, dlvResponse([]*reqLogRow{row}, "", ""))
}

// dlvClearFailure puts a row that needed attention back in the queue.
func dlvClearFailure(id int64) {
	reqMu.Lock()
	defer reqMu.Unlock()
	for _, row := range reqLog {
		if row.ID == id {
			row.AddFailureReason = ""
			row.RequestedAt = time.Now()
			return
		}
	}
}
