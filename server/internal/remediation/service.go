package remediation

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

const (
	maxIssueTitleBytes  = 512
	maxIssueDetailBytes = 8192
	maxIssueReplyBytes  = 8192
)

// Notifier delivers realtime events about issues. The websocket hub (and the
// push composite) satisfy this; it is optional and may be nil. Same shape as
// request.Notifier so the existing fan-out is reused verbatim.
type Notifier interface {
	NotifyUser(userID int64, eventType string, data map[string]interface{})
	NotifyAdmins(eventType string, data map[string]interface{})
}

// Service records, threads, lists, and dismisses issues, and owns the global
// remediation settings. It mirrors request.Service (db + registry + bridge +
// notifier). No AI agent is wired in Wave 1: OpenAutoIssue exists and dedupes
// correctly, but nothing calls it until auto-dispatch lands.
type Service struct {
	db       *sql.DB
	registry *instance.Registry
	bridge   *tmdb.Bridge
	notifier Notifier

	// executor replays an approved proposal against the arr. It is the ONLY code
	// that mutates Radarr/Sonarr, reached solely from ApproveAction. It is an
	// interface so a test can inject a fake seam (no network), satisfied in
	// production by *Executor.
	executor actionExecutor

	// jobs is the buffered queue of investigation/resume jobs. The Runner drains
	// it via StartWorkers; Enqueue/EnqueueResume push onto it. Buffered so the
	// request and approval paths never block on the agent.
	jobs chan job
}

// actionExecutor is the narrow seam ApproveAction calls to replay an approved
// action against the arr. *Executor satisfies it in production; a fake satisfies
// it in tests so the approve→execute→resume cycle can be asserted without a
// network or a live arr.
type actionExecutor interface {
	Execute(ctx context.Context, issueID int64, kind ActionKind, params json.RawMessage) (string, error)
}

// NewService constructs the remediation service, mirroring request.NewService.
func NewService(db *sql.DB, registry *instance.Registry, bridge *tmdb.Bridge, notifier Notifier) *Service {
	return &Service{
		db:       db,
		registry: registry,
		bridge:   bridge,
		notifier: notifier,
		executor: NewExecutor(registry, bridge, db),
		jobs:     make(chan job, jobQueueSize),
	}
}

// validCategory reports whether a user-selected category is one of the known
// values.
func validCategory(c string) bool {
	switch c {
	case CategoryWrongContent, CategoryBadCopy, CategoryWrongAudio, CategoryOther:
		return true
	}
	return false
}

