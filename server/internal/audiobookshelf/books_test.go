package audiobookshelf

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

func testBook(id string) map[string]any {
	return map[string]any{"id": id, "libraryId": "books", "mediaType": "book", "isMissing": false, "isInvalid": false,
		"path": "/private/library/path", "media": map[string]any{
			"metadata": map[string]any{"title": "A Book", "asin": "B012345678", "isbn": "9780306406157", "explicit": false, "narrators": []string{"A Narrator"}},
			"tags":     []string{"family"}, "tracks": []any{map[string]any{"duration": 10, "contentUrl": "/private/file?token=sentinel"}},
		}}
}

func TestFindBooksUsesExactIDsAndLivePermissions(t *testing.T) {
	tests := []struct {
		name   string
		change func(map[string]any, map[string]any)
		query  mediaserver.BookQuery
		found  bool
	}{
		{name: "ASIN", found: true},
		{name: "ISBN equivalent", query: mediaserver.BookQuery{Available: true, ISBNs: []string{"0-306-40615-2"}}, found: true},
		{name: "title alone never matches", change: func(u, b map[string]any) {
			m := b["media"].(map[string]any)["metadata"].(map[string]any)
			m["asin"] = "B999999999"
			m["isbn"] = ""
		}},
		{name: "ebook has no audio", change: func(u, b map[string]any) { b["media"].(map[string]any)["tracks"] = []any{} }},
		{name: "missing file", change: func(u, b map[string]any) { b["isMissing"] = true }},
		{name: "invalid file", change: func(u, b map[string]any) { b["isInvalid"] = true }},
		{name: "wrong library", change: func(u, b map[string]any) { b["libraryId"] = "private" }},
		{name: "no granted library", change: func(u, b map[string]any) { u["librariesAccessible"] = []string{} }},
		{name: "disabled", change: func(u, b map[string]any) { u["isActive"] = false }},
		{name: "explicit restricted", change: func(u, b map[string]any) { b["media"].(map[string]any)["metadata"].(map[string]any)["explicit"] = true }},
		{name: "explicit allowed", found: true, change: func(u, b map[string]any) {
			u["permissions"].(map[string]any)["accessExplicitContent"] = true
			b["media"].(map[string]any)["metadata"].(map[string]any)["explicit"] = true
		}},
		{name: "tag allow", found: true, change: func(u, b map[string]any) {
			u["permissions"].(map[string]any)["accessAllTags"] = false
			u["itemTagsSelected"] = []string{"family"}
		}},
		{name: "tag deny", change: func(u, b map[string]any) {
			p := u["permissions"].(map[string]any)
			p["accessAllTags"] = false
			p["selectedTagsNotAccessible"] = true
			u["itemTagsSelected"] = []string{"family"}
		}},
		{name: "empty tag allow", change: func(u, b map[string]any) { u["permissions"].(map[string]any)["accessAllTags"] = false }},
		{name: "untagged deny allowed", found: true, change: func(u, b map[string]any) {
			p := u["permissions"].(map[string]any)
			p["accessAllTags"] = false
			p["selectedTagsNotAccessible"] = true
			u["itemTagsSelected"] = []string{"blocked"}
			b["media"].(map[string]any)["tags"] = []string{}
		}},
		{name: "unknown permission", change: func(u, b map[string]any) { delete(u["permissions"].(map[string]any), "accessAllTags") }},
		{name: "unknown explicit metadata", change: func(u, b map[string]any) {
			delete(b["media"].(map[string]any)["metadata"].(map[string]any), "explicit")
		}},
		{name: "path traversal item", change: func(u, b map[string]any) { b["id"] = ".." }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, b := testUser(), testBook("copy-1")
			if tt.change != nil {
				tt.change(u, b)
			}
			q := tt.query
			if !q.Available {
				q = mediaserver.BookQuery{Available: true, ASINs: []string{"b012345678"}}
			}
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer admin-key" {
					t.Error("missing server key")
				}
				switch r.URL.Path {
				case "/api/users/user-1":
					sendJSON(w, u)
				case "/api/libraries":
					sendJSON(w, map[string]any{"libraries": []any{map[string]any{"id": "books", "name": "Shared books", "mediaType": "book"}, map[string]any{"id": "private", "name": "Private", "mediaType": "book"}}})
				case "/api/libraries/books/search":
					sendJSON(w, map[string]any{"book": []any{map[string]any{"libraryItem": b}}})
				case "/api/items/copy-1":
					sendJSON(w, b)
				default:
					t.Errorf("unapproved request: %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer s.Close()
			items, err := NewClient(s.URL, "admin-key").FindBooks(context.Background(), "user-1", q)
			if tt.found {
				if err != nil || len(items) != 1 {
					t.Fatalf("items=%+v error=%v", items, err)
				}
			} else if !errors.Is(err, mediaserver.ErrItemUnverified) || len(items) != 0 {
				t.Fatalf("unsafe match: %+v %v", items, err)
			}
			data, _ := json.Marshal(items)
			if strings.Contains(string(data), "private") || strings.Contains(string(data), "sentinel") || strings.Contains(string(data), "admin-key") {
				t.Fatal("private metadata exposed")
			}
		})
	}
}

func TestFindBooksKeepsDistinctCopiesAndRefusesIncompleteSearch(t *testing.T) {
	for _, scenario := range []string{"duplicates", "permission changed", "truncated", "missing envelope", "item changed", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			u, reads := testUser(), 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/users/user-1":
					reads++
					if scenario == "permission changed" && reads == 2 {
						u["librariesAccessible"] = []string{}
					}
					sendJSON(w, u)
				case r.URL.Path == "/api/libraries":
					sendJSON(w, map[string]any{"libraries": []any{map[string]any{"id": "books", "name": "Shared", "mediaType": "book"}}})
				case strings.HasSuffix(r.URL.Path, "/search"):
					if scenario == "timeout" {
						<-r.Context().Done()
						return
					}
					if scenario == "missing envelope" {
						sendJSON(w, map[string]any{})
						return
					}
					items := []any{map[string]any{"libraryItem": testBook("copy-1")}, map[string]any{"libraryItem": testBook("copy-2")}}
					if scenario == "truncated" {
						for len(items) < 100 {
							items = append(items, items[0])
						}
					}
					sendJSON(w, map[string]any{"book": items})
				case strings.HasPrefix(r.URL.Path, "/api/items/"):
					b := testBook(strings.TrimPrefix(r.URL.Path, "/api/items/"))
					if scenario == "item changed" {
						b["libraryId"] = "private"
					}
					sendJSON(w, b)
				default:
					w.WriteHeader(404)
				}
			}))
			defer s.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if scenario == "timeout" {
				var c context.CancelFunc
				ctx, c = context.WithTimeout(ctx, 20*time.Millisecond)
				defer c()
			}
			items, err := NewClient(s.URL, "admin-key").FindBooks(ctx, "user-1", mediaserver.BookQuery{Available: true, ASINs: []string{"B012345678"}, ISBNs: []string{"9780306406157"}})
			if scenario == "duplicates" {
				if err != nil || len(items) != 2 || items[0].ID == items[1].ID {
					t.Fatalf("copies lost: %+v %v", items, err)
				}
			} else if err == nil || len(items) != 0 {
				t.Fatalf("unverified result accepted: %+v", items)
			}
		})
	}
}
