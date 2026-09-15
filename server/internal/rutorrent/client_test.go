package rutorrent

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
)

var methodRe = regexp.MustCompile(`<methodName>([^<]+)</methodName>`)

// fakeRutorrent serves ruTorrent's httprpc endpoint: raw XML-RPC reads with
// canned rTorrent answers, and the form command protocol.
type fakeRutorrent struct {
	t        *testing.T
	version  string
	rows     string // XML for the d.multicall2 result array
	commands string // body answered to form commands
	user     string // when set, Basic auth is required
	pass     string

	mu     sync.Mutex
	calls  []string // XML-RPC method names, or "form:<body>"
	auths  []string // Authorization header of every request
	bodies []string // every XML-RPC request body
}

func newFake(t *testing.T) *fakeRutorrent {
	return &fakeRutorrent{t: t, version: "0.16.21", commands: `[0]`}
}

func (f *fakeRutorrent) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.auths = append(f.auths, r.Header.Get("Authorization"))
		f.mu.Unlock()
		if f.user != "" {
			u, p, ok := r.BasicAuth()
			if !ok || u != f.user || p != f.pass {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		if r.URL.Path != "/plugins/httprpc/action.php" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
			f.mu.Lock()
			f.calls = append(f.calls, "form:"+string(body))
			answer := f.commands
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, answer)
			return
		}
		method := ""
		if m := methodRe.FindStringSubmatch(string(body)); m != nil {
			method = m[1]
		}
		f.mu.Lock()
		f.calls = append(f.calls, method)
		f.bodies = append(f.bodies, string(body))
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/xml")
		switch method {
		case "system.client_version":
			_, _ = io.WriteString(w, `<?xml version="1.0"?><methodResponse><params><param><value><string>`+f.version+`</string></value></param></params></methodResponse>`)
		case "throttle.global_down.rate":
			_, _ = io.WriteString(w, `<?xml version="1.0"?><methodResponse><params><param><value><i8>123456</i8></value></param></params></methodResponse>`)
		case "d.multicall2":
			_, _ = io.WriteString(w, `<?xml version="1.0"?><methodResponse><params><param><value><array><data>`+f.rows+`</data></array></value></param></params></methodResponse>`)
		default:
			_, _ = io.WriteString(w, `<?xml version="1.0"?><methodResponse><fault><value><struct><member><name>faultCode</name><value><i4>-507</i4></value></member><member><name>faultString</name><value><string>Command "`+method+`" is not allowed for untrusted connections.</string></value></member></struct></value></fault></methodResponse>`)
		}
	}
}

func (f *fakeRutorrent) methods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func serve(t *testing.T, f *fakeRutorrent, user, pass string) *Client {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return NewClient(srv.URL+"/", user, pass)
}

func row(hash, name, label string, size, left, done, rate int64, open, active, state, complete, hashing int, message string, finished int64, addtime string) string {
	return fmt.Sprintf(`<value><array><data>`+
		`<value><string>%s</string></value><value><string>%s</string></value><value><string>%s</string></value>`+
		`<value><i8>%d</i8></value><value><i8>%d</i8></value><value><i8>%d</i8></value><value><i8>%d</i8></value>`+
		`<value><i8>%d</i8></value><value><i8>%d</i8></value><value><i8>%d</i8></value><value><i8>%d</i8></value><value><i8>%d</i8></value>`+
		`<value><string>%s</string></value><value><i8>%d</i8></value><value><string>%s</string></value>`+
		`</data></array></value>`, hash, name, label, size, left, done, rate, open, active, state, complete, hashing, message, finished, addtime)
}

func TestVersionAndBasicAuth(t *testing.T) {
	f := newFake(t)
	f.user, f.pass = "web-user", "web-secret"
	c := serve(t, f, "web-user", "web-secret")
	version, err := c.Version()
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version != "0.16.21" {
		t.Errorf("version = %q, want 0.16.21", version)
	}
	if got := f.methods(); len(got) != 1 || got[0] != "system.client_version" {
		t.Errorf("methods = %v, want [system.client_version]", got)
	}
	if !strings.HasPrefix(f.auths[0], "Basic ") {
		t.Errorf("Authorization = %q, want Basic", f.auths[0])
	}

	wrong := serve(t, f, "web-user", "nope-secret")
	_, err = wrong.Version()
	if err == nil || !strings.Contains(err.Error(), "refused the credentials") || strings.Contains(err.Error(), "nope-secret") {
		t.Fatalf("err = %v, want a credentials refusal that never echoes the password", err)
	}
}

func TestNoCredentialsSendsNoAuthorization(t *testing.T) {
	f := newFake(t)
	c := serve(t, f, "", "")
	if _, err := c.Version(); err != nil {
		t.Fatalf("Version: %v", err)
	}
	if f.auths[0] != "" {
		t.Errorf("Authorization = %q, want none", f.auths[0])
	}
}

