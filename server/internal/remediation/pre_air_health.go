package remediation

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// Catching a season the service filled before it aired at IMPORT rather than at
// complaint.
//
// The detector, the read tool and the repair all shipped for the reported path:
// a person notices wrong episodes, files a report, and the agent works it out.
// On the incident that prompted all of it, noticing took thirteen days and eight
// more impossible files. Nothing about the finding needs a human — the fact that
// makes it decidable, an episode's own air date against the moment a file
// claiming to be it arrived, is in the import event itself.
//
// So this is the same detector, run at the only moment that matters, and it
// opens ONE issue for the SEASON. A pack that imports as ten episodes is one
// problem and gets one investigation and one approval, the same rule the repair
// itself follows.

const (
	// preAirDedupePrefix namespaces the season scope key. Distinct from the
	// queue observer's incidentScopeKey namespace on purpose: this is not a
	// queue incident, it has no issue_observations row, and the two must never
	// collide on one key.
	preAirDedupePrefix = "pre-air:"
	// preAirCatchUpWindow bounds the fallback sweep's first pass after a
	// restart. Long enough to catch a batch that landed while Cantinarr was
	// down, short enough that a cold start is not a library scan.
	preAirCatchUpWindow = 6 * time.Hour
	// preAirSweepMaxSeasons bounds how many distinct seasons one fallback pass
	// will read. A real batch is one season; anything past this is a bulk
	// re-import, which is not what this detector is for.
	preAirSweepMaxSeasons = 8
	// preAirSweepHistoryPage is how much recent history one pass reads. One arr
	// call per instance per pass, whatever the library holds.
	preAirSweepHistoryPage = 100
	// sonarrImportEventType is how Sonarr renders a completed import on a
	// history READ (a string, not the numeric code the eventType query
	// parameter takes).
	sonarrImportEventType = "downloadFolderImported"
)

