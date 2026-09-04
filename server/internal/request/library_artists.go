package request

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/lidarr"
)

// ErrMusicArtistNotFound means the requested foreignArtistId does not name an
// artist this Lidarr library holds. It is a 404, not a server fault: the
// artist page was opened for someone the library does not have.
var ErrMusicArtistNotFound = errors.New("artist is not in this music library")

// musicArtistsCacheTTL matches the books authors digest: the artists row
// answers "whose music do we have", which changes only when an artist is added
// or removed, and the Lidarr webhook drops this key on any library change.
const musicArtistsCacheTTL = 60 * time.Second

// musicArtistsMaxItems caps the browse row, applied after the requested sort
// for the same reason the authors row caps after sorting: capping first would
// silently omit everyone below the cut.
const musicArtistsMaxItems = 200

// The orders the artists row can be read in. An unknown value is treated as
// [ArtistSortAlbums] rather than rejected.
const (
	// ArtistSortAlbums leads with the artists the library actually holds.
	ArtistSortAlbums = "albums"
	// ArtistSortName is alphabetical by the name the card displays.
	ArtistSortName = "name"
	// ArtistSortAdded is newest arrival in the library first.
	ArtistSortAdded = "added"
)

func normalizeArtistSort(sort string) string {
	switch strings.ToLower(strings.TrimSpace(sort)) {
	case ArtistSortName:
		return ArtistSortName
	case ArtistSortAdded:
		return ArtistSortAdded
	default:
		return ArtistSortAlbums
	}
}

// LibraryArtist is one artist the Lidarr library holds albums for.
type LibraryArtist struct {
	// ForeignArtistID is the MusicBrainz artist id, the identity clients
	// address an artist by. Empty for a record Lidarr has not keyed yet, which
	// leaves the artist visible but not openable.
	ForeignArtistID string `json:"foreign_artist_id"`
	Name            string `json:"name"`
	// Image is a client-reachable artist image: the MediaCover path rewritten
	// onto the /api/v1/mediacover proxy route, or the metadata CDN copy. An
	// arr-origin absolute URL is never passed through.
	Image string `json:"image"`
	// AlbumCount is how many albums by this artist the library tracks.
	AlbumCount int `json:"album_count"`
	// AvailableCount is how many of those are complete on disk.
	AvailableCount int `json:"available_count"`
	// Added is when the artist entered the library, for the "date added"
	// order. Nil when the record carries no date, which sorts last rather than
	// as the beginning of time.
	Added *time.Time `json:"added,omitempty"`
}

// MusicArtistsDigest is the artists browse row's payload. Artists is always a
// non-nil slice.
type MusicArtistsDigest struct {
	Artists []LibraryArtist `json:"artists"`
	// Total is how many artists the library holds, before the row's cap, so a
	// truncated row can say so.
	Total int `json:"total"`
}

// MusicArtistDetail is one artist plus every album of theirs the library
// tracks, carrying the same ownership shape the music digest does.
type MusicArtistDetail struct {
	Artist LibraryArtist       `json:"artist"`
	Titles []MusicLibraryTitle `json:"titles"`
}

// GetLibraryArtistsForInstance returns the artists of the Lidarr instance this
// user may see, most-collected first. A user with no Lidarr grant gets an
// empty list rather than an error: the artists row is simply absent for them.
func (s *Service) GetLibraryArtistsForInstance(userID int64, requestedInstanceID, sort string) (*MusicArtistsDigest, error) {
	client, instanceID, err := s.resolveLidarr(userID, requestedInstanceID)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return &MusicArtistsDigest{Artists: []LibraryArtist{}}, nil
	}

	order := normalizeArtistSort(sort)

	// The cache holds every artist in no particular order: one entry serves
	// all three orders, so switching order in the app never refetches.
	cacheKey := "music-artists:" + instanceID
	if s.libraryCache != nil {
		if data, ok := s.libraryCache.Get(cacheKey); ok {
			var digest MusicArtistsDigest
			if err := json.Unmarshal(data, &digest); err == nil {
				return &MusicArtistsDigest{
					Artists: sortLibraryArtists(digest.Artists, order, musicArtistsMaxItems),
					Total:   len(digest.Artists),
				}, nil
			}
		}
	}

	artists, err := client.GetArtists()
	if err != nil {
		return nil, err
	}
	albums, err := client.GetAllAlbums()
	if err != nil {
		// Fail closed rather than shipping a row of artists with no counts:
		// "0 albums" on an artist whose shelf is full is a wrong answer.
		return nil, err
	}

	all := buildLibraryArtists(artists, albums)
	if s.libraryCache != nil {
		if data, err := json.Marshal(MusicArtistsDigest{Artists: all}); err == nil {
			s.libraryCache.Set(cacheKey, data, musicArtistsCacheTTL)
		}
	}
	return &MusicArtistsDigest{
		Artists: sortLibraryArtists(all, order, musicArtistsMaxItems),
		Total:   len(all),
	}, nil
}

