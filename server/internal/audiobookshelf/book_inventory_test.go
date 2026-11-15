package audiobookshelf

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"testing"
)

func inventoryFixture(id string) map[string]any {
	return map[string]any{
		"id": id, "libraryId": "books", "mediaType": "book",
		"media": map[string]any{
			"metadata":   map[string]any{"asin": "B012345678", "isbn": "9780306406157"},
			"tags":       []string{"shared", "requester:alice"},
			"tracks":     []any{map[string]any{"duration": 12}},
			"audioFiles": []any{}, "ebookFile": map[string]any{"metadata": map[string]any{"ext": "epub"}},
		},
	}
}

func inventoryPage(w http.ResponseWriter, page, total int, results []any) {
	sendJSON(w, map[string]any{"results": results, "total": total, "limit": bookInventoryPageSize, "page": page})
}

func TestBookInventoryReadsEveryPageAndPreservesExactIdentityAndMedia(t *testing.T) {
	requests := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/api/libraries/books/items" || r.Method != http.MethodGet {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("limit") != strconv.Itoa(bookInventoryPageSize) {
			t.Fatalf("limit=%q", r.URL.Query().Get("limit"))
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		switch page {
		case 0:
			inventoryPage(w, 0, 2, []any{inventoryFixture("item-1")})
		case 1:
			second := inventoryFixture("item-2")
			media := second["media"].(map[string]any)
			media["tracks"] = []any{}
			media["audioFiles"] = []any{}
			media["ebookFile"] = nil
			media["ebookFormat"] = ""
			inventoryPage(w, 1, 2, []any{second})
		default:
			t.Fatalf("unexpected page %d", page)
		}
	}))
	defer s.Close()

	got, err := NewClient(s.URL, "admin-key").BookInventory(context.Background(), "books")
	if err != nil {
		t.Fatal(err)
	}
	want := []BookInventoryItem{
		{ID: "item-1", LibraryID: "books", ASIN: "B012345678", ISBN: "9780306406157", Tags: []string{"shared", "requester:alice"}, HasAudio: true, HasEbook: true, HasMediaFiles: true},
		{ID: "item-2", LibraryID: "books", ASIN: "B012345678", ISBN: "9780306406157", Tags: []string{"shared", "requester:alice"}},
	}
	if !reflect.DeepEqual(got, want) || requests != 2 {
		t.Fatalf("inventory=%+v requests=%d, want %+v and two requests", got, requests, want)
	}
}

func TestBookInventoryAcceptsEmptyLibrary(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inventoryPage(w, 0, 0, []any{})
	}))
	defer s.Close()
	got, err := NewClient(s.URL, "admin-key").BookInventory(context.Background(), "books")
	if err != nil || len(got) != 0 {
		t.Fatalf("inventory=%v error=%v", got, err)
	}
}

func TestBookInventoryFailsClosedOnMalformedOrTruncatedResponses(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
	}{
		{name: "missing pagination envelope", body: map[string]any{"results": []any{}}},
		{name: "wrong page", body: map[string]any{"results": []any{}, "total": 0, "limit": bookInventoryPageSize, "page": 1}},
		{name: "missing item metadata", body: map[string]any{"results": []any{map[string]any{"id": "item-1", "libraryId": "books", "mediaType": "book", "media": map[string]any{"tags": []string{}}}}, "total": 1, "limit": bookInventoryPageSize, "page": 0}},
		{name: "wrong library", body: map[string]any{"results": []any{map[string]any{"id": "item-1", "libraryId": "other", "mediaType": "book", "media": map[string]any{"tags": []string{}, "metadata": map[string]any{}}}}, "total": 1, "limit": bookInventoryPageSize, "page": 0}},
		{name: "truncated page", body: map[string]any{"results": []any{}, "total": 1, "limit": bookInventoryPageSize, "page": 0}},
		{name: "above hard item cap", body: map[string]any{"results": []any{}, "total": bookInventoryMaxItems + 1, "limit": bookInventoryPageSize, "page": 0}},
		{name: "maximum page count truncation", body: map[string]any{"results": []any{}, "total": bookInventoryMaxItems, "limit": bookInventoryPageSize, "page": 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/libraries/books/items" {
					t.Fatalf("unexpected path %s", r.URL.Path)
				}
				sendJSON(w, tc.body)
			}))
			defer s.Close()
			got, err := NewClient(s.URL, "admin-key").BookInventory(context.Background(), "books")
			if err == nil || got != nil {
				t.Fatalf("unsafe partial inventory: got=%v error=%v", got, err)
			}
		})
	}
}

func TestBookInventoryRejectsDuplicateIDsAndInvalidLibraryID(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inventoryPage(w, 0, 2, []any{inventoryFixture("same"), inventoryFixture("same")})
	}))
	defer s.Close()
	c := NewClient(s.URL, "admin-key")
	if got, err := c.BookInventory(context.Background(), "books"); err == nil || got != nil {
		t.Fatalf("duplicate inventory accepted: %v %v", got, err)
	}
	if got, err := c.BookInventory(context.Background(), url.PathEscape("../books")); err == nil || got != nil {
		t.Fatalf("invalid library ID accepted: %v %v", got, err)
	}
}
