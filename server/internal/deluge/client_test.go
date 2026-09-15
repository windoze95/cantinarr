package deluge

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type rpcCall struct {
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
	ID     int64             `json:"id"`
}

// fakeDeluge is a Deluge web UI: /json with a password, session cookies,
// the Connection Manager flow, and canned daemon answers.
type fakeDeluge struct {
	t             *testing.T
	password      string
	connected     bool   // web.connected on the first call
	hosts         string // JSON for web.get_hosts
	version       string
	noGetVersion  bool // Deluge 1.3: daemon.get_version is unknown
	torrents      string
	sessionStatus string
	removeResult  string

	mu        sync.Mutex
	calls     []rpcCall
	sessions  map[string]bool
	nextSess  int
	expireAll bool // drop every session before answering the next call
}

func newFake(t *testing.T) *fakeDeluge {
	return &fakeDeluge{
		t:             t,
		password:      "web-secret",
		connected:     true,
		hosts:         `[]`,
		version:       "2.1.1",
		torrents:      `{}`,
		sessionStatus: `{"payload_download_rate":1500.0,"payload_upload_rate":20}`,
		removeResult:  `true`,
		sessions:      map[string]bool{},
	}
}

func (f *fakeDeluge) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json" {
			f.t.Errorf("path = %s, want /json", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var call rpcCall
		if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
			f.t.Errorf("decode request: %v", err)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, call)
		if f.expireAll {
			f.sessions = map[string]bool{}
			f.expireAll = false
		}
		w.Header().Set("Content-Type", "application/json")
		reply := func(result string) {
			_, _ = io.WriteString(w, `{"result":`+result+`,"error":null,"id":`+fmt.Sprint(call.ID)+`}`)
		}
		fail := func(code int, msg string) {
			_, _ = io.WriteString(w, fmt.Sprintf(`{"result":null,"error":{"message":%q,"code":%d},"id":%d}`, msg, code, call.ID))
		}

		if call.Method == "auth.login" {
			var pw string
			if len(call.Params) > 0 {
				_ = json.Unmarshal(call.Params[0], &pw)
			}
			if pw != f.password {
				reply("false")
				return
			}
			f.nextSess++
			id := fmt.Sprintf("sess-%d", f.nextSess)
			f.sessions[id] = true
			http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: id, Path: "/"})
			reply("true")
			return
		}
		cookie, err := r.Cookie("_session_id")
		if err != nil || !f.sessions[cookie.Value] {
			fail(1, "Not authenticated")
			return
		}
		switch call.Method {
		case "web.connected":
			reply(fmt.Sprint(f.connected))
		case "web.get_hosts":
			reply(f.hosts)
		case "web.connect":
			f.connected = true
			reply(`["core.get_torrents_status"]`)
		case "daemon.get_version":
			if f.noGetVersion || !f.connected {
				fail(2, "Unknown method")
				return
			}
			reply(`"` + f.version + `"`)
		case "daemon.info":
			if !f.connected {
				fail(2, "Unknown method")
				return
			}
			reply(`"` + f.version + `"`)
		case "core.get_torrents_status":
			reply(f.torrents)
		case "core.get_session_status":
			reply(f.sessionStatus)
		case "core.pause_torrent", "core.resume_torrent":
			reply("null")
		case "core.remove_torrent":
			reply(f.removeResult)
		default:
			fail(2, "Unknown method")
		}
	}
}

func (f *fakeDeluge) methods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, c.Method)
	}
	return out
}

func (f *fakeDeluge) callsOf(method string) []rpcCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []rpcCall
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func count(methods []string, method string) int {
	n := 0
	for _, m := range methods {
		if m == method {
			n++
		}
	}
	return n
}

func serve(t *testing.T, f *fakeDeluge) *Client {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return NewClient(srv.URL+"/", f.password)
}

