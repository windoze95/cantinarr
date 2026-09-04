// downloads.go — download-client monitoring for the two seeded clients
// (srv-instances §3, app-admin §10, gap-plan §1.13 + §4.6): the unified
// QueueView, whole-client and per-item pause/resume, item removal, history,
// and the downloads_queue WS snapshot ticker.
//
// SABnzbd (instSab) is the animated client: its queue advances, completes,
// recycles into history, and respawns. qBittorrent (instQbittorrent) is a
// static torrent set rendered through the real handler's mapping — raw
// torrent states pass through as status, progress is a fraction of the size,
// completed torrents are the history, and paused means "nothing is outside a
// paused state" — with working pause/resume/delete so every styling the app
// has for a torrent client is on screen.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// dlClientSpeedBps is the simulated whole-client download rate while at least
// one SABnzbd item is actively downloading.
const dlClientSpeedBps int64 = 12_500_000

// dlETAHiddenSentinel mirrors qBittorrent's unknown-ETA sentinel: the real
// handler maps any eta >= 8640000 (or negative) to 0, and the app hides a 0.
// SABnzbd items keep computed ETAs; the constant documents the boundary.
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

// dlTorrent is one torrent the seeded qBittorrent client holds, in the
// shape /api/v2/torrents/info reports it: a 40-hex hash, a 0..1 progress,
// the raw state string, and a completion instant once finished. Torrents
// below progress 1 are the queue; the rest are the history.
type dlTorrent struct {
	Hash         string
	Name         string
	Size         int64
	Progress     float64 // 0..1
	DLSpeed      int64   // bytes/s while downloading
	State        string  // raw qBittorrent state
	Category     string
	CompletionOn time.Time // zero until complete
	// resumeState is what a paused torrent goes back to on resume: a queued
	// torrent stays queued, a stalled one stays stalled.
	resumeState string
}

// ─── State (guarded by dlMu) ────────────────────────────

var (
	dlMu           sync.Mutex
	dlPaused       bool
	dlItems        []*dlItem
	dlHistory      []dlHistoryItemJSON // newest first
	dlTorrents     []*dlTorrent
	dlLastSnapshot = map[string][]byte{} // instance id -> last emitted QueueView
	dlNextSpawnID  = 100
	dlSpawnCursor  int
	dlRecycleCount int
)

// dlRespawnPool keeps the simulated SABnzbd queue alive forever: when an item
// completes, the next pool entry is queued in its place. Lidarr's default
// SABnzbd category is literally "music", so the pool carries one.
var dlRespawnPool = []dlItem{
	{Name: "The.Cabinet.of.Dr.Caligari.1920.1080p.BluRay.x264-DEMO", SizeBytes: 3_650_000_000, Category: "movies"},
	{Name: "Frankenstein.Mary.Shelley.1818.ebook.EPUB-DEMO", SizeBytes: 3_400_000, Category: "books"},
	{Name: "The.Gold.Rush.1925.1080p.BluRay.x264-DEMO", SizeBytes: 4_050_000_000, Category: "movies"},
	{Name: "Scott.Joplin.Piano.Rolls.1916.FLAC-DEMO", SizeBytes: 210_000_000, Category: "music"},
	{Name: "Dracula.Bram.Stoker.1897.Audiobook.M4B-DEMO", SizeBytes: 410_000_000, Category: "books"},
	{Name: "Sunrise.A.Song.of.Two.Humans.1927.1080p.BluRay.x264-DEMO", SizeBytes: 4_480_000_000, Category: "movies"},
}

