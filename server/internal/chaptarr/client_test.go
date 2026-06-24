package chaptarr

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestSetBookMonitored asserts SetBookMonitored posts the {bookIds, monitored}
// body Chaptarr's book/monitor endpoint expects.
func TestSetBookMonitored(t *testing.T) {
	var gotPath, gotMethod string
	var body map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "key")
	if err := c.SetBookMonitored([]int{7, 9, 11}, true); err != nil {
		t.Fatalf("SetBookMonitored: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/api/v1/book/monitor" {
		t.Errorf("path = %s, want /api/v1/book/monitor", gotPath)
	}
	if body["monitored"] != true {
		t.Errorf("monitored = %v, want true", body["monitored"])
	}
	ids, ok := body["bookIds"].([]any)
	if !ok || len(ids) != 3 {
		t.Fatalf("bookIds = %v, want 3 entries", body["bookIds"])
	}
	want := []int{7, 9, 11}
	for i, v := range ids {
		if int(v.(float64)) != want[i] {
			t.Errorf("bookIds[%d] = %v, want %d", i, v, want[i])
		}
	}
}

// TestExecuteManualImport asserts ExecuteManualImport posts a ManualImport
// command with a lowercase importMode and a files array whose bookId is set and
// whose Quality is preserved verbatim.
func TestExecuteManualImport(t *testing.T) {
	var gotPath string
	var body map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	quality := json.RawMessage(`{"quality":{"id":3,"name":"EPUB"},"revision":{"version":1}}`)
	files := []ManualImportFile{{
		Path:     "/downloads/book.epub",
		AuthorID: 4,
		BookID:   42,
		Quality:  quality,
	}}

	c := NewClient(srv.URL, "key")
	if err := c.ExecuteManualImport(files); err != nil {
		t.Fatalf("ExecuteManualImport: %v", err)
	}

	if gotPath != "/api/v1/command" {
		t.Errorf("path = %s, want /api/v1/command", gotPath)
	}
	if body["name"] != "ManualImport" {
		t.Errorf("name = %v, want ManualImport", body["name"])
	}
	if body["importMode"] != "auto" {
		t.Errorf("importMode = %v, want lowercase \"auto\"", body["importMode"])
	}

	gotFiles, ok := body["files"].([]any)
	if !ok || len(gotFiles) != 1 {
		t.Fatalf("files = %v, want a single-element array", body["files"])
	}
	file0 := gotFiles[0].(map[string]any)
	if int(file0["bookId"].(float64)) != 42 {
		t.Errorf("files[0].bookId = %v, want 42", file0["bookId"])
	}
	if int(file0["authorId"].(float64)) != 4 {
		t.Errorf("files[0].authorId = %v, want 4", file0["authorId"])
	}
	// Quality must round-trip verbatim: the nested name survives re-marshaling.
	q, ok := file0["quality"].(map[string]any)
	if !ok {
		t.Fatalf("files[0].quality = %v, want an object", file0["quality"])
	}
	inner, ok := q["quality"].(map[string]any)
	if !ok || inner["name"] != "EPUB" {
		t.Errorf("files[0].quality.quality.name = %v, want EPUB", q["quality"])
	}
}

// TestGetQueue asserts GetQueue hits the queue endpoint with includeAuthor=true
// and decodes the records array.
func TestGetQueue(t *testing.T) {
	var gotPath string
	var gotQuery url.Values

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"records":[{"id":1,"bookId":42,"title":"Some Book"}]}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "key")
	items, err := c.GetQueue()
	if err != nil {
		t.Fatalf("GetQueue: %v", err)
	}

	if gotPath != "/api/v1/queue" {
		t.Errorf("path = %s, want /api/v1/queue", gotPath)
	}
	if gotQuery.Get("includeAuthor") != "true" {
		t.Errorf("includeAuthor = %q, want true (query %v)", gotQuery.Get("includeAuthor"), gotQuery)
	}
	if len(items) != 1 || items[0].BookID != 42 {
		t.Fatalf("items = %+v, want one item with bookId 42", items)
	}
}

// TestFormatOf asserts the quality-name classifier maps representative ebook
// and audiobook formats and falls back to "unknown".
func TestFormatOf(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"EPUB", "ebook"},
		{"epub", "ebook"},
		{"AZW3", "ebook"},
		{"PDF", "ebook"},
		{"MP3-320", "audiobook"},
		{"M4B", "audiobook"},
		{"FLAC", "audiobook"},
		{"AudioBook", "audiobook"},
		{"Unknown Quality", "unknown"},
		{"", "unknown"},
	}
	for _, tc := range cases {
		if got := FormatOf(tc.name); got != tc.want {
			t.Errorf("FormatOf(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}
