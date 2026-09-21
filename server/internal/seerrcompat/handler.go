package seerrcompat

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/request"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"github.com/windoze95/cantinarr-server/internal/serversettings"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
	"github.com/windoze95/cantinarr-server/internal/version"
)

// Handler serves the Seerr-compatible surface. Metadata (release dates,
// season lists) comes from TMDB through the same client the rest of the
// server uses; tmdbClient is a func because the admin can change the
// credential at runtime.
type Handler struct {
	db         *sql.DB
	requests   *request.Service
	settings   *serversettings.Service
	tmdbClient func() *tmdb.Client
	now        func() time.Time
}

// NewHandler wires the surface. tmdbClient may return nil when TMDB is
// unavailable; the detail endpoints then answer 503.
func NewHandler(db *sql.DB, requests *request.Service, settings *serversettings.Service, tmdbClient func() *tmdb.Client) *Handler {
	if tmdbClient == nil {
		tmdbClient = func() *tmdb.Client { return nil }
	}
	return &Handler{db: db, requests: requests, settings: settings, tmdbClient: tmdbClient, now: time.Now}
}

// Routes is the router to mount at /api/v1. Every route sits behind the
// API-key check.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(h.authenticate)
	r.Get("/status", h.status)
	r.Get("/settings/about", h.about)
	r.Get("/settings/jobs", h.jobs)
	r.Post("/settings/jobs/{jobID}/run", h.runJob)
	r.Get("/request", h.listRequests)
	r.Get("/request/count", h.countRequests)
	r.Get("/request/{id}", h.getRequest)
	r.Delete("/request/{id}", h.deleteRequest)
	r.Post("/request/{id}/approve", h.decide)
	r.Post("/request/{id}/decline", h.decide)
	r.Get("/movie/{tmdbID}", h.movie)
	r.Get("/tv/{tmdbID}", h.tv)
	r.Get("/tv/{tmdbID}/season/{season}", h.season)
	r.Delete("/media/{id}", h.deleteMedia)
	r.Get("/user", h.listUsers)
	r.Get("/user/{id}", h.getUser)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeMessage(w, http.StatusNotFound, "This endpoint is not part of Cantinarr's Seerr-compatible API.")
	})
	return r
}

type contextKey string

const actorKey contextKey = "seerrcompat.actor"

// actor is the administrator the API key acts as.
type actor struct {
	id       int64
	username string
}

// authenticate is Seerr's API-key check: the X-Api-Key header must equal the
// issued key, and the administrator it was issued to must still be an
// administrator. Every 401 names its cause in the log the way the session
// middleware does, never the key material.
func (h *Handler) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented := strings.TrimSpace(r.Header.Get("X-Api-Key"))
		if presented == "" {
			log.Printf("seerr-api: 401 %s %s from %s: no X-Api-Key header", r.Method, r.URL.Path, r.RemoteAddr)
			writeMessage(w, http.StatusUnauthorized, "An X-Api-Key header is required. Issue the key under Settings > Seerr-compatible API.")
			return
		}
		issued, err := h.settings.SeerrAPIKey()
		if err != nil {
			log.Printf("seerr-api: 503 %s %s from %s: stored key unreadable: %v", r.Method, r.URL.Path, r.RemoteAddr, err)
			writeMessage(w, http.StatusServiceUnavailable, "The stored API key could not be read; retry shortly.")
			return
		}
		if !issued.Configured() || subtle.ConstantTimeCompare([]byte(presented), []byte(issued.Key)) != 1 {
			reason := "invalid key"
			if !issued.Configured() {
				reason = "no key issued"
			}
			log.Printf("seerr-api: 401 %s %s from %s: %s", r.Method, r.URL.Path, r.RemoteAddr, reason)
			writeMessage(w, http.StatusUnauthorized, "Invalid API key.")
			return
		}
		var (
			username string
			role     string
		)
		err = h.db.QueryRow("SELECT username, role FROM users WHERE id = ?", issued.UserID).Scan(&username, &role)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && role != "admin") {
			log.Printf("seerr-api: 401 %s %s from %s: issuing administrator no longer authorizes the key", r.Method, r.URL.Path, r.RemoteAddr)
			writeMessage(w, http.StatusUnauthorized, "The administrator this key was issued to no longer authorizes it. Issue a new key.")
			return
		}
		if err != nil {
			log.Printf("seerr-api: 503 %s %s from %s: load issuing administrator: %v", r.Method, r.URL.Path, r.RemoteAddr, err)
			writeMessage(w, http.StatusServiceUnavailable, "Temporarily unavailable; retry shortly.")
			return
		}
		ctx := context.WithValue(r.Context(), actorKey, actor{id: issued.UserID, username: username})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func actorFrom(ctx context.Context) actor {
	a, _ := ctx.Value(actorKey).(actor)
	return a
}

