package downloads

import (
	"sort"
	"time"

	"github.com/windoze95/cantinarr-server/internal/qbittorrent"
)

// qBittorrent reports this ETA when it is unknown/infinite; normalize to 0.
const qbitETAInfinity = 8640000

// qbittorrentBackend maps qBittorrent's torrent list onto the unified shapes:
// incomplete torrents are the queue, completed ones the history. Item ids are
// torrent hashes.
type qbittorrentBackend struct{ c *qbittorrent.Client }

func (b qbittorrentBackend) Snapshot() (*QueueView, error) {
	torrents, err := b.c.GetTorrents()
	if err != nil {
		return nil, err
	}
	info, err := b.c.GetTransferInfo()
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
}

func (b qbittorrentBackend) History(limit int) ([]historyItem, error) {
	torrents, err := b.c.GetTorrents()
	if err != nil {
		return nil, err
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
	items := make([]historyItem, 0, len(completed))
	for _, t := range completed {
		completedAt := ""
		if t.CompletionOn > 0 {
			completedAt = time.Unix(t.CompletionOn, 0).UTC().Format(time.RFC3339)
		}
		errMsg := ""
		if t.State == "error" || t.State == "missingFiles" {
			errMsg = t.State
		}
		items = append(items, historyItem{
			Name:        t.Name,
			Status:      t.State,
			SizeBytes:   t.Size,
			CompletedAt: completedAt,
			Category:    t.Category,
			Error:       errMsg,
		})
	}
	return items, nil
}

func (b qbittorrentBackend) PauseItem(id string) error  { return b.c.PauseTorrents(id) }
func (b qbittorrentBackend) ResumeItem(id string) error { return b.c.ResumeTorrents(id) }
func (b qbittorrentBackend) DeleteItem(id string, deleteData bool) error {
	return b.c.Delete(id, deleteData)
}
func (b qbittorrentBackend) PauseAll() error  { return b.c.PauseTorrents("all") }
func (b qbittorrentBackend) ResumeAll() error { return b.c.ResumeTorrents("all") }

func isQbitPausedState(state string) bool {
	switch state {
	case "pausedDL", "pausedUP", "stoppedDL", "stoppedUP":
		return true
	}
	return false
}
