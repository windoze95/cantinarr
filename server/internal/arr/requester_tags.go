package arr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/transporterr"
)

// RequesterTags shares native tag creation and verified delivery across arrs.
type RequesterTags struct {
	client                  *http.Client
	baseURL, apiKey         string
	collection, identityKey string
	apiPath                 string
}

func NewRequesterTags(client *http.Client, baseURL, apiKey, collection, identityKey string) *RequesterTags {
	apiPath := "/api/v3"
	if collection == "artist" || collection == "author" {
		apiPath = "/api/v1"
	}
	return &RequesterTags{client: client, baseURL: baseURL, apiKey: apiKey, collection: collection, identityKey: identityKey, apiPath: apiPath}
}

type nativeTag struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}

type taggedTitle struct {
	ID     int   `json:"id"`
	TMDBID int   `json:"tmdbId"`
	TVDBID int   `json:"tvdbId"`
	Tags   []int `json:"tags"`
}

var ErrTagIdentity = errors.New("The saved title could not be verified in the destination library.")

func (c *RequesterTags) matches(title taggedTitle, externalID int) bool {
	if c.identityKey == "tmdbId" {
		return title.ID > 0 && title.TMDBID == externalID
	}
	return title.ID > 0 && title.TVDBID == externalID
}

func (c *RequesterTags) do(ctx context.Context, method, path string, body, out any, beforeWrite func() error) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return errors.New("Could not encode the requester tag.")
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return errors.New("The library address is invalid.")
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		if beforeWrite == nil {
			return errors.New("Requester tag authorization is missing.")
		}
		if err := beforeWrite(); err != nil {
			return err
		}
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return transporterr.Connection("Could not update requester tags: ", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return transporterr.HTTP(fmt.Sprintf("The library returned HTTP %d while updating requester tags.", resp.StatusCode), resp)
	}
	if out != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out); err != nil {
			return &transporterr.Upstream{Message: "The library returned an unreadable requester tag response.", Transient: true}
		}
	}
	return nil
}

func (c *RequesterTags) findTag(ctx context.Context, prefix string) (*nativeTag, error) {
	var tags []nativeTag
	if err := c.do(ctx, http.MethodGet, c.apiPath+"/tag", nil, &tags, nil); err != nil {
		return nil, err
	}
	// If an admin made multiple labels in this namespace, use the oldest
	// native ID consistently. Never delete or rename any of them.
	var found *nativeTag
	for _, tag := range tags {
		if tag.ID > 0 && strings.HasPrefix(strings.ToLower(tag.Label), prefix) && (found == nil || tag.ID < found.ID) {
			copy := tag
			found = &copy
		}
	}
	return found, nil
}

// Apply reuses the stable user-ID prefix, including after username changes.
// A lost create/update response is safe to retry: each attempt reads native
// state first and editor applyTags=add is idempotent.
func (c *RequesterTags) Apply(ctx context.Context, externalID int, prefix, label string, beforeWrite func() error) (string, error) {
	if beforeWrite == nil {
		return "", errors.New("Requester tag authorization is missing.")
	}
	var titles []taggedTitle
	path := "/api/v3/" + c.collection
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s?%s=%d", path, c.identityKey, externalID), nil, &titles, nil); err != nil {
		return "", err
	}
	if externalID <= 0 || len(titles) != 1 || !c.matches(titles[0], externalID) {
		return "", ErrTagIdentity
	}
	readTitle := func() (*taggedTitle, error) {
		var title taggedTitle
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/%d", path, titles[0].ID), nil, &title, nil); err != nil {
			return nil, err
		}
		if title.ID != titles[0].ID || !c.matches(title, externalID) {
			return nil, ErrTagIdentity
		}
		return &title, nil
	}
	add := func(tagID int) error {
		body := map[string]any{c.collection + "Ids": []int{titles[0].ID}, "tags": []int{tagID}, "applyTags": "add"}
		return c.do(ctx, http.MethodPut, path+"/editor", body, nil, beforeWrite)
	}
	return c.apply(ctx, prefix, label, readTitle, add, beforeWrite)
}

