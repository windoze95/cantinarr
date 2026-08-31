package emby

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

const testAPIKey = "EMBY_KEY_SENTINEL"

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

// fakeServer is a minimal Emby that records what it was sent and in which
// order. Its user policy carries fields this client has never heard of and
// numbers, so a round trip that drops or reshapes anything is visible.
type fakeServer struct {
	t                  *testing.T
	mu                 sync.Mutex
	users              []map[string]any
	usersAsQueryResult bool
	policy             map[string]any
	postedPolicy       map[string]any
	createBody         map[string]any
	passwordBody       map[string]any
	calls              []string
	policyStatus       int
	passwordStatus     int
	createStatus       int
	createResponse     string
	createGarbled      bool // answer 200 with an unreadable body after making the account
	deleted            atomic.Int32
	requests           atomic.Int32
}

func newFakeServer(t *testing.T) *fakeServer {
	return &fakeServer{
		t: t,
		policy: map[string]any{
			"IsAdministrator":          true,
			"IsDisabled":               false,
			"EnableAllFolders":         true,
			"EnabledFolders":           []string{},
			"BlockedMediaFolders":      []string{},
			"ExcludedSubFolders":       []string{},
			"AuthenticationProviderId": "Emby.Server.Implementations.Library.DefaultAuthenticationProvider",
			"MaxParentalRating":        12,
			"RemoteClientBitrateLimit": 0,
			"FutureField":              map[string]any{"nested": []any{1, "two"}},
		},
	}
}

func (f *fakeServer) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeServer) decodeBody(r *http.Request) map[string]any {
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	body := map[string]any{}
	if err := dec.Decode(&body); err != nil {
		f.t.Errorf("decode %s body: %v", r.URL.Path, err)
	}
	return body
}

func (f *fakeServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/Users", func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		if f.usersAsQueryResult {
			writeJSON(f.t, w, map[string]any{"Items": f.users, "TotalRecordCount": len(f.users)})
			return
		}
		writeJSON(f.t, w, f.users)
	})
	mux.HandleFunc("/Users/New", func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		f.record("create")
		if f.createStatus != 0 {
			w.WriteHeader(f.createStatus)
			_, _ = io.WriteString(w, f.createResponse)
			return
		}
		body := f.decodeBody(r)
		name, _ := body["Name"].(string)
		f.mu.Lock()
		f.createBody = body
		// The account exists from here on, whatever the answer looks like.
		f.users = append(f.users, map[string]any{"Id": "new-user-id", "Name": name, "Policy": f.policy})
		f.mu.Unlock()
		if f.createGarbled {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte{0x1f, 0x8b, 'n', 'o', 't', ' ', 'j', 's', 'o', 'n'})
			return
		}
		writeJSON(f.t, w, map[string]any{"Id": "new-user-id", "Name": name, "HasPassword": false, "Policy": f.policy})
	})
	mux.HandleFunc("/Users/", func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		rest := strings.TrimPrefix(r.URL.Path, "/Users/")
		switch {
		case strings.HasSuffix(rest, "/Password") && r.Method == http.MethodPost:
			f.record("password")
			if f.passwordStatus != 0 {
				w.WriteHeader(f.passwordStatus)
				return
			}
			body := f.decodeBody(r)
			f.mu.Lock()
			f.passwordBody = body
			f.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(rest, "/Policy") && r.Method == http.MethodPost:
			f.record("policy")
			if f.policyStatus != 0 {
				w.WriteHeader(f.policyStatus)
				return
			}
			body := f.decodeBody(r)
			f.mu.Lock()
			f.postedPolicy = body
			f.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete:
			f.record("delete")
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

func TestHeadersCarryEmbyToken(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/System/Info" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Emby-Token"); got != testAPIKey {
			t.Errorf("X-Emby-Token = %q", got)
		}
		auth := r.Header.Get("X-Emby-Authorization")
		if !strings.HasPrefix(auth, "MediaBrowser ") || !strings.Contains(auth, `Token="`+testAPIKey+`"`) || !strings.Contains(auth, `Client="Cantinarr"`) {
			t.Errorf("X-Emby-Authorization = %q", auth)
		}
		writeJSON(t, w, map[string]any{"ServerName": "Den", "Version": "4.8.11.0", "Id": "srv-1"})
	}))
	info, err := c.SystemInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.ServerName != "Den" || info.Version != "4.8.11.0" || info.ID != "srv-1" {
		t.Fatalf("SystemInfo = %+v", info)
	}
}

