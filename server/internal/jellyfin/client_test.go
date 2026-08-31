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

const testAPIKey = "JELLYFIN_KEY_SENTINEL"

func testClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, testAPIKey)
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatal(err)
	}
}

// fakeServer is a minimal Jellyfin that records the policy it was sent.
type fakeServer struct {
	t             *testing.T
	users         []map[string]any
	policy        map[string]any
	postedPolicy  map[string]any
	policyStatus  int
	createStatus  int
	createBody    string
	createGarbled bool // answer 200 with an unreadable body after making the account
	deleted       atomic.Int32
	requests      atomic.Int32
}

func newFakeServer(t *testing.T) *fakeServer {
	return &fakeServer{
		t: t,
		policy: map[string]any{
			"IsAdministrator":          true,
			"IsDisabled":               false,
			"EnableAllFolders":         true,
			"EnabledFolders":           []string{},
			"AuthenticationProviderId": "Jellyfin.Server.Implementations.Users.DefaultAuthenticationProvider",
			"PasswordResetProviderId":  "Jellyfin.Server.Implementations.Users.DefaultPasswordResetProvider",
			"MaxParentalRating":        12,
			"RemoteClientBitrateLimit": 0,
			"FutureField":              map[string]any{"nested": []any{1, "two"}},
		},
	}
}

func (f *fakeServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/Users", func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		writeJSON(f.t, w, f.users)
	})
	mux.HandleFunc("/Users/New", func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if f.createStatus != 0 {
			w.WriteHeader(f.createStatus)
			_, _ = io.WriteString(w, f.createBody)
			return
		}
		var body struct{ Name, Password string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Errorf("decode create body: %v", err)
		}
		if body.Password == "" {
			f.t.Error("create user sent no password")
		}
		// The account exists from here on, whatever the answer looks like.
		f.users = append(f.users, map[string]any{"Id": "new-user-id", "Name": body.Name, "Policy": f.policy})
		if f.createGarbled {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte{0x1f, 0x8b, 'n', 'o', 't', ' ', 'j', 's', 'o', 'n'})
			return
		}
		writeJSON(f.t, w, map[string]any{"Id": "new-user-id", "Name": body.Name, "Policy": f.policy})
	})
	mux.HandleFunc("/Users/", func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		rest := strings.TrimPrefix(r.URL.Path, "/Users/")
		switch {
		case strings.HasSuffix(rest, "/Policy") && r.Method == http.MethodPost:
			if f.policyStatus != 0 {
				w.WriteHeader(f.policyStatus)
				return
			}
			dec := json.NewDecoder(r.Body)
			dec.UseNumber()
			f.postedPolicy = map[string]any{}
			if err := dec.Decode(&f.postedPolicy); err != nil {
				f.t.Errorf("decode posted policy: %v", err)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete:
			f.deleted.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet:
			if rest == "missing-user" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeJSON(f.t, w, map[string]any{"Id": rest, "Name": "alice", "Policy": f.policy})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	return mux
}

func TestHeadersCarryTokenInBothForms(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/System/Info" {
			t.Errorf("path = %s", r.URL.Path)
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "MediaBrowser ") || !strings.Contains(auth, `Token="`+testAPIKey+`"`) {
			t.Errorf("Authorization = %q", auth)
		}
		if got := r.Header.Get("X-Emby-Token"); got != testAPIKey {
			t.Errorf("X-Emby-Token = %q", got)
		}
		writeJSON(t, w, map[string]any{"ServerName": "Home", "Version": "10.11.0", "Id": "srv-1"})
	}))
	info, err := c.SystemInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.ServerName != "Home" || info.Version != "10.11.0" || info.ID != "srv-1" {
		t.Fatalf("SystemInfo = %+v", info)
	}
}

func TestLibrariesDropLocations(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Library/VirtualFolders" {
			t.Errorf("path = %s", r.URL.Path)
		}
		writeJSON(t, w, []map[string]any{
			{"Name": "Movies", "CollectionType": "movies", "ItemId": "lib-movies", "Locations": []string{"/srv/media/movies"}},
			{"Name": "Shows", "CollectionType": "tvshows", "ItemId": "lib-shows", "Locations": []string{"/srv/media/shows"}},
			{"Name": "Broken", "CollectionType": "music", "ItemId": ""},
		})
	}))
	libs, err := c.Libraries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(libs) != 2 || libs[0].ID != "lib-movies" || libs[1].Name != "Shows" || libs[1].CollectionType != "tvshows" {
		t.Fatalf("Libraries = %+v", libs)
	}
	encoded, _ := json.Marshal(libs)
	if strings.Contains(string(encoded), "/srv/media") {
		t.Fatalf("library paths leaked: %s", encoded)
	}
}