// CreateUserIssue records a user-reported problem. It validates the media type
// and category, dedupes a duplicate open report from the same reporter+scope+
// category (bumping occurrences rather than inserting a second row), inserts
// otherwise, notifies admins, and returns the issue id + status.
//
// All free text (Reason, Title) is UNTRUSTED and stored verbatim. When tmdb_id
// is 0 but tvdb_id is set, a best-effort reverse lookup of the cached
// tmdb<->tvdb mapping is attempted; otherwise the ids are stored as given.
func (s *Service) CreateUserIssue(reporterID int64, req *CreateIssueRequest) (*CreateIssueResponse, error) {
	instanceID := strings.TrimSpace(req.InstanceID)
	if instanceID == "" {
		return nil, fmt.Errorf("instance_id is required")
	}
	if req.MediaType != "movie" && req.MediaType != "tv" {
		return nil, fmt.Errorf("unsupported media type: %s", req.MediaType)
	}
	if s.registry == nil {
		return nil, fmt.Errorf("instance registry unavailable")
	}
	var instanceErr error
	if req.MediaType == "movie" {
		_, instanceErr = s.registry.GetRadarrClient(instanceID)
	} else {
		_, instanceErr = s.registry.GetSonarrClient(instanceID)
	}
	if instanceErr != nil {
		return nil, fmt.Errorf("invalid instance_id for %s: %w", req.MediaType, instanceErr)
	}
	if !validCategory(req.Category) {
		return nil, fmt.Errorf("invalid category: %s", req.Category)
	}
	if req.TmdbID < 0 || req.TvdbID < 0 || req.SeasonNumber < 0 || req.EpisodeNumber < 0 {
		return nil, fmt.Errorf("media ids and episode scope must not be negative")
	}
	if req.MediaType == "tv" && req.EpisodeNumber > 0 && req.SeasonNumber == 0 {
		return nil, fmt.Errorf("episode_number requires a positive season_number")
	}
	if len(req.Title) > maxIssueTitleBytes || len(req.Reason) > maxIssueDetailBytes {
		return nil, fmt.Errorf("issue title or detail is too long")
	}

	tmdbID := req.TmdbID
	tvdbID := req.TvdbID
	// Resolve a missing tmdb_id from a known tvdb_id via the cached mapping the
	// ID bridge maintains (request flows populate tmdb_tvdb_cache). There is no
	// live reverse resolver, so this is best-effort: on a miss the ids are stored
	// as given, which the contract permits.
	if tmdbID == 0 && tvdbID != 0 {
		var cached int
		if err := s.db.QueryRow("SELECT tmdb_id FROM tmdb_tvdb_cache WHERE tvdb_id = ?", tvdbID).Scan(&cached); err == nil && cached != 0 {
			tmdbID = cached
		}
	}

	season := req.SeasonNumber
	episode := req.EpisodeNumber
	if req.MediaType == "movie" {
		// Movies have no season/episode scope; keep the stored row clean.
		season = 0
		episode = 0
	}

	// Dedupe a duplicate open report: the same reporter re-reporting the same
	// scope (tmdb + media_type + season + episode) with the same category bumps
	// occurrences instead of opening a second issue. The check + insert is one
	// statement under the single-writer DB, mirroring createPending.
	res, err := s.db.Exec(
		`INSERT INTO issues
			(source, status, category, reporter_id, tmdb_id, tvdb_id, media_type, title, season_number, episode_number, instance_id, detail)
		 SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		 WHERE NOT EXISTS (
			SELECT 1 FROM issues
			WHERE source = ? AND reporter_id = ? AND instance_id = ? AND tmdb_id = ? AND media_type = ?
			  AND season_number = ? AND episode_number = ? AND category = ? AND closed_at IS NULL
		 )`,
		SourceUser, IssueOpen, req.Category, reporterID, tmdbID, sqlNullInt(tvdbID), req.MediaType, req.Title, season, episode, instanceID, req.Reason,
		SourceUser, reporterID, instanceID, tmdbID, req.MediaType, season, episode, req.Category,
	)
	if err != nil {
		return nil, fmt.Errorf("create issue: %w", err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		// Duplicate open report: bump occurrences + refresh updated_at on the
		// existing open issue, and return it.
		id, status, derr := s.bumpDuplicateUserIssue(reporterID, req, instanceID, tmdbID, season, episode)
		if derr != nil {
			return nil, derr
		}
		return &CreateIssueResponse{IssueID: id, Status: status}, nil
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("create issue: %w", err)
	}

	s.notifyIssueCreated(id, req.Title)
	// Kick off the read-only investigation for a GENUINELY NEW issue (not a
	// duplicate that only bumped occurrences) when the feature is enabled. NO
	// auto-dispatch and NO propose/approve happen here — just the read-only loop.
	if s.Settings().Enabled {
		s.Enqueue(id)
	}
	return &CreateIssueResponse{IssueID: id, Status: IssueOpen}, nil
}

// bumpDuplicateUserIssue increments occurrences AND appends the newly submitted
// reason in one transaction. Keeping every report as an AuthorUser thread event
// prevents an active agent from continuing against only the original detail.
func (s *Service) bumpDuplicateUserIssue(reporterID int64, req *CreateIssueRequest, instanceID string, tmdbID, season, episode int) (int64, string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, "", fmt.Errorf("begin duplicate issue: %w", err)
	}
	defer tx.Rollback()
	var id int64
	var status string
	err = tx.QueryRow(
		`SELECT id, status FROM issues
		 WHERE source = ? AND reporter_id = ? AND instance_id = ? AND tmdb_id = ? AND media_type = ?
		   AND season_number = ? AND episode_number = ? AND category = ? AND closed_at IS NULL
		 ORDER BY id DESC LIMIT 1`,
		SourceUser, reporterID, instanceID, tmdbID, req.MediaType, season, episode, req.Category,
	).Scan(&id, &status)
	if err != nil {
		return 0, "", fmt.Errorf("find duplicate issue: %w", err)
	}
	res, err := tx.Exec(
		`UPDATE issues SET occurrences = occurrences + 1, read = 0, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND closed_at IS NULL`,
		id,
	)
	if err != nil {
		return 0, "", fmt.Errorf("bump duplicate issue: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return 0, "", fmt.Errorf("duplicate issue closed before it could be updated")
	}
	if _, err := tx.Exec(
		`INSERT INTO issue_messages (issue_id, author_kind, author_id, body)
		 VALUES (?, ?, ?, ?)`,
		id, AuthorUser, reporterID, req.Reason,
	); err != nil {
		return 0, "", fmt.Errorf("append duplicate issue reason: %w", err)
	}
	resumeReady := false
	switch status {
	case IssueAwaitingUser:
		resumeReady, err = stageReporterReplyTx(tx, id, req.Reason, true)
	case IssueAwaitingApproval:
		resumeReady, err = stageApprovalThreadUpdateTx(tx, id)
	}
	if err != nil {
		return 0, "", fmt.Errorf("stage duplicate report update: %w", err)
	}
	if (status == IssueAwaitingUser || status == IssueAwaitingApproval) && !resumeReady {
		return 0, "", fmt.Errorf("duplicate report raced an invalid agent gate")
	}
	if resumeReady {
		status = IssueInvestigating
	}
	if err := tx.Commit(); err != nil {
		return 0, "", fmt.Errorf("commit duplicate issue: %w", err)
	}
	if resumeReady {
		s.EnqueueResume(id)
	}
	return id, status, nil
}

// OpenAutoIssue is the poller hook for auto-detected problems (later waves; no
// caller in Wave 1). It conditionally inserts an issue keyed by a stable
// dedupe_key so the same stuck download opens at most one open issue across many
// polls, mirroring createPending. On a duplicate it bumps occurrences and
// returns created=false. A terminal (closed) issue does not block a genuinely
// new recurrence: once closed_at is set the partial unique index no longer
// guards that key, so the same download re-failing later opens a fresh issue.
func (s *Service) OpenAutoIssue(serviceType, instanceID, downloadID string, media arr.QueueMediaContext, d arr.Diagnosis) (created bool, id int64) {
	sum := sha256.Sum256([]byte(instanceID + "|" + downloadID))
	dedupeKey := hex.EncodeToString(sum[:])

	// The poller hands us the *service* type, but media_type on an issues row is
	// the client-facing 'movie'|'tv' contract — storing it raw made clients fall
	// back to a "Movie" label on Sonarr issues.
	mediaType := serviceType
	switch serviceType {
	case "radarr":
		mediaType = "movie"
	case "sonarr":
		mediaType = "tv"
	case "chaptarr":
		mediaType = "book"
	}

	tmdbID := media.TmdbID
	if tmdbID == 0 && media.TvdbID != 0 {
		_ = s.db.QueryRow("SELECT tmdb_id FROM tmdb_tvdb_cache WHERE tvdb_id = ?", media.TvdbID).Scan(&tmdbID)
	}
	title := media.Title
	if title == "" {
		title = d.Problem
	}

	res, err := s.db.Exec(
		`INSERT INTO issues
			(source, status, media_type, tmdb_id, tvdb_id, title, season_number, episode_number,
			 instance_id, download_id, arr_queue_id, detail, dedupe_key)
		 SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		 WHERE NOT EXISTS (
			SELECT 1 FROM issues WHERE dedupe_key = ? AND closed_at IS NULL
		 )`,
		SourceAuto, IssueOpen, mediaType, tmdbID, sqlNullInt(media.TvdbID), title,
		media.SeasonNumber, media.EpisodeNumber, sqlNullStr(instanceID), sqlNullStr(downloadID),
		sqlNullInt(media.QueueID), d.Transparency, dedupeKey,
		dedupeKey,
	)
	if err != nil {
		return false, 0
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Existing open issue for this key: just refresh last-seen, no new issue.
		// Re-detecting the same ongoing problem on every 30s poll is NOT a new
		// occurrence, so we don't bump occurrences (it only inflated a confusing
		// per-poll counter that climbed with time-open, not severity).
		s.db.Exec(
			`UPDATE issues SET title = ?, tmdb_id = CASE WHEN ? > 0 THEN ? ELSE tmdb_id END,
			 tvdb_id = COALESCE(?, tvdb_id), season_number = ?, episode_number = ?,
			 arr_queue_id = COALESCE(?, arr_queue_id), detail = ?, updated_at = CURRENT_TIMESTAMP
			 WHERE dedupe_key = ? AND closed_at IS NULL`,
			title, tmdbID, tmdbID, sqlNullInt(media.TvdbID), media.SeasonNumber,
			media.EpisodeNumber, sqlNullInt(media.QueueID), d.Transparency, dedupeKey,
		)
		return false, 0
	}
	newID, err := res.LastInsertId()
	if err != nil {
		return false, 0
	}
	s.notifyIssueCreated(newID, d.Problem)
	return true, newID
}

const arrStateClearedResolution = "Cantinarr stopped detecting the original stuck or blocked download. Sonarr/Radarr may have recovered it, replaced it, or removed it."

// CloseAutoIssueForDownload resolves the open auto issue for a download whose
// problem the poller no longer detects (it recovered or left the queue). A
// no-op when there's no matching open issue. Idempotent via ConcludeIssue's CAS.
func (s *Service) CloseAutoIssueForDownload(instanceID, downloadID string) {
	if downloadID == "" {
		return
	}
	rows, err := s.db.Query(
		`SELECT id FROM issues
		 WHERE source = ? AND instance_id = ? AND download_id = ? AND closed_at IS NULL
		 ORDER BY id`,
		SourceAuto, instanceID, downloadID,
	)
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, issueID := range ids {
		_ = s.concludeIssue(context.Background(), issueID, IssueResolved,
			arrStateClearedResolution,
			ResolutionArrStateCleared)
	}
}

// ReconcileAutoIssues closes incidents that are absent from a successful full
// problem snapshot. This makes recovery durable across process restarts; a
// failed queue fetch never reaches this method.
func (s *Service) ReconcileAutoIssues(instanceID string, activeDownloadIDs []string) {
	active := make(map[string]bool, len(activeDownloadIDs))
	for _, id := range activeDownloadIDs {
		active[id] = true
	}
	rows, err := s.db.Query(
		`SELECT DISTINCT download_id FROM issues
		 WHERE source = ? AND instance_id = ? AND download_id IS NOT NULL AND closed_at IS NULL`,
		SourceAuto, instanceID,
	)
	if err != nil {
		return
	}
	var stale []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil && !active[id] {
			stale = append(stale, id)
		}
	}
	rows.Close()
	for _, id := range stale {
		s.CloseAutoIssueForDownload(instanceID, id)
	}
}

