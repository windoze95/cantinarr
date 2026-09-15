package appletv

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

const testCredential = "synthetic-pairing-credential"
const testTitle = `{"media_type":"tv","tmdb_id":12}`

type fakeRunner struct {
	available bool
	commands  []Command
	start     func(Command) Conversation
}

func (f *fakeRunner) Available() bool { return f.available }
func (f *fakeRunner) Start(_ context.Context, command Command) (Conversation, error) {
	f.commands = append(f.commands, command)
	return f.start(command), nil
}

type fakeConversation struct {
	replies []Reply
	sent    []any
	before  func(int)
	reads   int
	closed  bool
	err     error
}

func (f *fakeConversation) Receive() (Reply, error) {
	if f.before != nil {
		f.before(f.reads)
	}
	f.reads++
	if f.err != nil {
		return Reply{}, f.err
	}
	if len(f.replies) == 0 {
		return Reply{}, errors.New("unexpected read")
	}
	reply := f.replies[0]
	f.replies = f.replies[1:]
	return reply, nil
}
func (f *fakeConversation) Send(value any) error { f.sent = append(f.sent, value); return nil }
func (f *fakeConversation) Close()               { f.closed = true }

type testEnv struct {
	t       *testing.T
	db      *sql.DB
	h       *Handler
	r       http.Handler
	runner  *fakeRunner
	revoked bool
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	cipher, err := secrets.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	e := &testEnv{t: t, db: database, runner: &fakeRunner{available: true}}
	e.exec(`INSERT INTO users(id,username,password_hash,role) VALUES(1,'admin','','admin'),(2,'adult','','user'),(3,'other','','user'),(4,'child','','user')`)
	e.exec(`INSERT INTO user_content_policies(user_id,max_movie_rating,max_tv_rating) VALUES(4,'PG','TV-PG')`)
	e.h = NewHandler(database, cipher, e.runner, func(ctx context.Context, id int64, device string, permission auth.Permission) error {
		if e.revoked || device == "revoked" {
			return auth.ErrPermissionDenied
		}
		var role string
		if err := database.QueryRowContext(ctx, `SELECT role FROM users WHERE id=?`, id).Scan(&role); err != nil {
			return err
		}
		if !auth.HasPermission(role, permission) {
			return auth.ErrPermissionDenied
		}
		return nil
	}, func(context.Context, int64, string, int64) error { return nil })
	t.Cleanup(e.h.Close)
	r := chi.NewRouter()
	r.Route("/api/apple-tvs", e.h.Register)
	e.r = r
	e.runner.start = func(Command) Conversation {
		return &fakeConversation{replies: []Reply{{State: "ready"}, {State: "sent"}}}
	}
	sealed, _ := cipher.Encrypt(testCredential)
	e.exec(`INSERT INTO apple_tv_devices(id,name,address,identifier,credentials) VALUES('tv','Living room','192.0.2.10','stable',?)`, sealed)
	e.exec(`INSERT INTO apple_tv_grants(tv_id,user_id) VALUES('tv',2)`)
	return e
}

func (e *testEnv) exec(query string, args ...any) {
	e.t.Helper()
	if _, err := e.db.Exec(query, args...); err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) request(user int64, device, method, path, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, "/api/apple-tvs"+path, strings.NewReader(body))
	if user != 0 {
		req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: user, DeviceID: device, Role: auth.RoleAdmin}))
	}
	rec := httptest.NewRecorder()
	e.r.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), testCredential) || strings.Contains(rec.Body.String(), "enc:v1:") {
		e.t.Fatal("response exposed pairing credentials")
	}
	return rec
}

func expectStatus(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d want %d: %s", rec.Code, status, rec.Body.String())
	}
}

func TestTVListIsLiveScopedAndSecretFree(t *testing.T) {
	e := newTestEnv(t)
	for _, path := range []string{"", "/"} {
		rec := e.request(1, "session", "GET", path, "")
		expectStatus(t, rec, 200)
		if !strings.Contains(rec.Body.String(), "192.0.2.10") {
			t.Fatal("admin missing address")
		}
	}
	rec := e.request(2, "session", "GET", "", "")
	expectStatus(t, rec, 200)
	if strings.Contains(rec.Body.String(), "192.0.2.10") || strings.Contains(rec.Body.String(), "stable") {
		t.Fatal("non-admin received device internals")
	}
	rec = e.request(3, "session", "GET", "", "")
	if !strings.Contains(rec.Body.String(), `"devices":[]`) {
		t.Fatal("ungranted TV listed")
	}
	expectStatus(t, e.request(4, "session", "GET", "", ""), 403)
	expectStatus(t, e.request(0, "session", "GET", "", ""), 401)
	e.revoked = true
	expectStatus(t, e.request(1, "session", "GET", "", ""), 403)
}

