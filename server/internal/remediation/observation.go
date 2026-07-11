package remediation

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

const (
	observationStateObserving  = "observing"
	observationStateRecovering = "recovering"
	observationStateSettling   = "settling"
	queueSnapshotFreshness     = 90 * time.Second
	observationSweepPeriod     = time.Minute
)

const recoveryInFlightResult = "Superseded because the live arr state changed or the arr continued its own retry before approval; no fix was executed."
const arrStateClearedResolution = "The requested movie or episode is now available."
const observationNeedsCloserLook = "We couldn't confirm the latest status from the connected media service. We didn't make automated changes, and this needs a closer look."

var errStaleObservation = errors.New("stale observation")

type observationRecord struct {
	issue            *Issue
	serviceType      string
	scopeKey         string
	state            string
	signature        string
	firstSeen        time.Time
	problemSince     sql.NullTime
	lastActivity     time.Time
	settlingSince    sql.NullTime
	promotedAt       sql.NullTime
	baselineHasFile  sql.NullBool
	baselineFileID   sql.NullInt64
	baselineCaptured sql.NullTime
}

type observationGroup struct {
	scopeKey string
	items    []arr.QueueObservation
}

type queueSnapshotJob struct {
	serviceType string
	instanceID  string
	items       []arr.QueueObservation
	failure     error
	observedAt  time.Time
}

// incidentScopeKey is deliberately independent of the download-client id when
// the arr supplied exact media identity. A failed download and its replacement
// are therefore one incident, while two episodes of the same series remain
// distinct. The download id is only the fail-closed fallback for incomplete
// queue payloads.
func incidentScopeKey(instanceID, mediaType, downloadID string, media arr.QueueMediaContext) string {
	var scope string
	switch mediaType {
	case "radarr", "movie":
		if media.TmdbID > 0 {
			scope = fmt.Sprintf("%s|movie|tmdb:%d", instanceID, media.TmdbID)
		}
	case "sonarr", "tv":
		if media.EpisodeNumber > 0 && media.TvdbID > 0 {
			scope = fmt.Sprintf("%s|tv|tvdb:%d|s:%d|e:%d", instanceID, media.TvdbID, media.SeasonNumber, media.EpisodeNumber)
		} else if media.EpisodeNumber > 0 && media.TmdbID > 0 {
			scope = fmt.Sprintf("%s|tv|tmdb:%d|s:%d|e:%d", instanceID, media.TmdbID, media.SeasonNumber, media.EpisodeNumber)
		}
	}
	if scope == "" && downloadID != "" {
		scope = instanceID + "|download:" + downloadID
	}
	if scope == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(scope))
	return hex.EncodeToString(sum[:])
}

func userIncidentScopeKey(instanceID, mediaType string, media arr.QueueMediaContext) string {
	scope := fmt.Sprintf("%s|%s|tmdb:%d|tvdb:%d|s:%d|e:%d",
		instanceID, mediaType, media.TmdbID, media.TvdbID, media.SeasonNumber, media.EpisodeNumber)
	sum := sha256.Sum256([]byte(scope))
	return hex.EncodeToString(sum[:])
}

func issueMediaContext(issue *Issue) arr.QueueMediaContext {
	return arr.QueueMediaContext{
		QueueID: issue.ArrQueueID, Title: issue.Title, TmdbID: issue.TmdbID,
		TvdbID: issue.TvdbID, SeasonNumber: issue.SeasonNumber,
		EpisodeNumber: issue.EpisodeNumber,
	}
}

func serviceMediaType(serviceType string) string {
	switch serviceType {
	case "radarr":
		return "movie"
	case "sonarr":
		return "tv"
	case "chaptarr":
		return "book"
	default:
		return serviceType
	}
}

func mediaServiceType(mediaType string) string {
	switch mediaType {
	case "movie":
		return "radarr"
	case "tv":
		return "sonarr"
	case "book":
		return "chaptarr"
	default:
		return mediaType
	}
}

func observationSignature(items []arr.QueueObservation) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, fmt.Sprintf("%s|%d|%s|%s|%s|%.0f|%.0f|%s|%s",
			item.DownloadID, item.Media.QueueID,
			strings.ToLower(item.Signal.Status),
			strings.ToLower(item.Signal.TrackedDownloadStatus),
			strings.ToLower(item.Signal.TrackedDownloadState),
			item.Signal.Size, item.Signal.SizeLeft,
			item.Diagnosis.Severity, item.Diagnosis.Problem))
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

func recoveryObservation(item arr.QueueObservation) bool {
	state := strings.ToLower(item.Signal.TrackedDownloadState)
	if state == "failedpending" {
		return true
	}
	if item.Diagnosis.Severity == arr.SeverityOK {
		return true
	}
	status := strings.ToLower(item.Signal.TrackedDownloadStatus)
	return status != "error" && item.Signal.Size > 0 && item.Signal.SizeLeft > 0 && item.Signal.SizeLeft < item.Signal.Size
}

func groupIsRecovery(group observationGroup, currentDownloadID string) bool {
	for _, item := range group.items {
		if recoveryObservation(item) {
			return true
		}
		if currentDownloadID != "" && item.DownloadID != "" && item.DownloadID != currentDownloadID {
			return true // same exact media scope, new download identity: arr replacement.
		}
	}
	return false
}

func groupHasProblemSignal(group observationGroup) bool {
	for _, item := range group.items {
		if item.Diagnosis.Severity == arr.SeverityWarning || item.Diagnosis.Severity == arr.SeverityError {
			return true
		}
	}
	return false
}

func selectObservation(group observationGroup, currentDownloadID string) arr.QueueObservation {
	if len(group.items) == 0 {
		return arr.QueueObservation{}
	}
	for _, item := range group.items {
		if currentDownloadID != "" && item.DownloadID != "" && item.DownloadID != currentDownloadID {
			return item
		}
	}
	for _, item := range group.items {
		if recoveryObservation(item) {
			return item
		}
	}
	return group.items[0]
}

func groupQueueObservations(serviceType, instanceID string, items []arr.QueueObservation) map[string]observationGroup {
	groups := make(map[string]observationGroup)
	for _, item := range items {
		key := incidentScopeKey(instanceID, serviceType, item.DownloadID, item.Media)
		if key == "" {
			continue
		}
		group := groups[key]
		group.scopeKey = key
		group.items = append(group.items, item)
		groups[key] = group
	}
	return groups
}

func mediaScopeMatches(want, got arr.QueueMediaContext, mediaType string) bool {
	if want.TvdbID > 0 && got.TvdbID > 0 {
		if want.TvdbID != got.TvdbID {
			return false
		}
	} else if want.TmdbID > 0 && got.TmdbID > 0 {
		if want.TmdbID != got.TmdbID {
			return false
		}
	} else {
		return false
	}
	if mediaType == "movie" {
		return true
	}
	if want.EpisodeNumber > 0 && want.EpisodeNumber != got.EpisodeNumber {
		return false
	}
	if want.EpisodeNumber > 0 && want.SeasonNumber != got.SeasonNumber {
		return false
	}
	if want.EpisodeNumber == 0 && want.SeasonNumber > 0 && want.SeasonNumber != got.SeasonNumber {
		return false
	}
	return true
}

