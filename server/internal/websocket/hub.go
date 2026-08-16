package websocket

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// queueWitnessStaleAfter bounds how far back any resumption may reach — a
// restart's persisted snapshot and an in-process poll gap alike. A window older
// than this is dropped whole and the instance re-seeds exactly as on a first
// boot. Long enough to cover a container restart, an image pull, or a host
// reboot; short enough that we never announce content the user has already
// found by opening the app, where availability is always computed live.
const queueWitnessStaleAfter = 6 * time.Hour

// restoredAlertCap bounds the single announce batch resumed after a restart or
// a poll gap — witnessed queue departures and history catch-up together. Above
// it the whole batch is dropped rather than partially delivered: new-content
// alerts fan out to the entire household by default and the only remedy a user
// has is a permanent category opt-out, so a burst of stale alerts costs more
// trust than silence — and past this count the app's live view is the better
// answer anyway. Steady-state departures are never capped.
const restoredAlertCap = 10

// queueResumeAfter is the gap between successful polls beyond which the next
// one is treated as a resumption rather than steady state: the departure diff
// becomes capped (it may hold a gap's worth of stale completions) and the
// import-history catch-up runs to recover completions no queue snapshot ever
// witnessed. It covers arr/network outages and process suspensions the same
// way a restart is covered, and must sit far above the poll period so a
// missed tick or two stays steady state.
const queueResumeAfter = 5 * time.Minute

// catchUpHistoryPageSize bounds the single history page a resumption reads.
// One page must prove it covered the whole gap; a window holding more import
// records than this is a mass job (bulk manual import, weeks of automation),
// and the whole batch is dropped rather than sampled.
const catchUpHistoryPageSize = 200

// errImportBacklogOverflow reports that the catch-up window holds more import
// history than one bounded page can prove complete. It is a "too many to
// announce" verdict, not a failure: the resumed batch is dropped whole, the
// same as a batch over restoredAlertCap.
var errImportBacklogOverflow = errors.New("import history window overflow")

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
	NotifyNewBook(title, foreignID, instanceID, format string)
	// The Upgraded variants page only admins opted into content_upgraded and
	// silently claim the matching broadcast key, which is what keeps the queue
	// poller from re-announcing a proven upgrade as new content.
	NotifyUpgradedMovie(title string, tmdbID int)
	NotifyUpgradedEpisode(seriesTitle string, tmdbID int)
	NotifyUpgradedBook(title, foreignID, instanceID, format string)
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

	// content pushes new-movie/new-episode/new-book notifications to opted-in
	// users when a download completes. nil when push is not configured.
	content ContentNotifier

	// opener receives stuck/blocked downloads for auto-dispatch. nil (the zero
	// value) disables the whole auto-dispatch path: the poll loop then skips the
	// detailed-queue diagnosis. Set only when the remediation feature is wired.
	opener IssueOpener

	// Previous polling state for detecting transitions
	prevRadarrQueue   map[string]map[int]float64  // instanceID -> movieId -> progress
	prevSonarrQueue   map[string]map[int]float64  // instanceID -> seriesId -> progress
	prevChaptarrQueue map[string]map[int]struct{} // instanceID -> set of book record ids

	// witness persists the three prev*Queue maps so a restart resumes the
	// departure diff instead of re-seeding from empty. nil disables persistence
	// and leaves the poller on its in-memory-only behavior.
	witness *queueWitness

	// restoredWitness marks the instances whose membership came from disk and
	// whose first diff has not run yet, holding the snapshot's observed_at so
	// the diff can be re-checked for staleness while it is deferred. An entry is
	// cleared as soon as that first diff is resolved.
	restoredWitness map[string]time.Time

	// lastPollAt records when each arr instance's queue was last successfully
	// polled and resolved. A gap of queueResumeAfter or more means completions
	// may have been grabbed and imported entirely unwatched — an arr or network
	// outage while the process stayed up — so the next poll runs the same
	// capped resumption as a restart. Written only from the poll goroutine.
	lastPollAt map[string]time.Time

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
// remediation.AutoDispatcher) to keep the whole auto-dispatch path off. database
// persists the poller's queue-departure memory across restarts; pass nil to keep
// that memory in-process only.
func NewHub(authService *auth.Service, registry *instance.Registry, store *instance.Store, database *sql.DB, content ContentNotifier, opener IssueOpener) *Hub {
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
		prevChaptarrQueue:  make(map[string]map[int]struct{}),
		prevArrQueueHash:   make(map[string]string),
		prevDownloadsHash:  make(map[string]string),
		downloadsErrLogged: make(map[string]bool),
		witness:            newQueueWitness(database),
		restoredWitness:    make(map[string]time.Time),
		lastPollAt:         make(map[string]time.Time),
	}
}

