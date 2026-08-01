// downloads.go — download-client monitoring for the seeded SABnzbd instance
// (srv-instances §3, app-admin §10, gap-plan §1.13 + §4.6): the unified
// QueueView, whole-client and per-item pause/resume, item removal, history,
// and the downloads_queue WS snapshot ticker.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// dlClientSpeedBps is the simulated whole-client download rate while at least
// one item is actively downloading.
const dlClientSpeedBps int64 = 12_500_000

// dlETAHiddenSentinel mirrors qBittorrent's unknown-ETA sentinel: the app
// hides any eta_seconds >= 8640000 (or <= 0). SABnzbd items keep computed
// ETAs; the constant documents the boundary the UI applies.
const dlETAHiddenSentinel = 8640000

// ─── Queue / history shapes (srv-instances §3) ──────────

type dlQueueItemJSON struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	SizeBytes     int64   `json:"size_bytes"`
	SizeLeftBytes int64   `json:"size_left_bytes"`
	Progress      float64 `json:"progress"` // 0–100
	SpeedBps      int64   `json:"speed_bps"`
	EtaSeconds    int     `json:"eta_seconds"`
	Status        string  `json:"status"`
	Category      string  `json:"category"`
}

type dlHistoryItemJSON struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	SizeBytes   int64  `json:"size_bytes"`
	CompletedAt string `json:"completed_at"` // RFC3339 UTC, "" when unknown
	Category    string `json:"category"`
	Error       string `json:"error"`
}

// dlItem is the internal mutable queue entry for the seeded SABnzbd client.
type dlItem struct {
	ID        string
	Name      string
	SizeBytes int64
	LeftBytes int64
	Status    string // SABnzbd-native: Downloading | Queued | Paused
	Category  string
}

// ─── State (guarded by dlMu) ────────────────────────────

var (
	dlMu           sync.Mutex
	dlPaused       bool
	dlItems        []*dlItem
	dlHistory      []dlHistoryItemJSON // newest first
	dlLastSnapshot []byte
	dlNextSpawnID  = 100
	dlSpawnCursor  int
)

// dlRespawnPool keeps the simulated queue alive forever: when an item
// completes, the next pool entry is queued in its place.
var dlRespawnPool = []dlItem{
	{Name: "The.Cabinet.of.Dr.Caligari.1920.1080p.BluRay.x264-DEMO", SizeBytes: 3_650_000_000, Category: "movies"},
	{Name: "Frankenstein.Mary.Shelley.1818.ebook.EPUB-DEMO", SizeBytes: 3_400_000, Category: "books"},
	{Name: "The.Gold.Rush.1925.1080p.BluRay.x264-DEMO", SizeBytes: 4_050_000_000, Category: "movies"},
	{Name: "Dracula.Bram.Stoker.1897.Audiobook.M4B-DEMO", SizeBytes: 410_000_000, Category: "books"},
	{Name: "Sunrise.A.Song.of.Two.Humans.1927.1080p.BluRay.x264-DEMO", SizeBytes: 4_480_000_000, Category: "movies"},
}

func init() {
	dlSeed()
}

