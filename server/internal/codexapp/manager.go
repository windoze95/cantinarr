package codexapp

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/mcp"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

const defaultRuntimeDir = "/dev/shm/cantinarr-codex"

var disabledFeatures = []string{
	"shell_tool",
	"unified_exec",
	"browser_use",
	"browser_use_external",
	"browser_use_full_cdp_access",
	"in_app_browser",
	"computer_use",
	"image_generation",
	"apps",
	"plugins",
	"remote_plugin",
	"plugin_sharing",
	"multi_agent",
	"multi_agent_v2",
	"code_mode",
	"code_mode_host",
	"code_mode_only",
	"deferred_executor",
	"enable_mcp_apps",
	"goals",
	"guardian_approval",
	"hooks",
	"artifact",
	"auth_elicitation",
	"current_time_reminder",
	"default_mode_request_user_input",
	"memories",
	"personality",
	"realtime_conversation",
	"remote_compaction_v2",
	"request_permissions_tool",
	"shell_snapshot",
	"skill_mcp_dependency_install",
	"standalone_web_search",
	"token_budget",
	"tool_call_mcp_elicitation",
	"tool_suggest",
	"workspace_dependencies",
	"web_search_cached",
	"web_search_request",
}

// Manager owns process discovery, encrypted account state, and active
// per-user device flows. It is safe for concurrent use.
type Manager struct {
	db         *sql.DB
	cipher     *secrets.Cipher
	toolServer *mcp.ToolServer

	binary                   string
	args                     []string
	runtimeDir               string
	available                bool
	allowDiskRuntimeForTests bool

	flowsMu            sync.Mutex
	flows              map[string]*deviceFlow
	accountFlows       map[AccountRef]string
	loginStarts        map[AccountRef]struct{}
	gatesMu            sync.Mutex
	accountGates       map[AccountRef]chan struct{}
	processSlots       chan struct{}
	loginSlots         chan struct{}
	serverRequestSlots chan struct{}
	sharedWaitSlots    chan struct{}
	actorRunsMu        sync.Mutex
	actorRuns          map[string]struct{}

	accountOperationTimeout time.Duration
	operationsMu            sync.Mutex
	accountOperations       map[AccountRef]*userOperation
	accountGenerations      map[AccountRef]uint64
	revocations             map[AccountRef]int
	beforeLoginPublish      func()
	afterCancelLookup       func()
	toolCallObserver        func(mcp.CallContext)
}

type userOperation struct {
	cancel     context.CancelFunc
	generation uint64
}

// NewManager constructs an adapter even when app-server is unavailable, so a
// server can still start and report that capability as disabled.
func NewManager(db *sql.DB, cipher *secrets.Cipher, tools *mcp.ToolServer, opts Options) *Manager {
	m := &Manager{
		db:                      db,
		cipher:                  cipher,
		toolServer:              tools,
		flows:                   make(map[string]*deviceFlow),
		accountFlows:            make(map[AccountRef]string),
		loginStarts:             make(map[AccountRef]struct{}),
		accountGates:            make(map[AccountRef]chan struct{}),
		processSlots:            make(chan struct{}, maxConcurrentApps),
		loginSlots:              make(chan struct{}, maxConcurrentLogins),
		serverRequestSlots:      make(chan struct{}, maxGlobalRequests),
		sharedWaitSlots:         make(chan struct{}, maxSharedWaiters),
		actorRuns:               make(map[string]struct{}),
		accountOperationTimeout: maxAccountOperation,
		accountOperations:       make(map[AccountRef]*userOperation),
		accountGenerations:      make(map[AccountRef]uint64),
		revocations:             make(map[AccountRef]int),
	}
	if db == nil || cipher == nil || tools == nil {
		return m
	}

	binary, prefix, err := discoverBinary(opts.Binary)
	if err != nil {
		return m
	}
	runtimeDir, err := prepareRuntimeDir(opts.RuntimeDir, opts.AllowDiskRuntimeForTests)
	if err != nil {
		return m
	}

	m.binary = binary
	m.args = append(prefix, opts.Args...)
	m.runtimeDir = runtimeDir
	m.available = true
	m.allowDiskRuntimeForTests = opts.AllowDiskRuntimeForTests
	return m
}