// preAirScopeKey identifies one SEASON on one instance. Season-scoped is the
// whole point: nine impossible files in one season are one problem, and keying
// per episode would open nine issues, run the agent nine times, and ask an admin
// to approve one decision nine times.
func preAirScopeKey(instanceID string, tvdbID, tmdbID, season int) string {
	// Prefer TVDB — it is what Sonarr indexes on, and it is present on every
	// webhook payload and series record. TMDB is the fallback for a series
	// Sonarr holds without one.
	identity := fmt.Sprintf("tvdb:%d", tvdbID)
	if tvdbID <= 0 {
		identity = fmt.Sprintf("tmdb:%d", tmdbID)
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s%s|tv|%s|s:%d", preAirDedupePrefix, instanceID, identity, season)))
	return hex.EncodeToString(sum[:])
}

// RecordPreAirImport implements webhooks.PreAirImportWitness. instanceID names
// the Sonarr the import landed on; the rest identifies the season.
//
// Errors are logged rather than returned: this runs off a webhook that must
// answer the service quickly and can never usefully report a failure back to it.
func (s *Service) RecordPreAirImport(instanceID string, tvdbID, tmdbID, seasonNumber int, title string) {
	if err := s.recordPreAirSeason(instanceID, tvdbID, tmdbID, seasonNumber, title); err != nil {
		log.Printf("remediation: pre-air check for %s season %d: %v", instanceID, seasonNumber, err)
	}
}

// recordPreAirSeason runs the detector over one live season and, only when it
// finds content that cannot exist, opens or refreshes the season's issue.
//
// The caller has only established that ONE file arrived early, which is an
// everyday air-date slip. Whether that is a season of impossible content is the
// detector's call, made over the whole season, and this defers to it entirely.
func (s *Service) recordPreAirSeason(instanceID string, tvdbID, tmdbID, seasonNumber int, title string) error {
	if s.registry == nil || instanceID == "" || seasonNumber < 0 {
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
	timeline, err := s.preAirSeasonTimeline(client, series.ID, seasonNumber)
	if err != nil {
		return err
	}
	if !timeline.PreAirFill {
		return nil
	}
	if title == "" {
		title = series.Title
	}
	return s.openPreAirIssue(instanceID, series.TvdbID, series.TmdbID, seasonNumber, title, timeline)
}

// resolvePreAirSeries finds the library record the identity in an import event
// refers to. A series Sonarr does not hold is not an error — it is a webhook for
// something that has since been removed.
func (s *Service) resolvePreAirSeries(client *sonarr.Client, tvdbID, tmdbID int) (*sonarr.Series, error) {
	if tvdbID > 0 {
		series, err := client.GetSeriesByTVDB(tvdbID)
		if err != nil {
			return nil, fmt.Errorf("resolve series by tvdb: %w", err)
		}
		if series != nil {
			return series, nil
		}
	}
	if tmdbID <= 0 {
		return nil, nil
	}
	all, err := client.GetAllSeries()
	if err != nil {
		return nil, fmt.Errorf("resolve series by tmdb: %w", err)
	}
	for i := range all {
		if all[i].TmdbID == tmdbID {
			return &all[i], nil
		}
	}
	return nil, nil
}

// preAirSeasonTimeline reads one live season and builds its timeline. Split out
// so the webhook witness and the fallback sweep share exactly one definition of
// what is being measured.
func (s *Service) preAirSeasonTimeline(client *sonarr.Client, seriesID, seasonNumber int) (arr.SeasonTimeline, error) {
	episodes, err := client.GetEpisodes(seriesID, seasonNumber)
	if err != nil {
		return arr.SeasonTimeline{}, fmt.Errorf("read season %d episodes: %w", seasonNumber, err)
	}
	files, err := client.GetEpisodeFiles(seriesID)
	if err != nil {
		return arr.SeasonTimeline{}, fmt.Errorf("read season %d files: %w", seasonNumber, err)
	}
	importedAt := make(map[int]*time.Time, len(files))
	for _, f := range files {
		importedAt[f.ID] = f.DateAdded
	}
	states := make([]arr.EpisodeState, 0, len(episodes))
	for _, ep := range episodes {
		state := arr.EpisodeState{
			Number:  ep.EpisodeNumber,
			Title:   ep.Title,
			AirsAt:  ep.AirDateUtc,
			HasFile: ep.HasFile,
			FileID:  ep.EpisodeFileID,
		}
		if state.HasFile && state.FileID > 0 {
			state.ImportedAt = importedAt[state.FileID]
		}
		states = append(states, state)
	}
	return arr.BuildSeasonTimeline(seasonNumber, states, time.Now().UTC()), nil
}

// openPreAirIssue records the finding as ONE auto issue for the season.
//
// It is opened at IssueOpen, not IssueNeedsAdmin: the agent is what turns a
// finding into a repair proposal, and recoverWork only enqueues open and
// investigating issues. problem_kind is set — unlike the three system health
// sinks, which leave it NULL and are therefore unreachable by a standing rule —
// so an admin who trusts this repair can arm one.
//
// Deliberately writes no issue_observations row. This is not a queue incident;
// the observation sweeper must not adopt it, and it recovers by its own proof.
func (s *Service) openPreAirIssue(instanceID string, tvdbID, tmdbID, seasonNumber int, title string, timeline arr.SeasonTimeline) error {
	scopeKey := preAirScopeKey(instanceID, tvdbID, tmdbID, seasonNumber)
	detail := secrets.RedactText(timeline.Transparency)
	safeTitle := secrets.RedactText(title)
	now := time.Now().UTC()

	// read = 0: unlike a queue observation, which starts silent inside a tracking
	// window and only marks itself unread if it survives to promotion, this
	// finding is already terminal the moment it is made — the files exist and
	// cannot become genuine.
	res, err := s.db.Exec(
		`INSERT INTO issues
		 (source, status, media_type, tmdb_id, tvdb_id, title, season_number, episode_number,
		  instance_id, detail, problem_kind, dedupe_key, read, created_at, updated_at)
		 SELECT ?, ?, 'tv', ?, ?, ?, ?, 0, ?, ?, ?, ?, 0, ?, ?
		 WHERE NOT EXISTS (SELECT 1 FROM issues WHERE dedupe_key = ? AND closed_at IS NULL)`,
		SourceAuto, IssueOpen, tmdbID, sqlNullInt(tvdbID), safeTitle, seasonNumber,
		instanceID, detail, arr.ProblemPreAirSeasonFill, scopeKey, now, now, scopeKey,
	)
	if err != nil {
		return fmt.Errorf("open pre-air issue: %w", err)
	}
	if inserted, _ := res.RowsAffected(); inserted == 0 {
		// The season already has an open issue. Refresh what it says and count
		// the recurrence; never a second row for one problem.
		if _, err := s.db.Exec(
			`UPDATE issues SET detail = ?, occurrences = occurrences + 1, updated_at = ?
			 WHERE dedupe_key = ? AND closed_at IS NULL`,
			detail, now, scopeKey,
		); err != nil {
			return fmt.Errorf("refresh pre-air issue: %w", err)
		}
		return nil
	}

	var issueID int64
	if err := s.db.QueryRow(
		"SELECT id FROM issues WHERE dedupe_key = ? AND closed_at IS NULL", scopeKey,
	).Scan(&issueID); err != nil {
		return fmt.Errorf("reload pre-air issue: %w", err)
	}
	// Held, not pushed. A batch import fires a webhook per file within minutes
	// and none of this is urgent; the hold-down coalesces and drops the page
	// entirely if the issue closes inside the window.
	s.queueIssueAlert(issueID, now)
	s.Enqueue(issueID)
	return nil
}

// preAirRepairProven is this incident class's own recovery proof: the season no
// longer holds a file for an episode that has not aired.
//
// It needs its own proof because it can reach neither of the existing two.
// exactRecoveryProven and upgradeAbandonProven both read issue_observations,
// and both call exactIssueFileState, which fails closed on episode_number == 0
// — a season-scoped issue is structurally unprovable there. That is correct for
// a queue incident, where "which exact file" is the whole question; it is simply
// the wrong question here, where the answer is about a season.
//
// Live, typed, and computed by the server from air dates and file identity. The
// agent's reading of its own tool output never enters into it.
func (s *Service) preAirRepairProven(issue *Issue) (bool, error) {
	if issue == nil || issue.MediaType != "tv" || issue.SeasonNumber < 0 || issue.InstanceID == "" {
		return false, nil
	}
	problemKind, err := s.storedProblemKind(issue.ID)
	if err != nil {
		return false, err
	}
	if problemKind != arr.ProblemPreAirSeasonFill {
		return false, nil
	}
	if s.registry == nil {
		return false, nil
	}
	client, err := s.registry.GetSonarrClient(issue.InstanceID)
	if err != nil {
		return false, err
	}
	series, err := s.resolvePreAirSeries(client, issue.TvdbID, issue.TmdbID)
	if err != nil || series == nil {
		return false, err
	}
	timeline, err := s.preAirSeasonTimeline(client, series.ID, issue.SeasonNumber)
	if err != nil {
		return false, err
	}
	// Not merely "the finding no longer trips": the threshold is two, so one
	// impossible file left behind would read as repaired. Nothing that has not
	// aired may still hold a file.
	return len(timeline.UnairedWithFile) == 0, nil
}

// stampUserContentLabel gives a season-scoped USER wrong-content report the
// same typed label the auto detector writes, when the server's own season
// check trips: files the service imported before their episodes aired. Called
// at proposal time for delete_media_files — the moment a label first matters
// for rule matching — and computed entirely from live arr facts, never from
// the model's narration. First verdict wins.
func (s *Service) stampUserContentLabel(issue *Issue) {
	if issue == nil || issue.Source != SourceUser || issue.MediaType != "tv" ||
		issue.SeasonNumber <= 0 || issue.EpisodeNumber != 0 || s.registry == nil {
		return
	}
	client, err := s.registry.GetSonarrClient(issue.InstanceID)
	if err != nil {
		return
	}
	series, err := s.resolvePreAirSeries(client, issue.TvdbID, issue.TmdbID)
	if err != nil || series == nil {
		return
	}
	timeline, err := s.preAirSeasonTimeline(client, series.ID, issue.SeasonNumber)
	if err != nil || !timeline.PreAirFill {
		return
	}
	_, _ = s.db.Exec(
		`UPDATE issues SET problem_kind = ? WHERE id = ? AND source = ?
		   AND (problem_kind IS NULL OR problem_kind = '')`,
		arr.ProblemPreAirSeasonFill, issue.ID, SourceUser,
	)
}

// storedProblemKind reads the server-authored detector label straight from the
// issue row. Deliberately not carried on Issue: it is not part of the client
// contract, and its consumers are server-side gates (this class's recovery
// proof and the recovery preflight's class routing).
func (s *Service) storedProblemKind(issueID int64) (string, error) {
	var kind sql.NullString
	if err := s.db.QueryRow("SELECT problem_kind FROM issues WHERE id = ?", issueID).Scan(&kind); err != nil {
		return "", fmt.Errorf("read problem kind: %w", err)
	}
	return kind.String, nil
}

// probePreAirRecovery is the recovery preflight for the pre-air season class,
// standing in for the queue-shaped probe that fits every other auto incident.
// completed carries the queue probe's exact meaning — the incident's own
// recovery proof holds, so pending work is cancelled or the issue closed
// instead of dispatching over an already-repaired season. An unreadable season
// fails closed like every other preflight read: no proof, no conclusion.
func (s *Service) probePreAirRecovery(issue *Issue) (arrRecoveryProbe, error) {
	proven, err := s.preAirRepairProven(issue)
	if err != nil {
		return arrRecoveryProbe{}, err
	}
	if proven {
		return arrRecoveryProbe{completed: true}, nil
	}
	return arrRecoveryProbe{}, nil
}

// --- fallback for instances whose webhook was never configured ---

// The instant path is a Sonarr Connect webhook, and Cantinarr only installs one
// when an admin asks it to. An instance that never got one would otherwise be
// entirely uncovered, so the same check runs on a slow timer.
//
// It is not a library scan, and there is no library scan anywhere in this
// server. Recent history names the seasons something actually imported into, so
// a quiet instance costs exactly one arr call per pass and reads nothing else.
type preAirSweepState struct {
	mu   sync.Mutex
	seen map[string]time.Time // instance id -> newest import already examined
}

func (s *preAirSweepState) since(instanceID string, now time.Time) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if at, ok := s.seen[instanceID]; ok {
		return at
	}
	// First pass for this instance — usually a restart. Look back a bounded
	// window so a batch that landed while Cantinarr was down is still caught,
	// without turning a cold start into a sweep of everything.
	return now.Add(-preAirCatchUpWindow)
}

func (s *preAirSweepState) advance(instanceID string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = make(map[string]time.Time)
	}
	if at.After(s.seen[instanceID]) {
		s.seen[instanceID] = at
	}
}

