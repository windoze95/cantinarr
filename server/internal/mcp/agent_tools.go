package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/windoze95/cantinarr-server/internal/auth"
)

// Agent-only MCP tools. These are NEVER added to toolDefinitions /
// arrToolDefinitions: the chat tool surface (GetToolsForRole) never offers them.
// They exist solely as mcp.Tool values the remediation Runner hands to a single
// model turn, plus ExecuteAgentTool, the dispatch the Runner calls. None of them
// touch Radarr/Sonarr — they only write issue rows via IssueStore — so they are
// safe even though they are not behind the read-tool allow-list (they ARE on the
// Runner's allow-list as the two non-read entries).
//
// Wave 2 shipped report a finding (post_issue_message) and finish
// (conclude_issue). Wave 3 adds propose_action: the agent's ONLY way to effect a
// change. It records an admin-approvable proposal; the server replays it on
// approval. The agent still has no mutation tool of its own.

// Agent-only tool names. Exported so the Runner's allow-list can name them.
const (
	ToolPostIssueMessage = "post_issue_message"
	ToolConcludeIssue    = "conclude_issue"
	ToolProposeAction    = "propose_action"
)

// Proposable action kinds accepted by propose_action. These mirror
// remediation.ActionKind (kept as plain strings here so internal/mcp does not
// import internal/remediation). The authoritative per-kind param validation runs
// in the IssueStore (remediation.Service.ProposeAction); the tool only checks the
// kind is one of these and that params is a JSON object.
const (
	ActionKindGrabRelease    = "grab_release"
	ActionKindRemediateQueue = "remediate_queue"
	ActionKindManualImport   = "manual_import"
	ActionKindTriggerSearch  = "trigger_search"
	ActionKindRescan         = "rescan"
)

// isProposableKind reports whether k is one of the known action kinds.
func isProposableKind(k string) bool {
	switch k {
	case ActionKindGrabRelease, ActionKindRemediateQueue, ActionKindManualImport, ActionKindTriggerSearch, ActionKindRescan:
		return true
	}
	return false
}

// Conclusion statuses accepted by conclude_issue (terminal issue states the
// read-only agent may set). Anything else is rejected by the dispatch.
const (
	ConcludeResolved = "resolved"
	ConcludeWontFix  = "wont_fix"
)

// AgentToolPostIssueMessage posts a plain-language finding/diagnosis to the issue
// thread. The body the model writes is data, not a command.
var AgentToolPostIssueMessage = Tool{
	Name:       ToolPostIssueMessage,
	Permission: auth.PermissionRemediationManage,
	Description: "Post a plain-language message to the issue thread the reporter and admins can read. " +
		"Use this to report what you found and your diagnosis in clear, non-technical language. " +
		"You have NO mutation tools: you can only investigate and report.",
	InputSchema: map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"issue_id": map[string]interface{}{
				"type":        "integer",
				"description": "The id of the issue being investigated.",
			},
			"body": map[string]interface{}{
				"type":        "string",
				"description": "The message to post (plain language; the reporter will read it).",
			},
		},
		"required": []string{"issue_id", "body"},
	},
}

// AgentToolConcludeIssue closes the investigation with a terminal disposition.
var AgentToolConcludeIssue = Tool{
	Name:       ToolConcludeIssue,
	Permission: auth.PermissionRemediationManage,
	Description: "Finish the investigation by setting a terminal status. Use 'resolved' when nothing further is " +
		"needed, or 'wont_fix' when the issue cannot be fixed read-only (always include a short plain-language " +
		"resolution explaining why). Call this exactly once, after you have posted your diagnosis.",
	InputSchema: map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"issue_id": map[string]interface{}{
				"type":        "integer",
				"description": "The id of the issue being investigated.",
			},
			"status": map[string]interface{}{
				"type":        "string",
				"enum":        []string{ConcludeResolved, ConcludeWontFix},
				"description": "Terminal disposition: resolved or wont_fix.",
			},
			"resolution": map[string]interface{}{
				"type":        "string",
				"description": "Short plain-language closing note.",
			},
		},
		"required": []string{"issue_id", "status", "resolution"},
	},
}

