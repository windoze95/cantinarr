package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

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

const remediationActorKey = "system:remediation"

type runBehavior struct {
	actorKey      string
	actorID       int64
	role          string
	executeTools  bool
	explicitTools []mcp.Tool
	callbacks     Callbacks
	captured      *AutonomousTurnResult
	maxOutput     int64
}

// Run executes one ephemeral, dynamic-tools-only Codex turn. baseInstructions
// replaces Codex's coding-agent prompt; developerInstructions carries the
// caller/date/service context. Every account-backed process is serialized per
// account so rotating refresh tokens and auth.json snapshots cannot race.
func (m *Manager) Run(
	ctx context.Context,
	userID int64,
	role, model, baseInstructions, developerInstructions, prompt string,
	callbacks Callbacks,
) (err error) {
	return m.RunWithAccount(ctx, PersonalAccount(userID), userID, role, model, baseInstructions, developerInstructions, prompt, callbacks)
}

// RunWithAccount executes a turn using account's authorization while all MCP
// tool authorization and attribution use the requesting Cantinarr actor.
func (m *Manager) RunWithAccount(
	ctx context.Context,
	account AccountRef,
	actorID int64,
	role, model, baseInstructions, developerInstructions, prompt string,
	callbacks Callbacks,
) (err error) {
	return m.runWithAccount(ctx, account, model, baseInstructions, developerInstructions, prompt, runBehavior{
		actorKey:     "user:" + strconv.FormatInt(actorID, 10),
		actorID:      actorID,
		role:         role,
		executeTools: true,
		callbacks:    callbacks,
	})
}

// ProbeAccount completes one tiny tool-free response using the exact account
// and model selected at an AI settings boundary. It is intentionally separate
// from interactive chat attribution and from the remediation-only shared turn.
func (m *Manager) ProbeAccount(ctx context.Context, account AccountRef, model string) error {
	var result AutonomousTurnResult
	actorKey := "system:ai-probe:shared"
	if !account.shared {
		actorKey = "system:ai-probe:user:" + strconv.FormatInt(account.userID, 10)
	}
	err := m.runWithAccount(ctx, account, model,
		"You are checking whether an AI provider is ready. Do not use tools. Return one short plain-text response.",
		"This is a Cantinarr provider readiness check.",
		"Reply with exactly: OK",
		runBehavior{
			actorKey:  actorKey,
			captured:  &result,
			maxOutput: 256,
		})
	if err != nil {
		return err
	}
	if strings.TrimSpace(result.Text) == "" {
		return ErrProvider
	}
	return nil
}

// RunSharedAutonomousTurn runs one server-owned Codex turn against only the
// admin-owned shared account, with an exact dynamic-tool surface. Tool requests
// are validated and returned as data; they are never executed inside app-server.
// This keeps remediation's Go Runner as the only tool dispatcher and makes a
// personal account unrepresentable at this credential boundary.
func (m *Manager) RunSharedAutonomousTurn(
	ctx context.Context,
	model, baseInstructions, developerInstructions, prompt string,
	tools []mcp.Tool,
	maxOutputTokens int64,
) (AutonomousTurnResult, error) {
	var result AutonomousTurnResult
	err := m.runWithAccount(ctx, SharedAccount(), model, baseInstructions, developerInstructions, prompt, runBehavior{
		actorKey:      remediationActorKey,
		explicitTools: append([]mcp.Tool(nil), tools...),
		captured:      &result,
		maxOutput:     maxOutputTokens,
	})
	if err != nil {
		return AutonomousTurnResult{}, err
	}
	return result, nil
}

