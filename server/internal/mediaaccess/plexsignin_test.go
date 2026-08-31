package mediaaccess

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/instance"
)

const (
	ownerToken = "owner-token-SENTINEL"
	userToken  = "user-token-SENTINEL"
)

// fakePlexTV is the slice of plex.tv a sign-in touches: the PIN flow (approved
// with whichever token the test says), the account behind each token, the
// sign-out, and one owned server with its shares and sent invites for the
// real Plex provider to read.
type fakePlexTV struct {
	mu        sync.Mutex
	t         *testing.T
	approved  string // the token the PIN yields once approved
	shares    string // XML SharedServer elements
	requests  []string
	invited   []string
	signedOut []string
}

func newFakePlexTV(t *testing.T) (*fakePlexTV, *httptest.Server) {
	t.Helper()
	f := &fakePlexTV{t: t}
	accounts := map[string]map[string]any{
		ownerToken: {"id": 1, "uuid": "u-owner", "username": "cantina-owner", "email": "Owner@Example.com", "title": "Owner"},
		userToken:  {"id": 2, "uuid": "u-rey", "username": "rey", "email": "Rey@Example.com", "title": "Rey"},
	}
	record := func(r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/pins", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if r.Header.Get("X-Plex-Client-Identifier") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": 777, "code": "WXYZ", "authToken": nil})
	})
	mux.HandleFunc("/api/v2/pins/", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		f.mu.Lock()
		approved := f.approved
		f.mu.Unlock()
		body := map[string]any{"id": 777, "code": "WXYZ", "authToken": nil}
		if approved != "" {
			body["authToken"] = approved
		}
		json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("/api/v2/user", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		acct := accounts[r.Header.Get("X-Plex-Token")]
		if acct == nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(acct)
	})
	// plex.tv registers a device per client identifier when a PIN is
	// approved; the sign-out removes that record, which is what drops the
	// entry from Authorized Devices and kills the token.
	mux.HandleFunc("/devices.xml", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if accounts[r.Header.Get("X-Plex-Token")] == nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><MediaContainer size="2"><Device id="555" name="Chrome" clientIdentifier="browser-cid"/><Device id="777001" name="Cantinarr" clientIdentifier="` + r.Header.Get("X-Plex-Client-Identifier") + `"/></MediaContainer>`))
	})
	mux.HandleFunc("/devices/777001.xml", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if r.Method != http.MethodDelete || accounts[r.Header.Get("X-Plex-Token")] == nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		f.signedOut = append(f.signedOut, r.Header.Get("X-Plex-Token"))
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/v2/users/signout", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		f.t.Errorf("the plain sign-out was used although plex.tv listed a device for the client")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/v2/shared_servers", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if r.Header.Get("X-Plex-Token") != ownerToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body struct {
			Email string `json:"invitedEmail"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.invited = append(f.invited, body.Email)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/api/servers/m1/shared_servers", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.Header().Set("Content-Type", "application/xml")
		f.mu.Lock()
		shares := f.shares
		f.mu.Unlock()
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><MediaContainer size="1">` + shares + `</MediaContainer>`))
	})
	mux.HandleFunc("/api/invites/requested", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><MediaContainer size="0"></MediaContainer>`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		f.t.Errorf("unexpected plex.tv request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakePlexTV) approve(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.approved = token
}

func (f *fakePlexTV) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

// plexServer creates a real Plex instance against the fake plex.tv, served by
// the real provider (rebuilt per call, so a recorded owner is seen at once).
// The owner is deliberately not recorded: that is what the sign-in backfills.
func (e *env) plexServer(name, url string) string {
	e.t.Helper()
	inst := &instance.Instance{ServiceType: "plex", Name: name, URL: url, APIKey: ownerToken,
		MediaServerConfig: instance.MediaServerConfig{PublicAddress: instance.PlexPublicAddress, MachineIdentifier: "m1", ClientID: "cid-1", LibraryIDs: []string{}}}
	if err := e.store.Create(inst); err != nil {
		e.t.Fatalf("create plex: %v", err)
	}
	e.real[inst.ID] = true
	return inst.ID
}

func signInEnv(t *testing.T) (*env, *fakePlexTV, string) {
	t.Helper()
	e := newEnv(t)
	f, srv := newFakePlexTV(t)
	e.svc.SetPlexBaseURL(srv.URL)
	return e, f, e.plexServer("Den Plex", srv.URL)
}

func TestPlexSignInBeginsAndWaitsForApproval(t *testing.T) {
	e, f, _ := signInEnv(t)
	rey, finn := e.user("rey"), e.user("finn")
	ctx := context.Background()

	start, err := e.svc.PlexSignInBegin(ctx, rey)
	if err != nil {
		t.Fatal(err)
	}
	if start.PinID != 777 || start.Code != "WXYZ" || !strings.HasPrefix(start.URL, "https://app.plex.tv/auth#?") || !strings.Contains(start.URL, "code=WXYZ") {
		t.Fatalf("start = %+v", start)
	}
	result, err := e.svc.PlexSignInCheck(ctx, rey, 777, "rey")
	if err != nil || result.Linked {
		t.Fatalf("check before approval = %+v, %v", result, err)
	}
	if e.email(rey) != "" {
		t.Fatal("an unapproved sign-in stored an email")
	}
	// Somebody else's pin, an unknown pin, an expired pin: all the same.
	if _, err := e.svc.PlexSignInCheck(ctx, finn, 777, "finn"); !errors.Is(err, ErrPinNotFound) {
		t.Fatalf("another user's pin = %v, want ErrPinNotFound", err)
	}
	if _, err := e.svc.PlexSignInCheck(ctx, rey, 778, "rey"); !errors.Is(err, ErrPinNotFound) {
		t.Fatalf("unknown pin = %v, want ErrPinNotFound", err)
	}
	e.svc.signIns.put(779, &plexSignIn{userID: rey, clientID: "c", expires: time.Now().Add(-time.Second)})
	if _, err := e.svc.PlexSignInCheck(ctx, rey, 779, "rey"); !errors.Is(err, ErrPinNotFound) {
		t.Fatalf("expired pin = %v, want ErrPinNotFound", err)
	}
	if f.count("GET /api/v2/user") != 0 {
		t.Fatal("an unapproved pin read an account")
	}
}

func TestPlexSignInLinksTheVerifiedEmailAndSendsTheInvite(t *testing.T) {
	e, f, plex := signInEnv(t)
	pushes := e.notifier()
	rey := e.user("rey")
	e.grantType(rey, "plex", plex)
	ctx := context.Background()

	if _, err := e.svc.PlexSignInBegin(ctx, rey); err != nil {
		t.Fatal(err)
	}
	f.approve(userToken)
	result, err := e.svc.PlexSignInCheck(ctx, rey, 777, "rey")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Linked || result.Email != "rey@example.com" || result.Username != "rey" || result.InviteState != "sent" {
		t.Fatalf("result = %+v", result)
	}
	if e.email(rey) != "rey@example.com" {
		t.Fatalf("stored email = %q", e.email(rey))
	}
	row := e.row(rey, plex)
	if row == nil || row.RemoteUserID != "rey@example.com" || !row.CreatedByCantinarr {
		t.Fatalf("row = %+v", row)
	}
	if len(f.invited) != 1 || f.invited[0] != "rey@example.com" {
		t.Fatalf("invited = %v", f.invited)
	}
	if len(f.signedOut) != 1 || f.signedOut[0] != userToken {
		t.Fatalf("signed out = %v, want the user's token once", f.signedOut)
	}
	if pushes.userEvents(rey, eventInviteSent) != 1 {
		t.Fatal("no check-your-email push")
	}
	if state, ok := pushes.lastAdminState(); !ok || state != "sent" {
		t.Fatalf("admin state = %q, %v", state, ok)
	}
	// The instance learned its owner on the way.
	inst, _ := e.store.Get(plex)
	if inst.MediaServerConfig.PlexOwnerEmail != "Owner@Example.com" || inst.MediaServerConfig.PlexOwnerID != 1 {
		t.Fatalf("owner not backfilled: %+v", inst.MediaServerConfig)
	}
	// Every later poll gets the same answer and sends nothing again.
	again, err := e.svc.PlexSignInCheck(ctx, rey, 777, "rey")
	if err != nil || again != result {
		t.Fatalf("second check = %+v, %v", again, err)
	}
	if len(f.invited) != 1 || f.count("GET /api/v2/user") != 2 {
		t.Fatalf("second check did work again: invited=%v user reads=%d", f.invited, f.count("GET /api/v2/user"))
	}
	// The token was proof, never stored or logged.
	var stored int
	if err := e.db.QueryRow("SELECT COUNT(*) FROM user_media_server_accounts WHERE remote_user_id LIKE '%SENTINEL%' OR remote_username LIKE '%SENTINEL%'").Scan(&stored); err != nil || stored != 0 {
		t.Fatalf("token reached the database (%d, %v)", stored, err)
	}
	var email string
	_ = e.db.QueryRow("SELECT plex_email FROM users WHERE id = ?", rey).Scan(&email)
	if strings.Contains(email, "SENTINEL") || strings.Contains(e.logs.String(), userToken) || strings.Contains(e.logs.String(), ownerToken) {
		t.Fatal("a token leaked into the users row or the logs")
	}
}

func TestPlexSignInAdoptsAnExistingShareAndTellsAdminsWhenUngranted(t *testing.T) {
	e, f, plex := signInEnv(t)
	pushes := e.notifier()
	rey, finn := e.user("rey"), e.user("finn")
	e.grantType(rey, "plex", plex)
	f.shares = `<SharedServer id="501" username="rey" email="rey@example.com" userID="2" acceptedAt="1700000000" invitedAt="1699999999"><Section id="101" shared="1"/></SharedServer>`
	ctx := context.Background()

	if _, err := e.svc.PlexSignInBegin(ctx, rey); err != nil {
		t.Fatal(err)
	}
	f.approve(userToken)
	result, err := e.svc.PlexSignInCheck(ctx, rey, 777, "rey")
	if err != nil || !result.Linked || result.InviteState != "adopted" {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if row := e.row(rey, plex); row == nil || row.CreatedByCantinarr {
		t.Fatalf("row = %+v, want an adopted (linked) row", row)
	}
	if len(f.invited) != 0 || pushes.userEvents(rey, eventInviteSent) != 0 {
		t.Fatal("an existing share was invited again")
	}

	// finn holds no Plex grant: the email is stored, the admins are told,
	// nothing is shared.
	if _, err := e.svc.PlexSignInBegin(ctx, finn); err != nil {
		t.Fatal(err)
	}
	result, err = e.svc.PlexSignInCheck(ctx, finn, 777, "finn")
	if err != nil || !result.Linked || result.InviteState != "" {
		t.Fatalf("ungranted result = %+v, %v", result, err)
	}
	if e.email(finn) != "rey@example.com" {
		t.Fatalf("finn's stored email = %q", e.email(finn))
	}
	if state, ok := pushes.lastAdminState(); !ok || state != "" {
		t.Fatalf("admin state = %q, %v; want needs-an-admin", state, ok)
	}
	if e.row(finn, plex) != nil || len(f.invited) != 0 {
		t.Fatal("an ungranted sign-in shared something")
	}
}

func TestPlexSignInWithAnAccountAnotherUserHoldsSaysClaimed(t *testing.T) {
	e, f, plex := signInEnv(t)
	pushes := e.notifier()
	rey, finn := e.user("rey"), e.user("finn")
	e.grantType(rey, "plex", plex)
	e.grantType(finn, "plex", plex)
	f.shares = `<SharedServer id="501" username="rey" email="rey@example.com" userID="2" acceptedAt="1700000000" invitedAt="1699999999"><Section id="101" shared="1"/></SharedServer>`
	ctx := context.Background()
	// rey's share is rey's row.
	if _, err := e.svc.RequestInvite(ctx, rey, plex, "rey@example.com"); err != nil {
		t.Fatal(err)
	}
	// finn approves the PIN with rey's Plex account (the wrong one).
	if _, err := e.svc.PlexSignInBegin(ctx, finn); err != nil {
		t.Fatal(err)
	}
	f.approve(userToken)
	result, err := e.svc.PlexSignInCheck(ctx, finn, 777, "finn")
	if err != nil || !result.Linked || result.InviteState != "claimed" {
		t.Fatalf("result = %+v, %v; want claimed", result, err)
	}
	if e.row(finn, plex) != nil || e.row(rey, plex) == nil {
		t.Fatal("the claimed sign-in moved a row")
	}
	if len(f.invited) != 0 {
		t.Fatalf("invited = %v", f.invited)
	}
	if state, ok := pushes.lastAdminState(); !ok || state != "claimed" {
		t.Fatalf("admin state = %q, %v; want claimed", state, ok)
	}
}

func TestPlexSignInRecognisesTheOwner(t *testing.T) {
	e, f, plex := signInEnv(t)
	pushes := e.notifier()
	julian := e.user("julian")
	e.grantType(julian, "plex", plex)
	ctx := context.Background()

	if _, err := e.svc.PlexSignInBegin(ctx, julian); err != nil {
		t.Fatal(err)
	}
	f.approve(ownerToken)
	result, err := e.svc.PlexSignInCheck(ctx, julian, 777, "julian")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Linked || result.Email != "owner@example.com" || result.Username != "cantina-owner" || result.InviteState != "adopted" {
		t.Fatalf("result = %+v", result)
	}
	if len(f.invited) != 0 {
		t.Fatalf("the owner was invited to their own server: %v", f.invited)
	}
	row := e.row(julian, plex)
	if row == nil || row.RemoteUserID != "owner@example.com" || row.CreatedByCantinarr {
		t.Fatalf("row = %+v", row)
	}
	views, err := e.svc.ListForUser(ctx, julian)
	if err != nil || len(views) != 1 || views[0].Account == nil || !views[0].Account.Administrator || views[0].Account.Pending || !views[0].Account.Verified {
		t.Fatalf("owner's guide = %+v, %v", views, err)
	}
	if pushes.userEvents(julian, eventInviteSent) != 0 {
		t.Fatal("the owner was told to check their email")
	}
	// Revoking and restoring the grant never touches plex.tv for the owner.
	before := f.count("DELETE ")
	e.grantType(julian, "plex")
	e.svc.OnGrantsChanged([]int64{julian})
	if !e.row(julian, plex).DisabledAt.Valid || f.count("DELETE ") != before {
		t.Fatal("revoking the owner's grant did not stamp the row, or dialed plex.tv")
	}
	e.grantType(julian, "plex", plex)
	e.svc.OnGrantsChanged([]int64{julian})
	if e.row(julian, plex).DisabledAt.Valid || len(f.invited) != 0 {
		t.Fatal("re-granting the owner cleared nothing, or sent an invite")
	}
}
