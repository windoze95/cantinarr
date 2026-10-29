package request

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"slices"
	"sort"
)

type intentReader interface{ QueryRow(string, ...any) *sql.Row }

// Intent belongs to approval/history even when live sources are unavailable.
// Old accepted rows explicitly keep the original library-copy semantics.
func savedMediaIntent(db intentReader, id int64) (string, []int) {
	var mode, raw string
	if db.QueryRow(`SELECT COALESCE(NULLIF(json_extract(t.snapshot,'$.request_mode'),''),m.mode,'library_copy'),COALESCE(json_extract(t.snapshot,'$.source_seasons'),'[]') FROM request_log r LEFT JOIN request_media_modes m ON m.request_id=r.id LEFT JOIN request_tv_targets t ON t.request_id=r.id WHERE r.id=? AND r.media_type IN ('movie','tv')`, id).Scan(&mode, &raw) != nil {
		return "", nil
	}
	var seasons []int
	_ = json.Unmarshal([]byte(raw), &seasons)
	return mode, seasons
}

func savedRequestMode(mode string) string {
	if mode == "" {
		return RequestModeLibraryCopy
	}
	return mode
}

// TVEpisodeScope is the immutable episode receipt reviewed at acceptance.
// Future episodes belong to the selected-season follow, not this initial list.
type TVEpisodeScope struct {
	MissingEpisodes            []CanonicalEpisode `json:"missing_episodes"`
	InitiallySatisfiedEpisodes []CanonicalEpisode `json:"initially_satisfied_episodes"`
}

func savedTVScope(db intentReader, id int64) *TVEpisodeScope {
	var raw string
	if db.QueryRow(`SELECT snapshot FROM request_tv_targets WHERE request_id=?`, id).Scan(&raw) != nil {
		return nil
	}
	var target TVRequestTarget
	if json.Unmarshal([]byte(raw), &target) != nil || !target.MediaCoverageRequired || target.RequestMode != RequestModeMissing {
		return nil
	}
	scope := &TVEpisodeScope{MissingEpisodes: []CanonicalEpisode{}, InitiallySatisfiedEpisodes: []CanonicalEpisode{}}
	for _, ep := range target.Episodes {
		if slices.Contains(target.ExcludedEpisodes, ep.key()) {
			scope.InitiallySatisfiedEpisodes = append(scope.InitiallySatisfiedEpisodes, ep)
		} else {
			scope.MissingEpisodes = append(scope.MissingEpisodes, ep)
		}
	}
	return scope
}
func tvEpisodeIntent(target *TVRequestTarget) string {
	type identity struct {
		TMDB, TVDB, Season, Episode int
		Excluded                    bool
	}
	items := make([]identity, 0, len(target.Episodes))
	for _, ep := range target.Episodes {
		items = append(items, identity{ep.TMDBID, ep.TVDBID, ep.SeasonNumber, ep.EpisodeNumber, slices.Contains(target.ExcludedEpisodes, ep.key())})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].TMDB < items[j].TMDB })
	raw, _ := json.Marshal(items)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
