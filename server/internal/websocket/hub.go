package websocket

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

const (
	writeWait  = 60 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
	pollPeriod = 30 * time.Second
)

// Event represents a WebSocket event sent to clients.
type Event struct {
	Type string                 `json:"type"`
	Data map[string]interface{} `json:"data"`
}

// Client represents a connected WebSocket client.
type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	userID int64
	send   chan []byte
}

// Hub manages WebSocket clients and broadcasts events.
type Hub struct {
	upgrader   websocket.Upgrader
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	mu         sync.RWMutex

	authService *auth.Service
	registry    *instance.Registry
	store       *instance.Store

	// Legacy direct clients (used when registry is nil)
	radarr *radarr.Client
	sonarr *sonarr.Client

	// Previous polling state for detecting transitions
	prevRadarrQueue map[string]map[int]float64 // instanceID -> movieId -> progress
	prevSonarrQueue map[string]map[int]float64 // instanceID -> seriesId -> progress
}

// NewHub creates a new WebSocket hub.
func NewHub(authService *auth.Service, registry *instance.Registry, store *instance.Store, radarrClient *radarr.Client, sonarrClient *sonarr.Client, allowedOrigins []string) *Hub {
	upgrader := websocket.Upgrader{
		CheckOrigin: makeOriginChecker(allowedOrigins),
	}
	return &Hub{
		upgrader:        upgrader,
		clients:         make(map[*Client]bool),
		broadcast:       make(chan []byte, 256),
		register:        make(chan *Client),
		unregister:      make(chan *Client),
		authService:     authService,
		registry:        registry,
		store:           store,
		radarr:          radarrClient,
		sonarr:          sonarrClient,
		prevRadarrQueue: make(map[string]map[int]float64),
		prevSonarrQueue: make(map[string]map[int]float64),
	}
}

// makeOriginChecker returns a CheckOrigin function. If allowedOrigins is empty,
// it allows same-origin only (gorilla/websocket default). Otherwise it checks
// the Origin header against the allowed list.
func makeOriginChecker(allowedOrigins []string) func(r *http.Request) bool {
	if len(allowedOrigins) == 0 {
		// Default: allow same-origin only (nil means gorilla default check)
		return nil
	}
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = true
	}
	return func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		return allowed[origin]
	}
}

// Run starts the hub's main loop and polling goroutine.
func (h *Hub) Run(ctx context.Context) {
	go h.pollLoop(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()
		case msg := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- msg:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Broadcast sends an event to all connected clients.
func (h *Hub) Broadcast(event Event) {
	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("websocket: marshal event: %v", err)
		return
	}
	h.broadcast <- data
}

// ServeWS handles WebSocket upgrade with JWT auth via subprotocol.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	// Read JWT from Sec-WebSocket-Protocol header.
	// Flutter sends protocols: ['Bearer', 'actualToken']
	protocols := websocket.Subprotocols(r)
	if len(protocols) < 2 || protocols[0] != "Bearer" {
		http.Error(w, "missing auth", http.StatusUnauthorized)
		return
	}
	token := protocols[1]

	claims, err := h.authService.ValidateToken(token)
	if err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	// Upgrade with the Bearer subprotocol so the client knows auth succeeded
	header := http.Header{}
	conn, err := h.upgrader.Upgrade(w, r, header)
	if err != nil {
		log.Printf("websocket: upgrade: %v", err)
		return
	}

	client := &Client{
		hub:    h,
		conn:   conn,
		userID: claims.UserID,
		send:   make(chan []byte, 256),
	}
	h.register <- client

	go client.writePump()
	go client.readPump()
}

func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (h *Hub) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(pollPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.pollAllRadarr()
			h.pollAllSonarr()
		}
	}
}

func (h *Hub) pollAllRadarr() {
	// Poll via registry (all instances)
	if h.store != nil {
		instances, err := h.store.List("radarr")
		if err == nil {
			for _, inst := range instances {
				if h.registry == nil {
					continue
				}
				client, err := h.registry.GetRadarrClient(inst.ID)
				if err != nil {
					continue
				}
				h.pollRadarrInstance(inst.ID, client)
			}
			return
		}
	}

	// Legacy fallback
	if h.radarr != nil {
		h.pollRadarrInstance("legacy", h.radarr)
	}
}

