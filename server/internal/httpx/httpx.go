// Package httpx owns the server's two outbound transport classes and the
// admin-configured outbound proxy.
//
// Internet-bound traffic -- TMDB, Trakt, hosted AI providers, plex.tv, the
// GitHub update check, the push relay -- rides External, which honours the
// admin's outbound proxy and, when none is set, Go's default transport with
// its standard HTTP_PROXY/HTTPS_PROXY/NO_PROXY handling. Cluster-internal
// traffic -- arr instances, download clients, Jellyfin/Emby, Tautulli/Tracearr,
// webhook installs, the Local AI provider -- rides Internal, which never uses a
// proxy: a VPN-tunnelled proxy cannot reach the LAN, and an env-var proxy with
// an incomplete NO_PROXY used to swallow arr calls and surface as timeouts that
// looked like unrelated bugs (#501).
//
// Every http.Client literal in the server names one of the two in its
// Transport field; classification_test.go fails any that does not.
package httpx

import (
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
)

// proxied pairs the admin proxy with the transport that dials through it.
type proxied struct {
	url       *url.URL
	transport *http.Transport
}

var (
	current atomic.Pointer[proxied]
	// setMu serialises the setters so two concurrent saves cannot interleave
	// the compare, the swap, and the idle-connection close.
	setMu sync.Mutex

	// internal is cloned once from the default transport; only Proxy differs.
	internal = func() *http.Transport {
		t := baseTransport()
		t.Proxy = nil
		return t
	}()
)

// SupportedProxyScheme reports whether http.Transport can dial through a
// proxy of this scheme. http.ProxyURL itself validates nothing -- an unknown
// scheme would silently be treated as an HTTP proxy -- so every entry point
// checks here first.
func SupportedProxyScheme(scheme string) bool {
	switch scheme {
	case "http", "https", "socks5", "socks5h":
		return true
	}
	return false
}

// SetOutboundProxy installs u as the proxy for internet-bound traffic, or
// clears it when u is nil. Setting the same URL again is a no-op; a change
// swaps the proxied transport and closes the old one's idle connections so
// nothing keeps talking to a proxy the admin just moved away from.
func SetOutboundProxy(u *url.URL) {
	setMu.Lock()
	defer setMu.Unlock()
	old := current.Load()
	if u == nil {
		if old == nil {
			return
		}
		current.Store(nil)
		old.transport.CloseIdleConnections()
		return
	}
	if old != nil && old.url.String() == u.String() {
		return
	}
	copied := *u
	current.Store(&proxied{url: &copied, transport: newProxyTransport(&copied)})
	if old != nil {
		old.transport.CloseIdleConnections()
	}
}

// SetOutboundProxyString parses and installs a proxy URL; "" clears it. The
// URL may carry userinfo (the settings layer composes it from the write-only
// password); the scheme must be one http.Transport understands.
func SetOutboundProxyString(raw string) error {
	if raw == "" {
		SetOutboundProxy(nil)
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("outbound proxy: %w", err)
	}
	if !SupportedProxyScheme(u.Scheme) || u.Host == "" {
		return fmt.Errorf("outbound proxy: %s is not an http, https, socks5, or socks5h URL with a host", u.Redacted())
	}
	SetOutboundProxy(u)
	return nil
}

// OutboundProxy returns a copy of the admin-configured proxy, nil when unset.
func OutboundProxy() *url.URL {
	p := current.Load()
	if p == nil {
		return nil
	}
	copied := *p.url
	return &copied
}

// OutboundProxyString is OutboundProxy in the form a subprocess environment
// wants (HTTPS_PROXY=...), "" when unset.
func OutboundProxyString() string {
	if u := OutboundProxy(); u != nil {
		return u.String()
	}
	return ""
}

// external is the internet-bound RoundTripper. It resolves the proxy per
// request, so long-lived clients follow a settings change without a rebuild,
// and it delegates to http.DefaultTransport when no admin proxy is set: that
// keeps Go's env-var proxy handling exactly as it was, and it is what lets the
// test harnesses that swap http.DefaultTransport keep intercepting TMDB and
// Trakt traffic.
type external struct{}

func (external) RoundTrip(req *http.Request) (*http.Response, error) {
	if p := current.Load(); p != nil {
		return p.transport.RoundTrip(req)
	}
	return http.DefaultTransport.RoundTrip(req)
}

// External returns the RoundTripper for internet-bound clients.
func External() http.RoundTripper { return external{} }

// ExternalTransport returns the *http.Transport External currently delegates
// to -- the proxied clone, or http.DefaultTransport itself -- for callers that
// must tune transport fields. Clone it before mutating, and rebuild the clone
// per request or turn, because a clone pins the proxy as of construction. It
// is nil when a test has replaced http.DefaultTransport with something else;
// callers then fall back to External().
func ExternalTransport() *http.Transport {
	if p := current.Load(); p != nil {
		return p.transport
	}
	t, _ := http.DefaultTransport.(*http.Transport)
	return t
}

// Internal returns the shared transport for cluster-internal clients. Its
// Proxy is nil unconditionally: neither the admin setting nor an environment
// variable ever routes LAN traffic through a proxy.
func Internal() *http.Transport { return internal }

// ProxyTransport returns a fresh transport that dials through u, for testing
// a candidate proxy without touching the installed one. Keep-alives are off
// so the probe leaves no pooled connection behind; callers still
// CloseIdleConnections when done.
func ProxyTransport(u *url.URL) *http.Transport {
	t := newProxyTransport(u)
	t.DisableKeepAlives = true
	return t
}

func newProxyTransport(u *url.URL) *http.Transport {
	t := baseTransport()
	t.Proxy = http.ProxyURL(u)
	return t
}

// baseTransport clones http.DefaultTransport so every class shares its dial,
// TLS, HTTP/2, and idle-pool settings; only Proxy ever differs.
func baseTransport() *http.Transport {
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		return t.Clone()
	}
	return &http.Transport{}
}
