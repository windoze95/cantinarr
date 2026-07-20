package sonarr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/transporterr"
)

// ErrCustomFormatsNotFound reports a 404 from the custom format endpoint. It
// is deliberately not called "unsupported": Sonarr v3 genuinely lacks the
// endpoint (custom formats arrived in v4), but an instance URL missing the
// service's URL base 404s identically, so callers must present both causes
// rather than diagnose one.
var ErrCustomFormatsNotFound = errors.New("sonarr: the custom format endpoint returned 404")

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

type Series struct {
	ID             int               `json:"id"`
	Title          string            `json:"title"`
	TvdbID         int               `json:"tvdbId"`
	TmdbID         int               `json:"tmdbId"`
	Year           int               `json:"year"`
	Monitored      bool              `json:"monitored"`
	RootFolderPath string            `json:"rootFolderPath,omitempty"`
	Statistics     *SeriesStatistics `json:"statistics,omitempty"`
	// Seasons carries Sonarr's per-season monitoring + statistics, used to
	// drive per-season availability in the request UI.
	Seasons []SeasonResource `json:"seasons,omitempty"`
}

// SeriesStatistics is the series-wide episode rollup Sonarr returns on a
// series. Episode counts only cover monitored episodes (plus ones with files),
// so PercentOfEpisodes reads 100 for a series whose few monitored episodes are
// all downloaded — availability checks that must not be fooled by partial
// monitoring should use TotalEpisodeCount.
type SeriesStatistics struct {
	EpisodeFileCount  int     `json:"episodeFileCount"`
	EpisodeCount      int     `json:"episodeCount"`
	TotalEpisodeCount int     `json:"totalEpisodeCount"`
	SizeOnDisk        int64   `json:"sizeOnDisk"`
	PercentOfEpisodes float64 `json:"percentOfEpisodes"`
}

// SeasonResource is one entry of a series' seasons[] array: its number, whether
// Sonarr is monitoring it, and (when the series is in the library) its episode
// statistics.
type SeasonResource struct {
	SeasonNumber int               `json:"seasonNumber"`
	Monitored    bool              `json:"monitored"`
	Statistics   *SeasonStatistics `json:"statistics,omitempty"`
}

// SeasonStatistics is the per-season episode rollup Sonarr returns on a season.
type SeasonStatistics struct {
	EpisodeFileCount  int     `json:"episodeFileCount"`
	EpisodeCount      int     `json:"episodeCount"`
	TotalEpisodeCount int     `json:"totalEpisodeCount"`
	SizeOnDisk        int64   `json:"sizeOnDisk"`
	PercentOfEpisodes float64 `json:"percentOfEpisodes"`
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
	// Seasons is the season list Sonarr's metadata knows for the series, used
	// to seed per-season monitored flags on an explicit-season add.
	Seasons []SeasonResource `json:"seasons"`
}

type AddSeriesRequest struct {
	Title            string `json:"title"`
	TvdbID           int    `json:"tvdbId"`
	Year             int    `json:"year"`
	QualityProfileID int    `json:"qualityProfileId"`
	RootFolderPath   string `json:"rootFolderPath"`
	Monitored        bool   `json:"monitored"`
	SeasonFolder     bool   `json:"seasonFolder"`
	// Seasons carries explicit per-season monitored flags applied at add time.
	// Sonarr keeps them through its add + metadata refresh, so this is the
	// reliable way to add a series watching an arbitrary set of seasons; leave
	// AddOptions.Monitor empty when using it (Sonarr then monitors episodes to
	// match the season flags).
	Seasons    []SeasonResource `json:"seasons,omitempty"`
	AddOptions struct {
		SearchForMissingEpisodes bool `json:"searchForMissingEpisodes"`
		// Monitor is Sonarr's monitor scope applied at add time: one of
		// all/future/missing/existing/firstSeason/lastSeason/pilot/none.
		// Empty means episode monitoring follows the seasons[].monitored flags.
		Monitor string `json:"monitor,omitempty"`
	} `json:"addOptions"`
}

