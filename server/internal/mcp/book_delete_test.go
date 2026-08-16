package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

// The wrong-book repair at the arr boundary: delete the record's files, stand
// the delivering grabs down, and — with the failed-download policy ON — post
// no search of our own.
func TestDeleteBookFilesHelperFullRepair(t *testing.T) {
	var deletes, failed []string
	searchPosted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/bookfile" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 71, "bookId": 9, "path": "/books/wrong.epub", "size": 1000000},
			})
		case strings.HasPrefix(r.URL.Path, "/api/v1/bookfile/") && r.Method == http.MethodDelete:
			deletes = append(deletes, r.URL.Path)
			_ = json.NewEncoder(w).Encode(map[string]any{})
		case r.URL.Path == "/api/v1/history" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"totalRecords": 1, "records": []map[string]any{
				{"id": 501, "eventType": "grabbed", "bookId": 9, "downloadId": "NZB-BOOK"},
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

	client := chaptarr.NewClient(srv.URL, "key")
	text, err := DeleteBookFilesHelper(client, 9, true, time.Now().UTC())
	if err != nil {
		t.Fatalf("DeleteBookFilesHelper: %v", err)
	}
	if len(deletes) != 1 || deletes[0] != "/api/v1/bookfile/71" {
		t.Fatalf("file deletes = %v, want the record's one file", deletes)
	}
	if len(failed) != 1 || failed[0] != "/api/v1/history/failed/501" {
		t.Fatalf("failed grabs = %v, want the delivering grab", failed)
	}
	if searchPosted {
		t.Fatalf("a search was posted despite the service's own failed-download handling being on")
	}
	if !strings.Contains(text, "searches for the replacement itself") {
		t.Fatalf("standdown not narrated: %s", text)
	}
}
