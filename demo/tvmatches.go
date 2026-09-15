// tvmatches.go — TV matching corrections: which Sonarr series (and which of
// its seasons) a TMDB show actually maps to, and the repair that re-sends an
// already-delivered request to the corrected target.
//
// A match has a provenance: `default` is the automatic title/TVDB match,
// `bundled` is a correction that shipped with the server, `custom` is one an
// admin saved here. A `revision` stamps the state an edit was made against,
// so a stale form is refused instead of overwriting a newer decision.
//
// Prefix: tvm…
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// tvmMatch is one show's resolved target.
type tvmMatch struct {
	TmdbID      int
	TvdbID      int
	SeriesID    int
	Title       string
	TargetTitle string
	SeasonMap   map[int]int
	Provenance  string
	State       string
	Message     string
	// Rev increments on every save, and is what the revision string is
	// derived from.
	Rev int
}

var (
	tvmMu sync.Mutex

	// tvmMatches is tmdbID -> the stored correction. Shows with no entry
	// resolve automatically from their seeded TVDB id.
	tvmMatches = map[int]*tvmMatch{}

	// tvmRepaired records requests already repaired this session, so the
	// preview reports the follow-up request instead of offering it twice.
	tvmRepaired = map[int]int{}
)

func init() {
	// One bundled correction: an anthology whose Sonarr series splits the
	// TMDB seasons differently, which is exactly the case the feature exists
	// for. Seeded as bundled so the screen opens on a real non-default row.
	tvmMatches[90005] = &tvmMatch{
		TmdbID: 90005, TvdbID: 390005, SeriesID: 5,
		Title:       "Tales from the Public Domain",
		TargetTitle: "Tales from the Public Domain (2022)",
		SeasonMap:   map[int]int{1: 1, 2: 3},
		Provenance:  "bundled",
		State:       "resolved",
		Message:     "Sonarr numbers the 2023 specials as season 2, so TMDB season 2 maps to Sonarr season 3.",
	}
}

// tvmRevision is the opaque stamp an edit must echo back.
func tvmRevision(m *tvmMatch) string {
	return fmt.Sprintf("%d:%d", m.TmdbID, m.Rev)
}

// tvmResolve returns the stored correction, or the automatic match derived
// from the show's own TVDB id. Caller holds tvmMu.
func tvmResolve(tmdbID int) *tvmMatch {
	if m := tvmMatches[tmdbID]; m != nil {
		return m
	}
	show, ok := findShow(tmdbID)
	if !ok {
		return nil
	}
	seasonMap := map[int]int{}
	for _, s := range show.Seasons {
		seasonMap[s.SeasonNumber] = s.SeasonNumber
	}
	return &tvmMatch{
		TmdbID: show.TmdbID, TvdbID: show.TvdbID, SeriesID: tvmSeriesID(show.TmdbID),
		Title: show.Name, TargetTitle: fmt.Sprintf("%s (%d)", show.Name, show.Year()),
		SeasonMap: seasonMap, Provenance: "default", State: "resolved",
	}
}

// tvmSeriesID is the Sonarr series id a show would land on. The fake Sonarr
// numbers its library from 1 in seed order.
func tvmSeriesID(tmdbID int) int { return tmdbID - 90000 }

func tvmMatchView(m *tvmMatch) map[string]any {
	seasonMap := map[string]int{}
	for source, target := range m.SeasonMap {
		seasonMap[strconv.Itoa(source)] = target
	}
	out := map[string]any{
		"tmdb_id":      m.TmdbID,
		"title":        m.Title,
		"tvdb_id":      m.TvdbID,
		"target_title": m.TargetTitle,
		"season_map":   seasonMap,
		"provenance":   m.Provenance,
		"revision":     tvmRevision(m),
		"state":        m.State,
	}
	if m.Message != "" {
		out["message"] = m.Message
	}
	if m.SeriesID > 0 {
		out["series_id"] = m.SeriesID
	}
	return out
}

func registerTVMatches(r chi.Router) {
	admin := r.With(requireAdmin)
	admin.Get("/admin/tv-matches", tvmListHandler)
	// Static segment first: it must win over /tv-matches/{tmdb_id}.
	admin.Get("/admin/tv-matches/candidates", tvmCandidatesHandler)
	admin.Get("/admin/tv-matches/{tmdb_id}", tvmDetailHandler)
	admin.Put("/admin/tv-matches/{tmdb_id}", tvmSaveHandler)
	admin.Delete("/admin/tv-matches/{tmdb_id}", tvmSaveHandler)
	admin.Get("/admin/tv-matches/{tmdb_id}/repairs", tvmRepairsHandler)
	admin.Post("/admin/requests/{id}/repair-tv-match", tvmRepairHandler)
}

