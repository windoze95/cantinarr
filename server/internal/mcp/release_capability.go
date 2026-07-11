package mcp

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

const releaseReferencePrefix = "[REDACTED release sha256:"

// releaseGUIDReference turns an arr release GUID into a stable, one-way
// selector. Release GUIDs are download capabilities: some are obvious signed
// URLs, while others are opaque tokens, so none of them are safe model data.
func releaseGUIDReference(guid string) string {
	digest := sha256.Sum256([]byte(guid))
	return fmt.Sprintf("%s%x]", releaseReferencePrefix, digest[:8])
}

func isReleaseGUIDReference(reference string) bool {
	const digestHexLen = 16
	if len(reference) != len(releaseReferencePrefix)+digestHexLen+1 ||
		!strings.HasPrefix(reference, releaseReferencePrefix) || reference[len(reference)-1] != ']' {
		return false
	}
	for _, char := range reference[len(releaseReferencePrefix) : len(reference)-1] {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

// canonicalReleaseGUIDReference is a defense-in-depth ToolResult boundary.
// Search rendering hashes raw GUIDs directly; this helper also catches a raw
// reference if another producer constructs ReleaseCandidates in the future.
func canonicalReleaseGUIDReference(reference string) string {
	if isReleaseGUIDReference(reference) {
		return reference
	}
	return releaseGUIDReference(reference)
}

// scrubRawReleaseGUIDs prevents a capability repeated in untrusted display
// metadata (for example, an indexer title or rejection string) from bypassing
// the dedicated GUID field's one-way rendering.
func scrubRawReleaseGUIDs(value string, releases []releaseCapability) string {
	capabilities := make([]string, 0, len(releases))
	seen := make(map[string]struct{}, len(releases))
	for _, release := range releases {
		if release.guid == "" {
			continue
		}
		if _, ok := seen[release.guid]; ok {
			continue
		}
		seen[release.guid] = struct{}{}
		capabilities = append(capabilities, release.guid)
	}
	sort.SliceStable(capabilities, func(i, j int) bool { return len(capabilities[i]) > len(capabilities[j]) })
	for _, capability := range capabilities {
		value = strings.ReplaceAll(value, capability, releaseGUIDReference(capability))
	}
	return value
}

type scopedReleaseGrabParams struct {
	GUID          string `json:"guid"`
	IndexerID     int    `json:"indexer_id"`
	MediaType     string `json:"media_type"`
	TmdbID        int    `json:"tmdb_id"`
	SeasonNumber  *int   `json:"season_number"`
	EpisodeNumber *int   `json:"episode_number"`
	BookID        int    `json:"book_id"`
}

type releaseCapability struct {
	guid      string
	indexerID int
}

// resolveFreshReleaseGUID matches a model-safe selector against a just-fetched
// exact-scope release list. The raw capability exists only on this stack long
// enough to dispatch it. A 64-bit selector collision or duplicate is treated
// as ambiguity and never guessed through.
func resolveFreshReleaseGUID(reference string, indexerID int, releases []releaseCapability) (string, error) {
	if !isReleaseGUIDReference(reference) {
		return "", &MutationNotStartedError{Detail: "guid must be the exact one-way release reference returned by search_releases"}
	}
	if indexerID <= 0 {
		return "", &MutationNotStartedError{Detail: "indexer_id must be a positive id returned by search_releases"}
	}

	matches := make([]string, 0, 1)
	for _, release := range releases {
		if release.guid != "" && release.indexerID == indexerID && releaseGUIDReference(release.guid) == reference {
			matches = append(matches, release.guid)
		}
	}
	if len(matches) == 0 {
		return "", &MutationNotStartedError{Detail: "the release reference and indexer_id were not present in a fresh search of the requested scope; search again and review the current candidates"}
	}
	if len(matches) != 1 {
		return "", &MutationNotStartedError{Detail: "the release reference is ambiguous in a fresh search of the requested scope; no release was grabbed"}
	}
	return matches[0], nil
}

func radarrReleaseCapabilities(releases []radarr.Release) []releaseCapability {
	out := make([]releaseCapability, 0, len(releases))
	for _, release := range releases {
		out = append(out, releaseCapability{guid: release.GUID, indexerID: release.IndexerID})
	}
	return out
}

func sonarrReleaseCapabilities(releases []sonarr.Release) []releaseCapability {
	out := make([]releaseCapability, 0, len(releases))
	for _, release := range releases {
		out = append(out, releaseCapability{guid: release.GUID, indexerID: release.IndexerID})
	}
	return out
}

func chaptarrReleaseCapabilities(releases []chaptarr.Release) []releaseCapability {
	out := make([]releaseCapability, 0, len(releases))
	for _, release := range releases {
		out = append(out, releaseCapability{guid: release.GUID, indexerID: release.IndexerID})
	}
	return out
}

// grabFreshScopedRelease performs the mandatory fresh search and immediate
// dispatch for the ordinary admin MCP tool. Scope is supplied by the caller,
// not recovered from prior chat state, and every mismatch fails before POST.
func grabFreshScopedRelease(rc *radarr.Client, sc *sonarr.Client, cc *chaptarr.Client, params scopedReleaseGrabParams) (string, error) {
	if !isReleaseGUIDReference(params.GUID) {
		return mutationNotStarted("guid must be the exact one-way release reference returned by search_releases")
	}
	if params.IndexerID <= 0 {
		return mutationNotStarted("indexer_id must be a positive id returned by search_releases")
	}

	switch params.MediaType {
	case "movie":
		if params.TmdbID <= 0 || params.SeasonNumber != nil || params.EpisodeNumber != nil || params.BookID != 0 {
			return mutationNotStarted("movie grab_release requires only a positive tmdb_id as its media scope")
		}
		if rc == nil {
			return mutationNotStarted("Radarr is not configured for the selected instance")
		}
		movies, err := rc.GetMovies()
		if err != nil {
			return "", fmt.Errorf("resolve scoped movie before release grab: %w", err)
		}
		matchingMovies := make([]radarr.Movie, 0, 1)
		for _, movie := range movies {
			if movie.ID > 0 && movie.TmdbID == params.TmdbID {
				matchingMovies = append(matchingMovies, movie)
			}
		}
		if len(matchingMovies) != 1 {
			return mutationNotStarted("tmdb_id did not resolve to exactly one matching movie in the selected Radarr instance")
		}
		releases, err := rc.SearchReleases(matchingMovies[0].ID)
		if err != nil {
			return "", fmt.Errorf("refresh scoped movie releases before grab: %w", err)
		}
		guid, err := resolveFreshReleaseGUID(params.GUID, params.IndexerID, radarrReleaseCapabilities(releases))
		if err != nil {
			return "", err
		}
		return GrabReleaseHelper(rc, nil, nil, params.MediaType, guid, params.IndexerID, 0)

	case "tv":
		if params.TmdbID <= 0 || params.SeasonNumber == nil || *params.SeasonNumber < 0 || params.BookID != 0 ||
			(params.EpisodeNumber != nil && *params.EpisodeNumber <= 0) {
			return mutationNotStarted("TV grab_release requires a positive tmdb_id, a non-negative season_number, and optionally a positive episode_number")
		}
		if sc == nil {
			return mutationNotStarted("Sonarr is not configured for the selected instance")
		}
		allSeries, err := sc.GetAllSeries()
		if err != nil {
			return "", fmt.Errorf("resolve scoped series before release grab: %w", err)
		}
		matchingSeries := make([]sonarr.Series, 0, 1)
		for _, series := range allSeries {
			if series.ID > 0 && series.TmdbID == params.TmdbID {
				matchingSeries = append(matchingSeries, series)
			}
		}
		if len(matchingSeries) != 1 {
			return mutationNotStarted("tmdb_id did not resolve to exactly one matching series in the selected Sonarr instance")
		}

		var releases []sonarr.Release
		if params.EpisodeNumber != nil {
			episodes, err := sc.GetEpisodes(matchingSeries[0].ID, *params.SeasonNumber)
			if err != nil {
				return "", fmt.Errorf("resolve scoped episode before release grab: %w", err)
			}
			matchingEpisodes := make([]sonarr.Episode, 0, 1)
			for _, episode := range episodes {
				if episode.ID > 0 && episode.SeasonNumber == *params.SeasonNumber && episode.EpisodeNumber == *params.EpisodeNumber {
					matchingEpisodes = append(matchingEpisodes, episode)
				}
			}
			if len(matchingEpisodes) != 1 {
				return mutationNotStarted("season_number and episode_number did not resolve to exactly one matching episode in the selected Sonarr instance")
			}
			releases, err = sc.SearchEpisodeReleases(matchingEpisodes[0].ID)
		} else {
			releases, err = sc.SearchReleases(matchingSeries[0].ID, *params.SeasonNumber)
		}
		if err != nil {
			return "", fmt.Errorf("refresh scoped TV releases before grab: %w", err)
		}
		guid, err := resolveFreshReleaseGUID(params.GUID, params.IndexerID, sonarrReleaseCapabilities(releases))
		if err != nil {
			return "", err
		}
		return GrabReleaseHelper(nil, sc, nil, params.MediaType, guid, params.IndexerID, 0)

	case "book":
		if params.BookID <= 0 || params.TmdbID != 0 || params.SeasonNumber != nil || params.EpisodeNumber != nil {
			return mutationNotStarted("book grab_release requires only a positive book_id as its media scope")
		}
		if cc == nil {
			return mutationNotStarted("Chaptarr is not configured for the selected instance")
		}
		releases, err := cc.SearchReleases(params.BookID)
		if err != nil {
			return "", fmt.Errorf("refresh scoped book releases before grab: %w", err)
		}
		guid, err := resolveFreshReleaseGUID(params.GUID, params.IndexerID, chaptarrReleaseCapabilities(releases))
		if err != nil {
			return "", err
		}
		return GrabReleaseHelper(nil, nil, cc, params.MediaType, guid, params.IndexerID, 0)

	default:
		return mutationNotStarted("media_type must be \"movie\", \"tv\", or \"book\"")
	}
}