// notifyIssueCreated fires the issue_created admin notification carrying the
// live open-issue count for badging. The title is passed as a structured field
// (arr/user-sourced) — the push layer builds the human-readable body from fixed
// templates, never by interpolating it.
func (s *Service) notifyIssueCreated(issueID int64, title string) {
	if s.notifier == nil {
		return
	}
	data := map[string]interface{}{
		"issue_id": issueID,
		"title":    title,
	}
	if count, err := s.OpenIssueCount(); err == nil {
		data["open_count"] = count
	}
	s.notifier.NotifyAdmins("issue_created", data)
}

// GetIssue loads one issue row (without its thread).
func (s *Service) GetIssue(issueID int64) (*Issue, error) {
	row := s.db.QueryRow(
		`SELECT i.id, i.source, i.status, i.category, i.reporter_id, u.username,
		        i.tmdb_id, i.tvdb_id, i.media_type, i.title, i.season_number, i.episode_number,
		        i.detail, i.occurrences, i.read, i.resolution, i.resolution_kind,
		        i.created_at, i.updated_at, i.closed_at,
		        i.instance_id, i.download_id, i.arr_queue_id
		 FROM issues i LEFT JOIN users u ON u.id = i.reporter_id
		 WHERE i.id = ?`,
		issueID,
	)
	iss, err := scanIssue(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("issue not found")
	}
	if err != nil {
		return nil, fmt.Errorf("load issue: %w", err)
	}
	return iss, nil
}

