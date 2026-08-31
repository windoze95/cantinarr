package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

const (
	testPassword     = "AlicePass!SENTINEL"
	testSessionToken = "session-token-SENTINEL"
)

// fakeSignIn is a Jellyfin that only knows how to sign a person in and out,
// and records exactly what each of those requests carried. Any other request
// is a test failure: a sign-in check has no business elsewhere.
type fakeSignIn struct {
	t            *testing.T
	status       int
	logoutStatus int
	echoPassword bool
	authHeader   http.Header
	authBody     map[string]any
	logoutHeader http.Header
	logins       atomic.Int32
	logouts      atomic.Int32
}

func (f *fakeSignIn) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/Users/AuthenticateByName", func(w http.ResponseWriter, r *http.Request) {
		f.logins.Add(1)
		f.authHeader = r.Header.Clone()
		body := map[string]any{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Errorf("decode sign-in body: %v", err)
		}
		f.authBody = body
		if f.status != 0 {
			w.WriteHeader(f.status)
			if f.echoPassword {
				pw, _ := body["Pw"].(string)
				_, _ = io.WriteString(w, "refused "+pw+" "+r.Host)
			}
			return
		}
		writeJSON(f.t, w, map[string]any{
			"User": map[string]any{
				"Id": "u-alice", "Name": "alice",
				"Policy": map[string]any{"IsAdministrator": true, "IsDisabled": false},
			},
			"AccessToken": testSessionToken,
			"ServerId":    "srv-1",
		})
	})
	mux.HandleFunc("/Sessions/Logout", func(w http.ResponseWriter, r *http.Request) {
		f.logouts.Add(1)
		f.logoutHeader = r.Header.Clone()
		if f.logoutStatus != 0 {
			w.WriteHeader(f.logoutStatus)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.t.Errorf("unexpected request %s %s during a sign-in check", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	})
	return mux
}

func TestAuthenticateSendsNoKeyThenLogsOut(t *testing.T) {
	f := &fakeSignIn{t: t}
	c := testClient(t, f.handler())

	user, err := c.Authenticate(context.Background(), "alice", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if user.ID != "u-alice" || user.Name != "alice" || !user.IsAdministrator || user.IsDisabled {
		t.Fatalf("Authenticate = %+v", user)
	}

	auth := f.authHeader.Get("Authorization")
	if !strings.HasPrefix(auth, "MediaBrowser ") || !strings.Contains(auth, `Client="Cantinarr"`) ||
		!strings.Contains(auth, `DeviceId="cantinarr-signin"`) || !strings.Contains(auth, `Device="Cantinarr sign-in check"`) {
		t.Errorf("sign-in Authorization = %q", auth)
	}
	if strings.Contains(auth, "Token=") || strings.Contains(auth, testAPIKey) {
		t.Errorf("sign-in check carried a token: %q", auth)
	}
	if got := f.authHeader.Get("X-Emby-Token"); got != "" {
		t.Errorf("sign-in check carried X-Emby-Token %q", got)
	}
	if len(f.authBody) != 2 || f.authBody["Username"] != "alice" || f.authBody["Pw"] != testPassword {
		t.Errorf("sign-in body = %v", f.authBody)
	}

	if got := f.logouts.Load(); got != 1 {
		t.Fatalf("logouts = %d, want 1", got)
	}
	logout := f.logoutHeader.Get("Authorization")
	if !strings.Contains(logout, `Token="`+testSessionToken+`"`) || !strings.Contains(logout, `DeviceId="cantinarr-signin"`) {
		t.Errorf("logout Authorization = %q", logout)
	}
	if got := f.logoutHeader.Get("X-Emby-Token"); got != testSessionToken {
		t.Errorf("logout X-Emby-Token = %q", got)
	}
	if strings.Contains(logout, testAPIKey) {
		t.Errorf("logout carried the API key: %q", logout)
	}
}

func TestAuthenticateMapsStatuses(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, mediaserver.ErrBadCredentials},
		{http.StatusForbidden, mediaserver.ErrAccountRefused},
	}
	for _, tc := range cases {
		f := &fakeSignIn{t: t, status: tc.status, echoPassword: true}
		c := testClient(t, f.handler())
		_, err := c.Authenticate(context.Background(), "alice", testPassword)
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d: err = %v, want %v", tc.status, err, tc.want)
		}
		if strings.Contains(err.Error(), testPassword) || strings.Contains(err.Error(), "127.0.0.1") {
			t.Errorf("status %d: error leaks: %v", tc.status, err)
		}
		if f.logouts.Load() != 0 {
			t.Errorf("status %d: logged out a session that never opened", tc.status)
		}
	}

	// Any other refusal is upstream trouble, said without the password or
	// the host even when the server echoes both.
	f := &fakeSignIn{t: t, status: http.StatusInternalServerError, echoPassword: true}
	c := testClient(t, f.handler())
	_, err := c.Authenticate(context.Background(), "alice", testPassword)
	if err == nil || errors.Is(err, mediaserver.ErrBadCredentials) || errors.Is(err, mediaserver.ErrAccountRefused) {
		t.Fatalf("500 err = %v", err)
	}
	if strings.Contains(err.Error(), testPassword) || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("500 error leaks: %v", err)
	}

	// A logout the server refuses does not undo a successful check.
	f = &fakeSignIn{t: t, logoutStatus: http.StatusInternalServerError}
	c = testClient(t, f.handler())
	user, err := c.Authenticate(context.Background(), "alice", testPassword)
	if err != nil || user.ID != "u-alice" {
		t.Fatalf("Authenticate with a failing logout = %+v, %v", user, err)
	}
	if f.logouts.Load() != 1 {
		t.Fatalf("logouts = %d, want 1", f.logouts.Load())
	}
}

func TestAuthenticateRedirectNeverDeliversThePassword(t *testing.T) {
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

	c := NewClient(source.URL, testAPIKey)
	_, err := c.Authenticate(context.Background(), "alice", testPassword)
	if err == nil || !strings.Contains(err.Error(), "redirect status 307") {
		t.Fatalf("err = %v, want a redirect error", err)
	}
	if strings.Contains(err.Error(), destination.URL) || strings.Contains(err.Error(), testPassword) {
		t.Fatalf("redirect error leaks: %v", err)
	}
	if got := redirectedRequests.Load(); got != 0 {
		t.Fatalf("redirect destination received %d requests, want 0", got)
	}
}

func TestAuthenticateErrorsNeverContainHostOrPassword(t *testing.T) {
	closed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := closed.URL
	closed.Close()
	c := NewClient(closedURL, testAPIKey)
	_, err := c.Authenticate(context.Background(), "alice", testPassword)
	if err == nil {
		t.Fatal("expected a transport error")
	}
	for _, forbidden := range []string{"127.0.0.1", "http://", testAPIKey, testPassword} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("transport error %q contains %q", err, forbidden)
		}
	}
}