func TestCreateUserRoundTripsFetchedPolicy(t *testing.T) {
	f := newFakeServer(t)
	c := testClient(t, f.handler())

	user, err := c.CreateUser(context.Background(), "alice", "alice-pass-1", []string{"lib-movies"})
	if err != nil {
		t.Fatal(err)
	}
	if user.ID != "new-user-id" || user.Name != "alice" || user.IsAdministrator {
		t.Fatalf("CreateUser = %+v", user)
	}
	p := f.postedPolicy
	if p == nil {
		t.Fatal("policy was never posted")
	}
	if p["IsAdministrator"] != false {
		t.Errorf("IsAdministrator = %v, want false", p["IsAdministrator"])
	}
	if p["EnableAllFolders"] != false {
		t.Errorf("EnableAllFolders = %v, want false", p["EnableAllFolders"])
	}
	if folders, _ := p["EnabledFolders"].([]any); len(folders) != 1 || folders[0] != "lib-movies" {
		t.Errorf("EnabledFolders = %v", p["EnabledFolders"])
	}
	// Everything the server sent comes back untouched, including fields this
	// client has never heard of and numbers in their original form.
	for _, key := range []string{"AuthenticationProviderId", "PasswordResetProviderId", "FutureField", "RemoteClientBitrateLimit"} {
		if _, ok := p[key]; !ok {
			t.Errorf("posted policy dropped %s", key)
		}
	}
	if n, ok := p["MaxParentalRating"].(json.Number); !ok || n.String() != "12" {
		t.Errorf("MaxParentalRating = %#v, want json.Number 12", p["MaxParentalRating"])
	}
	if f.deleted.Load() != 0 {
		t.Fatal("successful create must not delete anything")
	}
}

func TestCreateUserShareAllWhenNoLibraries(t *testing.T) {
	f := newFakeServer(t)
	c := testClient(t, f.handler())
	if _, err := c.CreateUser(context.Background(), "alice", "alice-pass-1", nil); err != nil {
		t.Fatal(err)
	}
	if f.postedPolicy["EnableAllFolders"] != true {
		t.Fatalf("EnableAllFolders = %v, want true", f.postedPolicy["EnableAllFolders"])
	}
}

func TestCreateUserRollsBackWhenPolicyFails(t *testing.T) {
	f := newFakeServer(t)
	f.policyStatus = http.StatusInternalServerError
	c := testClient(t, f.handler())

	_, err := c.CreateUser(context.Background(), "alice", "alice-pass-1", []string{"lib-movies"})
	if err == nil {
		t.Fatal("CreateUser succeeded despite a failed policy update")
	}
	if f.deleted.Load() != 1 {
		t.Fatalf("half-created user deleted %d times, want 1", f.deleted.Load())
	}
}

// A create whose answer cannot be read is not a refusal: the account may
// exist. The pre-check proved the name was free a moment ago, so the account
// that carries it now is the new one, and it is deleted rather than left
// where Cantinarr cannot see it.
func TestCreateUserRollsBackWhenCreateAnswerIsUnreadable(t *testing.T) {
	f := newFakeServer(t)
	f.createGarbled = true
	c := testClient(t, f.handler())

	_, err := c.CreateUser(context.Background(), "alice", "alice-pass-1", nil)
	if err == nil {
		t.Fatal("CreateUser succeeded on an unreadable answer")
	}
	if f.deleted.Load() != 1 {
		t.Fatalf("half-created user deleted %d times, want 1", f.deleted.Load())
	}
	if f.postedPolicy != nil {
		t.Fatal("a policy was posted for an account that was being rolled back")
	}
}

func TestCreateUserRejectsInvalidNameBeforeAnyRequest(t *testing.T) {
	f := newFakeServer(t)
	c := testClient(t, f.handler())
	_, err := c.CreateUser(context.Background(), " alice/1", "alice-pass-1", nil)
	if !errors.Is(err, mediaserver.ErrInvalidName) {
		t.Fatalf("err = %v, want ErrInvalidName", err)
	}
	if f.requests.Load() != 0 {
		t.Fatalf("server received %d requests, want 0", f.requests.Load())
	}
}

func TestCreateUserDetectsCaseInsensitiveCollision(t *testing.T) {
	f := newFakeServer(t)
	f.users = []map[string]any{{"Id": "u1", "Name": "Alice", "Policy": map[string]any{}}}
	c := testClient(t, f.handler())
	_, err := c.CreateUser(context.Background(), "alice", "alice-pass-1", nil)
	if !errors.Is(err, mediaserver.ErrUserExists) {
		t.Fatalf("err = %v, want ErrUserExists", err)
	}
	if f.postedPolicy != nil || f.deleted.Load() != 0 {
		t.Fatal("collision must not create or touch anything")
	}
}

