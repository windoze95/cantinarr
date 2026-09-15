package downloads

import (
	"sort"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/rutorrent"
)

// rutorrentBackend maps rTorrent's download list (read through ruTorrent)
// onto the unified shapes: unfinished downloads are the queue, finished ones
// the history. Item ids are info hashes; ruTorrent's label is the category.
// rTorrent reports no ETA, so it is derived from the bytes left and the rate.
type rutorrentBackend struct{ c *rutorrent.Client }

// rutorrentStatus names a download's state the way the app's status styling
// expects. rTorrent keeps three switches: open/closed, started/stopped
// (d.state), and active/paused; a hash check outranks them all.
func rutorrentStatus(t rutorrent.Torrent) string {
	switch {
	case t.Hashing != 0:
		return "checking"
	case !t.IsOpen || t.State == 0:
		return "stopped"
	case !t.IsActive:
		return "paused"
	case t.Complete:
		return "seeding"
	}
	return "downloading"
}

func (b rutorrentBackend) Snapshot() (*QueueView, error) {
	torrents, err := b.c.GetTorrents()
	if err != nil {
		return nil, err
	}
	rate, err := b.c.GlobalDownRate()
	if err != nil {
		return nil, err
	}
	view := &QueueView{
		SpeedBPS: rate,
		Items:    make([]QueueItem, 0),
	}
	anyActive := false
	for _, t := range torrents {
		if t.Complete {
			continue // finished downloads are reported via /history
		}
		status := rutorrentStatus(t)
		if status != "paused" && status != "stopped" {
			anyActive = true
		}
		progress := 0.0
		if t.SizeBytes > 0 {
			progress = float64(t.SizeBytes-t.LeftBytes) / float64(t.SizeBytes) * 100
		}
		var eta int64
		if t.DownRate > 0 {
			eta = t.LeftBytes / t.DownRate
		}
		view.Items = append(view.Items, QueueItem{
			ID:            t.Hash,
			Name:          t.Name,
			SizeBytes:     t.SizeBytes,
			SizeLeftBytes: t.LeftBytes,
			Progress:      progress,
			SpeedBPS:      t.DownRate,
			ETASeconds:    eta,
			Status:        status,
			Category:      t.Label,
		})
	}
	view.Paused = len(view.Items) > 0 && !anyActive
	return view, nil
}

// rutorrentCompletedAt is when a download finished: rTorrent stamps
// d.timestamp.finished on completion; a download that was already complete
// when loaded carries none, so ruTorrent's add time stands in.
func rutorrentCompletedAt(t rutorrent.Torrent) int64 {
	if t.FinishedAt > 0 {
		return t.FinishedAt
	}
	return t.AddedAt
}

// rutorrentError is the message worth showing for a finished download:
// rTorrent's d.message also carries tracker chatter ("Tracker: [Timeout was
// reached]") on perfectly healthy downloads, so only the rest counts.
func rutorrentError(t rutorrent.Torrent) string {
	msg := strings.TrimSpace(t.Message)
	if msg == "" || strings.HasPrefix(msg, "Tracker:") {
		return ""
	}
	return msg
}

func (b rutorrentBackend) History(limit int) ([]historyItem, error) {
	torrents, err := b.c.GetTorrents()
	if err != nil {
		return nil, err
	}
	completed := torrents[:0:0]
	for _, t := range torrents {
		if t.Complete {
			completed = append(completed, t)
		}
	}
	sort.SliceStable(completed, func(i, j int) bool {
		return rutorrentCompletedAt(completed[i]) > rutorrentCompletedAt(completed[j])
	})
	if len(completed) > limit {
		completed = completed[:limit]
	}
	items := make([]historyItem, 0, len(completed))
	for _, t := range completed {
		completedAt := ""
		if at := rutorrentCompletedAt(t); at > 0 {
			completedAt = time.Unix(at, 0).UTC().Format(time.RFC3339)
		}
		items = append(items, historyItem{
			Name:        t.Name,
			Status:      rutorrentStatus(t),
			SizeBytes:   t.SizeBytes,
			CompletedAt: completedAt,
			Category:    t.Label,
			Error:       rutorrentError(t),
		})
	}
	return items, nil
}

func (b rutorrentBackend) PauseItem(id string) error  { return b.c.Pause(id) }
func (b rutorrentBackend) ResumeItem(id string) error { return b.c.Resume(id) }
func (b rutorrentBackend) DeleteItem(id string, deleteData bool) error {
	if deleteData {
		return b.c.EraseWithData(id)
	}
	return b.c.Erase(id)
}

func (b rutorrentBackend) PauseAll() error  { return b.queueAction(b.c.Pause) }
func (b rutorrentBackend) ResumeAll() error { return b.queueAction(b.c.Resume) }

// queueAction applies a pause/resume command only to the unfinished
// downloads the unified queue shows, never to seeding ones (the same
// guardrail as Transmission's); nothing unfinished means nothing is sent.
func (b rutorrentBackend) queueAction(action func(...string) error) error {
	torrents, err := b.c.GetTorrents()
	if err != nil {
		return err
	}
	var hashes []string
	for _, t := range torrents {
		if !t.Complete {
			hashes = append(hashes, t.Hash)
		}
	}
	if len(hashes) == 0 {
		return nil
	}
	return action(hashes...)
}
