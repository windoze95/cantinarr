// Package audiobookshelf implements local account access using Audiobookshelf's
// API-key API. Wire fixtures and permission semantics target version 2.36.0.
package audiobookshelf

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/httpx"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
	"github.com/windoze95/cantinarr-server/internal/transporterr"
)

type Client struct {
	baseURL, apiKey string
	httpClient      *http.Client
}

var (
	_       mediaserver.Provider      = (*Client)(nil)
	_       mediaserver.Authenticator = (*Client)(nil)
	_       mediaserver.BookFinder    = (*Client)(nil)
	validID                           = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

func NewClient(baseURL, apiKey string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, httpClient: &http.Client{
		Transport: httpx.Internal(), Timeout: 30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

type statusError int

func (e statusError) Error() string { return fmt.Sprintf("audiobookshelf returned status %d", int(e)) }

func statusOf(err error) int {
	var status statusError
	if errors.As(err, &status) {
		return int(status)
	}
	return 0
}

// doAs never follows redirects or exposes response bodies/URLs in errors.
// Login and logout explicitly pass an empty API key.
func (c *Client) doAs(ctx context.Context, method, path, token string, headers map[string]string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return errors.New("audiobookshelf could not encode request")
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return errors.New("audiobookshelf address is invalid")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("audiobookshelf request: %s", transporterr.Summarize(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusError(resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(data) > 8<<20 {
		return errors.New("audiobookshelf response could not be read")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return errors.New("audiobookshelf returned an invalid response")
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	return c.doAs(ctx, method, path, c.apiKey, nil, body, out)
}

// Pointers distinguish a missing permission field from an explicit refusal.
// Current responses keep library/tag selections beside permissions.
type permissions struct {
	Download              *bool `json:"download"`
	Update                *bool `json:"update"`
	Delete                *bool `json:"delete"`
	Upload                *bool `json:"upload"`
	CreateEreader         *bool `json:"createEreader"`
	AccessAllLibraries    *bool `json:"accessAllLibraries"`
	AccessAllTags         *bool `json:"accessAllTags"`
	AccessExplicitContent *bool `json:"accessExplicitContent"`
	SelectedTagsDenied    *bool `json:"selectedTagsNotAccessible"`
}

type user struct {
	ID          string      `json:"id"`
	Username    string      `json:"username"`
	Type        string      `json:"type"`
	Active      *bool       `json:"isActive"`
	Permissions permissions `json:"permissions"`
	Libraries   []string    `json:"librariesAccessible"`
	Tags        []string    `json:"itemTagsSelected"`
}

func (u user) valid() bool {
	return validID.MatchString(u.ID) && u.Username != "" && u.Active != nil &&
		slices.Contains([]string{"root", "admin", "user", "guest"}, u.Type)
}

func (u user) remote() mediaserver.RemoteUser {
	return mediaserver.RemoteUser{ID: u.ID, Name: u.Username,
		IsAdministrator: u.Type == "root" || u.Type == "admin", IsDisabled: u.Active == nil || !*u.Active}
}

func (c *Client) SystemInfo(ctx context.Context) (mediaserver.SystemInfo, error) {
	var status struct {
		App         string `json:"app"`
		Version     string `json:"serverVersion"`
		Initialized bool   `json:"isInit"`
	}
	if err := c.doAs(ctx, "GET", "/status", "", nil, nil, &status); err != nil {
		return mediaserver.SystemInfo{}, err
	}
	if status.App != "audiobookshelf" || !status.Initialized || status.Version == "" {
		return mediaserver.SystemInfo{}, errors.New("audiobookshelf server is not initialized")
	}
	var auth struct {
		User user `json:"user"`
	}
	if err := c.do(ctx, "POST", "/api/authorize", nil, &auth); err != nil {
		return mediaserver.SystemInfo{}, err
	}
	if !auth.User.valid() || !auth.User.remote().IsAdministrator || auth.User.remote().IsDisabled {
		return mediaserver.SystemInfo{}, errors.New("audiobookshelf requires an active administrator API key")
	}
	return mediaserver.SystemInfo{ServerName: "Audiobookshelf", Version: status.Version}, nil
}

func (c *Client) Libraries(ctx context.Context) ([]mediaserver.Library, error) {
	var response struct {
		Libraries []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			MediaType string `json:"mediaType"`
		} `json:"libraries"`
	}
	if err := c.do(ctx, "GET", "/api/libraries", nil, &response); err != nil {
		return nil, err
	}
	if response.Libraries == nil {
		return nil, errors.New("audiobookshelf returned an incomplete library list")
	}
	out := make([]mediaserver.Library, 0, len(response.Libraries))
	for _, library := range response.Libraries {
		if !validID.MatchString(library.ID) || library.Name == "" {
			return nil, errors.New("audiobookshelf returned an invalid library")
		}
		out = append(out, mediaserver.Library{ID: library.ID, Name: library.Name, CollectionType: library.MediaType})
	}
	return out, nil
}

func (c *Client) Users(ctx context.Context) ([]mediaserver.RemoteUser, error) {
	var response struct {
		Users []user `json:"users"`
	}
	if err := c.do(ctx, "GET", "/api/users", nil, &response); err != nil {
		return nil, err
	}
	users := response.Users
	if users == nil {
		return nil, errors.New("audiobookshelf returned an incomplete user list")
	}
	out := make([]mediaserver.RemoteUser, 0, len(users))
	for _, u := range users {
		if !u.valid() {
			return nil, errors.New("audiobookshelf returned an invalid user")
		}
		out = append(out, u.remote())
	}
	return out, nil
}

func (c *Client) getUser(ctx context.Context, id string) (user, error) {
	if !validID.MatchString(id) {
		return user{}, mediaserver.ErrUserNotFound
	}
	var u user
	if err := c.do(ctx, "GET", "/api/users/"+url.PathEscape(id), nil, &u); err != nil {
		if statusOf(err) == 404 {
			return user{}, mediaserver.ErrUserNotFound
		}
		return user{}, err
	}
	if !u.valid() || u.ID != id {
		return user{}, errors.New("audiobookshelf returned an invalid user")
	}
	return u, nil
}

func (c *Client) GetUser(ctx context.Context, id string) (mediaserver.RemoteUser, error) {
	u, err := c.getUser(ctx, id)
	return u.remote(), err
}

func libraryPolicy(ids []string) map[string]any {
	return map[string]any{"permissions": map[string]any{"accessAllLibraries": len(ids) == 0}, "librariesAccessible": append([]string{}, ids...)}
}

func (u user) hasLibraries(ids []string) bool {
	if u.Permissions.AccessAllLibraries == nil || *u.Permissions.AccessAllLibraries != (len(ids) == 0) {
		return false
	}
	if len(ids) == 0 {
		return true
	}
	a, b := slices.Clone(u.Libraries), slices.Clone(ids)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func (u user) hasNewAccountPolicy() bool {
	p := u.Permissions
	for _, permission := range []*bool{p.Update, p.Delete, p.Upload, p.CreateEreader, p.AccessExplicitContent, p.SelectedTagsDenied} {
		if permission == nil || *permission {
			return false
		}
	}
	return p.Download != nil && *p.Download && p.AccessAllTags != nil && *p.AccessAllTags
}

func (c *Client) CreateUser(ctx context.Context, name, password string, ids []string) (mediaserver.RemoteUser, error) {
	if !mediaserver.ValidUsername(name) {
		return mediaserver.RemoteUser{}, mediaserver.ErrInvalidName
	}
	if password == "" {
		return mediaserver.RemoteUser{}, mediaserver.ErrBadCredentials
	}
	users, err := c.Users(ctx)
	if err != nil {
		return mediaserver.RemoteUser{}, err
	}
	for _, u := range users {
		if strings.EqualFold(u.Name, name) {
			return mediaserver.RemoteUser{}, mediaserver.ErrUserExists
		}
	}
	body := libraryPolicy(ids)
	body["username"], body["password"], body["type"], body["isActive"] = name, password, "user", true
	policy := body["permissions"].(map[string]any)
	for key, value := range map[string]bool{"download": true, "update": false, "delete": false, "upload": false, "createEreader": false, "accessAllTags": true, "accessExplicitContent": false, "selectedTagsNotAccessible": false} {
		policy[key] = value
	}
	var response struct {
		User user `json:"user"`
	}
	if err := c.do(ctx, "POST", "/api/users", body, &response); err != nil {
		// A concurrent creation can take the name after our pre-check.
		if statusOf(err) == 500 || statusOf(err) == 400 {
			if users, lookupErr := c.Users(ctx); lookupErr == nil {
				for _, u := range users {
					if strings.EqualFold(u.Name, name) {
						return mediaserver.RemoteUser{}, mediaserver.ErrUserExists
					}
				}
			}
		}
		if statusOf(err) == 0 {
			return mediaserver.RemoteUser{}, c.rollbackUnconfirmedCreation(name, password, users)
		}
		return mediaserver.RemoteUser{}, err
	}
	created := response.User
	if !validID.MatchString(created.ID) {
		return mediaserver.RemoteUser{}, c.rollbackUnconfirmedCreation(name, password, users)
	}
	for _, existing := range users {
		if existing.ID == created.ID {
			return mediaserver.RemoteUser{}, errors.New("audiobookshelf did not return a new account")
		}
	}
	live, err := c.getUser(ctx, created.ID)
	if err == nil && live.Type == "user" && !live.remote().IsDisabled && live.Username == name && live.hasLibraries(ids) && live.hasNewAccountPolicy() {
		return live.remote(), nil
	}
	// Cleanup must still run when the original request timed out.
	cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if rollback := c.DeleteUser(cleanup, created.ID); rollback != nil {
		return mediaserver.RemoteUser{}, errors.New("audiobookshelf account setup failed; administrator must check the new account")
	}
	return mediaserver.RemoteUser{}, errors.New("audiobookshelf did not confirm the new account's access")
}

// A lost/unreadable create response may have left an account behind. Only
// remove a newly observed ordinary account that accepts the password from
// this attempt; a concurrent administrator-created namesake is not ours.
func (c *Client) rollbackUnconfirmedCreation(name, password string, before []mediaserver.RemoteUser) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	unconfirmed := errors.New("audiobookshelf account creation was not confirmed; administrator must check the new account")
	users, err := c.Users(ctx)
	if err != nil {
		return unconfirmed
	}
	for _, candidate := range users {
		if !strings.EqualFold(candidate.Name, name) {
			continue
		}
		if candidate.IsAdministrator || slices.ContainsFunc(before, func(u mediaserver.RemoteUser) bool { return u.ID == candidate.ID }) {
			return unconfirmed
		}
		verified, err := c.Authenticate(ctx, name, password)
		if err != nil || verified.ID != candidate.ID || c.DeleteUser(ctx, candidate.ID) != nil {
			return unconfirmed
		}
		break
	}
	return errors.New("audiobookshelf did not confirm account creation")
}

func (c *Client) SetLibraries(ctx context.Context, id string, ids []string) error {
	u, err := c.getUser(ctx, id)
	if err != nil || u.remote().IsAdministrator {
		return err
	}
	if err := c.do(ctx, "PATCH", "/api/users/"+url.PathEscape(id), libraryPolicy(ids), nil); err != nil {
		return err
	}
	u, err = c.getUser(ctx, id)
	if err != nil {
		return err
	}
	if !u.hasLibraries(ids) {
		return errors.New("audiobookshelf did not apply library access")
	}
	return nil
}

func (c *Client) SetDisabled(ctx context.Context, id string, disabled bool) error {
	u, err := c.getUser(ctx, id)
	if err != nil || u.remote().IsAdministrator {
		return err
	}
	if err := c.do(ctx, "PATCH", "/api/users/"+url.PathEscape(id), map[string]bool{"isActive": !disabled}, nil); err != nil {
		return err
	}
	u, err = c.getUser(ctx, id)
	if err != nil {
		return err
	}
	if u.remote().IsDisabled != disabled {
		return errors.New("audiobookshelf did not apply account access")
	}
	return nil
}

func (c *Client) DeleteUser(ctx context.Context, id string) error {
	u, err := c.getUser(ctx, id)
	if errors.Is(err, mediaserver.ErrUserNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if u.remote().IsAdministrator {
		return errors.New("audiobookshelf administrator accounts cannot be deleted")
	}
	return c.do(ctx, "DELETE", "/api/users/"+url.PathEscape(id), nil, nil)
}

func (c *Client) Authenticate(ctx context.Context, name, password string) (mediaserver.RemoteUser, error) {
	var response struct {
		User struct {
			user
			RefreshToken string `json:"refreshToken"`
		} `json:"user"`
	}
	err := c.doAs(ctx, "POST", "/login", "", map[string]string{"X-Return-Tokens": "true"}, map[string]string{"username": name, "password": password}, &response)
	if err != nil {
		if statusOf(err) == 401 {
			return mediaserver.RemoteUser{}, mediaserver.ErrBadCredentials
		}
		if statusOf(err) == 403 {
			return mediaserver.RemoteUser{}, mediaserver.ErrAccountRefused
		}
		return mediaserver.RemoteUser{}, err
	}
	if response.User.RefreshToken == "" {
		return mediaserver.RemoteUser{}, errors.New("audiobookshelf did not return a temporary sign-in session")
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.doAs(cleanup, "POST", "/logout", "", map[string]string{"X-Refresh-Token": response.User.RefreshToken}, nil, nil); err != nil {
		return mediaserver.RemoteUser{}, err
	}
	if !response.User.valid() || response.User.remote().IsDisabled {
		return mediaserver.RemoteUser{}, mediaserver.ErrAccountRefused
	}
	return response.User.remote(), nil
}

// sameAccess excludes display data: a policy or activity change invalidates
// in-flight title reads. No user token is decoded, stored, or reused.
func sameAccess(a, b user) bool {
	return a.ID == b.ID && a.Type == b.Type && reflect.DeepEqual(a.Active, b.Active) &&
		reflect.DeepEqual(a.Permissions, b.Permissions) && slices.Equal(a.Libraries, b.Libraries) && slices.Equal(a.Tags, b.Tags)
}