func (m *Manager) tryAcquireActorRun(actorKey string) bool {
	m.actorRunsMu.Lock()
	defer m.actorRunsMu.Unlock()
	if actorKey == "" {
		return false
	}
	if _, running := m.actorRuns[actorKey]; running {
		return false
	}
	m.actorRuns[actorKey] = struct{}{}
	return true
}

func (m *Manager) releaseActorRun(actorKey string) {
	m.actorRunsMu.Lock()
	delete(m.actorRuns, actorKey)
	m.actorRunsMu.Unlock()
}

// Available reports whether both an app-server command and a writable
// memory-backed runtime root were found.
func (m *Manager) Available() bool {
	if m == nil || !m.available {
		return false
	}
	if info, err := os.Stat(m.binary); err != nil || !info.Mode().IsRegular() {
		return false
	}
	info, err := os.Lstat(m.runtimeDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 || !runtimeDirOwnedByCurrentUser(info) {
		return false
	}
	return m.allowDiskRuntimeForTests || isMemoryBacked(m.runtimeDir)
}

// AvailabilityError is intentionally generic and contains no process output.
func (m *Manager) AvailabilityError() error {
	if m == nil || !m.Available() {
		return ErrUnavailable
	}
	return nil
}

// HasAccount reports whether encrypted account material exists for userID.
func (m *Manager) HasAccount(userID int64) bool {
	found, _ := m.AccountExists(PersonalAccount(userID))
	return found
}

func discoverBinary(override string) (string, []string, error) {
	if override != "" {
		path, err := resolveCommand(override)
		if err != nil {
			return "", nil, err
		}
		if name := filepath.Base(path); name == "codex" || name == "codex.exe" {
			return path, []string{"app-server"}, nil
		}
		return path, nil, nil
	}
	if path, err := resolveCommand("codex-app-server"); err == nil {
		return path, nil, nil
	}
	if path, err := resolveCommand("codex"); err == nil {
		return path, []string{"app-server"}, nil
	}
	return "", nil, errors.New("app-server command not found")
}

func resolveCommand(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	// Cmd.Dir is the empty isolated workspace. Freeze command discovery to an
	// absolute path so a relative override or PATH entry cannot be re-resolved
	// inside that directory at process start.
	return filepath.Abs(path)
}

func prepareRuntimeDir(configured string, allowDiskForTests bool) (string, error) {
	if configured != "" && !filepath.IsAbs(configured) {
		return "", errors.New("runtime directory must be absolute")
	}
	dir := configured
	if dir == "" {
		dir = defaultRuntimeDir
		if stat, err := os.Stat("/dev/shm"); err != nil || !stat.IsDir() {
			return "", errors.New("memory-backed runtime is unavailable")
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if filepath.Dir(abs) == abs {
		return "", errors.New("runtime directory cannot be a filesystem root")
	}
	info, err := os.Lstat(abs)
	if errors.Is(err, os.ErrNotExist) {
		parent, parentErr := os.Stat(filepath.Dir(abs))
		if parentErr != nil || !parent.IsDir() {
			return "", errors.New("runtime directory parent is unavailable")
		}
		if err := os.Mkdir(abs, 0o700); err != nil {
			return "", err
		}
		// Chmod is safe only for the directory this call just created.
		if err := os.Chmod(abs, 0o700); err != nil {
			return "", err
		}
		info, err = os.Lstat(abs)
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("runtime path is not a private directory")
	}
	if info.Mode().Perm() != 0o700 || !runtimeDirOwnedByCurrentUser(info) {
		return "", errors.New("runtime directory must be private and owned by the server user")
	}
	if !allowDiskForTests && !isMemoryBacked(abs) {
		return "", errors.New("runtime filesystem is not memory-backed")
	}
	if err := scrubStaleSessions(abs); err != nil {
		return "", err
	}
	probe, err := os.CreateTemp(abs, ".probe-")
	if err != nil {
		return "", err
	}
	probeName := probe.Name()
	if err := probe.Chmod(0o600); err != nil {
		probe.Close()
		os.Remove(probeName)
		return "", err
	}
	if err := probe.Close(); err != nil {
		os.Remove(probeName)
		return "", err
	}
	if err := os.Remove(probeName); err != nil {
		return "", err
	}
	return abs, nil
}

func scrubStaleSessions(runtimeDir string) error {
	entries, err := os.ReadDir(runtimeDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "session-") {
			continue
		}
		path := filepath.Join(runtimeDir, entry.Name())
		// Lstat binds the cleanup decision to the entry itself. RemoveAll does
		// not follow a symlink passed as its root, so an attacker-controlled
		// target outside this private directory is never traversed.
		if _, err := os.Lstat(path); err != nil {
			return err
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) gate(account AccountRef) chan struct{} {
	m.gatesMu.Lock()
	defer m.gatesMu.Unlock()
	gate := m.accountGates[account]
	if gate == nil {
		gate = make(chan struct{}, 1)
		m.accountGates[account] = gate
	}
	return gate
}

func (m *Manager) acquireAccount(ctx context.Context, account AccountRef) error {
	if !account.valid() {
		return ErrInvalidInput
	}
	select {
	case m.gate(account) <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) tryAcquireAccount(account AccountRef) error {
	if !account.valid() {
		return ErrInvalidInput
	}
	select {
	case m.gate(account) <- struct{}{}:
		return nil
	default:
		return ErrLoginInProgress
	}
}

func (m *Manager) releaseAccount(account AccountRef) {
	select {
	case <-m.gate(account):
	default:
	}
}

func (m *Manager) reserveLoginStart(account AccountRef) bool {
	m.flowsMu.Lock()
	defer m.flowsMu.Unlock()
	if _, active := m.accountFlows[account]; active {
		return false
	}
	if _, starting := m.loginStarts[account]; starting {
		return false
	}
	m.loginStarts[account] = struct{}{}
	return true
}

func (m *Manager) releaseLoginStart(account AccountRef) {
	m.flowsMu.Lock()
	delete(m.loginStarts, account)
	m.flowsMu.Unlock()
}

func (m *Manager) publishLoginFlow(flow *deviceFlow, operation *userOperation) bool {
	m.operationsMu.Lock()
	defer m.operationsMu.Unlock()
	if operation == nil || m.revocations[flow.account] != 0 || m.accountGenerations[flow.account] != operation.generation {
		return false
	}
	m.flowsMu.Lock()
	delete(m.loginStarts, flow.account)
	m.flows[flow.id] = flow
	m.accountFlows[flow.account] = flow.id
	m.flowsMu.Unlock()
	return true
}

func (m *Manager) accountContext(parent context.Context) (context.Context, context.CancelFunc) {
	timeout := m.accountOperationTimeout
	if timeout <= 0 {
		timeout = maxAccountOperation
	}
	return context.WithTimeout(parent, timeout)
}

func (m *Manager) registerAccountOperation(parent context.Context, account AccountRef) (context.Context, *userOperation, error) {
	m.operationsMu.Lock()
	defer m.operationsMu.Unlock()
	if m.revocations[account] != 0 {
		return nil, nil, ErrNotConnected
	}
	ctx, cancel := context.WithCancel(parent)
	operation := &userOperation{cancel: cancel, generation: m.accountGenerations[account]}
	m.accountOperations[account] = operation
	return ctx, operation, nil
}

func (m *Manager) unregisterAccountOperation(account AccountRef, operation *userOperation) {
	if operation == nil {
		return
	}
	operation.cancel()
	m.operationsMu.Lock()
	if m.accountOperations[account] == operation {
		delete(m.accountOperations, account)
	}
	m.operationsMu.Unlock()
}

func (m *Manager) beginAccountRevocation(account AccountRef) {
	m.operationsMu.Lock()
	m.revocations[account]++
	m.accountGenerations[account]++
	operation := m.accountOperations[account]
	m.operationsMu.Unlock()
	if operation != nil {
		operation.cancel()
	}
}

func (m *Manager) endAccountRevocation(account AccountRef) {
	m.operationsMu.Lock()
	if m.revocations[account] <= 1 {
		delete(m.revocations, account)
	} else {
		m.revocations[account]--
	}
	m.operationsMu.Unlock()
}

func randomFlowID() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func wrapContextOrSafe(ctx context.Context, fallback error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fallback
}

func validateManager(m *Manager) error {
	if m == nil || !m.Available() {
		return ErrUnavailable
	}
	return nil
}