// observeQueueSnapshot reconciles one successful COMPLETE queue read. It first
// commits the snapshot cache, then advances every matching durable incident.
// Empty snapshots are evidence; failed reads never reach this method.
func (s *Service) observeQueueSnapshot(serviceType, instanceID string, items []arr.QueueObservation, now time.Time) error {
	s.observationMu.Lock()
	defer s.observationMu.Unlock()
	if serviceType != "radarr" && serviceType != "sonarr" {
		return fmt.Errorf("unsupported observation service %q", serviceType)
	}
	if strings.TrimSpace(instanceID) == "" {
		return fmt.Errorf("missing observation instance")
	}
	if err := s.storeQueueSnapshot(serviceType, instanceID, items, now); errors.Is(err, errStaleObservation) {
		return nil
	} else if err != nil {
		return err
	}

	groups := groupQueueObservations(serviceType, instanceID, items)
	records, err := s.loadObservationRecords(serviceType, instanceID, now)
	if err != nil {
		return err
	}
	if err := s.attachUnobservedUserIssues(serviceType, instanceID, groups, now); err != nil {
		return err
	}
	// Reload because newly attached user issues must participate in this same
	// snapshot's dedupe/reconciliation (and prevent a duplicate auto incident).
	records, err = s.loadObservationRecords(serviceType, instanceID, now)
	if err != nil {
		return err
	}
	byScope := make(map[string][]*observationRecord)
	for _, record := range records {
		byScope[record.scopeKey] = append(byScope[record.scopeKey], record)
	}

	settings := s.Settings()
	if settings.Enabled && settings.AutoDispatch {
		for key, group := range groups {
			hasMatchingRecord := len(byScope[key]) != 0
			if !hasMatchingRecord {
				for _, record := range records {
					for _, item := range group.items {
						if record.issue.Source == SourceUser && mediaScopeMatches(issueMediaContext(record.issue), item.Media, record.issue.MediaType) {
							hasMatchingRecord = true
							break
						}
					}
				}
			}
			if hasMatchingRecord || !groupHasProblemSignal(group) {
				continue
			}
			record, createErr := s.createAutoObservation(serviceType, instanceID, group, now)
			if createErr != nil {
				return createErr
			}
			if record != nil {
				records = append(records, record)
				byScope[key] = append(byScope[key], record)
			}
		}
	}

	for _, record := range records {
		group, matched := groups[record.scopeKey]
		if !matched && record.issue.Source == SourceUser {
			for _, candidate := range groups {
				for _, item := range candidate.items {
					if mediaScopeMatches(issueMediaContext(record.issue), item.Media, record.issue.MediaType) {
						group.scopeKey = record.scopeKey
						group.items = append(group.items, candidate.items...)
						matched = true
						break
					}
				}
			}
		}
		if matched {
			if err := s.applyMatchingObservation(record, group, now, settings); err != nil {
				return err
			}
			continue
		}
		if err := s.applyAbsentObservation(record, now, settings); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) attachUnobservedUserIssues(serviceType, instanceID string, groups map[string]observationGroup, now time.Time) error {
	rows, err := s.db.Query(
		`SELECT i.id FROM issues i
		 WHERE i.source=? AND i.instance_id=? AND i.closed_at IS NULL
		   AND NOT EXISTS (SELECT 1 FROM issue_observations o WHERE o.issue_id=i.id)`,
		SourceUser, instanceID,
	)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		issue, err := s.GetIssue(id)
		if err != nil {
			return err
		}
		var matched []arr.QueueObservation
		for _, group := range groups {
			for _, item := range group.items {
				if mediaScopeMatches(issueMediaContext(issue), item.Media, issue.MediaType) {
					matched = append(matched, item)
				}
			}
		}
		if len(matched) == 0 {
			continue
		}
		if _, err := s.attachUserObservation(id, observationGroup{
			scopeKey: userIncidentScopeKey(instanceID, issue.MediaType, issueMediaContext(issue)),
			items:    matched,
		}, now); err != nil {
			return fmt.Errorf("attach user issue %d to %s recovery: %w", id, serviceType, err)
		}
	}
	return nil
}

