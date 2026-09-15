// Package rutorrent provides a client for rTorrent reached through
// ruTorrent's httprpc plugin. Reads go as raw XML-RPC, which the plugin
// forwards to rTorrent as an untrusted connection (rTorrent answers reads
// on those). Mutations use the plugin's own form protocol, the one
// ruTorrent's UI speaks, because it runs them trusted: rTorrent refuses
// d.start, d.stop, and d.open from untrusted connections, and only
// ruTorrent's erasedata helper can delete a download's files.
package rutorrent

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/httpx"
)

// rpcPath is the httprpc plugin endpoint, relative to the ruTorrent address
// the admin enters. It serves both the raw XML-RPC reads and the form
// commands.
const rpcPath = "/plugins/httprpc/action.php"

// Client talks to rTorrent through a ruTorrent installation. Credentials are
// HTTP Basic, for web servers that protect ruTorrent; both may be blank.
type Client struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client
}

// NewClient creates a ruTorrent client. baseURL is the ruTorrent address
// (scheme://host:port, or with the path ruTorrent is served under).
func NewClient(baseURL, username, password string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
		httpClient: &http.Client{
			Transport:     httpx.Internal(),
			Timeout:       30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// post sends one request to a ruTorrent endpoint and returns the response
// once its status is acceptable. The body is deliberately never echoed in
// errors: it can carry the request (and the credentials) back.
func (c *Client) post(path, contentType string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest("POST", c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("rutorrent request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rutorrent request: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		resp.Body.Close()
		return nil, fmt.Errorf("rutorrent: the web server refused the credentials (status 401)")
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		location := resp.Header.Get("Location")
		resp.Body.Close()
		return nil, fmt.Errorf("rutorrent returned redirect status %d to %q (redirects are not followed; use ruTorrent's final URL)", resp.StatusCode, location)
	case resp.StatusCode == http.StatusNotFound:
		resp.Body.Close()
		return nil, fmt.Errorf("rutorrent returned status 404 for %s (enter the ruTorrent address, such as http://rutorrent:8080 or the path ruTorrent is served under; Cantinarr appends the plugin paths)", path)
	}
	return resp, nil
}

// call performs one XML-RPC method through the httprpc plugin.
func (c *Client) call(method string, params ...interface{}) (Value, error) {
	body, err := encodeCall(method, params...)
	if err != nil {
		return Value{}, err
	}
	resp, err := c.post(rpcPath, "text/xml", body)
	if err != nil {
		return Value{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return Value{}, fmt.Errorf("rutorrent read response: %w", err)
	}
	trimmed := strings.TrimSpace(string(raw))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// ruTorrent answers its own refusals as an XML-RPC fault (403 when
		// its proxy policy blocks the method) and rTorrent outages as a short
		// text (500 "Could not reach rTorrent over XMLRPC. Is rTorrent
		// running?"); both are worth passing on verbatim.
		if strings.HasPrefix(trimmed, "<?xml") || strings.HasPrefix(trimmed, "<methodResponse") {
			if _, err := decodeResponse(strings.NewReader(trimmed)); err != nil {
				return Value{}, fmt.Errorf("rutorrent %s: %w", method, err)
			}
		}
		if len(trimmed) > 0 && len(trimmed) < 200 && !strings.HasPrefix(trimmed, "<") {
			return Value{}, fmt.Errorf("rutorrent returned status %d: %s", resp.StatusCode, trimmed)
		}
		return Value{}, fmt.Errorf("rutorrent returned status %d", resp.StatusCode)
	}
	if !strings.HasPrefix(trimmed, "<?xml") && !strings.HasPrefix(trimmed, "<methodResponse") {
		return Value{}, fmt.Errorf("rutorrent returned something other than XML-RPC (is this the ruTorrent address? Cantinarr appends %s)", rpcPath)
	}
	value, err := decodeResponse(strings.NewReader(trimmed))
	if err != nil {
		return Value{}, fmt.Errorf("rutorrent %s: %w", method, err)
	}
	return value, nil
}

// Version returns rTorrent's version; used as the connection test, which
// therefore proves ruTorrent, its httprpc plugin, and rTorrent behind it.
func (c *Client) Version() (string, error) {
	v, err := c.call("system.client_version")
	if err != nil {
		return "", err
	}
	version := strings.TrimSpace(v.String())
	if version == "" {
		return "", fmt.Errorf("rutorrent returned no rTorrent version (wrong URL?)")
	}
	return version, nil
}

// Torrent is one download as rTorrent reports it.
type Torrent struct {
	Hash           string
	Name           string
	Label          string // ruTorrent label (d.custom1)
	SizeBytes      int64
	LeftBytes      int64
	CompletedBytes int64
	DownRate       int64 // bytes/s
	IsOpen         bool
	IsActive       bool
	State          int // 0 = stopped, 1 = started (pausing does not change it)
	Complete       bool
	Hashing        int // 0 = none; anything else = a hash check is running
	Message        string
	FinishedAt     int64 // unix seconds; 0 when unfinished
	AddedAt        int64 // unix seconds (ruTorrent's addtime custom); 0 if unset
}

// multicallParams builds d.multicall2's parameters: the (empty) target,
// the view, then each column command as its own string parameter; rTorrent
// refuses them packed into one array.
func multicallParams(view string, commands []string) []interface{} {
	params := make([]interface{}, 0, len(commands)+2)
	params = append(params, "", view)
	for _, cmd := range commands {
		params = append(params, cmd)
	}
	return params
}

// torrentFields are the d.multicall2 columns, in Torrent field order.
var torrentFields = []string{
	"d.hash=", "d.name=", "d.custom1=", "d.size_bytes=", "d.left_bytes=",
	"d.completed_bytes=", "d.down.rate=", "d.is_open=", "d.is_active=",
	"d.state=", "d.complete=", "d.hashing=", "d.message=",
	"d.timestamp.finished=", "d.custom=addtime",
}

// GetTorrents returns every download in rTorrent's main view, in view order.
func (c *Client) GetTorrents() ([]Torrent, error) {
	v, err := c.call("d.multicall2", multicallParams("main", torrentFields)...)
	if err != nil {
		return nil, err
	}
	if v.Kind != "array" {
		return nil, fmt.Errorf("rutorrent d.multicall2: unexpected %s result", v.Kind)
	}
	torrents := make([]Torrent, 0, len(v.Array))
	for _, row := range v.Array {
		if row.Kind != "array" || len(row.Array) < len(torrentFields) {
			return nil, fmt.Errorf("rutorrent d.multicall2: row with %d columns, want %d", len(row.Array), len(torrentFields))
		}
		col := row.Array
		added, _ := strconv.ParseInt(strings.TrimSpace(col[14].String()), 10, 64)
		torrents = append(torrents, Torrent{
			Hash:           col[0].String(),
			Name:           col[1].String(),
			Label:          col[2].String(),
			SizeBytes:      col[3].Int64(),
			LeftBytes:      col[4].Int64(),
			CompletedBytes: col[5].Int64(),
			DownRate:       col[6].Int64(),
			IsOpen:         col[7].Int64() != 0,
			IsActive:       col[8].Int64() != 0,
			State:          int(col[9].Int64()),
			Complete:       col[10].Int64() != 0,
			Hashing:        int(col[11].Int64()),
			Message:        col[12].String(),
			FinishedAt:     col[13].Int64(),
			AddedAt:        added,
		})
	}
	return torrents, nil
}

// command runs one of ruTorrent's httprpc form commands on the given
// downloads: the plugin builds the rTorrent calls itself and sends them
// trusted. It answers the rTorrent results as JSON, or false when rTorrent
// refused or could not be reached.
func (c *Client) command(mode string, hashes []string, extra url.Values) error {
	if len(hashes) == 0 {
		return fmt.Errorf("rutorrent %s: no torrent specified", mode)
	}
	form := url.Values{"mode": {mode}}
	for _, h := range hashes {
		if h == "" {
			return fmt.Errorf("rutorrent %s: no torrent specified", mode)
		}
		form.Add("hash", h)
	}
	for k, vs := range extra {
		for _, v := range vs {
			form.Add(k, v)
		}
	}
	resp, err := c.post(rpcPath, "application/x-www-form-urlencoded", []byte(form.Encode()))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("rutorrent %s: read response: %w", mode, err)
	}
	trimmed := strings.TrimSpace(string(raw))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// ruTorrent answers rTorrent outages with a short text (500 "Could
		// not reach rTorrent over XMLRPC. Is rTorrent running?") or the
		// fault string; both are worth passing on verbatim.
		if len(trimmed) > 0 && len(trimmed) < 200 && !strings.HasPrefix(trimmed, "<") {
			return fmt.Errorf("rutorrent %s returned status %d: %s", mode, resp.StatusCode, trimmed)
		}
		return fmt.Errorf("rutorrent %s returned status %d", mode, resp.StatusCode)
	}
	if trimmed == "false" {
		if mode == "removewithdata" {
			return fmt.Errorf("rutorrent kept the torrent: its erasedata plugin could not determine the torrent's files (open ruTorrent once so it learns rTorrent's commands, then try again)")
		}
		return fmt.Errorf("rutorrent %s: rTorrent refused the command", mode)
	}
	if !strings.HasPrefix(trimmed, "[") {
		return fmt.Errorf("rutorrent %s returned something other than its JSON result (is this the ruTorrent address? Cantinarr appends %s)", mode, rpcPath)
	}
	return nil
}

// Pause halts downloads the way ruTorrent's own Pause does (d.stop: the
// download stays open and stops transferring).
func (c *Client) Pause(hashes ...string) error {
	return c.command("pause", hashes, nil)
}

// Resume makes downloads active again whatever halted them: ruTorrent's
// start command runs d.open, d.start, and d.resume, each a no-op when
// already in that state, so a paused, stopped, or closed download all come
// back.
func (c *Client) Resume(hashes ...string) error {
	return c.command("start", hashes, nil)
}

// Erase removes a download from rTorrent, leaving its files on disk.
func (c *Client) Erase(hash string) error {
	return c.command("remove", []string{hash}, nil)
}

// EraseWithData removes a download together with its files through
// ruTorrent's erasedata helper. ruTorrent answers false, and keeps the
// torrent, when it cannot work out the files: that happens on a ruTorrent
// that has never been opened, because it learns rTorrent's command names on
// its first page load. Without the erasedata plugin ruTorrent falls back to
// a plain erase, so the plugin (which ships with ruTorrent) is required for
// the files to go.
func (c *Client) EraseWithData(hash string) error {
	return c.command("removewithdata", []string{hash}, url.Values{"v": {"1"}})
}

// GlobalDownRate returns rTorrent's current total download rate in bytes/s.
func (c *Client) GlobalDownRate() (int64, error) {
	v, err := c.call("throttle.global_down.rate")
	if err != nil {
		return 0, err
	}
	return v.Int64(), nil
}
