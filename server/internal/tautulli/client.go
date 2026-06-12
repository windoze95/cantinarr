// Package tautulli provides a client and REST handler for Tautulli (Plex
// monitoring) instances.
package tautulli

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

// Client talks to the Tautulli API v2:
// GET {base}/api/v2?apikey={key}&cmd=...
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient creates a new Tautulli client.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// call performs a GET against the Tautulli API, checks both the HTTP status
// and Tautulli's response envelope ({"response":{"result":"success",...}}),
// and decodes the data field into out when out is non-nil.
func (c *Client) call(cmd string, params url.Values, out interface{}) error {
	q := url.Values{}
	q.Set("apikey", c.apiKey)
	q.Set("cmd", cmd)
	for key, values := range params {
		for _, v := range values {
			q.Add(key, v)
		}
	}

	resp, err := c.httpClient.Get(c.baseURL + "/api/v2?" + q.Encode())
	if err != nil {
		// Transport errors (*url.Error) embed the full request URL including
		// the apikey query parameter; redact it so the secret can never leak
		// into logs or API error responses.
		return fmt.Errorf("tautulli request: %s", redactSecret(err.Error(), c.apiKey))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return fmt.Errorf("tautulli read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("tautulli returned status %d", resp.StatusCode)
	}

	var envelope struct {
		Response struct {
			Result  string          `json:"result"`
			Message string          `json:"message"`
			Data    json.RawMessage `json:"data"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("tautulli decode response: %w", err)
	}
	if envelope.Response.Result != "success" {
		msg := envelope.Response.Message
		if msg == "" {
			msg = "unknown error"
		}
		return fmt.Errorf("tautulli %s: %s", cmd, msg)
	}

	if out != nil && len(envelope.Response.Data) > 0 {
		if err := json.Unmarshal(envelope.Response.Data, out); err != nil {
			return fmt.Errorf("tautulli decode data: %w", err)
		}
	}
	return nil
}

// flexInt is an int64 that tolerates Tautulli's habit of encoding numbers as
// strings (and occasionally empty strings) in JSON.
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		*f = 0
		return nil
	}
	*f = flexInt(n)
	return nil
}

// ServerInfo is the subset of get_server_info used here.
type ServerInfo struct {
	PMSName    string `json:"pms_name"`
	PMSVersion string `json:"pms_version"`
}

// GetServerInfo returns the connected Plex server info; used as the
// connection test.
func (c *Client) GetServerInfo() (*ServerInfo, error) {
	var info ServerInfo
	if err := c.call("get_server_info", nil, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// Session is a single active stream from get_activity.
type Session struct {
	User              string  `json:"user"`
	Title             string  `json:"title"`
	FullTitle         string  `json:"full_title"`
	Player            string  `json:"player"`
	Product           string  `json:"product"`
	State             string  `json:"state"` // playing/paused/buffering
	ProgressPercent   flexInt `json:"progress_percent"`
	QualityProfile    string  `json:"quality_profile"`
	TranscodeDecision string  `json:"transcode_decision"` // direct play/copy/transcode
	Bandwidth         flexInt `json:"bandwidth"`          // kbps
}

// Activity is the current streaming activity from get_activity.
type Activity struct {
	StreamCount    flexInt   `json:"stream_count"`
	TotalBandwidth flexInt   `json:"total_bandwidth"` // kbps
	Sessions       []Session `json:"sessions"`
}

// GetActivity returns the current streaming activity.
func (c *Client) GetActivity() (*Activity, error) {
	var activity Activity
	if err := c.call("get_activity", nil, &activity); err != nil {
		return nil, err
	}
	return &activity, nil
}

// HistoryRow is a single watch-history entry from get_history.
type HistoryRow struct {
	User            string  `json:"user"`
	FullTitle       string  `json:"full_title"`
	Date            flexInt `json:"date"`     // unix seconds
	Duration        flexInt `json:"duration"` // seconds played
	PercentComplete flexInt `json:"percent_complete"`
	Player          string  `json:"player"`
	Platform        string  `json:"platform"`
}

// GetHistory returns the most recent watch-history entries, up to length
// (0 = server default).
func (c *Client) GetHistory(length int) ([]HistoryRow, error) {
	params := url.Values{}
	if length > 0 {
		params.Set("length", strconv.Itoa(length))
	}
	var out struct {
		Data []HistoryRow `json:"data"`
	}
	if err := c.call("get_history", params, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// HomeStatRow is a single row within a get_home_stats stat block.
type HomeStatRow struct {
	Title        string  `json:"title"`
	User         string  `json:"user"`
	FriendlyName string  `json:"friendly_name"`
	TotalPlays   flexInt `json:"total_plays"`
}

// HomeStat is one stat block (e.g. top_movies) from get_home_stats.
type HomeStat struct {
	StatID string        `json:"stat_id"`
	Rows   []HomeStatRow `json:"rows"`
}

// GetHomeStats returns the home statistics over the given number of days.
func (c *Client) GetHomeStats(days int) ([]HomeStat, error) {
	params := url.Values{}
	if days > 0 {
		params.Set("time_range", strconv.Itoa(days))
	}
	var stats []HomeStat
	if err := c.call("get_home_stats", params, &stats); err != nil {
		return nil, err
	}
	return stats, nil
}

// redactSecret removes a secret value from an error string before it can
// escape into logs or HTTP responses.
func redactSecret(msg, secret string) string {
	if secret == "" {
		return msg
	}
	return strings.ReplaceAll(msg, secret, "[redacted]")
}
