package sabnzbd

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func mustParseQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	q, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("parse query %q: %v", raw, err)
	}
	return q
}

// TestCallSendsAPIKeyQueryParam pins SABnzbd's auth dialect: every call is a
// GET against /api carrying output=json, the apikey query parameter, and the
// mode selector.
func TestCallSendsAPIKeyQueryParam(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"4.3.2"}`))
	}))
	t.Cleanup(srv.Close)

	version, err := NewClient(srv.URL, "sab-api-key").Version()
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version != "4.3.2" {
		t.Errorf("version = %q, want 4.3.2", version)
	}
	if gotPath != "/api" {
		t.Errorf("path = %s, want /api", gotPath)
	}
	q := mustParseQuery(t, gotQuery)
	if q.Get("output") != "json" || q.Get("apikey") != "sab-api-key" || q.Get("mode") != "version" {
		t.Errorf("query = %s, want output=json apikey=sab-api-key mode=version", gotQuery)
	}
}

// TestTransportErrorRedactsAPIKey pins the credential-echo property: SABnzbd
// is the one client whose secret rides in the request URL, and Go transport
// errors embed the full URL — the client must redact the key before the error
// can reach logs or HTTP responses.
func TestTransportErrorRedactsAPIKey(t *testing.T) {
	const secret = "SABNZBD_APIKEY_SENTINEL"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // unreachable: the transport error carries the request URL

	_, err := NewClient(srv.URL, secret).Version()
	if err == nil {
		t.Fatal("Version against a dead server returned nil error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("transport error echoed the API key: %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("transport error did not mark the redaction: %v", err)
	}
}

// TestErrorEnvelopeSurfacedAsError pins SABnzbd's failure dialect: errors are
// reported with HTTP 200 and a {"status": false, "error": ...} envelope.
func TestErrorEnvelopeSurfacedAsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status": false, "error": "API Key Incorrect"}`))
	}))
	t.Cleanup(srv.Close)

	if _, err := NewClient(srv.URL, "key").GetQueue(); err == nil {
		t.Fatal("GetQueue accepted an error envelope")
	} else if !strings.Contains(err.Error(), "API Key Incorrect") {
		t.Fatalf("error = %v, want the SABnzbd error message surfaced", err)
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

	client := NewClient(source.URL, "sabnzbd-secret")
	if _, err := client.GetQueue(); err == nil {
		t.Fatal("GetQueue accepted an upstream redirect")
	}
	if err := client.PauseQueue(); err == nil {
		t.Fatal("PauseQueue accepted an upstream redirect")
	}
	if got := redirectedRequests.Load(); got != 0 {
		t.Fatalf("redirect destination received %d requests, want 0", got)
	}
}

// TestQueueSlotDerivedFields pins the string-typed numeric parsing SABnzbd
// forces on clients, in particular the "[dd:]hh:mm:ss" timeleft format.
func TestQueueSlotDerivedFields(t *testing.T) {
	etaCases := []struct {
		timeLeft string
		want     int64
	}{
		{"0:07:30", 450},
		{"12:03", 723},
		{"1:02:03:04", 93784}, // dd:hh:mm:ss
		{"", 0},
		{"junk", 0},
	}
	for _, tc := range etaCases {
		slot := QueueSlot{TimeLeft: tc.timeLeft}
		if got := slot.ETASeconds(); got != tc.want {
			t.Errorf("ETASeconds(%q) = %d, want %d", tc.timeLeft, got, tc.want)
		}
	}

	slot := QueueSlot{MB: "1400.00", MBLeft: "350.00", Percentage: "75"}
	if got := slot.SizeBytes(); got != 1400*1024*1024 {
		t.Errorf("SizeBytes = %d, want %d", got, int64(1400*1024*1024))
	}
	if got := slot.SizeLeftBytes(); got != 350*1024*1024 {
		t.Errorf("SizeLeftBytes = %d, want %d", got, int64(350*1024*1024))
	}
	if got := slot.Progress(); got != 75 {
		t.Errorf("Progress = %v, want 75", got)
	}

	queue := Queue{KBPerSec: "2048.00"}
	if got := queue.SpeedBPS(); got != 2048*1024 {
		t.Errorf("SpeedBPS = %d, want %d", got, int64(2048*1024))
	}
}
