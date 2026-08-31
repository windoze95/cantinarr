// Package jellyfin is the Jellyfin implementation of mediaserver.Provider:
// an API-key client that reads the server's libraries and users and manages
// the accounts Cantinarr provisions on it.
package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/mediaserver"
	"github.com/windoze95/cantinarr-server/internal/transporterr"
)

// Client talks to one Jellyfin server with an API key. API keys hold the
// Administrator role on Jellyfin, which every call here relies on.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

var (
	_ mediaserver.Provider      = (*Client)(nil)
	_ mediaserver.Authenticator = (*Client)(nil)
)

// The API key acts under one device identity; a person's sign-in check runs
// under a second, fixed one, so the check never attaches to the key's device
// record and repeated checks never pile up devices on the server's dashboard.
const (
	apiDevice      = "Cantinarr"
	apiDeviceID    = "cantinarr"
	signInDevice   = "Cantinarr sign-in check"
	signInDeviceID = "cantinarr-signin"
)

// NewClient creates a client for the server at baseURL. Redirects are never
// followed: a redirect would otherwise hand the API key to whatever host the
// server named.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// statusError is a non-2xx answer. The body is kept for classification and
// deliberately never rendered: Jellyfin error bodies can echo request data.
type statusError struct {
	status int
	body   []byte
}

func (e *statusError) Error() string {
	switch e.status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "jellyfin rejected the API key"
	default:
		return fmt.Sprintf("jellyfin returned status %d", e.status)
	}
}

// do performs one request as the API key. op names the operation in errors
// instead of the URL, so nothing about the host ever appears in an error
// string.
func (c *Client) do(ctx context.Context, method, path, op string, body, out any) error {
	return c.doAs(ctx, method, path, op, c.apiKey, apiDevice, apiDeviceID, body, out)
}

// doAs is do under an explicit token and device identity. An empty token
// sends the device header alone, which is how a sign-in check introduces
// itself without any credential of Cantinarr's.
func (c *Client) doAs(ctx context.Context, method, path, op, token, device, deviceID string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("jellyfin %s: encode request: %w", op, err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("jellyfin %s: invalid url", op)
	}
	if token != "" {
		req.Header.Set("Authorization", `MediaBrowser Token="`+token+`", Client="Cantinarr", Device="`+device+`", DeviceId="`+deviceID+`", Version="1"`)
		req.Header.Set("X-Emby-Token", token)
	} else {
		req.Header.Set("Authorization", `MediaBrowser Client="Cantinarr", Device="`+device+`", DeviceId="`+deviceID+`", Version="1"`)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("jellyfin %s: %s", op, transporterr.Summarize(err))
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return fmt.Errorf("jellyfin %s: read response: %w", op, err)
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return fmt.Errorf("jellyfin %s: server returned redirect status %d (redirects are not followed; use the server's final URL)", op, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &statusError{status: resp.StatusCode, body: data}
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("jellyfin %s: decode response: %w", op, err)
		}
	}
	return nil
}

func statusOf(err error) int {
	var se *statusError
	if errors.As(err, &se) {
		return se.status
	}
	return 0
}

type systemInfo struct {
	ServerName string `json:"ServerName"`
	Version    string `json:"Version"`
	ID         string `json:"Id"`
}

// virtualFolder deliberately declares no Locations: those are server-side
// filesystem paths and must never travel further than Jellyfin itself.
type virtualFolder struct {
	Name           string `json:"Name"`
	CollectionType string `json:"CollectionType"`
	ItemID         string `json:"ItemId"`
}

type userDTO struct {
	ID     string          `json:"Id"`
	Name   string          `json:"Name"`
	Policy json.RawMessage `json:"Policy"`
}

type policyFlags struct {
	IsAdministrator bool `json:"IsAdministrator"`
	IsDisabled      bool `json:"IsDisabled"`
}

func (d userDTO) remoteUser() mediaserver.RemoteUser {
	var flags policyFlags
	if len(d.Policy) > 0 {
		_ = json.Unmarshal(d.Policy, &flags)
	}
	return mediaserver.RemoteUser{
		ID:              d.ID,
		Name:            d.Name,
		IsAdministrator: flags.IsAdministrator,
		IsDisabled:      flags.IsDisabled,
	}
}

