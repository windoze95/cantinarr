package downloads

import (
	"sort"
	"time"

	"github.com/windoze95/cantinarr-server/internal/deluge"
)

// delugeBackend maps Deluge's torrent list onto the unified shapes:
// unfinished torrents are the queue, finished ones the history. Item ids are
// torrent hashes; the Label plugin's label is the category. Status carries
// Deluge's own state names (Downloading, Paused, Queued, Checking, Seeding,
// Error, Allocating, Moving).
type delugeBackend struct{ c *deluge.Client }

// delugeFinished keys the queue/history split on is_finished alone: Deluge
// reports progress 100 for any torrent in the Error state whatever it has
// downloaded, so progress would hide every failed download in history.
func delugeFinished(t deluge.Torrent) bool {
	return t.IsFinished
}

func (b delugeBackend) Snapshot() (*QueueView, error) {
	torrents, err := b.c.GetTorrents()
	if err != nil {
		return nil, err
	}
	stats, err := b.c.GetSessionStatus()
	if err != nil {
		return nil, err
	}
	view := &QueueView{
		SpeedBPS: stats.DownloadRate,
		Items:    make([]QueueItem, 0),
	}
	anyActive := false
	for _, t := range torrents {
		if delugeFinished(t) {
			continue // finished torrents are reported via /history
		}
		// Queued is Deluge's own scheduling, not a user pause.
		if t.State != deluge.StatePaused {
			anyActive = true
		}
		left := t.TotalSize - t.TotalDone
		if left < 0 {
			left = 0
		}
		progress := t.Progress
		if t.State == deluge.StateError && t.TotalSize > 0 {
			// Deluge reports 100 for every errored torrent; the bytes say
			// how far it actually got.
			progress = float64(t.TotalDone) / float64(t.TotalSize) * 100
		}
		eta := t.ETA
		if eta < 0 {
			eta = 0
		}
		view.Items = append(view.Items, QueueItem{
			ID:            t.Hash,
			Name:          t.Name,
			SizeBytes:     t.TotalSize,
			SizeLeftBytes: left,
			Progress:      progress,
			SpeedBPS:      t.DownloadRate,
			ETASeconds:    eta,
			Status:        t.State,
			Category:      t.Label,
		})
	}
	view.Paused = len(view.Items) > 0 && !anyActive
	return view, nil
}

// delugeCompletedAt is when a finished torrent completed: Deluge 2.x reports
// completed_time; 1.3 does not, so the time it was added stands in.
func delugeCompletedAt(t deluge.Torrent) int64 {
	if t.CompletedTime > 0 {
		return t.CompletedTime
	}
	return int64(t.TimeAdded)
}

func (b delugeBackend) History(limit int) ([]historyItem, error) {
	torrents, err := b.c.GetTorrents()
	if err != nil {
		return nil, err
	}
	completed := torrents[:0:0]
	for _, t := range torrents {
		if delugeFinished(t) {
			completed = append(completed, t)
		}
	}
	sort.SliceStable(completed, func(i, j int) bool {
		return delugeCompletedAt(completed[i]) > delugeCompletedAt(completed[j])
	})
	if len(completed) > limit {
		completed = completed[:limit]
	}
	items := make([]historyItem, 0, len(completed))
	for _, t := range completed {
		completedAt := ""
		if at := delugeCompletedAt(t); at > 0 {
			completedAt = time.Unix(at, 0).UTC().Format(time.RFC3339)
		}
		errMsg := ""
		if t.State == deluge.StateError {
			errMsg = t.Message
		}
		items = append(items, historyItem{
			Name:        t.Name,
			Status:      t.State,
			SizeBytes:   t.TotalSize,
			CompletedAt: completedAt,
			Category:    t.Label,
			Error:       errMsg,
		})
	}
	return items, nil
}

func (b delugeBackend) PauseItem(id string) error  { return b.c.PauseTorrents([]string{id}) }
func (b delugeBackend) ResumeItem(id string) error { return b.c.ResumeTorrents([]string{id}) }
func (b delugeBackend) DeleteItem(id string, deleteData bool) error {
	return b.c.RemoveTorrent(id, deleteData)
}

func (b delugeBackend) PauseAll() error  { return b.queueAction(b.c.PauseTorrents) }
func (b delugeBackend) ResumeAll() error { return b.queueAction(b.c.ResumeTorrents) }

// queueAction applies a pause/resume action only to the unfinished torrents
// the unified queue shows, never to seeding ones (the same guardrail as
// Transmission's; an empty list is a no-op at the client too, because Deluge
// reads an empty list as every torrent).
func (b delugeBackend) queueAction(action func([]string) error) error {
	torrents, err := b.c.GetTorrents()
	if err != nil {
		return err
	}
	var hashes []string
	for _, t := range torrents {
		if !delugeFinished(t) {
			hashes = append(hashes, t.Hash)
		}
	}
	if len(hashes) == 0 {
		return nil
	}
	return action(hashes)
}
