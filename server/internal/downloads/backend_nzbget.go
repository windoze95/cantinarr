package downloads

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/nzbget"
)

// nzbgetBackend maps NZBGet's groups and history onto the unified shapes.
// Item ids are NZBIDs; a non-numeric id is a client error, not an upstream
// one. NZBGet has no delete-files call, so DeleteItem ignores deleteData and
// the app's remove dialog says the files stay on disk.
type nzbgetBackend struct{ c *nzbget.Client }

func (b nzbgetBackend) Snapshot() (*QueueView, error) {
	groups, err := b.c.ListGroups()
	if err != nil {
		return nil, err
	}
	status, err := b.c.GetStatus()
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
}

func (b nzbgetBackend) History(limit int) ([]historyItem, error) {
	entries, err := b.c.GetHistory()
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].HistoryTime > entries[j].HistoryTime
	})
	if len(entries) > limit {
		entries = entries[:limit]
	}
	items := make([]historyItem, 0, len(entries))
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
		items = append(items, historyItem{
			Name:        e.Name,
			Status:      status,
			SizeBytes:   e.SizeBytes(),
			CompletedAt: completedAt,
			Category:    e.Category,
			Error:       errMsg,
		})
	}
	return items, nil
}

func nzbID(id string) (int, error) {
	n, err := strconv.Atoi(id)
	if err != nil {
		return 0, &badItemIDError{"item id must be a numeric NZBGet id"}
	}
	return n, nil
}

func (b nzbgetBackend) PauseItem(id string) error {
	n, err := nzbID(id)
	if err != nil {
		return err
	}
	return b.c.PauseGroups([]int{n})
}

func (b nzbgetBackend) ResumeItem(id string) error {
	n, err := nzbID(id)
	if err != nil {
		return err
	}
	return b.c.ResumeGroups([]int{n})
}

func (b nzbgetBackend) DeleteItem(id string, _ bool) error {
	n, err := nzbID(id)
	if err != nil {
		return err
	}
	return b.c.DeleteGroups([]int{n})
}

func (b nzbgetBackend) PauseAll() error  { return b.c.PauseDownload() }
func (b nzbgetBackend) ResumeAll() error { return b.c.ResumeDownload() }
