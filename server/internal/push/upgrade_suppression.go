package push

import (
	"fmt"
	"strconv"

	"github.com/windoze95/cantinarr-server/internal/contentnotify"
)

func suppressionKey(category, mediaType, id, title string) string {
	return fmt.Sprintf("suppressed:%s|%s|%s|%s", category, mediaType, id, title)
}

func (n *Notifier) recordUpgradeSuppression(category, mediaType, id, title string) {
	if n == nil || n.db == nil {
		return
	}
	_, err := n.db.Exec(`INSERT INTO content_alert_claims(alert_key,storm_scope) VALUES(?,'none') ON CONFLICT(alert_key) DO UPDATE SET claimed_at=CURRENT_TIMESTAMP,storm_scope='none'`, suppressionKey(category, mediaType, id, title))
	if err != nil {
		n.logger.Error("push: record explicit upgrade evidence", "err", err)
	}
}

func (n *Notifier) upgradeSuppressed(category, mediaType, id, title string) bool {
	var count int
	err := n.db.QueryRow(`SELECT COUNT(*) FROM content_alert_claims WHERE alert_key=? AND claimed_at>datetime('now',?)`, suppressionKey(category, mediaType, id, title), sqliteOffset(-contentAlertWindow)).Scan(&count)
	return err != nil || count > 0
}

func (n *Notifier) RecordEpisodeUpgrades(title string, tmdbID int, instanceID string, episodes []int) {
	for _, id := range episodes {
		if id > 0 {
			n.recordUpgradeSuppression(CategoryNewEpisode, "tv", contentClaimID(instanceID, fmt.Sprintf("%d:episode:%d", tmdbID, id)), title)
		}
	}
}

func (n *Notifier) NotifyUpgradedEpisodes(title string, tmdbID int, instanceID string, episodes []int) {
	n.RecordEpisodeUpgrades(title, tmdbID, instanceID, episodes)
	n.notifyUpgradedContent(CategoryNewEpisode, "tv", "Episode upgraded", "An episode of "+title+" was replaced with a better version", title, tmdbID, instanceID, true)
}

func (n *Notifier) withoutUpgradedEpisodes(event contentnotify.Import, category, identity, title string) contentnotify.Import {
	if event.MediaType != "tv" {
		return event
	}
	ids := []int{}
	for _, id := range event.EpisodeIDs {
		if !n.upgradeSuppressed(category, "tv", contentClaimID(event.InstanceID, strconv.Itoa(event.TmdbID)+":episode:"+strconv.Itoa(id)), title) {
			ids = append(ids, id)
		}
	}
	event.EpisodeIDs = ids
	return event
}
