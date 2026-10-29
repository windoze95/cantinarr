package request

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/windoze95/cantinarr-server/internal/mediaavailability"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

const (
	RequestModeMissing     = "missing"
	RequestModeLibraryCopy = "library_copy"
)

var ErrMediaAvailabilityUnknown = errors.New("Media-server availability could not be verified. Retry, or explicitly request a copy for this library.")

type mediaScopeReader interface{ ScopeRevision(int64) (string, error) }

type MediaAvailabilitySource interface {
	Lookup(context.Context, int64, mediaserver.ItemQuery, bool) (mediaavailability.Result, error)
}

func (s *Service) SetMediaAvailability(source MediaAvailabilitySource) { s.mediaAvailability = source }

func normalizedRequestMode(mode string) string {
	if mode == RequestModeLibraryCopy {
		return mode
	}
	return RequestModeMissing
}

// Availability is playable coverage. The enclosing StatusResponse remains the
// selected automation library's state, including pending decisions and delivery.
type Availability struct {
	Status              string                     `json:"status"`
	StatusKnown         bool                       `json:"status_known"`
	StatusUnknownReason string                     `json:"status_unknown_reason,omitempty"`
	Progress            float64                    `json:"progress"`
	Is4K                bool                       `json:"is_4k"`
	Seasons             []SeasonStatus             `json:"seasons,omitempty"`
	Sources             []mediaavailability.Source `json:"sources"`
}

// CanonicalEpisode is durable catalog identity. Sonarr's database ID is never
// stored here: it must be resolved again before every operation.
type CanonicalEpisode struct {
	TMDBID        int    `json:"tmdb_id"`
	TVDBID        int    `json:"tvdb_id,omitempty"`
	SeasonNumber  int    `json:"season_number"`
	EpisodeNumber int    `json:"episode_number"`
	AirDate       string `json:"air_date"`
}

func (e CanonicalEpisode) key() string { return strconv.Itoa(e.TMDBID) }
func (e CanonicalEpisode) aired(now time.Time) bool {
	date, err := time.Parse("2006-01-02", e.AirDate)
	// Date-only metadata cannot promise an unaired episode is obtainable at
	// midnight. Start automatic searches after that UTC date has ended.
	return err == nil && !now.UTC().Before(date.Add(24*time.Hour))
}

func (s *Service) mediaLookup(ctx context.Context, userID int64, tmdbID int, mediaType string, fresh bool) (mediaavailability.Result, error) {
	if s.mediaAvailability == nil {
		return mediaavailability.Result{}, nil
	}
	if err := s.checkContentPolicy(userID, s.userIsAdmin(userID), mediaType, tmdbID); err != nil {
		return mediaavailability.Result{}, err
	}
	q := mediaserver.ItemQuery{MediaType: mediaType, TMDBID: int64(tmdbID)}
	if mediaType == "tv" {
		m, _, err := configuredTVMatch(s.db, tmdbID)
		if err != nil {
			return mediaavailability.Result{}, err
		}
		if m.State != "paused" {
			q.TVDBID = int64(m.TVDBID)
		}
		if q.TVDBID == 0 && s.bridge != nil {
			if result, err := s.bridge.ResolveTVDBID(tmdbID); err == nil && result != nil {
				q.TVDBID = int64(result.TVDBID)
			}
		}
	}
	out, err := s.mediaAvailability.Lookup(ctx, userID, q, fresh)
	// Narrowed provider searches need a title as well as an exact external ID.
	// Only fetch catalog metadata when enabled sources could not prove an answer.
	if out.Enabled && !out.Known && !out.Movie && s.bridge != nil && s.bridge.TMDB() != nil {
		if mediaType == "movie" {
			if d, e := s.bridge.TMDB().GetMovieDetails(tmdbID); e == nil && d.ID == tmdbID {
				q.Title = d.Title
				if len(d.ReleaseDate) >= 4 {
					q.Year, _ = strconv.Atoi(d.ReleaseDate[:4])
				}
			}
		} else if d, e := s.tvSource(tmdbID); e == nil && d.ID == tmdbID {
			q.Title = d.Name
			if len(d.FirstAir) >= 4 {
				q.Year, _ = strconv.Atoi(d.FirstAir[:4])
			}
		}
		if q.Title != "" {
			out, err = s.mediaAvailability.Lookup(ctx, userID, q, fresh)
		}
	}
	if e := s.checkContentPolicy(userID, s.userIsAdmin(userID), mediaType, tmdbID); e != nil {
		return mediaavailability.Result{}, e
	}
	out.Episodes = append([]mediaavailability.Episode(nil), out.Episodes...)
	return out, err
}

