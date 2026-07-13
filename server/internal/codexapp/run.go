package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/windoze95/cantinarr-server/internal/mcp"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

type callbackSink struct {
	mu sync.Mutex
	cb Callbacks
}

func (s *callbackSink) text(text string) {
	if text == "" || s.cb.OnText == nil {
		return
	}
	s.mu.Lock()
	s.cb.OnText(text)
	s.mu.Unlock()
}

func (s *callbackSink) toolStart(name string) {
	if s.cb.OnToolStart == nil {
		return
	}
	s.mu.Lock()
	s.cb.OnToolStart(name)
	s.mu.Unlock()
}

func (s *callbackSink) toolEnd(name string, ok bool) {
	if s.cb.OnToolEnd == nil {
		return
	}
	s.mu.Lock()
	s.cb.OnToolEnd(name, ok)
	s.mu.Unlock()
}

func (s *callbackSink) toolResult(name string, data any) {
	if s.cb.OnToolResult == nil {
		return
	}
	s.mu.Lock()
	s.cb.OnToolResult(name, data)
	s.mu.Unlock()
}

func (s *callbackSink) toolRecord(name string, input json.RawMessage, result string, isError bool) {
	if s.cb.OnToolRecord == nil {
		return
	}
	s.mu.Lock()
	s.cb.OnToolRecord(name, cloneRaw(input), result, isError)
	s.mu.Unlock()
}

type dynamicToolParams struct {
	CallID    string          `json:"callId"`
	ThreadID  string          `json:"threadId"`
	TurnID    string          `json:"turnId"`
	Tool      string          `json:"tool"`
	Namespace *string         `json:"namespace"`
	Arguments json.RawMessage `json:"arguments"`
}

