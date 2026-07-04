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
	IssueCreated       bool `json:"issue_created"`
	AgentActionPending bool `json:"agent_action_pending"`
	PlexAccessRequest  bool `json:"plex_access_request"`
}

// Notification categories. These are the wire values used by the preferences
// API and the column names in notification_prefs.
const (
	CategoryRequestDecision = "request_decision"
	CategoryRequestPending  = "request_pending"
	CategoryNewMovie        = "new_movie"
	CategoryNewEpisode      = "new_episode"
	// CategoryIssueCreated notifies admins of a new AI-remediation issue
	// (user-reported or auto-detected). Admin-scoped, on by default.
	CategoryIssueCreated = "issue_created"
	// CategoryAgentActionPending notifies admins that the AI agent proposed a fix
	// awaiting their approval. Admin-scoped, on by default.
	CategoryAgentActionPending = "agent_action_pending"
	// CategoryPlexAccessRequest notifies admins that a user shared their Plex
	// email and is waiting for a server invite. Admin-scoped, on by default.
	CategoryPlexAccessRequest = "plex_access_request"
)

// defaultPrefs is the preference set applied when a user has no row. It must
// match the notification_prefs column defaults and the documented API
// defaults: request_decision off, everything else on.
var defaultPrefs = Prefs{
	RequestDecision:    false,
	RequestPending:     true,
	NewMovie:           true,
	NewEpisode:         true,
	IssueCreated:       true,
	AgentActionPending: true,
	PlexAccessRequest:  true,
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
	CategoryIssueCreated:       {"issue_created", defaultPrefs.IssueCreated},
	CategoryAgentActionPending: {"agent_action_pending", defaultPrefs.AgentActionPending},
	CategoryPlexAccessRequest:  {"plex_access_request", defaultPrefs.PlexAccessRequest},
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
		`SELECT request_decision, request_pending, new_movie, new_episode, issue_created, agent_action_pending, plex_access_request
		 FROM notification_prefs WHERE user_id = ?`,
		userID,
	).Scan(&p.RequestDecision, &p.RequestPending, &p.NewMovie, &p.NewEpisode, &p.IssueCreated, &p.AgentActionPending, &p.PlexAccessRequest)
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
		   (user_id, request_decision, request_pending, new_movie, new_episode, issue_created, agent_action_pending, plex_access_request)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET
		   request_decision = excluded.request_decision,
		   request_pending  = excluded.request_pending,
		   new_movie        = excluded.new_movie,
		   new_episode      = excluded.new_episode,
		   issue_created    = excluded.issue_created,
		   agent_action_pending = excluded.agent_action_pending,
		   plex_access_request  = excluded.plex_access_request`,
		userID, p.RequestDecision, p.RequestPending, p.NewMovie, p.NewEpisode, p.IssueCreated, p.AgentActionPending, p.PlexAccessRequest,
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
	// agent-action approvals, or Plex access requests.
	if category == CategoryRequestPending || category == CategoryIssueCreated ||
		category == CategoryAgentActionPending || category == CategoryPlexAccessRequest {
		query += " AND u.role = 'admin'"
	}

	rows, err := s.db.Query(query)
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
