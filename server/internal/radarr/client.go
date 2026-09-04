package radarr

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
	"github.com/windoze95/cantinarr-server/internal/httpx"
	"github.com/windoze95/cantinarr-server/internal/transporterr"
)

// ErrCustomFormatsNotFound reports a 404 from the custom format endpoint. It
// is deliberately not called "unsupported": a build predating custom formats
// and an instance URL missing the service's URL base are indistinguishable
// from here, so callers must present both causes rather than diagnose one.
var ErrCustomFormatsNotFound = errors.New("radarr: the custom format endpoint returned 404")

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
			Transport:     httpx.Internal(),
			Timeout:       30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

type Movie struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	TmdbID      int    `json:"tmdbId"`
	Year        int    `json:"year"`
	HasFile     bool   `json:"hasFile"`
	MovieFileID int    `json:"movieFileId"`
	MovieFile   struct {
		ID int `json:"id"`
	} `json:"movieFile"`
	Monitored      bool   `json:"monitored"`
	IsAvailable    bool   `json:"isAvailable"`
	RootFolderPath string `json:"rootFolderPath,omitempty"`
	// InCinemas and DigitalRelease are the movie's theatrical and digital
	// release dates. Radarr serves them as RFC3339 timestamps, but they are
	// calendar dates with no meaningful time-of-day: read the Y/M/D components
	// directly and never convert time zones, which would shift a midnight date
	// onto the previous day. Absent when the date is unknown to Radarr.
	InCinemas      *time.Time `json:"inCinemas,omitempty"`
	DigitalRelease *time.Time `json:"digitalRelease,omitempty"`
}

// MovieFile is Radarr's metadata for one completed movie file on disk.
type MovieFile struct {
	ID           int    `json:"id"`
	MovieID      int    `json:"movieId"`
	RelativePath string `json:"relativePath"`
	Path         string `json:"path"`
	Size         int64  `json:"size"`
	// DateAdded is when Radarr imported this file. Pointer-typed so an absent
	// field reads as "unknown" rather than the zero time, which a caller
	// comparing timestamps would otherwise treat as impossibly old.
	DateAdded *time.Time `json:"dateAdded"`
	SceneName string     `json:"sceneName"`
	Quality   struct {
		Quality struct {
			Name string `json:"name"`
		} `json:"quality"`
	} `json:"quality"`
	// MediaInfo mirrors sonarr.FileMediaInfo: the arr's ffprobe-derived truth
	// about the file on disk. Pointer-typed: older records may not carry it.
	MediaInfo *MovieFileMediaInfo `json:"mediaInfo"`
}

// MovieFileMediaInfo is the media-property block Radarr serves on a file record.
type MovieFileMediaInfo struct {
	AudioChannels     float64 `json:"audioChannels"`
	AudioCodec        string  `json:"audioCodec"`
	AudioLanguages    string  `json:"audioLanguages"`
	Height            int     `json:"height"`
	Width             int     `json:"width"`
	Resolution        string  `json:"resolution"`
	RunTime           string  `json:"runTime"`
	VideoCodec        string  `json:"videoCodec"`
	VideoDynamicRange string  `json:"videoDynamicRange"`
	Subtitles         string  `json:"subtitles"`
}

type QualityProfile struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type RootFolder struct {
	ID   int    `json:"id"`
	Path string `json:"path"`
}

type LookupResult struct {
	Title  string `json:"title"`
	TmdbID int    `json:"tmdbId"`
	Year   int    `json:"year"`
	Images []struct {
		CoverType string `json:"coverType"`
		RemoteURL string `json:"remoteUrl"`
	} `json:"images"`
}

type AddMovieRequest struct {
	Title            string `json:"title"`
	TmdbID           int    `json:"tmdbId"`
	Year             int    `json:"year"`
	QualityProfileID int    `json:"qualityProfileId"`
	RootFolderPath   string `json:"rootFolderPath"`
	Monitored        bool   `json:"monitored"`
	AddOptions       struct {
		SearchForMovie bool `json:"searchForMovie"`
	} `json:"addOptions"`
}

type QueueItem struct {
	MovieID  int     `json:"movieId"`
	Title    string  `json:"title"`
	Status   string  `json:"status"`
	Sizeleft float64 `json:"sizeleft"`
	Size     float64 `json:"size"`
}

// do executes a request with an optional JSON body, fails on non-2xx status,
// and decodes JSON into out when out is non-nil. Upstream error bodies are
// deliberately excluded because they can contain credentials or signed URLs.
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
		return fmt.Errorf("radarr %s %s: %s", method, requestPath, transporterr.Summarize(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		requestPath, _, _ := strings.Cut(path, "?")
		return fmt.Errorf("radarr %s %s returned status %d", method, requestPath, resp.StatusCode)
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
		return nil, fmt.Errorf("radarr %s %s: %s", method, requestPath, transporterr.Summarize(err))
	}
	return resp, nil
}

