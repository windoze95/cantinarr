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
	corrected, _, err := s.importTVMatches(series)
	if err != nil {
		return nil, tvMatchFailure("tv_metadata_unavailable")
	}
	out := &TVLibraryDetail{InstanceID: resolvedID, SeriesID: seriesID}
	isAdmin := s.userIsAdmin(userID)
	if !corrected {
		if series.TmdbID <= 0 {
			return nil, tvMatchFailure("tv_match_ambiguous")
		}
		if err = s.checkContentPolicy(userID, isAdmin, "tv", series.TmdbID); err != nil {
			return nil, err
		}
		out.TmdbID = series.TmdbID
	} else {
		var evidence []sonarr.ImportedEpisode
		for _, season := range series.Seasons {
			if season.SeasonNumber > 0 {
				evidence = append(evidence, sonarr.ImportedEpisode{SeasonNumber: season.SeasonNumber})
			}
		}
		titles, err := s.resolveTVImports(client, series, evidence)
		if err != nil {
			return nil, libraryReadError(err)
		}
		if len(titles) == 0 {
			return nil, tvMatchFailure("tv_seasons_unmapped")
		}
		known := true
		out.Status = &StatusResponse{StatusKnown: &known}
		seasons := []map[string]any{}
		for _, title := range titles {
			// A parent's overview/artwork can name any of its stories. Do not
			// expose that parent to a child allowed only a subset of its seasons.
			if err = s.checkContentPolicy(userID, isAdmin, "tv", title.TmdbID); err != nil {
				return nil, err
			}
			status, err := s.userTVStatus(userID, title.TmdbID, resolvedID)
			if err != nil {
				return nil, libraryReadError(err)
			}
			m := status.Match
			if m == nil || m.State != "resolved" || m.TVDBID != series.TvdbID || m.SeriesID != seriesID {
				return nil, tvMatchFailure("tv_match_ambiguous")
			}
			source, err := s.tvSource(title.TmdbID)
			if err != nil {
				return nil, err
			}
			out.Matches = append(out.Matches, *m)
			for _, season := range source.Seasons {
				target := m.SeasonMap[season.SeasonNumber]
				if target <= 0 {
					continue
				}
				seasons = append(seasons, map[string]any{
					"id": target, "season_number": target, "name": source.Name,
					"episode_count": season.EpisodeCount,
				})
			}
			for _, season := range status.Seasons {
				season.SeasonNumber = m.SeasonMap[season.SeasonNumber]
				if season.SeasonNumber > 0 {
					out.Status.Seasons = append(out.Status.Seasons, season)
				}
			}
			if status.StatusKnown == nil || !*status.StatusKnown {
				known = false
				out.Status.StatusUnknownReason = status.StatusUnknownReason
			}
			out.Status.Delivery = append(out.Status.Delivery, status.Delivery...)
		}
		sort.Slice(seasons, func(i, j int) bool { return seasons[i]["season_number"].(int) < seasons[j]["season_number"].(int) })
		sort.Slice(out.Status.Seasons, func(i, j int) bool { return out.Status.Seasons[i].SeasonNumber < out.Status.Seasons[j].SeasonNumber })
		out.Status.Status = librarySeasonStatus(out.Status.Seasons)
		out.Detail = map[string]any{
			"id": 0, "name": series.Title, "overview": series.Overview,
			"seasons": seasons, "number_of_seasons": len(seasons),
			"external_ids": map[string]any{"tvdb_id": series.TvdbID, "imdb_id": series.ImdbID},
		}
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
		proof, _ := json.Marshal([]any{resolvedID, seriesID, series.TvdbID, out.Matches})
		out.Revision = fmt.Sprintf("%x", sha256.Sum256(proof))
	}
	// Revoke during any provider read means no metadata leaves this method.
	if _, _, err = s.resolveSonarr(userID, resolvedID); err != nil {
		return nil, err
	}
	return out, nil
}

func libraryReadError(err error) error {
	var matchError *tvMatchError
	if errors.As(err, &matchError) {
		return matchError
	}
	if errors.Is(err, ErrArrInstanceForbidden) || errors.Is(err, ErrArrInstanceInvalid) || errors.Is(err, ErrTVMatchStale) {
		return err
	}
	return tvMatchFailure("tv_metadata_unavailable")
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
	for _, season := range detail.Status.Seasons {
		all = append(all, season.SeasonNumber)
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
	if eff.AllowSeasonChoice && len(req.Seasons) > 0 {
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
	for _, n := range selected {
		i := sort.SearchInts(all, n)
		if i >= len(all) || all[i] != n {
			return nil, tvMatchFailure("tv_seasons_unmapped")
		}
	}
	result := &TVLibraryRequestResult{AcceptedSeasons: []int{}}
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
