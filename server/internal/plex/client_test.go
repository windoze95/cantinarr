package plex

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewClient()
	c.baseURL = srv.URL
	return c
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	var redirectedRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedRequests.Add(1)
		if got := r.Header.Get("X-Plex-Token"); got != "" {
			t.Errorf("redirect destination received X-Plex-Token %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(destination.Close)

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/credential-sink", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)

	client := NewClient()
	client.baseURL = source.URL
	if _, err := client.GetUser(context.Background(), "client-id", "plex-secret"); err == nil {
		t.Fatal("GetUser accepted an upstream redirect")
	}
	if got := redirectedRequests.Load(); got != 0 {
		t.Fatalf("redirect destination received %d requests, want 0", got)
	}
}

func TestCreateAndCheckPin(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/pins", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("pins method = %s", r.Method)
		}
		if r.URL.Query().Get("strong") != "true" {
			t.Error("expected strong=true")
		}
		if r.Header.Get("X-Plex-Client-Identifier") != "cid-1" {
			t.Errorf("client id header = %q", r.Header.Get("X-Plex-Client-Identifier"))
		}
		if r.Header.Get("X-Plex-Product") == "" {
			t.Error("missing X-Plex-Product")
		}
		json.NewEncoder(w).Encode(map[string]any{"id": 123, "code": "abcd"})
	})
	mux.HandleFunc("/api/v2/pins/123", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"id": 123, "code": "abcd", "authToken": "tok-9"})
	})
	c := testClient(t, mux)

	pin, err := c.CreatePin(context.Background(), "cid-1")
	if err != nil {
		t.Fatalf("create pin: %v", err)
	}
	if pin.ID != 123 || pin.Code != "abcd" {
		t.Fatalf("pin = %+v", pin)
	}

	checked, err := c.CheckPin(context.Background(), "cid-1", 123)
	if err != nil {
		t.Fatalf("check pin: %v", err)
	}
	if checked.AuthToken != "tok-9" {
		t.Fatalf("auth token = %q", checked.AuthToken)
	}
}

func TestAuthURLCarriesClientAndCode(t *testing.T) {
	c := NewClient()
	u := c.AuthURL("cid-1", "abcd")
	if !strings.HasPrefix(u, "https://app.plex.tv/auth#?") {
		t.Fatalf("auth url = %q", u)
	}
	for _, want := range []string{"clientID=cid-1", "code=abcd", "context%5Bdevice%5D%5Bproduct%5D=Cantinarr"} {
		if !strings.Contains(u, want) {
			t.Errorf("auth url missing %q: %s", want, u)
		}
	}
}

func TestListServersFiltersOwnedServers(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/resources", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") != "tok" {
			t.Errorf("token header = %q", r.Header.Get("X-Plex-Token"))
		}
		json.NewEncoder(w).Encode([]map[string]any{
			{"name": "Cantina", "clientIdentifier": "m1", "provides": "server", "owned": true},
			{"name": "Friend's", "clientIdentifier": "m2", "provides": "server", "owned": false},
			{"name": "Apple TV", "clientIdentifier": "m3", "provides": "client,player", "owned": true},
		})
	})
	c := testClient(t, mux)

	servers, err := c.ListServers(context.Background(), "cid", "tok")
	if err != nil {
		t.Fatalf("list servers: %v", err)
	}
	if len(servers) != 1 || servers[0].ClientIdentifier != "m1" {
		t.Fatalf("servers = %+v", servers)
	}
}

func TestListLibrariesParsesSectionIDs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/servers/m1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<MediaContainer friendlyName="myPlex" size="1">
  <Server name="Cantina" machineIdentifier="m1">
    <Section id="101" key="1" type="movie" title="Movies"/>
    <Section id="102" key="2" type="show" title="TV Shows"/>
  </Server>
