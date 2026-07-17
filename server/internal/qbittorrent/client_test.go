package qbittorrent

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// qbitAuthFake models the qBittorrent WebUI cookie-auth flow: POST
// /api/v2/auth/login issues an SID cookie, every other endpoint requires the
// current cookie and returns 403 otherwise.
type qbitAuthFake struct {
	username string
	password string

	mu     sync.Mutex
	sid    string
	logins int
}

func (f *qbitAuthFake) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			if r.Header.Get("Referer") == "" {
				t.Error("login request missing Referer header")
			}
			_ = r.ParseForm()
			if r.PostForm.Get("username") != f.username || r.PostForm.Get("password") != f.password {
				_, _ = io.WriteString(w, "Fails.")
				return
			}
			f.mu.Lock()
			f.logins++
			f.sid = fmt.Sprintf("sid-%d", f.logins)
			sid := f.sid
			f.mu.Unlock()
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: sid})
			_, _ = io.WriteString(w, "Ok.")
			return
		}

		cookie, err := r.Cookie("SID")
		f.mu.Lock()
		valid := err == nil && cookie.Value == f.sid
		f.mu.Unlock()
		if !valid {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `[{"name":"t1","hash":"aaa","size":10,"progress":0.5,"state":"downloading"}]`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func (f *qbitAuthFake) loginCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logins
}

// expireSession invalidates the issued cookie, simulating a WebUI session
// expiring server-side.
func (f *qbitAuthFake) expireSession() {
	f.mu.Lock()
	f.sid = "expired"
	f.mu.Unlock()
}

// TestLoginOnceAndReuseCookie pins the login flow: the first API call logs in
// and stores the SID cookie; subsequent calls reuse it without re-logging-in.
func TestLoginOnceAndReuseCookie(t *testing.T) {
	fake := &qbitAuthFake{username: "admin", password: "adminadmin"}
	srv := httptest.NewServer(fake.handler(t))
	t.Cleanup(srv.Close)

	client := NewClient(srv.URL, "admin", "adminadmin")
	for i := 0; i < 3; i++ {
		torrents, err := client.GetTorrents()
		if err != nil {
			t.Fatalf("GetTorrents call %d: %v", i, err)
		}
		if len(torrents) != 1 || torrents[0].Hash != "aaa" {
			t.Fatalf("torrents = %+v, want the fake torrent", torrents)
		}
	}
	if got := fake.loginCount(); got != 1 {
		t.Fatalf("logins = %d, want exactly 1 across repeated calls", got)
	}
}

// TestExpiredSessionTriggersReloginRetry pins the 403 recovery path: when the
// stored cookie has expired, the client re-logs-in once and retries the call
// instead of surfacing the 403.
func TestExpiredSessionTriggersReloginRetry(t *testing.T) {
	fake := &qbitAuthFake{username: "admin", password: "adminadmin"}
	srv := httptest.NewServer(fake.handler(t))
	t.Cleanup(srv.Close)

	client := NewClient(srv.URL, "admin", "adminadmin")
	if _, err := client.GetTorrents(); err != nil {
		t.Fatalf("initial GetTorrents: %v", err)
	}

	fake.expireSession()
	if _, err := client.GetTorrents(); err != nil {
		t.Fatalf("GetTorrents after session expiry: %v", err)
	}
	if got := fake.loginCount(); got != 2 {
		t.Fatalf("logins = %d, want 2 (initial + re-login after 403)", got)
	}
}

// TestLoginFailuresDoNotEchoCredentials pins both rejection shapes (the
// "Fails." body and a non-200 status) and the credential-echo property:
// error strings never contain the username or password.
func TestLoginFailuresDoNotEchoCredentials(t *testing.T) {
	const password = "QBIT_PASSWORD_SENTINEL"

	badCreds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "Fails.")
	}))
	t.Cleanup(badCreds.Close)
	err := NewClient(badCreds.URL, "admin", password).Login()
	if err == nil {
		t.Fatal("Login accepted a Fails. response")
	}
	if !strings.Contains(err.Error(), "invalid credentials") {
		t.Errorf("error = %v, want invalid-credentials message", err)
	}
	if strings.Contains(err.Error(), password) || strings.Contains(err.Error(), "admin") {
		t.Fatalf("login error echoed credentials: %v", err)
	}

	serverError := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "banned: too many attempts for "+password, http.StatusForbidden)
	}))
	t.Cleanup(serverError.Close)
	err = NewClient(serverError.URL, "admin", password).Login()
	if err == nil {
		t.Fatal("Login accepted a 403 response")
	}
	if strings.Contains(err.Error(), password) {
		t.Fatalf("login error echoed the upstream body secret: %v", err)
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

	client := NewClient(source.URL, "admin", "qbit-secret")
	if err := client.Login(); err == nil {
		t.Fatal("Login accepted an upstream redirect")
	}
	if _, err := client.GetTorrents(); err == nil {
		t.Fatal("GetTorrents accepted an upstream redirect")
	}
	if got := redirectedRequests.Load(); got != 0 {
		t.Fatalf("redirect destination received %d requests, want 0", got)
	}
}

// TestPauseResumeFallbackOn404 pins the 4.x/5.x rename shim: stop/start are
// tried first and pause/resume are used only when the modern path 404s.
func TestPauseResumeFallbackOn404(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid-1"})
			_, _ = io.WriteString(w, "Ok.")
			return
		}
		_ = r.ParseForm()
		if r.PostForm.Get("hashes") != "all" {
			t.Errorf("%s hashes = %q, want all", r.URL.Path, r.PostForm.Get("hashes"))
		}
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/api/v2/torrents/stop", "/api/v2/torrents/start":
			w.WriteHeader(http.StatusNotFound) // pre-5.x server
		case "/api/v2/torrents/pause", "/api/v2/torrents/resume":
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	client := NewClient(srv.URL, "admin", "adminadmin")
	if err := client.PauseTorrents("all"); err != nil {
		t.Fatalf("PauseTorrents: %v", err)
	}
	if err := client.ResumeTorrents("all"); err != nil {
		t.Fatalf("ResumeTorrents: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{
		"/api/v2/torrents/stop", "/api/v2/torrents/pause",
		"/api/v2/torrents/start", "/api/v2/torrents/resume",
	}
	if len(paths) != len(want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("paths = %v, want %v (modern endpoint first, legacy fallback second)", paths, want)
		}
	}
}
