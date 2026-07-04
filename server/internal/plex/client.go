// Package plex integrates with plex.tv: linking the admin's Plex account via
// the PIN flow and sending library-share invites, so Cantinarr can turn a
// user's "here's my Plex email" into an actual server invite without the
// admin leaving the app.
package plex

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrAlreadyShared reports that the invited account already has access to the
// server (plex.tv answers 422 for duplicate shares). Callers treat it as a
// soft success: the user is invited either way.
var ErrAlreadyShared = errors.New("account already has access to this server")

// Client is a minimal plex.tv API client. The admin token is passed per call
// (it lives encrypted in the settings store, owned by the Service); the
// client itself is stateless. baseURL is a field so tests can point it at a
// fake plex.tv.
type Client struct {
	http    *http.Client
	baseURL string
	product string
}

func NewClient() *Client {
	return &Client{
		http:    &http.Client{Timeout: 15 * time.Second},
		baseURL: "https://plex.tv",
		product: "Cantinarr",
	}
}

// Pin is a plex.tv link PIN. AuthToken stays empty until the admin approves
// the link in their browser.
type Pin struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"`
	AuthToken string `json:"authToken"`
}

// Account identifies the linked plex.tv account (display only).
type Account struct {
	Username string `json:"username"`
	Email    string `json:"email"`
}

// Server is an owned Plex Media Server visible to the linked account.
// ClientIdentifier is what the sharing API calls the machine identifier.
type Server struct {
	Name             string `json:"name"`
	ClientIdentifier string `json:"clientIdentifier"`
	Provides         string `json:"provides"`
	Owned            bool   `json:"owned"`
}

// Library is one library section on a server. ID is the plex.tv-global
// section id the sharing API expects (NOT the server-local key).
type Library struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

// CreatePin starts the PIN link flow.
func (c *Client) CreatePin(ctx context.Context, clientID string) (*Pin, error) {
	var pin Pin
	if err := c.doJSON(ctx, http.MethodPost, "/api/v2/pins?strong=true", clientID, "", nil, &pin); err != nil {
		return nil, fmt.Errorf("create pin: %w", err)
	}
	return &pin, nil
}

// CheckPin polls a PIN; AuthToken is non-empty once the admin approved it.
func (c *Client) CheckPin(ctx context.Context, clientID string, id int64) (*Pin, error) {
	var pin Pin
	if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/api/v2/pins/%d", id), clientID, "", nil, &pin); err != nil {
		return nil, fmt.Errorf("check pin: %w", err)
	}
	return &pin, nil
}

// AuthURL is the page the admin opens to approve the PIN. Always the real
// plex.tv app, never baseURL: it is user-facing, not an API call.
func (c *Client) AuthURL(clientID, code string) string {
	v := url.Values{
		"clientID":                {clientID},
		"code":                    {code},
		"context[device][product]": {c.product},
	}
	return "https://app.plex.tv/auth#?" + v.Encode()
}

// GetUser verifies a token and returns the account it belongs to.
func (c *Client) GetUser(ctx context.Context, clientID, token string) (*Account, error) {
	var acct Account
	if err := c.doJSON(ctx, http.MethodGet, "/api/v2/user", clientID, token, nil, &acct); err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	return &acct, nil
}

// ListServers returns the owned Plex Media Servers on the account.
func (c *Client) ListServers(ctx context.Context, clientID, token string) ([]Server, error) {
	var all []Server
	if err := c.doJSON(ctx, http.MethodGet, "/api/v2/resources?includeHttps=1", clientID, token, nil, &all); err != nil {
		return nil, fmt.Errorf("list servers: %w", err)
	}
	servers := make([]Server, 0, len(all))
	for _, s := range all {
		if s.Owned && strings.Contains(s.Provides, "server") {
			servers = append(servers, s)
		}
	}
	return servers, nil
}

// ListLibraries returns a server's library sections with their plex.tv-global
// ids. This is the one v1/XML endpoint: /api/servers/{machineID} is where the
// global section ids live (the JSON APIs only expose server-local keys).
func (c *Client) ListLibraries(ctx context.Context, clientID, token, machineID string) ([]Library, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/servers/"+url.PathEscape(machineID), nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req, clientID, token)
	req.Header.Set("Accept", "application/xml")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list libraries: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("list libraries: plex.tv answered %d", resp.StatusCode)
	}

	var container struct {
		Servers []struct {
			Sections []struct {
				ID    int64  `xml:"id,attr"`
				Title string `xml:"title,attr"`
				Type  string `xml:"type,attr"`
			} `xml:"Section"`
		} `xml:"Server"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&container); err != nil {
		return nil, fmt.Errorf("list libraries: decode: %w", err)
	}

	var libs []Library
	for _, srv := range container.Servers {
		for _, sec := range srv.Sections {
			libs = append(libs, Library{ID: sec.ID, Title: sec.Title, Type: sec.Type})
		}
	}
	return libs, nil
}

// InviteEmail shares the server's selected libraries with an email address.
// An empty sectionIDs list shares every library (plex.tv semantics). Returns
// ErrAlreadyShared when the account already has access.
func (c *Client) InviteEmail(ctx context.Context, clientID, token, machineID, email string, sectionIDs []int64) error {
	if sectionIDs == nil {
		sectionIDs = []int64{}
	}
	body := map[string]any{
		"machineIdentifier": machineID,
		"invitedEmail":      email,
		"librarySectionIds": sectionIDs,
		"settings":          map[string]any{},
	}
	err := c.doJSON(ctx, http.MethodPost, "/api/v2/shared_servers", clientID, token, body, nil)
	if err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.status == http.StatusUnprocessableEntity {
			return ErrAlreadyShared
		}
		return fmt.Errorf("invite: %w", err)
	}
	return nil
}

// apiError is a non-2xx plex.tv answer, keeping the status for callers that
// map specific codes (422 = duplicate share).
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string {
	if e.message != "" {
		return fmt.Sprintf("plex.tv answered %d: %s", e.status, e.message)
	}
	return fmt.Sprintf("plex.tv answered %d", e.status)
}

func (c *Client) setHeaders(req *http.Request, clientID, token string) {
	req.Header.Set("X-Plex-Product", c.product)
	req.Header.Set("X-Plex-Client-Identifier", clientID)
	if token != "" {
		req.Header.Set("X-Plex-Token", token)
	}
}

func (c *Client) doJSON(ctx context.Context, method, path, clientID, token string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	c.setHeaders(req, clientID, token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// plex.tv error bodies look like {"errors":[{"code":..,"message":".."}]}.
		var parsed struct {
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		msg := ""
		if data, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096)); readErr == nil {
			if json.Unmarshal(data, &parsed) == nil && len(parsed.Errors) > 0 {
				msg = parsed.Errors[0].Message
			}
		}
		return &apiError{status: resp.StatusCode, message: msg}
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