func dlSeed() {
	dlMu.Lock()
	defer dlMu.Unlock()

	dlItems = []*dlItem{
		{
			ID: "SABnzbd_nzo_metropolis27", Name: "Metropolis.1927.Restored.1080p.BluRay.x264-DEMO",
			SizeBytes: 4_510_000_000, LeftBytes: 1_710_000_000, Status: "Downloading", Category: "movies",
		},
		{
			ID: "SABnzbd_nzo_mobydick51", Name: "Moby.Dick.Herman.Melville.1851.Audiobook.M4B-DEMO",
			SizeBytes: 620_000_000, LeftBytes: 505_000_000, Status: "Downloading", Category: "books",
		},
		{
			ID: "SABnzbd_nzo_general26", Name: "The.General.1926.1080p.BluRay.x264-DEMO",
			SizeBytes: 3_920_000_000, LeftBytes: 3_920_000_000, Status: "Queued", Category: "movies",
		},
		{
			ID: "SABnzbd_nzo_nosferatu22", Name: "Nosferatu.1922.720p.WEB-DL.x264-DEMO",
			SizeBytes: 2_150_000_000, LeftBytes: 1_400_000_000, Status: "Paused", Category: "movies",
		},
	}

	now := time.Now().UTC()
	at := func(minsAgo int) string { return now.Add(-time.Duration(minsAgo) * time.Minute).Format(time.RFC3339) }
	dlHistory = []dlHistoryItemJSON{
		{Name: "A.Trip.to.the.Moon.1902.1080p.Restored.x264-DEMO", Status: "Completed", SizeBytes: 890_000_000, CompletedAt: at(34), Category: "movies", Error: ""},
		{Name: "Pride.and.Prejudice.Jane.Austen.1813.ebook.EPUB-DEMO", Status: "Completed", SizeBytes: 2_400_000, CompletedAt: at(96), Category: "books", Error: ""},
		{Name: "The.Adventures.of.Sherlock.Holmes.1892.ebook.EPUB-DEMO", Status: "Failed", SizeBytes: 3_100_000, CompletedAt: at(150), Category: "books", Error: "Download failed: repair failed, not enough repair blocks (need 12 more)"},
		{Name: "The.Phantom.of.the.Opera.1925.1080p.BluRay.x264-DEMO", Status: "Completed", SizeBytes: 4_120_000_000, CompletedAt: at(263), Category: "movies", Error: ""},
		{Name: "The.Time.Machine.HG.Wells.1895.Audiobook.M4B-DEMO", Status: "Completed", SizeBytes: 385_000_000, CompletedAt: at(322), Category: "books", Error: ""},
		{Name: "Battleship.Potemkin.1925.1080p.BluRay.x264-DEMO", Status: "Completed", SizeBytes: 3_340_000_000, CompletedAt: at(701), Category: "movies", Error: ""},
		{Name: "Treasure.Island.RL.Stevenson.1883.Audiobook.M4B-DEMO", Status: "Failed", SizeBytes: 460_000_000, CompletedAt: at(880), Category: "books", Error: "Unpacking failed, archive requires a password"},
		{Name: "Alices.Adventures.in.Wonderland.Lewis.Carroll.1865.ebook.EPUB-DEMO", Status: "Completed", SizeBytes: 1_900_000, CompletedAt: at(1_130), Category: "books", Error: ""},
		{Name: "The.Kid.1921.1080p.BluRay.x264-DEMO", Status: "Completed", SizeBytes: 3_010_000_000, CompletedAt: at(1_402), Category: "movies", Error: ""},
		{Name: "Safety.Last.1923.1080p.BluRay.x264-DEMO", Status: "Completed", SizeBytes: 3_270_000_000, CompletedAt: at(1_688), Category: "movies", Error: ""},
		{Name: "The.Strange.Case.of.Dr.Jekyll.and.Mr.Hyde.1886.ebook.EPUB-DEMO", Status: "Completed", SizeBytes: 1_400_000, CompletedAt: at(2_120), Category: "books", Error: ""},
		{Name: "The.Lost.World.1925.1080p.BluRay.x264-DEMO", Status: "Completed", SizeBytes: 3_760_000_000, CompletedAt: at(2_501), Category: "movies", Error: ""},
	}
}

// ─── QueueView rendering ────────────────────────────────

func dlActiveCountLocked() int {
	if dlPaused {
		return 0
	}
	n := 0
	for _, it := range dlItems {
		if it.Status == "Downloading" {
			n++
		}
	}
	return n
}

// dlQueueViewLocked renders the current QueueView fields. items is never nil.
func dlQueueViewLocked() (paused bool, speedBps int64, items []dlQueueItemJSON) {
	paused = dlPaused
	active := dlActiveCountLocked()
	if active > 0 {
		speedBps = dlClientSpeedBps
	}
	items = make([]dlQueueItemJSON, 0, len(dlItems))
	for _, it := range dlItems {
		progress := 0.0
		if it.SizeBytes > 0 {
			progress = 100 * float64(it.SizeBytes-it.LeftBytes) / float64(it.SizeBytes)
		}
		if progress < 0 {
			progress = 0
		}
		if progress > 100 {
			progress = 100
		}
		eta := 0
		if !paused && it.Status == "Downloading" && active > 0 {
			share := dlClientSpeedBps / int64(active)
			if share > 0 {
				eta = int(it.LeftBytes / share)
			}
		}
		items = append(items, dlQueueItemJSON{
			ID:            it.ID,
			Name:          it.Name,
			SizeBytes:     it.SizeBytes,
			SizeLeftBytes: it.LeftBytes,
			Progress:      float64(int(progress*10)) / 10, // one decimal place
			SpeedBps:      0,                              // SABnzbd does not report per-item speed
			EtaSeconds:    eta,
			Status:        it.Status,
			Category:      it.Category,
		})
	}
	return paused, speedBps, items
}