func (m *Manager) runWithAccount(
	ctx context.Context,
	account AccountRef,
	model, baseInstructions, developerInstructions, prompt string,
	behavior runBehavior,
) (err error) {
	if err := validateManager(m); err != nil {
		return err
	}
	if !account.valid() || behavior.actorKey == "" || strings.TrimSpace(baseInstructions) == "" || strings.TrimSpace(prompt) == "" ||
		(behavior.executeTools && behavior.actorID <= 0) || (behavior.executeTools == (behavior.captured != nil)) ||
		(behavior.captured != nil && behavior.maxOutput <= 0) {
		return ErrInvalidInput
	}
	if !m.tryAcquireActorRun(behavior.actorKey) {
		return ErrBusy
	}
	defer m.releaseActorRun(behavior.actorKey)
	runCtx, cancel := context.WithTimeout(ctx, maxRunDuration)
	defer cancel()
	if account.shared {
		select {
		case m.sharedWaitSlots <- struct{}{}:
			defer func() { <-m.sharedWaitSlots }()
		default:
			return ErrBusy
		}
	}
	if err := m.acquireAccount(runCtx, account); err != nil {
		return err
	}
	defer m.releaseAccount(account)
	var operation *userOperation
	runCtx, operation, err = m.registerAccountOperation(runCtx, account)
	if err != nil {
		return err
	}
	defer m.unregisterAccountOperation(account, operation)
	record, found, err := m.loadAccount(account)
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
			_ = m.deleteAccount(account)
			return
		}
		persistErr := m.finishAccountSession(account, session, nil, operation)
		if err == nil && persistErr != nil {
			err = persistErr
		}
	}()
	if initErr := session.initialize(runCtx); initErr != nil {
		return contextOrClassified(runCtx, initErr)
	}

	tools := behavior.explicitTools
	if behavior.executeTools {
		tools = m.toolServer.GetToolsForRole(behavior.role)
	}
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

	var autonomousText strings.Builder
	callbacks := behavior.callbacks
	if behavior.captured != nil {
		callbacks.OnText = func(delta string) {
			remaining := maxProtocolBytes - autonomousText.Len()
			if remaining <= 0 {
				return
			}
			if len(delta) > remaining {
				delta = delta[:remaining]
			}
			autonomousText.WriteString(delta)
		}
	}
	sink := &callbackSink{cb: callbacks}
	var stateMu sync.RWMutex
	threadID := ""
	turnID := ""
	toolCalls := 0
	seenCallIDs := make(map[string]struct{})
	turnReady := make(chan struct{})
	limitHit := make(chan struct{})
	var limitOnce sync.Once
	toolCaptured := make(chan struct{})
	var capturedOnce sync.Once
	outputLimitHit := make(chan struct{})
	var outputLimitOnce sync.Once
	session.setRequestHandler(func(callCtx context.Context, method string, raw json.RawMessage) (any, error) {
		if method != "item/tool/call" {
			return nil, ErrInvalidInput
		}
		var call dynamicToolParams
		if len(raw) > maxProtocolBytes || json.Unmarshal(raw, &call) != nil || call.CallID == "" || len(call.CallID) > 256 || call.Tool == "" || call.Namespace != nil {
			return nil, ErrInvalidInput
		}
		stateMu.Lock()
		currentThread, currentTurn := threadID, turnID
		toolCalls++
		count := toolCalls
		stateMu.Unlock()
		// A well-behaved app-server can write the turn/start response and its
		// first tool request back-to-back. The read loop may dispatch that request
		// just before this goroutine publishes turnID. Briefly wait for that local
		// handoff, while still rejecting a request sent before turn/start actually
		// completed (the timeout prevents a protocol deadlock).
		if currentThread != "" && currentTurn == "" && call.ThreadID == currentThread {
			select {
			case <-turnReady:
				stateMu.RLock()
				currentThread, currentTurn = threadID, turnID
				stateMu.RUnlock()
			case <-time.After(50 * time.Millisecond):
			}
		}
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
		if behavior.captured != nil {
			stateMu.Lock()
			if _, duplicate := seenCallIDs[call.CallID]; duplicate {
				stateMu.Unlock()
				return dynamicToolResponse("Duplicate tool call rejected.", false), nil
			}
			seenCallIDs[call.CallID] = struct{}{}
			behavior.captured.ToolCalls = append(behavior.captured.ToolCalls, TurnToolCall{
				ID: call.CallID, Name: call.Tool, Input: input,
			})
			stateMu.Unlock()
			return serverRequestResult{
				value: dynamicToolResponse("Tool call accepted for Cantinarr's guarded runner; execution continues outside this turn.", true),
				afterWrite: func() {
					capturedOnce.Do(func() { close(toolCaptured) })
				},
			}, nil
		}

		sink.toolStart(call.Tool)
		callContext := mcp.CallContext{UserID: behavior.actorID, Role: behavior.role}
		if m.toolCallObserver != nil {
			m.toolCallObserver(callContext)
		}
		result, toolErr := m.toolServer.ExecuteTool(callCtx, call.Tool, input, callContext)
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
	close(turnReady)
	applyTokenUsage := func(params json.RawMessage) bool {
		if behavior.captured == nil {
			return false
		}
		update, ok := decodeTokenUsageUpdate(params)
		if !ok || update.ThreadID != threadStart.Thread.ID || update.TurnID != turnStart.Turn.ID {
			return false
		}
		usage := *update.TokenUsage.Last
		stateMu.Lock()
		behavior.captured.Usage = usage
		reached := usage.OutputTokens >= behavior.maxOutput
		if reached {
			behavior.captured.OutputLimitReached = true
		}
		stateMu.Unlock()
		return reached
	}
	textSeen := false
	finishCapturedText := func() {
		if behavior.captured == nil {
			return
		}
		stateMu.Lock()
		defer stateMu.Unlock()
		// app-server must receive a dynamic-tool response so it can finish the
		// protocol turn. Any text it emits after that placeholder is discarded;
		// the Runner executes the tool and supplies the authoritative result on
		// the next turn.
		if len(behavior.captured.ToolCalls) == 0 {
			behavior.captured.Text = autonomousText.String()
		}
	}
	handleNotification := func(notification rpcNotification, allowInterrupted bool) (bool, error) {
		switch notification.method {
		case "thread/tokenUsage/updated":
			if applyTokenUsage(notification.params) {
				outputLimitOnce.Do(func() { close(outputLimitHit) })
			}
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
				return false, nil
			}
			if complete.Turn.Status != "completed" && !(allowInterrupted && complete.Turn.Status == "interrupted") {
				return false, safeTurnError(complete)
			}
			if !textSeen {
				for _, item := range complete.Turn.Items {
					if item.Type == "agentMessage" && item.Text != "" {
						sink.text(item.Text)
					}
				}
			}
			finishCapturedText()
			return true, nil
		}
		return false, nil
	}
	// The turn/interrupt RPC only requests cancellation. Codex documents the
	// matching turn/completed notification as the authoritative completion
	// boundary, so keep pumping bounded notifications until both arrive. This
	// also preserves usage and partial text emitted after the interrupt ACK.
	waitForInterruptedTurn := func() error {
		interruptCtx, cancelInterrupt := context.WithTimeout(runCtx, 2*devicePollInterval)
		defer cancelInterrupt()
		ack := make(chan error, 1)
		go func() {
			var ignored any
			ack <- session.request(interruptCtx, "turn/interrupt", map[string]string{
				"threadId": threadStart.Thread.ID,
				"turnId":   turnStart.Turn.ID,
			}, &ignored)
		}()
		ackDone, turnDone := false, false
		for !ackDone || !turnDone {
			select {
			case <-ack:
				ackDone = true
			case notification, ok := <-session.notifications:
				if !ok {
					return ErrProvider
				}
				completed, err := handleNotification(notification, true)
				if err != nil {
					return err
				}
				turnDone = turnDone || completed
			case <-session.processDone:
				if turnDone {
					return nil
				}
				return ErrProvider
			case <-interruptCtx.Done():
				if turnDone {
					return nil
				}
				return interruptCtx.Err()
			}
		}
		// A matching completion is definitive even if an already-finished turn
		// made the interrupt request itself return an error.
		return nil
	}

	for {
		select {
		case <-runCtx.Done():
			interruptTurn(session, threadStart.Thread.ID, turnStart.Turn.ID)
			return runCtx.Err()
		case <-limitHit:
			interruptTurn(session, threadStart.Thread.ID, turnStart.Turn.ID)
			return ErrProvider
		case <-toolCaptured:
			return waitForInterruptedTurn()
		case <-outputLimitHit:
			return waitForInterruptedTurn()
		case <-session.processDone:
			return ErrProvider
		case notification := <-session.notifications:
			completed, err := handleNotification(notification, false)
			if err != nil {
				return err
			}
			if completed {
				return nil
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