// dlQbitPausedStates mirrors isQbitPausedState in downloads/handler.go.
var dlQbitPausedStates = map[string]bool{
	"pausedDL": true, "pausedUP": true, "stoppedDL": true, "stoppedUP": true,
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
		// The music request mid-download in the Lidarr fixtures, seen from
		// the client's side (category "music" is Lidarr's SABnzbd default).
		{
			ID: "SABnzbd_nzo_odjb17", Name: "Original.Dixieland.Jass.Band.1917.Sessions.FLAC-DEMO",
			SizeBytes: 168_000_000, LeftBytes: 109_000_000, Status: "Downloading", Category: "music",
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
		{Name: "Bessie.Smith.Downhearted.Blues.1923.FLAC-DEMO", Status: "Completed", SizeBytes: 58_000_000, CompletedAt: at(360), Category: "music", Error: ""},
		{Name: "Battleship.Potemkin.1925.1080p.BluRay.x264-DEMO", Status: "Completed", SizeBytes: 3_340_000_000, CompletedAt: at(701), Category: "movies", Error: ""},
		{Name: "Treasure.Island.RL.Stevenson.1883.Audiobook.M4B-DEMO", Status: "Failed", SizeBytes: 460_000_000, CompletedAt: at(880), Category: "books", Error: "Unpacking failed, archive requires a password"},
		{Name: "Alices.Adventures.in.Wonderland.Lewis.Carroll.1865.ebook.EPUB-DEMO", Status: "Completed", SizeBytes: 1_900_000, CompletedAt: at(1_130), Category: "books", Error: ""},
		{Name: "The.Kid.1921.1080p.BluRay.x264-DEMO", Status: "Completed", SizeBytes: 3_010_000_000, CompletedAt: at(1_402), Category: "movies", Error: ""},
		{Name: "Safety.Last.1923.1080p.BluRay.x264-DEMO", Status: "Completed", SizeBytes: 3_270_000_000, CompletedAt: at(1_688), Category: "movies", Error: ""},
		{Name: "The.Strange.Case.of.Dr.Jekyll.and.Mr.Hyde.1886.ebook.EPUB-DEMO", Status: "Completed", SizeBytes: 1_400_000, CompletedAt: at(2_120), Category: "books", Error: ""},
		{Name: "The.Lost.World.1925.1080p.BluRay.x264-DEMO", Status: "Completed", SizeBytes: 3_760_000_000, CompletedAt: at(2_501), Category: "movies", Error: ""},
	}

	// qBittorrent: five incomplete torrents covering the raw states the app
	// styles (downloading, stalledDL, queuedDL, pausedDL) and five completed
	// ones for the history (uploading, stalledUP, pausedUP, missingFiles).
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	dlTorrents = []*dlTorrent{
		{Hash: "3f9a1c7e5b2d8046a9c3e1f7b5d2a8c4e6f0b1d3", Name: "The.General.1926.Remastered.1080p.BluRay.x265-DEMO",
			Size: 4_200_000_000, Progress: 0.375, DLSpeed: 3_100_000, State: "downloading", Category: "movies"},
		{Hash: "7b2e4d6f8a0c1e3b5d7f9a1c2e4b6d8f0a2c4e61", Name: "Sherlock.Holmes.Adventures.S04E09.1080p.WEB.H264-DEMO",
			Size: 1_400_000_000, Progress: 0.82, DLSpeed: 2_400_000, State: "downloading", Category: "tv"},
		{Hash: "c4d8e2f6a0b4c8d2e6f0a4b8c2d6e0f4a8b2c6d0", Name: "Metropolis.1927.The.Complete.Metropolis.2160p.BluRay-DEMO",
			Size: 38_000_000_000, Progress: 0.04, State: "stalledDL", Category: "movies"},
		{Hash: "e1f3a5b7c9d1e3f5a7b9c1d3e5f7a9b1c3d5e7f9", Name: "The.Cabinet.of.Dr.Caligari.1920.1080p.BluRay.x264-DEMO",
			Size: 3_600_000_000, Progress: 0, State: "queuedDL", Category: "movies"},
		{Hash: "a9b8c7d6e5f4a3b2c1d0e9f8a7b6c5d4e3f2a1b0", Name: "Nosferatu.1922.1080p.BluRay.x264-DEMO",
			Size: 5_100_000_000, Progress: 0.612, State: "pausedDL", Category: "movies", resumeState: "downloading"},

		{Hash: "0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e", Name: "A.Trip.to.the.Moon.1902.Restored.1080p-DEMO",
			Size: 890_000_000, Progress: 1, State: "uploading", Category: "movies", CompletionOn: ago(40 * time.Minute)},
		{Hash: "5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b", Name: "Classic.Science.Theater.S06E14.1080p.WEB-DEMO",
			Size: 1_200_000_000, Progress: 1, State: "stalledUP", Category: "tv", CompletionOn: ago(3 * time.Hour)},
		{Hash: "b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0", Name: "Plan.9.from.Outer.Space.1957.1080p.BluRay-DEMO",
			Size: 2_900_000_000, Progress: 1, State: "pausedUP", Category: "movies", CompletionOn: ago(9 * time.Hour)},
		{Hash: "6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f", Name: "Detour.1945.1080p.BluRay.x264-DEMO",
			Size: 2_700_000_000, Progress: 1, State: "missingFiles", Category: "movies", CompletionOn: ago(26 * time.Hour)},
		{Hash: "2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d", Name: "The.Phantom.of.the.Opera.1925.1080p-DEMO",
			Size: 4_100_000_000, Progress: 1, State: "uploading", Category: "movies", CompletionOn: ago(48 * time.Hour)},
	}
}

