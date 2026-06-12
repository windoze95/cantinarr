package sabnzbd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to the SABnzbd JSON API:
// GET {base}/api?output=json&apikey={key}&mode=...
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient creates a new SABnzbd client.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// call performs a GET against the SABnzbd API with the given params, checks
// both the HTTP status and SABnzbd's error envelope ({"status": false,
// "error": "..."}), and decodes the body into out when out is non-nil.
func (c *Client) call(params url.Values, out interface{}) error {
	q := url.Values{}
	q.Set("output", "json")
	q.Set("apikey", c.apiKey)
	for key, values := range params {
		for _, v := range values {
			q.Add(key, v)
		}
	}

	resp, err := c.httpClient.Get(c.baseURL + "/api?" + q.Encode())
	if err != nil {
		return fmt.Errorf("sabnzbd request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return fmt.Errorf("sabnzbd read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("sabnzbd returned status %d", resp.StatusCode)
	}

	// SABnzbd reports failures with HTTP 200 and an error envelope.
	var envelope struct {
		Status *bool  `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Status != nil && !*envelope.Status {
		if envelope.Error == "" {
			envelope.Error = "unknown error"
		}
		return fmt.Errorf("sabnzbd error: %s", envelope.Error)
	}

	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("sabnzbd decode response: %w", err)
		}
	}
	return nil
}

// Version returns the SABnzbd version; used as the connection test.
func (c *Client) Version() (string, error) {
	var out struct {
		Version string `json:"version"`
	}
	if err := c.call(url.Values{"mode": {"version"}}, &out); err != nil {
		return "", err
	}
	if out.Version == "" {
		return "", fmt.Errorf("sabnzbd returned no version (wrong URL or API key?)")
	}
	return out.Version, nil
}

// QueueSlot is a single entry in the SABnzbd download queue. SABnzbd returns
// most numeric values as strings.
type QueueSlot struct {
	NzoID      string `json:"nzo_id"`
	Filename   string `json:"filename"`
	MB         string `json:"mb"`
	MBLeft     string `json:"mbleft"`
	Percentage string `json:"percentage"`
	TimeLeft   string `json:"timeleft"`
	Status     string `json:"status"`
	Category   string `json:"cat"`
}

// SizeBytes returns the total size of the item in bytes.
func (s QueueSlot) SizeBytes() int64 {
	return int64(parseFloat(s.MB) * 1024 * 1024)
}

// SizeLeftBytes returns the remaining size of the item in bytes.
func (s QueueSlot) SizeLeftBytes() int64 {
	return int64(parseFloat(s.MBLeft) * 1024 * 1024)
}

// Progress returns the completion percentage (0-100).
func (s QueueSlot) Progress() float64 {
	return parseFloat(s.Percentage)
}

// ETASeconds parses SABnzbd's "[dd:]hh:mm:ss" timeleft format into seconds.
// Returns 0 when the value is missing or unparseable.
func (s QueueSlot) ETASeconds() int64 {
	parts := strings.Split(strings.TrimSpace(s.TimeLeft), ":")
	multipliers := []int64{1, 60, 3600, 86400}
	var total int64
	for i := 0; i < len(parts) && i < len(multipliers); i++ {
		n, err := strconv.ParseInt(strings.TrimSpace(parts[len(parts)-1-i]), 10, 64)
		if err != nil {
			return 0
		}
		total += n * multipliers[i]
	}
	return total
}

// Queue is the SABnzbd download queue.
type Queue struct {
	Slots         []QueueSlot `json:"slots"`
	KBPerSec      string      `json:"kbpersec"`
	Speed         string      `json:"speed"`
	Paused        bool        `json:"paused"`
	SpeedLimitAbs string      `json:"speedlimit_abs"`
}

// SpeedBPS returns the current overall download speed in bytes per second.
func (q Queue) SpeedBPS() int64 {
	return int64(parseFloat(q.KBPerSec) * 1024)
}

// GetQueue fetches the current download queue.
func (c *Client) GetQueue() (*Queue, error) {
	var out struct {
		Queue Queue `json:"queue"`
	}
	if err := c.call(url.Values{"mode": {"queue"}}, &out); err != nil {
		return nil, err
	}
	return &out.Queue, nil
}

// PauseQueue pauses the entire download queue.
func (c *Client) PauseQueue() error {
	return c.call(url.Values{"mode": {"pause"}}, nil)
}

// ResumeQueue resumes the entire download queue.
func (c *Client) ResumeQueue() error {
	return c.call(url.Values{"mode": {"resume"}}, nil)
}

// PauseItem pauses a single queue item by nzo_id.
func (c *Client) PauseItem(nzoID string) error {
	return c.call(url.Values{"mode": {"queue"}, "name": {"pause"}, "value": {nzoID}}, nil)
}

// ResumeItem resumes a single queue item by nzo_id.
func (c *Client) ResumeItem(nzoID string) error {
	return c.call(url.Values{"mode": {"queue"}, "name": {"resume"}, "value": {nzoID}}, nil)
}

// DeleteItem removes a queue item by nzo_id, optionally deleting downloaded files.
func (c *Client) DeleteItem(nzoID string, deleteFiles bool) error {
	delFiles := "0"
	if deleteFiles {
		delFiles = "1"
	}
	return c.call(url.Values{
		"mode":      {"queue"},
		"name":      {"delete"},
		"value":     {nzoID},
		"del_files": {delFiles},
	}, nil)
}

// HistorySlot is a single entry in SABnzbd's download history.
type HistorySlot struct {
	Name        string  `json:"name"`
	Status      string  `json:"status"`
	FailMessage string  `json:"fail_message"`
	Bytes       float64 `json:"bytes"`
	Size        string  `json:"size"`
	Completed   int64   `json:"completed"`
	Category    string  `json:"category"`
}

// GetHistory fetches the most recent history entries, up to limit (0 = server default).
func (c *Client) GetHistory(limit int) ([]HistorySlot, error) {
	params := url.Values{"mode": {"history"}}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}
	var out struct {
		History struct {
			Slots []HistorySlot `json:"slots"`
		} `json:"history"`
	}
	if err := c.call(params, &out); err != nil {
		return nil, err
	}
	return out.History.Slots, nil
}

// SetSpeedLimit sets the download speed limit. The value may be a percentage
// (e.g. "50") or an absolute rate (e.g. "4M", "512K").
func (c *Client) SetSpeedLimit(value string) error {
	return c.call(url.Values{"mode": {"config"}, "name": {"speedlimit"}, "value": {value}}, nil)
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}
