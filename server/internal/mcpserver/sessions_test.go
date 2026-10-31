package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/windoze95/cantinarr-server/internal/auth"
)

const initializeRequest = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"session-test","version":"1"}}}`

func testSessionHandler(t *testing.T, max int, ttl time.Duration) http.Handler {
	return testSessionHandlerWithLimits(t, max, max, ttl)
}

func testSessionHandlerWithLimits(t *testing.T, max, perUser int, ttl time.Duration) http.Handler {
	handler, _ := testSessionHandlerAndServer(t, max, perUser, ttl, nil)
	return handler
}

func testSessionHandlerAndServer(t *testing.T, max, perUser int, ttl time.Duration, hooks *server.Hooks) (http.Handler, *server.MCPServer) {
	t.Helper()
	ids := newBoundedSessionIDs(max, perUser)
	options := []server.ServerOption{}
	if hooks != nil {
		options = append(options, server.WithHooks(hooks))
	}
	mcpServer := server.NewMCPServer("test", "1", options...)
	transport := server.NewStreamableHTTPServer(mcpServer,
		server.WithSessionIdManagerResolver(sessionIDResolver{ids: ids}),
		server.WithSessionIdleTTL(ttl),
	)
	t.Cleanup(func() { _ = transport.Shutdown(context.Background()) })
	return sessionAdmission{next: transport, ids: ids}, mcpServer
}

func sessionRequest(handler http.Handler, method, sessionID, body string) *httptest.ResponseRecorder {
	return sessionRequestForUser(handler, 0, method, sessionID, body)
}

func sessionRequestForUser(handler http.Handler, userID int64, method, sessionID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/mcp", strings.NewReader(body))
	if userID != 0 {
		req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: userID, Role: auth.RoleUser}))
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
	}
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func TestMCPSessionAdmissionPreservesPerUserCapacity(t *testing.T) {
	handler := testSessionHandlerWithLimits(t, 3, 1, time.Minute)
	first := sessionRequestForUser(handler, 1, http.MethodPost, "", initializeRequest)
	if first.Code != http.StatusOK {
		t.Fatalf("user 1 initialize = %d %s", first.Code, first.Body.String())
	}
	id := first.Header().Get("Mcp-Session-Id")
	if second := sessionRequestForUser(handler, 1, http.MethodPost, "", initializeRequest); second.Code != http.StatusTooManyRequests {
		t.Fatalf("user 1 second session = %d %s", second.Code, second.Body.String())
	}
	for _, userID := range []int64{2, 3} {
		if next := sessionRequestForUser(handler, userID, http.MethodPost, "", initializeRequest); next.Code != http.StatusOK {
			t.Fatalf("user %d initialize = %d %s", userID, next.Code, next.Body.String())
		}
	}
	if crossUser := sessionRequestForUser(handler, 2, http.MethodDelete, id, ""); crossUser.Code != http.StatusNotFound {
		t.Fatalf("cross-user DELETE = %d %s", crossUser.Code, crossUser.Body.String())
	}
	if full := sessionRequestForUser(handler, 4, http.MethodPost, "", initializeRequest); full.Code != http.StatusTooManyRequests {
		t.Fatalf("global limit = %d %s", full.Code, full.Body.String())
	}
	if deleted := sessionRequestForUser(handler, 1, http.MethodDelete, id, ""); deleted.Code != http.StatusOK {
		t.Fatalf("owner DELETE = %d %s", deleted.Code, deleted.Body.String())
	}
	if replacement := sessionRequestForUser(handler, 4, http.MethodPost, "", initializeRequest); replacement.Code != http.StatusOK {
		t.Fatalf("replacement session = %d %s", replacement.Code, replacement.Body.String())
	}
}

func TestMCPSessionAdmissionCapsConcurrentInitializations(t *testing.T) {
	const maxSessions = 4
	handler := testSessionHandler(t, maxSessions, time.Minute)
	const attempts = 32
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, attempts)
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- sessionRequest(handler, http.MethodPost, "", initializeRequest)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	accepted, limited := 0, 0
	seen := map[string]bool{}
	for response := range results {
		switch response.Code {
		case http.StatusOK:
			id := response.Header().Get("Mcp-Session-Id")
			if !validMCPSessionID(id) || seen[id] {
				t.Fatalf("accepted session ID = %q", id)
			}
			seen[id] = true
			accepted++
		case http.StatusTooManyRequests:
			if response.Header().Get("Retry-After") == "" {
				t.Fatal("capacity response omitted Retry-After")
			}
			limited++
		default:
			t.Fatalf("unexpected initialization response = %d %s", response.Code, response.Body.String())
		}
	}
	if accepted != maxSessions || limited != attempts-maxSessions {
		t.Fatalf("accepted=%d limited=%d, want %d/%d", accepted, limited, maxSessions, attempts-maxSessions)
	}
}

func TestMCPSessionAdmissionReleasesFailedHandshakeAndDelete(t *testing.T) {
	handler := testSessionHandler(t, 1, time.Minute)
	bad := sessionRequest(handler, http.MethodPost, "", "not-json")
	if bad.Code == http.StatusTooManyRequests {
		t.Fatalf("failed handshake consumed capacity: %d %s", bad.Code, bad.Body.String())
	}
	first := sessionRequest(handler, http.MethodPost, "", initializeRequest)
	if first.Code != http.StatusOK {
		t.Fatalf("initialize = %d %s", first.Code, first.Body.String())
	}
	id := first.Header().Get("Mcp-Session-Id")
	if !validMCPSessionID(id) {
		t.Fatalf("session ID = %q", id)
	}
	if full := sessionRequest(handler, http.MethodPost, "", initializeRequest); full.Code != http.StatusTooManyRequests {
		t.Fatalf("capacity check = %d %s", full.Code, full.Body.String())
	}
	if unknown := sessionRequest(handler, http.MethodGet, "mcp-session-00000000-0000-4000-8000-000000000000", ""); unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown GET = %d %s", unknown.Code, unknown.Body.String())
	}
	if missing := sessionRequest(handler, http.MethodGet, "", ""); missing.Code != http.StatusBadRequest {
		t.Fatalf("missing GET ID = %d %s", missing.Code, missing.Body.String())
	}
	deleted := sessionRequest(handler, http.MethodDelete, id, "")
	if deleted.Code != http.StatusOK {
		t.Fatalf("DELETE = %d %s", deleted.Code, deleted.Body.String())
	}
	if old := sessionRequest(handler, http.MethodPost, id, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`); old.Code != http.StatusNotFound {
		t.Fatalf("old POST ID = %d %s", old.Code, old.Body.String())
	}
	if old := sessionRequest(handler, http.MethodGet, id, ""); old.Code != http.StatusNotFound {
		t.Fatalf("old GET ID = %d %s", old.Code, old.Body.String())
	}
	second := sessionRequest(handler, http.MethodPost, "", initializeRequest)
	if second.Code != http.StatusOK || second.Header().Get("Mcp-Session-Id") == "" {
		t.Fatalf("post-DELETE initialize = %d %s", second.Code, second.Body.String())
	}
}

