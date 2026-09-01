package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/lidarr"
)

// The wrong-album repair at the arr boundary: delete the record's track files,
// stand the delivering grabs down, and — with the failed-download policy ON —
// post no search of our own. The book repair's exact shape over Lidarr's ids.
func TestDeleteTrackFilesHelperFullRepair(t *testing.T) {
	var deletes, failed []string
	searchPosted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/trackfile" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 71, "albumId": 9, "path": "/music/01-wrong.flac", "size": 1000000},
				{"id": 72, "albumId": 9, "path": "/music/02-wrong.flac", "size": 1000000},
			})
		case strings.HasPrefix(r.URL.Path, "/api/v1/trackfile/") && r.Method == http.MethodDelete:
			deletes = append(deletes, r.URL.Path)
			_ = json.NewEncoder(w).Encode(map[string]any{})
		case r.URL.Path == "/api/v1/history" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"totalRecords": 1, "records": []map[string]any{
				{"id": 501, "eventType": "grabbed", "albumId": 9, "downloadId": "NZB-ALBUM"},
			}})
		case strings.HasPrefix(r.URL.Path, "/api/v1/history/failed/") && r.Method == http.MethodPost:
			failed = append(failed, r.URL.Path)
			_ = json.NewEncoder(w).Encode(map[string]any{})
		case r.URL.Path == "/api/v1/config/downloadclient":
			_ = json.NewEncoder(w).Encode(map[string]any{"autoRedownloadFailed": true})
		case r.URL.Path == "/api/v1/command":
			searchPosted = true
			_ = json.NewEncoder(w).Encode(map[string]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := lidarr.NewClient(srv.URL, "key")
	text, err := DeleteTrackFilesHelper(client, 9, true, time.Now().UTC())
	if err != nil {
		t.Fatalf("DeleteTrackFilesHelper: %v", err)
	}
	if len(deletes) != 2 || deletes[0] != "/api/v1/trackfile/71" || deletes[1] != "/api/v1/trackfile/72" {
		t.Fatalf("file deletes = %v, want both of the album's files", deletes)
	}
	if len(failed) != 1 || failed[0] != "/api/v1/history/failed/501" {
		t.Fatalf("failed grabs = %v, want the delivering grab", failed)
	}
	if searchPosted {
		t.Fatal("a search was posted despite the service's own failed-download handling being on")
	}
	if !strings.Contains(text, "Deleted 2 track file(s)") || !strings.Contains(text, "Blocklisted 1 release(s)") {
		t.Fatalf("result = %q", text)
	}
}

// A file that arrived after the proposal is spared: the staleness gate keeps a
// just-imported replacement out of its own repair.
func TestDeleteTrackFilesHelperSparesFreshFiles(t *testing.T) {
	var deletes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/trackfile" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 71, "albumId": 9, "path": "/music/01-old.flac", "dateAdded": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)},
				{"id": 90, "albumId": 9, "path": "/music/01-fresh.flac", "dateAdded": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)},
			})
		case strings.HasPrefix(r.URL.Path, "/api/v1/trackfile/") && r.Method == http.MethodDelete:
			deletes = append(deletes, r.URL.Path)
			_ = json.NewEncoder(w).Encode(map[string]any{})
		case r.URL.Path == "/api/v1/command":
			_ = json.NewEncoder(w).Encode(map[string]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	text, err := DeleteTrackFilesHelper(lidarr.NewClient(srv.URL, "key"), 9, false, time.Now().UTC())
	if err != nil {
		t.Fatalf("DeleteTrackFilesHelper: %v", err)
	}
	if len(deletes) != 1 || deletes[0] != "/api/v1/trackfile/71" {
		t.Fatalf("deletes = %v, want only the pre-proposal file", deletes)
	}
	if !strings.Contains(text, "Skipped") || !strings.Contains(text, "arrived after this fix was proposed") {
		t.Fatalf("result = %q", text)
	}
}
