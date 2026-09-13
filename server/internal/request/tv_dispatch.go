package request

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// TVRequestTarget is persisted before the first Sonarr mutation. SourceSeasons
// are the user's selection; TargetSeasons are a snapshot, never retranslated.
type TVRequestTarget struct {
	Match         TVMatch `json:"match"`
	SourceSeasons []int   `json:"source_seasons"`
	TargetSeasons []int   `json:"target_seasons"`
	Pilot         bool    `json:"pilot"`
}

func (s *Service) prepareTVTarget(r *resolvedRequest) (*TVRequestTarget, error) {
	client, id, err := s.resolveSonarr(r.userID, r.instanceID)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, ErrArrInstanceInvalid
	}
	r.instanceID = id
	m, err := s.resolveTVMatch(client, r.tmdbID)
	if err != nil {
		return nil, err
	}
	target, err := client.LookupByTVDB(m.TVDBID)
	if err != nil {
		return nil, tvMatchFailure("tv_metadata_unavailable")
	}
	all := make([]int, 0, len(m.SeasonMap))
	for n := range m.SeasonMap {
		all = append(all, n)
	}
	sort.Ints(all)
	if err = validateTVSeasons(m.SeasonMap, all, target.Seasons); err != nil {
		return nil, err
	}
	selected := r.seasonNumbers
	pilot := len(selected) == 0 && r.seasonScope == SeasonScopePilot
	if len(selected) == 0 {
		selected = all
		switch r.seasonScope {
		case SeasonScopeFirst, SeasonScopePilot:
			selected = all[:1]
		case SeasonScopeLatest:
			selected = all[len(all)-1:]
		}
	}
	out := &TVRequestTarget{Match: *m, SourceSeasons: append([]int(nil), selected...), Pilot: pilot}
	out.Match.TargetTitle = target.Title
	for _, n := range selected {
		dest := m.SeasonMap[n]
		if n <= 0 || dest <= 0 {
			return nil, tvMatchFailure("tv_seasons_unmapped")
		}
		out.TargetSeasons = append(out.TargetSeasons, dest)
	}
	r.tvdbID, r.title = m.TVDBID, m.Title
	existing, err := client.GetSeriesByTVDB(m.TVDBID)
	if err != nil {
		return nil, err
	}
	r.newWork = existing == nil || !existing.Monitored
	if existing != nil {
		for _, n := range out.TargetSeasons {
			for _, season := range existing.Seasons {
				if season.SeasonNumber == n && !season.Monitored {
					r.newWork = true
				}
			}
		}
	}
	return out, nil
}

func (s *Service) loadTVTarget(id int64) (*TVRequestTarget, string, int, error) {
	var raw, phase string
	var seriesID int
	if err := s.db.QueryRow(`SELECT snapshot,phase,series_id FROM request_tv_targets WHERE request_id=?`, id).Scan(&raw, &phase, &seriesID); err != nil {
		return nil, "", 0, err
	}
	var target TVRequestTarget
	if err := json.Unmarshal([]byte(raw), &target); err != nil {
		return nil, "", 0, err
	}
	return &target, phase, seriesID, nil
}

func (s *Service) createTVRequest(r *resolvedRequest, approval bool) (*CreateResponse, error) {
	target, err := s.prepareTVTarget(r)
	if err != nil {
		return nil, err
	}
	lock := s.bookLock(fmt.Sprintf("tv-intake:%d:%s:%d", r.userID, r.instanceID, r.tmdbID))
	lock.Lock()
	notifyNew := s.isNewSubmission(r)
	id, inserted, err := s.saveTVDelivery(r, target, approval, 0, 0)
	lock.Unlock()
	if err != nil {
		return nil, err
	}
	if inserted && notifyNew {
		s.notifyCreated(id, approval)
	}
	if inserted && approval && s.notifier != nil {
		s.notifier.NotifyAdmins("request_pending", map[string]interface{}{"request_id": id, "tmdb_id": r.tmdbID, "media_type": "tv", "title": r.title, "instance_id": r.instanceID})
	}
	if !approval {
		s.dispatchRequest(context.Background(), id)
		s.wakeDispatch()
	}
	return s.tvDeliveryResponse(r.userID, id)
}

