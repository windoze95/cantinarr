// Package remediation implements Cantinarr's issue-reporting data model, the
// service that records and threads issues, and the admin REST surface. Wave 1
// ships issue reporting only: there is no AI agent here yet (the agent loop,
// Runner, Executor, agent-only tools, and auto-dispatch arrive in later waves).
//
// All user-supplied text (an issue's detail/reason and any message body) is
// UNTRUSTED: it is stored verbatim and never interpreted as an instruction.
package remediation

import "time"

// Issue status values stored in issues.status and returned to clients. The
// state machine spans the whole feature; Wave 1 only ever sets IssueOpen (on
// create) and IssueDismissed (admin dismiss). The rest are defined now so the
// API contract and later waves agree.
const (
	IssueOpen             = "open"
	IssueInvestigating    = "investigating"
	IssueAwaitingUser     = "awaiting_user"
	IssueAwaitingApproval = "awaiting_approval"
	IssueResolved         = "resolved"
	IssueWontFix          = "wont_fix"
	IssueFailed           = "failed"
	IssueDismissed        = "dismissed"
)

// Issue source values.
const (
	SourceUser = "user"
	SourceAuto = "auto"
)

// User-selectable issue categories (NULL/"" for auto-detected issues).
const (
	CategoryWrongContent = "wrong_content"
	CategoryBadCopy      = "bad_copy"
	CategoryWrongAudio   = "wrong_audio"
	CategoryOther        = "other"
)

// Message author kinds. Provenance tag so agent code never treats a user/system
// message as an instruction.
const (
	AuthorUser   = "user"
	AuthorAgent  = "agent"
	AuthorAdmin  = "admin"
	AuthorSystem = "system"
)

// Action lifecycle status values (agent_actions.status). Used by later waves;
// defined now so the table vocabulary is stable.
const (
	ActionProposed   = "proposed"
	ActionApproved   = "approved"
	ActionExecuting  = "executing"
	ActionExecuted   = "executed"
	ActionDenied     = "denied"
	ActionFailed     = "failed"
	ActionSuperseded = "superseded"
)

// ActionKind enumerates the proposable arr mutations (later waves).
type ActionKind string

const (
	ActionGrabRelease    ActionKind = "grab_release"
	ActionRemediateQueue ActionKind = "remediate_queue" // remove | blocklist_search | change_category
	ActionManualImport   ActionKind = "manual_import"   // force bool
	ActionTriggerSearch  ActionKind = "trigger_search"
	ActionRescan         ActionKind = "rescan"
)

// Issue is one row of the issues table as returned to clients. Nullable columns
// (category, reporter) are exposed as pointers so the JSON carries null, matching
// the Wave-1 API contract exactly.
type Issue struct {
	ID            int64     `json:"id"`
	Source        string    `json:"source"`        // "user" | "auto"
	Status        string    `json:"status"`        // see Issue* consts
	Category      *string   `json:"category"`      // null for auto
	ReporterID    *int64    `json:"reporter_id"`   // null for auto
	ReporterName  *string   `json:"reporter_name"` // null for auto / unknown
	TmdbID        int       `json:"tmdb_id"`
	MediaType     string    `json:"media_type"` // "movie" | "tv"
	Title         string    `json:"title"`
	SeasonNumber  int       `json:"season_number"`  // 0 = whole series / movie
	EpisodeNumber int       `json:"episode_number"` // 0 = whole season / movie
	Detail        string    `json:"detail"`         // UNTRUSTED free text
	Occurrences   int       `json:"occurrences"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// IssueMessage is one row of an issue's append-only thread.
type IssueMessage struct {
	ID         int64     `json:"id"`
	AuthorKind string    `json:"author_kind"` // "user" | "agent" | "admin" | "system"
	AuthorName *string   `json:"author_name"` // null for agent/system / unknown
	Body       string    `json:"body"`        // UNTRUSTED when author_kind="user"
	CreatedAt  time.Time `json:"created_at"`
}

// IssueDetail is the GET /api/issues/{id} payload: the issue plus its thread.
type IssueDetail struct {
	Issue  Issue          `json:"issue"`
	Thread []IssueMessage `json:"thread"`
}

// CreateIssueRequest is the POST /api/issues body (snake_case wire contract).
// Reason/Title are UNTRUSTED. SeasonNumber 0 = whole series; EpisodeNumber 0 =
// whole season.
type CreateIssueRequest struct {
	MediaType     string `json:"media_type"` // "movie" | "tv"
	TmdbID        int    `json:"tmdb_id"`
	TvdbID        int    `json:"tvdb_id"`
	SeasonNumber  int    `json:"season_number"`
	EpisodeNumber int    `json:"episode_number"`
	Category      string `json:"category"` // wrong_content|bad_copy|wrong_audio|other
	Reason        string `json:"reason"`   // UNTRUSTED free text
	Title         string `json:"title"`    // UNTRUSTED display hint
}

// CreateIssueResponse is the POST /api/issues result.
type CreateIssueResponse struct {
	IssueID int64  `json:"issue_id"`
	Status  string `json:"status"`
}

// ListIssuesResponse is the GET /api/admin/issues result.
type ListIssuesResponse struct {
	Issues []Issue `json:"issues"`
}
