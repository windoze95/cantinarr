package api

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/cache"
	"github.com/windoze95/cantinarr-server/internal/codexapp"
	"github.com/windoze95/cantinarr-server/internal/config"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	projectdb "github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/discover"
	"github.com/windoze95/cantinarr-server/internal/downloads"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mcp"
	"github.com/windoze95/cantinarr-server/internal/plex"
	"github.com/windoze95/cantinarr-server/internal/proxy"
	"github.com/windoze95/cantinarr-server/internal/push"
	"github.com/windoze95/cantinarr-server/internal/remediation"
	requestsvc "github.com/windoze95/cantinarr-server/internal/request"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"github.com/windoze95/cantinarr-server/internal/serversettings"
	"github.com/windoze95/cantinarr-server/internal/tautulli"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
	"github.com/windoze95/cantinarr-server/internal/update"
	"github.com/windoze95/cantinarr-server/internal/webhooks"
	ws "github.com/windoze95/cantinarr-server/internal/websocket"
)

func TestRouterRBACMatrixWithAdminAndRequesterTokens(t *testing.T) {
	harness := newRBACRouterHarness(t, false)
	routes := privilegedRoutes(t, harness.router)
	if len(routes) < 50 {
		t.Fatalf("privileged route inventory unexpectedly small: got %d", len(routes))
	}

	for _, route := range routes {
		route := route
		t.Run(route.method+" "+route.pattern, func(t *testing.T) {
			path := concreteRBACPath(route.pattern)
			requester := serveRBACRequest(harness.router, route.method, path, harness.requesterToken)
			if requester.Code != http.StatusForbidden {
				t.Fatalf("requester status = %d, want 403; body=%s", requester.Code, requester.Body.String())
			}

			anonymous := serveRBACRequest(harness.router, route.method, path, "")
			if anonymous.Code != http.StatusUnauthorized {
				t.Fatalf("anonymous status = %d, want 401; body=%s", anonymous.Code, anonymous.Body.String())
			}
		})
	}

	// Exercise one non-mutating route for every admin permission surface. These
	// are real authenticated API requests; a backend-specific 404/5xx is fine in
	// the empty harness, but auth must admit the admin and never return 401/403.
	adminRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/admin/users"},
		{http.MethodGet, "/api/admin/credentials"},
		{http.MethodGet, "/api/admin/ai-tools"},
		{http.MethodGet, "/api/admin/requests"},
		{http.MethodGet, "/api/admin/issues"},
		{http.MethodGet, "/api/admin/setup-status"},
		{http.MethodGet, "/api/instances"},
		{http.MethodGet, "/api/downloads/missing/queue"},
		{http.MethodGet, "/api/tautulli/missing/activity"},
	}
	for _, route := range adminRoutes {
		recorder := serveRBACRequest(harness.router, route.method, route.path, harness.adminToken)
		if recorder.Code == http.StatusUnauthorized || recorder.Code == http.StatusForbidden {
			t.Errorf("admin %s %s was rejected with %d: %s", route.method, route.path, recorder.Code, recorder.Body.String())
		}
	}

	// Interactive AI and caller-owned settings are intentionally available to
	// both roles. With no provider configured, chat fails at availability (503),
	// which proves the request passed auth and AI permission middleware.
	for _, token := range []string{harness.adminToken, harness.requesterToken} {
		for _, route := range []struct {
			method string
			path   string
			body   string
			want   int
		}{
			{http.MethodGet, "/api/ai/available", "", http.StatusOK},
			{http.MethodGet, "/api/ai/settings", "", http.StatusOK},
			{http.MethodPost, "/api/ai/chat", `{"messages":[{"role":"user","content":"hello"}]}`, http.StatusServiceUnavailable},
		} {
			recorder := serveRBACRequestWithBody(harness.router, route.method, route.path, token, route.body)
			if recorder.Code != route.want {
				t.Errorf("%s %s status = %d, want %d; body=%s", route.method, route.path, recorder.Code, route.want, recorder.Body.String())
			}
		}
	}
}

