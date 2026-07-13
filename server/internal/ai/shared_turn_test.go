package ai

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/codexapp"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/mcp"
)

func TestResolveSharedAutonomousTurnIgnoresPersonalSettingsAndGrant(t *testing.T) {
	h, registry, database, userID := newResolverTestHandler(t)
	if _, err := database.Exec(`UPDATE users SET ai_shared_enabled = 0 WHERE id = ?`, userID); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetUserAIConfig(userID, credentials.AIProviderOpenAI, "personal-model"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetUserAICredential(userID, credentials.AIProviderOpenAI, "personal-secret"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetCredential(credentials.KeyGeminiKey, "shared-secret"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAIConfig(credentials.AIProviderGemini, "shared-model"); err != nil {
		t.Fatal(err)
	}

	resolved, err := h.ResolveSharedAutonomousTurn(context.Background())
	if err != nil {
		t.Fatalf("resolve shared autonomous turn: %v", err)
	}
	if resolved.Provider != credentials.AIProviderGemini || resolved.Model != "shared-model" {
		t.Fatalf("resolved shared turn = %+v", resolved)
	}
	admitted, ok := resolved.Runner.(*admittedAutonomousTurnRunner)
	if !ok {
		t.Fatalf("runner type = %T", resolved.Runner)
	}
	gemini, ok := admitted.delegate.(*geminiService)
	if !ok || gemini.model != "shared-model" {
		t.Fatalf("delegate = %T model=%v", admitted.delegate, gemini)
	}
}

func TestResolveSharedAutonomousTurnNeedsNoUserIdentity(t *testing.T) {
	h, registry, database, userID := newResolverTestHandler(t)
	if _, err := database.Exec(`DELETE FROM users WHERE id = ?`, userID); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetCredential(credentials.KeyAnthropicKey, "shared-secret"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAIConfig(credentials.AIProviderAnthropic, "shared-model"); err != nil {
		t.Fatal(err)
	}

	resolved, err := h.ResolveSharedAutonomousTurn(context.Background())
	if err != nil || resolved.Provider != credentials.AIProviderAnthropic || resolved.Model != "shared-model" || resolved.Runner == nil {
		t.Fatalf("shared autonomous turn without users = %+v err=%v", resolved, err)
	}
}

func TestResolveSharedAutonomousTurnFailsClosedInsteadOfUsingPersonalKey(t *testing.T) {
	h, registry, database, userID := newResolverTestHandler(t)
	if err := registry.SetUserAIConfig(userID, credentials.AIProviderOpenAI, "personal-model"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetUserAICredential(userID, credentials.AIProviderOpenAI, "personal-secret"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAIConfig(credentials.AIProviderOpenAI, "shared-model"); err != nil {
		t.Fatal(err)
	}
	// A plaintext shared key is invalid storage. The personal key is healthy,
	// but server-owned work must never borrow it.
	if _, err := database.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES (?, 'plaintext-shared-key')`, credentials.KeyOpenAIKey); err != nil {
		t.Fatal(err)
	}

	resolved, err := h.ResolveSharedAutonomousTurn(context.Background())
	if err == nil || resolved.Runner != nil || resolved.Provider != credentials.AIProviderOpenAI {
		t.Fatalf("resolved corrupt shared profile = %+v err=%v", resolved, err)
	}
}

type fakeCodexAutonomousManager struct {
	result codexapp.AutonomousTurnResult
	err    error
	model  string
	tools  []mcp.Tool
	prompt string
	max    int64
}

func (f *fakeCodexAutonomousManager) RunSharedAutonomousTurn(
	_ context.Context,
	model, _, _, prompt string,
	tools []mcp.Tool,
	maxOutputTokens int64,
) (codexapp.AutonomousTurnResult, error) {
	f.model = model
	f.tools = append([]mcp.Tool(nil), tools...)
	f.prompt = prompt
	f.max = maxOutputTokens
	return f.result, f.err
}

func TestCodexAutonomousTurnReturnsToolUseForRunnerDispatch(t *testing.T) {
	manager := &fakeCodexAutonomousManager{result: codexapp.AutonomousTurnResult{
		Text: "discarded by codexapp when tools exist",
		Usage: codexapp.AutonomousTurnUsage{
			InputTokens: 18, CachedInputTokens: 4, OutputTokens: 7,
		},
		ToolCalls: []codexapp.TurnToolCall{{
			ID: "call-1", Name: "get_queue", Input: json.RawMessage(`{"queue_id":7}`),
		}},
	}}
	runner := &codexAutonomousTurnRunner{
		manager: manager,
		model:   "default",
	}
	result, err := runner.NextTurn(context.Background(), TurnParams{
		System:  "guarded remediation prompt",
		Tools:   []mcp.Tool{{Name: "get_queue"}},
		History: Transcript{{Role: RoleUser, Content: []TranscriptBlock{{Type: BlockText, Text: "inspect"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if manager.model != "" || len(manager.tools) != 1 || manager.tools[0].Name != "get_queue" || manager.max != defaultTurnMaxTokens {
		t.Fatalf("manager model/tools/max = %q/%v/%d", manager.model, manager.tools, manager.max)
	}
	if result.StopReason != StopReasonToolUse || len(result.Message.Content) != 1 {
		t.Fatalf("turn result = %+v", result)
	}
	tool := result.Message.Content[0]
	if tool.Type != BlockToolUse || tool.ID != "call-1" || tool.Name != "get_queue" || string(tool.Input) != `{"queue_id":7}` {
		t.Fatalf("tool block = %+v", tool)
	}
	if manager.prompt == "" {
		t.Fatal("transcript prompt was not forwarded")
	}
	if result.Usage.InputTokens != 18 || result.Usage.CacheReadTokens != 4 || result.Usage.OutputTokens != 7 {
		t.Fatalf("normalized usage = %+v", result.Usage)
	}
}

func TestCodexAutonomousTurnReportsMonitoredOutputLimit(t *testing.T) {
	manager := &fakeCodexAutonomousManager{result: codexapp.AutonomousTurnResult{
		Text:               "partial diagnosis",
		Usage:              codexapp.AutonomousTurnUsage{InputTokens: 30, OutputTokens: 12},
		OutputLimitReached: true,
	}}
	runner := &codexAutonomousTurnRunner{manager: manager}
	result, err := runner.NextTurn(context.Background(), TurnParams{
		System:    "system",
		History:   Transcript{{Role: RoleUser, Content: []TranscriptBlock{{Type: BlockText, Text: "inspect"}}}},
		MaxTokens: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if manager.max != 10 || result.StopReason != StopReasonMaxOut || result.Usage.OutputTokens != 12 {
		t.Fatalf("max=%d result=%+v", manager.max, result)
	}
	if len(result.Message.Content) != 1 || result.Message.Content[0].Text != "partial diagnosis" {
		t.Fatalf("partial message = %+v", result.Message)
	}
}

func TestCodexAutonomousTurnForceNoToolsAndPropagatesError(t *testing.T) {
	wantErr := errors.New("provider unavailable")
	manager := &fakeCodexAutonomousManager{err: wantErr}
	runner := &codexAutonomousTurnRunner{manager: manager}
	_, err := runner.NextTurn(context.Background(), TurnParams{
		System: "system", Tools: []mcp.Tool{{Name: "get_queue"}},
		History:      Transcript{{Role: RoleUser, Content: []TranscriptBlock{{Type: BlockText, Text: "inspect"}}}},
		ForceNoTools: true,
	})
	if !errors.Is(err, wantErr) || len(manager.tools) != 0 {
		t.Fatalf("NextTurn err=%v tools=%v", err, manager.tools)
	}
}