func (s *Service) canonicalEpisodes(tmdbID int, selected []int, fresh bool) ([]CanonicalEpisode, error) {
	if s.bridge == nil || s.bridge.TMDB() == nil {
		return nil, tvMatchFailure("tv_metadata_unavailable")
	}
	details, err := s.tvSource(tmdbID)
	if err != nil {
		return nil, err
	}
	chosen := map[int]bool{}
	for _, n := range selected {
		chosen[n] = true
	}
	var out []CanonicalEpisode
	seen := map[int]bool{}
	for _, season := range details.Seasons {
		if season.SeasonNumber <= 0 || (len(chosen) > 0 && !chosen[season.SeasonNumber]) {
			continue
		}
		key := fmt.Sprintf("media-season:%d:%d", tmdbID, season.SeasonNumber)
		var d tmdb.TVSeasonDetails
		cached := false
		if !fresh && s.libraryCache != nil {
			if raw, ok := s.libraryCache.Get(key); ok && json.Unmarshal(raw, &d) == nil {
				cached = true
			}
		}
		if !cached || len(d.Episodes) != season.EpisodeCount {
			live, err := s.bridge.TMDB().GetTVSeason(tmdbID, season.SeasonNumber)
			if err != nil {
				return nil, tvMatchFailure("tv_metadata_unavailable")
			}
			d = *live
			if s.libraryCache != nil {
				raw, _ := json.Marshal(d)
				s.libraryCache.Set(key, raw, 5*time.Minute)
			}
		}
		if len(d.Episodes) != season.EpisodeCount {
			return nil, tvMatchFailure("tv_metadata_unavailable")
		}
		for _, ep := range d.Episodes {
			if seen[ep.ID] {
				return nil, tvMatchFailure("tv_match_ambiguous")
			}
			seen[ep.ID] = true
			e := CanonicalEpisode{TMDBID: ep.ID, SeasonNumber: ep.SeasonNumber, EpisodeNumber: ep.EpisodeNumber, AirDate: ep.AirDate}
			idKey := fmt.Sprintf("media-episode-ids:%d:%d", tmdbID, ep.ID)
			var ids tmdb.ExternalIDs
			idsKnown := false
			if s.libraryCache != nil {
				if raw, ok := s.libraryCache.Get(idKey); ok && json.Unmarshal(raw, &ids) == nil {
					idsKnown = true
				}
			}
			if !idsKnown && fresh && len(chosen) > 0 {
				if live, e := s.bridge.TMDB().GetTVEpisodeExternalIDs(tmdbID, ep.SeasonNumber, ep.EpisodeNumber); e == nil && live.TMDbID == ep.ID {
					ids = *live
					if s.libraryCache != nil && ids.TVDBID != nil && *ids.TVDBID > 0 {
						raw, _ := json.Marshal(ids)
						s.libraryCache.Set(idKey, raw, 6*time.Hour)
					}
				}
			}
			if ids.TVDBID != nil {
				e.TVDBID = *ids.TVDBID
			}
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		return nil, tvMatchFailure("tv_metadata_unavailable")
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SeasonNumber != out[j].SeasonNumber {
			return out[i].SeasonNumber < out[j].SeasonNumber
		}
		return out[i].EpisodeNumber < out[j].EpisodeNumber
	})
	return out, nil
}

