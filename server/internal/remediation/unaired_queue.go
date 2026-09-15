package remediation

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/mcp"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// normalizeLiveAction narrows new proposals and pending approvals using live
// episode dates. The final mutation repeats the check; no persisted snapshot or
// agent-authored rationale is an authority on whether an episode has aired.
func (s *Service) normalizeLiveAction(issueID int64, kind ActionKind, params json.RawMessage) (json.RawMessage, error) {
	if s.registry == nil || (kind != ActionRemediateQueue && kind != ActionTriggerSearch) {
		return params, nil
	}
	var queue RemediateQueueParams
	var search TriggerSearchParams
	if kind == ActionRemediateQueue {
		if err := json.Unmarshal(params, &queue); err != nil {
			return nil, err
		}
		if queue.MediaType != "tv" || queue.Action != "blocklist_search" {
			return params, nil
		}
	} else {
		if err := json.Unmarshal(params, &search); err != nil {
			return nil, err
		}
		if search.MediaType != "tv" {
			return params, nil
		}
	}
	if err := s.validateActionScope(issueID, kind, params); err != nil {
		return nil, err
	}
	e := NewExecutor(s.registry, s.bridge, s.db)
	ic, err := e.loadIssueContext(issueID)
	if err != nil {
		return nil, err
	}
	rc, sc, cc, lc, err := e.clientsFor(ic)
	if err != nil {
		return nil, err
	}
	if sc == nil {
		return nil, fmt.Errorf("the issue's Sonarr instance is not configured")
	}
	if kind == ActionTriggerSearch {
		return params, e.validateTVSearchDates(ic, search, sc)
	}
	if err := e.validateQueueItem("tv", queue.QueueID, ic, rc, sc, cc, lc); err != nil {
		return nil, err
	}
	_, queue.Action, _, err = mcp.ResolveSonarrQueueAction(sc, queue.QueueID, queue.Action)
	if err != nil {
		return nil, err
	}
	return canonicalJSON(queue)
}

// Agent remediation must not replace a suppressed queue search with a separate
// trigger_search. Manual searches outside the remediation workflow retain their
// existing policy. A broad search that includes unaired episodes is refused too.
func (e *Executor) validateTVSearchDates(ic issueContext, p TriggerSearchParams, client *sonarr.Client) error {
	series, err := e.resolveIssueSeries(ic, client)
	if err != nil {
		return err
	}
	if series == nil {
		return fmt.Errorf("the issue's series is no longer in Sonarr")
	}
	episodes, err := client.GetAllEpisodes(series.ID)
	if err != nil {
		return err
	}
	matched := false
	for _, ep := range episodes {
		if p.Season != nil && ep.SeasonNumber != *p.Season {
			continue
		}
		if p.Episode != nil && ep.EpisodeNumber != *p.Episode {
			continue
		}
		if ep.SeriesID != series.ID || ep.ID <= 0 {
			return fmt.Errorf("could not verify search episode identity")
		}
		matched = true
		if sonarr.HasUnairedEpisode([]sonarr.Episode{ep}, time.Now().UTC()) {
			return fmt.Errorf("S%02dE%02d has not aired yet; leave monitoring unchanged and do not search for a replacement", ep.SeasonNumber, ep.EpisodeNumber)
		}
	}
	if !matched {
		return fmt.Errorf("could not find the search's episode scope in Sonarr")
	}
	return nil
}

const removedWaitingForAirResolution = "The bad download was removed and blocklisted without searching for a replacement. The episode has not aired yet; its monitoring is unchanged."

// unairedQueueRemovalProven is the automatic incident's intended zero-file
// outcome. Callers have already settled absence of the exact issue scope. A
// complete download-history read additionally checks pack siblings, including
// ones no longer in the queue, so an aired library gap is never called resolved.
func (s *Service) unairedQueueRemovalProven(issue *Issue) (bool, error) {
	if issue == nil || issue.Source != SourceAuto || issue.MediaType != "tv" ||
		s.registry == nil || issue.InstanceID == "" || issue.EpisodeNumber <= 0 || issue.SeasonNumber < 0 {
		return false, nil
	}
	var baseline sql.NullBool
	var baselineFileID sql.NullInt64
	var captured sql.NullTime
	if err := s.db.QueryRow("SELECT baseline_has_file, baseline_file_id, baseline_captured_at FROM issue_observations WHERE issue_id=?", issue.ID).Scan(&baseline, &baselineFileID, &captured); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if !captured.Valid || !baseline.Valid || baseline.Bool || (baselineFileID.Valid && baselineFileID.Int64 > 0) {
		return false, nil
	}
	var params, downloadID string
	err := s.db.QueryRow(`SELECT COALESCE(NULLIF(approved_params, ''), params), COALESCE(target_download_id, '')
		FROM agent_actions WHERE issue_id=? AND kind='remediate_queue' AND status='executed' AND executed_at IS NOT NULL
		ORDER BY executed_at DESC, id DESC LIMIT 1`, issue.ID).Scan(&params, &downloadID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var p RemediateQueueParams
	if json.Unmarshal([]byte(params), &p) != nil || p.MediaType != "tv" || p.Action != "blocklist_only" || downloadID == "" {
		return false, nil
	}
	client, err := s.registry.GetSonarrClient(issue.InstanceID)
	if err != nil {
		return false, err
	}
	queue, err := client.GetQueueDetailed()
	if err != nil {
		return false, err
	}
	for _, item := range queue {
		if item.ID == p.QueueID || strings.EqualFold(item.DownloadID, downloadID) {
			return false, nil
		}
	}
	e := NewExecutor(s.registry, s.bridge, s.db)
	ic, err := e.loadIssueContext(issue.ID)
	if err != nil {
		return false, err
	}
	series, err := e.resolveIssueSeries(ic, client)
	if err != nil || series == nil {
		return false, err
	}
	episodes, err := client.DownloadEpisodes(downloadID)
	if err != nil {
		return false, err
	}
	matched := false
	now := time.Now().UTC()
	for _, ep := range episodes {
		unaired := sonarr.HasUnairedEpisode([]sonarr.Episode{ep}, now)
		hasFile := ep.HasFile || ep.EpisodeFileID > 0
		if ep.SeriesID == series.ID && ep.SeasonNumber == issue.SeasonNumber && ep.EpisodeNumber == issue.EpisodeNumber {
			if hasFile || !unaired {
				return false, nil
			}
			matched = true
		}
		if !hasFile && !unaired {
			return false, nil // includes unknown dates: an aired/unknown pack gap remains.
		}
	}
	return matched, nil
}
