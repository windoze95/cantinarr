package downloads

import (
	"sort"
	"time"

	"github.com/windoze95/cantinarr-server/internal/transmission"
)

// transmissionBackend maps Transmission's torrent list onto the unified
// shapes: incomplete torrents are the queue, completed ones the history. Item
// ids are torrent hashes; the first label is the category.
type transmissionBackend struct{ c *transmission.Client }

func (b transmissionBackend) Snapshot() (*QueueView, error) {
	torrents, err := b.c.GetTorrents()
	if err != nil {
		return nil, err
	}
	stats, err := b.c.GetSessionStats()
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

func (b transmissionBackend) History(limit int) ([]historyItem, error) {
	torrents, err := b.c.GetTorrents()
	if err != nil {
		return nil, err
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
	items := make([]historyItem, 0, len(completed))
	for _, t := range completed {
		completedAt := ""
		if t.DoneDate > 0 {
			completedAt = time.Unix(t.DoneDate, 0).UTC().Format(time.RFC3339)
		}
		errMsg := ""
		if t.Error != 0 {
			errMsg = t.ErrorString
		}
		items = append(items, historyItem{
			Name:        t.Name,
			Status:      transmission.StatusString(t.Status),
			SizeBytes:   t.TotalSize,
			CompletedAt: completedAt,
			Category:    transmissionCategory(t),
			Error:       errMsg,
		})
	}
	return items, nil
}

func (b transmissionBackend) PauseItem(id string) error {
	return b.c.StopTorrents([]string{id})
}

func (b transmissionBackend) ResumeItem(id string) error {
	return b.c.StartTorrents([]string{id})
}

func (b transmissionBackend) DeleteItem(id string, deleteData bool) error {
	return b.c.RemoveTorrents([]string{id}, deleteData)
}

func (b transmissionBackend) PauseAll() error  { return b.queueAction(b.c.StopTorrents) }
func (b transmissionBackend) ResumeAll() error { return b.queueAction(b.c.StartTorrents) }

// queueAction applies a start/stop action only to the torrents visible in
// the unified queue view (incomplete ones). A nil ids list would hit every
// torrent Transmission knows — silently stopping seeding on completed
// torrents, or resuming torrents the user deliberately stopped, none of which
// appear in the queue the user thinks they are acting on.
func (b transmissionBackend) queueAction(action func([]string) error) error {
	torrents, err := b.c.GetTorrents()
	if err != nil {
		return err
	}
	var hashes []string
	for _, t := range torrents {
		if t.PercentDone < 1 {
			hashes = append(hashes, t.HashString)
		}
	}
	if len(hashes) == 0 {
		return nil
	}
	return action(hashes)
}

// transmissionCategory maps a torrent's labels to the unified category field:
// the first label, or "" when unlabeled.
func transmissionCategory(t transmission.Torrent) string {
	if len(t.Labels) > 0 {
		return t.Labels[0]
	}
	return ""
}