// ─── SABnzbd QueueView rendering ────────────────────────

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

// dlQueueViewLocked renders the current SABnzbd QueueView fields. items is
// never nil.
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

// ─── qBittorrent QueueView rendering ────────────────────

// dlQbitQueueViewLocked renders the qBittorrent QueueView with the real
// handler's mapping (downloads/handler.go): incomplete torrents only, id =
// hash, progress as a percentage, size_left derived from the fraction, the
// raw state as status, eta 0 when unknown, client speed = the sum of item
// speeds, and paused = "every queued torrent is in a paused state".
func dlQbitQueueViewLocked() (paused bool, speedBps int64, items []dlQueueItemJSON) {
	items = make([]dlQueueItemJSON, 0, len(dlTorrents))
	anyActive := false
	for _, t := range dlTorrents {
		if t.Progress >= 1 {
			continue // completed torrents are reported via /history
		}
		if !dlQbitPausedStates[t.State] {
			anyActive = true
		}
		sizeLeft := int64(float64(t.Size) * (1 - t.Progress))
		if sizeLeft < 0 {
			sizeLeft = 0
		}
		var speed int64
		if t.State == "downloading" {
			speed = t.DLSpeed
		}
		eta := 0
		if speed > 0 {
			eta = int(sizeLeft / speed)
		}
		if eta >= dlETAHiddenSentinel || eta < 0 {
			eta = 0
		}
		speedBps += speed
		items = append(items, dlQueueItemJSON{
			ID:            t.Hash,
			Name:          t.Name,
			SizeBytes:     t.Size,
			SizeLeftBytes: sizeLeft,
			Progress:      math.Round(t.Progress*1000) / 10, // one decimal place
			SpeedBps:      speed,
			EtaSeconds:    eta,
			Status:        t.State,
			Category:      t.Category,
		})
	}
	paused = len(items) > 0 && !anyActive
	return paused, speedBps, items
}

// dlQbitHistoryLocked renders the completed torrents, newest completion
// first, capped at limit. error carries the state only for the two failure
// states, exactly like the real mapping.
func dlQbitHistoryLocked(limit int) []dlHistoryItemJSON {
	completed := make([]*dlTorrent, 0, len(dlTorrents))
	for _, t := range dlTorrents {
		if t.Progress >= 1 {
			completed = append(completed, t)
		}
	}
	sort.SliceStable(completed, func(i, j int) bool {
		return completed[i].CompletionOn.After(completed[j].CompletionOn)
	})
	if len(completed) > limit {
		completed = completed[:limit]
	}
	out := make([]dlHistoryItemJSON, 0, len(completed))
	for _, t := range completed {
		completedAt := ""
		if !t.CompletionOn.IsZero() {
			completedAt = t.CompletionOn.UTC().Format(time.RFC3339)
		}
		errMsg := ""
		if t.State == "error" || t.State == "missingFiles" {
			errMsg = t.State
		}
		out = append(out, dlHistoryItemJSON{
			Name:        t.Name,
			Status:      t.State,
			SizeBytes:   t.Size,
			CompletedAt: completedAt,
			Category:    t.Category,
			Error:       errMsg,
		})
	}
	return out
}

