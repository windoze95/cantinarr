package main

// A deliberately bounded Seerr-compatible API for the demo. It exposes the
// same movie/TV request ledger the app uses, authenticated by the key issued
// in Settings, so Maintainerr, Dashbrr, Homepage, and similar clients can be
// exercised against demo.cantinarr.com without a user session.

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

var (
	seerrMu        sync.Mutex
	seerrKey       string
	seerrIssuedBy  int
	seerrCreatedAt time.Time
)

func registerSeerrAdmin(r chi.Router) {
	admin := r.With(requireAdmin)
	admin.Get("/admin/seerr-api", seerrAdminKey)
	admin.Post("/admin/seerr-api", seerrAdminKey)
	admin.Delete("/admin/seerr-api", seerrAdminKey)
}

func seerrAdminKey(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	seerrMu.Lock()
	switch r.Method {
	case http.MethodPost:
		seerrKey = "cantinarr-" + randomHex(32)
		seerrIssuedBy = u.ID
		seerrCreatedAt = time.Now().UTC().Truncate(time.Second)
	case http.MethodDelete:
		seerrKey, seerrIssuedBy, seerrCreatedAt = "", 0, time.Time{}
	}
	key, by, created := seerrKey, seerrIssuedBy, seerrCreatedAt
	seerrMu.Unlock()
	payload := map[string]any{"configured": key != ""}
	if key != "" {
		payload["api_key"] = key
		payload["created_at"] = created
		if issuer := userByID(by); issuer != nil {
			payload["issued_by"] = issuer.Username
		} else {
			payload["issued_by"] = "a deleted administrator"
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, payload)
}

func registerSeerrCompat(r chi.Router) {
	r.Route("/v1", func(v1 chi.Router) {
		v1.Use(seerrAuthenticate)
		v1.Get("/status", seerrStatus)
		v1.Get("/settings/about", seerrAbout)
		v1.Get("/settings/jobs", seerrJobs)
		v1.Post("/settings/jobs/{jobID}/run", seerrRunJob)
		v1.Get("/request", seerrListRequests)
		v1.Get("/request/count", seerrCountRequests)
		v1.Get("/request/{id}", seerrGetRequest)
		v1.Delete("/request/{id}", seerrDeleteRequest)
		v1.Post("/request/{id}/approve", seerrDecideRequest)
		v1.Post("/request/{id}/decline", seerrDecideRequest)
		v1.Get("/movie/{tmdbID}", seerrMovie)
		v1.Get("/tv/{tmdbID}", seerrTV)
		v1.Get("/tv/{tmdbID}/season/{season}", seerrSeason)
		v1.Delete("/media/{id}", seerrDeleteMedia)
		v1.Get("/user", seerrListUsers)
		v1.Get("/user/{id}", seerrGetUser)
		v1.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			seerrError(w, http.StatusNotFound, "This endpoint is not part of Cantinarr's Seerr-compatible API.")
		})
	})
}

func seerrAuthenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented := strings.TrimSpace(r.Header.Get("X-Api-Key"))
		seerrMu.Lock()
		key, issuedBy := seerrKey, seerrIssuedBy
		seerrMu.Unlock()
		issuer := userByID(issuedBy)
		if presented == "" {
			seerrError(w, http.StatusUnauthorized, "An X-Api-Key header is required. Issue the key under Settings > Seerr-compatible API.")
			return
		}
		if key == "" || subtle.ConstantTimeCompare([]byte(presented), []byte(key)) != 1 {
			seerrError(w, http.StatusUnauthorized, "Invalid API key.")
			return
		}
		if issuer == nil || issuer.Role != roleAdmin {
			seerrError(w, http.StatusUnauthorized, "The administrator this key was issued to no longer authorizes it. Issue a new key.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func seerrError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"message": message})
}

func seerrStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"version": "demo", "commitTag": "", "updateAvailable": false, "commitsBehind": 0, "restartRequired": false})
}

