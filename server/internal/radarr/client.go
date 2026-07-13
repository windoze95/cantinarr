package radarr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

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
		return err
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
	req, err := http.NewRequest(method, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	return c.httpClient.Do(req)
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
		return fmt.Errorf("radarr add movie: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("radarr add movie returned status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) GetQueue() ([]QueueItem, error) {
	resp, err := c.doRequest("GET", "/api/v3/queue?includeMovie=true")
	if err != nil {
		return nil, fmt.Errorf("radarr queue: %w", err)
	}
	defer resp.Body.Close()

	var queueResp struct {
		Records []QueueItem `json:"records"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&queueResp); err != nil {
		return nil, fmt.Errorf("decode queue: %w", err)
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
	path := fmt.Sprintf("/api/v3/queue?page=1&pageSize=%d&includeMovie=true&sortKey=id&sortDirection=ascending", queueMaxRecords)
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

// GetMovies lists all movies in the Radarr library.
func (c *Client) GetMovies() ([]Movie, error) {
	var movies []Movie
	if err := c.do("GET", "/api/v3/movie", nil, &movies); err != nil {
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
