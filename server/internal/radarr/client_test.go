package radarr

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestClientDoesNotFollowRedirects(t *testing.T) {
	var redirectedRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(destination.Close)

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/credential-sink", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)

	client := NewClient(source.URL, "radarr-secret")
	if _, err := client.GetMovies(); err == nil {
		t.Fatal("GetMovies accepted an upstream redirect")
	}
	if _, err := client.SearchReleases(42); err == nil {
		t.Fatal("SearchReleases accepted an upstream redirect")
	}
	if got := redirectedRequests.Load(); got != 0 {
		t.Fatalf("redirect destination received %d requests, want 0", got)
	}
}

func TestClientErrorDoesNotEchoUpstreamBody(t *testing.T) {
	const secret = "PROWLARR_DOWNLOAD_URL_API_KEY_SENTINEL"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"downloadUrl":"https://indexer.invalid/download?apikey=`+secret+`"}`, http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)

	_, err := NewClient(server.URL, "radarr-secret").GetMovies()
	if err == nil {
		t.Fatal("GetMovies returned nil error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error echoed upstream response secret: %v", err)
	}
}

func TestGetImportHistoryUsesExactBoundedFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("eventType") != "3" || q.Get("movieIds") != "42" || q.Get("downloadId") != "ABC/Case+ID" || q.Get("pageSize") != "20" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalRecords":1,"records":[{"id":7,"movieId":42,"downloadId":"ABC/Case+ID"}]}`))
	}))
	t.Cleanup(server.Close)
	records, err := NewClient(server.URL, "key").GetImportHistory(42, "ABC/Case+ID", 20)
	if err != nil || len(records) != 1 || records[0].MovieID != 42 {
		t.Fatalf("records=%+v err=%v", records, err)
	}
}

func TestGetImportHistoryRejectsTruncatedResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"totalRecords":21,"records":[]}`))
	}))
	t.Cleanup(server.Close)
	if _, err := NewClient(server.URL, "key").GetImportHistory(42, "id", 20); err == nil {
		t.Fatal("accepted incomplete filtered history")
	}
}

func TestGetQueueDetailedRejectsClampedSinglePage(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Query().Get("sortKey") != "id" || r.URL.Query().Get("sortDirection") != "ascending" {
			t.Errorf("queue snapshot is not stably sorted: %s", r.URL.RawQuery)
		}
		if r.URL.Query().Get("page") != "1" || r.URL.Query().Get("pageSize") != "1000" {
			t.Errorf("queue snapshot was not requested in one bounded page: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"totalRecords":2,"records":[{"id":1}]}`))
	}))
	t.Cleanup(server.Close)
	if _, err := NewClient(server.URL, "key").GetQueueDetailed(); err == nil {
		t.Fatal("accepted a truncated queue as a complete snapshot")
	}
	if requests != 1 {
		t.Fatalf("queue requests=%d, want one atomic bounded page", requests)
	}
}