</MediaContainer>`))
	})
	c := testClient(t, mux)

	libs, err := c.ListLibraries(context.Background(), "cid", "tok", "m1")
	if err != nil {
		t.Fatalf("list libraries: %v", err)
	}
	if len(libs) != 2 {
		t.Fatalf("libraries = %+v", libs)
	}
	if libs[0].ID != 101 || libs[0].Title != "Movies" || libs[1].ID != 102 {
		t.Fatalf("libraries = %+v", libs)
	}
}

func TestInviteEmailSendsExpectedPayload(t *testing.T) {
	var got map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/shared_servers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if r.Header.Get("X-Plex-Token") != "tok" {
			t.Errorf("token header = %q", r.Header.Get("X-Plex-Token"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("{}"))
	})
	c := testClient(t, mux)

	err := c.InviteEmail(context.Background(), "cid", "tok", "m1", "bob@example.com", []int64{101, 102})
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	if got["machineIdentifier"] != "m1" || got["invitedEmail"] != "bob@example.com" {
		t.Fatalf("payload = %+v", got)
	}
	ids, ok := got["librarySectionIds"].([]any)
	if !ok || len(ids) != 2 {
		t.Fatalf("librarySectionIds = %+v", got["librarySectionIds"])
	}
}

func TestInviteEmailMapsDuplicateShare(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/shared_servers", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"errors":[{"code":422,"message":"Account already has access to this server"}]}`))
	})
	c := testClient(t, mux)

	err := c.InviteEmail(context.Background(), "cid", "tok", "m1", "bob@example.com", nil)
	if !errors.Is(err, ErrAlreadyShared) {
		t.Fatalf("expected ErrAlreadyShared, got %v", err)
	}
}

func TestGetUserDecodesTheAccountAndSignOutRemovesTheDevice(t *testing.T) {
	var signedOut, deleted, lists, listFrom atomic.Int32
	var deletedPath string
	var listDevice atomic.Bool
	listDevice.Store(true)
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") != "user-token" || r.Header.Get("X-Plex-Client-Identifier") != "client-id" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v2/user":
			w.Write([]byte(`{"id":12345,"uuid":"u-1","username":"rey","email":"Rey@Example.com","title":"Rey","thumb":"https://plex.tv/users/x/avatar","authToken":"user-token"}`))
		case "GET /devices.xml":
			n := lists.Add(1)
			w.Header().Set("Content-Type", "application/xml")
			mine := ""
			// Listed outright, or only from the listFrom-th listing on (a
			// device plex.tv registers a moment after the approval).
			if listDevice.Load() || (listFrom.Load() > 0 && n >= listFrom.Load()) {
				mine = `<Device id="1778846848" name="Cantinarr" clientIdentifier="client-id" product="Cantinarr"/>`
			}
			w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><MediaContainer size="2"><Device id="555" name="Chrome" clientIdentifier="browser-cid" product="Plex Web"/>` + mine + `</MediaContainer>`))
		case "DELETE /devices/1778846848.xml":
			deleted.Add(1)
			deletedPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		case "DELETE /api/v2/users/signout":
			signedOut.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	acct, err := client.GetUser(context.Background(), "client-id", "user-token")
	if err != nil {
		t.Fatal(err)
	}
	if acct.ID != 12345 || acct.UUID != "u-1" || acct.Username != "rey" || acct.Email != "Rey@Example.com" || acct.Title != "Rey" {
		t.Fatalf("account = %+v", acct)
	}
	// The device plex.tv registered for this client goes; the browser's
	// device and the plain sign-out are left alone.
	removed, err := client.SignOut(context.Background(), "client-id", "user-token")
	if err != nil || !removed {
		t.Fatalf("SignOut = %v, %v", removed, err)
	}
	if deleted.Load() != 1 || deletedPath != "/devices/1778846848.xml" || signedOut.Load() != 0 {
		t.Fatalf("deleted=%d path=%q signouts=%d", deleted.Load(), deletedPath, signedOut.Load())
	}
	// A device that shows up a moment after the approval is still found.
	listDevice.Store(false)
	lists.Store(0)
	listFrom.Store(3)
	signOutDeviceWait, signOutDevicePoll = 2*time.Second, 20*time.Millisecond
	removed, err = client.SignOut(context.Background(), "client-id", "user-token")
	if err != nil || !removed || deleted.Load() != 2 || signedOut.Load() != 0 {
		t.Fatalf("late device: removed=%v err=%v deleted=%d signouts=%d", removed, err, deleted.Load(), signedOut.Load())
	}
	// Never registered: the token is signed out directly once the wait is up.
	listFrom.Store(0)
	signOutDeviceWait = 50 * time.Millisecond
	removed, err = client.SignOut(context.Background(), "client-id", "user-token")
	if err != nil || removed || deleted.Load() != 2 || signedOut.Load() != 1 {
		t.Fatalf("fallback: removed=%v err=%v deleted=%d signouts=%d", removed, err, deleted.Load(), signedOut.Load())
	}
	if _, err := client.SignOut(context.Background(), "client-id", "wrong-token"); err == nil {
		t.Fatal("a refused sign-out was reported as done")
	}
}
