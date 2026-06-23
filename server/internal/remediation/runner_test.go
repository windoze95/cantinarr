package remediation

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/mcp"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// mutatingTools is the exact set of arr-mutating tool names that MUST be
// unreachable by the read-only remediation agent. The safety test asserts none
// is offered to the model and none is dispatchable.
var mutatingTools = []string{
	"grab_release",
	"remove_queue_item",
	"remediate_queue_item",
	"execute_manual_import",
	"rescan_media",
	"trigger_search",
}

// fakeToolHost is a stand-in for *mcp.ToolServer that records every ExecuteTool
// call so a test can prove a mutating tool name NEVER reaches arr execution. It
// also serves the read allow-list definitions and benign agent-tool results.
type fakeToolHost struct {
	mu               sync.Mutex
	executeToolNames []string // every name passed to ExecuteTool, in order
	concludeCalled   bool
}

func (f *fakeToolHost) ToolsByName(names []string) []mcp.Tool {
	out := make([]mcp.Tool, 0, len(names))
	for _, n := range names {
		out = append(out, mcp.Tool{Name: n})
	}
	return out
}

func (f *fakeToolHost) ExecuteTool(ctx context.Context, name string, input json.RawMessage, callCtx mcp.CallContext) (*mcp.ToolResult, error) {
	f.mu.Lock()
	f.executeToolNames = append(f.executeToolNames, name)
	f.mu.Unlock()
	return &mcp.ToolResult{Text: "ok: " + name}, nil
}

func (f *fakeToolHost) ExecuteAgentTool(ctx context.Context, name string, input json.RawMessage, issueID int64) (*mcp.AgentToolResult, error) {
	switch name {
	case mcp.ToolPostIssueMessage:
		return &mcp.AgentToolResult{Text: "posted"}, nil
	case mcp.ToolConcludeIssue:
		f.mu.Lock()
		f.concludeCalled = true
		f.mu.Unlock()
		// Mirror the real tool: it also writes the terminal issue state via the
		// IssueStore. The Runner injects the real Service for that side effect, so
		// here we only signal Concluded.
		return &mcp.AgentToolResult{Text: "concluded", Concluded: true, Status: mcp.ConcludeResolved}, nil
	}
	return &mcp.AgentToolResult{Text: "noop"}, nil
}

func (f *fakeToolHost) executedNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.executeToolNames))
	copy(out, f.executeToolNames)
	return out
}

// scriptedTurn is a fake ai.TurnRunner that returns pre-scripted assistant turns
// and records the tool list it was offered on each call.
type scriptedTurn struct {
	turns       []ai.TranscriptMessage
	idx         int
	offeredTool [][]string // names offered to NextTurn per call
}

func (s *scriptedTurn) NextTurn(ctx context.Context, p ai.TurnParams) (ai.TurnResult, error) {
	names := make([]string, 0, len(p.Tools))
	for _, t := range p.Tools {
		names = append(names, t.Name)
	}
	s.offeredTool = append(s.offeredTool, names)

	if s.idx >= len(s.turns) {
		// Past the script: a plain end_turn with no tools so the loop terminates.
		return ai.TurnResult{
			Message:    ai.TranscriptMessage{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{{Type: ai.BlockText, Text: "done"}}},
			StopReason: ai.StopReasonEndTurn,
		}, nil
	}
	msg := s.turns[s.idx]
	s.idx++
	stop := ai.StopReasonEndTurn
	for _, b := range msg.Content {
		if b.Type == ai.BlockToolUse {
			stop = ai.StopReasonToolUse
			break
		}
	}
	return ai.TurnResult{Message: msg, StopReason: stop, Usage: ai.Usage{InputTokens: 10, OutputTokens: 5}}, nil
}

func toolUse(id, name string) ai.TranscriptBlock {
	return ai.TranscriptBlock{Type: ai.BlockToolUse, ID: id, Name: name, Input: json.RawMessage(`{}`)}
}

// newTestRunner builds a Runner over an in-memory DB with the feature ENABLED, a
// configured (fake) AI key, and the given fake tool host + scripted turns. It
// returns the Runner, the Service, the fake host, and a seeded issue id.
func newTestRunner(t *testing.T, host toolHost, script *scriptedTurn) (*Runner, *Service, int64) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	svc := NewService(database, nil, nil, &fakeNotifier{})
	// Enable the feature and pin a tiny step budget by default; individual tests
	// override bounds as needed.
	if _, err := svc.SetSettings(Settings{Enabled: true, Autonomy: AutonomyPropose, MaxSteps: 12, MaxTurnTokens: 1024, MaxWallClockSecs: 30, MaxCostMicros: 500000, DailyRunCap: 50, DailyCostCeilingMicros: 5000000}); err != nil {
		t.Fatalf("set settings: %v", err)
	}

	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	creds := credentials.NewRegistry(database, cipher)
	if err := creds.SetCredential(credentials.AIKeyCredentialKey(credentials.AIProviderAnthropic), "fake-key"); err != nil {
		t.Fatalf("set credential: %v", err)
	}

	// Seed a user issue to investigate.
	res, err := database.Exec(
		"INSERT INTO issues (source, status, media_type, tmdb_id, title, detail) VALUES ('user','open','movie',42,'Test Movie','wrong content')",
	)
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issueID, _ := res.LastInsertId()

	r := &Runner{
		db:         database,
		svc:        svc,
		toolServer: host,
		creds:      creds,
		procToken:  "test",
		newTurn: func(provider, apiKey, model string) (ai.TurnRunner, error) {
			return script, nil
		},
	}
	return r, svc, issueID
}

