// data_issues.go — domain-local store, shared helpers, and seed data for the
// issues & AI-remediation showcase (issues.go + remediation.go). All content
// references public-domain catalog titles and the frozen instance constants
// from state.go. Domain prefix: iss…; the store is guarded by issMu (never
// stateMu — see contract.md locking rules).
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

// ─── Domain types ───────────────────────────────────────

// issIssue is one reported or auto-detected problem. Fields that render as
// nullable JSON use zero values as the "null" sentinel (ReporterID 0,
// Category "", ClosedAt nil). ProblemKind and ForeignID are internal-only and
// never serialized (ProblemKind backs auto-approval offers and remember).
type issIssue struct {
	ID             int
	Source         string // user | auto | system
	Status         string
	Category       string // "" = null (auto/system issues)
	ReporterID     int    // 0 = null
	ReporterName   string
	TmdbID         int
	MediaType      string // movie | tv | book
	Title          string
	SeasonNumber   int
	EpisodeNumber  int
	Detail         string
	Occurrences    int
	Read           bool
	Resolution     string
	ResolutionKind string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ClosedAt       *time.Time
	InstanceID     string
	IsPrevention   bool   // recurrence notice (real: derived from the dedupe namespace)
	ProblemKind    string // internal: Import-Doctor problem label ("" = none)
	ForeignID      string // internal: book scope for dedupe ("" = not a book)
}

// issMsg is one append-only issue-thread entry.
type issMsg struct {
	ID         int
	AuthorKind string // user | agent | admin | system
	AuthorName string // "" renders null (agent/system)
	Body       string
	CreatedAt  time.Time
}

// issAction is one agent-proposed arr mutation.
type issAction struct {
	ID             int
	IssueID        int
	RunID          int // 0 = null
	Kind           string
	Params         map[string]any
	ApprovedParams map[string]any // nil until approved
	Rationale      string
	Risk           string // "mutating" | "safe" (the app's vocabulary)
	Status         string // proposed | executing | executed | denied | failed | superseded | outcome_unknown
	DecidedBy      int    // 0 = null
	DecidedAt      *time.Time
	DenyReason     string // "" = null
	ExecutedAt     *time.Time
	ResultText     string // "" = null
	CreatedAt      time.Time
	AutoRuleID     int // 0 = null
	AutoApproved   bool
}

// issRun is one agent investigation run.
type issRun struct {
	ID                  int
	IssueID             int
	Trigger             string // user_report | auto
	Status              string // running | succeeded | gave_up | waiting_approval | waiting_user | resume_pending | aborted
	Model               string
	StepCount           int
	InputTokens         int
	OutputTokens        int
	CacheCreationTokens int
	CacheReadTokens     int
	StopReason          string // "" = null
	StartedAt           time.Time
	FinishedAt          *time.Time
}

// issStep is one audit-ledger step of a run.
type issStep struct {
	ID        int
	Seq       int
	Kind      string // assistant | tool_call | tool_result | giveup
	ToolName  *string
	ToolInput *string
	ToolOut   *string
	Text      *string
	IsError   bool
	CreatedAt time.Time
}

