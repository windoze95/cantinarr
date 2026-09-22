package main

// Native TV-library navigation keeps Sonarr's series/season identity separate
// from TMDB identity. The seeded anthology demonstrates the split-season case:
// catalog season 2 is Sonarr season 3.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
)

func arrSNativeSeasonNumber(st *arrSonarrSeries, source int) int {
	if st != nil && st.TmdbID == 90005 && source == 2 {
		return 3
	}
	return source
}

func tvLibraryGetHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	arrEnsureSeeded()
	instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	seriesID, err := strconv.Atoi(r.URL.Query().Get("series_id"))
	if err != nil || seriesID <= 0 || instanceID == "" {
		writeErr(w, http.StatusBadRequest, "instance_id and a positive series_id are required")
		return
	}
	inst := instanceByID(instanceID)
	if inst == nil || inst.ServiceType != serviceSonarr {
		writeErr(w, http.StatusBadRequest, "invalid sonarr instance")
		return
	}
	if !userCanSeeInstance(u, instanceID) {
		writeErr(w, http.StatusForbidden, "sonarr instance is not available to you")
		return
	}
	arrMu.Lock()
	st := arrSSeriesByIDLocked(seriesID)
	var tmdbID int
	if st != nil {
		tmdbID = st.TmdbID
	}
	arrMu.Unlock()
	if st == nil {
		writeErr(w, http.StatusNotFound, "title not available")
		return
	}
	if !cpAllowsTmdb(u, mediaTypeTV, tmdbID) {
		writeErr(w, http.StatusNotFound, "title not available")
		return
	}

	tvmMu.Lock()
	match := tvmResolve(tmdbID)
	corrected := match != nil && match.Provenance != "default"
	if !corrected {
		tvmMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"instance_id": instanceID, "series_id": seriesID, "tmdb_id": tmdbID,
		})
		return
	}
	matchView := tvmMatchView(match)
	revision := "library:" + tvmRevision(match)
	seasonMap := map[int]int{}
	for source, target := range match.SeasonMap {
		seasonMap[source] = target
	}
	tvmMu.Unlock()

	show, ok := findShow(tmdbID)
	if !ok {
		writeErr(w, http.StatusNotFound, "title not available")
		return
	}
	seasonBySource := map[int]DemoSeason{}
	for _, season := range show.Seasons {
		seasonBySource[season.SeasonNumber] = season
	}
	targets := make([]int, 0, len(seasonMap))
	for _, target := range seasonMap {
		targets = append(targets, target)
	}
	sort.Ints(targets)
	seasons := make([]map[string]any, 0, len(targets))
	statuses := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		source := 0
		for candidate, mapped := range seasonMap {
			if mapped == target {
				source = candidate
				break
			}
		}
		season, found := seasonBySource[source]
		if !found {
			statuses = append(statuses, map[string]any{
				"season_number": target, "status": statusUnavailable, "progress": 0,
				"status_known": true, "request_blocked_reason": "tv_seasons_unmapped",
				"request_blocked_message": "This season needs a TV match before it can be requested.",
			})
			continue
		}
		seasons = append(seasons, map[string]any{
			"id": tmdbID*10 + target, "season_number": target,
			"name": "Season " + strconv.Itoa(target), "episode_count": season.EpisodeCount,
			"air_date": season.AirDate, "poster_path": season.PosterPath,
		})
		files := 0
		arrMu.Lock()
		for _, ep := range season.Episodes {
			if st.Files[ep.ID] != nil {
				files++
			}
		}
		arrMu.Unlock()
		status := statusUnavailable
		progress := 0.0
		if season.EpisodeCount > 0 {
			progress = float64(files) / float64(season.EpisodeCount)
		}
		switch {
		case files >= season.EpisodeCount && season.EpisodeCount > 0:
			status = statusAvailable
		case files > 0:
			status = statusPartial
		case st.SeasonMonitored[source]:
			status = statusRequested
		}
		if own := tvLibraryOwnSeasonStatus(u.ID, tmdbID, source); own != "" {
			status = own
		}
		statuses = append(statuses, map[string]any{
			"season_number": target, "episode_file_count": files,
			"episode_count": season.EpisodeCount, "status": status,
			"progress": progress, "status_known": true,
		})
	}
	overall := statusUnavailable
	if len(statuses) > 0 {
		overall, _ = statuses[0]["status"].(string)
		for _, row := range statuses[1:] {
			if row["status"] != overall {
				overall = statusPartial
				break
			}
		}
	}
	detail := map[string]any{
		"id": 0, "name": show.Name, "tagline": show.Tagline, "overview": show.Overview,
		"poster_path": show.PosterPath, "backdrop_path": show.BackdropPath,
		"vote_average": show.VoteAverage, "first_air_date": show.FirstAirDate,
		"status": show.Status, "number_of_seasons": len(seasons), "seasons": seasons,
		"genres": discGenreObjs(show.Genres), "credits": map[string]any{"cast": []any{}, "crew": []any{}},
		"external_ids": map[string]any{"tvdb_id": show.TvdbID, "imdb_id": nil},
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"instance_id": instanceID, "series_id": seriesID, "detail": detail,
		"matches": []any{matchView}, "revision": revision,
		"status": map[string]any{"status": overall, "status_known": true, "seasons": statuses},
	})
}