type QueueItem struct {
	SeriesID int     `json:"seriesId"`
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
		return fmt.Errorf("sonarr %s %s: %s", method, requestPath, transporterr.Summarize(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		requestPath, _, _ := strings.Cut(path, "?")
		return fmt.Errorf("sonarr %s %s returned status %d", method, requestPath, resp.StatusCode)
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
	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Host-free, like doWith: transport errors embed the full request URL.
		requestPath, _, _ := strings.Cut(path, "?")
		return nil, fmt.Errorf("sonarr %s %s: %s", method, requestPath, transporterr.Summarize(err))
	}
	return resp, nil
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

// GetQualityProfilesRaw returns every quality profile exactly as Sonarr sent
// it. Settings objects must round-trip verbatim on a future PUT (modeling and
// re-serializing them risks losing fields Sonarr requires), so callers decode
// only the fields they need from each raw object.
func (c *Client) GetQualityProfilesRaw() ([]json.RawMessage, error) {
	resp, err := c.doRequest("GET", "/api/v3/qualityprofile")
	if err != nil {
		return nil, fmt.Errorf("sonarr quality profiles: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("sonarr GET /api/v3/qualityprofile returned status %d", resp.StatusCode)
	}
	var profiles []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&profiles); err != nil {
		return nil, fmt.Errorf("decode quality profiles: %w", err)
	}
	return profiles, nil
}

// GetCustomFormatsRaw returns every custom format exactly as Sonarr sent it,
// verbatim for the same round-trip reason as GetQualityProfilesRaw. A 404
// maps to ErrCustomFormatsNotFound.
func (c *Client) GetCustomFormatsRaw() ([]json.RawMessage, error) {
	resp, err := c.doRequest("GET", "/api/v3/customformat")
	if err != nil {
		return nil, fmt.Errorf("sonarr custom formats: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrCustomFormatsNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("sonarr GET /api/v3/customformat returned status %d", resp.StatusCode)
	}
	var formats []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&formats); err != nil {
		return nil, fmt.Errorf("decode custom formats: %w", err)
	}
	return formats, nil
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
		// Host-free: this error reaches requesters through request failures.
		return fmt.Errorf("sonarr add series: %s", transporterr.Summarize(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("sonarr add series returned status %d", resp.StatusCode)
	}
	return nil
}

// UpdateSeriesMonitoring sets a series' monitored flag and the monitored flag
// of each season present in seasonMonitored (seasons absent from the map keep
// their current flag). It round-trips the full series JSON (GET → patch → PUT)
// so no fields Sonarr requires are dropped. Sonarr's series update also syncs
// episode monitoring for any season whose flag CHANGES — but only for those, so
// callers that need episodes of an already-monitored season watched must set
// them explicitly via SetEpisodesMonitored.
//
// Deliberately NOT implemented with /seasonpass: that endpoint requires a
// monitoringOptions.monitor scope, and every scope rewrites episode monitoring
// series-wide ("none" even unmonitors the series and every episode in it).
func (c *Client) UpdateSeriesMonitoring(seriesID int, seriesMonitored bool, seasonMonitored map[int]bool) error {
	var raw map[string]json.RawMessage
	if err := c.do("GET", fmt.Sprintf("/api/v3/series/%d", seriesID), nil, &raw); err != nil {
		return fmt.Errorf("sonarr get series: %w", err)
	}
	monitored, err := json.Marshal(seriesMonitored)
	if err != nil {
		return fmt.Errorf("encode monitored flag: %w", err)
	}
	raw["monitored"] = monitored
	if seasonsRaw, ok := raw["seasons"]; ok {
		var seasons []map[string]json.RawMessage
		if err := json.Unmarshal(seasonsRaw, &seasons); err != nil {
			return fmt.Errorf("decode series seasons: %w", err)
		}
		for _, season := range seasons {
			if season == nil {
				continue
			}
			var num int
			if err := json.Unmarshal(season["seasonNumber"], &num); err != nil {
				continue
			}
			if want, ok := seasonMonitored[num]; ok {
				flag, err := json.Marshal(want)
				if err != nil {
					return fmt.Errorf("encode season monitored flag: %w", err)
				}
				season["monitored"] = flag
			}
		}
		patched, err := json.Marshal(seasons)
		if err != nil {
			return fmt.Errorf("encode series seasons: %w", err)
		}
		raw["seasons"] = patched
	}
	if err := c.do("PUT", fmt.Sprintf("/api/v3/series/%d", seriesID), raw, nil); err != nil {
		return fmt.Errorf("sonarr update series: %w", err)
	}
	return nil
}

// SetEpisodesMonitored sets the monitored flag on the given episodes. A no-op
// for an empty id list.
func (c *Client) SetEpisodesMonitored(episodeIDs []int, monitored bool) error {
	if len(episodeIDs) == 0 {
		return nil
	}
	body := map[string]any{"episodeIds": episodeIDs, "monitored": monitored}
	if err := c.do("PUT", "/api/v3/episode/monitor", body, nil); err != nil {
		return fmt.Errorf("sonarr set episodes monitored: %w", err)
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
	TmdbID int    `json:"tmdbId"`
}

// EpisodeContext is the lean episode object embedded in queue/history records.
type EpisodeContext struct {
	ID            int    `json:"id"`
	SeriesID      int    `json:"seriesId"`
	SeasonNumber  int    `json:"seasonNumber"`
	EpisodeNumber int    `json:"episodeNumber"`
	EpisodeFileID *int   `json:"episodeFileId"`
	HasFile       *bool  `json:"hasFile"`
	Title         string `json:"title"`
}

type DetailedQueueItem struct {
	ID                    int        `json:"id"`
	SeriesID              int        `json:"seriesId"`
	EpisodeID             int        `json:"episodeId"`
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
	EpisodeHasFile        *bool      `json:"episodeHasFile"`
	ErrorMessage          string     `json:"errorMessage"`
	StatusMessages        []struct {
		Title    string   `json:"title"`
		Messages []string `json:"messages"`
	} `json:"statusMessages"`
	Series  *SeriesContext  `json:"series,omitempty"`
	Episode *EpisodeContext `json:"episode,omitempty"`
}

// FileIDAtSnapshot returns the exact embedded episode's file ID only when
// Sonarr supplied consistent queue, series, episode, and file-state identity.
// Zero means known absent. Top-level episodeHasFile alone is ambiguous.
func (item DetailedQueueItem) FileIDAtSnapshot() *int64 {
	if item.Series == nil || item.Episode == nil || item.SeriesID <= 0 || item.EpisodeID <= 0 ||
		item.Series.ID != item.SeriesID || item.Series.TvdbID <= 0 ||
		item.Episode.ID != item.EpisodeID || item.Episode.SeriesID != item.SeriesID ||
		item.Episode.EpisodeNumber <= 0 || item.Episode.EpisodeFileID == nil || *item.Episode.EpisodeFileID < 0 {
		return nil
	}
	fileID := int64(*item.Episode.EpisodeFileID)
	hasFile := fileID > 0
	if (item.Episode.HasFile != nil && *item.Episode.HasFile != hasFile) ||
		(item.EpisodeHasFile != nil && *item.EpisodeHasFile != hasFile) {
		return nil
	}
	return &fileID
}

// queueMaxRecords is both the requested single-page size and a safety cap.
// Offset pagination can mix queue generations during same-count churn, so a
// multi-page read is never accepted as an authoritative absence witness.
const queueMaxRecords = 1000

// GetQueueDetailed returns one bounded, internally consistent queue page with
// series/episode context. A server that clamps/truncates it fails closed.
func (c *Client) GetQueueDetailed() ([]DetailedQueueItem, error) {
	var resp struct {
		TotalRecords int                 `json:"totalRecords"`
		Records      []DetailedQueueItem `json:"records"`
	}
	path := fmt.Sprintf("/api/v3/queue?page=1&pageSize=%d&includeSeries=true&includeEpisode=true&sortKey=id&sortDirection=ascending", queueMaxRecords)
	if err := c.do("GET", path, nil, &resp); err != nil {
		return nil, fmt.Errorf("sonarr queue: %w", err)
	}
	if resp.TotalRecords < 0 || resp.TotalRecords > queueMaxRecords {
		return nil, fmt.Errorf("sonarr queue snapshot incomplete: invalid or oversized total %d (safety cap %d)", resp.TotalRecords, queueMaxRecords)
	}
	if len(resp.Records) != resp.TotalRecords {
		return nil, fmt.Errorf("sonarr queue snapshot incomplete: received %d of %d records in bounded page", len(resp.Records), resp.TotalRecords)
	}
	seenIDs := make(map[int]struct{})
	for _, item := range resp.Records {
		if item.ID <= 0 {
			return nil, fmt.Errorf("sonarr queue snapshot incomplete: record has invalid id")
		}
		if _, duplicate := seenIDs[item.ID]; duplicate {
			return nil, fmt.Errorf("sonarr queue snapshot incomplete: duplicate record id %d", item.ID)
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
		return fmt.Errorf("sonarr remove queue item: %w", err)
	}
	return nil
}

type HistoryRecord struct {
	ID          int64             `json:"id"`
	EpisodeID   int               `json:"episodeId"`
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

func (c *Client) GetImportHistory(episodeID int, downloadID string, pageSize int) ([]HistoryRecord, error) {
	var resp struct {
		TotalRecords int             `json:"totalRecords"`
		Records      []HistoryRecord `json:"records"`
	}
	path := fmt.Sprintf("/api/v3/history?page=1&pageSize=%d&sortKey=date&sortDirection=descending&includeSeries=true&includeEpisode=true&eventType=3&episodeId=%d&downloadId=%s",
		pageSize, episodeID, url.QueryEscape(downloadID))
	if err := c.do("GET", path, nil, &resp); err != nil {
		return nil, fmt.Errorf("sonarr import history: %w", err)
	}
	if resp.TotalRecords > pageSize {
		return nil, fmt.Errorf("sonarr import history incomplete: %d records exceeds bound %d", resp.TotalRecords, pageSize)
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
	return &http.Client{
		Timeout:       120 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
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

// HealthCheck is one entry from Sonarr's system health report: a config-level
// problem (download client unreachable, remote path mapping, indexers down, no
// root folder, low disk, etc.). Type is one of ok/notice/warning/error.
type HealthCheck struct {
	Source  string `json:"source"`
	Type    string `json:"type"`
	Message string `json:"message"`
	WikiURL string `json:"wikiUrl"`
}

// GetHealth returns Sonarr's current system health checks. These surface
// config-level root causes (download client down, remote path mapping wrong,
// indexers unavailable) that per-item queue diagnosis can only guess at.
func (c *Client) GetHealth() ([]HealthCheck, error) {
	var checks []HealthCheck
	if err := c.do("GET", "/api/v3/health", nil, &checks); err != nil {
		return nil, fmt.Errorf("sonarr health: %w", err)
	}
	return checks, nil
}

type Episode struct {
	ID            int        `json:"id"`
	SeriesID      int        `json:"seriesId"`
	SeasonNumber  int        `json:"seasonNumber"`
	EpisodeNumber int        `json:"episodeNumber"`
	Title         string     `json:"title"`
	AirDateUtc    *time.Time `json:"airDateUtc,omitempty"`
	HasFile       bool       `json:"hasFile"`
	EpisodeFileID int        `json:"episodeFileId"`
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

// GetAllEpisodes lists every episode of a series across all seasons.
func (c *Client) GetAllEpisodes(seriesID int) ([]Episode, error) {
	var episodes []Episode
	path := fmt.Sprintf("/api/v3/episode?seriesId=%d", seriesID)
	if err := c.do("GET", path, nil, &episodes); err != nil {
		return nil, fmt.Errorf("sonarr episodes: %w", err)
	}
	return episodes, nil
}

// Completion is an on-disk completeness rollup: how many episodes have files
// versus how many are actually obtainable right now ("aired": released, or
// already on disk). Unaired episodes are excluded on purpose — a fully
// caught-up airing series is complete for availability purposes, while an
// ended series with gaps is not, regardless of what is monitored.
type Completion struct {
	Files int
	Aired int
}

// Complete reports whether every obtainable episode has a file.
func (c Completion) Complete() bool {
	return c.Aired > 0 && c.Files >= c.Aired
}

// SeriesCompletion aggregates an episode list into a series-wide completion
// plus a per-season breakdown. Specials (season 0) are excluded from the
// series-wide rollup but still reported per-season. Prefer this over
// Statistics.PercentOfEpisodes for availability decisions: percentOfEpisodes
// only counts monitored episodes, so a series with two monitored, downloaded
// episodes and everything else unmonitored reads 100%.
func SeriesCompletion(episodes []Episode, now time.Time) (series Completion, bySeason map[int]Completion) {
	bySeason = make(map[int]Completion)
	for _, e := range episodes {
		aired := e.HasFile || (e.AirDateUtc != nil && !e.AirDateUtc.After(now))
		c := bySeason[e.SeasonNumber]
		if aired {
			c.Aired++
		}
		if e.HasFile {
			c.Files++
		}
		bySeason[e.SeasonNumber] = c
		if e.SeasonNumber > 0 {
			if aired {
				series.Aired++
			}
			if e.HasFile {
				series.Files++
			}
		}
	}
	return series, bySeason
}

// EpisodeTotals returns how many episodes are on disk versus every episode
// Sonarr knows about (aired or not, monitored or not) across the series' real
// (non-Specials) seasons, from the season statistics already present on the
// series. Cheaper than SeriesCompletion (no episode fetch) but stricter: a
// caught-up airing series counts as incomplete because unaired episodes are
// included. Falls back to the series-level statistics when Sonarr sent no
// per-season breakdown.
func (s *Series) EpisodeTotals() (files, total int) {
	for _, season := range s.Seasons {
		if season.SeasonNumber <= 0 || season.Statistics == nil {
			continue
		}
		files += season.Statistics.EpisodeFileCount
		t := season.Statistics.TotalEpisodeCount
		if t == 0 {
			t = season.Statistics.EpisodeCount
		}
		total += t
	}
	if files == 0 && total == 0 && s.Statistics != nil {
		files = s.Statistics.EpisodeFileCount
		total = s.Statistics.TotalEpisodeCount
		if total == 0 {
			total = s.Statistics.EpisodeCount
		}
	}
	return files, total
}

// ManualImportRejection is a single reason Sonarr would not auto-import a file,
// plus whether the rejection is permanent (a force import will likely still
// fail) or temporary.
type ManualImportRejection struct {
	Reason string `json:"reason"`
	Type   string `json:"type"`
}

// ManualImportCandidate is a file Sonarr found for a download, as returned by
// GET /manualimport. Quality and Languages are kept as raw JSON so they can be
// round-tripped verbatim back into the ManualImport command (modeling and
// re-serializing them risks losing fields Sonarr requires).
type ManualImportCandidate struct {
	ID           int                     `json:"id"`
	Path         string                  `json:"path"`
	FolderName   string                  `json:"folderName"`
	Name         string                  `json:"name"`
	Size         int64                   `json:"size"`
	SeriesID     int                     `json:"-"`
	SeasonNumber int                     `json:"seasonNumber"`
	Episodes     []EpisodeContext        `json:"episodes"`
	Quality      json.RawMessage         `json:"quality"`
	Languages    json.RawMessage         `json:"languages"`
	ReleaseGroup string                  `json:"releaseGroup"`
	DownloadID   string                  `json:"downloadId"`
	IndexerFlags int                     `json:"indexerFlags"`
	ReleaseType  string                  `json:"releaseType"`
	Rejections   []ManualImportRejection `json:"rejections"`
}

// UnmarshalJSON decodes a manual-import candidate, lifting the nested series id
// (Sonarr nests it under "series": {"id": ...}) into SeriesID.
func (m *ManualImportCandidate) UnmarshalJSON(data []byte) error {
	type alias ManualImportCandidate
	aux := struct {
		*alias
		Series *struct {
			ID int `json:"id"`
		} `json:"series"`
	}{alias: (*alias)(m)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.Series != nil {
		m.SeriesID = aux.Series.ID
	}
	return nil
}

// GetManualImportCandidates lists the files Sonarr found for a download,
// including any rejection reasons, without importing existing files.
func (c *Client) GetManualImportCandidates(downloadID string) ([]ManualImportCandidate, error) {
	var candidates []ManualImportCandidate
	path := fmt.Sprintf("/api/v3/manualimport?downloadId=%s&filterExistingFiles=false", url.QueryEscape(downloadID))
	if err := c.do("GET", path, nil, &candidates); err != nil {
		return nil, fmt.Errorf("sonarr manual import candidates: %w", err)
	}
	return candidates, nil
}

// ManualImportFile is one file to import via the ManualImport command. Quality
// and Languages are passed back verbatim from the candidate. EpisodeIDs must be
// non-empty for Sonarr or the file is silently skipped.
type ManualImportFile struct {
	Path         string          `json:"path"`
	FolderName   string          `json:"folderName,omitempty"`
	SeriesID     int             `json:"seriesId"`
	EpisodeIDs   []int           `json:"episodeIds"`
	Quality      json.RawMessage `json:"quality,omitempty"`
	Languages    json.RawMessage `json:"languages,omitempty"`
	ReleaseGroup string          `json:"releaseGroup,omitempty"`
	DownloadID   string          `json:"downloadId,omitempty"`
	IndexerFlags int             `json:"indexerFlags,omitempty"`
	ReleaseType  string          `json:"releaseType,omitempty"`
}

// ExecuteManualImport tells Sonarr to import the given files. importMode must be
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

// ProcessMonitoredDownloads asks Sonarr to run its import pass over the
// download client now (the pass that normally runs on a timer).
func (c *Client) ProcessMonitoredDownloads() error {
	return c.triggerCommand(map[string]any{"name": "ProcessMonitoredDownloads"})
}

// RescanSeries rescans the files on disk for a series.
func (c *Client) RescanSeries(seriesID int) error {
	return c.triggerCommand(map[string]any{"name": "RescanSeries", "seriesId": seriesID})
}
