package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// recordingIssueStore is a recording fake of the narrow remediation write
// surface the agent-only tools use. It captures the exact arguments each write
// receives so tests can pin the argument mapping (notably: which issue id a
// write lands on).
type recordingIssueStore struct {
	enabled     bool
	hasReporter bool
	proposalID  int64
	existed     bool

	posts []struct {
		issueID int64
		body    string
	}
	asks []struct {
		issueID   int64
		question  string
		toolUseID string
	}
	proposals []struct {
		issueID   int64
		kind      string
		params    string
		rationale string
		toolUseID string
	}
}

func (s *recordingIssueStore) RemediationEnabled(context.Context) bool { return s.enabled }

func (s *recordingIssueStore) PostIssueMessage(_ context.Context, issueID int64, body string) error {
	s.posts = append(s.posts, struct {
		issueID int64
		body    string
	}{issueID, body})
	return nil
}

func (s *recordingIssueStore) AskReporter(_ context.Context, issueID int64, question, toolUseID string) (bool, error) {
	s.asks = append(s.asks, struct {
		issueID   int64
		question  string
		toolUseID string
	}{issueID, question, toolUseID})
	return s.hasReporter, nil
}

func (s *recordingIssueStore) ProposeAction(_ context.Context, issueID int64, kind string, params json.RawMessage, rationale, toolUseID string) (int64, bool, error) {
	s.proposals = append(s.proposals, struct {
		issueID   int64
		kind      string
		params    string
		rationale string
		toolUseID string
	}{issueID, kind, string(params), rationale, toolUseID})
	return s.proposalID, s.existed, nil
}

func newAgentToolServer(store IssueStore) *ToolServer {
	server := NewToolServer(nil, nil, nil, nil)
	server.SetIssueStore(store)
	return server
}

// TestExecuteAgentToolIgnoresModelSuppliedIssueID pins the anti-hijack routing
// rule: writes land on the Runner's CAS-claimed issue id, never on the
// issue_id the model wrote into its own tool input.
func TestExecuteAgentToolIgnoresModelSuppliedIssueID(t *testing.T) {
	store := &recordingIssueStore{enabled: true, hasReporter: true, proposalID: 31}
	server := newAgentToolServer(store)
	const claimedIssue = int64(7)

	if _, err := server.ExecuteAgentTool(
		context.Background(),
		ToolPostIssueMessage,
		json.RawMessage(`{"issue_id":999,"body":"found the stuck import"}`),
		claimedIssue,
		"toolu_post",
	); err != nil {
		t.Fatalf("post_issue_message: %v", err)
	}
	if _, err := server.ExecuteAgentTool(
		context.Background(),
		ToolProposeAction,
		json.RawMessage(`{"issue_id":999,"kind":"remediate_queue","params":{"media_type":"movie","queue_id":42,"action":"remove"},"rationale":"stalled with no seeders"}`),
		claimedIssue,
		"toolu_propose",
	); err != nil {
		t.Fatalf("propose_action: %v", err)
	}
	if _, err := server.ExecuteAgentTool(
		context.Background(),
		ToolAskReporter,
		json.RawMessage(`{"issue_id":999,"question":"which language do you want?"}`),
		claimedIssue,
		"toolu_ask",
	); err != nil {
		t.Fatalf("ask_reporter: %v", err)
	}

	if len(store.posts) != 1 || store.posts[0].issueID != claimedIssue {
		t.Fatalf("post routing = %+v, want claimed issue %d", store.posts, claimedIssue)
	}
	if len(store.proposals) != 1 || store.proposals[0].issueID != claimedIssue {
		t.Fatalf("proposal routing = %+v, want claimed issue %d", store.proposals, claimedIssue)
	}
	if len(store.asks) != 1 || store.asks[0].issueID != claimedIssue {
		t.Fatalf("ask routing = %+v, want claimed issue %d", store.asks, claimedIssue)
	}
}

