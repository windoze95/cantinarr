package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/lidarr"
)

// The album timeline is receipts: track files with import dates joined to
// grab/import history with download identities — and honest absence lines
// when either side is empty.
func TestGetAlbumTimelineRendersReceipts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/album/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 9, "title": "The Wrong Record", "artist": map[string]any{"artistName": "The Artist"}})
		case r.URL.Path == "/api/v1/trackfile":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 71, "albumId": 9, "path": "/music/track01.flac", "size": 30000000, "dateAdded": "2026-07-20T10:00:00Z"},
			})
		case r.URL.Path == "/api/v1/history" && strings.Contains(r.URL.RawQuery, "eventType=1"):
			_ = json.NewEncoder(w).Encode(map[string]any{"totalRecords": 1, "records": []map[string]any{
				{"id": 501, "eventType": "grabbed", "albumId": 9, "sourceTitle": "Record.FLAC", "downloadId": "NZB-R", "date": "2026-07-20T09:00:00Z"},
			}})
		case r.URL.Path == "/api/v1/history":
			_ = json.NewEncoder(w).Encode(map[string]any{"totalRecords": 1, "records": []map[string]any{
				{"id": 502, "eventType": "trackFileImported", "albumId": 9, "sourceTitle": "Record.FLAC", "downloadId": "NZB-R", "date": "2026-07-20T10:00:00Z"},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ts := NewToolServer(nil, nil, nil, nil)
	res, err := ts.getAlbumTimelineWithClient(lidarr.NewClient(srv.URL, "k"), json.RawMessage(`{"media_type":"music","album_id":9}`))
	if err != nil {
		t.Fatalf("getAlbumTimeline: %v", err)
	}
	for _, want := range []string{"The Wrong Record by The Artist", "/music/track01.flac", "imported 2026-07-20", "grabbed", "trackFileImported", "download=NZB-R"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("timeline missing %q:\n%s", want, res.Text)
		}
	}
}

// A one-sided history failure must render as blindness on that side, never as
// a shorter complete list — the same rule the book timeline learned the hard
// way.
func TestGetAlbumTimelineLabelsOneSidedHistoryBlindness(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/album/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 9, "title": "The Wrong Record"})
		case r.URL.Path == "/api/v1/trackfile":
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		case r.URL.Path == "/api/v1/history" && strings.Contains(r.URL.RawQuery, "eventType=1"):
			_ = json.NewEncoder(w).Encode(map[string]any{"totalRecords": 1, "records": []map[string]any{
				{"id": 501, "eventType": "grabbed", "albumId": 9, "sourceTitle": "Record.FLAC", "downloadId": "NZB-R", "date": "2026-07-20T09:00:00Z"},
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
	res, err := ts.getAlbumTimelineWithClient(lidarr.NewClient(srv.URL, "k"), json.RawMessage(`{"media_type":"music","album_id":9}`))
	if err != nil {
		t.Fatalf("getAlbumTimeline: %v", err)
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
	// The empty file list is genuine absence, stated as such.
	if !strings.Contains(res.Text, "genuine absence") {
		t.Fatalf("empty file list not labelled as absence:\n%s", res.Text)
	}
}
