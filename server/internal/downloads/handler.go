// Package downloads exposes a unified REST API over SABnzbd, qBittorrent,
// NZBGet, Transmission, and Deluge download-client instances, normalizing
// all backends into a common shape. Each client's mapping lives in its own
// backend_<type>.go adapter behind the backend interface (backend.go).
package downloads

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/instance"
)

// Handler provides REST endpoints for managing download clients.
type Handler struct {
	store    *instance.Store
	registry *instance.Registry
}

// NewHandler creates a new downloads handler.
func NewHandler(store *instance.Store, registry *instance.Registry) *Handler {
	return &Handler{store: store, registry: registry}
}

// QueueItem is the normalized shape of a single download across backends.
// id is the SABnzbd nzo_id, the qBittorrent/Transmission/Deluge torrent
// hash, or the NZBGet NZBID.
type QueueItem struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	SizeBytes     int64   `json:"size_bytes"`
	SizeLeftBytes int64   `json:"size_left_bytes"`
	Progress      float64 `json:"progress"` // 0-100
	SpeedBPS      int64   `json:"speed_bps"`
	ETASeconds    int64   `json:"eta_seconds"`
	Status        string  `json:"status"`
	Category      string  `json:"category"`
}

// QueueView is the normalized download queue for one instance. It is the
// response body of the queue endpoint and the payload of the websocket
// downloads_queue event.
type QueueView struct {
	Paused   bool        `json:"paused"`
	SpeedBPS int64       `json:"speed_bps"`
	Items    []QueueItem `json:"items"`
}

type historyItem struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	SizeBytes   int64  `json:"size_bytes"`
	CompletedAt string `json:"completed_at"` // RFC3339 UTC, "" if unknown
	Category    string `json:"category"`
	Error       string `json:"error"`
}

type historyResponse struct {
	Items []historyItem `json:"items"`
}

// Snapshot builds the normalized queue view for a download-client instance.
// It is the single implementation behind both the HTTP queue endpoint and the
// websocket hub's downloads poller.
func Snapshot(reg *instance.Registry, inst instance.Instance) (*QueueView, error) {
	b, err := backendFor(reg, inst)
	if err != nil {
		return nil, err
	}
	return b.Snapshot()
}

// GetQueue returns the normalized download queue for an instance.
func (h *Handler) GetQueue(w http.ResponseWriter, r *http.Request) {
	inst := h.resolveInstance(w, r)
	if inst == nil {
		return
	}

	view, err := Snapshot(h.registry, *inst)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, view)
}

// PauseItem pauses a single queue item.
func (h *Handler) PauseItem(w http.ResponseWriter, r *http.Request) {
	h.itemAction(w, r, func(b backend, itemID string) error { return b.PauseItem(itemID) })
}

// ResumeItem resumes a single queue item.
func (h *Handler) ResumeItem(w http.ResponseWriter, r *http.Request) {
	h.itemAction(w, r, func(b backend, itemID string) error { return b.ResumeItem(itemID) })
}

// DeleteItem removes a queue item; ?deleteData=true also deletes downloaded files.
func (h *Handler) DeleteItem(w http.ResponseWriter, r *http.Request) {
	deleteData := r.URL.Query().Get("deleteData") == "true"
	h.itemAction(w, r, func(b backend, itemID string) error { return b.DeleteItem(itemID, deleteData) })
}

// PauseAll pauses the whole queue.
func (h *Handler) PauseAll(w http.ResponseWriter, r *http.Request) {
	h.queueAction(w, r, func(b backend) error { return b.PauseAll() })
}

// ResumeAll resumes the whole queue.
func (h *Handler) ResumeAll(w http.ResponseWriter, r *http.Request) {
	h.queueAction(w, r, func(b backend) error { return b.ResumeAll() })
}

// GetHistory returns recently completed downloads. ?limit=N (default 50).
func (h *Handler) GetHistory(w http.ResponseWriter, r *http.Request) {
	inst := h.resolveInstance(w, r)
	if inst == nil {
		return
	}

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	b, err := backendFor(h.registry, *inst)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items, err := b.History(limit)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, historyResponse{Items: items})
}

// --- dispatch helpers ---

// itemAction resolves the instance and itemID, runs the per-item action on
// the instance's backend, and replies 204 on success. A registry failure is
// a 500, an unusable item id a 400, and a backend failure a 502.
func (h *Handler) itemAction(w http.ResponseWriter, r *http.Request, action func(backend, string) error) {
	inst := h.resolveInstance(w, r)
	if inst == nil {
		return
	}
	itemID := chi.URLParam(r, "itemID")
	if itemID == "" {
		writeError(w, http.StatusBadRequest, "item id is required")
		return
	}

	b, err := backendFor(h.registry, *inst)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := action(b, itemID); err != nil {
		var bad *badItemIDError
		if errors.As(err, &bad) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// queueAction resolves the instance, runs the whole-queue action on its
// backend, and replies 204 on success.
func (h *Handler) queueAction(w http.ResponseWriter, r *http.Request, action func(backend) error) {
	inst := h.resolveInstance(w, r)
	if inst == nil {
		return
	}

	b, err := backendFor(h.registry, *inst)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := action(b); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resolveInstance loads the instance from the path and verifies it is a
// download client. On failure it writes the error response and returns nil.
func (h *Handler) resolveInstance(w http.ResponseWriter, r *http.Request) *instance.Instance {
	instanceID := chi.URLParam(r, "instanceID")
	inst, err := h.store.Get(instanceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil
	}
	if inst == nil {
		writeError(w, http.StatusNotFound, "instance not found")
		return nil
	}
	if !IsDownloadClientType(inst.ServiceType) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("instance %s is not a download client", instanceID))
		return nil
	}
	return inst
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
