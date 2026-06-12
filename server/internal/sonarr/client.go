package sonarr

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

type Series struct {
	ID             int    `json:"id"`
	Title          string `json:"title"`
	TvdbID         int    `json:"tvdbId"`
	TmdbID         int    `json:"tmdbId"`
	Year           int    `json:"year"`
	Monitored      bool   `json:"monitored"`
	RootFolderPath string `json:"rootFolderPath,omitempty"`
	Statistics     *struct {
		EpisodeFileCount  int     `json:"episodeFileCount"`
		EpisodeCount      int     `json:"episodeCount"`
		PercentOfEpisodes float64 `json:"percentOfEpisodes"`
	} `json:"statistics,omitempty"`
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
	TvdbID int    `json:"tvdbId"`
	Year   int    `json:"year"`
	Images []struct {
		CoverType string `json:"coverType"`
		RemoteURL string `json:"remoteUrl"`
	} `json:"images"`
}

type AddSeriesRequest struct {
	Title            string `json:"title"`
	TvdbID           int    `json:"tvdbId"`
	Year             int    `json:"year"`
	QualityProfileID int    `json:"qualityProfileId"`
	RootFolderPath   string `json:"rootFolderPath"`
	Monitored        bool   `json:"monitored"`
	SeasonFolder     bool   `json:"seasonFolder"`
	AddOptions       struct {
		SearchForMissingEpisodes bool `json:"searchForMissingEpisodes"`
	} `json:"addOptions"`
}

type QueueItem struct {
	SeriesID int     `json:"seriesId"`
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
		return fmt.Errorf("sonarr %s %s returned status %d: %s",
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

func (c *Client) LookupByTVDB(tvdbID int) (*LookupResult, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/api/v3/series/lookup?term=tvdb:%d", tvdbID))
	if err != nil {
		return nil, fmt.Errorf("sonarr lookup: %w", err)
	}
	defer resp.Body.Close()

	var results []LookupResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("decode sonarr lookup: %w", err)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no results found for TVDB ID %d", tvdbID)
	}
	return &results[0], nil
}

func (c *Client) LookupByTitle(title string) (*LookupResult, error) {
	resp, err := c.doRequest("GET", "/api/v3/series/lookup?term="+url.QueryEscape(title))
	if err != nil {
		return nil, fmt.Errorf("sonarr title lookup: %w", err)
	}
	defer resp.Body.Close()

	var results []LookupResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("decode sonarr title lookup: %w", err)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no results found for title %q", title)
	}
	return &results[0], nil
}

func (c *Client) GetSeries(id int) (*Series, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/api/v3/series/%d", id))
	if err != nil {
		return nil, fmt.Errorf("sonarr get series: %w", err)
	}
	defer resp.Body.Close()

	var series Series
	if err := json.NewDecoder(resp.Body).Decode(&series); err != nil {
		return nil, fmt.Errorf("decode sonarr series: %w", err)
	}
	return &series, nil
}

func (c *Client) GetSeriesByTVDB(tvdbID int) (*Series, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/api/v3/series?tvdbId=%d", tvdbID))
	if err != nil {
		return nil, fmt.Errorf("sonarr get series: %w", err)
	}
	defer resp.Body.Close()

	var series []Series
	if err := json.NewDecoder(resp.Body).Decode(&series); err != nil {
		return nil, fmt.Errorf("decode sonarr series: %w", err)
	}
	if len(series) == 0 {
		return nil, nil
	}
	return &series[0], nil
}

func (c *Client) GetQualityProfiles() ([]QualityProfile, error) {
	resp, err := c.doRequest("GET", "/api/v3/qualityprofile")
	if err != nil {
		return nil, fmt.Errorf("sonarr quality profiles: %w", err)
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
		return nil, fmt.Errorf("sonarr root folders: %w", err)
	}
	defer resp.Body.Close()

	var folders []RootFolder
	if err := json.NewDecoder(resp.Body).Decode(&folders); err != nil {
		return nil, fmt.Errorf("decode root folders: %w", err)
	}
	return folders, nil
}

