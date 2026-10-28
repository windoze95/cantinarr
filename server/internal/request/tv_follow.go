package request

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

type TVFollowStatus struct {
	RequestID     int64  `json:"request_id"`
	State         string `json:"state"`
	SourceSeasons []int  `json:"source_seasons"`
	Message       string `json:"message"`
	CanStop       bool   `json:"can_stop"`
}

func (s *Service) prepareMissingTV(r *resolvedRequest, target *TVRequestTarget, existing *sonarr.Series) error {
	result, err := s.mediaLookup(context.Background(), r.userID, r.tmdbID, "tv", true)
	if err != nil {
		return err
	}
	if !result.Enabled {
		return nil
	}
	all, err := s.canonicalFollowEpisodes(r.tmdbID, target.SourceSeasons)
	if err != nil {
		return err
	}
	all = s.bindProviderEpisodeIDs(r.tmdbID, all, result.Episodes)
	covered, _, known := mediaEpisodeCoverage(r.tmdbID, all, result)
	if !known {
		return ErrMediaAvailabilityUnknown
	}
	var native []sonarr.Episode
	if existing != nil {
		client, _, e := s.resolveSonarr(r.userID, r.instanceID)
		if e != nil {
			return e
		}
		native, err = client.GetAllEpisodes(existing.ID)
		if err != nil {
			return err
		}
	}
	target.RequestMode = RequestModeMissing
	target.MediaCoverageRequired, target.MediaScope = true, result.ScopeRevision
	r.mediaCoverageRequired, r.mediaScope = true, result.ScopeRevision
	target.freeSeasons = map[int]bool{}
	for _, n := range target.SourceSeasons {
		target.freeSeasons[n] = true
	}
	for _, ep := range all {
		if !slices.Contains(target.SourceSeasons, ep.SeasonNumber) || (target.Pilot && ep.EpisodeNumber != 1) {
			continue
		}
		target.Episodes = append(target.Episodes, ep)
		if existing != nil {
			if item, e := resolveCanonicalEpisode(ep, existing.ID, native); e == nil && item != nil && item.HasFile {
				covered[ep.key()] = true
			}
		}
		if covered[ep.key()] {
			target.ExcludedEpisodes = append(target.ExcludedEpisodes, ep.key())
			continue
		}
		if ep.TVDBID <= 0 && ep.aired(time.Now()) {
			return tvMatchFailure("tv_metadata_unavailable")
		}
		target.freeSeasons[ep.SeasonNumber] = false
	}
	if len(target.Episodes) == 0 {
		return tvMatchFailure("tv_metadata_unavailable")
	}
	// A deliberate season follow includes future episodes, even when every
	// currently announced episode is covered. It retains approval and the
	// existing one-season quota charge; later episodes cost no additional unit.
	for _, n := range target.SourceSeasons {
		target.freeSeasons[n] = false
	}
	target.noOpStatus = StatusAvailable
	target.EpisodeIntent = tvEpisodeIntent(target)
	return nil
}

