package websocket

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/radarr"
)

// wsTestEnv wires a real auth service (in-memory SQLite) behind a running hub
// that serves ServeWS over httptest — the production wiring minus the pollers,
// so handshake and routing tests exercise the real HTTP upgrade path.
type wsTestEnv struct {
	hub    *Hub
	auth   *auth.Service
	server *httptest.Server
}

func newWSTestEnv(t *testing.T) *wsTestEnv {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	svc := auth.NewService(database, "test-secret-key")
	if err := svc.EnsureAdmin("testpass123"); err != nil {
		t.Fatalf("ensure admin: %v", err)
	}

	h := NewHub(svc, nil, nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go h.Run(ctx)

	server := httptest.NewServer(http.HandlerFunc(h.ServeWS))
	t.Cleanup(func() {
		server.Close()
		cancel()
	})
	return &wsTestEnv{hub: h, auth: svc, server: server}
}

// adminToken logs in the bootstrap admin (user id 1) and returns its bearer token.
func (e *wsTestEnv) adminToken(t *testing.T) string {
	t.Helper()
	resp, err := e.auth.Login("admin", "testpass123", "admin-desktop", "hw-admin")
	if err != nil {
		t.Fatalf("admin login: %v", err)
	}
	return resp.AccessToken
}

// userToken invites a passwordless user via a connect link and redeems it,
// exactly like the app does. Repeat calls with the same username mint extra
// sessions (devices) for the same user.
func (e *wsTestEnv) userToken(t *testing.T, username, hardwareID string) *auth.TokenResponse {
	t.Helper()
	created, err := e.auth.CreateConnectToken(1, username, "http://cantinarr.test")
	if err != nil {
		t.Fatalf("create connect token: %v", err)
	}
	link, err := url.Parse(created.Link)
	if err != nil {
		t.Fatalf("parse connect link: %v", err)
	}
	resp, err := e.auth.RedeemConnectToken(link.Query().Get("token"), username+"-device", hardwareID)
	if err != nil {
		t.Fatalf("redeem connect token: %v", err)
	}
	return resp
}

// dial opens a websocket connection against ServeWS with the given subprotocol
// list (the app sends ["Bearer", token]).
func (e *wsTestEnv) dial(t *testing.T, protocols []string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(e.server.URL, "http")
	dialer := websocket.Dialer{Subprotocols: protocols, HandshakeTimeout: 5 * time.Second}
	return dialer.Dial(wsURL, nil)
}

// mustDial is dial for connections the test expects to succeed.
func (e *wsTestEnv) mustDial(t *testing.T, token string) *websocket.Conn {
	t.Helper()
	conn, _, err := e.dial(t, []string{"Bearer", token})
	if err != nil {
		t.Fatalf("dial with valid token: %v", err)
	}
	return conn
}

// waitForClients blocks until the hub's client set reaches want. Registration
// happens on the hub goroutine after the 101 response, so a successful Dial
// alone does not guarantee the client is routable yet. Uses the write lock so
// the read can never overlap Run's broadcast-side map mutation.
func (e *wsTestEnv) waitForClients(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.hub.mu.Lock()
		n := len(e.hub.clients)
		e.hub.mu.Unlock()
		if n == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("hub clients = %d, want %d", n, want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// clientsSnapshot copies the registered client set for identity assertions.
func (e *wsTestEnv) clientsSnapshot() []*Client {
	e.hub.mu.Lock()
	defer e.hub.mu.Unlock()
	out := make([]*Client, 0, len(e.hub.clients))
	for c := range e.hub.clients {
		out = append(out, c)
	}
	return out
}

func readEvent(t *testing.T, conn *websocket.Conn) Event {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read event: %v", err)
	}
	var ev Event
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatalf("decode event %q: %v", data, err)
	}
	return ev
}

// TestServeWSHandshakeRejectsBadAuth proves the upgrade never happens without a
// valid ["Bearer", token] subprotocol pair: no header, a wrong scheme (even
// with a perfectly valid token), a bare scheme, and a garbage token are all
// refused with 401 before any client is registered.
func TestServeWSHandshakeRejectsBadAuth(t *testing.T) {
	env := newWSTestEnv(t)
	validToken := env.userToken(t, "alice", "hw-alice").AccessToken

	cases := []struct {
		name      string
		protocols []string
	}{
		{"no subprotocols", nil},
		{"wrong scheme with valid token", []string{"Basic", validToken}},
		{"scheme without token", []string{"Bearer"}},
		{"invalid token", []string{"Bearer", "not-a-real-token"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, resp, err := env.dial(t, tc.protocols)
			if err == nil {
				conn.Close()
				t.Fatalf("handshake succeeded, want rejection")
			}
			if resp == nil || resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("handshake response = %+v, want HTTP 401", resp)
			}
		})
	}

	if got := len(env.clientsSnapshot()); got != 0 {
		t.Fatalf("rejected handshakes registered %d client(s), want 0", got)
	}
}