// AgentToolProposeAction lets the agent propose a consequential arr mutation for
// an admin to approve. It is the agent's ONLY route to a change: it records a
// proposal; the server replays the stored params verbatim ONLY after an admin
// approves. The model never executes the mutation and never touches the params
// again. The params shape depends on kind (validated server-side).
var AgentToolProposeAction = Tool{
	Name:       ToolProposeAction,
	Permission: auth.PermissionRemediationManage,
	Description: "Propose a fix for an admin to approve. This is the ONLY way you can change anything: you record a " +
		"proposal and an admin must approve it before the server carries it out. You never perform the change yourself. " +
		"After you propose, the investigation pauses until the admin decides, then resumes so you can verify the result " +
		"(on approval) or try another approach (on denial). Choose a 'kind' and supply its 'params':\n" +
		"- grab_release: download a specific release. params: {media_type, guid, indexer_id, queue_id_to_replace?} " +
		"(guid + indexer_id come from search_releases; set queue_id_to_replace to swap out a current queue item).\n" +
		"- remediate_queue: act on a stuck queue item. params: {media_type, queue_id, action} where action is " +
		"\"remove\", \"blocklist_search\" (remove + blocklist + re-search), or \"change_category\".\n" +
		"- manual_import: import a download's files. params: {media_type, queue_id, force} (force imports despite " +
		"permanent rejections — only when a rejection is known-safe/temporary).\n" +
		"- trigger_search: start an automatic search. params: {media_type, tmdb_id, season?}.\n" +
		"- rescan: rescan the media on disk and run the import pass. params: {media_type, tmdb_id}.\n" +
		"Always include a clear 'rationale' explaining why this fix is correct (the admin reads it).",
	InputSchema: map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"issue_id": map[string]interface{}{
				"type":        "integer",
				"description": "The id of the issue being investigated.",
			},
			"kind": map[string]interface{}{
				"type":        "string",
				"enum":        []string{ActionKindGrabRelease, ActionKindRemediateQueue, ActionKindManualImport, ActionKindTriggerSearch, ActionKindRescan},
				"description": "Which kind of fix to propose.",
			},
			"params": map[string]interface{}{
				"type":        "object",
				"description": "The typed arguments for this kind (see the description). Stored verbatim and replayed on approval.",
			},
			"rationale": map[string]interface{}{
				"type":        "string",
				"description": "Plain-language justification the admin will read before approving.",
			},
		},
		"required": []string{"issue_id", "kind", "params", "rationale"},
	},
}

// AgentTools returns the agent-only tool definitions, for the Runner to add to
// its allow-list and hand to a model turn.
func AgentTools() []Tool {
	return []Tool{AgentToolPostIssueMessage, AgentToolConcludeIssue, AgentToolProposeAction}
}

// IsAgentTool reports whether a name is one of the agent-only tools.
func IsAgentTool(name string) bool {
	switch name {
	case ToolPostIssueMessage, ToolConcludeIssue, ToolProposeAction:
		return true
	}
	return false
}

// ToolsByName returns the tool definitions for the given names, preserving the
// order of names and skipping any unknown name. The remediation Runner uses this
// to materialize its hardcoded read-tool allow-list into the mcp.Tool values it
// hands to a single model turn — so the model only ever sees those read tools
// (plus the agent-only tools), never a mutating one.
func (s *ToolServer) ToolsByName(names []string) []Tool {
	out := make([]Tool, 0, len(names))
	for _, n := range names {
		if def := findToolDefinition(n); def != nil {
			out = append(out, *def)
		}
	}
	return out
}

// AgentToolResult is the structured outcome of an agent-only tool call the Runner
// needs to drive its loop: Concluded signals the investigation should terminate;
// Parked signals it should pause for a human (an admin approval) after this turn.
type AgentToolResult struct {
	Text      string
	Concluded bool
	Status    string // terminal issue status when Concluded
	Parked    bool   // true after a propose_action: the run pauses for admin approval
}

