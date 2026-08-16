package push

import (
	"database/sql"
	"fmt"
)

// Prefs is a user's per-category push notification preferences. Each field
// gates one notification category; see the category constants below.
type Prefs struct {
	RequestDecision    bool `json:"request_decision"`
	RequestPending     bool `json:"request_pending"`
	NewMovie           bool `json:"new_movie"`
	NewEpisode         bool `json:"new_episode"`
	NewBook            bool `json:"new_book"`
	IssueCreated       bool `json:"issue_created"`
	AgentActionPending bool `json:"agent_action_pending"`
	PlexAccessRequest  bool `json:"plex_access_request"`
	PlexInviteSent     bool `json:"plex_invite_sent"`
	IssueReportUpdate  bool `json:"issue_report_update"`
	AgentDigest        bool `json:"agent_digest"`
	ContentUpgraded    bool `json:"content_upgraded"`
}

// Notification categories. These are the wire values used by the preferences
// API and the column names in notification_prefs.
const (
	CategoryRequestDecision = "request_decision"
	CategoryRequestPending  = "request_pending"
	CategoryNewMovie        = "new_movie"
	CategoryNewEpisode      = "new_episode"
	// CategoryNewBook tells users a Chaptarr book import landed. On by
	// default, but scoped per instance (usersOptedIntoNewBook): the audience
	// is the users assigned to the instance the import landed on — a chaptarr
	// assignment is the books access model, and it scopes admins too, so a
	// household running one library per person is not cross-paged. Only an
	// admin with no books assignment at all hears every instance.
	CategoryNewBook = "new_book"
	// CategoryIssueCreated notifies admins of a new AI-remediation issue
	// (user-reported or auto-detected). Admin-scoped, on by default.
	CategoryIssueCreated = "issue_created"
	// CategoryAgentActionPending notifies admins that the AI agent proposed a fix
	// awaiting their approval. Admin-scoped, on by default.
	CategoryAgentActionPending = "agent_action_pending"
	// CategoryAgentAutoApprovalPaused tells admins a standing auto-approval
	// rule disarmed itself after a failed or unverifiable outcome. It
	// deliberately SHARES the agent_action_pending preference column: both are
	// "the agent-fix pipeline needs your decision" alerts, and a schema-free
	// mapping keeps notification_prefs (and its full-row PUT contract)
	// untouched.
	CategoryAgentAutoApprovalPaused = "agent_autoapproval_paused"
	// CategoryPlexAccessRequest notifies admins that a user shared their Plex
	// email and is waiting for a server invite. Admin-scoped, on by default.
	CategoryPlexAccessRequest = "plex_access_request"
	// CategoryPlexInviteSent tells a user their Plex invite went out (one-tap
	// or auto) so they know to check their inbox. User-scoped, on by default.
	CategoryPlexInviteSent = "plex_invite_sent"
	// CategoryIssueReportUpdate covers every reporter-loop push about a user's
	// OWN report — the agent asked them a question, a fix was applied and
	// awaits their confirmation, or the report closed. One user-scoped
	// preference on purpose: they are a single conversation's beats, and a
	// reporter who wants any of them wants all of them. On by default.
	CategoryIssueReportUpdate = "issue_report_update"
	// CategoryAgentDigest is the weekly agent scoreboard — the one push that
	// exists to report SUCCESS. The pipeline deliberately never pages when
	// automation works, which left an admin's felt experience as silence
	// punctuated by problems; one line a week rebalances that. Admin-scoped,
	// on by default, and skipped entirely on a week with nothing to say.
	CategoryAgentDigest = "agent_digest"
	// CategoryProfileChangePending tells admins an external MCP agent parked
	// a quality-profile change that applies only if one of them approves it
	// in the app. It deliberately SHARES the agent_action_pending preference
	// column (the CategoryAgentAutoApprovalPaused precedent): this too is a
	// "the agent pipeline needs your decision" alert, and the schema-free
	// mapping keeps notification_prefs and its full-row PUT contract
	// untouched.
	CategoryProfileChangePending = "profile_change_pending"
	// CategoryContentUpgraded tells admins an existing movie/episode/book file
	// was replaced by a quality upgrade. Admin-scoped and OFF by default:
	// upgrades are library maintenance, not news — a requester already has the
	// content, and re-paging the household about a better copy is noise. The
	// broadcast new_movie/new_episode/new_book alert is suppressed only on
	// positive proof of an upgrade (webhook isUpgrade, or a paired
	// delete-for-upgrade history record on catch-up); without proof the event
	// broadcasts as new content, never the other way around.
	CategoryContentUpgraded = "content_upgraded"
)