// tvmListHandler lists every show carrying a non-default match, which is what
// the settings screen shows: the corrections in force, not the whole catalog.
func tvmListHandler(w http.ResponseWriter, _ *http.Request) {
	tvmMu.Lock()
	defer tvmMu.Unlock()
	ids := []int{}
	for id := range tvmMatches {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := []map[string]any{}
	for _, id := range ids {
		out = append(out, tvmMatchView(tvmMatches[id]))
	}
	writeJSON(w, http.StatusOK, out)
}

func tvmPathID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "tmdb_id"))
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid TMDB id")
		return 0, false
	}
	return id, true
}

// tvmDetailHandler answers the editor: the current match, the show's own
// seasons, and the seasons the Sonarr series offers.
func tvmDetailHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := tvmPathID(w, r)
	if !ok {
		return
	}
	tvmMu.Lock()
	defer tvmMu.Unlock()
	writeJSON(w, http.StatusOK, tvmDetailView(id, r.URL.Query().Get("instance_id")))
}

// tvmDetailView builds the editor payload. Caller holds tvmMu.
func tvmDetailView(tmdbID int, instanceID string) map[string]any {
	m := tvmResolve(tmdbID)
	if m == nil {
		return map[string]any{
			"match":          nil,
			"source_seasons": []any{},
			"target_seasons": []any{},
		}
	}
	sourceSeasons := []map[string]any{}
	if show, ok := findShow(tmdbID); ok {
		for _, s := range show.Seasons {
			sourceSeasons = append(sourceSeasons, map[string]any{
				"season_number": s.SeasonNumber,
				"name":          s.Name,
			})
		}
	}
	// The target is the Sonarr series: the seasons it actually carries. The
	// seeded correction's target has one more than the show does, which is
	// why it needed correcting.
	targetCount := len(sourceSeasons)
	for _, target := range m.SeasonMap {
		if target > targetCount {
			targetCount = target
		}
	}
	targetSeasons := []map[string]any{}
	for n := 1; n <= targetCount; n++ {
		targetSeasons = append(targetSeasons, map[string]any{"seasonNumber": n})
	}
	if instanceID == "" {
		instanceID = instSonarr
	}
	return map[string]any{
		"match":          tvmMatchView(m),
		"source_seasons": sourceSeasons,
		"target_seasons": targetSeasons,
		"instance_id":    instanceID,
	}
}