// SystemInfo reads the server identity; it doubles as the connection test.
func (c *Client) SystemInfo(ctx context.Context) (mediaserver.SystemInfo, error) {
	var info systemInfo
	if err := c.do(ctx, http.MethodGet, "/System/Info", "system info", nil, &info); err != nil {
		return mediaserver.SystemInfo{}, err
	}
	return mediaserver.SystemInfo{ServerName: info.ServerName, Version: info.Version, ID: info.ID}, nil
}

// Libraries lists the server's libraries. The returned ID is the ItemId the
// user policy's EnabledFolders expects.
func (c *Client) Libraries(ctx context.Context) ([]mediaserver.Library, error) {
	var folders []virtualFolder
	if err := c.do(ctx, http.MethodGet, "/Library/VirtualFolders", "list libraries", nil, &folders); err != nil {
		return nil, err
	}
	out := make([]mediaserver.Library, 0, len(folders))
	for _, f := range folders {
		if f.ItemID == "" {
			continue
		}
		out = append(out, mediaserver.Library{ID: f.ItemID, Name: f.Name, CollectionType: f.CollectionType})
	}
	return out, nil
}

// Users lists every account on the server.
func (c *Client) Users(ctx context.Context) ([]mediaserver.RemoteUser, error) {
	var dtos []userDTO
	if err := c.do(ctx, http.MethodGet, "/Users", "list users", nil, &dtos); err != nil {
		return nil, err
	}
	out := make([]mediaserver.RemoteUser, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, d.remoteUser())
	}
	return out, nil
}

func userPath(remoteID string) string {
	return "/Users/" + url.PathEscape(remoteID)
}

// itemDTO is the slice of a library item Cantinarr reads: its id and the
// provider ids that identify the title. No paths, no media info.
type itemDTO struct {
	ID          string            `json:"Id"`
	ProviderIDs map[string]string `json:"ProviderIds"`
}

type itemsResponse struct {
	Items []itemDTO `json:"Items"`
}

var itemTypes = map[string]string{"movie": "Movie", "tv": "Series"}

// FindItem implements mediaserver.ItemFinder. The query runs as the linked
// account (userId), so Jellyfin applies that account's library access, and
// the answer is matched on provider ids, never on a name alone. Jellyfin has
// no provider-id filter of its own (10.11 silently ignores
// anyProviderIdEquals), so the candidates are narrowed by production year, a
// three-year window around the title's year since both sides took the date
// from the same metadata, or by the title when no year is known. The item's
// page is the web client's details route, keyed by item and server id.
func (c *Client) FindItem(ctx context.Context, remoteUserID string, q mediaserver.ItemQuery) (mediaserver.Item, error) {
	itemType, ok := itemTypes[q.MediaType]
	if !ok {
		return mediaserver.Item{}, errors.New("jellyfin find item: unsupported media type")
	}
	if q.TMDBID <= 0 && q.TVDBID <= 0 {
		return mediaserver.Item{}, errors.New("jellyfin find item: no provider id to match")
	}
	params := url.Values{}
	params.Set("userId", remoteUserID)
	params.Set("recursive", "true")
	params.Set("includeItemTypes", itemType)
	params.Set("fields", "ProviderIds")
	params.Set("enableImages", "false")
	params.Set("enableUserData", "false")
	params.Set("enableTotalRecordCount", "false")
	switch {
	case q.Year > 0:
		params.Set("years", fmt.Sprintf("%d,%d,%d", q.Year-1, q.Year, q.Year+1))
	case strings.TrimSpace(q.Title) != "":
		params.Set("searchTerm", strings.TrimSpace(q.Title))
	default:
		return mediaserver.Item{}, errors.New("jellyfin find item: no year or title to narrow by")
	}
	var resp itemsResponse
	if err := c.do(ctx, http.MethodGet, "/Items?"+params.Encode(), "find item", nil, &resp); err != nil {
		return mediaserver.Item{}, err
	}
	for _, item := range resp.Items {
		if item.ID == "" || !mediaserver.ItemMatches(item.ProviderIDs, q) {
			continue
		}
		info, err := c.SystemInfo(ctx)
		if err != nil {
			return mediaserver.Item{}, err
		}
		return mediaserver.Item{ID: item.ID, WebPath: itemWebPath(item.ID, info.ID)}, nil
	}
	return mediaserver.Item{}, mediaserver.ErrItemNotFound
}