type turnCompleteParams struct {
	ThreadID string `json:"threadId"`
	Turn     struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Items  []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"items"`
		Error *struct {
			CodexErrorInfo json.RawMessage `json:"codexErrorInfo"`
		} `json:"error"`
	} `json:"turn"`
}

// Run executes one ephemeral, dynamic-tools-only Codex turn. baseInstructions
// replaces Codex's coding-agent prompt; developerInstructions carries the
// caller/date/service context. Every account-backed process is serialized per
// user so rotating refresh tokens and auth.json snapshots cannot race.
func (m *Manager) Run(
	ctx context.Context,
	userID int64,
	role, model, baseInstructions, developerInstructions, prompt string,
	callbacks Callbacks,
) (err error) {
	if err := validateManager(m); err != nil {
		return err
	}
	if strings.TrimSpace(baseInstructions) == "" || strings.TrimSpace(prompt) == "" {
		return ErrInvalidInput
	}
	runCtx, cancel := context.WithTimeout(ctx, maxRunDuration)
	defer cancel()
	if err := m.acquireUser(runCtx, userID); err != nil {
		return err
	}
	defer m.releaseUser(userID)
	var operation *userOperation
	runCtx, operation, err = m.registerUserOperation(runCtx, userID)
	if err != nil {
		return err
	}
	defer m.unregisterUserOperation(userID, operation)
	record, found, err := m.loadAccount(userID)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotConnected
	}
	session, err := m.startSession(record.authJSON)
	if err != nil {
		return err
	}
	defer func() {
		if errors.Is(err, ErrNotConnected) {
			session.stop()
			session.cleanup()
			_ = m.deleteAccount(userID)
			return
		}
		persistErr := m.finishAccountSession(userID, session, nil, operation)
		if err == nil && persistErr != nil {
			err = persistErr
		}
	}()
	if initErr := session.initialize(runCtx); initErr != nil {
		return contextOrClassified(runCtx, initErr)
	}

	tools := m.toolServer.GetToolsForRole(role)
	allowed := make(map[string]bool, len(tools))
	dynamicTools := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		allowed[tool.Name] = true
		dynamicTools = append(dynamicTools, map[string]any{
			"type":         "function",
			"name":         tool.Name,
			"description":  tool.Description,
			"inputSchema":  tool.InputSchema,
			"deferLoading": false,
		})
	}

	sink := &callbackSink{cb: callbacks}
	var stateMu sync.RWMutex
	threadID := ""
	turnID := ""
	toolCalls := 0
	limitHit := make(chan struct{})
	var limitOnce sync.Once
	session.setRequestHandler(func(callCtx context.Context, method string, raw json.RawMessage) (any, error) {
		if method != "item/tool/call" {
			return nil, ErrInvalidInput
		}
		var call dynamicToolParams
		if len(raw) > maxProtocolBytes || json.Unmarshal(raw, &call) != nil || call.CallID == "" || call.Tool == "" || call.Namespace != nil {
			return nil, ErrInvalidInput
		}
		stateMu.Lock()
		currentThread, currentTurn := threadID, turnID
		toolCalls++
		count := toolCalls
		stateMu.Unlock()
		if count > maxDynamicToolCalls {
			limitOnce.Do(func() { close(limitHit) })
			return dynamicToolResponse("Tool call limit reached.", false), nil
		}
		if currentThread == "" || call.ThreadID != currentThread || currentTurn == "" || call.TurnID != currentTurn || !allowed[call.Tool] {
			return dynamicToolResponse("This tool is not available.", false), nil
		}
		input := cloneRaw(call.Arguments)
		if len(input) == 0 || string(input) == "null" {
			input = json.RawMessage("{}")
		}
		if len(input) > maxAuthFileBytes || !json.Valid(input) {
			return dynamicToolResponse("Tool input was invalid.", false), nil
		}
		if callCtx.Err() != nil {
			return dynamicToolResponse("Tool call was canceled.", false), nil
		}

		sink.toolStart(call.Tool)
		result, toolErr := m.toolServer.ExecuteTool(callCtx, call.Tool, input, mcp.CallContext{UserID: userID, Role: role})
		// A contextless downstream client may finish after the session was
		// canceled. Never emit late SSE callbacks or history records into a
		// request that has already returned.
		if callCtx.Err() != nil {
			return dynamicToolResponse("Tool call was canceled.", false), nil
		}
		if toolErr != nil {
			safe := secrets.RedactError(toolErr)
			text := "Tool failed."
			if safe != nil && safe.Error() != "" {
				text = boundedToolText("Error: " + safe.Error())
			}
			sink.toolEnd(call.Tool, false)
			sink.toolRecord(call.Tool, input, text, true)
			return dynamicToolResponse(text, false), nil
		}
		text := boundedToolText(result.Text)
		if result.StructuredData != nil && mcp.ToolsWithUI[call.Tool] {
			sink.toolResult(call.Tool, result.StructuredData)
		}
		sink.toolEnd(call.Tool, true)
		sink.toolRecord(call.Tool, input, text, false)
		return dynamicToolResponse(text, true), nil
	})

	threadParams := map[string]any{
		"cwd":                     session.workDir,
		"runtimeWorkspaceRoots":   []any{},
		"approvalPolicy":          "never",
		"sandbox":                 "read-only",
		"baseInstructions":        baseInstructions,
		"developerInstructions":   developerInstructions,
		"ephemeral":               true,
		"environments":            []any{},
		"dynamicTools":            dynamicTools,
		"selectedCapabilityRoots": []any{},
		"config":                  restrictedThreadConfig(),
		"serviceName":             "Cantinarr",
		"threadSource":            "cantinarr",
	}
	if model != "" {
		threadParams["model"] = model
	}
	var threadStart struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if requestErr := session.request(runCtx, "thread/start", threadParams, &threadStart); requestErr != nil {
		return contextOrClassified(runCtx, requestErr)
	}
	if threadStart.Thread.ID == "" {
		return ErrProvider
	}
	stateMu.Lock()
	threadID = threadStart.Thread.ID
	stateMu.Unlock()

	var turnStart struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if requestErr := session.request(runCtx, "turn/start", map[string]any{
		"threadId": threadStart.Thread.ID,
		"input": []map[string]any{{
			"type": "text",
			"text": prompt,
		}},
	}, &turnStart); requestErr != nil {
		return contextOrClassified(runCtx, requestErr)
	}
	if turnStart.Turn.ID == "" {
		return ErrProvider
	}
	stateMu.Lock()
	turnID = turnStart.Turn.ID
	stateMu.Unlock()

	textSeen := false
	for {
		select {
		case <-runCtx.Done():
			interruptTurn(session, threadStart.Thread.ID, turnStart.Turn.ID)
			return runCtx.Err()
		case <-limitHit:
			interruptTurn(session, threadStart.Thread.ID, turnStart.Turn.ID)
			return ErrProvider
		case <-session.processDone:
			return ErrProvider
		case notification := <-session.notifications:
			switch notification.method {
			case "item/agentMessage/delta":
				var delta struct {
					ThreadID string `json:"threadId"`
					TurnID   string `json:"turnId"`
					Delta    string `json:"delta"`
				}
				if json.Unmarshal(notification.params, &delta) == nil && delta.ThreadID == threadStart.Thread.ID && delta.TurnID == turnStart.Turn.ID {
					textSeen = textSeen || delta.Delta != ""
					sink.text(delta.Delta)
				}
			case "turn/completed":
				var complete turnCompleteParams
				if json.Unmarshal(notification.params, &complete) != nil || complete.ThreadID != threadStart.Thread.ID || complete.Turn.ID != turnStart.Turn.ID {
					continue
				}
				if complete.Turn.Status == "completed" {
					if !textSeen {
						for _, item := range complete.Turn.Items {
							if item.Type == "agentMessage" && item.Text != "" {
								sink.text(item.Text)
							}
						}
					}
					return nil
				}
				return safeTurnError(complete)
			}
		}
	}
}

func contextOrClassified(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, ErrNotConnected) {
		return ErrNotConnected
	}
	if errors.Is(err, ErrUsageLimit) {
		return ErrUsageLimit
	}
	return ErrProvider
}

func restrictedThreadConfig() map[string]any {
	return map[string]any{
		"features.shell_tool":                      false,
		"features.unified_exec":                    false,
		"features.browser_use":                     false,
		"features.browser_use_external":            false,
		"features.browser_use_full_cdp_access":     false,
		"features.in_app_browser":                  false,
		"features.computer_use":                    false,
		"features.image_generation":                false,
		"features.apps":                            false,
		"apps.enabled":                             false,
		"features.plugins":                         false,
		"features.remote_plugin":                   false,
		"features.plugin_sharing":                  false,
		"features.multi_agent":                     false,
		"features.multi_agent_v2":                  false,
		"features.code_mode":                       false,
		"features.code_mode_host":                  false,
		"features.code_mode_only":                  false,
		"features.hooks":                           false,
		"features.artifact":                        false,
		"features.auth_elicitation":                false,
		"features.current_time_reminder":           false,
		"features.default_mode_request_user_input": false,
		"features.enable_mcp_apps":                 false,
		"features.goals":                           false,
		"features.guardian_approval":               false,
		"features.memories":                        false,
		"features.personality":                     false,
		"features.realtime_conversation":           false,
		"features.remote_compaction_v2":            false,
		"features.request_permissions_tool":        false,
		"features.shell_snapshot":                  false,
		"features.skill_mcp_dependency_install":    false,
		"features.standalone_web_search":           false,
		"features.token_budget":                    false,
		"features.tool_call_mcp_elicitation":       false,
		"features.tool_suggest":                    false,
		"features.workspace_dependencies":          false,
		"features.web_search_cached":               false,
		"features.web_search_request":              false,
		"web_search":                               "disabled",
		"mcp_servers":                              map[string]any{},
		"analytics.enabled":                        false,
	}
}

func dynamicToolResponse(text string, success bool) map[string]any {
	return map[string]any{
		"contentItems": []map[string]string{{"type": "inputText", "text": boundedToolText(text)}},
		"success":      success,
	}
}

func boundedToolText(text string) string {
	const max = 256 << 10
	if len(text) <= max {
		return text
	}
	return text[:max] + "\n[output truncated]"
}

func interruptTurn(session *appSession, threadID, turnID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*devicePollInterval)
	defer cancel()
	var ignored any
	_ = session.request(ctx, "turn/interrupt", map[string]string{"threadId": threadID, "turnId": turnID}, &ignored)
}

func safeTurnError(complete turnCompleteParams) error {
	if complete.Turn.Error == nil {
		return ErrProvider
	}
	info := string(complete.Turn.Error.CodexErrorInfo)
	switch {
	case strings.Contains(info, "usageLimitExceeded"), strings.Contains(info, "usage_limit"):
		return ErrUsageLimit
	case strings.Contains(info, "unauthorized"):
		return ErrNotConnected
	default:
		return ErrProvider
	}
}