func (c *Client) LookupByTMDB(tmdbID int) (*LookupResult, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/api/v3/movie/lookup?term=tmdb:%d", tmdbID))
	if err != nil {
		return nil, fmt.Errorf("radarr lookup: %w", err)
	}
	defer resp.Body.Close()

	var results []LookupResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("decode radarr lookup: %w", err)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no results found for TMDB ID %d", tmdbID)
	}
	return &results[0], nil
}

func (c *Client) GetMovie(id int) (*Movie, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/api/v3/movie/%d", id))
	if err != nil {
		return nil, fmt.Errorf("radarr get movie: %w", err)
	}
	defer resp.Body.Close()

	var movie Movie
	if err := json.NewDecoder(resp.Body).Decode(&movie); err != nil {
		return nil, fmt.Errorf("decode radarr movie: %w", err)
	}
	return &movie, nil
}

// GetMovieFile returns live metadata for one completed file in Radarr.
func (c *Client) GetMovieFile(id int) (*MovieFile, error) {
	var file MovieFile
	path := fmt.Sprintf("/api/v3/moviefile/%d", id)
	if err := c.do(http.MethodGet, path, nil, &file); err != nil {
		return nil, fmt.Errorf("radarr movie file: %w", err)
	}
	return &file, nil
}

// DeleteMovieFile deletes one imported file from disk and from Radarr's
// records. The movie itself stays monitored, so Radarr remains free to grab a
// replacement under its own policy.
func (c *Client) DeleteMovieFile(id int) error {
	path := fmt.Sprintf("/api/v3/moviefile/%d", id)
	if err := c.do(http.MethodDelete, path, nil, nil); err != nil {
		return fmt.Errorf("radarr delete movie file: %w", err)
	}
	return nil
}

func (c *Client) GetMovieByTMDB(tmdbID int) (*Movie, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/api/v3/movie?tmdbId=%d", tmdbID))
	if err != nil {
		return nil, fmt.Errorf("radarr get movie: %w", err)
	}
	defer resp.Body.Close()

	var movies []Movie
	if err := json.NewDecoder(resp.Body).Decode(&movies); err != nil {
		return nil, fmt.Errorf("decode radarr movie: %w", err)
	}
	if len(movies) == 0 {
		return nil, nil
	}
	return &movies[0], nil
}

func (c *Client) GetQualityProfiles() ([]QualityProfile, error) {
	resp, err := c.doRequest("GET", "/api/v3/qualityprofile")
	if err != nil {
		return nil, fmt.Errorf("radarr quality profiles: %w", err)
	}
	defer resp.Body.Close()

	var profiles []QualityProfile
	if err := json.NewDecoder(resp.Body).Decode(&profiles); err != nil {
		return nil, fmt.Errorf("decode quality profiles: %w", err)
	}
	return profiles, nil
}

// GetQualityProfilesRaw returns every quality profile exactly as Radarr sent
// it. Settings objects must round-trip verbatim on a future PUT (modeling and
// re-serializing them risks losing fields Radarr requires), so callers decode
// only the fields they need from each raw object.
func (c *Client) GetQualityProfilesRaw() ([]json.RawMessage, error) {
	return c.GetQualityProfilesRawContext(context.Background())
}

func (c *Client) GetQualityProfilesRawContext(ctx context.Context) ([]json.RawMessage, error) {
	resp, err := c.doRequestContext(ctx, "GET", "/api/v3/qualityprofile")
	if err != nil {
		return nil, fmt.Errorf("radarr quality profiles: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("radarr GET /api/v3/qualityprofile returned status %d", resp.StatusCode)
	}
	var profiles []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&profiles); err != nil {
		return nil, fmt.Errorf("decode quality profiles: %w", err)
	}
	return profiles, nil
}

// UpdateQualityProfileRaw fully replaces one credential-free quality profile.
// The caller must start from a fresh GetQualityProfilesRawContext object so
// fields introduced by newer Radarr builds survive the round trip.
func (c *Client) UpdateQualityProfileRaw(id int, body json.RawMessage) (json.RawMessage, error) {
	return c.UpdateQualityProfileRawContext(context.Background(), id, body)
}

func (c *Client) UpdateQualityProfileRawContext(ctx context.Context, id int, body json.RawMessage) (json.RawMessage, error) {
	path := fmt.Sprintf("/api/v3/qualityprofile/%d", id)
	raw, _, err := arrcommon.DoSettingsWrite(ctx, c.httpClient, "radarr", c.baseURL, c.apiKey, http.MethodPut, path, body)
	return raw, err
}