func (s *Service) storeQueueSnapshot(serviceType, instanceID string, items []arr.QueueObservation, now time.Time) error {
	// Persist only the typed fields observation needs. Upstream error/status
	// bodies can contain paths, URLs, or credentials and never cross this DB
	// boundary.
	safeItems := make([]arr.QueueObservation, 0, len(items))
	for _, item := range items {
		safeItems = append(safeItems, arr.QueueObservation{
			DownloadID: item.DownloadID,
			Media: arr.QueueMediaContext{
				QueueID: item.Media.QueueID, Title: secrets.RedactText(item.Media.Title),
				TmdbID: item.Media.TmdbID, TvdbID: item.Media.TvdbID,
				SeasonNumber: item.Media.SeasonNumber, EpisodeNumber: item.Media.EpisodeNumber,
			},
			Signal: arr.QueueSignal{
				Status: item.Signal.Status, TrackedDownloadStatus: item.Signal.TrackedDownloadStatus,
				TrackedDownloadState: item.Signal.TrackedDownloadState,
				Size:                 item.Signal.Size, SizeLeft: item.Signal.SizeLeft,
			},
			Diagnosis: arr.Diagnosis{Severity: item.Diagnosis.Severity, Problem: secrets.RedactText(item.Diagnosis.Problem)},
		})
	}
	encoded, err := json.Marshal(safeItems)
	if err != nil {
		return fmt.Errorf("encode queue snapshot: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin queue snapshot: %w", err)
	}
	defer tx.Rollback()
	claim, err := tx.Exec(
		`INSERT INTO remediation_observation_watermarks(instance_id,service_type,observed_at)
		 VALUES (?,?,?) ON CONFLICT(instance_id) DO UPDATE SET
		 service_type=excluded.service_type,observed_at=excluded.observed_at
		 WHERE excluded.observed_at > remediation_observation_watermarks.observed_at`,
		instanceID, serviceType, now,
	)
	if err != nil {
		return fmt.Errorf("claim queue snapshot watermark: %w", err)
	}
	if n, _ := claim.RowsAffected(); n != 1 {
		return errStaleObservation
	}
	_, err = tx.Exec(
		`INSERT INTO remediation_queue_snapshots(instance_id, service_type, observed_at, items_json)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(instance_id) DO UPDATE SET service_type=excluded.service_type,
		 observed_at=excluded.observed_at, items_json=excluded.items_json`,
		instanceID, serviceType, now, string(encoded),
	)
	if err != nil {
		return fmt.Errorf("store queue snapshot: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM remediation_observation_failures WHERE instance_id=?", instanceID); err != nil {
		return fmt.Errorf("reset queue observation failure: %w", err)
	}
	return tx.Commit()
}

func (s *Service) noteObservationFailure(serviceType, instanceID string, cause error, now time.Time) {
	s.observationMu.Lock()
	defer s.observationMu.Unlock()
	message := "queue_read_failed"
	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	claim, err := tx.Exec(
		`INSERT INTO remediation_observation_watermarks(instance_id,service_type,observed_at)
		 VALUES (?,?,?) ON CONFLICT(instance_id) DO UPDATE SET
		 service_type=excluded.service_type,observed_at=excluded.observed_at
		 WHERE excluded.observed_at > remediation_observation_watermarks.observed_at`,
		instanceID, serviceType, now,
	)
	if err != nil {
		return
	}
	if n, _ := claim.RowsAffected(); n != 1 {
		return
	}
	_, err = tx.Exec(
		`INSERT INTO remediation_observation_failures
		 (instance_id,service_type,first_failed_at,last_failed_at,error_text)
		 VALUES (?,?,?,?,?)
		 ON CONFLICT(instance_id) DO UPDATE SET service_type=excluded.service_type,
		 last_failed_at=excluded.last_failed_at,error_text=excluded.error_text`,
		instanceID, serviceType, now, now, message,
	)
	if err != nil {
		return
	}
	if err := tx.Commit(); err != nil {
		return
	}
	var first time.Time
	if err := s.db.QueryRow(
		"SELECT first_failed_at FROM remediation_observation_failures WHERE instance_id=?",
		instanceID,
	).Scan(&first); err != nil {
		return
	}
	if now.Sub(first) >= time.Duration(s.Settings().ObservationMinMinutes)*time.Minute {
		s.promoteObservationUnavailable(serviceType, instanceID, now)
	}
}

func (s *Service) promoteObservationUnavailable(serviceType, instanceID string, now time.Time) {
	rows, err := s.db.Query(
		`SELECT i.id,i.title,i.source FROM issues i
		 JOIN issue_observations o ON o.issue_id=i.id
		 WHERE o.service_type=? AND i.instance_id=? AND i.closed_at IS NULL
		   AND i.status IN (?,?)`,
		serviceType, instanceID, IssueObserving, IssueRecovering,
	)
	if err != nil {
		return
	}
	type pending struct {
		id            int64
		title, source string
	}
	var issues []pending
	for rows.Next() {
		var issue pending
		if rows.Scan(&issue.id, &issue.title, &issue.source) == nil {
			issues = append(issues, issue)
		}
	}
	rows.Close()
	for _, issue := range issues {
		_ = s.promoteObservationNeedsAdmin(issue.id, now, observationNeedsCloserLook)
	}
}

func (s *Service) loadObservationRecords(serviceType, instanceID string, now time.Time) ([]*observationRecord, error) {
	// Auto incidents created by older builds did not have the sidecar row. Seed
	// it from their exact stored media scope, treating already-visible states as
	// previously promoted. User issues are seeded only by the new report path.
	rows, err := s.db.Query(
		`SELECT id FROM issues WHERE instance_id = ? AND source = ? AND closed_at IS NULL`,
		instanceID, SourceAuto,
	)
	if err != nil {
		return nil, err
	}
	var autoIDs []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			autoIDs = append(autoIDs, id)
		}
	}
	rows.Close()
	for _, id := range autoIDs {
		issue, getErr := s.GetIssue(id)
		if getErr != nil {
			continue
		}
		key := incidentScopeKey(instanceID, issue.MediaType, issue.DownloadID, issueMediaContext(issue))
		if key == "" {
			continue
		}
		var promoted any
		if issue.Status != IssueObserving && issue.Status != IssueRecovering {
			promoted = issue.CreatedAt
		}
		_, _ = s.db.Exec(
			`INSERT OR IGNORE INTO issue_observations
			 (issue_id, service_type, scope_key, state, first_seen_at, problem_since_at,
			  last_activity_at, promoted_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, serviceType, key, observationStateObserving, issue.CreatedAt,
			issue.CreatedAt, issue.CreatedAt, promoted, now,
		)
	}

	rows, err = s.db.Query(
		`SELECT o.issue_id, o.service_type, o.scope_key, o.state, o.signature,
		        o.first_seen_at, o.problem_since_at, o.last_activity_at,
		        o.settling_since, o.promoted_at, o.baseline_has_file,
		        o.baseline_file_id, o.baseline_captured_at
		 FROM issue_observations o JOIN issues i ON i.id = o.issue_id
		 WHERE o.service_type = ? AND i.instance_id = ? AND i.closed_at IS NULL
		 ORDER BY o.issue_id`, serviceType, instanceID,
	)
	if err != nil {
		return nil, err
	}
	var records []*observationRecord
	for rows.Next() {
		record := &observationRecord{}
		var issueID int64
		if err := rows.Scan(&issueID, &record.serviceType, &record.scopeKey,
			&record.state, &record.signature, &record.firstSeen,
			&record.problemSince, &record.lastActivity, &record.settlingSince,
			&record.promotedAt, &record.baselineHasFile, &record.baselineFileID,
			&record.baselineCaptured); err != nil {
			rows.Close()
			return nil, err
		}
		record.issue = &Issue{ID: issueID}
		records = append(records, record)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, record := range records {
		issue, getErr := s.GetIssue(record.issue.ID)
		if getErr != nil {
			return nil, getErr
		}
		record.issue = issue
	}
	return records, nil
}

func (s *Service) ensureIssueObservation(issue *Issue, serviceType string, now time.Time) (string, error) {
	if issue == nil {
		return "", fmt.Errorf("missing issue")
	}
	key := incidentScopeKey(issue.InstanceID, issue.MediaType, issue.DownloadID, issueMediaContext(issue))
	if issue.Source == SourceUser {
		key = userIncidentScopeKey(issue.InstanceID, issue.MediaType, issueMediaContext(issue))
	}
	if key == "" {
		return "", nil
	}
	var promoted any
	if issue.Status != IssueObserving && issue.Status != IssueRecovering {
		promoted = issue.CreatedAt
	}
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO issue_observations
		 (issue_id,service_type,scope_key,state,first_seen_at,problem_since_at,
		  last_activity_at,promoted_at,updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		issue.ID, serviceType, key, observationStateObserving, issue.CreatedAt,
		issue.CreatedAt, issue.CreatedAt, promoted, now,
	)
	return key, err
}

func (s *Service) attachUserObservation(issueID int64, group observationGroup, now time.Time) (string, error) {
	issue, err := s.GetIssue(issueID)
	if err != nil {
		return "", err
	}
	if _, err := s.ensureIssueObservation(issue, mediaServiceType(issue.MediaType), now); err != nil {
		return "", err
	}
	if err := s.recordObservationDownloads(issueID, group.items, now); err != nil {
		return "", err
	}
	item := selectObservation(group, issue.DownloadID)
	state := observationStateObserving
	status := IssueObserving
	var problemSince any = now
	if groupIsRecovery(group, issue.DownloadID) {
		state = observationStateRecovering
		status = IssueRecovering
		problemSince = nil
	}
	signature := observationSignature(group.items)
	if _, err := s.db.Exec(
		`UPDATE issue_observations SET scope_key=?,state=?,signature=?,problem_since_at=?,
		 last_seen_at=?,last_activity_at=?,settling_since=NULL,updated_at=? WHERE issue_id=?`,
		userIncidentScopeKey(issue.InstanceID, issue.MediaType, issueMediaContext(issue)),
		state, signature, problemSince, now, now, now, issueID,
	); err != nil {
		return "", err
	}
	if _, err := s.suspendIssueForRecovery(issueID, item, now); err != nil {
		return "", err
	}
	if status == IssueObserving {
		_, err = s.db.Exec("UPDATE issues SET status=?,read=1,updated_at=? WHERE id=? AND status=? AND closed_at IS NULL",
			IssueObserving, now, issueID, IssueRecovering)
	}
	_ = s.recordObservationAttempt(issueID, state, signature, item, "user report matched active arr queue work", now)
	if err != nil {
		return "", err
	}
	current, err := s.GetIssue(issueID)
	if err != nil {
		return "", err
	}
	return current.Status, nil
}

func (s *Service) createAutoObservation(serviceType, instanceID string, group observationGroup, now time.Time) (*observationRecord, error) {
	item := selectObservation(group, "")
	recovering := groupIsRecovery(group, "")
	issueStatus := IssueObserving
	observationState := observationStateObserving
	var problemSince any = now
	if recovering {
		issueStatus = IssueRecovering
		observationState = observationStateRecovering
		problemSince = nil
	}
	mediaType := serviceMediaType(serviceType)
	tmdbID := item.Media.TmdbID
	if tmdbID == 0 && item.Media.TvdbID > 0 {
		_ = s.db.QueryRow("SELECT tmdb_id FROM tmdb_tvdb_cache WHERE tvdb_id = ?", item.Media.TvdbID).Scan(&tmdbID)
	}
	title := item.Media.Title
	if title == "" {
		title = item.Diagnosis.Problem
	}
	res, err := s.db.Exec(
		`INSERT INTO issues
		 (source, status, media_type, tmdb_id, tvdb_id, title, season_number,
		  episode_number, instance_id, download_id, arr_queue_id, detail,
		  dedupe_key, read, created_at, updated_at)
		 SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?
		 WHERE NOT EXISTS (SELECT 1 FROM issues WHERE dedupe_key = ? AND closed_at IS NULL)`,
		SourceAuto, issueStatus, mediaType, tmdbID, sqlNullInt(item.Media.TvdbID), secrets.RedactText(title),
		item.Media.SeasonNumber, item.Media.EpisodeNumber, instanceID,
		sqlNullStr(item.DownloadID), sqlNullInt(item.Media.QueueID), secrets.RedactText(item.Diagnosis.Transparency),
		group.scopeKey, now, now, group.scopeKey,
	)
	if err != nil {
		return nil, fmt.Errorf("create observed issue: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	signature := observationSignature(group.items)
	if _, err := s.db.Exec(
		`INSERT INTO issue_observations
		 (issue_id, service_type, scope_key, state, signature, first_seen_at,
		  problem_since_at, last_seen_at, last_activity_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, serviceType, group.scopeKey, observationState, signature,
		now, problemSince, now, now, now,
	); err != nil {
		return nil, fmt.Errorf("create issue observation: %w", err)
	}
	if err := s.recordObservationDownloads(id, group.items, now); err != nil {
		return nil, err
	}
	_ = s.recordObservationAttempt(id, observationState, signature, item, "problem first observed", now)
	issue, err := s.GetIssue(id)
	if err != nil {
		return nil, err
	}
	_ = s.captureObservationBaseline(issue)
	return &observationRecord{
		issue: issue, serviceType: serviceType, scopeKey: group.scopeKey,
		state: observationState, signature: signature,
		firstSeen: now, problemSince: sql.NullTime{Time: now, Valid: !recovering},
		lastActivity: now,
	}, nil
}

func (s *Service) applyMatchingObservation(record *observationRecord, group observationGroup, now time.Time, settings Settings) error {
	if err := s.recordObservationDownloads(record.issue.ID, group.items, now); err != nil {
		return err
	}
	// Baselines are captured only while this exact media scope is present in a
	// successful complete queue snapshot. This applies to user and auto issues;
	// user complaints are not auto-closed, but stale mutations must still stop.
	_ = s.captureObservationBaseline(record.issue)
	item := selectObservation(group, record.issue.DownloadID)
	signature := observationSignature(group.items)
	recovering := groupIsRecovery(group, record.issue.DownloadID)
	state := observationStateObserving
	if recovering {
		state = observationStateRecovering
	}
	changed := signature != record.signature || state != record.state
	auditTransition := state != record.state || item.DownloadID != record.issue.DownloadID || item.Media.QueueID != record.issue.ArrQueueID
	lastActivity := record.lastActivity
	if changed || lastActivity.IsZero() {
		lastActivity = now
	}
	problemSince := record.problemSince
	if recovering {
		problemSince = sql.NullTime{}
	} else if record.state != observationStateObserving || !problemSince.Valid {
		problemSince = sql.NullTime{Time: now, Valid: true}
		lastActivity = now
	}

	_, err := s.db.Exec(
		`UPDATE issue_observations SET state = ?, signature = ?, problem_since_at = ?,
		 last_seen_at = ?, last_activity_at = ?, settling_since = NULL, updated_at = ?
		 WHERE issue_id = ?`,
		state, signature, nullTime(problemSince), now, lastActivity, now, record.issue.ID,
	)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(
		`UPDATE issues SET download_id = ?, arr_queue_id = ?,
		 title = CASE WHEN source = ? AND ? != '' THEN ? ELSE title END,
		 tmdb_id = CASE WHEN source = ? AND ? > 0 THEN ? ELSE tmdb_id END,
		 tvdb_id = CASE WHEN source = ? AND ? > 0 THEN ? ELSE tvdb_id END,
		 season_number = CASE WHEN source = ? THEN ? ELSE season_number END,
		 episode_number = CASE WHEN source = ? THEN ? ELSE episode_number END,
		 detail = CASE WHEN source = ? AND ? != '' THEN ? ELSE detail END,
		 updated_at = ? WHERE id = ? AND closed_at IS NULL`,
		sqlNullStr(item.DownloadID), sqlNullInt(item.Media.QueueID), SourceAuto, item.Media.Title, item.Media.Title,
		SourceAuto, item.Media.TmdbID, item.Media.TmdbID, SourceAuto, item.Media.TvdbID, item.Media.TvdbID,
		SourceAuto, item.Media.SeasonNumber, SourceAuto, item.Media.EpisodeNumber, SourceAuto,
		secrets.RedactText(item.Diagnosis.Transparency), secrets.RedactText(item.Diagnosis.Transparency), now, record.issue.ID,
	); err != nil {
		return err
	}
	if auditTransition {
		note := "problem remains unchanged"
		if recovering {
			note = "arr work is active or a same-scope replacement appeared"
		}
		_ = s.recordObservationAttempt(record.issue.ID, state, signature, item, note, now)
	}
	// Any fresh same-scope transition invalidates a diagnosis, not only a row we
	// classify as recovery. Reclaim visible/running work until the new signature
	// survives the configured quiet window.
	if changed && record.issue.Status != IssueObserving && record.issue.Status != IssueRecovering {
		_, err := s.suspendIssueForRecovery(record.issue.ID, item, now)
		return err
	}
	if recovering {
		staleRecovery := now.Sub(record.firstSeen) >= time.Duration(settings.ObservationMinMinutes)*time.Minute &&
			now.Sub(lastActivity) >= time.Duration(settings.ObservationQuietMinutes)*time.Minute
		// An unchanged recovery signature that was already promoted remains
		// actionable. Do not re-run expensive library/history proof every poll.
		if record.promotedAt.Valid && record.state == observationStateRecovering && signature == record.signature &&
			record.issue.Status != IssueObserving && record.issue.Status != IssueRecovering {
			return nil
		}
		if staleRecovery {
			probe, guardErr := s.exactRecoveryGuard(record.issue)
			if guardErr != nil {
				return s.promoteObservationNeedsAdmin(record.issue.ID, now, observationNeedsCloserLook)
			}
			if probe.completed {
				return s.closeObservedRecovery(record.issue.ID, record.promotedAt.Valid)
			}
			if probe.needsAdmin {
				return s.promoteObservationNeedsAdmin(record.issue.ID, now, probe.reason)
			}
			return s.promoteObservedIssue(record.issue.ID, now)
		}
		_, err := s.suspendIssueForRecovery(record.issue.ID, item, now)
		return err
	}

	if record.issue.Status == IssueRecovering {
		if _, err := s.db.Exec(
			`UPDATE issues SET status = ?, read = 1, active_run_id = NULL,
			 resolution = '', resolution_kind = '', updated_at = ?
			 WHERE id = ? AND status = ? AND closed_at IS NULL`,
			IssueObserving, now, record.issue.ID, IssueRecovering,
		); err != nil {
			return err
		}
	}
	if problemSince.Valid && now.Sub(problemSince.Time) >= time.Duration(settings.ObservationMinMinutes)*time.Minute &&
		now.Sub(lastActivity) >= time.Duration(settings.ObservationQuietMinutes)*time.Minute {
		probe, guardErr := s.exactRecoveryGuard(record.issue)
		if guardErr != nil {
			return s.promoteObservationNeedsAdmin(record.issue.ID, now, observationNeedsCloserLook)
		}
		if probe.completed {
			return s.closeObservedRecovery(record.issue.ID, record.promotedAt.Valid)
		}
		if probe.needsAdmin {
			return s.promoteObservationNeedsAdmin(record.issue.ID, now, probe.reason)
		}
		return s.promoteObservedIssue(record.issue.ID, now)
	}
	return nil
}

func (s *Service) applyAbsentObservation(record *observationRecord, now time.Time, settings Settings) error {
	// A promoted incident that already completed its absence settle must not
	// restart settling on every subsequent empty snapshot. Keep it actionable,
	// while continuing to enforce exact library/import evidence so a late arr
	// import still invalidates any stale agent/admin work.
	if record.promotedAt.Valid && record.state == observationStateSettling && !record.settlingSince.Valid {
		probe, err := s.exactRecoveryGuard(record.issue)
		if err != nil {
			_, moveErr := s.moveIssueToObservationNeedsAdmin(record.issue, observationNeedsCloserLook, now)
			return moveErr
		}
		if probe.completed {
			return s.closeObservedRecovery(record.issue.ID, true)
		}
		if probe.needsAdmin {
			_, err = s.moveIssueToObservationNeedsAdmin(record.issue, probe.reason, now)
			return err
		}
		return nil
	}
	settlingSince := record.settlingSince
	if record.state != observationStateSettling || !settlingSince.Valid {
		settlingSince = sql.NullTime{Time: now, Valid: true}
		if _, err := s.db.Exec(
			`UPDATE issue_observations SET state = ?, settling_since = ?,
			 last_activity_at = ?, updated_at = ? WHERE issue_id = ?`,
			observationStateSettling, now, now, now, record.issue.ID,
		); err != nil {
			return err
		}
		_ = s.recordObservationAttempt(record.issue.ID, observationStateSettling, record.signature,
			arr.QueueObservation{}, "exact scope absent from complete queue; settling", now)
		_, err := s.suspendIssueForRecovery(record.issue.ID, arr.QueueObservation{}, now)
		return err
	}
	if now.Sub(settlingSince.Time) < time.Duration(settings.ObservationSettleMinutes)*time.Minute {
		return nil
	}
	if now.Sub(record.firstSeen) < time.Duration(settings.ObservationMinMinutes)*time.Minute {
		return nil
	}
	probe, err := s.exactRecoveryGuard(record.issue)
	if err != nil {
		if now.Sub(settlingSince.Time) >= time.Duration(settings.ObservationMinMinutes)*time.Minute {
			return s.promoteObservationNeedsAdmin(record.issue.ID, now, observationNeedsCloserLook)
		}
		return nil
	}
	if probe.completed {
		return s.closeObservedRecovery(record.issue.ID, record.promotedAt.Valid)
	}
	if probe.needsAdmin {
		return s.promoteObservationNeedsAdmin(record.issue.ID, now, probe.reason)
	}
	return s.promoteObservedIssue(record.issue.ID, now)
}

func (s *Service) promoteObservationNeedsAdmin(issueID int64, now time.Time, reason string) error {
	cutoff := now.Add(-time.Duration(s.Settings().ObservationMinMinutes) * time.Minute)
	res, err := s.db.Exec(
		`UPDATE issues SET status=?,read=0,active_run_id=NULL,resolution=?,
		 resolution_kind='',updated_at=?
		 WHERE id=? AND closed_at IS NULL AND status IN (?,?)
		   AND EXISTS (SELECT 1 FROM issue_observations o WHERE o.issue_id=issues.id AND o.first_seen_at<=?)`,
		IssueNeedsAdmin, reason, now, issueID, IssueObserving, IssueRecovering, cutoff,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil
	}
	_, _ = s.db.Exec(
		"UPDATE issue_observations SET promoted_at=COALESCE(promoted_at,?),updated_at=? WHERE issue_id=?",
		now, now, issueID,
	)
	var title, source, status string
	var closed sql.NullTime
	if err := s.db.QueryRow("SELECT title,source,status,closed_at FROM issues WHERE id=?", issueID).Scan(&title, &source, &status, &closed); err == nil && !closed.Valid && status == IssueNeedsAdmin {
		s.notifyIssueCreatedWithSource(issueID, title, source)
	}
	return nil
}

func (s *Service) promoteObservedIssue(issueID int64, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var source, title string
	var promoted sql.NullTime
	if err := tx.QueryRow(
		`SELECT i.source, i.title, o.promoted_at
		 FROM issues i JOIN issue_observations o ON o.issue_id=i.id
		 WHERE i.id=? AND i.closed_at IS NULL`, issueID,
	).Scan(&source, &title, &promoted); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	res, err := tx.Exec(
		`UPDATE issues SET status=?, read=0, updated_at=?
		 WHERE id=? AND closed_at IS NULL AND status IN (?, ?)`,
		IssueOpen, now, issueID, IssueObserving, IssueRecovering,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil
	}
	if _, err := tx.Exec(
		`UPDATE issue_observations SET promoted_at=COALESCE(promoted_at, ?),
		 settling_since=NULL, updated_at=? WHERE issue_id=?`,
		now, now, issueID,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO issue_observation_attempts(issue_id,state,note,created_at)
		 VALUES (?, ?, 'promoted for attention after the observation window', ?)`,
		issueID, observationStateObserving, now,
	); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if !promoted.Valid {
		var current string
		var closed sql.NullTime
		if err := s.db.QueryRow("SELECT status,closed_at FROM issues WHERE id=?", issueID).Scan(&current, &closed); err == nil && !closed.Valid && current == IssueOpen {
			s.notifyIssueCreatedWithSource(issueID, title, source)
		}
	} else {
		s.pingIssueUpdated(issueID)
	}
	if s.Settings().Enabled {
		s.Enqueue(issueID)
	}
	return nil
}

func (s *Service) suspendIssueForRecovery(issueID int64, item arr.QueueObservation, now time.Time) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var status string
	var promoted sql.NullTime
	var hazardous int
	if err := tx.QueryRow(
		`SELECT i.status, o.promoted_at,
		 EXISTS(SELECT 1 FROM agent_actions a WHERE a.issue_id=i.id AND a.status IN (?, ?))
		 FROM issues i JOIN issue_observations o ON o.issue_id=i.id
		 WHERE i.id=? AND i.closed_at IS NULL`,
		ActionExecuting, ActionOutcomeUnknown, issueID,
	).Scan(&status, &promoted, &hazardous); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if hazardous != 0 || status == IssueNeedsAdmin {
		return false, nil
	}
	switch status {
	case IssueObserving, IssueRecovering, IssueOpen, IssueInvestigating, IssueAwaitingUser, IssueAwaitingApproval:
	default:
		return false, nil
	}
	actionRes, err := tx.Exec(
		`UPDATE agent_actions SET status=?, decided_at=COALESCE(decided_at, ?),
		 result_text=COALESCE(result_text, ?) WHERE issue_id=? AND status=?`,
		ActionSuperseded, now, recoveryInFlightResult, issueID, ActionProposed,
	)
	if err != nil {
		return false, err
	}
	superseded, _ := actionRes.RowsAffected()
	if _, err := tx.Exec(
		`UPDATE agent_runs SET status='aborted', stop_reason='arr_recovery_in_flight',
		 deadline_at=NULL, finished_at=COALESCE(finished_at, ?)
		 WHERE issue_id=? AND status IN ('running','waiting_user','waiting_approval','resume_pending')`,
		now, issueID,
	); err != nil {
		return false, err
	}
	res, err := tx.Exec(
		`UPDATE issues SET status=?, read=1, active_run_id=NULL,
		 download_id=CASE WHEN ?!='' THEN ? ELSE download_id END,
		 arr_queue_id=CASE WHEN ?>0 THEN ? ELSE arr_queue_id END,
		 resolution='', resolution_kind='', updated_at=?
		 WHERE id=? AND closed_at IS NULL`,
		IssueRecovering, item.DownloadID, item.DownloadID,
		item.Media.QueueID, item.Media.QueueID, now, issueID,
	)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	wasVisible := promoted.Valid || status != IssueObserving && status != IssueRecovering
	if superseded > 0 {
		s.notifyActionsChanged(issueID, ActionSuperseded)
	} else if wasVisible {
		s.pingIssueUpdated(issueID)
	}
	return true, nil
}

func (s *Service) closeObservedRecovery(issueID int64, wasPromoted bool) error {
	transitioned, err := s.concludeIssueAggregate(context.Background(), issueID, IssueResolved,
		arrStateClearedResolution,
		ResolutionArrStateCleared, issueClosureOptions{silentNotifications: !wasPromoted})
	// Silent means no alert/push, not no cache invalidation. `issue_updated` is a
	// websocket-only refresh signal in the push notifier, so the Tracking list and
	// an open thread learn that a never-promoted incident quietly resolved.
	if err == nil && transitioned && !wasPromoted {
		s.pingIssueUpdated(issueID)
	}
	return err
}

func (s *Service) recordObservationAttempt(issueID int64, state, signature string, item arr.QueueObservation, note string, now time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO issue_observation_attempts
		 (issue_id,state,signature,download_id,arr_queue_id,note,created_at)
		 VALUES (?,?,?,?,?,?,?)`,
		issueID, state, signature, item.DownloadID, sqlNullInt(item.Media.QueueID), note, now,
	)
	return err
}

func (s *Service) recordObservationDownloads(issueID int64, items []arr.QueueObservation, now time.Time) error {
	for _, item := range items {
		if item.DownloadID == "" {
			continue
		}
		if _, err := s.db.Exec(
			`INSERT OR IGNORE INTO issue_observation_downloads(issue_id,download_id,first_seen_at)
			 VALUES (?,?,?)`, issueID, item.DownloadID, now,
		); err != nil {
			return err
		}
	}
	return nil
}

func nullTime(value sql.NullTime) any {
	if !value.Valid {
		return nil
	}
	return value.Time
}

type exactFileState struct {
	hasFile bool
	fileID  int64
	mediaID int
	known   bool
}

func (s *Service) exactIssueFileState(issue *Issue) (exactFileState, error) {
	if s.registry == nil || issue == nil || issue.InstanceID == "" {
		return exactFileState{}, nil
	}
	switch issue.MediaType {
	case "movie":
		if issue.TmdbID <= 0 {
			return exactFileState{}, nil
		}
		client, err := s.registry.GetRadarrClient(issue.InstanceID)
		if err != nil {
			return exactFileState{}, err
		}
		movie, err := client.GetMovieByTMDB(issue.TmdbID)
		if err != nil {
			return exactFileState{}, err
		}
		if movie == nil {
			return exactFileState{known: true}, nil
		}
		fileID := movie.MovieFileID
		if fileID == 0 {
			fileID = movie.MovieFile.ID
		}
		return exactFileState{hasFile: movie.HasFile || fileID > 0, fileID: int64(fileID), mediaID: movie.ID, known: true}, nil
	case "tv":
		if issue.TvdbID <= 0 || issue.SeasonNumber < 0 || issue.EpisodeNumber <= 0 {
			return exactFileState{}, nil
		}
		client, err := s.registry.GetSonarrClient(issue.InstanceID)
		if err != nil {
			return exactFileState{}, err
		}
		series, err := client.GetSeriesByTVDB(issue.TvdbID)
		if err != nil {
			return exactFileState{}, err
		}
		if series == nil {
			return exactFileState{known: true}, nil
		}
		episodes, err := client.GetEpisodes(series.ID, issue.SeasonNumber)
		if err != nil {
			return exactFileState{}, err
		}
		for _, episode := range episodes {
			if episode.SeasonNumber == issue.SeasonNumber && episode.EpisodeNumber == issue.EpisodeNumber {
				return exactFileState{hasFile: episode.HasFile || episode.EpisodeFileID > 0, fileID: int64(episode.EpisodeFileID), mediaID: episode.ID, known: true}, nil
			}
		}
		return exactFileState{known: true}, nil
	default:
		return exactFileState{}, nil
	}
}

func (s *Service) exactIssueHasFile(issue *Issue) (hasFile, known bool, err error) {
	state, err := s.exactIssueFileState(issue)
	return state.hasFile, state.known, err
}

func (s *Service) captureObservationBaseline(issue *Issue) error {
	if issue == nil {
		return nil
	}
	var captured int
	if err := s.db.QueryRow("SELECT baseline_captured_at IS NOT NULL FROM issue_observations WHERE issue_id=?", issue.ID).Scan(&captured); err != nil || captured != 0 {
		return err
	}
	state, err := s.exactIssueFileState(issue)
	if err != nil || !state.known {
		return err
	}
	_, err = s.db.Exec(
		`UPDATE issue_observations SET baseline_has_file=?,baseline_file_id=?,
		 baseline_captured_at=CURRENT_TIMESTAMP
		 WHERE issue_id=? AND baseline_captured_at IS NULL`,
		state.hasFile, sqlNullInt64(state.fileID), issue.ID,
	)
	return err
}

func (s *Service) exactRecoveryProven(issue *Issue) (proven, known bool, err error) {
	var baselineHasFile sql.NullBool
	var baselineFileID sql.NullInt64
	var captured sql.NullTime
	var firstSeen time.Time
	var receiptHistoryID, receiptFileID sql.NullInt64
	var receiptDownloadID sql.NullString
	var provenAt sql.NullTime
	if err := s.db.QueryRow(
		`SELECT baseline_has_file,baseline_file_id,baseline_captured_at,first_seen_at,
		 import_history_id,import_download_id,import_file_id,recovery_proven_at
		 FROM issue_observations WHERE issue_id=?`,
		issue.ID,
	).Scan(&baselineHasFile, &baselineFileID, &captured, &firstSeen,
		&receiptHistoryID, &receiptDownloadID, &receiptFileID, &provenAt); err != nil {
		return false, false, err
	}
	if !captured.Valid || !baselineHasFile.Valid {
		return false, false, nil
	}
	if baselineHasFile.Bool && (!baselineFileID.Valid || baselineFileID.Int64 <= 0) {
		return false, false, nil
	}
	current, err := s.exactIssueFileState(issue)
	if err != nil || !current.known {
		return false, false, err
	}
	if current.hasFile && current.fileID <= 0 {
		return false, false, nil
	}
	if provenAt.Valid && receiptHistoryID.Valid && receiptHistoryID.Int64 > 0 &&
		receiptDownloadID.Valid && receiptDownloadID.String != "" && receiptFileID.Valid && receiptFileID.Int64 > 0 {
		return current.hasFile && current.fileID == receiptFileID.Int64, true, nil
	}
	transitioned := !baselineHasFile.Bool && current.hasFile
	if baselineHasFile.Bool && current.hasFile && baselineFileID.Valid && baselineFileID.Int64 > 0 && current.fileID > 0 && current.fileID != baselineFileID.Int64 {
		transitioned = true
	}
	if !transitioned || current.fileID <= 0 {
		return false, true, nil
	}
	receipt, err := s.exactImportWitness(issue, current.fileID, current.mediaID, firstSeen)
	if err != nil || receipt.historyID == 0 {
		return false, true, err
	}
	_, err = s.db.Exec(
		`UPDATE issue_observations SET import_history_id=?,import_download_id=?,
		 import_file_id=?,recovery_proven_at=CURRENT_TIMESTAMP WHERE issue_id=?`,
		receipt.historyID, receipt.downloadID, receipt.fileID, issue.ID,
	)
	return err == nil, true, err
}

func (s *Service) baselineFileStateChanged(issue *Issue) (changed, known bool, err error) {
	var baselineHasFile sql.NullBool
	var baselineFileID sql.NullInt64
	var captured sql.NullTime
	if err := s.db.QueryRow(
		"SELECT baseline_has_file,baseline_file_id,baseline_captured_at FROM issue_observations WHERE issue_id=?",
		issue.ID,
	).Scan(&baselineHasFile, &baselineFileID, &captured); err != nil {
		return false, false, err
	}
	if !captured.Valid || !baselineHasFile.Valid {
		return false, false, nil
	}
	if baselineHasFile.Bool && (!baselineFileID.Valid || baselineFileID.Int64 <= 0) {
		return false, false, nil
	}
	current, err := s.exactIssueFileState(issue)
	if err != nil || !current.known {
		return false, false, err
	}
	if current.hasFile && current.fileID <= 0 {
		return false, false, nil
	}
	if baselineHasFile.Bool != current.hasFile {
		return true, true, nil
	}
	if baselineHasFile.Bool && current.hasFile && baselineFileID.Valid && baselineFileID.Int64 > 0 &&
		current.fileID > 0 && baselineFileID.Int64 != current.fileID {
		return true, true, nil
	}
	return false, true, nil
}

type importReceipt struct {
	historyID  int64
	downloadID string
	fileID     int64
}

func (s *Service) exactImportWitness(issue *Issue, currentFileID int64, internalMediaID int, firstSeen time.Time) (importReceipt, error) {
	downloadIDs := map[string]string{}
	if issue.DownloadID != "" {
		downloadIDs[strings.ToLower(issue.DownloadID)] = issue.DownloadID
	}
	remaining := 20 - len(downloadIDs)
	rows, err := s.db.Query(
		"SELECT download_id FROM issue_observation_downloads WHERE issue_id=? ORDER BY first_seen_at DESC,download_id LIMIT ?",
		issue.ID, remaining,
	)
	if err != nil {
		return importReceipt{}, err
	}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			downloadIDs[strings.ToLower(id)] = id
		}
	}
	rows.Close()
	if len(downloadIDs) == 0 {
		return importReceipt{}, nil
	}
	fileIDMatches := func(data map[string]string) bool {
		for key, value := range data {
			if strings.EqualFold(key, "fileId") {
				id, _ := strconv.ParseInt(value, 10, 64)
				return id == currentFileID
			}
		}
		return false
	}
	switch issue.MediaType {
	case "movie":
		client, err := s.registry.GetRadarrClient(issue.InstanceID)
		if err != nil {
			return importReceipt{}, err
		}
		for _, downloadID := range downloadIDs {
			history, err := client.GetImportHistory(internalMediaID, downloadID, 20)
			if err != nil {
				return importReceipt{}, err
			}
			for _, record := range history {
				if !strings.EqualFold(record.EventType, "downloadFolderImported") || record.Date.Before(firstSeen.Truncate(time.Second)) ||
					!strings.EqualFold(record.DownloadID, downloadID) || !fileIDMatches(record.Data) ||
					record.MovieID != internalMediaID || record.ID <= 0 {
					continue
				}
				if record.Movie != nil && (record.Movie.ID != internalMediaID || record.Movie.TmdbID != issue.TmdbID) {
					continue
				}
				return importReceipt{historyID: record.ID, downloadID: record.DownloadID, fileID: currentFileID}, nil
			}
		}
	case "tv":
		client, err := s.registry.GetSonarrClient(issue.InstanceID)
		if err != nil {
			return importReceipt{}, err
		}
		for _, downloadID := range downloadIDs {
			history, err := client.GetImportHistory(internalMediaID, downloadID, 20)
			if err != nil {
				return importReceipt{}, err
			}
			for _, record := range history {
				if !strings.EqualFold(record.EventType, "downloadFolderImported") || record.Date.Before(firstSeen.Truncate(time.Second)) ||
					!strings.EqualFold(record.DownloadID, downloadID) || !fileIDMatches(record.Data) ||
					record.EpisodeID != internalMediaID || record.ID <= 0 {
					continue
				}
				if record.Series != nil && record.Series.TvdbID != issue.TvdbID {
					continue
				}
				if record.Episode != nil && (record.Episode.ID != internalMediaID ||
					record.Episode.SeasonNumber != issue.SeasonNumber || record.Episode.EpisodeNumber != issue.EpisodeNumber) {
					continue
				}
				return importReceipt{historyID: record.ID, downloadID: record.DownloadID, fileID: currentFileID}, nil
			}
		}
	}
	return importReceipt{}, nil
}

