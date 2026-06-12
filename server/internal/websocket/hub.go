package websocket

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/downloads"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

const (
	writeWait           = 60 * time.Second
	pongWait            = 60 * time.Second
	pingPeriod          = (pongWait * 9) / 10
	pollPeriod          = 30 * time.Second
	downloadsPollPeriod = 15 * time.Second
)

// downloadClientTypes are the service types polled for downloads_queue events.
var downloadClientTypes = []string{"sabnzbd", "qbittorrent", "nzbget", "transmission"}

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

	// Previous polling state for detecting transitions
	prevRadarrQueue map[string]map[int]float64 // instanceID -> movieId -> progress
	prevSonarrQueue map[string]map[int]float64 // instanceID -> seriesId -> progress

	// prevArrQueueHash tracks the queue composition (id/status/sizeleft
	// tuples) per arr instance so any change can emit an invalidation ping.
	prevArrQueueHash map[string]string // instanceID -> composition hash

	// prevDownloadsHash tracks the marshaled downloads snapshot per download
	// client instance so downloads_queue is only broadcast on change.
	prevDownloadsHash map[string]string // instanceID -> snapshot hash

	// downloadsErrLogged suppresses repeat error logs for an instance until
	// it succeeds again (one log per failure streak).
	downloadsErrLogged map[string]bool
}

// NewHub creates a new WebSocket hub.
func NewHub(authService *auth.Service, registry *instance.Registry, store *instance.Store) *Hub {
	return &Hub{
		clients:            make(map[*Client]bool),
		broadcast:          make(chan []byte, 256),
		register:           make(chan *Client),
		unregister:         make(chan *Client),
		authService:        authService,
		registry:           registry,
		store:              store,
		prevRadarrQueue:    make(map[string]map[int]float64),
		prevSonarrQueue:    make(map[string]map[int]float64),
		prevArrQueueHash:   make(map[string]string),
		prevDownloadsHash:  make(map[string]string),
		downloadsErrLogged: make(map[string]bool),
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
	arrTicker := time.NewTicker(pollPeriod)
	defer arrTicker.Stop()
	downloadsTicker := time.NewTicker(downloadsPollPeriod)
	defer downloadsTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-arrTicker.C:
			h.pollAllRadarr()
			h.pollAllSonarr()
		case <-downloadsTicker.C:
			h.pollAllDownloadClients()
		}
	}
}

func (h *Hub) pollAllDownloadClients() {
	if h.store == nil || h.registry == nil {
		return
	}
	for _, serviceType := range downloadClientTypes {
		instances, err := h.store.List(serviceType)
		if err != nil {
			continue
		}
		for _, inst := range instances {
			h.pollDownloadClientInstance(inst)
		}
	}
}

func (h *Hub) pollDownloadClientInstance(inst instance.Instance) {
	view, err := downloads.Snapshot(h.registry, inst)
	if err != nil {
		if !h.downloadsErrLogged[inst.ID] {
			log.Printf("websocket: poll downloads queue (%s/%s): %v", inst.ServiceType, inst.ID, err)
			h.downloadsErrLogged[inst.ID] = true
		}
		return
	}
	delete(h.downloadsErrLogged, inst.ID)

	payload, err := json.Marshal(view)
	if err != nil {
		log.Printf("websocket: marshal downloads snapshot (%s): %v", inst.ID, err)
		return
	}
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])
	if h.prevDownloadsHash[inst.ID] == hash {
		return
	}
	h.prevDownloadsHash[inst.ID] = hash

	// Decode the snapshot back into a map so the event carries exactly the
	// QueueView JSON shape (paused, speed_bps, items) plus instance_id.
	data := make(map[string]interface{})
	if err := json.Unmarshal(payload, &data); err != nil {
		log.Printf("websocket: decode downloads snapshot (%s): %v", inst.ID, err)
		return
	}
	if data["items"] == nil {
		data["items"] = []interface{}{}
	}
	data["instance_id"] = inst.ID

	h.Broadcast(Event{
		Type: "downloads_queue",
		Data: data,
	})
}

// queueCompositionHash builds an order-independent hash over per-item tuples
// so any queue change (add/remove/status/progress) is detected cheaply.
func queueCompositionHash(tuples []string) string {
	sort.Strings(tuples)
	sum := sha256.Sum256([]byte(strings.Join(tuples, "\n")))
	return hex.EncodeToString(sum[:])
}

// noteArrQueueComposition compares the instance's queue composition hash to
// the previous poll and broadcasts an arr_queue_changed invalidation ping on
// any change. The first poll only seeds the hash.
func (h *Hub) noteArrQueueComposition(instanceID, serviceType string, tuples []string) {
	newHash := queueCompositionHash(tuples)
	prevHash, seen := h.prevArrQueueHash[instanceID]
	h.prevArrQueueHash[instanceID] = newHash
	if !seen || prevHash == newHash {
		return
	}
	h.Broadcast(Event{
		Type: "arr_queue_changed",
		Data: map[string]interface{}{
			"instance_id":  instanceID,
			"service_type": serviceType,
		},
	})
}

func (h *Hub) pollAllRadarr() {
	if h.store == nil || h.registry == nil {
		return
	}
	instances, err := h.store.List("radarr")
	if err != nil {
		return
	}
	for _, inst := range instances {
		client, err := h.registry.GetRadarrClient(inst.ID)
		if err != nil {
			continue
		}
		h.pollRadarrInstance(inst.ID, client)
	}
}

func (h *Hub) pollAllSonarr() {
	if h.store == nil || h.registry == nil {
		return
	}
	instances, err := h.store.List("sonarr")
	if err != nil {
		return
	}
	for _, inst := range instances {
		client, err := h.registry.GetSonarrClient(inst.ID)
		if err != nil {
			continue
		}
		h.pollSonarrInstance(inst.ID, client)
	}
}

func (h *Hub) pollRadarrInstance(instanceID string, client *radarr.Client) {
	queue, err := client.GetQueue()
	if err != nil {
		log.Printf("websocket: poll radarr queue (%s): %v", instanceID, err)
		return
	}

	currentQueue := make(map[int]float64)
	tuples := make([]string, 0, len(queue))
	for _, item := range queue {
		var progress float64
		if item.Size > 0 {
			progress = (item.Size - item.Sizeleft) / item.Size
		}
		currentQueue[item.MovieID] = progress
		tuples = append(tuples, fmt.Sprintf("%d|%s|%.0f", item.MovieID, item.Status, item.Sizeleft))

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
	h.noteArrQueueComposition(instanceID, "radarr", tuples)
}

func (h *Hub) pollSonarrInstance(instanceID string, client *sonarr.Client) {
	queue, err := client.GetQueue()
	if err != nil {
		log.Printf("websocket: poll sonarr queue (%s): %v", instanceID, err)
		return
	}

	currentQueue := make(map[int]float64)
	tuples := make([]string, 0, len(queue))
	for _, item := range queue {
		var progress float64
		if item.Size > 0 {
			progress = (item.Size - item.Sizeleft) / item.Size
		}
		currentQueue[item.SeriesID] = progress
		tuples = append(tuples, fmt.Sprintf("%d|%s|%.0f", item.SeriesID, item.Status, item.Sizeleft))

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
	h.noteArrQueueComposition(instanceID, "sonarr", tuples)
}