// TestLoginOnceAndSessionReuse pins the session flow: one auth.login, the
// cookie carried on every later call, and no second login while the
// session is valid.
func TestLoginOnceAndSessionReuse(t *testing.T) {
	f := newFake(t)
	c := serve(t, f)

	version, err := c.Version()
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version != "2.1.1" {
		t.Errorf("version = %q, want 2.1.1", version)
	}
	stats, err := c.GetSessionStatus()
	if err != nil {
		t.Fatalf("GetSessionStatus: %v", err)
	}
	if stats.DownloadRate != 1500 || stats.UploadRate != 20 {
		t.Errorf("stats = %+v, want 1500/20", stats)
	}
	got := f.methods()
	want := []string{"auth.login", "web.connected", "daemon.get_version", "core.get_session_status"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("methods = %v, want %v", got, want)
	}
}

// TestConnectsTheWebUIToItsLocalDaemon covers a web UI that starts out
// disconnected: the client reads the Connection Manager, prefers the local
// daemon over the first entry, and connects before any daemon call.
func TestConnectsTheWebUIToItsLocalDaemon(t *testing.T) {
	f := newFake(t)
	f.connected = false
	f.hosts = `[["remote-id","seedbox.example",58846,"localclient"],["local-id","127.0.0.1",58846,"localclient"]]`
	c := serve(t, f)

	if _, err := c.Version(); err != nil {
		t.Fatalf("Version: %v", err)
	}
	got := f.methods()
	want := []string{"auth.login", "web.connected", "web.get_hosts", "web.connect", "daemon.get_version"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("methods = %v, want %v", got, want)
	}
	connects := f.callsOf("web.connect")
	if len(connects) != 1 || string(connects[0].Params[0]) != `"local-id"` {
		t.Errorf("web.connect params = %+v, want the 127.0.0.1 host id", connects)
	}
	// The connection is remembered: a later call needs no connect step.
	if _, err := c.GetTorrents(); err != nil {
		t.Fatalf("GetTorrents: %v", err)
	}
	if n := count(f.methods(), "web.connect"); n != 1 {
		t.Errorf("web.connect calls = %d, want 1", n)
	}
}

func TestSoleRemoteDaemonIsUsed(t *testing.T) {
	f := newFake(t)
	f.connected = false
	f.hosts = `[["a-id","10.0.0.5",58846,"Offline"]]`
	c := serve(t, f)
	if _, err := c.Version(); err != nil {
		t.Fatalf("Version: %v", err)
	}
	connects := f.callsOf("web.connect")
	if len(connects) != 1 || string(connects[0].Params[0]) != `"a-id"` {
		t.Errorf("web.connect params = %+v, want the only host", connects)
	}
}

// TestSeveralRemoteDaemonsAreNotGuessed: the web UI keeps whatever daemon it
// is connected to, so picking one of several remote daemons would redirect
// the admin's own web UI. The admin chooses in the Connection Manager.
func TestSeveralRemoteDaemonsAreNotGuessed(t *testing.T) {
	f := newFake(t)
	f.connected = false
	f.hosts = `[["a-id","10.0.0.5",58846,"Offline"],["b-id","10.0.0.6",58846,"Offline"]]`
	c := serve(t, f)
	_, err := c.Version()
	if err == nil || !strings.Contains(err.Error(), "2 remote daemons") || !strings.Contains(err.Error(), "Connection Manager") {
		t.Fatalf("err = %v, want a refusal naming the Connection Manager", err)
	}
	if n := count(f.methods(), "web.connect"); n != 0 {
		t.Errorf("web.connect calls = %d, want 0", n)
	}
}

func TestNoDaemonInConnectionManagerIsExplained(t *testing.T) {
	f := newFake(t)
	f.connected = false
	f.hosts = `[]`
	c := serve(t, f)
	_, err := c.Version()
	if err == nil || !strings.Contains(err.Error(), "Connection Manager") {
		t.Fatalf("err = %v, want the Connection Manager explanation", err)
	}
}

// TestExpiredSessionLogsInAgainOnce pins the recovery: a "Not authenticated"
// answer costs exactly one new login and the call is retried once.
func TestExpiredSessionLogsInAgainOnce(t *testing.T) {
	f := newFake(t)
	c := serve(t, f)
	if _, err := c.Version(); err != nil {
		t.Fatalf("Version: %v", err)
	}
	f.mu.Lock()
	f.expireAll = true
	f.mu.Unlock()

	stats, err := c.GetSessionStatus()
	if err != nil {
		t.Fatalf("GetSessionStatus after expiry: %v", err)
	}
	if stats.DownloadRate != 1500 {
		t.Errorf("DownloadRate = %d, want 1500", stats.DownloadRate)
	}
	got := f.methods()
	if n := count(got, "auth.login"); n != 2 {
		t.Errorf("auth.login calls = %d, want 2: %v", n, got)
	}
	if n := count(got, "core.get_session_status"); n != 2 {
		t.Errorf("core.get_session_status calls = %d, want 2 (one refused, one retried): %v", n, got)
	}
}

