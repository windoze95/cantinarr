package sonarr

import "time"

// ImportedEpisode is evidence from a Download callback or import history.
// SeasonNumber <= 0 means there is no positive-season evidence; it must never
// select an anthology story by looking at other files already in the library.
type ImportedEpisode struct {
	EpisodeID    int
	SeasonNumber int
	Upgrade      bool
	ImportedAt   time.Time
}

// ImportedTitle is one notification destination, after season corrections and
// per-title upgrade classification. The caller supplies the importing instance.
type ImportedTitle struct {
	EpisodeIDs        []int
	UpgradeEpisodeIDs []int
	TmdbID            int
	Title             string
	Upgrade           bool
}

// ImportResolver is implemented by request.Service, the owner of TV matching.
// A partial result may accompany an error: verified sibling stories still
// announce, while the caller logs the rejected scopes.
type ImportResolver interface {
	HasTVImportCorrection(series *Series) (bool, error)
	ResolveTVImports(client *Client, series *Series, episodes []ImportedEpisode) ([]ImportedTitle, error)
}

// UncorrectedImportTitle preserves ordinary series identity and treats a mixed
// batch as new content. A nil episode slice represents the legacy queue witness.
func UncorrectedImportTitle(series *Series, episodes []ImportedEpisode) []ImportedTitle {
	var ids, upgrades []int
	upgrade := len(episodes) > 0
	for _, episode := range episodes {
		upgrade = upgrade && episode.Upgrade
		if episode.EpisodeID > 0 {
			if episode.Upgrade {
				upgrades = append(upgrades, episode.EpisodeID)
			} else {
				ids = append(ids, episode.EpisodeID)
			}
		}
	}
	return []ImportedTitle{{TmdbID: series.TmdbID, Title: series.Title, Upgrade: upgrade, EpisodeIDs: ids, UpgradeEpisodeIDs: upgrades}}
}

// NotifyImportedEpisodes preserves episode evidence for sinks that can target
// saved requests. Legacy sinks still receive the established title event.
func NotifyImportedEpisodes(sink interface{ NotifyNewEpisode(string, int, string) }, title ImportedTitle, instanceID string) {
	if scoped, ok := sink.(interface {
		RecordEpisodeUpgrades(string, int, string, []int)
	}); ok {
		scoped.RecordEpisodeUpgrades(title.Title, title.TmdbID, instanceID, title.UpgradeEpisodeIDs)
	}
	if scoped, ok := sink.(interface {
		NotifyNewEpisodes(string, int, string, []int)
	}); ok {
		scoped.NotifyNewEpisodes(title.Title, title.TmdbID, instanceID, title.EpisodeIDs)
	} else {
		sink.NotifyNewEpisode(title.Title, title.TmdbID, instanceID)
	}
}

func NotifyImportedUpgrade(sink interface{ NotifyUpgradedEpisode(string, int, string) }, title ImportedTitle, instanceID string) {
	if scoped, ok := sink.(interface {
		NotifyUpgradedEpisodes(string, int, string, []int)
	}); ok && len(title.UpgradeEpisodeIDs) > 0 {
		scoped.NotifyUpgradedEpisodes(title.Title, title.TmdbID, instanceID, title.UpgradeEpisodeIDs)
	} else {
		sink.NotifyUpgradedEpisode(title.Title, title.TmdbID, instanceID)
	}
}