func TestAIChatAPIScopeAndRoleMatrixWithCodexAppServer(t *testing.T) {
	harness := newRBACRouterHarness(t, true)
	if err := harness.registry.SetAIConfig(credentials.AIProviderCodex, "default"); err != nil {
		t.Fatal(err)
	}
	storeAPICodexAccount(t, harness, 0, "shared")

	// Setup grants the first admin shared AI access. A newly invited requester
	// starts without it, so the same shared account is available only to the
	// admin until an authorized grant is made.
	assertSuccessfulAIChat(t, harness.router, harness.adminToken, "shared response")
	assertUnavailableAIChat(t, harness.router, harness.requesterToken)

	accessPath := "/api/admin/users/" + strconv.FormatInt(harness.requesterID, 10) + "/ai-access"
	denied := serveRBACRequestWithBody(harness.router, http.MethodPut, accessPath, harness.requesterToken, `{"shared_ai_enabled":true}`)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("requester changed own shared grant: status=%d body=%s", denied.Code, denied.Body.String())
	}
	setSharedAIAccess(t, harness, accessPath, true)
	assertSuccessfulAIChat(t, harness.router, harness.requesterToken, "shared response")

	// Revoking the shared grant takes effect immediately. A caller-owned Codex
	// account remains usable and is selected through the real settings API,
	// including its mandatory test turn.
	setSharedAIAccess(t, harness, accessPath, false)
	assertUnavailableAIChat(t, harness.router, harness.requesterToken)
	storeAPICodexAccount(t, harness, harness.requesterID, "requester-personal")
	updatePersonalCodexSettings(t, harness.router, harness.requesterToken)
	assertSuccessfulAIChat(t, harness.router, harness.requesterToken, "requester-personal response")

	// A selected but disconnected personal profile is fail-closed even when the
	// administrator grants a healthy shared fallback. Deleting the personal
	// selection through the caller-owned endpoint deliberately restores shared.
	setSharedAIAccess(t, harness, accessPath, true)
	if _, err := harness.database.Exec(`DELETE FROM user_codex_accounts WHERE user_id = ?`, harness.requesterID); err != nil {
		t.Fatal(err)
	}
	assertUnavailableAIChat(t, harness.router, harness.requesterToken)
	storeAPICodexAccount(t, harness, harness.requesterID, "requester-personal")
	deletePersonalAISettings(t, harness.router, harness.requesterToken)
	assertSuccessfulAIChat(t, harness.router, harness.requesterToken, "shared response")

	// Admins have the same caller-owned personal boundary; their elevated role
	// changes tool permissions, not whose AI credential the turn consumes.
	storeAPICodexAccount(t, harness, harness.adminID, "admin-personal")
	updatePersonalCodexSettings(t, harness.router, harness.adminToken)
	assertSuccessfulAIChat(t, harness.router, harness.adminToken, "admin-personal response")
	deletePersonalAISettings(t, harness.router, harness.adminToken)
	assertSuccessfulAIChat(t, harness.router, harness.adminToken, "shared response")
}

// TestAPICodexHelperProcess is a subprocess-only JSONL app-server used by the
// full router test above. It returns the non-secret test_scope from the
// account-specific auth.json so the assertion proves which credential boundary
// served each API request.
func TestAPICodexHelperProcess(t *testing.T) {
	if !hasProcessArg("--api-codex-helper") {
		return
	}
	scope := readAPICodexScope(t)
	encoder := json.NewEncoder(os.Stdout)
	send := func(value any) {
		if err := encoder.Encode(value); err != nil {
			t.Fatalf("encode app-server response: %v", err)
		}
	}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	for scanner.Scan() {
		var message struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			t.Fatalf("decode app-server request: %v", err)
		}
		if message.ID == nil {
			continue
		}
		switch message.Method {
		case "initialize":
			send(map[string]any{"id": message.ID, "result": map[string]any{
				"codexHome": os.Getenv("CODEX_HOME"), "platformFamily": "unix", "platformOs": "test", "userAgent": "codex-app-server/test",
			}})
		case "thread/start":
			var params struct {
				Config map[string]any `json:"config"`
			}
			if json.Unmarshal(message.Params, &params) != nil {
				sendAPICodexError(send, message.ID, "invalid thread config")
				continue
			}
			if _, invalid := params.Config["apps.enabled"]; invalid {
				sendAPICodexError(send, message.ID, "apps.enabled is not a valid app-server config key")
				continue
			}
			if apps, ok := params.Config["features.apps"].(bool); !ok || apps {
				sendAPICodexError(send, message.ID, "features.apps must be disabled")
				continue
			}
			send(map[string]any{"id": message.ID, "result": map[string]any{"thread": map[string]any{"id": "thread-1"}}})
		case "turn/start":
			send(map[string]any{"id": message.ID, "result": map[string]any{"turn": map[string]any{
				"id": "turn-1", "status": "inProgress", "items": []any{},
			}}})
			send(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{
				"threadId": "thread-1", "turnId": "turn-1", "itemId": "message-1", "delta": scope + " response",
			}})
			send(map[string]any{"method": "turn/completed", "params": map[string]any{
				"threadId": "thread-1", "turn": map[string]any{"id": "turn-1", "status": "completed", "items": []any{}},
			}})
		default:
			sendAPICodexError(send, message.ID, "unsupported test method")
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read app-server protocol: %v", err)
	}
}

func sendAPICodexError(send func(any), id any, message string) {
	send(map[string]any{"id": id, "error": map[string]any{"code": -32602, "message": message}})
}

