package remediation

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// The truncated-import sentinel: the failure class the Doctor structurally
// cannot see. A sample clip or truncated encode that IMPORTS leaves the queue
// looking healthy — the file sits in the library at a fraction of the
// episode's runtime until a human presses play and finds twelve minutes of
// show. The evidence is the arr's own ffprobe runtime, read at the only
// moment that matters: the import that just announced itself.
//
// Advisory-first, deliberately: these notices open at needs_admin with the
// SAMPLE problem label (one cause, one label — the prevention rollup and its
// min-size advice already speak for it) and no agent run. Running the agent
// on an episode-scoped, queue-less auto issue needs its own recovery proof —
// the same design the pre-air class earned — and that upgrade is a follow-up
// with the state machine mapped, not a bolt-on.
const (
	suspectImportDedupePrefix = "suspect-import:"
	// suspectRuntimeFraction is the conservative line: a file under 40% of the
	// series' own runtime is not a shorter-than-usual episode, it is not the
	// episode. Specials and varying runtimes stay clear of it.
	suspectRuntimeFraction = 0.40
)

func suspectImportScopeKey(instanceID string, tvdbID, season, episode int) string {
	return fmt.Sprintf("%s%s:tvdb-%d-s%d-e%d", suspectImportDedupePrefix, instanceID, tvdbID, season, episode)
}

// parseArrRunTime reads the "mm:ss" / "h:mm:ss" runtime the arr's mediaInfo
// carries. Zero on anything unparseable — no verdict without evidence.
func parseArrRunTime(v string) time.Duration {
	parts := strings.Split(strings.TrimSpace(v), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0
	}
	total := 0
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return 0
		}
		total = total*60 + n
	}
	return time.Duration(total) * time.Second
}

// RecordSuspectImport implements the webhook seam. Errors are logged, never
// returned — a webhook must answer the service quickly.
func (s *Service) RecordSuspectImport(instanceID string, tvdbID, tmdbID, seasonNumber, episodeNumber int, title string) {
	if err := s.recordSuspectImport(instanceID, tvdbID, tmdbID, seasonNumber, episodeNumber, title); err != nil {
		log.Printf("remediation: suspect-import check for %s S%dE%d: %v", instanceID, seasonNumber, episodeNumber, err)
	}
}

func (s *Service) recordSuspectImport(instanceID string, tvdbID, tmdbID, seasonNumber, episodeNumber int, title string) error {
	if s.registry == nil || instanceID == "" || seasonNumber < 0 || episodeNumber <= 0 {
		return nil
	}
	settings := s.Settings()
	if !settings.Enabled || !settings.AutoDispatch {
		return nil
	}
	client, err := s.registry.GetSonarrClient(instanceID)
	if err != nil {
		return fmt.Errorf("resolve sonarr client: %w", err)
	}
	series, err := s.resolvePreAirSeries(client, tvdbID, tmdbID)
	if err != nil || series == nil {
		return err
	}
	if series.Runtime <= 0 {
		return nil // no honest baseline to judge against.
	}
	episodes, err := client.GetEpisodes(series.ID, seasonNumber)
	if err != nil {
		return fmt.Errorf("read season: %w", err)
	}
	fileID := 0
	for _, ep := range episodes {
		if ep.EpisodeNumber == episodeNumber && ep.HasFile && ep.EpisodeFileID > 0 {
			fileID = ep.EpisodeFileID
			break
		}
	}
	if fileID == 0 {
		return nil
	}
	files, err := client.GetEpisodeFiles(series.ID)
	if err != nil {
		return fmt.Errorf("read files: %w", err)
	}
	var runTime time.Duration
	analyzed := false
	for _, f := range files {
		if f.ID != fileID {
			continue
		}
		if f.MediaInfo == nil {
			return nil // not analyzed yet: blindness, and no verdict without evidence.
		}
		analyzed = true
		runTime = parseArrRunTime(f.MediaInfo.RunTime)
		break
	}
	if !analyzed || runTime <= 0 {
		return nil
	}
	expected := time.Duration(series.Runtime) * time.Minute
	if float64(runTime) >= float64(expected)*suspectRuntimeFraction {
		return nil
	}

	key := suspectImportScopeKey(instanceID, series.TvdbID, seasonNumber, episodeNumber)
	detail := secrets.RedactText(fmt.Sprintf(
		"The file just imported for S%02dE%02d runs %s, against this show's own %d-minute runtime — under %.0f%%, which is not a short episode, it is not the episode. Sonarr's analysis of the file on disk is the evidence.",
		seasonNumber, episodeNumber, runTime.Round(time.Second), series.Runtime, suspectRuntimeFraction*100,
	))
	now := time.Now().UTC()
	res, err := s.db.Exec(
		`INSERT INTO issues
		 (source, status, media_type, tmdb_id, tvdb_id, title, season_number, episode_number,
		  instance_id, detail, problem_kind, dedupe_key, read, created_at, updated_at)
		 SELECT ?, ?, 'tv', ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?
		 WHERE NOT EXISTS (SELECT 1 FROM issues WHERE dedupe_key = ? AND closed_at IS NULL)`,
		SourceAuto, IssueNeedsAdmin, series.TmdbID, sqlNullInt(series.TvdbID), secrets.RedactText(title),
		seasonNumber, episodeNumber, instanceID, detail, arr.ProblemSample, key, now, now, key,
	)
	if err != nil {
		return fmt.Errorf("open suspect-import issue: %w", err)
	}
	if inserted, _ := res.RowsAffected(); inserted == 0 {
		return nil
	}
	var issueID int64
	if err := s.db.QueryRow("SELECT id FROM issues WHERE dedupe_key = ? AND closed_at IS NULL", key).Scan(&issueID); err != nil {
		return fmt.Errorf("reload suspect-import issue: %w", err)
	}
	s.queueIssueAlert(issueID, now)
	return nil
}
