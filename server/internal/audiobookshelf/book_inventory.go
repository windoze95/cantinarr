package audiobookshelf

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
)

const (
	bookInventoryPageSize = 200
	bookInventoryMaxItems = 100000
)

// BookInventoryItem is the identity and media-presence snapshot used by the
// library-separation migration. Title is intentionally not included: records
// are identified only by Audiobookshelf's item ID.
type BookInventoryItem struct {
	ID            string
	LibraryID     string
	ASIN          string
	ISBN          string
	Tags          []string
	HasAudio      bool
	HasEbook      bool
	HasMediaFiles bool
}

// BookInventory returns a complete, bounded inventory of one book library.
// Any malformed, inconsistent, duplicate, or truncated page fails the whole
// operation; callers must never apply migration tags to a partial snapshot.
func (c *Client) BookInventory(ctx context.Context, libraryID string) ([]BookInventoryItem, error) {
	if !validID.MatchString(libraryID) {
		return nil, errors.New("audiobookshelf inventory library ID is invalid")
	}
	items := make([]BookInventoryItem, 0)
	seen := make(map[string]struct{})
	var expectedTotal int
	for page := 0; ; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if page >= (bookInventoryMaxItems+bookInventoryPageSize-1)/bookInventoryPageSize {
			return nil, errors.New("audiobookshelf inventory exceeded its page cap")
		}
		var response struct {
			Results []inventoryWireItem `json:"results"`
			Total   *int                `json:"total"`
			Limit   *int                `json:"limit"`
			Page    *int                `json:"page"`
		}
		query := url.Values{"limit": {strconv.Itoa(bookInventoryPageSize)}, "page": {strconv.Itoa(page)}}
		path := "/api/libraries/" + url.PathEscape(libraryID) + "/items?" + query.Encode()
		if err := c.do(ctx, "GET", path, nil, &response); err != nil {
			return nil, err
		}
		if response.Results == nil || response.Total == nil || response.Limit == nil || response.Page == nil ||
			*response.Page != page || *response.Limit != bookInventoryPageSize || *response.Total < 0 ||
			*response.Total > bookInventoryMaxItems || len(response.Results) > bookInventoryPageSize {
			return nil, errors.New("audiobookshelf returned incomplete inventory pagination")
		}
		if page == 0 {
			expectedTotal = *response.Total
			items = make([]BookInventoryItem, 0, expectedTotal)
		} else if *response.Total != expectedTotal {
			return nil, errors.New("audiobookshelf inventory total changed during pagination")
		}
		for _, wire := range response.Results {
			item, err := wire.inventoryItem(libraryID)
			if err != nil {
				return nil, err
			}
			if _, exists := seen[item.ID]; exists {
				return nil, errors.New("audiobookshelf returned a duplicate inventory item")
			}
			seen[item.ID] = struct{}{}
			items = append(items, item)
		}
		if len(items) == expectedTotal {
			return items, nil
		}
		if len(response.Results) == 0 || len(items) > expectedTotal {
			return nil, errors.New("audiobookshelf inventory was truncated")
		}
	}
}

type inventoryWireItem struct {
	ID        string `json:"id"`
	LibraryID string `json:"libraryId"`
	MediaType string `json:"mediaType"`
	Media     struct {
		Tags   []string `json:"tags"`
		Tracks []struct {
			Duration float64 `json:"duration"`
		} `json:"tracks"`
		AudioFiles []struct {
			Exclude bool `json:"exclude"`
		} `json:"audioFiles"`
		EbookFile *struct {
			Metadata *struct {
				Ext string `json:"ext"`
			} `json:"metadata"`
		} `json:"ebookFile"`
		EbookFormat string `json:"ebookFormat"`
		Metadata    *struct {
			ASIN string `json:"asin"`
			ISBN string `json:"isbn"`
		} `json:"metadata"`
	} `json:"media"`
}

func (w inventoryWireItem) inventoryItem(libraryID string) (BookInventoryItem, error) {
	if !validID.MatchString(w.ID) || w.LibraryID != libraryID || w.MediaType != "book" || w.Media.Metadata == nil {
		return BookInventoryItem{}, fmt.Errorf("audiobookshelf returned an invalid inventory item")
	}
	if w.Media.Tags == nil {
		return BookInventoryItem{}, errors.New("audiobookshelf returned incomplete inventory tags")
	}
	tags := slices.Clone(w.Media.Tags)
	for _, tag := range tags {
		if tag == "" {
			return BookInventoryItem{}, errors.New("audiobookshelf returned an invalid inventory tag")
		}
	}
	hasAudio := false
	for _, track := range w.Media.Tracks {
		if track.Duration > 0 {
			hasAudio = true
		}
	}
	for _, file := range w.Media.AudioFiles {
		if !file.Exclude {
			hasAudio = true
		}
	}
	hasEbook := w.Media.EbookFile != nil || w.Media.EbookFormat != ""
	return BookInventoryItem{
		ID: w.ID, LibraryID: w.LibraryID, ASIN: w.Media.Metadata.ASIN,
		ISBN: w.Media.Metadata.ISBN, Tags: tags, HasAudio: hasAudio,
		HasEbook: hasEbook, HasMediaFiles: hasAudio || hasEbook,
	}, nil
}
