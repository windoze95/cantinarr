// issues.go — user-facing issue reporting/threads plus the admin issue queue
// (srv-issues §3–§4, app-admin §12). Store, renderers, and seeds live in
// data_issues.go; the remediation/agent surfaces live in remediation.go.
package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// registerIssues mounts the issue routes on the authenticated /api router.
func registerIssues(r chi.Router) {
	r.Post("/issues", issHandleCreateIssue)
	r.Get("/issues/{id}", issHandleGetIssue)
	r.Post("/issues/{id}/reply", issHandleReplyIssue)

	r.With(requireAdmin).Get("/admin/issues", issHandleAdminListIssues)
	r.With(requireAdmin).Post("/admin/issues/{id}/dismiss", issHandleAdminDismissIssue)
	r.With(requireAdmin).Post("/admin/issues/{id}/resolve", issHandleAdminResolveIssue)
	r.With(requireAdmin).Get("/admin/issues/{id}/activity", issHandleAdminIssueActivity)
}

// issParseID reads a numeric {id} path param; ok=false on a non-numeric id.
func issParseID(r *http.Request) (int, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		return 0, false
	}
	return id, true
}

// ─── POST /api/issues ───────────────────────────────────

type issCreateReq struct {
	InstanceID    string `json:"instance_id"`
	MediaType     string `json:"media_type"`
	TmdbID        int    `json:"tmdb_id"`
	TvdbID        int    `json:"tvdb_id"`
	SeasonNumber  int    `json:"season_number"`
	EpisodeNumber int    `json:"episode_number"`
	ForeignID     string `json:"foreign_id"`
	BookFormat    string `json:"book_format"`
	Category      string `json:"category"`
	Reason        string `json:"reason"`
	Title         string `json:"title"`
}

