package codexapp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type rpcErrorBody struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type rpcEnvelope struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcErrorBody   `json:"error,omitempty"`
}

type rpcReply struct {
	result json.RawMessage
	err    *rpcErrorBody
}

type rpcNotification struct {
	method string
	params json.RawMessage
}

type serverRequestHandler func(context.Context, string, json.RawMessage) (any, error)

type appSession struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	rootDir  string
	homeDir  string
	workDir  string
	authPath string

	nextID    atomic.Int64
	writeOnce sync.Once
	writeSlot chan struct{}

	pendingMu sync.Mutex
	pending   map[string]chan rpcReply

	handlerMu      sync.RWMutex
	handler        serverRequestHandler
	serverRequests chan struct{}
	globalRequests chan struct{}
	requestContext context.Context
	cancelRequests context.CancelFunc
	activeRequests atomic.Int32

	notifications chan rpcNotification
	processDone   chan struct{}
	exitMu        sync.Mutex
	exitErr       error
	stopOnce      sync.Once
	cleanupOnce   sync.Once
	releaseSlot   func()
}

func (m *Manager) startSession(authJSON []byte) (*appSession, error) {
	if err := validateManager(m); err != nil {
		return nil, err
	}
	select {
	case m.processSlots <- struct{}{}:
	default:
		return nil, ErrProvider
	}
	releaseSlot := func() {
		select {
		case <-m.processSlots:
		default:
		}
	}
	root, err := os.MkdirTemp(m.runtimeDir, "session-")
	if err != nil {
		releaseSlot()
		return nil, ErrUnavailable
	}
	cleanup := func() {
		_ = os.RemoveAll(root)
		releaseSlot()
	}
	if err := os.Chmod(root, 0o700); err != nil {
		cleanup()
		return nil, ErrUnavailable
	}
	home := filepath.Join(root, "home")
	work := filepath.Join(root, "work")
	tmp := filepath.Join(root, "tmp")
	for _, dir := range []string{home, work, tmp} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			cleanup()
			return nil, ErrUnavailable
		}
	}
	authPath := filepath.Join(home, "auth.json")
	if len(authJSON) != 0 {
		if !validAuthJSON(authJSON) {
			cleanup()
			return nil, ErrStorage
		}
		file, err := os.OpenFile(authPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			cleanup()
			return nil, ErrStorage
		}
		if _, err := file.Write(authJSON); err != nil {
			file.Close()
			cleanup()
			return nil, ErrStorage
		}
		if err := file.Sync(); err != nil {
			file.Close()
			cleanup()
			return nil, ErrStorage
		}
		if err := file.Close(); err != nil {
			cleanup()
			return nil, ErrStorage
		}
	}

	args := appServerArgs(m.args)
	cmd := exec.Command(m.binary, args...)
	cmd.Dir = work
	cmd.Env = isolatedEnvironment(home, tmp)
	configureChildProcess(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cleanup()
		return nil, ErrProvider
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cleanup()
		return nil, ErrProvider
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cleanup()
		return nil, ErrProvider
	}
	if err := cmd.Start(); err != nil {
		cleanup()
		return nil, ErrProvider
	}

	requestContext, cancelRequests := context.WithCancel(context.Background())
	s := &appSession{
		cmd:            cmd,
		stdin:          stdin,
		rootDir:        root,
		homeDir:        home,
		workDir:        work,
		authPath:       authPath,
		pending:        make(map[string]chan rpcReply),
		notifications:  make(chan rpcNotification, maxQueuedNotifications),
		processDone:    make(chan struct{}),
		releaseSlot:    releaseSlot,
		serverRequests: make(chan struct{}, maxServerRequests),
		globalRequests: m.serverRequestSlots,
		requestContext: requestContext,
		cancelRequests: cancelRequests,
	}
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	go s.readLoop(stdout)
	go func() {
		err := cmd.Wait()
		s.exitMu.Lock()
		s.exitErr = err
		s.exitMu.Unlock()
		close(s.processDone)
		s.failPending()
	}()
	return s, nil
}

