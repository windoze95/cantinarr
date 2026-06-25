package remediation

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
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
	if req.MediaType != "movie" && req.MediaType != "tv" {
		return nil, fmt.Errorf("unsupported media type: %s", req.MediaType)
	}
	if !validCategory(req.Category) {
		return nil, fmt.Errorf("invalid category: %s", req.Category)
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
			(source, status, category, reporter_id, tmdb_id, tvdb_id, media_type, title, season_number, episode_number, detail)
		 SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		 WHERE NOT EXISTS (
			SELECT 1 FROM issues
			WHERE source = ? AND reporter_id = ? AND tmdb_id = ? AND media_type = ?
			  AND season_number = ? AND episode_number = ? AND category = ? AND closed_at IS NULL
		 )`,
		SourceUser, IssueOpen, req.Category, reporterID, tmdbID, sqlNullInt(tvdbID), req.MediaType, req.Title, season, episode, req.Reason,
		SourceUser, reporterID, tmdbID, req.MediaType, season, episode, req.Category,
	)
	if err != nil {
		return nil, fmt.Errorf("create issue: %w", err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		// Duplicate open report: bump occurrences + refresh updated_at on the
		// existing open issue, and return it.
		id, derr := s.bumpDuplicateUserIssue(reporterID, req, tmdbID, season, episode)
		if derr != nil {
			return nil, derr
		}
		return &CreateIssueResponse{IssueID: id, Status: IssueOpen}, nil
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

// bumpDuplicateUserIssue increments occurrences on the existing open issue that
// matched the dedupe predicate and returns its id.
func (s *Service) bumpDuplicateUserIssue(reporterID int64, req *CreateIssueRequest, tmdbID, season, episode int) (int64, error) {
	var id int64
	err := s.db.QueryRow(
		`SELECT id FROM issues
		 WHERE source = ? AND reporter_id = ? AND tmdb_id = ? AND media_type = ?
		   AND season_number = ? AND episode_number = ? AND category = ? AND closed_at IS NULL
		 ORDER BY id DESC LIMIT 1`,
		SourceUser, reporterID, tmdbID, req.MediaType, season, episode, req.Category,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("find duplicate issue: %w", err)
	}
	if _, err := s.db.Exec(
		"UPDATE issues SET occurrences = occurrences + 1, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		id,
	); err != nil {
		return 0, fmt.Errorf("bump duplicate issue: %w", err)
	}
	return id, nil
}

// OpenAutoIssue is the poller hook for auto-detected problems (later waves; no
// caller in Wave 1). It conditionally inserts an issue keyed by a stable
// dedupe_key so the same stuck download opens at most one open issue across many
// polls, mirroring createPending. On a duplicate it bumps occurrences and
// returns created=false. A terminal (closed) issue does not block a genuinely
// new recurrence: once closed_at is set the partial unique index no longer
// guards that key, so the same download re-failing later opens a fresh issue.
func (s *Service) OpenAutoIssue(scope, instanceID, downloadID string, d arr.Diagnosis) (created bool, id int64) {
	sum := sha256.Sum256([]byte(instanceID + "|" + downloadID + "|" + d.Problem))
	dedupeKey := hex.EncodeToString(sum[:])

	res, err := s.db.Exec(
		`INSERT INTO issues
			(source, status, media_type, tmdb_id, title, instance_id, download_id, detail, dedupe_key)
		 SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?
		 WHERE NOT EXISTS (
			SELECT 1 FROM issues WHERE dedupe_key = ? AND closed_at IS NULL
		 )`,
		SourceAuto, IssueOpen, scope, 0, d.Problem, sqlNullStr(instanceID), sqlNullStr(downloadID), d.Transparency, dedupeKey,
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
			"UPDATE issues SET updated_at = CURRENT_TIMESTAMP WHERE dedupe_key = ? AND closed_at IS NULL",
			dedupeKey,
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

// CloseAutoIssueForDownload resolves the open auto issue for a download whose
// problem the poller no longer detects (it recovered or left the queue). A
// no-op when there's no matching open issue. Idempotent via ConcludeIssue's CAS.
func (s *Service) CloseAutoIssueForDownload(instanceID, downloadID string) {
	if downloadID == "" {
		return
	}
	var issueID int64
	err := s.db.QueryRow(
		`SELECT id FROM issues
		 WHERE source = ? AND instance_id = ? AND download_id = ? AND closed_at IS NULL
		 LIMIT 1`,
		SourceAuto, instanceID, downloadID,
	).Scan(&issueID)
	if err != nil {
		return // sql.ErrNoRows (nothing open) or a read error: nothing to do.
	}
	_ = s.ConcludeIssue(context.Background(), issueID, IssueResolved,
		"Auto-resolved: the download is no longer stuck or blocked — the problem cleared.")
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
		        i.tmdb_id, i.media_type, i.title, i.season_number, i.episode_number,
		        i.detail, i.occurrences, i.created_at, i.updated_at
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
	// Confirm the issue exists (and read its reporter + status for routing).
	var (
		reporterID sql.NullInt64
		status     string
	)
	err := s.db.QueryRow("SELECT reporter_id, status FROM issues WHERE id = ?", issueID).Scan(&reporterID, &status)
	if err == sql.ErrNoRows {
		return fmt.Errorf("issue not found")
	}
	if err != nil {
		return fmt.Errorf("load issue: %w", err)
	}

	if _, err := s.db.Exec(
		"INSERT INTO issue_messages (issue_id, author_kind, author_id, body) VALUES (?, ?, ?, ?)",
		issueID, authorKind, sqlNullInt64(authorID), body,
	); err != nil {
		return fmt.Errorf("post reply: %w", err)
	}
	s.db.Exec("UPDATE issues SET updated_at = CURRENT_TIMESTAMP WHERE id = ?", issueID)

	// Ping the counterpart so a live thread refreshes. Body text is never put on
	// the notification; only the issue id + a fixed event string travel.
	if s.notifier != nil {
		if authorKind == AuthorAdmin && reporterID.Valid {
			s.notifier.NotifyUser(reporterID.Int64, "issue_updated", map[string]interface{}{"issue_id": issueID})
		} else if authorKind == AuthorUser {
			s.notifier.NotifyAdmins("issue_updated", map[string]interface{}{"issue_id": issueID})
		}
	}

	// Resume a parked investigation: a reporter (or admin) reply to an
	// awaiting_user issue answers the agent's ask_reporter question. Feed the reply
	// to the run keyed to the ask tool_use, then enqueue the resume. (Defined in
	// approvals.go alongside the W3 approval-resume helper, which it mirrors.)
	if status == IssueAwaitingUser && (authorKind == AuthorUser || authorKind == AuthorAdmin) {
		s.resumeOnReporterReply(issueID, body)
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
		// Finalize any run still parked waiting on this reporter so it doesn't sit in
		// waiting_user forever (best-effort; the run is no longer resumable once the
		// issue is terminal).
		s.db.ExecContext(ctx,
			"UPDATE agent_runs SET status = ?, stop_reason = ?, finished_at = CURRENT_TIMESTAMP WHERE issue_id = ? AND status = ?",
			runStatusGaveUp, stopUserUnresponsive, id, runStatusWaitingUser,
		)
		// Plain-language closing message, then move the issue terminal. ConcludeIssue
		// stamps closed_at, releases the claim, and notifies the reporter + admins.
		_ = s.PostIssueMessage(ctx, id, "I didn't hear back, so I'm closing this for now. If it's still a problem, please report it again and I'll take another look.")
		if err := s.ConcludeIssue(ctx, id, IssueWontFix, ResolutionUserUnresponsive); err != nil {
			// Already closed (raced with a reply) or gone: skip without failing the
			// whole sweep.
			continue
		}
		closed++
	}
	return closed, nil
}

// ListIssues returns issues for the admin queue (newest first), optionally
// filtered by status. An empty/blank status returns all issues.
func (s *Service) ListIssues(status string) ([]Issue, error) {
	query := `SELECT i.id, i.source, i.status, i.category, i.reporter_id, u.username,
	                 i.tmdb_id, i.media_type, i.title, i.season_number, i.episode_number,
	                 i.detail, i.occurrences, i.created_at, i.updated_at
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
// CAS on closed_at IS NULL makes a double-dismiss a no-op.
func (s *Service) DismissIssue(issueID int64) error {
	res, err := s.db.Exec(
		"UPDATE issues SET status = ?, closed_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND closed_at IS NULL",
		IssueDismissed, issueID,
	)
	if err != nil {
		return fmt.Errorf("dismiss issue: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Either no such issue or already closed; treat as idempotent success
		// only when the issue exists.
		var exists int
		if qerr := s.db.QueryRow("SELECT 1 FROM issues WHERE id = ?", issueID).Scan(&exists); qerr == sql.ErrNoRows {
			return fmt.Errorf("issue not found")
		}
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
		iss        Issue
		category   sql.NullString
		reporterID sql.NullInt64
		reporter   sql.NullString
	)
	if err := row.Scan(
		&iss.ID, &iss.Source, &iss.Status, &category, &reporterID, &reporter,
		&iss.TmdbID, &iss.MediaType, &iss.Title, &iss.SeasonNumber, &iss.EpisodeNumber,
		&iss.Detail, &iss.Occurrences, &iss.CreatedAt, &iss.UpdatedAt,
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