func issHandleCreateIssue(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)

	issMu.Lock()
	allowReporting := issSettings.AllowReporting
	issMu.Unlock()
	if !allowReporting {
		writeErr(w, http.StatusForbidden, "problem reporting is disabled")
		return
	}

	var req issCreateReq
	if err := issDecodeJSON(r, &req, false); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Handler-level validation (srv-issues §3.1).
	if req.InstanceID == "" || req.MediaType == "" || req.Category == "" {
		writeErr(w, http.StatusBadRequest, "instance_id, media_type, and category required")
		return
	}
	if req.MediaType == mediaTypeBook {
		if req.ForeignID == "" {
			writeErr(w, http.StatusBadRequest, "foreign_id required for a book report")
			return
		}
	} else if req.TmdbID == 0 && req.TvdbID == 0 {
		writeErr(w, http.StatusBadRequest, "tmdb_id or tvdb_id required")
		return
	}

	// Service-level validation.
	expectedService := map[string]string{
		mediaTypeMovie: serviceRadarr,
		mediaTypeTV:    serviceSonarr,
		mediaTypeBook:  serviceChaptarr,
	}[req.MediaType]
	if expectedService == "" {
		writeErr(w, http.StatusBadRequest, "unsupported media type: "+req.MediaType)
		return
	}
	inst := instanceByID(req.InstanceID)
	if inst == nil || inst.ServiceType != expectedService {
		writeErr(w, http.StatusBadRequest,
			fmt.Sprintf("invalid instance_id for %s: %s", req.MediaType, req.InstanceID))
		return
	}
	switch req.Category {
	case "wrong_content", "bad_copy", "wrong_audio", "other":
	default:
		writeErr(w, http.StatusBadRequest, "invalid category: "+req.Category)
		return
	}
	if req.TmdbID < 0 || req.TvdbID < 0 || req.SeasonNumber < 0 || req.EpisodeNumber < 0 {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Title) > 512 || len(req.Reason) > 8192 {
		writeErr(w, http.StatusBadRequest, "issue title or detail is too long")
		return
	}

	title := req.Title
	if req.MediaType == mediaTypeBook {
		switch req.BookFormat {
		case "", bookFormatEbook, bookFormatAudiobook:
		default:
			writeErr(w, http.StatusBadRequest, "book_format must be ebook or audiobook")
			return
		}
		book, ok := bookByForeignID(req.ForeignID)
		if !ok || book == nil || len(book.Formats) == 0 {
			writeErr(w, http.StatusBadRequest, "no library book matches this report; it may have been removed")
			return
		}
		if req.BookFormat == "" && book.Formats[bookFormatEbook] != nil && book.Formats[bookFormatAudiobook] != nil {
			writeErr(w, http.StatusBadRequest, "this title exists as both ebook and audiobook; book_format is required")
			return
		}
		if req.BookFormat != "" && book.Formats[req.BookFormat] == nil {
			writeErr(w, http.StatusBadRequest, "no library book matches this report; it may have been removed")
			return
		}
		if title == "" {
			title = book.Title
		}
	}

	// Normalization: non-TV reports zero out season/episode; movies zero tvdb.
	if req.MediaType != mediaTypeTV {
		req.SeasonNumber, req.EpisodeNumber = 0, 0
	}
	if req.MediaType == mediaTypeMovie {
		req.TvdbID = 0
	}
	if req.MediaType == mediaTypeBook {
		req.TmdbID = 0
	}

	now := time.Now().UTC()
	sameScope := func(i *issIssue) bool {
		return i.InstanceID == req.InstanceID &&
			i.MediaType == req.MediaType &&
			i.TmdbID == req.TmdbID &&
			i.ForeignID == req.ForeignID &&
			i.SeasonNumber == req.SeasonNumber &&
			i.EpisodeNumber == req.EpisodeNumber
	}

	issMu.Lock()

	// Dedupe: the same reporter re-reporting the same open scope bumps
	// occurrences and returns the existing issue.
	for _, i := range issIssues {
		if i.ClosedAt != nil || i.ReporterID != u.ID || i.Category != req.Category || !sameScope(i) {
			continue
		}
		i.Occurrences++
		if req.Reason != "" {
			issLockedAppendMsg(i.ID, "user", u.Username, req.Reason, now)
		}
		i.UpdatedAt = now
		if i.Status != "observing" && i.Status != "recovering" {
			i.Read = false
		}
		id, status, rid := i.ID, i.Status, i.ReporterID
		issMu.Unlock()
		wsToAdmins(evtIssueUpdated, map[string]any{"issue_id": id})
		wsToUser(rid, evtIssueUpdated, map[string]any{"issue_id": id})
		writeJSON(w, http.StatusOK, map[string]any{"issue_id": id, "status": status})
		return
	}

	// Adoption: an in-flight auto-detected observing/recovering issue matching
	// the exact scope is converted to a user report.
	for _, i := range issIssues {
		if i.ClosedAt != nil || i.Source != "auto" || !sameScope(i) {
			continue
		}
		if i.Status != "observing" && i.Status != "recovering" {
			continue
		}
		i.Source = "user"
		i.Category = req.Category
		i.ReporterID = u.ID
		i.ReporterName = u.Username
		if req.Reason != "" {
			i.Detail = req.Reason
			issLockedAppendMsg(i.ID, "user", u.Username, req.Reason, now)
		}
		i.UpdatedAt = now
		id, status, rid := i.ID, i.Status, i.ReporterID
		issMu.Unlock()
		wsToAdmins(evtIssueUpdated, map[string]any{"issue_id": id})
		wsToUser(rid, evtIssueUpdated, map[string]any{"issue_id": id})
		writeJSON(w, http.StatusOK, map[string]any{"issue_id": id, "status": status})
		return
	}

	// Fresh issue: initial status is always observing (never open on create);
	// passive observing issues stay read.
	i := &issIssue{
		ID: issNextIssueID, Source: "user", Status: "observing",
		Category: req.Category, ReporterID: u.ID, ReporterName: u.Username,
		TmdbID: req.TmdbID, MediaType: req.MediaType, Title: title,
		SeasonNumber: req.SeasonNumber, EpisodeNumber: req.EpisodeNumber,
		Detail: req.Reason, Occurrences: 1, Read: true,
		CreatedAt: now, UpdatedAt: now,
		InstanceID: req.InstanceID, ForeignID: req.ForeignID,
	}
	issNextIssueID++
	issIssues[i.ID] = i
	openCount := issLockedOpenCount()
	id, status, issueTitle := i.ID, i.Status, i.Title
	issMu.Unlock()

	wsToAdmins(evtIssueCreated, map[string]any{
		"issue_id": id, "title": issueTitle, "source": "user", "open_count": openCount,
	})
	writeJSON(w, http.StatusOK, map[string]any{"issue_id": id, "status": status})
}

// ─── GET /api/issues/{id} ───────────────────────────────

func issHandleGetIssue(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	id, ok := issParseID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid issue id")
		return
	}
	issMu.Lock()
	i := issIssues[id]
	if i == nil {
		issMu.Unlock()
		writeErr(w, http.StatusNotFound, "issue not found")
		return
	}
	isAdmin := u.Role == roleAdmin
	if !isAdmin && i.ReporterID != u.ID {
		issMu.Unlock()
		writeErr(w, http.StatusForbidden, "forbidden")
		return
	}
	// An admin viewer marks the issue read in this very response; the reporter
	// does not. Mark-read never touches updated_at.
	if isAdmin {
		i.Read = true
	}
	thread := make([]map[string]any, 0, len(issThreads[id]))
	for _, m := range issThreads[id] {
		thread = append(thread, issLockedMsgJSON(m))
	}
	payload := map[string]any{"issue": issLockedIssueJSON(i), "thread": thread}
	issMu.Unlock()
	writeJSON(w, http.StatusOK, payload)
}

