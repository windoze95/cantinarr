// Package lidarr is a typed HTTP client for a Lidarr server, the Servarr
// family's music manager. It speaks the /api/v1 API and is a structural mirror
// of the Chaptarr client, translating the author>book>edition>bookFile model
// to Lidarr's artist>album>release>trackFile model. Unlike Chaptarr there is
// no per-format split (ebook/audiobook): one album is one record.
package lidarr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	arrcommon "github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/transporterr"
)

// ErrCustomFormatsNotFound reports a 404 from the custom format endpoint. It
// lets settings readers distinguish "this Lidarr build has no custom formats"
// from a transport failure.
var ErrCustomFormatsNotFound = errors.New("lidarr: the custom format endpoint returned 404")

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// Image is a cover/poster reference returned on artists and albums.
type Image struct {
	CoverType string `json:"coverType"`
	URL       string `json:"url"`
	RemoteURL string `json:"remoteUrl"`
}

// ArtistStatistics is the per-artist rollup Lidarr returns on an artist.
type ArtistStatistics struct {
	AlbumCount      int     `json:"albumCount"`
	TrackFileCount  int     `json:"trackFileCount"`
	TrackCount      int     `json:"trackCount"`
	TotalTrackCount int     `json:"totalTrackCount"`
	SizeOnDisk      int64   `json:"sizeOnDisk"`
	PercentOfTracks float64 `json:"percentOfTracks"`
}

type Artist struct {
	ID         int    `json:"id"`
	ArtistName string `json:"artistName"`
	// ForeignArtistID is the MusicBrainz artist id.
	ForeignArtistID string `json:"foreignArtistId"`
	// Added is when this artist entered the library. Lidarr sets it on every
	// artist record; it is the only date an artist carries.
	Added             *time.Time `json:"added,omitempty"`
	Overview          string     `json:"overview"`
	ArtistType        string     `json:"artistType"`
	Status            string     `json:"status"`
	Monitored         bool       `json:"monitored"`
	Path              string     `json:"path,omitempty"`
	QualityProfileID  int        `json:"qualityProfileId"`
	MetadataProfileID int        `json:"metadataProfileId"`
	// RemotePoster is set only on lookup results: a metadata-server image URL
	// that is client-reachable without touching the Lidarr origin.
	RemotePoster string           `json:"remotePoster,omitempty"`
	Statistics   ArtistStatistics `json:"statistics"`
	Images       []Image          `json:"images"`
	Genres       []string         `json:"genres"`
}

// AlbumStatistics is the per-album track rollup Lidarr returns on an album.
type AlbumStatistics struct {
	TrackFileCount  int     `json:"trackFileCount"`
	TrackCount      int     `json:"trackCount"`
	TotalTrackCount int     `json:"totalTrackCount"`
	SizeOnDisk      int64   `json:"sizeOnDisk"`
	PercentOfTracks float64 `json:"percentOfTracks"`
}

type Album struct {
	ID       int    `json:"id"`
	Title    string `json:"title"`
	ArtistID int    `json:"artistId"`
	// ForeignAlbumID is the MusicBrainz release-group id.
	ForeignAlbumID string     `json:"foreignAlbumId"`
	Overview       string     `json:"overview"`
	Disambiguation string     `json:"disambiguation"`
	ReleaseDate    *time.Time `json:"releaseDate,omitempty"`
	Monitored      bool       `json:"monitored"`
	AnyReleaseOk   bool       `json:"anyReleaseOk"`
	// AlbumType is the MusicBrainz primary type: Album, EP, Single, Broadcast,
	// Other. SecondaryTypes carries Compilation/Live/Soundtrack/etc.
	AlbumType      string   `json:"albumType"`
	SecondaryTypes []string `json:"secondaryTypes"`
	// RemoteCover is set only on lookup results: a metadata-server image URL
	// that is client-reachable without touching the Lidarr origin.
	RemoteCover string          `json:"remoteCover,omitempty"`
	Artist      *Artist         `json:"artist,omitempty"`
	Statistics  AlbumStatistics `json:"statistics"`
	Images      []Image         `json:"images"`
	Genres      []string        `json:"genres"`
}

type QualityProfile struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type MetadataProfile struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// RootFolder carries Lidarr's per-folder add defaults: unlike Radarr/Sonarr,
// a Lidarr root folder names the quality profile, metadata profile, and
// monitor option new artists should get, so profile selection for adds reads
// these before falling back to the instance's profile lists.
type RootFolder struct {
	ID                       int    `json:"id"`
	Name                     string `json:"name"`
	Path                     string `json:"path"`
	Accessible               bool   `json:"accessible"`
	FreeSpace                int64  `json:"freeSpace"`
	DefaultQualityProfileID  int    `json:"defaultQualityProfileId"`
	DefaultMetadataProfileID int    `json:"defaultMetadataProfileId"`
	DefaultMonitorOption     string `json:"defaultMonitorOption"`
}