// itemWebPath is the Jellyfin web client's page for an item, under the
// server root: the "details" route of jellyfin-web, which also serves the
// older "#!/" form through a redirect.
func itemWebPath(itemID, serverID string) string {
	return "/web/#/details?id=" + url.QueryEscape(itemID) + "&serverId=" + url.QueryEscape(serverID)
}

func (c *Client) getUser(ctx context.Context, remoteID string) (userDTO, error) {
	var dto userDTO
	err := c.do(ctx, http.MethodGet, userPath(remoteID), "get user", nil, &dto)
	if statusOf(err) == http.StatusNotFound {
		return userDTO{}, mediaserver.ErrUserNotFound
	}
	if err != nil {
		return userDTO{}, err
	}
	return dto, nil
}

// GetUser reads one account; ErrUserNotFound when it no longer exists.
func (c *Client) GetUser(ctx context.Context, remoteID string) (mediaserver.RemoteUser, error) {
	dto, err := c.getUser(ctx, remoteID)
	if err != nil {
		return mediaserver.RemoteUser{}, err
	}
	return dto.remoteUser(), nil
}

// authResult is what /Users/AuthenticateByName answers: the account and a
// session token for the device that asked.
type authResult struct {
	User        userDTO `json:"User"`
	AccessToken string  `json:"AccessToken"`
}

// Authenticate implements mediaserver.Authenticator: the person's own
// username and password are checked with the server under the sign-in
// check's device identity and without the API key, and the session the
// check opened is closed again. Jellyfin answers 401 for a wrong password
// or an unknown name and 403 for an account it refuses (disabled, or a
// network or schedule rule), before or after looking at the password.
func (c *Client) Authenticate(ctx context.Context, username, password string) (mediaserver.RemoteUser, error) {
	body := struct {
		Username string `json:"Username"`
		Pw       string `json:"Pw"`
	}{Username: username, Pw: password}
	var result authResult
	err := c.doAs(ctx, http.MethodPost, "/Users/AuthenticateByName", "sign-in check", "", signInDevice, signInDeviceID, body, &result)
	switch statusOf(err) {
	case http.StatusUnauthorized:
		return mediaserver.RemoteUser{}, mediaserver.ErrBadCredentials
	case http.StatusForbidden:
		return mediaserver.RemoteUser{}, mediaserver.ErrAccountRefused
	}
	if err != nil {
		return mediaserver.RemoteUser{}, err
	}
	if result.AccessToken != "" {
		// Best effort, on a fresh context: the session is the check's to
		// close, not to keep. A logout that fails leaves one idle session
		// under the check's own device name for the server to expire.
		logoutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = c.doAs(logoutCtx, http.MethodPost, "/Sessions/Logout", "sign-in check logout", result.AccessToken, signInDevice, signInDeviceID, nil, nil)
	}
	if result.User.ID == "" {
		return mediaserver.RemoteUser{}, errors.New("jellyfin sign-in check: response carried no user")
	}
	return result.User.remoteUser(), nil
}

// updatePolicy is the single path for every policy change. Jellyfin's
// POST /Users/{id}/Policy replaces the whole policy and validates required
// fields, so the policy is always fetched fresh, mutated as a map (unknown
// fields and numbers round-trip untouched), and posted back in full.
func (c *Client) updatePolicy(ctx context.Context, remoteID string, mutate func(policy map[string]any)) error {
	dto, err := c.getUser(ctx, remoteID)
	if err != nil {
		return err
	}
	policy := map[string]any{}
	if len(dto.Policy) > 0 {
		dec := json.NewDecoder(bytes.NewReader(dto.Policy))
		dec.UseNumber()
		if err := dec.Decode(&policy); err != nil {
			return fmt.Errorf("jellyfin get user: decode policy: %w", err)
		}
	}
	if len(policy) == 0 {
		return errors.New("jellyfin get user: policy missing from response")
	}
	mutate(policy)
	return c.do(ctx, http.MethodPost, userPath(remoteID)+"/Policy", "update user policy", policy, nil)
}

func setLibraries(policy map[string]any, libraryIDs []string) {
	if len(libraryIDs) == 0 {
		policy["EnableAllFolders"] = true
		policy["EnabledFolders"] = []string{}
		return
	}
	policy["EnableAllFolders"] = false
	policy["EnabledFolders"] = append([]string(nil), libraryIDs...)
}

