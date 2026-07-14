package codexapp

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	projectdb "github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/mcp"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// TestCodexAppHelperProcess is executed in a subprocess by fakeManager. Its
// wire shapes are pinned to the Codex app-server 0.144.x protocol: JSONL over
// stdio, experimental initialize, v2 account/device/thread/turn methods, and
// item/tool/call client responses.
func TestCodexAppHelperProcess(t *testing.T) {
	logPath := fakeArg("--fake-log=")
	if logPath == "" {
		return
	}
	logFake(t, logPath, "startup", map[string]any{
		"args":                  os.Args,
		"home":                  os.Getenv("CODEX_HOME"),
		"parent_secret_present": os.Getenv("CANTINARR_FAKE_PARENT_SECRET") != "",
		"auth_mode":             fileMode(filepath.Join(os.Getenv("CODEX_HOME"), "auth.json")),
		"home_mode":             fileMode(os.Getenv("CODEX_HOME")),
		"cwd_entries":           directoryEntryCount(t, "."),
	})

	encoder := json.NewEncoder(os.Stdout)
	var pendingTurnStartID any
	send := func(value any) {
		if err := encoder.Encode(value); err != nil {
			t.Fatalf("encode fake response: %v", err)
		}
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64<<10), maxProtocolBytes)
	for scanner.Scan() {
		var message map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		logFake(t, logPath, "received", message)
		method, _ := message["method"].(string)
		id, hasID := message["id"]
		if !hasID {
			continue
		}
		switch method {
		case "initialize":
			if slices.Contains(os.Args, "--fake-hang-initialize") {
				continue
			}
			send(map[string]any{"id": id, "result": map[string]any{
				"codexHome": os.Getenv("CODEX_HOME"), "platformFamily": "unix", "platformOs": "test", "userAgent": "codex-app-server/0.144.x",
			}})
		case "account/login/start":
			if delay, err := time.ParseDuration(fakeArg("--fake-login-delay=")); err == nil && delay > 0 {
				time.Sleep(delay)
			}
			send(map[string]any{"id": id, "result": map[string]any{
				"type": "chatgptDeviceCode", "loginId": "upstream-login-id", "userCode": "ABCD-EFGH", "verificationUrl": "https://auth.openai.com/codex/device",
			}})
			writeFakeAuth(t, "device-access-secret", "device-refresh-secret")
			send(map[string]any{"method": "account/login/completed", "params": map[string]any{
				"loginId": "upstream-login-id", "success": true,
			}})
		case "account/login/cancel":
			send(map[string]any{"id": id, "result": map[string]string{"status": "canceled"}})
		case "account/read":
			if slices.Contains(os.Args, "--fake-hang-account-read") {
				continue
			}
			if delay, err := time.ParseDuration(fakeArg("--fake-account-read-delay=")); err == nil && delay > 0 {
				time.Sleep(delay)
			}
			if _, err := os.Stat(filepath.Join(os.Getenv("CODEX_HOME"), "auth.json")); err != nil {
				send(map[string]any{"id": id, "result": map[string]any{"account": nil, "requiresOpenaiAuth": true}})
				continue
			}
			writeFakeAuth(t, fmt.Sprintf("refreshed-%d-secret", os.Getpid()), "rotated-refresh-secret")
			email := "viewer@example.test"
			send(map[string]any{"id": id, "result": map[string]any{
				"account": map[string]any{"type": "chatgpt", "email": email, "planType": "plus"}, "requiresOpenaiAuth": true,
			}})
		case "account/rateLimits/read":
			if slices.Contains(os.Args, "--fake-fail-rate-limits") {
				send(map[string]any{"id": id, "error": map[string]any{"code": -32000, "message": "fake rate limit failure"}})
				continue
			}
			if delay, err := time.ParseDuration(fakeArg("--fake-rate-limits-delay=")); err == nil && delay > 0 {
				time.Sleep(delay)
			}
			send(map[string]any{"id": id, "result": map[string]any{
				"rateLimits": map[string]any{"primary": map[string]any{"usedPercent": 17, "resetsAt": int64(1900000000), "windowDurationMins": 300}},
			}})
		case "account/logout":
			_ = os.Remove(filepath.Join(os.Getenv("CODEX_HOME"), "auth.json"))
			send(map[string]any{"id": id, "result": map[string]any{}})
		case "thread/start":
			send(map[string]any{"id": id, "result": map[string]any{"thread": map[string]any{"id": "thread-1"}}})
		case "turn/start":
			if slices.Contains(os.Args, "--fake-hang-turn") {
				send(map[string]any{"id": id, "result": map[string]any{"turn": map[string]any{"id": "turn-1", "status": "inProgress", "items": []any{}}}})
				continue
			}
			pendingTurnStartID = id
			// A tool request before the turn/start response must never execute,
			// even if it names an otherwise user-allowed Cantinarr tool.
			send(map[string]any{"id": "preturn-tool", "method": "item/tool/call", "params": map[string]any{
				"callId": "call-before-turn", "threadId": "thread-1", "turnId": "turn-1", "tool": "search_movies", "arguments": map[string]any{"query": "secret"},
			}})
		case "":
			if fmt.Sprint(id) == "preturn-tool" {
				send(map[string]any{"id": pendingTurnStartID, "result": map[string]any{"turn": map[string]any{"id": "turn-1", "status": "inProgress", "items": []any{}}}})
				if slices.Contains(os.Args, "--fake-prefill-notifications") {
					for i := 0; i < maxQueuedNotifications+32; i++ {
						send(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{
							"threadId": "thread-1", "turnId": "turn-1", "itemId": "prefill-message", "delta": "queued text",
						}})
					}
				}
				// Deliberately request an admin-only tool from a user turn. The
				// adapter must reject it even though the process invented the call.
				send(map[string]any{"id": "server-tool-1", "method": "item/tool/call", "params": map[string]any{
					"callId": "call-1", "threadId": "thread-1", "turnId": "turn-1", "tool": "get_queue", "arguments": map[string]any{},
				}})
				continue
			}
			if fmt.Sprint(id) == "server-tool-1" {
				if slices.Contains(os.Args, "--fake-allowed-tool") {
					send(map[string]any{"id": "server-tool-2", "method": "item/tool/call", "params": map[string]any{
						"callId": "call-2", "threadId": "thread-1", "turnId": "turn-1", "tool": "search_movies", "arguments": map[string]any{"query": "Dune"},
					}})
					continue
				}
				if slices.Contains(os.Args, "--fake-token-limit") {
					send(map[string]any{"method": "thread/tokenUsage/updated", "params": map[string]any{
						"threadId": "thread-1", "turnId": "turn-1",
						"tokenUsage": map[string]any{
							"last":  map[string]any{"inputTokens": 21, "cachedInputTokens": 5, "outputTokens": 12, "reasoningOutputTokens": 3, "totalTokens": 33},
							"total": map[string]any{"inputTokens": 21, "cachedInputTokens": 5, "outputTokens": 12, "reasoningOutputTokens": 3, "totalTokens": 33},
						},
					}})
					continue
				}
				if slices.Contains(os.Args, "--fake-token-after-interrupt") {
					continue
				}
				if slices.Contains(os.Args, "--fake-token-usage") {
					send(map[string]any{"method": "thread/tokenUsage/updated", "params": map[string]any{
						"threadId": "thread-1", "turnId": "turn-1",
						"tokenUsage": map[string]any{
							"last": map[string]any{"inputTokens": 18, "cachedInputTokens": 4, "outputTokens": 7, "reasoningOutputTokens": 2, "totalTokens": 25},
						},
					}})
				}
				send(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{
					"threadId": "thread-1", "turnId": "turn-1", "itemId": "message-1", "delta": "safe response",
				}})
				send(map[string]any{"method": "turn/completed", "params": map[string]any{
					"threadId": "thread-1", "turn": map[string]any{"id": "turn-1", "status": "completed", "items": []any{}},
				}})
			}
			if fmt.Sprint(id) == "server-tool-2" {
				send(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{
					"threadId": "thread-1", "turnId": "turn-1", "itemId": "message-1", "delta": "safe response",
				}})
				send(map[string]any{"method": "turn/completed", "params": map[string]any{
					"threadId": "thread-1", "turn": map[string]any{"id": "turn-1", "status": "completed", "items": []any{}},
				}})
			}
		case "turn/interrupt":
			send(map[string]any{"id": id, "result": map[string]any{}})
			if slices.Contains(os.Args, "--fake-token-after-interrupt") {
				send(map[string]any{"method": "thread/tokenUsage/updated", "params": map[string]any{
					"threadId": "thread-1", "turnId": "turn-1",
					"tokenUsage": map[string]any{
						"last": map[string]any{"inputTokens": 18, "cachedInputTokens": 4, "outputTokens": 7, "reasoningOutputTokens": 2, "totalTokens": 25},
					},
				}})
				send(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{
					"threadId": "thread-1", "turnId": "turn-1", "itemId": "late-message", "delta": "late partial response",
				}})
			}
			if slices.Contains(os.Args, "--fake-token-limit") {
				send(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{
					"threadId": "thread-1", "turnId": "turn-1", "itemId": "limited-message", "delta": "partial capped response",
				}})
			}
			send(map[string]any{"method": "turn/completed", "params": map[string]any{
				"threadId": "thread-1", "turn": map[string]any{"id": "turn-1", "status": "interrupted", "items": []any{}},
			}})
		default:
			send(map[string]any{"id": id, "error": map[string]any{"code": -32601, "message": "unsupported fake method"}})
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read fake protocol: %v", err)
	}
}