func TestCreateUserMaps400AlreadyExists(t *testing.T) {
	f := newFakeServer(t)
	f.createStatus = http.StatusBadRequest
	f.createBody = `"A user with the name 'alice' already exists."`
	c := testClient(t, f.handler())
	_, err := c.CreateUser(context.Background(), "alice", "alice-pass-1", nil)
	if !errors.Is(err, mediaserver.ErrUserExists) {
		t.Fatalf("err = %v, want ErrUserExists", err)
	}
}

func TestSetDisabledRoundTrip(t *testing.T) {
	f := newFakeServer(t)
	c := testClient(t, f.handler())
	if err := c.SetDisabled(context.Background(), "u1", true); err != nil {
		t.Fatal(err)
	}
	if f.postedPolicy["IsDisabled"] != true {
		t.Fatalf("IsDisabled = %v, want true", f.postedPolicy["IsDisabled"])
	}
	if f.postedPolicy["IsAdministrator"] != true {
		t.Fatal("SetDisabled must not touch unrelated policy fields")
	}
	if err := c.SetDisabled(context.Background(), "u1", false); err != nil {
		t.Fatal(err)
	}
	if f.postedPolicy["IsDisabled"] != false {
		t.Fatalf("IsDisabled = %v, want false", f.postedPolicy["IsDisabled"])
	}
}

func TestUsersAndGetUserReadPolicyFlags(t *testing.T) {
	f := newFakeServer(t)
	f.users = []map[string]any{
		{"Id": "u1", "Name": "admin", "Policy": map[string]any{"IsAdministrator": true}},
		{"Id": "u2", "Name": "bob", "Policy": map[string]any{"IsDisabled": true}},
	}
	c := testClient(t, f.handler())
	users, err := c.Users(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || !users[0].IsAdministrator || !users[1].IsDisabled || users[1].IsAdministrator {
		t.Fatalf("Users = %+v", users)
	}
	one, err := c.GetUser(context.Background(), "u1")
	if err != nil || one.ID != "u1" || !one.IsAdministrator {
		t.Fatalf("GetUser = %+v, %v", one, err)
	}
}

func TestGetUser404IsErrUserNotFound(t *testing.T) {
	f := newFakeServer(t)
	c := testClient(t, f.handler())
	if _, err := c.GetUser(context.Background(), "missing-user"); !errors.Is(err, mediaserver.ErrUserNotFound) {
		t.Fatalf("err = %v, want ErrUserNotFound", err)
	}
	if err := c.SetDisabled(context.Background(), "missing-user", true); !errors.Is(err, mediaserver.ErrUserNotFound) {
		t.Fatalf("SetDisabled err = %v, want ErrUserNotFound", err)
	}
}

func TestDeleteUserTreatsGoneAsDeleted(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	if err := c.DeleteUser(context.Background(), "gone"); err != nil {
		t.Fatalf("DeleteUser on a missing user = %v, want nil", err)
	}
}

func TestRedirectNeverDeliversKey(t *testing.T) {
	var redirectedRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedRequests.Add(1)
		if r.Header.Get("X-Emby-Token") != "" || r.Header.Get("Authorization") != "" {
			t.Error("redirect destination received credentials")
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(destination.Close)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/credential-sink", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)

	c := NewClient(source.URL, testAPIKey)
	_, err := c.SystemInfo(context.Background())
	if err == nil || !strings.Contains(err.Error(), "redirect status 307") {
		t.Fatalf("err = %v, want a redirect error", err)
	}
	if strings.Contains(err.Error(), destination.URL) || strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("redirect error leaks destination or key: %v", err)
	}
	if got := redirectedRequests.Load(); got != 0 {
		t.Fatalf("redirect destination received %d requests, want 0", got)
	}
}

func TestErrorsNeverContainHostOrKey(t *testing.T) {
	// A server that is gone: the transport error must not name the host.
	closed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := closed.URL
	closed.Close()
	c := NewClient(closedURL, testAPIKey)
	_, err := c.SystemInfo(context.Background())
	if err == nil {
		t.Fatal("expected a transport error")
	}
	for _, forbidden := range []string{"127.0.0.1", "http://", testAPIKey} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("transport error %q contains %q", err, forbidden)
		}
	}

	// A 500 whose body echoes the key: the body is never rendered.
	c = testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "boom "+testAPIKey+" "+r.Host)
	}))
	_, err = c.SystemInfo(context.Background())
	if err == nil || strings.Contains(err.Error(), testAPIKey) || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("status error leaks: %v", err)
	}

	// 401 reads as a bad key, without echoing anything.
	c = testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	_, err = c.SystemInfo(context.Background())
	if err == nil || !strings.Contains(err.Error(), "rejected the API key") {
		t.Fatalf("401 err = %v", err)
	}
}
