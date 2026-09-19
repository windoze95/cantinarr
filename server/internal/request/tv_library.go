package request

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

type TVLibraryTitle struct {
	TmdbID int    `json:"tmdb_id"`
	Title  string `json:"title"`
}

type TVLibraryTitles struct {
	InstanceID string           `json:"instance_id"`
	Titles     []TVLibraryTitle `json:"titles"`
}

// TVLibraryDestinations resolves a live Sonarr record back to catalog titles.
// A series card has no single story for an anthology; a calendar episode can
// narrow it by season. The same reverse resolver owns notification identity.
func (s *Service) TVLibraryDestinations(userID int64, instanceID string, seriesID int, season *int) (*TVLibraryTitles, error) {
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
	var evidence []sonarr.ImportedEpisode
	for _, candidate := range series.Seasons {
		if (season == nil && candidate.SeasonNumber > 0) || (season != nil && candidate.SeasonNumber == *season) {
			evidence = append(evidence, sonarr.ImportedEpisode{SeasonNumber: candidate.SeasonNumber})
		}
	}
	if season != nil && len(evidence) == 0 {
		return nil, tvMatchFailure("tv_seasons_unmapped")
	}
	titles, err := s.ResolveTVImports(client, series, evidence)
	if err != nil {
		// Do not return a partial chooser that looks complete, or upstream
		// error strings containing provider addresses or rejected title data.
		var matchError *tvMatchError
		if errors.As(err, &matchError) {
			return nil, matchError
		}
		return nil, tvMatchFailure("tv_metadata_unavailable")
	}
	out := &TVLibraryTitles{InstanceID: resolvedID, Titles: []TVLibraryTitle{}}
	for _, title := range titles {
		if title.TmdbID <= 0 {
			return nil, tvMatchFailure("tv_match_ambiguous")
		}
		if err := s.checkContentPolicy(userID, s.userIsAdmin(userID), "tv", title.TmdbID); err != nil {
			if errors.Is(err, ErrTitleNotAvailable) {
				continue
			}
			return nil, err
		}
		out.Titles = append(out.Titles, TVLibraryTitle{TmdbID: title.TmdbID, Title: title.Title})
	}
	// Grants can change during provider reads. No cached destination survives
	// a tap, and an explicit library never falls back to another one.
	if _, _, err = s.resolveSonarr(userID, resolvedID); err != nil {
		return nil, err
	}
	if len(out.Titles) == 0 {
		return nil, ErrTitleNotAvailable
	}
	return out, nil
}

func (h *Handler) GetTVLibraryTitles(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	query := r.URL.Query()
	instanceID := strings.TrimSpace(query.Get("instance_id"))
	seriesID, err := strconv.Atoi(query.Get("series_id"))
	if instanceID == "" || err != nil || seriesID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "instance_id and a positive series_id are required"})
		return
	}
	var season *int
	if query.Has("season_number") {
		n, err := strconv.Atoi(query.Get("season_number"))
		if err != nil || n < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid season_number"})
			return
		}
		season = &n
	}
	out, err := h.service.TVLibraryDestinations(claims.UserID, instanceID, seriesID, season)
	tvMatchReply(w, out, err)
}
