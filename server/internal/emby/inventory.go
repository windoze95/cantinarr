package emby

import (
	"context"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
	"net/http"
	"time"
)

// Catalog parent IDs are native item IDs. They deliberately differ from
// Libraries(), whose GUIDs belong to Emby's account-policy API.
func (c *Client) InventoryLibraries(ctx context.Context) ([]mediaserver.Library, error) {
	var resp mediaFoldersResponse
	if err := c.do(ctx, http.MethodGet, "/Library/MediaFolders", "list catalog libraries", nil, &resp); err != nil {
		return nil, err
	}
	libs := []mediaserver.Library{}
	for _, f := range resp.Items {
		if f.ID != "" {
			libs = append(libs, mediaserver.Library{ID: string(f.ID), Name: f.Name, CollectionType: f.CollectionType})
		}
	}
	return libs, nil
}
func (c *Client) inventoryReader() mediaserver.JSONInventory {
	return mediaserver.JSONInventory{Read: func(ctx context.Context, path string, out any) error {
		return c.do(ctx, http.MethodGet, path, "read inventory", nil, out)
	}, Access: c.itemAccess, Libraries: c.InventoryLibraries, UserPath: true, ExactProviderFilter: true}
}
func (c *Client) Inventory(ctx context.Context, libs []string, user string, q *mediaserver.ItemQuery) ([]mediaserver.InventoryItem, error) {
	return c.inventoryReader().Run(ctx, libs, user, q, nil, time.Time{}, false)
}
func (c *Client) VerifyInventory(ctx context.Context, libs []string, user string, q mediaserver.ItemQuery, candidates []mediaserver.InventoryItem) ([]mediaserver.InventoryItem, error) {
	return c.inventoryReader().Run(ctx, libs, user, &q, candidates, time.Time{}, false)
}
func (c *Client) InventorySince(ctx context.Context, libs []string, since time.Time) ([]mediaserver.InventoryItem, error) {
	return c.inventoryReader().Run(ctx, libs, "", nil, nil, since, false)
}

func (c *Client) InventoryCatalog(ctx context.Context, libs []string, user string) ([]mediaserver.InventoryItem, error) {
	return c.inventoryReader().Run(ctx, libs, user, nil, nil, time.Time{}, true)
}

var (
	_ mediaserver.InventoryProvider    = (*Client)(nil)
	_ mediaserver.InventoryVerifier    = (*Client)(nil)
	_ mediaserver.InventoryCatalog     = (*Client)(nil)
	_ mediaserver.IncrementalInventory = (*Client)(nil)
)
