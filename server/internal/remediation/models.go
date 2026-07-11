// Package remediation implements Cantinarr's issue-reporting data model, the
// service that records and threads issues, and the admin REST surface. Wave 1
// ships issue reporting only: there is no AI agent here yet (the agent loop,
// Runner, Executor, agent-only tools, and auto-dispatch arrive in later waves).
//
// All user-supplied text (an issue's detail/reason and any message body) is
// UNTRUSTED: it is stored verbatim and never interpreted as an instruction.
package remediation

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// Issue status values stored in issues.status and returned to clients. The
// state machine spans the whole feature; Wave 1 only ever sets IssueOpen (on
// create) and IssueDismissed (admin dismiss). The rest are defined now so the
// API contract and later waves agree.
const (
	IssueObserving        = "observing"
	IssueRecovering       = "recovering"
	IssueOpen             = "open"
	IssueInvestigating    = "investigating"
	IssueAwaitingUser     = "awaiting_user"
	IssueAwaitingApproval = "awaiting_approval"
	IssueNeedsAdmin       = "needs_admin"
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

// ResolutionUserUnresponsive is the closing note set on an awaiting_user issue
// that the reply-TTL sweep closes because the reporter never answered the
// agent's clarifying question within the window (W4).
const ResolutionUserUnresponsive = "user_unresponsive"

// Resolution kinds explain why an issue left active work. They deliberately
// distinguish an observed arr-state recovery from an agent-verified outcome:
// seeing a queue signal disappear proves only that the original incident is no
// longer present, not who fixed it.
const (
	ResolutionAgentConcluded  = "agent_concluded"
	ResolutionArrStateCleared = "arr_state_cleared"
	ResolutionReporterTimeout = "reporter_timeout"
	ResolutionAdminDismissed  = "admin_dismissed"
	ResolutionAdminCompleted  = "admin_completed"
	ResolutionLegacyUnknown   = "legacy_unknown"
)

// AdminIssueDisposition is the explicit human judgment recorded by the admin
// completion endpoint. Dismissal remains a separate workflow/provenance.
type AdminIssueDisposition string

const (
	AdminDispositionResolved AdminIssueDisposition = IssueResolved
	AdminDispositionWontFix  AdminIssueDisposition = IssueWontFix
)

// Action lifecycle status values (agent_actions.status). Used by later waves;
// defined now so the table vocabulary is stable.
const (
	ActionProposed       = "proposed"
	ActionExecuting      = "executing"
	ActionExecuted       = "executed"
	ActionDenied         = "denied"
	ActionFailed         = "failed"
	ActionSuperseded     = "superseded"
	ActionOutcomeUnknown = "outcome_unknown"
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
	ID             int64      `json:"id"`
	Source         string     `json:"source"`        // "user" | "auto"
	Status         string     `json:"status"`        // see Issue* consts
	Category       *string    `json:"category"`      // null for auto
	ReporterID     *int64     `json:"reporter_id"`   // null for auto
	ReporterName   *string    `json:"reporter_name"` // null for auto / unknown
	TmdbID         int        `json:"tmdb_id"`
	MediaType      string     `json:"media_type"` // "movie" | "tv"
	Title          string     `json:"title"`
	SeasonNumber   int        `json:"season_number"`  // 0 = whole series only when episode_number=0; otherwise specials
	EpisodeNumber  int        `json:"episode_number"` // 0 = whole season / movie
	Detail         string     `json:"detail"`         // UNTRUSTED free text
	Occurrences    int        `json:"occurrences"`
	Read           bool       `json:"read"` // admin has seen the current state; any non-admin status change re-flags unread
	Resolution     string     `json:"resolution"`
	ResolutionKind string     `json:"resolution_kind"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	ClosedAt       *time.Time `json:"closed_at"`

	// Exact arr scope. Auto-detected and user-reported issues both carry the
	// instance that owns the affected media so investigation cannot drift to a
	// different Radarr/Sonarr installation.
	InstanceID string `json:"instance_id"`
	DownloadID string `json:"-"`
	ArrQueueID int    `json:"-"`
	TvdbID     int    `json:"-"`
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
// Reason/Title are UNTRUSTED. EpisodeNumber 0 means whole season/series;
// SeasonNumber 0 with a positive episode is an exact special (S00E##).
type CreateIssueRequest struct {
	InstanceID    string `json:"instance_id"` // exact Radarr/Sonarr instance
	MediaType     string `json:"media_type"`  // "movie" | "tv"
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

// AgentAction is one row of the agent_actions table as returned to the admin
// approval queue. params/rationale are the agent's UNTRUSTED proposal text/JSON
// and are rendered as data (never executed by the client). Nullable decision
// fields are pointers so the JSON carries null until a decision is made.
type AgentAction struct {
	ID             int64            `json:"id"`
	IssueID        int64            `json:"issue_id"`
	RunID          *int64           `json:"run_id"`
	Kind           string           `json:"kind"`
	Params         json.RawMessage  `json:"params"`    // typed args for the kind (UNTRUSTED)
	Rationale      string           `json:"rationale"` // agent's justification (UNTRUSTED)
	Risk           string           `json:"risk"`      // compatibility/audit field; all current actions are mutating and gated
	Status         string           `json:"status"`    // see Action* consts
	DecidedBy      *int64           `json:"decided_by"`
	DecidedAt      *time.Time       `json:"decided_at"`
	DenyReason     *string          `json:"deny_reason"`
	ExecutedAt     *time.Time       `json:"executed_at"`
	ResultText     *string          `json:"result_text"`
	CreatedAt      time.Time        `json:"created_at"`
	ApprovedParams *json.RawMessage `json:"approved_params"`

	// ToolUseID is the propose_action tool_use.id; internal only (used to key the
	// resume tool_result back to the originating call), not exposed on the wire.
	ToolUseID string `json:"-"`
	GateValid bool   `json:"-"`

	// Joined from the issue for the approval-queue list view.
	IssueTitle     string     `json:"issue_title"`
	IssueMediaType string     `json:"issue_media_type"`
	IssueCategory  *string    `json:"issue_category"`
	IssueStatus    string     `json:"issue_status"`
	IssueClosedAt  *time.Time `json:"issue_closed_at"`
	// InstanceID is copied from the issue's immutable arr scope. Name and
	// service type are display metadata joined from the current instance row;
	// approval always targets InstanceID, never a name or client-supplied value.
	InstanceID          string `json:"instance_id"`
	InstanceName        string `json:"instance_name"`
	InstanceServiceType string `json:"instance_service_type"`
	CanDecide           bool   `json:"can_decide"`
	BlockedReason       string `json:"blocked_reason,omitempty"`
}

// MarshalJSON is the wire boundary for action params. Release GUIDs are opaque
// indexer capabilities: some are signed URLs or embed API credentials. Only a
// stable one-way reference is persisted or returned; approval resolves the raw
// capability from a fresh exact-scope search and keeps it in memory only for
// the immediate dispatch.
func (a AgentAction) MarshalJSON() ([]byte, error) {
	type wireAction AgentAction
	wire := wireAction(a)
	wire.Rationale = secrets.RedactText(a.Rationale)
	wire.IssueTitle = secrets.RedactText(a.IssueTitle)
	wire.DenyReason = redactedStringPointer(a.DenyReason)
	wire.ResultText = redactedStringPointer(a.ResultText)
	wire.Params = actionParamsForWire(a.Kind, a.Params)
	if a.ApprovedParams != nil {
		approved := actionParamsForWire(a.Kind, *a.ApprovedParams)
		wire.ApprovedParams = &approved
	}
	return json.Marshal(wire)
}

func actionParamsForWire(kind string, raw json.RawMessage) json.RawMessage {
	if kind != string(ActionGrabRelease) {
		redacted := secrets.RedactText(string(raw))
		if !json.Valid([]byte(redacted)) {
			return json.RawMessage(`{"redacted":"[REDACTED invalid action params]"}`)
		}
		return json.RawMessage(redacted)
	}
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil || params == nil {
		return json.RawMessage(`{"redacted":"[REDACTED invalid release params]"}`)
	}
	guid, ok := params["guid"].(string)
	if !ok || guid == "" {
		return json.RawMessage(`{"redacted":"[REDACTED invalid release params]"}`)
	}
	params["guid"] = releaseGUIDForWire(guid)
	safeValue, err := secrets.RedactJSONValue(params)
	if err != nil {
		return json.RawMessage(`{"redacted":"[REDACTED invalid release params]"}`)
	}
	encoded, err := json.Marshal(safeValue)
	if err != nil {
		return json.RawMessage(`{"redacted":"[REDACTED invalid release params]"}`)
	}
	return encoded
}

func releaseGUIDFingerprint(guid string) string {
	digest := sha256.Sum256([]byte(guid))
	return fmt.Sprintf("[REDACTED release sha256:%x]", digest[:8])
}

const releaseGUIDFingerprintPrefix = "[REDACTED release sha256:"

func isReleaseGUIDFingerprint(guid string) bool {
	const digestHexLen = 16
	if len(guid) != len(releaseGUIDFingerprintPrefix)+digestHexLen+1 ||
		!strings.HasPrefix(guid, releaseGUIDFingerprintPrefix) || guid[len(guid)-1] != ']' {
		return false
	}
	for _, char := range guid[len(releaseGUIDFingerprintPrefix) : len(guid)-1] {
		if char < '0' || char > '9' {
			if char < 'a' || char > 'f' {
				return false
			}
		}
	}
	return true
}

// normalizeReleaseGUIDReference is the persistence boundary. Plain/raw release
// capabilities, including partially redacted URLs, become one-way fingerprints.
// The executor can match either hash(raw) or hash(redacted(raw)) against its
// fresh scoped search, so no capability-shaped string needs to be persisted.
func normalizeReleaseGUIDReference(guid string) string {
	if isReleaseGUIDFingerprint(guid) {
		return guid
	}
	return releaseGUIDFingerprint(guid)
}

func releaseGUIDForWire(guid string) string {
	if isReleaseGUIDFingerprint(guid) {
		return guid
	}
	return releaseGUIDFingerprint(guid)
}

func redactedStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	redacted := secrets.RedactText(*value)
	return &redacted
}

// ListActionsResponse is the GET /api/admin/agent-actions result.
type ListActionsResponse struct {
	Actions []AgentAction `json:"actions"`
}

// ActionDecision is the POST .../approve body: an optional params override an
// admin may supply to edit the proposal before it executes.
type ActionDecision struct {
	Override *json.RawMessage `json:"override"`
}

// ActionDenyRequest is the POST .../deny body.
type ActionDenyRequest struct {
	Note string `json:"note"`
}

// AdminIssueResolutionRequest is POST /api/admin/issues/{id}/resolve. Note is
// required human evidence/judgment; it is stored in the terminal issue and its
// append-only thread audit.
type AdminIssueResolutionRequest struct {
	Disposition AdminIssueDisposition `json:"disposition"`
	Note        string                `json:"note"`
}

// AgentRunDetail is the GET /api/admin/agent-runs/{id} audit payload: the run row
// plus its ordered steps.
type AgentRunDetail struct {
	Run   AgentRun    `json:"run"`
	Steps []AgentStep `json:"steps"`
}

// IssueActivity is the durable admin audit surface for one issue. Unlike the
// approval queue it includes terminal actions and runs, so evidence remains
// reachable after a decision or external resolution.
type IssueActivity struct {
	Actions []AgentAction `json:"actions"`
	Runs    []AgentRun    `json:"runs"`
}

// AgentRun is one row of the agent_runs table for the audit view.
type AgentRun struct {
	ID                  int64      `json:"id"`
	IssueID             int64      `json:"issue_id"`
	Trigger             string     `json:"trigger"`
	Status              string     `json:"status"`
	Model               string     `json:"model"`
	StepCount           int        `json:"step_count"`
	InputTokens         int64      `json:"input_tokens"`
	OutputTokens        int64      `json:"output_tokens"`
	CacheCreationTokens int64      `json:"cache_creation_tokens"`
	CacheReadTokens     int64      `json:"cache_read_tokens"`
	CostMicros          int64      `json:"cost_micros"`
	StopReason          *string    `json:"stop_reason"`
	StartedAt           time.Time  `json:"started_at"`
	FinishedAt          *time.Time `json:"finished_at"`
}

// AgentStep is one row of the agent_steps audit ledger for the audit view.
type AgentStep struct {
	ID         int64     `json:"id"`
	Seq        int       `json:"seq"`
	Kind       string    `json:"kind"`
	ToolName   *string   `json:"tool_name"`
	ToolInput  *string   `json:"tool_input"`
	ToolOutput *string   `json:"tool_output"`
	Text       *string   `json:"text"`
	IsError    bool      `json:"is_error"`
	CreatedAt  time.Time `json:"created_at"`
}
