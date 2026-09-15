package httpx

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/httpx/httpxtest"
)

func resetProxy(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { SetOutboundProxy(nil) })
}

func get(t *testing.T, rt http.RoundTripper, target string) *http.Response {
	t.Helper()
	client := &http.Client{Transport: rt}
	resp, err := client.Get(target)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// TestExternalRidesAdminProxy pins the headline behaviour: once an admin proxy
// is installed, an internet-bound client sends its request to the proxy in
// absolute form, credentials attached, even for a host that does not resolve.
func TestExternalRidesAdminProxy(t *testing.T) {
	resetProxy(t)
	proxy := httpxtest.New(t)
	SetOutboundProxy(proxy.URLWithCredentials("admin", "s3cret"))

	resp := get(t, External(), "http://api.example.invalid/3/configuration?x=1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 from the fake proxy", resp.StatusCode)
	}
	hits := proxy.Hits()
	if len(hits) != 1 {
		t.Fatalf("proxy hits = %d, want 1", len(hits))
	}
	if hits[0].Target != "http://api.example.invalid/3/configuration?x=1" {
		t.Errorf("target = %q, want the absolute-form request", hits[0].Target)
	}
	if want := httpxtest.BasicAuth("admin", "s3cret"); hits[0].ProxyAuthorization != want {
		t.Errorf("Proxy-Authorization = %q, want %q", hits[0].ProxyAuthorization, want)
	}
}

type recordingTransport struct {
	hits int
}

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.hits++
	return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Request: req}, nil
}

// TestExternalDelegatesToDefaultTransportWhenUnset is the compatibility
// contract with the harnesses that swap http.DefaultTransport to intercept
// TMDB/Trakt: with no admin proxy, External consults DefaultTransport per
// request, so a swap made after the client was built still reaches it.
func TestExternalDelegatesToDefaultTransportWhenUnset(t *testing.T) {
	resetProxy(t)
	original := http.DefaultTransport
	recorder := &recordingTransport{}
	http.DefaultTransport = recorder
	t.Cleanup(func() { http.DefaultTransport = original })

	resp := get(t, External(), "http://api.example.invalid/anything")
	if resp.StatusCode != http.StatusNoContent || recorder.hits != 1 {
		t.Fatalf("status = %d, default-transport hits = %d; want the swapped transport to answer", resp.StatusCode, recorder.hits)
	}
	if ExternalTransport() != nil {
		t.Error("ExternalTransport() should be nil while DefaultTransport is not an *http.Transport")
	}
}

// TestInternalNeverRidesAdminProxy is issue #501's point 2: LAN traffic goes
// direct no matter what proxy is installed.
func TestInternalNeverRidesAdminProxy(t *testing.T) {
	resetProxy(t)
	proxy := httpxtest.New(t)
	SetOutboundProxy(proxy.URL())
	t.Setenv("HTTP_PROXY", proxy.Server.URL)

	var upstreamHits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHits++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	if Internal().Proxy != nil {
		t.Fatal("Internal().Proxy must be nil")
	}
	resp := get(t, Internal(), upstream.URL+"/api/v3/system/status")
	if resp.StatusCode != http.StatusOK || upstreamHits != 1 {
		t.Fatalf("status = %d, upstream hits = %d; want a direct hit", resp.StatusCode, upstreamHits)
	}
	if hits := proxy.Hits(); len(hits) != 0 {
		t.Fatalf("proxy saw %d request(s) from an internal client: %+v", len(hits), hits)
	}
}

// TestProxyTransportTestsTheCandidateOnly: the Test button must exercise the
// typed proxy, never the installed one, and must not leave pooled connections.
func TestProxyTransportTestsTheCandidateOnly(t *testing.T) {
	resetProxy(t)
	installed := httpxtest.New(t)
	candidate := httpxtest.New(t)
	SetOutboundProxy(installed.URL())

	transport := ProxyTransport(candidate.URL())
	defer transport.CloseIdleConnections()
	if !transport.DisableKeepAlives {
		t.Error("ProxyTransport should disable keep-alives")
	}
	resp := get(t, transport, "http://api.example.invalid/3/configuration")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(candidate.Hits()) != 1 || len(installed.Hits()) != 0 {
		t.Fatalf("candidate hits = %d, installed hits = %d; want 1 and 0", len(candidate.Hits()), len(installed.Hits()))
	}
}

func TestSetOutboundProxyStringValidation(t *testing.T) {
	resetProxy(t)
	for _, bad := range []string{"ftp://proxy:21", "http://", "proxy:8118", "://x"} {
		if err := SetOutboundProxyString(bad); err == nil {
			t.Errorf("SetOutboundProxyString(%q) = nil, want error", bad)
		}
	}
	if OutboundProxy() != nil {
		t.Fatal("a rejected value must not install anything")
	}
	if err := SetOutboundProxyString("socks5h://user:pw@proxy.local:1080"); err != nil {
		t.Fatalf("SetOutboundProxyString: %v", err)
	}
	if got := OutboundProxyString(); got != "socks5h://user:pw@proxy.local:1080" {
		t.Errorf("OutboundProxyString = %q", got)
	}
	if err := SetOutboundProxyString(""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if OutboundProxy() != nil || OutboundProxyString() != "" {
		t.Error("empty string must clear the proxy")
	}
}

// TestSetOutboundProxySameValueKeepsTransport: a save that changes nothing
// must not tear down the connection pool, and a real change must.
func TestSetOutboundProxySameValueKeepsTransport(t *testing.T) {
	resetProxy(t)
	a, _ := url.Parse("http://proxy.local:8118")
	SetOutboundProxy(a)
	first := ExternalTransport()
	if first == nil || first.Proxy == nil {
		t.Fatal("expected a proxied transport")
	}
	again, _ := url.Parse("http://proxy.local:8118")
	SetOutboundProxy(again)
	if ExternalTransport() != first {
		t.Error("re-setting the same URL rebuilt the transport")
	}
	b, _ := url.Parse("http://other.local:8118")
	SetOutboundProxy(b)
	if ExternalTransport() == first {
		t.Error("a changed URL kept the old transport")
	}
	if got := OutboundProxyString(); got != "http://other.local:8118" {
		t.Errorf("OutboundProxyString = %q", got)
	}
	// The copy handed out must not alias the installed one.
	OutboundProxy().Host = "mutated"
	if got := OutboundProxyString(); got != "http://other.local:8118" {
		t.Errorf("OutboundProxy() leaked a mutable alias: %q", got)
	}
}

func TestExternalTransportIsDefaultTransportWhenUnset(t *testing.T) {
	resetProxy(t)
	if ExternalTransport() != http.DefaultTransport.(*http.Transport) {
		t.Error("with no admin proxy, ExternalTransport must be http.DefaultTransport itself")
	}
}
