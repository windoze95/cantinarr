package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

// The book timeline is receipts: files with import dates joined to grab/import
// history with download identities — and honest absence lines when either side
// is empty.
func TestGetBookTimelineRendersReceipts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/book/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 9, "title": "The Wrong Tome"})
		case r.URL.Path == "/api/v1/bookfile":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 71, "bookId": 9, "path": "/books/tome.epub", "size": 2000000, "dateAdded": "2026-07-20T10:00:00Z"},
			})
		case r.URL.Path == "/api/v1/history" && strings.Contains(r.URL.RawQuery, "eventType=1"):
			_ = json.NewEncoder(w).Encode(map[string]any{"totalRecords": 1, "records": []map[string]any{
				{"id": 501, "eventType": "grabbed", "bookId": 9, "sourceTitle": "Tome.Retail.EPUB", "downloadId": "NZB-T", "date": "2026-07-20T09:00:00Z"},
			}})
		case r.URL.Path == "/api/v1/history":
			_ = json.NewEncoder(w).Encode(map[string]any{"totalRecords": 1, "records": []map[string]any{
				{"id": 502, "eventType": "bookFileImported", "bookId": 9, "sourceTitle": "Tome.Retail.EPUB", "downloadId": "NZB-T", "date": "2026-07-20T10:00:00Z"},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	server := &ToolServer{}
	_ = server // direct client path: the tool resolves via GetChaptarrFor which needs a registry; test the renderer through a raw client call instead
	client := chaptarr.NewClient(srv.URL, "k")
	_ = client
	ts := NewToolServer(nil, nil, nil, nil)
	res, err := ts.getBookTimelineWithClient(client, json.RawMessage(`{"media_type":"book","book_id":9}`))
	if err != nil {
		t.Fatalf("getBookTimeline: %v", err)
	}
	for _, want := range []string{"The Wrong Tome", "/books/tome.epub", "imported 2026-07-20", "grabbed", "bookFileImported", "download=NZB-T"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("timeline missing %q:\n%s", want, res.Text)
		}
	}
}

// A one-sided history failure must render as blindness on that side, never as
// a shorter complete list. The import read errors BY DESIGN when the record
// holds more import events than one page can prove complete — a book with a
// long import history hit that on every call, and the timeline silently showed
// grabs alone under a "grabs + imports" heading.
func TestGetBookTimelineLabelsOneSidedHistoryBlindness(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/book/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 9, "title": "The Wrong Tome"})
		case r.URL.Path == "/api/v1/bookfile":
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		case r.URL.Path == "/api/v1/history" && strings.Contains(r.URL.RawQuery, "eventType=1"):
			_ = json.NewEncoder(w).Encode(map[string]any{"totalRecords": 1, "records": []map[string]any{
				{"id": 501, "eventType": "grabbed", "bookId": 9, "sourceTitle": "Tome.Retail.EPUB", "downloadId": "NZB-T", "date": "2026-07-20T09:00:00Z"},
			}})
		case r.URL.Path == "/api/v1/history":
			// More import records than the page can hold: the client refuses
			// this as incomplete rather than returning a partial page.
			_ = json.NewEncoder(w).Encode(map[string]any{"totalRecords": 99, "records": []map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ts := NewToolServer(nil, nil, nil, nil)
	res, err := ts.getBookTimelineWithClient(chaptarr.NewClient(srv.URL, "k"), json.RawMessage(`{"media_type":"book","book_id":9}`))
	if err != nil {
		t.Fatalf("getBookTimeline: %v", err)
	}
	if !strings.Contains(res.Text, "IMPORT history is UNREADABLE") {
		t.Fatalf("one-sided failure not labelled as blindness:\n%s", res.Text)
	}
	if strings.Contains(res.Text, "grabs + imports") {
		t.Fatalf("a grabs-only page claimed to be the full history:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "grabbed") {
		t.Fatalf("the readable grab half went missing:\n%s", res.Text)
	}
}