// rollbackByName deletes an account that carries name, for the case where
// the create request may have reached the server but its answer never came
// back readable (a cut connection, a timeout, an unreadable body). The
// pre-check proved no account carried this name a moment ago, so one that
// does now is the one just made. Runs on a fresh context.
func (c *Client) rollbackByName(ctx context.Context, name string) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	users, err := c.Users(cleanupCtx)
	if err != nil {
		return fmt.Errorf("look up new user for roll back: %w", err)
	}
	for _, u := range users {
		if strings.EqualFold(u.Name, name) {
			if err := c.DeleteUser(cleanupCtx, u.ID); err != nil {
				return fmt.Errorf("roll back new user: %w", err)
			}
			return nil
		}
	}
	return nil
}

// CreateUser implements mediaserver.Provider.
func (c *Client) CreateUser(ctx context.Context, name, password string, libraryIDs []string) (mediaserver.RemoteUser, error) {
	if !mediaserver.ValidUsername(name) {
		return mediaserver.RemoteUser{}, mediaserver.ErrInvalidName
	}
	// Jellyfin compares names case-insensitively; pre-check so a collision is
	// reported deterministically instead of depending on the 400 body.
	existing, err := c.Users(ctx)
	if err != nil {
		return mediaserver.RemoteUser{}, err
	}
	for _, u := range existing {
		if strings.EqualFold(u.Name, name) {
			return mediaserver.RemoteUser{}, mediaserver.ErrUserExists
		}
	}

	var created userDTO
	body := struct {
		Name     string `json:"Name"`
		Password string `json:"Password"`
	}{Name: name, Password: password}
	if err := c.do(ctx, http.MethodPost, "/Users/New", "create user", body, &created); err != nil {
		var se *statusError
		if errors.As(err, &se) && se.status == http.StatusBadRequest && bytes.Contains(bytes.ToLower(se.body), []byte("exist")) {
			return mediaserver.RemoteUser{}, mediaserver.ErrUserExists
		}
		if se == nil {
			// Not a refusal: the server may have made the account and the
			// answer got lost. Never leave an account Cantinarr cannot see.
			if rbErr := c.rollbackByName(ctx, name); rbErr != nil {
				return mediaserver.RemoteUser{}, errors.Join(err, rbErr)
			}
		}
		return mediaserver.RemoteUser{}, err
	}
	if created.ID == "" {
		err := errors.New("jellyfin create user: response carried no user id")
		if rbErr := c.rollbackByName(ctx, name); rbErr != nil {
			return mediaserver.RemoteUser{}, errors.Join(err, rbErr)
		}
		return mediaserver.RemoteUser{}, err
	}

	if err := c.updatePolicy(ctx, created.ID, func(policy map[string]any) {
		policy["IsAdministrator"] = false
		setLibraries(policy, libraryIDs)
	}); err != nil {
		// Never leave an unrestricted account behind. Rollback uses a fresh
		// context so a cancelled create still cleans up.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if delErr := c.DeleteUser(cleanupCtx, created.ID); delErr != nil {
			return mediaserver.RemoteUser{}, errors.Join(fmt.Errorf("restrict new user: %w", err), fmt.Errorf("roll back new user: %w", delErr))
		}
		return mediaserver.RemoteUser{}, fmt.Errorf("restrict new user: %w", err)
	}
	return mediaserver.RemoteUser{ID: created.ID, Name: created.Name}, nil
}

// SetLibraries implements mediaserver.Provider.
func (c *Client) SetLibraries(ctx context.Context, remoteID string, libraryIDs []string) error {
	return c.updatePolicy(ctx, remoteID, func(policy map[string]any) { setLibraries(policy, libraryIDs) })
}

// SetDisabled implements mediaserver.Provider.
func (c *Client) SetDisabled(ctx context.Context, remoteID string, disabled bool) error {
	return c.updatePolicy(ctx, remoteID, func(policy map[string]any) { policy["IsDisabled"] = disabled })
}

// DeleteUser implements mediaserver.Provider. An account that is already
// gone counts as deleted.
func (c *Client) DeleteUser(ctx context.Context, remoteID string) error {
	err := c.do(ctx, http.MethodDelete, userPath(remoteID), "delete user", nil, nil)
	if statusOf(err) == http.StatusNotFound {
		return nil
	}
	return err
}
