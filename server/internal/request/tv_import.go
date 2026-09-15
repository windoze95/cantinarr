package request

import (
	"errors"
	"fmt"
	"sort"

	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// importTVMatches reads effective configuration, including paused scopes. The
// bundled parent stays protected even if an override moves its last story away:
// that parent's TMDB ID is not a fallback for an unmapped imported season.
func (s *Service) importTVMatches(series *sonarr.Series) (bool, []TVMatch, error) {
	matches, err := s.configuredTVMatches()
	if err != nil {
		return true, nil, err
	}
	corrected := false
	for _, m := range bundledTVMatches.Corrections {
		if m.TVDBID == series.TvdbID || m.TmdbID == series.TmdbID {
			corrected = true
		}
	}
	var candidates []TVMatch
	for _, m := range matches {
		if m.Provenance == "default" {
			continue
		}
		if m.TmdbID == series.TmdbID {
			corrected = true
		}
		if m.TVDBID > 0 && m.TVDBID == series.TvdbID {
			corrected = true
			candidates = append(candidates, m)
		}
	}
	return corrected, candidates, nil
}

func (s *Service) HasTVImportCorrection(series *sonarr.Series) (bool, error) {
	corrected, _, err := s.importTVMatches(series)
	return corrected, err
}

// ResolveTVImports reverses the same configured season maps used by requests,
// status and repairs. It never reads request history. Validation is live and
// each imported season must have exactly one owner, including paused owners.
func (s *Service) ResolveTVImports(client *sonarr.Client, series *sonarr.Series, episodes []sonarr.ImportedEpisode) ([]sonarr.ImportedTitle, error) {
	// Serialize with edits, so metadata reads cannot mix revisions or miss a
	// newly introduced overlapping custom scope halfway through this resolution.
	s.tvMatchMu.Lock()
	defer s.tvMatchMu.Unlock()
	corrected, candidates, err := s.importTVMatches(series)
	if err != nil {
		return nil, err
	}
	if !corrected {
		return sonarr.UncorrectedImportTitle(series, episodes), nil
	}
	if len(episodes) == 0 {
		return nil, tvMatchFailure("tv_seasons_unmapped")
	}
	bySeason := map[int][]sonarr.ImportedEpisode{}
	for _, episode := range episodes {
		bySeason[episode.SeasonNumber] = append(bySeason[episode.SeasonNumber], episode)
	}
	verified := map[int]*TVMatch{}
	failures := map[int]error{}
	titles := map[int]sonarr.ImportedTitle{}
	var rejected []error
	for season, imports := range bySeason {
		var owners []TVMatch
		if season > 0 {
			for _, m := range candidates {
				for _, target := range m.SeasonMap {
					if target == season {
						owners = append(owners, m)
						break
					}
				}
			}
		}
		var failure error
		switch len(owners) {
		case 0:
			failure = tvMatchFailure("tv_seasons_unmapped")
		case 1:
			id := owners[0].TmdbID
			if _, ok := verified[id]; !ok {
				m, e := s.resolveTVMatch(client, id)
				if e == nil && m.Revision != owners[0].Revision {
					e = ErrTVMatchStale
				}
				if e == nil {
					// Also verify the target seasons in THIS importing library.
					source := make([]int, 0, len(m.SeasonMap))
					for n := range m.SeasonMap {
						source = append(source, n)
					}
					e = validateTVSeasons(m.SeasonMap, source, series.Seasons)
				}
				verified[id], failures[id] = m, e
			}
			failure = failures[id]
			if failure == nil {
				title, exists := titles[id]
				if !exists {
					title = sonarr.ImportedTitle{TmdbID: id, Title: verified[id].Title, Upgrade: true}
				}
				for _, episode := range imports {
					title.Upgrade = title.Upgrade && episode.Upgrade
				}
				titles[id] = title
			}
		default:
			failure = tvMatchFailure("tv_match_ambiguous")
		}
		if failure != nil {
			rejected = append(rejected, fmt.Errorf("series %d season %d: %w", series.ID, season, failure))
		}
	}
	out := make([]sonarr.ImportedTitle, 0, len(titles))
	for _, title := range titles {
		out = append(out, title)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TmdbID < out[j].TmdbID })
	return out, errors.Join(rejected...)
}