func saveTVFollow(tx *sql.Tx, id int64, target *TVRequestTarget) error {
	now := time.Now().Unix()
	if _, err := tx.Exec(`INSERT INTO request_tv_follows(request_id,created_at,updated_at) VALUES (?,?,?)`, id, now, now); err != nil {
		return err
	}
	for _, ep := range target.Episodes {
		state := "queued"
		if slices.Contains(target.ExcludedEpisodes, ep.key()) {
			state = "excluded"
		}
		raw, _ := json.Marshal(ep)
		if _, err := tx.Exec(`INSERT INTO request_tv_follow_episodes(request_id,episode_key,identity,state) VALUES (?,?,?,?)`, id, ep.key(), string(raw), state); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) tvFollowing(userID int64, tmdbID int, instanceID string) ([]TVFollowStatus, error) {
	rows, err := s.db.Query(`SELECT f.request_id,f.state,f.message,t.snapshot,r.status,COALESCE(r.park_reason,'') FROM request_tv_follows f JOIN request_log r ON r.id=f.request_id JOIN request_tv_targets t ON t.request_id=r.id WHERE r.user_id=? AND r.tmdb_id=? AND (?='' OR r.instance_id=?) ORDER BY f.request_id DESC`, userID, tmdbID, instanceID, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TVFollowStatus
	for rows.Next() {
		var f TVFollowStatus
		var raw, status, park string
		if err = rows.Scan(&f.RequestID, &f.State, &f.Message, &raw, &status, &park); err != nil {
			return nil, err
		}
		var target TVRequestTarget
		if json.Unmarshal([]byte(raw), &target) != nil {
			continue
		}
		f.SourceSeasons = target.SourceSeasons
		// Approval history gates scheduling even though the durable follow
		// scope already exists. Do not advertise an unapproved/denied follow
		// as active; the saved decision and worker state remain unchanged.
		if status == StatusDenied {
			f.State = "stopped"
			f.Message = "This request was denied or cancelled. Following will not run."
		} else if status == StatusPending && park == "" && f.State != "stopped" {
			f.State = "paused"
			f.Message = "Following starts after this request is approved."
		}
		f.CanStop = f.State != "stopped"
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Service) StopTVFollow(userID, id int64) (*TVFollowStatus, error) {
	r, _, err := s.loadRequest(id)
	if err != nil {
		return nil, err
	}
	if r.userID != userID && !s.userIsAdmin(userID) {
		return nil, ErrArrInstanceForbidden
	}
	res, err := s.db.Exec(`UPDATE request_tv_follows SET state='stopped',message='Following stopped. Existing Sonarr work is unchanged.',lease_token='',lease_until=0,updated_at=? WHERE request_id=?`, time.Now().Unix(), id)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, errors.New("TV follow not found")
	}
	target, _, _, err := s.loadTVTarget(id)
	if err != nil {
		return nil, err
	}
	s.notifyDelivery(id, "follow_stopped")
	return &TVFollowStatus{RequestID: id, State: "stopped", SourceSeasons: target.SourceSeasons, Message: "Following stopped. Existing Sonarr work is unchanged."}, nil
}

func (h *Handler) StopTVFollow(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "requestID"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request id"})
		return
	}
	out, err := h.service.StopTVFollow(claims.UserID, id)
	if err != nil {
		writeJSON(w, requestErrorStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Service) pauseTVFollow(id int64, message string) {
	s.pauseTVFollowCode(id, "attention", message)
}
func (s *Service) pauseTVFollowCode(id int64, code, message string) {
	_, _ = s.db.Exec(`UPDATE request_tv_follows SET state='paused',pause_code=?,message=?,updated_at=? WHERE request_id=? AND state='active'`, code, message, time.Now().Unix(), id)
}

func (s *Service) followGuard(ctx context.Context, id int64, r *resolvedRequest, target *TVRequestTarget, client *sonarr.Client, initial ...bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var state string
	if err := s.db.QueryRow(`SELECT state FROM request_tv_follows WHERE request_id=?`, id).Scan(&state); err != nil || (state != "active" && !(state == "stopped" && len(initial) > 0 && initial[0])) {
		return errors.New("TV following is not active")
	}
	if target.MediaScope != "" {
		if scope, ok := s.mediaAvailability.(mediaScopeReader); ok {
			current, err := scope.ScopeRevision(r.userID)
			if err != nil {
				return ErrMediaAvailabilityUnknown
			}
			if current != target.MediaScope {
				s.pauseTVFollow(id, "Media-server access or scan configuration changed. Following is paused.")
				return ErrMediaAvailabilityUnknown
			}
		}
	}
	if _, _, err := s.resolveSonarr(r.userID, r.instanceID); err != nil {
		s.pauseTVFollow(id, "Library access changed. Review this request before following again.")
		return err
	}
	if err := s.checkContentPolicy(r.userID, s.userIsAdmin(r.userID), "tv", r.tmdbID); err != nil {
		s.pauseTVFollow(id, "Title access could not be verified. Following is paused.")
		return err
	}
	current, err := s.resolveTVMatch(client, r.tmdbID)
	if err != nil {
		return err
	}
	if current.Revision != target.Match.Revision {
		s.pauseTVFollow(id, "The TV match changed. Review the match before following again.")
		return ErrTVMatchStale
	}
	return nil
}

// Initial delivery uses the existing request/instance lease, approval gate and
// quota receipt. Subsequent discovery owns a separate lease; it never changes
// a completed request back into a pending request.
func (s *Service) dispatchMissingTV(ctx context.Context, id int64, token string, r *resolvedRequest, target *TVRequestTarget, client *sonarr.Client, guard func() error, fail func(error)) {
	// A lost command response can be reconciled by positive queue/file/command
	// evidence. Absence never authorizes replay of that POST.
	s.reconcileTVSearches(ctx, id, r, target, client)
	baseGuard := guard
	guard = func() error {
		if err := baseGuard(); err != nil {
			return err
		}
		return s.followGuard(ctx, id, r, target, client, true)
	}
	client = client.WithMutationGuard(guard)
	if err := guard(); err != nil {
		fail(err)
		return
	}
	if _, err := s.freshTVCoverage(ctx, id, r); err != nil {
		fail(err)
		return
	}
	series, err := client.GetSeriesByTVDB(target.Match.TVDBID)
	if err != nil {
		fail(err)
		return
	}
	var savedID, owned int
	if err = s.db.QueryRow(`SELECT series_id,series_owned FROM request_tv_follows WHERE request_id=?`, id).Scan(&savedID, &owned); err != nil {
		fail(err)
		return
	}
	if series == nil {
		if savedID > 0 {
			s.pauseTVFollow(id, "The Sonarr series was removed. Following is paused.")
			s.finishDelivery(id, "", token, "attention", "tv_series_removed", nil)
			return
		}
		_, phase, _, e := s.loadTVTarget(id)
		if e != nil {
			fail(e)
			return
		}
		if phase == "adding" {
			s.pauseTVFollow(id, "The series add could not be confirmed. Check Sonarr before retrying.")
			s.finishDelivery(id, "", token, "attention", "tv_add_uncertain", nil)
			return
		}
		lookup, e := client.LookupByTVDB(target.Match.TVDBID)
		if e != nil {
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
			fail(errors.New("Sonarr has no request profile or root folder"))
			return
		}
		profile := r.qualityProfileID
		if profile == 0 || !sonarrProfileExists(profiles, profile) {
			profile = profiles[0].ID
		}
		add := &sonarr.AddSeriesRequest{Title: lookup.Title, TvdbID: target.Match.TVDBID, Year: lookup.Year, QualityProfileID: profile, RootFolderPath: folders[0].Path, SeasonFolder: true, MonitorNewItems: "none", Monitored: false, Seasons: seasonSelection(lookup.Seasons, nil)}
		add.AddOptions.Monitor = "none"
		if err = guard(); err != nil {
			fail(err)
			return
		}
		if _, err = s.db.Exec(`UPDATE request_tv_targets SET phase='adding' WHERE request_id=?`, id); err != nil {
			fail(err)
			return
		}
		if _, err = s.db.Exec(`UPDATE request_tv_follows SET series_owned=1 WHERE request_id=?`, id); err != nil {
			fail(err)
			return
		}
		if err = client.AddSeries(add); err != nil {
			fail(err)
			return
		}
		_, _ = s.db.Exec(`UPDATE request_tv_targets SET phase='awaiting_metadata' WHERE request_id=?`, id)
		s.finishDelivery(id, "", token, "retry", "tv_metadata_refresh", nil)
		return
	}
	if savedID > 0 && savedID != series.ID {
		s.pauseTVFollow(id, "The Sonarr series identity changed. Following is paused.")
		fail(ErrTVMatchStale)
		return
	}
	if raw := strings.TrimSpace(string(series.AddOptions)); raw != "" && raw != "null" {
		s.finishDelivery(id, "", token, "retry", "tv_metadata_refresh", nil)
		return
	}
	if _, err = s.db.Exec(`UPDATE request_tv_follows SET series_id=? WHERE request_id=?`, series.ID, id); err != nil {
		fail(err)
		return
	}
	_, _ = s.db.Exec(`UPDATE request_tv_targets SET series_id=? WHERE request_id=?`, series.ID, id)
	if !series.Monitored {
		if owned != 1 {
			s.pauseTVFollow(id, "The Sonarr series is unmonitored. Following will not change existing monitoring.")
			s.finishDelivery(id, "", token, "attention", "tv_series_unmonitored", nil)
			return
		}
		if err = guard(); err != nil {
			fail(err)
			return
		}
		if err = client.UpdateSeriesMonitoring(series.ID, true, nil); err != nil {
			fail(err)
			return
		}
		series.Monitored = true
	}
	// Mark activation durably; a later manual unmonitor must never be undone.
	_, _ = s.db.Exec(`UPDATE request_tv_follows SET series_owned=2 WHERE request_id=? AND series_owned=1`, id)
	if err = s.deliverFollowEpisodes(ctx, id, r, target, client, series, guard); err != nil {
		fail(err)
		return
	}
	// Starting an explicit future follow consumes its season reservation even
	// when every current episode was excluded and no Sonarr write was needed.
	if err = guard(); err != nil {
		fail(err)
		return
	}
	if err = s.startDelivery(ctx, id, "", token); err != nil {
		fail(err)
		return
	}
	_, _ = s.db.Exec(`UPDATE request_tv_targets SET phase='complete' WHERE request_id=?`, id)
	s.finishDelivery(id, "", token, "complete", StatusRequested, nil)
}

type followEpisode struct {
	CanonicalEpisode
	state    string
	nativeID int
	owned    bool
}

func (s *Service) deliverFollowEpisodes(ctx context.Context, id int64, r *resolvedRequest, target *TVRequestTarget, client *sonarr.Client, series *sonarr.Series, guard func() error) error {
	result, err := s.mediaLookup(ctx, r.userID, r.tmdbID, "tv", true)
	if err != nil {
		return err
	}
	if !result.Enabled || (target.MediaScope != "" && result.ScopeRevision != target.MediaScope) {
		s.pauseTVFollow(id, "Media-server access or scan configuration changed. Following is paused.")
		return ErrMediaAvailabilityUnknown
	}
	all, err := s.canonicalFollowEpisodes(r.tmdbID, target.SourceSeasons)
	if err != nil {
		return err
	}
	all = s.bindProviderEpisodeIDs(r.tmdbID, all, result.Episodes)
	covered, _, known := mediaEpisodeCoverage(r.tmdbID, all, result)
	if !known {
		return ErrMediaAvailabilityUnknown
	}
	canonicalByID := map[string]CanonicalEpisode{}
	for _, ep := range all {
		canonicalByID[ep.key()] = ep
	}
	rows, err := s.db.Query(`SELECT identity,state,episode_id,monitor_owned FROM request_tv_follow_episodes WHERE request_id=? ORDER BY episode_key`, id)
	if err != nil {
		return err
	}
	var work []followEpisode
	for rows.Next() {
		var ep followEpisode
		var raw string
		if err = rows.Scan(&raw, &ep.state, &ep.nativeID, &ep.owned); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(raw), &ep.CanonicalEpisode); err != nil {
			break
		}
		work = append(work, ep)
	}
	readErr := rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if readErr != nil {
		return readErr
	}
	native, err := client.GetAllEpisodes(series.ID)
	if err != nil {
		return err
	}
	for _, ep := range work {
		if ep.state == "excluded" {
			continue
		}
		current, ok := canonicalByID[ep.key()]
		if !ok || (ep.TVDBID > 0 && current.TVDBID > 0 && current.TVDBID != ep.TVDBID) || current.SeasonNumber != ep.SeasonNumber || current.EpisodeNumber != ep.EpisodeNumber {
			s.pauseTVFollow(id, "Episode identity changed. Review the TV match.")
			return ErrTVMatchStale
		}
		// Announced catalog episodes may not yet have a TVDB binding or even
		// exist in Sonarr. Preserve their stable TMDB scope and wait; they must
		// never enable RSS or pause an otherwise healthy season follow.
		if !current.aired(time.Now()) && ep.nativeID == 0 && !ep.owned {
			continue
		}
		if current.TVDBID <= 0 {
			return tvMatchFailure("tv_metadata_unavailable")
		}
		if ep.TVDBID == 0 {
			if err = guard(); err != nil {
				return err
			}
			raw, _ := json.Marshal(current)
			if _, err = s.db.Exec(`UPDATE request_tv_follow_episodes SET identity=? WHERE request_id=? AND episode_key=? AND COALESCE(json_extract(identity,'$.tvdb_id'),0)=0`, string(raw), id, ep.key()); err != nil {
				return err
			}
			ep.CanonicalEpisode = current
		}
		item, e := resolveCanonicalEpisode(current, series.ID, native)
		if e != nil {
			s.pauseTVFollow(id, "Episode identity could not be mapped safely.")
			return e
		}
		if item == nil {
			return tvMatchFailure("tv_metadata_unavailable")
		}
		if ep.nativeID > 0 && ep.nativeID != item.ID {
			s.pauseTVFollow(id, "Sonarr episode identity changed. Following is paused.")
			return ErrTVMatchStale
		}
		if ep.owned && !item.Monitored {
			s.pauseTVFollow(id, "An episode was unmonitored in Sonarr. Following is paused.")
			return errors.New("episode monitoring changed")
		}
		if ep.state == "available" || ep.state == "submitted" {
			continue
		}
		// Recheck recipient scope and positive coverage for each episode. Long
		// deliveries must not reuse the first episode's access snapshot.
		covered, err = s.freshTVCoverage(ctx, id, r)
		if err != nil {
			return err
		}
		coverageCheckedAt := time.Now()
		if item.HasFile || covered[ep.key()] {
			_, err = s.db.Exec(`UPDATE request_tv_follow_episodes SET state='available',episode_id=? WHERE request_id=? AND episode_key=?`, item.ID, id, ep.key())
			if err != nil {
				return err
			}
			continue
		}
		if err = guard(); err != nil {
			return err
		}
		// Leave unaired episodes unmonitored: Sonarr RSS must not bypass the
		// future recipient/coverage check performed by this follower.
		if !current.aired(time.Now()) {
			continue
		}
		if !item.Monitored {
			if err = client.SetEpisodesMonitored([]int{item.ID}, true); err != nil {
				return err
			}
			if _, err = s.db.Exec(`UPDATE request_tv_follow_episodes SET state='monitored',episode_id=?,monitor_owned=1 WHERE request_id=? AND episode_key=?`, item.ID, id, ep.key()); err != nil {
				return err
			}
		}
		// A queue item proves Sonarr already owns delivery. An unreadable queue
		// does not justify another search.
		queue, e := client.GetQueue()
		if e != nil {
			return e
		}
		queued := false
		for _, q := range queue {
			if q.EpisodeID == item.ID {
				queued = true
			}
		}
		if queued {
			_, err = s.db.Exec(`UPDATE request_tv_follow_episodes SET state='submitted',episode_id=? WHERE request_id=? AND episode_key=?`, item.ID, id, ep.key())
			if err != nil {
				return err
			}
			continue
		}
		if ep.state != "searching" {
			reused, e := s.reuseTVEpisodeWork(ctx, id, r, target, client, series.ID, ep, item.ID)
			if e != nil {
				return e
			}
			if reused {
				continue
			}
		}
		if ep.state == "searching" {
			s.reconcileTVSearches(ctx, id, r, target, client)
			var reconciled string
			if s.db.QueryRow(`SELECT state FROM request_tv_follow_episodes WHERE request_id=? AND episode_key=?`, id, ep.key()).Scan(&reconciled) == nil && reconciled != "searching" {
				continue
			}
			s.pauseTVFollowCode(id, "search_uncertain", "A previous episode search has an unknown outcome. Check Sonarr before requesting again.")
			return errors.New("episode search outcome is uncertain")
		}
		if err = guard(); err != nil {
			return err
		}
		// Reuse only this dispatch step's fresh verification, never the
		// display cache. Slow monitor/queue reads require another check.
		if time.Since(coverageCheckedAt) >= 5*time.Second {
			covered, err = s.freshTVCoverage(ctx, id, r)
			if err != nil {
				return err
			}
		}
		if covered[ep.key()] {
			_, err = s.db.Exec(`UPDATE request_tv_follow_episodes SET state='available' WHERE request_id=? AND episode_key=?`, id, ep.key())
			if err != nil {
				return err
			}
			continue
		}
		// Write intent before the non-idempotent search. Any lost response is
		// retained as uncertain and never automatically replayed after restart.
		if _, err = s.db.Exec(`UPDATE request_tv_follow_episodes SET state='searching',episode_id=?,searched_at=? WHERE request_id=? AND episode_key=?`, item.ID, time.Now().UnixNano(), id, ep.key()); err != nil {
			return err
		}
		command, e := client.SubmitEpisodeSearch([]int{item.ID})
		if e != nil {
			s.pauseTVFollowCode(id, "search_uncertain", "An episode search could not be confirmed. Check Sonarr before requesting again.")
			return e
		}
		if _, err = s.db.Exec(`UPDATE request_tv_follow_episodes SET state='submitted',command_id=? WHERE request_id=? AND episode_key=?`, command, id, ep.key()); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) StartTVFollowMaintenance(ctx context.Context) {
	go func() {
		s.SweepTVFollows(ctx)
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.SweepTVFollows(ctx)
			}
		}
	}()
}

func (s *Service) SweepTVFollows(ctx context.Context) {
	if s.mediaAvailability == nil {
		return
	}
	s.tvFollowMu.Lock()
	defer s.tvFollowMu.Unlock()
	rows, err := s.db.Query(`SELECT f.request_id FROM request_tv_follows f JOIN request_log r ON r.id=f.request_id WHERE (f.state='active' OR (f.state='paused' AND f.pause_code='search_uncertain')) AND r.status NOT IN ('pending','denied') AND f.next_attempt_at<=? AND f.lease_until<=? ORDER BY f.request_id LIMIT 32`, time.Now().Unix(), time.Now().Unix())
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) != nil {
			rows.Close()
			return
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		s.sweepTVFollow(ctx, id)
	}
}