// recentQueueObservationForUser uses only a recent successful complete snapshot
// and never performs network I/O on the report request path.
func (s *Service) recentQueueObservationForUser(instanceID, mediaType string, media arr.QueueMediaContext, now time.Time) (observationGroup, bool) {
	var serviceType string
	var observedAt time.Time
	var encoded string
	err := s.db.QueryRow(
		`SELECT service_type, observed_at, items_json FROM remediation_queue_snapshots
		 WHERE instance_id=?`, instanceID,
	).Scan(&serviceType, &observedAt, &encoded)
	if err != nil || serviceType != mediaServiceType(mediaType) || now.Sub(observedAt) > queueSnapshotFreshness || observedAt.After(now.Add(time.Second)) {
		return observationGroup{}, false
	}
	var items []arr.QueueObservation
	if json.Unmarshal([]byte(encoded), &items) != nil {
		return observationGroup{}, false
	}
	var matched []arr.QueueObservation
	for _, item := range items {
		if mediaScopeMatches(media, item.Media, mediaType) {
			matched = append(matched, item)
		}
	}
	if len(matched) == 0 {
		return observationGroup{}, false
	}
	return observationGroup{scopeKey: userIncidentScopeKey(instanceID, mediaType, media), items: matched}, true
}

// StartObservationSweeper gives durable observation its own restart path. It
// immediately refreshes every instance with a live observed incident and then
// repeats periodically. A failed read freezes state and is never reconciled as
// an empty queue.
func (a *AutoDispatcher) StartObservationSweeper(ctx context.Context) {
	if a == nil || a.svc == nil {
		return
	}
	a.startOnce.Do(func() {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-a.snapshotWake:
					for {
						a.snapshotMu.Lock()
						jobs := make([]queueSnapshotJob, 0, len(a.pendingSnapshots))
						for key, pending := range a.pendingSnapshots {
							jobs = append(jobs, pending...)
							delete(a.pendingSnapshots, key)
						}
						a.snapshotMu.Unlock()
						if len(jobs) == 0 {
							break
						}
						for _, job := range jobs {
							if job.failure != nil {
								a.svc.noteObservationFailure(job.serviceType, job.instanceID, job.failure, job.observedAt)
								continue
							}
							if err := a.svc.observeQueueSnapshot(job.serviceType, job.instanceID, job.items, job.observedAt); err != nil {
								log.Printf("remediation: observe %s queue for %s: %v", job.serviceType, job.instanceID, err)
							}
						}
					}
				}
			}
		}()
		go func() {
			a.sweepObservedInstances()
			ticker := time.NewTicker(observationSweepPeriod)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					a.sweepObservedInstances()
				}
			}
		}()
	})
}