// defaultPrefs is the preference set applied when a user has no row. It must
// match the notification_prefs column defaults and the documented API
// defaults: request_decision and content_upgraded off, everything else on.
var defaultPrefs = Prefs{
	RequestDecision:    false,
	RequestPending:     true,
	NewMovie:           true,
	NewEpisode:         true,
	NewBook:            true,
	IssueCreated:       true,
	AgentActionPending: true,
	PlexAccessRequest:  true,
	PlexInviteSent:     true,
	IssueReportUpdate:  true,
	AgentDigest:        true,
	ContentUpgraded:    false,
}

// categoryColumn maps a category to its notification_prefs column and the
// default applied when a user has no row. Centralizing this keeps the store's
// SQL and the defaults in one place.
var categoryColumn = map[string]struct {
	column     string
	defaultVal bool
}{
	CategoryRequestDecision:    {"request_decision", defaultPrefs.RequestDecision},
	CategoryRequestPending:     {"request_pending", defaultPrefs.RequestPending},
	CategoryNewMovie:           {"new_movie", defaultPrefs.NewMovie},
	CategoryNewEpisode:         {"new_episode", defaultPrefs.NewEpisode},
	CategoryNewBook:            {"new_book", defaultPrefs.NewBook},
	CategoryIssueCreated:       {"issue_created", defaultPrefs.IssueCreated},
	CategoryAgentActionPending: {"agent_action_pending", defaultPrefs.AgentActionPending},
	// Shares the agent_action_pending column by design (see the category const).
	CategoryAgentAutoApprovalPaused: {"agent_action_pending", defaultPrefs.AgentActionPending},
	// Shares the agent_action_pending column by design (see the category const).
	CategoryProfileChangePending: {"agent_action_pending", defaultPrefs.AgentActionPending},
	CategoryPlexAccessRequest:    {"plex_access_request", defaultPrefs.PlexAccessRequest},
	CategoryPlexInviteSent:       {"plex_invite_sent", defaultPrefs.PlexInviteSent},
	CategoryIssueReportUpdate:    {"issue_report_update", defaultPrefs.IssueReportUpdate},
	CategoryAgentDigest:          {"agent_digest", defaultPrefs.AgentDigest},
	CategoryContentUpgraded:      {"content_upgraded", defaultPrefs.ContentUpgraded},
}

// PrefsStore reads and writes per-user notification preferences. It is safe to
// build with any *sql.DB carrying the notification_prefs table.
type PrefsStore struct {
	db *sql.DB
}

// NewPrefsStore builds a preferences store over the given database.
func NewPrefsStore(db *sql.DB) *PrefsStore {
	return &PrefsStore{db: db}
}

// Get returns a user's preferences, applying the defaults for any user without
// a row.
func (s *PrefsStore) Get(userID int64) (Prefs, error) {
	p := defaultPrefs
	err := s.db.QueryRow(
		`SELECT request_decision, request_pending, new_movie, new_episode, new_book, issue_created, agent_action_pending, plex_access_request, plex_invite_sent, issue_report_update, agent_digest, content_upgraded
		 FROM notification_prefs WHERE user_id = ?`,
		userID,
	).Scan(&p.RequestDecision, &p.RequestPending, &p.NewMovie, &p.NewEpisode, &p.NewBook, &p.IssueCreated, &p.AgentActionPending, &p.PlexAccessRequest, &p.PlexInviteSent, &p.IssueReportUpdate, &p.AgentDigest, &p.ContentUpgraded)
	if err == sql.ErrNoRows {
		return defaultPrefs, nil
	}
	if err != nil {
		return Prefs{}, fmt.Errorf("query notification prefs: %w", err)
	}
	return p, nil
}

// Set upserts a user's preferences.
func (s *PrefsStore) Set(userID int64, p Prefs) error {
	_, err := s.db.Exec(
		`INSERT INTO notification_prefs
		   (user_id, request_decision, request_pending, new_movie, new_episode, new_book, issue_created, agent_action_pending, plex_access_request, plex_invite_sent, issue_report_update, agent_digest, content_upgraded)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET
		   request_decision = excluded.request_decision,
		   request_pending  = excluded.request_pending,
		   new_movie        = excluded.new_movie,
		   new_episode      = excluded.new_episode,
		   new_book         = excluded.new_book,
		   issue_created    = excluded.issue_created,
		   agent_action_pending = excluded.agent_action_pending,
		   plex_access_request  = excluded.plex_access_request,
		   plex_invite_sent     = excluded.plex_invite_sent,
		   issue_report_update  = excluded.issue_report_update,
		   agent_digest         = excluded.agent_digest,
		   content_upgraded     = excluded.content_upgraded`,
		userID, p.RequestDecision, p.RequestPending, p.NewMovie, p.NewEpisode, p.NewBook, p.IssueCreated, p.AgentActionPending, p.PlexAccessRequest, p.PlexInviteSent, p.IssueReportUpdate, p.AgentDigest, p.ContentUpgraded,
	)
	if err != nil {
		return fmt.Errorf("upsert notification prefs: %w", err)
	}
	return nil
}