// TestServeWSHandshakeRegistersAuthenticatedClients proves a valid user token
// upgrades and registers a client bound to that user with isAdmin=false, and a
// valid admin token registers with isAdmin=true.
func TestServeWSHandshakeRegistersAuthenticatedClients(t *testing.T) {
	env := newWSTestEnv(t)
	alice := env.userToken(t, "alice", "hw-alice")

	userConn, userResp, err := env.dial(t, []string{"Bearer", alice.AccessToken})
	if err != nil {
		t.Fatalf("dial with user token: %v", err)
	}
	defer userConn.Close()
	env.waitForClients(t, 1)

	clients := env.clientsSnapshot()
	if clients[0].userID != alice.User.ID || clients[0].isAdmin {
		t.Fatalf("user client = {userID:%d isAdmin:%t}, want {userID:%d isAdmin:false}",
			clients[0].userID, clients[0].isAdmin, alice.User.ID)
	}

	// The upgrade must echo the static "Bearer" subprotocol from the client's
	// ["Bearer", token] offer: browsers (the web build) fail the connection
	// when the server selects none of the offered subprotocols, and the token
	// must never be copied into a response header.
	if got := userResp.Header.Get("Sec-WebSocket-Protocol"); got != "Bearer" {
		t.Fatalf("negotiated subprotocol = %q, want %q", got, "Bearer")
	}
	if got := userConn.Subprotocol(); got != "Bearer" {
		t.Fatalf("client-side negotiated subprotocol = %q, want %q", got, "Bearer")
	}

	adminConn := env.mustDial(t, env.adminToken(t))
	defer adminConn.Close()
	env.waitForClients(t, 2)

	var adminClient *Client
	for _, c := range env.clientsSnapshot() {
		if c.userID != alice.User.ID {
			adminClient = c
		}
	}
	if adminClient == nil || !adminClient.isAdmin || adminClient.userID != 1 {
		t.Fatalf("admin client = %+v, want {userID:1 isAdmin:true}", adminClient)
	}
}

// TestServeWSHandshakeRejectsRevokedDeviceToken proves a token that worked is
// refused after its device is revoked: 401 strictly means the session is gone.
func TestServeWSHandshakeRejectsRevokedDeviceToken(t *testing.T) {
	env := newWSTestEnv(t)
	alice := env.userToken(t, "alice", "hw-alice")

	conn := env.mustDial(t, alice.AccessToken)
	conn.Close()

	if err := env.auth.RevokeDevice(alice.DeviceID); err != nil {
		t.Fatalf("revoke device: %v", err)
	}

	retry, resp, err := env.dial(t, []string{"Bearer", alice.AccessToken})
	if err == nil {
		retry.Close()
		t.Fatalf("handshake with revoked device token succeeded, want 401")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("handshake response = %+v, want HTTP 401", resp)
	}
}

// TestBroadcastUserReachesOnlyThatUsersConnections is the cross-user privacy
// boundary: a user-targeted event reaches every connection of that user (two
// devices here) and nothing else — not another user, and not admins either
// (admin visibility is a separate channel, not a duplicate of user traffic).
//
// Non-delivery is asserted deterministically via ordering rather than timing:
// the hub drains its broadcast channel FIFO into per-client FIFO send channels,
// so a global "fence" event enqueued after the targeted event must arrive
// after it on any connection that (incorrectly) received the targeted one.
func TestBroadcastUserReachesOnlyThatUsersConnections(t *testing.T) {
	env := newWSTestEnv(t)
	alice := env.userToken(t, "alice", "hw-alice")
	bob := env.userToken(t, "bob", "hw-bob")

	alicePhone := env.mustDial(t, alice.AccessToken)
	defer alicePhone.Close()
	aliceTablet := env.mustDial(t, alice.AccessToken)
	defer aliceTablet.Close()
	bobConn := env.mustDial(t, bob.AccessToken)
	defer bobConn.Close()
	adminConn := env.mustDial(t, env.adminToken(t))
	defer adminConn.Close()
	env.waitForClients(t, 4)

	env.hub.BroadcastUser(alice.User.ID, Event{
		Type: "request_status_changed",
		Data: map[string]interface{}{"for": "alice"},
	})
	env.hub.Broadcast(Event{Type: "fence"})

	for name, conn := range map[string]*websocket.Conn{"phone": alicePhone, "tablet": aliceTablet} {
		ev := readEvent(t, conn)
		if ev.Type != "request_status_changed" || ev.Data["for"] != "alice" {
			t.Fatalf("alice %s first event = %+v, want her targeted event", name, ev)
		}
		if ev := readEvent(t, conn); ev.Type != "fence" {
			t.Fatalf("alice %s second event = %+v, want fence", name, ev)
		}
	}
	if ev := readEvent(t, bobConn); ev.Type != "fence" {
		t.Fatalf("bob's first event = %+v, want only the fence (alice's event leaked)", ev)
	}
	if ev := readEvent(t, adminConn); ev.Type != "fence" {
		t.Fatalf("admin's first event = %+v, want only the fence (user event duplicated to admin)", ev)
	}
}

