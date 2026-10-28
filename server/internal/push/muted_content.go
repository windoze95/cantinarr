package push

import (
	"fmt"
	"strconv"

	"github.com/windoze95/cantinarr-server/internal/contentnotify"
)

func mutedImportKey(user int64, event contentnotify.Import, category string, episode int) string {
	identity := event.ForeignID
	if event.MediaType == "movie" || event.MediaType == "tv" {
		identity = strconv.Itoa(event.TmdbID)
	}
	return fmt.Sprintf("muted:%s|%s|%s|%s|%s|%d|%d", category, event.MediaType, event.InstanceID, identity, event.Format, user, episode)
}

// Muted witnesses stay muted when the other import witness arrives after a
// category/master is enabled. TV receipts name exact episodes, so a different
// later episode remains eligible and mixed batches can be filtered per user.
func (n *Notifier) recordMutedImport(user int64, event contentnotify.Import, category string) {
	units := []int{0}
	if event.MediaType == "tv" {
		units = event.EpisodeIDs
	}
	for _, unit := range units {
		if _, err := n.db.Exec(`INSERT INTO content_alert_claims(alert_key,storm_scope) VALUES(?,'none') ON CONFLICT(alert_key) DO UPDATE SET claimed_at=CURRENT_TIMESTAMP,storm_scope='none'`, mutedImportKey(user, event, category, unit)); err != nil {
			n.logger.Error("push: record muted import", "err", err)
		}
	}
}

func (n *Notifier) importMuted(user int64, event contentnotify.Import, category string, episode int) bool {
	var count int
	err := n.db.QueryRow(`SELECT COUNT(*) FROM content_alert_claims WHERE alert_key=? AND claimed_at>datetime('now',?)`, mutedImportKey(user, event, category, episode), sqliteOffset(-contentAlertWindow)).Scan(&count)
	return err != nil || count > 0
}

func (n *Notifier) unmutedEpisodes(user int64, event contentnotify.Import, category string) []int {
	ids := []int{}
	for _, episode := range event.EpisodeIDs {
		if !n.importMuted(user, event, category, episode) {
			ids = append(ids, episode)
		}
	}
	return ids
}