// GetLanguagesRawContext returns Radarr's live language catalog. Language IDs
// may vary by service and version, so callers resolve names from this catalog
// instead of hardcoding or reusing an ID from another arr.
func (c *Client) GetLanguagesRawContext(ctx context.Context) ([]json.RawMessage, error) {
	resp, err := c.doRequestContext(ctx, http.MethodGet, "/api/v3/language")
	if err != nil {
		return nil, fmt.Errorf("radarr languages: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("radarr GET /api/v3/language returned status %d", resp.StatusCode)
	}
	var languages []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&languages); err != nil {
		return nil, fmt.Errorf("decode radarr languages: %w", err)
	}
	return languages, nil
}

// GetCustomFormatsRaw returns every custom format exactly as Radarr sent it,
// verbatim for the same round-trip reason as GetQualityProfilesRaw. A 404
// maps to ErrCustomFormatsNotFound.
func (c *Client) GetCustomFormatsRaw() ([]json.RawMessage, error) {
	return c.GetCustomFormatsRawContext(context.Background())
}

// GetCustomFormatsRawContext is the cancellation-aware mutation preflight.
func (c *Client) GetCustomFormatsRawContext(ctx context.Context) ([]json.RawMessage, error) {
	resp, err := c.doRequestContext(ctx, "GET", "/api/v3/customformat")
	if err != nil {
		return nil, fmt.Errorf("radarr custom formats: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrCustomFormatsNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("radarr GET /api/v3/customformat returned status %d", resp.StatusCode)
	}
	var formats []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&formats); err != nil {
		return nil, fmt.Errorf("decode custom formats: %w", err)
	}
	return formats, nil
}

// CreateCustomFormatRaw creates one credential-free custom-format object. Its
// dedicated write path is the only Radarr client path allowed to surface the
// typed, redacted validation details from an HTTP 400 response.
func (c *Client) CreateCustomFormatRaw(body json.RawMessage) (json.RawMessage, error) {
	return c.CreateCustomFormatRawContext(context.Background(), body)
}

func (c *Client) CreateCustomFormatRawContext(ctx context.Context, body json.RawMessage) (json.RawMessage, error) {
	raw, _, err := arrcommon.DoSettingsWrite(ctx, c.httpClient, "radarr", c.baseURL, c.apiKey, http.MethodPost, "/api/v3/customformat", body)
	return raw, err
}

// UpdateCustomFormatRaw fully replaces one custom-format object.
func (c *Client) UpdateCustomFormatRaw(id int, body json.RawMessage) (json.RawMessage, error) {
	return c.UpdateCustomFormatRawContext(context.Background(), id, body)
}

func (c *Client) UpdateCustomFormatRawContext(ctx context.Context, id int, body json.RawMessage) (json.RawMessage, error) {
	path := fmt.Sprintf("/api/v3/customformat/%d", id)
	raw, _, err := arrcommon.DoSettingsWrite(ctx, c.httpClient, "radarr", c.baseURL, c.apiKey, http.MethodPut, path, body)
	return raw, err
}

func (c *Client) GetRootFolders() ([]RootFolder, error) {
	resp, err := c.doRequest("GET", "/api/v3/rootfolder")
	if err != nil {
		return nil, fmt.Errorf("radarr root folders: %w", err)
	}
	defer resp.Body.Close()

	var folders []RootFolder
	if err := json.NewDecoder(resp.Body).Decode(&folders); err != nil {
		return nil, fmt.Errorf("decode root folders: %w", err)
	}
	return folders, nil
}

