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
	TmdbID  int
	Title   string
	Upgrade bool
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
	upgrade := len(episodes) > 0
	for _, episode := range episodes {
		upgrade = upgrade && episode.Upgrade
	}
	return []ImportedTitle{{TmdbID: series.TmdbID, Title: series.Title, Upgrade: upgrade}}
}
