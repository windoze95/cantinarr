// Package webhooks receives Radarr/Sonarr/Chaptarr "Connect → Webhook"
// callbacks so library changes made outside Cantinarr (manual imports, deletes,
// adds) are pushed instantly instead of caught on the next poll or user-driven
// refresh. Each callback authenticates with the instance's server-only Basic
// credential. These requests carry no user session and translate into the same
// websocket events and push notifications the queue-poll witness already emits,
// so the app needs no new event handling.
//
// It matters most for books: a small ebook can be grabbed and imported inside a
// single 30-second poll interval, so without this callback its alert is never
// sent at all rather than merely delayed.
package webhooks

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
	ws "github.com/windoze95/cantinarr-server/internal/websocket"
)

// Broadcaster fans an event out to connected websocket clients. *ws.Hub
// satisfies it; declared here so tests can record broadcasts.
type Broadcaster interface {
	Broadcast(event ws.Event)
}

// AvailabilityInvalidator drops cached availability digests for an instance —
// movie/series for Radarr/Sonarr, book for Chaptarr. *request.Service satisfies
// it.
type AvailabilityInvalidator interface {
	InvalidateAvailabilityDigests(instanceID string)
	InvalidateBookDigests(instanceID string)
}

// PreAirImportWitness is told when an import lands on an episode that has not
// aired yet. *remediation.Service satisfies it.
//
// The witness, not this handler, decides whether that is a problem: one early
// file is an everyday air-date slip, and the threshold that separates a slip
// from a season of content that does not exist lives with the detector.
type PreAirImportWitness interface {
	RecordPreAirImport(instanceID string, tvdbID, tmdbID, seasonNumber int, title string)
	// RecordSuspectImport is told about every completed Sonarr import so the
	// truncated-file sentinel can judge it against the arr's own analysis.
	// Advisory-only in this wave: the witness opens admin notices, never runs.
	RecordSuspectImport(instanceID string, tvdbID, tmdbID, seasonNumber, episodeNumber int, title string)
}

// Handler terminates the arr webhook callbacks.
type Handler struct {
	store       *instance.Store
	registry    *instance.Registry
	hub         Broadcaster
	requests    AvailabilityInvalidator
	content     ws.ContentNotifier
	preAir      PreAirImportWitness
	parkResumer BookParkResumer
}

// NewHandler builds the webhook handler. content may be nil (push disabled).
func NewHandler(store *instance.Store, registry *instance.Registry, hub Broadcaster, requests AvailabilityInvalidator, content ws.ContentNotifier) *Handler {
	return &Handler{store: store, registry: registry, hub: hub, requests: requests, content: content}
}

// SetPreAirImportWitness wires the pre-air detector after construction, matching
// how the other optional dependencies are attached in main.
func (h *Handler) SetPreAirImportWitness(w PreAirImportWitness) { h.preAir = w }

// BookParkResumer resumes the server-owned author-import parks out of cadence;
// *request.Service satisfies it. Chaptarr's AuthorAdded callback fires at the
// exact moment a queued author import lands — the one event every park is
// waiting for — so the sweep runs now instead of at the next five-minute tick.
type BookParkResumer interface {
	ResumeBookParks()
}

// SetBookParkResumer wires the park resumer after construction; nil leaves the
// parks on the maintenance cadence alone.
func (h *Handler) SetBookParkResumer(r BookParkResumer) { h.parkResumer = r }