func hasProcessArg(want string) bool {
	for _, arg := range os.Args {
		if arg == want {
			return true
		}
	}
	return false
}

func readAPICodexScope(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(os.Getenv("CODEX_HOME"), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	var authJSON struct {
		TestScope string `json:"test_scope"`
	}
	if json.Unmarshal(data, &authJSON) != nil || authJSON.TestScope == "" {
		t.Fatal("auth.json did not contain a test scope")
	}
	return authJSON.TestScope
}

type rbacRoute struct {
	method  string
	pattern string
}

func privilegedRoutes(t *testing.T, router http.Handler) []rbacRoute {
	t.Helper()
	routes, ok := router.(chi.Routes)
	if !ok {
		t.Fatal("router does not expose chi routes")
	}
	var out []rbacRoute
	err := chi.Walk(routes, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		privileged := strings.HasPrefix(pattern, "/api/admin/") ||
			strings.HasPrefix(pattern, "/api/downloads/") ||
			strings.HasPrefix(pattern, "/api/tautulli/") ||
			(strings.HasPrefix(pattern, "/api/instances") && !strings.HasSuffix(pattern, "/*"))
		if privileged {
			out = append(out, rbacRoute{method: method, pattern: pattern})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].pattern == out[j].pattern {
			return out[i].method < out[j].method
		}
		return out[i].pattern < out[j].pattern
	})
	return out
}

var routeParameterPattern = regexp.MustCompile(`\{[^}]+\}`)

func concreteRBACPath(pattern string) string {
	path := routeParameterPattern.ReplaceAllString(pattern, "1")
	if strings.HasSuffix(path, "/*") {
		return strings.TrimSuffix(path, "/*") + "/api/v3/config/host"
	}
	return path
}

func serveRBACRequest(router http.Handler, method, path, token string) *httptest.ResponseRecorder {
	return serveRBACRequestWithBody(router, method, path, token, `{}`)
}

func serveRBACRequestWithBody(router http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

type rbacRouterHarness struct {
	router         http.Handler
	database       *sql.DB
	cipher         *secrets.Cipher
	registry       *credentials.Registry
	adminID        int64
	requesterID    int64
	adminToken     string
	requesterToken string
}

func newRBACRouterHarness(t *testing.T, withCodex bool) *rbacRouterHarness {
	t.Helper()
	database, err := projectdb.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x4a}, 32))
	if err != nil {
		t.Fatal(err)
	}
	registry := credentials.NewRegistry(database, cipher)
	authService := auth.NewService(database, "rbac-integration-jwt-secret")
	authHandler := auth.NewHandler(authService)
	admin, err := authService.Setup("admin", "correct-horse-battery-staple", "Admin Test", "admin-hardware")
	if err != nil {
		t.Fatal(err)
	}
	connect, err := authService.CreateConnectToken(admin.User.ID, "requester", "http://cantinarr.test")
	if err != nil {
		t.Fatal(err)
	}
	link, err := url.Parse(connect.Link)
	if err != nil {
		t.Fatal(err)
	}
	requester, err := authService.RedeemConnectToken(link.Query().Get("token"), "Requester Test", "requester-hardware")
	if err != nil {
		t.Fatal(err)
	}

	store := instance.NewStore(database, cipher)
	instanceRegistry := instance.NewRegistry(store)
	bridge := tmdb.NewBridge(registry, database)
	requestService := requestsvc.NewService(database, instanceRegistry, bridge, nil)
	requestHandler := requestsvc.NewHandler(requestService)
	remediationService := remediation.NewService(database, instanceRegistry, bridge, nil)
	remediationHandler := remediation.NewHandler(remediationService)
	toolServer := mcp.NewToolServer(registry, requestService, instanceRegistry, bridge)
	var codexManager *codexapp.Manager
	if withCodex {
		codexManager = codexapp.NewManager(database, cipher, toolServer, codexapp.Options{
			Binary:                   os.Args[0],
			RuntimeDir:               filepath.Join(t.TempDir(), "codex-runtime"),
			Args:                     []string{"-test.run=TestAPICodexHelperProcess", "--", "--api-codex-helper"},
			AllowDiskRuntimeForTests: true,
		})
		if !codexManager.Available() {
			t.Fatalf("fake Codex manager unavailable: %v", codexManager.AvailabilityError())
		}
	}
	aiHandler := ai.NewHandler(registry, toolServer, codexManager)
	credentialHandler := credentials.NewHandler(registry)
	credentialHandler.SetSharedAIConfigured(aiHandler.ProviderConfigured)
	instanceHandler := instance.NewHandler(store, instanceRegistry, "http://cantinarr.test")
	downloadsHandler := downloads.NewHandler(store, instanceRegistry)
	tautulliHandler := tautulli.NewHandler(store, instanceRegistry)
	proxyHandler := proxy.NewHandler(store)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pushHandler := push.NewHandler(database, nil, logger)
	hub := ws.NewHub(authService, instanceRegistry, store, nil, nil)
	plexService := plex.NewService(database, cipher, plex.NewClient(), nil, logger)
	plexHandler := plex.NewHandler(plexService, logger)
	webhookHandler := webhooks.NewHandler(store, instanceRegistry, hub, requestService, nil)
	discoverCache := cache.New()
	t.Cleanup(discoverCache.Close)
	discoverHandler := discover.NewHandler(registry, discoverCache)

	cfg := &config.Config{
		PublicURL:          "http://cantinarr.test",
		ServerName:         "RBAC Test",
		DisableUpdateCheck: true,
	}
	router := NewRouter(
		cfg,
		authHandler,
		authService,
		requestHandler,
		remediationService,
		remediationHandler,
		proxyHandler,
		hub,
		aiHandler,
		discoverHandler,
		instanceHandler,
		store,
		downloadsHandler,
		tautulliHandler,
		registry,
		credentialHandler,
		toolServer,
		pushHandler,
		webhookHandler,
		plexHandler,
		plexService,
		update.NewChecker("dev", true),
		serversettings.NewService(database),
	)
	return &rbacRouterHarness{
		router:         router,
		database:       database,
		cipher:         cipher,
		registry:       registry,
		adminID:        admin.User.ID,
		requesterID:    requester.User.ID,
		adminToken:     admin.AccessToken,
		requesterToken: requester.AccessToken,
	}
}