// mediaEpisodeCoverage never matches by titles or assumes a provider's display
// numbering is TMDB ordering. Unknown source records cannot prove absence.
func mediaEpisodeCoverage(tmdbID int, canonical []CanonicalEpisode, result mediaavailability.Result) (map[string]bool, map[string]bool, bool) {
	covered, fourK := map[string]bool{}, map[string]bool{}
	byTMDB, byTVDB := map[int]CanonicalEpisode{}, map[int]CanonicalEpisode{}
	byNumber := map[[2]int]CanonicalEpisode{}
	for _, ep := range canonical {
		byTMDB[ep.TMDBID] = ep
		if ep.TVDBID > 0 {
			byTVDB[ep.TVDBID] = ep
		}
		byNumber[[2]int{ep.SeasonNumber, ep.EpisodeNumber}] = ep
	}
	known := result.Known
	for _, item := range result.Episodes {
		if item.Numbering == "tmdb" && item.SeriesTMDBID == int64(tmdbID) && item.SeasonNumber == 0 {
			continue
		}
		ep, ok := byTMDB[int(item.TMDBID)]
		if !ok && item.TVDBID > 0 {
			ep, ok = byTVDB[int(item.TVDBID)]
		}
		if !ok && item.Numbering == "tmdb" && item.SeriesTMDBID == int64(tmdbID) {
			ep, ok = byNumber[[2]int{item.SeasonNumber, item.EpisodeNumber}]
		}
		if !ok {
			known = false
			continue
		}
		if item.TMDBID > 0 && int(item.TMDBID) != ep.TMDBID {
			known = false
			continue
		}
		if item.TVDBID > 0 && ep.TVDBID > 0 && int(item.TVDBID) != ep.TVDBID {
			known = false
			continue
		}
		covered[ep.key()] = true
		fourK[ep.key()] = fourK[ep.key()] || item.Is4K
	}
	return covered, fourK, known
}

func resolveCanonicalEpisode(ep CanonicalEpisode, seriesID int, episodes []sonarr.Episode) (*sonarr.Episode, error) {
	if ep.TVDBID <= 0 {
		return nil, tvMatchFailure("tv_match_ambiguous")
	}
	var match *sonarr.Episode
	for i := range episodes {
		if episodes[i].TVDBID != ep.TVDBID {
			continue
		}
		if episodes[i].ID <= 0 || episodes[i].SeriesID != seriesID || match != nil {
			return nil, tvMatchFailure("tv_match_ambiguous")
		}
		copy := episodes[i]
		match = &copy
	}
	return match, nil
}

