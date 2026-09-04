// Package deluge provides a client for the JSON-RPC API of Deluge's web UI.
package deluge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/httpx"
)

// Deluge's JSON-RPC error codes (deluge/ui/web/json_api.py). "Not
// authenticated" is an expired or missing session; "Unknown method" is what a
// core.* or daemon.* call gets while the web UI is not connected to a daemon
// (it only learns the daemon's methods on connect), and also what Deluge 1.3
// answers for a method it predates.
const (
	errCodeNotAuthenticated = 1
	errCodeUnknownMethod    = 2
)

// sessionCookie is the cookie Deluge's web UI issues on auth.login.
const sessionCookie = "_session_id"

// Torrent states as Deluge reports them (deluge/common.py TORRENT_STATE):
// Allocating, Checking, Downloading, Seeding, Paused, Error, Queued, Moving.
const (
	StatePaused = "Paused"
	StateError  = "Error"
)

// torrentKeys are the status fields requested from the daemon. Keys the
// daemon does not know (label without the Label plugin, completed_time on
// Deluge 1.3) are silently absent from the answer, never an error.
var torrentKeys = []string{
	"name", "state", "progress", "total_size", "total_done",
	"download_payload_rate", "eta", "is_finished", "message",
	"time_added", "completed_time", "label",
}

// Client talks to Deluge's web UI: POST {base}/json carrying
// {"method","params","id"}. The web UI has a single password and answers
// auth.login with a _session_id cookie; every later call carries it. The web
// UI is itself a client of the daemon (deluged) and may start out not
// connected to it, so the client also drives web.connected / web.get_hosts /
// web.connect before the first daemon call. One mutex serialises calls so
// the session and connection state are never raced.
type Client struct {
	baseURL    string
	password   string
	httpClient *http.Client

	mu        sync.Mutex
	sessionID string // value of the _session_id cookie; "" = not logged in
	connected bool   // the web UI reported a daemon connection this session
	nextID    int64
}

