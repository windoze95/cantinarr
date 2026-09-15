package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/httpx"
	"github.com/windoze95/cantinarr-server/internal/httpx/httpxtest"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"github.com/windoze95/cantinarr-server/internal/serversettings"
)

// newOutboundProxyEnv builds the handlers over a real, cipher-backed settings
// service and a registry whose TMDB client points at a plain-http fake root,
// so the probe reaches a fake proxy in absolute form (no CONNECT). Authorization
// is covered by the RBAC route sweep; these tests cover the payload contract.
func newOutboundProxyEnv(t *testing.T) (*serversettings.Service, *credentials.Registry) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	creds := credentials.NewRegistry(database, cipher,
		credentials.WithDefaultTMDBToken("test-token"),
		credentials.WithTMDBBaseURL("http://tmdb.test/3"),
	)
	settings := serversettings.NewService(database, func() bool { return false }, serversettings.WithCipher(cipher))
	t.Cleanup(func() { httpx.SetOutboundProxy(nil) })
	return settings, creds
}

func decodeOutboundProxy(t *testing.T, body string) outboundProxyResponse {
	t.Helper()
	var out outboundProxyResponse
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode outbound proxy: %v (body = %s)", err, body)
	}
	return out
}

func putOutboundProxy(settings *serversettings.Service, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	updateOutboundProxyHandler(settings)(rec, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body)))
	return rec
}

func getOutboundProxy(settings *serversettings.Service) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	outboundProxyHandler(settings)(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec
}

