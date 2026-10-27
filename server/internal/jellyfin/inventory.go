package jellyfin

import (
	"context"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
	"net/http"
	"time"
)

func (c *Client) InventoryLibraries(ctx context.Context) ([]mediaserver.Library, error) {
	return c.Libraries(ctx)
}
func (c *Client) inventoryReader() mediaserver.JSONInventory {
	return mediaserver.JSONInventory{Read: func(ctx context.Context, path string, out any) error {
		return c.do(ctx, http.MethodGet, path, "read inventory", nil, out)
	}, Access: c.itemAccess, Libraries: c.InventoryLibraries}
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
