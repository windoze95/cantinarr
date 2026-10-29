package request

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/mediametadata"
)

// Composite parents retain native metadata. Only verified story labels in
// their library-numbered seasons receive catalog presentation text.
func (s *Service) localizeTVLibrary(ctx context.Context, userID int64, out *TVLibraryDetail, language string) error {
	if language == "" || s.metadata == nil || out == nil || out.Detail == nil {
		return nil
	}
	seasons, _ := out.Detail["seasons"].([]map[string]any)
	enrichment, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	values := make([]*mediametadata.Presentation, len(out.Matches))
	failures := make([]error, len(out.Matches))
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for i, match := range out.Matches {
		wg.Add(1)
		go func(i int, match TVMatch) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-enrichment.Done():
				return
			}
			values[i], failures[i] = s.catalogPresentation(enrichment, userID, "tv", match.TmdbID, language)
		}(i, match)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for i, match := range out.Matches {
		if failures[i] != nil {
			return failures[i]
		}
		p := values[i]
		if p == nil || p.Title == "" {
			continue
		}
		for _, target := range match.SeasonMap {
			for _, season := range seasons {
				if number, ok := season["season_number"].(int); ok && number == target {
					season["source_name"] = season["name"]
					season["name"] = p.Title
				}
			}
		}
	}
	// Recheck native identity, configuration and policy without repeating all
	// source-detail/status/episode reads used to build the page.
	client, _, err := s.resolveSonarr(userID, out.InstanceID)
	if err != nil {
		return err
	}
	if client == nil {
		return ErrArrInstanceInvalid
	}
	validation, cancelValidation := context.WithTimeout(ctx, 5*time.Second)
	defer cancelValidation()
	series, err := client.GetSeriesContext(validation, out.SeriesID)
	if err != nil || series == nil || series.ID != out.SeriesID {
		return tvMatchFailure("tv_metadata_unavailable")
	}
	s.tvMatchMu.Lock()
	_, candidates, err := s.importTVMatches(series)
	s.tvMatchMu.Unlock()
	if err != nil {
		return err
	}
	numbers := []int{}
	for _, season := range series.Seasons {
		if season.SeasonNumber > 0 {
			numbers = append(numbers, season.SeasonNumber)
		}
	}
	numbers = normalizeSeasonNumbers(numbers)
	proof, _ := json.Marshal([]any{out.InstanceID, series.ID, series.TvdbID, numbers, candidates, out.Matches})
	if fmt.Sprintf("%x", sha256.Sum256(proof)) != out.Revision {
		return ErrTVMatchStale
	}
	policy, err := s.contentPolicyFor(userID, s.userIsAdmin(userID))
	if err != nil {
		return err
	}
	if policy != nil {
		if len(numbers) == 0 {
			return ErrContentPolicyUnavailable
		}
		checked := map[int]bool{}
		for _, number := range numbers {
			owner, count := 0, 0
			for _, candidate := range candidates {
				for _, target := range candidate.SeasonMap {
					if target == number {
						owner = candidate.TmdbID
						count++
					}
				}
			}
			if count != 1 {
				return ErrContentPolicyUnavailable
			}
			if !checked[owner] {
				if err := s.checkContentPolicy(userID, false, "tv", owner); err != nil {
					return err
				}
				checked[owner] = true
			}
		}
	}
	_, _, err = s.resolveSonarr(userID, out.InstanceID)
	if err != nil {
		return err
	}
	// Policy lookups can contact TMDB. Keep them outside the correction lock,
	// then make one final local configuration check after those reads.
	s.tvMatchMu.Lock()
	_, latest, err := s.importTVMatches(series)
	s.tvMatchMu.Unlock()
	if err != nil {
		return err
	}
	proof, _ = json.Marshal([]any{out.InstanceID, series.ID, series.TvdbID, numbers, latest, out.Matches})
	if fmt.Sprintf("%x", sha256.Sum256(proof)) != out.Revision {
		return ErrTVMatchStale
	}
	return nil
}