// issRule is one standing auto-approval rule.
type issRule struct {
	ID             int
	ProblemKind    string
	ActionKind     string
	ActionFacet    string
	Status         string // active | paused
	PausedReason   string // "" = null
	PausedAt       *time.Time
	CreatedBy      int // 0 = null
	CreatedByName  string
	SeedActionID   int // 0 = null
	ApprovedCount  int
	ResolvedCount  int
	LastApprovedAt *time.Time
	LastResolvedAt *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// issSettingsT is the remediation Settings object (srv-issues §5).
type issSettingsT struct {
	Enabled                  bool
	AutoDispatch             bool
	AllowReporting           bool
	MarkResolvedAsRead       bool
	Mode                     string
	ModelOverride            string
	ModelOverrideProvider    string
	MaxSteps                 int
	MaxTurnTokens            int
	MaxWallClockSecs         int
	DailyRunCap              int
	CircuitBreakerGiveups    int
	MaxUserWaitHours         int
	ObservationMinMinutes    int
	ObservationQuietMinutes  int
	ObservationSettleMinutes int
}

// ─── Domain store (guarded by issMu) ────────────────────

var (
	issMu sync.Mutex

	issIssues  = map[int]*issIssue{}
	issThreads = map[int][]*issMsg{} // issue id -> messages, id ASC
	issActions = map[int]*issAction{}
	issRuns    = map[int]*issRun{}
	issSteps   = map[int][]*issStep{} // run id -> steps, seq ASC
	issRules   = map[int]*issRule{}

	issNextIssueID  = 1
	issNextMsgID    = 1
	issNextActionID = 1
	issNextRunID    = 1
	issNextRuleID   = 1

	// The demo ships with the remediation feature ON and reporting allowed
	// (gap-plan §1.6), numeric limits at the server defaults.
	issSettings = issSettingsT{
		Enabled: true, AutoDispatch: true, AllowReporting: true,
		MarkResolvedAsRead: true, Mode: "supervised",
		MaxSteps: 12, MaxTurnTokens: 4096, MaxWallClockSecs: 300,
		DailyRunCap: 50, CircuitBreakerGiveups: 5, MaxUserWaitHours: 72,
		ObservationMinMinutes: 10, ObservationQuietMinutes: 5,
		ObservationSettleMinutes: 2,
	}
)

// ─── Nullable-JSON helpers ──────────────────────────────

func issNullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func issNullInt(i int) any {
	if i == 0 {
		return nil
	}
	return i
}

func issNullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

func issPtrStr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// ─── Renderers (call with issMu held) ───────────────────

func issLockedIssueJSON(i *issIssue) map[string]any {
	var reporterID, reporterName any
	if i.ReporterID != 0 {
		reporterID = i.ReporterID
		reporterName = i.ReporterName
	}
	return map[string]any{
		"id":              i.ID,
		"source":          i.Source,
		"status":          i.Status,
		"category":        issNullStr(i.Category),
		"reporter_id":     reporterID,
		"reporter_name":   reporterName,
		"tmdb_id":         i.TmdbID,
		"media_type":      i.MediaType,
		"title":           i.Title,
		"season_number":   i.SeasonNumber,
		"episode_number":  i.EpisodeNumber,
		"detail":          i.Detail,
		"occurrences":     i.Occurrences,
		"read":            i.Read,
		"resolution":      i.Resolution,
		"resolution_kind": i.ResolutionKind,
		"created_at":      i.CreatedAt,
		"updated_at":      i.UpdatedAt,
		"closed_at":       issNullTime(i.ClosedAt),
		// can_confirm_fixed is a live per-read answer, deliberately false on
		// every list row; only the single-issue GET overrides it for the
		// reporter (issLockedCanConfirmFixed).
		"can_confirm_fixed": false,
		"is_prevention":     i.IsPrevention,
		"instance_id":       i.InstanceID,
	}
}

// issLockedCanConfirmFixed mirrors the real CanReporterConfirmFix gate: the
// reporter may close their own report as fixed only while it is a still-open
// user report, a fix actually reached the arr (an executed or outcome-unknown
// action with an execution timestamp), and nothing is mid-dispatch. Call with
// issMu held; the caller has already checked the caller IS the reporter.
func issLockedCanConfirmFixed(i *issIssue) bool {
	if i == nil || i.Source != "user" || i.ClosedAt != nil {
		return false
	}
	applied := false
	for _, a := range issActions {
		if a.IssueID != i.ID {
			continue
		}
		if a.Status == "executing" {
			return false
		}
		if (a.Status == "executed" || a.Status == "outcome_unknown") && a.ExecutedAt != nil {
			applied = true
		}
	}
	return applied
}

// issApplyRequesterCopy is the requester-copy boundary: an issue leaving the
// server to a NON-ADMIN reader carries only server-authored requester
// vocabulary in resolution, never admin-facing diagnostics. Every rewritten
// (status, resolution_kind) pair matches the real applyRequesterCopy exactly;
// statuses outside the switch keep their stored resolution.
func issApplyRequesterCopy(m map[string]any, i *issIssue) {
	switch i.Status {
	case "needs_admin":
		m["resolution"] = "An administrator is taking a closer look at this."
	case "awaiting_confirmation":
		m["resolution"] = "A fix was applied — please open the report and confirm whether it's right now."
	case "wont_fix":
		if i.ResolutionKind == "reporter_timeout" {
			m["resolution"] = "Closed after no reply. If this is still a problem, report it again."
		} else {
			m["resolution"] = "This was closed without a fix. If it still looks wrong, report it again."
		}
	case "resolved":
		if i.ResolutionKind == "reporter_confirmed" {
			m["resolution"] = "You confirmed this is fixed."
		} else {
			m["resolution"] = "This was resolved. If it still looks wrong, report it again."
		}
	case "dismissed":
		m["resolution"] = "An administrator closed this report."
	}
}

func issLockedMsgJSON(m *issMsg) map[string]any {
	return map[string]any{
		"id":          m.ID,
		"author_kind": m.AuthorKind,
		"author_name": issNullStr(m.AuthorName),
		"body":        m.Body,
		"created_at":  m.CreatedAt,
	}
}

// issLockedCanDecide computes the server-side decision gate and, when the
// action is not decidable, the fixed blocked_reason copy (srv-issues §2.3).
func issLockedCanDecide(a *issAction) (bool, string) {
	switch a.Status {
	case "superseded":
		return false, "This proposal was superseded and cannot be executed."
	case "proposed":
		// fall through to the live gate below
	default:
		return false, "This proposal has already been decided."
	}
	issue := issIssues[a.IssueID]
	if issue == nil || issue.ClosedAt != nil {
		return false, "The issue is already closed, so this proposal cannot be executed."
	}
	var run *issRun
	if a.RunID != 0 {
		run = issRuns[a.RunID]
	}
	if issue.Status != "awaiting_approval" || run == nil || run.Status != "waiting_approval" {
		return false, "This proposal cannot be decided."
	}
	return true, ""
}

// issActionFacet derives the per-kind auto-approval facet from params.
// ok=false disqualifies the action from rule matching entirely.
func issActionFacet(kind string, params map[string]any) (string, bool) {
	switch kind {
	case "remediate_queue":
		a, _ := params["action"].(string)
		if a == "" {
			return "", false
		}
		return a, true
	case "manual_import":
		if f, _ := params["force"].(bool); f {
			return "force", true
		}
		return "", true
	case "grab_release", "trigger_search", "rescan":
		return "", true
	}
	return "", false
}

// issRuleLabel is the fixed server-authored rule display name:
// "<fix label> · <problem_kind>" (mirrors the real approvalRuleLabel).
func issRuleLabel(problemKind, kind, facet string) string {
	action := kind
	switch kind {
	case "grab_release":
		action = "Grab release"
	case "remediate_queue":
		switch facet {
		case "remove":
			action = "Remove from queue"
		case "blocklist_search":
			action = "Blocklist & re-search"
		case "change_category":
			action = "Change download category"
		default:
			action = "Remediate queue"
		}
	case "manual_import":
		if facet == "force" {
			action = "Manual import (force)"
		} else {
			action = "Manual import"
		}
	case "trigger_search":
		action = "Search again"
	case "rescan":
		action = "Rescan"
	}
	return action + " · " + problemKind
}

// issLockedOffer computes the auto_approval_offer object for a decidable
// proposal whose issue carries a problem label and whose triple is not
// already covered by an active rule; nil when no offer applies.
func issLockedOffer(a *issAction) map[string]any {
	if can, _ := issLockedCanDecide(a); !can {
		return nil
	}
	issue := issIssues[a.IssueID]
	if issue == nil || issue.ProblemKind == "" {
		return nil
	}
	facet, ok := issActionFacet(a.Kind, a.Params)
	if !ok {
		return nil
	}
	reactivates := false
	for _, r := range issRules {
		if r.ProblemKind == issue.ProblemKind && r.ActionKind == a.Kind && r.ActionFacet == facet {
			if r.Status == "active" {
				return nil
			}
			reactivates = true
		}
	}
	return map[string]any{
		"problem_kind":            issue.ProblemKind,
		"action_kind":             a.Kind,
		"action_facet":            facet,
		"label":                   issRuleLabel(issue.ProblemKind, a.Kind, facet),
		"reactivates_paused_rule": reactivates,
	}
}

func issLockedActionJSON(a *issAction) map[string]any {
	issue := issIssues[a.IssueID]
	var (
		issueTitle, issueMediaType, issueStatus string
		issueCategory                           any
		issueClosedAt                           any
		instanceID                              string
	)
	if issue != nil {
		issueTitle = issue.Title
		issueMediaType = issue.MediaType
		issueStatus = issue.Status
		issueCategory = issNullStr(issue.Category)
		issueClosedAt = issNullTime(issue.ClosedAt)
		instanceID = issue.InstanceID
	}
	instanceName, instanceService := "", ""
	if inst := instanceByID(instanceID); inst != nil {
		instanceName = inst.Name
		instanceService = inst.ServiceType
	}
	var approvedParams any
	if a.ApprovedParams != nil {
		approvedParams = a.ApprovedParams
	}
	var autoRuleLabel any
	if a.AutoRuleID != 0 {
		if r := issRules[a.AutoRuleID]; r != nil {
			autoRuleLabel = issRuleLabel(r.ProblemKind, r.ActionKind, r.ActionFacet)
		}
	}
	can, blocked := issLockedCanDecide(a)
	m := map[string]any{
		"id":                    a.ID,
		"issue_id":              a.IssueID,
		"run_id":                issNullInt(a.RunID),
		"kind":                  a.Kind,
		"params":                a.Params,
		"rationale":             a.Rationale,
		"risk":                  a.Risk,
		"status":                a.Status,
		"decided_by":            issNullInt(a.DecidedBy),
		"decided_at":            issNullTime(a.DecidedAt),
		"deny_reason":           issNullStr(a.DenyReason),
		"executed_at":           issNullTime(a.ExecutedAt),
		"result_text":           issNullStr(a.ResultText),
		"created_at":            a.CreatedAt,
		"approved_params":       approvedParams,
		"issue_title":           issueTitle,
		"issue_media_type":      issueMediaType,
		"issue_category":        issueCategory,
		"issue_status":          issueStatus,
		"issue_closed_at":       issueClosedAt,
		"instance_id":           instanceID,
		"instance_name":         instanceName,
		"instance_service_type": instanceService,
		"can_decide":            can,
		"auto_rule_id":          issNullInt(a.AutoRuleID),
		"auto_approved":         a.AutoApproved,
		"auto_rule_label":       autoRuleLabel,
	}
	if blocked != "" {
		m["blocked_reason"] = blocked
	}
	if offer := issLockedOffer(a); offer != nil {
		m["auto_approval_offer"] = offer
	}
	return m
}

func issLockedRunJSON(r *issRun) map[string]any {
	return map[string]any{
		"id":                    r.ID,
		"issue_id":              r.IssueID,
		"trigger":               r.Trigger,
		"status":                r.Status,
		"model":                 r.Model,
		"step_count":            r.StepCount,
		"input_tokens":          r.InputTokens,
		"output_tokens":         r.OutputTokens,
		"cache_creation_tokens": r.CacheCreationTokens,
		"cache_read_tokens":     r.CacheReadTokens,
		"stop_reason":           issNullStr(r.StopReason),
		"started_at":            r.StartedAt,
		"finished_at":           issNullTime(r.FinishedAt),
	}
}

func issLockedStepJSON(s *issStep) map[string]any {
	return map[string]any{
		"id":          s.ID,
		"seq":         s.Seq,
		"kind":        s.Kind,
		"tool_name":   issPtrStr(s.ToolName),
		"tool_input":  issPtrStr(s.ToolInput),
		"tool_output": issPtrStr(s.ToolOut),
		"text":        issPtrStr(s.Text),
		"is_error":    s.IsError,
		"created_at":  s.CreatedAt,
	}
}

func issLockedRuleJSON(r *issRule) map[string]any {
	return map[string]any{
		"id":               r.ID,
		"problem_kind":     r.ProblemKind,
		"action_kind":      r.ActionKind,
		"action_facet":     r.ActionFacet,
		"label":            issRuleLabel(r.ProblemKind, r.ActionKind, r.ActionFacet),
		"status":           r.Status,
		"paused_reason":    issNullStr(r.PausedReason),
		"paused_at":        issNullTime(r.PausedAt),
		"created_by":       issNullInt(r.CreatedBy),
		"created_by_name":  issNullStr(r.CreatedByName),
		"seed_action_id":   issNullInt(r.SeedActionID),
		"approved_count":   r.ApprovedCount,
		"resolved_count":   r.ResolvedCount,
		"last_approved_at": issNullTime(r.LastApprovedAt),
		"last_resolved_at": issNullTime(r.LastResolvedAt),
		"created_at":       r.CreatedAt,
		"updated_at":       r.UpdatedAt,
	}
}

// ─── Counts, thread append (call with issMu held) ───────

// issLockedPendingCount counts genuinely decidable proposals (the badge and
// agent_action_decided pending_count semantics).
func issLockedPendingCount() int {
	n := 0
	for _, a := range issActions {
		if can, _ := issLockedCanDecide(a); can {
			n++
		}
	}
	return n
}

// issLockedOpenCount counts non-closed issues that are neither observing nor
// recovering (the open-issue badge semantics).
func issLockedOpenCount() int {
	n := 0
	for _, i := range issIssues {
		if i.ClosedAt == nil && i.Status != "observing" && i.Status != "recovering" {
			n++
		}
	}
	return n
}

func issLockedAppendMsg(issueID int, authorKind, authorName, body string, at time.Time) {
	m := &issMsg{
		ID: issNextMsgID, AuthorKind: authorKind, AuthorName: authorName,
		Body: body, CreatedAt: at,
	}
	issNextMsgID++
	issThreads[issueID] = append(issThreads[issueID], m)
}

// ─── Body decoding & params validation ──────────────────

// issDecodeJSON decodes one JSON value (max 64 KiB, unknown fields rejected,
// trailing JSON rejected). optional=true tolerates a completely empty body.
func issDecodeJSON(r *http.Request, v any, optional bool) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if optional && errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if dec.More() {
		return errors.New("request body must contain exactly one JSON value")
	}
	return nil
}

