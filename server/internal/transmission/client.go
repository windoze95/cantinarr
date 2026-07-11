// Package transmission provides a client for the Transmission RPC API.
package transmission

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// sessionHeader carries the CSRF token Transmission requires on every request.
const sessionHeader = "X-Transmission-Session-Id"

// Transmission torrent status codes (the "status" field of torrent-get).
const (
	StatusStopped       = 0
	StatusCheckWaiting  = 1
	StatusChecking      = 2
	StatusDownloadQueue = 3
	StatusDownloading   = 4
	StatusSeedQueue     = 5
	StatusSeeding       = 6
)

// StatusString maps a Transmission numeric torrent status to a readable string.
func StatusString(status int) string {
	switch status {
	case StatusStopped:
		return "stopped"
	case StatusCheckWaiting:
		return "checkWaiting"
	case StatusChecking:
		return "checking"
	case StatusDownloadQueue:
		return "downloadWaiting"
	case StatusDownloading:
		return "downloading"
	case StatusSeedQueue:
		return "seedWaiting"
	case StatusSeeding:
		return "seeding"
	}
	return fmt.Sprintf("unknown (%d)", status)
}

// Client talks to the Transmission RPC API:
// POST {base}/transmission/rpc with optional HTTP Basic auth and the
// X-Transmission-Session-Id handshake (on a 409 the new session id is read
// from the response and the request retried once).
type Client struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client

	mu        sync.Mutex
	sessionID string
}

// NewClient creates a new Transmission client. Username/password may be blank
// when the RPC endpoint has no authentication enabled.
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

// call performs an RPC request, handling the 409 session-id handshake, and
// decodes the response arguments into out when out is non-nil.
func (c *Client) call(method string, args interface{}, out interface{}) error {
	payload := map[string]interface{}{"method": method}
	if args != nil {
		payload["arguments"] = args
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("transmission encode request: %w", err)
	}

	resp, err := c.doOnce(body)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusConflict {
		sid := resp.Header.Get(sessionHeader)
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if sid == "" {
			return fmt.Errorf("transmission: 409 response without %s header", sessionHeader)
		}
		c.mu.Lock()
		c.sessionID = sid
		c.mu.Unlock()
		resp, err = c.doOnce(body)
		if err != nil {
			return err
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("transmission: invalid credentials")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("transmission returned status %d", resp.StatusCode)
	}

	var envelope struct {
		Result    string          `json:"result"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&envelope); err != nil {
		return fmt.Errorf("transmission decode response: %w", err)
	}
	if envelope.Result != "success" {
		return fmt.Errorf("transmission %s: %s", method, envelope.Result)
	}
	if out != nil && len(envelope.Arguments) > 0 {
		if err := json.Unmarshal(envelope.Arguments, out); err != nil {
			return fmt.Errorf("transmission decode arguments: %w", err)
		}
	}
	return nil
}

// doOnce executes a single RPC POST with the current session id.
func (c *Client) doOnce(body []byte) (*http.Response, error) {
	req, err := http.NewRequest("POST", c.baseURL+"/transmission/rpc", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("transmission request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	c.mu.Lock()
	sid := c.sessionID
	c.mu.Unlock()
	if sid != "" {
		req.Header.Set(sessionHeader, sid)
	}
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("transmission request: %w", err)
	}
	return resp, nil
}

// Session is the subset of session-get used here.
type Session struct {
	Version    string `json:"version"`
	RPCVersion int    `json:"rpc-version"`
}

// SessionGet returns the Transmission session info; used (together with the
// 409 handshake inside call) as the connection test.
func (c *Client) SessionGet() (*Session, error) {
	var session Session
	if err := c.call("session-get", nil, &session); err != nil {
		return nil, err
	}
	if session.Version == "" {
		return nil, fmt.Errorf("transmission returned no version (wrong URL?)")
	}
	return &session, nil
}

// Torrent is a single torrent as returned by torrent-get.
type Torrent struct {
	ID            int64    `json:"id"`
	HashString    string   `json:"hashString"`
	Name          string   `json:"name"`
	TotalSize     int64    `json:"totalSize"`
	LeftUntilDone int64    `json:"leftUntilDone"`
	PercentDone   float64  `json:"percentDone"`  // 0-1
	RateDownload  int64    `json:"rateDownload"` // bytes/s
	RateUpload    int64    `json:"rateUpload"`   // bytes/s
	ETA           int64    `json:"eta"`          // seconds; negative = unknown/unavailable
	Status        int      `json:"status"`
	Error         int      `json:"error"` // 0 = none
	ErrorString   string   `json:"errorString"`
	DownloadDir   string   `json:"downloadDir"`
	Labels        []string `json:"labels"`
	DoneDate      int64    `json:"doneDate"` // unix seconds, 0 if not finished
}

var torrentFields = []string{
	"id", "hashString", "name", "totalSize", "leftUntilDone", "percentDone",
	"rateDownload", "rateUpload", "eta", "status", "error", "errorString",
	"downloadDir", "labels", "doneDate",
}

// GetTorrents returns all torrents known to Transmission.
func (c *Client) GetTorrents() ([]Torrent, error) {
	var out struct {
		Torrents []Torrent `json:"torrents"`
	}
	if err := c.call("torrent-get", map[string]interface{}{"fields": torrentFields}, &out); err != nil {
		return nil, err
	}
	return out.Torrents, nil
}

// idsArgs builds the arguments map for an ids-based action. A nil/empty hash
// list omits "ids", which Transmission treats as "all torrents".
func idsArgs(hashes []string) map[string]interface{} {
	args := map[string]interface{}{}
	if len(hashes) > 0 {
		args["ids"] = hashes
	}
	return args
}

// StopTorrents stops the given torrents by hash; nil/empty stops all.
func (c *Client) StopTorrents(hashes []string) error {
	return c.call("torrent-stop", idsArgs(hashes), nil)
}

// StartTorrents starts the given torrents by hash; nil/empty starts all.
func (c *Client) StartTorrents(hashes []string) error {
	return c.call("torrent-start", idsArgs(hashes), nil)
}

// RemoveTorrents removes the given torrents by hash, optionally deleting
// downloaded data. An empty hash list is rejected to avoid removing everything.
func (c *Client) RemoveTorrents(hashes []string, deleteData bool) error {
	if len(hashes) == 0 {
		return fmt.Errorf("transmission torrent-remove: no torrents specified")
	}
	args := idsArgs(hashes)
	args["delete-local-data"] = deleteData
	return c.call("torrent-remove", args, nil)
}

// SessionStats is the subset of session-stats used here.
type SessionStats struct {
	DownloadSpeed int64 `json:"downloadSpeed"` // bytes/s
	UploadSpeed   int64 `json:"uploadSpeed"`   // bytes/s
}

// GetSessionStats returns the global transfer statistics.
func (c *Client) GetSessionStats() (*SessionStats, error) {
	var stats SessionStats
	if err := c.call("session-stats", nil, &stats); err != nil {
		return nil, err
	}
	return &stats, nil
}
