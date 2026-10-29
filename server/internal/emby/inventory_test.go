package emby

import (
	"context"
	"encoding/json"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestInventoryUsesNativeLibraryIDAndRecipientPolicy(t *testing.T) {
	var policies atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("X-Emby-Token") != "fixture" {
			t.Errorf("unexpected mutation/credential: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(400)
			return
		}
		switch r.URL.Path {
		case "/Library/MediaFolders":
			json.NewEncoder(w).Encode(map[string]any{"Items": []any{map[string]any{"Id": 5, "Guid": "sharing-guid", "Name": "Movies", "CollectionType": "movies"}}, "TotalRecordCount": 1})
		case "/Users/reader":
			policies.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"Id": "reader", "Name": "Reader", "Policy": map[string]any{"IsDisabled": false, "EnableAllFolders": false, "EnabledFolders": []string{"sharing-guid"}}})
		case "/Users/reader/Items":
			if r.URL.Query().Get("ParentId") != "5" || r.URL.Query().Get("Ids") != "movie" {
				t.Errorf("sharing ID or broad query used for catalog: %s", r.URL.RawQuery)
			}
			json.NewEncoder(w).Encode(map[string]any{"TotalRecordCount": 1, "Items": []any{map[string]any{"Id": "movie", "Type": "Movie", "ProviderIds": map[string]string{"Tmdb": "1"}, "MediaStreams": []any{map[string]any{"Type": "Video", "Width": 1920, "Height": 1080}}}}})
		default:
			t.Errorf("unscoped request: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "fixture")
	share, err := client.Libraries(context.Background())
	if err != nil || len(share) != 1 || share[0].ID != "sharing-guid" {
		t.Fatalf("sharing=%+v %v", share, err)
	}
	native, err := client.InventoryLibraries(context.Background())
	if err != nil || len(native) != 1 || native[0].ID != "5" {
		t.Fatalf("native=%+v %v", native, err)
	}
	items, err := client.VerifyInventory(context.Background(), []string{"5"}, "reader", mediaserver.ItemQuery{MediaType: "movie", TMDBID: 1}, []mediaserver.InventoryItem{{LibraryID: "5", ItemID: "movie"}})
	if err != nil || len(items) != 1 || !items[0].Movie || policies.Load() != 2 {
		t.Fatalf("items=%+v policies=%d err=%v", items, policies.Load(), err)
	}
}