// AddArtistRequest is the nested artist Lidarr requires inside an album add
// (and the body of a direct artist add). Profile and root-folder fields are
// required by Lidarr's add validation even when the artist already exists.
type AddArtistRequest struct {
	ArtistName        string `json:"artistName,omitempty"`
	ForeignArtistID   string `json:"foreignArtistId"`
	QualityProfileID  int    `json:"qualityProfileId"`
	MetadataProfileID int    `json:"metadataProfileId"`
	RootFolderPath    string `json:"rootFolderPath"`
	Monitored         bool   `json:"monitored"`
	// MonitorNewItems is Lidarr's standing policy for albums that appear
	// later: "all" or "none". Single-album requests use "none" so a request
	// for one album never silently subscribes the whole future discography.
	MonitorNewItems string `json:"monitorNewItems,omitempty"`
	AddOptions      struct {
		// Monitor is the monitor scope applied at add time: one of
		// all/future/missing/existing/first/latest/none. Single-album adds
		// OMIT it (verified live): "none" unmonitors the artist itself, whose
		// albums then never count as wanted. With it omitted the artist stays
		// monitored and the album's own monitored flag carries the intent.
		Monitor                string `json:"monitor,omitempty"`
		SearchForMissingAlbums bool   `json:"searchForMissingAlbums"`
	} `json:"addOptions"`
}

// AddAlbumRequest adds a single album. Lidarr nests the artist inside the
// album-add payload and finds-or-adds it inline, fetching all metadata
// synchronously from its metadata server during the call — there is no
// queued/pending import state to track afterwards.
type AddAlbumRequest struct {
	ForeignAlbumID string           `json:"foreignAlbumId"`
	Title          string           `json:"title,omitempty"`
	Monitored      bool             `json:"monitored"`
	AnyReleaseOk   bool             `json:"anyReleaseOk"`
	Artist         AddArtistRequest `json:"artist"`
	AddOptions     struct {
		SearchForNewAlbum bool `json:"searchForNewAlbum"`
	} `json:"addOptions"`
}

// ArtistContext is the lean artist object embedded in queue/history records.
type ArtistContext struct {
	ID              int    `json:"id"`
	ArtistName      string `json:"artistName"`
	ForeignArtistID string `json:"foreignArtistId"`
}

// AlbumContext is the lean album object embedded in queue/history records.
type AlbumContext struct {
	ID             int        `json:"id"`
	Title          string     `json:"title"`
	ForeignAlbumID string     `json:"foreignAlbumId"`
	ReleaseDate    *time.Time `json:"releaseDate,omitempty"`
}

// StatusMessage is one grouped warning/error Lidarr attaches to a queue item.
type StatusMessage struct {
	Title    string   `json:"title"`
	Messages []string `json:"messages"`
}

type QueueItem struct {
	ID                    int        `json:"id"`
	ArtistID              int        `json:"artistId"`
	AlbumID               int        `json:"albumId"`
	Title                 string     `json:"title"`
	Added                 *time.Time `json:"added,omitempty"`
	Status                string     `json:"status"`
	TrackedDownloadStatus string     `json:"trackedDownloadStatus"`
	TrackedDownloadState  string     `json:"trackedDownloadState"`
	Timeleft              string     `json:"timeleft"`
	Size                  float64    `json:"size"`
	Sizeleft              float64    `json:"sizeleft"`
	// TrackFileCount/TrackHasFileCount are Lidarr's per-item import progress:
	// how many of the download's tracks exist and how many have imported.
	TrackFileCount    int             `json:"trackFileCount"`
	TrackHasFileCount int             `json:"trackHasFileCount"`
	DownloadClient    string          `json:"downloadClient"`
	DownloadID        string          `json:"downloadId"`
	Indexer           string          `json:"indexer"`
	Protocol          string          `json:"protocol"`
	ErrorMessage      string          `json:"errorMessage"`
	StatusMessages    []StatusMessage `json:"statusMessages"`
	Artist            *ArtistContext  `json:"artist,omitempty"`
	Album             *AlbumContext   `json:"album,omitempty"`
}

// DetailedQueueItem is the queue record with artist and album context. Lidarr
// returns the same shape as QueueItem; the alias keeps callers symmetric with
// the Sonarr/Radarr clients, which distinguish a leaner queue view.
type DetailedQueueItem = QueueItem

type HistoryRecord struct {
	ID          int               `json:"id"`
	EventType   string            `json:"eventType"`
	SourceTitle string            `json:"sourceTitle"`
	Date        *time.Time        `json:"date,omitempty"`
	Quality     json.RawMessage   `json:"quality"`
	ArtistID    int               `json:"artistId"`
	AlbumID     int               `json:"albumId"`
	DownloadID  string            `json:"downloadId"`
	Artist      *ArtistContext    `json:"artist,omitempty"`
	Album       *AlbumContext     `json:"album,omitempty"`
	Data        map[string]string `json:"data,omitempty"`
}

type HistoryPage struct {
	Page         int             `json:"page"`
	PageSize     int             `json:"pageSize"`
	TotalRecords int             `json:"totalRecords"`
	Records      []HistoryRecord `json:"records"`
}

// WantedPage is a page of monitored albums Lidarr reports as missing or
// cutoff-unmet. Records are full album resources with artist context.
type WantedPage struct {
	Page         int     `json:"page"`
	PageSize     int     `json:"pageSize"`
	TotalRecords int     `json:"totalRecords"`
	Records      []Album `json:"records"`
}

type DiskSpace struct {
	Path       string `json:"path"`
	Label      string `json:"label"`
	FreeSpace  int64  `json:"freeSpace"`
	TotalSpace int64  `json:"totalSpace"`
}

// TrackFile is one music file on disk, lean: what the recent-imports digest
// needs to date an album's arrival.
type TrackFile struct {
	ID        int        `json:"id"`
	AlbumID   int        `json:"albumId"`
	Path      string     `json:"path"`
	Size      int64      `json:"size"`
	DateAdded *time.Time `json:"dateAdded,omitempty"`
}

