package request

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/lidarr"
)

func timePtr(t time.Time) *time.Time { return &t }

func TestReduceMusicLibraryAggregatesDuplicateForeignIDs(t *testing.T) {
	release := time.Date(1994, 5, 10, 0, 0, 0, 0, time.UTC)
	albums := []lidarr.Album{
		{ID: 1, Title: "Blue Album", ForeignAlbumID: "mb-1", Monitored: false, ReleaseDate: timePtr(release),
			Artist: &lidarr.Artist{ArtistName: "Weezer"}},
		{ID: 2, Title: "Blue Album", ForeignAlbumID: "mb-1", Monitored: true,
			Statistics: lidarr.AlbumStatistics{TrackFileCount: 10, TrackCount: 10}},
		{ID: 3, Title: "Keyless", ForeignAlbumID: ""},
		{ID: 4, Title: "Keyless", ForeignAlbumID: ""},
	}
	digest := reduceMusicLibrary(albums)
	if len(digest.Titles) != 3 {
		t.Fatalf("titles = %+v", digest.Titles)
	}
	merged := digest.Titles[0]
	if merged.ForeignAlbumID != "mb-1" || !merged.Monitored || !merged.Downloaded {
		t.Fatalf("merged = %+v", merged)
	}
	if merged.Artist != "Weezer" || merged.Year != 1994 {
		t.Fatalf("merged metadata = %+v", merged)
	}
	// Records without a foreignAlbumId never merge — two keyless records stay
	// two rows for the user to decide about.
	if digest.Titles[1].Title != "Keyless" || digest.Titles[2].Title != "Keyless" {
		t.Fatalf("keyless rows = %+v", digest.Titles[1:])
	}
}

// TestLidarrCoverRewriting pins the cover-URL contract: Lidarr's root-level
// MediaCover shapes are rebuilt onto the /api/v1/mediacover routes the proxy
// allowlists (artist and album subtrees differ), remote CDN copies pass, and
// arr-origin absolutes never leak.
func TestLidarrCoverRewriting(t *testing.T) {
	album := lidarr.Album{Images: []lidarr.Image{{CoverType: "cover", URL: "/MediaCover/Albums/9/cover.jpg?lastWrite=123"}}}
	if got := clientReachableAlbumCover(album); got != "/mediacover/album/9/cover.jpg?lastWrite=123" {
		t.Fatalf("album cover = %q", got)
	}
	artist := lidarr.Artist{Images: []lidarr.Image{{CoverType: "poster", URL: "/MediaCover/7/poster.jpg"}}}
	if got := clientReachableArtistImage(artist); got != "/mediacover/artist/7/poster.jpg" {
		t.Fatalf("artist image = %q", got)
	}
	remote := lidarr.Album{Images: []lidarr.Image{{CoverType: "cover", URL: "https://lidarr.internal:8686/MediaCover/Albums/9/cover.jpg", RemoteURL: "https://cdn.example/cover.jpg"}}}
	if got := clientReachableAlbumCover(remote); got != "https://cdn.example/cover.jpg" {
		t.Fatalf("remote fallback = %q", got)
	}
	leak := lidarr.Album{Images: []lidarr.Image{{CoverType: "cover", URL: "https://lidarr.internal:8686/MediaCover/Albums/9/cover.jpg"}}}
	if got := clientReachableAlbumCover(leak); got != "" {
		t.Fatalf("arr-origin absolute leaked as %q", got)
	}
	unknownShape := lidarr.Album{Images: []lidarr.Image{{CoverType: "cover", URL: "/UnknownTree/9/cover.jpg"}}}
	if got := clientReachableAlbumCover(unknownShape); got != "" {
		t.Fatalf("unknown relative shape passed through as %q", got)
	}
}

func TestMusicLibraryDigestEmptyWithoutGrant(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(server.Close)
	svc, _, _ := newLidarrMusicTestService(t, server.URL)

	res, err := svc.db.Exec("INSERT INTO users (username, password_hash, role) VALUES ('stranger', '', 'user')")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	strangerID, _ := res.LastInsertId()
	digest, err := svc.GetMusicLibraryDigestForInstance(strangerID, "")
	if err != nil {
		t.Fatalf("digest error = %v", err)
	}
	if digest.Titles == nil || len(digest.Titles) != 0 {
		t.Fatalf("ungranted digest = %+v, want empty non-nil", digest)
	}
}

