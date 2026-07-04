package plex

import (
	"context"
	"errors"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

type inviteCall struct {
	machineID  string
	email      string
	sectionIDs []int64
}

// fakeAPI is a canned plex.tv: tests set the fields they need.
type fakeAPI struct {
	pin       Pin
	checked   Pin
	account   Account
	servers   []Server
	libraries []Library
	inviteErr error
	invites   []inviteCall
}

func (f *fakeAPI) CreatePin(_ context.Context, _ string) (*Pin, error) { p := f.pin; return &p, nil }
func (f *fakeAPI) CheckPin(_ context.Context, _ string, _ int64) (*Pin, error) {
	p := f.checked
	return &p, nil
}
func (f *fakeAPI) AuthURL(clientID, code string) string { return "https://app.plex.tv/auth#?code=" + code }
func (f *fakeAPI) GetUser(_ context.Context, _, _ string) (*Account, error) {
	a := f.account
	return &a, nil
}
func (f *fakeAPI) ListServers(_ context.Context, _, _ string) ([]Server, error) {
	return f.servers, nil
}
func (f *fakeAPI) ListLibraries(_ context.Context, _, _, _ string) ([]Library, error) {
	return f.libraries, nil
}
func (f *fakeAPI) InviteEmail(_ context.Context, _, _, machineID, email string, sectionIDs []int64) error {
	f.invites = append(f.invites, inviteCall{machineID: machineID, email: email, sectionIDs: sectionIDs})
	return f.inviteErr
}

type notifyCall struct {
	userID    int64
	eventType string
	data      map[string]interface{}
}

type fakeNotifier struct {
	userCalls  []notifyCall
	adminCalls []notifyCall
}

func (f *fakeNotifier) NotifyUser(userID int64, eventType string, data map[string]interface{}) {
	f.userCalls = append(f.userCalls, notifyCall{userID: userID, eventType: eventType, data: data})
}
func (f *fakeNotifier) NotifyAdmins(eventType string, data map[string]interface{}) {
	f.adminCalls = append(f.adminCalls, notifyCall{eventType: eventType, data: data})
}

func newTestService(t *testing.T) (*Service, *fakeAPI, *fakeNotifier) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	cipher, err := secrets.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	api := &fakeAPI{}
	notif := &fakeNotifier{}
	return NewService(database, cipher, api, notif, nil), api, notif
}

// link walks the PIN flow so the service holds a stored (encrypted) token.
func link(t *testing.T, svc *Service, api *fakeAPI) {
	t.Helper()
	api.pin = Pin{ID: 1, Code: "abcd"}
	api.checked = Pin{ID: 1, Code: "abcd", AuthToken: "secret-token"}
	api.account = Account{Username: "captain"}
	if _, _, _, err := svc.BeginLink(context.Background()); err != nil {
		t.Fatalf("begin link: %v", err)
	}
	linked, account, err := svc.CheckLink(context.Background(), 1)
	if err != nil || !linked {
		t.Fatalf("check link: linked=%v err=%v", linked, err)
	}
	if account != "captain" {
		t.Fatalf("account = %q", account)
	}
}

func seedUser(t *testing.T, svc *Service, id int64, username, plexEmail string) {
	t.Helper()
	if _, err := svc.db.Exec(
		"INSERT INTO users (id, username, password_hash, role, plex_email) VALUES (?, ?, '', 'user', ?)",
		id, username, plexEmail,
	); err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

func TestLinkFlowStoresEncryptedTokenAndStatus(t *testing.T) {
	svc, api, _ := newTestService(t)

	// Pending PIN: not linked yet.
	api.pin = Pin{ID: 1, Code: "abcd"}
	api.checked = Pin{ID: 1, Code: "abcd"}
	if _, _, _, err := svc.BeginLink(context.Background()); err != nil {
		t.Fatalf("begin link: %v", err)
	}
	linked, _, err := svc.CheckLink(context.Background(), 1)
	if err != nil || linked {
		t.Fatalf("pending pin: linked=%v err=%v", linked, err)
	}

	link(t, svc, api)

	// The stored row must be ciphertext, but token() must round-trip.
	var stored string
	if err := svc.db.QueryRow("SELECT value FROM settings WHERE key = ?", settingToken).Scan(&stored); err != nil {
		t.Fatalf("load stored token: %v", err)
	}
	if stored == "secret-token" {
		t.Fatal("token stored in plaintext")
	}
	if tok, ok := svc.token(); !ok || tok != "secret-token" {
		t.Fatalf("token() = %q, %v", tok, ok)
	}

	st := svc.Status()
	if !st.Linked || st.Account != "captain" || st.Configured {
		t.Fatalf("status = %+v", st)
	}

	// Unlink forgets the token but keeps the stable client id.
	if err := svc.Unlink(); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if st := svc.Status(); st.Linked {
		t.Fatalf("still linked after unlink: %+v", st)
	}
	if _, ok := svc.getSetting(settingClientID); !ok {
		t.Fatal("client id should survive unlink")
	}
}

func TestUpdateSettingsRequiresLinkAndServer(t *testing.T) {
	svc, api, _ := newTestService(t)

	if err := svc.UpdateSettings("m1", "Cantina", []int64{1}, true); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("expected ErrNotLinked, got %v", err)
	}

	link(t, svc, api)
	if err := svc.UpdateSettings("", "", nil, false); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}

	if err := svc.UpdateSettings("m1", "Cantina", []int64{101, 102}, true); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	st := svc.Status()
	if !st.Configured || !st.AutoInvite || st.ServerName != "Cantina" || st.MachineIdentifier != "m1" {
		t.Fatalf("status = %+v", st)
	}
	if len(st.LibrarySectionIDs) != 2 || st.LibrarySectionIDs[0] != 101 {
		t.Fatalf("library ids = %v", st.LibrarySectionIDs)
	}
}