// contentReadiness is optionally implemented by ContentNotifier (*push.Notifier
// does). Checked by type assertion so existing implementations compile
// unchanged.
type contentReadiness interface{ ContentReady() bool }

// contentReady reports whether a restored departure diff can be announced yet.
// A hub with no content notifier has nothing to deliver, so it never holds.
func (h *Hub) contentReady() bool {
	if h.content == nil {
		return true
	}
	if r, ok := h.content.(contentReadiness); ok {
		return r.ContentReady()
	}
	return true
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
			// Deliver under the read lock, but only collect clients whose
			// send buffer is full — evicting them in place would mutate the
			// client map (and close a channel) while other readers may hold
			// the same read lock, which is exactly the concurrency RLock
			// promises to allow.
			var stalled []*Client
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
					stalled = append(stalled, client)
				}
			}
			h.mu.RUnlock()
			if len(stalled) > 0 {
				// Evict under the write lock. The presence check mirrors the
				// unregister case so a client can never be double-closed no
				// matter which path removes it first.
				h.mu.Lock()
				for _, client := range stalled {
					if _, ok := h.clients[client]; ok {
						delete(h.clients, client)
						close(client.send)
					}
				}
				h.mu.Unlock()
			}
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
		log.Printf("websocket: 401 from %s: no bearer subprotocol", r.RemoteAddr)
		http.Error(w, "missing auth", http.StatusUnauthorized)
		return
	}
	token := protocols[1]

	claims, _, err := h.authService.AuthenticateToken(token)
	if err != nil {
		// Reason and source only — never token material (see the subprotocol
		// echo note below for the same rule on the success path).
		log.Printf("websocket: 401 from %s: %v", r.RemoteAddr, err)
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	// Upgrade echoing the Bearer subprotocol so the client knows auth
	// succeeded. Echoing one of the offered subprotocols is required for the
	// web build: browsers fail a WebSocket connection outright when the
	// client offered subprotocols but the server selected none. Native
	// clients (dart:io) accept any echoed value they offered. "Bearer" is the
	// static member of the client's ["Bearer", <token>] offer — never echo
	// the token, which would copy a credential into a response header.
	// (http.Header.Set canonicalizes the key to the form gorilla reads.)
	header := http.Header{}
	header.Set("Sec-WebSocket-Protocol", "Bearer")
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
	// Restore the previous process's queue membership before the first tick, on
	// this goroutine: it owns the prev*Queue maps, so no lock is needed, and it
	// broadcasts nothing, so it cannot fill the broadcast channel before Run's
	// drain loop is live. The ordinary first tick then performs the resumed
	// diff — there is exactly one code path that witnesses completions.
	h.restoreQueueWitness()

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

// restoreQueueWitness seeds the prev*Queue maps from the last poll of the
// previous process, so the first tick diffs against real membership instead of
// re-seeding from empty. On a first boot the table is empty, nothing is
// restored, and the existing seed-only behavior is unchanged — which is why no
// upgrade can produce a burst of alerts.
func (h *Hub) restoreQueueWitness() {
	if h.witness == nil {
		return
	}
	rows, err := h.witness.load(time.Now(), queueWitnessStaleAfter)
	if err != nil {
		log.Printf("websocket: restore queue witness: %v", err)
		return
	}
	for instanceID, row := range rows {
		switch row.serviceType {
		case "radarr", "sonarr":
			// Only the keys are ever read; the progress values are re-derived
			// from the live queue on every poll.
			seeded := make(map[int]float64, len(row.ids))
			for _, id := range row.ids {
				seeded[id] = 0
			}
			if row.serviceType == "radarr" {
				h.prevRadarrQueue[instanceID] = seeded
			} else {
				h.prevSonarrQueue[instanceID] = seeded
			}
		case "chaptarr":
			seeded := make(map[int]struct{}, len(row.ids))
			for _, id := range row.ids {
				seeded[id] = struct{}{}
			}
			h.prevChaptarrQueue[instanceID] = seeded
		default:
			continue
		}
		h.restoredWitness[instanceID] = row.observedAt
		h.lastPollAt[instanceID] = row.observedAt
	}
	if len(h.restoredWitness) > 0 {
		log.Printf("websocket: resumed queue witness for %d instance(s)", len(h.restoredWitness))
	}
}

// resumeOrigin reports when this instance's queue was last watched, when that
// was long enough ago to make this tick a resumption, plus whether the
// knowledge came from a restored boot snapshot — the only case that may hold
// for gateway readiness, because only a fresh process has a gateway that has
// not had time to enroll. A zero time means steady state.
func (h *Hub) resumeOrigin(instanceID string) (since time.Time, boot bool) {
	if observedAt, restored := h.restoredWitness[instanceID]; restored {
		return observedAt, true
	}
	if last, ok := h.lastPollAt[instanceID]; ok && time.Since(last) >= queueResumeAfter {
		return last, false
	}
	return time.Time{}, false
}

// resolveAnnouncements decides what one successful poll may announce. In
// steady state it passes the departure diff through untouched — never capped,
// and at zero extra cost (importsSince is not called). On a resumption — a
// process restart or a poll gap of queueResumeAfter or more — it recovers what
// happened while nobody watched and applies every storm guard:
//
//   - Completions grabbed AND imported entirely inside the gap never appear in
//     any queue snapshot, so no membership diff can see them. importsSince
//     replays the arr's own import-history event log (the durable form of the
//     same event its webhook delivers, written only by the download-import
//     path — a library rescan or restore stamps no such records) and its ids
//     are merged into the departure diff. History may only ever ADD alerts:
//     a fetch failure degrades to the witnessed departures, never silences
//     them, and every merged id still passes the same live re-verification
//     (HasFile / BookFileCount) before anything is announced.
//   - History proof may REROUTE an alert, never suppress it: an import whose
//     every record is matched by a delete-for-upgrade goes to the returned
//     upgraded list (the admin content_upgraded audience) instead of the
//     broadcast. The proof read fails open — any error, drift, or ambiguity
//     leaves the id in the broadcast list, so vocabulary drift re-pages
//     upgrades as new content rather than silencing anything.
//   - A window older than queueWitnessStaleAfter describes completions the
//     user has long since found in the app: the whole batch is dropped.
//   - A merged batch over restoredAlertCap, or a window importsSince reports
//     as too big to enumerate (errImportBacklogOverflow), is dropped whole
//     rather than partially delivered.
//   - On a boot resumption only, a non-empty batch waits (hold=true: persist
//     nothing, announce nothing, retry next tick) until the push gateway
//     enrolls, so recovered alerts are not dropped into a nil client. It
//     cannot wedge: the staleness arm above drops the batch unconditionally
//     once the snapshot ages out. A hold requires push configured but
//     unenrolled, so it cannot happen on a push-disabled server. While one is
//     in effect this instance's membership stays frozen, which also defers its
//     request_status_changed broadcasts — accepted because the push those
//     alerts exist for is undeliverable during exactly that window, and
//     availability is recomputed live on every read.
func (h *Hub) resolveAnnouncements(instanceID string, departed []int, importsSince func(time.Time) (fresh, upgraded []int, err error)) (announce, upgraded []int, hold bool) {
	since, boot := h.resumeOrigin(instanceID)
	if since.IsZero() {
		return departed, nil, false // steady state is never gated
	}
	if time.Since(since) > queueWitnessStaleAfter {
		delete(h.restoredWitness, instanceID)
		if len(departed) > 0 {
			log.Printf("websocket: dropping %d stale resumed completion(s) for %s", len(departed), instanceID)
		}
		return nil, nil, false
	}
	catchup, upgrades, err := importsSince(since)
	if errors.Is(err, errImportBacklogOverflow) {
		delete(h.restoredWitness, instanceID)
		log.Printf("websocket: skipping resumed alerts for %s: over %d imports while unwatched", instanceID, catchUpHistoryPageSize)
		return nil, nil, false
	}
	if err != nil {
		log.Printf("websocket: import catch-up (%s): %v (announcing witnessed departures only)", instanceID, err)
		catchup, upgrades = nil, nil
	}
	// A departed id history proved to be an upgrade moves to the upgrade list:
	// both witnesses saw the same import, and the proof travels with it. A
	// departed id history never saw stays a broadcast — suppression requires
	// positive proof.
	merged := subtractInts(unionInts(departed, catchup), upgrades)
	if len(merged)+len(upgrades) > 0 && boot && !h.contentReady() {
		return nil, nil, true
	}
	delete(h.restoredWitness, instanceID)
	// Independent caps, each dropped whole: upgrades are filtered out before
	// the broadcast cap is judged, so a mass upgrade sweep during the gap
	// cannot evict a genuine new-content alert — and an over-cap upgrade batch
	// is dropped without touching the broadcasts.
	if len(merged) > restoredAlertCap {
		log.Printf("websocket: skipping %d resumed completion alert(s) for %s (over cap)", len(merged), instanceID)
		merged = nil
	}
	if len(upgrades) > restoredAlertCap {
		log.Printf("websocket: skipping %d resumed upgrade alert(s) for %s (over cap)", len(upgrades), instanceID)
		upgrades = nil
	}
	return merged, upgrades, false
}

// historyDataValue reads a history record's data value by case-insensitive
// key. The lineage's API camelCases dictionary keys ("Reason" → "reason"),
// but a fork on a different serializer may not, and a missed key here would
// only re-page an upgrade as new content — tolerate the drift instead.
func historyDataValue(data map[string]string, key string) string {
	if v, ok := data[key]; ok {
		return v
	}
	for k, v := range data {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

// subtractInts returns the members of a not present in b, preserving a's order.
func subtractInts(a, b []int) []int {
	if len(b) == 0 {
		return a
	}
	drop := make(map[int]struct{}, len(b))
	for _, id := range b {
		drop[id] = struct{}{}
	}
	var out []int
	for _, id := range a {
		if _, gone := drop[id]; gone {
			continue
		}
		out = append(out, id)
	}
	return out
}

// unionInts merges two id sets into one sorted, duplicate-free slice.
func unionInts(a, b []int) []int {
	seen := make(map[int]struct{}, len(a)+len(b))
	var out []int
	for _, ids := range [][]int{a, b} {
		for _, id := range ids {
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	sort.Ints(out)
	return out
}

// radarrImportsSince lists the distinct movie ids with a completed-import
// history record dated after since, split into fresh imports and proven
// upgrades. The event name is re-checked against the response (the query asks
// by enum id) so a renumbered enum in a fork yields an empty catch-up rather
// than presenting deletions as imports — history may only ever add alerts, so
// an unrecognized vocabulary degrades to the queue witness instead of
// misfiring.
//
// A movie is a proven upgrade only when every one of its window imports is
// matched by a delete-for-upgrade record (count-aware: two imports against one
// upgrade delete means at least one filled a gap, so the movie broadcasts).
// The proof read itself fails open — see radarrUpgradeDeletesSince.
func radarrImportsSince(client *radarr.Client, since time.Time) (fresh, upgraded []int, err error) {
	records, complete, err := client.GetImportHistorySince(since, catchUpHistoryPageSize)
	if err != nil {
		return nil, nil, err
	}
	if !complete {
		return nil, nil, errImportBacklogOverflow
	}
	imports := make(map[int]int, len(records))
	var ids []int
	for _, rec := range records {
		if !strings.EqualFold(rec.EventType, "downloadFolderImported") || rec.MovieID <= 0 {
			continue
		}
		if imports[rec.MovieID] == 0 {
			ids = append(ids, rec.MovieID)
		}
		imports[rec.MovieID]++
	}
	sort.Ints(ids)
	if len(ids) == 0 {
		return nil, nil, nil
	}
	deletes := radarrUpgradeDeletesSince(client, since)
	for _, id := range ids {
		if deletes[id] >= imports[id] {
			upgraded = append(upgraded, id)
		} else {
			fresh = append(fresh, id)
		}
	}
	return fresh, upgraded, nil
}

// radarrUpgradeDeletesSince counts the window's delete-for-upgrade records per
// movie. Any failure — fetch error, a window over one page, a drifted event
// name or missing reason — returns no proof, so every import announces as new
// content. History proof can only reroute an alert to admins by positively
// matching both the event name and the Upgrade reason; drift degrades to
// alerting, never to silence.
func radarrUpgradeDeletesSince(client *radarr.Client, since time.Time) map[int]int {
	records, complete, err := client.GetUpgradeDeleteHistorySince(since, catchUpHistoryPageSize)
	if err != nil {
		log.Printf("websocket: radarr upgrade-delete catch-up: %v (announcing all imports as new)", err)
		return nil
	}
	if !complete {
		log.Printf("websocket: radarr upgrade-delete catch-up: window over one page (announcing all imports as new)")
		return nil
	}
	counts := make(map[int]int, len(records))
	for _, rec := range records {
		if !strings.EqualFold(rec.EventType, "movieFileDeleted") || rec.MovieID <= 0 {
			continue
		}
		if !strings.EqualFold(historyDataValue(rec.Data, "reason"), "Upgrade") {
			continue
		}
		counts[rec.MovieID]++
	}
	return counts
}

// sonarrImportsSince is the Sonarr analogue of radarrImportsSince, collapsing
// per-episode-file import records to their distinct series ids so a season
// pack imported during the gap becomes one alert, not twenty.
//
// Upgrade proof pairs per EPISODE, not per series: a series moves to the
// upgraded list only when every import record it produced in the window is
// matched by a delete-for-upgrade of the same episode. One unproven import —
// including one whose episode id the record doesn't carry — keeps the whole
// series a broadcast, because "one new episode plus three upgrades" is news.
func sonarrImportsSince(client *sonarr.Client, since time.Time) (fresh, upgraded []int, err error) {
	records, complete, err := client.GetImportHistorySince(since, catchUpHistoryPageSize)
	if err != nil {
		return nil, nil, err
	}
	if !complete {
		return nil, nil, errImportBacklogOverflow
	}
	// series id → episode id → import-record count. Episode id 0 buckets the
	// records that carry none; they can never be proven upgrades.
	imports := make(map[int]map[int]int, len(records))
	var ids []int
	for _, rec := range records {
		if !strings.EqualFold(rec.EventType, "downloadFolderImported") {
			continue
		}
		seriesID := rec.SeriesID
		if seriesID <= 0 && rec.Series != nil {
			seriesID = rec.Series.ID
		}
		if seriesID <= 0 && rec.Episode != nil {
			seriesID = rec.Episode.SeriesID
		}
		if seriesID <= 0 {
			continue
		}
		episodeID := rec.EpisodeID
		if episodeID <= 0 && rec.Episode != nil {
			episodeID = rec.Episode.ID
		}
		if episodeID < 0 {
			episodeID = 0
		}
		if imports[seriesID] == nil {
			imports[seriesID] = make(map[int]int)
			ids = append(ids, seriesID)
		}
		imports[seriesID][episodeID]++
	}
	sort.Ints(ids)
	if len(ids) == 0 {
		return nil, nil, nil
	}
	deletes := sonarrUpgradeDeletesSince(client, since)
	for _, id := range ids {
		allUpgrades := true
		for episodeID, n := range imports[id] {
			if episodeID <= 0 || deletes[episodeID] < n {
				allUpgrades = false
				break
			}
		}
		if allUpgrades {
			upgraded = append(upgraded, id)
		} else {
			fresh = append(fresh, id)
		}
	}
	return fresh, upgraded, nil
}

// sonarrUpgradeDeletesSince counts the window's delete-for-upgrade records per
// episode, failing open exactly like radarrUpgradeDeletesSince.
func sonarrUpgradeDeletesSince(client *sonarr.Client, since time.Time) map[int]int {
	records, complete, err := client.GetUpgradeDeleteHistorySince(since, catchUpHistoryPageSize)
	if err != nil {
		log.Printf("websocket: sonarr upgrade-delete catch-up: %v (announcing all imports as new)", err)
		return nil
	}
	if !complete {
		log.Printf("websocket: sonarr upgrade-delete catch-up: window over one page (announcing all imports as new)")
		return nil
	}
	counts := make(map[int]int, len(records))
	for _, rec := range records {
		if !strings.EqualFold(rec.EventType, "episodeFileDeleted") {
			continue
		}
		episodeID := rec.EpisodeID
		if episodeID <= 0 && rec.Episode != nil {
			episodeID = rec.Episode.ID
		}
		if episodeID <= 0 {
			continue
		}
		if !strings.EqualFold(historyDataValue(rec.Data, "reason"), "Upgrade") {
			continue
		}
		counts[episodeID]++
	}
	return counts
}

// chaptarrImportsSince is the Chaptarr analogue of radarrImportsSince. The
// event name is re-checked against the shared Readarr-lineage import
// vocabulary (the same set the webhook receiver accepts), so both witnesses
// agree on what counts as an import and an unrecognized fork vocabulary
// contributes nothing rather than misfiring. Upgrade proof pairs per book
// record, count-aware like the Radarr split.
func chaptarrImportsSince(client *chaptarr.Client, since time.Time) (fresh, upgraded []int, err error) {
	records, complete, err := client.GetImportHistorySince(since, catchUpHistoryPageSize)
	if err != nil {
		return nil, nil, err
	}
	if !complete {
		return nil, nil, errImportBacklogOverflow
	}
	imports := make(map[int]int, len(records))
	var ids []int
	for _, rec := range records {
		if !chaptarr.IsImportEventType(rec.EventType) || rec.BookID <= 0 {
			continue
		}
		if imports[rec.BookID] == 0 {
			ids = append(ids, rec.BookID)
		}
		imports[rec.BookID]++
	}
	sort.Ints(ids)
	if len(ids) == 0 {
		return nil, nil, nil
	}
	deletes := chaptarrUpgradeDeletesSince(client, since)
	for _, id := range ids {
		if deletes[id] >= imports[id] {
			upgraded = append(upgraded, id)
		} else {
			fresh = append(fresh, id)
		}
	}
	return fresh, upgraded, nil
}

// chaptarrUpgradeDeletesSince counts the window's delete-for-upgrade records
// per book, failing open exactly like radarrUpgradeDeletesSince.
func chaptarrUpgradeDeletesSince(client *chaptarr.Client, since time.Time) map[int]int {
	records, complete, err := client.GetUpgradeDeleteHistorySince(since, catchUpHistoryPageSize)
	if err != nil {
		log.Printf("websocket: chaptarr upgrade-delete catch-up: %v (announcing all imports as new)", err)
		return nil
	}
	if !complete {
		log.Printf("websocket: chaptarr upgrade-delete catch-up: window over one page (announcing all imports as new)")
		return nil
	}
	counts := make(map[int]int, len(records))
	for _, rec := range records {
		if !strings.EqualFold(rec.EventType, "bookFileDeleted") || rec.BookID <= 0 {
			continue
		}
		if !strings.EqualFold(historyDataValue(rec.Data, "reason"), "Upgrade") {
			continue
		}
		counts[rec.BookID]++
	}
	return counts
}

// saveQueueWitness records this instance's current queue membership. A failure
// is logged and swallowed: degrading to in-memory-only behavior is right, but
// suppressing the alerts this poll already found is not.
func (h *Hub) saveQueueWitness(instanceID, serviceType string, ids []int) {
	if h.witness == nil {
		return
	}
	if err := h.witness.save(instanceID, serviceType, ids, time.Now()); err != nil {
		log.Printf("websocket: persist queue witness (%s): %v", instanceID, err)
	}
}

// progressKeys returns the sorted arr record ids of a radarr/sonarr queue map.
func progressKeys(queue map[int]float64) []int {
	ids := make([]int, 0, len(queue))
	for id := range queue {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// setKeys returns the sorted arr record ids of a chaptarr queue set.
func setKeys(queue map[int]struct{}) []int {
	ids := make([]int, 0, len(queue))
	for id := range queue {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
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
		DownloadID:       item.DownloadID,
		AddedAt:          item.Added,
		FileIDAtSnapshot: item.FileIDAtSnapshot(),
		Media:            media,
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
		DownloadID:       item.DownloadID,
		AddedAt:          item.Added,
		FileIDAtSnapshot: item.FileIDAtSnapshot(),
		Media:            media,
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

// chaptarrQueueSignal projects a Chaptarr detailed queue item into the neutral
// classifier signal plus its stable download id and durable book identity. It
// mirrors remediation's chaptarrObservation (kept local so the hub need not
// import that package). Chaptarr queue payloads never embed the book's current
// file id, so FileIDAtSnapshot stays nil (unknown).
func chaptarrQueueSignal(item chaptarr.DetailedQueueItem) arr.QueueObservation {
	messages := make([]arr.StatusMessage, 0, len(item.StatusMessages))
	for _, m := range item.StatusMessages {
		messages = append(messages, arr.StatusMessage{Title: m.Title, Messages: m.Messages})
	}
	media := arr.QueueMediaContext{QueueID: item.ID, Title: item.Title, AuthorID: item.AuthorID, BookID: item.BookID}
	if item.Book != nil {
		if item.Book.Title != "" {
			media.Title = item.Book.Title
		}
		if media.BookID == 0 {
			media.BookID = item.Book.ID
		}
	}
	if item.Author != nil && media.AuthorID == 0 {
		media.AuthorID = item.Author.ID
	}
	return arr.QueueObservation{
		DownloadID: item.DownloadID,
		AddedAt:    item.Added,
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

// autoDispatchChaptarr is the Chaptarr analogue of autoDispatchRadarr: books
// enter remediation observation through the same complete-snapshot channel.
func (h *Hub) autoDispatchChaptarr(instanceID string, client *chaptarr.Client) {
	if !h.autoDispatchEnabled() {
		return
	}
	items, err := client.GetQueueDetailed()
	if err != nil {
		log.Printf("websocket: auto-dispatch chaptarr detailed queue (%s): %v", instanceID, err)
		return
	}
	observations := make([]arr.QueueObservation, 0, len(items))
	for _, item := range items {
		observations = append(observations, chaptarrQueueSignal(item))
	}
	h.dispatchDetailedItems("chaptarr", instanceID, observations)
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
		tuples = append(tuples, fmt.Sprintf("%d|%s|%.0f", item.MovieID, item.Status, item.Sizeleft))
		if item.MovieID <= 0 {
			// A queue row Radarr could not match to a library movie has no id
			// to witness or announce; it still counts toward the composition
			// tuple above so its arrival/departure invalidates queue views.
			continue
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
	var departed []int
	if prevQueue := h.prevRadarrQueue[instanceID]; prevQueue != nil {
		for movieID := range prevQueue {
			if _, stillInQueue := currentQueue[movieID]; stillInQueue || movieID <= 0 {
				continue
			}
			departed = append(departed, movieID)
		}
		sort.Ints(departed)
	}

	announce, upgraded, hold := h.resolveAnnouncements(instanceID, departed, func(since time.Time) (fresh, upgrades []int, err error) {
		return radarrImportsSince(client, since)
	})
	if hold {
		h.noteArrQueueComposition(instanceID, "radarr", tuples)
		h.autoDispatchRadarr(instanceID, client)
		return
	}

	// Persist before announcing — see pollChaptarrInstance.
	h.prevRadarrQueue[instanceID] = currentQueue
	h.saveQueueWitness(instanceID, "radarr", progressKeys(currentQueue))
	h.lastPollAt[instanceID] = time.Now()

	// Upgrades pass the same live re-verification as broadcasts; only the
	// audience differs (admins opted into content_upgraded).
	announceMovie := func(movieID int, upgrade bool) {
		movie, err := client.GetMovie(movieID)
		if err != nil {
			log.Printf("websocket: get completed radarr movie %d: %v", movieID, err)
			return
		}
		if !movie.HasFile {
			return
		}
		h.Broadcast(Event{
			Type: "request_status_changed",
			Data: map[string]interface{}{
				"tmdb_id":     movie.TmdbID,
				"media_type":  "movie",
				"status":      "available",
				"instance_id": instanceID,
			},
		})
		if h.content == nil {
			return
		}
		if upgrade {
			h.content.NotifyUpgradedMovie(movie.Title, movie.TmdbID)
		} else {
			h.content.NotifyNewMovie(movie.Title, movie.TmdbID)
		}
	}
	for _, movieID := range announce {
		announceMovie(movieID, false)
	}
	for _, movieID := range upgraded {
		announceMovie(movieID, true)
	}

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
		tuples = append(tuples, fmt.Sprintf("%d|%s|%.0f", item.SeriesID, item.Status, item.Sizeleft))
		if item.SeriesID <= 0 {
			// A queue row Sonarr could not match to a library series has no id
			// to witness or announce; it still counts toward the composition
			// tuple above so its arrival/departure invalidates queue views.
			continue
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
	var departed []int
	if prevQueue := h.prevSonarrQueue[instanceID]; prevQueue != nil {
		for seriesID := range prevQueue {
			if _, stillInQueue := currentQueue[seriesID]; stillInQueue || seriesID <= 0 {
				continue
			}
			departed = append(departed, seriesID)
		}
		sort.Ints(departed)
	}

	announce, upgraded, hold := h.resolveAnnouncements(instanceID, departed, func(since time.Time) (fresh, upgrades []int, err error) {
		return sonarrImportsSince(client, since)
	})
	if hold {
		h.noteArrQueueComposition(instanceID, "sonarr", tuples)
		h.autoDispatchSonarr(instanceID, client)
		return
	}

	// Persist before announcing — see pollChaptarrInstance.
	h.prevSonarrQueue[instanceID] = currentQueue
	h.saveQueueWitness(instanceID, "sonarr", progressKeys(currentQueue))
	h.lastPollAt[instanceID] = time.Now()

	// Upgrades pass the same live availability recomputation as broadcasts;
	// only the audience differs (admins opted into content_upgraded).
	announceSeries := func(seriesID int, upgrade bool) {
		series, err := client.GetSeries(seriesID)
		if err != nil {
			log.Printf("websocket: get completed sonarr series %d: %v", seriesID, err)
			return
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
		if h.content == nil {
			return
		}
		if upgrade {
			h.content.NotifyUpgradedEpisode(series.Title, series.TmdbID)
		} else {
			h.content.NotifyNewEpisode(series.Title, series.TmdbID)
		}
	}
	for _, seriesID := range announce {
		announceSeries(seriesID, false)
	}
	for _, seriesID := range upgraded {
		announceSeries(seriesID, true)
	}

	h.noteArrQueueComposition(instanceID, "sonarr", tuples)

	// Auto-dispatch pass (see pollRadarrInstance). No-op when the opener is nil.
	h.autoDispatchSonarr(instanceID, client)
}

// pollChaptarrInstance emits an arr_queue_changed invalidation ping whenever
// the instance's download queue composition changes, and witnesses queue
// departures to push new_book alerts — the same completion witness the Radarr
// poller uses. It is the fallback witness for books: instances where the admin
// never configured instant updates, and imports of files already on disk, which
// the Readarr lineage does not announce over its webhook.
// Unlike the Radarr/Sonarr pollers it does not emit per-item download_progress
// events: Chaptarr books carry no TMDB id, which those events key on. It ends
// with the same auto-dispatch observation pass as Radarr/Sonarr, feeding book
// queue snapshots to remediation.
func (h *Hub) pollChaptarrInstance(instanceID string, client *chaptarr.Client) {
	queue, err := client.GetQueue()
	if err != nil {
		log.Printf("websocket: poll chaptarr queue (%s): %v", instanceID, err)
		return
	}

	currentQueue := make(map[int]struct{}, len(queue))
	tuples := make([]string, 0, len(queue))
	for _, item := range queue {
		tuples = append(tuples, fmt.Sprintf("%d|%s|%.0f", item.BookID, item.Status, item.Sizeleft))
		if item.BookID <= 0 {
			// A queue row Chaptarr could not match to a library book has no id
			// to witness or announce; it still counts toward the composition
			// tuple above so its arrival/departure invalidates queue views.
			continue
		}
		currentQueue[item.BookID] = struct{}{}
	}

	// A book record that left the queue and now has a file finished importing.
	// Ebook and audiobook are separate records sharing a foreignBookId, so each
	// format alerts on its own import; a record that departed without a file
	// failed or was removed — say nothing.
	var departed []int
	if prevQueue := h.prevChaptarrQueue[instanceID]; prevQueue != nil {
		for bookID := range prevQueue {
			if _, stillInQueue := currentQueue[bookID]; stillInQueue || bookID <= 0 {
				continue
			}
			departed = append(departed, bookID)
		}
		sort.Ints(departed)
	}

	announce, upgraded, hold := h.resolveAnnouncements(instanceID, departed, func(since time.Time) (fresh, upgrades []int, err error) {
		return chaptarrImportsSince(client, since)
	})
	if hold {
		// Keep the restored membership and retry next tick, so these
		// completions are not lost to an unenrolled gateway.
		h.noteArrQueueComposition(instanceID, "chaptarr", tuples)
		h.autoDispatchChaptarr(instanceID, client)
		return
	}

	// Persist before announcing: a crash between the two costs this batch of
	// alerts, whereas announcing first would re-announce them on every restart
	// of a crashlooping container. The save is deliberately outside the
	// h.content check — enabling push later must not start from amnesia.
	h.prevChaptarrQueue[instanceID] = currentQueue
	h.saveQueueWitness(instanceID, "chaptarr", setKeys(currentQueue))
	h.lastPollAt[instanceID] = time.Now()

	if h.content != nil {
		// Upgrades pass the same live re-verification as broadcasts; only the
		// audience differs (admins opted into content_upgraded).
		announceBook := func(bookID int, upgrade bool) {
			book, err := client.GetBook(bookID)
			if err != nil {
				log.Printf("websocket: get completed chaptarr book %d: %v", bookID, err)
				return
			}
			// Chaptarr answers 404 with (nil, nil) for a record deleted while it
			// was downloading. Dereferencing that would panic the poll goroutine
			// and take the process down.
			if book == nil {
				return
			}
			if book.Statistics.BookFileCount == 0 {
				return
			}
			if upgrade {
				h.content.NotifyUpgradedBook(book.Title, book.ForeignBookID, instanceID, book.MediaType)
			} else {
				h.content.NotifyNewBook(book.Title, book.ForeignBookID, instanceID, book.MediaType)
			}
		}
		for _, bookID := range announce {
			announceBook(bookID, false)
		}
		for _, bookID := range upgraded {
			announceBook(bookID, true)
		}
	}

	h.noteArrQueueComposition(instanceID, "chaptarr", tuples)

	// Auto-dispatch pass (see pollRadarrInstance). No-op when the opener is nil.
	h.autoDispatchChaptarr(instanceID, client)
}
