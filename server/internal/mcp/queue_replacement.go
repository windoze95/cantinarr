package mcp

import (
	"fmt"
	"time"

	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// ResolveSonarrQueueAction is a read-only preflight shared by proposal creation
// and the final mutation. Only search permission can shrink; blocklist_only is
// never upgraded to a search when an episode airs while approval is pending.
func ResolveSonarrQueueAction(client *sonarr.Client, queueID int, action string) (*sonarr.DetailedQueueItem, string, bool, error) {
	items, err := client.GetQueueDetailed()
	if err != nil {
		return nil, action, false, &MutationNotStartedError{Detail: err.Error()}
	}
	var target *sonarr.DetailedQueueItem
	for i := range items {
		if items[i].ID == queueID {
			target = &items[i]
			break
		}
	}
	if target == nil {
		return nil, action, false, &MutationNotStartedError{Detail: fmt.Sprintf("no TV queue item with id %d", queueID)}
	}
	if action != "blocklist_search" && action != "blocklist_only" {
		return target, action, false, nil
	}
	// A blocklist-only operation already suppresses search. Still read known
	// episode scopes so its result can explain unaired cleanup accurately.
	// Unmatched downloads have no episode to inspect and can still be removed
	// explicitly without replacement; they cannot opt into a search blindly.
	if action == "blocklist_only" && (target.SeriesID <= 0 || target.EpisodeID <= 0) {
		return target, action, false, nil
	}
	episodes, err := client.QueueReplacementEpisodes(items, *target)
	if err != nil {
		return nil, action, false, &MutationNotStartedError{Detail: fmt.Sprintf("could not verify replacement episode dates: %v", err)}
	}
	unaired := sonarr.HasUnairedEpisode(episodes, time.Now().UTC())
	if unaired {
		action = "blocklist_only"
	}
	return target, action, unaired, nil
}