// TestBroadcastAdminReachesOnlyAdmins proves admin-only payloads (queue
// contents whose REST equivalents sit behind admin middleware) never reach
// non-admin connections. Same fence technique as the user-routing test.
func TestBroadcastAdminReachesOnlyAdmins(t *testing.T) {
	env := newWSTestEnv(t)
	alice := env.userToken(t, "alice", "hw-alice")

	aliceConn := env.mustDial(t, alice.AccessToken)
	defer aliceConn.Close()
	adminConn := env.mustDial(t, env.adminToken(t))
	defer adminConn.Close()
	env.waitForClients(t, 2)

	env.hub.BroadcastAdmin(Event{
		Type: "downloads_queue",
		Data: map[string]interface{}{"instance_id": "sab-1"},
	})
	env.hub.Broadcast(Event{Type: "fence"})

	ev := readEvent(t, adminConn)
	if ev.Type != "downloads_queue" || ev.Data["instance_id"] != "sab-1" {
		t.Fatalf("admin first event = %+v, want downloads_queue", ev)
	}
	if ev := readEvent(t, adminConn); ev.Type != "fence" {
		t.Fatalf("admin second event = %+v, want fence", ev)
	}
	if ev := readEvent(t, aliceConn); ev.Type != "fence" {
		t.Fatalf("alice's first event = %+v, want only the fence (admin event leaked)", ev)
	}
}

// TestDisconnectUnregistersClient proves closing a connection removes its
// client from the hub, and that broadcasting afterwards neither panics (the
// send channel is closed on unregister) nor resurrects the dead client, while
// surviving connections keep receiving.
func TestDisconnectUnregistersClient(t *testing.T) {
	env := newWSTestEnv(t)
	alice := env.userToken(t, "alice", "hw-alice")
	bob := env.userToken(t, "bob", "hw-bob")

	aliceConn := env.mustDial(t, alice.AccessToken)
	bobConn := env.mustDial(t, bob.AccessToken)
	defer bobConn.Close()
	env.waitForClients(t, 2)

	aliceConn.Close()
	env.waitForClients(t, 1)

	// Target the departed user, then fence. A send to alice's closed channel
	// would panic the hub goroutine and fail the whole test process.
	env.hub.BroadcastUser(alice.User.ID, Event{Type: "request_status_changed"})
	env.hub.Broadcast(Event{Type: "fence"})

	if ev := readEvent(t, bobConn); ev.Type != "fence" {
		t.Fatalf("bob's first event = %+v, want only the fence", ev)
	}
	remaining := env.clientsSnapshot()
	if len(remaining) != 1 || remaining[0].userID != bob.User.ID {
		t.Fatalf("remaining clients = %+v, want only bob's", remaining)
	}
}