func TestManagerProtocol0144DeviceAccountAndRestrictedRun(t *testing.T) {
	manager, database, cipher, runtimeDir, logPath := fakeManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	login, err := manager.BeginDeviceLogin(ctx, 1)
	if err != nil {
		t.Fatalf("begin device login: %v", err)
	}
	if login.FlowID == "" || login.FlowID == "upstream-login-id" || login.VerificationURI != "https://auth.openai.com/codex/device" {
		t.Fatalf("unsafe login response: %#v", login)
	}
	if _, err := manager.CheckDeviceLogin(ctx, 2, login.FlowID); !errors.Is(err, ErrFlowNotFound) {
		t.Fatalf("other user checked flow: %v", err)
	}
	if err := manager.CancelDeviceLogin(2, login.FlowID); !errors.Is(err, ErrFlowNotFound) {
		t.Fatalf("other user canceled flow: %v", err)
	}

	var check DeviceLoginCheck
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		check, err = manager.CheckDeviceLogin(ctx, 1, login.FlowID)
		if err != nil {
			t.Fatalf("check device login: %v", err)
		}
		if check.Status != LoginPending {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if check.Status != LoginConnected || !check.Account.Connected || check.Account.Email != "viewer@example.test" {
		t.Fatalf("device flow did not connect: %#v", check)
	}
	if !manager.HasAccount(1) || manager.HasAccount(2) {
		t.Fatalf("account ownership mismatch: user1=%t user2=%t", manager.HasAccount(1), manager.HasAccount(2))
	}

	stored := storedAuth(t, database, 1)
	if !secrets.IsEncrypted(stored) || strings.Contains(stored, "secret") || strings.Contains(stored, "access_token") {
		t.Fatalf("auth was not encrypted at rest: %q", stored)
	}
	plain, err := cipher.Decrypt(stored)
	if err != nil || !strings.Contains(plain, "refreshed-") || !strings.Contains(plain, "rotated-refresh-secret") {
		t.Fatalf("stored auth did not contain rotated fake credentials: %v %q", err, plain)
	}
	if _, err := database.Exec(`UPDATE user_codex_accounts SET updated_at = datetime('now', '-2 minutes') WHERE user_id = 1`); err != nil {
		t.Fatal(err)
	}

	status, err := manager.Status(ctx, 1, true)
	if err != nil || !status.Connected || status.PlanType != "plus" || !bytes.Contains(status.RateLimits, []byte("usedPercent")) {
		t.Fatalf("refresh status = %#v, %v", status, err)
	}
	status, err = manager.Status(ctx, 2, false)
	if err != nil || status.Connected {
		t.Fatalf("unlinked user status = %#v, %v", status, err)
	}

	var text string
	toolStarted := false
	toolRecorded := false
	err = manager.Run(ctx, 1, auth.RoleUser, "gpt-5.4", "Cantinarr base prompt", "current user context", "find a movie", Callbacks{
		OnText:       func(delta string) { text += delta },
		OnToolStart:  func(string) { toolStarted = true },
		OnToolRecord: func(string, json.RawMessage, string, bool) { toolRecorded = true },
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if text != "safe response" {
		t.Fatalf("streamed text = %q", text)
	}
	if toolStarted || toolRecorded {
		t.Fatal("RBAC-rejected admin tool reached execution callbacks")
	}

	assertRestrictedProtocol(t, readFakeLog(t, logPath))
	assertRuntimeEmpty(t, runtimeDir)
	if err := manager.Unlink(1); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if manager.HasAccount(1) {
		t.Fatal("unlink retained account row")
	}
	assertRuntimeEmpty(t, runtimeDir)
}

func TestSharedAccountIsIndependentAndToolsUseRequestingActor(t *testing.T) {
	manager, _, _, runtimeDir, _ := fakeManager(t)
	personalAuth := []byte(`{"tokens":{"access_token":"personal-secret"}}`)
	sharedAuth := []byte(`{"tokens":{"access_token":"shared-secret"}}`)
	if err := manager.saveAccount(PersonalAccount(1), personalAuth, AccountStatus{Connected: true, Email: "personal@example.test"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.saveAccount(SharedAccount(), sharedAuth, AccountStatus{Connected: true, Email: "shared@example.test"}); err != nil {
		t.Fatal(err)
	}
	if found, err := manager.AccountExists(SharedAccount()); err != nil || !found {
		t.Fatalf("shared exists=%t err=%v", found, err)
	}
	manager.args = append(manager.args, "--fake-allowed-tool")
	var observed mcp.CallContext
	var authorized mcp.CallContext
	manager.toolCallObserver = func(call mcp.CallContext) { observed = call }
	manager.toolServer.SetCallAuthorizer(func(_ context.Context, call mcp.CallContext) (string, error) {
		authorized = call
		return auth.RoleUser, nil
	})
	if err := manager.RunWithAccountSession(context.Background(), SharedAccount(), 2, "device-2", auth.RoleUser, "", "base", "context", "prompt", Callbacks{}); err != nil {
		t.Fatal(err)
	}
	if observed.UserID != 2 || observed.Role != auth.RoleUser || observed.DeviceID != "device-2" || !observed.RequireSharedAI || !observed.Reauthorize {
		t.Fatalf("shared tool actor = %#v, want requester user 2", observed)
	}
	if authorized != observed {
		t.Fatalf("live authorizer actor = %#v, want %#v", authorized, observed)
	}
	if err := manager.UnlinkAccount(SharedAccount()); err != nil {
		t.Fatal(err)
	}
	if found, err := manager.AccountExists(SharedAccount()); err != nil || found {
		t.Fatalf("shared after unlink exists=%t err=%v", found, err)
	}
	if found, err := manager.AccountExists(PersonalAccount(1)); err != nil || !found {
		t.Fatalf("personal was affected by shared unlink: exists=%t err=%v", found, err)
	}
	if err := manager.Unlink(1); err != nil {
		t.Fatal(err)
	}
	assertRuntimeEmpty(t, runtimeDir)
}

func TestInteractiveAuthorizationRevocationTerminatesCodexTurn(t *testing.T) {
	manager, _, _, runtimeDir, logPath := fakeManager(t)
	if err := manager.saveAccount(
		SharedAccount(),
		[]byte(`{"tokens":{"access_token":"shared-secret"}}`),
		AccountStatus{Connected: true},
	); err != nil {
		t.Fatal(err)
	}
	manager.args = append(manager.args, "--fake-allowed-tool")
	manager.toolServer.SetCallAuthorizer(func(context.Context, mcp.CallContext) (string, error) {
		return "", errors.New("device revoked")
	})

	var text string
	var starts []string
	var ends []struct {
		name string
		ok   bool
	}
	recorded := false
	err := manager.RunWithAccountSession(
		context.Background(), SharedAccount(), 2, "device-2", auth.RoleUser, "",
		"base", "context", "prompt",
		Callbacks{
			OnText:      func(delta string) { text += delta },
			OnToolStart: func(name string) { starts = append(starts, name) },
			OnToolEnd: func(name string, ok bool) {
				ends = append(ends, struct {
					name string
					ok   bool
				}{name: name, ok: ok})
			},
			OnToolRecord: func(string, json.RawMessage, string, bool) { recorded = true },
		},
	)
	if !errors.Is(err, mcp.ErrToolAuthorization) {
		t.Fatalf("run error = %v, want ErrToolAuthorization", err)
	}
	if len(starts) != 1 || starts[0] != "search_movies" {
		t.Fatalf("tool starts = %v, want search_movies", starts)
	}
	if len(ends) != 1 || ends[0].name != "search_movies" || ends[0].ok {
		t.Fatalf("tool ends = %#v, want one failed search_movies", ends)
	}
	if text != "" || recorded {
		t.Fatalf("revoked turn continued callbacks: text=%q recorded=%t", text, recorded)
	}

	for _, entry := range readFakeLog(t, logPath) {
		if entry.Kind != "received" {
			continue
		}
		var message map[string]any
		if json.Unmarshal(entry.Value, &message) == nil && fmt.Sprint(message["id"]) == "server-tool-2" && message["method"] == nil {
			t.Fatalf("revoked tool received a model-visible response: %#v", message)
		}
	}
	assertRuntimeEmpty(t, runtimeDir)
}

func TestAutonomousTurnReturnsExplicitToolCallWithoutExecutingIt(t *testing.T) {
	manager, _, _, runtimeDir, _ := fakeManager(t)
	if err := manager.saveAccount(
		SharedAccount(),
		[]byte(`{"tokens":{"access_token":"shared-secret"}}`),
		AccountStatus{Connected: true},
	); err != nil {
		t.Fatal(err)
	}
	executed := false
	manager.toolCallObserver = func(mcp.CallContext) { executed = true }
	manager.args = append(manager.args, "--fake-token-after-interrupt", "--fake-prefill-notifications")

	result, err := manager.RunSharedAutonomousTurn(
		context.Background(),
		"",
		"server-owned remediation prompt",
		"guarded runner context",
		"inspect the issue",
		[]mcp.Tool{{
			Name:        "get_queue",
			Description: "Read the queue",
			InputSchema: map[string]any{"type": "object"},
		}},
		4096,
	)
	if err != nil {
		t.Fatalf("autonomous turn: %v", err)
	}
	if executed {
		t.Fatal("autonomous app-server turn executed a tool inside codexapp")
	}
	if result.Text != "" {
		t.Fatalf("post-placeholder assistant text was retained: %q", result.Text)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].ID != "call-1" || result.ToolCalls[0].Name != "get_queue" || string(result.ToolCalls[0].Input) != "{}" {
		t.Fatalf("captured tool calls = %#v", result.ToolCalls)
	}
	if result.Usage.InputTokens != 18 || result.Usage.CachedInputTokens != 4 || result.Usage.OutputTokens != 7 || result.Usage.ReasoningOutputTokens != 2 {
		t.Fatalf("captured tool usage = %#v", result.Usage)
	}
	assertRuntimeEmpty(t, runtimeDir)
}

func TestAutonomousTurnWithNoToolsReturnsText(t *testing.T) {
	manager, _, _, runtimeDir, _ := fakeManager(t)
	if err := manager.saveAccount(
		SharedAccount(),
		[]byte(`{"tokens":{"access_token":"shared-secret"}}`),
		AccountStatus{Connected: true},
	); err != nil {
		t.Fatal(err)
	}

	result, err := manager.RunSharedAutonomousTurn(
		context.Background(), "", "base", "context", "prompt", nil, 4096,
	)
	if err != nil {
		t.Fatalf("autonomous text turn: %v", err)
	}
	if result.Text != "safe response" || len(result.ToolCalls) != 0 {
		t.Fatalf("autonomous text result = %#v", result)
	}
	assertRuntimeEmpty(t, runtimeDir)
}

func TestProbeAccountUsesExactModelWithoutExecutingTools(t *testing.T) {
	manager, _, _, runtimeDir, logPath := fakeManager(t)
	if err := manager.saveAccount(
		SharedAccount(),
		[]byte(`{"tokens":{"access_token":"shared-secret"}}`),
		AccountStatus{Connected: true},
	); err != nil {
		t.Fatal(err)
	}
	executed := false
	manager.toolCallObserver = func(mcp.CallContext) { executed = true }
	if err := manager.ProbeAccount(context.Background(), SharedAccount(), "gpt-5.6-luna"); err != nil {
		t.Fatalf("probe account: %v", err)
	}
	if executed {
		t.Fatal("provider probe executed a Cantinarr tool")
	}
	var received []map[string]any
	for _, entry := range readFakeLog(t, logPath) {
		if entry.Kind != "received" {
			continue
		}
		var message map[string]any
		if json.Unmarshal(entry.Value, &message) == nil {
			received = append(received, message)
		}
	}
	thread := requestByMethod(t, received, "thread/start")
	params, _ := thread["params"].(map[string]any)
	if params["model"] != "gpt-5.6-luna" {
		t.Fatalf("probe model=%v", params["model"])
	}
	tools, ok := params["dynamicTools"].([]any)
	if !ok || len(tools) != 0 {
		t.Fatalf("probe dynamic tools=%#v, want empty", params["dynamicTools"])
	}
	assertRuntimeEmpty(t, runtimeDir)
}

func TestSharedAutonomousTurnInterruptsAtReportedOutputLimit(t *testing.T) {
	manager, _, _, runtimeDir, _ := fakeManager(t)
	if err := manager.saveAccount(
		SharedAccount(),
		[]byte(`{"tokens":{"access_token":"shared-secret"}}`),
		AccountStatus{Connected: true},
	); err != nil {
		t.Fatal(err)
	}
	manager.args = append(manager.args, "--fake-token-limit")

	result, err := manager.RunSharedAutonomousTurn(
		context.Background(), "", "base", "context", "prompt", nil, 10,
	)
	if err != nil {
		t.Fatalf("autonomous limited turn: %v", err)
	}
	if !result.OutputLimitReached || result.Usage.InputTokens != 21 || result.Usage.CachedInputTokens != 5 ||
		result.Usage.OutputTokens != 12 || result.Usage.ReasoningOutputTokens != 3 {
		t.Fatalf("limited result = %#v", result)
	}
	if result.Text != "partial capped response" {
		t.Fatalf("limited partial text = %q", result.Text)
	}
	assertRuntimeEmpty(t, runtimeDir)
}

func TestSharedDeviceFlowIsBoundToInitiatingAdminAndScope(t *testing.T) {
	manager, _, _, runtimeDir, _ := fakeManager(t)
	login, err := manager.BeginDeviceLoginForAccount(context.Background(), SharedAccount(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CheckDeviceLoginForAccount(context.Background(), SharedAccount(), 2, login.FlowID); !errors.Is(err, ErrFlowNotFound) {
		t.Fatalf("different actor poll = %v, want ErrFlowNotFound", err)
	}
	if _, err := manager.CheckDeviceLogin(context.Background(), 1, login.FlowID); !errors.Is(err, ErrFlowNotFound) {
		t.Fatalf("personal-scope poll of shared flow = %v, want ErrFlowNotFound", err)
	}
	if err := manager.CancelDeviceLoginForAccount(SharedAccount(), 1, login.FlowID); err != nil {
		t.Fatal(err)
	}
	assertRuntimeEmpty(t, runtimeDir)
}

func TestStatusPurgesAuthoritativelyDisconnectedAccount(t *testing.T) {
	manager, database, cipher, _, _ := fakeManager(t)
	authJSON := []byte(`{"tokens":{"access_token":"expired-secret"}}`)
	encrypted, err := cipher.Encrypt(string(authJSON))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO user_codex_accounts (user_id, auth_blob, updated_at)
		VALUES (1, ?, datetime('now', '-2 minutes'))`, encrypted); err != nil {
		t.Fatal(err)
	}
	// The fake reports a connected account whenever auth.json exists. Remove
	// auth before account/read by using a wrapper mode understood by the helper.
	manager.args = append([]string{"-test.run=TestDisconnectedCodexAppHelperProcess", "--"}, manager.args[2:]...)
	status, err := manager.Status(context.Background(), 1, true)
	if err != nil || status.Connected {
		t.Fatalf("disconnected status = %#v, %v", status, err)
	}
	if manager.HasAccount(1) {
		t.Fatal("authoritatively disconnected auth row blocked relink")
	}
}

func TestDeviceFlowsReserveCapacityForChat(t *testing.T) {
	manager, _, _, runtimeDir, _ := fakeManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	logins := make([]DeviceLogin, 0, maxConcurrentLogins)
	for userID := int64(10); userID < 10+maxConcurrentLogins; userID++ {
		login, err := manager.BeginDeviceLogin(ctx, userID)
		if err != nil {
			t.Fatalf("begin flow %d: %v", userID, err)
		}
		logins = append(logins, login)
	}
	if _, err := manager.BeginDeviceLogin(ctx, 99); !errors.Is(err, ErrProvider) {
		t.Fatalf("fifth concurrent login = %v, want bounded rejection", err)
	}
	if got := len(manager.processSlots); got != maxConcurrentLogins {
		t.Fatalf("device flows used %d process slots", got)
	}
	for i, login := range logins {
		if err := manager.CancelDeviceLogin(int64(10+i), login.FlowID); err != nil {
			t.Fatalf("cancel flow %d: %v", i, err)
		}
	}
	if len(manager.loginSlots) != 0 || len(manager.processSlots) != 0 {
		t.Fatalf("flow cleanup leaked slots: login=%d process=%d", len(manager.loginSlots), len(manager.processSlots))
	}
	assertRuntimeEmpty(t, runtimeDir)
}

func TestConcurrentDeviceLoginStartsDoNotQueue(t *testing.T) {
	manager, _, _, runtimeDir, _ := fakeManager(t)
	manager.args = append(manager.args, "--fake-login-delay=150ms")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := make(chan struct{})
	type result struct {
		login DeviceLogin
		err   error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			login, err := manager.BeginDeviceLogin(ctx, 1)
			results <- result{login: login, err: err}
		}()
	}
	close(start)
	var login DeviceLogin
	started, conflicted := 0, 0
	for range 2 {
		result := <-results
		switch {
		case result.err == nil:
			started++
			login = result.login
		case errors.Is(result.err, ErrLoginInProgress):
			conflicted++
		default:
			t.Fatalf("concurrent begin returned %v", result.err)
		}
	}
	if started != 1 || conflicted != 1 {
		t.Fatalf("concurrent begins: started=%d conflicted=%d", started, conflicted)
	}
	if err := manager.CancelDeviceLogin(1, login.FlowID); err != nil {
		t.Fatal(err)
	}
	assertRuntimeEmpty(t, runtimeDir)
}

func TestBeginCannotPublishAcrossUnlinkRevocation(t *testing.T) {
	manager, _, _, runtimeDir, _ := fakeManager(t)
	beforePublish := make(chan struct{})
	releasePublish := make(chan struct{})
	manager.beforeLoginPublish = func() {
		close(beforePublish)
		<-releasePublish
	}
	beginResult := make(chan error, 1)
	go func() {
		_, err := manager.BeginDeviceLogin(context.Background(), 1)
		beginResult <- err
	}()
	select {
	case <-beforePublish:
	case <-time.After(2 * time.Second):
		t.Fatal("begin did not reach its pre-publish boundary")
	}
	unlinkResult := make(chan error, 1)
	go func() { unlinkResult <- manager.Unlink(1) }()
	for deadline := time.Now().Add(time.Second); ; {
		manager.operationsMu.Lock()
		revoking := manager.revocations[PersonalAccount(1)] != 0
		manager.operationsMu.Unlock()
		if revoking {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("unlink did not establish revocation")
		}
		time.Sleep(time.Millisecond)
	}
	close(releasePublish)
	if err := <-beginResult; !errors.Is(err, ErrNotConnected) {
		t.Fatalf("begin across unlink = %v, want ErrNotConnected", err)
	}
	if err := <-unlinkResult; err != nil {
		t.Fatal(err)
	}
	if manager.flowForUser(1) != nil || manager.HasAccount(1) {
		t.Fatal("unlink allowed a late login flow or account to survive")
	}
	assertRuntimeEmpty(t, runtimeDir)
}

func TestCancelWinsConcurrentLoginFinalization(t *testing.T) {
	manager, _, _, runtimeDir, logPath := fakeManager(t)
	manager.args = append(manager.args, "--fake-account-read-delay=1s")
	login, err := manager.BeginDeviceLogin(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	checkResult := make(chan DeviceLoginCheck, 1)
	checkError := make(chan error, 1)
	go func() {
		for {
			check, checkErr := manager.CheckDeviceLogin(context.Background(), 1, login.FlowID)
			if checkErr != nil || check.Status != LoginPending {
				checkResult <- check
				checkError <- checkErr
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	waitForFakeMethod(t, logPath, "account/read")
	if err := manager.CancelDeviceLogin(1, login.FlowID); err != nil {
		t.Fatal(err)
	}
	check := <-checkResult
	if err := <-checkError; err != nil || check.Status != LoginFailed {
		t.Fatalf("canceled finalization = %#v, %v", check, err)
	}
	if manager.HasAccount(1) {
		t.Fatal("concurrent finalization resurrected a canceled account")
	}
	assertRuntimeEmpty(t, runtimeDir)
}

func TestStaleCancelCannotDeleteNewerAccount(t *testing.T) {
	manager, _, _, runtimeDir, _ := fakeManager(t)
	oldLogin, err := manager.BeginDeviceLogin(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	lookupDone := make(chan struct{})
	releaseCancel := make(chan struct{})
	manager.afterCancelLookup = func() {
		close(lookupDone)
		<-releaseCancel
	}
	cancelResult := make(chan error, 1)
	go func() { cancelResult <- manager.CancelDeviceLogin(1, oldLogin.FlowID) }()
	select {
	case <-lookupDone:
	case <-time.After(time.Second):
		t.Fatal("cancel did not capture the old flow")
	}
	oldFlow := manager.ownedFlow(1, oldLogin.FlowID)
	if oldFlow == nil {
		t.Fatal("old flow disappeared before forced rollover")
	}
	manager.finishFlow(oldFlow)
	newLogin, err := manager.BeginDeviceLogin(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	var connected bool
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		check, checkErr := manager.CheckDeviceLogin(context.Background(), 1, newLogin.FlowID)
		if checkErr != nil {
			t.Fatal(checkErr)
		}
		if check.Status == LoginConnected {
			connected = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !connected {
		t.Fatal("newer login did not connect")
	}
	close(releaseCancel)
	if err := <-cancelResult; !errors.Is(err, ErrFlowNotFound) {
		t.Fatalf("stale cancel = %v, want ErrFlowNotFound", err)
	}
	if !manager.HasAccount(1) {
		t.Fatal("stale cancel deleted the newer account")
	}
	if err := manager.Unlink(1); err != nil {
		t.Fatal(err)
	}
	assertRuntimeEmpty(t, runtimeDir)
}

func TestUnlinkCancelsStatusAndPreventsResurrection(t *testing.T) {
	manager, database, cipher, runtimeDir, logPath := fakeManager(t)
	encrypted, err := cipher.Encrypt(`{"tokens":{"access_token":"status-unlink-secret"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO user_codex_accounts (user_id, auth_blob, updated_at)
		VALUES (1, ?, datetime('now', '-2 minutes'))`, encrypted); err != nil {
		t.Fatal(err)
	}
	manager.args = append(manager.args, "--fake-rate-limits-delay=1s")
	statusResult := make(chan error, 1)
	go func() {
		_, statusErr := manager.Status(context.Background(), 1, true)
		statusResult <- statusErr
	}()
	waitForFakeMethod(t, logPath, "account/rateLimits/read")
	if err := manager.Unlink(1); err != nil {
		t.Fatal(err)
	}
	if err := <-statusResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("status during unlink = %v, want cancellation", err)
	}
	if manager.HasAccount(1) {
		t.Fatal("status persistence resurrected an unlinked account")
	}
	assertRuntimeEmpty(t, runtimeDir)
}

func TestUnlinkCancelsActiveRun(t *testing.T) {
	manager, database, cipher, runtimeDir, logPath := fakeManager(t)
	encrypted, err := cipher.Encrypt(`{"tokens":{"access_token":"run-unlink-secret"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO user_codex_accounts (user_id, auth_blob) VALUES (1, ?)`, encrypted); err != nil {
		t.Fatal(err)
	}
	manager.args = append(manager.args, "--fake-hang-turn")
	runResult := make(chan error, 1)
	go func() {
		runResult <- manager.Run(context.Background(), 1, auth.RoleUser, "", "base", "context", "prompt", Callbacks{})
	}()
	waitForFakeMethod(t, logPath, "turn/start")
	if err := manager.Unlink(1); err != nil {
		t.Fatal(err)
	}
	if err := <-runResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("run during unlink = %v, want cancellation", err)
	}
	if manager.HasAccount(1) {
		t.Fatal("active run retained an unlinked account")
	}
	assertRuntimeEmpty(t, runtimeDir)
}

func TestDeviceLoginRejectsAlreadyConnectedAccount(t *testing.T) {
	manager, database, cipher, runtimeDir, _ := fakeManager(t)
	encrypted, err := cipher.Encrypt(`{"tokens":{"access_token":"connected-secret"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO user_codex_accounts (user_id, auth_blob) VALUES (1, ?)`, encrypted); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.BeginDeviceLogin(context.Background(), 1); !errors.Is(err, ErrAlreadyConnected) {
		t.Fatalf("begin for connected account = %v, want ErrAlreadyConnected", err)
	}
	assertRuntimeEmpty(t, runtimeDir)
}

func TestFreshStatusUsesMetadataWithoutDecryptingOrStartingProcess(t *testing.T) {
	manager, database, _, runtimeDir, _ := fakeManager(t)
	otherCipher, err := secrets.NewCipher(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := otherCipher.Encrypt(`{"tokens":{"access_token":"different-key-secret"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO user_codex_accounts (user_id, auth_blob, email, plan_type, rate_limits_json)
		VALUES (1, ?, 'cached@example.test', 'plus', '{"primary":{"usedPercent":12}}')`, encrypted); err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status(context.Background(), 1, true)
	if err != nil || !status.Connected || status.Stale || status.Email != "cached@example.test" || status.UpdatedAt.IsZero() {
		t.Fatalf("fresh cached status = %#v, %v", status, err)
	}
	assertRuntimeEmpty(t, runtimeDir)
}

func TestAuthOnlyPersistenceDoesNotExtendStatusFreshness(t *testing.T) {
	manager, database, cipher, _, _ := fakeManager(t)
	encrypted, err := cipher.Encrypt(`{"tokens":{"access_token":"old-secret"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO user_codex_accounts (user_id, auth_blob, rate_limits_json, updated_at)
		VALUES (1, ?, '{"primary":{"usedPercent":44}}', datetime('now', '-2 minutes'))`, encrypted); err != nil {
		t.Fatal(err)
	}
	var before int64
	if err := database.QueryRow(`SELECT CAST(strftime('%s', updated_at) AS INTEGER) FROM user_codex_accounts WHERE user_id = 1`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := manager.saveRefreshedAuth(PersonalAccount(1), []byte(`{"tokens":{"access_token":"rotated-secret"}}`)); err != nil {
		t.Fatal(err)
	}
	var after int64
	if err := database.QueryRow(`SELECT CAST(strftime('%s', updated_at) AS INTEGER) FROM user_codex_accounts WHERE user_id = 1`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("auth-only persistence advanced metadata freshness: before=%d after=%d", before, after)
	}
	status, err := manager.Status(context.Background(), 1, false)
	if err != nil || !status.Stale {
		t.Fatalf("old usage snapshot became fresh: %#v, %v", status, err)
	}
}

func TestFailedRateLimitRefreshRemainsStale(t *testing.T) {
	manager, database, cipher, _, _ := fakeManager(t)
	encrypted, err := cipher.Encrypt(`{"tokens":{"access_token":"rate-secret"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO user_codex_accounts (user_id, auth_blob, rate_limits_json, updated_at)
		VALUES (1, ?, '{"primary":{"usedPercent":88}}', datetime('now', '-2 minutes'))`, encrypted); err != nil {
		t.Fatal(err)
	}
	manager.args = append(manager.args, "--fake-fail-rate-limits")
	status, err := manager.Status(context.Background(), 1, true)
	if err != nil || !status.Connected || !status.Stale {
		t.Fatalf("failed rate-limit refresh = %#v, %v", status, err)
	}
	cached, err := manager.Status(context.Background(), 1, false)
	if err != nil || !cached.Stale {
		t.Fatalf("failed refresh persisted as fresh: %#v, %v", cached, err)
	}
}

func TestAccountOperationsHaveInternalDeadlines(t *testing.T) {
	t.Run("begin", func(t *testing.T) {
		manager, _, _, runtimeDir, _ := fakeManager(t)
		manager.accountOperationTimeout = 75 * time.Millisecond
		manager.args = append(manager.args, "--fake-hang-initialize")
		started := time.Now()
		_, err := manager.BeginDeviceLogin(context.Background(), 1)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("hung begin = %v, want deadline", err)
		}
		if time.Since(started) > 3*time.Second {
			t.Fatal("hung begin exceeded its internal deadline")
		}
		assertRuntimeEmpty(t, runtimeDir)
	})

	t.Run("status refresh", func(t *testing.T) {
		manager, database, cipher, runtimeDir, _ := fakeManager(t)
		manager.accountOperationTimeout = 75 * time.Millisecond
		encrypted, err := cipher.Encrypt(`{"tokens":{"access_token":"status-secret"}}`)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`
			INSERT INTO user_codex_accounts (user_id, auth_blob, updated_at)
			VALUES (1, ?, datetime('now', '-2 minutes'))`, encrypted); err != nil {
			t.Fatal(err)
		}
		manager.args = append(manager.args, "--fake-hang-initialize")
		started := time.Now()
		_, err = manager.Status(context.Background(), 1, true)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("hung status = %v, want deadline", err)
		}
		if time.Since(started) > 3*time.Second {
			t.Fatal("hung status exceeded its internal deadline")
		}
		assertRuntimeEmpty(t, runtimeDir)
	})

	t.Run("login finalization", func(t *testing.T) {
		manager, _, _, runtimeDir, _ := fakeManager(t)
		manager.args = append(manager.args, "--fake-hang-account-read")
		login, err := manager.BeginDeviceLogin(context.Background(), 1)
		if err != nil {
			t.Fatal(err)
		}
		manager.accountOperationTimeout = 75 * time.Millisecond
		for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
			check, checkErr := manager.CheckDeviceLogin(context.Background(), 1, login.FlowID)
			if errors.Is(checkErr, context.DeadlineExceeded) {
				assertRuntimeEmpty(t, runtimeDir)
				return
			}
			if checkErr != nil || check.Status != LoginPending {
				t.Fatalf("hung finalization = %#v, %v", check, checkErr)
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("hung login finalization did not reach its internal deadline")
	})
}

func TestServerRequestDispatchIsBoundedBeforeSpawning(t *testing.T) {
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readPipe.Close()
	defer writePipe.Close()
	go func() { _, _ = io.Copy(io.Discard, readPipe) }()
	session := &appSession{
		stdin:          writePipe,
		serverRequests: make(chan struct{}, maxServerRequests),
	}
	entered := make(chan struct{}, maxServerRequests+1)
	release := make(chan struct{})
	session.setRequestHandler(func(context.Context, string, json.RawMessage) (any, error) {
		entered <- struct{}{}
		<-release
		return map[string]any{"success": false}, nil
	})
	for i := 0; i < 64; i++ {
		session.dispatch(rpcEnvelope{
			ID:     json.RawMessage(fmt.Sprintf("%d", i+1)),
			Method: "item/tool/call",
			Params: json.RawMessage(`{}`),
		})
	}
	for i := 0; i < maxServerRequests; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("bounded handlers did not start")
		}
	}
	select {
	case <-entered:
		t.Fatal("server request dispatch exceeded its goroutine bound")
	default:
	}
	close(release)
	for deadline := time.Now().Add(time.Second); len(session.serverRequests) != 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if len(session.serverRequests) != 0 {
		t.Fatal("server request handlers did not release capacity")
	}
}

func TestSessionStopCancelsActiveServerRequests(t *testing.T) {
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readPipe.Close()
	defer writePipe.Close()
	requestContext, cancelRequests := context.WithCancel(context.Background())
	processDone := make(chan struct{})
	close(processDone)
	session := &appSession{
		stdin:          writePipe,
		serverRequests: make(chan struct{}, maxServerRequests),
		requestContext: requestContext,
		cancelRequests: cancelRequests,
		processDone:    processDone,
	}
	entered := make(chan struct{})
	canceled := make(chan struct{})
	session.setRequestHandler(func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	})
	session.dispatch(rpcEnvelope{ID: json.RawMessage(`1`), Method: "item/tool/call", Params: json.RawMessage(`{}`)})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("server request did not start")
	}
	session.stop()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("session stop did not cancel its server request")
	}
	if session.activeRequests.Load() != 0 {
		t.Fatal("session stop returned with a cancelable request active")
	}
}

func TestNotificationBackpressureSupportsLongDeltaStreams(t *testing.T) {
	requestContext, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	session := &appSession{
		notifications:  make(chan rpcNotification, maxQueuedNotifications),
		requestContext: requestContext,
		cancelRequests: cancelRequests,
	}
	const notificationCount = maxQueuedNotifications + 128
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		for i := 0; i < notificationCount; i++ {
			session.dispatch(rpcEnvelope{
				Method: "item/agentMessage/delta",
				Params: json.RawMessage(fmt.Sprintf(`{"threadId":"thread","turnId":"turn","delta":"%d"}`, i)),
			})
		}
	}()
	select {
	case <-producerDone:
		t.Fatal("producer bypassed the bounded notification queue")
	case <-time.After(20 * time.Millisecond):
	}
	for i := 0; i < notificationCount; i++ {
		select {
		case notification := <-session.notifications:
			if notification.method != "item/agentMessage/delta" {
				t.Fatalf("unexpected notification %q", notification.method)
			}
		case <-time.After(time.Second):
			t.Fatalf("received only %d of %d valid deltas", i, notificationCount)
		}
	}
	select {
	case <-producerDone:
	case <-time.After(time.Second):
		t.Fatal("valid delta producer remained blocked after the queue drained")
	}
	if requestContext.Err() != nil {
		t.Fatal("valid long delta stream failed the provider session")
	}
}

func TestNotificationBackpressureUnblocksOnSessionCancel(t *testing.T) {
	requestContext, cancelRequests := context.WithCancel(context.Background())
	session := &appSession{
		notifications:  make(chan rpcNotification, 1),
		requestContext: requestContext,
		cancelRequests: cancelRequests,
	}
	message := rpcEnvelope{Method: "item/agentMessage/delta", Params: json.RawMessage(`{"delta":"x"}`)}
	session.dispatch(message)
	dispatchDone := make(chan struct{})
	go func() {
		session.dispatch(message)
		close(dispatchDone)
	}()
	select {
	case <-dispatchDone:
		t.Fatal("full notification queue did not apply backpressure")
	case <-time.After(20 * time.Millisecond):
	}
	cancelRequests()
	select {
	case <-dispatchDone:
	case <-time.After(time.Second):
		t.Fatal("session cancellation did not release notification backpressure")
	}
}

func TestTurnCompletedCompactsOversizedDynamicToolContent(t *testing.T) {
	params, err := json.Marshal(map[string]any{
		"threadId": "thread-1",
		"turn": map[string]any{
			"id": "turn-1", "status": "completed",
			"items": []any{
				map[string]any{
					"type": "dynamicToolCall", "id": "tool-1", "tool": "search_movies",
					"contentItems": []any{map[string]any{"type": "inputText", "text": strings.Repeat("x", 1<<20)}},
				},
				map[string]any{"type": "agentMessage", "text": "safe response"},
			},
		},
	})
	if err != nil || len(params) <= maxNotificationBytes {
		t.Fatalf("oversized turn fixture = %d bytes, %v", len(params), err)
	}
	requestContext, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	session := &appSession{
		notifications:  make(chan rpcNotification, 1),
		requestContext: requestContext,
		cancelRequests: cancelRequests,
	}
	session.dispatch(rpcEnvelope{Method: "turn/completed", Params: params})
	if requestContext.Err() != nil {
		t.Fatal("valid oversized turn failed the provider session")
	}
	notification := <-session.notifications
	if len(notification.params) > maxNotificationBytes {
		t.Fatalf("compacted turn remained %d bytes", len(notification.params))
	}
	var complete turnCompleteParams
	if err := json.Unmarshal(notification.params, &complete); err != nil {
		t.Fatal(err)
	}
	if complete.ThreadID != "thread-1" || complete.Turn.ID != "turn-1" || complete.Turn.Status != "completed" {
		t.Fatalf("compaction lost turn identity: %#v", complete)
	}
	if len(complete.Turn.Items) != 1 || complete.Turn.Items[0].Type != "agentMessage" || complete.Turn.Items[0].Text != "safe response" {
		t.Fatalf("compaction retained tool payload or lost agent text: %#v", complete.Turn.Items)
	}
}

func TestTokenUsageNotificationIsValidatedAndCompacted(t *testing.T) {
	params, err := json.Marshal(map[string]any{
		"threadId": "thread-1",
		"turnId":   "turn-1",
		"tokenUsage": map[string]any{
			"last": map[string]any{
				"inputTokens": 21, "cachedInputTokens": 5,
				"outputTokens": 12, "reasoningOutputTokens": 3,
			},
			"ignored": strings.Repeat("x", maxNotificationBytes),
		},
	})
	if err != nil || len(params) <= maxNotificationBytes {
		t.Fatalf("oversized token fixture = %d bytes, %v", len(params), err)
	}
	compact, ok := compactNotification("thread/tokenUsage/updated", params)
	if !ok || len(compact) >= maxNotificationBytes {
		t.Fatalf("compacted token notification = %d bytes, ok=%t", len(compact), ok)
	}
	update, ok := decodeTokenUsageUpdate(compact)
	if !ok || update.TokenUsage.Last.OutputTokens != 12 || update.TokenUsage.Last.CachedInputTokens != 5 {
		t.Fatalf("decoded compact usage = %#v, ok=%t", update, ok)
	}

	for _, invalid := range []json.RawMessage{
		json.RawMessage(`{"threadId":"","turnId":"turn-1","tokenUsage":{"last":{"outputTokens":1}}}`),
		json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","tokenUsage":{"last":{"outputTokens":-1}}}`),
		json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","tokenUsage":{}}`),
	} {
		if _, ok := compactNotification("thread/tokenUsage/updated", invalid); ok {
			t.Fatalf("invalid token notification was accepted: %s", invalid)
		}
	}
}

func TestDuplicateRPCReplyFailsSessionWithoutBlocking(t *testing.T) {
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readPipe.Close()
	requestContext, cancelRequests := context.WithCancel(context.Background())
	replies := make(chan rpcReply, 1)
	replies <- rpcReply{}
	session := &appSession{
		stdin:          writePipe,
		pending:        map[string]chan rpcReply{"1": replies},
		requestContext: requestContext,
		cancelRequests: cancelRequests,
	}
	session.dispatch(rpcEnvelope{ID: json.RawMessage(`1`), Result: json.RawMessage(`{}`)})
	select {
	case <-requestContext.Done():
	case <-time.After(time.Second):
		t.Fatal("duplicate reply did not fail the provider session")
	}
}

func TestWriteContextClosesBlockedStdinAtDeadline(t *testing.T) {
	stdin := &blockingWriteCloser{closed: make(chan struct{})}
	session := &appSession{stdin: stdin}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := session.writeContext(ctx, map[string]any{"prompt": strings.Repeat("x", 1<<20)})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked protocol write = %v, want deadline", err)
	}
	select {
	case <-stdin.closed:
	default:
		t.Fatal("deadline did not close blocked app-server stdin")
	}
}

func TestWriteSerializationAcquisitionHonorsContext(t *testing.T) {
	stdin := &blockingWriteCloser{closed: make(chan struct{}), started: make(chan struct{})}
	session := &appSession{stdin: stdin}
	firstResult := make(chan error, 1)
	go func() {
		firstResult <- session.writeContext(context.Background(), map[string]string{"first": "blocked"})
	}()
	select {
	case <-stdin.started:
	case <-time.After(time.Second):
		t.Fatal("first protocol write did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := session.writeContext(ctx, map[string]string{"second": "must not wait"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second protocol write = %v, want deadline", err)
	}
	select {
	case <-firstResult:
	case <-time.After(time.Second):
		t.Fatal("canceling the queued writer did not unblock the stalled writer")
	}
}

func TestCompletedLoginStorageFailureIsTerminalFailed(t *testing.T) {
	manager, _, _, runtimeDir, logPath := fakeManager(t)
	manager.args = []string{"-test.run=TestBrokenCodexLoginHelperProcess", "--", "--fake-log=" + logPath}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	login, err := manager.BeginDeviceLogin(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	var check DeviceLoginCheck
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		check, err = manager.CheckDeviceLogin(ctx, 1, login.FlowID)
		if err != nil {
			t.Fatalf("terminal check returned transport error: %v", err)
		}
		if check.Status != LoginPending {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if check.Status != LoginFailed || check.Error == "" {
		t.Fatalf("storage failure was not terminal failed: %#v", check)
	}
	if _, err := manager.CheckDeviceLogin(ctx, 1, login.FlowID); !errors.Is(err, ErrFlowNotFound) {
		t.Fatalf("finished failed flow remained usable: %v", err)
	}
	assertRuntimeEmpty(t, runtimeDir)
}

func TestRunPurgesAuthoritativelyUnauthorizedAccount(t *testing.T) {
	manager, database, cipher, runtimeDir, logPath := fakeManager(t)
	authJSON := `{"tokens":{"access_token":"expired-run-secret"}}`
	encrypted, err := cipher.Encrypt(authJSON)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO user_codex_accounts (user_id, auth_blob) VALUES (1, ?)`, encrypted); err != nil {
		t.Fatal(err)
	}
	manager.args = []string{"-test.run=TestUnauthorizedCodexRunHelperProcess", "--", "--fake-log=" + logPath}
	err = manager.Run(context.Background(), 1, auth.RoleUser, "", "base", "context", "prompt", Callbacks{})
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("unauthorized turn = %v, want ErrNotConnected", err)
	}
	if manager.HasAccount(1) {
		t.Fatal("unauthorized run retained stale auth row")
	}
	assertRuntimeEmpty(t, runtimeDir)
}

func TestUnauthorizedCodexRunHelperProcess(t *testing.T) {
	if fakeArg("--fake-log=") == "" {
		return
	}
	encoder := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var message map[string]any
		if json.Unmarshal(scanner.Bytes(), &message) != nil {
			t.Fatal("bad request")
		}
		id, hasID := message["id"]
		if !hasID {
			continue
		}
		switch message["method"] {
		case "initialize":
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{"codexHome": os.Getenv("CODEX_HOME")}})
		case "thread/start":
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{"thread": map[string]any{"id": "unauthorized-thread"}}})
		case "turn/start":
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{"turn": map[string]any{"id": "unauthorized-turn"}}})
			_ = encoder.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{
				"threadId": "unauthorized-thread",
				"turn": map[string]any{
					"id": "unauthorized-turn", "status": "failed", "items": []any{},
					"error": map[string]any{"message": "hidden upstream detail", "codexErrorInfo": "unauthorized"},
				},
			}})
		default:
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{}})
		}
	}
}

func TestBrokenCodexLoginHelperProcess(t *testing.T) {
	if fakeArg("--fake-log=") == "" {
		return
	}
	encoder := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var message map[string]any
		if json.Unmarshal(scanner.Bytes(), &message) != nil {
			t.Fatal("bad request")
		}
		id, hasID := message["id"]
		if !hasID {
			continue
		}
		switch message["method"] {
		case "initialize":
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{"codexHome": os.Getenv("CODEX_HOME")}})
		case "account/login/start":
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{
				"type": "chatgptDeviceCode", "loginId": "broken-login", "userCode": "BROKEN", "verificationUrl": "https://auth.openai.com/codex/device",
			}})
			_ = encoder.Encode(map[string]any{"method": "account/login/completed", "params": map[string]any{"loginId": "broken-login", "success": true}})
		case "account/read":
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{
				"account": map[string]any{"type": "chatgpt", "email": "broken@example.test", "planType": "plus"}, "requiresOpenaiAuth": true,
			}})
		case "account/rateLimits/read":
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{"rateLimits": map[string]any{}}})
		default:
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{}})
		}
	}
}

func TestDisconnectedCodexAppHelperProcess(t *testing.T) {
	logPath := fakeArg("--fake-log=")
	if logPath == "" {
		return
	}
	encoder := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var message map[string]any
		if json.Unmarshal(scanner.Bytes(), &message) != nil {
			t.Fatal("bad request")
		}
		id, hasID := message["id"]
		if !hasID {
			continue
		}
		switch message["method"] {
		case "initialize":
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{"codexHome": os.Getenv("CODEX_HOME")}})
		case "account/read":
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{"account": nil, "requiresOpenaiAuth": true}})
		default:
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{}})
		}
	}
}

func TestManagerRejectsDiskRuntimeWithoutExplicitTestEscape(t *testing.T) {
	database, err := projectdb.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(database, cipher, mcp.NewToolServer(credentials.NewRegistry(database, cipher), nil, nil, nil), Options{
		Binary:     os.Args[0],
		RuntimeDir: t.TempDir(),
	})
	if manager.Available() {
		t.Fatal("disk-backed runtime was accepted")
	}
	if _, err := database.Exec(`INSERT INTO users (id, username, password_hash, role) VALUES (1, 'cached', 'x', 'user')`); err != nil {
		t.Fatal(err)
	}
	authJSON := `{"tokens":{"access_token":"cached-secret"}}`
	encrypted, err := cipher.Encrypt(authJSON)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO user_codex_accounts (user_id, auth_blob, email, plan_type) VALUES (1, ?, 'cached@example.test', 'plus')`, encrypted); err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status(context.Background(), 1, false)
	if err != nil || !status.Connected || status.Email != "cached@example.test" {
		t.Fatalf("cached status unavailable without runtime: %#v, %v", status, err)
	}
}

func TestInstalledAppServerCommandSurface(t *testing.T) {
	standalone := os.Getenv("CANTINARR_CODEX_APP_SERVER_SMOKE_BINARY")
	if standalone != "" {
		assertAppServerCommandSurface(t, standalone, nil)
	}
	path, prefix, err := discoverBinary("")
	if err != nil {
		if standalone == "" {
			t.Skip("Codex app-server is not installed in this test environment")
		}
		return
	}
	if path != standalone {
		assertAppServerCommandSurface(t, path, prefix)
	}
}

func TestDiscoverBinaryCanonicalizesRelativeOverride(t *testing.T) {
	target, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	link := filepath.Join(dir, "app-server-fixture")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	path, prefix, err := discoverBinary("./app-server-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(path) || len(prefix) != 0 {
		t.Fatalf("relative override resolved to path=%q prefix=%v", path, prefix)
	}
	if !sameDirectory(filepath.Dir(path), dir) {
		t.Fatalf("relative override resolved outside its discovery directory: %q", path)
	}
}

func assertAppServerCommandSurface(t *testing.T, path string, prefix []string) {
	t.Helper()
	args := append(append([]string(nil), prefix...), "--help")
	output, err := exec.Command(path, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("app-server --help failed: %v", err)
	}
	text := string(output)
	if !strings.Contains(text, "--listen") || !strings.Contains(text, "stdio://") || !strings.Contains(text, "--config") {
		t.Fatalf("unexpected app-server command surface:\n%s", text)
	}

	base := t.TempDir()
	home := filepath.Join(base, "home")
	work := filepath.Join(base, "work")
	tmp := filepath.Join(base, "tmp")
	for _, dir := range []string{home, work, tmp} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, appServerArgs(prefix)...)
	cmd.Dir = work
	cmd.Env = isolatedEnvironment(home, tmp)
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start app-server with production arguments: %v", err)
	}
	defer func() {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	if err := json.NewEncoder(stdin).Encode(map[string]any{
		"id": 1, "method": "initialize", "params": map[string]any{
			"clientInfo":   map[string]string{"name": "cantinarr-test", "title": "Cantinarr Test", "version": "1"},
			"capabilities": map[string]any{"experimentalApi": true},
		},
	}); err != nil {
		t.Fatalf("write initialize request: %v", err)
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), maxProtocolBytes)
	replies := make(chan rpcEnvelope, 64)
	scanDone := make(chan error, 1)
	go func() {
		defer close(replies)
		for scanner.Scan() {
			var response rpcEnvelope
			if json.Unmarshal(scanner.Bytes(), &response) == nil {
				replies <- response
			}
		}
		scanDone <- scanner.Err()
	}()
	readReply := func(id string) rpcEnvelope {
		t.Helper()
		for {
			select {
			case response, ok := <-replies:
				if !ok {
					t.Fatalf("app-server exited before response %s: %v", id, <-scanDone)
				}
				if string(response.ID) == id {
					return response
				}
			case <-ctx.Done():
				t.Fatalf("app-server request %s timed out", id)
			}
		}
	}

	initialize := readReply("1")
	if initialize.Error != nil {
		t.Fatalf("app-server rejected initialize with production arguments: code %d", initialize.Error.Code)
	}
	var initializeResult struct {
		CodexHome string `json:"codexHome"`
	}
	if json.Unmarshal(initialize.Result, &initializeResult) != nil || !sameDirectory(initializeResult.CodexHome, home) {
		t.Fatalf("app-server did not honor isolated CODEX_HOME: got %q, want %q", initializeResult.CodexHome, home)
	}

	encoder := json.NewEncoder(stdin)
	if err := encoder.Encode(map[string]any{"method": "initialized"}); err != nil {
		t.Fatalf("write initialized notification: %v", err)
	}
	if err := encoder.Encode(map[string]any{
		"id": 2, "method": "thread/start", "params": map[string]any{
			"cwd":                     work,
			"runtimeWorkspaceRoots":   []any{},
			"approvalPolicy":          "never",
			"sandbox":                 "read-only",
			"baseInstructions":        "Cantinarr protocol smoke test",
			"developerInstructions":   "Do not run a model turn.",
			"ephemeral":               true,
			"environments":            []any{},
			"dynamicTools":            []any{},
			"selectedCapabilityRoots": []any{},
			"config":                  restrictedThreadConfig(),
			"serviceName":             "Cantinarr",
			"threadSource":            "cantinarr",
		},
	}); err != nil {
		t.Fatalf("write thread/start request: %v", err)
	}
	threadStart := readReply("2")
	if threadStart.Error != nil {
		t.Fatalf("app-server rejected production thread/start config: code %d: %s", threadStart.Error.Code, threadStart.Error.Message)
	}
	var threadResult struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
		ApprovalPolicy string `json:"approvalPolicy"`
		Sandbox        struct {
			Type string `json:"type"`
		} `json:"sandbox"`
	}
	if err := json.Unmarshal(threadStart.Result, &threadResult); err != nil {
		t.Fatalf("decode thread/start response: %v", err)
	}
	if threadResult.Thread.ID == "" || threadResult.ApprovalPolicy != "never" || threadResult.Sandbox.Type != "readOnly" {
		t.Fatalf("unsafe or incomplete thread/start response: %#v", threadResult)
	}
}

func TestPrepareRuntimeScrubsOnlyStaleAdapterSessions(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	if err := os.Mkdir(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	staleAuth := filepath.Join(runtimeDir, "session-stale", "home", "auth.json")
	if err := os.MkdirAll(filepath.Dir(staleAuth), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staleAuth, []byte(`{"access_token":"stale-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(runtimeDir, "keep-me")
	if err := os.WriteFile(keep, []byte("unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	marker := filepath.Join(external, "marker")
	if err := os.WriteFile(marker, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(runtimeDir, "session-link")); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareRuntimeDir(runtimeDir, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staleAuth); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale plaintext auth survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "session-link")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale session symlink survived: %v", err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "preserve" {
		t.Fatalf("session symlink target was traversed: %q, %v", data, err)
	}
	if data, err := os.ReadFile(keep); err != nil || string(data) != "unrelated" {
		t.Fatalf("unrelated runtime entry was removed: %q, %v", data, err)
	}
}

func TestPrepareRuntimeRejectsBroadExistingDirectoryWithoutChmod(t *testing.T) {
	if _, err := prepareRuntimeDir("relative-runtime", true); err == nil {
		t.Fatal("relative runtime directory was accepted")
	}
	parent := t.TempDir()
	broad := filepath.Join(parent, "shared")
	if err := os.Mkdir(broad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(broad, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareRuntimeDir(broad, true); err == nil {
		t.Fatal("broad existing runtime was accepted")
	}
	info, err := os.Stat(broad)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("existing directory mode changed to %04o", info.Mode().Perm())
	}
	if _, err := prepareRuntimeDir(string(os.PathSeparator), true); err == nil {
		t.Fatal("filesystem root was accepted as runtime")
	}
}

func fakeManager(t *testing.T) (*Manager, *sql.DB, *secrets.Cipher, string, string) {
	t.Helper()
	database, err := projectdb.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if _, err := database.Exec(`
		INSERT INTO users (id, username, password_hash, role) VALUES
			(1, 'viewer-one', 'x', 'user'),
			(2, 'viewer-two', 'x', 'user')`); err != nil {
		t.Fatal(err)
	}
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	runtimeDir := filepath.Join(base, "runtime")
	logPath := filepath.Join(base, "protocol.jsonl")
	t.Setenv("CANTINARR_FAKE_PARENT_SECRET", "must-not-reach-child")
	manager := NewManager(database, cipher, mcp.NewToolServer(credentials.NewRegistry(database, cipher), nil, nil, nil), Options{
		Binary:                   os.Args[0],
		RuntimeDir:               runtimeDir,
		Args:                     []string{"-test.run=TestCodexAppHelperProcess", "--", "--fake-log=" + logPath},
		AllowDiskRuntimeForTests: true,
	})
	if !manager.Available() {
		t.Fatalf("fake manager unavailable: %v", manager.AvailabilityError())
	}
	return manager, database, cipher, runtimeDir, logPath
}

type fakeLogEntry struct {
	Kind  string          `json:"kind"`
	Value json.RawMessage `json:"value"`
}

func logFake(t *testing.T, path, kind string, value any) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open fake log: %v", err)
	}
	defer file.Close()
	if err := json.NewEncoder(file).Encode(map[string]any{"kind": kind, "value": value}); err != nil {
		t.Fatalf("write fake log: %v", err)
	}
}

func readFakeLog(t *testing.T, path string) []fakeLogEntry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entries []fakeLogEntry
	decoder := json.NewDecoder(bytes.NewReader(data))
	for decoder.More() {
		var entry fakeLogEntry
		if err := decoder.Decode(&entry); err != nil {
			t.Fatalf("decode fake log: %v", err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func waitForFakeMethod(t *testing.T, path, method string) {
	t.Helper()
	needle := []byte(`"method":"` + method + `"`)
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if data, err := os.ReadFile(path); err == nil && bytes.Contains(data, needle) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("fake app-server did not receive %s", method)
}

func fakeArg(prefix string) string {
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, prefix) {
			return strings.TrimPrefix(arg, prefix)
		}
	}
	return ""
}

func writeFakeAuth(t *testing.T, access, refresh string) {
	t.Helper()
	path := filepath.Join(os.Getenv("CODEX_HOME"), "auth.json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("open fake auth: %v", err)
	}
	if err := json.NewEncoder(file).Encode(map[string]any{"tokens": map[string]string{"access_token": access, "refresh_token": refresh}}); err != nil {
		file.Close()
		t.Fatalf("write fake auth: %v", err)
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		t.Fatalf("chmod fake auth: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close fake auth: %v", err)
	}
}

func fileMode(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%04o", info.Mode().Perm())
}

func directoryEntryCount(t *testing.T, path string) int {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("read directory: %v", err)
	}
	return len(entries)
}

func storedAuth(t *testing.T, database *sql.DB, userID int64) string {
	t.Helper()
	var stored string
	if err := database.QueryRow(`SELECT auth_blob FROM user_codex_accounts WHERE user_id = ?`, userID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	return stored
}

func assertRuntimeEmpty(t *testing.T, runtimeDir string) {
	t.Helper()
	entries, err := os.ReadDir(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("runtime retained plaintext session entries: %v", entries)
	}
}

func assertRestrictedProtocol(t *testing.T, entries []fakeLogEntry) {
	t.Helper()
	var startups []map[string]any
	var received []map[string]any
	for _, entry := range entries {
		var value map[string]any
		if json.Unmarshal(entry.Value, &value) != nil {
			continue
		}
		switch entry.Kind {
		case "startup":
			startups = append(startups, value)
		case "received":
			received = append(received, value)
		}
	}
	if len(startups) < 3 {
		t.Fatalf("expected isolated login/status/run processes, got %d", len(startups))
	}
	homes := map[string]bool{}
	for _, startup := range startups {
		if startup["parent_secret_present"] == true {
			t.Fatal("server secret environment reached app-server")
		}
		if startup["home_mode"] != "0700" || startup["cwd_entries"] != float64(0) {
			t.Fatalf("insecure process directories: %#v", startup)
		}
		home, _ := startup["home"].(string)
		if home == "" || homes[home] {
			t.Fatalf("process home was empty or reused: %q", home)
		}
		homes[home] = true
		args := stringSlice(startup["args"])
		if !hasArgPair(args, "--listen", "stdio://") || slices.Contains(args, "--stdio") || slices.Contains(args, "--disable") {
			t.Fatalf("app-server transport args are not standalone-compatible: %v", args)
		}
		for _, feature := range disabledFeatures {
			if !hasFeatureOverride(args, feature) {
				t.Fatalf("process args did not disable %s: %v", feature, args)
			}
		}
	}

	initialize := requestByMethod(t, received, "initialize")
	initializeParams := initialize["params"].(map[string]any)
	capabilities := initializeParams["capabilities"].(map[string]any)
	if capabilities["experimentalApi"] != true {
		t.Fatal("initialize did not negotiate experimental API")
	}
	login := requestByMethod(t, received, "account/login/start")
	if login["params"].(map[string]any)["type"] != "chatgptDeviceCode" {
		t.Fatalf("wrong login type: %#v", login)
	}
	thread := requestByMethod(t, received, "thread/start")
	params := thread["params"].(map[string]any)
	if params["approvalPolicy"] != "never" || params["sandbox"] != "read-only" || params["ephemeral"] != true {
		t.Fatalf("thread safety fields missing: %#v", params)
	}
	if params["baseInstructions"] != "Cantinarr base prompt" || params["developerInstructions"] != "current user context" {
		t.Fatal("Cantinarr instructions were not forwarded")
	}
	for _, key := range []string{"environments", "runtimeWorkspaceRoots", "selectedCapabilityRoots"} {
		values, ok := params[key].([]any)
		if !ok || len(values) != 0 {
			t.Fatalf("%s must be an explicit empty array: %#v", key, params[key])
		}
	}
	config := params["config"].(map[string]any)
	for _, key := range []string{
		"features.shell_tool", "features.unified_exec", "features.browser_use", "features.computer_use",
		"features.image_generation", "features.apps", "features.plugins", "features.multi_agent",
		"features.shell_snapshot", "features.standalone_web_search", "features.request_permissions_tool",
		"features.memories", "features.guardian_approval", "features.goals", "features.auth_elicitation",
		"features.personality", "features.artifact", "features.realtime_conversation", "features.remote_compaction_v2",
	} {
		if config[key] != false {
			t.Fatalf("thread config did not disable %s: %#v", key, config[key])
		}
	}
	if _, invalid := config["apps.enabled"]; invalid {
		t.Fatal("thread config included invalid app-server key apps.enabled")
	}
	if config["web_search"] != "disabled" {
		t.Fatalf("web search not disabled: %#v", config["web_search"])
	}
	tools, ok := params["dynamicTools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatal("Cantinarr dynamic tools missing")
	}
	var names []string
	for _, raw := range tools {
		tool := raw.(map[string]any)
		names = append(names, tool["name"].(string))
		if tool["type"] != "function" || tool["inputSchema"] == nil {
			t.Fatalf("bad dynamic tool: %#v", tool)
		}
	}
	if !slices.Contains(names, "search_movies") || slices.Contains(names, "get_queue") {
		t.Fatalf("user RBAC tool list = %v", names)
	}
	for _, name := range []string{"shell", "exec", "browser", "computer", "view_image", "spawn_agent"} {
		if slices.Contains(names, name) {
			t.Fatalf("dangerous built-in appeared as a dynamic tool: %s", name)
		}
	}

	responses := map[string]map[string]any{}
	for _, message := range received {
		id := fmt.Sprint(message["id"])
		if (id == "preturn-tool" || id == "server-tool-1") && message["method"] == nil {
			responses[id] = message
		}
	}
	for _, id := range []string{"preturn-tool", "server-tool-1"} {
		result, _ := responses[id]["result"].(map[string]any)
		if result == nil || result["success"] != false {
			t.Fatalf("unsafe %s call was not rejected: %#v", id, responses[id])
		}
	}
}

func requestByMethod(t *testing.T, messages []map[string]any, method string) map[string]any {
	t.Helper()
	for _, message := range messages {
		if message["method"] == method {
			return message
		}
	}
	t.Fatalf("protocol request %s not found", method)
	return nil
}

func stringSlice(value any) []string {
	raw, _ := value.([]any)
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func hasFeatureOverride(args []string, feature string) bool {
	return hasArgPair(args, "-c", "features."+feature+"=false")
}

func hasArgPair(args []string, first, second string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == first && args[i+1] == second {
			return true
		}
	}
	return false
}

type blockingWriteCloser struct {
	closed    chan struct{}
	started   chan struct{}
	once      sync.Once
	startOnce sync.Once
}

func (w *blockingWriteCloser) Write([]byte) (int, error) {
	if w.started != nil {
		w.startOnce.Do(func() { close(w.started) })
	}
	<-w.closed
	return 0, io.ErrClosedPipe
}

func (w *blockingWriteCloser) Close() error {
	w.once.Do(func() { close(w.closed) })
	return nil
}