func TestBuildRecentAlbumsOrdersByNewestFile(t *testing.T) {
	old := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	fresh := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	albums := []lidarr.Album{
		{ID: 1, Title: "Old", ForeignAlbumID: "mb-old", Artist: &lidarr.Artist{ArtistName: "A"}},
		{ID: 2, Title: "Fresh", ForeignAlbumID: "mb-fresh", Artist: &lidarr.Artist{ArtistName: "B"}},
		{ID: 3, Title: "Undated", ForeignAlbumID: "mb-undated"},
	}
	files := map[int][]lidarr.TrackFile{
		1: {{ID: 10, AlbumID: 1, DateAdded: timePtr(old)}},
		2: {{ID: 11, AlbumID: 2, DateAdded: timePtr(old)}, {ID: 12, AlbumID: 2, DateAdded: timePtr(fresh)}},
		3: {{ID: 13, AlbumID: 3}},
	}
	items := buildRecentAlbums(albums, files, 10)
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
	if items[0].ForeignAlbumID != "mb-fresh" || !items[0].ImportedAt.Equal(fresh) {
		t.Fatalf("newest first = %+v", items[0])
	}
	// A file with no timestamp makes no recency claim; its album is left out
	// rather than sorted as the beginning of time.
	for _, item := range items {
		if item.ForeignAlbumID == "mb-undated" {
			t.Fatalf("undated album surfaced: %+v", item)
		}
	}
}

func TestBuildLibraryArtistsCountsAndDrops(t *testing.T) {
	added := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	artists := []lidarr.Artist{
		{ID: 1, ArtistName: "Weezer", ForeignArtistID: "a-1", Added: timePtr(added)},
		{ID: 2, ArtistName: "Empty Shelf", ForeignArtistID: "a-2"},
		{ID: 3, ArtistName: "", ForeignArtistID: "a-3"},
	}
	albums := []lidarr.Album{
		{ID: 10, ArtistID: 1, ForeignAlbumID: "mb-1", Statistics: lidarr.AlbumStatistics{TrackFileCount: 10, TrackCount: 10}},
		{ID: 11, ArtistID: 1, ForeignAlbumID: "mb-2", Statistics: lidarr.AlbumStatistics{TrackFileCount: 3, TrackCount: 10}},
		{ID: 12, ArtistID: 3, ForeignAlbumID: "mb-3"},
	}
	items := buildLibraryArtists(artists, albums)
	if len(items) != 1 {
		t.Fatalf("items = %+v (empty-shelf and nameless artists must drop)", items)
	}
	if items[0].AlbumCount != 2 || items[0].AvailableCount != 1 {
		t.Fatalf("counts = %+v (partial album is not available)", items[0])
	}
}

func TestSortLibraryArtistsOrdersAndCaps(t *testing.T) {
	early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	items := []LibraryArtist{
		{Name: "Beta", AlbumCount: 1, AvailableCount: 1, Added: timePtr(early)},
		{Name: "Alpha", AlbumCount: 3, AvailableCount: 2, Added: timePtr(late)},
		{Name: "Gamma", AlbumCount: 2, AvailableCount: 2},
	}
	byAlbums := sortLibraryArtists(items, ArtistSortAlbums, 0)
	if byAlbums[0].Name != "Alpha" || byAlbums[1].Name != "Gamma" {
		t.Fatalf("albums order = %+v", byAlbums)
	}
	byName := sortLibraryArtists(items, ArtistSortName, 0)
	if byName[0].Name != "Alpha" || byName[2].Name != "Gamma" {
		t.Fatalf("name order = %+v", byName)
	}
	byAdded := sortLibraryArtists(items, ArtistSortAdded, 0)
	// The undated artist trails the dated ones rather than leading as year 0.
	if byAdded[0].Name != "Alpha" || byAdded[2].Name != "Gamma" {
		t.Fatalf("added order = %+v", byAdded)
	}
	capped := sortLibraryArtists(items, ArtistSortAlbums, 2)
	if len(capped) != 2 {
		t.Fatalf("capped = %+v", capped)
	}
}

func TestGetLibraryArtistDetailNotFoundVsForbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/artist":
			_, _ = w.Write([]byte(`[{"id":1,"artistName":"Weezer","foreignArtistId":"a-1"}]`))
		case "/api/v1/album":
			_, _ = w.Write([]byte(`[{"id":10,"artistId":1,"title":"Blue Album","foreignAlbumId":"mb-1"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	svc, uid, _ := newLidarrMusicTestService(t, server.URL)

	detail, err := svc.GetLibraryArtistDetailForInstance(uid, "a-1", "")
	if err != nil {
		t.Fatalf("detail error = %v", err)
	}
	if detail.Artist.Name != "Weezer" || len(detail.Titles) != 1 || detail.Titles[0].Artist != "Weezer" {
		t.Fatalf("detail = %+v", detail)
	}

	if _, err := svc.GetLibraryArtistDetailForInstance(uid, "a-missing", ""); err != ErrMusicArtistNotFound {
		t.Fatalf("missing artist error = %v", err)
	}

	// No grant is not "not found" — that would claim this library was
	// searched.
	res, _ := svc.db.Exec("INSERT INTO users (username, password_hash, role) VALUES ('stranger', '', 'user')")
	strangerID, _ := res.LastInsertId()
	if _, err := svc.GetLibraryArtistDetailForInstance(strangerID, "a-1", ""); err != ErrLidarrInstanceForbidden {
		t.Fatalf("ungranted error = %v", err)
	}
}
