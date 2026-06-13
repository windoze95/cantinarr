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
			Timeout: 30 * time.Second,
		},
	}
}

type Movie struct {
	ID             int    `json:"id"`
	Title          string `json:"title"`
	TmdbID         int    `json:"tmdbId"`
	Year           int    `json:"year"`
	HasFile        bool   `json:"hasFile"`
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

// do executes a request with an optional JSON body, fails on non-2xx status
// (including a snippet of the response body), and decodes JSON into out when
// out is non-nil.
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
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("radarr %s %s returned status %d: %s",
			method, strings.SplitN(path, "?", 2)[0], resp.StatusCode, strings.TrimSpace(string(snippet)))
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
	ID     int    `json:"id"`
	Title  string `json:"title"`
	Year   int    `json:"year"`
	TmdbID int    `json:"tmdbId"`
}

type DetailedQueueItem struct {
	ID                    int     `json:"id"`
	MovieID               int     `json:"movieId"`
	Title                 string  `json:"title"`
	Status                string  `json:"status"`
	TrackedDownloadStatus string  `json:"trackedDownloadStatus"`
	TrackedDownloadState  string  `json:"trackedDownloadState"`
	Timeleft              string  `json:"timeleft"`
	Size                  float64 `json:"size"`
	Sizeleft              float64 `json:"sizeleft"`
	DownloadClient        string  `json:"downloadClient"`
	Indexer               string  `json:"indexer"`
	Protocol              string  `json:"protocol"`
	ErrorMessage          string  `json:"errorMessage"`
	StatusMessages        []struct {
		Title    string   `json:"title"`
		Messages []string `json:"messages"`
	} `json:"statusMessages"`
	Movie *MovieContext `json:"movie,omitempty"`
}

// queuePageSize is the per-request page size for queue pagination, and
// queueMaxRecords is a safety cap on the total records accumulated.
const (
	queuePageSize   = 100
	queueMaxRecords = 1000
)

// GetQueueDetailed returns the full download queue with movie context,
// paginating until all records are fetched (capped at queueMaxRecords).
func (c *Client) GetQueueDetailed() ([]DetailedQueueItem, error) {
	var all []DetailedQueueItem
	for page := 1; ; page++ {
		var resp struct {
			TotalRecords int                 `json:"totalRecords"`
			Records      []DetailedQueueItem `json:"records"`
		}
		path := fmt.Sprintf("/api/v3/queue?page=%d&pageSize=%d&includeMovie=true", page, queuePageSize)
		if err := c.do("GET", path, nil, &resp); err != nil {
			return nil, fmt.Errorf("radarr queue: %w", err)
		}
		all = append(all, resp.Records...)
		if len(resp.Records) == 0 || len(all) >= resp.TotalRecords || len(all) >= queueMaxRecords {
			break
		}
	}
	if len(all) > queueMaxRecords {
		all = all[:queueMaxRecords]
	}
	return all, nil
}

// RemoveQueueItem removes an item from the download queue.
func (c *Client) RemoveQueueItem(id int, removeFromClient, blocklist bool) error {
	path := fmt.Sprintf("/api/v3/queue/%d?removeFromClient=%t&blocklist=%t", id, removeFromClient, blocklist)
	if err := c.do("DELETE", path, nil, nil); err != nil {
		return fmt.Errorf("radarr remove queue item: %w", err)
	}
	return nil
}

type HistoryRecord struct {
	EventType   string    `json:"eventType"`
	SourceTitle string    `json:"sourceTitle"`
	Date        time.Time `json:"date"`
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
	searchClient := &http.Client{Timeout: 120 * time.Second}
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