func TestInviteUserSharesStampsAndNotifies(t *testing.T) {
	svc, api, notif := newTestService(t)
	seedUser(t, svc, 7, "bob", "bob@example.com")
	seedUser(t, svc, 8, "nomail", "")

	if _, err := svc.InviteUser(context.Background(), 7); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("expected ErrNotLinked, got %v", err)
	}

	link(t, svc, api)
	if err := svc.UpdateSettings("m1", "Cantina", []int64{101}, false); err != nil {
		t.Fatalf("update settings: %v", err)
	}

	outcome, err := svc.InviteUser(context.Background(), 7)
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	if outcome.AlreadyShared || outcome.Email != "bob@example.com" {
		t.Fatalf("outcome = %+v", outcome)
	}
	if len(api.invites) != 1 || api.invites[0].machineID != "m1" || api.invites[0].email != "bob@example.com" {
		t.Fatalf("invites = %+v", api.invites)
	}

	var invitedAt any
	if err := svc.db.QueryRow("SELECT plex_invited_at FROM users WHERE id = 7").Scan(&invitedAt); err != nil {
		t.Fatalf("load invited_at: %v", err)
	}
	if invitedAt == nil {
		t.Fatal("plex_invited_at not stamped")
	}

	if len(notif.userCalls) != 1 || notif.userCalls[0].userID != 7 || notif.userCalls[0].eventType != "plex_invite_sent" {
		t.Fatalf("user pushes = %+v", notif.userCalls)
	}

	// No shared email → 409 material, nothing sent.
	if _, err := svc.InviteUser(context.Background(), 8); !errors.Is(err, ErrNoEmail) {
		t.Fatalf("expected ErrNoEmail, got %v", err)
	}

	// Duplicate share: soft success, but no "check your email" push.
	api.inviteErr = ErrAlreadyShared
	outcome, err = svc.InviteUser(context.Background(), 7)
	if err != nil || !outcome.AlreadyShared {
		t.Fatalf("duplicate share: outcome=%+v err=%v", outcome, err)
	}
	if len(notif.userCalls) != 1 {
		t.Fatalf("duplicate share should not re-push, got %+v", notif.userCalls)
	}
}

func TestHandleAccessRequestStates(t *testing.T) {
	svc, api, notif := newTestService(t)
	seedUser(t, svc, 7, "bob", "bob@example.com")

	// Not configured: admins are told a manual invite is needed.
	svc.handleAccessRequest(7, "bob")
	if len(notif.adminCalls) != 1 || notif.adminCalls[0].data["invite_state"] != "" {
		t.Fatalf("admin calls = %+v", notif.adminCalls)
	}

	// Configured with auto-invite: the invite goes out and admins hear "sent".
	link(t, svc, api)
	if err := svc.UpdateSettings("m1", "Cantina", nil, true); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	svc.handleAccessRequest(7, "bob")
	if len(api.invites) != 1 {
		t.Fatalf("expected auto-invite, got %+v", api.invites)
	}
	if got := notif.adminCalls[1].data["invite_state"]; got != "sent" {
		t.Fatalf("invite_state = %v", got)
	}

	// Upstream failure: admins hear "failed" so they retry manually.
	api.inviteErr = errors.New("plex.tv down")
	svc.handleAccessRequest(7, "bob")
	if got := notif.adminCalls[2].data["invite_state"]; got != "failed" {
		t.Fatalf("invite_state = %v", got)
	}

	// Auto-invite off: no attempt, plain "waiting for an invite".
	api.inviteErr = nil
	if err := svc.UpdateSettings("m1", "Cantina", nil, false); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	before := len(api.invites)
	svc.handleAccessRequest(7, "bob")
	if len(api.invites) != before {
		t.Fatal("auto-invite ran while disabled")
	}
	if got := notif.adminCalls[3].data["invite_state"]; got != "" {
		t.Fatalf("invite_state = %v", got)
	}
}
