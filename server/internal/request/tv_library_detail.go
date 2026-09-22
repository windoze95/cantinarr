package request

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// TVLibraryDetail addresses a native library record without pretending its ID
// is a catalog ID. Ordinary shows keep their full catalog detail. Corrected
// series use the same title page with library-numbered seasons and live status.
type TVLibraryDetail struct {
	InstanceID string          `json:"instance_id"`
	SeriesID   int             `json:"series_id"`
	TmdbID     int             `json:"tmdb_id,omitempty"`
	Detail     map[string]any  `json:"detail,omitempty"`
	Matches    []TVMatch       `json:"matches,omitempty"`
	Status     *StatusResponse `json:"status,omitempty"`
	Revision   string          `json:"revision,omitempty"`
}

func (s *Service) TVLibraryDetail(userID int64, instanceID string, seriesID int) (*TVLibraryDetail, error) {
	if strings.TrimSpace(instanceID) == "" || seriesID <= 0 {
		return nil, ErrArrInstanceInvalid
	}
	client, resolvedID, err := s.resolveSonarr(userID, instanceID)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, ErrArrInstanceInvalid
	}
	series, err := client.GetSeries(seriesID)
	if err != nil {
		return nil, tvMatchFailure("tv_metadata_unavailable")
	}
	if series == nil || series.ID != seriesID {
		return nil, ErrTitleNotAvailable
	}

	s.tvMatchMu.Lock()
	defer s.tvMatchMu.Unlock()
	corrected, candidates, err := s.importTVMatches(series)
	if err != nil {
		return nil, tvMatchFailure("tv_metadata_unavailable")
	}
	out := &TVLibraryDetail{InstanceID: resolvedID, SeriesID: seriesID}
	isAdmin := s.userIsAdmin(userID)
	if !corrected && series.TmdbID > 0 {
		if err = s.checkContentPolicy(userID, isAdmin, "tv", series.TmdbID); err != nil {
			return nil, err
		}
		out.TmdbID = series.TmdbID
	} else {
		if err := s.populateTVLibraryDetail(userID, isAdmin, client, series, candidates, out); err != nil {
			return nil, err
		}
	}
	// Revoke during any provider read means no metadata leaves this method.
	if _, _, err = s.resolveSonarr(userID, resolvedID); err != nil {
		return nil, err
	}
	return out, nil
}