// issPInt reads an integral JSON number from a decoded params map.
func issPInt(p map[string]any, key string) (int, bool) {
	switch v := p[key].(type) {
	case int:
		return v, true
	case float64:
		if v == float64(int(v)) {
			return int(v), true
		}
	}
	return 0, false
}

// issValidateParams mirrors the strict per-kind action-params schema the app
// enforces (agent_action_models.dart validationProblem). Returns "" when the
// params are valid, else a human-readable problem.
func issValidateParams(kind string, p map[string]any) string {
	if p == nil {
		return "params must be a JSON object"
	}
	allowed := map[string]map[string]bool{
		"grab_release": {
			"media_type": true, "guid": true, "indexer_id": true,
			"queue_id_to_replace": true, "release_title": true, "quality": true,
			"size": true, "protocol": true, "indexer": true, "rejected": true,
			"rejections": true,
		},
		"remediate_queue": {"media_type": true, "queue_id": true, "action": true},
		"manual_import":   {"media_type": true, "queue_id": true, "force": true},
		"trigger_search": {
			"media_type": true, "tmdb_id": true, "season": true, "episode": true,
			"author_id": true, "book_id": true,
		},
		"rescan": {"media_type": true, "tmdb_id": true, "author_id": true},
	}
	ak, ok := allowed[kind]
	if !ok {
		return "unknown action kind"
	}
	for k := range p {
		if !ak[k] {
			return fmt.Sprintf("unrecognized field %q", k)
		}
	}
	mt, _ := p["media_type"].(string)
	if mt != mediaTypeMovie && mt != mediaTypeTV && mt != mediaTypeBook {
		return "media_type must be movie, tv, or book"
	}
	hasInt := func(key string) bool { _, ok := issPInt(p, key); return ok }
	optInt := func(key string) bool {
		if _, present := p[key]; !present {
			return true
		}
		return hasInt(key)
	}
	str := func(key string) (string, bool) { s, ok := p[key].(string); return s, ok }
	switch kind {
	case "remediate_queue":
		if qid, ok := issPInt(p, "queue_id"); !ok || qid <= 0 {
			return "queue_id must be a positive integer"
		}
		a, _ := str("action")
		if a != "remove" && a != "blocklist_search" && a != "change_category" {
			return "action must be remove, blocklist_search, or change_category"
		}
	case "manual_import":
		if qid, ok := issPInt(p, "queue_id"); !ok || qid <= 0 {
			return "queue_id must be a positive integer"
		}
		if v, present := p["force"]; present {
			if _, ok := v.(bool); !ok {
				return "force must be a boolean"
			}
		}
	case "grab_release":
		if g, ok := str("guid"); !ok || g == "" {
			return "guid is required"
		}
		if idx, ok := issPInt(p, "indexer_id"); !ok || idx <= 0 {
			return "indexer_id must be a positive integer"
		}
		if t, ok := str("release_title"); !ok || t == "" {
			return "release_title is required"
		}
		if sz, ok := issPInt(p, "size"); !ok || sz < 0 {
			return "size must be a non-negative integer"
		}
		if s, ok := str("protocol"); !ok || s == "" {
			return "protocol is required"
		}
		if s, ok := str("indexer"); !ok || s == "" {
			return "indexer is required"
		}
		if v, present := p["quality"]; present {
			if _, ok := v.(string); !ok {
				return "quality must be a string"
			}
		}
		if v, present := p["rejected"]; present {
			if _, ok := v.(bool); !ok {
				return "rejected must be a boolean"
			}
		}
		if v, present := p["rejections"]; present {
			list, ok := v.([]any)
			if !ok {
				return "rejections must be a list of strings"
			}
			for _, e := range list {
				if _, ok := e.(string); !ok {
					return "rejections must be a list of strings"
				}
			}
		}
		if _, present := p["queue_id_to_replace"]; present {
			if q, ok := issPInt(p, "queue_id_to_replace"); !ok || q < 0 {
				return "queue_id_to_replace must be a non-negative integer"
			}
		}
	case "trigger_search":
		for _, k := range []string{"tmdb_id", "season", "episode", "author_id", "book_id"} {
			if !optInt(k) {
				return fmt.Sprintf("%s must be an integer", k)
			}
		}
		if mt == mediaTypeBook {
			aid, _ := issPInt(p, "author_id")
			bid, _ := issPInt(p, "book_id")
			if aid <= 0 && bid <= 0 {
				return "author_id or book_id is required for a book search"
			}
			if tid, _ := issPInt(p, "tmdb_id"); tid != 0 {
				return "a book search must not carry a tmdb_id"
			}
			if _, present := p["season"]; present {
				return "a book search must not carry a season"
			}
			if _, present := p["episode"]; present {
				return "a book search must not carry an episode"
			}
		} else {
			if tid, _ := issPInt(p, "tmdb_id"); tid <= 0 {
				return "tmdb_id is required"
			}
			if mt == mediaTypeMovie {
				if _, present := p["season"]; present {
					return "a movie search must not carry a season"
				}
				if _, present := p["episode"]; present {
					return "a movie search must not carry an episode"
				}
			}
			if _, present := p["season"]; present {
				if s, _ := issPInt(p, "season"); s < 0 {
					return "season must be non-negative"
				}
			}
			if _, present := p["episode"]; present {
				ep, _ := issPInt(p, "episode")
				if ep <= 0 {
					return "episode must be a positive integer"
				}
				if _, present := p["season"]; !present {
					return "an episode search requires a season"
				}
			}
		}
	case "rescan":
		for _, k := range []string{"tmdb_id", "author_id"} {
			if !optInt(k) {
				return fmt.Sprintf("%s must be an integer", k)
			}
		}
		if mt == mediaTypeBook {
			if aid, _ := issPInt(p, "author_id"); aid <= 0 {
				return "author_id is required for a book rescan"
			}
			if tid, _ := issPInt(p, "tmdb_id"); tid != 0 {
				return "a book rescan must not carry a tmdb_id"
			}
		} else if tid, _ := issPInt(p, "tmdb_id"); tid <= 0 {
			return "tmdb_id is required"
		}
	}
	return ""
}