// arrPayload is the superset of the Sonarr and Radarr webhook fields this
// handler acts on. Both apps send eventType plus a movie or series object;
// everything else is ignored.
type arrPayload struct {
	EventType string `json:"eventType"`
	// IsUpgrade marks a Download event whose import replaced an existing file.
	// It gates WHO is paged, never whether state refreshes: a proven upgrade
	// goes to the admin content_upgraded category instead of the household
	// broadcast. Absent decodes false, so an arr that doesn't send it (or a
	// drifted payload) broadcasts as new content — suppression requires
	// positive proof, never the other way around.
	IsUpgrade bool `json:"isUpgrade"`
	Movie     *struct {
		ID     int    `json:"id"`
		Title  string `json:"title"`
		TmdbID int    `json:"tmdbId"`
	} `json:"movie"`
	Series *struct {
		ID     int    `json:"id"`
		Title  string `json:"title"`
		TvdbID int    `json:"tvdbId"`
		TmdbID int    `json:"tmdbId"`
	} `json:"series"`
	// Episodes is what Sonarr already sends on every Download and Grab, and
	// what Cantinarr threw away until the pre-air detector needed it: an
	// episode's own air date is the only thing that can say a file claiming to
	// be it cannot possibly be it.
	Episodes []struct {
		ID            int        `json:"id"`
		EpisodeNumber int        `json:"episodeNumber"`
		SeasonNumber  int        `json:"seasonNumber"`
		AirDateUtc    *time.Time `json:"airDateUtc"`
	} `json:"episodes"`
	// Chaptarr sends a singular book on import and a plural list on grab. Only
	// the record id is read; identity comes from a live lookup.
	Book *struct {
		ID int `json:"id"`
	} `json:"book"`
	Books []struct {
		ID int `json:"id"`
	} `json:"books"`
}

// checkPreAirImport hands an import that landed on an unaired episode to the
// witness, and does nothing at all otherwise.
//
// The test is deliberately free: it reads air dates the payload already carries
// and touches no network, so the overwhelmingly normal case — a file arriving
// for an episode that has aired — costs one comparison per episode. Only the
// impossible case pays for a library read, and it pays for it in the witness.
func (h *Handler) checkPreAirImport(instanceID string, payload arrPayload) {
	if h.preAir == nil || payload.Series == nil {
		return
	}
	// The floor mirrors the season verdict's (arr.PreAirMarginFloor): an
	// episode airing within it is a binge premiere on TheTVDB's runtime-
	// staggered calendar, not a pre-air import, and must not even wake the
	// witness for a season probe.
	horizon := time.Now().UTC().Add(arr.PreAirMarginFloor)
	for _, ep := range payload.Episodes {
		if ep.AirDateUtc == nil || !ep.AirDateUtc.After(horizon) {
			continue
		}
		h.preAir.RecordPreAirImport(instanceID, payload.Series.TvdbID, payload.Series.TmdbID,
			ep.SeasonNumber, payload.Series.Title)
		// One report per season is enough: the witness reads the whole season
		// anyway, and a pack that imports as ten episodes must not open ten
		// investigations of one problem.
		return
	}
}

// checkSuspectImport hands every completed import to the truncated-file
// sentinel. Unlike the pre-air gate there is no free payload test — the
// evidence (the arr's ffprobe runtime for the imported file) needs a library
// read — so the witness owns the whole judgment and its cost.
func (h *Handler) checkSuspectImport(instanceID string, payload arrPayload) {
	if h.preAir == nil || payload.Series == nil {
		return
	}
	for _, ep := range payload.Episodes {
		h.preAir.RecordSuspectImport(instanceID, payload.Series.TvdbID, payload.Series.TmdbID,
			ep.SeasonNumber, ep.EpisodeNumber, payload.Series.Title)
	}
}