// NewClient creates a Deluge client. baseURL is the web UI address
// (scheme://host:port); password is the web UI password.
func NewClient(baseURL, password string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		password: password,
		httpClient: &http.Client{
			Transport:     httpx.Internal(),
			Timeout:       30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// rpcError is Deluge's error envelope.
type rpcError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

func (e *rpcError) Error() string { return e.Message }

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// isRPCCode reports whether err is a Deluge RPC error with the given code.
func isRPCCode(err error, code int) bool {
	var rpcErr *rpcError
	return errors.As(err, &rpcErr) && rpcErr.Code == code
}

// call runs one RPC method: it logs in and connects the web UI to its daemon
// when needed, and starts over exactly once when the answer says the session
// is gone (code 1) or the web UI lost its daemon (code 2).
func (c *Client) call(method string, params []interface{}, out interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureReady(method); err != nil {
		return err
	}
	err := c.invoke(method, params, out)
	if isRPCCode(err, errCodeNotAuthenticated) || isRPCCode(err, errCodeUnknownMethod) {
		c.sessionID = ""
		c.connected = false
		if err := c.ensureReady(method); err != nil {
			return err
		}
		err = c.invoke(method, params, out)
	}
	return err
}

// needsDaemon reports whether a method is served by the daemon (through the
// web UI) rather than by the web UI itself.
func needsDaemon(method string) bool {
	return strings.HasPrefix(method, "core.") || strings.HasPrefix(method, "daemon.")
}

// ensureReady logs in when there is no session and, for daemon methods,
// makes sure the web UI is connected to a daemon. Caller holds c.mu.
func (c *Client) ensureReady(method string) error {
	if c.sessionID == "" {
		if err := c.login(); err != nil {
			return err
		}
	}
	if needsDaemon(method) && !c.connected {
		if err := c.connect(); err != nil {
			return err
		}
	}
	return nil
}

// login runs auth.login and records the session cookie. Caller holds c.mu.
func (c *Client) login() error {
	c.sessionID = ""
	c.connected = false
	var result json.RawMessage
	sessionID, err := c.post("auth.login", []interface{}{c.password}, &result)
	if err != nil {
		return err
	}
	// Deluge answers false for a wrong password (2.x otherwise answers the
	// session id, 1.3 answers true). The password is never part of the error.
	trimmed := strings.TrimSpace(string(result))
	if trimmed == "" || trimmed == "false" || trimmed == "null" {
		return fmt.Errorf("deluge: invalid password")
	}
	if sessionID == "" {
		return fmt.Errorf("deluge: login succeeded but no %s cookie was issued", sessionCookie)
	}
	c.sessionID = sessionID
	return nil
}

// connect makes the web UI connect to a daemon when it is not already.
// Caller holds c.mu.
func (c *Client) connect() error {
	var connected bool
	if err := c.invoke("web.connected", nil, &connected); err != nil {
		return err
	}
	if connected {
		c.connected = true
		return nil
	}
	var hosts [][]json.RawMessage
	if err := c.invoke("web.get_hosts", nil, &hosts); err != nil {
		return err
	}
	host, ok := pickHost(hosts)
	if !ok {
		if len(hosts) == 0 {
			return fmt.Errorf("deluge web UI is not connected to a daemon and has none in its Connection Manager (add the daemon there, or set a default daemon for deluge-web)")
		}
		return fmt.Errorf("deluge web UI is not connected to a daemon and lists %d remote daemons in its Connection Manager; connect it to the right one there (or set a default daemon for deluge-web) and test again", len(hosts))
	}
	if err := c.invoke("web.connect", []interface{}{host.id}, nil); err != nil {
		return fmt.Errorf("deluge web UI could not connect to its daemon at %s:%d: %w", host.hostname, host.port, err)
	}
	c.connected = true
	return nil
}

type daemonHost struct {
	id       string
	hostname string
	port     int
}

// pickHost chooses the daemon to connect the web UI to: the only entry, or
// the local one when several are listed. Several remote daemons and no local
// one is a choice the admin has to make in the Connection Manager (the web
// UI keeps whatever it is connected to, so guessing would redirect the
// admin's own web UI). Entries are [host_id, hostname, port, …] on every
// Deluge version (the fourth column is the username on 2.x and a status on
// 1.3, so it is not read).
func pickHost(hosts [][]json.RawMessage) (daemonHost, bool) {
	var parsed []daemonHost
	for _, entry := range hosts {
		if len(entry) < 3 {
			continue
		}
		var h daemonHost
		var port float64
		if json.Unmarshal(entry[0], &h.id) != nil || h.id == "" {
			continue
		}
		_ = json.Unmarshal(entry[1], &h.hostname)
		_ = json.Unmarshal(entry[2], &port)
		h.port = int(port)
		parsed = append(parsed, h)
	}
	for _, h := range parsed {
		switch h.hostname {
		case "127.0.0.1", "localhost", "::1":
			return h, true
		}
	}
	if len(parsed) == 1 {
		return parsed[0], true
	}
	return daemonHost{}, false
}

// invoke posts one method with the current session and decodes its result.
// Caller holds c.mu.
func (c *Client) invoke(method string, params []interface{}, out interface{}) error {
	_, err := c.post(method, params, out)
	return err
}

// post performs a single JSON-RPC request. It returns the session id from a
// _session_id cookie in the response, if one was set. Caller holds c.mu.
func (c *Client) post(method string, params []interface{}, out interface{}) (string, error) {
	if params == nil {
		params = []interface{}{}
	}
	c.nextID++
	body, err := json.Marshal(map[string]interface{}{
		"method": method,
		"params": params,
		"id":     c.nextID,
	})
	if err != nil {
		return "", fmt.Errorf("deluge encode request: %w", err)
	}
	req, err := http.NewRequest("POST", c.baseURL+"/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("deluge request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.sessionID != "" {
		// Sent by hand rather than through a cookie jar: Deluge scopes the
		// cookie to the web UI's base path, and /json is the only path used.
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: c.sessionID})
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("deluge request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return "", fmt.Errorf("deluge returned redirect status %d to %q (redirects are not followed; use the web UI's final URL)", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp.StatusCode == http.StatusNotFound {
		// /json is appended to the base URL, so a 404 almost always means the
		// admin pasted something other than the web UI root.
		return "", fmt.Errorf("deluge returned status 404 (not the JSON endpoint — enter just scheme://host:port of the web UI; Cantinarr appends /json)")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The body is deliberately not echoed: it can carry the request back.
		return "", fmt.Errorf("deluge returned status %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return "", fmt.Errorf("deluge read response: %w", err)
	}
	var envelope rpcResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		if strings.HasPrefix(strings.TrimSpace(string(raw)), "<") {
			return "", fmt.Errorf("deluge returned a web page instead of JSON (is this the Deluge web UI URL? enter just scheme://host:port; Cantinarr appends /json)")
		}
		return "", fmt.Errorf("deluge decode response: %w", err)
	}
	sessionID := ""
	for _, cookie := range resp.Cookies() {
		if cookie.Name == sessionCookie {
			sessionID = cookie.Value
		}
	}
	if envelope.Error != nil {
		return sessionID, fmt.Errorf("deluge %s: %w", method, envelope.Error)
	}
	if out != nil && len(envelope.Result) > 0 && string(envelope.Result) != "null" {
		if err := json.Unmarshal(envelope.Result, out); err != nil {
			return sessionID, fmt.Errorf("deluge decode %s result: %w", method, err)
		}
	}
	return sessionID, nil
}

// Version returns the daemon's version; used as the connection test, which
// therefore proves the password, the web UI's daemon connection, and the
// daemon itself. Deluge 1.3 predates daemon.get_version and answers
// "Unknown method", so daemon.info is tried next.
func (c *Client) Version() (string, error) {
	var version string
	err := c.call("daemon.get_version", nil, &version)
	if isRPCCode(err, errCodeUnknownMethod) {
		err = c.call("daemon.info", nil, &version)
	}
	if err != nil {
		return "", err
	}
	if version == "" {
		return "", fmt.Errorf("deluge returned no version (wrong URL?)")
	}
	return version, nil
}

// Torrent is one torrent as the daemon reports it.
type Torrent struct {
	Hash          string
	Name          string
	State         string  // see the State* constants
	Progress      float64 // 0-100
	TotalSize     int64
	TotalDone     int64
	DownloadRate  int64 // bytes/s
	ETA           int64 // seconds; 0 = unknown or not applicable
	IsFinished    bool
	Message       string  // tracker status, or the error text in the Error state
	TimeAdded     float64 // unix seconds
	CompletedTime int64   // unix seconds; 0 when unfinished or on Deluge 1.3
	Label         string  // Label plugin; "" without it
}

// torrentStatus is the wire shape. Numbers are decoded as floats because
// Deluge is not consistent about int versus float across versions.
type torrentStatus struct {
	Name                string  `json:"name"`
	State               string  `json:"state"`
	Progress            float64 `json:"progress"`
	TotalSize           float64 `json:"total_size"`
	TotalDone           float64 `json:"total_done"`
	DownloadPayloadRate float64 `json:"download_payload_rate"`
	ETA                 float64 `json:"eta"`
	IsFinished          bool    `json:"is_finished"`
	Message             string  `json:"message"`
	TimeAdded           float64 `json:"time_added"`
	CompletedTime       float64 `json:"completed_time"`
	Label               string  `json:"label"`
}

// GetTorrents returns every torrent the daemon knows in the order they were
// added (then by hash): the daemon answers with a map, and a stable order
// keeps the queue steady and lets the websocket hub tell a real change from
// a reshuffle.
func (c *Client) GetTorrents() ([]Torrent, error) {
	var raw map[string]torrentStatus
	if err := c.call("core.get_torrents_status", []interface{}{map[string]interface{}{}, torrentKeys}, &raw); err != nil {
		return nil, err
	}
	torrents := make([]Torrent, 0, len(raw))
	for hash, s := range raw {
		torrents = append(torrents, Torrent{
			Hash:          hash,
			Name:          s.Name,
			State:         s.State,
			Progress:      s.Progress,
			TotalSize:     int64(s.TotalSize),
			TotalDone:     int64(s.TotalDone),
			DownloadRate:  int64(s.DownloadPayloadRate),
			ETA:           int64(s.ETA),
			IsFinished:    s.IsFinished,
			Message:       s.Message,
			TimeAdded:     s.TimeAdded,
			CompletedTime: int64(s.CompletedTime),
			Label:         s.Label,
		})
	}
	sort.Slice(torrents, func(i, j int) bool {
		if torrents[i].TimeAdded != torrents[j].TimeAdded {
			return torrents[i].TimeAdded < torrents[j].TimeAdded
		}
		return torrents[i].Hash < torrents[j].Hash
	})
	return torrents, nil
}

// PauseTorrents pauses the given torrents. An empty list is a no-op: Deluge
// treats an empty list as "every torrent", which no caller means.
func (c *Client) PauseTorrents(hashes []string) error {
	if len(hashes) == 0 {
		return nil
	}
	// A list works on every version: Deluge 1.3's pause_torrent takes a
	// list, and 2.x's falls through to pause_torrents for a non-string.
	return c.call("core.pause_torrent", []interface{}{hashes}, nil)
}

// ResumeTorrents resumes the given torrents; an empty list is a no-op.
func (c *Client) ResumeTorrents(hashes []string) error {
	if len(hashes) == 0 {
		return nil
	}
	return c.call("core.resume_torrent", []interface{}{hashes}, nil)
}

// RemoveTorrent removes one torrent, optionally deleting its data.
func (c *Client) RemoveTorrent(hash string, deleteData bool) error {
	if hash == "" {
		return fmt.Errorf("deluge core.remove_torrent: no torrent specified")
	}
	var removed bool
	if err := c.call("core.remove_torrent", []interface{}{hash, deleteData}, &removed); err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("deluge core.remove_torrent: the daemon did not remove the torrent")
	}
	return nil
}

// SessionStatus is the subset of the daemon's session status used here.
type SessionStatus struct {
	DownloadRate int64 // payload bytes/s
	UploadRate   int64 // payload bytes/s
}

// GetSessionStatus returns the global transfer rates.
func (c *Client) GetSessionStatus() (*SessionStatus, error) {
	var raw map[string]float64
	keys := []string{"payload_download_rate", "payload_upload_rate"}
	if err := c.call("core.get_session_status", []interface{}{keys}, &raw); err != nil {
		return nil, err
	}
	return &SessionStatus{
		DownloadRate: int64(raw["payload_download_rate"]),
		UploadRate:   int64(raw["payload_upload_rate"]),
	}, nil
}