type preAirSeasonRef struct {
	seriesID int
	season   int
}

// SweepPreAirImports checks every season a Sonarr instance imported into since
// the last pass. Safe to call on any schedule; repeats are free because the
// dedupe index makes a second open a no-op.
func (s *Service) SweepPreAirImports() {
	if s.registry == nil {
		return
	}
	instances, err := s.registry.ListInstanceSummaries("sonarr")
	if err != nil {
		log.Printf("remediation: pre-air sweep could not list sonarr instances: %v", err)
		return
	}
	now := time.Now().UTC()
	for _, inst := range instances {
		if err := s.sweepPreAirInstance(inst.ID, now); err != nil {
			log.Printf("remediation: pre-air sweep for %s: %v", inst.ID, err)
		}
	}
}

func (s *Service) sweepPreAirInstance(instanceID string, now time.Time) error {
	client, err := s.registry.GetSonarrClient(instanceID)
	if err != nil {
		return fmt.Errorf("resolve sonarr client: %w", err)
	}
	records, err := client.GetHistory(preAirSweepHistoryPage)
	if err != nil {
		return fmt.Errorf("read recent history: %w", err)
	}
	since := s.preAirSweep.since(instanceID, now)
	newest := since
	seen := make(map[preAirSeasonRef]struct{})
	var seasons []preAirSeasonRef
	for _, rec := range records {
		if rec.EventType != sonarrImportEventType || !rec.Date.After(since) {
			continue
		}
		if rec.Date.After(newest) {
			newest = rec.Date
		}
		if rec.Episode == nil || rec.SeriesID <= 0 {
			continue
		}
		ref := preAirSeasonRef{seriesID: rec.SeriesID, season: rec.Episode.SeasonNumber}
		if _, dup := seen[ref]; dup {
			continue
		}
		seen[ref] = struct{}{}
		if len(seasons) >= preAirSweepMaxSeasons {
			// Bounded on purpose, and said out loud rather than silently
			// truncated: a pass this busy is a bulk re-import, not the batch of
			// impossible files this detector exists for.
			log.Printf("remediation: pre-air sweep for %s capped at %d seasons; the rest wait for the next pass",
				instanceID, preAirSweepMaxSeasons)
			break
		}
		seasons = append(seasons, ref)
	}
	for _, ref := range seasons {
		series, err := client.GetSeries(ref.seriesID)
		if err != nil || series == nil {
			continue
		}
		timeline, err := s.preAirSeasonTimeline(client, ref.seriesID, ref.season)
		if err != nil || !timeline.PreAirFill {
			continue
		}
		if err := s.openPreAirIssue(instanceID, series.TvdbID, series.TmdbID, ref.season, series.Title, timeline); err != nil {
			log.Printf("remediation: pre-air sweep could not open an issue for %s season %d: %v",
				series.Title, ref.season, err)
		}
	}
	// Advance only after the pass: a read that failed above returned early and
	// left the watermark alone, so the next pass re-examines the same window
	// instead of stepping over it.
	s.preAirSweep.advance(instanceID, newest)
	return nil
}
