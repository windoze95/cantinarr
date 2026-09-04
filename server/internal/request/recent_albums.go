package request

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/lidarr"
)

// musicRecentCacheTTL is longer than the other music digests because building
// it fans out across artists. The Lidarr webhook drops this key on import, so
// an album that lands still surfaces immediately rather than waiting out the
// TTL.
const musicRecentCacheTTL = 60 * time.Second

// recentAlbumsDefaultLimit is what the Music row asks for when the caller does
// not say.
const recentAlbumsDefaultLimit = 20

// recentAlbumsMaxItems caps what is cached and therefore what any caller can
// ask for. This row answers "what landed lately"; a longer list is the
// library.
const recentAlbumsMaxItems = 50

// recentAlbumsFanOut bounds concurrent per-artist file reads against one arr.
const recentAlbumsFanOut = 4

// RecentAlbum is one library album that recently gained files.
type RecentAlbum struct {
	AlbumID        int       `json:"album_id"`
	ForeignAlbumID string    `json:"foreign_album_id"`
	Title          string    `json:"title"`
	Artist         string    `json:"artist"`
	Cover          string    `json:"cover"`
	ImportedAt     time.Time `json:"imported_at"`
}

// MusicRecentDigest is the newest-first list of albums that gained files.
type MusicRecentDigest struct {
	Items []RecentAlbum `json:"items"`
}

// GetRecentAlbumsForInstance returns the newest music imports for the Lidarr
// instance this user may see, newest first. A user with no Lidarr grant gets
// an empty list rather than an error: the music row is simply absent for them.
func (s *Service) GetRecentAlbumsForInstance(userID int64, requestedInstanceID string, limit int) (*MusicRecentDigest, error) {
	client, instanceID, err := s.resolveLidarr(userID, requestedInstanceID)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return &MusicRecentDigest{Items: []RecentAlbum{}}, nil
	}
	if limit <= 0 || limit > recentAlbumsMaxItems {
		limit = recentAlbumsMaxItems
	}

	cacheKey := "music-recent:" + instanceID
	if s.libraryCache != nil {
		if data, ok := s.libraryCache.Get(cacheKey); ok {
			var digest MusicRecentDigest
			if err := json.Unmarshal(data, &digest); err == nil {
				return &MusicRecentDigest{Items: takeRecentAlbums(digest.Items, limit)}, nil
			}
		}
	}

	albums, err := client.GetAllAlbums()
	if err != nil {
		return nil, err
	}
	filesByAlbum, err := recentAlbumFiles(client, albums)
	if err != nil {
		// Fail closed. A partial list would silently omit the very import the
		// user opened the tab to find, which is worse than showing no row.
		return nil, err
	}

	items := buildRecentAlbums(albums, filesByAlbum, recentAlbumsMaxItems)
	if s.libraryCache != nil {
		if data, err := json.Marshal(MusicRecentDigest{Items: items}); err == nil {
			s.libraryCache.Set(cacheKey, data, musicRecentCacheTTL)
		}
	}
	return &MusicRecentDigest{Items: takeRecentAlbums(items, limit)}, nil
}

func takeRecentAlbums(items []RecentAlbum, limit int) []RecentAlbum {
	if items == nil {
		return []RecentAlbum{}
	}
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

// recentAlbumFiles reads the file records for every artist owning something on
// disk. Lidarr has no library-wide trackfile read — its API requires an
// artist, album, or id filter — so this is a bounded fan-out, which is exactly
// why it lives on the server rather than in the app.
func recentAlbumFiles(client *lidarr.Client, albums []lidarr.Album) (map[int][]lidarr.TrackFile, error) {
	artistIDs := make([]int, 0, 8)
	seen := make(map[int]struct{})
	for _, album := range albums {
		if album.Statistics.TrackFileCount == 0 || album.ArtistID <= 0 {
			continue
		}
		if _, dup := seen[album.ArtistID]; dup {
			continue
		}
		seen[album.ArtistID] = struct{}{}
		artistIDs = append(artistIDs, album.ArtistID)
	}

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
		byAlbum  = make(map[int][]lidarr.TrackFile)
	)
	sem := make(chan struct{}, recentAlbumsFanOut)
	for _, artistID := range artistIDs {
		wg.Add(1)
		go func(artistID int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			files, err := client.GetTrackFilesForArtist(artistID)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			// Keyed on the album record, so a build that ignores the artist
			// filter and returns everything is harmless.
			for _, f := range files {
				byAlbum[f.AlbumID] = append(byAlbum[f.AlbumID], f)
			}
		}(artistID)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return byAlbum, nil
}

// buildRecentAlbums reduces the library to one card per album, ordered by when
// that album's files landed.
//
// Recency is the newest trackFile.dateAdded, never the album record's own
// release date: an album requested months ago and downloaded today belongs at
// the top. An album whose files carry no timestamp makes no recency claim and
// is left out, whatever its statistics say.
func buildRecentAlbums(albums []lidarr.Album, filesByAlbum map[int][]lidarr.TrackFile, limit int) []RecentAlbum {
	items := make([]RecentAlbum, 0, len(albums))
	for _, album := range albums {
		var newest time.Time
		for _, f := range filesByAlbum[album.ID] {
			if f.DateAdded == nil {
				continue
			}
			if f.DateAdded.After(newest) {
				newest = *f.DateAdded
			}
		}
		if newest.IsZero() {
			continue
		}
		item := RecentAlbum{
			AlbumID:        album.ID,
			ForeignAlbumID: album.ForeignAlbumID,
			Title:          album.Title,
			Cover:          clientReachableAlbumCover(album),
			ImportedAt:     newest,
		}
		if album.Artist != nil {
			item.Artist = strings.TrimSpace(album.Artist.ArtistName)
		}
		items = append(items, item)
	}

	sort.Slice(items, func(i, j int) bool {
		if !items[i].ImportedAt.Equal(items[j].ImportedAt) {
			return items[i].ImportedAt.After(items[j].ImportedAt)
		}
		// A freshly scanned library can stamp every file identically; without
		// a tie-break the row would reshuffle on every fetch.
		return items[i].AlbumID > items[j].AlbumID
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}