func storeAPICodexAccount(t *testing.T, harness *rbacRouterHarness, userID int64, scope string) {
	t.Helper()
	authJSON, err := json.Marshal(map[string]string{"test_scope": scope})
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := harness.cipher.Encrypt(string(authJSON))
	if err != nil {
		t.Fatal(err)
	}
	if userID == 0 {
		_, err = harness.database.Exec(`
			INSERT INTO shared_codex_account (singleton, auth_blob)
			VALUES (1, ?)
			ON CONFLICT(singleton) DO UPDATE SET auth_blob = excluded.auth_blob`, encrypted)
	} else {
		_, err = harness.database.Exec(`
			INSERT INTO user_codex_accounts (user_id, auth_blob)
			VALUES (?, ?)
			ON CONFLICT(user_id) DO UPDATE SET auth_blob = excluded.auth_blob`, userID, encrypted)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func setSharedAIAccess(t *testing.T, harness *rbacRouterHarness, path string, enabled bool) {
	t.Helper()
	body := `{"shared_ai_enabled":false}`
	if enabled {
		body = `{"shared_ai_enabled":true}`
	}
	recorder := serveRBACRequestWithBody(harness.router, http.MethodPut, path, harness.adminToken, body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("set shared AI access to %t: status=%d body=%s", enabled, recorder.Code, recorder.Body.String())
	}
}

func updatePersonalCodexSettings(t *testing.T, router http.Handler, token string) {
	t.Helper()
	recorder := serveRBACRequestWithBody(
		router,
		http.MethodPut,
		"/api/ai/settings",
		token,
		`{"provider":"codex","model":"default"}`,
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("select personal Codex: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"source":"personal"`) {
		t.Fatalf("personal Codex was not effective: %s", recorder.Body.String())
	}
}

func deletePersonalAISettings(t *testing.T, router http.Handler, token string) {
	t.Helper()
	recorder := serveRBACRequest(router, http.MethodDelete, "/api/ai/settings", token)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete personal AI settings: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func assertSuccessfulAIChat(t *testing.T, router http.Handler, token, wantText string) {
	t.Helper()
	recorder := serveRBACRequestWithBody(
		router,
		http.MethodPost,
		"/api/ai/chat",
		token,
		`{"messages":[{"role":"user","content":"hello"}]}`,
	)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("AI chat status=%d body=%s", recorder.Code, body)
	}
	if !strings.Contains(body, `"text":"`+wantText+`"`) || !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("AI chat did not complete with %q: %s", wantText, body)
	}
	if strings.Contains(body, `"error"`) {
		t.Fatalf("AI chat returned an error frame: %s", body)
	}
}

func assertUnavailableAIChat(t *testing.T, router http.Handler, token string) {
	t.Helper()
	recorder := serveRBACRequestWithBody(
		router,
		http.MethodPost,
		"/api/ai/chat",
		token,
		`{"messages":[{"role":"user","content":"hello"}]}`,
	)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable AI chat status=%d, want 503; body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), " response") {
		t.Fatalf("unavailable AI chat reached a model account: %s", recorder.Body.String())
	}
}