// TestBroadcastEvictsStalledClient proves a client whose send buffer is full
// is evicted by the next broadcast: removed from the hub, its send channel
// closed exactly once (buffered messages stay readable), other clients
// unaffected, and the hub still routing afterwards — even when a late
// unregister races the eviction.
//
// The stalled client is constructed directly (same package) with a 1-slot
// send buffer and no running writePump, so "buffer full" is a deterministic
// state rather than a timing accident. A background reader continuously
// iterates the client set under RLock for the duration, exactly as the read
// lock permits: under the old code, which deleted from the map while holding
// only RLock, that overlap is a data race the -race detector reports; the fix
// re-acquires the write lock to evict, so `go test -race` stays clean.
func TestBroadcastEvictsStalledClient(t *testing.T) {
	env := newWSTestEnv(t)
	alice := env.userToken(t, "alice", "hw-alice")

	healthyConn := env.mustDial(t, alice.AccessToken)
	defer healthyConn.Close()
	env.waitForClients(t, 1)

	// A stalled client: registered like any other, but with a tiny send
	// buffer and no writePump draining it.
	stalledClient := &Client{hub: env.hub, userID: alice.User.ID, send: make(chan []byte, 1)}
	env.hub.register <- stalledClient
	env.waitForClients(t, 2)

	// A concurrent reader of the client set, holding the read lock exactly
	// as RLock allows. It runs until the eviction below has been observed.
	stopReader := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stopReader:
				return
			default:
			}
			env.hub.mu.RLock()
			for c := range env.hub.clients {
				_ = c.userID
			}
			env.hub.mu.RUnlock()
		}
	}()

	env.hub.Broadcast(Event{Type: "first"})  // fills the stalled client's buffer
	env.hub.Broadcast(Event{Type: "second"}) // finds it full -> eviction

	env.waitForClients(t, 1)
	close(stopReader)
	<-readerDone

	// The evicted client's channel retains the delivered message and is then
	// closed.
	select {
	case msg, ok := <-stalledClient.send:
		if !ok {
			t.Fatalf("stalled client's buffered message was dropped by eviction")
		}
		if !strings.Contains(string(msg), "first") {
			t.Fatalf("stalled client's buffered message = %q, want the first event", msg)
		}
	default:
		t.Fatalf("stalled client's send channel is empty, want the buffered first event")
	}
	if _, ok := <-stalledClient.send; ok {
		t.Fatalf("stalled client's send channel still open after eviction, want closed")
	}

	// A late unregister for the evicted client (its readPump exiting after
	// the connection actually drops) must not close the channel a second
	// time — a double close would panic the hub goroutine.
	env.hub.unregister <- stalledClient

	// The healthy client received both events in order and the hub is still
	// routing.
	if ev := readEvent(t, healthyConn); ev.Type != "first" {
		t.Fatalf("healthy client first event = %+v, want first", ev)
	}
	if ev := readEvent(t, healthyConn); ev.Type != "second" {
		t.Fatalf("healthy client second event = %+v, want second", ev)
	}
	env.hub.Broadcast(Event{Type: "fence"})
	if ev := readEvent(t, healthyConn); ev.Type != "fence" {
		t.Fatalf("post-eviction event = %+v, want fence (hub goroutine dead?)", ev)
	}

	remaining := env.clientsSnapshot()
	if len(remaining) != 1 || remaining[0] == stalledClient {
		t.Fatalf("remaining clients = %+v, want only the healthy client", remaining)
	}
}

// TestPollResultReachesSubscribedClients drives one manual tick of the radarr
// poll body (the 30s ticker interval is a package const, so the loop itself is
// not instantiable with a tiny interval) against an httptest Radarr and proves
// the resulting download_progress event reaches a connected client end to end.
func TestPollResultReachesSubscribedClients(t *testing.T) {
	env := newWSTestEnv(t)
	alice := env.userToken(t, "alice", "hw-alice")

	conn := env.mustDial(t, alice.AccessToken)
	defer conn.Close()
	env.waitForClients(t, 1)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/queue":
			_, _ = w.Write([]byte(`{"records":[{"movieId":7,"title":"Heat","status":"downloading","size":1000,"sizeleft":250}]}`))
		case "/api/v3/movie/7":
			_, _ = w.Write([]byte(`{"id":7,"title":"Heat","tmdbId":949,"hasFile":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()

	env.hub.pollRadarrInstance("radarr-1", radarr.NewClient(backend.URL, "test-key"))

	ev := readEvent(t, conn)
	if ev.Type != "download_progress" {
		t.Fatalf("event type = %q, want download_progress", ev.Type)
	}
	if ev.Data["tmdb_id"] != float64(949) || ev.Data["media_type"] != "movie" ||
		ev.Data["status"] != "downloading" || ev.Data["instance_id"] != "radarr-1" {
		t.Fatalf("download_progress data = %+v", ev.Data)
	}
	if ev.Data["progress"] != 0.75 {
		t.Fatalf("progress = %v, want 0.75 (size 1000, sizeleft 250)", ev.Data["progress"])
	}
}