func TestManagementRequiresLiveAdminAndRejectsChildGrants(t *testing.T) {
	e := newTestEnv(t)
	for _, route := range []struct{ method, path, body string }{
		{"POST", "/discover", `{}`}, {"POST", "/pairings", `{}`}, {"PATCH", "/tv", `{}`},
		{"DELETE", "/tv", ""}, {"GET", "/tv/grants", ""}, {"PUT", "/tv/grants", `{"user_ids":[3]}`},
		{"POST", "/tv/check", ""},
	} {
		expectStatus(t, e.request(2, "session", route.method, route.path, route.body), 403)
	}
	for _, body := range []string{`{"user_ids":[4]}`, `{"user_ids":[999]}`} {
		expectStatus(t, e.request(1, "session", "PUT", "/tv/grants", body), 400)
	}
	// Rejected replacement is atomic: the existing adult still has access.
	expectStatus(t, e.request(2, "session", "POST", "/tv/open", testTitle), 200)
	e.exec(`UPDATE users SET role='user' WHERE id=1`)
	expectStatus(t, e.request(1, "session", "GET", "/tv/grants", ""), 403)
}

func TestOpenUsesStrictIdentityAndReauthorizesAfterNetworkWait(t *testing.T) {
	for _, scenario := range []string{"grant revoked", "child account", "session revoked", "tv forgotten", "address changed", "title lost", "metadata unreadable"} {
		t.Run(scenario, func(t *testing.T) {
			e := newTestEnv(t)
			conversation := &fakeConversation{replies: []Reply{{State: "ready"}, {State: "sent"}}}
			e.runner.start = func(Command) Conversation { return conversation }
			conversation.before = func(read int) {
				if read != 0 {
					return
				}
				switch scenario {
				case "grant revoked":
					e.exec(`DELETE FROM apple_tv_grants WHERE user_id=2`)
				case "child account":
					e.exec(`INSERT INTO user_content_policies(user_id,max_movie_rating,max_tv_rating) VALUES(2,'PG','TV-PG')`)
				case "session revoked":
					e.revoked = true
				case "tv forgotten":
					e.exec(`DELETE FROM apple_tv_devices WHERE id='tv'`)
				case "address changed":
					e.exec(`UPDATE apple_tv_devices SET revision=revision+1 WHERE id='tv'`)
				case "title lost":
					e.h.title = func(context.Context, int64, string, int64) error { return ErrTitleUnavailable }
				case "metadata unreadable":
					e.h.title = func(context.Context, int64, string, int64) error { return ErrLookupUnavailable }
				}
			}
			rec := e.request(2, "session", "POST", "/tv/open", testTitle)
			if rec.Code == 200 || len(conversation.sent) != 0 || !conversation.closed {
				t.Fatalf("action escaped revocation: %s", rec.Body.String())
			}
		})
	}
}

func TestNoTVActionForUnauthorizedOrUnverifiedTitle(t *testing.T) {
	e := newTestEnv(t)
	for _, body := range []string{`{"media_type":"tv","tmdb_id":0}`, `{"media_type":"book","tmdb_id":1}`, `{"media_type":"tv","tmdb_id":1,"url":"infuse://series/2"}`, `{"media_type":"tv","tmdb_id":true}`, `{} {}`} {
		expectStatus(t, e.request(2, "session", "POST", "/tv/open", body), 400)
	}
	unknown := e.request(2, "session", "POST", "/unknown/open", testTitle)
	ungranted := e.request(3, "session", "POST", "/tv/open", testTitle)
	if unknown.Body.String() != ungranted.Body.String() {
		t.Fatal("TV existence oracle")
	}
	e.h.title = func(context.Context, int64, string, int64) error { return ErrTitleUnavailable }
	expectStatus(t, e.request(2, "session", "POST", "/tv/open", testTitle), 403)
	if len(e.runner.commands) != 0 {
		t.Fatal("worker contacted before authorization")
	}
}

