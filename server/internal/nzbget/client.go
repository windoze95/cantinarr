// Package nzbget provides a client for the NZBGet JSON-RPC API.
package nzbget

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to the NZBGet JSON-RPC API:
// POST {base}/jsonrpc with HTTP Basic auth.
type Client struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client
}

// NewClient creates a new NZBGet client.
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

// call performs a JSON-RPC request, checks both the HTTP status and the
// JSON-RPC error envelope, and decodes the result into out when out is non-nil.
func (c *Client) call(method string, params []interface{}, out interface{}) error {
	if params == nil {
		params = []interface{}{}
	}
	reqBody, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
		"id":      1,
	})
	if err != nil {
		return fmt.Errorf("nzbget encode request: %w", err)
	}

	req, err := http.NewRequest("POST", c.baseURL+"/jsonrpc", bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("nzbget request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("nzbget request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return fmt.Errorf("nzbget read response: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("nzbget: invalid credentials")
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return fmt.Errorf("nzbget returned redirect status %d to %q (redirects are not followed; use the service's final URL)", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("nzbget returned status %d", resp.StatusCode)
	}

	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Name    string `json:"name"`
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("nzbget decode response: %w", err)
	}
	if envelope.Error != nil {
		msg := envelope.Error.Message
		if msg == "" {
			msg = envelope.Error.Name
		}
		if msg == "" {
			msg = "unknown error"
		}
		return fmt.Errorf("nzbget %s: %s", method, msg)
	}

	if out != nil && len(envelope.Result) > 0 {
		if err := json.Unmarshal(envelope.Result, out); err != nil {
			return fmt.Errorf("nzbget decode result: %w", err)
		}
	}
	return nil
}

// Version returns the NZBGet version; used as the connection test.
func (c *Client) Version() (string, error) {
	var version string
	if err := c.call("version", nil, &version); err != nil {
		return "", err
	}
	if version == "" {
		return "", fmt.Errorf("nzbget returned no version (wrong URL or credentials?)")
	}
	return version, nil
}

// combineLoHi reassembles NZBGet's 32-bit Lo/Hi pair into a byte count.
func combineLoHi(lo, hi uint32) int64 {
	return int64(uint64(hi)<<32 | uint64(lo))
}

// Group is a single entry in the NZBGet download queue (one NZB).
type Group struct {
	NZBID           int    `json:"NZBID"`
	NZBName         string `json:"NZBName"`
	FileSizeLo      uint32 `json:"FileSizeLo"`
	FileSizeHi      uint32 `json:"FileSizeHi"`
	FileSizeMB      int64  `json:"FileSizeMB"`
	RemainingSizeLo uint32 `json:"RemainingSizeLo"`
	RemainingSizeHi uint32 `json:"RemainingSizeHi"`
	RemainingSizeMB int64  `json:"RemainingSizeMB"`
	Status          string `json:"Status"`
	Category        string `json:"Category"`
}

// SizeBytes returns the total size of the item in bytes, preferring the exact
// Lo/Hi pair over the rounded MB value.
func (g Group) SizeBytes() int64 {
	if b := combineLoHi(g.FileSizeLo, g.FileSizeHi); b > 0 {
		return b
	}
	return g.FileSizeMB * 1024 * 1024
}

// RemainingBytes returns the remaining size of the item in bytes.
func (g Group) RemainingBytes() int64 {
	if b := combineLoHi(g.RemainingSizeLo, g.RemainingSizeHi); b > 0 {
		return b
	}
	return g.RemainingSizeMB * 1024 * 1024
}

// ListGroups returns the current download queue.
func (c *Client) ListGroups() ([]Group, error) {
	var groups []Group
	if err := c.call("listgroups", []interface{}{0}, &groups); err != nil {
		return nil, err
	}
	return groups, nil
}

// Status is NZBGet's global download state.
type Status struct {
	DownloadRate    int64  `json:"DownloadRate"` // bytes/s
	DownloadPaused  bool   `json:"DownloadPaused"`
	RemainingSizeLo uint32 `json:"RemainingSizeLo"`
	RemainingSizeHi uint32 `json:"RemainingSizeHi"`
	RemainingSizeMB int64  `json:"RemainingSizeMB"`
}

// RemainingBytes returns the total remaining queue size in bytes.
func (s Status) RemainingBytes() int64 {
	if b := combineLoHi(s.RemainingSizeLo, s.RemainingSizeHi); b > 0 {
		return b
	}
	return s.RemainingSizeMB * 1024 * 1024
}

// GetStatus returns the global download state.
func (c *Client) GetStatus() (*Status, error) {
	var status Status
	if err := c.call("status", nil, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// HistoryEntry is a single entry in NZBGet's download history. Status is
// "SUCCESS/...", "WARNING/...", "FAILURE/..." or "DELETED/...".
type HistoryEntry struct {
	NZBID       int    `json:"NZBID"`
	Name        string `json:"Name"`
	Status      string `json:"Status"`
	FileSizeLo  uint32 `json:"FileSizeLo"`
	FileSizeHi  uint32 `json:"FileSizeHi"`
	FileSizeMB  int64  `json:"FileSizeMB"`
	HistoryTime int64  `json:"HistoryTime"` // unix seconds
	Category    string `json:"Category"`
}

// SizeBytes returns the total size of the item in bytes.
func (e HistoryEntry) SizeBytes() int64 {
	if b := combineLoHi(e.FileSizeLo, e.FileSizeHi); b > 0 {
		return b
	}
	return e.FileSizeMB * 1024 * 1024
}

// GetHistory returns the download history (excluding hidden entries).
func (c *Client) GetHistory() ([]HistoryEntry, error) {
	var entries []HistoryEntry
	if err := c.call("history", []interface{}{false}, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// PauseDownload pauses the entire download queue.
func (c *Client) PauseDownload() error {
	var ok bool
	if err := c.call("pausedownload", nil, &ok); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("nzbget pausedownload failed")
	}
	return nil
}

// ResumeDownload resumes the entire download queue.
func (c *Client) ResumeDownload() error {
	var ok bool
	if err := c.call("resumedownload", nil, &ok); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("nzbget resumedownload failed")
	}
	return nil
}

// editQueue invokes the editqueue RPC using the modern 3-parameter signature
// (NZBGet v16+: Command, Param, IDs), falling back to the older 4-parameter
// signature (Command, Offset, Param, IDs) when the server rejects it.
func (c *Client) editQueue(command string, ids []int) error {
	var ok bool
	err := c.call("editqueue", []interface{}{command, "", ids}, &ok)
	if err != nil {
		// Pre-v16 NZBGet requires the legacy signature with an Offset param.
		if legacyErr := c.call("editqueue", []interface{}{command, 0, "", ids}, &ok); legacyErr != nil {
			return err
		}
	}
	if !ok {
		return fmt.Errorf("nzbget editqueue %s failed", command)
	}
	return nil
}

// PauseGroups pauses the given queue items by NZBID.
func (c *Client) PauseGroups(ids []int) error {
	return c.editQueue("GroupPause", ids)
}

// ResumeGroups resumes the given queue items by NZBID.
func (c *Client) ResumeGroups(ids []int) error {
	return c.editQueue("GroupResume", ids)
}

// DeleteGroups removes the given queue items by NZBID.
func (c *Client) DeleteGroups(ids []int) error {
	return c.editQueue("GroupDelete", ids)
}