// dlEmitSnapshot recomputes the QueueView and sends the downloads_queue event
// to admins — only when it changed since the last emit (force overrides).
func dlEmitSnapshot(force bool) {
	dlMu.Lock()
	paused, speed, items := dlQueueViewLocked()
	view := map[string]any{"paused": paused, "speed_bps": speed, "items": items}
	b, _ := json.Marshal(view)
	changed := !bytes.Equal(b, dlLastSnapshot)
	if changed || force {
		dlLastSnapshot = b
	}
	dlMu.Unlock()
	if changed || force {
		view["instance_id"] = instSab
		wsToAdmins(evtDownloadsQueue, view)
	}
}

// ─── Simulation ticker ──────────────────────────────────

// dlStartDownloadsTicker starts the downloads_queue simulation loop: ~15 s
// recompute cadence, tightening to 1.5 s while anything is animating. The
// integration pass wires this into startSimulations().
func dlStartDownloadsTicker() {
	go func() {
		for {
			animating := dlAdvance(1500 * time.Millisecond)
			dlEmitSnapshot(false)
			if animating {
				time.Sleep(1500 * time.Millisecond)
			} else {
				time.Sleep(15 * time.Second)
			}
		}
	}()
}

// dlAdvance moves active items forward by one interval's worth of bytes and
// recycles completed items into history; reports whether anything is animating.
func dlAdvance(interval time.Duration) bool {
	dlMu.Lock()
	defer dlMu.Unlock()
	active := dlActiveCountLocked()
	if active == 0 {
		return false
	}
	share := dlClientSpeedBps / int64(active)
	step := int64(float64(share) * interval.Seconds())
	completed := []*dlItem{}
	for _, it := range dlItems {
		if it.Status != "Downloading" {
			continue
		}
		it.LeftBytes -= step
		if it.LeftBytes <= 0 {
			it.LeftBytes = 0
			completed = append(completed, it)
		}
	}
	for _, done := range completed {
		// Move to history (newest first)…
		dlHistory = append([]dlHistoryItemJSON{{
			Name: done.Name, Status: "Completed", SizeBytes: done.SizeBytes,
			CompletedAt: time.Now().UTC().Format(time.RFC3339),
			Category:    done.Category, Error: "",
		}}, dlHistory...)
		if len(dlHistory) > 40 {
			dlHistory = dlHistory[:40]
		}
		// …drop from the queue…
		for idx, it := range dlItems {
			if it == done {
				dlItems = append(dlItems[:idx], dlItems[idx+1:]...)
				break
			}
		}
		// …promote the first queued item…
		for _, it := range dlItems {
			if it.Status == "Queued" {
				it.Status = "Downloading"
				break
			}
		}
		// …and respawn a fresh queued item so the demo never runs dry.
		tpl := dlRespawnPool[dlSpawnCursor%len(dlRespawnPool)]
		dlSpawnCursor++
		dlItems = append(dlItems, &dlItem{
			ID:        fmt.Sprintf("SABnzbd_nzo_demo%03d", dlNextSpawnID),
			Name:      tpl.Name,
			SizeBytes: tpl.SizeBytes,
			LeftBytes: tpl.SizeBytes,
			Status:    "Queued",
			Category:  tpl.Category,
		})
		dlNextSpawnID++
	}
	return dlActiveCountLocked() > 0
}

// ─── Handlers ───────────────────────────────────────────

var dlClientTypes = map[string]bool{
	serviceSabnzbd: true, serviceQbittorrent: true,
	serviceNzbget: true, serviceTransmission: true,
}

// dlResolveClient validates the {instanceID} route param as a download-client
// instance; writes the error response and returns nil when invalid.
func dlResolveClient(w http.ResponseWriter, r *http.Request) *DemoInstance {
	id := chi.URLParam(r, "instanceID")
	inst := instMgmtResolve(id)
	if inst == nil {
		writeErr(w, http.StatusNotFound, "instance not found")
		return nil
	}
	if !dlClientTypes[inst.ServiceType] {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("instance %s is not a download client", inst.ID))
		return nil
	}
	return inst
}