func (s *Service) saveTVDelivery(r *resolvedRequest, target *TVRequestTarget, approval bool, repairOf, adminID int64) (int64, bool, error) {
	raw, err := json.Marshal(target)
	if err != nil {
		return 0, false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	var id int64
	if repairOf > 0 {
		err = tx.QueryRow(`SELECT request_id FROM request_tv_targets WHERE repair_of=?`, repairOf).Scan(&id)
	} else {
		err = tx.QueryRow(`SELECT r.id FROM request_log r JOIN request_tv_targets t ON t.request_id=r.id WHERE r.user_id=? AND r.tmdb_id=? AND r.media_type='tv' AND r.instance_id=? AND r.status='pending' AND t.snapshot=? AND COALESCE(r.quality_profile_id,0)=? ORDER BY r.id DESC LIMIT 1`, r.userID, r.tmdbID, r.instanceID, string(raw), r.qualityProfileID).Scan(&id)
	}
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	if repairOf > 0 {
		var allowed int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM request_log WHERE id=? AND user_id=? AND media_type='tv' AND tmdb_id=? AND instance_id=? AND status!='denied' AND COALESCE(season_scope,'')=? AND COALESCE(quality_profile_id,0)=?`, repairOf, r.userID, r.tmdbID, r.instanceID, r.seasonScope, r.qualityProfileID).Scan(&allowed); err != nil {
			return 0, false, err
		}
		if allowed != 1 {
			return 0, false, ErrTVMatchStale
		}
	}
	state, park := "queued", "delivery"
	if approval {
		state, park = "approval", ""
	}
	res, err := tx.Exec(`INSERT INTO request_log(user_id,tmdb_id,tvdb_id,instance_id,media_type,title,status,season_scope,quality_profile_id,park_reason) VALUES (?,?,?,?,'tv',?,'pending',?,?,?)`, r.userID, r.tmdbID, target.Match.TVDBID, r.instanceID, r.title, sqlNullStr(r.seasonScope), sqlNullInt(r.qualityProfileID), sqlNullStr(park))
	if err != nil {
		return 0, false, err
	}
	id, err = res.LastInsertId()
	if err != nil {
		return 0, false, err
	}
	var repair, actor any
	if repairOf > 0 {
		repair, actor = repairOf, adminID
	}
	if _, err = tx.Exec(`INSERT INTO request_tv_targets(request_id,snapshot,repair_of,repaired_by) VALUES (?,?,?,?)`, id, string(raw), repair, actor); err != nil {
		return 0, false, err
	}
	if _, err = tx.Exec(`INSERT INTO request_dispatch(request_id,format,state) VALUES (?,'',?)`, id, state); err != nil {
		return 0, false, err
	}
	if err = tx.Commit(); err != nil {
		return 0, false, err
	}
	return id, true, nil
}

func (s *Service) tvDeliveryResponse(userID, id int64) (*CreateResponse, error) {
	r, _, err := s.loadRequest(id)
	if err != nil {
		return nil, err
	}
	out, err := s.deliveryResponse(userID, []int64{id}, r.title, r.instanceID, nil)
	if err != nil {
		return nil, err
	}
	if target, _, seriesID, e := s.loadTVTarget(id); e == nil {
		out.Match = &target.Match
		out.Match.SeriesID = seriesID
	}
	return out, nil
}

// dispatchTV runs inside the existing job/instance lease and heartbeat. Every
// mutation rechecks current access, policy, revision and lease ownership.
func (s *Service) dispatchTV(ctx context.Context, id int64, token string, r *resolvedRequest) {
	s.tvMatchMu.Lock()
	defer s.tvMatchMu.Unlock()
	target, phase, _, err := s.loadTVTarget(id)
	if err != nil {
		s.finishDelivery(id, "", token, "attention", "tv_target_missing", nil)
		return
	}
	client, _, err := s.resolveSonarr(r.userID, r.instanceID)
	if err != nil || client == nil {
		s.finishDelivery(id, "", token, "attention", "access_unavailable", nil)
		return
	}
	guard := func() error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, _, err := s.resolveSonarr(r.userID, r.instanceID); err != nil {
			return err
		}
		if err := s.checkContentPolicy(r.userID, s.userIsAdmin(r.userID), "tv", r.tmdbID); err != nil {
			return err
		}
		var held int
		now := time.Now().Unix()
		if s.db.QueryRow(`SELECT COUNT(*) FROM request_dispatch d JOIN request_log r ON r.id=d.request_id JOIN request_dispatch_locks l ON l.instance_id=r.instance_id AND l.lease_token=d.lease_token WHERE d.request_id=? AND d.lease_token=? AND d.state='processing' AND r.status='pending' AND r.park_reason='delivery' AND d.lease_until>? AND l.lease_until>?`, id, token, now, now).Scan(&held) != nil || held != 1 {
			return errors.New("delivery lease no longer held")
		}
		current, err := s.resolveTVMatch(client, r.tmdbID)
		if err != nil {
			return err
		}
		if current.Revision != target.Match.Revision {
			return ErrTVMatchStale
		}
		return nil
	}
	fail := func(err error) {
		state, code := "retry", "library_unavailable"
		var matchErr *tvMatchError
		if errors.Is(err, ErrTVMatchStale) {
			state, code = "attention", "tv_match_changed"
		} else if errors.As(err, &matchErr) {
			code = matchErr.code
			if code != "tv_metadata_unavailable" {
				state = "attention"
			}
		} else if errors.Is(err, ErrArrInstanceForbidden) || errors.Is(err, ErrArrInstanceInvalid) || errors.Is(err, ErrTitleNotAvailable) {
			state, code = "attention", "access_unavailable"
		}
		s.finishDelivery(id, "", token, state, code, err)
	}
	if err = guard(); err != nil {
		fail(err)
		return
	}
	series, err := client.GetSeriesByTVDB(target.Match.TVDBID)
	if err != nil {
		fail(err)
		return
	}
	if series == nil {
		lookup, e := client.LookupByTVDB(target.Match.TVDBID)
		if e != nil {
			fail(e)
			return
		}
		all := make([]int, 0, len(target.Match.SeasonMap))
		for n := range target.Match.SeasonMap {
			all = append(all, n)
		}
		if e = validateTVSeasons(target.Match.SeasonMap, all, lookup.Seasons); e != nil {
			fail(e)
			return
		}
		profiles, e := client.GetQualityProfiles()
		if e != nil {
			fail(e)
			return
		}
		folders, e := client.GetRootFolders()
		if e != nil {
			fail(e)
			return
		}
		if len(profiles) == 0 || len(folders) == 0 {
			s.finishDelivery(id, "", token, "attention", "service_unavailable", nil)
			return
		}
		profile := r.qualityProfileID
		if profile == 0 || !sonarrProfileExists(profiles, profile) {
			profile = profiles[0].ID
		}
		add := &sonarr.AddSeriesRequest{Title: lookup.Title, TvdbID: target.Match.TVDBID, Year: lookup.Year, QualityProfileID: profile, RootFolderPath: folders[0].Path, SeasonFolder: true, MonitorNewItems: "none", Monitored: !target.Pilot}
		if target.Pilot {
			add.Seasons = seasonSelection(lookup.Seasons, nil)
			add.AddOptions.Monitor = "none"
		} else {
			add.Seasons = seasonSelection(lookup.Seasons, target.TargetSeasons)
			add.AddOptions.SearchForMissingEpisodes = true
		}
		if err = guard(); err != nil {
			fail(err)
			return
		}
		// Persist before POST: a lost response or restart recovers by exact ID.
		if _, err = s.db.Exec(`UPDATE request_tv_targets SET phase='adding' WHERE request_id=?`, id); err != nil {
			fail(err)
			return
		}
		if err = client.AddSeries(add); err != nil {
			fail(err)
			return
		}
		if !target.Pilot {
			// Sonarr owns applying the explicit flags and its post-refresh search.
			if _, err = s.db.Exec(`UPDATE request_tv_targets SET phase='complete' WHERE request_id=?`, id); err != nil {
				fail(err)
				return
			}
			s.finishDelivery(id, "", token, "complete", StatusRequested, nil)
			return
		}
		_, err = s.db.Exec(`UPDATE request_tv_targets SET phase='awaiting_metadata' WHERE request_id=?`, id)
		if err != nil {
			fail(err)
			return
		}
		s.finishDelivery(id, "", token, "retry", "tv_metadata_refresh", nil)
		return
	}
	if _, err = s.db.Exec(`UPDATE request_tv_targets SET series_id=? WHERE request_id=?`, series.ID, id); err != nil {
		fail(err)
		return
	}
	// Applies to every existing parent, including one another story just added.
	// A second request cannot race Sonarr's first post-add monitoring pass.
	if raw := strings.TrimSpace(string(series.AddOptions)); raw != "" && raw != "null" {
		s.finishDelivery(id, "", token, "retry", "tv_metadata_refresh", nil)
		return
	}
	known := map[int]bool{}
	for _, season := range series.Seasons {
		known[season.SeasonNumber] = true
	}
	for _, n := range target.TargetSeasons {
		if !known[n] {
			fail(tvMatchFailure("tv_seasons_unmapped"))
			return
		}
	}
	if target.Pilot {
		episodes, e := client.GetEpisodes(series.ID, target.TargetSeasons[0])
		if e != nil {
			fail(e)
			return
		}
		var pilot *sonarr.Episode
		for i := range episodes {
			if episodes[i].SeasonNumber == target.TargetSeasons[0] && episodes[i].EpisodeNumber == 1 {
				if pilot != nil {
					fail(tvMatchFailure("tv_match_ambiguous"))
					return
				}
				pilot = &episodes[i]
			}
		}
		if pilot == nil {
			s.finishDelivery(id, "", token, "retry", "tv_pilot_unavailable", nil)
			return
		}
		if !pilot.HasFile {
			if err = guard(); err != nil {
				fail(err)
				return
			}
			if !series.Monitored {
				if err = client.UpdateSeriesMonitoring(series.ID, true, nil); err != nil {
					fail(err)
					return
				}
			}
			if err = guard(); err != nil {
				fail(err)
				return
			}
			if !pilot.Monitored {
				if err = client.SetEpisodesMonitored([]int{pilot.ID}, true); err != nil {
					fail(err)
					return
				}
			}
			if err = guard(); err != nil {
				fail(err)
				return
			}
			if err = client.TriggerEpisodeSearch([]int{pilot.ID}); err != nil {
				fail(err)
				return
			}
		}
	} else {
		complete := true
		for _, n := range target.TargetSeasons {
			complete = complete && seasonHasAllFiles(series, n)
		}
		if complete {
			s.finishDelivery(id, "", token, "complete", StatusAvailable, nil)
			return
		}
		// Only chosen flags are sent; the client patches fresh full JSON. Every
		// unrelated season, episode, file and setting retains its current value.
		flags := map[int]bool{}
		for _, n := range target.TargetSeasons {
			flags[n] = true
		}
		if err = guard(); err != nil {
			fail(err)
			return
		}
		if err = client.UpdateSeriesMonitoring(series.ID, true, flags); err != nil {
			fail(err)
			return
		}
		for _, n := range target.TargetSeasons {
			episodes, e := client.GetEpisodes(series.ID, n)
			if e != nil {
				fail(e)
				return
			}
			ids := []int{}
			for _, ep := range episodes {
				if ep.SeasonNumber != n {
					fail(tvMatchFailure("tv_match_ambiguous"))
					return
				}
				if !ep.Monitored {
					ids = append(ids, ep.ID)
				}
			}
			if err = guard(); err != nil {
				fail(err)
				return
			}
			if err = client.SetEpisodesMonitored(ids, true); err != nil {
				fail(err)
				return
			}
			if err = guard(); err != nil {
				fail(err)
				return
			}
			if err = client.TriggerSeasonSearch(series.ID, n); err != nil {
				fail(err)
				return
			}
		}
	}
	_ = phase // the phase is audit evidence; live Sonarr state owns convergence.
	if _, err = s.db.Exec(`UPDATE request_tv_targets SET phase='complete' WHERE request_id=?`, id); err != nil {
		fail(err)
		return
	}
	s.finishDelivery(id, "", token, "complete", StatusRequested, nil)
}