func (c *RequesterTags) apply(ctx context.Context, prefix, label string, readTitle func() (*taggedTitle, error), add func(int) error, beforeWrite func() error) (string, error) {
	if beforeWrite == nil {
		return "", errors.New("Requester tag authorization is missing.")
	}
	if _, err := readTitle(); err != nil {
		return "", err
	}
	tag, err := c.findTag(ctx, prefix)
	if err != nil {
		return "", err
	}
	if tag == nil {
		var created nativeTag
		err = c.do(ctx, http.MethodPost, c.apiPath+"/tag", map[string]string{"label": label}, &created, beforeWrite)
		if err == nil && created.ID > 0 && strings.EqualFold(created.Label, label) {
			tag = &created
		} else {
			// A concurrent creator or a lost POST response may already have
			// created it. Read before deciding whether this attempt failed.
			var readErr error
			tag, readErr = c.findTag(ctx, prefix)
			if tag == nil {
				if err != nil {
					return "", err
				}
				if readErr != nil {
					return "", readErr
				}
				return "", &transporterr.Upstream{Message: "The library did not confirm the new requester tag.", Transient: true}
			}
		}
	}
	title, err := readTitle()
	if err != nil {
		return "", err
	}
	if !slices.Contains(title.Tags, tag.ID) {
		if err := add(tag.ID); err != nil {
			return "", err
		}
		title, err = readTitle()
		if err != nil {
			return "", err
		}
		if !slices.Contains(title.Tags, tag.ID) {
			return "", &transporterr.Upstream{Message: "The library has not confirmed the requester tag on the title.", Transient: true}
		}
	}
	if err := beforeWrite(); err != nil {
		return "", err
	}
	return tag.Label, nil
}

// ApplyParent tags the verified artist/author behind a delivered album/book.
// Chaptarr stores tags per author format; Lidarr stores them on the artist.
func (c *RequesterTags) ApplyParent(ctx context.Context, id int, foreignID, format, prefix, label string, beforeWrite func() error) (string, error) {
	if id <= 0 || foreignID == "" || (c.collection != "artist" && c.collection != "author") || (c.collection == "author" && format != "ebook" && format != "audiobook") {
		return "", ErrTagIdentity
	}
	path := fmt.Sprintf("%s/%s/%d", c.apiPath, c.collection, id)
	read := func() (map[string]json.RawMessage, *taggedTitle, error) {
		var raw map[string]json.RawMessage
		if err := c.do(ctx, http.MethodGet, path, nil, &raw, nil); err != nil {
			return nil, nil, err
		}
		var gotID int
		var gotForeign string
		if json.Unmarshal(raw["id"], &gotID) != nil || json.Unmarshal(raw[c.identityKey], &gotForeign) != nil || gotID != id || gotForeign != foreignID {
			return nil, nil, ErrTagIdentity
		}
		key := "tags"
		if c.collection == "author" {
			key = format + "Tags"
		}
		var tags []int
		// A missing format field is not proof of support on an older Chaptarr.
		if json.Unmarshal(raw[key], &tags) != nil {
			return nil, nil, errors.New("The library does not expose the required requester tag field.")
		}
		return raw, &taggedTitle{ID: id, Tags: tags}, nil
	}
	readTitle := func() (*taggedTitle, error) {
		_, title, err := read()
		return title, err
	}
	add := func(tagID int) error {
		if c.collection == "artist" {
			return c.do(ctx, http.MethodPut, c.apiPath+"/artist/editor", map[string]any{"artistIds": []int{id}, "tags": []int{tagID}, "applyTags": "add"}, nil, beforeWrite)
		}
		// Chaptarr 0.9.911's bulk editor changes only legacy Tags, while its
		// reader displays the format arrays. Use the individual editor with a
		// fresh union and only fields its mapper/validator requires preserving.
		// Omit format monitoring entirely: echoing it marks inherited settings
		// manually overridden even when the value has not changed.
		if beforeWrite == nil {
			return errors.New("Requester tag authorization is missing.")
		}
		if err := beforeWrite(); err != nil {
			return err
		}
		raw, title, err := read()
		if err != nil {
			return err
		}
		if slices.Contains(title.Tags, tagID) {
			return nil
		}
		body := map[string]json.RawMessage{}
		for _, key := range []string{"id", "path", "monitored", "audiobookRootFolderPath", "ebookRootFolderPath", "addOptions", "lastSelectedMediaType", "ebookQualityProfileId", "audiobookQualityProfileId"} {
			if value, ok := raw[key]; ok {
				body[key] = value
			}
		}
		body[format+"Tags"], _ = json.Marshal(append(title.Tags, tagID))
		return c.do(ctx, http.MethodPut, path, body, nil, beforeWrite)
	}
	return c.apply(ctx, prefix, label, readTitle, add, beforeWrite)
}