// HealthCheck is one entry from Lidarr's system health report: a config-level
// problem (download client unreachable, remote path mapping, indexers down, no
// root folder, low disk, etc.). Type is one of ok/notice/warning/error.
type HealthCheck struct {
	Source  string `json:"source"`
	Type    string `json:"type"`
	Message string `json:"message"`
	WikiURL string `json:"wikiUrl"`
}

func (c *Client) do(method, path string, body, out any) error {
	return c.doWith(c.httpClient, method, path, body, out)
}

func (c *Client) doWith(client *http.Client, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		// Transport errors embed the full request URL (and DNS failures repeat
		// the hostname). These errors surface beyond admins — e.g. in request
		// failures — so summarize them host-free like the status branch below.
		requestPath, _, _ := strings.Cut(path, "?")
		return fmt.Errorf("lidarr %s %s: %s", method, requestPath, transporterr.Summarize(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		requestPath, _, _ := strings.Cut(path, "?")
		return fmt.Errorf("lidarr %s %s returned status %d", method, requestPath, resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func (c *Client) doRequest(method, path string) (*http.Response, error) {
	return c.doRequestContext(context.Background(), method, path)
}

func (c *Client) doRequestContext(ctx context.Context, method, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Host-free, like doWith: transport errors embed the full request URL.
		requestPath, _, _ := strings.Cut(path, "?")
		return nil, fmt.Errorf("lidarr %s %s: %s", method, requestPath, transporterr.Summarize(err))
	}
	return resp, nil
}

// LookupArtist searches Lidarr's metadata for artists matching term. An exact
// MusicBrainz id fetch uses a "lidarr:<mbid>" term.
func (c *Client) LookupArtist(term string) ([]Artist, error) {
	var results []Artist
	if err := c.do("GET", "/api/v1/artist/lookup?term="+url.QueryEscape(term), nil, &results); err != nil {
		return nil, fmt.Errorf("lidarr artist lookup: %w", err)
	}
	return results, nil
}

// LookupAlbum searches Lidarr's metadata for albums matching term. An exact
// MusicBrainz release-group id fetch uses a "lidarr:<mbid>" term.
func (c *Client) LookupAlbum(term string) ([]Album, error) {
	var results []Album
	if err := c.do("GET", "/api/v1/album/lookup?term="+url.QueryEscape(term), nil, &results); err != nil {
		return nil, fmt.Errorf("lidarr album lookup: %w", err)
	}
	return results, nil
}

// GetArtists lists every artist in the Lidarr library.
func (c *Client) GetArtists() ([]Artist, error) {
	var artists []Artist
	if err := c.do("GET", "/api/v1/artist", nil, &artists); err != nil {
		return nil, fmt.Errorf("lidarr artist list: %w", err)
	}
	return artists, nil
}

// GetArtist returns a single artist by id.
func (c *Client) GetArtist(id int) (*Artist, error) {
	var artist Artist
	if err := c.do("GET", fmt.Sprintf("/api/v1/artist/%d", id), nil, &artist); err != nil {
		return nil, fmt.Errorf("lidarr get artist: %w", err)
	}
	return &artist, nil
}

// GetAlbumsForArtist lists the albums of one artist.
func (c *Client) GetAlbumsForArtist(artistID int) ([]Album, error) {
	var albums []Album
	path := fmt.Sprintf("/api/v1/album?artistId=%d", artistID)
	if err := c.do("GET", path, nil, &albums); err != nil {
		return nil, fmt.Errorf("lidarr albums: %w", err)
	}
	// The artistId query narrows server-side; re-filter here so a build that
	// ignores the parameter still returns only this artist's albums.
	matched := albums[:0]
	for _, a := range albums {
		if a.ArtistID == artistID {
			matched = append(matched, a)
		}
	}
	return matched, nil
}

// libraryFetchClient allows the much longer round-trips of a full-library
// fetch, whose serve time grows with library size (matching the app's 120s
// ceiling for the same endpoint). The normal 30s client fails closed on
// libraries big enough to matter.
func libraryFetchClient() *http.Client {
	return &http.Client{
		Timeout:       120 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// GetAllAlbums lists every album in the Lidarr library (all artists). Lidarr
// returns the same album shape as GetAlbumsForArtist; omitting artistId widens
// the scope.
func (c *Client) GetAllAlbums() ([]Album, error) {
	var albums []Album
	if err := c.doWith(libraryFetchClient(), "GET", "/api/v1/album", nil, &albums); err != nil {
		return nil, fmt.Errorf("lidarr albums: %w", err)
	}
	return albums, nil
}

// GetAlbum returns a single album by id, or (nil, nil) when the record no
// longer exists. Non-2xx responses become host-free errors instead of decoding
// an error body into a bogus zero-id record.
func (c *Client) GetAlbum(id int) (*Album, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/api/v1/album/%d", id))
	if err != nil {
		return nil, fmt.Errorf("lidarr get album: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("lidarr GET /api/v1/album/%d returned status %d", id, resp.StatusCode)
	}

	var album Album
	if err := json.NewDecoder(resp.Body).Decode(&album); err != nil {
		return nil, fmt.Errorf("decode lidarr album: %w", err)
	}
	return &album, nil
}

func (c *Client) GetQualityProfiles() ([]QualityProfile, error) {
	var profiles []QualityProfile
	if err := c.do("GET", "/api/v1/qualityprofile", nil, &profiles); err != nil {
		return nil, fmt.Errorf("lidarr quality profiles: %w", err)
	}
	return profiles, nil
}

func (c *Client) GetMetadataProfiles() ([]MetadataProfile, error) {
	var profiles []MetadataProfile
	if err := c.do("GET", "/api/v1/metadataprofile", nil, &profiles); err != nil {
		return nil, fmt.Errorf("lidarr metadata profiles: %w", err)
	}
	return profiles, nil
}

func (c *Client) GetRootFolders() ([]RootFolder, error) {
	var folders []RootFolder
	if err := c.do("GET", "/api/v1/rootfolder", nil, &folders); err != nil {
		return nil, fmt.Errorf("lidarr root folders: %w", err)
	}
	return folders, nil
}

// addClient allows the much longer round-trip of an album add: Lidarr fetches
// artist and album metadata from its metadata server synchronously inside the
// call, which for a large discography can far outlast the normal 30s budget.
func addClient() *http.Client {
	return &http.Client{
		Timeout:       120 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// AddAlbum adds a single album (and, if needed, its artist) to the library.
// The call is synchronous: Lidarr finds-or-adds the artist inline and fetches
// metadata during the request, so a failure here is final — there is no
// queued-import state to wait on afterwards (unlike Chaptarr's author
// imports).
func (c *Client) AddAlbum(req AddAlbumRequest) (*Album, error) {
	var album Album
	if err := c.doWith(addClient(), "POST", "/api/v1/album", req, &album); err != nil {
		return nil, fmt.Errorf("lidarr add album: %w", err)
	}
	return &album, nil
}

// SetAlbumMonitored toggles monitoring for the given albums. Lidarr's
// album/monitor endpoint is a PUT (a POST returns 405).
func (c *Client) SetAlbumMonitored(albumIDs []int, monitored bool) error {
	body := map[string]any{"albumIds": albumIDs, "monitored": monitored}
	if err := c.do("PUT", "/api/v1/album/monitor", body, nil); err != nil {
		return fmt.Errorf("lidarr set album monitored: %w", err)
	}
	return nil
}

// GetQueue returns the same complete bounded snapshot as GetQueueDetailed.
func (c *Client) GetQueue() ([]QueueItem, error) {
	return c.GetQueueDetailed()
}

// queueMaxRecords is a safety cap on the queue records a detailed snapshot may
// contain, mirroring the Radarr/Sonarr clients.
const queueMaxRecords = 1000

// GetQueueDetailed returns the download queue with artist and album context as
// one complete bounded snapshot, mirroring the Radarr/Sonarr contract: a
// truncated, oversized, or internally inconsistent response is an ERROR, never
// a silently shortened queue — remediation observation treats a successful
// read as complete evidence.
func (c *Client) GetQueueDetailed() ([]DetailedQueueItem, error) {
	var resp struct {
		TotalRecords int                 `json:"totalRecords"`
		Records      []DetailedQueueItem `json:"records"`
	}
	// includeUnknownArtistItems: without it Lidarr silently drops queue rows
	// it could not match to a library artist — exactly the rows most likely to
	// be stuck — before the completeness checks below ever see them.
	path := fmt.Sprintf("/api/v1/queue?page=1&pageSize=%d&includeArtist=true&includeAlbum=true&includeUnknownArtistItems=true&sortKey=id&sortDirection=ascending", queueMaxRecords)
	if err := c.do("GET", path, nil, &resp); err != nil {
		return nil, fmt.Errorf("lidarr queue: %w", err)
	}
	if resp.TotalRecords < 0 || resp.TotalRecords > queueMaxRecords {
		return nil, fmt.Errorf("lidarr queue snapshot incomplete: invalid or oversized total %d (safety cap %d)", resp.TotalRecords, queueMaxRecords)
	}
	if len(resp.Records) != resp.TotalRecords {
		return nil, fmt.Errorf("lidarr queue snapshot incomplete: received %d of %d records in bounded page", len(resp.Records), resp.TotalRecords)
	}
	seenIDs := make(map[int]struct{})
	for _, item := range resp.Records {
		if item.ID <= 0 {
			return nil, fmt.Errorf("lidarr queue snapshot incomplete: record has invalid id")
		}
		if _, duplicate := seenIDs[item.ID]; duplicate {
			return nil, fmt.Errorf("lidarr queue snapshot incomplete: duplicate record id %d", item.ID)
		}
		seenIDs[item.ID] = struct{}{}
	}
	return resp.Records, nil
}

// RemoveQueueItem removes an item from the download queue. removeFromClient
// also deletes the download from the client; blocklist prevents the release
// from being grabbed again; skipRedownload suppresses the automatic re-search
// that a blocklist would otherwise trigger; changeCategory hands the download
// to the client's post-import category instead of removing it.
func (c *Client) RemoveQueueItem(id int, removeFromClient, blocklist, skipRedownload, changeCategory bool) error {
	path := fmt.Sprintf("/api/v1/queue/%d?removeFromClient=%t&blocklist=%t&skipRedownload=%t&changeCategory=%t",
		id, removeFromClient, blocklist, skipRedownload, changeCategory)
	if err := c.do("DELETE", path, nil, nil); err != nil {
		return fmt.Errorf("lidarr remove queue item: %w", err)
	}
	return nil
}

// GetHistory returns a page of history records (grabs, imports, failures),
// most recent first.
func (c *Client) GetHistory(page, pageSize int) (*HistoryPage, error) {
	var hp HistoryPage
	path := fmt.Sprintf("/api/v1/history?page=%d&pageSize=%d&sortKey=date&sortDirection=descending", page, pageSize)
	if err := c.do("GET", path, nil, &hp); err != nil {
		return nil, fmt.Errorf("lidarr history: %w", err)
	}
	return &hp, nil
}

// GetImportHistorySince returns the completed-import history records dated
// after since, newest first, read from one bounded page (eventType=3 —
// trackFileImported in Lidarr's enum). complete reports whether that page
// provably covered the whole window: it reached a dated record at or before
// since, or it held every record the instance has. A record without a date can
// neither prove the boundary nor be windowed, so it is skipped. Callers must
// treat an incomplete window as "more imports than one page can enumerate",
// never as an empty one.
func (c *Client) GetImportHistorySince(since time.Time, pageSize int) (inWindow []HistoryRecord, complete bool, err error) {
	var hp HistoryPage
	path := fmt.Sprintf("/api/v1/history?page=1&pageSize=%d&sortKey=date&sortDirection=descending&eventType=3", pageSize)
	if err := c.do("GET", path, nil, &hp); err != nil {
		return nil, false, fmt.Errorf("lidarr import history since: %w", err)
	}
	complete = hp.TotalRecords <= len(hp.Records)
	for _, rec := range hp.Records {
		if rec.Date == nil {
			continue
		}
		if !rec.Date.After(since) {
			// The page reached past the window boundary, so the window is
			// fully enumerated even when older records exist beyond the page.
			complete = true
			continue
		}
		inWindow = append(inWindow, rec)
	}
	return inWindow, complete, nil
}

// GetUpgradeDeleteHistorySince returns the track-file-deleted history records
// dated after since, newest first, from one bounded page (eventType=5 —
// trackFileDeleted in Lidarr's enum). Lidarr has no webhook toggle for file
// deletes, so the import catch-up pairs these against the same window's
// imports: a delete with data.reason "Upgrade" is the only durable proof that
// an import replaced files rather than filled a gap. Callers must treat an
// error or incomplete window as "no upgrade proof" (announce as new content),
// never as "no upgrades happened".
func (c *Client) GetUpgradeDeleteHistorySince(since time.Time, pageSize int) (inWindow []HistoryRecord, complete bool, err error) {
	var hp HistoryPage
	path := fmt.Sprintf("/api/v1/history?page=1&pageSize=%d&sortKey=date&sortDirection=descending&eventType=5", pageSize)
	if err := c.do("GET", path, nil, &hp); err != nil {
		return nil, false, fmt.Errorf("lidarr upgrade-delete history since: %w", err)
	}
	complete = hp.TotalRecords <= len(hp.Records)
	for _, rec := range hp.Records {
		if rec.Date == nil {
			continue
		}
		if !rec.Date.After(since) {
			// The page reached past the window boundary, so the window is
			// fully enumerated even when older records exist beyond the page.
			complete = true
			continue
		}
		inWindow = append(inWindow, rec)
	}
	return inWindow, complete, nil
}

// GetWantedMissing returns a page of monitored albums with no files.
func (c *Client) GetWantedMissing(page, pageSize int) (*WantedPage, error) {
	var wp WantedPage
	path := fmt.Sprintf("/api/v1/wanted/missing?page=%d&pageSize=%d&sortKey=releaseDate&sortDirection=descending&includeArtist=true", page, pageSize)
	if err := c.do("GET", path, nil, &wp); err != nil {
		return nil, fmt.Errorf("lidarr wanted missing: %w", err)
	}
	return &wp, nil
}

// GetWantedCutoff returns a page of monitored albums below their quality
// cutoff.
func (c *Client) GetWantedCutoff(page, pageSize int) (*WantedPage, error) {
	var wp WantedPage
	path := fmt.Sprintf("/api/v1/wanted/cutoff?page=%d&pageSize=%d&sortKey=releaseDate&sortDirection=descending&includeArtist=true", page, pageSize)
	if err := c.do("GET", path, nil, &wp); err != nil {
		return nil, fmt.Errorf("lidarr wanted cutoff: %w", err)
	}
	return &wp, nil
}

// triggerCommand posts a command payload to Lidarr's command endpoint.
func (c *Client) triggerCommand(payload map[string]any) error {
	if err := c.do("POST", "/api/v1/command", payload, nil); err != nil {
		return fmt.Errorf("lidarr command: %w", err)
	}
	return nil
}

// TriggerArtistSearch starts an automatic search for all monitored albums of
// an artist.
func (c *Client) TriggerArtistSearch(artistID int) error {
	return c.triggerCommand(map[string]any{"name": "ArtistSearch", "artistId": artistID})
}

// TriggerAlbumSearch starts an automatic search for specific albums.
func (c *Client) TriggerAlbumSearch(albumIDs []int) error {
	return c.triggerCommand(map[string]any{"name": "AlbumSearch", "albumIds": albumIDs})
}

// TriggerRefreshArtist refreshes metadata and rescans files for an artist.
func (c *Client) TriggerRefreshArtist(artistID int) error {
	return c.triggerCommand(map[string]any{"name": "RefreshArtist", "artistId": artistID})
}

// ProcessMonitoredDownloads asks Lidarr to run its import pass over the
// download client now (the pass that normally runs on a timer).
func (c *Client) ProcessMonitoredDownloads() error {
	return c.triggerCommand(map[string]any{"name": "ProcessMonitoredDownloads"})
}

// RescanArtist rescans the files on disk for an artist. Lidarr has no
// per-artist rescan command; RescanFolders scoped by artistId is its
// equivalent.
func (c *Client) RescanArtist(artistID int) error {
	return c.triggerCommand(map[string]any{"name": "RescanFolders", "artistId": artistID})
}

// GetTrackFilesForArtist lists the music files on disk for one artist.
// Lidarr's trackfile read requires an artist, album, or id filter — there is
// no library-wide read — which is why recency digests fan out per artist.
func (c *Client) GetTrackFilesForArtist(artistID int) ([]TrackFile, error) {
	var files []TrackFile
	path := fmt.Sprintf("/api/v1/trackfile?artistId=%d", artistID)
	if err := c.do("GET", path, nil, &files); err != nil {
		return nil, fmt.Errorf("lidarr track files: %w", err)
	}
	return files, nil
}

// GetDiskSpace reports disk usage for Lidarr's mounted volumes.
func (c *Client) GetDiskSpace() ([]DiskSpace, error) {
	var disks []DiskSpace
	if err := c.do("GET", "/api/v1/diskspace", nil, &disks); err != nil {
		return nil, fmt.Errorf("lidarr diskspace: %w", err)
	}
	return disks, nil
}

// GetHealth returns Lidarr's current system health checks. These surface
// config-level root causes (download client down, remote path mapping wrong,
// indexers unavailable) that per-item queue diagnosis can only guess at.
func (c *Client) GetHealth() ([]HealthCheck, error) {
	var checks []HealthCheck
	if err := c.do("GET", "/api/v1/health", nil, &checks); err != nil {
		return nil, fmt.Errorf("lidarr health: %w", err)
	}
	return checks, nil
}

// GetCalendar returns the albums whose release dates fall inside [start, end].
// Album release dates are calendar dates (MusicBrainz release-group dates with
// no meaningful time-of-day), so callers should read the Y/M/D components the
// way the Radarr calendar convention does. includeArtist embeds the artist so
// rows can be labelled without a fan-out.
func (c *Client) GetCalendar(start, end time.Time) ([]Album, error) {
	var albums []Album
	path := fmt.Sprintf("/api/v1/calendar?start=%s&end=%s&unmonitored=false&includeArtist=true",
		url.QueryEscape(start.UTC().Format(time.RFC3339)), url.QueryEscape(end.UTC().Format(time.RFC3339)))
	if err := c.do("GET", path, nil, &albums); err != nil {
		return nil, fmt.Errorf("lidarr calendar: %w", err)
	}
	return albums, nil
}

// GetTrackFilesForAlbum lists the music files on disk for one album, with the
// same defensive re-filter as GetAlbumsForArtist: only rows matching the
// requested album are returned even if the server answers wider.
func (c *Client) GetTrackFilesForAlbum(albumID int) ([]TrackFile, error) {
	var files []TrackFile
	path := fmt.Sprintf("/api/v1/trackfile?albumId=%d", albumID)
	if err := c.do("GET", path, nil, &files); err != nil {
		return nil, fmt.Errorf("lidarr album track files: %w", err)
	}
	matched := files[:0]
	for _, f := range files {
		if f.AlbumID == albumID {
			matched = append(matched, f)
		}
	}
	return matched, nil
}

// GetAlbumGrabs returns the grab history for one album, newest first
// (eventType=1 — grabbed), from one bounded page.
func (c *Client) GetAlbumGrabs(albumID, pageSize int) ([]HistoryRecord, error) {
	var hp HistoryPage
	path := fmt.Sprintf("/api/v1/history?page=1&pageSize=%d&sortKey=date&sortDirection=descending&eventType=1&albumId=%d",
		pageSize, albumID)
	if err := c.do("GET", path, nil, &hp); err != nil {
		return nil, fmt.Errorf("lidarr album grabs: %w", err)
	}
	return hp.Records, nil
}

// GetImportHistory returns the completed-import history for one album
// (eventType=3 — trackFileImported), newest first, optionally narrowed to one
// download id, from one bounded page. It errors rather than truncating when
// the record count exceeds the page, so a partial answer can never read as a
// complete one.
func (c *Client) GetImportHistory(albumID int, downloadID string, pageSize int) ([]HistoryRecord, error) {
	var hp HistoryPage
	path := fmt.Sprintf("/api/v1/history?page=1&pageSize=%d&sortKey=date&sortDirection=descending&eventType=3&albumId=%d&downloadId=%s",
		pageSize, albumID, url.QueryEscape(downloadID))
	if err := c.do("GET", path, nil, &hp); err != nil {
		return nil, fmt.Errorf("lidarr import history: %w", err)
	}
	if hp.TotalRecords > pageSize {
		return nil, fmt.Errorf("lidarr import history incomplete: %d records exceeds bound %d", hp.TotalRecords, pageSize)
	}
	return hp.Records, nil
}

// GetConfigSummary returns bounded, credential-free entries for one settings
// section. The raw payloads never leave this method (see arr.ConfigEntry).
func (c *Client) GetConfigSummary(section string) ([]arrcommon.ConfigEntry, error) {
	paths := map[string]string{
		arrcommon.ConfigIndexers:           "/api/v1/indexer",
		arrcommon.ConfigDelayProfiles:      "/api/v1/delayprofile",
		arrcommon.ConfigReleaseProfiles:    "/api/v1/releaseprofile",
		arrcommon.ConfigDownloadClients:    "/api/v1/downloadclient",
		arrcommon.ConfigRemotePathMappings: "/api/v1/remotepathmapping",
	}
	path, ok := paths[section]
	if !ok {
		return nil, fmt.Errorf("unknown config section %q", section)
	}
	var raws []json.RawMessage
	if err := c.do("GET", path, nil, &raws); err != nil {
		return nil, fmt.Errorf("read %s: %w", section, err)
	}
	return arrcommon.SummarizeConfigSection(section, raws), nil
}

// GetQualityProfilesRaw returns every quality profile exactly as Lidarr sent
// it. Settings objects must round-trip verbatim on a future PUT (modeling and
// re-serializing them risks losing fields Lidarr requires), so callers decode
// only the fields they need from each raw object.
func (c *Client) GetQualityProfilesRaw() ([]json.RawMessage, error) {
	return c.GetQualityProfilesRawContext(context.Background())
}

func (c *Client) GetQualityProfilesRawContext(ctx context.Context) ([]json.RawMessage, error) {
	resp, err := c.doRequestContext(ctx, "GET", "/api/v1/qualityprofile")
	if err != nil {
		return nil, fmt.Errorf("lidarr quality profiles: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("lidarr GET /api/v1/qualityprofile returned status %d", resp.StatusCode)
	}
	var profiles []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&profiles); err != nil {
		return nil, fmt.Errorf("decode quality profiles: %w", err)
	}
	return profiles, nil
}

// UpdateQualityProfileRaw fully replaces one credential-free quality profile.
func (c *Client) UpdateQualityProfileRaw(id int, body json.RawMessage) (json.RawMessage, error) {
	return c.UpdateQualityProfileRawContext(context.Background(), id, body)
}

func (c *Client) UpdateQualityProfileRawContext(ctx context.Context, id int, body json.RawMessage) (json.RawMessage, error) {
	path := fmt.Sprintf("/api/v1/qualityprofile/%d", id)
	raw, _, err := arrcommon.DoSettingsWrite(ctx, c.httpClient, "lidarr", c.baseURL, c.apiKey, http.MethodPut, path, body)
	return raw, err
}

// GetCustomFormatsRaw returns every custom format exactly as Lidarr sent it,
// verbatim for the same round-trip reason as GetQualityProfilesRaw. A 404
// maps to ErrCustomFormatsNotFound.
func (c *Client) GetCustomFormatsRaw() ([]json.RawMessage, error) {
	return c.GetCustomFormatsRawContext(context.Background())
}

// GetCustomFormatsRawContext is the cancellation-aware mutation preflight.
func (c *Client) GetCustomFormatsRawContext(ctx context.Context) ([]json.RawMessage, error) {
	resp, err := c.doRequestContext(ctx, "GET", "/api/v1/customformat")
	if err != nil {
		return nil, fmt.Errorf("lidarr custom formats: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrCustomFormatsNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("lidarr GET /api/v1/customformat returned status %d", resp.StatusCode)
	}
	var formats []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&formats); err != nil {
		return nil, fmt.Errorf("decode custom formats: %w", err)
	}
	return formats, nil
}

// CreateCustomFormatRaw creates one credential-free custom-format object. Its
// dedicated write path is the only Lidarr client path allowed to surface the
// typed, redacted validation details from an HTTP 400 response.
func (c *Client) CreateCustomFormatRaw(body json.RawMessage) (json.RawMessage, error) {
	return c.CreateCustomFormatRawContext(context.Background(), body)
}

func (c *Client) CreateCustomFormatRawContext(ctx context.Context, body json.RawMessage) (json.RawMessage, error) {
	raw, _, err := arrcommon.DoSettingsWrite(ctx, c.httpClient, "lidarr", c.baseURL, c.apiKey, http.MethodPost, "/api/v1/customformat", body)
	return raw, err
}

// UpdateCustomFormatRaw fully replaces one custom-format object.
func (c *Client) UpdateCustomFormatRaw(id int, body json.RawMessage) (json.RawMessage, error) {
	return c.UpdateCustomFormatRawContext(context.Background(), id, body)
}

func (c *Client) UpdateCustomFormatRawContext(ctx context.Context, id int, body json.RawMessage) (json.RawMessage, error) {
	path := fmt.Sprintf("/api/v1/customformat/%d", id)
	raw, _, err := arrcommon.DoSettingsWrite(ctx, c.httpClient, "lidarr", c.baseURL, c.apiKey, http.MethodPut, path, body)
	return raw, err
}

// Release is one interactive-search result from Lidarr's release endpoint.
type Release struct {
	GUID       string          `json:"guid"`
	IndexerID  int             `json:"indexerId"`
	Indexer    string          `json:"indexer"`
	Title      string          `json:"title"`
	Size       int64           `json:"size"`
	Seeders    *int            `json:"seeders"`
	Leechers   *int            `json:"leechers"`
	Protocol   string          `json:"protocol"`
	AgeHours   float64         `json:"ageHours"`
	Quality    json.RawMessage `json:"quality"`
	Rejected   bool            `json:"rejected"`
	Rejections []string        `json:"rejections"`
}

// SearchReleases runs an interactive release search for an album. It queries
// every configured indexer, so it rides the long-timeout client.
func (c *Client) SearchReleases(albumID int) ([]Release, error) {
	var releases []Release
	path := fmt.Sprintf("/api/v1/release?albumId=%d", albumID)
	if err := c.doWith(libraryFetchClient(), "GET", path, nil, &releases); err != nil {
		return nil, fmt.Errorf("lidarr release search: %w", err)
	}
	return releases, nil
}

// GrabRelease tells Lidarr to send a previously searched release to the
// download client.
func (c *Client) GrabRelease(guid string, indexerID int) error {
	body := map[string]any{"guid": guid, "indexerId": indexerID}
	if err := c.do("POST", "/api/v1/release", body, nil); err != nil {
		return fmt.Errorf("lidarr grab release: %w", err)
	}
	return nil
}

// ManualImportRejection is a single reason Lidarr would not auto-import a
// file, plus whether the rejection is permanent.
type ManualImportRejection struct {
	Reason string `json:"reason"`
	Type   string `json:"type"`
}

// ManualImportCandidate is a file Lidarr found for a download, as returned by
// GET /manualimport. Quality is kept as raw JSON so it can be round-tripped
// verbatim back into the ManualImport command. The nested artist/album ids and
// matched track ids are lifted for the mapping checks.
type ManualImportCandidate struct {
	ID           int                     `json:"id"`
	Path         string                  `json:"path"`
	FolderName   string                  `json:"folderName"`
	Name         string                  `json:"name"`
	Size         int64                   `json:"size"`
	ArtistID     int                     `json:"-"`
	AlbumID      int                     `json:"-"`
	TrackIDs     []int                   `json:"-"`
	AlbumRelease int                     `json:"-"`
	Quality      json.RawMessage         `json:"quality"`
	ReleaseGroup string                  `json:"releaseGroup"`
	DownloadID   string                  `json:"downloadId"`
	Rejections   []ManualImportRejection `json:"rejections"`
}

// UnmarshalJSON lifts the nested artist/album ids, the albumReleaseId, and the
// matched track ids out of Lidarr's nested shapes.
func (m *ManualImportCandidate) UnmarshalJSON(data []byte) error {
	type alias ManualImportCandidate
	aux := struct {
		*alias
		Artist *struct {
			ID int `json:"id"`
		} `json:"artist"`
		Album *struct {
			ID int `json:"id"`
		} `json:"album"`
		AlbumReleaseID int `json:"albumReleaseId"`
		Tracks         []struct {
			ID int `json:"id"`
		} `json:"tracks"`
	}{alias: (*alias)(m)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.Artist != nil {
		m.ArtistID = aux.Artist.ID
	}
	if aux.Album != nil {
		m.AlbumID = aux.Album.ID
	}
	m.AlbumRelease = aux.AlbumReleaseID
	for _, t := range aux.Tracks {
		if t.ID > 0 {
			m.TrackIDs = append(m.TrackIDs, t.ID)
		}
	}
	return nil
}

// ManualImportFile is one file to import via the ManualImport command. Quality
// is passed back verbatim from the candidate. Artist, album, and track ids
// must be set or Lidarr silently skips the file.
type ManualImportFile struct {
	Path           string          `json:"path"`
	FolderName     string          `json:"folderName,omitempty"`
	ArtistID       int             `json:"artistId"`
	AlbumID        int             `json:"albumId"`
	AlbumReleaseID int             `json:"albumReleaseId,omitempty"`
	TrackIDs       []int           `json:"trackIds,omitempty"`
	Quality        json.RawMessage `json:"quality,omitempty"`
	ReleaseGroup   string          `json:"releaseGroup,omitempty"`
	DownloadID     string          `json:"downloadId,omitempty"`
}

// GetManualImportCandidates lists the files Lidarr found for a download,
// including any rejection reasons, without importing existing files.
func (c *Client) GetManualImportCandidates(downloadID string) ([]ManualImportCandidate, error) {
	var candidates []ManualImportCandidate
	path := fmt.Sprintf("/api/v1/manualimport?downloadId=%s&filterExistingFiles=false", url.QueryEscape(downloadID))
	if err := c.doWith(libraryFetchClient(), "GET", path, nil, &candidates); err != nil {
		return nil, fmt.Errorf("lidarr manual import candidates: %w", err)
	}
	return candidates, nil
}

// ExecuteManualImport tells Lidarr to import the given files. importMode must
// be lowercase (move/copy/auto); the PascalCase form is silently ignored.
func (c *Client) ExecuteManualImport(files []ManualImportFile) error {
	payload := map[string]any{
		"name":       "ManualImport",
		"importMode": "auto",
		"files":      files,
	}
	if err := c.do("POST", "/api/v1/command", payload, nil); err != nil {
		return fmt.Errorf("lidarr command: %w", err)
	}
	return nil
}

// DeleteTrackFile removes one imported music file from disk and the library —
// the DELETE /trackfile/{id} the wrong-album repair needs.
func (c *Client) DeleteTrackFile(id int) error {
	path := fmt.Sprintf("/api/v1/trackfile/%d", id)
	if err := c.do("DELETE", path, nil, nil); err != nil {
		return fmt.Errorf("lidarr delete track file: %w", err)
	}
	return nil
}

// MarkHistoryFailed marks one grab record as a failed download — the only
// route to blocklist a release that already imported.
func (c *Client) MarkHistoryFailed(historyID int64) error {
	path := fmt.Sprintf("/api/v1/history/failed/%d", historyID)
	if err := c.do("POST", path, nil, nil); err != nil {
		return fmt.Errorf("lidarr mark history failed: %w", err)
	}
	return nil
}

// GetFailedDownloadPolicy reports autoRedownloadFailed: whether Lidarr itself
// searches for a replacement when a grab is marked failed.
func (c *Client) GetFailedDownloadPolicy() (autoRedownloadFailed bool, err error) {
	var config struct {
		AutoRedownloadFailed bool `json:"autoRedownloadFailed"`
	}
	if err := c.do("GET", "/api/v1/config/downloadclient", nil, &config); err != nil {
		return false, fmt.Errorf("lidarr download client config: %w", err)
	}
	return config.AutoRedownloadFailed, nil
}