// bookIDs returns the usable Chaptarr record ids in the payload, deduplicated
// and ordered so repeated fields cannot produce repeated lookups.
func (p arrPayload) bookIDs() []int {
	seen := make(map[int]struct{})
	var ids []int
	add := func(id int) {
		if id <= 0 {
			return
		}
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if p.Book != nil {
		add(p.Book.ID)
	}
	for _, b := range p.Books {
		add(b.ID)
	}
	sort.Ints(ids)
	return ids
}

// HandleArr receives the server-managed Sonarr/Radarr Connect webhook. The
// server-only credential is carried as the Basic Auth password, never in a URL
// that an HTTP access logger could persist.
func (h *Handler) HandleArr(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "instanceID")
	inst, err := h.store.Get(instanceID)
	if err != nil || inst == nil || !instance.SupportsManagedWebhook(inst.ServiceType) {
		http.Error(w, `{"error":"unknown instance"}`, http.StatusNotFound)
		return
	}
	tokens, err := h.store.WebhookTokens(instanceID)
	if err != nil || !tokenMatches(r, tokens) {
		http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
		return
	}

	var payload arrPayload
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(&payload); err != nil {
		http.Error(w, `{"error":"invalid payload"}`, http.StatusBadRequest)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		http.Error(w, `{"error":"invalid payload"}`, http.StatusBadRequest)
		return
	}

	if inst.ServiceType == "chaptarr" {
		h.handleBookEvent(instanceID, payload)
	} else {
		h.handleVideoEvent(instanceID, inst.ServiceType, payload)
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// handleVideoEvent applies a Radarr/Sonarr callback. Movies and series carry a
// TMDB id, so these events drive request_status_changed directly.
func (h *Handler) handleVideoEvent(instanceID, serviceType string, payload arrPayload) {
	switch payload.EventType {
	case "Test":
		// Sonarr/Radarr's "Test" button — succeed without side effects.

	case "Grab":
		// A release was sent to the download client: the queue just changed
		// shape, ahead of the hub's next poll.
		h.hub.Broadcast(ws.Event{
			Type: "arr_queue_changed",
			Data: map[string]interface{}{
				"instance_id":  instanceID,
				"service_type": serviceType,
			},
		})

	case "Download": // import completed (including manual imports)
		h.requests.InvalidateAvailabilityDigests(instanceID)
		if payload.Movie != nil {
			h.movieImported(instanceID, payload.Movie.ID, payload.Movie.Title, payload.Movie.TmdbID, payload.IsUpgrade)
		}
		if payload.Series != nil {
			h.seriesChanged(instanceID, payload.Series.ID, payload.Series.Title, payload.Series.TmdbID, true, payload.IsUpgrade)
			h.checkPreAirImport(instanceID, payload)
			h.checkSuspectImport(instanceID, payload)
		}

	case "MovieAdded", "SeriesAdd":
		h.requests.InvalidateAvailabilityDigests(instanceID)
		h.broadcastStatus(instanceID, payload, "requested")

	case "MovieDelete", "SeriesDelete":
		h.requests.InvalidateAvailabilityDigests(instanceID)
		h.broadcastStatus(instanceID, payload, "unavailable")

	case "MovieFileDelete":
		h.requests.InvalidateAvailabilityDigests(instanceID)
		if payload.Movie != nil {
			h.movieFileDeleted(instanceID, payload.Movie.ID, payload.Movie.TmdbID)
		}

	case "EpisodeFileDelete":
		h.requests.InvalidateAvailabilityDigests(instanceID)
		if payload.Series != nil {
			h.seriesChanged(instanceID, payload.Series.ID, payload.Series.Title, payload.Series.TmdbID, false, false)
		}

	default:
		// Health, Rename, ApplicationUpdate, … — acknowledged, no action.
	}
}

// Import events are recognized by chaptarr.IsImportEventType — one vocabulary
// shared with the queue poller's import-history catch-up, so the two witnesses
// can never disagree about what counts as an import. An unrecognized name is
// simply acknowledged and the queue poller witnesses the import instead.
//
// bookLibraryEvents are the normalized non-import Chaptarr event names that
// still change what the library shows (grabs, adds, deletes, renames): they
// invalidate caches and ping clients, and must never push a "ready" alert.
var bookLibraryEvents = map[string]struct{}{
	"grab": {}, "bookadd": {}, "bookadded": {}, "authoradd": {}, "authoradded": {},
	"bookdelete": {}, "authordelete": {}, "bookfiledelete": {}, "rename": {},
	"retag": {}, "bookretag": {},
}

// handleBookEvent applies a Chaptarr callback.
//
// Books have no TMDB id, so this path never emits request_status_changed (0
// would collide across every book); arr_queue_changed is the invalidation ping
// the app already consumes. The payload is treated purely as a trigger: only the
// event name and record id are read, and the alert itself is built from a live
// read of the record, so a drifted or forged body cannot fabricate an alert.
func (h *Handler) handleBookEvent(instanceID string, payload arrPayload) {
	event := chaptarr.NormalizeEventType(payload.EventType)
	if event == "test" {
		return
	}
	isImport := chaptarr.IsImportEventType(payload.EventType)
	_, isLibraryChange := bookLibraryEvents[event]
	if !isImport && !isLibraryChange {
		return
	}

	// Invalidate before announcing so a user who taps the alert lands on fresh
	// availability rather than a cached "Requested".
	h.requests.InvalidateBookDigests(instanceID)
	h.hub.Broadcast(ws.Event{
		Type: "arr_queue_changed",
		Data: map[string]interface{}{
			"instance_id":  instanceID,
			"service_type": "chaptarr",
		},
	})
	if (event == "authoradd" || event == "authoradded") && h.parkResumer != nil {
		// The arr just finished importing an author — the exit every
		// author-import park waits on. Resume the sweep now; the maintenance
		// cadence stays the fallback for missed callbacks.
		h.parkResumer.ResumeBookParks()
	}
	if !isImport {
		return
	}

	ids := payload.bookIDs()
	if len(ids) == 0 {
		log.Printf("webhooks: chaptarr %q import carried no book id; leaving it to the queue poller", payload.EventType)
		return
	}
	for _, id := range ids {
		h.bookImported(instanceID, id, payload.IsUpgrade)
	}
}

// bookImported announces a completed book import after confirming it against
// the live record, applying the same guards as the queue-departure witness so
// the two witnesses produce an identical alert and dedupe against each other.
// isUpgrade reroutes the alert to the admin content_upgraded category.
func (h *Handler) bookImported(instanceID string, bookID int, isUpgrade bool) {
	if h.content == nil || h.registry == nil {
		return
	}
	client, err := h.registry.GetChaptarrClient(instanceID)
	if err != nil {
		log.Printf("webhooks: chaptarr client for imported book %d: %v", bookID, err)
		return
	}
	book, err := client.GetBook(bookID)
	if err != nil {
		log.Printf("webhooks: get imported chaptarr book %d: %v", bookID, err)
		return
	}
	// Chaptarr answers 404 with (nil, nil) for a record deleted between the
	// import and this read.
	if book == nil {
		return
	}
	// No file means the import ghosted or the file was already removed; the
	// queue witness stays silent in the same case.
	if book.Statistics.BookFileCount == 0 {
		return
	}
	// Raw MediaType, exactly as the poller passes it: any normalization here
	// would change the dedupe key and produce two pushes for one import.
	if isUpgrade {
		h.content.NotifyUpgradedBook(book.Title, book.ForeignBookID, instanceID, book.MediaType)
	} else {
		h.content.NotifyNewBook(book.Title, book.ForeignBookID, instanceID, book.MediaType)
	}
}

// tokenMatches checks the Basic Auth password against every credential valid
// during a managed rotation. The query-string form is intentionally rejected:
// standard HTTP request logs persist URLs.
func tokenMatches(r *http.Request, accepted []string) bool {
	_, got, ok := r.BasicAuth()
	if !ok || got == "" || len(accepted) == 0 {
		return false
	}
	matched := 0
	for _, want := range accepted {
		matched |= subtle.ConstantTimeCompare([]byte(got), []byte(want))
	}
	return matched == 1
}

// movieImported reflects a completed movie import: re-reads the movie so the
// broadcast carries live state (mirrors the hub's queue-departure witness) and
// pushes the new-content alert — or, for a proven upgrade, the admin-only
// upgrade alert. Falls back to the payload identity when the arr can't be
// reached.
func (h *Handler) movieImported(instanceID string, movieID int, title string, tmdbID int, isUpgrade bool) {
	if client, err := h.registry.GetRadarrClient(instanceID); err == nil {
		if movie, err := client.GetMovie(movieID); err == nil {
			if !movie.HasFile {
				return // upgrade replaced nothing / import ghosted; say nothing
			}
			title, tmdbID = movie.Title, movie.TmdbID
		}
	}
	h.hub.Broadcast(ws.Event{
		Type: "request_status_changed",
		Data: map[string]interface{}{
			"tmdb_id":     tmdbID,
			"media_type":  "movie",
			"status":      "available",
			"instance_id": instanceID,
		},
	})
	if h.content != nil {
		if isUpgrade {
			h.content.NotifyUpgradedMovie(title, tmdbID)
		} else {
			h.content.NotifyNewMovie(title, tmdbID)
		}
	}
}

// movieFileDeleted reflects a movie file removed while the movie stays in the
// library: monitored means Radarr will look again (requested), unmonitored
// means nobody will (unavailable).
func (h *Handler) movieFileDeleted(instanceID string, movieID, tmdbID int) {
	status := "unavailable"
	if client, err := h.registry.GetRadarrClient(instanceID); err == nil {
		if movie, err := client.GetMovie(movieID); err == nil {
			tmdbID = movie.TmdbID
			if movie.HasFile {
				return // another file still satisfies the movie
			}
			if movie.Monitored {
				status = "requested"
			}
		}
	}
	h.hub.Broadcast(ws.Event{
		Type: "request_status_changed",
		Data: map[string]interface{}{
			"tmdb_id":     tmdbID,
			"media_type":  "movie",
			"status":      status,
			"instance_id": instanceID,
		},
	})
}

// seriesChanged recomputes a series' availability from the live episode list
// (the same aired-aware completion the hub and status endpoint use) and
// broadcasts it; notify pushes the new-episode alert too (import events only —
// file deletions change availability but aren't news). isUpgrade reroutes that
// alert to the admin content_upgraded category.
func (h *Handler) seriesChanged(instanceID string, seriesID int, title string, tmdbID int, notify, isUpgrade bool) {
	status := "partially_available"
	if client, err := h.registry.GetSonarrClient(instanceID); err == nil {
		if series, err := client.GetSeries(seriesID); err == nil {
			title, tmdbID = series.Title, series.TmdbID
		}
		if episodes, err := client.GetAllEpisodes(seriesID); err == nil {
			if completion, _ := sonarr.SeriesCompletion(episodes, time.Now()); completion.Complete() {
				status = "available"
			}
		}
	} else {
		log.Printf("webhooks: sonarr client for %s: %v", instanceID, err)
	}
	h.hub.Broadcast(ws.Event{
		Type: "request_status_changed",
		Data: map[string]interface{}{
			"tmdb_id":     tmdbID,
			"media_type":  "tv",
			"status":      status,
			"instance_id": instanceID,
		},
	})
	if notify && h.content != nil {
		if isUpgrade {
			h.content.NotifyUpgradedEpisode(title, tmdbID)
		} else {
			h.content.NotifyNewEpisode(title, tmdbID)
		}
	}
}

// broadcastStatus emits a request_status_changed for whichever media object
// the payload carries, using only payload identity (no arr round-trip) — used
// for adds and full deletes where the new state is implied by the event.
func (h *Handler) broadcastStatus(instanceID string, payload arrPayload, status string) {
	if payload.Movie != nil {
		h.hub.Broadcast(ws.Event{
			Type: "request_status_changed",
			Data: map[string]interface{}{
				"tmdb_id":     payload.Movie.TmdbID,
				"media_type":  "movie",
				"status":      status,
				"instance_id": instanceID,
			},
		})
	}
	if payload.Series != nil {
		h.hub.Broadcast(ws.Event{
			Type: "request_status_changed",
			Data: map[string]interface{}{
				"tmdb_id":     payload.Series.TmdbID,
				"media_type":  "tv",
				"status":      status,
				"instance_id": instanceID,
			},
		})
	}
}
