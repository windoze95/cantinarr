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

// RequesterTags uses the native tag and additive editor APIs. It never sends a
// whole movie/series record back, which would overwrite concurrent admin edits.
type RequesterTags struct {
	client                  *http.Client
	baseURL, apiKey         string
	collection, identityKey string
}

func NewRequesterTags(client *http.Client, baseURL, apiKey, collection, identityKey string) *RequesterTags {
	return &RequesterTags{client: client, baseURL: baseURL, apiKey: apiKey, collection: collection, identityKey: identityKey}
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
	if err := c.do(ctx, http.MethodGet, "/api/v3/tag", nil, &tags, nil); err != nil {
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
	tag, err := c.findTag(ctx, prefix)
	if err != nil {
		return "", err
	}
	if tag == nil {
		var created nativeTag
		err = c.do(ctx, http.MethodPost, "/api/v3/tag", map[string]string{"label": label}, &created, beforeWrite)
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
	title, err := readTitle()
	if err != nil {
		return "", err
	}
	if !slices.Contains(title.Tags, tag.ID) {
		body := map[string]any{c.collection + "Ids": []int{title.ID}, "tags": []int{tag.ID}, "applyTags": "add"}
		if err := c.do(ctx, http.MethodPut, path+"/editor", body, nil, beforeWrite); err != nil {
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
