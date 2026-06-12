// Package downloads exposes a unified REST API over SABnzbd, qBittorrent,
// NZBGet, and Transmission download-client instances, normalizing all
// backends into a common shape.
package downloads

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/nzbget"
	"github.com/windoze95/cantinarr-server/internal/qbittorrent"
	"github.com/windoze95/cantinarr-server/internal/sabnzbd"
	"github.com/windoze95/cantinarr-server/internal/transmission"
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

// QueueItem is the normalized shape of a single download across backends.
// id is the SABnzbd nzo_id, the qBittorrent/Transmission torrent hash, or the
// NZBGet NZBID.
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
	switch inst.ServiceType {
	case "sabnzbd":
		client, err := reg.GetSabnzbdClient(inst.ID)
		if err != nil {
			return nil, err
		}
		queue, err := client.GetQueue()
		if err != nil {
			return nil, err
		}
		view := &QueueView{
			Paused:   queue.Paused,
			SpeedBPS: queue.SpeedBPS(),
			Items:    make([]QueueItem, 0, len(queue.Slots)),
		}
		for _, slot := range queue.Slots {
			category := slot.Category
			if category == "*" {
				category = ""
			}
			view.Items = append(view.Items, QueueItem{
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
		return view, nil

	case "qbittorrent":
		client, err := reg.GetQbittorrentClient(inst.ID)
		if err != nil {
			return nil, err
		}
		torrents, err := client.GetTorrents()
		if err != nil {
			return nil, err
		}
		info, err := client.GetTransferInfo()
		if err != nil {
			return nil, err
		}
		view := &QueueView{
			SpeedBPS: info.DLInfoSpeed,
			Items:    make([]QueueItem, 0),
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
			view.Items = append(view.Items, QueueItem{
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
		view.Paused = len(view.Items) > 0 && !anyActive
		return view, nil

	case "nzbget":
		client, err := reg.GetNzbgetClient(inst.ID)
		if err != nil {
			return nil, err
		}
		groups, err := client.ListGroups()
		if err != nil {
			return nil, err
		}
		status, err := client.GetStatus()
		if err != nil {
			return nil, err
		}
		view := &QueueView{
			Paused:   status.DownloadPaused,
			SpeedBPS: status.DownloadRate,
			Items:    make([]QueueItem, 0, len(groups)),
		}
		for _, g := range groups {
			size := g.SizeBytes()
			left := g.RemainingBytes()
			progress := 0.0
			if size > 0 {
				progress = float64(size-left) / float64(size) * 100
			}
			var eta int64
			if status.DownloadRate > 0 {
				eta = left / status.DownloadRate
			}
			view.Items = append(view.Items, QueueItem{
				ID:            strconv.Itoa(g.NZBID),
				Name:          g.NZBName,
				SizeBytes:     size,
				SizeLeftBytes: left,
				Progress:      progress,
				SpeedBPS:      0, // NZBGet does not report per-item speed
				ETASeconds:    eta,
				Status:        g.Status,
				Category:      g.Category,
			})
		}
		return view, nil

	case "transmission":
		client, err := reg.GetTransmissionClient(inst.ID)
		if err != nil {
			return nil, err
		}
		torrents, err := client.GetTorrents()
		if err != nil {
			return nil, err
		}
		stats, err := client.GetSessionStats()
		if err != nil {
			return nil, err
		}
		view := &QueueView{
			SpeedBPS: stats.DownloadSpeed,
			Items:    make([]QueueItem, 0),
		}
		anyActive := false
		for _, t := range torrents {
			if t.PercentDone >= 1 {
				continue // completed torrents are reported via /history
			}
			if t.Status != transmission.StatusStopped {
				anyActive = true
			}
			eta := t.ETA
			if eta < 0 {
				eta = 0 // negative = unknown/unavailable
			}
			view.Items = append(view.Items, QueueItem{
				ID:            t.HashString,
				Name:          t.Name,
				SizeBytes:     t.TotalSize,
				SizeLeftBytes: t.LeftUntilDone,
				Progress:      t.PercentDone * 100,
				SpeedBPS:      t.RateDownload,
				ETASeconds:    eta,
				Status:        transmission.StatusString(t.Status),
				Category:      transmissionCategory(t),
			})
		}
		view.Paused = len(view.Items) > 0 && !anyActive
		return view, nil
	}
	return nil, fmt.Errorf("instance %s is not a download client", inst.ID)
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
	h.itemAction(w, r,
		func(c *sabnzbd.Client, itemID string) error { return c.PauseItem(itemID) },
		func(c *qbittorrent.Client, itemID string) error { return c.PauseTorrents(itemID) },
		func(c *nzbget.Client, nzbID int) error { return c.PauseGroups([]int{nzbID}) },
		func(c *transmission.Client, hash string) error { return c.StopTorrents([]string{hash}) },
	)
}

// ResumeItem resumes a single queue item.
func (h *Handler) ResumeItem(w http.ResponseWriter, r *http.Request) {
	h.itemAction(w, r,
		func(c *sabnzbd.Client, itemID string) error { return c.ResumeItem(itemID) },
		func(c *qbittorrent.Client, itemID string) error { return c.ResumeTorrents(itemID) },
		func(c *nzbget.Client, nzbID int) error { return c.ResumeGroups([]int{nzbID}) },
		func(c *transmission.Client, hash string) error { return c.StartTorrents([]string{hash}) },
	)
}

// DeleteItem removes a queue item; ?deleteData=true also deletes downloaded files.
func (h *Handler) DeleteItem(w http.ResponseWriter, r *http.Request) {
	deleteData := r.URL.Query().Get("deleteData") == "true"
	h.itemAction(w, r,
		func(c *sabnzbd.Client, itemID string) error { return c.DeleteItem(itemID, deleteData) },
		func(c *qbittorrent.Client, itemID string) error { return c.Delete(itemID, deleteData) },
		func(c *nzbget.Client, nzbID int) error { return c.DeleteGroups([]int{nzbID}) },
		func(c *transmission.Client, hash string) error { return c.RemoveTorrents([]string{hash}, deleteData) },
	)
}

// PauseAll pauses the whole queue.
func (h *Handler) PauseAll(w http.ResponseWriter, r *http.Request) {
	h.queueAction(w, r,
		func(c *sabnzbd.Client) error { return c.PauseQueue() },
		func(c *qbittorrent.Client) error { return c.PauseTorrents("all") },
		func(c *nzbget.Client) error { return c.PauseDownload() },
		func(c *transmission.Client) error { return c.StopTorrents(nil) },
	)
}

// ResumeAll resumes the whole queue.
func (h *Handler) ResumeAll(w http.ResponseWriter, r *http.Request) {
	h.queueAction(w, r,
		func(c *sabnzbd.Client) error { return c.ResumeQueue() },
		func(c *qbittorrent.Client) error { return c.ResumeTorrents("all") },
		func(c *nzbget.Client) error { return c.ResumeDownload() },
		func(c *transmission.Client) error { return c.StartTorrents(nil) },
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

	case "nzbget":
		client, err := h.registry.GetNzbgetClient(inst.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		entries, err := client.GetHistory()
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].HistoryTime > entries[j].HistoryTime
		})
		if len(entries) > limit {
			entries = entries[:limit]
		}
		resp := historyResponse{Items: make([]historyItem, 0, len(entries))}
		for _, e := range entries {
			completedAt := ""
			if e.HistoryTime > 0 {
				completedAt = time.Unix(e.HistoryTime, 0).UTC().Format(time.RFC3339)
			}
			status := e.Status
			errMsg := ""
			switch {
			case strings.HasPrefix(e.Status, "SUCCESS"):
				status = "Completed"
			case strings.HasPrefix(e.Status, "FAILURE"):
				status = "Failed"
				errMsg = e.Status
			}
			resp.Items = append(resp.Items, historyItem{
				Name:        e.Name,
				Status:      status,
				SizeBytes:   e.SizeBytes(),
				CompletedAt: completedAt,
				Category:    e.Category,
				Error:       errMsg,
			})
		}
		writeJSON(w, resp)

	case "transmission":
		client, err := h.registry.GetTransmissionClient(inst.ID)
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
			if t.PercentDone >= 1 {
				completed = append(completed, t)
			}
		}
		sort.Slice(completed, func(i, j int) bool {
			return completed[i].DoneDate > completed[j].DoneDate
		})
		if len(completed) > limit {
			completed = completed[:limit]
		}
		resp := historyResponse{Items: make([]historyItem, 0, len(completed))}
		for _, t := range completed {
			completedAt := ""
			if t.DoneDate > 0 {
				completedAt = time.Unix(t.DoneDate, 0).UTC().Format(time.RFC3339)
			}
			errMsg := ""
			if t.Error != 0 {
				errMsg = t.ErrorString
			}
			resp.Items = append(resp.Items, historyItem{
				Name:        t.Name,
				Status:      transmission.StatusString(t.Status),
				SizeBytes:   t.TotalSize,
				CompletedAt: completedAt,
				Category:    transmissionCategory(t),
				Error:       errMsg,
			})
		}
		writeJSON(w, resp)
	}
}

// --- dispatch helpers ---

// itemAction resolves the instance and itemID, dispatches the per-item action
// for the backend, and replies 204 on success.
func (h *Handler) itemAction(w http.ResponseWriter, r *http.Request,
	sabFn func(*sabnzbd.Client, string) error,
	qbitFn func(*qbittorrent.Client, string) error,
	nzbFn func(*nzbget.Client, int) error,
	transFn func(*transmission.Client, string) error,
) {
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
	case "nzbget":
		nzbID, err := strconv.Atoi(itemID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "item id must be a numeric NZBGet id")
			return
		}
		client, err := h.registry.GetNzbgetClient(inst.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := nzbFn(client, nzbID); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	case "transmission":
		client, err := h.registry.GetTransmissionClient(inst.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := transFn(client, itemID); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// queueAction resolves the instance, dispatches the whole-queue action for
// the backend, and replies 204 on success.
func (h *Handler) queueAction(w http.ResponseWriter, r *http.Request,
	sabFn func(*sabnzbd.Client) error,
	qbitFn func(*qbittorrent.Client) error,
	nzbFn func(*nzbget.Client) error,
	transFn func(*transmission.Client) error,
) {
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
	case "nzbget":
		client, err := h.registry.GetNzbgetClient(inst.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := nzbFn(client); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	case "transmission":
		client, err := h.registry.GetTransmissionClient(inst.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := transFn(client); err != nil {
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
	switch inst.ServiceType {
	case "sabnzbd", "qbittorrent", "nzbget", "transmission":
	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("instance %s is not a download client", instanceID))
		return nil
	}
	return inst
}

// transmissionCategory maps a torrent's labels to the unified category field:
// the first label, or "" when unlabeled.
func transmissionCategory(t transmission.Torrent) string {
	if len(t.Labels) > 0 {
		return t.Labels[0]
	}
	return ""
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
