package qbittorrent

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client talks to the qBittorrent WebUI API v2 using cookie (SID) auth.
// On a 403 from any call it re-logs-in once and retries.
type Client struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client

	mu            sync.Mutex
	cookies       []*http.Cookie
	authenticated bool
}

// NewClient creates a new qBittorrent client.
func NewClient(baseURL, username, password string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
		httpClient: &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// Login authenticates against the WebUI and stores the SID session cookie.
func (c *Client) Login() error {
	form := url.Values{"username": {c.username}, "password": {c.password}}
	req, err := http.NewRequest("POST", c.baseURL+"/api/v2/auth/login", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("qbittorrent login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", c.baseURL)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("qbittorrent login: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("qbittorrent login failed: status %d", resp.StatusCode)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(body)), "Ok") {
		return fmt.Errorf("qbittorrent login failed: invalid credentials")
	}

	// When the WebUI has auth bypass enabled (localhost/whitelisted IPs),
	// login succeeds with "Ok." but returns no SID cookie — requests work
	// without one, so an empty cookie set is not an error.
	c.mu.Lock()
	c.cookies = resp.Cookies()
	c.authenticated = true
	c.mu.Unlock()
	return nil
}

// do executes an authenticated request, logging in first when no session
// exists and re-logging-in once on a 403 response.
func (c *Client) do(method, path string, form url.Values) (*http.Response, error) {
	c.mu.Lock()
	haveSession := c.authenticated
	c.mu.Unlock()
	if !haveSession {
		if err := c.Login(); err != nil {
			return nil, err
		}
	}

	resp, err := c.doOnce(method, path, form)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusForbidden {
		resp.Body.Close()
		if err := c.Login(); err != nil {
			return nil, err
		}
		resp, err = c.doOnce(method, path, form)
		if err != nil {
			return nil, err
		}
	}
	return resp, nil
}

func (c *Client) doOnce(method, path string, form url.Values) (*http.Response, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("qbittorrent request: %w", err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Referer", c.baseURL)

	c.mu.Lock()
	for _, cookie := range c.cookies {
		req.AddCookie(cookie)
	}
	c.mu.Unlock()

	return c.httpClient.Do(req)
}

// post executes a POST and checks for a 2xx status.
func (c *Client) post(path string, form url.Values) error {
	resp, err := c.do("POST", path, form)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("qbittorrent %s: status %d", path, resp.StatusCode)
	}
	return nil
}

// postWithFallback tries each path in order, falling through to the next on a
// 404 (used for endpoints renamed between qBittorrent 4.x and 5.x).
func (c *Client) postWithFallback(paths []string, form url.Values) error {
	var lastErr error
	for _, path := range paths {
		resp, err := c.do("POST", path, form)
		if err != nil {
			return err
		}
		status := resp.StatusCode
		resp.Body.Close()
		if status == http.StatusNotFound {
			lastErr = fmt.Errorf("qbittorrent %s: status 404", path)
			continue
		}
		if status < 200 || status >= 300 {
			return fmt.Errorf("qbittorrent %s: status %d", path, status)
		}
		return nil
	}
	return lastErr
}

// getJSON executes a GET and decodes the JSON response into out.
func (c *Client) getJSON(path string, out interface{}) error {
	resp, err := c.do("GET", path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("qbittorrent %s: status %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("qbittorrent decode %s: %w", path, err)
	}
	return nil
}

// Version returns the qBittorrent application version; used (together with
// Login) as the connection test.
func (c *Client) Version() (string, error) {
	resp, err := c.do("GET", "/api/v2/app/version", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("qbittorrent version: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("qbittorrent read version: %w", err)
	}
	return strings.TrimSpace(string(body)), nil
}

// Torrent is a single torrent as returned by /api/v2/torrents/info.
type Torrent struct {
	Name         string  `json:"name"`
	Hash         string  `json:"hash"`
	Size         int64   `json:"size"`
	Progress     float64 `json:"progress"` // 0-1
	DLSpeed      int64   `json:"dlspeed"`  // bytes/s
	UPSpeed      int64   `json:"upspeed"`  // bytes/s
	ETA          int64   `json:"eta"`      // seconds; 8640000 = unknown
	State        string  `json:"state"`
	Category     string  `json:"category"`
	NumSeeds     int     `json:"num_seeds"`
	NumLeechs    int     `json:"num_leechs"`
	CompletionOn int64   `json:"completion_on"` // unix seconds
}

// GetTorrents returns all torrents known to qBittorrent.
func (c *Client) GetTorrents() ([]Torrent, error) {
	var torrents []Torrent
	if err := c.getJSON("/api/v2/torrents/info", &torrents); err != nil {
		return nil, err
	}
	return torrents, nil
}

// PauseTorrents pauses the given torrents. hashes is "all" or "h1|h2|...".
// qBittorrent 5.x renamed pause→stop; try stop first, fall back to pause on 404.
func (c *Client) PauseTorrents(hashes string) error {
	return c.postWithFallback(
		[]string{"/api/v2/torrents/stop", "/api/v2/torrents/pause"},
		url.Values{"hashes": {hashes}},
	)
}

// ResumeTorrents resumes the given torrents. hashes is "all" or "h1|h2|...".
// qBittorrent 5.x renamed resume→start; try start first, fall back to resume on 404.
func (c *Client) ResumeTorrents(hashes string) error {
	return c.postWithFallback(
		[]string{"/api/v2/torrents/start", "/api/v2/torrents/resume"},
		url.Values{"hashes": {hashes}},
	)
}

// Delete removes the given torrents, optionally deleting downloaded files.
func (c *Client) Delete(hashes string, deleteFiles bool) error {
	df := "false"
	if deleteFiles {
		df = "true"
	}
	return c.post("/api/v2/torrents/delete", url.Values{"hashes": {hashes}, "deleteFiles": {df}})
}

// TransferInfo is the global transfer state from /api/v2/transfer/info.
type TransferInfo struct {
	DLInfoSpeed int64 `json:"dl_info_speed"` // bytes/s
	UPInfoSpeed int64 `json:"up_info_speed"` // bytes/s
	DLRateLimit int64 `json:"dl_rate_limit"` // bytes/s, 0 = unlimited
}

// GetTransferInfo returns the global transfer state.
func (c *Client) GetTransferInfo() (*TransferInfo, error) {
	var info TransferInfo
	if err := c.getJSON("/api/v2/transfer/info", &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// SetDownloadLimit sets the global download speed limit in bytes per second
// (0 = unlimited).
func (c *Client) SetDownloadLimit(bytesPerSec int64) error {
	return c.post("/api/v2/transfer/setDownloadLimit", url.Values{"limit": {strconv.FormatInt(bytesPerSec, 10)}})
}