func (a *AutoDispatcher) sweepObservedInstances() {
	rows, err := a.svc.db.Query(
		`SELECT DISTINCT o.service_type, i.instance_id
		 FROM issue_observations o JOIN issues i ON i.id=o.issue_id
		 WHERE i.closed_at IS NULL AND i.instance_id IS NOT NULL`,
	)
	if err != nil {
		return
	}
	type target struct{ serviceType, instanceID string }
	var targets []target
	for rows.Next() {
		var target target
		if rows.Scan(&target.serviceType, &target.instanceID) == nil {
			targets = append(targets, target)
		}
	}
	rows.Close()
	for _, target := range targets {
		observedAt := time.Now().UTC()
		if a.now != nil {
			observedAt = a.now().UTC()
		}
		items, err := a.svc.fetchQueueSnapshot(target.serviceType, target.instanceID)
		if err != nil {
			log.Printf("remediation: observation sweep %s %s: %v", target.serviceType, target.instanceID, err)
			a.enqueueSnapshotJob(queueSnapshotJob{
				serviceType: target.serviceType, instanceID: target.instanceID,
				failure: err, observedAt: observedAt,
			})
			continue
		}
		a.enqueueSnapshotJob(queueSnapshotJob{
			serviceType: target.serviceType, instanceID: target.instanceID,
			items: items, observedAt: observedAt,
		})
	}
}

