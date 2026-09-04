package request

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/lidarr"
)

// musicLibraryCacheTTL bounds how long a user's reduced Lidarr library digest
// is served from cache before a fresh GetAllAlbums. Short enough that a
// just-added album shows as owned soon, long enough to spare Lidarr a full
// library fetch on every search keystroke.
const musicLibraryCacheTTL = 15 * time.Second

// MusicLibraryTitle is one album in the owned-music digest. Music has no
// format axis, so ownership is a single monitored/downloaded pair rather than
// the per-format struct books carry.
type MusicLibraryTitle struct {
	Title  string `json:"title"`
	Artist string `json:"artist"`
	Year   int    `json:"year"`
	// ForeignAlbumID lets the app address the album: the MusicBrainz
	// release-group id requests and detail reads carry.
	ForeignAlbumID string `json:"foreign_album_id"`
	// Cover is a client-reachable cover reference: the record's MediaCover
	// path rewritten onto the /api/v1/mediacover proxy route, or the metadata
	// CDN copy. An arr-origin absolute URL is never passed through.
	Cover      string `json:"cover"`
	Monitored  bool   `json:"monitored"`
	Downloaded bool   `json:"downloaded"`
}

// MusicLibraryDigest is the lean per-album ownership digest the app uses to
// mark search results as already-owned. Titles is always a non-nil slice.
type MusicLibraryDigest struct {
	Titles []MusicLibraryTitle `json:"titles"`
}

// reduceMusicLibrary collapses the Lidarr album list into the digest: one
// entry per album record, with duplicate foreignAlbumIds aggregated the same
// way the live projection aggregates them (any file counts as downloaded, any
// monitored record as monitored).
func reduceMusicLibrary(albums []lidarr.Album) MusicLibraryDigest {
	byForeign := make(map[string]int)
	titles := make([]MusicLibraryTitle, 0, len(albums))

	for _, album := range albums {
		key := album.ForeignAlbumID
		if key != "" {
			if at, ok := byForeign[key]; ok {
				existing := &titles[at]
				existing.Monitored = existing.Monitored || album.Monitored
				existing.Downloaded = existing.Downloaded || album.Statistics.TrackFileCount > 0
				if existing.Cover == "" {
					existing.Cover = clientReachableAlbumCover(album)
				}
				continue
			}
		}
		entry := MusicLibraryTitle{
			Title:          album.Title,
			ForeignAlbumID: key,
			Cover:          clientReachableAlbumCover(album),
			Monitored:      album.Monitored,
			Downloaded:     album.Statistics.TrackFileCount > 0,
		}
		if album.Artist != nil {
			entry.Artist = strings.TrimSpace(album.Artist.ArtistName)
		}
		if album.ReleaseDate != nil {
			entry.Year = album.ReleaseDate.Year()
		}
		titles = append(titles, entry)
		if key != "" {
			byForeign[key] = len(titles) - 1
		}
	}

	return MusicLibraryDigest{Titles: titles}
}

// GetMusicLibraryDigestForInstance returns the reduced, cached Lidarr library
// digest for an explicitly selected authorized instance, or the user's
// effective instance when omitted. A user with no Lidarr access gets an empty
// (non-nil) digest rather than an error, so the app can degrade gracefully to
// "nothing owned".
func (s *Service) GetMusicLibraryDigestForInstance(userID int64, requestedInstanceID string) (*MusicLibraryDigest, error) {
	client, instanceID, err := s.resolveLidarr(userID, requestedInstanceID)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return &MusicLibraryDigest{Titles: []MusicLibraryTitle{}}, nil
	}

	cacheKey := "music-library:" + instanceID
	if s.libraryCache != nil {
		if data, ok := s.libraryCache.Get(cacheKey); ok {
			var digest MusicLibraryDigest
			if err := json.Unmarshal(data, &digest); err == nil {
				if digest.Titles == nil {
					digest.Titles = []MusicLibraryTitle{}
				}
				return &digest, nil
			}
		}
	}

	albums, err := client.GetAllAlbums()
	if err != nil {
		return nil, err
	}
	digest := reduceMusicLibrary(albums)

	if s.libraryCache != nil {
		if data, err := json.Marshal(digest); err == nil {
			s.libraryCache.Set(cacheKey, data, musicLibraryCacheTTL)
		}
	}
	return &digest, nil
}