// Browsing starts with the native series, not with the subset of seasons that
// currently have a working request match. No unresolved season is hidden or
// assigned a guessed catalog identity. Caller holds tvMatchMu.
func (s *Service) populateTVLibraryDetail(userID int64, isAdmin bool, client *sonarr.Client, series *sonarr.Series, candidates []TVMatch, out *TVLibraryDetail) error {
	owners := map[int][]TVMatch{}
	for _, match := range candidates {
		for _, target := range match.SeasonMap {
			owners[target] = append(owners[target], match)
		}
	}
	var numbers []int
	for _, season := range series.Seasons {
		if season.SeasonNumber > 0 {
			numbers = append(numbers, season.SeasonNumber)
		}
	}
	numbers = normalizeSeasonNumbers(numbers)
	policy, err := s.contentPolicyFor(userID, isAdmin)
	if err != nil {
		return err
	}
	if policy != nil {
		// Native parent artwork/overview can identify any of its stories. A
		// paused request match is still rateable; an unknown/ambiguous owner is
		// not. Neither a broken match nor a failed rating read bypasses limits.
		if len(numbers) == 0 {
			return ErrContentPolicyUnavailable
		}
		checked := map[int]bool{}
		for _, n := range numbers {
			if len(owners[n]) != 1 {
				return ErrContentPolicyUnavailable
			}
			id := owners[n][0].TmdbID
			if !checked[id] {
				if err := s.checkContentPolicy(userID, isAdmin, "tv", id); err != nil {
					return err
				}
				checked[id] = true
			}
		}
	}
	episodes, episodeErr := client.GetAllEpisodes(series.ID)
	_, completion := sonarr.SeriesCompletion(episodes, time.Now())
	monitored := map[int]bool{}
	for _, ep := range episodes {
		monitored[ep.SeasonNumber] = monitored[ep.SeasonNumber] || ep.Monitored
	}
	known := episodeErr == nil && len(numbers) > 0
	out.Status = &StatusResponse{StatusKnown: &known}
	if !known {
		out.Status.StatusUnknownReason = "library_unavailable"
	}
	rows := map[int]SeasonStatus{}
	metadata := map[int]map[string]any{}
	for _, native := range series.Seasons {
		n := native.SeasonNumber
		if n <= 0 {
			continue
		}
		c := completion[n]
		if episodeErr != nil && native.Statistics != nil {
			c = sonarr.Completion{Files: native.Statistics.EpisodeFileCount, Aired: native.Statistics.TotalEpisodeCount}
		}
		status, progress := statusFromCompletion(c, series.Monitored && (native.Monitored || monitored[n]))
		row := SeasonStatus{SeasonNumber: n, Status: status, Progress: progress, EpisodeFileCount: c.Files, EpisodeCount: c.Aired, StatusKnown: &known}
		blockLibrarySeason(&row, "tv_seasons_unmapped")
		rows[n] = row
		metadata[n] = map[string]any{"id": n, "season_number": n, "name": fmt.Sprintf("Season %d", n)}
		if native.Statistics != nil && native.Statistics.TotalEpisodeCount > 0 {
			metadata[n]["episode_count"] = native.Statistics.TotalEpisodeCount
		}
	}
	sources := map[int]tvLibrarySource{}
	verified := map[int]*TVMatch{}
	for _, n := range numbers {
		row := rows[n]
		if len(owners[n]) > 1 {
			blockLibrarySeason(&row, "tv_match_ambiguous")
		} else if len(owners[n]) == 1 {
			owner := owners[n][0]
			read, exists := sources[owner.TmdbID]
			if !exists {
				read = s.readTVLibrarySource(userID, out.InstanceID, series, owner)
				if read.reason == "" {
					out.Status.Delivery = append(out.Status.Delivery, read.status.Delivery...)
				}
				sources[owner.TmdbID] = read
			}
			if read.reason != "" {
				blockLibrarySeason(&row, read.reason)
			} else {
				m := read.status.Match
				for source, target := range m.SeasonMap {
					if target != n {
						continue
					}
					metadata[n]["name"], metadata[n]["episode_count"] = m.Title, read.counts[source]
					if verified[m.TmdbID] == nil {
						copy := *m
						copy.SeasonMap = map[int]int{}
						verified[m.TmdbID] = &copy
					}
					verified[m.TmdbID].SeasonMap[source] = n
					for _, status := range read.status.Seasons {
						if status.SeasonNumber == source {
							row = status
							row.SeasonNumber, row.StatusKnown = n, read.status.StatusKnown
						}
					}
					if row.StatusKnown == nil || !*row.StatusKnown {
						blockLibrarySeason(&row, "library_unavailable")
					}
				}
			}
		}
		out.Status.Seasons = append(out.Status.Seasons, row)
	}
	for _, match := range verified {
		out.Matches = append(out.Matches, *match)
	}
	sort.Slice(out.Matches, func(i, j int) bool { return out.Matches[i].TmdbID < out.Matches[j].TmdbID })
	seasons := []map[string]any{}
	for _, n := range numbers {
		seasons = append(seasons, metadata[n])
	}
	out.Status.Status = librarySeasonStatus(out.Status.Seasons)
	out.Detail = map[string]any{"id": 0, "name": series.Title, "overview": series.Overview,
		"seasons": seasons, "number_of_seasons": len(seasons),
		"external_ids": map[string]any{"tvdb_id": series.TvdbID, "imdb_id": series.ImdbID}}
	if first, err := time.Parse(time.RFC3339, series.FirstAired); err == nil {
		out.Detail["first_air_date"] = first.Format("2006-01-02")
	}
	for _, image := range series.Images {
		u, err := url.Parse(image.RemoteURL)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
			continue
		}
		switch image.CoverType {
		case "poster":
			out.Detail["poster_path"] = image.RemoteURL
		case "fanart":
			out.Detail["backdrop_path"] = image.RemoteURL
		}
	}
	// Include unresolved configuration and native seasons too: repairing a
	// match or adding a season invalidates a previously displayed selection.
	proof, _ := json.Marshal([]any{out.InstanceID, series.ID, series.TvdbID, numbers, candidates, out.Matches})
	out.Revision = fmt.Sprintf("%x", sha256.Sum256(proof))
	return nil
}

type tvLibrarySource struct {
	status *StatusResponse
	reason string
	counts map[int]int
}

