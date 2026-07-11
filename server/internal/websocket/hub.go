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
	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/chaptarr"
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

// ContentNotifier pushes new-content alerts to opted-in users. *push.Notifier
// satisfies it. Declared here (rather than importing push) so the hub stays
// decoupled from the push package and easy to test with a fake.
type ContentNotifier interface {
	NotifyNewMovie(title string, tmdbID int)
	NotifyNewEpisode(seriesTitle string, tmdbID int)
}

// IssueOpener is the auto-dispatch seam: after every successful detailed queue
// read, the poller hands one complete diagnosed snapshot to the remediation
// feature. *remediation.AutoDispatcher satisfies it. It is declared here
// (rather than importing remediation) so the hub stays decoupled and is wired
// exactly like ContentNotifier: nil (the zero value) means auto-dispatch is off,
// so the poll path skips the detailed-queue fetch and diagnosis entirely.
//
// The observer owns all temporal policy and issue lifecycle decisions. The hub
// deliberately does not debounce, open, or close issues: it reports arr state,
// including healthy items and empty snapshots, without interpreting whether an
// in-flight retry has had enough time to recover.
type IssueOpener interface {
	// ObserveQueueSnapshot is called exactly once for each successful detailed
	// queue read. serviceType is "radarr" or "sonarr". items is the full queue,
	// including healthy entries, and is empty when the successful read found no
	// entries. Failed reads do not call this method. Implementations must not block
	// the poll goroutine.
	ObserveQueueSnapshot(serviceType, instanceID string, items []arr.QueueObservation)
}

// Client represents a connected WebSocket client.
type Client struct {
	hub     *Hub
	conn    *websocket.Conn
	userID  int64
	isAdmin bool
	send    chan []byte
}

// Hub manages WebSocket clients and broadcasts events.
type Hub struct {
	upgrader   websocket.Upgrader
	clients    map[*Client]bool
	broadcast  chan outboundMessage
	register   chan *Client
	unregister chan *Client
	mu         sync.RWMutex

	authService *auth.Service
	registry    *instance.Registry
	store       *instance.Store

	// content pushes new-movie/new-episode notifications to opted-in users
	// when a download completes. nil when push is not configured.
	content ContentNotifier

	// opener receives stuck/blocked downloads for auto-dispatch. nil (the zero
	// value) disables the whole auto-dispatch path: the poll loop then skips the
	// detailed-queue diagnosis. Set only when the remediation feature is wired.
	opener IssueOpener

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

	// pollMu guards prevDownloadsHash and downloadsErrLogged, which are
	// written from concurrent per-instance poll goroutines.
	pollMu sync.Mutex
}

// NewHub creates a new WebSocket hub. content pushes new-content alerts when a
// download completes; pass nil (or a nil *push.Notifier) when push is disabled.
// opener receives stuck/blocked downloads for auto-dispatch; pass nil (or a nil
// remediation.AutoDispatcher) to keep the whole auto-dispatch path off.
func NewHub(authService *auth.Service, registry *instance.Registry, store *instance.Store, content ContentNotifier, opener IssueOpener) *Hub {
	return &Hub{
		clients:            make(map[*Client]bool),
		broadcast:          make(chan outboundMessage, 256),
		register:           make(chan *Client),
		unregister:         make(chan *Client),
		authService:        authService,
		registry:           registry,
		store:              store,
		content:            content,
		opener:             opener,
		prevRadarrQueue:    make(map[string]map[int]float64),
		prevSonarrQueue:    make(map[string]map[int]float64),
		prevArrQueueHash:   make(map[string]string),
		prevDownloadsHash:  make(map[string]string),
		downloadsErrLogged: make(map[string]bool),
	}
}