func (s *Service) attachMediaAvailability(ctx context.Context, userID int64, tmdbID int, mediaType, instanceID string, out *StatusResponse) error {
	if mediaType == "tv" {
		out.Following, _ = s.tvFollowing(userID, tmdbID, instanceID)
	}
	if s.mediaAvailability == nil || (mediaType != "movie" && mediaType != "tv") {
		return nil
	}
	result, err := s.mediaLookup(ctx, userID, tmdbID, mediaType, false)
	if errors.Is(err, ErrTitleNotAvailable) || errors.Is(err, ErrContentPolicyUnavailable) {
		return err
	}
	if !result.Enabled && err == nil {
		return nil
	}
	a := &Availability{Status: StatusUnavailable, StatusKnown: result.Known && err == nil, Sources: result.Sources}
	if a.Sources == nil {
		a.Sources = []mediaavailability.Source{}
	}
	out.Availability = a
	if mediaType == "movie" {
		if result.Movie || out.Status == StatusAvailable {
			a.Status, a.Progress, a.StatusKnown = StatusAvailable, 1, true
			a.Is4K = out.Is4K || (result.Movie && result.Is4K)
		}
	} else {
		canonical, e := s.canonicalEpisodes(tmdbID, nil, false)
		if e != nil {
			a.StatusKnown = false
			a.StatusUnknownReason = "tv_metadata_unavailable"
			return nil
		}
		canonical = s.bindProviderEpisodeIDs(tmdbID, canonical, result.Episodes)
		covered, fourK, known := mediaEpisodeCoverage(tmdbID, canonical, result)
		a.StatusKnown = a.StatusKnown && known
		arrEmptyVerified := false
		if client, _, e := s.resolveSonarr(userID, instanceID); e == nil && client != nil && out.Match != nil && out.Match.State == "resolved" {
			if series, e := client.GetSeriesByTVDB(out.Match.TVDBID); e == nil {
				if series == nil {
					arrEmptyVerified = true
				} else if episodes, e := client.GetAllEpisodes(series.ID); e == nil && episodes != nil {
					arrEmptyVerified = true
					mappedSeasons := map[int]bool{}
					for _, target := range out.Match.SeasonMap {
						mappedSeasons[target] = true
					}
					// Raw files disprove emptiness even before their canonical
					// episode bindings are cached.
					for _, ep := range episodes {
						if ep.HasFile && mappedSeasons[ep.SeasonNumber] {
							arrEmptyVerified = false
							break
						}
					}
					for _, ep := range canonical {
						if native, e := resolveCanonicalEpisode(ep, series.ID, episodes); e == nil && native != nil && native.HasFile {
							covered[ep.key()] = true
						}
					}
				}
			}
		}
		counts := map[int]*SeasonStatus{}
		total, files, all4K := 0, 0, true
		unknownDates := map[int]bool{}
		// Successful provider and arr reads can prove there are no files
		// without knowing when every episode airs. Keep date uncertainty for
		// actual holdings and for any library whose state is not verified.
		knownEmpty := a.StatusKnown && arrEmptyVerified && len(result.Episodes) == 0 && len(covered) == 0 &&
			out.StatusKnown != nil && *out.StatusKnown &&
			out.Status != StatusAvailable && out.Status != StatusPartial
		for _, ep := range canonical {
			if counts[ep.SeasonNumber] == nil {
				counts[ep.SeasonNumber] = &SeasonStatus{SeasonNumber: ep.SeasonNumber, Status: StatusUnavailable}
			}
			if _, e := time.Parse("2006-01-02", ep.AirDate); e != nil && !covered[ep.key()] && !knownEmpty {
				a.StatusKnown = false
				unknownDates[ep.SeasonNumber] = true
			}
			if !ep.aired(time.Now()) && !covered[ep.key()] {
				continue
			}
			counts[ep.SeasonNumber].EpisodeCount++
			total++
			if covered[ep.key()] {
				counts[ep.SeasonNumber].EpisodeFileCount++
				files++
				all4K = all4K && fourK[ep.key()]
			}
		}
		for _, st := range counts {
			st.Status, st.Progress = statusFromCompletion(sonarr.Completion{Files: st.EpisodeFileCount, Aired: st.EpisodeCount}, false)
			seasonKnown := known && !unknownDates[st.SeasonNumber] && (a.StatusKnown || (st.EpisodeCount > 0 && st.EpisodeFileCount == st.EpisodeCount))
			if !seasonKnown && st.Status == StatusAvailable {
				st.Status = StatusPartial
			}
			st.StatusKnown = &seasonKnown
			a.Seasons = append(a.Seasons, *st)
		}
		sort.Slice(a.Seasons, func(i, j int) bool { return a.Seasons[i].SeasonNumber < a.Seasons[j].SeasonNumber })
		a.Status, a.Progress = statusFromCompletion(sonarr.Completion{Files: files, Aired: total}, false)
		if a.Status == StatusAvailable && len(unknownDates) == 0 && known {
			a.StatusKnown = true
			a.Is4K = all4K
		}
		if (len(unknownDates) > 0 || !known) && a.Status == StatusAvailable {
			a.Status = StatusPartial
		}
		// Preserve complete live arr proof even when provider episode bindings
		// are not warm yet. Per-instance state remains unchanged.
		if out.Status == StatusAvailable {
			a.Status, a.Progress, a.StatusKnown = StatusAvailable, 1, true
			a.Is4K = a.Is4K || out.Is4K
		}
	}
	if !a.StatusKnown && a.StatusUnknownReason == "" {
		a.StatusUnknownReason = "media_library_unavailable"
	}
	if err := s.checkContentPolicy(userID, s.userIsAdmin(userID), mediaType, tmdbID); err != nil {
		return err
	}
	return nil
}