func TestExecuteAgentToolProposeActionMapsArgumentsVerbatim(t *testing.T) {
	store := &recordingIssueStore{enabled: true, proposalID: 31}
	server := newAgentToolServer(store)

	result, err := server.ExecuteAgentTool(
		context.Background(),
		ToolProposeAction,
		json.RawMessage(`{"kind":"grab_release","params":{"media_type":"movie","guid":"[REDACTED release sha256:0011223344556677]","indexer_id":9,"queue_id_to_replace":42},"rationale":"the current grab is stalled"}`),
		7,
		"toolu_1",
	)
	if err != nil {
		t.Fatalf("propose_action: %v", err)
	}
	if !result.Parked || result.AwaitingUser || result.Concluded {
		t.Fatalf("propose result = %+v, want parked (awaiting admin, not user)", result)
	}
	if !strings.Contains(result.Text, "Proposal #31 recorded") {
		t.Fatalf("propose text = %q", result.Text)
	}

	if len(store.proposals) != 1 {
		t.Fatalf("proposals = %+v", store.proposals)
	}
	proposal := store.proposals[0]
	if proposal.kind != "grab_release" || proposal.rationale != "the current grab is stalled" || proposal.toolUseID != "toolu_1" {
		t.Fatalf("proposal mapping = %+v", proposal)
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(proposal.params), &params); err != nil {
		t.Fatalf("stored params are not JSON: %v", err)
	}
	if params["media_type"] != "movie" || params["indexer_id"] != float64(9) || params["queue_id_to_replace"] != float64(42) {
		t.Fatalf("stored params = %v, want the verbatim typed args", params)
	}

	// An identical re-proposal comes back idempotent and does NOT park again.
	store.existed = true
	result, err = server.ExecuteAgentTool(
		context.Background(),
		ToolProposeAction,
		json.RawMessage(`{"kind":"grab_release","params":{"media_type":"movie"},"rationale":"again"}`),
		7,
		"toolu_2",
	)
	if err != nil {
		t.Fatalf("duplicate propose_action: %v", err)
	}
	if result.Parked || !strings.Contains(result.Text, "already proposed or decided") {
		t.Fatalf("duplicate proposal result = %+v", result)
	}
}

func TestExecuteAgentToolProposeActionRejectsBadArgumentsBenignly(t *testing.T) {
	store := &recordingIssueStore{enabled: true}
	server := newAgentToolServer(store)

	tests := []struct {
		name     string
		input    string
		wantText string
	}{
		{
			name:     "unknown kind",
			input:    `{"kind":"delete_everything","params":{"media_type":"movie"},"rationale":"r"}`,
			wantText: "Unknown action kind",
		},
		{
			name:     "missing params",
			input:    `{"kind":"rescan","rationale":"r"}`,
			wantText: "params is required for propose_action",
		},
		{
			name:     "null params",
			input:    `{"kind":"rescan","params":null,"rationale":"r"}`,
			wantText: "params is required for propose_action",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := server.ExecuteAgentTool(context.Background(), ToolProposeAction, json.RawMessage(tt.input), 7, "toolu")
			if err != nil {
				t.Fatalf("propose_action: %v", err)
			}
			if result.Parked || result.Concluded || !strings.Contains(result.Text, tt.wantText) {
				t.Fatalf("result = %+v, want benign %q", result, tt.wantText)
			}
		})
	}
	if len(store.proposals) != 0 {
		t.Fatalf("invalid proposals were recorded: %+v", store.proposals)
	}
}

func TestExecuteAgentToolConcludeIssueCoercesUnknownStatus(t *testing.T) {
	server := newAgentToolServer(&recordingIssueStore{enabled: true})

	result, err := server.ExecuteAgentTool(
		context.Background(),
		ToolConcludeIssue,
		json.RawMessage(`{"status":"completely_fixed","resolution":"all done"}`),
		7,
		"toolu",
	)
	if err != nil {
		t.Fatalf("conclude_issue: %v", err)
	}
	if !result.Concluded || result.Status != ConcludeWontFix || result.Resolution != "all done" {
		t.Fatalf("coerced conclusion = %+v, want wont_fix terminal intent", result)
	}

	result, err = server.ExecuteAgentTool(
		context.Background(),
		ToolConcludeIssue,
		json.RawMessage(`{"status":"resolved","resolution":"target gone from queue"}`),
		7,
		"toolu",
	)
	if err != nil {
		t.Fatalf("conclude_issue resolved: %v", err)
	}
	if !result.Concluded || result.Status != ConcludeResolved {
		t.Fatalf("resolved conclusion = %+v", result)
	}
}

