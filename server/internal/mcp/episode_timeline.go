package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// get_episode_timeline lays a TV season out episode by episode: when each one
// airs, whether it has aired, and what file the library holds for it.
//
// It exists because none of the other read tools can see an episode. get_library
// reports "29/164 episodes", get_queue is empty once a download has imported,
// and get_history only shows events. A report of "this is the wrong episode"
// arrives long after the queue emptied, so the only evidence left is the shape
// of the season itself — and the single most telling fact in that shape is a
// file the service imported before the episode it claims to be had aired.

// timelineMaxEpisodeLines bounds one rendering. A season past this is a daily
// soap or an anime run, and the finding does not need every line to land.
const timelineMaxEpisodeLines = 60

func (s *ToolServer) getEpisodeTimeline(input json.RawMessage, instanceID string) (*ToolResult, error) {
	var params struct {
		MediaType    string `json:"media_type"`
		TmdbID       int    `json:"tmdb_id"`
		TvdbID       int    `json:"tvdb_id"`
		SeasonNumber int    `json:"season_number"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	if params.MediaType != "" && params.MediaType != "tv" {
		return &ToolResult{Text: "An episode timeline exists only for TV."}, nil
	}
	client := s.GetSonarrFor(instanceID)
	if client == nil {
		return &ToolResult{Text: "Sonarr is not configured."}, nil
	}
	scope := mediaReadScope{TmdbID: params.TmdbID, TvdbID: params.TvdbID}
	if !scope.hasTitleIdentity() {
		return &ToolResult{Text: "get_episode_timeline needs a series: pass tmdb_id."}, nil
	}
	series := resolveScopedSeries(s.bridge, client, scope)
	if series == nil {
		return &ToolResult{Text: "That series is not in the Sonarr library."}, nil
	}

	episodes, err := client.GetAllEpisodes(series.ID)
	if err != nil {
		return nil, err
	}
	files, err := client.GetEpisodeFiles(series.ID)
	if err != nil {
		return nil, err
	}
	fileByID := make(map[int]sonarr.EpisodeFile, len(files))
	for _, f := range files {
		fileByID[f.ID] = f
	}

	now := time.Now().UTC()
	bySeason := groupEpisodeStates(episodes)
	result := &ToolResult{Text: renderEpisodeTimeline(series, params.TmdbID, params.SeasonNumber, bySeason, fileByID, now)}
	result.Verification = seasonCleanVerification(bySeason, fileByID, params.SeasonNumber, now)
	return result, nil
}

// seasonCleanVerification is the server's own answer to "is the impossible
// content still there", emitted out of band so the runner never has to believe
// the model's reading of this text.
//
// Only for a read scoped to ONE season: that is the shape of the incident, and
// a series-wide rollup would answer a question nobody asked. A season Sonarr
// does not hold gets no verdict at all rather than a clean one — an absent
// season must never read as a repaired season.
func seasonCleanVerification(bySeason map[int][]arr.EpisodeState, fileByID map[int]sonarr.EpisodeFile, seasonNumber int, now time.Time) *ToolVerification {
	if seasonNumber <= 0 {
		return nil
	}
	states, ok := bySeason[seasonNumber]
	if !ok || len(states) == 0 {
		return nil
	}
	timeline := arr.BuildSeasonTimeline(seasonNumber, withImportTimes(states, fileByID), now)
	return &ToolVerification{
		Kind:          VerificationSeasonClean,
		ExactScope:    true,
		TargetPresent: timeline.PreAirFill,
	}
}

// groupEpisodeStates converts Sonarr's episode records into the service-neutral
// facts the detector reasons about, keyed by season.
func groupEpisodeStates(episodes []sonarr.Episode) map[int][]arr.EpisodeState {
	out := make(map[int][]arr.EpisodeState)
	for _, ep := range episodes {
		out[ep.SeasonNumber] = append(out[ep.SeasonNumber], arr.EpisodeState{
			Number:  ep.EpisodeNumber,
			Title:   ep.Title,
			AirsAt:  ep.AirDateUtc,
			HasFile: ep.HasFile,
			FileID:  ep.EpisodeFileID,
		})
	}
	return out
}

// withImportTimes stamps each episode's file import time onto its state. The
// import time lives on the episode FILE, not the episode, so it can only be
// filled in once both reads are joined — and it is the fact the whole detector
// turns on, so a missing join silently disarms the finding.
func withImportTimes(states []arr.EpisodeState, fileByID map[int]sonarr.EpisodeFile) []arr.EpisodeState {
	out := make([]arr.EpisodeState, len(states))
	copy(out, states)
	for i := range out {
		if !out[i].HasFile || out[i].FileID <= 0 {
			continue
		}
		if file, ok := fileByID[out[i].FileID]; ok {
			out[i].ImportedAt = file.DateAdded
		}
	}
	return out
}

func renderEpisodeTimeline(series *sonarr.Series, tmdbID, seasonNumber int, bySeason map[int][]arr.EpisodeState, fileByID map[int]sonarr.EpisodeFile, now time.Time) string {
	if len(bySeason) == 0 {
		return fmt.Sprintf("%s has no episodes in Sonarr yet.", series.Title)
	}
	if seasonNumber > 0 {
		states := withImportTimes(bySeason[seasonNumber], fileByID)
		if len(states) == 0 {
			return fmt.Sprintf("%s has no season %d in Sonarr.", series.Title, seasonNumber)
		}
		return renderOneSeason(series, tmdbID, arr.BuildSeasonTimeline(seasonNumber, states, now), states, fileByID, now)
	}

	// No season in scope: a rollup for the whole series, then full detail for the
	// season that trips the finding — the agent needs the episode list to name
	// the episodes in a fix, and printing every season would bury it.
	seasons := make([]int, 0, len(bySeason))
	for season := range bySeason {
		seasons = append(seasons, season)
	}
	sort.Ints(seasons)

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s — %d season(s). Now %s.", series.Title, len(seasons), now.Format(time.RFC3339))
	flagged := -1
	for _, season := range seasons {
		states := withImportTimes(bySeason[season], fileByID)
		t := arr.BuildSeasonTimeline(season, states, now)
		fmt.Fprintf(&sb, "\n- Season %d: %d episode(s), %d aired, %d with files", season, t.Total, t.Aired, t.FilesTotal)
		if len(t.UnairedWithFile) > 0 {
			fmt.Fprintf(&sb, " — %d file(s) for episodes that have NOT aired", len(t.UnairedWithFile))
		}
		if len(t.AiredMissing) > 0 {
			fmt.Fprintf(&sb, " — %d aired episode(s) missing", len(t.AiredMissing))
		}
		if t.PreAirFill && season > flagged {
			flagged = season
		}
	}
	if flagged < 0 {
		sb.WriteString("\nNo season holds files for episodes that have not aired.")
		return sb.String()
	}
	states := withImportTimes(bySeason[flagged], fileByID)
	sb.WriteString("\n\n")
	sb.WriteString(renderOneSeason(series, tmdbID, arr.BuildSeasonTimeline(flagged, states, now), states, fileByID, now))
	return sb.String()
}

func renderOneSeason(series *sonarr.Series, tmdbID int, timeline arr.SeasonTimeline, states []arr.EpisodeState, fileByID map[int]sonarr.EpisodeFile, now time.Time) string {
	ordered := make([]arr.EpisodeState, len(states))
	copy(ordered, states)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Number < ordered[j].Number })

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s season %d — %d episode(s), %d aired, %d with files. Now %s.",
		series.Title, timeline.Season, timeline.Total, timeline.Aired, timeline.FilesTotal, now.Format(time.RFC3339))

	shown := ordered
	if len(shown) > timelineMaxEpisodeLines {
		shown = shown[:timelineMaxEpisodeLines]
	}
	for _, ep := range shown {
		fmt.Fprintf(&sb, "\n- E%02d", ep.Number)
		if ep.Title != "" {
			fmt.Fprintf(&sb, " %q", ep.Title)
		}
		// Same predicates the detector reasons with, not a second copy of the
		// rule: a line that reads "aired" under a finding that counted it as
		// unaired would make the evidence contradict itself.
		switch {
		case ep.AirsAt == nil:
			sb.WriteString(" — no air date")
		case ep.Unaired(now):
			fmt.Fprintf(&sb, " — airs %s (NOT YET AIRED)", ep.AirsAt.UTC().Format(time.RFC3339))
		default:
			fmt.Fprintf(&sb, " — aired %s", ep.AirsAt.UTC().Format(time.RFC3339))
		}
		if !ep.HasFile {
			sb.WriteString(" · no file")
			continue
		}
		file := fileByID[ep.FileID]
		if ep.ImportedAt != nil {
			fmt.Fprintf(&sb, " · file imported %s", ep.ImportedAt.UTC().Format(time.RFC3339))
		} else {
			sb.WriteString(" · has a file")
		}
		if name := file.Quality.Quality.Name; name != "" {
			fmt.Fprintf(&sb, " [%s]", name)
		}
		if file.SceneName != "" {
			fmt.Fprintf(&sb, " %s", file.SceneName)
		} else if file.RelativePath != "" {
			fmt.Fprintf(&sb, " %s", file.RelativePath)
		}
	}
	if len(ordered) > len(shown) {
		fmt.Fprintf(&sb, "\n…and %d more episode(s).", len(ordered)-len(shown))
	}

	fmt.Fprintf(&sb, "\n\n%s", timeline.Transparency)
	if len(timeline.AiredMissing) > 0 {
		fmt.Fprintf(&sb, "\nAired and missing a file: %s.", episodeNumberList(timeline.AiredMissing))
	} else {
		sb.WriteString("\nNo aired episode of this season is missing a file.")
	}

	if timeline.PreAirFill && len(timeline.ImportedBeforeAir) > 0 {
		// Same prescriptive shape diagnose_queue already uses: name the exact
		// next call. ONE call — this is one problem with one repair, and asking
		// an admin to approve the second half of a decision they already made is
		// not a safety property, it is a worse product.
		fmt.Fprintf(&sb, "\n→ next: propose_action delete_media_files {\"media_type\": \"tv\", \"tmdb_id\": %d, \"season\": %d, \"episodes\": [%s], \"blocklist\": true}",
			tmdbID, timeline.Season, episodeNumberList(timeline.ImportedBeforeAir))
		sb.WriteString("\n  That one fix deletes those files, blocks the releases that delivered them, and searches the episodes that have already aired — the ones still to come are left for the service to grab as they air. Do not propose a separate search afterwards.")
	}
	return sb.String()
}

func episodeNumberList(numbers []int) string {
	parts := make([]string, 0, len(numbers))
	for _, n := range numbers {
		parts = append(parts, fmt.Sprintf("%d", n))
	}
	return strings.Join(parts, ", ")
}