// dlQbitPauseLocked parks one incomplete torrent in pausedDL, remembering
// where it resumes to.
func dlQbitPauseLocked(t *dlTorrent) {
	if t.Progress >= 1 || dlQbitPausedStates[t.State] {
		return
	}
	t.resumeState = t.State
	t.State = "pausedDL"
}

// dlQbitResumeLocked returns a paused torrent to its previous state: a
// queued torrent stays queued, a stalled one stays stalled, anything else
// downloads.
func dlQbitResumeLocked(t *dlTorrent) {
	if t.Progress >= 1 || !dlQbitPausedStates[t.State] {
		return
	}
	t.State = t.resumeState
	if t.State == "" || dlQbitPausedStates[t.State] {
		t.State = "downloading"
	}
}

// ─── Per-instance dispatch ──────────────────────────────

// dlQueueViewFor renders the QueueView for an instance. Only the two seeded
// clients carry simulated state; any other download client is empty.
func dlQueueViewFor(instanceID string) map[string]any {
	dlMu.Lock()
	defer dlMu.Unlock()
	return dlQueueViewForLocked(instanceID)
}

func dlQueueViewForLocked(instanceID string) map[string]any {
	var (
		paused bool
		speed  int64
		items  []dlQueueItemJSON
	)
	switch instanceID {
	case instSab:
		paused, speed, items = dlQueueViewLocked()
	case instQbittorrent:
		paused, speed, items = dlQbitQueueViewLocked()
	default:
		items = []dlQueueItemJSON{}
	}
	return map[string]any{"paused": paused, "speed_bps": speed, "items": items}
}