func appServerArgs(base []string) []string {
	args := append([]string(nil), base...)
	for _, feature := range disabledFeatures {
		// The full `codex app-server` wrapper accepts feature flags, but the
		// standalone app-server does not. Configuration overrides are part of
		// both command surfaces and keep the process-wide lockdown identical.
		args = append(args, "-c", "features."+feature+"=false")
	}
	// `--listen stdio://` is supported by both the standalone app-server and
	// the `codex app-server` wrapper. The shorter `--stdio` alias has existed
	// only on some wrappers and must not be used for the bundled standalone.
	args = append(args, "--listen", "stdio://")
	return args
}

func isolatedEnvironment(home, tmp string) []string {
	allowed := map[string]bool{
		"PATH": true, "LANG": true, "LC_ALL": true, "TZ": true,
		"SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
		"HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true, "NO_PROXY": true,
		"http_proxy": true, "https_proxy": true, "all_proxy": true, "no_proxy": true,
	}
	env := make([]string, 0, len(allowed)+6)
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if ok && allowed[key] {
			env = append(env, entry)
		}
	}
	env = append(env,
		"CODEX_HOME="+home,
		"HOME="+home,
		"TMPDIR="+tmp,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"NO_COLOR=1",
	)
	return env
}

func (s *appSession) initialize(ctx context.Context) error {
	var response struct {
		CodexHome string `json:"codexHome"`
	}
	err := s.request(ctx, "initialize", map[string]any{
		"clientInfo": map[string]string{
			"name":    "cantinarr",
			"title":   "Cantinarr",
			"version": "1",
		},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	}, &response)
	if err != nil {
		return err
	}
	// A mismatched home means this process could be sharing another account.
	// Compare directory identity instead of path spelling because the process
	// may canonicalize a platform alias such as macOS /var -> /private/var.
	if !sameDirectory(response.CodexHome, s.homeDir) {
		return ErrProvider
	}
	return s.notify(ctx, "initialized", nil)
}

func sameDirectory(first, second string) bool {
	if !filepath.IsAbs(first) || !filepath.IsAbs(second) {
		return false
	}
	firstInfo, err := os.Stat(first)
	if err != nil || !firstInfo.IsDir() {
		return false
	}
	secondInfo, err := os.Stat(second)
	return err == nil && secondInfo.IsDir() && os.SameFile(firstInfo, secondInfo)
}

func (s *appSession) setRequestHandler(handler serverRequestHandler) {
	s.handlerMu.Lock()
	s.handler = handler
	s.handlerMu.Unlock()
}

func (s *appSession) request(ctx context.Context, method string, params any, out any) error {
	id := s.nextID.Add(1)
	key := strconv.FormatInt(id, 10)
	response := make(chan rpcReply, 1)
	s.pendingMu.Lock()
	s.pending[key] = response
	s.pendingMu.Unlock()
	defer func() {
		s.pendingMu.Lock()
		delete(s.pending, key)
		s.pendingMu.Unlock()
	}()

	message := map[string]any{"id": id, "method": method}
	if params != nil {
		message["params"] = params
	}
	if err := s.writeContext(ctx, message); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrProvider
	}
	select {
	case reply := <-response:
		if reply.err != nil {
			return classifyRPCError(reply.err)
		}
		if out != nil {
			if len(reply.result) == 0 || json.Unmarshal(reply.result, out) != nil {
				return ErrProvider
			}
		}
		return nil
	case <-s.processDone:
		return ErrProvider
	case <-ctx.Done():
		return ctx.Err()
	}
}

func classifyRPCError(rpcErr *rpcErrorBody) error {
	if rpcErr == nil {
		return ErrProvider
	}
	text := strings.ToLower(rpcErr.Message + " " + string(rpcErr.Data))
	switch {
	case strings.Contains(text, "unauthorized"),
		strings.Contains(text, "authentication_required"),
		strings.Contains(text, "not logged in"),
		strings.Contains(text, "invalid_grant"):
		return ErrNotConnected
	case strings.Contains(text, "usagelimitexceeded"),
		strings.Contains(text, "usage_limit"),
		strings.Contains(text, "rate_limit_reached"):
		return ErrUsageLimit
	default:
		return ErrProvider
	}
}

func (s *appSession) notify(ctx context.Context, method string, params any) error {
	message := map[string]any{"method": method}
	if params != nil {
		message["params"] = params
	}
	if err := s.writeContext(ctx, message); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrProvider
	}
	return nil
}

func (s *appSession) write(message any) error {
	ctx := s.requestContext
	if ctx == nil {
		ctx = context.Background()
	}
	writeCtx, cancel := context.WithTimeout(ctx, maxServerWrite)
	defer cancel()
	return s.writeContext(writeCtx, message)
}