// SetIssueOpener wires the auto-dispatch opener after construction. This exists
// because the opener (a remediation AutoDispatcher) depends on the notifier
// composite, which in turn depends on this hub — a construction cycle the
// content notifier sidesteps because it does not. Call it BEFORE Run starts the
// poll goroutine; it is not safe to call concurrently with a running poll loop.
// Passing nil leaves auto-dispatch off.
func (h *Hub) SetIssueOpener(opener IssueOpener) {
	h.opener = opener
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
				if msg.adminOnly && !client.isAdmin {
					continue
				}
				if msg.userID != 0 && client.userID != msg.userID {
					continue
				}
				select {
				case client.send <- msg.data:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// outboundMessage pairs a marshaled event with its audience. When userID is
// non-zero the event is delivered only to that user's connected clients.
type outboundMessage struct {
	data      []byte
	adminOnly bool
	userID    int64
}

// Broadcast sends an event to all connected clients.
func (h *Hub) Broadcast(event Event) {
	h.enqueue(event, false, 0)
}

// BroadcastAdmin sends an event only to clients authenticated as admins.
// Used for payloads whose REST equivalents sit behind the admin middleware
// (e.g. download-client queue contents).
func (h *Hub) BroadcastAdmin(event Event) {
	h.enqueue(event, true, 0)
}

// BroadcastUser sends an event only to the connected clients of one user.
// A non-positive userID would otherwise degrade to a global broadcast, so it
// is dropped.
func (h *Hub) BroadcastUser(userID int64, event Event) {
	if userID <= 0 {
		return
	}
	h.enqueue(event, false, userID)
}

// NotifyUser delivers an event to a single user (implements request.Notifier).
func (h *Hub) NotifyUser(userID int64, eventType string, data map[string]interface{}) {
	h.BroadcastUser(userID, Event{Type: eventType, Data: data})
}

// NotifyAdmins delivers an event to all admin clients (implements request.Notifier).
func (h *Hub) NotifyAdmins(eventType string, data map[string]interface{}) {
	h.BroadcastAdmin(Event{Type: eventType, Data: data})
}

func (h *Hub) enqueue(event Event, adminOnly bool, userID int64) {
	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("websocket: marshal event: %v", err)
		return
	}
	h.broadcast <- outboundMessage{data: data, adminOnly: adminOnly, userID: userID}
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

	claims, _, err := h.authService.AuthenticateToken(token)
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
		hub:     h,
		conn:    conn,
		userID:  claims.UserID,
		isAdmin: auth.HasPermission(claims.Role, auth.PermissionAdmin),
		send:    make(chan []byte, 256),
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
			h.pollAllChaptarr()
		case <-downloadsTicker.C:
			h.pollAllDownloadClients()
		}
	}
}

func (h *Hub) pollAllDownloadClients() {
	if h.store == nil || h.registry == nil {
		return
	}
	// Poll instances concurrently: a hung backend (30s client timeout, two
	// calls per snapshot) would otherwise stall the shared poll goroutine
	// and starve every other instance's events for minutes.
	var wg sync.WaitGroup
	for _, serviceType := range downloadClientTypes {
		instances, err := h.store.List(serviceType)
		if err != nil {
			continue
		}
		for _, inst := range instances {
			wg.Add(1)
			go func(inst instance.Instance) {
				defer wg.Done()
				h.pollDownloadClientInstance(inst)
			}(inst)
		}
	}
	wg.Wait()
}