func (c *Client) AddSeries(addReq *AddSeriesRequest) error {
	body, err := json.Marshal(addReq)
	if err != nil {
		return fmt.Errorf("marshal add series: %w", err)
	}

	req, err := http.NewRequest("POST", c.baseURL+"/api/v3/series", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create add request: %w", err)
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sonarr add series: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("sonarr add series returned status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) GetQueue() ([]QueueItem, error) {
	resp, err := c.doRequest("GET", "/api/v3/queue?includeSeries=true")
	if err != nil {
		return nil, fmt.Errorf("sonarr queue: %w", err)
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

// SeriesContext is the lean series object embedded in queue/history/calendar records.
type SeriesContext struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	Year   int    `json:"year"`
	TvdbID int    `json:"tvdbId"`
}

// EpisodeContext is the lean episode object embedded in queue/history records.
type EpisodeContext struct {
	ID            int    `json:"id"`
	SeasonNumber  int    `json:"seasonNumber"`
	EpisodeNumber int    `json:"episodeNumber"`
	Title         string `json:"title"`
}

type DetailedQueueItem struct {
	ID                    int     `json:"id"`
	SeriesID              int     `json:"seriesId"`
	EpisodeID             int     `json:"episodeId"`
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
	Series  *SeriesContext  `json:"series,omitempty"`
	Episode *EpisodeContext `json:"episode,omitempty"`
}

// queuePageSize is the per-request page size for queue pagination, and
// queueMaxRecords is a safety cap on the total records accumulated.
const (
	queuePageSize   = 100
	queueMaxRecords = 1000
)

// GetQueueDetailed returns the full download queue with series and episode
// context, paginating until all records are fetched (capped at queueMaxRecords).
func (c *Client) GetQueueDetailed() ([]DetailedQueueItem, error) {
	var all []DetailedQueueItem
	for page := 1; ; page++ {
		var resp struct {
			TotalRecords int                 `json:"totalRecords"`
			Records      []DetailedQueueItem `json:"records"`
		}
		path := fmt.Sprintf("/api/v3/queue?page=%d&pageSize=%d&includeSeries=true&includeEpisode=true", page, queuePageSize)
		if err := c.do("GET", path, nil, &resp); err != nil {
			return nil, fmt.Errorf("sonarr queue: %w", err)
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
		return fmt.Errorf("sonarr remove queue item: %w", err)
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
	Series  *SeriesContext  `json:"series,omitempty"`
	Episode *EpisodeContext `json:"episode,omitempty"`
}

// GetHistory returns the most recent history records (grabs, imports, failures).
func (c *Client) GetHistory(pageSize int) ([]HistoryRecord, error) {
	var resp struct {
		Records []HistoryRecord `json:"records"`
	}
	path := fmt.Sprintf("/api/v3/history?page=1&pageSize=%d&sortKey=date&sortDirection=descending&includeSeries=true&includeEpisode=true", pageSize)
	if err := c.do("GET", path, nil, &resp); err != nil {
		return nil, fmt.Errorf("sonarr history: %w", err)
	}
	return resp.Records, nil
}

type CalendarItem struct {
	ID            int            `json:"id"`
	SeriesID      int            `json:"seriesId"`
	SeasonNumber  int            `json:"seasonNumber"`
	EpisodeNumber int            `json:"episodeNumber"`
	Title         string         `json:"title"`
	AirDateUtc    *time.Time     `json:"airDateUtc,omitempty"`
	HasFile       bool           `json:"hasFile"`
	Monitored     bool           `json:"monitored"`
	Series        *SeriesContext `json:"series,omitempty"`
}

// GetCalendar returns monitored episodes airing in [start, end].
func (c *Client) GetCalendar(start, end time.Time) ([]CalendarItem, error) {
	path := fmt.Sprintf("/api/v3/calendar?start=%s&end=%s&unmonitored=false&includeSeries=true",
		url.QueryEscape(start.UTC().Format(time.RFC3339)),
		url.QueryEscape(end.UTC().Format(time.RFC3339)))
	var items []CalendarItem
	if err := c.do("GET", path, nil, &items); err != nil {
		return nil, fmt.Errorf("sonarr calendar: %w", err)
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

// releaseSearchClient allows the much longer round-trips of interactive
// release searches, which query every configured indexer.
func releaseSearchClient() *http.Client {
	return &http.Client{Timeout: 120 * time.Second}
}

// SearchReleases runs an interactive release search for a season of a series.
func (c *Client) SearchReleases(seriesID, seasonNumber int) ([]Release, error) {
	var releases []Release
	path := fmt.Sprintf("/api/v3/release?seriesId=%d&seasonNumber=%d", seriesID, seasonNumber)
	if err := c.doWith(releaseSearchClient(), "GET", path, nil, &releases); err != nil {
		return nil, fmt.Errorf("sonarr release search: %w", err)
	}
	return releases, nil
}

// SearchEpisodeReleases runs an interactive release search for a single episode.
func (c *Client) SearchEpisodeReleases(episodeID int) ([]Release, error) {
	var releases []Release
	path := fmt.Sprintf("/api/v3/release?episodeId=%d", episodeID)
	if err := c.doWith(releaseSearchClient(), "GET", path, nil, &releases); err != nil {
		return nil, fmt.Errorf("sonarr episode release search: %w", err)
	}
	return releases, nil
}

// GrabRelease tells Sonarr to send a previously searched release to the
// download client.
func (c *Client) GrabRelease(guid string, indexerID int) error {
	body := map[string]any{"guid": guid, "indexerId": indexerID}
	if err := c.do("POST", "/api/v3/release", body, nil); err != nil {
		return fmt.Errorf("sonarr grab release: %w", err)
	}
	return nil
}

// triggerCommand posts a command payload to Sonarr's command endpoint.
func (c *Client) triggerCommand(payload map[string]any) error {
	if err := c.do("POST", "/api/v3/command", payload, nil); err != nil {
		return fmt.Errorf("sonarr command: %w", err)
	}
	return nil
}

// TriggerSeriesSearch starts an automatic search for all monitored episodes of a series.
func (c *Client) TriggerSeriesSearch(seriesID int) error {
	return c.triggerCommand(map[string]any{"name": "SeriesSearch", "seriesId": seriesID})
}

// TriggerSeasonSearch starts an automatic search for a season.
func (c *Client) TriggerSeasonSearch(seriesID, seasonNumber int) error {
	return c.triggerCommand(map[string]any{"name": "SeasonSearch", "seriesId": seriesID, "seasonNumber": seasonNumber})
}

// TriggerEpisodeSearch starts an automatic search for specific episodes.
func (c *Client) TriggerEpisodeSearch(episodeIDs []int) error {
	return c.triggerCommand(map[string]any{"name": "EpisodeSearch", "episodeIds": episodeIDs})
}

// TriggerRefreshSeries refreshes metadata and rescans files for a series.
func (c *Client) TriggerRefreshSeries(seriesID int) error {
	return c.triggerCommand(map[string]any{"name": "RefreshSeries", "seriesId": seriesID})
}

// TriggerRssSync runs an RSS sync across all indexers.
func (c *Client) TriggerRssSync() error {
	return c.triggerCommand(map[string]any{"name": "RssSync"})
}

// GetAllSeries lists every series in the Sonarr library.
func (c *Client) GetAllSeries() ([]Series, error) {
	var series []Series
	if err := c.do("GET", "/api/v3/series", nil, &series); err != nil {
		return nil, fmt.Errorf("sonarr series list: %w", err)
	}
	return series, nil
}

type DiskSpace struct {
	Path       string `json:"path"`
	Label      string `json:"label"`
	FreeSpace  int64  `json:"freeSpace"`
	TotalSpace int64  `json:"totalSpace"`
}

// GetDiskSpace reports disk usage for Sonarr's mounted volumes.
func (c *Client) GetDiskSpace() ([]DiskSpace, error) {
	var disks []DiskSpace
	if err := c.do("GET", "/api/v3/diskspace", nil, &disks); err != nil {
		return nil, fmt.Errorf("sonarr diskspace: %w", err)
	}
	return disks, nil
}

type Episode struct {
	ID            int        `json:"id"`
	SeriesID      int        `json:"seriesId"`
	SeasonNumber  int        `json:"seasonNumber"`
	EpisodeNumber int        `json:"episodeNumber"`
	Title         string     `json:"title"`
	AirDateUtc    *time.Time `json:"airDateUtc,omitempty"`
	HasFile       bool       `json:"hasFile"`
	Monitored     bool       `json:"monitored"`
}

// GetEpisodes lists the episodes of one season of a series.
func (c *Client) GetEpisodes(seriesID, seasonNumber int) ([]Episode, error) {
	var episodes []Episode
	path := fmt.Sprintf("/api/v3/episode?seriesId=%d&seasonNumber=%d", seriesID, seasonNumber)
	if err := c.do("GET", path, nil, &episodes); err != nil {
		return nil, fmt.Errorf("sonarr episodes: %w", err)
	}
	return episodes, nil
}