// TestGetTorrentsParsesMulticallRows also pins the request shape: rTorrent
// takes every column command as its own parameter, never one array.
func TestGetTorrentsParsesMulticallRows(t *testing.T) {
	f := newFake(t)
	f.rows = row("ABC123", "Fedora", "linux", 4000000000, 500000000, 3500000000, 250000, 1, 1, 1, 0, 0, "", 0, "1788556982\n") +
		row("DEF456", "Done", "", 10, 0, 10, 0, 1, 1, 1, 1, 0, "Tracker: [Timeout was reached]", 1788556990, "")
	c := serve(t, f, "", "")
	torrents, err := c.GetTorrents()
	if err != nil {
		t.Fatalf("GetTorrents: %v", err)
	}
	if len(torrents) != 2 {
		t.Fatalf("torrents = %d, want 2", len(torrents))
	}
	a := torrents[0]
	if a.Hash != "ABC123" || a.Name != "Fedora" || a.Label != "linux" || a.SizeBytes != 4000000000 || a.LeftBytes != 500000000 ||
		a.CompletedBytes != 3500000000 || a.DownRate != 250000 || !a.IsOpen || !a.IsActive || a.State != 1 || a.Complete ||
		a.Hashing != 0 || a.AddedAt != 1788556982 {
		t.Errorf("torrent 0 = %+v", a)
	}
	b := torrents[1]
	if !b.Complete || b.FinishedAt != 1788556990 || b.Message != "Tracker: [Timeout was reached]" || b.AddedAt != 0 || b.Label != "" {
		t.Errorf("torrent 1 = %+v", b)
	}
	body := f.bodies[0]
	if !strings.Contains(body, `<methodName>d.multicall2</methodName>`) ||
		!strings.Contains(body, `<param><value><string></string></value></param><param><value><string>main</string></value></param>`) ||
		!strings.Contains(body, `</param><param><value><string>d.hash=</string></value></param><param><value><string>d.name=</string></value></param>`) ||
		!strings.Contains(body, `<param><value><string>d.custom=addtime</string></value></param>`) || strings.Contains(body, "<array>") {
		t.Errorf("multicall body = %s", body)
	}
}

func TestMutationsUseRutorrentFormCommands(t *testing.T) {
	f := newFake(t)
	c := serve(t, f, "", "")
	if err := c.Pause("ABC123"); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if err := c.Resume("ABC123", "DEF456"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if err := c.Erase("ABC123"); err != nil {
		t.Fatalf("Erase: %v", err)
	}
	if err := c.EraseWithData("ABC123"); err != nil {
		t.Fatalf("EraseWithData: %v", err)
	}
	want := []string{
		"form:hash=ABC123&mode=pause",
		"form:hash=ABC123&hash=DEF456&mode=start",
		"form:hash=ABC123&mode=remove",
		"form:hash=ABC123&mode=removewithdata&v=1",
	}
	if got := f.methods(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if err := c.Pause(); err == nil {
		t.Error("Pause with no hashes was accepted")
	}
	if err := c.Erase(""); err == nil {
		t.Error("an empty hash was accepted")
	}
}

func TestFormCommandRefusalsAreExplained(t *testing.T) {
	f := newFake(t)
	c := serve(t, f, "", "")
	f.commands = "false"
	err := c.EraseWithData("ABC123")
	if err == nil || !strings.Contains(err.Error(), "kept the torrent") || !strings.Contains(err.Error(), "open ruTorrent once") {
		t.Fatalf("err = %v, want the kept-torrent explanation", err)
	}
	if err := c.Resume("ABC123"); err == nil || !strings.Contains(err.Error(), "rTorrent refused the command") {
		t.Fatalf("err = %v, want a refusal", err)
	}
	f.commands = "<html>not json</html>"
	if err := c.Pause("ABC123"); err == nil || !strings.Contains(err.Error(), "other than its JSON result") {
		t.Fatalf("err = %v, want the JSON hint", err)
	}
}

func TestUntrustedFaultSurfacesTheMessage(t *testing.T) {
	f := newFake(t)
	c := serve(t, f, "", "")
	_, err := c.call("d.start", "ABC")
	if err == nil || !strings.Contains(err.Error(), "not allowed for untrusted connections") {
		t.Fatalf("err = %v, want rTorrent's fault string", err)
	}
}

func TestRtorrentDownIsNamed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "Could not reach rTorrent over XMLRPC. Is rTorrent running?")
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "", "")
	if _, err := c.Version(); err == nil || !strings.Contains(err.Error(), "Is rTorrent running?") {
		t.Fatalf("read err = %v, want ruTorrent's own text", err)
	}
	if err := c.Pause("ABC"); err == nil || !strings.Contains(err.Error(), "Is rTorrent running?") {
		t.Fatalf("command err = %v, want ruTorrent's own text", err)
	}
}

func TestWrongURLShapesAreExplained(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<!DOCTYPE html><html><body>ruTorrent</body></html>")
	}))
	t.Cleanup(page.Close)
	_, err := NewClient(page.URL, "", "").Version()
	if err == nil || !strings.Contains(err.Error(), "other than XML-RPC") {
		t.Fatalf("html err = %v", err)
	}

	missing := httptest.NewServer(http.HandlerFunc(http.NotFound))
	t.Cleanup(missing.Close)
	_, err = NewClient(missing.URL, "", "").Version()
	if err == nil || !strings.Contains(err.Error(), "enter the ruTorrent address") {
		t.Fatalf("404 err = %v", err)
	}

	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://elsewhere.example/rutorrent/", http.StatusMovedPermanently)
	}))
	t.Cleanup(redirect.Close)
	_, err = NewClient(redirect.URL, "", "").Version()
	if err == nil || !strings.Contains(err.Error(), "redirect status 301") || !strings.Contains(err.Error(), "elsewhere.example") {
		t.Fatalf("redirect err = %v", err)
	}
}

func TestGlobalDownRate(t *testing.T) {
	f := newFake(t)
	c := serve(t, f, "", "")
	rate, err := c.GlobalDownRate()
	if err != nil || rate != 123456 {
		t.Fatalf("GlobalDownRate = %d, %v", rate, err)
	}
}