func testOutboundProxy(settings *serversettings.Service, creds *credentials.Registry, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	testOutboundProxyHandler(settings, creds)(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
	return rec
}

// TestOutboundProxyRoundTrip pins the GET/PUT contract: the password never
// comes back, a save installs the proxy at once, a blank password keeps the
// stored one, and an empty address clears everything.
func TestOutboundProxyRoundTrip(t *testing.T) {
	settings, _ := newOutboundProxyEnv(t)

	rec := getOutboundProxy(settings)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := decodeOutboundProxy(t, rec.Body.String()); got.URL != "" || got.HasPassword {
		t.Fatalf("fresh server = %+v, want unset", got)
	}

	rec = putOutboundProxy(settings, `{"url":"http://proxy.test:8118/","username":"alice","password":"secretpw"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secretpw") {
		t.Fatalf("the password leaked into the PUT reply: %s", rec.Body.String())
	}
	saved := decodeOutboundProxy(t, rec.Body.String())
	if saved.URL != "http://proxy.test:8118" || saved.Username != "alice" || !saved.HasPassword {
		t.Fatalf("saved = %+v", saved)
	}
	if got := httpx.OutboundProxyString(); got != "http://alice:secretpw@proxy.test:8118" {
		t.Fatalf("installed proxy = %q, want the credential-bearing URL", got)
	}

	rec = getOutboundProxy(settings)
	if strings.Contains(rec.Body.String(), "secretpw") {
		t.Fatalf("the password leaked into GET: %s", rec.Body.String())
	}
	if reread := decodeOutboundProxy(t, rec.Body.String()); reread != saved {
		t.Fatalf("GET = %+v, want %+v", reread, saved)
	}

	rec = putOutboundProxy(settings, `{"url":"http://proxy.test:8118","username":"alice","password":""}`)
	if rec.Code != http.StatusOK || !decodeOutboundProxy(t, rec.Body.String()).HasPassword {
		t.Fatalf("a blank password should keep the stored one: %d %s", rec.Code, rec.Body.String())
	}
	if got := httpx.OutboundProxyString(); got != "http://alice:secretpw@proxy.test:8118" {
		t.Fatalf("installed proxy after a blank-password save = %q", got)
	}

	rec = putOutboundProxy(settings, `{"url":"","username":"alice","password":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if cleared := decodeOutboundProxy(t, rec.Body.String()); cleared.URL != "" || cleared.Username != "" || cleared.HasPassword {
		t.Fatalf("after clearing = %+v", cleared)
	}
	if httpx.OutboundProxy() != nil {
		t.Fatal("clearing must uninstall the proxy")
	}
}

func TestOutboundProxyRejectsBadInput(t *testing.T) {
	settings, _ := newOutboundProxyEnv(t)
	for _, body := range []string{
		`not json`,
		`{"url":"ftp://proxy:21"}`,
		`{"url":"proxy:8118"}`,
		`{"url":"http://proxy:8118/path"}`,
		`{"url":"http://alice:pw@proxy:8118"}`,
	} {
		rec := putOutboundProxy(settings, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("PUT %s: status = %d, want 400 (body %s)", body, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"error"`) {
			t.Errorf("PUT %s: body %s carries no error envelope", body, rec.Body.String())
		}
	}
	if rec := putOutboundProxy(settings, `{"url":"http://alice:pw@proxy:8118"}`); !strings.Contains(rec.Body.String(), "username and password fields") {
		t.Errorf("credentials in the address should point at the fields: %s", rec.Body.String())
	}
	if httpx.OutboundProxy() != nil {
		t.Fatal("a rejected save must not install anything")
	}
}

// TestOutboundProxyTestFetchesTMDBThroughCandidate: the Test button proves the
// typed proxy carries a TMDB request, credentials attached, and leaves the
// installed proxy alone.
func TestOutboundProxyTestFetchesTMDBThroughCandidate(t *testing.T) {
	settings, creds := newOutboundProxyEnv(t)
	installed := httpxtest.New(t)
	if rec := putOutboundProxy(settings, `{"url":"`+installed.Server.URL+`","username":"","password":""}`); rec.Code != http.StatusOK {
		t.Fatalf("install: %d %s", rec.Code, rec.Body.String())
	}
	candidate := httpxtest.New(t)

	rec := testOutboundProxy(settings, creds, `{"url":"`+candidate.Server.URL+`","username":"alice","password":"pw"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	hits := candidate.Hits()
	if len(hits) != 1 {
		t.Fatalf("candidate hits = %d, want 1", len(hits))
	}
	if hits[0].Target != "http://tmdb.test/3/configuration" {
		t.Errorf("target = %q, want TMDB's configuration endpoint in absolute form", hits[0].Target)
	}
	if want := httpxtest.BasicAuth("alice", "pw"); hits[0].ProxyAuthorization != want {
		t.Errorf("Proxy-Authorization = %q, want %q", hits[0].ProxyAuthorization, want)
	}
	if len(installed.Hits()) != 0 {
		t.Error("the test must not touch the installed proxy")
	}
	if got := httpx.OutboundProxyString(); got != installed.Server.URL {
		t.Errorf("installed proxy changed to %q", got)
	}
}

// TestOutboundProxyTestUsesStoredPasswordWhenBlank mirrors the save: retesting
// without retyping the password tests the real credentials.
func TestOutboundProxyTestUsesStoredPasswordWhenBlank(t *testing.T) {
	settings, creds := newOutboundProxyEnv(t)
	proxy := httpxtest.New(t)
	if rec := putOutboundProxy(settings, `{"url":"`+proxy.Server.URL+`","username":"alice","password":"stored-pw"}`); rec.Code != http.StatusOK {
		t.Fatalf("install: %d %s", rec.Code, rec.Body.String())
	}
	rec := testOutboundProxy(settings, creds, `{"url":"`+proxy.Server.URL+`","username":"alice","password":""}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	hits := proxy.Hits()
	if len(hits) != 1 || hits[0].ProxyAuthorization != httpxtest.BasicAuth("alice", "stored-pw") {
		t.Fatalf("hits = %+v, want one request carrying the stored password", hits)
	}
}

// TestOutboundProxyTestReportsFailures: an unreachable proxy is a 400 with the
// reason and no credentials; a proxy that works but a TMDB that objects is
// named as such; a blank address is refused.
func TestOutboundProxyTestReportsFailures(t *testing.T) {
	settings, creds := newOutboundProxyEnv(t)

	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()
	rec := testOutboundProxy(settings, creds, `{"url":"`+closedURL+`","username":"alice","password":"secretpw"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "proxy test failed") || strings.Contains(body, "secretpw") {
		t.Fatalf("body = %s, want the reason without the password", body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json so the app can decode the envelope", ct)
	}

	objecting := httpxtest.New(t)
	objecting.SetResponse(http.StatusUnauthorized, `{"status_message":"Invalid API key"}`)
	rec = testOutboundProxy(settings, creds, `{"url":"`+objecting.Server.URL+`"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "answered status 401") {
		t.Fatalf("status = %d, body = %s; want TMDB's own answer named", rec.Code, rec.Body.String())
	}

	rec = testOutboundProxy(settings, creds, `{"url":"","username":"","password":""}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "proxy address is required") {
		t.Fatalf("blank address: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = testOutboundProxy(settings, creds, `{"url":"http://alice:pw@proxy:8118"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("credentials in the address: status = %d", rec.Code)
	}
}

// TestOutboundProxyTestWithoutTMDB: a registry with no TMDB client cannot
// probe, and says so rather than answering as if the proxy were fine.
func TestOutboundProxyTestWithoutTMDB(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, _ := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	creds := credentials.NewRegistry(database, cipher)
	settings := serversettings.NewService(database, func() bool { return false })
	proxy := httpxtest.New(t)
	rec := testOutboundProxy(settings, creds, `{"url":"`+proxy.Server.URL+`"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "TMDB is not configured") {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(proxy.Hits()) != 0 {
		t.Error("nothing should be dialed without a TMDB client")
	}
}
