package downloads

import (
	"time"

	"github.com/windoze95/cantinarr-server/internal/sabnzbd"
)

// sabnzbdBackend maps SABnzbd's queue and history onto the unified shapes.
// Item ids are SABnzbd nzo_ids.
type sabnzbdBackend struct{ c *sabnzbd.Client }

func (b sabnzbdBackend) Snapshot() (*QueueView, error) {
	queue, err := b.c.GetQueue()
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
}

func (b sabnzbdBackend) History(limit int) ([]historyItem, error) {
	slots, err := b.c.GetHistory(limit)
	if err != nil {
		return nil, err
	}
	items := make([]historyItem, 0, len(slots))
	for _, slot := range slots {
		completedAt := ""
		if slot.Completed > 0 {
			completedAt = time.Unix(slot.Completed, 0).UTC().Format(time.RFC3339)
		}
		items = append(items, historyItem{
			Name:        slot.Name,
			Status:      slot.Status,
			SizeBytes:   int64(slot.Bytes),
			CompletedAt: completedAt,
			Category:    slot.Category,
			Error:       slot.FailMessage,
		})
	}
	return items, nil
}

func (b sabnzbdBackend) PauseItem(id string) error  { return b.c.PauseItem(id) }
func (b sabnzbdBackend) ResumeItem(id string) error { return b.c.ResumeItem(id) }
func (b sabnzbdBackend) DeleteItem(id string, deleteData bool) error {
	return b.c.DeleteItem(id, deleteData)
}
func (b sabnzbdBackend) PauseAll() error  { return b.c.PauseQueue() }
func (b sabnzbdBackend) ResumeAll() error { return b.c.ResumeQueue() }