func (c *Client) AddMovie(addReq *AddMovieRequest) error {
	body, err := json.Marshal(addReq)
	if err != nil {
		return fmt.Errorf("marshal add movie: %w", err)
	}

	req, err := http.NewRequest("POST", c.baseURL+"/api/v3/movie", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create add request: %w", err)
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Host-free: this error reaches requesters through request failures.
		return fmt.Errorf("radarr add movie: %s", transporterr.Summarize(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("radarr add movie returned status %d", resp.StatusCode)
	}
	return nil
}

// GetQueue returns the lean queue view as one complete bounded page. It used
// to issue an unpaged read, which Radarr answers with its default page of 10
// rows — a silently truncated queue for any instance downloading more than
// that. includeUnknownMovieItems keeps rows Radarr could not match to a
// library movie visible; consumers must treat MovieID 0 as unmatched.
func (c *Client) GetQueue() ([]QueueItem, error) {
	var queueResp struct {
		TotalRecords int         `json:"totalRecords"`
		Records      []QueueItem `json:"records"`
	}
	path := fmt.Sprintf("/api/v3/queue?page=1&pageSize=%d&includeMovie=true&includeUnknownMovieItems=true&sortKey=id&sortDirection=ascending", queueMaxRecords)
	if err := c.do("GET", path, nil, &queueResp); err != nil {
		return nil, fmt.Errorf("radarr queue: %w", err)
	}
	if queueResp.TotalRecords < 0 || queueResp.TotalRecords > queueMaxRecords {
		return nil, fmt.Errorf("radarr queue snapshot incomplete: invalid or oversized total %d (safety cap %d)", queueResp.TotalRecords, queueMaxRecords)
	}
	if len(queueResp.Records) != queueResp.TotalRecords {
		return nil, fmt.Errorf("radarr queue snapshot incomplete: received %d of %d records in bounded page", len(queueResp.Records), queueResp.TotalRecords)
	}
	return queueResp.Records, nil
}

// MovieContext is the lean movie object embedded in queue/history records.
type MovieContext struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	Year        int    `json:"year"`
	TmdbID      int    `json:"tmdbId"`
	MovieFileID *int   `json:"movieFileId"`
}

type DetailedQueueItem struct {
	ID                    int        `json:"id"`
	MovieID               int        `json:"movieId"`
	Title                 string     `json:"title"`
	Status                string     `json:"status"`
	TrackedDownloadStatus string     `json:"trackedDownloadStatus"`
	TrackedDownloadState  string     `json:"trackedDownloadState"`
	Timeleft              string     `json:"timeleft"`
	Size                  float64    `json:"size"`
	Sizeleft              float64    `json:"sizeleft"`
	DownloadClient        string     `json:"downloadClient"`
	DownloadID            string     `json:"downloadId"`
	Indexer               string     `json:"indexer"`
	Protocol              string     `json:"protocol"`
	Added                 *time.Time `json:"added"`
	ErrorMessage          string     `json:"errorMessage"`
	StatusMessages        []struct {
		Title    string   `json:"title"`
		Messages []string `json:"messages"`
	} `json:"statusMessages"`
	Movie *MovieContext `json:"movie,omitempty"`
}

// FileIDAtSnapshot returns the exact embedded movie's file ID when Radarr
// supplied enough identity and file-id data to prove it. Zero means known
// absent. Queue movie.hasFile is not populated, while movieFileId is.
func (item DetailedQueueItem) FileIDAtSnapshot() *int64 {
	if item.Movie == nil || item.MovieID <= 0 || item.Movie.ID != item.MovieID ||
		item.Movie.TmdbID <= 0 || item.Movie.MovieFileID == nil || *item.Movie.MovieFileID < 0 {
		return nil
	}
	fileID := int64(*item.Movie.MovieFileID)
	return &fileID
}

// queueMaxRecords is both the requested single-page size and a safety cap.
// A multi-page offset snapshot can silently mix two queue generations when
// rows churn without changing totalRecords, so observation never treats one as
// authoritative.
const queueMaxRecords = 1000

// GetQueueDetailed returns one bounded, internally consistent queue page with
// movie context. A server that clamps/truncates the requested page fails closed.
func (c *Client) GetQueueDetailed() ([]DetailedQueueItem, error) {
	var resp struct {
		TotalRecords int                 `json:"totalRecords"`
		Records      []DetailedQueueItem `json:"records"`
	}
	// includeUnknownMovieItems: without it Radarr silently drops queue rows it
	// could not match to a library movie — exactly the rows most likely to be
	// stuck — before the completeness checks below ever see them.
	path := fmt.Sprintf("/api/v3/queue?page=1&pageSize=%d&includeMovie=true&includeUnknownMovieItems=true&sortKey=id&sortDirection=ascending", queueMaxRecords)
	if err := c.do("GET", path, nil, &resp); err != nil {
		return nil, fmt.Errorf("radarr queue: %w", err)
	}
	if resp.TotalRecords < 0 || resp.TotalRecords > queueMaxRecords {
		return nil, fmt.Errorf("radarr queue snapshot incomplete: invalid or oversized total %d (safety cap %d)", resp.TotalRecords, queueMaxRecords)
	}
	if len(resp.Records) != resp.TotalRecords {
		return nil, fmt.Errorf("radarr queue snapshot incomplete: received %d of %d records in bounded page", len(resp.Records), resp.TotalRecords)
	}
	seenIDs := make(map[int]struct{})
	for _, item := range resp.Records {
		if item.ID <= 0 {
			return nil, fmt.Errorf("radarr queue snapshot incomplete: record has invalid id")
		}
		if _, duplicate := seenIDs[item.ID]; duplicate {
			return nil, fmt.Errorf("radarr queue snapshot incomplete: duplicate record id %d", item.ID)
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
	path := fmt.Sprintf("/api/v3/queue/%d?removeFromClient=%t&blocklist=%t&skipRedownload=%t&changeCategory=%t",
		id, removeFromClient, blocklist, skipRedownload, changeCategory)
	if err := c.do("DELETE", path, nil, nil); err != nil {
		return fmt.Errorf("radarr remove queue item: %w", err)
	}
	return nil
}

type HistoryRecord struct {
	ID          int64             `json:"id"`
	MovieID     int               `json:"movieId"`
	EventType   string            `json:"eventType"`
	SourceTitle string            `json:"sourceTitle"`
	Date        time.Time         `json:"date"`
	DownloadID  string            `json:"downloadId"`
	Data        map[string]string `json:"data"`
	Quality     struct {
		Quality struct {
			Name string `json:"name"`
		} `json:"quality"`
	} `json:"quality"`
	Movie *MovieContext `json:"movie,omitempty"`
}

// GetHistory returns the most recent history records (grabs, imports, failures).
func (c *Client) GetHistory(pageSize int) ([]HistoryRecord, error) {
	var resp struct {
		Records []HistoryRecord `json:"records"`
	}
	path := fmt.Sprintf("/api/v3/history?page=1&pageSize=%d&sortKey=date&sortDirection=descending&includeMovie=true", pageSize)
	if err := c.do("GET", path, nil, &resp); err != nil {
		return nil, fmt.Errorf("radarr history: %w", err)
	}
	return resp.Records, nil
}

// GetMovieHistory returns the history Radarr still holds for one movie — every
// grab, import and failure. Prefer it over GetHistory whenever the caller knows
// the movie: GetHistory reads one page of the GLOBAL log, so a busy instance
// buries a title's records within hours, while this endpoint filters
// server-side and reaches records months old.
//
// /history/movie answers with a bare JSON array, not the paged envelope the
// /history endpoint uses, so there is no records wrapper to unwrap and no
// server-side page size to ask for. pageSize is therefore a purely client-side
// cap on how many of the returned records (newest first, the order Radarr sends
// them in) the caller wants back; pageSize <= 0 returns all of them.
func (c *Client) GetMovieHistory(movieID, pageSize int) ([]HistoryRecord, error) {
	var records []HistoryRecord
	path := fmt.Sprintf("/api/v3/history/movie?movieId=%d&includeMovie=true", movieID)
	if err := c.do("GET", path, nil, &records); err != nil {
		return nil, fmt.Errorf("radarr movie history: %w", err)
	}
	if pageSize > 0 && len(records) > pageSize {
		records = records[:pageSize]
	}
	return records, nil
}

// GetImportHistory returns a bounded server-filtered import witness for one
// internal movie and observed download identity. Callers still revalidate every
// returned field; filters reduce both noise and truncation risk.
func (c *Client) GetImportHistory(movieID int, downloadID string, pageSize int) ([]HistoryRecord, error) {
	var resp struct {
		TotalRecords int             `json:"totalRecords"`
		Records      []HistoryRecord `json:"records"`
	}
	path := fmt.Sprintf("/api/v3/history?page=1&pageSize=%d&sortKey=date&sortDirection=descending&includeMovie=true&eventType=3&movieIds=%d&downloadId=%s",
		pageSize, movieID, url.QueryEscape(downloadID))
	if err := c.do("GET", path, nil, &resp); err != nil {
		return nil, fmt.Errorf("radarr import history: %w", err)
	}
	if resp.TotalRecords > pageSize {
		return nil, fmt.Errorf("radarr import history incomplete: %d records exceeds bound %d", resp.TotalRecords, pageSize)
	}
	return resp.Records, nil
}

// GrabProvenance reports how the *newest* grab of this download came about —
// the grab that put the item currently in the queue there. Radarr stamps a
// releaseSource on every grabbed history event: "Rss" means it found the
// release on its own because it beat what the library already had; anything
// else ("Search", "UserInvokedSearch", "InteractiveSearch", "ReleasePush")
// means something went looking. An empty string means unknown, which callers
// must treat as "assume a search was involved" rather than guessing.
//
// One download can carry several grab records (a re-grab of the same release
// makes another), so newest-first ordering is load-bearing.
func (c *Client) GrabProvenance(movieID int, downloadID string) (string, error) {
	var resp struct {
		Records []HistoryRecord `json:"records"`
	}
	path := fmt.Sprintf("/api/v3/history?page=1&pageSize=10&sortKey=date&sortDirection=descending&eventType=1&movieIds=%d&downloadId=%s",
		movieID, url.QueryEscape(downloadID))
	if err := c.do("GET", path, nil, &resp); err != nil {
		return "", fmt.Errorf("radarr grab provenance: %w", err)
	}
	for _, record := range resp.Records {
		for key, value := range record.Data {
			if strings.EqualFold(key, "releaseSource") {
				return value, nil
			}
		}
	}
	return "", nil
}

// GetImportHistorySince returns the completed-import history records dated
// after since, newest first, read from one bounded page. complete reports
// whether that page provably covered the whole window: it reached a record at
// or before since, or it held every record the instance has. Callers must
// treat an incomplete window as "more imports than one page can enumerate",
// never as an empty one.
func (c *Client) GetImportHistorySince(since time.Time, pageSize int) (inWindow []HistoryRecord, complete bool, err error) {
	var resp struct {
		TotalRecords int             `json:"totalRecords"`
		Records      []HistoryRecord `json:"records"`
	}
	path := fmt.Sprintf("/api/v3/history?page=1&pageSize=%d&sortKey=date&sortDirection=descending&includeMovie=true&eventType=3", pageSize)
	if err := c.do("GET", path, nil, &resp); err != nil {
		return nil, false, fmt.Errorf("radarr import history since: %w", err)
	}
	complete = resp.TotalRecords <= len(resp.Records)
	for _, rec := range resp.Records {
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

// GetUpgradeDeleteHistorySince returns the movie-file-deleted history records
// dated after since, newest first, from one bounded page (eventType=6 —
// movieFileDeleted). The import-history catch-up pairs these against the same
// window's imports: a delete with data.reason "Upgrade" is the only durable
// proof that an import replaced a file rather than filled a gap. Callers must
// treat an error or incomplete window as "no upgrade proof" (announce as new
// content), never as "no upgrades happened".
func (c *Client) GetUpgradeDeleteHistorySince(since time.Time, pageSize int) (inWindow []HistoryRecord, complete bool, err error) {
	var resp struct {
		TotalRecords int             `json:"totalRecords"`
		Records      []HistoryRecord `json:"records"`
	}
	path := fmt.Sprintf("/api/v3/history?page=1&pageSize=%d&sortKey=date&sortDirection=descending&eventType=6", pageSize)
	if err := c.do("GET", path, nil, &resp); err != nil {
		return nil, false, fmt.Errorf("radarr upgrade-delete history since: %w", err)
	}
	complete = resp.TotalRecords <= len(resp.Records)
	for _, rec := range resp.Records {
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

// MarkHistoryFailed marks one grab history record as a failed download — the
// "Mark as Failed" button. It is Radarr's only route to blocklist a release
// that already finished and imported: the blocklist endpoint has no add
// operation, and the queue-side blocklist flag needs a live queue row, which a
// download that completed two weeks ago no longer has. Marking a grab failed
// also lets Radarr decide for itself whether to look for a replacement (see
// GetFailedDownloadPolicy).
func (c *Client) MarkHistoryFailed(historyID int64) error {
	path := fmt.Sprintf("/api/v3/history/failed/%d", historyID)
	if err := c.do(http.MethodPost, path, nil, nil); err != nil {
		return fmt.Errorf("radarr mark history failed: %w", err)
	}
	return nil
}

// GetFailedDownloadPolicy reports the instance's autoRedownloadFailed setting:
// whether Radarr searches for a replacement on its own once a download is
// marked failed. That is the admin's decision, so a caller that blocklists a
// release must read it rather than assume — with the policy on, adding a search
// of our own only duplicates the grab Radarr already dispatched.
func (c *Client) GetFailedDownloadPolicy() (autoRedownloadFailed bool, err error) {
	var config struct {
		AutoRedownloadFailed bool `json:"autoRedownloadFailed"`
	}
	if err := c.do("GET", "/api/v3/config/downloadclient", nil, &config); err != nil {
		return false, fmt.Errorf("radarr download client config: %w", err)
	}
	return config.AutoRedownloadFailed, nil
}

type CalendarItem struct {
	ID              int        `json:"id"`
	Title           string     `json:"title"`
	Year            int        `json:"year"`
	TmdbID          int        `json:"tmdbId"`
	HasFile         bool       `json:"hasFile"`
	Monitored       bool       `json:"monitored"`
	InCinemas       *time.Time `json:"inCinemas,omitempty"`
	DigitalRelease  *time.Time `json:"digitalRelease,omitempty"`
	PhysicalRelease *time.Time `json:"physicalRelease,omitempty"`
}

// GetCalendar returns monitored movies with release dates in [start, end].
func (c *Client) GetCalendar(start, end time.Time) ([]CalendarItem, error) {
	path := fmt.Sprintf("/api/v3/calendar?start=%s&end=%s&unmonitored=false",
		url.QueryEscape(start.UTC().Format(time.RFC3339)),
		url.QueryEscape(end.UTC().Format(time.RFC3339)))
	var items []CalendarItem
	if err := c.do("GET", path, nil, &items); err != nil {
		return nil, fmt.Errorf("radarr calendar: %w", err)
	}
	return items, nil
}

type Release struct {
	GUID      string  `json:"guid"`
	IndexerID int     `json:"indexerId"`
	Indexer   string  `json:"indexer"`
	Title     string  `json:"title"`
	Size      int64   `json:"size"`
	Seeders   int     `json:"seeders"`
	Leechers  int     `json:"leechers"`
	Protocol  string  `json:"protocol"`
	AgeHours  float64 `json:"ageHours"`
	Quality   struct {
		Quality struct {
			Name string `json:"name"`
		} `json:"quality"`
	} `json:"quality"`
	Languages []struct {
		Name string `json:"name"`
	} `json:"languages"`
	Rejected   bool     `json:"rejected"`
	Rejections []string `json:"rejections"`
}

// SearchReleases runs an interactive release search for a movie. Indexer
// queries can take well over the normal timeout, so a longer one is used.
func (c *Client) SearchReleases(movieID int) ([]Release, error) {
	searchClient := &http.Client{
		Transport:     httpx.Internal(),
		Timeout:       120 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	var releases []Release
	path := fmt.Sprintf("/api/v3/release?movieId=%d", movieID)
	if err := c.doWith(searchClient, "GET", path, nil, &releases); err != nil {
		return nil, fmt.Errorf("radarr release search: %w", err)
	}
	return releases, nil
}

// GrabRelease tells Radarr to send a previously searched release to the
// download client.
func (c *Client) GrabRelease(guid string, indexerID int) error {
	body := map[string]any{"guid": guid, "indexerId": indexerID}
	if err := c.do("POST", "/api/v3/release", body, nil); err != nil {
		return fmt.Errorf("radarr grab release: %w", err)
	}
	return nil
}

// SetMoviesMonitored sets the monitored flag on the given movies via Radarr's
// bulk movie editor, which applies only the fields present in the payload (no
// full-object round-trip needed).
func (c *Client) SetMoviesMonitored(movieIDs []int, monitored bool) error {
	if len(movieIDs) == 0 {
		return nil
	}
	body := map[string]any{"movieIds": movieIDs, "monitored": monitored}
	if err := c.do("PUT", "/api/v3/movie/editor", body, nil); err != nil {
		return fmt.Errorf("radarr set movies monitored: %w", err)
	}
	return nil
}

// triggerCommand posts a command payload to Radarr's command endpoint.
func (c *Client) triggerCommand(payload map[string]any) error {
	if err := c.do("POST", "/api/v3/command", payload, nil); err != nil {
		return fmt.Errorf("radarr command: %w", err)
	}
	return nil
}

// TriggerMoviesSearch starts an automatic search for the given movies.
func (c *Client) TriggerMoviesSearch(movieIDs []int) error {
	return c.triggerCommand(map[string]any{"name": "MoviesSearch", "movieIds": movieIDs})
}

// TriggerRefreshMovie refreshes metadata and rescans files for a movie.
func (c *Client) TriggerRefreshMovie(movieID int) error {
	return c.triggerCommand(map[string]any{"name": "RefreshMovie", "movieIds": []int{movieID}})
}

// TriggerRssSync runs an RSS sync across all indexers.
func (c *Client) TriggerRssSync() error {
	return c.triggerCommand(map[string]any{"name": "RssSync"})
}

// libraryFetchClient allows the much longer round-trips of a full-library
// fetch, whose serve time grows with library size (matching the app's 120s
// ceiling for the same endpoint). The normal 30s client fails closed on
// libraries big enough to matter.
func libraryFetchClient() *http.Client {
	return &http.Client{
		Transport:     httpx.Internal(),
		Timeout:       120 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// GetMovies lists all movies in the Radarr library.
func (c *Client) GetMovies() ([]Movie, error) {
	var movies []Movie
	if err := c.doWith(libraryFetchClient(), "GET", "/api/v3/movie", nil, &movies); err != nil {
		return nil, fmt.Errorf("radarr movies: %w", err)
	}
	return movies, nil
}

type DiskSpace struct {
	Path       string `json:"path"`
	Label      string `json:"label"`
	FreeSpace  int64  `json:"freeSpace"`
	TotalSpace int64  `json:"totalSpace"`
}

// GetDiskSpace reports disk usage for Radarr's mounted volumes.
func (c *Client) GetDiskSpace() ([]DiskSpace, error) {
	var disks []DiskSpace
	if err := c.do("GET", "/api/v3/diskspace", nil, &disks); err != nil {
		return nil, fmt.Errorf("radarr diskspace: %w", err)
	}
	return disks, nil
}

// HealthCheck is one entry from Radarr's system health report: a config-level
// problem (download client unreachable, remote path mapping, indexers down, no
// root folder, low disk, etc.). Type is one of ok/notice/warning/error.
type HealthCheck struct {
	Source  string `json:"source"`
	Type    string `json:"type"`
	Message string `json:"message"`
	WikiURL string `json:"wikiUrl"`
}

// GetHealth returns Radarr's current system health checks. These surface
// config-level root causes (download client down, remote path mapping wrong,
// indexers unavailable) that per-item queue diagnosis can only guess at.
func (c *Client) GetHealth() ([]HealthCheck, error) {
	var checks []HealthCheck
	if err := c.do("GET", "/api/v3/health", nil, &checks); err != nil {
		return nil, fmt.Errorf("radarr health: %w", err)
	}
	return checks, nil
}

// ManualImportRejection is a single reason Radarr would not auto-import a file,
// plus whether the rejection is permanent (a force import will likely still
// fail) or temporary.
type ManualImportRejection struct {
	Reason string `json:"reason"`
	Type   string `json:"type"`
}

// ManualImportCandidate is a file Radarr found for a download, as returned by
// GET /manualimport. Quality and Languages are kept as raw JSON so they can be
// round-tripped verbatim back into the ManualImport command (modeling and
// re-serializing them risks losing fields Radarr requires).
type ManualImportCandidate struct {
	ID           int                     `json:"id"`
	Path         string                  `json:"path"`
	FolderName   string                  `json:"folderName"`
	Name         string                  `json:"name"`
	Size         int64                   `json:"size"`
	MovieID      int                     `json:"-"`
	Quality      json.RawMessage         `json:"quality"`
	Languages    json.RawMessage         `json:"languages"`
	ReleaseGroup string                  `json:"releaseGroup"`
	DownloadID   string                  `json:"downloadId"`
	IndexerFlags int                     `json:"indexerFlags"`
	Rejections   []ManualImportRejection `json:"rejections"`
}

// UnmarshalJSON decodes a manual-import candidate, lifting the nested movie id
// (Radarr nests it under "movie": {"id": ...}) into MovieID.
func (m *ManualImportCandidate) UnmarshalJSON(data []byte) error {
	type alias ManualImportCandidate
	aux := struct {
		*alias
		Movie *struct {
			ID int `json:"id"`
		} `json:"movie"`
	}{alias: (*alias)(m)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.Movie != nil {
		m.MovieID = aux.Movie.ID
	}
	return nil
}

// GetManualImportCandidates lists the files Radarr found for a download,
// including any rejection reasons, without importing existing files.
func (c *Client) GetManualImportCandidates(downloadID string) ([]ManualImportCandidate, error) {
	var candidates []ManualImportCandidate
	path := fmt.Sprintf("/api/v3/manualimport?downloadId=%s&filterExistingFiles=false", url.QueryEscape(downloadID))
	if err := c.do("GET", path, nil, &candidates); err != nil {
		return nil, fmt.Errorf("radarr manual import candidates: %w", err)
	}
	return candidates, nil
}

// ManualImportFile is one file to import via the ManualImport command. Quality
// and Languages are passed back verbatim from the candidate.
type ManualImportFile struct {
	Path         string          `json:"path"`
	FolderName   string          `json:"folderName,omitempty"`
	MovieID      int             `json:"movieId"`
	Quality      json.RawMessage `json:"quality,omitempty"`
	Languages    json.RawMessage `json:"languages,omitempty"`
	ReleaseGroup string          `json:"releaseGroup,omitempty"`
	DownloadID   string          `json:"downloadId,omitempty"`
	IndexerFlags int             `json:"indexerFlags,omitempty"`
}

// ExecuteManualImport tells Radarr to import the given files. importMode must be
// lowercase (move/copy/auto); the PascalCase form is silently ignored by the
// ManualImport command.
func (c *Client) ExecuteManualImport(files []ManualImportFile, importMode string) error {
	payload := map[string]any{
		"name":       "ManualImport",
		"importMode": importMode,
		"files":      files,
	}
	return c.triggerCommand(payload)
}

// ProcessMonitoredDownloads asks Radarr to run its import pass over the
// download client now (the pass that normally runs on a timer).
func (c *Client) ProcessMonitoredDownloads() error {
	return c.triggerCommand(map[string]any{"name": "ProcessMonitoredDownloads"})
}

// RescanMovie rescans the files on disk for a movie.
func (c *Client) RescanMovie(movieID int) error {
	return c.triggerCommand(map[string]any{"name": "RescanMovie", "movieIds": []int{movieID}})
}

// GetConfigSummary returns a bounded, secret-free summary of one settings
// section. The raw payloads (which carry API keys and passwords in their
// dynamic fields) are summarized HERE and never leave the client.
func (c *Client) GetConfigSummary(section string) ([]arrcommon.ConfigEntry, error) {
	paths := map[string]string{
		arrcommon.ConfigIndexers:           "/api/v3/indexer",
		arrcommon.ConfigDelayProfiles:      "/api/v3/delayprofile",
		arrcommon.ConfigReleaseProfiles:    "/api/v3/releaseprofile",
		arrcommon.ConfigDownloadClients:    "/api/v3/downloadclient",
		arrcommon.ConfigRemotePathMappings: "/api/v3/remotepathmapping",
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