// The policy wants the folder Guid, not the numeric Id Emby 4.7 introduced;
// a folder without a Guid falls back to its Id, whatever JSON type it came
// as; and the server's filesystem paths never leave the client.
func TestLibrariesUseGuidAndNeverPaths(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Library/MediaFolders" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"Items":[
			{"Name":"Movies","Id":"5","Guid":"guid-movies","CollectionType":"movies","Path":"/srv/media/movies"},
			{"Name":"Shows","Id":7,"CollectionType":"tvshows","Path":"/srv/media/shows"},
			{"Name":"Broken","CollectionType":"music"}
		],"TotalRecordCount":3}`)
	}))
	libs, err := c.Libraries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(libs) != 2 || libs[0].ID != "guid-movies" || libs[0].CollectionType != "movies" || libs[1].ID != "7" || libs[1].Name != "Shows" {
		t.Fatalf("Libraries = %+v", libs)
	}
	encoded, _ := json.Marshal(libs)
	if strings.Contains(string(encoded), "/srv/media") {
		t.Fatalf("library paths leaked: %s", encoded)
	}
}

func TestUsersDecodeBareListAndQueryResult(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		f := newFakeServer(t)
		f.usersAsQueryResult = wrapped
		f.users = []map[string]any{
			{"Id": "u1", "Name": "admin", "Policy": map[string]any{"IsAdministrator": true}},
			{"Id": "u2", "Name": "bob", "Policy": map[string]any{"IsDisabled": true}},
		}
		c := testClient(t, f.handler())
		users, err := c.Users(context.Background())
		if err != nil {
			t.Fatalf("wrapped=%v: %v", wrapped, err)
		}
		if len(users) != 2 || !users[0].IsAdministrator || users[1].IsAdministrator || !users[1].IsDisabled || users[1].Name != "bob" {
			t.Fatalf("wrapped=%v: Users = %+v", wrapped, users)
		}
	}
}

func TestCreateUserSetsPasswordThenRestricts(t *testing.T) {
	f := newFakeServer(t)
	c := testClient(t, f.handler())

	user, err := c.CreateUser(context.Background(), "alice", "alice-pass-1", []string{"guid-movies"})
	if err != nil {
		t.Fatal(err)
	}
	if user.ID != "new-user-id" || user.Name != "alice" || user.IsAdministrator {
		t.Fatalf("CreateUser = %+v", user)
	}
	if strings.Join(f.calls, ",") != "create,password,policy" {
		t.Fatalf("calls = %v, want create, password, policy", f.calls)
	}
	// Emby's create route has no password field; sending one would be
	// silently dropped at best. The password goes in its own call.
	if _, sent := f.createBody["Password"]; sent {
		t.Error("create user sent a Password field")
	}
	if f.createBody["Name"] != "alice" {
		t.Errorf("create body = %v", f.createBody)
	}
	if f.passwordBody["NewPw"] != "alice-pass-1" || f.passwordBody["ResetPassword"] != false || f.passwordBody["Id"] != "new-user-id" {
		t.Errorf("password body = %v", f.passwordBody)
	}
	if current, ok := f.passwordBody["CurrentPw"]; !ok || current != "" {
		t.Errorf("CurrentPw = %#v, want an empty string (a new account's current password)", current)
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
	if folders, _ := p["EnabledFolders"].([]any); len(folders) != 1 || folders[0] != "guid-movies" {
		t.Errorf("EnabledFolders = %v", p["EnabledFolders"])
	}
	// Everything the server sent comes back untouched, including fields this
	// client has never heard of and numbers in their original form.
	for _, key := range []string{"AuthenticationProviderId", "BlockedMediaFolders", "ExcludedSubFolders", "FutureField", "RemoteClientBitrateLimit"} {
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

// An Emby account without a password signs in with an empty one, so a
// create whose password write fails must not leave the account behind.
func TestCreateUserRollsBackWhenPasswordFails(t *testing.T) {
	f := newFakeServer(t)
	f.passwordStatus = http.StatusInternalServerError
	c := testClient(t, f.handler())

	_, err := c.CreateUser(context.Background(), "alice", "alice-pass-1", []string{"guid-movies"})
	if err == nil {
		t.Fatal("CreateUser succeeded despite a failed password write")
	}
	if !strings.Contains(err.Error(), "set password") {
		t.Fatalf("err = %v, want the password step named", err)
	}
	if f.deleted.Load() != 1 {
		t.Fatalf("half-created user deleted %d times, want 1", f.deleted.Load())
	}
	if f.postedPolicy != nil {
		t.Fatal("policy was posted for an account that was being rolled back")
	}
}

// A create whose answer cannot be read is not a refusal: the account may
// exist. The pre-check proved the name was free a moment ago, so the account
// that carries it now is the new one, and it is deleted rather than left
// behind without a password.
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
	if strings.Join(f.calls, ",") != "create,delete" {
		t.Fatalf("calls = %v, want create then delete", f.calls)
	}
}

func TestCreateUserRollsBackWhenPolicyFails(t *testing.T) {
	f := newFakeServer(t)
	f.policyStatus = http.StatusInternalServerError
	c := testClient(t, f.handler())

	_, err := c.CreateUser(context.Background(), "alice", "alice-pass-1", []string{"guid-movies"})
	if err == nil {
		t.Fatal("CreateUser succeeded despite a failed policy update")
	}
	if f.deleted.Load() != 1 {
		t.Fatalf("half-created user deleted %d times, want 1", f.deleted.Load())
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
	if len(f.calls) != 0 || f.deleted.Load() != 0 {
		t.Fatalf("collision must not create or touch anything: calls = %v", f.calls)
	}
}

// A 400 on create is either the duplicate Emby's own check missed or the
// name rule, which is closed-source and so cannot be mirrored exactly. Both
// map to a sentinel the handler already explains; neither creates anything
// to roll back.
func TestCreateUserMaps400s(t *testing.T) {
	cases := []struct {
		body string
		want error
	}{
		{`"A user with the name 'alice' already exists."`, mediaserver.ErrUserExists},
		{`"Usernames can contain letters (a-z), numbers (0-9), dashes (-), underscores (_), apostrophes ('), and periods (.)"`, mediaserver.ErrInvalidName},
		{``, mediaserver.ErrInvalidName},
	}
	for _, tc := range cases {
		f := newFakeServer(t)
		f.createStatus = http.StatusBadRequest
		f.createResponse = tc.body
		c := testClient(t, f.handler())
		_, err := c.CreateUser(context.Background(), "alice", "alice-pass-1", nil)
		if !errors.Is(err, tc.want) {
			t.Fatalf("body %s: err = %v, want %v", tc.body, err, tc.want)
		}
		if f.deleted.Load() != 0 {
			t.Fatalf("body %s: a refused create deleted something", tc.body)
		}
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

func TestGetUser404IsErrUserNotFound(t *testing.T) {
	f := newFakeServer(t)
	c := testClient(t, f.handler())
	if _, err := c.GetUser(context.Background(), "missing-user"); !errors.Is(err, mediaserver.ErrUserNotFound) {
		t.Fatalf("err = %v, want ErrUserNotFound", err)
	}
	if err := c.SetDisabled(context.Background(), "missing-user", true); !errors.Is(err, mediaserver.ErrUserNotFound) {
		t.Fatalf("SetDisabled err = %v, want ErrUserNotFound", err)
	}
	one, err := c.GetUser(context.Background(), "u1")
	if err != nil || one.ID != "u1" || !one.IsAdministrator {
		t.Fatalf("GetUser = %+v, %v", one, err)
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
		for _, header := range []string{"X-Emby-Token", "X-Emby-Authorization", "Authorization"} {
			if r.Header.Get(header) != "" {
				t.Errorf("redirect destination received %s", header)
			}
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