// IssueThread returns an issue's append-only message thread, oldest first.
func (s *Service) IssueThread(issueID int64) ([]IssueMessage, error) {
	rows, err := s.db.Query(
		`SELECT m.id, m.author_kind, u.username, m.body, m.created_at
		 FROM issue_messages m LEFT JOIN users u ON u.id = m.author_id
		 WHERE m.issue_id = ? ORDER BY m.id ASC`,
		issueID,
	)
	if err != nil {
		return nil, fmt.Errorf("query thread: %w", err)
	}
	defer rows.Close()

	out := []IssueMessage{}
	for rows.Next() {
		var m IssueMessage
		var name sql.NullString
		if err := rows.Scan(&m.ID, &m.AuthorKind, &name, &m.Body, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		if name.Valid {
			v := name.String
			m.AuthorName = &v
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// PostReply appends a message to an issue's thread. The caller's role decides
// authorKind (the handler passes "admin" or "user"); body is UNTRUSTED and
// stored verbatim. Touching updated_at keeps the issue sorted as recently
// active. On a reporter/admin reply the other party is notified via the WS hub.
//
// W4 resume: when the issue is parked awaiting_user (the agent asked the reporter
// a clarifying question via ask_reporter) and the reply comes from the reporter
// or an admin, the reply is fed back into the parked run as the ask_reporter
// tool_result and a resume is enqueued so the agent continues. A reply from
// anyone else, or on a non-parked issue, only threads the message.
func (s *Service) PostReply(issueID int64, authorKind string, authorID int64, body string) error {
	if len(body) > maxIssueReplyBytes {
		return fmt.Errorf("reply is too long")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin reply: %w", err)
	}
	defer tx.Rollback()

	// Confirm the issue exists (and read its reporter + status for routing).
	var (
		reporterID sql.NullInt64
		status     string
		closedAt   sql.NullTime
	)
	err = tx.QueryRow("SELECT reporter_id, status, closed_at FROM issues WHERE id = ?", issueID).Scan(&reporterID, &status, &closedAt)
	if err == sql.ErrNoRows {
		return fmt.Errorf("issue not found")
	}
	if err != nil {
		return fmt.Errorf("load issue: %w", err)
	}
	if closedAt.Valid {
		return fmt.Errorf("issue is closed")
	}

	if _, err := tx.Exec(
		"INSERT INTO issue_messages (issue_id, author_kind, author_id, body) VALUES (?, ?, ?, ?)",
		issueID, authorKind, sqlNullInt64(authorID), body,
	); err != nil {
		return fmt.Errorf("post reply: %w", err)
	}

	resumeReady := false
	approvalInvalidated := false
	if status == IssueAwaitingUser && (authorKind == AuthorUser || authorKind == AuthorAdmin) {
		// The reply and its transcript handoff are one transaction. A reporter's
		// reply is a non-admin change and re-flags the issue unread; an admin reply
		// preserves the current read state.
		resumeReady, err = stageReporterReplyTx(tx, issueID, body, authorKind == AuthorUser)
		if err != nil {
			_ = tx.Rollback()
			return s.saveUnresumableReply(issueID, authorKind, authorID, body)
		}
		if !resumeReady {
			// Roll back the tentative insert and use the fallback aggregate
			// transition, which saves the reply exactly once and aborts the orphaned
			// waiting run in the same transaction.
			_ = tx.Rollback()
			return s.saveUnresumableReply(issueID, authorKind, authorID, body)
		}
	} else if status == IssueAwaitingApproval && (authorKind == AuthorUser || authorKind == AuthorAdmin) {
		// A reply committed after the Runner parked must invalidate the stale
		// proposal gate in this same transaction. The reply remains in the thread;
		// Resume syncs it as untrusted data before the model can propose again.
		resumeReady, err = stageApprovalThreadUpdateTx(tx, issueID)
		if err != nil || !resumeReady {
			_ = tx.Rollback()
			return s.saveUnresumableApprovalReply(issueID, authorKind, authorID, body)
		}
		approvalInvalidated = true
	} else if _, err := tx.Exec(
		`UPDATE issues SET read = CASE WHEN ? THEN 0 ELSE read END, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		authorKind == AuthorUser, issueID,
	); err != nil {
		return fmt.Errorf("touch issue reply: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reply: %w", err)
	}

	// Ping the counterpart so a live thread refreshes. Body text is never put on
	// the notification; only the issue id + a fixed event string travel.
	if s.notifier != nil {
		if authorKind == AuthorAdmin && reporterID.Valid {
			s.notifier.NotifyUser(reporterID.Int64, "issue_updated", map[string]interface{}{"issue_id": issueID})
		} else if authorKind == AuthorUser {
			s.notifier.NotifyAdmins("issue_updated", map[string]interface{}{"issue_id": issueID})
		}
	}

	if resumeReady {
		s.EnqueueResume(issueID)
	}
	if approvalInvalidated {
		s.notifyActionsChanged(issueID, ActionSuperseded)
	}
	return nil
}

func stageApprovalThreadUpdateTx(tx *sql.Tx, issueID int64) (bool, error) {
	var actionID, runID int64
	var toolUseID string
	if err := tx.QueryRow(
		`SELECT a.id, a.run_id, COALESCE(a.tool_use_id, '')
		 FROM agent_actions a JOIN issues i ON i.id = a.issue_id
		 JOIN agent_runs r ON r.id = a.run_id
		 WHERE a.issue_id = ? AND a.status = ? AND i.status = ? AND i.closed_at IS NULL
		   AND r.status = ? ORDER BY a.id DESC LIMIT 1`,
		issueID, ActionProposed, IssueAwaitingApproval, runStatusWaitingApproval,
	).Scan(&actionID, &runID, &toolUseID); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	res, err := tx.Exec(
		`UPDATE agent_actions SET status = ?, decided_at = COALESCE(decided_at, CURRENT_TIMESTAMP),
		 result_text = 'Superseded because new issue-thread information arrived before an admin decision.'
		 WHERE id = ? AND status = ?`,
		ActionSuperseded, actionID, ActionProposed,
	)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return false, nil
	}
	return stageResumeResultTx(tx, issueID, runID,
		IssueAwaitingApproval, runStatusWaitingApproval,
		"propose_action", toolUseID,
		"Proposal superseded because new issue-thread information arrived; read it before proposing another fix.", true)
}

func (s *Service) saveUnresumableApprovalReply(issueID int64, authorKind string, authorID int64, body string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin fallback approval reply: %w", err)
	}
	defer tx.Rollback()
	var reporterID sql.NullInt64
	var closedAt sql.NullTime
	if err := tx.QueryRow("SELECT reporter_id, closed_at FROM issues WHERE id = ?", issueID).Scan(&reporterID, &closedAt); err == sql.ErrNoRows {
		return fmt.Errorf("issue not found")
	} else if err != nil {
		return err
	} else if closedAt.Valid {
		return fmt.Errorf("issue is closed")
	}
	if _, err := tx.Exec(
		"INSERT INTO issue_messages (issue_id, author_kind, author_id, body) VALUES (?, ?, ?, ?)",
		issueID, authorKind, sqlNullInt64(authorID), body,
	); err != nil {
		return fmt.Errorf("save fallback approval reply: %w", err)
	}
	if _, err := tx.Exec(
		`UPDATE agent_actions SET status = ?, decided_at = COALESCE(decided_at, CURRENT_TIMESTAMP),
		 result_text = 'Superseded because new issue-thread information arrived but the agent transcript could not resume.'
		 WHERE issue_id = ? AND status = ?`,
		ActionSuperseded, issueID, ActionProposed,
	); err != nil {
		return err
	}
	res, err := tx.Exec(
		`UPDATE issues SET status = ?, read = CASE WHEN ? THEN 0 ELSE read END,
		 active_run_id = NULL,
		 resolution = 'A reply arrived while approval was pending, but the agent transcript could not resume. An administrator needs to review it.',
		 resolution_kind = '', updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status = ? AND closed_at IS NULL`,
		IssueNeedsAdmin, authorKind == AuthorUser, issueID, IssueAwaitingApproval,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		if _, err := tx.Exec(
			`UPDATE agent_runs SET status = 'aborted', stop_reason = 'unresumable_transcript',
			 deadline_at = NULL, finished_at = COALESCE(finished_at, CURRENT_TIMESTAMP)
			 WHERE issue_id = ? AND status IN (?, ?)`,
			issueID, runStatusWaitingApproval, runStatusResumePending,
		); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.notifyActionsChanged(issueID, ActionSuperseded)
	if s.notifier != nil {
		if authorKind == AuthorAdmin && reporterID.Valid {
			s.notifier.NotifyUser(reporterID.Int64, "issue_updated", map[string]interface{}{"issue_id": issueID})
		} else if authorKind == AuthorUser {
			s.notifier.NotifyAdmins("issue_updated", map[string]interface{}{"issue_id": issueID})
		}
	}
	return nil
}

func (s *Service) saveUnresumableReply(issueID int64, authorKind string, authorID int64, body string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin fallback reply: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.Exec(
		`UPDATE issues SET status = ?, read = CASE WHEN ? THEN 0 ELSE read END,
		 active_run_id = NULL,
		 resolution = 'The reply was saved, but the original agent transcript could not be resumed. An administrator needs to review it.',
		 resolution_kind = '', updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status = ? AND closed_at IS NULL`,
		IssueNeedsAdmin, authorKind == AuthorUser, issueID, IssueAwaitingUser,
	)
	if err != nil {
		return fmt.Errorf("escalate fallback reply: %w", err)
	}
	escalated, _ := res.RowsAffected()
	var reporterID sql.NullInt64
	var closedAt sql.NullTime
	if err := tx.QueryRow("SELECT reporter_id, closed_at FROM issues WHERE id = ?", issueID).Scan(&reporterID, &closedAt); err == sql.ErrNoRows {
		return fmt.Errorf("issue not found")
	} else if err != nil {
		return fmt.Errorf("reload fallback issue: %w", err)
	} else if closedAt.Valid {
		return fmt.Errorf("issue is closed")
	}
	if _, err := tx.Exec(
		"INSERT INTO issue_messages (issue_id, author_kind, author_id, body) VALUES (?, ?, ?, ?)",
		issueID, authorKind, sqlNullInt64(authorID), body,
	); err != nil {
		return fmt.Errorf("save fallback reply: %w", err)
	}
	if escalated > 0 {
		if _, err := tx.Exec(
			`UPDATE agent_runs SET status = 'aborted', stop_reason = 'unresumable_transcript',
			 deadline_at = NULL, finished_at = COALESCE(finished_at, CURRENT_TIMESTAMP)
			 WHERE issue_id = ? AND status IN (?, ?)`,
			issueID, runStatusWaitingUser, runStatusResumePending,
		); err != nil {
			return fmt.Errorf("stop unresumable reporter run: %w", err)
		}
	} else if _, err := tx.Exec(
		`UPDATE issues SET read = CASE WHEN ? THEN 0 ELSE read END,
		 updated_at = CURRENT_TIMESTAMP WHERE id = ? AND closed_at IS NULL`,
		authorKind == AuthorUser, issueID,
	); err != nil {
		return fmt.Errorf("touch raced fallback reply: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit fallback reply: %w", err)
	}
	s.pingIssueUpdated(issueID)
	if s.notifier != nil {
		if authorKind == AuthorAdmin && reporterID.Valid {
			s.notifier.NotifyUser(reporterID.Int64, "issue_updated", map[string]interface{}{"issue_id": issueID})
		} else if authorKind == AuthorUser {
			s.notifier.NotifyAdmins("issue_updated", map[string]interface{}{"issue_id": issueID})
		}
	}
	return nil
}

// SweepStaleAwaitingUser is the W4 reply-TTL: it closes every issue parked
// awaiting_user whose last activity is older than maxWaitHours (the reporter
// never answered the agent's clarifying question within the window). Each is
// moved to wont_fix(user_unresponsive) with a plain-language thread message; the
// parked run is finalized and the admins are notified (via ConcludeIssue's
// resolution ping). It runs from a cheap periodic ticker (StartReplyTTLSweeper)
// and is idempotent — an already-closed issue no longer matches. Returns the
// number of issues closed (for logging/tests). maxWaitHours<=0 disables the sweep.
func (s *Service) SweepStaleAwaitingUser(ctx context.Context, maxWaitHours int) (int, error) {
	if maxWaitHours <= 0 {
		return 0, nil
	}
	// Find awaiting_user issues whose updated_at is older than the window. The ask
	// message touched updated_at when the question was posted, so the clock starts
	// from "asked"; a reply would have moved the issue out of awaiting_user.
	cutoff := fmt.Sprintf("-%d hours", maxWaitHours)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM issues
		 WHERE status = ? AND closed_at IS NULL
		   AND updated_at <= datetime('now', ?)`,
		IssueAwaitingUser, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("query stale awaiting_user issues: %w", err)
	}
	var stale []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan stale issue: %w", err)
		}
		stale = append(stale, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	closed := 0
	for _, id := range stale {
		transitioned, err := s.concludeIssueCAS(ctx, id, IssueWontFix,
			ResolutionUserUnresponsive, ResolutionReporterTimeout,
			IssueAwaitingUser, cutoff)
		if err != nil {
			continue
		}
		if transitioned {
			closed++
		}
	}
	return closed, nil
}

// ListIssues returns issues for the admin queue (newest first), optionally
// filtered by status. An empty/blank status returns all issues.
func (s *Service) ListIssues(status string) ([]Issue, error) {
	query := `SELECT i.id, i.source, i.status, i.category, i.reporter_id, u.username,
	                 i.tmdb_id, i.tvdb_id, i.media_type, i.title, i.season_number, i.episode_number,
	                 i.detail, i.occurrences, i.read, i.resolution, i.resolution_kind,
	                 i.created_at, i.updated_at, i.closed_at,
	                 i.instance_id, i.download_id, i.arr_queue_id
	          FROM issues i LEFT JOIN users u ON u.id = i.reporter_id`
	var (
		rows *sql.Rows
		err  error
	)
	if status != "" {
		rows, err = s.db.Query(query+" WHERE i.status = ? ORDER BY i.updated_at DESC, i.id DESC", status)
	} else {
		rows, err = s.db.Query(query + " ORDER BY i.updated_at DESC, i.id DESC")
	}
	if err != nil {
		return nil, fmt.Errorf("query issues: %w", err)
	}
	defer rows.Close()

	out := []Issue{}
	for rows.Next() {
		iss, err := scanIssue(rows)
		if err != nil {
			return nil, fmt.Errorf("scan issue: %w", err)
		}
		out = append(out, *iss)
	}
	return out, rows.Err()
}

// DismissIssue marks an open (non-terminal) issue dismissed and closes it. The
// CAS on closed_at IS NULL makes a double-dismiss a no-op. Dismissal is an admin
// action, so the issue is also marked read (an admin status change never re-flags
// it unread).
func (s *Service) DismissIssue(issueID int64) error {
	return s.concludeIssue(context.Background(), issueID, IssueDismissed,
		"Dismissed by an administrator.", ResolutionAdminDismissed)
}

// MarkIssueRead clears the admin unread flag on an issue. It is a side effect of
// an admin opening the issue thread (the Get handler calls it); a reporter
// viewing their own issue does not mark it read. It deliberately leaves
// updated_at untouched so "read" never re-sorts the issue as recently active.
// Idempotent, and a harmless no-op for a nonexistent issue.
func (s *Service) MarkIssueRead(issueID int64) error {
	if _, err := s.db.Exec("UPDATE issues SET read = 1 WHERE id = ?", issueID); err != nil {
		return fmt.Errorf("mark issue read: %w", err)
	}
	return nil
}

// OpenIssueCount counts issues that are not in a terminal/closed state. It backs
// the admin badge, mirroring request.PendingCount.
func (s *Service) OpenIssueCount() (int, error) {
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM issues WHERE closed_at IS NULL").Scan(&n); err != nil {
		return 0, fmt.Errorf("count open issues: %w", err)
	}
	return n, nil
}

// rowScanner abstracts *sql.Row and *sql.Rows so scanIssue serves GetIssue and
// ListIssues alike.
type rowScanner interface {
	Scan(dest ...interface{}) error
}

// scanIssue reads one issue row (joined to its reporter's username) into an
// Issue, mapping NULL category/reporter to nil pointers for the JSON contract.
func scanIssue(row rowScanner) (*Issue, error) {
	var (
		iss            Issue
		category       sql.NullString
		reporterID     sql.NullInt64
		reporter       sql.NullString
		resolution     sql.NullString
		resolutionKind sql.NullString
		closedAt       sql.NullTime
		tvdbID         sql.NullInt64
		instanceID     sql.NullString
		downloadID     sql.NullString
		arrQueueID     sql.NullInt64
	)
	if err := row.Scan(
		&iss.ID, &iss.Source, &iss.Status, &category, &reporterID, &reporter,
		&iss.TmdbID, &tvdbID, &iss.MediaType, &iss.Title, &iss.SeasonNumber, &iss.EpisodeNumber,
		&iss.Detail, &iss.Occurrences, &iss.Read, &resolution, &resolutionKind,
		&iss.CreatedAt, &iss.UpdatedAt, &closedAt, &instanceID, &downloadID, &arrQueueID,
	); err != nil {
		return nil, err
	}
	if category.Valid && category.String != "" {
		v := category.String
		iss.Category = &v
	}
	if reporterID.Valid {
		v := reporterID.Int64
		iss.ReporterID = &v
	}
	if reporter.Valid && reporter.String != "" {
		v := reporter.String
		iss.ReporterName = &v
	}
	iss.Resolution = resolution.String
	iss.ResolutionKind = resolutionKind.String
	if closedAt.Valid {
		v := closedAt.Time
		iss.ClosedAt = &v
	}
	iss.InstanceID = instanceID.String
	iss.DownloadID = downloadID.String
	iss.TvdbID = int(tvdbID.Int64)
	if arrQueueID.Valid {
		iss.ArrQueueID = int(arrQueueID.Int64)
	}
	return &iss, nil
}

// sqlNullInt / sqlNullInt64 / sqlNullStr map zero values to NULL for nullable
// columns, mirroring the request package's helpers.
func sqlNullInt(v int) interface{} {
	if v == 0 {
		return nil
	}
	return v
}

func sqlNullInt64(v int64) interface{} {
	if v == 0 {
		return nil
	}
	return v
}

func sqlNullStr(v string) interface{} {
	if v == "" {
		return nil
	}
	return v
}