// GetLibraryArtistDetailForInstance returns one artist and their library
// albums. Like the author page it is deliberately uncached: it is opened to
// decide what to request, so it must reflect an album requested seconds ago.
func (s *Service) GetLibraryArtistDetailForInstance(userID int64, foreignArtistID, requestedInstanceID string) (*MusicArtistDetail, error) {
	client, _, err := s.resolveLidarr(userID, requestedInstanceID)
	if err != nil {
		return nil, err
	}
	// No Lidarr access is not "artist missing" — the caller asked about a
	// library they cannot see at all, and saying "not found" would claim this
	// library was searched.
	if client == nil {
		return nil, ErrLidarrInstanceForbidden
	}
	wanted := strings.TrimSpace(foreignArtistID)
	if wanted == "" {
		return nil, ErrMusicArtistNotFound
	}

	artists, err := client.GetArtists()
	if err != nil {
		return nil, err
	}
	var match *lidarr.Artist
	for i := range artists {
		if strings.TrimSpace(artists[i].ForeignArtistID) == wanted {
			match = &artists[i]
			break
		}
	}
	if match == nil {
		return nil, ErrMusicArtistNotFound
	}

	albums, err := client.GetAlbumsForArtist(match.ID)
	if err != nil {
		return nil, err
	}
	titles := reduceMusicLibrary(albums).Titles
	// The reduction fills the artist name from each record's embedded artist
	// object, which the per-artist album list may not carry. We looked this
	// artist up by id, so stamp what we already know.
	stampArtistName(titles, match.ArtistName)
	sortArtistTitles(titles)

	artist := libraryArtistFrom(*match)
	artist.AlbumCount, artist.AvailableCount = countArtistAlbums(albums)
	return &MusicArtistDetail{Artist: artist, Titles: titles}, nil
}

// buildLibraryArtists joins the artist list to the library's albums so every
// count comes from the same records the ownership digest reduces. An artist
// with no album records is dropped: the row exists to be browsed into, and an
// artist with nothing behind them opens onto an empty page.
func buildLibraryArtists(artists []lidarr.Artist, albums []lidarr.Album) []LibraryArtist {
	byArtist := make(map[int][]lidarr.Album, len(artists))
	for _, album := range albums {
		if album.ArtistID <= 0 {
			continue
		}
		byArtist[album.ArtistID] = append(byArtist[album.ArtistID], album)
	}

	items := make([]LibraryArtist, 0, len(artists))
	for _, a := range artists {
		owned := byArtist[a.ID]
		if len(owned) == 0 {
			continue
		}
		entry := libraryArtistFrom(a)
		if entry.Name == "" {
			continue
		}
		entry.AlbumCount, entry.AvailableCount = countArtistAlbums(owned)
		items = append(items, entry)
	}

	return items
}

// sortLibraryArtists orders the row and then caps it. Every order ends in the
// same name tie-break so an unchanged library never reshuffles between fetches.
func sortLibraryArtists(items []LibraryArtist, order string, limit int) []LibraryArtist {
	if items == nil {
		return []LibraryArtist{}
	}
	sorted := make([]LibraryArtist, len(items))
	copy(sorted, items)

	byName := func(i, j int) bool {
		return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name)
	}
	switch normalizeArtistSort(order) {
	case ArtistSortName:
		sort.Slice(sorted, byName)
	case ArtistSortAdded:
		sort.Slice(sorted, func(i, j int) bool {
			a, b := sorted[i].Added, sorted[j].Added
			// A record with no date makes no recency claim, so it trails the
			// ones that do rather than leading as the beginning of time.
			if (a == nil) != (b == nil) {
				return b == nil
			}
			if a != nil && !a.Equal(*b) {
				return a.After(*b)
			}
			return byName(i, j)
		})
	default:
		sort.Slice(sorted, func(i, j int) bool {
			if sorted[i].AvailableCount != sorted[j].AvailableCount {
				return sorted[i].AvailableCount > sorted[j].AvailableCount
			}
			if sorted[i].AlbumCount != sorted[j].AlbumCount {
				return sorted[i].AlbumCount > sorted[j].AlbumCount
			}
			return byName(i, j)
		})
	}

	if limit > 0 && len(sorted) > limit {
		sorted = sorted[:limit]
	}
	return sorted
}

func libraryArtistFrom(a lidarr.Artist) LibraryArtist {
	return LibraryArtist{
		ForeignArtistID: strings.TrimSpace(a.ForeignArtistID),
		Name:            strings.TrimSpace(a.ArtistName),
		Image:           clientReachableArtistImage(a),
		Added:           a.Added,
	}
}

// countArtistAlbums reduces an artist's records to distinct albums and how
// many of them are complete on disk, using the same grouping as the ownership
// digest so the two never disagree about what "an album" is.
func countArtistAlbums(albums []lidarr.Album) (count, available int) {
	complete := make(map[string]bool, len(albums))
	for _, album := range albums {
		// Keyed like the digest: the foreignAlbumId when present, else a
		// per-record key so records without one never merge.
		key := album.ForeignAlbumID
		if key == "" {
			key = fmt.Sprintf("id:%d", album.ID)
		}
		if _, seen := complete[key]; !seen {
			complete[key] = false
		}
		if albumComplete(album) {
			complete[key] = true
		}
	}
	for _, done := range complete {
		count++
		if done {
			available++
		}
	}
	return count, available
}

// stampArtistName fills in the artist on titles the reduction left blank.
func stampArtistName(titles []MusicLibraryTitle, name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	for i := range titles {
		if strings.TrimSpace(titles[i].Artist) == "" {
			titles[i].Artist = name
		}
	}
}

// sortArtistTitles orders a discography newest-first. Undated records sort
// last rather than leading the page as year zero.
func sortArtistTitles(titles []MusicLibraryTitle) {
	sort.SliceStable(titles, func(i, j int) bool {
		a, b := titles[i], titles[j]
		if (a.Year > 0) != (b.Year > 0) {
			return a.Year > 0
		}
		if a.Year != b.Year {
			return a.Year > b.Year
		}
		return strings.ToLower(a.Title) < strings.ToLower(b.Title)
	})
}