// TestLostDaemonConnectionIsRecovered: the web UI answers "Unknown method"
// for daemon methods once it loses the daemon; the client reconnects and
// retries once.
func TestLostDaemonConnectionIsRecovered(t *testing.T) {
	f := newFake(t)
	c := serve(t, f)
	if _, err := c.Version(); err != nil {
		t.Fatalf("Version: %v", err)
	}
	f.mu.Lock()
	f.connected = false
	f.hosts = `[["local-id","127.0.0.1",58846,"localclient"]]`
	f.mu.Unlock()

	if _, err := c.Version(); err != nil {
		t.Fatalf("Version after daemon loss: %v", err)
	}
	got := f.methods()
	if n := count(got, "web.connect"); n != 1 {
		t.Errorf("web.connect calls = %d, want 1: %v", n, got)
	}
}

func TestVersionFallsBackToDaemonInfoOnDeluge13(t *testing.T) {
	f := newFake(t)
	f.noGetVersion = true
	f.version = "1.3.15"
	c := serve(t, f)
	version, err := c.Version()
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version != "1.3.15" {
		t.Errorf("version = %q, want 1.3.15", version)
	}
	if n := count(f.methods(), "daemon.info"); n != 1 {
		t.Errorf("daemon.info calls = %d, want 1: %v", n, f.methods())
	}
}

func TestWrongPasswordIsNeverEchoed(t *testing.T) {
	f := newFake(t)
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "wrong-secret")
	_, err := c.Version()
	if err == nil || !strings.Contains(err.Error(), "invalid password") {
		t.Fatalf("err = %v, want invalid password", err)
	}
	if strings.Contains(err.Error(), "wrong-secret") {
		t.Errorf("error echoes the password: %v", err)
	}
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://elsewhere.example/json", http.StatusMovedPermanently)
	}))
	t.Cleanup(srv.Close)
	_, err := NewClient(srv.URL, "pw").Version()
	if err == nil || !strings.Contains(err.Error(), "redirect status 301") || !strings.Contains(err.Error(), "elsewhere.example") {
		t.Fatalf("err = %v, want a redirect error naming the location", err)
	}
}

func TestNotFoundExplainsURLShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	_, err := NewClient(srv.URL, "pw").Version()
	if err == nil || !strings.Contains(err.Error(), "appends /json") {
		t.Fatalf("err = %v, want the /json hint", err)
	}
}

func TestWebPageInsteadOfJSONIsExplained(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<!DOCTYPE html><html><body>Deluge</body></html>")
	}))
	t.Cleanup(srv.Close)
	_, err := NewClient(srv.URL, "pw").Version()
	if err == nil || !strings.Contains(err.Error(), "web page instead of JSON") {
		t.Fatalf("err = %v, want the web page hint", err)
	}
}

func TestPauseAndResumeSendListsAndSkipEmpty(t *testing.T) {
	f := newFake(t)
	c := serve(t, f)
	if err := c.PauseTorrents(nil); err != nil {
		t.Fatalf("PauseTorrents(nil): %v", err)
	}
	if err := c.ResumeTorrents([]string{}); err != nil {
		t.Fatalf("ResumeTorrents(empty): %v", err)
	}
	if len(f.methods()) != 0 {
		t.Fatalf("empty lists must send nothing, got %v", f.methods())
	}
	if err := c.PauseTorrents([]string{"aa", "bb"}); err != nil {
		t.Fatalf("PauseTorrents: %v", err)
	}
	if err := c.ResumeTorrents([]string{"aa"}); err != nil {
		t.Fatalf("ResumeTorrents: %v", err)
	}
	pauses := f.callsOf("core.pause_torrent")
	if len(pauses) != 1 || len(pauses[0].Params) != 1 || string(pauses[0].Params[0]) != `["aa","bb"]` {
		t.Errorf("pause params = %+v, want one list param", pauses)
	}
	resumes := f.callsOf("core.resume_torrent")
	if len(resumes) != 1 || string(resumes[0].Params[0]) != `["aa"]` {
		t.Errorf("resume params = %+v, want one list param", resumes)
	}
}