// ─── Sorted views (call with issMu held) ────────────────

func issLockedIssuesSorted() []*issIssue {
	out := make([]*issIssue, 0, len(issIssues))
	for _, i := range issIssues {
		out = append(out, i)
	}
	sort.Slice(out, func(a, b int) bool {
		if !out[a].UpdatedAt.Equal(out[b].UpdatedAt) {
			return out[a].UpdatedAt.After(out[b].UpdatedAt)
		}
		return out[a].ID > out[b].ID
	})
	return out
}

func issLockedActionsSorted() []*issAction {
	out := make([]*issAction, 0, len(issActions))
	for _, a := range issActions {
		out = append(out, a)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID > out[b].ID })
	return out
}

func issLockedRulesSorted() []*issRule {
	out := make([]*issRule, 0, len(issRules))
	for _, r := range issRules {
		out = append(out, r)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID > out[b].ID })
	return out
}

// ─── Seed data (gap-plan dataset item 3) ────────────────

func init() {
	now := time.Now().UTC()
	tp := func(t time.Time) *time.Time { return &t }

	// (c) Resolved user-reported issue with a full mixed thread —
	// Night of the Living Dead (tmdb 10331, PD film), Radarr.
	resolvedCreated := now.Add(-72 * time.Hour)
	resolvedClosed := now.Add(-70 * time.Hour)
	issIssues[1] = &issIssue{
		ID: 1, Source: "user", Status: "resolved", Category: "bad_copy",
		ReporterID: 2, ReporterName: "user",
		TmdbID: 10331, MediaType: mediaTypeMovie, Title: "Night of the Living Dead",
		Detail:      "The copy that downloaded is unwatchable — the video freezes at 00:12 and the audio keeps crackling after that.",
		Occurrences: 1, Read: true,
		Resolution:     "The corrupt copy was blocklisted and replaced with a verified 1080p remux, which imported cleanly.",
		ResolutionKind: "agent_concluded",
		CreatedAt:      resolvedCreated, UpdatedAt: resolvedClosed,
		ClosedAt: tp(resolvedClosed), InstanceID: instRadarr,
	}
	issThreads[1] = []*issMsg{
		{ID: 1, AuthorKind: "user", AuthorName: "user",
			Body:      "The copy that downloaded is unwatchable — the video freezes at 00:12 and the audio keeps crackling after that.",
			CreatedAt: resolvedCreated},
		{ID: 2, AuthorKind: "agent",
			Body:      "Thanks for the report. I checked the Radarr history for Night of the Living Dead: the release that imported logged a verification warning at import time, which fits the playback problem you describe. I'm looking for a better copy now.",
			CreatedAt: resolvedCreated.Add(2 * time.Minute)},
		{ID: 3, AuthorKind: "agent",
			Body:      "One quick question before I replace the file: does the playback problem happen on every device, or only on one player?",
			CreatedAt: resolvedCreated.Add(4 * time.Minute)},
		{ID: 4, AuthorKind: "user", AuthorName: "user",
			Body:      "Every device — I tried the TV and my phone, same freeze both times.",
			CreatedAt: resolvedCreated.Add(30 * time.Minute)},
		{ID: 5, AuthorKind: "agent",
			Body:      "That points to a bad source file rather than a player issue. I found a higher-scoring replacement release on Nebula Indexer and proposed grabbing it; an administrator needs to approve the change before anything runs.",
			CreatedAt: resolvedCreated.Add(33 * time.Minute)},
		{ID: 6, AuthorKind: "admin", AuthorName: "admin",
			Body:      "Approved — grab the remux and blocklist the bad release.",
			CreatedAt: resolvedCreated.Add(55 * time.Minute)},
		{ID: 7, AuthorKind: "agent",
			Body:      "Approved and executed: grabbed Night.of.the.Living.Dead.1968.1080p.BluRay.Remux from Nebula Indexer and blocklisted the corrupt release. I'll confirm once the import completes.",
			CreatedAt: resolvedCreated.Add(56 * time.Minute)},
		{ID: 8, AuthorKind: "agent",
			Body:      "The replacement imported cleanly and passed verification. Marking this resolved — thanks for flagging it.",
			CreatedAt: resolvedClosed},
		{ID: 9, AuthorKind: "system",
			Body:      "Issue resolved: the corrupt copy was replaced and the new file verified.",
			CreatedAt: resolvedClosed},
	}
	issRuns[1] = &issRun{
		ID: 1, IssueID: 1, Trigger: "user_report", Status: "succeeded",
		Model: "claude-sonnet-4-5", StepCount: 6,
		InputTokens: 18420, OutputTokens: 2210,
		CacheCreationTokens: 5120, CacheReadTokens: 13300,
		StopReason: "resolved",
		StartedAt:  resolvedCreated.Add(1 * time.Minute), FinishedAt: tp(resolvedClosed),
	}
	issSteps[1] = []*issStep{
		{ID: 1, Seq: 1, Kind: "assistant",
			Text:      strPtr("A bad-copy report came in for Night of the Living Dead. I'll start with the Radarr history for the imported file."),
			CreatedAt: resolvedCreated.Add(1 * time.Minute)},
		{ID: 2, Seq: 2, Kind: "tool_call", ToolName: strPtr("get_history"),
			ToolInput: strPtr(`{"instance_id":"` + instRadarr + `","media_type":"movie","tmdb_id":10331}`),
			CreatedAt: resolvedCreated.Add(1*time.Minute + 10*time.Second)},
		{ID: 3, Seq: 3, Kind: "tool_result", ToolName: strPtr("get_history"),
			ToolOut:   strPtr(`{"records":[{"eventType":"downloadFolderImported","sourceTitle":"Night.of.the.Living.Dead.1968.720p.WEB.x264","data":{"fileId":"8801","verificationWarning":"container reported truncated stream"}}]}`),
			CreatedAt: resolvedCreated.Add(1*time.Minute + 12*time.Second)},
		{ID: 4, Seq: 4, Kind: "tool_call", ToolName: strPtr("search_releases"),
			ToolInput: strPtr(`{"instance_id":"` + instRadarr + `","media_type":"movie","tmdb_id":10331}`),
			CreatedAt: resolvedCreated.Add(31 * time.Minute)},
		{ID: 5, Seq: 5, Kind: "tool_result", ToolName: strPtr("search_releases"),
			ToolOut:   strPtr(`{"releases":[{"title":"Night.of.the.Living.Dead.1968.1080p.BluRay.Remux.AVC.FLAC.1.0","indexer":"Nebula Indexer","size":18763229184,"protocol":"usenet","score":97}]}`),
			CreatedAt: resolvedCreated.Add(31*time.Minute + 4*time.Second)},
		{ID: 6, Seq: 6, Kind: "assistant",
			Text:      strPtr("The imported file logged a truncated-stream warning and the reporter confirmed the freeze on every device. Proposing to grab the 1080p remux and blocklist the bad release."),
			CreatedAt: resolvedCreated.Add(32 * time.Minute)},
	}
	// (f) grab_release params carry the one-way [REDACTED release sha256:…]
	// fingerprint — raw indexer GUIDs are never served.
	grabParams := map[string]any{
		"media_type":          mediaTypeMovie,
		"guid":                "[REDACTED release sha256:4be71c09d2a58f36]",
		"indexer_id":          2,
		"queue_id_to_replace": 9101,
		"release_title":       "Night.of.the.Living.Dead.1968.1080p.BluRay.Remux.AVC.FLAC.1.0",
		"quality":             "Remux-1080p",
		"size":                18763229184,
		"protocol":            "usenet",
		"indexer":             "Nebula Indexer",
		"rejected":            false,
		"rejections":          []string{},
	}
	issActions[1] = &issAction{
		ID: 1, IssueID: 1, RunID: 1, Kind: "grab_release",
		Params: grabParams, ApprovedParams: grabParams,
		Rationale: "The imported file logged a truncated-stream verification warning and the reporter confirmed playback fails on every device. Grabbing the 1080p remux from Nebula Indexer and blocklisting the corrupt release replaces it with a verified copy.",
		Risk:      "mutating", Status: "executed",
		DecidedBy: 1, DecidedAt: tp(resolvedCreated.Add(55 * time.Minute)),
		ExecutedAt: tp(resolvedCreated.Add(56 * time.Minute)),
		ResultText: "Grabbed Night.of.the.Living.Dead.1968.1080p.BluRay.Remux.AVC.FLAC.1.0 from Nebula Indexer and blocklisted the previous release. The import completed and the file verified.",
		CreatedAt:  resolvedCreated.Add(33 * time.Minute),
	}

	// (a)+(b) User-reported issue awaiting approval with a decidable proposal,
	// its waiting_approval run, and full assistant/tool_call/tool_result steps
	// — Metropolis (tmdb 19, PD film), Radarr, stalled download. The issue was
	// auto-detected first and adopted by the user report, so it carries the
	// Import-Doctor problem label that powers the auto-approval offer.
	stalledCreated := now.Add(-4 * time.Hour)
	stalledProposed := now.Add(-3*time.Hour - 55*time.Minute)
	issIssues[2] = &issIssue{
		ID: 2, Source: "user", Status: "awaiting_approval", Category: "other",
		ReporterID: 2, ReporterName: "user",
		TmdbID: 19, MediaType: mediaTypeMovie, Title: "Metropolis",
		Detail:      "My request for Metropolis has been stuck at 62% for two days now.",
		Occurrences: 1, Read: false,
		CreatedAt: stalledCreated, UpdatedAt: stalledProposed,
		InstanceID: instRadarr, ProblemKind: "Download stalled",
	}
	issThreads[2] = []*issMsg{
		{ID: 10, AuthorKind: "user", AuthorName: "user",
			Body:      "My request for Metropolis has been stuck at 62% for two days now.",
			CreatedAt: stalledCreated},
		{ID: 11, AuthorKind: "agent",
			Body:      "I can see the download in the queue — it has made no progress in 41 hours and the client reports zero active connections. Checking whether a healthier release is available before proposing a fix.",
			CreatedAt: stalledCreated.Add(2 * time.Minute)},
		{ID: 12, AuthorKind: "agent",
			Body:      "The release has stalled with no seeders and won't recover on its own. I've proposed removing it from the queue, blocklisting the release, and searching for a replacement. An administrator has been asked to approve this fix.",
			CreatedAt: stalledProposed},
	}
	issRuns[2] = &issRun{
		ID: 2, IssueID: 2, Trigger: "user_report", Status: "waiting_approval",
		Model: "claude-sonnet-4-5", StepCount: 7,
		InputTokens: 15040, OutputTokens: 1830,
		CacheCreationTokens: 4010, CacheReadTokens: 11020,
		StopReason: "awaiting_approval",
		StartedAt:  stalledCreated.Add(1 * time.Minute),
	}
	issSteps[2] = []*issStep{
		{ID: 7, Seq: 1, Kind: "assistant",
			Text:      strPtr("A stalled download was reported for Metropolis. I'll start by inspecting the Radarr queue."),
			CreatedAt: stalledCreated.Add(1 * time.Minute)},
		{ID: 8, Seq: 2, Kind: "tool_call", ToolName: strPtr("get_queue"),
			ToolInput: strPtr(`{"instance_id":"` + instRadarr + `","media_type":"movie"}`),
			CreatedAt: stalledCreated.Add(1*time.Minute + 8*time.Second)},
		{ID: 9, Seq: 3, Kind: "tool_result", ToolName: strPtr("get_queue"),
			ToolOut:   strPtr(`{"records":[{"id":4211,"title":"Metropolis.1927.1080p.BluRay.x264","status":"warning","trackedDownloadStatus":"error","errorMessage":"The download is stalled with no connections","sizeleft":6442450944,"timeleft":"00:00:00"}]}`),
			CreatedAt: stalledCreated.Add(1*time.Minute + 11*time.Second)},
		{ID: 10, Seq: 4, Kind: "assistant",
			Text:      strPtr("Queue item 4211 is stalled with no connections and zero progress for 41 hours. I'll check whether replacement releases are available before proposing removal."),
			CreatedAt: stalledCreated.Add(2 * time.Minute)},
		{ID: 11, Seq: 5, Kind: "tool_call", ToolName: strPtr("search_releases"),
			ToolInput: strPtr(`{"instance_id":"` + instRadarr + `","media_type":"movie","tmdb_id":19}`),
			CreatedAt: stalledCreated.Add(2*time.Minute + 20*time.Second)},
		{ID: 12, Seq: 6, Kind: "tool_result", ToolName: strPtr("search_releases"),
			ToolOut:   strPtr(`{"releases":[{"title":"Metropolis.1927.1080p.BluRay.x265.RESTORED","indexer":"Nebula Indexer","seeders":44,"score":95},{"title":"Metropolis.1927.The.Complete.1080p","indexer":"Aurora NZB","score":91},{"title":"Metropolis.1927.720p.BluRay","indexer":"Nebula Indexer","seeders":18,"score":80}]}`),
			CreatedAt: stalledCreated.Add(2*time.Minute + 24*time.Second)},
		{ID: 13, Seq: 7, Kind: "assistant",
			Text:      strPtr("Three healthier releases are available. Proposing to remove and blocklist the stalled release and trigger a fresh search; this needs admin approval."),
			CreatedAt: stalledProposed},
	}
	// The decidable proposal: strict remediate_queue params, matching Radarr
	// instance triple, live approval gate, and an auto-approval offer.
	issActions[2] = &issAction{
		ID: 2, IssueID: 2, RunID: 2, Kind: "remediate_queue",
		Params: map[string]any{
			"media_type": mediaTypeMovie,
			"queue_id":   4211,
			"action":     "blocklist_search",
		},
		Rationale: "The download for Metropolis (1927) has been stalled at 62% for 41 hours with zero active connections, so it will not recover on its own. Removing it, blocklisting the release, and triggering a new search is the standard recovery — three healthier releases are available from the same indexers.",
		Risk:      "mutating", Status: "proposed",
		CreatedAt: stalledProposed,
	}

	// (d) One observing auto-detected issue — Sherlock Holmes Adventures
	// (fictional show, tmdb 90001), Sonarr, waiting to import. Passive
	// tracking: no category, no reporter, stays read.
	observingCreated := now.Add(-22 * time.Minute)
	issIssues[3] = &issIssue{
		ID: 3, Source: "auto", Status: "observing",
		TmdbID: 90001, MediaType: mediaTypeTV, Title: "Sherlock Holmes Adventures",
		SeasonNumber: 2, EpisodeNumber: 4,
		Detail:      "The download finished but hasn't been imported yet — the service hasn't run its import pass.",
		Occurrences: 1, Read: true,
		CreatedAt: observingCreated, UpdatedAt: now.Add(-8 * time.Minute),
		InstanceID: instSonarr, ProblemKind: "Waiting to import",
	}

	// (e) One active + one paused standing auto-approval rule.
	rule1Created := now.Add(-20 * 24 * time.Hour)
	issRules[1] = &issRule{
		ID: 1, ProblemKind: "Download failed", ActionKind: "remediate_queue",
		ActionFacet: "blocklist_search", Status: "active",
		CreatedBy: 1, CreatedByName: "admin",
		ApprovedCount: 4, ResolvedCount: 4,
		LastApprovedAt: tp(now.Add(-48 * time.Hour)),
		LastResolvedAt: tp(now.Add(-47 * time.Hour)),
		CreatedAt:      rule1Created, UpdatedAt: now.Add(-47 * time.Hour),
	}
	rule2Created := now.Add(-15 * 24 * time.Hour)
	rule2Paused := now.Add(-5 * 24 * time.Hour)
	issRules[2] = &issRule{
		ID: 2, ProblemKind: "Waiting to import", ActionKind: "manual_import",
		ActionFacet: "", Status: "paused",
		PausedReason: "An auto-approved fix ended with an unverified outcome. Verify the arr state before re-arming this rule.",
		PausedAt:     tp(rule2Paused),
		CreatedBy:    1, CreatedByName: "admin",
		ApprovedCount: 2, ResolvedCount: 1,
		LastApprovedAt: tp(rule2Paused),
		CreatedAt:      rule2Created, UpdatedAt: rule2Paused,
	}

	// (g) User-reported issue parked at awaiting_confirmation with its EXECUTED
	// fix — His Girl Friday (tmdb 3085, PD film), Radarr, wrong audio language.
	// The fix reached the arr and nothing is mid-dispatch, so the reporter's
	// single-issue GET answers can_confirm_fixed=true and the confirm-fixed
	// flow is demonstrable end to end. The wrong-audio complaint arrived after
	// the queue was empty, so the agent diagnosed it from history, not queue
	// state.
	confirmCreated := now.Add(-26 * time.Hour)
	confirmParked := now.Add(-24 * time.Hour)
	issIssues[4] = &issIssue{
		ID: 4, Source: "user", Status: "awaiting_confirmation", Category: "wrong_audio",
		ReporterID: 2, ReporterName: "user",
		TmdbID: 3085, MediaType: mediaTypeMovie, Title: "His Girl Friday",
		Detail: "The copy of His Girl Friday that came in only has a French audio track — there's no English at all.",
		// The park clears resolution/resolution_kind and leaves the issue READ:
		// this state pages the reporter, never the admin queue.
		Occurrences: 1, Read: true,
		CreatedAt: confirmCreated, UpdatedAt: confirmParked,
		InstanceID: instRadarr,
	}
	issThreads[4] = []*issMsg{
		{ID: 13, AuthorKind: "user", AuthorName: "user",
			Body:      "The copy of His Girl Friday that came in only has a French audio track — there's no English at all.",
			CreatedAt: confirmCreated},
		{ID: 14, AuthorKind: "agent",
			Body:      "Thanks for the report. The queue is empty — this file already imported — so I checked the Radarr history for His Girl Friday: the release that imported is tagged FRENCH and its media info lists a single French audio stream, which matches what you're hearing. I'm looking for an English-audio copy now.",
			CreatedAt: confirmCreated.Add(2 * time.Minute)},
		{ID: 15, AuthorKind: "agent",
			Body:      "Approved and executed: grabbed His.Girl.Friday.1940.1080p.BluRay.x264.ENG from Aurora NZB and blocklisted the French-audio release. I'll follow up once the import completes.",
			CreatedAt: confirmCreated.Add(56 * time.Minute)},
		{ID: 16, AuthorKind: "agent",
			Body:      "I applied the approved fix. Whether it's right now is your call rather than something I can prove — have a look, and tap \"This is fixed\" if the content is what you expected. If it still isn't, reply and tell me what you see.",
			CreatedAt: confirmParked},
	}
	issRuns[3] = &issRun{
		ID: 3, IssueID: 4, Trigger: "user_report", Status: "gave_up",
		Model: "claude-sonnet-4-5", StepCount: 8,
		InputTokens: 21360, OutputTokens: 2540,
		CacheCreationTokens: 5410, CacheReadTokens: 15230,
		StopReason: "unverified_conclusion",
		StartedAt:  confirmCreated.Add(1 * time.Minute), FinishedAt: tp(confirmParked),
	}
	issSteps[3] = []*issStep{
		{ID: 14, Seq: 1, Kind: "assistant",
			Text:      strPtr("A wrong-audio report came in for His Girl Friday. The queue is empty — the file already imported — so I'll start with the Radarr history for the imported release."),
			CreatedAt: confirmCreated.Add(1 * time.Minute)},
		{ID: 15, Seq: 2, Kind: "tool_call", ToolName: strPtr("get_history"),
			ToolInput: strPtr(`{"instance_id":"` + instRadarr + `","media_type":"movie","tmdb_id":3085}`),
			CreatedAt: confirmCreated.Add(1*time.Minute + 9*time.Second)},
		{ID: 16, Seq: 3, Kind: "tool_result", ToolName: strPtr("get_history"),
			ToolOut:   strPtr(`{"records":[{"eventType":"downloadFolderImported","sourceTitle":"His.Girl.Friday.1940.1080p.WEBRip.FRENCH.x264","data":{"fileId":"8802","audioLanguages":"fre"}}]}`),
			CreatedAt: confirmCreated.Add(1*time.Minute + 12*time.Second)},
		{ID: 17, Seq: 4, Kind: "tool_call", ToolName: strPtr("search_releases"),
			ToolInput: strPtr(`{"instance_id":"` + instRadarr + `","media_type":"movie","tmdb_id":3085}`),
			CreatedAt: confirmCreated.Add(3 * time.Minute)},
		{ID: 18, Seq: 5, Kind: "tool_result", ToolName: strPtr("search_releases"),
			ToolOut:   strPtr(`{"releases":[{"title":"His.Girl.Friday.1940.1080p.BluRay.x264.ENG","indexer":"Aurora NZB","size":9663676416,"protocol":"usenet","score":94}]}`),
			CreatedAt: confirmCreated.Add(3*time.Minute + 4*time.Second)},
		{ID: 19, Seq: 6, Kind: "assistant",
			Text:      strPtr("The imported release is a French-audio encode. Proposing to grab the English-audio Blu-ray from Aurora NZB and blocklist the French release; this needs admin approval."),
			CreatedAt: confirmCreated.Add(6 * time.Minute)},
		{ID: 20, Seq: 7, Kind: "assistant",
			Text:      strPtr("The approved grab executed and the English-audio release imported cleanly, replacing the French file. Whether the audio is right now is the reporter's judgment, not something I can prove — parking this for their confirmation."),
			CreatedAt: confirmParked.Add(-1 * time.Minute)},
		{ID: 21, Seq: 8, Kind: "giveup",
			Text:      strPtr("awaiting reporter confirmation: unverified_conclusion"),
			CreatedAt: confirmParked},
	}
	hgfParams := map[string]any{
		"media_type":    mediaTypeMovie,
		"guid":          "[REDACTED release sha256:9d21f4c7a80be5d2]",
		"indexer_id":    3,
		"release_title": "His.Girl.Friday.1940.1080p.BluRay.x264.ENG",
		"quality":       "Bluray-1080p",
		"size":          9663676416,
		"protocol":      "usenet",
		"indexer":       "Aurora NZB",
		"rejected":      false,
		"rejections":    []string{},
	}
	issActions[3] = &issAction{
		ID: 3, IssueID: 4, RunID: 3, Kind: "grab_release",
		Params: hgfParams, ApprovedParams: hgfParams,
		Rationale: "The imported release of His Girl Friday carries a single French audio stream, so the reporter has no English track at all. Grabbing the English-audio 1080p Blu-ray from Aurora NZB and blocklisting the French release replaces the file with one in the expected language.",
		Risk:      "mutating", Status: "executed",
		DecidedBy: 1, DecidedAt: tp(confirmCreated.Add(55 * time.Minute)),
		ExecutedAt: tp(confirmCreated.Add(56 * time.Minute)),
		ResultText: "Grabbed His.Girl.Friday.1940.1080p.BluRay.x264.ENG from Aurora NZB and blocklisted the French-audio release. The import completed.",
		CreatedAt:  confirmCreated.Add(6 * time.Minute),
	}

	// (h) One waiting system issue — the Chaptarr author-import park. A user
	// requested "The Glass Harbor" (invented novel by the invented author
	// Marian Ashwood) and Chaptarr's metadata service is still importing the
	// author, so the request sits parked past the stall horizon. The server
	// watches the import and resolves this itself: passive tracking, no admin
	// verbs, mirroring RecordBookImportStall's exact row and copy (source
	// system, media_type system, unread, refreshed each sweep pass).
	waitingCreated := now.Add(-30 * time.Hour)
	waitingDetail := `Chaptarr has book requests parked for more than a day because its metadata service is still importing their authors. Waiting: "The Glass Harbor". The instance retries the import on its own schedule; Cantinarr watches it and completes the requests automatically when it lands, or hands them to the approval queue if the import fails or is cancelled.`
	waitingResolution := "Check the Chaptarr instance's queued author imports (System page / logs). Nothing is required on the Cantinarr side; this issue resolves itself when the imports land."
	issIssues[5] = &issIssue{
		ID: 5, Source: "system", Status: "waiting",
		MediaType:   "system",
		Title:       "Book requests are waiting on Chaptarr's metadata import",
		Detail:      waitingDetail,
		Occurrences: 3, Read: false,
		Resolution: waitingResolution,
		CreatedAt:  waitingCreated, UpdatedAt: now.Add(-1 * time.Hour),
		InstanceID: instChaptarr,
	}
	issThreads[5] = []*issMsg{
		{ID: 17, AuthorKind: "system",
			Body:      waitingDetail + " " + waitingResolution,
			CreatedAt: waitingCreated},
	}

	issNextIssueID = 6
	issNextMsgID = 18
	issNextActionID = 4
	issNextRunID = 4
	issNextRuleID = 3
}
