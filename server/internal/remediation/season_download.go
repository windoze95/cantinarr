package remediation

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// A download key is distinct from both a queue row (ephemeral) and an episode
// (several clients can be downloading it concurrently). Never group on a title.
func sonarrDownloadKey(instance string, item arr.QueueObservation) string {
	m := item.Media
	id := strings.ToLower(strings.TrimSpace(item.DownloadID))
	if id == "" || m.SeriesID <= 0 || m.EpisodeID <= 0 || m.EpisodeNumber <= 0 || m.SeasonNumber < 0 {
		return ""
	}
	added := ""
	if item.AddedAt != nil {
		added = item.AddedAt.UTC().Format(time.RFC3339Nano)
	}
	return fmt.Sprintf("sonarr-download:%x", sha256.Sum256([]byte(fmt.Sprintf("%q/%d/%d/%q/%q/%s",
		instance, m.SeriesID, m.SeasonNumber, m.DownloadClient, id, added))))
}

// routeSonarrDownloads preserves the identity of a new singleton when siblings
// arrive later, and of an unambiguous single-episode replacement. Existing
// episode histories from before grouping continue through their original keys.
func (s *Service) routeSonarrDownloads(instance string, items []arr.QueueObservation, records []*observationRecord) (map[string]observationGroup, error) {
	groups := make(map[string]observationGroup)
	downloads := make(map[string][]arr.QueueObservation)
	episodeAttempts := make(map[int]map[string]bool)
	byID := make(map[int64]*observationRecord)
	occupied := make(map[string]int64)
	legacyDownloads := make(map[string]bool)
	for _, r := range records {
		if r.issue.Source != SourceAuto {
			continue
		}
		byID[r.issue.ID] = r
		occupied[r.scopeKey] = r.issue.ID
		if r.issue.memberCount == 0 {
			legacyDownloads[strings.ToLower(r.issue.DownloadID)] = true
		}
	}
	// Only the current attempt can route by download. A retired singleton
	// attempt might reappear alongside its replacement; it now needs a
	// separate incident rather than overwriting the replacement's queue rows.
	current := make(map[string]*observationRecord)
	singletons := make(map[int][]*observationRecord)
	rows, err := s.db.Query(`SELECT m.issue_id,m.download_key,m.episode_id FROM issue_sonarr_members m
		JOIN issues i ON m.issue_id=i.id WHERE i.instance_id=? AND i.source='auto' AND i.closed_at IS NULL
		AND m.last_seen=(SELECT MAX(last_seen) FROM issue_sonarr_members WHERE issue_id=m.issue_id)`, instance)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var key string
		var episode int
		if err := rows.Scan(&id, &key, &episode); err != nil {
			rows.Close()
			return nil, err
		}
		if r := byID[id]; r != nil {
			current[key] = r
			if r.issue.memberCount == 1 {
				singletons[episode] = append(singletons[episode], r)
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	appendRow := func(key string, item arr.QueueObservation) {
		if key != "" {
			g := groups[key]
			g.scopeKey = key
			g.items = append(g.items, item)
			groups[key] = g
		}
	}
	for _, item := range items {
		key := sonarrDownloadKey(instance, item)
		if key == "" {
			appendRow(incidentScopeKey(instance, "tv", item.DownloadID, item.Media), item)
			continue
		}
		downloads[key] = append(downloads[key], item)
		if episodeAttempts[item.Media.EpisodeID] == nil {
			episodeAttempts[item.Media.EpisodeID] = make(map[string]bool)
		}
		episodeAttempts[item.Media.EpisodeID][key] = true
	}
	for key, members := range downloads {
		sort.Slice(members, func(i, j int) bool { return members[i].Media.QueueID < members[j].Media.QueueID })
		item := members[0]
		prior := current[key]
		if legacyDownloads[strings.ToLower(item.DownloadID)] && prior == nil {
			for _, row := range members {
				appendRow(incidentScopeKey(instance, "tv", row.DownloadID, row.Media), row)
			}
			continue
		}
		scope := key
		parallel := len(episodeAttempts[item.Media.EpisodeID]) > 1
		legacyScope := incidentScopeKey(instance, "tv", item.DownloadID, item.Media)
		if r := byID[occupied[legacyScope]]; prior == nil && !parallel && len(members) == 1 && r != nil && r.issue.memberCount == 0 {
			appendRow(legacyScope, item)
			continue
		}
		if prior == nil && !parallel && len(members) == 1 && len(singletons[item.Media.EpisodeID]) == 1 {
			r := singletons[item.Media.EpisodeID][0]
			if r.issue.SeasonNumber == item.Media.SeasonNumber && r.issue.EpisodeNumber == item.Media.EpisodeNumber &&
				r.issue.TvdbID > 0 && r.issue.TvdbID == item.Media.TvdbID {
				prior = r
			}
		}
		if prior != nil {
			scope = prior.scopeKey
		} else if id := occupied[scope]; id != 0 {
			scope = fmt.Sprintf("%s:after:%d", key, id)
		}
		pack := false
		for _, row := range members {
			if row.Media.EpisodeID != item.Media.EpisodeID {
				pack = true
			}
		}
		if prior != nil && prior.issue.memberCount > 1 {
			pack = true
		}
		groups[scope] = observationGroup{scopeKey: scope, downloadKey: key, items: members, pack: pack, parallel: parallel}
	}
	return groups, nil
}

func (g observationGroup) reportMatches(issue *Issue, item arr.QueueObservation) bool {
	if g.pack || g.parallel || g.downloadKey != "" && (len(g.items) != 1 || issue.EpisodeNumber <= 0) {
		return false
	}
	return mediaScopeMatches(issueMediaContext(issue), item.Media, issue.MediaType)
}

func checkDownloadActionScope(q actionScopeQuerier, id int64) error {
	var grouped bool
	if err := q.QueryRow(`SELECT COUNT(DISTINCT episode_id)>1 FROM issue_sonarr_members WHERE issue_id=?`, id).Scan(&grouped); err != nil {
		return err
	}
	if grouped {
		return fmt.Errorf("shared season downloads support diagnosis only; review the individual files in Import Doctor")
	}
	return nil
}

func seasonMemberBlocked(item arr.QueueObservation) bool {
	return !recoveryObservation(item) && (item.Diagnosis.Severity == arr.SeverityWarning || item.Diagnosis.Severity == arr.SeverityError)
}

func seasonMemberDetail(item arr.QueueObservation) string {
	if item.Diagnosis.Problem == arr.ProblemAlreadyImported {
		return "Sonarr reports an import for this download. Cantinarr is verifying each affected episode."
	}
	return item.Diagnosis.Transparency
}

func (s *Service) blockedMemberReady(issueID int64, now time.Time, settings Settings) (bool, error) {
	var ready bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM issue_sonarr_members WHERE issue_id=?
		AND last_seen=(SELECT MAX(last_seen) FROM issue_sonarr_members WHERE issue_id=?)
		AND problem_since<=? AND quiet_since<=?)`, issueID, issueID,
		now.Add(-time.Duration(settings.ObservationMinMinutes)*time.Minute), now.Add(-time.Duration(settings.ObservationQuietMinutes)*time.Minute)).Scan(&ready)
	return ready, err
}

// The normal MCP episode/season filters cannot express a download attempt:
// two clients can carry the same hash, and season zero is a real season.
// Use the observer's exact key for both grouped reads. Never let model input
// choose a queue row or expand the read to the rest of the library.
func (s *Service) describeSeasonQueue(issue *Issue, diagnose bool) (string, error) {
	var key string
	if err := s.db.QueryRow(`SELECT download_key FROM issue_sonarr_members WHERE issue_id=? ORDER BY last_seen DESC LIMIT 1`, issue.ID).Scan(&key); err != nil {
		return "", err
	}
	items, err := s.fetchQueueSnapshot("sonarr", issue.InstanceID)
	if err != nil {
		return "", err
	}
	var result strings.Builder
	matched := 0
	for _, item := range items {
		if sonarrDownloadKey(issue.InstanceID, item) != key {
			continue
		}
		matched++
		if matched > 20 {
			continue
		}
		fmt.Fprintf(&result, "S%02dE%02d: %s", item.Media.SeasonNumber, item.Media.EpisodeNumber, item.Signal.TrackedDownloadState)
		if diagnose && item.Diagnosis.Transparency != "" {
			fmt.Fprintf(&result, ". %s", seasonMemberDetail(item))
		}
		result.WriteByte('\n')
	}
	if matched == 0 {
		return "Sonarr's complete queue has no rows for this download attempt. Queue absence does not verify that the affected episodes imported.", nil
	}
	return fmt.Sprintf("%d current queue rows for this download attempt; %d recorded episodes. Showing up to 20 rows.\n%s", matched, issue.memberCount, secrets.RedactText(result.String())), nil
}

// Preserve the disposition of this exact attempt. Only a previously unseen
// sibling invalidates machine proof; a human disposition stays final.
func previousSeasonObservation(tx *sql.Tx, g observationGroup, now time.Time) (id int64, handled bool, err error) {
	if g.downloadKey == "" {
		return 0, false, nil
	}
	var resolution string
	err = tx.QueryRow(`SELECT i.id,i.resolution_kind FROM issue_sonarr_members m JOIN issues i ON i.id=m.issue_id
		WHERE m.download_key=? AND i.source='auto' AND i.closed_at IS NOT NULL ORDER BY i.id DESC LIMIT 1`, g.downloadKey).Scan(&id, &resolution)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil || resolution != ResolutionArrStateCleared {
		return 0, true, err
	}
	for _, item := range g.items {
		var seen bool
		err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM issue_sonarr_members WHERE issue_id=? AND download_key=? AND episode_id=?)`,
			id, g.downloadKey, item.Media.EpisodeID).Scan(&seen)
		if err != nil {
			return 0, true, err
		}
		if seen {
			continue
		}
		res, err := tx.Exec(`UPDATE issues SET closed_at=NULL,resolution_kind='',resolution='',status='observing',read=1,
			arr_queue_id=NULL,episode_number=0,dedupe_key=?,updated_at=? WHERE id=? AND reopened_at IS NULL AND closed_at IS NOT NULL`, g.scopeKey, now, id)
		if err != nil {
			return 0, true, err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return 0, true, nil
		}
		if _, err := tx.Exec(`UPDATE issue_observations SET scope_key=?,state='observing',first_seen_at=?,last_activity_at=?,problem_since_at=?,
			settling_since=NULL,updated_at=? WHERE issue_id=?`, g.scopeKey, now, now, now, now, id); err != nil {
			return 0, true, err
		}
		_, err = tx.Exec(`INSERT INTO issue_observation_attempts(issue_id,state,note,created_at)
			VALUES(?,'observing','Additional episode observed after verified import; tracking the same download again.',?)`, id, now)
		return id, true, err
	}
	return 0, true, nil
}