// registerDownloads mounts /api/downloads/{instanceID}/* (admin only).
func registerDownloads(r chi.Router) {
	r.Route("/downloads/{instanceID}", func(sr chi.Router) {
		sr.Use(requireAdmin)
		sr.Get("/queue", dlHandleQueue)
		sr.Get("/history", dlHandleHistory)
		sr.Post("/pause", dlHandleClientPause)
		sr.Post("/resume", dlHandleClientResume)
		sr.Post("/queue/{itemID}/pause", dlHandleItemPause)
		sr.Post("/queue/{itemID}/resume", dlHandleItemResume)
		sr.Delete("/queue/{itemID}", dlHandleItemDelete)
	})
}

func dlHandleQueue(w http.ResponseWriter, r *http.Request) {
	inst := dlResolveClient(w, r)
	if inst == nil {
		return
	}
	if inst.ID != instSab {
		// Only the seeded SABnzbd instance carries simulated state.
		writeJSON(w, http.StatusOK, map[string]any{
			"paused": false, "speed_bps": 0, "items": []dlQueueItemJSON{},
		})
		return
	}
	dlMu.Lock()
	paused, speed, items := dlQueueViewLocked()
	dlMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"paused": paused, "speed_bps": speed, "items": items,
	})
}

func dlHandleHistory(w http.ResponseWriter, r *http.Request) {
	inst := dlResolveClient(w, r)
	if inst == nil {
		return
	}
	limit := queryInt(r, "limit", 50)
	if limit <= 0 {
		limit = 50
	}
	items := []dlHistoryItemJSON{}
	if inst.ID == instSab {
		dlMu.Lock()
		if limit > len(dlHistory) {
			limit = len(dlHistory)
		}
		items = append(items, dlHistory[:limit]...)
		dlMu.Unlock()
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func dlHandleClientPause(w http.ResponseWriter, r *http.Request) {
	if dlResolveClient(w, r) == nil {
		return
	}
	dlMu.Lock()
	dlPaused = true
	dlMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
	dlEmitSnapshot(false)
}

func dlHandleClientResume(w http.ResponseWriter, r *http.Request) {
	if dlResolveClient(w, r) == nil {
		return
	}
	dlMu.Lock()
	dlPaused = false
	dlMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
	dlEmitSnapshot(false)
}

func dlSetItemStatus(itemID, status string) {
	dlMu.Lock()
	for _, it := range dlItems {
		if it.ID == itemID {
			it.Status = status
			break
		}
	}
	dlMu.Unlock()
}

func dlHandleItemPause(w http.ResponseWriter, r *http.Request) {
	if dlResolveClient(w, r) == nil {
		return
	}
	itemID := chi.URLParam(r, "itemID")
	if itemID == "" {
		writeErr(w, http.StatusBadRequest, "item id is required")
		return
	}
	dlSetItemStatus(itemID, "Paused")
	w.WriteHeader(http.StatusNoContent)
	dlEmitSnapshot(false)
}

func dlHandleItemResume(w http.ResponseWriter, r *http.Request) {
	if dlResolveClient(w, r) == nil {
		return
	}
	itemID := chi.URLParam(r, "itemID")
	if itemID == "" {
		writeErr(w, http.StatusBadRequest, "item id is required")
		return
	}
	dlSetItemStatus(itemID, "Downloading")
	w.WriteHeader(http.StatusNoContent)
	dlEmitSnapshot(false)
}

func dlHandleItemDelete(w http.ResponseWriter, r *http.Request) {
	if dlResolveClient(w, r) == nil {
		return
	}
	itemID := chi.URLParam(r, "itemID")
	if itemID == "" {
		writeErr(w, http.StatusBadRequest, "item id is required")
		return
	}
	_ = r.URL.Query().Get("deleteData") == "true" // demo has no files to delete
	dlMu.Lock()
	for idx, it := range dlItems {
		if it.ID == itemID {
			dlItems = append(dlItems[:idx], dlItems[idx+1:]...)
			break
		}
	}
	dlMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
	// The spec requires a fresh snapshot after a removal.
	dlEmitSnapshot(true)
}