func (s *Service) fetchQueueSnapshot(serviceType, instanceID string) ([]arr.QueueObservation, error) {
	if s.registry == nil {
		return nil, fmt.Errorf("instance registry unavailable")
	}
	switch serviceType {
	case "radarr":
		client, err := s.registry.GetRadarrClient(instanceID)
		if err != nil {
			return nil, err
		}
		items, err := client.GetQueueDetailed()
		if err != nil {
			return nil, err
		}
		out := make([]arr.QueueObservation, 0, len(items))
		for _, item := range items {
			out = append(out, radarrObservation(item))
		}
		return out, nil
	case "sonarr":
		client, err := s.registry.GetSonarrClient(instanceID)
		if err != nil {
			return nil, err
		}
		items, err := client.GetQueueDetailed()
		if err != nil {
			return nil, err
		}
		out := make([]arr.QueueObservation, 0, len(items))
		for _, item := range items {
			out = append(out, sonarrObservation(item))
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported queue service %q", serviceType)
	}
}

type arrRecoveryProbe struct {
	active     bool
	completed  bool
	needsAdmin bool
	reason     string
	item       arr.QueueObservation
}

// exactRecoveryGuard interprets the exact library baseline and a locally
// validated import receipt. Auto incidents may close only with that proof.
// User reports are subjective and never auto-close; any attributable or
// ambiguous file transition instead parks stale work for administrator review.
// A user report with no baseline is allowed to proceed after the queue settle
// window because it may never have had a matching queue row. Auto incidents are
// born from a queue row, so a missing baseline is an unsafe state.
func (s *Service) exactRecoveryGuard(issue *Issue) (arrRecoveryProbe, error) {
	var captured int
	if err := s.db.QueryRow(
		"SELECT baseline_captured_at IS NOT NULL FROM issue_observations WHERE issue_id=?",
		issue.ID,
	).Scan(&captured); err != nil {
		return arrRecoveryProbe{}, err
	}
	if captured == 0 {
		if issue.Source == SourceAuto {
			return arrRecoveryProbe{needsAdmin: true, reason: observationNeedsCloserLook}, nil
		}
		return arrRecoveryProbe{}, nil
	}

	proven, known, err := s.exactRecoveryProven(issue)
	if err != nil {
		return arrRecoveryProbe{}, err
	}
	if !known {
		return arrRecoveryProbe{needsAdmin: true, reason: observationNeedsCloserLook}, nil
	}
	if proven {
		if issue.Source == SourceAuto {
			return arrRecoveryProbe{completed: true}, nil
		}
		return arrRecoveryProbe{needsAdmin: true, reason: observationNeedsCloserLook}, nil
	}
	changed, stateKnown, err := s.baselineFileStateChanged(issue)
	if err != nil {
		return arrRecoveryProbe{}, err
	}
	if !stateKnown || changed {
		return arrRecoveryProbe{needsAdmin: true, reason: observationNeedsCloserLook}, nil
	}
	return arrRecoveryProbe{}, nil
}

// queueAbsenceSettled advances the synchronous preflight's durable absence
// timer. The first complete no-match snapshot is never permission to mutate:
// arr commonly removes a queue row before its library/history endpoints expose
// the completed import. A previously promoted, fully settled incident keeps its
// cleared marker and does not oscillate back into recovery on every empty poll.
func (s *Service) queueAbsenceSettled(issueID int64, now time.Time) (bool, error) {
	var state string
	var settling, promoted sql.NullTime
	if err := s.db.QueryRow(
		"SELECT state,settling_since,promoted_at FROM issue_observations WHERE issue_id=?",
		issueID,
	).Scan(&state, &settling, &promoted); err != nil {
		return false, err
	}
	if promoted.Valid && state == observationStateSettling && !settling.Valid {
		return true, nil
	}
	if state != observationStateSettling || !settling.Valid {
		if _, err := s.db.Exec(
			`UPDATE issue_observations SET state=?,settling_since=?,last_activity_at=?,updated_at=?
			 WHERE issue_id=?`,
			observationStateSettling, now, now, now, issueID,
		); err != nil {
			return false, err
		}
		_ = s.recordObservationAttempt(issueID, observationStateSettling, "", arr.QueueObservation{},
			"exact scope absent during execution preflight; settling", now)
		return false, nil
	}
	return now.Sub(settling.Time) >= time.Duration(s.Settings().ObservationSettleMinutes)*time.Minute, nil
}

func (s *Service) probeArrRecovery(issue *Issue) (arrRecoveryProbe, error) {
	if s.recoveryProbe != nil {
		return s.recoveryProbe(issue)
	}
	if s.registry == nil {
		return arrRecoveryProbe{}, nil
	}
	serviceType := mediaServiceType(issue.MediaType)
	if serviceType != "radarr" && serviceType != "sonarr" {
		return arrRecoveryProbe{}, nil
	}
	now := time.Now().UTC() // request-start ordering; delayed reads remain stale.
	items, err := s.fetchQueueSnapshot(serviceType, issue.InstanceID)
	if err != nil {
		return arrRecoveryProbe{}, err
	}
	s.observationMu.Lock()
	defer s.observationMu.Unlock()
	if err := s.storeQueueSnapshot(serviceType, issue.InstanceID, items, now); err != nil {
		return arrRecoveryProbe{}, err
	}
	if _, err := s.ensureIssueObservation(issue, serviceType, now); err != nil {
		return arrRecoveryProbe{}, err
	}
	var matched []arr.QueueObservation
	for _, item := range items {
		if mediaScopeMatches(issueMediaContext(issue), item.Media, issue.MediaType) ||
			(item.DownloadID != "" && item.DownloadID == issue.DownloadID) {
			matched = append(matched, item)
		}
	}
	if len(matched) == 0 {
		settled, err := s.queueAbsenceSettled(issue.ID, now)
		if err != nil {
			return arrRecoveryProbe{}, err
		}
		if !settled {
			return arrRecoveryProbe{active: true}, nil
		}
		return s.exactRecoveryGuard(issue)
	}

	// This is a fresh, complete, live queue match (never the report path's cache),
	// so it is the only safe moment to retry baseline capture for either source.
	if err := s.captureObservationBaseline(issue); err != nil {
		return arrRecoveryProbe{}, err
	}
	if _, err := s.db.Exec(
		`UPDATE issue_observations SET settling_since=NULL,
		 state=CASE WHEN state=? THEN ? ELSE state END,updated_at=? WHERE issue_id=?`,
		observationStateSettling, observationStateObserving, now, issue.ID,
	); err != nil {
		return arrRecoveryProbe{}, err
	}
	guard, err := s.exactRecoveryGuard(issue)
	if err != nil || guard.completed || guard.needsAdmin {
		return guard, err
	}
	group := observationGroup{items: matched}
	active, err := s.matchingGroupStillActive(issue, group, now)
	if err != nil {
		return arrRecoveryProbe{}, err
	}
	if active {
		return arrRecoveryProbe{active: true, item: selectObservation(group, issue.DownloadID)}, nil
	}
	return arrRecoveryProbe{}, nil
}

func (s *Service) matchingGroupStillActive(issue *Issue, group observationGroup, now time.Time) (bool, error) {
	var state, signature string
	var firstSeen, lastActivity time.Time
	var promoted sql.NullTime
	err := s.db.QueryRow(
		`SELECT state,signature,first_seen_at,last_activity_at,promoted_at
		 FROM issue_observations WHERE issue_id=?`, issue.ID,
	).Scan(&state, &signature, &firstSeen, &lastActivity, &promoted)
	if err != nil {
		return true, err
	}
	wantState := observationStateObserving
	if groupIsRecovery(group, issue.DownloadID) {
		wantState = observationStateRecovering
	}
	wantSignature := observationSignature(group.items)
	if state != wantState || signature != wantSignature {
		if _, err := s.db.Exec(
			`UPDATE issue_observations SET state=?,signature=?,last_seen_at=?,last_activity_at=?,
			 settling_since=NULL,updated_at=? WHERE issue_id=?`,
			wantState, wantSignature, now, now, now, issue.ID,
		); err != nil {
			return true, err
		}
		_ = s.recordObservationAttempt(issue.ID, wantState, wantSignature,
			selectObservation(group, issue.DownloadID), "live arr signature changed during execution preflight", now)
		return true, nil
	}
	if !promoted.Valid {
		return true, nil
	}
	settings := s.Settings()
	quiet := now.Sub(lastActivity) >= time.Duration(settings.ObservationQuietMinutes)*time.Minute
	if wantState == observationStateRecovering {
		quiet = quiet && now.Sub(firstSeen) >= time.Duration(settings.ObservationMinMinutes)*time.Minute
	}
	return !quiet, nil
}

// preflightArrRecovery performs the synchronous safety read used immediately
// before an approval claim (and before a new runner starts).
func (s *Service) preflightArrRecovery(issueID int64) (bool, error) {
	if s.registry == nil && s.recoveryProbe == nil {
		return false, nil // test/unwired service; production always supplies the registry.
	}
	issue, err := s.GetIssue(issueID)
	if err != nil {
		return false, err
	}
	serviceType := mediaServiceType(issue.MediaType)
	probe, err := s.probeArrRecovery(issue)
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	if !probe.active && !probe.completed {
		if probe.needsAdmin {
			return s.moveIssueToObservationNeedsAdmin(issue, probe.reason, now)
		}
		return false, nil
	}
	if _, err := s.ensureIssueObservation(issue, serviceType, now); err != nil {
		return false, err
	}
	if probe.active {
		transitioned, err := s.suspendIssueForRecovery(issue.ID, probe.item, now)
		return transitioned, err
	}
	var promoted int
	_ = s.db.QueryRow("SELECT promoted_at IS NOT NULL FROM issue_observations WHERE issue_id=?", issue.ID).Scan(&promoted)
	return true, s.closeObservedRecovery(issue.ID, promoted != 0)
}

func (s *Service) moveIssueToObservationNeedsAdmin(issue *Issue, reason string, now time.Time) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var hazardous int
	if err := tx.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM agent_actions WHERE issue_id=? AND status IN (?,?))",
		issue.ID, ActionExecuting, ActionOutcomeUnknown,
	).Scan(&hazardous); err != nil {
		return false, err
	}
	if hazardous != 0 || issue.Status == IssueNeedsAdmin {
		return false, nil
	}
	actions, err := tx.Exec(
		`UPDATE agent_actions SET status=?,decided_at=COALESCE(decided_at,?),
		 result_text=COALESCE(result_text,'Superseded because live media state changed before execution; no fix was executed.')
		 WHERE issue_id=? AND status=?`, ActionSuperseded, now, issue.ID, ActionProposed)
	if err != nil {
		return false, err
	}
	superseded, _ := actions.RowsAffected()
	if _, err := tx.Exec(
		`UPDATE agent_runs SET status='aborted',stop_reason='media_state_changed',
		 deadline_at=NULL,finished_at=COALESCE(finished_at,?)
		 WHERE issue_id=? AND status IN ('running','waiting_user','waiting_approval','resume_pending')`,
		now, issue.ID); err != nil {
		return false, err
	}
	res, err := tx.Exec(
		`UPDATE issues SET status=?,read=0,active_run_id=NULL,resolution=?,resolution_kind='',updated_at=?
		 WHERE id=? AND closed_at IS NULL AND status IN (?,?,?,?,?,?)`,
		IssueNeedsAdmin, reason, now, issue.ID, IssueObserving, IssueRecovering,
		IssueOpen, IssueInvestigating, IssueAwaitingUser, IssueAwaitingApproval)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	if superseded > 0 {
		s.notifyActionsChanged(issue.ID, ActionSuperseded)
	} else {
		s.pingIssueUpdated(issue.ID)
	}
	return true, nil
}

