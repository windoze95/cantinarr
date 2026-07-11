// Package webhooks receives Sonarr/Radarr "Connect → Webhook" callbacks so
// library changes made outside Cantinarr (manual imports, deletes, adds) are
// pushed instantly instead of caught on the next poll or user-driven refresh.
// Each callback authenticates with the instance's server-only Basic credential.
// These requests carry no user session and translate into the same websocket
// events and push notifications the queue-poll witness already emits, so the
// app needs no new event handling.
package webhooks

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
	ws "github.com/windoze95/cantinarr-server/internal/websocket"
)

// Broadcaster fans an event out to connected websocket clients. *ws.Hub
// satisfies it; declared here so tests can record broadcasts.
type Broadcaster interface {
	Broadcast(event ws.Event)
}

// AvailabilityInvalidator drops cached availability digests for an instance.
// *request.Service satisfies it.
type AvailabilityInvalidator interface {
	InvalidateAvailabilityDigests(instanceID string)
}

// Handler terminates the arr webhook callbacks.
type Handler struct {
	store    *instance.Store
	registry *instance.Registry
	hub      Broadcaster
	requests AvailabilityInvalidator
	content  ws.ContentNotifier
}

// NewHandler builds the webhook handler. content may be nil (push disabled).
func NewHandler(store *instance.Store, registry *instance.Registry, hub Broadcaster, requests AvailabilityInvalidator, content ws.ContentNotifier) *Handler {
	return &Handler{store: store, registry: registry, hub: hub, requests: requests, content: content}
}

// arrPayload is the superset of the Sonarr and Radarr webhook fields this
// handler acts on. Both apps send eventType plus a movie or series object;
// everything else is ignored.
type arrPayload struct {
	EventType string `json:"eventType"`
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
}

// HandleArr receives the server-managed Sonarr/Radarr Connect webhook. The
// server-only credential is carried as the Basic Auth password, never in a URL
// that an HTTP access logger could persist.
func (h *Handler) HandleArr(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "instanceID")
	inst, err := h.store.Get(instanceID)
	if err != nil || inst == nil || (inst.ServiceType != "radarr" && inst.ServiceType != "sonarr") {
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
				"service_type": inst.ServiceType,
			},
		})

	case "Download": // import completed (including manual imports)
		h.requests.InvalidateAvailabilityDigests(instanceID)
		if payload.Movie != nil {
			h.movieImported(instanceID, payload.Movie.ID, payload.Movie.Title, payload.Movie.TmdbID)
		}
		if payload.Series != nil {
			h.seriesChanged(instanceID, payload.Series.ID, payload.Series.Title, payload.Series.TmdbID, true)
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
			h.seriesChanged(instanceID, payload.Series.ID, payload.Series.Title, payload.Series.TmdbID, false)
		}

	default:
		// Health, Rename, ApplicationUpdate, … — acknowledged, no action.
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
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
// pushes the new-content alert. Falls back to the payload identity when the
// arr can't be reached.
func (h *Handler) movieImported(instanceID string, movieID int, title string, tmdbID int) {
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
		h.content.NotifyNewMovie(title, tmdbID)
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
// file deletions change availability but aren't news).
func (h *Handler) seriesChanged(instanceID string, seriesID int, title string, tmdbID int, notify bool) {
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
		h.content.NotifyNewEpisode(title, tmdbID)
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