func (s *Service) readTVLibrarySource(userID int64, instanceID string, series *sonarr.Series, owner TVMatch) tvLibrarySource {
	status, err := s.userTVStatus(userID, owner.TmdbID, instanceID)
	if err != nil {
		return tvLibrarySource{reason: "library_unavailable"}
	}
	m := status.Match
	if m == nil || m.State != "resolved" {
		reason := status.StatusUnknownReason
		if reason == "" {
			reason = "tv_match_ambiguous"
		}
		return tvLibrarySource{reason: reason}
	}
	if m.TVDBID != series.TvdbID || m.SeriesID != series.ID || m.Revision != owner.Revision {
		return tvLibrarySource{reason: "tv_match_ambiguous"}
	}
	source, err := s.tvSource(owner.TmdbID)
	if err != nil {
		return tvLibrarySource{reason: "tv_metadata_unavailable"}
	}
	if validateTVSeasons(m.SeasonMap, sourceSeasonNumbers(source), series.Seasons) != nil {
		return tvLibrarySource{reason: "tv_seasons_unmapped"}
	}
	read := tvLibrarySource{status: status, counts: map[int]int{}}
	for _, season := range source.Seasons {
		read.counts[season.SeasonNumber] = season.EpisodeCount
	}
	return read
}

func blockLibrarySeason(row *SeasonStatus, reason string) {
	row.RequestBlockedReason = reason
	switch reason {
	case "tv_match_paused":
		row.RequestBlockedMessage = "Matching is paused for this season. An admin can review its TV match."
	case "tv_match_ambiguous":
		row.RequestBlockedMessage = "This season has conflicting TV matches. An admin can review them."
	case "tv_seasons_unmapped":
		row.RequestBlockedMessage = "This season needs a TV match before it can be requested."
	case "tv_metadata_unavailable":
		row.RequestBlockedMessage = "Could not verify this season's catalog details. Retry to check again."
	default:
		row.RequestBlockedMessage = "Could not verify this season's request status. Retry to check again."
	}
}

func librarySeasonStatus(seasons []SeasonStatus) string {
	if len(seasons) == 0 {
		return StatusUnavailable
	}
	first, same, accepted := seasons[0].Status, true, false
	for _, season := range seasons {
		same = same && season.Status == first
		accepted = accepted || (season.Status != StatusUnavailable && season.Status != StatusDenied)
	}
	if same {
		return first
	}
	// Mixed accepted/missing scopes must keep Request More and the individual
	// rows available. A pending story never marks its siblings pending.
	if accepted {
		return StatusPartial
	}
	return StatusUnavailable
}

// This is an internal precondition, never decoded from a public create body.
type tvLibraryScope struct {
	seriesID      int
	revision      string
	targetSeasons []int
	pilot         bool
}

type TVLibraryRequest struct {
	InstanceID       string `json:"instance_id"`
	SeriesID         int    `json:"series_id"`
	Revision         string `json:"revision"`
	Seasons          []int  `json:"seasons,omitempty"`
	SeasonScope      string `json:"season_scope,omitempty"`
	QualityProfileID int    `json:"quality_profile_id,omitempty"`
}

type TVLibraryRequestResult struct {
	Success         bool   `json:"success"`
	AcceptedSeasons []int  `json:"accepted_seasons"`
	SkippedSeasons  []int  `json:"skipped_seasons,omitempty"`
	Error           string `json:"error,omitempty"`
}

