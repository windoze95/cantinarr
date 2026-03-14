package sonarr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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
		EpisodeFileCount int `json:"episodeFileCount"`
		EpisodeCount     int `json:"episodeCount"`
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