func TestMCPSessionAdmissionRollsBackRejectedInitialize(t *testing.T) {
	hooks := &server.Hooks{}
	registered, unregistered := "", ""
	hooks.AddOnRegisterSession(func(_ context.Context, session server.ClientSession) {
		registered = session.SessionID()
	})
	hooks.AddOnUnregisterSession(func(_ context.Context, session server.ClientSession) {
		unregistered = session.SessionID()
	})
	handler, mcpServer := testSessionHandlerAndServer(t, 1, 1, time.Minute, hooks)
	invalid := strings.Replace(initializeRequest, `"jsonrpc":"2.0"`, `"jsonrpc":"1.0"`, 1)
	response := sessionRequest(handler, http.MethodPost, "", invalid)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"error"`) {
		t.Fatalf("invalid JSON-RPC initialize = %d %s", response.Code, response.Body.String())
	}
	if id := response.Header().Get("Mcp-Session-Id"); id != "" {
		t.Fatalf("failed initialize exposed session ID %q", id)
	}
	ids := handler.(sessionAdmission).ids
	ids.mu.Lock()
	active := len(ids.active)
	ids.mu.Unlock()
	if active != 0 {
		t.Fatalf("failed initialize retained %d active sessions", active)
	}
	if !validMCPSessionID(registered) || unregistered != registered {
		t.Fatalf("mcp-go registration cleanup: registered=%q unregistered=%q", registered, unregistered)
	}
	if err := mcpServer.SendLogMessageToSpecificClient(registered, mcp.LoggingMessageNotification{}); !errors.Is(err, server.ErrSessionNotFound) {
		t.Fatalf("mcp-go retained failed session: %v", err)
	}
	valid := sessionRequest(handler, http.MethodPost, "", initializeRequest)
	if valid.Code != http.StatusOK || valid.Header().Get("Mcp-Session-Id") == "" {
		t.Fatalf("valid initialize after rollback = %d %s", valid.Code, valid.Body.String())
	}
}

func TestMCPSessionAdmissionRejectsInitializeWithExistingID(t *testing.T) {
	handler := testSessionHandler(t, 1, time.Minute)
	first := sessionRequest(handler, http.MethodPost, "", initializeRequest)
	if first.Code != http.StatusOK {
		t.Fatalf("first initialize = %d %s", first.Code, first.Body.String())
	}
	id := first.Header().Get("Mcp-Session-Id")
	if !validMCPSessionID(id) {
		t.Fatalf("first initialize ID = %q", id)
	}
	reused := sessionRequest(handler, http.MethodPost, id, initializeRequest)
	if reused.Code != http.StatusBadRequest || reused.Header().Get("Mcp-Session-Id") != "" {
		t.Fatalf("initialize with ID = %d, ID %q: %s", reused.Code, reused.Header().Get("Mcp-Session-Id"), reused.Body.String())
	}
	// A headerless ping response must remain independent of session admission,
	// even while the single session slot is full.
	pingResponse := sessionRequest(handler, http.MethodPost, "", `{"jsonrpc":"2.0","id":7,"result":{}}`)
	if pingResponse.Code != http.StatusOK {
		t.Fatalf("headerless ping response = %d %s", pingResponse.Code, pingResponse.Body.String())
	}
	if missing := sessionRequest(handler, http.MethodPost, "", `{"jsonrpc":"2.0","id":8,"method":"ping"}`); missing.Code != http.StatusNotFound {
		t.Fatalf("headerless ping request = %d %s", missing.Code, missing.Body.String())
	}
	if deleted := sessionRequest(handler, http.MethodDelete, id, ""); deleted.Code != http.StatusOK {
		t.Fatalf("DELETE after rejected initialize = %d %s", deleted.Code, deleted.Body.String())
	}
}

func TestMCPSessionGETStreamsAreBoundedAndCanceledOnDelete(t *testing.T) {
	handler := testSessionHandlerWithLimits(t, 3, 3, time.Minute)
	first := sessionRequestForUser(handler, 1, http.MethodPost, "", initializeRequest)
	if first.Code != http.StatusOK {
		t.Fatalf("initialize = %d %s", first.Code, first.Body.String())
	}
	id := first.Header().Get("Mcp-Session-Id")
	ids := handler.(sessionAdmission).ids
	streams := make([]chan *httptest.ResponseRecorder, 2)
	for i := range streams {
		streams[i] = make(chan *httptest.ResponseRecorder, 1)
		go func(result chan *httptest.ResponseRecorder) {
			result <- sessionRequestForUser(handler, 1, http.MethodGet, id, "")
		}(streams[i])
		waitForMCPStreamCount(t, ids, i+1)
	}
	if excess := sessionRequestForUser(handler, 1, http.MethodGet, id, ""); excess.Code != http.StatusTooManyRequests {
		t.Fatalf("third GET = %d %s", excess.Code, excess.Body.String())
	}
	if crossUser := sessionRequestForUser(handler, 2, http.MethodGet, id, ""); crossUser.Code != http.StatusNotFound {
		t.Fatalf("cross-user GET = %d %s", crossUser.Code, crossUser.Body.String())
	}
	if deleted := sessionRequestForUser(handler, 1, http.MethodDelete, id, ""); deleted.Code != http.StatusOK {
		t.Fatalf("DELETE = %d %s", deleted.Code, deleted.Body.String())
	}
	for i, result := range streams {
		select {
		case response := <-result:
			if response.Code != http.StatusOK {
				t.Fatalf("GET %d after DELETE = %d %s", i, response.Code, response.Body.String())
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("GET %d did not close after DELETE", i)
		}
	}
	waitForMCPStreamCount(t, ids, 0)
	if replacement := sessionRequestForUser(handler, 1, http.MethodPost, "", initializeRequest); replacement.Code != http.StatusOK {
		t.Fatalf("initialize after stream cleanup = %d %s", replacement.Code, replacement.Body.String())
	}
}

func TestMCPSessionGETStreamLifetimeRequiresReconnect(t *testing.T) {
	handler := testSessionHandler(t, 1, time.Minute)
	ids := handler.(sessionAdmission).ids
	ids.streamLifetime = 50 * time.Millisecond
	first := sessionRequest(handler, http.MethodPost, "", initializeRequest)
	if first.Code != http.StatusOK {
		t.Fatalf("initialize = %d %s", first.Code, first.Body.String())
	}
	id := first.Header().Get("Mcp-Session-Id")
	stream := make(chan *httptest.ResponseRecorder, 1)
	go func() { stream <- sessionRequest(handler, http.MethodGet, id, "") }()
	select {
	case response := <-stream:
		if response.Code != http.StatusOK {
			t.Fatalf("GET after stream lifetime = %d %s", response.Code, response.Body.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GET stream did not close at its lifetime")
	}
	waitForMCPStreamCount(t, ids, 0)
	if _, err := ids.Validate(id); err != nil {
		t.Fatalf("stream lifetime incorrectly expired session ID: %v", err)
	}
	if ping := sessionRequest(handler, http.MethodPost, id, `{"jsonrpc":"2.0","id":9,"method":"ping"}`); ping.Code != http.StatusOK {
		t.Fatalf("session after GET reconnect = %d %s", ping.Code, ping.Body.String())
	}
}

func waitForMCPStreamCount(t *testing.T, ids *boundedSessionIDs, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ids.mu.Lock()
		got := ids.streamCount
		ids.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("MCP GET stream count did not reach %d", want)
}

func TestMCPSessionCountersDoNotRetainPastUsers(t *testing.T) {
	ids := newBoundedSessionIDs(1, 1)
	for userID := int64(1); userID <= 100; userID++ {
		unused, ok := ids.reserve(userID)
		if !ok {
			t.Fatalf("reserve unused user %d", userID)
		}
		ids.release(unused)
		reservation, ok := ids.reserve(userID)
		if !ok {
			t.Fatalf("reserve user %d", userID)
		}
		id := ids.generate(reservation)
		if id == "" {
			t.Fatalf("generate user %d", userID)
		}
		ctx, closeStream, err := ids.openStream(id, userID, context.Background())
		if err != nil || ctx == nil {
			t.Fatalf("open stream user %d: %v", userID, err)
		}
		closeStream()
		_, _ = ids.Terminate(id)
	}
	if len(ids.active) != 0 || len(ids.activeByUser) != 0 || len(ids.pendingByUser) != 0 || len(ids.streams) != 0 || len(ids.streamsByUser) != 0 {
		t.Fatalf("retained session counters: active=%d activeUsers=%d pendingUsers=%d streams=%d streamUsers=%d",
			len(ids.active), len(ids.activeByUser), len(ids.pendingByUser), len(ids.streams), len(ids.streamsByUser))
	}
}

func TestMCPSessionIdleExpiryReleasesCapacity(t *testing.T) {
	handler := testSessionHandler(t, 1, 50*time.Millisecond)
	first := sessionRequest(handler, http.MethodPost, "", initializeRequest)
	if first.Code != http.StatusOK {
		t.Fatalf("initialize = %d %s", first.Code, first.Body.String())
	}
	id := first.Header().Get("Mcp-Session-Id")
	ids := handler.(sessionAdmission).ids
	stream := make(chan *httptest.ResponseRecorder, 1)
	go func() { stream <- sessionRequest(handler, http.MethodGet, id, "") }()
	waitForMCPStreamCount(t, ids, 1)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := ids.Validate(id); err != nil {
			select {
			case response := <-stream:
				if response.Code != http.StatusOK {
					t.Fatalf("GET after idle expiry = %d %s", response.Code, response.Body.String())
				}
			case <-time.After(2 * time.Second):
				t.Fatal("GET did not close after idle expiry")
			}
			waitForMCPStreamCount(t, ids, 0)
			if expired := sessionRequest(handler, http.MethodPost, id, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`); expired.Code != http.StatusNotFound {
				t.Fatalf("expired session POST = %d %s", expired.Code, expired.Body.String())
			}
			second := sessionRequest(handler, http.MethodPost, "", initializeRequest)
			if second.Code != http.StatusOK || second.Header().Get("Mcp-Session-Id") == "" {
				t.Fatalf("post-expiry initialize = %d %s", second.Code, second.Body.String())
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("idle session never expired")
}