// Persist queue-time evidence while the complete response is still in hand.
// Members never disappear when an episode leaves the queue. Missing intervals
// reset persistence; activity on another row cannot reset this row's quiet time.
func recordSeasonMembers(tx *sql.Tx, id int64, g observationGroup, now time.Time) error {
	if g.downloadKey == "" {
		return nil
	}
	for _, item := range g.items {
		var since any
		if !recoveryObservation(item) && groupWarrantsAutoIncident(observationGroup{items: []arr.QueueObservation{item}}, now) {
			since = now
		}
		var baselineAt *time.Time
		if item.AddedAt == nil && item.FileIDAtSnapshot != nil {
			baselineAt = &now
		}
		// Without Added, a changed queue id or a return after a complete read
		// missed this member can be hash reuse. Keep the member, but retire
		// its earlier baseline as an import witness.
		_, err := tx.Exec(`INSERT INTO issue_sonarr_members
			(issue_id,series_id,episode_id,episode_number,queue_id,download_key,download_id,client_name,added_at,queue_file_id,baseline_observed_at,signature,problem_since,quiet_since,last_seen)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(issue_id,download_key,episode_id) DO UPDATE SET
			queue_id=excluded.queue_id, signature=excluded.signature,last_seen=excluded.last_seen,
			baseline_observed_at=CASE WHEN added_at IS NULL AND (queue_id!=excluded.queue_id OR EXISTS (
				SELECT 1 FROM issue_observations o WHERE o.issue_id=issue_sonarr_members.issue_id
				AND (o.state='settling' OR o.last_seen_at>issue_sonarr_members.last_seen))) THEN NULL
				WHEN queue_file_id IS NULL THEN excluded.baseline_observed_at ELSE baseline_observed_at END,
			queue_file_id=COALESCE(queue_file_id,excluded.queue_file_id),
			quiet_since=CASE WHEN signature=excluded.signature THEN quiet_since ELSE excluded.quiet_since END,
			problem_since=CASE WHEN excluded.problem_since IS NULL THEN NULL ELSE COALESCE(problem_since,excluded.problem_since) END`,
			id, item.Media.SeriesID, item.Media.EpisodeID, item.Media.EpisodeNumber, item.Media.QueueID, g.downloadKey, item.DownloadID, secrets.RedactText(item.Media.DownloadClient), item.AddedAt,
			item.FileIDAtSnapshot, baselineAt, observationSignature([]arr.QueueObservation{item}), since, now, now)
		if err != nil {
			return err
		}
	}
	_, err := tx.Exec(`UPDATE issue_sonarr_members SET problem_since=NULL WHERE issue_id=? AND last_seen < ? AND problem_since IS NOT NULL`, id, now)
	return err
}

