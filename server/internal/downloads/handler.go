// Package downloads exposes a unified REST API over SABnzbd and qBittorrent
// download-client instances, normalizing both backends into a common shape.
package downloads

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/qbittorrent"
	"github.com/windoze95/cantinarr-server/internal/sabnzbd"
)

// qBittorrent reports this ETA when it is unknown/infinite; normalize to 0.
const qbitETAInfinity = 8640000

// Handler provides REST endpoints for managing download clients.
type Handler struct {
	store    *instance.Store
	registry *instance.Registry
}

// NewHandler creates a new downloads handler.
func NewHandler(store *instance.Store, registry *instance.Registry) *Handler {
	return &Handler{store: store, registry: registry}
}

// queueItem is the normalized shape of a single download across backends.
// id is the SABnzbd nzo_id or the qBittorrent torrent hash.
type queueItem struct {
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

type queueResponse struct {
	Paused   bool        `json:"paused"`
	SpeedBPS int64       `json:"speed_bps"`
	Items    []queueItem `json:"items"`
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

// GetQueue returns the normalized download queue for an instance.
func (h *Handler) GetQueue(w http.ResponseWriter, r *http.Request) {
	inst := h.resolveInstance(w, r)
	if inst == nil {
		return
	}

	switch inst.ServiceType {
	case "sabnzbd":
		client, err := h.registry.GetSabnzbdClient(inst.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		queue, err := client.GetQueue()
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		resp := queueResponse{
			Paused:   queue.Paused,
			SpeedBPS: queue.SpeedBPS(),
			Items:    make([]queueItem, 0, len(queue.Slots)),
		}
		for _, slot := range queue.Slots {
			category := slot.Category
			if category == "*" {
				category = ""
			}
			resp.Items = append(resp.Items, queueItem{
				ID:            slot.NzoID,
				Name:          slot.Filename,
				SizeBytes:     slot.SizeBytes(),
				SizeLeftBytes: slot.SizeLeftBytes(),
				Progress:      slot.Progress(),
				SpeedBPS:      0, // SABnzbd does not report per-item speed
				ETASeconds:    slot.ETASeconds(),
				Status:        slot.Status,
				Category:      category,
			})
		}
		writeJSON(w, resp)

	case "qbittorrent":
		client, err := h.registry.GetQbittorrentClient(inst.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		torrents, err := client.GetTorrents()
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		info, err := client.GetTransferInfo()
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}

		resp := queueResponse{
			SpeedBPS: info.DLInfoSpeed,
			Items:    make([]queueItem, 0),
		}
		anyActive := false
		for _, t := range torrents {
			if t.Progress >= 1 {
				continue // completed torrents are reported via /history
			}
			if !isQbitPausedState(t.State) {
				anyActive = true
			}
			eta := t.ETA
			if eta >= qbitETAInfinity || eta < 0 {
				eta = 0
			}
			sizeLeft := int64(float64(t.Size) * (1 - t.Progress))
			if sizeLeft < 0 {
				sizeLeft = 0
			}
			resp.Items = append(resp.Items, queueItem{
				ID:            t.Hash,
				Name:          t.Name,
				SizeBytes:     t.Size,
				SizeLeftBytes: sizeLeft,
				Progress:      t.Progress * 100,
				SpeedBPS:      t.DLSpeed,
				ETASeconds:    eta,
				Status:        t.State,
				Category:      t.Category,
			})
		}
		resp.Paused = len(resp.Items) > 0 && !anyActive
		writeJSON(w, resp)
	}
}

// PauseItem pauses a single queue item.
func (h *Handler) PauseItem(w http.ResponseWriter, r *http.Request) {
	h.itemAction(w, r,
		func(c *sabnzbd.Client, itemID string) error { return c.PauseItem(itemID) },
		func(c *qbittorrent.Client, itemID string) error { return c.PauseTorrents(itemID) },
	)
}

// ResumeItem resumes a single queue item.
func (h *Handler) ResumeItem(w http.ResponseWriter, r *http.Request) {
	h.itemAction(w, r,
		func(c *sabnzbd.Client, itemID string) error { return c.ResumeItem(itemID) },
		func(c *qbittorrent.Client, itemID string) error { return c.ResumeTorrents(itemID) },
	)
}

// DeleteItem removes a queue item; ?deleteData=true also deletes downloaded files.
func (h *Handler) DeleteItem(w http.ResponseWriter, r *http.Request) {
	deleteData := r.URL.Query().Get("deleteData") == "true"
	h.itemAction(w, r,
		func(c *sabnzbd.Client, itemID string) error { return c.DeleteItem(itemID, deleteData) },
		func(c *qbittorrent.Client, itemID string) error { return c.Delete(itemID, deleteData) },
	)
}

// PauseAll pauses the whole queue.
func (h *Handler) PauseAll(w http.ResponseWriter, r *http.Request) {
	h.queueAction(w, r,
		func(c *sabnzbd.Client) error { return c.PauseQueue() },
		func(c *qbittorrent.Client) error { return c.PauseTorrents("all") },
	)
}

// ResumeAll resumes the whole queue.
func (h *Handler) ResumeAll(w http.ResponseWriter, r *http.Request) {
	h.queueAction(w, r,
		func(c *sabnzbd.Client) error { return c.ResumeQueue() },
		func(c *qbittorrent.Client) error { return c.ResumeTorrents("all") },
	)
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

	switch inst.ServiceType {
	case "sabnzbd":
		client, err := h.registry.GetSabnzbdClient(inst.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		slots, err := client.GetHistory(limit)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		resp := historyResponse{Items: make([]historyItem, 0, len(slots))}
		for _, slot := range slots {
			completedAt := ""
			if slot.Completed > 0 {
				completedAt = time.Unix(slot.Completed, 0).UTC().Format(time.RFC3339)
			}
			resp.Items = append(resp.Items, historyItem{
				Name:        slot.Name,
				Status:      slot.Status,
				SizeBytes:   int64(slot.Bytes),
				CompletedAt: completedAt,
				Category:    slot.Category,
				Error:       slot.FailMessage,
			})
		}
		writeJSON(w, resp)

	case "qbittorrent":
		client, err := h.registry.GetQbittorrentClient(inst.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		torrents, err := client.GetTorrents()
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		completed := torrents[:0:0]
		for _, t := range torrents {
			if t.Progress >= 1 {
				completed = append(completed, t)
			}
		}
		sort.Slice(completed, func(i, j int) bool {
			return completed[i].CompletionOn > completed[j].CompletionOn
		})
		if len(completed) > limit {
			completed = completed[:limit]
		}
		resp := historyResponse{Items: make([]historyItem, 0, len(completed))}
		for _, t := range completed {
			completedAt := ""
			if t.CompletionOn > 0 {
				completedAt = time.Unix(t.CompletionOn, 0).UTC().Format(time.RFC3339)
			}
			errMsg := ""
			if t.State == "error" || t.State == "missingFiles" {
				errMsg = t.State
			}
			resp.Items = append(resp.Items, historyItem{
				Name:        t.Name,
				Status:      t.State,
				SizeBytes:   t.Size,
				CompletedAt: completedAt,
				Category:    t.Category,
				Error:       errMsg,
			})
		}
		writeJSON(w, resp)
	}
}

// --- dispatch helpers ---

// itemAction resolves the instance and itemID, dispatches the per-item action
// for the backend, and replies 204 on success.
func (h *Handler) itemAction(w http.ResponseWriter, r *http.Request, sabFn func(*sabnzbd.Client, string) error, qbitFn func(*qbittorrent.Client, string) error) {
	inst := h.resolveInstance(w, r)
	if inst == nil {
		return
	}
	itemID := chi.URLParam(r, "itemID")
	if itemID == "" {
		writeError(w, http.StatusBadRequest, "item id is required")
		return
	}

	switch inst.ServiceType {
	case "sabnzbd":
		client, err := h.registry.GetSabnzbdClient(inst.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := sabFn(client, itemID); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	case "qbittorrent":
		client, err := h.registry.GetQbittorrentClient(inst.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := qbitFn(client, itemID); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// queueAction resolves the instance, dispatches the whole-queue action for
// the backend, and replies 204 on success.
func (h *Handler) queueAction(w http.ResponseWriter, r *http.Request, sabFn func(*sabnzbd.Client) error, qbitFn func(*qbittorrent.Client) error) {
	inst := h.resolveInstance(w, r)
	if inst == nil {
		return
	}

	switch inst.ServiceType {
	case "sabnzbd":
		client, err := h.registry.GetSabnzbdClient(inst.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := sabFn(client); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	case "qbittorrent":
		client, err := h.registry.GetQbittorrentClient(inst.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := qbitFn(client); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
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
	if inst.ServiceType != "sabnzbd" && inst.ServiceType != "qbittorrent" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("instance %s is not a download client", instanceID))
		return nil
	}
	return inst
}

func isQbitPausedState(state string) bool {
	switch state {
	case "pausedDL", "pausedUP", "stoppedDL", "stoppedUP":
		return true
	}
	return false
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