// usersOptedInto returns the ids of every user opted into a category, applying
// the category default for users without a row (LEFT JOIN + COALESCE). The
// request_pending category is additionally limited to admins, since only
// admins act on pending requests.
func (s *PrefsStore) usersOptedInto(category string) ([]int64, error) {
	if category == CategoryNewBook {
		// new_book is per-instance truth: a chaptarr grant is the books access
		// model, so an unscoped audience would leak book alerts to users who
		// cannot see any books. Force callers through the scoped query.
		return nil, fmt.Errorf("category %q requires usersOptedIntoNewBook", category)
	}
	col, ok := categoryColumn[category]
	if !ok {
		return nil, fmt.Errorf("unknown notification category %q", category)
	}
	def := 0
	if col.defaultVal {
		def = 1
	}
	// Column names come from the trusted categoryColumn table, never user input.
	query := fmt.Sprintf(
		`SELECT u.id FROM users u
		 LEFT JOIN notification_prefs p ON p.user_id = u.id
		 WHERE COALESCE(p.%s, %d) = 1`,
		col.column, def,
	)
	// Admin-scoped categories: only admins act on pending requests, issues,
	// agent-action approvals, paused auto-approval rules, Plex access
	// requests, or care that a file was replaced by a quality upgrade.
	if category == CategoryRequestPending || category == CategoryIssueCreated ||
		category == CategoryAgentActionPending || category == CategoryAgentAutoApprovalPaused ||
		category == CategoryProfileChangePending ||
		category == CategoryPlexAccessRequest || category == CategoryAgentDigest ||
		category == CategoryContentUpgraded {
		query += " AND u.role = 'admin'"
	}
	return s.queryUserIDs(query)
}

// usersOptedIntoNewBook returns the ids of every user opted into new_book
// whose book library is the given Chaptarr instance. Book availability is
// per-instance truth, and Chaptarr instances are per-person libraries, so
// unlike the other new-content categories the audience follows the assignment
// row — for admins too: "ready to read" is a call to action for the person
// who will read it, and an admin running a sibling library must not be paged
// for every import in someone else's (their oversight lives in the
// request/issue categories). One deliberate exception keeps the pre-existing
// behavior: an admin with no books assignment at all browses Books through
// the default-instance fallback, so they keep hearing every instance rather
// than being silently muted by a screen they never visited.
func (s *PrefsStore) usersOptedIntoNewBook(instanceID string) ([]int64, error) {
	col := categoryColumn[CategoryNewBook]
	def := 0
	if col.defaultVal {
		def = 1
	}
	// Column name comes from the trusted categoryColumn table, never user input.
	query := fmt.Sprintf(
		`SELECT u.id FROM users u
		 LEFT JOIN notification_prefs p ON p.user_id = u.id
		 WHERE COALESCE(p.%s, %d) = 1
		   AND (EXISTS (
		     SELECT 1 FROM user_default_instances d
		     WHERE d.user_id = u.id AND d.instance_id = ?)
		   OR (u.role = 'admin' AND NOT EXISTS (
		     SELECT 1 FROM user_default_instances d
		     WHERE d.user_id = u.id AND d.service_type = 'chaptarr')))`,
		col.column, def,
	)
	return s.queryUserIDs(query, instanceID)
}

// queryUserIDs runs a query whose result set is a single user-id column and
// returns the ids.
func (s *PrefsStore) queryUserIDs(query string, args ...any) ([]int64, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query opted-in users: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan opted-in user id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// optedIn reports whether a single user is opted into a category, applying the
// category default when the user has no row. Errors (and unknown categories)
// fail closed by returning false.
func (s *PrefsStore) optedIn(userID int64, category string) bool {
	col, ok := categoryColumn[category]
	if !ok {
		return false
	}
	def := 0
	if col.defaultVal {
		def = 1
	}
	var enabled int
	query := fmt.Sprintf(
		`SELECT COALESCE(
		   (SELECT %s FROM notification_prefs WHERE user_id = ?), %d)`,
		col.column, def,
	)
	if err := s.db.QueryRow(query, userID).Scan(&enabled); err != nil {
		return false
	}
	return enabled == 1
}