// cancelExecutingForRecovery is the second, post-CAS safety boundary. The
// action has been claimed but Executor has not been called, so cancelling this
// claim is a definitive zero-mutation outcome, not outcome_unknown.
func (s *Service) cancelExecutingForRecovery(act *AgentAction, probe arrRecoveryProbe) error {
	issue, err := s.GetIssue(act.IssueID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err := s.ensureIssueObservation(issue, mediaServiceType(issue.MediaType), now); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(
		`UPDATE agent_actions SET status=?,
		 result_text=? WHERE id=? AND status=?`,
		ActionSuperseded, recoveryInFlightResult, act.ID, ActionExecuting,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrActionDecisionConflict
	}
	if _, err := tx.Exec(
		`UPDATE agent_runs SET status='aborted',stop_reason='arr_recovery_in_flight',
		 deadline_at=NULL,finished_at=COALESCE(finished_at,?)
		 WHERE issue_id=? AND status IN ('running','waiting_user','waiting_approval','resume_pending')`,
		now, act.IssueID,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE issues SET status=?,read=1,active_run_id=NULL,
		 download_id=CASE WHEN ?!='' THEN ? ELSE download_id END,
		 arr_queue_id=CASE WHEN ?>0 THEN ? ELSE arr_queue_id END,
		 resolution='',resolution_kind='',updated_at=?
		 WHERE id=? AND closed_at IS NULL`,
		IssueRecovering, probe.item.DownloadID, probe.item.DownloadID,
		probe.item.Media.QueueID, probe.item.Media.QueueID, now, act.IssueID,
	); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.notifyActionsChanged(act.IssueID, ActionSuperseded)
	if probe.completed {
		return s.closeObservedRecovery(act.IssueID, true)
	}
	return nil
}

func (s *Service) failExecutingRecoveryPreflight(act *AgentAction, cause error) error {
	now := time.Now().UTC()
	result := observationNeedsCloserLook
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(
		`UPDATE agent_actions SET status=?,result_text=?
		 WHERE id=? AND status=?`, ActionFailed, result, act.ID, ActionExecuting,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrActionDecisionConflict
	}
	if _, err := tx.Exec(
		`UPDATE issues SET status=?,read=0,active_run_id=NULL,resolution=?,
		 resolution_kind='',updated_at=? WHERE id=? AND closed_at IS NULL`,
		IssueNeedsAdmin, result, now, act.IssueID,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE agent_runs SET status='aborted',stop_reason='recovery_preflight_failed',
		 deadline_at=NULL,finished_at=COALESCE(finished_at,?)
		 WHERE issue_id=? AND status IN ('running','waiting_user','waiting_approval','resume_pending')`,
		now, act.IssueID,
	); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.notifyActionsChanged(act.IssueID, ActionFailed)
	return nil
}

func (s *Service) cancelExecutingForObservationReview(act *AgentAction, reason string) error {
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(
		`UPDATE agent_actions SET status=?,result_text=? WHERE id=? AND status=?`,
		ActionSuperseded, "Superseded because live media state changed before execution; no fix was executed.", act.ID, ActionExecuting)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrActionDecisionConflict
	}
	if _, err := tx.Exec(
		`UPDATE agent_runs SET status='aborted',stop_reason='media_state_changed',deadline_at=NULL,
		 finished_at=COALESCE(finished_at,?) WHERE issue_id=?
		 AND status IN ('running','waiting_user','waiting_approval','resume_pending')`, now, act.IssueID); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE issues SET status=?,read=0,active_run_id=NULL,resolution=?,resolution_kind='',updated_at=?
		 WHERE id=? AND closed_at IS NULL`, IssueNeedsAdmin, reason, now, act.IssueID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.notifyActionsChanged(act.IssueID, ActionSuperseded)
	return nil
}

func radarrObservation(item radarr.DetailedQueueItem) arr.QueueObservation {
	messages := make([]arr.StatusMessage, 0, len(item.StatusMessages))
	for _, message := range item.StatusMessages {
		messages = append(messages, arr.StatusMessage{Title: message.Title, Messages: message.Messages})
	}
	media := arr.QueueMediaContext{QueueID: item.ID, Title: item.Title}
	if item.Movie != nil {
		media.Title = item.Movie.Title
		media.TmdbID = item.Movie.TmdbID
	}
	signal := arr.QueueSignal{
		Status: item.Status, TrackedDownloadStatus: item.TrackedDownloadStatus,
		TrackedDownloadState: item.TrackedDownloadState, ErrorMessage: item.ErrorMessage,
		StatusMessages: messages, Protocol: item.Protocol, Size: item.Size, SizeLeft: item.Sizeleft,
	}
	return arr.QueueObservation{DownloadID: item.DownloadID, Media: media, Signal: signal, Diagnosis: arr.Diagnose(signal)}
}

func sonarrObservation(item sonarr.DetailedQueueItem) arr.QueueObservation {
	messages := make([]arr.StatusMessage, 0, len(item.StatusMessages))
	for _, message := range item.StatusMessages {
		messages = append(messages, arr.StatusMessage{Title: message.Title, Messages: message.Messages})
	}
	media := arr.QueueMediaContext{QueueID: item.ID, Title: item.Title}
	if item.Series != nil {
		media.Title = item.Series.Title
		media.TmdbID = item.Series.TmdbID
		media.TvdbID = item.Series.TvdbID
	}
	if item.Episode != nil {
		media.SeasonNumber = item.Episode.SeasonNumber
		media.EpisodeNumber = item.Episode.EpisodeNumber
	}
	signal := arr.QueueSignal{
		Status: item.Status, TrackedDownloadStatus: item.TrackedDownloadStatus,
		TrackedDownloadState: item.TrackedDownloadState, ErrorMessage: item.ErrorMessage,
		StatusMessages: messages, Protocol: item.Protocol, Size: item.Size, SizeLeft: item.Sizeleft,
	}
	return arr.QueueObservation{DownloadID: item.DownloadID, Media: media, Signal: signal, Diagnosis: arr.Diagnose(signal)}
}
