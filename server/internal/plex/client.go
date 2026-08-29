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
	return NewClientAt(BaseURL)
}

// BaseURL is where plex.tv lives. A Plex instance's url field carries it so
// a lab can put a proxy in front of plex.tv; production never changes it.
const BaseURL = "https://plex.tv"

// NewClientAt is NewClient against another base URL (a lab proxy, a test
// server). The PIN approval page (AuthURL) still points at the real plex.tv.
func NewClientAt(baseURL string) *Client {
	return &Client{
		http: &http.Client{
			Timeout:       15 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
		baseURL: strings.TrimRight(baseURL, "/"),
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
		"clientID":                 {clientID},
		"code":                     {code},
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
// ids. This is a v1/XML endpoint: /api/servers/{machineID} is where the
// global section ids live (the JSON APIs only expose server-local keys).
func (c *Client) ListLibraries(ctx context.Context, clientID, token, machineID string) ([]Library, error) {
	container, err := c.getServer(ctx, clientID, token, machineID)
	if err != nil {
		return nil, fmt.Errorf("list libraries: %w", err)
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

// ServerInfo identifies an owned Plex Media Server as plex.tv knows it.
type ServerInfo struct {
	Name              string
	Version           string
	MachineIdentifier string
}

// Share is one account a server is shared with, as plex.tv lists it. ID is
// the share's own id (what an update or removal addresses), not the user's.
type Share struct {
	ID         int64
	UserID     int64
	Username   string
	Email      string
	Accepted   bool
	SectionIDs []int64
}

// Invite is a share invitation plex.tv is still holding for someone who has
// not accepted it (or has no account yet), from the owner's sent list. ID is
// whatever plex.tv keyed the invite by: a numeric user id for a registered
// account, the invited email itself for someone with no account yet.
type Invite struct {
	ID       string
	Username string
	Email    string
	// Machines names the servers the invite covers, when plex.tv says.
	Machines []string
	// Servers names them when only the name is given.
	Servers []string
}

// serverContainer is the XML plex.tv answers for /api/servers/{machineID}:
// the server's identity and its sections with their global ids.
type serverContainer struct {
	Servers []struct {
		Name              string `xml:"name,attr"`
		Version           string `xml:"version,attr"`
		MachineIdentifier string `xml:"machineIdentifier,attr"`
		Sections          []struct {
			ID    int64  `xml:"id,attr"`
			Title string `xml:"title,attr"`
			Type  string `xml:"type,attr"`
		} `xml:"Section"`
	} `xml:"Server"`
}

func (c *Client) getServer(ctx context.Context, clientID, token, machineID string) (*serverContainer, error) {
	var container serverContainer
	if err := c.doXML(ctx, http.MethodGet, "/api/servers/"+url.PathEscape(machineID), clientID, token, &container); err != nil {
		return nil, err
	}
	return &container, nil
}

// GetServer reads an owned server's identity: the connection test for a Plex
// instance, which proves both the token and that the account owns the server.
func (c *Client) GetServer(ctx context.Context, clientID, token, machineID string) (*ServerInfo, error) {
	container, err := c.getServer(ctx, clientID, token, machineID)
	if err != nil {
		return nil, fmt.Errorf("get server: %w", err)
	}
	for _, srv := range container.Servers {
		if srv.MachineIdentifier == "" || srv.MachineIdentifier == machineID {
			return &ServerInfo{Name: srv.Name, Version: srv.Version, MachineIdentifier: machineID}, nil
		}
	}
	return nil, fmt.Errorf("get server: plex.tv lists no server %s for this account", machineID)
}

// ListShares lists the accounts a server is shared with.
func (c *Client) ListShares(ctx context.Context, clientID, token, machineID string) ([]Share, error) {
	var container struct {
		Shares []struct {
			ID         int64  `xml:"id,attr"`
			UserID     int64  `xml:"userID,attr"`
			Username   string `xml:"username,attr"`
			Email      string `xml:"email,attr"`
			Accepted   string `xml:"accepted,attr"`
			AcceptedAt string `xml:"acceptedAt,attr"`
			Sections   []struct {
				ID     int64  `xml:"id,attr"`
				Shared string `xml:"shared,attr"`
			} `xml:"Section"`
		} `xml:"SharedServer"`
	}
	if err := c.doXML(ctx, http.MethodGet, "/api/servers/"+url.PathEscape(machineID)+"/shared_servers", clientID, token, &container); err != nil {
		return nil, fmt.Errorf("list shares: %w", err)
	}
	shares := make([]Share, 0, len(container.Shares))
	for _, s := range container.Shares {
		share := Share{ID: s.ID, UserID: s.UserID, Username: s.Username, Email: s.Email}
		// plex.tv has said "accepted" two ways over the years; either counts.
		share.Accepted = s.Accepted == "1" || s.Accepted == "true" ||
			(s.Accepted == "" && s.AcceptedAt != "" && s.AcceptedAt != "0")
		for _, sec := range s.Sections {
			if sec.Shared == "" || sec.Shared == "1" || sec.Shared == "true" {
				share.SectionIDs = append(share.SectionIDs, sec.ID)
			}
		}
		shares = append(shares, share)
	}
	return shares, nil
}

// ListInvites lists the share invitations the account has sent that nobody
// has accepted yet. plex.tv keeps these apart from the shares themselves.
func (c *Client) ListInvites(ctx context.Context, clientID, token string) ([]Invite, error) {
	var container struct {
		Invites []struct {
			ID       string `xml:"id,attr"`
			Username string `xml:"username,attr"`
			Email    string `xml:"email,attr"`
			Server   string `xml:"server,attr"`
			Servers  []struct {
				Name              string `xml:"name,attr"`
				MachineIdentifier string `xml:"machineIdentifier,attr"`
			} `xml:"Server"`
		} `xml:"Invite"`
	}
	if err := c.doXML(ctx, http.MethodGet, "/api/invites/requested", clientID, token, &container); err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	invites := make([]Invite, 0, len(container.Invites))
	for _, i := range container.Invites {
		if i.Server == "0" || i.Server == "false" {
			continue // a friend request, not a share
		}
		inv := Invite{ID: i.ID, Username: i.Username, Email: i.Email}
		for _, srv := range i.Servers {
			if srv.MachineIdentifier != "" {
				inv.Machines = append(inv.Machines, srv.MachineIdentifier)
			}
			if srv.Name != "" {
				inv.Servers = append(inv.Servers, srv.Name)
			}
		}
		invites = append(invites, inv)
	}
	return invites, nil
}

// UpdateShare replaces the libraries an existing share covers. An empty list
// shares every library, as on invite.
func (c *Client) UpdateShare(ctx context.Context, clientID, token, machineID string, shareID int64, sectionIDs []int64) error {
	if sectionIDs == nil {
		sectionIDs = []int64{}
	}
	body := map[string]any{
		"server_id":     machineID,
		"shared_server": map[string]any{"library_section_ids": sectionIDs},
	}
	path := fmt.Sprintf("/api/servers/%s/shared_servers/%d", url.PathEscape(machineID), shareID)
	if err := c.doJSON(ctx, http.MethodPut, path, clientID, token, body, nil); err != nil {
		return fmt.Errorf("update share: %w", err)
	}
	return nil
}

// RemoveShare takes a server away from an account. The friendship, if any,
// is untouched; plex.tv treats the two separately.
func (c *Client) RemoveShare(ctx context.Context, clientID, token, machineID string, shareID int64) error {
	path := fmt.Sprintf("/api/servers/%s/shared_servers/%d", url.PathEscape(machineID), shareID)
	if err := c.doJSON(ctx, http.MethodDelete, path, clientID, token, nil, nil); err != nil {
		return fmt.Errorf("remove share: %w", err)
	}
	return nil
}

// CancelInvite withdraws a share invitation nobody has accepted. Only the
// server share is withdrawn (server=1); a friend or home invite that rode
// along stays.
func (c *Client) CancelInvite(ctx context.Context, clientID, token string, inviteID string) error {
	path := "/api/invites/requested/" + url.PathEscape(inviteID) + "?friend=0&home=0&server=1"
	if err := c.doJSON(ctx, http.MethodDelete, path, clientID, token, nil, nil); err != nil {
		return fmt.Errorf("cancel invite: %w", err)
	}
	return nil
}

// doXML is the v1 transport: the older plex.tv endpoints answer XML.
func (c *Client) doXML(ctx context.Context, method, path, clientID, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	c.setHeaders(req, clientID, token)
	req.Header.Set("Accept", "application/xml")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &apiError{status: resp.StatusCode}
	}
	if out == nil {
		return nil
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(out); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}
