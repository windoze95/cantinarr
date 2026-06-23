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
// Wave 2 ships exactly two: report a finding (post_issue_message) and finish
// (conclude_issue). propose_action / ask_reporter are later waves.

// Agent-only tool names. Exported so the Runner's allow-list can name them.
const (
	ToolPostIssueMessage = "post_issue_message"
	ToolConcludeIssue    = "conclude_issue"
)

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

// AgentTools returns the agent-only tool definitions, for the Runner to add to
// its allow-list and hand to a model turn.
func AgentTools() []Tool {
	return []Tool{AgentToolPostIssueMessage, AgentToolConcludeIssue}
}

// IsAgentTool reports whether a name is one of the agent-only tools.
func IsAgentTool(name string) bool {
	switch name {
	case ToolPostIssueMessage, ToolConcludeIssue:
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
// needs to drive its loop: Concluded signals the investigation should terminate.
type AgentToolResult struct {
	Text      string
	Concluded bool
	Status    string // terminal issue status when Concluded
}

// ExecuteAgentTool dispatches an agent-only tool. The Runner passes the
// authoritative issueID (the issue it CAS-claimed); the model-supplied issue_id
// in the input is IGNORED for routing, so a hijacked model cannot post to or
// close a different issue. Every tool early-returns a benign result when
// remediation is disabled or no IssueStore is wired.
func (s *ToolServer) ExecuteAgentTool(ctx context.Context, name string, input json.RawMessage, issueID int64) (*AgentToolResult, error) {
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