func TestRemoveSendsHashAndFlag(t *testing.T) {
	f := newFake(t)
	c := serve(t, f)
	if err := c.RemoveTorrent("abc", true); err != nil {
		t.Fatalf("RemoveTorrent: %v", err)
	}
	removes := f.callsOf("core.remove_torrent")
	if len(removes) != 1 || len(removes[0].Params) != 2 ||
		string(removes[0].Params[0]) != `"abc"` || string(removes[0].Params[1]) != `true` {
		t.Errorf("remove params = %+v, want [\"abc\", true]", removes)
	}
	if err := c.RemoveTorrent("", false); err == nil {
		t.Error("RemoveTorrent(\"\") must be refused")
	}
	f.mu.Lock()
	f.removeResult = "false"
	f.mu.Unlock()
	if err := c.RemoveTorrent("abc", false); err == nil || !strings.Contains(err.Error(), "did not remove") {
		t.Errorf("err = %v, want a refusal error", err)
	}
}

func TestGetTorrentsParsesMixedNumbersAndOptionalKeys(t *testing.T) {
	f := newFake(t)
	f.torrents = `{
		"bbb": {"name":"Old","state":"Seeding","progress":100.0,"total_size":10,"total_done":10,
		        "download_payload_rate":0,"eta":0,"is_finished":true,"message":"OK","time_added":1700000000.5,
		        "completed_time":1700003600,"label":"tv"},
		"aaa": {"name":"New","state":"Downloading","progress":12.5,"total_size":4000000000,"total_done":500000000,
		        "download_payload_rate":123456.7,"eta":28350.0,"is_finished":false,"message":"OK","time_added":1700009000}
	}`
	c := serve(t, f)
	torrents, err := c.GetTorrents()
	if err != nil {
		t.Fatalf("GetTorrents: %v", err)
	}
	// Ordered by time_added, so the later-added "aaa" comes second despite its hash.
	if len(torrents) != 2 || torrents[0].Hash != "bbb" || torrents[1].Hash != "aaa" {
		t.Fatalf("torrents = %+v, want bbb (added first) then aaa", torrents)
	}
	a := torrents[1]
	if a.Name != "New" || a.State != "Downloading" || a.Progress != 12.5 || a.TotalSize != 4000000000 ||
		a.TotalDone != 500000000 || a.DownloadRate != 123456 || a.ETA != 28350 || a.IsFinished ||
		a.Label != "" || a.CompletedTime != 0 {
		t.Errorf("torrent aaa = %+v", a)
	}
	b := torrents[0]
	if !b.IsFinished || b.CompletedTime != 1700003600 || b.Label != "tv" || b.TimeAdded != 1700000000.5 {
		t.Errorf("torrent bbb = %+v", b)
	}
	statusCalls := f.callsOf("core.get_torrents_status")
	if len(statusCalls) != 1 || string(statusCalls[0].Params[0]) != `{}` ||
		!strings.Contains(string(statusCalls[0].Params[1]), `"label"`) {
		t.Errorf("get_torrents_status params = %+v, want an empty filter and the key list", statusCalls)
	}
}

func TestRPCErrorNamesMethodAndMessage(t *testing.T) {
	f := newFake(t)
	c := serve(t, f)
	if _, err := c.Version(); err != nil {
		t.Fatalf("Version: %v", err)
	}
	// An unknown-to-the-fake method is answered with code 2 forever; the
	// client reconnects once, retries once, then gives up with the message.
	err := c.call("core.does_not_exist", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "deluge core.does_not_exist: Unknown method") {
		t.Fatalf("err = %v, want the method and Deluge's message", err)
	}
	if n := count(f.methods(), "core.does_not_exist"); n != 2 {
		t.Errorf("attempts = %d, want exactly 2", n)
	}
}