func (s *Service) sweepTVFollow(ctx context.Context, id int64) {
	// Completed initial requests have no pending dispatch to reconcile later
	// uncertain episode work. Maintenance performs the same positive-only read.
	var state, code string
	if s.db.QueryRow(`SELECT state,pause_code FROM request_tv_follows WHERE request_id=?`, id).Scan(&state, &code) == nil && state == "paused" && code == "search_uncertain" {
		r, _, err := s.loadRequest(id)
		if err != nil {
			return
		}
		target, _, _, err := s.loadTVTarget(id)
		if err != nil {
			return
		}
		client, _, err := s.resolveSonarr(r.userID, r.instanceID)
		if err != nil || client == nil {
			return
		}
		if s.checkContentPolicy(r.userID, s.userIsAdmin(r.userID), "tv", r.tmdbID) != nil {
			return
		}
		s.reconcileTVSearches(ctx, id, r, target, client)
	}
	token := leaseToken()
	now := time.Now().Unix()
	res, err := s.db.Exec(`UPDATE request_tv_follows SET lease_token=?,lease_until=?,next_attempt_at=?,updated_at=? WHERE request_id=? AND state='active' AND lease_until<=?`, token, now+int64(dispatchLease/time.Second), now+300, now, id, now)
	if err != nil {
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return
	}
	defer func() {
		_, _ = s.db.Exec(`UPDATE request_tv_follows SET lease_token='',lease_until=0 WHERE request_id=? AND lease_token=?`, id, token)
	}()
	r, _, err := s.loadRequest(id)
	if err != nil {
		return
	}
	target, _, _, err := s.loadTVTarget(id)
	if err != nil || target.RequestMode != RequestModeMissing {
		return
	}
	client, _, err := s.resolveSonarr(r.userID, r.instanceID)
	if err != nil || client == nil {
		s.pauseTVFollow(id, "Library access changed. Following is paused.")
		return
	}
	instanceHeld := false
	guard := func() error {
		var held int
		if s.db.QueryRow(`SELECT COUNT(*) FROM request_tv_follows WHERE request_id=? AND lease_token=? AND lease_until>? AND state='active'`, id, token, time.Now().Unix()).Scan(&held) != nil || held != 1 {
			return errors.New("following lease no longer held")
		}
		if instanceHeld && (s.db.QueryRow(`SELECT COUNT(*) FROM request_dispatch_locks WHERE instance_id=? AND lease_token=? AND lease_until>?`, r.instanceID, token, time.Now().Unix()).Scan(&held) != nil || held != 1) {
			return errors.New("following instance lease no longer held")
		}
		return s.followGuard(ctx, id, r, target, client)
	}
	if err = guard(); err != nil {
		return
	}
	// Share the same instance lease as normal requests, so separate followers
	// cannot interleave a series create or metadata mutation.
	res, err = s.db.Exec(`INSERT INTO request_dispatch_locks(instance_id,lease_token,lease_until) VALUES (?,?,?) ON CONFLICT(instance_id) DO UPDATE SET lease_token=excluded.lease_token,lease_until=excluded.lease_until WHERE request_dispatch_locks.lease_until<=?`, r.instanceID, token, now+int64(dispatchLease/time.Second), now)
	if err != nil {
		return
	}
	n, _ = res.RowsAffected()
	if n != 1 {
		return
	}
	instanceHeld = true
	defer func() {
		_, _ = s.db.Exec(`DELETE FROM request_dispatch_locks WHERE instance_id=? AND lease_token=?`, r.instanceID, token)
	}()
	client = client.WithMutationGuard(guard)
	series, err := client.GetSeriesByTVDB(target.Match.TVDBID)
	if err != nil {
		return
	}
	var expected int
	_ = s.db.QueryRow(`SELECT series_id FROM request_tv_follows WHERE request_id=?`, id).Scan(&expected)
	if series == nil || series.ID != expected || !series.Monitored {
		s.pauseTVFollow(id, "The Sonarr series was removed, replaced, or unmonitored. Following is paused.")
		return
	}
	canonical, err := s.canonicalFollowEpisodes(r.tmdbID, target.SourceSeasons)
	if err != nil {
		return
	}
	result, err := s.mediaLookup(ctx, r.userID, r.tmdbID, "tv", true)
	if err != nil {
		return
	}
	if !result.Enabled || (target.MediaScope != "" && result.ScopeRevision != target.MediaScope) {
		s.pauseTVFollow(id, "Media-server access or scan configuration changed. Following is paused.")
		return
	}
	canonical = s.bindProviderEpisodeIDs(r.tmdbID, canonical, result.Episodes)
	covered, _, known := mediaEpisodeCoverage(r.tmdbID, canonical, result)
	if !known {
		return
	}
	if err = guard(); err != nil {
		return
	}
	for _, ep := range canonical {
		if !slices.Contains(target.SourceSeasons, ep.SeasonNumber) || (target.Pilot && ep.EpisodeNumber != 1) {
			continue
		}
		state := "queued"
		if covered[ep.key()] {
			state = "excluded"
		}
		raw, _ := json.Marshal(ep)
		if _, err = s.db.Exec(`INSERT INTO request_tv_follow_episodes(request_id,episode_key,identity,state) VALUES (?,?,?,?) ON CONFLICT(request_id,episode_key) DO NOTHING`, id, ep.key(), string(raw), state); err != nil {
			return
		}
	}
	if err = s.deliverFollowEpisodes(ctx, id, r, target, client, series, guard); err != nil {
		_, _ = s.db.Exec(`UPDATE request_tv_follows SET message='Could not verify or deliver the selected episodes. Following will retry.',updated_at=? WHERE request_id=? AND state='active'`, time.Now().Unix(), id)
		return
	}
	_, _ = s.db.Exec(`UPDATE request_tv_follows SET message='',updated_at=? WHERE request_id=? AND state='active'`, time.Now().Unix(), id)
}