// dlEmitSnapshot recomputes each seeded client's QueueView and sends the
// downloads_queue event to admins — only when that client's view changed
// since its last emit (force overrides). The event carries instance_id; the
// app's All view fans the two clients in.
func dlEmitSnapshot(force bool) {
	for _, id := range []string{instSab, instQbittorrent} {
		dlMu.Lock()
		view := dlQueueViewForLocked(id)
		b, _ := json.Marshal(view)
		changed := !bytes.Equal(b, dlLastSnapshot[id])
		if changed || force {
			dlLastSnapshot[id] = b
		}
		dlMu.Unlock()
		if changed || force {
			view["instance_id"] = id
			wsToAdmins(evtDownloadsQueue, view)
		}
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

// dlAdvance moves active SABnzbd items forward by one interval's worth of
// bytes and recycles completed items into history; reports whether anything
// is animating. The qBittorrent set is deliberately static: its actions
// change it, the clock does not.
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
		// Move to history (newest first). Every 7th recycle fails so the
		// app's failure styling stays demonstrable at any uptime.
		dlRecycleCount++
		status, errText := "Completed", ""
		if dlRecycleCount%7 == 0 {
			status, errText = "Failed", "Unpacking failed, archive is damaged"
		}
		dlHistory = append([]dlHistoryItemJSON{{
			Name: done.Name, Status: status, SizeBytes: done.SizeBytes,
			CompletedAt: time.Now().UTC().Format(time.RFC3339),
			Category:    done.Category, Error: errText,
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
	writeJSON(w, http.StatusOK, dlQueueViewFor(inst.ID))
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
	dlMu.Lock()
	switch inst.ID {
	case instSab:
		if limit > len(dlHistory) {
			limit = len(dlHistory)
		}
		items = append(items, dlHistory[:limit]...)
	case instQbittorrent:
		items = dlQbitHistoryLocked(limit)
	}
	dlMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// dlSetClientPaused pauses or resumes a whole client: SABnzbd's global pause
// flag, or every incomplete qBittorrent torrent (qBittorrent has no client
// switch; "paused" is what the queue reads when nothing is active).
func dlSetClientPaused(instanceID string, paused bool) {
	dlMu.Lock()
	defer dlMu.Unlock()
	switch instanceID {
	case instSab:
		dlPaused = paused
	case instQbittorrent:
		for _, t := range dlTorrents {
			if paused {
				dlQbitPauseLocked(t)
			} else {
				dlQbitResumeLocked(t)
			}
		}
	}
}

func dlHandleClientPause(w http.ResponseWriter, r *http.Request) {
	inst := dlResolveClient(w, r)
	if inst == nil {
		return
	}
	dlSetClientPaused(inst.ID, true)
	w.WriteHeader(http.StatusNoContent)
	dlEmitSnapshot(false)
}

func dlHandleClientResume(w http.ResponseWriter, r *http.Request) {
	inst := dlResolveClient(w, r)
	if inst == nil {
		return
	}
	dlSetClientPaused(inst.ID, false)
	w.WriteHeader(http.StatusNoContent)
	dlEmitSnapshot(false)
}

// dlSetItemPaused pauses or resumes one queue item of a client.
func dlSetItemPaused(instanceID, itemID string, paused bool) {
	dlMu.Lock()
	defer dlMu.Unlock()
	switch instanceID {
	case instSab:
		status := "Downloading"
		if paused {
			status = "Paused"
		}
		for _, it := range dlItems {
			if it.ID == itemID {
				it.Status = status
				break
			}
		}
	case instQbittorrent:
		for _, t := range dlTorrents {
			if t.Hash != itemID {
				continue
			}
			if paused {
				dlQbitPauseLocked(t)
			} else {
				dlQbitResumeLocked(t)
			}
			break
		}
	}
}

func dlHandleItemPause(w http.ResponseWriter, r *http.Request) {
	inst := dlResolveClient(w, r)
	if inst == nil {
		return
	}
	itemID := chi.URLParam(r, "itemID")
	if itemID == "" {
		writeErr(w, http.StatusBadRequest, "item id is required")
		return
	}
	dlSetItemPaused(inst.ID, itemID, true)
	w.WriteHeader(http.StatusNoContent)
	dlEmitSnapshot(false)
}

func dlHandleItemResume(w http.ResponseWriter, r *http.Request) {
	inst := dlResolveClient(w, r)
	if inst == nil {
		return
	}
	itemID := chi.URLParam(r, "itemID")
	if itemID == "" {
		writeErr(w, http.StatusBadRequest, "item id is required")
		return
	}
	dlSetItemPaused(inst.ID, itemID, false)
	w.WriteHeader(http.StatusNoContent)
	dlEmitSnapshot(false)
}

func dlHandleItemDelete(w http.ResponseWriter, r *http.Request) {
	inst := dlResolveClient(w, r)
	if inst == nil {
		return
	}
	itemID := chi.URLParam(r, "itemID")
	if itemID == "" {
		writeErr(w, http.StatusBadRequest, "item id is required")
		return
	}
	_ = r.URL.Query().Get("deleteData") == "true" // demo has no files to delete
	dlMu.Lock()
	switch inst.ID {
	case instSab:
		for idx, it := range dlItems {
			if it.ID == itemID {
				dlItems = append(dlItems[:idx], dlItems[idx+1:]...)
				break
			}
		}
	case instQbittorrent:
		for idx, t := range dlTorrents {
			if t.Hash == itemID {
				dlTorrents = append(dlTorrents[:idx], dlTorrents[idx+1:]...)
				break
			}
		}
	}
	dlMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
	// The spec requires a fresh snapshot after a removal.
	dlEmitSnapshot(true)
}