func seerrMovieTVRows() []reqLogRow {
	reqMu.Lock()
	defer reqMu.Unlock()
	out := []reqLogRow{}
	for _, row := range reqLog {
		if row.MediaType == mediaTypeMovie || row.MediaType == mediaTypeTV {
			cp := *row
			out = append(out, cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

func seerrAbout(w http.ResponseWriter, _ *http.Request) {
	rows := seerrMovieTVRows()
	titles := map[string]bool{}
	for _, row := range rows {
		titles[row.MediaType+":"+strconv.Itoa(row.TmdbID)] = true
	}
	zone, _ := time.Now().Zone()
	writeJSON(w, http.StatusOK, map[string]any{"version": "demo", "totalRequests": len(rows), "totalMediaItems": len(titles), "tz": zone})
}

func seerrJobView() map[string]any {
	return map[string]any{"id": "availability-sync", "name": "Availability Sync", "type": "process",
		"interval": "seconds", "cronSchedule": "", "nextExecutionTime": time.Now().UTC(), "running": false}
}

func seerrJobs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, []any{seerrJobView()})
}

func seerrRunJob(w http.ResponseWriter, r *http.Request) {
	if chi.URLParam(r, "jobID") != "availability-sync" {
		seerrError(w, http.StatusNotFound, "Job not found.")
		return
	}
	writeJSON(w, http.StatusOK, seerrJobView())
}

func seerrUserView(u *DemoUser) map[string]any {
	count := 0
	for _, row := range seerrMovieTVRows() {
		if row.UserID == u.ID {
			count++
		}
	}
	permissions := 32
	if u.Role == roleAdmin {
		permissions = 2
	}
	return map[string]any{
		"id": u.ID, "email": u.PlexEmail, "username": u.Username,
		"plexUsername": nil, "jellyfinUsername": nil, "userType": 2,
		"plexId": nil, "jellyfinUserId": nil, "permissions": permissions,
		"avatar": "", "createdAt": u.CreatedAt.UTC(), "updatedAt": u.CreatedAt.UTC(),
		"requestCount": count, "displayName": u.Username,
	}
}

func seerrMediaID(mediaType string, tmdbID int) int64 {
	id := int64(tmdbID) * 2
	if mediaType == mediaTypeTV {
		id++
	}
	return id
}

func seerrMediaStatus(mediaType string, tmdbID int) int {
	status, _ := requestStatusForTmdb(tmdbID, mediaType)
	switch status {
	case statusAvailable:
		return 5
	case statusPartial:
		return 4
	case statusRequested, statusDownloading:
		return 3
	default:
		return 1
	}
}

func seerrRequestStatus(row reqLogRow) int {
	switch row.Status {
	case statusPending:
		return 1
	case statusDenied:
		return 3
	case statusAvailable:
		return 5
	}
	if seerrMediaStatus(row.MediaType, row.TmdbID) == 5 {
		return 5
	}
	return 2
}

func seerrRequestedSeasons(row reqLogRow) []int {
	if row.MediaType != mediaTypeTV {
		return []int{}
	}
	var seasons []int
	if strings.HasPrefix(row.SeasonScope, "[") {
		_ = json.Unmarshal([]byte(row.SeasonScope), &seasons)
	} else {
		seasons = reqSeasonsForScope(row.TmdbID, row.SeasonScope)
	}
	if seasons == nil {
		seasons = []int{}
	}
	return seasons
}

func seerrMediaView(row reqLogRow, includeRequests bool) map[string]any {
	status := seerrMediaStatus(row.MediaType, row.TmdbID)
	media := map[string]any{
		"id": seerrMediaID(row.MediaType, row.TmdbID), "mediaType": row.MediaType,
		"tmdbId": row.TmdbID, "tvdbId": nil, "status": status, "status4k": 1,
		"createdAt": row.RequestedAt.UTC(), "updatedAt": row.RequestedAt.UTC(), "mediaAddedAt": nil,
	}
	if row.MediaType == mediaTypeTV {
		if row.TvdbID != 0 {
			media["tvdbId"] = row.TvdbID
		} else if show, ok := findShow(row.TmdbID); ok {
			media["tvdbId"] = show.TvdbID
		}
		seasons := []map[string]any{}
		if show, ok := findShow(row.TmdbID); ok {
			for _, season := range show.Seasons {
				seasons = append(seasons, map[string]any{"id": seerrMediaID(row.MediaType, row.TmdbID)*1000 + int64(season.SeasonNumber),
					"seasonNumber": season.SeasonNumber, "status": status, "status4k": 1})
			}
		}
		media["seasons"] = seasons
	}
	if includeRequests {
		requests := []map[string]any{}
		for _, candidate := range seerrMovieTVRows() {
			if candidate.MediaType == row.MediaType && candidate.TmdbID == row.TmdbID {
				requests = append(requests, seerrRequestView(candidate, false))
			}
		}
		media["requests"] = requests
	}
	return media
}

func seerrRequestView(row reqLogRow, includeMediaRequests bool) map[string]any {
	requestedBy := map[string]any{"id": row.UserID, "displayName": "Deleted user", "userType": 2, "permissions": 0}
	if u := userByID(row.UserID); u != nil {
		requestedBy = seerrUserView(u)
	}
	seasons := []map[string]any{}
	for _, n := range seerrRequestedSeasons(row) {
		seasons = append(seasons, map[string]any{"id": row.ID*1000 + int64(n), "seasonNumber": n,
			"status": seerrRequestStatus(row), "createdAt": row.RequestedAt.UTC(), "updatedAt": row.RequestedAt.UTC()})
	}
	view := map[string]any{
		"id": row.ID, "status": seerrRequestStatus(row), "createdAt": row.RequestedAt.UTC(),
		"updatedAt": row.RequestedAt.UTC(), "type": row.MediaType, "is4k": false,
		"serverId": 0, "profileId": nil, "rootFolder": "",
		"media": seerrMediaView(row, includeMediaRequests), "seasons": seasons,
		"requestedBy": requestedBy, "modifiedBy": nil, "seasonCount": len(seasons),
	}
	if row.QualityProfileID != 0 {
		view["profileId"] = row.QualityProfileID
	}
	return view
}

func seerrListRequests(w http.ResponseWriter, r *http.Request) {
	take, skip := queryInt(r, "take", 10), queryInt(r, "skip", 0)
	if take <= 0 {
		take = 10
	}
	if skip < 0 {
		skip = 0
	}
	filter, mediaType := r.URL.Query().Get("filter"), r.URL.Query().Get("mediaType")
	requestedBy, _ := strconv.Atoi(r.URL.Query().Get("requestedBy"))
	rows := []reqLogRow{}
	for _, row := range seerrMovieTVRows() {
		status, mediaStatus := seerrRequestStatus(row), seerrMediaStatus(row.MediaType, row.TmdbID)
		if mediaType != "" && mediaType != "all" && row.MediaType != mediaType {
			continue
		}
		if requestedBy > 0 && row.UserID != requestedBy {
			continue
		}
		matches := true
		switch filter {
		case "approved", "processing":
			matches = status == 2
		case "pending":
			matches = status == 1
		case "failed":
			matches = status == 4
		case "completed", "available":
			matches = status == 5
		case "unavailable":
			matches = (status == 1 || status == 2) && mediaStatus != 5
		case "deleted":
			matches = false
		}
		if matches {
			rows = append(rows, row)
		}
	}
	total := len(rows)
	if skip > total {
		skip = total
	}
	end := skip + take
	if end > total {
		end = total
	}
	results := []map[string]any{}
	for _, row := range rows[skip:end] {
		results = append(results, seerrRequestView(row, false))
	}
	pages := 0
	if total > 0 {
		pages = (total + take - 1) / take
	}
	writeJSON(w, http.StatusOK, map[string]any{"pageInfo": map[string]int{
		"pages": pages, "pageSize": take, "results": total, "page": skip/take + 1}, "results": results})
}

func seerrCountRequests(w http.ResponseWriter, _ *http.Request) {
	counts := map[string]int{"total": 0, "movie": 0, "tv": 0, "pending": 0, "approved": 0, "declined": 0, "processing": 0, "available": 0, "completed": 0}
	for _, row := range seerrMovieTVRows() {
		counts["total"]++
		counts[row.MediaType]++
		switch seerrRequestStatus(row) {
		case 1:
			counts["pending"]++
		case 2:
			counts["approved"]++
			counts["processing"]++
		case 3:
			counts["declined"]++
		case 5:
			counts["completed"]++
			counts["available"]++
		}
	}
	writeJSON(w, http.StatusOK, counts)
}

func seerrRequestID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id, err == nil && id > 0
}

func seerrFindRequest(id int64) (reqLogRow, bool) {
	for _, row := range seerrMovieTVRows() {
		if row.ID == id {
			return row, true
		}
	}
	return reqLogRow{}, false
}

func seerrGetRequest(w http.ResponseWriter, r *http.Request) {
	id, ok := seerrRequestID(r)
	row, found := seerrFindRequest(id)
	if !ok || !found {
		seerrError(w, http.StatusNotFound, "Request not found.")
		return
	}
	writeJSON(w, http.StatusOK, seerrRequestView(row, false))
}

func seerrDeleteRequest(w http.ResponseWriter, r *http.Request) {
	id, ok := seerrRequestID(r)
	if !ok {
		seerrError(w, http.StatusNotFound, "Request not found.")
		return
	}
	deleted := false
	reqMu.Lock()
	for i, row := range reqLog {
		if row.ID == id && (row.MediaType == mediaTypeMovie || row.MediaType == mediaTypeTV) {
			reqLog = append(reqLog[:i], reqLog[i+1:]...)
			deleted = true
			break
		}
	}
	reqMu.Unlock()
	if !deleted {
		seerrError(w, http.StatusNotFound, "Request not found.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func seerrDecideRequest(w http.ResponseWriter, r *http.Request) {
	id, ok := seerrRequestID(r)
	if !ok {
		seerrError(w, http.StatusNotFound, "Request not found.")
		return
	}
	approve := strings.HasSuffix(r.URL.Path, "/approve")
	var target *reqLogRow
	var snapshot reqLogRow
	reqMu.Lock()
	for _, row := range reqLog {
		if row.ID == id && (row.MediaType == mediaTypeMovie || row.MediaType == mediaTypeTV) {
			target, snapshot = row, *row
			break
		}
	}
	reqMu.Unlock()
	if target == nil {
		seerrError(w, http.StatusNotFound, "Request not found.")
		return
	}
	if snapshot.Status != statusPending {
		seerrError(w, http.StatusBadRequest, "request is not pending")
		return
	}
	if approve {
		recorder := &discardResponseWriter{header: http.Header{}}
		reqAdminApproveTitle(recorder, target, &snapshot, &reqDecisionOverride{})
		if recorder.status >= 400 {
			seerrError(w, recorder.status, "Request could not be approved.")
			return
		}
	} else {
		reqMu.Lock()
		target.Status = statusDenied
		reqMu.Unlock()
		wsToUser(snapshot.UserID, evtRequestDecision, map[string]any{"decision": "denied", "tmdb_id": snapshot.TmdbID, "media_type": snapshot.MediaType, "title": snapshot.Title, "status": statusDenied})
	}
	row, _ := seerrFindRequest(id)
	writeJSON(w, http.StatusOK, seerrRequestView(row, false))
}

type discardResponseWriter struct {
	header http.Header
	status int
}

func (w *discardResponseWriter) Header() http.Header { return w.header }
func (w *discardResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return len(p), nil
}
func (w *discardResponseWriter) WriteHeader(status int) { w.status = status }

func seerrDeleteMedia(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	mediaType, tmdbID := mediaTypeMovie, int(id/2)
	if id%2 == 1 {
		mediaType = mediaTypeTV
	}
	reqMu.Lock()
	kept := reqLog[:0]
	for _, row := range reqLog {
		if row.MediaType != mediaType || row.TmdbID != tmdbID {
			kept = append(kept, row)
		}
	}
	reqLog = kept
	reqMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func seerrTMDBID(r *http.Request) (int, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "tmdbID"))
	return id, err == nil && id > 0
}

func seerrMovie(w http.ResponseWriter, r *http.Request) {
	id, ok := seerrTMDBID(r)
	movie, found := findMovie(id)
	if !ok || !found {
		seerrError(w, http.StatusNotFound, "Movie not found.")
		return
	}
	var media any
	rows := seerrMovieTVRows()
	for _, row := range rows {
		if row.MediaType == mediaTypeMovie && row.TmdbID == id {
			media = seerrMediaView(row, true)
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": movie.TmdbID, "mediaType": "movie", "imdbId": movie.ImdbID,
		"title": movie.Title, "overview": movie.Overview, "posterPath": movie.PosterPath,
		"releaseDate": movie.ReleaseDate, "adult": false, "mediaInfo": media})
}

func seerrTV(w http.ResponseWriter, r *http.Request) {
	id, ok := seerrTMDBID(r)
	show, found := findShow(id)
	if !ok || !found {
		seerrError(w, http.StatusNotFound, "Series not found.")
		return
	}
	seasons := []map[string]any{}
	for _, season := range show.Seasons {
		seasons = append(seasons, map[string]any{"id": int64(show.TmdbID)*1000 + int64(season.SeasonNumber),
			"seasonNumber": season.SeasonNumber, "name": season.Name, "episodeCount": season.EpisodeCount, "airDate": season.AirDate})
	}
	var media any
	for _, row := range seerrMovieTVRows() {
		if row.MediaType == mediaTypeTV && row.TmdbID == id {
			media = seerrMediaView(row, true)
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": show.TmdbID, "mediaType": "tv", "name": show.Name,
		"originalName": show.Name, "overview": show.Overview, "posterPath": show.PosterPath,
		"firstAirDate": show.FirstAirDate, "seasons": seasons, "mediaInfo": media})
}

func seerrSeason(w http.ResponseWriter, r *http.Request) {
	id, ok := seerrTMDBID(r)
	number, err := strconv.Atoi(chi.URLParam(r, "season"))
	show, found := findShow(id)
	if !ok || err != nil || number < 0 || !found {
		seerrError(w, http.StatusNotFound, "Season not found.")
		return
	}
	for _, season := range show.Seasons {
		if season.SeasonNumber != number {
			continue
		}
		episodes := []map[string]any{}
		for _, episode := range season.Episodes {
			episodes = append(episodes, map[string]any{"id": episode.ID, "name": episode.Name,
				"overview": episode.Overview, "airDate": episode.AirDate,
				"seasonNumber": season.SeasonNumber, "episodeNumber": episode.EpisodeNumber})
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": season.ID, "name": season.Name, "overview": "",
			"airDate": season.AirDate, "seasonNumber": season.SeasonNumber,
			"episodeCount": len(episodes), "episodes": episodes})
		return
	}
	seerrError(w, http.StatusNotFound, "Season not found.")
}

func seerrListUsers(w http.ResponseWriter, r *http.Request) {
	take, skip := queryInt(r, "take", 10), queryInt(r, "skip", 0)
	if take <= 0 {
		take = 10
	}
	if skip < 0 {
		skip = 0
	}
	users := allUsers()
	total := len(users)
	if skip > total {
		skip = total
	}
	end := skip + take
	if end > total {
		end = total
	}
	results := []map[string]any{}
	for _, u := range users[skip:end] {
		results = append(results, seerrUserView(u))
	}
	pages := 0
	if total > 0 {
		pages = (total + take - 1) / take
	}
	writeJSON(w, http.StatusOK, map[string]any{"pageInfo": map[string]int{
		"pages": pages, "pageSize": take, "results": total, "page": skip/take + 1}, "results": results})
}

func seerrGetUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	u := userByID(id)
	if err != nil || id <= 0 || u == nil {
		seerrError(w, http.StatusNotFound, "User not found.")
		return
	}
	writeJSON(w, http.StatusOK, seerrUserView(u))
}