// Verify every recorded episode against Sonarr's current season list and an
// import receipt for this exact episode, hash, attempt and current file. One
// receipt for a multi-episode file still attests only to its named episode.
func (s *Service) seasonRecovery(issue *Issue) (arrRecoveryProbe, error) {
	rows, err := s.db.Query(`SELECT series_id,episode_id,episode_number,download_key,download_id,client_name,added_at,baseline_observed_at,queue_file_id,history_id,imported_file_id,
		EXISTS(SELECT 1 FROM issue_sonarr_members other JOIN issues i ON other.issue_id=i.id
			WHERE i.instance_id=? AND other.episode_id=m.episode_id AND other.download_id=m.download_id AND other.download_key!=m.download_key)
		FROM issue_sonarr_members m WHERE issue_id=? AND download_key=(
		SELECT download_key FROM issue_sonarr_members WHERE issue_id=? ORDER BY last_seen DESC LIMIT 1) ORDER BY episode_id`, issue.InstanceID, issue.ID, issue.ID)
	if err != nil {
		return arrRecoveryProbe{}, err
	}
	type member struct {
		key, downloadID, clientName string
		series, id, number          int
		added, baselineAt           sql.NullTime
		queueFile, history, file    sql.NullInt64
		reusedHash                  bool
	}
	var members []member
	for rows.Next() {
		var m member
		if err := rows.Scan(&m.series, &m.id, &m.number, &m.key, &m.downloadID, &m.clientName, &m.added, &m.baselineAt, &m.queueFile, &m.history, &m.file, &m.reusedHash); err != nil {
			rows.Close()
			return arrRecoveryProbe{}, err
		}
		members = append(members, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(members) == 0 {
		return arrRecoveryProbe{}, err
	}
	client, err := s.registry.GetSonarrClient(issue.InstanceID)
	if err != nil {
		return arrRecoveryProbe{}, err
	}
	eps, err := client.GetEpisodes(members[0].series, issue.SeasonNumber)
	if err != nil {
		return arrRecoveryProbe{}, err
	}
	live := make(map[int]sonarr.Episode, len(eps))
	for _, ep := range eps {
		if _, duplicate := live[ep.ID]; duplicate {
			return arrRecoveryProbe{}, fmt.Errorf("ambiguous Sonarr episode identity")
		}
		live[ep.ID] = ep
	}
	proven := 0
	for _, m := range members {
		ep := live[m.id]
		if ep.SeriesID != m.series || ep.SeasonNumber != issue.SeasonNumber || ep.EpisodeNumber != m.number || !ep.HasFile || ep.EpisodeFileID <= 0 {
			continue
		}
		if m.added.Valid && !m.reusedHash && m.history.Int64 > 0 && m.file.Int64 == int64(ep.EpisodeFileID) {
			proven++
			continue
		}
		if !m.queueFile.Valid {
			continue
		}
		// A manually queued transfer can have no Sonarr Added time. Only a
		// changed, known file baseline and a later exact import can date it.
		// Recheck these receipts live, including ones credited on earlier polls.
		if !m.added.Valid && (!m.baselineAt.Valid || m.baselineAt.Time.IsZero() || m.queueFile.Int64 < 0 ||
			m.queueFile.Int64 == int64(ep.EpisodeFileID) || strings.TrimSpace(m.clientName) == "" || m.reusedHash) {
			continue
		}
		history, err := client.GetImportHistory(m.id, m.downloadID, 20)
		if err != nil {
			return arrRecoveryProbe{}, err
		}
		for _, h := range history {
			clientName := h.Data["downloadClientName"]
			if !m.added.Valid && (!h.Date.After(m.baselineAt.Time) || clientName != m.clientName ||
				h.Episode == nil || h.Episode.SeriesID != m.series) {
				continue
			}
			file, ok := historyFileID(h.Data)
			if !ok || file != int64(ep.EpisodeFileID) || h.ID <= 0 || h.SeriesID != m.series || h.EpisodeID != m.id ||
				!strings.EqualFold(h.EventType, "downloadFolderImported") || !strings.EqualFold(h.DownloadID, m.downloadID) || (m.added.Valid && h.Date.Before(m.added.Time)) ||
				(m.clientName != "" && clientName != "" && secrets.RedactText(clientName) != m.clientName) ||
				(h.Series != nil && h.Series.ID != m.series) || (h.Episode != nil && (h.Episode.ID != m.id || h.Episode.SeriesID != 0 && h.Episode.SeriesID != m.series || h.Episode.EpisodeNumber != m.number || h.Episode.SeasonNumber != issue.SeasonNumber)) {
				continue
			}
			// A reused hash is not an attempt identifier. If another observed
			// attempt could own this receipt, do not credit either one. An older
			// attempt can still be active on another client when a newer one starts.
			result, err := s.db.Exec(`UPDATE issue_sonarr_members AS member SET history_id=?,imported_file_id=? WHERE issue_id=? AND episode_id=? AND download_key=?
				AND NOT EXISTS (SELECT 1 FROM issue_sonarr_members other JOIN issues i ON i.id=other.issue_id
				WHERE i.instance_id=? AND other.download_id=member.download_id AND (
				    (member.added_at IS NULL AND (other.download_key!=member.download_key OR other.issue_id!=member.issue_id))
				    OR (member.added_at IS NOT NULL AND other.download_key!=member.download_key AND other.episode_id=member.episode_id
				        AND (other.added_at IS NULL OR (other.added_at<=? AND (other.added_at>=member.added_at OR other.last_seen>=member.added_at)))
				        AND (?='' OR other.client_name='' OR other.client_name=?))))`, h.ID, file, issue.ID, m.id, m.key,
				issue.InstanceID, h.Date, secrets.RedactText(clientName), secrets.RedactText(clientName))
			if err != nil {
				return arrRecoveryProbe{}, err
			}
			if n, _ := result.RowsAffected(); n != 1 {
				continue
			}
			proven++
			break
		}
	}
	if proven > 0 && proven < len(members) {
		return arrRecoveryProbe{needsAdmin: true, reason: fmt.Sprintf("Verified imports for %d of %d affected episodes. Review the remaining episodes in Import Doctor.", proven, len(members))}, nil
	}
	return arrRecoveryProbe{completed: proven == len(members), verifiedMembers: proven == len(members)}, nil
}