func (h *Hub) pollAllSonarr() {
	// Poll via registry (all instances)
	if h.store != nil {
		instances, err := h.store.List("sonarr")
		if err == nil {
			for _, inst := range instances {
				if h.registry == nil {
					continue
				}
				client, err := h.registry.GetSonarrClient(inst.ID)
				if err != nil {
					continue
				}
				h.pollSonarrInstance(inst.ID, client)
			}
			return
		}
	}

	// Legacy fallback
	if h.sonarr != nil {
		h.pollSonarrInstance("legacy", h.sonarr)
	}
}

func (h *Hub) pollRadarrInstance(instanceID string, client *radarr.Client) {
	queue, err := client.GetQueue()
	if err != nil {
		log.Printf("websocket: poll radarr queue (%s): %v", instanceID, err)
		return
	}

	currentQueue := make(map[int]float64)
	for _, item := range queue {
		var progress float64
		if item.Size > 0 {
			progress = (item.Size - item.Sizeleft) / item.Size
		}
		currentQueue[item.MovieID] = progress

		// Look up TMDB ID for this movie
		movie, err := client.GetMovie(item.MovieID)
		if err != nil {
			log.Printf("websocket: get radarr movie %d: %v", item.MovieID, err)
			continue
		}

		h.Broadcast(Event{
			Type: "download_progress",
			Data: map[string]interface{}{
				"tmdb_id":     movie.TmdbID,
				"media_type":  "movie",
				"progress":    progress,
				"status":      "downloading",
				"instance_id": instanceID,
			},
		})
	}

	// Check for items that were previously downloading but are no longer in queue
	prevQueue := h.prevRadarrQueue[instanceID]
	if prevQueue != nil {
		for movieID := range prevQueue {
			if _, stillInQueue := currentQueue[movieID]; !stillInQueue {
				movie, err := client.GetMovie(movieID)
				if err != nil {
					log.Printf("websocket: get completed radarr movie %d: %v", movieID, err)
					continue
				}
				if movie.HasFile {
					h.Broadcast(Event{
						Type: "request_status_changed",
						Data: map[string]interface{}{
							"tmdb_id":     movie.TmdbID,
							"media_type":  "movie",
							"status":      "available",
							"instance_id": instanceID,
						},
					})
				}
			}
		}
	}

	h.prevRadarrQueue[instanceID] = currentQueue
}

func (h *Hub) pollSonarrInstance(instanceID string, client *sonarr.Client) {
	queue, err := client.GetQueue()
	if err != nil {
		log.Printf("websocket: poll sonarr queue (%s): %v", instanceID, err)
		return
	}

	currentQueue := make(map[int]float64)
	for _, item := range queue {
		var progress float64
		if item.Size > 0 {
			progress = (item.Size - item.Sizeleft) / item.Size
		}
		currentQueue[item.SeriesID] = progress

		series, err := client.GetSeries(item.SeriesID)
		if err != nil {
			log.Printf("websocket: get sonarr series %d: %v", item.SeriesID, err)
			continue
		}

		h.Broadcast(Event{
			Type: "download_progress",
			Data: map[string]interface{}{
				"tmdb_id":     series.TmdbID,
				"media_type":  "tv",
				"progress":    progress,
				"status":      "downloading",
				"instance_id": instanceID,
			},
		})
	}

	// Check for items that left the queue
	prevQueue := h.prevSonarrQueue[instanceID]
	if prevQueue != nil {
		for seriesID := range prevQueue {
			if _, stillInQueue := currentQueue[seriesID]; !stillInQueue {
				series, err := client.GetSeries(seriesID)
				if err != nil {
					log.Printf("websocket: get completed sonarr series %d: %v", seriesID, err)
					continue
				}
				status := "available"
				if series.Statistics != nil && series.Statistics.PercentOfEpisodes < 100 {
					status = "partially_available"
				}
				h.Broadcast(Event{
					Type: "request_status_changed",
					Data: map[string]interface{}{
						"tmdb_id":     series.TmdbID,
						"media_type":  "tv",
						"status":      status,
						"instance_id": instanceID,
					},
				})
			}
		}
	}

	h.prevSonarrQueue[instanceID] = currentQueue
}
