// Package httpxtest provides an in-process fake forward proxy, so a test can
// prove which class of traffic rides the outbound proxy without a real one.
package httpxtest

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

// Hit is one request the fake proxy received.
type Hit struct {
	// Target is the absolute-form request target (http://host/path?query),
	// which is how an HTTP client addresses an origin when it talks to a proxy.
	Target string
	Method string
	// ProxyAuthorization is the credential header the client attached, "" when
	// the proxy URL carried no userinfo.
	ProxyAuthorization string
}

// Proxy is a fake forward proxy for plain-http targets. It never dials the
// target: it records the request and answers with the configured status and
// body, so the proof that a request went through it needs no upstream at all.
// CONNECT (an https target) is refused with 502; tests point clients at http://
// targets on purpose.
type Proxy struct {
	// Server is the listening proxy; URL() is the address to configure.
	Server *httptest.Server

	mu     sync.Mutex
	hits   []Hit
	status int
	body   string
}

// New starts a fake proxy that answers 200 {} until SetResponse says
// otherwise, closed when the test ends.
func New(t testing.TB) *Proxy {
	t.Helper()
	p := &Proxy{status: http.StatusOK, body: "{}"}
	p.Server = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.Server.Close)
	return p
}

func (p *Proxy) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		http.Error(w, "fake proxy: CONNECT is not supported; use an http:// target", http.StatusBadGateway)
		return
	}
	p.mu.Lock()
	p.hits = append(p.hits, Hit{
		Target:             r.RequestURI,
		Method:             r.Method,
		ProxyAuthorization: r.Header.Get("Proxy-Authorization"),
	})
	status, body := p.status, p.body
	p.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// SetResponse changes what every subsequent request is answered with.
func (p *Proxy) SetResponse(status int, body string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status, p.body = status, body
}

// URL is the proxy address a client is configured with.
func (p *Proxy) URL() *url.URL {
	u, err := url.Parse(p.Server.URL)
	if err != nil {
		panic(err)
	}
	return u
}

// URLWithCredentials is URL carrying user:pass, for proving that the client
// attaches Proxy-Authorization.
func (p *Proxy) URLWithCredentials(user, pass string) *url.URL {
	u := p.URL()
	u.User = url.UserPassword(user, pass)
	return u
}

// Hits returns a copy of every request received so far, in order.
func (p *Proxy) Hits() []Hit {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Hit(nil), p.hits...)
}

// BasicAuth is the Proxy-Authorization value a client sends for user:pass.
func BasicAuth(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}
