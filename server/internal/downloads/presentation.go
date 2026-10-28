package downloads

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/mediametadata"
)

type PresentationResolver func(context.Context, int64, string, string, int, int, string) (*mediametadata.Presentation, error)

// The request service owns exact library/catalog identity resolution. Activity
// keeps its source snapshots independent of display language and user access.
func (h *Handler) SetPresentationResolver(resolve PresentationResolver, scope ...func(context.Context) context.Context) {
	h.presentation = resolve
	if len(scope) > 0 {
		h.presentationContext = scope[0]
	}
}

func (h *Handler) presentActivity(ctx context.Context, userID int64, view *Activity, language string) error {
	if h.presentation == nil || language == "" {
		return nil
	}
	if h.presentationContext != nil {
		ctx = h.presentationContext(ctx)
	}
	enrichment, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	type target struct{ group, child int }
	tasks := []target{}
	for i := range view.Groups {
		g := &view.Groups[i]
		if (g.MediaType != "movie" && g.MediaType != "tv") || g.recordID <= 0 {
			continue
		}
		tasks = append(tasks, target{i, -1})
		if g.MediaType == "tv" {
			for j := range g.Children {
				tasks = append(tasks, target{i, j})
			}
		}
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	failures := make([]error, len(tasks))
	for i, t := range tasks {
		wg.Add(1)
		go func(i int, t target) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-enrichment.Done():
				return
			}
			g := &view.Groups[t.group]
			episodeID := 0
			if t.child >= 0 {
				episodeID, _ = strconv.Atoi(g.Children[t.child].ID)
				if episodeID <= 0 {
					return
				}
			}
			p, err := h.presentation(enrichment, userID, g.MediaType, g.InstanceID, g.recordID, episodeID, language)
			failures[i] = err
			if err != nil || p == nil {
				return
			}
			if t.child < 0 {
				g.Presentation = p
			} else if p.EpisodeTitle != "" {
				g.Children[t.child].Presentation = &mediametadata.Presentation{Title: p.EpisodeTitle}
			}
		}(i, t)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for _, err := range failures {
		if err != nil {
			return err
		}
	}
	return nil
}
