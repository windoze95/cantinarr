package radarr

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type failingTransport struct{ err error }

func (f failingTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, f.err }

func TestGetMovieFileUsesExactAuthenticatedEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v3/moviefile/73" || r.URL.RawQuery != "" {
			t.Errorf("request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if got := r.Header.Get("X-Api-Key"); got != "movie-key" {
			t.Errorf("X-Api-Key = %q", got)
		}
		_, _ = w.Write([]byte(`{"id":73,"movieId":8,"relativePath":"Movie.mkv","path":"/library/Movie.mkv","size":123456}`))
	}))
	t.Cleanup(server.Close)

	file, err := NewClient(server.URL, "movie-key").GetMovieFile(73)
	if err != nil {
		t.Fatalf("GetMovieFile() error = %v", err)
	}
	if file.ID != 73 || file.MovieID != 8 || file.Path != "/library/Movie.mkv" || file.RelativePath != "Movie.mkv" || file.Size != 123456 {
		t.Fatalf("file = %#v", file)
	}
}

func TestGetMovieFileOmitsUpstreamErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`secret path /library/private and signed-token`))
	}))
	t.Cleanup(server.Close)

	_, err := NewClient(server.URL, "key").GetMovieFile(73)
	if err == nil {
		t.Fatal("GetMovieFile() error = nil")
	}
	if message := err.Error(); strings.Contains(message, "/library/private") || strings.Contains(message, "signed-token") {
		t.Fatalf("error leaked upstream body: %q", message)
	}
}

// TestTransportErrorOmitsHost pins the topology-privacy property: transport
// failures embed the full request URL (and DNS errors repeat the hostname),
// and these errors surface to requesters through request failures — so the
// client must summarize them host-free.
func TestTransportErrorOmitsHost(t *testing.T) {
	dnsFailure := &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: "radarr-internal"}}
	c := NewClient("http://radarr-internal:7878", "key")
	c.httpClient = &http.Client{Transport: failingTransport{dnsFailure}}

	if err := c.AddMovie(&AddMovieRequest{}); err == nil {
		t.Fatal("AddMovie succeeded against a failing transport")
	} else if msg := err.Error(); strings.Contains(msg, "radarr-internal") || strings.Contains(msg, "7878") {
		t.Errorf("AddMovie error %q names the host", msg)
	} else if !strings.Contains(msg, "could not resolve host") {
		t.Errorf("AddMovie error %q lacks the failure summary", msg)
	}

	if _, err := c.LookupByTMDB(603); err == nil {
		t.Fatal("LookupByTMDB succeeded against a failing transport")
	} else if msg := err.Error(); strings.Contains(msg, "radarr-internal") || strings.Contains(msg, "7878") {
		t.Errorf("LookupByTMDB error %q names the host", msg)
	} else if !strings.Contains(msg, "radarr GET /api/v3/movie/lookup") {
		t.Errorf("LookupByTMDB error %q does not identify the call", msg)
	}
}

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

// TestGetImportHistorySinceWindowsAndProvesCompleteness pins the catch-up
// reader's two jobs: only records dated after the cursor are returned, and
// completeness holds exactly when the page reached past the window boundary.
func TestGetImportHistorySinceWindowsAndProvesCompleteness(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("eventType") != "3" || q.Get("pageSize") != "3" || q.Get("sortDirection") != "descending" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalRecords":9,"records":[
			{"id":3,"movieId":30,"eventType":"downloadFolderImported","date":"2026-07-25T12:30:00Z"},
			{"id":2,"movieId":20,"eventType":"downloadFolderImported","date":"2026-07-25T12:10:00Z"},
			{"id":1,"movieId":10,"eventType":"downloadFolderImported","date":"2026-07-25T11:00:00Z"}]}`))
	}))
	t.Cleanup(server.Close)

	since := mustTime(t, "2026-07-25T12:00:00Z")
	records, complete, err := NewClient(server.URL, "key").GetImportHistorySince(since, 3)
	if err != nil {
		t.Fatalf("GetImportHistorySince() error = %v", err)
	}
	if !complete {
		t.Fatal("a page reaching past the cursor must prove the window complete")
	}
	if len(records) != 2 || records[0].MovieID != 30 || records[1].MovieID != 20 {
		t.Fatalf("in-window records = %+v, want movies 30 and 20", records)
	}
}

// TestGetImportHistorySinceReportsUnprovenWindow pins the overflow signal: a
// full page whose oldest record is still inside the window cannot claim it
// enumerated everything, and callers must not treat it as a smaller truth.
func TestGetImportHistorySinceReportsUnprovenWindow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalRecords":50,"records":[
			{"id":2,"movieId":20,"eventType":"downloadFolderImported","date":"2026-07-25T12:10:00Z"},
			{"id":1,"movieId":10,"eventType":"downloadFolderImported","date":"2026-07-25T12:05:00Z"}]}`))
	}))
	t.Cleanup(server.Close)

	since := mustTime(t, "2026-07-25T12:00:00Z")
	records, complete, err := NewClient(server.URL, "key").GetImportHistorySince(since, 2)
	if err != nil {
		t.Fatalf("GetImportHistorySince() error = %v", err)
	}
	if complete {
		t.Fatal("a full in-window page with more records upstream claimed completeness")
	}
	if len(records) != 2 {
		t.Fatalf("in-window records = %d, want 2", len(records))
	}
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return parsed
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

func TestDetailedQueueFileStateRequiresExactMovieIdentity(t *testing.T) {
	noFile := 0
	item := DetailedQueueItem{
		MovieID: 7,
		Movie:   &MovieContext{ID: 7, TmdbID: 42, MovieFileID: &noFile},
	}
	if got := item.FileIDAtSnapshot(); got == nil || *got != 0 {
		t.Fatalf("exact movie file ID = %v, want known absent", got)
	}
	item.Movie.ID = 8
	if got := item.FileIDAtSnapshot(); got != nil {
		t.Fatalf("mismatched movie identity produced file ID %v", *got)
	}
	item.Movie.ID = 7
	item.Movie.MovieFileID = nil
	if got := item.FileIDAtSnapshot(); got != nil {
		t.Fatalf("omitted movieFileId produced file ID %v", *got)
	}
}