// ─── POST /api/issues/{id}/reply ────────────────────────

type issReplyBody struct {
	Body string `json:"body"`
}

func issHandleReplyIssue(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	id, ok := issParseID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid issue id")
		return
	}
	var body issReplyBody
	if err := issDecodeJSON(r, &body, true); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(body.Body) == "" {
		writeErr(w, http.StatusBadRequest, "body required")
		return
	}
	if len(body.Body) > 8192 {
		writeErr(w, http.StatusBadRequest, "reply is too long")
		return
	}

	issMu.Lock()
	i := issIssues[id]
	if i == nil {
		issMu.Unlock()
		writeErr(w, http.StatusNotFound, "issue not found")
		return
	}
	isAdmin := u.Role == roleAdmin
	if !isAdmin && i.ReporterID != u.ID {
		issMu.Unlock()
		writeErr(w, http.StatusForbidden, "forbidden")
		return
	}
	if i.ClosedAt != nil {
		issMu.Unlock()
		writeErr(w, http.StatusBadRequest, "issue is closed")
		return
	}

	now := time.Now().UTC()
	authorKind := "user"
	if isAdmin {
		authorKind = "admin"
	}
	issLockedAppendMsg(id, authorKind, u.Username, body.Body, now)
	i.UpdatedAt = now
	if !isAdmin {
		// A user reply flags the issue unread; an admin reply preserves read.
		i.Read = false
	}

	supersededCount := 0
	fromReporterOrAdmin := isAdmin || i.ReporterID == u.ID
	switch {
	case i.Status == "awaiting_user" && fromReporterOrAdmin:
		// The parked run resumes.
		i.Status = "investigating"
		for _, run := range issRuns {
			if run.IssueID == id && run.Status == "waiting_user" {
				run.Status = "running"
				run.StopReason = ""
			}
		}
	case i.Status == "awaiting_approval" && fromReporterOrAdmin:
		// New thread information supersedes the pending proposal; the
		// investigation resumes.
		for _, a := range issActions {
			if a.IssueID == id && a.Status == "proposed" {
				a.Status = "superseded"
				a.ResultText = "Superseded because new issue-thread information arrived before an admin decision."
				supersededCount++
			}
		}
		i.Status = "investigating"
		for _, run := range issRuns {
			if run.IssueID == id && run.Status == "waiting_approval" {
				run.Status = "running"
				run.StopReason = ""
			}
		}
	}
	pending := issLockedPendingCount()
	reporterID := i.ReporterID
	issMu.Unlock()

	if supersededCount > 0 {
		wsToAdmins(evtAgentActionDecided, map[string]any{
			"issue_id": id, "status": "superseded", "pending_count": pending,
		})
	}
	// Contract §6: issue_updated fans out to the reporter AND admins.
	wsToAdmins(evtIssueUpdated, map[string]any{"issue_id": id})
	if reporterID != 0 {
		wsToUser(reporterID, evtIssueUpdated, map[string]any{"issue_id": id})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ─── GET /api/admin/issues ──────────────────────────────

func issHandleAdminListIssues(w http.ResponseWriter, r *http.Request) {
	statusFilter := r.URL.Query().Get("status")
	issMu.Lock()
	sorted := issLockedIssuesSorted()
	out := make([]map[string]any, 0, len(sorted))
	for _, i := range sorted {
		if statusFilter != "" && i.Status != statusFilter {
			continue
		}
		out = append(out, issLockedIssueJSON(i))
	}
	issMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"issues": out})
}

// ─── POST /api/admin/issues/{id}/dismiss ────────────────

