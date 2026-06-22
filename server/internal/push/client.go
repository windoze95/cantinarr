// Package push integrates Cantinarr with the self-hosted push gateway. The
// Client speaks the gateway's /v1 HTTP API (Bearer per-app key) to register
// device tokens and fan notifications out to APNs. Cantinarr user IDs are
// int64; the gateway treats user ids as opaque strings, so they are converted
// to decimal strings on the wire.
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Client talks to the push gateway's /v1 API using a per-app bearer key.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient builds a gateway client. baseURL is the gateway origin (no trailing
// slash) and apiKey is the per-app bearer key.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// EnrollResponse is the gateway's reply to POST /v1/enroll.
type EnrollResponse struct {
	TenantID string `json:"tenant_id"`
	APIKey   string `json:"api_key"`
}

// Enroll self-registers a new tenant with the gateway and returns its tenant id
// and per-app API key. It uses no bearer auth (enrollment is unauthenticated);
// when the gateway runs in gated mode, pass the shared enrollToken (sent as the
// X-Enroll-Token header). Used for zero-config auto-enrollment on first start.
func Enroll(baseURL, name, enrollToken string) (EnrollResponse, error) {
	var out EnrollResponse
	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return out, fmt.Errorf("marshal enroll body: %w", err)
	}
	url := strings.TrimRight(baseURL, "/") + "/v1/enroll"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	if enrollToken != "" {
		req.Header.Set("X-Enroll-Token", enrollToken)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return out, fmt.Errorf("enroll: gateway returned status %d: %s",
			resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, fmt.Errorf("decode enroll response: %w", err)
	}
	if out.APIKey == "" {
		return out, fmt.Errorf("enroll: gateway returned an empty api_key")
	}
	return out, nil
}

// RegisterDevice upserts a device token with the gateway. The gateway defaults
// the APNs topic from the tenant, so no topic is sent.
func (c *Client) RegisterDevice(ctx context.Context, userID int64, deviceID, platform, token string) error {
	body := map[string]any{
		"user_id":   strconv.FormatInt(userID, 10),
		"device_id": deviceID,
		"platform":  platform,
		"token":     token,
	}
	return c.do(ctx, http.MethodPost, "/v1/devices", body, nil)
}

// DeleteDevice removes a device's token from the gateway by caller device id.
func (c *Client) DeleteDevice(ctx context.Context, deviceID string) error {
	body := map[string]any{"device_id": deviceID}
	return c.do(ctx, http.MethodDelete, "/v1/devices", body, nil)
}

// SendOptions carries optional per-send tuning. The zero value is valid and
// reproduces the default high-priority send.
type SendOptions struct {
	// CollapseID, when set, is passed to the gateway as the APNs collapse id so
	// repeat notifications about the same subject coalesce on-device into a
	// single alert. Empty means no collapsing.
	CollapseID string
}

// Send fans a high-priority notification out to the given users' devices. data
// is delivered as the APNs payload's custom data.
func (c *Client) Send(ctx context.Context, userIDs []int64, title, body string, data map[string]any) error {
	return c.SendWithOptions(ctx, userIDs, title, body, data, SendOptions{})
}

// SendWithOptions is Send with explicit per-send options (e.g. a collapse id).
func (c *Client) SendWithOptions(ctx context.Context, userIDs []int64, title, body string, data map[string]any, opts SendOptions) error {
	ids := make([]string, len(userIDs))
	for i, id := range userIDs {
		ids[i] = strconv.FormatInt(id, 10)
	}
	options := map[string]any{"priority": "high"}
	if opts.CollapseID != "" {
		options["collapse_id"] = opts.CollapseID
	}
	payload := map[string]any{
		"to": map[string]any{"user_ids": ids},
		"notification": map[string]any{
			"title": title,
			"body":  body,
		},
		"data":    data,
		"options": options,
	}
	return c.do(ctx, http.MethodPost, "/v1/notifications", payload, nil)
}

// do executes a request with an optional JSON body, fails on non-2xx status
// (including a snippet of the response body), and decodes JSON into out when
// out is non-nil.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("push gateway %s %s returned status %d: %s",
			method, path, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