func TestConfirmationIsExplicitSingleUseSessionBoundAndExpiresBeforeCommit(t *testing.T) {
	e := newTestEnv(t)
	rec := e.request(2, "session", "POST", "/tv/open", testTitle)
	expectStatus(t, rec, 200)
	var result struct {
		ID string `json:"confirmation_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	body := `{"confirmation_id":"` + result.ID + `"}`
	if len(e.runner.commands) != 1 || e.runner.commands[0].Action != "open" {
		t.Fatal("open automatically selected")
	}
	expectStatus(t, e.request(2, "different-session", "POST", "/tv/confirm-open", body), 404)
	expectStatus(t, e.request(2, "session", "POST", "/tv/confirm-open", body), 200)
	if len(e.runner.commands) != 2 || e.runner.commands[1].Action != "select" {
		t.Fatal("confirmation did not select")
	}
	expectStatus(t, e.request(2, "session", "POST", "/tv/confirm-open", body), 404)
	// Expiry during connection preparation must also prevent Select.
	pending := confirmation{id: "short", userID: 2, deviceID: "session", revision: 1, title: titleRequest{"tv", 12}, deadline: time.Now().Add(30 * time.Millisecond)}
	e.h.confirmations["tv"] = pending
	conversation := &fakeConversation{replies: []Reply{{State: "ready"}, {State: "sent"}}, before: func(int) { time.Sleep(40 * time.Millisecond) }}
	e.runner.start = func(Command) Conversation { return conversation }
	expectStatus(t, e.request(2, "session", "POST", "/tv/confirm-open", `{"confirmation_id":"short"}`), 404)
	if len(conversation.sent) != 0 {
		t.Fatal("expired confirmation sent Select")
	}
}

func TestBusyTVRejectsInsteadOfQueuingAndWorkerErrorsAreSafe(t *testing.T) {
	e := newTestEnv(t)
	e.h.lock("tv")
	expectStatus(t, e.request(2, "session", "POST", "/tv/open", testTitle), 409)
	e.h.unlock("tv")
	if len(e.runner.commands) != 0 {
		t.Fatal("busy action started worker")
	}
	e.runner.start = func(Command) Conversation { return &fakeConversation{err: errors.New("sensitive upstream details")} }
	rec := e.request(2, "session", "POST", "/tv/open", testTitle)
	expectStatus(t, rec, 503)
	if strings.Contains(rec.Body.String(), "sensitive") {
		t.Fatal("worker error leaked")
	}
	e.runner.available = false
	expectStatus(t, e.request(1, "session", "POST", "/discover", `{}`), 503)
}

func (e *testEnv) beginPairing() (string, *fakeConversation) {
	e.t.Helper()
	device := Device{Name: "Living room", Address: "192.0.2.10", Identifier: "stable"}
	conversation := &fakeConversation{replies: []Reply{{State: "pin_required", Device: device}, {State: "paired", Device: device, Credentials: testCredential}}}
	e.runner.start = func(Command) Conversation { return conversation }
	rec := e.request(1, "session", "POST", "/pairings", `{"name":"Living room","address":"192.0.2.10","identifier":"stable"}`)
	expectStatus(e.t, rec, 201)
	var response struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		e.t.Fatal(err)
	}
	return response.ID, conversation
}

func TestPairingEncryptsDeduplicatesAndPreservesGrants(t *testing.T) {
	e := newTestEnv(t)
	id, conversation := e.beginPairing()
	expectStatus(t, e.request(1, "other-session", "POST", "/pairings/"+id+"/complete", `{"pin":"2468"}`), 404)
	expectStatus(t, e.request(1, "session", "POST", "/pairings/"+id+"/complete", `{"pin":"bad"}`), 400)
	expectStatus(t, e.request(1, "session", "POST", "/pairings/"+id+"/complete", `{"pin":"2468"}`), 201)
	var count int
	var sealed string
	if err := e.db.QueryRow(`SELECT count(*),credentials FROM apple_tv_devices`).Scan(&count, &sealed); err != nil {
		t.Fatal(err)
	}
	plain, err := e.h.cipher.Decrypt(sealed)
	if err != nil || plain != testCredential || sealed == plain || count != 1 {
		t.Fatal("pairing was not encrypted and deduplicated")
	}
	if !conversation.closed {
		t.Fatal("completed worker remains open")
	}
	expectStatus(t, e.request(2, "session", "GET", "", ""), 200)
	e.exec(`DELETE FROM apple_tv_devices WHERE id='tv'`)
	if err := e.db.QueryRow(`SELECT count(*) FROM apple_tv_grants`).Scan(&count); err != nil || count != 0 {
		t.Fatal("forget did not cascade grants")
	}
}

func TestCancelledExpiredOrRevokedPairingNeverPersists(t *testing.T) {
	for _, reason := range []string{"cancelled", "expired", "revoked"} {
		t.Run(reason, func(t *testing.T) {
			e := newTestEnv(t)
			id, conversation := e.beginPairing()
			conversation.before = func(read int) {
				if read != 1 {
					return
				}
				switch reason {
				case "cancelled":
					e.h.dropPairing(id, e.h.pairings[id])
				case "expired":
					e.h.pairings[id].deadline = time.Now().Add(-time.Second)
				case "revoked":
					e.revoked = true
				}
			}
			rec := e.request(1, "session", "POST", "/pairings/"+id+"/complete", `{"pin":"2468"}`)
			if rec.Code == 201 {
				t.Fatal("ended pairing persisted credentials")
			}
			var revision int
			if err := e.db.QueryRow(`SELECT revision FROM apple_tv_devices WHERE id='tv'`).Scan(&revision); err != nil || revision != 1 {
				t.Fatal("pairing updated after cancellation")
			}
		})
	}
}
