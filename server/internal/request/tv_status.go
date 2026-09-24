package request

import (
	"errors"
	"sort"
	"time"

	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

func tvUnknown(match *TVMatch, err error) *StatusResponse {
	known := false
	reason := "library_unavailable"
	var failure *tvMatchError
	if errors.As(err, &failure) {
		reason = failure.code
	}
	if match != nil && reason != "library_unavailable" {
		if match.State != "paused" {
			match.State = "unresolved"
		}
		match.Message = err.Error()
	}
	return &StatusResponse{Status: StatusUnavailable, StatusKnown: &known, StatusUnknownReason: reason, Match: match}
}

func (s *Service) tvLiveStatus(userID int64, tmdbID int, instanceID string) (*StatusResponse, error) {
	client, _, err := s.resolveSonarr(userID, instanceID)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return tvUnknown(nil, ErrArrInstanceInvalid), nil
	}
	m, err := s.resolveTVMatch(client, tmdbID)
	if err != nil {
		return tvUnknown(m, err), nil
	}
	series, err := client.GetSeriesByTVDB(m.TVDBID)
	if err != nil {
		return tvUnknown(m, err), nil
	}
	known := true
	out := &StatusResponse{Status: StatusUnavailable, StatusKnown: &known, Match: m}
	if series == nil {
		for source := range m.SeasonMap {
			out.Seasons = append(out.Seasons, SeasonStatus{SeasonNumber: source, Status: StatusUnavailable})
		}
		sort.Slice(out.Seasons, func(i, j int) bool { return out.Seasons[i].SeasonNumber < out.Seasons[j].SeasonNumber })
		return out, nil
	}
	m.SeriesID = series.ID
	episodes, epErr := client.GetAllEpisodes(series.ID)
	_, bySeason := sonarr.SeriesCompletion(episodes, time.Now())
	seasons := map[int]sonarr.SeasonResource{}
	for _, season := range series.Seasons {
		seasons[season.SeasonNumber] = season
	}
	epMonitored := map[int]bool{}
	for _, ep := range episodes {
		if ep.Monitored {
			epMonitored[ep.SeasonNumber] = true
		}
	}
	var total sonarr.Completion
	requested := false
	for source, target := range m.SeasonMap {
		season, exists := seasons[target]
		if !exists {
			known = false
			out.StatusUnknownReason = "tv_seasons_unmapped"
		}
		completion := bySeason[target]
		if epErr != nil {
			known = false
			out.StatusUnknownReason = "library_unavailable"
			// A failed episode read can use only this mapped season's stats.
			// Never fall back to the parent's totals or monitored-only count.
			if season.Statistics != nil {
				completion = sonarr.Completion{Files: season.Statistics.EpisodeFileCount, Aired: season.Statistics.TotalEpisodeCount}
			}
		}
		monitored := series.Monitored && (season.Monitored || epMonitored[target])
		status, progress := statusFromCompletion(completion, monitored)
		out.Seasons = append(out.Seasons, SeasonStatus{SeasonNumber: source, Status: status, Progress: progress, EpisodeFileCount: completion.Files, EpisodeCount: completion.Aired})
		total.Files += completion.Files
		total.Aired += completion.Aired
		requested = requested || monitored
	}
	out.Status, out.Progress = statusFromCompletion(total, requested)
	sort.Slice(out.Seasons, func(i, j int) bool { return out.Seasons[i].SeasonNumber < out.Seasons[j].SeasonNumber })
	if epErr == nil {
		out.tvFileIDs = countedEpisodeFiles(episodes, m.SeasonMap)
	}
	return out, nil
}

// countedEpisodeFiles lists the files behind the episodes this status counted:
// only the mapped seasons, each file once (one file can hold two episodes).
func countedEpisodeFiles(episodes []sonarr.Episode, seasonMap map[int]int) []int {
	targets := map[int]bool{}
	for _, target := range seasonMap {
		targets[target] = true
	}
	seen := map[int]bool{}
	var ids []int
	for _, ep := range episodes {
		if !targets[ep.SeasonNumber] || !ep.HasFile || ep.EpisodeFileID <= 0 || seen[ep.EpisodeFileID] {
			continue
		}
		seen[ep.EpisodeFileID] = true
		ids = append(ids, ep.EpisodeFileID)
	}
	return ids
}

// Overlay only this TMDB title's saved source seasons, preserving live files.
// Pending approval and durable delivery have independent, explicit wire data.
func (s *Service) userTVStatus(userID int64, tmdbID int, instanceID string) (*StatusResponse, error) {
	out, err := s.tvLiveStatus(userID, tmdbID, instanceID)
	if err != nil {
		return nil, err
	}
	selected := instanceID
	if selected == "" {
		selected = s.effectiveArrInstanceID(userID, "tv")
	}
	rows, err := s.db.Query(`SELECT id,status FROM request_log WHERE user_id=? AND tmdb_id=? AND media_type='tv' AND (instance_id=? OR (?='' AND instance_id IS NULL)) ORDER BY id DESC`, userID, tmdbID, selected, instanceID)
	if err != nil {
		return nil, err
	}
	type row struct {
		id     int64
		status string
	}
	saved := []row{}
	for rows.Next() {
		var r row
		if err = rows.Scan(&r.id, &r.status); err != nil {
			rows.Close()
			return nil, err
		}
		saved = append(saved, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i, r := range saved {
		if i == 0 && r.status == StatusDenied && out.Status == StatusUnavailable {
			out.Status = StatusDenied
		}
		if r.status != StatusPending {
			continue
		}
		target, _, _, e := s.loadTVTarget(r.id)
		if e != nil {
			// Legacy pending requests have no target proof. They stay subject to
			// approval; displaying the hold does not infer library availability.
			out.Status = StatusPending
			continue
		}
		delivery, e := s.tvDeliveryResponse(userID, r.id)
		if e != nil {
			return nil, e
		}
		out.Delivery = append(out.Delivery, delivery.Delivery...)
		if out.RequestID == 0 {
			out.RequestID = r.id
		}
		state := delivery.Status
		for n := range out.Seasons {
			for _, source := range target.SourceSeasons {
				if out.Seasons[n].SeasonNumber == source && out.Seasons[n].Status != StatusAvailable {
					out.Seasons[n].Status = state
				}
			}
		}
		if out.Status == StatusUnavailable || out.Status == StatusDenied {
			out.Status = state
		}
	}
	return out, nil
}