// Apply season-choice policy once, in the coordinates the user can see. Each
// source request then uses the existing approval, quota and durable dispatch
// path. Multi-source submission is explicitly not an atomic transaction.
func (s *Service) RequestTVLibrary(userID int64, req TVLibraryRequest) (*TVLibraryRequestResult, error) {
	detail, err := s.TVLibraryDetail(userID, req.InstanceID, req.SeriesID)
	if err != nil {
		return nil, err
	}
	if req.Revision == "" || detail.Revision != req.Revision || detail.Status == nil || detail.Status.StatusKnown == nil || !*detail.Status.StatusKnown {
		return nil, ErrTVMatchStale
	}
	eff, err := s.effectiveSettings(userID, s.userIsAdmin(userID))
	if err != nil {
		return nil, err
	}
	var all []int
	blocked := map[int]string{}
	for _, season := range detail.Status.Seasons {
		all = append(all, season.SeasonNumber)
		if season.RequestBlockedReason != "" {
			blocked[season.SeasonNumber] = season.RequestBlockedReason
		} else if season.StatusKnown != nil && !*season.StatusKnown {
			blocked[season.SeasonNumber] = "tv_metadata_unavailable"
		}
	}
	all = normalizeSeasonNumbers(all)
	if len(all) == 0 {
		return nil, tvMatchFailure("tv_seasons_unmapped")
	}
	selected, scope := all, eff.SeasonScope
	if eff.AllowSeasonChoice && validSeasonScope(req.SeasonScope) {
		scope = req.SeasonScope
	}
	pilot := scope == SeasonScopePilot
	explicit := eff.AllowSeasonChoice && len(req.Seasons) > 0
	if explicit {
		for _, n := range req.Seasons {
			if n <= 0 {
				return nil, tvMatchFailure("tv_seasons_unmapped")
			}
		}
		selected, pilot = normalizeSeasonNumbers(req.Seasons), false
	} else {
		switch scope {
		case SeasonScopeFirst, SeasonScopePilot:
			selected = all[:1]
		case SeasonScopeLatest:
			selected = all[len(all)-1:]
		}
	}
	result := &TVLibraryRequestResult{AcceptedSeasons: []int{}}
	var safe []int
	for _, n := range selected {
		i := sort.SearchInts(all, n)
		if i >= len(all) || all[i] != n {
			return nil, tvMatchFailure("tv_seasons_unmapped")
		}
		if reason := blocked[n]; reason != "" {
			if !explicit && scope == SeasonScopeAll {
				result.SkippedSeasons = append(result.SkippedSeasons, n)
				continue
			}
			return nil, tvMatchFailure(reason)
		}
		safe = append(safe, n)
	}
	if len(safe) == 0 {
		return nil, tvMatchFailure("tv_seasons_unmapped")
	}
	selected = safe
	// Matches are sorted by source ID, not parent season. Sort groups by their
	// first target so any partial success is predictable to the user.
	sort.Slice(detail.Matches, func(i, j int) bool { return firstTarget(detail.Matches[i]) < firstTarget(detail.Matches[j]) })
	for _, match := range detail.Matches {
		var sources, targets []int
		for source, target := range match.SeasonMap {
			for _, n := range selected {
				if n == target {
					sources = append(sources, source)
					targets = append(targets, target)
				}
			}
		}
		if len(sources) == 0 {
			continue
		}
		sources, targets = normalizeSeasonNumbers(sources), normalizeSeasonNumbers(targets)
		_, err = s.CreateMediaRequest(userID, &CreateRequest{
			TmdbID: match.TmdbID, MediaType: "tv", InstanceID: detail.InstanceID,
			Seasons: sources, QualityProfileID: req.QualityProfileID,
			tvLibraryScope: &tvLibraryScope{seriesID: req.SeriesID, revision: match.Revision, targetSeasons: targets, pilot: pilot},
		})
		if err != nil {
			if len(result.AcceptedSeasons) == 0 {
				return nil, err
			}
			result.Error = "Some seasons were accepted, but the remaining seasons could not be requested. Review the refreshed season statuses before retrying."
			return result, nil
		}
		result.AcceptedSeasons = append(result.AcceptedSeasons, targets...)
	}
	result.Success = true
	return result, nil
}

func firstTarget(m TVMatch) int {
	n := int(^uint(0) >> 1)
	for _, target := range m.SeasonMap {
		if target < n {
			n = target
		}
	}
	return n
}

func (h *Handler) GetTVLibrary(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	id, err := strconv.Atoi(r.URL.Query().Get("series_id"))
	instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if err != nil || id <= 0 || instanceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "instance_id and a positive series_id are required"})
		return
	}
	out, err := h.service.TVLibraryDetail(claims.UserID, instanceID, id)
	tvMatchReply(w, out, err)
}

func (h *Handler) CreateTVLibraryRequest(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req TVLibraryRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil || req.SeriesID <= 0 || strings.TrimSpace(req.InstanceID) == "" || req.Revision == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid TV library request"})
		return
	}
	out, err := h.service.RequestTVLibrary(claims.UserID, req)
	if err != nil && writeQuotaError(w, err) {
		return
	}
	if err != nil && requestErrorStatus(err) == http.StatusInternalServerError {
		err = errors.New("Could not request these seasons. Retry after checking their status.")
	}
	tvMatchReply(w, out, err)
}