// writeMessage answers the way Seerr reports errors: a JSON body with one
// message field.
func writeMessage(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"message": message})
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeLedgerError turns a ledger failure into the answer an integrator can
// act on: a library that could not be read is a 503 (transient, retry; the
// integrators protect their items on it), anything else a 500.
func writeLedgerError(w http.ResponseWriter, err error) {
	if errors.Is(err, request.ErrLedgerLibraryUnreadable) {
		log.Printf("seerr-api: 503 ledger read: %v", err)
		writeMessage(w, http.StatusServiceUnavailable, "Cantinarr could not read a movie or TV library, so request availability is unknown right now. Retry shortly.")
		return
	}
	log.Printf("seerr-api: 500 ledger read: %v", err)
	writeMessage(w, http.StatusInternalServerError, "Cantinarr could not read its request ledger.")
}

// --- status and about -------------------------------------------------------

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"version":         version.Version,
		"commitTag":       "",
		"updateAvailable": false,
		"commitsBehind":   0,
		"restartRequired": false,
	})
}

func (h *Handler) about(w http.ResponseWriter, r *http.Request) {
	var requests, titles int
	if err := h.db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT media_type || ':' || tmdb_id) FROM request_log WHERE media_type IN ('movie', 'tv')`).Scan(&requests, &titles); err != nil {
		writeMessage(w, http.StatusInternalServerError, "Cantinarr could not count its requests.")
		return
	}
	zone, _ := h.now().Zone()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"version":         version.Version,
		"totalRequests":   requests,
		"totalMediaItems": titles,
		"tz":              zone,
	})
}

// --- jobs -----------------------------------------------------------------------

const availabilitySyncJob = "availability-sync"

// jobView is Seerr's job record. Cantinarr computes availability live, so
// the only job it advertises is the one integrators trigger after they change
// a library: running it drops the cached library digests so the next read
// fetches fresh state instead of waiting out the cache window.
func (h *Handler) jobView(running bool) map[string]interface{} {
	return map[string]interface{}{
		"id":                availabilitySyncJob,
		"name":              "Availability Sync",
		"type":              "process",
		"interval":          "seconds",
		"cronSchedule":      "",
		"nextExecutionTime": h.now().UTC(),
		"running":           running,
	}
}

func (h *Handler) jobs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []interface{}{h.jobView(false)})
}

func (h *Handler) runJob(w http.ResponseWriter, r *http.Request) {
	if chi.URLParam(r, "jobID") != availabilitySyncJob {
		writeMessage(w, http.StatusNotFound, "Job not found.")
		return
	}
	h.requests.InvalidateAllAvailabilityDigests()
	writeJSON(w, http.StatusOK, h.jobView(false))
}

// --- requests ---------------------------------------------------------------------

// view is one ledger read rendered into Seerr's shapes: every request, and
// every title's media record, with the requester identities resolved.
type view struct {
	requests []seerrRequest
	media    map[request.LedgerKey]*seerrMedia
	users    map[int64]*userView
}

// read performs the ledger read and renders it. Requests come back newest
// first, as Seerr's default sort (request id descending) has them.
func (h *Handler) read(f request.LedgerFilter) (*view, error) {
	ledger, err := h.requests.ReadLedger(f)
	if err != nil {
		return nil, err
	}
	users, err := loadUsers(h.db)
	if err != nil {
		return nil, err
	}
	v := &view{media: map[request.LedgerKey]*seerrMedia{}, users: users}
	// Media records first: their timestamps summarize every request of the
	// title, so each request can carry the finished record.
	for _, row := range ledger.Rows {
		key := request.LedgerKey{MediaType: row.MediaType, TmdbID: row.TmdbID}
		m, ok := v.media[key]
		if !ok {
			m = h.mediaFor(key, ledger.Titles[key])
			v.media[key] = m
		}
		if row.RequestedAt.Before(m.CreatedAt) || m.CreatedAt.IsZero() {
			m.CreatedAt = row.RequestedAt.UTC()
		}
		if row.MediaType == "tv" && row.TVDBID != 0 && m.TvdbID == nil {
			tvdb := row.TVDBID
			m.TvdbID = &tvdb
		}
		if updated := rowUpdatedAt(row); updated.After(m.UpdatedAt) {
			m.UpdatedAt = updated
		}
	}
	if f.TmdbID != 0 && f.MediaType != "" {
		key := request.LedgerKey{MediaType: f.MediaType, TmdbID: f.TmdbID}
		if _, ok := v.media[key]; !ok {
			state := ledger.Titles[key]
			if state.InLibrary {
				m := h.mediaFor(key, state)
				m.CreatedAt, m.UpdatedAt = h.now().UTC(), h.now().UTC()
				v.media[key] = m
			}
		}
	}
	for _, row := range ledger.Rows {
		v.requests = append(v.requests, h.requestFor(row, ledger.Titles, v))
	}
	return v, nil
}

// mediaFor renders a title's live state as Seerr's Media record, timestamps
// left for the caller to fill from the title's requests.
func (h *Handler) mediaFor(key request.LedgerKey, state request.LedgerTitleState) *seerrMedia {
	m := &seerrMedia{
		ID:           mediaID(key.MediaType, key.TmdbID),
		MediaType:    key.MediaType,
		TmdbID:       key.TmdbID,
		Status:       mediaStatus(state),
		Status4k:     mediaUnknown,
		MediaAddedAt: state.AddedAt,
	}
	if key.MediaType == "tv" {
		numbers := make([]int, 0, len(state.Seasons))
		for n := range state.Seasons {
			numbers = append(numbers, n)
		}
		sort.Ints(numbers)
		for _, n := range numbers {
			m.Seasons = append(m.Seasons, seerrSeason{
				ID:           m.ID*1000 + int64(n),
				SeasonNumber: n,
				Status:       seasonStatus(state.Seasons[n]),
				Status4k:     mediaUnknown,
			})
		}
	}
	return m
}

// rowUpdatedAt is the request's last change: its decision when one was
// made, else its creation.
func rowUpdatedAt(row request.LedgerRow) time.Time {
	if row.DecidedAt != nil {
		return row.DecidedAt.UTC()
	}
	return row.RequestedAt.UTC()
}

func (h *Handler) requestFor(row request.LedgerRow, titles map[request.LedgerKey]request.LedgerTitleState, v *view) seerrRequest {
	key := request.LedgerKey{MediaType: row.MediaType, TmdbID: row.TmdbID}
	state := titles[key]
	media := *v.media[key]
	media.Requests = nil
	out := seerrRequest{
		ID:          row.ID,
		Status:      requestStatus(row, state),
		CreatedAt:   row.RequestedAt.UTC(),
		UpdatedAt:   rowUpdatedAt(row),
		Type:        row.MediaType,
		ServerID:    0,
		RootFolder:  "",
		Media:       media,
		Seasons:     []seerrSeasonRequest{},
		RequestedBy: h.userFor(v, row.UserID),
	}
	if row.QualityProfileID != 0 {
		profile := row.QualityProfileID
		out.ProfileID = &profile
	}
	if row.ApprovedBy != 0 {
		modifier := h.userFor(v, row.ApprovedBy)
		out.ModifiedBy = &modifier
	}
	for _, n := range row.Seasons {
		out.Seasons = append(out.Seasons, seerrSeasonRequest{
			ID:           row.ID*1000 + int64(n),
			SeasonNumber: n,
			Status:       seasonRequestStatus(row, state, n),
			CreatedAt:    out.CreatedAt,
			UpdatedAt:    out.UpdatedAt,
		})
	}
	out.SeasonCount = len(out.Seasons)
	return out
}

func (h *Handler) userFor(v *view, id int64) seerrUser {
	if u, ok := v.users[id]; ok {
		return u.seerr()
	}
	return unknownUser(id)
}

// listQuery is Seerr's GET /request query: pagination plus the filters its
// own UI and the integrators send.
type listQuery struct {
	take, skip    int
	filter        string
	mediaType     string
	requestedBy   int64
	sortModified  bool
	sortAscending bool
}

func parseListQuery(r *http.Request) listQuery {
	q := listQuery{take: 10, filter: "all", mediaType: "all"}
	if n, err := strconv.Atoi(r.URL.Query().Get("take")); err == nil && n > 0 {
		q.take = n
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("skip")); err == nil && n >= 0 {
		q.skip = n
	}
	if f := r.URL.Query().Get("filter"); f != "" {
		q.filter = f
	}
	if t := r.URL.Query().Get("mediaType"); t != "" {
		q.mediaType = t
	}
	if n, err := strconv.ParseInt(r.URL.Query().Get("requestedBy"), 10, 64); err == nil && n > 0 {
		q.requestedBy = n
	}
	q.sortModified = r.URL.Query().Get("sort") == "modified"
	q.sortAscending = r.URL.Query().Get("sortDirection") == "asc"
	return q
}

// matchesFilter applies Seerr's filter vocabulary (request.ts) to a rendered
// request: each named filter is a set of request statuses and a set of media
// statuses, and both must admit the request.
func matchesFilter(filter string, req seerrRequest) bool {
	requestOK := true
	mediaOK := true
	switch filter {
	case "approved", "processing":
		requestOK = req.Status == requestApproved
	case "pending":
		requestOK = req.Status == requestPending
	case "unavailable":
		requestOK = req.Status == requestPending || req.Status == requestApproved
	case "failed":
		requestOK = req.Status == requestFailed
	case "completed", "available", "deleted":
		requestOK = req.Status == requestCompleted
	}
	switch filter {
	case "available":
		mediaOK = req.Media.Status == mediaAvailable
	case "processing", "unavailable":
		mediaOK = req.Media.Status != mediaAvailable
	case "deleted":
		// Seerr keeps a "deleted" media status for titles removed after
		// arriving; Cantinarr reads availability live and has no such memory.
		mediaOK = false
	}
	return requestOK && mediaOK
}

func (h *Handler) listRequests(w http.ResponseWriter, r *http.Request) {
	q := parseListQuery(r)
	filter := request.LedgerFilter{UserID: q.requestedBy}
	if q.mediaType == "movie" || q.mediaType == "tv" {
		filter.MediaType = q.mediaType
	}
	v, err := h.read(filter)
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	matched := make([]seerrRequest, 0, len(v.requests))
	for _, req := range v.requests {
		if matchesFilter(q.filter, req) {
			matched = append(matched, req)
		}
	}
	// Seerr sorts by request id unless asked for "modified" (last change),
	// descending unless asked for ascending.
	less := func(a, b seerrRequest) bool {
		if q.sortModified && !a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.UpdatedAt.Before(b.UpdatedAt)
		}
		return a.ID < b.ID
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if q.sortAscending {
			return less(matched[i], matched[j])
		}
		return less(matched[j], matched[i])
	})
	total := len(matched)
	start := q.skip
	if start > total {
		start = total
	}
	end := start + q.take
	if end > total {
		end = total
	}
	page := matched[start:end]
	if page == nil {
		page = []seerrRequest{}
	}
	pages := (total + q.take - 1) / q.take
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"pageInfo": pageInfo{Pages: pages, PageSize: q.take, Results: total, Page: q.skip/q.take + 1},
		"results":  page,
	})
}

func (h *Handler) countRequests(w http.ResponseWriter, r *http.Request) {
	v, err := h.read(request.LedgerFilter{})
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	counts := map[string]int{"total": 0, "movie": 0, "tv": 0, "pending": 0, "approved": 0, "declined": 0, "processing": 0, "available": 0, "completed": 0}
	for _, req := range v.requests {
		counts["total"]++
		counts[req.Type]++
		switch req.Status {
		case requestPending:
			counts["pending"]++
		case requestApproved:
			counts["approved"]++
			if req.Media.Status == mediaAvailable {
				counts["available"]++
			} else {
				counts["processing"]++
			}
		case requestDeclined:
			counts["declined"]++
		case requestCompleted:
			counts["completed"]++
		}
	}
	writeJSON(w, http.StatusOK, counts)
}

func (h *Handler) getRequest(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeMessage(w, http.StatusNotFound, "Request not found.")
		return
	}
	v, err := h.read(request.LedgerFilter{RequestID: id})
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	if len(v.requests) == 0 {
		writeMessage(w, http.StatusNotFound, "Request not found.")
		return
	}
	writeJSON(w, http.StatusOK, v.requests[0])
}

// deleteRequest is Seerr's DELETE /request/{id}: the request is forgotten.
// A request whose delivery is still running answers 409 rather than being
// pulled out from under the worker; an id that is not a movie/TV request is
// not found, so an integrator that only ever saw movie/TV ids cannot remove
// a book or album request.
func (h *Handler) deleteRequest(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeMessage(w, http.StatusNotFound, "Request not found.")
		return
	}
	deleted, skipped, err := h.requests.ForgetRequests([]int64{id})
	if err != nil {
		log.Printf("seerr-api: delete request %d: %v", id, err)
		writeMessage(w, http.StatusInternalServerError, "Cantinarr could not delete the request.")
		return
	}
	if len(skipped) > 0 {
		writeMessage(w, http.StatusConflict, "That request is still being delivered to the library; retry once it settles.")
		return
	}
	if deleted == 0 {
		writeMessage(w, http.StatusNotFound, "Request not found.")
		return
	}
	log.Printf("seerr-api: %s deleted request %d", actorFrom(r.Context()).username, id)
	w.WriteHeader(http.StatusNoContent)
}

// decide is Seerr's POST /request/{id}/approve and /decline, acting as the
// administrator the key was issued to. The answer is the request as it now
// reads.
func (h *Handler) decide(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeMessage(w, http.StatusNotFound, "Request not found.")
		return
	}
	who := actorFrom(r.Context())
	approve := strings.HasSuffix(r.URL.Path, "/approve")
	if approve {
		_, err = h.requests.ApproveRequest(who.id, id, &request.DecisionOverride{})
	} else {
		err = h.requests.DenyRequest(who.id, id, "")
	}
	if err != nil {
		log.Printf("seerr-api: %s decide request %d (approve=%t): %v", who.username, id, approve, secrets.RedactError(err))
		writeMessage(w, http.StatusBadRequest, secrets.RedactError(err).Error())
		return
	}
	v, err := h.read(request.LedgerFilter{RequestID: id})
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	if len(v.requests) == 0 {
		writeMessage(w, http.StatusNotFound, "Request not found.")
		return
	}
	writeJSON(w, http.StatusOK, v.requests[0])
}

// deleteMedia is Seerr's DELETE /media/{id}: every request for the title is
// forgotten. Seerr answers 204 for an id it does not hold, and so does this.
func (h *Handler) deleteMedia(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	mediaType, tmdbID := decodeMediaID(id)
	ids, err := h.requests.LedgerRequestIDs(mediaType, tmdbID)
	if err != nil {
		writeMessage(w, http.StatusInternalServerError, "Cantinarr could not read the title's requests.")
		return
	}
	deleted, skipped, err := h.requests.ForgetRequests(ids)
	if err != nil {
		log.Printf("seerr-api: delete media %d (%s %d): %v", id, mediaType, tmdbID, err)
		writeMessage(w, http.StatusInternalServerError, "Cantinarr could not delete the title's requests.")
		return
	}
	if len(skipped) > 0 {
		writeMessage(w, http.StatusConflict, "A request for that title is still being delivered to the library; retry once it settles.")
		return
	}
	if deleted > 0 {
		log.Printf("seerr-api: %s deleted %d request(s) for %s %d", actorFrom(r.Context()).username, deleted, mediaType, tmdbID)
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- titles ---------------------------------------------------------------------

// mediaInfoFor is the mediaInfo block of a detail answer: the title's media
// record with its requests attached, or nil when Cantinarr knows nothing of
// the title (no request, not in any library), which Seerr renders the same
// way.
func (h *Handler) mediaInfoFor(mediaType string, tmdbID int) (*seerrMedia, error) {
	v, err := h.read(request.LedgerFilter{MediaType: mediaType, TmdbID: tmdbID})
	if err != nil {
		return nil, err
	}
	m, ok := v.media[request.LedgerKey{MediaType: mediaType, TmdbID: tmdbID}]
	if !ok {
		return nil, nil
	}
	// Seerr lists a title's requests oldest first; integrators read the
	// first one as "the" request date.
	requests := make([]seerrRequest, 0, len(v.requests))
	for i := len(v.requests) - 1; i >= 0; i-- {
		requests = append(requests, v.requests[i])
	}
	m.Requests = &requests
	return m, nil
}

func (h *Handler) tmdbOr503(w http.ResponseWriter) *tmdb.Client {
	client := h.tmdbClient()
	if client == nil {
		writeMessage(w, http.StatusServiceUnavailable, "TMDB is not configured, so title details are unavailable.")
	}
	return client
}

func tmdbParam(r *http.Request, name string) (int, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, name))
	return id, err == nil && id > 0
}

func (h *Handler) movie(w http.ResponseWriter, r *http.Request) {
	tmdbID, ok := tmdbParam(r, "tmdbID")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Movie not found.")
		return
	}
	client := h.tmdbOr503(w)
	if client == nil {
		return
	}
	details, err := client.GetMovieDetails(tmdbID)
	if err != nil {
		log.Printf("seerr-api: movie %d details: %v", tmdbID, err)
		writeMessage(w, http.StatusBadGateway, "TMDB could not answer for this movie.")
		return
	}
	info, err := h.mediaInfoFor("movie", tmdbID)
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":          details.ID,
		"mediaType":   "movie",
		"imdbId":      details.IMDBID,
		"title":       details.Title,
		"overview":    details.Overview,
		"posterPath":  details.PosterPath,
		"releaseDate": details.ReleaseDate,
		"adult":       details.Adult,
		"mediaInfo":   info,
	})
}

func (h *Handler) tv(w http.ResponseWriter, r *http.Request) {
	tmdbID, ok := tmdbParam(r, "tmdbID")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Series not found.")
		return
	}
	client := h.tmdbOr503(w)
	if client == nil {
		return
	}
	details, err := client.GetTVDetails(tmdbID)
	if err != nil {
		log.Printf("seerr-api: tv %d details: %v", tmdbID, err)
		writeMessage(w, http.StatusBadGateway, "TMDB could not answer for this series.")
		return
	}
	info, err := h.mediaInfoFor("tv", tmdbID)
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	seasons := make([]map[string]interface{}, 0, len(details.Seasons))
	for _, s := range details.Seasons {
		seasons = append(seasons, map[string]interface{}{
			"id":           int64(details.ID)*1000 + int64(s.SeasonNumber),
			"seasonNumber": s.SeasonNumber,
			"name":         s.Name,
			"episodeCount": s.EpisodeCount,
			"airDate":      s.AirDate,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":           details.ID,
		"mediaType":    "tv",
		"name":         details.Name,
		"originalName": details.OriginalName,
		"overview":     details.Overview,
		"posterPath":   details.PosterPath,
		"firstAirDate": details.FirstAir,
		"seasons":      seasons,
		"mediaInfo":    info,
	})
}

// tmdbSeason is the slice of TMDB's season detail the season endpoint
// renders.
type tmdbSeason struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Overview     string `json:"overview"`
	AirDate      string `json:"air_date"`
	SeasonNumber int    `json:"season_number"`
	Episodes     []struct {
		ID            int    `json:"id"`
		Name          string `json:"name"`
		Overview      string `json:"overview"`
		AirDate       string `json:"air_date"`
		SeasonNumber  int    `json:"season_number"`
		EpisodeNumber int    `json:"episode_number"`
	} `json:"episodes"`
}

func (h *Handler) season(w http.ResponseWriter, r *http.Request) {
	tmdbID, ok := tmdbParam(r, "tmdbID")
	seasonNumber, okSeason := strconv.Atoi(chi.URLParam(r, "season"))
	if !ok || okSeason != nil || seasonNumber < 0 {
		writeMessage(w, http.StatusNotFound, "Season not found.")
		return
	}
	client := h.tmdbOr503(w)
	if client == nil {
		return
	}
	raw, err := client.DoGetRaw("/tv/"+strconv.Itoa(tmdbID)+"/season/"+strconv.Itoa(seasonNumber), nil)
	if err != nil {
		log.Printf("seerr-api: tv %d season %d: %v", tmdbID, seasonNumber, err)
		writeMessage(w, http.StatusBadGateway, "TMDB could not answer for this season.")
		return
	}
	var season tmdbSeason
	if err := json.Unmarshal(raw, &season); err != nil {
		writeMessage(w, http.StatusBadGateway, "TMDB answered with an unexpected season shape.")
		return
	}
	episodes := make([]map[string]interface{}, 0, len(season.Episodes))
	for _, ep := range season.Episodes {
		episodes = append(episodes, map[string]interface{}{
			"id":            ep.ID,
			"name":          ep.Name,
			"overview":      ep.Overview,
			"airDate":       ep.AirDate,
			"seasonNumber":  ep.SeasonNumber,
			"episodeNumber": ep.EpisodeNumber,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":           season.ID,
		"name":         season.Name,
		"overview":     season.Overview,
		"airDate":      season.AirDate,
		"seasonNumber": season.SeasonNumber,
		"episodeCount": len(season.Episodes),
		"episodes":     episodes,
	})
}

// --- users ----------------------------------------------------------------------

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	q := parseListQuery(r)
	users, err := loadUsers(h.db)
	if err != nil {
		log.Printf("seerr-api: list users: %v", err)
		writeMessage(w, http.StatusInternalServerError, "Cantinarr could not read its users.")
		return
	}
	all := sortedUsers(users)
	total := len(all)
	start := q.skip
	if start > total {
		start = total
	}
	end := start + q.take
	if end > total {
		end = total
	}
	page := make([]seerrUser, 0, end-start)
	for _, u := range all[start:end] {
		page = append(page, u.seerr())
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"pageInfo": pageInfo{Pages: (total + q.take - 1) / q.take, PageSize: q.take, Results: total, Page: q.skip/q.take + 1},
		"results":  page,
	})
}

func (h *Handler) getUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeMessage(w, http.StatusNotFound, "User not found.")
		return
	}
	users, err := loadUsers(h.db)
	if err != nil {
		writeMessage(w, http.StatusInternalServerError, "Cantinarr could not read its users.")
		return
	}
	u, ok := users[id]
	if !ok {
		writeMessage(w, http.StatusNotFound, "User not found.")
		return
	}
	writeJSON(w, http.StatusOK, u.seerr())
}