func tvLibraryOwnSeasonStatus(userID, tmdbID, sourceSeason int) string {
	reqMu.Lock()
	defer reqMu.Unlock()
	var latest *reqLogRow
	for _, row := range reqLog {
		if row.UserID != userID || row.MediaType != mediaTypeTV || row.TmdbID != tmdbID {
			continue
		}
		selected := reqSeasonsForScope(tmdbID, row.SeasonScope)
		if strings.HasPrefix(row.SeasonScope, "[") {
			_ = json.Unmarshal([]byte(row.SeasonScope), &selected)
		}
		found := false
		for _, n := range selected {
			found = found || n == sourceSeason
		}
		if found && (latest == nil || row.RequestedAt.After(latest.RequestedAt)) {
			latest = row
		}
	}
	if latest == nil {
		return ""
	}
	return latest.Status
}

func tvLibraryCreateHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	arrEnsureSeeded()
	var body struct {
		InstanceID       string `json:"instance_id"`
		SeriesID         int    `json:"series_id"`
		Revision         string `json:"revision"`
		Seasons          []int  `json:"seasons"`
		SeasonScope      string `json:"season_scope"`
		QualityProfileID int    `json:"quality_profile_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body) != nil ||
		body.InstanceID == "" || body.SeriesID <= 0 || body.Revision == "" {
		writeErr(w, http.StatusBadRequest, "invalid TV library request")
		return
	}
	if body.InstanceID != instSonarr || !userCanSeeInstance(u, body.InstanceID) {
		writeErr(w, http.StatusForbidden, "sonarr instance is not available to you")
		return
	}
	arrMu.Lock()
	st := arrSSeriesByIDLocked(body.SeriesID)
	arrMu.Unlock()
	if st == nil {
		writeErr(w, http.StatusNotFound, "title not available")
		return
	}
	tvmMu.Lock()
	match := tvmResolve(st.TmdbID)
	wantRevision := ""
	seasonMap := map[int]int{}
	if match != nil {
		wantRevision = "library:" + tvmRevision(match)
		for source, target := range match.SeasonMap {
			seasonMap[source] = target
		}
	}
	tvmMu.Unlock()
	if match == nil || body.Revision != wantRevision {
		writeErr(w, http.StatusConflict, "TV matching changed. Refresh this title and try again.")
		return
	}
	targets := append([]int{}, body.Seasons...)
	if len(targets) == 0 {
		for _, target := range seasonMap {
			targets = append(targets, target)
		}
		sort.Ints(targets)
		if body.SeasonScope == "first" || body.SeasonScope == "pilot" {
			targets = targets[:1]
		} else if body.SeasonScope == "latest" {
			targets = targets[len(targets)-1:]
		}
	}
	sources := make([]int, 0, len(targets))
	for _, target := range targets {
		found := false
		for source, mapped := range seasonMap {
			if mapped == target {
				sources = append(sources, source)
				found = true
				break
			}
		}
		if !found {
			writeErr(w, http.StatusBadRequest, "This season needs a TV match before it can be requested.")
			return
		}
	}
	request := reqCreateBody{TmdbID: st.TmdbID, MediaType: mediaTypeTV,
		InstanceID: body.InstanceID, Seasons: sources, SeasonScope: body.SeasonScope,
		QualityProfileID: body.QualityProfileID}
	if !reqChargeAllowance(w, u, &request) {
		return
	}
	recorder := httptest.NewRecorder()
	reqCreateMovieTV(recorder, u, &request)
	if recorder.Code < 200 || recorder.Code >= 300 {
		w.WriteHeader(recorder.Code)
		_, _ = w.Write(recorder.Body.Bytes())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "accepted_seasons": targets, "skipped_seasons": []int{},
	})
}
