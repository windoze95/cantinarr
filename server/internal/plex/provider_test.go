package plex

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

// fakePlexTV is the slice of plex.tv the provider talks to: one owned server
// with two sections, its shares, and the account's sent invites. It records
// the writes it receives.
type fakePlexTV struct {
	mu           sync.Mutex
	t            *testing.T
	shares       string // XML SharedServer elements
	invites      string // XML Invite elements
	requests     []string
	invited      map[string]any
	updated      map[string]any
	inviteStatus int
}

func newFakePlexTV(t *testing.T) (*fakePlexTV, *Client) {
	t.Helper()
	f := &fakePlexTV{t: t, inviteStatus: http.StatusCreated}
	mux := http.NewServeMux()
	authed := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("X-Plex-Token") != "tok-SECRET" || r.Header.Get("X-Plex-Client-Identifier") != "cid-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return false
		}
		return true
	}
	record := func(r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI())
		f.mu.Unlock()
	}
	mux.HandleFunc("/api/servers/m1", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if !authed(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<MediaContainer friendlyName="myPlex" size="1">
  <Server name="Cantina" version="1.41.0.8992" machineIdentifier="m1" host="10.0.0.5" port="32400">
    <Section id="101" key="1" type="movie" title="Movies"/>
    <Section id="102" key="2" type="show" title="TV Shows"/>
  </Server>
</MediaContainer>`))
	})
	mux.HandleFunc("/api/servers/m1/shared_servers", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if !authed(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		f.mu.Lock()
		shares := f.shares
		f.mu.Unlock()
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><MediaContainer size="2">` + shares + `</MediaContainer>`))
	})
	mux.HandleFunc("/api/servers/m1/shared_servers/", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if !authed(w, r) {
			return
		}
		if r.Method == http.MethodPut {
			f.mu.Lock()
			_ = json.NewDecoder(r.Body).Decode(&f.updated)
			f.mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/invites/requested", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if !authed(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		f.mu.Lock()
		invites := f.invites
		f.mu.Unlock()
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><MediaContainer size="1">` + invites + `</MediaContainer>`))
	})
	mux.HandleFunc("/api/invites/requested/", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if !authed(w, r) {
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/v2/shared_servers", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if !authed(w, r) {
			return
		}
		f.mu.Lock()
		_ = json.NewDecoder(r.Body).Decode(&f.invited)
		status := f.inviteStatus
		f.mu.Unlock()
		w.WriteHeader(status)
		if status == http.StatusUnprocessableEntity {
			w.Write([]byte(`{"errors":[{"code":422,"message":"Account already has access to this server"}]}`))
			return
		}
		w.Write([]byte(`{}`))
	})
	c := testClient(t, mux)
	return f, c
}

func (f *fakePlexTV) saw(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			return true
		}
	}
	return false
}

func (f *fakePlexTV) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

const (
	acceptedShare = `<SharedServer id="501" username="bob" email="Bob@Example.com" userID="77" acceptedAt="1700000000" invitedAt="1699999999"><Section id="101" key="1" title="Movies" type="movie" shared="1"/><Section id="102" key="2" title="TV Shows" type="show" shared="0"/></SharedServer>`
	pendingShare  = `<SharedServer id="502" username="carol" email="carol@example.com" userID="78" accepted="0" invitedAt="1700000100"/>`
	// plex.tv keys an invite to someone with no account by the email itself.
	sentInvite    = `<Invite id="Dave@Example.com" createdAt="1700000200" friend="0" home="0" server="1" username="" email="Dave@Example.com" friendlyName="dave"><Server name="Cantina" machineIdentifier="m1" numLibraries="2"/></Invite>`
	otherInvite   = `<Invite id="901" createdAt="1700000300" friend="0" home="0" server="1" email="erin@example.com"><Server name="Other" machineIdentifier="m2" numLibraries="1"/></Invite>`
	friendRequest = `<Invite id="902" createdAt="1700000400" friend="1" home="0" server="0" email="frank@example.com"/>`
)

func newTestProvider(t *testing.T) (*fakePlexTV, *Provider) {
	t.Helper()
	f, c := newFakePlexTV(t)
	f.shares = acceptedShare + pendingShare
	f.invites = sentInvite + otherInvite + friendRequest
	return f, NewProvider(c, "cid-1", "tok-SECRET", "m1")
}

func TestProviderIsAnInviteServerThatListsSharesAndPendingInvites(t *testing.T) {
	_, p := newTestProvider(t)
	if mediaserver.KindOf(p) != mediaserver.KindInvite {
		t.Fatal("provider is not an invite server")
	}
	users, err := p.Users(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"bob@example.com": false, "carol@example.com": true, "dave@example.com": true}
	if len(users) != len(want) {
		t.Fatalf("users = %+v", users)
	}
	for _, u := range users {
		pending, ok := want[u.ID]
		if !ok || u.Pending != pending || u.IsDisabled || u.IsAdministrator {
			t.Errorf("user %+v unexpected", u)
		}
	}
	// Ids are canonical, names are what plex.tv shows, an invite for another
	// server and a plain friend request are not shares of this one.
	for _, u := range users {
		if u.ID != strings.ToLower(u.ID) {
			t.Errorf("id %q is not canonical", u.ID)
		}
		if u.ID == "bob@example.com" && u.Name != "bob" {
			t.Errorf("bob's name = %q", u.Name)
		}
	}
}

func TestProviderGetUserMatchesEmailOrUsernameCaseInsensitively(t *testing.T) {
	_, p := newTestProvider(t)
	for _, identity := range []string{"BOB@example.com", " bob@Example.COM ", "Bob"} {
		u, err := p.GetUser(context.Background(), identity)
		if err != nil || u.ID != "bob@example.com" || u.Pending {
			t.Errorf("GetUser(%q) = %+v, %v", identity, u, err)
		}
	}
	u, err := p.GetUser(context.Background(), "dave@example.com")
	if err != nil || !u.Pending {
		t.Fatalf("pending invite = %+v, %v", u, err)
	}
	for _, missing := range []string{"nobody@example.com", "erin@example.com", "frank@example.com", ""} {
		if _, err := p.GetUser(context.Background(), missing); !errors.Is(err, mediaserver.ErrUserNotFound) {
			t.Errorf("GetUser(%q) err = %v, want ErrUserNotFound", missing, err)
		}
	}
}

func TestProviderSystemInfoAndLibrariesReadTheServer(t *testing.T) {
	f, p := newTestProvider(t)
	info, err := p.SystemInfo(context.Background())
	if err != nil || info.ServerName != "Cantina" || info.Version != "1.41.0.8992" || info.ID != "m1" {
		t.Fatalf("SystemInfo = %+v, %v", info, err)
	}
	libs, err := p.Libraries(context.Background())
	if err != nil || len(libs) != 2 || libs[0].ID != "101" || libs[0].Name != "Movies" || libs[1].CollectionType != "show" {
		t.Fatalf("Libraries = %+v, %v", libs, err)
	}
	if f.saw("GET /api/servers/m1/shared_servers") {
		t.Fatal("reading the server listed its shares")
	}
	// An unselected server is refused before anything is dialed.
	empty := NewProvider(p.client, "cid-1", "tok-SECRET", "")
	if _, err := empty.SystemInfo(context.Background()); err == nil {
		t.Fatal("no machine id accepted")
	}
}

func TestProviderCreateUserSendsTheInvite(t *testing.T) {
	f, p := newTestProvider(t)
	if _, err := p.CreateUser(context.Background(), "new@example.com", "a-password", nil); err == nil || f.count() != 0 {
		t.Fatalf("a password was accepted: err=%v requests=%d", err, f.count())
	}
	if _, err := p.CreateUser(context.Background(), "not-an-email", "", nil); !errors.Is(err, mediaserver.ErrInvalidName) {
		t.Fatalf("invalid identity err = %v", err)
	}
	if _, err := p.CreateUser(context.Background(), "new@example.com", "", []string{"101", "nope"}); err == nil {
		t.Fatal("a non-numeric library id was accepted")
	}
	if f.count() != 0 {
		t.Fatalf("refusals dialed plex.tv %d times", f.count())
	}

	u, err := p.CreateUser(context.Background(), " New@Example.com ", "", []string{"101", "102"})
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != "new@example.com" || !u.Pending {
		t.Fatalf("created = %+v", u)
	}
	if f.invited["machineIdentifier"] != "m1" || f.invited["invitedEmail"] != "new@example.com" {
		t.Fatalf("invite payload = %+v", f.invited)
	}
	ids, _ := f.invited["librarySectionIds"].([]any)
	if len(ids) != 2 || ids[0] != float64(101) {
		t.Fatalf("librarySectionIds = %+v", f.invited["librarySectionIds"])
	}
	// Empty selection shares everything: the list is sent, and empty.
	if _, err := p.CreateUser(context.Background(), "all@example.com", "", nil); err != nil {
		t.Fatal(err)
	}
	if ids, ok := f.invited["librarySectionIds"].([]any); !ok || len(ids) != 0 {
		t.Fatalf("empty selection sent %+v", f.invited["librarySectionIds"])
	}
}

func TestProviderCreateUserAnswersAnAlreadySharedAddressWithTheShare(t *testing.T) {
	f, p := newTestProvider(t)
	f.inviteStatus = http.StatusUnprocessableEntity
	u, err := p.CreateUser(context.Background(), "bob@example.com", "", nil)
	if err != nil || u.ID != "bob@example.com" || u.Pending {
		t.Fatalf("already shared = %+v, %v", u, err)
	}
	// 422 for an address plex.tv then does not list (the owner's own) is a
	// real refusal.
	if _, err := p.CreateUser(context.Background(), "owner@example.com", "", nil); !errors.Is(err, mediaserver.ErrUserExists) {
		t.Fatalf("unlisted 422 err = %v, want ErrUserExists", err)
	}
}

func TestProviderSetDisabledRemovesShareOrCancelsInvite(t *testing.T) {
	f, p := newTestProvider(t)
	if err := p.SetDisabled(context.Background(), "bob@example.com", false); err != nil || f.count() != 0 {
		t.Fatalf("re-enable dialed plex.tv: err=%v requests=%d", err, f.count())
	}
	if err := p.SetDisabled(context.Background(), "BOB@example.com", true); err != nil {
		t.Fatal(err)
	}
	if !f.saw("DELETE /api/servers/m1/shared_servers/501") {
		t.Fatalf("share not removed: %v", f.requests)
	}
	if err := p.SetDisabled(context.Background(), "dave@example.com", true); err != nil {
		t.Fatal(err)
	}
	if !f.saw("DELETE /api/invites/requested/Dave@Example.com?friend=0&home=0&server=1") {
		t.Fatalf("invite not cancelled: %v", f.requests)
	}
	if err := p.SetDisabled(context.Background(), "nobody@example.com", true); !errors.Is(err, mediaserver.ErrUserNotFound) {
		t.Fatalf("unknown err = %v", err)
	}
	if err := p.DeleteUser(context.Background(), "nobody@example.com"); err != nil {
		t.Fatalf("DeleteUser of a gone share = %v, want nil", err)
	}
}

func TestProviderSetLibrariesUpdatesOnlyAnExistingShare(t *testing.T) {
	f, p := newTestProvider(t)
	if err := p.SetLibraries(context.Background(), "bob@example.com", []string{"102"}); err != nil {
		t.Fatal(err)
	}
	if !f.saw("PUT /api/servers/m1/shared_servers/501") {
		t.Fatalf("share not updated: %v", f.requests)
	}
	shared, _ := f.updated["shared_server"].(map[string]any)
	ids, _ := shared["library_section_ids"].([]any)
	if f.updated["server_id"] != "m1" || len(ids) != 1 || ids[0] != float64(102) {
		t.Fatalf("update payload = %+v", f.updated)
	}
	before := f.count()
	if err := p.SetLibraries(context.Background(), "dave@example.com", []string{"101"}); err != nil {
		t.Fatal(err)
	}
	if err := p.SetLibraries(context.Background(), "nobody@example.com", []string{"101"}); err != nil {
		t.Fatal(err)
	}
	if f.saw("PUT /api/servers/m1/shared_servers/9") || f.saw("PUT /api/invites") {
		t.Fatalf("a pending invite or a missing share was written: %v", f.requests[before:])
	}
}

func TestProviderErrorsNeverCarryTheToken(t *testing.T) {
	f, c := newFakePlexTV(t)
	p := NewProvider(c, "cid-1", "wrong-token", "m1")
	_, err := p.Users(context.Background())
	if err == nil {
		t.Fatal("wrong token accepted")
	}
	for _, secret := range []string{"wrong-token", "tok-SECRET"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error carries the token: %v", err)
		}
	}
	_ = f
}