func (s *appSession) writeContext(ctx context.Context, message any) error {
	encoded, err := json.Marshal(message)
	if err != nil || len(encoded) > maxProtocolBytes {
		return ErrProvider
	}
	encoded = append(encoded, '\n')
	s.writeOnce.Do(func() { s.writeSlot = make(chan struct{}, 1) })
	select {
	case s.writeSlot <- struct{}{}:
		defer func() { <-s.writeSlot }()
	case <-ctx.Done():
		s.abortProvider()
		return ctx.Err()
	}
	written := make(chan error, 1)
	go func() {
		n, writeErr := s.stdin.Write(encoded)
		if writeErr == nil && n != len(encoded) {
			writeErr = io.ErrShortWrite
		}
		written <- writeErr
	}()
	select {
	case err := <-written:
		return err
	case <-ctx.Done():
		// Closing stdin and killing the child unblocks a pipe write even when a
		// buggy app-server stopped consuming input. The write goroutine reports
		// into a buffered channel, so it cannot leak waiting for this caller.
		s.abortProvider()
		return ctx.Err()
	}
}

func (s *appSession) abortProvider() {
	if s.cancelRequests != nil {
		s.cancelRequests()
	}
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

func (s *appSession) readLoop(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), maxProtocolBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || len(line) > maxProtocolBytes {
			continue
		}
		var message rpcEnvelope
		if json.Unmarshal(line, &message) != nil {
			continue
		}
		s.dispatch(message)
	}
	// A malformed/oversized stream is a failed provider process. Killing it
	// wakes every pending request without exposing scanner or process details.
	if scanner.Err() != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

func (s *appSession) dispatch(message rpcEnvelope) {
	hasID := len(message.ID) != 0 && string(message.ID) != "null"
	if message.Method != "" && hasID {
		select {
		case s.serverRequests <- struct{}{}:
			globalAcquired := false
			if s.globalRequests != nil {
				select {
				case s.globalRequests <- struct{}{}:
					globalAcquired = true
				default:
					<-s.serverRequests
					s.writeRPCError(message.ID, -32001, "request capacity reached")
					return
				}
			}
			s.activeRequests.Add(1)
			go func() {
				defer func() {
					s.activeRequests.Add(-1)
					<-s.serverRequests
					if globalAcquired {
						<-s.globalRequests
					}
				}()
				s.handleServerRequest(message)
			}()
		default:
			s.writeRPCError(message.ID, -32001, "request capacity reached")
		}
		return
	}
	if message.Method != "" {
		switch message.Method {
		case "account/login/completed", "item/agentMessage/delta", "turn/completed":
		default:
			return
		}
		params, ok := compactNotification(message.Method, message.Params)
		if !ok {
			s.abortProvider()
			return
		}
		ctx := s.requestContext
		if ctx == nil {
			ctx = context.Background()
		}
		select {
		case s.notifications <- rpcNotification{method: message.Method, params: params}:
		case <-ctx.Done():
		}
		return
	}
	if !hasID {
		return
	}
	key := strings.TrimSpace(string(message.ID))
	s.pendingMu.Lock()
	response := s.pending[key]
	s.pendingMu.Unlock()
	if response != nil {
		select {
		case response <- rpcReply{result: cloneRaw(message.Result), err: message.Error}:
		default:
			s.abortProvider()
		}
	}
}

func compactNotification(method string, params json.RawMessage) (json.RawMessage, bool) {
	if method != "turn/completed" {
		if len(params) > maxNotificationBytes {
			return nil, false
		}
		return cloneRaw(params), true
	}
	var complete turnCompleteParams
	if json.Unmarshal(params, &complete) != nil {
		return nil, false
	}
	if complete.Turn.Error != nil {
		complete.Turn.Error.CodexErrorInfo = compactTurnError(complete.Turn.Error.CodexErrorInfo)
	}
	totalText := 0
	for _, item := range complete.Turn.Items {
		if item.Type == "agentMessage" {
			totalText += len(item.Text)
		}
	}
	// Find the largest agent-message fallback budget that still keeps the
	// compact notification bounded after JSON escaping. Dynamic-tool items and
	// their potentially multi-megabyte contentItems are never copied.
	low, high := 0, min(totalText, maxNotificationBytes)
	var best json.RawMessage
	for low <= high {
		budget := low + (high-low)/2
		candidate, err := marshalCompactTurn(complete, budget)
		if err != nil {
			return nil, false
		}
		if len(candidate) <= maxNotificationBytes {
			best = candidate
			low = budget + 1
		} else {
			high = budget - 1
		}
	}
	return best, len(best) != 0
}