// No provider configuration/access change can turn an accepted missing-only
// request into an unqualified Sonarr request.
func (s *Service) freshTVCoverage(ctx context.Context, id int64, r *resolvedRequest) (map[string]bool, error) {
	result, err := s.mediaLookup(ctx, r.userID, r.tmdbID, "tv", true)
	if err != nil {
		return nil, err
	}
	if !result.Enabled || (r.mediaScope != "" && result.ScopeRevision != r.mediaScope) {
		s.pauseTVFollow(id, "Media-server access or scan configuration changed. Following is paused.")
		return nil, ErrMediaAvailabilityUnknown
	}
	canonical, err := s.canonicalEpisodes(r.tmdbID, nil, false)
	if err != nil {
		return nil, err
	}
	canonical = s.bindProviderEpisodeIDs(r.tmdbID, canonical, result.Episodes)
	covered, _, known := mediaEpisodeCoverage(r.tmdbID, canonical, result)
	if !known {
		return nil, ErrMediaAvailabilityUnknown
	}
	return covered, nil
}

// Recovery is read-only against Sonarr and may only advance an uncertain
// delivery when the exact episode has positive execution evidence. It never
// sends another search, including after process restart.
func (s *Service) reconcileTVSearches(ctx context.Context, id int64, r *resolvedRequest, target *TVRequestTarget, client *sonarr.Client) {
	var state, code string
	if s.db.QueryRow(`SELECT state,pause_code FROM request_tv_follows WHERE request_id=?`, id).Scan(&state, &code) != nil || (state != "active" && !(state == "paused" && code == "search_uncertain")) {
		return
	}
	rows, err := s.db.Query(`SELECT episode_key,identity,episode_id,searched_at FROM request_tv_follow_episodes WHERE request_id=? AND state='searching'`, id)
	if err != nil {
		return
	}
	type uncertain struct {
		key      string
		identity CanonicalEpisode
		native   int
		since    int64
	}
	var pending []uncertain
	for rows.Next() {
		var item uncertain
		var raw string
		if rows.Scan(&item.key, &raw, &item.native, &item.since) != nil {
			rows.Close()
			return
		}
		if json.Unmarshal([]byte(raw), &item.identity) != nil {
			rows.Close()
			return
		}
		pending = append(pending, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(pending) == 0 {
		return
	}
	if ctx.Err() != nil {
		return
	}
	series, err := client.GetSeriesByTVDB(target.Match.TVDBID)
	if err != nil || series == nil {
		return
	}
	var expected int
	if s.db.QueryRow(`SELECT series_id FROM request_tv_follows WHERE request_id=?`, id).Scan(&expected) != nil || expected != series.ID {
		return
	}
	episodes, err := client.GetAllEpisodes(series.ID)
	if err != nil {
		return
	}
	queue, err := client.GetQueue()
	if err != nil {
		return
	}
	for _, ep := range pending {
		native, err := resolveCanonicalEpisode(ep.identity, series.ID, episodes)
		if err != nil || native == nil || native.ID != ep.native {
			continue
		}
		stage := ""
		command := 0
		if native.HasFile {
			stage = "available"
		}
		for _, q := range queue {
			if q.EpisodeID == native.ID {
				stage = "submitted"
			}
		}
		if stage == "" && ep.since > 0 {
			command, err = client.FindEpisodeSearch(native.ID, time.Unix(0, ep.since))
			if err == nil && command > 0 {
				stage = "submitted"
			}
		}
		if stage != "" {
			_, _ = s.db.Exec(`UPDATE request_tv_follow_episodes SET state=?,command_id=? WHERE request_id=? AND episode_key=? AND state='searching'`, stage, command, id, ep.key)
		}
	}
	var left int
	if s.db.QueryRow(`SELECT COUNT(*) FROM request_tv_follow_episodes WHERE request_id=? AND state='searching'`, id).Scan(&left) == nil && left == 0 {
		_, _ = s.db.Exec(`UPDATE request_tv_follows SET state='active',pause_code='',message='' WHERE request_id=? AND state='paused' AND pause_code='search_uncertain'`, id)
	}
}

// The instance lease serializes this check with other initial/follower work.
// Recipient-specific coverage is never shared; only exact, recent Sonarr
// execution receipts can prevent a second in-flight search.
func (s *Service) reuseTVEpisodeWork(ctx context.Context, id int64, r *resolvedRequest, target *TVRequestTarget, client *sonarr.Client, seriesID int, ep followEpisode, nativeID int) (bool, error) {
	var state string
	var command int
	var searched int64
	err := s.db.QueryRow(`SELECT e.state,e.command_id,e.searched_at FROM request_tv_follow_episodes e JOIN request_tv_follows f ON f.request_id=e.request_id JOIN request_log r ON r.id=e.request_id WHERE e.request_id!=? AND r.instance_id=? AND f.series_id=? AND e.episode_id=? AND json_extract(e.identity,'$.tvdb_id')=? AND (e.state='searching' OR (e.state='submitted' AND e.command_id>0 AND e.searched_at>=?)) ORDER BY CASE e.state WHEN 'submitted' THEN 0 ELSE 1 END,e.searched_at DESC LIMIT 1`, id, r.instanceID, seriesID, nativeID, ep.TVDBID, time.Now().Add(-5*time.Minute).UnixNano()).Scan(&state, &command, &searched)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if state == "submitted" {
		pending, err := client.EpisodeSearchPending(command, nativeID)
		if err != nil {
			return false, err
		}
		if !pending {
			return false, nil
		}
		_, err = s.db.Exec(`UPDATE request_tv_follow_episodes SET state='submitted',episode_id=?,command_id=?,searched_at=? WHERE request_id=? AND episode_key=?`, nativeID, command, searched, id, ep.key())
		return err == nil, err
	}
	// Copy uncertainty, never a user's authorization. The current recipient
	// independently reconciles the exact work under current policy and grants.
	_, err = s.db.Exec(`UPDATE request_tv_follow_episodes SET state='searching',episode_id=?,searched_at=? WHERE request_id=? AND episode_key=?`, nativeID, searched, id, ep.key())
	if err != nil {
		return false, err
	}
	s.reconcileTVSearches(ctx, id, r, target, client)
	if err = s.db.QueryRow(`SELECT state FROM request_tv_follow_episodes WHERE request_id=? AND episode_key=?`, id, ep.key()).Scan(&state); err != nil {
		return false, err
	}
	if state != "searching" {
		return true, nil
	}
	s.pauseTVFollowCode(id, "search_uncertain", "An existing search for this episode has an unknown outcome. Check Sonarr before requesting again.")
	return false, errors.New("shared episode search outcome is uncertain")
}
