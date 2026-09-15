package request

import (
	"context"
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
	RecordID        int    `json:"record_id"`
	ForeignArtistID string `json:"foreign_artist_id,omitempty"`
	ReleaseType     string `json:"release_type,omitempty"`
	Status          string `json:"status"`
	Title           string `json:"title"`
	Artist          string `json:"artist"`
	Year            int    `json:"year"`
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

// reduceMusicLibrary preserves each native album record, including records
// with the same release-group identity. Status is computed from current files.
func reduceMusicLibrary(albums []lidarr.Album) MusicLibraryDigest {
	titles := make([]MusicLibraryTitle, 0, len(albums))
	for _, album := range albums {
		key := album.ForeignAlbumID
		entry := MusicLibraryTitle{
			RecordID:       album.ID,
			ReleaseType:    album.AlbumType,
			Status:         StatusUnavailable,
			Title:          album.Title,
			ForeignAlbumID: key,
			Cover:          clientReachableAlbumCover(album),
			Monitored:      album.Monitored,
			Downloaded:     album.Statistics.TrackFileCount > 0,
		}
		if album.Artist != nil {
			entry.Artist = strings.TrimSpace(album.Artist.ArtistName)
			entry.ForeignArtistID = album.Artist.ForeignArtistID
		}
		if album.ReleaseDate != nil {
			entry.Year = album.ReleaseDate.Year()
		}
		if albumComplete(album) {
			entry.Status = StatusAvailable
		} else if album.Monitored {
			entry.Status = StatusRequested
		} else if album.Statistics.TrackFileCount > 0 {
			entry.Status = StatusPartial
		}
		titles = append(titles, entry)
	}

	return MusicLibraryDigest{Titles: titles}
}

// GetMusicLibraryDigestForInstance returns the reduced, cached Lidarr library
// digest for an explicitly selected authorized instance, or the user's
// effective instance when omitted. A user with no Lidarr access gets an empty
// (non-nil) digest rather than an error, so the app can degrade gracefully to
// "nothing owned".
func (s *Service) GetMusicLibraryDigestForInstance(userID int64, requestedInstanceID string, contexts ...context.Context) (*MusicLibraryDigest, error) {
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

	if len(contexts) > 0 {
		client = client.WithContext(contexts[0])
	}
	albums, err := client.GetAllAlbums()
	if err != nil {
		return nil, err
	}
	digest := reduceMusicLibrary(albums)
	if _, _, err = s.resolveLidarr(userID, instanceID); err != nil {
		return nil, err
	}

	if s.libraryCache != nil {
		if data, err := json.Marshal(digest); err == nil {
			s.libraryCache.Set(cacheKey, data, musicLibraryCacheTTL)
		}
	}
	return &digest, nil
}
