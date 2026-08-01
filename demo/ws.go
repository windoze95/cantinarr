// ws.go — the WebSocket hub (srv-realtime §1). Part of the frozen Stage A
// contract (see contract.md).
//
// Auth: the client offers subprotocols ["Bearer", <access JWT>]; the server
// validates the JWT pre-upgrade (401 on failure) and echoes exactly "Bearer"
// in the 101 response. Envelope: every frame is {"type": <event>, "data": {…}}.
// Keepalive: ping every 54s, 60s read deadline; inbound frames are discarded.
//
// Three delivery scopes:
//   - wsBroadcast(event, data)      — every connected client
//   - wsToAdmins(event, data)       — admin-role connections only
//   - wsToUser(userID, event, data) — that user's connections only
package main

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

const (
	wsWriteWait  = 60 * time.Second
	wsPongWait   = 60 * time.Second
	wsPingPeriod = 54 * time.Second // pongWait * 9 / 10
	wsSendBuffer = 256
)

type wsScope int

const (
	wsScopeAll wsScope = iota
	wsScopeAdmins
	wsScopeUser
)

type wsMessage struct {
	payload []byte
	scope   wsScope
	userID  int // wsScopeUser only
}

type wsClient struct {
	conn    *websocket.Conn
	send    chan []byte
	userID  int
	isAdmin bool
}

type wsHubT struct {
	mu         sync.Mutex
	clients    map[*wsClient]bool
	events     chan wsMessage
	register   chan *wsClient
	unregister chan *wsClient
}

var demoWSHub = &wsHubT{
	clients:    make(map[*wsClient]bool),
	events:     make(chan wsMessage, 256),
	register:   make(chan *wsClient),
	unregister: make(chan *wsClient),
}

// wsStartHub launches the hub's fan-out goroutine. main() calls it once
// before ListenAndServe.
func wsStartHub() {
	go demoWSHub.run()
}

func (h *wsHubT) run() {
	for {
		select {
		case c := <-h.register:
			h.mu.Lock()
			h.clients[c] = true
			h.mu.Unlock()
		case c := <-h.unregister:
			h.mu.Lock()
			if h.clients[c] {
				delete(h.clients, c)
				close(c.send)
			}
			h.mu.Unlock()
		case msg := <-h.events:
			h.mu.Lock()
			for c := range h.clients {
				switch msg.scope {
				case wsScopeAdmins:
					if !c.isAdmin {
						continue
					}
				case wsScopeUser:
					if c.userID != msg.userID {
						continue
					}
				}
				select {
				case c.send <- msg.payload:
				default: // slow client: evict
					delete(h.clients, c)
					close(c.send)
				}
			}
			h.mu.Unlock()
		}
	}
}

func (h *wsHubT) emit(scope wsScope, userID int, event string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	payload, err := json.Marshal(map[string]any{"type": event, "data": data})
	if err != nil {
		return
	}
	h.events <- wsMessage{payload: payload, scope: scope, userID: userID}
}

// wsBroadcast sends {type: event, data: data} to every connected client.
// data may be nil (sent as {}).
func wsBroadcast(event string, data map[string]any) {
	demoWSHub.emit(wsScopeAll, 0, event, data)
}

// wsToAdmins sends the event to admin connections only.
func wsToAdmins(event string, data map[string]any) {
	demoWSHub.emit(wsScopeAdmins, 0, event, data)
}

// wsToUser sends the event to the given user's connections only.
func wsToUser(userID int, event string, data map[string]any) {
	demoWSHub.emit(wsScopeUser, userID, event, data)
}

// wsUpgrader selects the "Bearer" subprotocol so the 101 echoes exactly
// "Bearer" (required by browser builds; the token itself is never echoed).
// CheckOrigin allows every origin — deliberate divergence from the real
// server's same-origin default, matching the demo's permissive CORS so
// browser-hosted app builds can connect.
var wsUpgrader = websocket.Upgrader{
	Subprotocols: []string{"Bearer"},
	CheckOrigin:  func(*http.Request) bool { return true },
}

// registerWS mounts GET /ws on the public /api router (it authenticates via
// the subprotocol offer, not the Authorization header).
func registerWS(r chi.Router) {
	r.Get("/ws", serveWS)
}

func serveWS(w http.ResponseWriter, r *http.Request) {
	protocols := websocket.Subprotocols(r)
	if len(protocols) < 2 || protocols[0] != "Bearer" {
		writeErr(w, http.StatusUnauthorized, "missing auth")
		return
	}
	claims, err := parseAccessClaims(protocols[1])
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid token")
		return
	}
	if claims.DeviceID != "" && deviceRevoked(claims.DeviceID) {
		writeErr(w, http.StatusUnauthorized, "invalid token")
		return
	}
	u := userByID(claims.UserID)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "invalid token")
		return
	}
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already wrote the error response
	}
	client := &wsClient{
		conn:    conn,
		send:    make(chan []byte, wsSendBuffer),
		userID:  u.ID,
		isAdmin: u.Role == roleAdmin,
	}
	demoWSHub.register <- client
	go client.writePump()
	go client.readPump()
}

// readPump services pongs and discards every inbound frame.
func (c *wsClient) readPump() {
	defer func() {
		demoWSHub.unregister <- c
		c.conn.Close()
	}()
	_ = c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (c *wsClient) writePump() {
	ticker := time.NewTicker(wsPingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