// tvmCandidatesHandler is the Sonarr series lookup behind the editor's
// "choose a different series" search, in Sonarr's own camelCase shape.
func tvmCandidatesHandler(w http.ResponseWriter, r *http.Request) {
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	out := []map[string]any{}
	for _, show := range demoShows {
		if query != "" && !strings.Contains(strings.ToLower(show.Name), query) {
			continue
		}
		seasons := []map[string]any{}
		for _, s := range show.Seasons {
			seasons = append(seasons, map[string]any{"seasonNumber": s.SeasonNumber})
		}
		out = append(out, map[string]any{
			"tvdbId":  show.TvdbID,
			"title":   show.Name,
			"year":    show.Year(),
			"seasons": seasons,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// tvmSaveHandler stores a correction (PUT) or drops back to the automatic
// match (DELETE). A revision that no longer matches is refused: somebody else
// decided this while the form was open.
func tvmSaveHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := tvmPathID(w, r)
	if !ok {
		return
	}
	var edit struct {
		Revision   string         `json:"revision"`
		Mode       string         `json:"mode"`
		TvdbID     int            `json:"tvdb_id"`
		SeasonMap  map[string]int `json:"season_map"`
		InstanceID string         `json:"instance_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&edit) != nil {
		writeErr(w, http.StatusBadRequest, "invalid correction")
		return
	}
	if r.Method == http.MethodDelete {
		edit.Mode = "default"
	}

	tvmMu.Lock()
	defer tvmMu.Unlock()
	current := tvmResolve(id)
	if current == nil {
		writeErr(w, http.StatusNotFound, "unknown show")
		return
	}
	if edit.Revision != "" && edit.Revision != tvmRevision(current) {
		writeErr(w, http.StatusConflict,
			"this match changed while you were editing it. Reload and try again.")
		return
	}

	if edit.Mode == "default" {
		delete(tvmMatches, id)
		writeJSON(w, http.StatusOK, tvmDetailView(id, edit.InstanceID))
		return
	}

	seasonMap := map[int]int{}
	for raw, target := range edit.SeasonMap {
		source, err := strconv.Atoi(raw)
		if err != nil || source < 1 || target < 1 {
			writeErr(w, http.StatusBadRequest, "invalid season mapping")
			return
		}
		seasonMap[source] = target
	}
	if len(seasonMap) == 0 {
		for source, target := range current.SeasonMap {
			seasonMap[source] = target
		}
	}
	tvdbID := edit.TvdbID
	title, targetTitle := current.Title, current.TargetTitle
	seriesID := current.SeriesID
	if tvdbID == 0 {
		tvdbID = current.TvdbID
	} else {
		for _, show := range demoShows {
			if show.TvdbID == tvdbID {
				targetTitle = fmt.Sprintf("%s (%d)", show.Name, show.Year())
				seriesID = tvmSeriesID(show.TmdbID)
				break
			}
		}
	}
	if show, ok := findShow(id); ok {
		title = show.Name
	}
	tvmMatches[id] = &tvmMatch{
		TmdbID: id, TvdbID: tvdbID, SeriesID: seriesID,
		Title: title, TargetTitle: targetTitle, SeasonMap: seasonMap,
		Provenance: "custom", State: "resolved", Rev: current.Rev + 1,
	}
	writeJSON(w, http.StatusOK, tvmDetailView(id, edit.InstanceID))
}

// ─── Repairs ────────────────────────────────────────────

// tvmRepairsHandler lists the already-delivered requests for this show that a
// corrected match would now route elsewhere. Each one is a decision an admin
// makes per request — the previous files are kept either way.
func tvmRepairsHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := tvmPathID(w, r)
	if !ok {
		return
	}
	tvmMu.Lock()
	defer tvmMu.Unlock()
	match := tvmResolve(id)
	out := []map[string]any{}
	if match == nil || match.Provenance == "default" {
		writeJSON(w, http.StatusOK, out)
		return
	}
	for _, req := range tvmDeliveredRequests(id) {
		instanceID := req.InstanceID
		if instanceID == "" {
			instanceID = instSonarr
		}
		instanceName := instanceID
		if inst := instanceByID(instanceID); inst != nil {
			instanceName = inst.Name
		}
		preview := map[string]any{
			"request_id":            req.ID,
			"title":                 req.Title,
			"status":                req.Status,
			"instance_id":           instanceID,
			"instance_name":         instanceName,
			"recorded_tvdb_id":      match.TvdbID,
			"recorded_target_known": true,
			"revision":              tvmRevision(match),
			"requires_approval":     false,
			"can_repair":            tvmRepaired[int(req.ID)] == 0,
			"message": "The previous target and files will be preserved. " +
				"This creates a linked corrective request.",
		}
		if repaired := tvmRepaired[int(req.ID)]; repaired != 0 {
			preview["repair_request_id"] = repaired
			preview["message"] = "Already repaired by a linked corrective request."
		}
		out = append(out, preview)
	}
	writeJSON(w, http.StatusOK, out)
}

// tvmRepairHandler creates the corrective request. The revision must match
// the match the admin was looking at.
func tvmRepairHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid request ID")
		return
	}
	var body struct {
		Revision string `json:"revision"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil || body.Revision == "" {
		writeErr(w, http.StatusBadRequest, "revision required")
		return
	}
	req := tvmRequestByID(int64(id))
	if req == nil {
		writeErr(w, http.StatusNotFound, "request not found")
		return
	}
	tvmMu.Lock()
	defer tvmMu.Unlock()
	match := tvmResolve(req.TmdbID)
	if match == nil || body.Revision != tvmRevision(match) {
		writeErr(w, http.StatusConflict,
			"this match changed while you were editing it. Reload and try again.")
		return
	}
	if existing := tvmRepaired[id]; existing != 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "status": statusRequested,
			"title": req.Title, "request_id": existing,
			"instance_id": req.InstanceID,
		})
		return
	}
	repaired := tvmCloneForRepair(req)
	if repaired == 0 {
		writeErr(w, http.StatusConflict, "this request cannot be repaired")
		return
	}
	tvmRepaired[id] = repaired
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "status": statusRequested,
		"title": req.Title, "request_id": repaired,
		"match":       tvmMatchView(match),
		"instance_id": req.InstanceID,
	})
}

// ─── Request-log access ─────────────────────────────────
//
// Kept here rather than in requests.go so this domain owns its own reads of
// the shared log (contract: no edits to another domain's file).

// tvmDeliveredRequests lists this show's delivered TV requests, newest first.
func tvmDeliveredRequests(tmdbID int) []*reqLogRow {
	reqMu.Lock()
	defer reqMu.Unlock()
	out := []*reqLogRow{}
	for i := len(reqLog) - 1; i >= 0; i-- {
		row := reqLog[i]
		if row.MediaType != mediaTypeTV || row.TmdbID != tmdbID {
			continue
		}
		switch row.Status {
		case statusAvailable, statusDownloading, statusPartial, statusRequested:
			copied := *row
			out = append(out, &copied)
		}
	}
	return out
}

func tvmRequestByID(id int64) *reqLogRow {
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

// tvmCloneForRepair appends the linked corrective request. The original row
// is untouched — its files and history are what the correction preserves.
func tvmCloneForRepair(src *reqLogRow) int {
	reqMu.Lock()
	defer reqMu.Unlock()
	reqNextID++
	row := *src
	row.ID = reqNextID
	row.Status = statusRequested
	row.RequestedAt = time.Now()
	row.Waiters = nil
	reqLog = append(reqLog, &row)
	return int(row.ID)
}