// ExecuteAgentTool dispatches an agent-only tool. The Runner passes the
// authoritative issueID (the issue it CAS-claimed) and the tool_use.id of the
// call; the model-supplied issue_id in the input is IGNORED for routing, so a
// hijacked model cannot post to, close, or propose against a different issue.
// Every tool early-returns a benign result when remediation is disabled or no
// IssueStore is wired.
func (s *ToolServer) ExecuteAgentTool(ctx context.Context, name string, input json.RawMessage, issueID int64, toolUseID string) (*AgentToolResult, error) {
	if s.issueStore == nil || !s.issueStore.RemediationEnabled(ctx) {
		return &AgentToolResult{Text: "Remediation is not enabled; this tool did nothing."}, nil
	}

	switch name {
	case ToolPostIssueMessage:
		var args struct {
			Body string `json:"body"`
		}
		if err := json.Unmarshal(nonEmptyJSON(input), &args); err != nil {
			return nil, fmt.Errorf("invalid %s input: %w", name, err)
		}
		if args.Body == "" {
			return &AgentToolResult{Text: "No message body provided; nothing posted."}, nil
		}
		if err := s.issueStore.PostIssueMessage(ctx, issueID, args.Body); err != nil {
			return nil, fmt.Errorf("post_issue_message: %w", err)
		}
		return &AgentToolResult{Text: "Message posted to the issue thread."}, nil

	case ToolConcludeIssue:
		var args struct {
			Status     string `json:"status"`
			Resolution string `json:"resolution"`
		}
		if err := json.Unmarshal(nonEmptyJSON(input), &args); err != nil {
			return nil, fmt.Errorf("invalid %s input: %w", name, err)
		}
		if args.Status != ConcludeResolved && args.Status != ConcludeWontFix {
			// Coerce an out-of-vocabulary status to wont_fix rather than fail:
			// the agent is finishing either way, and a terminal state must result.
			args.Status = ConcludeWontFix
		}
		if err := s.issueStore.ConcludeIssue(ctx, issueID, args.Status, args.Resolution); err != nil {
			return nil, fmt.Errorf("conclude_issue: %w", err)
		}
		return &AgentToolResult{Text: "Investigation concluded (" + args.Status + ").", Concluded: true, Status: args.Status}, nil

	case ToolProposeAction:
		var args struct {
			Kind      string          `json:"kind"`
			Params    json.RawMessage `json:"params"`
			Rationale string          `json:"rationale"`
		}
		if err := json.Unmarshal(nonEmptyJSON(input), &args); err != nil {
			return nil, fmt.Errorf("invalid %s input: %w", name, err)
		}
		if !isProposableKind(args.Kind) {
			// A benign, non-error result so the model can correct itself within its
			// remaining budget rather than the run failing on a typo.
			return &AgentToolResult{Text: "Unknown action kind. Valid kinds: grab_release, remediate_queue, manual_import, trigger_search, rescan."}, nil
		}
		if len(args.Params) == 0 || string(args.Params) == "null" {
			return &AgentToolResult{Text: "params is required for propose_action and must be a JSON object for the chosen kind."}, nil
		}
		// Authoritative per-kind validation + fingerprint + conditional insert all
		// happen in the IssueStore. The model-supplied issue_id is ignored; the
		// Runner's claimed issueID is used.
		proposalID, existed, err := s.issueStore.ProposeAction(ctx, issueID, args.Kind, args.Params, args.Rationale, toolUseID)
		if err != nil {
			// A validation error is data for the model (it can fix params and retry),
			// not an infrastructure failure — return it as a benign tool result.
			return &AgentToolResult{Text: "Could not record that proposal: " + err.Error()}, nil
		}
		if existed {
			return &AgentToolResult{
				Text: fmt.Sprintf("Proposal #%d for this exact action is already proposed or decided; not duplicating it. Wait for the admin's decision or try a different fix.", proposalID),
			}, nil
		}
		// A genuinely new proposal: the run parks until an admin decides. The
		// resume will append this tool_use's result with the decision outcome.
		return &AgentToolResult{
			Text:   fmt.Sprintf("Proposal #%d recorded; awaiting admin approval — the investigation resumes with the outcome.", proposalID),
			Parked: true,
		}, nil

	default:
		return nil, fmt.Errorf("unknown agent tool: %s", name)
	}
}

// nonEmptyJSON normalizes empty/null tool input to an empty object so unmarshal
// into a struct never fails on a missing argument blob.
func nonEmptyJSON(input json.RawMessage) json.RawMessage {
	if len(input) == 0 || string(input) == "null" {
		return json.RawMessage("{}")
	}
	return input
}
