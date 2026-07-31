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

// TestLoginDistinguishesWrongPageFromBadPassword pins that an HTML answer —
// some other WebUI on the entered port, not qBittorrent's plain "Ok."/"Fails."
// endpoint — is not misreported as invalid credentials.
func TestLoginDistinguishesWrongPageFromBadPassword(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "<html><title>Some other WebUI</title></html>")
	}))
	t.Cleanup(srv.Close)

	err := NewClient(srv.URL, "admin", "password").Login()
	if err == nil {
		t.Fatal("Login accepted an HTML response")
	}
	if strings.Contains(err.Error(), "invalid credentials") {
		t.Errorf("error %v misreports a non-qBittorrent page as bad credentials", err)
	}
	if !strings.Contains(err.Error(), "unexpected response") {
		t.Errorf("error %v lacks the wrong-page explanation", err)
	}
}

// TestLoginAcceptsBothWebUIGenerations pins the two login contracts we have to
// live with: qBittorrent 5.x answers a successful login with 204 No Content and
// an empty body (and 401 for bad credentials), while 4.x answers 200 "Ok."
// (and 200 "Fails."). Reading 204 as a failure locked every 5.x WebUI out.
func TestLoginAcceptsBothWebUIGenerations(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantErr    string // "" means the login must succeed
		setsCookie bool
	}{
		{name: "5.x success", status: http.StatusNoContent, body: "", setsCookie: true},
		{name: "5.x success behind auth bypass", status: http.StatusNoContent, body: ""},
		{name: "5.x bad credentials", status: http.StatusUnauthorized, body: "Unauthorized", wantErr: "invalid credentials"},
		{name: "4.x success", status: http.StatusOK, body: "Ok.", setsCookie: true},
		{name: "4.x bad credentials", status: http.StatusOK, body: "Fails.", wantErr: "invalid credentials"},
		{name: "empty 200 body", status: http.StatusOK, body: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.setsCookie {
					http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid-1"})
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			t.Cleanup(srv.Close)

			err := NewClient(srv.URL, "admin", "adminadmin").Login()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Login() = %v, want success for a %d response", err, tc.status)
				}
				return
			}
			if err == nil {
				t.Fatalf("Login() accepted a %d %q response", tc.status, tc.body)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

// TestLoginStillRejectsWrongPageOn204Path pins that accepting an empty body no
// longer being proof of failure did not also make an HTML page look like a
// success: a wrong port answering 200 with a page must still be reported as the
// wrong URL, not as a working WebUI.
func TestLoginStillRejectsWrongPageOn204Path(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "<!doctype html><html><body>login</body></html>")
	}))
	t.Cleanup(srv.Close)

	err := NewClient(srv.URL, "admin", "adminadmin").Login()
	if err == nil {
		t.Fatal("Login accepted an HTML 200 response")
	}
	if !strings.Contains(err.Error(), "unexpected response") {
		t.Errorf("error = %v, want the wrong-page explanation", err)
	}
}

// TestLoginRedirectNamesLocation pins that a refused login redirect reports
// where the WebUI tried to send us, so scheme misconfigurations are
// self-diagnosing.
func TestLoginRedirectNamesLocation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://qbittorrent.internal/", http.StatusMovedPermanently)
	}))
	t.Cleanup(srv.Close)

	err := NewClient(srv.URL, "admin", "password").Login()
	if err == nil || !strings.Contains(err.Error(), "https://qbittorrent.internal/") {
		t.Fatalf("redirect error = %v, want the Location named", err)
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
