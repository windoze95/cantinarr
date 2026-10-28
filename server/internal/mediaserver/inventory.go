package mediaserver

import (
	"context"
	"time"
)

// InventoryProvider is optional: account management does not imply video
// inventory support. Library IDs here are native catalog identifiers, never
// the identifiers used by a provider's account-sharing API.
type InventoryProvider interface {
	InventoryLibraries(context.Context) ([]Library, error)
	// Empty remoteUserID selects the administrator's catalog for indexing.
	// A recipient lookup must use its live account/share and recheck access
	// before returning. A nil query enumerates the complete selected catalog.
	Inventory(context.Context, []string, string, *ItemQuery) ([]InventoryItem, error)
}

type InventoryVerifier interface {
	VerifyInventory(context.Context, []string, string, ItemQuery, []InventoryItem) ([]InventoryItem, error)
}

// InventoryCatalog enumerates only title identities, under live recipient
// access, for complete absence checks without traversing unrelated episodes.
type InventoryCatalog interface {
	InventoryCatalog(context.Context, []string, string) ([]InventoryItem, error)
}

// IncrementalInventory returns complete replacement records for changed
// movies/shows. Only full scans may infer removals from absent records.
type IncrementalInventory interface {
	InventorySince(context.Context, []string, time.Time) ([]InventoryItem, error)
}

// InventoryCopy preserves individual native versions/files without storing
// filesystem paths, playback URLs, or upstream response bodies.
type InventoryCopy struct {
	SourceID string `json:"source_id,omitempty"`
	PartID   string `json:"part_id,omitempty"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Playable bool   `json:"playable"`
	Is4K     bool   `json:"is_4k"`
}

type InventoryEpisode struct {
	Copies        []InventoryCopy `json:"copies,omitempty"`
	SeasonNumber  int             `json:"season_number"`
	EpisodeNumber int             `json:"episode_number"`
	TVDBID        int64           `json:"tvdb_id,omitempty"`
	TMDBID        int64           `json:"tmdb_id,omitempty"`
	SeriesTMDBID  int64           `json:"series_tmdb_id,omitempty"`
	SeriesTVDBID  int64           `json:"series_tvdb_id,omitempty"`
	Numbering     string          `json:"numbering"`
	InstanceID    string          `json:"instance_id,omitempty"`
	LibraryID     string          `json:"library_id"`
	ItemID        string          `json:"item_id"`
	Is4K          bool            `json:"is_4k"`
}

// InventoryItem holds metadata identity and actual playable-file evidence.
// Paths, tokens, media URLs, and arbitrary provider payloads are never stored.
type InventoryItem struct {
	Copies    []InventoryCopy    `json:"copies,omitempty"`
	LibraryID string             `json:"library_id"`
	ItemID    string             `json:"item_id"`
	MediaType string             `json:"media_type"`
	TMDBID    int64              `json:"tmdb_id,omitempty"`
	TVDBID    int64              `json:"tvdb_id,omitempty"`
	Movie     bool               `json:"movie"`
	Is4K      bool               `json:"is_4k"`
	Episodes  []InventoryEpisode `json:"episodes,omitempty"`
}

// InventoryMatches rejects conflicting IDs rather than letting one correct
// ID conceal another title. A missing ID may be supplied by the other source.
func InventoryMatches(item InventoryItem, q ItemQuery) bool {
	if item.MediaType != q.MediaType {
		return false
	}
	if q.TMDBID > 0 && item.TMDBID > 0 && q.TMDBID != item.TMDBID {
		return false
	}
	if q.MediaType == "tv" && q.TVDBID > 0 && item.TVDBID > 0 && q.TVDBID != item.TVDBID {
		return false
	}
	return q.TMDBID > 0 && q.TMDBID == item.TMDBID || q.MediaType == "tv" && q.TVDBID > 0 && q.TVDBID == item.TVDBID
}

func VideoLibrary(kind string) bool {
	switch kind {
	case "movie", "movies", "show", "tvshows", "mixed", "":
		return true
	}
	return false
}

// InventoryIdentityConflict identifies a plausible title with contradictory
// provider identities. It is unknown evidence, never proof of absence.
func InventoryIdentityConflict(item InventoryItem, q ItemQuery) bool {
	if item.MediaType != q.MediaType {
		return false
	}
	tmdbMatch := q.TMDBID > 0 && item.TMDBID == q.TMDBID
	tvdbMatch := q.MediaType == "tv" && q.TVDBID > 0 && item.TVDBID == q.TVDBID
	return tmdbMatch && q.MediaType == "tv" && q.TVDBID > 0 && item.TVDBID > 0 && item.TVDBID != q.TVDBID || tvdbMatch && q.TMDBID > 0 && item.TMDBID > 0 && item.TMDBID != q.TMDBID
}