// Catalog cards never enumerate every episode's external_ids. Only actual
// provider TVDB holdings need translation, capped per read and cached; more
// than the bound remains explicitly unknown rather than blocking a poster on
// thousands of network calls.
func (s *Service) bindProviderEpisodeIDs(tmdbID int, canonical []CanonicalEpisode, items []mediaavailability.Episode) []CanonicalEpisode {
	if s.bridge == nil || s.bridge.TMDB() == nil {
		return canonical
	}
	byID := map[int]int{}
	byTVDB := map[int]bool{}
	for i, ep := range canonical {
		byID[ep.TMDBID] = i
		if ep.TVDBID > 0 {
			byTVDB[ep.TVDBID] = true
		}
	}
	s.bindSpecialEpisodes(tmdbID, items, byID)
	calls := 0
	for index, item := range items {
		if item.TMDBID > 0 || item.TVDBID <= 0 || byTVDB[int(item.TVDBID)] {
			continue
		}
		key := fmt.Sprintf("media-episode-find:%d", item.TVDBID)
		var identity struct {
			ShowID       int
			EpisodeID    int
			SeasonNumber *int
		}
		cached := false
		if s.libraryCache != nil {
			if raw, ok := s.libraryCache.Get(key); ok && json.Unmarshal(raw, &identity) == nil {
				cached = true
			}
		}
		if !cached {
			if calls >= 8 {
				continue
			}
			calls++
			show, id, season, err := s.bridge.TMDB().FindTVEpisode(int(item.TVDBID))
			if err != nil {
				if s.libraryCache != nil {
					s.libraryCache.Set(key, []byte(`{}`), time.Minute)
				}
				continue
			}
			identity.ShowID, identity.EpisodeID, identity.SeasonNumber = show, id, season
			if s.libraryCache != nil {
				raw, _ := json.Marshal(identity)
				s.libraryCache.Set(key, raw, 6*time.Hour)
			}
		}
		if identity.ShowID == tmdbID && identity.SeasonNumber != nil && *identity.SeasonNumber == 0 {
			items[index].Numbering = "tmdb"
			items[index].SeriesTMDBID = int64(tmdbID)
			items[index].SeasonNumber = 0
			continue
		}
		i, ok := byID[identity.EpisodeID]
		if !ok || identity.ShowID != tmdbID {
			continue
		}
		if canonical[i].TVDBID != 0 && canonical[i].TVDBID != int(item.TVDBID) {
			continue
		}
		canonical[i].TVDBID = int(item.TVDBID)
		byTVDB[int(item.TVDBID)] = true
	}
	return canonical
}

// Read the full catalog from its short cache, refreshing and binding only the
// explicitly selected seasons. A follower never enumerates external IDs for
// unrelated historical seasons on every pass.
func (s *Service) canonicalFollowEpisodes(tmdbID int, seasons []int) ([]CanonicalEpisode, error) {
	all, err := s.canonicalEpisodes(tmdbID, nil, false)
	if err != nil {
		return nil, err
	}
	selected, err := s.canonicalEpisodes(tmdbID, seasons, true)
	if err != nil {
		return nil, err
	}
	chosen := map[int]bool{}
	for _, n := range seasons {
		chosen[n] = true
	}
	out := make([]CanonicalEpisode, 0, len(all))
	for _, ep := range all {
		if !chosen[ep.SeasonNumber] {
			out = append(out, ep)
		}
	}
	return append(out, selected...), nil
}

// A provider's native season 0 is only a hint. Confirm direct TMDB episode
// IDs against TMDB's actual specials catalog before excluding them.
func (s *Service) bindSpecialEpisodes(tmdbID int, items []mediaavailability.Episode, canonical map[int]int) {
	need := false
	for _, item := range items {
		_, ordinary := canonical[int(item.TMDBID)]
		if !ordinary && item.SeasonNumber == 0 && item.TMDBID > 0 && item.Numbering != "tmdb" {
			need = true
			break
		}
	}
	if !need {
		return
	}
	key := fmt.Sprintf("media-season:%d:0", tmdbID)
	var season tmdb.TVSeasonDetails
	cached := false
	if s.libraryCache != nil {
		if raw, ok := s.libraryCache.Get(key); ok && json.Unmarshal(raw, &season) == nil {
			cached = true
		}
	}
	if !cached {
		d, err := s.bridge.TMDB().GetTVSeason(tmdbID, 0)
		if err != nil {
			return
		}
		season = *d
		if s.libraryCache != nil {
			raw, _ := json.Marshal(season)
			s.libraryCache.Set(key, raw, 5*time.Minute)
		}
	}
	ids := map[int]bool{}
	for _, ep := range season.Episodes {
		ids[ep.ID] = true
	}
	for i, item := range items {
		if ids[int(item.TMDBID)] {
			items[i].Numbering = "tmdb"
			items[i].SeriesTMDBID = int64(tmdbID)
			items[i].SeasonNumber = 0
		}
	}
}