func (h *Hub) pollDownloadClientInstance(inst instance.Instance) {
	view, err := downloads.Snapshot(h.registry, inst)
	if err != nil {
		h.pollMu.Lock()
		if !h.downloadsErrLogged[inst.ID] {
			log.Printf("websocket: poll downloads queue (%s/%s): %v", inst.ServiceType, inst.ID, err)
			h.downloadsErrLogged[inst.ID] = true
		}
		h.pollMu.Unlock()
		return
	}
	h.pollMu.Lock()
	delete(h.downloadsErrLogged, inst.ID)
	h.pollMu.Unlock()

	payload, err := json.Marshal(view)
	if err != nil {
		log.Printf("websocket: marshal downloads snapshot (%s): %v", inst.ID, err)
		return
	}
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])
	h.pollMu.Lock()
	unchanged := h.prevDownloadsHash[inst.ID] == hash
	if !unchanged {
		h.prevDownloadsHash[inst.ID] = hash
	}
	h.pollMu.Unlock()
	if unchanged {
		return
	}

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

	h.BroadcastAdmin(Event{
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

// autoDispatchEnabled reports whether the auto-dispatch path is wired at all.
// When the opener is nil the poll path skips the extra detailed-queue fetch and
// the diagnosis entirely, so a server without the remediation feature pays
// nothing. The opener itself still re-checks the live Enabled/AutoDispatch
// toggles per call (so flipping them takes effect without a restart); this is
// only the cheap "is it even wired" short-circuit.
func (h *Hub) autoDispatchEnabled() bool { return h.opener != nil }

// dispatchDetailedItems runs the Import Doctor over every item in one
// successful detailed queue snapshot and forwards the complete observation to
// the remediation observer exactly once. Healthy entries and entries without a
// download id are intentionally preserved: they are evidence that an arr is
// still working, and temporal issue policy belongs to the observer rather than
// this transport layer. An empty successful snapshot is forwarded as an empty
// (non-nil) slice. Fetch failures return before this function is called.
func (h *Hub) dispatchDetailedItems(serviceType, instanceID string, items []arr.QueueObservation) {
	if h.opener == nil {
		return
	}

	observations := make([]arr.QueueObservation, len(items))
	copy(observations, items)
	for i := range observations {
		observations[i].Diagnosis = arr.Diagnose(observations[i].Signal)
	}
	h.opener.ObserveQueueSnapshot(serviceType, instanceID, observations)
}

// radarrQueueSignal projects a Radarr detailed queue item into the neutral
// classifier signal plus its stable download id. It mirrors mcp.radarrSignal
// (kept local so the hub need not import the mcp package).
func radarrQueueSignal(item radarr.DetailedQueueItem) arr.QueueObservation {
	messages := make([]arr.StatusMessage, 0, len(item.StatusMessages))
	for _, m := range item.StatusMessages {
		messages = append(messages, arr.StatusMessage{Title: m.Title, Messages: m.Messages})
	}
	media := arr.QueueMediaContext{QueueID: item.ID, Title: item.Title}
	if item.Movie != nil {
		media.Title = item.Movie.Title
		media.TmdbID = item.Movie.TmdbID
	}
	return arr.QueueObservation{
		DownloadID: item.DownloadID,
		Media:      media,
		Signal: arr.QueueSignal{
			Status:                item.Status,
			TrackedDownloadStatus: item.TrackedDownloadStatus,
			TrackedDownloadState:  item.TrackedDownloadState,
			ErrorMessage:          item.ErrorMessage,
			StatusMessages:        messages,
			Protocol:              item.Protocol,
			Size:                  item.Size,
			SizeLeft:              item.Sizeleft,
		},
	}
}

// sonarrQueueSignal projects a Sonarr detailed queue item into the neutral
// classifier signal plus its stable download id. It mirrors mcp.sonarrSignal.
func sonarrQueueSignal(item sonarr.DetailedQueueItem) arr.QueueObservation {
	messages := make([]arr.StatusMessage, 0, len(item.StatusMessages))
	for _, m := range item.StatusMessages {
		messages = append(messages, arr.StatusMessage{Title: m.Title, Messages: m.Messages})
	}
	media := arr.QueueMediaContext{QueueID: item.ID, Title: item.Title}
	if item.Series != nil {
		media.Title = item.Series.Title
		media.TmdbID = item.Series.TmdbID
		media.TvdbID = item.Series.TvdbID
	}
	if item.Episode != nil {
		media.SeasonNumber = item.Episode.SeasonNumber
		media.EpisodeNumber = item.Episode.EpisodeNumber
	}
	return arr.QueueObservation{
		DownloadID: item.DownloadID,
		Media:      media,
		Signal: arr.QueueSignal{
			Status:                item.Status,
			TrackedDownloadStatus: item.TrackedDownloadStatus,
			TrackedDownloadState:  item.TrackedDownloadState,
			ErrorMessage:          item.ErrorMessage,
			StatusMessages:        messages,
			Protocol:              item.Protocol,
			Size:                  item.Size,
			SizeLeft:              item.Sizeleft,
		},
	}
}

// autoDispatchRadarr fetches the detailed queue (the lightweight GetQueue used
// for progress lacks tracked-download fields, so it cannot drive Diagnose) and
// delivers one complete diagnosed snapshot. A fetch error is logged and
// skipped: it produces no observation and cannot be mistaken for an empty
// queue. The progress poll above already ran off the lightweight queue, so a
// detailed fetch failure never affects download_progress events.
func (h *Hub) autoDispatchRadarr(instanceID string, client *radarr.Client) {
	if !h.autoDispatchEnabled() {
		return
	}
	items, err := client.GetQueueDetailed()
	if err != nil {
		log.Printf("websocket: auto-dispatch radarr detailed queue (%s): %v", instanceID, err)
		return
	}
	observations := make([]arr.QueueObservation, 0, len(items))
	for _, item := range items {
		observations = append(observations, radarrQueueSignal(item))
	}
	h.dispatchDetailedItems("radarr", instanceID, observations)
}

// autoDispatchSonarr is the Sonarr analogue of autoDispatchRadarr.
func (h *Hub) autoDispatchSonarr(instanceID string, client *sonarr.Client) {
	if !h.autoDispatchEnabled() {
		return
	}
	items, err := client.GetQueueDetailed()
	if err != nil {
		log.Printf("websocket: auto-dispatch sonarr detailed queue (%s): %v", instanceID, err)
		return
	}
	observations := make([]arr.QueueObservation, 0, len(items))
	for _, item := range items {
		observations = append(observations, sonarrQueueSignal(item))
	}
	h.dispatchDetailedItems("sonarr", instanceID, observations)
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

func (h *Hub) pollAllChaptarr() {
	if h.store == nil || h.registry == nil {
		return
	}
	instances, err := h.store.List("chaptarr")
	if err != nil {
		return
	}
	for _, inst := range instances {
		client, err := h.registry.GetChaptarrClient(inst.ID)
		if err != nil {
			continue
		}
		h.pollChaptarrInstance(inst.ID, client)
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
					if h.content != nil {
						h.content.NotifyNewMovie(movie.Title, movie.TmdbID)
					}
				}
			}
		}
	}

	h.prevRadarrQueue[instanceID] = currentQueue
	h.noteArrQueueComposition(instanceID, "radarr", tuples)

	// Auto-dispatch observation pass: diagnose and deliver the full detailed
	// queue. No-op (and no extra fetch) when the observer is nil. Runs on this
	// poll goroutine; the observer is required to be non-blocking.
	h.autoDispatchRadarr(instanceID, client)
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
				// "available" strictly means every aired episode has a file.
				// Sonarr's percentOfEpisodes only counts monitored episodes,
				// so it reads 100 for a series that's mostly unmonitored and
				// missing — which would flip request buttons green over this
				// broadcast for incomplete series.
				status := "available"
				if episodes, err := client.GetAllEpisodes(seriesID); err == nil {
					if completion, _ := sonarr.SeriesCompletion(episodes, time.Now()); !completion.Complete() {
						status = "partially_available"
					}
				} else {
					files, total := series.EpisodeTotals()
					if total == 0 || files < total {
						status = "partially_available"
					}
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
				if h.content != nil {
					h.content.NotifyNewEpisode(series.Title, series.TmdbID)
				}
			}
		}
	}

	h.prevSonarrQueue[instanceID] = currentQueue
	h.noteArrQueueComposition(instanceID, "sonarr", tuples)

	// Auto-dispatch pass (see pollRadarrInstance). No-op when the opener is nil.
	h.autoDispatchSonarr(instanceID, client)
}

// pollChaptarrInstance emits an arr_queue_changed invalidation ping whenever the
// instance's download queue composition changes. Unlike the Radarr/Sonarr
// pollers it does not emit per-item download_progress events: Chaptarr books
// carry no TMDB id, which those events key on. There is also no auto-dispatch
// pass; remediation does not cover chaptarr.
func (h *Hub) pollChaptarrInstance(instanceID string, client *chaptarr.Client) {
	queue, err := client.GetQueue()
	if err != nil {
		log.Printf("websocket: poll chaptarr queue (%s): %v", instanceID, err)
		return
	}

	tuples := make([]string, 0, len(queue))
	for _, item := range queue {
		tuples = append(tuples, fmt.Sprintf("%d|%s|%.0f", item.BookID, item.Status, item.Sizeleft))
	}

	h.noteArrQueueComposition(instanceID, "chaptarr", tuples)
}