func marshalCompactTurn(complete turnCompleteParams, textBudget int) (json.RawMessage, error) {
	items := complete.Turn.Items[:0:0]
	remaining := textBudget
	for _, item := range complete.Turn.Items {
		if item.Type != "agentMessage" || item.Text == "" || remaining == 0 {
			continue
		}
		if len(item.Text) > remaining {
			item.Text = item.Text[:remaining]
		}
		remaining -= len(item.Text)
		items = append(items, item)
	}
	complete.Turn.Items = items
	encoded, err := json.Marshal(complete)
	return json.RawMessage(encoded), err
}

func compactTurnError(raw json.RawMessage) json.RawMessage {
	const maxClassifiedErrorBytes = 64 << 10
	if len(raw) > maxClassifiedErrorBytes {
		raw = raw[:maxClassifiedErrorBytes]
	}
	text := strings.ToLower(string(raw))
	switch {
	case strings.Contains(text, "usagelimitexceeded"), strings.Contains(text, "usage_limit"):
		return json.RawMessage(`"usageLimitExceeded"`)
	case strings.Contains(text, "unauthorized"), strings.Contains(text, "authentication_required"):
		return json.RawMessage(`"unauthorized"`)
	default:
		return json.RawMessage(`"providerError"`)
	}
}

func (s *appSession) handleServerRequest(message rpcEnvelope) {
	s.handlerMu.RLock()
	handler := s.handler
	s.handlerMu.RUnlock()
	if handler == nil {
		s.writeRPCError(message.ID, -32601, "request is disabled")
		return
	}
	ctx := s.requestContext
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := handler(ctx, message.Method, cloneRaw(message.Params))
	if err != nil {
		s.writeRPCError(message.ID, -32601, "request is disabled")
		return
	}
	response := map[string]any{"id": json.RawMessage(message.ID), "result": result}
	_ = s.write(response)
}

func (s *appSession) writeRPCError(id json.RawMessage, code int, message string) {
	_ = s.write(map[string]any{
		"id": json.RawMessage(id),
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func (s *appSession) failPending() {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	for _, response := range s.pending {
		select {
		case response <- rpcReply{err: &rpcErrorBody{Code: -32000, Message: "process ended"}}:
		default:
		}
	}
}

func (s *appSession) stop() {
	s.stopOnce.Do(func() {
		if s.cancelRequests != nil {
			s.cancelRequests()
		}
		_ = s.stdin.Close()
		select {
		case <-s.processDone:
		case <-time.After(2 * time.Second):
			if s.cmd.Process != nil {
				_ = s.cmd.Process.Kill()
			}
			<-s.processDone
		}
		s.waitForRequests()
	})
}

func (s *appSession) waitForRequests() {
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for s.activeRequests.Load() != 0 {
		select {
		case <-deadline.C:
			return
		case <-ticker.C:
		}
	}
}

func (s *appSession) cleanup() {
	s.cleanupOnce.Do(func() {
		_ = os.RemoveAll(s.rootDir)
		if s.releaseSlot != nil {
			s.releaseSlot()
		}
	})
}

func (s *appSession) readAuthJSON() ([]byte, error) {
	info, err := os.Lstat(s.authPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxAuthFileBytes {
		return nil, ErrStorage
	}
	if err := os.Chmod(s.authPath, 0o600); err != nil {
		return nil, ErrStorage
	}
	file, err := os.Open(s.authPath)
	if err != nil {
		return nil, ErrStorage
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxAuthFileBytes+1))
	if err != nil || len(data) > maxAuthFileBytes || !validAuthJSON(data) {
		return nil, ErrStorage
	}
	return data, nil
}

func validAuthJSON(data []byte) bool {
	if len(data) == 0 || len(data) > maxAuthFileBytes || !json.Valid(data) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil || object == nil {
		return false
	}
	var extra any
	return errors.Is(decoder.Decode(&extra), io.EOF)
}