func issHandleAdminDismissIssue(w http.ResponseWriter, r *http.Request) {
	id, ok := issParseID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid issue id")
		return
	}
	issMu.Lock()
	i := issIssues[id]
	if i == nil {
		issMu.Unlock()
		writeErr(w, http.StatusBadRequest, "issue not found")
		return
	}
	if i.ClosedAt != nil {
		// Double-dismiss of an already-closed issue is an idempotent no-op.
		issMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	for _, a := range issActions {
		if a.IssueID == id && a.Status == "executing" {
			issMu.Unlock()
			writeErr(w, http.StatusBadRequest,
				"an approved fix is still executing; wait for its outcome before closing the issue")
			return
		}
	}
	now := time.Now().UTC()
	i.Status = "dismissed"
	i.Resolution = "Dismissed by an administrator."
	i.ResolutionKind = "admin_dismissed"
	closed := now
	i.ClosedAt = &closed
	i.Read = true
	i.UpdatedAt = now
	supersededCount := issLockedCloseIssueSideEffects(id)
	pending := issLockedPendingCount()
	reporterID := i.ReporterID
	issMu.Unlock()

	if supersededCount > 0 {
		wsToAdmins(evtAgentActionDecided, map[string]any{
			"issue_id": id, "status": "superseded", "pending_count": pending,
		})
	}
	wsToAdmins(evtIssueUpdated, map[string]any{"issue_id": id})
	if reporterID != 0 {
		wsToUser(reporterID, evtIssueUpdated, map[string]any{"issue_id": id})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// issLockedCloseIssueSideEffects supersedes any still-proposed action and
// parks the issue's waiting runs when an issue closes. Returns how many
// proposals were superseded. Call with issMu held.
func issLockedCloseIssueSideEffects(issueID int) int {
	superseded := 0
	for _, a := range issActions {
		if a.IssueID == issueID && a.Status == "proposed" {
			a.Status = "superseded"
			a.ResultText = "Superseded because the issue closed before approval; no fix was executed."
			superseded++
		}
	}
	now := time.Now().UTC()
	for _, run := range issRuns {
		if run.IssueID != issueID {
			continue
		}
		switch run.Status {
		case "running", "waiting_approval", "waiting_user", "resume_pending":
			run.Status = "aborted"
			f := now
			run.FinishedAt = &f
		}
	}
	return superseded
}

// ─── POST /api/admin/issues/{id}/resolve ────────────────

type issResolveBody struct {
	Disposition string `json:"disposition"`
	Note        string `json:"note"`
}

func issHandleAdminResolveIssue(w http.ResponseWriter, r *http.Request) {
	id, ok := issParseID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid issue id")
		return
	}
	var body issResolveBody
	if err := issDecodeJSON(r, &body, false); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Disposition != "resolved" && body.Disposition != "wont_fix" {
		writeErr(w, http.StatusBadRequest, "disposition must be resolved or wont_fix")
		return
	}
	note := strings.TrimSpace(body.Note)
	if note == "" {
		writeErr(w, http.StatusBadRequest, "resolution note is required")
		return
	}
	if len(note) > 8192 {
		writeErr(w, http.StatusBadRequest, "resolution note is too long")
		return
	}

	issMu.Lock()
	i := issIssues[id]
	if i == nil {
		issMu.Unlock()
		writeErr(w, http.StatusBadRequest, "issue not found")
		return
	}
	if i.ClosedAt != nil {
		issMu.Unlock()
		writeErr(w, http.StatusConflict, "issue completion conflict: issue is already closed or changed")
		return
	}
	for _, a := range issActions {
		if a.IssueID == id && a.Status == "executing" {
			issMu.Unlock()
			writeErr(w, http.StatusConflict, "issue completion conflict: an approved fix is still executing")
			return
		}
	}
	now := time.Now().UTC()
	i.Status = body.Disposition
	i.Resolution = note
	i.ResolutionKind = "admin_completed"
	closed := now
	i.ClosedAt = &closed
	i.Read = true
	i.UpdatedAt = now
	supersededCount := issLockedCloseIssueSideEffects(id)
	pending := issLockedPendingCount()
	payload := issLockedIssueJSON(i)
	reporterID := i.ReporterID
	issMu.Unlock()

	if supersededCount > 0 {
		wsToAdmins(evtAgentActionDecided, map[string]any{
			"issue_id": id, "status": "superseded", "pending_count": pending,
		})
	}
	wsToAdmins(evtIssueUpdated, map[string]any{"issue_id": id})
	if reporterID != 0 {
		wsToUser(reporterID, evtIssueUpdated, map[string]any{"issue_id": id})
	}
	writeJSON(w, http.StatusOK, payload)
}

// ─── GET /api/admin/issues/{id}/activity ────────────────

func issHandleAdminIssueActivity(w http.ResponseWriter, r *http.Request) {
	id, ok := issParseID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid issue id")
		return
	}
	issMu.Lock()
	if issIssues[id] == nil {
		issMu.Unlock()
		writeErr(w, http.StatusNotFound, "issue not found")
		return
	}
	actions := make([]map[string]any, 0)
	for _, a := range issLockedActionsSorted() {
		if a.IssueID == id {
			actions = append(actions, issLockedActionJSON(a))
		}
	}
	runIDs := make([]int, 0)
	for rid, run := range issRuns {
		if run.IssueID == id {
			runIDs = append(runIDs, rid)
		}
	}
	// id DESC, matching the actions ordering.
	for a := 0; a < len(runIDs); a++ {
		for b := a + 1; b < len(runIDs); b++ {
			if runIDs[b] > runIDs[a] {
				runIDs[a], runIDs[b] = runIDs[b], runIDs[a]
			}
		}
	}
	runs := make([]map[string]any, 0, len(runIDs))
	for _, rid := range runIDs {
		runs = append(runs, issLockedRunJSON(issRuns[rid]))
	}
	issMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"actions": actions, "runs": runs})
}
