package tmdb

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/windoze95/cantinarr-server/internal/trakt"
)

// ServiceClients provides lazy access to TMDB and Trakt clients.
type ServiceClients interface {
	TMDB() *Client
	Trakt() *trakt.Client
}

type Bridge struct {
	clients ServiceClients
	db      *sql.DB
}

func NewBridge(clients ServiceClients, db *sql.DB) *Bridge {
	return &Bridge{clients: clients, db: db}
}

type BridgeResult struct {
	TVDBID int
	IMDBID string
}

func (b *Bridge) ResolveTVDBID(tmdbID int) (*BridgeResult, error) {
	// 1. Check cache first
	var tvdbID sql.NullInt64
	var imdbID sql.NullString
	var cachedAt time.Time
	err := b.db.QueryRow(
		"SELECT tvdb_id, imdb_id, cached_at FROM tmdb_tvdb_cache WHERE tmdb_id = ?", tmdbID,
	).Scan(&tvdbID, &imdbID, &cachedAt)
	if err == nil {
		// Check TTL (30 days)
		if time.Since(cachedAt) < 30*24*time.Hour && tvdbID.Valid {
			result := &BridgeResult{TVDBID: int(tvdbID.Int64)}
			if imdbID.Valid {
				result.IMDBID = imdbID.String
			}
			return result, nil
		}
		// Expired, delete and re-resolve
		if time.Since(cachedAt) >= 30*24*time.Hour {
			b.db.Exec("DELETE FROM tmdb_tvdb_cache WHERE tmdb_id = ?", tmdbID)
		}
	}

	// 2. Try TMDB external IDs
	tmdbClient := b.clients.TMDB()
	if tmdbClient == nil {
		return nil, fmt.Errorf("TMDB client not configured")
	}
	ids, err := tmdbClient.GetTVExternalIDs(tmdbID)
	if err == nil && ids.TVDBID != nil && *ids.TVDBID != 0 {
		result := &BridgeResult{TVDBID: *ids.TVDBID}
		if ids.IMDBID != nil {
			result.IMDBID = *ids.IMDBID
		}
		b.cacheResult(tmdbID, result)
		return result, nil
	}

	// 3. Try Trakt as fallback
	if traktClient := b.clients.Trakt(); traktClient != nil {
		traktResult, err := traktClient.SearchByTMDB(tmdbID, "show")
		if err == nil && traktResult != nil && traktResult.TVDBID != 0 {
			result := &BridgeResult{
				TVDBID: traktResult.TVDBID,
				IMDBID: traktResult.IMDBID,
			}
			b.cacheResult(tmdbID, result)
			return result, nil
		}
	}

	return nil, fmt.Errorf("could not resolve TVDB ID for TMDB ID %d", tmdbID)
}

func (b *Bridge) cacheResult(tmdbID int, result *BridgeResult) {
	b.db.Exec(
		"INSERT OR REPLACE INTO tmdb_tvdb_cache (tmdb_id, tvdb_id, imdb_id, cached_at) VALUES (?, ?, ?, ?)",
		tmdbID, result.TVDBID, result.IMDBID, time.Now(),
	)
}