// TestRunnerNeverOffersOrDispatchesMutatingTools is the CRITICAL safety test.
// The model is scripted to attempt EVERY mutating tool. The test asserts that
// (1) no mutating tool is ever offered to the model, and (2) no mutating tool is
// ever dispatched to ExecuteTool — the dispatch refuses each by name. Read-only
// by construction.
func TestRunnerNeverOffersOrDispatchesMutatingTools(t *testing.T) {
	// Turn 1: the (hijacked) model asks to run every mutating tool at once.
	var mutBlocks []ai.TranscriptBlock
	for i, name := range mutatingTools {
		mutBlocks = append(mutBlocks, toolUse("mut"+string(rune('a'+i)), name))
	}
	script := &scriptedTurn{turns: []ai.TranscriptMessage{
		{Role: ai.RoleAssistant, Content: mutBlocks},
		// Turn 2: a legitimate read, then conclude.
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{toolUse("read1", "get_queue")}},
		{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{toolUse("c1", mcp.ToolConcludeIssue)}},
	}}
	host := &fakeToolHost{}
	r, svc, issueID := newTestRunner(t, host, script)

	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// (1) No mutating tool was ever OFFERED to the model.
	for callIdx, offered := range script.offeredTool {
		for _, name := range offered {
			if isMutating(name) {
				t.Fatalf("turn %d offered mutating tool %q to the model", callIdx, name)
			}
		}
		// Sanity: the read allow-list IS offered.
		if !contains(offered, "get_queue") || !contains(offered, "diagnose_queue") {
			t.Fatalf("turn %d did not offer the read allow-list: %v", callIdx, offered)
		}
		if !contains(offered, mcp.ToolPostIssueMessage) || !contains(offered, mcp.ToolConcludeIssue) {
			t.Fatalf("turn %d did not offer the agent-only tools: %v", callIdx, offered)
		}
	}

	// (2) ExecuteTool was reached ONLY for allow-listed read tools — never for a
	// mutating one. The dispatch refused every mutating name without calling it.
	for _, name := range host.executedNames() {
		if isMutating(name) {
			t.Fatalf("ExecuteTool was called with mutating tool %q — mutation reachable!", name)
		}
		if !isReadToolAllowed(name) {
			t.Fatalf("ExecuteTool was called with non-allow-listed tool %q", name)
		}
	}
	// The legitimate read DID reach ExecuteTool.
	if !contains(host.executedNames(), "get_queue") {
		t.Fatalf("expected the allow-listed get_queue to reach ExecuteTool, got %v", host.executedNames())
	}

	// The run concluded resolved; the issue is terminal.
	issue, err := svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Status != IssueResolved {
		t.Fatalf("issue status = %q, want resolved", issue.Status)
	}
}

// TestRunnerMaxStepsGivesUp asserts the MaxSteps bound terminates a run that
// keeps calling tools without concluding, via the give-up terminal path.
func TestRunnerMaxStepsGivesUp(t *testing.T) {
	// Every scripted turn emits one allowed read tool and never concludes; the
	// fallback (past the script) would conclude, but MaxSteps must trip first.
	turns := make([]ai.TranscriptMessage, 10)
	for i := range turns {
		turns[i] = ai.TranscriptMessage{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{toolUse("r", "get_queue")}}
	}
	script := &scriptedTurn{turns: turns}
	host := &fakeToolHost{}
	r, svc, issueID := newTestRunner(t, host, script)

	// Tighten the step bound to 2.
	if _, err := svc.SetSettings(Settings{Enabled: true, Autonomy: AutonomyPropose, MaxSteps: 2, MaxTurnTokens: 1024, MaxWallClockSecs: 30, MaxCostMicros: 500000, DailyRunCap: 50, DailyCostCeilingMicros: 5000000}); err != nil {
		t.Fatalf("set settings: %v", err)
	}

	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The issue is terminal (wont_fix) and was never resolved.
	issue, err := svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Status != IssueWontFix {
		t.Fatalf("issue status = %q, want wont_fix after max steps", issue.Status)
	}

	// A giveup audit step was recorded with the max_steps stop reason.
	var giveups int
	if err := r.db.QueryRow("SELECT COUNT(*) FROM agent_steps WHERE kind = 'giveup'").Scan(&giveups); err != nil {
		t.Fatalf("count giveup steps: %v", err)
	}
	if giveups != 1 {
		t.Fatalf("giveup steps = %d, want 1", giveups)
	}
	var runStatus, stopReason string
	if err := r.db.QueryRow("SELECT status, COALESCE(stop_reason,'') FROM agent_runs WHERE issue_id = ?", issueID).Scan(&runStatus, &stopReason); err != nil {
		t.Fatalf("load run: %v", err)
	}
	if runStatus != runStatusGaveUp || stopReason != stopMaxSteps {
		t.Fatalf("run status/stop = %q/%q, want gave_up/max_steps", runStatus, stopReason)
	}

	// A plain-language give-up message was posted to the thread.
	thread, err := svc.IssueThread(issueID)
	if err != nil {
		t.Fatalf("IssueThread: %v", err)
	}
	var agentMsgs int
	for _, m := range thread {
		if m.AuthorKind == AuthorAgent {
			agentMsgs++
		}
	}
	if agentMsgs == 0 {
		t.Fatalf("expected at least one agent message on the thread after give-up")
	}
}

// --- test helpers ---

func isMutating(name string) bool {
	for _, m := range mutatingTools {
		if m == name {
			return true
		}
	}
	return false
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
