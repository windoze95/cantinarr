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
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	mu         sync.RWMutex

	authService *auth.Service
	radarr      *radarr.Client
	sonarr      *sonarr.Client

	// Previous polling state for detecting transitions
	prevRadarrQueue map[int]float64 // movieId -> progress
	prevSonarrQueue map[int]float64 // seriesId -> progress
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// NewHub creates a new WebSocket hub.
func NewHub(authService *auth.Service, radarrClient *radarr.Client, sonarrClient *sonarr.Client) *Hub {
	return &Hub{
		clients:         make(map[*Client]bool),
		broadcast:       make(chan []byte, 256),
		register:        make(chan *Client),
		unregister:      make(chan *Client),
		authService:     authService,
		radarr:          radarrClient,
		sonarr:          sonarrClient,
		prevRadarrQueue: make(map[int]float64),
		prevSonarrQueue: make(map[int]float64),
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
	conn, err := upgrader.Upgrade(w, r, header)
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
			h.pollRadarr()
			h.pollSonarr()
		}
	}
}

func (h *Hub) pollRadarr() {
	if h.radarr == nil {
		return
	}

	queue, err := h.radarr.GetQueue()
	if err != nil {
		log.Printf("websocket: poll radarr queue: %v", err)
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
		movie, err := h.radarr.GetMovie(item.MovieID)
		if err != nil {
			log.Printf("websocket: get radarr movie %d: %v", item.MovieID, err)
			continue
		}

		h.Broadcast(Event{
			Type: "download_progress",
			Data: map[string]interface{}{
				"tmdb_id":    movie.TmdbID,
				"media_type": "movie",
				"progress":   progress,
				"status":     "downloading",
			},
		})
	}

	// Check for items that were previously downloading but are no longer in queue
	for movieID := range h.prevRadarrQueue {
		if _, stillInQueue := currentQueue[movieID]; !stillInQueue {
			movie, err := h.radarr.GetMovie(movieID)
			if err != nil {
				log.Printf("websocket: get completed radarr movie %d: %v", movieID, err)
				continue
			}
			if movie.HasFile {
				h.Broadcast(Event{
					Type: "request_status_changed",
					Data: map[string]interface{}{
						"tmdb_id":    movie.TmdbID,
						"media_type": "movie",
						"status":     "available",
					},
				})
			}
		}
	}

	h.prevRadarrQueue = currentQueue
}

func (h *Hub) pollSonarr() {
	if h.sonarr == nil {
		return
	}

	queue, err := h.sonarr.GetQueue()
	if err != nil {
		log.Printf("websocket: poll sonarr queue: %v", err)
		return
	}

	currentQueue := make(map[int]float64)
	for _, item := range queue {
		var progress float64
		if item.Size > 0 {
			progress = (item.Size - item.Sizeleft) / item.Size
		}
		currentQueue[item.SeriesID] = progress

		series, err := h.sonarr.GetSeries(item.SeriesID)
		if err != nil {
			log.Printf("websocket: get sonarr series %d: %v", item.SeriesID, err)
			continue
		}

		h.Broadcast(Event{
			Type: "download_progress",
			Data: map[string]interface{}{
				"tmdb_id":    series.TmdbID,
				"media_type": "tv",
				"progress":   progress,
				"status":     "downloading",
			},
		})
	}

	// Check for items that left the queue
	for seriesID := range h.prevSonarrQueue {
		if _, stillInQueue := currentQueue[seriesID]; !stillInQueue {
			series, err := h.sonarr.GetSeries(seriesID)
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
					"tmdb_id":    series.TmdbID,
					"media_type": "tv",
					"status":     status,
				},
			})
		}
	}

	h.prevSonarrQueue = currentQueue
}