func TestExecuteAgentToolAskReporterParksOnlyWhenReporterExists(t *testing.T) {
	store := &recordingIssueStore{enabled: true, hasReporter: true}
	server := newAgentToolServer(store)

	result, err := server.ExecuteAgentTool(
		context.Background(),
		ToolAskReporter,
		json.RawMessage(`{"question":"do you want the English release?"}`),
		7,
		"toolu_ask",
	)
	if err != nil {
		t.Fatalf("ask_reporter: %v", err)
	}
	if !result.Parked || !result.AwaitingUser || result.Question != "do you want the English release?" {
		t.Fatalf("ask result = %+v, want parked awaiting_user with the question", result)
	}
	if len(store.asks) != 1 || store.asks[0].toolUseID != "toolu_ask" {
		t.Fatalf("ask mapping = %+v", store.asks)
	}

	store.hasReporter = false
	result, err = server.ExecuteAgentTool(
		context.Background(),
		ToolAskReporter,
		json.RawMessage(`{"question":"anyone there?"}`),
		7,
		"toolu_ask2",
	)
	if err != nil {
		t.Fatalf("ask_reporter without reporter: %v", err)
	}
	if result.Parked || result.AwaitingUser || !strings.Contains(result.Text, "no reporter to ask") {
		t.Fatalf("no-reporter result = %+v, want benign non-parking answer", result)
	}
}

func TestExecuteAgentToolRedactsCredentialBearingText(t *testing.T) {
	store := &recordingIssueStore{enabled: true, hasReporter: true}
	server := newAgentToolServer(store)
	const secret = "issue-store-secret-sentinel"

	if _, err := server.ExecuteAgentTool(
		context.Background(),
		ToolPostIssueMessage,
		json.RawMessage(`{"body":"download failed from https://indexer.invalid/get?apiKey=`+secret+`"}`),
		7,
		"toolu",
	); err != nil {
		t.Fatalf("post_issue_message: %v", err)
	}
	if len(store.posts) != 1 {
		t.Fatalf("posts = %+v", store.posts)
	}
	if strings.Contains(store.posts[0].body, secret) {
		t.Fatalf("issue thread received an unredacted credential: %s", store.posts[0].body)
	}
	if !strings.Contains(store.posts[0].body, "download failed") {
		t.Fatalf("redaction removed the useful diagnosis: %s", store.posts[0].body)
	}
}

func TestExecuteAgentToolsDegradeBenignlyWhenRemediationOff(t *testing.T) {
	tests := []struct {
		name  string
		store IssueStore
	}{
		{name: "no issue store wired", store: nil},
		{name: "remediation disabled", store: &recordingIssueStore{enabled: false}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := NewToolServer(nil, nil, nil, nil)
			if tt.store != nil {
				server.SetIssueStore(tt.store)
			}
			for _, tool := range AgentTools() {
				result, err := server.ExecuteAgentTool(
					context.Background(),
					tool.Name,
					json.RawMessage(`{"issue_id":7,"body":"b","status":"resolved","resolution":"r","kind":"rescan","params":{},"rationale":"r","question":"q"}`),
					7,
					"toolu",
				)
				if err != nil {
					t.Fatalf("%s: %v", tool.Name, err)
				}
				if result.Parked || result.Concluded || !strings.Contains(result.Text, "Remediation is not enabled") {
					t.Fatalf("%s disabled result = %+v", tool.Name, result)
				}
			}
			if recording, ok := tt.store.(*recordingIssueStore); ok {
				if len(recording.posts)+len(recording.asks)+len(recording.proposals) != 0 {
					t.Fatalf("disabled remediation still wrote: %+v", recording)
				}
			}
		})
	}
}

// TestExecuteAgentToolEmptyInputsAreBenign pins nonEmptyJSON: a missing or
// null argument blob never crashes dispatch, and empty required strings result
// in benign no-op answers.
func TestExecuteAgentToolEmptyInputsAreBenign(t *testing.T) {
	store := &recordingIssueStore{enabled: true, hasReporter: true}
	server := newAgentToolServer(store)

	for _, input := range []json.RawMessage{nil, json.RawMessage("null"), json.RawMessage(`{}`)} {
		result, err := server.ExecuteAgentTool(context.Background(), ToolPostIssueMessage, input, 7, "toolu")
		if err != nil {
			t.Fatalf("post_issue_message(%q): %v", string(input), err)
		}
		if !strings.Contains(result.Text, "nothing posted") {
			t.Fatalf("empty-body result = %+v", result)
		}
		result, err = server.ExecuteAgentTool(context.Background(), ToolAskReporter, input, 7, "toolu")
		if err != nil {
			t.Fatalf("ask_reporter(%q): %v", string(input), err)
		}
		if !strings.Contains(result.Text, "nothing asked") {
			t.Fatalf("empty-question result = %+v", result)
		}
	}
	if len(store.posts)+len(store.asks) != 0 {
		t.Fatalf("empty inputs still wrote: %+v", store)
	}

	if _, err := server.ExecuteAgentTool(context.Background(), "get_queue", json.RawMessage(`{}`), 7, "toolu"); err == nil {
		t.Fatal("a non-agent tool name was dispatched by the agent-only dispatch")
	}
}
