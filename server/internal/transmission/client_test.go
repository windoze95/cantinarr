package transmission

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type rpcRequest struct {
	Method    string                 `json:"method"`
	Arguments map[string]interface{} `json:"arguments"`
}

// TestSessionIDHandshake pins the X-Transmission-Session-Id CSRF flow: the
// first request 409s, the client adopts the header value and retries once,
// and later calls reuse the cached session id without another 409.
func TestSessionIDHandshake(t *testing.T) {
	const sessionID = "csrf-session-42"
	var conflicts atomic.Int32
	var mu sync.Mutex
	var methods []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/transmission/rpc" {
			t.Errorf("path = %s, want /transmission/rpc", r.URL.Path)
		}
		if r.Header.Get("X-Transmission-Session-Id") != sessionID {
			conflicts.Add(1)
			w.Header().Set("X-Transmission-Session-Id", sessionID)
			w.WriteHeader(http.StatusConflict)
			return
		}
		var req rpcRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		methods = append(methods, req.Method)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "session-get":
			_, _ = io.WriteString(w, `{"result":"success","arguments":{"version":"4.0.5","rpc-version":17}}`)
		case "session-stats":
			_, _ = io.WriteString(w, `{"result":"success","arguments":{"downloadSpeed":100,"uploadSpeed":5}}`)
		default:
			_, _ = io.WriteString(w, `{"result":"success"}`)
		}
	}))
	t.Cleanup(srv.Close)

	client := NewClient(srv.URL, "", "")
	session, err := client.SessionGet()
	if err != nil {
		t.Fatalf("SessionGet: %v", err)
	}
	if session.Version != "4.0.5" || session.RPCVersion != 17 {
		t.Errorf("session = %+v, want version 4.0.5 rpc 17", session)
	}
	stats, err := client.GetSessionStats()
	if err != nil {
		t.Fatalf("GetSessionStats: %v", err)
	}
	if stats.DownloadSpeed != 100 {
		t.Errorf("DownloadSpeed = %d, want 100", stats.DownloadSpeed)
	}

	if got := conflicts.Load(); got != 1 {
		t.Errorf("409 handshakes = %d, want exactly 1 (session id must be cached)", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(methods) != 2 || methods[0] != "session-get" || methods[1] != "session-stats" {
		t.Errorf("served methods = %v, want [session-get session-stats]", methods)
	}
}

// Test409WithoutSessionHeaderFails pins that a 409 with no session id header
// is an error rather than an infinite retry.
func Test409WithoutSessionHeaderFails(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusConflict)
	}))
	t.Cleanup(srv.Close)

	if _, err := NewClient(srv.URL, "", "").SessionGet(); err == nil {
		t.Fatal("SessionGet accepted a 409 without a session id header")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1 (no blind retry)", got)
	}
}

// TestUnauthorizedDoesNotEchoCredentials pins the credential-echo property on
// the 401 path.
func TestUnauthorizedDoesNotEchoCredentials(t *testing.T) {
	const password = "TRANSMISSION_PASSWORD_SENTINEL"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "401 rejected credentials "+password, http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	_, err := NewClient(srv.URL, "admin", password).GetTorrents()
	if err == nil {
		t.Fatal("GetTorrents accepted a 401 response")
	}
	if !strings.Contains(err.Error(), "invalid credentials") {
		t.Errorf("error = %v, want invalid-credentials message", err)
	}
	if strings.Contains(err.Error(), password) {
		t.Fatalf("error echoed the password: %v", err)
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

	client := NewClient(source.URL, "admin", "transmission-secret")
	if _, err := client.GetTorrents(); err == nil {
		t.Fatal("GetTorrents accepted an upstream redirect")
	}
	if _, err := client.SessionGet(); err == nil {
		t.Fatal("SessionGet accepted an upstream redirect")
	}
	if got := redirectedRequests.Load(); got != 0 {
		t.Fatalf("redirect destination received %d requests, want 0", got)
	}
}

// TestRemoveTorrentsGuardsEmptyList pins the destructive-op guard: an empty
// hash list must be rejected client-side, because a bare torrent-remove would
// remove every torrent Transmission knows.
func TestRemoveTorrentsGuardsEmptyList(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, `{"result":"success"}`)
	}))
	t.Cleanup(srv.Close)

	client := NewClient(srv.URL, "", "")
	if err := client.RemoveTorrents(nil, true); err == nil {
		t.Fatal("RemoveTorrents(nil) succeeded")
	}
	if err := client.RemoveTorrents([]string{}, false); err == nil {
		t.Fatal("RemoveTorrents(empty) succeeded")
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("requests = %d, want 0 (guard must fire before any RPC)", got)
	}
}

// TestRemoveTorrentsSendsIdsAndDeleteFlag pins the torrent-remove argument
// shape, including the delete-local-data flag.
func TestRemoveTorrentsSendsIdsAndDeleteFlag(t *testing.T) {
	var mu sync.Mutex
	var got rpcRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		_ = json.NewDecoder(r.Body).Decode(&got)
		mu.Unlock()
		_, _ = io.WriteString(w, `{"result":"success"}`)
	}))
	t.Cleanup(srv.Close)

	if err := NewClient(srv.URL, "", "").RemoveTorrents([]string{"hash-a", "hash-b"}, true); err != nil {
		t.Fatalf("RemoveTorrents: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if got.Method != "torrent-remove" {
		t.Errorf("method = %s, want torrent-remove", got.Method)
	}
	if got.Arguments["delete-local-data"] != true {
		t.Errorf("delete-local-data = %v, want true", got.Arguments["delete-local-data"])
	}
	ids, ok := got.Arguments["ids"].([]interface{})
	if !ok || len(ids) != 2 || ids[0] != "hash-a" || ids[1] != "hash-b" {
		t.Errorf("ids = %v, want [hash-a hash-b]", got.Arguments["ids"])
	}
}

// TestResultFailureSurfaced pins that a non-"success" result string fails the
// call even on HTTP 200.
func TestResultFailureSurfaced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"result":"unrecognized method"}`)
	}))
	t.Cleanup(srv.Close)

	if err := NewClient(srv.URL, "", "").StartTorrents([]string{"h"}); err == nil {
		t.Fatal("StartTorrents accepted a failure result")
	} else if !strings.Contains(err.Error(), "unrecognized method") {
		t.Fatalf("error = %v, want the result string surfaced", err)
	}
}

func TestStatusString(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{StatusStopped, "stopped"},
		{StatusCheckWaiting, "checkWaiting"},
		{StatusChecking, "checking"},
		{StatusDownloadQueue, "downloadWaiting"},
		{StatusDownloading, "downloading"},
		{StatusSeedQueue, "seedWaiting"},
		{StatusSeeding, "seeding"},
		{99, "unknown (99)"},
	}
	for _, tc := range cases {
		if got := StatusString(tc.status); got != tc.want {
			t.Errorf("StatusString(%d) = %q, want %q", tc.status, got, tc.want)
		}
	}
}
